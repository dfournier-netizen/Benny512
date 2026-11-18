// library_cap_test.js — G3b1, owner round 5 #1: the Fixture Library takes a
// GDTF file up to 20 MB (was 8 MB). Runs the LITERAL library.js the browser
// loads (internal/web/library_cap_g3b_test.go) in minidom: an 11 MB .gdtf
// picked from disk reaches the confirm step; one over 20 MB is refused before
// it is read, and the refusal says 20 MB.
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { makeWindow } = require('./minidom.js');
const JS = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + JSON.stringify(detail) : ''));
}

const { window, document } = makeWindow();
let parsed = 0;
const ctx = vm.createContext({
  window, document, console,
  Api: {
    getLibrary: async () => ({ records: [] }),
    getPatch: async () => ({ patch: null }),
    libraryExportUrl: () => '/api/library/export',
  },
  UI: { showPanel: d => { d.open = true; } },
  MvrImport: {
    parseGdtfFile: async () => { parsed++; return { manufacturer: 'QA', model: 'Big', modes: [{ name: 'Std', footprint: 1 }], warnings: [] }; },
    libraryDocFromGdtf: () => ({ format: 'benny512-fixture-library', schemaVersion: 2, records: [{}] }),
  },
  Workspace: { ask: async () => true },
});
vm.runInContext(fs.readFileSync(path.join(JS, 'library.js'), 'utf8') + '\nthis.LibraryPanel = LibraryPanel;', ctx, { filename: 'library.js' });
const MB = 1024 * 1024;
const fakeFile = (name, size) => ({ name, size, arrayBuffer: async () => new ArrayBuffer(8), text: async () => '{}' });

(async () => {
  await ctx.LibraryPanel.open();
  const dialog = document.querySelector('dialog');
  const pick = async f => { await dialog.querySelector('[data-library-file]').onchange({ target: { files: [f] } }); };
  const statusText = () => dialog.querySelector('[data-library-status]').textContent;

  await pick(fakeFile('Big@Spot@1.gdtf', 11 * MB));
  check(parsed === 1 && /Review the import/.test(statusText()) && !!dialog.querySelector('[data-apply-library]'),
    'an 11 MB GDTF reaches the confirm step', statusText());

  await pick(fakeFile('Big@Spot@2.gdtf', 20 * MB));
  check(parsed === 2, 'a GDTF of exactly 20 MB is read', statusText());

  await pick(fakeFile('Huge@Spot@1.gdtf', 20 * MB + 1));
  check(parsed === 2, 'a GDTF over 20 MB is not read');
  check(/20 MB/.test(statusText()) && !/8 MB/.test(statusText()), 'the refusal says 20 MB', statusText());

  if (failures) { console.log(failures + ' failure(s)'); process.exit(1); }
  console.log('all passed');
})().catch(e => { console.error(e); process.exit(1); });
