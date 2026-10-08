package web

import (
	"net/http"

	"benny512/internal/sacn"
)

// This file is the persisted sACN configuration's HTTP surface. The binding
// that lets the unified output engine transmit E1.31 lives in output.go.
//
// The CID is deliberately absent from both directions of the API. It is
// generated once with crypto/rand and persisted (see internal/sacn's
// settings.go), and it is the one piece of this configuration that exists to
// be STABLE rather than adjustable — a receiver distinguishes sources by CID
// (ANSI E1.31-2025 Section 6.2.3), so letting a browser set it would let a
// browser impersonate another source. It is never read out either: there is
// nothing a client could correctly do with it.

// SetSACNStorePath switches the sACN configuration to persist at path (a JSON
// file next to the exe) — mirrors SetPatchStorePath/SetLibraryStorePath. Any
// existing file at path is loaded immediately; a missing, corrupt or
// CID-less one is repaired and re-saved. The returned error reports a failed
// SAVE only: the Server is left with a usable in-memory configuration either
// way, because an unwritable directory is not a reason to refuse to run.
//
// The output engine's sACN binding (output.go) reads s.SACNSettings each time
// it opens the socket, so nothing needs re-binding here.
func (s *Server) SetSACNStorePath(path string) error {
	st, err := sacn.NewStore(path)
	s.SACNSettings = st
	return err
}

// sacnConfigJSON is the wire shape of GET/POST /api/sacn. No cid field, in
// either direction — and because decodeJSON sets DisallowUnknownFields, a
// request that tries to supply one is a 400 rather than a silent no-op.
type sacnConfigJSON struct {
	StartUniverse int    `json:"startUniverse"`
	Priority      int    `json:"priority"`
	UnicastTo     string `json:"unicastTo"`
}

func sacnConfigResponse(cfg sacn.Settings) sacnConfigJSON {
	return sacnConfigJSON{StartUniverse: cfg.StartUniverse, Priority: cfg.Priority, UnicastTo: cfg.UnicastTo}
}

func (s *Server) handleGetSACNConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sacnConfigResponse(s.SACNSettings.Get()))
}

func (s *Server) handlePostSACNConfig(w http.ResponseWriter, r *http.Request) {
	var req sacnConfigJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.SACNSettings.Set(req.StartUniverse, req.Priority, req.UnicastTo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, sacnConfigResponse(cfg))
}
