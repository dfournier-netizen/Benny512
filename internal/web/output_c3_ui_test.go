package web

import (
	"os"
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

// TestI2fPhoneStripHidesShowNameWhenArmed (owner 2026-10-09): at phone
// widths, while armed, the strip's show name was squeezed to "D." by ARMED
// · LIVE. The phone block of the literal workspace.css must hide it
// visually while armed — clipped, not display:none, so assistive
// technology still reads it (and Show tools names the show) — and only
// while armed (output_strip_test.js checks workspace.js sets the state).
func TestI2fPhoneStripHidesShowNameWhenArmed(t *testing.T) {
	css, err := os.ReadFile("static/css/workspace.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(css)
	at := strings.Index(src, "@media (max-width: 767px) {\n  .b5-header { position: relative; top: auto; }")
	if at < 0 {
		t.Fatal("the phone block of the strip is not where it was; update this test with it")
	}
	rule := regexp.MustCompile(`\.b5-show-context\[data-out-state="armed"\] \.b5-show-context__name \{([^}]*)\}`).FindStringSubmatch(src[at:])
	if rule == nil {
		t.Fatal(`no phone rule hides the show name while armed (.b5-show-context[data-out-state="armed"] .b5-show-context__name)`)
	}
	if strings.Contains(rule[1], "display: none") || strings.Contains(rule[1], "visibility: hidden") {
		t.Errorf("the armed phone rule removes the name from assistive technology too: {%s}", rule[1])
	}
	for _, want := range []string{"position: absolute", "clip-path: inset(50%)", "width: 1px", "height: 1px", "overflow: hidden"} {
		if !strings.Contains(rule[1], want) {
			t.Errorf("the armed phone rule is not a visually-hidden clip (missing %q): {%s}", want, rule[1])
		}
	}
}
