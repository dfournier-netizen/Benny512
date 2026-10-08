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
	// rigcheck.js and send.js were retired in C7; their functions are the
	// Console's Tests and Tools panels, checked here and in console_c7_test.go.
	pj := readJS(t, "patch.js")
	if strings.Contains(pj, "UI.RC_PROTOCOLS") || strings.Contains(pj, "rigCheckStopBeacon") {
		t.Errorf("patch.js still carries the Rig Check protocol picker or the stop-on-hide beacon")
	}
	if regexp.MustCompile(`function onLeaveScreen\(\) \{[^}]*rigCheckStop\(`).MatchString(pj) {
		t.Errorf("patch.js still stops Rig Check output when the tab is left; leaving a screen no longer stops output")
	}
	tj := readJS(t, "console-tools.js")
	if strings.Contains(tj, "btnDmxStart") || strings.Contains(tj, "Api.dmxStart(") {
		t.Errorf("console-tools.js carries its own output START; raw levels follow the master Arm")
	}
	if !strings.Contains(tj, "Output follows the master Arm") {
		t.Errorf("console-tools.js does not tell the user that output follows the master Arm")
	}
}
