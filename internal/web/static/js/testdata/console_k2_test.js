// console_k2_test.js — K2: the Console workspace (W1 component-specs §2, §3,
// §5, §6, §7; owner 2026-10-19). Runs the LITERAL ui.js and console.js in the
// real index.html (minidom.js); ProgrammerSync and Api are stand-ins.
//
// WHAT IT PROTECTS
//  1. One toolbar row: layer chips (and the narrow-screen Layer selector),
//     Plan | Compact, Fit | − | +, Lasso, Add, Select menu, View menu.
//  2. Compact removes every empty row AND column (display only): fixtures
//     close up and keep their arrangement; the stored layout is untouched;
//     editing the layout always draws Plan.
//  3. Fit is on by default; − / + turn it off and step the zoom (x1.25, L2); the
//     choices are remembered per browser.
//  4. Tiles lead with the fixture number and the short type; Names on tiles
//     hides the type line only; HL appears on highlighted selected tiles.
//  5. The selection bar summarises by short type with CONTIGUOUS number
//     runs only, and the More menu presses the bar's own tool buttons.
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
  const B = boot();
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const qa = s => document.querySelectorAll(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const menuItem = (menu, item) => { const t = q('[' + menu + ']'); if (t.getAttribute('aria-expanded') !== 'true') click(t); return t.closest('.b5-menu').querySelector('[' + item + ']'); };
  const tile = id => q('[data-gridbox] [data-select-entry="' + id + '"]');
  const at = id => { const st = tile(id).closest('[data-item-id]').style; return [st.getPropertyValue('left'), st.getPropertyValue('top')]; };
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('W1'); i++) await tick();
  check(!!tile('W1'), 'the grid draws the show');

  // ---- 1. one toolbar row ------------------------------------------------------
  const row = q('.b5-con-tools');
  const order = [];
  const walk = n => n.childNodes.forEach(c => { if (c.nodeType !== 1) return; const a = ['data-layer-chip', 'data-layer-select', 'data-spacing', 'data-fit', 'data-zoom-out', 'data-zoom-slider', 'data-zoom-in', 'data-lasso-toggle', 'data-add-mode', 'data-select-menu', 'data-view-menu'].find(x => c.hasAttribute(x)); if (a) order.push(a + (a === 'data-spacing' ? '=' + c.getAttribute(a) : '')); else walk(c); });
  walk(row);
  const firstOf = [...new Set(order)];
  check(same(firstOf, ['data-layer-chip', 'data-layer-select', 'data-spacing=plan', 'data-spacing=compact', 'data-fit', 'data-zoom-out', 'data-zoom-slider', 'data-zoom-in', 'data-lasso-toggle', 'data-add-mode', 'data-select-menu', 'data-view-menu']),
    'one row: layers, Plan | Compact, Fit | − | +, Lasso, Add, Select, View', firstOf);
  check(!q('[data-select-all]') && !q('[data-ghost-toggle]') && !q('[data-edit-toggle]'), 'Select all / Ghosts / Edit layout are no longer loose toolbar buttons (they are menu items)');
  check(q('[data-spacing="plan"]').getAttribute('aria-pressed') === 'true' && q('[data-fit]').getAttribute('aria-pressed') === 'true', 'defaults: Plan and Fit are ON');
  const ls = q('[data-layer-select]');
  check(!!ls && same(ls.querySelectorAll('option').map(o => o.textContent), ['All layers', 'Truss · 6', 'Unplaced · 0']), 'the narrow-screen Layer selector lists All layers and each layer with its count', ls && ls.querySelectorAll('option').map(o => o.textContent));

  // ---- 2. Plan vs Compact --------------------------------------------------------
  check(same(at('W2'), ['960px', '0px']) && same(at('W5'), ['2400px', '672px']), 'Plan: the plan\'s own spacing (96 px cells: column 10 at 960 px, row 7 at 672 px)', [at('W2'), at('W5')]);
  check(!!q('[data-gridbox] .b5-con-obj'), 'Plan draws the objects (truss, labels)');
  click(q('[data-spacing="compact"]'));
  check(same(at('W1'), ['0px', '0px']) && same(at('W2'), ['96px', '0px']) && same(at('NONUM'), ['192px', '0px']) && same(at('BAR'), ['0px', '96px']) && same(at('W3'), ['96px', '96px']) && same(at('W5'), ['192px', '96px']),
    'Compact: empty rows and columns removed — 3 columns x 2 rows, every fixture keeps its row and its column order', ['W1', 'W2', 'NONUM', 'BAR', 'W3', 'W5'].map(at));
  check(!q('[data-gridbox] .b5-con-obj'), 'Compact leaves the objects out (their positions mean nothing packed)');
  check(/compact — empty rows and columns removed/.test(q('.b5-con-layerhead').textContent), 'the layer head says it is compact, in words', q('.b5-con-layerhead').textContent);
  check(same(B.ctx.ConsoleScreen._state.layout.layout.items.find(it => it.ref === 'W5').col, 25) && !B.posts.some(p => p.action !== 'select'), 'Compact is display only: the stored layout is unchanged and nothing is posted');
  check(JSON.parse(B.window.localStorage.getItem('benny512.console.view')).spacing === 'compact', 'the spacing choice is remembered in this browser');
  click(menuItem('data-view-menu', 'data-edit-toggle'));
  check(same(at('W5'), ['2400px', '672px']) && q('[data-spacing="compact"]').disabled && q('[data-spacing="plan"]').getAttribute('aria-pressed') === 'true', 'editing the layout always draws Plan (Compact off and disabled while editing)', at('W5'));
  click(menuItem('data-view-menu', 'data-edit-toggle'));
  check(same(at('W5'), ['192px', '96px']), 'leaving edit mode returns to the remembered Compact');

  // ---- 3. Fit and zoom -------------------------------------------------------------
  click(q('[data-zoom-in]'));
  check(q('[data-fit]').getAttribute('aria-pressed') === 'false' && same(at('W2'), ['120px', '0px']), '+ turns Fit off and steps up from the current size by x1.25 (96 -> 120 px; L2)', at('W2'));
  click(q('[data-fit]'));
  check(q('[data-fit]').getAttribute('aria-pressed') === 'true', 'Fit turns back on');
  const B2 = boot({ 'benny512.console.view': B.window.localStorage.getItem('benny512.console.view') });
  B2.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !B2.document.querySelector('[data-select-entry="W1"]'); i++) await tick();
  check(B2.document.querySelector('[data-spacing="compact"]').getAttribute('aria-pressed') === 'true', 'a new page in the same browser opens in the remembered Compact');

  // ---- 4. tiles ----------------------------------------------------------------------
  const w1 = tile('W1');
  check(w1.querySelector('[data-fid]').textContent === '201' && w1.querySelector('[data-type-line]').textContent === 'Wash', 'a tile leads with the fixture number and the short type', [w1.querySelector('[data-fid]').textContent, w1.querySelector('[data-type-line]').textContent]);
  check(/^Wash 201, Wash, /.test(w1.getAttribute('aria-label')) && w1.getAttribute('title') === 'Wash 201 · Maker Wash long type name', 'the full name and type stay in the accessible name and the tooltip', [w1.getAttribute('aria-label'), w1.getAttribute('title')]);
  check(tile('NONUM').querySelector('[data-fid]').textContent === 'Spot ' && tile('NONUM').querySelector('[data-fid]').classList.contains('b5-con-tile__fid--name'), 'a fixture with no number shows its name in that place', tile('NONUM').querySelector('[data-fid]').textContent);
  click(menuItem('data-view-menu', 'data-names-toggle'));
  check(q('[data-gridbox]').classList.contains('is-names-off') && !!tile('W1').querySelector('[data-fid]'), 'Names on tiles OFF hides only the type line (the number stays)');
  click(menuItem('data-view-menu', 'data-names-toggle'));
  await B.ProgrammerSync.act('select', { action: 'set', targets: [{ entryId: 'W1', cell: '' }, { entryId: 'W2', cell: '' }] });
  await B.ProgrammerSync.act('highlight', { highlight: true });
  check(!tile('W1').querySelector('[data-hl-tag]').hidden && tile('W3').querySelector('[data-hl-tag]').hidden, 'HL shows on highlighted selected tiles only');
  await B.ProgrammerSync.act('highlight', { highlight: false });
  check(tile('W1').querySelector('[data-hl-tag]').hidden, 'and goes when Highlight is OFF');

  // ---- 5. selection bar ---------------------------------------------------------------
  await B.ProgrammerSync.act('select', { action: 'set', targets: ['W1', 'W3', 'W2', 'W5'].map(e => ({ entryId: e, cell: '' })).concat([{ entryId: 'BAR', cell: 'c2' }, { entryId: 'NONUM', cell: '' }]) });
  const names = q('[data-sel-names]').textContent;
  check(names === 'Wash 201–203, 205 (4) · Bar 401.2 · Spot Spot ', 'summary by short type with contiguous runs only (201–203, 205), a cell as number.cell, a numberless fixture by name', names);
  check(/1 Wash 201, 2 Wash 203/.test(q('[data-sel-names]').getAttribute('title')), 'the full selection in order is the summary\'s title');
  const moreT = q('[data-more-menu]');
  check(!!moreT && moreT.getAttribute('aria-haspopup') === 'menu', 'the bar has a More menu');
  click(moreT);
  const labels = moreT.closest('.b5-menu').querySelectorAll('.b5-menu__item').map(b => b.textContent);
  check(same(labels, ['Lowlight', 'Fan…', 'Store group…', 'Open Tests for the selection']), 'More holds Lowlight, Fan, Store group and Tests', labels);
  const fan0 = document.querySelector('[data-fan-open]');
  let pressed = 0; fan0.addEventListener('click', () => pressed++);
  click(moreT.closest('.b5-menu').querySelector('[data-more-fan]'));
  check(pressed === 1, 'choosing Fan… presses the bar\'s own Fan button (same behaviour as the phone menu)');

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
