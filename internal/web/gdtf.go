// This file is the GDTF library's REST surface (chunk G1; see
// internal/gdtfshare for the sync itself and docs/plans/
// gdtf-library-brief_2026-11-17_2101.md for the owner's decisions). The GDTF
// library is a raw cache of .gdtf files from GDTF Share plus an index,
// kept apart from the Fixture Library: a file picked from it goes through the
// existing browser import path (GET /api/gdtf/file/{rid} -> gdtfparse.js ->
// POST /api/library/import), so there is no second parser.
//
// Endpoint reference (authoritative; keep in sync with routes() in
// server.go):
//
//	GET    /api/gdtf/status          -> gdtfStatusResponse
//	POST   /api/gdtf/login           <- gdtfLoginRequest   -> gdtfLoginStatus
//	DELETE /api/gdtf/login           -> gdtfLoginStatus
//	POST   /api/gdtf/login/test      <- gdtfLoginRequest (empty = the saved login) -> gdtfTestResponse
//	POST   /api/gdtf/manufacturers   <- gdtfManufacturersRequest -> gdtfManufacturersResponse
//	POST   /api/gdtf/sync            -> 202 {"status":"started"} (409 while one runs)
//	GET    /api/gdtf/search?q=&in=library|share&limit= -> gdtfSearchResponse
//	GET    /api/gdtf/match?manufacturerId=&deviceModelId=[&softwareVersionId=][&personality=] -> gdtfMatchResponse
//	POST   /api/gdtf/pull            <- gdtfPullRequest    -> gdtfshare.IndexEntry
//	GET    /api/gdtf/file/{rid}      -> the .gdtf bytes (application/octet-stream)
//
// THE PASSWORD. It is accepted by POST /api/gdtf/login and /login/test and
// is never returned by any endpoint, never logged, and never in show files,
// library exports, diagnostics or captures. In benny512-settings.json it is
// only ever a DPAPI blob (Windows, current-user scope); on other systems it
// is held in memory only. GET /api/settings drops the blob, and POST
// /api/settings keeps the saved login as it is (settings.js does not send it).
//
// The login is part of the station settings, so the full reset (POST
// /api/reset) clears it with the rest of Settings. The GDTF folder itself is
// not touched by a reset, like the Fixture Library.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"benny512/internal/gdtfshare"
)

// gdtfConfig builds the sync's configuration over this server's settings
// and adapters. root "" gives a disabled service (every test, --demo).
func (s *Server) gdtfConfig(root string) gdtfshare.Config {
	return gdtfshare.Config{
		Root:             root,
		Credentials:      s.gdtfCredentials,
		LightingNetworks: s.gdtfLightingNetworks,
		Logf: func(format string, args ...any) {
			log.Printf("[info] "+format, args...)
		},
	}
}

// SetGDTFLibraryPath points the GDTF library at root (in production
// %APPDATA%\Benny512\GDTF, from cmd/benny512). Call before Run, like the
// other Set*Path setters.
func (s *Server) SetGDTFLibraryPath(root string) {
	s.gdtf = gdtfshare.New(s.gdtfConfig(root))
}

// gdtfLightingNetworks is every subnet the lighting output uses: the Art-Net
// adapter this process bound at start-up (OutputInterface/OutputBindIP) and
// the sACN adapter in Settings ("" = the Art-Net one). No request to GDTF
// Share may leave through these (owner decision, round 2, 3).
func (s *Server) gdtfLightingNetworks() []netip.Prefix {
	out := make([]netip.Prefix, 0, 4)
	addIfi := func(ifi *net.Interface) {
		addrs, err := ifi.Addrs()
		if err != nil {
			return
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(n.IP)
			if !ok {
				continue
			}
			ones, _ := n.Mask.Size()
			if p, err := ip.Unmap().Prefix(ones); err == nil {
				out = append(out, p)
			}
		}
	}
	if s.OutputInterface != nil {
		addIfi(s.OutputInterface)
	}
	if ip, ok := netip.AddrFromSlice(s.OutputBindIP); ok && !ip.Unmap().IsUnspecified() {
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	if name := s.SettingsSnapshot().SACNNIC; name != "" {
		if ifi, err := net.InterfaceByName(name); err == nil {
			addIfi(ifi)
		}
	}
	return out
}

// gdtfCredentials is the saved login, decrypted for one use.
func (s *Server) gdtfCredentials() (string, string, bool) {
	s.settingsMu.Lock()
	user := s.settings.GDTFShareUser
	blob := append([]byte(nil), s.settings.GDTFSharePasswordDPAPI...)
	mem := s.gdtfPasswordMem
	s.settingsMu.Unlock()
	if user == "" {
		return "", "", false
	}
	if len(blob) > 0 {
		p, err := gdtfshare.UnprotectSecret(blob)
		if err != nil {
			return user, "", false
		}
		return user, string(p), true
	}
	if mem != "" {
		return user, mem, true
	}
	return user, "", false
}

// gdtfLoginStatus describes the saved login. It never carries the password.
type gdtfLoginStatus struct {
	Saved bool   `json:"saved"` // a complete login is available now
	User  string `json:"user"`
	// Storage: "encrypted" (DPAPI, survives a restart), "memory" (this
	// system has no protected store: kept until Benny512 closes),
	// "unreadable" (a saved blob this Windows account cannot decrypt),
	// "missing" (a user name with no password) or "none".
	Storage string `json:"storage"`
	Message string `json:"message"`
}

func (s *Server) gdtfLogin() gdtfLoginStatus {
	s.settingsMu.Lock()
	user := s.settings.GDTFShareUser
	blob := append([]byte(nil), s.settings.GDTFSharePasswordDPAPI...)
	mem := s.gdtfPasswordMem != ""
	s.settingsMu.Unlock()
	switch {
	case user == "":
		return gdtfLoginStatus{Storage: "none", Message: "No GDTF Share login saved"}
	case len(blob) > 0:
		if _, err := gdtfshare.UnprotectSecret(blob); err != nil {
			return gdtfLoginStatus{User: user, Storage: "unreadable", Message: "The saved password cannot be read by this Windows account on this computer; enter it again"}
		}
		return gdtfLoginStatus{Saved: true, User: user, Storage: "encrypted", Message: "Saved · user " + user}
	case mem:
		return gdtfLoginStatus{Saved: true, User: user, Storage: "memory", Message: "Saved until Benny512 closes · user " + user + " (this system cannot store the password)"}
	}
	return gdtfLoginStatus{User: user, Storage: "missing", Message: "The password for user " + user + " is not stored on this system; enter it again"}
}

type gdtfStatusResponse struct {
	gdtfshare.Status
	Login gdtfLoginStatus `json:"login"`
}

func (s *Server) handleGDTFStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, gdtfStatusResponse{Status: s.gdtf.Status(), Login: s.gdtfLogin()})
}

type gdtfLoginRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func (req gdtfLoginRequest) validate() error {
	if strings.TrimSpace(req.User) == "" || req.Password == "" {
		return errors.New("enter both the GDTF Share user name and password")
	}
	if len(req.User) > 200 || len(req.Password) > 1024 || strings.ContainsAny(req.User, "\r\n") {
		return errors.New("the GDTF Share user name or password is not usable")
	}
	return nil
}

func (s *Server) handleGDTFSetLogin(w http.ResponseWriter, r *http.Request) {
	var req gdtfLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user := strings.TrimSpace(req.User)
	var blob []byte
	mem := ""
	if gdtfshare.SecretsPersist {
		b, err := gdtfshare.ProtectSecret([]byte(req.Password))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		blob = b
	} else {
		mem = req.Password
	}
	s.settingsMu.Lock()
	updated := s.settings
	updated.GDTFShareUser = user
	updated.GDTFSharePasswordDPAPI = blob
	// Persist before publishing, like POST /api/settings.
	if err := s.settingsStore.Save(updated); err != nil {
		s.settingsMu.Unlock()
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.settings = updated
	s.gdtfPasswordMem = mem
	s.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, s.gdtfLogin())
}

func (s *Server) handleGDTFClearLogin(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.Lock()
	updated := s.settings
	updated.GDTFShareUser = ""
	updated.GDTFSharePasswordDPAPI = nil
	if err := s.settingsStore.Save(updated); err != nil {
		s.settingsMu.Unlock()
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.settings = updated
	s.gdtfPasswordMem = ""
	s.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, s.gdtfLogin())
}

// gdtfTestResponse: OK true means GDTF Share accepted the login. State uses
// gdtfshare's state words (login-failed, offline, refused, error).
type gdtfTestResponse struct {
	OK      bool   `json:"ok"`
	State   string `json:"state"`
	Message string `json:"message"`
}

func (s *Server) handleGDTFTestLogin(w http.ResponseWriter, r *http.Request) {
	var req gdtfLoginRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	user, pass := strings.TrimSpace(req.User), req.Password
	if user == "" && pass == "" {
		var ok bool
		if user, pass, ok = s.gdtfCredentials(); !ok {
			writeError(w, http.StatusBadRequest, gdtfshare.ErrNoLogin)
			return
		}
	} else if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	err := s.gdtf.TestLogin(ctx, user, pass)
	if errors.Is(err, gdtfshare.ErrBusy) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		res := gdtfResultFor(err)
		writeJSON(w, http.StatusOK, gdtfTestResponse{State: res.State, Message: res.Message})
		return
	}
	writeJSON(w, http.StatusOK, gdtfTestResponse{OK: true, State: gdtfshare.StateOK, Message: "GDTF Share accepted the login"})
}

// gdtfResultFor names an error in the sync's state words.
func gdtfResultFor(err error) gdtfshare.Result {
	var off *gdtfshare.OfflineError
	switch {
	case errors.Is(err, gdtfshare.ErrLightingRoute):
		return gdtfshare.Result{State: gdtfshare.StateRefused, Message: err.Error()}
	case errors.As(err, &off):
		return gdtfshare.Result{State: gdtfshare.StateOffline, Message: "Offline: " + err.Error()}
	case errors.Is(err, gdtfshare.ErrBadLogin):
		return gdtfshare.Result{State: gdtfshare.StateLoginFailed, Message: err.Error()}
	}
	return gdtfshare.Result{State: gdtfshare.StateError, Message: err.Error()}
}

// gdtfErrStatus maps a pull/sync error to an HTTP status.
func gdtfErrStatus(err error) int {
	var off *gdtfshare.OfflineError
	switch {
	case errors.Is(err, gdtfshare.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, gdtfshare.ErrNoLogin):
		return http.StatusBadRequest
	case errors.Is(err, gdtfshare.ErrDisabled), errors.Is(err, gdtfshare.ErrNotRunning),
		errors.Is(err, gdtfshare.ErrLightingRoute), errors.As(err, &off):
		return http.StatusServiceUnavailable
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

type gdtfManufacturersRequest struct {
	Manufacturers []string `json:"manufacturers"`
}

type gdtfManufacturersResponse struct {
	Followed []string `json:"followed"`
}

func (s *Server) handleGDTFSetManufacturers(w http.ResponseWriter, r *http.Request) {
	var req gdtfManufacturersRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Manufacturers == nil {
		writeError(w, http.StatusBadRequest, errors.New("manufacturers is required (an empty list follows none)"))
		return
	}
	got, err := s.gdtf.SetFollowed(req.Manufacturers)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, gdtfshare.ErrDisabled) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, gdtfManufacturersResponse{Followed: got})
}

func (s *Server) handleGDTFSync(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.gdtfCredentials(); !ok {
		writeError(w, http.StatusBadRequest, gdtfshare.ErrNoLogin)
		return
	}
	if err := s.gdtf.Trigger(); err != nil {
		writeError(w, gdtfErrStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

type gdtfSearchResponse struct {
	In    string `json:"in"`
	Query string `json:"query"`
	// Count deliberately has no omitempty: zero hits is an answer.
	Count   int                      `json:"count"`
	Results []gdtfshare.SearchResult `json:"results"`
}

func (s *Server) handleGDTFSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := q.Get("in")
	if in == "" {
		in = "library"
	}
	if in != "library" && in != "share" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("in must be library or share, got %q", in))
		return
	}
	limit := 200
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 2000 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit must be 1-2000, got %q", v))
			return
		}
		limit = n
	}
	res, err := s.gdtf.Search(q.Get("q"), in == "share", limit)
	if err != nil {
		writeError(w, gdtfErrStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, gdtfSearchResponse{In: in, Query: q.Get("q"), Count: len(res), Results: res})
}

// gdtfMatchQuery echoes what was asked. The optional values are null when
// not given, never 0 (a plausible 0 is not "unknown").
type gdtfMatchQuery struct {
	ManufacturerID    uint16  `json:"manufacturerId"`
	DeviceModelID     uint16  `json:"deviceModelId"`
	SoftwareVersionID *uint32 `json:"softwareVersionId"`
	Personality       *uint8  `json:"personality"`
}

type gdtfMatchResponse struct {
	Query gdtfMatchQuery `json:"query"`
	// Count deliberately has no omitempty: zero candidates is an answer.
	Count int `json:"count"`
	gdtfshare.MatchResult
}

// parseRDMNumber reads an RDM ID from a query value: decimal ("8870", as
// the Devices JSON carries it) or hex with a 0x prefix ("0x22A6", as RDM
// IDs are written). Hex needs the prefix, so "1093" is always decimal.
func parseRDMNumber(name, v string, bits int) (uint64, error) {
	t := strings.TrimSpace(v)
	base := 10
	if len(t) > 2 && (t[:2] == "0x" || t[:2] == "0X") {
		t, base = t[2:], 16
	}
	n, err := strconv.ParseUint(t, base, bits)
	if err != nil || t == "" {
		return 0, fmt.Errorf("%s must be a whole number from 0 to %d, decimal or 0x hex; got %q", name, uint64(1)<<bits-1, v)
	}
	return n, nil
}

// handleGDTFMatch: library files for a discovered device's RDM identity
// (gdtfshare.Match, whose doc comment explains the ranking). manufacturerId
// and deviceModelId are required; softwareVersionId and personality are
// optional (leave them out when unknown). personality is 1-255: RDM
// numbers DMX personalities from 1 (params.SetDMXPersonality). No RDM match
// is 200 with an empty list; there is no fallback to names.
func (s *Server) handleGDTFMatch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var mq gdtfshare.MatchQuery
	for _, p := range []struct {
		name string
		dst  *uint16
	}{{"manufacturerId", &mq.ManufacturerID}, {"deviceModelId", &mq.DeviceModelID}} {
		if !q.Has(p.name) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s is required", p.name))
			return
		}
		n, err := parseRDMNumber(p.name, q.Get(p.name), 16)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		*p.dst = uint16(n)
	}
	if q.Has("softwareVersionId") {
		n, err := parseRDMNumber("softwareVersionId", q.Get("softwareVersionId"), 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		v := uint32(n)
		mq.SoftwareVersionID = &v
	}
	if q.Has("personality") {
		n, err := parseRDMNumber("personality", q.Get("personality"), 8)
		if err == nil && n == 0 {
			err = errors.New("personality must be 1-255: RDM numbers DMX personalities from 1 (leave it out when unknown)")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		v := uint8(n)
		mq.Personality = &v
	}
	res, err := s.gdtf.Match(mq)
	if err != nil {
		writeError(w, gdtfErrStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, gdtfMatchResponse{
		Query: gdtfMatchQuery{ManufacturerID: mq.ManufacturerID, DeviceModelID: mq.DeviceModelID,
			SoftwareVersionID: mq.SoftwareVersionID, Personality: mq.Personality},
		Count: len(res.Candidates), MatchResult: res,
	})
}

type gdtfPullRequest struct {
	RID int64 `json:"rid"`
}

func (s *Server) handleGDTFPull(w http.ResponseWriter, r *http.Request) {
	var req gdtfPullRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RID <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("rid must be a positive GDTF Share revision id"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	e, err := s.gdtf.Pull(ctx, req.RID)
	if err != nil {
		writeError(w, gdtfErrStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleGDTFFile serves a downloaded file's bytes for the browser import.
func (s *Server) handleGDTFFile(w http.ResponseWriter, r *http.Request) {
	rid, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil || rid <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad rid %q", r.PathValue("rid")))
		return
	}
	f, e, err := s.gdtf.OpenFile(rid)
	if err != nil {
		writeError(w, gdtfErrStatus(err), err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": gdtfFileName(e)}))
	w.Header().Set("X-Benny-GDTF-Uploader", e.UploaderLabel)
	w.Header().Set("X-Benny-GDTF-SHA256", e.SHA256)
	http.ServeContent(w, r, "", e.DownloadedAt, f)
}

// gdtfFileName is a plain .gdtf file name (internal/library's source-file
// rule: no slashes, no line breaks): Manufacturer@Fixture@Revision.gdtf.
func gdtfFileName(e gdtfshare.IndexEntry) string {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if r < 0x20 || strings.ContainsRune(`/\:*?"<>|@`, r) {
				return '_'
			}
			return r
		}, strings.TrimSpace(s))
		if r := []rune(s); len(r) > 60 {
			s = string(r[:60])
		}
		return s
	}
	name := clean(e.Manufacturer) + "@" + clean(e.Fixture) + "@" + clean(e.Revision)
	if strings.Trim(name, "@_ ") == "" {
		name = strconv.FormatInt(e.RID, 10)
	}
	return name + ".gdtf"
}
