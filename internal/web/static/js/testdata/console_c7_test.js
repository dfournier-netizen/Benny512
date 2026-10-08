// console_c7_test.js — Console-lite C7 run as the browser runs it: the real
// index.html parsed into a small DOM (minidom.js) and the LITERAL api.js,
// ws.js, programmer.js, ui.js, universeidentify.js, console-tests.js,
// console-tools.js, console.js and workspace.js against a real Benny512
// server (started by internal/web/console_c7_test.go): real fetch, real
// WebSocket. Every request the page makes is recorded on its way to the
// server, and after each step GET /__wire (test-only, in the Go test) reports
// the ArtDmx datagrams the real output engine handed the FakeTransport.
//
//  Part 1 — Tools · raw universe (Send parity): the exact /api/dmx payloads
//    a fader, a number box, a park, a page change, a jump, All off, a
//    universe change and a numbering change produce, the live-scrub
//    coalescing, Release all (/api/dmx/stop) — and the wire after each.
//  Part 2 — Tools · Universe Identify on the real server and the wire.
//  Part 3 — Show tools' "use the selection" button opens the Console with
//    that selection (programmer select) instead of the retired Function check.
//
// usage: node console_c7_test.js <jsDir> <baseURL> <idsJSON>
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
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(what, fn, ms = 4000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try { if (fn()) return true; } catch (_) { /* not yet */ }
    await sleep(15);
  }
  check(false, 'timed out waiting for ' + what);
  return false;
}
const indexHTML = fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8');
function memStore() { const m = new Map(); return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) }; }

function browser(files, extra) {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(indexHTML);
  const url = new URL(base);
  const calls = [];
  const sandbox = {
    console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date, Uint8Array,
    WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
    localStorage: memStore(), sessionStorage: memStore(), getComputedStyle: window.getComputedStyle,
    fetch: (p, opts) => {
      opts = opts || {};
      const rec = { method: opts.method || 'GET', path: String(p), headers: Object.assign({}, opts.headers || {}), body: opts.body ? JSON.parse(opts.body) : undefined };
      calls.push(rec);
      return fetch(new URL(p, base), opts);
    },
  };
  Object.assign(sandbox, extra || {});
  window.addEventListener = window.addEventListener.bind(window);
  const ctx = vm.createContext(sandbox);
  for (const f of files) {
    // A missing file is a failure, not a harness crash: the rest still runs.
    if (!fs.existsSync(path.join(jsDir, f))) { check(false, 'the page loads ' + f, 'no such file'); continue; }
    vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
  }
  return { ctx, window, document, calls, get: expr => vm.runInContext(expr, ctx) };
}

async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  if (!r.ok) throw new Error(method + ' ' + p + ': ' + r.status + ' ' + t);
  return t ? JSON.parse(t) : null;
}
// wire: what reached the FakeTransport in the next `ms` of engine ticks,
// keyed by wire universe: {frames, last[512]}.
const wire = (ms) => server('GET', '/__wire?ms=' + (ms || 100));
const frameOf = body => Array.from(Buffer.from(body.channels, 'base64'));

const FILES = ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'universeidentify.js', 'console-tests.js', 'console-tools.js', 'console.js', 'workspace.js'];

(async () => {
  const A = browser(FILES);
  const { document, calls } = A;
  const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
  const lastPost = p => { const l = posts(p); return l[l.length - 1]; };
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const type = (el, v) => { el.value = String(v); el.dispatchEvent(new Event('input', { bubbles: true })); };
  const change = (el, v) => { el.value = String(v); el.dispatchEvent(new Event('change', { bubbles: true })); };
  const tools = () => document.querySelector('[data-tools-region]');
  const q = sel => tools().querySelector(sel);
  const cell = ch => q('[data-tools-grid] .b5-dmx-cell[data-ch="' + ch + '"]');
  const fader = ch => cell(ch).querySelector('.dmx-fader');
  const box = ch => cell(ch).querySelector('.dmx-num');
  const park = ch => cell(ch).querySelector('.dmx-park');
  const DMX = '/api/dmx';
  // settle: wait until the throttle has nothing left to send.
  async function settle() {
    let n = -1;
    while (n !== posts(DMX).length) { n = posts(DMX).length; await sleep(120); }
  }

  await until('the WebSocket and the programmer to load', () => A.get('ProgrammerSync.state()'));
  A.get('ConsoleScreen').onEnterScreen();
  if (await until('the Tools panel to mount', () => tools() && q('[data-tools-toggle]') && q('[data-tools-grid] .b5-dmx-cell'))) await partsOneAndTwo();
  else check(false, 'parts 1 and 2 (raw universe, Universe Identify) cannot run without the Tools panel');
  await partThree();
  await partFour();

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);

  async function partsOneAndTwo() {
  // ---------------------------------------------------------------- part 1
  const side = document.querySelector('.b5-con-pane--side');
  const kids = side.childNodes.filter(n => n.nodeType === 1);
  check(kids.indexOf(tools()) > kids.indexOf(document.querySelector('[data-tests-region]')) && kids.indexOf(document.querySelector('[data-tests-region]')) >= 0,
    'the Tools panel is mounted in the Console side pane, under the Tests panel');
  check(q('[data-tools-toggle]').getAttribute('aria-expanded') === 'false' && /Tools · show/.test(q('[data-tools-toggle]').textContent) && q('[data-tools-body]').hidden,
    'the panel starts collapsed and says so in words (Tools · show, aria-expanded=false)');
  click(q('[data-tools-toggle]'));
  check(!q('[data-tools-body]').hidden && /Tools · hide/.test(q('[data-tools-toggle]').textContent) && q('[data-tools-toggle]').getAttribute('aria-expanded') === 'true', 'the toggle opens it (Tools · hide)');
  check(posts(DMX).length === 0, 'mounting and opening the panel sends nothing');
  check(/Nothing sent from this page/.test(q('[data-tools-pill]').textContent), 'the pill starts as "Nothing sent from this page"', q('[data-tools-pill]').textContent);

  await server('POST', '/__arm');
  const uni = q('[data-tools-universe]');
  check(uni.value === '1', 'the universe box shows the operator numbering of wire universe 0 (UI.formatUser)', uni.value);
  type(uni, '3');
  check(A.get('ConsoleTools._state.universe') === 2, 'typing universe 3 selects wire universe 2 (UI.parseUser)', A.get('ConsoleTools._state.universe'));
  check(/universe 3 · show universe/.test(q('[data-tools-universe-note]').textContent), 'the head note restates the universe and its numbering', q('[data-tools-universe-note]').textContent);
  check(/Port-Address 2 on the wire/.test(q('[data-tools-universe-hint]').textContent), 'the caption names the wire Port-Address', q('[data-tools-universe-hint]').textContent);
  check(q('[data-tools-grid]').querySelectorAll('.b5-dmx-cell').length === 32 && cell(1) && cell(32) && !cell(33), 'page 1 shows channels 1–32');

  type(fader(1), 200);
  await settle();
  let body = lastPost(DMX).body;
  let f = frameOf(body);
  check(body.universe === 2 && f.length === 512 && f[0] === 200 && f.slice(1).every(v => v === 0),
    'a fader move posts /api/dmx {universe 2, all 512 slots, slot 1 = 200, zeros included}', { universe: body.universe, len: f.length, head: f.slice(0, 4) });
  check(box(1).value === '200' && cell(1).classList.contains('is-active'), 'the number box follows the fader and the cell is marked active');
  let w = await wire();
  check(w['2'] && w['2'].frames > 0 && w['2'].last[0] === 200, 'the wire: ArtDmx universe 2 slot 1 = 200', w['2'] && w['2'].last.slice(0, 4));
  check(/Holding raw levels · universe 3/.test(q('[data-tools-pill]').textContent), 'the pill says raw levels hold universe 3 (operator numbering)', q('[data-tools-pill]').textContent);

  type(box(2), 300);
  await settle();
  f = frameOf(lastPost(DMX).body);
  check(f[0] === 200 && f[1] === 255 && fader(2).value === '255', 'a number box above 255 is clamped to 255 and the fader follows', f.slice(0, 3));
  type(fader(2), 0);
  await settle();
  f = frameOf(lastPost(DMX).body);
  check(f[1] === 0 && f[0] === 200, 'a fader pulled to 0 is sent as 0 (zeros included)', f.slice(0, 3));
  w = await wire();
  check(w['2'].last[1] === 0 && w['2'].last[0] === 200, 'the wire: slot 2 back to 0, slot 1 still 200', w['2'].last.slice(0, 3));

  // Live-scrub coalescing: twenty moves in one burst, far fewer requests,
  // and the last value always lands.
  let before = posts(DMX).length;
  for (let v = 1; v <= 20; v++) type(fader(3), v);
  await settle();
  const burst = posts(DMX).length - before;
  check(burst >= 1 && burst < 20, 'twenty moves in one burst are coalesced (at most one request in flight, ~30 Hz)', burst);
  check(frameOf(lastPost(DMX).body)[2] === 20, 'the value the fader stopped at is what was sent last', frameOf(lastPost(DMX).body)[2]);
  check(/2 channels above zero · 0 parked/.test(q('[data-tools-tally]').textContent), 'the tally says how many channels are up and parked', q('[data-tools-tally]').textContent);

  // Park.
  before = posts(DMX).length;
  click(park(1));
  await sleep(150);
  check(park(1).getAttribute('aria-pressed') === 'true' && /Parked/.test(park(1).textContent) && cell(1).classList.contains('is-parked'),
    'Park says Parked, aria-pressed=true, and marks the cell', park(1).textContent);
  check(fader(1).disabled && box(1).disabled, 'a parked channel\'s fader and number box are disabled');
  check(posts(DMX).length === before, 'parking never itself sends');
  type(fader(1), 50);
  await sleep(150);
  check(posts(DMX).length === before && A.get('ConsoleTools._levels()[0]') === 200, 'a move on a parked channel is ignored (level stays 200, nothing sent)');
  check(/1 parked/.test(q('[data-tools-tally]').textContent), 'the tally counts the parked channel');

  // Paging and jump.
  click(q('[data-tools-page-next]'));
  check(cell(33) && cell(64) && !cell(1) && q('[data-tools-page]').value === '1', 'Next shows channels 33–64');
  check(!q('[data-tools-page-prev]').disabled, 'Previous is enabled after the first page');
  type(fader(40), 77);
  await settle();
  f = frameOf(lastPost(DMX).body);
  check(f[39] === 77 && f[0] === 200 && f[2] === 20, 'a channel on another page joins the same full frame (slot 40 = 77, slot 1 and 3 kept)', [f[0], f[2], f[39]]);
  change(q('[data-tools-page]'), 0);
  check(cell(1) && park(1).getAttribute('aria-pressed') === 'true' && box(1).value === '200', 'back on page 1, levels and park are as they were');
  const jumpBox = q('[data-tools-jump]');
  jumpBox.value = '512';
  const enter = new Event('keydown', { bubbles: true }); enter.key = 'Enter';
  jumpBox.dispatchEvent(enter);
  check(cell(512) && q('[data-tools-page]').value === '15' && q('[data-tools-page-next]').disabled, 'Jump to 512 (Enter) shows the last page; Next is disabled there');
  check(document.activeElement === box(512), 'the jump puts the focus on channel 512\'s level box');
  jumpBox.value = '0';
  click(q('[data-tools-jump-go]'));
  check(/channel from 1 to 512/.test(q('[data-tools-status]').textContent) && cell(512), 'jumping to channel 0 is refused in words and the page stays', q('[data-tools-status]').textContent);
  jumpBox.value = '40';
  click(q('[data-tools-jump-go]'));
  check(cell(40) && fader(40).value === '77', 'Go jumps to channel 40 (page 2) and shows its level');

  // All off: parked channels exempt.
  click(q('[data-tools-alloff]'));
  await settle();
  f = frameOf(lastPost(DMX).body);
  check(f[0] === 200 && f[2] === 0 && f[39] === 0 && f.filter(v => v !== 0).length === 1, 'All off zeroes every unparked channel at once; the parked one stays 200', [f[0], f[2], f[39]]);
  check(fader(40).value === '0', 'All off updates the faders on screen');
  w = await wire();
  check(w['2'].last[0] === 200 && w['2'].last[2] === 0 && w['2'].last[39] === 0, 'the wire after All off: slot 1 = 200, slots 3 and 40 = 0', [w['2'].last[0], w['2'].last[2], w['2'].last[39]]);

  change(q('[data-tools-page]'), 0);
  click(park(1));
  check(park(1).getAttribute('aria-pressed') === 'false' && /Park$/.test(park(1).textContent.trim()) && !fader(1).disabled, 'unparking gives the channel back to the fader');

  // Universe change: the next send goes to the new universe; the old one
  // keeps its frame until Release all (Send's semantics).
  type(uni, '5');
  type(fader(1), 10);
  await settle();
  body = lastPost(DMX).body;
  check(body.universe === 4 && frameOf(body)[0] === 10, 'after typing universe 5 the next move posts universe 4 (wire) with slot 1 = 10', { universe: body.universe, s1: frameOf(body)[0] });
  w = await wire();
  check(w['4'] && w['4'].last[0] === 10 && w['2'] && w['2'].last[0] === 200, 'the wire: universe 4 slot 1 = 10, and universe 2 still holds its frame until Release all', [w['4'] && w['4'].last[0], w['2'] && w['2'].last[0]]);
  check(/Holding raw levels · universe 3, 5/.test(q('[data-tools-pill]').textContent), 'the pill lists both held universes in operator numbering', q('[data-tools-pill]').textContent);

  // Numbering change on Settings: the box is rewritten from the canonical
  // wire universe, never re-read under the new base.
  A.get('UI').setArtnetStart(1);
  A.window.dispatchEvent(new Event('b5-universe-base-changed'));
  check(uni.value === '4' && A.get('ConsoleTools._state.universe') === 4, 'a numbering change rewrites the box (wire 4 shows as 4 when user 1 = Art-Net 1) and keeps the wire universe', uni.value);
  type(fader(1), 11);
  await settle();
  check(lastPost(DMX).body.universe === 4, 'after the numbering change the frame still goes to wire universe 4');
  A.get('UI').setArtnetStart(0);
  A.window.dispatchEvent(new Event('b5-universe-base-changed'));

  // Release all.
  click(q('[data-tools-release]'));
  await until('the release', () => posts('/api/dmx/stop').length === 1 && /Released/.test(q('[data-tools-pill]').textContent));
  check(posts('/api/dmx/stop').length === 1, 'Release all posts /api/dmx/stop');
  w = await wire(200);
  const lit = k => w[k] && w[k].last.some(v => v !== 0);
  check(!lit('2') && !lit('4'), 'the wire after Release all: universes 2 and 4 carry no raw level any more', [w['2'] && w['2'].last.slice(0, 2), w['4'] && w['4'].last.slice(0, 2)]);
  w = await wire(200);
  check(!w['2'] && !w['4'], 'and then leave the wire (no more datagrams for 2 or 4)', Object.keys(w));
  check(box(1).value === '11', 'Release all leaves the levels on this page as they were');

  // ---------------------------------------------------------------- part 2
  const idf = id => document.getElementById(id);
  check(!!q('[data-tools-identify] #identifyHead') && q('[data-tools-identify] #identifyHead').localName === 'h3', 'Universe Identify is inside the Tools panel, titled as a sub-section (h3)');
  check(idf('identifyArm').disabled && idf('identifyStart').disabled, 'Set range and Identify on start disabled (no range typed)');
  type(idf('identifyFrom'), '9'); type(idf('identifyTo'), '8');
  check(idf('identifyArm').disabled, 'a reversed range keeps Set range disabled');
  type(idf('identifyFrom'), '7');
  check(!idf('identifyArm').disabled, 'a valid range enables Set range');
  click(idf('identifyArm'));
  await until('the arm', () => /Range set/.test(idf('identifyStatus').textContent));
  check(JSON.stringify(lastPost('/api/dmx/identify/arm').body) === JSON.stringify({ protocol: 'artnet', from: 7, to: 8 }), 'Set range posts /api/dmx/identify/arm {protocol, from, to} with raw protocol numbers', lastPost('/api/dmx/identify/arm').body);
  w = await wire();
  check(!w['7'] && !w['8'], 'setting the range sends nothing');
  click(idf('identifyStart'));
  await until('identify on', () => /Identifying/.test(idf('identifyStatus').textContent));
  check(typeof lastPost('/api/dmx/identify/start').body.token === 'string' && lastPost('/api/dmx/identify/start').body.token.length > 0, 'Identify on posts start with the arm\'s token');
  w = await wire();
  check(w['7'] && w['7'].last[6] === 255 && w['7'].last.filter(v => v).length === 1 && w['8'] && w['8'].last[7] === 255 && w['8'].last.filter(v => v).length === 1,
    'the wire: universe 7 channel 7 = 255 and universe 8 channel 8 = 255, every other channel 0', [w['7'] && w['7'].last.slice(5, 9), w['8'] && w['8'].last.slice(5, 9)]);
  A.get('ConsoleScreen').onLeaveScreen();
  w = await wire();
  check(w['7'] && w['7'].last[6] === 255, 'leaving the Console does not turn Identify off (output follows the master Arm)');
  A.get('ConsoleScreen').onEnterScreen();
  type(idf('identifyTo'), '9');
  await until('the edit to disarm', () => posts('/api/dmx/identify/stop').length >= 1 && /Identify off/.test(idf('identifyStatus').textContent));
  check(posts('/api/dmx/identify/stop').length >= 1, 'editing the range turns Identify off (posts stop)');
  w = await wire(200);
  check(!(w['7'] && w['7'].last.some(v => v)) && !(w['8'] && w['8'].last.some(v => v)), 'the wire after the edit: universes 7 and 8 no longer identified');
  click(idf('identifyArm'));
  await until('re-armed', () => /Range set/.test(idf('identifyStatus').textContent));
  click(idf('identifyStart'));
  await until('identify on again', () => /Identifying/.test(idf('identifyStatus').textContent));
  const stops = posts('/api/dmx/identify/stop').length;
  click(idf('identifyStop'));
  await until('identify off', () => posts('/api/dmx/identify/stop').length === stops + 1 && /Identify off/.test(idf('identifyStatus').textContent));
  check(/Identify off/.test(idf('identifyStatus').textContent), 'Identify off posts stop and says Identify off');

  }

  async function partThree() {
  // ---------------------------------------------------------------- part 3
  let navigated = null;
  A.window.addEventListener('b5-navigate', e => { navigated = e.detail; });
  A.get('Workspace').selection([ids.G1, ids.G2]);
  await A.get('Workspace').open();
  const dlg = document.querySelector('dialog.b5-workspace-dialog');
  const useSel = dlg && dlg.querySelector('[data-test-selection]');
  check(!!useSel && /Console/.test(useSel.textContent) && !/Function check/.test(useSel.textContent), 'Show tools offers the selection to the Console, not the retired Function check', useSel && useSel.textContent);
  const selBefore = posts('/api/programmer/select').length;
  click(useSel);
  await until('the navigation', () => navigated === 'console');
  check(navigated === 'console', 'the button opens the Console (b5-navigate console)', navigated);
  const sp = posts('/api/programmer/select')[selBefore];
  check(sp && sp.body.action === 'set' && JSON.stringify(sp.body.targets) === JSON.stringify([{ entryId: ids.G1, cell: '' }, { entryId: ids.G2, cell: '' }]),
    'and selects exactly those fixtures in the programmer (POST /api/programmer/select set)', sp && sp.body);
  const prog = await server('GET', '/api/programmer');
  check((prog.selection || []).map(s => s.entryId).join(',') === [ids.G1, ids.G2].join(','), 'the server programmer holds the selection', prog.selection);
  check(!posts('/api/patch/rigcheck/stop').length && !posts('/api/patch/rigcheck/pattern/scope').length, 'no retired Rig Check endpoint is called');
  check(!dlg.open, 'the Show tools dialog closes');
  }

  // ---------------------------------------------------------------- part 4
  // The retired screens are gone from the page the browser loads: the Send
  // screen (nav, markup, script) and the Patch screen's Rig Check view
  // (rigcheck.js). app.js runs against the real index.html with every
  // screen module stubbed — none named SendScreen — and a stale saved tab
  // of 'send' opens the Console.
  async function partFour() {
    const screens = {};
    const stub = name => { const s = { calls: [] }; ['init', 'onEnterScreen', 'onLeaveScreen'].forEach(m => { s[m] = () => s.calls.push(m); }); screens[name] = s; return s; };
    const fakeApi = new Proxy({}, { get: (t, k) => () => (k === 'getSettings' ? Promise.resolve({ artnetStartUniverse: 0 }) : Promise.resolve({})) });
    const extra = { Api: fakeApi, UI: { setArtnetStart() {} } };
    ['NodesScreen', 'DevicesScreen', 'PatchScreen', 'AnalyzerScreen', 'WalkScreen', 'SettingsScreen', 'Workspace', 'ConsoleScreen', 'Faders'].forEach(n => { extra[n] = stub(n); });
    const P = browser([], extra);
    P.get('localStorage').setItem('benny512.tab', 'send');
    let appError = null;
    try { vm.runInContext(fs.readFileSync(path.join(jsDir, 'app.js'), 'utf8'), P.ctx, { filename: 'app.js' }); } catch (e) { appError = e; }
    await sleep(50);
    const doc = P.document;
    check(!appError && screens.ConsoleScreen.calls.includes('init') && screens.WalkScreen.calls.includes('init'), 'app.js starts every remaining screen without a Send screen module', appError && appError.message);
    check(doc.querySelector('#screen-console').classList.contains('active') && screens.ConsoleScreen.calls.includes('onEnterScreen'), 'a saved tab of "send" (the retired screen) opens the Console instead');
    check(!doc.querySelector('[data-tab="send"]') && !doc.querySelector('#screen-send'), 'no Send tab (desktop or phone) and no Send screen markup');
    const deskTabs = doc.querySelectorAll('#tabs [data-tab]').map(b => b.dataset.tab);
    const phoneTabs = doc.querySelectorAll('#tabsMobile [data-tab]').map(b => b.dataset.tab);
    check(deskTabs.length === 7 && phoneTabs.length === 7, 'seven tabs on desktop and on the phone bottom bar', { deskTabs, phoneTabs });
    check(['nodes', 'devices', 'patch', 'analyzer', 'console', 'walk', 'settings'].every(t => deskTabs.includes(t) && phoneTabs.includes(t)), 'the seven are Nodes, Devices, Patch, Analyzer, Console, Rig Walk and Settings', phoneTabs);
    const scripts = doc.querySelectorAll('script').map(x => x.getAttribute('src'));
    const at = f => scripts.indexOf('/js/' + f);
    check(at('send.js') < 0 && at('rigcheck.js') < 0, 'the page no longer loads send.js or rigcheck.js', scripts);
    check(!fs.existsSync(path.join(jsDir, 'send.js')) && !fs.existsSync(path.join(jsDir, 'rigcheck.js')), 'send.js and rigcheck.js are deleted');
    check(at('console-tools.js') > at('universeidentify.js') && at('universeidentify.js') >= 0 && at('console-tools.js') < at('console.js'), 'console-tools.js loads after universeidentify.js and before console.js', scripts);
    check(doc.querySelectorAll('link').some(l => l.getAttribute('href') === '/css/screens-console-tools.css'), 'the Tools stylesheet is linked');
    const pj = fs.readFileSync(path.join(jsDir, 'patch.js'), 'utf8');
    check(!/data-view="rigcheck"/.test(pj) && !/RigCheckPanel/.test(pj) && !/openFunctionCheck/.test(pj), 'the Patch screen has no Rig Check view (no tab, no RigCheckPanel, no openFunctionCheck)');
    const api = fs.readFileSync(path.join(jsDir, 'api.js'), 'utf8');
    check(!/\/api\/patch\/rigcheck/.test(api) && !/dmxStart/.test(api), 'api.js no longer names a retired endpoint (/api/patch/rigcheck/*, /api/dmx/start)');
  }
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
