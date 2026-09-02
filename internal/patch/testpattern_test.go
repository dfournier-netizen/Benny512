package patch

import (
	"fmt"
	"math"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// dimmerEntry, ballyhooEntry etc. build small, focused Entry fixtures — one
// concept per helper — rather than reusing one big shared fixture, so each
// test's failure message points at exactly the shape under test.

func dimmerEntry(id string, universe uint16) Entry {
	return Entry{
		ID: id, Universe: universe, StartAddress: 10, Footprint: 2,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Dimmer", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]ChannelSet, 0)},
			2: {Source: SourceGDTF, Attribute: "Gobo1", DMXFrom: 0, DMXTo: 255, ChannelSets: make([]ChannelSet, 0)}, // untouched-channel sentinel
		},
	}
}

// panTiltEntry builds a 16-bit (coarse+fine) Pan/Tilt fixture, one function
// GDTF-sourced and one RDM-inferred, so a single test can assert both the
// coarse/fine split AND the inferred-provenance flag at once.
func panTiltEntry(id string) Entry {
	return Entry{
		ID: id, Universe: 0, StartAddress: 1, Footprint: 4,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Pan", ChannelSets: make([]ChannelSet, 0)},
			2: {Source: SourceGDTF, Attribute: "Pan", ChannelSets: make([]ChannelSet, 0)}, // fine
			3: {Source: SourceRDMInferred, Attribute: "Tilt", RDMSlotType: "primary", RDMSlotLabel: "SD_TILT", ChannelSets: make([]ChannelSet, 0)},
			4: {Source: SourceRDMInferred, Attribute: "Tilt", RDMSlotType: "secondary-fine", ChannelSets: make([]ChannelSet, 0)}, // fine
		},
	}
}

func colourWheelEntry(id string, withChannelSets bool) Entry {
	sets := make([]ChannelSet, 0)
	if withChannelSets {
		sets = []ChannelSet{{Name: "Open", DMXFrom: 0}, {Name: "Red", DMXFrom: 32}, {Name: "Green", DMXFrom: 64}}
	}
	return Entry{
		ID: id, Universe: 0, StartAddress: 1, Footprint: 1,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "ColorWheel", ChannelSets: sets},
		},
	}
}

// only returns the single selected test's status, failing the test if the
// selection does not hold exactly one — the shape every single-test case
// here uses, now that PatternStatus reports a SET of tests.
func only(t *testing.T, st PatternStatus) TestStatus {
	t.Helper()
	if len(st.Tests) != 1 {
		t.Fatalf("expected exactly one selected test, got %d: %+v", len(st.Tests), st.Tests)
	}
	return st.Tests[0]
}

func harness(t *testing.T) (*RigCheck, *session.FakeTransport, *session.FakeClock) {
	t.Helper()
	clock := session.NewFakeClock(time.Time{})
	tr := session.NewFakeTransport()
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: clock, Transport: tr, Rate: 40})
	t.Cleanup(dmx.Stop)
	return NewRigCheck(dmx), tr, clock
}

func lastFrame(t *testing.T, sent []session.SentPacket, universe uint16) ([]byte, bool) {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(universe)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	found := false
	for _, sp := range sent {
		if sp.DecodeErr != nil {
			t.Fatalf("undecodable ArtDmx: %v", sp.DecodeErr)
		}
		if sp.Packet.Kind != artnet.KindDmx {
			continue
		}
		d := sp.Packet.Dmx
		if d.SubUni != pa.SubUni() || d.Net != pa.Net {
			continue
		}
		out = append([]byte(nil), d.Data...)
		found = true
	}
	return out, found
}

// TestPattern_DimmerSine_OnlyTouchesSelectedChannel proves decision 1 (safe
// mode: touch ONLY the selected function) directly: channel 11 (Gobo1, same
// entry) must stay 0 for the life of the run even as the dimmer channel
// (10) visibly moves.
func TestPattern_DimmerSine_OnlyTouchesSelectedChannel(t *testing.T) {
	rc, tr, clock := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()

	// This is a raised cosine (0.5*(1-cos(2*pi*rate*t))): it starts at its
	// floor (t=0) and reaches its peak at HALF a cycle (500ms at 1Hz), not
	// a quarter — a plain sin() would peak at a quarter, this doesn't.
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[9] < 250 { // channel 10 = index 9
		t.Errorf("dimmer channel = %d at half cycle, want near 255", frame[9])
	}
	if frame[10] != 0 { // channel 11 = Gobo1, never selected
		t.Errorf("untouched channel (Gobo1) = %d, want 0 (decision 1: only the selected function is ever written)", frame[10])
	}
}

// TestPattern_Ballyhoo_16BitCoarseFineAndInferredFlag exercises the coarse+
// fine split on Pan (GDTF) and confirms the RDM-inferred Tilt function is
// flagged, both from one entry/one run.
func TestPattern_Ballyhoo_16BitCoarseFineAndInferredFlag(t *testing.T) {
	rc, tr, clock := harness(t)
	e := panTiltEntry("pt1")
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternBallyhoo, Params: PatternParams{RateHz: 1, Max: 255}})
	if err != nil {
		t.Fatal(err)
	}
	ts := only(t, st)
	if !ts.Entries[0].Inferred {
		t.Error("expected Inferred=true: Tilt on this entry is RDM-inferred")
	}
	if ts.AppliedCount != 1 || ts.InferredCount != 1 {
		t.Errorf("AppliedCount=%d InferredCount=%d, want 1,1", ts.AppliedCount, ts.InferredCount)
	}

	// Pan (phase 0, raised cosine) is at its 16-bit peak at HALF a cycle
	// (500ms @ 1Hz — see TestPattern_DimmerSine's comment on why).
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] < 250 || frame[1] < 250 {
		t.Errorf("Pan coarse/fine = %d/%d at half cycle, want both near 255", frame[0], frame[1])
	}
}

// TestPattern_ColourWheelStep_UsesChannelSets confirms discrete slot
// stepping actually lands on a GDTF ChannelSet's own DMXFrom value, not an
// interpolated one.
func TestPattern_ColourWheelStep_UsesChannelSets(t *testing.T) {
	rc, tr, clock := harness(t)
	e := colourWheelEntry("cw1", true)
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternColourWheelStep, Params: PatternParams{RateHz: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.MissingDetailCount != 0 || ts.Entries[0].DetailMissing {
		t.Error("ChannelSets are present — DetailMissing must be false")
	}

	// Step 0 at t=0 -> "Open" (DMXFrom 0); after 1.5s (1Hz -> 1 step/sec,
	// wraps every 3 steps) -> step 1 -> "Red" (DMXFrom 32).
	clock.Advance(1500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] != 32 {
		t.Errorf("colour wheel channel = %d at step 1, want 32 (the ChannelSet's own DMXFrom)", frame[0])
	}
}

// TestPattern_ColourWheelStep_DegradesWithoutChannelSets proves the "no
// slot-boundary guessing" rule: absent ChannelSets, the value stays a raw
// 0-255 sweep (never equal to a slot value it invented) and DetailMissing is
// reported.
func TestPattern_ColourWheelStep_DegradesWithoutChannelSets(t *testing.T) {
	rc, _, _ := harness(t)
	e := colourWheelEntry("cw2", false)
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternColourWheelStep, Params: PatternParams{RateHz: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.MissingDetailCount != 1 || !ts.Entries[0].DetailMissing {
		t.Error("expected DetailMissing=true and MissingDetailCount=1 with no ChannelSets available")
	}
}

// TestPattern_MixedRig_AppliesToSomeSkipsOthers is decision 2's mixed-rig
// contract directly: a fixture with Dimmer gets driven, one without is
// skipped — silently (no error) but visibly counted.
func TestPattern_MixedRig_AppliesToSomeSkipsOthers(t *testing.T) {
	rc, tr, _ := harness(t)
	withDimmer := dimmerEntry("has", 0)
	noDimmer := Entry{ID: "hasnt", Universe: 0, StartAddress: 50, Footprint: 2, ChannelFunctions: map[uint16]ChannelFunction{}}
	st, err := rc.StartPattern([]Entry{withDimmer, noDimmer}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}})
	if err != nil {
		t.Fatal(err)
	}
	ts := only(t, st)
	if ts.TotalScope != 2 || ts.AppliedCount != 1 || ts.SkippedCount != 1 {
		t.Fatalf("TotalScope=%d AppliedCount=%d SkippedCount=%d, want 2,1,1", ts.TotalScope, ts.AppliedCount, ts.SkippedCount)
	}
	var sawApplied, sawSkipped bool
	for _, es := range ts.Entries {
		switch es.EntryID {
		case "has":
			sawApplied = es.Applied
		case "hasnt":
			sawSkipped = !es.Applied
		}
	}
	if !sawApplied || !sawSkipped {
		t.Errorf("per-entry Applied flags wrong: %+v", ts.Entries)
	}

	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[49] != 0 { // channel 50 = index 49, the skipped fixture's first channel
		t.Errorf("skipped fixture's channel = %d, want 0 (never written)", frame[49])
	}
}

// TestPattern_Stop_BlacksOutAndClears proves the sacred safety discipline
// extends to patterns: Stop must push an all-zero frame and end the run.
func TestPattern_Stop_BlacksOutAndClears(t *testing.T) {
	rc, tr, clock := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(250 * time.Millisecond) // pattern is now clearly non-zero
	tr.TakeSent()

	rc.Stop()
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("Stop must push a final frame")
	}
	for i, v := range frame {
		if v != 0 {
			t.Fatalf("channel %d = %d after Stop, want 0 (blackout)", i+1, v)
		}
	}
	st := rc.PatternStatus()
	if st.OutputEnabled {
		t.Error("PatternStatus.OutputEnabled must be false after Stop")
	}
	// The owner's rule: Stop stops OUTPUT and deselects nothing.
	if len(st.Tests) != 1 {
		t.Errorf("Stop must leave the test selection intact, got %d tests", len(st.Tests))
	}
	if st.LastEndReason != "manual" {
		t.Errorf("LastEndReason = %q, want %q", st.LastEndReason, "manual")
	}

	// The pattern ticker must actually be cancelled, not just ignored:
	// advancing time further must not resurrect any output.
	clock.Advance(2 * time.Second)
	if got := tr.TakeSent(); len(got) != 0 {
		t.Errorf("expected no further sends after Stop, got %d", len(got))
	}
}

// TestPattern_Blackout_EndsTheRun documents and pins the deliberate
// divergence from classic Blackout's "Running stays true" contract — see
// RigCheck.Blackout's doc comment for why a running pattern can't honour
// that promise and still be a "hard all-off that always works".
func TestPattern_Blackout_EndsTheRun(t *testing.T) {
	rc, tr, _ := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()

	rc.Blackout()
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok || frame[9] != 0 {
		t.Fatalf("Blackout must push an all-zero frame; channel 10 = %v (ok=%v)", frame, ok)
	}
	if rc.PatternStatus().OutputEnabled {
		t.Error("Blackout must end a running pattern (see Blackout's doc comment), not merely dim it")
	}
}

// TestPattern_Watchdog_AutoStopsWhenClientGoesQuiet is the client-
// disappears-mid-pattern scenario made concrete: advancing the clock past
// PatternWatchdogTimeout with no StartPattern/AdjustPattern/PatternStatus
// touch in between must blackout-and-stop the run on its own.
func TestPattern_Watchdog_AutoStopsWhenClientGoesQuiet(t *testing.T) {
	rc, tr, clock := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()

	clock.Advance(PatternWatchdogTimeout + 200*time.Millisecond)

	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected the watchdog's blackout frame")
	}
	for i, v := range frame {
		if v != 0 {
			t.Fatalf("channel %d = %d after watchdog trip, want 0", i+1, v)
		}
	}
	st := rc.PatternStatus()
	if st.OutputEnabled {
		t.Error("pattern should have been auto-stopped by the watchdog")
	}
	if st.LastEndReason != "watchdog" {
		t.Errorf("LastEndReason = %q, want %q", st.LastEndReason, "watchdog")
	}
}

// TestPattern_Watchdog_StatusPollKeepsItAlive is the watchdog's other half:
// a client that keeps reading status (as any live UI must, to render the
// running waveform) must NOT trip the watchdog even though, cumulatively,
// far more than PatternWatchdogTimeout elapses.
func TestPattern_Watchdog_StatusPollKeepsItAlive(t *testing.T) {
	rc, _, clock := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		clock.Advance(PatternWatchdogTimeout - time.Second) // always under the window since last touch
		if !rc.PatternStatus().OutputEnabled {
			t.Fatalf("pattern stopped early at iteration %d despite regular status polling", i)
		}
	}
}

// TestPattern_ClassicMutatorsRejectedWhilePatternRunning guards the
// mutual-exclusion contract: SetMode/SetLevel/Jump/Next/Previous/
// StepChannel must not silently corrupt a running pattern's frame.
func TestPattern_ClassicMutatorsRejectedWhilePatternRunning(t *testing.T) {
	rc, _, _ := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	if err := rc.SetLevel(100); err != ErrRigCheckPatternRunning {
		t.Errorf("SetLevel = %v, want ErrRigCheckPatternRunning", err)
	}
	if err := rc.SetMode(ModeHighlight); err != ErrRigCheckPatternRunning {
		t.Errorf("SetMode = %v, want ErrRigCheckPatternRunning", err)
	}
	if err := rc.Next(); err != ErrRigCheckPatternRunning {
		t.Errorf("Next = %v, want ErrRigCheckPatternRunning", err)
	}
	if err := rc.Jump(0); err != ErrRigCheckPatternRunning {
		t.Errorf("Jump = %v, want ErrRigCheckPatternRunning", err)
	}
	if err := rc.StepChannel(1); err != ErrRigCheckPatternRunning {
		t.Errorf("StepChannel = %v, want ErrRigCheckPatternRunning", err)
	}
}

// TestPattern_ClassicStart_SupersedesRunningPattern proves the reverse
// direction: a classic Start always wins over (and cleanly stops) a
// previously running pattern, matching Start's existing "always start
// clean" rule.
func TestPattern_ClassicStart_SupersedesRunningPattern(t *testing.T) {
	rc, _, _ := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	if err := rc.Start([]Entry{e}, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	if rc.PatternStatus().OutputEnabled {
		t.Error("classic Start must end any running pattern")
	}
	if rc.State().PatternRunning {
		t.Error("State().PatternRunning must be false once classic mode has taken over")
	}
}

// TestPattern_AdjustPattern_ReResolvesTarget proves AdjustPattern actually
// re-walks resolution when Target changes which attribute is driven
// (move_extreme's axis) rather than just replaying stale offsets.
func TestPattern_AdjustPattern_ReResolvesTarget(t *testing.T) {
	rc, tr, _ := harness(t)
	e := panTiltEntry("pt1")
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: "pan_max"}}); err != nil {
		t.Fatal(err)
	}
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok || frame[0] != 255 || frame[1] != 255 {
		t.Fatalf("pan_max: coarse/fine = %v/%v (ok=%v), want 255/255", frame[0], frame[1], ok)
	}

	if _, err := rc.AdjustPattern(PatternParams{Target: "tilt_min"}); err != nil {
		t.Fatal(err)
	}
	frame2, ok2 := lastFrame(t, tr.TakeSent(), 0)
	if !ok2 {
		t.Fatal("expected a frame after AdjustPattern")
	}
	if frame2[0] != 0 || frame2[1] != 0 {
		t.Errorf("after retargeting to tilt_min, Pan channels = %d/%d, want 0/0 (no longer driven)", frame2[0], frame2[1])
	}
	if frame2[2] != 0 || frame2[3] != 0 {
		t.Errorf("tilt_min: coarse/fine = %d/%d, want 0/0", frame2[2], frame2[3])
	}
}

// TestPattern_AdjustPattern_ErrorsWithoutRunningPattern guards the
// pattern-only mutator's own not-running guard.
func TestPattern_AdjustPattern_ErrorsWithoutRunningPattern(t *testing.T) {
	rc, _, _ := harness(t)
	if _, err := rc.AdjustPattern(PatternParams{}); err != ErrRigCheckNoPatternRunning {
		t.Errorf("AdjustPattern before any StartPattern = %v, want ErrRigCheckNoPatternRunning", err)
	}
}

// TestPattern_StartPattern_ValidatesKind guards the API boundary: an
// unrecognized Kind must be rejected before anything is started (nothing
// should end up running, no universes started).
func TestPattern_StartPattern_ValidatesKind(t *testing.T) {
	rc, _, _ := harness(t)
	e := dimmerEntry("d1", 0)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: "not_a_real_kind"}); err == nil {
		t.Fatal("expected an error for an unknown pattern kind")
	}
	if rc.PatternStatus().OutputEnabled {
		t.Error("a rejected StartPattern must not leave a pattern running")
	}
}

// TestPattern_StartPattern_EmptyScopeErrors mirrors classic Start's
// TestRigCheck_StartEmptyScopeErrors.
func TestPattern_StartPattern_EmptyScopeErrors(t *testing.T) {
	rc, _, _ := harness(t)
	if _, err := rc.StartPattern(nil, PatternSpec{Kind: PatternDimmerSine}); err != ErrRigCheckEmptyScope {
		t.Errorf("StartPattern(nil, ...) = %v, want ErrRigCheckEmptyScope", err)
	}
}

// TestPattern_MoveExtreme_InvalidTargetRejected pins the Target grammar's
// validation for move_extreme (axis_extreme) and manual_value/frost's
// narrower enums, all via the public StartPattern boundary.
func TestPattern_MoveExtreme_InvalidTargetRejected(t *testing.T) {
	rc, _, _ := harness(t)
	e := panTiltEntry("pt1")
	cases := []PatternSpec{
		{Kind: PatternMoveExtreme, Params: PatternParams{Target: "pan_sideways"}},
		{Kind: PatternMoveExtreme, Params: PatternParams{Target: "focus_centre"}}, // centre invalid for focus/zoom
		{Kind: PatternMoveExtreme, Params: PatternParams{Target: "notanaxis_max"}},
		{Kind: PatternManualValue, Params: PatternParams{Target: "pan"}},
		// Frost's target is now a GDTF attribute name — the old
		// "light"/"heavy" judgment call is gone (see testpattern.go's
		// enumeration doc section), so BOTH of these are rejected now.
		{Kind: PatternFrost, Params: PatternParams{Target: "medium"}},
		{Kind: PatternFrost, Params: PatternParams{Target: "light"}},
	}
	for _, spec := range cases {
		if _, err := rc.StartPattern([]Entry{e}, spec); err == nil {
			t.Errorf("StartPattern(%+v) succeeded, want a validation error", spec)
		}
	}
}

// TestPattern_ShaperIndividual_OneSliceActiveAtATime proves the time-sliced
// "each shaper 0->100 individually" behaviour: only the currently active
// shaper's channel is non-zero at any instant.
func TestPattern_ShaperIndividual_OneSliceActiveAtATime(t *testing.T) {
	rc, tr, clock := harness(t)
	e := Entry{
		ID: "sh1", Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Blade1A", ChannelSets: make([]ChannelSet, 0)},
			2: {Source: SourceGDTF, Attribute: "Blade2A", ChannelSets: make([]ChannelSet, 0)},
		},
	}
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternShaperIndividual, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()

	// RateHz=1 -> 1s dwell per shaper (2 total): at t=0.5s, slice 0
	// (Blade1A) is mid-ramp and slice 1 (Blade2A) is silent.
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] == 0 {
		t.Error("Blade1A (active slice) should be mid-ramp, not 0")
	}
	if frame[1] != 0 {
		t.Errorf("Blade2A (inactive slice) = %d, want 0", frame[1])
	}
}

// TestPattern_PrismSpin_DirectionReversesRamp confirms cw/ccw actually
// invert the ramp direction rather than being ignored.
func TestPattern_PrismSpin_DirectionReversesRamp(t *testing.T) {
	rc, tr, clock := harness(t)
	entry := func(id, attr string) Entry {
		return Entry{ID: id, Universe: 0, StartAddress: 1, Footprint: 1, ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: attr, ChannelSets: make([]ChannelSet, 0)},
		}}
	}
	cw, err := rc.StartPattern([]Entry{entry("p1", "Prism1Rot")}, PatternSpec{Kind: PatternPrismSpin, Params: PatternParams{RateHz: 1, Max: 255, Direction: "cw"}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, cw); ts.MissingDetailCount != 1 {
		t.Errorf("MissingDetailCount = %d, want 1 (no ChannelSets on this synthetic fixture)", ts.MissingDetailCount)
	}
	// A quarter into an ascending sawtooth (period 1s @ 1Hz) sits at ~25%
	// of the span; a quarter into the mirrored (ccw) descending ramp sits
	// at ~75% — a fixed relative comparison, not an exact-shape assertion.
	clock.Advance(250 * time.Millisecond)
	cwFrame, _ := lastFrame(t, tr.TakeSent(), 0)

	if _, err := rc.StartPattern([]Entry{entry("p2", "Prism1Rot")}, PatternSpec{Kind: PatternPrismSpin, Params: PatternParams{RateHz: 1, Max: 255, Direction: "ccw"}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	clock.Advance(250 * time.Millisecond)
	ccwFrame, _ := lastFrame(t, tr.TakeSent(), 0)

	if cwFrame[0] >= ccwFrame[0] {
		t.Errorf("cw quarter-cycle value = %d, ccw = %d — cw (ascending) should be well below ccw (descending) at the same elapsed time", cwFrame[0], ccwFrame[0])
	}
}

// TestPattern_ColourMixSweep_RGBPreferredOverCMY and its sibling below pin
// pickMixChannels' "RGB if present, else CMY, else nothing" rule.
func TestPattern_ColourMixSweep_RGBPreferredOverCMY(t *testing.T) {
	rc, tr, clock := harness(t)
	e := Entry{
		ID: "mix1", Universe: 0, StartAddress: 1, Footprint: 3,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "ColorAdd_R", ChannelSets: make([]ChannelSet, 0)},
			2: {Source: SourceGDTF, Attribute: "ColorAdd_G", ChannelSets: make([]ChannelSet, 0)},
			3: {Source: SourceGDTF, Attribute: "ColorAdd_B", ChannelSets: make([]ChannelSet, 0)},
		},
	}
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternColourMixSweep, Params: PatternParams{RateHz: 1, Max: 255}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.AppliedCount != 1 {
		t.Fatalf("AppliedCount = %d, want 1", ts.AppliedCount)
	}
	clock.Advance(500 * time.Millisecond) // raised-cosine peak — see TestPattern_DimmerSine's comment
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	// "in and out together": all three channels must move in unison.
	if frame[0] != frame[1] || frame[1] != frame[2] {
		t.Errorf("R/G/B = %d/%d/%d, want equal (unison sweep, not a fade)", frame[0], frame[1], frame[2])
	}
	if frame[0] < 200 {
		t.Errorf("R/G/B = %d at half cycle, want near 255", frame[0])
	}
}

func TestPattern_ColourMixSweep_NoCompleteSetAppliesNothing(t *testing.T) {
	rc, _, _ := harness(t)
	e := Entry{
		ID: "mix2", Universe: 0, StartAddress: 1, Footprint: 1,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "ColorAdd_R", ChannelSets: make([]ChannelSet, 0)}, // only red, no green/blue
		},
	}
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternColourMixSweep, Params: PatternParams{RateHz: 1, Max: 255}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.AppliedCount != 0 || ts.SkippedCount != 1 {
		t.Errorf("AppliedCount=%d SkippedCount=%d, want 0,1 (incomplete RGB/CMY set applies to nothing)", ts.AppliedCount, ts.SkippedCount)
	}
}

// --- base state: "GDTF defaults + open only when needed" -------------------

func gdtfCF(attr string, name string, hasDefault bool, def uint32, nbytes uint16, sets ...ChannelSet) ChannelFunction {
	if sets == nil {
		sets = make([]ChannelSet, 0)
	}
	return ChannelFunction{
		Source: SourceGDTF, Attribute: attr, FunctionName: name, ChannelSets: sets,
		HasDefault: hasDefault, Default: def, DefaultByteCount: nbytes,
	}
}

// strobeBarEntry is a JDC-1-shaped fixture: a dimmer, a shutter with named
// ChannelSets, and a tilt whose GDTF Default is mid-travel. It is the exact
// shape the owner's bug report was about — a dimmer-only test on this fixture
// must not command tilt to an extreme, and must produce light.
func strobeBarEntry(id string, universe uint16, addr uint16) Entry {
	return Entry{
		ID: id, Universe: universe, StartAddress: addr, Footprint: 3,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Dimmer", "Dimmer", true, 0, 1),
			2: gdtfCF("Shutter1", "Shutter", false, 0, 0,
				ChannelSet{Name: "Closed", DMXFrom: 0},
				ChannelSet{Name: "Open", DMXFrom: 32},
				ChannelSet{Name: "Strobe", DMXFrom: 64}),
			3: gdtfCF("Tilt", "Tilt", true, 128, 1),
		},
	}
}

// TestPattern_BaseState_DefaultsDimmerAndShutter is the owner's bug report
// turned into an assertion. Running ONLY the dimmer test on a JDC-1-shaped
// fixture must:
//   - leave Tilt at its GDTF Default (128 — mid travel), NOT at 0. Zero on a
//     Tilt channel is not "untouched", it is a commanded move to one end of
//     travel, which is what swung his fixtures to their tilt extreme.
//   - open the Shutter (32, the "Open" ChannelSet's own DMXFrom), because
//     no active test drives the shutter and a fixture with a closed shutter
//     emits no light however the dimmer moves.
//   - leave the DIMMER to the dimmer test — the base state must not drive it
//     to full on top of the test that owns it.
func TestPattern_BaseState_DefaultsDimmerAndShutter(t *testing.T) {
	rc, tr, clock := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond) // raised-cosine peak
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] < 250 {
		t.Errorf("dimmer channel = %d at half cycle, want near 255 (the test owns it)", frame[0])
	}
	if frame[1] != 32 {
		t.Errorf("shutter channel = %d, want 32 (the \"Open\" ChannelSet's DMXFrom) — a closed shutter emits no light", frame[1])
	}
	if frame[2] != 128 {
		t.Errorf("tilt channel = %d, want its GDTF Default 128 — driving an untested Tilt to 0 is a commanded move to the end of travel, not \"leaving it alone\"", frame[2])
	}
}

// TestPattern_BaseState_DimmerUpWhenNoTestDrivesIt is the other half of
// "only when needed": a POSITION test leaves the dimmer unowned, so the base
// state drives it to full so the move is actually visible.
func TestPattern_BaseState_DimmerUpWhenNoTestDrivesIt(t *testing.T) {
	rc, tr, _ := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_max"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.BaseState.DimmerDrivenCount != 1 || st.BaseState.ShutterOpenedCount != 1 {
		t.Errorf("BaseState = %+v, want DimmerDrivenCount=1 ShutterOpenedCount=1", st.BaseState)
	}
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] != 255 {
		t.Errorf("dimmer = %d, want 255 (no test drives it, so the base state opens it up)", frame[0])
	}
	if frame[1] != 32 {
		t.Errorf("shutter = %d, want 32", frame[1])
	}
	if frame[2] != 255 {
		t.Errorf("tilt = %d, want 255 (the move_extreme test owns it)", frame[2])
	}
}

// TestPattern_BaseState_IsolateModeZeroesEverythingElse pins that the OLD
// behaviour is still available, on request — it is genuinely useful for
// proving which channel drives which function, which is what it was built
// for.
func TestPattern_BaseState_IsolateModeZeroesEverythingElse(t *testing.T) {
	rc, tr, clock := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	if _, err := rc.SetPatternTests([]Entry{e}, []PatternSpec{{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}}, true); err != nil {
		t.Fatal(err)
	}
	st, err := rc.StartPatternOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !st.BaseState.Isolate {
		t.Error("BaseState.Isolate must be reported true")
	}
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] < 250 {
		t.Errorf("dimmer = %d, want near 255", frame[0])
	}
	if frame[1] != 0 || frame[2] != 0 {
		t.Errorf("isolate mode: shutter/tilt = %d/%d, want 0/0 (nothing but the tested channel)", frame[1], frame[2])
	}
}

// TestPattern_BaseState_ShutterUnknownIsReportedNotGuessed is this project's
// standing rule applied to the honest-judgment part of the base state: with
// no usable ChannelSet name and no GDTF Default, the shutter channel is left
// at 0 and the fixture is NAMED in the status as unknown. Inventing a value
// with no textual basis would be worse than not having one.
func TestPattern_BaseState_ShutterUnknownIsReportedNotGuessed(t *testing.T) {
	rc, tr, _ := harness(t)
	e := Entry{
		ID: "mystery", Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Dimmer", "Dimmer", true, 0, 1),
			// Names that say nothing about an open state, and no Default.
			2: gdtfCF("Shutter1", "Shutter", false, 0, 0,
				ChannelSet{Name: "Mode A", DMXFrom: 10}, ChannelSet{Name: "Mode B", DMXFrom: 20}),
		},
	}
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.BaseState.ShutterUnknownEntries; len(got) != 1 || got[0] != "mystery" {
		t.Errorf("ShutterUnknownEntries = %v, want [mystery]", got)
	}
	if st.BaseState.ShutterOpenedCount != 0 {
		t.Errorf("ShutterOpenedCount = %d, want 0 — nothing was opened, and nothing must be pretended", st.BaseState.ShutterOpenedCount)
	}
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[1] != 0 {
		t.Errorf("unknown shutter channel = %d, want 0 — no value may be invented for it", frame[1])
	}
}

// TestPattern_BaseState_ShutterOpenNoStrobeNameAccepted pins the one
// deliberate subtlety in shutterOpenValue's name matching: "strobe" rules a
// set out, EXCEPT where it appears inside the accept token that matched.
func TestPattern_BaseState_ShutterOpenNoStrobeNameAccepted(t *testing.T) {
	cases := []struct {
		name string
		sets []ChannelSet
		want uint32
		ok   bool
	}{
		{"plain open", []ChannelSet{{Name: "Closed", DMXFrom: 0}, {Name: "Open", DMXFrom: 32}}, 32, true},
		{"open no strobe", []ChannelSet{{Name: "Shutter closed", DMXFrom: 0}, {Name: "Open (no strobe)", DMXFrom: 40}}, 40, true},
		{"no strobe only", []ChannelSet{{Name: "Dark", DMXFrom: 0}, {Name: "No strobe", DMXFrom: 8}}, 8, true},
		{"strobing sets rejected", []ChannelSet{{Name: "Strobe slow", DMXFrom: 64}, {Name: "Random strobe", DMXFrom: 128}}, 0, false},
		{"open pulse is not open", []ChannelSet{{Name: "Open pulse", DMXFrom: 90}}, 0, false},
	}
	for _, tc := range cases {
		got, ok, _ := shutterOpenValue(ChannelFunction{ChannelSets: tc.sets})
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: shutterOpenValue = (%d,%v), want (%d,%v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	// Default fallback, and its absence.
	if got, ok, src := shutterOpenValue(ChannelFunction{ChannelSets: make([]ChannelSet, 0), HasDefault: true, Default: 255}); !ok || got != 255 || src != "gdtfDefault" {
		t.Errorf("Default fallback = (%d,%v,%q), want (255,true,\"gdtfDefault\")", got, ok, src)
	}
	if _, ok, _ := shutterOpenValue(ChannelFunction{ChannelSets: make([]ChannelSet, 0)}); ok {
		t.Error("with neither ChannelSets nor a Default, shutterOpenValue must report unknown, not a guess")
	}
}

// --- selection vs output --------------------------------------------------

// TestPattern_SelectionSurvivesStopAndTogglesLive is the owner's workflow
// verbatim: pick tests before running, start, toggle a test live, stop —
// and the selection is still there afterwards.
func TestPattern_SelectionSurvivesStopAndTogglesLive(t *testing.T) {
	rc, tr, _ := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)

	// 1. Select a test with output OFF — nothing may reach the wire.
	st, err := rc.SetPatternTests([]Entry{e}, []PatternSpec{{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.OutputEnabled || len(st.Tests) != 1 {
		t.Fatalf("after selecting with output off: OutputEnabled=%v Tests=%d, want false,1", st.OutputEnabled, len(st.Tests))
	}
	if got := tr.TakeSent(); len(got) != 0 {
		t.Errorf("selecting a test with output off sent %d packets, want 0", len(got))
	}

	// 2. Start output.
	if st, err = rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	if !st.OutputEnabled {
		t.Fatal("StartPatternOutput must enable output")
	}
	tr.TakeSent()

	// 3. Toggle a SECOND test on while output flows — legal, no error, and
	//    it takes effect on the wire immediately.
	st, err = rc.SelectPatternTest(PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_max"}}, true)
	if err != nil {
		t.Fatalf("toggling a test on while output flows must be legal, got %v", err)
	}
	if len(st.Tests) != 2 {
		t.Fatalf("Tests = %d, want 2", len(st.Tests))
	}
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("toggling a test live must push a frame immediately")
	}
	if frame[2] != 255 {
		t.Errorf("tilt = %d after enabling tilt_max live, want 255", frame[2])
	}

	// 4. Toggle it back off, live.
	if st, err = rc.SelectPatternTest(PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_max"}}, false); err != nil {
		t.Fatal(err)
	}
	if len(st.Tests) != 1 {
		t.Fatalf("Tests = %d after deselecting, want 1", len(st.Tests))
	}

	// 5. Stop: output ceases, selection stays.
	st = rc.StopPatternOutput()
	if st.OutputEnabled {
		t.Error("StopPatternOutput must disable output")
	}
	if len(st.Tests) != 1 {
		t.Errorf("Stop deselected tests (%d left) — the owner's rule is that stop stops OUTPUT and deselects nothing", len(st.Tests))
	}
	if st.LastEndReason != "manual" {
		t.Errorf("LastEndReason = %q, want manual", st.LastEndReason)
	}
	frame2, ok2 := lastFrame(t, tr.TakeSent(), 0)
	if !ok2 {
		t.Fatal("Stop must push a blackout frame immediately, not wait for the retransmit tick")
	}
	for i, v := range frame2 {
		if v != 0 {
			t.Fatalf("channel %d = %d after stop, want 0", i+1, v)
		}
	}

	// 6. And it can be restarted from the surviving selection.
	if st, err = rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	if !st.OutputEnabled || len(st.Tests) != 1 {
		t.Errorf("restart from surviving selection: OutputEnabled=%v Tests=%d", st.OutputEnabled, len(st.Tests))
	}
}

// TestPattern_WatchdogKeepsSelection proves the watchdog stops OUTPUT only —
// a client that comes back finds its tests still picked.
func TestPattern_WatchdogKeepsSelection(t *testing.T) {
	rc, _, clock := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(PatternWatchdogTimeout + 200*time.Millisecond)
	st := rc.PatternStatus()
	if st.OutputEnabled || st.LastEndReason != "watchdog" {
		t.Fatalf("watchdog: OutputEnabled=%v LastEndReason=%q", st.OutputEnabled, st.LastEndReason)
	}
	if len(st.Tests) != 1 {
		t.Errorf("watchdog cleared the selection (%d tests left), want it kept", len(st.Tests))
	}
}

// --- composition order and contention -------------------------------------

// TestPattern_CanonicalOrder_IndependentOfClickOrder is the deterministic
// composition rule: the same set of tests must produce the same frame
// whatever order they were toggled in.
func TestPattern_CanonicalOrder_IndependentOfClickOrder(t *testing.T) {
	e := strobeBarEntry("jdc1", 0, 1)
	extreme := PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_min"}}
	ballyhoo := PatternSpec{Kind: PatternBallyhoo, Params: PatternParams{RateHz: 1, Max: 255}}

	run := func(order []PatternSpec) []byte {
		rc, tr, clock := harness(t)
		if _, err := rc.SetPatternScope([]Entry{e}); err != nil {
			t.Fatal(err)
		}
		for _, spec := range order {
			if _, err := rc.SelectPatternTest(spec, true); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := rc.StartPatternOutput(); err != nil {
			t.Fatal(err)
		}
		tr.TakeSent()
		clock.Advance(300 * time.Millisecond)
		frame, ok := lastFrame(t, tr.TakeSent(), 0)
		if !ok {
			t.Fatal("expected a frame")
		}
		return frame
	}
	a := run([]PatternSpec{extreme, ballyhoo})
	b := run([]PatternSpec{ballyhoo, extreme})
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("frames differ at channel %d (%d vs %d): composition must follow the CANONICAL order, not the order the user clicked", i+1, a[i], b[i])
		}
	}
	// And the canonical order is the documented one: ballyhoo (later in
	// patternKindOrder) wins the shared Tilt offset over move_extreme.
	if a[2] == 0 {
		t.Errorf("tilt = %d, want the ballyhoo value: ballyhoo sorts after move_extreme and must win the contested offset", a[2])
	}
}

// TestPattern_ContestedOffsetsReported proves contention is never silent.
func TestPattern_ContestedOffsetsReported(t *testing.T) {
	rc, _, _ := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	st, err := rc.SetPatternTests([]Entry{e}, []PatternSpec{
		{Kind: PatternBallyhoo, Params: PatternParams{RateHz: 1, Max: 255}},
		{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_min"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Contested) != 1 {
		t.Fatalf("Contested = %+v, want exactly one contested offset (Tilt, channel 3)", st.Contested)
	}
	c := st.Contested[0]
	if c.Channel != 3 || c.EntryID != "jdc1" {
		t.Errorf("contested offset = %+v, want channel 3 on jdc1", c)
	}
	want := []TestID{"move_extreme:tilt_min", "ballyhoo"}
	if len(c.Tests) != 2 || c.Tests[0] != want[0] || c.Tests[1] != want[1] {
		t.Errorf("contested Tests = %v, want %v (canonical order; the last is the winner)", c.Tests, want)
	}
}

// --- phase / offset --------------------------------------------------------

// TestPattern_PhaseSpreadWrapsAtN pins the owner's decision that the divisor
// is n and NOT n-1: 0..360 across 8 fixtures puts the last at 315°, so the
// chase wraps seamlessly back onto the first rather than doubling up on it.
func TestPattern_PhaseSpreadWrapsAtN(t *testing.T) {
	rc, _, _ := harness(t)
	entries := make([]Entry, 0, 8)
	for i := 0; i < 8; i++ {
		entries = append(entries, strobeBarEntry(fmt.Sprintf("e%d", i), 0, uint16(1+i*3)))
	}
	st, err := rc.SetPatternTests(entries, []PatternSpec{
		{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255, OffsetMin: 0, OffsetMax: 360}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	ts := only(t, st)
	for i, es := range ts.Entries {
		want := float64(i) * 45
		if math.Abs(es.PhaseDegrees-want) > 1e-9 {
			t.Errorf("entry %d phase = %v°, want %v° (min + (max-min)*i/n, divisor n=8 not n-1)", i, es.PhaseDegrees, want)
		}
	}

	// 0..720 is two full cycles across the same selection.
	st, err = rc.SelectPatternTest(PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255, OffsetMin: 0, OffsetMax: 720}}, true)
	if err != nil {
		t.Fatal(err)
	}
	ts = only(t, st)
	if got := ts.Entries[1].PhaseDegrees; math.Abs(got-90) > 1e-9 {
		t.Errorf("entry 1 phase with 0..720 = %v°, want 90° (two cycles across 8 fixtures)", got)
	}
}

// TestPattern_PhaseActuallyShiftsTheWaveform proves the phase is not merely
// reported but applied: with a half-cycle spread across two fixtures, one is
// at its floor exactly when the other is at its peak.
func TestPattern_PhaseActuallyShiftsTheWaveform(t *testing.T) {
	rc, tr, _ := harness(t)
	a := strobeBarEntry("a", 0, 1)
	b := strobeBarEntry("b", 0, 10)
	if _, err := rc.SetPatternTests([]Entry{a, b}, []PatternSpec{
		{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255, OffsetMin: 0, OffsetMax: 360}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	// t=0: fixture a is at phase 0 (raised cosine floor -> 0); fixture b is
	// at 180° (peak -> 255).
	if frame[0] != 0 {
		t.Errorf("fixture a dimmer = %d at t=0, want 0 (phase 0, raised-cosine floor)", frame[0])
	}
	if frame[9] < 250 {
		t.Errorf("fixture b dimmer = %d at t=0, want near 255 (phase 180 = half a cycle ahead)", frame[9])
	}
}

// TestPattern_PhaseRejectedOnStaticKinds pins the explicit rejection: a phase
// spread on a pattern with no cycle is a caller error, not a silent no-op.
func TestPattern_PhaseRejectedOnStaticKinds(t *testing.T) {
	rc, _, _ := harness(t)
	e := strobeBarEntry("jdc1", 0, 1)
	for _, spec := range []PatternSpec{
		{Kind: PatternMoveExtreme, Params: PatternParams{Target: "tilt_max", OffsetMax: 360}},
		{Kind: PatternManualValue, Params: PatternParams{Target: "focus", OffsetMin: 90}},
	} {
		if _, err := rc.StartPattern([]Entry{e}, spec); err == nil {
			t.Errorf("StartPattern(%v) accepted a phase offset on a static kind, want an error", spec.Kind)
		}
	}
}

// --- waveform -------------------------------------------------------------

// TestPattern_WaveformSnapAppliesToEveryContinuousPattern proves "snap for
// everything": a colour mix sweep with waveform snap holds at max for half
// the cycle and min for the other half, jumping instantly.
func TestPattern_WaveformSnapAppliesToEveryContinuousPattern(t *testing.T) {
	rc, tr, clock := harness(t)
	e := Entry{
		ID: "rgb", Universe: 0, StartAddress: 1, Footprint: 3,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("ColorAdd_R", "Red", false, 0, 0),
			2: gdtfCF("ColorAdd_G", "Green", false, 0, 0),
			3: gdtfCF("ColorAdd_B", "Blue", false, 0, 0),
		},
	}
	if _, err := rc.StartPattern([]Entry{e}, PatternSpec{
		Kind: PatternColourMixSweep, Params: PatternParams{RateHz: 1, Max: 255, Waveform: WaveSnap},
	}); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	// A raised cosine would be near 0 at t=0.25s and near 255 at 0.5s; a
	// square is flat at 255 for the whole first half and flat at 0 after.
	clock.Advance(250 * time.Millisecond)
	f1, _ := lastFrame(t, tr.TakeSent(), 0)
	clock.Advance(400 * time.Millisecond) // t=0.65s, second half of the cycle
	f2, _ := lastFrame(t, tr.TakeSent(), 0)
	if f1[0] != 255 {
		t.Errorf("snap at t=0.25s = %d, want 255 (flat high for the first half cycle, not a cosine's %v)", f1[0], f1[0])
	}
	if f2[0] != 0 {
		t.Errorf("snap at t=0.65s = %d, want 0 (flat low for the second half cycle)", f2[0])
	}
}

// --- gobo wheels ----------------------------------------------------------

func goboEntry(id string) Entry {
	return Entry{
		ID: id, Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Gobo1", "Gobo Wheel 1", false, 0, 0,
				ChannelSet{Name: "Open", DMXFrom: 0}, ChannelSet{Name: "Dots", DMXFrom: 20}, ChannelSet{Name: "Breakup", DMXFrom: 40}),
			2: gdtfCF("Gobo1WheelSpin", "Gobo 1 Rotation", false, 0, 0),
		},
	}
}

// TestPattern_GoboStep_UsesChannelSets mirrors the colour wheel's contract on
// the new gobo wheel test.
func TestPattern_GoboStep_UsesChannelSets(t *testing.T) {
	rc, tr, clock := harness(t)
	st, err := rc.StartPattern([]Entry{goboEntry("g1")}, PatternSpec{
		Kind: PatternGoboStep, Params: PatternParams{RateHz: 1, Target: "Gobo1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.AppliedCount != 1 || ts.MissingDetailCount != 0 {
		t.Fatalf("AppliedCount=%d MissingDetailCount=%d, want 1,0", ts.AppliedCount, ts.MissingDetailCount)
	}
	tr.TakeSent()
	clock.Advance(1500 * time.Millisecond) // 1 step/sec, 3 slots -> step 1 = "Dots" (20)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] != 20 {
		t.Errorf("gobo wheel = %d at step 1, want 20 (the ChannelSet's own DMXFrom)", frame[0])
	}
}

// TestPattern_GoboStep_DegradesWithoutChannelSets is the "never guess a slot
// boundary" rule, carried over verbatim from the colour wheel.
func TestPattern_GoboStep_DegradesWithoutChannelSets(t *testing.T) {
	rc, _, _ := harness(t)
	e := Entry{ID: "g2", Universe: 0, StartAddress: 1, Footprint: 1, ChannelFunctions: map[uint16]ChannelFunction{
		1: gdtfCF("Gobo1", "Gobo Wheel 1", false, 0, 0),
	}}
	st, err := rc.StartPattern([]Entry{e}, PatternSpec{Kind: PatternGoboStep, Params: PatternParams{RateHz: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if ts := only(t, st); ts.MissingDetailCount != 1 || !ts.Entries[0].DetailMissing {
		t.Error("expected DetailMissing=true and MissingDetailCount=1 with no ChannelSets available")
	}
}

// TestPattern_GoboRotate_DrivesTheWheelsRotationFunction proves the rotate
// test targets the wheel's rotation sibling, not its slot-select channel.
func TestPattern_GoboRotate_DrivesTheWheelsRotationFunction(t *testing.T) {
	rc, tr, clock := harness(t)
	if _, err := rc.SetPatternTests([]Entry{goboEntry("g1")}, []PatternSpec{
		{Kind: PatternGoboRotate, Params: PatternParams{RateHz: 1, Max: 255, Target: "Gobo1"}},
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond) // half way up an ascending sawtooth
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] != 0 {
		t.Errorf("gobo SELECT channel = %d, want 0 — the rotate test must not touch the slot-select channel", frame[0])
	}
	if frame[1] < 100 || frame[1] > 155 {
		t.Errorf("gobo rotation channel = %d at half a cycle, want ~127", frame[1])
	}
}

// --- enumeration instead of guessing --------------------------------------

// TestAvailableTests_EnumeratesFrostAndGoboFromGDTF is the replacement for
// selectFrostTarget's documented guess: one test per frost function the rig
// actually has, labelled from GDTF's own ChannelFunction Name. This package
// does not decide which one is "light".
func TestAvailableTests_EnumeratesFrostAndGoboFromGDTF(t *testing.T) {
	e := Entry{
		ID: "beamy", Universe: 0, StartAddress: 1, Footprint: 4,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Frost1", "Light Frost", false, 0, 0),
			2: gdtfCF("Frost2", "Heavy Frost", false, 0, 0),
			3: gdtfCF("Gobo1", "Gobo Wheel 1", false, 0, 0, ChannelSet{Name: "Open", DMXFrom: 0}),
			4: gdtfCF("Gobo1WheelSpin", "Gobo 1 Rotation", false, 0, 0),
		},
	}
	got := map[TestID]AvailableTest{}
	for _, a := range AvailableTests([]Entry{e}) {
		got[a.ID] = a
	}
	for _, want := range []struct {
		id    TestID
		label string
		attr  string
	}{
		{"frost:Frost1", "Light Frost", "Frost1"},
		{"frost:Frost2", "Heavy Frost", "Frost2"},
		{"gobo_step:Gobo1", "Gobo Wheel 1", "Gobo1"},
		{"gobo_rotate:Gobo1", "Gobo 1 Rotation", "Gobo1WheelSpin"},
	} {
		a, ok := got[want.id]
		if !ok {
			t.Errorf("AvailableTests is missing %q", want.id)
			continue
		}
		if a.Label != want.label || !a.LabelFromGDTF {
			t.Errorf("%s label = %q (fromGDTF=%v), want %q from GDTF", want.id, a.Label, a.LabelFromGDTF, want.label)
		}
		if a.Attribute != want.attr {
			t.Errorf("%s attribute = %q, want %q", want.id, a.Attribute, want.attr)
		}
		if a.FixtureCount != 1 {
			t.Errorf("%s FixtureCount = %d, want 1", want.id, a.FixtureCount)
		}
	}
}

// TestPattern_FrostTargetsOneFunctionOnly proves the enumerated frost tests
// are genuinely separate: driving Frost2 leaves Frost1 alone.
func TestPattern_FrostTargetsOneFunctionOnly(t *testing.T) {
	rc, tr, clock := harness(t)
	e := Entry{
		ID: "frosty", Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Frost1", "Light Frost", false, 0, 0),
			2: gdtfCF("Frost2", "Heavy Frost", false, 0, 0),
		},
	}
	if _, err := rc.SetPatternTests([]Entry{e}, []PatternSpec{
		{Kind: PatternFrost, Params: PatternParams{RateHz: 1, Max: 255, Target: "Frost2"}},
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] != 0 {
		t.Errorf("Frost1 = %d, want 0 — the Frost2 test must drive only Frost2", frame[0])
	}
	if frame[1] < 250 {
		t.Errorf("Frost2 = %d at half cycle, want near 255", frame[1])
	}
}

// --- 16-bit handling ------------------------------------------------------

// TestPattern_16BitPanSweepsCoarseAndFine verifies (rather than assumes) the
// coarse/fine convention: sweeping a 16-bit Pan across its full range must
// produce a smoothly incrementing coarse byte with the fine byte ramping and
// WRAPPING inside each coarse step — not a coarse-only sweep with fine stuck
// at 0, and not a fine byte that jumps discontinuously. The 0x00FF -> 0x0100
// boundary is checked explicitly.
func TestPattern_16BitPanSweepsCoarseAndFine(t *testing.T) {
	// The pure value path, sampled densely — this is where the arithmetic
	// either is or is not right; the frame writer is checked below.
	ft := patternFuncTarget{role: "pan", offsets: []uint16{1, 2}, nbytes: 2}
	spec := PatternSpec{Kind: PatternBallyhoo, Params: PatternParams{RateHz: 0.5, Min: 0, Max: 255}}

	var sawFineWrap, sawCoarseBoundary bool
	prevCoarse, prevFine := -1, -1
	frame := make([]byte, 512)
	for step := 0; step <= 2000; step++ {
		elapsed := float64(step) / 2000 // exactly one half cycle: 0 -> full range
		raw, nbytes := patternValueForFunc(spec, ft, elapsed, 0)
		if nbytes != 2 {
			t.Fatalf("nbytes = %d, want 2", nbytes)
		}
		writeFuncValue(frame, 1, ft.offsets, nbytes, raw)
		coarse, fine := int(frame[0]), int(frame[1])
		if got := coarse*256 + fine; got != int(raw) {
			t.Fatalf("frame bytes %d/%d recompose to %d, want raw %d — offsets must be (coarse, fine)", coarse, fine, got, raw)
		}
		if prevCoarse >= 0 {
			if coarse < prevCoarse {
				t.Fatalf("coarse byte went backwards (%d -> %d) on a monotonic rise", prevCoarse, coarse)
			}
			if coarse > prevCoarse+1 {
				t.Fatalf("coarse byte jumped %d -> %d: the sweep is not smooth", prevCoarse, coarse)
			}
			if coarse == prevCoarse && fine < prevFine {
				t.Fatalf("fine byte went backwards within one coarse step (%d -> %d at coarse %d)", prevFine, fine, coarse)
			}
			if coarse == prevCoarse+1 && fine < prevFine {
				sawFineWrap = true // fine wrapped as coarse incremented — the expected behaviour
			}
			if prevCoarse == 0 && coarse == 1 {
				sawCoarseBoundary = true
				if prevFine < 200 {
					t.Errorf("at the 0x00FF -> 0x0100 boundary the fine byte was only %d before the carry, want it to have climbed near 255", prevFine)
				}
			}
		}
		prevCoarse, prevFine = coarse, fine
	}
	if !sawFineWrap {
		t.Error("the fine byte never wrapped within a coarse step — this is a coarse-only sweep with fine stuck, not real 16-bit resolution")
	}
	if !sawCoarseBoundary {
		t.Error("the sweep never crossed the 0x00FF -> 0x0100 boundary; the test proved nothing about it")
	}
	if prevCoarse != 255 || prevFine != 255 {
		t.Errorf("end of the half cycle = %d/%d, want 255/255 (the full 16-bit range)", prevCoarse, prevFine)
	}
}

// TestPattern_MultipleSameAttributeOffsetsAreNotAFineByte guards the wrinkle
// ResolveEntryGroups' (Attribute, Source) merging creates: a multi-cell
// fixture with one 8-bit Dimmer per cell resolves to ONE function with
// several offsets, which the bare len(Offsets)>=2 convention would read as a
// 16-bit channel and drive with a fast-wrapping fine byte. GDTF's own
// DefaultByteCount=1 says otherwise, and every cell must get the same level.
func TestPattern_MultipleSameAttributeOffsetsAreNotAFineByte(t *testing.T) {
	rc, tr, clock := harness(t)
	e := Entry{
		ID: "multicell", Universe: 0, StartAddress: 1, Footprint: 3,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: gdtfCF("Dimmer", "Dimmer", true, 0, 1),
			2: gdtfCF("Dimmer", "Dimmer", true, 0, 1),
			3: gdtfCF("Dimmer", "Dimmer", true, 0, 1),
		},
	}
	if _, err := rc.SetPatternTests([]Entry{e}, []PatternSpec{
		{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 1, Max: 255}},
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.StartPatternOutput(); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	clock.Advance(500 * time.Millisecond)
	frame, ok := lastFrame(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	if frame[0] < 250 || frame[1] != frame[0] || frame[2] != frame[0] {
		t.Errorf("three 8-bit Dimmer cells = %d/%d/%d, want all three near 255 and equal — GDTF said defaultByteCount=1, so these are sibling cells, not a coarse/fine pair", frame[0], frame[1], frame[2])
	}
}
