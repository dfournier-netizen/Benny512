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

// TestConsole_I2d (Console-lite I2d: fixture-command confirmation §12,
// action bar §15, presets §14) runs static/js/testdata/console_i2d_test.js:
// the real index.html in a small DOM and the LITERAL api.js, ws.js,
// programmer.js, ui.js, console-controls.js and console.js against this
// real server, with two real Robe BMFL Spot profiles (their Control1 channel
// carries FixtureGlobalReset sets, "Lamp On" and "Lamp Off") and two real
// Robe LEDBeam 100 profiles.
func TestConsole_I2d(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L2", 1, 101),
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
	if len(ids) != 4 {
		t.Fatalf("patched %d entries, want 4", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_i2d_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_i2d_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_i2d_test.js failed: %v", err)
	}
}
