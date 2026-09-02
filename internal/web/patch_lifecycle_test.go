package web

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestPatchScreenLifecycleSeams runs the Patch screen's three sub-tabs —
// Entries (patch.js), Reconcile (reconcile.js) and Rig Check (rigcheck.js) —
// as the browser loads them, over a DOM stub and a recording Api, and
// asserts the two cross-file lifecycle behaviours that nothing else in this
// suite can see:
//
//  1. leaving the Rig Check view, or its Function sub-view, while pattern
//     output is flowing stops that output there and then, rather than
//     stopping only the client-liveness heartbeat and leaving the rig lit
//     until the server's watchdog catches it 5s later;
//  2. a Reconcile commit reaches patch.js's own copy of the patch, so the
//     Entries table shows the entry as confirmed without the whole Patch tab
//     having to be left and re-entered.
//
// Both defects were invisible to every existing test: the Go handlers were
// correct (patch_test.go / reconcilecommit_test.go prove the server does the
// right thing on each request), and each JS module was correct about its own
// state. The bug was in which requests the screens make, and when — see
// internal/web/static/js/testdata/patch_lifecycle_test.js's doc comment.
//
// Same Node prerequisite and same skip-not-fail policy as
// TestGdtfParseFootprintGroundTruth in gdtfparse_footprint_test.go: the code
// under test is the literal JS the browser loads, not a Go port of it, and a
// dev/CI box without Node shouldn't lose the rest of `go test ./...` over it.
func TestPatchScreenLifecycleSeams(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping patch.js lifecycle seam test")
	}

	cmd := exec.Command(nodePath, "static/js/testdata/patch_lifecycle_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("patch_lifecycle_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}
