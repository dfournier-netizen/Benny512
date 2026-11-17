package web

import "testing"

// TestM2ConsoleGridSnap (M2, decision 0004): the View menu's Snap and Grid
// switches. Snap off draws stored positions exactly and moves freely (drag,
// 0.1-square arrows, Shift = 1 square); Grid off hides the square grid; both
// are remembered per browser. Literal ui.js and console.js in the real
// index.html.
func TestM2ConsoleGridSnap(t *testing.T) { runNodeTest(t, "console_m2_test.js") }
