package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// Console-lite C2 layout: a per-show 2D grid with Z layers, stored in the
// show file's workspace beside saved groups. All assertions go through the
// HTTP API as generic JSON.

func layoutGet(t *testing.T, h *testHarness) map[string]any {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/layout", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/patch/layout: %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	mustUnmarshal(t, rr, &out)
	return out
}

func layoutPost(t *testing.T, h *testHarness, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doBytes(t, h.srv.Handler(), "POST", "/api/patch/layout/"+action, []byte(body))
}

func mustLayoutPost(t *testing.T, h *testHarness, action, body string) map[string]any {
	t.Helper()
	rr := layoutPost(t, h, action, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST layout/%s %s: %d %s", action, body, rr.Code, rr.Body.String())
	}
	var out map[string]any
	mustUnmarshal(t, rr, &out)
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// placementsByNumber maps each placed entry's fixtureNumber to
// "<layer name> col,row" from a GET /api/patch/layout body.
func placementsByNumber(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	layout := asMap(body["layout"])
	layerName := map[string]string{}
	for _, l := range asList(layout["layers"]) {
		lm := asMap(l)
		layerName[lm["id"].(string)] = lm["name"].(string)
	}
	number := map[string]string{}
	for _, e := range asList(body["entries"]) {
		em := asMap(e)
		number[em["id"].(string)] = fmt.Sprint(em["fixtureNumber"])
	}
	out := map[string]string{}
	for _, it := range asList(layout["items"]) {
		im := asMap(it)
		if im["kind"] != "entry" {
			continue
		}
		out[number[im["ref"].(string)]] = fmt.Sprintf("%s %v,%v", layerName[im["layer"].(string)], im["col"], im["row"])
	}
	return out
}

func importCaptureExtract(t *testing.T, h *testHarness) {
	t.Helper()
	importDump(t, h, runMVRDump(t, filepath.Join("static", "js", "testdata", "capture_demo_show_real_extract.xml"), "fallback"), "fresh")
}

// Hand-derived expectations for the real Capture extract at cellMm 2000,
// zGapMm 800 (the arithmetic, step by step):
//
// Layers — sort the nine Z values and start a new layer wherever the gap to
// the previous one exceeds 800 mm:
//
//	1053.27075 ×2 | gap 3511.75 | 4565.0166 | gap 1000.00 | 5565.0166 ×2,
//	5856.77441 ×2 (gaps 0, 291.76) | gap 1803.26 | 7660.03125, 7812.87549
//	→ four layers, ordered by height, named after their Z range.
//
// Grid — origin is (min X, max Y) over located entries = (−6670.87305,
// 1865.55334); col = round((X − originX)/2000), row = round((originY − Y)/2000)
// (plan view, rows grow downstage). Patch order decides who keeps a contested
// cell; the loser takes the free cell nearest its exact fractional position
// (Euclidean), ties to the smaller row, then the smaller col.
//
//	11  (0, 1.378) → 0,1    12  (0, 1.378) → 0,1 (another layer)
//	16  (6.696, 1.378) → 7,1
//	21  (1.649, 0.887) → 2,1
//	22  (2.149, 0.887) → 2,1 taken by 21; nearest free: 3,1 (d²=0.738;
//	     2,0 is d²=0.809)
//	1   (1.570, 0.252) → 2,0
//	2   (1.954, 0.000) → 2,0 taken by 1; nearest free: 1,0 (d²=0.911;
//	     2,1 and 2,−1 are d²=1.002)
//	47  (6.641, 2.628) → 7,3    71  (6.524, 2.342) → 7,2
var captureDerived2000 = map[string]string{
	"11": "Height 5565–5857 mm 0,1",
	"12": "Height 4565 mm 0,1",
	"16": "Height 5565–5857 mm 7,1",
	"21": "Height 1053 mm 2,1",
	"22": "Height 1053 mm 3,1",
	"1":  "Height 5565–5857 mm 2,0",
	"2":  "Height 5565–5857 mm 1,0",
	"47": "Height 7660–7813 mm 7,3",
	"71": "Height 7660–7813 mm 7,2",
}

func TestLayoutDerive_RealCaptureExtract(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	body := layoutGet(t, h)

	layers := asList(asMap(body["layout"])["layers"])
	wantLayers := []struct {
		name       string
		zMin, zMax float64
	}{
		{"Height 1053 mm", 1053.27075, 1053.27075},
		{"Height 4565 mm", 4565.0166, 4565.0166},
		{"Height 5565–5857 mm", 5565.0166, 5856.77441},
		{"Height 7660–7813 mm", 7660.03125, 7812.87549},
	}
	// C2b: the system Unplaced layer is always present; derived layers are
	// inserted before it.
	if len(layers) != len(wantLayers)+1 || asMap(layers[len(wantLayers)])["id"] != "unplaced" {
		t.Fatalf("derived %d layers, want %d + Unplaced: %v", len(layers), len(wantLayers), layers)
	}
	for i, w := range wantLayers {
		l := asMap(layers[i])
		if l["name"] != w.name || l["zKnown"] != true || l["zMin"] != w.zMin || l["zMax"] != w.zMax || l["order"] != float64(i) {
			t.Errorf("layer %d = %v, want %s z %v..%v order %d", i, l, w.name, w.zMin, w.zMax, i)
		}
	}
	got := placementsByNumber(t, body)
	for num, want := range captureDerived2000 {
		if got[num] != want {
			t.Errorf("fixture %s placed at %q, want %q", num, got[num], want)
		}
	}
	if l := asMap(body["layout"]); l["derived"] != true || l["cellMm"] != 2000.0 || l["zGapMm"] != 800.0 {
		t.Errorf("layout header = derived %v cellMm %v zGapMm %v", l["derived"], l["cellMm"], l["zGapMm"])
	}

	// Deterministic: a replace-derive from scratch lands everything in the
	// same cells.
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800,"replace":true,"confirm":"REPLACE"}`)
	again := placementsByNumber(t, layoutGet(t, h))
	for num, want := range captureDerived2000 {
		if again[num] != want {
			t.Errorf("re-derive: fixture %s at %q, want %q", num, again[num], want)
		}
	}
}

func layerIDByName(t *testing.T, body map[string]any, name string) string {
	t.Helper()
	for _, l := range asList(asMap(body["layout"])["layers"]) {
		if asMap(l)["name"] == name {
			return asMap(l)["id"].(string)
		}
	}
	t.Fatalf("no layer named %q", name)
	return ""
}

func entryIDByNumber(t *testing.T, body map[string]any, num string) string {
	t.Helper()
	for _, e := range asList(body["entries"]) {
		if fmt.Sprint(asMap(e)["fixtureNumber"]) == num {
			return asMap(e)["id"].(string)
		}
	}
	t.Fatalf("no entry %s", num)
	return ""
}

func TestLayoutDerive_PreservesManualPlacements(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "layer-create", `{"name":"Manual"}`)
	body := layoutGet(t, h)
	manual := layerIDByName(t, body, "Manual")
	e12 := entryIDByNumber(t, body, "12")
	mustLayoutPost(t, h, "place", `{"kind":"entry","ref":"`+e12+`","layer":"`+manual+`","col":5,"row":5}`)

	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	got := placementsByNumber(t, layoutGet(t, h))
	if got["12"] != "Manual 5,5" {
		t.Errorf("derive moved the manual placement of 12 to %q", got["12"])
	}
	for num, want := range captureDerived2000 {
		if num != "12" && got[num] != want {
			t.Errorf("fixture %s at %q, want %q", num, got[num], want)
		}
	}
	if n := len(asList(asMap(layoutGet(t, h)["layout"])["layers"])); n != 5 {
		t.Errorf("layers = %d, want Manual + 3 derived (4565 mm has nothing left to place) + Unplaced", n)
	}

	// A second derive with everything placed changes nothing.
	res := mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	if placed := asList(asMap(res["result"])["placed"]); len(placed) != 0 {
		t.Errorf("second derive placed %d items, want 0", len(placed))
	}

	// Replace must be explicit and confirmed.
	if rr := layoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800,"replace":true}`); rr.Code != http.StatusBadRequest {
		t.Errorf("replace without confirm: %d %s, want 400", rr.Code, rr.Body.String())
	}
	if got := placementsByNumber(t, layoutGet(t, h)); got["12"] != "Manual 5,5" {
		t.Errorf("refused replace still changed 12: %q", got["12"])
	}
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800,"replace":true,"confirm":"REPLACE"}`)
	if got := placementsByNumber(t, layoutGet(t, h)); got["12"] != captureDerived2000["12"] {
		t.Errorf("confirmed replace: 12 at %q, want %q", got["12"], captureDerived2000["12"])
	}
}

func TestLayout_UnplacedDeletionCleanupAndStaleRefs(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Desk Par","fixtureNumber":"99","startAddress":400,"footprint":4}`))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	body := layoutGet(t, h)
	desk := entryIDByNumber(t, body, "99")
	found := false
	for _, u := range asList(body["unplaced"]) {
		um := asMap(u)
		if um["entryId"] == desk {
			found = true
			if um["reason"] != "no-location" {
				t.Errorf("Desk Par unplaced reason = %v, want no-location", um["reason"])
			}
		}
	}
	if !found {
		t.Errorf("an entry with no location must be listed as unplaced: %v", body["unplaced"])
	}
	// C2b: it is not given a derived spot, but sits on the system Unplaced
	// layer so it stays selectable.
	if got := placementsByNumber(t, body)["99"]; got != "Unplaced 0,0" {
		t.Errorf("an entry with no location is at %q, want Unplaced 0,0", got)
	}

	// A group placed as one item; deleting the group removes the item.
	e11, e16 := entryIDByNumber(t, body, "11"), entryIDByNumber(t, body, "16")
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/save-group", map[string]any{"name": "Truss pair", "entryIds": []string{e11, e16}})
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	var saved struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, rr, &saved)
	layer := asMap(asList(asMap(body["layout"])["layers"])[0])["id"].(string)
	mustLayoutPost(t, h, "place", `{"kind":"group","ref":"`+saved.ID+`","layer":"`+layer+`","col":20,"row":20,"w":2,"h":1,"rot":90}`)
	countKind := func(kind, ref string) int {
		n := 0
		for _, it := range asList(asMap(layoutGet(t, h)["layout"])["items"]) {
			if asMap(it)["kind"] == kind && asMap(it)["ref"] == ref {
				n++
			}
		}
		return n
	}
	if countKind("group", saved.ID) != 1 {
		t.Fatal("group placement missing")
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/delete-group", map[string]any{"id": saved.ID})
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	if countKind("group", saved.ID) != 0 {
		t.Error("deleting a group left its placement behind")
	}

	// Deleting an entry removes its placement.
	if countKind("entry", e11) != 1 {
		t.Fatal("entry 11 not placed")
	}
	if rr := doJSON(t, h.srv.Handler(), "DELETE", "/api/patch/entries/"+e11, nil); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	if countKind("entry", e11) != 0 {
		t.Error("deleting an entry left its placement behind")
	}
	if stale := asList(layoutGet(t, h)["stale"]); len(stale) != 0 {
		t.Errorf("clean deletions reported stale refs: %v", stale)
	}

	// A stale ref already in the file (written by hand, or by an older
	// build) is reported on load, not silently dropped.
	if _, err := h.srv.PatchStore.Mutate(func(p *patch.Patch) error {
		var ws map[string]any
		if err := json.Unmarshal(p.Workspace, &ws); err != nil {
			return err
		}
		l := ws["layout"].(map[string]any)
		l["items"] = append(l["items"].([]any), map[string]any{"id": "ghost-item", "kind": "entry", "ref": "ghost", "layer": layer, "col": 30, "row": 30, "w": 1, "h": 1, "rot": 0})
		b, err := json.Marshal(ws)
		p.Workspace = b
		return err
	}); err != nil {
		t.Fatal(err)
	}
	body = layoutGet(t, h)
	stale := asList(body["stale"])
	if len(stale) != 1 || asMap(stale[0])["itemId"] != "ghost-item" {
		t.Errorf("stale = %v, want the ghost item reported", stale)
	}
	if countKind("entry", "ghost") != 1 {
		t.Error("a stale item must stay in the layout until removed explicitly")
	}
	mustLayoutPost(t, h, "remove", `{"id":"ghost-item"}`)
	if len(asList(layoutGet(t, h)["stale"])) != 0 {
		t.Error("removing the stale item did not clear the report")
	}
}

func TestLayout_ShowSwitchIsolatesLayouts(t *testing.T) {
	h := newHarness(t)
	h.srv.SetPatchStorePath(filepath.Join(t.TempDir(), "benny512-patch.json"))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", newPatchRequest{Name: "Show A"})
	importCaptureExtract(t, h)
	// importCaptureExtract is a fresh import: it replaces Show A's content.
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patches", newPatchRequest{Name: "Show B"}); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	b := asMap(layoutGet(t, h)["layout"])
	if len(asList(b["layers"])) != 1 || len(asList(b["items"])) != 0 || b["derived"] != false {
		t.Errorf("Show B inherited Show A's layout: %v", b)
	}
	mustLayoutPost(t, h, "layer-create", `{"name":"B only"}`)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patches/default/load", nil)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	a := layoutGet(t, h)
	if got := placementsByNumber(t, a); got["22"] != captureDerived2000["22"] {
		t.Errorf("Show A layout after switching back: 22 at %q", got["22"])
	}
	for _, l := range asList(asMap(a["layout"])["layers"]) {
		if asMap(l)["name"] == "B only" {
			t.Error("Show B's layer leaked into Show A")
		}
	}
}

func TestLayout_ValidationAndLimits(t *testing.T) {
	h := newHarness(t)
	if rr := layoutPost(t, h, "layer-create", `{"name":"x"}`); rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
		t.Errorf("layout edit with no show: %d %s", rr.Code, rr.Body.String())
	}
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "layer-create", `{"name":"Floor","zKnown":true,"zMin":0,"zMax":1500}`)
	body := layoutGet(t, h)
	floor := layerIDByName(t, body, "Floor")
	e11, e12 := entryIDByNumber(t, body, "11"), entryIDByNumber(t, body, "12")
	mustLayoutPost(t, h, "place", `{"kind":"entry","ref":"`+e11+`","layer":"`+floor+`","col":0,"row":0,"w":2,"h":2}`)
	placed := asMap(asList(asMap(layoutGet(t, h)["layout"])["items"])[0])
	item := placed["id"].(string)
	if placed["w"] != 2.0 || placed["h"] != 2.0 || placed["rot"] != 0.0 {
		t.Errorf("placed item = %v", placed)
	}
	mustLayoutPost(t, h, "layer-create", `{"name":"Upper"}`)
	upper := layerIDByName(t, layoutGet(t, h), "Upper")

	cases := []struct {
		action, body string
		code         int
	}{
		{"layer-create", `{"name":""}`, 400},
		{"layer-create", `{"name":"` + strings.Repeat("n", 81) + `"}`, 400},
		{"layer-create", `{"name":"z","zKnown":false,"zMin":5}`, 400},
		{"layer-create", `{"name":"z","zKnown":true,"zMin":10,"zMax":5}`, 400},
		{"layer-create", `{"name":"z","colour":"red"}`, 400},
		{"layer-rename", `{"id":"nope","name":"x"}`, 404},
		{"layer-rename", `{"id":"` + floor + `","name":" "}`, 400},
		{"layer-reorder", `{"order":["` + floor + `"]}`, 400},
		{"layer-reorder", `{"order":["` + floor + `","` + floor + `","unplaced"]}`, 400},
		{"layer-delete", `{"id":"` + floor + `"}`, 409},
		{"layer-delete", `{"id":"nope"}`, 404},
		{"place", `{"kind":"fixture","ref":"` + e12 + `","layer":"` + floor + `","col":5,"row":5}`, 400},
		{"place", `{"kind":"entry","ref":"nope","layer":"` + floor + `","col":5,"row":5}`, 404},
		{"place", `{"kind":"group","ref":"nope","layer":"` + floor + `","col":5,"row":5}`, 404},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"nope","col":5,"row":5}`, 404},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":5,"row":5,"rot":45}`, 400},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":5,"row":5,"w":0}`, 400},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":5,"row":5,"h":65}`, 400},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":1000,"row":5}`, 400},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":-1000,"row":5}`, 400},
		{"place", `{"kind":"entry","ref":"` + e11 + `","layer":"` + upper + `","col":9,"row":9}`, 409},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":1,"row":1}`, 409},
		{"place", `{"kind":"entry","ref":"` + e12 + `","layer":"` + floor + `","col":5,"row":5,"extra":1}`, 400},
		{"move", `{"id":"nope","col":1,"row":1}`, 404},
		{"move", `{"id":"` + item + `","col":1,"row":1,"rot":13}`, 400},
		{"move", `{"id":"` + item + `","layer":"nope"}`, 404},
		{"remove", `{"id":"nope"}`, 404},
		{"derive", `{"cellMm":0}`, 400},
		{"derive", `{"cellMm":20000}`, 400},
		{"derive", `{"zGapMm":-1}`, 400},
		{"explode", `{}`, 404},
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
		if c.action != "explode" && !strings.HasSuffix(strings.TrimSpace(e.Error), ".") {
			t.Errorf("%s %s: error %q is not a finished sentence", c.action, c.body, e.Error)
		}
	}

	// A move into an occupied cell is refused; moving between layers works.
	mustLayoutPost(t, h, "place", `{"kind":"entry","ref":"`+e12+`","layer":"`+floor+`","col":5,"row":5}`)
	if rr := layoutPost(t, h, "move", `{"id":"`+item+`","col":4,"row":4}`); rr.Code != http.StatusConflict {
		t.Errorf("move overlapping (2x2 at 4,4 covers 5,5): %d %s, want 409", rr.Code, rr.Body.String())
	}
	mustLayoutPost(t, h, "move", `{"id":"`+item+`","layer":"`+upper+`","col":5,"row":5}`)
	if got := placementsByNumber(t, layoutGet(t, h)); got["11"] != "Upper 5,5" {
		t.Errorf("move between layers: 11 at %q", got["11"])
	}
	// Reorder, rename, delete with explicit removal of its items.
	mustLayoutPost(t, h, "layer-reorder", `{"order":["`+upper+`","`+floor+`","unplaced"]}`)
	mustLayoutPost(t, h, "layer-rename", `{"id":"`+upper+`","name":"Top"}`)
	layers := asList(asMap(layoutGet(t, h)["layout"])["layers"])
	if asMap(layers[0])["name"] != "Top" || asMap(layers[0])["order"] != 0.0 || asMap(layers[1])["order"] != 1.0 {
		t.Errorf("reorder/rename: %v", layers)
	}
	mustLayoutPost(t, h, "layer-delete", `{"id":"`+floor+`","removeItems":true}`)
	// C2b: 12 falls back to the Unplaced layer (first in patch order there).
	if got := placementsByNumber(t, layoutGet(t, h)); got["12"] != "Unplaced 0,0" {
		t.Errorf("layer delete with removeItems left 12 at %q, want Unplaced 0,0", got["12"])
	}

	// Layer limit: 32.
	for i := len(asList(asMap(layoutGet(t, h)["layout"])["layers"])); i < 32; i++ {
		mustLayoutPost(t, h, "layer-create", fmt.Sprintf(`{"name":"L%d"}`, i))
	}
	if rr := layoutPost(t, h, "layer-create", `{"name":"one too many"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("33rd layer: %d %s, want 400", rr.Code, rr.Body.String())
	}

	// The show-guard token applies like every other structural edit.
	req := httptest.NewRequest("POST", "/api/patch/layout/layer-rename", strings.NewReader(`{"id":"`+upper+`","name":"Stale"}`))
	req.Header.Set("X-Benny-Show", "999999")
	rr := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("stale show token: %d %s, want 409", rr.Code, rr.Body.String())
	}
}

// TestLayout_EntryViewForConsole: GET carries what the Console-lite grid
// (C6) needs per entry without a second fetch.
func TestLayout_EntryViewForConsole(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	body := layoutGet(t, h)
	for _, e := range asList(body["entries"]) {
		em := asMap(e)
		for _, k := range []string{"id", "name", "fixtureType", "mode", "fixtureNumber", "universe", "startAddress", "footprint", "location", "cellCount", "cellCountKnown", "placed"} {
			if _, ok := em[k]; !ok {
				t.Errorf("entry %v lacks %q", em["id"], k)
			}
		}
		// The fallback import resolved no GDTF: cell count unknown, said so.
		if em["cellCountKnown"] != false || em["cellCount"] != 0.0 {
			t.Errorf("entry %v: cellCount %v known %v, want unknown", em["id"], em["cellCount"], em["cellCountKnown"])
		}
	}
	// A GDTF-profiled entry with four instances of one attribute: 4 cells.
	cf := map[string]any{}
	for i := 1; i <= 4; i++ {
		cf[fmt.Sprint(i)] = map[string]any{"source": "gdtf", "attribute": "Dimmer", "geometryInstance": fmt.Sprintf("Cell:%d", i), "channelSets": []any{}}
	}
	cf["5"] = map[string]any{"source": "gdtf", "attribute": "Pan", "geometryInstance": "Yoke:0", "channelSets": []any{}}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", map[string]any{"name": "Bar", "fixtureNumber": "77", "startAddress": 300, "footprint": 5, "channelFunctions": cf})
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	for _, e := range asList(layoutGet(t, h)["entries"]) {
		em := asMap(e)
		if em["fixtureNumber"] == "77" && (em["cellCount"] != 4.0 || em["cellCountKnown"] != true) {
			t.Errorf("Bar: cellCount %v known %v, want 4 known", em["cellCount"], em["cellCountKnown"])
		}
	}
}
