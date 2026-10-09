// console_c6c_test.js — Console-lite C6c run as the browser runs it: the
// real index.html parsed into a small DOM (minidom.js) and the LITERAL
// api.js, ws.js, programmer.js, ui.js, console-tests.js and
// console.js against a real Benny512 server (started by
// internal/web/console_c6c_test.go): real fetch, real WebSocket. Every
// request the page makes is recorded on its way to the server.
//
//  Part 1 — the Tests panel: the exact /api/tests/set payload a toggle,
//    a parameter, a scope and a fade send (with X-Benny-Tests); the
//    sequence editor's exact sequence-save payload; rename/delete through
//    in-app dialogs; every run control's call; another browser's change
//    arriving through the "tests" broadcast; a second browser showing the
//    running step.
//  Part 2 — live layout sync in console.js: another browser's layout edit
//    is re-read through the "layout" broadcast; this browser's own edit is
//    not re-read; a hidden Console reloads on entry.
//  Part 3 — the compact selection bar: Store group / Highlight / Locate
//    sit behind a More toggle.
//
// usage: node console_c6c_test.js <jsDir> <baseURL> <idsJSON>
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
    try { if (fn()) return true; } catch (_) { /* not yet */ }
    await sleep(15);
  }
  check(false, 'timed out waiting for ' + what);
  return false;
}
const indexHTML = fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8');
function memStore() { const m = new Map(); return { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k) }; }

function browser(files) {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(indexHTML);
  const url = new URL(base);
  const calls = [];
  const sandbox = {
    console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
    WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
    localStorage: memStore(), sessionStorage: memStore(), getComputedStyle: window.getComputedStyle,
    fetch: (p, opts) => {
      opts = opts || {};
      const rec = { method: opts.method || 'GET', path: String(p), headers: Object.assign({}, opts.headers || {}), body: opts.body ? JSON.parse(opts.body) : undefined };
      calls.push(rec);
      return fetch(new URL(p, base), opts);
    },
  };
  window.addEventListener = window.addEventListener.bind(window);
  const ctx = vm.createContext(sandbox);
  for (const f of files) vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
  return { ctx, window, document, calls, get: expr => vm.runInContext(expr, ctx) };
}

async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  if (!r.ok) throw new Error(method + ' ' + p + ': ' + r.status + ' ' + t);
  return t ? JSON.parse(t) : null;
}

const FILES = ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-tests.js', 'console.js'];

(async () => {
  const A = browser(FILES);
  const { document, calls } = A;
  const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
  const lastPost = p => { const l = posts(p); return l[l.length - 1]; };
  const gets = p => calls.filter(c => c.method === 'GET' && c.path === p).length;
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const change = (el, v) => { el.value = String(v); el.dispatchEvent(new Event('change', { bubbles: true })); };
  const type = (el, v) => { el.value = String(v); el.dispatchEvent(new Event('input', { bubbles: true })); };
  const panel = () => document.querySelector('[data-tests-region]');
  const q = sel => panel().querySelector(sel);
  const SET = '/api/tests/set';
  const T = '/api/tests/';

  await until('the WebSocket and the programmer to load', () => A.get('ProgrammerSync.state()'));
  A.get('ConsoleScreen').onEnterScreen();
  await until('the Tests panel to mount under Controls', () => panel() && q('[data-tests-toggle]') && q('[data-tests-rule]'));
  check(!!document.querySelector('.b5-con-pane--side [data-tests-region]'), 'the Tests panel is mounted in the Console\'s side pane, under Controls');
  check(/manual values/.test(q('[data-tests-rule]').textContent) && /by hand wins/.test(q('[data-tests-rule]').textContent), 'one line says tests sit under manual values and a hand-set channel wins', q('[data-tests-rule]').textContent);
  check(q('[data-tests-toggle]').getAttribute('aria-expanded') === 'true' && /hide/.test(q('[data-tests-toggle]').textContent), 'the panel is collapsible: its toggle says Tests · hide and aria-expanded=true');

  // ---------------------------------------------------------------- part 1
  await A.get('ProgrammerSync').act('select', { action: 'set', targets: [{ entryId: ids.B1, cell: '' }, { entryId: ids.B2, cell: '' }] });
  await until('the catalog for the two BMFLs', () => q('[data-test-toggle="dimmer_sine"]'));
  const tile = id => q('[data-test-toggle="' + id + '"]');
  check(tile('dimmer_sine').getAttribute('aria-pressed') === 'false' && /OFF/.test(tile('dimmer_sine').textContent), 'a test is OFF until pressed, said in words');
  check(/2 of 2 fixtures have this/.test(tile('dimmer_sine').textContent), 'the tile says how many fixtures in scope have the test', tile('dimmer_sine').textContent);
  check(/Testing 2 fixtures/.test(q('[data-scope-targets]').textContent), 'the scope line names what is tested', q('[data-scope-targets]').textContent);

  const rev0 = A.get('ConsoleTests._state.known');
  click(tile('dimmer_sine'));
  await until('the dimmer test ON', () => tile('dimmer_sine') && tile('dimmer_sine').getAttribute('aria-pressed') === 'true');
  const dimmer = { kind: 'dimmer_sine', rateHz: 0, min: 0, max: 255, target: '', direction: '', value: 128, on: true, waveform: 'sine', offsetMin: 0, offsetMax: 0 };
  check(same(lastPost(SET).body, { tests: [dimmer] }), 'turning a test ON posts /api/tests/set {tests:[the full spec]}', lastPost(SET).body);
  check(lastPost(SET).headers['X-Benny-Tests'] === String(rev0), 'the write carries the tests revision this browser last saw (X-Benny-Tests)', [lastPost(SET).headers, rev0]);
  check(!!lastPost(SET).headers['X-Benny-Show'], 'tests writes carry the show-guard token (X-Benny-Show)');
  check(/ON/.test(tile('dimmer_sine').textContent) && /2 of 2 testing/.test(tile('dimmer_sine').textContent), 'the tile says ON and "2 of 2 testing"', tile('dimmer_sine').textContent);
  let sv = await server('GET', '/api/tests');
  check(sv.adHoc.length === 1 && sv.adHoc[0].kind === 'dimmer_sine' && sv.owner === 'tests', 'the server holds the ad-hoc test and owns the tests layer', sv.adHoc);

  click(q('[data-test-more="dimmer_sine"]'));
  await until('the settings', () => q('[data-param-choice="dimmer_sine:waveform:snap"]'));
  click(q('[data-param-choice="dimmer_sine:waveform:snap"]'));
  await until('the waveform change', () => posts(SET).length === 2);
  check(same(lastPost(SET).body, { tests: [Object.assign({}, dimmer, { rateHz: lastPost(SET).body.tests[0].rateHz, waveform: 'snap' })] }) && lastPost(SET).body.tests[0].waveform === 'snap',
    'a parameter edit resends the test with that field changed (waveform snap)', lastPost(SET).body);
  await until('the max slider', () => q('[data-param="dimmer_sine:max"]'));
  change(q('[data-param="dimmer_sine:max"]'), 200);
  await until('the max change', () => posts(SET).length === 3);
  check(lastPost(SET).body.tests[0].max === 200 && lastPost(SET).body.tests[0].waveform === 'snap', 'the Max level slider posts max 200 and keeps the waveform', lastPost(SET).body);
  await until('the per-fixture button', () => q('[data-test-fixtures="dimmer_sine"]'));
  click(q('[data-test-fixtures="dimmer_sine"]'));
  await until('the per-fixture list', () => q('[data-fixture-status="' + ids.B1 + '"]'));
  check(/B1/.test(q('[data-fixture-status="' + ids.B1 + '"]').textContent) && /testing/.test(q('[data-fixture-status="' + ids.B1 + '"]').textContent), 'per-fixture status is said in words ("B1 · testing")', q('[data-fixture-status="' + ids.B1 + '"]').textContent);

  click(tile('dimmer_sine'));
  await until('the dimmer test OFF', () => tile('dimmer_sine').getAttribute('aria-pressed') === 'false');
  check(same(lastPost(SET).body, { tests: [] }), 'turning the last test OFF posts {tests:[]}', lastPost(SET).body);

  click(q('[data-scope-kind="all"]'));
  await until('scope all', () => /Testing 5 fixtures/.test(q('[data-scope-targets]').textContent));
  check(same(lastPost(SET).body, { tests: [], scope: { kind: 'all' } }), 'the scope picker posts set {tests, scope:{kind:all}}', lastPost(SET).body);
  check(q('[data-scope-kind="all"]').getAttribute('aria-pressed') === 'true', 'the chosen scope is pressed');
  click(q('[data-scope-kind="selection"]'));
  await until('scope selection', () => /Testing 2 fixtures/.test(q('[data-scope-targets]').textContent));
  check(same(lastPost(SET).body, { tests: [], scope: { kind: 'selection' } }), 'back to the selection scope', lastPost(SET).body);

  change(q('[data-fade]'), 2000);
  await until('the fade', () => posts(T + 'fade').length === 1);
  check(same(lastPost(T + 'fade').body, { fadeMs: 2000 }), 'the fade picker posts fade {fadeMs:2000}', lastPost(T + 'fade').body);
  await until('the fade echoed', () => q('[data-fade]').value === '2000');

  // Another browser turns a test on: it arrives here by the broadcast alone.
  const remote = { kind: 'dimmer_toggle', rateHz: 0, min: 0, max: 255, target: '', direction: '', value: 0, on: true, waveform: 'sine', offsetMin: 0, offsetMax: 0 };
  const getsBefore = gets('/api/tests');
  await server('POST', SET, { tests: [remote] });
  await until('the other browser\'s test to show here', () => tile('dimmer_toggle') && tile('dimmer_toggle').getAttribute('aria-pressed') === 'true');
  check(gets('/api/tests') > getsBefore, 'a "tests" broadcast makes this browser re-read GET /api/tests');
  check(/1 test on/.test(q('[data-tests-summary]').textContent), 'the panel head says "1 test on"', q('[data-tests-summary]').textContent);

  // A stale write is refused and said in words.
  A.get('ConsoleTests._state.known = 1');
  click(tile('dimmer_sine'));
  await until('the stale refusal', () => /Not done/.test(panel().querySelector('[data-tests-status]').textContent));
  check(/another browser/.test(panel().querySelector('[data-tests-status]').textContent), 'a write on a stale revision is refused (409) and said in a sentence', panel().querySelector('[data-tests-status]').textContent);
  sv = await server('GET', '/api/tests');
  check(sv.adHoc.length === 1 && sv.adHoc[0].kind === 'dimmer_toggle', 'the refused write changed nothing on the server', sv.adHoc);
  await until('the view re-read after the refusal', () => A.get('ConsoleTests._state.known') === sv.revision);

  // --- sequence editor ---------------------------------------------------
  click(q('[data-seq-new]'));
  await until('the editor', () => q('[data-seq-editor]'));
  type(q('[data-seq-name]'), 'Check');
  type(q('[data-step-name="0"]'), 'Dimmers');
  check(!!q('[data-step-test="0:dimmer_toggle"]'), 'a new step starts with the tests that are on now');
  change(q('[data-step-advance="0"]'), 'auto');
  await until('the seconds field', () => q('[data-step-seconds="0"]'));
  type(q('[data-step-seconds="0"]'), '2');
  click(q('[data-step-add]'));
  await until('step 2', () => q('[data-step-editor="1"]'));
  type(q('[data-step-name="1"]'), 'Position');
  change(q('[data-step-scope="1"]'), 'all');
  await until('the all-fixtures catalog', () => { const p = q('[data-step-test-pick="1"]'); return p && !p.disabled && p.value; });
  click(q('[data-step-test-remove="1:0"]'));
  await until('the copied test removed', () => !q('[data-step-test="1:dimmer_toggle"]'));
  const pick = q('[data-step-test-pick="1"]').value;
  const avAll = (await server('GET', '/api/tests?kind=all')).available.find(a => a.id === pick);
  check(!!avAll, 'the step test picker offers the all-fixtures catalog (GET /api/tests?kind=all)', pick);
  click(q('[data-step-test-add="1"]'));
  await until('the picked test added', () => q('[data-step-test="1:' + pick + '"]'));
  change(q('[data-step-fade="1"]'), 500);
  click(q('[data-seq-save]'));
  await until('sequence-save', () => posts(T + 'sequence-save').length === 1);
  const spec = a => ({ kind: a.kind, rateHz: 0, min: 0, max: 255, target: a.target || '', direction: A.get('ConsoleTests').paramsFor(a.kind).direction ? 'cw' : '', value: 128, on: true, waveform: 'sine', offsetMin: 0, offsetMax: 0 });
  const wantSeq = { name: 'Check', steps: [
    { name: 'Dimmers', tests: [remote], scope: { kind: 'selection', group: '', layer: '' }, fadeMs: null, advance: { mode: 'auto', seconds: 2 } },
    { name: 'Position', tests: [spec(avAll)], scope: { kind: 'all', group: '', layer: '' }, fadeMs: 500, advance: { mode: 'manual', seconds: 0 } },
  ] };
  check(same(lastPost(T + 'sequence-save').body, wantSeq), 'Save posts sequence-save {name, steps:[{name, tests, scope, fadeMs, advance}]} exactly', lastPost(T + 'sequence-save').body);
  await until('the editor to close and the list to show the sequence', () => !q('[data-seq-editor]') && q('[data-seq-run]'));
  sv = await server('GET', '/api/tests');
  const seq = sv.sequences.find(s => s.name === 'Check');
  check(!!seq && seq.steps.length === 2, 'the server stored the two-step sequence');

  click(q('[data-seq-rename="' + seq.id + '"]'));
  await until('the rename dialog', () => document.querySelector('[data-ask] [data-ask-input]'));
  check(posts(T + 'sequence-rename').length === 0, 'Rename asks in an in-app dialog first (no native prompt)');
  document.querySelector('[data-ask-input]').value = 'Rig check';
  click(document.querySelector('[data-ask-ok]'));
  await until('sequence-rename', () => posts(T + 'sequence-rename').length === 1);
  check(same(lastPost(T + 'sequence-rename').body, { id: seq.id, name: 'Rig check' }), 'Rename posts sequence-rename {id, name}', lastPost(T + 'sequence-rename').body);
  await until('the renamed sequence', () => /Rig check/.test(q('[data-seq="' + seq.id + '"]').textContent));

  // --- run controls -------------------------------------------------------------
  const B = browser(['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-tests.js']);
  await until('browser B to connect', () => B.get('ProgrammerSync.state()'));
  const bRoot = B.document.getElementById('consoleRoot');
  B.get('ConsoleTests').mount(bRoot, { layers: () => [] });
  await until('browser B\'s panel', () => bRoot.querySelector('[data-seq-run]'));

  click(q('[data-seq-run="' + seq.id + '"]'));
  await until('run-start', () => posts(T + 'run-start').length === 1);
  check(same(lastPost(T + 'run-start').body, { id: seq.id }), 'Run posts run-start {id}', lastPost(T + 'run-start').body);
  await until('the run bar', () => q('[data-run]'));
  check(/Step 1 of 2 · Dimmers/.test(q('[data-run-step-word]').textContent), 'the run bar says "Step 1 of 2 · Dimmers"', q('[data-run-step-word]').textContent);
  check(/Next step in \d+\.\d s/.test(q('[data-run-remaining]').textContent), 'an auto step shows its time left in words', q('[data-run-remaining]').textContent);
  check(q('[data-run-step="0"]').getAttribute('aria-current') === 'step' && /NOW/.test(q('[data-run-step="0"]').textContent), 'the current step is marked NOW and aria-current, not by colour');
  check(tile('dimmer_toggle').disabled, 'while a sequence runs the test toggles are locked (the step chooses the tests)');
  await until('browser B to see the run', () => bRoot.querySelector('[data-run-step-word]'));
  check(/Step 1 of 2 · Dimmers/.test(bRoot.querySelector('[data-run-step-word]').textContent) && /Next step in/.test(bRoot.querySelector('[data-run-remaining]').textContent),
    'a second browser shows the running step and the time left (tests broadcast)', bRoot.querySelector('[data-run-step-word]').textContent);

  click(q('[data-run-pause]'));
  await until('paused', () => q('[data-run-resume]'));
  check(same(lastPost(T + 'run-pause').body, {}) && /Paused with \d+\.\d s left/.test(q('[data-run-remaining]').textContent), 'Pause posts run-pause {} and says "Paused with … left"', q('[data-run-remaining]').textContent);
  click(q('[data-run-resume]'));
  await until('resumed', () => q('[data-run-pause]'));
  check(same(lastPost(T + 'run-resume').body, {}), 'Resume posts run-resume {}');
  click(q('[data-run-next]'));
  await until('step 2', () => /Step 2 of 2 · Position/.test(q('[data-run-step-word]').textContent));
  check(same(lastPost(T + 'run-next').body, {}), 'Next posts run-next {}');
  check(/Manual step/.test(q('[data-run-remaining]').textContent) && q('[data-run-next]').disabled, 'a manual last step says so and Next is disabled at the end', q('[data-run-remaining]').textContent);
  await until('browser B at step 2', () => /Step 2 of 2 · Position/.test(bRoot.querySelector('[data-run-step-word]').textContent));
  check(/Step 2 of 2/.test(bRoot.querySelector('[data-run-step-word]').textContent), 'browser B follows to step 2');
  click(q('[data-run-back]'));
  await until('back to step 1', () => /Step 1 of 2/.test(q('[data-run-step-word]').textContent));
  check(same(lastPost(T + 'run-back').body, {}), 'Back posts run-back {}');
  change(q('[data-run-jump-step]'), 1);
  click(q('[data-run-jump]'));
  await until('jumped', () => posts(T + 'run-jump').length === 1);
  check(same(lastPost(T + 'run-jump').body, { step: 1 }), 'Jump posts run-jump {step}', lastPost(T + 'run-jump').body);
  await until('step 2 again', () => /Step 2 of 2/.test(q('[data-run-step-word]').textContent));
  sv = await server('GET', '/api/tests');
  check(sv.run.active && sv.run.step === 1 && sv.run.stepName === 'Position', 'the server is on step 2 (index 1)', sv.run);
  click(q('[data-run-stop]'));
  await until('stopped', () => !q('[data-run]'));
  check(same(lastPost(T + 'run-stop').body, {}), 'Stop posts run-stop {}');
  check(/ended: stopped/.test(q('[data-run-ended]').textContent), 'after Stop the panel says the run ended: stopped', q('[data-run-ended]').textContent);
  check(posts(T + 'run-start').concat(posts(T + 'run-next'), posts(T + 'run-back'), posts(T + 'run-jump'), posts(T + 'run-pause'), posts(T + 'run-resume'), posts(T + 'run-stop')).every(c => /^\d+$/.test(c.headers['X-Benny-Tests'] || '')),
    'every run control carries X-Benny-Tests');

  click(q('[data-seq-delete="' + seq.id + '"]'));
  await until('the delete dialog', () => document.querySelector('[data-ask] [data-ask-ok]'));
  check(posts(T + 'sequence-delete').length === 0, 'Delete asks in an in-app dialog first (no native confirm)');
  click(document.querySelector('[data-ask-ok]'));
  await until('sequence-delete', () => posts(T + 'sequence-delete').length === 1);
  check(same(lastPost(T + 'sequence-delete').body, { id: seq.id }), 'Delete posts sequence-delete {id}');
  await until('the list empty', () => !q('[data-seq="' + seq.id + '"]'));

  // ---------------------------------------------------------------- part 2
  const LAY = '/api/patch/layout';
  const chips = () => document.querySelectorAll('[data-layer-chip]').map(c => c.textContent);
  let g0 = gets(LAY);
  await server('POST', LAY + '/layer-create', { name: 'Remote' });
  await until('the other browser\'s layer to arrive', () => chips().some(t => /Remote/.test(t)));
  check(gets(LAY) > g0, 'another browser\'s layout edit makes this Console re-read GET /api/patch/layout (layout broadcast)');
  check(chips().some(t => /Remote/.test(t)), 'the new layer is on the grid without pressing Reload', chips());

  click(document.querySelector('[data-edit-toggle]'));
  await until('edit mode', () => document.querySelector('[data-layer-create]'));
  await sleep(150);
  g0 = gets(LAY);
  click(document.querySelector('[data-layer-create]'));
  await until('the layer dialog', () => document.querySelector('[data-ask] [data-ask-input]'));
  document.querySelector('[data-ask-input]').value = 'Local';
  click(document.querySelector('[data-ask-ok]'));
  await until('the local layer', () => chips().some(t => /Local/.test(t)));
  await sleep(400);
  check(gets(LAY) === g0, 'this browser\'s own layout edit is applied from its answer and NOT re-read when its broadcast arrives', [g0, gets(LAY)]);
  check(typeof A.get('ConsoleScreen._state.layout.revision') === 'number', 'the Console holds the layout revision from the answer');
  click(document.querySelector('[data-edit-done]'));

  A.get('ConsoleScreen').onLeaveScreen();
  g0 = gets(LAY);
  await server('POST', LAY + '/layer-create', { name: 'While away' });
  await sleep(300);
  check(gets(LAY) === g0, 'a Console that is not shown does not re-read on a layout broadcast');
  A.get('ConsoleScreen').onEnterScreen();
  await until('the layer made while away', () => chips().some(t => /While away/.test(t)));
  check(chips().some(t => /While away/.test(t)), 'entering the Console again shows the change made while away');

  // ---------------------------------------------------------------- part 3
  await A.get('ProgrammerSync').act('select', { action: 'set', targets: [{ entryId: ids.B1, cell: '' }] });
  await until('the summary', () => /1 selected/.test(document.querySelector('[data-sel-count]').textContent));
  const moreBtn = document.querySelector('[data-summary-more-toggle]');
  const more = document.querySelector('[data-summary-more]');
  check(!!moreBtn && moreBtn.getAttribute('aria-expanded') === 'false', 'the selection bar has a More toggle (collapsed)');
  check(!!more.querySelector('[data-store-group]') && !!more.querySelector('[data-highlight]') && !!more.querySelector('[data-locate]'), 'Store group, Highlight and Locate sit inside the More menu');
  check(!!document.querySelector('[data-summary] [data-clear-open]') && !more.querySelector('[data-clear-open]'), 'Clear… stays on the bar itself (I2d §15: it opens the scope chooser)');
  click(moreBtn);
  await until('More open', () => document.querySelector('[data-summary-more-toggle]').getAttribute('aria-expanded') === 'true');
  check(document.querySelector('[data-summary-more]').classList.contains('is-open'), 'More opens the menu (aria-expanded=true)');
  click(document.querySelector('[data-summary-more] [data-locate]'));
  await until('locate posted', () => posts('/api/programmer/locate').length === 1);
  check(!document.querySelector('[data-summary-more]').classList.contains('is-open'), 'picking a tool closes the menu');

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
