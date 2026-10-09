// console_grid_test.js — I2b, the Console selection grid restyle
// (docs/design/console-lite/component-specs.md §3 and §4), run on the
// LITERAL ui.js and console.js in the real index.html (minidom.js). Api,
// ProgrammerSync and Live are small fakes: the layout and fixture models
// are crafted so every glyph class, flag and layer case is on the plan; the
// selection fake applies set/add/remove/toggle/none the way the server does
// (internal/patch/programmer.go Select) so the grid redraws from it.
//  1. Glyphs come from capabilities (fx-* sprite symbols); unknown stays
//     fx-unknown. Flags are words plus a dashed/hatched body.
//  2. Parent vs cell: 3 px outline + order badge vs cell tab; accessible
//     names say class, order, cells, highlight; the badge is decorative.
//  3. A visible text order list in selection order with a live total.
//  4. Layer chips are toggles naming the height; Select all / Invert
//     respect the layers shown; ghosts are dashed words, never buttons.
//  5. Group tile / chip: icon, name, count; hidden members are reported.
//  6. Lasso is a mode toggle; a drag selects what the box touches; the
//     click after the drag is swallowed; Escape and edit mode turn it off.
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

// ---- the show ---------------------------------------------------------------
const P = (attribute, group, extra) => Object.assign({ attribute, group, source: 'gdtf', virtual: false }, extra || {});
const models = {
  MH1: { profiled: true, footprint: 20, unaddressable: [], cells: [], parameters: [P('Pan', 'position'), P('Tilt', 'position'), P('Dimmer', 'dimmer')] },
  MH2: { profiled: true, footprint: 20, unaddressable: [], cells: [], parameters: [P('Pan', 'position'), P('Dimmer', 'dimmer')] },
  WASH: { profiled: true, footprint: 4, unaddressable: [], cells: [], parameters: [P('Dimmer', 'dimmer'), P('ColorAdd_R', 'colour')] },
  BAR: { profiled: true, footprint: 16, unaddressable: [], cells: [1, 2, 3, 4].map(i => ({ id: 'c' + i, name: 'Pixel ' + i, index: i })), parameters: [P('Dimmer', 'dimmer', { cell: 'c1' }), P('ColorAdd_R', 'colour', { cell: 'c1' })] },
  PRO: { profiled: true, footprint: 30, unaddressable: [], cells: [], parameters: [P('Dimmer', 'dimmer'), P('Blade1A', 'shaper'), P('ColorAdd_R', 'colour')] },
  STR: { profiled: true, footprint: 2, unaddressable: [], cells: [], parameters: [P('Shutter1', 'beam'), P('Dimmer', 'dimmer')] },
  DIM: { profiled: true, footprint: 1, unaddressable: [], cells: [], parameters: [P('Dimmer', 'dimmer'), P('Dimmer', 'dimmer', { virtual: true })] },
  FOG: { profiled: true, footprint: 2, unaddressable: [], cells: [], parameters: [P('Fog', 'other')] },
  RAW: { profiled: false, footprint: 4, unaddressable: [], cells: [], parameters: [] },
  RDM: { profiled: true, footprint: 8, unaddressable: [], cells: [], parameters: [P('Pan', 'position', { source: 'rdm-inferred' }), P('Gobo', 'beam', { source: 'rdm-inferred' })] },
  OFF: { profiled: true, footprint: 2, unaddressable: [1, 2], cells: [], parameters: [P('Dimmer', 'dimmer')] },
};
const names = Object.keys(models);
const fixtures = names.map((n, i) => Object.assign({ entryId: n, name: n + ' 1', fixtureType: 'Type ' + n, fixtureNumber: String(i + 1) }, models[n]));
const placed = { MH1: ['truss', 0, 0, 1], MH2: ['truss', 1, 0, 1], WASH: ['floor', 0, 0, 1], BAR: ['floor', 1, 0, 2], PRO: ['floor', 3, 0, 1], STR: ['floor', 4, 0, 1], DIM: ['floor', 5, 0, 1], FOG: ['floor', 6, 0, 1] };
const items = [];
names.forEach((n, i) => {
  const pl = placed[n];
  if (pl) items.push({ id: 'it-' + n, kind: 'entry', ref: n, layer: pl[0], col: pl[1], row: pl[2], w: pl[3], h: 1, rot: 0, mode: 'manual', order: i });
  else items.push({ id: 'entry-' + n, kind: 'entry', ref: n, layer: 'unplaced', col: i, row: 0, w: 1, h: 1, rot: 0, mode: 'auto', order: i });
});
items.push({ id: 'it-g1', kind: 'group', ref: 'g1', layer: 'truss', col: 2, row: 0, w: 1, h: 1, rot: 0, order: 20 });
const group = { id: 'g1', name: 'Front', members: [{ entryId: 'WASH', cell: '' }, { entryId: 'BAR', cell: 'c2' }] };
const layout = {
  revision: 3, active: true, name: 'Test show',
  layout: { layers: [
    { id: 'truss', name: 'Truss', zKnown: true, zMin: 6000, zMax: 6000 },
    { id: 'floor', name: 'Floor', zKnown: false, zMin: 0, zMax: 0 },
    { id: 'unplaced', name: 'Unplaced', system: true, zKnown: false, zMin: 0, zMax: 0 }], items },
  entries: fixtures.map(f => ({ id: f.entryId, name: f.name, fixtureType: f.fixtureType, fixtureNumber: f.fixtureNumber, itemId: (items.find(it => it.ref === f.entryId) || {}).id })),
  groups: [group], unplaced: ['RAW', 'RDM', 'OFF'].map(n => ({ entryId: n, reason: 'no-location', itemId: 'entry-' + n })),
  stale: [], limits: {}, objectTypes: [],
};

// ---- the page ---------------------------------------------------------------
function boot() {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(fs.readFileSync(path.join(JS, '..', 'index.html'), 'utf8'));
  const posts = [];
  const subs = [];
  const state = { revision: 1, selection: [], highlight: { on: false }, storedGroups: [group] };
  const valid = t => fixtures.some(f => f.entryId === t.entryId && (!t.cell || f.cells.some(c => c.id === t.cell)));
  const view = t => { const f = fixtures.find(x => x.entryId === t.entryId); const c = t.cell ? f.cells.find(x => x.id === t.cell) : null; return { entryId: t.entryId, cell: t.cell, name: f.name, cellName: c ? c.name : '', cellIndex: c ? c.index : 0 }; };
  const ProgrammerSync = {
    state: () => state,
    refresh: () => Promise.resolve(state),
    onChange: fn => subs.push(fn),
    act: (action, body) => {
      posts.push({ action, body: JSON.parse(JSON.stringify(body)) });
      if (action === 'select') {
        let targets = body.targets || [];
        if (body.group) targets = group.members;
        if (body.layer) targets = items.filter(it => it.layer === body.layer && it.kind === 'entry').map(it => ({ entryId: it.ref, cell: '' }));
        targets = targets.filter(valid);
        const k = t => t.entryId + '/' + (t.cell || '');
        let next = state.selection.map(s => ({ entryId: s.entryId, cell: s.cell }));
        const has = t => next.findIndex(s => k(s) === k(t));
        if (body.action === 'set') { next = []; targets.forEach(t => { if (has(t) < 0) next.push(t); }); }
        if (body.action === 'add') targets.forEach(t => { if (has(t) < 0) next.push(t); });
        if (body.action === 'remove') targets.forEach(t => { const i = has(t); if (i >= 0) next.splice(i, 1); });
        if (body.action === 'toggle') targets.forEach(t => { const i = has(t); if (i >= 0) next.splice(i, 1); else next.push(t); });
        if (body.action === 'none') next = [];
        if (body.action === 'all') next = fixtures.map(f => ({ entryId: f.entryId, cell: '' }));
        state.selection = next.map(view);
      }
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
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, Map, Set, window, document, Event, CustomEvent,
    getComputedStyle: window.getComputedStyle, localStorage: window.localStorage, Api, ProgrammerSync, Live: { on() {} },
  });
  window.addEventListener = window.addEventListener.bind(window);
  for (const f of ['ui.js', 'console.js']) vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
  vm.runInContext('this.ConsoleScreen = ConsoleScreen;', ctx);
  return { ctx, document, posts, state, ProgrammerSync };
}

(async () => {
  const B = boot();
  const { document, posts, state } = B;
  const q = s => document.querySelector(s);
  const qa = s => document.querySelectorAll(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const grid = () => q('[data-gridbox]');
  const tile = id => grid().querySelector('[data-select-entry="' + id + '"]');
  const holder = id => tile(id).closest('[data-item-id]');
  const lastSel = () => posts.filter(p => p.action === 'select').slice(-1)[0];
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('MH1'); i++) await tick();
  check(!!tile('MH1'), 'the grid draws the crafted show');

  // ---- 1. glyphs and flags --------------------------------------------------
  const glyphOf = id => { const u = tile(id) && tile(id).querySelector('.b5-con-glyph use'); return u ? u.getAttribute('href').replace(/^.*#b5-icon-/, '') : '(none)'; };
  const want = { MH1: 'fx-moving-head', WASH: 'fx-wash', BAR: 'fx-pixel-bar', PRO: 'fx-profile', STR: 'fx-strobe', DIM: 'fx-dimmer', FOG: 'fx-unknown', RAW: 'fx-unknown', RDM: 'fx-moving-head', OFF: 'fx-dimmer' };
  const got = {};
  Object.keys(want).forEach(k => { got[k] = glyphOf(k); });
  check(same(got, want), 'every tile draws the sprite fixture glyph its capabilities give (unknown capabilities = fx-unknown, never a guessed head)', got);
  const gl = tile('MH1').querySelector('.b5-con-glyph');
  check(!!gl && gl.getAttribute('aria-hidden') === 'true' && !/\?/.test(gl.textContent), 'the glyph is decorative (aria-hidden) and the "?" placeholder is gone');
  const flags = id => (tile(id).querySelector('[data-flag-words]') || { textContent: '' }).textContent;
  check(flags('RAW') === 'No profile' && holder('RAW').classList.contains('is-unprofiled'), 'no channel map: dashed body + the words "No profile"', flags('RAW'));
  check(flags('RDM') === 'RDM labels' && holder('RDM').classList.contains('is-unprofiled'), 'functions only from RDM slot labels: dashed body + "RDM labels"', flags('RDM'));
  check(flags('OFF') === 'Not on wire' && holder('OFF').classList.contains('is-offwire'), 'no byte addressable: hatched body + "Not on wire"', flags('OFF'));
  check(flags('MH1') === '' && !holder('MH1').classList.contains('is-unprofiled'), 'a profiled, addressable fixture carries no flag');
  check(holder('RAW').classList.contains('is-unplaced') && /no position/.test(holder('RAW').querySelector('.b5-con-tile__meta').textContent), 'an unplaced fixture is a dashed outline with its words ("no position")');

  // ---- 2. parent vs cell, names, badge ---------------------------------------
  click(tile('MH1'));
  await tick();
  const cell2 = grid().querySelector('[data-select-cell="c2"][data-entry-id="BAR"]');
  check(!!cell2, 'the multi-cell fixture shows one button per cell at this zoom');
  click(cell2, { shiftKey: true });
  await tick();
  check(same(state.selection.map(s => s.entryId + '/' + s.cell), ['MH1/', 'BAR/c2']), 'tap + Shift-tap a cell selects parent then cell, in order', state.selection);
  const badge = tile('MH1').querySelector('[data-badge]');
  check(!badge.hidden && badge.textContent === '1' && badge.getAttribute('aria-hidden') === 'true' && !badge.querySelector('svg'), 'the parent order badge is the bare number, decorative for assistive tech', badge.textContent);
  check(holder('MH1').classList.contains('is-selected') && !holder('BAR').classList.contains('is-selected') && holder('BAR').classList.contains('has-cells-selected'), 'parent selected = tile outline; cell selected = cell mark, parent stays unselected');
  check(cell2.classList.contains('is-selected') && cell2.querySelector('[data-badge]').textContent === '2', 'the selected cell carries its own order number (2)');
  const lab1 = tile('MH1').getAttribute('aria-label') || '';
  check(/^MH1 1, moving head, selected, order 1, #1/.test(lab1), 'parent accessible name: name, class, "selected, order 1", number', lab1);
  const labC = cell2.getAttribute('aria-label') || '';
  check(labC === 'BAR 1, cell 2 of 4 (Pixel 2), selected, order 2', 'cell accessible name: "BAR 1, cell 2 of 4 (Pixel 2), selected, order 2"', labC);
  check(/1 of 4 cells selected/.test(tile('BAR').getAttribute('aria-label') || ''), 'the parent of a selected cell says "1 of 4 cells selected"', tile('BAR').getAttribute('aria-label'));
  check(/No profile/.test(tile('RAW').getAttribute('aria-label') || '') && /not selected/.test(tile('RAW').getAttribute('aria-label') || ''), 'flag words are in the accessible name too', tile('RAW').getAttribute('aria-label'));
  await B.ProgrammerSync.act('highlight', { highlight: true });
  check(holder('MH1').classList.contains('is-highlight') && /highlight/.test(tile('MH1').getAttribute('aria-label')) && cell2.classList.contains('is-highlight'), 'Highlight ON: selected marks get the double outline and "highlight" in their name');
  check(!holder('MH2').classList.contains('is-highlight'), 'Highlight marks only the selection');
  await B.ProgrammerSync.act('highlight', { highlight: false });

  // ---- 3. order list ------------------------------------------------------------
  const items3 = qa('[data-order-list] li').map(li => li.textContent);
  check(same(items3, ['1MH1 1', '2BAR 1 · cell 2 (Pixel 2)']), 'a visible text list gives the selection in order', items3);
  const total = q('[data-order-total]');
  check(total && total.getAttribute('role') === 'status' && total.textContent === '1 fixture · 1 cell selected, in this order', 'the order list total is a live status in words', total && total.textContent);

  // ---- 4. layer chips, filter, Select all, Invert, ghosts -------------------------
  const chip = id => q('[data-layer-chip="' + id + '"]');
  check(/Truss · 3 · height 6\.0 m/.test(chip('truss').textContent) && /height unknown/.test(chip('floor').textContent) && /no position/.test(chip('unplaced').textContent), 'layer chips name their height ("height 6.0 m", "height unknown")', qa('[data-layer-chip]').map(c => c.textContent));
  check(chip('all').getAttribute('aria-pressed') === 'true' && chip('truss').getAttribute('aria-pressed') === 'false', 'no filter: All layers is pressed');
  click(chip('truss'));
  check(qa('[data-layer-block]').map(b => b.getAttribute('data-layer-block')).join() === 'truss' && chip('truss').getAttribute('aria-pressed') === 'true', 'a layer chip filters the plan to that layer');
  click(chip('floor'));
  check(qa('[data-layer-block]').map(b => b.getAttribute('data-layer-block')).join() === 'truss,floor' && chip('floor').getAttribute('aria-pressed') === 'true' && chip('all').getAttribute('aria-pressed') === 'false', 'a second chip adds its layer: chips are toggles (aria-pressed)');
  check(/Select all on 2 layers/.test(q('[data-select-all]').textContent), 'Select all names what it will take', q('[data-select-all]').textContent);
  check(!tile('RAW'), 'a filtered-out fixture has no selectable tile');
  click(q('[data-select-all]'));
  await tick();
  check(same(lastSel().body, { action: 'set', targets: ['MH1', 'MH2', 'WASH', 'BAR/c2', 'BAR', 'PRO', 'STR', 'DIM', 'FOG'].map(t => ({ entryId: t.split('/')[0], cell: t.split('/')[1] || '' })) }),
    'Select all with two layers shown: their fixtures in layer then reading order, a placed group adding its members (as the server does for one layer)', lastSel().body);
  click(q('[data-select-none]'));
  await tick();
  click(tile('MH2'));
  await tick();
  click(q('[data-select-invert]'));
  await tick();
  check(same(lastSel().body, { action: 'toggle', targets: ['MH1', 'MH2', 'WASH', 'BAR', 'PRO', 'STR', 'DIM', 'FOG'].map(e => ({ entryId: e, cell: '' })) }), 'Invert toggles only the fixtures on the layers shown', lastSel().body);
  check(!state.selection.some(s => s.entryId === 'MH2') && state.selection.some(s => s.entryId === 'MH1') && !state.selection.some(s => s.entryId === 'RAW'), 'after Invert: MH2 out, MH1 in, hidden fixtures untouched');
  click(chip('floor'));
  check(!!q('[data-hidden-note]') && !q('[data-ghost-layer]'), 'Ghosts OFF: hidden layers are not drawn, and a note says so in words', (q('[data-hidden-note]') || {}).textContent);
  const gt = q('[data-ghost-toggle]');
  check(gt && gt.getAttribute('aria-pressed') === 'false' && /Ghosts: OFF/.test(gt.textContent), 'the Ghosts toggle says Ghosts: OFF by default');
  click(gt);
  const ghostLayers = qa('[data-ghost-layer]').map(l => l.getAttribute('data-ghost-layer'));
  check(same(ghostLayers, ['floor', 'unplaced']) && /Ghosts: ON/.test(gt.textContent), 'Ghosts ON draws the layers not shown as ghost layers', ghostLayers);
  check(qa('[data-ghost-layer] button').length === 0 && qa('[data-ghost-layer] [data-select-entry]').length === 0, 'ghosts are never buttons: not selectable while filtered');
  const gtile = q('[data-ghost-item="it-WASH"]');
  check(!!gtile && /Outside layer/.test(gtile.textContent) && /WASH 1/.test(gtile.textContent) && !!gtile.querySelector('use'), 'a ghost is a glyph, its name and the words "Outside layer"', gtile && gtile.textContent);
  check(/not selectable/.test(q('[data-ghost-layer="floor"]').getAttribute('aria-label')), 'the ghost layer says "not selectable" to assistive tech');
  click(gt);

  // ---- 5. group tile and chip ----------------------------------------------------
  const gTile = grid().querySelector('[data-select-group="g1"]');
  check(!!gTile && /fx-group/.test(gTile.querySelector('use').getAttribute('href')) && gTile.querySelector('.b5-con-glyph__count').textContent === '2', 'the group tile draws fx-group with its member count as text');
  const gChip = q('[data-group-chip="g1"]');
  check(!!gChip.querySelector('svg') && /Front · 2/.test(gChip.textContent), 'the group chip has an icon, its name and count', gChip.textContent);
  click(gChip);
  await tick(); await tick();
  const st = q('[data-status]').textContent;
  check(/2 of 2 members of Front are on layers not shown/.test(st) && /WASH 1/.test(st), 'selecting a group says which members the layer filter hides', st);
  click(chip('all'));

  // ---- 6. lasso -----------------------------------------------------------------
  const lt = q('[data-lasso-toggle]');
  check(lt && lt.getAttribute('aria-pressed') === 'false' && /Lasso: OFF/.test(lt.textContent), 'Lasso is a mode toggle, OFF by default, said in words');
  const box = (id, x, y) => { holder(id).getBoundingClientRect = () => ({ left: x, top: y, right: x + 90, bottom: y + 90, width: 90, height: 90, x, y }); };
  box('MH1', 100, 100); box('MH2', 196, 100); box('WASH', 100, 300); box('BAR', 196, 300);
  const ptr = (type, x, y, init) => grid().dispatchEvent(new Event(type, Object.assign({ bubbles: true, clientX: x, clientY: y, pointerId: 3, button: 0, pointerType: 'touch' }, init || {})));
  const n0 = posts.length;
  ptr('pointerdown', 90, 90); ptr('pointermove', 200, 150); ptr('pointerup', 250, 150);
  await tick();
  check(posts.length === n0, 'Lasso OFF: a drag on the plan selects nothing (it scrolls)');
  click(lt);
  check(lt.getAttribute('aria-pressed') === 'true' && /Lasso: ON/.test(lt.textContent) && q('#consoleRoot').classList.contains('is-lasso'), 'Lasso: ON — the grid takes the drag (is-lasso: touch-action none)');
  ptr('pointerdown', 90, 90); ptr('pointermove', 200, 150);
  check(!q('[data-lasso-box]').hidden, 'the lasso box is drawn while dragging');
  ptr('pointerup', 250, 150);
  click(tile('MH2'));
  await tick();
  check(posts.length === n0 + 1 && same(lastSel().body, { action: 'set', targets: [{ entryId: 'MH1', cell: '' }, { entryId: 'MH2', cell: '' }] }), 'a lasso drag selects the fixtures the box touches, in reading order; the click that ends the drag is swallowed', posts.slice(n0));
  check(q('[data-lasso-box]').hidden, 'the box goes away on release');
  click(tile('WASH'));
  await tick();
  check(same(lastSel().body, { action: 'set', targets: [{ entryId: 'WASH', cell: '' }] }), 'with Lasso ON a plain tap still selects as usual');
  ptr('pointerdown', 90, 290, { shiftKey: true }); ptr('pointermove', 260, 330); ptr('pointerup', 260, 330, { shiftKey: true });
  await tick();
  check(same(lastSel().body, { action: 'add', targets: [{ entryId: 'WASH', cell: '' }, { entryId: 'BAR', cell: '' }] }), 'Shift (or Add mode) + lasso adds to the selection', lastSel().body);
  const n1 = posts.length;
  ptr('pointerdown', 90, 90); ptr('pointermove', 200, 150); ptr('pointercancel', 200, 150); ptr('pointerup', 200, 150);
  await tick();
  check(posts.length === n1, 'a pointer cancel drops the lasso without selecting');
  tile('MH1').dispatchEvent(new Event('keydown', { bubbles: true, key: 'Escape' }));
  check(lt.getAttribute('aria-pressed') === 'false' && !q('#consoleRoot').classList.contains('is-lasso'), 'Escape turns Lasso off');
  click(lt);
  click(q('[data-edit-toggle]'));
  check(lt.getAttribute('aria-pressed') === 'false' && lt.disabled, 'edit mode turns Lasso off and disables it (an edit drag moves items, never lassos)');
  click(q('[data-edit-toggle]'));

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
