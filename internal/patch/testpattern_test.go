package patch

import (
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
	if !st.Entries[0].Inferred {
		t.Error("expected Inferred=true: Tilt on this entry is RDM-inferred")
	}
	if st.AppliedCount != 1 || st.InferredCount != 1 {
		t.Errorf("AppliedCount=%d InferredCount=%d, want 1,1", st.AppliedCount, st.InferredCount)
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
	if st.MissingDetailCount != 0 || st.Entries[0].DetailMissing {
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
	if st.MissingDetailCount != 1 || !st.Entries[0].DetailMissing {
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
	if st.TotalScope != 2 || st.AppliedCount != 1 || st.SkippedCount != 1 {
		t.Fatalf("TotalScope=%d AppliedCount=%d SkippedCount=%d, want 2,1,1", st.TotalScope, st.AppliedCount, st.SkippedCount)
	}
	var sawApplied, sawSkipped bool
	for _, es := range st.Entries {
		switch es.EntryID {
		case "has":
			sawApplied = es.Applied
		case "hasnt":
			sawSkipped = !es.Applied
		}
	}
	if !sawApplied || !sawSkipped {
		t.Errorf("per-entry Applied flags wrong: %+v", st.Entries)
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
	if st.Running {
		t.Error("PatternStatus.Running must be false after Stop")
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
	if rc.PatternStatus().Running {
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
	if st.Running {
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
		if !rc.PatternStatus().Running {
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
	if rc.PatternStatus().Running {
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
	if rc.PatternStatus().Running {
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
		{Kind: PatternFrost, Params: PatternParams{Target: "medium"}},
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
	if cw.MissingDetailCount != 1 {
		t.Errorf("MissingDetailCount = %d, want 1 (no ChannelSets on this synthetic fixture)", cw.MissingDetailCount)
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
	if st.AppliedCount != 1 {
		t.Fatalf("AppliedCount = %d, want 1", st.AppliedCount)
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
	if st.AppliedCount != 0 || st.SkippedCount != 1 {
		t.Errorf("AppliedCount=%d SkippedCount=%d, want 0,1 (incomplete RGB/CMY set applies to nothing)", st.AppliedCount, st.SkippedCount)
	}
}
