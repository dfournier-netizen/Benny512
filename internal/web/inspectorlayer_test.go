package web

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Devices inspector is a position:fixed drawer that was rendering
// UNDERNEATH the persistent show-context strip: its own bar, and therefore
// its close button, was unreachable. Two independent causes, and fixing
// either alone leaves a broken screen:
//
//  1. Both components opted out of the design system's z-index scale.
//     `.b5-show-context` hard-coded 40 — numerically --b5-z-modal, so a
//     page-chrome strip claimed the dialog layer — and `.b5-inspector`
//     hard-coded 20 against it. Hand-picked numbers in two files is how two
//     components end up in the wrong order with neither one looking wrong.
//
//  2. The inspector anchored to the VIEWPORT top (`top: var(--b5-space-4)`),
//     so even with the layering corrected it would still start behind the
//     sticky chrome. It now offsets by the chrome's measured height.
//
// These are asserted against the literal CSS and JS the browser loads. No
// Go type sees any of this, and a screenshot is the only other way to catch
// it — which is to say it would have been caught the next time an operator
// opened the screen, in a venue.
func TestInspectorClearsAppChrome(t *testing.T) {
	devices := readAsset(t, "static/css/screens-devices.css")
	workspace := readAsset(t, "static/css/workspace.css")
	app := readAsset(t, "static/js/app.js")

	inspector := ruleBody(t, devices, ".b5-inspector {")
	context := ruleBody(t, workspace, ".b5-show-context {")

	// --- 1. both on the shared scale ---------------------------------------
	for _, c := range []struct{ name, body string }{
		{".b5-inspector", inspector},
		{".b5-show-context", context},
	} {
		z := declaration(c.body, "z-index")
		if z == "" {
			t.Errorf("%s declares no z-index; it needs one from the shared scale", c.name)
			continue
		}
		if !strings.Contains(z, "--b5-z-") {
			t.Errorf("%s has a hand-picked z-index (%s) instead of a --b5-z-* token. "+
				"That is the defect: this rule and the other one were picked independently, "+
				"in different files, and put a chrome strip above a drawer's close button.",
				c.name, z)
		}
	}

	// --- 2. and in the right order -----------------------------------------
	// Resolved through the token table so the test fails on a REORDERING of
	// the scale, not merely on the literal token names.
	tokens := zIndexTokens(t, readAsset(t, "static/css/benny512-tokens.css"))
	zi, ok1 := resolveZ(declaration(inspector, "z-index"), tokens)
	zc, ok2 := resolveZ(declaration(context, "z-index"), tokens)
	if ok1 && ok2 && zi <= zc {
		t.Errorf("inspector z-index (%d) is not above the show-context strip (%d) — "+
			"the inspector bar, and its close button, render behind the strip", zi, zc)
	}

	// --- 3. and starting below the chrome, by MEASUREMENT -------------------
	top := declaration(inspector, "top")
	if !strings.Contains(top, "--b5-chrome-height") {
		t.Errorf("`.b5-inspector` top is %q, which does not account for the sticky app "+
			"chrome. Anchoring to the viewport top puts the drawer's bar behind "+
			"`.b5-header` + `.b5-show-context`.", top)
	}
	// A constant would be wrong exactly where it matters: .b5-show-context
	// wraps and caps the show name at 35vw, so the chrome is two rows tall on
	// a tablet in portrait — the venue case this app exists for.
	if !strings.Contains(app, "--b5-chrome-height") {
		t.Error("app.js never sets --b5-chrome-height, so the inspector's top falls back " +
			"to a constant forever and clips again as soon as the show-context strip wraps")
	}
	for _, want := range []string{".b5-header", ".b5-show-context", "getBoundingClientRect"} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js's chrome measurement does not reference %s — it must measure "+
				"BOTH sticky bars, and measure them rather than assume a height", want)
		}
	}
}

func readAsset(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the literal asset the browser loads (%s): %v", path, err)
	}
	return string(b)
}

// ruleBody returns the declarations of the first rule whose opening line is
// selector, e.g. ".b5-inspector {".
func ruleBody(t *testing.T, css, selector string) string {
	t.Helper()
	i := strings.Index(css, selector)
	if i < 0 {
		t.Fatalf("could not find %q — the rule was renamed; re-scope this test rather "+
			"than deleting it, the layering it guards is invisible to every other test", selector)
	}
	rest := css[i+len(selector):]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("unterminated rule for %q", selector)
	}
	return rest[:end]
}

// declaration returns the value of prop within a rule body, or "".
func declaration(body, prop string) string {
	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		// Trim any comment that ran up to this declaration.
		if k := strings.LastIndex(line, "*/"); k >= 0 {
			line = strings.TrimSpace(line[k+2:])
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == prop {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func zIndexTokens(t *testing.T, css string) map[string]int {
	t.Helper()
	re := regexp.MustCompile(`(--b5-z-[a-z]+)\s*:\s*(\d+)`)
	out := map[string]int{}
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out[m[1]] = n
	}
	if len(out) == 0 {
		t.Fatal("no --b5-z-* tokens found in benny512-tokens.css")
	}
	return out
}

// resolveZ turns either a raw number or a var(--b5-z-*) reference into its
// numeric layer, so ordering is checked on what the browser computes.
func resolveZ(value string, tokens map[string]int) (int, bool) {
	value = strings.TrimSpace(value)
	if n, err := strconv.Atoi(value); err == nil {
		return n, true
	}
	m := regexp.MustCompile(`var\(\s*(--b5-z-[a-z]+)`).FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	n, ok := tokens[m[1]]
	return n, ok
}
