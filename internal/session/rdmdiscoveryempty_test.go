package session

import (
	"testing"
	"time"

	"benny512/internal/rdm"
)

// TestDiscoverEmptyTodAfterFlushIsNotCompletion reproduces the defect from
// bench capture RDM-LOG25, where the owner could not discover a rig of
// Elation Paladin Cubes on any port, with any cable — because neither the
// port nor the cable was the variable.
//
// The capture is three packets:
//
//	16:00:18.757  OUT  ArtTodControl AtcFlush  universe 11
//	16:00:18.982  IN   ArtTodData  uidTotal=0 blockCount=0
//
// **225 milliseconds.** A real RDM discovery walks a 48-bit UID space with
// DISC_UNIQUE_BRANCH and takes seconds. What the gateway sent back is its
// just-flushed — and therefore empty — Table of Devices; the real one follows
// when discovery actually finishes. AtcFlush is defined as "flush your ToD
// and run a full discovery", so an empty table arriving immediately after it
// is the *expected* first thing to hear, not an answer.
//
// HandleTodData took it as one:
//
//	d.total = td.UidTotal                                  // 0
//	d.haveTotal = true
//	complete := d.haveTotal && len(uids) >= int(d.total)    // 0 >= 0 → TRUE
//	if complete { finishDiscoveryLocked(d, nil) }           // "done, no devices"
//
// so the 20-second window that exists precisely to let a node finish
// discovering was abandoned after a quarter of a second, and the rig came
// back empty every time.
//
// This is the tenth instance on this project of one defect class: a plausible
// zero mistaken for a real answer. `uidTotal=0` means two entirely different
// things — "I have no devices" and "I have not looked yet" — and after a
// flush it means the second.
//
// A plain RequestToD is the opposite case and is covered below: there the
// node is being asked for its cached table, nothing was flushed, and an empty
// answer is a real and final one.
func TestDiscoverEmptyTodAfterFlushIsNotCompletion(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 11) // LOG25's universe
	node := nodeRef("2.11.90.4", 1, port)

	d := h.ctrl.Discover(node)
	h.tr.TakeSent()

	// The gateway's immediate post-flush reply: nothing found *yet*.
	h.ctrl.HandleInbound(todDataInbound(port, 0, 0, nil, node.Addr))

	select {
	case res := <-d.Done():
		t.Fatalf("discovery finished on an empty post-flush ToD (uids=%v err=%v) — "+
			"the node had 225ms to run a discovery that takes seconds, so this "+
			"reports an empty rig instead of waiting for the real table",
			uidStrings(res.UIDs), res.Err)
	default:
	}

	// The real table, once the gateway has actually finished discovering.
	h.ctrl.HandleInbound(todDataInbound(port, 2, 1, []rdm.UID{uidA, uidB}, node.Addr))

	res := awaitDiscovery(t, h, d, 2*time.Second)
	if res.Err != nil {
		t.Fatalf("discovery errored: %v", res.Err)
	}
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA, uidB}) {
		t.Fatalf("UIDs = %v, want both fixtures — the late, real ToD must be the "+
			"one that completes the discovery", uidStrings(res.UIDs))
	}
}

// TestDiscoverEmptyTodStillTimesOutOnAGenuinelyEmptyPort is the other half of
// the same decision, and the reason the fix is "don't complete early" rather
// than "never complete empty".
//
// A port with genuinely nothing on it also answers uidTotal=0 and then says
// nothing further. That must still terminate — on the discovery timeout,
// reporting an empty rig — rather than hanging forever waiting for fixtures
// that do not exist. The cost of the fix is therefore latency on an empty
// port, not a stuck UI, and that is the right trade: waiting is recoverable,
// reporting "no fixtures" when eight are plugged in is not.
func TestDiscoverEmptyTodStillTimesOutOnAGenuinelyEmptyPort(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 11)
	node := nodeRef("2.11.90.4", 1, port)

	d := h.ctrl.Discover(node)
	h.tr.TakeSent()
	h.ctrl.HandleInbound(todDataInbound(port, 0, 0, nil, node.Addr))

	res := awaitDiscovery(t, h, d, 2*DefaultDiscoveryTimeout)
	if len(res.UIDs) != 0 {
		t.Fatalf("UIDs = %v, want none on a genuinely empty port", uidStrings(res.UIDs))
	}
	// Whether this surfaces as a timeout error or a clean empty result is a
	// presentation decision; what matters is that it TERMINATES.
	t.Logf("empty port terminated with err=%v after the discovery window", res.Err)
}

// TestRequestToDEmptyTodCompletesImmediately pins the case the fix must NOT
// change. RequestToD sends ArtTodRequest, which asks a node for the table it
// already holds and forces no discovery — so an empty answer there is a real,
// final answer, and making the user wait 20 seconds for it would be a
// regression introduced by fixing the flush case.
func TestRequestToDEmptyTodCompletesImmediately(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 11)
	node := nodeRef("2.11.90.4", 1, port)

	d := h.ctrl.RequestToD(node)
	h.tr.TakeSent()
	h.ctrl.HandleInbound(todDataInbound(port, 0, 0, nil, node.Addr))

	// No clock advance at all: this must already be done.
	select {
	case res := <-d.Done():
		if res.Err != nil {
			t.Fatalf("RequestToD errored on an empty cached table: %v", res.Err)
		}
		if len(res.UIDs) != 0 {
			t.Fatalf("UIDs = %v, want none", uidStrings(res.UIDs))
		}
	default:
		t.Fatal("RequestToD did not complete on an empty ToD — nothing was flushed, " +
			"so the node's empty cached table is a real answer and must not wait " +
			"out the discovery window")
	}
}
