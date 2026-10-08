package web

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFaderBar_UI (Console-lite G3) runs static/js/testdata/faders_test.js:
// the real index.html in a small DOM and the LITERAL api.js, ws.js, ui.js,
// workspace.js and faders.js against this real server over real HTTP and
// WebSocket, on the G2 rig (real BMFL, LEDBeam and Paladin profiles, the
// Paladin in two modes, an unprofiled fixture, a spec-derived RGB-only
// fixture). It checks one fader per type+mode and per stored group with
// labels and counts, throttled drag writes with the final value on release,
// keyboard steps, Release / Release all, WebSocket refresh, the once-only
// retry of a stale write, the disarmed hint following ARM, and the
// remembered collapsed state — and what the server holds after each.
func TestFaderBar_UI(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newG2Rig(t)
	idsJSON, _ := json.Marshal(r.ids)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "faders_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("faders_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("faders_test.js failed: %v", err)
	}
}
