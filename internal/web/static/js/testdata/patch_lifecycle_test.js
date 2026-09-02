// patch_lifecycle_test.js — cross-file lifecycle regression test for the
// Patch screen's three sub-tabs, run under plain Node via
// internal/web/patch_lifecycle_test.go (`node patch_lifecycle_test.js`,
// exit 0 = pass). Not part of the browser app; testdata/ is never loaded by
// index.html.
//
// WHY THIS FILE EXISTS
//
// Rig Check (rigcheck.js) and Reconcile (reconcile.js) were each extracted
// out of patch.js in a different wave. patch.js still owns the lifecycle —
// which sub-tab is showing, when a panel is entered and left, and the
// screen's own copy of the patch — while each panel owns its own server
// snapshot. Both halves verified themselves and both were internally
// correct; these two defects lived in the gap between them:
//
//  1. SAFETY. Leaving the Rig Check view (to Entries/Reconcile) or the
//     Function sub-view (to Channel check) stopped RigCheckPanel's
//     client-liveness heartbeat but NOT the pattern output. That heartbeat
//     is what feeds the server's watchdog, so output kept driving real
//     fixtures for up to PatternWatchdogTimeout (5s) with nothing on screen
//     saying so; then the watchdog blacked out and stamped lastEndReason
//     "watchdog" — a false diagnosis, the page was alive the whole time —
//     and the Channel check sub-view, which renders "A Function check
//     pattern is running" with its own controls disabled off
//     RigCheckPanel.outputEnabled(), never learned about that blackout and
//     sat behind the stale banner indefinitely.
//
//  2. STALENESS. Reconcile's mutators write to the PATCH (commit/decommit
//     stamp ConfirmedUID + MatchState; adopt writes intended settings and
//     Mode). reconcile.js correctly re-read its own board after each one,
//     but nothing refetched patch.js's `patchData` — so committing on
//     Reconcile and then switching to Entries showed the entry still
//     "Unresolved" until the whole Patch tab was left and re-entered.
//
// HOW IT TESTS THEM
//
// It loads the LITERAL ui.js / rigcheck.js / reconcile.js / patch.js the
// browser loads into one vm context over a hand-written DOM stub, wires a
// recording Api, and drives the screen the way a user does: through the
// click handlers those files attach to their own buttons. What is asserted
// is the ordered list of HTTP calls the three files actually make and the
// data the Entries table would render from. Nothing here reads a file's
// internals or restates its source.
//
// The DOM stub is deliberately dumb — innerHTML is a write-only sink, and
// querySelectorAll answers only the handful of selectors these screens use
// to wire their own controls, with FRESH nodes per query so a handler
// registered by an earlier render can never fire twice. This test is about
// lifecycle and network traffic, not markup. Zero external dependencies,
// same as the rest of this project.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

// ---- DOM stub ----------------------------------------------------------

function makeEl(id) {
  const el = {
    id: id || '',
    _innerHTML: '',
    textContent: '',
    value: '',
    checked: false,
    disabled: false,
    readOnly: false,
    type: '',
    className: '',
    style: {},
    dataset: {},
    children: [],
    parentElement: null,
    selectionStart: 0,
    handlers: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    // LAST WIRING WINS. In the browser these screens re-wire their controls
    // after every render, and the node they wire is a brand-new one that
    // innerHTML just created — the previous render's handler goes away with
    // the node it was attached to. This stub memoises one node per id (it
    // has no markup to re-create nodes from), so keeping every handler ever
    // registered would fire a control's handler once per render that had
    // ever happened, which no browser does. Replacing per type reproduces
    // the real "one live handler per control" behaviour.
    addEventListener(type, fn) { this.handlers[type] = [fn]; },
    removeEventListener() {},
    appendChild(c) { this.children.push(c); return c; },
    removeChild() {},
    remove() {},
    setAttribute() {},
    getAttribute() { return null; },
    focus() {}, blur() {}, click() {},
    setSelectionRange() {},
    scrollIntoView() {},
    querySelector(sel) { return doc.querySelector(sel); },
    querySelectorAll(sel) { return doc.querySelectorAll(sel); },
    contains() { return true; },
    fire(type) {
      const ev = { target: this, currentTarget: this, preventDefault() {}, stopPropagation() {} };
      (this.handlers[type] || []).forEach(fn => fn(ev));
    },
  };
  Object.defineProperty(el, 'innerHTML', { get: () => el._innerHTML, set: v => { el._innerHTML = String(v); } });
  return el;
}

// SELECTOR_NODES: the selectors these three files use to wire their own
// controls, and the dataset each matched node carries. Everything else
// answers with [] — a screen wiring a control this table does not name is
// simply not driven by this test.
function selectorNodes(sel) {
  switch (sel) {
    case '#patchViewTabs .detail-tab-btn':
      return ['entries', 'reconcile', 'rigcheck'].map(v => { const b = makeEl('tab-' + v); b.dataset.view = v; return b; });
    case '[data-rcb-arm-entry]': {
      const b = makeEl(''); b.dataset.rcbArmEntry = 'e1'; return [b];
    }
    case '[data-rcb-commit-device]': {
      const b = makeEl(''); b.dataset.rcbCommitDevice = '2222:00000001'; return [b];
    }
    case '[data-rcb-decommit]': {
      const b = makeEl(''); b.dataset.rcbDecommit = 'e1'; return [b];
    }
    default:
      return [];
  }
}

const byId = new Map();
const doc = {
  handlers: {},
  visibilityState: 'visible',
  lastQuery: new Map(),
  getElementById(id) {
    if (!byId.has(id)) byId.set(id, makeEl(id));
    return byId.get(id);
  },
  // A bare "#id" selector must resolve to the SAME node getElementById
  // hands back — rigcheck.js reaches its own controls with
  // containerEl.querySelector('#rcpStart') while patch.js reaches its with
  // document.getElementById, and two different stub nodes for one id would
  // silently disconnect a wired handler from the button this test presses.
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

// live(sel, pred): the node from the MOST RECENT render that matches — the
// one a user's click would actually land on.
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

// ---- recording Api -----------------------------------------------------

const calls = [];
function record(name, args) { calls.push({ name, args }); }
function callNames() { return calls.map(c => c.name); }
function countOf(name) { return calls.filter(c => c.name === name).length; }

let outputEnabled = false;
function patternStatus() {
  return {
    running: outputEnabled, outputEnabled, selectedCount: 1, totalScope: 2, elapsedMs: 0,
    tests: [{
      id: 'dimmer_sine', kind: 'dimmer_sine', group: 'dimmer', target: '', rateHz: 1,
      min: 0, max: 255, value: 128, on: true, waveform: 'sine', offsetMin: 0, offsetMax: 0,
      direction: '', appliedCount: 2, totalScope: 2,
    }],
    available: [{ id: 'dimmer_sine', kind: 'dimmer_sine', group: 'dimmer', target: '', label: 'dimmer_sine', fixtureCount: 2, labelFromGdtf: false }],
    contested: [],
    baseState: { isolate: false, defaultsKnownCount: 2, defaultsUnknownCount: 0, dimmerDrivenCount: 2, shutterOpenedCount: 0, shutterUnknownEntries: [] },
    lastEndReason: '',
  };
}

// committed drives BOTH the patch document and the reconcile board, exactly
// as the server does: a commit stamps the entry's matchState/confirmedUid.
// That is what lets assertion 3 check what the Entries table can SEE rather
// than merely counting requests.
let committed = false;
function patchDoc() {
  return {
    active: true,
    patch: {
      schemaVersion: 4, name: 'Lifecycle Test', entries: [
        { id: 'e1', name: 'Wash L', fixtureType: 'Robe Wash', universe: 0, startAddress: 1, footprint: 20, position: 'SL Boom', matchState: committed ? 'confirmed' : '', confirmedUid: committed ? '2222:00000001' : '' },
        { id: 'e2', name: 'Par 1', fixtureType: 'Elation Par', universe: 1, startAddress: 90, footprint: 8, position: 'US Truss 2', matchState: '', confirmedUid: '' },
      ],
    },
  };
}
function board() {
  return {
    generatedAt: new Date().toISOString(),
    intended: [{
      entryId: 'e1', name: 'Wash L', fixtureType: 'Robe Wash', position: 'SL Boom', universe: 0,
      startAddress: 1, footprint: 20, committed, committedUid: committed ? '2222:00000001' : '',
      deviceOnline: committed, differsCount: 0, diff: [], asFoundAt: null,
    }],
    detected: [{
      uid: '2222:00000001', manufacturer: 'Robe', model: 'Wash', label: '', universe: 0,
      startAddress: 1, addressKnown: true, footprint: 20, footprintKnown: true,
      committedToEntryId: committed ? 'e1' : '', committedToName: committed ? 'Wash L' : '',
    }],
    proposals: [],
  };
}

const apiImpl = {
  getPatch: async () => { record('getPatch'); return patchDoc(); },
  getPatchCollisions: async () => { record('getPatchCollisions'); return []; },
  getRigCheckState: async () => { record('getRigCheckState'); return { running: false, level: 255, entryIndex: 0, entryCount: 0, channelOffset: 0 }; },
  rigCheckStop: async () => { record('rigCheckStop'); outputEnabled = false; return { running: false, level: 255, entryIndex: 0, entryCount: 0, channelOffset: 0 }; },
  rigCheckStopBeacon: () => { record('rigCheckStopBeacon'); },
  getPattern: async () => { record('getPattern'); return patternStatus(); },
  patternSetScope: async (b) => { record('patternSetScope', b); return patternStatus(); },
  patternSelect: async (spec, on) => { record('patternSelect', { spec, on }); return patternStatus(); },
  patternSetIsolate: async (on) => { record('patternSetIsolate', on); return patternStatus(); },
  patternSetOutput: async (enabled) => { record('patternSetOutput', enabled); outputEnabled = !!enabled; return patternStatus(); },
  getReconcileBoard: async () => { record('getReconcileBoard'); return board(); },
  reconcileCommit: async (id, uid) => { record('reconcileCommit', { id, uid }); committed = true; return { readError: '' }; },
  reconcileDecommit: async (id) => { record('reconcileDecommit', id); committed = false; return {}; },
  // Real synchronous helpers from api.js that the render paths call inline.
  formatAddressRange: (start, footprint) => String(start) + '-' + String(start + (footprint || 1) - 1),
  patchExportUrl: (f) => '/api/patch/export?format=' + f,
  patchReconcileExportUrl: (f) => '/api/patch/reconcile/export?format=' + f,
  identify: async () => ({}),
};
// Anything these screens call that this test does not model answers with an
// empty object rather than throwing, so an unrelated call site can never
// masquerade as one of the behaviours under test.
const Api = new Proxy(apiImpl, {
  get(t, k) {
    if (k in t) return t[k];
    return async (...args) => { record(String(k), args); return {}; };
  },
});

// ---- context ------------------------------------------------------------

const win = {
  handlers: {},
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
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
  Api,
  confirm: () => true,
  prompt: () => null,
  // escapeHtml lives in nodes.js, a screen this test has no reason to load.
  // It is not under test here — only that markup gets built at all.
  escapeHtml: (s) => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'),
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'rigcheck.js', 'reconcile.js', 'patch.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}

const tick = () => new Promise(r => setTimeout(r, 0));
async function settle(n) { for (let i = 0; i < (n || 25); i++) await tick(); }

// ---- assertions ---------------------------------------------------------

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

function clickTab(view) { live('#patchViewTabs .detail-tab-btn', b => b.dataset.view === view).fire('click'); }

async function goToRigCheckFunction() {
  clickTab('rigcheck');
  await settle();
  doc.getElementById('rcSubFunction').fire('click');
  await settle();
}

async function startOutput() {
  doc.getElementById('rcpStart').fire('click'); // confirm() is stubbed to accept
  await settle();
  return outputEnabled === true;
}

async function main() {
  vm.runInContext('PatchScreen.init(); PatchScreen.onEnterScreen();', ctx, { filename: 'drive' });
  await settle();

  console.log('1. leaving the Rig Check VIEW with pattern output flowing stops the output');
  await goToRigCheckFunction();
  check(await startOutput(), 'output is flowing before the sub-tab switch', 'outputEnabled=' + outputEnabled);
  calls.length = 0;
  clickTab('entries');
  await settle();
  const stop1 = calls.filter(c => c.name === 'patternSetOutput' && c.args === false);
  check(stop1.length === 1, 'Rig Check -> Entries sends exactly one patternSetOutput(false)',
    'calls after the switch: ' + JSON.stringify(callNames()));
  check(outputEnabled === false, 'pattern output is off after the sub-tab switch', 'outputEnabled=' + outputEnabled);

  console.log('2. leaving the Function SUB-VIEW for Channel check stops the output');
  await goToRigCheckFunction();
  check(await startOutput(), 'output is flowing before the sub-view switch', 'outputEnabled=' + outputEnabled);
  calls.length = 0;
  doc.getElementById('rcSubClassic').fire('click');
  await settle();
  const stop2 = calls.filter(c => c.name === 'patternSetOutput' && c.args === false);
  check(stop2.length === 1, 'Function -> Channel check sends exactly one patternSetOutput(false)',
    'calls after the switch: ' + JSON.stringify(callNames()));
  check(outputEnabled === false, 'pattern output is off after the sub-view switch', 'outputEnabled=' + outputEnabled);

  console.log('3. a Reconcile commit reaches the screen\'s own patch copy without a manual refresh');
  clickTab('reconcile');
  await settle();
  check(countOf('getReconcileBoard') > 0, 'the Reconcile board was fetched on entry');
  committed = false;
  calls.length = 0;
  // Commit the way the screen does: arm the entry on the left, then press
  // Commit on the device that answers for it on the right.
  live('[data-rcb-arm-entry]').fire('click');
  await settle();
  live('[data-rcb-commit-device]').fire('click');
  await settle(40);
  check(countOf('reconcileCommit') === 1, 'the commit was sent', 'calls: ' + JSON.stringify(callNames()));
  check(countOf('getPatch') >= 1, 'the host refetched the patch after the commit',
    'calls after the commit: ' + JSON.stringify(callNames()));

  // The refetch is only worth anything if the Entries view then renders the
  // new state, so switch to it and read what it was built from.
  calls.length = 0;
  clickTab('entries');
  await settle();
  check(countOf('getPatch') === 0,
    'switching to Entries needs no further fetch — the data was already refreshed',
    'calls on the way into Entries: ' + JSON.stringify(callNames()));
  const rendered = doc.querySelector('#patchEntriesTable tbody').innerHTML;
  check(/Confirmed|confirmed/.test(rendered),
    'the Entries table shows the entry as confirmed',
    'rendered entries markup did not mention the confirmed match state');

  if (failures) {
    console.log('\n' + failures + ' assertion(s) failed');
    process.exit(1);
  }
  console.log('\nall assertions passed');
}

main().catch(e => { console.error(e); process.exit(1); });
