package web

import (
	"os/exec"
	"testing"
)

func TestRigCheckFixtureTypeScopeUI(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping rigcheck.js scope test")
	}
	output, err := exec.Command(nodePath, "static/js/testdata/rigcheck_scope_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("rigcheck_scope_test.js failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
