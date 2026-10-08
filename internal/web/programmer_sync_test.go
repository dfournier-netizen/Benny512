package web

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProgrammerLiveSyncAcrossBrowsers runs the literal api.js, ws.js and
// programmer.js as two browsers against a real HTTP + WebSocket server:
// browser B follows browser A's writes through the "programmer" broadcast
// alone, a write carrying a stale revision is refused, and a browser's own
// queued writes never refuse each other.
func TestProgrammerLiveSyncAcrossBrowsers(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newC4aRig(t)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "programmer_sync_test.js"), jsDir, ts.URL, r.ids["B1"]).CombinedOutput()
	if err != nil {
		t.Fatalf("programmer_sync_test.js: %v\n%s", err, out)
	}
	var res struct {
		ARevision  uint64   `json:"aRevision"`
		BRevision  uint64   `json:"bRevision"`
		BSelection []string `json:"bSelection"`
		BDimmer    uint32   `json:"bDimmer"`
		Stale      string   `json:"stale"`
		BChain     []uint64 `json:"bChain"`
		ADimmer    uint32   `json:"aDimmer"`
		BHighlight bool     `json:"bHighlight"`
		BGroup     string   `json:"bGroup"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.BRevision != res.ARevision || len(res.BSelection) != 1 || res.BSelection[0] != r.ids["B1"] || res.BDimmer != 65535 {
		t.Errorf("browser B did not follow browser A: %+v", res)
	}
	if !strings.Contains(res.Stale, "another browser") {
		t.Errorf("stale write: %q, want the 409 sentence", res.Stale)
	}
	if !res.BHighlight || res.BGroup != "From A" {
		t.Errorf("browser B did not follow A's Highlight and stored group: %+v", res)
	}
	if len(res.BChain) != 2 || res.BChain[1] != res.BChain[0]+1 || res.ADimmer != 2000 {
		t.Errorf("queued writes / A following B: %+v", res)
	}
}
