package session

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
)

func newDMX(t *testing.T, cfg DMXConfig) (*DMXOutputEngine, *FakeClock, *FakeTransport) {
	t.Helper()
	clock := NewFakeClock(time.Time{})
	tr := NewFakeTransport()
	cfg.Clock = clock
	cfg.Transport = tr
	e := NewDMXOutputEngine(cfg)
	t.Cleanup(e.Stop)
	return e, clock, tr
}

func dmxPackets(t *testing.T, sent []SentPacket) []artnet.Dmx {
	t.Helper()
	out := make([]artnet.Dmx, 0, len(sent))
	for _, sp := range sent {
		if sp.DecodeErr != nil {
			t.Fatalf("undecodable ArtDmx: %v", sp.DecodeErr)
		}
		if sp.Packet.Kind != artnet.KindDmx {
			t.Fatalf("packet kind = %v, want ArtDmx", sp.Packet.Kind)
		}
		out = append(out, sp.Packet.Dmx)
	}
	return out
}

// --- tick cadence -----------------------------------------------------

func TestDMXDefaultRateIs40Hz(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	if got, want := e.Interval(), 25*time.Millisecond; got != want {
		t.Fatalf("default interval = %v, want %v (40 Hz)", got, want)
	}
}

func TestDMXTickCadence(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Rate: 40})
	pa := mustPortAddress(t, 0, 0, 1)
	e.StartUniverse(pa, nodeAddr, 0)
	e.Start()

	// Nothing goes out until the first tick — Start must not emit a stale
	// all-zero frame before the caller has loaded any levels.
	if got := tr.SentCount(); got != 0 {
		t.Fatalf("packets before first tick = %d, want 0", got)
	}
	clock.Advance(24 * time.Millisecond)
	if got := tr.SentCount(); got != 0 {
		t.Fatalf("packets at 24 ms = %d, want 0", got)
	}
	clock.Advance(time.Millisecond)
	if got := tr.SentCount(); got != 1 {
		t.Fatalf("packets at 25 ms = %d, want 1", got)
	}

	tr.TakeSent()
	clock.Advance(time.Second) // 40 more ticks
	if got := len(tr.TakeSent()); got != 40 {
		t.Fatalf("packets in 1 s at 40 Hz = %d, want 40", got)
	}
}

func TestDMXStopHaltsAndResumesWithoutResettingSequence(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Rate: 100})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	e.Start()
	clock.Advance(30 * time.Millisecond) // 3 ticks
	last := dmxPackets(t, tr.TakeSent())
	if len(last) != 3 {
		t.Fatalf("ticks = %d, want 3", len(last))
	}
	lastSeq := last[2].Sequence

	e.Stop()
	clock.Advance(time.Second)
	if got := tr.SentCount(); got != 0 {
		t.Fatalf("packets after Stop = %d, want 0", got)
	}
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending after Stop = %d, want 0", got)
	}

	e.Start()
	clock.Advance(10 * time.Millisecond)
	resumed := dmxPackets(t, tr.TakeSent())
	if len(resumed) != 1 {
		t.Fatalf("packets after resume = %d, want 1", len(resumed))
	}
	if got, want := resumed[0].Sequence, lastSeq+1; got != want {
		t.Fatalf("sequence after resume = %d, want %d (continues, not reset)", got, want)
	}
}

// --- sequence numbering -----------------------------------------------

func TestDMXSequenceStartsAtOneAndWrapsSkippingZero(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	e.Start()

	// 260 ticks walks past the 255→1 wrap.
	clock.Advance(260 * time.Millisecond)
	pkts := dmxPackets(t, tr.TakeSent())
	if len(pkts) != 260 {
		t.Fatalf("packets = %d, want 260", len(pkts))
	}
	if pkts[0].Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", pkts[0].Sequence)
	}
	if pkts[254].Sequence != 255 {
		t.Fatalf("255th sequence = %d, want 255", pkts[254].Sequence)
	}
	// 0 is reserved for "sequencing disabled" and must never appear.
	if pkts[255].Sequence != 1 {
		t.Fatalf("sequence after 255 = %d, want 1 (0 is reserved)", pkts[255].Sequence)
	}
	for i, p := range pkts {
		if p.Sequence == 0 {
			t.Fatalf("packet %d used reserved sequence 0", i)
		}
	}
}

func TestDMXSequenceIsPerUniverse(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	a := mustPortAddress(t, 0, 0, 1)
	b := mustPortAddress(t, 0, 0, 2)
	e.StartUniverse(a, nodeAddr, 0)
	e.Start()
	clock.Advance(5 * time.Millisecond) // universe a reaches sequence 5

	e.StartUniverse(b, nodeAddr, 0)
	tr.TakeSent()
	clock.Advance(time.Millisecond)

	pkts := dmxPackets(t, tr.TakeSent())
	if len(pkts) != 2 {
		t.Fatalf("packets = %d, want 2", len(pkts))
	}
	seq := map[byte]byte{}
	for _, p := range pkts {
		seq[p.SubUni] = p.Sequence
	}
	if seq[a.SubUni()] != 6 {
		t.Fatalf("universe 1 sequence = %d, want 6", seq[a.SubUni()])
	}
	if seq[b.SubUni()] != 1 {
		t.Fatalf("universe 2 sequence = %d, want 1 (its own counter)", seq[b.SubUni()])
	}
}

func TestDMXSequencingDisabledSendsZero(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond, DisableSequencing: true})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	e.Start()
	clock.Advance(5 * time.Millisecond)

	for i, p := range dmxPackets(t, tr.TakeSent()) {
		if p.Sequence != 0 {
			t.Fatalf("packet %d sequence = %d, want 0 with sequencing disabled", i, p.Sequence)
		}
	}
}

// --- keep-alive -------------------------------------------------------

func TestDMXResendsUnchangedFramesEveryTick(t *testing.T) {
	// Art-Net receivers drop a source that goes quiet for ~1 s, so an
	// unchanged frame must still be retransmitted on every tick.
	e, clock, tr := newDMX(t, DMXConfig{Interval: 10 * time.Millisecond})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	if err := e.SetChannels(pa, 1, []byte{255, 128}); err != nil {
		t.Fatalf("SetChannels: %v", err)
	}
	e.Start()
	clock.Advance(100 * time.Millisecond)

	pkts := dmxPackets(t, tr.TakeSent())
	if len(pkts) != 10 {
		t.Fatalf("packets = %d, want 10 keep-alive resends", len(pkts))
	}
	for i, p := range pkts {
		if p.Data[0] != 255 || p.Data[1] != 128 {
			t.Fatalf("packet %d lost its payload: %v", i, p.Data[:4])
		}
		if len(p.Data) != DMXUniverseSize {
			t.Fatalf("packet %d length = %d, want 512", i, len(p.Data))
		}
	}
	if n, _ := e.FrameCount(pa); n != 10 {
		t.Fatalf("FrameCount = %d, want 10", n)
	}
}

// --- addressing and buffers -------------------------------------------

func TestDMXPortAddressEncoding(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	pa := mustPortAddress(t, 5, 9, 3)
	e.StartUniverse(pa, nodeAddr, 0)
	e.Start()
	clock.Advance(time.Millisecond)

	p := dmxPackets(t, tr.TakeSent())[0]
	if p.Net != 5 {
		t.Fatalf("Net = %d, want 5", p.Net)
	}
	if got, want := p.SubUni, byte(0x93); got != want {
		t.Fatalf("SubUni = 0x%02X, want 0x%02X", got, want)
	}
	if got := p.PortAddress(); got != pa {
		t.Fatalf("round-tripped Port-Address = %+v, want %+v", got, pa)
	}
}

func TestDMXUnicastVersusBroadcast(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	uni := mustPortAddress(t, 0, 0, 1)
	bcast := mustPortAddress(t, 0, 0, 2)
	e.StartUniverse(uni, nodeAddr, 0)
	e.StartUniverse(bcast, netip.AddrPort{}, 0)
	e.Start()
	clock.Advance(time.Millisecond)

	sent := tr.TakeSent()
	if len(sent) != 2 {
		t.Fatalf("packets = %d, want 2", len(sent))
	}
	for _, sp := range sent {
		switch sp.Packet.Dmx.SubUni {
		case uni.SubUni():
			if sp.Broadcast || sp.Dst != nodeAddr {
				t.Fatalf("universe 1 should unicast to %v, got broadcast=%v dst=%v", nodeAddr, sp.Broadcast, sp.Dst)
			}
		case bcast.SubUni():
			if !sp.Broadcast {
				t.Fatal("universe 2 has no destination and should broadcast")
			}
		}
	}
}

func TestDMXSetFrameReplacesWholeBuffer(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)

	if err := e.SetChannels(pa, 100, []byte{7, 7, 7}); err != nil {
		t.Fatalf("SetChannels: %v", err)
	}
	if err := e.SetFrame(pa, []byte{1, 2, 3}); err != nil {
		t.Fatalf("SetFrame: %v", err)
	}
	frame, ok := e.Frame(pa)
	if !ok {
		t.Fatal("Frame missing")
	}
	if frame[0] != 1 || frame[1] != 2 || frame[2] != 3 {
		t.Fatalf("frame head = %v", frame[:3])
	}
	if frame[99] != 0 {
		t.Fatalf("SetFrame left slot 100 at %d, want 0 (whole-frame replace)", frame[99])
	}
}

func TestDMXSetChannelsIsAddressedFromOne(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)

	if err := e.SetChannels(pa, 512, []byte{200}); err != nil {
		t.Fatalf("SetChannels at 512: %v", err)
	}
	frame, _ := e.Frame(pa)
	if frame[511] != 200 {
		t.Fatalf("slot 512 = %d, want 200", frame[511])
	}
	if err := e.SetChannels(pa, 0, []byte{1}); !errors.Is(err, ErrChannelOutOfRange) {
		t.Fatalf("SetChannels(0) err = %v, want ErrChannelOutOfRange", err)
	}
	if err := e.SetChannels(pa, 512, []byte{1, 2}); !errors.Is(err, ErrChannelOutOfRange) {
		t.Fatalf("overrunning SetChannels err = %v, want ErrChannelOutOfRange", err)
	}
	if err := e.SetFrame(pa, make([]byte, 513)); !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("SetFrame(513) err = %v, want ErrFrameTooLong", err)
	}
}

func TestDMXUnstartedUniverseRejectsWrites(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	pa := mustPortAddress(t, 0, 0, 0)
	if err := e.SetFrame(pa, []byte{1}); !errors.Is(err, ErrUniverseNotStarted) {
		t.Fatalf("SetFrame err = %v, want ErrUniverseNotStarted", err)
	}
	if err := e.SetChannels(pa, 1, []byte{1}); !errors.Is(err, ErrUniverseNotStarted) {
		t.Fatalf("SetChannels err = %v, want ErrUniverseNotStarted", err)
	}
}

func TestDMXStopUniverseHaltsThatUniverseOnly(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	a := mustPortAddress(t, 0, 0, 1)
	b := mustPortAddress(t, 0, 0, 2)
	e.StartUniverse(a, nodeAddr, 0)
	e.StartUniverse(b, nodeAddr, 0)
	e.Start()
	clock.Advance(time.Millisecond)
	if got := len(tr.TakeSent()); got != 2 {
		t.Fatalf("packets = %d, want 2", got)
	}

	e.StopUniverse(a)
	clock.Advance(time.Millisecond)
	pkts := dmxPackets(t, tr.TakeSent())
	if len(pkts) != 1 || pkts[0].SubUni != b.SubUni() {
		t.Fatalf("after StopUniverse got %d packets for %v", len(pkts), pkts)
	}
	if _, ok := e.Frame(a); ok {
		t.Fatal("stopped universe still has a frame buffer")
	}
}

func TestDMXOddSlotCountIsRoundedUp(t *testing.T) {
	// Art-Net requires an even data length (protocol reference §3.4).
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 13)
	e.Start()
	clock.Advance(time.Millisecond)

	p := dmxPackets(t, tr.TakeSent())[0]
	if len(p.Data) != 14 {
		t.Fatalf("slot count = %d, want 14 (13 rounded up to even)", len(p.Data))
	}
}

func TestDMXSendNowTransmitsOutsideTheTick(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Interval: time.Second})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	e.SendNow()
	if got := len(tr.TakeSent()); got != 1 {
		t.Fatalf("SendNow sent %d packets, want 1", got)
	}
}

// --- Blackout (full-reset flow / any "never leave the rig lit" caller) --

func TestBlackoutZeroesEveryStartedUniverseAndPushesImmediately(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Interval: time.Second}) // engine never Started: Blackout must not depend on the tick loop
	paA := mustPortAddress(t, 0, 0, 0)
	paB := mustPortAddress(t, 0, 0, 1)
	e.StartUniverse(paA, nodeAddr, 0)
	e.StartUniverse(paB, nodeAddr, 0)
	if err := e.SetFrame(paA, []byte{255, 200, 100}); err != nil {
		t.Fatalf("SetFrame A: %v", err)
	}
	if err := e.SetFrame(paB, []byte{50, 60}); err != nil {
		t.Fatalf("SetFrame B: %v", err)
	}
	tr.TakeSent() // discard nothing-yet-sent baseline

	e.Blackout()

	sent := dmxPackets(t, tr.TakeSent())
	if len(sent) != 2 {
		t.Fatalf("Blackout sent %d packets, want 2 (one per started universe)", len(sent))
	}
	frameA, ok := e.Frame(paA)
	if !ok {
		t.Fatal("universe A should still be started after Blackout")
	}
	for i, b := range frameA {
		if b != 0 {
			t.Fatalf("frame A slot %d = %d, want 0 after Blackout", i, b)
		}
	}
	frameB, ok := e.Frame(paB)
	if !ok {
		t.Fatal("universe B should still be started after Blackout")
	}
	for i, b := range frameB {
		if b != 0 {
			t.Fatalf("frame B slot %d = %d, want 0 after Blackout", i, b)
		}
	}
}

func TestBlackoutOnNoStartedUniversesIsANoOp(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{})
	e.Blackout() // must not panic
	if got := len(tr.TakeSent()); got != 0 {
		t.Fatalf("Blackout with no started universes sent %d packets, want 0", got)
	}
}

func TestDMXRecordsSendErrors(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond})
	pa := mustPortAddress(t, 0, 0, 0)
	e.StartUniverse(pa, nodeAddr, 0)
	boom := errors.New("network down")
	tr.SetSendError(boom)
	e.Start()
	clock.Advance(time.Millisecond)

	if err := e.LastSendError(); !errors.Is(err, boom) {
		t.Fatalf("LastSendError = %v, want %v", err, boom)
	}
	if n, _ := e.FrameCount(pa); n != 0 {
		t.Fatalf("FrameCount = %d, want 0 (failed sends are not counted)", n)
	}
}
