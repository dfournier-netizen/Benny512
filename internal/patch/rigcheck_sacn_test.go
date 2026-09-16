package patch

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// --- a recording sACN stream ---------------------------------------------
//
// Deliberately NOT a real *sacn.Sender: these tests are about the ORDER and
// the EXISTENCE of the protocol operations Rig Check performs, and a fake
// records that directly. The bytes those operations put on the wire are
// asserted separately, against a real socket, in internal/web's
// sacn_isolation_test.go — and there they are parsed by hand, not with the
// encoder that produced them.

type sacnOp struct {
	kind     string // "send" | "stop" | "close"
	universe uint16
	payload  []byte
}

type fakeSACNStream struct {
	mu      sync.Mutex
	ops     []sacnOp
	sendErr error
}

func (f *fakeSACNStream) Send(universe uint16, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.ops = append(f.ops, sacnOp{"send", universe, append([]byte(nil), payload...)})
	return nil
}

func (f *fakeSACNStream) Stop(universe uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, sacnOp{kind: "stop", universe: universe})
	return nil
}

func (f *fakeSACNStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, sacnOp{kind: "close"})
	return nil
}

func (f *fakeSACNStream) taken() []sacnOp {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.ops
	f.ops = nil
	return out
}

func (f *fakeSACNStream) all() []sacnOp {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sacnOp(nil), f.ops...)
}

func kinds(ops []sacnOp) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.kind)
	}
	return out
}

type sacnHarness struct {
	rc      *RigCheck
	tport   *session.FakeTransport
	clock   *session.FakeClock
	streams []*fakeSACNStream
	opens   int
	openErr error
	mapErr  map[uint16]error
	offset  int // sACN universe = raw + offset
}

func newSACNHarness(t *testing.T) *sacnHarness {
	t.Helper()
	h := &sacnHarness{
		clock:  session.NewFakeClock(time.Time{}),
		tport:  session.NewFakeTransport(),
		mapErr: map[uint16]error{},
		offset: 1, // the default install: Art-Net 0 (show 1) is sACN 1
	}
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: h.clock, Transport: h.tport, Rate: 40})
	t.Cleanup(dmx.Stop)
	h.rc = NewRigCheck(dmx)
	h.rc.SetSACNBinding(SACNBinding{
		Open: func() (SACNStream, error) {
			h.opens++
			if h.openErr != nil {
				return nil, h.openErr
			}
			s := &fakeSACNStream{}
			h.streams = append(h.streams, s)
			return s, nil
		},
		Universe: func(raw uint16) (uint16, error) {
			if err := h.mapErr[raw]; err != nil {
				return 0, err
			}
			return raw + uint16(h.offset), nil
		},
	})
	t.Cleanup(h.rc.Stop)
	return h
}

func (h *sacnHarness) stream(t *testing.T, i int) *fakeSACNStream {
	t.Helper()
	if i >= len(h.streams) {
		t.Fatalf("expected at least %d sACN stream(s) to have been opened, got %d", i+1, len(h.streams))
	}
	return h.streams[i]
}

// artDmxCountFor counts ArtDmx datagrams addressed to a raw Port-Address.
func artDmxCountFor(t *testing.T, sent []session.SentPacket, universe uint16) int {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(universe)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, sp := range sent {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindDmx {
			continue
		}
		if sp.Packet.Dmx.SubUni == pa.SubUni() && sp.Packet.Dmx.Net == pa.Net {
			n++
		}
	}
	return n
}

// --- the isolation guarantee ---------------------------------------------

// TestSelectingSACNTakesTheUniverseOffArtNet is requirement 1 and 2 of the
// output-target contract at once: selecting sACN is not "also send sACN", and
// a universe is never driven by both protocols at the same time.
func TestSelectingSACNTakesTheUniverseOffArtNet(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}

	// Art-Net first, so there is something real to take off the air.
	if err := h.rc.Start(entries, ModeAllChannels, 255); err != nil {
		t.Fatal(err)
	}
	if proto, live := h.rc.LiveProtocolFor(0); !live || proto != ProtocolArtNet {
		t.Fatalf("after an Art-Net start, LiveProtocolFor(0) = (%q, %v), want (artnet, true)", proto, live)
	}
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond) // ~10 engine ticks
	if n := artDmxCountFor(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("no ArtDmx for universe 0 while running on Art-Net; the rest of this test would prove nothing")
	}

	// Now switch the same scope to sACN.
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	if proto, live := h.rc.LiveProtocolFor(0); !live || proto != ProtocolSACN {
		t.Fatalf("after an sACN start, LiveProtocolFor(0) = (%q, %v), want (sacn, true)", proto, live)
	}

	h.tport.TakeSent()
	st := h.stream(t, 0)
	st.taken()
	h.clock.Advance(250 * time.Millisecond)

	if n := artDmxCountFor(t, h.tport.TakeSent(), 0); n != 0 {
		t.Fatalf("universe 0 is still being driven on Art-Net after selecting sACN: %d ArtDmx frames in 250ms", n)
	}
	sends := 0
	for _, op := range st.all() {
		if op.kind == "send" {
			sends++
			if op.universe != 1 {
				t.Fatalf("sACN send went to universe %d, want 1 (Art-Net 0 is show 1 is sACN 1)", op.universe)
			}
			if op.payload[0] != 255 {
				t.Fatalf("sACN payload slot 1 = %d, want 255", op.payload[0])
			}
		}
	}
	if sends == 0 {
		t.Fatal("nothing was transmitted over sACN after selecting it")
	}

	// And back again.
	if err := h.rc.Start(entries, ModeAllChannels, 255); err != nil {
		t.Fatal(err)
	}
	if proto, live := h.rc.LiveProtocolFor(0); !live || proto != ProtocolArtNet {
		t.Fatalf("after switching back, LiveProtocolFor(0) = (%q, %v), want (artnet, true)", proto, live)
	}
	if got := kinds(st.taken()); len(got) < 2 || got[len(got)-2] != "stop" || got[len(got)-1] != "close" {
		t.Fatalf("switching away from sACN ended with %v; the outgoing protocol must be stopped (zero frames + Stream_Terminated) and its socket closed", got)
	}
	st.taken()
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := artDmxCountFor(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("Art-Net did not resume for universe 0 after switching back")
	}
	for _, op := range st.all() {
		if op.kind == "send" {
			t.Fatalf("the sACN stream was still sending (universe %d) after the switch back to Art-Net", op.universe)
		}
	}
}

// TestNoUniverseIsEverDrivenByBothProtocols is the assertion that would fail
// if a future change made DMXOutputEngine emit E1.31 alongside ArtDmx.
func TestNoUniverseIsEverDrivenByBothProtocols(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 3, StartAddress: 1, Footprint: 4},
	}
	for _, proto := range []Protocol{ProtocolArtNet, ProtocolSACN, ProtocolArtNet} {
		if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, proto); err != nil {
			t.Fatalf("start on %s: %v", proto, err)
		}
		// Drain the handover itself: switching protocols blacks the outgoing
		// one out first, which legitimately puts one last frame on the OLD
		// protocol's wire. What must not happen is both protocols carrying
		// this universe from here on.
		h.tport.TakeSent()
		for _, s := range h.streams {
			s.taken()
		}
		h.clock.Advance(250 * time.Millisecond)
		for _, u := range []uint16{0, 3} {
			got, live := h.rc.LiveProtocolFor(u)
			if !live {
				t.Fatalf("universe %d is not live at all on %s", u, proto)
			}
			if got == "" {
				t.Fatalf("universe %d is registered on BOTH Art-Net and sACN at once", u)
			}
			if got != proto {
				t.Fatalf("universe %d is live on %q, want %q", u, got, proto)
			}
		}
		artnetFrames := artDmxCountFor(t, h.tport.TakeSent(), 0)
		sacnFrames := 0
		for _, s := range h.streams {
			for _, op := range s.all() {
				if op.kind == "send" {
					sacnFrames++
				}
			}
		}
		if artnetFrames > 0 && sacnFrames > 0 {
			t.Fatalf("on %s, universe 0 produced %d ArtDmx frames AND %d sACN frames in the same window", proto, artnetFrames, sacnFrames)
		}
		for _, s := range h.streams {
			s.taken()
		}
	}
}

// --- refusing rather than clamping ---------------------------------------

func TestStartOverSACNRefusesAnUnmappableUniverse(t *testing.T) {
	h := newSACNHarness(t)
	good := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := h.rc.Start(good, ModeAllChannels, 255); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("sACN universe must be 1..63999: show universe 1 (Art-Net Port-Address 0) would map to sACN universe 0")
	h.mapErr[0] = boom

	scope := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 3, StartAddress: 1, Footprint: 4},
	}
	err := h.rc.StartWithProtocol(scope, ModeAllChannels, 255, ProtocolSACN)
	if err == nil {
		t.Fatal("StartWithProtocol accepted a scope containing an unmappable universe")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the mapping error naming the universe", err)
	}
	if h.opens != 0 {
		t.Fatalf("a refused start opened %d sACN socket(s); it must refuse before opening anything", h.opens)
	}
	// The previous run is untouched: refusing must not also stop what was
	// already working.
	if proto, live := h.rc.LiveProtocolFor(0); !live || proto != ProtocolArtNet {
		t.Fatalf("a refused sACN start disturbed the running Art-Net check: LiveProtocolFor(0) = (%q, %v)", proto, live)
	}
	if !h.rc.State().Running {
		t.Fatal("a refused sACN start stopped the run that was already going")
	}
	// Universe 3 was mappable, but it must not have been started either:
	// the scope is refused as a whole.
	if _, live := h.rc.LiveProtocolFor(3); live {
		t.Fatal("universe 3 was started even though the scope as a whole was refused")
	}
}

func TestStartOverSACNWithoutABindingRefuses(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: clock, Transport: session.NewFakeTransport(), Rate: 40})
	t.Cleanup(dmx.Stop)
	rc := NewRigCheck(dmx) // no SetSACNBinding
	err := rc.StartWithProtocol([]Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}, ModeAllChannels, 255, ProtocolSACN)
	if !errors.Is(err, ErrSACNNotConfigured) {
		t.Fatalf("error = %v, want ErrSACNNotConfigured", err)
	}
	if rc.State().Running {
		t.Fatal("a refused start left the rig check running")
	}
}

func TestNormalizeProtocol(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Protocol
		ok   bool
	}{
		{"", ProtocolArtNet, true}, // absent means Art-Net: the compatibility contract
		{"artnet", ProtocolArtNet, true},
		{"sacn", ProtocolSACN, true},
		{"sACN", "", false},
		{"ArtNet", "", false},
		{"e131", "", false},
		{"artnet ", "", false},
	} {
		got, err := NormalizeProtocol(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("NormalizeProtocol(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("NormalizeProtocol(%q) = %q with no error; an unknown protocol must never fall back", tc.in, got)
		}
	}
}

// --- stop paths ----------------------------------------------------------

// TestStopEmitsTheSACNTerminationSequence covers the path
// web.handleStopAllOutput takes: it calls RigCheck.Stop() first, and that is
// the only thing in that handler that can terminate an E1.31 stream.
// DMX.Blackout() + DMX.Stop() afterwards touch Art-Net only.
func TestStopEmitsTheSACNTerminationSequence(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 3, StartAddress: 1, Footprint: 4},
	}
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	st := h.stream(t, 0)
	st.taken()

	h.rc.Stop()

	ops := st.all()
	stopped := map[uint16]bool{}
	closes := 0
	lastZeroFrame := map[uint16]bool{}
	for _, op := range ops {
		switch op.kind {
		case "send":
			allZero := true
			for _, b := range op.payload {
				if b != 0 {
					allZero = false
					break
				}
			}
			lastZeroFrame[op.universe] = allZero
		case "stop":
			if !lastZeroFrame[op.universe] {
				t.Fatalf("universe %d was terminated without a blackout frame first; the rig would be left lit if a receiver holds its last look", op.universe)
			}
			stopped[op.universe] = true
		case "close":
			closes++
			if len(stopped) != 2 {
				t.Fatalf("the socket was closed after terminating only %d universe(s); both must be stopped first", len(stopped))
			}
		}
	}
	// The harness maps raw -> raw+1, so Art-Net 0 and 3 are sACN 1 and 4.
	if !stopped[1] || !stopped[4] {
		t.Fatalf("Stop terminated %v, want both sACN universes 1 and 4", stopped)
	}
	if closes != 1 {
		t.Fatalf("the sACN socket was closed %d times, want exactly 1 (no socket leak, no double close)", closes)
	}
	if _, live := h.rc.LiveProtocolFor(0); live {
		t.Fatal("universe 0 is still live after Stop")
	}
}

// TestPatternWatchdogEmitsTheSACNTerminationSequence: a browser tab that dies
// mid-pattern must not leave receivers holding a stream until their 2.5s
// data-loss timeout (ANSI E1.31-2025 §6.7.1).
func TestPatternWatchdogEmitsTheSACNTerminationSequence(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	// Arm sACN with a classic start, then hand over to the pattern engine.
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	if _, err := h.rc.SetPatternTests(entries, []PatternSpec{{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	// StartPatternOutput supersedes the classic run, which closes that run's
	// socket and opens a fresh one — so the live stream is the last opened.
	st := h.stream(t, len(h.streams)-1)
	st.taken()

	// Nobody touches the pattern surface, so the watchdog must fire.
	h.clock.Advance(PatternWatchdogTimeout + time.Second)

	saw := false
	for _, op := range st.all() {
		if op.kind == "stop" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("the watchdog stopped the pattern without terminating the sACN stream; ops were %v", kinds(st.all()))
	}
	if h.rc.PatternStatus().LastEndReason != "watchdog" {
		t.Fatalf("LastEndReason = %q, want \"watchdog\"", h.rc.PatternStatus().LastEndReason)
	}
}

// --- cadence -------------------------------------------------------------

// TestSACNCadenceFollowsSection662 checks the two rules Section 6.6.2 states:
// three packets after a change, then a keep-alive every 800-1000ms.
func TestSACNCadenceFollowsSection662(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	st := h.stream(t, 0)

	// Start itself pushes the changed frame: that is packet 1 of the three.
	if n := len(st.all()); n != 1 {
		t.Fatalf("a changed frame produced %d packet(s) immediately, want 1", n)
	}
	// The remaining two arrive one burst interval apart.
	h.clock.Advance(SACNBurstInterval)
	if n := len(st.all()); n != 2 {
		t.Fatalf("after one burst interval there were %d packets, want 2", n)
	}
	h.clock.Advance(SACNBurstInterval)
	if n := len(st.all()); n != SACNSuppressionBurst {
		t.Fatalf("after two burst intervals there were %d packets, want %d (§6.6.2 exception 1)", n, SACNSuppressionBurst)
	}
	// Then suppression: nothing more until the keep-alive is due. The third
	// packet went out at t=2*SACNBurstInterval, so the keep-alive falls due
	// at t = 2*SACNBurstInterval + SACNKeepAliveInterval.
	h.clock.Advance(SACNKeepAliveInterval - SACNBurstInterval)
	if n := len(st.all()); n != SACNSuppressionBurst {
		t.Fatalf("unchanged data was retransmitted %d times before the keep-alive was due; §6.6.2 says transmit only on change", n-SACNSuppressionBurst)
	}
	h.clock.Advance(2 * SACNBurstInterval)
	if n := len(st.all()); n != SACNSuppressionBurst+1 {
		t.Fatalf("after the keep-alive window there were %d packets, want %d", n, SACNSuppressionBurst+1)
	}
	if SACNKeepAliveInterval < 800*time.Millisecond || SACNKeepAliveInterval > time.Second {
		t.Fatalf("SACNKeepAliveInterval is %v; §6.6.2 exception 2 requires 800mS..1000mS", SACNKeepAliveInterval)
	}
	if SACNKeepAliveInterval >= 2500*time.Millisecond {
		t.Fatalf("SACNKeepAliveInterval %v is at or past E131_NETWORK_DATA_LOSS_TIMEOUT", SACNKeepAliveInterval)
	}

	// A real change restarts the burst.
	st.taken()
	if err := h.rc.SetLevel(128); err != nil {
		t.Fatal(err)
	}
	if n := len(st.all()); n != 1 {
		t.Fatalf("a level change produced %d packets immediately, want 1", n)
	}
	h.clock.Advance(2 * SACNBurstInterval)
	if n := len(st.all()); n != SACNSuppressionBurst {
		t.Fatalf("a level change produced %d packets in its burst, want %d", n, SACNSuppressionBurst)
	}
	for _, op := range st.all() {
		if op.payload[0] != 128 {
			t.Fatalf("burst packet carries slot 1 = %d, want the new level 128", op.payload[0])
		}
	}
}

// TestBlackoutAlwaysPutsAPacketOnTheWire: the panic button must not be eaten
// by §6.6.2's change-detection when the frame was already zero.
func TestBlackoutAlwaysPutsAPacketOnTheWire(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 0, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	st := h.stream(t, 0)
	h.rc.Blackout()
	st.taken()
	h.rc.Blackout() // already black
	if n := len(st.all()); n == 0 {
		t.Fatal("a second Blackout transmitted nothing; \"a hard all-off that always works\" must always put a frame on the wire")
	}
}

// --- socket lifecycle ----------------------------------------------------

func TestSACNSocketIsOpenedLazilyAndClosedOnce(t *testing.T) {
	h := newSACNHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}

	if err := h.rc.Start(entries, ModeAllChannels, 255); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Second)
	if h.opens != 0 {
		t.Fatalf("an Art-Net-only run opened %d sACN socket(s); the socket must be opened only when sACN is actually needed", h.opens)
	}

	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	if h.opens != 1 {
		t.Fatalf("opens = %d after one sACN start, want 1", h.opens)
	}
	h.clock.Advance(3 * time.Second)
	if h.opens != 1 {
		t.Fatalf("opens = %d after three seconds of sACN output, want 1 (one socket per run, not one per tick)", h.opens)
	}

	h.rc.Stop()
	h.rc.Stop() // idempotent
	closes := 0
	for _, op := range h.stream(t, 0).all() {
		if op.kind == "close" {
			closes++
		}
	}
	if closes != 1 {
		t.Fatalf("closes = %d, want exactly 1", closes)
	}

	// A second sACN run opens a fresh socket rather than reusing a closed one.
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	if h.opens != 2 {
		t.Fatalf("opens = %d after a second sACN run, want 2", h.opens)
	}
}

func TestSACNOpenFailureLeavesNothingRunning(t *testing.T) {
	h := newSACNHarness(t)
	h.openErr = fmt.Errorf("bind: address already in use")
	err := h.rc.StartWithProtocol([]Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}, ModeAllChannels, 255, ProtocolSACN)
	if err == nil {
		t.Fatal("StartWithProtocol succeeded with an unopenable socket")
	}
	if h.rc.State().Running {
		t.Fatal("a failed sACN start left the rig check running")
	}
	if _, live := h.rc.LiveProtocolFor(0); live {
		t.Fatal("a failed sACN start left universe 0 registered")
	}
}

// TestProtocolIsEchoedInState covers the status surface the HTTP layer maps
// straight through.
func TestProtocolIsEchoedInState(t *testing.T) {
	h := newSACNHarness(t)
	if got := h.rc.State().Protocol; got != ProtocolArtNet {
		t.Fatalf("a fresh RigCheck reports protocol %q, want %q", got, ProtocolArtNet)
	}
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := h.rc.StartWithProtocol(entries, ModeAllChannels, 255, ProtocolSACN); err != nil {
		t.Fatal(err)
	}
	if got := h.rc.State().Protocol; got != ProtocolSACN {
		t.Fatalf("State().Protocol = %q after an sACN start, want %q", got, ProtocolSACN)
	}
	h.rc.Stop()
	if got := h.rc.State().Protocol; got != ProtocolSACN {
		t.Fatalf("State().Protocol = %q after Stop, want the armed protocol %q to survive", got, ProtocolSACN)
	}
}
