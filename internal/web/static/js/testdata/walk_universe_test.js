// walk_universe_test.js — the Rig Walk screen's universe notation and its
// identify-off safety beacon, run against the LITERAL walk.js and ui.js the
// browser loads.
//
// WHY THIS FILE EXISTS
//
// Rig Walk was carrying the same defect class that has now been caught eight
// times on this project — one value spelled two ways either side of a
// boundary — in THREE places at once, and every one of them was a universe
// printed or accepted as a raw Art-Net Port-Address while every other screen
// in the app showed the owner's display base:
//
//  (a) THE SCOPE PICKER, the known open item. "One universe (Port-Address)"
//      was a bare <input min=0 max=32767 value=0> whose value went straight
//      into POST /api/walk/start's scopeValue. internal/web/walk.go's
//      walkCandidates parses that as a RAW Port-Address (f.Port.RawValue()
//      == u). So at the owner's default base of 1: the box opened on "0" for
//      a universe Patch calls 1, and typing the number he reads off Patch
//      ("3") walked wire universe 3 — the one Patch calls 4. The universe he
//      asked for and the universe he walked were one apart, silently, on the
//      screen whose entire job is lining a patch up against a real truss.
//
//  (b) THE PORT PICKER labelled each option "(addr 12)" from
//      port.outputAddress — the same raw Port-Address, sitting next to a
//      Nodes screen that has shown the display base since its own
//      conversion.
//
//  (c) THE ACTIVE WALK CARD printed "U${dev.portAddress}" in its kicker and
//      "port-addr ${dev.portAddress}" in its footer. That is the card he
//      reads standing under the fixture.
//
// A test that only checked "canonical 0 shows as 1" would not have caught any
// of these, because none of them called UI.formatUniverse at all. So every
// assertion below reads the MARKUP the screen actually produced, or the
// ARGUMENTS it actually put on the wire — never a helper in isolation.
//
// It also pins the one piece of Rig Walk that is a safety property rather
// than a display one: leaving the screen with a walk running must fire the
// identify-off beacon, or a fixture is left flashing on a truss.
//
// Run: node walk_universe_test.js   (exit 0 = pass)
//
// NOTE FOR WHOEVER PICKS THIS UP: unlike the other five suites in this
// directory there is no internal/web/walk_universe_test.go wrapper yet, so
// `go test ./...` does not run this file. Adding that three-line wrapper was
// outside the file-ownership boundary of the change that wrote this test.

'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

// ---- DOM stub -----------------------------------------------------------
// Deliberately dumb, same shape as patch_lifecycle_test.js's: innerHTML is a
// recorded sink, ids are memoised, and handlers are replaced per type so a
// control fires once per render rather than once per render that ever
// happened.

function makeEl(id) {
  const el = {
    id: id || '', tagName: 'DIV', _innerHTML: '', textContent: '', className: '',
    value: '', checked: false, disabled: false, readOnly: false, type: '',
    style: {}, dataset: {}, children: [], handlers: {}, scrollTop: 0,
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    addEventListener(t, fn) { this.handlers[t] = [fn]; },
    removeEventListener() {},
    appendChild(c) { this.children.push(c); return c; },
    removeChild() {}, remove() {},
    setAttribute() {}, removeAttribute() {}, getAttribute: () => null,
    focus() {}, blur() {}, click() {}, setSelectionRange() {}, scrollIntoView() {},
    querySelector(sel) { return doc.querySelector(sel); },
    querySelectorAll(sel) { return doc.querySelectorAll(sel); },
    contains: () => true, closest: () => null,
    fire(t) {
      const ev = { target: this, currentTarget: this, preventDefault() {}, stopPropagation() {} };
      (this.handlers[t] || []).forEach(fn => fn(ev));
    },
  };
  Object.defineProperty(el, 'innerHTML', { get: () => el._innerHTML, set: v => { el._innerHTML = String(v); } });
  return el;
}

const byId = new Map();
const doc = {
  handlers: {},
  visibilityState: 'visible',
  getElementById(id) {
    if (!byId.has(id)) byId.set(id, makeEl(id));
    return byId.get(id);
  },
  querySelector(sel) {
    const m = /^#([A-Za-z][\w-]*)$/.exec(String(sel).trim());
    return this.getElementById(m ? m[1] : 'sel:' + sel);
  },
  querySelectorAll() { return []; },
  createElement(t) { const e = makeEl(''); e.tagName = t; return e; },
  addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
  removeEventListener() {},
};
doc.body = makeEl('body');
doc.documentElement = makeEl('html');

function storageStub() {
  const m = new Map();
  return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) };
}

// ---- recording Api ------------------------------------------------------

const calls = [];
let walkActive = false;

// The device the walk is sitting on. portAddress 17 is deliberate: it is
// Net 0 / Sub-Net 1 / Universe 1, so its low nibble (1) and its
// Port-Address (17) are DIFFERENT numbers. A screen printing the nibble, the
// raw address or the formatted address all produce three different strings
// here, which is what makes the assertion able to tell them apart.
const DEVICE = {
  uid: '2222:00000001', model: 'Robe T1 Profile', manufacturer: 'Robe',
  dmxStartAddress: 100, dmxFootprint: 40, addressKnown: true,
  nodeIp: '10.0.0.9', bindIndex: 1, portAddress: 17,
  status: '', note: '', identifyOn: true, identifyErr: '',
};

const NODES = [{
  ip: '10.0.0.9', bindIndex: 1, shortName: 'EN4', longName: 'Obsidian EN4',
  ports: [{ index: 0, outputAddress: 17, inputAddress: 0 }],
}];

const apiImpl = {
  getWalkSession: async () => {
    calls.push({ name: 'getWalkSession' });
    return walkActive
      ? { active: true, session: { devices: [DEVICE], current: 0, autoAdvance: false }, summary: { total: 1, confirmed: 0, problems: 0, remaining: 1 } }
      : { active: false, session: null, summary: null };
  },
  getNodes: async () => { calls.push({ name: 'getNodes' }); return NODES; },
  startWalk: async (body) => { calls.push({ name: 'startWalk', args: body }); walkActive = true; return {}; },
  walkIdentifyOffBeacon: () => { calls.push({ name: 'walkIdentifyOffBeacon' }); },
  formatAddressRange: (start, footprint, known) => {
    if (!known) return '—';
    if (!footprint) return String(start);
    return `${start} (${start}-${start + footprint - 1})`;
  },
  walkExportUrl: (f) => '/api/walk/export?format=' + f,
};
const Api = new Proxy(apiImpl, {
  get(t, k) {
    if (k in t) return t[k];
    return async (...args) => { calls.push({ name: String(k), args }); return {}; };
  },
});

// DeviceDetail is a whole other screen's module; Rig Walk only needs it to
// exist and to be told which device is selected.
const DeviceDetail = {
  init() {}, select() {}, deselect() {},
  renderInfoSection() {}, renderParamsSection() {}, renderSensorsSection() {}, renderStatusSection() {},
};

const win = {
  handlers: {},
  addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
  removeEventListener() {},
  dispatchEvent(ev) { (this.handlers[ev.type] || []).forEach(fn => fn(ev)); return true; },
  open() {},
};
class CustomEventStub {
  constructor(type, init) { this.type = type; this.detail = (init && init.detail) || null; }
}

const ctx = vm.createContext({
  window: win, document: doc, console,
  sessionStorage: storageStub(), localStorage: storageStub(),
  CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Api, DeviceDetail,
  confirm: () => true, prompt: () => null,
  escapeHtml: (s) => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'),
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'walk.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}
const Walk = vm.runInContext('WalkScreen', ctx, { filename: 'grab' });
const UI = vm.runInContext('UI', ctx, { filename: 'grab' });

const tick = () => new Promise(r => setTimeout(r, 0));
async function settle(n) { for (let i = 0; i < (n || 20); i++) await tick(); }

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

const root = () => doc.getElementById('walkRoot').innerHTML;
function el(id) { return doc.getElementById(id); }

async function main() {
  Walk.init();

  // ------------------------------------------------------------------
  console.log('1. DEFECT (a): the scope picker labels the DISPLAY base, not the raw Port-Address');
  UI.setUniverseBase(1);
  walkActive = false;
  Walk.onEnterScreen();
  await settle();

  el('walkScopeKind').value = 'universe';
  el('walkScopeKind').fire('change');
  await settle();

  // The DOM stub does not evaluate a value="" attribute into a .value
  // property (innerHTML is a recorded sink, not a parser), so the box's
  // opening value is read out of the markup the screen produced — which is
  // what the browser would parse.
  let uniInput = el('walkScopeUniverse');
  const scopeMarkup = doc.getElementById('walkScopeValueWrap').innerHTML;
  check(/id="walkScopeUniverse"[^>]*value="1"/.test(scopeMarkup),
    'the universe box opens on 1 at base 1 — the number Patch shows for the first universe',
    'markup was: ' + scopeMarkup.replace(/\s+/g, ' ').slice(0, 300) +
    ' — value="0" means the raw Port-Address is still being labelled');

  check(!/Port-Address\)?\s*<\/label>/.test(scopeMarkup) && /industry standard, 1-based|Art-Net native, 0-based/.test(scopeMarkup),
    'the field label names the active notation instead of saying "(Port-Address)"',
    'label markup: ' + scopeMarkup.slice(0, 240));

  // ------------------------------------------------------------------
  console.log('2. DEFECT (a), the dangerous half: what goes ON THE WIRE stays canonical');
  // Type the number he reads off Patch for wire universe 3.
  uniInput.value = '4';
  uniInput.fire('input');
  await settle();
  el('walkStartBtn').fire('click');
  await settle();
  const start = calls.filter(c => c.name === 'startWalk').pop();
  check(!!start && start.args.scopeValue === '3',
    'typing universe 4 at base 1 starts a walk on wire Port-Address 3',
    'scopeValue was ' + JSON.stringify(start && start.args.scopeValue) +
    ' — "4" means the displayed number was sent raw and the walk is one universe off');
  check(start.args.scopeKind === 'universe', 'the scope kind is still sent unchanged');

  // ------------------------------------------------------------------
  console.log('3. DEFECT (c): the active walk card shows the display universe, not the raw one');
  await settle();
  let card = root();
  // portAddress 17, base 1 -> "Universe 18". The three wrong answers are 17
  // (raw), 1 (nibble) and 2 (formatted nibble), and none of them can produce
  // this string.
  check(/Universe 18/.test(card),
    'the card reads "Universe 18" for Port-Address 17 at base 1',
    'card markup did not contain it: ' + card.replace(/\s+/g, ' ').slice(0, 400));
  check(!/U17\b/.test(card) && !/port-addr 17\b/.test(card),
    'the raw Port-Address is never printed as though it were the universe',
    'found a bare raw 17 in: ' + card.replace(/\s+/g, ' ').slice(0, 400));

  // ------------------------------------------------------------------
  console.log('4. a display-base change re-renders the open card AND the staged scope box');
  UI.setUniverseBase(0);
  await settle();
  card = root();
  check(/Universe 17/.test(card),
    'the same card re-reads "Universe 17" at base 0 without a refetch',
    'card markup: ' + card.replace(/\s+/g, ' ').slice(0, 400));

  // ------------------------------------------------------------------
  console.log('5. DEFECT (b): the port picker names universes, not raw addresses');
  UI.setUniverseBase(1);
  walkActive = false;
  Walk.onEnterScreen();
  await settle();
  el('walkScopeKind').value = 'port';
  el('walkScopeKind').fire('change');
  await settle();
  const portMarkup = doc.getElementById('walkScopeValueWrap').innerHTML;
  check(/universe 18/i.test(portMarkup),
    'the port option reads "universe 18" for outputAddress 17 at base 1',
    'port markup: ' + portMarkup.replace(/\s+/g, ' ').slice(0, 300));
  check(!/addr 17\b/.test(portMarkup),
    'the raw output Port-Address is not printed as "addr 17"',
    'port markup: ' + portMarkup.replace(/\s+/g, ' ').slice(0, 300));

  // ------------------------------------------------------------------
  console.log('6. the staged scope box survives a base change without being re-parsed');
  el('walkScopeKind').value = 'universe';
  el('walkScopeKind').fire('change');
  await settle();
  uniInput = el('walkScopeUniverse');
  uniInput.value = '9';           // base 1 -> canonical 8
  uniInput.fire('input');
  UI.setUniverseBase(0);
  await settle();
  const reMarkup = doc.getElementById('walkScopeValueWrap').innerHTML;
  check(/id="walkScopeUniverse"[^>]*value="8"/.test(reMarkup),
    'a box holding display 9 at base 1 is rewritten to 8 at base 0 — the same wire universe',
    'markup was: ' + reMarkup.replace(/\s+/g, ' ').slice(0, 300) +
    ' — value="9" means the base change silently moved the wire value');

  // ------------------------------------------------------------------
  console.log('7. the identify-off safety beacon still fires when the screen is left');
  UI.setUniverseBase(1);
  walkActive = true;
  Walk.onEnterScreen();
  await settle();
  calls.length = 0;
  Walk.onLeaveScreen();
  check(calls.some(c => c.name === 'walkIdentifyOffBeacon'),
    'leaving Rig Walk with a walk running sends identify-off',
    'calls were: ' + JSON.stringify(calls.map(c => c.name)));

  calls.length = 0;
  doc.visibilityState = 'hidden';
  (doc.handlers.visibilitychange || []).forEach(fn => fn({}));
  check(calls.some(c => c.name === 'walkIdentifyOffBeacon'),
    'backgrounding the tab with a walk running sends identify-off',
    'calls were: ' + JSON.stringify(calls.map(c => c.name)));

  console.log(failures ? `\n${failures} assertion(s) failed.` : '\nall assertions passed');
  process.exit(failures ? 1 : 0);
}

main().catch(e => { console.error(e); process.exit(1); });
