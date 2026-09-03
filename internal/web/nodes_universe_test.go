package web

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestNodesUniverseNotation runs nodes.js and ui.js exactly as the browser
// loads them and pins the Nodes screen's universe handling, both halves.
//
// The owner reported from a real bench, with an Elation EN4 he had confirmed
// was in 1-based notation starting at universe 1: "the universe values in
// Benny512 are displaying as 1 less than what the faceplate says." Two
// defects sat behind that, and only the first was visible to him:
//
//   - nodes.js had 25 universe references and ZERO UI.formatUniverse calls,
//     the only screen in the app that never converted, so it printed the
//     canonical 0-based Port-Address raw;
//   - and what it printed was not the Port-Address but `outputAddress & 0x0F`,
//     the Art-Net Universe nibble alone. That equals the Port-Address only
//     while Net and Sub-Net are both zero — true of his rig and of the demo.
//     One port on Sub-Net 1 and Nodes reads "0" where Patch reads "17".
//
// The write path is the dangerous half and is what this test mostly covers.
// ArtAddress carries ONE NetSwitch and ONE SubSwitch for the whole packet and
// only a 4-bit Universe nibble per port, so a node's ports physically cannot
// span more than one 16-universe block. The owner's chosen UI is a single
// universe box per port in his own display base with Net/Sub-Net derived on
// save, which means the impossible case must be REFUSED — never clamped,
// truncated, or partially written, because this writes to a gateway on a show
// and a wrong universe sends output to the wrong fixtures. Mutating the
// refusal into a clamp makes this test fail with swOut=[0,1,null,null]: port 1
// silently programmed to universe 1 when the user asked for universe 17.
//
// Same Node prerequisite and same skip-not-fail policy as
// TestGdtfParseFootprintGroundTruth in gdtfparse_footprint_test.go.
func TestNodesUniverseNotation(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping nodes.js universe notation test")
	}

	cmd := exec.Command(nodePath, "static/js/testdata/nodes_universe_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("nodes_universe_test.js failed: %v\n--- stdout ---\n%s\n--- stderr ---\n%s",
			err, stdout.String(), stderr.String())
	}
	t.Log(stdout.String())
}
