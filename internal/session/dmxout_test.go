package session

import (
	"bytes"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
)

// Tests for the unified output engine (C3). Every assertion decodes the
// real bytes the engine handed its transports: ArtDmx through artnet.Decode
// at FakeTransport, E1.31 by octet offset (ANSI E1.31 Table B-13) at a fake
// sACN link — never by re-encoding with the code under test.

func newDMX(t *testing.T, cfg DMXConfig) (*DMXOutputEngine, *FakeClock, *FakeTransport) {
	t.Helper()
	clock := NewFakeClock(time.Time{})
	tr := NewFakeTransport()
	cfg.Clock = clock
	cfg.Transport = tr
	e := NewDMXOutputEngine(cfg)
	t.Cleanup(e.Disarm)
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

func dmxFor(t *testing.T, sent []SentPacket, raw uint16) []artnet.Dmx {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []artnet.Dmx
	for _, d := range dmxPackets(t, sent) {
		if d.SubUni == pa.SubUni() && d.Net == pa.Net {
			out = append(out, d)
		}
	}
	return out
}

func isZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// --- fake sACN link ---------------------------------------------------------

type e131 struct {
	dst        netip.AddrPort
	universe   uint16
	priority   byte
	sequence   byte
	terminated bool
	cid        []byte
	slots      []byte
}

// parseE131 reads an E1.31 Data Packet by octet offset (Table B-13).
func parseE131(t *testing.T, b []byte, dst netip.AddrPort) e131 {
	t.Helper()
	if len(b) != 638 || !bytes.Equal(b[4:16], []byte("ASC-E1.17\x00\x00\x00")) {
		t.Fatalf("not an E1.31 Data Packet carrying 512 slots: %d octets", len(b))
	}
	return e131{
		dst: dst, universe: uint16(b[113])<<8 | uint16(b[114]), priority: b[108],
		sequence: b[111], terminated: b[112]&0x40 != 0, cid: append([]byte(nil), b[22:38]...),
		slots: append([]byte(nil), b[126:]...),
	}
}

type fakeLink struct {
	mu   sync.Mutex
	sent []struct {
		data []byte
		dst  netip.AddrPort
	}
	closed int
}

func (l *fakeLink) Send(data []byte, dst netip.AddrPort) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, struct {
		data []byte
		dst  netip.AddrPort
	}{append([]byte(nil), data...), dst})
	return nil
}
func (l *fakeLink) Close() error { l.mu.Lock(); l.closed++; l.mu.Unlock(); return nil }
func (l *fakeLink) take(t *testing.T) []e131 {
	l.mu.Lock()
	s := l.sent
	l.sent = nil
	l.mu.Unlock()
	out := make([]e131, 0, len(s))
	for _, p := range s {
		out = append(out, parseE131(t, p.data, p.dst))
	}
	return out
}

// sacnRig wires a fake sACN link that records which adapter it was opened
// for, with the linear show→sACN mapping (start universe 1, Art-Net start 0).
type sacnRig struct {
	link    *fakeLink
	opens   int
	adapter string
	fail    error
}

func (r *sacnRig) binding(adapter string, unicast netip.Addr) SACNBinding {
	return SACNBinding{
		Open: func() (SACNLink, SACNParams, error) {
			if r.fail != nil {
				return nil, SACNParams{}, r.fail
			}
			r.opens++
			r.adapter = adapter
			r.link = &fakeLink{}
			return r.link, SACNParams{CID: [16]byte{1, 2, 3}, Priority: 120, UnicastTo: unicast, Adapter: adapter}, nil
		},
		Universe: func(raw uint16) (uint16, error) {
			if raw == 0x7FFF { // stands in for a start-universe pair that maps outside 1-63999
				return 0, errors.New("no sACN universe")
			}
			return raw + 1, nil
		},
	}
}

// --- tick cadence and Art-Net framing (carried over from the pre-C3 engine) --

func TestDMXDefaultRateIs40Hz(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	if got, want := e.Interval(), 25*time.Millisecond; got != want {
		t.Fatalf("default interval = %v, want %v (40 Hz)", got, want)
	}
}

func TestDMXTickCadenceWhileArmed(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Rate: 40})
	if err := e.SetFrame(SourceRaw, 1, []byte{9}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if got := tr.SentCount(); got != 0 {
		t.Fatalf("disarmed for 1 s: %d packets, want 0", got)
	}
	e.Arm("a")
	tr.TakeSent() // Arm sends the current look at once
	clock.Advance(24 * time.Millisecond)
	if got := tr.SentCount(); got != 0 {
		t.Fatalf("packets at 24 ms = %d, want 0", got)
	}
	clock.Advance(time.Millisecond)
	if got := tr.SentCount(); got != 1 {
		t.Fatalf("packets at 25 ms = %d, want 1", got)
	}
	tr.TakeSent()
	clock.Advance(time.Second)
	if got := len(tr.TakeSent()); got != 40 {
		t.Fatalf("packets in 1 s at 40 Hz = %d, want 40 (unchanged frames are resent every tick)", got)
	}
}

func TestDMXSequenceStartsAtOneAndWrapsSkippingZero(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond, Lease: -1})
	_ = e.SetFrame(SourceRaw, 0, nil)
	e.Arm("a")
	clock.Advance(259 * time.Millisecond)
	pkts := dmxPackets(t, tr.TakeSent())
	if len(pkts) != 260 {
		t.Fatalf("packets = %d, want 260", len(pkts))
	}
	if pkts[0].Sequence != 1 || pkts[254].Sequence != 255 || pkts[255].Sequence != 1 {
		t.Fatalf("sequences 1st/255th/256th = %d/%d/%d, want 1/255/1", pkts[0].Sequence, pkts[254].Sequence, pkts[255].Sequence)
	}
	for i, p := range pkts {
		if p.Sequence == 0 {
			t.Fatalf("packet %d used reserved sequence 0", i)
		}
	}
}

func TestDMXSequenceIsPerUniverse(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond, Lease: -1})
	_ = e.SetFrame(SourceRaw, 1, nil)
	e.Arm("a")
	clock.Advance(4 * time.Millisecond) // universe 1 reaches sequence 5
	_ = e.SetFrame(SourceRaw, 2, nil)
	tr.TakeSent()
	clock.Advance(time.Millisecond)
	sent := tr.TakeSent()
	if a, b := dmxFor(t, sent, 1), dmxFor(t, sent, 2); len(a) != 1 || len(b) != 1 || a[0].Sequence != 6 || b[0].Sequence != 1 {
		t.Fatalf("per-universe sequence: universe 1 %v, universe 2 %v; want 6 and 1", a, b)
	}
}

func TestDMXSequencingDisabledSendsZero(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Interval: time.Millisecond, DisableSequencing: true, Lease: -1})
	_ = e.SetFrame(SourceRaw, 0, nil)
	e.Arm("a")
	clock.Advance(5 * time.Millisecond)
	for _, p := range dmxPackets(t, tr.TakeSent()) {
		if p.Sequence != 0 {
			t.Fatalf("sequence = %d, want 0 with sequencing disabled", p.Sequence)
		}
	}
}

func TestDMXPortAddressEncodingAndBroadcast(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	pa := mustPortAddress(t, 0x12, 0x3, 0x4)
	_ = e.SetFrame(SourceRaw, pa.RawValue(), []byte{1})
	e.Arm("a")
	sent := tr.TakeSent()
	if len(sent) != 1 || !sent[0].Broadcast {
		t.Fatalf("want one broadcast ArtDmx, got %d (broadcast=%v)", len(sent), len(sent) == 1 && sent[0].Broadcast)
	}
	d := sent[0].Packet.Dmx
	if d.Net != 0x12 || d.SubUni != 0x34 || len(d.Data) != 512 || d.Data[0] != 1 {
		t.Fatalf("ArtDmx Net=%#x SubUni=%#x len=%d slot1=%d; want 0x12, 0x34, 512, 1", d.Net, d.SubUni, len(d.Data), d.Data[0])
	}
}

func TestDMXWriteValidation(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	if err := e.SetChannels(SourceRaw, 0, 0, []byte{1}); !errors.Is(err, ErrChannelOutOfRange) {
		t.Errorf("channel 0: %v, want ErrChannelOutOfRange", err)
	}
	if err := e.SetChannels(SourceRaw, 0, 512, []byte{1, 2}); !errors.Is(err, ErrChannelOutOfRange) {
		t.Errorf("past 512: %v, want ErrChannelOutOfRange", err)
	}
	if err := e.SetFrame(SourceRaw, 0, make([]byte, 513)); !errors.Is(err, ErrFrameTooLong) {
		t.Errorf("513 slots: %v, want ErrFrameTooLong", err)
	}
	if err := e.SetFrame(SourceRaw, 0x8000, nil); !errors.Is(err, ErrUniverseOutOfRange) {
		t.Errorf("universe 0x8000: %v, want ErrUniverseOutOfRange", err)
	}
	if err := e.SetChannels(SourceRaw, 0, 512, []byte{200}); err != nil {
		t.Fatal(err)
	}
	f, _ := e.Frame(mustPortAddress(t, 0, 0, 0))
	if f[511] != 200 {
		t.Errorf("SetChannels(512) wrote slot %d, want slot 512", 511)
	}
}

func TestDMXRecordsSendErrors(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{Lease: -1})
	_ = e.SetFrame(SourceRaw, 0, []byte{1})
	e.Arm("a")
	boom := errors.New("network unreachable")
	tr.SetSendError(boom)
	clock.Advance(25 * time.Millisecond)
	if !errors.Is(e.LastSendError(), boom) {
		t.Fatalf("LastSendError = %v, want %v", e.LastSendError(), boom)
	}
	if st := e.Status(); st.Error == "" {
		t.Errorf("Status().Error is empty after a failed send; the UI must be able to say Output error")
	}
}

// --- the master Arm ----------------------------------------------------------

func TestDisarmedEngineSendsNoDMXFromAnySource(t *testing.T) {
	rig := &sacnRig{}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("sacn-nic", netip.Addr{})})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputBoth})
	_ = e.SetFrame(SourceTests, 0, []byte{1})
	_ = e.SetChannels(SourceProgrammer, 0, 2, []byte{2})
	_ = e.SetFrame(SourceRaw, 3, []byte{3})
	_ = e.SetIdentify(map[Stream][]byte{{WireArtNet, 9}: {255}, {WireSACN, 9}: {255}})
	e.Push(true, 0, 3)
	clock.Advance(5 * time.Second)
	if n := tr.SentCount(); n != 0 {
		t.Errorf("disarmed: %d ArtDmx datagrams, want 0", n)
	}
	if rig.opens != 0 {
		t.Errorf("disarmed: the sACN socket was opened %d times, want never", rig.opens)
	}
	if e.OutputRunning() {
		t.Errorf("OutputRunning() true while disarmed")
	}
}

func TestDisarmBlacksOutEveryStreamThenSilence(t *testing.T) {
	rig := &sacnRig{}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("sacn-nic", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputBoth})
	_ = e.SetFrame(SourceRaw, 0, []byte{200})
	_ = e.SetFrame(SourceRaw, 1, []byte{100})
	e.Arm("a")
	clock.Advance(100 * time.Millisecond)
	tr.TakeSent()
	rig.link.take(t)
	link := rig.link

	e.Disarm()
	sent := tr.TakeSent()
	for _, raw := range []uint16{0, 1} {
		frames := dmxFor(t, sent, raw)
		if len(frames) != BlackoutFrameCount {
			t.Errorf("universe %d: %d ArtDmx on disarm, want %d", raw, len(frames), BlackoutFrameCount)
		}
		for _, f := range frames {
			if !isZero(f.Data) {
				t.Errorf("universe %d: a disarm frame is not all zero", raw)
			}
		}
	}
	pk := link.take(t)
	if len(pk) != 6 {
		t.Fatalf("sACN on disarm: %d packets, want 3 zero + 3 Stream_Terminated", len(pk))
	}
	for i, p := range pk {
		if !isZero(p.slots) || p.terminated != (i >= 3) || p.universe != 1 {
			t.Errorf("sACN disarm packet %d: universe=%d terminated=%v zero=%v; want universe 1, zero, terminated only for the last three", i, p.universe, p.terminated, isZero(p.slots))
		}
		if i > 0 && p.sequence != pk[i-1].sequence+1 {
			t.Errorf("sACN disarm packet %d sequence %d does not continue %d (Section 6.2.5)", i, p.sequence, pk[i-1].sequence)
		}
	}
	if link.closed != 1 {
		t.Errorf("sACN socket closed %d times on disarm, want 1", link.closed)
	}
	clock.Advance(5 * time.Second)
	if n := tr.SentCount(); n != 0 || len(link.take(t)) != 0 {
		t.Errorf("after disarm: %d ArtDmx and some E1.31 still sent; want silence", n)
	}
	if clock.PendingTimers() != 0 {
		t.Errorf("timers pending after disarm = %d, want 0", clock.PendingTimers())
	}
	if st := e.Status(); st.State != StateDisarmed || st.LastDisarm != "operator" {
		t.Errorf("status %q/%q, want disarmed/operator", st.State, st.LastDisarm)
	}
	// Sources survive: re-arming restores the live look.
	e.Arm("a")
	if f := dmxFor(t, tr.TakeSent(), 0); len(f) != 1 || f[0].Data[0] != 200 {
		t.Errorf("re-arm did not restore the live look")
	}
}

// --- the lease ---------------------------------------------------------------

func TestLeaseHeldByAnyClientLostWhenAllSilent(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{})
	_ = e.SetFrame(SourceRaw, 0, []byte{5})
	e.Arm("laptop")
	for i := 0; i < 20; i++ {
		clock.Advance(time.Second)
		e.Heartbeat("phone")
	}
	if e.State() != StateArmed {
		t.Fatalf("one client heartbeating: state %q, want armed", e.State())
	}
	if st := e.Status(); st.Browsers != 1 {
		t.Errorf("browsers = %d, want 1 (the laptop went silent long ago)", st.Browsers)
	}
	tr.TakeSent()
	clock.Advance(4975 * time.Millisecond)
	if e.State() != StateArmed {
		t.Fatalf("4.975 s silent: state %q, want armed", e.State())
	}
	clock.Advance(25 * time.Millisecond)
	if e.State() != StateDisarmed || e.Status().LastDisarm != "lease" {
		t.Fatalf("5 s silent: state %q (%q), want disarmed by lease", e.State(), e.Status().LastDisarm)
	}
	frames := dmxFor(t, tr.TakeSent(), 0)
	if len(frames) < BlackoutFrameCount || !isZero(frames[len(frames)-1].Data) {
		t.Errorf("lease loss with Blackout did not end in zero frames")
	}
	e.Heartbeat("phone")
	if e.State() != StateDisarmed {
		t.Errorf("a heartbeat re-armed the engine; only Arm may")
	}
}

func TestLeaseLossHoldKeepsTheWireFrozenUntilReArm(t *testing.T) {
	rig := &sacnRig{}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{})})
	e.SetLeaseLossAction(LeaseLossHold)
	e.SetRouting(map[uint16]OutputProtocols{0: OutputBoth})
	_ = e.SetFrame(SourceRaw, 0, []byte{200})
	e.Arm("laptop")
	clock.Advance(5 * time.Second)
	if e.State() != StateHolding {
		t.Fatalf("lease lost with Hold: state %q, want holding", e.State())
	}
	tr.TakeSent()
	rig.link.take(t)
	_ = e.SetFrame(SourceRaw, 0, []byte{50})
	_ = e.SetFrame(SourceRaw, 7, []byte{50})
	e.Push(true, 0, 7)
	clock.Advance(2 * time.Second)
	sent := tr.TakeSent()
	if len(dmxFor(t, sent, 7)) != 0 {
		t.Errorf("holding: a new universe reached the wire")
	}
	frames := dmxFor(t, sent, 0)
	if len(frames) < 79 {
		t.Errorf("holding 2 s: %d Art-Net frames, want the held look at 40 Hz", len(frames))
	}
	for _, f := range frames {
		if f.Data[0] != 200 {
			t.Fatalf("holding: slot 1 = %d, want the held 200", f.Data[0])
		}
	}
	pk := rig.link.take(t)
	if len(pk) < 2 || len(pk) > 3 {
		t.Errorf("holding 2 s on sACN: %d keep-alives, want 2 or 3 (one per 850 ms, Section 6.6.2)", len(pk))
	}
	for _, p := range pk {
		if p.slots[0] != 200 {
			t.Errorf("sACN keep-alive while holding carries %d, want the held 200", p.slots[0])
		}
	}
	e.Heartbeat("phone")
	if e.State() != StateHolding {
		t.Errorf("a heartbeat ended the hold; only Arm may")
	}
	e.Arm("phone")
	sent = tr.TakeSent()
	if f := dmxFor(t, sent, 0); len(f) == 0 || f[len(f)-1].Data[0] != 50 {
		t.Errorf("re-arm: the live edit did not reach the wire")
	}
	if f := dmxFor(t, sent, 7); len(f) == 0 {
		t.Errorf("re-arm: the universe added while holding did not start")
	}
}

func TestGoodbyeFromLastClientLosesTheLeaseAtOnce(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	_ = e.SetFrame(SourceRaw, 0, []byte{5})
	e.Arm("laptop")
	e.Heartbeat("phone")
	e.Goodbye("laptop")
	if e.State() != StateArmed {
		t.Fatalf("goodbye with another client present: %q, want armed", e.State())
	}
	e.Goodbye("phone")
	if e.State() != StateDisarmed || e.Status().LastDisarm != "lease" {
		t.Fatalf("goodbye from the last client: %q, want disarmed by lease", e.State())
	}
}

// --- composition ---------------------------------------------------------------

func TestCompositionPriorityPerChannel(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	tests := make([]byte, 512)
	for i := range tests {
		tests[i] = 10
	}
	_ = e.SetFrame(SourceTests, 0, tests)                     // tests: every slot
	_ = e.SetChannels(SourceProgrammer, 0, 2, []byte{20, 21}) // programmer: slots 2-3
	_ = e.SetChannels(SourceRaw, 0, 3, []byte{30})            // raw: slot 3
	e.Arm("a")
	f := dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 {
		t.Fatalf("want one frame, got %d", len(f))
	}
	got := f[0].Data[:4]
	if !bytes.Equal(got, []byte{10, 20, 30, 10}) {
		t.Errorf("slots 1-4 = %v; want tests 10, programmer 20, raw 30 (raw > programmer > tests), tests 10", got)
	}
	// Releasing the raw claim hands slot 3 back to the programmer at once.
	e.Release(SourceRaw, 0)
	f = dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 || !bytes.Equal(f[0].Data[:4], []byte{10, 20, 21, 10}) {
		t.Errorf("after releasing raw: %v, want [10 20 21 10]", f)
	}
	// A programmer value of zero is a claim: it beats the test's 10.
	_ = e.SetChannels(SourceProgrammer, 0, 4, []byte{0})
	e.Push(false, 0)
	f = dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 || f[0].Data[3] != 0 {
		t.Errorf("programmer zero on slot 4 did not win over the test")
	}
	if err := e.ReleaseChannels(SourceProgrammer, 0, 2, 3); err != nil {
		t.Fatal(err)
	}
	f = dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 || !bytes.Equal(f[0].Data[:4], []byte{10, 10, 10, 10}) {
		t.Errorf("after releasing the programmer: %v, want the tests underneath", f)
	}
}

func TestIdentifyOwnsItsStreamExclusively(t *testing.T) {
	rig := &sacnRig{}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{4: OutputBoth})
	_ = e.SetFrame(SourceRaw, 4, []byte{1, 2, 3})
	_ = e.SetFrame(SourceRaw, 6, []byte{6})
	e.Arm("a")
	clock.Advance(100 * time.Millisecond) // let the sACN burst for universe 4 complete
	tr.TakeSent()
	rig.link.take(t)
	id := make([]byte, 512)
	id[4] = 255
	if err := e.SetIdentify(map[Stream][]byte{{WireArtNet, 4}: id}); err != nil {
		t.Fatal(err)
	}
	sent := tr.TakeSent()
	if f := dmxFor(t, sent, 4); len(f) != 1 || f[0].Data[4] != 255 || f[0].Data[0] != 0 {
		t.Errorf("identify on Art-Net 4: %v; want identify's frame alone", f)
	}
	if f := dmxFor(t, sent, 6); len(f) != 0 {
		t.Errorf("identify pushed universe 6 too")
	}
	e.Push(false, 4)
	if pk := rig.link.take(t); len(pk) != 0 {
		t.Errorf("an unchanged sACN stream was resent outside its cadence")
	}
	sent = tr.TakeSent()
	if f := dmxFor(t, sent, 4); len(f) != 0 {
		t.Errorf("a push for show universe 4 transmitted the identify-owned Art-Net stream")
	}
	e.ClearIdentify()
	if f := dmxFor(t, tr.TakeSent(), 4); len(f) != 1 || f[0].Data[0] != 1 {
		t.Errorf("after identify ends, Art-Net 4 must fall back to the raw frame at once: %v", f)
	}
}

func TestIdentifyOnAnUnusedStreamRetiresWithZeros(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	e.Arm("a")
	_ = e.SetIdentify(map[Stream][]byte{{WireArtNet, 0}: {255, 255}})
	tr.TakeSent()
	e.ClearIdentify()
	f := dmxFor(t, tr.TakeSent(), 0)
	if len(f) != BlackoutFrameCount {
		t.Fatalf("identify off on an otherwise unused universe: %d frames, want %d zero frames", len(f), BlackoutFrameCount)
	}
	for _, x := range f {
		if !isZero(x.Data) {
			t.Errorf("retire frame not zero")
		}
	}
}

// --- protocols ---------------------------------------------------------------

func TestBothProtocolsCarryIdenticalSlotsEachOnItsOwnBinding(t *testing.T) {
	rig := &sacnRig{}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("sACN adapter eth1", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0x0102: OutputBoth, 3: OutputSACN})
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i * 7)
	}
	_ = e.SetFrame(SourceRaw, 0x0102, data)
	_ = e.SetFrame(SourceRaw, 3, []byte{9})
	e.Arm("a")
	clock.Advance(100 * time.Millisecond)
	art := dmxFor(t, tr.TakeSent(), 0x0102)
	pk := rig.link.take(t)
	if rig.opens != 1 || rig.adapter != "sACN adapter eth1" {
		t.Fatalf("sACN socket opened %d times on %q; want once on its own adapter", rig.opens, rig.adapter)
	}
	if len(art) == 0 {
		t.Fatal("no ArtDmx on the Art-Net transport for the dual-protocol universe")
	}
	if n := len(dmxFor(t, tr.Sent(), 3)); n != 0 {
		t.Errorf("universe 3 is sACN-only but %d ArtDmx went out", n)
	}
	var u259, u4 []e131
	for _, p := range pk {
		switch p.universe {
		case 0x0103:
			u259 = append(u259, p)
		case 4:
			u4 = append(u4, p)
		default:
			t.Errorf("unexpected sACN universe %d", p.universe)
		}
	}
	if len(u259) != SACNSuppressionBurst || len(u4) != SACNSuppressionBurst {
		t.Errorf("sACN packets in 100 ms: %d and %d; want the %d-packet burst then suppression (Section 6.6.2)", len(u259), len(u4), SACNSuppressionBurst)
	}
	if len(u259) > 0 {
		if !bytes.Equal(u259[0].slots, art[len(art)-1].Data) {
			t.Errorf("Art-Net and sACN slot data differ for the same universe")
		}
		if want := netip.MustParseAddrPort("239.255.1.3:5568"); u259[0].dst != want {
			t.Errorf("sACN destination %v, want %v (Table 9-10)", u259[0].dst, want)
		}
		if u259[0].priority != 120 || u259[0].cid[2] != 3 || u259[0].sequence != 0 || u259[1].sequence != 1 {
			t.Errorf("sACN header priority=%d cid=%x seq=%d,%d", u259[0].priority, u259[0].cid, u259[0].sequence, u259[1].sequence)
		}
	}
	if st := e.Status(); st.SACNAdapter != "sACN adapter eth1" {
		t.Errorf("status adapter %q", st.SACNAdapter)
	}
}

func TestSACNCadenceFollowsSection662(t *testing.T) {
	rig := &sacnRig{}
	e, clock, _ := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputSACN})
	_ = e.SetFrame(SourceRaw, 0, []byte{1})
	e.Arm("a")
	clock.Advance(2 * time.Second)
	pk := rig.link.take(t)
	// 3-packet burst at 0, 25, 50 ms; then keep-alives each 850 ms after the
	// last send: 900 and 1750 ms.
	if len(pk) != 5 {
		t.Fatalf("2 s of an unchanged frame: %d packets, want 3 + 2 keep-alives", len(pk))
	}
	_ = e.SetFrame(SourceRaw, 0, []byte{2})
	clock.Advance(100 * time.Millisecond)
	pk = rig.link.take(t)
	if len(pk) != 3 || pk[0].slots[0] != 2 {
		t.Fatalf("a change must go out three times: got %d packets", len(pk))
	}
	e.Push(true, 0)
	if pk := rig.link.take(t); len(pk) != 1 {
		t.Errorf("a forced push of an unchanged frame sent %d packets, want 1 (and a new burst)", len(pk))
	}
}

func TestRoutingSwitchTerminatesTheOldProtocol(t *testing.T) {
	rig := &sacnRig{}
	e, _, tr := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputSACN})
	_ = e.SetFrame(SourceRaw, 0, []byte{1})
	e.Arm("a")
	rig.link.take(t)
	e.SetRouting(map[uint16]OutputProtocols{0: OutputArtNet})
	pk := rig.link.take(t)
	if len(pk) != 6 || !pk[5].terminated {
		t.Fatalf("switching universe 0 to Art-Net: %d sACN packets, want 3 zero + 3 Stream_Terminated", len(pk))
	}
	tr.TakeSent()
}

func TestUnmappableSACNUniverseIsAnErrorNotAClamp(t *testing.T) {
	rig := &sacnRig{}
	e, _, tr := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0x7FFF: OutputBoth})
	_ = e.SetFrame(SourceRaw, 0x7FFF, []byte{1})
	e.Arm("a")
	if n := len(dmxFor(t, tr.TakeSent(), 0x7FFF)); n != 1 {
		t.Errorf("Art-Net half of an unmappable universe: %d frames, want 1", n)
	}
	if rig.link != nil && len(rig.link.take(t)) != 0 {
		t.Errorf("an unmappable universe was sent on sACN")
	}
	st := e.Status()
	if len(st.Unmapped) != 1 || st.Unmapped[0] != 0x7FFF || st.Error == "" {
		t.Errorf("status unmapped=%v error=%q; want the universe listed and an error in words", st.Unmapped, st.Error)
	}
}

func TestSACNOpenFailureIsAnOutputErrorAndArtNetContinues(t *testing.T) {
	rig := &sacnRig{fail: errors.New("adapter eth9 not found")}
	e, clock, tr := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.Addr{}), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputBoth})
	_ = e.SetFrame(SourceRaw, 0, []byte{1})
	e.Arm("a")
	clock.Advance(100 * time.Millisecond)
	if n := len(dmxFor(t, tr.TakeSent(), 0)); n < 4 {
		t.Errorf("Art-Net stopped because sACN failed: %d frames", n)
	}
	if st := e.Status(); st.Error == "" || st.State != StateArmed {
		t.Errorf("status %q error %q; want armed with an output error", st.State, st.Error)
	}
}

func TestSimulatedIsReported(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{Simulated: true})
	if !e.Status().Simulated {
		t.Fatal("Simulated engine does not report it")
	}
}

func TestUnicastOverride(t *testing.T) {
	rig := &sacnRig{}
	e, _, _ := newDMX(t, DMXConfig{SACN: rig.binding("n", netip.MustParseAddr("10.1.2.3")), Lease: -1})
	e.SetRouting(map[uint16]OutputProtocols{0: OutputSACN})
	_ = e.SetFrame(SourceRaw, 0, nil)
	e.Arm("a")
	pk := rig.link.take(t)
	if len(pk) == 0 || pk[0].dst != netip.MustParseAddrPort("10.1.2.3:5568") {
		t.Fatalf("unicast override: %v", pk)
	}
}
