package patch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
