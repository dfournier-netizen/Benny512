// This file implements the Fixture Library's REST surface (see
// internal/library for the model it wraps). The library is the store of
// everything generic to a DEVICE TYPE — manufacturer/model identity, RDM
// identity numbers, observed PIDs, and each DMX mode's footprint + channel
// map — so a fixture characterised once (by GDTF import or by live RDM
// observation) never has to be characterised again, and so Benny512 can be
// carried between unrelated rigs: a patch file per rig, one library
// underneath them all.
//
// Endpoint reference (authoritative; keep in sync with routes() in
// server.go):
//
//	GET    /api/library                 -> libraryListResponse
//	GET    /api/library/export          -> file download (the shareable library document)
//	POST   /api/library/import          <- libraryImportRequest  -> library.ImportResult
//	GET    /api/library/record/{key}    -> library.Record (404 if unknown)
//	DELETE /api/library/record/{key}    <- libraryDeleteRequest  -> libraryDeleteResponse
//	POST   /api/library/from-patch      <- libraryFromPatchRequest  -> libraryFromPatchResponse
//	POST   /api/library/reprofile       <- libraryReprofileRequest -> libraryReprofileResponse
//
// The last two are the library's two directions of travel against a show
// patch, and they are deliberately separate endpoints rather than one
// "sync": /from-patch REMEMBERS what the patch already knows (harvesting a
// fixture type + mode + channel map out of patch entries into the library),
// while /reprofile REPAIRS the patch from what the library knows
// (overwriting entries' Footprint and ChannelFunctions from a chosen
// library record + mode). Only the second one writes to the patch, and only
// the second one can move DMX addressing — see its confirmation contract
// below.
//
// Apply-to-confirm (this project's standing UI contract): every destructive
// endpoint requires an explicit confirmation string in the request body,
// exactly the way POST /api/reset requires {"confirm":"RESET"} (see
// reset.go's ErrResetConfirmationRequired). DELETE requires
// {"confirm":"DELETE"}; a replace-mode import requires
// {"confirm":"REPLACE"}. A merge import needs no confirmation because it
// cannot remove anything.
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"benny512/internal/library"
	"benny512/internal/patch"
)

// --- store wiring ------------------------------------------------------
//
// The store is an ordinary Server field (Server.LibraryStore), constructed
// in New beside the patch store and pointed at a file on disk by
// cmd/benny512's SetLibraryStorePath call. It used to be held in a
// package-level sync.Map keyed by *Server, on the theory that a store the
// Server cannot reach is a store handleReset cannot wipe. That bought the
// reset exemption at the price of the library never being wired up at
// startup at all — SetLibraryStorePath existed, was tested, and was never
// called, so in production the library was in-memory only and was lost on
// every restart, which defeats the entire point of a library.
//
// The exemption is now kept by the two properties documented on
// Server.LibraryStore: internal/library.Store has no Clear() method (the
// load-bearing half — nothing handleReset could call), and the library's
// on-disk path is deliberately not retained on the Server (nothing
// handleReset could delete). See handleReset's step 5.

// --- list / get one ----------------------------------------------------

// libraryListResponse is GET /api/library. It repeats the document
// envelope (format/schemaVersion) so a client can tell at a glance which
// file format an export from this build will produce.
type libraryListResponse struct {
	Format        string `json:"format"`
	SchemaVersion int    `json:"schemaVersion"`
	// Count deliberately has NO `omitempty`: an empty library reporting
	// nothing at all instead of `"count":0` is the same class of defect
	// this codebase has hit three times with numeric zeros.
	Count      int       `json:"count"`
	CreatedAt  time.Time `json:"createdAt"`
	ModifiedAt time.Time `json:"modifiedAt"`
	// Records is never nil (library.Store.List always returns a made
	// slice), so this marshals as `[]`, never `null`.
	Records []library.Record `json:"records"`
}

func (s *Server) handleGetLibrary(w http.ResponseWriter, r *http.Request) {
	st := s.LibraryStore
	lib := st.Get()
	// Browsing needs source metadata, not every base64 archive. Export and
	// the source download retain the original bytes.
	for i := range lib.Records {
		for j := range lib.Records[i].SourceFiles {
			lib.Records[i].SourceFiles[j].Data = nil
		}
	}
	writeJSON(w, http.StatusOK, libraryListResponse{
		Format:        lib.Format,
		SchemaVersion: lib.SchemaVersion,
		Count:         len(lib.Records),
		CreatedAt:     lib.CreatedAt,
		ModifiedAt:    lib.ModifiedAt,
		Records:       lib.Records,
	})
}

func (s *Server) handleGetLibraryRecord(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	rec, ok := s.LibraryStore.GetByKey(key)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no library record with key %q", key))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleVerifyLibraryMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string        `json:"key"`
		Mode     string        `json:"mode"`
		Note     string        `json:"note"`
		Verified bool          `json:"verified"`
		Confirm  string        `json:"confirm"`
		Expected *library.Mode `json:"expected"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.Confirm != "VERIFY" || len(req.Note) > 500 || req.Expected == nil {
		writeError(w, 400, fmt.Errorf("confirm VERIFY with the reviewed mode; note must be at most 500 characters"))
		return
	}
	rec, err := s.LibraryStore.VerifyMode(req.Key, req.Mode, strings.TrimSpace(req.Note), req.Verified, req.Expected)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, rec)
}

// --- delete ------------------------------------------------------------

// ErrLibraryDeleteConfirmationRequired is returned (as a 400) when DELETE
// /api/library/record/{key} arrives without the exact confirm string —
// the same deliberately un-guessable tripwire against an accidental or
// scripted call that POST /api/reset uses, and this project's standing
// Apply-to-confirm contract for any write the user cannot undo.
var ErrLibraryDeleteConfirmationRequired = errors.New("confirmation required")

type libraryDeleteRequest struct {
	Confirm string `json:"confirm"`
}

type libraryDeleteResponse struct {
	// Deleted/Key/Count all deliberately without `omitempty`: `false` and
	// `0` are the meaningful answers here.
	Deleted bool   `json:"deleted"`
	Key     string `json:"key"`
	Count   int    `json:"count"`
}

func (s *Server) handleDeleteLibraryRecord(w http.ResponseWriter, r *http.Request) {
	var req libraryDeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != "DELETE" {
		writeError(w, http.StatusBadRequest, ErrLibraryDeleteConfirmationRequired)
		return
	}
	key := r.PathValue("key")
	st := s.LibraryStore
	deleted, err := st.DeleteChecked(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, fmt.Errorf("no library record with key %q", key))
		return
	}
	writeJSON(w, http.StatusOK, libraryDeleteResponse{Deleted: true, Key: key, Count: len(st.List())})
}

// --- export ------------------------------------------------------------

// handleLibraryExport serves the whole library as one shareable JSON
// document — the file the owner hands to a coworker alongside the binary.
// Same Content-Disposition/timestamped-filename mechanics as GET
// /api/patch/export and GET /api/capture/export.
//
// Deliberately JSON only (no TXT variant, unlike the patch export): this
// file's purpose is to be re-imported by another install, not read by a
// human, and offering a lossy text rendering of it would invite someone to
// hand over the unimportable one.
func (s *Server) handleLibraryExport(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	doc := s.LibraryStore.Export(AppVersion, now)
	filename := fmt.Sprintf("benny512_library_%s.json", now.Format("20060102_150405"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}

// --- import ------------------------------------------------------------

// ErrLibraryReplaceConfirmationRequired is returned (as a 400) when a
// replace-mode import arrives without {"confirm":"REPLACE"}. A merge
// import needs no confirmation — it can only add.
var ErrLibraryReplaceConfirmationRequired = errors.New("confirmation required: a replace import discards the entire existing library")

// libraryImportRequest is POST /api/library/import's body.
//
// Library is json.RawMessage rather than a library.Library so the two
// halves of this body get the decoding discipline each one needs:
// decodeJSON's DisallowUnknownFields catches a typo in the ENVELOPE
// ("moode":"merge" must not silently become an empty mode), while the
// document itself is decoded tolerantly below — it is a FILE, possibly
// written by a different build, and this project's "old files must always
// open" rule applies to it. Rejecting a coworker's library because a newer
// Benny512 added a field would be exactly the wrong failure.
type libraryImportRequest struct {
	Mode    string          `json:"mode"`
	Confirm string          `json:"confirm"`
	Library json.RawMessage `json:"library"`
}

func (s *Server) handleLibraryImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	var req libraryImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	mode := library.ImportMode(req.Mode)
	switch mode {
	case library.ModeMerge:
	case library.ModeReplace:
		if req.Confirm != "REPLACE" {
			writeError(w, http.StatusBadRequest, ErrLibraryReplaceConfirmationRequired)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be %q or %q, got %q", library.ModeMerge, library.ModeReplace, req.Mode))
		return
	}
	if len(req.Library) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no library document in the request body"))
		return
	}
	var doc library.Library
	if err := json.Unmarshal(req.Library, &doc); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("library document is not valid JSON: %w", err))
		return
	}
	res, err := s.LibraryStore.Import(doc, mode)
	if err != nil {
		// A rejected document has changed nothing (library.Store.Import
		// validates before it locks or mutates), so a 400 here is a true
		// "your file was not imported", not a partial state.
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLibrarySource(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.LibraryStore.GetByKey(r.URL.Query().Get("key"))
	if ok {
		for _, f := range rec.SourceFiles {
			if f.SHA256 == r.URL.Query().Get("hash") {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
				_, _ = w.Write(f.Data)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("source file not found"))
}

// --- populate the library from the patch --------------------------------

// libraryOutcomeSkipped extends library.UpsertOutcome for the one outcome
// Upsert itself can never return: an entry that carried nothing worth
// storing, so no upsert was attempted at all. Reported in the same field as
// the three real outcomes rather than as a separate list, so a UI renders
// one table and a caller counting rows sees every entry it asked about.
const libraryOutcomeSkipped library.UpsertOutcome = "skipped"

// libraryFromPatchRequest is POST /api/library/from-patch's body.
//
// No confirmation string: this endpoint is additive by construction. It
// upserts, and library.Store's merge rules never remove a mode, never erase
// a populated channel map with an empty one, and never re-key an existing
// record — the same reasoning that lets a merge-mode import skip
// Apply-to-confirm while a replace-mode one requires it.
type libraryFromPatchRequest struct {
	// EntryIDs selects which patch entries to harvest. Absent or empty
	// means EVERY entry in the active patch — the ordinary "I just imported
	// an MVR, remember all of it" case. An unknown ID is an error, not a
	// silent skip: a UI that sent a stale ID must find out.
	EntryIDs []string `json:"entryIds"`
}

// libraryFromPatchEntryResult is one line of the harvest report.
type libraryFromPatchEntryResult struct {
	EntryID string `json:"entryId"`
	Label   string `json:"label"`
	// Key/Manufacturer/Model/Mode describe the library record the entry
	// landed in — which is NOT necessarily the identity the entry supplied:
	// the tolerant matcher may have merged "GLP JDC1 Strobe" into an
	// existing "GLP" / "JDC-1" record, and the response says so rather than
	// letting the UI assume its own spelling won.
	Key          string `json:"key"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	// Footprint/ChannelCount are what was stored for that mode. No
	// `omitempty` on either: a footprint of 0 is real, meaningful data (a
	// data-only device, or an import that resolved no DMX channels) and a
	// channel count of 0 is the whole signal that this entry contributed a
	// footprint but no channel map.
	Footprint    uint16 `json:"footprint"`
	ChannelCount int    `json:"channelCount"`
	// Outcome is library.Upsert's own outcome verbatim (added / updated /
	// unchanged), or libraryOutcomeSkipped.
	Outcome library.UpsertOutcome `json:"outcome"`
	Detail  string                `json:"detail,omitempty"`
}

// libraryFromPatchResponse is the whole harvest report.
type libraryFromPatchResponse struct {
	// Every count is `omitempty`-free for the reason the import report
	// states: "0 added" is the single most important thing this response
	// ever has to say.
	Total     int `json:"total"`
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Skipped   int `json:"skipped"`
	// Count is how many records the library holds afterwards.
	Count int `json:"count"`
	// Entries is one line per entry asked about, in patch order. Never nil.
	Entries []libraryFromPatchEntryResult `json:"entries"`
}

// provenanceForEntry infers where a patch entry's channel map came from,
// from the per-channel Source the entry already carries — never a guess and
// never a record-level assumption. A map holding any GDTF-sourced channel is
// GDTF-sourced (a GDTF import is authoritative and is what populates a whole
// mode at once); a map holding only RDM-inferred channels is RDM-observed;
// an empty map came from a human typing a footprint in.
func provenanceForEntry(e patch.Entry) library.Provenance {
	sawRDM := false
	for _, cf := range e.ChannelFunctions {
		switch cf.Source {
		case patch.SourceGDTF:
			return library.ProvenanceGDTF
		case patch.SourceRDMInferred:
			sawRDM = true
		}
	}
	if sawRDM {
		return library.ProvenanceRDM
	}
	return library.ProvenanceManual
}

// recordFromEntry builds the one-mode library record a patch entry implies.
//
// Manufacturer is deliberately left EMPTY and the whole of the entry's
// FixtureType becomes the model. A patch entry's FixtureType is free text
// ("Chauvet Rogue Outcast 2X Wash", or just "JDC 1" scrawled on a patch
// sheet) with no reliable manufacturer/model split, and splitting it on the
// first space would invent a manufacturer of "Chauvet" for one entry and
// "JDC" for the next. Leaving it unstated costs nothing: library.Upsert
// matches tolerantly, so "GLP JDC1 Strobe" merges into an existing
// "GLP" / "JDC-1" record and inherits its (real) manufacturer spelling,
// and a genuinely new record simply has no manufacturer until a GDTF import
// or a live RDM device supplies one.
func recordFromEntry(e patch.Entry, now time.Time) library.Record {
	origin := library.Origin{
		Source: provenanceForEntry(e),
		Detail: "patch entry " + patch.EntryLabel(e),
		At:     now,
	}
	cfs := make(map[uint16]patch.ChannelFunction, len(e.ChannelFunctions))
	for off, cf := range e.ChannelFunctions {
		cf.ChannelSets = append(make([]patch.ChannelSet, 0, len(cf.ChannelSets)), cf.ChannelSets...)
		cfs[off] = cf
	}
	return library.Record{
		Model:          strings.TrimSpace(e.FixtureType),
		IdentityOrigin: origin,
		SupportedPIDs:  make([]uint16, 0),
		Modes: []library.Mode{{
			Name:             e.Mode,
			Footprint:        e.Footprint,
			ChannelFunctions: cfs,
			Origin:           origin,
		}},
	}
}

// handleLibraryFromPatch harvests fixture types out of the active patch and
// into the library — the "remember what I just imported" half of the
// library's reason to exist. Its counterpart is handleLibraryReprofile
// below, which pushes a remembered profile back onto a patch entry.
func (s *Server) handleLibraryFromPatch(w http.ResponseWriter, r *http.Request) {
	var req libraryFromPatchRequest
	// An EMPTY body is the natural spelling of this endpoint's default
	// ("harvest the whole patch") and is accepted as such — io.EOF here
	// means the client sent no body at all, not that it sent a malformed
	// one. Every other decode error is still a 400, so a typo in a body
	// that IS present is caught exactly as decodeJSON's
	// DisallowUnknownFields intends.
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusBadRequest, patch.ErrNoPatch)
		return
	}
	targets, err := selectEntries(p, req.EntryIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now()
	st := s.LibraryStore
	res := libraryFromPatchResponse{
		Total:   len(targets),
		Entries: make([]libraryFromPatchEntryResult, 0, len(targets)),
	}
	for _, e := range targets {
		line := libraryFromPatchEntryResult{
			EntryID:      e.ID,
			Label:        patch.EntryLabel(e),
			Mode:         e.Mode,
			Footprint:    e.Footprint,
			ChannelCount: len(e.ChannelFunctions),
		}
		if strings.TrimSpace(e.FixtureType) == "" {
			line.Outcome = libraryOutcomeSkipped
			line.Detail = "entry has no fixture type — there is nothing to key a library record on"
			res.Skipped++
			res.Entries = append(res.Entries, line)
			continue
		}
		stored, outcome, err := st.UpsertChecked(recordFromEntry(e, now))
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("library save failed (earlier profiles may have saved): %w", err))
			return
		}
		line.Key, line.Manufacturer, line.Model = stored.Key, stored.Manufacturer, stored.Model
		line.Outcome = outcome
		switch outcome {
		case library.OutcomeAdded:
			res.Added++
		case library.OutcomeUpdated:
			res.Updated++
		default:
			res.Unchanged++
			line.Detail = "the library already held this mode, identically"
		}
		if outcome != library.OutcomeAdded && stored.Key != library.KeyFor("", e.FixtureType) {
			line.Detail = fmt.Sprintf("merged into existing record %q / %q", stored.Manufacturer, stored.Model)
		}
		res.Entries = append(res.Entries, line)
	}
	res.Count = len(st.List())
	writeJSON(w, http.StatusOK, res)
}

// selectEntries resolves a request's entry-ID list against p: an empty list
// means every entry, in patch order; a non-empty list is returned in PATCH
// order too (not the caller's order), so a report always reads down the
// patch the way the Patch screen shows it. Every ID must exist.
func selectEntries(p patch.Patch, ids []string) ([]patch.Entry, error) {
	if len(ids) == 0 {
		return append(make([]patch.Entry, 0, len(p.Entries)), p.Entries...), nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]patch.Entry, 0, len(ids))
	for _, e := range p.Entries {
		if want[e.ID] {
			out = append(out, e)
			delete(want, e.ID)
		}
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for _, id := range ids {
			if want[id] {
				missing = append(missing, id)
			}
		}
		return nil, fmt.Errorf("no patch entry with id %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// --- re-profile patch entries from the library --------------------------

// ErrLibraryReprofileConfirmationRequired is returned (as a 400) when POST
// /api/library/reprofile arrives without {"confirm":"REPROFILE"}.
//
// This endpoint is the one place the library WRITES to a real show patch,
// and what it writes is a footprint — i.e. DMX addressing. A fixture whose
// footprint grows by one channel now overlaps whatever is patched
// immediately after it, and the tech finds out on stage. That is exactly
// the class of change this project's standing Apply-to-confirm contract
// exists for (POST /api/reset's {"confirm":"RESET"}, a replace-mode
// library import's {"confirm":"REPLACE"}).
var ErrLibraryReprofileConfirmationRequired = errors.New("confirmation required: re-profiling overwrites patched footprints and can move DMX addressing")

// libraryReprofileRequest is POST /api/library/reprofile's body.
//
// The motivating case, in the owner's words: his show file's MVR carried
// Vectorworks PLACEHOLDER GDTFs for two fixture types — flat, featureless
// channel lists under a single generic "DMX Mode", 61 and 23 channels where
// the real vendor files have 62 and 24 — so he patched a whole rig one
// channel short per fixture. Importing the real vendor GDTF into the
// library and re-profiling the already-patched entries from it is the
// repair, without re-importing the MVR and losing every name, position and
// confirmed RDM pairing the patch has accumulated since.
type libraryReprofileRequest struct {
	Confirm string `json:"confirm"`
	// EntryIDs are the patch entries to re-profile. Required and non-empty:
	// unlike the harvest above, "all of them" is never a safe default for a
	// write that moves DMX addressing.
	EntryIDs []string `json:"entryIds"`
	// Key is the library record's storage key (library.Record.Key, as GET
	// /api/library reports it) — the exact key, not a fuzzy name. Choosing
	// WHICH fixture type to stamp onto a patch entry is the user's decision
	// made in the UI against the record list; resolving it tolerantly here
	// would let a typo silently re-profile a rig from the wrong fixture.
	Key string `json:"key"`
	// Mode is the record's mode name, matched case-insensitively (the same
	// fold library.Store's own mode merging uses, so a mode name copied out
	// of a GET response always resolves).
	Mode string `json:"mode"`
}

// libraryReprofileEntryResult is the per-entry statement of what changed —
// old footprint -> new footprint above all, since that is the number that
// can break a rig.
type libraryReprofileEntryResult struct {
	EntryID string `json:"entryId"`
	Label   string `json:"label"`
	// Universe/StartAddress are unchanged by this endpoint and are echoed
	// purely so a UI can render the affected address range without a second
	// fetch. No `omitempty`: universe 0 and start address 0 are both real.
	Universe     uint16 `json:"universe"`
	StartAddress uint16 `json:"startAddress"`
	// Old/New footprints and the channel range they imply. No `omitempty`
	// anywhere here — a footprint of 0 is the exact value this codebase has
	// been bitten by erasing three times, and FootprintChanged==false is the
	// meaningful half of that boolean.
	OldFootprint     uint16 `json:"oldFootprint"`
	NewFootprint     uint16 `json:"newFootprint"`
	FootprintChanged bool   `json:"footprintChanged"`
	OldEndAddress    int    `json:"oldEndAddress"`
	NewEndAddress    int    `json:"newEndAddress"`
	OldMode          string `json:"oldMode"`
	NewMode          string `json:"newMode"`
	// Channel-map sizes before and after. A jump from 0 to 62 is how the
	// user sees that a placeholder profile has been replaced by a real one.
	OldChannelFunctionCount int `json:"oldChannelFunctionCount"`
	NewChannelFunctionCount int `json:"newChannelFunctionCount"`
}

// libraryReprofileResponse states exactly what the write did, plus what it
// may have broken.
type libraryReprofileResponse struct {
	Applied bool `json:"applied"`
	// The library record and mode the profile came from.
	Key          string `json:"key"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Mode         string `json:"mode"`
	// Footprint/ChannelFunctionCount are the profile that was applied. No
	// `omitempty`: a library mode with footprint 0 is real data.
	Footprint            uint16 `json:"footprint"`
	ChannelFunctionCount int    `json:"channelFunctionCount"`
	// EntriesChanged counts entries whose footprint, mode name or channel
	// map actually differed. No `omitempty` — "0 changed" is the answer a
	// user most needs to see.
	EntriesChanged int `json:"entriesChanged"`
	// Entries is one line per re-profiled entry, in patch order. Never nil.
	Entries []libraryReprofileEntryResult `json:"entries"`
	// Collisions is internal/patch's own collision detection re-run over
	// the WHOLE patch after the change (patch.DetectCollisions), because a
	// grown footprint collides with the NEIGHBOUR, not with the entry that
	// grew — reporting only the touched entries would hide the damage.
	//
	// Deliberately REPORTED, NOT ENFORCED: the change is applied either
	// way. A rig being re-profiled is expected to collide partway through
	// (fix the fixture type, then re-address), and a server that refused
	// the repair because the intermediate state was invalid would make the
	// repair impossible. The owner decides what to do about the findings.
	// Never nil — DetectCollisions always returns a made slice.
	Collisions []patch.Finding `json:"collisions"`
	// CollisionErrors/CollisionWarnings are the findings split by severity
	// so a UI can lead with the count without re-deriving it. No
	// `omitempty`: zero errors is the good news this response exists to
	// deliver.
	CollisionErrors   int `json:"collisionErrors"`
	CollisionWarnings int `json:"collisionWarnings"`
}

// findMode resolves a mode name within a record, case-insensitively —
// matching library.Store's own mergeModes fold, so a name round-tripped
// through a GET always resolves.
func findMode(rec library.Record, name string) (library.Mode, bool) {
	want := strings.Join(strings.Fields(strings.ToLower(name)), " ")
	for _, m := range rec.Modes {
		if strings.Join(strings.Fields(strings.ToLower(m.Name)), " ") == want {
			return m, true
		}
	}
	return library.Mode{}, false
}

func (s *Server) handleLibraryReprofile(w http.ResponseWriter, r *http.Request) {
	var req libraryReprofileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != "REPROFILE" {
		writeError(w, http.StatusBadRequest, ErrLibraryReprofileConfirmationRequired)
		return
	}
	if len(req.EntryIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("entryIds is required: name the patch entries to re-profile"))
		return
	}
	rec, ok := s.LibraryStore.GetByKey(req.Key)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no library record with key %q", req.Key))
		return
	}
	mode, ok := findMode(rec, req.Mode)
	if !ok {
		names := make([]string, 0, len(rec.Modes))
		for _, m := range rec.Modes {
			names = append(names, strconv.Quote(m.Name))
		}
		avail := "none"
		if len(names) > 0 {
			avail = strings.Join(names, ", ")
		}
		writeError(w, http.StatusBadRequest, fmt.Errorf("library record %q / %q has no mode %q (it has: %s)", rec.Manufacturer, rec.Model, req.Mode, avail))
		return
	}

	// Validate the entry IDs against the CURRENT patch before mutating, so
	// a request naming one stale ID changes nothing at all rather than
	// re-profiling the entries it did recognise (the same validate-then-
	// mutate ordering library.Store.Import uses).
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusBadRequest, patch.ErrNoPatch)
		return
	}
	targets, err := selectEntries(p, req.EntryIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	want := make(map[string]bool, len(targets))
	for _, e := range targets {
		want[e.ID] = true
	}

	res := libraryReprofileResponse{
		Applied:              true,
		Key:                  rec.Key,
		Manufacturer:         rec.Manufacturer,
		Model:                rec.Model,
		Mode:                 mode.Name,
		Footprint:            mode.Footprint,
		ChannelFunctionCount: len(mode.ChannelFunctions),
		Entries:              make([]libraryReprofileEntryResult, 0, len(targets)),
	}

	updated, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		for i := range pp.Entries {
			e := &pp.Entries[i]
			if !want[e.ID] {
				continue
			}
			line := libraryReprofileEntryResult{
				EntryID:                 e.ID,
				Label:                   patch.EntryLabel(*e),
				Universe:                e.Universe,
				StartAddress:            e.StartAddress,
				OldFootprint:            e.Footprint,
				NewFootprint:            mode.Footprint,
				OldEndAddress:           e.EndAddress(),
				OldMode:                 e.Mode,
				NewMode:                 mode.Name,
				OldChannelFunctionCount: len(e.ChannelFunctions),
				NewChannelFunctionCount: len(mode.ChannelFunctions),
			}
			line.FootprintChanged = line.OldFootprint != line.NewFootprint
			changed := line.FootprintChanged || e.Mode != mode.Name ||
				!sameChannelFunctions(e.ChannelFunctions, mode.ChannelFunctions)

			// Each entry gets its OWN copy of the mode's channel map. A
			// shared map would alias every re-profiled entry (and the
			// library record itself) to one object, so a later per-entry
			// edit would silently rewrite the fixture type for the whole
			// rig — library.Store hands out deep copies for exactly this
			// reason and that guarantee must not be thrown away here.
			cfs := make(map[uint16]patch.ChannelFunction, len(mode.ChannelFunctions))
			for off, cf := range mode.ChannelFunctions {
				cf.ChannelSets = append(make([]patch.ChannelSet, 0, len(cf.ChannelSets)), cf.ChannelSets...)
				cfs[off] = cf
			}
			e.Footprint = mode.Footprint
			e.ChannelFunctions = cfs
			// The mode NAME is overwritten alongside the footprint and the
			// channel map it names. Leaving a stale "DMX Mode" label on an
			// entry now carrying "Mode 2 Normal (23ch)" data would be a
			// lie on the Patch screen about what the fixture is set to —
			// and the mode name is what a tech reads off the fixture's own
			// display when checking. FixtureType is deliberately NOT
			// touched: that is the rig's naming, which the owner owns.
			e.Mode = mode.Name
			line.NewEndAddress = e.EndAddress()
			if changed {
				res.EntriesChanged++
			}
			res.Entries = append(res.Entries, line)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Collision detection AFTER the change, over the whole patch — a grown
	// footprint collides with its neighbour, not with itself.
	res.Collisions = patch.DetectCollisions(updated)
	for _, f := range res.Collisions {
		switch f.Severity {
		case patch.SeverityError:
			res.CollisionErrors++
		case patch.SeverityWarning:
			res.CollisionWarnings++
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// sameChannelFunctions reports whether two channel maps are equivalent —
// the "did this entry actually change?" question, answered the same way
// library.Store answers it for a record (reflect.DeepEqual over normalised
// values) rather than by comparing lengths, which would call a swapped
// profile of the same size "unchanged".
func sameChannelFunctions(a, b map[uint16]patch.ChannelFunction) bool {
	if len(a) != len(b) {
		return false
	}
	for off, av := range a {
		bv, ok := b[off]
		if !ok {
			return false
		}
		if len(av.ChannelSets) == 0 {
			av.ChannelSets = make([]patch.ChannelSet, 0)
		}
		if len(bv.ChannelSets) == 0 {
			bv.ChannelSets = make([]patch.ChannelSet, 0)
		}
		if !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	return true
}
