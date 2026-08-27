// This file implements the Patch screen's REST surface: patch CRUD, the
// collision report, the patch<->RDM reconcile report and its actions
// (single fix, bulk fix, adopt-from-discovered), rig-check control/state,
// and export (task ask, item 5). See internal/patch's package doc comment
// for the model this wraps; this file's job is entirely HTTP/JSON framing
// plus adapting registry.Fixture (RDM-package-aware) into
// patch.DiscoveredDevice (RDM-package-free) at the boundary.
//
// Endpoint reference (authoritative; keep in sync with routes() in
// server.go):
//
//	GET    /api/patch                            -> patchResponse ({"active":false} if none created yet)
//	POST   /api/patch/new                         <- newPatchRequest      -> patchResponse (discards any existing patch)
//	POST   /api/patch/entries                     <- entryRequest         -> patchResponse (creates one entry, lazy-inits the patch)
//	PUT    /api/patch/entries/{id}                <- entryRequest         -> patchResponse
//	DELETE /api/patch/entries/{id}                -> patchResponse
//	POST   /api/patch/reorder                     <- reorderRequest       -> patchResponse
//	GET    /api/patch/collisions                  -> []patch.Finding
//	GET    /api/patch/reconcile                   -> patch.Report
//	POST   /api/patch/reconcile/{id}/confirm      <- reconcileConfirmRequest -> patchResponse
//	POST   /api/patch/reconcile/{id}/reject       -> patchResponse
//	POST   /api/patch/reconcile/{id}/fix          <- reconcileConfirmRequest -> patchResponse (one-tap SET DMX_START_ADDRESS + confirm)
//	POST   /api/patch/reconcile/fix-all           <- fixAllRequest        -> fixAllResponse (confirm:false = preview only, never applies)
//	POST   /api/patch/adopt                       <- adoptRequest         -> patchResponse
//	POST   /api/patch/import                      <- importRequest       -> patchResponse
//	GET    /api/patch/export?format=json|txt      -> file download
//	GET    /api/patch/reconcile/export?format=json|txt -> file download
//	GET    /api/patch/rigcheck                    -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/start              <- rigCheckStartRequest -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/stop               -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/blackout           -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/next               -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/previous           -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/jump               <- rigCheckJumpRequest  -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/mode               <- rigCheckModeRequest  -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/level              <- rigCheckLevelRequest -> rigCheckStateJSON
//	POST   /api/patch/rigcheck/channel            <- rigCheckChannelRequest -> rigCheckStateJSON
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"benny512/internal/params"
	"benny512/internal/patch"
	"benny512/internal/rdm"
)

// --- patch CRUD --------------------------------------------------------

type patchResponse struct {
	Active bool         `json:"active"`
	Patch  *patch.Patch `json:"patch,omitempty"`
}

func toPatchResponse(p patch.Patch) patchResponse { return patchResponse{Active: true, Patch: &p} }

func (s *Server) handleGetPatch(w http.ResponseWriter, r *http.Request) {
	p, ok := s.PatchStore.Get()
	if !ok {
		writeJSON(w, http.StatusOK, patchResponse{Active: false})
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(p))
}

type newPatchRequest struct {
	Name string `json:"name"`
}

// handleNewPatch discards whatever patch is active and starts a fresh,
// empty one — the explicit "start a new patch" action (distinct from
// EnsureActive's silent lazy-init the first time an entry is added).
func (s *Server) handleNewPatch(w http.ResponseWriter, r *http.Request) {
	var req newPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := req.Name
	if name == "" {
		name = "Patch"
	}
	p := s.PatchStore.Replace(patch.Patch{Name: name})
	writeJSON(w, http.StatusOK, toPatchResponse(p))
}

// entryRequest is both the create and update payload — task ask's field
// list verbatim (name, fixture type/model, mode, footprint, universe,
// start address, position, fixture number, notes).
type entryRequest struct {
	Name          string `json:"name"`
	FixtureType   string `json:"fixtureType"`
	Mode          string `json:"mode"`
	Footprint     uint16 `json:"footprint"`
	Universe      uint16 `json:"universe"`
	StartAddress  uint16 `json:"startAddress"`
	Position      string `json:"position"`
	FixtureNumber string `json:"fixtureNumber"`
	Notes         string `json:"notes"`
}

func (s *Server) handleCreatePatchEntry(w http.ResponseWriter, r *http.Request) {
	var req entryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.PatchStore.EnsureActive()
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		pp.Entries = append(pp.Entries, entryFromRequest(patch.NewEntryID(), req))
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

func (s *Server) handleUpdatePatchEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req entryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		confirmedUID, matchState := pp.Entries[idx].ConfirmedUID, pp.Entries[idx].MatchState
		pp.Entries[idx] = entryFromRequest(id, req)
		// A plain field edit (fixing a typo, adjusting Notes) must not
		// silently discard a confirmed RDM pairing — only an explicit
		// reconcile confirm/reject/fix action changes ConfirmedUID/
		// MatchState (task ask: "confirmed pairings ... never
		// re-litigated").
		pp.Entries[idx].ConfirmedUID = confirmedUID
		pp.Entries[idx].MatchState = matchState
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

func entryFromRequest(id string, req entryRequest) patch.Entry {
	return patch.Entry{
		ID: id, Name: req.Name, FixtureType: req.FixtureType, Mode: req.Mode,
		Footprint: req.Footprint, Universe: req.Universe, StartAddress: req.StartAddress,
		Position: req.Position, FixtureNumber: req.FixtureNumber, Notes: req.Notes,
	}
}

func (s *Server) handleDeletePatchEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries = append(pp.Entries[:idx], pp.Entries[idx+1:]...)
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

type reorderRequest struct {
	Order []string `json:"order"`
}

func (s *Server) handleReorderPatch(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		if len(req.Order) != len(pp.Entries) {
			return fmt.Errorf("reorder must list all %d entries, got %d", len(pp.Entries), len(req.Order))
		}
		byID := make(map[string]patch.Entry, len(pp.Entries))
		for _, e := range pp.Entries {
			byID[e.ID] = e
		}
		seen := make(map[string]bool, len(req.Order))
		next := make([]patch.Entry, 0, len(req.Order))
		for _, id := range req.Order {
			if seen[id] {
				return fmt.Errorf("reorder: duplicate entry id %q", id)
			}
			e, ok := byID[id]
			if !ok {
				return fmt.Errorf("reorder: unknown entry id %q", id)
			}
			seen[id] = true
			next = append(next, e)
		}
		pp.Entries = next
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

func writePatchStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, patch.ErrNoPatch) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusNotFound, err)
}

// --- collisions ----------------------------------------------------------

func (s *Server) handlePatchCollisions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.PatchStore.Get()
	if !ok {
		writeJSON(w, http.StatusOK, []patch.Finding{})
		return
	}
	writeJSON(w, http.StatusOK, patch.DetectCollisions(p))
}

// --- reconcile: building patch.DiscoveredDevice from the live registry ---

// buildDiscoveredDevices adapts every currently-discovered RDM device into
// the RDM-package-free shape internal/patch's matcher consumes. Address
// resolution reuses resolveWalkAddresses (walk.go) — the exact same
// cached-first, concurrent-fan-out, "NACK/timeout resolves as unknown
// rather than guessing 0" policy Rig Walk already relies on, so the Patch
// screen's reconcile view and Rig Walk never disagree about a device's live
// address.
func (s *Server) buildDiscoveredDevices() []patch.DiscoveredDevice {
	fixtures := s.Registry.Devices()
	addrs := s.resolveWalkAddresses(fixtures)
	out := make([]patch.DiscoveredDevice, 0, len(fixtures))
	for _, f := range fixtures {
		a := addrs[f.UID.String()]
		out = append(out, patch.DiscoveredDevice{
			UID: f.UID.String(), Universe: f.Port.RawValue(),
			StartAddress: a.addr, AddressKnown: a.known,
			Footprint: f.DMXFootprint, FootprintKnown: f.HasDeviceInfo,
			Manufacturer: effectiveManufacturer(f), Model: effectiveModel(f),
		})
	}
	return out
}

func (s *Server) handlePatchReconcile(w http.ResponseWriter, r *http.Request) {
	p, _ := s.PatchStore.Get() // ok=false -> zero-value Patch, zero entries: still a valid (empty) reconcile against whatever's discovered
	rep := patch.Reconcile(p.Entries, s.buildDiscoveredDevices())
	writeJSON(w, http.StatusOK, rep)
}

// --- reconcile actions: confirm / reject / one-tap fix --------------------

type reconcileConfirmRequest struct {
	DeviceUID string `json:"deviceUid"`
}

func (s *Server) handlePatchReconcileConfirm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req reconcileConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.DeviceUID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("deviceUid is required"))
		return
	}
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries[idx].ConfirmedUID = req.DeviceUID
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

func (s *Server) handlePatchReconcileReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries[idx].ConfirmedUID = ""
		pp.Entries[idx].MatchState = patch.MatchStateRejected
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

// applyOneTapFix issues SET DMX_START_ADDRESS(addr) against deviceUIDStr,
// reusing internal/params rather than duplicating any RDM logic (task ask,
// item 3).
func (s *Server) applyOneTapFix(ctx context.Context, deviceUIDStr string, addr uint16) error {
	uid, ok := rdm.ParseUID(deviceUIDStr)
	if !ok {
		return fmt.Errorf("bad device uid %q", deviceUIDStr)
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		return fmt.Errorf("device %s not currently reachable", deviceUIDStr)
	}
	client := params.New(s.RDM, node, uid)
	return client.SetDMXStartAddress(ctx, addr)
}

// handlePatchReconcileFix is the one-tap fix (task ask, item 3): SET
// DMX_START_ADDRESS on the given device to match the entry's patched
// address, then persist the pairing as confirmed — a decisive, explicit
// action (the client only calls this after Apply-to-confirm on the Patch
// screen), so treating it as an implicit confirm matches "confirmed
// pairings persist ... never re-litigated" rather than requiring a second
// separate confirm click for what was already a deliberate fix.
func (s *Server) handlePatchReconcileFix(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req reconcileConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, patch.ErrNoPatch)
		return
	}
	idx := p.IndexOf(id)
	if idx < 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown patch entry %q", id))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := s.applyOneTapFix(ctx, req.DeviceUID, p.Entries[idx].StartAddress); err != nil {
		writeParamError(w, err)
		return
	}
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx2 := pp.IndexOf(id)
		if idx2 < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries[idx2].ConfirmedUID = req.DeviceUID
		pp.Entries[idx2].MatchState = patch.MatchStateConfirmed
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

// --- reconcile actions: bulk fix -------------------------------------------

type fixAllRequest struct {
	Confirm bool `json:"confirm"`
}

type fixAllItemJSON struct {
	EntryID   string `json:"entryId"`
	EntryName string `json:"entryName"`
	DeviceUID string `json:"deviceUid"`
	From      uint16 `json:"from"`
	To        uint16 `json:"to"`
	Error     string `json:"error,omitempty"`
}

type fixAllResponse struct {
	// NeedsConfirm is true when this call was a preview (Confirm=false in
	// the request) — task ask: "must show exactly what will change and
	// require one explicit confirmation listing the count; never fire
	// silently". Nothing is applied when NeedsConfirm is true.
	NeedsConfirm bool             `json:"needsConfirm"`
	Count        int              `json:"count"`
	Items        []fixAllItemJSON `json:"items"`
	Applied      int              `json:"applied,omitempty"`
}

// handlePatchReconcileFixAll previews (Confirm=false) or applies
// (Confirm=true) SET DMX_START_ADDRESS for every currently-AddressMismatch
// entry with a resolvable device — task ask, item 3's "bulk fix" (with its
// own explicit-confirmation requirement, independent of the client's
// separate Apply-to-confirm UI step).
func (s *Server) handlePatchReconcileFixAll(w http.ResponseWriter, r *http.Request) {
	var req fixAllRequest
	// Body is optional (a bare preview GET-like POST with no body means
	// Confirm=false) — decodeJSON's DisallowUnknownFields still applies to
	// whatever body IS sent, so a typo'd field name is still caught.
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	p, _ := s.PatchStore.Get()
	devices := s.buildDiscoveredDevices()
	deviceAddr := make(map[string]uint16, len(devices))
	for _, d := range devices {
		deviceAddr[d.UID] = d.StartAddress
	}
	rep := patch.Reconcile(p.Entries, devices)

	var items []fixAllItemJSON
	for _, row := range rep.Rows {
		if row.Status != patch.StatusAddressMismatch || row.EntryID == "" || row.DeviceUID == "" {
			continue
		}
		idx := p.IndexOf(row.EntryID)
		if idx < 0 {
			continue
		}
		e := p.Entries[idx]
		items = append(items, fixAllItemJSON{
			EntryID: e.ID, EntryName: patch.EntryLabel(e), DeviceUID: row.DeviceUID,
			From: deviceAddr[row.DeviceUID], To: e.StartAddress,
		})
	}

	if !req.Confirm {
		writeJSON(w, http.StatusOK, fixAllResponse{NeedsConfirm: true, Count: len(items), Items: items})
		return
	}

	type outcome struct {
		entryID, deviceUID string
		err                error
	}
	outcomes := make([]outcome, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		i, it := i, it
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), deviceParamTimeout)
			defer cancel()
			outcomes[i] = outcome{entryID: it.EntryID, deviceUID: it.DeviceUID, err: s.applyOneTapFix(ctx, it.DeviceUID, it.To)}
		}()
	}
	wg.Wait()

	applied := 0
	succeeded := map[string]string{} // entryID -> deviceUID
	for i, oc := range outcomes {
		if oc.err == nil {
			applied++
			succeeded[oc.entryID] = oc.deviceUID
		} else {
			items[i].Error = oc.err.Error()
		}
	}
	if len(succeeded) > 0 {
		_, _ = s.PatchStore.Mutate(func(pp *patch.Patch) error {
			for id, uid := range succeeded {
				if idx := pp.IndexOf(id); idx >= 0 {
					pp.Entries[idx].ConfirmedUID = uid
					pp.Entries[idx].MatchState = patch.MatchStateConfirmed
				}
			}
			return nil
		})
	}
	writeJSON(w, http.StatusOK, fixAllResponse{NeedsConfirm: false, Count: len(items), Items: items, Applied: applied})
}

// --- adopt from discovered --------------------------------------------------

type adoptRequest struct {
	Mode string `json:"mode"` // "merge" | "fresh"
}

// handlePatchAdopt builds patch entries directly from the currently
// discovered rig (task ask, item 3: "the owner's fastest path to a working
// patch now that CSV is deferred"). "fresh" discards any existing patch;
// "merge" updates the live-derived fields (universe/address/footprint) of
// any existing entry already confirmed-paired to that UID, leaving every
// user-edited field (Name/Position/FixtureNumber/Notes/FixtureType)
// untouched, and appends a new entry for any device with no existing
// confirmed pairing.
func (s *Server) handlePatchAdopt(w http.ResponseWriter, r *http.Request) {
	var req adoptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Mode != "merge" && req.Mode != "fresh" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be merge or fresh, got %q", req.Mode))
		return
	}

	fixtures := s.Registry.Devices()
	addrs := s.resolveWalkAddresses(fixtures)
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].UID.Less(fixtures[j].UID) })

	adopted := make([]patch.Entry, 0, len(fixtures))
	for _, f := range fixtures {
		a := addrs[f.UID.String()]
		mfr, model := effectiveManufacturer(f), effectiveModel(f)
		name := model
		if name == "" || name == "—" {
			name = mfr
		}
		fixtureType := strings.TrimSpace(mfr + " " + model)
		adopted = append(adopted, patch.Entry{
			ID: patch.NewEntryID(), Name: name, FixtureType: fixtureType,
			Footprint: f.DMXFootprint, Universe: f.Port.RawValue(),
			StartAddress: a.addr, ConfirmedUID: f.UID.String(), MatchState: patch.MatchStateConfirmed,
		})
	}

	if req.Mode == "fresh" {
		result := s.PatchStore.Replace(patch.Patch{Name: "Adopted from discovered rig", Entries: adopted})
		writeJSON(w, http.StatusOK, toPatchResponse(result))
		return
	}

	s.PatchStore.EnsureActive()
	result, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		for _, ne := range adopted {
			found := -1
			for i, e := range pp.Entries {
				if e.ConfirmedUID != "" && e.ConfirmedUID == ne.ConfirmedUID {
					found = i
					break
				}
			}
			if found >= 0 {
				pp.Entries[found].Universe = ne.Universe
				pp.Entries[found].StartAddress = ne.StartAddress
				pp.Entries[found].Footprint = ne.Footprint
				pp.Entries[found].MatchState = patch.MatchStateConfirmed
				continue
			}
			pp.Entries = append(pp.Entries, ne)
		}
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(result))
}

// --- export ----------------------------------------------------------------

func patchExportFormat(r *http.Request) (string, error) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "txt" {
		return "", fmt.Errorf("format must be json or txt, got %q", format)
	}
	return format, nil
}

type patchExportDoc struct {
	AppVersion  string          `json:"appVersion"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Patch       patch.Patch     `json:"patch"`
	Findings    []patch.Finding `json:"findings"`
}

// handlePatchExport serves the patch itself (JSON, plus a readable TXT
// listing including collision findings) — same Content-Disposition/
// timestamped-filename mechanics as GET /api/capture/export and
// GET /api/walk/export (task ask, item 5: "reuse the existing export
// mechanics").
func (s *Server) handlePatchExport(w http.ResponseWriter, r *http.Request) {
	format, err := patchExportFormat(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no patch to export"))
		return
	}
	now := time.Now()
	filename := fmt.Sprintf("benny512_patch_%s.%s", now.Format("20060102_150405"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	findings := patch.DetectCollisions(p)
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(patchExportDoc{AppVersion: AppVersion, GeneratedAt: now, Patch: p, Findings: findings})
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writePatchExportText(w, now, p, findings)
	}
}

func writePatchExportText(w io.Writer, at time.Time, p patch.Patch, findings []patch.Finding) {
	fmt.Fprintln(w, "Benny512 Patch Export")
	fmt.Fprintf(w, "App version: %s\n", AppVersion)
	fmt.Fprintf(w, "Generated:   %s\n", at.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "Patch:       %s (%d entries, schema v%d)\n", nonEmptyStr(p.Name, "(unnamed)"), len(p.Entries), p.SchemaVersion)
	fmt.Fprintln(w, strings.Repeat("=", 78))
	fmt.Fprintln(w)

	if len(findings) > 0 {
		fmt.Fprintf(w, "%d collision finding(s):\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(w, "  [%s] %s: %s\n", strings.ToUpper(string(f.Severity)), f.Kind, f.Message)
		}
		fmt.Fprintln(w)
	}

	for i, e := range p.Entries {
		fmt.Fprintf(w, "%3d. %s\n", i+1, patch.EntryLabel(e))
		fmt.Fprintf(w, "     type: %s | mode: %s\n", nonEmptyStr(e.FixtureType, "—"), nonEmptyStr(e.Mode, "—"))
		fmt.Fprintf(w, "     universe %d, %s\n", e.Universe, formatEntryAddressRange(e))
		if e.Position != "" {
			fmt.Fprintf(w, "     position: %s\n", e.Position)
		}
		if e.FixtureNumber != "" {
			fmt.Fprintf(w, "     fixture number: %s\n", e.FixtureNumber)
		}
		if e.ConfirmedUID != "" {
			fmt.Fprintf(w, "     confirmed RDM UID: %s (%s)\n", e.ConfirmedUID, e.MatchState)
		}
		if e.Notes != "" {
			fmt.Fprintf(w, "     notes: %s\n", e.Notes)
		}
		fmt.Fprintln(w)
	}
}

func formatEntryAddressRange(e patch.Entry) string {
	return "address " + fmtAddressRange(e.StartAddress, e.Footprint)
}

func fmtAddressRange(start, footprint uint16) string {
	if footprint == 0 {
		return fmt.Sprintf("%d", start)
	}
	end := int(start) + int(footprint) - 1
	if footprint == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// handlePatchReconcileExport serves the reconcile diff as a readable TXT
// report (plus JSON) — task ask, item 5: "a readable TXT report of the
// reconcile diff".
func (s *Server) handlePatchReconcileExport(w http.ResponseWriter, r *http.Request) {
	format, err := patchExportFormat(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, _ := s.PatchStore.Get()
	rep := patch.Reconcile(p.Entries, s.buildDiscoveredDevices())

	now := time.Now()
	filename := fmt.Sprintf("benny512_reconcile_%s.%s", now.Format("20060102_150405"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			AppVersion  string       `json:"appVersion"`
			GeneratedAt time.Time    `json:"generatedAt"`
			Report      patch.Report `json:"report"`
		}{AppVersion, now, rep})
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writeReconcileExportText(w, now, p, rep)
	}
}

func writeReconcileExportText(w io.Writer, at time.Time, p patch.Patch, rep patch.Report) {
	fmt.Fprintln(w, "Benny512 Patch <-> RDM Reconcile Export")
	fmt.Fprintf(w, "App version: %s\n", AppVersion)
	fmt.Fprintf(w, "Generated:   %s\n", at.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "Patch:       %s\n", nonEmptyStr(p.Name, "(unnamed)"))
	fmt.Fprintln(w, strings.Repeat("=", 78))

	groups := []struct {
		status patch.RowStatus
		title  string
	}{
		{patch.StatusAddressMismatch, "ADDRESS MISMATCH"},
		{patch.StatusAmbiguous, "AMBIGUOUS"},
		{patch.StatusMissing, "MISSING (patched, no device found)"},
		{patch.StatusUnpatched, "UNPATCHED (device found, not in patch)"},
		{patch.StatusMatched, "MATCHED"},
	}
	for _, g := range groups {
		var rows []patch.Row
		for _, row := range rep.Rows {
			if row.Status == g.status {
				rows = append(rows, row)
			}
		}
		fmt.Fprintf(w, "\n-- %s (%d) --\n", g.title, len(rows))
		if len(rows) == 0 {
			fmt.Fprintln(w, "  (none)")
			continue
		}
		for _, row := range rows {
			label := row.EntryID
			if row.EntryID != "" {
				if idx := p.IndexOf(row.EntryID); idx >= 0 {
					label = patch.EntryLabel(p.Entries[idx])
				}
			} else {
				label = "(unpatched device)"
			}
			fmt.Fprintf(w, "  %-30s entry=%s device=%s confidence=%.2f\n", label, nonEmptyStr(row.EntryID, "—"), nonEmptyStr(row.DeviceUID, "—"), row.Confidence)
			for _, ev := range row.Evidence {
				fmt.Fprintf(w, "      evidence: %s — %s\n", ev.Kind, ev.Detail)
			}
			for _, c := range row.Candidates {
				fmt.Fprintf(w, "      candidate: %s (confidence %.2f)\n", c.DeviceUID, c.Confidence)
			}
		}
	}
}

// --- rig check ---------------------------------------------------------

type rigCheckStateJSON struct {
	Running          bool   `json:"running"`
	Mode             string `json:"mode,omitempty"`
	Level            byte   `json:"level"`
	EntryIndex       int    `json:"entryIndex"`
	EntryCount       int    `json:"entryCount"`
	CurrentEntryID   string `json:"currentEntryId,omitempty"`
	CurrentEntryName string `json:"currentEntryName,omitempty"`
	ChannelOffset    int    `json:"channelOffset"`
	CurrentChannel   uint16 `json:"currentChannel,omitempty"`
}

func (s *Server) buildRigCheckStateJSON() rigCheckStateJSON {
	st := s.RigCheck.State()
	out := rigCheckStateJSON{
		Running: st.Running, Mode: string(st.Mode), Level: st.Level,
		EntryIndex: st.EntryIndex, EntryCount: len(st.EntryIDs),
		ChannelOffset: st.ChannelOffset, CurrentChannel: st.CurrentChannel,
	}
	if st.EntryIndex >= 0 && st.EntryIndex < len(st.EntryIDs) {
		out.CurrentEntryID = st.EntryIDs[st.EntryIndex]
		if p, ok := s.PatchStore.Get(); ok {
			if idx := p.IndexOf(out.CurrentEntryID); idx >= 0 {
				out.CurrentEntryName = patch.EntryLabel(p.Entries[idx])
			}
		}
	}
	return out
}

func (s *Server) handleGetRigCheckState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

type rigCheckStartRequest struct {
	// ScopeKind: "" / "all" | "universe" | "selection" (task ask, item 4:
	// "whole patch, one universe, or a selection").
	ScopeKind string   `json:"scopeKind"`
	Universe  uint16   `json:"universe"`
	EntryIDs  []string `json:"entryIds"`
	Mode      string   `json:"mode"`
	Level     byte     `json:"level"`
}

func (s *Server) rigCheckScopeEntries(p patch.Patch, kind string, universe uint16, entryIDs []string) ([]patch.Entry, error) {
	switch kind {
	case "", "all":
		return p.Entries, nil
	case "universe":
		var out []patch.Entry
		for _, e := range p.Entries {
			if e.Universe == universe {
				out = append(out, e)
			}
		}
		return out, nil
	case "selection":
		want := make(map[string]bool, len(entryIDs))
		for _, id := range entryIDs {
			want[id] = true
		}
		var out []patch.Entry
		for _, e := range p.Entries {
			if want[e.ID] {
				out = append(out, e)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("scopeKind must be all|universe|selection, got %q", kind)
	}
}

func (s *Server) handleRigCheckStart(w http.ResponseWriter, r *http.Request) {
	var req rigCheckStartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("no active patch"))
		return
	}
	entries, err := s.rigCheckScopeEntries(p, req.ScopeKind, req.Universe, req.EntryIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.RigCheck.Start(entries, patch.Mode(req.Mode), req.Level); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

func (s *Server) handleRigCheckStop(w http.ResponseWriter, r *http.Request) {
	s.RigCheck.Stop()
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

func (s *Server) handleRigCheckBlackout(w http.ResponseWriter, r *http.Request) {
	s.RigCheck.Blackout()
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

func (s *Server) handleRigCheckNext(w http.ResponseWriter, r *http.Request) {
	if err := s.RigCheck.Next(); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

func (s *Server) handleRigCheckPrevious(w http.ResponseWriter, r *http.Request) {
	if err := s.RigCheck.Previous(); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

type rigCheckJumpRequest struct {
	Index int `json:"index"`
}

func (s *Server) handleRigCheckJump(w http.ResponseWriter, r *http.Request) {
	var req rigCheckJumpRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.RigCheck.Jump(req.Index); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

type rigCheckModeRequest struct {
	Mode string `json:"mode"`
}

func (s *Server) handleRigCheckMode(w http.ResponseWriter, r *http.Request) {
	var req rigCheckModeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.RigCheck.SetMode(patch.Mode(req.Mode)); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

type rigCheckLevelRequest struct {
	Level byte `json:"level"`
}

func (s *Server) handleRigCheckLevel(w http.ResponseWriter, r *http.Request) {
	var req rigCheckLevelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.RigCheck.SetLevel(req.Level); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

type rigCheckChannelRequest struct {
	Delta int `json:"delta"`
}

func (s *Server) handleRigCheckChannel(w http.ResponseWriter, r *http.Request) {
	var req rigCheckChannelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.RigCheck.StepChannel(req.Delta); err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildRigCheckStateJSON())
}

func writeRigCheckError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, patch.ErrRigCheckNotRunning):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, patch.ErrRigCheckEmptyScope):
		writeError(w, http.StatusUnprocessableEntity, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}
