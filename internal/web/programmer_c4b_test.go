package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// Console-lite C4b — the programmer's tools — at the HTTP surface, on the
// same real-profile rig as programmer_c4a_test.go (BMFL x2 on universe 0;
// LEDBeam, Paladin Cube, unprofiled G1 on universe 1), wire decoded at
// FakeTransport. Every route under test is new, so on the C4a tree every
// test here fails (404s) — see proof-c4b.txt.

type c4bHighlight struct {
	On              bool
	Lowlight        bool
	LowlightPercent int
	Channels        int
	Unresolved      []struct {
		EntryID string
		Offset  int
		Reason  string
	}
	Lowlit      int
	LowlitCells int // I2d3
	NoDimmer    []string
}

type c4bView struct {
	c4aView
	Highlight    c4bHighlight
	StoredGroups []struct {
		ID       string
		Name     string
		EntryIDs []string
		Members  []struct{ EntryID, Cell string }
	}
	Presets []struct {
		ID       string
		Name     string
		Family   string
		Fixtures int
		Channels int
	}
}

func (r *c4aRig) view4b(t *testing.T) c4bView {
	t.Helper()
	rr := r.do(t, "GET", "/api/programmer", nil, nil)
	var v c4bView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &v) != nil {
		t.Fatalf("GET /api/programmer: %d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return v
}

func cellTarget(id, cell string) map[string]any { return map[string]any{"entryId": id, "cell": cell} }

// TestHighlightLowlightOnTheWireAndOffRestoresExactly: Highlight drives the
// selection to its highlight values (profile highlight, dimmer full,
// shutter/colour open sets), Lowlight leaves every fixture outside the
// selection alone (I2d2, §15: it dims only non-highlighted members of the
// selection, and the whole selection is highlighted here), and switching
// it off gives back the exact frames — the
// programmer's stored values are never touched.
func TestHighlightLowlightOnTheWireAndOffRestoresExactly(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("B2")}, "attribute": "Dimmer", "dmx": 1000})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "Dimmer", "dmx": 101})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "ColorAdd_G", "dmx": 7})
	r.selectNames(t, "B1", "L1")
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Pan", "dmx": 1234})
	u0, u1 := r.wire(t, 0), r.wire(t, 1)
	touchedBefore := r.view4b(t).Touched

	var v c4bView
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, &v)
	if !v.Highlight.On || !v.Highlight.Lowlight || v.Highlight.LowlightPercent != 20 {
		t.Fatalf("highlight state: %+v", v.Highlight)
	}
	h0, h1 := r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "B1 dimmer full", h0, 40, 255, 255)
	// BMFL "Shutter open" 32-63 states one physical value (1 to 1): its
	// DMXFrom, 32 — the value Rig Check's shutter rule uses too.
	wantSlots(t, "B1 shutter open set", h0, 39, 32)
	// C4a channel-set rule on the two wheels' "Open/white - Positioning":
	// Color1's spans physical -0.5..0.5, so its midpoint 2313 (0x0909);
	// Color2's states 0 to 0, so its DMXFrom 0.
	wantSlots(t, "B1 colour wheels open/white", h0, 7, 0x09, 0x09, 0, 0)
	wantSlots(t, "B1 pan untouched by highlight", h0, 1, slots(u0, 1, 2)...)
	wantSlots(t, "B2 (not selected) not lowlit: 1000", h0, 81, 0x03, 0xE8)
	// LEDBeam: GDTF 1.0 <DMXChannel Highlight="255/1"> on RGBW, shutter and
	// dimmer; Color1 has none, its "Open" set (0) is used.
	wantSlots(t, "L1 RGBW profile highlight", h1, 7, 255, 255, 255, 255)
	wantSlots(t, "L1 Color1 open, shutter and dimmer highlight", h1, 12, 0, 255, 255)
	unresolved := map[int]bool{}
	for _, u := range v.Highlight.Unresolved {
		if u.EntryID == r.ids["B1"] {
			unresolved[u.Offset] = true
		}
	}
	for _, off := range []int{11, 12, 13, 14, 15} { // CMY, CTO, ColorMacro1: no highlight, no open set
		if !unresolved[off] {
			t.Errorf("B1 offset %d should be reported unresolved: %+v", off, v.Highlight.Unresolved)
		}
	}
	// I2d2: the virtual-dimmer and no-dimmer lowlight cases are proven
	// on selected, non-highlighted fixtures in programmer_i2d2_test.go.
	if len(v.Highlight.NoDimmer) != 0 || v.Highlight.Lowlit != 0 {
		t.Errorf("lowlight: lowlit %d, noDimmer %v (want none: the whole selection is highlighted)", v.Highlight.Lowlit, v.Highlight.NoDimmer)
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"lowlightPercent": 50}, nil)
	wantSlots(t, "B2 still untouched at 50%", r.wire(t, 0), 81, 0x03, 0xE8)

	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)
	if f := r.wire(t, 0); !bytes.Equal(f, u0) {
		t.Errorf("universe 0 after highlight off differs from before")
	}
	if f := r.wire(t, 1); !bytes.Equal(f, u1) {
		t.Errorf("universe 1 after highlight off differs from before")
	}
	if n := r.view4b(t).Touched; n != touchedBefore {
		t.Errorf("highlight changed the programmer's stored values: %d touched, was %d", n, touchedBefore)
	}
	rr := r.do(t, "POST", "/api/programmer/highlight", map[string]any{"lowlightPercent": 120}, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("lowlight 120%%: %d", rr.Code)
	}
}

// TestHighlightFollowsSelection: with Highlight on, changing the selection
// moves it; the previously selected fixture goes back to its own level
// (I2d2: Lowlight never dims fixtures outside the selection).
func TestHighlightFollowsSelection(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("B1"), r.target("B2")}, "attribute": "Dimmer", "dmx": 50000})
	r.selectNames(t, "B1")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	f := r.wire(t, 0)
	wantSlots(t, "B1 highlighted", f, 40, 255, 255)
	wantSlots(t, "B2 not selected: not lowlit (50000)", f, 81, 0xC3, 0x50)
	r.selectNames(t, "B2")
	f = r.wire(t, 0)
	wantSlots(t, "B1 back at 50000", f, 40, 0xC3, 0x50)
	wantSlots(t, "B2 now highlighted", f, 81, 255, 255)
}

// TestLocateReportsPerAttribute: Locate writes into the programmer — dimmer
// full, shutter open, pan/tilt default, colour/beam default — and says how
// each attribute was resolved; on a hand-made first-function profile with
// no defaults (labelled: spec-derived, not a vendor file) pan falls to
// physical 0, gobo to its "Open" set, and zoom is reported unknown.
func TestLocateReportsPerAttribute(t *testing.T) {
	r := newC4aRig(t)
	m1 := map[string]any{"name": "M1", "fixtureType": "Spec-derived mover", "footprint": 3, "universe": 2, "startAddress": 1,
		"channelFunctions": map[string]any{
			"1": map[string]any{"source": "gdtf", "attribute": "Pan", "functionName": "Pan", "dmxFrom": 0, "dmxTo": 255, "physicalFrom": -270, "physicalTo": 270, "channelSets": []any{}},
			"2": map[string]any{"source": "gdtf", "attribute": "Gobo1", "functionName": "Gobo1", "dmxFrom": 0, "dmxTo": 255, "physicalFrom": 0, "physicalTo": 1,
				"channelSets": []any{map[string]any{"name": "Gobo 1", "dmxFrom": 0, "physicalFrom": 0, "physicalTo": 0}, map[string]any{"name": "Open", "dmxFrom": 20, "physicalFrom": 0, "physicalTo": 0}}},
			"3": map[string]any{"source": "gdtf", "attribute": "Zoom", "functionName": "Zoom", "dmxFrom": 0, "dmxTo": 255, "physicalFrom": 10, "physicalTo": 40, "channelSets": []any{}},
		}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{m1}}, nil); rr.Code != 200 {
		t.Fatalf("import M1: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	_ = json.Unmarshal(r.do(t, "GET", "/api/patch", nil, nil).Body.Bytes(), &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	r.arm(t)
	r.selectNames(t, "B1", "L1", "M1")
	var res struct {
		Applied    int
		Attributes []struct {
			Attribute  string
			Applied    int
			How        []string
			Unresolved []struct{ EntryID, Reason string }
		}
	}
	r.post(t, "/api/programmer/locate", map[string]any{}, &res)
	got := map[string]string{}
	for _, a := range res.Attributes {
		got[a.Attribute] = strings.Join(a.How, "|")
		if a.Attribute == "Zoom" && (len(a.Unresolved) != 1 || a.Unresolved[0].EntryID != r.ids["M1"] || a.Applied != 1) {
			t.Errorf("Zoom: BMFL default applied, M1 unresolved: %+v", a)
		}
	}
	for attr, how := range map[string]string{"Dimmer": "full", "Pan": "default|physical 0", "Tilt": "default",
		"Shutter1": "open set Shutter open|open set Shutter Open", "Gobo1": "default|open set Open", "ColorAdd_R": "default", "Iris": "default"} {
		if got[attr] != how {
			t.Errorf("locate %s how = %q, want %q", attr, got[attr], how)
		}
	}
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 dimmer full", u0, 40, 255, 255)
	wantSlots(t, "B1 pan default", u0, 1, 128, 0)
	wantSlots(t, "M1 pan physical 0 (127.5 -> 128), gobo Open", r.wire(t, 2), 1, 128, 20)
	if d := r.view4b(t).attr("Dimmer"); d == nil || !d.AllTouched {
		t.Errorf("locate values are programmer values (touched): %+v", d)
	}
}

// TestFanLinearReverseMirrorAndCells: values spread in selection order,
// each channel at its own resolution (16-bit BMFL, 8-bit LEDBeam), cells as
// positions; a mirror fan needs a centre.
func TestFanLinearReverseMirrorAndCells(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "B2", "L1")
	fan := func(shape string, from, to any) {
		r.post(t, "/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": shape,
			"from": map[string]any{"fraction": from}, "to": map[string]any{"fraction": to}}, nil)
	}
	fan("linear", 0, 1)
	u0, u1 := r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "linear B1", u0, 40, 0, 0)
	wantSlots(t, "linear B2 (0.5 x 65535 = 32767.5 -> 32768)", u0, 81, 128, 0)
	wantSlots(t, "linear L1", u1, 14, 255)
	fan("reverse", 0, 1)
	u0, u1 = r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "reverse B1", u0, 40, 255, 255)
	wantSlots(t, "reverse B2", u0, 81, 128, 0)
	wantSlots(t, "reverse L1", u1, 14, 0)
	fan("mirror", 0, 1)
	u0, u1 = r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "mirror B1 (end)", u0, 40, 255, 255)
	wantSlots(t, "mirror B2 (centre)", u0, 81, 0, 0)
	wantSlots(t, "mirror L1 (end)", u1, 14, 255)

	r.selectNames(t, "B2", "B1")
	r.post(t, "/api/programmer/fan", map[string]any{"attribute": "Pan", "shape": "linear",
		"from": map[string]any{"physical": -90}, "to": map[string]any{"physical": 90}}, nil)
	u0 = r.wire(t, 0)
	wantSlots(t, "B2 first: -90 deg (180/540 x 65535 = 21845)", u0, 42, 0x55, 0x55)
	wantSlots(t, "B1 second: +90 deg (43690)", u0, 1, 0xAA, 0xAA)
	rr := r.do(t, "POST", "/api/programmer/fan", map[string]any{"attribute": "Pan", "shape": "mirror",
		"from": map[string]any{"fraction": 0}, "to": map[string]any{"fraction": 1}}, nil)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "at least 3") {
		t.Errorf("mirror over 2 fixtures: %d %s", rr.Code, rr.Body.String())
	}

	p1 := r.ids["P1"]
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(p1, "Beam 1:0"), cellTarget(p1, "Beam 2:0"), cellTarget(p1, "Beam 3:0")}}, nil)
	r.post(t, "/api/programmer/fan", map[string]any{"attribute": "ColorAdd_R", "shape": "linear",
		"from": map[string]any{"fraction": 0}, "to": map[string]any{"fraction": 1}}, nil)
	u1 = r.wire(t, 1)
	wantSlots(t, "cell 1 red", u1, 101, 0, 0)
	wantSlots(t, "cell 2 red", u1, 109, 128, 0)
	wantSlots(t, "cell 3 red", u1, 117, 255, 255)
	r.selectNames(t, "B1")
	rr = r.do(t, "POST", "/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": "linear",
		"from": map[string]any{"fraction": 0}, "to": map[string]any{"fraction": 1}}, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("a fan over one fixture: %d %s", rr.Code, rr.Body.String())
	}
}

// TestGroupStoreWithCellsAndOldGroups: a stored group keeps fixtures AND
// cells in selection order (entryIds for older readers); a group saved
// before C4b (no members) loads as whole fixtures.
func TestGroupStoreWithCellsAndOldGroups(t *testing.T) {
	r := newC4aRig(t)
	p1, b2 := r.ids["P1"], r.ids["B2"]
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(p1, "Beam 3:0"), cellTarget(b2, ""), cellTarget(p1, "Beam 1:0")}}, nil)
	var v c4bView
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Mix"}, &v)
	if len(v.StoredGroups) != 1 {
		t.Fatalf("stored groups: %+v", v.StoredGroups)
	}
	g := v.StoredGroups[0]
	if len(g.Members) != 3 || g.Members[0].Cell != "Beam 3:0" || g.Members[1].EntryID != b2 || g.Members[2].Cell != "Beam 1:0" ||
		strings.Join(g.EntryIDs, ",") != p1+","+b2 {
		t.Fatalf("group members/entryIds: %+v", g)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "none"}, nil)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "group": g.ID}, &v)
	if len(v.Selection) != 3 || v.Selection[0].Cell != "Beam 3:0" || v.Selection[1].EntryID != b2 || v.Selection[2].Cell != "Beam 1:0" {
		t.Fatalf("selecting the group: %+v", v.Selection)
	}
	r.selectNames(t, "L1")
	r.post(t, "/api/programmer/groups/update", map[string]any{"id": g.ID}, &v)
	r.post(t, "/api/programmer/groups/rename", map[string]any{"id": g.ID, "name": "Beam"}, &v)
	if v.StoredGroups[0].Name != "Beam" || len(v.StoredGroups[0].Members) != 1 || v.StoredGroups[0].Members[0].EntryID != r.ids["L1"] {
		t.Fatalf("update + rename: %+v", v.StoredGroups[0])
	}
	r.post(t, "/api/programmer/groups/delete", map[string]any{"id": g.ID}, &v)
	if len(v.StoredGroups) != 0 {
		t.Fatalf("delete: %+v", v.StoredGroups)
	}

	// A pre-C4b workspace exactly as an older build wrote it.
	old := `{"groups":[{"id":"w-1","name":"Old","entryIds":["` + r.ids["B1"] + `","` + r.ids["L1"] + `"]}]}`
	if _, err := r.h.srv.PatchStore.Mutate(func(p *patch.Patch) error { p.Workspace = json.RawMessage(old); return nil }); err != nil {
		t.Fatal(err)
	}
	v = r.view4b(t)
	if len(v.StoredGroups) != 1 || len(v.StoredGroups[0].Members) != 2 || v.StoredGroups[0].Members[1].EntryID != r.ids["L1"] || v.StoredGroups[0].Members[1].Cell != "" {
		t.Fatalf("old group: %+v", v.StoredGroups)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "group": "w-1"}, &v)
	if len(v.Selection) != 2 || v.Selection[0].Name != "B1" || v.Selection[1].Name != "L1" {
		t.Fatalf("selecting an old group: %+v", v.Selection)
	}
}

// TestPresetStoreRecallExactByTypeAndUnmatched: a colour preset stores the
// selection's touched colour values; recall gives each selected fixture its
// own values, or a same type+mode fixture's, and reports the rest.
func TestPresetStoreRecallExactByTypeAndUnmatched(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "L1")
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Color1", "slot": 2})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "ColorAdd_G", "dmx": 12})
	r.set(t, map[string]any{"attribute": "Dimmer", "fraction": 1}) // not colour: not stored
	var v c4bView
	r.post(t, "/api/programmer/presets/store", map[string]any{"name": "Red-ish", "family": "colour"}, &v)
	if len(v.Presets) != 1 || v.Presets[0].Family != "colour" || v.Presets[0].Fixtures != 2 || v.Presets[0].Channels != 2 {
		t.Fatalf("stored preset: %+v", v.Presets)
	}
	id := v.Presets[0].ID
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)
	r.selectNames(t, "B2", "L1", "P1")
	var rec struct {
		Applied int
		Targets []struct {
			EntryID  string
			How      string
			From     string
			Channels int
			Reason   string
		}
	}
	r.post(t, "/api/programmer/presets/recall", map[string]any{"id": id}, &rec)
	how := map[string]string{}
	for _, tg := range rec.Targets {
		how[tg.EntryID] = tg.How
		if tg.EntryID == r.ids["B2"] && tg.From != r.ids["B1"] {
			t.Errorf("B2 by type should come from B1: %+v", tg)
		}
		if tg.EntryID == r.ids["P1"] && tg.Reason == "" {
			t.Errorf("P1 got nothing and should say why: %+v", tg)
		}
	}
	if how[r.ids["B2"]] != "by-type" || how[r.ids["L1"]] != "exact" || how[r.ids["P1"]] != "nothing" || rec.Applied != 2 {
		t.Fatalf("recall: %+v", rec)
	}
	wantSlots(t, "B2 colour from B1 (slot 2, 7067)", r.wire(t, 0), 48, 0x1B, 0x9B)
	wantSlots(t, "L1 green exact", r.wire(t, 1), 8, 12)

	r.selectNames(t, "P1")
	rr := r.do(t, "POST", "/api/programmer/presets/recall", map[string]any{"id": id}, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("recall with nothing matching: %d %s", rr.Code, rr.Body.String())
	}
	rr = r.do(t, "POST", "/api/programmer/presets/store", map[string]any{"name": "Empty", "family": "colour"}, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("storing a family the selection never touched: %d %s", rr.Code, rr.Body.String())
	}
	r.selectNames(t, "L1")
	r.set(t, map[string]any{"attribute": "ColorAdd_B", "dmx": 99})
	r.post(t, "/api/programmer/presets/overwrite", map[string]any{"id": id}, &v)
	r.post(t, "/api/programmer/presets/rename", map[string]any{"id": id, "name": "Blue"}, &v)
	if v.Presets[0].Name != "Blue" || v.Presets[0].Channels != 2 || v.Presets[0].Fixtures != 1 {
		t.Fatalf("overwrite + rename: %+v", v.Presets[0])
	}
	r.post(t, "/api/programmer/presets/delete", map[string]any{"id": id}, &v)
	if len(v.Presets) != 0 {
		t.Fatalf("delete: %+v", v.Presets)
	}
}

// TestProgrammerToolsShowIsolation: groups and presets belong to their show;
// switching shows clears the programmer and Highlight; switching back finds
// that show's groups and presets again.
func TestProgrammerToolsShowIsolation(t *testing.T) {
	dir := t.TempDir()
	r := newC4aRigWith(t, func(h *testHarness) { h.srv.SetPatchStorePath(filepath.Join(dir, "show.json")) })
	r.arm(t)
	r.selectNames(t, "B1")
	r.set(t, map[string]any{"attribute": "Dimmer", "dmx": 65535})
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "First show group"}, nil)
	r.post(t, "/api/programmer/presets/store", map[string]any{"name": "Full", "family": "dimmer"}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	r.post(t, "/api/patches", map[string]any{"name": "Second show"}, nil)
	v := r.view4b(t)
	if len(v.StoredGroups) != 0 || len(v.Presets) != 0 || v.Highlight.On || len(v.Selection) != 0 || v.Touched != 0 {
		t.Fatalf("second show sees the first one's tools: groups %d presets %d highlight %v selection %d touched %d",
			len(v.StoredGroups), len(v.Presets), v.Highlight.On, len(v.Selection), v.Touched)
	}
	r.post(t, "/api/patches/default/load", map[string]any{}, nil)
	v = r.view4b(t)
	if len(v.StoredGroups) != 1 || v.StoredGroups[0].Name != "First show group" || len(v.Presets) != 1 || v.Presets[0].Name != "Full" || v.Touched != 0 {
		t.Fatalf("back on the first show: %+v / %+v, touched %d", v.StoredGroups, v.Presets, v.Touched)
	}
}

// TestProgrammerToolsBumpRevision: every tool is a programmer change.
func TestProgrammerToolsBumpRevision(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1", "B2", "L1")
	last := r.view4b(t).Revision
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/api/programmer/highlight", map[string]any{"highlight": true}},
		{"/api/programmer/locate", map[string]any{}},
		{"/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": "linear", "from": map[string]any{"dmx": 0}, "to": map[string]any{"fraction": 1}}},
		{"/api/programmer/groups/store", map[string]any{"name": "G"}},
		{"/api/programmer/presets/store", map[string]any{"name": "P", "family": "dimmer"}},
	} {
		r.post(t, step.path, step.body, nil)
		rev := r.view4b(t).Revision
		if rev <= last {
			t.Errorf("%s did not bump the revision (%d -> %d)", step.path, last, rev)
		}
		last = rev
	}
}
