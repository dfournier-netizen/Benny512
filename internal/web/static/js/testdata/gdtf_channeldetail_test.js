// gdtf_channeldetail_test.js — full channel detail (Console-lite C1): every
// LogicalChannel/ChannelFunction of every DMXChannel with its DMX range at
// the channel's own resolution, every ChannelSet with its range and wheel
// slot, wheels and their slots, and mode-master dependencies. Run under
// plain Node by internal/web/gdtfparse_channeldetail_test.go (exit 0 = pass),
// loading the LITERAL gdtfparse.js the browser loads.
//
// EVIDENCE. Two real Robe vendor description.xml files, extracted VERBATIM
// (byte-for-byte lines, CRLF kept) from the GDTF archives that ship in the
// GDTF reference implementation's own unit tests
// (github.com/mvrdevelopment/libMVRgdtf, unittest/files/, MVR SDK License):
//
//   robe_bmfl_spot_real_extract.xml   — "Robe lighting s.r.o.@BMFL Spot@
//       cannot assign 0 to frost function.gdtf", GDTF DataVersion 1.0. Kept:
//       the <FixtureType> line, <Wheels>, <Geometries> and DMX mode
//       "Mode 1 - Standard 16 bit". Removed: AttributeDefinitions,
//       PhysicalDescriptions, Models, Mode 2, Revisions, FTPresets,
//       Protocols (nothing in them is read by gdtfparse.js).
//   robe_ledbeam100_real_extract.xml  — "WrongDmxValue.gdtf" (Robe Robin 100
//       LEDBeam), DataVersion 1.0. Kept: <Wheels>, <Geometries>, DMX mode
//       "mode 3". Removed: every other mode and block.
//
// Nothing was rewritten. Every number asserted against these two files is
// read off the vendor XML by hand (the line it comes from is quoted beside
// each check), not restated from what the parser happens to produce.
//
// The SPEC-DERIVED section at the end covers behaviour neither real file
// exercises (DMXValue byte shifting, several LogicalChannels in one
// DMXChannel, an unresolvable ModeMaster). It is labelled as such: it proves
// the parser does what DIN SPEC 15800 / the reference implementation say,
// NOT that any vendor file is shaped that way.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { DOMParser } = require('./tinydom');

const jsDir = path.resolve(__dirname, '..');
const GdtfParse = vm.runInContext(
  fs.readFileSync(path.join(jsDir, 'gdtfparse.js'), 'utf8') + '\nGdtfParse;',
  vm.createContext({ DOMParser, console }), { filename: 'gdtfparse.js' });

let failures = 0;
function check(label, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) { failures++; console.error(`FAIL ${label}: got ${g}, want ${w}`); }
  else console.log(`ok   ${label}: ${g}`);
}
function safe(fn) { try { return fn(); } catch (e) { return 'THREW: ' + e.message; } }

const bmfl = GdtfParse.parseDescriptionXml(fs.readFileSync(path.join(__dirname, 'robe_bmfl_spot_real_extract.xml'), 'utf8'));
const bm = bmfl.modes[0];
const cf = bm.channelFunctions;

// ---- BMFL Spot, Mode 1 — offset 39, Shutter1 (8-bit, 8 functions) -------
// <DMXChannel DMXBreak="1" Default="32/1" ... Offset="39">, one
// LogicalChannel "Shutter1" holding eight ChannelFunctions whose DMXFrom are
// 0, 64, 96, 128, 144, 160, 192, 224 (all "/1").
check('BMFL Shutter1 byteCount (Offset="39")', safe(() => cf[39].byteCount), 1);
check('BMFL Shutter1 byteIndex', safe(() => cf[39].byteIndex), 0);
check('BMFL Shutter1 functionsKnown', safe(() => cf[39].functionsKnown), true);
check('BMFL Shutter1 every ChannelFunction, document order', safe(() => cf[39].functions.map(f => f.name)),
  ['Shutter1', 'Shutter1Strobe', 'Shutter 1', 'Shutter1StrobePulseOpen', 'Shutter1StrobePulseClose',
    'Shutter1StrobeEffect', 'Shutter1StrobeRandom', 'Shutter 2']);
check('BMFL Shutter1 function ranges', safe(() => cf[39].functions.map(f => [f.dmxFrom, f.dmxTo])),
  [[0, 63], [64, 95], [96, 127], [128, 143], [144, 159], [160, 191], [192, 223], [224, 255]]);
check('BMFL Shutter1 logicalAttribute', safe(() => cf[39].functions[0].logicalAttribute), 'Shutter1');
check('BMFL Shutter1Strobe attribute', safe(() => cf[39].functions[1].attribute), 'Shutter1Strobe');
// <ChannelSet DMXFrom="0/1" Name="Shutter closed"/> <ChannelSet DMXFrom="32/1" Name="Shutter open"/>
check('BMFL Shutter1 sets with ranges', safe(() => cf[39].functions[0].sets.map(s => [s.name, s.dmxFrom, s.dmxTo])),
  [['Shutter closed', 0, 31], ['Shutter open', 32, 63]]);
// GDTF 1.0 puts Default on <DMXChannel>; 1.1 moved it to ChannelFunction.
// The functions carry it (reference-implementation behaviour); the legacy
// first-function fields Rig Check reads are left exactly as they were.
check('BMFL Shutter1 functions[0] default from GDTF 1.0 DMXChannel Default="32/1"',
  safe(() => [cf[39].functions[0].hasDefault, cf[39].functions[0].default]), [true, 32]);
// C1b (owner decision 2026-10-07): the first-function fields Rig Check and
// the programmer read now carry that GDTF 1.0 channel-level Default too, in
// their own raw "X/Y" units (default X, defaultByteCount Y).
check('BMFL Shutter1 top-level default from GDTF 1.0 DMXChannel Default="32/1"',
  safe(() => [cf[39].hasDefault, cf[39].default, cf[39].defaultByteCount]), [true, 32, 1]);
check('BMFL Gobo1Pos top-level default from DMXChannel Default="32896/2"',
  safe(() => [cf[24].hasDefault, cf[24].default, cf[24].defaultByteCount]), [true, 32896, 2]);
check('BMFL 16-bit Dimmer top-level default from DMXChannel Default="0/2" (a known zero)',
  safe(() => [cf[40].hasDefault, cf[40].default, cf[40].defaultByteCount, cf[41].hasDefault]), [true, 0, 2, true]);

// ---- offsets 7,8 — Color1, 16-bit, 5 functions, wheel slots -------------
// <DMXChannel ... Default="0/2" Offset="7,8">: Color1WheelIndex 0/2,
// Color1 33410/2, Color1WheelSpin 48830/2, Color1WheelAudio 62708/2,
// Color1WheelRandom 64250/2.
check('BMFL Color1 coarse byteCount/byteIndex', safe(() => [cf[7].byteCount, cf[7].byteIndex]), [2, 0]);
check('BMFL Color1 fine byteCount/byteIndex', safe(() => [cf[8].byteCount, cf[8].byteIndex]), [2, 1]);
check('BMFL Color1 function ranges at 16-bit', safe(() => cf[7].functions.map(f => [f.name, f.dmxFrom, f.dmxTo])),
  [['Color1WheelIndex', 0, 33409], ['Color1', 33410, 48829], ['Color1WheelSpin', 48830, 62707],
    ['Color1WheelAudio', 62708, 64249], ['Color1WheelRandom', 64250, 65535]]);
check('BMFL Color1 last function ends at 65535, not 255', safe(() => cf[7].functions[4].dmxTo), 65535);
check('BMFL Color1WheelIndex wheel link', safe(() => cf[7].functions[0].wheel), 'Color1');
check('BMFL Color1WheelAudio has no wheel link', safe(() => cf[7].functions[3].wheel), '');
// <ChannelSet DMXFrom="0/2" Name="Open/white - Positioning" WheelSlotIndex="1"/>
// <ChannelSet DMXFrom="4626/2" Name="Deep red - Positioning" WheelSlotIndex="2"/>
// <ChannelSet DMXFrom="9509/2" Name="Deep blue - Positioning" WheelSlotIndex="3"/>
check('BMFL Color1 positioning sets with wheel slots', safe(() => cf[7].functions[0].sets.slice(0, 3).map(s => [s.name, s.dmxFrom, s.dmxTo, s.hasWheelSlot, s.wheelSlot])),
  [['Open/white - Positioning', 0, 4625, true, 1], ['Deep red - Positioning', 4626, 9508, true, 2], ['Deep blue - Positioning', 9509, 14134, true, 3]]);
// WheelSlotIndex="0" on a non-wheel set (spec: the index is normalised to 1).
check('BMFL Color1WheelSpin set WheelSlotIndex="0" is not a slot', safe(() => cf[7].functions[2].sets[0].hasWheelSlot), false);
check('BMFL fine byte carries the same functions as coarse', safe(() => JSON.stringify(cf[8].functions) === JSON.stringify(cf[7].functions)), true);

// ---- offsets 40,41 — Dimmer, 16-bit single function --------------------
check('BMFL 16-bit Dimmer single function spans 0..65535', safe(() => cf[40].functions.map(f => [f.dmxFrom, f.dmxTo])), [[0, 65535]]);
check('BMFL Dimmer sets at 16-bit', safe(() => cf[40].functions[0].sets.map(s => [s.name, s.dmxFrom, s.dmxTo])),
  [['min', 0, 0], ['', 1, 65534], ['max', 65535, 65535]]);

// ---- offset 23 — Gobo1, 8-bit, last function to 255 --------------------
check('BMFL Gobo1 last function (Gobo1WheelRandom 250/1) ends at 255', safe(() => { const f = cf[23].functions; return [f[f.length - 1].name, f[f.length - 1].dmxFrom, f[f.length - 1].dmxTo]; }),
  ['Gobo1WheelRandom', 250, 255]);

// ---- offsets 24,25 — Gobo1Pos: ModeMaster on a ChannelFunction ---------
// <ChannelFunction Attribute="Gobo1Pos" DMXFrom="0/2" ModeFrom="0/1"
//   ModeMaster="Head_Gobo1.Gobo1.Gobo1" ModeTo="0/1" ...>; the master
// DMXChannel "Head_Gobo1" is Offset="23", 8-bit.
check('BMFL Gobo1Pos modeMaster verbatim', safe(() => cf[24].functions[0].modeMaster), 'Head_Gobo1.Gobo1.Gobo1');
check('BMFL Gobo1Pos mode range', safe(() => [cf[24].functions[0].hasMode, cf[24].functions[0].modeFrom, cf[24].functions[0].modeTo]), [true, 0, 0]);
check('BMFL Gobo1PosRotate modeMaster', safe(() => cf[24].functions[1].modeMaster), 'Head_Gobo1.Gobo1.Gobo1SelectSpin');
check('BMFL Gobo1Pos functions without a master say so', safe(() => [cf[24].functions[2].modeMaster, cf[24].functions[2].hasMode]), ['', false]);
check('BMFL Gobo1Pos 16-bit default from DMXChannel Default="32896/2"', safe(() => cf[24].functions[0].default), 32896);

// ---- offset 31 — Frost1 starts at 1/1 (the vendor defect the reference
// implementation's own test is named after). Kept as stated; warned about.
check('BMFL Frost1 first function DMXFrom kept as the file states', safe(() => cf[31].functions[0].dmxFrom), 1);
check('BMFL Frost1 not-starting-at-0 is warned, not corrected',
  bmfl.warnings.some(w => /Frost1/.test(w) && /does not start at 0/.test(w)), true);

// ---- wheels -------------------------------------------------------------
check('BMFL wheels (fixture level), document order', safe(() => bmfl.wheels.map(w => [w.name, w.slots.length])),
  [['Gobo1', 7], ['Gobo2', 7], ['Color1', 7], ['Color2', 7], ['Animation', 2], ['Prism1', 3]]);
check('BMFL mode carries the wheels too', safe(() => bm.wheels.map(w => w.name)), ['Gobo1', 'Gobo2', 'Color1', 'Color2', 'Animation', 'Prism1']);
// <Slot Color="0.598530,0.337692,0.145223" MediaFileName="14070411" Name="C01 (Deep Red)"/>
check('BMFL Color1 slot 2 (Deep Red)', safe(() => { const s = bmfl.wheels[2].slots[1]; return [s.name, s.hasColor, s.colorX, s.colorY, s.colorYY, s.mediaFileName]; }),
  ['C01 (Deep Red)', true, 0.59853, 0.337692, 0.145223, '14070411']);
// sRGB display swatch, chromaticity only (Y ignored — see gdtfparse.js).
// Expected values computed independently (Python, IEC 61966-2-1 matrix).
check('BMFL Deep Red sRGB swatch', safe(() => [bmfl.wheels[2].slots[1].hasSRGB, bmfl.wheels[2].slots[1].srgb]), [true, '#ff392a']);
check('BMFL Open slot (D65 white) sRGB swatch', safe(() => bmfl.wheels[2].slots[0].srgb), '#ffffff');
check('BMFL Open slot has no media file', safe(() => bmfl.wheels[2].slots[0].mediaFileName), '');
// <Slot Color="0.312730,0.329020,100.000000" MediaFileName="15020290" Name="G01 (New Raylines)"/>
check('BMFL Gobo1 slot 2 media file', safe(() => [bmfl.wheels[0].slots[1].name, bmfl.wheels[0].slots[1].mediaFileName]), ['G01 (New Raylines)', '15020290']);

// ---- Robin 100 LEDBeam, "mode 3" ----------------------------------------
const led = GdtfParse.parseDescriptionXml(fs.readFileSync(path.join(__dirname, 'robe_ledbeam100_real_extract.xml'), 'utf8'));
const lcf = led.modes[0].channelFunctions;
// <DMXChannel ... Default="255/1" Geometry="Beam" Highlight="255/1" Offset="7">
//   <ChannelFunction Attribute="ColorAdd_R" DMXFrom="0/1" ModeFrom="0/1" ModeMaster="Head_Color1" ModeTo="0/1">
//   <ChannelFunction Attribute="NoFeature" DMXFrom="0/1" ModeFrom="1/1" ModeMaster="Head_Color1" ModeTo="255/1">
// Master "Head_Color1" = the DMXChannel Geometry="Head" whose first
// LogicalChannel is Color1 (Offset="12", 8-bit).
check('LEDBeam red: two mode-mastered functions', safe(() => lcf[7].functions.map(f => [f.attribute, f.modeMaster, f.hasMode, f.modeFrom, f.modeTo])),
  [['ColorAdd_R', 'Head_Color1', true, 0, 0], ['NoFeature', 'Head_Color1', true, 1, 255]]);
check('LEDBeam red: overlapping mode-mastered functions both span the channel', safe(() => lcf[7].functions.map(f => [f.dmxFrom, f.dmxTo])), [[0, 255], [0, 255]]);
check('LEDBeam red: Highlight from DMXChannel Highlight="255/1"', safe(() => [lcf[7].functions[0].hasHighlight, lcf[7].functions[0].highlight]), [true, 255]);
// <DMXChannel ... Default="255/1" Geometry="Head" Highlight="255/1" Offset="13"> (Shutter1)
check('LEDBeam Shutter1 top-level default from GDTF 1.0 DMXChannel Default="255/1"',
  safe(() => [lcf[13].hasDefault, lcf[13].default, lcf[13].defaultByteCount]), [true, 255, 1]);
check('LEDBeam LampControl: 12 functions, last ends at 255', safe(() => { const f = lcf[6].functions; return [f.length, f[11].name, f[11].dmxFrom, f[11].dmxTo]; }), [12, 'Reserved 5', 210, 255]);
// A real vendor defect: <ChannelFunction Name="Shutter1" DMXFrom="0/1"> (it
// ends at 31, the next function starts at 32/1) lists sets at 0/1, 32/1,
// 0/1, 1/1. Sets that do not ascend are left out (libMVRgdtf drops them
// too) and the one at 32 lies past its function's end; each is warned about.
check('LEDBeam Shutter1 malformed sets refused, not guessed', safe(() => lcf[13].functions[0].sets.map(s => [s.name, s.dmxFrom, s.dmxTo])), [['Shutter closed', 0, 31]]);
check('LEDBeam Shutter1 malformed sets each warned', led.warnings.filter(w => /Head_Shutter1/.test(w)).length, 3);
// <Slot Color="0.639400,0.332800,100.000000" Name="Red"/> — Y is 100 here,
// 0.145 for a BMFL colour: the vendors disagree on Y's scale.
check('LEDBeam VirtualColorWheel Red slot', safe(() => { const s = led.wheels[0].slots[12]; return [s.name, s.colorYY, s.srgb, s.mediaFileName]; }), ['Red', 100, '#ff0c00', '']);

// ---- SPEC-DERIVED fixture (NOT vendor evidence) --------------------------
// DMXValue (DIN SPEC 15800 Table 1): "By default byte mirroring is used ...
// 255/1 in a 16 bit channel will result in 65535 ... 255/1s in a 16 bit
// channel will result in 65280." Channel function end (Table 60): "DMXFrom of
// the next channel function – 1 or the maximum value of the DMX channel",
// with "next" taken across LogicalChannels in document order as the
// reference implementation links them (libMVRgdtf GdtfDmxChannel::
// ReadFromNode). No real file in testdata has two LogicalChannels in one
// DMXChannel, so that rule is UNVERIFIED against vendor data.
const SPEC_XML = `<?xml version="1.0" encoding="UTF-8"?>
<GDTF DataVersion="1.2"><FixtureType Manufacturer="Spec" Name="Derived">
<Geometries><Geometry Name="Body"/></Geometries>
<DMXModes><DMXMode Name="M" Geometry="Body"><DMXChannels>
<DMXChannel Geometry="Body" Offset="1,2">
  <LogicalChannel Attribute="Dimmer">
    <ChannelFunction Name="Dim" Attribute="Dimmer" DMXFrom="0/1" Default="255/1" Highlight="255/1s"/>
  </LogicalChannel></DMXChannel>
<DMXChannel Geometry="Body" Offset="3">
  <LogicalChannel Attribute="Shutter1">
    <ChannelFunction Name="Closed" Attribute="Shutter1" DMXFrom="0/1" Default="32768/2"/>
    <ChannelFunction Name="Open" Attribute="Shutter1" DMXFrom="128/1"/>
  </LogicalChannel>
  <LogicalChannel Attribute="Shutter1Strobe">
    <ChannelFunction Name="Strobe" Attribute="Shutter1Strobe" DMXFrom="192/1" ModeMaster="Nowhere_Nothing" ModeFrom="0/1" ModeTo="10/1"/>
  </LogicalChannel></DMXChannel>
<DMXChannel Geometry="Body" Offset="4" Default="10/1">
  <LogicalChannel Attribute="Iris">
    <ChannelFunction Name="Iris" Attribute="Iris" DMXFrom="0/1" Default="20/1"/>
  </LogicalChannel></DMXChannel>
</DMXChannels></DMXMode></DMXModes>
</FixtureType></GDTF>`;
const spec = GdtfParse.parseDescriptionXml(SPEC_XML);
const scf = spec.modes[0].channelFunctions;
check('SPEC Table 1: 255/1 mirrored into a 16-bit channel', safe(() => scf[1].functions[0].default), 65535);
check('SPEC Table 1: 255/1s shifted into a 16-bit channel', safe(() => scf[1].functions[0].highlight), 65280);
check('SPEC 32768/2 into an 8-bit channel (proportional, half-up)', safe(() => scf[3].functions[0].default), 128);
check('SPEC/libMVRgdtf: functions linked across LogicalChannels', safe(() => scf[3].functions.map(f => [f.logicalAttribute, f.name, f.dmxFrom, f.dmxTo])),
  [['Shutter1', 'Closed', 0, 127], ['Shutter1', 'Open', 128, 191], ['Shutter1Strobe', 'Strobe', 192, 255]]);
check('SPEC unresolvable ModeMaster: kept verbatim, no mode range invented', safe(() => [scf[3].functions[2].modeMaster, scf[3].functions[2].hasMode]), ['Nowhere_Nothing', false]);
check('SPEC unresolvable ModeMaster is warned', spec.warnings.some(w => /Nowhere_Nothing/.test(w)), true);
check('SPEC 1.1-style: a ChannelFunction Default wins over a DMXChannel Default (top-level)',
  safe(() => [scf[4].hasDefault, scf[4].default, scf[4].defaultByteCount]), [true, 20, 1]);
check('SPEC 1.1-style: ChannelFunction Default unchanged on a channel with no DMXChannel Default',
  safe(() => [scf[1].hasDefault, scf[1].default, scf[1].defaultByteCount]), [true, 255, 1]);
check('SPEC no <Wheels> gives an empty (not missing) wheels list', safe(() => [spec.wheels, spec.modes[0].wheels]), [[], []]);

if (failures) { console.error(`${failures} check(s) failed`); process.exit(1); }
console.log('all channel-detail checks passed');
