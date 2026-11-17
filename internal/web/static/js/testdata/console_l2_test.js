// console_l2_test.js — L2: the Console plot zooms smoothly, pans and turns
// (owner 2026-11-17). Runs the LITERAL ui.js and console.js in the real
// index.html (minidom.js); ProgrammerSync and Api are stand-ins. Same show
// as console_k2_test.js.
//
// WHAT IT PROTECTS
//  1. Zoom is a cell size in px, not four steps: a slider sits between − and
//     +, − / + step by x1.25, the floor is the touch minimum here (no fine
//     pointer in minidom), and a view saved by v0.3.0 (an index) still opens
//     at its size.
//  2. Below 56 px a tile is its number only (is-tiny); Fit may go that small.
//  3. Ctrl+wheel zooms; a plain wheel does not.
//  4. Rotate 90° right / left (View menu) turn the drawn plan a quarter turn
//     either way, in Plan and in Compact, are remembered, and are off while
//     editing.
//  5. A mouse drag on the plan pans it and does not select; a tap selects.
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
const fx = (id, fid, shortType, extra) => Object.assign({ entryId: id, name: shortType + ' ' + fid, fixtureType: 'Maker ' + shortType + ' long type name', shortType, fixtureNumber: fid,
  profiled: true, footprint: 8, unaddressable: [], cells: [], parameters: [P('Dimmer', 'dimmer'), P('ColorAdd_R', 'colour')] }, extra || {});
const fixtures = [
  fx('W1', '201', 'Wash'), fx('W2', '202', 'Wash'), fx('W3', '203', 'Wash'), fx('W5', '205', 'Wash'),
  fx('BAR', '401', 'Bar', { cells: [1, 2, 3].map(i => ({ id: 'c' + i, name: 'Pixel ' + i, index: i })) }),
  fx('NONUM', '', 'Spot'),
];
// A plan with gaps: columns 0, 10, 25 and rows 0, 7 are used; everything
// between is empty. Compact must close it to 3 columns x 2 rows.
const pos = { W1: [0, 0], W2: [10, 0], W3: [10, 7], W5: [25, 7], BAR: [0, 7], NONUM: [25, 0] };
const items = fixtures.map((f, i) => ({ id: 'it-' + f.entryId, kind: 'entry', ref: f.entryId, layer: 'truss', col: pos[f.entryId][0], row: pos[f.entryId][1], w: 1, h: 1, rot: 0, mode: 'auto', order: i }));
items.push({ id: 'obj-1', kind: 'object', objectType: 'label', layer: 'truss', col: 3, row: 3, w: 1, h: 1, text: 'US', order: 50 });
const layout = {
  revision: 1, active: true, name: 'K2 show',
  layout: { layers: [{ id: 'truss', name: 'Truss', zKnown: true, zMin: 6000, zMax: 6000 }, { id: 'unplaced', name: 'Unplaced', system: true }], items },
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
    layoutAction: async () => JSON.parse(JSON.stringify(layout)),
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
  const B = boot({ 'benny512.console.view': JSON.stringify({ spacing: 'plan', fit: false, names: true, zoom: 2 }) });
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const menuItem = (menu, item) => { const t = q('[' + menu + ']'); if (t.getAttribute('aria-expanded') !== 'true') click(t); return t.closest('.b5-menu').querySelector('[' + item + ']'); };
  const tile = id => q('[data-gridbox] [data-select-entry="' + id + '"]');
  const at = id => { const st = tile(id).closest('[data-item-id]').style; return [st.getPropertyValue('left'), st.getPropertyValue('top')]; };
  const saved = () => JSON.parse(B.window.localStorage.getItem('benny512.console.view'));
  const slider = () => q('[data-zoom-slider]');
  const slide = v => { slider().value = String(v); slider().dispatchEvent(new Event('input', { bubbles: true })); };
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('W1'); i++) await tick();
  check(!!tile('W1'), 'the grid draws the show');

  // ---- 1. continuous zoom --------------------------------------------------------
  check(same(at('W2'), ['1280px', '0px']), 'a view saved by v0.3.0 (zoom index 2) opens at its size, 128 px', at('W2'));
  const zoomKids = [...q('[data-zoom-in]').parentNode.childNodes].filter(n => n.nodeType === 1).map(n => ['data-fit', 'data-zoom-out', 'data-zoom-slider', 'data-zoom-in'].find(a => n.hasAttribute(a)));
  check(same(zoomKids, ['data-fit', 'data-zoom-out', 'data-zoom-slider', 'data-zoom-in']), 'the zoom group is Fit, −, slider, +', zoomKids);
  check(slider().getAttribute('type') === 'range' && slider().getAttribute('aria-label') === 'Zoom' && slider().value === '128', 'the slider is a labelled range showing the size drawn', slider().value);
  slide(70);
  check(same(at('W2'), ['700px', '0px']) && saved().cell === 70 && saved().fit === false, 'the slider sets any size in px, turns Fit off, and is remembered', [at('W2'), saved()]);
  click(q('[data-zoom-out]'));
  check(same(at('W2'), ['560px', '0px']), '− divides by 1.25 (70 -> 56 px)', at('W2'));
  for (let i = 0; i < 10; i++) click(q('[data-zoom-out]'));
  check(same(at('W2'), ['440px', '0px']) && q('[data-zoom-out]').disabled, '− stops at the floor (the 44 px touch minimum without a fine pointer) and then disables', at('W2'));
  slide(500);
  check(same(at('W2'), ['1600px', '0px']) && q('[data-zoom-in]').disabled, 'nothing goes past 160 px; + disables there', at('W2'));
  check(/^160 px per cell/.test(slider().getAttribute('aria-valuetext')), 'the slider says its size in words', slider().getAttribute('aria-valuetext'));

  // ---- 2. number-only tiles ------------------------------------------------------
  slide(50);
  check(q('[data-gridbox]').classList.contains('is-tiny') && tile('W1').querySelector('[data-fid]').textContent === '201', 'below 56 px the grid is is-tiny and the tile still carries its number', q('[data-gridbox]').className);
  slide(56);
  check(!q('[data-gridbox]').classList.contains('is-tiny'), 'from 56 px the full tile returns');

  // ---- 3. Ctrl+wheel -------------------------------------------------------------
  slide(100);
  q('[data-gridbox]').dispatchEvent(new Event('wheel', { bubbles: true, deltaY: -100, deltaMode: 0 }));
  await tick(); await tick();
  check(same(at('W2'), ['1000px', '0px']), 'a plain wheel does not zoom (it scrolls)', at('W2'));
  const ev = new Event('wheel', { bubbles: true, deltaY: -100, deltaMode: 0, ctrlKey: true, clientX: 0, clientY: 0 });
  q('[data-gridbox]').dispatchEvent(ev);
  await tick(); await tick();
  const w = parseFloat(at('W2')[0]) / 10;
  check(ev.defaultPrevented && w > 120 && w < 124, 'Ctrl+wheel zooms in (100 -> ~122 px for 100 units) and keeps the page from zooming', [ev.defaultPrevented, w]);

  // ---- 4. Rotate 90° -------------------------------------------------------------
  slide(100);
  click(menuItem('data-view-menu', 'data-rotate-toggle="cw"'));
  // Clockwise: (col, row) -> (-(row + 1), col). W2 at (10, 0) -> (-1, 10);
  // the leftmost drawn column is -8 (W3, W5, BAR at row 7), so left = 7 cells.
  check(same(at('W2'), ['700px', '1000px']) && same(at('W1'), ['700px', '0px']) && same(at('BAR'), ['0px', '0px']), 'Rotate turns the plan a quarter turn clockwise', [at('W1'), at('W2'), at('BAR')]);
  check(saved().rotate === 90 && /rotated 90° clockwise/.test(q('[data-layer-block="truss"] .b5-con-layername').textContent), 'it is remembered and the layer says so');
  check(/rotate\(90deg\)/.test(tile('W2').querySelector('.b5-con-glyph').style.getPropertyValue('transform')), 'the fixture glyph turns with the plan');
  click(q('[data-spacing="compact"]'));
  // Compact first (cols 0,10,25 -> 0,1,2; rows 0,7 -> 0,1), then turned:
  // W2 (1, 0) -> (-1, 1); leftmost column -2, so left = 1 cell, top = 1 cell.
  check(same(at('W2'), ['100px', '100px']) && same(at('W5'), ['0px', '200px']), 'Compact and Rotate together: closed up, then turned (short and wide)', [at('W2'), at('W5')]);
  click(q('[data-spacing="plan"]'));
  click(menuItem('data-view-menu', 'data-edit-toggle'));
  check(same(at('W2'), ['1000px', '0px']) && menuItem('data-view-menu', 'data-rotate-toggle="cw"').disabled, 'editing the layout draws the plan unturned and disables Rotate', at('W2'));
  click(q('[data-edit-done]'));
  check(same(at('W2'), ['700px', '1000px']), 'leaving edit mode returns to the remembered Rotate', at('W2'));
  click(menuItem('data-view-menu', 'data-rotate-toggle="ccw"'));
  // Counterclockwise: (col, row) -> (row, -(col + 1)). W2 (10, 0) -> (0, -11),
  // W1 (0, 0) -> (0, -1); the top drawn row is -26 (W5, NONUM at col 25).
  check(same(at('W2'), ['0px', '1500px']) && same(at('W1'), ['0px', '2500px']) && saved().rotate === 270 && /rotated 90° counterclockwise/.test(q('[data-layer-block="truss"] .b5-con-layername').textContent), 'Rotate 90° left turns it the other way (replacing right)', [at('W1'), at('W2')]);
  check(/rotate\(270deg\)/.test(tile('W2').querySelector('.b5-con-glyph').style.getPropertyValue('transform')), 'and the glyph turns left with it');
  click(menuItem('data-view-menu', 'data-rotate-toggle="ccw"'));
  check(same(at('W2'), ['1000px', '0px']) && saved().rotate === 0, 'choosing the checked direction again turns Rotate off');

  // ---- 5. drag to pan ------------------------------------------------------------
  const grid = q('[data-gridbox]');
  grid.scrollLeft = 300; grid.scrollTop = 200;
  const n0 = posts.length;
  const pt = (type, x, y) => tile('W1').dispatchEvent(new Event(type, { bubbles: true, pointerId: 7, pointerType: 'mouse', button: 0, clientX: x, clientY: y }));
  pt('pointerdown', 100, 100); pt('pointermove', 130, 120); pt('pointerup', 130, 120);
  click(tile('W1'));
  check(grid.scrollLeft === 270 && grid.scrollTop === 180, 'a mouse drag pans the plan with the pointer', [grid.scrollLeft, grid.scrollTop]);
  check(posts.length === n0, 'the click that ends a pan does not select', posts.slice(n0));
  await tick();
  pt('pointerdown', 100, 100); pt('pointerup', 102, 101);
  click(tile('W1'));
  await tick();
  check(posts.length === n0 + 1 && posts[n0].action === 'select', 'a press without travel is still a tap that selects', posts.slice(n0));

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
