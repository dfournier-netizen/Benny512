package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Console-lite I2d — server halves of the action bar (component-specs §15),
// on the C4a real-profile rig (BMFL B1/B2 on universe 0, LEDBeam L1 on
// universe 1), wire decoded at FakeTransport.

type i2dView struct {
	Highlight struct {
		On   bool
		Step *int // null = the whole selection is highlighted
	}
}

func (r *c4aRig) hl(t *testing.T, body map[string]any) (int, i2dView) {
	t.Helper()
	var v i2dView
	rr := r.do(t, "POST", "/api/programmer/highlight", body, nil)
	if rr.Code == http.StatusOK {
		_ = json.Unmarshal(rr.Body.Bytes(), &v)
	}
	return rr.Code, v
}

// TestHighlightStepsThroughSelectionOrder: §15 "Highlight ... exposes
// Previous/Next that follow selection order (non-wrapping)". A step
// highlights ONE member of the selection, in selection order; the others
// are lowlit like any non-highlighted fixture; Next at the last member stays
// there; "all" goes back to the whole selection; a selection change ends
// stepping.
func TestHighlightStepsThroughSelectionOrder(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("B1"), r.target("B2")}, "attribute": "Dimmer", "dmx": 50000})
	r.selectNames(t, "B1", "B2", "L1")

	if code, _ := r.hl(t, map[string]any{"step": "next"}); code != http.StatusBadRequest {
		t.Errorf("stepping with Highlight off: %d, want 400", code)
	}
	if code, v := r.hl(t, map[string]any{"highlight": true, "lowlight": true}); code != http.StatusOK || v.Highlight.Step != nil {
		t.Fatalf("highlight on: %d step %v (want the whole selection, null)", code, v.Highlight.Step)
	}
	f := r.wire(t, 0)
	wantSlots(t, "whole selection: B1 full", f, 40, 255, 255)
	wantSlots(t, "whole selection: B2 full", f, 81, 255, 255)

	step := func(dir string, want int) {
		t.Helper()
		code, v := r.hl(t, map[string]any{"step": dir})
		if code != http.StatusOK || v.Highlight.Step == nil || *v.Highlight.Step != want {
			got := "null"
			if v.Highlight.Step != nil {
				got = fmt.Sprint(*v.Highlight.Step)
			}
			t.Fatalf("step %s: %d, step %s (want %d)", dir, code, got, want)
		}
	}
	step("next", 0)
	f = r.wire(t, 0)
	wantSlots(t, "step 1 of 3: B1 highlighted", f, 40, 255, 255)
	wantSlots(t, "step 1 of 3: B2 lowlit (50000 -> 10000)", f, 81, 0x27, 0x10)
	step("next", 1)
	f = r.wire(t, 0)
	wantSlots(t, "step 2 of 3: B1 lowlit", f, 40, 0x27, 0x10)
	wantSlots(t, "step 2 of 3: B2 highlighted", f, 81, 255, 255)
	step("previous", 0)
	step("next", 1)
	step("next", 2)
	step("next", 2) // non-wrapping: the last stays the last
	f = r.wire(t, 0)
	wantSlots(t, "step 3 of 3 (L1): B1 lowlit", f, 40, 0x27, 0x10)
	wantSlots(t, "step 3 of 3 (L1): B2 lowlit", f, 81, 0x27, 0x10)
	wantSlots(t, "step 3 of 3: L1 dimmer highlight", r.wire(t, 1), 14, 255)

	if code, v := r.hl(t, map[string]any{"step": "all"}); code != http.StatusOK || v.Highlight.Step != nil {
		t.Errorf("step all: %d step %v (want null)", code, v.Highlight.Step)
	}
	wantSlots(t, "back to the whole selection: B1 full", r.wire(t, 0), 40, 255, 255)

	step("previous", 2) // from the whole selection, Previous starts at the last
	r.selectNames(t, "B2", "B1")
	if v := r.view4b(t); !v.Highlight.On {
		t.Errorf("a selection change turned Highlight off")
	}
	_, v := r.hl(t, map[string]any{"lowlightPercent": 20})
	if v.Highlight.Step != nil {
		t.Errorf("a selection change kept stepping at %d; it must go back to the whole selection", *v.Highlight.Step)
	}
	if code, _ := r.hl(t, map[string]any{"step": "sideways"}); code != http.StatusBadRequest {
		t.Errorf("unknown step: %d, want 400", code)
	}
	r.hl(t, map[string]any{"step": "next"})
	r.hl(t, map[string]any{"highlight": false})
	r.hl(t, map[string]any{"highlight": true})
	if _, v := r.hl(t, map[string]any{"lowlightPercent": 20}); v.Highlight.Step != nil {
		t.Errorf("Highlight off and on again kept stepping")
	}
}

// TestFanEdgesIn: §15 lists four fan shapes; edges-in "requires a server
// mapping/extension, never silently reinterpret it as linear". It is the
// mirror image of mirror (centre out): both ends = From, centre = To.
func TestFanEdgesIn(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "B2", "L1")
	r.post(t, "/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": "edges-in",
		"from": map[string]any{"fraction": 0}, "to": map[string]any{"fraction": 1}}, nil)
	u0, u1 := r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "edges-in B1 (end = From)", u0, 40, 0, 0)
	wantSlots(t, "edges-in B2 (centre = To)", u0, 81, 255, 255)
	wantSlots(t, "edges-in L1 (end = From)", u1, 14, 0)
	r.selectNames(t, "B1", "B2")
	rr := r.do(t, "POST", "/api/programmer/fan", map[string]any{"attribute": "Dimmer", "shape": "edges-in",
		"from": map[string]any{"fraction": 0}, "to": map[string]any{"fraction": 1}}, nil)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "at least 3") {
		t.Errorf("edges-in over 2 fixtures: %d %s", rr.Code, rr.Body.String())
	}
}

// TestPresetAppliesToSelection: §14 "Preset button: family glyph + name +
// `applies to 6 of 8 selected` if partial". Each preset in the programmer
// view says how many of the CURRENT selection's targets a recall would set
// something on, by recall's own rules (exact entry, else same type+mode).
func TestPresetAppliesToSelection(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "L1")
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Color1", "slot": 2})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "ColorAdd_G", "dmx": 12})
	var v struct {
		Presets []struct {
			ID      string
			Applies *int
		}
	}
	r.post(t, "/api/programmer/presets/store", map[string]any{"name": "Red-ish", "family": "colour"}, &v)
	r.selectNames(t, "B2", "L1", "P1")
	r.post(t, "/api/programmer/highlight", map[string]any{"lowlightPercent": 20}, &v)
	if len(v.Presets) != 1 || v.Presets[0].Applies == nil || *v.Presets[0].Applies != 2 {
		t.Fatalf("B2 (by type) + L1 (exact) of B2, L1, P1: applies %+v, want 2", v.Presets)
	}
	r.selectNames(t, "P1")
	r.post(t, "/api/programmer/highlight", map[string]any{"lowlightPercent": 20}, &v)
	if v.Presets[0].Applies == nil || *v.Presets[0].Applies != 0 {
		t.Fatalf("P1 only: applies %v, want 0 (present, not absent)", v.Presets[0].Applies)
	}
}

// TestGroupMerge: §14 "group storage can also Merge" — the selection's
// targets are added to a stored group after its own members, in selection
// order, without duplicates.
func TestGroupMerge(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1", "B2")
	var v c4bView
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Front"}, &v)
	id := v.StoredGroups[0].ID
	r.selectNames(t, "B2", "L1")
	r.post(t, "/api/programmer/groups/merge", map[string]any{"id": id}, &v)
	got := []string{}
	for _, m := range v.StoredGroups[0].Members {
		got = append(got, m.EntryID)
	}
	want := []string{r.ids["B1"], r.ids["B2"], r.ids["L1"]}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("merged members %v, want B1, B2, L1 %v", got, want)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "none"}, nil)
	if rr := r.do(t, "POST", "/api/programmer/groups/merge", map[string]any{"id": id}, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("merge with nothing selected: %d", rr.Code)
	}
	r.selectNames(t, "B1")
	if rr := r.do(t, "POST", "/api/programmer/groups/merge", map[string]any{"id": "nope"}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("merge into an unknown group: %d", rr.Code)
	}
}
