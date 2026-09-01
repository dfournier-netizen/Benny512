package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// wireDeviceResponder scripts h.tport to answer any RDM request to uid
// using handler, delivering the response via a clock-fired callback (never
// synchronously — session.RDMController's Submit path holds its own mutex
// across the Send call, so a synchronous HandleRDMResponse from inside
// OnSend would deadlock; see internal/params's dynamicResponder, which
// this mirrors).
func (h *testHarness) wireDeviceResponder(uid rdm.UID, handler func(msg rdm.Message) (data []byte, nack bool, reason rdm.NackReason, messageCount byte)) {
	h.tport.OnSend = func(sp session.SentPacket) {
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
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseNackReason), SubDevice: msg.SubDevice, MessageCount: messageCount,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: []byte{byte(reason >> 8), byte(reason)},
			}
		} else {
			if msg.CommandClass == rdm.SetCommand {
				data = nil
			}
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice, MessageCount: messageCount,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
			}
		}
		h.clock.AfterFunc(time.Millisecond, func() { h.rdmc.HandleRDMResponse(resp) })
	}
}

// runHTTPAsync drives an HTTP request against handler on its own goroutine
// (since it blocks on RDM round-trips) while advancing h.clock until the
// response is ready.
//
// This intentionally does NOT race a real wall-clock deadline against the
// fake clock. An earlier version looped on `time.Now().Before(deadline)`
// with a 1ms real sleep between fake-clock advances: under CPU contention
// (parallel test processes, -race overhead, a loaded CI box) the real
// deadline could expire before the handler goroutine had been scheduled
// enough times to make progress, even though the fake-clock-driven state
// machine itself hadn't stalled and would have resolved fine given more
// wall-clock slack. That produced flaky failures with no logic bug behind
// them — sometimes a timeout, sometimes a downstream assertion tripped by
// an incompletely-processed response. Bounding on a fixed number of fake
// advances instead (with a cooperative yield, not a real sleep, in between)
// makes completion depend only on logical progress, never on how much real
// CPU time the scheduler happened to hand this goroutine.
func (h *testHarness) runHTTPAsync(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, h.srv.Handler(), method, path, body)
	}()

	// 20,000 advances of 2ms of fake time each is 40 fake seconds of
	// protocol time to resolve in — orders of magnitude more than any
	// real RDM round-trip/backoff in this codebase needs — while imposing
	// no real-time budget at all, so it can't be raced by CPU contention.
	const maxAdvances = 20000
	for i := 0; i < maxAdvances; i++ {
		select {
		case r := <-done:
			h.waitForRegistrySync(t)
			return r
		default:
		}
		h.clock.Advance(2 * time.Millisecond)
		select {
		case r := <-done:
			h.waitForRegistrySync(t)
			return r
		default:
		}
		runtime.Gosched()
	}
	t.Fatal("timed out waiting for HTTP handler to resolve (no progress after many fake-clock advances — a real hang, not scheduling jitter)")
	return nil
}

// waitForRegistrySync closes a second, independent race behind the same
// symptom: RDMController.Command.done fires the instant a command
// completes, but that completion only reaches the fixture table when
// Registry.Run() — a separate goroutine started by newHarness — later
// drains the corresponding EventCommandComplete off RDMController.Events()
// and applies it via handleRDMEvent (see completeLocked: it sends on
// cmd.done, *then* emits the event — so the HTTP response the caller just
// received can legitimately arrive before Registry has seen the same
// completion at all). A caller that immediately inspects Registry state
// right after runHTTPAsync returns (as
// TestDevicesBackfillPopulatesManufacturerAndModel does via /api/fixtures)
// would otherwise race that goroutine under contention — reproduced by
// running this file's flaky test under artificial CPU load until it failed,
// not merely inferred.
//
// The fix borrows devicesclear_test.go's seedToD technique (see its doc
// comment) and generalizes it into a barrier: submit a harmless broadcast
// RDM command (broadcasts complete synchronously in issueLocked, no
// simulated device required) and block until *that* command's own
// EventCommandComplete is republished on Registry.RDMEvents(). Registry.Run
// drains RDMController.Events() strictly in arrival order, one event at a
// time, handling each fully (handleRDMEvent) before republishing it and
// moving to the next — so by construction every event queued ahead of the
// sentinel (in particular, whatever the just-completed HTTP call produced)
// has already been applied by the time the sentinel's own event comes back
// out. That is a deterministic proof, not a poll-and-hope: it holds
// regardless of how many RDM round trips the request triggered, including
// zero (a request rejected before ever reaching Submit).
func (h *testHarness) waitForRegistrySync(t *testing.T) {
	t.Helper()
	sentinelUID := rdm.Broadcast(0x7FFE) // ESTA prototyping range; no test fixture ever uses it
	sentinelNode := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("0.0.0.1"), BindIndex: 0xFF},
		Addr: netip.MustParseAddrPort("0.0.0.1:6454"),
		Port: mustPort(t),
	}
	h.rdmc.Get(sentinelNode, sentinelUID, 0x0000, nil)

	const maxAdvances = 20000
	for i := 0; i < maxAdvances; i++ {
		select {
		case ev := <-h.srv.Registry.RDMEvents():
			if ev.Kind == session.EventCommandComplete && ev.UID == sentinelUID {
				return
			}
		default:
			h.clock.Advance(2 * time.Millisecond)
			runtime.Gosched()
		}
	}
	t.Fatal("timed out waiting for Registry to catch up with RDM completion events")
}

func TestDeviceParamsFlow(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	pd := rdm.ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16,
		Description: "PIXEL COUNT",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)
	current := byte(10)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0, 0
		case 0x8010:
			if msg.CommandClass == rdm.SetCommand {
				current = msg.ParameterData[0]
				return nil, false, 0, 0
			}
			return []byte{current}, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/param/8010", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET param status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got paramValueJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Kind != "int" || got.Int != 10 {
		t.Fatalf("got %+v", got)
	}
	if !got.Descriptor.SelfDescribing || got.Descriptor.Label != "PIXEL COUNT" {
		t.Fatalf("descriptor=%+v", got.Descriptor)
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/param/8010", map[string]any{"value": 12})
	if rr.Code != http.StatusOK {
		t.Fatalf("SET param status=%d body=%s", rr.Code, rr.Body.String())
	}
	if current != 12 {
		t.Fatalf("device-side value = %d, want 12", current)
	}

	// Out-of-range SET should be rejected with 4xx and not reach the device.
	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/param/8010", map[string]any{"value": 999})
	if rr.Code < 400 {
		t.Fatalf("expected error status for out-of-range SET, got %d", rr.Code)
	}
	if current != 12 {
		t.Fatalf("out-of-range SET should not reach device: current=%d", current)
	}

	// Cached descriptor list should now include 0x8010.
	rr2 := doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET params status=%d", rr2.Code)
	}
	var list []paramDescriptorJSON
	if err := json.Unmarshal(rr2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, d := range list {
		if d.PID == "8010" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 8010 in cached descriptors: %+v", list)
	}
}

func TestDeviceSensorsEndpoint(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 2}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	deviceInfo := params.EncodeDeviceInfo(params.DeviceInfo{ProtocolVersionMajor: 1, SensorCount: 1})
	def := rdm.EncodeSensorDefinition(rdm.SensorDefinition{
		SensorNumber: 0, Type: rdm.SensorVoltage, Unit: rdm.UnitVoltsDC, Prefix: rdm.PrefixNone,
		RangeMin: 0, RangeMax: 300, NormalMin: 100, NormalMax: 250, SupportsRecording: 0x03,
		Description: "Signal Quality",
	})
	val := rdm.EncodeSensorValue(rdm.SensorValue{SensorNumber: 0, Present: 50, Lowest: 10, Highest: 60, Recorded: 50})
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDDeviceInfo:
			return deviceInfo, false, 0, 0
		case rdm.PIDSensorDefinition:
			return def, false, 0, 0
		case rdm.PIDSensorValue:
			if msg.CommandClass == rdm.SetCommand {
				return nil, false, 0, 0
			}
			return val, false, 0, 0
		case rdm.PIDRecordSensors:
			return nil, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/sensors", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var readings []sensorReadingJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &readings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(readings) != 1 || readings[0].Present != 50 {
		t.Fatalf("readings=%+v", readings)
	}
	// present=50 falls outside the normal band [100,250] — that's the
	// out-of-band-warning-state case the demo mode is meant to exercise.
	if readings[0].InNormalBand {
		t.Fatalf("50 should read as outside the [100,250] normal band: %+v", readings[0])
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uidStr+"/sensors/record", map[string]any{"sensor": 0})
	if rr.Code != http.StatusOK {
		t.Fatalf("record status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestIntrospectEndpointAsyncCompletion(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 3}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{0x8020})
	pdBytes := rdm.EncodeParameterDescription(rdm.ParameterDescription{
		PID: 0x8020, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGet,
		Description: "REFRESH RATE",
	})
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0, 0
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	uidStr := uid.String()
	// POST /introspect kicks off the walk on its own goroutine and must
	// return immediately (202) without waiting for RDM traffic.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uidStr+"/introspect", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Advance the fake clock repeatedly until the background introspection
	// goroutine's RDM traffic resolves and the descriptor becomes visible
	// via the cached-descriptors endpoint.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.clock.Advance(2 * time.Millisecond)
		listRR := doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
		var list []paramDescriptorJSON
		if err := json.Unmarshal(listRR.Body.Bytes(), &list); err == nil && len(list) == 1 && list[0].PID == "8020" {
			if list[0].Label != "REFRESH RATE" {
				t.Fatalf("descriptor=%+v", list[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for introspection to populate the descriptor cache")
}

func TestDeviceUnknownUID(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/device/5370:00000099/sensors", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeviceBadUID(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/device/not-a-uid/sensors", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func mustPort(t *testing.T) artnet.PortAddress {
	t.Helper()
	p, err := artnet.NewPortAddress(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// --- Phase D task 2/3: service-life, destructive actions, supported-PID ---

// wireServiceLifeDevice scripts a fake device that advertises the full
// service-life family plus RESET_DEVICE/FACTORY_DEFAULTS in SUPPORTED_
// PARAMETERS (so the speculative gate lets every GET through) and answers
// each with a fixed value.
func (h *testHarness) wireServiceLifeDevice(uid rdm.UID) {
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{
		rdm.PIDDeviceHours, rdm.PIDLampHours, rdm.PIDLampStrikes, rdm.PIDLampState,
		rdm.PIDDevicePowerCycles, rdm.PIDFactoryDefaults, rdm.PIDResetDevice,
	})
	values := map[rdm.ParameterID][]byte{
		rdm.PIDDeviceHours:       rdm.EncodeUint32Counter(500),
		rdm.PIDLampHours:         rdm.EncodeUint32Counter(120),
		rdm.PIDLampStrikes:       rdm.EncodeUint32Counter(9),
		rdm.PIDLampState:         rdm.EncodeLampState(rdm.LampOn),
		rdm.PIDDevicePowerCycles: rdm.EncodeUint32Counter(3),
		rdm.PIDFactoryDefaults:   {0x00},
	}
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return supported, false, 0, 0
		}
		if msg.ParameterID == rdm.PIDResetDevice {
			return nil, false, 0, 0 // ACK with no data, per E1.20 §10.11.2
		}
		v, ok := values[msg.ParameterID]
		if !ok {
			return nil, true, rdm.NackUnknownPID, 0
		}
		if msg.CommandClass == rdm.SetCommand {
			values[msg.ParameterID] = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0, 0
		}
		return v, false, 0, 0
	})
}

func TestGetServiceLife(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x10}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireServiceLifeDevice(uid)

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/service-life", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got serviceLifeJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.DeviceHours.Known || got.DeviceHours.Value != 500 {
		t.Errorf("DeviceHours = %+v, want known/500", got.DeviceHours)
	}
	if !got.LampHours.Known || got.LampHours.Value != 120 {
		t.Errorf("LampHours = %+v, want known/120", got.LampHours)
	}
	if !got.LampStrikes.Known || got.LampStrikes.Value != 9 {
		t.Errorf("LampStrikes = %+v, want known/9", got.LampStrikes)
	}
	if !got.LampState.Known || got.LampState.Value != int64(rdm.LampOn) || got.LampState.Label != "On" {
		t.Errorf("LampState = %+v, want known/1/On", got.LampState)
	}
	if !got.DevicePowerCycles.Known || got.DevicePowerCycles.Value != 3 {
		t.Errorf("DevicePowerCycles = %+v, want known/3", got.DevicePowerCycles)
	}
}

// TestGetServiceLifeDegradesForUnsupportedDevice proves GET /service-life
// returns 200 with every field Known:false — not an error — for a device
// that advertises none of the service-life PIDs (Task 4's "prove the UI
// degrades" ask, exercised directly against the HTTP layer here).
func TestGetServiceLifeDegradesForUnsupportedDevice(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x11}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel}), false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/service-life", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got serviceLifeJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for name, f := range map[string]serviceLifeFieldJSON{
		"deviceHours": got.DeviceHours, "lampHours": got.LampHours, "lampStrikes": got.LampStrikes,
		"lampState": got.LampState, "devicePowerCycles": got.DevicePowerCycles,
	} {
		if f.Known {
			t.Errorf("%s.Known = true, want false (device never advertised it)", name)
		}
		if f.Error == "" {
			t.Errorf("%s.Error is empty, want an explanatory message", name)
		}
	}
}

func TestSetServiceLifeField(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x12}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireServiceLifeDevice(uid)

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/service-life", map[string]any{"field": "lampHours", "value": 0})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/service-life", nil)
	var got serviceLifeJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.LampHours.Value != 0 {
		t.Fatalf("LampHours after SET 0 = %+v, want value 0", got.LampHours)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/service-life", map[string]any{"field": "bogus", "value": 1})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad field: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDeviceActionsReportsAdvertisement guards Phase D task 2's "offer both
// [warm/cold] when 0x1001 is advertised; never claim to know more" rule at
// the HTTP layer: GET /actions must say resetDevice.supported=true for a
// device that lists RESET_DEVICE, and known=false (not supported=false) for
// a device whose SUPPORTED_PARAMETERS could not be resolved at all.
func TestDeviceActionsReportsAdvertisement(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)

	t.Run("advertised", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x13}
		ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
		h.srv.Registry.NoteFixture(ref, uid)
		h.wireServiceLifeDevice(uid)

		rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/actions", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var got deviceActionsJSON
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.ResetDevice.Known || !got.ResetDevice.Supported {
			t.Errorf("ResetDevice = %+v, want known/supported", got.ResetDevice)
		}
		if !got.FactoryDefaults.Known || !got.FactoryDefaults.Supported {
			t.Errorf("FactoryDefaults = %+v, want known/supported", got.FactoryDefaults)
		}
	})

	t.Run("unresolvable -> known false, not supported false", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x14}
		ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
		h.srv.Registry.NoteFixture(ref, uid)
		h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
			return nil, true, rdm.NackUnknownPID, 0 // NACKs SUPPORTED_PARAMETERS itself too
		})

		rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/actions", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var got deviceActionsJSON
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.ResetDevice.Known {
			t.Errorf("ResetDevice.Known = true, want false (SUPPORTED_PARAMETERS unresolvable)")
		}
		if got.ResetDevice.Supported {
			t.Errorf("ResetDevice.Supported = true, want false when Known is false")
		}
	})
}

func TestResetDeviceRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x15}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	wireHit := false
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDResetDevice {
			wireHit = true
		}
		return nil, false, 0, 0
	})

	// No confirm at all.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/reset", map[string]any{"mode": "warm"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Wrong confirm string.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/reset", map[string]any{"mode": "warm", "confirm": "yes"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Bad mode, even with correct confirm.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/reset", map[string]any{"mode": "sideways", "confirm": "RESET"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad mode: status=%d body=%s", rr.Code, rr.Body.String())
	}

	if wireHit {
		t.Fatal("RESET_DEVICE reached the wire despite missing/bad confirmation")
	}
}

func TestResetDeviceSucceedsWithConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x16}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	var gotPD []byte
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDResetDevice {
			gotPD = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/reset", map[string]any{"mode": "cold", "confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got resetDeviceResponseJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "ok" || got.Mode != "cold" || got.Note == "" {
		t.Fatalf("response=%+v", got)
	}
	if len(gotPD) != 1 || gotPD[0] != byte(rdm.ResetCold) {
		t.Fatalf("device received PD %x, want [FF]", gotPD)
	}
}

func TestFactoryDefaultsRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x17}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	wireHit := false
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDFactoryDefaults && msg.CommandClass == rdm.SetCommand {
			wireHit = true
		}
		return nil, false, 0, 0
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/factory-defaults", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if wireHit {
		t.Fatal("FACTORY_DEFAULTS SET reached the wire without confirmation")
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/factory-defaults", map[string]any{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("confirmed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !wireHit {
		t.Fatal("FACTORY_DEFAULTS SET never reached the wire despite confirmation")
	}
}

func TestGetSupportedParameters(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x18}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel, rdm.PIDCurve}), false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/supported-parameters", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got supportedParametersJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Known {
		t.Fatalf("Known = false, want true")
	}
	want := []string{"0082", "0343"} // DEVICE_LABEL, CURVE, sorted ascending
	if len(got.PIDs) != len(want) || got.PIDs[0] != want[0] || got.PIDs[1] != want[1] {
		t.Fatalf("PIDs = %v, want %v", got.PIDs, want)
	}
}

// TestFixtureJSONExposesProxiedDeviceCount guards Phase D task 1's "hide as
// rows, expose as structured data" wrinkle end to end: a device that
// advertises PROXIED_DEVICE_COUNT and is asked for its value directly must
// never show that PID in /params (TierHidden), while GET /api/fixtures
// carries the structured proxiedDeviceCount/proxiedDeviceCountKnown fields.
func TestFixtureJSONExposesProxiedDeviceCount(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x19}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDProxiedDeviceCount}), false, 0, 0
		case rdm.PIDProxiedDeviceCount:
			return rdm.EncodeProxiedDeviceCount(rdm.ProxiedDeviceCount{Count: 2}), false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})

	// Introspect first: PROXIED_DEVICE_COUNT must not appear in /params
	// even though the device advertises it (TierHidden).
	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/introspect", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("introspect status=%d body=%s", rr.Code, rr.Body.String())
	}
	uidStr := uid.String()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.clock.Advance(2 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
	var list []paramDescriptorJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, d := range list {
		if d.PID == "0011" {
			t.Fatalf("PROXIED_DEVICE_COUNT (0011) appeared in /params despite TierHidden: %+v", d)
		}
	}

	// The client CAN still reach the raw value by explicit PID (hiding is
	// an editor-row/UI-prominence decision, not an access-control one) —
	// doing so caches it into the registry, which is what /api/fixtures
	// reads from.
	rr = h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/param/0011", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("direct GET 0011 status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/fixtures", nil)
	var fixtures []fixtureJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &fixtures); err != nil {
		t.Fatalf("unmarshal fixtures: %v", err)
	}
	var found *fixtureJSON
	for i := range fixtures {
		if fixtures[i].UID == uidStr {
			found = &fixtures[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("device %s not in /api/fixtures", uidStr)
	}
	if !found.ProxiedDeviceCountKnown || found.ProxiedDeviceCount != 2 {
		t.Fatalf("fixture proxy fields = known=%v count=%d, want known=true count=2", found.ProxiedDeviceCountKnown, found.ProxiedDeviceCount)
	}
}

// The following tests guard the same defect class the owner's bug report
// traced to entry.go's Universe field (`omitempty` on a numeric field
// erasing a legitimate zero), across every other place this codebase's own
// Notes and this audit found it. Asserting a struct field equals 0 after
// unmarshal would be vacuous (it's the zero value either way); each test
// instead asserts the actual marshalled JSON BYTES contain the key.

// TestSensorReadingJSON_ZeroRangeAndNormalBoundsMarshalExplicit guards the
// exact field this project's own Project Notes flagged before ("a
// legitimate 0 is absent from the JSON... worked around client-side at the
// time") — RangeMin/RangeMax/NormalMin/NormalMax must round-trip a real 0
// boundary explicitly, not rely on HasRange/HasNormalBand alone.
func TestSensorReadingJSON_ZeroRangeAndNormalBoundsMarshalExplicit(t *testing.T) {
	r := sensorReadingJSON{
		HasRange: true, RangeMin: 0, RangeMax: 100,
		HasNormalBand: true, NormalMin: 0, NormalMax: 50,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"rangeMin":0`, `"normalMin":0`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled sensor reading missing %s; got %s", want, got)
		}
	}
}

// TestParamValueJSON_ZeroIntMarshalsExplicit guards paramValueJSON.Int: a
// live RDM param legitimately reporting 0 (e.g. DMX level, an address, a
// count) when Kind=="int" must round-trip as an explicit "int":0, not
// vanish and read back as `undefined` in the device detail pane.
func TestParamValueJSON_ZeroIntMarshalsExplicit(t *testing.T) {
	v := paramValueJSON{PID: "0080", Kind: "int", Int: 0}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"int":0`) {
		t.Errorf("marshalled param value missing \"int\":0; got %s", got)
	}
}

// TestServiceLifeFieldJSON_ZeroValueMarshalsExplicit guards
// serviceLifeFieldJSON.Value: a brand-new fixture legitimately reporting 0
// lamp strikes / 0 power cycles (Known==true) must round-trip that 0
// explicitly rather than being indistinguishable from "not yet fetched".
func TestServiceLifeFieldJSON_ZeroValueMarshalsExplicit(t *testing.T) {
	f := serviceLifeFieldJSON{Known: true, Value: 0}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"value":0`) {
		t.Errorf("marshalled service-life field missing \"value\":0; got %s", got)
	}
}

// TestFixtureJSON_DeviceModelIDAndProxiedDeviceCountZeroMarshalExplicit
// guards fixtureJSON's DeviceModelID (once HasDeviceInfo is true) and
// ProxiedDeviceCount (once ProxiedDeviceCountKnown is true): both must
// round-trip a real, known 0 explicitly.
func TestFixtureJSON_DeviceModelIDAndProxiedDeviceCountZeroMarshalExplicit(t *testing.T) {
	f := fixtureJSON{
		UID: "1900:00000001", HasDeviceInfo: true, DeviceModelID: 0,
		ProxiedDeviceCountKnown: true, ProxiedDeviceCount: 0,
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"deviceModelId":0`, `"proxiedDeviceCount":0`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled fixture missing %s; got %s", want, got)
		}
	}
}
