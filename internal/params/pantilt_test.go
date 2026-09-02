package params

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// TestClientPanTiltOrientationPIDs covers the three E1.20 §10.7 orientation
// PIDs the Reconcile screen's commit/push needs — PAN_INVERT (0x0600),
// TILT_INVERT (0x0601) and PAN_TILT_SWAP (0x0602).
//
// Two things are asserted that a plain "does it round-trip" test would miss:
//
//   - The GET side is a TOLERANT READER: E1.20 says 0/1, but a non-zero byte
//     that is not 1 (0xFF has been seen in the wild) must read as true, not
//     as a decode failure and not as false. A fixture answering 0xFF for
//     "pan is inverted" and being reported as NOT inverted would send a
//     mover to the wrong side of the stage.
//
//   - The SET side puts exactly one byte on the wire, and `false` is a real
//     value that must be SENT (as 0x00) rather than treated as "nothing to
//     do". Turning an invert OFF is as much a configuration change as
//     turning it on, and it is one of the settings a substituted fixture
//     inherits.
//
// No time.Sleep and no runtime.Gosched spin-loop: the fake clock is advanced
// against a wall-clock deadline, exactly as TestClientDimmerPIDs does.
func TestClientPanTiltOrientationPIDs(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	ctrlRef := make([]*session.RDMController, 1)

	// GET answers. PAN_INVERT deliberately answers 0xFF rather than 0x01 —
	// the tolerant-reader case above. TILT_INVERT answers a real 0x00.
	answers := map[rdm.ParameterID][]byte{
		rdm.PIDSupportedParameters: rdm.EncodeSupportedParameters([]rdm.ParameterID{
			rdm.PIDPanInvert, rdm.PIDTiltInvert, rdm.PIDPanTiltSwap,
		}),
		rdm.PIDPanInvert:   {0xFF},
		rdm.PIDTiltInvert:  {0x00},
		rdm.PIDPanTiltSwap: {0x01},
	}

	var mu sync.Mutex
	sets := map[rdm.ParameterID][]byte{}

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
			mu.Lock()
			sets[msg.ParameterID] = append([]byte(nil), msg.ParameterData...)
			mu.Unlock()
			data = nil
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
		clock.AfterFunc(time.Millisecond, func() { ctrlRef[0].HandleRDMResponse(resp) })
	}

	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl
	port, _ := artnet.NewPortAddress(0, 0, 0)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.7"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.7:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	type result struct {
		pan, tilt, swap bool
		err             error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		if r.pan, r.err = client.PanInvert(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.tilt, r.err = client.TiltInvert(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.swap, r.err = client.PanTiltSwap(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		// Now the SET side. Turning pan invert OFF is the "false is a real
		// value" case — it must put 0x00 on the wire, not skip the write.
		if r.err = client.SetPanInvert(context.Background(), false); r.err != nil {
			resCh <- r
			return
		}
		if r.err = client.SetTiltInvert(context.Background(), true); r.err != nil {
			resCh <- r
			return
		}
		r.err = client.SetPanTiltSwap(context.Background(), false)
		resCh <- r
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("client call failed: %v", r.err)
			}
			if !r.pan {
				t.Error("PanInvert = false for a device answering 0xFF; any non-zero byte means inverted")
			}
			if r.tilt {
				t.Error("TiltInvert = true for a device answering 0x00")
			}
			if !r.swap {
				t.Error("PanTiltSwap = false for a device answering 0x01")
			}
			mu.Lock()
			got := map[rdm.ParameterID][]byte{}
			for k, v := range sets {
				got[k] = v
			}
			mu.Unlock()
			want := map[rdm.ParameterID][]byte{
				rdm.PIDPanInvert:   {0x00},
				rdm.PIDTiltInvert:  {0x01},
				rdm.PIDPanTiltSwap: {0x00},
			}
			for pid, wantData := range want {
				gotData, ok := got[pid]
				if !ok {
					t.Errorf("no SET was sent for PID 0x%04X — 'false' must be written, not skipped", uint16(pid))
					continue
				}
				if len(gotData) != 1 || gotData[0] != wantData[0] {
					t.Errorf("SET 0x%04X put % X on the wire, want % X", uint16(pid), gotData, wantData)
				}
			}
			return
		default:
		}
	}
	t.Fatal("timed out waiting for the pan/tilt orientation calls to resolve")
}

// TestClientPanInvert_BadLength: a device answering the wrong number of
// bytes must produce an error, not a confident boolean. This screen writes
// DMX addresses to real fixtures on the strength of what it reads back, so a
// malformed answer must never quietly become "false".
func TestClientPanInvert_BadLength(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}
	ctrlRef := make([]*session.RDMController, 1)
	tport := scriptedResponder(t, clock, &ctrlRef, uid, map[rdm.ParameterID][]byte{
		rdm.PIDSupportedParameters: rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDPanInvert}),
		rdm.PIDPanInvert:           {0x01, 0x02}, // two bytes: not a valid PAN_INVERT response
	})
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl
	port, _ := artnet.NewPortAddress(0, 0, 0)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.8"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.8:6454"),
		Port: port,
	}
	client := New(ctrl, node, uid)

	type result struct {
		v   bool
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		v, err := client.PanInvert(context.Background())
		resCh <- result{v, err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Millisecond)
		select {
		case r := <-resCh:
			if r.err == nil {
				t.Fatalf("PanInvert returned %v with no error for a 2-byte response", r.v)
			}
			return
		default:
		}
	}
	t.Fatal("timed out waiting for PanInvert to resolve")
}
