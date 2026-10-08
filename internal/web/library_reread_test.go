package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// libraryRereadHarnessJS loads the LITERAL api.js, mvrzip.js, gdtfparse.js
// and mvrimport.js into one global scope (as index.html does), points fetch
// at the real Go handler, and runs the Fixture Library's "re-read channel
// detail from the stored GDTF" path: list the library, download the stored
// archive, re-parse it, merge the result back.
const libraryRereadHarnessJS = `'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const [, , base, jsDir] = process.argv;
globalThis.DOMParser = require(path.join(jsDir, 'testdata', 'tinydom.js')).DOMParser;
globalThis.window = { dispatchEvent() {} };
globalThis.CustomEvent = class { constructor(t) { this.type = t; } };
const realFetch = globalThis.fetch;
globalThis.fetch = (p, o) => realFetch(new URL(p, base), o);
for (const f of ['api.js', 'mvrzip.js', 'gdtfparse.js', 'mvrimport.js']) {
  vm.runInThisContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), { filename: f });
}
(async () => {
  const Api = vm.runInThisContext('Api'), MvrImport = vm.runInThisContext('MvrImport');
  const lib = await Api.getLibrary();
  const rec = lib.records[0], src = rec.sourceFiles[0];
  const buf = await Api.getLibrarySourceBytes(rec.key, src.sha256);
  const res = await MvrImport.rereadGdtfIntoLibrary(buf, src.name);
  process.stdout.write(JSON.stringify(res.result));
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
`

// TestLibraryRereadFromStoredArchive_AddsChannelDetail: a library record
// imported before C1 holds the original .gdtf archive but only first-
// function channel data. Re-reading that stored archive through the real
// browser path must leave the record carrying every function, set and wheel.
func TestLibraryRereadFromStoredArchive_AddsChannelDetail(t *testing.T) {
	nodePath := nodeOrSkip(t)
	desc, err := os.ReadFile("static/js/testdata/robe_bmfl_spot_real_extract.xml")
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, _ := zw.Create("description.xml")
	_, _ = w.Write(desc)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t)
	// The record as a pre-C1 build stored it: original archive kept, mode
	// channel map without any of C1's keys.
	doc := map[string]any{
		"format": "benny512-fixture-library", "schemaVersion": 1,
		"records": []any{map[string]any{
			"manufacturer": "Robe lighting s.r.o.", "model": "BMFL Spot",
			"sourceFiles": []any{map[string]any{"name": "Robe_BMFL_Spot.gdtf", "data": archive.Bytes()}},
			"modes": []any{map[string]any{
				"name": "Mode 1 - Standard 16 bit", "footprint": 41,
				"origin":           map[string]any{"source": "gdtf", "detail": "Robe_BMFL_Spot.gdtf"},
				"channelFunctions": map[string]any{"39": map[string]any{"source": "gdtf", "attribute": "Shutter1", "functionName": "Shutter1", "dmxFrom": 0, "dmxTo": 63, "channelSets": []any{}}},
			}},
		}},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/library/import", map[string]any{"mode": "merge", "library": doc}); rr.Code != http.StatusOK {
		t.Fatalf("seed library: %d %s", rr.Code, rr.Body.String())
	}

	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()
	abs, _ := filepath.Abs("static/js")
	script := filepath.Join(t.TempDir(), "reread.js")
	if err := os.WriteFile(script, []byte(libraryRereadHarnessJS), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nodePath, script, ts.URL+"/", abs).CombinedOutput()
	if err != nil {
		t.Fatalf("re-read through the literal browser path failed: %v\n%s", err, out)
	}

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/library/record/"+url.PathEscape("robe lighting s.r.o.|bmfl spot"), nil)
	var rec struct {
		Modes []struct {
			Name        string `json:"name"`
			WheelsKnown bool   `json:"wheelsKnown"`
			Wheels      []struct {
				Name  string `json:"name"`
				Slots []struct {
					Name          string `json:"name"`
					MediaFileName string `json:"mediaFileName"`
					SRGB          string `json:"srgb"`
				} `json:"slots"`
			} `json:"wheels"`
			ChannelFunctions map[string]struct {
				FunctionsKnown bool `json:"functionsKnown"`
				ByteCount      int  `json:"byteCount"`
				Functions      []struct {
					Name  string `json:"name"`
					DMXTo uint32 `json:"dmxTo"`
				} `json:"functions"`
			} `json:"channelFunctions"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil || len(rec.Modes) != 1 {
		t.Fatalf("library record after re-read: %v %s", err, rr.Body.String())
	}
	m := rec.Modes[0]
	shutter := m.ChannelFunctions["39"]
	if !shutter.FunctionsKnown || len(shutter.Functions) != 8 || shutter.Functions[7].DMXTo != 255 {
		t.Errorf("Shutter1 after re-read: functionsKnown=%v, %d functions (want 8, last ending 255): %+v", shutter.FunctionsKnown, len(shutter.Functions), shutter.Functions)
	}
	if dim := m.ChannelFunctions["40"]; !dim.FunctionsKnown || dim.ByteCount != 2 || len(dim.Functions) != 1 || dim.Functions[0].DMXTo != 65535 {
		t.Errorf("16-bit Dimmer after re-read: %+v", dim)
	}
	if len(m.ChannelFunctions) != 41 {
		t.Errorf("re-read mode has %d channel offsets, want 41", len(m.ChannelFunctions))
	}
	if !m.WheelsKnown || len(m.Wheels) != 6 || m.Wheels[2].Name != "Color1" || m.Wheels[2].Slots[1].MediaFileName != "14070411" || m.Wheels[2].Slots[1].SRGB != "#ff392a" {
		t.Errorf("wheels after re-read: known=%v %+v", m.WheelsKnown, m.Wheels)
	}

	// The existing "re-profile selected entries" path must carry the new
	// fields onto a patch entry patched before C1.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", map[string]any{
		"name": "BMFL 1", "fixtureType": "Robe BMFL Spot", "mode": "Mode 1 - Standard 16 bit", "footprint": 41, "universe": 0, "startAddress": 1,
		"channelFunctions": map[string]any{"39": map[string]any{"source": "gdtf", "attribute": "Shutter1", "channelSets": []any{}}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create old-style entry: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct {
			Entries []struct {
				ID               string `json:"id"`
				WheelsKnown      bool   `json:"wheelsKnown"`
				Wheels           []any  `json:"wheels"`
				ChannelFunctions map[string]struct {
					FunctionsKnown bool  `json:"functionsKnown"`
					Functions      []any `json:"functions"`
				} `json:"channelFunctions"`
			} `json:"entries"`
		} `json:"patch"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &pr)
	id := pr.Patch.Entries[0].ID
	if pr.Patch.Entries[0].ChannelFunctions["39"].FunctionsKnown {
		t.Fatal("precondition: the old-style entry should not claim channel detail")
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/library/reprofile", map[string]any{
		"confirm": "REPROFILE", "entryIds": []string{id}, "key": "robe lighting s.r.o.|bmfl spot", "mode": "Mode 1 - Standard 16 bit",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("reprofile: %d %s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &pr)
	e := pr.Patch.Entries[0]
	if cf := e.ChannelFunctions["39"]; !cf.FunctionsKnown || len(cf.Functions) != 8 || !e.WheelsKnown || len(e.Wheels) != 6 {
		t.Errorf("re-profile from the re-read library mode did not carry full detail: functionsKnown=%v functions=%d wheelsKnown=%v wheels=%d",
			cf.FunctionsKnown, len(cf.Functions), e.WheelsKnown, len(e.Wheels))
	}
}
