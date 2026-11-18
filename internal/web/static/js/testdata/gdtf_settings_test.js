// gdtf_settings_test.js — G3b2: Settings › 7 GDTF Share and 9 About, run
// against the LITERAL ui.js, gdtf-common.js and gdtf-settings.js the browser
// loads (internal/web/gdtf_settings_g3b_test.go), in minidom, over status
// documents in the shape GET /api/gdtf/status answers (gdtf.go).
//
// WHAT IT PROTECTS (approved G3a mockup scenes 1–8; owner rounds 3 and 5)
//  1. Every state in words: not set up, up to date, syncing with "file n of
//     m · now <manufacturer> (manufacturer i of k)", offline · last synced,
//     refused, partly synced, unmatched names; colour never alone.
//  2. The "Which network sync uses" box is ALWAYS there and names the
//     lighting adapter (and "picked automatically"); a refusal adds the
//     guidance and a button that moves focus to the Art-Net adapter field.
//  3. The buttons act at once: Save login sends the login and starts the
//     first sync; Follow / Stop following / Follow "X" instead send the new
//     list straight away; Sync now is refused (aria-disabled) while a sync
//     runs or with no login. Stop following says the files stay.
//  4. The password never reaches the page: the field is cleared after Save
//     and no status text carries it.
//  5. About shows exactly the --version line; "Powered by GDTF" with a
//     labelled logo placeholder (role=img), not a drawn logo.
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

const PASSWORD = 'Zq9-unique-GDTF-password-7731';
const T = '2026-11-17T21:40:00Z';
const mfr = (name, o) => Object.assign({ name, matched: true, shareName: name, shareManufacturerFiles: 10, shareUserFiles: 2, localFiles: 12,
  downloaded: 0, replaced: 0, failed: 0, result: '', similar: [], checkedAt: T, notYetChecked: false }, o || {});
function status(o) {
  return Object.assign({
    enabled: true, running: false, state: 'ok', message: 'Library up to date: 1,505 file(s) for the followed manufacturers',
    progress: { done: 0, total: 0, manufacturer: '', manufacturerIndex: 0, manufacturerCount: 0 },
    lastAttempt: T, lastSync: T, files: 1505, followed: ['Robe', 'Martin', 'GLP'],
    manufacturers: [mfr('Robe', { shareName: 'Robe Lighting' }), mfr('Martin', { matched: false, shareName: '', similar: ['Martin Professional'], localFiles: 0 }), mfr('GLP')],
    pending: [], intervalMinutes: 120, unreadable: 0, notYetRead: 0, unreadableFiles: [],
    internetRoute: { localAddress: '192.168.1.22', adapter: 'Wi-Fi', at: T },
    lightingAdapters: [
      { protocol: 'Art-Net', bound: true, name: 'Ethernet 2', addresses: ['2.0.0.10/8'], auto: false, sameAsArtNet: false },
      { protocol: 'sACN', bound: true, name: 'Ethernet 2', addresses: ['2.0.0.10/8'], auto: false, sameAsArtNet: true }],
    fixtureLibraryMaxBytes: 20971520,
    login: { saved: true, user: 'dom', storage: 'encrypted', message: 'Saved · user dom' },
  }, o || {});
}

const { window, document } = makeWindow();
document.body.innerHTML = `<section id="screen-settings" class="screen active">
  <select id="nicSelect" class="b5-select"><option value="">(default / any)</option></select>
  <div id="gdtfShareRoot"></div><div id="aboutRoot"></div></section>`;
const calls = [];
let current = status();
const ctx = vm.createContext({
  window, document, console, setTimeout: () => 0, clearTimeout: () => {},
  Api: {
    gdtfStatus: async () => JSON.parse(JSON.stringify(current)),
    getVersion: async () => ({ version: 'v0.3.5', commit: 'abc1234', built: '2026-11-18T09:00:00Z', line: 'benny512 v0.3.5 (commit abc1234, built 2026-11-18T09:00:00Z)' }),
    gdtfSetLogin: async (u, p) => { calls.push(['setLogin', u, p]); current = status({ login: { saved: true, user: u, storage: 'encrypted', message: 'Saved · user ' + u } }); return current.login; },
    gdtfClearLogin: async () => { calls.push(['clearLogin']); return {}; },
    gdtfTestLogin: async (u, p) => { calls.push(['test', u, p]); return { ok: true, state: 'ok', message: 'GDTF Share accepted the login' }; },
    gdtfSetManufacturers: async names => { calls.push(['manufacturers', names]); return { followed: names }; },
    gdtfSync: async () => { calls.push(['sync']); return { status: 'started' }; },
  },
});
const load = f => vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
load('ui.js');
ctx.escapeHtml = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
load('gdtf-common.js');
load('gdtf-settings.js');
const GdtfSettings = vm.runInContext('GdtfSettings', ctx);
const root = () => document.getElementById('gdtfShareRoot');
const text = el => (el ? el.textContent : '').replace(/\s+/g, ' ').trim();
const q = s => root().querySelector(s);
const tick = () => new Promise(r => setImmediate(r));
async function show(s) { current = s; await GdtfSettings.refresh(); await tick(); }

(async () => {
  // --- 1. not set up ---------------------------------------------------------
  current = status({ state: 'never', lastSync: null, lastAttempt: null, files: 0, internetRoute: null,
    manufacturers: ['Robe', 'GLP'].map(n => mfr(n, { notYetChecked: true, matched: false })),
    login: { saved: false, user: '', storage: 'none', message: 'No GDTF Share login saved' } });
  await GdtfSettings.init(); await tick();
  check(/Not set up/.test(text(q('[role="status"]'))) && /No GDTF Share login saved/.test(text(root())), 'not set up: says so in words', text(q('[role="status"]')));
  const pass = q('#gdtfPass');
  check(pass && pass.getAttribute('type') === 'password' && q('#gdtfUser'), 'login fields: user name and a password field');
  check(/Lighting network: sync never uses it\./.test(text(q('[data-gdtf-route]'))) && /Ethernet 2 \(2\.0\.0\.10\)/.test(text(q('[data-gdtf-route]'))),
    'the route box is there before any sync, naming the lighting adapter', text(q('[data-gdtf-route]')));
  check(/No connection to GDTF Share has been made/.test(text(q('[data-gdtf-route]'))), 'no route yet: says none, guesses none');
  check(q('[data-gdtf-sync]').getAttribute('aria-disabled') === 'true' && /Save a login first/.test(text(root())), 'Sync now refused with no login, and says why');
  q('[data-gdtf-sync]').click(); await tick();
  check(!calls.some(c => c[0] === 'sync'), 'pressing a refused Sync now sends nothing');
  check(/Not checked yet/.test(text(root())), 'a followed name not looked up yet says so');
  q('#gdtfUser').value = 'dom'; q('#gdtfPass').value = PASSWORD;
  q('[data-gdtf-save]').click(); await tick(); await tick(); await tick();
  check(calls.some(c => c[0] === 'setLogin' && c[1] === 'dom' && c[2] === PASSWORD), 'Save login sends the login at once (no Apply)');
  check(calls.some(c => c[0] === 'sync'), 'and starts the first sync, as the screen promised');
  check(!document.body.innerHTML.includes(PASSWORD) && !(q('#gdtfPass') && q('#gdtfPass').value), 'the password is not on the page after Save');
  check(/Saved · user dom/.test(text(root())) && /never shown/.test(text(root())), 'saved: the server\'s "Saved · user dom", password never shown');

  // --- 2. up to date ------------------------------------------------------------
  calls.length = 0;
  await show(status());
  check(/Up to date/.test(text(q('[role="status"]'))) && /Last synced/.test(text(q('[role="status"]'))), 'up to date: badge words and last synced');
  check(/Wi-Fi \(192\.168\.1\.22\)/.test(text(q('[data-gdtf-route]'))), 'the route the last sync took is named');
  check(/on GDTF Share as “Robe Lighting”/.test(text(root())), 'a followed name spelt differently on GDTF Share says how');
  check(/1 followed name matched nothing on GDTF Share/.test(text(q('[role="status"]'))), 'unmatched names counted in the status, in words');
  check(/Not found on GDTF Share/.test(text(root())) && !!q('[data-gdtf-instead="Martin"]'), 'unmatched: "Not found", similar name offered');
  q('[data-gdtf-instead="Martin"]').click(); await tick(); await tick();
  const inst = calls.find(c => c[0] === 'manufacturers');
  check(inst && JSON.stringify(inst[1]) === JSON.stringify(['Robe', 'Martin Professional', 'GLP']), 'Follow "X" instead replaces the name, at once', inst);
  calls.length = 0;
  await show(status());
  q('[data-gdtf-unfollow="GLP"]').click(); await tick(); await tick();
  const un = calls.find(c => c[0] === 'manufacturers');
  check(un && JSON.stringify(un[1]) === JSON.stringify(['Robe', 'Martin']), 'Stop following sends the list without it, at once', un);
  check(/Its files stay on this computer/.test(text(root())), 'Stop following says the files stay');
  calls.length = 0;
  q('#gdtfAddMfr').value = 'Claypaky'; q('[data-gdtf-follow]').click(); await tick(); await tick();
  const add = calls.find(c => c[0] === 'manufacturers');
  check(add && add[1].slice(-1)[0] === 'Claypaky', 'Follow adds the typed name', add);
  calls.length = 0;
  await show(status());
  q('[data-gdtf-sync]').click(); await tick(); await tick();
  check(calls.some(c => c[0] === 'sync'), 'Sync now acts at once');
  q('[data-gdtf-test]').click(); await tick(); await tick();
  check(calls.some(c => c[0] === 'test' && c[1] === undefined), 'Test login with a saved login tests the saved one');
  check(/GDTF Share accepted the login/.test(text(root())), 'and says what GDTF Share answered');

  // --- 3. syncing -------------------------------------------------------------
  calls.length = 0;
  await show(status({ running: true, state: 'syncing', progress: { done: 412, total: 1505, manufacturer: 'Robe Lighting', manufacturerIndex: 4, manufacturerCount: 12 } }));
  const s3 = text(q('[role="status"]'));
  check(/Syncing/.test(s3) && /File 413 of 1,505 · now Robe Lighting \(manufacturer 4 of 12\)/.test(s3), 'syncing: file n of m and the manufacturer in words', s3);
  const bar = q('[role="progressbar"]');
  check(bar && bar.getAttribute('aria-valuetext') === 'File 413 of 1,505', 'the progress bar says the same words to a screen reader', bar && bar.getAttribute('aria-valuetext'));
  check(/Syncing now/.test(text(root())), 'the manufacturer being synced says "Syncing now"');
  check(q('[data-gdtf-sync]').getAttribute('aria-disabled') === 'true' && /A sync is running/.test(text(root())), 'Sync now refused while one runs, saying why');

  // --- 4. offline -------------------------------------------------------------
  await show(status({ state: 'offline', message: 'Offline: GDTF Share could not be reached: dial tcp: i/o timeout. The library on this computer is unchanged.' }));
  check(/Offline/.test(text(q('[role="status"]'))) && /offline · last synced/.test(text(q('[role="status"]'))) && /used as they are/.test(text(q('[role="status"]'))),
    'offline: "offline · last synced <time>", the files here are used', text(q('[role="status"]')));

  // --- 5. refused on the auto-picked adapter --------------------------------------
  await show(status({ state: 'refused', message: 'the only route to the internet is the lighting network adapter',
    lightingAdapters: [{ protocol: 'Art-Net', bound: true, name: 'Ethernet', addresses: ['192.168.1.40/24'], auto: true, sameAsArtNet: false },
      { protocol: 'sACN', bound: true, name: 'Ethernet', addresses: ['192.168.1.40/24'], auto: false, sameAsArtNet: true }] }));
  const alert = q('[role="alert"]');
  check(alert && /Sync refused: the only route to the internet is the lighting network adapter/.test(text(alert)) && /on purpose, not a fault/.test(text(alert)),
    'refused: a plain warning that this is on purpose', text(alert));
  check(/Settings › 1 Network › Art-Net network adapter/.test(text(alert)) && /instead of “\(default \/ any\)”/.test(text(alert)), 'and what to change');
  check(/picked automatically/.test(text(q('[data-gdtf-route]'))) && /Ethernet \(192\.168\.1\.40\)/.test(text(q('[data-gdtf-route]'))), 'the route box names the auto-picked lighting adapter');
  q('[data-gdtf-goto-nic]').click();
  check(document.activeElement === document.getElementById('nicSelect'), 'the button moves focus to the Art-Net network adapter field');

  // --- 6. partly synced -------------------------------------------------------------
  await show(status({ state: 'partial', message: 'Synced with 1 file(s) still to download; the next sync retries them',
    pending: [{ rid: 1002, manufacturer: 'Robe', fixture: 'Wash (tour edit)', revision: 'tour v2', uploaderLabel: 'User', error: 'GDTF Share download of rid 1002 answered HTTP 503: Internal error.', attempts: 2, since: T }],
    unreadable: 1, unreadableFiles: [{ rid: 1599, manufacturer: 'GLP', fixture: 'JDC2', revision: '1.2b', uploaderLabel: 'User', reason: 'description.xml is not well-formed XML: XML syntax error on line 1: unexpected EOF' }] }));
  const all6 = text(root());
  check(/Partly synced/.test(all6) && /Waiting to download \(1\)/.test(all6) && /answered HTTP 503/.test(all6) && /User file/.test(all6), 'pending downloads with reason and label in words');
  check(/Files that could not be read \(1\)/.test(all6) && /JDC2/.test(all6) && /not well-formed XML/.test(all6), 'unreadable files listed with their reasons');

  // --- 7. off (demo) ---------------------------------------------------------
  await show(status({ enabled: false, state: 'error', message: 'the GDTF library has no folder in this mode',
    lightingAdapters: [{ protocol: 'Art-Net', bound: false, name: '', addresses: [], auto: false, sameAsArtNet: false }, { protocol: 'sACN', bound: false, name: '', addresses: [], auto: false, sameAsArtNet: true }] }));
  check(/Off/.test(text(q('[role="status"]'))) && !q('[data-gdtf-sync]') && /no adapter is bound in this mode/.test(text(q('[data-gdtf-route]'))), 'demo: the library is off, said in words; no adapter named');

  // --- 8. About + credit --------------------------------------------------------
  let about = document.getElementById('aboutRoot');
  check(/Data folder\s*The GDTF library is off in this mode: a demo or rehearsal run has no GDTF folder\./.test(text(about)) && !about.querySelector('[data-gdtf-folder]'),
    'About › Data folder: off in a demo, said in words, no path', text(about));
  await show(status({ libraryFolder: 'C:\\Users\\dom\\AppData\\Roaming\\Benny512\\GDTF' }));
  const folder = about.querySelector('[data-gdtf-folder]');
  check(folder && folder.textContent === 'C:\\Users\\dom\\AppData\\Roaming\\Benny512\\GDTF' && folder.classList.contains('b5-gdtf-path'), 'About › Data folder shows the GDTF library folder, in the selectable style', folder && folder.textContent);
  const before = about.firstChild;
  await show(status({ libraryFolder: 'C:\\Users\\dom\\AppData\\Roaming\\Benny512\\GDTF' }));
  check(about.firstChild === before, 'a status refresh with nothing new does not repaint About (a selection survives)');
  check(/benny512 v0\.3\.5 \(commit abc1234, built 2026-11-18T09:00:00Z\)/.test(text(about)), 'About shows exactly the --version line', text(about));
  const ph = about.querySelector('.b5-gdtf-logo-ph');
  check(/Powered by GDTF/.test(text(about)) && ph && ph.getAttribute('role') === 'img' && /logo goes here/.test(ph.getAttribute('aria-label')) && !ph.querySelector('svg, img'),
    'Powered by GDTF with a labelled placeholder, no drawn logo');
  check(!document.body.innerHTML.includes(PASSWORD), 'the password appears nowhere on the page, in any state');

  if (failures) { console.log(failures + ' failure(s)'); process.exit(1); }
  console.log('all passed');
})().catch(e => { console.error(e); process.exit(1); });
