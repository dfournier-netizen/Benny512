'use strict';

// gdtf_shortname_dump.js (J1b, owner 2026-10-12: short fixture-type names in
// fader labels, from the GDTF FixtureType ShortName). Runs the LITERAL
// gdtfparse.js over the three real vendor extracts and the LITERAL
// mvrparse.js + mvrimport.js over the real Capture scene extract, and prints
// {shortNames, entries} as JSON; internal/web/shortname_j1b_test.go posts the
// entries, unmodified, to the real POST /api/patch/import handler.
//
// Stand-ins: the zip layer (only the scene description is redistributed
// here), so every GDTF the scene names is served as the real Paladin Cube
// extract; and, so each fixture resolves to a mode with a profile, every
// <GDTFMode> of the scene is read as "Cells 24CH" (a mode that extract has).
// Neither file's content is otherwise changed.
//
// usage: node gdtf_shortname_dump.js
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { DOMParser } = require('./tinydom');

const here = __dirname;
const jsDir = path.resolve(here, '..');
const read = f => fs.readFileSync(path.join(here, f), 'utf8');
const GdtfParse = vm.runInContext(fs.readFileSync(path.join(jsDir, 'gdtfparse.js'), 'utf8') + '\nGdtfParse;',
  vm.createContext({ DOMParser, console }), { filename: 'gdtfparse.js' });

const shortNames = {};
for (const f of ['paladin_cube_real_extract.xml', 'robe_bmfl_spot_real_extract.xml', 'robe_ledbeam100_real_extract.xml']) {
  shortNames[f] = GdtfParse.parseDescriptionXml(read(f)).shortName;
}

const scene = read('capture_demo_show_real_extract.xml').replace(/<GDTFMode>[^<]*<\/GDTFMode>/g, '<GDTFMode>Cells 24CH</GDTFMode>');
const paladin = read('paladin_cube_real_extract.xml');
const OUTER = { outer: true };
const GSD = 'GeneralSceneDescription.xml';
const specs = Array.from(scene.matchAll(/<GDTFSpec>([^<]*)<\/GDTFSpec>/g)).map(m => m[1]).filter(Boolean);
const MvrZip = {
  async openZip(buf) {
    if (buf === OUTER) {
      return { names: [GSD, ...new Set(specs)], async read(name) { return name === GSD ? Buffer.from(scene, 'utf8') : { gdtf: name }; } };
    }
    return { names: ['description.xml'], async read() { return Buffer.from(paladin, 'utf8'); } };
  },
};
const ctx = { console, DOMParser, TextDecoder, Map, Set, Error, Uint8Array, Buffer, MvrZip, GdtfParse, Api: {} };
ctx.globalThis = ctx;
vm.createContext(ctx);
for (const f of ['mvrparse.js', 'mvrimport.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
vm.runInContext('MvrImport', ctx).parseMvrFile(OUTER).then(res => {
  process.stdout.write(JSON.stringify({ shortNames, entries: res.entries, warnings: res.warnings }));
}).catch(e => { console.error(e && e.stack || e); process.exit(1); });
