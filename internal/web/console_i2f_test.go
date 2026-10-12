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

// TestConsole_I2f (Console-lite I2f, component-specs §16) runs
// static/js/testdata/console_i2f_test.js: the real index.html in a small
// DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js, console-tests.js, console-midi.js and console.js
// against this real server — the 4-cell RGB batten BT (master dimmer) and
// an LED par PAR (dimmer + RGB). The masked-test note and Isolate.
func TestConsole_I2f(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "channelSets": []any{}}
	}
	par := map[string]any{"name": "PAR", "fixtureType": "Spec-derived LED par", "footprint": 4, "universe": 2, "startAddress": 301,
		"channelFunctions": map[string]any{"1": ch("Dimmer"), "2": ch("ColorAdd_R"), "3": ch("ColorAdd_G"), "4": ch("ColorAdd_B")}}
	entries := []any{battenEntry("BT", 2, 1), par}
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
	if len(ids) != 2 {
		t.Fatalf("patched %d entries, want 2", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_i2f_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_i2f_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_i2f_test.js failed: %v", err)
	}
}
