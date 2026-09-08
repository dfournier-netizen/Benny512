package patch

import (
	"path/filepath"
	"testing"
)

func TestStore_NamedPatchesKeepIndependentRigsAndRestoreActiveShow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-patch.json")
	st := NewStore(path)
	st.Replace(Patch{Name: "Show A", Entries: []Entry{{ID: "a", Name: "A fixture"}}})

	ref, showB, err := st.CreatePatch("Show B")
	if err != nil {
		t.Fatalf("CreatePatch: %v", err)
	}
	if ref.ID == defaultPatchID || showB.Name != "Show B" || len(showB.Entries) != 0 {
		t.Fatalf("new show = ref=%+v patch=%+v", ref, showB)
	}
	if _, err := st.Mutate(func(p *Patch) error {
		p.Entries = append(p.Entries, Entry{ID: "b", Name: "B fixture"})
		return nil
	}); err != nil {
		t.Fatalf("Mutate Show B: %v", err)
	}

	showA, err := st.LoadPatch(defaultPatchID)
	if err != nil {
		t.Fatalf("LoadPatch(default): %v", err)
	}
	if showA.Name != "Show A" || len(showA.Entries) != 1 || showA.Entries[0].ID != "a" {
		t.Fatalf("Show A was changed by Show B: %+v", showA)
	}
	showB, err = st.LoadPatch(ref.ID)
	if err != nil {
		t.Fatalf("LoadPatch(Show B): %v", err)
	}
	if showB.Name != "Show B" || len(showB.Entries) != 1 || showB.Entries[0].ID != "b" {
		t.Fatalf("Show B did not retain its own entries: %+v", showB)
	}

	refs := st.ListPatches()
	if len(refs) != 2 {
		t.Fatalf("ListPatches len = %d, want 2: %+v", len(refs), refs)
	}
	active := ""
	for _, candidate := range refs {
		if candidate.Active {
			active = candidate.ID
		}
	}
	if active != ref.ID {
		t.Fatalf("active show = %q, want %q", active, ref.ID)
	}

	// The marker is a startup behavior, not only an in-memory selector.
	restarted := NewStore(path)
	got, ok := restarted.Get()
	if !ok || got.Name != "Show B" || len(got.Entries) != 1 || got.Entries[0].ID != "b" {
		t.Fatalf("restart active patch = ok=%v patch=%+v, want Show B", ok, got)
	}
}

func TestStore_ClearRemovesNamedPatchCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-patch.json")
	st := NewStore(path)
	st.Replace(Patch{Name: "Show A"})
	if _, _, err := st.CreatePatch("Show B"); err != nil {
		t.Fatalf("CreatePatch: %v", err)
	}
	st.Clear()
	if got := NewStore(path).ListPatches(); len(got) != 0 {
		t.Fatalf("saved shows resurrected after Clear: %+v", got)
	}
}
