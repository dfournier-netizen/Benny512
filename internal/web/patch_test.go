package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"benny512/internal/artnet"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

func TestPatchCRUD(t *testing.T) {
	h := newHarness(t)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	if resp.Active {
		t.Fatal("expected no active patch before any create")
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Wash 1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Footprint: 20, Universe: 0, StartAddress: 1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if !resp.Active || len(resp.Patch.Entries) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	id := resp.Patch.Entries[0].ID
	if id == "" {
		t.Fatal("expected a server-assigned entry ID")
	}

	// update
	rr = doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, entryRequest{
		Name: "Wash 1 renamed", FixtureType: "Chauvet Rogue Outcast 2X Wash", Footprint: 20, Universe: 0, StartAddress: 5,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update entry: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if resp.Patch.Entries[0].Name != "Wash 1 renamed" || resp.Patch.Entries[0].StartAddress != 5 {
		t.Fatalf("entry not updated: %+v", resp.Patch.Entries[0])
	}

	// add a second entry, then reorder
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 2", Universe: 0, StartAddress: 30, Footprint: 20})
	mustUnmarshal(t, rr, &resp)
	id2 := resp.Patch.Entries[1].ID

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reorder", reorderRequest{Order: []string{id2, id}})
	if rr.Code != http.StatusOK {
		t.Fatalf("reorder: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if resp.Patch.Entries[0].ID != id2 || resp.Patch.Entries[1].ID != id {
		t.Fatalf("reorder did not apply: %+v", resp.Patch.Entries)
	}

	// delete
	rr = doJSON(t, h.srv.Handler(), "DELETE", "/api/patch/entries/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if len(resp.Patch.Entries) != 1 || resp.Patch.Entries[0].ID != id2 {
		t.Fatalf("delete did not apply: %+v", resp.Patch.Entries)
	}

	// deleting an unknown id 404s
	rr = doJSON(t, h.srv.Handler(), "DELETE", "/api/patch/entries/nope", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("delete unknown = %d, want 404", rr.Code)
	}
}

func TestPatchCollisions(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "A", Universe: 0, StartAddress: 1, Footprint: 10})
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "B", Universe: 0, StartAddress: 5, Footprint: 10})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/collisions", nil)
	var findings []patch.Finding
	mustUnmarshal(t, rr, &findings)
	found := false
	for _, f := range findings {
		if f.Kind == patch.KindOverlap {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an overlap finding, got %+v", findings)
	}
}

// seedPatchFixture registers one discoverable RDM fixture (with DEVICE_INFO
// footprint/manufacturer/model cached in the registry, mirroring what the
// Devices screen's classification sweep would have already fetched) and
// resolves its live DMX_START_ADDRESS via a scripted responder — the shape
// buildDiscoveredDevices needs.
func seedPatchFixture(t *testing.T, h *testHarness, uid rdm.UID, universe artnet.PortAddress, liveAddr uint16) {
	t.Helper()
	node := h.seedNode(t)
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: universe}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDDMXStartAddress:
			if msg.CommandClass == rdm.SetCommand {
				liveAddr = uint16(msg.ParameterData[0])<<8 | uint16(msg.ParameterData[1])
				return nil, false, 0, 0
			}
			return []byte{byte(liveAddr >> 8), byte(liveAddr)}, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})
}

func TestPatchReconcile_MatchedAndMissing(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	pa := mustPort(t)
	seedPatchFixture(t, h, uid, pa, 1)

	// entry a matches the device; entry b matches nothing.
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 1", Universe: pa.RawValue(), StartAddress: 1, Footprint: 1})
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Nothing here", Universe: pa.RawValue(), StartAddress: 200, Footprint: 1})

	rr := h.runHTTPAsync(t, "GET", "/api/patch/reconcile", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reconcile: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rep patch.Report
	mustUnmarshal(t, rr, &rep)

	var sawMatched, sawMissing bool
	for _, row := range rep.Rows {
		if row.Status == patch.StatusMatched && row.DeviceUID == uid.String() {
			sawMatched = true
		}
		if row.Status == patch.StatusMissing {
			sawMissing = true
		}
	}
	if !sawMatched {
		t.Errorf("expected a Matched row, got %+v", rep.Rows)
	}
	if !sawMissing {
		t.Errorf("expected a Missing row, got %+v", rep.Rows)
	}
}

func TestPatchReconcileFix_OneTap(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	pa := mustPort(t)
	seedPatchFixture(t, h, uid, pa, 50) // device is live at 50, patch says 1

	var resp patchResponse
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 1", Universe: pa.RawValue(), StartAddress: 1, Footprint: 1})
	mustUnmarshal(t, rr, &resp)
	id := resp.Patch.Entries[0].ID

	rr = h.runHTTPAsync(t, "POST", "/api/patch/reconcile/"+id+"/fix", reconcileConfirmRequest{DeviceUID: uid.String()})
	if rr.Code != http.StatusOK {
		t.Fatalf("fix: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if resp.Patch.Entries[0].ConfirmedUID != uid.String() || resp.Patch.Entries[0].MatchState != patch.MatchStateConfirmed {
		t.Fatalf("fix did not persist confirmation: %+v", resp.Patch.Entries[0])
	}

	// Re-reconcile: the device should now report address 1 (fixed) and
	// match cleanly via the confirmed pairing.
	rr = h.runHTTPAsync(t, "GET", "/api/patch/reconcile", nil)
	var rep patch.Report
	mustUnmarshal(t, rr, &rep)
	for _, row := range rep.Rows {
		if row.EntryID == id {
			if row.Status != patch.StatusMatched {
				t.Errorf("row after fix = %+v, want Matched", row)
			}
		}
	}
}

func TestPatchReconcileFixAll_PreviewThenApply(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	pa := mustPort(t)
	seedPatchFixture(t, h, uid, pa, 99) // live at 99

	var resp patchResponse
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Wash 1", FixtureType: "Robe Wash", Universe: pa.RawValue(), StartAddress: 1, Footprint: 1,
	})
	mustUnmarshal(t, rr, &resp)
	id := resp.Patch.Entries[0].ID

	// Manually confirm the pairing (Tier 3) — a confirmed pairing is
	// classified AddressMismatch purely from the live-address comparison,
	// independent of fuzzy corroboration, which keeps this test focused on
	// fix-all's own preview/apply/confirm-count behavior rather than the
	// matcher's scoring (already covered exhaustively in internal/patch).
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/confirm", reconcileConfirmRequest{DeviceUID: uid.String()})

	// Preview must not apply anything.
	rr = h.runHTTPAsync(t, "POST", "/api/patch/reconcile/fix-all", fixAllRequest{Confirm: false})
	var fa fixAllResponse
	mustUnmarshal(t, rr, &fa)
	if !fa.NeedsConfirm {
		t.Fatal("preview call should set NeedsConfirm=true")
	}
	if fa.Applied != 0 {
		t.Fatalf("preview must not apply anything, Applied=%d", fa.Applied)
	}

	// Live device must still be unfixed after a preview-only call.
	rr = h.runHTTPAsync(t, "GET", "/api/patch/reconcile", nil)
	var rep patch.Report
	mustUnmarshal(t, rr, &rep)
	for _, row := range rep.Rows {
		if row.EntryID != "" && row.Status == patch.StatusMatched {
			t.Fatal("preview call must not have fixed the address")
		}
	}

	// Now actually confirm.
	rr = h.runHTTPAsync(t, "POST", "/api/patch/reconcile/fix-all", fixAllRequest{Confirm: true})
	mustUnmarshal(t, rr, &fa)
	if fa.Applied != 1 {
		t.Fatalf("Applied = %d, want 1: %+v", fa.Applied, fa)
	}
}

func TestPatchAdopt_Fresh(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	pa := mustPort(t)
	seedPatchFixture(t, h, uid, pa, 7)

	rr := h.runHTTPAsync(t, "POST", "/api/patch/adopt", adoptRequest{Mode: "fresh"})
	if rr.Code != http.StatusOK {
		t.Fatalf("adopt: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	if len(resp.Patch.Entries) != 1 {
		t.Fatalf("expected 1 adopted entry, got %d", len(resp.Patch.Entries))
	}
	e := resp.Patch.Entries[0]
	if e.ConfirmedUID != uid.String() || e.MatchState != patch.MatchStateConfirmed {
		t.Errorf("adopted entry should be pre-confirmed: %+v", e)
	}
	if e.StartAddress != 7 {
		t.Errorf("StartAddress = %d, want 7 (live value)", e.StartAddress)
	}
}

func TestPatchAdopt_MergeDoesNotClobberEditedFields(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	pa := mustPort(t)
	seedPatchFixture(t, h, uid, pa, 7)

	rr := h.runHTTPAsync(t, "POST", "/api/patch/adopt", adoptRequest{Mode: "fresh"})
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	id := resp.Patch.Entries[0].ID

	// User edits the entry's Position/Notes by hand.
	entry := resp.Patch.Entries[0]
	doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, entryRequest{
		Name: entry.Name, FixtureType: entry.FixtureType, Footprint: entry.Footprint,
		Universe: entry.Universe, StartAddress: entry.StartAddress, Position: "SR Boom 2", Notes: "hand-tuned",
	})

	// Device moves address; merge-adopt again.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	mustUnmarshal(t, rr, &resp)

	rr = h.runHTTPAsync(t, "POST", "/api/patch/reconcile/"+id+"/fix", reconcileConfirmRequest{DeviceUID: uid.String()}) // no-op sanity, keep confirmed
	_ = rr

	rr = h.runHTTPAsync(t, "POST", "/api/patch/adopt", adoptRequest{Mode: "merge"})
	mustUnmarshal(t, rr, &resp)
	if len(resp.Patch.Entries) != 1 {
		t.Fatalf("merge should update the existing entry, not duplicate it: %+v", resp.Patch.Entries)
	}
	if resp.Patch.Entries[0].Position != "SR Boom 2" || resp.Patch.Entries[0].Notes != "hand-tuned" {
		t.Errorf("merge must preserve user-edited fields: %+v", resp.Patch.Entries[0])
	}
}

func TestRigCheck_StartStopViaREST(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "A", Universe: pa.RawValue(), StartAddress: 1, Footprint: 4})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", rigCheckStartRequest{ScopeKind: "all", Mode: "highlight", Level: 200})
	if rr.Code != http.StatusOK {
		t.Fatalf("rigcheck start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st rigCheckStateJSON
	mustUnmarshal(t, rr, &st)
	if !st.Running || st.EntryCount != 1 {
		t.Fatalf("state after start = %+v", st)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/stop", nil)
	mustUnmarshal(t, rr, &st)
	if st.Running {
		t.Error("expected Running=false after stop")
	}

	// Starting with an empty patch must 422, never panic.
	h2 := newHarness(t)
	rr = doJSON(t, h2.srv.Handler(), "POST", "/api/patch/rigcheck/start", rigCheckStartRequest{ScopeKind: "all"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("start with no patch = %d, want 422", rr.Code)
	}
}

func TestPatchExport(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 1", Universe: 0, StartAddress: 1, Footprint: 20})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/export?format=txt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("export txt: status=%d", rr.Code)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd == "" {
		t.Error("expected a Content-Disposition header")
	}
	if len(rr.Body.String()) == 0 {
		t.Error("expected non-empty txt export body")
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/export?format=json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reconcile export json: status=%d", rr.Code)
	}
}

func TestPatchImport_Fresh(t *testing.T) {
	h := newHarness(t)
	// Seed an existing patch that "fresh" import must discard entirely.
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Old", Universe: 0, StartAddress: 1, Footprint: 1})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", importRequest{
		Mode: "fresh",
		Entries: []entryRequest{
			{Name: "Imported A", FixtureType: "Robe MegaPointe", Footprint: 20, Universe: 0, StartAddress: 1},
			{Name: "Imported B", FixtureType: "Robe MegaPointe", Footprint: 20, Universe: 0, StartAddress: 21},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("import fresh: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	if !resp.Active || resp.Patch.Name != "Imported" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Patch.Entries) != 2 {
		t.Fatalf("expected fresh import to replace the old patch with 2 entries, got %+v", resp.Patch.Entries)
	}
	for _, e := range resp.Patch.Entries {
		if e.Name == "Old" {
			t.Fatalf("fresh import must discard the previous patch, still found: %+v", resp.Patch.Entries)
		}
		if e.ID == "" {
			t.Error("expected a server-assigned entry ID")
		}
	}
}

func TestPatchImport_MergeUpdatesExistingByUniverseAndAddress(t *testing.T) {
	h := newHarness(t)

	// Existing entry already reconciled/confirmed against a live device —
	// import-merge must preserve ID/ConfirmedUID/MatchState untouched even
	// though it overwrites the descriptive fields.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Old Name", FixtureType: "Old Type", Mode: "Old Mode", Footprint: 10,
		Universe: 3, StartAddress: 50, Position: "Old Pos", FixtureNumber: "101", Notes: "old notes",
	})
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	id := resp.Patch.Entries[0].ID

	uid := "1900:00000042"
	_, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].ConfirmedUID = uid
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		return nil
	})
	if err != nil {
		t.Fatalf("seed confirmed pairing: %v", err)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", importRequest{
		Mode: "merge",
		Entries: []entryRequest{
			{
				Name: "New Name", FixtureType: "New Type", Mode: "New Mode", Footprint: 16,
				Universe: 3, StartAddress: 50, Position: "New Pos", FixtureNumber: "202", Notes: "new notes",
			},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("import merge: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &resp)
	if len(resp.Patch.Entries) != 1 {
		t.Fatalf("merge on matching (universe,startAddress) must update in place, not duplicate: %+v", resp.Patch.Entries)
	}
	e := resp.Patch.Entries[0]
	if e.ID != id {
		t.Errorf("ID changed: got %q, want %q (must never disturb existing entry ID)", e.ID, id)
	}
	if e.ConfirmedUID != uid || e.MatchState != patch.MatchStateConfirmed {
		t.Errorf("import merge must never disturb existing reconciliation state: %+v", e)
	}
	if e.Name != "New Name" || e.FixtureType != "New Type" || e.Mode != "New Mode" || e.Footprint != 16 ||
		e.Position != "New Pos" || e.FixtureNumber != "202" || e.Notes != "new notes" {
		t.Errorf("descriptive fields not updated from imported entry: %+v", e)
	}
}

func TestPatchImport_MergeAppendsWhenNoAddressMatch(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Existing", Universe: 0, StartAddress: 1, Footprint: 4})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", importRequest{
		Mode: "merge",
		Entries: []entryRequest{
			{Name: "New Fixture", FixtureType: "Chauvet Rogue", Footprint: 8, Universe: 5, StartAddress: 100},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("import merge: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp patchResponse
	mustUnmarshal(t, rr, &resp)
	if len(resp.Patch.Entries) != 2 {
		t.Fatalf("expected the non-matching entry to be appended, got %+v", resp.Patch.Entries)
	}
	var found bool
	for _, e := range resp.Patch.Entries {
		if e.Name == "New Fixture" {
			found = true
			if e.ID == "" {
				t.Error("expected a server-assigned entry ID for the appended entry")
			}
			if e.ConfirmedUID != "" || e.MatchState != patch.MatchStateUnresolved {
				t.Errorf("a freshly imported entry must not carry any RDM identity: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("appended entry not found: %+v", resp.Patch.Entries)
	}
}

func TestPatchImport_InvalidMode(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", importRequest{Mode: "bogus"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
}

func TestPatchImport_MalformedJSON(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("POST", "/api/patch/import", bytes.NewReader([]byte(`{not valid json`)))
	rr := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed json: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
}

func mustUnmarshal(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("unmarshal %s: %v", rr.Body.String(), err)
	}
}
