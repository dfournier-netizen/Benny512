package patch

import (
	"bytes"
	"testing"
	"time"

	"benny512/internal/session"
)

func fadeFrame(t *testing.T, tr *session.FakeTransport, universe uint16) []byte {
	t.Helper()
	f, ok := lastFrame(t, tr.TakeSent(), universe)
	if !ok {
		t.Fatal("no emitted DMX frame")
	}
	return f
}

func TestPatternFadeDefaultStartAndStop(t *testing.T) {
	rc, tr, clock := harness(t)
	// A fresh production instance, without the legacy harness's Snap override.
	rc = NewRigCheck(rc.dmx)
	if rc.PatternStatus().FadeMS != 1000 {
		t.Fatal("default must be one second")
	}
	_, err := rc.StartPattern([]Entry{dimmerEntry("a", 0)}, PatternSpec{Kind: PatternDimmerToggle, Params: PatternParams{On: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fadeFrame(t, tr, 0)[9]; got != 0 {
		t.Fatalf("start=%d, want dark", got)
	}
	clock.Advance(500 * time.Millisecond)
	if got := fadeFrame(t, tr, 0)[9]; got != 128 {
		t.Fatalf("midpoint=%d", got)
	}
	rc.StopPatternOutput()
	if got := fadeFrame(t, tr, 0)[9]; got != 0 {
		t.Fatalf("Stop did not blackout immediately: %d", got)
	}
	clock.Advance(2 * time.Second)
	if len(tr.TakeSent()) != 0 {
		t.Fatal("fade resumed after Stop")
	}
	if rc.PatternStatus().FadeMS != 1000 || len(rc.PatternStatus().Tests) != 1 {
		t.Fatal("Stop lost settings")
	}
}

func TestPatternFade16BitRolloverAndRetarget(t *testing.T) {
	rc, tr, clock := harness(t)
	rc.SetPatternFade(time.Second)
	e := panTiltEntry("p")
	for _, off := range []uint16{1, 2} {
		cf := e.ChannelFunctions[off]
		cf.Attribute = "Focus"
		cf.HasDefault = true
		cf.Default = 255
		cf.DefaultByteCount = 2
		e.ChannelFunctions[off] = cf
	}
	spec := PatternSpec{Kind: PatternManualValue, Params: PatternParams{Target: "focus", Value: 1}}
	if _, err := rc.StartPattern([]Entry{e}, spec); err != nil {
		t.Fatal(err)
	}
	if f := fadeFrame(t, tr, 0); f[0] != 0 || f[1] != 255 {
		t.Fatalf("start=%v", f[:2])
	}
	clock.Advance(500 * time.Millisecond)
	if f := fadeFrame(t, tr, 0); f[0] != 1 || f[1] != 0 {
		t.Fatalf("16-bit midpoint must be 0x0100, got %v", f[:2])
	}
	clock.Advance(500 * time.Millisecond)
	if f := fadeFrame(t, tr, 0); f[0] != 1 || f[1] != 1 {
		t.Fatalf("end=%v", f[:2])
	}
	spec.Params.Value = 255
	rc.SelectPatternTest(spec, true)
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond)
	f := fadeFrame(t, tr, 0)
	before := uint16(f[0])<<8 | uint16(f[1])
	if before != 32896 {
		t.Fatalf("mid-fade=%d", before)
	}
	spec.Params.Value = 0
	rc.SelectPatternTest(spec, true)
	f = fadeFrame(t, tr, 0)
	if got := uint16(f[0])<<8 | uint16(f[1]); got != before {
		t.Fatalf("retarget jumps: %d -> %d", before, got)
	}
	clock.Advance(500 * time.Millisecond)
	f = fadeFrame(t, tr, 0)
	if got := uint16(f[0])<<8 | uint16(f[1]); got != 16448 {
		t.Fatalf("retarget midpoint=%d", got)
	}
}

func TestPatternFadeDeselectAndScope(t *testing.T) {
	rc, tr, clock := harness(t)
	rc.SetPatternFade(time.Second)
	spec := PatternSpec{Kind: PatternDimmerToggle, Params: PatternParams{On: false}}
	rc.StartPattern([]Entry{dimmerEntry("a", 0)}, spec)
	clock.Advance(time.Second)
	tr.TakeSent()
	rc.SelectPatternTest(spec, false) // base state is now full; it must fade up.
	if got := fadeFrame(t, tr, 0)[9]; got != 0 {
		t.Fatalf("deselect snapped to %d", got)
	}
	clock.Advance(500 * time.Millisecond)
	if got := fadeFrame(t, tr, 0)[9]; got != 128 {
		t.Fatalf("deselect midpoint=%d", got)
	}
	clock.Advance(500 * time.Millisecond)
	tr.TakeSent()
	if _, err := rc.SetPatternScope([]Entry{dimmerEntry("b", 1)}); err != nil {
		t.Fatal(err)
	}
	sent := tr.TakeSent()
	old, _ := lastFrame(t, sent, 0)
	next, ok := lastFrame(t, sent, 1)
	if !ok || old[9] != 255 || next[9] != 0 {
		t.Fatalf("scope start old=%v new=%v", old, next)
	}
	clock.Advance(time.Second)
	sent = tr.TakeSent()
	old, _ = lastFrame(t, sent, 0)
	next, ok = lastFrame(t, sent, 1)
	if !ok || old[9] != 0 || next[9] != 255 {
		t.Fatal("scope did not retire old fixture and start new universe")
	}
}

func TestPatternFadeDoesNotRestartOtherWaveforms(t *testing.T) {
	rc, tr, clock := harness(t)
	control, ct, cc := harness(t)
	rc.SetPatternFade(time.Second)
	e := panTiltEntry("p")
	d := dimmerEntry("d", 0)
	specs := []PatternSpec{{Kind: PatternBallyhoo, Params: PatternParams{RateHz: 1, Max: 255}}, {Kind: PatternDimmerToggle, Params: PatternParams{On: true}}}
	for _, r := range []*RigCheck{rc, control} {
		r.SetPatternTests([]Entry{e, d}, specs, false)
		r.StartPatternOutput()
	}
	clock.Advance(time.Second)
	cc.Advance(time.Second)
	tr.TakeSent()
	ct.TakeSent()
	specs[1].Params.On = false
	rc.SelectPatternTest(specs[1], true)
	control.SelectPatternTest(specs[1], true)
	tr.TakeSent()
	ct.TakeSent()
	// Four full cycles: editing dimmer never interrupts position; once the
	// transition finishes there is no periodic reapplication of the fade.
	for i := 0; i < 160; i++ {
		rc.PatternStatus()
		control.PatternStatus()
		clock.Advance(25 * time.Millisecond)
		cc.Advance(25 * time.Millisecond)
		f := fadeFrame(t, tr, 0)
		ref := fadeFrame(t, ct, 0)
		if !bytes.Equal(f[:4], ref[:4]) {
			t.Fatalf("position changed at sample %d: %v vs %v", i, f[:4], ref[:4])
		}
		if i >= 39 && f[9] != 0 {
			t.Fatalf("dimmer revived at sample %d: %d", i, f[9])
		}
	}
}

func TestPatternFadeWatchdogAndSettingChanges(t *testing.T) {
	rc, tr, clock := harness(t)
	rc.SetPatternFade(10 * time.Second)
	rc.StartPattern([]Entry{dimmerEntry("a", 0)}, PatternSpec{Kind: PatternDimmerToggle, Params: PatternParams{On: true}})
	clock.Advance(time.Second)
	if got := fadeFrame(t, tr, 0)[9]; got != 26 {
		t.Fatalf("10s fade at 1s=%d", got)
	}
	rc.SetPatternFade(0) // Applies to the NEXT transition, not the current one.
	if len(tr.TakeSent()) != 0 {
		t.Fatal("changing duration emitted output")
	}
	clock.Advance(time.Second)
	if got := fadeFrame(t, tr, 0)[9]; got != 51 {
		t.Fatalf("duration change restarted current transition: %d", got)
	}
	clock.Advance(5 * time.Second)
	if got := fadeFrame(t, tr, 0)[9]; got != 0 {
		t.Fatalf("watchdog faded instead of blacking out: %d", got)
	}
	if st := rc.PatternStatus(); st.OutputEnabled || st.LastEndReason != "watchdog" {
		t.Fatalf("%+v", st)
	}
}

func TestPatternFadeDiscreteShutterAndInvalidSettings(t *testing.T) {
	rc, tr, clock := harness(t)
	rc.SetPatternFade(time.Second)
	e := dimmerEntry("a", 0)
	e.ChannelFunctions[2] = ChannelFunction{Source: SourceGDTF, Attribute: "Shutter1", HasDefault: true, Default: 22, DefaultByteCount: 1}
	rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerToggle, Params: PatternParams{On: true}})
	for i := 0; i < 20; i++ {
		clock.Advance(25 * time.Millisecond)
		if got := fadeFrame(t, tr, 0)[10]; got != 22 {
			t.Fatalf("shutter swept through %d", got)
		}
	}
	for _, d := range []time.Duration{-1, MaxPatternFade + 1} {
		if _, err := rc.SetPatternFade(d); err == nil {
			t.Fatal("accepted invalid fade")
		}
	}
	if rc.PatternStatus().FadeMS != 1000 {
		t.Fatal("invalid request changed fade")
	}
}
