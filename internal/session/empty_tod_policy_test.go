package session

import (
	"encoding/hex"
	"testing"
)

// The owner confirmed the old Paladin failure was hardware and requested
// restoration of immediate empty-table completion. This is a policy regression,
// not evidence that every gateway's first empty table must be its final table.
// Feed LOG25's literal inbound datagram, not an encode of our own ToD struct.
func TestCapturedEmptyToDCompletesWithoutWaiting(t *testing.T) {
	h := newHarness(t)
	node := nodeRef("2.11.90.4", 4, mustPortAddress(t, 0, 0, 11))
	d := h.ctrl.Discover(node)
	b, err := hex.DecodeString("4172742d4e6574000081000e01010000000000000400000b00000000")
	if err != nil {
		t.Fatal(err)
	}
	h.ctrl.HandleInbound(Inbound{Data: b, From: node.Addr})
	select {
	case res := <-d.Done():
		if !res.Complete || len(res.UIDs) != 0 || res.Err != nil {
			t.Fatalf("empty table result = %+v", res)
		}
	default:
		t.Fatal("captured empty ToD still waits for the discovery timeout")
	}
}
