package patch

import (
	"sort"
	"time"
)

// Console-lite chunk C5: the Rig Check engine as the TESTS layer of the
// Console. The web layer resolves a scope — the programmer selection, a
// stored group, a layout layer, or every fixture — into TestTargets and
// drives this engine with SetTests; a test sequence is nothing more than a
// series of SetTests calls, one per step.
//
// Three rules differ from Rig Check's own screen and are why this is its own
// entry point:
//
//  1. A target can be a CELL: only that cell's channels are resolved, tested
//     and claimed (CellTestTarget).
//  2. Phase spreads follow the targets' order (the programmer selection's
//     order), not universe/address order.
//  3. Ending output releases the tests layer, so the base defaults and the
//     programmer beneath show at once; Rig Check's screen blacks its claim
//     out first. Rig Check's Blackout keeps blacking out.
//
// A step change is one SetTests call: the whole claim is replaced in one
// engine operation (rigcheckout.go), never released and re-claimed.

// TestTarget is one fixture or cell a test acts on.
type TestTarget struct {
	Entry Entry
	// Offsets (1-based within the footprint) are the only channels the
	// target owns — a cell's. nil: the whole footprint.
	Offsets []uint16
}

// CellTestTarget narrows e to one cell whose channels are offsets: a copy of
// e whose channel map holds only those offsets, with an ID of its own
// (entryID + "#" + cell) so it is a distinct target everywhere a test
// reports or phases by entry. A cell is one phase position.
func CellTestTarget(e Entry, cell string, offsets []uint16) TestTarget {
	own := append([]uint16(nil), offsets...)
	sort.Slice(own, func(i, j int) bool { return own[i] < own[j] })
	ce := e
	ce.ID = CellTargetID(e.ID, cell)
	ce.PhaseWeight = 0
	ce.ChannelFunctions = make(map[uint16]ChannelFunction, len(own))
	for _, off := range own {
		if cf, ok := e.ChannelFunctions[off]; ok {
			ce.ChannelFunctions[off] = cf
		}
	}
	return TestTarget{Entry: ce, Offsets: own}
}

// CellTargetID is the target ID CellTestTarget gives a cell.
func CellTargetID(entryID, cell string) string { return entryID + "#" + cell }

// ValidatePatternSpec reports whether spec is a test this engine can run,
// after the same normalization SetTests applies.
func ValidatePatternSpec(spec PatternSpec) error {
	return validatePatternSpec(normalizePatternSpec(spec))
}

// SetTests replaces the whole tests selection with specs over targets, in
// one step, and (on a server whose output follows the selection) renders it
// at once: a selection with tests and targets is on the wire while the
// master Arm is armed; with no targets or no tests the tests layer is
// released. fade, when given, replaces the fade time first so the change
// itself fades by it. Isolate is always off: the Tests layer shows each
// tested fixture with its untested channels at their defaults.
func (r *RigCheck) SetTests(targets []TestTarget, specs []PatternSpec, fade *time.Duration) (PatternStatus, error) {
	normalized := make([]PatternSpec, 0, len(specs))
	for _, spec := range specs {
		spec = normalizePatternSpec(spec)
		if err := validatePatternSpec(spec); err != nil {
			return PatternStatus{}, err
		}
		normalized = append(normalized, spec)
	}
	if fade != nil && (*fade < 0 || *fade > MaxPatternFade) {
		return PatternStatus{}, errFadeRange
	}
	entries := make([]Entry, 0, len(targets))
	owned := map[string][]uint16{}
	for _, t := range targets {
		entries = append(entries, t.Entry)
		if t.Offsets != nil {
			owned[t.Entry.ID] = append([]uint16(nil), t.Offsets...)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running && !r.patternOutput {
		// A classic channel walk is superseded by the Tests layer.
		r.stopLocked("restarted")
	}
	r.testsLayer = true
	if fade != nil {
		r.patternFadeTime = *fade
	}
	if err := r.preparePatternScopeLocked(entries); err != nil {
		return PatternStatus{}, err
	}
	r.selection.scope = entries
	r.selection.owned, r.selection.scopeOrder, r.selection.isolate = owned, true, false
	r.selection.tests = make(map[TestID]PatternSpec, len(normalized))
	for _, spec := range normalized {
		r.selection.tests[spec.TestID()] = spec
	}
	r.selection.rebuild()
	r.afterSelectionChangeLocked()
	return r.patternStatusLocked(), nil
}

// ReleaseTests clears the tests selection and releases the tests layer
// without blacking it out: the sources beneath show at once.
func (r *RigCheck) ReleaseTests() PatternStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.testsLayer = true
	r.selection.scope = nil
	r.selection.owned, r.selection.scopeOrder, r.selection.isolate = nil, true, false
	r.selection.tests = map[TestID]PatternSpec{}
	r.selection.rebuild()
	r.stopLocked("manual")
	return r.patternStatusLocked()
}

// CellOffsets returns every offset (all bytes) of the parameters the
// programmer model assigns to cell on entryID — the channels a test
// addressed to that cell may drive. ok is false when the fixture or cell is
// unknown.
func (pg *Programmer) CellOffsets(entryID, cell string) ([]uint16, bool) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	m := pg.models[entryID]
	if m == nil || !m.HasCell(cell) {
		return nil, false
	}
	out := make([]uint16, 0)
	for _, p := range m.Parameters {
		if p.Cell == cell {
			out = append(out, p.Offsets...)
		}
	}
	return out, true
}
