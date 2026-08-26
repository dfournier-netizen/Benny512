package params

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// queuedResponse is one scripted answer to a GET QUEUED_MESSAGE. PID is what
// the responder answers *with*, which by E1.20 is the queued parameter's own
// PID rather than QUEUED_MESSAGE's — the substitution the controller's PID
// invariant now allows for this one request PID.
type queuedResponse struct {
	PID  rdm.ParameterID
	Data []byte
}

// queuedMessageResponder answers GET QUEUED_MESSAGE from a script and NACKs
// everything else with UNKNOWN_PID.
func queuedMessageResponder(clock *session.FakeClock, ctrlRef *[]*session.RDMController, uid rdm.UID, script []queuedResponse) *session.FakeTransport {
	next := 0
	tport := session.NewFakeTransport()
	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: uid,
			TransactionNumber: msg.TransactionNumber,
			SubDevice:         msg.SubDevice,
			CommandClass:      rdm.GetCommandResponse,
		}
		if msg.ParameterID != rdm.PIDQueuedMessage || next >= len(script) {
			resp.PortIDOrResponseType = byte(rdm.ResponseNackReason)
			resp.ParameterID = msg.ParameterID
			resp.ParameterData = []byte{0x00, byte(rdm.NackUnknownPID)}
		} else {
			r := script[next]
			next++
			resp.PortIDOrResponseType = byte(rdm.ResponseACK)
			resp.ParameterID = r.PID
			resp.ParameterData = r.Data
		}
		clock.AfterFunc(time.Millisecond, func() { (*ctrlRef)[0].HandleRDMResponse(resp) })
	}
	return tport
}

func newQueuedClient(t *testing.T, uid rdm.UID, script []queuedResponse) (*Client, *session.FakeClock) {
	t.Helper()
	clock := session.NewFakeClock(time.Time{})
	ctrlRef := make([]*session.RDMController, 1)
	tport := queuedMessageResponder(clock, &ctrlRef, uid, script)
	ctrl := session.NewRDMController(session.RDMConfig{
		Transport:          tport,
		Clock:              clock,
		QueuedMessageDrain: session.DrainOff,
	})
	ctrlRef[0] = ctrl
	t.Cleanup(ctrl.Stop)
	port, err := artnet.NewPortAddress(0, 0, 1)
	if err != nil {
		t.Fatalf("NewPortAddress: %v", err)
	}
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.9"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.9:6454"),
		Port: port,
	}
	return New(ctrl, node, uid), clock
}

func statusBytes(id uint16) []byte {
	return rdm.EncodeStatusMessages([]rdm.StatusMessage{
		{SubDevice: 0, Type: rdm.StatusWarning, MessageID: id, Value1: 7},
	})
}

// TestClientDrainQueuedMessages is the params-level view of the drain: a
// responder hands back a status report, then a deferred SENSOR_VALUE that
// the old repeat-GET-STATUS_MESSAGES workaround could never have collected,
// then E1.20's empty marker.
func TestClientDrainQueuedMessages(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x6DA2C93B}
	client, clock := newQueuedClient(t, uid, []queuedResponse{
		{PID: rdm.PIDStatusMessages, Data: statusBytes(0x0021)},
		{PID: rdm.PIDSensorValue, Data: []byte{0, 0, 0x3D, 0, 0, 0, 0, 0, 0}},
		{PID: rdm.PIDStatusMessages, Data: nil}, // queue empty
	})

	var res session.DrainResult
	var err error
	runAsync(t, clock, func() {
		res, err = client.DrainQueuedMessages(context.Background(), rdm.StatusNone, 0)
	})
	if err != nil {
		t.Fatalf("DrainQueuedMessages: %v", err)
	}
	if !res.Empty {
		t.Fatal("Empty = false, want true — the drain did not reach the queue-empty marker")
	}
	if res.Iterations != 3 {
		t.Fatalf("Iterations = %d, want 3", res.Iterations)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(res.Messages))
	}
	if len(res.Status) != 1 || res.Status[0].MessageID != 0x0021 {
		t.Fatalf("Status = %+v, want one message 0x0021", res.Status)
	}
	if res.Messages[1].PID != rdm.PIDSensorValue {
		t.Fatalf("second queued PID = 0x%04X, want SENSOR_VALUE", uint16(res.Messages[1].PID))
	}
}

// TestClientDrainQueuedMessagesUnsupported: a responder with no
// QUEUED_MESSAGE support answers once and is not asked again.
func TestClientDrainQueuedMessagesUnsupported(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x004D09D1}
	client, clock := newQueuedClient(t, uid, nil)

	var res session.DrainResult
	var err error
	runAsync(t, clock, func() {
		res, err = client.DrainQueuedMessages(context.Background(), rdm.StatusNone, 0)
	})
	if err != nil {
		t.Fatalf("DrainQueuedMessages: %v", err)
	}
	if !res.Unsupported {
		t.Fatalf("Unsupported = false, want true (kind %v)", res.LastKind)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
}
