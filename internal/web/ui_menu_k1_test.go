package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestK1SharedMenu (K1, W1 component-specs §2): the shared menu that holds
// Show tools / Fixture library (and, from K2, the Console's Select, View,
// Windows and More menus) is a real button + role=menu with keyboard
// movement, Escape returning focus, a visible check on checked items, and
// item hooks kept. Runs the literal ui.js in minidom.
func TestK1SharedMenu(t *testing.T) { runNodeTest(t, "ui_menu_test.js") }

// TestK1NavGroups (W1b owner answer A, 2026-10-19): the desktop nav in the
// literal index.html is grouped Show (Patch, Console, Rig Walk) then Network
// (Nodes, Devices, Analyzer), with Settings last, each group named by a word
// (role=group labelled by its visible label). The phone bar is unchanged.
func TestK1NavGroups(t *testing.T) {
	b, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, `<nav class="b5-nav" id="tabs"`)
	end := strings.Index(src, `<nav class="b5-nav--mobile"`)
	if start < 0 || end < start {
		t.Fatal("desktop nav not found")
	}
	nav := src[start:end]
	var order []string
	for _, m := range regexp.MustCompile(`data-tab="([a-z]+)"|aria-labelledby="(navGroup[A-Za-z]+)"`).FindAllStringSubmatch(nav, -1) {
		if m[1] != "" {
			order = append(order, m[1])
		} else {
			order = append(order, m[2])
		}
	}
	want := "navGroupShow patch console walk navGroupNetwork nodes devices analyzer settings"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("desktop nav order\n got %s\nwant %s", got, want)
	}
	for _, id := range []string{"navGroupShow\">Show<", "navGroupNetwork\">Network<"} {
		if !strings.Contains(nav, id) {
			t.Errorf("group label %q missing: groups must be named by a visible word", id)
		}
	}
	mob := src[end:]
	mob = mob[:strings.Index(mob, "</nav>")]
	if n := strings.Count(mob, "data-tab="); n != 7 || strings.Contains(mob, "b5-nav__group") {
		t.Errorf("the phone bar must stay ungrouped with its 7 tabs (got %d)", n)
	}
}
