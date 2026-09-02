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
// in literal "NoFeature" (no-op) channels, nothing past the last one.
//
// READ THIS BEFORE TRUSTING THE 61/23 NUMBERS BELOW. Unlike every other
// fixture in this file, these two are CIRCULAR and carry no evidentiary
// weight about the real files. They are hand-built as exactly 61 and 23
// DMXChannel elements, each with a real, dense Offset — a flat list of N
// addressed channels can only ever resolve to N, whatever the parser does.
// So "asserts 61" here means "this synthetic input has 61 channels", not
// "the real Custom@Light_Instr_GLP_JDC1_Strobe.gdtf resolves to 61". An
// earlier session recorded these as ground truth alongside a note that the
// real DMX address spacing measured 62 and 24 — i.e. the assertions
// contradict the only independent measurement the file itself cites, and
// the "designer padding" story reconciling them was never tested.
//
// The owner disputes those numbers and says the true footprints are 62 and
// 24. Fixture 8 below tests the actual mechanism that could cause a
// one-channel under-count, and main()'s vendor cross-check shows the
// parser's virtual-channel rule is corroborated by the real GLP JDC1 file.
// These two are kept, unchanged, purely as a flat-list regression guard;
// they are NOT evidence for 61/23. See the task report for what would be
// needed to settle the real files (their description.xml <DMXChannels>
// lists — specifically how many <DMXChannel> elements each "DMX Mode" has
// and each one's Offset attribute).

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

// ---- fixture 8: virtual-channel / NoFeature footprint probes (Task 2, the
// footprint dispute). The owner reported that the two Custom@Light_Instr_*
// placeholder fixtures above resolve one channel SHORT of their real DMX
// address spacing (61 vs 62, 23 vs 24), and the leading hypothesis was that
// the parser drops a trailing "NoFeature" placeholder channel that still
// occupies a DMX slot.
//
// These two fixtures isolate the two distinct shapes that hypothesis
// conflates, on otherwise identical 62-channel modes, so the mechanism is
// mechanically visible instead of inferred:
//
//   NOFEATURE_ADDRESSED — channel 62 has Attribute="NoFeature" but a real
//     Offset="62". This channel HAS NO FUNCTION but DOES EXIST at a DMX
//     address. Footprint must be 62, and offset 62 must still appear in
//     channelFunctions (it is a real, addressed slot a rig check will drive).
//
//   NOFEATURE_VIRTUAL — channel 62 has Attribute="NoFeature" and
//     Offset="None" (GDTF's own default for a missing Offset). This channel
//     DOES NOT EXIST at any DMX address — GDTF calls it a virtual channel.
//     Footprint must be 61.
//
// Only the second shrinks a footprint, which is exactly the distinction the
// dispute turns on. See the JDC1_REAL_XML cross-check in main() for the
// vendor evidence that excluding virtual channels is right.

function noFeatureTailXml(lastOffset) {
  let out = '';
  for (let i = 1; i <= 61; i++) out += dmxChannel('Yoke', i, `Ch${i}`);
  out += dmxChannel('Yoke', lastOffset, 'NoFeature');
  return out;
}

const NOFEATURE_ADDRESSED_XML = gdtfDoc(
  'Test', 'NoFeature Addressed',
  `<Geometry Name="Yoke"/>`,
  `<DMXMode Name="DMX Mode" Geometry="Yoke"><DMXChannels>${noFeatureTailXml(62)}</DMXChannels></DMXMode>`
);

const NOFEATURE_VIRTUAL_XML = gdtfDoc(
  'Test', 'NoFeature Virtual',
  `<Geometry Name="Yoke"/>`,
  `<DMXMode Name="DMX Mode" Geometry="Yoke"><DMXChannels>${noFeatureTailXml('None')}</DMXChannels></DMXMode>`
);

// ---- fixture 9: GDTF Default/Highlight capture (Task 1). Rig Check drives
// one channel at a time and sends 0 to everything else, which makes a real
// fixture emit nothing at all — a fixture needs BOTH a dimmer at level AND a
// shutter in its "open" position. GDTF states each channel's resting value
// in <ChannelFunction Default="X/Y"> (and, less often, Highlight="X/Y").
//
// The cases below are chosen to pin the exact failure modes this data has:
//
//   offset 1 (Dimmer)   Default="0/1"    — 0 IS REAL DATA. This is the whole
//                                          reason hasDefault exists; a bare
//                                          number cannot tell this apart
//                                          from a file that said nothing.
//   offset 2 (Shutter1) Default="255/1" + Highlight="255/1" + <ChannelSet>s
//                                        — a non-zero default alongside the
//                                          ChannelSet data the shutter-open
//                                          lookup will later need, proving
//                                          the two survive together.
//   offsets 3,4 (Pan)   Default="32768/2" — a 16-BIT default. Both offsets
//                                          carry the identical record, and
//                                          defaultByteCount is what lets a
//                                          consumer recover coarse 128 at
//                                          offset 3 and fine 0 at offset 4.
//   offset 5 (Zoom)     no Default attr  — must read as UNKNOWN
//                                          (hasDefault false), never as 0.

const DEFAULTS_XML = gdtfDoc(
  'Test', 'Defaults Probe',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="Default Mode" Geometry="Body"><DMXChannels>` +
  `<DMXChannel DMXBreak="1" Geometry="Body" Offset="1">` +
  `<LogicalChannel Attribute="Dimmer">` +
  `<ChannelFunction Name="Dimmer" Attribute="Dimmer" DMXFrom="0/1" Default="0/1"/>` +
  `</LogicalChannel></DMXChannel>` +
  `<DMXChannel DMXBreak="1" Geometry="Body" Offset="2">` +
  `<LogicalChannel Attribute="Shutter1">` +
  `<ChannelFunction Name="Shutter" Attribute="Shutter1" DMXFrom="0/1" Default="255/1" Highlight="255/1">` +
  `<ChannelSet Name="Closed" DMXFrom="0/1"/>` +
  `<ChannelSet Name="Open" DMXFrom="32/1"/>` +
  `<ChannelSet Name="Strobe" DMXFrom="64/1"/>` +
  `</ChannelFunction>` +
  `</LogicalChannel></DMXChannel>` +
  `<DMXChannel DMXBreak="1" Geometry="Body" Offset="3,4">` +
  `<LogicalChannel Attribute="Pan">` +
  `<ChannelFunction Name="Pan" Attribute="Pan" DMXFrom="0/1" Default="32768/2"/>` +
  `</LogicalChannel></DMXChannel>` +
  `<DMXChannel DMXBreak="1" Geometry="Body" Offset="5">` +
  `<LogicalChannel Attribute="Zoom">` +
  `<ChannelFunction Name="Zoom" Attribute="Zoom" DMXFrom="0/1"/>` +
  `</LogicalChannel></DMXChannel>` +
  `</DMXChannels></DMXMode>`
);

// dmxByteAt: the per-offset byte a consumer recovers from a multi-byte
// default — the formula gdtfparse.js's multi-byte note and
// patch.ChannelFunction.DefaultByteCount's doc comment both state, applied
// here independently of the parser so the test derives it rather than
// echoing a parser-provided array.
function dmxByteAt(value, byteCount, index) {
  return (value >>> (8 * (byteCount - 1 - index))) & 0xff;
}

// ---- fixture 10: mode-name channel-count cross-check (Task 2, line of
// investigation 1). A mode name that states its own channel count is an
// INDEPENDENT oracle. gdtfparse.js uses it as a warning only — never as
// parser input — so these two modes must resolve to their structural
// footprints (7 and 8) regardless of what their names claim, with a warning
// raised for exactly the one that disagrees.

const MODE_NAME_ORACLE_XML = gdtfDoc(
  'Test', 'Mode Name Oracle',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="Disagrees (62ch)" Geometry="Body"><DMXChannels>` +
  flatChannelsXml('Body', 7) +
  `</DMXChannels></DMXMode>` +
  `<DMXMode Name="Agrees (8ch)" Geometry="Body"><DMXChannels>` +
  flatChannelsXml('Body', 8) +
  `</DMXChannels></DMXMode>`
);


// ---- fixture 11: Vectorworks placeholder-profile detection (Gap 4).
//
// The real placeholder .gdtf files are not vendored (see fixture 3 & 4's
// note — they are what JDC1_XML / PALADIN_XML above reproduce), so the
// POSITIVE cases below reuse those two: manufacturer "Custom", a single
// generic "DMX Mode", 61 and 23 flat single-offset channels in a dense
// 1..N run. That shape is the whole signature, and it is reproduced
// exactly.
//
// The NEGATIVE cases matter more, because a warning that fires on a real
// fixture is worse than one that misses: it teaches the owner to ignore
// every warning this parser emits. They are, deliberately, one per guard,
// each holding the other two guards TRUE so it proves that guard alone:
//
//   PLACEHOLDER_NEG_SMALL_XML  — Custom + "Default" + flat + dense, but
//                                only 4 channels: the literal "real
//                                4-channel LED par" case, published under
//                                the worst-case manufacturer and mode name.
//   PLACEHOLDER_NEG_NAMED_XML  — Custom + flat + dense + 32 channels, but
//                                the mode name states its channel count.
//   PLACEHOLDER_NEG_16BIT_XML  — Custom + "DMX Mode" + 16 offsets, but one
//                                channel is a 16-bit coarse+fine pair, so
//                                the mode is not a featureless flat run.
//   ERA_800_XML                — a real vendor mode ("Basic", 42 flat
//                                dense 8-bit channels) that passes guards
//                                2 and 3 and is excluded by the
//                                manufacturer alone.
//   PALADIN_CUBE_REAL         — the real Elation vendor file (extract, see
//                                below), whose genuine "RGB 3CH" and
//                                "8bit 4CH" modes ARE flat, dense and
//                                all-8-bit.

const PLACEHOLDER_NEG_SMALL_XML = gdtfDoc(
  'Custom', 'Tiny LED Par',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="Default" Geometry="Body"><DMXChannels>${flatChannelsXml('Body', 4)}</DMXChannels></DMXMode>`
);

const PLACEHOLDER_NEG_NAMED_XML = gdtfDoc(
  'Custom', 'Hand Built Dimmer Pack',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="32 Channel" Geometry="Body"><DMXChannels>${flatChannelsXml('Body', 32)}</DMXChannels></DMXMode>`
);

const PLACEHOLDER_NEG_16BIT_XML = gdtfDoc(
  'Custom', 'Hand Built Mover',
  `<Geometry Name="Body"/>`,
  `<DMXMode Name="DMX Mode" Geometry="Body"><DMXChannels>` +
  dmxChannel('Body', '1,2', 'Pan', true) +
  (() => { let out = ''; for (let i = 3; i <= 16; i++) out += dmxChannel('Body', i, `Ch${i}`); return out; })() +
  `</DMXChannels></DMXMode>`
);

// The real Elation Paladin Cube vendor description.xml, verbatim except
// that every DMXMode other than "Cells 24CH", "8bit 4CH" and "RGB 3CH" (and
// the AttributeDefinitions/Wheels/PhysicalDescriptions/Models blocks the
// parser never reads) has been removed to keep the file to ~21KB. Nothing
// was rewritten: the <Geometries> tree, the surviving <DMXMode> elements
// and every <DMXChannel>/@Offset in them are the vendor's own bytes. This
// is the file the placeholder detector must stay silent on, and the two
// small modes in it are the exact false positives the criterion is designed
// around.
const PALADIN_CUBE_REAL = fs.readFileSync(
  path.join(__dirname, 'paladin_cube_real_extract.xml'), 'utf8');


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
  // CIRCULAR — see fixture 3 & 4's doc comment. These assert only that a
  // flat list of N addressed channels resolves to N; they are not evidence
  // about the real Custom@Light_Instr_* files' footprints.
  check('GLP JDC1 Strobe "DMX Mode" footprint (synthetic 61-channel flat list — NOT ground truth)',
    modeByName(JDC1_XML, 'DMX Mode').footprint, 61);
  check('Elation Paladin Cube "DMX Mode" footprint (synthetic 23-channel flat list — NOT ground truth)',
    modeByName(PALADIN_XML, 'DMX Mode').footprint, 23);
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

  // ---- Task 2: the footprint dispute -----------------------------------
  //
  // "This channel has no function" vs "this channel does not exist" — only
  // the second may shrink a footprint. These two run the SAME 62-channel
  // mode through both shapes.
  const noFeatureAddressed = modeByName(NOFEATURE_ADDRESSED_XML, 'DMX Mode');
  check('NoFeature channel WITH a real Offset still occupies its DMX slot (footprint)',
    noFeatureAddressed.footprint, 62);
  check('NoFeature channel WITH a real Offset is still in channelFunctions',
    noFeatureAddressed.channelFunctions[62] && noFeatureAddressed.channelFunctions[62].attribute, 'NoFeature');
  check('virtual channel (Offset="None") occupies no DMX slot (footprint)',
    modeByName(NOFEATURE_VIRTUAL_XML, 'DMX Mode').footprint, 61);

  // The vendor evidence that excluding virtual channels is CORRECT, not a
  // convenient choice: GLP JDC1's Modes 3/4/5/6 each declare exactly one
  // virtual channel (Offset="") alongside their addressed ones, and each
  // mode's own name independently states its channel count. Those declared
  // counts (68/62/17/11, asserted above) are matched exactly with the
  // virtual channel excluded — and every one of them would be off by one if
  // virtual channels were counted. Counting them to "fix" the disputed
  // placeholder fixtures would therefore break four independently
  // corroborated vendor modes.
  function virtualChannelCount(mode) {
    return mode.channels.filter(ch => ch.offsets.length === 0).length;
  }
  check('JDC1 Mode 3 declares exactly one virtual channel (excluded from its declared 68)',
    virtualChannelCount(jdc1Mode('Mode 3 SPix (68ch)')), 1);
  check('JDC1 Mode 4 declares exactly one virtual channel (excluded from its declared 62)',
    virtualChannelCount(jdc1Mode('Mode 4 SPix PRO (62ch)')), 1);
  check('JDC1 Mode 5 declares exactly one virtual channel (excluded from its declared 17)',
    virtualChannelCount(jdc1Mode('Mode 5 1Pix Pro (17ch)')), 1);
  check('JDC1 Mode 6 declares exactly one virtual channel (excluded from its declared 11)',
    virtualChannelCount(jdc1Mode('Mode 6 Easy (11ch)')), 1);

  // Mode-name count is a cross-check (warning), never parser input.
  const oracle = GdtfParse.parseDescriptionXml(MODE_NAME_ORACLE_XML);
  const oracleDisagrees = oracle.modes.find(m => m.name === 'Disagrees (62ch)');
  const oracleAgrees = oracle.modes.find(m => m.name === 'Agrees (8ch)');
  check('mode-name count never overrides the structural walk (disagreeing mode)',
    oracleDisagrees.footprint, 7);
  check('mode-name count never overrides the structural walk (agreeing mode)',
    oracleAgrees.footprint, 8);
  check('disagreeing mode name raises exactly one warning',
    oracle.warnings.filter(w => w.indexOf('Disagrees (62ch)') >= 0 && w.indexOf('declares 62 channel(s)') >= 0).length, 1);
  check('agreeing mode name raises no warning',
    oracle.warnings.filter(w => w.indexOf('Agrees (8ch)') >= 0).length, 0);
  // The real vendor JDC1 file's six mode names each state a channel count
  // and each matches what the structural walk resolves, so the cross-check
  // must stay silent for it — no count-mismatch warning at all. (The file
  // does raise one unrelated pre-existing geometry warning, about the
  // "Single Back Plate M3" array container; this filters on the mismatch
  // text specifically rather than on the total warning count.)
  check('real vendor JDC1 file raises no mode-name count mismatch',
    GdtfParse.parseDescriptionXml(JDC1_REAL_XML).warnings
      .filter(w => w.indexOf('the mode name declares') >= 0).length, 0);

  // ---- Task 1: GDTF Default / Highlight capture -------------------------
  const defaults = modeByName(DEFAULTS_XML, 'Default Mode');
  const dim = defaults.channelFunctions[1];
  const shutter = defaults.channelFunctions[2];
  const panCoarse = defaults.channelFunctions[3];
  const panFine = defaults.channelFunctions[4];
  const zoom = defaults.channelFunctions[5];

  // "0 is real data" — the single most important assertion here. A parser
  // that simply didn't read Default at all leaves hasDefault undefined, and
  // one that returned a bare number would be indistinguishable from the
  // no-Default channel at offset 5.
  check('Dimmer Default="0/1" is captured as KNOWN', dim.hasDefault, true);
  check('Dimmer Default="0/1" value is 0 (a real resting value, not "unknown")', dim.defaultValue, 0);
  check('Dimmer Default="0/1" byte count', dim.defaultByteCount, 1);

  // ...and the channel that genuinely says nothing must be distinguishable
  // from it by hasDefault alone.
  check('Zoom with no Default attribute is UNKNOWN', zoom.hasDefault, false);
  check('Zoom with no Default attribute has byte count 0', zoom.defaultByteCount, 0);
  check('a known-0 default and an unknown default differ ONLY in hasDefault',
    dim.defaultValue === zoom.defaultValue && dim.hasDefault !== zoom.hasDefault, true);

  // Non-zero default + Highlight + ChannelSets all surviving together.
  check('Shutter1 Default="255/1" value', shutter.defaultValue, 255);
  check('Shutter1 Highlight="255/1" is captured as KNOWN', shutter.hasHighlight, true);
  check('Shutter1 Highlight value', shutter.highlightValue, 255);
  check('Dimmer with no Highlight attribute is UNKNOWN', dim.hasHighlight, false);
  // ChannelSets must survive intact for every channel — the engine's future
  // shutter-open lookup reads these names/DMXFrom values. No shutter-open
  // heuristic is invented here (deliberately out of scope, see the task
  // report); this only proves the raw data arrives unharmed.
  check('Shutter1 ChannelSets survive alongside the defaults (count)', shutter.channelSets.length, 3);
  check('Shutter1 ChannelSet[1] name', shutter.channelSets[1].name, 'Open');
  check('Shutter1 ChannelSet[1] dmxFrom', shutter.channelSets[1].dmxFrom, 32);
  check('Shutter1 ChannelSet[0] dmxFrom 0 survives (0 is real data here too)',
    shutter.channelSets[0].dmxFrom, 0);

  // 16-bit: both offsets of the coarse+fine pair carry the same record, and
  // defaultByteCount is what makes it meaningful for the FINE byte too.
  check('16-bit Pan Default="32768/2" is known at the coarse offset', panCoarse.hasDefault, true);
  check('16-bit Pan Default="32768/2" is known at the fine offset', panFine.hasDefault, true);
  check('16-bit Pan default value is the full 16-bit value, not a coarse byte',
    panCoarse.defaultValue, 32768);
  check('16-bit Pan default byte count', panCoarse.defaultByteCount, 2);
  check('16-bit Pan fine offset carries the identical record',
    panFine.defaultValue === panCoarse.defaultValue && panFine.defaultByteCount === panCoarse.defaultByteCount, true);
  // The whole point of the byte count: recovering each byte's own resting
  // value, in the (coarse, fine) ascending order testpattern.go commits to.
  check('16-bit Pan coarse byte (offset 3) derives to 128',
    dmxByteAt(panCoarse.defaultValue, panCoarse.defaultByteCount, 0), 128);
  check('16-bit Pan fine byte (offset 4) derives to 0',
    dmxByteAt(panFine.defaultValue, panFine.defaultByteCount, 1), 0);


  // ---- Gap 4: Vectorworks placeholder-profile detection -----------------
  //
  // placeholderWarnings isolates the new warning from every other warning
  // the parser can raise for the same file (geometry notes, mode-name count
  // mismatches) by matching on its own distinctive phrase, so a count of 0
  // here means "this criterion did not fire", not "this file was clean".
  function placeholderWarnings(xml) {
    return GdtfParse.parseDescriptionXml(xml).warnings
      .filter(w => w.indexOf('GENERATED PLACEHOLDER') >= 0);
  }

  // Positive: both placeholder shapes are flagged, exactly once each.
  check('placeholder JDC1 (Custom / "DMX Mode" / 61 flat) is flagged exactly once',
    placeholderWarnings(JDC1_XML).length, 1);
  check('placeholder Paladin (Custom / "DMX Mode" / 23 flat) is flagged exactly once',
    placeholderWarnings(PALADIN_XML).length, 1);
  // The warning must name the mode and the footprint it is warning about —
  // a warning that doesn't say which of a fixture's modes is suspect is
  // unactionable.
  check('the placeholder warning names the mode and its channel count',
    placeholderWarnings(JDC1_XML)[0].indexOf('"DMX Mode"') >= 0 &&
    placeholderWarnings(JDC1_XML)[0].indexOf('61 channels') >= 0, true);
  // Detection must not touch footprint computation. This re-asserts the
  // synthetic 61/23 from above AFTER the detector runs.
  check('flagging a placeholder does not change its resolved footprint (61)',
    modeByName(JDC1_XML, 'DMX Mode').footprint, 61);
  check('flagging a placeholder does not change its resolved footprint (23)',
    modeByName(PALADIN_XML, 'DMX Mode').footprint, 23);

  // Negative, guard by guard. Each of these holds the OTHER two guards true.
  check('a real 4-channel LED par is not flagged, even as Custom / "Default" (channel-count guard)',
    placeholderWarnings(PLACEHOLDER_NEG_SMALL_XML).length, 0);
  check('a 32-channel Custom fixture whose mode name states its count is not flagged (mode-name guard)',
    placeholderWarnings(PLACEHOLDER_NEG_NAMED_XML).length, 0);
  check('a 16-offset Custom "DMX Mode" containing a 16-bit channel is not flagged (structure guard)',
    placeholderWarnings(PLACEHOLDER_NEG_16BIT_XML).length, 0);
  check('a real vendor 42-channel flat mode is not flagged (manufacturer guard)',
    placeholderWarnings(ERA_800_XML).length, 0);
  check('the real GLP JDC1 vendor file is not flagged',
    placeholderWarnings(JDC1_REAL_XML).length, 0);

  // The decisive real-file case: the actual Elation Paladin Cube vendor
  // description.xml. Its "RGB 3CH" and "8bit 4CH" modes are genuinely flat,
  // dense and entirely 8-bit — the exact shape the detector looks for — and
  // must not be flagged. The two checks below prove the parse really
  // contained them, so the silence is a decision and not an empty input.
  const paladinReal = GdtfParse.parseDescriptionXml(PALADIN_CUBE_REAL);
  check('real Elation vendor file parses with its genuine "RGB 3CH" mode',
    paladinReal.modes.find(m => m.name === 'RGB 3CH').footprint, 3);
  check('real Elation vendor file parses with its genuine "8bit 4CH" mode',
    paladinReal.modes.find(m => m.name === '8bit 4CH').footprint, 4);
  check('real Elation vendor file parses with its genuine 16-bit "Cells 24CH" mode',
    paladinReal.modes.find(m => m.name === 'Cells 24CH').footprint, 24);
  check('the real Elation Paladin Cube vendor file raises NO placeholder warning',
    placeholderWarnings(PALADIN_CUBE_REAL).length, 0);


  if (failures > 0) {
    console.error(`\n${failures} check(s) failed.`);
    process.exit(1);
  }
  console.log('\nall checks passed.');
}

main();
