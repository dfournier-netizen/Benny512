// faders_j1_test.js — J1 (owner 2026-10-12) SUB-FIXTURE FADERS in the bar,
// run as the browser runs it: the real index.html in minidom.js and the
// LITERAL api.js, ws.js, ui.js, workspace.js and faders.js against a real
// Benny512 server on the J1 rig (internal/web/intensity_j1_test.go:
// synthetic JDC1-, Rayzor-, ERA- and Paladin-like profiles). A type with
// several intensity parts is drawn as one labelled cluster: its MASTER
// fader first, then one fader per part, each named "<type> · <part>" for
// assistive technology; a type with one part keeps its one plain fader.
// J1b (owner 2026-10-12 20:11): the bar SHOWS the short type name
// (shortLabel: the GDTF short name, else derived); the full name stays in
// every accessible label and tooltip.
//
// usage: node faders_j1_test.js <jsDir> <baseURL>
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { makeWindow, Event } = require('./minidom.js');
const [jsDir, base] = process.argv.slice(2);

let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + (typeof detail === 'string' ? detail : JSON.stringify(detail)) : ''));
}
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
window.localStorage.setItem('benny512.faders.collapsed', '0');
const url = new URL(base);
const calls = [];
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date, Uint8Array,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle, crypto: globalThis.crypto,
  LibraryPanel: { open() {} },
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p), body: opts.body ? JSON.parse(opts.body) : undefined };
    calls.push(rec);
    const res = await fetch(new URL(p, base), opts);
    rec.status = res.status;
    return res;
  },
};
const ctx = vm.createContext(sandbox);
for (const f of ['api.js', 'ws.js', 'ui.js', 'workspace.js', 'faders.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
const get = expr => vm.runInContext(expr, ctx);
const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
const last = p => { const l = posts(p); return l[l.length - 1]; };
const q = sel => document.querySelector(sel);
const qa = sel => document.querySelectorAll(sel);
const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
const key = (el, k) => el.dispatchEvent(new Event('keydown', { bubbles: true, key: k }));
const fader = id => q('[data-gfader-id="' + id + '"]');
const settled = async () => { await sleep(120); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); };
const srvFader = async id => (await server('GET', '/api/faders')).body.faders.find(f => f.id === id);

(async () => {
  get('Workspace').init();
  get('Faders').init();
  const srv = (await server('GET', '/api/faders')).body;
  await until('the faders to load', () => qa('[data-gfader-id]').length === srv.faders.length && srv.faders.length > 0);
  const byLabel = l => srv.faders.find(f => f.kind === 'type' && f.label === l);
  const partsOf = f => srv.faders.filter(p => p.kind === 'part' && p.parent === f.id);
  const jd = byLabel('Test JDC-like strobe'), rz = byLabel('Test Rayzor-like bar'), er = byLabel('Test ERA-like spot'), pc = byLabel('Test Paladin-like cube');
  check(jd && rz && er && pc && jd.hasParts && rz.hasParts && !er.hasParts && !pc.hasParts, 'the server says which types have parts (JD, RZ) and which do not (ER, PC)');
  check(jd.shortLabel === 'JDC-like' && rz.shortLabel === 'Rayzor-like', 'the server gives the GDTF short type names', [jd.shortLabel, rz.shortLabel]);

  // --- a type with parts: one cluster, Master first, then its parts ----------
  for (const [t, want] of [[jd, ['Beam', 'Plate']], [rz, ['Spark LED Strobe Module', 'RGBW Pixel']]]) {
    const cl = q('[data-gfader-cluster="' + t.id + '"]');
    check(cl && cl.closest('[data-faders-section="type"]'), t.label + ': drawn as one cluster in the "Fixture types" section');
    if (!cl) continue;
    check(cl.getAttribute('role') === 'group' && cl.getAttribute('aria-label') === t.label + ' — master and parts', t.label + ': the cluster is a group named in words', cl.getAttribute('aria-label'));
    const head = cl.querySelector('[data-gfader-cluster-label]');
    check(head && head.textContent === t.shortLabel && head.getAttribute('title') === t.label, t.label + ': the cluster heading shows the short type name, the full one as its title', head && [head.textContent, head.getAttribute('title')]);
    const ids = [...cl.querySelectorAll('[data-gfader-id]')].map(e => e.getAttribute('data-gfader-id'));
    const parts = partsOf(t);
    check(same(ids, [t.id, ...parts.map(p => p.id)]) && same(parts.map(p => p.label), want), t.label + ': Master first, then ' + want.join(', '), ids);
    const m = fader(t.id);
    check(m.querySelector('[data-gfader-label]').textContent === 'Master' && m.getAttribute('aria-label') === t.label + ' · Master'
      && m.querySelector('[role="slider"]').getAttribute('aria-label') === t.label + ' · Master dimmer level',
    t.label + ': the master fader says "Master", and its full name to assistive technology', m.getAttribute('aria-label'));
    for (const p of parts) {
      const el = fader(p.id);
      const name = t.label + ' · ' + p.label;
      check(el.querySelector('[data-gfader-label]').textContent === p.label && el.getAttribute('aria-label') === name
        && el.querySelector('[role="slider"]').getAttribute('aria-label') === name + ' dimmer level'
        && el.querySelector('[data-gfader-release]').getAttribute('aria-label') === 'Release ' + name
        && el.querySelector('[data-gfader-label]').getAttribute('title') === name,
      'part fader "' + name + '": its part word on the fader, the full name in its group, slider, Release and title', [el.getAttribute('aria-label'), el.querySelector('[data-gfader-label]').textContent]);
      check(el.querySelector('[data-gfader-value]').textContent === '—', '"' + name + '" starts untouched ("—")');
    }
  }
  // --- a type with one part keeps its one plain fader ------------------------
  for (const t of [er, pc]) {
    const el = fader(t.id);
    check(el && !el.closest('[data-gfader-cluster]') && el.querySelector('[data-gfader-label]').textContent === t.shortLabel && el.getAttribute('aria-label') === t.label
      && el.querySelector('[data-gfader-label]').getAttribute('title') === t.label,
      t.label + ': one plain fader showing its short label, the full name in its group label and title', el && el.getAttribute('aria-label'));
  }
  check(!q('[data-faders-section="group"] [data-gfader-cluster]'), 'no clusters under Groups');

  // --- J1c: a Master over a profile without its geometry tree says so ----
  const legacy = byLabel('Test Rayzor-like bar (legacy)');
  check(legacy && legacy.treeUnknown && !rz.treeUnknown && !jd.treeUnknown, 'the server marks only the legacy Master treeUnknown', [legacy && legacy.treeUnknown, rz.treeUnknown, jd.treeUnknown]);
  if (legacy) {
    const m = fader(legacy.id);
    const note = m && m.querySelector('[data-gfader-tree-note]');
    const words = 'Parts counted as dimmed by the master channel — re-import the GDTF to refine.';
    check(m && m.querySelector('[data-gfader-label]').getAttribute('title').indexOf(words) >= 0 && note && note.textContent === words,
      'the legacy Master says in words, in its tooltip and to assistive technology: "' + words + '"', m && [m.querySelector('[data-gfader-label]').getAttribute('title'), note && note.textContent]);
    check(!fader(rz.id).querySelector('[data-gfader-tree-note]') && !/re-import/.test(fader(rz.id).querySelector('[data-gfader-label]').getAttribute('title')), 'a Master whose tree is known says nothing of it');
  }

  // --- the part and master faders write their own ids ------------------------
  const plate = partsOf(jd).find(p => p.label === 'Plate');
  key(fader(plate.id).querySelector('[role="slider"]'), 'End');
  await settled();
  check(same(last('/api/faders/set').body, { id: plate.id, level: 1 }), 'End on the Plate fader posts {id: its part id, level: 1}', last('/api/faders/set').body);
  check((await srvFader(plate.id)).touched, 'the server holds the Plate fader');
  await until('the Plate fader to read 100 %', () => fader(plate.id).querySelector('[data-gfader-value]').textContent === '100 %');
  key(fader(jd.id).querySelector('[role="slider"]'), 'PageUp');
  await settled();
  check(same(last('/api/faders/set').body, { id: jd.id, level: 0.1 }), 'PageUp on the JD Master posts {id: the type id, level: 0.1}', last('/api/faders/set').body);
  await until('the Master to read 10 % and the Plate to keep 100 %', () => fader(jd.id).querySelector('[data-gfader-value]').textContent === '10 %'
    && fader(plate.id).querySelector('[data-gfader-value]').textContent === '100 %');
  // Another browser moves a part: it shows here over the WebSocket.
  const spark = partsOf(rz)[0];
  await server('POST', '/api/faders/set', { id: spark.id, level: 0.25 });
  await until('another browser\'s SparkLED 25 % to arrive', () => fader(spark.id).querySelector('[data-gfader-value]').textContent === '25 %');
  click(fader(plate.id).querySelector('[data-gfader-release]'));
  await settled();
  check(same(last('/api/faders/release').body, { id: plate.id }) && !(await srvFader(plate.id)).touched, 'Release on the Plate fader releases that part only');
  check((await srvFader(jd.id)).touched, 'the Master stays moved');
  click(q('[data-faders-release-all]'));
  await settled();
  check((await server('GET', '/api/faders')).body.faders.every(f => !f.touched), 'Release all releases masters and parts');

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
