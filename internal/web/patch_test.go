package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestSavedPatchCatalogKeepsIndependentShows(t *testing.T) {
	h := newHarness(t)
	h.srv.SetPatchStorePath(filepath.Join(t.TempDir(), "benny512-patch.json"))

	// The pre-catalog/default patch remains a valid first show.
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", newPatchRequest{Name: "Show A"})
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "A fixture", Universe: 0, StartAddress: 1, Footprint: 10})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patches", newPatchRequest{Name: "Show B"})
	if rr.Code != http.StatusOK {
		t.Fatalf("create Show B: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	if !created.Active || created.Patch.Name != "Show B" || len(created.Patch.Entries) != 0 {
		t.Fatalf("created show = %+v", created)
	}
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "B fixture", Universe: 0, StartAddress: 20, Footprint: 10})

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patches", nil)
	var catalog patchCatalogResponse
	mustUnmarshal(t, rr, &catalog)
	if len(catalog.Patches) != 2 {
		t.Fatalf("catalog = %+v, want two shows", catalog)
	}
	var showBID string
	for _, ref := range catalog.Patches {
		if ref.Name == "Show B" {
			showBID = ref.ID
			if !ref.Active {
				t.Fatal("Show B should be active after creation")
			}
		}
	}
	if showBID == "" {
		t.Fatalf("Show B missing from catalog: %+v", catalog.Patches)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patches/default/load", nil)
	var loaded patchResponse
	mustUnmarshal(t, rr, &loaded)
	if loaded.Patch.Name != "Show A" || len(loaded.Patch.Entries) != 1 || loaded.Patch.Entries[0].Name != "A fixture" {
		t.Fatalf("loaded Show A = %+v", loaded.Patch)
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patches/"+showBID+"/load", nil)
	mustUnmarshal(t, rr, &loaded)
	if loaded.Patch.Name != "Show B" || len(loaded.Patch.Entries) != 1 || loaded.Patch.Entries[0].Name != "B fixture" {
		t.Fatalf("loaded Show B = %+v", loaded.Patch)
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

func TestRDMDiagnosticsKeepsMeaningfulZeroCounters(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/diagnostics/rdm", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("diagnostics status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, key := range []string{"\"ackTimerCollectHits\":0", "\"ackTimerReissues\":0", "\"ackTimerCollects\":0"} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Errorf("diagnostics response omitted meaningful zero %s: %s", key, rr.Body.String())
		}
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

// TestFixAllResponse_AppliedZeroMarshalsExplicit guards fixAllResponse.
// Applied directly at the JSON-bytes level: patch.js's onFixAll reads
// `result.applied` with no `|| 0` fallback to render "fixed N of M", so a
// Confirm=true call where every SET attempt failed (Applied legitimately
// 0, distinct from a Confirm=false preview) must still marshal an explicit
// "applied":0 — an omitted key would render as "fixed undefined of M".
// Asserting the struct field equals 0 (as the preview-branch check above
// does) is vacuous against the `omitempty` bug; this checks the bytes.
func TestFixAllResponse_AppliedZeroMarshalsExplicit(t *testing.T) {
	fa := fixAllResponse{NeedsConfirm: false, Count: 2, Items: []fixAllItemJSON{}, Applied: 0}
	data, err := json.Marshal(fa)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"applied":0`) {
		t.Errorf("marshalled fix-all response missing \"applied\":0; got %s", got)
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

// TestRigCheckPattern_StartStatusStopViaREST is the stage 2 test-pattern
// engine's end-to-end REST contract: start a pattern over a scope, read its
// live status, and confirm Stop applies to it exactly like the classic walk.
func TestRigCheckPattern_StartStatusStopViaREST(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Dimmer 1", Universe: pa.RawValue(), StartAddress: 1, Footprint: 1, Position: "US Truss 1",
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "gdtf", Attribute: "Dimmer", ChannelSets: []channelSetRequest{}},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "position", Position: "US Truss 1", Kind: "dimmer_sine", RateHz: 1, Max: 255,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if !st.Running || st.Kind != "dimmer_sine" || st.TotalScope != 1 || st.AppliedCount != 1 || st.SkippedCount != 0 {
		t.Fatalf("pattern status after start = %+v", st)
	}
	if len(st.Entries) != 1 || !st.Entries[0].Applied {
		t.Fatalf("Entries = %+v, want one Applied entry", st.Entries)
	}

	h.clock.Advance(300 * time.Millisecond)

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern status: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if !st.Running || st.ElapsedMS < 300 {
		t.Fatalf("pattern status after advance = %+v", st)
	}

	// Stop (the EXISTING classic endpoint) must apply to the pattern too —
	// there is no separate pattern-stop endpoint by design.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/stop", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	mustUnmarshal(t, rr, &st)
	if st.Running {
		t.Error("expected Running=false after POST /rigcheck/stop")
	}
	if st.LastEndReason != "manual" {
		t.Errorf("LastEndReason = %q, want %q", st.LastEndReason, "manual")
	}
}

// TestRigCheckPattern_JSONShape_NoOmittedZeros pins this codebase's hard
// JSON rules directly on the wire: a numeric/boolean field whose zero is
// real data must still appear in the marshalled bytes, normalized defaults
// must be echoed as their resolved values, and Entries must be
// `[]`, never `null`, for a pattern that (deliberately, via an empty
// selection scope) applies to nothing.
func TestRigCheckPattern_JSONShape_NoOmittedZeros(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "No functions", Universe: pa.RawValue(), StartAddress: 1, Footprint: 1})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "all", Kind: "dimmer_snap", // fields left at zero to exercise server defaults and zero preservation
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"min":0`, `"max":255`, `"value":0`, `"on":false`,
		`"appliedCount":0`, `"skippedCount":1`, `"inferredCount":0`, `"missingDetailCount":0`,
		`"entries":[{`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pattern status JSON missing %s; got %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("pattern status JSON must never contain null: %s", body)
	}

	// A scope that resolves to zero entries must still emit `"entries":[]`
	// (an empty array), never `null`.
	rr2 := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "position", Position: "Nowhere", Kind: "dimmer_sine",
	})
	if rr2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("pattern start over an empty scope: status=%d body=%s, want 422", rr2.Code, rr2.Body.String())
	}
}

// TestRigCheckPattern_AdjustAndValidation exercises AdjustPattern's REST
// surface and the unknown-Kind/no-pattern-running error paths.
func TestRigCheckPattern_AdjustAndValidation(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Mover", Universe: pa.RawValue(), StartAddress: 1, Footprint: 4,
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "gdtf", Attribute: "Pan", ChannelSets: []channelSetRequest{}},
			"3": {Source: "gdtf", Attribute: "Tilt", ChannelSets: []channelSetRequest{}},
		},
	})

	// Unknown kind -> 400, nothing started.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{ScopeKind: "all", Kind: "not_a_kind"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}

	// Adjust with nothing running -> 409.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/adjust", patternAdjustRequest{Target: "pan_max"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("adjust with no pattern running: status=%d body=%s, want 409", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{ScopeKind: "all", Kind: "move_extreme", Target: "pan_max"})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern start: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/adjust", patternAdjustRequest{Target: "tilt_min"})
	if rr.Code != http.StatusOK {
		t.Fatalf("adjust: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.Target != "tilt_min" {
		t.Errorf("Target after adjust = %q, want tilt_min", st.Target)
	}

	// A classic rig-check mutator against a running pattern -> 409, not a
	// silent no-op.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/level", rigCheckLevelRequest{Level: 100})
	if rr.Code != http.StatusConflict {
		t.Fatalf("classic level change while pattern running: status=%d body=%s, want 409", rr.Code, rr.Body.String())
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

// TestPatchExport_UniverseDisplayBase covers the export-side half of the
// universe-numbering-base bug: the collision banner (client-composed, see
// patch.js's composeFindingMessage) and the Patch table both show universe
// N+base for a canonical entry in universe N, and the TXT export a tech
// downloads and carries to the console must say the exact same number —
// for both the entry listing and the collision finding line — not the raw
// canonical value. GET /api/patch/export?format=json, by contrast, is wire
// data (round-trips back into the app) and must stay canonical regardless
// of the display setting.
func TestPatchExport_UniverseDisplayBase(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Practical 1", Universe: 5, StartAddress: 100, Footprint: 10})
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Practical 2", Universe: 5, StartAddress: 105, Footprint: 10})

	for _, base := range []int{0, 1} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", Settings{PollIntervalMS: 3000, CaptureLimit: 1000, TimeoutProfiles: map[string]string{}, UniverseBase: base})
		if rr.Code != http.StatusOK {
			t.Fatalf("POST settings base=%d: status=%d body=%s", base, rr.Code, rr.Body.String())
		}

		rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/export?format=txt", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("export txt: status=%d", rr.Code)
		}
		body := rr.Body.String()
		wantDisplayed := 5 + base
		wantEntryLine := "universe " + itoa(wantDisplayed) + ","
		if !bytes.Contains(rr.Body.Bytes(), []byte(wantEntryLine)) {
			t.Errorf("base=%d: entry listing missing %q, got:\n%s", base, wantEntryLine, body)
		}
		wantFindingSuffix := "in universe " + itoa(wantDisplayed)
		if !bytes.Contains(rr.Body.Bytes(), []byte(wantFindingSuffix)) {
			t.Errorf("base=%d: collision finding line missing %q, got:\n%s", base, wantFindingSuffix, body)
		}
		// The other base's number must never appear as a universe number —
		// the exact confusion this whole class of bug produces (banner and
		// table disagreeing about which universe a fixture is in).
		wrongDisplayed := 5 + (1 - base)
		wrongEntryLine := "universe " + itoa(wrongDisplayed) + ","
		if bytes.Contains(rr.Body.Bytes(), []byte(wrongEntryLine)) {
			t.Errorf("base=%d: entry listing states the wrong-base universe number %q, got:\n%s", base, wrongEntryLine, body)
		}

		// JSON export is canonical wire data — always universe 5, never
		// shifted by the display setting.
		rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/export?format=json", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("export json: status=%d", rr.Code)
		}
		var doc patchExportDoc
		mustUnmarshal(t, rr, &doc)
		for _, e := range doc.Patch.Entries {
			if e.Universe != 5 {
				t.Errorf("base=%d: JSON export entry universe = %d, want canonical 5", base, e.Universe)
			}
		}
		for _, f := range doc.Findings {
			if f.Kind == patch.KindOverlap && f.Universe != 5 {
				t.Errorf("base=%d: JSON export finding universe = %d, want canonical 5", base, f.Universe)
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

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

// TestChannelFunctionImport_PreservesGDTFDefaultAndHighlight is the
// regression test for the import path silently dropping every GDTF
// Default/Highlight the browser parsed: channelFunctionRequest mirrors
// patch.ChannelFunction field-for-field and channelFunctionsFromRequest
// copies fields explicitly, so a field added to the model but not to the
// request struct is dropped with no error anywhere.
//
// The value under test is deliberately a ZERO default (a shutter resting
// closed, a dimmer resting dark — the single most common real value), which
// is exactly the case a `Default != 0` heuristic would get wrong: only the
// hasDefault flag distinguishes "the file said 0" from "the file said
// nothing". Asserted on the MARSHALLED JSON BYTES of GET /api/patch, per
// this project's serialization-test rule.
func TestChannelFunctionImport_PreservesGDTFDefaultAndHighlight(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Strobe 1", Universe: 0, StartAddress: 1, Footprint: 2,
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {
				Source: "gdtf", Attribute: "Shutter1", FunctionName: "Shutter1",
				ChannelSets:      []channelSetRequest{{Name: "Closed", DMXFrom: 0}, {Name: "Open", DMXFrom: 32}},
				HasDefault:       true,
				Default:          0,
				DefaultByteCount: 1,
				HasHighlight:     true,
				Highlight:        0,
				// 2 here proves ByteCount is carried independently of the
				// value it describes, not derived from it.
				HighlightByteCount: 2,
			},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	body := rr.Body.String()
	for _, want := range []string{
		`"hasDefault":true`, `"default":0`, `"defaultByteCount":1`,
		`"hasHighlight":true`, `"highlight":0`, `"highlightByteCount":2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /api/patch JSON missing %s — the GDTF resting value was dropped on import; got %s", want, body)
		}
	}
}

// jdcLikeEntryRequest is a JDC-1-shaped fixture on the wire: dimmer, a
// shutter with named GDTF ChannelSets, and a tilt with a stated GDTF Default.
func jdcLikeEntryRequest(name string, universe uint16, addr uint16) entryRequest {
	return entryRequest{
		Name: name, Universe: universe, StartAddress: addr, Footprint: 3, Position: "US Truss 1",
		ChannelFunctions: map[string]channelFunctionRequest{
			"1": {Source: "gdtf", Attribute: "Dimmer", FunctionName: "Dimmer", ChannelSets: []channelSetRequest{}, HasDefault: true, Default: 0, DefaultByteCount: 1},
			"2": {Source: "gdtf", Attribute: "Shutter1", FunctionName: "Shutter", ChannelSets: []channelSetRequest{
				{Name: "Closed", DMXFrom: 0}, {Name: "Open", DMXFrom: 32}, {Name: "Strobe", DMXFrom: 64},
			}},
			"3": {Source: "gdtf", Attribute: "Tilt", FunctionName: "Tilt", ChannelSets: []channelSetRequest{}, HasDefault: true, Default: 128, DefaultByteCount: 1},
		},
	}
}

// TestRigCheckPattern_SelectionAndOutputAreIndependent is the owner's
// workflow over REST: select tests with output off (nothing moves), start
// output, toggle a second test live, stop — and the selection is still there.
func TestRigCheckPattern_SelectionAndOutputAreIndependent(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))
	off := false
	on := true

	// 1. Select a test with output OFF.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "all", OutputEnabled: &off,
		Tests: []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("select with output off: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.OutputEnabled || st.Running || st.SelectedCount != 1 {
		t.Fatalf("after select-only: outputEnabled=%v running=%v selectedCount=%d, want false,false,1", st.OutputEnabled, st.Running, st.SelectedCount)
	}

	// 2. Start output via the adjust endpoint's outputEnabled flag.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/adjust", patternAdjustRequest{OutputEnabled: &on})
	mustUnmarshal(t, rr, &st)
	if !st.OutputEnabled || st.SelectedCount != 1 {
		t.Fatalf("after start: %+v", st)
	}

	// 3. Toggle a SECOND test on while output flows — must not 409.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/adjust", patternAdjustRequest{
		Test: &patternTestRequest{Kind: "move_extreme", Target: "tilt_max"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("toggling a test live: status=%d body=%s, want 200 — configuration is never gated on output", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != 2 {
		t.Fatalf("selectedCount = %d after toggling a second test on, want 2", st.SelectedCount)
	}

	// 4. Stop stops OUTPUT and deselects nothing.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/stop", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	mustUnmarshal(t, rr, &st)
	if st.OutputEnabled {
		t.Error("stop must disable output")
	}
	if st.SelectedCount != 2 {
		t.Errorf("selectedCount = %d after stop, want 2 — stop stops output and deselects nothing", st.SelectedCount)
	}
	if st.LastEndReason != "manual" {
		t.Errorf("lastEndReason = %q, want manual", st.LastEndReason)
	}
}

// TestRigCheckPattern_StackedJSONShape pins the whole new wire contract on
// the MARSHALLED BYTES: every new field present, every zero explicit, every
// slice an array and never null.
func TestRigCheckPattern_StackedJSONShape(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 2", pa.RawValue(), 10))

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "all",
		Tests: []patternTestRequest{
			// Every numeric/boolean parameter deliberately left at zero. Max
			// must be normalized by the server to the usable full range.
			{Kind: "ballyhoo"},
			{Kind: "move_extreme", Target: "tilt_max"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"outputEnabled":true`, `"running":true`, `"selectedCount":2`,
		`"tests":[{`, `"id":"move_extreme:tilt_max"`, `"id":"ballyhoo"`,
		`"waveform":"sine"`, `"offsetMin":0`, `"offsetMax":0`,
		`"min":0`, `"max":255`, `"value":0`, `"on":false`, `"direction":""`,
		`"phaseDegrees":0`, `"applied":true`, `"inferred":false`, `"detailMissing":false`,
		`"contested":[{`, `"tests":["move_extreme:tilt_max","ballyhoo"]`,
		`"baseState":{"isolate":false`, `"defaultsKnownCount":`, `"defaultsUnknownCount":`,
		`"dimmerDrivenCount":2`, `"shutterOpenedCount":2`, `"shutterUnknownEntries":[]`,
		`"available":[{`, `"labelFromGdtf":false`, `"fixtureCount":2`,
		`"elapsedMs":0`, `"totalScope":2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pattern status JSON missing %s;\ngot %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("pattern status JSON must never contain null: %s", body)
	}

	// The canonical composition order must be reflected in `tests` order —
	// move_extreme sorts before ballyhoo, whatever order they were sent in.
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if len(st.Tests) != 2 || st.Tests[0].ID != "move_extreme:tilt_max" || st.Tests[1].ID != "ballyhoo" {
		t.Errorf("tests order = %v, want the canonical [move_extreme:tilt_max ballyhoo] regardless of request order", st.Tests)
	}
}

// TestRigCheckPattern_PhaseAndWaveformOverREST covers the two new per-test
// parameters end to end, including the explicit rejection of a phase spread
// on a static kind.
func TestRigCheckPattern_PhaseAndWaveformOverREST(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	for i := 0; i < 4; i++ {
		doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC", pa.RawValue(), uint16(1+i*3)))
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "all",
		Tests:     []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255, Waveform: "snap", OffsetMin: 0, OffsetMax: 360}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"waveform":"snap"`, `"offsetMax":360`, `"phaseDegrees":90`, `"phaseDegrees":270`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}

	// A phase spread on a static kind is a 400, not a silent no-op.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", patternStartRequest{
		ScopeKind: "all",
		Tests:     []patternTestRequest{{Kind: "move_extreme", Target: "tilt_max", OffsetMax: 180}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("phase on a static kind: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
}

// goboFrostEntryRequest is a mover with TWO distinct GDTF Frost functions
// and one gobo wheel that both indexes and rotates — the shape
// patch.AvailableTests exists to enumerate honestly rather than guess at
// (see testpattern.go's "enumeration instead of guessing" doc section).
// Frost1 carries a GDTF ChannelFunction Name and Frost2 deliberately does
// not, so the labelFromGdtf provenance flag has both states to report.
func goboFrostEntryRequest(name string, universe uint16, addr uint16) entryRequest {
	return entryRequest{
		Name: name, Universe: universe, StartAddress: addr, Footprint: 6, Position: "DS Truss",
		ChannelFunctions: map[string]channelFunctionRequest{
			"0": {Source: "gdtf", Attribute: "Dimmer", FunctionName: "Dimmer", ChannelSets: []channelSetRequest{}, HasDefault: true, Default: 0, DefaultByteCount: 1},
			"1": {Source: "gdtf", Attribute: "Frost1", FunctionName: "Light Frost", ChannelSets: []channelSetRequest{}},
			"2": {Source: "gdtf", Attribute: "Frost2", ChannelSets: []channelSetRequest{}},
			"3": {Source: "gdtf", Attribute: "Gobo1", FunctionName: "Gobo Wheel 1", ChannelSets: []channelSetRequest{
				{Name: "Open", DMXFrom: 0}, {Name: "Gobo 1", DMXFrom: 10}, {Name: "Gobo 2", DMXFrom: 20},
			}},
			"4": {Source: "gdtf", Attribute: "Gobo1WheelSpin", FunctionName: "Gobo 1 Rotate", ChannelSets: []channelSetRequest{}},
		},
	}
}

// TestRigCheckPatternEndpoints_SelectionOutlivesOutput drives the five
// single-purpose endpoints through the owner's workflow and pins the two
// safety rules that motivated the split: selecting a SECOND test while
// output is flowing takes effect with no stop in between, and stopping
// output deselects nothing — a later status GET still lists both tests.
func TestRigCheckPatternEndpoints_SelectionOutlivesOutput(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))

	// Scope + one test, output still off.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", patternTestsRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "all"},
		Tests:              []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/tests: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.OutputEnabled || st.SelectedCount != 1 || st.TotalScope != 1 {
		t.Fatalf("after pattern/tests: outputEnabled=%v selectedCount=%d totalScope=%d, want false,1,1",
			st.OutputEnabled, st.SelectedCount, st.TotalScope)
	}

	// Start output.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", patternOutputRequest{Enabled: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/output start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if !st.OutputEnabled || !st.Running {
		t.Fatalf("after pattern/output{enabled:true}: %+v", st)
	}

	// Select a SECOND test with output flowing. No stop, no 409 — and the
	// response itself must already show it, so a UI re-rendering from this
	// snapshot alone is correct.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/select", patternSelectRequest{
		Test: patternTestRequest{Kind: "move_extreme", Target: "tilt_max"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/select while output flows: status=%d body=%s, want 200 — configuration is never gated on output",
			rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != 2 || !st.OutputEnabled {
		t.Fatalf("after selecting a second test live: selectedCount=%d outputEnabled=%v, want 2,true", st.SelectedCount, st.OutputEnabled)
	}
	if len(st.Tests) != 2 {
		t.Fatalf("tests = %+v, want both tests in the SAME response that made the change", st.Tests)
	}

	// Stop OUTPUT. The selection must survive completely.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", patternOutputRequest{Enabled: false})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/output stop: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if st.OutputEnabled {
		t.Error("pattern/output{enabled:false} must stop output")
	}
	if st.SelectedCount != 2 {
		t.Errorf("selectedCount = %d immediately after stop, want 2 — stop stops output and deselects nothing", st.SelectedCount)
	}

	// ...and a subsequent independent status GET still lists both.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != 2 || len(st.Tests) != 2 {
		t.Fatalf("GET after stop: selectedCount=%d tests=%d, want 2,2", st.SelectedCount, len(st.Tests))
	}
	gotIDs := []string{st.Tests[0].ID, st.Tests[1].ID}
	if gotIDs[0] != "dimmer_sine" || gotIDs[1] != "move_extreme:tilt_max" {
		t.Errorf("tests after stop = %v, want the canonical [dimmer_sine move_extreme:tilt_max]", gotIDs)
	}
	if st.LastEndReason != "manual" {
		t.Errorf("lastEndReason = %q, want manual", st.LastEndReason)
	}

	// Output can be re-enabled with no re-selection at all.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", patternOutputRequest{Enabled: true})
	mustUnmarshal(t, rr, &st)
	if !st.OutputEnabled || st.SelectedCount != 2 {
		t.Errorf("restart: outputEnabled=%v selectedCount=%d, want true,2", st.OutputEnabled, st.SelectedCount)
	}
}

// TestRigCheckPatternSelect_DisableIsExplicitOnTheWire pins the
// deselect path against this codebase's no-`omitempty`-on-a-real-false rule,
// on the MARSHALLED REQUEST BYTES: a toggle-off must travel as
// "enabled":false. An enabled flag that vanished when false would be read by
// the server as "select it" and the test would never turn off.
func TestRigCheckPatternSelect_DisableIsExplicitOnTheWire(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))

	doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", patternTestsRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "all"},
		Tests:              []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255}, {Kind: "move_extreme", Target: "tilt_max"}},
	})

	off := false
	body, err := json.Marshal(patternSelectRequest{
		Test: patternTestRequest{Kind: "dimmer_sine", RateHz: 1, Max: 255}, Enabled: &off,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("deselect request marshalled to %s, want it to carry \"enabled\":false — a false that vanishes reads as \"select\"", body)
	}

	// Post those exact bytes, not a struct, so the wire form is what is tested.
	rr := doRaw(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/select", string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("deselect: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != 1 || len(st.Tests) != 1 || st.Tests[0].ID != "move_extreme:tilt_max" {
		t.Fatalf("after deselect: selectedCount=%d tests=%+v, want only move_extreme:tilt_max left", st.SelectedCount, st.Tests)
	}

	// The same request WITHOUT the enabled key means "select" — which is
	// exactly why the false above has to be on the wire.
	rr = doRaw(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/select",
		`{"test":{"kind":"dimmer_sine","rateHz":1,"max":255}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("select: status=%d body=%s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != 2 {
		t.Fatalf("omitting enabled = %d selected, want 2 (omitted means select)", st.SelectedCount)
	}
}

// TestRigCheckPatternEndpoints_AvailableTestsEnumerated asserts the
// available-test enumeration reaches the wire for a scope whose fixture has
// TWO frost functions and a gobo wheel: one frost test per GDTF Frost*
// function, one gobo step test and one gobo rotate test for the wheel, each
// labelled from GDTF and each honest about whether the label came from GDTF.
func TestRigCheckPatternEndpoints_AvailableTestsEnumerated(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", goboFrostEntryRequest("Spot 1", pa.RawValue(), 1))

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", patternTestsRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "all"},
		Tests:              []patternTestRequest{},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/tests: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"available":[{`,
		`"id":"frost:Frost1"`, `"label":"Light Frost"`, `"attribute":"Frost1"`, `"labelFromGdtf":true`,
		`"id":"frost:Frost2"`, `"label":"Frost2"`,
		`"id":"gobo_step:Gobo1"`, `"label":"Gobo Wheel 1"`,
		`"id":"gobo_rotate:Gobo1"`, `"label":"Gobo 1 Rotate"`, `"attribute":"Gobo1WheelSpin"`,
		`"fixtureCount":1`, `"target":"Frost1"`,
		`"selectedCount":0`, `"tests":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pattern status JSON missing %s;\ngot %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("pattern status JSON must never contain null: %s", body)
	}

	// The same enumeration must be on a plain status GET too — that is the
	// call a UI polls, and it is what drives the toggle buttons.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	ids := map[string]availableTestJSON{}
	for _, a := range st.Available {
		ids[a.ID] = a
	}
	for _, want := range []string{"frost:Frost1", "frost:Frost2", "gobo_step:Gobo1", "gobo_rotate:Gobo1"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("GET status available[] missing %q; got %+v", want, st.Available)
		}
	}
	if a := ids["frost:Frost2"]; a.LabelFromGDTF {
		t.Errorf("Frost2 has no GDTF ChannelFunction Name, so labelFromGdtf must be false; got %+v", a)
	}

	// And every enumerated test must actually be selectable by the id's own
	// (kind, target) — the enumeration is the UI's only source of these.
	for _, a := range st.Available {
		rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/select", patternSelectRequest{
			Test: patternTestRequest{Kind: a.Kind, Target: a.Target},
		})
		if rr.Code != http.StatusOK {
			t.Fatalf("selecting enumerated test %q: status=%d body=%s", a.ID, rr.Code, rr.Body.String())
		}
	}
	want := len(st.Available)
	mustUnmarshal(t, rr, &st)
	if st.SelectedCount != want {
		t.Errorf("selectedCount = %d after selecting every available test, want %d", st.SelectedCount, want)
	}
}

// TestRigCheckPatternEndpoints_ScopeAndIsolate covers the two remaining
// single-purpose endpoints, including that each is legal while output flows
// and that each answers with the full snapshot.
func TestRigCheckPatternEndpoints_ScopeAndIsolate(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 2", pa.RawValue(), 10))

	doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", patternTestsRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "all"},
		Tests:              []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255}},
	})
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", patternOutputRequest{Enabled: true})

	// Narrow the scope while output is flowing.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/scope", patternScopeRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "position", Position: "US Truss 1"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/scope: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var st patternStatusJSON
	mustUnmarshal(t, rr, &st)
	if st.TotalScope != 2 || st.SelectedCount != 1 || !st.OutputEnabled {
		t.Fatalf("after scope change: totalScope=%d selectedCount=%d outputEnabled=%v, want 2,1,true",
			st.TotalScope, st.SelectedCount, st.OutputEnabled)
	}
	if st.ScopeKind != "position" || st.ScopePosition != "US Truss 1" || st.ScopeUniverse != 0 || len(st.ScopeEntryIDs) != 0 {
		t.Fatalf("scope readback = kind=%q position=%q universe=%d ids=%v, want position/US Truss 1/0/[]",
			st.ScopeKind, st.ScopePosition, st.ScopeUniverse, st.ScopeEntryIDs)
	}

	// A scope resolving to no entries is a 422, and leaves the old scope be.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/scope", patternScopeRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "position", Position: "Nowhere"},
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty scope: status=%d body=%s, want 422", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	mustUnmarshal(t, rr, &st)
	if st.ScopeKind != "position" || st.ScopePosition != "US Truss 1" {
		t.Fatalf("failed scope change replaced server readback: %+v", st)
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/scope", patternScopeRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "not_a_kind"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad scopeKind: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}

	// Isolate flips on its own, on the wire, with output still flowing.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/isolate", patternIsolateRequest{Isolate: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/isolate: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if b := rr.Body.String(); !strings.Contains(b, `"isolate":true`) {
		t.Errorf("isolate on: body missing \"isolate\":true; got %s", b)
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/isolate", patternIsolateRequest{Isolate: false})
	if b := rr.Body.String(); !strings.Contains(b, `"isolate":false`) {
		t.Errorf("isolate off: body missing \"isolate\":false — a false that vanishes is unreadable; got %s", b)
	}
	mustUnmarshal(t, rr, &st)
	if !st.OutputEnabled || st.SelectedCount != 1 {
		t.Errorf("isolate must not disturb output or selection: %+v", st)
	}
}

// TestRigCheckPatternAdjust_AmbiguousIsAConflict pins the status code
// patternAdjustRequest's own doc comment promises: the legacy
// single-selected-test parameter replace has no single test to mean when
// several are selected, which is a state conflict (409), not a malformed
// request (400).
func TestRigCheckPatternAdjust_AmbiguousIsAConflict(t *testing.T) {
	h := newHarness(t)
	pa := mustPort(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", pa.RawValue(), 1))
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", patternTestsRequest{
		patternScopeFields: patternScopeFields{ScopeKind: "all"},
		Tests:              []patternTestRequest{{Kind: "dimmer_sine", RateHz: 1, Max: 255}, {Kind: "move_extreme", Target: "tilt_max"}},
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/adjust", patternAdjustRequest{RateHz: 2})
	if rr.Code != http.StatusConflict {
		t.Fatalf("legacy adjust with two tests selected: status=%d body=%s, want 409", rr.Code, rr.Body.String())
	}
}
