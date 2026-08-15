// This file implements Rig Walk mode's REST surface: a phone-optimized,
// one-device-at-a-time walkthrough that auto-drives IDENTIFY_DEVICE (ON for
// whatever's on screen, OFF for whatever was) so exactly one fixture is
// ever flashing, plus a verification checklist (Confirmed/Problem/note),
// running progress, a server-persisted session, and export.
//
// Endpoint reference (authoritative; keep in sync with routes() in
// server.go):
//
//	GET  /api/walk/session               -> walkSessionResponse ({"active":false} if none)
//	POST /api/walk/session                <- startWalkRequest      -> walkSessionResponse (builds a new session, lands on device 0)
//	POST /api/walk/end                    -> {"status":"ok"}       (identify-off best-effort, discards the session)
//	POST /api/walk/goto                   <- walkGotoRequest       -> walkSessionResponse (moves the cursor, swaps identify)
//	POST /api/walk/autoadvance             <- walkAutoAdvanceRequest -> walkSessionResponse
//	POST /api/walk/{uid}/status            <- walkStatusRequest    -> walkSessionResponse (auto-advances on Confirmed if enabled)
//	POST /api/walk/{uid}/address           <- walkAddressRequest   -> {"status":"ok"} (quick DMX start-address fix, Apply-to-confirm on the client)
//	POST /api/walk/identify/retry          -> walkSessionResponse (re-sends ON for the current device)
//	POST /api/walk/identify/off            -> {"status":"ok"} (lightweight: current device only — used by pagehide/visibilitychange)
//	POST /api/walk/identify/all-off        -> walkSessionResponse (the big manual escape button: every device in the session)
//	GET  /api/walk/export?format=json|txt -> file download, same Content-Disposition pattern as /api/capture/export
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
	"benny512/internal/walk"
)

// --- response envelope ------------------------------------------------------

type walkSessionResponse struct {
	Active  bool          `json:"active"`
	Session *walk.Session `json:"session,omitempty"`
	Summary *walk.Summary `json:"summary,omitempty"`
}

func toWalkSessionResponse(sess walk.Session) walkSessionResponse {
	sum := sess.Summary()
	return walkSessionResponse{Active: true, Session: &sess, Summary: &sum}
}

func (s *Server) handleGetWalkSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.walkStore.Get()
	if !ok {
		writeJSON(w, http.StatusOK, walkSessionResponse{Active: false})
		return
	}
	writeJSON(w, http.StatusOK, toWalkSessionResponse(sess))
}

// --- starting/ending a session -----------------------------------------------

type startWalkRequest struct {
	Order        string `json:"order"`        // "address" (default) | "discovery"
	ScopeKind    string `json:"scopeKind"`    // "" / "all" | "node" | "port" | "universe" | "class"
	ScopeValue   string `json:"scopeValue"`   // interpretation depends on ScopeKind — see walkCandidates
	FixturesOnly bool   `json:"fixturesOnly"` // task ask: "defaulting to fixtures" — the UI defaults this checkbox checked
}

func (s *Server) handleStartWalkSession(w http.ResponseWriter, r *http.Request) {
	var req startWalkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	order := walk.OrderAddress
	if req.Order == "discovery" {
		order = walk.OrderDiscovery
	}

	candidates, scope, err := s.walkCandidates(req.ScopeKind, req.ScopeValue)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.FixturesOnly {
		// A device that hasn't been classified yet (Class still Unknown —
		// nobody has opened its Devices-screen Info tab this session, so no
		// DEVICE_INFO/PRODUCT_DETAIL/PROXIED_DEVICE_COUNT probe has run) is
		// kept in rather than dropped: excluding it would risk a confusing
		// "no devices match the selected scope" on a fresh session where
		// Dom hasn't browsed the Devices tab yet, which is a worse failure
		// mode than occasionally walking one extra infrastructure device.
		// A device with a confirmed *non*-fixture class is still excluded.
		only := make([]registry.Fixture, 0, len(candidates))
		for _, f := range candidates {
			if f.Class == registry.ClassFixture || f.Class == registry.ClassUnknown {
				only = append(only, f)
			}
		}
		candidates = only
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("no devices match the selected scope"))
		return
	}

	// Best-effort: never leave a previous session's device flashing when
	// starting a fresh walk over it.
	if prev, ok := s.walkStore.Get(); ok {
		if dev, ok2 := prev.CurrentDevice(); ok2 {
			_ = s.walkSetIdentify(dev.UID, false)
		}
	}

	addrs := s.resolveWalkAddresses(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		if order == walk.OrderDiscovery {
			return candidates[i].FirstSeen.Before(candidates[j].FirstSeen)
		}
		pi, pj := candidates[i].Port.RawValue(), candidates[j].Port.RawValue()
		if pi != pj {
			return pi < pj
		}
		return addrs[candidates[i].UID.String()].addr < addrs[candidates[j].UID.String()].addr
	})

	devices := make([]walk.Device, len(candidates))
	for i, f := range candidates {
		a := addrs[f.UID.String()]
		devices[i] = buildWalkDevice(f, a.addr, a.known)
	}

	s.walkStore.Replace(walk.Session{
		Order: order, Scope: scope, FixturesOnly: req.FixturesOnly,
		Devices: devices, Current: -1, AutoAdvance: true,
	})

	// Land on device 0: this sends the first IDENTIFY_DEVICE ON.
	resp, err := s.walkAdvanceTo(0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWalkEnd(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.walkStore.Get(); ok {
		if dev, ok2 := sess.CurrentDevice(); ok2 {
			_ = s.walkSetIdentify(dev.UID, false)
		}
	}
	s.walkStore.Clear()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// walkCandidates resolves a scope selector to the registry.Fixtures it
// covers, reusing the same node/port shape the Devices screen's node/port
// selector already sends (JSON {"ip":...,"bindIndex":...,"portAddress":...})
// so the UI doesn't need a second selector convention.
func (s *Server) walkCandidates(kind, value string) ([]registry.Fixture, walk.Scope, error) {
	all := s.Registry.Devices()
	switch kind {
	case "", "all":
		return all, walk.Scope{Kind: "all", Label: "All devices"}, nil
	case "node":
		ip, err := netip.ParseAddr(value)
		if err != nil {
			return nil, walk.Scope{}, fmt.Errorf("bad node IP %q: %w", value, err)
		}
		return s.Registry.FixturesOnNode(session.NodeKey{IP: ip, BindIndex: 1}),
			walk.Scope{Kind: "node", Label: "Node " + value}, nil
	case "port":
		var sel struct {
			IP          string `json:"ip"`
			BindIndex   byte   `json:"bindIndex"`
			PortAddress uint16 `json:"portAddress"`
		}
		if err := json.Unmarshal([]byte(value), &sel); err != nil {
			return nil, walk.Scope{}, fmt.Errorf("bad port scope value: %w", err)
		}
		ip, err := netip.ParseAddr(sel.IP)
		if err != nil {
			return nil, walk.Scope{}, fmt.Errorf("bad node IP %q: %w", sel.IP, err)
		}
		bind := sel.BindIndex
		if bind == 0 {
			bind = 1
		}
		pa, err := artnet.PortAddressFromRaw(sel.PortAddress)
		if err != nil {
			return nil, walk.Scope{}, err
		}
		return s.Registry.Fixtures(session.NodeKey{IP: ip, BindIndex: bind}, pa, true),
			walk.Scope{Kind: "port", Label: fmt.Sprintf("%s port-addr %d", sel.IP, sel.PortAddress)}, nil
	case "universe":
		u, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return nil, walk.Scope{}, fmt.Errorf("bad universe %q: %w", value, err)
		}
		var out []registry.Fixture
		for _, f := range all {
			if f.Port.RawValue() == uint16(u) {
				out = append(out, f)
			}
		}
		return out, walk.Scope{Kind: "universe", Label: fmt.Sprintf("Universe (Port-Address) %d", u)}, nil
	case "class":
		var out []registry.Fixture
		for _, f := range all {
			if f.Class.String() == value {
				out = append(out, f)
			}
		}
		return out, walk.Scope{Kind: "class", Label: "Class " + value}, nil
	default:
		return nil, walk.Scope{}, fmt.Errorf("unknown scope kind %q", kind)
	}
}

// buildWalkDevice snapshots a registry.Fixture into a walk.Device — reuses
// the Devices screen's own Manufacturer/Model priority-chain helpers
// (effectiveManufacturer/effectiveModel in device.go) so a Rig Walk entry
// reads identically to what the Devices table already shows.
func buildWalkDevice(f registry.Fixture, addr uint16, addrKnown bool) walk.Device {
	return walk.Device{
		UID: f.UID.String(), Manufacturer: effectiveManufacturer(f), Model: effectiveModel(f),
		Class: f.Class.String(), IsWirelessProxy: f.IsWirelessProxy,
		NodeIP: f.Node.IP.String(), BindIndex: f.Node.BindIndex, PortAddress: f.Port.RawValue(),
		DMXStartAddress: addr, DMXFootprint: f.DMXFootprint, AddressKnown: addrKnown,
		Status: walk.StatusUnvisited,
	}
}

type walkAddr struct {
	addr  uint16
	known bool
}

// resolveWalkAddresses resolves every candidate's DMX start address
// concurrently (report-confirmed: RDMController serializes all commands for
// one UID regardless of how many are fired client-side at once, so this
// fan-out across *different* UIDs is safe even with a slow wireless-proxied
// device on the ACK_TIMER path — matches devices.js's classifyUnknown
// fan-out).
func (s *Server) resolveWalkAddresses(candidates []registry.Fixture) map[string]walkAddr {
	out := make(map[string]walkAddr, len(candidates))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, f := range candidates {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr, known := s.resolveWalkAddress(f)
			mu.Lock()
			out[f.UID.String()] = walkAddr{addr, known}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// resolveWalkAddress prefers the registry's already-cached DMX_START_ADDRESS
// (no wire traffic — set if the Devices screen's Parameters tab has already
// been opened for this device this session) and only falls back to a live
// GET when nothing is cached. A device that NACKs/times out (a
// zero-footprint infrastructure device, or a proxy having a bad day)
// resolves as known=false rather than guessing 0.
func (s *Server) resolveWalkAddress(f registry.Fixture) (uint16, bool) {
	if data, ok := f.Params[rdm.PIDDMXStartAddress]; ok && len(data) == 2 {
		return uint16(data[0])<<8 | uint16(data[1]), true
	}
	node, ok := s.Registry.FixtureNode(f.UID)
	if !ok {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), deviceParamTimeout)
	defer cancel()
	client := params.New(s.RDM, node, f.UID)
	addr, err := client.DMXStartAddress(ctx)
	if err != nil {
		return 0, false
	}
	return addr, true
}

// --- cursor / identify swap --------------------------------------------------

type walkGotoRequest struct {
	Index int `json:"index"`
}

func (s *Server) handleWalkGoto(w http.ResponseWriter, r *http.Request) {
	var req walkGotoRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.walkAdvanceTo(req.Index)
	if err != nil {
		writeWalkStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// walkAdvanceTo moves the session's cursor to index, best-effort turning
// Identify OFF on whatever was current and ON for the new current device.
// Per task ask, a failure on either leg (NACK/timeout — the expected path
// through a wireless proxy) is recorded on the relevant Device's
// IdentifyErr field but never fails this call: the walk must never get
// stuck because one fixture didn't answer.
func (s *Server) walkAdvanceTo(index int) (walkSessionResponse, error) {
	sess, ok := s.walkStore.Get()
	if !ok {
		return walkSessionResponse{}, walk.ErrNoSession
	}
	if index < 0 || index >= len(sess.Devices) {
		return walkSessionResponse{}, fmt.Errorf("index %d out of range [0,%d)", index, len(sess.Devices))
	}

	prevIdx := sess.Current
	if prevIdx >= 0 && prevIdx < len(sess.Devices) && prevIdx != index {
		prevUID := sess.Devices[prevIdx].UID
		offErr := s.walkSetIdentify(prevUID, false)
		_, _ = s.walkStore.Mutate(func(ss *walk.Session) error {
			if prevIdx < len(ss.Devices) && ss.Devices[prevIdx].UID == prevUID {
				ss.Devices[prevIdx].IdentifyOn = false
				ss.Devices[prevIdx].IdentifyErr = errString(offErr)
			}
			return nil
		})
	}

	newUID := sess.Devices[index].UID
	onErr := s.walkSetIdentify(newUID, true)
	updated, err := s.walkStore.Mutate(func(ss *walk.Session) error {
		ss.Current = index
		if index < len(ss.Devices) {
			ss.Devices[index].IdentifyOn = onErr == nil
			ss.Devices[index].IdentifyErr = errString(onErr)
		}
		return nil
	})
	if err != nil {
		return walkSessionResponse{}, err
	}
	return toWalkSessionResponse(updated), nil
}

// --- auto-advance toggle -----------------------------------------------------

type walkAutoAdvanceRequest struct {
	On bool `json:"on"`
}

func (s *Server) handleWalkAutoAdvance(w http.ResponseWriter, r *http.Request) {
	var req walkAutoAdvanceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.walkStore.Mutate(func(ss *walk.Session) error {
		ss.AutoAdvance = req.On
		return nil
	})
	if err != nil {
		writeWalkStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWalkSessionResponse(updated))
}

// --- verification checklist --------------------------------------------------

type walkStatusRequest struct {
	Status string `json:"status"` // "confirmed" | "problem" | "unvisited"
	Note   string `json:"note,omitempty"`
}

func (s *Server) handleWalkStatus(w http.ResponseWriter, r *http.Request) {
	uid, ok := rdm.ParseUID(r.PathValue("uid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad uid %q", r.PathValue("uid")))
		return
	}
	uidStr := uid.String()

	var req walkStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, ok := parseWalkStatus(req.Status)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("status must be confirmed|problem|unvisited, got %q", req.Status))
		return
	}

	matchedIdx := -1
	autoAdvanceNow := false
	updated, err := s.walkStore.Mutate(func(ss *walk.Session) error {
		idx := ss.IndexOf(uidStr)
		if idx < 0 {
			return fmt.Errorf("device %s is not part of the active walk session", uidStr)
		}
		matchedIdx = idx
		ss.Devices[idx].Status = status
		ss.Devices[idx].Note = req.Note
		if status == walk.StatusUnvisited {
			ss.Devices[idx].VisitedAt = time.Time{}
		} else {
			ss.Devices[idx].VisitedAt = time.Now()
		}
		autoAdvanceNow = status == walk.StatusConfirmed && ss.AutoAdvance && idx == ss.Current && idx+1 < len(ss.Devices)
		return nil
	})
	if err != nil {
		if errors.Is(err, walk.ErrNoSession) {
			writeWalkStoreError(w, err)
		} else {
			writeError(w, http.StatusNotFound, err)
		}
		return
	}

	if autoAdvanceNow {
		if resp, aerr := s.walkAdvanceTo(matchedIdx + 1); aerr == nil {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		// Advance failed for some reason (shouldn't happen — session was
		// just confirmed present above); fall through to returning the
		// status-only update rather than losing the confirm.
	}
	writeJSON(w, http.StatusOK, toWalkSessionResponse(updated))
}

func parseWalkStatus(s string) (walk.Status, bool) {
	switch walk.Status(s) {
	case walk.StatusConfirmed, walk.StatusProblem, walk.StatusUnvisited:
		return walk.Status(s), true
	default:
		return "", false
	}
}

// --- quick DMX start-address fix ---------------------------------------------

type walkAddressRequest struct {
	Value uint16 `json:"value"`
}

// handleWalkAddress applies a corrected DMX start address directly from the
// walk screen (task ask: "discovering a wrong address is the main reason a
// tech notices a problem"). The client stages the value and only calls this
// on an explicit Apply tap (Apply-to-confirm rule) — this handler itself
// commits immediately, exactly like the existing
// POST /api/fixture/{uid}/param/dmx_start_address path it wraps.
func (s *Server) handleWalkAddress(w http.ResponseWriter, r *http.Request) {
	uid, ok := rdm.ParseUID(r.PathValue("uid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad uid %q", r.PathValue("uid")))
		return
	}
	var req walkAddressRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown device %s", uid))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	client := params.New(s.RDM, node, uid)
	if err := client.SetDMXStartAddress(ctx, req.Value); err != nil {
		writeParamError(w, err)
		return
	}
	// Best-effort reflect the corrected value in the walk session snapshot
	// so the screen updates without needing to leave the walk.
	_, _ = s.walkStore.Mutate(func(ss *walk.Session) error {
		if idx := ss.IndexOf(uid.String()); idx >= 0 {
			ss.Devices[idx].DMXStartAddress = req.Value
			ss.Devices[idx].AddressKnown = true
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- identify safety net -----------------------------------------------------

// handleWalkIdentifyRetry re-sends IDENTIFY_DEVICE ON for the current
// device (task ask: "keep a big 'retry identify' affordance" next to an
// inline NACK/timeout error).
func (s *Server) handleWalkIdentifyRetry(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.walkStore.Get()
	if !ok {
		writeWalkStoreError(w, walk.ErrNoSession)
		return
	}
	dev, ok := sess.CurrentDevice()
	if !ok {
		writeError(w, http.StatusConflict, fmt.Errorf("no current device"))
		return
	}
	identErr := s.walkSetIdentify(dev.UID, true)
	updated, err := s.walkStore.Mutate(func(ss *walk.Session) error {
		if cur, ok := ss.CurrentDevice(); ok && cur.UID == dev.UID {
			ss.Devices[ss.Current].IdentifyOn = identErr == nil
			ss.Devices[ss.Current].IdentifyErr = errString(identErr)
		}
		return nil
	})
	if err != nil {
		writeWalkStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWalkSessionResponse(updated))
}

// handleWalkIdentifyOffCurrent is the lightweight safety call: turns off
// Identify for the current device only. Used by the client's
// visibilitychange/pagehide handlers and on navigating away from Rig
// Walk — cheap and fast enough to fire-and-forget from an unload handler,
// unlike the full sweep below.
func (s *Server) handleWalkIdentifyOffCurrent(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.walkStore.Get()
	if ok {
		if dev, ok2 := sess.CurrentDevice(); ok2 {
			identErr := s.walkSetIdentify(dev.UID, false)
			_, _ = s.walkStore.Mutate(func(ss *walk.Session) error {
				if cur, ok := ss.CurrentDevice(); ok && cur.UID == dev.UID {
					ss.Devices[ss.Current].IdentifyOn = false
					ss.Devices[ss.Current].IdentifyErr = errString(identErr)
				}
				return nil
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleWalkIdentifyAllOff is the manual "Identify off / all off" escape
// button (task ask): best-effort turns Identify off for every device in the
// session, not just the current one, so a stuck-flashing fixture from an
// earlier bug/race can always be cleared without hunting for it. Clears the
// cursor (Current = -1) since nothing is under the spotlight afterward.
func (s *Server) handleWalkIdentifyAllOff(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.walkStore.Get()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	errs := make([]string, len(sess.Devices))
	var wg sync.WaitGroup
	for i, d := range sess.Devices {
		i, uid := i, d.UID
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = errString(s.walkSetIdentify(uid, false))
		}()
	}
	wg.Wait()

	updated, err := s.walkStore.Mutate(func(ss *walk.Session) error {
		for i := range ss.Devices {
			if i < len(errs) {
				ss.Devices[i].IdentifyOn = false
				ss.Devices[i].IdentifyErr = errs[i]
			}
		}
		ss.Current = -1
		return nil
	})
	if err != nil {
		writeWalkStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWalkSessionResponse(updated))
}

// walkSetIdentify issues SET IDENTIFY_DEVICE(on) for uidStr against the
// live registry/controller. Best-effort by design: every caller in this
// file records the returned error onto the relevant Device.IdentifyErr
// rather than failing its own request, per the task's "never blocks the
// walk" rule.
func (s *Server) walkSetIdentify(uidStr string, on bool) error {
	uid, ok := rdm.ParseUID(uidStr)
	if !ok {
		return fmt.Errorf("bad uid %q", uidStr)
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		return fmt.Errorf("device %s not currently reachable", uidStr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), deviceParamTimeout)
	defer cancel()
	client := params.New(s.RDM, node, uid)
	return client.SetIdentifyDevice(ctx, on)
}

func writeWalkStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, walk.ErrNoSession) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

// --- export --------------------------------------------------------------

// handleWalkExport serves the active (or most recently persisted) walk
// session as a download, same Content-Disposition/timestamped-filename
// pattern as GET /api/capture/export (task ask: "same pattern and download
// mechanics as the existing capture export").
func (s *Server) handleWalkExport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "txt" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("format must be json or txt, got %q", format))
		return
	}
	sess, ok := s.walkStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no walk session to export"))
		return
	}

	now := time.Now()
	filename := fmt.Sprintf("benny512_rigwalk_%s.%s", now.Format("20060102_150405"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		doc := walkExportDoc{AppVersion: AppVersion, GeneratedAt: now, Session: sess, Summary: sess.Summary()}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writeWalkExportText(w, now, sess)
	}
}

type walkExportDoc struct {
	AppVersion  string       `json:"appVersion"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Session     walk.Session `json:"session"`
	Summary     walk.Summary `json:"summary"`
}

func writeWalkExportText(w io.Writer, at time.Time, sess walk.Session) {
	sum := sess.Summary()
	scopeExtra := ""
	if sess.FixturesOnly {
		scopeExtra = ", fixtures only"
	} else {
		scopeExtra = ", all RDM devices"
	}
	fmt.Fprintln(w, "Benny512 Rig Walk Export")
	fmt.Fprintf(w, "App version: %s\n", AppVersion)
	fmt.Fprintf(w, "Generated:   %s\n", at.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "Scope:       %s (order: %s%s)\n", sess.Scope.Label, sess.Order, scopeExtra)
	fmt.Fprintf(w, "Started:     %s\n", sess.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "Summary:     %d confirmed, %d problem(s), %d unvisited (of %d)\n", sum.Confirmed, sum.Problems, sum.Remaining, sum.Total)
	fmt.Fprintln(w, strings.Repeat("=", 78))
	fmt.Fprintln(w)

	if len(sess.Devices) == 0 {
		fmt.Fprintln(w, "(no devices in this walk)")
		return
	}

	for i, d := range sess.Devices {
		fmt.Fprintf(w, "%3d. [%s] %s — %s\n", i+1, strings.ToUpper(string(d.Status)), d.Manufacturer, d.Model)
		fmt.Fprintf(w, "     UID %s | node %s port-addr %d | DMX ", d.UID, d.NodeIP, d.PortAddress)
		if d.AddressKnown {
			fmt.Fprintf(w, "%d", d.DMXStartAddress)
			if d.DMXFootprint > 1 {
				fmt.Fprintf(w, "-%d", int(d.DMXStartAddress)+int(d.DMXFootprint)-1)
			}
		} else {
			fmt.Fprint(w, "unknown")
		}
		fmt.Fprintln(w)
		if d.Class != "" {
			proxy := ""
			if d.IsWirelessProxy {
				proxy = " (wireless proxy)"
			}
			fmt.Fprintf(w, "     class: %s%s\n", d.Class, proxy)
		}
		if d.Note != "" {
			fmt.Fprintf(w, "     note: %s\n", d.Note)
		}
		if !d.VisitedAt.IsZero() {
			fmt.Fprintf(w, "     visited: %s\n", d.VisitedAt.Format("2006-01-02 15:04:05 MST"))
		}
		if d.IdentifyErr != "" {
			fmt.Fprintf(w, "     last identify error: %s\n", d.IdentifyErr)
		}
		fmt.Fprintln(w)
	}
}
