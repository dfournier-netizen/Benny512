// gdtf_library_test.js — G3b3: Fixture library › Add from GDTF library, run
// against the LITERAL scripts the browser loads (internal/web/
// gdtf_library_g3b_test.go): ui.js, gdtf-common.js, mvrzip.js, gdtfparse.js,
// mvrimport.js, gdtf-library.js and library.js, in minidom. The GDTF file is
// a real zip around a real Robe extract (robe_bmfl_spot_real_extract.xml,
// see README.md), so the import goes through the real parser.
//
// WHAT IT PROTECTS (approved G3a mockup scenes 9–11; owner rounds 2 and 5)
//  1. Offline search of this computer's files; every row says "Manufacturer
//     file" / "User file" in words, with revision and modes with footprints.
//  2. Import goes through the EXISTING path: the bytes from
//     /api/gdtf/file/{rid} -> the real parser -> the existing confirm step
//     -> POST /api/library/import (merge), with the file's label kept on the
//     retained archive and said in the confirm step.
//  3. GDTF Share: a single pull (POST /api/gdtf/pull) for a file not on this
//     computer, and words for a manufacturer nobody follows; a failed pull
//     says why and imports nothing.
//  4. The 20 MB cap: a bigger file is refused on its row before anyone tries
//     (no Import button), and again by its real byte count.
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const zlib = require('zlib');
const { makeWindow, Event } = require('./minidom.js');
const { DOMParser } = require('./tinydom.js');
const JS = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + JSON.stringify(detail) : ''));
}

// A stored (uncompressed) zip with one description.xml.
function storedZip(name, data) {
  const n = Buffer.from(name), crc = zlib.crc32(data) >>> 0;
  const lh = Buffer.alloc(30); lh.writeUInt32LE(0x04034b50, 0); lh.writeUInt16LE(20, 4); lh.writeUInt32LE(crc, 14);
  lh.writeUInt32LE(data.length, 18); lh.writeUInt32LE(data.length, 22); lh.writeUInt16LE(n.length, 26);
  const cd = Buffer.alloc(46); cd.writeUInt32LE(0x02014b50, 0); cd.writeUInt16LE(20, 4); cd.writeUInt16LE(20, 6); cd.writeUInt32LE(crc, 16);
  cd.writeUInt32LE(data.length, 20); cd.writeUInt32LE(data.length, 24); cd.writeUInt16LE(n.length, 28); cd.writeUInt32LE(0, 42);
  const body = Buffer.concat([lh, n, data]);
  const eocd = Buffer.alloc(22); eocd.writeUInt32LE(0x06054b50, 0); eocd.writeUInt16LE(1, 8); eocd.writeUInt16LE(1, 10);
  eocd.writeUInt32LE(46 + n.length, 12); eocd.writeUInt32LE(body.length, 16);
  const all = Buffer.concat([body, cd, n, eocd]);
  return all.buffer.slice(all.byteOffset, all.byteOffset + all.length);
}
const GDTF = storedZip('description.xml', fs.readFileSync(path.join(__dirname, 'robe_bmfl_spot_real_extract.xml')));
const MB = 1024 * 1024;

const entry = (o) => Object.assign({ rid: 0, fixture: '', manufacturer: 'Robe Lighting', revision: 'v1', creationDate: 1790000000, lastModified: 1790000000,
  uploader: 'Manuf.', rating: null, version: '1.2', creator: 'x', uuid: 'u', filesize: 6 * MB, modes: [], uploaderLabel: 'Manufacturer',
  inLibrary: true, sha256: 'abc', description: null, inFixtureLibrary: false }, o);
const LOCAL = [
  entry({ rid: 11, fixture: 'BMFL Spot', revision: 'DMX v1.3', uploaderLabel: 'Manufacturer', modes: [{ name: 'Mode 1 - Standard 16 bit', dmxfootprint: 49 }, { name: 'Mode 2', dmxfootprint: null }],
    description: { state: 'read', longName: 'Robe BMFL Spot long name' } }),
  entry({ rid: 12, fixture: 'BMFL Spot (tour edit)', revision: 'tour v2', uploader: 'User', uploaderLabel: 'User', modes: [{ name: 'Mode 1', dmxfootprint: 49 }], inFixtureLibrary: true }),
  entry({ rid: 13, fixture: 'MAC Ultra Performance', manufacturer: 'Martin Professional', revision: 'fw 2.4.0', filesize: 21 * MB, modes: [{ name: 'Basic', dmxfootprint: 41 }] }),
];
const SHARE = [
  entry({ rid: 21, fixture: 'Sharpy', manufacturer: 'Clay Paky', revision: 'fw 1.4', inLibrary: false, sha256: '', modes: [{ name: 'Standard', dmxfootprint: 16 }] }),
  entry({ rid: 11, fixture: 'BMFL Spot', revision: 'DMX v1.3', inLibrary: true }),
];

const { window, document } = makeWindow();
const calls = [];
let pullFails = '', bigBytes = false;
const ctx = vm.createContext({
  window, document, console, setTimeout, clearTimeout, DOMParser, TextDecoder, Uint8Array, DataView, ArrayBuffer, Promise,
  DecompressionStream: globalThis.DecompressionStream, Response: globalThis.Response, Blob: globalThis.Blob,
  btoa: s => Buffer.from(s, 'binary').toString('base64'),
  Api: {
    getLibrary: async () => ({ records: [] }),
    getPatch: async () => ({ patch: null }),
    libraryExportUrl: () => '/api/library/export',
    gdtfStatus: async () => ({ enabled: true, files: 3, fixtureLibraryMaxBytes: 20 * MB, lastSync: '2026-11-17T21:40:00Z',
      manufacturers: [{ name: 'Robe', matched: true, shareName: 'Robe Lighting' }] }),
    gdtfSearch: async (q, inWhere, limit) => {
      calls.push(['search', q, inWhere, limit]);
      const list = inWhere === 'share' ? SHARE : LOCAL;
      const words = String(q || '').toLowerCase().split(/\s+/).filter(Boolean);
      const res = list.filter(r => words.every(w => (r.manufacturer + ' ' + r.fixture).toLowerCase().includes(w)));
      return { in: inWhere, query: q, count: res.length, results: JSON.parse(JSON.stringify(res)) };
    },
    gdtfPull: async rid => { calls.push(['pull', rid]); if (pullFails) throw new Error(pullFails); return {}; },
    gdtfFileBytes: async rid => {
      calls.push(['file', rid]);
      return { bytes: bigBytes ? new ArrayBuffer(21 * MB) : GDTF.slice(0), disposition: `attachment; filename="Robe Lighting@BMFL Spot@DMX v1.3.gdtf"`, uploader: rid === 12 ? 'User' : 'Manufacturer' };
    },
    importLibrary: async (doc, mode) => { calls.push(['import', doc, mode]); return { added: 1, updated: 0, skipped: 0 }; },
  },
  Workspace: { ask: async () => true },
});
const load = f => vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
ctx.escapeHtml = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); // nodes.js's global
for (const f of ['ui.js', 'gdtf-common.js', 'mvrzip.js', 'gdtfparse.js', 'mvrimport.js', 'gdtf-library.js', 'library.js']) load(f);
const UI = vm.runInContext('UI', ctx);
UI.showPanel = d => { d.open = true; };
const LibraryPanel = vm.runInContext('LibraryPanel', ctx);
const wait = ms => new Promise(r => setTimeout(r, ms));
const text = el => (el ? el.textContent : '').replace(/\s+/g, ' ').trim();

(async () => {
  await LibraryPanel.open();
  const dialog = document.querySelector('dialog');
  const q = s => dialog.querySelector(s);
  const toggle = q('[data-gdtf-add-toggle]');
  check(toggle && /Add from GDTF library/.test(text(toggle)) && toggle.getAttribute('aria-pressed') === 'false' && q('[data-gdtf-add]').hidden,
    'the Fixture library has "Add from GDTF library", closed at first');
  toggle.click(); await wait(30);
  check(toggle.getAttribute('aria-pressed') === 'true' && !q('[data-gdtf-add]').hidden, 'pressing it opens the section (aria-pressed)');
  check(calls.some(c => c[0] === 'search' && c[2] === 'library'), 'it searches the files on this computer first (offline)');
  const area = q('[data-gdtf-add]');
  check(/On this computer \(3 files\)/.test(text(area)) && /Works offline/.test(text(area)), 'source "On this computer" with the file count, said to work offline');
  const rows = area.querySelectorAll('[data-gdtf-rid]');
  check(rows.length === 3, 'three rows', rows.length);
  const r11 = area.querySelector('[data-gdtf-rid="11"]'), r12 = area.querySelector('[data-gdtf-rid="12"]'), r13 = area.querySelector('[data-gdtf-rid="13"]');
  check(/Manufacturer file/.test(text(r11)) && /User file/.test(text(r12)), 'every row says Manufacturer file / User file in words');
  check(/Revision “DMX v1\.3” · GDTF 1\.2 · 6\.0 MB · updated/.test(text(r11)) && /Long name: Robe BMFL Spot long name/.test(text(r11)), 'revision, GDTF version, size, date and long name');
  check(/Modes: Mode 1 - Standard 16 bit · 49 ch; Mode 2 · channels not listed/.test(text(r11)), 'modes with footprints; a missing footprint is said, never 0', text(r11));
  check(/In your Fixture library/.test(text(r12)) && !/In your Fixture library/.test(text(r11)), 'a file already in the Fixture Library says so');
  check(/Too large for the Fixture Library: 21\.0 MB/.test(text(r13)) && /up to 20 MB/.test(text(r13)) && !r13.querySelector('[data-gdtf-import]'),
    'a 21 MB file is refused on its row before anyone tries, with no Import button', text(r13));

  // Typing searches.
  const input = q('#gdtfQuery');
  input.value = 'tour'; input.dispatchEvent(new Event('input'));
  await wait(320);
  check(calls.some(c => c[0] === 'search' && c[1] === 'tour'), 'typing searches (debounced)');
  check(/1 file matches “tour”/.test(text(q('[data-gdtf-count]'))), 'the count is said in words', text(q('[data-gdtf-count]')));
  input.value = ''; input.dispatchEvent(new Event('input')); await wait(320);

  // Import through the existing path.
  q('[data-gdtf-rid="12"] [data-gdtf-import]').click();
  await wait(80);
  check(calls.some(c => c[0] === 'file' && c[1] === 12), 'Import… fetches the file bytes from the GDTF library');
  const imp = q('[data-library-import]');
  check(/Review the import, then apply/.test(text(q('[data-library-status]'))) && !!imp.querySelector('[data-apply-library]'),
    'the existing confirm step appears (nothing imported yet)', text(q('[data-library-status]')));
  check(!calls.some(c => c[0] === 'import'), 'nothing is imported before Apply');
  check(/From the GDTF library · User file/.test(text(imp)) && /Robe Lighting@BMFL Spot@DMX v1\.3\.gdtf/.test(text(imp)), 'the confirm step says where it came from and its label', text(imp));
  imp.querySelector('[data-apply-library]').click();
  await wait(50);
  const ic = calls.find(c => c[0] === 'import');
  const sf = ic && ic[1].records[0].sourceFiles && ic[1].records[0].sourceFiles[0];
  check(ic && ic[2] === 'merge' && ic[1].records[0].modes.length > 0, 'Apply import sends the parsed record through POST /api/library/import (merge)', ic && ic[1].records[0].modes.length);
  check(sf && sf.uploaderLabel === 'User' && sf.name === 'Robe Lighting@BMFL Spot@DMX v1.3.gdtf' && sf.data.length > 100, 'the archive is kept with its User label', sf && { name: sf.name, label: sf.uploaderLabel });

  // GDTF Share: single pull.
  calls.length = 0;
  q('[data-gdtf-source="share"]').click(); await wait(40);
  const area2 = q('[data-gdtf-add]');
  check(calls.some(c => c[0] === 'search' && c[2] === 'share') && q('[data-gdtf-source="share"]').getAttribute('aria-pressed') === 'true', 'GDTF Share (online) searches the cached list');
  const s21 = area2.querySelector('[data-gdtf-rid="21"]');
  check(/Not downloaded/.test(text(s21)) && /Clay Paky is not followed: this downloads this one file only/.test(text(s21)) && /Download and import/.test(text(s21)) && /Long name: known once downloaded/.test(text(s21)),
    'a file not on this computer says so, and that it is a single pull', text(s21));
  check(/On this computer/.test(text(area2.querySelector('[data-gdtf-rid="11"]'))), 'a file already downloaded says "On this computer"');
  pullFails = 'GDTF Share could not be reached: dial tcp: i/o timeout';
  s21.querySelector('[data-gdtf-import]').click(); await wait(60);
  check(calls.some(c => c[0] === 'pull' && c[1] === 21) && !calls.some(c => c[0] === 'file') && /Not imported: Sharpy: GDTF Share could not be reached/.test(text(q('[data-library-status]'))),
    'a failed pull says why and imports nothing', text(q('[data-library-status]')));
  pullFails = ''; calls.length = 0;
  q('[data-gdtf-add] [data-gdtf-rid="21"] [data-gdtf-import]').click(); await wait(80);
  check(calls[0] && calls[0][0] === 'pull' && calls[1] && calls[1][0] === 'file' && /Downloaded from GDTF Share/.test(text(q('[data-library-import]'))),
    'Download and import pulls the one file, then the same confirm step', calls.map(c => c[0]));

  // The real byte count is the authority.
  q('[data-library-import] [data-cancel-library]').click();
  bigBytes = true; calls.length = 0;
  q('[data-gdtf-source="library"]').click(); await wait(40);
  q('[data-gdtf-add] [data-gdtf-rid="11"] [data-gdtf-import]').click(); await wait(60);
  check(/Not imported: BMFL Spot is 21\.0 MB, and the Fixture Library takes GDTF files up to 20 MB/.test(text(q('[data-library-status]'))) && !q('[data-apply-library]'),
    'a file whose real bytes are over 20 MB is refused', text(q('[data-library-status]')));

  if (failures) { console.log(failures + ' failure(s)'); process.exit(1); }
  console.log('all passed');
})().catch(e => { console.error(e); process.exit(1); });
