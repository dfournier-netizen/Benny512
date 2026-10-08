package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"benny512/internal/patch"
)

// This file is the HTTP surface of the group faders (Console-lite G2,
// internal/patch/faders.go). Like the programmer, one set of faders per
// station, shared by every browser.
//
//	GET  /api/faders               -> patch.FadersView {revision, output, faders}
//	POST /api/faders/set           {id, level (0..1)}
//	POST /api/faders/release       {id}
//	POST /api/faders/release-all   {}
//
// Every route goes through the show guard (X-Benny-Show). Every response
// carries the fader revision in X-Benny-Faders; a POST that sends
// X-Benny-Faders is refused with 409 when it is not the current revision.
// Every change broadcasts {"type":"faders","revision":N} on the WebSocket.

const fadersRevisionHeader = "X-Benny-Faders"

func (s *Server) broadcastFaders() {
	rev := s.Programmer.FadersRevision()
	s.hub.broadcast(wsMessage{Type: "faders", At: time.Now(), Revision: &rev})
}

func (s *Server) writeFadersJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(fadersRevisionHeader, strconv.FormatUint(s.Programmer.FadersRevision(), 10))
	writeJSON(w, status, v)
}

func (s *Server) handleGetFaders(w http.ResponseWriter, r *http.Request) {
	s.syncProgrammer(false)
	s.writeFadersJSON(w, http.StatusOK, s.Programmer.Faders())
}

type fadersRequest struct {
	ID    string   `json:"id"`
	Level *float64 `json:"level"`
}

func (s *Server) handleFadersAction(w http.ResponseWriter, r *http.Request) {
	var req fadersRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var expected *uint64
	if h := r.Header.Get(fadersRevisionHeader); h != "" {
		v, err := strconv.ParseUint(h, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s must be the fader revision number this browser last saw.", fadersRevisionHeader))
			return
		}
		expected = &v
	}
	s.syncProgrammer(false)
	var (
		view    patch.FadersView
		changed bool
		err     error
	)
	switch r.PathValue("action") {
	case "set":
		if req.ID == "" || req.Level == nil {
			err = patch.ProgrammerRequestError{Msg: "Name the fader (id) and its level (0 to 1)."}
			break
		}
		view, err = s.Programmer.SetFader(req.ID, *req.Level, expected)
		changed = err == nil
	case "release":
		if req.ID == "" {
			err = patch.ProgrammerRequestError{Msg: "Name the fader to release (id), or use release-all."}
			break
		}
		view, changed, err = s.Programmer.ReleaseFader(req.ID, expected)
	case "release-all":
		view, changed, err = s.Programmer.ReleaseFader("", expected)
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown fader action %q; use set, release or release-all", r.PathValue("action")))
		return
	}
	var unknown patch.ErrFaderUnknown
	var bad patch.ProgrammerRequestError
	switch {
	case errors.Is(err, patch.ErrFadersStale):
		s.writeFadersJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case errors.As(err, &unknown):
		s.writeFadersJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case errors.As(err, &bad):
		s.writeFadersJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		s.writeFadersJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if changed {
		s.broadcastFaders()
	}
	s.writeFadersJSON(w, http.StatusOK, view)
}
