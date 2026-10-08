package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"benny512/internal/patch"
)

// Console-lite C1: full GDTF channel detail. These tests close the three
// seams the change crosses — the literal browser parser against real vendor
// files, the parser's own bytes through the real HTTP decoder, and Rig
// Check's inputs, which must not move. Same Node prerequisite and
// skip-not-fail policy as gdtfparse_wire_test.go.

func nodeOrSkip(t *testing.T) string {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping a gdtfparse.js seam test")
	}
	return nodePath
}

// TestGdtfChannelDetail_RealVendorFiles runs gdtf_channeldetail_test.js
// against the two verbatim Robe extracts (and its labelled spec-derived
// fixture). See that file's header for provenance.
func TestGdtfChannelDetail_RealVendorFiles(t *testing.T) {
	nodePath := nodeOrSkip(t)
	cmd := exec.Command(nodePath, "static/js/testdata/gdtf_channeldetail_test.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("gdtf_channeldetail_test.js failed: %v\n--- stderr ---\n%s\n--- stdout ---\n%s", err, stderr.String(), stdout.String())
	}
	t.Log(stdout.String())
}

// channelDetailHarnessJS parses one vendor extract with the literal
// gdtfparse.js and prints the entry body a GDTF apply sends for its first
// mode — the parser's channelFunctions and wheels objects, unmodified.
const channelDetailHarnessJS = `'use strict';
const fs = require('fs');
const vm = require('vm');
const { DOMParser } = require(process.argv[2]);
const GdtfParse = vm.runInContext(fs.readFileSync(process.argv[3], 'utf8') + '\nGdtfParse;', vm.createContext({ DOMParser, console }), { filename: 'gdtfparse.js' });
const parsed = GdtfParse.parseDescriptionXml(fs.readFileSync(process.argv[4], 'utf8'));
const mode = parsed.modes[0];
process.stdout.write(JSON.stringify({
  name: 'BMFL 1', fixtureType: parsed.fixtureType, mode: mode.name, footprint: mode.footprint,
  universe: 0, startAddress: 1, position: '', fixtureNumber: '', notes: '',
  channelFunctions: mode.channelFunctions, wheels: mode.wheels, wheelsKnown: true,
}));
`

func parseVendorEntryBody(t *testing.T, extract string) []byte {
	t.Helper()
	nodePath := nodeOrSkip(t)
	abs, err := filepath.Abs("static/js")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "detail.js")
	if err := os.WriteFile(script, []byte(channelDetailHarnessJS), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nodePath, script,
		filepath.Join(abs, "testdata", "tinydom.js"),
		filepath.Join(abs, "gdtfparse.js"),
		filepath.Join(abs, "testdata", extract)).CombinedOutput()
	if err != nil {
		t.Fatalf("running gdtfparse.js under node: %v\n%s", err, out)
	}
	return out
}

// containsAll reports whether got carries every key/value want carries,
// recursively (arrays element by element, same length). Extra keys on the
// server side (fields the parser does not produce, e.g. rdmSlotType) are
// allowed; a missing or different one is a field lost on the trip.
func containsAll(want, got any, path string, t *testing.T) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: server returned %T, parser sent an object", path, got)
			return
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present && wv == "" && preexistingOmitEmpty[k] && (k != "name" || strings.Contains(path, ".channelSets[")) {
				// A pre-C1 string field tagged `omitempty` (e.g. an unnamed
				// legacy ChannelSet): "" comes back absent. Pre-existing,
				// and deliberately not changed here — those bytes are what
				// stored verification stamps and baseline digests hash.
				continue
			}
			if !present {
				t.Errorf("%s.%s: sent by the parser, missing from the server's JSON", path, k)
				continue
			}
			containsAll(wv, gv, path+"."+k, t)
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: parser sent %d items, server returned %v", path, len(w), got)
			return
		}
		for i := range w {
			containsAll(w[i], g[i], path+"["+jsonInt(i)+"]", t)
		}
	default:
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: parser sent %v, server returned %v", path, want, got)
		}
	}
}

// preexistingOmitEmpty are the pre-C1 string keys of patch.ChannelFunction
// and patch.ChannelSet that carry `omitempty`. None of C1's keys do.
var preexistingOmitEmpty = map[string]bool{"name": true, "geometryInstance": true, "attribute": true, "functionName": true, "rdmSlotType": true, "rdmSlotLabel": true}

func jsonInt(i int) string { b, _ := json.Marshal(i); return string(b) }

// TestGdtfChannelDetail_RoundTripsThroughEntryEndpoint: the literal parser's
// output for a real vendor file, posted to the real handler, comes back with
// every function, set, wheel and slot intact.
func TestGdtfChannelDetail_RoundTripsThroughEntryEndpoint(t *testing.T) {
	raw := parseVendorEntryBody(t, "robe_bmfl_spot_real_extract.xml")
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("parser output is not JSON: %v", err)
	}

	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", json.RawMessage(raw))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/patch/entries with gdtfparse.js's own channel detail: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var got struct {
		Patch struct {
			Entries []map[string]any `json:"entries"`
		} `json:"patch"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got.Patch.Entries) != 1 {
		t.Fatalf("GET /api/patch: %v %s", err, rr.Body.String())
	}
	entry := got.Patch.Entries[0]
	containsAll(sent["channelFunctions"], entry["channelFunctions"], "channelFunctions", t)
	containsAll(sent["wheels"], entry["wheels"], "wheels", t)
	if entry["wheelsKnown"] != true {
		t.Errorf("wheelsKnown = %v after a GDTF apply, want true", entry["wheelsKnown"])
	}

	// A plain field edit (no channelFunctions in the body) must not wipe the
	// profile — the rule ChannelFunctions already follows, extended to wheels.
	id, _ := entry["id"].(string)
	rr = doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, map[string]any{
		"name": "renamed", "fixtureType": sent["fixtureType"], "mode": sent["mode"], "footprint": sent["footprint"],
		"universe": 0, "startAddress": 1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("plain PUT: %d %s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	containsAll(sent["wheels"], got.Patch.Entries[0]["wheels"], "wheels after plain edit", t)
	if got.Patch.Entries[0]["wheelsKnown"] != true {
		t.Error("a plain field edit cleared wheelsKnown")
	}
}

// TestGdtfChannelDetail_RejectsInconsistentKnownFlag: "functionsKnown" is
// the only signal that separates "this channel has these functions" from
// "nobody imported them". A body that claims one and carries the other is
// refused rather than stored.
func TestGdtfChannelDetail_RejectsInconsistentKnownFlag(t *testing.T) {
	h := newHarness(t)
	for name, cf := range map[string]map[string]any{
		"known without functions":     {"source": "gdtf", "attribute": "Dimmer", "functionsKnown": true},
		"functions but not known":     {"source": "gdtf", "attribute": "Dimmer", "functionsKnown": false, "functions": []any{map[string]any{"attribute": "Dimmer"}}},
		"wheels but wheels not known": nil,
	} {
		body := map[string]any{"name": "x", "footprint": 1, "universe": 0, "startAddress": 1}
		if cf != nil {
			body["channelFunctions"] = map[string]any{"1": cf}
		} else {
			body["wheels"] = []any{map[string]any{"name": "Color1", "slots": []any{}}}
			body["wheelsKnown"] = false
		}
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, rr.Code, rr.Body.String())
		}
	}
}

// legacyKeys are the keys C1 added to a channelFunctions record. Stripping
// them from today's parser output must give back, byte for byte in value,
// what the parser produced before C1 (golden captured from the pre-change
// gdtfparse.js) — the fields Rig Check reads.
var c1ChannelFunctionKeys = []string{"byteCount", "byteIndex", "functionsKnown", "functions"}

// TestGdtfChannelDetail_LegacyFieldsUnchanged is a NO-CHANGE guard, not a
// fail-first test: it passes against the pre-C1 parser by construction (the
// golden file was generated from it) and exists so a later edit to the
// parser cannot quietly move a field Rig Check depends on.
func TestGdtfChannelDetail_LegacyFieldsUnchanged(t *testing.T) {
	goldenBytes, err := os.ReadFile("static/js/testdata/robe_extracts_legacy_channelfunctions.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string][]struct {
		Name             string                    `json:"name"`
		Footprint        int                       `json:"footprint"`
		ChannelFunctions map[string]map[string]any `json:"channelFunctions"`
	}
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		t.Fatal(err)
	}
	for file, modes := range golden {
		var body struct {
			Mode             string                    `json:"mode"`
			Footprint        int                       `json:"footprint"`
			ChannelFunctions map[string]map[string]any `json:"channelFunctions"`
		}
		if err := json.Unmarshal(parseVendorEntryBody(t, file), &body); err != nil {
			t.Fatal(err)
		}
		want := modes[0]
		if body.Mode != want.Name || body.Footprint != want.Footprint {
			t.Errorf("%s: mode/footprint %q/%d, golden %q/%d", file, body.Mode, body.Footprint, want.Name, want.Footprint)
		}
		if len(body.ChannelFunctions) != len(want.ChannelFunctions) {
			t.Errorf("%s: %d channelFunctions, golden %d", file, len(body.ChannelFunctions), len(want.ChannelFunctions))
		}
		for off, wcf := range want.ChannelFunctions {
			gcf := body.ChannelFunctions[off]
			for _, k := range c1ChannelFunctionKeys {
				delete(gcf, k)
			}
			if !reflect.DeepEqual(gcf, wcf) {
				gb, _ := json.Marshal(gcf)
				wb, _ := json.Marshal(wcf)
				t.Errorf("%s offset %s: legacy fields moved\n got  %s\n want %s", file, off, gb, wb)
			}
		}
	}
}

// TestRigCheckAvailability_UnchangedByChannelDetail: Rig Check reads the
// first-function fields only, so a real profile carrying full detail must
// offer exactly the tests the same profile without detail offers.
func TestRigCheckAvailability_UnchangedByChannelDetail(t *testing.T) {
	raw := parseVendorEntryBody(t, "robe_bmfl_spot_real_extract.xml")
	var withDetail patch.Entry
	if err := json.Unmarshal(raw, &withDetail); err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	for _, cf := range generic["channelFunctions"].(map[string]any) {
		for _, k := range c1ChannelFunctionKeys {
			delete(cf.(map[string]any), k)
		}
	}
	delete(generic, "wheels")
	delete(generic, "wheelsKnown")
	stripped, _ := json.Marshal(generic)
	var without patch.Entry
	if err := json.Unmarshal(stripped, &without); err != nil {
		t.Fatal(err)
	}
	withDetail.ID, without.ID = "e1", "e1"
	a, b := patch.AvailableTests([]patch.Entry{withDetail}), patch.AvailableTests([]patch.Entry{without})
	if len(a) == 0 {
		t.Fatal("no Rig Check tests available for a real BMFL profile — the comparison below would prove nothing")
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Rig Check availability changed with channel detail present:\n with    %+v\n without %+v", a, b)
	}
}
