// This file implements stage 2 of function-aware Rig Check: an
// attribute-level test-pattern engine that rides alongside (never replaces)
// rigcheck.go's existing channel-level walk on the same *RigCheck instance,
// sharing its mutex, its started-universe bookkeeping, and — critically —
// its blackout-on-stop safety discipline (rigcheck.go's file doc comment).
// internal/web/patch.go is the only HTTP-facing consumer; see its "test
// pattern" section for the exact request/response JSON.
//
// --- Tick source (design note the task brief asks for) ---------------------
//
// A time-varying pattern (a sine dimmer fade, a ballyhoo, colour-wheel
// stepping, a prism/animation spin) needs its own clock-driven recompute
// loop — recomputeLocked (rigcheck.go) only fires in response to an
// explicit mutator call (SetLevel, Next, ...), never on its own. This file
// adds a second, parallel self-rescheduling AfterFunc chain
// (armPatternTickLocked/patternTick), built the exact same way
// DMXOutputEngine's own tick()/scheduleLocked() chain is (session/
// dmxout.go) — and, crucially, driven by the SAME session.Clock the DMX
// engine itself uses (RigCheck.clock == dmx.Clock(), see NewRigCheck and
// DMXOutputEngine.Clock's doc comments). That is what makes a test's one
// FakeClock.Advance(...) call deterministically settle both the DMX
// engine's retransmit tick AND every running pattern's value recomputation
// in the same step, with no time.Sleep anywhere in this package's tests.
// The tick period is r.dmx.Interval() — whatever rate production/--demo
// configured the DMX engine at (~40 Hz by default) — so a pattern updates
// in lockstep with the engine that's actually putting bytes on the wire,
// not some independently-chosen rate that could drift from it.
//
// --- Client-disappears-mid-pattern (design note the task brief asks for) --
//
// A pattern, once started, needs no further HTTP request to keep moving —
// it is driven entirely by this package's own ticker. That is exactly the
// scenario the task brief's safety section calls out: a browser tab that
// crashes, or a laptop that loses network, leaves nothing to ever send the
// Stop a classic (static-level) rig check can safely rely a human for. The
// decision made here is a liveness watchdog (PatternWatchdogTimeout,
// rigcheck.go): every pattern-surface call that proves a client is still
// there — StartPattern, AdjustPattern, and a plain PatternStatus read —
// refreshes r.lastTouch, and patternTick blackout-and-stops the run the
// moment more than that window has elapsed since the last one. A
// "GET .../rigcheck/pattern" status read counts as a touch specifically so
// a UI that's merely polling to render live state (which any such UI must
// do anyway to show a moving sine/ballyhoo) keeps the pattern alive for
// free, with no separate heartbeat call needed — but it also means a
// caller that stops polling status (not just one that stops issuing
// commands) is exactly the "client disappeared" case this exists to catch.
// See internal/web/patch.go's endpoint doc comment for the poll-interval
// contract this implies.
//
// --- Safe mode / mixed rigs (owner decisions 1 and 2, task brief) --------
//
// Every frame this file builds starts from an all-zero buffer per
// recomputePatternLocked (same discipline recomputeLocked already uses) and
// only the DMX offset(s) backing the pattern's target attribute(s) are ever
// written a non-zero value — never a whole entry's footprint, never a
// "helpful" implicit dimmer-up or shutter-open so the tested function is
// visible. A fixture missing the requested attribute contributes nothing to
// the frame at all (decision 2: applied silently to fixtures that have the
// function, skipped silently otherwise) and is counted, not filtered out of
// PatternStatus — see PatternStatus.SkippedCount/Entries.
//
// --- 16-bit (coarse+fine) attributes --------------------------------------
//
// resolve.go's ResolvedFunction.Offsets doc comment already states the
// convention this file relies on: a 16-bit function's two offsets are
// (coarse, then fine), ascending. funcTarget below sorts Offsets
// defensively but does not otherwise second-guess that ordering — if GDTF
// (or the RDM-inferred bridge) ever gave a 16-bit function only ONE offset,
// this file treats it as a plain 8-bit function rather than inventing a
// fine byte with no textual basis for where it lives; there is no case
// where a wrong guess at a fine channel's address is preferable to just not
// having 16-bit precision.
//
// --- Missing GDTF ChannelSet detail (owner decision, task brief) ---------
//
// Where a pattern is inherently discrete (colour wheel stepping, prism
// in/out) it drives GDTF's own <ChannelSet> DMXFrom values when the
// underlying ChannelFunction has them, and degrades to a raw 0-255 sweep
// (colourWheelStepValue/prismInOutValue) — flagged via
// PatternEntryStatus.DetailMissing / PatternStatus.MissingDetailCount —
// when it doesn't, rather than inventing where slot boundaries might be.
// Prism/animation SPIN and shaper rotation are treated as pure continuous
// range controls regardless of ChannelSet presence: dividing one
// rotation-speed channel's 0-255 range into a CW half and a CCW half from
// ChannelSet names or PhysicalFrom signs would itself be exactly the kind
// of slot-boundary guess this rule forbids (a fixture's own convention for
// where "stop" sits is not standardized), so this file does not attempt it
// — DetailMissing is still reported for these (informational: "GDTF gave us
// no ChannelSet data for this channel at all"), but never changes what the
// sweep does.
package patch

import (
	"fmt"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// PatternKind identifies one attribute-level test pattern. Every value is a
// row in patternGroups below, which both validates it (StartPattern/
// AdjustPattern reject anything else) and says which taxonomy group it
// belongs to for reporting.
type PatternKind string

// Pattern kinds, grouped by the taxonomy group they drive. See each
// constant's inline comment for what PatternParams.Target/Direction/Value
// mean for it — an unused param field is simply ignored.
const (
	// --- Dimmer ---
	PatternDimmerSine   PatternKind = "dimmer_sine"   // continuous sine fade over [Min,Max] at RateHz
	PatternDimmerSnap   PatternKind = "dimmer_snap"   // square-wave snap between Min and Max at RateHz
	PatternDimmerToggle PatternKind = "dimmer_toggle" // static Max when Params.On, else Min — flipped via AdjustPattern, not time-driven

	// --- Position ---
	// PatternMoveExtreme is shared with Focus (see below); Target is
	// "<axis>_<extreme>": axis one of pan|tilt|focus|zoom, extreme one of
	// max|min|centre (centre valid for pan/tilt only — a fixture has no
	// meaningful "centre" focus/zoom value this package can derive without
	// a physical-range midpoint GDTF doesn't reliably give either).
	PatternMoveExtreme PatternKind = "move_extreme"
	// PatternBallyhoo drives Pan (phase 0) and Tilt (phase 1/4 cycle)
	// together as two sine waves at RateHz — a continuous
	// Lissajous-style sweep, the classic moving-head "ballyhoo" test.
	PatternBallyhoo PatternKind = "ballyhoo"

	// --- Colour ---
	// PatternColourWheelStep advances the ColorWheel attribute through its
	// GDTF ChannelSets, one slot per 1/RateHz seconds, looping.
	PatternColourWheelStep PatternKind = "colour_wheel_step"
	// PatternColourMixSweep drives every RGB (ColorAdd_R/G/B) or, absent
	// that, CMY (ColorSub_C/M/Y) channel with ONE shared sine wave in
	// unison — "mixing in and out" together, not a colour change.
	PatternColourMixSweep PatternKind = "colour_mix_sweep"
	// PatternColourFade drives the same RGB-or-CMY channel set with three
	// sine waves 1/3-cycle apart (a continuous hue rotation) — "rig-wide
	// colour fades": every fixture in scope cycles through colour
	// together, using the same phase relationship.
	PatternColourFade PatternKind = "colour_fade"

	// --- Beam ---
	// PatternFrost sine-sweeps Frost* over [Min,Max]. Target selects which
	// Frost function on a fixture with more than one: "" (all of them,
	// together), "light", or "heavy" — see selectFrostTarget's doc comment
	// for how that selection is made and why it is a judgment call, not a
	// GDTF-confirmed fact.
	PatternFrost PatternKind = "frost"
	// PatternPrismInOut square-toggles a non-rotation Prism* function
	// between its "out"/open ChannelSet and its "in"/inserted one when
	// GDTF gives ChannelSet data, else between raw Min and Max.
	PatternPrismInOut PatternKind = "prism_in_out"
	// PatternPrismSpin continuously ramps a Prism* rotation function
	// (attribute name containing "Rot") over [Min,Max]; Direction "cw"
	// (default) ramps up, "ccw" ramps down.
	PatternPrismSpin PatternKind = "prism_spin"
	// PatternAnimationSpin is PatternPrismSpin's sibling for an Animation*
	// wheel rotate/index attribute.
	PatternAnimationSpin PatternKind = "animation_spin"

	// --- Focus ---
	// PatternManualValue sets Focus or Zoom (Target) to a fixed Params.Value
	// (0-255, scaled proportionally into the fine byte for a 16-bit
	// function) — "a settable manual value for each", held until stopped
	// or re-adjusted, not time-driven.
	PatternManualValue PatternKind = "manual_value"

	// --- Shaper ---
	// PatternShaperIndividual time-slices RateHz seconds per shaper
	// insertion function (attribute prefix Blade/Shaper, name NOT
	// containing "Rot"), ramping each 0->Max in turn while every other one
	// sits at Min — "each shaper 0->100 individually".
	PatternShaperIndividual PatternKind = "shaper_individual"
	// PatternShaperAll sine-sweeps every shaper insertion function
	// together over [Min,Max] — "all shapers together".
	PatternShaperAll PatternKind = "shaper_all"
	// PatternShaperRotate triangle-sweeps (min -> max -> min, not a
	// continuous spin — bounded, per the task brief's own phrasing) every
	// shaper ROTATION function (name containing "Rot") together.
	PatternShaperRotate PatternKind = "shaper_rotate"
)

// patternGroups is both PatternKind's validity table (StartPattern/
// AdjustPattern reject any Kind not listed here) and its default taxonomy
// group. PatternMoveExtreme has no single fixed group (it drives Position
// for pan/tilt targets, Focus for focus/zoom ones) — its entry here is a
// placeholder never actually reported; groupForPatternSpec resolves it
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
	PatternPrismInOut:       GroupBeam,
	PatternPrismSpin:        GroupBeam,
	PatternAnimationSpin:    GroupBeam,
	PatternManualValue:      GroupFocus,
	PatternShaperIndividual: GroupShaper,
	PatternShaperAll:        GroupShaper,
	PatternShaperRotate:     GroupShaper,
}

// PatternParams parameterizes a running pattern. Every field is optional —
// RateHz<=0 falls back to a sensible per-kind default (defaultRateForKind),
// Min/Max default to the full 0-255 byte range (their zero values, {0,0},
// are NOT treated as "unset": Min==Max==0 is a legitimate (if useless) "stay
// dark" configuration a caller could deliberately choose, matching every
// other numeric field's zero-is-real-data convention in this codebase — a
// caller that wants the default full range must send Max:255 explicitly).
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
}

// PatternSpec is what StartPattern needs beyond the entry scope.
type PatternSpec struct {
	Kind   PatternKind
	Params PatternParams
}

// PatternEntryStatus is one scoped entry's outcome in the current pattern
// run — the per-fixture half of decision 2's "12 of 40 fixtures" mixed-rig
// counting, applied to patterns instead of ResolveEntryGroups.
type PatternEntryStatus struct {
	EntryID string
	// Applied is true iff this entry had at least one function the pattern
	// could drive. A false entry was silently skipped, never written to —
	// decision 2, "skip the rest silently, report counts".
	Applied bool
	// Inferred is true iff at least one offset this pattern drives on this
	// entry came from an RDM-inferred (not GDTF) ChannelFunction — only
	// meaningful when Applied. Decision 3's hard constraint: an
	// RDM-inferred channel is still usable by a pattern, but the caller
	// must always be able to tell it apart from a GDTF-authoritative one.
	Inferred bool
	// DetailMissing is true iff a discrete (slot-type) pattern had to
	// degrade to a raw range sweep on this entry for lack of GDTF
	// ChannelSet data — see this file's doc comment. Always false for a
	// pattern kind that isn't slot-driven, and false when Applied is false
	// (nothing to have missing detail about).
	DetailMissing bool
}

// PatternStatus is a full snapshot of the running pattern (or the most
// recent one, via LastEndReason, when none is running) — GET
// .../rigcheck/pattern's payload.
type PatternStatus struct {
	Running            bool
	Kind               PatternKind
	Params             PatternParams
	Group              AttributeGroup
	ElapsedMS          int64
	TotalScope         int
	AppliedCount       int
	SkippedCount       int
	InferredCount      int
	MissingDetailCount int
	// LastEndReason explains how the MOST RECENT pattern run (if any, ever)
	// ended: "" (none has ever run on this RigCheck instance), "manual"
	// (Stop, or Blackout — see Blackout's doc comment for why Blackout ends
	// a running pattern rather than merely dimming it), "restarted"
	// (superseded by a fresh Start or StartPattern call), or "watchdog"
	// (PatternWatchdogTimeout elapsed with no client touch — see this
	// file's doc comment). Sticky across Running becoming false so a UI
	// that polls in and finds nothing running can still tell "the user hit
	// Stop" apart from "the browser tab died and the watchdog caught it".
	LastEndReason string
	Entries       []PatternEntryStatus
}

// patternFuncTarget is one resolved DMX target (one attribute, on one
// entry) a pattern drives, precomputed once at StartPattern/AdjustPattern
// time (ChannelFunctions do not change while a pattern runs) so every tick
// only has to evaluate a waveform and write bytes, never re-walk the
// taxonomy.
type patternFuncTarget struct {
	// role tells the per-tick renderer (patternValueForFunc) which
	// physical concept this target is, when that's not fully determined by
	// the pattern Kind alone (a Kind can drive several roles — e.g.
	// PatternBallyhoo's "pan" and "tilt" need different sine phases).
	// "static": a fixed, precomputed value (staticValue/is16) — no
	// per-tick math at all.
	// "level": the generic single-channel [Min,Max] target most range
	// patterns use.
	// "wheel", "prism_inout": discrete, ChannelSet-driven.
	// "pan", "tilt": PatternBallyhoo's two phases.
	role        string
	offsets     []uint16 // 1-based-within-footprint, ascending: (coarse) or (coarse, fine) — resolve.go's ResolvedFunction.Offsets convention
	channelSets []ChannelSet
	inferred    bool
	index       int // this function's position among its sibling group (PatternColourFade's phase index, PatternShaperIndividual's dwell slot)
	total       int // sibling count for index above
	staticValue uint32
	is16        bool // only meaningful together with staticValue (role=="static")
}

// patternEntryTarget is one scoped Entry's resolved pattern targets.
type patternEntryTarget struct {
	entryID       string
	universe      uint16
	startAddr     uint16
	inferred      bool
	detailMissing bool
	functions     []patternFuncTarget
}

// patternRun is StartPattern's live state, held on RigCheck.pattern.
type patternRun struct {
	spec               PatternSpec
	startedAt          time.Time
	targets            []patternEntryTarget
	totalScope         int
	appliedCount       int
	inferredCount      int
	missingDetailCount int
}

// --- validation --------------------------------------------------------

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
		if t := spec.Params.Target; t != "" && t != "light" && t != "heavy" {
			return fmt.Errorf("patch: frost target must be \"\", \"light\" or \"heavy\", got %q", t)
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

// --- resolution: which offsets, on which entries, does this pattern drive --

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

func funcTarget(e Entry, rf ResolvedFunction, role string, idx, total int) patternFuncTarget {
	offs := append([]uint16(nil), rf.Offsets...)
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	var sets []ChannelSet
	if len(offs) > 0 {
		sets = e.ChannelFunctions[offs[0]].ChannelSets
	}
	return patternFuncTarget{
		role: role, offsets: offs, channelSets: sets,
		inferred: rf.Source == SourceRDMInferred, index: idx, total: total,
	}
}

// selectFrostTarget picks which Frost* function(s) PatternFrost's Target
// applies to. Neither real GDTF sample file this package's other provenance
// claims are checked against (taxonomy.go's file doc comment) distinguishes
// a "light" from a "heavy" frost by name, and RDM has no equivalent concept
// at all — so this is entirely a judgment call, not a confirmed fact: match
// a function whose attribute name literally says "light"/"heavy" first (a
// real GDTF export CAN name it that way), and only when that evidence is
// absent, fall back to the numerically/alphabetically first Frost function
// being "light" and the second being "heavy" — the common but unstandardized
// GDTF convention of Frost1 being the lighter effect. A caller that cares
// about getting this right on a specific fixture should pass Target:"" (all
// Frost functions together) rather than trust this guess.
func selectFrostTarget(fns []ResolvedFunction, target string) []ResolvedFunction {
	if target == "" || len(fns) <= 1 {
		return fns
	}
	sorted := append([]ResolvedFunction(nil), fns...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Attribute < sorted[j].Attribute })
	for _, f := range sorted {
		lower := strings.ToLower(f.Attribute)
		if target == "light" && strings.Contains(lower, "light") {
			return []ResolvedFunction{f}
		}
		if target == "heavy" && strings.Contains(lower, "heavy") {
			return []ResolvedFunction{f}
		}
	}
	if target == "light" {
		return sorted[:1]
	}
	return sorted[1:2]
}

// pickMixChannels resolves the Colour group's mixing attributes for
// PatternColourMixSweep/PatternColourFade: RGB (ColorAdd_R/G/B) if the
// fixture has all three, else CMY (ColorSub_C/M/Y) if it has those instead
// — "RGB or CMY, whichever the fixture has" (task brief). A fixture with
// neither complete set (e.g. only ColorAdd_R present) applies to nothing;
// this file does not partially drive one or two channels of an incomplete
// set, since a lone red channel with no green/blue sweeping alongside it
// would render as a red flash, not the "mixing in and out"/"colour fade"
// effect either pattern is meant to demonstrate.
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

// resolveEntry computes what (if anything) spec drives on e. ok is false
// when e has no matching function at all (decision 2: skip silently).
func resolveEntry(spec PatternSpec, e Entry) (target patternEntryTarget, ok bool) {
	target = patternEntryTarget{entryID: e.ID, universe: e.Universe, startAddr: e.StartAddress}
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
			ft.is16 = len(ft.offsets) >= 2
			full := uint32(255)
			if ft.is16 {
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
			ft.is16 = len(ft.offsets) >= 2
			ft.staticValue = uint32(spec.Params.Value)
			if ft.is16 {
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
		role := "level"
		for i, name := range names {
			for _, f := range filterAttr(functionsInGroup(groups, GroupColour), func(a string) bool { return a == name }) {
				if spec.Kind == PatternColourFade {
					add(funcTarget(e, f, "fade", i, len(names)))
				} else {
					add(funcTarget(e, f, role, i, len(names)))
				}
			}
		}

	case PatternFrost:
		fns := filterAttr(functionsInGroup(groups, GroupBeam), func(a string) bool { return strings.HasPrefix(a, "Frost") })
		for _, f := range selectFrostTarget(fns, spec.Params.Target) {
			add(funcTarget(e, f, "level", 0, 1))
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

func sineFrac(t, rateHz float64) float64 { return sineFracPhase(t, rateHz, 0) }

func sineFracPhase(t, rateHz, phase float64) float64 {
	return 0.5 * (1 - math.Cos(2*math.Pi*(rateHz*t+phase)))
}

func fracCycle(t, rateHz float64) float64 {
	x := math.Mod(rateHz*t, 1.0)
	if x < 0 {
		x += 1
	}
	return x
}

func squareHigh(t, rateHz float64) bool { return fracCycle(t, rateHz) < 0.5 }

func sawtoothFrac(t, rateHz float64) float64 { return fracCycle(t, rateHz) }

func triangleFrac(t, rateHz float64) float64 {
	x := fracCycle(t, rateHz)
	if x < 0.5 {
		return 2 * x
	}
	return 2 * (1 - x)
}

func scaleMinMax(min, max byte, is16 bool) (uint32, uint32) {
	if !is16 {
		return uint32(min), uint32(max)
	}
	return uint32(min) * 257, uint32(max) * 257
}

// colourWheelStepValue drives PatternColourWheelStep: steps through sets in
// order, one per 1/rateHz seconds, looping. Degrades to a raw 0-255 sawtooth
// sweep (never guessing where slots would be) when sets is empty.
func colourWheelStepValue(sets []ChannelSet, elapsed, rateHz float64) byte {
	if len(sets) == 0 {
		return byte(sawtoothFrac(elapsed, rateHz) * 255)
	}
	n := len(sets)
	idx := int(math.Mod(elapsed*rateHz, float64(n)))
	if idx < 0 {
		idx = 0
	}
	if sets[idx].DMXFrom > 255 {
		return 255
	}
	return byte(sets[idx].DMXFrom)
}

// prismInOutValue drives PatternPrismInOut: square-toggles between the
// first ChannelSet's DMXFrom ("out"/open — GDTF ChannelSets are
// conventionally authored in ascending open-to-closed DMX order) and the
// last one's ("in"/inserted) when at least two are known, else between raw
// minV/maxV.
func prismInOutValue(sets []ChannelSet, elapsed, rateHz float64, minV, maxV uint32) uint32 {
	if len(sets) >= 2 {
		if squareHigh(elapsed, rateHz) {
			return uint32(sets[len(sets)-1].DMXFrom)
		}
		return uint32(sets[0].DMXFrom)
	}
	if squareHigh(elapsed, rateHz) {
		return maxV
	}
	return minV
}

// patternValueForFunc computes ft's current raw DMX value (0-255 or
// 0-65535, per is16) at elapsed seconds into the run.
func patternValueForFunc(spec PatternSpec, ft patternFuncTarget, elapsed float64) (raw uint32, is16 bool) {
	is16 = len(ft.offsets) >= 2
	if ft.role == "static" {
		return ft.staticValue, ft.is16
	}

	minV, maxV := scaleMinMax(spec.Params.Min, spec.Params.Max, is16)
	if maxV < minV {
		minV, maxV = maxV, minV
	}
	span := float64(maxV - minV)
	rate := spec.Params.RateHz
	if rate <= 0 {
		rate = defaultRateForKind(spec.Kind)
	}
	ccw := strings.EqualFold(spec.Params.Direction, "ccw")

	switch ft.role {
	case "wheel":
		v := uint32(colourWheelStepValue(ft.channelSets, elapsed, rate))
		if is16 {
			return v * 257, is16
		}
		return v, is16
	case "prism_inout":
		return prismInOutValue(ft.channelSets, elapsed, rate, minV, maxV), is16
	case "pan":
		return minV + uint32(sineFrac(elapsed, rate)*span), is16
	case "tilt":
		return minV + uint32(sineFracPhase(elapsed, rate, 0.25)*span), is16
	case "fade":
		phase := 0.0
		if ft.total > 0 {
			phase = float64(ft.index) / float64(ft.total)
		}
		return minV + uint32(sineFracPhase(elapsed, rate, phase)*span), is16
	case "slice":
		total := ft.total
		if total <= 0 {
			total = 1
		}
		cyc := math.Mod(elapsed*rate, float64(total))
		active := int(cyc)
		frac := cyc - float64(active)
		if active != ft.index {
			return minV, is16
		}
		return minV + uint32(frac*span), is16
	}

	// role == "level": the specific waveform shape depends on Kind.
	switch spec.Kind {
	case PatternDimmerSnap:
		if squareHigh(elapsed, rate) {
			return maxV, is16
		}
		return minV, is16
	case PatternDimmerToggle:
		if spec.Params.On {
			return maxV, is16
		}
		return minV, is16
	case PatternPrismSpin, PatternAnimationSpin:
		x := sawtoothFrac(elapsed, rate)
		if ccw {
			x = 1 - x
		}
		return minV + uint32(x*span), is16
	case PatternShaperRotate:
		return minV + uint32(triangleFrac(elapsed, rate)*span), is16
	default: // PatternDimmerSine, PatternColourMixSweep, PatternFrost, PatternShaperAll
		return minV + uint32(sineFrac(elapsed, rate)*span), is16
	}
}

// writeFuncValue writes raw into frame at ft's absolute DMX offset(s),
// splitting a 16-bit value into (coarse=hi, fine=lo) across the two
// offsets when both are known. A function whose offsets fall outside the
// 512-slot universe (a malformed patch entry) is silently skipped, same
// bounds tolerance fillEntry/fillChannel already apply.
func writeFuncValue(frame []byte, startAddr uint16, ft patternFuncTarget, raw uint32, is16 bool) {
	if len(ft.offsets) == 0 {
		return
	}
	setOne := func(offset uint16, v byte) {
		ch := int(startAddr) + int(offset) - 1
		if ch < 1 || ch > session.DMXUniverseSize {
			return
		}
		frame[ch-1] = v
	}
	if is16 && len(ft.offsets) >= 2 {
		setOne(ft.offsets[0], byte(raw>>8))
		setOne(ft.offsets[1], byte(raw))
		return
	}
	setOne(ft.offsets[0], byte(raw))
}

// --- RigCheck integration ------------------------------------------------

// StartPattern begins a new attribute-level test-pattern run over entries
// (already scoped by the caller — see rigCheckScopeEntries in
// internal/web/patch.go). Like Start, it always stops whatever was running
// first (classic OR pattern) so a fresh run never inherits stale lit
// channels or a stale ticker.
func (r *RigCheck) StartPattern(entries []Entry, spec PatternSpec) (PatternStatus, error) {
	if len(entries) == 0 {
		return PatternStatus{}, ErrRigCheckEmptyScope
	}
	if err := validatePatternSpec(spec); err != nil {
		return PatternStatus{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked("restarted")

	r.entries = append([]Entry(nil), entries...)
	r.idx, r.chOff, r.mode, r.level = 0, 0, "", 0
	r.running = true

	for _, e := range r.entries {
		if r.started[e.Universe] {
			continue
		}
		pa, err := artnet.PortAddressFromRaw(e.Universe)
		if err != nil {
			continue
		}
		r.dmx.StartUniverse(pa, netip.AddrPort{}, session.DMXUniverseSize)
		r.started[e.Universe] = true
	}
	r.dmx.Start()

	r.pattern = buildPatternRun(spec, r.entries, r.clock.Now())
	r.lastPatternEnd = ""
	r.lastTouch = r.clock.Now()

	r.recomputePatternLocked(0)
	r.armPatternTickLocked()
	return r.patternStatusLocked(), nil
}

// AdjustPattern replaces the running pattern's Params wholesale (same
// whole-value-replace convention as SetLevel/SetMode) and re-resolves which
// offsets it drives — needed because Target can change WHICH attribute a
// pattern targets (move_extreme's axis, frost's light/heavy pick). Kind
// cannot be changed this way; call StartPattern again for that. Counts as a
// client-liveness touch (see PatternWatchdogTimeout).
func (r *RigCheck) AdjustPattern(params PatternParams) (PatternStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pattern == nil {
		return PatternStatus{}, ErrRigCheckNoPatternRunning
	}
	spec := r.pattern.spec
	spec.Params = params
	if err := validatePatternSpec(spec); err != nil {
		return PatternStatus{}, err
	}
	elapsed := r.clock.Now().Sub(r.pattern.startedAt).Seconds()
	r.pattern = buildPatternRun(spec, r.entries, r.pattern.startedAt) // preserve startedAt: adjusting params must not reset a continuous waveform's phase
	r.lastTouch = r.clock.Now()
	r.recomputePatternLocked(elapsed)
	return r.patternStatusLocked(), nil
}

// PatternStatus reports the current (or, via LastEndReason, most recent)
// pattern run. Every call is itself a client-liveness touch when a pattern
// is running — see this file's doc comment on why a status poll doubles as
// the watchdog heartbeat.
func (r *RigCheck) PatternStatus() PatternStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pattern != nil {
		r.lastTouch = r.clock.Now()
	}
	return r.patternStatusLocked()
}

func buildPatternRun(spec PatternSpec, entries []Entry, startedAt time.Time) *patternRun {
	// Resolve RateHz<=0 to its per-kind default HERE, once, rather than
	// leaving every tick's patternValueForFunc to do it: PatternStatus
	// echoes spec.Params verbatim, and a caller reading rateHz:0 back
	// after sending 0 (meaning "give me the default") would otherwise have
	// no way to learn what rate is actually in effect without duplicating
	// defaultRateForKind's table client-side.
	if spec.Params.RateHz <= 0 {
		spec.Params.RateHz = defaultRateForKind(spec.Kind)
	}
	targets := make([]patternEntryTarget, 0, len(entries))
	applied, inferred, missing := 0, 0, 0
	for _, e := range entries {
		t, ok := resolveEntry(spec, e)
		if !ok {
			continue
		}
		targets = append(targets, t)
		applied++
		if t.inferred {
			inferred++
		}
		if t.detailMissing {
			missing++
		}
	}
	return &patternRun{
		spec: spec, startedAt: startedAt, targets: targets, totalScope: len(entries),
		appliedCount: applied, inferredCount: inferred, missingDetailCount: missing,
	}
}

func (r *RigCheck) patternStatusLocked() PatternStatus {
	st := PatternStatus{LastEndReason: r.lastPatternEnd, Entries: make([]PatternEntryStatus, 0, len(r.entries))}
	if r.pattern == nil {
		return st
	}
	p := r.pattern
	st.Running = true
	st.Kind = p.spec.Kind
	st.Params = p.spec.Params
	st.Group = groupForPatternSpec(p.spec)
	st.ElapsedMS = r.clock.Now().Sub(p.startedAt).Milliseconds()
	st.TotalScope = p.totalScope
	st.AppliedCount = p.appliedCount
	st.SkippedCount = p.totalScope - p.appliedCount
	st.InferredCount = p.inferredCount
	st.MissingDetailCount = p.missingDetailCount

	byID := make(map[string]patternEntryTarget, len(p.targets))
	for _, t := range p.targets {
		byID[t.entryID] = t
	}
	for _, e := range r.entries {
		t, applied := byID[e.ID]
		st.Entries = append(st.Entries, PatternEntryStatus{
			EntryID: e.ID, Applied: applied, Inferred: applied && t.inferred, DetailMissing: applied && t.detailMissing,
		})
	}
	return st
}

// recomputePatternLocked rebuilds every touched universe's frame from
// scratch (always starting all-zero — decision 1's "every other channel...
// is never written", enforced the same way recomputeLocked already enforces
// it for the classic walk) and pushes it immediately.
func (r *RigCheck) recomputePatternLocked(elapsed float64) {
	if r.pattern == nil {
		return
	}
	frames := make(map[uint16][]byte, len(r.started))
	for u := range r.started {
		frames[u] = make([]byte, session.DMXUniverseSize)
	}
	for _, et := range r.pattern.targets {
		frame, ok := frames[et.universe]
		if !ok {
			continue
		}
		for _, ft := range et.functions {
			raw, is16 := patternValueForFunc(r.pattern.spec, ft, elapsed)
			writeFuncValue(frame, et.startAddr, ft, raw, is16)
		}
	}
	for u, data := range frames {
		if pa, err := artnet.PortAddressFromRaw(u); err == nil {
			_ = r.dmx.SetFrame(pa, data)
		}
	}
	r.dmx.SendNow()
}

// armPatternTickLocked (re)schedules the next patternTick, ticking at the
// same interval the DMX engine itself retransmits at (r.dmx.Interval()) —
// see this file's doc comment on why that, and not some independently
// chosen rate, is the right tick source.
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
	if r.pattern == nil || !r.running {
		return // stopped between scheduling and firing — nothing to do, and nothing to reschedule
	}
	now := r.clock.Now()
	if now.Sub(r.lastTouch) > PatternWatchdogTimeout {
		r.stopLocked("watchdog")
		return
	}
	elapsed := now.Sub(r.pattern.startedAt).Seconds()
	r.recomputePatternLocked(elapsed)
	r.armPatternTickLocked()
}

// cancelPatternLocked stops the pattern ticker (if any) and clears the
// running pattern, recording reason as LastEndReason. Called from
// stopLocked (rigcheck.go) so every path out of a running rig check — Stop,
// a fresh Start/StartPattern superseding this one, or the watchdog —
// cancels the ticker the same way; a no-op when no pattern is running.
func (r *RigCheck) cancelPatternLocked(reason string) {
	if r.patternTimer != nil {
		r.patternTimer.Stop()
		r.patternTimer = nil
	}
	if r.pattern != nil {
		r.lastPatternEnd = reason
		r.pattern = nil
	}
}
