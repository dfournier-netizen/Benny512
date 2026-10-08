package web

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The browser half of chunk C3, against the literal files the browser loads.

func runNodeTest(t *testing.T, script string) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping " + script)
	}
	out, err := exec.Command(nodePath, "static/js/testdata/"+script).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, out)
	}
	t.Log(string(out))
}

// TestC3OutputStripUI: the master ARM / DISARM control, its state words, the
// multi-browser heartbeat and the unload goodbye (workspace.js + api.js).
func TestC3OutputStripUI(t *testing.T) { runNodeTest(t, "output_strip_test.js") }

// TestC3SettingsOutputUI: lease-loss action, per-protocol adapters and
// per-universe protocol on the Settings screen (settings.js + ui.js).
func TestC3SettingsOutputUI(t *testing.T) { runNodeTest(t, "settings_output_test.js") }

// TestC3ScreensNoLongerGateOutputThemselves: Arm is the confirm step and the
// master Arm governs all data, so the screens must no longer carry their own
// output START/STOP, confirm() on output, protocol pickers, or stop-on-leave
// beacons (owner decisions, 2026-10-06/07). Read from the literal sources.
func TestC3ScreensNoLongerGateOutputThemselves(t *testing.T) {
	rc := readJS(t, "rigcheck.js")
	for _, banned := range []string{`id="rcpStart"`, `id="rcpStop"`, "confirm(`Let output flow", "UI.RC_PROTOCOLS"} {
		if strings.Contains(rc, banned) {
			t.Errorf("rigcheck.js still contains %q — Rig Check's own START/STOP, its confirm and its protocol picker go away; output follows the master Arm", banned)
		}
	}
	if !strings.Contains(rc, "Output follows the master Arm") {
		t.Errorf("rigcheck.js does not tell the user that output follows the master Arm")
	}
	pj := readJS(t, "patch.js")
	if strings.Contains(pj, "UI.RC_PROTOCOLS") || strings.Contains(pj, "rigCheckStopBeacon") {
		t.Errorf("patch.js still carries the Rig Check protocol picker or the stop-on-hide beacon")
	}
	if regexp.MustCompile(`function onLeaveScreen\(\) \{[^}]*rigCheckStop\(`).MatchString(pj) {
		t.Errorf("patch.js still stops Rig Check output when the tab is left; leaving a screen no longer stops output")
	}
	sj := readJS(t, "send.js")
	if strings.Contains(sj, `id="btnDmxStart"`) || strings.Contains(sj, "Api.dmxStart(") {
		t.Errorf("send.js still carries its own output START; Send follows the master Arm")
	}
	if !strings.Contains(sj, "Output follows the master Arm") {
		t.Errorf("send.js does not tell the user that output follows the master Arm")
	}
}
