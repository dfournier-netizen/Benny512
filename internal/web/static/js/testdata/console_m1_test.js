// console_m1_test.js — M1 (owner 2026-11-17, decision 0004): positions are
// stored exactly and may overlap; with snapping on the Console draws every
// fixture on the nearest half square and moves an overlapping one to the
// nearest free half-square spot; arrow keys move from the DRAWN spot in half
// squares. Literal ui.js and console.js in the real index.html (minidom.js).
//
// Expected spots, by hand (half squares; stacking order A, B, C, D):
//   A (0.2, 0)    -> (0, 0)        nearest half square, free
//   B (0.3, 0.1)  -> (0.5, 0) would overlap A (A covers 0..1); nearest free
//                    spot to (0.3, 0.1): (1, 0), d^2 = 0.49 + 0.01 = 0.50
//                    [(0.5, 1) is 0.04 + 0.81 = 0.85; (-1, 0) is 1.70]
//   C (2.74, 1.26) -> (2.5, 1.5)   free
//   D (0.1, 0.05) -> (0, 0) taken; nearest free to (0.1, 0.05):
//                    (0, 1) d^2 = 0.01 + 0.9025 = 0.9125
//                    [(-1, 0) is 1.21 + 0.0025 = 1.2125; (1, 1) is free but
//                     0.81 + 0.9025 = 1.7125]
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
  revision: 1, active: true, name: 'M1 show',
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
  const B = boot();
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const menuItem = (menu, item) => { const t = q('[' + menu + ']'); if (t.getAttribute('aria-expanded') !== 'true') click(t); return t.closest('.b5-menu').querySelector('[' + item + ']'); };
  const tile = id => q('[data-gridbox] [data-select-entry="' + id + '"]');
  const pos = id => { const st = tile(id).closest('[data-item-id]').style; return [st.getPropertyValue('left'), st.getPropertyValue('top')]; };
  const slide = v => { const s = q('[data-zoom-slider]'); s.value = String(v); s.dispatchEvent(new Event('input', { bubbles: true })); };
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('A'); i++) await tick();
  slide(100);
  // The plan's left edge is column 0 (no negative columns drawn), top row 0.
  check(same(pos('A'), ['0px', '0px']), 'A is drawn on its nearest half square (0, 0)', pos('A'));
  check(same(pos('B'), ['100px', '0px']), 'B, which overlaps A, is moved to the nearest free spot (1, 0)', pos('B'));
  check(same(pos('C'), ['250px', '150px']), 'C is drawn on its nearest half square (2.5, 1.5)', pos('C'));
  check(same(pos('D'), ['0px', '100px']), 'D, exactly on A, takes the nearest free spot (0, 1)', pos('D'));
  const obj = q('[data-item-id="obj-1"] .b5-con-obj__text');
  check(!!obj && obj.style.getPropertyValue('left') === '330px', 'objects are drawn as stored (3.3 squares), not snapped', obj && obj.style.getPropertyValue('left'));

  click(menuItem('data-view-menu', 'data-edit-toggle'));
  for (let i = 0; i < 20 && !tile('B'); i++) await tick();
  tile('B').dispatchEvent(new Event('keydown', { bubbles: true, key: 'ArrowLeft' }));
  await tick(); await tick();
  const mv = posts.filter(p => p.action === 'move').pop();
  check(!!mv && same(mv.body, { id: 'it-B', col: 0.5, row: 0 }), 'ArrowLeft moves B half a square from where it is DRAWN (1, 0) -> (0.5, 0), not from its stored 0.3', mv && mv.body);

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
