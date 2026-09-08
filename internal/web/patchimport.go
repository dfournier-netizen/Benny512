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
	"fmt"
	"net/http"

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
