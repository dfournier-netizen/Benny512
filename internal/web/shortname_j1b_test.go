package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"
)

// TestJ1b_ShortNameFromGDTF (owner 2026-10-12 20:11): fader labels use the
// GDTF FixtureType ShortName. The literal gdtfparse.js reads it from the
// real vendor files; the literal mvrimport.js sends it with each entry; the
// real import handler keeps it (a plain field edit does not drop it); GET
// /api/faders gives it as the type fader's shortLabel beside the full
// label.
func TestJ1b_ShortNameFromGDTF(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH")
	}
	cmd := exec.Command(node, "static/js/testdata/gdtf_shortname_dump.js")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("dump: %v\n%s", err, stderr.String())
	}
	var d struct {
		ShortNames map[string]string
		Entries    []json.RawMessage
	}
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("dump is not JSON: %v\n%s", err, out.String())
	}
	// Read off the vendor XML by hand: <FixtureType ... ShortName="...">.
	want := map[string]string{"paladin_cube_real_extract.xml": "PalCube", "robe_bmfl_spot_real_extract.xml": "BMFL Spot", "robe_ledbeam100_real_extract.xml": "100 LEDBeam"}
	for f, w := range want {
		if d.ShortNames[f] != w {
			t.Errorf("gdtfparse.js short name of %s = %q, want %q", f, d.ShortNames[f], w)
		}
	}
	h := newHarness(t)
	entries := importDump(t, h, out.Bytes(), "fresh")
	if len(entries) == 0 {
		t.Fatalf("nothing imported")
	}
	for _, e := range entries {
		if e["fixtureTypeShortName"] != "PalCube" {
			t.Errorf("entry %v: fixtureTypeShortName %v, want PalCube", e["name"], e["fixtureTypeShortName"])
		}
	}
	// A plain field edit (no channelFunctions) keeps it.
	id := entries[0]["id"].(string)
	if rr := doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, map[string]any{"name": "Renamed", "fixtureType": entries[0]["fixtureType"], "mode": entries[0]["mode"],
		"footprint": entries[0]["footprint"], "universe": entries[0]["universe"], "startAddress": entries[0]["startAddress"]}); rr.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rr.Code, rr.Body.String())
	}
	var fv struct {
		Faders []struct{ ID, Kind, Label, ShortLabel string }
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/faders", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &fv); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fv.Faders {
		if f.Kind == "type" {
			found = true
			if f.Label != "Elation Paladin Cube" || f.ShortLabel != "PalCube" {
				t.Errorf("type fader label %q short %q, want the full name and PalCube", f.Label, f.ShortLabel)
			}
		}
	}
	if !found {
		t.Errorf("no type fader")
	}
}

// TestJ1b_ShortLabelDerivedAndModes: with no stored short name the label is
// derived (a leading "Custom" and a known manufacturer stripped); two modes
// of one type stay apart by their footprint ("24ch" / "3ch").
func TestJ1b_ShortLabelDerivedAndModes(t *testing.T) {
	r := newG2Rig(t)
	var fv struct {
		Faders []struct{ ID, Kind, Label, ShortLabel string }
	}
	r.get(t, "/api/faders", &fv)
	got := map[string]string{}
	for _, f := range fv.Faders {
		if f.Kind == "type" {
			got[f.Label] = f.ShortLabel
		}
	}
	for label, w := range map[string]string{"Elation Paladin Cube — Cells 24CH": "Paladin Cube — 24ch", "Elation Paladin Cube — RGB 3CH": "Paladin Cube — 3ch"} {
		if got[label] != w {
			t.Errorf("fader %q short label %q, want %q (all: %v)", label, got[label], w, got)
		}
	}
}
