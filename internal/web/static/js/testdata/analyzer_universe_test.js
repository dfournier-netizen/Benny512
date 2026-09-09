// analyzer_universe_test.js — the Analyzer screen's universe notation and its
// honest-unknown states, run against the LITERAL analyzer.js and ui.js the
// browser loads.
//
// WHY THIS FILE EXISTS
//
// This is the ninth sighting of one defect class on this project: THE SAME
// VALUE SPELLED TWO WAYS IN TWO FILES. The owner reported it from a bench
// once already, against the Nodes screen — every universe read one lower
// than his Elation EN4 faceplate — and the cause was that nodes.js had 25
// universe references and zero universe-format calls. analyzer.js was in
// exactly that state: SIX universe references, ZERO conversions.
//
// Two of the six were live, user-facing defects:
//
//  (a) the "Universe" column printed capture.Entry.Universe raw. That field
//      is the canonical 0-based Art-Net Port-Address (capture.summarize
//      fills it from pkt.Dmx.PortAddress().RawValue()), so a packet on the
//      faceplate's Universe 1 showed as "0" — Patch, Devices and Nodes all
//      said 1 for the same universe, the Analyzer said 0.
//
//  (b) the universe FILTER read the typed number as a wire value. A tech
//      typing the number printed on his gateway filtered the universe below
//      it, and got back an empty table. At a bench an empty table is
//      indistinguishable from "no traffic on that universe", which is the
//      single conclusion the Analyzer exists to let him draw. This half is
//      the dangerous one: (a) shows you a wrong number, (b) shows you a
//      wrong ANSWER and gives you no reason to doubt it.
//
// A THIRD defect was introduced by the fix for (a) and is pinned in section
// 4 below. capture.Entry.Universe is a plain uint16 with no `omitempty` and
// no pointer ("raw Port-Address value, when applicable; 0 otherwise"), and
// capture.summarize() returns a literal 0 for every kind except
// artnet.KindDmx. So `e.Universe === undefined` can NEVER be true for data
// that came from this server, and a first cut that used that check to mean
// "this packet has no universe" instead ran every ArtPoll, ArtPollReply and
// ArtRdm row through the universe formatter — printing "Universe 1" under the
// default 1-based notation. That is rule 3's "plausible 0" failure with the
// base offset stacked on top: a feed full of rows claiming to be traffic on
// universe 1, when in fact none of them carries a universe at all.
//
// Run: node analyzer_universe_test.js

'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const JS_DIR = path.join(__dirname, '..');

// ---- the smallest DOM/window stub ui.js and analyzer.js need at load time --

function makeEl(id) {
  const el = {
    id, tagName: 'DIV', _innerHTML: '', className: '', dataset: {}, style: {},
    children: [], handlers: {}, value: '', checked: false, scrollTop: 0,
    scrollHeight: 0, clientHeight: 0, textContent: '',
    setAttribute() {}, removeAttribute() {}, getAttribute: () => null,
    appendChild(c) { this.children.push(c); return c; },
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
    removeEventListener() {},
    contains: () => true,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
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
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
};

function CustomEventStub(type, init) { return { type, detail: (init || {}).detail }; }

const ctx = vm.createContext({
  window: win, document: doc, console,
  CustomEvent: CustomEventStub,
  setTimeout, clearTimeout, setInterval, clearInterval,
  Intl, Date, JSON, alert: () => {},
  fetch: async () => ({ ok: true, json: async () => ({}) }),
  Api: new Proxy({}, { get: () => async () => ({}) }),
  Live: { on() {}, off() {} },
  sessionStorage: {
    _v: {},
    getItem(k) { return Object.prototype.hasOwnProperty.call(this._v, k) ? this._v[k] : null; },
    setItem(k, v) { this._v[k] = String(v); },
    removeItem(k) { delete this._v[k]; },
  },
  confirm: () => true, prompt: () => null,
  // escapeHtml is a bare cross-file global declared in nodes.js, a screen
  // this test has no reason to load. Same stub reconcile_view_test.js and
  // patch_lifecycle_test.js use.
  escapeHtml: (s) => (s === undefined || s === null) ? '' : String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c])),
});
ctx.globalThis = ctx;

for (const f of ['ui.js', 'analyzer.js']) {
  vm.runInContext(fs.readFileSync(path.join(JS_DIR, f), 'utf8'), ctx, { filename: f });
}

// A top-level `const` in a vm script does not become a property of the context
// object the way `var` does — reach the bindings by evaluating them inside.
const An = vm.runInContext('AnalyzerScreen', ctx, { filename: 'grab' })._test;
const UI = vm.runInContext('UI', ctx, { filename: 'grab' });

// ---- assertions ---------------------------------------------------------

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

// ---- helpers ------------------------------------------------------------

// entry: one capture.Entry as the server actually marshals it. Note that
// Universe is ALWAYS present — that is the point of section 4.
const entry = (kind, universe, extra) =>
  Object.assign({ Kind: kind, Universe: universe, Dir: 0, Size: 530, Key: '' }, extra || {});

// Strip the markup universeCell wraps its words in, so an assertion reads
// like the tech reads the cell.
const cellText = (e) => String(An.universeCell(e)).replace(/<[^>]*>/g, '').trim();

// ---- the tests ----------------------------------------------------------

console.log('1. the Universe column is the RAW Art-Net Port-Address');
// The Analyzer reports what is ON THE WIRE. It used to apply the global
// display base, which meant a packet capture disagreed with the packet: at
// base 1 an ArtDmx addressed to Port-Address 0 was labelled "universe 1".
// For a screen whose entire job is showing what was transmitted, that is the
// one thing it must not do.
//
// The show's own numbering lives on Patch, Rig Check and Rig Walk. The
// starting universe must not reach this screen at all, which is what the
// second half of each pair checks.
UI.setArtnetStart(0);
check(cellText(entry('ArtDmx', 0)) === '0',
  'an ArtDmx on canonical Port-Address 0 reads "0" — the wire value',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 0))));
check(cellText(entry('ArtDmx', 3)) === '3',
  'canonical 3 reads 3',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 3))));
UI.setArtnetStart(100);
check(cellText(entry('ArtDmx', 0)) === '0',
  'the same packet still reads "0" with a show start of 100 — a capture must never ' +
  'be renumbered by a display setting, or it stops being evidence',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 0))));
check(cellText(entry('ArtDmx', 3)) === '3', 'canonical 3 still reads 3 at start 100');

console.log('2. the FULL 15-bit Port-Address, never the 4-bit Universe nibble');
// Canonical 17 = Net 0, Sub-Net 1, Universe 1. The nibble is 1; the
// Port-Address is 17, and 17 is what the cell must read. A screen printing
// `addr & 0x0F` reads 1 here — defect (b) of the Nodes bench report, which
// never showed on the owner's own rig because his Net and Sub-Net are both 0.
UI.setArtnetStart(0);
check(cellText(entry('ArtDmx', 17)) === '17',
  'Sub-Net 1 / Universe 1 reads 17, not 1 — the nibble is not the universe',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 17))));
check(cellText(entry('ArtDmx', 4096)) === '4096',
  'a Net-1 Port-Address survives the whole 15-bit range',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 4096))));
check(cellText(entry('ArtDmx', 32767)) === '32767',
  'the top of the Art-Net Port-Address range formats without wrapping',
  'got ' + JSON.stringify(cellText(entry('ArtDmx', 32767))));

console.log('3. the filter box takes the same raw Art-Net number the column shows');
// Typing and reading must agree on one screen. Because this screen is raw
// both ways, a tech can copy a number out of the Universe column straight
// into the filter box — which is the whole reason the Analyzer does not
// carry the show offset.
UI.setArtnetStart(0);
check(An.setFilterUniverseFromInput('1') === 1,
  'typing 1 filters wire universe 1 — what the column labels 1',
  'got ' + An.setFilterUniverseFromInput('1'));
check(An.matches(entry('ArtDmx', 1)) === true,
  'and a packet on wire universe 1 is KEPT by that filter',
  'the tech would have seen an empty table and concluded "no traffic"');
check(An.matches(entry('ArtDmx', 0)) === false,
  'while a packet on wire universe 0 is excluded');
UI.setArtnetStart(100);
check(An.setFilterUniverseFromInput('1') === 1,
  'the same keystroke still filters wire universe 1 with a show start of 100 — ' +
  'the show offset must not reach the filter either, or typing what the column ' +
  'shows returns nothing',
  'got ' + An.setFilterUniverseFromInput('1'));
check(An.setFilterUniverseFromInput('') === null,
  'an empty box is "no universe filter", not universe 0');

console.log('4. a packet with no universe SAYS SO — it never borrows universe 1');
// capture.Entry.Universe has no omitempty and is not a pointer, so every one
// of these arrives as a literal 0. A null check cannot distinguish them; the
// packet KIND is the only honest discriminator.
UI.setArtnetStart(1);
for (const k of ['ArtPoll', 'ArtPollReply', 'ArtIpProg', 'ArtIpProgReply', 'ArtTimeCode', 'Note']) {
  const txt = cellText(entry(k, 0));
  check(txt === 'no universe',
    `${k} (marshalled as Universe:0) reads "no universe", not a number`,
    'got ' + JSON.stringify(txt));
}
for (const k of ['ArtRdm', 'ArtRdmSub', 'ArtTodData', 'ArtTodRequest', 'ArtTodControl', 'ArtAddress', 'ArtInput']) {
  const txt = cellText(entry(k, 0));
  check(txt === 'universe not recorded',
    `${k} carries a Port-Address on the wire the capture does not keep — it says that, not "no universe"`,
    'got ' + JSON.stringify(txt));
}
check(!/^\d+$/.test(cellText(entry('ArtPollReply', 0))),
  'NO non-ArtDmx kind ever renders as a bare number at base 1',
  'got ' + JSON.stringify(cellText(entry('ArtPollReply', 0))));

console.log('5. every unknown is a WORD, so it survives greyscale (rule 1 + rule 3)');
for (const e of [entry('ArtPoll', 0), entry('ArtRdm', 0)]) {
  const txt = cellText(e);
  check(txt.length > 1 && /[a-z]/.test(txt),
    `${e.Kind}'s empty state is prose, not a dash or a blank cell`,
    'got ' + JSON.stringify(txt));
}

console.log('6. direction is three states, not two — a Note never claims to be a packet');
const pill = (d) => String(An.dirPill(d)).replace(/<[^>]*>/g, '').trim();
check(pill(1) === 'out', 'DirOut reads "out"', 'got ' + JSON.stringify(pill(1)));
check(pill(0) === 'in', 'DirIn reads "in"', 'got ' + JSON.stringify(pill(0)));
// capture.DirNote(2) marks "an entry that is not a datagram at all" — Size 0,
// no Peer. Folding it into the `in` branch puts a confident blue "in" pill on
// a row that never touched the wire: a state the app would be inventing.
check(pill(2) === 'note',
  'DirNote reads "note" — it is not a datagram and must not be labelled "in"',
  'got ' + JSON.stringify(pill(2)));
check(!/b5-pill--(ok|warn|danger|info|accent)/.test(An.dirPill(2)),
  'and it borrows NO neighbouring state\'s tone — an untoned pill is honest',
  An.dirPill(2));

console.log('7. every pill this screen draws contains a word (there is no icon-only pill)');
for (const d of [0, 1, 2]) {
  check(pill(d).length > 0, `dirPill(${d}) has text, not just an icon`);
}

console.log(failures ? `\n${failures} assertion(s) failed.` : '\nall assertions passed');
process.exit(failures ? 1 : 0);
