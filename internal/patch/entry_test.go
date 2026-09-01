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
