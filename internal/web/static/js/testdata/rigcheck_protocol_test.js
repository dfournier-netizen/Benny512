// rigcheck_protocol_test.js — the browser half of selectable sACN output,
// exercised against the LITERAL ui.js / rigcheck.js / patch.js the browser
// loads, run under plain Node by internal/web/sacn_ui_test.go. Zero
// dependencies, same as the rest of this project.
//
// WHAT IT PROTECTS
//
//  1. THE NUMBERING HELPERS. Art-Net Port-Address -> sACN universe is the
//     one piece of arithmetic that can silently light the wrong universe on
//     a real rig, and sACN has no universe 0 while Art-Net Port-Address 0 is
//     this app's default show universe 1. ui.js must REFUSE (null), never
//     clamp, exactly as sacn.ArtnetPortAddressToSACNUniverse does. The
//     multicast addresses are literal strings transcribed from ANSI
//     E1.31-2025 Table 9-10, never recomputed here from the universe number
//     — a symmetric-decode check would pass against a wrong formula.
//
//  2. APPLY-TO-CONFIRM. Pressing "sACN" must send NOTHING. The protocol is
//     what the next run puts on the wire, so it goes through the project's
//     standing Apply-to-confirm contract like every other wire-affecting
//     control (the signed-off exceptions — Identify, the Send/Rig Check
//     faders, the Function-check test toggles — are all things that are
//     already live and adjusted by feel; a protocol is not).
//
//  3. THE 422. `sACN universe must be 1..63999: ...` NAMES the offending
//     universe. Swallowing it into "couldn't start" throws away the only
//     part of the message that tells a tech what to change, so the server's
//     own words must reach the screen verbatim.
//
//  4. SERVER TRUTH WINS. A refused start must snap the control back to the
//     protocol the SERVER reports, not leave the browser's guess showing —
//     the same discipline rigcheck.js's scope echo already follows.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

// ---- DOM stub (same shape as patch_lifecycle_test.js's) -----------------

function makeEl(id) {
  const el = {
    id: id || '', _innerHTML: '', textContent: '', value: '', checked: false,
    disabled: false, readOnly: false, type: '', className: '', style: {}, dataset: {},
    children: [], parentElement: null, selectionStart: 0, handlers: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    addEventListener(type, fn) { this.handlers[type] = [fn]; },
    removeEventListener() {},
    appendChild(c) { this.children.push(c); return c; },
    removeChild() {}, remove() {}, setAttribute() {}, getAttribute() { return null; },
    focus() {}, blur() {}, click() {}, setSelectionRange() {}, scrollIntoView() {},
    querySelector(sel) { return doc.querySelector(sel); },
    querySelectorAll(sel) { return doc.querySelectorAll(sel); },
    contains() { return true; },
    fire(type) {
      const ev = { target: this, currentTarget: this, isConnected: true, preventDefault() {}, stopPropagation() {} };
      (this.handlers[type] || []).forEach(fn => fn(ev));
    },
  };
  Object.defineProperty(el, 'innerHTML', { get: () => el._innerHTML, set: v => { el._innerHTML = String(v); } });
  return el;
}

function selectorNodes(sel) {
  switch (sel) {
    case '#patchViewTabs .detail-tab-btn':
      return ['entries', 'reconcile', 'rigcheck'].map(v => { const b = makeEl('tab-' + v); b.dataset.view = v; return b; });
    case '[data-rc-protocol]':
      return ['artnet', 'sacn'].map(v => { const b = makeEl('proto-' + v); b.dataset.rcProtocol = v; return b; });
    default:
      return [];
  }
}

const byId = new Map();
const doc = {
  handlers: {}, visibilityState: 'visible', lastQuery: new Map(),
  getElementById(id) {
    if (!byId.has(id)) byId.set(id, makeEl(id));
    return byId.get(id);
  },
  querySelector(sel) {
    const m = /^#([A-Za-z][\w-]*)$/.exec(String(sel).trim());
    return this.getElementById(m ? m[1] : 'sel:' + sel);
  },
  querySelectorAll(sel) {
    const nodes = selectorNodes(sel);
    if (nodes.length) this.lastQuery.set(sel, nodes);
    return nodes;
  },
  createElement(tagName) { const e = makeEl(''); e.tagName = tagName; return e; },
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
  removeEventListener() {},
};
doc.body = makeEl('body');
doc.documentElement = makeEl('html');

function live(sel, pred) {
  const nodes = doc.lastQuery.get(sel) || [];
  const n = pred ? nodes.find(pred) : nodes[0];
  if (!n) throw new Error('no live node for selector ' + sel);
  return n;
}

function storageStub() {
  const m = new Map();
  return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) };
}

// ---- recording Api -------------------------------------------------------

const calls = [];
function record(name, args) { calls.push({ name, args }); }
function callNames() { return calls.map(c => c.name); }
function writes() { return calls.filter(c => c.name !== 'getRigCheckState' && c.name !== 'getPattern' && c.name !== 'getPatch' && c.name !== 'getPatchCollisions' && c.name !== 'getSACNConfig'); }

// serverState mirrors what internal/web actually answers with: `protocol` is
// ALWAYS present (patch.go's rigCheckStateJSON), and it is the protocol the
// LAST accepted Start selected.
let serverState = { running: false, level: 255, entryIndex: 0, entryCount: 0, channelOffset: 0, protocol: 'artnet' };

// THE SERVER'S OWN 422. Transcribed from sacn.ErrInvalidUniverse +
// ArtnetPortAddressToSACNUniverse's wrapping, which is what
// writeRigCheckError puts in the body. It NAMES the universe.
const SERVER_422 = 'sACN universe must be 1..63999: show universe -99 (Art-Net Port-Address 0) would map to sACN universe -99, with an sACN start universe of 1 and an Art-Net start universe of 100';
let rejectStart = false;

function patchDoc() {
  return {
    active: true,
    patch: {
      schemaVersion: 4, name: 'Protocol Test', entries: [
        { id: 'e1', name: 'Wash L', fixtureType: 'Robe Wash', universe: 0, startAddress: 1, footprint: 20, position: 'SL Boom' },
        { id: 'e2', name: 'Par 1', fixtureType: 'Elation Par', universe: 1, startAddress: 90, footprint: 8, position: 'US Truss 2' },
      ],
    },
  };
}

const apiImpl = {
  getPatch: async () => { record('getPatch'); return patchDoc(); },
  getPatchCollisions: async () => { record('getPatchCollisions'); return []; },
  getRigCheckState: async () => { record('getRigCheckState'); return JSON.parse(JSON.stringify(serverState)); },
  getSACNConfig: async () => { record('getSACNConfig'); return { startUniverse: 1, priority: 100, unicastTo: '' }; },
  rigCheckStart: async (body) => {
    record('rigCheckStart', JSON.parse(JSON.stringify(body || {})));
    if (rejectStart) throw new Error(SERVER_422);
    serverState = Object.assign({}, serverState, { running: true, entryCount: 2, protocol: body.protocol || 'artnet' });
    return JSON.parse(JSON.stringify(serverState));
  },
  rigCheckStop: async () => {
    record('rigCheckStop');
    serverState = Object.assign({}, serverState, { running: false });
    return JSON.parse(JSON.stringify(serverState));
  },
  rigCheckStopBeacon: () => { record('rigCheckStopBeacon'); },
  getPattern: async () => {
    record('getPattern');
    return {
      running: false, outputEnabled: false, selectedCount: 0, totalScope: 2, elapsedMs: 0,
      tests: [], available: [], contested: [], scopeKind: 'all',
      baseState: { isolate: false, defaultsUnknownCount: 0, shutterUnknownEntries: [] }, lastEndReason: '',
    };
  },
  formatAddressRange: (start, footprint) => String(start) + '-' + String(start + (footprint || 1) - 1),
  patchExportUrl: (f) => '/api/patch/export?format=' + f,
  patchReconcileExportUrl: (f) => '/api/patch/reconcile/export?format=' + f,
  identify: async () => ({}),
};
const Api = new Proxy(apiImpl, {
  get(t, k) {
    if (k in t) return t[k];
    return async (...args) => { record(String(k), args); return {}; };
  },
});

// ---- context -------------------------------------------------------------

const win = {
  handlers: {},
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
  removeEventListener() {},
  dispatchEvent(ev) { (this.handlers[ev.type] || []).forEach(fn => fn(ev)); return true; },
  open() {},
};
class CustomEventStub { constructor(type, init) { this.type = type; this.detail = (init && init.detail) || null; } }

let confirmed = true;
const confirmPrompts = [];

const ctx = vm.createContext({
  window: win, document: doc, console,
  sessionStorage: storageStub(), localStorage: storageStub(),
  CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Api,
  confirm: (msg) => { confirmPrompts.push(String(msg)); return confirmed; },
  prompt: () => null,
  escapeHtml: (s) => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'),
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'rigcheck.js', 'reconcile.js', 'patch.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}
const UI = vm.runInContext('UI', ctx);

const tick = () => new Promise(r => setTimeout(r, 0));
async function settle(n) { for (let i = 0; i < (n || 30); i++) await tick(); }

function rigCheckMarkup() { return doc.getElementById('rcSubBody')._innerHTML; }
function clickTab(view) { live('#patchViewTabs .detail-tab-btn', b => b.dataset.view === view).fire('click'); }
function pressProtocol(id) { live('[data-rc-protocol]', b => b.dataset.rcProtocol === id).fire('click'); }

// ---- 1. the numbering helpers -------------------------------------------

function numberingChecks() {
  console.log('1. ui.js sACN numbering helpers');
  check(typeof UI.artnetToSacn === 'function', 'UI.artnetToSacn exists');
  check(typeof UI.formatSacn === 'function', 'UI.formatSacn exists');
  check(typeof UI.sacnMulticastAddress === 'function', 'UI.sacnMulticastAddress exists');
  if (typeof UI.artnetToSacn !== 'function') return;

  UI.setArtnetStart(0);
  UI.setSacnStart(1);
  check(UI.artnetToSacn(0) === 1, 'Art-Net Port-Address 0 (show universe 1) is sACN universe 1 at sACN start 1',
    'got ' + UI.artnetToSacn(0));
  check(UI.artnetToSacn(4) === 5, 'Art-Net 4 (show universe 5) is sACN universe 5', 'got ' + UI.artnetToSacn(4));
  check(UI.formatSacn(0) === '1', 'formatSacn(0) is "1"', 'got ' + JSON.stringify(UI.formatSacn(0)));

  UI.setSacnStart(100);
  check(UI.artnetToSacn(0) === 100, 'an sACN start of 100 puts show universe 1 on sACN 100', 'got ' + UI.artnetToSacn(0));

  // THE REFUSAL. With an Art-Net start of 100, a fixture patched on Art-Net
  // Port-Address 0 is show universe -99, which has no sACN universe at all.
  // Clamping to 1 would light the wrong universe; null is the only honest
  // answer, and it is what makes the screen able to warn BEFORE Start.
  UI.setArtnetStart(100);
  UI.setSacnStart(1);
  check(UI.artnetToSacn(0) === null, 'a universe that maps below sACN 1 is REFUSED (null), never clamped',
    'got ' + UI.artnetToSacn(0));
  check(UI.formatSacn(0) !== '1' && UI.formatSacn(0) !== '0',
    'formatSacn says so in words rather than printing a plausible number',
    'got ' + JSON.stringify(UI.formatSacn(0)));

  // Table 9-10, literal strings, not recomputed from the universe number.
  for (const [u, want] of [[1, '239.255.0.1'], [255, '239.255.0.255'], [256, '239.255.1.0'],
    [1000, '239.255.3.232'], [7962, '239.255.31.26'], [63999, '239.255.249.255']]) {
    check(UI.sacnMulticastAddress(u) === want, 'sACN universe ' + u + ' multicasts to ' + want,
      'got ' + UI.sacnMulticastAddress(u));
  }
  check(UI.sacnMulticastAddress(0) === '', 'there is no multicast group for sACN universe 0',
    'got ' + JSON.stringify(UI.sacnMulticastAddress(0)));

  UI.setArtnetStart(0);
  UI.setSacnStart(1);
}

// ---- 2..4. the screen ----------------------------------------------------

async function main() {
  numberingChecks();

  vm.runInContext('PatchScreen.init(); PatchScreen.onEnterScreen();', ctx, { filename: 'drive' });
  await settle();
  clickTab('rigcheck');
  await settle();

  console.log('2. the protocol control is on the Rig Check page and says which protocol is live');
  let html = rigCheckMarkup();
  check(/data-rc-protocol="artnet"/.test(html) && /data-rc-protocol="sacn"/.test(html),
    'both protocol choices are rendered', 'markup: ' + html.slice(0, 400));
  check(/Art-Net/.test(html) && /sACN/.test(html), 'both are labelled in words');
  check(/Armed/i.test(html) && /Art-Net/.test(html),
    'the armed protocol is stated in WORDS, not by colour alone',
    'markup did not state the armed protocol');

  console.log('3. picking a protocol is Apply-to-confirm: the press itself sends nothing');
  calls.length = 0;
  pressProtocol('sacn');
  await settle();
  check(writes().length === 0, 'pressing sACN sends nothing', 'calls: ' + JSON.stringify(callNames()));
  html = rigCheckMarkup();
  check(/not applied|unapplied|unsaved/i.test(html),
    'the staged-but-not-applied state is stated in words', 'markup: ' + html.slice(0, 600));

  doc.getElementById('rcProtocolApply').fire('click');
  await settle();
  check(writes().length === 0,
    'applying a protocol while nothing is running puts nothing on the wire',
    'calls: ' + JSON.stringify(callNames()));

  console.log('4. a refused start surfaces the SERVER\'S message and snaps back to server truth');
  // The server has never accepted an sACN run, so its echoed protocol is
  // still "artnet" — that is the truth the control must return to.
  rejectStart = true;
  calls.length = 0;
  doc.getElementById('rcStart').fire('click');
  await settle();
  const refused = calls.filter(c => c.name === 'rigCheckStart');
  check(refused.length === 1 && refused[0].args.protocol === 'sacn',
    'the refused attempt did ask for sACN, so the refusal is the server\'s and not ours',
    'bodies: ' + JSON.stringify(refused.map(c => c.args)));
  html = rigCheckMarkup();
  check(html.indexOf('sACN universe must be 1..63999') !== -1,
    'the server\'s 422 text reaches the screen verbatim',
    'markup: ' + html.slice(0, 1400));
  check(/Art-Net Port-Address 0/.test(html),
    'the offending universe the server named is still in the message shown',
    'the message was truncated or replaced by a house phrase');
  check(serverState.protocol === 'artnet', 'the server never accepted sACN');
  check(/armed for Art-Net/i.test(html),
    'the control snapped back to the protocol the server reports',
    'markup: ' + html.slice(0, 900));

  calls.length = 0;
  rejectStart = false;
  doc.getElementById('rcStart').fire('click');
  await settle();
  const after = calls.filter(c => c.name === 'rigCheckStart');
  check(after.length === 1 && after[0].args.protocol === 'artnet',
    'after the refusal the next start goes out on the server-reported Art-Net, not the browser\'s rejected guess',
    'body: ' + JSON.stringify(after.length ? after[0].args : null));
  doc.getElementById('rcStop').fire('click');
  await settle();

  console.log('5. Start sends the chosen protocol in the wire vocabulary the server accepts');
  pressProtocol('sacn');
  await settle();
  doc.getElementById('rcProtocolApply').fire('click');
  await settle();
  calls.length = 0;
  doc.getElementById('rcStart').fire('click');
  await settle();
  const started = calls.filter(c => c.name === 'rigCheckStart');
  check(started.length === 1, 'exactly one start was sent', 'calls: ' + JSON.stringify(callNames()));
  check(started.length === 1 && started[0].args.protocol === 'sacn',
    'the start body carries protocol "sacn"',
    'body: ' + JSON.stringify(started.length ? started[0].args : null));
  html = rigCheckMarkup();
  check(/LIVE ON sACN/i.test(html),
    'the running protocol is shown as live, in words', 'markup: ' + html.slice(0, 900));
  check(/239\.255\./.test(html),
    'the multicast destination the current mapping produces is on screen',
    'markup: ' + html.slice(0, 1600));

  if (failures) {
    console.log('\n' + failures + ' assertion(s) failed');
    process.exit(1);
  }
  console.log('\nall assertions passed');
}

main().catch(e => { console.error(e); process.exit(1); });
