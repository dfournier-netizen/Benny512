'use strict';
// Runs the LITERAL mvrimport.js the browser loads, and asserts that every
// GDTF entering Benny512 puts its FULL mode list into the Fixture Library.
//
// Why this test loads the real file rather than reimplementing the shape:
// the defect it guards is two hand-rolled copies of one document drifting
// apart. library.js used to build the library record inline; patch.js now
// imports GDTFs too. A test that built its own expected record would agree
// with whichever copy it was written from and prove nothing about the other.
//
// The rule being pinned: a patch entry stores ONE mode (patch.Entry: Mode,
// Footprint, ChannelFunctions). Import is therefore the only moment a
// fixture's other modes exist — harvesting the show afterwards can only ever
// return the one mode each entry actually uses. A GDTF import that keeps
// just the applied mode has silently discarded the rest.

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const jsDir = path.resolve(__dirname, '..');

let failures = 0;
function check(cond, msg) {
  if (!cond) { failures++; console.error('FAIL: ' + msg); }
}

// --- load mvrimport.js with just enough of its world -----------------------
// It reaches for MvrZip, GdtfParse and Api at call time; the two functions
// under test touch only Api, so the rest are present but inert.
const captured = [];
const ctx = {
  console,
  Date,
  Uint8Array,
  String,
  btoa: (s) => Buffer.from(s, 'binary').toString('base64'),
  TextDecoder,
  Map,
  Error,
  MvrZip: {},
  GdtfParse: {},
  Api: {
    importLibrary: async (doc, mode) => { captured.push({ doc, mode }); return { ok: true }; },
  },
};
ctx.globalThis = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(path.join(jsDir, 'mvrimport.js'), 'utf8'), ctx);
// `const MvrImport = ...` at file scope is NOT a context property, so reach
// it by evaluating its name — the harness gotcha this project already hit
// once in nodes_universe_test.js.
const MvrImport = vm.runInContext('MvrImport', ctx);

check(typeof MvrImport.libraryDocFromGdtf === 'function',
  'mvrimport.js does not export libraryDocFromGdtf — the shared builder is the whole point; ' +
  'without it each caller hand-rolls the record and they drift');
check(typeof MvrImport.rememberGdtfInLibrary === 'function',
  'mvrimport.js does not export rememberGdtfInLibrary');

// A fixture with three personalities, the ordinary case: an operator patches
// one of them and the other two are exactly what the library exists to keep.
const parsed = {
  manufacturer: 'GLP',
  model: 'JDC1',
  fixtureType: 'JDC1',
  modes: [
    { name: '24ch Extended', footprint: 24, channelFunctions: { 0: { attribute: 'Dimmer' } } },
    { name: '16ch Standard', footprint: 16, channelFunctions: {} },
    { name: '8ch Basic', footprint: 8, channelFunctions: {} },
  ],
  warnings: [],
};

// --- 1. the document carries every mode ------------------------------------
const doc = MvrImport.libraryDocFromGdtf(parsed, 'GLP@JDC1.gdtf', null);

check(doc.format === 'benny512-fixture-library',
  `doc.format = ${doc.format}, want benny512-fixture-library (the server rejects anything else)`);
check(doc.records && doc.records.length === 1, 'expected exactly one record');

const rec = (doc.records || [])[0] || {};
check(rec.manufacturer === 'GLP' && rec.model === 'JDC1',
  `record identity = ${rec.manufacturer}/${rec.model}, want GLP/JDC1`);

const names = (rec.modes || []).map(m => m.name);
check(rec.modes && rec.modes.length === 3,
  `record carries ${names.length} mode(s) [${names.join(', ')}], want all 3. ` +
  'A patch entry stores one mode, so any mode dropped here is unrecoverable later.');
['24ch Extended', '16ch Standard', '8ch Basic'].forEach(n => {
  check(names.includes(n), `mode "${n}" is missing from the library record`);
});
check((rec.modes || []).every(m => m.origin && m.origin.source === 'gdtf'),
  'every mode must carry gdtf provenance, or the library cannot tell a parsed ' +
  'manufacturer file from a profile harvested off a patch');
check((rec.modes || []).some(m => m.footprint === 24) && (rec.modes || []).some(m => m.footprint === 8),
  'footprints were not carried through with their modes');

// Channel functions ride along, not just names and counts — a mode without
// them cannot drive Rig Check's function-aware tests.
const ext = (rec.modes || []).find(m => m.name === '24ch Extended') || {};
check(ext.channelFunctions && ext.channelFunctions[0],
  'channelFunctions were dropped; a remembered mode that cannot drive Function check ' +
  'is a footprint, not a profile');

// --- 2. the original archive is retained when the bytes are supplied --------
check(!rec.sourceFiles,
  'sourceFiles present although no buffer was supplied — never invent an archive');

const withBytes = MvrImport.libraryDocFromGdtf(parsed, 'GLP@JDC1.gdtf', new Uint8Array([1, 2, 3, 4]).buffer);
const wrec = withBytes.records[0];
check(wrec.sourceFiles && wrec.sourceFiles.length === 1 && wrec.sourceFiles[0].name === 'GLP@JDC1.gdtf',
  'the original .gdtf archive was not retained when its bytes were available');
check(typeof (wrec.sourceFiles || [{}])[0].data === 'string',
  'retained archive is not base64 text');

// --- 3. remembering posts a merge, and never throws -------------------------
(async () => {
  captured.length = 0;
  const err = await MvrImport.rememberGdtfInLibrary(parsed, 'GLP@JDC1.gdtf', null);
  check(err === null, `rememberGdtfInLibrary returned an error on a good import: ${err}`);
  check(captured.length === 1, `expected one importLibrary call, got ${captured.length}`);
  check(captured[0] && captured[0].mode === 'merge',
    `import mode = ${captured[0] && captured[0].mode}, want "merge" — a GDTF import must never ` +
    'REPLACE the library, which would discard every other fixture type the user has collected');
  check((captured[0].doc.records[0].modes || []).length === 3,
    'the posted document lost modes between builder and call');

  // A library failure must be REPORTED, not thrown: it runs alongside an
  // import the operator actually asked for (patching entries), and losing
  // their patch to a library error would be the worse bug.
  ctx.Api.importLibrary = async () => { throw new Error('disk full'); };
  let threw = false;
  let msg = null;
  try {
    msg = await MvrImport.rememberGdtfInLibrary(parsed, 'x.gdtf', null);
  } catch (e) {
    threw = true;
  }
  check(!threw, 'rememberGdtfInLibrary threw on a library failure — that would abort the ' +
    'patch import the operator actually asked for');
  check(typeof msg === 'string' && msg.indexOf('disk full') >= 0,
    `expected the failure reported back for the status line, got ${JSON.stringify(msg)}`);

  // Nothing to remember is not an error, and must not post an empty record.
  captured.length = 0;
  ctx.Api.importLibrary = async (doc, mode) => { captured.push({ doc, mode }); return {}; };
  const none = await MvrImport.rememberGdtfInLibrary({ modes: [] }, 'empty.gdtf', null);
  check(none === null && captured.length === 0,
    'a GDTF with no modes posted a record anyway; an empty fixture type is noise in the library');

  if (failures) {
    console.error(`\n${failures} check(s) failed`);
    process.exit(1);
  }
  console.log('library_allmodes_test.js: all checks passed');
})();
