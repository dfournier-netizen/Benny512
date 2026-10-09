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
	// VirtualDimmers (I2e) are the engine keys (VirtualDimmerKey) of the
	// I2d4 virtual dimmers inside this target's scope: a dimmer test drives
	// them as SourceTests levels, beside any real Dimmer channel it drives.
	// Master (I2e) are the offsets of a cell target's fixture master Dimmer:
	// the base state opens it, as it opens a whole fixture's dimmer, so the
	// cell can be seen. Both come from Programmer.TestDimmers.
	VirtualDimmers []string
	Master         []uint16
	// Whites (I2f) are the engine keys of the colour scopes in this
	// target that a dimmer test shows at white while their colour is unset
	// (claimed at 255: the G1 fill rule), beyond VirtualDimmers. From
	// Programmer.TestDimmers.
	Whites []string
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
	virtual := map[string][]string{}
	masters := map[string][]uint16{}
	whites := map[string][]string{}
	for _, t := range targets {
		entries = append(entries, t.Entry)
		if t.Offsets != nil {
			owned[t.Entry.ID] = append([]uint16(nil), t.Offsets...)
		}
		if len(t.VirtualDimmers) > 0 {
			virtual[t.Entry.ID] = append([]string(nil), t.VirtualDimmers...)
		}
		if len(t.Master) > 0 {
			masters[t.Entry.ID] = append([]uint16(nil), t.Master...)
		}
		if len(t.Whites) > 0 {
			whites[t.Entry.ID] = append([]string(nil), t.Whites...)
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
	r.selection.virtual, r.selection.masters, r.selection.whites = virtual, masters, whites
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
	r.selection.virtual, r.selection.masters, r.selection.whites = nil, nil, nil
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

// TestDimmers (I2e; owner 2026-10-09: Tests drive virtual dimmers) is how
// the Tests layer reaches target t's intensity beyond the real Dimmer
// channels it finds itself, by the same scope rule as group faders and the
// programmer (dimmersOf / inScope, I2d4):
//
//   - virtual: the engine keys of the virtual dimmers in t's scope. A whole
//     fixture with no master reaches its own and its cells' virtual
//     dimmers; a whole fixture WITH a master is tested through the master
//     (a real channel), so none; a cell, its own virtual dimmer.
//   - master: for a cell target of a fixture with a real master Dimmer, the
//     master's offsets (coarse first). The master is the cell's output on
//     top of its colour, so the base state opens it (owner, 2026-10-09).
//   - whites (I2f, owner 2026-10-09): every other colour scope in t — its
//     virtual dimmer key, or its ColourFillKey when it has a real dimmer —
//     which a dimmer test shows at white while its colour is unset.
//
// Unknown fixture or cell: nothing.
func (pg *Programmer) TestDimmers(t ProgTarget) (virtual []string, master []uint16, whites []string) {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	m := pg.models[t.EntryID]
	if m == nil || (t.Cell != "" && !m.HasCell(t.Cell)) {
		return nil, nil, nil
	}
	pulsed := map[string]bool{}
	for _, p := range dimmersOf(m, t) {
		if p.Virtual {
			k := VirtualDimmerKey(t.EntryID, p.Offset)
			virtual = append(virtual, k)
			pulsed[k] = true
		}
	}
	for _, cs := range colourScopesOf(m) {
		if t.Cell != "" && cs.cell != t.Cell {
			continue
		}
		k := ColourFillKey(t.EntryID, cs.cell)
		if cs.virtual != nil {
			k = VirtualDimmerKey(t.EntryID, cs.virtual.Offset)
		}
		if !pulsed[k] {
			whites = append(whites, k)
		}
	}
	if t.Cell != "" {
		for i := range m.Parameters {
			p := &m.Parameters[i]
			if p.Attribute == "Dimmer" && p.Cell == "" && !p.Virtual && len(p.Offsets) > 0 {
				master = append([]uint16(nil), p.Offsets...)
				break
			}
		}
	}
	return virtual, master, whites
}

// AvailableTestsForTargets is AvailableTests over targets (I2e): a target
// with a virtual dimmer is offered the dimmer tests even when it has no
// Dimmer channel.
func AvailableTestsForTargets(targets []TestTarget) []AvailableTest {
	entries := make([]Entry, 0, len(targets))
	virtual := map[string]bool{}
	for _, t := range targets {
		entries = append(entries, t.Entry)
		if len(t.VirtualDimmers) > 0 {
			virtual[t.Entry.ID] = true
		}
	}
	return availableTests(entries, virtual)
}
