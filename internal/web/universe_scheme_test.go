package web

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestUniverseScheme runs universe_scheme_test.js against the literal ui.js
// the browser loads: the two conversions, the outside-show-range rule, and
// the bounds.
func TestUniverseScheme(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping universe scheme test")
	}
	cmd := exec.Command(nodePath, "static/js/testdata/universe_scheme_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("universe_scheme_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}

// TestEachScreenUsesItsOwnNumbering is the coverage check the JS test cannot
// make: that each screen calls the conversion belonging to what it is FOR.
//
// The original defect was not a wrong formula. It was ONE formatter, applied
// globally, whose name did not say which numbering it produced — so the
// Nodes tab read one less than the EN4's faceplate, and no individual call
// site looked wrong, because every screen was calling the same function and
// that function could only be correct for some of them.
//
// Renaming it into two pairs is what makes a wrong screen visible. This test
// is what keeps it that way: it fails if a protocol screen starts applying
// the show offset, or an operator screen starts showing raw wire numbers.
func TestEachScreenUsesItsOwnNumbering(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("static/js/" + name)
		if err != nil {
			t.Fatalf("reading the literal %s the browser loads: %v", name, err)
		}
		return string(b)
	}

	// Comments legitimately name the other pair when explaining the split, so
	// match CALLS (`UI.formatUser(`), not mentions.
	calls := func(src, fn string) bool {
		return regexp.MustCompile(`UI\.` + fn + `\s*\(`).MatchString(src)
	}

	for _, c := range []struct {
		file    string
		wants   []string // conversions this screen must use
		forbids []string // conversions that would mean the wrong numbering
		why     string
	}{
		{
			file:    "nodes.js",
			wants:   []string{"formatArtnet"},
			forbids: []string{"formatUser", "parseUser"},
			why: "Nodes is a protocol screen: it must agree with the gateway's own faceplate. " +
				"Applying the show offset here is the original defect — the Nodes tab read one " +
				"less than the EN4 for every universe",
		},
		{
			file:    "analyzer.js",
			wants:   []string{"formatArtnet"},
			forbids: []string{"formatUser", "parseUser"},
			why: "the Analyzer reports what was on the wire. A capture renumbered by a display " +
				"setting is not evidence of anything",
		},
		{
			file:    "patch.js",
			wants:   []string{"formatUser"},
			forbids: []string{"formatArtnet"},
			why:     "a patch sheet says universe 1; the operator should not have to translate",
		},
		{
			file:    "rigcheck.js",
			wants:   []string{"formatUser"},
			forbids: []string{"formatArtnet"},
			why:     "Rig Check and Function check are operator screens, scoped the way Patch is",
		},
		{
			file:    "walk.js",
			wants:   []string{"formatUser"},
			forbids: []string{"formatArtnet"},
			why:     "Rig Walk is worked from the patch sheet",
		},
		{
			file:    "devices.js",
			wants:   []string{"formatBoth"},
			forbids: []string{"formatUser"},
			why: "Devices is where a physical port and a patched fixture meet, so it is the one " +
				"screen that must show both numbers — showing either alone forces the operator " +
				"to do the correlation in their head, which is the job this setting exists to do",
		},
	} {
		src := read(c.file)
		for _, w := range c.wants {
			if !calls(src, w) {
				t.Errorf("%s never calls UI.%s — %s", c.file, w, c.why)
			}
		}
		for _, f := range c.forbids {
			if calls(src, f) {
				t.Errorf("%s calls UI.%s, which is the wrong numbering for this screen — %s",
					c.file, f, c.why)
			}
		}
	}

	// And the ambiguous old API is gone everywhere, not merely unused in the
	// screens above: while the name exists, a new screen can reach for it.
	for _, name := range []string{
		"ui.js", "nodes.js", "analyzer.js", "patch.js", "rigcheck.js", "walk.js",
		"devices.js", "devicedetail.js", "send.js", "reconcile.js", "settings.js",
		"library.js", "workspace.js", "app.js",
	} {
		src := read(name)
		for _, gone := range []string{"UI.formatUniverse(", "UI.parseUniverse(", "UI.setUniverseBase(", "UI.universeBaseLabel("} {
			if strings.Contains(src, gone) {
				t.Errorf("%s still calls %s — the old single formatter could not say which "+
					"numbering it produced, which is what let one screen disagree with another",
					name, gone)
			}
		}
	}

	// Settings must offer the starting universe, and must no longer offer the
	// 0/1 notation switch it replaced.
	settings := read("settings.js")
	if !strings.Contains(settings, "artnetStartUniverse") {
		t.Error("settings.js has no artnetStartUniverse control — the setting the whole scheme hangs off")
	}
	if regexp.MustCompile(`id="universeBase"`).MatchString(settings) {
		t.Error("settings.js still renders the old universeBase picker; two settings meaning " +
			"overlapping things is worse than either alone")
	}
}
