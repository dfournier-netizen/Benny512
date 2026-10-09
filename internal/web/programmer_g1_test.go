package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Console-lite G1 — the virtual dimmer — at the HTTP surface, on the C4a
// rig (BMFL x2 on universe 0 with a real 16-bit Dimmer; LEDBeam, the Paladin
// Cube "P1" at 101 — three 16-bit RGBW cells, NO dimmer channel — and the
// unprofiled G1 on universe 1), plus "S1": a minimal RGB-only profile built
// by hand from the GDTF attribute names (spec-derived, NOT a vendor file:
// ColorAdd_R/G/B, 8-bit, no defaults, no dimmer) imported at universe 2,
// slot 1 through the real POST /api/patch/import. Every level is read back
// as ArtDmx decoded at session.FakeTransport under FakeClock.

func newG1Rig(t *testing.T) *c4aRig {
	t.Helper()
	r := newC4aRig(t)
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "physicalFrom": 0, "physicalTo": 1, "channelSets": []any{}}
	}
	s1 := map[string]any{"name": "S1", "fixtureType": "Spec-derived RGB (no dimmer)", "footprint": 3, "universe": 2, "startAddress": 1,
		"channelFunctions": map[string]any{"1": ch("ColorAdd_R"), "2": ch("ColorAdd_G"), "3": ch("ColorAdd_B")}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{s1}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import S1: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	_ = json.Unmarshal(r.do(t, "GET", "/api/patch", nil, nil).Body.Bytes(), &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	if r.ids["S1"] == "" {
		t.Fatal("S1 not patched")
	}
	return r
}

// p1Cells checks the Paladin's three RGBW cells (16-bit, slots 101-124).
func p1Cells(t *testing.T, what string, f []byte, r, g, b, w uint16) {
	t.Helper()
	for c := 0; c < 3; c++ {
		at := 101 + 8*c
		wantSlots(t, fmt.Sprintf("%s: P1 cell %d RGBW", what, c+1), f, at,
			byte(r>>8), byte(r), byte(g>>8), byte(g), byte(b>>8), byte(b), byte(w>>8), byte(w))
	}
}

type g1Fixtures struct {
	Fixtures []struct {
		EntryID    string
		Parameters []struct {
			Offset    int
			Offsets   []int
			Attribute string
			Cell      string
			Detail    string
			Default   uint32
			Virtual   bool
		}
	}
}

// TestVirtualDimmerModelAndUntouchedDefaults: rule 1 (who gets one, flagged
// virtual in both read models) and rule 2 (untouched = profile defaults on
// the wire; a colour set without touching it goes out unscaled).
func TestVirtualDimmerModelAndUntouchedDefaults(t *testing.T) {
	r := newG1Rig(t)
	var fx g1Fixtures
	if rr := r.do(t, "GET", "/api/programmer/fixtures", nil, nil); rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &fx) != nil {
		t.Fatalf("fixtures: %d", rr.Code)
	}
	virt := map[string][]string{}
	for _, f := range fx.Fixtures {
		for _, p := range f.Parameters {
			if p.Virtual {
				if p.Attribute != "Dimmer" || p.Detail != "virtual" || len(p.Offsets) != 0 || p.Default != 255 {
					t.Errorf("virtual parameter: %+v", p)
				}
				virt[f.EntryID] = append(virt[f.EntryID], fmt.Sprintf("%s@%d", p.Cell, p.Offset))
			}
		}
	}
	if got := fmt.Sprint(virt[r.ids["P1"]]); got != "[Beam 1:0@65281 Beam 2:0@65282 Beam 3:0@65283]" {
		t.Errorf("P1 virtual dimmers = %s, want one per cell", got)
	}
	if got := fmt.Sprint(virt[r.ids["S1"]]); got != "[@65280]" {
		t.Errorf("S1 virtual dimmers = %s, want one for the fixture", got)
	}
	for _, n := range []string{"B1", "L1", "G1"} {
		if len(virt[r.ids[n]]) != 0 {
			t.Errorf("%s has a real dimmer or no RGB, but got virtual %v", n, virt[r.ids[n]])
		}
	}

	r.arm(t)
	p1Cells(t, "untouched", r.wire(t, 1), 0, 0, 0, 0)
	wantSlots(t, "S1 untouched", r.wire(t, 2), 1, 0, 0, 0)
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "ColorAdd_B", "dmx": 1234})
	p1Cells(t, "blue set, dimmer untouched = full, unscaled", r.wire(t, 1), 0, 0, 1234, 0)

	r.selectNames(t, "P1")
	d := r.view(t).attr("Dimmer")
	if d == nil || len(d.Channels) != 3 || d.Touched || d.Value == nil || *d.Value != 255 {
		t.Fatalf("P1 Dimmer in GET /api/programmer: %+v", d)
	}
	var raw struct {
		Groups []struct {
			Attributes []struct {
				Attribute string
				Variants  []struct{ Virtual bool }
				Channels  []struct{ Virtual bool }
			}
		}
	}
	_ = json.Unmarshal(r.do(t, "GET", "/api/programmer", nil, nil).Body.Bytes(), &raw)
	flagged := false
	for _, g := range raw.Groups {
		for _, a := range g.Attributes {
			if a.Attribute == "Dimmer" {
				flagged = len(a.Variants) == 1 && a.Variants[0].Virtual && len(a.Channels) == 3 && a.Channels[0].Virtual
			}
		}
	}
	if !flagged {
		t.Errorf("GET /api/programmer does not flag the Dimmer as virtual: %+v", raw.Groups)
	}
}

// TestVirtualDimmerScalesColourOrWhite: rules 3 and 4 — no colour means
// white; a colour is scaled; 16-bit channels scale as one number (floor).
func TestVirtualDimmerScalesColourOrWhite(t *testing.T) {
	r := newG1Rig(t)
	r.arm(t)
	// fraction 0.5 of 0-255 = round(127.5) = 128; 65535 x 128 / 255 = 32896.
	r.set(t, map[string]any{"targets": []any{r.target("P1"), r.target("S1")}, "attribute": "Dimmer", "fraction": 0.5})
	p1Cells(t, "dimmer 50%, no colour = white at half", r.wire(t, 1), 32896, 32896, 32896, 32896)
	wantSlots(t, "S1 dimmer 50%, no colour = white at half", r.wire(t, 2), 1, 128, 128, 128)

	r.set(t, map[string]any{"targets": []any{r.target("P1"), r.target("S1")}, "attribute": "ColorAdd_R", "fraction": 1})
	p1Cells(t, "red at half, G/B/W stay 0", r.wire(t, 1), 32896, 0, 0, 0)
	wantSlots(t, "S1 red at half", r.wire(t, 2), 1, 128, 0, 0)

	// floor(1000 x 100 / 255) = 392 = 0x0188; floor(65535 x 100 / 255) = 25700.
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "ColorAdd_R", "dmx": 1000})
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "ColorAdd_G", "dmx": 65535})
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "Dimmer", "dmx": 100})
	p1Cells(t, "16-bit scaled exactly", r.wire(t, 1), 392, 25700, 0, 0)

	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "Dimmer", "dmx": 0})
	p1Cells(t, "dimmer 0", r.wire(t, 1), 0, 0, 0, 0)
}

// TestVirtualDimmerMixedSelectionOneCall: rule 6 — one Dimmer set reaches
// the BMFL's real 16-bit Dimmer and the Paladin's virtual ones.
func TestVirtualDimmerMixedSelectionOneCall(t *testing.T) {
	r := newG1Rig(t)
	r.arm(t)
	r.selectNames(t, "B1", "P1")
	res := r.set(t, map[string]any{"attribute": "Dimmer", "fraction": 0.5})
	if len(res.Applied) != 4 || len(res.Skipped) != 0 {
		t.Errorf("applied %d (want B1 + 3 cells), skipped %+v", len(res.Applied), res.Skipped)
	}
	wantSlots(t, "B1 real dimmer 32768", r.wire(t, 0), 40, 0x80, 0x00)
	p1Cells(t, "P1 virtual dimmer 128 = white half", r.wire(t, 1), 32896, 32896, 32896, 32896)
}

// TestVirtualDimmerHighlightLocateLowlight: rule 5 (highlight / locate =
// virtual full, white when no colour; the highlight source's claim wins
// over the programmer's — rule 7's priority resolution), Lowlight still
// scales on top, and highlight off restores the exact look.
func TestVirtualDimmerHighlightLocateLowlight(t *testing.T) {
	r := newG1Rig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "Dimmer", "dmx": 0})
	before := r.wire(t, 1)
	p1Cells(t, "programmer dimmer 0", before, 0, 0, 0, 0)
	r.selectNames(t, "P1")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	p1Cells(t, "highlight: white full over the programmer's 0", r.wire(t, 1), 65535, 65535, 65535, 65535)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)
	p1Cells(t, "highlight off: back to 0", r.wire(t, 1), 0, 0, 0, 0)

	// A colour is kept at full, not replaced by the profile's 65535 white.
	r.set(t, map[string]any{"targets": []any{cellTarget(r.ids["P1"], "Beam 1:0")}, "attribute": "ColorAdd_R", "dmx": 40000})
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	f := r.wire(t, 1)
	wantSlots(t, "highlight: cell 1 red mix at full", f, 101, 0x9C, 0x40, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "highlight: cell 2 no colour = white", f, 109, 255, 255, 255, 255, 255, 255, 255, 255)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)

	// Lowlight: highlight L1 (stepped to, first of L1, B1, P1), so B1 and
	// P1 — selected, not highlighted — are lowlit at 20% (I2d2, §15).
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Dimmer", "dmx": 50000})
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "Dimmer", "dmx": 128})
	// cell 1 red: floor(40000 x 128 / 255) = 20078, then floor(x 20%) = 4015.
	// cells 2/3: white 32896, then 6579.
	r.selectNames(t, "L1", "B1", "P1")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, nil)
	wantSlots(t, "B1 real dimmer lowlit 10000", r.wire(t, 0), 40, 0x27, 0x10)
	f = r.wire(t, 1)
	wantSlots(t, "P1 cell 1 red scaled then lowlit", f, 101, byte(4015>>8), byte(4015&0xFF), 0, 0, 0, 0, 0, 0)
	wantSlots(t, "P1 cell 2 white scaled then lowlit", f, 109, byte(6579>>8), byte(6579&0xFF), byte(6579>>8), byte(6579&0xFF), byte(6579>>8), byte(6579&0xFF), byte(6579>>8), byte(6579&0xFF))
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)

	// Locate S1 (no colour defaults): dimmer full, white.
	r.selectNames(t, "S1")
	r.post(t, "/api/programmer/locate", map[string]any{}, nil)
	wantSlots(t, "S1 locate: white full", r.wire(t, 2), 1, 255, 255, 255)
}

// TestVirtualDimmerDisarmedNothingOnWire: the arm gate covers it.
func TestVirtualDimmerDisarmedNothingOnWire(t *testing.T) {
	r := newG1Rig(t)
	r.set(t, map[string]any{"targets": []any{r.target("P1"), r.target("S1")}, "attribute": "Dimmer", "fraction": 1})
	r.h.clock.Advance(200 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Fatalf("%d ArtDmx frames while disarmed", n)
	}
	r.arm(t)
	p1Cells(t, "armed: the stored virtual dimmer, white full", r.wire(t, 1), 65535, 65535, 65535, 65535)
}
