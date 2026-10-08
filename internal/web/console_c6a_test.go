package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestConsoleScreen_SelectionGridAndLayoutEdit (Console-lite C6a) runs
// static/js/testdata/console_test.js: the real index.html in a small DOM and
// the LITERAL api.js, ws.js, programmer.js, ui.js and console.js against
// this real server over real HTTP and WebSocket. It checks the exact
// payloads each gesture sends (select set/toggle/cell/layer/group/all/none,
// store group, highlight, locate, derive, move by drag and by key,
// reset-auto, object-create, object-update by drag, layer-create), that a
// drag with edit mode OFF sends nothing, that another browser's selection
// arrives through the programmer broadcast, and what the server holds
// afterwards. Part 2 runs the literal app.js on the real index.html: nav
// entries on desktop and mobile, enter/leave hooks, leaving sends nothing,
// script order.
func TestConsoleScreen_SelectionGridAndLayoutEdit(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	loc := func(e map[string]any, x, y, z float64) map[string]any {
		e["location"] = map[string]any{"known": true, "x": x, "y": y, "z": z}
		return e
	}
	entries := []any{
		loc(vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1), 0, 0, 6000),
		loc(vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42), 1000, 0, 6000),
		loc(vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1), 0, -2000, 0),
		loc(vendorEntry(t, "paladin_cube_real_extract.xml", "P1", 1, 101), 1000, -2000, 0),
		map[string]any{"name": "G1", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 1, "startAddress": 201},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	if err := json.Unmarshal(doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range resp.Patch.Entries {
		ids[e.Name] = e.ID
	}
	if len(ids) != 5 {
		t.Fatalf("patched %d entries, want 5", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_test.js failed: %v", err)
	}
}

// TestConsoleScreen_ServedAndOrdered: the index.html the server actually
// serves registers the Console screen and loads console.js after the
// scripts it uses (api, ws, programmer, ui, workspace) and before app.js;
// every script and stylesheet it names is served.
func TestConsoleScreen_ServedAndOrdered(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	get := func(p string) (int, string) {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	code, html := get("/")
	if code != http.StatusOK {
		t.Fatalf("GET /: %d", code)
	}
	for _, want := range []string{`id="screen-console"`, `id="consoleRoot"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if n := strings.Count(html, `data-tab="console"`); n != 2 {
		t.Errorf("index.html has %d Console nav entries, want 2 (desktop and mobile)", n)
	}
	var scripts []string
	for _, m := range regexp.MustCompile(`<script src="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		scripts = append(scripts, m[1])
	}
	at := func(s string) int {
		for i, v := range scripts {
			if v == "/js/"+s {
				return i
			}
		}
		return -1
	}
	c := at("console.js")
	if c < 0 {
		t.Fatalf("console.js is not loaded: %v", scripts)
	}
	for _, dep := range []string{"api.js", "ws.js", "programmer.js", "ui.js", "workspace.js"} {
		if i := at(dep); i < 0 || i > c {
			t.Errorf("%s must load before console.js: %v", dep, scripts)
		}
	}
	if at("app.js") < c {
		t.Errorf("console.js must load before app.js: %v", scripts)
	}
	refs := append([]string{}, scripts...)
	for _, m := range regexp.MustCompile(`<link rel="stylesheet" href="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		refs = append(refs, m[1])
	}
	if !strings.Contains(strings.Join(refs, " "), "/css/screens-console.css") {
		t.Errorf("screens-console.css is not linked")
	}
	for _, p := range refs {
		if code, _ := get(p); code != http.StatusOK {
			t.Errorf("GET %s: %d", p, code)
		}
	}
	_, app := get("/js/app.js")
	for _, want := range []string{"ConsoleScreen.init()", "ConsoleScreen.onEnterScreen()", "ConsoleScreen.onLeaveScreen()"} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not call %s", want)
		}
	}
}
