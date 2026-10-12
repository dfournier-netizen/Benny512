package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoBlockingDialogsInTheUI (I2d2, owner 2026-10-09): every question
// the web UI asks uses the I2d non-modal pattern (ui.js UI.ask / choose /
// notice / confirmAsk / showPanel): Cancel focused, Escape closes and
// restores focus, and DISARM stays reachable. Native prompt(), confirm()
// and alert() block the whole page, and showModal() makes everything but
// the dialog inert — so none of them may appear in the browser scripts the
// app serves. (The console_i2d_test.js run proves the pattern on the real
// store-preset and Store group dialogs.) The full-reset "Benny512 has shut
// down" overlay in settings.js is the one deliberate exception: the server
// process has exited, so there is no output left to disarm.
func TestNoBlockingDialogsInTheUI(t *testing.T) {
	files, err := filepath.Glob("static/js/*.js")
	if err != nil || len(files) < 10 {
		t.Fatalf("browser scripts: %v %v", files, err)
	}
	blocking := regexp.MustCompile(`(^|[^.\w])(prompt|confirm|alert)\s*\(|\.showModal\s*\(`)
	comment := regexp.MustCompile(`^\s*(//|\*)`)
	trailing := regexp.MustCompile(`\s//\s.*$`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if comment.MatchString(line) {
				continue
			}
			if m := blocking.FindString(trailing.ReplaceAllString(line, "")); m != "" {
				t.Errorf("%s:%d uses a blocking dialog (%s): %s", filepath.Base(f), i+1, strings.TrimSpace(m), strings.TrimSpace(line))
			}
		}
	}
}

// TestOutputNoteHiddenRule (I2d2): the Controls output note is kept in the
// panel and hidden while output is live; .b5-cc-output sets display:flex,
// which beats the hidden attribute unless a [hidden] rule says otherwise —
// in Chromium the note said "Disarmed · values retained, nothing sent"
// under ARMED · LIVE until this rule existed.
func TestOutputNoteHiddenRule(t *testing.T) {
	css := readAsset(t, "static/css/screens-console-controls.css")
	if !regexp.MustCompile(`\.b5-cc-output\[hidden\]\s*\{\s*display:\s*none;`).MatchString(css) {
		t.Errorf("screens-console-controls.css has no .b5-cc-output[hidden] { display: none; } rule, so the hidden output note still shows")
	}
}

// TestUIUsesServerCommandFlag (I2d3, owner 2026-10-09: decide what counts
// as a command the same way on both paths so they never disagree): the
// Controls read the server's per-function "command" flag
// (patch.IsCommandFunction) and keep no command pattern of their own; no
// browser script posts a value for a command function to /set. The JS runs
// (console_i2d_test.js) prove no Control1 value ever goes to /set.
func TestUIUsesServerCommandFlag(t *testing.T) {
	js := readAsset(t, "static/js/console-controls.js")
	if regexp.MustCompile(`/Reset\|\^Lamp/`).MatchString(js) {
		t.Errorf("console-controls.js still carries its own command pattern /Reset|^Lamp/; it must use the server's f.command flag")
	}
	if !strings.Contains(js, "f.command") {
		t.Errorf("console-controls.js never reads the server's f.command flag")
	}
}
