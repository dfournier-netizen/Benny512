package web

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Console-lite I2e — Tests drive VIRTUAL dimmers (owner, 2026-10-09). A
// dimmer test reaches a fixture or cell with colour mixing and no dimmer
// channel of its own through its I2d4 virtual dimmer (the engine's
// SourceTests level for that key; no second mechanism), by the same scope
// rule as group faders: a whole fixture through its master when it has
// one, a selected cell through its own virtual dimmer. Proven at the HTTP
// surface and read back as ArtDmx: the 4-cell RGB batten with a master
// (BT, universe 2: slot 1 master Dimmer, "Pixel n" RGB at 2-4 ... 11-13),
// and an RGB-only fixture with no dimmer and no cells (RG, universe 2,
// R G B at 201-203).

func newI2eTestsRig(t *testing.T) *c4aRig {
	t.Helper()
	r := newI2d4Rig(t)
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "channelSets": []any{}}
	}
	rg := map[string]any{"name": "RG", "fixtureType": "Spec-derived RGB (no dimmer)", "footprint": 3, "universe": 2, "startAddress": 201,
		"channelFunctions": map[string]any{"1": ch("ColorAdd_R"), "2": ch("ColorAdd_G"), "3": ch("ColorAdd_B")}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{rg}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import RG: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	if r.ids["RG"] == "" || r.ids["BT"] == "" {
		t.Fatalf("rig not patched: %v", r.ids)
	}
	return r
}

func availableCount(v c5Tests, kind string) int {
	for _, a := range v.Available {
		if a.Kind == kind {
			return a.FixtureCount
		}
	}
	return 0
}

// TestDimmerTestsDriveCellVirtualDimmers: the batten's four cells selected,
// a dimmer test at 128 reaches each cell's virtual dimmer. No colour set
// means white (the G1 fill rule), so each cell goes out at 128/128/128; a
// cell with red set scales its red; a cell whose virtual dimmer the
// programmer holds keeps the programmer's level (manual wins). The master
// is a real channel shared by every cell: the tests' base state opens it
// (as it opens a whole fixture's dimmer) so the cells can be seen.
func TestDimmerTestsDriveCellVirtualDimmers(t *testing.T) {
	r := newI2eTestsRig(t)
	bt := r.ids["BT"]
	cell := func(n int) map[string]any { return cellTarget(bt, fmt.Sprintf("Pixel %d:0", n)) }
	r.arm(t)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cell(1), cell(2), cell(3), cell(4)}}, nil)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})

	if n := availableCount(v, "dimmer_toggle"); n != 4 {
		t.Errorf("dimmer_toggle offered over %d of the 4 selected cells, want 4 (each has a virtual dimmer)", n)
	}
	if len(v.Tests) != 1 || len(v.Tests[0].Entries) != 4 {
		t.Fatalf("tests = %+v, want one test over the 4 cells", v.Tests)
	}
	for _, e := range v.Tests[0].Entries {
		if !e.Applied {
			t.Errorf("cell %q: the dimmer test is not applied (it should reach the cell's virtual dimmer)", e.Cell)
		}
		if !e.Virtual {
			t.Errorf("cell %q: the status does not say the test reaches it through its virtual dimmer", e.Cell)
		}
	}
	f := r.wire(t, 2)
	wantSlots(t, "master opened by the tests' base state", f, 1, 255)
	for c := 0; c < 4; c++ {
		wantSlots(t, fmt.Sprintf("cell %d: no colour set = white at the test's 128", c+1), f, 2+3*c, 128, 128, 128)
	}

	// A colour on cell 2: the test scales it (red 200 x 128/255 = 100).
	r.set(t, map[string]any{"targets": []any{cell(2)}, "attribute": "ColorAdd_R", "dmx": 200})
	f = r.wire(t, 2)
	wantSlots(t, "cell 2 red scaled by the test", f, 5, 100, 0, 0)
	// Manual wins: the programmer's own level for cell 3's virtual dimmer.
	r.set(t, map[string]any{"targets": []any{cell(3)}, "attribute": "Dimmer", "dmx": 50})
	f = r.wire(t, 2)
	wantSlots(t, "cell 3 at the programmer's 50, not the test's 128", f, 8, 50, 50, 50)
	wantSlots(t, "cell 1 still at the test's 128", f, 2, 128, 128, 128)

	// Tests off: the tests' claim is gone, the programmer's values remain.
	r.testsPost(t, "clear", map[string]any{})
	f = r.wire(t, 2)
	wantSlots(t, "after the tests: master back at its base default", f, 1, 0)
	wantSlots(t, "after the tests: cell 1 back at its default colour", f, 2, 0, 0, 0)
	wantSlots(t, "after the tests: cell 2 red, virtual untouched (full)", f, 5, 200, 0, 0)
}

// TestDimmerTestWholeFixtureScopeRule: the batten selected WHOLE is tested
// through its master — the cells' virtual dimmers are left alone (the
// I2d4 scope rule) — and an RGB-only fixture with no dimmer channel, which
// used to offer no dimmer test at all, is tested through its own virtual
// dimmer.
func TestDimmerTestWholeFixtureScopeRule(t *testing.T) {
	r := newI2eTestsRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("BT")}, "attribute": "ColorAdd_G", "dmx": 90})
	r.selectNames(t, "BT", "RG")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	if n := availableCount(v, "dimmer_toggle"); n != 2 {
		t.Errorf("dimmer_toggle offered over %d fixtures, want 2 (BT by its master, RG by its virtual dimmer)", n)
	}
	if len(v.Tests) != 1 || len(v.Tests[0].Entries) != 2 {
		t.Fatalf("tests = %+v", v.Tests)
	}
	for _, e := range v.Tests[0].Entries {
		if !e.Applied {
			t.Errorf("%s: the dimmer test is not applied", e.EntryID)
		}
		if want := e.EntryID == r.ids["RG"]; e.Virtual != want {
			t.Errorf("%s: virtual = %v, want %v (BT through its master, RG through its virtual dimmer)", e.EntryID, e.Virtual, want)
		}
	}
	f := r.wire(t, 2)
	wantSlots(t, "BT whole: the master carries the test", f, 1, 128)
	for c := 0; c < 4; c++ {
		wantSlots(t, fmt.Sprintf("BT whole: cell %d colour unscaled (its virtual dimmer is not the fixture's intensity)", c+1), f, 2+3*c, 0, 90, 0)
	}
	wantSlots(t, "RG: no colour set = white at the test's 128", f, 201, 128, 128, 128)

	// The level follows the test: switched to its minimum.
	v = r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": false, "min": 0}}})
	f = r.wire(t, 2)
	wantSlots(t, "RG at the test's 0", f, 201, 0, 0, 0)
	wantSlots(t, "BT master at the test's 0", f, 1, 0)
	_ = v
}

// TestDimmerTestVirtualFades: a virtual dimmer level fades by the tests'
// fade time like a dimmer channel does — a target new to the tests starts
// from zero — and when a test stops driving it while the target stays in
// scope it fades back to untouched (full) and is then released.
func TestDimmerTestVirtualFades(t *testing.T) {
	r := newI2eTestsRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("RG")}, "attribute": "ColorAdd_R", "dmx": 200})
	r.selectNames(t, "RG")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 1000})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 255}}})
	f := r.wire(t, 2) // 30 ms in
	if v := f[200]; v > 20 {
		t.Errorf("30 ms into a 1 s fade the virtual level shows red %d, want near 0 (a new target starts from zero)", v)
	}
	r.advance(t, 470*time.Millisecond)
	f = r.wire(t, 2) // ~530 ms in
	if v := f[200]; v < 80 || v > 130 {
		t.Errorf("half way through the fade red = %d, want about 100 (200 x ~0.5)", v)
	}
	r.advance(t, 600*time.Millisecond)
	wantSlots(t, "fade done: red 200 x 255/255", r.wire(t, 2), 201, 200, 0, 0)

	// Switch to a test RG does not have: RG stays in scope, nothing drives
	// its virtual dimmer any more, so it fades back to untouched (full) and
	// is released — the programmer's red shows unscaled.
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": false, "min": 0}}})
	wantSlots(t, "test at 0", r.wire(t, 2), 201, 0, 0, 0)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 1000})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "move_extreme", "target": "pan_max"}}})
	if len(v.Targets) != 1 {
		t.Fatalf("RG should stay in scope: %+v", v.Targets)
	}
	r.advance(t, 470*time.Millisecond)
	f = r.wire(t, 2)
	if v := f[200]; v < 80 || v > 130 {
		t.Errorf("half way back to untouched red = %d, want about 100", v)
	}
	r.advance(t, 600*time.Millisecond)
	wantSlots(t, "back to untouched: red 200 unscaled", r.wire(t, 2), 201, 200, 0, 0)
	r.advance(t, 100*time.Millisecond)
	wantSlots(t, "released: still red 200", r.wire(t, 2), 201, 200, 0, 0)
}

// TestDimmerTestShowsWhiteWhereColourIsUnset (I2f, owner 2026-10-09): a
// dimmer test on a fixture or cell whose colour is unset shows it at full
// white for the test only — the batten selected whole (its master pulses,
// its cells' colour is unset) and an LED par with a real dimmer and RGB.
// Manual colour still wins; the white is released when the test ends and
// is never written into the programmer.
func TestDimmerTestShowsWhiteWhereColourIsUnset(t *testing.T) {
	r := newI2eTestsRig(t)
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "channelSets": []any{}}
	}
	par := map[string]any{"name": "PAR", "fixtureType": "Spec-derived LED par", "footprint": 4, "universe": 2, "startAddress": 301,
		"channelFunctions": map[string]any{"1": ch("Dimmer"), "2": ch("ColorAdd_R"), "3": ch("ColorAdd_G"), "4": ch("ColorAdd_B")}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{par}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import PAR: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	bt := r.ids["BT"]
	r.arm(t)
	// Cell 2 has a manual colour; the rest of the batten and the par have none.
	r.set(t, map[string]any{"targets": []any{cellTarget(bt, "Pixel 2:0")}, "attribute": "ColorAdd_B", "dmx": 60})
	r.selectNames(t, "BT", "PAR")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	f := r.wire(t, 2)
	wantSlots(t, "BT master carries the test", f, 1, 128)
	wantSlots(t, "BT cell 1: colour unset = white for the test", f, 2, 255, 255, 255)
	wantSlots(t, "BT cell 2: manual blue wins over the test's white", f, 5, 0, 0, 60)
	wantSlots(t, "BT cell 4: white", f, 11, 255, 255, 255)
	wantSlots(t, "PAR: dimmer carries the test, colour unset = white", f, 301, 128, 255, 255, 255)

	// Never written into the programmer.
	var pv struct {
		Touched int
		Groups  []struct {
			Attributes []struct {
				Attribute string
				Channels  []struct {
					EntryID string
					Cell    string
					Touched bool
				}
			}
		}
	}
	r.get(t, "/api/programmer", &pv)
	if pv.Touched != 1 {
		t.Errorf("the programmer holds %d channels, want 1 (cell 2's blue): the test's white must not be stored", pv.Touched)
	}
	for _, g := range pv.Groups {
		for _, a := range g.Attributes {
			for _, c := range a.Channels {
				if c.Touched && (c.EntryID == r.ids["PAR"] || (c.EntryID == bt && c.Cell != "Pixel 2:0")) {
					t.Errorf("the test's white was written into the programmer: %s %s %s", c.EntryID, c.Cell, a.Attribute)
				}
			}
		}
	}

	// The test ends: the white is released.
	r.testsPost(t, "clear", map[string]any{})
	f = r.wire(t, 2)
	wantSlots(t, "after the test: BT cell 1 back to its unset colour", f, 2, 0, 0, 0)
	wantSlots(t, "after the test: BT cell 2 keeps its manual blue", f, 5, 0, 0, 60)
	wantSlots(t, "after the test: PAR colour back to unset", f, 302, 0, 0, 0)
}
