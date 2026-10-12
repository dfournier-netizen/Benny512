package session

import (
	"testing"
	"time"
)

// TestChannelSourcesFollowTheComposition (I2f): the engine reports, per
// channel, the source whose value the composition shows — the highest one
// owning it — and Universe Identify when it replaces the universe's
// stream; a virtual dimmer reports the source whose level wins. This is
// what the Tests panel reads to say a test is overridden.
func TestChannelSourcesFollowTheComposition(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	_ = e.ReplaceSource(SourceBase, map[uint16]LayerFrame{0: lf(map[int]byte{1: 0, 2: 0, 3: 0, 4: 0, 5: 0, 6: 0, 7: 0})})
	_ = e.ReplaceSource(SourceTests, map[uint16]LayerFrame{0: lf(map[int]byte{1: 10, 2: 10, 3: 10, 4: 10, 5: 10, 6: 10})})
	_ = e.ReplaceSource(SourceGroupFaders, map[uint16]LayerFrame{0: lf(map[int]byte{2: 20})})
	_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{3: 30})})
	_ = e.ReplaceSource(SourceHighlight, map[uint16]LayerFrame{0: lf(map[int]byte{4: 40})})
	_ = e.ReplaceSource(SourceRaw, map[uint16]LayerFrame{0: lf(map[int]byte{5: 50})})
	got := e.ChannelSources(0, []int{1, 2, 3, 4, 5, 6, 7, 8, 0, 513})
	want := []struct {
		src   Source
		owned bool
	}{{SourceTests, true}, {SourceGroupFaders, true}, {SourceProgrammer, true}, {SourceHighlight, true}, {SourceRaw, true},
		{SourceTests, true}, {SourceBase, true}, {0, false}, {0, false}, {0, false}}
	if len(got) != len(want) {
		t.Fatalf("got %d answers for %d channels", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Owned != w.owned || (w.owned && got[i].Source != w.src) || got[i].Identify {
			t.Errorf("answer %d: %+v, want source %v owned %v, no Identify", i, got[i], w.src, w.owned)
		}
	}
	if got := e.ChannelSources(1, []int{1}); got[0].Owned {
		t.Errorf("a universe no source claims reports %+v, want not owned", got[0])
	}

	_ = e.SetIdentify(map[Stream][]byte{{WireArtNet, 0}: {255}})
	if got := e.ChannelSources(0, []int{1}); !got[0].Identify || got[0].Source != SourceTests {
		t.Errorf("under Identify: %+v, want Identify true over the tests' claim", got[0])
	}
	e.ClearIdentify()

	_ = e.SetVirtualDimmers(map[uint16][]VirtualDimmer{0: {{Key: "fx#1", Channels: []ScaleGroup{{Slots: []int{6}}}}}})
	if _, ok := e.VirtualSource("fx#1"); ok {
		t.Errorf("an unclaimed virtual dimmer reports a source")
	}
	_ = e.SetVirtualDimmerLevels(SourceTests, map[string]byte{"fx#1": 128})
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"fx#1": 50})
	if s, ok := e.VirtualSource("fx#1"); !ok || s != SourceProgrammer {
		t.Errorf("virtual dimmer claimed by tests and programmer reports %v %v, want programmer", s, ok)
	}
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, nil)
	if s, ok := e.VirtualSource("fx#1"); !ok || s != SourceTests {
		t.Errorf("after the programmer let go: %v %v, want tests", s, ok)
	}
}

// isolateRig: tests drive slots 1-3 at 10; Isolate (when on) holds slots 2
// and 3 at 0; the programmer holds slot 3 at 30 (manual wins).
func isolateRig(t *testing.T) (*DMXOutputEngine, *FakeClock, func() []byte, chan string) {
	t.Helper()
	e, clock, tr := newDMX(t, DMXConfig{})
	_ = e.ReplaceSource(SourceTests, map[uint16]LayerFrame{0: lf(map[int]byte{1: 10, 2: 10, 3: 10})})
	_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{3: 30})})
	wire := func() []byte {
		t.Helper()
		tr.TakeSent()
		clock.Advance(30 * time.Millisecond)
		f := dmxFor(t, tr.TakeSent(), 0)
		if len(f) == 0 {
			t.Fatal("no frame on the wire")
		}
		return f[len(f)-1].Data[:3]
	}
	return e, clock, wire, make(chan string, 4)
}

func isolateZeros() map[uint16]LayerFrame {
	return map[uint16]LayerFrame{0: lf(map[int]byte{2: 0, 3: 0})}
}

func endedWith(t *testing.T, ended chan string, want string) {
	t.Helper()
	select {
	case got := <-ended:
		if got != want {
			t.Errorf("Isolate ended with %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("Isolate never reported its end (%q)", want)
	}
}

// TestIsolateIsOnlyWhileArmedAndLive (I2f, owner 2026-10-09: Isolate must
// stay safe with Arm/Disarm, and never leave the rig isolated after a
// Disarm): refused while disarmed (nothing kept for the next Arm); armed,
// it holds the untested slots at 0 under every manual source; Disarm ends
// it and a re-Arm comes back NOT isolated, and frames sent while it is off
// are ignored.
func TestIsolateIsOnlyWhileArmedAndLive(t *testing.T) {
	e, _, wire, ended := isolateRig(t)
	if err := e.StartIsolate("laptop", func(r string) { ended <- r }); err != ErrOutputNotLive {
		t.Fatalf("StartIsolate while disarmed: %v, want ErrOutputNotLive", err)
	}
	e.Arm("laptop")
	if err := e.StartIsolate("", nil); err != ErrIsolateClient {
		t.Errorf("StartIsolate without a client: %v, want ErrIsolateClient", err)
	}
	if err := e.StartIsolate("laptop", func(r string) { ended <- r }); err != nil {
		t.Fatal(err)
	}
	_ = e.SetIsolateFrames(isolateZeros())
	if d := wire(); d[0] != 10 || d[1] != 0 || d[2] != 30 {
		t.Errorf("isolated: %v, want [10 0 30] (tested 10, untested 0, the programmer still wins)", d)
	}
	if got := e.ChannelSources(0, []int{2})[0]; got.Source != SourceIsolate {
		t.Errorf("slot 2 under Isolate reports %v, want isolate", got.Source)
	}
	e.Disarm()
	endedWith(t, ended, IsolateEndDisarm)
	if e.Isolating() {
		t.Errorf("still isolating after Disarm")
	}
	_ = e.SetIsolateFrames(isolateZeros()) // off: ignored
	e.Arm("laptop")
	if d := wire(); d[0] != 10 || d[1] != 10 || d[2] != 30 {
		t.Errorf("re-armed after a Disarm: %v, want [10 10 30] — never back isolated", d)
	}
}

// TestIsolateEndsOnLeaseLossBeforeTheHold: with "Hold last look" the look
// held after the lease is lost is the one WITHOUT Isolate (it is dropped
// first, as a one-shot command is), so the rig is never held isolated.
func TestIsolateEndsOnLeaseLossBeforeTheHold(t *testing.T) {
	e, clock, wire, ended := isolateRig(t)
	e.SetLeaseLossAction(LeaseLossHold)
	e.Arm("laptop")
	if err := e.StartIsolate("laptop", func(r string) { ended <- r }); err != nil {
		t.Fatal(err)
	}
	_ = e.SetIsolateFrames(isolateZeros())
	if d := wire(); d[1] != 0 {
		t.Fatalf("isolated: %v", d)
	}
	clock.Advance(5 * time.Second)
	if e.State() != StateHolding {
		t.Fatalf("state %q, want holding", e.State())
	}
	endedWith(t, ended, IsolateEndLease)
	if d := wire(); d[0] != 10 || d[1] != 10 || d[2] != 30 {
		t.Errorf("held look: %v, want [10 10 30] — the hold must not freeze Isolate", d)
	}
}

// TestIsolateEndsWhenItsBrowserLeaves: another browser keeps the lease
// alive, but Isolate ends when the browser that turned it on says goodbye —
// or goes silent for the lease.
func TestIsolateEndsWhenItsBrowserLeaves(t *testing.T) {
	e, clock, wire, ended := isolateRig(t)
	e.Arm("laptop")
	e.Heartbeat("phone")
	if err := e.StartIsolate("laptop", func(r string) { ended <- r }); err != nil {
		t.Fatal(err)
	}
	_ = e.SetIsolateFrames(isolateZeros())
	e.Goodbye("laptop")
	endedWith(t, ended, IsolateEndBrowserLeft)
	if d := wire(); e.State() != StateArmed || d[1] != 10 {
		t.Errorf("after its browser left: state %q, wire %v — want still armed (the phone) and not isolated", e.State(), d)
	}

	if err := e.StartIsolate("phone", func(r string) { ended <- r }); err != nil {
		t.Fatal(err)
	}
	_ = e.SetIsolateFrames(isolateZeros())
	for i := 0; i < 6; i++ { // the laptop keeps the lease; the phone goes quiet
		e.Heartbeat("laptop")
		clock.Advance(time.Second)
	}
	endedWith(t, ended, IsolateEndBrowserLeft)
	if d := wire(); e.State() != StateArmed || d[1] != 10 {
		t.Errorf("after its browser went quiet: state %q, wire %v — want armed and not isolated", e.State(), d)
	}
}
