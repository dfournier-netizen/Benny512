package web

import (
	"benny512/internal/library"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLibraryVerifyRequiresReviewedMode(t *testing.T) {
	h := newHarness(t)
	st := seedLibrary(t, h)
	r, _ := st.GetByKey(library.KeyFor("GLP", "JDC-1"))
	body := map[string]any{"key": r.Key, "mode": "Standard", "verified": true, "confirm": "VERIFY"}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/verify", body)
	if rr.Code != 400 {
		t.Fatal("missing reviewed mode accepted")
	}
	body["expected"] = r.Modes[0]
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/library/verify", body)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"verifiedHash":"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	r.Modes[0].Footprint++
	body["expected"] = r.Modes[0]
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/library/verify", body)
	if rr.Code != 400 {
		t.Fatal("stale mode accepted")
	}
}

func TestLibrarySourceDownloadAndCompactList(t *testing.T) {
	h := newHarness(t)
	r, _, err := h.srv.LibraryStore.UpsertChecked(library.Record{Model: "QA", SourceFiles: []library.SourceFile{{Name: "test.gdtf", Data: []byte("original GDTF")}}})
	if err != nil {
		t.Fatal(err)
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/library", nil)
	if strings.Contains(rr.Body.String(), `"data":`) {
		t.Fatal("list should omit base64 archive")
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/library/source?key="+url.QueryEscape(r.Key)+"&hash="+r.SourceFiles[0].SHA256, nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "original GDTF" || !strings.Contains(rr.Header().Get("Content-Disposition"), "test.gdtf") {
		t.Fatal(rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/library/export", nil)
	if !strings.Contains(rr.Body.String(), `"data":`) {
		t.Fatal("export lost original GDTF")
	}
}
