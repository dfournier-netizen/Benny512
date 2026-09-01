package web

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestGdtfParseFootprintGroundTruth runs gdtfparse.js's footprint/
// channelFunctions resolution against synthetic fixtures built from the
// real GDTF geometry shapes of this project's 5-fixture sample show,
// asserting the ground-truth footprints independently derived from that
// show's DMX address spacing (see
// internal/web/static/js/testdata/gdtf_footprint_test.js's doc comment for
// the fixtures and internal/web/static/js/gdtfparse.js's doc comment for
// the resolution algorithm these lock in).
//
// gdtfparse.js is browser JS with no Node build step in this project
// (zero external dependencies, no package.json) — this test shells out to
// plain `node` (no packages: internal/web/static/js/testdata/tinydom.js is
// a small hand-written DOM shim checked into the repo, not an npm
// dependency) rather than reimplementing the resolution in Go, so the code
// under test is the literal file the browser loads, not a Go port of it.
// Skipped, not failed, when node isn't on PATH — this is the one Go test
// in the suite with an environment prerequisite beyond the Go toolchain,
// and CI/dev environments without Node shouldn't lose the rest of `go test
// ./...` over it.
func TestGdtfParseFootprintGroundTruth(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping gdtfparse.js ground-truth test")
	}

	cmd := exec.Command(nodePath, "static/js/testdata/gdtf_footprint_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("gdtf_footprint_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}
