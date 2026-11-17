package web

import "testing"

// TestM1ConsoleSnap (M1, decision 0004): exact stored positions are drawn
// on the nearest half square, overlapping tiles move to the nearest free
// spot, objects are drawn as stored, and arrow keys move from the drawn spot
// in half squares. Literal ui.js and console.js in the real index.html.
func TestM1ConsoleSnap(t *testing.T) { runNodeTest(t, "console_m1_test.js") }
