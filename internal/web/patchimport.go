// This file implements POST /api/patch/import — Phase 2b's MVR/GDTF patch
// import endpoint. Per this project's client/server split, the BROWSER owns
// all .mvr/.gdtf zip and XML parsing and hands this endpoint already-
// normalized patch-entry JSON (entryRequest, the same shape every other
// patch-entry endpoint in patch.go already uses); this file's only job is
// converting that JSON into []patch.Entry and merging it into the active
// patch. See patch.go's package doc comment and endpoint reference table —
// this endpoint is listed there alongside every other patch endpoint.
package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"benny512/internal/patch"
)

// importRequest is POST /api/patch/import's body: a fresh/merge mode flag
// (same vocabulary as adoptRequest) plus a list of browser-parsed entries.
// Entries reuse entryRequest verbatim — an MVR/GDTF-imported entry is
// field-for-field the same shape as a hand-entered one, just produced by a
// different source.
type importRequest struct {
	Mode    string         `json:"mode"` // "merge" | "fresh"
	Entries []entryRequest `json:"entries"`
}

// handlePatchImport merges browser-parsed MVR/GDTF entries into the active
// patch (task ask, Phase 2b). It deliberately mirrors handlePatchAdopt's
// fresh/merge shape (same mode validation, same Replace-vs-Mutate split) but
// differs in exactly one place: imported entries never carry RDM identity —
// MVR/GDTF files don't know anything about RDM, reconciliation against
// discovered devices is a wholly separate later step (Phase 2a's
// reconcile flow) — so adopt's dedupe-by-ConfirmedUID doesn't apply here.
// Instead "merge" dedupes by (Universe, StartAddress): an incoming entry
// that lands on an address an existing entry already occupies updates that
// existing entry's descriptive fields in place while leaving its
// ID/ConfirmedUID/MatchState completely untouched (never disturb existing
// reconciliation state, same spirit as adopt's merge preserving user-edited
// fields and existing pairings); anything else is appended as new, exactly
// like adopt's merge does for unmatched devices.
func (s *Server) handlePatchImport(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Mode != "merge" && req.Mode != "fresh" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be merge or fresh, got %q", req.Mode))
		return
	}

	converted := make([]patch.Entry, 0, len(req.Entries))
	for _, er := range req.Entries {
		entry, err := entryFromRequest(patch.NewEntryID(), er)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		converted = append(converted, entry)
	}

	if req.Mode == "fresh" {
		result, err := s.PatchStore.ReplaceChecked(patch.Patch{Name: "Imported", Entries: converted})
		if err != nil {
			writePatchStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPatchResponse(result))
		return
	}

	s.PatchStore.EnsureActive()
	result, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		for _, ne := range converted {
			found := -1
			if ne.Footprint > 0 {
				for i, e := range pp.Entries {
					if e.Universe == ne.Universe && e.StartAddress == ne.StartAddress {
						found = i
						break
					}
				}
			}
			if found >= 0 {
				// Descriptive fields only — ID, ConfirmedUID and MatchState
				// are left exactly as they were (see doc comment above).
				// ChannelFunctions IS overwritten here (unlike the plain
				// hand-edit form's PUT, which preserves it — see
				// handleUpdatePatchEntry's doc comment): a fresh MVR/GDTF
				// import at the same address is exactly the case that
				// SHOULD refresh resolved channel-function data, the same
				// way it already refreshes Footprint.
				pp.Entries[found].Name = ne.Name
				pp.Entries[found].FixtureType = ne.FixtureType
				pp.Entries[found].Mode = ne.Mode
				pp.Entries[found].Footprint = ne.Footprint
				pp.Entries[found].Position = ne.Position
				pp.Entries[found].FixtureNumber = ne.FixtureNumber
				pp.Entries[found].Notes = ne.Notes
				pp.Entries[found].ChannelFunctions = ne.ChannelFunctions
				pp.Entries[found].Wheels, pp.Entries[found].WheelsKnown = ne.Wheels, ne.WheelsKnown
				// Location (schema v7) fills a gap but never replaces a
				// position the show already holds: a merge would otherwise
				// silently move a fixture the operator may have corrected.
				// Changing a known location is POST /api/patch/locations,
				// which names the entries and reports old -> new.
				if !pp.Entries[found].Location.Known {
					pp.Entries[found].Location = ne.Location
				}
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

// --- re-importing positions into an existing show -------------------------

// locationsRequest is POST /api/patch/locations' body: update ONLY the world
// location of named patch entries from a re-imported MVR, without touching
// anything else (names, profiles, pairings, addresses). The selected-entry
// pattern of POST /api/library/reprofile: the client names the entries, the
// server validates every one before writing anything, a confirmation token
// is required to apply, and the response states old -> new per entry.
//
// Sources are the entries mvrimport.js's parseMvrFile returns — the very
// objects /api/patch/import accepts — so the browser posts its parse result
// as-is. Each selected entry is matched to the ONE source at its universe and
// start address. Two sources on that address cannot say which location is
// this fixture's, so the request is refused; no source = reported unmatched,
// unchanged; a source with no location never erases a known one.
type locationsRequest struct {
	EntryIDs []string       `json:"entryIds"`
	Sources  []entryRequest `json:"sources"`
	// Preview reports what would change and writes nothing; it needs no
	// confirmation. Applying needs Confirm == "UPDATE LOCATIONS".
	Preview bool   `json:"preview"`
	Confirm string `json:"confirm"`
}

type locationsEntryResult struct {
	EntryID string `json:"entryId"`
	Label   string `json:"label"`
	// Matched: a source fixture sits at this entry's address. Source is its
	// name as the file gives it ("" when unmatched).
	Matched bool   `json:"matched"`
	Source  string `json:"source"`
	// Changed: the stored location differs from the file's and the file has
	// one (a file fixture without a location leaves the entry alone).
	Changed bool           `json:"changed"`
	Old     patch.Location `json:"old"`
	New     patch.Location `json:"new"`
}

type locationsResponse struct {
	Applied        bool                   `json:"applied"`
	EntriesChanged int                    `json:"entriesChanged"`
	Unmatched      int                    `json:"unmatched"`
	Entries        []locationsEntryResult `json:"entries"`
}

func (s *Server) handlePatchLocations(w http.ResponseWriter, r *http.Request) {
	var req locationsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !req.Preview && req.Confirm != "UPDATE LOCATIONS" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Confirm UPDATE LOCATIONS to replace the stored positions of the selected fixtures, or ask for a preview."))
		return
	}
	if len(req.EntryIDs) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("Select at least one patch entry to update."))
		return
	}
	type addr struct{ u, a uint16 }
	byAddr := map[addr][]patch.Location{}
	names := map[addr][]string{}
	for _, src := range req.Sources {
		loc := patch.Location{}
		if src.Location != nil {
			if err := src.Location.Validate(); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("Fixture %q in the file: %w", src.Name, err))
				return
			}
			loc = *src.Location
		}
		k := addr{src.Universe, src.StartAddress}
		byAddr[k] = append(byAddr[k], loc)
		names[k] = append(names[k], src.Name)
	}
	var res locationsResponse
	_, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		targets, err := selectEntries(*pp, req.EntryIDs)
		if err != nil {
			return locationsError{http.StatusBadRequest, fmt.Errorf("A selected entry is no longer in the patch (%v); refresh and select again.", err)}
		}
		res = locationsResponse{Applied: !req.Preview, Entries: make([]locationsEntryResult, 0, len(targets))}
		for _, e := range targets {
			label := e.Name
			if label == "" {
				label = e.ID
			}
			row := locationsEntryResult{EntryID: e.ID, Label: label, Old: e.Location, New: e.Location}
			k := addr{e.Universe, e.StartAddress}
			switch n := len(byAddr[k]); {
			case n > 1:
				return locationsError{http.StatusConflict, fmt.Errorf("The file has %d fixtures at the address of %q (%s), so it cannot say which position is this fixture's.", n, label, strings.Join(names[k], ", "))}
			case n == 0:
				res.Unmatched++
			default:
				row.Matched, row.Source = true, names[k][0]
				if src := byAddr[k][0]; src.Known && src != e.Location {
					row.New, row.Changed = src, true
					res.EntriesChanged++
					pp.Entries[pp.IndexOf(e.ID)].Location = src
				}
			}
			res.Entries = append(res.Entries, row)
		}
		if req.Preview {
			return errPreviewOnly
		}
		return nil
	})
	if err == errPreviewOnly {
		writeJSON(w, http.StatusOK, res)
		return
	}
	if le, ok := err.(locationsError); ok {
		writeError(w, le.status, le.err)
		return
	}
	if err != nil {
		writePatchStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// errPreviewOnly aborts a Mutate after computing the report, so a preview
// reads exactly the state an apply would change and writes nothing.
var errPreviewOnly = errors.New("preview only")

type locationsError struct {
	status int
	err    error
}

func (e locationsError) Error() string { return e.err.Error() }
