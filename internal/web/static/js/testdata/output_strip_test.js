// output_strip_test.js — the master ARM / DISARM control in the persistent
// show-context strip, run against the LITERAL api.js and workspace.js the
// browser loads (internal/web/output_c3_ui_test.go). Zero dependencies.
//
// WHAT IT PROTECTS (owner decisions, Dom 2026-10-06/07)
//  1. The master control is always in the strip, and its state is carried by
//     a WORD and an icon, never colour alone: Disarmed, Armed,
//     Lease lost — holding, Simulated, Output error.
//  2. Arm is the confirm step: one press, no confirm() dialog. Disarm and
//     Stop all output both disarm (and black out, server-side).
//  3. Every connected browser heartbeats the lease, with a stable per-page
//     client id, comfortably inside the server's 5 s window.
//  4. A page unload says goodbye with a keepalive request, so the server can
//     drop this browser from the lease at once; it does NOT stop output by
//     itself (the server decides, from whether other browsers are present).
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const JS_DIR = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail ? '\n        ' + detail : ''));
}

// --- tiny DOM: elements carrying data-* attributes become queryable --------
function el(tag, attrs) {
  const n = {
    tagName: tag, attrs: attrs || {}, textContent: '', className: '', disabled: false,
    handlers: {}, onclick: null, children: [], style: {}, dataset: {},
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
    addEventListener(t, fn) { (this.handlers[t] = this.handlers[t] || []).push(fn); },
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; },
    querySelectorAll(sel) {
      const m = /^\[([\w-]+)\]$/.exec(sel);
      if (!m) return [];
      const out = [];
      const walk = x => { for (const c of x.children) { if (m[1] in c.attrs) out.push(c); walk(c); } };
      walk(this);
      return out;
    },
    appendChild(c) { this.children.push(c); return c; },
    click() { if (this.onclick) return this.onclick({ target: this }); (this.handlers.click || []).forEach(f => f({ target: this })); },
  };
  let html = '';
  Object.defineProperty(n, 'innerHTML', {
    get: () => html,
    set: v => {
      html = String(v);
      n.children = [];
      const re = /<(\w+)((?:\s+[\w-]+(?:="[^"]*")?)*)\s*>([^<]*)/g;
      let mm;
      while ((mm = re.exec(html))) {
        const a = {};
        for (const am of mm[2].matchAll(/([\w-]+)(?:="([^"]*)")?/g)) a[am[1]] = am[2] === undefined ? '' : am[2];
        const child = el(mm[1], a);
        child.textContent = mm[3];
        n.children.push(child);
      }
    },
  });
  return n;
}

const root = el('div', { id: 'showContext' });
const listeners = {};
const timers = [];
let confirms = 0;
const calls = [];
let serverState = { state: 'disarmed', simulated: false, error: '', leaseLossAction: 'blackout', browsers: 1, lastDisarm: '' };

function respond(url, method, body) {
  if (url === '/api/context') return { active: true, name: 'Show A', nic: 'eth0', output: false, simulation: false };
  if (url === '/api/output' || url === '/api/output/heartbeat') return serverState;
  if (url === '/api/output/arm') { serverState = Object.assign({}, serverState, { state: 'armed' }); return serverState; }
  if (url === '/api/output/disarm' || url === '/api/output/stop') { serverState = Object.assign({}, serverState, { state: 'disarmed', lastDisarm: 'operator' }); return url === '/api/output/stop' ? { stopped: true } : serverState; }
  if (url === '/api/output/goodbye') return serverState;
  return {};
}

const ctx = vm.createContext({
  console, JSON, Promise, Math, Date, Object, Array, String, Number, Set, Map, Error, URL, URLSearchParams,
  crypto: { randomUUID: () => 'page-uuid-1' },
  sessionStorage: { getItem: () => null, setItem() {} },
  localStorage: { getItem: () => null, setItem() {} },
  confirm: () => { confirms++; return true; },
  CustomEvent: function (t, o) { this.type = t; this.detail = o && o.detail; },
  document: {
    getElementById: id => (id === 'showContext' ? root : null),
    querySelector: () => null,
    createElement: t => el(t),
    body: el('body'),
    visibilityState: 'visible',
    addEventListener(t, fn) { (listeners['doc:' + t] = listeners['doc:' + t] || []).push(fn); },
  },
  window: {
    addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
    dispatchEvent() {},
  },
  setInterval: (fn, ms) => { timers.push({ fn, ms, repeat: true }); return timers.length; },
  setTimeout: (fn, ms) => { timers.push({ fn, ms, repeat: false }); return timers.length; },
  clearTimeout() {}, clearInterval() {},
  UI: { icon: name => `<svg data-icon="${name}"></svg>`, formatUser: n => n + 1 },
  LibraryPanel: { open() {} },
  fetch: async (url, opts) => {
    const body = opts && opts.body ? JSON.parse(opts.body) : undefined;
    calls.push({ url, method: opts && opts.method, body, keepalive: !!(opts && opts.keepalive) });
    const data = respond(url, opts && opts.method, body);
    return { ok: true, status: 200, headers: { get: () => null }, text: async () => JSON.stringify(data) };
  },
});
ctx.window.document = ctx.document;
vm.runInContext(fs.readFileSync(path.join(JS_DIR, 'api.js'), 'utf8') + '\nthis.Api = Api;', ctx, { filename: 'api.js' });
vm.runInContext(fs.readFileSync(path.join(JS_DIR, 'workspace.js'), 'utf8') + '\nthis.Workspace = Workspace;', ctx, { filename: 'workspace.js' });

const settle = () => new Promise(r => setImmediate(r));
async function fireTimers(filter) {
  for (const t of timers.slice()) if (!filter || filter(t)) await t.fn();
  await settle(); await settle();
}
// The output state lives in the strip's [data-outbox], which workspace.js
// re-renders on every heartbeat; read its markup plus every element's text.
const outbox = () => root.querySelector('[data-outbox]');
const stripText = () => {
  const box = outbox();
  if (!box) return root.innerHTML;
  const texts = [];
  const walk = x => { for (const c of x.children) { texts.push(c.textContent); walk(c); } };
  walk(box);
  return box.innerHTML + ' ' + texts.join(' ');
};
const armBtn = () => root.querySelector('[data-arm]');
const outWord = () => root.querySelector('[data-output]');

(async () => {
  ctx.Workspace.init();
  await settle(); await settle();

  check(!!armBtn(), 'the strip carries a master [data-arm] control');
  check(!!root.querySelector('[data-stop]'), 'the strip still carries Stop all output');

  // Heartbeat cadence and identity.
  const beat = timers.find(t => t.repeat && t.ms <= 1000);
  check(!!beat && beat.ms <= 1000, 'a heartbeat timer runs at 1 s or faster (lease is 5 s)', beat ? 'interval ' + beat.ms : 'no timer');
  calls.length = 0;
  await fireTimers(t => t === beat);
  await fireTimers(t => t === beat);
  const beats = calls.filter(c => c.url === '/api/output/heartbeat');
  check(beats.length >= 2 && beats.every(b => b.method === 'POST'), 'the heartbeat POSTs /api/output/heartbeat', JSON.stringify(calls));
  check(beats.length >= 2 && beats[0].body && beats[0].body.client && beats[0].body.client === beats[1].body.client,
    'every heartbeat carries the same non-empty per-page client id', JSON.stringify(beats.map(b => b.body)));
  const client = beats.length ? beats[0].body.client : '';

  // State words, each with an icon.
  const words = [
    [{ state: 'disarmed' }, 'Disarmed', 'ARM'],
    [{ state: 'armed' }, 'Armed', 'DISARM'],
    [{ state: 'holding' }, 'Lease lost — holding', 'ARM'],
    [{ state: 'armed', simulated: true }, 'Simulated', 'DISARM'],
    [{ state: 'armed', error: 'sACN socket failed.' }, 'Output error', 'DISARM'],
  ];
  for (const [st, word, btn] of words) {
    serverState = Object.assign({ simulated: false, error: '', leaseLossAction: 'blackout', browsers: 1, lastDisarm: '' }, st);
    await fireTimers(t => t === beat);
    const text = stripText();
    check(text.includes(word), `state ${JSON.stringify(st)} reads "${word}" in words`, text);
    const box = outbox();
    const boxHtml = box ? box.innerHTML : '';
    const word_ = /<span[^>]*data-output[^>]*>(.*?)<\/span>(?=<button|$)/.exec(boxHtml);
    check(!!word_ && /data-icon=/.test(word_[1]), `state ${JSON.stringify(st)} carries an icon beside the word`, boxHtml);
    const b = armBtn();
    check(!!b && (boxHtml.includes('>' + btn + '<') || boxHtml.includes('</svg>' + btn)), `state ${JSON.stringify(st)}: the master control offers ${btn}`, boxHtml);
  }

  // Arm: one press, no confirm dialog.
  serverState = { state: 'disarmed', simulated: false, error: '', leaseLossAction: 'blackout', browsers: 1, lastDisarm: '' };
  await fireTimers(t => t === beat);
  calls.length = 0;
  await armBtn().click(); await settle(); await settle();
  const armCall = calls.find(c => c.url === '/api/output/arm');
  check(!!armCall && armCall.body && armCall.body.client === client, 'ARM posts /api/output/arm with this page\'s client id', JSON.stringify(calls));
  check(confirms === 0, 'ARM asks no confirm() — Arm IS the confirm step');
  check(stripText().includes('Armed'), 'after ARM the strip reads Armed');

  calls.length = 0;
  await armBtn().click(); await settle(); await settle();
  check(calls.some(c => c.url === '/api/output/disarm' && c.body && c.body.client === client), 'DISARM posts /api/output/disarm', JSON.stringify(calls));

  calls.length = 0;
  await root.querySelector('[data-stop]').click(); await settle(); await settle();
  check(calls.some(c => c.url === '/api/output/stop'), 'Stop all output posts /api/output/stop (which disarms)');

  // Unload: a keepalive goodbye, nothing else.
  calls.length = 0;
  for (const fn of (listeners.pagehide || [])) fn({});
  await settle();
  const bye = calls.find(c => c.url === '/api/output/goodbye');
  check(!!bye && bye.keepalive && bye.body && bye.body.client === client, 'pagehide sends a keepalive goodbye with the client id', JSON.stringify(calls));
  check(!calls.some(c => /stop|disarm/.test(c.url)), 'pagehide does not itself stop or disarm output', JSON.stringify(calls));

  console.log(failures ? `${failures} failure(s)` : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
