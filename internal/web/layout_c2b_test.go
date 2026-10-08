package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// Console-lite C2b: auto/manual placement modes, the system Unplaced layer
// that keeps every fixture selectable, and layout objects (truss, label,
// area, mark). Everything goes through the HTTP API as generic JSON; the
// migration test loads a show file written by the C2 build itself.

const unplacedLayer = "unplaced"

// layoutItems returns GET's layout.items as maps.
func layoutItems(body map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	for _, it := range asList(asMap(body["layout"])["items"]) {
		out = append(out, asMap(it))
	}
	return out
}

// entryItem returns the one item placing entry id, failing on none or two.
func entryItem(t *testing.T, body map[string]any, id string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, it := range layoutItems(body) {
		if it["kind"] == "entry" && it["ref"] == id {
			if found != nil {
				t.Fatalf("entry %s has two items: %v and %v", id, found, it)
			}
			found = it
		}
	}
	if found == nil {
		t.Fatalf("entry %s has no item on the layout (every fixture must stay selectable)", id)
	}
	return found
}

func itemByID(body map[string]any, id string) map[string]any {
	for _, it := range layoutItems(body) {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

func unplacedReasons(body map[string]any) map[string]string {
	out := map[string]string{}
	for _, u := range asList(body["unplaced"]) {
		um := asMap(u)
		out[fmt.Sprint(um["entryId"])] = fmt.Sprint(um["reason"])
	}
	return out
}

// setLocation changes one entry's stored location through PUT
// /api/patch/entries/{id}, the edit path the UI uses. loc nil = unknown.
func setLocation(t *testing.T, h *testHarness, body map[string]any, num string, loc map[string]any) {
	t.Helper()
	var e map[string]any
	for _, x := range asList(body["entries"]) {
		if fmt.Sprint(asMap(x)["fixtureNumber"]) == num {
			e = asMap(x)
		}
	}
	if e == nil {
		t.Fatalf("no entry %s", num)
	}
	if loc == nil {
		loc = map[string]any{"known": false}
	}
	req := map[string]any{"name": e["name"], "fixtureType": e["fixtureType"], "mode": e["mode"], "footprint": e["footprint"],
		"universe": e["universe"], "startAddress": e["startAddress"], "fixtureNumber": e["fixtureNumber"], "location": loc}
	if rr := doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+e["id"].(string), req); rr.Code != http.StatusOK {
		t.Fatalf("PUT entry %s: %d %s", num, rr.Code, rr.Body.String())
	}
}

func shiftedLocation(body map[string]any, num string, dx float64) map[string]any {
	for _, x := range asList(body["entries"]) {
		if fmt.Sprint(asMap(x)["fixtureNumber"]) == num {
			l := asMap(asMap(x)["location"])
			out := map[string]any{}
			for k, v := range l {
				out[k] = v
			}
			out["x"] = l["x"].(float64) + dx
			return out
		}
	}
	return nil
}

func movedEntries(res map[string]any) []string {
	out := make([]string, 0)
	for _, m := range asList(res["moved"]) {
		out = append(out, fmt.Sprint(asMap(m)["entryId"]))
	}
	return out
}

// TestLayoutC2b_MigratesC2LayoutToManual loads a show file the C2 build
// wrote (testdata/c2_layout_show.json: capture extract + "Desk Par" with no
// position, 12 placed by hand on "Manual", the rest derived at 2000/800).
// C2 did not record which placements were derived, so every one is kept as
// manual and the layout says how many were converted.
func TestLayoutC2b_MigratesC2LayoutToManual(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "c2_layout_show.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "benny512-patch.json")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	h.srv.SetPatchStorePath(path)

	body := layoutGet(t, h)
	l := asMap(body["layout"])
	if l["schema"] != 2.0 || l["migratedManual"] != 9.0 {
		t.Errorf("migrated layout header: schema %v migratedManual %v, want 2 and 9", l["schema"], l["migratedManual"])
	}
	for _, it := range layoutItems(body) {
		if it["layer"] == unplacedLayer {
			continue
		}
		if it["mode"] != "manual" {
			t.Errorf("C2 item %v: mode %v, want manual (C2 never recorded which were derived)", it["id"], it["mode"])
		}
	}
	got := placementsByNumber(t, body)
	if got["12"] != "Manual 5,5" || got["22"] != captureDerived2000["22"] {
		t.Errorf("migration moved items: 12 %q 22 %q", got["12"], got["22"])
	}
	// Desk Par (no position) is on the system Unplaced layer.
	desk := entryIDByNumber(t, body, "99")
	if it := entryItem(t, body, desk); it["layer"] != unplacedLayer || it["mode"] != "auto" || it["col"] != 0.0 || it["row"] != 0.0 {
		t.Errorf("Desk Par item = %v, want auto on %s at 0,0", it, unplacedLayer)
	}
	if r := unplacedReasons(body); len(r) != 1 || r[desk] != "no-location" {
		t.Errorf("unplaced = %v, want only Desk Par / no-location", body["unplaced"])
	}
	// GET never writes the show file.
	if now, _ := os.ReadFile(path); !bytes.Equal(now, src) {
		t.Error("GET /api/patch/layout rewrote the show file")
	}

	// Refresh moves nothing: everything C2 placed is manual now.
	res := asMap(mustLayoutPost(t, h, "refresh", `{}`)["result"])
	if len(asList(res["placed"])) != 0 || len(asList(res["moved"])) != 0 {
		t.Errorf("refresh of a migrated layout placed %v moved %v, want nothing", res["placed"], res["moved"])
	}
	var stored struct {
		Workspace struct {
			Layout struct {
				Schema         int `json:"schema"`
				MigratedManual int `json:"migratedManual"`
				Items          []struct {
					Mode string `json:"mode"`
				} `json:"items"`
			} `json:"layout"`
		} `json:"workspace"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Workspace.Layout.Schema != 2 || stored.Workspace.Layout.MigratedManual != 9 || len(stored.Workspace.Layout.Items) != 9 {
		t.Errorf("written layout: schema %d migratedManual %d items %d, want 2/9/9 (auto Unplaced items are never stored)",
			stored.Workspace.Layout.Schema, stored.Workspace.Layout.MigratedManual, len(stored.Workspace.Layout.Items))
	}
	for _, it := range stored.Workspace.Layout.Items {
		if it.Mode != "manual" {
			t.Errorf("written item mode %q, want manual", it.Mode)
		}
	}

	// Reset all to auto must be confirmed; once done, derive owns them and
	// 12 joins its derived layer.
	if rr := layoutPost(t, h, "reset-auto", `{"all":true}`); rr.Code != http.StatusBadRequest {
		t.Errorf("reset-auto all without confirm: %d %s, want 400", rr.Code, rr.Body.String())
	}
	mustLayoutPost(t, h, "reset-auto", `{"all":true,"confirm":"RESET"}`)
	body = layoutGet(t, h)
	got = placementsByNumber(t, body)
	for num, want := range captureDerived2000 {
		if got[num] != want {
			t.Errorf("after reset-auto all: %s at %q, want %q", num, got[num], want)
		}
		if it := entryItem(t, body, entryIDByNumber(t, body, num)); it["mode"] != "auto" {
			t.Errorf("after reset-auto all: %s mode %v", num, it["mode"])
		}
	}
}

// TestLayoutC2b_AutoVsManualThroughDeriveAndRefresh: derive and refresh
// create and update only auto placements; any move makes an item manual and
// it is never moved again until reset to auto; pin makes it manual in place.
func TestLayoutC2b_AutoVsManualThroughDeriveAndRefresh(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	if rr := layoutPost(t, h, "refresh", `{}`); rr.Code != http.StatusConflict {
		t.Errorf("refresh before any derive: %d %s, want 409", rr.Code, rr.Body.String())
	}
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	body := layoutGet(t, h)
	for _, it := range layoutItems(body) {
		if it["kind"] == "entry" && it["mode"] != "auto" {
			t.Errorf("derived item %v mode %v, want auto", it["id"], it["mode"])
		}
	}
	e16, e21, e22, e47 := entryIDByNumber(t, body, "16"), entryIDByNumber(t, body, "21"), entryIDByNumber(t, body, "22"), entryIDByNumber(t, body, "47")
	id16 := entryItem(t, body, e16)["id"].(string)
	id21 := entryItem(t, body, e21)["id"].(string)

	// Moving 21 (auto) makes it manual.
	out := mustLayoutPost(t, h, "move", `{"id":"`+id21+`","col":10,"row":10}`)
	if it := entryItem(t, out, e21); it["mode"] != "manual" || it["col"] != 10.0 {
		t.Errorf("moved 21 = %v, want manual at 10,10", it)
	}
	// 16 and 21 move 4 m stage-left in the MVR; refresh follows 16 (auto)
	// but not 21 (manual), and 22 — displaced by 21 at derive — now gets
	// its own exact cell 2,1.
	setLocation(t, h, body, "16", shiftedLocation(body, "16", 4000))
	setLocation(t, h, body, "21", shiftedLocation(body, "21", 4000))
	res := asMap(mustLayoutPost(t, h, "refresh", `{}`)["result"])
	if m := movedEntries(res); len(m) != 2 || !contains(m, e16) || !contains(m, e22) {
		t.Errorf("refresh moved %v, want 16 and 22", res["moved"])
	}
	got := placementsByNumber(t, layoutGet(t, h))
	if got["16"] != "Height 5565–5857 mm 9,1" || got["21"] != "Height 1053 mm 10,10" || got["22"] != "Height 1053 mm 2,1" {
		t.Errorf("after refresh: 16 %q 21 %q 22 %q", got["16"], got["21"], got["22"])
	}
	if it := entryItem(t, layoutGet(t, h), e16); it["id"] != id16 || it["mode"] != "auto" {
		t.Errorf("refresh changed 16's item id or mode: %v (id was %s)", it, id16)
	}

	// Pin 16: manual, same cell; a refresh after its position changes
	// again leaves it alone.
	out = mustLayoutPost(t, h, "pin", `{"id":"`+id16+`"}`)
	if it := entryItem(t, out, e16); it["mode"] != "manual" || it["col"] != 9.0 || it["row"] != 1.0 {
		t.Errorf("pinned 16 = %v, want manual at 9,1", it)
	}
	body = layoutGet(t, h)
	setLocation(t, h, body, "16", shiftedLocation(body, "16", -4000))
	res = asMap(mustLayoutPost(t, h, "derive", `{}`)["result"])
	if m := movedEntries(res); len(m) != 0 {
		t.Errorf("derive moved %v with every changed fixture manual", m)
	}
	if got := placementsByNumber(t, layoutGet(t, h)); got["16"] != "Height 5565–5857 mm 9,1" {
		t.Errorf("derive moved pinned 16 to %q", got["16"])
	}
	// Reset 16 to auto: placed again from its position, same item id.
	out = mustLayoutPost(t, h, "reset-auto", `{"id":"`+id16+`"}`)
	if it := entryItem(t, out, e16); it["mode"] != "auto" || it["id"] != id16 || it["col"] != 7.0 || it["row"] != 1.0 {
		t.Errorf("reset 16 = %v, want auto %s at 7,1", it, id16)
	}

	// An auto fixture that loses its position goes back to Unplaced on
	// refresh; a manual one that loses it stays put until reset.
	body = layoutGet(t, h)
	setLocation(t, h, body, "47", nil)
	setLocation(t, h, body, "21", nil)
	res = asMap(mustLayoutPost(t, h, "refresh", `{}`)["result"])
	if r := asList(res["returnedToUnplaced"]); len(r) != 1 || fmt.Sprint(r[0]) != e47 {
		t.Errorf("returnedToUnplaced = %v, want [47]", res["returnedToUnplaced"])
	}
	body = layoutGet(t, h)
	if it := entryItem(t, body, e47); it["layer"] != unplacedLayer || it["mode"] != "auto" {
		t.Errorf("47 after losing its position: %v", it)
	}
	if r := unplacedReasons(body); r[e47] != "no-location" || r[e21] != "" {
		t.Errorf("unplaced = %v, want 47 no-location and manual 21 not listed", body["unplaced"])
	}
	out = mustLayoutPost(t, h, "reset-auto", `{"id":"`+id21+`"}`)
	if it := entryItem(t, out, e21); it["layer"] != unplacedLayer || it["mode"] != "auto" {
		t.Errorf("21 reset with no position: %v, want auto on Unplaced", it)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// TestLayoutC2b_UnplacedLayerKeepsEveryFixtureSelectable: every entry with
// no other placement sits in the system Unplaced layer in a tidy grid (16
// columns, patch order), kept current on GET as entries come and go, with
// the show file untouched by GET.
func TestLayoutC2b_UnplacedLayerKeepsEveryFixtureSelectable(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "benny512-patch.json")
	h.srv.SetPatchStorePath(path)
	ids := make([]string, 0)
	for i := 1; i <= 20; i++ {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", map[string]any{"name": fmt.Sprintf("F%d", i), "fixtureNumber": fmt.Sprint(i), "startAddress": i * 10, "footprint": 4})
		if rr.Code != http.StatusOK {
			t.Fatal(rr.Body.String())
		}
	}
	before, _ := os.ReadFile(path)
	body := layoutGet(t, h)
	again := layoutGet(t, h)
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("GET /api/patch/layout wrote the show file")
	}
	layers := asList(asMap(body["layout"])["layers"])
	if len(layers) != 1 || asMap(layers[0])["id"] != unplacedLayer || asMap(layers[0])["system"] != true || asMap(layers[0])["name"] != "Unplaced" {
		t.Fatalf("layers = %v, want the system Unplaced layer", layers)
	}
	for _, e := range asList(body["entries"]) {
		ids = append(ids, asMap(e)["id"].(string))
	}
	checkGrid := func(body map[string]any, order []string) {
		t.Helper()
		for k, id := range order {
			it := entryItem(t, body, id)
			if it["layer"] != unplacedLayer || it["mode"] != "auto" || it["col"] != float64(k%16) || it["row"] != float64(k/16) || it["w"] != 1.0 || it["h"] != 1.0 {
				t.Errorf("unplaced #%d = %v, want auto at %d,%d", k, it, k%16, k/16)
			}
		}
	}
	checkGrid(body, ids)
	for i, u := range asList(body["unplaced"]) {
		um := asMap(u)
		if um["entryId"] != ids[i] || um["reason"] != "no-location" || um["itemId"] != entryItem(t, body, ids[i])["id"] {
			t.Errorf("unplaced[%d] = %v", i, um)
		}
	}
	for _, e := range asList(body["entries"]) {
		em := asMap(e)
		if em["placed"] != false || em["itemId"] != entryItem(t, body, em["id"].(string))["id"] {
			t.Errorf("entry view %v: placed %v itemId %v", em["id"], em["placed"], em["itemId"])
		}
	}
	for _, id := range ids {
		if entryItem(t, body, id)["id"] != entryItem(t, again, id)["id"] {
			t.Errorf("item id for %s changed between two GETs", id)
		}
	}

	// Place F5 elsewhere by hand: the grid re-packs in patch order.
	mustLayoutPost(t, h, "layer-create", `{"name":"Rig"}`)
	rig := layerIDByName(t, layoutGet(t, h), "Rig")
	mustLayoutPost(t, h, "place", `{"kind":"entry","ref":"`+ids[4]+`","layer":"`+rig+`","col":0,"row":0}`)
	body = layoutGet(t, h)
	rest := append(append([]string{}, ids[:4]...), ids[5:]...)
	checkGrid(body, rest)
	if r := unplacedReasons(body); r[ids[4]] != "" || len(r) != 19 {
		t.Errorf("unplaced after placing F5: %v", body["unplaced"])
	}
	// Move F1's auto item: it becomes manual where it was dropped, keeps
	// its id, and the cell it took is skipped by the grid.
	f1 := entryItem(t, body, ids[0])["id"].(string)
	out := mustLayoutPost(t, h, "move", `{"id":"`+f1+`","col":1,"row":0}`)
	if it := entryItem(t, out, ids[0]); it["id"] != f1 || it["mode"] != "manual" || it["layer"] != unplacedLayer || it["col"] != 1.0 {
		t.Errorf("moved F1 = %v", it)
	}
	if it := entryItem(t, out, ids[1]); it["col"] != 0.0 || it["row"] != 0.0 {
		t.Errorf("F2 = %v, want 0,0", it)
	}
	if it := entryItem(t, out, ids[2]); it["col"] != 2.0 {
		t.Errorf("F3 = %v, want col 2 (col 1 is F1's)", it)
	}
	// A virtual item cannot be removed: it IS the fallback.
	f2 := entryItem(t, out, ids[1])["id"].(string)
	if rr := layoutPost(t, h, "remove", `{"id":"`+f2+`"}`); rr.Code != http.StatusConflict {
		t.Errorf("remove an Unplaced auto item: %d %s, want 409", rr.Code, rr.Body.String())
	}
	// Pin F2 in place.
	out = mustLayoutPost(t, h, "pin", `{"id":"`+f2+`"}`)
	if it := entryItem(t, out, ids[1]); it["mode"] != "manual" || it["col"] != 0.0 || it["row"] != 0.0 || it["id"] != f2 {
		t.Errorf("pinned F2 = %v", it)
	}

	// A new entry appears at the end of the grid; a deleted one leaves it.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", map[string]any{"name": "F21", "fixtureNumber": "21", "startAddress": 400, "footprint": 4})
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	body = layoutGet(t, h)
	f21 := entryIDByNumber(t, body, "21")
	// Auto in patch order: F3,F4,F6..F20,F21 = 18 items, cells 0,0 and 1,0
	// taken by F2 and F1 → F21 is the 18th free cell, index 19 → 3,1.
	if it := entryItem(t, body, f21); it["col"] != 3.0 || it["row"] != 1.0 || it["layer"] != unplacedLayer {
		t.Errorf("new entry F21 = %v, want Unplaced 3,1", it)
	}
	if rr := doJSON(t, h.srv.Handler(), "DELETE", "/api/patch/entries/"+ids[2], nil); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	body = layoutGet(t, h)
	if it := entryItem(t, body, ids[3]); it["col"] != 2.0 || it["row"] != 0.0 {
		t.Errorf("F4 after deleting F3 = %v, want 2,0", it)
	}
	if it := entryItem(t, body, f21); it["col"] != 2.0 || it["row"] != 1.0 {
		t.Errorf("F21 after deleting F3 = %v, want 2,1", it)
	}

	// The Unplaced layer cannot be deleted; it can be renamed (identity is
	// its id and system flag); it takes part in reorder.
	if rr := layoutPost(t, h, "layer-delete", `{"id":"unplaced","removeItems":true}`); rr.Code != http.StatusConflict {
		t.Errorf("delete Unplaced: %d %s, want 409", rr.Code, rr.Body.String())
	}
	mustLayoutPost(t, h, "layer-rename", `{"id":"unplaced","name":"Spares"}`)
	mustLayoutPost(t, h, "layer-reorder", `{"order":["unplaced","`+rig+`"]}`)
	layers = asList(asMap(layoutGet(t, h)["layout"])["layers"])
	if asMap(layers[0])["id"] != unplacedLayer || asMap(layers[0])["name"] != "Spares" || asMap(layers[0])["system"] != true {
		t.Errorf("layers after rename/reorder = %v", layers)
	}
	// New layers go in just before the Unplaced layer.
	mustLayoutPost(t, h, "layer-reorder", `{"order":["`+rig+`","unplaced"]}`)
	mustLayoutPost(t, h, "layer-create", `{"name":"Later"}`)
	layers = asList(asMap(layoutGet(t, h)["layout"])["layers"])
	if len(layers) != 3 || asMap(layers[1])["name"] != "Later" || asMap(layers[2])["id"] != unplacedLayer {
		t.Errorf("layer-create order = %v, want Rig, Later, Unplaced", layers)
	}
}

// TestLayoutC2b_UnplacedReasons: located fixtures not yet derived are
// "not-derived"; fixtures with no position are "no-location".
func TestLayoutC2b_UnplacedReasons(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	if rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Desk Par","fixtureNumber":"99","startAddress":400,"footprint":4}`)); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	body := layoutGet(t, h)
	r := unplacedReasons(body)
	if len(r) != 10 {
		t.Fatalf("unplaced = %v, want all 10", body["unplaced"])
	}
	for num := range captureDerived2000 {
		if r[entryIDByNumber(t, body, num)] != "not-derived" {
			t.Errorf("%s reason %q, want not-derived", num, r[entryIDByNumber(t, body, num)])
		}
	}
	desk := entryIDByNumber(t, body, "99")
	if r[desk] != "no-location" {
		t.Errorf("Desk Par reason %q", r[desk])
	}
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	body = layoutGet(t, h)
	if r := unplacedReasons(body); len(r) != 1 || r[desk] != "no-location" {
		t.Errorf("after derive unplaced = %v", body["unplaced"])
	}
	// Desk Par is first in the Unplaced grid.
	if it := entryItem(t, body, desk); it["layer"] != unplacedLayer || it["col"] != 0.0 || it["row"] != 0.0 {
		t.Errorf("Desk Par item = %v", it)
	}
}

// TestLayoutC2b_Objects: CRUD, geometry in half cells, covering cells,
// selection flags, z-order, duplicate and validation.
func TestLayoutC2b_Objects(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "layer-create", `{"name":"Stage"}`)
	body := layoutGet(t, h)
	stage := layerIDByName(t, body, "Stage")

	types := map[string]map[string]any{}
	for _, ot := range asList(body["objectTypes"]) {
		types[asMap(ot)["type"].(string)] = asMap(ot)
	}
	for typ, shape := range map[string]string{"truss": "line", "label": "point", "area": "rect", "mark": "line"} {
		if types[typ]["shape"] != shape {
			t.Errorf("objectTypes[%s] = %v, want shape %s", typ, types[typ], shape)
		}
	}
	if types["label"]["textRequired"] != true || types["label"]["rotatable"] != true || types["truss"]["rotatable"] != false {
		t.Errorf("objectTypes flags: %v", body["objectTypes"])
	}

	create := func(js string) string {
		t.Helper()
		out := mustLayoutPost(t, h, "object-create", js)
		return asMap(out["result"])["id"].(string)
	}
	truss := create(`{"objectType":"truss","layer":"` + stage + `","text":"FOH truss","geometry":{"col":0,"row":2.5,"col2":12,"row2":2.5}}`)
	area := create(`{"objectType":"area","layer":"` + stage + `","text":"Stage","geometry":{"col":-2,"row":0,"w":16.5,"h":8}}`)
	label := create(`{"objectType":"label","layer":"` + stage + `","text":"DSC","rot":90,"geometry":{"col":6.5,"row":9}}`)
	mark := create(`{"objectType":"mark","layer":"` + stage + `","text":"CL","geometry":{"col":6,"row":-1,"col2":6,"row2":10}}`)
	body = layoutGet(t, h)
	want := map[string]struct {
		col, row, w, h, rot float64
		text                string
	}{
		truss: {0, 2, 12, 1, 0, "FOH truss"},
		area:  {-2, 0, 17, 8, 0, "Stage"},
		label: {6, 9, 1, 1, 90, "DSC"},
		mark:  {6, -1, 1, 11, 0, "CL"},
	}
	for id, w := range want {
		it := itemByID(body, id)
		if it == nil {
			t.Fatalf("object %s missing", id)
		}
		if it["kind"] != "object" || it["mode"] != "manual" || it["ref"] != "" || it["layer"] != stage || it["text"] != w.text ||
			it["col"] != w.col || it["row"] != w.row || it["w"] != w.w || it["h"] != w.h || it["rot"] != w.rot {
			t.Errorf("object %v, want covering cells %v", it, w)
		}
	}
	if g := asMap(itemByID(body, truss)["geometry"]); g["col"] != 0.0 || g["row"] != 2.5 || g["col2"] != 12.0 || g["row2"] != 2.5 || g["w"] != nil {
		t.Errorf("truss geometry = %v", g)
	}
	if g := asMap(itemByID(body, area)["geometry"]); g["w"] != 16.5 || g["h"] != 8.0 || g["col2"] != nil {
		t.Errorf("area geometry = %v", g)
	}
	// Objects are not fixtures: no unplaced entry, no entry view, and they
	// never block a fixture's cell.
	if len(asList(body["entries"])) != 9 {
		t.Errorf("objects leaked into entries")
	}
	e11 := entryIDByNumber(t, body, "11")
	mustLayoutPost(t, h, "place", `{"kind":"entry","ref":"`+e11+`","layer":"`+stage+`","col":3,"row":2}`)

	// z-order within the layer: created order, then front/back/forward/backward.
	orders := func() map[string]float64 {
		out := map[string]float64{}
		for _, it := range layoutItems(layoutGet(t, h)) {
			if it["layer"] == stage {
				out[it["id"].(string)] = it["order"].(float64)
			}
		}
		return out
	}
	fx := entryItem(t, layoutGet(t, h), e11)["id"].(string)
	if o := orders(); o[truss] != 0 || o[area] != 1 || o[label] != 2 || o[mark] != 3 || o[fx] != 4 {
		t.Errorf("creation order = %v", o)
	}
	mustLayoutPost(t, h, "item-order", `{"id":"`+area+`","to":"back"}`)
	mustLayoutPost(t, h, "item-order", `{"id":"`+truss+`","to":"front"}`)
	if o := orders(); o[area] != 0 || o[label] != 1 || o[mark] != 2 || o[fx] != 3 || o[truss] != 4 {
		t.Errorf("after back/front = %v", o)
	}
	mustLayoutPost(t, h, "item-order", `{"id":"`+label+`","to":"forward"}`)
	mustLayoutPost(t, h, "item-order", `{"id":"`+fx+`","to":"backward"}`)
	if o := orders(); o[area] != 0 || o[mark] != 1 || o[fx] != 2 || o[label] != 3 || o[truss] != 4 {
		t.Errorf("after forward/backward = %v", o)
	}

	// Update: text, geometry, rotation, layer.
	mustLayoutPost(t, h, "object-update", `{"id":"`+label+`","text":"Down stage centre","rot":180,"geometry":{"col":7,"row":9.5}}`)
	it := itemByID(layoutGet(t, h), label)
	if it["text"] != "Down stage centre" || it["rot"] != 180.0 || asMap(it["geometry"])["col"] != 7.0 || it["row"] != 9.0 {
		t.Errorf("updated label = %v", it)
	}
	mustLayoutPost(t, h, "object-update", `{"id":"`+mark+`","layer":"unplaced"}`)
	if it := itemByID(layoutGet(t, h), mark); it["layer"] != unplacedLayer {
		t.Errorf("mark layer change = %v", it)
	}
	// Duplicate (default offset one cell right and down) then delete.
	dup := asMap(mustLayoutPost(t, h, "object-duplicate", `{"id":"`+truss+`"}`)["result"])["id"].(string)
	if g := asMap(itemByID(layoutGet(t, h), dup)["geometry"]); g["col"] != 1.0 || g["row"] != 3.5 || g["col2"] != 13.0 || g["row2"] != 3.5 {
		t.Errorf("duplicate geometry = %v", g)
	}
	dup2 := asMap(mustLayoutPost(t, h, "object-duplicate", `{"id":"`+truss+`","dCol":0,"dRow":-0.5}`)["result"])["id"].(string)
	if g := asMap(itemByID(layoutGet(t, h), dup2)["geometry"]); g["row"] != 2.0 || itemByID(layoutGet(t, h), dup2)["text"] != "FOH truss" {
		t.Errorf("duplicate with offset = %v", itemByID(layoutGet(t, h), dup2))
	}
	mustLayoutPost(t, h, "object-delete", `{"id":"`+dup2+`"}`)
	if itemByID(layoutGet(t, h), dup2) != nil {
		t.Error("object-delete left the object")
	}

	unplacedItem := ""
	for _, it := range layoutItems(layoutGet(t, h)) {
		if it["layer"] == unplacedLayer && it["kind"] == "entry" {
			unplacedItem = it["id"].(string)
			break
		}
	}
	cases := []struct {
		action, body string
		code         int
	}{
		{"object-create", `{"objectType":"speaker","layer":"` + stage + `","geometry":{"col":0,"row":0}}`, 400},
		{"object-create", `{"objectType":"label","layer":"` + stage + `","geometry":{"col":0,"row":0}}`, 400},
		{"object-create", `{"objectType":"label","layer":"` + stage + `","text":"` + strings.Repeat("x", 81) + `","geometry":{"col":0,"row":0}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","geometry":{"col":0.25,"row":0,"col2":4,"row2":0}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","geometry":{"col":0,"row":0,"col2":1000,"row2":0}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","geometry":{"col":3,"row":3,"col2":3,"row2":3}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","geometry":{"col":0,"row":0,"col2":4,"row2":0,"w":1}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","geometry":{"col":0,"row":0}}`, 400},
		{"object-create", `{"objectType":"truss","layer":"` + stage + `","rot":90,"geometry":{"col":0,"row":0,"col2":4,"row2":0}}`, 400},
		{"object-create", `{"objectType":"area","layer":"` + stage + `","geometry":{"col":0,"row":0,"w":4}}`, 400},
		{"object-create", `{"objectType":"area","layer":"` + stage + `","geometry":{"col":0,"row":0,"w":0,"h":2}}`, 400},
		{"object-create", `{"objectType":"area","layer":"` + stage + `","geometry":{"col":0,"row":0,"w":2000,"h":2}}`, 400},
		{"object-create", `{"objectType":"area","layer":"` + stage + `","geometry":{"col":0,"row":0,"w":2,"h":2,"col2":3}}`, 400},
		{"object-create", `{"objectType":"label","layer":"` + stage + `","text":"x","rot":45,"geometry":{"col":0,"row":0}}`, 400},
		{"object-create", `{"objectType":"label","layer":"` + stage + `","text":"x","geometry":{"col":0,"row":0,"w":1,"h":1}}`, 400},
		{"object-create", `{"objectType":"label","layer":"nope","text":"x","geometry":{"col":0,"row":0}}`, 404},
		{"object-create", `{"objectType":"label","layer":"` + stage + `","text":"x","geometry":{"col":0,"row":0},"colour":"red"}`, 400},
		{"object-update", `{"id":"nope","text":"x"}`, 404},
		{"object-update", `{"id":"` + fx + `","text":"x"}`, 400},
		{"object-update", `{"id":"` + truss + `","objectType":"area"}`, 400},
		{"object-update", `{"id":"` + truss + `","text":"` + strings.Repeat("x", 81) + `"}`, 400},
		{"object-update", `{"id":"` + label + `","text":" "}`, 400},
		{"object-update", `{"id":"` + truss + `","geometry":{"col":0,"row":0,"w":2,"h":2}}`, 400},
		{"object-update", `{"id":"` + truss + `","layer":"nope"}`, 404},
		{"object-duplicate", `{"id":"` + truss + `","dCol":2000}`, 400},
		{"object-duplicate", `{"id":"` + truss + `","dCol":0.3}`, 400},
		{"object-duplicate", `{"id":"` + fx + `"}`, 400},
		{"object-delete", `{"id":"nope"}`, 404},
		{"object-delete", `{"id":"` + fx + `"}`, 400},
		{"move", `{"id":"` + truss + `","col":1,"row":1}`, 400},
		{"reset-auto", `{"id":"` + truss + `"}`, 409},
		{"reset-auto", `{"id":"nope"}`, 404},
		{"reset-auto", `{}`, 400},
		{"pin", `{"id":"nope"}`, 404},
		{"item-order", `{"id":"` + truss + `","to":"sideways"}`, 400},
		{"item-order", `{"id":"nope","to":"front"}`, 404},
		{"item-order", `{"id":"` + unplacedItem + `","to":"front"}`, 409},
	}
	for _, c := range cases {
		rr := layoutPost(t, h, c.action, c.body)
		if rr.Code != c.code {
			t.Errorf("%s %s: status %d, want %d (%s)", c.action, c.body, rr.Code, c.code, rr.Body.String())
			continue
		}
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if !strings.HasSuffix(strings.TrimSpace(e.Error), ".") || e.Error == "" {
			t.Errorf("%s %s: error %q is not a finished sentence", c.action, c.body, e.Error)
		}
	}

	// Limit: 500 objects per layout.
	n := 0
	for _, it := range layoutItems(layoutGet(t, h)) {
		if it["kind"] == "object" {
			n++
		}
	}
	for ; n < 500; n++ {
		create(fmt.Sprintf(`{"objectType":"label","layer":"%s","text":"n%d","geometry":{"col":%d,"row":20}}`, stage, n, n%900))
	}
	rr := layoutPost(t, h, "object-create", `{"objectType":"label","layer":"`+stage+`","text":"one too many","geometry":{"col":0,"row":0}}`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "500") {
		t.Errorf("501st object: %d %s, want 400 naming the limit", rr.Code, rr.Body.String())
	}
	if rr := layoutPost(t, h, "object-duplicate", `{"id":"`+truss+`"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("duplicate past the limit: %d", rr.Code)
	}
	if lim := asMap(layoutGet(t, h)["limits"]); lim["maxObjects"] != 500.0 || lim["objectTextMax"] != 80.0 || lim["objectStep"] != 0.5 || lim["unplacedCols"] != 16.0 {
		t.Errorf("limits = %v", lim)
	}
}

// TestLayoutC2b_ShowSwitchAndStale: objects and Unplaced edits stay with
// their show; unknown object types and modes from other builds are
// reported, not dropped.
func TestLayoutC2b_ShowSwitchAndStale(t *testing.T) {
	h := newHarness(t)
	h.srv.SetPatchStorePath(filepath.Join(t.TempDir(), "benny512-patch.json"))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", newPatchRequest{Name: "Show A"})
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "object-create", `{"objectType":"area","layer":"unplaced","text":"A stage","geometry":{"col":0,"row":10,"w":4,"h":2}}`)
	body := layoutGet(t, h)
	e11 := entryIDByNumber(t, body, "11")
	mustLayoutPost(t, h, "pin", `{"id":"`+entryItem(t, body, e11)["id"].(string)+`"}`)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patches", newPatchRequest{Name: "Show B"}); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	b := layoutGet(t, h)
	if len(layoutItems(b)) != 0 || len(asList(asMap(b["layout"])["layers"])) != 1 {
		t.Errorf("Show B layout = %v, want only an empty Unplaced layer", b["layout"])
	}
	mustLayoutPost(t, h, "object-create", `{"objectType":"label","layer":"unplaced","text":"B only","geometry":{"col":0,"row":0}}`)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patches/default/load", nil); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	a := layoutGet(t, h)
	objs := 0
	for _, it := range layoutItems(a) {
		if it["kind"] == "object" {
			objs++
			if it["text"] != "A stage" {
				t.Errorf("Show A has object %v", it)
			}
		}
	}
	if objs != 1 || entryItem(t, a, e11)["mode"] != "manual" {
		t.Errorf("Show A after switching back: %d objects, 11 mode %v", objs, entryItem(t, a, e11)["mode"])
	}

	// Inject what a later build might write.
	if _, err := h.srv.PatchStore.Mutate(func(p *patch.Patch) error {
		var ws map[string]any
		if err := json.Unmarshal(p.Workspace, &ws); err != nil {
			return err
		}
		l := ws["layout"].(map[string]any)
		l["items"] = append(l["items"].([]any),
			map[string]any{"id": "future-obj", "kind": "object", "objectType": "speaker", "ref": "", "layer": "unplaced", "col": 0, "row": 30, "w": 1, "h": 1, "rot": 0, "mode": "manual", "order": 9, "geometry": map[string]any{"col": 0, "row": 30}},
			map[string]any{"id": "odd-mode", "kind": "group", "ref": "nope", "layer": "unplaced", "col": 0, "row": 31, "w": 1, "h": 1, "rot": 0, "mode": "sideways", "order": 10})
		bb, err := json.Marshal(ws)
		p.Workspace = bb
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a = layoutGet(t, h)
	stale := map[string]string{}
	for _, s := range asList(a["stale"]) {
		stale[asMap(s)["itemId"].(string)] = asMap(s)["problem"].(string)
	}
	if !strings.Contains(stale["future-obj"], "speaker") || stale["odd-mode"] == "" || len(stale) != 2 {
		t.Errorf("stale = %v", a["stale"])
	}
	if itemByID(a, "future-obj") == nil {
		t.Error("an object of an unknown type must be kept until removed")
	}
	mustLayoutPost(t, h, "remove", `{"id":"future-obj"}`)
	mustLayoutPost(t, h, "remove", `{"id":"odd-mode"}`)
	if len(asList(layoutGet(t, h)["stale"])) != 0 {
		t.Error("removing stale items did not clear the report")
	}
}
