package registry

import (
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// TestUnreachableCauseFollowsTheBreaker feeds the real event handler the
// events the controller emits when a device's breaker opens for silence
// (RDM-LOG36's 4D50:00115938) and when it answers again. The registry must
// keep WHY the controller stopped asking, so the web layer can say
// "not answering" rather than blaming a wireless proxy the rig doesn't
// have, and must drop the cause the moment the device answers.
func TestUnreachableCauseFollowsTheBreaker(t *testing.T) {
	node := session.NodeRef{Key: session.NodeKey{IP: netip.MustParseAddr("2.11.90.6"), BindIndex: 1}}
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115938}
	reg := New(nil, nil)
	reg.NoteFixture(node, uid)
	read := func() Fixture { return reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)[0] }
	retry := time.Date(2026, 10, 6, 15, 22, 48, 0, time.UTC)

	due := &session.DeviceUnreachableError{UID: uid, Cause: session.CauseNoResponse, Silences: 3, RetryAt: retry}
	reg.handleRDMEvent(session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid,
		Result: &session.Result{Kind: session.ResultDeviceUnreachable, Err: due}})
	f := read()
	if !f.ProxyUnreachable || f.UnreachableCause != session.CauseNoResponse {
		t.Fatalf("after a no-response open: unreachable=%v cause=%v, want true/no-response", f.ProxyUnreachable, f.UnreachableCause)
	}
	if !f.ProxyRetryAt.Equal(retry) {
		t.Errorf("retryAt = %v, want %v", f.ProxyRetryAt, retry)
	}

	reg.handleRDMEvent(session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid,
		Result: &session.Result{Kind: session.ResultAck, Request: session.Request{PID: rdm.PIDDeviceInfo}}})
	f = read()
	if f.ProxyUnreachable || f.UnreachableCause != session.CauseProxyRefusal {
		t.Errorf("after an answer: unreachable=%v cause=%v, want false and the zero cause", f.ProxyUnreachable, f.UnreachableCause)
	}

	// A proxy-refusal open carries its own cause, not a stale one.
	reg.handleRDMEvent(session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid,
		Result: &session.Result{Kind: session.ResultDeviceUnreachable, Err: due}})
	pdue := &session.DeviceUnreachableError{UID: uid, Cause: session.CauseProxyRefusal, Refusals: 3, RetryAt: retry}
	reg.handleRDMEvent(session.Event{Kind: session.EventCommandComplete, Node: node, UID: uid,
		Result: &session.Result{Kind: session.ResultDeviceUnreachable, Err: pdue}})
	if f = read(); f.UnreachableCause != session.CauseProxyRefusal {
		t.Errorf("cause after a proxy-refusal open = %v, want proxy-refusal", f.UnreachableCause)
	}
}
