package params

import (
	"bytes"
	"context"
	"encoding/hex"
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
