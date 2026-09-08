package web

import (
	"os/exec"
	"testing"
)

func TestAutomaticModeNames(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	if out, err := exec.Command(node, "static/js/testdata/mode_names_test.js").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
