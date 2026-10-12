package web

import (
	"encoding/json"
	"io/fs"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// C9a — Dark and Daylight themes, switched per browser.
// theme-contract.md: component CSS uses roles, and a theme sets the roles,
// so every role a component reads must be defined by BOTH theme blocks of
// css/console-tokens.css — a role only the base token file defines keeps
// its old (unchecked) value whatever the theme.

var c9VarRE = regexp.MustCompile(`var\(--b5-color-([a-z0-9-]+)`)

func TestC9EveryUsedColourRoleIsThemed(t *testing.T) {
	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	css, _ := fs.Glob(embeddedUI, "static/css/*.css")
	js, _ := fs.Glob(embeddedUI, "static/js/*.js")
	missing := map[string][]string{}
	for _, f := range append(css, js...) {
		if i1bTokenFiles[f] {
			continue
		}
		src := i1bCommentRE.ReplaceAllString(i1Embedded(t, f), "")
		for _, m := range c9VarRE.FindAllStringSubmatch(src, -1) {
			role := m[1]
			if i1RampRE.MatchString("--b5-color-" + role) {
				continue // ramps are TestI1bNoColourOutsideTokenFiles's business
			}
			_, d := tok.Dark[role]
			_, l := tok.Light[role]
			if !d || !l {
				missing[role] = append(missing[role], strings.TrimPrefix(f, "static/"))
			}
		}
	}
	names := make([]string, 0, len(missing))
	for r := range missing {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, r := range names {
		t.Errorf("--b5-color-%s is read by %v but not defined by both theme blocks of console-tokens.css", r, uniq(missing[r]))
	}
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TestC9DarkAndDaylightDefineSameRoles: a role in one theme block and not the
// other would keep the other theme's value when the theme changes.
func TestC9DarkAndDaylightDefineSameRoles(t *testing.T) {
	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Dark) < 60 {
		t.Fatalf("parsed only %d dark roles", len(tok.Dark))
	}
	for r := range tok.Dark {
		if _, ok := tok.Light[r]; !ok {
			t.Errorf("--b5-color-%s is defined for Dark but not for Daylight", r)
		}
	}
	for r := range tok.Light {
		if _, ok := tok.Dark[r]; !ok {
			t.Errorf("--b5-color-%s is defined for Daylight but not for Dark", r)
		}
	}
}

// TestC9BuiltInThemesPassContract re-checks every pair of the shipped Dark
// and Daylight themes with the contract's own maths (unrounded), including
// the I1b roles bg-pressed, info-border and info-fill; scrim is translucent
// and exempt (dialogs sit on their own opaque surface).
func TestC9BuiltInThemesPassContract(t *testing.T) {
	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"bg-pressed", "info-border", "info-fill"} {
		found := false
		for _, x := range tok.Roles {
			found = found || x == r
		}
		if !found {
			t.Fatalf("role %s is not checked (not an opaque colour in both blocks)", r)
		}
	}
	for _, theme := range []struct {
		base string
		vals map[string]string
	}{{"dark", tok.colours("dark")}, {"light", tok.colours("light")}} {
		fails := validateTheme(theme.base, theme.vals, tok.Preview[theme.base], tok.Roles, themeLabels)
		for _, f := range fails {
			t.Errorf("%s: %s", theme.base, f.Message)
		}
		for _, r := range []string{"bg-pressed", "info-border", "info-fill"} {
			rule := themeRuleFor(r)
			worst, against := 99.0, ""
			for _, a := range rule.Against {
				if c := themeContrast(theme.vals[r], theme.vals[a]); c < worst {
					worst, against = c, a
				}
			}
			t.Logf("%s %s %s: worst %.4f:1 against %s (needs %v)", theme.base, r, theme.vals[r], worst, against, rule.Min)
		}
	}
}

// TestC9ThemeSwitchUI runs static/js/testdata/theme_switch_test.js: the
// literal theme.js and theme-settings.js — Dark/Daylight per browser, live,
// stored in localStorage with a safe fallback, set before first paint.
func TestC9ThemeSwitchUI(t *testing.T) { runNodeTest(t, "theme_switch_test.js") }

// --- C9b: custom themes ------------------------------------------------------

// c9Dark is the shipped Dark theme as a custom-theme body.
func c9Dark(t *testing.T) map[string]string {
	t.Helper()
	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return tok.colours("dark")
}

func c9Body(name, base string, colours map[string]string, preview float64) map[string]any {
	return map[string]any{"name": name, "base": base, "colors": colours, "preview": preview}
}

type c9Theme struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Base     string            `json:"base"`
	Colors   map[string]string `json:"colors"`
	Preview  float64           `json:"preview"`
	Failures []themeFailure    `json:"failures"`
}

func c9List(t *testing.T, h http.Handler) []c9Theme {
	t.Helper()
	rr := doJSON(t, h, "GET", "/api/themes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/themes: %d %s", rr.Code, rr.Body.String())
	}
	var v struct {
		Roles  []string                     `json:"roles"`
		Labels map[string]string            `json:"labels"`
		Bases  map[string]map[string]string `json:"bases"`
		Themes []c9Theme                    `json:"themes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Roles) < 60 || len(v.Bases["dark"]) != len(v.Roles) || len(v.Bases["light"]) != len(v.Roles) || v.Labels["text-muted"] != "Muted text" {
		t.Fatalf("GET /api/themes: %d roles, %d/%d base values, label %q", len(v.Roles), len(v.Bases["dark"]), len(v.Bases["light"]), v.Labels["text-muted"])
	}
	return v.Themes
}

// TestC9CustomThemesAPI: custom themes are station settings (every browser
// can pick them), validated by the SERVER with the contract on every save —
// the browser is never trusted — and survive POST /api/settings, which
// settings.js sends without them.
func TestC9CustomThemesAPI(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatal(err)
	}
	handler := h.srv.Handler()
	dark := c9Dark(t)

	// A failing pair: muted text darkened until it fails on overlay.
	bad := map[string]string{}
	for k, v := range dark {
		bad[k] = v
	}
	bad["text-muted"] = "#7a7062"
	rr := doJSON(t, handler, "POST", "/api/themes", c9Body("Dim", "dark", bad, 0.12))
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "Muted text on overlay is") || !strings.Contains(rr.Body.String(), "needs 4.5:1.") {
		t.Errorf("failing theme: want 422 naming the pair in words, got %d %s", rr.Code, rr.Body.String())
	}
	if got := c9List(t, handler); len(got) != 0 {
		t.Errorf("a refused theme was stored: %+v", got)
	}

	// Refusals: alpha / short hex, an unknown role, a missing role, too
	// bright for a dark venue, equal tone outlines, preview above 0.15.
	refuse := func(what string, mutate func(m map[string]string), base string, preview float64, want string) {
		t.Helper()
		c := map[string]string{}
		for k, v := range dark {
			c[k] = v
		}
		mutate(c)
		rr := doJSON(t, handler, "POST", "/api/themes", c9Body("X "+what, base, c, preview))
		if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("%s: want 422 containing %q, got %d %s", what, want, rr.Code, rr.Body.String())
		}
	}
	refuse("alpha", func(m map[string]string) { m["bg-canvas"] = "#100e0b80" }, "dark", 0.12, "Canvas must be an opaque colour written #rrggbb.")
	refuse("rgba", func(m map[string]string) { m["bg-canvas"] = "rgba(0,0,0,.5)" }, "dark", 0.12, "Canvas must be an opaque colour")
	refuse("unknown role", func(m map[string]string) { m["font-size"] = "#000000" }, "dark", 0.12, `\"font-size\" is not a theme colour.`)
	refuse("missing role", func(m map[string]string) { delete(m, "thumb") }, "dark", 0.12, "Fader thumb is missing.")
	refuse("bright canvas", func(m map[string]string) { m["bg-canvas"] = "#2a2a2a" }, "dark", 0.12, "Canvas is too bright for a dark theme")
	refuse("equal tones", func(m map[string]string) { m["armed-border"] = m["danger-border"] }, "dark", 0.12, "Armed outline and danger outline are the same colour")
	refuse("preview", func(m map[string]string) {}, "dark", 0.2, "Live-preview intensity must be more than 0 and at most 0.15.")
	refuse("base", func(m map[string]string) {}, "sepia", 0.12, "Base must be Dark or Daylight.")

	// A valid theme saves, persists to the settings file, and is listed.
	good := map[string]string{}
	for k, v := range dark {
		good[k] = v
	}
	good["accent-border"] = "#86d6d0"
	rr = doJSON(t, handler, "POST", "/api/themes", c9Body("  Warm FOH  ", "dark", good, 0.1))
	if rr.Code != http.StatusOK {
		t.Fatalf("valid theme: %d %s", rr.Code, rr.Body.String())
	}
	var saved c9Theme
	_ = json.Unmarshal(rr.Body.Bytes(), &saved)
	if saved.ID == "" || saved.Name != "Warm FOH" || saved.Colors["accent-border"] != "#86d6d0" || saved.Preview != 0.1 {
		t.Errorf("saved theme: %+v", saved)
	}
	if rr := doJSON(t, handler, "POST", "/api/themes", c9Body("warm foh", "dark", good, 0.1)); rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "already") {
		t.Errorf("duplicate name: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doJSON(t, handler, "POST", "/api/themes", c9Body("Daylight", "light", tokColoursLight(t), 0.12)); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("a custom theme named like a built-in one: %d %s", rr.Code, rr.Body.String())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"customThemes"`) || !strings.Contains(string(raw), "Warm FOH") {
		t.Errorf("settings file does not carry the theme:\n%s", raw)
	}

	// settings.js's Apply sends the station settings without customThemes;
	// that must not delete them.
	if rr := doJSON(t, handler, "POST", "/api/settings", map[string]any{"pollIntervalMs": 3000, "captureLimit": 10000, "timeoutProfiles": map[string]string{}, "leaseLossAction": "blackout", "universeProtocols": []any{}}); rr.Code != http.StatusOK {
		t.Fatalf("POST /api/settings: %d %s", rr.Code, rr.Body.String())
	}
	if got := c9List(t, handler); len(got) != 1 || got[0].ID != saved.ID || len(got[0].Failures) != 0 {
		t.Fatalf("after Apply settings: %+v", got)
	}

	// Rename = save again by id; a reload of the settings file keeps it.
	ren := c9Body("Front of house", "dark", good, 0.1)
	ren["id"] = saved.ID
	if rr := doJSON(t, handler, "POST", "/api/themes", ren); rr.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatal(err)
	}
	if got := c9List(t, handler); len(got) != 1 || got[0].Name != "Front of house" {
		t.Fatalf("after rename and reload: %+v", got)
	}
	// An id that does not exist is not silently created.
	ghost := c9Body("Ghost", "dark", good, 0.1)
	ghost["id"] = "nope"
	if rr := doJSON(t, handler, "POST", "/api/themes", ghost); rr.Code != http.StatusNotFound {
		t.Errorf("save to an unknown id: %d", rr.Code)
	}

	// A theme stored before a contract change (or edited by hand) is listed
	// with its failures, so no browser applies it.
	h.srv.settingsMu.Lock()
	h.srv.settings.CustomThemes[0].Colors["text-primary"] = "#555555"
	h.srv.settingsMu.Unlock()
	if got := c9List(t, handler); len(got[0].Failures) == 0 || !strings.Contains(got[0].Failures[0].Message, "Primary text on") {
		t.Errorf("an invalid stored theme is listed without its failures: %+v", got[0].Failures)
	}

	if rr := doJSON(t, handler, "DELETE", "/api/themes/"+saved.ID, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if got := c9List(t, handler); len(got) != 0 {
		t.Errorf("after delete: %+v", got)
	}
	if rr := doJSON(t, handler, "DELETE", "/api/themes/"+saved.ID, nil); rr.Code != http.StatusNotFound {
		t.Errorf("second delete: %d", rr.Code)
	}
}

func tokColoursLight(t *testing.T) map[string]string {
	t.Helper()
	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return tok.colours("light")
}

// TestC9ContractJSAgreesWithGo: the editor (static/js/theme.js) and the
// server (theme.go) carry one contract each. Both must reproduce the
// design package's recorded ratios (contrast.json, every pair) and tone
// distances (theme-validation.json), and fail the same drafts with the
// same words.
func TestC9ContractJSAgreesWithGo(t *testing.T) {
	nodePath := nodeOrSkip(t)
	var contrast struct {
		Report []struct {
			Theme, Role, Hex string
			Pairs            []struct {
				Against string
				Ratio   float64
			}
		}
	}
	raw, err := os.ReadFile("../../docs/design/console-lite/contrast.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &contrast); err != nil {
		t.Fatal(err)
	}
	hex := map[string]map[string]string{"dark": {}, "light": {}}
	for _, r := range contrast.Report {
		hex[r.Theme][r.Role] = r.Hex
	}
	type fx struct {
		a, b string
		want float64
	}
	var pairs []fx
	for _, r := range contrast.Report {
		for _, p := range r.Pairs {
			pairs = append(pairs, fx{r.Hex, hex[r.Theme][p.Against], p.Ratio})
		}
	}
	var validation struct {
		ToneDistances []struct {
			Theme, A, B string
			Delta       float64
		}
	}
	raw, err = os.ReadFile("../../docs/design/console-lite/theme-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &validation); err != nil {
		t.Fatal(err)
	}
	var tones []fx
	for _, d := range validation.ToneDistances {
		tones = append(tones, fx{hex[d.Theme][d.A], hex[d.Theme][d.B], d.Delta})
	}
	if len(pairs) < 1000 || len(tones) < 20 {
		t.Fatalf("fixtures: %d pairs, %d tone distances", len(pairs), len(tones))
	}

	tok, err := parseThemeTokens(i1Embedded(t, "static/css/console-tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	type draft struct {
		Base    string            `json:"base"`
		Colors  map[string]string `json:"colors"`
		Preview float64           `json:"preview"`
	}
	mut := func(base string, preview float64, f func(m map[string]string)) draft {
		m := tok.colours(base)
		f(m)
		return draft{base, m, preview}
	}
	drafts := []draft{
		mut("dark", 0.12, func(map[string]string) {}),
		mut("light", 0.12, func(map[string]string) {}),
		mut("dark", 0.12, func(m map[string]string) { m["text-muted"] = "#7a7062" }),
		mut("light", 0.12, func(m map[string]string) { m["text-muted"] = "#8a8070"; m["thumb"] = "#e7e4dc" }),
		mut("dark", 0.12, func(m map[string]string) {
			m["bg-canvas"] = "#2a2a2a"
			m["grid-bg"] = "#333333"
			m["armed-fill"] = "#40304f"
		}),
		mut("dark", 0.12, func(m map[string]string) { m["armed-border"] = m["danger-border"]; m["info-border"] = "#80d1ca" }),
		mut("dark", 0.15, func(m map[string]string) { m["bg-surface-overlay"] = "#221d18" }),
		mut("dark", 0.2, func(map[string]string) {}),
		mut("light", 0, func(map[string]string) {}),
		mut("dark", 0.12, func(m map[string]string) { delete(m, "thumb"); m["track"] = "#0b0a0880"; m["font-size"] = "#000000" }),
		mut("sepia", 0.12, func(map[string]string) {}),
		mut("dark", 0.12, func(m map[string]string) { m["disarm-slab-bg"] = "#c04040"; m["disarm-slab-text"] = "#ffdddd" }),
	}
	in := map[string]any{"roles": tok.Roles, "labels": themeLabels, "drafts": drafts}
	var ps, ts [][2]string
	for _, p := range pairs {
		ps = append(ps, [2]string{p.a, p.b})
	}
	for _, p := range tones {
		ts = append(ts, [2]string{p.a, p.b})
	}
	in["pairs"], in["tones"] = ps, ts
	inPath := filepath.Join(t.TempDir(), "in.json")
	b, _ := json.Marshal(in)
	if err := os.WriteFile(inPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	jsDir, _ := filepath.Abs("static/js")
	cmd := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "theme_contract_dump.js"), jsDir, inPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	outB, err := cmd.Output()
	if err != nil {
		t.Fatalf("theme_contract_dump.js: %v\n%s", err, stderr.String())
	}
	var out struct {
		Ratios   []float64        `json:"ratios"`
		Deltas   []float64        `json:"deltas"`
		Failures [][]themeFailure `json:"failures"`
	}
	if err := json.Unmarshal(outB, &out); err != nil {
		t.Fatalf("%v\n%s", err, outB)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }
	bad := 0
	for i, p := range pairs {
		g := themeContrast(p.a, p.b)
		if !near(g, p.want) || !near(out.Ratios[i], p.want) {
			if bad++; bad < 10 {
				t.Errorf("pair %s/%s: contrast.json %.15g, Go %.15g, JS %.15g", p.a, p.b, p.want, g, out.Ratios[i])
			}
		}
	}
	for i, p := range tones {
		g := themeDeltaE(p.a, p.b)
		if !near(g, p.want) || !near(out.Deltas[i], p.want) {
			t.Errorf("tone %s/%s: theme-validation.json %.15g, Go %.15g, JS %.15g", p.a, p.b, p.want, g, out.Deltas[i])
		}
	}
	for i, d := range drafts {
		g := validateTheme(d.Base, d.Colors, d.Preview, tok.Roles, themeLabels)
		gj, _ := json.Marshal(g)
		jj, _ := json.Marshal(out.Failures[i])
		if string(gj) != string(jj) {
			t.Errorf("draft %d (%s): Go and JS disagree\n Go: %s\n JS: %s", i, d.Base, gj, jj)
		}
		if i >= 2 && len(g) == 0 {
			t.Errorf("draft %d was meant to fail and passes", i)
		}
		if testing.Verbose() {
			for _, f := range g {
				t.Logf("draft %d: %s", i, f.Message)
			}
		}
	}
	t.Logf("%d contrast.json pairs and %d tone distances reproduced by both; %d drafts agree", len(pairs), len(tones), len(drafts))
}

// TestC9ThemeEditorUI runs static/js/testdata/theme_editor_test.js against
// this real server: the custom theme editor's live validation, preview,
// Revert, Save, rename, export/import and Delete.
func TestC9ThemeEditorUI(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "theme_editor_test.js"), jsDir, ts.URL).CombinedOutput()
	t.Logf("theme_editor_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("theme_editor_test.js failed: %v", err)
	}
}

// --- C9c: Daylight previews at full brightness (owner, 2026-10-09 23:12) ---

// c9EffectiveVar resolves a custom property for one theme the way the
// browser's cascade does for this file: every rule whose selector list
// holds :root or [data-theme="<theme>"] applies (all specificity 0,1,0),
// and the last declaration in file order wins. Written here, apart from
// theme.go, so the test does not agree with itself.
func c9EffectiveVar(css, theme, name string) string {
	css = i1bCommentRE.ReplaceAllString(css, "")
	val := ""
	for _, m := range i1bRuleRE.FindAllStringSubmatch(css, -1) {
		applies := false
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.TrimSpace(sel)
			applies = applies || sel == ":root" || sel == `[data-theme="`+theme+`"]`
		}
		if !applies {
			continue
		}
		for _, decl := range strings.Split(m[2], ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(decl), ":")
			if ok && strings.TrimSpace(k) == name {
				val = strings.TrimSpace(v)
			}
		}
	}
	return val
}

// TestC9DaylightPreviewFullBrightness: the 0.15 cap and 0.12 default are
// dark-venue rules (theme-contract.md "Dark venue limits"); in Daylight the
// colour picker's swatches show at full brightness, and the editor's
// Daylight default follows. Dark keeps 0.12 / 0.15.
func TestC9DaylightPreviewFullBrightness(t *testing.T) {
	css := i1Embedded(t, "static/css/console-tokens.css")
	if got := c9EffectiveVar(css, "dark", "--b5-preview-max-brightness"); got != "0.12" {
		t.Errorf("Dark preview intensity = %q, want 0.12", got)
	}
	if got := c9EffectiveVar(css, "light", "--b5-preview-max-brightness"); got != "1" {
		t.Errorf("Daylight preview intensity = %q, want 1 (full brightness)", got)
	}
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/themes", nil)
	var v struct {
		Preview    map[string]float64 `json:"preview"`
		PreviewMax map[string]float64 `json:"previewMax"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("GET /api/themes preview is not a per-base default: %v\n%s", err, rr.Body.String())
	}
	if v.Preview["dark"] != 0.12 || v.Preview["light"] != 1 || v.PreviewMax["dark"] != 0.15 || v.PreviewMax["light"] != 1 {
		t.Errorf("GET /api/themes: defaults %v, maxima %v; want dark 0.12 / 0.15, light 1 / 1", v.Preview, v.PreviewMax)
	}
	// A custom theme on Daylight saves at 1.0; on Dark 1.0 is still refused.
	tok, _ := parseThemeTokens(css)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/themes", c9Body("Sun", "light", tok.colours("light"), 1)); rr.Code != http.StatusOK {
		t.Errorf("Daylight custom theme at 1.0: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/themes", c9Body("Moon", "dark", tok.colours("dark"), 1)); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("Dark custom theme at 1.0 must be refused: %d", rr.Code)
	}
}
