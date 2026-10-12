package web

// C9 colour themes — the server's copy of the theme contract
// (docs/design/console-lite/theme-contract.md, the contrast maths of
// docs/design/console-lite/build-assets.mjs). static/js/theme.js carries the
// browser's copy; console_c9_test.go runs both over the same fixtures and
// requires them to agree, so the editor and the server can never pass
// different themes.
//
// The role list and the two built-in themes are read from the embedded
// css/console-tokens.css, the same file the browser loads: there is no
// second list of roles to drift.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// themeSurfaces are the backgrounds text, icons and essential borders may sit
// on (contrast.json "surfaces"), plus the two I1b surfaces bg-pressed and
// info-fill, which carry text in production.
var themeSurfaces = []string{
	"bg-canvas", "bg-surface", "bg-surface-raised", "bg-surface-overlay", "bg-inset", "bg-hover", "bg-selected",
	"grid-bg", "xypad-bg", "ok-fill", "warn-fill", "danger-fill", "accent-fill", "armed-fill",
	"bg-pressed", "info-fill",
}

// themeText45 are the non text-* roles that are read as text (≥4.5:1).
var themeText45 = map[string]bool{"armed-text": true, "arm-pill-text": true, "simulated-text": true,
	"grid-layer-label": true, "value-set": true, "value-default": true, "value-test": true}

// themeDecorative are separators and grid lines: exempt, never essential.
var themeDecorative = map[string]bool{"border-subtle": true, "border-default": true, "grid-line": true, "grid-line-major": true}

// themeRule is one role's contract: its minimum against each listed role.
// background is true when the role is the background of the pair (the
// listed roles are the foregrounds read on it).
type themeRule struct {
	Against    []string
	Min        float64
	Background bool
}

// themeRuleFor is build-assets.mjs contract() with the I1b roles.
func themeRuleFor(key string) themeRule {
	textOn := []string{"text-primary", "text-secondary", "text-muted"}
	trackAnd := append([]string{"track"}, themeSurfaces...)
	switch {
	case key == "disarm-slab-bg":
		return themeRule{Against: []string{"disarm-slab-text"}, Min: 4.5, Background: true}
	case key == "disarm-slab-text" || key == "text-on-danger":
		return themeRule{Against: []string{"disarm-slab-bg"}, Min: 4.5}
	case key == "text-on-accent":
		return themeRule{Against: []string{"thumb"}, Min: 4.5}
	case key == "text-disabled":
		return themeRule{Against: themeSurfaces, Min: 0}
	case key == "text-primary":
		return themeRule{Against: themeSurfaces, Min: 7}
	case strings.HasPrefix(key, "text-") || themeText45[key]:
		return themeRule{Against: themeSurfaces, Min: 4.5}
	case themeDecorative[key]:
		return themeRule{Against: []string{"grid-bg", "bg-surface"}, Min: 0}
	case key == "thumb-border" || key == "track-border" || key == "thumb":
		return themeRule{Against: trackAnd, Min: 3}
	case strings.Contains(key, "border") || strings.HasPrefix(key, "fixture-") || strings.Contains(key, "crosshair") || strings.Contains(key, "mixed-band"):
		return themeRule{Against: themeSurfaces, Min: 3}
	}
	return themeRule{Against: textOn, Min: 4.5, Background: true}
}

// Dark-venue limits (theme-contract.md "Dark venue limits").
var (
	themeDarkLimit020 = []string{"bg-canvas", "bg-surface"}
	themeDarkLimit035 = []string{"bg-surface-raised", "bg-surface-overlay", "bg-inset", "bg-hover", "bg-selected", "bg-pressed",
		"ok-fill", "warn-fill", "danger-fill", "accent-fill", "armed-fill", "info-fill"}
	// themeLargeRegions may not be brighter than overlay in a dark theme.
	themeLargeRegions = []string{"bg-canvas", "bg-surface", "grid-bg", "xypad-bg"}
	// themeToneRoles must stay apart (OKLab ΔE ≥ 0.04, never equal). The
	// contract names five; info-border (I1b) is a tone outline too.
	themeToneRoles = []string{"armed-border", "danger-border", "warn-border", "ok-border", "accent-border", "info-border"}
)

const (
	themeMinToneDelta   = 0.04
	themePreviewDarkMax = 0.15
)

func themeChannel(hex string, i int) float64 {
	v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// themeLuminance is WCAG relative luminance of an opaque #rrggbb.
func themeLuminance(hex string) float64 {
	return 0.2126*themeChannel(hex, 0) + 0.7152*themeChannel(hex, 1) + 0.0722*themeChannel(hex, 2)
}

// themeContrast is the WCAG contrast ratio, unrounded.
func themeContrast(a, b string) float64 {
	x, y := themeLuminance(a), themeLuminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// themeOKLab converts an opaque sRGB #rrggbb to OKLab (L in 0–1).
func themeOKLab(hex string) [3]float64 {
	r, g, b := themeChannel(hex, 0), themeChannel(hex, 1), themeChannel(hex, 2)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return [3]float64{
		0.2104542553*l + 0.793617785*m - 0.0040720468*s,
		1.9779984951*l - 2.428592205*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.808675766*s,
	}
}

func themeDeltaE(a, b string) float64 {
	x, y := themeOKLab(a), themeOKLab(b)
	return math.Sqrt((x[0]-y[0])*(x[0]-y[0]) + (x[1]-y[1])*(x[1]-y[1]) + (x[2]-y[2])*(x[2]-y[2]))
}

// themePreviewLuminance: the live-preview multiplier scales sRGB content
// before compositing, so the brightest preview (white) becomes sRGB m,m,m.
func themePreviewLuminance(m float64) float64 {
	if m <= 0.04045 {
		return m / 12.92
	}
	return math.Pow((m+0.055)/1.055, 2.4)
}

// themeFloorShow writes v for a "needs at least min" message: floored, with
// as many decimals as it takes for the shown number to fall below min, so a
// failing 4.47 never reads "4.5:1; needs 4.5:1".
func themeFloorShow(v, min float64, from int) string {
	for d := from; d <= 6; d++ {
		p := math.Pow(10, float64(d))
		s := math.Floor(v*p) / p
		if s < min || d == 6 {
			return strconv.FormatFloat(s, 'f', d, 64)
		}
	}
	return ""
}

// themeCeilShow is the "at most max" counterpart: rounded up until it reads
// above max.
func themeCeilShow(v, max float64) string {
	for d := 3; d <= 8; d++ {
		p := math.Pow(10, float64(d))
		s := math.Ceil(v*p) / p
		if s > max || d == 8 {
			return strconv.FormatFloat(s, 'f', d, 64)
		}
	}
	return ""
}

func themeNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// themeFailure is one reason a theme cannot be saved, in plain words.
type themeFailure struct {
	Kind    string `json:"kind"` // format | contrast | luminance | tone | preview
	Role    string `json:"role"`
	Against string `json:"against"`
	Message string `json:"message"`
}

var themeHexRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// themeLabelOf is a role's plain name; themeObjectOf the same name inside a
// sentence ("Muted text on overlay"): the first letter lowered unless the
// name starts with an abbreviation (OK, XY, SET).
func themeLabelOf(labels map[string]string, role string) string {
	if l, ok := labels[role]; ok && l != "" {
		return l
	}
	return role
}

func themeObjectOf(labels map[string]string, role string) string {
	l := themeLabelOf(labels, role)
	if len(l) > 1 && l[1] >= 'a' && l[1] <= 'z' && l[0] >= 'A' && l[0] <= 'Z' {
		return string(l[0]+('a'-'A')) + l[1:]
	}
	return l
}

// validateTheme checks a whole theme: base "dark" or "light", every role in
// roles as an opaque #rrggbb (lower case), and the live-preview intensity.
// It returns every failure, in a stable order; none means the theme passes.
func validateTheme(base string, colours map[string]string, preview float64, roles []string, labels map[string]string) []themeFailure {
	out := make([]themeFailure, 0)
	if base != "dark" && base != "light" {
		return append(out, themeFailure{Kind: "format", Message: `Base must be Dark or Daylight.`})
	}
	known := map[string]bool{}
	for _, r := range roles {
		known[r] = true
		v, ok := colours[r]
		switch {
		case !ok:
			out = append(out, themeFailure{Kind: "format", Role: r, Message: themeLabelOf(labels, r) + " is missing."})
		case !themeHexRE.MatchString(v):
			out = append(out, themeFailure{Kind: "format", Role: r, Message: themeLabelOf(labels, r) + " must be an opaque colour written #rrggbb."})
		}
	}
	extra := make([]string, 0)
	for k := range colours {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, themeFailure{Kind: "format", Role: k, Message: fmt.Sprintf("%q is not a theme colour.", k)})
	}
	if len(out) > 0 {
		return out
	}

	// Contrast: every pair once, as foreground on background, at the
	// highest minimum any rule gives it. Exempt (0) pairs are not checked.
	type pair struct {
		fg, bg string
		min    float64
	}
	pairs := make([]pair, 0)
	index := map[string]int{}
	for _, r := range roles {
		rule := themeRuleFor(r)
		if rule.Min <= 0 {
			continue
		}
		for _, a := range rule.Against {
			if _, ok := colours[a]; !ok {
				continue
			}
			fg, bg := r, a
			if rule.Background {
				fg, bg = a, r
			}
			k := fg + "|" + bg
			if i, ok := index[k]; ok {
				if rule.Min > pairs[i].min {
					pairs[i].min = rule.Min
				}
				continue
			}
			index[k] = len(pairs)
			pairs = append(pairs, pair{fg, bg, rule.Min})
		}
	}
	for _, p := range pairs {
		if c := themeContrast(colours[p.fg], colours[p.bg]); c < p.min {
			out = append(out, themeFailure{Kind: "contrast", Role: p.fg, Against: p.bg,
				Message: fmt.Sprintf("%s on %s is %s:1; needs %s:1.", themeLabelOf(labels, p.fg), themeObjectOf(labels, p.bg), themeFloorShow(c, p.min, 1), themeNum(p.min))})
		}
	}

	if base == "dark" {
		limit := func(list []string, max float64) {
			for _, r := range list {
				if y := themeLuminance(colours[r]); y > max {
					out = append(out, themeFailure{Kind: "luminance", Role: r,
						Message: fmt.Sprintf("%s is too bright for a dark theme: luminance %s; at most %s.", themeLabelOf(labels, r), themeCeilShow(y, max), themeNum(max))})
				}
			}
		}
		limit(themeDarkLimit020, 0.020)
		limit(themeDarkLimit035, 0.035)
		yo := themeLuminance(colours["bg-surface-overlay"])
		for _, r := range themeLargeRegions {
			if y := themeLuminance(colours[r]); y > yo {
				out = append(out, themeFailure{Kind: "luminance", Role: r, Against: "bg-surface-overlay",
					Message: fmt.Sprintf("%s is brighter than overlay (luminance %s against %s); a large area may not be brighter than overlay in a dark theme.", themeLabelOf(labels, r), themeCeilShow(y, yo), themeNum(math.Floor(yo*1e4)/1e4))})
			}
		}
	}

	for i := 0; i < len(themeToneRoles); i++ {
		for j := i + 1; j < len(themeToneRoles); j++ {
			a, b := themeToneRoles[i], themeToneRoles[j]
			if colours[a] == colours[b] {
				out = append(out, themeFailure{Kind: "tone", Role: a, Against: b,
					Message: fmt.Sprintf("%s and %s are the same colour; tones must differ.", themeLabelOf(labels, a), themeObjectOf(labels, b))})
				continue
			}
			if d := themeDeltaE(colours[a], colours[b]); d < themeMinToneDelta {
				out = append(out, themeFailure{Kind: "tone", Role: a, Against: b,
					Message: fmt.Sprintf("%s and %s are too alike: OKLab ΔE %s; needs %s.", themeLabelOf(labels, a), themeObjectOf(labels, b), themeFloorShow(d, themeMinToneDelta, 3), themeNum(themeMinToneDelta))})
			}
		}
	}

	max := 1.0
	if base == "dark" {
		max = themePreviewDarkMax
	}
	if math.IsNaN(preview) || preview <= 0 || preview > max {
		out = append(out, themeFailure{Kind: "preview", Role: "preview",
			Message: fmt.Sprintf("Live-preview intensity must be more than 0 and at most %s.", themeNum(max))})
	} else if base == "dark" {
		yo := themeLuminance(colours["bg-surface-overlay"])
		if y := themePreviewLuminance(preview); y > yo {
			out = append(out, themeFailure{Kind: "preview", Role: "preview", Against: "bg-surface-overlay",
				Message: fmt.Sprintf("Live-preview intensity %s lights the colour preview brighter than overlay (luminance %s against %s).", themeNum(preview), themeCeilShow(y, yo), themeNum(math.Floor(yo*1e4)/1e4))})
		}
	}
	return out
}

// themeTokens is what css/console-tokens.css defines.
type themeTokens struct {
	Dark, Light map[string]string  // every --b5-color-* role → its declared value
	Order       []string           // dark block order
	Roles       []string           // the themeable roles: opaque #rrggbb in both blocks
	Preview     map[string]float64 // --b5-preview-max-brightness in effect per base ("dark", "light")
}

var (
	themeTokensOnce sync.Once
	themeTokensVal  themeTokens
	themeTokensErr  error
	themeDeclRE     = regexp.MustCompile(`--b5-color-([a-z0-9-]+):\s*([^;]+);`)
	themeCommentRE  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	themeRuleRE     = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	themePreviewRE  = regexp.MustCompile(`--b5-preview-max-brightness:\s*([0-9.]+)\s*(;|$)`)
)

func themeBlock(css, selector string) (string, error) {
	i := strings.Index(css, selector+" {")
	if i < 0 {
		return "", fmt.Errorf("console-tokens.css has no %s block", selector)
	}
	j := strings.Index(css[i:], "}")
	if j < 0 {
		return "", fmt.Errorf("console-tokens.css: %s block is not closed", selector)
	}
	return css[i : i+j], nil
}

func parseThemeTokens(css string) (themeTokens, error) {
	css = themeCommentRE.ReplaceAllString(css, "")
	t := themeTokens{Dark: map[string]string{}, Light: map[string]string{}}
	dark, err := themeBlock(css, `:root, [data-theme="dark"]`)
	if err != nil {
		return t, err
	}
	light, err := themeBlock(css, `[data-theme="light"]`)
	if err != nil {
		return t, err
	}
	for _, m := range themeDeclRE.FindAllStringSubmatch(dark, -1) {
		t.Dark[m[1]] = strings.TrimSpace(m[2])
		t.Order = append(t.Order, m[1])
	}
	for _, m := range themeDeclRE.FindAllStringSubmatch(light, -1) {
		t.Light[m[1]] = strings.TrimSpace(m[2])
	}
	for _, r := range t.Order {
		if themeHexRE.MatchString(t.Dark[r]) && themeHexRE.MatchString(t.Light[r]) {
			t.Roles = append(t.Roles, r)
		}
	}
	// The preview intensity in effect per base, as the cascade gives it:
	// every rule for :root or that base's [data-theme] (all one
	// specificity), the last in file order winning. C9c: Daylight is 1.
	t.Preview = map[string]float64{}
	for _, base := range []string{"dark", "light"} {
		val := ""
		for _, rule := range themeRuleRE.FindAllStringSubmatch(css, -1) {
			applies := false
			for _, sel := range strings.Split(rule[1], ",") {
				sel = strings.TrimSpace(sel)
				applies = applies || sel == ":root" || sel == `[data-theme="`+base+`"]`
			}
			if m := themePreviewRE.FindStringSubmatch(rule[2]); applies && m != nil {
				val = m[1]
			}
		}
		if val == "" {
			return t, fmt.Errorf("console-tokens.css sets no --b5-preview-max-brightness for %s", base)
		}
		if t.Preview[base], err = strconv.ParseFloat(val, 64); err != nil {
			return t, err
		}
	}
	return t, nil
}

// loadThemeTokens parses the embedded token file once.
func loadThemeTokens() (themeTokens, error) {
	themeTokensOnce.Do(func() {
		b, err := embeddedUI.ReadFile("static/css/console-tokens.css")
		if err != nil {
			themeTokensErr = err
			return
		}
		themeTokensVal, themeTokensErr = parseThemeTokens(string(b))
	})
	return themeTokensVal, themeTokensErr
}

// themeLabels are the roles' names in the editor and in every message.
var themeLabels = map[string]string{
	"bg-canvas": "Canvas", "bg-surface": "Surface", "bg-surface-raised": "Raised surface", "bg-surface-overlay": "Overlay",
	"bg-inset": "Inset", "bg-hover": "Hover", "bg-selected": "Selected", "bg-pressed": "Pressed",
	"text-primary": "Primary text", "text-secondary": "Secondary text", "text-muted": "Muted text", "text-disabled": "Disabled text",
	"text-accent": "Accent text", "text-info": "Info text", "text-danger": "Danger text", "text-success": "OK text",
	"text-warning": "Warning text", "text-on-accent": "Thumb text", "text-on-danger": "Danger button text",
	"border-control": "Control border", "border-focus": "Focus ring", "border-subtle": "Faint separator",
	"border-default": "Separator", "border-strong": "Strong border",
	"ok-border": "OK outline", "ok-fill": "OK fill", "warn-border": "Warning outline", "warn-fill": "Warning fill",
	"danger-border": "Danger outline", "danger-fill": "Danger fill", "accent-border": "Accent outline", "accent-fill": "Accent fill",
	"info-border": "Info outline", "info-fill": "Info fill",
	"armed-border": "Armed outline", "armed-fill": "Armed fill", "armed-text": "Armed text",
	"disarm-slab-bg": "Disarm slab", "disarm-slab-text": "Disarm slab text",
	"arm-pill-border": "Arm button outline", "arm-pill-text": "Arm button text",
	"simulated-text": "Simulated text", "lease-lost-border": "Lease lost outline",
	"fixture-idle": "Fixture idle", "fixture-selected": "Fixture selected", "fixture-cell-selected": "Cell selected",
	"fixture-touched": "Fixture touched", "fixture-test": "Fixture under test", "fixture-highlight": "Fixture highlight",
	"fixture-ghost": "Ghost fixture", "fixture-unprofiled": "Unprofiled fixture", "fixture-offline": "Fixture not on wire",
	"value-set": "SET value", "value-default": "Default value", "value-test": "Test value", "value-mixed-band": "Mixed-value band",
	"grid-bg": "Grid", "grid-line": "Grid line", "grid-line-major": "Grid major line", "grid-layer-label": "Layer label",
	"track": "Fader track", "track-border": "Fader track border", "thumb": "Fader thumb", "thumb-border": "Fader thumb border",
	"xypad-bg": "XY pad", "xypad-crosshair": "XY pad crosshair",
}

// colours is a built-in theme's themeable roles ("dark" or "light").
func (t themeTokens) colours(base string) map[string]string {
	src := t.Dark
	if base == "light" {
		src = t.Light
	}
	out := make(map[string]string, len(t.Roles))
	for _, r := range t.Roles {
		out[r] = src[r]
	}
	return out
}

// CustomTheme is a saved custom colour theme: a base block of
// console-tokens.css plus a value for every themeable role.
type CustomTheme struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Base    string            `json:"base"` // "dark" | "light"
	Colors  map[string]string `json:"colors"`
	Preview float64           `json:"preview"` // --b5-preview-max-brightness
}

// themeMaxCustom bounds the stored list (a settings file, not a library).
const themeMaxCustom = 20

type themeRequest struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Base    string            `json:"base"`
	Colors  map[string]string `json:"colors"`
	Preview float64           `json:"preview"`
}

type themeListed struct {
	CustomTheme
	Failures []themeFailure `json:"failures"`
}

// handleGetThemes: GET /api/themes — the role list, the plain names, both
// built-in themes, and every custom theme re-validated now, so a theme
// stored before a contract change (or edited by hand) comes with its
// failures and no browser applies it.
func (s *Server) handleGetThemes(w http.ResponseWriter, r *http.Request) {
	tok, err := loadThemeTokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.settingsMu.Lock()
	stored := append([]CustomTheme(nil), s.settings.CustomThemes...)
	s.settingsMu.Unlock()
	list := make([]themeListed, 0, len(stored))
	for _, th := range stored {
		list = append(list, themeListed{th, validateTheme(th.Base, th.Colors, th.Preview, tok.Roles, themeLabels)})
	}
	labels := make(map[string]string, len(tok.Roles))
	for _, r := range tok.Roles {
		labels[r] = themeLabelOf(themeLabels, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":      tok.Roles,
		"labels":     labels,
		"bases":      map[string]map[string]string{"dark": tok.colours("dark"), "light": tok.colours("light")},
		"preview":    tok.Preview, // per base: dark 0.12, light 1 (C9c)
		"previewMax": map[string]float64{"dark": themePreviewDarkMax, "light": 1},
		"maxThemes":  themeMaxCustom,
		"themes":     list,
	})
}

// handlePostTheme: POST /api/themes — create (no id) or replace (id) one
// custom theme. The server validates every save with the contract: a
// browser's own check is a convenience, never trusted.
func (s *Server) handlePostTheme(w http.ResponseWriter, r *http.Request) {
	var req themeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tok, err := loadThemeTokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	colours := make(map[string]string, len(req.Colors))
	for k, v := range req.Colors {
		colours[k] = strings.ToLower(strings.TrimSpace(v))
	}
	name := strings.TrimSpace(req.Name)
	refuse := func(failures []themeFailure) {
		msg := "Theme needs changes: " + failures[0].Message
		if len(failures) > 1 {
			msg += fmt.Sprintf(" (and %d more)", len(failures)-1)
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": msg, "failures": failures})
	}
	if n := len([]rune(name)); n == 0 || n > 40 {
		refuse([]themeFailure{{Kind: "name", Message: "Name the theme in 1 to 40 characters."}})
		return
	}
	switch strings.ToLower(name) {
	case "dark", "daylight", "light":
		refuse([]themeFailure{{Kind: "name", Message: fmt.Sprintf("%q is a built-in theme; choose another name.", name)}})
		return
	}
	if failures := validateTheme(req.Base, colours, req.Preview, tok.Roles, themeLabels); len(failures) > 0 {
		refuse(failures)
		return
	}
	th := CustomTheme{ID: req.ID, Name: name, Base: req.Base, Colors: colours, Preview: req.Preview}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	next := make([]CustomTheme, 0, len(s.settings.CustomThemes)+1)
	found := false
	for _, x := range s.settings.CustomThemes {
		if x.ID == th.ID {
			found = true
			next = append(next, th)
			continue
		}
		if strings.EqualFold(x.Name, name) {
			refuse([]themeFailure{{Kind: "name", Message: fmt.Sprintf("A theme called %q already exists.", x.Name)}})
			return
		}
		next = append(next, x)
	}
	if th.ID != "" && !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("no custom theme %q (deleted in another browser?)", th.ID))
		return
	}
	if th.ID == "" {
		if len(next) >= themeMaxCustom {
			refuse([]themeFailure{{Kind: "name", Message: fmt.Sprintf("At most %d custom themes; delete one first.", themeMaxCustom)}})
			return
		}
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		th.ID = "t" + hex.EncodeToString(b[:])
		next = append(next, th)
	}
	updated := s.settings
	updated.CustomThemes = next
	// Persist before publishing, like POST /api/settings.
	if err := s.settingsStore.Save(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.settings = updated
	writeJSON(w, http.StatusOK, th)
}

// handleDeleteTheme: DELETE /api/themes/{id}. A browser that was using it
// goes back to Dark the next time it reads the list.
func (s *Server) handleDeleteTheme(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	next := make([]CustomTheme, 0, len(s.settings.CustomThemes))
	for _, x := range s.settings.CustomThemes {
		if x.ID != id {
			next = append(next, x)
		}
	}
	if len(next) == len(s.settings.CustomThemes) {
		writeError(w, http.StatusNotFound, fmt.Errorf("no custom theme %q", id))
		return
	}
	updated := s.settings
	updated.CustomThemes = next
	if err := s.settingsStore.Save(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.settings = updated
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
