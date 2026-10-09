// This file is Console-lite chunk C4b: the programmer's TOOLS — Highlight /
// Lowlight, Locate, Fan, and the capture/recall halves of per-family
// presets (their storage is the show's workspace, internal/web). Owner
// decision 2026-10-07: "Definitely add the highlight/lowlight, locate, fan,
// and group storing as functions. Presets as well per attribute."
//
// HIGHLIGHT is an OVERLAY, not programmer values: while it is on, the
// selected targets' highlight values go out on session.SourceHighlight
// (above the programmer, below raw — see that constant) and nothing is
// stored; turning it off removes the overlay and the look underneath is
// back exactly. A channel's highlight value, in this order:
//
//  1. the profile's stated Highlight (GDTF ChannelFunction Highlight, or the
//     GDTF 1.0 <DMXChannel Highlight> — DIN SPEC 15800 Table 58);
//  2. a Dimmer: full;
//  3. a Shutter/Strobe: its open state — a channel set whose name reads as
//     open (Rig Check's shutter vocabulary, shutterOpenValue), else the
//     channel's stated default;
//  4. a colour channel: the set named "open", else one named "white";
//  5. otherwise the channel is not part of Highlight. A dimmer, shutter or
//     colour channel that rules 1-4 cannot resolve is REPORTED and left at
//     its current value — never given an invented one.
//
// VIRTUAL DIMMERS (G1, programmer_model.go): a selected virtual dimmer is
// highlighted by claiming it at full on the highlight source — with no
// colour composed that is white at full, otherwise the composed mix at
// full — and the additive channels it scales are left out of the
// highlight frames (their profile highlight, e.g. the Paladin's 65535,
// would otherwise replace the mix with white). Lowlight scales an
// unselected fixture's virtual dimmers by scaling the additive channels.
//
// LOWLIGHT, only while Highlight is on: every fixture in the selection that
// is not highlighted (I2d2, component-specs §15: "applies only to the
// non-highlighted members of selection" — owner 2026-10-09: dimming
// everything but the selection would be solo, a different feature) has
// each of its Dimmer-group channels scaled to
// LowlightPercent of what it would otherwise show (default 20%). The engine
// scales the live composition (session.DMXOutputEngine.SetLowlight), so a
// running Rig Check test is scaled as it moves; floor(v x % / 100) never
// raises a value, so a dark fixture stays dark. A fixture with no dimmer
// channel is reported, not dimmed some other way.
package patch

import (
	"fmt"
	"math"
	"strings"

	"benny512/internal/session"
)

// DefaultLowlightPercent is Lowlight's level until someone sets another
// (coordinator default for C4b; GDTF defines no lowlight value).
const DefaultLowlightPercent = 20

// colourOpenAcceptTokens are, in priority order, the channel-set name
// fragments that read as an open / white colour state.
var colourOpenAcceptTokens = []string{"open", "white"}

// locateOpenAcceptTokens are the fragments Locate accepts for a colour or
// beam channel whose default is unknown.
var locateOpenAcceptTokens = []string{"open", "home"}

// openRejectTokens rule a set out as a steady open state: a moving,
// random or effect state that merely contains "open" or "white".
var openRejectTokens = []string{"rotation", "spin", "random", "audio", "rainbow", "effect", "pulse", "strobe", "ramp", "shak"}

// namedSet is the first channel set (document order, accept tokens in
// priority order) whose name carries an accept token and no reject token.
func namedSet(p *ProgParameter, accept, reject []string) (ProgSet, bool) {
	for _, want := range accept {
		for _, f := range p.Functions {
			for _, s := range f.Sets {
				name := strings.ToLower(s.Name)
				if !strings.Contains(name, want) {
					continue
				}
				bad := false
				for _, r := range reject {
					if !strings.Contains(want, r) && strings.Contains(name, r) {
						bad = true
					}
				}
				if !bad {
					return s, true
				}
			}
		}
	}
	return ProgSet{}, false
}

// shutterOpen is Rig Check's shutter rule (shutterOpenValue) over every
// function's sets: a set reading as open, else the stated default.
func shutterOpen(p *ProgParameter) (uint32, string, bool) {
	for _, want := range shutterOpenAcceptTokens {
		for _, f := range p.Functions {
			for _, s := range f.Sets {
				name := strings.ToLower(s.Name)
				if strings.Contains(name, want) && !shutterNameRejected(name, want) {
					return setValue(s), "open set " + s.Name, true
				}
			}
		}
	}
	if p.HasDefault {
		return p.Default, "default", true
	}
	return 0, "", false
}

// highlightFor resolves one channel's highlight value. part is false when
// the channel is not part of Highlight at all.
func highlightFor(p *ProgParameter) (v uint32, how string, ok, part bool) {
	switch {
	case p.HasHighlight:
		return p.Highlight, "profile highlight", true, true
	case p.Group == GroupDimmer:
		return p.Max, "full", true, true
	case isShutterAttr(p.Attribute):
		v, how, ok := shutterOpen(p)
		return v, how, ok, true
	case p.Group == GroupColour:
		if s, ok := namedSet(p, colourOpenAcceptTokens, openRejectTokens); ok {
			return setValue(s), "open set " + s.Name, true, true
		}
		return 0, "", false, true
	}
	return 0, "", false, false
}

// ProgHighlightView is the Highlight/Lowlight state and what it covers.
type ProgHighlightView struct {
	On              bool `json:"on"`
	Lowlight        bool `json:"lowlight"`
	LowlightPercent int  `json:"lowlightPercent"`
	// Channels is how many selected channels Highlight drives.
	Channels int `json:"channels"`
	// Unresolved: selected dimmer/shutter/colour channels with no highlight
	// value; they keep their current value.
	Unresolved []ProgSkipped `json:"unresolved"`
	// Lowlit: fixtures whose dimmers Lowlight scales (when both are on).
	Lowlit int `json:"lowlit"`
	// NoDimmer: selected, not highlighted fixtures Lowlight cannot dim (no
	// dimmer channel).
	NoDimmer []string `json:"noDimmer"`
	// Step (I2d): the index in the selection that Highlight is stepped to,
	// or null when the whole selection is highlighted.
	Step *int `json:"step"`
}

// overlayLocked builds the highlight frames, the lowlight groups and the
// report for the current selection. It is computed whether or not
// Highlight is on, so the report can be read before switching it on.
func (pg *Programmer) overlayLocked() (map[uint16]session.LayerFrame, map[string]byte, map[uint16][]session.ScaleGroup, ProgHighlightView) {
	view := ProgHighlightView{On: pg.highlight, Lowlight: pg.lowlight, LowlightPercent: pg.lowPct,
		Unresolved: make([]ProgSkipped, 0), NoDimmer: make([]string, 0)}
	frames := map[uint16]session.LayerFrame{}
	levels := map[string]byte{}
	selected := map[string]bool{}
	done := map[progKey]bool{}
	// Stepping highlights one member; the rest of the selection is then
	// not highlighted, so Lowlight treats it like any other fixture.
	lit := pg.selection
	if pg.stepping && pg.step >= 0 && pg.step < len(pg.selection) {
		lit = pg.selection[pg.step : pg.step+1]
		step := pg.step
		view.Step = &step
	}
	for _, t := range lit {
		selected[t.EntryID] = true
		e, m := pg.entries[t.EntryID], pg.models[t.EntryID]
		if m == nil {
			continue
		}
		scaled := map[uint16]bool{}
		for _, p := range m.Parameters {
			for _, off := range p.virtualOf {
				scaled[off] = true
			}
		}
		for i := range m.Parameters {
			p := &m.Parameters[i]
			k := progKey{t.EntryID, p.Offset}
			if (t.Cell != "" && p.Cell != t.Cell) || done[k] || scaled[p.Offset] {
				continue
			}
			done[k] = true
			if p.Virtual {
				levels[VirtualDimmerKey(t.EntryID, p.Offset)] = 255
				view.Channels++
				continue
			}
			v, _, ok, part := highlightFor(p)
			if !part {
				continue
			}
			if !ok {
				view.Unresolved = append(view.Unresolved, ProgSkipped{EntryID: t.EntryID, Cell: p.Cell, Offset: p.Offset,
					Reason: fmt.Sprintf("%s has no highlight value in its profile and no channel set that reads as open, so Highlight leaves it as it is.", p.Attribute)})
				continue
			}
			view.Channels++
			for bi, b := range p.bytesOf(v) {
				off := p.Offsets[bi]
				if !e.addressable(off) {
					continue
				}
				f := frames[e.Universe]
				slot := int(e.StartAddress) + int(off) - 2
				f.Values[slot], f.Owned[slot] = b, true
				frames[e.Universe] = f
			}
		}
	}
	// Lowlight scope (I2d2): selected fixtures that are not highlighted —
	// only stepping leaves any. Fixtures outside the selection are never
	// dimmed.
	inSelection := map[string]bool{}
	for _, t := range pg.selection {
		inSelection[t.EntryID] = true
	}
	scale := map[uint16][]session.ScaleGroup{}
	for _, id := range pg.order {
		if selected[id] || !inSelection[id] {
			continue
		}
		e, m := pg.entries[id], pg.models[id]
		dims := 0
		for _, p := range m.Parameters {
			if p.Virtual {
				n := 0
				for _, off := range p.virtualOf {
					if g, ok := scaleGroupOf(e, m.paramAt(off)); ok {
						scale[e.Universe] = append(scale[e.Universe], g)
						n++
					}
				}
				if n > 0 {
					dims++
				}
				continue
			}
			if p.Group != GroupDimmer {
				continue
			}
			g := session.ScaleGroup{}
			for _, off := range p.Offsets {
				if e.addressable(off) {
					g.Slots = append(g.Slots, int(e.StartAddress)+int(off)-2)
				}
			}
			if len(g.Slots) == len(p.Offsets) {
				scale[e.Universe] = append(scale[e.Universe], g)
				dims++
			}
		}
		if dims == 0 {
			view.NoDimmer = append(view.NoDimmer, id)
		} else {
			view.Lowlit++
		}
	}
	if !pg.highlight || !pg.lowlight {
		view.Lowlit = 0
	}
	return frames, levels, scale, view
}

// applyOverlayLocked puts the overlay on (or takes it off) the engine.
func (pg *Programmer) applyOverlayLocked() {
	frames, levels, scale, _ := pg.overlayLocked()
	if !pg.highlight {
		frames, levels = nil, nil
	}
	if !pg.highlight || !pg.lowlight {
		scale = nil
	}
	_ = pg.dmx.ReplaceSource(session.SourceHighlight, frames)
	_ = pg.dmx.SetVirtualDimmerLevels(session.SourceHighlight, levels)
	_ = pg.dmx.SetLowlight(scale, pg.lowPct)
}

// Highlight steps (I2d, component-specs §15).
const (
	StepNext     = "next"
	StepPrevious = "previous"
	StepAll      = "all"
)

// SetHighlight changes Highlight, Lowlight and/or the Lowlight percentage
// (nil = unchanged), and steps Highlight through the selection (step "" =
// unchanged). Stepping is non-wrapping: Next on the last member and
// Previous on the first stay where they are. From the whole selection,
// Next starts at the first member and Previous at the last. Turning
// Highlight off, or any selection change, goes back to the whole selection.
func (pg *Programmer) SetHighlight(highlight, lowlight *bool, percent *int, step string, expected *uint64) (uint64, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkLocked(expected); err != nil {
		return 0, err
	}
	if highlight == nil && lowlight == nil && percent == nil && step == "" {
		return 0, reqErr("Say what to change: highlight, lowlight, lowlightPercent or step.")
	}
	if percent != nil && (*percent < 0 || *percent > 100) {
		return 0, reqErr("The lowlight level is a whole percentage from 0 to 100.")
	}
	switch step {
	case "", StepAll:
	case StepNext, StepPrevious:
		on := pg.highlight
		if highlight != nil {
			on = *highlight
		}
		if !on {
			return 0, reqErr("Turn Highlight on before stepping through the selection.")
		}
		if len(pg.selection) == 0 {
			return 0, reqErr("Nothing is selected, so there is nothing to step through.")
		}
	default:
		return 0, reqErr("The highlight step must be next, previous or all; %q is not one of them.", step)
	}
	if highlight != nil {
		pg.highlight = *highlight
		if !pg.highlight {
			pg.stepping = false
		}
	}
	last := len(pg.selection) - 1
	switch step {
	case StepAll:
		pg.stepping = false
	case StepNext:
		if !pg.stepping {
			pg.stepping, pg.step = true, 0
		} else if pg.step < last {
			pg.step++
		}
	case StepPrevious:
		if !pg.stepping {
			pg.stepping, pg.step = true, last
		} else if pg.step > 0 {
			pg.step--
		}
	}
	if lowlight != nil {
		pg.lowlight = *lowlight
	}
	if percent != nil {
		pg.lowPct = *percent
	}
	pg.applyOverlayLocked()
	pg.revision++
	return pg.revision, nil
}

// --- Locate ---------------------------------------------------------------------

// ProgLocateAttr is what Locate did with one attribute across the targets.
type ProgLocateAttr struct {
	Attribute string         `json:"attribute"`
	Group     AttributeGroup `json:"group"`
	Applied   int            `json:"applied"`
	// How lists, once each, the rules that produced the values.
	How        []string      `json:"how"`
	Unresolved []ProgSkipped `json:"unresolved"`
}

// ProgLocateResult is Locate's outcome.
type ProgLocateResult struct {
	Revision   uint64           `json:"revision"`
	Output     ProgOutput       `json:"output"`
	Applied    int              `json:"applied"`
	Attributes []ProgLocateAttr `json:"attributes"`
}

// locateFor resolves one channel's locate value. part is false when Locate
// leaves the channel alone (speeds, controls, effects rates, other).
//
//   - Dimmer group: full.
//   - Shutter/Strobe: open (see shutterOpen).
//   - Pan, Tilt: the stated default; when unknown, physical 0 (the centre of
//     a symmetric range) if the channel's own function has a physical range
//     containing 0, by the same interpolation as a physical set.
//   - Colour and the rest of Beam (gobo, prism, frost, iris, animation):
//     the stated default; when unknown, a set named open, else home.
//   - Focus, Shaper: the stated default.
func locateFor(p *ProgParameter) (v uint32, how string, ok, part bool) {
	switch {
	case p.Group == GroupDimmer:
		return p.Max, "full", true, true
	case isShutterAttr(p.Attribute):
		v, how, ok := shutterOpen(p)
		return v, how, ok, true
	case p.Attribute == "Pan" || p.Attribute == "Tilt":
		if p.HasDefault {
			return p.Default, "default", true, true
		}
		for _, f := range p.Functions {
			lo, hi := math.Min(f.PhysicalFrom, f.PhysicalTo), math.Max(f.PhysicalFrom, f.PhysicalTo)
			if f.Attribute == p.Attribute && f.HasPhysicalRange && lo <= 0 && hi >= 0 {
				frac := (0 - f.PhysicalFrom) / (f.PhysicalTo - f.PhysicalFrom)
				return f.DMXFrom + uint32(math.Round(frac*float64(f.DMXTo-f.DMXFrom))), "physical 0", true, true
			}
		}
		return 0, "", false, true
	case p.Group == GroupColour || p.Group == GroupBeam:
		if p.HasDefault {
			return p.Default, "default", true, true
		}
		if s, ok := namedSet(p, locateOpenAcceptTokens, openRejectTokens); ok {
			return setValue(s), "open set " + s.Name, true, true
		}
		return 0, "", false, true
	case p.Group == GroupFocus || p.Group == GroupShaper:
		if p.HasDefault {
			return p.Default, "default", true, true
		}
		return 0, "", false, true
	}
	return 0, "", false, false
}

// Locate writes locate values INTO the programmer (touched) for targets
// (nil = the selection), reporting per attribute what was resolved and what
// was left as it is. Mode masters are not moved: every value comes from the
// fixture's own resting / open states.
func (pg *Programmer) Locate(targets []ProgTarget, expected *uint64) (ProgLocateResult, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	res := ProgLocateResult{Attributes: make([]ProgLocateAttr, 0)}
	if err := pg.checkLocked(expected); err != nil {
		return res, err
	}
	if targets == nil {
		targets = pg.selection
	}
	if len(targets) == 0 {
		return res, reqErr("Nothing is selected. Select fixtures first.")
	}
	idx := map[string]int{}
	hows := map[string]map[string]bool{}
	pending := map[progKey]uint32{}
	for _, t := range targets {
		m := pg.models[t.EntryID]
		if m == nil {
			return res, reqErr("A target fixture is not in the patch; refresh the fixture list.")
		}
		for i := range m.Parameters {
			p := &m.Parameters[i]
			k := progKey{t.EntryID, p.Offset}
			if t.Cell != "" && p.Cell != t.Cell {
				continue
			}
			if _, seen := pending[k]; seen {
				continue
			}
			v, how, ok, part := locateFor(p)
			if !part {
				continue
			}
			ai, have := idx[p.Attribute]
			if !have {
				ai = len(res.Attributes)
				idx[p.Attribute] = ai
				hows[p.Attribute] = map[string]bool{}
				res.Attributes = append(res.Attributes, ProgLocateAttr{Attribute: p.Attribute, Group: p.Group, How: make([]string, 0), Unresolved: make([]ProgSkipped, 0)})
			}
			a := &res.Attributes[ai]
			if !ok {
				a.Unresolved = append(a.Unresolved, ProgSkipped{EntryID: t.EntryID, Cell: p.Cell, Offset: p.Offset,
					Reason: fmt.Sprintf("%s has no stated default or open state, so Locate left it as it is.", p.Attribute)})
				continue
			}
			pending[k] = v
			a.Applied++
			if !hows[p.Attribute][how] {
				hows[p.Attribute][how] = true
				a.How = append(a.How, how)
			}
		}
	}
	if len(pending) == 0 {
		return res, reqErr("Locate found nothing it could set on the selection.")
	}
	for k, v := range pending {
		pg.values[k] = v
	}
	res.Applied = len(pending)
	pg.applyLocked()
	pg.revision++
	res.Revision, res.Output = pg.revision, pg.outputLocked()
	return res, nil
}

// --- Fan --------------------------------------------------------------------------

// Fan shapes.
const (
	FanLinear  = "linear"
	FanReverse = "reverse"
	FanMirror  = "mirror"
	FanEdgesIn = "edges-in" // I2d: both ends = From, centre = To (§15)
)

// ProgValueSpec is one end of a fan, in any C4a value mode.
type ProgValueSpec struct {
	DMX      *float64
	Fraction *float64
	Physical *float64
	Set      *string
	Slot     *int
}

// ProgFanRequest spreads one attribute across the targets in order.
type ProgFanRequest struct {
	Targets       []ProgTarget
	Attribute     string
	Function      string
	FunctionName  string
	FunctionIndex *int
	Shape         string
	From, To      ProgValueSpec
}

func (r ProgFanRequest) end(v ProgValueSpec) ProgSetRequest {
	return ProgSetRequest{Attribute: r.Attribute, Function: r.Function, FunctionName: r.FunctionName, FunctionIndex: r.FunctionIndex,
		DMX: v.DMX, Fraction: v.Fraction, Physical: v.Physical, Set: v.Set, Slot: v.Slot}
}

// fanPosition is where position i of n sits between From (0) and To (1):
//
//	linear  t = i/(n-1)            first = From, last = To
//	reverse t = 1 - i/(n-1)        first = To,   last = From
//	mirror  t = |2i-(n-1)|/(n-1)   centre = From, both ends = To (centre out)
//	edges-in t = 1 - |2i-(n-1)|/(n-1)  both ends = From, centre = To
func fanPosition(shape string, i, n int) float64 {
	d := float64(n - 1)
	switch shape {
	case FanReverse:
		return 1 - float64(i)/d
	case FanMirror:
		return math.Abs(float64(2*i)-d) / d
	case FanEdgesIn:
		return 1 - math.Abs(float64(2*i)-d)/d
	}
	return float64(i) / d
}

// Fan spreads values across the targets IN TARGET ORDER. Each target that
// has the attribute is one position (a whole fixture moves its cells
// together; a cell selected as a cell is its own position). Each channel
// resolves From and To in its own terms (physical maps per fixture type),
// and takes
//
//	value = From + round(t x (To - From))
//
// rounding half away from zero (math.Round), at channel resolution, so a
// 16-bit channel fans in 16-bit steps. linear and reverse need at least 2
// positions, mirror at least 3 (it needs a centre and two ends). Mode
// masters are handled as for a set.
func (pg *Programmer) Fan(req ProgFanRequest, expected *uint64) (ProgSetResult, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	res := newSetResult()
	if err := pg.checkLocked(expected); err != nil {
		return res, err
	}
	from, to := req.end(req.From), req.end(req.To)
	for _, r := range []ProgSetRequest{from, to} {
		if err := r.validate(); err != nil {
			return res, err
		}
	}
	need := 2
	switch req.Shape {
	case FanLinear, FanReverse:
	case FanMirror, FanEdgesIn:
		need = 3
	default:
		return res, reqErr("The fan shape must be linear, reverse, mirror or edges-in; %q is not one of them.", req.Shape)
	}
	matches, err := pg.matchLocked(req.Targets, req.Attribute, &res)
	if err != nil {
		return res, err
	}
	if len(matches) < need {
		return res, reqErr("A %s fan needs at least %d selected fixtures or cells with %s; %d have it.", req.Shape, need, req.Attribute, len(matches))
	}
	pending := map[progKey]uint32{}
	for i, tm := range matches {
		t := fanPosition(req.Shape, i, len(matches))
		for _, p := range tm.params {
			a, fa, why := from.resolveValue(p)
			if why == "" {
				var b uint32
				b, _, why = to.resolveValue(p)
				if why == "" {
					v := uint32(int64(a) + int64(math.Round(t*(float64(b)-float64(a)))))
					fi := -1
					if from.namesFunction() {
						fi = fa
					}
					pg.placeLocked(&res, tm.target.EntryID, tm.model, p, v, fi, from.namesFunction(), pending)
					continue
				}
			}
			res.Skipped = append(res.Skipped, ProgSkipped{EntryID: tm.target.EntryID, Cell: p.Cell, Offset: p.Offset, Reason: why})
		}
	}
	return pg.commitLocked(res, pending)
}

// --- presets per attribute family ----------------------------------------------

// PresetValue is one stored channel of a preset: the fixture it came from
// (with its type and mode for the by-type rule), the channel, the value.
type PresetValue struct {
	EntryID     string `json:"entryId"`
	FixtureType string `json:"fixtureType"`
	Mode        string `json:"mode"`
	Instance    string `json:"instance"`
	Offset      uint16 `json:"offset"`
	Attribute   string `json:"attribute"`
	ByteCount   int    `json:"byteCount"`
	Value       uint32 `json:"value"`
}

// ValidGroup reports whether g is one of the attribute families.
func ValidGroup(g AttributeGroup) bool {
	for _, x := range AllGroups {
		if x == g {
			return true
		}
	}
	return false
}

// CapturePreset returns the programmer's TOUCHED values of the selection
// (a cell target: that cell's channels) in one attribute family, in
// selection order.
func (pg *Programmer) CapturePreset(family AttributeGroup) ([]PresetValue, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if !ValidGroup(family) {
		return nil, reqErr("%q is not an attribute family; use dimmer, position, colour, beam, focus, shaper or other.", family)
	}
	if len(pg.selection) == 0 {
		return nil, reqErr("Nothing is selected. Select the fixtures whose values the preset should hold.")
	}
	out := make([]PresetValue, 0)
	done := map[progKey]bool{}
	for _, t := range pg.selection {
		m := pg.models[t.EntryID]
		if m == nil {
			continue
		}
		for _, p := range m.Parameters {
			k := progKey{t.EntryID, p.Offset}
			v, touched := pg.values[k]
			if p.Group != family || !touched || done[k] || (t.Cell != "" && p.Cell != t.Cell) {
				continue
			}
			done[k] = true
			out = append(out, PresetValue{EntryID: t.EntryID, FixtureType: m.FixtureType, Mode: m.Mode, Instance: p.Instance,
				Offset: p.Offset, Attribute: p.Attribute, ByteCount: p.ByteCount, Value: v})
		}
	}
	if len(out) == 0 {
		return nil, reqErr("The selection holds no %s values in the programmer, so there is nothing to store.", family.String())
	}
	return out, nil
}

// ProgRecallTarget is what a recall gave one selected target.
type ProgRecallTarget struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
	// How: "exact" (its own stored values), "by-type" (a stored fixture of
	// the same fixture type and mode, From), "exact+by-type", or "nothing".
	How      string `json:"how"`
	From     string `json:"from"`
	Channels int    `json:"channels"`
	Reason   string `json:"reason"`
}

// ProgRecallResult is a recall's outcome.
type ProgRecallResult struct {
	Revision uint64             `json:"revision"`
	Output   ProgOutput         `json:"output"`
	Applied  int                `json:"applied"`
	Targets  []ProgRecallTarget `json:"targets"`
}

// ProgRecallNothing is returned when no selected target got a value.
type ProgRecallNothing struct{ Result ProgRecallResult }

func (e ProgRecallNothing) Error() string {
	return "Nothing was recalled: no selected fixture is in the preset or shares a fixture type and mode with one in it."
}

// RecallPreset writes a family preset INTO the programmer for the selection
// (touched). Per selected target, per channel of the family:
//
//  1. EXACT: the preset holds this fixture's own value for this channel
//     (same offset, attribute and byte count).
//  2. BY TYPE: otherwise, the value of the first fixture stored in the
//     preset with the same fixture type AND mode (non-empty) that holds this
//     channel — same offset, attribute and byte count, which within one
//     type+mode is the same channel (and the same cell instance).
//
// Targets that get nothing are reported. Mode masters are not moved: a
// stored family look is applied as stored.
func (pg *Programmer) RecallPreset(family AttributeGroup, values []PresetValue, expected *uint64) (ProgRecallResult, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	res := ProgRecallResult{Targets: make([]ProgRecallTarget, 0)}
	if err := pg.checkLocked(expected); err != nil {
		return res, err
	}
	if len(pg.selection) == 0 {
		return res, reqErr("Nothing is selected. Select the fixtures to recall the preset onto.")
	}
	pending, targets := pg.presetMatchLocked(family, values)
	res.Targets = targets
	if len(pending) == 0 {
		res.Revision, res.Output = pg.revision, pg.outputLocked()
		return res, ProgRecallNothing{Result: res}
	}
	for k, v := range pending {
		pg.values[k] = v
	}
	res.Applied = len(pending)
	pg.applyLocked()
	pg.revision++
	res.Revision, res.Output = pg.revision, pg.outputLocked()
	return res, nil
}

// PresetApplies (I2d, component-specs §14 "applies to 6 of 8 selected") is
// how many of the current selection's targets a recall of values would set
// at least one channel on — RecallPreset's own matching, nothing written.
func (pg *Programmer) PresetApplies(family AttributeGroup, values []PresetValue) int {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	_, targets := pg.presetMatchLocked(family, values)
	n := 0
	for _, t := range targets {
		if t.How != "nothing" {
			n++
		}
	}
	return n
}

// presetMatchLocked resolves a family preset onto the selection by the
// rules RecallPreset documents, without writing: the values per channel and
// the per-target report.
func (pg *Programmer) presetMatchLocked(family AttributeGroup, values []PresetValue) (map[progKey]uint32, []ProgRecallTarget) {
	targets := make([]ProgRecallTarget, 0, len(pg.selection))
	type chKey struct {
		entry     string
		offset    uint16
		attribute string
		bytes     int
	}
	exact := map[chKey]uint32{}
	donors := map[string][]string{} // type\x00mode -> entry ids, preset order
	seen := map[string]bool{}
	for _, v := range values {
		exact[chKey{v.EntryID, v.Offset, v.Attribute, v.ByteCount}] = v.Value
		if v.FixtureType != "" && !seen[v.EntryID] {
			seen[v.EntryID] = true
			tk := v.FixtureType + "\x00" + v.Mode
			donors[tk] = append(donors[tk], v.EntryID)
		}
	}
	pending := map[progKey]uint32{}
	for _, t := range pg.selection {
		m := pg.models[t.EntryID]
		if m == nil {
			continue
		}
		rt := ProgRecallTarget{EntryID: t.EntryID, Cell: t.Cell}
		var nExact, nType int
		for _, p := range m.Parameters {
			if p.Group != family || (t.Cell != "" && p.Cell != t.Cell) {
				continue
			}
			k := progKey{t.EntryID, p.Offset}
			if v, ok := exact[chKey{t.EntryID, p.Offset, p.Attribute, p.ByteCount}]; ok && v <= p.Max {
				pending[k] = v
				nExact++
				continue
			}
			if m.FixtureType == "" {
				continue
			}
			for _, d := range donors[m.FixtureType+"\x00"+m.Mode] {
				if v, ok := exact[chKey{d, p.Offset, p.Attribute, p.ByteCount}]; ok && v <= p.Max {
					pending[k] = v
					nType++
					if rt.From == "" {
						rt.From = d
					}
					break
				}
			}
		}
		rt.Channels = nExact + nType
		switch {
		case nExact > 0 && nType > 0:
			rt.How = "exact+by-type"
		case nExact > 0:
			rt.How = "exact"
		case nType > 0:
			rt.How = "by-type"
		default:
			rt.How = "nothing"
			rt.Reason = "The preset holds no values for this fixture, nor for a fixture of the same type and mode."
		}
		targets = append(targets, rt)
	}
	return pending, targets
}

// NoteWorkspace records a digest of the show's stored groups and presets;
// a change bumps the revision so every browser re-reads them. Reports
// whether it changed.
func (pg *Programmer) NoteWorkspace(digest string) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if digest == pg.wsDigest {
		return false
	}
	pg.wsDigest = digest
	pg.revision++
	return true
}
