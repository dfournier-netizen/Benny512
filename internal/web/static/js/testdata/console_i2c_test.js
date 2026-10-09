// console_i2c_test.js — Console-lite I2c attribute controls, run as the
// browser runs them: the real index.html in minidom.js, the LITERAL api.js,
// ws.js, programmer.js, ui.js, console-controls.js and console.js, against
// a real Benny512 server (internal/web/console_i2c_test.go) with two real
// Robe BMFL Spot profiles (B1, B2: 16-bit Pan/Tilt) and an RDM-inferred
// spot (R1: Pan/Tilt known only as attributes, no functions, no ranges).
//
// usage: node console_i2c_test.js <jsDir> <baseURL> <idsJSON>
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

  // --- crash at 1cd2717: Pan/Tilt known only from RDM (no functions) ---------
  // The demo's "Spot 1" + "Beam FX 1" opened on the Position tab and threw
  // "Cannot read properties of undefined (reading 'dmxTo')": the XY pad
  // read function 0 of an attribute that has none.
  await select('R1');
  await until('the Position panel for the RDM-only spot', () => q('[data-panel="position"]'), 2000);
  check(thrown.length === 0, 'selecting an RDM-only spot throws nothing', thrown);
  check(!!q('[data-panel="position"] [data-xypad]'), 'the Position panel draws the XY pad for Pan/Tilt known only from RDM');
  const pad = q('[data-xypad]');
  if (pad) {
    pad.getBoundingClientRect = () => ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200, x: 0, y: 0 });
    const before = posts(SET).length;
    pad.dispatchEvent(new Event('pointerdown', { bubbles: true, clientX: 100, clientY: 100, pointerId: 1, button: 0 }));
    pad.dispatchEvent(new Event('pointerup', { bubbles: true, clientX: 100, clientY: 100, pointerId: 1 }));
    await settled();
    const w = posts(SET).slice(before).map(c => c.body);
    check(same(w, [
      { targets: [T('R1')], attribute: 'Pan', fraction: 0.5 },
      { targets: [T('R1')], attribute: 'Tilt', fraction: 0.5 },
    ]), 'with no function list the pad writes the whole channel (no function reference): Pan and Tilt at 0.5', w);
    await until('the server to hold Pan/Tilt on R1', () => attr('Pan') && attr('Pan').allTouched && attr('Tilt').allTouched);
    check(attr('Pan').allTouched && attr('Pan').channels.every(c => c.value === 128), 'the server holds the RDM spot\'s Pan at 128, touched', attr('Pan') && attr('Pan').channels.map(c => c.value));
  }
  await select('R1', 'B1');
  await until('the Position panel for R1 + B1', () => q('[data-panel="position"] [data-xypad]'), 2000);
  check(thrown.length === 0 && !!q('[data-xypad]'), 'R1 first + a BMFL: the pad draws and nothing throws', thrown);

  // --- I2c1: attribute family tabs (component-specs §14) -----------------------
  const NB = '\u00a0';
  const kd = (el, key, extra) => el.dispatchEvent(new Event('keydown', Object.assign({ bubbles: true, key }, extra || {})));
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const input = (el, v, type) => { el.value = String(v); el.dispatchEvent(new Event(type || 'input', { bubbles: true })); };
  const held = (name, n) => { const a = attr(name); const c = a && a.channels.find(x => x.entryId === ids[n]); return c ? c.value : undefined; };
  const sentDMX = () => { const b = last(SET).body; return Math.round(b.fraction * 65535); };
  // A write made straight to the server (another station), then waited for
  // until this browser has its revision (else its next write is stale).
  const serverSet = async body => {
    const r = await server('POST', SET, body);
    if (r.status !== 200) check(false, 'direct server write ' + JSON.stringify(body), r);
    await until('the browser to see revision ' + (r.body && r.body.revision), () => PS().revision() >= r.body.revision);
  };
  const ro = () => q('[data-readout="Pan"]');
  const pf = () => q('[data-fader="Pan#0"]');
  // Each section runs on its own: a missing control fails its checks and
  // the run goes on, so a proof-of-failure shows every missing behaviour.
  const sect = async (name, fn) => { try { await fn(); } catch (e) { check(false, name + ': ' + (e && e.message || e)); } };
  await sect('I2c1 tabs', async () => {
    await select('B1');
    await until('the tabs for B1', () => document.querySelectorAll('[role="tab"]').length === 7);
    const tabEls = document.querySelectorAll('[role="tab"]');
    check(same(tabEls.map(t => t.getAttribute('data-tab-group')), ['dimmer', 'position', 'colour', 'beam', 'focus', 'shaper', 'other']),
      'seven family tabs in the fixed order dimmer, position, colour, beam, focus, shaper, other', tabEls.map(t => t.getAttribute('data-tab-group')));
    check(same(tabEls.map(t => t.querySelector('.b5-cc-tab__word').textContent), ['Dimmer', 'Position', 'Colour', 'Beam/Gobo', 'Focus', 'Shaper', 'Control']),
      'tab words are Dimmer, Position, Colour, Beam/Gobo, Focus, Shaper, Control (owner-confirmed names)', tabEls.map(t => t.textContent));
    const icons = tabEls.map(t => (/#b5-icon-([a-z-]+)/.exec((t.querySelector('.b5-cc-tab__ic') || {}).innerHTML || '') || [])[1]);
    check(same(icons, ['attr-dimmer', 'attr-position', 'attr-colour', 'attr-beam', 'attr-focus', 'attr-shaper', 'attr-control']), 'each tab carries its sprite icon (attr-*)', icons);
    const have = PS().state().groups.map(g => g.group);
    const liveTabs = ['dimmer', 'position', 'colour', 'beam', 'focus', 'shaper', 'other'].filter(k => have.includes(k));
    check(liveTabs.every(k => /\b0 set$/.test(q('[data-tab-group="' + k + '"] .b5-cc-tabcount').textContent)), 'every family the selection has says "0 set" before anything is set',
      liveTabs.map(k => q('[data-tab-group="' + k + '"]').textContent));
    const none = tabEls.filter(t => !have.includes(t.getAttribute('data-tab-group')));
    check(none.length > 0 && none.every(t => t.getAttribute('aria-disabled') === 'true' && /— not on these fixtures/.test(t.textContent)), 'a family the selection lacks says "— not on these fixtures" and is aria-disabled', none.map(t => t.textContent));
    const active = q('[role="tab"][aria-selected="true"]');
    check(!!active && active.getAttribute('tabindex') === '0' && tabEls.filter(t => t !== active).every(t => t.getAttribute('tabindex') === '-1'), 'roving tabindex: only the selected tab is in the Tab order');
    check(q('[role="tabpanel"]') && q('[role="tabpanel"]').getAttribute('aria-labelledby') === active.getAttribute('id') && active.getAttribute('aria-controls') === q('[role="tabpanel"]').getAttribute('id'), 'tab and panel name each other (aria-controls / aria-labelledby)');
    kd(q('[data-tab-group="' + liveTabs[0] + '"]'), 'End');
    check(get('ConsoleControls')._state.tab === liveTabs[liveTabs.length - 1] && document.activeElement && document.activeElement.getAttribute('data-tab-group') === liveTabs[liveTabs.length - 1], 'End selects and focuses the last family the selection has', get('ConsoleControls')._state.tab);
    kd(document.activeElement, 'Home');
    check(get('ConsoleControls')._state.tab === liveTabs[0], 'Home selects the first family', get('ConsoleControls')._state.tab);
    const fi = liveTabs.indexOf('focus');
    if (fi >= 0 && fi + 1 < liveTabs.length) {
      click(q('[data-tab-group="focus"]'));
      kd(q('[data-tab-group="focus"]'), 'ArrowRight');
      check(get('ConsoleControls')._state.tab === liveTabs[fi + 1] && liveTabs[fi + 1] !== 'shaper', 'ArrowRight from Focus skips the Shaper family the BMFL lacks', get('ConsoleControls')._state.tab);
    }
    click(q('[data-tab-group="shaper"]'));
    check(get('ConsoleControls')._state.tab !== 'shaper', 'clicking a family the selection lacks does nothing');

  });
  // --- shared value grammar, readout, units, SET / default ----------------------
  await sect('shared value grammar, readout, units, SET / default', async () => {
    await serverSet({ targets: [T('B1')], attribute: 'Pan', dmx: 49152 });
    click(q('[data-tab-group="position"]'));
    await until('Pan 49152 drawn', () => /49\u00a0152/.test(q('[data-readout="Pan"]').textContent));
    check(/\b1 set$/.test(q('[data-tab-group="position"] .b5-cc-tabcount').textContent), 'the Position tab counts "1 set" after Pan is set', q('[data-tab-group="position"]').textContent);
    check(ro().querySelector('.b5-cc-val').textContent === '75' + NB + '%', 'the readout shows the chosen unit large (default %): 75 %', ro().querySelector('.b5-cc-val').textContent);
    check(/49\u00a0152 \/ 65\u00a0535/.test(ro().textContent) && /192 · 0 \(coarse · fine\)/.test(ro().textContent) && /16-bit/.test(ro().textContent),
      'a 16-bit readout shows the full value 49 152 / 65 535, its bytes 192 · 0 (coarse · fine) and a 16-bit badge', ro().textContent);
    check(/●/.test(q('[data-markers="Pan"]').textContent) && /SET/.test(q('[data-markers="Pan"]').textContent) && /○/.test(q('[data-markers="Tilt"]').textContent) && /default/.test(q('[data-markers="Tilt"]').textContent),
      'source words: Pan "● SET", Tilt "○ default"', [q('[data-markers="Pan"]').textContent, q('[data-markers="Tilt"]').textContent]);
    const units = q('[data-readout-units]');
    check(!!units, 'a Readout selector (%, DMX, Physical) is on the panel');
    if (units) {
      input(units, 'dmx', 'change');
      check(ro().querySelector('.b5-cc-val').textContent === '49' + NB + '152' && window.localStorage.getItem('b5.consoleControls.readout') === 'dmx', 'Readout DMX: the large value is 49 152, remembered in this browser', ro().querySelector('.b5-cc-val').textContent);
      input(q('[data-readout-units]'), 'phys', 'change');
      check(/°$/.test(ro().querySelector('.b5-cc-val').textContent) && /75\u00a0%/.test(ro().textContent), 'Readout Physical: degrees from the profile large, % still shown', ro().textContent);
      input(q('[data-readout-units]'), 'pct', 'change');
    }

  });
  // --- MIXED, reference value, absolute writes on a mixed selection ---------------
  await sect('MIXED, reference value, absolute writes on a mixed selection', async () => {
    await select('B1', 'B2');
    await serverSet({ targets: [T('B1')], attribute: 'Pan', dmx: 0 });
    await serverSet({ targets: [T('B2')], attribute: 'Pan', dmx: 65535 });
    click(q('[data-tab-group="position"]'));
    await until('Pan MIXED drawn', () => attr('Pan').mixed && /MIXED/.test(q('[data-readout="Pan"]').textContent));
    await sleep(700);
    get('ConsoleControls').refresh();
    check(/MIXED 0–100\u00a0%/.test(ro().textContent) && /reference \(first selected\) 0/.test(ro().textContent), 'MIXED reads its range (0–100 %) and the first selected as reference, never an average', ro().textContent);
    const panF = q('[data-fader="Pan#0"]');
    check(/MIXED/.test(panF.getAttribute('aria-valuetext')) && /reference/.test(panF.getAttribute('aria-valuetext')), 'the slider says MIXED and that its position is the first selected fixture\'s reference', panF.getAttribute('aria-valuetext'));
    const band = q('[data-mixed-band="Pan#0"]');
    check(!!band && !band.hasAttribute('hidden') && band.style.left === '0%' && band.style.right === '0%', 'a dashed mixed band spans the selection\'s range on the track', band && [band.getAttribute('hidden'), band.style.left, band.style.right]);
    check(document.querySelectorAll('.b5-cc-pad__other').length === 1, 'the pad draws the other selected fixture as a dashed dot', document.querySelectorAll('.b5-cc-pad__other').length);
    input(panF, 20000, 'change');
    await settled();
    check(same(last(SET).body.targets, [T('B1'), T('B2')]) && sentDMX() === 20000, 'moving a MIXED fader writes ONE absolute value to every selected fixture (owner: absolute)', last(SET).body);
    await until('both at 20000', () => held('Pan', 'B1') === 20000 && held('Pan', 'B2') === 20000);
    check(held('Pan', 'B1') === 20000 && held('Pan', 'B2') === 20000, 'the server holds Pan 20000 on both');

  });
  // --- Fine: 1:10 over the whole value, no jump, carries across the bytes ----------
  await sect('Fine: 1:10 over the whole value, no jump, carries across the bytes', async () => {
    click(q('[data-fine]'));
    check(/is-fine/.test(q('[data-cc-body]').className), 'Fine ON marks the controls (the veil takes the pointer)');
    await sleep(700);
    const veil = q('[data-fine-veil="Pan#0"]');
    check(!!veil, 'each fader has a fine veil');
    if (veil) {
      q('[data-fader="Pan#0"]').getBoundingClientRect = () => ({ left: 0, top: 0, width: 1000, height: 44, right: 1000, bottom: 44, x: 0, y: 0 });
      const b0 = posts(SET).length;
      veil.dispatchEvent(new Event('pointerdown', { bubbles: true, clientX: 100, clientY: 10, pointerId: 7 }));
      check(posts(SET).length === b0, 'Fine: touching the fader does not jump the value');
      veil.dispatchEvent(new Event('pointermove', { bubbles: true, clientX: 600, clientY: 10, pointerId: 7 }));
      veil.dispatchEvent(new Event('pointerup', { bubbles: true, clientX: 600, clientY: 10, pointerId: 7 }));
      await settled();
      check(sentDMX() === 23277, 'Fine: a 500 px drag on a 1000 px fader moves 1/10 of half the range: 20000 + 3276.75 → 23277 (whole value)', sentDMX());
      await until('23277 held', () => held('Pan', 'B1') === 23277);
      check(held('Pan', 'B1') === 23277 && held('Pan', 'B2') === 23277, 'the server holds the complete value 23277 on both');
    }
    const exact = q('[data-fader-exact="Pan#0"]');
    check(!!exact, 'each fader has an exact numeric entry');
    if (exact) {
      input(exact, 255, 'change');
      await settled();
      await until('255 held', () => held('Pan', 'B1') === 255);
      click(q('[data-step-up="Pan#0"]'));
      await settled();
      await until('256 held', () => held('Pan', 'B1') === 256);
      check(held('Pan', 'B1') === 256, 'Fine +: 255 → 256, the carry into the coarse byte (never the fine byte alone)', held('Pan', 'B1'));
      await sleep(700);
      get('ConsoleControls').refresh();
      check(/1 · 0 \(coarse · fine\)/.test(q('[data-fader-value="Pan#0"]').textContent), 'the fader reads 256 as bytes 1 · 0 (coarse · fine)', q('[data-fader-value="Pan#0"]').textContent);
      const b1 = posts(SET).length;
      input(q('[data-fader-exact="Pan#0"]'), 70000, 'change');
      await sleep(80);
      check(posts(SET).length === b1 && /from 0 to 65535; nothing was sent/.test(q('[data-cc-status]').textContent), 'an exact value outside the range is refused in words; nothing is sent', q('[data-cc-status]').textContent);
    }
    click(q('[data-fine]'));

  });
  // --- contract K on a fader ---------------------------------------------------------
  await sect('contract K on a fader', async () => {
    input(pf(), 256, 'change');
    await settled();
    const keys = [['ArrowRight', {}, 911], ['ArrowRight', { shiftKey: true }, 977], ['PageUp', {}, 7531], ['ArrowDown', {}, 6876], ['Home', {}, 0], ['End', {}, 65535]];
    for (const [k, extra, want] of keys) {
      kd(pf(), k, extra);
      await settled();
      check(sentDMX() === want, 'K: ' + (extra.shiftKey ? 'Shift+' : '') + k + ' → ' + want + ' (1 % = 655, Shift a tenth = 66, Page 10 % = 6554, Home/End the ends)', sentDMX());
    }

  });
  // --- XY pad: role, K steps, fine ------------------------------------------------------
  await sect('XY pad: role, K steps, fine', async () => {
    await serverSet({ targets: [T('B1'), T('B2')], attribute: 'Pan', dmx: 32768 });
    await serverSet({ targets: [T('B1'), T('B2')], attribute: 'Tilt', dmx: 32768 });
    await until('pan/tilt 32768', () => held('Pan', 'B1') === 32768 && held('Tilt', 'B2') === 32768);
    await sleep(700);
    get('ConsoleControls').refresh(true);
    const pd = q('[data-xypad]');
    check(pd.getAttribute('role') === 'group' && !/application/.test(pd.getAttribute('role')), 'the pad is a labelled group, not role=application');
    check(/Shift\+arrow, or Fine ON, moves a tenth/.test(q('[data-pad-hint]').textContent), 'the pad hint says Shift is the fine step (K), not a big step', q('[data-pad-hint]').textContent);
    kd(pd, 'ArrowRight');
    await settled();
    check(last(SET).body.attribute === 'Pan' && sentDMX() === 32768 + 655, 'pad ArrowRight: Pan + 1 % of its span (655)', [last(SET).body.attribute, sentDMX()]);
    kd(pd, 'ArrowUp', { shiftKey: true });
    await settled();
    check(last(SET).body.attribute === 'Tilt' && sentDMX() === 32768 + 66, 'pad Shift+ArrowUp: Tilt + a tenth of 1 % (66) — fine, not the old big step', [last(SET).body.attribute, sentDMX()]);

  });
  // --- Dimmer quick buttons ---------------------------------------------------------------
  await sect('Dimmer quick buttons', async () => {
    await select('B1');
    click(q('[data-tab-group="dimmer"]'));
    await until('the Dimmer quick buttons', () => q('[data-dimmer-quick="1"]'));
    if (q('[data-dimmer-quick="1"]')) {
      click(q('[data-dimmer-quick="1"]'));
      await settled();
      const db = last(SET).body;
      check(db.attribute === 'Dimmer' && db.fraction === 1 && same(db.targets, [T('B1')]) && typeof db.functionIndex === 'number', 'FULL sets the Dimmer function to 1 for the selection', db);
      await until('Dimmer full', () => attr('Dimmer').value === 65535);
      check(attr('Dimmer').value === 65535, 'the server holds Dimmer 65535');
      click(q('[data-dimmer-quick="0"]'));
      await settled();
      await until('Dimmer 0', () => attr('Dimmer').value === 0);
      check(attr('Dimmer').value === 0, '0 sets it back to 0');
    }

  });
  // --- partial scope and the RDM-only whole-channel fader in tandem with the pad ------
  await sect('partial scope and the RDM-only whole-channel fader in tandem with the pad', async () => {
    await select('R1', 'B1');
    click(q('[data-tab-group="dimmer"]'));
    await until('the Dimmer card', () => q('[data-scope="Dimmer"]'));
    check(q('[data-scope="Dimmer"]') && q('[data-scope="Dimmer"]').textContent === '1 of 2 selected', 'Partial: the Dimmer card says "1 of 2 selected" under its label', q('[data-scope="Dimmer"]') && q('[data-scope="Dimmer"]').textContent);
    await select('R1');
    click(q('[data-tab-group="position"]'));
    await until('the R1 whole-channel Pan fader', () => q('[data-fader="Pan#-1"]'));
    const wf = q('[data-fader="Pan#-1"]');
    check(!!wf, 'an RDM-only Pan gets one whole-channel fader');
    if (wf) {
      input(wf, 200, 'change');
      await settled();
      check(same(last(SET).body, { targets: [T('R1')], attribute: 'Pan', fraction: 200 / 255 }), 'it writes the whole channel (no function reference)', last(SET).body);
      check(/Pan 78\u00a0%/.test(q('[data-pad-readout]').textContent), 'tandem: the whole-channel fader moves the pad (Pan 78 %)', q('[data-pad-readout]').textContent);
    }
    check(/Disarmed · values retained, nothing sent/.test(q('[data-output-note]').textContent), 'disarmed: the panel note says values are retained and nothing is sent', q('[data-output-note]').textContent);
  });
  check(thrown.length === 0, 'nothing threw during the whole run', thrown);

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
