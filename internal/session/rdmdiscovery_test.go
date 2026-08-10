package session

import (
	"errors"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

func awaitDiscovery(t *testing.T, h *rdmHarness, d *Discovery, limit time.Duration) DiscoveryResult {
	t.Helper()
	const step = time.Millisecond
	for elapsed := time.Duration(0); elapsed <= limit; elapsed += step {
		select {
		case res := <-d.Done():
			return res
		default:
		}
		h.clock.Advance(step)
	}
	select {
	case res := <-d.Done():
		return res
	default:
	}
	t.Fatalf("discovery did not complete within %v of simulated time", limit)
	return DiscoveryResult{}
}

func uidStrings(uids []rdm.UID) []string {
	out := make([]string, len(uids))
	for i, u := range uids {
		out[i] = u.String()
	}
	return out
}

func sameUIDSet(got, want []rdm.UID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- request packets --------------------------------------------------

func TestDiscoverSendsArtTodControlFlush(t *testing.T) {
	h := newHarness(t)
	node := nodeRef("2.11.90.2", 1, mustPortAddress(t, 3, 2, 5))
	h.ctrl.Discover(node)

	sent := h.tr.TakeSent()
	if len(sent) != 1 {
		t.Fatalf("datagrams = %d, want 1", len(sent))
	}
	if sent[0].Packet.Kind != artnet.KindTodControl {
		t.Fatalf("packet kind = %v, want ArtTodControl", sent[0].Packet.Kind)
	}
	tc := sent[0].Packet.TodControl
	if tc.Command != AtcFlush {
		t.Fatalf("Command = 0x%02X, want AtcFlush (0x01) to force a fresh discovery", tc.Command)
	}
	if tc.Net != 3 {
		t.Fatalf("Net = %d, want 3", tc.Net)
	}
	if got, want := tc.Address, byte(0x25); got != want {
		t.Fatalf("Address = 0x%02X, want 0x%02X (SubNet<<4 | Universe)", got, want)
	}
	if sent[0].Dst != node.Addr || sent[0].Broadcast {
		t.Fatalf("dst = %v broadcast = %v, want unicast to the node", sent[0].Dst, sent[0].Broadcast)
	}
}

func TestRequestToDSendsArtTodRequestWithoutForcingDiscovery(t *testing.T) {
	h := newHarness(t)
	node := nodeRef("2.11.90.2", 1, mustPortAddress(t, 1, 0, 2))
	h.ctrl.RequestToD(node)

	sent := h.tr.TakeSent()
	if len(sent) != 1 || sent[0].Packet.Kind != artnet.KindTodRequest {
		t.Fatalf("want a single ArtTodRequest, got %d packets", len(sent))
	}
	tr := sent[0].Packet.TodRequest
	if tr.Command != TodFull {
		t.Fatalf("Command = 0x%02X, want TodFull", tr.Command)
	}
	if tr.Net != 1 {
		t.Fatalf("Net = %d, want 1", tr.Net)
	}
	if len(tr.Address) != 1 || tr.Address[0] != 0x02 {
		t.Fatalf("Address = %v, want [0x02]", tr.Address)
	}
}

// --- multi-block assembly ---------------------------------------------

func TestToDAssemblesAcrossMultipleBlocks(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(port, 3, 0, []rdm.UID{uidA}, node.Addr))
	if _, done := tryDiscovery(d); done {
		t.Fatal("discovery completed before all UidTotal entries arrived")
	}
	h.ctrl.HandleInbound(todDataInbound(port, 3, 1, []rdm.UID{uidB}, node.Addr))
	h.ctrl.HandleInbound(todDataInbound(port, 3, 2, []rdm.UID{uidC}, node.Addr))

	res := awaitDiscovery(t, h, d, time.Second)
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}
	if !res.Complete {
		t.Fatal("Complete = false with all UidTotal entries received")
	}
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA, uidB, uidC}) {
		t.Fatalf("UIDs = %v", uidStrings(res.UIDs))
	}
	if res.Blocks != 3 {
		t.Fatalf("Blocks = %d, want 3", res.Blocks)
	}
	cached, ok := h.ctrl.ToD(nodeIP, port)
	if !ok || !sameUIDSet(cached, []rdm.UID{uidA, uidB, uidC}) {
		t.Fatalf("cached ToD = %v ok=%v", uidStrings(cached), ok)
	}
}

func TestToDAssemblesOutOfOrderAndDuplicateBlocks(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	// Blocks arrive 2, 0, 2 (duplicate), 1 — UDP makes no ordering promise.
	h.ctrl.HandleInbound(todDataInbound(port, 3, 2, []rdm.UID{uidC}, node.Addr))
	h.ctrl.HandleInbound(todDataInbound(port, 3, 0, []rdm.UID{uidA}, node.Addr))
	h.ctrl.HandleInbound(todDataInbound(port, 3, 2, []rdm.UID{uidC}, node.Addr))
	if _, done := tryDiscovery(d); done {
		t.Fatal("a duplicated block must not be counted as progress toward UidTotal")
	}
	h.ctrl.HandleInbound(todDataInbound(port, 3, 1, []rdm.UID{uidB}, node.Addr))

	res := awaitDiscovery(t, h, d, time.Second)
	if !res.Complete || res.Err != nil {
		t.Fatalf("res = %+v", res)
	}
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA, uidB, uidC}) {
		t.Fatalf("UIDs = %v, want the sorted de-duplicated union", uidStrings(res.UIDs))
	}
	if res.Blocks != 4 || res.DistinctBlockIDs != 3 {
		t.Fatalf("Blocks = %d / DistinctBlockIDs = %d, want 4 packets carrying 3 distinct blocks",
			res.Blocks, res.DistinctBlockIDs)
	}
}

func TestToDSingleBlockWithSeveralUIDs(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(port, 3, 0, []rdm.UID{uidC, uidA, uidB}, node.Addr))
	res := awaitDiscovery(t, h, d, time.Second)
	if !res.Complete {
		t.Fatalf("res = %+v", res)
	}
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA, uidB, uidC}) {
		t.Fatalf("UIDs = %v, want sorted", uidStrings(res.UIDs))
	}
}

func TestEmptyToDCompletesImmediately(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(port, 0, 0, nil, node.Addr))
	res := awaitDiscovery(t, h, d, time.Second)
	if !res.Complete || len(res.UIDs) != 0 || res.Err != nil {
		t.Fatalf("res = %+v, want an immediate empty completion", res)
	}
}

// --- failure paths ----------------------------------------------------

func TestTodNakFailsTheDiscovery(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(port, 4, 0, []rdm.UID{uidA}, node.Addr))
	h.ctrl.HandleInbound(todNakInbound(port, node.Addr))

	res := awaitDiscovery(t, h, d, time.Second)
	if !errors.Is(res.Err, ErrTodNak) {
		t.Fatalf("err = %v, want ErrTodNak", res.Err)
	}
	if res.Complete {
		t.Fatal("a TodNak discovery must not report Complete")
	}
	// Whatever did arrive is still handed back — a partial list beats none
	// for the fixtures screen.
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA}) {
		t.Fatalf("UIDs = %v, want the partial list", uidStrings(res.UIDs))
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending after TodNak = %d, want 0", got)
	}
}

func TestToDAssemblyTimesOutWithPartialResults(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, DiscoveryTimeout: 200 * time.Millisecond})
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(port, 3, 0, []rdm.UID{uidA}, node.Addr))
	res := awaitDiscovery(t, h, d, time.Second)

	if !errors.Is(res.Err, ErrTodTimeout) {
		t.Fatalf("err = %v, want ErrTodTimeout", res.Err)
	}
	if res.Complete {
		t.Fatal("Complete = true on a timed-out assembly")
	}
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA}) {
		t.Fatalf("UIDs = %v, want the block that did arrive", uidStrings(res.UIDs))
	}
}

func TestToDTimeoutRestartsOnEachBlock(t *testing.T) {
	// The timeout is a quiet-period timer, not a hard cap: a node that keeps
	// feeding blocks slowly must not be cut off.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, DiscoveryTimeout: 100 * time.Millisecond})
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	for i, uid := range []rdm.UID{uidA, uidB, uidC} {
		h.clock.Advance(80 * time.Millisecond)
		if _, done := tryDiscovery(d); done {
			t.Fatalf("discovery gave up before block %d despite steady progress", i)
		}
		h.ctrl.HandleInbound(todDataInbound(port, 3, byte(i), []rdm.UID{uid}, node.Addr))
	}
	res := awaitDiscovery(t, h, d, time.Second)
	if !res.Complete || res.Err != nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestRediscoverSupersedesTheRunningDiscovery(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	node := nodeRef("2.11.90.2", 1, port)

	first := h.ctrl.Discover(node)
	second := h.ctrl.Discover(node)

	res, done := tryDiscovery(first)
	if !done {
		t.Fatal("the superseded discovery should complete rather than hang")
	}
	if res.Err == nil {
		t.Fatalf("superseded discovery err = nil, want a failure: %+v", res)
	}

	h.ctrl.HandleInbound(todDataInbound(port, 1, 0, []rdm.UID{uidA}, node.Addr))
	if r := awaitDiscovery(t, h, second, time.Second); !r.Complete {
		t.Fatalf("the current discovery did not complete: %+v", r)
	}
}

// --- routing and cache ------------------------------------------------

func TestToDDataForADifferentPortDoesNotSatisfyThisDiscovery(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 0)
	otherPort := mustPortAddress(t, 0, 0, 1)
	node := nodeRef("2.11.90.2", 1, port)
	d := h.ctrl.Discover(node)

	h.ctrl.HandleInbound(todDataInbound(otherPort, 1, 0, []rdm.UID{uidB}, node.Addr))
	if _, done := tryDiscovery(d); done {
		t.Fatal("ArtTodData for another Port-Address completed this discovery")
	}
	h.ctrl.HandleInbound(todDataInbound(port, 1, 0, []rdm.UID{uidA}, node.Addr))

	res := awaitDiscovery(t, h, d, time.Second)
	if !sameUIDSet(res.UIDs, []rdm.UID{uidA}) {
		t.Fatalf("UIDs = %v, want only this port's device", uidStrings(res.UIDs))
	}
	// The other port's table is still cached, keyed separately.
	if cached, ok := h.ctrl.ToD(nodeIP, otherPort); !ok || !sameUIDSet(cached, []rdm.UID{uidB}) {
		t.Fatalf("other port cache = %v ok=%v", uidStrings(cached), ok)
	}
}

func TestUnsolicitedToDDataUpdatesTheCacheAndEmitsAnEvent(t *testing.T) {
	h := newHarness(t)
	port := mustPortAddress(t, 0, 0, 4)
	from := addrPort("2.11.90.2", ArtNetUDPPort)

	h.ctrl.HandleInbound(todDataInbound(port, 2, 0, []rdm.UID{uidA, uidB}, from))

	cached, ok := h.ctrl.ToD(nodeIP, port)
	if !ok || !sameUIDSet(cached, []rdm.UID{uidA, uidB}) {
		t.Fatalf("cache = %v ok=%v", uidStrings(cached), ok)
	}
	var ev *Event
	for _, e := range drainEvents(h.ctrl.Events()) {
		if e.Kind == EventToDUpdate {
			cp := e
			ev = &cp
		}
	}
	if ev == nil {
		t.Fatal("no EventToDUpdate for unsolicited ArtTodData")
	}
	if !ev.Complete {
		t.Fatal("a full unsolicited table should be flagged Complete")
	}
	if ev.Node.Port != port {
		t.Fatalf("event Port-Address = %+v, want %+v", ev.Node.Port, port)
	}
}

func TestToDLookupMissIsReported(t *testing.T) {
	h := newHarness(t)
	if _, ok := h.ctrl.ToD(nodeIP, mustPortAddress(t, 0, 0, 7)); ok {
		t.Fatal("ToD reported a hit for a port that was never discovered")
	}
}

func tryDiscovery(d *Discovery) (DiscoveryResult, bool) {
	select {
	case r := <-d.Done():
		return r, true
	default:
		return DiscoveryResult{}, false
	}
}
