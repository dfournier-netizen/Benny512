package library

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"benny512/internal/patch"
)

// --- serialization: the three defect classes this project has been bitten
// by. Every assertion here is against the MARSHALLED BYTES, never against
// struct fields: `rec.RDMManufacturerID == 0` and `len(rec.Modes) == 0` are
// both true for the broken code AND the fixed code, so a field-level
// assertion would pass vacuously against exactly the bug it claims to
// cover.

func TestRecordJSON_ZeroNumericAndBooleanFieldsSurvive(t *testing.T) {
	// A fixture type whose RDM identity is genuinely zero / genuinely
	// unknown, in a mode whose footprint is genuinely zero (a data-only
	// device, or a GDTF import that resolved no DMX channels). Every one
	// of these zeros is real data and must appear on the wire.
	rec := normalizeRecord(Record{
		Manufacturer: "Zero Co",
		Model:        "Nought",
		Modes:        []Mode{{Name: "None", Footprint: 0}},
	})
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"rdmManufacturerId":0`,
		`"rdmManufacturerIdKnown":false`,
		`"rdmDeviceModelId":0`,
		`"rdmDeviceModelIdKnown":false`,
		`"footprint":0`,
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("marshalled record is missing %s\ngot: %s", want, data)
		}
	}
}

func TestRecordJSON_EmptySlicesMarshalAsArraysNotNull(t *testing.T) {
	rec := normalizeRecord(Record{Manufacturer: "Empty Co", Model: "Bare"})
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"supportedPids":[]`, `"modes":[]`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("marshalled record is missing %s\ngot: %s", want, data)
		}
	}
	if bytes.Contains(data, []byte(`null`)) {
		t.Errorf("marshalled record contains null (a nil slice/map reached the wire)\ngot: %s", data)
	}
}

func TestModeJSON_ChannelMapAndChannelSetsNeverNull(t *testing.T) {
	m := normalizeMode(Mode{
		Name:             "Basic",
		Footprint:        1,
		ChannelFunctions: map[uint16]patch.ChannelFunction{1: {Source: patch.SourceGDTF, Attribute: "Dimmer"}},
	})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"channelSets":[]`)) {
		t.Errorf("channel function's channelSets marshalled as something other than []\ngot: %s", data)
	}
	empty, err := json.Marshal(normalizeMode(Mode{Name: "Empty"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(empty, []byte(`"channelFunctions":{}`)) {
		t.Errorf("empty channel map must marshal as {}, not null\ngot: %s", empty)
	}
}

func TestLibraryJSON_EmptyLibraryHasRecordsArray(t *testing.T) {
	data, err := json.Marshal(newLibrary())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"records":[]`)) {
		t.Errorf("empty library must marshal records as [], not null\ngot: %s", data)
	}
	if !bytes.Contains(data, []byte(`"format":"`+FileFormat+`"`)) {
		t.Errorf("library must self-identify its format\ngot: %s", data)
	}
}

func TestImportResultJSON_ZeroCountsSurvive(t *testing.T) {
	// "0 added" is the most important thing an import report can say.
	data, err := json.Marshal(ImportResult{Mode: ModeMerge, Records: make([]ImportRecordResult, 0)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"added":0`, `"updated":0`, `"skipped":0`, `"removed":0`, `"total":0`, `"records":[]`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("import result is missing %s\ngot: %s", want, data)
		}
	}
}

// --- tolerant matching -------------------------------------------------

// TestTokensContainedIn_TracksPatchNormalizer pins the indirection
// described in match.go's doc comment. If internal/patch ever retunes its
// weights or proposal threshold, this fails loudly here rather than
// silently degrading the library into "nothing ever matches".
func TestTokensContainedIn_TracksPatchNormalizer(t *testing.T) {
	cases := []struct {
		ref, text string
		want      bool
	}{
		{"JDC-1", "GLP JDC1 Strobe", true}, // punctuation + letter/digit join
		{"jdc 1", "GLP JDC-1", true},       // case + punctuation
		{"ERA800", "ERA 800 Performance", true},
		{"MAC Aura", "Martin MAC Viper", false}, // shares tokens, is not the same model
		{"", "anything", false},                 // empty ref matches nothing
		{"   ", "anything", false},
	}
	for _, c := range cases {
		if got := tokensContainedIn(c.ref, c.text); got != c.want {
			t.Errorf("tokensContainedIn(%q, %q) = %v, want %v", c.ref, c.text, got, c.want)
		}
	}
}

func TestMatches_TolerantOnCasePunctuationAndLetterDigitJoin(t *testing.T) {
	rec := normalizeRecord(Record{Manufacturer: "GLP", Model: "JDC-1"})
	for _, q := range []struct{ mfr, model string }{
		{"", "JDC 1"},
		{"GLP", "JDC1"},
		{"glp", "jdc-1"},
		{"", "GLP JDC1 Strobe"},
		{"GLP", "JDC-1"},
	} {
		if !rec.Matches(q.mfr, q.model) {
			t.Errorf("record %q/%q should match query %q/%q", rec.Manufacturer, rec.Model, q.mfr, q.model)
		}
	}
}

func TestMatches_RejectsSiblingModels(t *testing.T) {
	rec := normalizeRecord(Record{Manufacturer: "Martin", Model: "MAC Aura"})
	for _, q := range []struct{ mfr, model string }{
		{"Martin", "MAC Viper"},
		{"Martin", "MAC Encore"},
		{"Robe", "MAC Aura"}, // manufacturers positively disagree
	} {
		if rec.Matches(q.mfr, q.model) {
			t.Errorf("record %q/%q must NOT match query %q/%q", rec.Manufacturer, rec.Model, q.mfr, q.model)
		}
	}
}

func TestKeyFor_FoldsCaseAndWhitespaceOnly(t *testing.T) {
	if a, b := KeyFor("  GLP ", "JDC 1"), KeyFor("glp", "jdc   1"); a != b {
		t.Errorf("KeyFor should fold case and whitespace: %q != %q", a, b)
	}
	if got := KeyFor("GLP", "JDC-1"); got != "glp|jdc-1" {
		t.Errorf("KeyFor = %q, want %q (punctuation is deliberately preserved in the storage key)", got, "glp|jdc-1")
	}
}

// --- store -------------------------------------------------------------

// fixedStamp keeps sampleRecord byte-identical across calls — an Origin
// stamped with time.Now() would make every re-import look like a real
// update, which is a property of the timestamp, not of the data.
var fixedStamp = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func sampleRecord() Record {
	return Record{
		Manufacturer:           "GLP",
		Model:                  "JDC-1",
		RDMManufacturerID:      0x4C55,
		RDMManufacturerIDKnown: true,
		SupportedPIDs:          []uint16{0x0060, 0x0010},
		PIDOrigin:              Origin{Source: ProvenanceRDM, Detail: "4c55:00000001", At: fixedStamp},
		IdentityOrigin:         Origin{Source: ProvenanceGDTF, Detail: "GLP@JDC1.gdtf", At: fixedStamp},
		Modes: []Mode{{
			Name:      "Standard",
			Footprint: 34,
			Origin:    Origin{Source: ProvenanceGDTF},
			ChannelFunctions: map[uint16]patch.ChannelFunction{
				1: {Source: patch.SourceGDTF, Attribute: "Dimmer", DMXFrom: 0, DMXTo: 255},
			},
		}},
	}
}

func TestStore_UpsertAddsThenReportsUnchanged(t *testing.T) {
	st := NewStore("")
	rec, out := st.Upsert(sampleRecord())
	if out != OutcomeAdded {
		t.Fatalf("first upsert outcome = %q, want %q", out, OutcomeAdded)
	}
	if rec.Key != "glp|jdc-1" {
		t.Errorf("stored key = %q", rec.Key)
	}
	if _, out = st.Upsert(sampleRecord()); out != OutcomeUnchanged {
		t.Errorf("re-upsert of identical record outcome = %q, want %q", out, OutcomeUnchanged)
	}
	if n := len(st.List()); n != 1 {
		t.Errorf("library holds %d records, want 1 (one record per manufacturer+model)", n)
	}
}

func TestStore_UpsertMergesTolerantlyIntoOneRecord(t *testing.T) {
	st := NewStore("")
	st.Upsert(sampleRecord())
	// Same fixture type, differently spelled, arriving from live RDM with
	// extra PIDs and a mode whose channel layout is unknown.
	_, out := st.Upsert(Record{
		Manufacturer:          "GLP",
		Model:                 "JDC1 Strobe",
		RDMDeviceModelID:      0,
		RDMDeviceModelIDKnown: true,
		SupportedPIDs:         []uint16{0x0060, 0x00F0},
		Modes:                 []Mode{{Name: "standard", Footprint: 34, Origin: Origin{Source: ProvenanceRDM}}},
	})
	if out != OutcomeUpdated {
		t.Fatalf("outcome = %q, want %q", out, OutcomeUpdated)
	}
	recs := st.List()
	if len(recs) != 1 {
		t.Fatalf("library holds %d records, want 1 — tolerant matching should have merged", len(recs))
	}
	r := recs[0]
	if r.Manufacturer != "GLP" || r.Model != "JDC-1" {
		t.Errorf("existing identity spelling should win, got %q / %q", r.Manufacturer, r.Model)
	}
	if !r.RDMDeviceModelIDKnown || r.RDMDeviceModelID != 0 {
		t.Errorf("a KNOWN device model id of 0 must survive the merge, got id=%d known=%v", r.RDMDeviceModelID, r.RDMDeviceModelIDKnown)
	}
	if !r.RDMManufacturerIDKnown || r.RDMManufacturerID != 0x4C55 {
		t.Errorf("existing known manufacturer id must not be erased, got %#x known=%v", r.RDMManufacturerID, r.RDMManufacturerIDKnown)
	}
	want := []uint16{0x0010, 0x0060, 0x00F0}
	if len(r.SupportedPIDs) != len(want) {
		t.Fatalf("supported pids = %v, want union %v", r.SupportedPIDs, want)
	}
	for i, p := range want {
		if r.SupportedPIDs[i] != p {
			t.Fatalf("supported pids = %v, want %v (ascending union)", r.SupportedPIDs, want)
		}
	}
	if len(r.Modes) != 1 {
		t.Fatalf("modes = %d, want 1 (case-insensitive name match)", len(r.Modes))
	}
	if len(r.Modes[0].ChannelFunctions) != 1 {
		t.Errorf("an RDM-observed mode with no channel map must not erase the GDTF channel map, got %d functions", len(r.Modes[0].ChannelFunctions))
	}
}

func TestStore_FindAndDelete(t *testing.T) {
	st := NewStore("")
	st.Upsert(sampleRecord())
	if _, ok := st.Find("", "JDC 1"); !ok {
		t.Error("Find should match tolerantly")
	}
	if _, ok := st.GetByKey("glp|jdc-1"); !ok {
		t.Error("GetByKey should find the exact key")
	}
	if st.Delete("nope|nothing") {
		t.Error("Delete of an absent key should report false")
	}
	if !st.Delete("glp|jdc-1") {
		t.Error("Delete of a present key should report true")
	}
	if n := len(st.List()); n != 0 {
		t.Errorf("library holds %d records after delete, want 0", n)
	}
}

func TestStore_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/lib.json"
	st := NewStore(path)
	st.Upsert(sampleRecord())

	reopened := NewStore(path)
	recs := reopened.List()
	if len(recs) != 1 {
		t.Fatalf("reloaded library holds %d records, want 1", len(recs))
	}
	if recs[0].Modes[0].Footprint != 34 {
		t.Errorf("footprint did not survive the round trip: %d", recs[0].Modes[0].Footprint)
	}
	if recs[0].Modes[0].ChannelFunctions[1].Attribute != "Dimmer" {
		t.Errorf("channel function did not survive the round trip: %+v", recs[0].Modes[0].ChannelFunctions)
	}
}

func TestNewStore_IgnoresAForeignJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/lib.json"
	// A patch export, say — same folder, same extension, not ours.
	if err := writeFile(path, `{"schemaVersion":2,"entries":[{"id":"e1"}]}`); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if n := len(st.List()); n != 0 {
		t.Errorf("a foreign JSON file must not become the library, got %d records", n)
	}
}

func TestMigrate_MissingSchemaVersionAndNilSlices(t *testing.T) {
	lib := Library{Format: FileFormat, Records: []Record{{Manufacturer: "A", Model: "B"}}}
	migrate(&lib)
	if lib.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schema version = %d, want %d", lib.SchemaVersion, CurrentSchemaVersion)
	}
	data, err := json.Marshal(lib)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte("null")) {
		t.Errorf("migrated library still marshals a null\ngot: %s", data)
	}
	if lib.Records[0].Key != "a|b" {
		t.Errorf("migrate must recompute keys, got %q", lib.Records[0].Key)
	}
}

// --- import ------------------------------------------------------------

func docWith(recs ...Record) Library {
	return Library{Format: FileFormat, SchemaVersion: CurrentSchemaVersion, Records: recs}
}

func TestImport_MergeAddsUpdatesAndSkips(t *testing.T) {
	st := NewStore("")
	st.Upsert(sampleRecord())

	incoming := docWith(
		sampleRecord(), // identical -> skipped
		Record{Manufacturer: "Robe", Model: "iForte", Modes: []Mode{{Name: "Mode 1", Footprint: 45}}}, // new -> added
		Record{Manufacturer: "GLP", Model: "impression X5"},                                           // new -> added
	)
	res, err := st.Import(incoming, ModeMerge)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Added != 2 || res.Updated != 0 || res.Skipped != 1 || res.Removed != 0 || res.Total != 3 {
		t.Errorf("import result = %+v, want added=2 updated=0 skipped=1 removed=0 total=3", res)
	}
	if len(res.Records) != 3 {
		t.Fatalf("per-record report has %d lines, want 3", len(res.Records))
	}
	if res.Records[0].Action != ActionSkipped {
		t.Errorf("first record action = %q, want %q", res.Records[0].Action, ActionSkipped)
	}
	if n := len(st.List()); n != 3 {
		t.Errorf("library holds %d records after merge, want 3", n)
	}
}

func TestImport_ReplaceRemovesEverythingElse(t *testing.T) {
	st := NewStore("")
	st.Upsert(sampleRecord())
	st.Upsert(Record{Manufacturer: "Robe", Model: "iForte"})

	res, err := st.Import(docWith(Record{Manufacturer: "Ayrton", Model: "Perseo"}), ModeReplace)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Removed != 2 || res.Added != 1 {
		t.Errorf("replace result = %+v, want removed=2 added=1", res)
	}
	recs := st.List()
	if len(recs) != 1 || recs[0].Model != "Perseo" {
		t.Errorf("after replace, library = %+v, want only Perseo", recs)
	}
}

func TestImport_RejectsBadDocumentsWithoutTouchingTheStore(t *testing.T) {
	cases := []struct {
		name string
		doc  Library
		mode ImportMode
	}{
		{"wrong format", Library{Format: "benny512-patch", Records: []Record{{Manufacturer: "X", Model: "Y"}}}, ModeMerge},
		{"missing format", Library{Records: []Record{{Manufacturer: "X", Model: "Y"}}}, ModeMerge},
		{"future schema", Library{Format: FileFormat, SchemaVersion: CurrentSchemaVersion + 1}, ModeMerge},
		{"nameless record", docWith(Record{Manufacturer: "  ", Model: ""}), ModeMerge},
		{"duplicate records", docWith(Record{Manufacturer: "GLP", Model: "JDC-1"}, Record{Manufacturer: "glp", Model: "JDC-1"}), ModeMerge},
		{"unknown mode", docWith(Record{Manufacturer: "X", Model: "Y"}), ImportMode("wipe")},
		{"empty mode", docWith(Record{Manufacturer: "X", Model: "Y"}), ImportMode("")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := NewStore("")
			st.Upsert(sampleRecord())
			before := st.List()
			res, err := st.Import(c.doc, c.mode)
			if err == nil {
				t.Fatalf("expected an error, got result %+v", res)
			}
			if err.Error() == "" {
				t.Error("error message must not be empty — the user has to be told what is wrong with the file")
			}
			after := st.List()
			if len(after) != len(before) || after[0].Key != before[0].Key {
				t.Errorf("a rejected import must change nothing: before=%d after=%d", len(before), len(after))
			}
		})
	}
}

func TestImport_ReplaceIsRejectedBeforeWipingWhenTheDocumentIsBad(t *testing.T) {
	// The dangerous combination: replace mode plus a malformed document.
	// Validation must happen before the existing library is discarded.
	st := NewStore("")
	st.Upsert(sampleRecord())
	if _, err := st.Import(Library{Format: "nope"}, ModeReplace); err == nil {
		t.Fatal("expected an error")
	}
	if n := len(st.List()); n != 1 {
		t.Fatalf("a rejected replace-import wiped the library: %d records left", n)
	}
}

func TestExport_RoundTripsThroughJSON(t *testing.T) {
	st := NewStore("")
	st.Upsert(sampleRecord())
	doc := st.Export("benny512 test", time.Now())
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("exported document contains null\n%s", data)
	}
	var back Library
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fresh := NewStore("")
	res, err := fresh.Import(back, ModeMerge)
	if err != nil {
		t.Fatalf("re-import of our own export failed: %v", err)
	}
	if res.Added != 1 {
		t.Errorf("re-import result = %+v, want added=1", res)
	}
	got := fresh.List()[0]
	want := st.List()[0]
	if got.Modes[0].Footprint != want.Modes[0].Footprint || got.RDMManufacturerID != want.RDMManufacturerID {
		t.Errorf("export/import round trip lost data:\n got %+v\nwant %+v", got, want)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// TestMergeModes_ReObservationKeepsOriginAndReportsUnchanged pins the rule
// that makes OutcomeUnchanged usable by a producer that stamps its own
// Origin.At — POST /api/library/from-patch, and any future live-RDM
// observer. Re-upserting a mode whose footprint and channel map are
// identical, from the same kind of source, must NOT restamp the origin and
// must report unchanged; without it every repeat of an idempotent harvest
// comes back "updated" and the user cannot tell a real change from a
// re-run.
func TestMergeModes_ReObservationKeepsOriginAndReportsUnchanged(t *testing.T) {
	st := NewStore("")
	first := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mk := func(at time.Time, src Provenance) Record {
		return Record{Manufacturer: "GLP", Model: "JDC-1", Modes: []Mode{{
			Name: "Standard", Footprint: 34,
			ChannelFunctions: map[uint16]patch.ChannelFunction{
				1: {Source: patch.SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]patch.ChannelSet, 0)},
			},
			Origin: Origin{Source: src, Detail: "patch entry x", At: at},
		}}}
	}
	if _, out := st.Upsert(mk(first, ProvenanceGDTF)); out != OutcomeAdded {
		t.Fatalf("first upsert = %q, want %q", out, OutcomeAdded)
	}
	later := first.Add(72 * time.Hour)
	rec, out := st.Upsert(mk(later, ProvenanceGDTF))
	if out != OutcomeUnchanged {
		t.Errorf("re-observing identical data = %q, want %q", out, OutcomeUnchanged)
	}
	if !rec.Modes[0].Origin.At.Equal(first) {
		t.Errorf("origin was restamped to %v, want the original %v", rec.Modes[0].Origin.At, first)
	}

	// ...but a genuine provenance change is still an update. The same
	// channel map newly corroborated by a different KIND of source is real
	// news, and must not be swallowed by the rule above.
	rec, out = st.Upsert(mk(later, ProvenanceRDM))
	if out != OutcomeUpdated {
		t.Fatalf("a provenance change = %q, want %q", out, OutcomeUpdated)
	}
	if rec.Modes[0].Origin.Source != ProvenanceRDM || !rec.Modes[0].Origin.At.Equal(later) {
		t.Errorf("a real provenance change must land: %+v", rec.Modes[0].Origin)
	}

	// And a real payload change is still an update, origin restamped.
	changed := mk(later, ProvenanceRDM)
	changed.Modes[0].Footprint = 35
	rec, out = st.Upsert(changed)
	if out != OutcomeUpdated || rec.Modes[0].Footprint != 35 {
		t.Errorf("a footprint change = %q / %d, want %q / 35", out, rec.Modes[0].Footprint, OutcomeUpdated)
	}
}
