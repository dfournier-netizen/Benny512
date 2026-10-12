package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Console-lite I2d4 — the G1 virtual dimmer extended to every cell with
// colour mixing and no dimmer of its own, even under a master dimmer (owner
// decision 2026-10-09). Proven at the HTTP surface on a 4-cell RGB batten
// with a master dimmer (BT: slot 1 Dimmer, cells "Pixel n" RGB at 2-4, 5-7,
// 8-10, 11-13 on universe 2) and a two-cell strobe bar with no dimmer and
// no colour (ST), reading levels back as ArtDmx.

func battenEntry(name string, universe, start int) map[string]any {
	cf := map[string]any{"1": map[string]any{"source": "gdtf", "attribute": "Dimmer", "functionName": "Dimmer"}}
	for c := 0; c < 4; c++ {
		for i, a := range []string{"ColorAdd_R", "ColorAdd_G", "ColorAdd_B"} {
			cf[fmt.Sprint(2+3*c+i)] = map[string]any{"source": "gdtf", "attribute": a, "functionName": a, "geometryInstance": fmt.Sprintf("Pixel %d:0", c+1)}
		}
	}
	return map[string]any{"name": name, "fixtureType": "Demo RGB Batten", "mode": "13ch", "footprint": 13, "universe": universe, "startAddress": start, "channelFunctions": cf}
}

func strobeBarEntry(name string, universe, start int) map[string]any {
	cf := map[string]any{}
	for c := 0; c < 2; c++ {
		cf[fmt.Sprint(1+c)] = map[string]any{"source": "gdtf", "attribute": "Shutter1", "functionName": "Strobe", "geometryInstance": fmt.Sprintf("Strobe %d:0", c+1)}
	}
	return map[string]any{"name": name, "fixtureType": "Demo Strobe Bar", "mode": "2ch", "footprint": 2, "universe": universe, "startAddress": start, "channelFunctions": cf}
}

func newI2d4Rig(t *testing.T) *c4aRig {
	t.Helper()
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{battenEntry("BT", 2, 1), strobeBarEntry("ST", 2, 101)}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	if err := json.Unmarshal(doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r := &c4aRig{h: h, ids: map[string]string{}}
	for _, e := range resp.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	return r
}

// TestVirtualDimmerCellsUnderMaster. The rule (programmer_model.go):
// every cell with red, green and blue and no Dimmer of its own has a
// virtual dimmer, even when the fixture has a master. Output = the cell's
// colour x its virtual level, and the master dimmer — a real channel —
// stays on top. A WHOLE-fixture intensity control (Dimmer set, fan,
// group-fader member, Lowlight) is the master when there is one; the cells'
// virtual dimmers are reached by selecting the cells. Highlight and
// presets take the whole look.
func TestVirtualDimmerCellsUnderMaster(t *testing.T) {
	r := newI2d4Rig(t)
	bt, st := r.ids["BT"], r.ids["ST"]
	cell := func(n int) map[string]any { return cellTarget(bt, fmt.Sprintf("Pixel %d:0", n)) }
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("BT")}, "attribute": "ColorAdd_R", "dmx": 200})
	r.set(t, map[string]any{"targets": []any{r.target("BT")}, "attribute": "ColorAdd_G", "dmx": 100})
	reds := func(what string, master byte, c1, c2, c3, c4 byte) {
		t.Helper()
		f := r.wire(t, 2)
		wantSlots(t, what+": master", f, 1, master)
		for i, v := range []byte{c1, c2, c3, c4} {
			wantSlots(t, fmt.Sprintf("%s: cell %d red", what, i+1), f, 2+3*i, v)
		}
	}

	// The model: four cell virtual dimmers beside the real master.
	var fx struct {
		Fixtures []struct {
			EntryID    string
			Parameters []struct {
				Attribute string
				Cell      string
				Virtual   bool
			}
		}
	}
	r.get(t, "/api/programmer/fixtures", &fx)
	virt := map[string]bool{}
	for _, f := range fx.Fixtures {
		for _, p := range f.Parameters {
			if f.EntryID == bt && p.Attribute == "Dimmer" && p.Virtual {
				virt[p.Cell] = true
			}
			if f.EntryID == st && p.Attribute == "Dimmer" {
				t.Errorf("ST (strobe cells, no colour) has a Dimmer: %+v", p)
			}
		}
	}
	if len(virt) != 4 || virt[""] {
		t.Fatalf("BT virtual dimmers by cell: %v, want one for each of the 4 cells and none for the whole fixture", virt)
	}

	// Cell level: Pixel 2 at 128 scales its colour; the master is untouched.
	r.set(t, map[string]any{"targets": []any{cell(2)}, "attribute": "Dimmer", "dmx": 128})
	reds("cell 2 virtual 128", 0, 200, 100, 200, 200)
	// Whole fixture: the master, never the cells' virtual dimmers on top.
	res := r.set(t, map[string]any{"targets": []any{r.target("BT")}, "attribute": "Dimmer", "dmx": 77})
	if len(res.Applied) != 1 {
		t.Errorf("whole-fixture Dimmer applied to %d channels, want 1 (the master): %+v", len(res.Applied), res.Applied)
	}
	reds("whole Dimmer 77", 77, 200, 100, 200, 200)

	// Fan over the four cells: their virtual dimmers 0 / 85 / 170 / 255.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cell(1), cell(2), cell(3), cell(4)}}, nil)
	r.post(t, "/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": "linear", "from": map[string]any{"dmx": 0}, "to": map[string]any{"dmx": 255}}, nil)
	reds("fan over the cells", 77, 0, 66, 133, 200)

	// Presets: a dimmer preset of the cells brings the fan back.
	var v c4bView
	r.post(t, "/api/programmer/presets/store", map[string]any{"name": "Ramp", "family": "dimmer"}, &v)
	r.post(t, "/api/programmer/set", map[string]any{"attribute": "Dimmer", "dmx": 255}, nil)
	reds("cells full", 77, 200, 200, 200, 200)
	r.post(t, "/api/programmer/presets/recall", map[string]any{"id": v.Presets[0].ID}, nil)
	reds("preset recalled", 77, 0, 66, 133, 200)

	// Lowlight on the cells, stepped to cell 4: cells 1-3 at 20 %.
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "previous"}, &v)
	reds("lowlight, stepped to cell 4", 77, 0, 13, 26, 200)
	if v.Highlight.Lowlit != 3 || v.Highlight.LowlitCells != 3 {
		t.Errorf("lowlight: lowlit %d (cells %d), want 3 (3)", v.Highlight.Lowlit, v.Highlight.LowlitCells)
	}
	// Highlight the whole batten: master and every cell's virtual at full.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{r.target("BT")}}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "all"}, nil)
	f := r.wire(t, 2)
	for i := 0; i < 4; i++ {
		if f[1+3*i] == 0 || f[1+3*i] == 13 || f[1+3*i] == 66 {
			t.Errorf("highlight whole BT: cell %d red %d, want its colour at full (virtual dimmer at full)", i+1, f[1+3*i])
		}
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false, "lowlight": false}, nil)

	// A strobe-only cell has no dimmer, said in words.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cell(1), cellTarget(st, "Strobe 1:0"), cellTarget(st, "Strobe 2:0")}}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	if len(v.Highlight.NoDimmer) != 1 || v.Highlight.NoDimmer[0] != st {
		t.Errorf("lowlight with strobe cells: noDimmer %v, want [ST]", v.Highlight.NoDimmer)
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false, "lowlight": false}, nil)
	rr := r.do(t, "POST", "/api/programmer/set", map[string]any{"targets": []any{cellTarget(st, "Strobe 1:0")}, "attribute": "Dimmer", "dmx": 10}, nil)
	if rr.Code == http.StatusOK || !strings.Contains(rr.Body.String(), "has no Dimmer") {
		t.Errorf("Dimmer on a strobe cell: %d %s, want a refusal saying it has no Dimmer", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
}

// TestGroupFaderCellVirtualDimmer (I2d4): a stored group of cells moves the
// cells' virtual dimmers; the type fader (whole fixtures) moves the master
// only, so the two never multiply. ST cells are listed as not
// controllable.
func TestGroupFaderCellVirtualDimmer(t *testing.T) {
	r := newI2d4Rig(t)
	bt := r.ids["BT"]
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("BT")}, "attribute": "ColorAdd_R", "dmx": 200})
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(bt, "Pixel 3:0"), cellTarget(r.ids["ST"], "Strobe 1:0")}}, nil)
	var v c4bView
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "P3"}, &v)
	var fv struct {
		Faders []struct {
			ID              string
			Kind            string
			Controllable    int
			NotControllable []string
		}
	}
	r.get(t, "/api/faders", &fv)
	typeID, groupID := "", ""
	for _, f := range fv.Faders {
		if f.Kind == "group" {
			groupID = f.ID
			if f.Controllable != 1 || len(f.NotControllable) != 1 {
				t.Errorf("group fader: controllable %d, not %v; want 1 (Pixel 3) and the strobe cell", f.Controllable, f.NotControllable)
			}
		} else if strings.Contains(f.ID, "Batten") {
			typeID = f.ID
		}
	}
	r.post(t, "/api/faders/set", map[string]any{"id": groupID, "level": 0.5}, nil)
	f := r.wire(t, 2)
	wantSlots(t, "group fader 50 %: cell 3 red", f, 8, 100)
	wantSlots(t, "group fader 50 %: cell 1 red untouched", f, 2, 200)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	r.post(t, "/api/faders/set", map[string]any{"id": typeID, "level": 0.5}, nil)
	f = r.wire(t, 2)
	wantSlots(t, "type fader 50 %: master", f, 1, 128)
	wantSlots(t, "type fader 50 %: cells keep their colour (not scaled twice)", f, 2, 200)
	wantSlots(t, "type fader 50 %: cell 3", f, 8, 200)
}

func (r *c4aRig) get(t *testing.T, path string, out any) {
	t.Helper()
	rr := r.do(t, "GET", path, nil, nil)
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), out) != nil {
		t.Fatalf("GET %s: %d %s", path, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
}
