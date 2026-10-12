package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// i2c2ShaperSpot is a GDTF-sourced channel layout for a fixture with
// framing blades, an assembly rotate, a gobo wheel whose second slot has
// no media and a colour wheel whose first slot states no colour. None of
// the real vendor extracts in testdata has blades, so this one is built
// here; its attribute names, physical units and wheel/slot wiring follow
// the GDTF attribute definitions (Blade(n)A/B, ShaperRot: Angle,
// Gobo(n)WheelSpin: AngularSpeed).
func i2c2ShaperSpot(name string, start int) map[string]any {
	fn := func(attr, name string, from, to uint32, pf, pt float64, wheel string, sets ...patch.SetRange) patch.FunctionRange {
		if sets == nil {
			sets = []patch.SetRange{}
		}
		return patch.FunctionRange{LogicalAttribute: attr, Attribute: attr, Name: name, DMXFrom: from, DMXTo: to, PhysicalFrom: pf, PhysicalTo: pt, HasDefault: true, Default: from, Wheel: wheel, Sets: sets}
	}
	ch := func(attr string, fns ...patch.FunctionRange) patch.ChannelFunction {
		return patch.ChannelFunction{GeometryInstance: "Head", Source: patch.SourceGDTF, Attribute: attr, FunctionName: fns[0].Name,
			DMXFrom: fns[0].DMXFrom, DMXTo: fns[0].DMXTo, HasDefault: true, ByteCount: 1, FunctionsKnown: true, Functions: fns, ChannelSets: []patch.ChannelSet{}}
	}
	spin := fn("Gobo1WheelSpin", "Gobo1WheelSpin", 128, 255, -60, 60, "Gobo1")
	spin.Default = 192 // a stated default inside the function: the rail jumps there
	cfs := map[uint16]patch.ChannelFunction{
		1: ch("Blade1A", fn("Blade1A", "Blade1A", 0, 255, 0, 1, "")),
		2: ch("Blade1B", fn("Blade1B", "Blade1B", 0, 255, 0, 1, "")),
		3: ch("Blade2A", fn("Blade2A", "Blade2A", 0, 255, 0, 1, "")),
		4: ch("Blade2B", fn("Blade2B", "Blade2B", 0, 255, 0, 1, "")),
		5: ch("ShaperRot", fn("ShaperRot", "ShaperRot", 0, 255, -45, 45, "")),
		6: ch("Gobo1", fn("Gobo1", "Gobo1", 0, 127, 0, 1, "Gobo1",
			patch.SetRange{Name: "Open", DMXFrom: 0, DMXTo: 63, HasWheelSlot: true, WheelSlot: 1},
			patch.SetRange{Name: "Dots", DMXFrom: 64, DMXTo: 127, HasWheelSlot: true, WheelSlot: 2}), spin),
		7: ch("Color1", fn("Color1", "Color1", 0, 255, 0, 1, "Color1",
			patch.SetRange{Name: "Mystery", DMXFrom: 0, DMXTo: 127, HasWheelSlot: true, WheelSlot: 1},
			patch.SetRange{Name: "Congo blue", DMXFrom: 128, DMXTo: 255, HasWheelSlot: true, WheelSlot: 2})),
	}
	wheels := []patch.Wheel{
		{Name: "Gobo1", Slots: []patch.WheelSlot{{Name: "Open"}, {Name: "Dots"}}},
		{Name: "Color1", Slots: []patch.WheelSlot{{Name: "Mystery"}, {Name: "Congo blue", HasColor: true, HasSRGB: true, SRGB: "#2a1f8f"}}},
	}
	return map[string]any{"name": name, "fixtureType": "Shaper Spot", "mode": "Std", "footprint": 7, "universe": 1, "startAddress": start,
		"channelFunctions": cfs, "wheels": wheels, "wheelsKnown": true}
}

// TestConsoleControls_I2c2 (Console-lite I2c2: colour §9, range segments
// and wheel slots §7/§8, shutter §10, shaper §11) runs
// static/js/testdata/console_i2c2_test.js: the real index.html in a small
// DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js and console.js against this real server, with two
// real Robe BMFL Spot and two real Robe LEDBeam 100 profiles, and two of
// the blade fixture above.
func TestConsoleControls_I2c2(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L2", 1, 101),
		i2c2ShaperSpot("SH1", 301),
		i2c2ShaperSpot("SH2", 311),
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp struct {
		Patch struct {
			Entries []struct{ ID, Name string }
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range resp.Patch.Entries {
		ids[e.Name] = e.ID
	}
	if len(ids) != 6 {
		t.Fatalf("patched %d entries, want 6", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_i2c2_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_i2c2_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_i2c2_test.js failed: %v", err)
	}
}

// TestI2c2PolishCSS pins two layout rules a DOM-only harness cannot see:
// set and function-label buttons are pressable at 44 px on EVERY pointer
// type (component-specs §7; the kit's .b5-btn--sm is 32 px on a mouse),
// and a fader caption wraps to its own line instead of squeezing the
// function name into one letter per line at 390 px.
func TestI2c2PolishCSS(t *testing.T) {
	css := i1bCommentRE.ReplaceAllString(i1Embedded(t, "static/css/screens-console-controls.css"), "")
	touch, wraps := false, false
	for _, m := range i1bRuleRE.FindAllStringSubmatch(css, -1) {
		sel := strings.Join(strings.Fields(m[1]), " ")
		if strings.Contains(sel, ".b5-cc-sets .b5-btn") && strings.Contains(sel, ".b5-cc-seglabel") &&
			strings.Contains(m[2], "min-height: var(--b5-size-touch-min)") {
			touch = true
		}
		if sel == ".b5-cc-faderhead .b5-caption" && strings.Contains(m[2], "white-space: normal") {
			wraps = true
		}
	}
	if !touch {
		t.Error("set and function-label buttons have no unconditional 44 px minimum (component-specs §7)")
	}
	if !wraps {
		t.Error("the fader caption is still nowrap: at 390 px the function name breaks one letter per line")
	}
}
