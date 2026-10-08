package patch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// vendorEntryHarnessJS runs the LITERAL browser gdtfparse.js on one vendor
// extract and prints the first mode as a patch entry body (the shape a GDTF
// apply sends), so Rig Check's base state can be computed from exactly what
// a real import stores.
const vendorEntryHarnessJS = `'use strict';
const fs = require('fs'), vm = require('vm'), path = require('path');
const jsDir = process.argv[2];
const { DOMParser } = require(path.join(jsDir, 'testdata', 'tinydom.js'));
const G = vm.runInContext(fs.readFileSync(path.join(jsDir, 'gdtfparse.js'), 'utf8') + '\nGdtfParse;', vm.createContext({ DOMParser, console }));
const p = G.parseDescriptionXml(fs.readFileSync(path.join(jsDir, 'testdata', process.argv[3]), 'utf8'));
const m = p.modes[0];
process.stdout.write(JSON.stringify({ id: 'e1', name: 'x', fixtureType: p.fixtureType, mode: m.name, footprint: m.footprint,
  universe: 0, startAddress: 1, channelFunctions: m.channelFunctions, wheels: m.wheels, wheelsKnown: true }));
`

func vendorEntry(t *testing.T, extract string) Entry {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH — skipping a gdtfparse.js seam test")
	}
	jsDir, _ := filepath.Abs("../web/static/js")
	script := filepath.Join(t.TempDir(), "entry.js")
	if err := os.WriteFile(script, []byte(vendorEntryHarnessJS), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nodePath, script, jsDir, extract).CombinedOutput()
	if err != nil {
		t.Fatalf("gdtfparse.js under node: %v\n%s", err, out)
	}
	var e Entry
	if err := json.Unmarshal(out, &e); err != nil {
		t.Fatal(err)
	}
	normalizeChannelFunctions([]Entry{e})
	return e
}

// TestRigCheckBaseState_UsesGDTF10ChannelDefaults: GDTF 1.0 states Default on
// <DMXChannel>; 1.1 moved it to <ChannelFunction> (DIN SPEC 15800 revision
// history, Version 1.1). Both real Robe files are 1.0, so before C1b Rig
// Check's base state knew no resting value for any of their channels, and
// the Robin 100 LEDBeam — whose shutter channel sets name no open position —
// was reported "shutter position unknown". With the channel-level Default
// carried into the first-function fields, both get real resting values.
func TestRigCheckBaseState_UsesGDTF10ChannelDefaults(t *testing.T) {
	bmfl := buildEntryBaseState(vendorEntry(t, "robe_bmfl_spot_real_extract.xml"))
	// <DMXChannel Default=...> is present on all 31 DMXChannels of BMFL
	// Mode 1, covering all 41 offsets.
	if bmfl.knownCount != 41 || bmfl.unknownCount != 0 {
		t.Errorf("BMFL Mode 1: %d slots with a known resting value, %d unknown; want 41 / 0", bmfl.knownCount, bmfl.unknownCount)
	}
	gotShutter := false
	for _, d := range bmfl.defaults {
		if d.attribute == "Shutter1" && d.raw == 32 && d.nbytes == 1 {
			gotShutter = true
		}
	}
	if !gotShutter {
		t.Errorf("BMFL Shutter1 resting value 32 (DMXChannel Default=\"32/1\") not in the base state: %+v", bmfl.defaults)
	}

	led := buildEntryBaseState(vendorEntry(t, "robe_ledbeam100_real_extract.xml"))
	if !led.shutterKnown || led.shutterSource != "gdtfDefault" {
		t.Errorf("LEDBeam shutter: known=%v source=%q; want known from gdtfDefault (DMXChannel Default=\"255/1\")", led.shutterKnown, led.shutterSource)
	}
	for _, w := range led.shutter {
		if w.raw != 255 {
			t.Errorf("LEDBeam shutter open value %d, want 255", w.raw)
		}
	}
}

// TestRigCheckBaseState_GDTF12FileUnchanged: a DataVersion 1.2 file (the real
// Elation Paladin Cube extract, defaults on ChannelFunctions, none on any
// DMXChannel) gets exactly the resting values it always had.
func TestRigCheckBaseState_GDTF12FileUnchanged(t *testing.T) {
	bs := buildEntryBaseState(vendorEntry(t, "paladin_cube_real_extract.xml"))
	if bs.knownCount != 24 || bs.unknownCount != 0 {
		t.Errorf("Paladin Cube Cells 24CH: known %d / unknown %d, want 24 / 0 (as before C1b)", bs.knownCount, bs.unknownCount)
	}
}
