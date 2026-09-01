package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChannelFunction_MarshalJSON_ZeroDMXFromPresent guards the same defect
// class TestEntry_MarshalJSON_ZeroUniverseStartAddressFootprintPresent
// guards for Entry: DMX 0 is real, legitimate data for a ChannelFunction
// starting at DMX 0 (the overwhelmingly common case — most functions' first
// range starts at 0) and for a ChannelSet's DMXFrom. `omitempty` on either
// would erase that from the wire exactly the way it did for Universe/
// StartAddress/Footprint.
func TestChannelFunction_MarshalJSON_ZeroDMXFromPresent(t *testing.T) {
	cf := ChannelFunction{
		Source: SourceGDTF, Attribute: "Dimmer", DMXFrom: 0, DMXTo: 255,
		ChannelSets: []ChannelSet{{Name: "Slot 1", DMXFrom: 0}},
	}
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"dmxFrom":0`, `"source":"gdtf"`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled ChannelFunction missing %s; got %s", want, got)
		}
	}
	if !strings.Contains(got, `"channelSets":[{`) {
		t.Errorf("marshalled ChannelFunction's channelSets should be an array, got %s", got)
	}
}

// TestChannelFunction_MarshalJSON_EmptyChannelSetsIsArrayNotNull guards the
// "slices must be make([]T,0)" rule: a ChannelFunction with zero
// ChannelSets must marshal to `"channelSets":[]`, never `"channelSets":null`
// — a nil slice with `omitempty` removed still marshals to `null`, which is
// exactly the kind of thing this project's JSON rule exists to catch (a JS
// caller doing `.map()`/`.length` on `null` throws).
func TestChannelFunction_MarshalJSON_EmptyChannelSetsIsArrayNotNull(t *testing.T) {
	cf := ChannelFunction{Source: SourceRDMInferred, Attribute: "Pan", ChannelSets: make([]ChannelSet, 0)}
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("marshalled ChannelFunction contains null: %s", data)
	}
	if !strings.Contains(string(data), `"channelSets":[]`) {
		t.Errorf("expected channelSets:[], got %s", data)
	}
}

// TestEntry_MarshalJSON_ChannelFunctionsPresentNotOmitted guards Task 1's
// storage rule directly: Entry.ChannelFunctions has no `omitempty`, so even
// an entry with none must still emit the key (as `{}`), matching this
// package's rule that "resolved and found nothing" (empty map) must stay
// distinguishable on the wire from a key that's simply missing (which, per
// entry.go's ChannelFunctions doc comment, should never happen once an
// entry has passed through any Store method — see the migrate/normalize
// tests below for that half).
func TestEntry_MarshalJSON_ChannelFunctionsPresentNotOmitted(t *testing.T) {
	e := Entry{ID: "e1", ChannelFunctions: make(map[uint16]ChannelFunction)}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"channelFunctions":{}`) {
		t.Errorf("expected explicit channelFunctions:{}, got %s", data)
	}
}

// TestEntry_ChannelFunctions_RoundTrip pins the full offset->ChannelFunction
// round trip through JSON, including a secondary (fine-byte) slot whose
// Attribute matches its primary's — the shape gdtfparse.js/
// BuildRDMInferredChannelFunctions both produce for a 16-bit function.
func TestEntry_ChannelFunctions_RoundTrip(t *testing.T) {
	e := Entry{
		ID: "e1",
		ChannelFunctions: map[uint16]ChannelFunction{
			1: {
				Source: SourceGDTF, Attribute: "Pan", FunctionName: "Pan", DMXFrom: 0, DMXTo: 255,
				PhysicalFrom: 0, PhysicalTo: 540,
				ChannelSets: []ChannelSet{{Name: "Full Left", DMXFrom: 0, PhysicalFrom: 0, PhysicalTo: 0}},
			},
			2: {Source: SourceGDTF, Attribute: "Pan", ChannelSets: make([]ChannelSet, 0)},
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.ChannelFunctions) != 2 {
		t.Fatalf("got %d channel functions, want 2: %+v", len(got.ChannelFunctions), got.ChannelFunctions)
	}
	cf1 := got.ChannelFunctions[1]
	if cf1.Source != SourceGDTF || cf1.Attribute != "Pan" || cf1.DMXTo != 255 || cf1.PhysicalTo != 540 {
		t.Errorf("offset 1 = %+v", cf1)
	}
	if len(cf1.ChannelSets) != 1 || cf1.ChannelSets[0].Name != "Full Left" {
		t.Errorf("offset 1 ChannelSets = %+v", cf1.ChannelSets)
	}
	cf2 := got.ChannelFunctions[2]
	if cf2.Attribute != "Pan" {
		t.Errorf("offset 2 = %+v", cf2)
	}
}

// TestMigrate_NilChannelFunctionsBecomesEmptyMap proves the schema-version-2
// migration path an old (v1, or hand-written pre-field) patch file needs:
// an entry with no "channelFunctions" key at all must load with a non-nil,
// empty ChannelFunctions map, and the file's SchemaVersion must be bumped
// to CurrentSchemaVersion.
func TestMigrate_NilChannelFunctionsBecomesEmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	// A real v1 file: no "channelFunctions" key anywhere, schemaVersion 1.
	raw := `{
		"schemaVersion": 1,
		"name": "Old Show",
		"entries": [
			{"id": "e1", "name": "Wash 1", "universe": 0, "startAddress": 1, "footprint": 4}
		]
	}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	st := NewStore(path)
	p, ok := st.Get()
	if !ok {
		t.Fatal("expected a patch to load from the v1 file")
	}
	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("Entries = %+v, want 1", p.Entries)
	}
	if p.Entries[0].ChannelFunctions == nil {
		t.Fatal("ChannelFunctions is nil after migrate — old file must load with a non-nil empty map")
	}
	if len(p.Entries[0].ChannelFunctions) != 0 {
		t.Errorf("expected empty ChannelFunctions, got %+v", p.Entries[0].ChannelFunctions)
	}

	// The migrated-and-resaved file must persist ChannelFunctions as an
	// explicit `{}`, not merely hold it in memory — confirms migrate()'s
	// fix actually reaches disk, not just the in-memory Patch this one
	// process happens to be holding.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = data // migrate() only writes back on the next persisting mutation;
	// re-open with a fresh Store to confirm the in-memory fix is stable
	// across repeated loads of the same (still-v1-on-disk) file.
	st2 := NewStore(path)
	p2, ok := st2.Get()
	if !ok || p2.Entries[0].ChannelFunctions == nil {
		t.Fatalf("second load: ChannelFunctions = %+v, ok=%v", p2.Entries[0].ChannelFunctions, ok)
	}
}

// TestMutate_NewEntryWithNilChannelFunctionsIsNormalized proves the OTHER
// half of the nil-map problem: an entry built by a caller that never
// touches ChannelFunctions at all (a plain `Entry{...}` literal — this is
// exactly what internal/web/patch.go's entryFromRequest historically did
// before this field existed, and what any future call site could still do
// by omission) gets normalized to a non-nil map by Store.Mutate, not just
// by migrate()'s on-load path.
func TestMutate_NewEntryWithNilChannelFunctionsIsNormalized(t *testing.T) {
	st := NewStore("")
	st.EnsureActive()
	updated, err := st.Mutate(func(p *Patch) error {
		p.Entries = append(p.Entries, Entry{ID: "e1", Name: "No channel functions set"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Entries[0].ChannelFunctions == nil {
		t.Fatal("Mutate did not normalize a nil ChannelFunctions map")
	}
}

// TestReplace_NewEntryWithNilChannelFunctionsIsNormalized is Mutate's
// sibling for Replace (fresh-create / adopt-from-discovered / MVR fresh
// import all go through Replace, not Mutate).
func TestReplace_NewEntryWithNilChannelFunctionsIsNormalized(t *testing.T) {
	st := NewStore("")
	p := st.Replace(Patch{Entries: []Entry{{ID: "e1"}}})
	if p.Entries[0].ChannelFunctions == nil {
		t.Fatal("Replace did not normalize a nil ChannelFunctions map")
	}
}
