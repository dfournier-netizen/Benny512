package web

import "testing"

// TestK2ConsoleWorkspace (K2, W1 component-specs §2–§7, owner 2026-10-19):
// one toolbar row with Select / View menus, Plan | Compact (empty rows and
// columns removed, display only, Plan while editing), Fit and zoom with
// per-browser memory, tiles led by fixture number and short type, HL tag,
// and the selection bar's grouped summary and More menu. Literal ui.js and
// console.js in the real index.html.
func TestK2ConsoleWorkspace(t *testing.T) { runNodeTest(t, "console_k2_test.js") }
