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

  if (failures > 0) {
    console.error(`\n${failures} check(s) failed.`);
    process.exit(1);
  }
  console.log('\nall checks passed.');
}

main();
