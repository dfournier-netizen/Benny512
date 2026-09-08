package web

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestMVRPositionUUIDLabels(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH")
	}
	cmd := exec.Command(node, "static/js/testdata/mvr_position_test.js")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mvr position test: %v\n%s\n%s", err, out.String(), stderr.String())
	}
}
