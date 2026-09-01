package params

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// TestDecodeDeviceInfoGolden uses the exact 19-byte DEVICE_INFO parameter
// data from the Phase-1a wire-format verification report §3.9: RDM Protocol
// Version=1.0, Device Model ID=0x0001, Product Category=0x0101
// (FIXTURE_FIXED), Software Version ID=0x01000000, DMX Footprint=4, Current
// Personality=1, Personality Count=4, DMX Start Address=1, Sub-Device
// Count=0, Sensor Count=0.
func TestDecodeDeviceInfoGolden(t *testing.T) {
	hexStr := "0100" + "0001" + "0101" + "01000000" + "0004" + "01" + "04" + "0001" + "0000" + "00"
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("bad golden hex: %v", err)
	}
	if len(data) != 19 {
		t.Fatalf("golden fixture is %d bytes, want 19", len(data))
	}
	got, err := DecodeDeviceInfo(data)
	if err != nil {
		t.Fatalf("DecodeDeviceInfo: %v", err)
	}
	want := DeviceInfo{
		ProtocolVersionMajor: 1, ProtocolVersionMinor: 0,
		DeviceModelID: 1, ProductCategory: 0x0101,
		SoftwareVersionID: 0x01000000, DMXFootprint: 4,
		CurrentPersonality: 1, PersonalityCount: 4,
		DMXStartAddress: 1, SubDeviceCount: 0, SensorCount: 0,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// Round-trip.
	if reenc := EncodeDeviceInfo(got); !bytes.Equal(reenc, data) {
		t.Errorf("round-trip mismatch: got %x want %x", reenc, data)
	}
}

func TestDecodeDeviceInfoBadLength(t *testing.T) {
	_, err := DecodeDeviceInfo([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

// TestDecodeEncodePersonalityDescription round-trips DMX_PERSONALITY_
// DESCRIPTION (0x00E1) against the exact bytes the owner's KL Core IP
// reported (index=5, footprint=13, per the DEVICE_INFO payload in this
// fix's bench log), plus a description at the max 32-byte label length and
// a zero-length ("truncated") description.
func TestDecodeEncodePersonalityDescription(t *testing.T) {
	cases := []struct {
		name string
		pd   PersonalityDescription
	}{
		{"typical", PersonalityDescription{Index: 5, DMXFootprint: 13, Description: "13ch Extended"}},
		{"empty description", PersonalityDescription{Index: 1, DMXFootprint: 4, Description: ""}},
		{"max length description (32 chars)", PersonalityDescription{
			Index: 9, DMXFootprint: 30,
			Description: "12345678901234567890123456789012", // 34 chars, encoder truncates to 32
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodePersonalityDescription(tc.pd)
			want := tc.pd
			if len(want.Description) > 32 {
				want.Description = want.Description[:32]
			}
			if len(encoded) != 3+len(want.Description) {
				t.Fatalf("encoded length = %d, want %d", len(encoded), 3+len(want.Description))
			}
			got, err := DecodePersonalityDescription(encoded)
			if err != nil {
				t.Fatalf("DecodePersonalityDescription: %v", err)
			}
			if got != want {
				t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
			}
		})
	}
}

// TestDecodePersonalityDescriptionTruncatedOnWire covers a response that is
// truncated below even the 3-byte fixed portion (a short/garbled PDL on the
// wire, distinct from a legitimately empty description string) — must error
// rather than panic or silently misread.
func TestDecodePersonalityDescriptionTruncatedOnWire(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {0x05}, {0x05, 0x00}} {
		if _, err := DecodePersonalityDescription(data); err == nil {
			t.Errorf("DecodePersonalityDescription(%x) = nil error, want ErrBadLength", data)
		} else if !errors.Is(err, ErrBadLength) {
			t.Errorf("DecodePersonalityDescription(%x) error = %v, want it to wrap ErrBadLength", data, err)
		}
	}
}

// scriptedResponder builds a FakeTransport that ACKs GET/SET with canned
// parameter data for the requested PID, mirroring the pattern used
// throughout the session package's own tests.
// scriptedResponder wires tport.OnSend to synthesize an ACK for any PID in
// answers, delivered by calling ctrl.HandleRDMResponse directly from a
// clock-scheduled callback (never synchronously from inside OnSend itself —
// see FakeTransport's doc comment on why that would risk deadlock/reentrancy
// against the controller's own mutex).
func scriptedResponder(t *testing.T, clock *session.FakeClock, ctrlRef *[]*session.RDMController, uid rdm.UID, answers map[rdm.ParameterID][]byte) *session.FakeTransport {
	t.Helper()
	tport := session.NewFakeTransport()
	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		data, ok := answers[msg.ParameterID]
		if !ok {
			return
		}
		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
			data = nil // SET ACKs typically carry no data
		}
		resp := rdm.Message{
			DestinationUID:       msg.SourceUID,
			SourceUID:            uid,
			TransactionNumber:    msg.TransactionNumber,
			PortIDOrResponseType: byte(rdm.ResponseACK),
			SubDevice:            msg.SubDevice,
			CommandClass:         respClass,
			ParameterID:          msg.ParameterID,
			ParameterData:        data,
		}
		clock.AfterFunc(time.Millisecond, func() {
			(*ctrlRef)[0].HandleRDMResponse(resp)
		})
	}
	return tport
}

func TestClientDeviceInfoAndLabels(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	uid := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	deviceInfoBytes := EncodeDeviceInfo(DeviceInfo{
		ProtocolVersionMajor: 1, DeviceModelID: 1, ProductCategory: 0x0101,
		SoftwareVersionID: 1, DMXFootprint: 4, CurrentPersonality: 1,
		PersonalityCount: 4, DMXStartAddress: 1,
	})
	ctrlRef := make([]*session.RDMController, 1)
	tport := scriptedResponder(t, clock, &ctrlRef, uid, map[rdm.ParameterID][]byte{
		rdm.PIDDeviceInfo:        deviceInfoBytes,
		rdm.PIDDeviceLabel:       []byte("Moving Head 1"),
		rdm.PIDManufacturerLabel: []byte("LumenRadio"),
		rdm.PIDDMXStartAddress:   {0x00, 0x01},
		rdm.PIDIdentifyDevice:    {0x01},
	})
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl

	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.5:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	// Drive requests in a goroutine since Await blocks; advance the fake
	// clock to fire the scripted response after the send.
	type result struct {
		di    DeviceInfo
		label string
		start uint16
		ident bool
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		r.di, r.err = client.DeviceInfo(context.Background())
		if r.err != nil {
			resCh <- r
			return
		}
		r.label, r.err = client.DeviceLabel(context.Background())
		if r.err != nil {
			resCh <- r
			return
		}
		r.start, r.err = client.DMXStartAddress(context.Background())
		if r.err != nil {
			resCh <- r
			return
		}
		r.ident, r.err = client.IdentifyDevice(context.Background())
		resCh <- r
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("client call failed: %v", r.err)
			}
			if r.di.DMXFootprint != 4 {
				t.Errorf("DeviceInfo footprint = %d, want 4", r.di.DMXFootprint)
			}
			if r.label != "Moving Head 1" {
				t.Errorf("DeviceLabel = %q", r.label)
			}
			if r.start != 1 {
				t.Errorf("DMXStartAddress = %d, want 1", r.start)
			}
			if !r.ident {
				t.Errorf("IdentifyDevice = false, want true")
			}
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for client calls to complete")
}

// TestClientDimmerPIDs exercises the E1.37-1 dimmer-PID typed helpers
// (Curve/CurveDescription/MinimumLevel/MaximumLevel/IdentifyMode) end to end
// through a scripted FakeTransport responder, mirroring
// TestClientDeviceInfoAndLabels' pattern.
func TestClientDimmerPIDs(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}
	ctrlRef := make([]*session.RDMController, 1)
	tport := scriptedResponder(t, clock, &ctrlRef, uid, map[rdm.ParameterID][]byte{
		// SUPPORTED_PARAMETERS must list every dimmer PID this test GETs —
		// Phase D task 3's speculative-probe gate (introspect.go's
		// isSpeculativePID) will otherwise refuse each of these calls
		// without a matching SUPPORTED_PARAMETERS entry, exactly as a real
		// device that never advertised them would be refused.
		rdm.PIDSupportedParameters: rdm.EncodeSupportedParameters([]rdm.ParameterID{
			rdm.PIDCurve, rdm.PIDCurveDescription, rdm.PIDOutputResponseTime,
			rdm.PIDModulationFrequency, rdm.PIDMinimumLevel, rdm.PIDMaximumLevel, rdm.PIDIdentifyMode,
		}),
		rdm.PIDCurve:               {3, 5}, // current=3 of 5
		rdm.PIDCurveDescription:    append([]byte{3}, []byte("S-Curve")...),
		rdm.PIDOutputResponseTime:  {1, 2},
		rdm.PIDModulationFrequency: {2, 6},
		rdm.PIDMinimumLevel:        rdm.EncodeMinimumLevel(rdm.MinimumLevel{Increasing: 10, Decreasing: 5, OnBelowMin: 1}),
		rdm.PIDMaximumLevel:        rdm.EncodeMaximumLevel(65000),
		rdm.PIDIdentifyMode:        {byte(rdm.IdentifyModeLoud)},
	})
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl

	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.6"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.6:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	type result struct {
		curve     rdm.IndexedChoice
		curveDesc rdm.IndexedDescription
		ort       rdm.IndexedChoice
		modFreq   rdm.IndexedChoice
		minLevel  rdm.MinimumLevel
		maxLevel  uint16
		identMode rdm.IdentifyMode
		err       error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		if r.curve, r.err = client.Curve(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.curveDesc, r.err = client.CurveDescription(context.Background(), 3); r.err != nil {
			resCh <- r
			return
		}
		if r.ort, r.err = client.OutputResponseTime(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.modFreq, r.err = client.ModulationFrequency(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.minLevel, r.err = client.MinimumLevel(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.maxLevel, r.err = client.MaximumLevel(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		r.identMode, r.err = client.IdentifyMode(context.Background())
		resCh <- r
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("client call failed: %v", r.err)
			}
			if r.curve != (rdm.IndexedChoice{Current: 3, Count: 5}) {
				t.Errorf("Curve = %+v", r.curve)
			}
			if r.curveDesc != (rdm.IndexedDescription{Index: 3, Description: "S-Curve"}) {
				t.Errorf("CurveDescription = %+v", r.curveDesc)
			}
			if r.ort != (rdm.IndexedChoice{Current: 1, Count: 2}) {
				t.Errorf("OutputResponseTime = %+v", r.ort)
			}
			if r.modFreq != (rdm.IndexedChoice{Current: 2, Count: 6}) {
				t.Errorf("ModulationFrequency = %+v", r.modFreq)
			}
			if r.minLevel != (rdm.MinimumLevel{Increasing: 10, Decreasing: 5, OnBelowMin: 1}) {
				t.Errorf("MinimumLevel = %+v", r.minLevel)
			}
			if r.maxLevel != 65000 {
				t.Errorf("MaximumLevel = %d, want 65000", r.maxLevel)
			}
			if r.identMode != rdm.IdentifyModeLoud {
				t.Errorf("IdentifyMode = %v, want Loud", r.identMode)
			}
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for dimmer-PID client calls to complete")
}

// TestClientDMXPersonalityAndDescription exercises DMXPersonality/
// SetDMXPersonality/DMXPersonalityDescription end to end through a scripted
// FakeTransport responder, mirroring TestClientDimmerPIDs' pattern — the
// owner-facing "personality 5/9" scenario from the KL Core IP bench log,
// wired through the real controller instead of calling the codec directly.
func TestClientDMXPersonalityAndDescription(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	uid := rdm.UID{ManufacturerID: 0x454C, DeviceID: 1} // "EL" for Elation, arbitrary
	ctrlRef := make([]*session.RDMController, 1)
	tport := scriptedResponder(t, clock, &ctrlRef, uid, map[rdm.ParameterID][]byte{
		rdm.PIDDMXPersonality: {5, 9}, // current=5 of 9, matches the bench log
		rdm.PIDDMXPersonalityDescription: EncodePersonalityDescription(PersonalityDescription{
			Index: 5, DMXFootprint: 13, Description: "13ch Extended",
		}),
	})
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl

	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.7"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.7:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	type result struct {
		pers Personality
		desc PersonalityDescription
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		if r.pers, r.err = client.DMXPersonality(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		r.desc, r.err = client.DMXPersonalityDescription(context.Background(), r.pers.Current)
		resCh <- r
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("client call failed: %v", r.err)
			}
			if r.pers != (Personality{Current: 5, Count: 9}) {
				t.Errorf("DMXPersonality = %+v, want {5 9}", r.pers)
			}
			want := PersonalityDescription{Index: 5, DMXFootprint: 13, Description: "13ch Extended"}
			if r.desc != want {
				t.Errorf("DMXPersonalityDescription = %+v, want %+v", r.desc, want)
			}
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for personality client calls to complete")
}
