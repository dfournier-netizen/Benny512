// nodes_fault_test.js — the Nodes screen's node-fault flag, run against the
// LITERAL nodes.js and ui.js the browser loads.
//
// RDM-LOG36: node 2.11.90.6 sent four 239-byte datagrams framed as ArtRdm
// whose body was ArtPollReply content; the RDM decoder rejected every one
// ("rdm: checksum mismatch"). That is the node's fault, not a fixture's, and
// Dom asked for it to be flagged on the node itself. The server carries it as
// nodeJSON.fault (absent = nothing recorded); this pins that nodes.js turns
// it into a worded pill on the node card and the server's sentence in the
// editor — including when the fault arrives AFTER the editor was opened,
// which is the background-refresh path (renderInfoStatic, not the shell).
//
// Run: node nodes_fault_test.js  (also run by go test via
// internal/web/nodes_fault_test.go)

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
    classList: { toggle() {} },
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

const NOTE = 'This node has sent 4 malformed RDM packets (last at 15:22:10). ' +
  'That is the node\'s firmware or its own RDM handling, not a fixture — ' +
  'power-cycling or updating the node is the next step.';
const fault = { malformed: 4, first: '2026-10-06T15:22:10Z', last: '2026-10-06T15:22:10Z',
  lastError: 'rdm: checksum mismatch', note: NOTE };
const healthy = { ip: '2.11.90.5', bindIndex: 1, shortName: 'Healthy node', rdmCapable: true,
  ports: [{ index: 0, output: true, outputAddress: 0 }] };
const faulted = { ip: '2.11.90.6', bindIndex: 1, shortName: 'Faulted node', rdmCapable: true,
  ports: [{ index: 0, output: true, outputAddress: 0 }], fault };

console.log('1. a node with a recorded fault says so on its card, in words');
Nodes.__setNodesForTest([healthy, faulted]);
Nodes.__selectPortForTest('2.11.90.5|1', 0, '2.11.90.5');
const acc = store.nodesAccordion.innerHTML;
const cardOf = ip => acc.split('<button').find(c => c.includes('data-node-ip="' + ip + '"')) || '';
check(cardOf('2.11.90.6').includes('Node fault · malformed RDM'),
  'faulted node card carries the "Node fault · malformed RDM" pill', cardOf('2.11.90.6'));
check(/b5-pill--warn/.test(cardOf('2.11.90.6')) && cardOf('2.11.90.6').includes('status-warning'),
  'the fault pill has a tone AND an icon, not colour alone', cardOf('2.11.90.6'));
check(!cardOf('2.11.90.6').includes('RDM capable'),
  'a faulted node is not also reported as plainly "RDM capable"', cardOf('2.11.90.6'));
check(!cardOf('2.11.90.5').includes('Node fault') && cardOf('2.11.90.5').includes('RDM capable'),
  'a healthy node beside it carries no fault pill', cardOf('2.11.90.5'));

console.log('2. stale still outranks the fault (a silent node is the bigger problem)');
Nodes.__setNodesForTest([{ ...faulted, stale: true }]);
Nodes.__selectPortForTest('2.11.90.6|1', 0, '2.11.90.6');
check(store.nodesAccordion.innerHTML.includes('Stale · stopped answering'),
  'stale wins over fault', store.nodesAccordion.innerHTML);

console.log('3. the editor shows the server\'s sentence verbatim');
const faultNote = () => (store.nodeFaultNote ? store.nodeFaultNote.innerHTML : '');
Nodes.__setNodesForTest([healthy, faulted]);
Nodes.__selectPortForTest('2.11.90.5|1', 0, '2.11.90.5');
Nodes.__selectPortForTest('2.11.90.6|1', 0, '2.11.90.6');
check(store.nodeDetail.innerHTML.includes('id="nodeFaultNote"'),
  'the editor shell has a place for the fault note', store.nodeDetail.innerHTML.slice(0, 400));
check(faultNote() === '<p class="b5-note">' + NOTE.replace(/'/g, '&#39;') + '</p>' ||
      faultNote() === '<p class="b5-note">' + NOTE + '</p>',
  'fault note rendered verbatim as a b5-note', JSON.stringify(faultNote()));
Nodes.__selectPortForTest('2.11.90.5|1', 0, '2.11.90.5');
check(faultNote() === '', 'a healthy node\'s editor shows no fault note', JSON.stringify(faultNote()));

console.log('4. a fault that arrives after the editor is open still appears');
Nodes.__setNodesForTest([healthy, { ...faulted, fault: undefined }]);
Nodes.__selectPortForTest('2.11.90.6|1', 0, '2.11.90.6');
check(faultNote() === '', 'no note while nothing is recorded', JSON.stringify(faultNote()));
// Same selection, new data: render() takes the refresh path, not the shell.
Nodes.__setNodesForTest([healthy, faulted]);
Nodes.__selectPortForTest('2.11.90.6|1', 0, '2.11.90.6');
check(faultNote().includes('malformed RDM packets'), 'refresh path fills the fault note', JSON.stringify(faultNote()));
check(store.nodesAccordion.innerHTML.includes('Node fault · malformed RDM'), 'and the card pill follows');
// And clears again if the server stops reporting it (a new session).
Nodes.__setNodesForTest([healthy, { ...faulted, fault: undefined }]);
Nodes.__selectPortForTest('2.11.90.6|1', 0, '2.11.90.6');
check(faultNote() === '', 'refresh path clears the note when the fault is gone', JSON.stringify(faultNote()));

console.log(failures ? `\n${failures} assertion(s) failed.` : '\nall assertions passed');
process.exit(failures ? 1 : 0);
