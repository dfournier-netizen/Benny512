package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"benny512/internal/patch"
)

// seedBMFLShow patches n BMFL Spot entries (Mode 1, the literal browser
// parser's output for the real Robe extract) through the real handler into
// a persisted show at dir/show.json.
func seedBMFLShow(t *testing.T, n int) (*testHarness, string) {
	t.Helper()
	raw := parseVendorEntryBody(t, "robe_bmfl_spot_real_extract.xml")
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "show.json")
	h.srv.SetPatchStorePath(path)
	entries := make([]any, 0, n)
	for i := 0; i < n; i++ {
		e := map[string]any{}
		for k, v := range body {
			e[k] = v
		}
		e["name"] = fmt.Sprintf("BMFL %d", i+1)
		e["universe"] = i / 12
		e["startAddress"] = 1 + (i%12)*41
		entries = append(entries, e)
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries})
	if rr.Code != http.StatusOK {
		t.Fatalf("import %d BMFL entries: %d %s", n, rr.Code, rr.Body.String())
	}
	return h, path
}

// TestShowFileSize_100BMFL measures what the owner asked about: the show
// file and GET /api/patch for 100 BMFL Spot entries. The file must hold the
// profile once; the API payload is deliberately unchanged this chunk (the
// owner kept every response shape), so it is reported, not shrunk.
func TestShowFileSize_100BMFL(t *testing.T) {
	h, path := seedBMFLShow(t, 100)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	t.Logf("100 × BMFL Spot Mode 1: show file %d bytes; GET /api/patch %d bytes", st.Size(), rr.Body.Len())
	// One profile (~160 KB indented) plus 100 small entries (~2.5 KB each:
	// identity, intended/as-found, location) — well under 1 MB. Inline
	// storage is ~100 × 160 KB.
	if st.Size() > 1<<20 {
		t.Errorf("show file for 100 identical BMFL entries is %d bytes; the profile is not being stored once", st.Size())
	}
}

// TestProfileCache_ReloadedShowIsIdenticalForRigCheckAndBaselines: what a
// reloaded (hydrated) show hands to Rig Check and to Rig Baselines is
// exactly what was saved — same entries, same available tests, same
// baseline profile digests, so a baseline taken before the save reports no
// change after it.
func TestProfileCache_ReloadedShowIsIdenticalForRigCheckAndBaselines(t *testing.T) {
	h, path := seedBMFLShow(t, 3)
	before, _ := h.srv.PatchStore.Get()
	after, ok := patch.NewStore(path).Get()
	if !ok {
		t.Fatal("saved show did not reload")
	}
	if !reflect.DeepEqual(before.Entries, after.Entries) {
		t.Fatal("reloaded entries differ from the saved ones")
	}
	if a, b := patch.AvailableTests(before.Entries), patch.AvailableTests(after.Entries); len(a) == 0 || !reflect.DeepEqual(a, b) {
		t.Errorf("Rig Check availability changed across save/reload (%d vs %d tests)", len(a), len(b))
	}
	base := rigBaseline{Entries: before.Entries, ProfileDigests: map[string]string{}}
	for _, e := range before.Entries {
		base.ProfileDigests[e.ID] = profileDigest(e)
	}
	if changes := baselineChanges(base, after, nil); len(changes) != 0 {
		t.Errorf("a baseline taken before saving reports changes after reload: %v", changes)
	}
}
