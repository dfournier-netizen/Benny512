// console_test.js — the Console screen (C6a) run as the browser runs it: the
// real index.html parsed into a small DOM (minidom.js), the LITERAL api.js,
// ws.js, programmer.js, ui.js and console.js, against a real Benny512 server
// (started by internal/web/console_c6a_test.go): real fetch, real WebSocket.
// Every request the page makes is recorded on its way to the server, so the
// checks are on the exact payloads the gestures send AND on what the server
// then holds. Part 2 runs the literal app.js against the real index.html to
// prove the screen is registered (nav, screen, enter/leave hooks, script
// order) and that leaving it stops nothing.
//
// usage: node console_test.js <jsDir> <baseURL> <idsJSON>
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

function browser(files, extra) {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(indexHTML);
  const url = new URL(base);
  const calls = [];
  const sandbox = {
    console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
    WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
    localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
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
  for (const f of files) vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
  return { ctx, window, document, calls, get: expr => vm.runInContext(expr, ctx) };
}

async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  if (!r.ok) throw new Error(method + ' ' + p + ': ' + r.status + ' ' + t);
  return t ? JSON.parse(t) : null;
}

(async () => {
  // ---------------------------------------------------------------- part 1
  const B = browser(['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console.js']);
  const { document, calls } = B;
  const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
  const lastPost = p => { const l = posts(p); return l[l.length - 1]; };
  const SEL = '/api/programmer/select';
  const grid = () => document.querySelector('[data-gridbox]');
  const tile = id => grid().querySelector('[data-select-entry="' + id + '"]');
  const badge = id => { const t = tile(id); const b = t && t.querySelector('[data-badge]'); return b && !b.hidden ? b.textContent : ''; };
  const selState = () => (B.get('ProgrammerSync.state()') || { selection: [] }).selection.map(s => s.entryId + (s.cell ? '/' + s.cell : ''));
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const ptr = (el, type, x, y) => el.dispatchEvent(new Event(type, { bubbles: true, clientX: x, clientY: y, pointerId: 7, button: 0, pointerType: 'touch' }));

  check(!!document.getElementById('consoleRoot'), 'index.html carries the Console screen root (#consoleRoot inside #screen-console)');
  await until('the WebSocket and the programmer to load', () => B.get('ProgrammerSync.state()'));
  B.get('ConsoleScreen').onEnterScreen();
  await until('the layout to render', () => tile(ids.B1) && tile(ids.G1));

  const fx = await server('GET', '/api/programmer/fixtures');
  const p1cells = fx.fixtures.find(f => f.entryId === ids.P1).cells;
  check(p1cells.length >= 2, 'the Paladin Cube extract has cells to select (' + p1cells.length + ')');

  // Every fixture is on the grid before any derive: the Unplaced layer.
  const chips = document.querySelectorAll('[data-layer-chip]').map(c => c.getAttribute('data-layer-chip'));
  check(same(chips, ['all', 'unplaced']), 'layer chips before derive are All + Unplaced', chips);
  check(['B1', 'B2', 'L1', 'P1', 'G1'].every(n => tile(ids[n])), 'every patched fixture has a selectable tile (Unplaced layer)');
  const g1meta = tile(ids.G1).closest('[data-item-id]').querySelector('.b5-con-tile__meta').textContent;
  check(/auto/.test(g1meta) && /no position/.test(g1meta), 'an unplaced fixture says so in words ("auto", "no position")', g1meta);

  // Edit mode is OFF by default.
  const toggle = document.querySelector('[data-edit-toggle]');
  check(toggle.getAttribute('aria-pressed') === 'false' && /OFF/.test(toggle.textContent), 'layout edit mode is OFF by default, said in words', toggle.textContent);
  check(document.querySelector('[data-modebar]').hidden, 'the edit-mode bar is hidden while editing is off');
  check(!!document.querySelector('[data-controls-region]') && /arrive next/.test(document.querySelector('[data-controls-region]').textContent), 'a marked, empty controls region says controls arrive next (C6b)');

  // --- selection gestures --------------------------------------------------
  click(tile(ids.B1));
  await until('B1 selected', () => badge(ids.B1));
  check(same(lastPost(SEL).body, { action: 'set', targets: [{ entryId: ids.B1, cell: '' }] }), 'tap = select only that fixture: {action:set, targets:[B1]}', lastPost(SEL).body);
  check(tile(ids.B1).getAttribute('aria-pressed') === 'true' && badge(ids.B1) === '1', 'the tile shows the server selection: aria-pressed and order badge 1', badge(ids.B1));

  click(tile(ids.B2), { shiftKey: true });
  await until('B2 selected', () => badge(ids.B2));
  check(same(lastPost(SEL).body, { action: 'toggle', targets: [{ entryId: ids.B2, cell: '' }] }), 'Shift+tap toggles in: {action:toggle, targets:[B2]}', lastPost(SEL).body);
  check(badge(ids.B1) === '1' && badge(ids.B2) === '2', 'selection order is drawn: B1 = 1, B2 = 2', [badge(ids.B1), badge(ids.B2)]);

  click(document.querySelector('[data-add-mode]'));
  check(document.querySelector('[data-add-mode]').getAttribute('aria-pressed') === 'true' && /ON/.test(document.querySelector('[data-add-mode]').textContent), 'Add mode toggle (no modifier keys needed) says ON');
  click(tile(ids.L1));
  await until('L1 selected', () => badge(ids.L1));
  click(tile(ids.B1));
  await until('B1 toggled out', () => !badge(ids.B1));
  const addBodies = posts(SEL).slice(-2).map(c => c.body);
  check(same(addBodies, [{ action: 'toggle', targets: [{ entryId: ids.L1, cell: '' }] }, { action: 'toggle', targets: [{ entryId: ids.B1, cell: '' }] }]), 'Add mode taps toggle in and out', addBodies);
  check(same(selState(), [ids.B2, ids.L1]) && badge(ids.B2) === '1' && badge(ids.L1) === '2', 'order follows the server after a removal: B2 = 1, L1 = 2', selState());
  click(document.querySelector('[data-add-mode]'));

  // A cell of a multi-cell fixture: directly from the strip when the cells
  // fit at full touch size, otherwise through the cell picker.
  const want = p1cells[1].id;
  let cellBtn = grid().querySelector('[data-select-cell="' + want + '"][data-entry-id="' + ids.P1 + '"]');
  if (!cellBtn) {
    const open = grid().querySelector('[data-open-cells="' + ids.P1 + '"]');
    check(!!open, 'a multi-cell tile at this zoom offers its cells through a full-size "cells" button');
    click(open);
    cellBtn = document.querySelector('[data-cellpicker] [data-select-cell="' + want + '"]');
    check(!document.querySelector('[data-cellpicker]').hidden && !!cellBtn, 'the cell picker opens with one button per cell');
    check(calls.filter(c => c.method === 'POST').length === posts(SEL).length && !posts(SEL).slice(-1)[0].body.targets.some(t => t.entryId === ids.P1), 'opening the cells selects nothing');
  }
  click(cellBtn);
  await until('the P1 cell selected', () => selState().join() === ids.P1 + '/' + want);
  check(same(lastPost(SEL).body, { action: 'set', targets: [{ entryId: ids.P1, cell: want }] }), 'tap a cell = select that cell: {action:set, targets:[{P1, cell}]}', lastPost(SEL).body);
  const p1tile = tile(ids.P1).closest('[data-item-id]');
  check(p1tile.classList.contains('has-cells-selected') && tile(ids.P1).getAttribute('aria-pressed') === 'false', 'cell selection reads differently from parent selection (parent not pressed, cells marked)');
  check(/1 of \d+ cells selected/.test((p1tile.querySelector('[data-cells-label]') || { textContent: '' }).textContent) || !!grid().querySelector('[data-select-cell][aria-pressed="true"]'), 'the cell state is said in words on the tile');
  click(tile(ids.P1));
  await until('P1 parent selected', () => selState().join() === ids.P1);
  check(same(lastPost(SEL).body, { action: 'set', targets: [{ entryId: ids.P1, cell: '' }] }), 'tap the tile = select the parent fixture', lastPost(SEL).body);

  // Layers: select a layer, filter to a layer, select all on it, select all.
  click(grid().querySelector('[data-select-layer="unplaced"]'));
  await until('the layer selected', () => selState().length === 5);
  check(same(lastPost(SEL).body, { action: 'set', layer: 'unplaced' }), 'Select this layer: {action:set, layer:unplaced}', lastPost(SEL).body);
  click(document.querySelector('[data-layer-chip="unplaced"]'));
  check(grid().querySelectorAll('[data-layer-block]').length === 1 && document.querySelector('[data-layer-chip="unplaced"]').getAttribute('aria-pressed') === 'true', 'a layer chip filters the grid to that layer');
  check(/Unplaced/.test(document.querySelector('[data-select-all]').textContent), 'with a layer shown, Select all names the layer', document.querySelector('[data-select-all]').textContent);
  click(document.querySelector('[data-select-none]'));
  await until('none selected', () => selState().length === 0);
  check(same(lastPost(SEL).body, { action: 'none' }), 'Select none: {action:none}');
  click(document.querySelector('[data-select-all]'));
  await until('the layer re-selected', () => selState().length === 5);
  check(same(lastPost(SEL).body, { action: 'set', layer: 'unplaced' }), 'Select all on a shown layer selects that layer', lastPost(SEL).body);
  click(document.querySelector('[data-layer-chip="all"]'));
  click(document.querySelector('[data-select-all]'));
  await until('all selected', () => same(lastPost(SEL).body, { action: 'all' }));
  check(same(lastPost(SEL).body, { action: 'all' }), 'Select all fixtures with All layers shown: {action:all}');

  // Another browser changes the selection: it arrives by the revision
  // broadcast alone.
  await sleep(100);
  await server('POST', SEL, { action: 'set', targets: [{ entryId: ids.L1, cell: '' }] });
  await until('the other browser\'s selection to arrive', () => badge(ids.L1) === '1' && !badge(ids.B1));
  check(badge(ids.L1) === '1' && !badge(ids.B1), 'another browser\'s selection is drawn here via the programmer broadcast');

  // Summary bar: count, names, store group, highlight, locate, clear.
  const count = () => document.querySelector('[data-sel-count]').textContent;
  check(/1 selected/.test(count()) && /L1/.test(document.querySelector('[data-sel-names]').textContent), 'the summary bar counts and names the selection', count());
  await B.get('ProgrammerSync').act('select', { action: 'set', targets: [{ entryId: ids.B1, cell: '' }, { entryId: ids.B2, cell: '' }] });
  await until('two selected', () => /2 selected/.test(count()));
  click(document.querySelector('[data-store-group]'));
  await until('the group-name dialog', () => document.querySelector('[data-ask] [data-ask-input]'));
  check(posts('/api/programmer/groups/store').length === 0, 'Store group asks for a name in an in-app dialog before posting (no native prompt)');
  document.querySelector('[data-ask-input]').value = 'Front pair';
  click(document.querySelector('[data-ask-ok]'));
  await until('the group to be stored', () => document.querySelector('[data-group-chip]'));
  check(same(lastPost('/api/programmer/groups/store').body, { name: 'Front pair' }), 'Store group posts {name}', lastPost('/api/programmer/groups/store') && lastPost('/api/programmer/groups/store').body);
  check(!document.querySelector('[data-ask]'), 'the dialog is gone after saving');
  const chip = document.querySelector('[data-group-chip]');
  check(/Front pair/.test(chip.textContent) && /all selected/.test(chip.textContent), 'the group chip says its state in words', chip.textContent);
  click(document.querySelector('[data-clear-selection]'));
  await until('cleared', () => selState().length === 0);
  check(same(lastPost(SEL).body, { action: 'none' }), 'Clear selection: {action:none}');
  click(document.querySelector('[data-group-chip]'));
  await until('the group selected', () => selState().length === 2);
  check(same(lastPost(SEL).body, { action: 'set', group: chip.getAttribute('data-group-chip') }), 'tap a stored group: {action:set, group:id}', lastPost(SEL).body);
  check(same(selState(), [ids.B1, ids.B2]), 'the group selects its members in stored order', selState());
  click(document.querySelector('[data-highlight]'));
  await until('highlight on', () => /ON/.test(document.querySelector('[data-highlight]').textContent));
  check(same(lastPost('/api/programmer/highlight').body, { highlight: true }) && document.querySelector('[data-highlight]').getAttribute('aria-pressed') === 'true', 'Highlight toggle posts {highlight:true} and says Highlight: ON');
  click(document.querySelector('[data-locate]'));
  await until('locate posted', () => posts('/api/programmer/locate').length === 1);
  check(same(lastPost('/api/programmer/locate').body, {}), 'Locate posts for the current selection ({})');

  // --- edit mode OFF: a drag moves nothing --------------------------------
  const layoutPosts = () => calls.filter(c => c.method === 'POST' && c.path.startsWith('/api/patch/layout/'));
  const before = calls.filter(c => c.method === 'POST').length;
  ptr(tile(ids.B1), 'pointerdown', 100, 100);
  ptr(tile(ids.B1), 'pointermove', 150, 150);
  ptr(tile(ids.B1), 'pointermove', 292, 196);
  ptr(tile(ids.B1), 'pointerup', 292, 196);
  await sleep(150);
  check(layoutPosts().length === 0 && calls.filter(c => c.method === 'POST').length === before, 'edit mode OFF: a drag on a tile sends nothing at all');

  // --- edit mode ON ----------------------------------------------------------
  click(toggle);
  check(toggle.getAttribute('aria-pressed') === 'true' && !document.querySelector('[data-modebar]').hidden && !document.querySelector('[data-editpanel]').hidden, 'Edit layout: ON shows the mode bar and the edit panel');
  click(document.querySelector('[data-derive]'));
  await until('derive', () => document.querySelectorAll('[data-layer-chip]').length > 2);
  check(same(lastPost('/api/patch/layout/derive').body, {}), 'Derive from positions posts derive {}');
  check(!!lastPost('/api/patch/layout/derive').headers['X-Benny-Show'], 'layout writes carry the show-guard token (X-Benny-Show)');
  let layout = await server('GET', '/api/patch/layout');
  const b1item = layout.layout.items.find(it => it.kind === 'entry' && it.ref === ids.B1);
  check(b1item.layer !== 'unplaced' && b1item.mode === 'auto', 'the server derived B1 onto a height layer, automatically', b1item);

  const selPosts = posts(SEL).length;
  click(tile(ids.B1));
  await sleep(100);
  check(posts(SEL).length === selPosts, 'edit mode ON: a tap does not select');
  check(/B1/.test(document.querySelector('[data-editpanel]').textContent) && !!document.querySelector('[data-editpanel] [data-pin]'), 'a tap picks the item for the edit panel (auto placement offers Pin here)');

  ptr(tile(ids.B1), 'pointerdown', 100, 100);
  ptr(tile(ids.B1), 'pointermove', 150, 150);
  ptr(tile(ids.B1), 'pointermove', 100 + 96 * 2, 100 + 96);
  ptr(tile(ids.B1), 'pointerup', 100 + 96 * 2, 100 + 96);
  await until('the move', () => posts('/api/patch/layout/move').length === 1);
  check(same(lastPost('/api/patch/layout/move').body, { id: b1item.id, col: b1item.col + 2, row: b1item.row + 1 }), 'edit mode ON: drag two cells right, one down = move {id, col+2, row+1}', lastPost('/api/patch/layout/move') && lastPost('/api/patch/layout/move').body);
  // Wait for the server's answer, not just the request.
  const settled = async (what, fn) => {
    const end = Date.now() + 4000;
    while (Date.now() < end) { layout = await server('GET', '/api/patch/layout'); if (fn(layout)) return; await sleep(20); }
    check(false, 'timed out waiting for ' + what);
  };
  await settled('the moved B1', l => l.layout.items.find(it => it.id === b1item.id).mode === 'manual');
  let b1now = layout.layout.items.find(it => it.id === b1item.id);
  check(b1now.col === b1item.col + 2 && b1now.row === b1item.row + 1 && b1now.mode === 'manual', 'the server holds the dragged position, now manual', b1now);
  await until('the page to hold the moved layout', () => B.get('ConsoleScreen._state.layout').layout.items.find(it => it.id === b1item.id).mode === 'manual' && tile(ids.B1));

  tile(ids.B1).dispatchEvent(new Event('keydown', { bubbles: true, key: 'ArrowLeft' }));
  await until('the nudge', () => posts('/api/patch/layout/move').length === 2);
  check(same(lastPost('/api/patch/layout/move').body, { id: b1item.id, col: b1now.col - 1, row: b1now.row }), 'arrow key nudges the focused item one cell', lastPost('/api/patch/layout/move').body);
  await until('the nudge to land', () => B.get('ConsoleScreen._state.layout').layout.items.find(it => it.id === b1item.id).col === b1now.col - 1 && document.querySelector('[data-editpanel] [data-reset-auto]'));
  click(document.querySelector('[data-editpanel] [data-reset-auto]'));
  await until('reset-auto', () => posts('/api/patch/layout/reset-auto').length === 1);
  check(same(lastPost('/api/patch/layout/reset-auto').body, { id: b1item.id }), 'Back to automatic posts reset-auto {id}');
  await settled('B1 back to auto', l => l.layout.items.find(it => it.id === b1item.id).mode === 'auto');
  b1now = layout.layout.items.find(it => it.id === b1item.id);
  check(b1now.mode === 'auto' && b1now.col === b1item.col && b1now.row === b1item.row, 'the server put B1 back at its derived cell', b1now);

  // Objects: create a truss and a label, drag the truss half a cell.
  const tlayer = layout.layout.layers[0].id;
  click(document.querySelector('[data-add-object="truss"]'));
  await until('the truss', () => posts('/api/patch/layout/object-create').length === 1);
  const tb = lastPost('/api/patch/layout/object-create').body;
  check(tb.objectType === 'truss' && tb.layer === tlayer && tb.text === '' && tb.rot === 0 && same(Object.keys(tb.geometry).sort(), ['col', 'col2', 'row', 'row2']) && tb.geometry.col2 - tb.geometry.col === 4, 'Add truss posts object-create {objectType:truss, layer, text:"", rot:0, line geometry}', tb);
  await until('the truss handle', () => grid().querySelector('[data-edit-object]'));
  await settled('the truss', l => l.layout.items.some(it => it.kind === 'object' && it.objectType === 'truss'));
  const truss = layout.layout.items.find(it => it.kind === 'object' && it.objectType === 'truss');
  check(!!truss, 'the server stored the truss');
  check(grid().querySelectorAll('[data-select-entry]').length === 5 && !grid().querySelector('[data-kind="object"] [data-select-entry]'), 'objects are drawn but never selectable');
  const handle = grid().querySelector('[data-edit-object="' + truss.id + '"]');
  ptr(handle, 'pointerdown', 10, 10);
  ptr(handle, 'pointermove', 40, 10);
  ptr(handle, 'pointerup', 10 + 48, 10);
  await until('the truss move', () => posts('/api/patch/layout/object-update').length === 1);
  const ub = lastPost('/api/patch/layout/object-update').body;
  check(same(ub, { id: truss.id, geometry: { col: truss.geometry.col + 0.5, row: truss.geometry.row, col2: truss.geometry.col2 + 0.5, row2: truss.geometry.row2 } }), 'dragging an object half a cell = object-update with the geometry moved 0.5', ub);
  click(document.querySelector('[data-add-object="label"]'));
  await until('the label dialog', () => document.querySelector('[data-ask-input]'));
  document.querySelector('[data-ask-input]').value = 'DSC';
  click(document.querySelector('[data-ask-ok]'));
  await until('the label', () => posts('/api/patch/layout/object-create').length === 2);
  const lb = lastPost('/api/patch/layout/object-create').body;
  check(lb.objectType === 'label' && lb.text === 'DSC' && same(Object.keys(lb.geometry).sort(), ['col', 'row']), 'Add text label asks for the text, then posts it with a point geometry', lb);

  // Layers: create one, then filter to it.
  click(document.querySelector('[data-layer-create]'));
  await until('the layer dialog', () => document.querySelector('[data-ask-input]'));
  document.querySelector('[data-ask-input]').value = 'Floor';
  click(document.querySelector('[data-ask-ok]'));
  await until('layer-create', () => posts('/api/patch/layout/layer-create').length === 1);
  check(same(lastPost('/api/patch/layout/layer-create').body, { name: 'Floor' }), 'New layer posts layer-create {name}');
  await until('the new chip', () => document.querySelectorAll('[data-layer-chip]').some(c => /Floor/.test(c.textContent)));

  // Done editing: taps select again; leaving the screen sends nothing.
  click(document.querySelector('[data-edit-done]'));
  check(toggle.getAttribute('aria-pressed') === 'false' && document.querySelector('[data-editpanel]').hidden, 'Done editing turns edit mode off');
  const n0 = calls.filter(c => c.method === 'POST').length;
  B.get('ConsoleScreen').onLeaveScreen();
  await sleep(100);
  check(calls.filter(c => c.method === 'POST').length === n0, 'leaving the Console screen sends nothing (no stop, no deselect)');

  // ---------------------------------------------------------------- part 2
  // app.js against the real index.html: the screen is registered.
  const screens = {};
  const stub = name => { const s = { calls: [] }; ['init', 'onEnterScreen', 'onLeaveScreen'].forEach(m => { s[m] = () => s.calls.push(m); }); screens[name] = s; return s; };
  const apiCalls = [];
  const fakeApi = new Proxy({}, { get: (t, k) => (...a) => { apiCalls.push(String(k)); return k === 'getSettings' ? Promise.resolve({ artnetStartUniverse: 0 }) : Promise.resolve({}); } });
  const extra = { Api: fakeApi, UI: { setArtnetStart() {} } };
  ['NodesScreen', 'DevicesScreen', 'PatchScreen', 'AnalyzerScreen', 'SendScreen', 'WalkScreen', 'SettingsScreen', 'Workspace', 'ConsoleScreen'].forEach(n => { extra[n] = stub(n); });
  const A = browser([], extra);
  vm.runInContext(fs.readFileSync(path.join(jsDir, 'app.js'), 'utf8'), A.ctx, { filename: 'app.js' });
  await sleep(50);
  const doc = A.document;
  check(!!doc.querySelector('#tabs [data-tab="console"]') && !!doc.querySelector('#tabsMobile [data-tab="console"]'), 'Console has a nav entry on desktop AND mobile');
  check(!!doc.querySelector('section#screen-console.screen'), 'index.html has the screen-console section');
  check(screens.ConsoleScreen.calls.includes('init'), 'app.js initialises ConsoleScreen');
  click(doc.querySelector('#tabs [data-tab="console"]'));
  check(doc.querySelector('#screen-console').classList.contains('active') && screens.ConsoleScreen.calls.filter(c => c === 'onEnterScreen').length === 1, 'tapping Console activates the screen and calls onEnterScreen');
  check(doc.querySelector('#tabsMobile [data-tab="console"]').classList.contains('is-active'), 'both nav copies show Console active');
  const apiBefore = apiCalls.length;
  click(doc.querySelector('#tabsMobile [data-tab="nodes"]'));
  check(screens.ConsoleScreen.calls.includes('onLeaveScreen') && !doc.querySelector('#screen-console').classList.contains('active'), 'leaving Console calls onLeaveScreen');
  check(apiCalls.length === apiBefore, 'leaving Console makes no API call (output is not stopped)', apiCalls.slice(apiBefore));
  const scripts = doc.querySelectorAll('script').map(s => s.getAttribute('src'));
  const at = f => scripts.indexOf('/js/' + f);
  check(at('console.js') > 0 && ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'workspace.js'].every(f => at(f) >= 0 && at(f) < at('console.js')) && at('console.js') < at('app.js'),
    'console.js loads after api/ws/programmer/ui/workspace and before app.js', scripts);
  check(doc.querySelectorAll('link').some(l => l.getAttribute('href') === '/css/screens-console.css'), 'the console stylesheet is linked');

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
