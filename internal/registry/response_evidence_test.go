package registry

import (
	"net/netip"
	"testing"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

func TestResponseEvidenceIsNotAnAdvertisement(t *testing.T) {
	node := session.NodeRef{Key: session.NodeKey{IP: netip.MustParseAddr("2.11.90.1"), BindIndex: 1}}
	uid := rdm.UID{ManufacturerID: 0x4d50, DeviceID: 0x1158fe}
	for _, tc := range []struct {
		name     string
		result   session.Result
		answered bool
	}{
		{"timeout", session.Result{Kind: session.ResultTimeout}, false},
		{"proxy refusal", session.Result{Kind: session.ResultProxyBufferFull}, false},
		{"breaker", session.Result{Kind: session.ResultDeviceUnreachable}, false},
		{"broadcast", session.Result{Kind: session.ResultBroadcast}, false},
		{"ack", session.Result{Kind: session.ResultAck}, true},
		{"unknown PID nack", session.Result{Kind: session.ResultNack, Request: session.Request{PID: rdm.PIDProductDetailIDList}}, true},
		{"timer then deadline", session.Result{Kind: session.ResultDeadlineExceeded, AckTimers: 1}, true},
		{"partial then timeout", session.Result{Kind: session.ResultTimeout, Blocks: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := New(nil, nil)
			reg.NoteFixture(node, uid)
			read := func() Fixture { return reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)[0] }
			if read().HasResponded {
				t.Fatal("advertisement counted as a reply")
			}
			reg.handleRDMEvent(session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid, Result: &tc.result})
			if read().HasResponded != tc.answered {
				t.Fatalf("response evidence=%v, want %v", read().HasResponded, tc.answered)
			}
			reg.NoteFixture(node, uid)
			if read().HasResponded != tc.answered {
				t.Fatal("rediscovery changed reply evidence")
			}
			reg.ClearDevicesOnPort(node.Key.IP, node.Port)
			reg.NoteFixture(node, uid)
			if read().HasResponded {
				t.Fatal("clear retained reply evidence")
			}
		})
	}
}
