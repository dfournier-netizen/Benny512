// console_j1d_test.js — Console-lite J1d: a MIXED readout across channel
// resolutions, run as the browser runs it: the real index.html in
// minidom.js, the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js and console.js, against a real Benny512 server on the
// J1 rig (internal/web/intensity_j1d_test.go). A whole RZ's Dimmer card
// holds its 16-bit head dimmer and its 8-bit virtual master; when they
// genuinely differ the range is drawn in normalized percent across both.
//
// usage: node console_j1d_test.js <jsDir> <baseURL> <idsJSON>
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { makeWindow, Event } = require('./minidom.js');
const [jsDir, base, idsJSON] = process.argv.slice(2);
const ids = JSON.parse(idsJSON);

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + (typeof detail === 'string' ? detail : JSON.stringify(detail)) : ''));
}
// Anything the controls throw (a render inside a promise) is a failure,
// with its message, not a silent half-drawn panel.
const thrown = [];
process.on('unhandledRejection', e => thrown.push(String(e && e.message || e)));
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(what, fn, ms = 4000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try { if (await fn()) return true; } catch (_) { /* not yet */ }
    await sleep(15);
  }
  check(false, 'timed out waiting for ' + what);
  return false;
}
async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  return { status: r.status, body: t ? JSON.parse(t) : null };
}

const { window, document, CustomEvent } = makeWindow();
document.loadHTML(fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8'));
const url = new URL(base);
const calls = [];
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p), body: opts.body ? JSON.parse(opts.body) : undefined };
    calls.push(rec);
    const res = await fetch(new URL(p, base), opts);
    rec.status = res.status;
    rec.res = await res.clone().text();
    return res;
  },
};
const ctx = vm.createContext(sandbox);
for (const f of ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-controls.js', 'console.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
const get = expr => vm.runInContext(expr, ctx);
const PS = () => get('ProgrammerSync');
const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
const last = p => { const l = posts(p); return l[l.length - 1]; };
const SET = '/api/programmer/set';
const q = sel => document.querySelector(sel);
const attr = name => { const s = PS().state(); for (const g of s.groups) for (const a of g.attributes) if (a.attribute === name) return a; return null; };
const T = n => ({ entryId: ids[n], cell: '' });
async function select(...names) {
  await PS().act('select', { action: 'set', targets: names.map(T) });
  await until('the selection ' + names.join('+') + ' to arrive', () => PS().state().selection.length === names.length);
  await sleep(150);
}
const settled = async () => { await sleep(120); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); };
(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));
  const NB = ' ';
  const ro = () => q('[data-readout="Dimmer"]');
  const serverSet = async body => {
    const r = await server('POST', SET, body);
    if (r.status !== 200) check(false, 'direct server write ' + JSON.stringify(body), r);
    await until('the browser to see revision ' + (r.body && r.body.revision), () => PS().revision() >= r.body.revision);
  };
  await select('RZ');
  await until('the Dimmer tab', () => q('[data-tab-group="dimmer"]'));
  q('[data-tab-group="dimmer"]').dispatchEvent(new Event('click', { bubbles: true }));
  await until('the Dimmer card', () => ro());
  // Equal levels: one value, no MIXED (server sameLevel).
  await serverSet({ targets: [T('RZ')], attribute: 'Dimmer', fraction: 0.5 });
  get('ConsoleControls').refresh();
  await until('the 50 % readout', () => /^50 ?%/.test(ro().textContent.trim()) || /50/.test(ro().textContent));
  check(!/MIXED/.test(ro().textContent), 'head 32 768 and virtual master 128 read as one level, not MIXED', ro().textContent);
  // Genuinely different: head 32 768 of 65 535 (50 %), virtual master 122
  // of 255 (47.8 %).
  await serverSet({ targets: [T('RZ')], attribute: 'Dimmer', functionName: 'Master', dmx: 122 });
  get('ConsoleControls').refresh();
  await until('MIXED drawn', () => attr('Dimmer').mixed && /MIXED/.test(ro().textContent));
  check(ro().textContent.includes('MIXED 48–50' + NB + '%'), 'the MIXED range is normalized percent across both resolutions (48–50 %), not raw values on the 16-bit scale', ro().textContent);
  check(/32 768 \/ 65 535/.test(ro().textContent) && /122 \/ 255/.test(ro().textContent), 'each resolution\'s DMX value is said on its own scale', ro().textContent);
  const units = q('[data-readout-units]');
  if (units) {
    units.value = 'dmx';
    units.dispatchEvent(new Event('change', { bubbles: true }));
    await sleep(100);
    check(ro().textContent.includes('MIXED 48–50' + NB + '%'), 'DMX readout units: across resolutions the range stays in percent (raw counts of two scales are not one range)', ro().textContent);
    units.value = 'pct';
    units.dispatchEvent(new Event('change', { bubbles: true }));
  } else check(false, 'the readout units selector exists');
  check(thrown.length === 0, 'nothing threw during the whole run', thrown);
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
