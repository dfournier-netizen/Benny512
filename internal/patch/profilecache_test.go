package patch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Profile cache (C1b, owner decision 2026-10-07): a show file stores each
// distinct profile (fixture type + mode + channel map incl. full detail +
// wheels) ONCE, keyed by a content hash, and each entry stores a reference.
// In memory and on the API nothing changes: entries are hydrated on load
// and dehydrated on save.

func detailedCF(attr string, dmxTo uint32) ChannelFunction {
	return ChannelFunction{Source: SourceGDTF, Attribute: attr, FunctionName: attr, DMXTo: dmxTo, PhysicalTo: 1,
		ChannelSets: []ChannelSet{{Name: "Open", DMXFrom: 32}}, HasDefault: true, Default: 32, DefaultByteCount: 1,
		ByteCount: 1, FunctionsKnown: true,
		Functions: []FunctionRange{
			{LogicalAttribute: attr, Attribute: attr, Name: attr, DMXTo: 63, PhysicalTo: 1, Wheel: "Gobo1",
				Sets: []SetRange{{Name: "Open", DMXFrom: 32, DMXTo: 63, HasWheelSlot: true, WheelSlot: 1}}},
			{LogicalAttribute: attr, Attribute: attr + "Strobe", Name: "Strobe", DMXFrom: 64, DMXTo: 255, ModeMaster: "Head_Color1", HasMode: true, ModeTo: 255, Sets: []SetRange{}},
		}}
}

var testWheels = []Wheel{{Name: "Gobo1", Slots: []WheelSlot{{Name: "Open", HasColor: true, ColorX: 0.3127, ColorY: 0.329, ColorYY: 100, HasSRGB: true, SRGB: "#ffffff"}, {Name: "G01", MediaFileName: "15020290"}}}}

func showEntries() []Entry {
	mk := func(id string, addr uint16, cf ChannelFunction) Entry {
		return Entry{ID: id, Name: id, FixtureType: "Robe BMFL Spot", Mode: "Mode 1", Footprint: 2, Universe: 0, StartAddress: addr,
			ChannelFunctions: map[uint16]ChannelFunction{1: cf, 2: detailedCF("Dimmer", 255)},
			Wheels:           CloneWheels(testWheels), WheelsKnown: true,
			Location: Location{Known: true, X: 1000, Y: -250.5, Z: 6000, RotationKnown: true, RotZ: 90}}
	}
	return []Entry{
		mk("a", 1, detailedCF("Shutter1", 63)),
		mk("b", 3, detailedCF("Shutter1", 63)),
		mk("c", 5, detailedCF("Shutter1", 31)), // a different profile
		{ID: "d", Name: "hand-entered", Footprint: 1, StartAddress: 7},
	}
}

func readShow(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func profileCount(file map[string]any) int {
	m, _ := file["profiles"].(map[string]any)
	return len(m)
}

func TestShowFile_StoresEachDistinctProfileOnceAndHydratesExactly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	st := NewStore(path)
	ws := json.RawMessage(`{"layout":{"cells":[{"entryId":"a","col":2,"row":3,"layer":1}]}}`)
	saved, err := st.ReplaceChecked(Patch{Name: "Cache", Entries: showEntries(), Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}

	file := readShow(t, path)
	if file["schemaVersion"] != float64(8) {
		t.Errorf("schemaVersion on disk = %v, want 8", file["schemaVersion"])
	}
	profiles, _ := file["profiles"].(map[string]any)
	if len(profiles) != 2 {
		t.Errorf("show file holds %d profiles, want 2 (a and b share one; c differs; d has none)", len(profiles))
	}
	entries, _ := file["entries"].([]any)
	refs := map[string]string{}
	for _, raw := range entries {
		e := raw.(map[string]any)
		if _, inline := e["channelFunctions"]; inline {
			t.Errorf("entry %v stores its channel map inline on disk", e["id"])
		}
		if _, inline := e["wheels"]; inline {
			t.Errorf("entry %v stores its wheels inline on disk", e["id"])
		}
		ref, _ := e["profile"].(string)
		refs[e["id"].(string)] = ref
	}
	if refs["a"] == "" || refs["a"] != refs["b"] || refs["a"] == refs["c"] || refs["d"] != "" {
		t.Errorf("profile refs = %v; want a == b, c different, d none", refs)
	}

	// Reload: every entry — channel map, detail, wheels, location — and the
	// workspace (C2 layout) are exactly what was saved.
	re, ok := NewStore(path).Get()
	if !ok {
		t.Fatal("saved show did not reload")
	}
	if !reflect.DeepEqual(re.Entries, saved.Entries) {
		a, _ := json.Marshal(saved.Entries)
		b, _ := json.Marshal(re.Entries)
		t.Errorf("hydrated entries differ from saved ones\n saved  %s\n loaded %s", a, b)
	}
	var wantWS, gotWS bytes.Buffer
	_ = json.Compact(&wantWS, ws)
	_ = json.Compact(&gotWS, re.Workspace)
	if wantWS.String() != gotWS.String() {
		t.Errorf("workspace (C2 layout) changed across save/load: %s", re.Workspace)
	}
	// The API shape is unchanged: an in-memory Patch marshals inline, with
	// no profile table and no per-entry refs.
	api, _ := json.Marshal(re)
	if strings.Contains(string(api), `"profiles"`) || strings.Contains(string(api), `"profile":`) {
		t.Errorf("the API/in-memory JSON leaked the on-disk profile table: %.300s", api)
	}
	if !strings.Contains(string(api), `"functionsKnown":true`) || !strings.Contains(string(api), `"mediaFileName":"15020290"`) {
		t.Errorf("the API/in-memory JSON lost hydrated detail")
	}
}

// TestShowFile_EditingOneEntryNeverChangesAnotherSharingItsProfile: a and b
// share one stored profile; changing a's channel map gives a its own profile
// and leaves b — and the record b points at — exactly as they were. Profiles
// no entry references any more are not written.
func TestShowFile_EditingOneEntryNeverChangesAnotherSharingItsProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	st := NewStore(path)
	before, err := st.ReplaceChecked(Patch{Name: "Cache", Entries: showEntries()})
	if err != nil {
		t.Fatal(err)
	}
	refBefore := readShow(t, path)["entries"].([]any)[1].(map[string]any)["profile"]

	if _, err := st.Mutate(func(p *Patch) error {
		cf := p.Entries[0].ChannelFunctions[1]
		cf.Functions[0].Name = "Renamed by hand"
		p.Entries[0].ChannelFunctions[1] = cf
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	file := readShow(t, path)
	ents := file["entries"].([]any)
	refA, refB := ents[0].(map[string]any)["profile"], ents[1].(map[string]any)["profile"]
	if refA == refB || refB != refBefore {
		t.Errorf("after editing a: ref a=%v b=%v (b before %v); want a moved to a new profile, b untouched", refA, refB, refBefore)
	}
	if n := profileCount(file); n != 3 {
		t.Errorf("%d profiles after the edit, want 3", n)
	}
	re, _ := NewStore(path).Get()
	if !reflect.DeepEqual(re.Entries[1], before.Entries[1]) {
		t.Error("entry b changed when entry a was edited")
	}
	if got := re.Entries[0].ChannelFunctions[1].Functions[0].Name; got != "Renamed by hand" {
		t.Errorf("entry a's edit lost: %q", got)
	}

	// Remove a and c: only b's profile is still referenced.
	if _, err := st.Mutate(func(p *Patch) error {
		p.Entries = []Entry{p.Entries[1], p.Entries[3]}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := profileCount(readShow(t, path)); n != 1 {
		t.Errorf("%d profiles after deleting the entries that used two of them, want 1 (unreferenced pruned)", n)
	}
}

// TestShowFile_V7InlineFileLoadsAndResavesWithProfiles: every older show file
// stored channel maps inline. It must load exactly as before, and the first
// save writes the profile table (the pre-save file is kept as .bak).
func TestShowFile_V7InlineFileLoadsAndResavesWithProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "show.json")
	inline := Patch{SchemaVersion: 7, Name: "Old", Entries: showEntries()}
	normalizeChannelFunctions(inline.Entries)
	normalizeSettingStates(inline.Entries)
	raw, _ := json.MarshalIndent(inline, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	got, ok := st.Get()
	if !ok || !reflect.DeepEqual(got.Entries, inline.Entries) {
		t.Fatalf("v7 inline show did not load exactly (ok=%v)", ok)
	}
	if _, err := st.Mutate(func(p *Patch) error { p.Name = "Old (resaved)"; return nil }); err != nil {
		t.Fatal(err)
	}
	file := readShow(t, path)
	if file["schemaVersion"] != float64(8) || profileCount(file) != 2 {
		t.Errorf("resaved v7 show: schemaVersion %v, %v profiles; want 8 and 2", file["schemaVersion"], profileCount(file))
	}
	if bak, err := os.ReadFile(path + ".bak"); err != nil || !strings.Contains(string(bak), `"schemaVersion": 7`) {
		t.Errorf("the pre-upgrade file was not kept as .bak (%v)", err)
	}
	re, _ := NewStore(path).Get()
	if !reflect.DeepEqual(re.Entries, inline.Entries) {
		t.Error("resaved v7 show does not hydrate back to the same entries")
	}
}

// TestShowFile_DanglingProfileRefIsRefusedNotGuessed: an entry pointing at a
// profile the file does not contain cannot be given a channel map without
// guessing. The show is not loaded, and the file is not overwritten.
func TestShowFile_DanglingProfileRefIsRefusedNotGuessed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	damaged := `{"schemaVersion":8,"name":"Damaged","profiles":{},
	  "entries":[{"id":"a","name":"a","footprint":1,"universe":0,"startAddress":1,"phaseCount":0,"profile":"0000"}]}`
	if err := os.WriteFile(path, []byte(damaged), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if _, ok := st.Get(); ok {
		t.Error("a show whose entry references a missing profile was loaded")
	}
	st.EnsureActive()
	if _, err := st.Mutate(func(p *Patch) error { p.Name = "x"; return nil }); err == nil {
		t.Error("saving over a show with a dangling profile reference succeeded; it must be refused like any damaged file")
	}
	if b, _ := os.ReadFile(path); string(b) != damaged {
		t.Error("the damaged show file was overwritten")
	}
}

// TestShowFile_NewerSchemaIsRefused: a show written by a newer build may hold
// data this build cannot represent (this very change is an example: an older
// build reading a v8 file sees no channel maps). Going forward this build
// refuses to open or overwrite a show newer than it understands.
func TestShowFile_NewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "show.json")
	newer := `{"schemaVersion":99,"name":"From the future","entries":[]}`
	if err := os.WriteFile(path, []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	if _, ok := st.Get(); ok {
		t.Error("a schema-99 show was opened by a build that understands schema 8")
	}
	st.EnsureActive()
	if _, err := st.Mutate(func(p *Patch) error { return nil }); err == nil {
		t.Error("a schema-99 show was overwritten")
	}
}
