package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsoleTestsPanelAndLayoutSync (Console-lite C6c) runs
// static/js/testdata/console_c6c_test.js: the real index.html in a small
// DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-tests.js and console.js against this real server over real HTTP
// and WebSocket, on the real BMFL / LEDBeam / Paladin profiles. It checks
// the exact payloads of the Tests panel (toggle, parameter, scope, fade,
// sequence-save, rename, delete, every run control, each with
// X-Benny-Tests), that another browser's tests change and a running
// sequence arrive through the "tests" broadcast, that a stale write is
// refused in words, that another browser's layout edit is re-read through
// the "layout" broadcast while this browser's own is not, and the compact
// selection bar's More menu.
func TestConsoleTestsPanelAndLayoutSync(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1),
		vendorEntry(t, "paladin_cube_real_extract.xml", "P1", 1, 101),
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
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_c6c_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_c6c_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_c6c_test.js failed: %v", err)
	}
}
