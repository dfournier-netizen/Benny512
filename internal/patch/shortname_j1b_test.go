package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// TestShortTypeName (J1b, owner 2026-10-12 20:11): fader labels use a short
// fixture-type name — the GDTF FixtureType ShortName when the importer kept
// it, otherwise the type with a leading "Custom" and a known manufacturer
// stripped. A name with no known manufacturer is kept whole (no guessing).
func TestShortTypeName(t *testing.T) {
	cases := []struct{ typ, stored, want string }{
		// Dom's show (imported before short names were kept).
		{"Custom GLP JDC1 Strobe", "", "JDC1 Strobe"},
		{"Martin Professional ERA 800 Performance", "", "ERA 800 Performance"},
		{"Elation Proteus Rayzor 1960", "", "Proteus Rayzor 1960"},
		{"Chauvet Professional Rogue Outcast 2X Wash", "", "Rogue Outcast 2X Wash"},
		{"Custom Elation Paladin Cube", "", "Paladin Cube"},
		// Real GDTF manufacturer attributes (Robe extracts).
		{"Robe lighting s.r.o. BMFL Spot", "", "BMFL Spot"},
		{"High End Systems SolaFrame 750", "", "SolaFrame 750"},
		// The GDTF ShortName wins (real Paladin Cube extract: "PalCube").
		{"Elation Paladin Cube", "PalCube", "PalCube"},
		{"Custom GLP JDC1 Strobe", "Custom JDC1", "JDC1"},
		// Unknown manufacturer: kept whole.
		{"Test JDC-like strobe", "", "Test JDC-like strobe"},
		{"Practical LED", "", "Practical LED"},
		{"", "", ""},
		// Nothing left after stripping: the whole name.
		{"Custom", "", "Custom"},
		{"GLP", "", "GLP"},
	}
	for _, c := range cases {
		if got := ShortTypeName(c.typ, c.stored); got != c.want {
			t.Errorf("ShortTypeName(%q, %q) = %q, want %q", c.typ, c.stored, got, c.want)
		}
	}
}

// TestShowFileKeepsShortName: the GDTF short name travels in the show
// file's profile cache (optional "shortName"; absent = not known), and a
// profile without one is written byte-for-byte as before, so older show
// files keep their profile keys.
func TestShowFileKeepsShortName(t *testing.T) {
	cf := map[uint16]ChannelFunction{1: {Source: SourceGDTF, Attribute: "Dimmer"}}
	p := Patch{SchemaVersion: CurrentSchemaVersion, Entries: []Entry{
		{ID: "a", FixtureType: "Elation Paladin Cube", Mode: "Cells 24CH", FixtureTypeShortName: "PalCube", ChannelFunctions: cf, Wheels: []Wheel{}},
		{ID: "b", FixtureType: "Elation Paladin Cube", Mode: "RGB 3CH", ChannelFunctions: cf, Wheels: []Wheel{}},
	}}
	b, err := encodeShowFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Profiles map[string]map[string]any
		Entries  []map[string]any
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatal(err)
	}
	with, without := 0, 0
	for _, pr := range disk.Profiles {
		if pr["shortName"] == "PalCube" {
			with++
		} else if _, ok := pr["shortName"]; !ok {
			without++
		}
	}
	if with != 1 || without != 1 {
		t.Errorf("profiles %v: want one with shortName PalCube and one without the key", disk.Profiles)
	}
	for _, e := range disk.Entries {
		if _, ok := e["fixtureTypeShortName"]; ok {
			t.Errorf("entry %v carries the short name itself; it belongs in its profile", e["id"])
		}
	}
	// The profile without a short name hashes exactly as before J1b.
	old, _ := json.Marshal(struct {
		FixtureType      string                     `json:"fixtureType"`
		Mode             string                     `json:"mode"`
		ChannelFunctions map[uint16]ChannelFunction `json:"channelFunctions"`
		Wheels           []Wheel                    `json:"wheels"`
		WheelsKnown      bool                       `json:"wheelsKnown"`
	}{"Elation Paladin Cube", "RGB 3CH", cf, []Wheel{}, false})
	sum := sha256.Sum256(old)
	if !strings.Contains(string(b), hex.EncodeToString(sum[:])) {
		t.Errorf("the profile without a short name changed its key")
	}
	back, err := decodeShowFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Entries[0].FixtureTypeShortName != "PalCube" || back.Entries[1].FixtureTypeShortName != "" {
		t.Errorf("decoded short names %q, %q; want PalCube and none", back.Entries[0].FixtureTypeShortName, back.Entries[1].FixtureTypeShortName)
	}
}

// TestShowFileKeepsGeometryTree (J1c): a mode's geometry tree travels in
// the show file's profile cache (optional "geometryParents"; absent = not
// known, a legacy profile), and a profile without one keeps its key.
func TestShowFileKeepsGeometryTree(t *testing.T) {
	cf := map[uint16]ChannelFunction{1: {Source: SourceGDTF, Attribute: "Dimmer", GeometryInstance: "Head:0"}}
	tree := map[string]string{"Base:0": "", "Head:0": "Base:0"}
	p := Patch{SchemaVersion: CurrentSchemaVersion, Entries: []Entry{
		{ID: "a", FixtureType: "T", Mode: "M", GeometryParents: tree, ChannelFunctions: cf, Wheels: []Wheel{}},
		{ID: "b", FixtureType: "T", Mode: "N", ChannelFunctions: cf, Wheels: []Wheel{}},
	}}
	b, err := encodeShowFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Profiles map[string]map[string]any
	}
	_ = json.Unmarshal(b, &disk)
	with, without := 0, 0
	for _, pr := range disk.Profiles {
		if gp, ok := pr["geometryParents"].(map[string]any); ok && gp["Head:0"] == "Base:0" {
			with++
		} else if _, ok := pr["geometryParents"]; !ok {
			without++
		}
	}
	if with != 1 || without != 1 {
		t.Errorf("profiles %v: want one with the tree and one without the key", disk.Profiles)
	}
	back, err := decodeShowFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Entries[0].GeometryParents["Head:0"] != "Base:0" || back.Entries[1].GeometryParents != nil {
		t.Errorf("decoded trees %v, %v; want the tree and none", back.Entries[0].GeometryParents, back.Entries[1].GeometryParents)
	}
}
