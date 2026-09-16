// functioncheck_protocol_test.js — the FUNCTION CHECK tab's output-protocol
// control, exercised against the LITERAL ui.js / rigcheck.js the browser
// loads, under plain Node, zero dependencies. Run by
// internal/web/sacn_ui_test.go.
//
// WHAT IT PROTECTS
//
//  1. THE TAB CAN SEE THE WIRE AT ALL. Before this control the Function
//     check inherited whatever protocol a Channel-check Start last armed and
//     said nothing about it. The armed/live protocol must be on screen in
//     WORDS — this screen is read in a dark venue on a tablet, and rule 1 of
//     the screen kit is that no state is carried by hue.
//
//  2. APPLY-TO-CONFIRM. Pressing a protocol must send NOTHING. It is the
//     same contract patch.js's control follows, and the reason is the same:
//     a protocol has no intermediate value, and switching one while output
//     flows terminates a stream and opens another.
//
//  3. THE WIRE VOCABULARY. START must put the armed protocol in the
//     .../pattern/output body, in the ids UI.RC_PROTOCOLS declares — the
//     table internal/web/sacn_ui_test.go checks against
//     patch.NormalizeProtocol.
//
//  4. SERVER TRUTH AND THE 422. A refused start must surface the server's
//     own message verbatim (it NAMES the universe it could not map) and snap
//     the control back to the protocol the SERVER reports.
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

// ---- DOM stub ------------------------------------------------------------

const registry = new Map(); // selector -> [nodes], rebuilt from the markup

function makeEl(id) {
  const el = {
    id: id || '', _innerHTML: '', textContent: '', value: '', checked: false,
    disabled: false, className: '', style: {}, dataset: {}, handlers: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
    removeEventListener() {},
    appendChild(c) { return c; }, removeChild() {}, remove() {},
    setAttribute() {}, getAttribute() { return null; },
    focus() {}, blur() {}, click() {}, scrollIntoView() {},
    contains() { return true; },
    querySelector(sel) { return doc.querySelector(sel); },
    querySelectorAll(sel) { return doc.querySelectorAll(sel); },
    fire(type) {
      const ev = { target: this, currentTarget: this, isConnected: true, preventDefault() {}, stopPropagation() {} };
      (this.handlers[type] || []).forEach(fn => fn(ev));
    },
  };
  Object.defineProperty(el, 'innerHTML', {
    get: () => el._innerHTML,
    // Every re-render replaces the node set the screen then wires, exactly
    // as a browser does. Nodes are derived FROM THE MARKUP the screen wrote,
    // so an option the screen never rendered cannot be pressed here.
    set: v => { el._innerHTML = String(v); rebuildNodes(String(v)); },
  });
  return el;
}

const byId = new Map();
const doc = {
  handlers: {}, visibilityState: 'visible',
  getElementById(id) {
    if (!byId.has(id)) byId.set(id, makeEl(id));
    return byId.get(id);
  },
  querySelector(sel) {
    const m = /^#([A-Za-z][\w-]*)$/.exec(String(sel).trim());
    if (m) return byId.has(m[1]) ? byId.get(m[1]) : null;
    const n = registry.get(sel);
    return n && n.length ? n[0] : null;
  },
  querySelectorAll(sel) { return registry.get(sel) || []; },
  createElement() { return makeEl(''); },
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
  removeEventListener() {},
};
doc.body = makeEl('body');

// rebuildNodes reads the ids and data-attributes the screen actually emitted.
function rebuildNodes(html) {
  byId.clear();
  registry.clear();
  for (const m of html.matchAll(/id="([A-Za-z][\w-]*)"/g)) {
    byId.set(m[1], makeEl(m[1]));
  }
  const protos = [];
  for (const m of html.matchAll(/data-rcp-protocol="([^"]*)"/g)) {
    const el = makeEl('proto-' + m[1]);
    el.dataset.rcpProtocol = m[1];
    protos.push(el);
  }
  if (protos.length) registry.set('[data-rcp-protocol]', protos);
  for (const sel of ['[data-rcp-scope]', '[data-rcp-pick]', '[data-rcp-toggle]', '[data-rcp-more]',
    '[data-rcp-slider]', '[data-rcp-wave]', '[data-rcp-dir]', '[data-rcp-on]',
    '[data-rcp-phase]', '[data-rcp-phaseset]']) {
    if (!registry.has(sel)) registry.set(sel, []);
  }
}

function storageStub() {
  const m = new Map();
  return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) };
}

// ---- the server ----------------------------------------------------------

const calls = [];
function record(name, args) { calls.push({ name, args }); }
function callNames() { return calls.map(c => c.name); }
// writes(): everything that is not the read-only status poll.
function writes() { return calls.filter(c => c.name !== 'getPattern'); }

// THE SERVER'S OWN 422, transcribed from sacn.ErrInvalidUniverse's wrapping
// in ArtnetPortAddressToSACNUniverse — which is the body writeRigCheckError
// produces. It NAMES the universe.
const SERVER_422 = 'sACN universe must be 1..63999: show universe -99 (Art-Net Port-Address 0) would map to sACN universe -99, with an sACN start universe of 1 and an Art-Net start universe of 100';
let rejectStart = false;

// pattern mirrors internal/web's patternStatusJSON: `protocol` is ALWAYS
// present (no omitempty), and it is the protocol actually armed.
let pattern = {
  running: false, outputEnabled: false, selectedCount: 1, totalScope: 2, elapsedMs: 0,
  protocol: 'artnet',
  scopeKind: 'all', scopeUniverse: 0, scopePosition: '', scopeEntryIds: [], scopeFixtureType: '',
  fixtureTypes: [],
  tests: [{ id: 'dimmer_sine', kind: 'dimmer_sine', group: 'dimmer', rateHz: 1, min: 0, max: 255, target: '', direction: '', value: 0, on: false, waveform: 'sine', offsetMin: 0, offsetMax: 0, totalScope: 2, appliedCount: 2, skippedCount: 0, inferredCount: 0, missingDetailCount: 0, entries: [] }],
  available: [{ id: 'dimmer_sine', kind: 'dimmer_sine', group: 'dimmer', target: '', label: 'Dimmer', attribute: 'Dimmer', fixtureCount: 2, labelFromGdtf: false }],
  contested: [],
  baseState: { isolate: false, defaultsKnownCount: 4, defaultsUnknownCount: 0, dimmerDrivenCount: 2, shutterOpenedCount: 0, shutterUnknownEntries: [] },
  lastEndReason: '',
};
const clone = o => JSON.parse(JSON.stringify(o));

const apiImpl = {
  getPattern: async () => { record('getPattern'); return clone(pattern); },
  patternSetScope: async (body) => { record('patternSetScope', clone(body || {})); return clone(pattern); },
  patternSelect: async (test, enabled) => { record('patternSelect', { test, enabled }); return clone(pattern); },
  patternSetIsolate: async (v) => { record('patternSetIsolate', v); return clone(pattern); },
  patternSetOutput: async (enabled, protocol) => {
    // The real api.js OMITS the key when no protocol is given; this records
    // the body it would actually build, so an accidental `protocol:
    // undefined` is visible as a key.
    const body = { enabled: !!enabled };
    if (enabled && protocol) body.protocol = protocol;
    record('patternSetOutput', body);
    if (enabled && rejectStart) throw new Error(SERVER_422);
    pattern.outputEnabled = !!enabled;
    pattern.running = !!enabled;
    if (enabled && body.protocol) pattern.protocol = body.protocol;
    return clone(pattern);
  },
};
const Api = new Proxy(apiImpl, {
  get(t, k) { if (k in t) return t[k]; return async (...a) => { record(String(k), a); return clone(pattern); }; },
});

// ---- context -------------------------------------------------------------

const win = {
  handlers: {},
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
  removeEventListener() {},
  dispatchEvent(ev) { (this.handlers[ev.type] || []).forEach(fn => fn(ev)); return true; },
};
const confirmPrompts = [];
let confirmed = true;

const ctx = vm.createContext({
  window: win, document: doc, console,
  sessionStorage: storageStub(), localStorage: storageStub(),
  setTimeout, clearTimeout, setInterval, clearInterval,
  Api,
  confirm: (msg) => { confirmPrompts.push(String(msg)); return confirmed; },
  escapeHtml: (s) => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'),
});
ctx.globalThis = ctx;
for (const f of ['ui.js', 'rigcheck.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}
const UI = vm.runInContext('UI', ctx);
const Panel = vm.runInContext('RigCheckPanel', ctx);

const container = makeEl('rcSubBody');
const entries = [
  { id: 'e1', name: 'Wash L', fixtureType: 'Robe Wash', universe: 0, startAddress: 1, footprint: 20, position: 'SL Boom' },
  { id: 'e2', name: 'Par 1', fixtureType: 'Elation Par', universe: 1, startAddress: 90, footprint: 8, position: 'US Truss 2' },
];
const statuses = [];
Panel.init({ getEntries: () => entries, setStatus: s => statuses.push(String(s)), goToEntries: () => {} });

const tick = () => new Promise(r => setTimeout(r, 0));
async function settle(n) { for (let i = 0; i < (n || 30); i++) await tick(); }
function markup() { return container._innerHTML; }
function pressProtocol(id) {
  const n = (registry.get('[data-rcp-protocol]') || []).find(b => b.dataset.rcpProtocol === id);
  if (!n) throw new Error('the screen never rendered a [data-rcp-protocol] button for ' + id);
  n.fire('click');
}
function press(id) {
  const n = byId.get(id);
  if (!n) throw new Error('the screen never rendered #' + id);
  n.fire('click');
}

async function main() {
  console.log('1. the vocabulary is shared, not re-declared on this screen');
  check(Array.isArray(UI.RC_PROTOCOLS) && UI.RC_PROTOCOLS.length === 2,
    'UI.RC_PROTOCOLS is the one protocol table both Rig Check screens read',
    'got ' + JSON.stringify(UI.RC_PROTOCOLS));
  const rigcheckSrc = fs.readFileSync(path.join(JS_DIR, 'rigcheck.js'), 'utf8');
  check(!/const\s+RC_PROTOCOLS\s*=\s*\[/.test(rigcheckSrc),
    'rigcheck.js declares no protocol table of its own');
  check(!/['"]sacn['"]\s*,\s*label/.test(rigcheckSrc),
    'rigcheck.js does not re-spell the wire ids beside labels');

  await Panel.enter(container);
  await settle();

  console.log('2. the Function check tab shows which protocol it is on, in words');
  let html = markup();
  check(/data-rcp-protocol="artnet"/.test(html) && /data-rcp-protocol="sacn"/.test(html),
    'both protocol choices are rendered on the Function check tab', 'markup: ' + html.slice(0, 300));
  check(/Art-Net/.test(html) && /sACN/.test(html), 'both are labelled in words');
  check(/armed for Art-Net/i.test(html),
    'the armed protocol is stated in WORDS, not by colour alone', 'markup did not state it');
  check(/ARMED/.test(html) && /not selected/.test(html),
    'each option carries its own word, so the control survives greyscale');

  console.log('3. picking a protocol is Apply-to-confirm: the press itself sends nothing');
  calls.length = 0;
  pressProtocol('sacn');
  await settle();
  check(writes().length === 0, 'pressing sACN sends nothing', 'calls: ' + JSON.stringify(callNames()));
  html = markup();
  check(/not applied/i.test(html), 'the staged-but-not-applied state is stated in words',
    'markup: ' + html.slice(0, 600));

  calls.length = 0;
  press('rcpProtocolApply');
  await settle();
  check(writes().length === 0, 'applying while nothing is running puts nothing on the wire',
    'calls: ' + JSON.stringify(callNames()));
  check(/armed for sACN/i.test(markup()), 'but it does move the choice to ARMED, in words',
    'markup: ' + markup().slice(0, 600));

  console.log('4. START carries the armed protocol on .../pattern/output');
  calls.length = 0;
  press('rcpStart');
  await settle();
  const started = calls.filter(c => c.name === 'patternSetOutput');
  check(started.length === 1 && started[0].args.enabled === true && started[0].args.protocol === 'sacn',
    'the output body carries protocol "sacn"', 'bodies: ' + JSON.stringify(started.map(c => c.args)));
  check(confirmPrompts.length >= 1 && /sACN/.test(confirmPrompts[confirmPrompts.length - 1]),
    'the START confirm names the protocol it is about to use',
    'prompt: ' + JSON.stringify(confirmPrompts[confirmPrompts.length - 1]));
  check(/LIVE ON sACN/i.test(markup()), 'the live protocol is shown as live, in words',
    'markup: ' + markup().slice(0, 900));

  console.log('5. switching while LIVE confirms by name and restarts through one call');
  calls.length = 0;
  confirmPrompts.length = 0;
  pressProtocol('artnet');
  await settle();
  check(writes().length === 0, 'staging while live still sends nothing');
  press('rcpProtocolApply');
  await settle();
  check(confirmPrompts.length === 1 && /sACN/.test(confirmPrompts[0]) && /Art-Net/.test(confirmPrompts[0]),
    'the confirm names BOTH the protocol being left and the one being taken',
    'prompt: ' + JSON.stringify(confirmPrompts[0]));
  const switched = calls.filter(c => c.name === 'patternSetOutput');
  check(switched.length === 1 && switched[0].args.protocol === 'artnet' && switched[0].args.enabled === true,
    'exactly one output call carried the new protocol',
    'bodies: ' + JSON.stringify(switched.map(c => c.args)));
  check(/LIVE ON Art-Net/i.test(markup()), 'the tab now says Art-Net is on the wire',
    'markup: ' + markup().slice(0, 900));

  console.log('6. refusing the switch at the confirm changes nothing');
  confirmed = false;
  calls.length = 0;
  pressProtocol('sacn');
  await settle();
  press('rcpProtocolApply');
  await settle();
  check(writes().length === 0, 'a declined confirm sends nothing', 'calls: ' + JSON.stringify(callNames()));
  check(/LIVE ON Art-Net/i.test(markup()), 'and the wire is still what it was');
  confirmed = true;

  console.log('7. STOP is protocol-agnostic');
  calls.length = 0;
  press('rcpStop');
  await settle();
  const stopped = calls.filter(c => c.name === 'patternSetOutput');
  check(stopped.length === 1 && stopped[0].args.enabled === false && !('protocol' in stopped[0].args),
    'the stop body carries no protocol at all — stopping must not re-arm anything',
    'bodies: ' + JSON.stringify(stopped.map(c => c.args)));

  console.log('8. a refused start surfaces the SERVER\'S words and snaps back to server truth');
  // The server is armed on Art-Net and has never accepted sACN. That is the
  // truth the control must return to.
  pattern.protocol = 'artnet';
  await Panel.refreshStatus();
  await settle();
  rejectStart = true;
  pressProtocol('sacn');
  await settle();
  press('rcpProtocolApply'); // not running: arms locally, sends nothing
  await settle();
  calls.length = 0;
  press('rcpStart');
  await settle();
  const refused = calls.filter(c => c.name === 'patternSetOutput');
  check(refused.length === 1 && refused[0].args.protocol === 'sacn',
    'the refused attempt did ask for sACN, so the refusal is the server\'s and not ours',
    'bodies: ' + JSON.stringify(refused.map(c => c.args)));
  html = markup();
  check(html.indexOf('sACN universe must be 1..63999') !== -1,
    'the server\'s 422 text reaches the screen verbatim', 'markup: ' + html.slice(0, 1400));
  check(/Art-Net Port-Address 0/.test(html),
    'the offending universe the server named survives to the screen',
    'the message was truncated or replaced by a house phrase');
  check(/armed for Art-Net/i.test(html),
    'the control snapped back to the protocol the server reports', 'markup: ' + html.slice(0, 900));

  rejectStart = false;
  calls.length = 0;
  press('rcpStart');
  await settle();
  const after = calls.filter(c => c.name === 'patternSetOutput');
  check(after.length === 1 && after[0].args.protocol === 'artnet',
    'after the refusal the next start goes out on the server-reported Art-Net, not the rejected guess',
    'body: ' + JSON.stringify(after.length ? after[0].args : null));

  Panel.stopPolling();
  if (failures) {
    console.log('\n' + failures + ' assertion(s) failed');
    process.exit(1);
  }
  console.log('\nall assertions passed');
  process.exit(0);
}

main().catch(e => { console.error(e); process.exit(1); });
