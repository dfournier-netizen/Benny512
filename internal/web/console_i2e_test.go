package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// TestConsole_I2e (Console-lite I2e: test tiles §16, sequence transport and
// editor §17, raw bank §18, MIDI bar) runs
// static/js/testdata/console_i2e_test.js: the real index.html in a small DOM
// and the LITERAL api.js, ws.js, programmer.js, ui.js, console-controls.js,
// console-tests.js, console-midi.js and console.js against this real
// server, with a real Robe BMFL Spot and LEDBeam 100, and three fixtures
// with no profile: G1 (4 channels), Z1 (no footprint) and R1 (3 channels,
// committed to an RDM device).
func TestConsole_I2e(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1),
		map[string]any{"name": "G1", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 2, "startAddress": 1},
		map[string]any{"name": "Z1", "fixtureType": "Mystery box", "footprint": 0, "universe": 2, "startAddress": 10},
		map[string]any{"name": "R1", "fixtureType": "RDM thing", "footprint": 3, "universe": 2, "startAddress": 20},
		battenEntry("BT", 3, 1), // I2e count pill: a fixture with cells
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	// R1 is committed to an RDM device the way Reconcile leaves it (a
	// confirmed UID), seeded as reconcilecommit_test.go does.
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		for i := range pp.Entries {
			if pp.Entries[i].Name == "R1" {
				pp.Entries[i].ConfirmedUID = "1900:00000042"
				pp.Entries[i].MatchState = patch.MatchStateConfirmed
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed R1's RDM commit: %v", err)
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp struct {
		Patch struct {
			Entries []struct{ ID, Name, ConfirmedUID string }
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range resp.Patch.Entries {
		ids[e.Name] = e.ID
		if e.Name == "R1" && e.ConfirmedUID == "" {
			t.Fatalf("R1 lost its confirmed UID on import; the RDM-only case cannot be exercised")
		}
	}
	if len(ids) != 6 {
		t.Fatalf("patched %d entries, want 6", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_i2e_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_i2e_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_i2e_test.js failed: %v", err)
	}
}

// TestConsole_I2eCountPillNeverCut: at 390 px "4 selected · 4 cells" lost
// its end — the phone rule made the count pill one unbreakable line with
// overflow hidden and an ellipsis. The pill's rule in the phone block of
// the literal screens-console.css must not clip (no ellipsis, no hidden
// overflow) and must let its parts wrap; console_i2e_test.js checks the
// two unbreakable parts console.js draws. Chromium measures it at 360 and
// 390 (the render proof).
func TestConsole_I2eCountPillNeverCut(t *testing.T) {
	css, err := os.ReadFile("static/css/screens-console.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(css)
	at := strings.Index(src, "@media (max-width: 767px) {\n  .b5-con-summary .b5-actionbar__title")
	if at < 0 {
		t.Fatal("the phone block of the selection bar is not where it was; update this test with it")
	}
	block := src[at:]
	rule := regexp.MustCompile(`\.b5-con-summary__status \.b5-pill \{([^}]*)\}`).FindStringSubmatch(block)
	if rule == nil {
		t.Fatal("no phone rule for the count pill (.b5-con-summary__status .b5-pill)")
	}
	for _, bad := range []string{"text-overflow: ellipsis", "overflow: hidden"} {
		if strings.Contains(rule[1], bad) {
			t.Errorf("phone count pill rule has %q: the count is cut off instead of shown whole: {%s}", bad, rule[1])
		}
	}
	if !strings.Contains(rule[1], "flex-wrap: wrap") {
		t.Errorf("phone count pill rule does not let the cell count wrap to a second line: {%s}", rule[1])
	}
	if !regexp.MustCompile(`\.b5-con-summary__countn, \.b5-con-summary__countcells \{ white-space: nowrap; \}`).MatchString(block) {
		t.Errorf("the count's two parts are not each kept whole (white-space: nowrap) on the phone")
	}
}
