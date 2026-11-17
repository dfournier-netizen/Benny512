package web

import "testing"

// TestL2ConsolePlotZoom (L2, owner 2026-11-17): continuous zoom with a
// slider and Ctrl+wheel, a lower floor with number-only tiles, Rotate 90°
// (display only, off while editing), and drag to pan without selecting.
// Literal ui.js and console.js in the real index.html.
func TestL2ConsolePlotZoom(t *testing.T) { runNodeTest(t, "console_l2_test.js") }
