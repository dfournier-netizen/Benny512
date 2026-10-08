package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v6ShowFile is a schema-6 show file as the C1 build wrote it: no entry
// carries a "location" key.
const v6ShowFile = `{
  "schemaVersion": 6,
  "name": "C1 show",
  "entries": [
    {"id":"e1","name":"Spot 1","footprint":8,"universe":0,"startAddress":1,"position":"LX 1","phaseCount":0,
     "channelFunctions":{},"wheels":[],"wheelsKnown":false}
  ]
}`

// TestMigrate_V6FileLoadsWithLocationUnknown: a pre-C2 show opens with every
// entry's world location explicitly unknown — on the wire, every key present
// (no omitempty on a meaningful zero) — and the free-text Position label is
// untouched. Nothing is derived from the label.
func TestMigrate_V6FileLoadsWithLocationUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(path, []byte(v6ShowFile), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := NewStore(path).Get()
	if !ok || len(p.Entries) != 1 {
		t.Fatalf("v6 show did not load: ok=%v", ok)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`"schemaVersion":7`,
		`"position":"LX 1"`,
		`"location":{"known":false,"x":0,"y":0,"z":0,"rotationKnown":false,"rotX":0,"rotY":0,"rotZ":0}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("migrated v6 show lacks %s:\n%s", want, got)
		}
	}
}

// TestLocation_SurvivesSaveAndReload: a known location (including a real
// zero coordinate, which omitempty would erase) round-trips through the
// on-disk show file.
func TestLocation_SurvivesSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.json")
	var e Entry
	if err := json.Unmarshal([]byte(`{"id":"e1","startAddress":1,"location":{"known":true,"x":0,"y":-2500.5,"z":6000,"rotationKnown":true,"rotX":0,"rotY":-90,"rotZ":0}}`), &e); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).ReplaceChecked(Patch{Name: "S", Entries: []Entry{e}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `"location":{"known":true,"x":0,"y":-2500.5,"z":6000,"rotationKnown":true,"rotX":0,"rotY":-90,"rotZ":0}`
	if !strings.Contains(strings.Join(strings.Fields(string(data)), ""), want) {
		t.Errorf("saved show lacks %s:\n%s", want, data)
	}
	p, _ := NewStore(path).Get()
	b, _ := json.Marshal(p.Entries[0])
	if !strings.Contains(string(b), want) {
		t.Errorf("reloaded entry lacks %s:\n%s", want, b)
	}
}
