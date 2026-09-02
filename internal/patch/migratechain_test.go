package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateChain_V2FileLoadsThroughEveryHop closes a SEAM, not a single
// migration step. v2->v3 (GDTF channel defaults) and v3->v4 (Reconcile's
// Intended/AsFound) were written by different agents in different waves, and
// each verified its OWN hop against a file one version behind it. Nobody ever
// loaded a genuinely v2 file through BOTH hops in sequence — which is exactly
// the shape of thing that passes two isolated verifications and is still
// broken end to end.
//
// It matters here because both hops are deliberate no-ops that rely on Go's
// zero values being the correct "we were never told this" state. That
// reasoning is only sound if nothing between them writes a value; a future
// hop that DOES rewrite entries would silently invalidate both, and this test
// is what would catch it.
//
// The fixture is hand-written v2 JSON, not a struct marshalled at v2 — a
// struct built from today's types would carry today's keys and prove nothing.
const v2PatchFile = `{
  "schemaVersion": 2,
  "name": "Shop rig",
  "entries": [
    {
      "id": "e1",
      "name": "Wash 1",
      "fixtureType": "GLP JDC1",
      "mode": "Mode 4 SPix PRO (62ch)",
      "footprint": 62,
      "universe": 0,
      "startAddress": 1,
      "channelFunctions": {
        "0": {
          "source": "gdtf",
          "attribute": "Dimmer",
          "functionName": "Dimmer",
          "dmxFrom": 0,
          "dmxTo": 255,
          "channelSets": []
        },
        "1": {
          "source": "gdtf",
          "attribute": "Shutter1",
          "functionName": "Shutter",
          "dmxFrom": 0,
          "dmxTo": 255,
          "channelSets": [{"name": "Open", "dmxFrom": 32}]
        }
      }
    }
  ]
}`

func TestMigrateChain_V2FileLoadsThroughEveryHop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.json")
	if err := os.WriteFile(path, []byte(v2PatchFile), 0o600); err != nil {
		t.Fatal(err)
	}

	st := NewStore(path)
	p, ok := st.Get()
	if !ok {
		t.Fatal("NewStore must load a v2 file; it loaded nothing (a parse failure is silently tolerated here)")
	}

	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schemaVersion = %d after migration, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(p.Entries))
	}
	e := p.Entries[0]

	// Real v2 data must survive untouched.
	if e.Footprint != 62 || e.StartAddress != 1 || e.Mode != "Mode 4 SPix PRO (62ch)" {
		t.Errorf("v2 data was altered by migration: footprint=%d start=%d mode=%q", e.Footprint, e.StartAddress, e.Mode)
	}
	if len(e.ChannelFunctions) != 2 {
		t.Fatalf("channelFunctions = %d, want 2", len(e.ChannelFunctions))
	}

	// v2->v3: defaults must read UNKNOWN, never a silent resting 0.
	for off, cf := range e.ChannelFunctions {
		if cf.HasDefault {
			t.Errorf("offset %d: HasDefault = true on a v2 file — a resting value was invented", off)
		}
		if cf.HasHighlight {
			t.Errorf("offset %d: HasHighlight = true on a v2 file — a highlight value was invented", off)
		}
		if cf.ChannelSets == nil {
			t.Errorf("offset %d: ChannelSets is nil after migration; nil marshals as JSON null", off)
		}
	}

	// v3->v4: never committed, nothing ever read off a fixture.
	if e.AsFound.UID != "" {
		t.Errorf("AsFound.UID = %q on a v2 file, want empty — a pairing was invented", e.AsFound.UID)
	}
	if e.ConfirmedUID != "" {
		t.Errorf("ConfirmedUID = %q on a v2 file, want empty", e.ConfirmedUID)
	}

	// The whole point: assert the MARSHALLED BYTES, because a struct field
	// reading false is also what an omitted key produces. Both hops' "we were
	// never told" markers must be visibly present on the wire, so the client
	// can distinguish them from a real zero.
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`"hasDefault":false`,
		`"hasHighlight":false`,
		`"channelSets":[`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("migrated v2 patch JSON is missing %s\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, `"channelFunctions":null`) || strings.Contains(got, `"channelSets":null`) {
		t.Errorf("a nil map/slice reached the wire as null\ngot: %s", got)
	}
}
