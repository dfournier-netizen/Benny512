// console_k4_test.js — K4: the Tests tab (W1 component-specs §9; owner
// 2026-10-18: Tests its own tab; W1b: fixture controls only on the main
// Console; <1024 px unchanged). Runs the LITERAL app.js, ui.js,
// console-tests.js, console-windows.js and console.js in the real
// index.html (minidom.js). The other screens, Api and ProgrammerSync are
// stand-ins; matchMedia switches "narrow" (below 1024 px) and "wide".
//
// WHAT IT PROTECTS
//  1. A Tests item in the Show nav group right after Console, wide only;
//     the phone bar is unchanged.
//  2. The Tests tab is the same workspace (grid, toolbar, selection bar)
//     with the Tests panel open on the right and no Controls, windows or
//     Windows menu; layout editing is off there.
//  3. One selection: switching Console <-> Tests keeps it and writes
//     nothing to the programmer.
//  4. The selection bar's Tests goes to the Tests tab from 1024 px.
//  5. Narrower than 1024 px Tests stays inside the Console as before; a
//     Tests tab left open (or remembered) becomes the Console.
'use strict';
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { makeWindow, Event } = require('./minidom.js');
const JS = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + JSON.stringify(detail) : ''));
}
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const tick = () => new Promise(r => setTimeout(r, 0));

const fx = (id, fid) => ({ entryId: id, name: 'Wash ' + fid, fixtureType: 'Maker Wash', shortType: 'Wash', fixtureNumber: fid, profiled: true, footprint: 8, unaddressable: [], cells: [], parameters: [] });
const fixtures = [fx('W1', '201'), fx('W2', '202')];
const items = fixtures.map((f, i) => ({ id: 'it-' + f.entryId, kind: 'entry', ref: f.entryId, layer: 'truss', col: i * 2, row: 0, w: 1, h: 1, rot: 0, mode: 'auto', order: i }));
const layout = {
  revision: 1, active: true, name: 'K4 show',
  layout: { layers: [{ id: 'truss', name: 'Truss', zKnown: true, zMin: 6000, zMax: 6000 }, { id: 'unplaced', name: 'Unplaced', system: true }], items },
  entries: fixtures.map(f => ({ id: f.entryId, name: f.name, fixtureType: f.fixtureType, fixtureNumber: f.fixtureNumber, itemId: 'it-' + f.entryId })),
  groups: [], unplaced: [], stale: [], limits: {}, objectTypes: [],
};

function boot(wide, store) {
  const { window, document, CustomEvent } = makeWindow();
  if (store) Object.entries(store).forEach(([k, v]) => window.localStorage.setItem(k, v));
  document.loadHTML(fs.readFileSync(path.join(JS, '..', 'index.html'), 'utf8'));
  const posts = [], subs = [];
  const state = { revision: 1, selection: [], highlight: { on: false }, storedGroups: [] };
  const view = t => { const f = fixtures.find(x => x.entryId === t.entryId); return { entryId: t.entryId, cell: '', name: f.name, cellName: '', cellIndex: 0 }; };
  const ProgrammerSync = {
    state: () => state, refresh: () => Promise.resolve(state), onChange: fn => subs.push(fn),
    act: (action, body) => {
      posts.push({ action, body });
      if (action === 'select') state.selection = (body.action === 'none' ? [] : body.targets || []).map(view);
      state.revision++; subs.forEach(fn => fn(state)); return Promise.resolve(state);
    },
  };
  const Api = {
    getSettings: async () => ({ artnetStartUniverse: 0 }),
    getPatch: async () => ({}),
    getLayout: async () => JSON.parse(JSON.stringify(layout)),
    getProgrammerFixtures: async () => ({ revision: 1, fixtures: JSON.parse(JSON.stringify(fixtures)) }),
    layoutAction: async () => JSON.parse(JSON.stringify(layout)),
    getTests: async () => { throw new Error('no tests server in this harness'); },
    testsAction: async () => { throw new Error('no tests server in this harness'); },
  };
  const screen = () => ({ init() {}, onEnterScreen() {}, onLeaveScreen() {} });
  const media = { wide };
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, Map, Set, window, document, Event, CustomEvent,
    getComputedStyle: window.getComputedStyle, localStorage: window.localStorage, Api, ProgrammerSync, Live: { on() {} },
    matchMedia: q => ({ matches: /min-width: 1024px/.test(q) ? media.wide : false }),
    NodesScreen: screen(), DevicesScreen: screen(), PatchScreen: screen(), AnalyzerScreen: screen(), WalkScreen: screen(),
    SettingsScreen: screen(), Workspace: screen(), Faders: screen(),
  });
  window.addEventListener = window.addEventListener.bind(window);
  for (const f of ['ui.js', 'console-tests.js', 'console-windows.js', 'console.js', 'app.js']) {
    const p = path.join(JS, f);
    if (fs.existsSync(p)) vm.runInContext(fs.readFileSync(p, 'utf8'), ctx, { filename: f });
  }
  vm.runInContext('this.ConsoleScreen = ConsoleScreen;', ctx);
  return { ctx, window, document, posts, media, state, ProgrammerSync };
}
const settle = async (B, sel) => { for (let i = 0; i < 40 && !B.document.querySelector(sel); i++) await tick(); for (let i = 0; i < 5; i++) await tick(); };

(async () => {
  // ---- 1-4. wide -------------------------------------------------------------------
  const B = boot(true, { 'benny512.tab': 'console' });
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const navBtn = (nav, tab) => q('#' + nav + ' [data-tab="' + tab + '"]');
  await settle(B, '[data-select-entry="W1"]');
  q('.b5-con-main').clientWidth = 1400; q('.b5-con-main').clientHeight = 580;

  const show = [...q('#tabs .b5-nav__group').querySelectorAll('[data-tab]')].map(b => b.getAttribute('data-tab'));
  check(same(show, ['patch', 'console', 'tests', 'walk']), 'the Show nav group reads Patch, Console, Tests, Rig Walk', show);
  check(!!navBtn('tabs', 'tests') && navBtn('tabs', 'tests').classList.contains('b5-nav__item--wide') && /Tests/.test(navBtn('tabs', 'tests').textContent), 'the Tests item is marked wide-only (CSS hides it below 1024 px)');
  check(!navBtn('tabsMobile', 'tests'), 'the phone nav bar is unchanged (no Tests item)');
  check(q('#screen-console').contains(q('#consoleRoot')) && q('#screen-console').classList.contains('active'), 'the Console starts on its own screen');
  check(!!q('[data-test-pane]') && q('[data-test-pane]').contains(q('[data-tests-region]')), 'from 1024 px the Tests panel lives in the Tests pane, not under Controls');
  const winIds = [...document.querySelectorAll('[data-win]')].map(w => w.getAttribute('data-win'));
  check(!winIds.includes('tests'), 'Tests is no longer a Console window', winIds);

  await B.ProgrammerSync.act('select', { action: 'set', targets: [{ entryId: 'W1', cell: '' }] });
  const before = posts.length;
  click(navBtn('tabs', 'tests'));
  await settle(B, '#screen-tests [data-select-entry="W1"]');
  check(q('#screen-tests').classList.contains('active') && q('#screen-tests').contains(q('#consoleRoot')), 'the Tests tab shows the same workspace, moved into the Tests screen');
  check(document.body.classList.contains('b5-tests-mode'), 'the page is in Tests mode (CSS: Tests pane on the right; no dock, windows or Windows menu)');
  check(!!q('#screen-tests [data-select-entry="W1"]') && q('#screen-tests [data-gridbox]') === q('[data-gridbox]'), 'the grid is on the Tests tab — one grid, not a copy');
  const tb = q('[data-tests-body]');
  check(!!tb && !tb.hidden, 'the Tests panel is open on the Tests tab');
  check(B.window.localStorage.getItem('benny512.tab') === 'tests', 'the tab is remembered');
  const vm1 = q('[data-view-menu]'); const trig = vm1.classList.contains('b5-menu__trigger') ? vm1 : vm1.querySelector('.b5-menu__trigger') || vm1.closest('.b5-menu').querySelector('.b5-menu__trigger');
  click(trig);
  const edit = trig.closest('.b5-menu').querySelector('[data-edit-toggle]');
  check(!!edit && edit.disabled && /on the Console/.test(edit.title || edit.getAttribute('title') || ''), 'Edit layout is off on the Tests tab and says where it is', edit && edit.title);
  click(trig);

  click(navBtn('tabs', 'console'));
  await settle(B, '#screen-console [data-select-entry="W1"]');
  check(q('#screen-console').contains(q('#consoleRoot')) && !document.body.classList.contains('b5-tests-mode'), 'back on the Console the workspace is home and out of Tests mode');
  check(same(B.state.selection.map(x => x.entryId), ['W1']) && posts.length === before, 'switching Console <-> Tests keeps the selection and writes nothing', posts.slice(before).map(p => p.action));

  const tj = q('[data-summary] [data-tests-jump]');
  if (tj) click(tj);
  await settle(B, '#screen-tests [data-select-entry="W1"]');
  check(!!tj && q('#screen-tests').classList.contains('active') && document.activeElement === q('[data-test-pane]'), 'the selection bar\'s Tests goes to the Tests tab and puts focus on its panel');

  // Layout editing on the Console ends when the Tests tab opens.
  click(navBtn('tabs', 'console'));
  await settle(B, '#screen-console [data-select-entry="W1"]');
  click(trig);
  click(trig.closest('.b5-menu').querySelector('[data-edit-toggle]'));
  check(B.ctx.ConsoleScreen._state.edit === true, '(editing the layout on the Console)');
  click(navBtn('tabs', 'tests'));
  await settle(B, '#screen-tests [data-select-entry="W1"]');
  check(B.ctx.ConsoleScreen._state.edit === false, 'opening the Tests tab ends layout editing');

  // ---- 5. narrower than 1024 px ----------------------------------------------------------
  B.media.wide = false;
  B.window.dispatchEvent(new Event('resize'));
  await settle(B, '#screen-console [data-select-entry="W1"]');
  const side = q('.b5-con-pane--side');
  const kids = [...side.childNodes].filter(n => n.nodeType === 1).map(n => n.hasAttribute('data-controls-region') ? 'controls' : n.hasAttribute('data-tests-region') ? 'tests' : n.hasAttribute('data-tools-region') ? 'tools' : null).filter(Boolean);
  check(q('#screen-console').classList.contains('active') && q('#screen-console').contains(q('#consoleRoot')), 'going below 1024 px on the Tests tab goes to the Console');
  check(same(kids, ['controls', 'tests', 'tools']), 'and Tests is back under Controls, as before K4', kids);

  const N = boot(false, { 'benny512.tab': 'tests' });
  await settle(N, '[data-select-entry="W1"]');
  check(N.document.querySelector('#screen-console').classList.contains('active') && N.document.querySelector('.b5-con-pane--side').contains(N.document.querySelector('[data-tests-region]')), 'a remembered Tests tab on a narrow screen opens the Console, with Tests inside it');
  const W = boot(true, { 'benny512.tab': 'tests' });
  await settle(W, '#screen-tests [data-select-entry="W1"]');
  check(W.document.querySelector('#screen-tests').classList.contains('active') && W.document.querySelector('#screen-tests').contains(W.document.querySelector('#consoleRoot')), 'a remembered Tests tab on a wide screen opens the Tests tab');

  console.log(failures ? failures + ' FAILURE(S)' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
