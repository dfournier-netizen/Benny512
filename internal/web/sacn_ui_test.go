package web

import (
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is the Go<->JS seam for selectable sACN output. Every test here
// reads the LITERAL file the browser loads, the way universe_scheme_test.go
// and rigcheck_scope_test.go already do, because the defects this class of
// test catches are the ones neither half can see alone: the JS is internally
// consistent, the Go is internally consistent, and the two disagree about a
// string.

func readJS(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("static/js/" + name)
	if err != nil {
		t.Fatalf("reading the literal %s the browser loads: %v", name, err)
	}
	return string(b)
}

// TestSACNSettingsFormUI runs settings_sacn_test.js against the real
// ui.js/settings.js: staged-not-live dirty tracking, the worked example, the
// two-document save, and the server's own refusal reaching the screen.
func TestSACNSettingsFormUI(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping sACN settings form test")
	}
	out, err := exec.Command(nodePath, "static/js/testdata/settings_sacn_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("settings_sacn_test.js failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// TestSACNConfigKeysMatchServer pins the GET/POST /api/sacn wire shape to
// what the sACN settings form sends. The decoder uses DisallowUnknownFields,
// so a key the Go struct does not name is a 400 rather than a silent no-op —
// and the CID is server-owned and must never appear in the form at all.
func TestSACNConfigKeysMatchServer(t *testing.T) {
	var goKeys []string
	rt := reflect.TypeOf(sacnConfigJSON{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			goKeys = append(goKeys, tag)
		}
	}
	sort.Strings(goKeys)

	src := readJS(t, "settings.js")
	var jsKeys []string
	start := strings.Index(src, "function sacnPayload(")
	if start < 0 {
		t.Fatal("settings.js has no sacnPayload() — the POST /api/sacn body must be built in one place a test can read")
	}
	rest := src[start:]
	end := strings.Index(rest, "\n  }")
	if end < 0 {
		end = len(rest)
	}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9]*):`).FindAllStringSubmatch(rest[:end], -1) {
		jsKeys = append(jsKeys, m[1])
	}
	sort.Strings(jsKeys)

	if !reflect.DeepEqual(jsKeys, goKeys) {
		t.Errorf("settings.js sends %v to POST /api/sacn; the server's sacnConfigJSON names %v. "+
			"decodeJSON sets DisallowUnknownFields, so any extra key is a flat 400", jsKeys, goKeys)
	}

	// The CID is generated server-side and persisted; a receiver tells
	// sources apart by it (ANSI E1.31-2025 Section 6.2.3). A form field for
	// it would let a browser impersonate another source, and sending one is
	// a 400 anyway.
	for _, name := range []string{"settings.js", "patch.js", "api.js"} {
		body := readJS(t, name)
		if regexp.MustCompile(`(?i)\bcid\b\s*:`).MatchString(body) {
			t.Errorf("%s has a cid field; the CID is server-owned and never travels in either direction over /api/sacn", name)
		}
	}
}

// TestSACNUniverseDisplayGoesThroughTheHelpers is the numbering-discipline
// check for the new screens, mirroring TestEachScreenUsesItsOwnNumbering's
// reason for existing: one ambiguous formatter, applied by hand at a call
// site, is what shipped a universe off-by-one here before.
//
// So no screen may do the Art-Net -> sACN arithmetic or the Table 9-10
// address derivation inline. Both live in ui.js, named for what they RETURN.
func TestSACNUniverseDisplayGoesThroughTheHelpers(t *testing.T) {
	ui := readJS(t, "ui.js")
	for _, fn := range []string{"artnetToSacn", "formatSacn", "sacnMulticastAddress", "setSacnStart", "getSacnStart"} {
		if !regexp.MustCompile(`function\s+` + fn + `\s*\(`).MatchString(ui) {
			t.Errorf("ui.js has no %s — the sACN numbering must live beside formatUser/formatArtnet, not in a screen", fn)
		}
	}
	// The name has to say which numbering it produces. A bare
	// "formatUniverse" is exactly the defect the September 9 rework removed.
	if regexp.MustCompile(`function\s+formatUniverse\s*\(`).MatchString(ui) {
		t.Error("ui.js re-introduced a bare formatUniverse")
	}

	for _, name := range []string{"patch.js", "settings.js", "console-tests.js", "console-tools.js"} {
		src := readJS(t, name)
		if strings.Contains(src, "239.255.") && !strings.Contains(src, "UI.sacnMulticastAddress") {
			t.Errorf("%s writes a 239.255 multicast address without going through UI.sacnMulticastAddress — "+
				"ANSI E1.31-2025 Table 9-10 is derived in exactly one place", name)
		}
		// The mapping is `sacnStart + showUniverse - 1`, and getting it wrong
		// lights the wrong universe. If a screen mentions an sACN start it
		// must be asking ui.js, not doing the sum.
		if regexp.MustCompile(`sacnStart\s*\+`).MatchString(src) {
			t.Errorf("%s computes an sACN universe inline; call UI.artnetToSacn/UI.formatSacn instead", name)
		}
	}
}
