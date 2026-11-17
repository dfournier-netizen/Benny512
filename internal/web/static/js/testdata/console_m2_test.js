// console_m2_test.js — M2 (owner 2026-11-17): the View menu's "Snap to half
// squares" and "Grid" switches, both on by default and remembered per browser.
// Snap off draws stored positions exactly and moves freely (drag, arrows 0.1
// square, Shift+arrow 1 square); snap on keeps M1 behaviour except that
// Shift+arrow is one square. Literal ui.js and console.js in the real
// index.html (minidom.js).
//
// Expected pixels at 100 px per square (slider 100), Snap OFF, by hand. The
// plan's left edge is colFrom = min(every drawn column, 0) = 0 (the owner's
// note said 0.1, but renderGrid anchors at column 0 whenever no column is
// negative), and the top edge is the min row = 0 (A is at row 0):
//   A (0.2, 0)    -> left 20,  top 0
//   B (0.3, 0.1)  -> left 30,  top 10
//   D (0.1, 0.05) -> left 10,  top 5
//   C (2.74, 1.26)-> left 274, top 126
// Arrows (edit mode, B stored at 0.3, 0.1): Snap off ArrowLeft 0.3-0.1 = 0.2;
// Shift+ArrowLeft 0.3-1 = -0.7. Snap on: B is drawn at (1, 0) (see M1 test),
// Shift+ArrowLeft -> col 0.
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

const P = (attribute, group) => ({ attribute, group, source: 'gdtf', virtual: false });
const fx = (id, fid) => ({ entryId: id, name: 'Spot ' + fid, fixtureType: 'Maker Spot', shortType: 'Spot', fixtureNumber: fid,
  profiled: true, footprint: 8, unaddressable: [], cells: [], parameters: [P('Dimmer', 'dimmer')] });
const fixtures = [fx('A', '1'), fx('B', '2'), fx('C', '3'), fx('D', '4')];
// Exact (fractional) positions as M1 stores them. A and B overlap; D sits
// exactly on A's spot but on top (order 3), and C is clear of everyone.
const at = { A: [0.2, 0, 0], B: [0.3, 0.1, 1], C: [2.74, 1.26, 2], D: [0.1, 0.05, 3] };
const items = fixtures.map(f => ({ id: 'it-' + f.entryId, kind: 'entry', ref: f.entryId, layer: 'truss', col: at[f.entryId][0], row: at[f.entryId][1], w: 1, h: 1, rot: 0, mode: 'auto', order: at[f.entryId][2] }));
items.push({ id: 'obj-1', kind: 'object', objectType: 'label', layer: 'truss', col: 3, row: 3, w: 1, h: 1, text: 'US', order: 9, geometry: { col: 3.3, row: 3.1 } });
const layout = {
  revision: 1, active: true, name: 'M2 show',
  layout: { layers: [{ id: 'truss', name: 'Truss', zKnown: true, zMin: 6000, zMax: 6000 }, { id: 'unplaced', name: 'Unplaced', system: true }], items, derived: true, cellMm: 500 },
  entries: fixtures.map(f => ({ id: f.entryId, name: f.name, fixtureType: f.fixtureType, fixtureNumber: f.fixtureNumber, itemId: 'it-' + f.entryId })),
  groups: [], unplaced: [], stale: [], limits: {}, objectTypes: [{ type: 'label', label: 'Label', shape: 'point' }],
};

function boot(store) {
  const { window, document } = makeWindow();
  if (store) Object.entries(store).forEach(([k, v]) => window.localStorage.setItem(k, v));
  document.loadHTML(fs.readFileSync(path.join(JS, '..', 'index.html'), 'utf8'));
  const posts = [], subs = [];
  const state = { revision: 1, selection: [], highlight: { on: false }, storedGroups: [] };
  const view = t => { const f = fixtures.find(x => x.entryId === t.entryId); const c = t.cell ? f.cells.find(x => x.id === t.cell) : null; return { entryId: t.entryId, cell: t.cell || '', name: f.name, cellName: c ? c.name : '', cellIndex: c ? c.index : 0 }; };
  const ProgrammerSync = {
    state: () => state, refresh: () => Promise.resolve(state), onChange: fn => subs.push(fn),
    act: (action, body) => {
      posts.push({ action, body: JSON.parse(JSON.stringify(body)) });
      if (action === 'select') state.selection = (body.action === 'none' ? [] : body.targets || []).map(view);
      if (action === 'highlight') state.highlight = { on: !!body.highlight };
      state.revision++;
      subs.forEach(fn => fn(state));
      return Promise.resolve(state);
    },
  };
  const Api = {
    getPatch: async () => ({}),
    getLayout: async () => JSON.parse(JSON.stringify(layout)),
    getProgrammerFixtures: async () => ({ revision: 1, fixtures: JSON.parse(JSON.stringify(fixtures)) }),
    layoutAction: async (action, body) => { posts.push({ action, body: JSON.parse(JSON.stringify(body || {})) }); return JSON.parse(JSON.stringify(layout)); },
  };
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, Map, Set, window, document, Event,
    getComputedStyle: window.getComputedStyle, localStorage: window.localStorage, Api, ProgrammerSync, Live: { on() {} },
  });
  for (const f of ['ui.js', 'console.js']) vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
  vm.runInContext('this.ConsoleScreen = ConsoleScreen;', ctx);
  return { ctx, window, document, posts, state, ProgrammerSync };
}

(async () => {
  const STORE_KEY = 'benny512.console.view';
  const B = boot();
  const { document, posts, window } = B;
  const q = s => document.querySelector(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const menuItem = (menu, item) => { const t = q('[' + menu + ']'); if (t.getAttribute('aria-expanded') !== 'true') click(t); return t.closest('.b5-menu').querySelector('[' + item + ']'); };
  const tile = id => q('[data-gridbox] [data-select-entry="' + id + '"]');
  const pos = id => { const st = tile(id).closest('[data-item-id]').style; return [st.getPropertyValue('left'), st.getPropertyValue('top')]; };
  const slide = v => { const s = q('[data-zoom-slider]'); s.value = String(v); s.dispatchEvent(new Event('input', { bubbles: true })); };
  const view = () => JSON.parse(window.localStorage.getItem(STORE_KEY) || '{}');
  const key = (el, k, extra) => el.dispatchEvent(new Event('keydown', Object.assign({ bubbles: true, key: k }, extra || {})));
  const lastMove = () => posts.filter(p => p.action === 'move').pop();
  const head = () => q('[data-layer-block="truss"] .b5-con-layername').textContent;
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('A'); i++) await tick();
  slide(100);

  const snapItem = menuItem('data-view-menu', 'data-snap-toggle'), gridItem = menuItem('data-view-menu', 'data-grid-toggle');
  check(!!snapItem && snapItem.getAttribute('aria-checked') === 'true', 'View menu has "Snap to half squares", checked by default', snapItem && snapItem.getAttribute('aria-checked'));
  check(!!gridItem && gridItem.getAttribute('aria-checked') === 'true', 'View menu has "Grid", checked by default', gridItem && gridItem.getAttribute('aria-checked'));
  check(!q('[data-gridbox]').classList.contains('is-grid-off'), 'grid is drawn by default');
  check(!/snapping off/.test(head()), 'layer heading does not mention snapping while it is on', head());

  // Snap off.
  click(snapItem);
  await tick();
  check(same(pos('A'), ['20px', '0px']), 'snap off: A drawn exactly at (0.2, 0)', pos('A'));
  check(same(pos('B'), ['30px', '10px']), 'snap off: B drawn exactly at (0.3, 0.1), overlapping A', pos('B'));
  check(same(pos('D'), ['10px', '5px']), 'snap off: D drawn exactly at (0.1, 0.05)', pos('D'));
  check(same(pos('C'), ['274px', '126px']), 'snap off: C drawn exactly at (2.74, 1.26)', pos('C'));
  check(view().snap === false, 'snap:false is remembered in localStorage', view());
  check(/snapping off — exact positions/.test(head()), 'layer heading says snapping off — exact positions', head());

  // Snap off, edit mode.
  click(menuItem('data-view-menu', 'data-edit-toggle'));
  for (let i = 0; i < 20 && !tile('B'); i++) await tick();
  const sOff = menuItem('data-view-menu', 'data-snap-toggle');
  check(sOff.getAttribute('disabled') === null && !sOff.disabled, 'Snap switch stays usable in edit mode');
  key(tile('B'), 'ArrowLeft');
  await tick(); await tick();
  let mv = lastMove();
  check(!!mv && same(mv.body, { id: 'it-B', col: 0.2, row: 0.1 }), 'snap off: ArrowLeft moves B 0.1 square from its stored spot (0.3 -> 0.2)', mv && mv.body);
  posts.length = 0;
  key(tile('B'), 'ArrowLeft', { shiftKey: true });
  await tick(); await tick();
  mv = lastMove();
  check(!!mv && same(mv.body, { id: 'it-B', col: -0.7, row: 0.1 }), 'snap off: Shift+ArrowLeft moves B one square (0.3 -> -0.7)', mv && mv.body);

  // Snap on again: B is drawn at (1, 0).
  click(menuItem('data-view-menu', 'data-snap-toggle'));
  for (let i = 0; i < 20 && !tile('B'); i++) await tick();
  await tick();
  check(view().snap === true, 'snap:true remembered again', view());
  posts.length = 0;
  key(tile('B'), 'ArrowLeft', { shiftKey: true });
  await tick(); await tick();
  mv = lastMove();
  check(!!mv && same(mv.body, { id: 'it-B', col: 0, row: 0 }), 'snap on: Shift+ArrowLeft moves B drawn at (1, 0) one square to col 0', mv && mv.body);

  // Grid.
  click(menuItem('data-view-menu', 'data-grid-toggle'));
  await tick();
  check(q('[data-gridbox]').classList.contains('is-grid-off'), 'Grid off adds is-grid-off to the gridbox');
  check(view().grid === false, 'grid:false is remembered', view());
  click(menuItem('data-view-menu', 'data-grid-toggle'));
  await tick();
  check(!q('[data-gridbox]').classList.contains('is-grid-off'), 'Grid on removes is-grid-off');
  check(view().grid === true, 'grid:true is remembered', view());

  // A new page with the saved view.
  click(menuItem('data-view-menu', 'data-snap-toggle'));
  click(menuItem('data-view-menu', 'data-grid-toggle'));
  await tick();
  const saved = JSON.stringify(view());
  const B2 = boot({ [STORE_KEY]: saved });
  const q2 = s => B2.document.querySelector(s);
  B2.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !q2('[data-gridbox] [data-select-entry="B"]'); i++) await tick();
  const t2 = q2('[data-view-menu]'); click(t2);
  const m2 = t2.closest('.b5-menu');
  check(m2.querySelector('[data-snap-toggle]').getAttribute('aria-checked') === 'false', 'a new page opens with snap off', saved);
  check(m2.querySelector('[data-grid-toggle]').getAttribute('aria-checked') === 'false', 'a new page opens with grid off', saved);
  check(q2('[data-gridbox]').classList.contains('is-grid-off'), 'a new page draws with is-grid-off');

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
