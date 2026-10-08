package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v5ShowFile is a schema-5 show file as the pre-C1 build wrote it: a GDTF-
// sourced entry with first-function channel data (a Default, ChannelSets)
// and an RDM-inferred entry. Neither carries any of C1's keys.
const v5ShowFile = `{
  "schemaVersion": 5,
  "name": "Old show",
  "entries": [
    {"id":"e1","name":"BMFL 1","fixtureType":"Robe lighting s.r.o. BMFL Spot","mode":"Mode 1 - Standard 16 bit",
     "footprint":41,"universe":0,"startAddress":1,"phaseCount":0,
     "channelFunctions":{
       "39":{"source":"gdtf","attribute":"Shutter1","functionName":"Shutter1","dmxFrom":0,"dmxTo":63,
             "physicalFrom":0,"physicalTo":1,
             "channelSets":[{"name":"Shutter closed","dmxFrom":0,"physicalFrom":0,"physicalTo":0},
                            {"name":"Shutter open","dmxFrom":32,"physicalFrom":0,"physicalTo":0}],
             "hasDefault":true,"default":32,"defaultByteCount":1,
             "hasHighlight":false,"highlight":0,"highlightByteCount":0}}},
    {"id":"e2","name":"Par","footprint":4,"universe":0,"startAddress":50,
     "channelFunctions":{"1":{"source":"rdm-inferred","attribute":"Dimmer","dmxFrom":0,"dmxTo":0,
       "physicalFrom":0,"physicalTo":0,"channelSets":[],"rdmSlotType":"primary","rdmSlotLabel":"intensity"}}}
  ]
}`

// TestMigrate_V5FileLoadsWithChannelDetailNotImported: an old show must open
// with every entry marked "full channel detail not imported" — explicitly,
// on the wire — and with nothing fabricated: no function ranges invented
// from the first-function fields, no wheels. Asserted on the marshalled
// bytes, per this package's serialization-test rule.
func TestMigrate_V5FileLoadsWithChannelDetailNotImported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(path, []byte(v5ShowFile), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	p, ok := st.Get()
	if !ok || len(p.Entries) != 2 {
		t.Fatalf("v5 show did not load: ok=%v entries=%d", ok, len(p.Entries))
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		fmt.Sprintf(`"schemaVersion":%d`, CurrentSchemaVersion),
		`"functionsKnown":false`,
		`"functions":[]`,
		`"byteCount":0`,
		`"byteIndex":0`,
		`"wheels":[]`,
		`"wheelsKnown":false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("migrated v5 show is missing %s — an old entry must say explicitly that its channel detail was never imported\n%s", want, got)
		}
	}
	if strings.Contains(got, `"functionsKnown":true`) {
		t.Errorf("a v5 entry came back claiming full channel detail it never had:\n%s", got)
	}
	// The first-function data Rig Check reads is untouched.
	for _, want := range []string{
		`"attribute":"Shutter1"`, `"dmxTo":63`, `"name":"Shutter open","dmxFrom":32`,
		`"hasDefault":true,"default":32,"defaultByteCount":1`,
		`"rdmSlotLabel":"intensity"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("migration changed first-function data: missing %s\n%s", want, got)
		}
	}
}
