package web

import (
	"benny512/internal/patch"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowSwitchInvalidatesPreviousRigCheckScope(t *testing.T) {
	h := newHarness(t)
	h.srv.PatchStore.Replace(patch.Patch{Name: "A", Entries: []patch.Entry{{ID: "a", Footprint: 1, StartAddress: 1}}})
	_, err := h.srv.RigCheck.SetPatternTests([]patch.Entry{{ID: "a", Universe: 42, StartAddress: 1, Footprint: 1}}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", map[string]string{"name": "B"})
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if _, err = h.srv.RigCheck.StartPatternOutput(); err == nil {
		t.Fatal("old show targets could restart")
	}
	if len(h.srv.DMX.Universes()) != 0 {
		t.Fatal("old universes still active")
	}
}

func TestStaleBrowserCannotEditDifferentShow(t *testing.T) {
	h := newHarness(t)
	first := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil).Header().Get("X-Benny-Show")
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", map[string]string{"name": "B"})
	req := httptest.NewRequest("POST", "/api/patch/entries", strings.NewReader(`{"name":"old edit"}`))
	req.Header.Set("X-Benny-Show", first)
	rr := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 409 {
		t.Fatalf("stale edit: %d %s", rr.Code, rr.Body.String())
	}
}
