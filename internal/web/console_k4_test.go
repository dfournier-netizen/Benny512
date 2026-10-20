package web

import "testing"

// TestK4TestsTab (K4, W1 component-specs §9; owner 2026-10-18 "its own
// tab"; W1b: fixture controls only on the main Console): a wide-only Tests
// item after Console, the same workspace moved into the Tests screen with
// the Tests panel open and layout editing off, one shared selection with
// nothing written on a tab switch, the selection bar's Tests going there,
// and below 1024 px Tests back inside the Console. Literal app.js, ui.js,
// console-tests.js, console-windows.js and console.js in the real
// index.html.
func TestK4TestsTab(t *testing.T) { runNodeTest(t, "console_k4_test.js") }
