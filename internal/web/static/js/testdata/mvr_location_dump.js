'use strict';

// Runs the LITERAL mvrparse.js + mvrimport.js the browser loads over a
// GeneralSceneDescription.xml file and prints the entryRequest-shaped
// entries mvrimport.js produces as JSON on stdout. The Go side
// (internal/web/mvrlocation_test.go) posts those bytes, unmodified, to the
// real POST /api/patch/import handler — so the seam under test is the actual
// browser parser -> wire -> Go decoder path, not a struct agreeing with
// itself.
//
// Usage: node mvr_location_dump.js <GeneralSceneDescription.xml> <fallback|resolved>
//
// Only the zip layer and the GDTF description parser are stood in for: the
// zip because the test data is a bare GeneralSceneDescription.xml (the real
// archive's GDTFs are not redistributed here), and the GDTF parser because
// position data does not come from GDTF at all.
//   fallback: the archive holds only the scene description, so every fixture
//             takes mvrimport.js's "GDTF file not found" path.
//   resolved: every referenced GDTF "exists" and parses to a profile whose
//             one mode is named after the fixture's GDTFMode, so every
//             fixture takes the normal resolved path.
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { DOMParser } = require('./tinydom');

const [xmlPath, mode] = process.argv.slice(2);
if (!xmlPath || (mode !== 'fallback' && mode !== 'resolved')) {
  console.error('usage: node mvr_location_dump.js <xml> <fallback|resolved>');
  process.exit(2);
}
const xml = fs.readFileSync(xmlPath, 'utf8');
const jsDir = path.resolve(__dirname, '..');

const OUTER = { outer: true };
const GSD = 'GeneralSceneDescription.xml';
const specs = Array.from(xml.matchAll(/<GDTFSpec>([^<]*)<\/GDTFSpec>/g)).map(m => m[1]).filter(Boolean);
const modes = Array.from(new Set(Array.from(xml.matchAll(/<GDTFMode>([^<]*)<\/GDTFMode>/g)).map(m => m[1])));

const MvrZip = {
  async openZip(buf) {
    if (buf === OUTER) {
      return {
        names: mode === 'resolved' ? [GSD, ...new Set(specs)] : [GSD],
        async read(name) {
          if (name === GSD) return Buffer.from(xml, 'utf8');
          return { gdtf: name };
        },
      };
    }
    // A resolved GDTF archive: one description.xml whose text is irrelevant
    // to the stand-in parser below.
    return { names: ['description.xml'], async read() { return Buffer.from('', 'utf8'); } };
  },
};
const GdtfParse = {
  parseDescriptionXml() {
    return {
      fixtureType: 'Stand-in type',
      warnings: [],
      modes: modes.map(name => ({ name, footprint: 8, channelFunctions: {}, wheels: [] })),
    };
  },
};

const ctx = { console, DOMParser, TextDecoder, Map, Set, Error, Uint8Array, Buffer, MvrZip, GdtfParse, Api: {} };
ctx.globalThis = ctx;
vm.createContext(ctx);
for (const f of ['mvrparse.js', 'mvrimport.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
const MvrImport = vm.runInContext('MvrImport', ctx);

MvrImport.parseMvrFile(OUTER).then(res => {
  process.stdout.write(JSON.stringify({ entries: res.entries, warnings: res.warnings }));
}).catch(e => {
  console.error(e && e.stack || e);
  process.exit(1);
});
