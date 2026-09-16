package web

import (
	"net"
	"net/http"

	"benny512/internal/patch"
	"benny512/internal/sacn"
)

// This file is the server half of selectable sACN output: the persisted sACN
// configuration's HTTP surface, and the binding that hands internal/patch's
// Rig Check everything it needs to actually transmit E1.31.
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
func (s *Server) SetSACNStorePath(path string) error {
	st, err := sacn.NewStore(path)
	s.SACNSettings = st
	s.RigCheck.SetSACNBinding(s.sacnBinding())
	return err
}

// sacnBinding builds the patch.SACNBinding this Server hands Rig Check. Both
// closures read the CURRENT settings every time they are called, so a POST to
// /api/sacn takes effect on the next run without re-binding anything.
func (s *Server) sacnBinding() patch.SACNBinding {
	return patch.SACNBinding{
		Open: func() (patch.SACNStream, error) {
			cfg := s.SACNSettings.Get()
			var unicast net.IP
			if cfg.UnicastTo != "" {
				unicast = net.ParseIP(cfg.UnicastTo)
			}
			return sacn.NewSender(sacn.Config{
				CID:       cfg.CID,
				Priority:  byte(cfg.Priority),
				Interface: s.OutputInterface,
				LocalIP:   s.OutputBindIP,
				UnicastTo: unicast,
				Port:      s.sacnPort,
			})
		},
		Universe: func(raw uint16) (uint16, error) {
			cfg := s.SACNSettings.Get()
			return sacn.ArtnetPortAddressToSACNUniverse(raw, s.SettingsSnapshot().ArtnetStartUniverse, cfg.StartUniverse)
		},
	}
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
