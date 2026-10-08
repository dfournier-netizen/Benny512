package web

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsoleControls_AttributeControls (Console-lite C6b) runs
// static/js/testdata/console_controls_test.js: the real index.html in a
// small DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js and console.js against this real server over real
// HTTP and WebSocket, with real vendor GDTF profiles (two Robe BMFL Spots,
// a Robe LEDBeam 100, an Elation Paladin Cube) and an unprofiled fixture.
// It checks the exact payloads of the XY pad (in tandem with the faders),
// rate-limited fader drags, fine steps, the colour picker's RGB/CMY/wheel
// mapping, wheel slots and function faders, the mode-master report,
// hold-to-fire Control buttons (and that Control has no faders), raw DMX
// with release, Clear, Fan, presets, Lowlight, the once-only retry of a
// stale write, and that a slow re-read never winds the revision back — and
// what the server holds after each.
func TestConsoleControls_AttributeControls(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newC4aRig(t)
	idsJSON, _ := json.Marshal(r.ids)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_controls_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_controls_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_controls_test.js failed: %v", err)
	}
}
