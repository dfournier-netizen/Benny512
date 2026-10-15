package web

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// J1d (owner 2026-10-13 10:39, "your calls all sound good — make the
// changes"), on the J1 rig (intensity_j1_test.go).

type j1dAttr struct {
	Attribute string
	Mixed     bool
	Value     *uint32
	Channels  []struct {
		Offset    int
		ByteCount int
		Value     uint32
		Virtual   bool
	}
}

func (r *c4aRig) j1dDimmer(t *testing.T) j1dAttr {
	t.Helper()
	var v struct {
		Groups []struct{ Attributes []j1dAttr }
	}
	r.get(t, "/api/programmer", &v)
	for _, g := range v.Groups {
		for _, a := range g.Attributes {
			if a.Attribute == "Dimmer" {
				return a
			}
		}
	}
	t.Fatalf("no Dimmer attribute in the programmer view")
	return j1dAttr{}
}

// TestJ1d_WholeFixtureDimmerNotMixedAcrossResolutions: a whole RZ's Dimmer
// card holds its 16-bit head dimmer (the master channel) and its 8-bit
// virtual master, always set together. Equal levels at different
// resolutions are one level: MIXED only when the normalized values differ
// at the coarser resolution.
func TestJ1d_WholeFixtureDimmerNotMixedAcrossResolutions(t *testing.T) {
	r := newJ1Rig(t)
	r.arm(t)
	r.selectNames(t, "RZ")
	check := func(what string, wantMixed bool) {
		t.Helper()
		a := r.j1dDimmer(t)
		if len(a.Channels) != 2 {
			t.Fatalf("%s: whole RZ Dimmer has %d channels, want 2 (head + virtual master)", what, len(a.Channels))
		}
		if a.Mixed != wantMixed {
			t.Errorf("%s: mixed %v, want %v (channels %+v)", what, a.Mixed, wantMixed, a.Channels)
		}
		if !wantMixed && a.Value == nil {
			t.Errorf("%s: no shared value", what)
		}
	}
	check("at rest (both 0)", false)
	for _, fr := range []float64{1, 0.5, 0.3, 0.07} {
		r.set(t, map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "fraction": fr})
		check("whole RZ Dimmer at "+ftoa(fr), false)
	}
	// Genuinely different levels still read MIXED: the virtual master alone
	// at 200 (its function is named "Master"; the head has none so named).
	r.post(t, "/api/programmer/set", map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "functionName": "Master", "dmx": 200}, nil)
	check("virtual master 200, head at 7 %", true)
	// One count apart at the coarse resolution is a different level.
	r.set(t, map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "fraction": 0.5})
	r.post(t, "/api/programmer/set", map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "functionName": "Master", "dmx": 129}, nil)
	check("head 32768 (128 of 255), virtual master 129", true)
}

func ftoa(f float64) string {
	return map[float64]string{1: "100 %", 0.5: "50 %", 0.3: "30 %", 0.07: "7 %"}[f]
}

// j1dTreeJD adds "JDT", the JDC-like strobe WITH its GDTF geometry tree, at
// universe 3 address 121 (Beam Module dimmer 121, Plate Module dimmer 123,
// beam pixel dimmers 136-138): the beam pixels hang under the Beam Module,
// the plate pixels under the Plate Module, both modules on the Base.
func j1dTreeJD(t *testing.T, r *c4aRig) {
	t.Helper()
	jdt := j1Entries()[0].(map[string]any)
	jdt["name"], jdt["fixtureType"], jdt["startAddress"] = "JDT", "Test JDC-like strobe (tree)", 121
	jdt["geometryParents"] = map[string]any{"Base:0": "", "Beam Module:0": "Base:0", "Plate Module:0": "Base:0",
		"Beam Pixel:0": "Beam Module:0", "Beam Pixel:1": "Beam Module:0", "Beam Pixel:2": "Beam Module:0",
		"Plate Pixel:0": "Plate Module:0", "Plate Pixel:3": "Plate Module:0", "Plate Pixel:6": "Plate Module:0"}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{jdt}}, nil); rr.Code != 200 {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &resp)
	for _, e := range resp.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
}

// TestJ1d_NoDoubleDimmingInsideAPart (owner 2026-10-13 10:39): a part fader
// drives the OUTERMOST dimmer(s) of its part; a dimmer of the part under
// another dimmer of the same part (by the GDTF tree) is dimmed by it on the
// fixture, so it is not also set to the part's level — the fader holds it
// open — and the software master does not multiply it either. Beam at 50 %
// shows 50 %, not 25 %. Without the tree (the plain JD), J1c's behaviour
// stays: every dimmer of the part takes the level.
func TestJ1d_NoDoubleDimmingInsideAPart(t *testing.T) {
	r := newJ1Rig(t)
	j1dTreeJD(t, r)
	r.arm(t)
	fd := j1cFaders(t, r)
	master, beam, plate := fd["Test JDC-like strobe (tree)"].ID, fd["Test JDC-like strobe (tree) / Beam"].ID, fd["Test JDC-like strobe (tree) / Plate"].ID
	if master == "" || beam == "" || plate == "" {
		t.Fatalf("JDT faders missing: %+v", fd)
	}
	f := r.wire(t, 3)
	wantSlots(t, "armed: JDT Beam Module and beam pixels dark", f, 121, 0)
	wantSlots(t, "armed: JDT beam pixel dimmers dark", f, 136, 0, 0, 0)

	r.fade(t, master, 1)
	r.fade(t, beam, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "Master full, Beam 50 %: Beam Module at 50 %", f, 121, 128)
	wantSlots(t, "Master full, Beam 50 %: beam pixels held open, not also 50 % (25 % on the fixture)", f, 136, 255, 255, 255)
	wantSlots(t, "Beam fader leaves the Plate alone", f, 123, 0)

	r.fade(t, beam, 1)
	r.fade(t, master, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "Master 50 %, Beam full: Beam Module multiplied once", f, 121, 128)
	wantSlots(t, "Master 50 %, Beam full: beam pixels not multiplied again", f, 136, 255, 255, 255)

	// A stored group of one beam pixel (a cell member: that cell's own
	// dimmer) keeps its level whichever fader moved last: the part fader's
	// hold is the weakest claim.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(r.ids["JDT"], "Beam Pixel:1")}}, nil)
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Pixel 1"}, nil)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{}}, nil)
	var gid string
	var fv struct{ Faders []j1Fader }
	r.get(t, "/api/faders", &fv)
	for _, g := range fv.Faders {
		if g.Kind == "group" {
			gid = g.ID
		}
	}
	r.fade(t, gid, 0.4)
	f = r.wire(t, 3)
	wantSlots(t, "group of pixel 1 at 40 % moved after Beam: pixel 1 at 40 %, the others held open", f, 136, 255, 102, 255)
	r.fade(t, beam, 1)
	f = r.wire(t, 3)
	wantSlots(t, "Beam moved after the group: pixel 1 keeps the group's 40 %", f, 136, 255, 102, 255)
	r.post(t, "/api/faders/release", map[string]any{"id": gid}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "group released: pixel 1 held open again", f, 136, 255, 255, 255)

	// Lowlight of the whole JDT goes through its master once: the Beam
	// Module scaled, the beam pixels not scaled on top.
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	r.fade(t, beam, 1)
	r.set(t, map[string]any{"targets": []any{r.target("JDT")}, "attribute": "Dimmer", "fraction": 1})
	r.set(t, map[string]any{"targets": []any{r.target("ER")}, "attribute": "Dimmer", "dmx": 255})
	r.selectNames(t, "ER", "JDT")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "JDT lowlit: Beam Module at 20 %", f, 121, 51)
	wantSlots(t, "JDT lowlit: beam pixels not scaled on top", f, 136, 255, 255, 255)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false, "lowlight": false}, nil)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)

	// Release: the inner dimmers go back to their rest.
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "released: beam pixels back to dark", f, 136, 0, 0, 0)

	// No tree (the plain JD): J1c unchanged — every dimmer of the part.
	r.fade(t, fd["Test JDC-like strobe"].ID, 1)
	r.fade(t, fd["Test JDC-like strobe / Beam"].ID, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "legacy JD Beam 50 %: Beam Module", f, 1, 128)
	wantSlots(t, "legacy JD Beam 50 %: every beam pixel too (no tree)", f, 16, 128, 128, 128)
}

// TestJ1d_SinglePartTypeFaderNoDoubleDimming (coordinator 2026-10-13, in
// line with Dom's "no double dimming"): a type with ONE intensity part has
// no part faders, so its type fader is that part's fader — where the tree
// nests its dimmers it sets only the outermost and holds the inner ones
// open, with the same give-way. "NB" (universe 3, 151-154): Head:0 Dimmer
// 151; Head Pixel:0/1/2 Dimmer 152-154, under the Head in the tree — one
// part ("head"), no master (the part holds cells).
func TestJ1d_SinglePartTypeFaderNoDoubleDimming(t *testing.T) {
	r := newJ1Rig(t)
	rows := []j1Row{{1, "Head:0", "Dimmer", 1, 0}, {2, "Head Pixel:0", "Dimmer", 1, 0}, {3, "Head Pixel:1", "Dimmer", 1, 0}, {4, "Head Pixel:2", "Dimmer", 1, 0}}
	nb := map[string]any{"name": "NB", "fixtureType": "Test nested bar", "mode": "Test", "footprint": 4, "universe": 3, "startAddress": 151,
		"channelFunctions": j1Functions(rows),
		"geometryParents":  map[string]any{"Base:0": "", "Head:0": "Base:0", "Head Pixel:0": "Head:0", "Head Pixel:1": "Head:0", "Head Pixel:2": "Head:0"}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{nb}}, nil); rr.Code != 200 {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &resp)
	for _, e := range resp.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	r.arm(t)
	fd := j1cFaders(t, r)
	typ, ok := fd["Test nested bar"]
	if !ok || typ.HasParts {
		t.Fatalf("NB type fader: %+v (want one plain fader, no parts)", typ)
	}
	r.fade(t, typ.ID, 0.5)
	f := r.wire(t, 3)
	wantSlots(t, "NB type fader 50 %: Head dimmer 50 %, pixels held open (50 % on the fixture, not 25 %)", f, 151, 128, 255, 255, 255)

	// A stored group of one pixel gives its level in either order.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(r.ids["NB"], "Head Pixel:1")}}, nil)
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "NB pixel 1"}, nil)
	var gid string
	var fv struct{ Faders []j1Fader }
	r.get(t, "/api/faders", &fv)
	for _, g := range fv.Faders {
		if g.Kind == "group" {
			gid = g.ID
		}
	}
	r.fade(t, gid, 0.4)
	f = r.wire(t, 3)
	wantSlots(t, "group of pixel 1 at 40 % after the type fader", f, 151, 128, 255, 102, 255)
	r.fade(t, typ.ID, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "type fader moved after the group: pixel 1 keeps 40 %", f, 151, 128, 255, 102, 255)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "released: dark", f, 151, 0, 0, 0, 0)
}

// TestConsoleControls_J1dMixedRange runs static/js/testdata/console_j1d_test.js:
// the real index.html and the LITERAL console scripts against this real
// server on the J1 rig. A whole RZ's Dimmer card whose 16-bit head (32 768,
// 50 %) and 8-bit virtual master (122, 47.8 %) genuinely differ reads
// "MIXED 48–50 %" (coordinator 2026-10-13), not a range of raw values on
// the 16-bit scale ("MIXED 0–50 %").
func TestConsoleControls_J1dMixedRange(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newJ1Rig(t)
	idsJSON, _ := json.Marshal(r.ids)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_j1d_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_j1d_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_j1d_test.js failed: %v", err)
	}
}
