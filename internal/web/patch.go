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
//	GET    /api/patches                          -> patchCatalogResponse
//	POST   /api/patches                          <- newPatchRequest      -> patchResponse (new named show)
//	POST   /api/patches/{id}/load                -> patchResponse (switch active show)
//	POST   /api/patch/new                         <- newPatchRequest      -> patchResponse (discards any existing patch)
//	POST   /api/patch/entries                     <- entryRequest         -> patchResponse (creates one entry, lazy-inits the patch)
//	PUT    /api/patch/entries/{id}                <- entryRequest         -> patchResponse
//	DELETE /api/patch/entries/{id}                -> patchResponse
//	POST   /api/patch/reorder                     <- reorderRequest       -> patchResponse
//	GET    /api/patch/collisions                  -> []patch.Finding
//	GET    /api/patch/attributes?ids=e1,e2,...    -> patchAttributesResponse (function-aware Rig Check foundation, Task 4; ids omitted = whole active patch)
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
//	POST   /api/patch/rigcheck/pattern/start      <- patternStartRequest  -> patternStatusJSON (stage 2 test-pattern engine — see internal/patch/testpattern.go)
//	POST   /api/patch/rigcheck/pattern/adjust     <- patternAdjustRequest -> patternStatusJSON
//	POST   /api/patch/rigcheck/pattern/tests      <- patternTestsRequest   -> patternStatusJSON (whole-selection apply)
//	POST   /api/patch/rigcheck/pattern/select     <- patternSelectRequest  -> patternStatusJSON (toggle ONE test)
//	POST   /api/patch/rigcheck/pattern/scope      <- patternScopeRequest   -> patternStatusJSON
//	POST   /api/patch/rigcheck/pattern/isolate    <- patternIsolateRequest -> patternStatusJSON
//	POST   /api/patch/rigcheck/pattern/output     <- patternOutputRequest  -> patternStatusJSON (start/stop output only)
//	GET    /api/patch/rigcheck/pattern            -> patternStatusJSON (also the pattern's client-liveness watchdog heartbeat — see its section below)
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
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

// patchCatalogResponse is intentionally only a compact index. The complete
// entries of a saved show are loaded only when that show becomes active.
type patchCatalogResponse struct {
	Patches []patch.PatchRef `json:"patches"`
}

func (s *Server) handleListPatches(w http.ResponseWriter, r *http.Request) {
	refs := s.PatchStore.ListPatches()
	if refs == nil {
		refs = make([]patch.PatchRef, 0)
	}
	writeJSON(w, http.StatusOK, patchCatalogResponse{Patches: refs})
}

type newPatchRequest struct {
	Name string `json:"name"`
}

// handleCreateSavedPatch starts a separate empty rig and makes it active.
// Stop is mandatory before the switch: neither a classic sequence nor a
// pattern may keep driving entries that belong to the previous show.
func (s *Server) handleCreateSavedPatch(w http.ResponseWriter, r *http.Request) {
	var req newPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.RigCheck.Stop()
	_, p, err := s.PatchStore.CreatePatch(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(p))
}

// handleLoadSavedPatch switches the active rig without replacing or
// reconciling either show's entries. A fixture committed in another saved
// show is therefore available to be committed independently in this one.
func (s *Server) handleLoadSavedPatch(w http.ResponseWriter, r *http.Request) {
	s.RigCheck.Stop()
	p, err := s.PatchStore.LoadPatch(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(p))
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
	s.RigCheck.Stop()
	p, err := s.PatchStore.ReplaceChecked(patch.Patch{Name: name})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(p))
}

// entryRequest is both the create and update payload — task ask's field
// list verbatim (name, fixture type/model, mode, footprint, universe,
// start address, position, fixture number, notes) plus ChannelFunctions
// (function-aware Rig Check foundation, Task 1's "wire the import path"):
// the MVR/GDTF import flows (patchimport.go, mvrimport.js/patch.js's
// single-GDTF apply) are the only client callers that ever populate this;
// a plain manual add/edit form submits it empty/absent, and
// handleUpdatePatchEntry below preserves whatever the entry already had in
// that case (see its doc comment) — the same "never disturb existing
// resolved state on an unrelated field edit" rule ConfirmedUID/MatchState
// already follow.
type entryRequest struct {
	PhaseCount    *uint16 `json:"phaseCount"`
	Name          string  `json:"name"`
	FixtureType   string  `json:"fixtureType"`
	Mode          string  `json:"mode"`
	Footprint     uint16  `json:"footprint"`
	Universe      uint16  `json:"universe"`
	StartAddress  uint16  `json:"startAddress"`
	Position      string  `json:"position"`
	FixtureNumber string  `json:"fixtureNumber"`
	Notes         string  `json:"notes"`

	ChannelFunctions map[string]channelFunctionRequest `json:"channelFunctions"`
}

// channelFunctionRequest is entryRequest.ChannelFunctions' value shape,
// keyed by DMX offset as a decimal string (JSON object keys are always
// strings — see entryFromRequest's offset-parsing loop for where that gets
// turned back into a uint16 matching patch.Entry.ChannelFunctions' key
// type). Field-for-field mirrors patch.ChannelFunction; see that struct's
// doc comment (internal/patch/entry.go) for what each field means.
type channelFunctionRequest struct {
	GeometryInstance string              `json:"geometryInstance"`
	Source           string              `json:"source"`
	Attribute        string              `json:"attribute"`
	FunctionName     string              `json:"functionName"`
	DMXFrom          uint32              `json:"dmxFrom"`
	DMXTo            uint32              `json:"dmxTo"`
	PhysicalFrom     float64             `json:"physicalFrom"`
	PhysicalTo       float64             `json:"physicalTo"`
	ChannelSets      []channelSetRequest `json:"channelSets"`

	// --- GDTF resting values (patch schema v3) ---------------------------
	//
	// These six mirror patch.ChannelFunction's HasDefault/Default/
	// DefaultByteCount and HasHighlight/Highlight/HighlightByteCount — the
	// values gdtfparse.js reads out of <ChannelFunction Default="X/Y">.
	// They are what the Rig Check base state (internal/patch/testpattern.go's
	// "GDTF defaults + open only when needed" section) needs in order to
	// leave an untested channel at the value its fixture actually rests at
	// instead of driving it to 0.
	//
	// Deliberately NO `omitempty` on any of them, and Has* is NOT redundant
	// with a non-zero value: a Default of 0 is the single most common real
	// value in the wild (a dimmer resting dark, a shutter resting closed),
	// so only Has* distinguishes "the file said 0" from "the file said
	// nothing". This is the same rule Entry.Universe/Entry.Footprint carry
	// and the same one patch.ChannelFunction's own doc comment states.
	HasDefault         bool   `json:"hasDefault"`
	Default            uint32 `json:"default"`
	DefaultByteCount   uint16 `json:"defaultByteCount"`
	HasHighlight       bool   `json:"hasHighlight"`
	Highlight          uint32 `json:"highlight"`
	HighlightByteCount uint16 `json:"highlightByteCount"`

	RDMSlotType  string `json:"rdmSlotType"`
	RDMSlotLabel string `json:"rdmSlotLabel"`
}

type channelSetRequest struct {
	Name         string  `json:"name"`
	DMXFrom      uint32  `json:"dmxFrom"`
	PhysicalFrom float64 `json:"physicalFrom"`
	PhysicalTo   float64 `json:"physicalTo"`
}

// errBadChannelFunctionSource is returned by entryFromRequest when a
// request's channelFunctions carries a Source value other than the two
// this package ever legitimately produces. Decision (3)'s hard constraint
// — an RDM-inferred mapping must never be able to pass for a GDTF one — is
// enforced at the model layer (patch.ChannelFunctionSource is a closed,
// documented set) but this is the boundary where an arbitrary client-
// supplied string would otherwise be able to forge either label; rejecting
// anything else outright (rather than silently coercing it to one of the
// two, or to absent) means a client-side bug that mislabels provenance
// fails loudly as a 400, not silently as bad data on disk.
var errBadChannelFunctionSource = fmt.Errorf("channelFunctions source must be %q or %q", patch.SourceGDTF, patch.SourceRDMInferred)

// channelFunctionsFromRequest converts entryRequest's wire shape into
// patch.Entry.ChannelFunctions, or an error if any offset key isn't a valid
// uint16 or any Source isn't one of the two real values (see
// errBadChannelFunctionSource). A nil/empty input converts to a non-nil
// empty map — entry.go's own normalizeChannelFunctions would catch a nil
// one anyway, but building it non-nil here means every other function in
// this file can treat the map as always-present without a nil check.
func channelFunctionsFromRequest(in map[string]channelFunctionRequest) (map[uint16]patch.ChannelFunction, error) {
	out := make(map[uint16]patch.ChannelFunction, len(in))
	for offsetStr, cfr := range in {
		offset, err := strconv.ParseUint(offsetStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("channelFunctions key %q is not a valid DMX offset: %w", offsetStr, err)
		}
		source := patch.ChannelFunctionSource(cfr.Source)
		if source != patch.SourceGDTF && source != patch.SourceRDMInferred {
			return nil, errBadChannelFunctionSource
		}
		sets := make([]patch.ChannelSet, 0, len(cfr.ChannelSets))
		for _, cs := range cfr.ChannelSets {
			sets = append(sets, patch.ChannelSet{
				Name: cs.Name, DMXFrom: cs.DMXFrom, PhysicalFrom: cs.PhysicalFrom, PhysicalTo: cs.PhysicalTo,
			})
		}
		out[uint16(offset)] = patch.ChannelFunction{
			GeometryInstance: cfr.GeometryInstance,
			Source:           source, Attribute: cfr.Attribute, FunctionName: cfr.FunctionName,
			DMXFrom: cfr.DMXFrom, DMXTo: cfr.DMXTo, PhysicalFrom: cfr.PhysicalFrom, PhysicalTo: cfr.PhysicalTo,
			ChannelSets: sets,
			// Copied verbatim, with no "is it non-zero?" filtering: a
			// present-but-zero Default is real data (see
			// channelFunctionRequest's doc comment), so HasDefault/
			// HasHighlight are the ONLY signals that decide whether the
			// value is meaningful — this layer must never second-guess them.
			HasDefault: cfr.HasDefault, Default: cfr.Default, DefaultByteCount: cfr.DefaultByteCount,
			HasHighlight: cfr.HasHighlight, Highlight: cfr.Highlight, HighlightByteCount: cfr.HighlightByteCount,
			RDMSlotType: cfr.RDMSlotType, RDMSlotLabel: cfr.RDMSlotLabel,
		}
	}
	return out, nil
}

func (s *Server) handleCreatePatchEntry(w http.ResponseWriter, r *http.Request) {
	var req entryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entry, err := entryFromRequest(patch.NewEntryID(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.PatchStore.EnsureActive()
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		pp.Entries = append(pp.Entries, entry)
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
	entry, err := entryFromRequest(id, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		confirmedUID, matchState := pp.Entries[idx].ConfirmedUID, pp.Entries[idx].MatchState
		// Same rule, extended to the schema-v4 commit model: entryRequest
		// has no `intended`/`asFound` fields at all (they are never edited
		// through this form — Intended changes only via an explicit
		// reconcile adopt, AsFound only via an explicit commit/re-read), so
		// entryFromRequest always produces them zero-valued. Dropping them
		// in here would mean that renaming a fixture silently decommitted
		// it and threw away every setting read off the real light.
		intended, asFound := pp.Entries[idx].Intended, pp.Entries[idx].AsFound
		if req.PhaseCount == nil {
			entry.PhaseCount = pp.Entries[idx].PhaseCount
		}
		// A plain field edit (fixing a typo, adjusting Notes) submits an
		// entryRequest with no channelFunctions at all — preserve whatever
		// this entry already had rather than wiping out (possibly
		// expensively resolved) GDTF/RDM-inferred data on an unrelated
		// edit. An explicit re-import (single-GDTF apply, patchimport.go's
		// merge path) always sends a non-empty channelFunctions and DOES
		// mean to replace it — same "only an explicit action changes
		// resolved state" rule ConfirmedUID/MatchState already follow
		// below.
		if len(req.ChannelFunctions) == 0 {
			entry.ChannelFunctions = pp.Entries[idx].ChannelFunctions
		}
		pp.Entries[idx] = entry
		// A plain field edit (fixing a typo, adjusting Notes) must not
		// silently discard a confirmed RDM pairing — only an explicit
		// reconcile confirm/reject/fix action changes ConfirmedUID/
		// MatchState (task ask: "confirmed pairings ... never
		// re-litigated").
		pp.Entries[idx].ConfirmedUID = confirmedUID
		pp.Entries[idx].MatchState = matchState
		pp.Entries[idx].Intended = intended
		pp.Entries[idx].AsFound = asFound
		return nil
	})
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPatchResponse(updated))
}

func entryFromRequest(id string, req entryRequest) (patch.Entry, error) {
	var phaseCount uint16
	if req.PhaseCount != nil {
		phaseCount = *req.PhaseCount
	}
	if phaseCount > 512 {
		return patch.Entry{}, fmt.Errorf("phase count must be 0 (auto) or 1–512")
	}
	cf, err := channelFunctionsFromRequest(req.ChannelFunctions)
	if err != nil {
		return patch.Entry{}, err
	}
	return patch.Entry{
		PhaseCount: phaseCount,
		ID:         id, Name: req.Name, FixtureType: req.FixtureType, Mode: req.Mode,
		Footprint: req.Footprint, Universe: req.Universe, StartAddress: req.StartAddress,
		Position: req.Position, FixtureNumber: req.FixtureNumber, Notes: req.Notes,
		ChannelFunctions: cf,
	}, nil
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
	// Applied deliberately has NO `omitempty`: 0 applied (every SET
	// attempt errored) is real, meaningful data on a Confirm=true call,
	// not an absent value — patch.js's onFixAll reads `result.applied`
	// directly (no `|| 0` guard) to render "fixed N of M", so an omitted
	// key here rendered as "fixed undefined of M" whenever every fix
	// failed. Zero-valued on every Confirm=false preview response too,
	// which is fine — NeedsConfirm:true already tells the client nothing
	// was applied, independent of this field's value.
	Applied int `json:"applied"`
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

	// Same defect class as internal/patch/collision.go's DetectCollisions
	// (see its comment): items is serialized as fixAllResponse.Items, whose
	// json tag has no `omitempty`, so a nil slice here would round-trip as
	// JSON `null` on the zero-mismatches preview instead of `[]`.
	items := make([]fixAllItemJSON, 0)
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
		result, err := s.PatchStore.ReplaceChecked(patch.Patch{Name: "Adopted from discovered rig", Entries: adopted})
		if err != nil {
			writePatchStoreError(w, err)
			return
		}
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
	artnetStart := s.SettingsSnapshot().ArtnetStartUniverse
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		// JSON is canonical, wire-format data (like every other API
		// response) — Universe values here stay the 0-based Art-Net
		// Port-Address regardless of the display-base setting; a consumer
		// re-importing this file must get back the same numbers it would
		// from GET /api/patch. Display conversion is a TXT-export-only
		// concern (see writePatchExportText/composeFindingText below).
		_ = enc.Encode(patchExportDoc{AppVersion: AppVersion, GeneratedAt: now, Patch: p, Findings: findings})
	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writePatchExportText(w, now, p, findings, artnetStart)
	}
}

// displayUniverse converts a canonical Art-Net Port-Address into the USER
// UNIVERSE a tech reads on the Patch screen, given
// Settings.ArtnetStartUniverse — the same formula as ui.js's artnetToUser.
//
// The patch TXT export is an operator-facing document (it is the printed
// patch sheet), so it uses the operator's numbering, exactly as the Patch
// screen does. It has no browser to convert in, which makes this the one
// place in internal/web that legitimately reimplements the conversion: the
// export IS the presentation boundary for a downloaded file, the same way
// ui.js is for the live DOM. Every other Go-side use of Entry.Universe /
// Finding.Universe must stay canonical (see collision.go's Finding.Message
// doc comment).
//
// A universe below the starting universe has no user number. It returns
// ok=false rather than a negative, and callers print the raw Art-Net value
// marked as outside the show's range — the same honesty rule as the UI's
// OUTSIDE_SHOW: an operator seeing "universe -94" would read a bug, not a
// fixture patched outside the block they were given.
func displayUniverse(raw uint16, artnetStart int) (int, bool) {
	u := int(raw) - artnetStart + 1
	if u < 1 {
		return 0, false
	}
	return u, true
}

// composeFindingText renders f as the one-line sentence a human reads,
// using patch.Patch p to resolve entry names and converting any universe
// number via displayUniverse — the TXT-export mirror of patch.js's
// renderCollisionBanner composer, so the collision banner on screen and
// this export always state the same universe number for the same finding.
// Every FindingKind DetectCollisions can produce today is handled
// explicitly; an unrecognized future Kind falls back to f.Message, which by
// contract (see Finding.Message's doc comment) never states a bare universe
// number, so that fallback can never be wrong here either.
func composeFindingText(p patch.Patch, f patch.Finding, artnetStart int) string {
	switch f.Kind {
	case patch.KindOverlap:
		labels := make([]string, 0, len(f.EntryIDs))
		for _, id := range f.EntryIDs {
			if idx := p.IndexOf(id); idx >= 0 {
				labels = append(labels, patch.EntryLabel(p.Entries[idx]))
			} else {
				labels = append(labels, id)
			}
		}
		who := ""
		for i, l := range labels {
			if i > 0 {
				who += " and "
			}
			who += fmt.Sprintf("%q", l)
		}
		if u, ok := displayUniverse(f.Universe, artnetStart); ok {
			return fmt.Sprintf("channels %d-%d overlap between %s in universe %d",
				f.ChannelStart, f.ChannelEnd, who, u)
		}
		return fmt.Sprintf("channels %d-%d overlap between %s in Art-Net universe %d (outside show range)",
			f.ChannelStart, f.ChannelEnd, who, f.Universe)
	default:
		return f.Message
	}
}

func writePatchExportText(w io.Writer, at time.Time, p patch.Patch, findings []patch.Finding, artnetStart int) {
	fmt.Fprintln(w, "Benny512 Patch Export")
	fmt.Fprintf(w, "App version: %s\n", AppVersion)
	fmt.Fprintf(w, "Generated:   %s\n", at.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "Patch:       %s (%d entries, schema v%d)\n", nonEmptyStr(p.Name, "(unnamed)"), len(p.Entries), p.SchemaVersion)
	fmt.Fprintln(w, strings.Repeat("=", 78))
	fmt.Fprintln(w)

	if len(findings) > 0 {
		fmt.Fprintf(w, "%d collision finding(s):\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(w, "  [%s] %s: %s\n", strings.ToUpper(string(f.Severity)), f.Kind, composeFindingText(p, f, artnetStart))
		}
		fmt.Fprintln(w)
	}

	for i, e := range p.Entries {
		fmt.Fprintf(w, "%3d. %s\n", i+1, patch.EntryLabel(e))
		fmt.Fprintf(w, "     type: %s | mode: %s\n", nonEmptyStr(e.FixtureType, "—"), nonEmptyStr(e.Mode, "—"))
		if u, ok := displayUniverse(e.Universe, artnetStart); ok {
			fmt.Fprintf(w, "     universe %d, %s\n", u, formatEntryAddressRange(e))
		} else {
			fmt.Fprintf(w, "     Art-Net universe %d (outside show range), %s\n", e.Universe, formatEntryAddressRange(e))
		}
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
	// ScopeKind: "" / "all" | "universe" | "position" | "selection" (task
	// ask, item 4: "whole patch, one universe, or a selection"; "position"
	// added for stage 2's test-pattern engine — task brief: "whole rig /
	// one universe / one position / an explicit set of fixtures" — and
	// shared here since it's equally meaningful for the classic
	// channel-level walk).
	ScopeKind   string   `json:"scopeKind"`
	Universe    uint16   `json:"universe"`
	Position    string   `json:"position"`
	EntryIDs    []string `json:"entryIds"`
	FixtureType string   `json:"fixtureType"`
	Mode        string   `json:"mode"`
	Level       byte     `json:"level"`
}

// rigCheckScopeEntries resolves ScopeKind into the ordered []patch.Entry
// both the classic walk (handleRigCheckStart) and the stage 2 pattern
// engine (handleRigCheckPatternStart) drive over — one scope-resolution
// implementation so the two surfaces' "whole rig / one universe / one
// position / a selection" options can never quietly diverge in meaning.
func (s *Server) rigCheckScopeEntries(p patch.Patch, kind string, universe uint16, position string, entryIDs []string, fixtureType ...string) ([]patch.Entry, error) {
	var out []patch.Entry
	switch kind {
	case "", "all":
		out = append([]patch.Entry(nil), p.Entries...)
	case "universe":
		for _, e := range p.Entries {
			if e.Universe == universe {
				out = append(out, e)
			}
		}
	case "position":
		for _, e := range p.Entries {
			if e.Position == position {
				out = append(out, e)
			}
		}
	case "selection":
		want := make(map[string]bool, len(entryIDs))
		for _, id := range entryIDs {
			want[id] = true
		}
		for _, e := range p.Entries {
			if want[e.ID] {
				out = append(out, e)
			}
		}
	case "fixtureType":
		want := ""
		if len(fixtureType) > 0 {
			want = strings.TrimSpace(fixtureType[0])
		}
		if want == "" {
			return nil, fmt.Errorf("fixtureType is required")
		}
		for _, e := range p.Entries {
			if strings.TrimSpace(e.FixtureType) == want {
				out = append(out, e)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("fixtureType %q does not exist", want)
		}
	default:
		return nil, fmt.Errorf("scopeKind must be all|universe|position|selection, got %q", kind)
	}
	return s.withRDMPhaseWeights(out), nil
}

// withRDMPhaseWeights adds runtime-only phase weights to entries committed
// to fixtures whose root DEVICE_INFO reports sub-devices. A 16-cell fixture
// therefore consumes 16 phase positions, while patch/reconcile counts and
// DMX channel mappings remain exactly one entry. Unknown/no-sub-device data
// deliberately stays at the normal single position.
func (s *Server) withRDMPhaseWeights(entries []patch.Entry) []patch.Entry {
	fixtures := s.Registry.Devices()
	byUID := make(map[string]uint16, len(fixtures))
	for _, f := range fixtures {
		if f.HasDeviceInfo && f.SubDeviceCount > 0 {
			byUID[f.UID.String()] = f.SubDeviceCount
		}
	}
	for i := range entries {
		entries[i].PhaseWeight, _ = patch.PhaseCountFor(entries[i], byUID[entries[i].ConfirmedUID])
	}
	return entries
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
	entries, err := s.rigCheckScopeEntries(p, req.ScopeKind, req.Universe, req.Position, req.EntryIDs)
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
	case errors.Is(err, patch.ErrRigCheckNotRunning), errors.Is(err, patch.ErrRigCheckPatternRunning), errors.Is(err, patch.ErrRigCheckNoPatternRunning),
		// ErrRigCheckAmbiguousTest is a state conflict, not a malformed
		// request: .../pattern/adjust's single-selected-test parameter
		// replace has no single test to mean when several are selected.
		// Its own doc comment above already promised a 409 here.
		errors.Is(err, patch.ErrRigCheckAmbiguousTest):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, patch.ErrRigCheckEmptyScope):
		writeError(w, http.StatusUnprocessableEntity, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}

// --- rig check: stage 2 attribute-level test-pattern engine ---------------
//
// Endpoints (see internal/patch/testpattern.go for the engine itself, and its
// package doc comment for every design decision behind the contract below):
//
//	POST /api/patch/rigcheck/pattern/tests   <- patternTestsRequest   -> patternStatusJSON
//	POST /api/patch/rigcheck/pattern/select  <- patternSelectRequest  -> patternStatusJSON
//	POST /api/patch/rigcheck/pattern/scope   <- patternScopeRequest   -> patternStatusJSON
//	POST /api/patch/rigcheck/pattern/isolate <- patternIsolateRequest -> patternStatusJSON
//	POST /api/patch/rigcheck/pattern/output  <- patternOutputRequest  -> patternStatusJSON
//	POST /api/patch/rigcheck/pattern/start   <- patternStartRequest   -> patternStatusJSON  (legacy)
//	POST /api/patch/rigcheck/pattern/adjust  <- patternAdjustRequest  -> patternStatusJSON  (legacy)
//	GET  /api/patch/rigcheck/pattern         -> patternStatusJSON
//
// The five single-purpose POSTs are the current surface, one endpoint per
// engine mutator, and EVERY one of them — including the two legacy ones and
// the GET — answers with the FULL patternStatusJSON snapshot. That is not
// incidental: a client re-renders from the returned snapshot and never
// mutates its own copy of the state, which is what stops a selection change
// from failing to show up until something else forces a refresh.
//
// The legacy pair remains routed and behaviourally unchanged because
// static/js/patch.js still calls it:
//
//	POST .../pattern/start   ==  POST .../pattern/tests  (+ output on unless
//	                             "outputEnabled": false), with a flat
//	                             single-test shorthand beside "tests"
//	POST .../pattern/adjust  ==  select / isolate / output multiplexed into
//	                             one request, plus the single-selected-test
//	                             parameter replace
//
// The engine holds TWO independent pieces of state and this surface mirrors
// that split exactly:
//
//   - .../pattern/start is the SELECTION apply. It sets the scope, the set of
//     selected tests, and the isolate flag, and — unless the caller sends
//     "outputEnabled": false — lets output flow. Sending it with
//     "outputEnabled": false is how a UI builds up a selection BEFORE
//     anything moves.
//   - .../pattern/adjust is the incremental mutator: toggle or
//     re-parameterise ONE test ("test" + "enabled"), and/or flip output
//     ("outputEnabled"), and/or flip isolate ("isolate"). Every one of those
//     works identically whether or not output is currently flowing — the
//     start button only allows output to flow, it does not limit
//     configuration.
//
// Stop/blackout are NOT separate endpoints: the existing
// POST /api/patch/rigcheck/stop and POST /api/patch/rigcheck/blackout apply
// equally here (patch.RigCheck.Stop/Blackout are pattern-aware) — one Stop
// button, one Blackout button, regardless of which engine is driving output.
// Both stop OUTPUT and leave the test selection completely intact, so a
// tech's picked tests survive a panic-button press and are still there to
// re-run.
//
// GET .../rigcheck/pattern is not just a read — see patternStatusJSON's
// lastEndReason field and testpattern.go's client-liveness-watchdog doc
// comment: every call to it refreshes the running pattern's liveness deadline
// exactly like the two POSTs do. The UI MUST poll this at an interval
// comfortably under patch.PatternWatchdogTimeout (5s) for as long as output
// is meant to keep flowing — stop polling (tab closed, navigated away,
// crashed) and output blacks out and ceases within that window with no
// further action from the UI required. The selection survives that too.

// patternTestRequest is one test in a selection. Kind is one of the
// patch.PatternKind string constants (testpattern.go); every other field is
// that kind's parameters, and a field the kind does not use is ignored.
// Target is what distinguishes the enumerated tests from each other (one
// frost test per GDTF Frost* function, one gobo test per wheel, one
// move_extreme per axis+extreme) and, with Kind, forms the test's id.
type patternTestRequest struct {
	Kind      string  `json:"kind"`
	RateHz    float64 `json:"rateHz"`
	Min       byte    `json:"min"`
	Max       byte    `json:"max"`
	Target    string  `json:"target"`
	Direction string  `json:"direction"`
	Value     byte    `json:"value"`
	On        bool    `json:"on"`
	// Waveform is "" (== "sine"), "sine" or "snap" — a square wave holding
	// at min for half the cycle and max for the other half. It applies to
	// every continuous kind, not just the dimmer, and is deliberately a
	// parameter rather than its own kind so it composes with everything.
	Waveform string `json:"waveform"`
	// OffsetMin/OffsetMax are the phase spread across the fixtures this test
	// drives, in DEGREES (GrandMA3's "phase"). Fixture i of n, ordered by
	// universe then start address, sits at
	// OffsetMin + (OffsetMax-OffsetMin)*i/n — divisor n, not n-1, so 0..360
	// across 8 fixtures puts the last at 315° and the chase wraps seamlessly.
	// 0/0 (the default) is everything in unison. Sending a non-zero value
	// for a STATIC kind (move_extreme, manual_value, dimmer_toggle) is a 400,
	// not a silent no-op.
	OffsetMin float64 `json:"offsetMin"`
	OffsetMax float64 `json:"offsetMax"`
}

func (t patternTestRequest) spec() patch.PatternSpec {
	return patch.PatternSpec{
		Kind: patch.PatternKind(t.Kind),
		Params: patch.PatternParams{
			RateHz: t.RateHz, Min: t.Min, Max: t.Max, Target: t.Target, Direction: t.Direction,
			Value: t.Value, On: t.On, Waveform: patch.Waveform(t.Waveform),
			OffsetMin: t.OffsetMin, OffsetMax: t.OffsetMax,
		},
	}
}

// patternStartRequest applies a whole selection. The scope vocabulary is
// identical to rigCheckStartRequest's.
//
// Tests is the current form. The flat Kind/RateHz/... fields beside it are
// the pre-stackable-tests single-test shorthand, still supported verbatim:
// when Tests is empty and Kind is set, the request means "select exactly this
// one test". When Tests is non-empty, the flat fields are ignored.
type patternStartRequest struct {
	ScopeKind string   `json:"scopeKind"`
	Universe  uint16   `json:"universe"`
	Position  string   `json:"position"`
	EntryIDs  []string `json:"entryIds"`

	Tests []patternTestRequest `json:"tests"`

	// --- single-test shorthand (see this struct's doc comment) ---
	Kind      string  `json:"kind"`
	RateHz    float64 `json:"rateHz"`
	Min       byte    `json:"min"`
	Max       byte    `json:"max"`
	Target    string  `json:"target"`
	Direction string  `json:"direction"`
	Value     byte    `json:"value"`
	On        bool    `json:"on"`
	Waveform  string  `json:"waveform"`
	OffsetMin float64 `json:"offsetMin"`
	OffsetMax float64 `json:"offsetMax"`

	// Isolate turns on the old zero-everything-else behaviour: no GDTF
	// defaults, no dimmer-up, no shutter-open, every channel not driven by a
	// selected test forced to 0. Default false. Useful for proving which
	// channel drives which function; useless for seeing light.
	Isolate bool `json:"isolate"`
	// OutputEnabled is a POINTER on purpose: omitted (nil) means "yes, start
	// output", which is what every pre-existing caller of this endpoint
	// means by hitting it. An explicit false selects the tests and leaves
	// the rig dark, which is the "pick your tests first" half of the owner's
	// workflow.
	OutputEnabled *bool `json:"outputEnabled"`
}

func (req patternStartRequest) specs() []patch.PatternSpec {
	if len(req.Tests) > 0 {
		out := make([]patch.PatternSpec, 0, len(req.Tests))
		for _, t := range req.Tests {
			out = append(out, t.spec())
		}
		return out
	}
	if req.Kind == "" {
		return make([]patch.PatternSpec, 0)
	}
	return []patch.PatternSpec{patternTestRequest{
		Kind: req.Kind, RateHz: req.RateHz, Min: req.Min, Max: req.Max, Target: req.Target,
		Direction: req.Direction, Value: req.Value, On: req.On, Waveform: req.Waveform,
		OffsetMin: req.OffsetMin, OffsetMax: req.OffsetMax,
	}.spec()}
}

// handleRigCheckPatternStart applies a selection and (by default) lets output
// flow — task ask: "starting a pattern that moves fixtures should be a
// deliberate action": this endpoint always requires an explicit POST naming
// both a scope and at least one test, never an implicit continuation of
// anything else.
func (s *Server) handleRigCheckPatternStart(w http.ResponseWriter, r *http.Request) {
	var req patternStartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("no active patch"))
		return
	}
	entries, err := s.rigCheckScopeEntries(p, req.ScopeKind, req.Universe, req.Position, req.EntryIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.RigCheck.SetPatternTests(entries, req.specs(), req.Isolate)
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	s.setPatternScope(patternScopeFields{ScopeKind: req.ScopeKind, Universe: req.Universe, Position: req.Position, EntryIDs: req.EntryIDs}, entries)
	if req.OutputEnabled == nil || *req.OutputEnabled {
		st, err = s.RigCheck.StartPatternOutput()
		if err != nil {
			writeRigCheckError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

// patternAdjustRequest is the incremental mutator — every field is optional
// and independent, and any combination is applied in the order listed below
// (isolate, then the test toggle, then the output flag) so one request can
// both pick a test and start output.
//
// Test names one test to enable/re-parameterise (or, with Enabled explicitly
// false, to deselect). Enabled is a POINTER so that omitting it means "yes,
// select it" — deselecting requires saying so.
//
// The flat RateHz/Min/Max/... fields beside it are the pre-stackable-tests
// shorthand: "replace the parameters of the one selected test", which is what
// the previous version of this endpoint did. They apply only when Test is
// absent, and 409 if zero or more than one test is selected (there is then no
// single test the call could mean).
type patternAdjustRequest struct {
	Test    *patternTestRequest `json:"test"`
	Enabled *bool               `json:"enabled"`

	OutputEnabled *bool `json:"outputEnabled"`
	Isolate       *bool `json:"isolate"`

	RateHz    float64 `json:"rateHz"`
	Min       byte    `json:"min"`
	Max       byte    `json:"max"`
	Target    string  `json:"target"`
	Direction string  `json:"direction"`
	Value     byte    `json:"value"`
	On        bool    `json:"on"`
	Waveform  string  `json:"waveform"`
	OffsetMin float64 `json:"offsetMin"`
	OffsetMax float64 `json:"offsetMax"`
}

func (s *Server) handleRigCheckPatternAdjust(w http.ResponseWriter, r *http.Request) {
	var req patternAdjustRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	st := s.RigCheck.PatternStatus()
	if req.Isolate != nil {
		st = s.RigCheck.SetPatternIsolate(*req.Isolate)
	}
	switch {
	case req.Test != nil:
		enabled := req.Enabled == nil || *req.Enabled
		var err error
		st, err = s.RigCheck.SelectPatternTest(req.Test.spec(), enabled)
		if err != nil {
			writeRigCheckError(w, err)
			return
		}
	case req.OutputEnabled == nil && req.Isolate == nil:
		// Pure single-test parameter replace (the legacy shape). Only taken
		// when the request carries nothing else at all, so an
		// {"outputEnabled":false} or {"isolate":true} request is never
		// misread as "and also blank the selected test's parameters".
		var err error
		st, err = s.RigCheck.AdjustPattern(patch.PatternParams{
			RateHz: req.RateHz, Min: req.Min, Max: req.Max, Target: req.Target, Direction: req.Direction,
			Value: req.Value, On: req.On, Waveform: patch.Waveform(req.Waveform),
			OffsetMin: req.OffsetMin, OffsetMax: req.OffsetMax,
		})
		if err != nil {
			writeRigCheckError(w, err)
			return
		}
	}
	if req.OutputEnabled != nil {
		if *req.OutputEnabled {
			var err error
			st, err = s.RigCheck.StartPatternOutput()
			if err != nil {
				writeRigCheckError(w, err)
				return
			}
		} else {
			st = s.RigCheck.StopPatternOutput()
		}
	}
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

// handleRigCheckPatternStatus is GET .../rigcheck/pattern — see this
// section's doc comment above for why this read is also the client-liveness
// watchdog's heartbeat.
func (s *Server) handleRigCheckPatternStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.patternStatusJSON(s.RigCheck.PatternStatus()))
}

// --- rig check pattern: one endpoint per engine mutator -------------------
//
// These five are the surface a new UI should drive. Each maps 1:1 onto one
// patch.RigCheck method, each is legal whether or not output is flowing
// (only .../output changes whether output flows at all), and each returns
// the same full patternStatusJSON snapshot the GET does.

// patternScopeFields is the scope-selection vocabulary, IDENTICAL to
// rigCheckStartRequest's and resolved by the same rigCheckScopeEntries — one
// vocabulary for both engines so they can never diverge in meaning.
//
//	scopeKind "" / "all"  -> every entry in the patch
//	scopeKind "universe"  -> every entry whose universe == `universe`
//	scopeKind "position"  -> every entry whose position == `position`
//	scopeKind "selection" -> the entries named in `entryIds`, in PATCH order
//
// Anything else is a 400. A scope that resolves to zero entries is a 422
// (patch.ErrRigCheckEmptyScope): the engine refuses to hold an empty scope
// rather than silently selecting nothing.
type patternScopeFields struct {
	ScopeKind   string   `json:"scopeKind"`
	Universe    uint16   `json:"universe"`
	Position    string   `json:"position"`
	EntryIDs    []string `json:"entryIds"`
	FixtureType string   `json:"fixtureType"`
}

// resolveScope turns the scope fields into the ordered entries the engine
// takes, or writes the appropriate error response and returns ok=false.
func (s *Server) resolveScope(w http.ResponseWriter, f patternScopeFields) ([]patch.Entry, bool) {
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("no active patch"))
		return nil, false
	}
	entries, err := s.rigCheckScopeEntries(p, f.ScopeKind, f.Universe, f.Position, f.EntryIDs, f.FixtureType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil, false
	}
	return entries, true
}

// patternTestsRequest is the whole-state selection apply — the primary call
// a UI makes. It replaces the scope, the ENTIRE set of selected tests, and
// the isolate flag in one atomic operation, and it never touches the output
// flag: applying a selection while output flows re-renders on the very next
// frame, and applying one with output off moves nothing.
//
// Sending "tests": [] is a legitimate request meaning "deselect everything",
// not a malformed one.
type patternTestsRequest struct {
	patternScopeFields
	Tests []patternTestRequest `json:"tests"`
	// Isolate is the zero-everything-else base state (no GDTF defaults, no
	// dimmer-up, no shutter-open). Absent means false, which is the normal
	// mode — this is a whole-state apply, so an omitted isolate really does
	// mean "isolate off", not "leave it as it was". Use .../pattern/isolate
	// to change only that flag.
	Isolate bool `json:"isolate"`
}

func (req patternTestsRequest) specs() []patch.PatternSpec {
	out := make([]patch.PatternSpec, 0, len(req.Tests))
	for _, t := range req.Tests {
		out = append(out, t.spec())
	}
	return out
}

func (s *Server) handleRigCheckPatternTests(w http.ResponseWriter, r *http.Request) {
	var req patternTestsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, ok := s.resolveScope(w, req.patternScopeFields)
	if !ok {
		return
	}
	st, err := s.RigCheck.SetPatternTests(entries, req.specs(), req.Isolate)
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	s.setPatternScope(req.patternScopeFields, entries)
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

// patternSelectRequest toggles ONE test on or off — what a single
// toggle-button press sends. `test` carries the full spec (kind + params);
// the test's identity is kind, or "kind:target" when the spec has a target,
// which is exactly the `id` the status's tests[]/available[] report, so a UI
// never has to invent one.
//
// Enabled is a POINTER: omitting it means "select it" (the common case),
// and DESELECTING requires an explicit "enabled": false on the wire. A
// client MUST serialize that false rather than dropping the key — an
// enabled flag that vanishes when it is false is the exact class of bug this
// codebase forbids `omitempty` for.
//
// Re-selecting an already-selected test replaces its parameters wholesale.
// Legal at any time; NEVER requires stopping output first.
type patternSelectRequest struct {
	Test    patternTestRequest `json:"test"`
	Enabled *bool              `json:"enabled"`
}

func (s *Server) handleRigCheckPatternSelect(w http.ResponseWriter, r *http.Request) {
	var req patternSelectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	enabled := req.Enabled == nil || *req.Enabled
	st, err := s.RigCheck.SelectPatternTest(req.Test.spec(), enabled)
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

// patternScopeRequest replaces the scope alone, keeping every selected test
// (each is re-resolved against the new fixtures). Legal while output flows.
type patternScopeRequest struct {
	patternScopeFields
}

func (s *Server) handleRigCheckPatternScope(w http.ResponseWriter, r *http.Request) {
	var req patternScopeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, ok := s.resolveScope(w, req.patternScopeFields)
	if !ok {
		return
	}
	st, err := s.RigCheck.SetPatternScope(entries)
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	s.setPatternScope(req.patternScopeFields, entries)
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

// patternIsolateRequest flips the isolate flag alone. Absent means false —
// this is a set, not a patch.
type patternIsolateRequest struct {
	Isolate bool `json:"isolate"`
}

func (s *Server) handleRigCheckPatternIsolate(w http.ResponseWriter, r *http.Request) {
	var req patternIsolateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.patternStatusJSON(s.RigCheck.SetPatternIsolate(req.Isolate)))
}

// patternOutputRequest is the start/stop button and NOTHING else:
// {"enabled": true} lets output flow for whatever is currently selected,
// {"enabled": false} blacks out (immediately, via SendNow — not on the next
// retransmit tick) and ceases output while leaving the selection COMPLETELY
// intact. The owner's rule, preserved here: the stop button stops output, it
// does not deselect any tests, and the start button only allows output to
// flow — it does not limit configuration.
//
// Enabled is a plain bool and an absent one therefore means false (stop):
// a client asking to start must say so.
//
// Starting with an empty scope is a 422; starting with an empty SELECTION is
// a legitimate 200 that renders only the base state.
type patternOutputRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleRigCheckPatternOutput(w http.ResponseWriter, r *http.Request) {
	var req patternOutputRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !req.Enabled {
		writeJSON(w, http.StatusOK, s.patternStatusJSON(s.RigCheck.StopPatternOutput()))
		return
	}
	st, err := s.RigCheck.StartPatternOutput()
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.patternStatusJSON(st))
}

type patternEntryStatusJSON struct {
	EntryID string `json:"entryId"`
	// Applied/Inferred/DetailMissing: see patch.PatternEntryStatus's doc
	// comment (testpattern.go) — mixed-rig counting and RDM-inferred
	// provenance at per-entry granularity. Deliberately no `omitempty` on
	// any of the three: false is real, meaningful data (an
	// un-applied/GDTF-sourced/fully-detailed entry), not an absent value.
	Applied       bool `json:"applied"`
	Inferred      bool `json:"inferred"`
	DetailMissing bool `json:"detailMissing"`
	// PhaseDegrees is where on the waveform's circle this fixture sits for
	// this test — 0 for every fixture when the test has no offset spread.
	// No `omitempty`: 0° is the overwhelmingly common REAL value.
	PhaseDegrees float64 `json:"phaseDegrees"`
}

// patternTestStatusJSON is one selected test. The `id` is the stable handle a
// UI uses to talk about this test (kind, or "kind:target"); it is also what
// contested[].tests names.
type patternTestStatusJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Group string `json:"group"`
	// Every parameter is echoed back at its CURRENT effective value —
	// including the per-kind default rateHz actually in effect when the
	// caller sent 0, and the normalized waveform ("sine" when the caller
	// sent ""). No `omitempty` anywhere: 0/""/false are all real, legitimate
	// configurations here, never absent data.
	RateHz             float64                  `json:"rateHz"`
	Min                byte                     `json:"min"`
	Max                byte                     `json:"max"`
	Target             string                   `json:"target"`
	Direction          string                   `json:"direction"`
	Value              byte                     `json:"value"`
	On                 bool                     `json:"on"`
	Waveform           string                   `json:"waveform"`
	OffsetMin          float64                  `json:"offsetMin"`
	OffsetMax          float64                  `json:"offsetMax"`
	TotalScope         int                      `json:"totalScope"`
	AppliedCount       int                      `json:"appliedCount"`
	SkippedCount       int                      `json:"skippedCount"`
	InferredCount      int                      `json:"inferredCount"`
	MissingDetailCount int                      `json:"missingDetailCount"`
	Entries            []patternEntryStatusJSON `json:"entries"`
}

// contestedOffsetJSON is one absolute DMX slot more than one active test
// wants. tests is in canonical composition order — the LAST one wins. A UI
// should warn on any non-empty list rather than letting one test silently
// corrupt another.
type contestedOffsetJSON struct {
	Universe uint16   `json:"universe"`
	Channel  uint16   `json:"channel"`
	EntryID  string   `json:"entryId"`
	Tests    []string `json:"tests"`
}

// patternBaseStateJSON reports what the "GDTF defaults + open only when
// needed" base state did, and — just as importantly — what it could not do.
type patternBaseStateJSON struct {
	Isolate bool `json:"isolate"`
	// DefaultsKnownCount/DefaultsUnknownCount count DMX slots across the
	// whole scope whose GDTF resting value the file did / did not state. An
	// unknown slot is LEFT AT 0: this engine does not invent a resting
	// value, and this count is how a UI learns how much of the rig it is
	// flying blind over.
	DefaultsKnownCount   int `json:"defaultsKnownCount"`
	DefaultsUnknownCount int `json:"defaultsUnknownCount"`
	DimmerDrivenCount    int `json:"dimmerDrivenCount"`
	ShutterOpenedCount   int `json:"shutterOpenedCount"`
	// ShutterUnknownEntries names every entry that HAS a shutter/strobe
	// function whose open position could not be established from GDTF
	// ChannelSets or a GDTF Default. Those channels are left at 0, the
	// fixture will probably not emit light, and this list is the engine
	// saying so plainly instead of guessing a value. Always an array, never
	// null.
	ShutterUnknownEntries []string `json:"shutterUnknownEntries"`
}

// availableTestJSON is one test this scope can actually run. label comes from
// GDTF's own ChannelFunction Name where the file gave one (labelFromGdtf
// true), else the GDTF attribute name — this server does not decide which
// frost is "light" and which is "heavy"; it reports what GDTF says.
type availableTestJSON struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Group         string `json:"group"`
	Target        string `json:"target"`
	Label         string `json:"label"`
	Attribute     string `json:"attribute"`
	FixtureCount  int    `json:"fixtureCount"`
	LabelFromGDTF bool   `json:"labelFromGdtf"`
}

// patternStatusJSON is GET .../rigcheck/pattern's (and both POSTs')
// response.
//
// The `tests`/`available`/`contested`/`baseState` block is the real,
// current shape. The scalar `kind`/`rateHz`/`min`/... fields above it are a
// COMPATIBILITY VIEW of the first test in canonical order (all zero when
// nothing is selected), kept so the pre-stackable-tests client keeps working
// unchanged; a new client should read `tests` and ignore them.
type patternStatusJSON struct {
	// Running and OutputEnabled are the same bit under two names: whether
	// the selected tests are currently being rendered to DMX. `running` is
	// the legacy name; `outputEnabled` says what it actually means now that
	// a test can be SELECTED without output flowing.
	Running       bool `json:"running"`
	OutputEnabled bool `json:"outputEnabled"`
	// SelectedCount is how many tests are selected — the number a UI needs
	// to render "3 tests selected" whether or not output is flowing.
	SelectedCount int `json:"selectedCount"`

	// --- compatibility view of the first selected test ---
	Kind               string                   `json:"kind,omitempty"`
	RateHz             float64                  `json:"rateHz"`
	Min                byte                     `json:"min"`
	Max                byte                     `json:"max"`
	Target             string                   `json:"target,omitempty"`
	Direction          string                   `json:"direction,omitempty"`
	Value              byte                     `json:"value"`
	On                 bool                     `json:"on"`
	Group              string                   `json:"group,omitempty"`
	AppliedCount       int                      `json:"appliedCount"`
	SkippedCount       int                      `json:"skippedCount"`
	InferredCount      int                      `json:"inferredCount"`
	MissingDetailCount int                      `json:"missingDetailCount"`
	Entries            []patternEntryStatusJSON `json:"entries"`

	ElapsedMS  int64 `json:"elapsedMs"`
	TotalScope int   `json:"totalScope"`

	// Scope is the server-resolved expression that selected the current
	// entries. Universe is canonical/0-based (as everywhere on the wire);
	// the browser formats it only at display time. EntryIDs is non-nil even
	// for every non-selection scope, so a client never has to distinguish an
	// empty valid selection from an absent field.
	ScopeKind        string                  `json:"scopeKind"`
	ScopeUniverse    uint16                  `json:"scopeUniverse"`
	ScopePosition    string                  `json:"scopePosition"`
	ScopeEntryIDs    []string                `json:"scopeEntryIds"`
	ScopeFixtureType string                  `json:"scopeFixtureType"`
	FixtureTypes     []fixtureTypeOptionJSON `json:"fixtureTypes"`

	Tests     []patternTestStatusJSON `json:"tests"`
	Contested []contestedOffsetJSON   `json:"contested"`
	BaseState patternBaseStateJSON    `json:"baseState"`
	Available []availableTestJSON     `json:"available"`

	// LastEndReason is why OUTPUT most recently stopped: "" (never enabled),
	// "manual", "restarted" or "watchdog" — sticky, so a UI polling in after
	// the fact can tell a deliberate Stop from an abandoned run the watchdog
	// caught.
	LastEndReason string `json:"lastEndReason,omitempty"`
}

func toPatternEntriesJSON(in []patch.PatternEntryStatus) []patternEntryStatusJSON {
	out := make([]patternEntryStatusJSON, 0, len(in))
	for _, e := range in {
		out = append(out, patternEntryStatusJSON{
			EntryID: e.EntryID, Applied: e.Applied, Inferred: e.Inferred,
			DetailMissing: e.DetailMissing, PhaseDegrees: e.PhaseDegrees,
		})
	}
	return out
}

func toPatternStatusJSON(st patch.PatternStatus) patternStatusJSON {
	out := patternStatusJSON{
		Running: st.OutputEnabled, OutputEnabled: st.OutputEnabled,
		SelectedCount: len(st.Tests), ElapsedMS: st.ElapsedMS, TotalScope: st.TotalScope,
		LastEndReason: st.LastEndReason,
		Entries:       make([]patternEntryStatusJSON, 0),
		ScopeKind:     "all",
		ScopeEntryIDs: make([]string, 0),
		Tests:         make([]patternTestStatusJSON, 0, len(st.Tests)),
		Contested:     make([]contestedOffsetJSON, 0, len(st.Contested)),
		Available:     make([]availableTestJSON, 0, len(st.Available)),
		BaseState: patternBaseStateJSON{
			Isolate:               st.BaseState.Isolate,
			DefaultsKnownCount:    st.BaseState.DefaultsKnownCount,
			DefaultsUnknownCount:  st.BaseState.DefaultsUnknownCount,
			DimmerDrivenCount:     st.BaseState.DimmerDrivenCount,
			ShutterOpenedCount:    st.BaseState.ShutterOpenedCount,
			ShutterUnknownEntries: append(make([]string, 0, len(st.BaseState.ShutterUnknownEntries)), st.BaseState.ShutterUnknownEntries...),
		},
	}
	for _, t := range st.Tests {
		out.Tests = append(out.Tests, patternTestStatusJSON{
			ID: string(t.ID), Kind: string(t.Kind), Group: string(t.Group),
			RateHz: t.Params.RateHz, Min: t.Params.Min, Max: t.Params.Max,
			Target: t.Params.Target, Direction: t.Params.Direction, Value: t.Params.Value, On: t.Params.On,
			Waveform: string(t.Params.Waveform), OffsetMin: t.Params.OffsetMin, OffsetMax: t.Params.OffsetMax,
			TotalScope: t.TotalScope, AppliedCount: t.AppliedCount, SkippedCount: t.SkippedCount,
			InferredCount: t.InferredCount, MissingDetailCount: t.MissingDetailCount,
			Entries: toPatternEntriesJSON(t.Entries),
		})
	}
	for _, c := range st.Contested {
		ids := make([]string, 0, len(c.Tests))
		for _, id := range c.Tests {
			ids = append(ids, string(id))
		}
		out.Contested = append(out.Contested, contestedOffsetJSON{Universe: c.Universe, Channel: c.Channel, EntryID: c.EntryID, Tests: ids})
	}
	for _, a := range st.Available {
		out.Available = append(out.Available, availableTestJSON{
			ID: string(a.ID), Kind: string(a.Kind), Group: string(a.Group), Target: a.Target,
			Label: a.Label, Attribute: a.Attribute, FixtureCount: a.FixtureCount, LabelFromGDTF: a.LabelFromGDTF,
		})
	}
	if len(st.Tests) > 0 {
		first := st.Tests[0]
		out.Kind, out.Group = string(first.Kind), string(first.Group)
		out.RateHz, out.Min, out.Max = first.Params.RateHz, first.Params.Min, first.Params.Max
		out.Target, out.Direction = first.Params.Target, first.Params.Direction
		out.Value, out.On = first.Params.Value, first.Params.On
		out.AppliedCount, out.SkippedCount = first.AppliedCount, first.SkippedCount
		out.InferredCount, out.MissingDetailCount = first.InferredCount, first.MissingDetailCount
		out.Entries = toPatternEntriesJSON(first.Entries)
	}
	return out
}

// patternScopeStatus is deliberately stored at the HTTP boundary. RigCheck
// receives already-resolved entries and is rightly independent of patch UI
// vocabulary; this companion preserves that vocabulary for status readback.
type patternScopeStatus struct {
	Kind        string
	Universe    uint16
	Position    string
	EntryIDs    []string
	FixtureType string
}

type fixtureTypeOptionJSON struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

func (s *Server) setPatternScope(f patternScopeFields, entries []patch.Entry) {
	kind := f.ScopeKind
	if kind == "" {
		kind = "all"
	}
	state := patternScopeStatus{Kind: kind, Universe: f.Universe, Position: f.Position, FixtureType: strings.TrimSpace(f.FixtureType), EntryIDs: make([]string, 0)}
	if kind == "selection" {
		for _, entry := range entries {
			state.EntryIDs = append(state.EntryIDs, entry.ID)
		}
	}
	s.patternScopeMu.Lock()
	s.patternScope = state
	s.patternScopeMu.Unlock()
}

func (s *Server) patternStatusJSON(st patch.PatternStatus) patternStatusJSON {
	out := toPatternStatusJSON(st)
	s.patternScopeMu.RLock()
	scope := s.patternScope
	s.patternScopeMu.RUnlock()
	if scope.Kind != "" {
		out.ScopeKind = scope.Kind
		out.ScopeUniverse = scope.Universe
		out.ScopePosition = scope.Position
		out.ScopeEntryIDs = append(make([]string, 0, len(scope.EntryIDs)), scope.EntryIDs...)
		out.ScopeFixtureType = scope.FixtureType
	}
	if p, ok := s.PatchStore.Get(); ok {
		counts := map[string]int{}
		for _, e := range p.Entries {
			if k := strings.TrimSpace(e.FixtureType); k != "" {
				counts[k]++
			}
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.FixtureTypes = make([]fixtureTypeOptionJSON, 0, len(keys))
		for _, k := range keys {
			out.FixtureTypes = append(out.FixtureTypes, fixtureTypeOptionJSON{Key: k, Label: k, Count: counts[k]})
		}
	} else {
		out.FixtureTypes = make([]fixtureTypeOptionJSON, 0)
	}
	return out
}
