package params

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// dynamicResponder is like params_test.go's scriptedResponder, but the
// answer is computed per-request (PID *and* request data) instead of from a
// static PID->bytes map — needed for PARAMETER_DESCRIPTION (answer depends
// on which PID is being described) and SENSOR_DEFINITION/VALUE (answer
// depends on the requested sensor index).
func dynamicResponder(clock *session.FakeClock, ctrlRef *[]*session.RDMController, uid rdm.UID, handler func(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason)) *session.FakeTransport {
	return dynamicResponderWithMessageCount(clock, ctrlRef, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		data, nack, reason := handler(msg)
		return data, nack, reason, 0
	})
}

// dynamicResponderWithMessageCount additionally lets the handler control the
// RDM header's Message Count field on each response — needed to exercise
// DeviceStatus's drain-until-MessageCount-reaches-zero loop.
func dynamicResponderWithMessageCount(clock *session.FakeClock, ctrlRef *[]*session.RDMController, uid rdm.UID, handler func(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason, messageCount byte)) *session.FakeTransport {
	tport := session.NewFakeTransport()
	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		data, nack, reason, messageCount := handler(msg)
		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
		}
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseNackReason),
				SubDevice: msg.SubDevice, MessageCount: messageCount, CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: []byte{byte(reason >> 8), byte(reason)},
			}
		} else {
			if msg.CommandClass == rdm.SetCommand {
				data = nil
			}
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid,
				TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACK),
				SubDevice: msg.SubDevice, MessageCount: messageCount, CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: data,
			}
		}
		clock.AfterFunc(time.Millisecond, func() {
			(*ctrlRef)[0].HandleRDMResponse(resp)
		})
	}
	return tport
}

// runAsync drives fn on its own goroutine (since Client methods block on
// Command.Await) while advancing the fake clock until fn delivers a result
// on done, or the deadline expires.
func runAsync(t *testing.T, clock *session.FakeClock, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case <-done:
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for async call to complete")
}

func newTestClient(t *testing.T, uid rdm.UID, handler func(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason)) (*Client, *session.FakeClock) {
	t.Helper()
	ClearDescriptorCache()
	ForgetDevice(uid)
	clock := session.NewFakeClock(time.Time{})
	ctrlRef := make([]*session.RDMController, 1)
	tport := dynamicResponder(clock, &ctrlRef, uid, handler)
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl
	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.9"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.9:6454"),
		Port: port,
	}
	return New(ctrl, node, uid), clock
}

// --- Introspect -------------------------------------------------------------

func TestIntrospectSelfDescribing(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel, 0x8010})
	pd := rdm.ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16,
		Description: "PIXEL COUNT",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var progressEvents []IntrospectProgress
	var result IntrospectResult
	var err error
	runAsync(t, clock, func() {
		result, err = client.Introspect(context.Background(), func(p IntrospectProgress) {
			progressEvents = append(progressEvents, p)
		})
	})
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(result.Descriptors) != 1 {
		t.Fatalf("descriptors=%+v", result.Descriptors)
	}
	got := result.Descriptors[0]
	if !got.SelfDescribing || got.PID != 0x8010 || got.Label != "PIXEL COUNT" || got.Max != 16 {
		t.Fatalf("got %+v", got)
	}
	if len(progressEvents) != 1 || progressEvents[0].Done != 1 || progressEvents[0].Total != 1 {
		t.Fatalf("progress=%+v", progressEvents)
	}

	// DEVICE_LABEL is a known-decoded ESTA PID, so it should NOT have
	// triggered a PARAMETER_DESCRIPTION probe / appear in the descriptor list.
	for _, d := range result.Descriptors {
		if d.PID == rdm.PIDDeviceLabel {
			t.Fatalf("DEVICE_LABEL should not be an introspection target: %+v", d)
		}
	}
}

func TestIntrospectNackFallback(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5371, DeviceID: 1}
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{0x8099})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0
		case rdm.PIDParameterDescription:
			return nil, true, rdm.NackUnknownPID // device doesn't implement PARAMETER_DESCRIPTION
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var result IntrospectResult
	var err error
	runAsync(t, clock, func() {
		result, err = client.Introspect(context.Background(), nil)
	})
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(result.Descriptors) != 1 || result.Descriptors[0].SelfDescribing {
		t.Fatalf("expected one non-self-describing descriptor, got %+v", result.Descriptors)
	}
	if result.Descriptors[0].PID != 0x8099 {
		t.Fatalf("pid=%v", result.Descriptors[0].PID)
	}
}

// TestIntrospectReachesProxiedDevicePIDs guards task item 2 ("keep
// PROXIED_DEVICES/PROXIED_DEVICE_COUNT reachable via the generic parameter
// editor"): the Devices screen dropped its dedicated proxy-status callout
// (0x0011 used to be fetched and rendered as a standalone "Proxied
// devices" field), so Introspect's normal SUPPORTED_PARAMETERS walk is now
// the *only* way either PID ever reaches the UI. Before this change both
// PIDs were listed in knownDecodedESTAPIDs and so were deliberately
// excluded from introspection (isIntrospectionTarget returned false) —
// that exclusion had to be lifted for this test to pass, matching what
// TestIntrospectSelfDescribing already asserts DEVICE_LABEL should NOT do.
// TestClearAllDeviceStateWipesPerUIDIntrospectionState checks
// ClearAllDeviceState (the full-reset flow's "everything" companion to
// ForgetDevice's single-UID scope) empties Descriptors() for a UID that had
// already resolved one, without needing to know that UID up front.
func TestClearAllDeviceStateWipesPerUIDIntrospectionState(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5372, DeviceID: 1}
	pd := rdm.ParameterDescription{
		PID: 0x8020, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 255, DefaultValue: 0,
		Description: "TEST PID",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDParameterDescription {
			return pdBytes, false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})

	var desc ParamDescriptor
	runAsync(t, clock, func() {
		desc = client.DescribeParam(context.Background(), 0x8020)
	})
	if !desc.SelfDescribing || desc.Label != "TEST PID" {
		t.Fatalf("DescribeParam = %+v", desc)
	}
	if got := client.Descriptors(); len(got) != 1 {
		t.Fatalf("Descriptors before ClearAllDeviceState = %+v, want 1", got)
	}

	ClearAllDeviceState()

	if got := client.Descriptors(); len(got) != 0 {
		t.Fatalf("Descriptors after ClearAllDeviceState = %+v, want 0", got)
	}
}

func TestIntrospectReachesProxiedDevicePIDs(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDProxiedDevices, rdm.PIDProxiedDeviceCount})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0
		default:
			// A real proxy commonly won't implement PARAMETER_DESCRIPTION for
			// these standard PIDs (report §2.2 notes it's optional even for
			// PIDs a responder is technically allowed to describe) — NACKing
			// here proves the universal raw-hex fallback still surfaces the
			// PID rather than silently dropping it (report §1.1 item 4).
			return nil, true, rdm.NackUnknownPID
		}
	})

	var result IntrospectResult
	var err error
	runAsync(t, clock, func() {
		result, err = client.Introspect(context.Background(), nil)
	})
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(result.Descriptors) != 2 {
		t.Fatalf("expected PROXIED_DEVICES and PROXIED_DEVICE_COUNT both reachable via Introspect, got %+v", result.Descriptors)
	}
	seen := map[rdm.ParameterID]bool{}
	for _, d := range result.Descriptors {
		seen[d.PID] = true
		if d.SelfDescribing {
			t.Errorf("PID 0x%04X: expected non-self-describing (NACKed PARAMETER_DESCRIPTION), got %+v", uint16(d.PID), d)
		}
	}
	if !seen[rdm.PIDProxiedDevices] || !seen[rdm.PIDProxiedDeviceCount] {
		t.Fatalf("descriptors=%+v, want both 0x0010 and 0x0011", result.Descriptors)
	}
}

// --- GetParam / SetParam -----------------------------------------------------

func TestGetSetParamNumericRoundTrip(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5372, DeviceID: 1}
	pd := rdm.ParameterDescription{
		PID: 0x8020, PDLSize: 2, DataType: rdm.DSUnsignedWord, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitHertz, Prefix: rdm.PrefixNone, MinValue: 750, MaxValue: 24000, DefaultValue: 1500,
		Description: "FREQUENCY",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)
	current := uint16(1500)

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0
		case 0x8020:
			if msg.CommandClass == rdm.SetCommand {
				current = binary.BigEndian.Uint16(msg.ParameterData)
				return nil, false, 0
			}
			b := make([]byte, 2)
			binary.BigEndian.PutUint16(b, current)
			return b, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var val ParamValue
	var desc ParamDescriptor
	var err error
	runAsync(t, clock, func() {
		val, desc, err = client.GetParam(context.Background(), 0x8020)
	})
	if err != nil {
		t.Fatalf("GetParam: %v", err)
	}
	if val.Kind != ParamValueInt || val.Int != 1500 {
		t.Fatalf("val=%+v", val)
	}
	if desc.Unit != rdm.UnitHertz {
		t.Fatalf("desc=%+v", desc)
	}

	// In-range SET.
	runAsync(t, clock, func() {
		err = client.SetParam(context.Background(), 0x8020, 6000)
	})
	if err != nil {
		t.Fatalf("SetParam in-range: %v", err)
	}
	if current != 6000 {
		t.Fatalf("device-side value = %d, want 6000", current)
	}

	// Out-of-range SET must be rejected locally, without going to the wire
	// (current must stay at 6000).
	runAsync(t, clock, func() {
		err = client.SetParam(context.Background(), 0x8020, 99999)
	})
	if !errors.Is(err, ErrParamOutOfRange) {
		t.Fatalf("err=%v, want ErrParamOutOfRange", err)
	}
	if current != 6000 {
		t.Fatalf("out-of-range SET should not have reached the device: current=%d", current)
	}
}

func TestGetParamRawFallback(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5373, DeviceID: 1}
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDParameterDescription:
			return nil, true, rdm.NackUnknownPID
		case 0x8055:
			return []byte{0xDE, 0xAD, 0xBE, 0xEF}, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var val ParamValue
	var err error
	runAsync(t, clock, func() {
		val, _, err = client.GetParam(context.Background(), 0x8055)
	})
	if err != nil {
		t.Fatalf("GetParam: %v", err)
	}
	if val.Kind != ParamValueRaw || len(val.Raw) != 4 {
		t.Fatalf("val=%+v", val)
	}

	runAsync(t, clock, func() {
		err = client.SetParam(context.Background(), 0x8055, "not bytes")
	})
	if !errors.Is(err, ErrParamTypeMismatch) {
		t.Fatalf("err=%v, want ErrParamTypeMismatch", err)
	}
}

func TestSetParamNotSettable(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x5374, DeviceID: 1}
	pd := rdm.ParameterDescription{
		PID: 0x8030, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGet,
		Description: "READONLY",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDParameterDescription {
			return pdBytes, false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})
	var err error
	runAsync(t, clock, func() {
		err = client.SetParam(context.Background(), 0x8030, 1)
	})
	if !errors.Is(err, ErrParamNotSettable) {
		t.Fatalf("err=%v, want ErrParamNotSettable", err)
	}
}

// --- Sensors ------------------------------------------------------------

func TestSensorsGaugeData(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 1}
	deviceInfo := EncodeDeviceInfo(DeviceInfo{ProtocolVersionMajor: 1, SensorCount: 1, ProductCategory: 0x7000})
	def := rdm.EncodeSensorDefinition(rdm.SensorDefinition{
		SensorNumber: 0, Type: rdm.SensorTemperature, Unit: rdm.UnitCentigrade, Prefix: rdm.PrefixNone,
		RangeMin: -20, RangeMax: 100, NormalMin: 0, NormalMax: 60, SupportsRecording: 0x03,
		Description: "PSU TEMP",
	})
	val := rdm.EncodeSensorValue(rdm.SensorValue{SensorNumber: 0, Present: 85, Lowest: 20, Highest: 90, Recorded: 85})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDDeviceInfo:
			return deviceInfo, false, 0
		case rdm.PIDSensorDefinition:
			return def, false, 0
		case rdm.PIDSensorValue:
			return val, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var readings []SensorReading
	var err error
	runAsync(t, clock, func() {
		readings, err = client.Sensors(context.Background())
	})
	if err != nil {
		t.Fatalf("Sensors: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("readings=%+v", readings)
	}
	r := readings[0]
	if r.Value.Present != 85 {
		t.Fatalf("present=%d", r.Value.Present)
	}
	if r.InNormalBand() {
		t.Fatalf("85 should be outside normal band [0,60]: %+v", r)
	}

	// SensorValues should reuse the cached definition (no further
	// SENSOR_DEFINITION traffic needed) and just refresh the value.
	runAsync(t, clock, func() {
		readings, err = client.SensorValues(context.Background())
	})
	if err != nil {
		t.Fatalf("SensorValues: %v", err)
	}
	if len(readings) != 1 || readings[0].Definition.Description != "PSU TEMP" {
		t.Fatalf("readings=%+v", readings)
	}
}

// --- DeviceStatus ---------------------------------------------------------

func TestDeviceStatusDrain(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x7A70, DeviceID: 99}
	ClearDescriptorCache()
	ForgetDevice(uid)
	clock := session.NewFakeClock(time.Time{})
	ctrlRef := make([]*session.RDMController, 1)

	// Three messages queued; MessageCount counts down 2,1,0 across three
	// GETs, so the drain loop should stop right after the third (not hit
	// the maxDrain=10 bound).
	calls := 0
	tport := dynamicResponderWithMessageCount(clock, &ctrlRef, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID != rdm.PIDStatusMessages {
			return nil, true, rdm.NackUnknownPID, 0
		}
		calls++
		one := rdm.EncodeStatusMessages([]rdm.StatusMessage{{SubDevice: 0, Type: rdm.StatusWarning, MessageID: 0x0021, Value1: int16(calls), Value2: 0}})
		remaining := byte(3 - calls)
		return one, false, 0, remaining
	})
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl
	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.9"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.9:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	var msgs []rdm.StatusMessage
	var err error
	runAsync(t, clock, func() {
		msgs, err = client.DeviceStatus(context.Background(), rdm.StatusAdvisory, 10)
	})
	if err != nil {
		t.Fatalf("DeviceStatus: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3 (should stop once MessageCount reaches 0)", calls)
	}
	if len(msgs) != 3 {
		t.Fatalf("msgs=%+v", msgs)
	}
}

func TestDeviceStatusMaxDrainBound(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x7A71, DeviceID: 1}
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID != rdm.PIDStatusMessages {
			return nil, true, rdm.NackUnknownPID
		}
		// MessageCount always reads 0 through this handler shape (see
		// dynamicResponder), so a responder that keeps returning a message
		// despite a zero count would otherwise loop once and stop — this
		// exercises that "stops after first ACK when MessageCount==0" path.
		return rdm.EncodeStatusMessages([]rdm.StatusMessage{{Type: rdm.StatusAdvisory, MessageID: 1}}), false, 0
	})
	var msgs []rdm.StatusMessage
	var err error
	runAsync(t, clock, func() {
		msgs, err = client.DeviceStatus(context.Background(), rdm.StatusAdvisory, 5)
	})
	if err != nil {
		t.Fatalf("DeviceStatus: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("msgs=%+v, want exactly 1 (loop stops at MessageCount==0)", msgs)
	}
}

// TestDescribeParamDoesNotProbeStandardPIDs is the round-4 wasted-transaction
// fix, verified as an absence of wire traffic.
//
// PARAMETER_DESCRIPTION (0x0051) is defined for PIDs a responder cannot
// describe from the standard; ask a conforming device about a standard PID
// and it NACKs. RDM-LOG4 caught Benny512 asking anyway — 0x0070
// (PRODUCT_DETAIL_ID_LIST), 0x0080 (DEVICE_MODEL_DESCRIPTION) and 0x0081
// (MANUFACTURER_LABEL) — and the one that reached a healthy responder came
// back NACK 0x0006 DATA_OUT_OF_RANGE, the hardware agreeing the round trip
// was pure waste. On a saturated wireless link every one of those costs a
// real transaction.
//
// Introspect's walk had been gated on isIntrospectionTarget since the waste
// was first identified. This on-demand path — DescribeParam, reached from the
// generic parameter editor rather than from the walk — had not been, which is
// why the requests were still on the wire two rounds later.
func TestDescribeParamDoesNotProbeStandardPIDs(t *testing.T) {
	ClearAllDeviceState()
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x004D05BF}

	var describedPIDs []rdm.ParameterID
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDParameterDescription {
			described := rdm.ParameterID(binaryBigEndianUint16(msg.ParameterData))
			describedPIDs = append(describedPIDs, described)
			// What real gear answers for a standard PID.
			return nil, true, rdm.NackDataOutOfRange
		}
		return nil, true, rdm.NackUnknownPID
	})

	// The three PIDs the bench log caught us describing, all of which this
	// package already decodes natively.
	standard := []rdm.ParameterID{
		rdm.PIDProductDetailIDList,    // 0x0070
		rdm.PIDDeviceModelDescription, // 0x0080
		rdm.PIDManufacturerLabel,      // 0x0081
	}
	for _, pid := range standard {
		var d ParamDescriptor
		runAsync(t, clock, func() {
			d = client.DescribeParam(context.Background(), pid)
		})
		// The outcome is unchanged — a NACK produced exactly this descriptor
		// before, so the parameter editor's raw-hex fallback still applies.
		// The only difference is that nothing was sent.
		if d.PID != pid || d.SelfDescribing {
			t.Errorf("DescribeParam(0x%04X) = %+v, want the non-self-describing descriptor", uint16(pid), d)
		}
	}
	if len(describedPIDs) != 0 {
		t.Fatalf("PARAMETER_DESCRIPTION was sent for standard PIDs %v; a conforming device can only NACK those", describedPIDs)
	}

	// A manufacturer PID must still be probed for real — the gate must not
	// have taken the generic editor's self-description away.
	mfr := rdm.ParameterID(0x8021)
	var d ParamDescriptor
	runAsync(t, clock, func() {
		d = client.DescribeParam(context.Background(), mfr)
	})
	if len(describedPIDs) != 1 || describedPIDs[0] != mfr {
		t.Fatalf("described PIDs = %v, want exactly [0x8021]", describedPIDs)
	}
	if d.PID != mfr {
		t.Errorf("DescribeParam(0x8021) = %+v", d)
	}
}

func binaryBigEndianUint16(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
