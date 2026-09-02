package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEntry_MarshalJSON_ZeroUniverseStartAddressFootprintPresent guards the
// root-cause bug: `omitempty` on a numeric field erases a legitimate zero
// from the JSON. Asserting a struct field equals 0 after unmarshal would be
// vacuous (it's the zero value either way, `omitempty` or not) — this
// checks the actual marshalled BYTES contain the key with value 0, which is
// exactly what fails when `omitempty` is present: a canonical universe-0
// entry (display "1" under the app's default 1-based UniverseBase) would be
// missing "universe" from GET /api/patch's JSON, leaving the client's
// e.universe `undefined` and its sort comparator's `a.universe -
// b.universe` producing NaN — the confirmed root cause of "universes 1-3
// intermingled, universes >10 fine".
func TestEntry_MarshalJSON_ZeroUniverseStartAddressFootprintPresent(t *testing.T) {
	e := Entry{ID: "e1", Universe: 0, StartAddress: 0, Footprint: 0}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"universe":0`, `"startAddress":0`, `"footprint":0`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled entry missing %s; got %s", want, got)
		}
	}
}

// TestEntry_MarshalJSON_NonzeroValuesStillRoundTrip is the companion check:
// removing `omitempty` must not change how a non-zero value round-trips.
func TestEntry_MarshalJSON_NonzeroValuesStillRoundTrip(t *testing.T) {
	e := Entry{ID: "e1", Universe: 3, StartAddress: 17, Footprint: 24}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Universe != 3 || got.StartAddress != 17 || got.Footprint != 24 {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

// TestEntry_UnmarshalJSON_AbsentKeysStillZero confirms backward
// compatibility: an old/hand-written patch file that never had "universe",
// "startAddress" or "footprint" keys at all must still unmarshal them to
// their zero value, exactly as before this fix (dropping `omitempty` only
// changes what MARSHAL emits — encoding/json's Unmarshal never consulted
// the `omitempty` option to begin with).
func TestEntry_UnmarshalJSON_AbsentKeysStillZero(t *testing.T) {
	var e Entry
	if err := json.Unmarshal([]byte(`{"id":"e1","name":"old entry"}`), &e); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if e.Universe != 0 || e.StartAddress != 0 || e.Footprint != 0 {
		t.Errorf("expected zero-valued numeric fields from absent keys, got %+v", e)
	}
	if e.Name != "old entry" {
		t.Errorf("Name = %q, want %q", e.Name, "old entry")
	}
}

// TestStore_RoundTripPersistsUniverseZero exercises the real on-disk path
// (Store.persistLocked -> NewStore's tolerant read) end to end: a saved
// patch entry at universe 0 must come back as universe 0, not silently
// vanish, confirming the fix is backward- and forward-compatible with the
// persisted benny512-patch.json format and migrate().
func TestStore_RoundTripPersistsUniverseZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benny512-patch.json")

	st := NewStore(path)
	p := st.EnsureActive()
	p.Entries = []Entry{{ID: "e1", Name: "First universe fixture", Universe: 0, StartAddress: 1, Footprint: 4}}
	st.Replace(p)

	// Read the raw persisted file to confirm universe 0 is actually ON DISK
	// as an explicit key, not merely reconstructible some other way.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"universe": 0`) {
		t.Errorf("persisted file missing explicit universe:0; got:\n%s", raw)
	}

	// Reload via a fresh Store (mirrors a server restart) and confirm the
	// entry survives with its universe intact.
	st2 := NewStore(path)
	got, ok := st2.Get()
	if !ok {
		t.Fatalf("Get() ok=false after reload")
	}
	if len(got.Entries) != 1 || got.Entries[0].Universe != 0 || got.Entries[0].ID != "e1" {
		t.Errorf("reloaded patch = %+v, want one entry at universe 0", got.Entries)
	}
}

func TestFormatAddressRange_EntryEndAddress(t *testing.T) {
	cases := []struct {
		name    string
		e       Entry
		wantEnd int
	}{
		{"footprint 0 returns start", Entry{StartAddress: 100, Footprint: 0}, 100},
		{"footprint 1 single channel", Entry{StartAddress: 141, Footprint: 1}, 141},
		{"footprint >1 range", Entry{StartAddress: 141, Footprint: 20}, 160},
		{"address 512 footprint>1 overflows", Entry{StartAddress: 512, Footprint: 4}, 515},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.EndAddress(); got != c.wantEnd {
				t.Errorf("EndAddress() = %d, want %d", got, c.wantEnd)
			}
		})
	}
}

func TestStore_TolerantReaderMigratesOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	// Simulate an old/partial file: no schemaVersion, unknown extra field,
	// one entry missing several fields entirely — every field must be
	// optional per the tolerant-reader task ask.
	raw := `{
		"name": "Old Show",
		"futureFieldNobodyKnowsAboutYet": {"nested": true},
		"entries": [
			{"id": "e1", "name": "Wash 1", "universe": 0, "startAddress": 1}
		]
	}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	st := NewStore(path)
	p, ok := st.Get()
	if !ok {
		t.Fatal("expected a patch to load from the old file")
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want migrated to %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if p.Name != "Old Show" {
		t.Errorf("Name = %q, want %q", p.Name, "Old Show")
	}
	if len(p.Entries) != 1 || p.Entries[0].ID != "e1" {
		t.Fatalf("Entries = %+v, want one entry e1", p.Entries)
	}
	if p.Entries[0].Footprint != 0 {
		t.Errorf("missing Footprint should default to zero value, got %d", p.Entries[0].Footprint)
	}
}

func TestStore_MalformedFileDoesNotCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if _, ok := st.Get(); ok {
		t.Fatal("expected no patch loaded from a malformed file")
	}
	// The corrupt file must be left untouched, never silently overwritten.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not valid json" {
		t.Error("malformed file was modified; it should be left untouched")
	}
}

func TestStore_ReplaceAndMutatePersistAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	st := NewStore(path)

	p := st.Replace(Patch{Name: "New Show", Entries: []Entry{{ID: "a", Name: "Fixture A"}}})
	if p.CreatedAt.IsZero() || p.ModifiedAt.IsZero() {
		t.Error("Replace should stamp CreatedAt/ModifiedAt")
	}

	// Reload from disk in a fresh Store to prove the tmp+rename write landed.
	st2 := NewStore(path)
	p2, ok := st2.Get()
	if !ok || p2.Name != "New Show" || len(p2.Entries) != 1 {
		t.Fatalf("reloaded patch = %+v, ok=%v", p2, ok)
	}

	before := p2.ModifiedAt
	time.Sleep(2 * time.Millisecond)
	updated, err := st2.Mutate(func(pp *Patch) error {
		pp.Entries = append(pp.Entries, Entry{ID: "b", Name: "Fixture B"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Entries) != 2 {
		t.Fatalf("expected 2 entries after Mutate, got %d", len(updated.Entries))
	}
	if !updated.ModifiedAt.After(before) {
		t.Error("Mutate should bump ModifiedAt")
	}
}

func TestStore_MutateNoActivePatch(t *testing.T) {
	st := NewStore("")
	_, err := st.Mutate(func(p *Patch) error { return nil })
	if err != ErrNoPatch {
		t.Errorf("Mutate on empty store = %v, want ErrNoPatch", err)
	}
}

func TestStore_EnsureActiveLazyInit(t *testing.T) {
	st := NewStore("")
	if _, ok := st.Get(); ok {
		t.Fatal("expected no patch before EnsureActive")
	}
	p := st.EnsureActive()
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	// idempotent: calling again must not discard the first one
	if _, err := st.Mutate(func(pp *Patch) error {
		pp.Entries = append(pp.Entries, Entry{ID: "x"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p2 := st.EnsureActive()
	if len(p2.Entries) != 1 {
		t.Errorf("EnsureActive discarded existing patch: %+v", p2)
	}
}

// TestStore_ClearDiscardsInMemoryPatch mirrors internal/walk's
// TestStoreClear — Clear is the full-reset flow's "everything including
// the patch" (task ask).
func TestStore_ClearDiscardsInMemoryPatch(t *testing.T) {
	st := NewStore("")
	st.Replace(Patch{Entries: []Entry{{ID: "a"}}})
	st.Clear()
	if _, ok := st.Get(); ok {
		t.Fatal("Clear should discard the active patch")
	}
}

// TestStore_ClearRemovesFile mirrors internal/walk's
// TestStoreClearRemovesFile: a cleared patch must not be resurrected by a
// fresh Store pointed at the same path (e.g. a server restart right after
// a full reset).
func TestStore_ClearRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.json")
	st := NewStore(path)
	st.Replace(Patch{Entries: []Entry{{ID: "a"}}})
	st.Clear()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(path) err = %v, want IsNotExist", err)
	}
	st2 := NewStore(path)
	if _, ok := st2.Get(); ok {
		t.Fatal("a cleared patch should not be resurrected by a fresh Store")
	}
}

// TestStore_ClearOnNoPatchNoPathIsANoOp ensures Clear tolerates being
// called with persistence disabled and nothing ever created (--demo mode's
// PatchStore, or any test-built Server that never calls SetPatchStorePath —
// see internal/web's handleReset, which calls Clear unconditionally).
func TestStore_ClearOnNoPatchNoPathIsANoOp(t *testing.T) {
	st := NewStore("")
	st.Clear() // must not panic
	if _, ok := st.Get(); ok {
		t.Fatal("expected no patch")
	}
}

func TestPatch_IndexOf(t *testing.T) {
	p := Patch{Entries: []Entry{{ID: "a"}, {ID: "b"}}}
	if p.IndexOf("b") != 1 {
		t.Errorf("IndexOf(b) = %d, want 1", p.IndexOf("b"))
	}
	if p.IndexOf("missing") != -1 {
		t.Errorf("IndexOf(missing) = %d, want -1", p.IndexOf("missing"))
	}
}

// --- schema v3: GDTF Default/Highlight capture -----------------------------

// TestChannelFunction_MarshalJSON_ZeroDefaultIsPresentAndKnown is the schema-
// v3 instance of this package's oldest and most-repeated defect class (see
// TestEntry_MarshalJSON_ZeroUniverseStartAddressFootprintPresent, and
// Entry.Universe's doc comment for the sensorReadingJSON incident before
// that): `omitempty` on a numeric field whose zero is real data erases the
// real zero and leaves the client reading `undefined`.
//
// A GDTF Default of 0 is emphatically real data — a dimmer resting dark, a
// shutter resting closed — and Rig Check's whole reason for capturing
// defaults is to know a channel's resting value, so "rests at 0" and "we
// don't know" must be different states on the wire.
//
// This asserts the MARSHALLED BYTES, not struct fields: `cf.Default == 0`
// would pass vacuously whether or not `omitempty` were present, which is
// precisely the trap this project has been bitten by.
func TestChannelFunction_MarshalJSON_ZeroDefaultIsPresentAndKnown(t *testing.T) {
	cf := ChannelFunction{
		Source: SourceGDTF, Attribute: "Dimmer", FunctionName: "Dimmer",
		DMXFrom: 0, DMXTo: 255, ChannelSets: make([]ChannelSet, 0),
		HasDefault: true, Default: 0, DefaultByteCount: 1,
	}
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"hasDefault":true`, `"default":0`, `"defaultByteCount":1`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled ChannelFunction missing %s; got %s", want, got)
		}
	}
}

// TestChannelFunction_MarshalJSON_UnknownDefaultDistinguishableFromZero is
// the other half of the pair above, and the assertion that actually proves
// the representation works: a channel whose GDTF file stated NO Default and
// a channel whose GDTF file stated Default="0/1" must be distinguishable on
// the wire. Their "default" values are byte-identical (both 0) — hasDefault
// is the only thing that separates them, so it must always be emitted.
func TestChannelFunction_MarshalJSON_UnknownDefaultDistinguishableFromZero(t *testing.T) {
	known := ChannelFunction{Source: SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]ChannelSet, 0),
		HasDefault: true, Default: 0, DefaultByteCount: 1}
	unknown := ChannelFunction{Source: SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]ChannelSet, 0)}

	knownJSON, err := json.Marshal(known)
	if err != nil {
		t.Fatalf("Marshal(known): %v", err)
	}
	unknownJSON, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("Marshal(unknown): %v", err)
	}
	if string(knownJSON) == string(unknownJSON) {
		t.Fatalf("a known-0 default and an unknown default marshalled identically (%s) — "+
			"the client cannot tell 'rests at 0' from 'we don't know'", knownJSON)
	}
	if !strings.Contains(string(unknownJSON), `"hasDefault":false`) {
		t.Errorf("unknown default must still emit hasDefault:false; got %s", unknownJSON)
	}
	if !strings.Contains(string(unknownJSON), `"default":0`) {
		t.Errorf("unknown default must still emit default:0 (no omitempty anywhere in this triple); got %s", unknownJSON)
	}
}

// TestChannelFunction_MarshalJSON_SixteenBitDefaultRoundTrips covers the
// multi-byte case: a 16-bit function spanning a (coarse, fine) offset pair
// carries ONE record, replicated to both offsets, and DefaultByteCount is
// what keeps that record meaningful for the fine byte too. Asserts the
// marshalled bytes carry the full 16-bit value (not a truncated coarse
// byte), that it survives a round-trip, and that the per-offset
// decomposition documented on DefaultByteCount actually yields (128, 0).
func TestChannelFunction_MarshalJSON_SixteenBitDefaultRoundTrips(t *testing.T) {
	cf := ChannelFunction{
		Source: SourceGDTF, Attribute: "Pan", FunctionName: "Pan",
		DMXFrom: 0, DMXTo: 255, ChannelSets: make([]ChannelSet, 0),
		HasDefault: true, Default: 32768, DefaultByteCount: 2,
	}
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"default":32768`, `"defaultByteCount":2`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshalled 16-bit ChannelFunction missing %s; got %s", want, data)
		}
	}
	var back ChannelFunction
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.HasDefault || back.Default != 32768 || back.DefaultByteCount != 2 {
		t.Fatalf("16-bit default did not round-trip: %+v", back)
	}
	// The (coarse, fine) ascending decomposition from DefaultByteCount's doc
	// comment — computed here rather than read back from the struct, so this
	// checks the documented contract and not just field storage.
	wantBytes := []uint32{128, 0}
	for i, want := range wantBytes {
		got := (back.Default >> (8 * (uint32(back.DefaultByteCount) - 1 - uint32(i)))) & 0xFF
		if got != want {
			t.Errorf("byte %d of 16-bit default: got %d, want %d", i, got, want)
		}
	}
}

// TestChannelFunction_MarshalJSON_HighlightPresentAndAbsent is the same
// known/unknown pair for GDTF's optional Highlight attribute, which far
// fewer files carry — making "absent" the common case and HasHighlight the
// only thing keeping it distinct from a real Highlight of 0.
func TestChannelFunction_MarshalJSON_HighlightPresentAndAbsent(t *testing.T) {
	with := ChannelFunction{Source: SourceGDTF, Attribute: "Shutter1", ChannelSets: make([]ChannelSet, 0),
		HasHighlight: true, Highlight: 255, HighlightByteCount: 1}
	data, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"hasHighlight":true`, `"highlight":255`, `"highlightByteCount":1`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshalled ChannelFunction missing %s; got %s", want, data)
		}
	}
	without := ChannelFunction{Source: SourceGDTF, Attribute: "Shutter1", ChannelSets: make([]ChannelSet, 0)}
	data, err = json.Marshal(without)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"hasHighlight":false`) || !strings.Contains(string(data), `"highlight":0`) {
		t.Errorf("absent Highlight must still emit hasHighlight:false and highlight:0; got %s", data)
	}
}

// TestMigrate_V2FileLoadsWithDefaultsUnknown is the migration guard the
// "old files must always open" rule demands. A patch file written BEFORE
// schema v3 has a fully-populated ChannelFunction with no default-related
// keys at all. It must load, be stamped v3 — and, critically, come back with
// its defaults marked UNKNOWN rather than silently resting at 0, which is
// what a bare numeric field with no HasDefault companion would have produced.
//
// The v2 JSON below is written out literally (not built by marshalling the
// current struct, which would defeat the point by including the new keys).
func TestMigrate_V2FileLoadsWithDefaultsUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	v2 := `{
  "schemaVersion": 2,
  "name": "Pre-v3 Show",
  "entries": [
    {
      "id": "e1",
      "name": "Wash 1",
      "footprint": 4,
      "universe": 0,
      "startAddress": 1,
      "channelFunctions": {
        "1": {
          "source": "gdtf",
          "attribute": "Dimmer",
          "functionName": "Dimmer",
          "dmxFrom": 0,
          "dmxTo": 255,
          "channelSets": [{"name": "Slot 1", "dmxFrom": 0}]
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(v2), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	st := NewStore(path)
	p, ok := st.Get()
	if !ok {
		t.Fatal("a pre-v3 patch file failed to load at all")
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want migrated to %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(p.Entries))
	}
	cf, ok := p.Entries[0].ChannelFunctions[1]
	if !ok {
		t.Fatal("offset 1's ChannelFunction did not survive the v2 load")
	}
	// The pre-existing v2 data must be intact...
	if cf.Attribute != "Dimmer" || cf.DMXTo != 255 || len(cf.ChannelSets) != 1 {
		t.Errorf("v2 ChannelFunction data was not preserved: %+v", cf)
	}
	// ...and the v3 fields must read as UNKNOWN, not as a real default of 0.
	if cf.HasDefault {
		t.Errorf("a v2 file stated no Default, but it loaded as HasDefault=true (%+v) — "+
			"migration must never invent a resting value", cf)
	}
	if cf.HasHighlight {
		t.Errorf("a v2 file stated no Highlight, but it loaded as HasHighlight=true (%+v)", cf)
	}
	if cf.DefaultByteCount != 0 || cf.HighlightByteCount != 0 {
		t.Errorf("byte counts should be 0 for an unstated value: %+v", cf)
	}

	// And the re-marshalled file must now carry the explicit unknown markers,
	// so the client reading it back gets `false`, never `undefined`.
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"hasDefault":false`, `"default":0`, `"hasHighlight":false`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("migrated patch JSON missing %s; got %s", want, data)
		}
	}
}

// TestStore_RoundTripPersistsKnownZeroDefault is the end-to-end on-disk
// counterpart: a GDTF-imported channel whose stated resting value is 0 must
// still be marked KNOWN after a save/reload cycle. If HasDefault were ever
// dropped (or Default given `omitempty`), this entry would come back
// indistinguishable from one whose file said nothing — and Rig Check would
// have no way to tell "hold this channel at 0" from "we have no idea".
func TestStore_RoundTripPersistsKnownZeroDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")

	st := NewStore(path)
	st.Replace(Patch{Name: "Defaults", Entries: []Entry{{
		ID: "e1", Name: "Strobe", Footprint: 2, Universe: 0, StartAddress: 1,
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]ChannelSet, 0),
				HasDefault: true, Default: 0, DefaultByteCount: 1},
			2: {Source: SourceGDTF, Attribute: "Zoom", ChannelSets: make([]ChannelSet, 0)},
		},
	}}})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"hasDefault": true`) {
		t.Errorf("persisted file lost the known-0 default marker; got %s", raw)
	}

	reloaded, ok := NewStore(path).Get()
	if !ok {
		t.Fatal("persisted patch failed to reload")
	}
	got := reloaded.Entries[0].ChannelFunctions
	if !got[1].HasDefault || got[1].Default != 0 || got[1].DefaultByteCount != 1 {
		t.Errorf("known-0 default did not survive the disk round-trip: %+v", got[1])
	}
	if got[2].HasDefault {
		t.Errorf("the channel with no stated default came back as known: %+v", got[2])
	}
}
