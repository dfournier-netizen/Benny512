package web

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file holds the ONE test in this package that closes the Go/JS seam on
// entryRequest.channelFunctions.
//
// Why it has to exist, in the words of the bug it was written for:
// TestChannelFunctionImport_PreservesGDTFDefaultAndHighlight (patch_test.go)
// proves the SERVER preserves a GDTF resting value — but it builds its
// request out of the Go struct `channelFunctionRequest`, so it can only ever
// prove the server agrees with itself. gdtf_footprint_test.js proves the
// BROWSER parser reads Default/Highlight out of a GDTF file — but it reads
// the parser's own output back with the parser's own key names, so it can
// only ever prove the browser agrees with itself. Both passed while the two
// halves used different key names (`defaultValue`/`highlightValue` in
// gdtfparse.js, `default`/`highlight` in channelFunctionRequest), and
// because internal/web's decodeJSON calls DisallowUnknownFields, EVERY real
// GDTF apply (PUT /api/patch/entries/{id}) and EVERY MVR import that
// resolved even one channel function failed outright with
//
//	400 {"error":"json: unknown field \"defaultValue\""}
//
// Nothing on either side could catch that, because neither side ever looked
// at the other's bytes. This test does: it runs the literal gdtfparse.js the
// browser loads, takes the literal channelFunctions object it produces, and
// feeds those exact bytes to the literal HTTP handler the browser posts to.
//
// Same Node prerequisite (and same skip-not-fail policy) as
// TestGdtfParseFootprintGroundTruth in gdtfparse_footprint_test.go.

// gdtfWireHarnessJS parses a small GDTF with a stated 8-bit Default of 0 (a
// real resting value, the case a "non-zero means known" heuristic gets
// wrong), a non-zero Default plus a Highlight, and a 16-bit Default, then
// prints ONLY the channelFunctions map — the exact object patch.js's
// runGdtfApply and mvrimport.js's buildEntries put on the wire verbatim.
const gdtfWireHarnessJS = `'use strict';
const fs = require('fs');
const vm = require('vm');
const { DOMParser } = require(process.argv[2]);
const src = fs.readFileSync(process.argv[3], 'utf8');
const GdtfParse = vm.runInContext(src + '\nGdtfParse;', vm.createContext({ DOMParser, console }), { filename: 'gdtfparse.js' });
const xml = ` + "`" + `<?xml version="1.0" encoding="UTF-8"?>
<GDTF><FixtureType Manufacturer="Acme" Name="Wire Test">
<DMXModes><DMXMode Name="Std" Geometry="Body"><DMXChannels>
<DMXChannel DMXBreak="1" Geometry="Body" Offset="1">
  <LogicalChannel Attribute="Dimmer">
    <ChannelFunction Name="Dimmer" Attribute="Dimmer" DMXFrom="0/1" Default="0/1" PhysicalFrom="0" PhysicalTo="100"/>
  </LogicalChannel></DMXChannel>
<DMXChannel DMXBreak="1" Geometry="Body" Offset="2">
  <LogicalChannel Attribute="Shutter1">
    <ChannelFunction Name="Shutter" Attribute="Shutter1" DMXFrom="0/1" Default="255/1" Highlight="255/1">
      <ChannelSet Name="Closed" DMXFrom="0/1"/><ChannelSet Name="Open" DMXFrom="32/1"/>
    </ChannelFunction>
  </LogicalChannel></DMXChannel>
<DMXChannel DMXBreak="1" Geometry="Body" Offset="3,4">
  <LogicalChannel Attribute="Pan">
    <ChannelFunction Name="Pan" Attribute="Pan" DMXFrom="0/1" Default="32768/2"/>
  </LogicalChannel></DMXChannel>
</DMXChannels></DMXMode></DMXModes>
<Geometries><Geometry Name="Body"/></Geometries>
</FixtureType></GDTF>` + "`" + `;
const mode = GdtfParse.parseDescriptionXml(xml).modes[0];
process.stdout.write(JSON.stringify(mode.channelFunctions));
`

// gdtfParseChannelFunctionsJSON runs gdtfparse.js under Node and returns the
// raw JSON bytes of the channelFunctions object it produced.
func gdtfParseChannelFunctionsJSON(t *testing.T) []byte {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping gdtfparse.js wire-shape test")
	}
	abs, err := filepath.Abs("static/js")
	if err != nil {
		t.Fatalf("abs static/js: %v", err)
	}
	script := filepath.Join(t.TempDir(), "wire.js")
	if err := os.WriteFile(script, []byte(gdtfWireHarnessJS), 0o644); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	cmd := exec.Command(nodePath, script,
		filepath.Join(abs, "testdata", "tinydom.js"),
		filepath.Join(abs, "gdtfparse.js"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running gdtfparse.js under node failed: %v\n%s", err, out)
	}
	return out
}

// TestGdtfParseChannelFunctions_AreAcceptedByTheEntryEndpoint is the seam
// test: gdtfparse.js's OWN output bytes, through the REAL handler.
func TestGdtfParseChannelFunctions_AreAcceptedByTheEntryEndpoint(t *testing.T) {
	raw := gdtfParseChannelFunctionsJSON(t)

	h := newHarness(t)
	// Exactly the body patch.js's runGdtfApply builds: the draft's own
	// fields plus the parser's channelFunctions object, unmodified.
	body := map[string]any{
		"name": "Wire Test 1", "fixtureType": "Acme Wire Test", "mode": "Std",
		"footprint": 4, "universe": 0, "startAddress": 1,
		"position": "", "fixtureNumber": "", "notes": "",
		"channelFunctions": json.RawMessage(raw),
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/patch/entries with gdtfparse.js's own channelFunctions: status=%d body=%s\n"+
			"the browser parser and channelFunctionRequest disagree on a key name; decodeJSON's "+
			"DisallowUnknownFields rejects the WHOLE request, so every GDTF apply and MVR import fails.\n"+
			"parser produced: %s", rr.Code, rr.Body.String(), raw)
	}

	// ...and the resting values must actually be on the wire coming back,
	// with hasDefault carrying the "the file said 0" signal. Asserted on the
	// marshalled bytes per this project's serialization-test rule.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	got := rr.Body.String()
	for _, want := range []string{
		// offset 1: Default="0/1" — a KNOWN zero.
		`"hasDefault":true`, `"default":0`, `"defaultByteCount":1`,
		// offset 2: Default="255/1" plus Highlight="255/1".
		`"default":255`, `"hasHighlight":true`, `"highlight":255`, `"highlightByteCount":1`,
		// offsets 3+4: a 16-bit default carried as the full value.
		`"default":32768`, `"defaultByteCount":2`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("GET /api/patch is missing %s — a GDTF resting value parsed by the browser "+
				"did not survive the trip to the server; got %s", want, got)
		}
	}
}
