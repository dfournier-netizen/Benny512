// faders_test.js — the always-visible group-fader bar (Console-lite G3) run
// as the browser runs it: the real index.html in minidom.js and the LITERAL
// api.js, ws.js, ui.js, workspace.js (its heartbeat and ARM button) and
// faders.js, against a real Benny512 server with real vendor GDTF profiles
// (internal/web/faders_g3_test.go: BMFL x2, LEDBeam, Paladin Cube in two
// modes, an unprofiled fixture, a spec-derived RGB-only fixture). Every
// request is recorded with its answer; checks are on exact payloads and on
// what the server holds afterwards.
//
// usage: node faders_test.js <jsDir> <baseURL> <idsJSON>
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
async function server(method, p, body, headers) {
  const r = await fetch(new URL(p, base), { method, headers: Object.assign({ 'Content-Type': 'application/json' }, headers || {}), body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  return { status: r.status, body: t ? JSON.parse(t) : null };
}

const { window, document, CustomEvent } = makeWindow();
document.loadHTML(fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8'));
// The collapsed state is remembered per browser: start collapsed, as a
// browser that hid the bar last time would.
window.localStorage.setItem('benny512.faders.collapsed', '1');
const url = new URL(base);
const calls = [];
let hook = null; // (rec) => Response | null: lets a check stand in for the server
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date, Uint8Array,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle, crypto: globalThis.crypto,
  LibraryPanel: { open() {} },
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p), headers: Object.assign({}, opts.headers || {}), body: opts.body ? JSON.parse(opts.body) : undefined };
    calls.push(rec);
    const fake = hook && hook(rec);
    const res = fake || await fetch(new URL(p, base), opts);
    rec.status = res.status;
    rec.res = await res.clone().text();
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
const SET = '/api/faders/set';
const q = sel => document.querySelector(sel);
const qa = sel => document.querySelectorAll(sel);
const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
const key = (el, k) => el.dispatchEvent(new Event('keydown', { bubbles: true, key: k }));
const fader = id => q('[data-gfader-id="' + id + '"]');
const settled = async () => { await sleep(120); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); };
const srvFader = async id => (await server('GET', '/api/faders')).body.faders.find(f => f.id === id);

(async () => {
  const appJS = fs.readFileSync(path.join(jsDir, 'app.js'), 'utf8');
  const html = fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8');
  check(/<script src="\/js\/faders\.js"><\/script>/.test(html) && /href="\/css\/faders\.css"/.test(html) && q('#faderBar'), 'index.html loads faders.js and faders.css and has the #faderBar mount');
  check(/Faders\.init\(\)/.test(appJS), 'app.js starts the fader bar with the other modules');

  get('Workspace').init();
  get('Faders').init();
  const srv = (await server('GET', '/api/faders')).body;
  await until('the faders to load', () => qa('[data-gfader-id]').length === srv.faders.length && srv.faders.length > 0);

  // --- collapsed state is remembered ------------------------------------------
  const toggle = q('[data-faders-toggle]');
  check(toggle && /Faders/.test(toggle.textContent) && /show/.test(toggle.textContent) && toggle.getAttribute('aria-expanded') === 'false' && q('[data-faders-body]').hidden,
    'a browser that hid the bar starts collapsed: "Faders · show", aria-expanded=false, body hidden', toggle && toggle.textContent);
  click(toggle);
  check(/hide/.test(toggle.textContent) && toggle.getAttribute('aria-expanded') === 'true' && !q('[data-faders-body]').hidden && window.localStorage.getItem('benny512.faders.collapsed') === '0',
    'show: expanded, "Faders · hide", remembered as 0', [toggle.textContent, window.localStorage.getItem('benny512.faders.collapsed')]);

  // --- one fader per type+mode, labels, counts, words -----------------------
  const types = srv.faders.filter(f => f.kind === 'type');
  check(qa('[data-faders-section="type"] [data-gfader-id]').length === types.length && types.length === 6, 'one fader per fixture type + mode under the "Fixture types" heading (6)', types.map(f => f.label));
  check(/Fixture types/.test(q('[data-faders-section="type"]').textContent), 'the type section has its heading in words');
  for (const f of types) {
    const el = fader(f.id);
    const label = el && el.querySelector('[data-gfader-label]');
    check(label && label.textContent === f.label, 'fader ' + f.label + ': its label', label && label.textContent);
    check(el && el.querySelector('[data-gfader-count]').textContent === f.count + (f.count === 1 ? ' fixture' : ' fixtures'), 'fader ' + f.label + ': its count in words', el && el.querySelector('[data-gfader-count]').textContent);
    check(el && el.querySelector('[data-gfader-value]').textContent === '—' && /untouched/.test(el.querySelector('[role="slider"]').getAttribute('aria-valuetext')), 'fader ' + f.label + ': untouched shows "—" (and says untouched)');
  }
  const pal = types.filter(f => /Cells 24CH|RGB 3CH/.test(f.label));
  check(pal.length === 2, 'the Paladin in two modes = two faders, each named with its mode', pal.map(f => f.label));
  const gen = types.find(f => f.notControllable.length > 0);
  check(gen && /1 not controllable/.test(fader(gen.id).textContent), 'a fader with a fixture it cannot dim says so in words ("1 not controllable")', gen && fader(gen.id).textContent);
  const bmfl = types.find(f => f.count === 2);
  check(!/not controllable/.test(fader(bmfl.id).textContent), 'a fully controllable fader says nothing about it');

  // --- disarmed hint, follows the ARM button --------------------------------
  const hint = () => q('[data-faders-hint]');
  await until('the disarmed hint', () => hint() && !hint().hidden && /nothing reaches the rig until ARM/.test(hint().textContent));
  check(/Faders move the programmer-level dimmer; nothing reaches the rig until ARM/.test(hint().textContent), 'disarmed: the one-line hint is shown', hint().textContent);
  await until('the ARM button', () => q('[data-arm]'));
  click(q('[data-arm]'));
  await until('armed: the hint to go', () => hint().hidden);
  check(hint().hidden, 'pressing ARM (workspace.js) hides the hint');

  // --- drag: throttled, latest wins, final value on release -----------------
  const track = fader(bmfl.id).querySelector('[data-gfader-track]');
  track.getBoundingClientRect = () => ({ top: 0, bottom: 100, height: 100, left: 0, right: 30, width: 30, x: 0, y: 0 });
  const p0 = posts(SET).length;
  track.dispatchEvent(new Event('pointerdown', { bubbles: true, clientY: 100, pointerId: 1 }));
  for (let y = 99; y >= 20; y--) track.dispatchEvent(new Event('pointermove', { bubbles: true, clientY: y, pointerId: 1 }));
  const burst = posts(SET).length - p0;
  check(burst <= 2, '80 pointer moves in one burst send at most 2 writes at once (≤30/s, latest wins)', burst);
  const t0 = Date.now();
  for (let y = 20; y <= 60; y++) { track.dispatchEvent(new Event('pointermove', { bubbles: true, clientY: y, pointerId: 1 })); await sleep(5); }
  const span = Date.now() - t0;
  track.dispatchEvent(new Event('pointerup', { bubbles: true, clientY: 50, pointerId: 1 }));
  await settled();
  const drag = posts(SET).slice(p0);
  check(drag.length <= Math.ceil((span + 160) / 33) + 2, 'writes during the drag stay under 30 per second', { writes: drag.length, ms: span });
  check(same(last(SET).body, { id: bmfl.id, level: 0.5 }), 'release sends the final value: {id, level: 0.5}', last(SET).body);
  check(drag.every(c => c.headers['X-Benny-Faders'] !== undefined && c.headers['X-Benny-Show'] !== undefined), 'every write carries X-Benny-Faders and the show token');
  const sb = await srvFader(bmfl.id);
  check(sb.touched && sb.level === 0.5, 'the server holds the BMFL fader at 0.5', sb);
  await until('the value to read 50 %', () => fader(bmfl.id).querySelector('[data-gfader-value]').textContent === '50 %');

  // --- keyboard steps ---------------------------------------------------------
  const pf = pal[0];
  // Re-queried every time: a rebuild (a new group fader) replaces elements.
  const sliderOf = () => fader(pf.id).querySelector('[role="slider"]');
  const slider = sliderOf();
  check(slider.getAttribute('tabindex') === '0', 'the fader is keyboard-focusable (role=slider, tabindex 0)');
  const steps = [['ArrowUp', 0.01], ['ArrowUp', 0.02], ['ArrowRight', 0.03], ['ArrowDown', 0.02], ['PageUp', 0.12], ['PageDown', 0.02], ['End', 1], ['PageUp', 1], ['ArrowLeft', 0.99], ['Home', 0], ['ArrowDown', 0]];
  for (const [k, want] of steps) {
    key(sliderOf(), k);
    await settled();
    const f = await srvFader(pf.id);
    check(f.touched && Math.abs(f.level - want) < 1e-9, 'key ' + k + ' → ' + Math.round(want * 100) + ' %', f);
  }
  check(sliderOf().getAttribute('aria-valuenow') === '0' && fader(pf.id).querySelector('[data-gfader-value]').textContent === '0 %', 'the slider reads 0 % after Home');

  // --- WebSocket: another browser's change shows here ------------------------
  const other = types.find(f => f.count === 1 && f.notControllable.length === 0 && f.id !== pf.id);
  await server('POST', SET, { id: other.id, level: 0.25 });
  await until('another browser\'s 25 % to arrive over the WebSocket', () => fader(other.id).querySelector('[data-gfader-value]').textContent === '25 %');
  const sel = await server('POST', '/api/programmer/select', { action: 'set', targets: [{ entryId: ids.P1, cell: 'Beam 2:0' }, { entryId: ids.B2, cell: '' }] });
  const st = await server('POST', '/api/programmer/groups/store', { name: 'Front wash' });
  check(sel.status === 200 && st.status === 200, 'a group is stored from another browser');
  await until('the new group fader under "Groups"', () => qa('[data-faders-section="group"] [data-gfader-id]').length === 1);
  const gsec = q('[data-faders-section="group"]');
  check(/Groups/.test(gsec.textContent) && /Front wash/.test(gsec.textContent) && /2 fixtures/.test(gsec.textContent), 'the group fader appears under its own "Groups" heading with its name and count', gsec.textContent);

  // --- Release, Release all -------------------------------------------------
  click(fader(bmfl.id).querySelector('[data-gfader-release]'));
  await settled();
  check(same(last('/api/faders/release').body, { id: bmfl.id }), 'Release posts {id}', last('/api/faders/release').body);
  check(!(await srvFader(bmfl.id)).touched, 'the server released the BMFL fader');
  await until('the BMFL fader to read "—"', () => fader(bmfl.id).querySelector('[data-gfader-value]').textContent === '—');
  click(q('[data-faders-release-all]'));
  await settled();
  check(posts('/api/faders/release-all').length === 1, 'Release all posts /api/faders/release-all');
  check((await server('GET', '/api/faders')).body.faders.every(f => !f.touched), 'every fader is released on the server');
  await until('every fader to read "—"', () => [...qa('[data-gfader-value]')].every(e => e.textContent === '—'));

  // --- a stale refusal is re-read and retried once ---------------------------
  let fakes = 1;
  hook = rec => (rec.method === 'POST' && rec.path === SET && fakes-- > 0) ? new Response(JSON.stringify({ error: 'The faders changed in another browser since this one last looked; refresh and try again.' }), { status: 409, headers: { 'Content-Type': 'application/json' } }) : null;
  const s0 = posts(SET).length;
  key(sliderOf(), 'End');
  await settled();
  await sleep(100);
  const tries = posts(SET).slice(s0);
  check(tries.length === 2 && tries[0].status === 409 && tries[1].status === 200, 'one stale refusal: re-read, retried once, lands', tries.map(t => t.status));
  check((await srvFader(pf.id)).level === 1, 'the retried value is on the server');
  fakes = 5;
  const s1 = posts(SET).length;
  key(sliderOf(), 'Home');
  await settled();
  await sleep(150);
  hook = null;
  check(posts(SET).length - s1 === 2, 'two stale refusals in a row: two attempts, no more', posts(SET).length - s1);
  check(/another browser/.test(q('[data-faders-status]').textContent), 'and the server\'s sentence is shown', q('[data-faders-status]').textContent);

  // --- collapse again ----------------------------------------------------------
  click(toggle);
  check(q('[data-faders-body]').hidden && window.localStorage.getItem('benny512.faders.collapsed') === '1' && /show/.test(toggle.textContent), 'hide: collapsed to the strip and remembered as 1');

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
