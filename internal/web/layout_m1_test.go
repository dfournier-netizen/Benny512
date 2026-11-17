package web

import (
	"net/http"
	"testing"
)

// TestM1_ArrangeFromPositions (owner 2026-11-17, decision 0004): arrange
// hands the chosen fixtures — hand-placed ones included — back to automatic
// placement and puts them exactly where their MVR position falls; fixtures
// not chosen stay where they were; all needs the ARRANGE confirm; a chosen
// fixture without a position is named, not moved.
func TestM1_ArrangeFromPositions(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	body := layoutGet(t, h)
	e16, e21 := entryIDByNumber(t, body, "16"), entryIDByNumber(t, body, "21")
	id16, id21 := entryItem(t, body, e16)["id"].(string), entryItem(t, body, e21)["id"].(string)
	mustLayoutPost(t, h, "move", `{"id":"`+id16+`","col":10,"row":10}`)
	mustLayoutPost(t, h, "move", `{"id":"`+id21+`","col":12.25,"row":11.5}`)

	out := mustLayoutPost(t, h, "arrange", `{"ids":["`+e16+`"]}`)
	if it := entryItem(t, out, e16); it["mode"] != "auto" || it["id"] != id16 || it["col"] != 6.696 || it["row"] != 1.378 {
		t.Errorf("arranged 16 = %v, want auto %s at its exact position 6.696,1.378", it, id16)
	}
	if it := entryItem(t, out, e21); it["mode"] != "manual" || it["col"] != 12.25 || it["row"] != 11.5 {
		t.Errorf("21 (not chosen) = %v, want it left manual at 12.25,11.5", it)
	}
	if r := asMap(out["result"]); r["arranged"] != 1.0 || len(asList(r["noPosition"])) != 0 {
		t.Errorf("arrange result = %v, want arranged 1 and nothing without a position", r)
	}

	for _, c := range []struct {
		body string
		code int
	}{
		{`{"all":true}`, http.StatusBadRequest},
		{`{"all":true,"confirm":"YES"}`, http.StatusBadRequest},
		{`{"ids":["` + e16 + `"],"all":true,"confirm":"ARRANGE"}`, http.StatusBadRequest},
		{`{"ids":["` + e16 + `"],"confirm":"ARRANGE"}`, http.StatusBadRequest},
		{`{}`, http.StatusBadRequest},
		{`{"ids":["nope"]}`, http.StatusNotFound},
	} {
		if rr := layoutPost(t, h, "arrange", c.body); rr.Code != c.code {
			t.Errorf("arrange %s: %d %s, want %d", c.body, rr.Code, rr.Body.String(), c.code)
		}
	}
	if it := entryItem(t, layoutGet(t, h), e21); it["mode"] != "manual" {
		t.Errorf("a refused arrange changed 21: %v", it)
	}

	out = mustLayoutPost(t, h, "arrange", `{"all":true,"confirm":"ARRANGE"}`)
	if it := entryItem(t, out, e21); it["mode"] != "auto" || it["col"] != 1.649 || it["row"] != 0.887 {
		t.Errorf("arrange all: 21 = %v, want auto at 1.649,0.887", it)
	}

	// A fixture with no position: named in noPosition, left in place; alone
	// it is refused.
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Desk Par","fixtureNumber":"99","startAddress":400,"footprint":4}`))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	desk := entryIDByNumber(t, layoutGet(t, h), "99")
	if rr := layoutPost(t, h, "arrange", `{"ids":["`+desk+`"]}`); rr.Code != http.StatusBadRequest {
		t.Errorf("arrange only a fixture without a position: %d, want 400", rr.Code)
	}
	r := asMap(mustLayoutPost(t, h, "arrange", `{"ids":["`+desk+`","`+e16+`"]}`)["result"])
	if np := asList(r["noPosition"]); len(np) != 1 || np[0] != desk || r["arranged"] != 1.0 {
		t.Errorf("arrange with one unlocated fixture: %v, want noPosition [%s] and arranged 1", r, desk)
	}
}

// TestM1_ScaleMovesEverything (owner 2026-11-17: Scale is "mm per grid
// square" and everything on the plot moves with it): halving the mm per
// square doubles every plan position — derived fixtures, hand placements and
// object geometry — around the origin, leaves the Unplaced grid and tile
// sizes alone, is exactly reversible, and refuses a scale the grid cannot
// hold without changing anything.
func TestM1_ScaleMovesEverything(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	mustLayoutPost(t, h, "derive", `{"cellMm":2000,"zGapMm":800}`)
	mustLayoutPost(t, h, "layer-create", `{"name":"Stage"}`)
	body := layoutGet(t, h)
	stage := layerIDByName(t, body, "Stage")
	e16, e21 := entryIDByNumber(t, body, "16"), entryIDByNumber(t, body, "21")
	mustLayoutPost(t, h, "move", `{"id":"`+entryItem(t, body, e21)["id"].(string)+`","col":10,"row":10,"w":2}`)
	truss := asMap(mustLayoutPost(t, h, "object-create", `{"objectType":"truss","layer":"`+stage+`","geometry":{"col":0.25,"row":2.5,"col2":12,"row2":2.5}}`)["result"])["id"].(string)
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Desk Par","fixtureNumber":"99","startAddress":400,"footprint":4}`))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	desk := entryIDByNumber(t, layoutGet(t, h), "99")

	out := mustLayoutPost(t, h, "scale", `{"cellMm":1000}`)
	if r := asMap(out["result"]); r["cellMm"] != 1000.0 || r["factor"] != 2.0 {
		t.Errorf("scale result = %v, want cellMm 1000 factor 2", r)
	}
	if l := asMap(out["layout"]); l["cellMm"] != 1000.0 || l["autoDerived"] != false {
		t.Errorf("layout after scale: cellMm %v autoDerived %v, want 1000 and false (the operator chose it)", l["cellMm"], l["autoDerived"])
	}
	if it := entryItem(t, out, e16); it["col"] != 13.392 || it["row"] != 2.756 || it["w"] != 1.0 {
		t.Errorf("derived 16 after scale = %v, want 13.392,2.756 and still 1 square wide", it)
	}
	if it := entryItem(t, out, e21); it["col"] != 20.0 || it["row"] != 20.0 || it["w"] != 2.0 || it["mode"] != "manual" {
		t.Errorf("hand-placed 21 after scale = %v, want manual 20,20, still 2 squares wide", it)
	}
	var tr map[string]any
	for _, it := range layoutItems(out) {
		if it["id"] == truss {
			tr = it
		}
	}
	if g := asMap(tr["geometry"]); g["col"] != 0.5 || g["row"] != 5.0 || g["col2"] != 24.0 || g["row2"] != 5.0 || tr["col"] != 0.0 || tr["w"] != 24.0 {
		t.Errorf("truss after scale = %v, want geometry 0.5,5 -> 24,5 covering columns 0..23", tr)
	}
	if it := entryItem(t, out, desk); it["layer"] != unplacedLayer || it["col"] != 0.0 || it["row"] != 0.0 {
		t.Errorf("Unplaced Desk Par after scale = %v, want it left at 0,0", it)
	}
	// An explicit derive keeps the chosen scale.
	mustLayoutPost(t, h, "derive", `{}`)
	if it := entryItem(t, layoutGet(t, h), e16); it["col"] != 13.392 {
		t.Errorf("derive after scale put 16 at col %v, want 13.392 (the chosen 1000 mm)", it["col"])
	}

	back := mustLayoutPost(t, h, "scale", `{"cellMm":2000}`)
	if it := entryItem(t, back, e16); it["col"] != 6.696 || it["row"] != 1.378 {
		t.Errorf("scaling back put 16 at %v,%v, want 6.696,1.378", it["col"], it["row"])
	}
	if it := entryItem(t, back, e21); it["col"] != 10.0 || it["row"] != 10.0 {
		t.Errorf("scaling back put 21 at %v,%v, want 10,10", it["col"], it["row"])
	}

	// 21 at column 600 would land at 1200 at half the mm per square: past
	// the 999 edge, so the whole scale is refused.
	mustLayoutPost(t, h, "move", `{"id":"`+entryItem(t, body, e21)["id"].(string)+`","col":600,"row":10}`)
	before := layoutGet(t, h)["layout"]
	if rr := layoutPost(t, h, "scale", `{"cellMm":1000}`); rr.Code != http.StatusConflict {
		t.Errorf("a scale that pushes 21 past the grid edge: %d %s, want 409", rr.Code, rr.Body.String()[:120])
	}
	if after := layoutGet(t, h)["layout"]; asMap(after)["cellMm"] != asMap(before)["cellMm"] {
		t.Error("a refused scale changed the layout")
	}
	for _, b := range []string{`{"cellMm":10}`, `{"cellMm":20000}`, `{}`} {
		if rr := layoutPost(t, h, "scale", b); rr.Code != http.StatusBadRequest {
			t.Errorf("scale %s: %d, want 400", b, rr.Code)
		}
	}
}

// TestM1_FractionalPositions: hand moves and object geometry take any
// finite position; the Console does the snapping.
func TestM1_FractionalPositions(t *testing.T) {
	h := newHarness(t)
	importCaptureExtract(t, h)
	body := layoutGet(t, h)
	e16 := entryIDByNumber(t, body, "16")
	out := mustLayoutPost(t, h, "move", `{"id":"`+entryItem(t, body, e16)["id"].(string)+`","col":2.37,"row":-1.5}`)
	if it := entryItem(t, out, e16); it["col"] != 2.37 || it["row"] != -1.5 {
		t.Errorf("fractional move = %v, want 2.37,-1.5", it)
	}
	mustLayoutPost(t, h, "layer-create", `{"name":"Stage"}`)
	stage := layerIDByName(t, layoutGet(t, h), "Stage")
	mustLayoutPost(t, h, "object-create", `{"objectType":"truss","layer":"`+stage+`","geometry":{"col":0.25,"row":0,"col2":4,"row2":0}}`)
	if rr := layoutPost(t, h, "move", `{"id":"`+entryItem(t, body, e16)["id"].(string)+`","col":999.5,"row":0}`); rr.Code != http.StatusBadRequest {
		t.Errorf("a move past the grid edge: %d, want 400", rr.Code)
	}
}
