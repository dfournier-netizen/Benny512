package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// I1b — every colour in the served CSS resolves through a semantic role.
// Palette ramps and colour literals may appear only in the two token layers
// (theme-contract.md: "Component CSS uses roles; palette ramps remain
// private to the base token file"), so a C9 theme that sets the roles
// re-colours everything and nothing keeps a dark-only value behind its back.

// i1bTokenFiles are where ramps and literal colour values legitimately live.
var i1bTokenFiles = map[string]bool{
	"static/css/benny512-tokens.css": true,
	"static/css/console-tokens.css":  true,
}

var (
	i1bRuleRE    = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	i1bCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	// Values only (selectors such as #rcLevel are ids, not colours).
	i1bLiteralRE = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(|\b(white|black)\b`)
)

func TestI1bNoColourOutsideTokenFiles(t *testing.T) {
	files, err := fs.Glob(embeddedUI, "static/css/*.css")
	if err != nil || len(files) < 10 {
		t.Fatalf("listing embedded css: %d files, %v", len(files), err)
	}
	bad := 0
	for _, f := range files {
		if i1bTokenFiles[f] {
			continue
		}
		css := i1bCommentRE.ReplaceAllString(i1Embedded(t, f), "")
		for _, m := range i1bRuleRE.FindAllStringSubmatch(css, -1) {
			sel := strings.Join(strings.Fields(m[1]), " ")
			for _, decl := range strings.Split(m[2], ";") {
				name, value, ok := strings.Cut(strings.TrimSpace(decl), ":")
				if !ok {
					continue
				}
				value = strings.TrimSpace(value)
				if i1RampRE.MatchString(value) || i1bLiteralRE.MatchString(value) {
					bad++
					t.Errorf("%s: `%s { %s: %s }` — use a semantic role from console-tokens.css",
						strings.TrimPrefix(f, "static/"), sel, strings.TrimSpace(name), value)
				}
			}
		}
	}
	if bad > 0 {
		t.Logf("%d raw colour declarations outside the token files", bad)
	}
}

// i1bJSAllowed are the only colour literals the browser scripts may carry.
// They are fixture DATA, not interface colour: the colour picker's preset
// swatches are the RGB values written to the programmer for the rig, so a
// theme must never change them.
var i1bJSAllowed = map[string]map[string]bool{
	"static/js/console-controls.js": {
		"'#ffffff'": true, "'#ff0000'": true, "'#ff8000'": true, "'#ffff00'": true,
		"'#00ff00'": true, "'#00ffff'": true, "'#0000ff'": true, "'#ff00ff'": true,
	},
}

// i1bJSColourRE finds colour a script could inject as an inline style: a
// ramp variable, a quoted hex literal, or a CSS colour function. (\b keeps
// identifiers such as hsv2rgb( out.)
var i1bJSColourRE = regexp.MustCompile(`--b5-color-(neutral|accent|info|danger|success|warning)-[0-9]{3}[\w-]*|['"` + "`" + `]#[0-9a-fA-F]{3,8}['"` + "`" + `]|\b(rgba?|hsla?)\(`)

func i1bJSFindings(name, src string) []string {
	var out []string
	for _, m := range i1bJSColourRE.FindAllString(src, -1) {
		if !i1bJSAllowed[name][m] {
			out = append(out, m)
		}
	}
	return out
}

func TestI1bNoColourLiteralsInScripts(t *testing.T) {
	// The scanner itself must catch each form, or a clean pass means nothing.
	for _, bad := range []string{
		`el.style.background = 'var(--b5-color-accent-500)';`,
		`el.style.color = "#c80100";`,
		"h('span', { style: `border-color: rgba(0, 0, 0, .5)` })",
	} {
		if len(i1bJSFindings("static/js/x.js", bad)) == 0 {
			t.Fatalf("scanner misses %q", bad)
		}
	}
	if got := i1bJSFindings("static/js/x.js", "const rgb = hsv2rgb(h, s);"); len(got) != 0 {
		t.Fatalf("scanner flags an identifier: %v", got)
	}

	files, err := fs.Glob(embeddedUI, "static/js/*.js")
	if err != nil || len(files) < 10 {
		t.Fatalf("listing embedded js: %d files, %v", len(files), err)
	}
	for _, f := range files {
		for _, m := range i1bJSFindings(f, i1Embedded(t, f)) {
			t.Errorf("%s: colour literal %s in a browser script; use a role class or var(--b5-color-<role>)", strings.TrimPrefix(f, "static/"), m)
		}
	}
}
