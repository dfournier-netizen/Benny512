// console_i2e_test.js — Console-lite I2e: test tiles (component-specs §16),
// sequence transport and editor (§17), the raw / unprofiled bank (§18) and
// the MIDI bar, run as the browser runs them: the real index.html in
// minidom.js and the LITERAL scripts, against a real Benny512 server
// (internal/web/console_i2e_test.go): BMFL B1, LEDBeam L1, and G1 (no
// profile, 4 channels), Z1 (no profile, no footprint), R1 (no profile, 3
// channels, committed to an RDM device).
//
// usage: node console_i2e_test.js <jsDir> <baseURL> <idsJSON>
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
for (const f of ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-controls.js', 'console-tests.js', 'console-midi.js', 'console.js']) {
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
const typeIn = (el, v) => { el.value = v; el.dispatchEvent(new Event('input', { bubbles: true })); };

(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));
  await until('the Tests panel to mount', () => q('[data-tests-toggle]'));

  // --- §16 test tiles -------------------------------------------------------------------
  await sect('tiles', async () => {
    await select('B1', 'L1');
    if (q('[data-tests-toggle]').getAttribute('aria-expanded') !== 'true') click(q('[data-tests-toggle]'));
    await until('the test tiles', () => qa('[data-test-tile]').length > 3);
    const tiles = qa('[data-test-tile]');
    check(tiles.every(t => iconOf(t.querySelector('[data-test-glyph]')).length === 1), 'every tile carries one glyph for its kind', tiles.map(t => t.getAttribute('data-test-tile')));
    const dim = q('[data-test-tile="dimmer_sine"]');
    check(!!dim && iconOf(dim.querySelector('[data-test-glyph]'))[0] === '#b5-icon-attr-dimmer' && /OFF/.test(dim.textContent), 'the dimmer tile shows the dimmer glyph and the word OFF', dim && dim.textContent);
    const det = q('[data-test-settings="dimmer_sine"]');
    check(!!det && det.tagName === 'DETAILS' && det.querySelector('[data-test-more="dimmer_sine"]') && det.querySelector('[data-test-more="dimmer_sine"]').tagName === 'SUMMARY', 'Settings is its own <details> with a <summary>');
    check(!q('[data-test-toggle="dimmer_sine"]').querySelector('[data-test-more]'), 'Settings is never nested inside the ON/OFF toggle button');
    click(q('[data-test-more="dimmer_sine"]'));
    await until('the settings panel', () => q('[data-test-settings="dimmer_sine"]').open && /Turn this test ON/.test(q('[data-test-settings="dimmer_sine"]').textContent));
    click(q('[data-test-toggle="dimmer_sine"]'));
    await until('the test on', () => /ON/.test(q('[data-test-tile="dimmer_sine"] .b5-tile__stateword').textContent));
    check(q('[data-test-tile="dimmer_sine"]').classList.contains('is-on') && q('[data-test-settings="dimmer_sine"]').open, 'ON: the word, the tile state, and Settings stays open across the redraw');
    click(q('[data-test-toggle="dimmer_sine"]'));
    await until('the test off', () => /OFF/.test(q('[data-test-tile="dimmer_sine"] .b5-tile__stateword').textContent));

    // The action bar's Tests opens the panel; Close brings focus back.
    click(q('[data-tests-toggle]'));
    await until('the panel closed', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'false');
    const jump = q('[data-tests-jump]');
    jump.focus();
    click(jump);
    await until('the panel opened from the action bar', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'true');
    check(!!q('[data-tests-close]'), 'the open panel has a Close button (the phone sheet\'s way out)');
    click(q('[data-tests-close]'));
    await until('closed again', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'false');
    check(document.activeElement && document.activeElement.hasAttribute('data-tests-jump'), 'closing returns focus to the Tests button that opened it');
    click(q('[data-tests-toggle]'));
    await until('open for the next part', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'true');
  });

  // --- §17 transport ----------------------------------------------------------------------
  await sect('transport', async () => {
    const sv = await server('GET', '/api/tests');
    const a = sv.body.available.find(x => x.kind === 'dimmer_sine');
    const spec = { kind: a.kind, rateHz: 0, min: 0, max: 255, target: a.target || '', direction: '', value: 128, on: true, waveform: 'sine', offsetMin: 0, offsetMax: 0 };
    const save = await server('POST', TT + 'sequence-save', { name: 'Walk', steps: [
      { name: 'Dimmers', tests: [spec], scope: { kind: 'selection', group: '', layer: '' }, fadeMs: null, advance: { mode: 'auto', seconds: 30 } },
      { name: 'Hold', tests: [spec], scope: { kind: 'selection', group: '', layer: '' }, fadeMs: null, advance: { mode: 'manual', seconds: 0 } },
    ] });
    check(save.status === 200, 'a two-step sequence is stored', save);
    await until('the sequence in the list', () => q('[data-seq-run]'));
    click(q('[data-seq-run]'));
    await until('the run', () => q('[data-run-step-word]'));
    const back = q('[data-run-back]'), next = q('[data-run-next]');
    check(iconOf(back)[0] === '#b5-icon-seq-back' && iconOf(next)[0] === '#b5-icon-seq-next' && back.classList.contains('b5-ct-stepbtn') && next.classList.contains('b5-ct-stepbtn'),
      'Back and Next are the large step targets with distinct directional glyphs');
    check(back.disabled && !next.disabled, 'Back stops at the first step');
    const auto = q('[data-run-auto]');
    check(!!auto && /AUTO 30 s/.test(auto.textContent) && auto.getAttribute('aria-pressed') === 'true', 'on an auto step AUTO is a toggle that says its time ("AUTO 30 s")', auto && auto.textContent);
    check(/CURRENT/.test(q('[data-run-step="0"]').textContent) && q('[data-run-step="0"]').getAttribute('aria-current') === 'step', 'the current step says CURRENT and is aria-current');
    click(auto);
    await until('AUTO off', () => q('[data-run-auto]').getAttribute('aria-pressed') === 'false');
    check(same(last(TT + 'run-pause').body, {}) && /AUTO OFF/.test(q('[data-run-auto]').textContent), 'AUTO off pauses the countdown (run-pause) and says AUTO OFF');
    click(q('[data-run-auto]'));
    await until('AUTO on', () => q('[data-run-auto]').getAttribute('aria-pressed') === 'true');
    click(q('[data-run-next]'));
    await until('step 2', () => /Step 2 of 2 · Hold/.test(q('[data-run-step-word]').textContent));
    check(q('[data-run-auto]').disabled && /advances by hand/.test(q('[data-run-auto-why]').textContent), 'on a manual step AUTO is unavailable and says why');
    check(q('[data-run-next]').disabled, 'Next stops at the last step (no silent loop)');
    click(q('[data-run-stop]'));
    await until('stopped', () => !q('[data-run-step-word]'));
  });

  // --- §17 editor --------------------------------------------------------------------------
  await sect('editor', async () => {
    click(q('[data-seq-new]'));
    await until('the editor', () => q('[data-seq-editor]'));
    check(/does not clear the programmer/.test(q('[data-seq-editor]').textContent), 'the editor says edits are not a programmer clear');
    const b0 = q('[data-step-add-after="0"]');
    check(!!b0 && /Add step after/.test(b0.textContent) && /Move up/.test(q('[data-step-up="0"]').textContent) && /Move down/.test(q('[data-step-down="0"]').textContent) && /Remove step/.test(q('[data-step-remove="0"]').textContent),
      'each row has worded Move up / Move down / Add step after / Remove step buttons (no drag-only path)');
    click(b0);
    await until('two steps', () => qa('[data-step-editor]').length === 2);
    check(/position 2 of 2/.test(q('[data-step-announce]').textContent), 'adding a step announces its position', q('[data-step-announce]').textContent);
    typeIn(q('[data-step-name="1"]'), 'Second');
    click(q('[data-step-up="1"]'));
    await until('the move', () => q('[data-step-name="0"]').value === 'Second');
    check(/Moved "Second" to position 1 of 2/.test(q('[data-step-announce]').textContent), 'the move is announced with the new position', q('[data-step-announce]').textContent);
    const row0 = q('[data-step-editor="0"]');
    check(row0.contains(document.activeElement) && document.activeElement.hasAttribute('data-step-down'), 'focus stays on the moved step (Move up is now disabled, so its Move down)', document.activeElement && document.activeElement.getAttribute && document.activeElement.outerHTML);
    check(row0.classList.contains('is-selected') && /SELECTED/.test(row0.textContent), 'the moved step is the selected one, in words and outline');
    click(q('[data-step-remove="0"]'));
    await until('one step', () => qa('[data-step-editor]').length === 1);
    check(/Removed "Second"/.test(q('[data-step-announce]').textContent), 'removing is announced', q('[data-step-announce]').textContent);
    click(q('[data-seq-cancel]'));
    await until('the editor closed', () => !q('[data-seq-editor]'));
  });


  // --- §18 raw bank ------------------------------------------------------------------------
  await sect('raw', async () => {
    // txt: an element's words, or '' when it is not drawn (so every check
    // reports instead of the first missing element throwing).
    const txt = sel => (q(sel) || { textContent: '' }).textContent;
    await select('G1');
    await until('the raw bank', () => qa('[data-raw-panel] [data-raw]').length === 4);
    const head = txt('[data-raw-head="' + ids.G1 + '"]');
    check(/No profile/.test(head) && /4 channels/.test(head) && !/RDM only/.test(head), 'G1: "No profile · 4 channels" (not RDM: no confirmed UID)', head);
    const ch = txt('[data-raw-ch="raw#' + ids.G1 + '#2"]');
    check(/ch 2/.test(ch) && /address 2/.test(ch) && /RDM slot label: not reported/.test(ch) && /\/ 255/.test(ch) && /default/.test(ch),
      'each channel names its relative ch, absolute address, slot label (not reported), 0–255 value and source', ch);
    check(!q('[data-raw-rdm-read]'), 'no RDM read is offered for a fixture with no RDM device');
    check(/whole universe/.test(txt('[data-raw-panel]')), 'the bank says fixture faders are not the whole-universe Tools');
    await select('Z1');
    await until('the no-footprint card', () => q('[data-raw-nofootprint="' + ids.Z1 + '"]'));
    check(!q('[data-raw-panel] [data-raw]') && /no faders are invented/.test(txt('[data-raw-nofootprint="' + ids.Z1 + '"]')), 'Z1 (footprint not reported) gets no faders, and says so');
    await select('R1');
    await until('R1\'s bank', () => qa('[data-raw-panel] [data-raw]').length === 3);
    check(/RDM only/.test(txt('[data-raw-head="' + ids.R1 + '"]')) && !!q('[data-raw-rdm-read="' + ids.R1 + '"]'), 'R1 (committed to an RDM device): "RDM only" and a Read RDM info button', txt('[data-raw-head="' + ids.R1 + '"]'));
  });

  // --- I2e: a dimmer test on cells says it works through their virtual dimmers
  await sect('virtual dimmer words', async () => {
    if (q('[data-tests-toggle]').getAttribute('aria-expanded') !== 'true') click(q('[data-tests-toggle]'));
    await PS().act('select', { action: 'set', targets: ['Pixel 1:0', 'Pixel 2:0'].map(c => ({ entryId: ids.BT, cell: c })) });
    await server('POST', TT + 'set', { tests: [{ kind: 'dimmer_toggle', on: true }] });
    await until('the dimmer tile ON', () => /ON/.test((q('[data-test-tile="dimmer_toggle"] .b5-tile__stateword') || {}).textContent || ''));
    if (!q('[data-test-settings="dimmer_toggle"]').open) click(q('[data-test-more="dimmer_toggle"]'));
    await until('the per-fixture button', () => q('[data-test-fixtures="dimmer_toggle"]'));
    click(q('[data-test-fixtures="dimmer_toggle"]'));
    await until('the per-fixture list', () => qa('[data-fixture-status]').length === 2);
    const rows = qa('[data-fixture-status]').map(e => e.textContent);
    check(rows.every(r => /testing/.test(r) && /virtual dimmer \(scales colour\)/.test(r)), 'each cell says the test reaches it through its virtual dimmer (scales colour), in words', rows);
    await server('POST', TT + 'clear', {});
  });

  // --- I2e count pill: two unbreakable parts, so the phone wraps, never cuts
  await sect('count pill', async () => {
    await PS().act('select', { action: 'set', targets: [{ entryId: ids.B1, cell: '' }] });
    await until('one selected', () => /1 selected/.test((q('[data-sel-count]') || {}).textContent || ''));
    const pill = q('[data-sel-count]');
    const n = pill.querySelector('[data-sel-count-n]'), cells = pill.querySelector('[data-sel-count-cells]');
    check(n && n.textContent === '1 selected' && !cells, 'the count is its own part; no cell part without cells', pill.textContent);
    const cellIds = ['Pixel 1:0', 'Pixel 2:0'];
    {
      await PS().act('select', { action: 'set', targets: cellIds.map(c => ({ entryId: ids.BT, cell: c })) });
      await until('two cells selected', () => /2 cells/.test((q('[data-sel-count]') || {}).textContent || ''));
      const p2 = q('[data-sel-count]');
      check(p2.querySelector('[data-sel-count-n]').textContent === '2 selected' && p2.querySelector('[data-sel-count-cells]').textContent === ' · 2 cells' && p2.textContent.includes('2 selected · 2 cells'),
        'cells: "2 selected" and " · 2 cells" are separate unbreakable parts; the words still read "2 selected · 2 cells"', p2.textContent);
    }
  });

  // --- §16 P: on a phone the panel is a sheet; Close / Escape give focus back
  await sect('phone sheet', async () => {
    click(q('[data-tests-toggle]'));
    await until('closed before the phone part', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'false');
    phone = true;
    get('ConsoleScreen').onEnterScreen();
    await until('the phone bar\'s More', () => q('[data-summary-more-toggle]'));
    click(q('[data-summary-more-toggle]'));
    await until('More open', () => q('[data-summary-more-toggle]').getAttribute('aria-expanded') === 'true');
    const jump = q('[data-summary-more] [data-tests-jump]');
    check(!!jump, 'on a phone Tests sits in More (C6c contract kept)');
    jump.focus();
    click(jump);
    await until('the sheet open', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'true');
    check(document.activeElement && document.activeElement.hasAttribute('data-tests-close'), 'the sheet opens with focus on its Close button', document.activeElement && document.activeElement.outerHTML);
    kd(q('[data-tests-close]'), 'Escape');
    await until('Escape closed the sheet', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'false');
    check(document.activeElement && document.activeElement.hasAttribute('data-summary-more-toggle'), 'closing the sheet gives focus to More, since the Tests button is inside the closed menu (I2d2 rule)', document.activeElement && document.activeElement.outerHTML);
    phone = false;
    get('ConsoleScreen').onEnterScreen();
    click(q('[data-tests-toggle]'));
    await until('open again (wide)', () => q('[data-tests-toggle]').getAttribute('aria-expanded') === 'true');
  });

  check(thrown.length === 0, 'nothing threw during the whole run', thrown);
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
