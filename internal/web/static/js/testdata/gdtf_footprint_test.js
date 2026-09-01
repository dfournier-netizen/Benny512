// gdtf_footprint_test.js — regression test for gdtfparse.js's footprint
// (and channelFunctions) resolution, run under plain Node via
// internal/web/gdtfparse_footprint_test.go (`node gdtf_footprint_test.js`,
// exit 0 = pass). Not part of the browser app; testdata/ is never loaded by
// index.html.
//
// Why synthetic fixtures instead of the real .mvr's GDTF files: the show
// file at issue (see the task brief this file was written against) is
// several MB with 5 GDTFs up to ~640KB of XML each — too large to vendor as
// committed test data. Each fixture below is a hand-built minimal GDTF
// <Geometries>/<DMXModes> fragment that reproduces the EXACT geometry shape
// and EXACT literal offsets of the real fixture/mode it's named for
// (verified by re-extracting description.xml from the real .mvr and
// diffing structure — see the PR/commit description for the extraction),
// just with fewer repeated <GeometryReference> instances where the real
// file has dozens (e.g. 2 RGBW-pixel refs stand in for the real 19, 2
// SparkLED-pixel refs stand in for the real 76) — footprint only depends on
// the MAX resolved offset, so the first and last instance's literal Break
// DMXOffset values (kept identical to the real file) are what matter, not
// the count in between. Every asserted footprint number below is the real,
// independently-measured ground truth (DMX address spacing between
// same-type fixtures in the real show, back-to-back placement means the
// gap IS the footprint) — not a restatement of what the parser happens to
// compute, per this project's rule against fixtures built on the code's own
// assumptions.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { DOMParser } = require('./tinydom');

function loadGdtfParse(gdtfparsePath) {
  const src = fs.readFileSync(gdtfparsePath, 'utf8');
  const context = vm.createContext({ DOMParser, console });
  return vm.runInContext(src + '\nGdtfParse;', context, { filename: 'gdtfparse.js' });
}

// ---- tiny XML builders -----------------------------------------------

function dmxChannel(geometry, offset, attribute, extraOffsets) {
  const off = offset === null ? '' : String(offset);
  const fnDmxTo = extraOffsets ? ' PhysicalTo="1"' : '';
  return (
    `<DMXChannel DMXBreak="1" Geometry="${geometry}" Offset="${off}">` +
    `<LogicalChannel Attribute="${attribute}">` +
    `<ChannelFunction Name="${attribute} 1" Attribute="${attribute}" DMXFrom="0/1" PhysicalFrom="0"${fnDmxTo}/>` +
    `</LogicalChannel></DMXChannel>`
  );
}

function geometryReference(name, targetGeometry, dmxOffset) {
  return (
    `<GeometryReference Name="${name}" Geometry="${targetGeometry}">` +
    `<Break DMXBreak="1" DMXOffset="${dmxOffset}"/>` +
    `</GeometryReference>`
  );
}

function gdtfDoc(manufacturer, model, geometriesXml, modesXml) {
  return (
    `<?xml version="1.0" encoding="UTF-8"?>` +
    `<GDTF><FixtureType Manufacturer="${manufacturer}" Name="${model}">` +
    `<Geometries>${geometriesXml}</Geometries>` +
    `<DMXModes>${modesXml}</DMXModes>` +
    `</FixtureType></GDTF>`
  );
}

// ---- fixture 1: ERA 800 Performance, 'Basic' — flat channel list, no
// <GeometryReference> at all (vendor file; ground truth footprint 42). ----

function flatChannelsXml(geometryName, count) {
  let out = '';
  for (let i = 1; i <= count; i++) out += dmxChannel(geometryName, i, i <= count - 2 ? `Ch${i}` : 'NoFeature');
  return out;
}

const ERA_800_XML = gdtfDoc(
  'Martin Professional', 'ERA 800 Performance',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="Basic" Geometry="Body"><DMXChannels>${flatChannelsXml('Body', 42)}</DMXChannels></DMXMode>`
);

// ---- fixture 2: Rogue Outcast 2X Wash, '22Ch Mode' — flat, no reference
// (vendor file; ground truth footprint 22). ----

const OUTCAST_XML = gdtfDoc(
  'Chauvet Professional', 'Rogue Outcast 2X Wash',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="22Ch Mode" Geometry="Body"><DMXChannels>${flatChannelsXml('Body', 22)}</DMXChannels></DMXMode>`
);

// ---- fixture 3 & 4: the two Custom@Light_Instr_* files — flat list ending
// in literal "NoFeature" (no-op) channels, nothing past the last one (Task
// 2 finding: designer padding, not a dropped channel — see gdtfparse.js's
// doc comment and the task report). Ground truth: JDC1 61, Paladin 23 — one
// LESS than the real DMX address spacing (62, 24); Benny512 is correct and
// the extra address is the designer's own patch spacing, not a parser bug.

const JDC1_XML = gdtfDoc(
  'Custom', 'Light_Instr_GLP_JDC1_Strobe',
  `<Geometry Name="Yoke"/>`,
  `<DMXMode Name="DMX Mode" Geometry="Yoke"><DMXChannels>${flatChannelsXml('Yoke', 61)}</DMXChannels></DMXMode>`
);

const PALADIN_XML = gdtfDoc(
  'Custom', 'Light_Instr_Elation_Paladin_Cube',
  `<Geometry Name="Yoke"/>`,
  `<DMXMode Name="DMX Mode" Geometry="Yoke"><DMXChannels>${flatChannelsXml('Yoke', 23)}</DMXChannels></DMXMode>`
);

// ---- fixture 5: Proteus Rayzor 1960, 'Extended Pan540/Tilt270' — the
// regression fixture. Reproduces, at the SAME literal offsets as the real
// file: Yoke_Extended (Pan @1,2), Head_Extended (Tilt @3,4), RGBW Cells (a
// pure <GeometryReference> array wrapper with NO channel of its own — must
// still expand; 2 of the real 19 RGBW Pixel refs, breaks 22 and 26, same as
// the real file's first two), and Extended SparkLED V (virtual, Offset=
// "None") > Spark LED Strobe Module (a pure <GeometryReference> array
// wrapper that ALSO owns real Shutter1/Dimmer channels @98/99,100 — the
// exact structure that triggered the regression; 2 of the real 76 SparkLED
// refs, breaks 101 and 176, the real file's first and last). Ground truth
// footprint: 100 (from the module's own 99,100 — NOT 176, which is what
// expanding the per-instance Spark LED Pixel refs would wrongly give). ----

const RAYZOR_GEOMETRIES = (
  `<Geometry Name="Base_Extended">` +
  `<Axis Name="Yoke_Extended">` +
  `<Axis Name="Head_Extended">` +
  `<Geometry Name="RGBW Cells">` +
  geometryReference('RGBW Pixel 1', 'RGBW Pixel', 22) +
  geometryReference('RGBW Pixel 2', 'RGBW Pixel', 26) +
  `</Geometry>` +
  `<Geometry Name="Extended SparkLED V">` +
  `<Geometry Name="Spark LED Strobe Module">` +
  geometryReference('SparkLED 1', 'Spark LED Pixel', 101) +
  geometryReference('SparkLED 76', 'Spark LED Pixel', 176) +
  `</Geometry>` +
  `</Geometry>` +
  `</Axis></Axis></Geometry>` +
  `<Beam Name="RGBW Pixel"/><Beam Name="Spark LED Pixel"/>`
);

const RAYZOR_EXTENDED_XML = gdtfDoc(
  'Elation', 'Proteus Rayzor 1960',
  RAYZOR_GEOMETRIES,
  `<DMXMode Name="Extended Pan540/Tilt270" Geometry="Base_Extended"><DMXChannels>` +
  dmxChannel('Yoke_Extended', '1,2', 'Pan', true) +
  dmxChannel('Head_Extended', '3,4', 'Tilt', true) +
  dmxChannel('RGBW Pixel', 1, 'ColorAdd_R') +
  dmxChannel('RGBW Pixel', 2, 'ColorAdd_G') +
  dmxChannel('RGBW Pixel', 3, 'ColorAdd_B') +
  dmxChannel('RGBW Pixel', 4, 'ColorAdd_W') +
  dmxChannel('Spark LED Strobe Module', 98, 'Shutter1') +
  dmxChannel('Spark LED Strobe Module', '99,100', 'Dimmer', true) +
  dmxChannel('Spark LED Pixel', 1, 'Dimmer') +
  dmxChannel('Extended SparkLED V', null, 'Dimmer') +
  `</DMXChannels></DMXMode>`
);

// ---- fixture 6: Proteus Rayzor 1960, 'Standard Pan540/Tilt270' — same
// fixture's OTHER GeometryReference shape: Head_Standard has its own real
// channels (Tilt) AND a non-reference sibling (a Beam) alongside its single
// <GeometryReference> to "SparkLED Mod" (break 24) — NOT a pure array
// container, so (unlike Spark LED Strobe Module above) its reference must
// still expand. This is also the fixture that motivated the pre-existing
// "SparkLED Mod template channels 1/2 were overwriting Yoke's Pan/Tilt at
// offsets 1/2" collision fix: SparkLED Mod's own literal channels are
// offsets 1 (Shutter1) and 2 (Dimmer) — LOCAL numbers that collide with
// Yoke_Standard's literal Pan @1,2 if never resolved through the reference.
// No show fixture uses this mode, so 25 is this task's own derivation
// (24 = local 1 + (24-1), 25 = local 2 + (24-1)), marked UNVERIFIED against
// real address spacing in the task report — this test locks in that the
// parser keeps producing 25 (not 23, and not a Pan/Tilt collision at
// offsets 1/2), i.e. that it doesn't regress, not that 25 is confirmed
// ground truth. ----

const RAYZOR_STANDARD_XML = gdtfDoc(
  'Elation', 'Proteus Rayzor 1960',
  `<Geometry Name="Base_Standard">` +
  `<Axis Name="Yoke_Standard">` +
  `<Axis Name="Head_Standard">` +
  `<Beam Name="Pixels_Standard"/>` +
  geometryReference('SparkLEDs_Standard', 'SparkLED Mod', 24) +
  `</Axis></Axis></Geometry>` +
  `<Beam Name="SparkLED Mod"/>`,
  `<DMXMode Name="Standard Pan540/Tilt270" Geometry="Base_Standard"><DMXChannels>` +
  dmxChannel('Yoke_Standard', '1,2', 'Pan', true) +
  dmxChannel('Head_Standard', '3,4', 'Tilt', true) +
  dmxChannel('SparkLED Mod', 1, 'Shutter1') +
  dmxChannel('SparkLED Mod', 2, 'Dimmer') +
  `</DMXChannels></DMXMode>`
);

// ---- fixture 7: GLP JDC1 (real vendor file, GLPJDC1_Strobetest1.gdtf) —
// the regression's actual trigger. Reproduces, at the SAME literal offsets
// and SAME geometry shape as the real file (re-extracted description.xml
// from the .gdtf, diffed structurally — see task report), the 6 declared
// modes. Ground truth for each mode's footprint is the fixture's OWN
// declared channel count in its mode name ("Mode 1 Compressed Pro (14ch)"
// -> 14), independently corroborated for Mode 4 by the owner's fixture
// manual and by real DMX addressing (back-to-back placement: next fixture
// starts at 63, i.e. this one spans 62). This is an INDEPENDENT oracle from
// the parser's own output, per this project's rule against fixtures built
// on the code's own assumptions.
//
// The real file's geometry tree: Base Yoke M<n> -> Axis Head M<n> ->
// <GeometryReference> children pointing at shared template geometries
// ("Beam Module", "Plate Module", "Background Plate", "All White Pixel") —
// NOT a replicated array (each ref targets a DIFFERENT template), just
// ordinary composition-via-reference, reused across all 6 head variants.
// Modes 3 and 4 additionally nest a genuine per-pixel array under Head M3/
// M4 ("Single Back Plate M<n>" x12 refs -> "Plate Pixel", "Single White
// Beam M<n>" x12 refs -> "Beam Pixel") — reduced to 2 refs each here
// (first/last real Break DMXOffset, same convention as the Rayzor fixture
// above), since footprint only depends on the max resolved offset.
//
// This is exactly the shape that exposed the regression: Head M1/M2/M5/M6
// have real literal channels of their own AND every child is a
// <GeometryReference> — the OLD "pure array container" guard blocked them
// (wrongly, since the references target different geometries, not one
// replicated template), dropping Beam Module/Plate Module/Background
// Plate/All White Pixel's real channels entirely. ----

const JDC1_REAL_GEOMETRIES = (
  `<Geometry Name="Base Yoke M1"><Axis Name="Head M1">` +
  geometryReference('Beam Module M1', 'Beam Module', 1) +
  geometryReference('Plate Module M1', 'Plate Module', 1) +
  `</Axis></Geometry>` +
  `<Geometry Name="Base Yoke M2"><Axis Name="Head M2">` +
  geometryReference('Beam Module M2', 'Beam Module', 1) +
  geometryReference('Plate Module M2', 'Plate Module', 1) +
  geometryReference('Background Plate M2', 'Background Plate', 1) +
  `</Axis></Geometry>` +
  `<Geometry Name="Base Yoke M3"><Axis Name="Head M3">` +
  geometryReference('Beam Module M3', 'Beam Module', 1) +
  geometryReference('Plate Module M3', 'Plate Module', 1) +
  `<Geometry Name="Single Back Plate M3">` +
  geometryReference('M3 Single Plate 1', 'Plate Pixel', 1) +
  geometryReference('M3 Single Plate 12', 'Plate Pixel', 34) +
  `</Geometry>` +
  `<Geometry Name="Single White Beam M3">` +
  geometryReference('M3 Single Beam 1', 'Beam Pixel', 1) +
  geometryReference('M3 Single Beam 12', 'Beam Pixel', 12) +
  `</Geometry>` +
  `</Axis></Geometry>` +
  `<Geometry Name="Base Yoke M4"><Axis Name="Head M4">` +
  geometryReference('Beam Module M4', 'Beam Module', 1) +
  geometryReference('Plate Module M4', 'Plate Module', 1) +
  `<Geometry Name="Single Back Plate M4">` +
  geometryReference('M4 Single Plate 1', 'Plate Pixel', 1) +
  geometryReference('M4 Single Plate 12', 'Plate Pixel', 34) +
  `</Geometry>` +
  `<Geometry Name="Single White Beam M4">` +
  geometryReference('M4 Single Beam 1', 'Beam Pixel', 1) +
  geometryReference('M4 Single Beam 12', 'Beam Pixel', 12) +
  `</Geometry>` +
  `</Axis></Geometry>` +
  `<Geometry Name="Base Yoke M5"><Axis Name="Head M5">` +
  geometryReference('Beam Module M5', 'Beam Module', 1) +
  geometryReference('Plate Module M5', 'Plate Module', 1) +
  geometryReference('Background Plate M5', 'Background Plate', 1) +
  `</Axis></Geometry>` +
  `<Geometry Name="Base Yoke M6"><Axis Name="Head M6">` +
  geometryReference('Beam Module M6', 'Beam Module', 1) +
  geometryReference('Plate Module M6', 'Plate Module', 1) +
  geometryReference('All Pixel White', 'All White Pixel', 1) +
  `</Axis></Geometry>` +
  `<Beam Name="Beam Module"/><Beam Name="Plate Module"/>` +
  `<Beam Name="Background Plate"/><Beam Name="Plate Pixel"/>` +
  `<Beam Name="Beam Pixel"/><Beam Name="All White Pixel"/>`
);

function jdc1Mode1Channels() {
  return (
    dmxChannel('Head M1', '1,2', 'Tilt') +
    dmxChannel('Beam Module', 3, 'Dimmer') +
    dmxChannel('Beam Module', 4, 'StrobeDuration') +
    dmxChannel('Beam Module', 5, 'StrobeRate') +
    dmxChannel('Beam Module', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M1', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'Dimmer') +
    dmxChannel('Plate Module', 9, 'StrobeDuration') +
    dmxChannel('Plate Module', 10, 'StrobeRate') +
    dmxChannel('Plate Module', 11, 'StrobeModeStrobe') +
    dmxChannel('Plate Module', 12, 'ColorAdd_R') +
    dmxChannel('Plate Module', 13, 'ColorAdd_G') +
    dmxChannel('Plate Module', 14, 'ColorAdd_B')
  );
}

function jdc1Mode2Channels() {
  return (
    dmxChannel('Head M2', '1,2', 'Tilt') +
    dmxChannel('Beam Module', 3, 'Dimmer') +
    dmxChannel('Beam Module', 4, 'StrobeDuration') +
    dmxChannel('Beam Module', 5, 'StrobeRate') +
    dmxChannel('Beam Module', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M2', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'Dimmer') +
    dmxChannel('Plate Module', 9, 'StrobeDuration') +
    dmxChannel('Plate Module', 10, 'StrobeRate') +
    dmxChannel('Plate Module', 11, 'StrobeModeStrobe') +
    dmxChannel('Plate Module', 12, 'ColorAdd_R') +
    dmxChannel('Plate Module', 13, 'ColorAdd_G') +
    dmxChannel('Plate Module', 14, 'ColorAdd_B') +
    dmxChannel('Head M2', 15, 'Pattern Crossfade') +
    dmxChannel('Plate Module', 16, 'Pattern Step / Speed') +
    dmxChannel('Plate Module', 17, 'Pattern Selection') +
    dmxChannel('Beam Module', 18, 'Pattern Step / Speed') +
    dmxChannel('Beam Module', 19, 'Pattern Selection') +
    dmxChannel('Background Plate', 20, 'Dimmer') +
    dmxChannel('Background Plate', 21, 'ColorAdd_R') +
    dmxChannel('Background Plate', 22, 'ColorAdd_G') +
    dmxChannel('Background Plate', 23, 'ColorAdd_B')
  );
}

function jdc1Mode3Channels() {
  return (
    dmxChannel('Plate Pixel', null, 'Dimmer') +
    dmxChannel('Head M3', '1,2', 'Tilt') +
    dmxChannel('Beam Module', 3, 'Dimmer') +
    dmxChannel('Beam Module', 4, 'StrobeDuration') +
    dmxChannel('Beam Module', 5, 'StrobeRate') +
    dmxChannel('Beam Module', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M3', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'Dimmer') +
    dmxChannel('Plate Module', 9, 'StrobeDuration') +
    dmxChannel('Plate Module', 10, 'StrobeRate') +
    dmxChannel('Plate Module', 11, 'StrobeModeStrobe') +
    dmxChannel('Plate Module', 12, 'ColorAdd_R') +
    dmxChannel('Plate Module', 13, 'ColorAdd_G') +
    dmxChannel('Plate Module', 14, 'ColorAdd_B') +
    dmxChannel('Head M3', 15, 'Pattern Crossfade') +
    dmxChannel('Plate Module', 16, 'Pattern Step / Speed') +
    dmxChannel('Plate Module', 17, 'Pattern Selection') +
    dmxChannel('Beam Module', 18, 'Pattern Step / Speed') +
    dmxChannel('Beam Module', 19, 'Pattern Selection') +
    dmxChannel('Single Back Plate M3', 20, 'Dimmer') +
    dmxChannel('Plate Pixel', 21, 'ColorAdd_R') +
    dmxChannel('Plate Pixel', 22, 'ColorAdd_G') +
    dmxChannel('Plate Pixel', 23, 'ColorAdd_B') +
    dmxChannel('Beam Pixel', 57, 'Dimmer')
  );
}

function jdc1Mode4Channels() {
  return (
    dmxChannel('Plate Pixel', null, 'Dimmer') +
    dmxChannel('Head M4', '1,2', 'Tilt') +
    dmxChannel('Beam Module', 3, 'Dimmer') +
    dmxChannel('Beam Module', 4, 'StrobeDuration') +
    dmxChannel('Beam Module', 5, 'StrobeRate') +
    dmxChannel('Beam Module', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M4', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'Dimmer') +
    dmxChannel('Plate Module', 9, 'StrobeDuration') +
    dmxChannel('Plate Module', 10, 'StrobeRate') +
    dmxChannel('Plate Module', 11, 'StrobeModeStrobe') +
    dmxChannel('Plate Module', 12, 'ColorAdd_R') +
    dmxChannel('Plate Module', 13, 'ColorAdd_G') +
    dmxChannel('Plate Module', 14, 'ColorAdd_B') +
    dmxChannel('Plate Pixel', 15, 'ColorAdd_R') +
    dmxChannel('Plate Pixel', 16, 'ColorAdd_G') +
    dmxChannel('Plate Pixel', 17, 'ColorAdd_B') +
    dmxChannel('Beam Pixel', 51, 'Dimmer')
  );
}

function jdc1Mode5Channels() {
  return (
    dmxChannel('Background Plate', null, 'Dimmer') +
    dmxChannel('Head M5', '1,2', 'Tilt') +
    dmxChannel('Beam Module', 3, 'Dimmer') +
    dmxChannel('Beam Module', 4, 'StrobeDuration') +
    dmxChannel('Beam Module', 5, 'StrobeRate') +
    dmxChannel('Beam Module', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M5', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'Dimmer') +
    dmxChannel('Plate Module', 9, 'StrobeDuration') +
    dmxChannel('Plate Module', 10, 'StrobeRate') +
    dmxChannel('Plate Module', 11, 'StrobeModeStrobe') +
    dmxChannel('Plate Module', 12, 'ColorAdd_R') +
    dmxChannel('Plate Module', 13, 'ColorAdd_G') +
    dmxChannel('Plate Module', 14, 'ColorAdd_B') +
    dmxChannel('Background Plate', 15, 'ColorAdd_R') +
    dmxChannel('Background Plate', 16, 'ColorAdd_G') +
    dmxChannel('Background Plate', 17, 'ColorAdd_B')
  );
}

function jdc1Mode6Channels() {
  return (
    dmxChannel('Head M6', '1,2', 'Tilt') +
    dmxChannel('Head M6', 3, 'Dimmer') +
    dmxChannel('Head M6', 4, 'StrobeDuration') +
    dmxChannel('Head M6', 5, 'StrobeRate') +
    dmxChannel('Head M6', 6, 'StrobeModeStrobe') +
    dmxChannel('Head M6', 7, 'Control1') +
    dmxChannel('Plate Module', 8, 'ColorAdd_R') +
    dmxChannel('Plate Module', 9, 'ColorAdd_G') +
    dmxChannel('Plate Module', 10, 'ColorAdd_B') +
    dmxChannel('Plate Module', null, 'Dimmer') +
    dmxChannel('All White Pixel', 11, 'Dimmer')
  );
}

const JDC1_REAL_XML = gdtfDoc(
  'GLP', 'JDC1',
  JDC1_REAL_GEOMETRIES,
  `<DMXMode Name="Mode 1 Compressed Pro (14ch)" Geometry="Base Yoke M1"><DMXChannels>${jdc1Mode1Channels()}</DMXChannels></DMXMode>` +
  `<DMXMode Name="Mode 2 Normal (23ch)" Geometry="Base Yoke M2"><DMXChannels>${jdc1Mode2Channels()}</DMXChannels></DMXMode>` +
  `<DMXMode Name="Mode 3 SPix (68ch)" Geometry="Base Yoke M3"><DMXChannels>${jdc1Mode3Channels()}</DMXChannels></DMXMode>` +
  `<DMXMode Name="Mode 4 SPix PRO (62ch)" Geometry="Base Yoke M4"><DMXChannels>${jdc1Mode4Channels()}</DMXChannels></DMXMode>` +
  `<DMXMode Name="Mode 5 1Pix Pro (17ch)" Geometry="Base Yoke M5"><DMXChannels>${jdc1Mode5Channels()}</DMXChannels></DMXMode>` +
  `<DMXMode Name="Mode 6 Easy (11ch)" Geometry="Base Yoke M6"><DMXChannels>${jdc1Mode6Channels()}</DMXChannels></DMXMode>`
);

// ---- run ---------------------------------------------------------------

function main() {
  const gdtfparsePath = path.join(__dirname, '..', 'gdtfparse.js');
  const GdtfParse = loadGdtfParse(gdtfparsePath);

  let failures = 0;
  function check(label, got, want) {
    if (got !== want) {
      failures++;
      console.error(`FAIL ${label}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
    } else {
      console.log(`ok   ${label}: ${JSON.stringify(got)}`);
    }
  }

  function modeByName(xml, name) {
    const result = GdtfParse.parseDescriptionXml(xml);
    const mode = result.modes.find(m => m.name === name);
    if (!mode) throw new Error(`mode "${name}" not found`);
    return mode;
  }

  // The five real-show ground-truth footprints (address-spacing derived).
  check('ERA 800 Performance "Basic" footprint', modeByName(ERA_800_XML, 'Basic').footprint, 42);
  check('Rogue Outcast 2X Wash "22Ch Mode" footprint', modeByName(OUTCAST_XML, '22Ch Mode').footprint, 22);
  check('GLP JDC1 Strobe "DMX Mode" footprint', modeByName(JDC1_XML, 'DMX Mode').footprint, 61);
  check('Elation Paladin Cube "DMX Mode" footprint', modeByName(PALADIN_XML, 'DMX Mode').footprint, 23);
  const rayzorExtended = modeByName(RAYZOR_EXTENDED_XML, 'Extended Pan540/Tilt270');
  check('Proteus Rayzor 1960 "Extended Pan540/Tilt270" footprint', rayzorExtended.footprint, 100);

  // The RGBW pixel array must still expand (this is the legitimate
  // under-counting fix the geometry-reference walk exists for) — offset 26
  // (2nd RGBW ref's break) local channel 4 (ColorAdd_W) resolves to 26-1+4
  // = 29, and must be present with the right attribute.
  check('Rayzor Extended RGBW pixel 2 W channel resolved offset present',
    Object.prototype.hasOwnProperty.call(rayzorExtended.channelFunctions, 29), true);
  check('Rayzor Extended RGBW pixel 2 W channel attribute',
    rayzorExtended.channelFunctions[29] && rayzorExtended.channelFunctions[29].attribute, 'ColorAdd_W');

  // The per-instance SparkLED pixel replication must NOT appear — this is
  // the regression itself: offset 101 (1st SparkLED ref) and 176 (2nd) must
  // be absent from both footprint and channelFunctions.
  check('Rayzor Extended offset 101 (blocked per-instance SparkLED pixel) absent',
    Object.prototype.hasOwnProperty.call(rayzorExtended.channelFunctions, 101), false);
  check('Rayzor Extended offset 176 (blocked per-instance SparkLED pixel) absent',
    Object.prototype.hasOwnProperty.call(rayzorExtended.channelFunctions, 176), false);

  // The module-level Shutter1/Dimmer that DOES give the real footprint.
  check('Rayzor Extended offset 98 attribute (SparkLED Strobe Module Shutter1)',
    rayzorExtended.channelFunctions[98] && rayzorExtended.channelFunctions[98].attribute, 'Shutter1');
  check('Rayzor Extended offset 100 attribute (SparkLED Strobe Module Dimmer)',
    rayzorExtended.channelFunctions[100] && rayzorExtended.channelFunctions[100].attribute, 'Dimmer');

  // Standard mode: unverified-but-preserved 25, AND the SparkLED-collision
  // fix must still hold — offsets 1/2 must stay Yoke's Pan, not get
  // overwritten by SparkLED Mod's own local 1/2 numbering.
  const rayzorStandard = modeByName(RAYZOR_STANDARD_XML, 'Standard Pan540/Tilt270');
  check('Rayzor Standard "Standard Pan540/Tilt270" footprint (UNVERIFIED against real spacing — see report)',
    rayzorStandard.footprint, 25);
  check('Rayzor Standard offset 1 attribute (must stay Pan, not SparkLED Shutter1)',
    rayzorStandard.channelFunctions[1] && rayzorStandard.channelFunctions[1].attribute, 'Pan');
  check('Rayzor Standard offset 2 attribute (must stay Pan, not SparkLED Dimmer)',
    rayzorStandard.channelFunctions[2] && rayzorStandard.channelFunctions[2].attribute, 'Pan');
  check('Rayzor Standard offset 24 attribute (resolved SparkLED Mod Shutter1)',
    rayzorStandard.channelFunctions[24] && rayzorStandard.channelFunctions[24].attribute, 'Shutter1');
  check('Rayzor Standard offset 25 attribute (resolved SparkLED Mod Dimmer)',
    rayzorStandard.channelFunctions[25] && rayzorStandard.channelFunctions[25].attribute, 'Dimmer');

  // ---- GLP JDC1 (real vendor file) — the actual regression trigger. Each
  // mode name declares its own channel count as an independent oracle; Mode
  // 4 is separately corroborated by the fixture manual and real addressing
  // (see fixture 7's doc comment above). ----
  const jdc1Modes = GdtfParse.parseDescriptionXml(JDC1_REAL_XML).modes;
  function jdc1Mode(name) {
    const mode = jdc1Modes.find(m => m.name === name);
    if (!mode) throw new Error(`mode "${name}" not found`);
    return mode;
  }
  check('JDC1 "Mode 1 Compressed Pro (14ch)" footprint', jdc1Mode('Mode 1 Compressed Pro (14ch)').footprint, 14);
  check('JDC1 "Mode 2 Normal (23ch)" footprint', jdc1Mode('Mode 2 Normal (23ch)').footprint, 23);
  check('JDC1 "Mode 3 SPix (68ch)" footprint', jdc1Mode('Mode 3 SPix (68ch)').footprint, 68);
  check('JDC1 "Mode 4 SPix PRO (62ch)" footprint', jdc1Mode('Mode 4 SPix PRO (62ch)').footprint, 62);
  check('JDC1 "Mode 5 1Pix Pro (17ch)" footprint', jdc1Mode('Mode 5 1Pix Pro (17ch)').footprint, 17);
  check('JDC1 "Mode 6 Easy (11ch)" footprint', jdc1Mode('Mode 6 Easy (11ch)').footprint, 11);

  // channelFunctions spot-check: a PLAIN mode (Mode 2 — composition via
  // <GeometryReference> to distinct shared templates, no replication) and a
  // REPLICATED mode (Mode 4 — genuine per-pixel array expansion), per the
  // task brief's requirement to verify the resolved per-offset attribute
  // map, not just the footprint number.
  const jdc1Mode2 = jdc1Mode('Mode 2 Normal (23ch)');
  check('JDC1 Mode 2 offset 1 attribute (Head M2 Tilt, reached directly)',
    jdc1Mode2.channelFunctions[1] && jdc1Mode2.channelFunctions[1].attribute, 'Tilt');
  check('JDC1 Mode 2 offset 3 attribute (Beam Module Dimmer, reached via reference)',
    jdc1Mode2.channelFunctions[3] && jdc1Mode2.channelFunctions[3].attribute, 'Dimmer');
  check('JDC1 Mode 2 offset 14 attribute (Plate Module ColorAdd_B, reached via reference)',
    jdc1Mode2.channelFunctions[14] && jdc1Mode2.channelFunctions[14].attribute, 'ColorAdd_B');
  check('JDC1 Mode 2 offset 20 attribute (Background Plate Dimmer, reached via reference)',
    jdc1Mode2.channelFunctions[20] && jdc1Mode2.channelFunctions[20].attribute, 'Dimmer');
  check('JDC1 Mode 2 offset 23 attribute (Background Plate ColorAdd_B, reached via reference)',
    jdc1Mode2.channelFunctions[23] && jdc1Mode2.channelFunctions[23].attribute, 'ColorAdd_B');

  const jdc1Mode4 = jdc1Mode('Mode 4 SPix PRO (62ch)');
  check('JDC1 Mode 4 offset 3 attribute (Beam Module Dimmer, reached via reference)',
    jdc1Mode4.channelFunctions[3] && jdc1Mode4.channelFunctions[3].attribute, 'Dimmer');
  check('JDC1 Mode 4 offset 15 attribute (Plate Pixel instance 1 ColorAdd_R, array-expanded)',
    jdc1Mode4.channelFunctions[15] && jdc1Mode4.channelFunctions[15].attribute, 'ColorAdd_R');
  check('JDC1 Mode 4 offset 48 attribute (Plate Pixel instance 12 ColorAdd_R, array-expanded)',
    jdc1Mode4.channelFunctions[48] && jdc1Mode4.channelFunctions[48].attribute, 'ColorAdd_R');
  check('JDC1 Mode 4 offset 51 attribute (Beam Pixel instance 1 Dimmer, array-expanded)',
    jdc1Mode4.channelFunctions[51] && jdc1Mode4.channelFunctions[51].attribute, 'Dimmer');
  check('JDC1 Mode 4 offset 62 attribute (Beam Pixel instance 12 Dimmer, array-expanded — the mode footprint)',
    jdc1Mode4.channelFunctions[62] && jdc1Mode4.channelFunctions[62].attribute, 'Dimmer');

  if (failures > 0) {
    console.error(`\n${failures} check(s) failed.`);
    process.exit(1);
  }
  console.log('\nall checks passed.');
}

main();
