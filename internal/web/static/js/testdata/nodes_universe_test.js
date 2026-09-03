// nodes_universe_test.js — the Nodes screen's universe notation, run against
// the LITERAL nodes.js and ui.js the browser loads.
//
// WHY THIS FILE EXISTS
//
// The owner reported, from a real bench with an Elation EN4 whose faceplate
// he had confirmed was in 1-based notation starting at universe 1:
//
//     "In the Nodes tab, the universe values in Benny512 are displaying as
//      1 less than what the faceplate says."
//
// Two separate defects sat behind that one sentence, and only the first is
// the one he could see:
//
//  (a) nodes.js had 25 universe references and ZERO UI.formatUniverse calls
//      — the only screen in the app that never converted. So it printed the
//      canonical 0-based Art-Net Port-Address raw, one less than his EN4.
//
//  (b) worse and invisible on his rig: what it printed was not the
//      Port-Address at all but `outputAddress & 0x0F`, the Art-Net *Universe
//      nibble* alone. A 15-bit Port-Address is Net(7) : Sub-Net(4) :
//      Universe(4); the nibble equals the Port-Address only while Net and
//      Sub-Net are both 0, which is true of his rig and of the demo. Put one
//      port on Sub-Net 1 and Nodes would read "0" for the port Patch reads
//      "17" for — the same value spelled two ways in two files, which is
//      this project's most expensive defect class and has now been caught
//      eight times.
//
// So a test that only checked "canonical 0 shows as 1" would pass against a
// screen that is still wrong for every rig bigger than one Sub-Net. The
// Sub-Net case below is the one that actually pins defect (b).
//
// THE WRITE PATH IS THE DANGEROUS HALF. The editor sends ArtAddress to a real
// gateway. ArtAddress carries ONE NetSwitch and ONE SubSwitch for the whole
// packet and only a 4-bit Universe nibble per port, so a node's four ports
// physically cannot span more than one 16-universe block. The owner's chosen
// UI is a single universe box per port in his own display base, with Net and
// Sub-Net derived on save — which means the impossible case has to be
// detected and REFUSED, never clamped, truncated or partially written. A
// wrong universe here sends output to the wrong fixtures on a show.
//
// Run: node nodes_universe_test.js   (also run by go test via
// internal/web/nodes_universe_test.go)

'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

// ---- the smallest DOM/window stub ui.js and nodes.js need at load time ----

function makeEl(id) {
  const el = {
    id, tagName: 'DIV', _innerHTML: '', className: '', dataset: {}, style: {},
    children: [], handlers: {}, value: '', checked: false, scrollTop: 0,
    setAttribute() {}, removeAttribute() {}, getAttribute: () => null,
    appendChild(c) { this.children.push(c); return c; },
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
    removeEventListener() {},
    contains: () => true,
    focus() {}, blur() {}, closest: () => null,
  };
  Object.defineProperty(el, 'innerHTML', {
    get: () => el._innerHTML, set: v => { el._innerHTML = String(v); },
  });
  return el;
}

const store = {};
const doc = {
  handlers: {},
  getElementById: (id) => (store[id] = store[id] || makeEl(id)),
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: (t) => { const e = makeEl(''); e.tagName = t; return e; },
  addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
  removeEventListener() {},
};
doc.body = makeEl('body');
doc.documentElement = makeEl('html');

const win = {
  handlers: {},
  addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
  removeEventListener() {},
  dispatchEvent() { return true; },
  localStorage: {
    _v: {},
    getItem(k) { return Object.prototype.hasOwnProperty.call(this._v, k) ? this._v[k] : null; },
    setItem(k, v) { this._v[k] = String(v); },
    removeItem(k) { delete this._v[k]; },
  },
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
};

function CustomEventStub(type, init) { return { type, detail: (init || {}).detail }; }

const ctx = vm.createContext({
  window: win, document: doc, console,
  CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Intl, fetch: async () => ({ ok: true, json: async () => ({}) }),
  Api: new Proxy({}, { get: () => async () => ({}) }),
  confirm: () => true, prompt: () => null,
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'nodes.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}

// nodes.js and ui.js declare `const NodesScreen` / `const UI`; a top-level
// lexical declaration in a vm script does NOT become a property of the
// context object the way `var` does, so reach them by evaluating the binding
// inside the context (same trick gdtfparse's and reconcile's tests use).
const Nodes = vm.runInContext('NodesScreen', ctx, { filename: 'grab' });
const UI = vm.runInContext('UI', ctx, { filename: 'grab' });

// ---- assertions ---------------------------------------------------------

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

// ---- helpers ------------------------------------------------------------

// portState: one entry of configState.ports as nodes.js builds it. `canonical`
// is the full 15-bit Port-Address, which is the whole point — the editor holds
// canonical and derives the wire fields at send time, so that changing the
// display base can never shift what goes on the wire.
function portState(index, canonicalOut, opts) {
  return Object.assign({
    index, universeIn: 0, universeOut: canonicalOut,
    input: false, output: true, direction: 'output', uniError: '',
  }, opts || {});
}
const stateWith = (...ports) => ({ ports });

// ---- the tests ----------------------------------------------------------

// HONESTY NOTE on sections 1 and 2: these assert UI.formatUniverse's own
// contract, which nodes.js now depends on but never broke — they pass against
// the unfixed nodes.js too, and are here to pin the contract rather than to
// guard the fix. The display half of the fix (nodes.js actually CALLING
// formatUniverse on the full Port-Address instead of printing `addr & 0x0F`)
// is proven in a real browser, where the ports table reads universes 1,2,3,4
// at base 1 and 0,1,2,3 at base 0 for canonical 0-3. Sections 3-6 below are
// the ones that fail against unfixed code — verified by mutating
// deriveAddressing to clamp instead of refuse, which produced
// swOut=[0,1,null,null]: port 1 silently programmed to universe 1 when the
// user asked for 17.
console.log('1. the display converts a full Port-Address, at whichever base is set');
UI.setUniverseBase(1);
check(UI.formatUniverse(0) === '1',
  'canonical 0 displays as 1 at base 1 — what the EN4 faceplate reads',
  'got ' + JSON.stringify(UI.formatUniverse(0)));
UI.setUniverseBase(0);
check(UI.formatUniverse(0) === '0',
  'canonical 0 displays as 0 at base 0 (Art-Net native)',
  'got ' + JSON.stringify(UI.formatUniverse(0)));

console.log('2. DEFECT (b): a Sub-Net port is the FULL Port-Address, not the nibble');
UI.setUniverseBase(1);
// Canonical 17 = Net 0, Sub-Net 1, Universe 1. The nibble is 1; the
// Port-Address is 17. At base 1 the screen must read 18. A screen still
// printing `addr & 0x0F` reads 2 here, and Patch reads 18 for the same port.
check(UI.formatUniverse(17) === '18',
  'canonical 17 (Sub-Net 1) displays as 18 — not the nibble',
  'got ' + JSON.stringify(UI.formatUniverse(17)) + ' — 2 means the nibble is still being printed');
check(UI.formatUniverse(16) === '17',
  'canonical 16 (first address of Sub-Net 1) displays as 17',
  'got ' + JSON.stringify(UI.formatUniverse(16)));

console.log('3. the write path derives Net/Sub-Net/SwOut from canonical');
UI.setUniverseBase(1);
// Four ports in one block: canonical 0..3 (Net 0, Sub-Net 0).
let d = Nodes.__deriveForTest(stateWith(
  portState(0, 0), portState(1, 1), portState(2, 2), portState(3, 3)));
check(d.ok === true, 'four ports inside one block are sendable', JSON.stringify(d.message || ''));
check(d.netSwitch === 0 && d.subSwitch === 0,
  'Net 0 / Sub-Net 0 derived for canonical 0-3',
  `got net=${d.netSwitch} sub=${d.subSwitch}`);
check(JSON.stringify(d.swOut) === JSON.stringify([0, 1, 2, 3]),
  'SwOut carries the per-port Universe nibbles 0,1,2,3',
  'got ' + JSON.stringify(d.swOut));

// A block that is NOT zero: canonical 17..19 all sit in Net 0 / Sub-Net 1,
// nibbles 1,2,3. This is the case defect (b) hid.
d = Nodes.__deriveForTest(stateWith(portState(0, 17), portState(1, 18), portState(2, 19)));
check(d.ok === true, 'three ports inside Sub-Net 1 are sendable', JSON.stringify(d.message || ''));
check(d.netSwitch === 0 && d.subSwitch === 1,
  'Net 0 / Sub-Net 1 derived for canonical 17-19',
  `got net=${d.netSwitch} sub=${d.subSwitch}`);
check(JSON.stringify(d.swOut.slice(0, 3)) === JSON.stringify([1, 2, 3]),
  'SwOut carries nibbles 1,2,3 — the low step of each Port-Address',
  'got ' + JSON.stringify(d.swOut));

// A high Net, to prove the 7-bit Net field is really being used.
d = Nodes.__deriveForTest(stateWith(portState(0, 4096)));
check(d.ok === true && d.netSwitch === 16 && d.subSwitch === 0 && d.swOut[0] === 0,
  'canonical 4096 derives Net 16 / Sub-Net 0 / nibble 0',
  `ok=${d.ok} net=${d.netSwitch} sub=${d.subSwitch} swOut0=${d.swOut && d.swOut[0]}`);

console.log('4. universes that cannot share one Net/Sub-Net are REFUSED, not clamped');
// Canonical 0 (block 0) and canonical 17 (block 1) cannot coexist on one
// ArtAddress packet. Anything other than a refusal here writes a wrong
// universe to a real gateway.
d = Nodes.__deriveForTest(stateWith(portState(0, 0), portState(1, 17)));
check(d.ok === false,
  'two ports in different 16-universe blocks are refused',
  'ok was ' + d.ok + ' — a clamp or a silent winner would corrupt a live rig');
check(d.swOut === undefined && d.netSwitch === undefined,
  'a refusal carries NO wire fields at all — nothing partial can be sent',
  'got netSwitch=' + d.netSwitch + ' swOut=' + JSON.stringify(d.swOut));
check(/port 0/.test(d.message) && /port 1/.test(d.message),
  'the refusal names the specific ports that conflict',
  'message was: ' + d.message);
check(/16-universe block/.test(d.message) && !/0x|nibble|SwOut|bitmask/i.test(d.message),
  'the refusal is written for a lighting tech, not a programmer',
  'message was: ' + d.message);

console.log('5. a per-port validation error also refuses, and says which port');
d = Nodes.__deriveForTest(stateWith(portState(0, 0), portState(1, 1, { uniError: 'not a number' })));
check(d.ok === false && /Port 1/.test(d.message),
  'a bad universe on one port blocks the whole save and names that port',
  'ok=' + d.ok + ' message=' + d.message);
check(d.swOut === undefined, 'nothing is sent when any port is invalid');

console.log('6. names-only saves leave addressing untouched');
d = Nodes.__deriveForTest(stateWith(
  Object.assign(portState(0, 0), { input: false, output: false })));
check(d.ok === true && d.netSwitch === null && d.subSwitch === null,
  'a node with no addressable port programs no block',
  `ok=${d.ok} net=${d.netSwitch} sub=${d.subSwitch}`);

console.log(failures ? `\n${failures} assertion(s) failed.` : '\nall assertions passed');
process.exit(failures ? 1 : 0);
