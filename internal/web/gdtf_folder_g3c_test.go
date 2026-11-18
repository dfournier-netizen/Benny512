package web

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// TestG3cStatusNamesTheGDTFFolder (owner round 6 #2): GET /api/gdtf/status
// gives the GDTF library folder for Settings › About, "" when the library is
// off (demo, rehearsal, tests). It is never written into a show file, an
// export or diagnostics: each of those outputs is grepped for the path.
func TestG3cStatusNamesTheGDTFFolder(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	handler := h.srv.Handler()
	var st struct {
		LibraryFolder *string `json:"libraryFolder"`
	}
	rr := doJSON(t, handler, "GET", "/api/gdtf/status", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil || st.LibraryFolder == nil || *st.LibraryFolder != "" {
		t.Fatalf("disabled library: libraryFolder = %v (%s), want \"\"", st.LibraryFolder, rr.Body.String())
	}

	root := filepath.Join(t.TempDir(), "Unique-GDTF-Folder-4471", "GDTF")
	h.srv.SetGDTFLibraryPath(root)
	rr = doJSON(t, handler, "GET", "/api/gdtf/status", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil || st.LibraryFolder == nil || *st.LibraryFolder != root {
		t.Fatalf("libraryFolder = %v, want %q", st.LibraryFolder, root)
	}
	h.srv.PatchStore.Replace(patch.Patch{Name: "Folder check"})
	for _, p := range []string{"/api/patch/export", "/api/patch/export?format=txt", "/api/library/export", "/api/diagnostics/rdm", "/api/capture/export", "/api/context", "/api/settings", "/api/patch"} {
		out := doJSON(t, handler, "GET", p, nil).Body.String()
		if strings.Contains(out, "Unique-GDTF-Folder-4471") || strings.Contains(out, strings.ReplaceAll(root, `\`, `\\`)) {
			t.Fatalf("%s carries the GDTF folder: %.300s", p, out)
		}
	}
}
