// console_i2f_test.js — Console-lite I2f, component-specs §16: the masked
// test note ("overridden by SET on n fixtures") and Isolate, run as the
// browser runs them: the real index.html in minidom.js and the LITERAL
// scripts, against a real Benny512 server (internal/web/console_i2f_test.go):
// the 4-cell RGB batten BT (master dimmer) and the LED par PAR.
//
// usage: node console_i2f_test.js <jsDir> <baseURL> <idsJSON>
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
// phone: matchMedia('(max-width: 767px)') answers true (the phone layout:
// the action bar's tools are in More, the Tests panel is a sheet).
let phone = false;
const matchMedia = qq => ({ matches: phone && /max-width:\s*767px/.test(qq), addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} });
window.matchMedia = matchMedia;
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle, matchMedia,
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
for (const f of ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'workspace.js', 'console-controls.js', 'console-tests.js', 'console-midi.js', 'console.js']) {
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
const NB = ' ';
const kd = (el, key, extra) => el.dispatchEvent(new Event('keydown', Object.assign({ bubbles: true, key }, extra || {})));
const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
const input = (el, v, type) => { el.value = String(v); el.dispatchEvent(new Event(type || 'input', { bubbles: true })); };
const qa = sel => document.querySelectorAll(sel);
const held = (name, n) => { const a = attr(name); const c = a && a.channels.find(x => x.entryId === ids[n]); return c ? c.value : undefined; };
const serverSet = async body => {
  const r = await server('POST', SET, body);
  if (r.status !== 200) check(false, 'direct server write ' + JSON.stringify(body), r);
  await until('the browser to see revision ' + (r.body && r.body.revision), () => PS().revision() >= r.body.revision);
  await sleep(700);
  get('ConsoleControls').refresh();
};
const tab = async k => { click(q('[data-tab-group="' + k + '"]')); await until('the ' + k + ' panel', () => q('[data-panel="' + k + '"]'), 2000); };
const sect = async (name, fn) => { try { await fn(); } catch (e) { check(false, name + ': ' + (e && e.stack || e)); } };
const iconOf = el => (el ? el.querySelectorAll('.b5-con-ic').map(i => i.innerHTML).join(' ') : '').match(/#b5-icon-([a-z-]+)/g) || [];




const TT = '/api/tests/';
const view = async () => (await server('GET', '/api/tests')).body;

(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));
  await until('the Tests panel to mount', () => q('[data-tests-toggle]'));

  // --- §16 masked test: stays ON and says what overrides it ---------------------------
  await sect('masked', async () => {
    await server('POST', '/api/output/arm', { client: 'node-i2f' });
    await select('BT', 'PAR');
    if (q('[data-tests-toggle]').getAttribute('aria-expanded') !== 'true') click(q('[data-tests-toggle]'));
    await until('the dimmer tile', () => q('[data-test-tile="dimmer_toggle"]'));
    click(q('[data-test-toggle="dimmer_toggle"]'));
    await until('the dimmer test ON', () => /ON/.test(q('[data-test-tile="dimmer_toggle"] .b5-tile__stateword').textContent));
    check(!q('[data-test-masked]'), 'nothing overrides the test: no masked note');
    await serverSet({ targets: [T('BT'), T('PAR')], attribute: 'Dimmer', dmx: 40 });
    await until('the masked note', () => q('[data-test-masked="dimmer_toggle"]'));
    const note = q('[data-test-masked="dimmer_toggle"]');
    check(/Overridden by SET on 2 fixtures/.test(note.textContent), 'the masked test says "Overridden by SET on 2 fixtures"', note.textContent);
    check(/ON/.test(q('[data-test-tile="dimmer_toggle"] .b5-tile__stateword').textContent), 'a masked test stays ON');
    check(iconOf(note).includes('#b5-icon-status-warning'), 'the note carries an icon beside its words (never colour alone)', iconOf(note));
    // The per-fixture list names the layer and the attribute.
    click(q('[data-test-more="dimmer_toggle"]'));
    await until('the settings panel', () => q('[data-test-fixtures="dimmer_toggle"]'));
    check(/Where no colour is set, the test shows white/.test(q('[data-test-hint="dimmer_toggle"]').textContent), 'a dimmer test says in words that unset colour shows white', q('[data-test-hint="dimmer_toggle"]').textContent);
    click(q('[data-test-fixtures="dimmer_toggle"]'));
    await until('the per-fixture list', () => qa('[data-fixture-status]').length === 2);
    check(qa('[data-fixture-status]').every(li => /overridden by SET \(Dimmer\)/.test(li.textContent)), 'each fixture says "overridden by SET (Dimmer)"', qa('[data-fixture-status]').map(li => li.textContent));
    // Highlight on: it overrides instead; the panel follows without a reload.
    await PS().act('highlight', { highlight: true });
    await until('the note to name Highlight', () => /Overridden by Highlight on 2 fixtures/.test((q('[data-test-masked="dimmer_toggle"]') || {}).textContent || ''));
    await PS().act('highlight', { highlight: false });
    await PS().act('clear', { scope: 'all' });
    await select('BT', 'PAR');
    await until('the note to go once nothing overrides', () => q('[data-test-tile="dimmer_toggle"]') && !q('[data-test-masked]'));
  });

  // --- §16 Isolate: the caution card, only while Armed and a test is on ------------------
  await sect('isolate', async () => {
    const card = () => q('[data-tests-isolate-card]');
    await until('the Isolate card', () => card());
    check(card().classList.contains('b5-choicecard--caution') && card().tagName === 'LABEL', 'Isolate is the kit\'s caution choice card (a label with its checkbox)');
    check(/Isolate: drive only the tested channel — OFF/.test(card().textContent) && !q('[data-tests-isolate]').disabled, 'Armed with a test on: Isolate OFF and offered', card().textContent);
    check(/held at zero/.test(card().textContent), 'the card explains what Isolate does in words');
    q('[data-tests-isolate]').dispatchEvent(new Event('change', { bubbles: true }));
    await until('Isolate ON', () => /— ON/.test(card().textContent) && card().classList.contains('is-on'));
    const post = last('/api/tests/isolate');
    check(post && post.body.on === true && typeof post.body.client === 'string' && post.body.client === get('Workspace.client()') && post.body.client.length > 0,
      'turning it on sends this page\'s lease client id (so it ends when this browser leaves)', post && post.body);
    check((await view()).isolate.on === true, 'the server has Isolate on');
    check(iconOf(card()).includes('#b5-icon-status-warning'), 'ON carries the caution icon as well as the word', iconOf(card()));
    // Disarm ends it, everywhere, with the reason in words; it is then not offered.
    await server('POST', '/api/output/disarm', {});
    await until('the card to say Isolate ended', () => /Isolate ended by itself: output was disarmed\./.test(card().textContent));
    check(/— OFF/.test(card().textContent) && q('[data-tests-isolate]').disabled && /Output is not live: Arm first/.test(card().textContent),
      'after Disarm: OFF, not offered, and why in words', card().textContent);
    check((await view()).isolate.on === false, 'the server has Isolate off after Disarm');
    // Re-armed: offered again, OFF (never resumed by itself).
    await server('POST', '/api/output/arm', { client: 'node-i2f' });
    window.dispatchEvent(new CustomEvent('b5-output', { detail: { state: 'armed' } }));
    await until('offered again once Armed', () => !q('[data-tests-isolate]').disabled);
    check(/— OFF/.test(card().textContent), 're-armed: Isolate stays OFF');
    // Every test off: not offered.
    click(q('[data-tests-clear]'));
    await until('no test on', () => q('[data-tests-isolate]') && q('[data-tests-isolate]').disabled);
    check(/Turn a test on first/.test(card().textContent), 'with no test on, the card says to turn one on first', card().textContent);
  });

  if (thrown.length) check(false, 'nothing threw', thrown);
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})();
