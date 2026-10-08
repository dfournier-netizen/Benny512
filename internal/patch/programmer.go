// This file is the Console-lite PROGRAMMER (chunk C4a): one programmer per
// Benny512 station, shared by every connected browser (owner decision
// 2026-10-07), holding an ordered selection and the values the user has
// touched, and driving two sources of the unified output engine:
//
//   - session.SourceBase: every channel of every patched fixture at its
//     profile default (programmer_model.go's DEFAULT rule), unknown = 0.
//   - session.SourceProgrammer: exactly the channels the user has touched.
//
// Layering (orchestrator decision): base < tests < programmer < raw <
// identify, so a running Rig Check test shows on every channel the
// programmer has not touched, and a touched channel wins over it. Values
// stay until cleared (owner decision). Nothing here reaches the wire unless
// the engine is armed; while it holds the last look, edits are stored and
// reported as not live.
//
// Every mutation bumps Revision. A client may send the revision it last saw
// (expected); a mismatch is ErrProgrammerStale, so two browsers cannot
// silently overwrite each other's view.
package patch

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"benny512/internal/session"
)

// ErrProgrammerStale is returned when a client's expected revision is not
// the current one.
var ErrProgrammerStale = errors.New("The programmer changed in another browser since this one last looked; refresh and try again.")

// ProgrammerRequestError is a request the programmer refuses as asked (400).
type ProgrammerRequestError struct{ Msg string }

func (e ProgrammerRequestError) Error() string { return e.Msg }

func reqErr(format string, a ...any) error {
	return ProgrammerRequestError{Msg: fmt.Sprintf(format, a...)}
}

// ProgTarget is one selected fixture (Cell "") or one of its cells.
type ProgTarget struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
}

type progKey struct {
	entry  string
	offset uint16
}

// Programmer — see the file comment.
type Programmer struct {
	mu        sync.Mutex
	dmx       *session.DMXOutputEngine
	revision  uint64
	token     any
	synced    bool
	order     []string
	entries   map[string]Entry
	models    map[string]*FixtureModel
	selection []ProgTarget
	// values: touched channels at channel resolution, keyed by the
	// parameter's coarse offset, or by a raw offset for raw writes.
	values map[progKey]uint32
	base   map[uint16]session.LayerFrame
	digest string
	// C4b: the Highlight/Lowlight overlay (programmer_tools.go) and the
	// digest of the show's stored groups and presets (NoteWorkspace).
	highlight bool
	lowlight  bool
	lowPct    int
	wsDigest  string
}

// NewProgrammer builds an empty programmer over dmx. seed is the first
// revision (the caller seeds it from the clock so a browser that saw a
// revision before a restart cannot match a new one by accident).
func NewProgrammer(dmx *session.DMXOutputEngine, seed uint64) *Programmer {
	return &Programmer{dmx: dmx, revision: seed, entries: map[string]Entry{}, models: map[string]*FixtureModel{},
		values: map[progKey]uint32{}, base: map[uint16]session.LayerFrame{}, lowPct: DefaultLowlightPercent}
}

// Revision is the current revision.
func (pg *Programmer) Revision() uint64 {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.revision
}

func (pg *Programmer) checkLocked(expected *uint64) error {
	if expected != nil && *expected != pg.revision {
		return ErrProgrammerStale
	}
	return nil
}

// SyncedTo reports whether the programmer was last synced to token.
func (pg *Programmer) SyncedTo(token any) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.synced && pg.token == token
}

// Sync rebuilds the parameter models and the base state from entries (the
// active show; token identifies its version) and re-applies both sources.
// clear (a show switch or reset) empties the selection and every value.
// Otherwise targets whose entry or cell is gone are dropped, and values are
// dropped for every entry that is gone or whose profile changed, so no value
// is ever left on a channel that no longer belongs to that fixture; a
// re-addressed fixture keeps its values, which move with it. Reports whether
// the revision changed (anything the read models show changed).
func (pg *Programmer) Sync(token any, entries []Entry, clear bool) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if !clear && pg.synced && pg.token == token {
		return false
	}
	selBefore, valBefore := len(pg.selection), len(pg.values)
	models := make(map[string]*FixtureModel, len(entries))
	order := make([]string, 0, len(entries))
	byID := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if _, dup := models[e.ID]; dup {
			continue
		}
		m := BuildFixtureModel(e)
		models[e.ID] = &m
		order = append(order, e.ID)
		byID[e.ID] = e
	}
	if clear {
		pg.selection = nil
		pg.values = map[progKey]uint32{}
		if pg.highlight {
			pg.highlight = false
			valBefore = -1 // a show switch ends Highlight: that is a change
		}
	} else {
		kept := make([]ProgTarget, 0, len(pg.selection))
		for _, t := range pg.selection {
			if m := models[t.EntryID]; m != nil && (t.Cell == "" || m.HasCell(t.Cell)) {
				kept = append(kept, t)
			}
		}
		pg.selection = kept
		for k := range pg.values {
			m, old := models[k.entry], pg.models[k.entry]
			if m == nil || old == nil || m.fingerprint != old.fingerprint {
				delete(pg.values, k)
			}
		}
	}
	digest := modelsDigest(order, models)
	changed := digest != pg.digest || len(pg.selection) != selBefore || len(pg.values) != valBefore
	pg.models, pg.order, pg.entries, pg.digest = models, order, byID, digest
	pg.token, pg.synced = token, true
	pg.base = pg.baseFramesLocked()
	_ = pg.dmx.ReplaceSource(session.SourceBase, pg.base)
	pg.applyLocked()
	if changed {
		pg.revision++
	}
	return changed
}

// modelsDigest covers everything the read models show, so a show edit that
// changes none of it (a layout move, a note) does not make every browser
// re-read the programmer.
func modelsDigest(order []string, models map[string]*FixtureModel) string {
	var b strings.Builder
	for _, id := range order {
		m := models[id]
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\n", id, m.fingerprint, m.Name, m.FixtureType, m.FixtureNumber, m.Universe, m.StartAddress, strings.Join(m.Notes, "|"))
	}
	return b.String()
}

// baseFramesLocked: every addressable byte of every patched entry, in patch
// order (a later overlapping entry wins, as it would on a console).
func (pg *Programmer) baseFramesLocked() map[uint16]session.LayerFrame {
	frames := map[uint16]session.LayerFrame{}
	write := func(e Entry, off uint16, v byte) {
		if !e.addressable(off) {
			return
		}
		f := frames[e.Universe]
		slot := int(e.StartAddress) + int(off) - 2
		f.Values[slot], f.Owned[slot] = v, true
		frames[e.Universe] = f
	}
	for _, id := range pg.order {
		e, m := pg.entries[id], pg.models[id]
		for _, off := range m.RawOffsets {
			write(e, off, 0)
		}
		for _, p := range m.Parameters {
			b := p.bytesOf(p.Default) // unknown default: Default is 0
			for i, off := range p.Offsets {
				write(e, off, b[i])
			}
		}
	}
	return frames
}

// applyLocked writes the touched values as the programmer source.
func (pg *Programmer) applyLocked() {
	frames := map[uint16]session.LayerFrame{}
	keys := make([]progKey, 0, len(pg.values))
	for k := range pg.values {
		keys = append(keys, k)
	}
	pos := map[string]int{}
	for i, id := range pg.order {
		pos[id] = i
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].entry != keys[j].entry {
			return pos[keys[i].entry] < pos[keys[j].entry]
		}
		return keys[i].offset < keys[j].offset
	})
	for _, k := range keys {
		e, m := pg.entries[k.entry], pg.models[k.entry]
		if m == nil {
			continue
		}
		v := pg.values[k]
		offs, b := []uint16{k.offset}, []byte{byte(v)}
		if p := m.paramAt(k.offset); p != nil {
			offs, b = p.Offsets, p.bytesOf(v)
		}
		for i, off := range offs {
			if !e.addressable(off) {
				continue
			}
			f := frames[e.Universe]
			slot := int(e.StartAddress) + int(off) - 2
			f.Values[slot], f.Owned[slot] = b[i], true
			frames[e.Universe] = f
		}
	}
	_ = pg.dmx.ReplaceSource(session.SourceProgrammer, frames)
	pg.applyOverlayLocked()
}

func (m *FixtureModel) paramAt(offset uint16) *ProgParameter {
	for i := range m.Parameters {
		if m.Parameters[i].Offset == offset {
			return &m.Parameters[i]
		}
	}
	return nil
}

func (m *FixtureModel) isRawOffset(off uint16) bool {
	for _, o := range m.RawOffsets {
		if o == off {
			return true
		}
	}
	return false
}

// --- output state ---------------------------------------------------------------

// ProgOutput says whether programmer values are reaching the wire.
type ProgOutput struct {
	State string `json:"state"`
	// Live is true only while armed: then every stored value is on the wire
	// (unless a higher source — raw/Send or Identify — owns that slot).
	Live bool   `json:"live"`
	Note string `json:"note"`
}

func (pg *Programmer) outputLocked() ProgOutput {
	st := pg.dmx.State()
	out := ProgOutput{State: string(st)}
	switch st {
	case session.StateArmed:
		out.Live = true
	case session.StateHolding:
		out.Note = "Output is holding the last look because every browser went silent. Programmer changes are stored and reach the wire when someone presses Arm."
	default:
		out.Note = "Output is disarmed. Programmer changes are stored and reach the wire when someone presses Arm."
	}
	return out
}

// --- selection ----------------------------------------------------------------

// Selection actions.
const (
	SelectSet    = "set"
	SelectAdd    = "add"
	SelectRemove = "remove"
	SelectToggle = "toggle"
	SelectAll    = "all"
	SelectNone   = "none"
)

// Select changes the selection; order is kept (first selected first) for
// the fan in C4b. targets are validated against the current models: an
// unknown entry or cell refuses the whole request.
func (pg *Programmer) Select(action string, targets []ProgTarget, expected *uint64) ([]ProgTarget, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkLocked(expected); err != nil {
		return nil, err
	}
	for _, t := range targets {
		m := pg.models[t.EntryID]
		if m == nil {
			return nil, reqErr("A selected fixture is not in the patch; refresh the fixture list.")
		}
		if t.Cell != "" && !m.HasCell(t.Cell) {
			return nil, reqErr("%q is not a cell of %s.", t.Cell, fixtureLabel(m))
		}
	}
	if (action == SelectAll || action == SelectNone) && len(targets) > 0 {
		return nil, reqErr("Select all and select none take no targets.")
	}
	index := func(sel []ProgTarget, t ProgTarget) int {
		for i, s := range sel {
			if s == t {
				return i
			}
		}
		return -1
	}
	next := append(make([]ProgTarget, 0, len(pg.selection)+len(targets)), pg.selection...)
	switch action {
	case SelectSet:
		next = next[:0]
		for _, t := range targets {
			if index(next, t) < 0 {
				next = append(next, t)
			}
		}
	case SelectAdd:
		for _, t := range targets {
			if index(next, t) < 0 {
				next = append(next, t)
			}
		}
	case SelectRemove:
		for _, t := range targets {
			if i := index(next, t); i >= 0 {
				next = append(next[:i], next[i+1:]...)
			}
		}
	case SelectToggle:
		for _, t := range targets {
			if i := index(next, t); i >= 0 {
				next = append(next[:i], next[i+1:]...)
			} else {
				next = append(next, t)
			}
		}
	case SelectAll:
		next = next[:0]
		for _, id := range pg.order {
			next = append(next, ProgTarget{EntryID: id})
		}
	case SelectNone:
		next = next[:0]
	default:
		return nil, reqErr("The selection action must be set, add, remove, toggle, all or none; %q is not one of them.", action)
	}
	pg.selection = next
	// Highlight and Lowlight follow the selection (C4b).
	pg.applyOverlayLocked()
	pg.revision++
	return append([]ProgTarget(nil), next...), nil
}

// ValidTarget reports whether t names a patched fixture and, when it names
// a cell, one of that fixture's cells.
func (pg *Programmer) ValidTarget(t ProgTarget) bool {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	m := pg.models[t.EntryID]
	return m != nil && (t.Cell == "" || m.HasCell(t.Cell))
}

// CheckRevision refuses a stale expected revision without changing anything
// (for writes that store programmer state elsewhere: groups, presets).
func (pg *Programmer) CheckRevision(expected *uint64) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return pg.checkLocked(expected)
}

func fixtureLabel(m *FixtureModel) string {
	if m.Name != "" {
		return m.Name
	}
	return "this fixture"
}

// --- set -------------------------------------------------------------------------

// ProgSetRequest sets one attribute on the selection (or Targets). Exactly
// one of DMX, Fraction, Physical, Set, Slot is given; at most one of
// Function (the function's GDTF attribute), FunctionName, FunctionIndex.
type ProgSetRequest struct {
	Targets       []ProgTarget
	Attribute     string
	Function      string
	FunctionName  string
	FunctionIndex *int
	DMX           *float64
	Fraction      *float64
	Physical      *float64
	Set           *string
	Slot          *int
}

// ProgByte is one DMX byte a set wrote, by offset within the fixture.
type ProgByte struct {
	Offset uint16 `json:"offset"`
	Value  byte   `json:"value"`
}

// ProgApplied is one channel a set wrote.
type ProgApplied struct {
	EntryID   string `json:"entryId"`
	Cell      string `json:"cell"`
	Offset    uint16 `json:"offset"`
	Attribute string `json:"attribute"`
	// FunctionIndex is the function the value was placed in, -1 when the
	// request named none and the value lies in more than one function.
	FunctionIndex int        `json:"functionIndex"`
	FunctionName  string     `json:"functionName"`
	Value         uint32     `json:"value"`
	Bytes         []ProgByte `json:"bytes"`
}

// ProgModeMaster reports a mode master the set had to move (or could not).
type ProgModeMaster struct {
	EntryID         string `json:"entryId"`
	Offset          uint16 `json:"offset"`
	Function        string `json:"function"`
	ModeMaster      string `json:"modeMaster"`
	Resolved        bool   `json:"resolved"`
	MasterOffset    uint16 `json:"masterOffset"`
	MasterAttribute string `json:"masterAttribute"`
	// Rule: "channel-range" (master channel within ModeFrom..ModeTo) or
	// "master-function" (master channel inside the named master function's
	// DMX range). See modeMasterFor.
	Rule      string `json:"rule"`
	RangeFrom uint32 `json:"rangeFrom"`
	RangeTo   uint32 `json:"rangeTo"`
	// Previous is the master's value before, null when it was not known
	// (untouched, no default stated).
	Previous *uint32 `json:"previous"`
	Value    uint32  `json:"value"`
	Changed  bool    `json:"changed"`
	Note     string  `json:"note"`
}

// ProgSkipped is one target the set did not apply to, and why.
type ProgSkipped struct {
	EntryID string `json:"entryId"`
	Cell    string `json:"cell"`
	Offset  uint16 `json:"offset"`
	Reason  string `json:"reason"`
}

// ProgSetResult is a set's outcome.
type ProgSetResult struct {
	Revision    uint64           `json:"revision"`
	Output      ProgOutput       `json:"output"`
	Applied     []ProgApplied    `json:"applied"`
	ModeMasters []ProgModeMaster `json:"modeMasters"`
	Skipped     []ProgSkipped    `json:"skipped"`
}

// ProgNothingApplied is returned when no target could take the value; the
// result carries every reason.
type ProgNothingApplied struct{ Result ProgSetResult }

func (e ProgNothingApplied) Error() string {
	if len(e.Result.Skipped) == 0 {
		return "Nothing was set."
	}
	more := ""
	if n := len(e.Result.Skipped) - 1; n > 0 {
		more = fmt.Sprintf(" (and %d more)", n)
	}
	return "Nothing was set: " + e.Result.Skipped[0].Reason + more
}

func (r ProgSetRequest) validate() error {
	if strings.TrimSpace(r.Attribute) == "" {
		return reqErr("Name the attribute to set.")
	}
	modes := 0
	for _, given := range []bool{r.DMX != nil, r.Fraction != nil, r.Physical != nil, r.Set != nil, r.Slot != nil} {
		if given {
			modes++
		}
	}
	if modes != 1 {
		return reqErr("Give exactly one value: dmx, fraction, physical, set or slot.")
	}
	sel := 0
	for _, given := range []bool{r.Function != "", r.FunctionName != "", r.FunctionIndex != nil} {
		if given {
			sel++
		}
	}
	if sel > 1 {
		return reqErr("Name the function one way only: function, functionName or functionIndex.")
	}
	if r.Fraction != nil && (math.IsNaN(*r.Fraction) || *r.Fraction < 0 || *r.Fraction > 1) {
		return reqErr("A fraction must be between 0 and 1.")
	}
	if r.DMX != nil && (math.IsNaN(*r.DMX) || *r.DMX < 0 || *r.DMX != math.Trunc(*r.DMX) || *r.DMX > float64(math.MaxUint32)) {
		return reqErr("A DMX value must be a whole number of 0 or more.")
	}
	if r.Physical != nil && (math.IsNaN(*r.Physical) || math.IsInf(*r.Physical, 0)) {
		return reqErr("A physical value must be a finite number.")
	}
	if r.Slot != nil && *r.Slot < 1 {
		return reqErr("Wheel slots are numbered from 1.")
	}
	return nil
}

// namedFunction resolves the request's function selector on p: -1 with no
// error when none was given.
func (r ProgSetRequest) namedFunction(p *ProgParameter) (int, string) {
	switch {
	case r.FunctionIndex != nil:
		if *r.FunctionIndex < 0 || *r.FunctionIndex >= len(p.Functions) {
			return -1, fmt.Sprintf("%s has no function number %d.", p.Attribute, *r.FunctionIndex)
		}
		return *r.FunctionIndex, ""
	case r.FunctionName != "":
		found := -1
		for i, f := range p.Functions {
			if f.Name == r.FunctionName {
				if found >= 0 {
					return -1, fmt.Sprintf("%s has more than one function named %q; name it by number.", p.Attribute, r.FunctionName)
				}
				found = i
			}
		}
		if found < 0 {
			return -1, fmt.Sprintf("%s has no function named %q.", p.Attribute, r.FunctionName)
		}
		return found, ""
	case r.Function != "":
		for i, f := range p.Functions {
			if f.Attribute == r.Function {
				return i, ""
			}
		}
		return -1, fmt.Sprintf("%s has no %s function.", p.Attribute, r.Function)
	}
	return -1, ""
}

// resolveValue computes the channel-resolution value for p and the function
// it lies in (-1 = not determined), or a reason it cannot.
//
// Channel sets (setValue): DIN SPEC 15800 Table 61 states a set's DMXFrom
// (its end is derived) and its PhysicalFrom/PhysicalTo. When the set names
// ONE physical state (PhysicalFrom == PhysicalTo, e.g. BMFL "Gobo 3", 0 to
// 0), every DMX value in it is that state and the value is DMXFrom — the one
// the file names, as Rig Check's shutter-open rule uses. When the set spans
// physical values, its DMXFrom is the physical START, not the state the
// set is named for: BMFL "Deep red - Positioning" runs 4626-9508 with
// physical -0.5 to 0.5 (slot offset), so DMXFrom is the red filter half out
// of the beam. Then the value is the one mapping to the set's physical
// centre, i.e. the DMX midpoint under the linear DMX-to-physical relation
// of Table 60.
//
// Physical: linear interpolation of the function's PhysicalFrom..PhysicalTo
// onto its DMXFrom..DMXTo (Table 60), rounded to the nearest step, refused
// outside the range and when the range is empty — never clamped.
func (r ProgSetRequest) resolveValue(p *ProgParameter) (uint32, int, string) {
	fi, why := r.namedFunction(p)
	if why != "" {
		return 0, -1, why
	}
	span := func(lo, hi uint32, f float64) uint32 {
		return lo + uint32(math.Round(f*float64(hi-lo)))
	}
	switch {
	case r.DMX != nil:
		v := *r.DMX
		if v > float64(p.Max) {
			return 0, -1, fmt.Sprintf("%v is beyond this %d-byte channel's maximum of %d.", v, p.ByteCount, p.Max)
		}
		u := uint32(v)
		if fi >= 0 && (u < p.Functions[fi].DMXFrom || u > p.Functions[fi].DMXTo) {
			f := p.Functions[fi]
			return 0, -1, fmt.Sprintf("%d is outside the %s function (%d to %d).", u, f.Name, f.DMXFrom, f.DMXTo)
		}
		return u, fi, ""
	case r.Fraction != nil:
		if fi < 0 {
			return span(0, p.Max, *r.Fraction), -1, ""
		}
		f := p.Functions[fi]
		return span(f.DMXFrom, f.DMXTo, *r.Fraction), fi, ""
	case r.Physical != nil:
		if fi < 0 {
			for i, f := range p.Functions {
				if f.Attribute == p.Attribute {
					fi = i
					break
				}
			}
			if fi < 0 {
				return 0, -1, fmt.Sprintf("%s has no function of its own attribute to take a physical value; name the function.", p.Attribute)
			}
		}
		f := p.Functions[fi]
		if !f.HasPhysicalRange {
			return 0, -1, fmt.Sprintf("The %s function has no physical range, so a physical value cannot be placed in it.", f.Name)
		}
		lo, hi := math.Min(f.PhysicalFrom, f.PhysicalTo), math.Max(f.PhysicalFrom, f.PhysicalTo)
		x := *r.Physical
		if x < lo || x > hi {
			return 0, -1, fmt.Sprintf("%v is outside the %s function's physical range of %v to %v.", x, f.Name, f.PhysicalFrom, f.PhysicalTo)
		}
		return span(f.DMXFrom, f.DMXTo, (x-f.PhysicalFrom)/(f.PhysicalTo-f.PhysicalFrom)), fi, ""
	case r.Set != nil:
		for i, f := range p.Functions {
			if fi >= 0 && i != fi {
				continue
			}
			for _, s := range f.Sets {
				if s.Name == *r.Set {
					return setValue(s), i, ""
				}
			}
		}
		return 0, -1, fmt.Sprintf("%s has no channel set named %q.", p.Attribute, *r.Set)
	case r.Slot != nil:
		for i, f := range p.Functions {
			if (fi >= 0 && i != fi) || f.Wheel == "" {
				continue
			}
			for _, s := range f.Sets {
				if s.HasWheelSlot && s.WheelSlot == *r.Slot {
					return setValue(s), i, ""
				}
			}
		}
		return 0, -1, fmt.Sprintf("%s has no wheel slot %d.", p.Attribute, *r.Slot)
	}
	return 0, -1, "No value was given."
}

// setValue is the DMX value a channel set is selected at — see
// resolveValue's channel-set rule.
func setValue(s ProgSet) uint32 {
	if s.PhysicalFrom == s.PhysicalTo || s.DMXTo <= s.DMXFrom {
		return s.DMXFrom
	}
	return s.DMXFrom + (s.DMXTo-s.DMXFrom+1)/2
}

// activeFunction is the function a value lies in when the request named
// none: the one function containing it, or among several (mode-mastered
// alternatives share a DMX range) the first whose attribute is the
// channel's own. -1 when that does not single one out.
func activeFunction(p *ProgParameter, v uint32) int {
	cands := make([]int, 0, 2)
	for i, f := range p.Functions {
		if v >= f.DMXFrom && v <= f.DMXTo {
			cands = append(cands, i)
		}
	}
	if len(cands) == 1 {
		return cands[0]
	}
	for _, i := range cands {
		if p.Functions[i].Attribute == p.Attribute {
			return i
		}
	}
	return -1
}

// currentLocked is a channel's programmer value, else its default; ok false
// when neither is known.
func (pg *Programmer) currentLocked(entry string, p *ProgParameter, pending map[progKey]uint32) (uint32, bool) {
	k := progKey{entry, p.Offset}
	if v, ok := pending[k]; ok {
		return v, true
	}
	if v, ok := pg.values[k]; ok {
		return v, true
	}
	return p.Default, p.HasDefault
}

// modeMasterFor resolves fn's ModeMaster on m (DIN SPEC 15800 Table 60:
// "Link to DMX Channel or Channel Function"; ModeFrom/ModeTo "DMX start/end
// value"). The master channel is the parameter whose derived DMXChannel name
// matches the link's first part, preferring the dependent's own geometry
// instance (a referenced cell's master is in that cell).
//
//   - A DMXChannel link: the master must sit within ModeFrom..ModeTo, at the
//     master's resolution (what the parser stores).
//   - A ChannelFunction link ("Channel.Logical.Function"): the master must
//     sit inside that function's DMX range. The table does not say what
//     ModeFrom/ModeTo mean for a function master; read literally, Robe's
//     BMFL (ModeFrom = ModeTo = "0/1" on Gobo1Pos, master Gobo1's static
//     function 0-31) would allow gobo indexing only on the open gobo, which
//     is not how the fixture works. UNVERIFIED reading, reported as rule
//     "master-function" so it is never silent.
func modeMasterFor(m *FixtureModel, dep *ProgParameter, fn *ProgFunction) (master *ProgParameter, lo, hi uint32, rule, why string) {
	parts := strings.Split(fn.ModeMaster, ".")
	var cands []*ProgParameter
	for i := range m.Parameters {
		if m.Parameters[i].channelName == parts[0] {
			cands = append(cands, &m.Parameters[i])
		}
	}
	for _, c := range cands {
		if c.Instance == dep.Instance {
			master = c
		}
	}
	if master == nil && len(cands) == 1 {
		master = cands[0]
	}
	if master == nil {
		return nil, 0, 0, "", fmt.Sprintf("Its mode master %q is not a channel of this fixture's patched mode, so it was not set.", fn.ModeMaster)
	}
	switch {
	case len(parts) == 1:
		if !fn.HasMode {
			return nil, 0, 0, "", fmt.Sprintf("Its mode master %q has a range the profile could not read, so it was not set.", fn.ModeMaster)
		}
		if fn.ModeFrom < 0 || fn.ModeTo < fn.ModeFrom || uint32(fn.ModeTo) > master.Max {
			return nil, 0, 0, "", fmt.Sprintf("Its mode master %q has a range that does not fit the master channel, so it was not set.", fn.ModeMaster)
		}
		return master, uint32(fn.ModeFrom), uint32(fn.ModeTo), "channel-range", ""
	case len(parts) >= 3:
		name := strings.Join(parts[2:], ".")
		for _, mf := range master.Functions {
			if mf.LogicalAttribute == parts[1] && mf.Name == name {
				return master, mf.DMXFrom, mf.DMXTo, "master-function", ""
			}
		}
		return nil, 0, 0, "", fmt.Sprintf("Its mode master function %q is not in the master channel's profile, so it was not set.", fn.ModeMaster)
	}
	return nil, 0, 0, "", fmt.Sprintf("Its mode master link %q is not a channel or a channel function, so it was not set.", fn.ModeMaster)
}

// Set applies req. Targets lacking the attribute (or whose functions cannot
// take the value) are skipped with a reason; if nothing applies the whole
// request is refused (ProgNothingApplied) and nothing changes.
func (pg *Programmer) Set(req ProgSetRequest, expected *uint64) (ProgSetResult, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	res := newSetResult()
	if err := pg.checkLocked(expected); err != nil {
		return res, err
	}
	if err := req.validate(); err != nil {
		return res, err
	}
	matches, err := pg.matchLocked(req.Targets, req.Attribute, &res)
	if err != nil {
		return res, err
	}
	pending := map[progKey]uint32{}
	for _, tm := range matches {
		for _, p := range tm.params {
			v, fi, why := req.resolveValue(p)
			if why != "" {
				res.Skipped = append(res.Skipped, ProgSkipped{EntryID: tm.target.EntryID, Cell: p.Cell, Offset: p.Offset, Reason: why})
				continue
			}
			pg.placeLocked(&res, tm.target.EntryID, tm.model, p, v, fi, req.namesFunction(), pending)
		}
	}
	return pg.commitLocked(res, pending)
}

func newSetResult() ProgSetResult {
	return ProgSetResult{Applied: make([]ProgApplied, 0), ModeMasters: make([]ProgModeMaster, 0), Skipped: make([]ProgSkipped, 0)}
}

func (r ProgSetRequest) namesFunction() bool {
	return r.FunctionIndex != nil || r.FunctionName != "" || r.Function != ""
}

// targetMatch is one target and its channels carrying an attribute (a
// channel two targets share — a parent and its cell — belongs to the first).
type targetMatch struct {
	target ProgTarget
	model  *FixtureModel
	params []*ProgParameter
}

// matchLocked resolves targets (nil = the selection), in order, to their
// channels carrying attribute; targets without it are reported in res.
func (pg *Programmer) matchLocked(targets []ProgTarget, attribute string, res *ProgSetResult) ([]targetMatch, error) {
	if targets == nil {
		targets = pg.selection
	}
	if len(targets) == 0 {
		return nil, reqErr("Nothing is selected. Select fixtures first.")
	}
	done := map[progKey]bool{}
	out := make([]targetMatch, 0, len(targets))
	for _, t := range targets {
		m := pg.models[t.EntryID]
		if m == nil {
			return nil, reqErr("A target fixture is not in the patch; refresh the fixture list.")
		}
		if t.Cell != "" && !m.HasCell(t.Cell) {
			return nil, reqErr("%q is not a cell of %s.", t.Cell, fixtureLabel(m))
		}
		tm := targetMatch{target: t, model: m}
		matched := false
		for i := range m.Parameters {
			p := &m.Parameters[i]
			if p.Attribute != attribute || (t.Cell != "" && p.Cell != t.Cell) {
				continue
			}
			matched = true
			k := progKey{t.EntryID, p.Offset}
			if done[k] {
				continue
			}
			done[k] = true
			tm.params = append(tm.params, p)
		}
		if !matched {
			what := fixtureLabel(m)
			if t.Cell != "" {
				what = fmt.Sprintf("Cell %s of %s", geometryName(t.Cell), what)
			}
			res.Skipped = append(res.Skipped, ProgSkipped{EntryID: t.EntryID, Cell: t.Cell, Reason: fmt.Sprintf("%s has no %s.", what, attribute)})
			continue
		}
		if len(tm.params) > 0 {
			out = append(out, tm)
		}
	}
	return out, nil
}

// placeLocked stages value v on p (into pending), resolves the function it
// lies in when the request named none, enforces that function's mode
// master, and records it in res.
func (pg *Programmer) placeLocked(res *ProgSetResult, entry string, m *FixtureModel, p *ProgParameter, v uint32, fi int, named bool, pending map[progKey]uint32) {
	if fi < 0 && !named {
		fi = activeFunction(p, v)
	}
	pending[progKey{entry, p.Offset}] = v
	a := ProgApplied{EntryID: entry, Cell: p.Cell, Offset: p.Offset, Attribute: p.Attribute, FunctionIndex: fi, Value: v, Bytes: make([]ProgByte, 0, p.ByteCount)}
	for bi, b := range p.bytesOf(v) {
		a.Bytes = append(a.Bytes, ProgByte{Offset: p.Offsets[bi], Value: b})
	}
	if fi >= 0 {
		fn := &p.Functions[fi]
		a.FunctionName = fn.Name
		if fn.ModeMaster != "" {
			res.ModeMasters = append(res.ModeMasters, pg.enforceMasterLocked(entry, m, p, fn, pending)...)
		}
	}
	res.Applied = append(res.Applied, a)
}

// commitLocked stores pending, or refuses when nothing applied.
func (pg *Programmer) commitLocked(res ProgSetResult, pending map[progKey]uint32) (ProgSetResult, error) {
	if len(res.Applied) == 0 {
		res.Revision, res.Output = pg.revision, pg.outputLocked()
		return res, ProgNothingApplied{Result: res}
	}
	for k, v := range pending {
		pg.values[k] = v
	}
	pg.applyLocked()
	pg.revision++
	res.Revision, res.Output = pg.revision, pg.outputLocked()
	return res, nil
}

// enforceMasterLocked moves fn's mode master into range when it is not
// already there, into pending, and reports it. Never silent: an unresolved
// master is reported too.
func (pg *Programmer) enforceMasterLocked(entry string, m *FixtureModel, p *ProgParameter, fn *ProgFunction, pending map[progKey]uint32) []ProgModeMaster {
	rep := ProgModeMaster{EntryID: entry, Offset: p.Offset, Function: fn.Name, ModeMaster: fn.ModeMaster}
	master, lo, hi, rule, why := modeMasterFor(m, p, fn)
	if master == nil {
		rep.Note = why
		return []ProgModeMaster{rep}
	}
	rep.Resolved, rep.MasterOffset, rep.MasterAttribute, rep.Rule, rep.RangeFrom, rep.RangeTo = true, master.Offset, master.Attribute, rule, lo, hi
	cur, known := pg.currentLocked(entry, master, pending)
	if known && cur >= lo && cur <= hi {
		return nil
	}
	next := lo
	if known {
		c := cur
		rep.Previous = &c
		if cur > hi {
			next = hi
		}
		rep.Note = fmt.Sprintf("%s needs %s between %d and %d; it was %d and is now %d.", fn.Name, master.Attribute, lo, hi, cur, next)
		// A wheel master keeps its slot where the allowed range offers the
		// same slot (BMFL: a shaking Gobo 3 becomes the static Gobo 3 for
		// indexing, not whichever gobo sits at the range's edge).
		if slot, ok := wheelSlotAt(master, cur); ok {
			if v, ok := slotValueIn(master, slot, lo, hi); ok {
				next = v
				rep.Note = fmt.Sprintf("%s needs %s between %d and %d; it was %d (wheel slot %d) and is now %d, the same slot.", fn.Name, master.Attribute, lo, hi, cur, slot, next)
			}
		}
	} else {
		rep.Note = fmt.Sprintf("%s needs %s between %d and %d; its value was not known, so it is now %d.", fn.Name, master.Attribute, lo, hi, next)
	}
	rep.Value, rep.Changed = next, true
	pending[progKey{entry, master.Offset}] = next
	return []ProgModeMaster{rep}
}

// wheelSlotAt is the wheel slot of the channel set v lies in, if any.
func wheelSlotAt(p *ProgParameter, v uint32) (int, bool) {
	for _, f := range p.Functions {
		if f.Wheel == "" || v < f.DMXFrom || v > f.DMXTo {
			continue
		}
		for _, s := range f.Sets {
			if s.HasWheelSlot && v >= s.DMXFrom && v <= s.DMXTo {
				return s.WheelSlot, true
			}
		}
	}
	return 0, false
}

// slotValueIn is the value selecting wheel slot slot inside lo..hi, if a
// set offers one.
func slotValueIn(p *ProgParameter, slot int, lo, hi uint32) (uint32, bool) {
	for _, f := range p.Functions {
		if f.Wheel == "" {
			continue
		}
		for _, s := range f.Sets {
			if v := setValue(s); s.HasWheelSlot && s.WheelSlot == slot && v >= lo && v <= hi {
				return v, true
			}
		}
	}
	return 0, false
}

// --- raw ---------------------------------------------------------------------------

// ProgRawWrite is one raw 8-bit write (Value) or release (Value nil) on an
// offset no parameter covers.
type ProgRawWrite struct {
	Offset uint16
	Value  *int
}

// Raw writes raw DMX to offsets no parameter covers: an unprofiled
// fixture's channels, or the uncovered offsets of a profiled one. A covered
// offset is refused — it is set by attribute, at its channel's resolution.
func (pg *Programmer) Raw(entryID string, writes []ProgRawWrite, expected *uint64) (uint64, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkLocked(expected); err != nil {
		return 0, err
	}
	m := pg.models[entryID]
	if m == nil {
		return 0, reqErr("That fixture is not in the patch; refresh the fixture list.")
	}
	if len(writes) == 0 {
		return 0, reqErr("Give at least one offset to write.")
	}
	for _, w := range writes {
		if !m.isRawOffset(w.Offset) {
			if m.paramAt(w.Offset) != nil || m.coveredByParam(w.Offset) {
				return 0, reqErr("Offset %d of %s belongs to a profiled channel; set it by attribute instead.", w.Offset, fixtureLabel(m))
			}
			return 0, reqErr("Offset %d is outside %s's footprint of %d channels.", w.Offset, fixtureLabel(m), m.Footprint)
		}
		if w.Value != nil && (*w.Value < 0 || *w.Value > 255) {
			return 0, reqErr("A raw DMX value must be between 0 and 255.")
		}
	}
	for _, w := range writes {
		k := progKey{entryID, w.Offset}
		if w.Value == nil {
			delete(pg.values, k)
		} else {
			pg.values[k] = uint32(*w.Value)
		}
	}
	pg.applyLocked()
	pg.revision++
	return pg.revision, nil
}

func (m *FixtureModel) coveredByParam(off uint16) bool {
	for _, p := range m.Parameters {
		for _, o := range p.Offsets {
			if o == off {
				return true
			}
		}
	}
	return false
}

// --- clear -------------------------------------------------------------------------

// Clear scopes.
const (
	ClearAll       = "all"
	ClearSelection = "selection"
)

// Clear releases values back to base/tests. scope "all" without a group
// empties the whole programmer — values AND selection, as a console's Clear
// All does; with a group, that group's values on every fixture. scope
// "selection" releases the selected targets' values (a cell target: that
// cell's channels only), optionally only one attribute group; raw offsets
// have no group and are released only without one. Returns how many
// channels were released.
func (pg *Programmer) Clear(scope string, group AttributeGroup, expected *uint64) (int, uint64, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if err := pg.checkLocked(expected); err != nil {
		return 0, 0, err
	}
	if group != "" {
		ok := false
		for _, g := range AllGroups {
			ok = ok || g == group
		}
		if !ok {
			return 0, 0, reqErr("%q is not an attribute group; use dimmer, position, colour, beam, focus, shaper or other.", group)
		}
	}
	match := func(k progKey) bool {
		m := pg.models[k.entry]
		if m == nil {
			return true
		}
		p := m.paramAt(k.offset)
		if group != "" && (p == nil || p.Group != group) {
			return false
		}
		if scope == ClearAll {
			return true
		}
		for _, t := range pg.selection {
			if t.EntryID != k.entry {
				continue
			}
			if t.Cell == "" || (p != nil && p.Cell == t.Cell) {
				return true
			}
		}
		return false
	}
	switch scope {
	case ClearAll, ClearSelection:
	default:
		return 0, 0, reqErr("The clear scope must be all or selection; %q is not one of them.", scope)
	}
	n := 0
	for k := range pg.values {
		if match(k) {
			delete(pg.values, k)
			n++
		}
	}
	if scope == ClearAll && group == "" {
		pg.selection = nil
	}
	pg.applyLocked()
	pg.revision++
	return n, pg.revision, nil
}
