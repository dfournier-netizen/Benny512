// This file implements stage 2 of function-aware Rig Check: an
// attribute-level test-pattern engine that rides alongside (never replaces)
// rigcheck.go's existing channel-level walk on the same *RigCheck instance,
// sharing its mutex, its started-universe bookkeeping, and — critically —
// its blackout-on-stop safety discipline (rigcheck.go's file doc comment).
// internal/web/patch.go is the only HTTP-facing consumer; see its "test
// pattern" section for the exact request/response JSON.
//
// --- Selection and output are two independent pieces of state -------------
//
// The owner's workflow, verbatim: "Select testing scope, then pick which
// tests I want, then run. Once running, I should still be able to toggle
// tests live — the start button just allows output to flow, it doesn't limit
// other configuration. Same goes for the stop button — should stop output,
// but not deselect any tests."
//
// So this engine holds TWO orthogonal things:
//
//  1. A SELECTION: a scope (an ordered []Entry) plus a set of selected
//     tests keyed by TestID, each with its own PatternParams, plus the
//     isolate flag. Mutating any of it is legal at any time and never
//     depends on whether output is flowing.
//  2. An OUTPUT-ENABLED flag: whether those tests are currently being
//     rendered onto DMX. Enabling it starts the scope's universes and arms
//     the tick; disabling it blacks out and stops them, and touches the
//     selection not at all.
//
// This replaces the previous "one running pattern, and configuring anything
// while it runs is ErrRigCheckPatternRunning" model, which conflated the two.
// The mutual exclusion that remains is the one that is actually about frame
// safety: while pattern output is flowing, the CLASSIC channel-level
// mutators (SetMode/SetLevel/Jump/Next/Previous/StepChannel) still fail with
// ErrRigCheckPatternRunning, because both engines would otherwise race to
// decide what a shared universe's buffer holds. Configuring TESTS is never
// blocked by anything.
//
// Every safety property of the old model is preserved: StopPatternOutput
// (and Stop, and Blackout) blackout via SendNow immediately rather than
// waiting for the DMX retransmit tick, and the client-liveness watchdog
// still blacks out an abandoned run.
//
// --- Composition order and offset contention (deterministic) --------------
//
// Several tests can be active at once and two of them can legitimately want
// the same DMX offset (a "move to tilt max" and a "ballyhoo" both drive
// Tilt). The composition rule, fixed and documented so the same set of tests
// always produces the same frame regardless of the order the user clicked
// them in:
//
//	Active tests are composed in a CANONICAL ORDER — taxonomy group order
//	(AllGroups: dimmer, position, colour, beam, focus, shaper, other), then
//	pattern kind (patternKindOrder below), then Target lexicographically.
//	NOT the order the user enabled them in. Where two tests write the same
//	absolute DMX slot, the LATER one in that canonical order wins, and every
//	such slot is reported in PatternStatus.Contested so the UI can warn.
//
// Contention is never silent: a contested offset names the universe, the
// absolute channel, the entry, and every TestID that wanted it, in canonical
// order (the last is the winner).
//
// --- Base state: "GDTF defaults + open only when needed" (owner decision) --
//
// This REVERSES the previous "safe mode" decision this file used to
// document ("never a 'helpful' implicit dimmer-up or shutter-open"). That
// rule made the engine physically incapable of doing its job: a real fixture
// needs BOTH a dimmer at level AND a shutter in its open position to emit
// light, so driving every untested channel to 0 guaranteed darkness, and
// driving a Tilt channel to 0 is not "leaving it alone" — it is a commanded
// move to one end of travel, which is exactly what the owner saw when a
// dimmer-only test swung his JDC-1s to their tilt extreme.
//
// The rule now:
//
//	Every channel in scope that no active test is driving is set to its
//	GDTF Default value. Dimmer is driven to full and Shutter to its open
//	position ONLY when no active test already drives that channel — so
//	running the dimmer test means the dimmer test owns the dimmer channel,
//	while the shutter still opens.
//
// A channel whose Default the GDTF file never stated (ChannelFunction.
// HasDefault == false, including a channel with no ChannelFunction at all)
// is LEFT AT 0 and counted in PatternStatus.BaseState.DefaultsUnknownCount.
// Zero is not a claim about that channel — it is this package declining to
// invent a resting value it was never given, and saying so in the status
// rather than pretending. This is the same rule the ChannelSet handling
// below follows.
//
// Finding "shutter open" (see shutterOpenValue): a Shutter/Strobe function's
// GDTF <ChannelSet> names are consulted first — the first set whose name
// reads as an open/no-strobe state and does not read as a
// closed/strobing/pulsing one; failing that, the channel's GDTF Default
// (many fixtures rest with the shutter open); failing BOTH, the channel is
// left at 0 and that entry is reported in
// PatternStatus.BaseState.ShutterUnknownEntries as "shutter position
// unknown". No value is ever guessed: inventing a shutter value with no
// textual basis would be worse than not having one, and a feature that
// cannot work here is reported, never quietly faked.
//
// The previous zero-everything-else behaviour remains available as an
// explicit ISOLATE mode (SetPatternIsolate / the isolate flag), default OFF.
// It is genuinely useful for proving which channel drives which function,
// which is what it was built for — but it is now something the tech asks
// for, not the only thing on offer.
//
// --- Offset / phase (GrandMA3's "phase") ---------------------------------
//
// A time-varying effect is a waveform sampled on a circle; a fixture's
// offset is where on that circle it sits. Spreading offsets across a
// selection produces a chase rather than everything moving in unison.
//
//	With the fixtures a test actually drives ordered by universe then start
//	address, fixture i (0-based) of n gets
//	    phase = OffsetMin + (OffsetMax-OffsetMin) * i / n   [degrees]
//
// The divisor is n, NOT n-1: with min 0 / max 360 across 8 fixtures the last
// sits at 315°, so the chase wraps seamlessly back onto the first. min 0 /
// max 720 therefore shows two full cycles across the selection. Defaults are
// 0 and 0 — everything in unison.
//
// n counts the fixtures this test ACTUALLY drives (its applied targets), not
// the whole scope, so a mixed rig where only half the fixtures have the
// tested function still chases evenly instead of leaving gaps where the
// skipped fixtures would have sat.
//
// Phase applies to every time-varying pattern (sine, snap, ballyhoo, wheel
// stepping, spins, sweeps) and is MEANINGLESS for a static one (manual
// value, move-to-extreme): validatePatternSpec REJECTS a non-zero offset on
// those kinds outright rather than half-applying it.
//
// --- Waveform ------------------------------------------------------------
//
// Every continuous pattern carries a Waveform: "sine" (the default, and the
// previous behaviour) or "snap" (a square wave — hold at min for half the
// cycle, max for the other half, jumping instantly). Same rate, same phase
// treatment. Waveform is a property of the spec, not a PatternKind, so it
// composes with everything. The legacy "dimmer_snap" kind is accepted on the
// wire and normalized to Kind=dimmer_sine + Waveform=snap.
//
// --- Tick source ----------------------------------------------------------
//
// A time-varying pattern needs its own clock-driven recompute loop —
// recomputeLocked (rigcheck.go) only fires in response to an explicit
// mutator call (SetLevel, Next, ...), never on its own. This file adds a
// second, parallel self-rescheduling AfterFunc chain (armPatternTickLocked/
// patternTick), built the exact same way DMXOutputEngine's own tick()/
// scheduleLocked() chain is (session/dmxout.go) — and, crucially, driven by
// the SAME session.Clock the DMX engine itself uses (RigCheck.clock ==
// dmx.Clock()). That is what makes a test's one FakeClock.Advance(...) call
// deterministically settle both the DMX engine's retransmit tick AND every
// active test's value recomputation in the same step, with no time.Sleep
// anywhere in this package's tests. The tick period is r.dmx.Interval().
//
// All active tests share ONE time base (the epoch stamped when output was
// last enabled), so composed tests stay in a fixed phase relationship with
// each other and a test toggled on mid-run joins the others in sync rather
// than starting its own private clock. Re-parameterising a test never
// resets that epoch.
//
// --- Client-disappears-mid-pattern ---------------------------------------
//
// Output, once enabled, needs no further HTTP request to keep moving — it is
// driven entirely by this package's own ticker. A browser tab that crashes,
// or a laptop that loses network, leaves nothing to ever send the Stop a
// classic (static-level) rig check can safely rely on a human for. The
// decision made here is a liveness watchdog (PatternWatchdogTimeout,
// rigcheck.go): every pattern-surface call that proves a client is still
// there — including a plain PatternStatus read — refreshes r.lastTouch, and
// patternTick blackout-and-stops output the moment more than that window has
// elapsed since the last one. A "GET .../rigcheck/pattern" status read counts
// as a touch specifically so a UI that's merely polling to render live state
// keeps output alive for free. The watchdog stops OUTPUT; it does not clear
// the selection, so a client that comes back finds its tests still picked.
//
// --- 16-bit (coarse+fine) attributes --------------------------------------
//
// resolve.go's ResolvedFunction.Offsets doc comment states the convention
// this file relies on: a 16-bit function's two offsets are (coarse, then
// fine), ascending. There is one wrinkle that convention alone cannot
// resolve, and this file now handles it explicitly: ResolveEntryGroups
// merges every offset sharing an (Attribute, Source) pair into ONE
// ResolvedFunction, so a fixture with several 8-bit channels that all
// resolve to the same attribute (a multi-cell strobe with one "Dimmer" per
// cell) is indistinguishable, by offset count alone, from a single 16-bit
// channel. Treating the second cell's Dimmer as a fine byte would drive it
// with a fast-wrapping sawtooth — visible garbage, not a dimmer.
//
// So the byte count comes from GDTF when GDTF stated it —
// ChannelFunction.DefaultByteCount, populated alongside HasDefault — and
// only falls back to the len(Offsets)>=2 convention when it did not. When
// the byte count is 1 but several offsets resolve to the attribute, EVERY
// offset gets the same 8-bit value (which is the correct rendering of "three
// cells that all do Dimmer"), and when it is 2 the first two offsets get
// (coarse, fine). A function GDTF says is 16-bit but that resolved to only
// ONE offset is treated as 8-bit rather than inventing where a fine byte
// might live — there is no case where a wrong guess at a fine channel's
// address beats not having 16-bit precision.
//
// --- Missing GDTF ChannelSet detail --------------------------------------
//
// Where a pattern is inherently discrete (colour wheel stepping, gobo wheel
// stepping, prism in/out) it drives GDTF's own <ChannelSet> DMXFrom values
// when the underlying ChannelFunction has them, and degrades to a raw 0-255
// sweep — flagged via PatternEntryStatus.DetailMissing / TestStatus.
// MissingDetailCount — when it doesn't, rather than inventing where slot
// boundaries might be. Prism/animation/gobo SPIN and shaper rotation are
// treated as pure continuous range controls regardless of ChannelSet
// presence: dividing one rotation-speed channel's 0-255 range into a CW half
// and a CCW half from ChannelSet names or PhysicalFrom signs would itself be
// exactly the kind of slot-boundary guess this rule forbids.
//
// --- Enumeration instead of guessing (frost, gobo wheels) -----------------
//
// PatternFrost used to carry a Target of ""/"light"/"heavy" resolved by a
// documented judgment call. That guess is GONE. Light and heavy frost are
// separate physical filters, and GDTF already says which functions a fixture
// has: AvailableTests enumerates ONE frost test per Frost* function the rig
// actually has, labelled from GDTF's own ChannelFunction Name, ordered by
// attribute. This package does not decide which one is "light" — it says
// what GDTF says and lets the user read the label. Gobo wheels are
// enumerated the same way (one index/step test and one rotate test per
// Gobo<N> the rig actually has), never hardcoded to one wheel.
package patch

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"benny512/internal/session"
)

// PatternKind identifies one attribute-level test pattern. Every value is a
// row in patternGroups below, which both validates it (SetPatternTests/
// SetPatternTest reject anything else) and says which taxonomy group it
// belongs to for reporting.
type PatternKind string

// Pattern kinds, grouped by the taxonomy group they drive. See each
// constant's inline comment for what PatternParams.Target/Direction/Value
// mean for it — an unused param field is simply ignored.
const (
	// --- Dimmer ---
	// PatternDimmerSine drives Dimmer* over [Min,Max] at RateHz, shaped by
	// Params.Waveform ("sine" or "snap").
	PatternDimmerSine PatternKind = "dimmer_sine"
	// PatternDimmerSnap is a LEGACY wire alias, normalized by
	// normalizePatternSpec to PatternDimmerSine with Waveform "snap" — a
	// snap is a waveform, not a kind (see this file's Waveform section).
	PatternDimmerSnap PatternKind = "dimmer_snap"
	// PatternDimmerToggle is static Max when Params.On, else Min — flipped
	// by re-selecting the test with a new On, not time-driven. Not a
	// waveform: there is no cycle here at all.
	PatternDimmerToggle PatternKind = "dimmer_toggle"

	// --- Position ---
	// PatternMoveExtreme is shared with Focus (see below); Target is
	// "<axis>_<extreme>": axis one of pan|tilt|focus|zoom, extreme one of
	// max|min|centre (centre valid for pan/tilt only). STATIC — rejects a
	// non-zero offset/phase.
	PatternMoveExtreme PatternKind = "move_extreme"
	// PatternBallyhoo drives Pan (phase 0) and Tilt (a quarter cycle later)
	// together at RateHz — a continuous Lissajous-style sweep, the classic
	// moving-head "ballyhoo" test. Honours Waveform and phase.
	PatternBallyhoo PatternKind = "ballyhoo"

	// --- Colour ---
	// PatternColourWheelStep advances the ColorWheel attribute through its
	// GDTF ChannelSets, one slot per 1/RateHz seconds, looping.
	PatternColourWheelStep PatternKind = "colour_wheel_step"
	// PatternColourMixSweep drives every RGB (ColorAdd_R/G/B) or, absent
	// that, CMY (ColorSub_C/M/Y) channel with ONE shared waveform in
	// unison — "mixing in and out" together, not a colour change.
	PatternColourMixSweep PatternKind = "colour_mix_sweep"
	// PatternColourFade drives the same RGB-or-CMY channel set with three
	// waves 1/3-cycle apart (a continuous hue rotation).
	PatternColourFade PatternKind = "colour_fade"

	// --- Beam ---
	// PatternFrost sweeps ONE Frost function over [Min,Max] at RateHz.
	// Target is the GDTF attribute name of the frost function to drive
	// (e.g. "Frost1", "Frost2"), or "" for every frost function together.
	// See AvailableTests for how the rig's actual frost functions — and
	// GDTF's own labels for them — are enumerated. This package no longer
	// has any notion of "light" vs "heavy".
	PatternFrost PatternKind = "frost"
	// PatternGoboStep advances one gobo wheel through its GDTF ChannelSet
	// slots, one per 1/RateHz seconds, looping — modelled on
	// PatternColourWheelStep, including its degrade-to-raw-sweep behaviour
	// and DetailMissing reporting. Target is the wheel's attribute name
	// ("Gobo1", "Gobo2", ...), or "" for every wheel the fixture has.
	PatternGoboStep PatternKind = "gobo_step"
	// PatternGoboRotate drives one gobo wheel's ROTATION function (an
	// attribute under the same Gobo<N> prefix whose name contains "Rot" or
	// "Spin", e.g. "Gobo1WheelSpin", "Gobo1PosRotate") as a continuous ramp;
	// Direction "cw" (default) ramps up, "ccw" ramps down. Target is the
	// wheel's base attribute name, same vocabulary as PatternGoboStep.
	PatternGoboRotate PatternKind = "gobo_rotate"
	// PatternPrismInOut square-toggles a non-rotation Prism* function
	// between its "out"/open ChannelSet and its "in"/inserted one when
	// GDTF gives ChannelSet data, else between raw Min and Max.
	PatternPrismInOut PatternKind = "prism_in_out"
	// PatternPrismSpin continuously ramps a Prism* rotation function
	// (attribute name containing "Rot") over [Min,Max].
	PatternPrismSpin PatternKind = "prism_spin"
	// PatternAnimationSpin is PatternPrismSpin's sibling for an Animation*
	// wheel rotate/index attribute.
	PatternAnimationSpin PatternKind = "animation_spin"

	// --- Focus ---
	// PatternManualValue sets Focus or Zoom (Target) to a fixed Params.Value
	// (0-255, scaled proportionally into the fine byte for a 16-bit
	// function), held until deselected. STATIC — rejects a non-zero
	// offset/phase.
	PatternManualValue PatternKind = "manual_value"

	// --- Shaper ---
	// PatternShaperIndividual time-slices 1/RateHz seconds per shaper
	// insertion function (attribute prefix Blade/Shaper, name NOT containing
	// "Rot"), ramping each 0->Max in turn while every other one sits at Min.
	PatternShaperIndividual PatternKind = "shaper_individual"
	// PatternShaperAll sweeps every shaper insertion function together over
	// [Min,Max].
	PatternShaperAll PatternKind = "shaper_all"
	// PatternShaperRotate triangle-sweeps (min -> max -> min, bounded, not a
	// continuous spin) every shaper ROTATION function together.
	PatternShaperRotate PatternKind = "shaper_rotate"
)

// patternGroups is both PatternKind's validity table and its default
// taxonomy group. PatternMoveExtreme has no single fixed group (it drives
// Position for pan/tilt targets, Focus for focus/zoom ones) — its entry here
// is a placeholder never actually reported; groupForPatternSpec resolves it
// properly from Params.Target.
var patternGroups = map[PatternKind]AttributeGroup{
	PatternDimmerSine:       GroupDimmer,
	PatternDimmerSnap:       GroupDimmer,
	PatternDimmerToggle:     GroupDimmer,
	PatternMoveExtreme:      GroupPosition,
	PatternBallyhoo:         GroupPosition,
	PatternColourWheelStep:  GroupColour,
	PatternColourMixSweep:   GroupColour,
	PatternColourFade:       GroupColour,
	PatternFrost:            GroupBeam,
	PatternGoboStep:         GroupBeam,
	PatternGoboRotate:       GroupBeam,
	PatternPrismInOut:       GroupBeam,
	PatternPrismSpin:        GroupBeam,
	PatternAnimationSpin:    GroupBeam,
	PatternManualValue:      GroupFocus,
	PatternShaperIndividual: GroupShaper,
	PatternShaperAll:        GroupShaper,
	PatternShaperRotate:     GroupShaper,
}

// patternKindOrder is the SECOND key of the canonical composition order
// (after taxonomy group; see this file's composition doc section). It is a
// fixed authored list, not a sort of the map above, precisely so the order
// can never depend on map iteration or on when a kind was added.
var patternKindOrder = []PatternKind{
	PatternDimmerToggle, PatternDimmerSine,
	PatternMoveExtreme, PatternBallyhoo,
	PatternColourWheelStep, PatternColourMixSweep, PatternColourFade,
	PatternFrost, PatternGoboStep, PatternGoboRotate,
	PatternPrismInOut, PatternPrismSpin, PatternAnimationSpin,
	PatternManualValue,
	PatternShaperIndividual, PatternShaperAll, PatternShaperRotate,
}

func patternKindRank(k PatternKind) int {
	for i, v := range patternKindOrder {
		if v == k {
			return i
		}
	}
	return len(patternKindOrder) // an unranked kind sorts last, deterministically
}

func groupRank(g AttributeGroup) int {
	for i, v := range AllGroups {
		if v == g {
			return i
		}
	}
	return len(AllGroups)
}

// staticPatternKinds are the kinds with no cycle at all — phase/offset is
// meaningless for them and validatePatternSpec rejects a non-zero one rather
// than half-applying it (this file's phase doc section).
func isStaticPatternKind(k PatternKind) bool {
	switch k {
	case PatternMoveExtreme, PatternManualValue, PatternDimmerToggle:
		return true
	}
	return false
}

// Waveform shapes a continuous pattern's cycle. See this file's Waveform doc
// section: it is a property of the spec, not a PatternKind, so it composes
// with every continuous kind.
type Waveform string

// Waveforms. The empty string is accepted on the wire and means WaveSine —
// the previous, and still default, behaviour.
const (
	WaveSine Waveform = "sine"
	WaveSnap Waveform = "snap"
)

// PatternParams parameterizes one selected test. Every field is optional —
// RateHz<=0 falls back to a sensible per-kind default (defaultRateForKind),
// and Max:0 is normalized to 255. A JSON request with only a pattern kind
// otherwise produces a zero-span range that silently drives nothing; 0 is
// therefore the request-level shorthand for the useful full-range default.
// Target/Direction/Value/On are used only by the PatternKinds documented
// against them above; ignored otherwise.
type PatternParams struct {
	RateHz    float64
	Min       byte
	Max       byte
	Target    string
	Direction string // "cw" (default) | "ccw"
	Value     byte
	On        bool
	// Waveform is "" (== WaveSine), WaveSine or WaveSnap. Ignored by the
	// static kinds (isStaticPatternKind).
	Waveform Waveform
	// OffsetMin/OffsetMax are the phase spread across the fixtures this test
	// drives, in DEGREES — see this file's phase doc section for the exact
	// formula and why the divisor is n and not n-1. Both default to 0:
	// everything in unison. Rejected (not ignored) as non-zero on a static
	// kind.
	OffsetMin float64
	OffsetMax float64
}

// PatternSpec is one selected test: a kind plus its parameters.
type PatternSpec struct {
	Kind   PatternKind
	Params PatternParams
}

// TestID identifies one selected test within the selection. The rule is
// uniform and deliberately simple, so a UI can compute it without a table:
// the kind alone, or "kind:target" when the spec carries a Target. That
// makes the target-enumerated kinds (one test per Frost function, per gobo
// wheel, per move_extreme axis) distinct selectable tests, while every other
// kind is a singleton.
type TestID string

// TestID computes s's identity — see TestID's doc comment.
func (s PatternSpec) TestID() TestID {
	if s.Params.Target == "" {
		return TestID(s.Kind)
	}
	return TestID(string(s.Kind) + ":" + s.Params.Target)
}

// --- validation ----------------------------------------------------------

// normalizePatternSpec applies the wire-compatibility rewrites (legacy
// dimmer_snap -> dimmer_sine + snap) and resolves omitted/default request
// values. They are resolved HERE, once, rather than leaving every tick to do
// it: PatternStatus echoes Params verbatim, so callers can see what is
// actually in effect.
func normalizePatternSpec(spec PatternSpec) PatternSpec {
	if spec.Kind == PatternDimmerSnap {
		spec.Kind = PatternDimmerSine
		spec.Params.Waveform = WaveSnap
	}
	if spec.Params.Waveform == "" {
		spec.Params.Waveform = WaveSine
	}
	if spec.Params.RateHz <= 0 {
		spec.Params.RateHz = defaultRateForKind(spec.Kind)
	}
	if spec.Params.Max == 0 {
		spec.Params.Max = 255
	}
	return spec
}

func validatePatternSpec(spec PatternSpec) error {
	if _, ok := patternGroups[spec.Kind]; !ok {
		return fmt.Errorf("patch: unknown pattern kind %q", spec.Kind)
	}
	if spec.Params.RateHz < 0 {
		return fmt.Errorf("patch: pattern rateHz must be >= 0, got %v", spec.Params.RateHz)
	}
	if d := spec.Params.Direction; d != "" && !strings.EqualFold(d, "cw") && !strings.EqualFold(d, "ccw") {
		return fmt.Errorf("patch: pattern direction must be \"\", \"cw\" or \"ccw\", got %q", d)
	}
	if w := spec.Params.Waveform; w != "" && w != WaveSine && w != WaveSnap {
		return fmt.Errorf("patch: pattern waveform must be \"\", %q or %q, got %q", WaveSine, WaveSnap, w)
	}
	if isStaticPatternKind(spec.Kind) && (spec.Params.OffsetMin != 0 || spec.Params.OffsetMax != 0) {
		// Rejected, not ignored: a phase spread on a pattern with no cycle
		// is a caller misunderstanding, and silently dropping it would let
		// a UI show an offset control that does nothing.
		return fmt.Errorf("patch: pattern kind %q is static — offsetMin/offsetMax (phase) do not apply to it", spec.Kind)
	}
	switch spec.Kind {
	case PatternMoveExtreme:
		if _, _, err := parseMoveExtremeTarget(spec.Params.Target); err != nil {
			return err
		}
	case PatternManualValue:
		if t := spec.Params.Target; t != "focus" && t != "zoom" {
			return fmt.Errorf("patch: manual_value target must be \"focus\" or \"zoom\", got %q", t)
		}
	case PatternFrost:
		// The target is now a GDTF attribute name, not a "light"/"heavy"
		// judgment — see this file's enumeration doc section.
		if t := spec.Params.Target; t != "" && !strings.HasPrefix(t, "Frost") {
			return fmt.Errorf("patch: frost target must be \"\" or a GDTF Frost* attribute name (e.g. \"Frost1\"), got %q", t)
		}
	case PatternGoboStep, PatternGoboRotate:
		if t := spec.Params.Target; t != "" && !strings.HasPrefix(t, "Gobo") {
			return fmt.Errorf("patch: %s target must be \"\" or a GDTF Gobo* attribute name (e.g. \"Gobo1\"), got %q", spec.Kind, t)
		}
	}
	return nil
}

func parseMoveExtremeTarget(target string) (axis, extreme string, err error) {
	parts := strings.SplitN(target, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("patch: move_extreme target must be \"<axis>_<extreme>\", got %q", target)
	}
	axis, extreme = parts[0], parts[1]
	switch axis {
	case "pan", "tilt":
		if extreme != "max" && extreme != "min" && extreme != "centre" {
			return "", "", fmt.Errorf("patch: move_extreme target extreme for %q must be max|min|centre, got %q", axis, extreme)
		}
	case "focus", "zoom":
		if extreme != "max" && extreme != "min" {
			return "", "", fmt.Errorf("patch: move_extreme target extreme for %q must be max|min, got %q", axis, extreme)
		}
	default:
		return "", "", fmt.Errorf("patch: move_extreme target axis must be pan|tilt|focus|zoom, got %q", axis)
	}
	return axis, extreme, nil
}

func groupForPatternSpec(spec PatternSpec) AttributeGroup {
	if spec.Kind == PatternMoveExtreme {
		if axis, _, err := parseMoveExtremeTarget(spec.Params.Target); err == nil && (axis == "focus" || axis == "zoom") {
			return GroupFocus
		}
		return GroupPosition
	}
	if g, ok := patternGroups[spec.Kind]; ok {
		return g
	}
	return GroupOther
}

// --- resolution: which offsets, on which entries, does this test drive ----

func filterAttr(fns []ResolvedFunction, match func(string) bool) []ResolvedFunction {
	out := make([]ResolvedFunction, 0, len(fns))
	for _, f := range fns {
		if match(f.Attribute) {
			out = append(out, f)
		}
	}
	return out
}

func functionsInGroup(groups []GroupAttributes, group AttributeGroup) []ResolvedFunction {
	for _, g := range groups {
		if g.Group == group {
			return g.Functions
		}
	}
	return nil
}

func isShaperRotationAttr(attr string) bool { return strings.Contains(strings.ToLower(attr), "rot") }

// isRotationAttr matches a wheel ROTATION/SPIN function by name. GDTF spells
// these several ways across real exports ("Gobo1WheelSpin", "Gobo1PosRotate",
// "Prism1PosRotate"), so both tokens are matched rather than one canonical
// spelling this package cannot confirm.
func isRotationAttr(attr string) bool {
	lower := strings.ToLower(attr)
	return strings.Contains(lower, "rot") || strings.Contains(lower, "spin")
}

// goboWheelBase returns the "Gobo<N>" prefix of a gobo attribute name, and
// whether attr is a gobo attribute at all. "Gobo1" -> ("Gobo1", true),
// "Gobo1WheelSpin" -> ("Gobo1", true), "GoboWheelMSpeed" -> ("", false) —
// the digits are what identify a wheel, and an attribute with none names no
// particular wheel this package can enumerate.
func goboWheelBase(attr string) (string, bool) {
	if !strings.HasPrefix(attr, "Gobo") {
		return "", false
	}
	i := len("Gobo")
	if i >= len(attr) || attr[i] < '0' || attr[i] > '9' {
		return "", false
	}
	for i < len(attr) && attr[i] >= '0' && attr[i] <= '9' {
		i++
	}
	return attr[:i], true
}

// isGoboSelectAttr reports whether attr is a wheel's SLOT-SELECT attribute
// ("Gobo1"), as opposed to one of its rotation/shake/index siblings.
func isGoboSelectAttr(attr string) bool {
	base, ok := goboWheelBase(attr)
	return ok && base == attr
}

// funcTarget builds one resolved DMX target. nbytes is resolved here, once,
// from GDTF's own byte count where it stated one — see this file's 16-bit doc
// section for why offset count alone is not enough.
func funcTarget(e Entry, rf ResolvedFunction, role string, idx, total int) patternFuncTarget {
	offs := append([]uint16(nil), rf.Offsets...)
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	var sets []ChannelSet
	var cf ChannelFunction
	if len(offs) > 0 {
		cf = e.ChannelFunctions[offs[0]]
		sets = cf.ChannelSets
	}
	return patternFuncTarget{
		role: role, offsets: offs, channelSets: sets, nbytes: funcByteCount(cf, len(offs)),
		inferred: rf.Source == SourceRDMInferred, index: idx, total: total,
	}
}

// funcByteCount decides how many DMX bytes one function's value spans. GDTF's
// own DefaultByteCount wins when the file stated it; otherwise the legacy
// (coarse, fine) convention of "two offsets means 16-bit" applies. Capped at
// the number of offsets actually resolved: a channel GDTF calls 16-bit but
// that resolved to one offset is driven 8-bit rather than inventing a fine
// channel's address. Capped at 2 because nothing in this engine renders a
// 24-bit waveform.
func funcByteCount(cf ChannelFunction, offsetCount int) int {
	n := 1
	switch {
	case cf.HasDefault && cf.DefaultByteCount >= 2:
		n = 2
	case cf.HasDefault && cf.DefaultByteCount == 1:
		n = 1
	case offsetCount >= 2:
		n = 2
	}
	if n > offsetCount {
		n = offsetCount
	}
	if n < 1 {
		n = 1
	}
	return n
}

// pickMixChannels resolves the Colour group's mixing attributes for
// PatternColourMixSweep/PatternColourFade: RGB (ColorAdd_R/G/B) if the
// fixture has all three, else CMY (ColorSub_C/M/Y) if it has those instead.
// A fixture with neither complete set applies to nothing; this file does not
// partially drive one or two channels of an incomplete set, since a lone red
// channel with no green/blue sweeping alongside it would render as a red
// flash, not the effect either pattern is meant to demonstrate.
func pickMixChannels(fns []ResolvedFunction) []string {
	has := make(map[string]bool, len(fns))
	for _, f := range fns {
		has[f.Attribute] = true
	}
	rgb := []string{"ColorAdd_R", "ColorAdd_G", "ColorAdd_B"}
	cmy := []string{"ColorSub_C", "ColorSub_M", "ColorSub_Y"}
	all := func(names []string) bool {
		for _, n := range names {
			if !has[n] {
				return false
			}
		}
		return true
	}
	if all(rgb) {
		return rgb
	}
	if all(cmy) {
		return cmy
	}
	return nil
}

// patternFuncTarget is one resolved DMX target (one attribute, on one entry)
// a test drives, precomputed once at selection time (ChannelFunctions do not
// change while output flows) so every tick only has to evaluate a waveform
// and write bytes, never re-walk the taxonomy.
type patternFuncTarget struct {
	// role tells the per-tick renderer (patternValueForFunc) which physical
	// concept this target is, when that's not fully determined by the
	// pattern Kind alone (a Kind can drive several roles — e.g.
	// PatternBallyhoo's "pan" and "tilt" need different phases).
	//	"static":      a fixed, precomputed value (staticValue) — no per-tick math.
	//	"level":       the generic single-channel [Min,Max] target most range patterns use.
	//	"wheel":       discrete, ChannelSet-driven slot stepping.
	//	"prism_inout": discrete two-state toggle.
	//	"pan"/"tilt":  PatternBallyhoo's two phases.
	//	"fade":        PatternColourFade's per-channel 1/3-cycle offsets.
	//	"slice":       PatternShaperIndividual's dwell slots.
	role        string
	offsets     []uint16 // 1-based-within-footprint, ascending: (coarse) or (coarse, fine)
	channelSets []ChannelSet
	nbytes      int // 1 or 2 — see funcByteCount
	inferred    bool
	index       int // this function's position among its sibling group
	total       int // sibling count for index above
	staticValue uint32
}

// patternEntryTarget is one scoped Entry's resolved targets for ONE test.
type patternEntryTarget struct {
	entryID       string
	universe      uint16
	startAddr     uint16
	inferred      bool
	detailMissing bool
	phase         float64 // cycles (0..1+), computed from the test's OffsetMin/OffsetMax spread
	phaseWeight   uint16  // RDM sub-device phase weight; 0 means the normal single slot
	functions     []patternFuncTarget
}

// resolveEntry computes what (if anything) spec drives on e. ok is false when
// e has no matching function at all (mixed rigs are skipped silently and
// counted, never treated as an error).
func resolveEntry(spec PatternSpec, e Entry) (target patternEntryTarget, ok bool) {
	target = patternEntryTarget{entryID: e.ID, universe: e.Universe, startAddr: e.StartAddress, phaseWeight: e.PhaseWeight}
	groups := ResolveEntryGroups(e)

	add := func(f patternFuncTarget) {
		target.functions = append(target.functions, f)
		if f.inferred {
			target.inferred = true
		}
	}

	switch spec.Kind {
	case PatternDimmerSine, PatternDimmerSnap, PatternDimmerToggle:
		for _, f := range filterAttr(functionsInGroup(groups, GroupDimmer), func(a string) bool { return strings.HasPrefix(a, "Dimmer") }) {
			add(funcTarget(e, f, "level", 0, 1))
		}

	case PatternMoveExtreme:
		axis, extreme, err := parseMoveExtremeTarget(spec.Params.Target)
		if err != nil {
			return target, false
		}
		group, attrName := GroupPosition, ""
		switch axis {
		case "pan":
			attrName = "Pan"
		case "tilt":
			attrName = "Tilt"
		case "focus":
			group, attrName = GroupFocus, "Focus"
		case "zoom":
			group, attrName = GroupFocus, "Zoom"
		}
		for _, f := range filterAttr(functionsInGroup(groups, group), func(a string) bool { return a == attrName }) {
			ft := funcTarget(e, f, "static", 0, 1)
			full := uint32(255)
			if ft.nbytes >= 2 {
				full = 65535
			}
			switch extreme {
			case "max":
				ft.staticValue = full
			case "min":
				ft.staticValue = 0
			case "centre":
				ft.staticValue = full / 2
			}
			add(ft)
		}

	case PatternManualValue:
		attrName := map[string]string{"focus": "Focus", "zoom": "Zoom"}[spec.Params.Target]
		for _, f := range filterAttr(functionsInGroup(groups, GroupFocus), func(a string) bool { return a == attrName }) {
			ft := funcTarget(e, f, "static", 0, 1)
			ft.staticValue = uint32(spec.Params.Value)
			if ft.nbytes >= 2 {
				ft.staticValue = uint32(spec.Params.Value) * 257 // 255*257 == 65535: exact, even byte->16-bit scaling
			}
			add(ft)
		}

	case PatternBallyhoo:
		for _, f := range filterAttr(functionsInGroup(groups, GroupPosition), func(a string) bool { return a == "Pan" }) {
			add(funcTarget(e, f, "pan", 0, 1))
		}
		for _, f := range filterAttr(functionsInGroup(groups, GroupPosition), func(a string) bool { return a == "Tilt" }) {
			add(funcTarget(e, f, "tilt", 0, 1))
		}

	case PatternColourWheelStep:
		for _, f := range filterAttr(functionsInGroup(groups, GroupColour), func(a string) bool { return a == "ColorWheel" }) {
			ft := funcTarget(e, f, "wheel", 0, 1)
			if len(ft.channelSets) == 0 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternColourMixSweep, PatternColourFade:
		names := pickMixChannels(functionsInGroup(groups, GroupColour))
		for i, name := range names {
			for _, f := range filterAttr(functionsInGroup(groups, GroupColour), func(a string) bool { return a == name }) {
				if spec.Kind == PatternColourFade {
					add(funcTarget(e, f, "fade", i, len(names)))
				} else {
					add(funcTarget(e, f, "level", i, len(names)))
				}
			}
		}

	case PatternFrost:
		// Target is a GDTF attribute name, or "" for every frost function
		// this fixture has. No light/heavy judgment is made anywhere.
		want := spec.Params.Target
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool {
			if !strings.HasPrefix(a, "Frost") {
				return false
			}
			return want == "" || a == want
		}) {
			add(funcTarget(e, f, "level", 0, 1))
		}

	case PatternGoboStep:
		want := spec.Params.Target
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool {
			if !isGoboSelectAttr(a) {
				return false
			}
			return want == "" || a == want
		}) {
			ft := funcTarget(e, f, "wheel", 0, 1)
			if len(ft.channelSets) == 0 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternGoboRotate:
		want := spec.Params.Target
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool {
			base, ok := goboWheelBase(a)
			if !ok || base == a || !isRotationAttr(a) {
				return false
			}
			return want == "" || base == want
		}) {
			ft := funcTarget(e, f, "level", 0, 1)
			if len(ft.channelSets) == 0 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternPrismInOut:
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool {
			return strings.HasPrefix(a, "Prism") && !isShaperRotationAttr(a)
		}) {
			ft := funcTarget(e, f, "prism_inout", 0, 1)
			if len(ft.channelSets) < 2 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternPrismSpin:
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool {
			return strings.HasPrefix(a, "Prism") && isShaperRotationAttr(a)
		}) {
			ft := funcTarget(e, f, "level", 0, 1)
			if len(ft.channelSets) == 0 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternAnimationSpin:
		for _, f := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool { return strings.HasPrefix(a, "Animation") }) {
			ft := funcTarget(e, f, "level", 0, 1)
			if len(ft.channelSets) == 0 {
				target.detailMissing = true
			}
			add(ft)
		}

	case PatternShaperIndividual, PatternShaperAll:
		fns := filterAttr(functionsInGroup(groups, GroupShaper), func(a string) bool { return !isShaperRotationAttr(a) })
		sort.Slice(fns, func(i, j int) bool { return fns[i].Attribute < fns[j].Attribute })
		for i, f := range fns {
			if spec.Kind == PatternShaperIndividual {
				add(funcTarget(e, f, "slice", i, len(fns)))
			} else {
				add(funcTarget(e, f, "level", i, len(fns)))
			}
		}

	case PatternShaperRotate:
		for _, f := range filterAttr(functionsInGroup(groups, GroupShaper), isShaperRotationAttr) {
			add(funcTarget(e, f, "level", 0, 1))
		}
	}

	return target, len(target.functions) > 0
}

// --- enumeration: which tests does THIS rig actually offer? ---------------

// AvailableTest is one test a given scope can actually run, with the label a
// UI should show for it. The target-enumerated kinds (frost, gobo) produce
// one AvailableTest per function/wheel the rig really has, labelled from
// GDTF's own ChannelFunction Name — this package never decides which frost
// is "light"; it reports what GDTF says and lets the user read the label.
type AvailableTest struct {
	ID    TestID
	Kind  PatternKind
	Group AttributeGroup
	// Target is the spec Target that produces this test ("" for a singleton
	// kind).
	Target string
	// Label is GDTF's ChannelFunction Name where one exists, else the GDTF
	// attribute name, else the kind's own name. Never invented.
	Label string
	// Attribute is the GDTF attribute this test drives, for the enumerated
	// kinds ("" for a singleton kind).
	Attribute string
	// FixtureCount is how many entries in the scope have the function this
	// test drives — the "N of M" mixed-rig count, at test granularity.
	FixtureCount int
	// LabelFromGDTF is true iff Label came from a GDTF ChannelFunction Name
	// rather than falling back to the attribute name. A UI that wants to
	// show provenance honestly has it here rather than having to guess.
	LabelFromGDTF bool
}

// AvailableTests enumerates every test the given scope can run. Pure —
// no locking, no output, safe to call from a read handler. The returned
// slice is always non-nil (this package's "slices must be make([]T,0)" rule)
// and is in the canonical composition order.
func AvailableTests(entries []Entry) []AvailableTest {
	type acc struct {
		test  AvailableTest
		seen  map[string]bool
		count int
	}
	byID := map[TestID]*acc{}

	note := func(spec PatternSpec, label, attr string, fromGDTF bool, entryID string) {
		id := spec.TestID()
		a, ok := byID[id]
		if !ok {
			a = &acc{test: AvailableTest{
				ID: id, Kind: spec.Kind, Group: groupForPatternSpec(spec), Target: spec.Params.Target,
				Label: label, Attribute: attr, LabelFromGDTF: fromGDTF,
			}, seen: map[string]bool{}}
			byID[id] = a
		}
		if !a.test.LabelFromGDTF && fromGDTF {
			a.test.Label, a.test.LabelFromGDTF = label, true
		}
		if !a.seen[entryID] {
			a.seen[entryID] = true
			a.count++
		}
	}

	for _, e := range entries {
		groups := ResolveEntryGroups(e)
		gdtfLabel := func(rf ResolvedFunction) (string, bool) {
			if len(rf.Offsets) == 0 {
				return rf.Attribute, false
			}
			if name := e.ChannelFunctions[rf.Offsets[0]].FunctionName; name != "" {
				return name, true
			}
			return rf.Attribute, false
		}

		// Singleton kinds: offered when the scope has any function the kind
		// can drive at all. resolveEntry is the single source of truth for
		// "can this kind drive this entry" — enumeration asks it rather than
		// duplicating the matching rules.
		for _, k := range patternKindOrder {
			switch k {
			case PatternFrost, PatternGoboStep, PatternGoboRotate, PatternMoveExtreme, PatternManualValue:
				continue // target-enumerated below
			}
			spec := PatternSpec{Kind: k}
			if _, ok := resolveEntry(spec, e); ok {
				note(spec, string(k), "", false, e.ID)
			}
		}
		for _, t := range []string{"pan_max", "pan_min", "pan_centre", "tilt_max", "tilt_min", "tilt_centre", "focus_max", "focus_min", "zoom_max", "zoom_min"} {
			spec := PatternSpec{Kind: PatternMoveExtreme, Params: PatternParams{Target: t}}
			if _, ok := resolveEntry(spec, e); ok {
				note(spec, t, "", false, e.ID)
			}
		}
		for _, t := range []string{"focus", "zoom"} {
			spec := PatternSpec{Kind: PatternManualValue, Params: PatternParams{Target: t}}
			if _, ok := resolveEntry(spec, e); ok {
				note(spec, t, "", false, e.ID)
			}
		}

		// Frost: one test per Frost* function this fixture has, labelled
		// from GDTF.
		for _, rf := range filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool { return strings.HasPrefix(a, "Frost") }) {
			label, fromGDTF := gdtfLabel(rf)
			note(PatternSpec{Kind: PatternFrost, Params: PatternParams{Target: rf.Attribute}}, label, rf.Attribute, fromGDTF, e.ID)
		}

		// Gobo wheels: one step test per wheel, plus one rotate test per
		// wheel that actually has a rotation function.
		for _, rf := range functionsInGroup(groups, GroupBeam) {
			base, ok := goboWheelBase(rf.Attribute)
			if !ok {
				continue
			}
			if isGoboSelectAttr(rf.Attribute) {
				label, fromGDTF := gdtfLabel(rf)
				note(PatternSpec{Kind: PatternGoboStep, Params: PatternParams{Target: base}}, label, rf.Attribute, fromGDTF, e.ID)
				continue
			}
			if isRotationAttr(rf.Attribute) {
				label, fromGDTF := gdtfLabel(rf)
				note(PatternSpec{Kind: PatternGoboRotate, Params: PatternParams{Target: base}}, label, rf.Attribute, fromGDTF, e.ID)
			}
		}
	}

	out := make([]AvailableTest, 0, len(byID))
	for _, a := range byID {
		a.test.FixtureCount = a.count
		out = append(out, a.test)
	}
	sortAvailable(out)
	return out
}

func sortAvailable(out []AvailableTest) {
	sort.Slice(out, func(i, j int) bool {
		gi, gj := groupRank(out[i].Group), groupRank(out[j].Group)
		if gi != gj {
			return gi < gj
		}
		ki, kj := patternKindRank(out[i].Kind), patternKindRank(out[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return out[i].Target < out[j].Target
	})
}

// --- base state ----------------------------------------------------------

// shutterOpenRejectTokens are ChannelSet name fragments that rule a set OUT
// as "open": either it is closed/dark, or it is an active strobe/effect state
// rather than a steady open one. Matched case-insensitively as substrings.
// Authored from the vocabulary GDTF fixture exports conventionally use; a
// name this table does not recognize simply fails to match "open" and the
// channel falls through to the GDTF Default, then to "unknown".
var shutterOpenRejectTokens = []string{
	"clos", "blackout", "black out", "dark", "off",
	"pulse", "random", "ramp", "sync", "echo", "lightning", "burst", "spike", "strobe",
}

// shutterOpenAcceptTokens are the fragments that read as a steady open /
// no-strobe state, in priority order.
var shutterOpenAcceptTokens = []string{"open", "no strobe", "no shutter"}

// shutterOpenValue finds the DMX value that puts a shutter/strobe channel in
// its open position, honestly:
//
//  1. The channel's GDTF <ChannelSet>s: the first set whose name reads as
//     open/no-strobe (shutterOpenAcceptTokens) and does NOT read as
//     closed/strobing (shutterOpenRejectTokens). Its DMXFrom is the value.
//  2. Failing that, the channel's GDTF Default — many fixtures rest with the
//     shutter open, and a stated Default is real evidence where a name match
//     is absent.
//  3. Failing BOTH: no value at all. ok is false, the caller leaves the
//     channel at 0, and the fixture is reported as "shutter position
//     unknown". Nothing is guessed.
//
// "no strobe" is in the accept list AND "strobe" is in the reject list on
// purpose: the reject scan runs only over names that did not already match a
// more specific accept token, so "Open (no strobe)" is accepted by "open"
// before "strobe" can rule it out. source names where the value came from,
// for the status report.
func shutterOpenValue(cf ChannelFunction) (value uint32, ok bool, source string) {
	for _, want := range shutterOpenAcceptTokens {
		for _, cs := range cf.ChannelSets {
			name := strings.ToLower(cs.Name)
			if !strings.Contains(name, want) {
				continue
			}
			if shutterNameRejected(name, want) {
				continue
			}
			return cs.DMXFrom, true, "channelSet:" + cs.Name
		}
	}
	if cf.HasDefault {
		return cf.Default, true, "gdtfDefault"
	}
	return 0, false, ""
}

// shutterNameRejected reports whether name carries a token that rules it out
// as a steady-open state, ignoring any reject token that is a substring of
// the accept token that already matched (so "no strobe" is not rejected by
// "strobe").
func shutterNameRejected(name, matched string) bool {
	for _, bad := range shutterOpenRejectTokens {
		if strings.Contains(matched, bad) {
			continue
		}
		if strings.Contains(name, bad) {
			return true
		}
	}
	return false
}

// baseWrite is one precomputed base-state assignment: raw value across
// offsets, exactly like a pattern's own write.
type baseWrite struct {
	offsets []uint16
	nbytes  int
	raw     uint32
}

// entryBaseState is one scoped entry's precomputed base state — see this
// file's base-state doc section.
type entryBaseState struct {
	entryID   string
	universe  uint16
	startAddr uint16
	// defaults are every channel whose GDTF Default the file stated.
	defaults []baseWrite
	// dimmer/shutter are the "drive it so the fixture can actually emit
	// light" writes, applied only where no active test owns the offsets.
	dimmer   []baseWrite
	shutter  []baseWrite
	position []baseWrite
	// knownCount/unknownCount count DMX slots in this entry's footprint
	// whose resting value GDTF stated / did not state. An unknown slot is
	// left at 0 — that is not a claim, it is this package declining to
	// invent one (and saying so via PatternStatus).
	knownCount   int
	unknownCount int
	// shutterKnown is false when the entry has a shutter/strobe function
	// whose open position could not be established from either ChannelSets
	// or a GDTF Default. Reported per-entry, never guessed around.
	shutterKnown  bool
	hasShutter    bool
	shutterSource string
}

func isShutterAttr(attr string) bool {
	return strings.HasPrefix(attr, "Shutter") || strings.HasPrefix(attr, "Strobe")
}

// buildEntryBaseState precomputes e's base state. Pure and cheap enough to
// redo on any selection change; nothing here depends on which tests are
// active (that is decided at frame time, by offset ownership).
func buildEntryBaseState(e Entry) entryBaseState {
	bs := entryBaseState{entryID: e.ID, universe: e.Universe, startAddr: e.StartAddress, shutterKnown: true}
	covered := map[uint16]bool{}

	for _, ga := range ResolveEntryGroups(e) {
		for _, rf := range ga.Functions {
			offs := append([]uint16(nil), rf.Offsets...)
			sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
			if len(offs) == 0 {
				continue
			}
			cf := e.ChannelFunctions[offs[0]]
			n := funcByteCount(cf, len(offs))
			for _, off := range offs {
				covered[off] = true
			}
			if cf.HasDefault {
				bs.defaults = append(bs.defaults, baseWrite{offsets: offs, nbytes: n, raw: cf.Default})
				bs.knownCount += len(offs)
			} else {
				bs.unknownCount += len(offs)
			}
			if !cf.HasDefault && (strings.HasPrefix(rf.Attribute, "Pan") || strings.HasPrefix(rf.Attribute, "Tilt")) && !strings.Contains(rf.Attribute, "Speed") && !strings.Contains(rf.Attribute, "Rotate") {
				raw := uint32(128)
				if n >= 2 {
					raw = 32768
				}
				bs.position = append(bs.position, baseWrite{offsets: offs, nbytes: n, raw: raw})
				bs.unknownCount -= len(offs)
			}
			switch {
			case strings.HasPrefix(rf.Attribute, "Dimmer"):
				full := uint32(255)
				if n >= 2 {
					full = 65535
				}
				bs.dimmer = append(bs.dimmer, baseWrite{offsets: offs, nbytes: n, raw: full})
			case isShutterAttr(rf.Attribute):
				bs.hasShutter = true
				v, ok, src := shutterOpenValue(cf)
				if !ok {
					bs.shutterKnown = false
					continue
				}
				bs.shutterSource = src
				bs.shutter = append(bs.shutter, baseWrite{offsets: offs, nbytes: n, raw: v})
			}
		}
	}
	// Footprint slots with no ChannelFunction at all are equally unknown —
	// counting only the resolved ones would understate how much of the rig
	// this engine is flying blind over.
	for off := uint16(1); off <= e.Footprint; off++ {
		if !covered[off] {
			bs.unknownCount++
		}
	}
	return bs
}

// --- waveform math (pure, deterministic — unit-tested directly) -----------

func defaultRateForKind(kind PatternKind) float64 {
	switch kind {
	case PatternDimmerSnap, PatternPrismInOut:
		return 1.0 // 1 Hz on/off snap — fast enough to read as a snap, not a slow fade
	case PatternShaperIndividual:
		return 0.5 // 2s dwell per shaper before moving to the next
	default:
		return 0.25 // a slow, watchable 4s cycle for every other continuous/spin/fade pattern
	}
}

func sineFracPhase(t, rateHz, phase float64) float64 {
	return 0.5 * (1 - math.Cos(2*math.Pi*(rateHz*t+phase)))
}

func fracCyclePhase(t, rateHz, phase float64) float64 {
	x := math.Mod(rateHz*t+phase, 1.0)
	if x < 0 {
		x += 1
	}
	return x
}

func squareHighPhase(t, rateHz, phase float64) bool { return fracCyclePhase(t, rateHz, phase) < 0.5 }

func sawtoothFracPhase(t, rateHz, phase float64) float64 { return fracCyclePhase(t, rateHz, phase) }

func triangleFracPhase(t, rateHz, phase float64) float64 {
	x := fracCyclePhase(t, rateHz, phase)
	if x < 0.5 {
		return 2 * x
	}
	return 2 * (1 - x)
}

// waveFrac is the ONE place a continuous pattern's shape is chosen. native is
// the pattern's own shape when Waveform is sine (a sawtooth for a spin, a
// triangle for a bounded rotate, a raised cosine for everything else); snap
// overrides all of them with a square, per the owner's "snap for everything".
func waveFrac(wf Waveform, native func(t, rate, phase float64) float64, t, rate, phase float64) float64 {
	if wf == WaveSnap {
		if squareHighPhase(t, rate, phase) {
			return 1
		}
		return 0
	}
	return native(t, rate, phase)
}

func scaleMinMax(min, max byte, nbytes int) (uint32, uint32) {
	if nbytes < 2 {
		return uint32(min), uint32(max)
	}
	return uint32(min) * 257, uint32(max) * 257
}

// colourWheelStepValue drives slot-stepping patterns (colour wheel, gobo
// wheel): steps through sets in order, one per 1/rateHz seconds, looping,
// with phase shifting WHICH slot a given fixture is on (a gobo chase).
// Degrades to a raw 0-255 sawtooth sweep (never guessing where slots would
// be) when sets is empty.
func colourWheelStepValue(sets []ChannelSet, elapsed, rateHz, phase float64) byte {
	if len(sets) == 0 {
		return byte(sawtoothFracPhase(elapsed, rateHz, phase) * 255)
	}
	n := len(sets)
	pos := math.Mod(elapsed*rateHz+phase*float64(n), float64(n))
	if pos < 0 {
		pos += float64(n)
	}
	idx := int(pos)
	if idx < 0 || idx >= n {
		idx = 0
	}
	if sets[idx].DMXFrom > 255 {
		return 255
	}
	return byte(sets[idx].DMXFrom)
}

// prismInOutValue square-toggles between the first ChannelSet's DMXFrom
// ("out"/open — GDTF ChannelSets are conventionally authored in ascending
// open-to-closed DMX order) and the last one's ("in"/inserted) when at least
// two are known, else between raw minV/maxV.
func prismInOutValue(sets []ChannelSet, elapsed, rateHz, phase float64, minV, maxV uint32) uint32 {
	if len(sets) >= 2 {
		if squareHighPhase(elapsed, rateHz, phase) {
			return uint32(sets[len(sets)-1].DMXFrom)
		}
		return uint32(sets[0].DMXFrom)
	}
	if squareHighPhase(elapsed, rateHz, phase) {
		return maxV
	}
	return minV
}

// patternValueForFunc computes ft's current raw DMX value (0-255 or 0-65535,
// per ft.nbytes) at elapsed seconds into the run, with phase applied (in
// cycles) for the entry this target belongs to.
func patternValueForFunc(spec PatternSpec, ft patternFuncTarget, elapsed, phase float64) (raw uint32, nbytes int) {
	nbytes = ft.nbytes
	if ft.role == "static" {
		return ft.staticValue, nbytes
	}

	minV, maxV := scaleMinMax(spec.Params.Min, spec.Params.Max, nbytes)
	if maxV < minV {
		minV, maxV = maxV, minV
	}
	span := float64(maxV - minV)
	rate := spec.Params.RateHz
	if rate <= 0 {
		rate = defaultRateForKind(spec.Kind)
	}
	wf := spec.Params.Waveform
	ccw := strings.EqualFold(spec.Params.Direction, "ccw")

	switch ft.role {
	case "wheel":
		v := uint32(colourWheelStepValue(ft.channelSets, elapsed, rate, phase))
		if nbytes >= 2 {
			return v * 257, nbytes
		}
		return v, nbytes
	case "prism_inout":
		return prismInOutValue(ft.channelSets, elapsed, rate, phase, minV, maxV), nbytes
	case "pan":
		return minV + uint32(waveFrac(wf, sineFracPhase, elapsed, rate, phase)*span), nbytes
	case "tilt":
		return minV + uint32(waveFrac(wf, sineFracPhase, elapsed, rate, phase+0.25)*span), nbytes
	case "fade":
		p := phase
		if ft.total > 0 {
			p += float64(ft.index) / float64(ft.total)
		}
		return minV + uint32(waveFrac(wf, sineFracPhase, elapsed, rate, p)*span), nbytes
	case "slice":
		total := ft.total
		if total <= 0 {
			total = 1
		}
		cyc := math.Mod(elapsed*rate+phase*float64(total), float64(total))
		if cyc < 0 {
			cyc += float64(total)
		}
		active := int(cyc)
		frac := cyc - float64(active)
		if active != ft.index {
			return minV, nbytes
		}
		return minV + uint32(frac*span), nbytes
	}

	// role == "level": the specific waveform shape depends on Kind.
	switch spec.Kind {
	case PatternDimmerToggle:
		if spec.Params.On {
			return maxV, nbytes
		}
		return minV, nbytes
	case PatternPrismSpin, PatternAnimationSpin, PatternGoboRotate:
		x := waveFrac(wf, sawtoothFracPhase, elapsed, rate, phase)
		if ccw {
			x = 1 - x
		}
		return minV + uint32(x*span), nbytes
	case PatternShaperRotate:
		return minV + uint32(waveFrac(wf, triangleFracPhase, elapsed, rate, phase)*span), nbytes
	default: // PatternDimmerSine, PatternColourMixSweep, PatternFrost, PatternShaperAll
		return minV + uint32(waveFrac(wf, sineFracPhase, elapsed, rate, phase)*span), nbytes
	}
}

// --- status types --------------------------------------------------------

// PatternEntryStatus is one scoped entry's outcome for ONE test — the
// per-fixture half of the mixed-rig "12 of 40 fixtures" counting.
type PatternEntryStatus struct {
	EntryID string
	// Applied is true iff this entry had at least one function this test
	// could drive. A false entry was silently skipped, never written to.
	Applied bool
	// Inferred is true iff at least one offset this test drives on this
	// entry came from an RDM-inferred (not GDTF) ChannelFunction — only
	// meaningful when Applied.
	Inferred bool
	// DetailMissing is true iff a discrete (slot-type) pattern had to
	// degrade to a raw range sweep on this entry for lack of GDTF ChannelSet
	// data. Always false when Applied is false.
	DetailMissing bool
	// PhaseDegrees is where on the waveform's circle this fixture sits, for
	// this test — 0 for every fixture when the test has no offset spread.
	// Deliberately reported per entry so a UI can show the chase it is about
	// to produce before anything moves.
	PhaseDegrees float64
}

// TestStatus is one selected test's full state.
type TestStatus struct {
	ID     TestID
	Kind   PatternKind
	Group  AttributeGroup
	Params PatternParams
	// TotalScope is the whole scope; AppliedCount the entries this test can
	// actually drive.
	TotalScope         int
	AppliedCount       int
	SkippedCount       int
	InferredCount      int
	MissingDetailCount int
	Entries            []PatternEntryStatus
}

// ContestedOffset is one absolute DMX slot that more than one active test
// wants to write. Tests is in canonical composition order — the LAST one is
// the winner. Never silent: this exists so a UI can warn rather than let one
// pattern corrupt another unseen.
type ContestedOffset struct {
	Universe uint16
	Channel  uint16 // absolute 1-based DMX address
	EntryID  string
	Tests    []TestID
}

// BaseStateStatus reports what the base state did and — just as importantly —
// what it could not do. See this file's base-state doc section.
type BaseStateStatus struct {
	// Isolate mirrors the run's isolate flag: when true, NO base state is
	// applied at all (every channel not driven by an active test is 0) and
	// every count below is 0.
	Isolate bool
	// DefaultsKnownCount/DefaultsUnknownCount count DMX slots across the
	// whole scope whose GDTF resting value was / was not stated. An unknown
	// slot is left at 0 and this count is how a caller learns that.
	DefaultsKnownCount   int
	DefaultsUnknownCount int
	// DimmerDrivenCount is entries whose dimmer the base state drove to full
	// because no active test owned it; ShutterOpenedCount the same for
	// shutter-open.
	DimmerDrivenCount  int
	ShutterOpenedCount int
	// ShutterUnknownEntries names every entry that HAS a shutter/strobe
	// function whose open position could not be established from either
	// GDTF ChannelSets or a GDTF Default. Those channels are left at 0 —
	// the fixture will probably not emit light, and this is the engine
	// saying so rather than inventing a value.
	ShutterUnknownEntries []string
}

// PatternStatus is a full snapshot of the pattern engine — both halves of the
// state model (see this file's doc comment): the SELECTION (Scope*, Tests,
// Isolate), which persists across stop/start, and the OUTPUT flag.
type PatternStatus struct {
	// OutputEnabled is whether the selected tests are currently being
	// rendered to DMX. Toggling it never changes the selection.
	OutputEnabled bool
	// TotalScope is how many entries the selection's scope covers.
	TotalScope int
	// ElapsedMS is milliseconds since output was last enabled (0 when it is
	// not) — the shared time base every active test's waveform is sampled
	// against.
	ElapsedMS int64
	Tests     []TestStatus
	Contested []ContestedOffset
	BaseState BaseStateStatus
	// Available is every test this scope could run, enumerated from the
	// rig's own GDTF data — see AvailableTests.
	Available []AvailableTest
	// LastEndReason explains how OUTPUT most recently stopped: "" (never
	// enabled on this RigCheck instance), "manual" (Stop/StopPatternOutput,
	// or Blackout), "restarted" (superseded by a classic Start), or
	// "watchdog" (PatternWatchdogTimeout elapsed with no client touch).
	// Sticky across OutputEnabled becoming false so a UI that polls in and
	// finds nothing running can still tell "the user hit Stop" apart from
	// "the browser tab died and the watchdog caught it".
	LastEndReason string
}

// --- the selection -------------------------------------------------------

// patternTest is one selected test plus its resolved targets.
type patternTest struct {
	spec               PatternSpec
	targets            []patternEntryTarget
	appliedCount       int
	inferredCount      int
	missingDetailCount int
}

// patternSelection is the whole selection half of the state model. Held on
// RigCheck.selection; survives every stop.
type patternSelection struct {
	scope   []Entry
	tests   map[TestID]PatternSpec
	isolate bool

	// resolved is rebuilt from scope+tests on every mutation, in canonical
	// order.
	order []TestID
	built map[TestID]*patternTest
	base  []entryBaseState
}

func newPatternSelection() *patternSelection {
	return &patternSelection{tests: map[TestID]PatternSpec{}, built: map[TestID]*patternTest{}}
}

// rebuild re-resolves every selected test against the current scope and
// recomputes the canonical composition order and the per-entry base state.
// Cheap enough to run on every mutation, and running it unconditionally is
// what makes "toggle a test live" and "toggle a test before starting"
// literally the same code path.
func (sel *patternSelection) rebuild() {
	sel.order = make([]TestID, 0, len(sel.tests))
	for id := range sel.tests {
		sel.order = append(sel.order, id)
	}
	sort.Slice(sel.order, func(i, j int) bool {
		a, b := sel.tests[sel.order[i]], sel.tests[sel.order[j]]
		ga, gb := groupRank(groupForPatternSpec(a)), groupRank(groupForPatternSpec(b))
		if ga != gb {
			return ga < gb
		}
		ka, kb := patternKindRank(a.Kind), patternKindRank(b.Kind)
		if ka != kb {
			return ka < kb
		}
		if a.Params.Target != b.Params.Target {
			return a.Params.Target < b.Params.Target
		}
		return sel.order[i] < sel.order[j]
	})

	sel.built = make(map[TestID]*patternTest, len(sel.tests))
	for id, spec := range sel.tests {
		pt := &patternTest{spec: spec}
		for _, e := range sel.scope {
			t, ok := resolveEntry(spec, e)
			if !ok {
				continue
			}
			pt.targets = append(pt.targets, t)
			pt.appliedCount++
			if t.inferred {
				pt.inferredCount++
			}
			if t.detailMissing {
				pt.missingDetailCount++
			}
		}
		assignPhases(pt)
		sel.built[id] = pt
	}

	sel.base = make([]entryBaseState, 0, len(sel.scope))
	for _, e := range sel.scope {
		sel.base = append(sel.base, buildEntryBaseState(e))
	}
}

// assignPhases spreads pt's OffsetMin..OffsetMax across the fixtures the test
// actually drives, ordered by universe then start address. A target's
// runtime-only PhaseWeight consumes that many positions in the calculation,
// while it remains one normal fixture everywhere else.
func assignPhases(pt *patternTest) {
	if len(pt.targets) == 0 {
		return
	}
	ordered := make([]int, len(pt.targets))
	for i := range ordered {
		ordered[i] = i
	}
	sort.SliceStable(ordered, func(a, b int) bool {
		ta, tb := pt.targets[ordered[a]], pt.targets[ordered[b]]
		if ta.universe != tb.universe {
			return ta.universe < tb.universe
		}
		if ta.startAddr != tb.startAddr {
			return ta.startAddr < tb.startAddr
		}
		return ta.entryID < tb.entryID
	})
	totalWeight := 0
	for _, idx := range ordered {
		weight := int(pt.targets[idx].phaseWeight)
		if weight < 1 {
			weight = 1
		}
		totalWeight += weight
	}
	min, max := pt.spec.Params.OffsetMin, pt.spec.Params.OffsetMax
	position := 0
	for _, idx := range ordered {
		deg := min + (max-min)*float64(position)/float64(totalWeight)
		pt.targets[idx].phase = deg / 360
		weight := int(pt.targets[idx].phaseWeight)
		if weight < 1 {
			weight = 1
		}
		position += weight
	}
}

// --- RigCheck integration: selection mutators ----------------------------

// SetPatternScope replaces the pattern engine's scope (already ordered/scoped
// by the caller — see rigCheckScopeEntries in internal/web/patch.go), keeping
// every selected test. Legal whether or not output is flowing: changing scope
// while running re-resolves every test onto the new fixtures and takes effect
// on the next frame (pushed immediately, not on the next tick).
func (r *RigCheck) SetPatternScope(entries []Entry) (PatternStatus, error) {
	if len(entries) == 0 {
		return PatternStatus{}, ErrRigCheckEmptyScope
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selection.scope = append([]Entry(nil), entries...)
	r.selection.rebuild()
	r.afterSelectionChangeLocked()
	return r.patternStatusLocked(), nil
}

// SelectPatternTest enables (or, with enabled=false, disables) one test,
// carrying its parameters. Re-selecting an already-selected test replaces its
// parameters wholesale (the same whole-value-replace convention SetLevel and
// SetMode use). Identical behaviour whether or not output is flowing — that
// is the whole point of the two-piece state model.
func (r *RigCheck) SelectPatternTest(spec PatternSpec, enabled bool) (PatternStatus, error) {
	spec = normalizePatternSpec(spec)
	if err := validatePatternSpec(spec); err != nil {
		return PatternStatus{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if enabled {
		r.selection.tests[spec.TestID()] = spec
	} else {
		delete(r.selection.tests, spec.TestID())
	}
	r.selection.rebuild()
	r.afterSelectionChangeLocked()
	return r.patternStatusLocked(), nil
}

// SetPatternTests replaces the ENTIRE selection — scope, the set of selected
// tests, and the isolate flag — in one call. This is the idempotent
// whole-state apply a UI uses to push "here is everything I have selected";
// it never touches the output flag.
func (r *RigCheck) SetPatternTests(entries []Entry, specs []PatternSpec, isolate bool) (PatternStatus, error) {
	if len(entries) == 0 {
		return PatternStatus{}, ErrRigCheckEmptyScope
	}
	normalized := make([]PatternSpec, 0, len(specs))
	for _, spec := range specs {
		spec = normalizePatternSpec(spec)
		if err := validatePatternSpec(spec); err != nil {
			return PatternStatus{}, err
		}
		normalized = append(normalized, spec)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selection.scope = append([]Entry(nil), entries...)
	r.selection.tests = make(map[TestID]PatternSpec, len(normalized))
	for _, spec := range normalized {
		r.selection.tests[spec.TestID()] = spec
	}
	r.selection.isolate = isolate
	r.selection.rebuild()
	r.afterSelectionChangeLocked()
	return r.patternStatusLocked(), nil
}

// SetPatternIsolate turns the old zero-everything-else behaviour on or off —
// see this file's base-state doc section. Legal at any time; takes effect on
// the next frame, pushed immediately.
func (r *RigCheck) SetPatternIsolate(isolate bool) PatternStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selection.isolate = isolate
	r.afterSelectionChangeLocked()
	return r.patternStatusLocked()
}

// afterSelectionChangeLocked re-renders the current frame if (and only if)
// output is flowing. A selection change with output off is pure bookkeeping —
// nothing reaches the wire, which is exactly the guarantee "pick your tests
// before you press start" needs.
func (r *RigCheck) afterSelectionChangeLocked() {
	r.lastTouch = r.clock.Now()
	if !r.patternOutput {
		return
	}
	r.recomputePatternLocked(r.clock.Now().Sub(r.patternEpoch).Seconds())
}

// --- RigCheck integration: the output flag -------------------------------

// StartPatternOutput lets output flow for the current selection. It
// supersedes any classic (channel-level) run exactly as classic Start
// supersedes a pattern, starts every universe the scope covers, stamps the
// shared waveform epoch, and pushes the first frame immediately rather than
// waiting for a tick. Idempotent: enabling output that is already enabled
// does NOT restart the waveforms.
func (r *RigCheck) StartPatternOutput() (PatternStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.selection.scope) == 0 {
		return PatternStatus{}, ErrRigCheckEmptyScope
	}
	if r.patternOutput {
		r.lastTouch = r.clock.Now()
		return r.patternStatusLocked(), nil
	}
	r.stopLocked("restarted") // supersede any classic run; leaves the selection alone

	// Pattern output goes out on whatever protocol the last Start armed
	// (r.out.proto), through the same output boundary the classic walk uses,
	// so a universe is never driven by both protocols here either.
	for _, u := range scopeUniverses(r.selection.scope) {
		if err := r.out.startUniverse(u); err != nil {
			r.stopLocked("restarted")
			return PatternStatus{}, err
		}
		r.started[u] = true
	}
	if r.out.proto == ProtocolArtNet {
		r.dmx.Start()
	}

	now := r.clock.Now()
	r.patternOutput = true
	r.running = true
	r.patternEpoch = now
	r.lastPatternEnd = ""
	r.lastTouch = now

	r.recomputePatternLocked(0)
	r.armPatternTickLocked()
	return r.patternStatusLocked(), nil
}

// SavedPattern captures settings only, without touching the watchdog or output.
func (r *RigCheck) SavedPattern() ([]string, []PatternSpec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.selection.scope))
	for _, e := range r.selection.scope {
		ids = append(ids, e.ID)
	}
	specs := make([]PatternSpec, 0, len(r.selection.order))
	for _, id := range r.selection.order {
		specs = append(specs, r.selection.tests[id])
	}
	return ids, specs, r.selection.isolate
}

// StopPatternOutput blacks out and ceases output while leaving the selection
// COMPLETELY intact — the owner's "stop button should stop output, but not
// deselect any tests". The blackout goes out via SendNow immediately, not on
// the next retransmit tick, exactly like every other stop path in
// rigcheck.go.
func (r *RigCheck) StopPatternOutput() PatternStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked("manual")
	r.lastTouch = r.clock.Now()
	return r.patternStatusLocked()
}

// PatternStatus reports the whole engine state. Every call is itself a
// client-liveness touch while output is flowing — see this file's doc comment
// on why a status poll doubles as the watchdog heartbeat.
func (r *RigCheck) PatternStatus() PatternStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.patternOutput {
		r.lastTouch = r.clock.Now()
	}
	return r.patternStatusLocked()
}

// --- back-compatible single-test surface ---------------------------------

// StartPattern is the pre-stackable-tests entry point, kept because it is
// exactly "replace the whole selection with this ONE test, over this scope,
// and let output flow" — still a useful shorthand, and the shape every
// existing caller and test uses. New callers wanting several tests at once
// want SetPatternTests + StartPatternOutput.
func (r *RigCheck) StartPattern(entries []Entry, spec PatternSpec) (PatternStatus, error) {
	if _, err := r.SetPatternTests(entries, []PatternSpec{spec}, false); err != nil {
		return PatternStatus{}, err
	}
	return r.StartPatternOutput()
}

// AdjustPattern replaces the parameters of the ONE selected test, when
// exactly one is selected — the pre-stackable-tests shorthand for
// SelectPatternTest. Returns ErrRigCheckNoPatternRunning when nothing is
// selected, and ErrRigCheckAmbiguousTest when several tests are, since there
// is then no single test this call could mean. Kind cannot be changed this
// way; Target can (it re-resolves which attribute is driven), which also
// changes the test's ID.
func (r *RigCheck) AdjustPattern(params PatternParams) (PatternStatus, error) {
	r.mu.Lock()
	kind := PatternKind("")
	switch len(r.selection.tests) {
	case 0:
		r.mu.Unlock()
		return PatternStatus{}, ErrRigCheckNoPatternRunning
	case 1:
		for _, spec := range r.selection.tests {
			kind = spec.Kind
		}
	default:
		r.mu.Unlock()
		return PatternStatus{}, ErrRigCheckAmbiguousTest
	}
	// Validate before mutating: an invalid adjust must leave the existing
	// selection exactly as it was, not half-replaced.
	spec := normalizePatternSpec(PatternSpec{Kind: kind, Params: params})
	if err := validatePatternSpec(spec); err != nil {
		r.mu.Unlock()
		return PatternStatus{}, err
	}
	r.selection.tests = map[TestID]PatternSpec{spec.TestID(): spec}
	r.selection.rebuild()
	r.afterSelectionChangeLocked()
	st := r.patternStatusLocked()
	r.mu.Unlock()
	return st, nil
}

// --- status assembly -----------------------------------------------------

func (r *RigCheck) patternStatusLocked() PatternStatus {
	sel := r.selection
	st := PatternStatus{
		OutputEnabled: r.patternOutput,
		TotalScope:    len(sel.scope),
		LastEndReason: r.lastPatternEnd,
		Tests:         make([]TestStatus, 0, len(sel.order)),
		Contested:     make([]ContestedOffset, 0),
		Available:     AvailableTests(sel.scope),
		BaseState:     BaseStateStatus{Isolate: sel.isolate, ShutterUnknownEntries: make([]string, 0)},
	}
	if r.patternOutput {
		st.ElapsedMS = r.clock.Now().Sub(r.patternEpoch).Milliseconds()
	}

	for _, id := range sel.order {
		pt := sel.built[id]
		if pt == nil {
			continue
		}
		ts := TestStatus{
			ID: id, Kind: pt.spec.Kind, Group: groupForPatternSpec(pt.spec), Params: pt.spec.Params,
			TotalScope: len(sel.scope), AppliedCount: pt.appliedCount,
			SkippedCount: len(sel.scope) - pt.appliedCount, InferredCount: pt.inferredCount,
			MissingDetailCount: pt.missingDetailCount,
			Entries:            make([]PatternEntryStatus, 0, len(sel.scope)),
		}
		byID := make(map[string]patternEntryTarget, len(pt.targets))
		for _, t := range pt.targets {
			byID[t.entryID] = t
		}
		for _, e := range sel.scope {
			t, applied := byID[e.ID]
			ts.Entries = append(ts.Entries, PatternEntryStatus{
				EntryID: e.ID, Applied: applied,
				Inferred: applied && t.inferred, DetailMissing: applied && t.detailMissing,
				PhaseDegrees: t.phase * 360,
			})
		}
		st.Tests = append(st.Tests, ts)
	}

	// Compose a throwaway frame purely to report contention and base-state
	// counts. Doing it through the SAME composePatternLocked the wire frames
	// go through is deliberate: a status that reported contention from its
	// own separate walk could drift out of agreement with what is actually
	// being sent.
	elapsed := 0.0
	if r.patternOutput {
		elapsed = r.clock.Now().Sub(r.patternEpoch).Seconds()
	}
	comp := r.composePatternLocked(elapsed)
	st.Contested = comp.contested
	st.BaseState = comp.base
	st.BaseState.Isolate = sel.isolate
	return st
}

// --- frame composition ---------------------------------------------------

// patternComposition is one built frame set plus everything the status needs
// to say about how it was built.
type patternComposition struct {
	frames    map[uint16][]byte
	contested []ContestedOffset
	base      BaseStateStatus
}

func slotKey(universe uint16, ch int) uint32 { return uint32(universe)<<16 | uint32(uint16(ch)) }

// composePatternLocked builds every scoped universe's frame from scratch
// (always starting all-zero), composing the active tests in CANONICAL ORDER
// and then filling in the base state on every slot no test claimed. See this
// file's composition and base-state doc sections for the rules; this function
// is their single implementation, used by both the wire path
// (recomputePatternLocked) and the status path.
func (r *RigCheck) composePatternLocked(elapsed float64) patternComposition {
	sel := r.selection
	comp := patternComposition{
		frames:    make(map[uint16][]byte, len(sel.scope)),
		contested: make([]ContestedOffset, 0),
		base:      BaseStateStatus{Isolate: sel.isolate, ShutterUnknownEntries: make([]string, 0)},
	}
	for _, e := range sel.scope {
		if _, ok := comp.frames[e.Universe]; !ok {
			comp.frames[e.Universe] = make([]byte, session.DMXUniverseSize)
		}
	}

	owner := make(map[uint32]TestID)
	contest := make(map[uint32]*ContestedOffset)
	claim := func(universe uint16, ch int, entryID string, id TestID) {
		key := slotKey(universe, ch)
		prev, seen := owner[key]
		owner[key] = id
		if !seen || prev == id {
			return
		}
		c, ok := contest[key]
		if !ok {
			c = &ContestedOffset{Universe: universe, Channel: uint16(ch), EntryID: entryID, Tests: []TestID{prev}}
			contest[key] = c
		}
		c.Tests = append(c.Tests, id)
	}

	// 1. Active tests, in canonical order — later wins any contested slot.
	for _, id := range sel.order {
		pt := sel.built[id]
		if pt == nil {
			continue
		}
		for _, et := range pt.targets {
			frame, ok := comp.frames[et.universe]
			if !ok {
				continue
			}
			for _, ft := range et.functions {
				raw, nbytes := patternValueForFunc(pt.spec, ft, elapsed, et.phase)
				for _, ch := range writeFuncValue(frame, et.startAddr, ft.offsets, nbytes, raw) {
					claim(et.universe, ch, et.entryID, id)
				}
			}
		}
	}

	// 2. Base state, on every slot no test claimed. Skipped entirely in
	//    isolate mode — that mode's whole purpose is a frame in which only
	//    the tested channel is non-zero.
	if !sel.isolate {
		for _, bs := range sel.base {
			frame, ok := comp.frames[bs.universe]
			if !ok {
				continue
			}
			comp.base.DefaultsKnownCount += bs.knownCount
			comp.base.DefaultsUnknownCount += bs.unknownCount
			apply := func(w baseWrite) bool {
				// "only when no active test already drives that channel":
				// ownership of ANY of the function's offsets means the test
				// owns the function, and the base state keeps its hands off
				// all of it (half-overwriting a 16-bit channel's fine byte
				// would be worse than not touching it).
				for _, off := range w.offsets {
					ch := int(bs.startAddr) + int(off) - 1
					if _, taken := owner[slotKey(bs.universe, ch)]; taken {
						return false
					}
				}
				writeFuncValue(frame, bs.startAddr, w.offsets, w.nbytes, w.raw)
				return true
			}
			for _, w := range bs.defaults {
				apply(w)
			}
			for _, w := range bs.dimmer {
				if apply(w) {
					comp.base.DimmerDrivenCount++
					break // count fixtures, not channels
				}
			}
			opened := false
			for _, w := range bs.shutter {
				if apply(w) {
					opened = true
				}
			}
			for _, w := range bs.position {
				apply(w)
			}
			if opened {
				comp.base.ShutterOpenedCount++
			}
			if bs.hasShutter && !bs.shutterKnown {
				comp.base.ShutterUnknownEntries = append(comp.base.ShutterUnknownEntries, bs.entryID)
			}
		}
	}

	keys := make([]uint32, 0, len(contest))
	for k := range contest {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		comp.contested = append(comp.contested, *contest[k])
	}
	return comp
}

// writeFuncValue writes raw into frame at the absolute DMX offset(s) derived
// from startAddr, splitting a 16-bit value into (coarse=hi, fine=lo) across
// the first two offsets when nbytes is 2. When nbytes is 1 and several
// offsets resolved to the same attribute, EVERY offset gets the same 8-bit
// value — see this file's 16-bit doc section on why offset count alone
// cannot mean "16-bit". Offsets falling outside the 512-slot universe (a
// malformed patch entry) are silently skipped, the same bounds tolerance
// fillEntry/fillChannel already apply. Returns the absolute channels it
// actually wrote, for ownership/contention bookkeeping.
func writeFuncValue(frame []byte, startAddr uint16, offsets []uint16, nbytes int, raw uint32) []int {
	written := make([]int, 0, len(offsets))
	setOne := func(offset uint16, v byte) {
		ch := int(startAddr) + int(offset) - 1
		if ch < 1 || ch > session.DMXUniverseSize {
			return
		}
		frame[ch-1] = v
		written = append(written, ch)
	}
	if len(offsets) == 0 {
		return written
	}
	if nbytes >= 2 && len(offsets) >= 2 {
		setOne(offsets[0], byte(raw>>8))
		setOne(offsets[1], byte(raw))
		return written
	}
	for _, off := range offsets {
		setOne(off, byte(raw))
	}
	return written
}

// recomputePatternLocked composes the current frame and pushes it
// immediately.
func (r *RigCheck) recomputePatternLocked(elapsed float64) {
	if !r.patternOutput {
		return
	}
	comp := r.composePatternLocked(elapsed)
	r.out.setFrames(comp.frames, false)
}

// armPatternTickLocked (re)schedules the next patternTick, ticking at the
// same interval the DMX engine itself retransmits at (r.dmx.Interval()) —
// see this file's doc comment on why that, and not some independently chosen
// rate, is the right tick source.
func (r *RigCheck) armPatternTickLocked() {
	if r.patternTimer != nil {
		r.patternTimer.Stop()
	}
	interval := r.dmx.Interval()
	if interval <= 0 {
		interval = time.Second / session.DefaultDMXRate
	}
	r.patternTimer = r.clock.AfterFunc(interval, r.patternTick)
}

// patternTick is the self-rescheduling chain's callback — see
// DMXOutputEngine.tick's doc comment (session/dmxout.go) for the identical
// pattern this mirrors. It is also where the client-liveness watchdog is
// enforced: see this file's doc comment.
func (r *RigCheck) patternTick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.patternOutput || !r.running {
		return // stopped between scheduling and firing — nothing to do, and nothing to reschedule
	}
	now := r.clock.Now()
	if now.Sub(r.lastTouch) > PatternWatchdogTimeout {
		r.stopLocked("watchdog") // blacks out; leaves the selection intact
		return
	}
	r.recomputePatternLocked(now.Sub(r.patternEpoch).Seconds())
	r.armPatternTickLocked()
}

// cancelPatternOutputLocked stops the pattern ticker (if any) and clears the
// OUTPUT flag, recording reason as LastEndReason. Called from stopLocked
// (rigcheck.go) so every path out of a running rig check — Stop, a classic
// Start superseding this one, or the watchdog — cancels the ticker the same
// way. It deliberately does NOT touch the selection: the owner's stop button
// stops output and deselects nothing.
func (r *RigCheck) cancelPatternOutputLocked(reason string) {
	if r.patternTimer != nil {
		r.patternTimer.Stop()
		r.patternTimer = nil
	}
	if r.patternOutput {
		r.lastPatternEnd = reason
		r.patternOutput = false
	}
}
