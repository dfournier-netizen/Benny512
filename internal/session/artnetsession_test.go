package session

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
)

func newSession(t *testing.T, cfg ArtNetConfig) (*ArtNetSession, *FakeClock, *FakeTransport) {
	t.Helper()
	clock := NewFakeClock(time.Time{})
	tr := NewFakeTransport()
	cfg.Clock = clock
	cfg.Transport = tr
	s := NewArtNetSession(cfg)
	t.Cleanup(s.Stop)
	return s, clock, tr
}

// --- poll cycle -------------------------------------------------------

func TestArtPollBroadcastCadence(t *testing.T) {
	s, clock, tr := newSession(t, ArtNetConfig{PollInterval: 3 * time.Second})

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sent := tr.TakeSent()
	if len(sent) != 1 {
		t.Fatalf("Start sent %d packets, want 1 immediate ArtPoll", len(sent))
	}
	if sent[0].Packet.Kind != artnet.KindPoll {
		t.Fatalf("first packet kind = %v, want ArtPoll", sent[0].Packet.Kind)
	}
	if !sent[0].Broadcast {
		t.Fatal("ArtPoll must be broadcast, not unicast")
	}
	if got := sent[0].Packet.Poll.Flags; got != 0x02 {
		t.Fatalf("ArtPoll Flags = 0x%02X, want 0x02 (reply on state change)", got)
	}
	if got := sent[0].Packet.Poll.ProtocolVersion; got != artnet.DefaultProtocolVersion {
		t.Fatalf("ArtPoll ProtVer = %d, want %d", got, artnet.DefaultProtocolVersion)
	}

	// Three more intervals ⇒ exactly three more polls, not two or four.
	clock.Advance(9 * time.Second)
	if got := len(tr.TakeSent()); got != 3 {
		t.Fatalf("polls over 9 s at 3 s interval = %d, want 3", got)
	}

	// Just short of the next boundary emits nothing.
	clock.Advance(2999 * time.Millisecond)
	if got := len(tr.TakeSent()); got != 0 {
		t.Fatalf("polls before the next boundary = %d, want 0", got)
	}
	clock.Advance(time.Millisecond)
	if got := len(tr.TakeSent()); got != 1 {
		t.Fatalf("polls at the boundary = %d, want 1", got)
	}
}

func TestArtNetSessionDefaults(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	if got, want := s.cfg.PollInterval, DefaultPollInterval; got != want {
		t.Fatalf("default poll interval = %v, want %v", got, want)
	}
	if got, want := s.cfg.LivenessTimeout, 3*DefaultPollInterval; got != want {
		t.Fatalf("default liveness timeout = %v, want 3× the poll interval (%v)", got, want)
	}

	// An explicit poll interval still drives the 3× liveness default.
	s2, _, _ := newSession(t, ArtNetConfig{PollInterval: time.Second})
	if got, want := s2.cfg.LivenessTimeout, 3*time.Second; got != want {
		t.Fatalf("liveness timeout = %v, want %v", got, want)
	}
}

func TestPollNowSendsOutsideTheCycle(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{PollInterval: time.Hour})
	if err := s.PollNow(); err != nil {
		t.Fatalf("PollNow: %v", err)
	}
	sent := tr.TakeSent()
	if len(sent) != 1 || sent[0].Packet.Kind != artnet.KindPoll {
		t.Fatalf("PollNow sent %d packets", len(sent))
	}
	if got := s.PollCount(); got != 1 {
		t.Fatalf("PollCount = %d, want 1", got)
	}
}

func TestArtPollStopHaltsCycle(t *testing.T) {
	s, clock, tr := newSession(t, ArtNetConfig{PollInterval: time.Second})
	_ = s.Start()
	tr.TakeSent()
	s.Stop()
	clock.Advance(10 * time.Second)
	if got := len(tr.TakeSent()); got != 0 {
		t.Fatalf("polls after Stop = %d, want 0", got)
	}
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("timers left pending after Stop = %d, want 0", got)
	}
}

// --- node table -------------------------------------------------------

func TestPollReplyAddsNodeWithParsedFields(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})

	r := pollReply("2.11.90.2", 1, "EN4", "Netron EN4 stage left")
	r.NetSwitch = 2
	r.SubSwitch = 3
	r.SwOut = [4]byte{1, 2, 3, 4}
	r.GoodOutputB = [4]byte{0x80, 0, 0, 0} // port 0 has RDM disabled
	s.HandleInbound(inboundPollReply(r, addrPort("2.11.90.2", ArtNetUDPPort)))

	nodes := s.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Key != (NodeKey{IP: nodeIP, BindIndex: 1}) {
		t.Fatalf("node key = %+v", n.Key)
	}
	if n.ShortName != "EN4" || n.LongName != "Netron EN4 stage left" {
		t.Fatalf("names = %q / %q", n.ShortName, n.LongName)
	}
	if !n.RDMCapable {
		t.Fatal("Status1 bit 1 set but RDMCapable is false")
	}
	if n.Style != StyleNode {
		t.Fatalf("style = %v, want Node", n.Style)
	}
	if len(n.Ports) != 4 {
		t.Fatalf("ports = %d, want 4", len(n.Ports))
	}
	// Port-Address is NetSwitch : SubSwitch : SwOut[i], not SwOut alone.
	want := artnet.PortAddress{Net: 2, SubNet: 3, Universe: 4}
	if got := n.Ports[3].OutputAddress; got != want {
		t.Fatalf("port 3 output address = %+v, want %+v", got, want)
	}
	if n.Ports[0].RDMEnabled {
		t.Fatal("GoodOutputB bit 7 set but port 0 reports RDM enabled")
	}
	if !n.Ports[1].RDMEnabled {
		t.Fatal("GoodOutputB clear but port 1 reports RDM disabled")
	}
	outs := n.OutputUniverses()
	if len(outs) != 4 {
		t.Fatalf("output universes = %d, want 4", len(outs))
	}
}

func TestBindIndexDistinguishesNodesAtSameIP(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	from := addrPort("2.11.90.2", ArtNetUDPPort)

	s.HandleInbound(inboundPollReply(pollReply("2.11.90.2", 1, "EN4-a", "bind 1"), from))
	s.HandleInbound(inboundPollReply(pollReply("2.11.90.2", 2, "EN4-b", "bind 2"), from))

	if got := len(s.Nodes()); got != 2 {
		t.Fatalf("node count = %d, want 2 (one per bind group)", got)
	}

	// BindIndex 0 (legacy, field not implemented) must normalise to 1
	// rather than creating a phantom third node.
	s.HandleInbound(inboundPollReply(pollReply("2.11.90.2", 0, "EN4-a", "bind 1"), from))
	if got := len(s.Nodes()); got != 2 {
		t.Fatalf("node count after BindIndex 0 reply = %d, want 2", got)
	}
}

func TestPollReplyFallsBackToSourceIPWhenFieldIsZero(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	r := pollReply("2.11.90.2", 1, "N", "N")
	r.IPAddress = [4]byte{}
	s.HandleInbound(inboundPollReply(r, addrPort("10.0.0.7", ArtNetUDPPort)))

	nodes := s.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(nodes))
	}
	if got, want := nodes[0].Key.IP, netip.MustParseAddr("10.0.0.7"); got != want {
		t.Fatalf("node IP = %v, want %v (datagram source)", got, want)
	}
}

// --- events -----------------------------------------------------------

func TestNodeEventsAddedUpdatedAndSilentRefresh(t *testing.T) {
	s, clock, _ := newSession(t, ArtNetConfig{})
	from := addrPort("2.11.90.2", ArtNetUDPPort)
	r := pollReply("2.11.90.2", 1, "EN4", "long")

	s.HandleInbound(inboundPollReply(r, from))
	evs := drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeAdded {
		t.Fatalf("first reply events = %+v, want one added", evs)
	}

	// An identical refresh changes nothing but LastSeen: no event, so the
	// 3 s poll cycle does not spam subscribers.
	clock.Advance(time.Second)
	s.HandleInbound(inboundPollReply(r, from))
	if evs := drainNodeEvents(s.Events()); len(evs) != 0 {
		t.Fatalf("unchanged refresh emitted %+v, want no events", evs)
	}

	r.LongName = "renamed"
	s.HandleInbound(inboundPollReply(r, from))
	evs = drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeUpdated {
		t.Fatalf("changed reply events = %+v, want one updated", evs)
	}
	if evs[0].Node.LongName != "renamed" {
		t.Fatalf("event carried stale node: %q", evs[0].Node.LongName)
	}

	// A port re-patch is an advertised change too.
	r.SwOut[0] = 9
	s.HandleInbound(inboundPollReply(r, from))
	evs = drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeUpdated {
		t.Fatalf("re-patch events = %+v, want one updated", evs)
	}
}

// --- liveness ---------------------------------------------------------

func TestNodeGoesStaleAfterLivenessTimeout(t *testing.T) {
	s, clock, tr := newSession(t, ArtNetConfig{PollInterval: 3 * time.Second})
	_ = s.Start()
	tr.TakeSent()

	from := addrPort("2.11.90.2", ArtNetUDPPort)
	r := pollReply("2.11.90.2", 1, "EN4", "long")
	s.HandleInbound(inboundPollReply(r, from))
	drainNodeEvents(s.Events())

	// Default liveness window is 3× poll = 9 s. Two polls later (6 s) the
	// node is still considered live.
	clock.Advance(6 * time.Second)
	if evs := drainNodeEvents(s.Events()); len(evs) != 0 {
		t.Fatalf("node declared lost too early: %+v", evs)
	}
	if n, _ := s.Node(NodeKey{IP: nodeIP, BindIndex: 1}); n.Stale {
		t.Fatal("node marked stale inside the liveness window")
	}

	// The sweep runs on poll ticks, so loss is observed on the first tick
	// past the window (12 s).
	clock.Advance(6 * time.Second)
	evs := drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeLost {
		t.Fatalf("events after liveness window = %+v, want one lost", evs)
	}
	n, ok := s.Node(NodeKey{IP: nodeIP, BindIndex: 1})
	if !ok || !n.Stale {
		t.Fatal("lost node should stay in the table, marked stale")
	}
	if got := len(s.LiveNodes()); got != 0 {
		t.Fatalf("LiveNodes = %d, want 0", got)
	}

	// Loss is reported once, not on every subsequent sweep.
	clock.Advance(30 * time.Second)
	if evs := drainNodeEvents(s.Events()); len(evs) != 0 {
		t.Fatalf("repeat loss events: %+v", evs)
	}
}

func TestNodeRepliesKeepItAliveAcrossManyIntervals(t *testing.T) {
	s, clock, tr := newSession(t, ArtNetConfig{PollInterval: 3 * time.Second})
	_ = s.Start()
	tr.TakeSent()

	from := addrPort("2.11.90.2", ArtNetUDPPort)
	r := pollReply("2.11.90.2", 1, "EN4", "long")
	s.HandleInbound(inboundPollReply(r, from))
	drainNodeEvents(s.Events()) // discard the initial NodeAdded

	for i := 0; i < 10; i++ {
		clock.Advance(3 * time.Second)
		s.HandleInbound(inboundPollReply(r, from))
	}
	for _, ev := range drainNodeEvents(s.Events()) {
		t.Fatalf("a continuously-replying node produced %v", ev.Kind)
	}
	if got := len(s.LiveNodes()); got != 1 {
		t.Fatalf("LiveNodes = %d, want 1", got)
	}
}

func TestLostNodeReturningEmitsAdded(t *testing.T) {
	s, clock, tr := newSession(t, ArtNetConfig{PollInterval: time.Second, LivenessTimeout: 2 * time.Second})
	_ = s.Start()
	tr.TakeSent()

	from := addrPort("2.11.90.2", ArtNetUDPPort)
	r := pollReply("2.11.90.2", 1, "EN4", "long")
	s.HandleInbound(inboundPollReply(r, from))
	drainNodeEvents(s.Events())

	clock.Advance(5 * time.Second)
	evs := drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeLost {
		t.Fatalf("want one lost event, got %+v", evs)
	}

	s.HandleInbound(inboundPollReply(r, from))
	evs = drainNodeEvents(s.Events())
	if len(evs) != 1 || evs[0].Kind != NodeAdded {
		t.Fatalf("returning node events = %+v, want one added", evs)
	}
	if n, _ := s.Node(NodeKey{IP: nodeIP, BindIndex: 1}); n.Stale {
		t.Fatal("returning node still marked stale")
	}
}

// --- plumbing ---------------------------------------------------------

func TestSessionRunPumpsInboundChannel(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	tr.Deliver(inboundPollReply(pollReply("2.11.90.2", 1, "EN4", "long"), addrPort("2.11.90.2", ArtNetUDPPort)))

	select {
	case ev := <-s.Events():
		if ev.Kind != NodeAdded {
			t.Fatalf("event kind = %v, want added", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not process the delivered datagram")
	}

	tr.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after transport close, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after transport close")
	}
}

// --- ClearNodes (full-reset flow) --------------------------------------

func TestClearNodesWipesTheTableAndReturnsCount(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	from1 := addrPort("2.11.90.2", ArtNetUDPPort)
	from2 := addrPort("2.11.90.3", ArtNetUDPPort)
	s.HandleInbound(inboundPollReply(pollReply("2.11.90.2", 1, "EN4-A", "long a"), from1))
	s.HandleInbound(inboundPollReply(pollReply("2.11.90.3", 1, "EN4-B", "long b"), from2))
	if got := len(s.Nodes()); got != 2 {
		t.Fatalf("seeded node count = %d, want 2", got)
	}

	n := s.ClearNodes()
	if n != 2 {
		t.Fatalf("ClearNodes returned %d, want 2", n)
	}
	if got := len(s.Nodes()); got != 0 {
		t.Fatalf("node count after ClearNodes = %d, want 0", got)
	}

	// A fresh reply repopulates from scratch.
	s.HandleInbound(inboundPollReply(pollReply("2.11.90.2", 1, "EN4-A", "long a"), from1))
	if got := len(s.Nodes()); got != 1 {
		t.Fatalf("node count after a post-clear reply = %d, want 1", got)
	}
}

func TestClearNodesOnEmptySessionReturnsZero(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	if n := s.ClearNodes(); n != 0 {
		t.Fatalf("ClearNodes on an empty session = %d, want 0", n)
	}
}

func TestSessionIgnoresNonPollReplyTraffic(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	from := addrPort("2.11.90.2", ArtNetUDPPort)

	s.HandleInbound(Inbound{Data: []byte("not art-net at all"), From: from})
	s.HandleInbound(Inbound{Data: artnet.Encode(artnet.Packet{Kind: artnet.KindDmx, Dmx: artnet.Dmx{Data: make([]byte, 512)}}), From: from})
	s.HandleInbound(Inbound{Data: artnet.Encode(artnet.Packet{Kind: artnet.KindPoll, Poll: artnet.Poll{}}), From: from})

	if got := len(s.Nodes()); got != 0 {
		t.Fatalf("node count = %d, want 0", got)
	}
}
