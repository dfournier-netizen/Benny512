package web

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestReconcileViewOrdering runs reconcile.js exactly as the browser loads
// it, over a DOM stub and a recording Api, and asserts the things that live
// only in the view layer and that no Go test can see:
//
//  1. every DiffState the SERVER can send has a human phrase on the SCREEN.
//     This is the guard for a defect that was about to ship: the Go side
//     added a third as-found state, "not_fitted" ("the fixture's own
//     SUPPORTED_PARAMETERS says it hasn't got this" — a GLP JDC-1 tilts but
//     does not pan), and reconcile.js's STATE_WORD map had no entry for it,
//     so `STATE_WORD[state] || state` would have printed the raw wire enum
//     `not_fitted` to a lighting technician. That is the eighth instance on
//     this project of one value spelled two ways either side of the Go/JS
//     boundary, and the only kind of test that catches it is one that runs
//     the real JS against the real vocabulary.
//
//  2. the pane ordering the owner asked for: each pane sorts on its own key,
//     independently of the other; sorts run on CANONICAL values, so universes
//     order 1, 2, 3, 11, 21 and not the lexicographic 1, 11, 2, 21, 3 that
//     produced this project's earlier universe sort bug; committed rows sink
//     into a named group at the bottom while still obeying the chosen sort;
//     and the sort survives the board refetch that every commit performs,
//     because a pane that reshuffles itself after each commit is unusable for
//     the exact job it exists to do.
//
//  3. sorting is VIEW state — picking a sort issues no HTTP request and
//     triggers no board refetch. reconcile.js's own rule is that the server
//     board is its only state; this pins the boundary between that and the
//     client-side view state the sort controls legitimately own.
//
// Same Node prerequisite and same skip-not-fail policy as
// TestGdtfParseFootprintGroundTruth in gdtfparse_footprint_test.go: the code
// under test is the literal JS the browser loads, not a Go port of it, and a
// dev/CI box without Node shouldn't lose the rest of `go test ./...` over it.
func TestReconcileViewOrdering(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping reconcile.js view test")
	}

	cmd := exec.Command(nodePath, "static/js/testdata/reconcile_view_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile_view_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}
