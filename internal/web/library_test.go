package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"benny512/internal/library"
	"benny512/internal/patch"
)

// seedLibrary installs a couple of known records into h's library store and
// returns the store.
func seedLibrary(t *testing.T, h *testHarness) *library.Store {
	t.Helper()
	st := h.srv.LibraryStore
	st.Upsert(library.Record{
		Manufacturer:           "GLP",
		Model:                  "JDC-1",
		RDMManufacturerID:      0x4C55,
		RDMManufacturerIDKnown: true,
		SupportedPIDs:          []uint16{0x0060},
		Modes: []library.Mode{{
			Name:      "Standard",
			Footprint: 34,
			ChannelFunctions: map[uint16]patch.ChannelFunction{
				1: {Source: patch.SourceGDTF, Attribute: "Dimmer"},
			},
		}},
	})
	st.Upsert(library.Record{Manufacturer: "Robe", Model: "iForte", Modes: []library.Mode{{Name: "Mode 1", Footprint: 45}}})
	return st
}

func TestLibraryList_ShapeAndZeroValues(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/library", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Marshalled-bytes assertions, not field assertions: an empty library
	// must say "count":0 and "records":[] on the wire, and `len(recs)==0`
	// would be just as true if the key were omitted or null.
	for _, want := range []string{`"count":0`, `"records":[]`, `"format":"` + library.FileFormat + `"`, `"schemaVersion":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("empty-library response is missing %s\ngot: %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("library list response contains null: %s", body)
	}

	seedLibrary(t, h)
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/library", nil)
	body = rr.Body.String()
	if !strings.Contains(body, `"count":2`) {
		t.Errorf("count did not follow the store: %s", body)
	}
	for _, want := range []string{`"rdmManufacturerId":19541`, `"rdmDeviceModelId":0`, `"rdmDeviceModelIdKnown":false`, `"footprint":34`, `"footprint":45`} {
		if !strings.Contains(body, want) {
			t.Errorf("library list response is missing %s\ngot: %s", want, body)
		}
	}
}

func TestLibraryGetRecord(t *testing.T) {
	h := newHarness(t)
	seedLibrary(t, h)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/library/record/"+url.PathEscape("glp|jdc-1"), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	var rec library.Record
	if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Model != "JDC-1" || len(rec.Modes) != 1 || rec.Modes[0].Footprint != 34 {
		t.Errorf("record = %+v", rec)
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/library/record/"+url.PathEscape("nope|nothing"), nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown key status=%d, want 404: %s", rr.Code, rr.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if errBody["error"] == "" {
		t.Errorf("404 must use the project's {\"error\":...} body shape, got %s", rr.Body.String())
	}
}

// TestLibraryDelete_RequiresConfirm is the Apply-to-confirm contract,
// mirroring TestReset_RequiresConfirmString exactly.
func TestLibraryDelete_RequiresConfirm(t *testing.T) {
	for _, body := range []map[string]string{
		{"confirm": "delete"}, // wrong case
		{"confirm": "yes"},
		{"confirm": ""},
		{},
	} {
		h := newHarness(t)
		st := seedLibrary(t, h)
		rr := doJSON(t, h.srv.Handler(), "DELETE", "/api/library/record/"+url.PathEscape("glp|jdc-1"), body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("confirm=%q status=%d, want 400: %s", body["confirm"], rr.Code, rr.Body.String())
		}
		var got map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["error"] != "confirmation required" {
			t.Errorf("error = %q, want %q", got["error"], "confirmation required")
		}
		if len(st.List()) != 2 {
			t.Errorf("an unconfirmed delete must remove nothing, library holds %d", len(st.List()))
		}
	}
}

func TestLibraryDelete_Confirmed(t *testing.T) {
	h := newHarness(t)
	st := seedLibrary(t, h)
	rr := doJSON(t, h.srv.Handler(), "DELETE", "/api/library/record/"+url.PathEscape("glp|jdc-1"), map[string]string{"confirm": "DELETE"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"deleted":true`) || !strings.Contains(rr.Body.String(), `"count":1`) {
		t.Errorf("delete response = %s", rr.Body.String())
	}
	if len(st.List()) != 1 {
		t.Errorf("library holds %d records after delete, want 1", len(st.List()))
	}

	rr = doJSON(t, h.srv.Handler(), "DELETE", "/api/library/record/"+url.PathEscape("glp|jdc-1"), map[string]string{"confirm": "DELETE"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("deleting an already-gone record status=%d, want 404", rr.Code)
	}
}

func TestLibraryExport_DownloadHeadersAndReimportableBody(t *testing.T) {
	h := newHarness(t)
	seedLibrary(t, h)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/library/export", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="benny512_library_`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rr.Body.String()
	if strings.Contains(body, "null") {
		t.Errorf("exported document contains null:\n%s", body)
	}
	for _, want := range []string{`"format": "` + library.FileFormat + `"`, `"appVersion"`, `"generatedAt"`} {
		if !strings.Contains(body, want) {
			t.Errorf("export is missing %s\n%s", want, body)
		}
	}

	// The exported bytes must import into a fresh server unchanged — this
	// is the whole point of the file (hand it to a coworker).
	var doc library.Library
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	h2 := newHarness(t)
	rr2 := doJSON(t, h2.srv.Handler(), "POST", "/api/library/import", map[string]any{"mode": "merge", "library": doc})
	if rr2.Code != http.StatusOK {
		t.Fatalf("re-import status=%d: %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), `"added":2`) {
		t.Errorf("re-import report = %s", rr2.Body.String())
	}
}

func TestLibraryImport_MergeReportsPerRecord(t *testing.T) {
	h := newHarness(t)
	seedLibrary(t, h)
	doc := h.srv.LibraryStore.Get()
	doc.Records = append(doc.Records, library.Record{Manufacturer: "Ayrton", Model: "Perseo"})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/import", map[string]any{"mode": "merge", "library": doc})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	var res library.ImportResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Added != 1 || res.Skipped != 2 || res.Updated != 0 || res.Removed != 0 || res.Total != 3 {
		t.Errorf("import result = %+v, want added=1 skipped=2 updated=0 removed=0 total=3", res)
	}
	if len(res.Records) != 3 {
		t.Fatalf("per-record report has %d lines, want 3", len(res.Records))
	}
	body := rr.Body.String()
	for _, want := range []string{`"added":1`, `"updated":0`, `"removed":0`, `"action":"skipped"`, `"action":"added"`} {
		if !strings.Contains(body, want) {
			t.Errorf("import response is missing %s\n%s", want, body)
		}
	}
}

// TestLibraryImport_ReplaceRequiresConfirm is the second half of the
// Apply-to-confirm contract: replace is destructive, merge is not.
func TestLibraryImport_ReplaceRequiresConfirm(t *testing.T) {
	h := newHarness(t)
	st := seedLibrary(t, h)
	doc := library.Library{Format: library.FileFormat, SchemaVersion: library.CurrentSchemaVersion,
		Records: []library.Record{{Manufacturer: "Ayrton", Model: "Perseo"}}}

	for _, confirm := range []string{"", "replace", "REPLACE ", "DELETE"} {
		req := map[string]any{"mode": "replace", "library": doc}
		if confirm != "" {
			req["confirm"] = confirm
		}
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/import", req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("confirm=%q status=%d, want 400: %s", confirm, rr.Code, rr.Body.String())
		}
		if n := len(st.List()); n != 2 {
			t.Fatalf("an unconfirmed replace must change nothing, library holds %d", n)
		}
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/import",
		map[string]any{"mode": "replace", "confirm": "REPLACE", "library": doc})
	if rr.Code != http.StatusOK {
		t.Fatalf("confirmed replace status=%d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"removed":2`) {
		t.Errorf("replace report should say what it removed: %s", rr.Body.String())
	}
	recs := st.List()
	if len(recs) != 1 || recs[0].Model != "Perseo" {
		t.Errorf("after a confirmed replace, library = %+v", recs)
	}
}

func TestLibraryImport_RejectsBadDocuments(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing mode", map[string]any{"library": map[string]any{"format": library.FileFormat}}},
		{"unknown mode", map[string]any{"mode": "obliterate", "library": map[string]any{"format": library.FileFormat}}},
		{"no document", map[string]any{"mode": "merge"}},
		{"wrong format", map[string]any{"mode": "merge", "library": map[string]any{"format": "benny512-patch"}}},
		{"future schema", map[string]any{"mode": "merge", "library": map[string]any{"format": library.FileFormat, "schemaVersion": 99}}},
		{"nameless record", map[string]any{"mode": "merge", "library": map[string]any{
			"format": library.FileFormat, "schemaVersion": 1, "records": []map[string]any{{"manufacturer": "", "model": " "}}}}},
		{"duplicate records", map[string]any{"mode": "merge", "library": map[string]any{
			"format": library.FileFormat, "schemaVersion": 1, "records": []map[string]any{
				{"manufacturer": "GLP", "model": "JDC-1"}, {"manufacturer": "glp", "model": "jdc-1"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			st := seedLibrary(t, h)
			rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/import", c.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400: %s", rr.Code, rr.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got["error"] == "" {
				t.Errorf("a rejected import must say why: %s", rr.Body.String())
			}
			if n := len(st.List()); n != 2 {
				t.Errorf("a rejected import must change nothing, library holds %d", n)
			}
		})
	}
}

// TestLibraryImport_ToleratesUnknownFieldsInTheDocument: the document half
// of the body is a FILE (possibly written by a newer build) and must open
// tolerantly, while the envelope half stays strict.
func TestLibraryImport_ToleratesUnknownFieldsInTheDocument(t *testing.T) {
	h := newHarness(t)
	body := `{"mode":"merge","library":{"format":"` + library.FileFormat + `","schemaVersion":1,` +
		`"somethingFromTheFuture":true,` +
		`"records":[{"manufacturer":"Ayrton","model":"Perseo","futureField":[1,2,3]}]}}`
	req := doRaw(t, h.srv.Handler(), "POST", "/api/library/import", body)
	if req.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (a newer file must still open): %s", req.Code, req.Body.String())
	}
	if n := len(h.srv.LibraryStore.List()); n != 1 {
		t.Errorf("library holds %d records, want 1", n)
	}

	// ...but a typo in the envelope is still caught.
	bad := doRaw(t, h.srv.Handler(), "POST", "/api/library/import",
		`{"moode":"merge","library":{"format":"`+library.FileFormat+`"}}`)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("an envelope typo status=%d, want 400: %s", bad.Code, bad.Body.String())
	}
}

// --- the reset exemption ----------------------------------------------

// TestLibrary_SurvivesFullReset is the owner's explicit requirement: POST
// /api/reset wipes the patch, the walk session, discovered devices and
// settings, and must leave the fixture library — the knowledge accumulated
// across every job — completely untouched, in memory AND on disk.
//
// It also covers the wiring the library now hangs off. The store used to
// live in a package-level sync.Map keyed by *Server, which made the reset
// exemption structural (handleReset could not reach it) at the price of the
// library never being wired to a file at startup at all. It is now an
// ordinary Server FIELD, so this test additionally asserts (a) that the
// field is what the handlers actually serve from, and (b) that the library
// survives a real RESTART — a fresh store constructed over the same path,
// which is exactly what cmd/benny512 does on the next launch. Two servers
// are used for that rather than one, because "the in-memory copy is still
// there" is not the property that was broken.
func TestLibrary_SurvivesFullReset(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	libPath := filepath.Join(dir, "benny512-library.json")
	h.srv.SetLibraryStorePath(libPath)
	st := seedLibrary(t, h)
	if _, err := os.Stat(libPath); err != nil {
		t.Fatalf("library file was not written: %v", err)
	}

	patchPath := filepath.Join(dir, "benny512-patch.json")
	h.srv.SetPatchStorePath(patchPath)
	h.srv.PatchStore.Replace(patch.Patch{Name: "Show"})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/reset", map[string]string{"confirm": "RESET"})
	if rr.Code != http.StatusOK {
		t.Fatalf("reset status=%d: %s", rr.Code, rr.Body.String())
	}

	// The patch is gone...
	if _, ok := h.srv.PatchStore.Get(); ok {
		t.Error("reset should have cleared the patch")
	}
	if _, err := os.Stat(patchPath); !os.IsNotExist(err) {
		t.Errorf("reset should have deleted the patch file, stat err = %v", err)
	}
	// ...and the library is not.
	if n := len(st.List()); n != 2 {
		t.Errorf("full reset wiped the fixture library: %d records left, want 2", n)
	}
	if _, err := os.Stat(libPath); err != nil {
		t.Errorf("full reset deleted the library file: %v", err)
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/library", nil)
	if !strings.Contains(rr.Body.String(), `"count":2`) {
		t.Errorf("library not served after reset: %s", rr.Body.String())
	}

	// The handlers must serve from the Server FIELD, not from some store
	// reachable only through a helper — otherwise "wired at startup" could
	// still be false while every test above passed.
	if h.srv.LibraryStore != st {
		t.Error("GET /api/library must serve from Server.LibraryStore, the field cmd/benny512 points at a file")
	}

	// ...and it survives an actual restart: drop every reference to the
	// live store and rebuild one over the same path, the way the next
	// process launch does.
	st = nil
	h.srv.LibraryStore = nil
	reopened := library.NewStore(libPath)
	recs := reopened.List()
	if len(recs) != 2 {
		t.Fatalf("after a restart the library holds %d records, want 2", len(recs))
	}
	var jdc1 *library.Record
	for i := range recs {
		if recs[i].Key == "glp|jdc-1" {
			jdc1 = &recs[i]
		}
	}
	if jdc1 == nil {
		t.Fatalf("the GLP JDC-1 record did not survive the restart: %+v", recs)
	}
	// Not just the identity — the payload that makes a library worth having.
	if len(jdc1.Modes) != 1 || jdc1.Modes[0].Footprint != 34 {
		t.Errorf("restarted record lost its modes: %+v", jdc1.Modes)
	}
	if cf, ok := jdc1.Modes[0].ChannelFunctions[1]; !ok || cf.Attribute != "Dimmer" {
		t.Errorf("restarted record lost its channel map: %+v", jdc1.Modes[0].ChannelFunctions)
	}
	if !jdc1.RDMManufacturerIDKnown || jdc1.RDMManufacturerID != 0x4C55 {
		t.Errorf("restarted record lost its RDM identity: %+v", jdc1)
	}
}

// TestLibraryStore_IsConstructedByNew pins the other half of the wiring
// defect: the store used to be created LAZILY on first use, so a Server
// that had never served a library request had no store at all and nothing
// at startup could point one at a file. New must construct it, and it must
// be per-Server.
func TestLibraryStore_IsConstructedByNew(t *testing.T) {
	h := newHarness(t)
	if h.srv.LibraryStore == nil {
		t.Fatal("New must construct Server.LibraryStore, like it does PatchStore")
	}
	if lib := h.srv.LibraryStore.Get(); lib.Format != library.FileFormat {
		t.Errorf("a freshly constructed library is not a valid one: %+v", lib)
	}
	h2 := newHarness(t)
	if h2.srv.LibraryStore == h.srv.LibraryStore {
		t.Error("two servers must not share one library store")
	}
	h.srv.SetLibraryStorePath(filepath.Join(t.TempDir(), "benny512-library.json"))
	if h.srv.LibraryStore == nil {
		t.Fatal("SetLibraryStorePath left the field nil")
	}
}

func TestLibraryStorePath_PersistsAcrossServers(t *testing.T) {
	dir := t.TempDir()
	libPath := filepath.Join(dir, "benny512-library.json")

	h := newHarness(t)
	h.srv.SetLibraryStorePath(libPath)
	seedLibrary(t, h)

	h2 := newHarness(t)
	h2.srv.SetLibraryStorePath(libPath)
	if n := len(h2.srv.LibraryStore.List()); n != 2 {
		t.Errorf("a second server on the same path loaded %d records, want 2", n)
	}
}

// doRaw sends a raw body string (doJSON marshals a Go value, which cannot
// express the unknown-field cases above — encoding/json would just drop
// them).
func doRaw(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// --- Gap 3(a): populating the library from the patch --------------------

// seedPatch installs a small patch and returns it. The two entries are the
// owner's real shape: an MVR-imported fixture with a mode name and a
// channel map, and a hand-entered one with neither.
func seedPatch(t *testing.T, h *testHarness, entries ...patch.Entry) patch.Patch {
	t.Helper()
	return h.srv.PatchStore.Replace(patch.Patch{Name: "Show", Entries: entries})
}

func placeholderEntry(id string, start uint16) patch.Entry {
	return patch.Entry{
		ID: id, Name: "JDC " + id, FixtureType: "GLP JDC1 Strobe", Mode: "DMX Mode",
		Footprint: 61, Universe: 0, StartAddress: start,
		ChannelFunctions: map[uint16]patch.ChannelFunction{},
	}
}

func TestLibraryFromPatch_HarvestsAndReports(t *testing.T) {
	h := newHarness(t)
	seedPatch(t, h,
		patch.Entry{ID: "e1", FixtureType: "GLP JDC1 Strobe", Mode: "Mode 2 Normal (23ch)", Footprint: 23,
			ChannelFunctions: map[uint16]patch.ChannelFunction{
				1: {Source: patch.SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]patch.ChannelSet, 0)},
			}},
		patch.Entry{ID: "e2", FixtureType: "Elation Paladin Cube", Mode: "Cells 24CH", Footprint: 24},
	)

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Marshalled-bytes assertions: the zeros and the outcome strings are
	// what a UI reads, and a struct-field check would pass just as happily
	// with an omitted key.
	for _, want := range []string{
		`"total":2`, `"added":2`, `"updated":0`, `"unchanged":0`, `"skipped":0`, `"count":2`,
		`"outcome":"added"`, `"footprint":23`, `"footprint":24`,
		`"channelCount":1`, `"channelCount":0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("harvest response is missing %s\ngot: %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("harvest response contains null: %s", body)
	}

	// The library now actually holds the profile, channel map included.
	rec, ok := h.srv.LibraryStore.Find("", "GLP JDC1 Strobe")
	if !ok {
		t.Fatalf("the harvested record is not in the library: %+v", h.srv.LibraryStore.List())
	}
	if len(rec.Modes) != 1 || rec.Modes[0].Name != "Mode 2 Normal (23ch)" || rec.Modes[0].Footprint != 23 {
		t.Fatalf("harvested modes = %+v", rec.Modes)
	}
	if cf := rec.Modes[0].ChannelFunctions[1]; cf.Attribute != "Dimmer" {
		t.Errorf("harvested channel map = %+v", rec.Modes[0].ChannelFunctions)
	}
	// Provenance is inferred from the entry's own per-channel Source, never
	// assumed: a GDTF-sourced channel map makes a GDTF-sourced mode.
	if rec.Modes[0].Origin.Source != library.ProvenanceGDTF {
		t.Errorf("mode origin = %q, want %q", rec.Modes[0].Origin.Source, library.ProvenanceGDTF)
	}

	// Re-harvesting the same patch must report "unchanged", not "updated" —
	// silently bumping a timestamp would make every repeat look like work.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", map[string]any{})
	body = rr.Body.String()
	for _, want := range []string{`"added":0`, `"updated":0`, `"unchanged":2`, `"outcome":"unchanged"`} {
		if !strings.Contains(body, want) {
			t.Errorf("re-harvest response is missing %s\ngot: %s", want, body)
		}
	}
}

// TestLibraryFromPatch_MergesIntoAnExistingRecordTolerantly is the reason
// recordFromEntry leaves manufacturer empty: a patch entry's FixtureType is
// free text, and it must land in the record that already exists for that
// fixture type rather than creating a near-duplicate.
func TestLibraryFromPatch_MergesIntoAnExistingRecordTolerantly(t *testing.T) {
	h := newHarness(t)
	seedLibrary(t, h)
	seedPatch(t, h, patch.Entry{ID: "e1", FixtureType: "GLP JDC1 Strobe", Mode: "Extended", Footprint: 61})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", map[string]any{"entryIds": []string{"e1"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	if n := len(h.srv.LibraryStore.List()); n != 2 {
		t.Fatalf("harvest created a near-duplicate record: library holds %d, want 2", n)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"key":"glp|jdc-1"`) || !strings.Contains(body, `"outcome":"updated"`) {
		t.Errorf("the report must name the record the entry actually landed in: %s", body)
	}
	if !strings.Contains(body, "merged into existing record") {
		t.Errorf("a merge into a differently-spelled record must be explained: %s", body)
	}
	rec, _ := h.srv.LibraryStore.GetByKey("glp|jdc-1")
	if len(rec.Modes) != 2 {
		t.Errorf("the existing record should have gained a mode, has %+v", rec.Modes)
	}
}

func TestLibraryFromPatch_SkipsEntriesWithNothingToKeyOn(t *testing.T) {
	h := newHarness(t)
	seedPatch(t, h,
		patch.Entry{ID: "e1", Name: "Nameless", Footprint: 4},
		patch.Entry{ID: "e2", FixtureType: "Robe iForte", Mode: "Mode 1", Footprint: 45},
	)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"skipped":1`, `"added":1`, `"outcome":"skipped"`, `"total":2`} {
		if !strings.Contains(body, want) {
			t.Errorf("skip report is missing %s\ngot: %s", want, body)
		}
	}
	if !strings.Contains(body, "no fixture type") {
		t.Errorf("a skipped entry must say why: %s", body)
	}
}

func TestLibraryFromPatch_RejectsUnknownEntryAndMissingPatch(t *testing.T) {
	h := newHarness(t)
	// No patch at all.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("with no active patch status=%d, want 400: %s", rr.Code, rr.Body.String())
	}

	seedPatch(t, h, patch.Entry{ID: "e1", FixtureType: "Robe iForte", Footprint: 45})
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch", map[string]any{"entryIds": []string{"e1", "nope"}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown entry id status=%d, want 400: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "nope") {
		t.Errorf("the error must name the unknown id: %s", rr.Body.String())
	}
	// Validate-then-mutate: a request naming one bad id harvests nothing.
	if n := len(h.srv.LibraryStore.List()); n != 0 {
		t.Errorf("a rejected harvest must store nothing, library holds %d", n)
	}
}

// --- Gap 3(b): re-profiling patch entries from the library --------------

// realJDC1Record is a library record built from the REAL vendor GDTF: the
// 62-channel mode the owner's placeholder-derived patch is one channel
// short of.
func realJDC1Record() library.Record {
	cfs := make(map[uint16]patch.ChannelFunction, 62)
	for off := uint16(1); off <= 62; off++ {
		cfs[off] = patch.ChannelFunction{Source: patch.SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]patch.ChannelSet, 0)}
	}
	return library.Record{
		Manufacturer: "GLP", Model: "JDC1 Strobe",
		Modes: []library.Mode{
			{Name: "Mode 4 SPix PRO (62ch)", Footprint: 62, ChannelFunctions: cfs},
			{Name: "Data Only", Footprint: 0},
		},
	}
}

// TestLibraryReprofile_RequiresConfirm is the Apply-to-confirm contract for
// the one endpoint that moves DMX addressing on a real show patch.
func TestLibraryReprofile_RequiresConfirm(t *testing.T) {
	for _, confirm := range []string{"", "reprofile", "REPROFILE ", "RESET", "APPLY"} {
		h := newHarness(t)
		h.srv.LibraryStore.Upsert(realJDC1Record())
		seedPatch(t, h, placeholderEntry("e1", 1))
		req := map[string]any{"entryIds": []string{"e1"}, "key": "glp|jdc1 strobe", "mode": "Mode 4 SPix PRO (62ch)"}
		if confirm != "" {
			req["confirm"] = confirm
		}
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("confirm=%q status=%d, want 400: %s", confirm, rr.Code, rr.Body.String())
		}
		p, _ := h.srv.PatchStore.Get()
		if p.Entries[0].Footprint != 61 {
			t.Fatalf("an unconfirmed re-profile changed the patch: footprint %d", p.Entries[0].Footprint)
		}
	}
}

// TestLibraryReprofile_RepairsThePlaceholderPatch is the capability the
// owner asked for, end to end: a rig patched from Vectorworks placeholder
// GDTFs at 61 channels, repaired to the real vendor 62 — and the address
// collision that repair creates with the neighbouring fixture, REPORTED
// rather than blocking the repair.
func TestLibraryReprofile_RepairsThePlaceholderPatch(t *testing.T) {
	h := newHarness(t)
	h.srv.LibraryStore.Upsert(realJDC1Record())
	// Two placeholder-profiled JDC1s patched back to back at 61 channels
	// (1-61, 62-122), which is exactly how the wrong footprint hides: the
	// patch is collision-free while it is wrong.
	seedPatch(t, h, placeholderEntry("e1", 1), placeholderEntry("e2", 62))
	if n := len(patch.DetectCollisions(mustPatch(t, h))); n != 0 {
		t.Fatalf("the wrong patch should start collision-free, got %d findings", n)
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", map[string]any{
		"confirm": "REPROFILE", "entryIds": []string{"e1"},
		"key": "glp|jdc1 strobe", "mode": "mode 4 spix pro (62CH)", // case-insensitive mode lookup
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"applied":true`, `"entriesChanged":1`,
		`"oldFootprint":61`, `"newFootprint":62`, `"footprintChanged":true`,
		`"oldEndAddress":61`, `"newEndAddress":62`,
		`"oldMode":"DMX Mode"`, `"newMode":"Mode 4 SPix PRO (62ch)"`,
		`"oldChannelFunctionCount":0`, `"newChannelFunctionCount":62`,
		`"universe":0`, `"startAddress":1`,
		`"collisionErrors":1`, `"collisionWarnings":0`,
		`"kind":"overlap"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-profile response is missing %s\ngot: %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("re-profile response contains null: %s", body)
	}

	// The patch really changed, and only the entry asked for.
	p := mustPatch(t, h)
	if p.Entries[0].Footprint != 62 || len(p.Entries[0].ChannelFunctions) != 62 || p.Entries[0].Mode != "Mode 4 SPix PRO (62ch)" {
		t.Errorf("e1 = footprint %d, %d channel functions, mode %q", p.Entries[0].Footprint, len(p.Entries[0].ChannelFunctions), p.Entries[0].Mode)
	}
	if p.Entries[1].Footprint != 61 || p.Entries[1].Mode != "DMX Mode" {
		t.Errorf("e2 must be untouched, got footprint %d mode %q", p.Entries[1].Footprint, p.Entries[1].Mode)
	}
	// FixtureType is the rig's naming and is deliberately not overwritten.
	if p.Entries[0].FixtureType != "GLP JDC1 Strobe" {
		t.Errorf("re-profile must not rewrite FixtureType, got %q", p.Entries[0].FixtureType)
	}
	// The collision is real and is now in the patch — reported, not blocked.
	if n := len(patch.DetectCollisions(p)); n != 1 {
		t.Errorf("the repair should have created exactly one collision, got %d", n)
	}
}

// TestLibraryReprofile_ZeroFootprintModeIsRealData: a library mode with
// footprint 0 (a data-only device) must apply as a real 0 and appear as one
// on the wire — the numeric-zero defect class this project has hit three
// times.
func TestLibraryReprofile_ZeroFootprintModeIsRealData(t *testing.T) {
	h := newHarness(t)
	h.srv.LibraryStore.Upsert(realJDC1Record())
	seedPatch(t, h, placeholderEntry("e1", 1))
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", map[string]any{
		"confirm": "REPROFILE", "entryIds": []string{"e1"}, "key": "glp|jdc1 strobe", "mode": "Data Only",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"footprint":0`, `"newFootprint":0`, `"channelFunctionCount":0`, `"newChannelFunctionCount":0`,
		`"collisionWarnings":1`, `"kind":"zero_footprint"`} {
		if !strings.Contains(body, want) {
			t.Errorf("zero-footprint re-profile is missing %s\ngot: %s", want, body)
		}
	}
	if p := mustPatch(t, h); p.Entries[0].Footprint != 0 {
		t.Errorf("footprint = %d, want 0", p.Entries[0].Footprint)
	}
}

// TestLibraryReprofile_EntriesDoNotAliasOneChannelMap: every re-profiled
// entry must get its own copy, or a later per-entry edit would rewrite the
// whole rig.
func TestLibraryReprofile_EntriesDoNotAliasOneChannelMap(t *testing.T) {
	h := newHarness(t)
	h.srv.LibraryStore.Upsert(realJDC1Record())
	seedPatch(t, h, placeholderEntry("e1", 1), placeholderEntry("e2", 100))
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", map[string]any{
		"confirm": "REPROFILE", "entryIds": []string{"e1", "e2"},
		"key": "glp|jdc1 strobe", "mode": "Mode 4 SPix PRO (62ch)",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rr.Code, rr.Body.String())
	}
	p := mustPatch(t, h)
	cf := p.Entries[0].ChannelFunctions[1]
	cf.Attribute = "MUTATED"
	p.Entries[0].ChannelFunctions[1] = cf
	if p.Entries[1].ChannelFunctions[1].Attribute != "Dimmer" {
		t.Error("re-profiled entries share one channel-function map — editing one rewrote the other")
	}
	rec, _ := h.srv.LibraryStore.GetByKey("glp|jdc1 strobe")
	var stored *library.Mode
	for i := range rec.Modes {
		if rec.Modes[i].Name == "Mode 4 SPix PRO (62ch)" {
			stored = &rec.Modes[i]
		}
	}
	if stored == nil {
		t.Fatalf("the library lost the mode that was applied: %+v", rec.Modes)
	}
	if stored.ChannelFunctions[1].Attribute != "Dimmer" {
		t.Error("a re-profiled entry aliases the library record's own channel map")
	}
}

func TestLibraryReprofile_RejectsUnknownRecordModeAndEntry(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown key", map[string]any{"confirm": "REPROFILE", "entryIds": []string{"e1"}, "key": "nope|nothing", "mode": "Mode 4 SPix PRO (62ch)"}, http.StatusNotFound},
		{"unknown mode", map[string]any{"confirm": "REPROFILE", "entryIds": []string{"e1"}, "key": "glp|jdc1 strobe", "mode": "Turbo"}, http.StatusBadRequest},
		{"no entry ids", map[string]any{"confirm": "REPROFILE", "entryIds": []string{}, "key": "glp|jdc1 strobe", "mode": "Mode 4 SPix PRO (62ch)"}, http.StatusBadRequest},
		{"unknown entry", map[string]any{"confirm": "REPROFILE", "entryIds": []string{"e1", "ghost"}, "key": "glp|jdc1 strobe", "mode": "Mode 4 SPix PRO (62ch)"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.srv.LibraryStore.Upsert(realJDC1Record())
			seedPatch(t, h, placeholderEntry("e1", 1))
			rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", c.body)
			if rr.Code != c.want {
				t.Fatalf("status=%d, want %d: %s", rr.Code, c.want, rr.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got["error"] == "" {
				t.Errorf("a rejected re-profile must say why: %s", rr.Body.String())
			}
			if p := mustPatch(t, h); p.Entries[0].Footprint != 61 {
				t.Errorf("a rejected re-profile must change nothing, footprint = %d", p.Entries[0].Footprint)
			}
		})
	}
}

func mustPatch(t *testing.T, h *testHarness) patch.Patch {
	t.Helper()
	p, ok := h.srv.PatchStore.Get()
	if !ok {
		t.Fatal("no active patch")
	}
	return p
}

// TestLibraryEndpoints_ConcurrentHarvestAndRead exercises the library store
// from many HTTP handlers at once under -race, which is the way it is
// actually reached in production (the store is a Server field shared by
// every request goroutine). The assertions are deliberately weak — this
// test exists for the race detector and for "no handler panics", not for a
// particular interleaving.
func TestLibraryEndpoints_ConcurrentHarvestAndRead(t *testing.T) {
	h := newHarness(t)
	h.srv.LibraryStore.Upsert(realJDC1Record())
	entries := make([]patch.Entry, 0, 8)
	for i := 0; i < 8; i++ {
		entries = append(entries, placeholderEntry(fmt.Sprintf("e%d", i), uint16(1+i*64)))
	}
	seedPatch(t, h, entries...)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			doJSON(t, h.srv.Handler(), "POST", "/api/library/from-patch",
				map[string]any{"entryIds": []string{fmt.Sprintf("e%d", i)}})
		}()
		go func() {
			defer wg.Done()
			doJSON(t, h.srv.Handler(), "GET", "/api/library", nil)
		}()
		go func() {
			defer wg.Done()
			doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", map[string]any{
				"confirm": "REPROFILE", "entryIds": []string{fmt.Sprintf("e%d", i)},
				"key": "glp|jdc1 strobe", "mode": "Mode 4 SPix PRO (62ch)",
			})
		}()
	}
	wg.Wait()

	p := mustPatch(t, h)
	for _, e := range p.Entries {
		if e.Footprint != 62 {
			t.Errorf("entry %s footprint = %d, want 62 after every re-profile completed", e.ID, e.Footprint)
		}
	}
}
