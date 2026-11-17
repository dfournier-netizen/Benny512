package web

import "testing"

// TestM3ConsolePlanMenu (M3, decision 0004): the toolbar's Plan button and
// its panel. Scale posts once on release, Enter or blur (never while
// dragging) and refuses a bad number; Arrange selection asks only when a
// hand-placed fixture is chosen, Arrange all… always asks. Literal ui.js and
// console.js in the real index.html.
func TestM3ConsolePlanMenu(t *testing.T) { runNodeTest(t, "console_m3_test.js") }
