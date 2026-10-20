package web

import "testing"

// TestK3ConsoleWindows (K3, W1 component-specs §4 and §8, owner 2026-10-18/19):
// from 1024 px Controls (with Tools) is docked on the right and Selection
// order, Groups and Tests are hidden floating windows; the Windows menu,
// Dock/Float, collapse, close, keyboard move, drop-to-dock with Escape to
// cancel, the dock-edge separator (320 px minimum), per-browser memory,
// Reset, and every region back home below 1024 px. Literal ui.js,
// console-windows.js and console.js in the real index.html.
func TestK3ConsoleWindows(t *testing.T) { runNodeTest(t, "console_k3_test.js") }
