package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Show operations and test configuration share one lock. Stop deliberately
// bypasses it, so a slow RDM read cannot delay a user's stop request.
func (s *Server) showGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		bound := strings.HasPrefix(path, "/api/patch") || path == "/api/reset" || path == "/api/library/reprofile" || path == "/api/library/from-patch" || path == "/api/workspace" || strings.HasPrefix(path, "/api/programmer") || strings.HasPrefix(path, "/api/tests")
		stop := strings.HasSuffix(path, "/stop") || strings.HasSuffix(path, "/blackout")
		// The old Rig Check screen taking the tests layer back ends the
		// Tests API's hold on it (C5, tests.go).
		legacy := isLegacyRigCheckWrite(r)
		if legacy {
			s.testsLegacyTakeover()
		}
		if !bound || stop {
			next.ServeHTTP(w, r)
			return
		}
		s.showMu.Lock()
		defer s.showMu.Unlock()
		boundary := false
		if r.Method != "GET" {
			if token := r.Header.Get("X-Benny-Show"); token != "" && token != strconv.FormatUint(s.showRevision, 10) {
				writeError(w, http.StatusConflict, fmt.Errorf("the active show changed; refresh before applying changes"))
				return
			}
			boundary = path == "/api/patches" || strings.HasSuffix(path, "/load") || path == "/api/patch/new" || path == "/api/patch/reset-active" || path == "/api/patch/recover" || path == "/api/reset"
			structure := strings.HasPrefix(path, "/api/patch/entries") || strings.HasPrefix(path, "/api/patch/reconcile/") || path == "/api/patch/reorder" || path == "/api/patch/import" || path == "/api/patch/adopt" || path == "/api/library/reprofile"
			// A structural edit while the Tests API drives the layer keeps its
			// tests: they are re-resolved on the edited show after the
			// handler (refreshTests) and swapped in atomically.
			if boundary || (structure && !s.testsOwnLayer()) {
				s.RigCheck.ResetSelection()
				s.patternScopeMu.Lock()
				s.patternScope = patternScopeStatus{}
				s.patternScopeMu.Unlock()
			}
			if boundary {
				s.testsShowBoundary()
				s.stopUniverseIdentify()
				s.showRevision++
			}
		}
		w.Header().Set("X-Benny-Show", strconv.FormatUint(s.showRevision, 10))
		next.ServeHTTP(w, r)
		// The programmer follows the show (C4a): cleared on a boundary,
		// pruned after any other change. A no-op when the show did not
		// change (programmer.go, syncProgrammer).
		if r.Method != "GET" {
			s.syncProgrammer(boundary)
			s.refreshTests()
		}
	})
}

func (s *Server) handleResetActiveShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.Confirm != "RESET SHOW" {
		writeError(w, 400, fmt.Errorf("confirm RESET SHOW to empty only the active show"))
		return
	}
	p, err := s.PatchStore.ResetActive()
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, 200, toPatchResponse(p))
}

func (s *Server) handleRecoverShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.Confirm != "RECOVER" {
		writeError(w, 400, fmt.Errorf("confirm RECOVER to restore the preceding save"))
		return
	}
	p, err := s.PatchStore.RecoverActive()
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, 200, toPatchResponse(p))
}
