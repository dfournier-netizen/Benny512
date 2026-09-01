package web

import (
	"net/http"
	"testing"

	"benny512/internal/patch"
	"benny512/internal/rdm"
)

// --- BuildRDMInferredChannelFunctions (Task 3 bridge) -----------------------

// TestBuildRDMInferredChannelFunctions_PrimaryMapped exercises the
// straightforward case: a primary SLOT_INFO record whose Slot Label ID is
// in rdmSlotLabelAttributes resolves to that attribute, tagged
// Source==SourceRDMInferred (decision 3's hard constraint — never
// SourceGDTF).
func TestBuildRDMInferredChannelFunctions_PrimaryMapped(t *testing.T) {
	slots := []rdm.SlotInfoEntry{
		{Offset: 5, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
	}
	got := BuildRDMInferredChannelFunctions(slots, nil)
	cf, ok := got[5]
	if !ok {
		t.Fatal("expected an entry at offset 5")
	}
	if cf.Source != patch.SourceRDMInferred {
		t.Errorf("Source = %v, want SourceRDMInferred", cf.Source)
	}
	if cf.Attribute != "Pan" {
		t.Errorf("Attribute = %q, want Pan", cf.Attribute)
	}
	if cf.RDMSlotType != "ST_PRIMARY" {
		t.Errorf("RDMSlotType = %q", cf.RDMSlotType)
	}
	if cf.RDMSlotLabel != "SD_PAN" {
		t.Errorf("RDMSlotLabel = %q, want the symbolic name (no SLOT_DESCRIPTION supplied)", cf.RDMSlotLabel)
	}
}

// TestBuildRDMInferredChannelFunctions_SlotDescriptionPreferred: when a
// SLOT_DESCRIPTION text is supplied for an offset, it takes precedence over
// the symbolic Slot Label ID name in RDMSlotLabel — stronger raw evidence.
func TestBuildRDMInferredChannelFunctions_SlotDescriptionPreferred(t *testing.T) {
	slots := []rdm.SlotInfoEntry{{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDIntensity)}}
	got := BuildRDMInferredChannelFunctions(slots, map[uint16]string{0: "Master Dim"})
	if got[0].RDMSlotLabel != "Master Dim" {
		t.Errorf("RDMSlotLabel = %q, want device's own SLOT_DESCRIPTION text", got[0].RDMSlotLabel)
	}
}

// TestBuildRDMInferredChannelFunctions_SecondaryResolvesViaPrimary: a
// secondary (fine-byte) slot has no Slot Label ID of its own — it must
// resolve to its PRIMARY slot's Attribute, the same "one function, two
// offsets" shape gdtfparse.js produces for a 16-bit GDTF function.
func TestBuildRDMInferredChannelFunctions_SecondaryResolvesViaPrimary(t *testing.T) {
	slots := []rdm.SlotInfoEntry{
		{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
		{Offset: 1, Type: rdm.SlotTypeSecondaryFine, Value: 0},
	}
	got := BuildRDMInferredChannelFunctions(slots, nil)
	if got[1].Attribute != "Pan" {
		t.Errorf("secondary slot Attribute = %q, want Pan (resolved via primary at offset 0)", got[1].Attribute)
	}
	if got[1].Source != patch.SourceRDMInferred {
		t.Errorf("secondary slot Source = %v, want SourceRDMInferred", got[1].Source)
	}
}

// TestBuildRDMInferredChannelFunctions_OrphanSecondarySkipped: a secondary
// slot referring to a primary offset that's absent from slots entirely
// (malformed/partial SLOT_INFO) must be skipped, never guessed.
func TestBuildRDMInferredChannelFunctions_OrphanSecondarySkipped(t *testing.T) {
	slots := []rdm.SlotInfoEntry{
		{Offset: 1, Type: rdm.SlotTypeSecondaryFine, Value: 99}, // offset 99 not in slots
	}
	got := BuildRDMInferredChannelFunctions(slots, nil)
	if _, ok := got[1]; ok {
		t.Error("expected orphan secondary slot to be skipped, not resolved")
	}
}

// TestBuildRDMInferredChannelFunctions_UnmappedLabelStillPresent: a slot
// label with no entry in rdmSlotLabelAttributes (e.g. a control function)
// still produces a ChannelFunction — with Attribute=="" (GroupOther via
// GroupForAttribute), never dropped, matching the task's "unmapped is
// surfaced" rule.
func TestBuildRDMInferredChannelFunctions_UnmappedLabelStillPresent(t *testing.T) {
	slots := []rdm.SlotInfoEntry{{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDMacro)}}
	got := BuildRDMInferredChannelFunctions(slots, nil)
	cf, ok := got[0]
	if !ok {
		t.Fatal("expected an entry even for an unmapped slot label")
	}
	if cf.Attribute != "" {
		t.Errorf("Attribute = %q, want empty (unmapped -> Other via GroupForAttribute)", cf.Attribute)
	}
	if patch.GroupForAttribute(cf.Attribute) != patch.GroupOther {
		t.Errorf("GroupForAttribute(%q) = %v, want GroupOther", cf.Attribute, patch.GroupForAttribute(cf.Attribute))
	}
}

// TestBuildRDMInferredChannelFunctions_NeverProducesGDTFSource guards
// decision (3)'s hard constraint end to end through this bridge function
// specifically (not just at the type-system level in entry.go): no matter
// what slot data comes in, every produced ChannelFunction's Source is
// SourceRDMInferred, never SourceGDTF.
func TestBuildRDMInferredChannelFunctions_NeverProducesGDTFSource(t *testing.T) {
	slots := []rdm.SlotInfoEntry{
		{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
		{Offset: 1, Type: rdm.SlotTypeSecondaryFine, Value: 0},
		{Offset: 2, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDFramingShutter)},
	}
	got := BuildRDMInferredChannelFunctions(slots, nil)
	for offset, cf := range got {
		if cf.Source != patch.SourceRDMInferred {
			t.Errorf("offset %d: Source = %v, want SourceRDMInferred", offset, cf.Source)
		}
	}
	if got[2].Attribute != "Shaper" {
		t.Errorf("SD_FRAMING_SHUTTER Attribute = %q, want Shaper", got[2].Attribute)
	}
}

// --- GET /api/patch/attributes (Task 4) -------------------------------------

func entryWithGDTFDimmer(id string, universe, addr uint16) entryRequest {
	return entryRequest{
		Name: id, Universe: universe, StartAddress: addr, Footprint: 1,
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "gdtf", Attribute: "Dimmer", FunctionName: "Dimmer"},
		},
	}
}

func TestHandleGetPatchAttributes_NoActivePatch(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/attributes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp patchAttributesResponse
	mustUnmarshal(t, rr, &resp)
	if len(resp.Entries) != 0 || resp.Summary.TotalFixtures != 0 {
		t.Errorf("resp = %+v, want empty", resp)
	}
}

func TestHandleGetPatchAttributes_WholePatch(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryWithGDTFDimmer("Wash 1", 0, 1))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "No functions", Universe: 0, StartAddress: 10, Footprint: 1})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/attributes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp patchAttributesResponse
	mustUnmarshal(t, rr, &resp)
	if resp.Summary.TotalFixtures != 2 {
		t.Fatalf("TotalFixtures = %d, want 2", resp.Summary.TotalFixtures)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("Entries = %+v, want 2", resp.Entries)
	}
	var dimmerCount int
	for _, g := range resp.Summary.Groups {
		if g.Group == "dimmer" {
			dimmerCount = g.Count
		}
	}
	if dimmerCount != 1 {
		t.Errorf("dimmer group count = %d, want 1 of 2", dimmerCount)
	}
}

// TestHandleGetPatchAttributes_SelectionByIDs proves the ?ids= scoping —
// the summary and entry list must reflect only the selected entries, per
// Task 4's "for a patch entry or a selection" requirement.
func TestHandleGetPatchAttributes_SelectionByIDs(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryWithGDTFDimmer("Wash 1", 0, 1))
	var resp1 patchResponse
	mustUnmarshal(t, rr, &resp1)
	id1 := resp1.Patch.Entries[0].ID

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 2", Universe: 0, StartAddress: 10, Footprint: 1})
	var resp2 patchResponse
	mustUnmarshal(t, rr, &resp2)

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/attributes?ids="+id1, nil)
	var attrResp patchAttributesResponse
	mustUnmarshal(t, rr, &attrResp)
	if attrResp.Summary.TotalFixtures != 1 {
		t.Fatalf("TotalFixtures = %d, want 1 (scoped to one id)", attrResp.Summary.TotalFixtures)
	}
	if len(attrResp.Entries) != 1 || attrResp.Entries[0].EntryID != id1 {
		t.Fatalf("Entries = %+v, want just %s", attrResp.Entries, id1)
	}
}

// --- entryRequest.ChannelFunctions validation --------------------------------

// TestCreatePatchEntry_RejectsBadChannelFunctionSource guards the boundary
// validation errBadChannelFunctionSource exists for: a client cannot forge
// Source=="gdtf" for data it didn't actually get from a GDTF file by just
// sending an arbitrary string — but ALSO, more importantly, cannot send
// anything other than the two real values at all.
func TestCreatePatchEntry_RejectsBadChannelFunctionSource(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Bad", Universe: 0, StartAddress: 1, Footprint: 1,
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "totally-trustworthy", Attribute: "Dimmer"},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400 for an invalid source", rr.Code, rr.Body.String())
	}
}

// TestUpdatePatchEntry_PreservesChannelFunctionsOnPlainEdit guards
// handleUpdatePatchEntry's doc comment directly: a plain field edit
// (submitting no channelFunctions at all) must not wipe out
// previously-resolved channel-function data — the same "never disturb
// existing resolved state" rule this project already applies to
// ConfirmedUID/MatchState.
func TestUpdatePatchEntry_PreservesChannelFunctionsOnPlainEdit(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryWithGDTFDimmer("Wash 1", 0, 1))
	var createResp patchResponse
	mustUnmarshal(t, rr, &createResp)
	id := createResp.Patch.Entries[0].ID
	if len(createResp.Patch.Entries[0].ChannelFunctions) != 1 {
		t.Fatalf("setup: expected 1 channel function, got %+v", createResp.Patch.Entries[0].ChannelFunctions)
	}

	// Plain edit: rename only, no channelFunctions in the request.
	rr = doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, entryRequest{
		Name: "Wash 1 renamed", Universe: 0, StartAddress: 1, Footprint: 1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	// A FRESH response struct — not reused across unmarshal calls. Reusing
	// one across a create-then-update pair here would be a self-defeating
	// test: encoding/json's map decoding merges keys into an EXISTING
	// non-nil map rather than clearing it first, so decoding this update's
	// (possibly empty) "channelFunctions" into the create response's
	// already-populated map would make a wiped-out map look preserved
	// regardless of what the handler actually did.
	var updateResp patchResponse
	mustUnmarshal(t, rr, &updateResp)
	if updateResp.Patch.Entries[0].Name != "Wash 1 renamed" {
		t.Fatalf("rename did not apply: %+v", updateResp.Patch.Entries[0])
	}
	if len(updateResp.Patch.Entries[0].ChannelFunctions) != 1 {
		t.Errorf("plain edit wiped ChannelFunctions: %+v", updateResp.Patch.Entries[0].ChannelFunctions)
	}
	cf := updateResp.Patch.Entries[0].ChannelFunctions[1]
	if cf.Attribute != "Dimmer" || string(cf.Source) != "gdtf" {
		t.Errorf("preserved ChannelFunctions changed content: %+v", cf)
	}
}

// TestUpdatePatchEntry_ExplicitChannelFunctionsReplace: submitting a
// non-empty channelFunctions on an update (the single-GDTF re-apply case)
// DOES replace the entry's previous channel-function data.
func TestUpdatePatchEntry_ExplicitChannelFunctionsReplace(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryWithGDTFDimmer("Wash 1", 0, 1))
	var createResp patchResponse
	mustUnmarshal(t, rr, &createResp)
	id := createResp.Patch.Entries[0].ID

	rr = doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, entryRequest{
		Name: "Wash 1", Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "gdtf", Attribute: "Pan"},
			"2": {Source: "gdtf", Attribute: "Pan"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var updateResp patchResponse // fresh struct — see the sibling test's comment on why
	mustUnmarshal(t, rr, &updateResp)
	if len(updateResp.Patch.Entries[0].ChannelFunctions) != 2 {
		t.Fatalf("expected replaced ChannelFunctions with 2 entries, got %+v", updateResp.Patch.Entries[0].ChannelFunctions)
	}
	if updateResp.Patch.Entries[0].ChannelFunctions[1].Attribute != "Pan" {
		t.Errorf("expected replacement to take effect: %+v", updateResp.Patch.Entries[0].ChannelFunctions)
	}
	// Confirm the OLD Dimmer function at offset 1 is really gone, not
	// merged/left over from the create response's map (the same stale-map
	// risk the sibling test's comment explains) — offset 1 now means Pan,
	// and there must be exactly 2 keys total (1 and 2), nothing else.
	if _, stillDimmer := updateResp.Patch.Entries[0].ChannelFunctions[1]; stillDimmer && updateResp.Patch.Entries[0].ChannelFunctions[1].Attribute == "Dimmer" {
		t.Errorf("stale Dimmer data survived the replace: %+v", updateResp.Patch.Entries[0].ChannelFunctions)
	}
}

// TestPatchImport_MergeAtSameAddressReplacesChannelFunctions guards
// patchimport.go's handlePatchImport merge path: an MVR/GDTF re-import
// landing on the same (universe, startAddress) as an existing entry must
// refresh that entry's ChannelFunctions (Task 1's "wire the import path"),
// same as it already refreshes Footprint — while still leaving the
// existing entry's ID/ConfirmedUID/MatchState untouched (patch_test.go's
// TestPatchImportMerge already covers that half).
func TestPatchImport_MergeAtSameAddressReplacesChannelFunctions(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryWithGDTFDimmer("Wash 1", 2, 40))
	var createResp patchResponse
	mustUnmarshal(t, rr, &createResp)

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", importRequest{
		Mode: "merge",
		Entries: []entryRequest{
			{
				Name: "Wash 1", FixtureType: "Reimported", Footprint: 2, Universe: 2, StartAddress: 40,
				ChannelFunctions: map[string]channelFunctionRequest{
					"1": {Source: "gdtf", Attribute: "Pan"},
					"2": {Source: "gdtf", Attribute: "Pan"},
				},
			},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("import merge: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var importResp patchResponse // fresh struct — see the earlier tests' comment on why
	mustUnmarshal(t, rr, &importResp)
	if len(importResp.Patch.Entries) != 1 {
		t.Fatalf("expected merge-in-place (same address), got %+v", importResp.Patch.Entries)
	}
	got := importResp.Patch.Entries[0].ChannelFunctions
	if len(got) != 2 || got[1].Attribute != "Pan" || got[2].Attribute != "Pan" {
		t.Errorf("merge import did not refresh ChannelFunctions: %+v", got)
	}
	if got[1].Attribute == "Dimmer" {
		t.Error("stale pre-merge Dimmer data survived")
	}
}
