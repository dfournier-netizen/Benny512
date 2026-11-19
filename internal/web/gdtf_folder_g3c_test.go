package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	rr = statusFrom(t, handler, "127.0.0.1:50123")
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

func statusFrom(t *testing.T, h http.Handler, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/gdtf/status", nil)
	req.RemoteAddr = remote
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestG3cDataFolderOnlyOnThisComputer (owner, 2026-11-18 11:40): the path
// goes only to a browser on the computer running Benny512 — a request from
// a loopback address (127.0.0.0/8 or ::1), by the connection's RemoteAddr.
// There is no reverse proxy, so X-Forwarded-For is never trusted. Any other
// browser gets no path and libraryFolderWithheld true, so the page can say
// why.
func TestG3cDataFolderOnlyOnThisComputer(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	root := filepath.Join(t.TempDir(), "GDTF")
	h.srv.SetGDTFLibraryPath(root)
	handler := h.srv.Handler()
	type ans struct {
		LibraryFolder         *string `json:"libraryFolder"`
		LibraryFolderWithheld *bool   `json:"libraryFolderWithheld"`
	}
	get := func(remote, xff string) ans {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/gdtf/status", nil)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
			req.Header.Set("X-Real-IP", xff)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var a ans
		if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil || a.LibraryFolder == nil || a.LibraryFolderWithheld == nil {
			t.Fatalf("%s: %v %s", remote, err, rr.Body.String())
		}
		return a
	}
	for _, remote := range []string{"127.0.0.1:50123", "127.4.5.6:1", "[::1]:50123", "[::ffff:127.0.0.1]:9"} {
		if a := get(remote, ""); *a.LibraryFolder != root || *a.LibraryFolderWithheld {
			t.Errorf("from %s: folder %q withheld %v, want the path", remote, *a.LibraryFolder, *a.LibraryFolderWithheld)
		}
	}
	for _, c := range []struct{ remote, xff string }{
		{"192.168.1.50:50123", ""},
		{"[fe80::1c2d:3e4f:5a6b:7c8d%eth0]:50123", ""},
		{"[2001:db8::50]:50123", ""},
		{"192.168.1.50:50123", "127.0.0.1"}, // a forged header changes nothing
		{"not-an-address", ""},
	} {
		if a := get(c.remote, c.xff); *a.LibraryFolder != "" || !*a.LibraryFolderWithheld {
			t.Errorf("from %s (X-Forwarded-For %q): folder %q withheld %v, want no path", c.remote, c.xff, *a.LibraryFolder, *a.LibraryFolderWithheld)
		}
	}
	// Library off: nothing to withhold, and still no path.
	h2 := newHarness(t)
	t.Cleanup(h2.srv.Close)
	req := httptest.NewRequest("GET", "/api/gdtf/status", nil)
	req.RemoteAddr = "192.168.1.50:1"
	rr := httptest.NewRecorder()
	h2.srv.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"libraryFolder":"","libraryFolderWithheld":false`) {
		t.Fatalf("library off: %s", rr.Body.String())
	}
}
