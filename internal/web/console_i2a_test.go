package web

import (
	"os"
	"strings"
	"testing"
)

// Chunk I2a (docs/plans/console-lite.md; docs/design/console-lite/
// component-specs.md §1, §19 and "Responsive composition"), against the
// literal files the browser loads.

// TestI2aFaderBarStartsCollapsed: with no remembered choice the group fader
// bar starts collapsed except on a tall desktop; a remembered choice wins;
// blocked storage falls back to the default (faders.js).
func TestI2aFaderBarStartsCollapsed(t *testing.T) { runNodeTest(t, "faders_default_test.js") }

// TestI2aConsoleNavIcon: the Console tab uses the design's nav-console
// symbol (it borrowed the filter icon), in both the desktop and phone navs.
func TestI2aConsoleNavIcon(t *testing.T) {
	b, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if n := strings.Count(html, `data-tab="console"><svg><use href="/icons/benny512-icons.svg#b5-icon-nav-console"/>`); n != 2 {
		t.Errorf("Console tab uses b5-icon-nav-console in %d of 2 navs", n)
	}
}

// TestI2aSelectionSummaryAboveGrid: the selection summary was a sticky
// bottom bar after the panes, so at rest it covered the top of the
// selection grid (1440x900 with the fader bar open). It now sits in flow
// before the grid and is never sticky.
func TestI2aSelectionSummaryAboveGrid(t *testing.T) {
	js := readJS(t, "console.js")
	// I2d: the action bar's panel (Clear scopes / Fan / Lowlight level)
	// sits in flow between the bar and the panes.
	sum := strings.Index(js, "      els.summary,\n      els.actPanel,\n      h('div', { class: 'b5-con-main' },")
	if sum < 0 {
		t.Errorf("console.js does not place els.summary (then its action panel) directly before the b5-con-main panes")
	}
	css, err := os.ReadFile("static/css/screens-console.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".b5-con-summary { position: static;") {
		t.Errorf("screens-console.css does not make the selection summary position: static")
	}
}
