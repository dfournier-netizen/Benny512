package web

import (
	"os/exec"
	"testing"
)

// TestNodesFaultPill runs the literal nodes.js/ui.js under node and pins the
// node-fault flag: a worded pill on the node card and the server's sentence
// in the editor. See static/js/testdata/nodes_fault_test.js.
func TestNodesFaultPill(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	if out, err := exec.Command(node, "static/js/testdata/nodes_fault_test.js").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
