package web

import "testing"

// Chunk I2b (docs/plans/console-lite.md; docs/design/console-lite/
// component-specs.md §3 and §4): the Console selection grid, run on the
// literal ui.js and console.js in the real index.html.

// TestI2bSelectionGrid: fixture glyphs from capabilities with flag words,
// parent outline vs cell tab and their accessible names, the text order
// list, layer chips as toggles with Select all / Invert respecting them,
// ghosts as dashed words that are never buttons, group tile and chip
// counts, and the touch lasso toggle.
func TestI2bSelectionGrid(t *testing.T) { runNodeTest(t, "console_grid_test.js") }
