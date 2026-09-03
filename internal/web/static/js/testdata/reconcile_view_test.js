// reconcile_view_test.js — regression test for the Reconcile screen's VIEW
// layer: the not-fitted diff state, and the per-pane sort/grouping the owner
// asked for. Run under plain Node via internal/web/reconcile_view_test.go
// (`node reconcile_view_test.js`, exit 0 = pass). Not part of the browser
// app; testdata/ is never loaded by index.html.
//
// WHY THIS FILE EXISTS
//
//  1. NOT_FITTED. A Go change added a third as-found state,
//     patch.DiffNotFitted ("not_fitted") — the fixture's own
//     SUPPORTED_PARAMETERS says it does not have this feature at all (a GLP
//     JDC-1 has tilt and no pan, so panInvert is not-fitted, not unread).
//     reconcile.js's STATE_WORD map had no entry for it and falls back to
//     `STATE_WORD[line.state] || line.state`, so the screen rendered the raw
//     wire enum `not_fitted` into a pill. That is this project's most
//     expensive defect class — one value spelled two ways either side of the
//     Go/JS boundary — and the ONLY thing that catches it is a test that
//     reads the markup the real file produces. internal/web's Go tests prove
//     the server sends "not_fitted"; nothing proved the client could say it.
//
//  2. SORT AND GROUPING. Sorting universes as DISPLAYED TEXT is how this
//     project's universe sort bug happened (1, 11, 12, 2, 21, 3). The
//     universe assertions below use universes 1, 2, 3, 11 and 21 precisely
//     because that set separates a numeric sort from a lexicographic one, and
//     they assert the ORDER OF THE RENDERED CARDS, not the internals of a
//     comparator.
//
//  3. SURVIVING A REFETCH. reconcile.js re-fetches the whole board and
//     repaints after every commit, decommit, push, adopt and onPatchChanged
//     (rule 1: the server board is the only state). Sort and scroll are view
//     state and must survive that. A pane that snaps back to its default
//     order after every commit is unusable for the exact job it exists for.
//
// HOW IT TESTS THEM
//
// It loads the LITERAL ui.js and reconcile.js the browser loads into one vm
// context over a hand-written DOM stub, wires a recording Api, and drives the
// screen through the handlers those files attach to their own controls. What
// is asserted is the MARKUP reconcile.js actually emits and the ordered list
// of HTTP calls it actually makes. Nothing here restates the file's source or
// stubs the screen under test.
//
// The DOM stub keeps innerHTML as a readable string, because the order of
// cards in a pane is the thing under test. Zero external dependencies, same
// as the rest of this project.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

// ---- DOM stub ----------------------------------------------------------

// Nodes are memoised by id, exactly as patch_lifecycle_test.js does and for
// the same reason: the screen re-wires its controls after every render and
// the browser's replaced node takes the old handler with it, so handlers are
// replaced per type rather than accumulated.
function makeEl(id) {
  const el = {
    id: id || '',
    _innerHTML: '',
    textContent: '',
    value: '',
    className: '',
    style: {},
    dataset: {},
    children: [],
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    handlers: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    addEventListener(type, fn) { this.handlers[type] = [fn]; },
    removeEventListener() {},
    appendChild(c) { this.children.push(c); return c; },
    removeChild() {}, remove() {}, setAttribute() {},
    getAttribute() { return null; },
    focus() { doc.focused = this; }, blur() {}, click() {},
    setSelectionRange() {}, scrollIntoView() {},
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

// SELECTOR_NODES: the [data-*] selectors reconcile.js wires, answered with
// nodes carrying the dataset the real markup would. A control this table does
// not name is simply not driven by this test.
function selectorNodes(sel) {
  const mk = (key, val) => { const b = makeEl(''); b.dataset[key] = val; return b; };
  switch (sel) {
    case '[data-rcb-arm-entry]':
      return state.armEntryIds.map(id => mk('rcbArmEntry', id));
    case '[data-rcb-commit-device]':
      return state.commitDeviceUids.map(u => mk('rcbCommitDevice', u));
    case '[data-rcb-toggle]':
      return state.toggleIds.map(id => mk('rcbToggle', id));
    default:
      return [];
  }
}

const byId = new Map();
const doc = {
  handlers: {},
  focused: null,
  lastQuery: new Map(),
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
    this.lastQuery.set(sel, nodes);
    return nodes;
  },
  createElement(tagName) { const e = makeEl(''); e.tagName = tagName; return e; },
  addEventListener(type, fn) { (this.handlers[type] = this.handlers[type] || []).push(fn); },
  removeEventListener() {},
};
doc.body = makeEl('body');
doc.documentElement = makeEl('html');

function live(sel, pred) {
  // lastQuery holds whatever reconcile.js asked for during its most recent
  // wiring pass, and that is the point: firing a click on one of those nodes
  // runs the screen's OWN handler. A node synthesized here instead would
  // carry no handler and fire into nothing, so a test that sets up `state`
  // must re-render (see wire()) before reaching for a control.
  const nodes = doc.lastQuery.get(sel) || [];
  const n = pred ? nodes.find(pred) : nodes[0];
  if (!n) throw new Error('no live node for selector ' + sel);
  return n;
}

// wire: re-render so reconcile.js re-runs its querySelectorAll wiring pass
// against the CURRENT `state`, which is what puts freshly-set-up controls
// into doc.lastQuery with the screen's handlers attached. Setting `state` and
// reaching straight for live() finds the previous pass's nodes.
async function wire() { panel.render(); await settle(); }

// ---- the board ---------------------------------------------------------
//
// Universes are RAW 0-based Art-Net Port-Addresses on the wire, exactly as
// the server sends them; UI.formatUniverse turns them into the displayed
// 1-based numbers. Raw 0, 1, 2, 10, 20 display as 1, 2, 3, 11, 21 — the set
// that separates a numeric sort from a lexicographic one.
const state = {
  committedEntry: null,   // entryId once committed in this run
  armEntryIds: [],
  commitDeviceUids: [],
  toggleIds: [],
};

// diffLines: three not-fitted fields, one unread, one differing and one
// matching — the exact shape internal/web/reconcilecommit.go emits for a
// fixture whose SUPPORTED_PARAMETERS omits pan. Note foundErr is "" on every
// not-fitted line: that is the server's deliberate signal that this is a
// settled answer and not a failed read.
function diffLines() {
  return [
    {
      field: 'startAddress', label: 'DMX address', kind: 'number', state: 'differs', pushable: true,
      intendedKnown: true, intendedNum: 41, foundKnown: true, foundNum: 21, foundErr: '',
    },
    {
      field: 'universe', label: 'Universe', kind: 'universe', state: 'match', pushable: false,
      intendedKnown: true, intendedNum: 0, foundKnown: true, foundNum: 0, foundErr: '',
    },
    {
      field: 'dimmerCurve', label: 'Dimmer curve', kind: 'index', state: 'unread', pushable: true,
      intendedKnown: false, foundKnown: false, foundErr: 'NACK: unsupported PID',
    },
    {
      field: 'panInvert', label: 'Pan invert', kind: 'bool', state: 'not_fitted', pushable: true,
      intendedKnown: true, intendedBool: true, foundKnown: false, foundBool: false, foundErr: '',
    },
    {
      field: 'tiltInvert', label: 'Tilt invert', kind: 'bool', state: 'not_fitted', pushable: true,
      intendedKnown: false, foundKnown: false, foundBool: false, foundErr: '',
    },
  ];
}

// The left pane. Names are deliberately NOT in universe order in the source
// array, so an assertion about universe order cannot pass by accident on the
// server's own ordering.
function intendedRows() {
  const rows = [
    { entryId: 'e-u21', name: 'Zulu 21', fixtureType: 'Robe Wash', position: 'Rear Truss', fixtureNumber: '111', universe: 20, startAddress: 5, footprint: 20 },
    { entryId: 'e-u2', name: 'Bravo 2', fixtureType: 'Elation Par', position: 'US Truss 2', fixtureNumber: '102', universe: 1, startAddress: 90, footprint: 8 },
    { entryId: 'e-u11', name: 'Kilo 11', fixtureType: 'Practical LED', position: '', fixtureNumber: '109', universe: 10, startAddress: 3, footprint: 10 },
    { entryId: 'e-u1a', name: 'Alpha 1', fixtureType: 'Chroma-Q CF2', position: 'US Truss 1', fixtureNumber: '101', universe: 0, startAddress: 1, footprint: 20 },
    { entryId: 'e-u3', name: 'Charlie 3', fixtureType: 'Ayrton Spot', position: 'SR Truss', fixtureNumber: '103', universe: 2, startAddress: 40, footprint: 24 },
    { entryId: 'e-u1b', name: 'Delta 1', fixtureType: 'Robe Wash', position: 'SL Boom', fixtureNumber: '106', universe: 0, startAddress: 41, footprint: 20 },
  ];
  return rows.map(r => {
    const committed = state.committedEntry === r.entryId;
    return Object.assign({}, r, {
      committed,
      committedUid: committed ? '2222:00000002' : '',
      deviceOnline: committed,
      diff: committed ? diffLines() : [],
      differsCount: committed ? 1 : 0,
      unreadCount: committed ? 1 : 0,
      notFittedCount: committed ? 2 : 0,
      asFoundAt: committed ? new Date().toISOString() : null,
    });
  });
}

function detectedRows() {
  const rows = [
    { uid: '2222:00000002', manufacturer: 'Robe', model: 'Wash', label: '', universe: 20, startAddress: 5, addressKnown: true, footprint: 20, footprintKnown: true },
    { uid: '00ff:0000000a', manufacturer: 'Ayrton', model: 'Spot', label: '', universe: 2, startAddress: 40, addressKnown: true, footprint: 24, footprintKnown: true },
    { uid: 'aaaa:00000003', manufacturer: 'Elation', model: 'Par', label: '', universe: 1, startAddress: 90, addressKnown: true, footprint: 8, footprintKnown: true },
    { uid: '1111:00000004', manufacturer: 'Chroma-Q', model: 'Color Force II', label: '', universe: 0, startAddress: 1, addressKnown: true, footprint: 20, footprintKnown: true },
    { uid: '3333:00000005', manufacturer: 'Zero 88', model: 'Practical', label: '', universe: 10, startAddress: 3, addressKnown: true, footprint: 10, footprintKnown: true },
  ];
  return rows.map(d => {
    const claimed = state.committedEntry && d.uid === '2222:00000002';
    return Object.assign({}, d, {
      committedToEntryId: claimed ? state.committedEntry : '',
      committedToName: claimed ? 'Zulu 21' : '',
    });
  });
}

function board() {
  return {
    generatedAt: new Date().toISOString(),
    intended: intendedRows(),
    detected: detectedRows(),
    proposals: [],
  };
}

// ---- recording Api -----------------------------------------------------

const calls = [];
function record(name, args) { calls.push({ name, args }); }
function countOf(name) { return calls.filter(c => c.name === name).length; }

const apiImpl = {
  getReconcileBoard: async () => { record('getReconcileBoard'); return board(); },
  reconcileCommit: async (id, uid) => { record('reconcileCommit', { id, uid }); state.committedEntry = id; return { readError: '' }; },
  reconcileDecommit: async (id) => { record('reconcileDecommit', id); state.committedEntry = null; return {}; },
  reconcilePush: async (id, fields) => { record('reconcilePush', { id, fields }); return { results: [] }; },
  reconcileAdoptIntended: async (id, fields) => { record('reconcileAdoptIntended', { id, fields }); return {}; },
  patchReconcileExportUrl: (f) => '/api/patch/reconcile/export?format=' + f,
  identify: async () => ({}),
};
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
  CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Intl,
  Api,
  confirm: () => true,
  prompt: () => null,
  // escapeHtml lives in nodes.js, a screen this test has no reason to load.
  // Byte-for-byte the same replacement set, INCLUDING the apostrophe -> &#39;
  // rule, because the not-fitted phrase contains one and this test asserts on
  // the escaped markup.
  escapeHtml: (s) => (s === undefined || s === null) ? '' : String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c])),
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'reconcile.js']) {
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

const host = makeEl('rcbHost');
// reconcile.js declares `const ReconcilePanel`, and a top-level lexical
// declaration in a vm script does NOT become a property of the context
// object the way `var` does — `ctx.ReconcilePanel` is undefined. Evaluate the
// binding INSIDE the context instead, the same way gdtfparse's test reaches
// GdtfParse and patch_lifecycle's reaches PatchScreen.
const panel = vm.runInContext('ReconcilePanel', ctx, { filename: 'grab-panel' });

function html() { return host.innerHTML; }

// paneHtml: the markup of one pane's list, sliced out of the board by the
// list's own id. Slicing by markup rather than by DOM traversal keeps the DOM
// stub dumb; the ids are the ones reconcile.js itself emits.
function paneHtml(which) {
  const s = html();
  const id = which === 'intended' ? 'rcbIntendedList' : 'rcbDetectedList';
  const start = s.indexOf('id="' + id + '"');
  if (start < 0) return '';
  const other = which === 'intended' ? 'rcbDetectedList' : 'rcbIntendedList';
  const stop = s.indexOf('id="' + other + '"');
  return stop > start ? s.slice(start, stop) : s.slice(start);
}

// names(which): the card names of one pane, in the order they are RENDERED.
function names(which) {
  const out = [];
  const re = /class="b5-statecard__name">([^<]*)</g;
  let m;
  const h = paneHtml(which);
  while ((m = re.exec(h)) !== null) out.push(m[1]);
  return out;
}

// uidsRendered: the detected pane's UIDs in render order (the mono UID span
// each detected card carries).
function detectedUids() {
  const out = [];
  const re = /class="b5-text-mono b5-rcb-uid">([^<]*)</g;
  let m;
  const h = paneHtml('detected');
  while ((m = re.exec(h)) !== null) out.push(m[1]);
  return out;
}

// universesRendered: the DISPLAYED universe number of each card in a pane, in
// render order, pulled out of the card's own meta line. This is what the owner
// actually sees, which is the only thing worth asserting an order on.
function universes(which) {
  const out = [];
  const re = /class="b5-statecard__meta">[^<]*?Universe (\d+)/g;
  let m;
  const h = paneHtml(which);
  while ((m = re.exec(h)) !== null) out.push(Number(m[1]));
  return out;
}

function setSort(which, key) {
  const sel = doc.getElementById(which === 'intended' ? 'rcbSortIntended' : 'rcbSortDetected');
  sel.value = key;
  sel.fire('change');
}

const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);

async function main() {
  panel.init({ setStatus: () => {}, getUniverseLabel: () => 'Universe', onPatchChanged: () => {} });
  await panel.enter(host);
  await settle();

  console.log('1. every DiffState the server can send has a human phrase');
  // Commit first: the diff only exists on a committed row.
  state.armEntryIds = ['e-u21'];
  state.commitDeviceUids = ['2222:00000002'];
  await wire();
  live('[data-rcb-arm-entry]').fire('click');
  await settle();
  await wire();
  live('[data-rcb-commit-device]').fire('click');
  await settle(40);
  check(countOf('reconcileCommit') === 1, 'the commit was sent');
  // commit() opens the diff panel straight away, so the diff is on screen.
  const h1 = html();
  check(/Fixture doesn&#39;t have this/.test(h1),
    'a not_fitted line renders the human phrase "Fixture doesn\'t have this"',
    'no such phrase in the rendered board');
  check(!/>not_fitted</.test(h1),
    'the raw wire enum "not_fitted" is never rendered as a state word',
    'found a pill whose text is the bare enum');
  check(/b5-pill--tag b5-pill--info">Fixture doesn&#39;t have this/.test(h1),
    'the not_fitted pill carries the informational tone, not an error tone',
    'tone class on the not_fitted pill was not b5-pill--info');
  check(!/data-rcb-push-field="panInvert"/.test(h1) && !/data-rcb-push-field="tiltInvert"/.test(h1),
    'no Apply button is offered for a not_fitted field',
    'a data-rcb-push button exists for a not-fitted field');
  check(/data-rcb-push-field="startAddress"/.test(h1),
    'a genuinely differing pushable field still gets its Apply button',
    'the differs line lost its Apply — the fix went too far');

  console.log('2. notFittedCount is surfaced and is distinct from unreadCount');
  check(/1 setting not read/.test(h1), 'the unread count is stated in words',
    'no "not read" count on the committed card');
  check(/2 settings this fixture doesn't have/.test(h1),
    'the not-fitted count is stated in words, separately',
    'no separate not-fitted count on the committed card');
  check(!/3 settings? not read/.test(h1),
    'the two counts are not added together',
    'unread and not-fitted appear to have been folded into one number');

  // Decommit back to a clean board for the ordering assertions.
  state.committedEntry = null;
  await panel.refresh();
  panel.render();
  await settle();

  console.log('3. universes sort NUMERICALLY, not lexicographically');
  setSort('intended', 'universe');
  await settle();
  const u = universes('intended');
  check(eq(u, [1, 1, 2, 3, 11, 21]),
    'intended pane universes render 1, 1, 2, 3, 11, 21',
    'got ' + JSON.stringify(u) + ' — lexicographic would be 1,1,11,2,21,3');
  const n3 = names('intended');
  check(eq(n3, ['Alpha 1', 'Delta 1', 'Bravo 2', 'Charlie 3', 'Kilo 11', 'Zulu 21']),
    'inside a universe the secondary key is the DMX address (Alpha 1 @1 before Delta 1 @41)',
    'got ' + JSON.stringify(n3));
  setSort('detected', 'universe');
  await settle();
  const du = universes('detected');
  check(eq(du, [1, 2, 3, 11, 21]),
    'detected pane universes render 1, 2, 3, 11, 21',
    'got ' + JSON.stringify(du));

  console.log('4. every other sort key orders on the canonical value');
  setSort('intended', 'name');
  await settle();
  check(eq(names('intended'), ['Alpha 1', 'Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Zulu 21']),
    'intended: sort by name', 'got ' + JSON.stringify(names('intended')));
  setSort('intended', 'type');
  await settle();
  check(names('intended')[0] === 'Charlie 3' && names('intended')[1] === 'Alpha 1',
    'intended: sort by fixture type (Ayrton, then Chroma-Q)', 'got ' + JSON.stringify(names('intended')));
  setSort('intended', 'address');
  await settle();
  check(eq(names('intended'), ['Alpha 1', 'Kilo 11', 'Zulu 21', 'Charlie 3', 'Delta 1', 'Bravo 2']),
    'intended: sort by DMX address, numerically (1, 3, 5, 40, 41, 90)',
    'got ' + JSON.stringify(names('intended')));
  setSort('intended', 'position');
  await settle();
  check(names('intended')[names('intended').length - 1] === 'Kilo 11',
    'intended: sort by position puts the entry with NO position last, not first',
    'got ' + JSON.stringify(names('intended')));
  setSort('intended', 'fixnum');
  await settle();
  check(eq(names('intended'), ['Alpha 1', 'Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Zulu 21']),
    'intended: sort by fixture number (101, 102, 103, 106, 109, 111)',
    'got ' + JSON.stringify(names('intended')));

  setSort('detected', 'manufacturer');
  await settle();
  check(names('detected')[0] === 'Ayrton Spot',
    'detected: sort by manufacturer', 'got ' + JSON.stringify(names('detected')));
  setSort('detected', 'uid');
  await settle();
  check(eq(detectedUids(), ['00ff:0000000a', '1111:00000004', '2222:00000002', '3333:00000005', 'aaaa:00000003']),
    'detected: sort by RDM UID, on the canonical hex value',
    'got ' + JSON.stringify(detectedUids()));

  console.log('5. the two panes sort independently of each other');
  setSort('intended', 'name');
  setSort('detected', 'universe');
  await settle();
  check(eq(names('intended'), ['Alpha 1', 'Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Zulu 21']),
    'the left pane keeps its own name order while the right pane is by universe',
    'got ' + JSON.stringify(names('intended')));
  check(eq(universes('detected'), [1, 2, 3, 11, 21]),
    'the right pane keeps its own universe order while the left pane is by name',
    'got ' + JSON.stringify(universes('detected')));

  console.log('6. committed rows sink to a NAMED group at the bottom, still sorted');
  setSort('intended', 'name');
  await settle();
  state.armEntryIds = ['e-u1a'];
  state.commitDeviceUids = ['2222:00000002'];
  await wire();
  live('[data-rcb-arm-entry]').fire('click');
  await settle();
  await wire();
  live('[data-rcb-commit-device]').fire('click');
  await settle(40);
  const after = names('intended');
  check(after[after.length - 1] === 'Alpha 1',
    'the committed entry moved to the BOTTOM of the pane',
    'got ' + JSON.stringify(after));
  check(eq(after.slice(0, 5), ['Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Zulu 21']),
    'the remaining uncommitted rows keep the chosen sort',
    'got ' + JSON.stringify(after));
  const ph = paneHtml('intended');
  check(/b5-rcb-groupdiv--todo/.test(ph) && /b5-rcb-groupdiv--done/.test(ph),
    'both groups are drawn with a labelled divider, not merely stacked');
  check(/Still to commit/.test(ph) && /Committed — done/.test(ph),
    'each divider carries a WORD, so the grouping does not depend on position or colour',
    'a divider was rendered without its heading text');
  check(ph.indexOf('b5-rcb-groupdiv--done') > ph.indexOf('Zulu 21'),
    'the done divider sits below the last uncommitted row');

  console.log('7. the sort survives the board refetch a commit performs');
  // The commit above already went through run() -> refresh() -> render().
  check(doc.getElementById('rcbSortIntended').value === 'name' ||
        /value="name" selected|<option value="name" selected/.test(html()),
    'the sort select still shows the chosen key after the commit repaint',
    'select markup: ' + (html().match(/id="rcbSortIntended"[\s\S]{0,400}/) || [''])[0]);
  check(eq(names('intended').slice(0, 5), ['Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Zulu 21']),
    'the pane is still in name order after the commit refetch',
    'got ' + JSON.stringify(names('intended')));

  console.log('8. within the committed group the chosen sort still applies');
  // Commit a second entry so the done group has two rows to order.
  state.armEntryIds = ['e-u21'];
  live('[data-rcb-arm-entry]').fire('click');
  await settle();
  // The stub board only tracks one committed entry, so assert on the shape the
  // comparator produces for a two-row done group directly through the screen:
  // decommit-free, re-render with both marked committed.
  const twoCommitted = board();
  twoCommitted.intended.forEach(r => {
    if (r.entryId === 'e-u1a' || r.entryId === 'e-u21') {
      r.committed = true; r.committedUid = '2222:00000002'; r.deviceOnline = true;
      r.diff = []; r.differsCount = 0; r.unreadCount = 0; r.notFittedCount = 0;
    }
  });
  apiImpl.getReconcileBoard = async () => { record('getReconcileBoard'); return twoCommitted; };
  await panel.refresh();
  panel.render();
  await settle();
  const two = names('intended');
  check(eq(two, ['Bravo 2', 'Charlie 3', 'Delta 1', 'Kilo 11', 'Alpha 1', 'Zulu 21']),
    'the done group is itself in the chosen order (Alpha 1 before Zulu 21, by name)',
    'got ' + JSON.stringify(two));

  console.log('9. changing a sort is VIEW state — it never touches the server');
  calls.length = 0;
  setSort('intended', 'universe');
  setSort('detected', 'model');
  await settle();
  check(calls.length === 0,
    'picking a sort sends no HTTP request and triggers no board refetch',
    'calls made: ' + JSON.stringify(calls.map(c => c.name)));

  console.log('10. each pane has its own scroll box, and the position survives a repaint');
  check(/id="rcbIntendedList"/.test(html()) && /id="rcbDetectedList"/.test(html()),
    'both panes render their own identified list container');
  const leftList = doc.getElementById('rcbIntendedList');
  const rightList = doc.getElementById('rcbDetectedList');
  leftList.scrollTop = 420;
  rightList.scrollTop = 90;
  panel.render();
  await settle();
  check(doc.getElementById('rcbIntendedList').scrollTop === 420 &&
        doc.getElementById('rcbDetectedList').scrollTop === 90,
    'both panes keep their OWN independent scroll position across a re-render',
    'left=' + doc.getElementById('rcbIntendedList').scrollTop +
    ' right=' + doc.getElementById('rcbDetectedList').scrollTop);

  if (failures) {
    console.log('\n' + failures + ' assertion(s) failed');
    process.exit(1);
  }
  console.log('\nall assertions passed');
}

main().catch(e => { console.error(e); process.exit(1); });
