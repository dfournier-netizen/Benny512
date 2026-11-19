// gdtf_devices_test.js — G3b4: the Device inspector's "GDTF file for this
// device" group and the Devices cards' GDTF tags, run against the LITERAL
// ui.js, gdtf-common.js, gdtf-devices.js and devicedetail.js the browser
// loads (internal/web/gdtf_devices_g3b_test.go), in minidom, over match
// answers in the shape GET /api/gdtf/match gives (gdtf.go, match.go).
//
// WHAT IT PROTECTS (approved G3a mockup scenes 12–16; owner rounds 4, 5)
//  1. found / likely / other-versions / not-mapped / no RDM match / no RDM
//     data / zero ID, each in words; modeBasis and modeWarning word for
//     word; the other modeChoices listed; Manufacturer before User kept.
//  2. Footprint check, option C: on likely / other-versions always
//     "Footprints agree" or "differ"; on found only when they differ.
//  3. No name guessing: with no RDM match the group says so and offers
//     Search by name, whose results say "Found by name" and why RDM could
//     not match them ("the file gives no model ID", "No RDM data").
//  4. The group sits right after "What this device says it is".
//  5. Tags in words on every card, from a per-identity cache: identical
//     devices cost one request, requests go one at a time, and a library
//     change drops the cache.
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { makeWindow, Event } = require('./minidom.js');
const JS = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + JSON.stringify(detail) : ''));
}

const cand = o => Object.assign({ rid: 1, fixture: 'Wash', manufacturer: 'Robe', revision: 'DMX v1.3', version: '1.2', lastModified: 1790000000, uploader: 'Manuf.',
  uploaderLabel: 'Manufacturer', sha256: 'x', matchedOn: 'software-version', description: null, dmxMode: 'Mode 1', modeState: 'found', modeInFile: true,
  modeChoices: [], modeBasis: '', modeWarning: '', modeFromVersions: [], note: '', inFixtureLibrary: false,
  modes: [{ name: 'Mode 1', dmxfootprint: 20 }, { name: 'Mode 2', dmxfootprint: 26 }] }, o);
const ANSWERS = {
  // found, footprints agree (device 20)
  '8738:1:16777216:1': [cand({}), cand({ rid: 2, fixture: 'Wash (tour edit)', uploaderLabel: 'User', revision: 'tour v2' })],
  // found, footprints differ (device 20, mode 22)
  '21360:1:16777216:1': [cand({ rid: 3, fixture: 'Color Force II 48', manufacturer: 'Chroma-Q', dmxMode: 'Std', modes: [{ name: 'Std', dmxfootprint: 22 }],
    modeBasis: 'software version 0x01000000 maps personality 1 to Std' })],
  // likely, footprints agree (device 24)
  '43690:1:16777216:1': [cand({ rid: 4, fixture: 'Spot', manufacturer: 'Ayrton', matchedOn: 'model', dmxMode: 'Standard', modeState: 'likely',
    modeChoices: ['Standard', 'Standard 16-bit'], modeBasis: 'likely: 2 of 3 software version(s) that map personality 1 map it to Standard',
    modeWarning: "This file's software versions disagree on personality 1 (Standard, Standard 16-bit); Standard is the most likely, not a certain answer",
    modes: [{ name: 'Standard', dmxfootprint: 24 }, { name: 'Standard 16-bit', dmxfootprint: 28 }] })],
  // other versions, footprints differ (device 16, mode 18)
  '19792:1:16777216:1': [cand({ rid: 5, fixture: 'Beam', manufacturer: 'Martin Professional', dmxMode: 'Mode A', modeState: 'other-versions',
    modeChoices: ['Mode A'], modeBasis: '2 of 2 other software version(s) that map personality 1 map it to Mode A',
    modeWarning: "Software version 0x01000000 (the device's) maps no mode for personality 1; Mode A comes from software version(s) 0x00080000, 0x00090000 of this file, not from 0x01000000, so it is a guess",
    modes: [{ name: 'Mode A', dmxfootprint: 18 }, { name: 'Mode B', dmxfootprint: 22 }] })],
  // no RDM match
  '17740:1:16777216:1': [],
};
const NAME_HITS = [
  { rid: 1401, fixture: 'Par', manufacturer: 'Elation', revision: 'DMX v2', uploaderLabel: 'Manufacturer', modes: [{ name: '8ch', dmxfootprint: 8 }, { name: '4ch', dmxfootprint: 4 }],
    description: { state: 'read', rdm: { manufacturerId: 0x454C, manufacturerIdText: '0x454C', deviceModelId: null, deviceModelIdText: '0x0' } }, inFixtureLibrary: false },
  { rid: 1402, fixture: 'Par (show set)', manufacturer: 'Elation', revision: 'Rev 2', uploaderLabel: 'User', modes: [{ name: '8ch', dmxfootprint: 8 }],
    description: { state: 'read', rdm: null }, inFixtureLibrary: false },
];

const { window, document } = makeWindow();
const calls = [];
let inflight = 0, maxInflight = 0;
const ctx = vm.createContext({
  window, document, console, setTimeout, clearTimeout, CustomEvent: makeWindow().CustomEvent || class { constructor(t) { this.type = t; } },
  CSS: { escape: s => s },
  Live: { on: () => {} },
  Api: {
    gdtfMatch: async (mid, model, sw, pers) => {
      calls.push(['match', mid, model, sw, pers]);
      inflight++; maxInflight = Math.max(maxInflight, inflight);
      await new Promise(r => setTimeout(r, 5));
      inflight--;
      const key = [mid, model, sw, pers || 0].join(':');
      if (!(key in ANSWERS)) { const e = new Error('the GDTF library has no folder in this mode'); e.status = 503; throw e; }
      return { count: ANSWERS[key].length, candidates: JSON.parse(JSON.stringify(ANSWERS[key])), notYetRead: 0, unreadable: 1, note: '' };
    },
    gdtfSearch: async (q, where, limit) => { calls.push(['search', q, where]); return { count: NAME_HITS.length, results: JSON.parse(JSON.stringify(NAME_HITS)) }; },
    gdtfStatus: async () => ({ enabled: true, files: 18 }),
    getParam: async () => ({ value: null }), getDeviceParam: async () => ({}),
    formatAddressRange: (a, f) => `${a}-${a + f - 1}`,
  },
});
window.CustomEvent = ctx.CustomEvent;
ctx.escapeHtml = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const load = f => vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
for (const f of ['ui.js', 'gdtf-common.js', 'gdtf-devices.js', 'devicedetail.js']) load(f);
const GdtfDevices = vm.runInContext('GdtfDevices', ctx);
const DeviceDetail = vm.runInContext('DeviceDetail', ctx);
DeviceDetail.init(() => {});
const wait = ms => new Promise(r => setTimeout(r, ms));
const text = el => (el ? el.textContent : '').replace(/\s+/g, ' ').trim();

const dev = (uid, mid, foot, o) => ({ f: Object.assign({ uid, manufacturerId: mid, manufacturerName: 'X', nodeIp: '2.0.0.31', bindIndex: 1, portAddress: 0, deviceModelId: 1, hasDeviceInfo: true, softwareVersionId: 0x01000000, currentPersonality: 1, dmxFootprint: foot }, o || {}),
  di: { DeviceModelID: 1, SoftwareVersionID: 0x01000000, CurrentPersonality: 1, PersonalityCount: 2, DMXFootprint: foot, DMXStartAddress: 1, SubDeviceCount: 0, SensorCount: 0 } });

async function info(d, modelName) {
  DeviceDetail._caches.infoCache[d.f.uid] = { deviceInfo: d.di, modelVal: modelName || '', mfrLabelVal: '', swVersion: '1.0', loading: false };
  const box = document.createElement('div'); document.body.appendChild(box);
  DeviceDetail.renderInfoSection(box, d.f); await wait(40); DeviceDetail.renderInfoSection(box, d.f);
  return box;
}

(async () => {
  await GdtfDevices.noteFiles();
  // 1. found, footprints agree: no footprint line; the user file listed after.
  let box = await info(dev('2222:00000001', 0x2222, 20), 'Wash');
  const heads = [...box.querySelectorAll('h4')].map(h => h.id);
  check(heads.indexOf('ddGdtfHead') === heads.indexOf('ddIdentityHead') + 1, 'the GDTF group comes right after "What this device says it is"', heads);
  let g = box.querySelector('[data-gdtf-device]');
  check(/manufacturer 0x2222, model 0x0001, software version 0x01000000, personality 1\. Nothing is sent to the device\./.test(text(g)), 'it names the RDM IDs it asked with', text(g));
  check(/Mode found/.test(text(g)) && /Personality 1 is Mode 1 · 20 channels/.test(text(g)) && /Matched on\s*manufacturer ID, model ID and this device’s software version/.test(text(g)), 'found: mode in words, how it matched');
  check(!/Footprints/.test(text(g.querySelector('[data-gdtf-candidate]'))), 'found with footprints that agree shows no footprint line');
  check(/1 more file matches/.test(text(g)) && /Wash \(tour edit\) User file/.test(text(g)), 'the user upload is listed after the manufacturer\'s file');
  check(/Not in your Fixture library yet/.test(text(g)) && !!g.querySelector('[data-gdtf-add-rid="1"]'), 'Add to Fixture library… when it is not there yet');

  // 2. found, footprints differ: a warning.
  box = await info(dev('5370:00000001', 0x5370, 20)); g = box.querySelector('[data-gdtf-device]');
  check(/Footprints differ/.test(text(g)) && /the device reports 20 slots; this mode has 22 channels\. Check the mode on the fixture's own menu\./.test(text(g)), 'found: footprints that differ are a warning', text(g));
  check(/software version 0x01000000 maps personality 1 to Std/.test(text(g)), 'modeBasis shown word for word on found');

  // 3. likely, footprints agree: always shown.
  box = await info(dev('AAAA:00000003', 0xAAAA, 24)); g = box.querySelector('[data-gdtf-device]');
  check(/Likely mode — not certain/.test(text(g)) && /Personality 1 is most likely Standard · 24 channels/.test(text(g)), 'likely: in words, not certain');
  check(text(g).includes("This file's software versions disagree on personality 1 (Standard, Standard 16-bit); Standard is the most likely, not a certain answer"), 'modeWarning word for word');
  check(text(g).includes('Why Standard: likely: 2 of 3 software version(s) that map personality 1 map it to Standard'), 'modeBasis word for word');
  check(/Other mode this personality maps to in this file\s*Standard 16-bit · 28 channels/.test(text(g)), 'the other choice is listed', text(g));
  check(/Footprints agree/.test(text(g)) && /Matched on\s*manufacturer ID and model ID only/.test(text(g)) && /This file does not list software version 0x01000000/.test(text(g)), 'likely: "Footprints agree" always shown; model-only said');

  // 4. other versions, footprints differ.
  box = await info(dev('4D50:00000004', 0x4D50, 16)); g = box.querySelector('[data-gdtf-device]');
  check(/Mode from other versions — not certain/.test(text(g)) && text(g).includes("Mode A comes from software version(s) 0x00080000, 0x00090000 of this file, not from 0x01000000, so it is a guess"), 'other-versions: warning names the versions, word for word');
  check(/Footprints differ/.test(text(g)) && /reports 16 slots; this mode has 18 channels/.test(text(g)), 'other-versions: footprint check shown');

  // 5. no RDM match -> name search, never a guess.
  calls.length = 0;
  box = await info(dev('454C:00000005', 0x454C, 8), 'Par'); g = box.querySelector('[data-gdtf-device]');
  check(/No GDTF file in your library has this device's RDM IDs/.test(text(g)) && /None of the 18 files lists manufacturer 0x454C with model 0x0001\. Benny512 does not pick a file by its name on its own\./.test(text(g)) && /1 file could not be read, so it was not checked/.test(text(g)),
    'no RDM match: says so, no name guessing, and what was not checked', text(g));
  check(g.querySelector('#ddNameSearch') && g.querySelector('#ddNameSearch').value === 'Par' && !calls.some(c => c[0] === 'search'), 'Search by name is offered, filled from the model name, and not run on its own');
  g.querySelector('[data-gdtf-name-search]').click(); await wait(20);
  g = box.querySelector('[data-gdtf-device]');
  check(calls.some(c => c[0] === 'search' && c[1] === 'Par' && c[2] === 'library'), 'Search by name searches the library');
  check(/2 files match “Par” by name\. Found by name, not by RDM\./.test(text(g)) && (text(g).match(/Found by name/g) || []).length >= 3, 'results say Found by name', text(g));
  check(/the file gives no model ID \(it says 0x0\), so it cannot match a device by RDM/.test(text(g)) && /No RDM data in this file\. It can only be found by name\./.test(text(g)), 'each says why RDM could not match it');
  check(/this file has a mode of 8 channels named 8ch\. Not confirmed\./.test(text(g)), 'the device\'s footprint is compared, not confirmed');

  // 6. no RDM data / zero ID.
  const nd = dev('6C74:00000006', 0x6C74, 20); nd.di = null;
  DeviceDetail._caches.infoCache[nd.f.uid] = { deviceInfo: null, loading: false, modelVal: '' };
  box = document.createElement('div'); DeviceDetail.renderInfoSection(box, nd.f); g = box.querySelector('[data-gdtf-device]');
  check(/No RDM data from this device yet/.test(text(g)) && !!g.querySelector('#ddNameSearch'), 'no RDM data: says so, offers the name search');
  const z = dev('1900:00000002', 0x1900, 0); z.di.DeviceModelID = 0;
  box = await info(z); g = box.querySelector('[data-gdtf-device]');
  check(/This device reports model ID 0x0000/.test(text(g)) && /an ID of 0 means "not given"/.test(text(g)), 'zero model ID: says 0 is "not given"');

  // 7. card tags, cache and queue.
  calls.length = 0; maxInflight = 0;
  window.dispatchEvent(new Event('b5-gdtf-library-changed'));
  const cards = [dev('2222:00000001', 0x2222, 20), dev('2222:00000002', 0x2222, 20), dev('AAAA:00000003', 0xAAAA, 24), dev('454C:00000005', 0x454C, 8), dev('7777:00000009', 0x7777, 8)];
  const list = document.createElement('div'); document.body.appendChild(list);
  list.innerHTML = cards.map(c => GdtfDevices.tagHtml(c.f)).join('') + GdtfDevices.tagHtml(Object.assign({}, cards[0].f, { uid: 'x', hasDeviceInfo: false })) + GdtfDevices.tagHtml(Object.assign({}, cards[0].f, { uid: 'y', deviceModelId: 0 }));
  check(/GDTF: checking…/.test(text(list)), 'a tag says "checking…" before the answer');
  await wait(120);
  const tags = [...list.querySelectorAll('[data-gdtf-tag]')].map(t => t.textContent);
  check(JSON.stringify(tags) === JSON.stringify(['GDTF: found', 'GDTF: found', 'GDTF: likely', 'GDTF: none', 'GDTF: library off', 'GDTF: no RDM data yet', 'GDTF: no model ID']), 'tags in words follow the match API', tags);
  check(calls.filter(c => c[0] === 'match').length === 4, 'two identical devices cost one request (4 identities, 4 requests)', calls.length);
  check(maxInflight === 1, 'lookups go one at a time', maxInflight);
  calls.length = 0;
  list.innerHTML = cards.map(c => GdtfDevices.tagHtml(c.f)).join('');
  await wait(30);
  check(calls.length === 0, 'a re-render asks nothing again (cached)');
  window.dispatchEvent(new Event('b5-gdtf-library-changed'));
  list.innerHTML = GdtfDevices.tagHtml(cards[0].f);
  await wait(30);
  check(calls.length === 1, 'a library change drops the cache', calls.length);

  if (failures) { console.log(failures + ' failure(s)'); process.exit(1); }
  console.log('all passed');
})().catch(e => { console.error(e); process.exit(1); });
