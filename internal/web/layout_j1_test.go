package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// J1 (Dom's bench test, 2026-10-12): "after a fresh show and MVR import, all
// fixtures show as Unplaced." The layout was only derived by the Edit layout
// button. Owner: derive automatically — on MVR import, on location
// re-import, and whenever the layout has never been derived and fixtures
// have positions — and never move a hand placement. Everything goes through
// the real handlers with the real Capture MVR extract parsed by the literal
// mvrimport.js.

// unplacedLocated lists entries with a known location that GET
// /api/patch/layout still reports as unplaced, with the reason.
func unplacedLocated(body map[string]any) map[string]string {
	located := map[string]bool{}
	for _, e := range asList(body["entries"]) {
		if asMap(asMap(e)["location"])["known"] == true {
			located[asMap(e)["id"].(string)] = true
		}
	}
	out := map[string]string{}
	for _, u := range asList(body["unplaced"]) {
		um := asMap(u)
		if id := um["entryId"].(string); located[id] {
			out[id] = um["reason"].(string)
		}
	}
	return out
}

func TestLayoutJ1_MVRImportDerivesAutomatically(t *testing.T) {
	h := newHarness(t)
	rev0 := layoutGet(t, h)["revision"]
	importCaptureExtract(t, h)
	body := layoutGet(t, h)
	l := asMap(body["layout"])
	if l["derived"] != true || l["autoDerived"] != true {
		t.Errorf("after a fresh MVR import: derived %v autoDerived %v, want both true", l["derived"], l["autoDerived"])
	}
	if u := unplacedLocated(body); len(u) != 0 {
		t.Errorf("located fixtures still unplaced after import: %v", u)
	}
	if body["revision"] == rev0 {
		t.Errorf("layout revision did not move (%v): browsers on the Console would not re-read", rev0)
	}
	// The automatic derive is the button's derive at its defaults: a second
	// show, imported and derived by hand, lands every fixture in the same
	// layer and cell.
	auto := placementsByNumber(t, body)
	h2 := newHarness(t)
	importCaptureExtract(t, h2)
	mustLayoutPost(t, h2, "derive", `{"replace":true,"confirm":"REPLACE"}`)
	byHand := placementsByNumber(t, layoutGet(t, h2))
	if len(auto) == 0 || len(auto) != len(byHand) {
		t.Fatalf("auto placed %d, by hand %d", len(auto), len(byHand))
	}
	for num, want := range byHand {
		if auto[num] != want {
			t.Errorf("fixture %s auto-derived at %q, by hand at %q", num, auto[num], want)
		}
	}

	// A hand placement is never moved: place 12 by hand, then re-import
	// positions with 12 and 16 moved 4 m. 16 (auto) follows; 12 stays.
	mustLayoutPost(t, h, "layer-create", `{"name":"Manual"}`)
	body = layoutGet(t, h)
	manual := layerIDByName(t, body, "Manual")
	e12, e16 := entryIDByNumber(t, body, "12"), entryIDByNumber(t, body, "16")
	id12 := entryItem(t, body, e12)["id"].(string)
	mustLayoutPost(t, h, "move", `{"id":"`+id12+`","layer":"`+manual+`","col":5,"row":5}`)
	before16 := placementsByNumber(t, layoutGet(t, h))["16"]
	var d dumpedImport
	_ = json.Unmarshal(runMVRDump(t, filepath.Join("static", "js", "testdata", "capture_demo_show_real_extract.xml"), "fallback"), &d)
	sources := make([]map[string]any, 0, len(d.Entries))
	for _, raw := range d.Entries {
		var e map[string]any
		_ = json.Unmarshal(raw, &e)
		if n := e["fixtureNumber"]; n == "12" || n == "16" {
			asMap(e["location"])["x"] = asMap(e["location"])["x"].(float64) + 4000
		}
		sources = append(sources, e)
	}
	req, _ := json.Marshal(map[string]any{"entryIds": []string{e12, e16}, "sources": sources, "confirm": "UPDATE LOCATIONS"})
	if rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/locations", req); rr.Code != http.StatusOK {
		t.Fatalf("locations: %d %s", rr.Code, rr.Body.String())
	}
	after := placementsByNumber(t, layoutGet(t, h))
	if after["12"] != "Manual 5,5" {
		t.Errorf("location re-import moved the hand placement of 12 to %q", after["12"])
	}
	if after["16"] == before16 {
		t.Errorf("location re-import left auto 16 at %q; it moved 4 m", before16)
	}

	// A merge import that brings a new located fixture places it.
	var extra map[string]any
	_ = json.Unmarshal(d.Entries[0], &extra)
	extra["name"], extra["fixtureNumber"], extra["startAddress"] = "Extra", "900", 480
	b, _ := json.Marshal(map[string]any{"mode": "merge", "entries": []any{extra}})
	if rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/import", b); rr.Code != http.StatusOK {
		t.Fatalf("merge import: %d %s", rr.Code, rr.Body.String())
	}
	body = layoutGet(t, h)
	if u := unplacedLocated(body); len(u) != 0 {
		t.Errorf("merge-imported located fixture left unplaced: %v", u)
	}
	if got := placementsByNumber(t, body); got["12"] != "Manual 5,5" {
		t.Errorf("merge import moved the hand placement of 12 to %q", got["12"])
	}
}

// A show that was imported before J1 (positions, never derived) is derived
// when it is opened — Dom's show on the bench.
func TestLayoutJ1_NeverDerivedShowIsDerivedWhenOpened(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	h.srv.SetPatchStorePath(filepath.Join(dir, "first.json"))
	importCaptureExtract(t, h)
	saved, err := os.ReadFile(filepath.Join(dir, "first.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(saved, &file); err != nil {
		t.Fatal(err)
	}
	delete(file, "workspace") // as the import left it before J1: no layout at all
	raw, _ := json.Marshal(file)
	path := filepath.Join(dir, "benny512-patch.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t)
	h2.srv.SetPatchStorePath(path)
	body := layoutGet(t, h2)
	if l := asMap(body["layout"]); l["derived"] != true {
		t.Errorf("opened a never-derived show with positions: derived %v", l["derived"])
	}
	if u := unplacedLocated(body); len(u) != 0 {
		t.Errorf("located fixtures unplaced after opening the show: %v", u)
	}
}

// Refresh still needs a derive: a show whose fixtures have no position is
// never derived automatically, so refresh is refused as before.
func TestLayoutJ1_RefreshNeedsADerive(t *testing.T) {
	h := newHarness(t)
	if rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Desk Par","fixtureNumber":"99","startAddress":400,"footprint":4}`)); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	if l := asMap(layoutGet(t, h)["layout"]); l["derived"] != false {
		t.Errorf("a show with no positions was derived: %v", l)
	}
	if rr := layoutPost(t, h, "refresh", `{}`); rr.Code != http.StatusConflict {
		t.Errorf("refresh before any derive: %d %s, want 409", rr.Code, rr.Body.String())
	}
}
