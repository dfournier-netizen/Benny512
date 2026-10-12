package web

import (
	"encoding/json"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// I1 — Console-lite design integration: semantic role tokens, the icon
// sprite and fixture glyphs, and kit components routed through roles.
//
// Every check reads the files through embeddedUI, i.e. the exact bytes the
// binary serves, and compares them with the design package in
// docs/design/console-lite (the handoff source of truth).

const i1DesignDir = "../../docs/design/console-lite/"

// i1PreexistingSymbols is the production sprite as it stood before I1. The
// design handoff says "append ... retaining all existing symbols"; losing
// one of these breaks a screen that already ships.
var i1PreexistingSymbols = []string{
	"b5-icon-apply", "b5-icon-chevron-collapse", "b5-icon-chevron-expand",
	"b5-icon-export", "b5-icon-filter", "b5-icon-identify",
	"b5-icon-nav-analyzer", "b5-icon-nav-devices", "b5-icon-nav-nodes",
	"b5-icon-nav-rig-walk", "b5-icon-nav-send", "b5-icon-nav-settings",
	"b5-icon-network-node", "b5-icon-refresh", "b5-icon-revert",
	"b5-icon-signal", "b5-icon-sort-asc", "b5-icon-sort-desc",
	"b5-icon-status-error", "b5-icon-status-ok", "b5-icon-status-pending",
	"b5-icon-status-warning",
}

func i1Embedded(t *testing.T, path string) string {
	t.Helper()
	b, err := embeddedUI.ReadFile(path)
	if err != nil {
		t.Fatalf("reading embedded %s (what the binary serves): %v", path, err)
	}
	return string(b)
}

func i1Design(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(i1DesignDir + name)
	if err != nil {
		t.Fatalf("reading design package %s: %v", name, err)
	}
	return string(b)
}

var i1SymbolRE = regexp.MustCompile(`(?s)<symbol id="([^"]+)".*?</symbol>`)

func TestI1SpriteCarriesDesignAndExistingSymbols(t *testing.T) {
	sprite := i1Embedded(t, "static/icons/benny512-icons.svg")

	seen := map[string]int{}
	for _, m := range i1SymbolRE.FindAllStringSubmatch(sprite, -1) {
		seen[m[1]]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("sprite defines #%s %d times; a duplicate id makes <use> resolve to whichever comes first", id, n)
		}
	}
	for _, id := range i1PreexistingSymbols {
		if seen[id] == 0 {
			t.Errorf("pre-existing symbol #%s is missing from the sprite", id)
		}
	}

	var manifest struct {
		Icons    []string `json:"icons"`
		Fixtures []string `json:"fixtures"`
	}
	if err := json.Unmarshal([]byte(i1Design(t, "asset-manifest.json")), &manifest); err != nil {
		t.Fatalf("asset-manifest.json: %v", err)
	}
	if len(manifest.Icons) != 72 || len(manifest.Fixtures) != 8 {
		t.Fatalf("manifest lists %d icons / %d fixtures; the handoff is 72 / 8", len(manifest.Icons), len(manifest.Fixtures))
	}
	missing := 0
	for _, name := range append(append([]string{}, manifest.Icons...), manifest.Fixtures...) {
		if seen["b5-icon-"+name] == 0 {
			missing++
			t.Errorf("sprite lacks #b5-icon-%s from asset-manifest.json", name)
		}
	}
	if missing > 0 {
		t.Logf("%d of %d manifest symbols missing", missing, len(manifest.Icons)+len(manifest.Fixtures))
	}

	// Geometry must arrive unaltered: each design symbol appears verbatim.
	for _, f := range []string{"console-icons.svg", "fixture-glyphs.svg"} {
		for _, m := range i1SymbolRE.FindAllStringSubmatch(i1Design(t, f), -1) {
			if seen[m[1]] > 0 && !strings.Contains(sprite, m[0]) {
				t.Errorf("sprite #%s differs from %s; symbols must be appended verbatim", m[1], f)
			}
		}
	}
}

// i1DarkBlock returns the custom-property declarations of the first
// `:root, [data-theme="dark"]` block in css.
func i1DarkBlock(css string) map[string]string {
	out := map[string]string{}
	i := strings.Index(css, `:root, [data-theme="dark"] {`)
	if i < 0 {
		return out
	}
	body := css[i:]
	body = body[strings.Index(body, "{")+1 : strings.Index(body, "}")]
	body = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(body, "")
	for _, decl := range strings.Split(body, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(decl), ":")
		if ok && strings.HasPrefix(name, "--") {
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return out
}

func TestI1RoleTokensLoadAfterBaseTokens(t *testing.T) {
	index := i1Embedded(t, "static/index.html")
	base := strings.Index(index, `href="/css/benny512-tokens.css"`)
	roles := strings.Index(index, `href="/css/console-tokens.css"`)
	firstComponent := strings.Index(index, `href="/css/benny512-layout.css"`)
	switch {
	case base < 0:
		t.Fatal("index.html does not load benny512-tokens.css")
	case roles < 0:
		t.Fatal("index.html does not load the semantic-role layer /css/console-tokens.css")
	case roles < base:
		t.Error("console-tokens.css loads before benny512-tokens.css; the base file would override the repaired roles")
	case firstComponent >= 0 && roles > firstComponent:
		t.Error("console-tokens.css must load with the tokens, before any component stylesheet")
	}

	want := i1DarkBlock(i1Design(t, "console-tokens.css"))
	if len(want) < 60 {
		t.Fatalf("parsed only %d dark roles from the design console-tokens.css", len(want))
	}
	got := i1DarkBlock(i1Embedded(t, "static/css/console-tokens.css"))
	for name, v := range want {
		if got[name] != v {
			t.Errorf("dark role %s = %q in the app, design specifies %q", name, got[name], v)
		}
	}
}

// Palette ramps are private to benny512-tokens.css (theme-contract.md,
// "Component CSS uses roles; palette ramps remain private").
var i1RampRE = regexp.MustCompile(`--b5-color-(neutral|accent|info|danger|success|warning)-[0-9]{3}`)

// i1MappedSelector reports whether a selector belongs to the kit components
// theme-contract.md "Non-themeable structure" names for role routing, and
// whether only its border declarations are in scope (normal buttons).
func i1MappedSelector(sel string) (mapped, bordersOnly bool) {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\.b5-bigbtn--(go|stop)([^\w-]|$)`),
		regexp.MustCompile(`\.b5-pill--(ok|warn|danger|info|accent)([^\w-]|$)`),
		regexp.MustCompile(`\.b5-range-touch([^\w-]|$)`),
		regexp.MustCompile(`\.b5-statecard\.is-armed([^\w-]|$)`),
		// The output strip's ARM/DISARM is a .b5-btn whose border these
		// classes override: an essential button border in all but name.
		regexp.MustCompile(`\.b5-out-arm--(go|stop)([^\w-]|$)`),
	} {
		if re.MatchString(sel) {
			return true, false
		}
	}
	if regexp.MustCompile(`\.b5-btn(--[a-z]+)?([^\w-]|$)`).MatchString(sel) {
		return true, true
	}
	return false, false
}

func TestI1KitComponentsUseRoles(t *testing.T) {
	files, err := fs.Glob(embeddedUI, "static/css/*.css")
	if err != nil || len(files) == 0 {
		t.Fatalf("listing embedded css: %v", err)
	}
	ruleRE := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	commentRE := regexp.MustCompile(`(?s)/\*.*?\*/`)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "benny512-tokens.css") || strings.HasSuffix(f, "console-tokens.css") {
			continue // the token layers are where ramps legitimately live
		}
		css := commentRE.ReplaceAllString(i1Embedded(t, f), "")
		for _, m := range ruleRE.FindAllStringSubmatch(css, -1) {
			sel := strings.TrimSpace(m[1])
			mapped, bordersOnly := i1MappedSelector(sel)
			if !mapped {
				continue
			}
			checked++
			for _, decl := range strings.Split(m[2], ";") {
				name, value, ok := strings.Cut(strings.TrimSpace(decl), ":")
				if !ok {
					continue
				}
				name = strings.TrimSpace(name)
				if bordersOnly && !strings.HasPrefix(name, "border") {
					continue
				}
				if i1RampRE.MatchString(value) {
					t.Errorf("%s: `%s { %s: %s }` uses a palette ramp; route it through a semantic role",
						strings.TrimPrefix(f, "static/"), sel, name, strings.TrimSpace(value))
				}
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d mapped rules found; selector matching is broken", checked)
	}

	// Normal essential buttons take the validated control border, not the
	// decorative default (theme-contract.md: "Map normal essential buttons
	// to border-control").
	comp := commentRE.ReplaceAllString(i1Embedded(t, "static/css/benny512-components.css"), "")
	m := regexp.MustCompile(`(?m)^\.b5-btn \{([^}]*)\}`).FindStringSubmatch(comp)
	if m == nil {
		t.Fatal("no `.b5-btn {` rule in benny512-components.css")
	}
	if !strings.Contains(m[1], "var(--b5-color-border-control)") {
		t.Error("`.b5-btn` border does not use --b5-color-border-control")
	}
}
