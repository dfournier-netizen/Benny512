package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/rdm"
)

// TestConsoleControls_I2c (Console-lite I2c, attribute controls restyle)
// runs static/js/testdata/console_i2c_test.js: the real index.html in a
// small DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js and console.js against this real server, with two
// real Robe BMFL Spot profiles (16-bit Pan/Tilt) and one RDM-inferred spot
// whose channels are built by BuildRDMInferredChannelFunctions from the
// E1.20 SLOT_INFO worked example (the same path the --demo "Spot 1" and a
// live SLOT_INFO read take): Pan/Tilt known only as attributes, no GDTF
// functions or ranges.
func TestConsoleControls_I2c(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	slots := []rdm.SlotInfoEntry{
		{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
		{Offset: 1, Type: rdm.SlotTypeSecondaryFine, Value: 0},
		{Offset: 2, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDTilt)},
		{Offset: 3, Type: rdm.SlotTypeSecondaryFine, Value: 2},
		{Offset: 4, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDRotoGoboWheel)},
	}
	spot := BuildRDMInferredChannelFunctions(slots, map[uint16]string{4: "Gobo Wheel 1"})
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42),
		map[string]any{"name": "R1", "fixtureType": "RDM Spot", "footprint": 5, "universe": 1, "startAddress": 301, "channelFunctions": spot},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp struct {
		Patch struct {
			Entries []struct{ ID, Name string }
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range resp.Patch.Entries {
		ids[e.Name] = e.ID
	}
	if len(ids) != 3 {
		t.Fatalf("patched %d entries, want 3", len(ids))
	}
	idsJSON, _ := json.Marshal(ids)
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_i2c_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_i2c_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_i2c_test.js failed: %v", err)
	}
}
