// console_m3_test.js — M3 (owner 2026-11-17): the toolbar's Plan button and its
// panel. Scale: a slider and a number box for mm per grid square; nothing is
// posted while dragging, one scale post on release / Enter / blur, a bad
// number is refused with a message and the boxes go back. Arrange from 3D:
// Arrange selection (confirm only when a chosen fixture was placed by hand;
// a selected cell counts as its parent fixture) and Arrange all… (always
// asks). Literal ui.js and console.js in the real index.html (minidom.js).
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
const BAR = Object.assign(fx('BAR', '5'), { cells: [1, 2, 3].map(i => ({ id: 'c' + i, name: 'Pixel ' + i, index: i })) });
const fixtures = [fx('A', '1'), fx('B', '2'), fx('C', '3'), BAR];
// A is placed by hand; the others are automatic.
const at = { A: [0, 0], B: [2, 0], C: [4, 0], BAR: [6, 0] };
const items = fixtures.map((f, i) => ({ id: 'it-' + f.entryId, kind: 'entry', ref: f.entryId, layer: 'truss', col: at[f.entryId][0], row: at[f.entryId][1], w: 1, h: 1, rot: 0, mode: f.entryId === 'A' ? 'manual' : 'auto', order: i }));
const layout = {
  revision: 1, active: true, name: 'M3 show',
  layout: { layers: [{ id: 'truss', name: 'Truss', zKnown: true, zMin: 6000, zMax: 6000 }, { id: 'unplaced', name: 'Unplaced', system: true }], items, derived: true },
  entries: fixtures.map(f => ({ id: f.entryId, name: f.name, fixtureType: f.fixtureType, fixtureNumber: f.fixtureNumber, itemId: 'it-' + f.entryId })),
  groups: [], unplaced: [], stale: [], limits: {}, objectTypes: [{ type: 'label', label: 'Label', shape: 'point' }],
};

function boot(cellMm) {
  let cur = cellMm;
  const { window, document } = makeWindow();
  document.loadHTML(fs.readFileSync(path.join(JS, '..', 'index.html'), 'utf8'));
  const posts = [], subs = [];
  let replyArranged = 3, replyNoPos = ['X'];
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
    getLayout: async () => { const out = JSON.parse(JSON.stringify(layout)); if (cur !== undefined) out.layout.cellMm = cur; return out; },
    getProgrammerFixtures: async () => ({ revision: 1, fixtures: JSON.parse(JSON.stringify(fixtures)) }),
    layoutAction: async (action, body) => {
      posts.push({ action, body: JSON.parse(JSON.stringify(body || {})) });
      const out = JSON.parse(JSON.stringify(layout));
      if (cur !== undefined) out.layout.cellMm = cur;
      if (action === 'scale') {
        if (body.cellMm === 9999) throw new Error('At 9999 mm per square the plan would not fit the grid.');
        cur = body.cellMm; out.layout.cellMm = cur;
      }
      if (action === 'arrange') { out.arranged = replyArranged; out.noPosition = replyNoPos; }
      return out;
    },
  };
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, Map, Set, window, document, Event,
    getComputedStyle: window.getComputedStyle, localStorage: window.localStorage, Api, ProgrammerSync, Live: { on() {} },
  });
  for (const f of ['ui.js', 'console.js']) vm.runInContext(fs.readFileSync(path.join(JS, f), 'utf8'), ctx, { filename: f });
  vm.runInContext('this.ConsoleScreen = ConsoleScreen;', ctx);
  return { ctx, window, document, posts, state, ProgrammerSync, reply: (a, n) => { replyArranged = a; replyNoPos = n; } };
}

(async () => {
  const B = boot(0);
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const ev = (el, t, extra) => el.dispatchEvent(new Event(t, Object.assign({ bubbles: true }, extra || {})));
  const key = (el, k) => ev(el, 'keydown', { key: k });
  const tile = id => q('[data-gridbox] [data-select-entry="' + id + '"]');
  const status = () => q('[data-status]').textContent;
  const posted = () => posts.filter(p => p.action === 'scale' || p.action === 'arrange');
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  const select = async targets => { await B.ProgrammerSync.act('select', { action: 'set', targets }); await settle(); };
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !tile('A'); i++) await tick();

  // ---- the button and the panel ---------------------------------------------
  const plan = q('[data-plan-menu]');
  check(!!plan && plan.textContent.trim() === 'Arrange', 'the Arrange button exists (owner named it so it is not a second "Plan")', plan && plan.textContent);
  check(plan && plan.getAttribute('aria-haspopup') === 'dialog' && plan.getAttribute('aria-expanded') === 'false', 'Plan has aria-haspopup=dialog and starts collapsed');
  const row = [...q('.b5-con-toolset').children];
  const view = q('[data-view-menu]').closest('.b5-menu');
  const vi = row.indexOf(view);
  check(vi >= 0 && row[vi + 1] === plan, 'Plan is in the toolbar row immediately after the View menu', row.map(e => e.className));
  check(!q('[data-plan-panel]') || q('[data-plan-panel]').hidden, 'the panel is hidden at first');
  click(plan);
  const panel = q('[data-plan-panel]');
  check(plan.getAttribute('aria-expanded') === 'true', 'clicking Plan sets aria-expanded=true');
  check(!!panel && !panel.hidden && panel.getAttribute('role') === 'dialog' && panel.getAttribute('aria-label') === 'Arrange', 'the panel appears with role=dialog and label Arrange');
  check(/mm per grid square\. Changes the plan for everyone using this show; fixture tiles keep their size\./.test(panel.textContent), 'the Scale caption is shown');
  check(/Puts fixtures where the MVR file places them\. Placed groups and objects stay\. No DMX changes\./.test(panel.textContent), 'the Arrange caption is shown');
  key(panel, 'Escape');
  check(panel.hidden && plan.getAttribute('aria-expanded') === 'false', 'Escape closes the panel');
  check(document.activeElement === plan, 'focus is back on Plan after Escape');
  click(plan); click(plan);
  check(panel.hidden && document.activeElement === plan, 'pressing Plan again closes it and focuses Plan');
  click(plan);

  // ---- Scale: initial values --------------------------------------------------
  const slider = () => q('[data-scale-slider]'), box = () => q('[data-scale-input]'), words = () => q('[data-scale-value]').textContent;
  check(slider().getAttribute('min') === '100' && slider().getAttribute('max') === '3000' && slider().getAttribute('step') === '10' && slider().getAttribute('aria-label') === 'Scale, mm per square', 'slider has min 100, max 3000, step 10 and its label');
  check(box().getAttribute('type') === 'number' && box().getAttribute('min') === '50' && box().getAttribute('max') === '10000' && box().getAttribute('step') === '10' && box().getAttribute('aria-label') === 'Scale in mm per square', 'number box has min 50, max 10000, step 10 and its label');
  check(slider().value === '500' && box().value === '500' && words() === '500 mm per square', 'with no cellMm the boxes show 500', [slider().value, box().value, words()]);
  click(plan);
  const B2 = boot(750);
  B2.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !B2.document.querySelector('[data-gridbox] [data-select-entry="A"]'); i++) await tick();
  B2.document.querySelector('[data-plan-menu]').dispatchEvent(new Event('click', { bubbles: true }));
  const q2 = s => B2.document.querySelector(s);
  check(q2('[data-scale-slider]').value === '750' && q2('[data-scale-input]').value === '750' && q2('[data-scale-value]').textContent === '750 mm per square', 'with cellMm 750 the boxes show 750', [q2('[data-scale-slider]').value, q2('[data-scale-input]').value]);
  click(plan);

  // ---- Scale: slider ----------------------------------------------------------
  slider().value = '300'; ev(slider(), 'input');
  check(box().value === '300' && words() === '300 mm per square', 'dragging updates the number box and the words', [box().value, words()]);
  check(posted().length === 0, 'dragging (input) posts nothing', posted());
  ev(slider(), 'change');
  await settle();
  check(same(posted(), [{ action: 'scale', body: { cellMm: 300 } }]), 'release (change) posts exactly one scale 300', posted());
  check(slider().value === '300' && box().value === '300' && words() === '300 mm per square', 'the boxes then show 300', [slider().value, box().value, words()]);
  check(status() === 'Scale 300 mm per square: the whole plan moved; tiles keep their size.', 'success status', status());
  posts.length = 0;
  slider().value = '300'; ev(slider(), 'input'); ev(slider(), 'change');
  await settle();
  check(posted().length === 0, 'the same value posts nothing', posted());

  // ---- Scale: number box --------------------------------------------------------
  box().value = '250'; ev(box(), 'input'); key(box(), 'Enter');
  await settle();
  check(same(posted(), [{ action: 'scale', body: { cellMm: 250 } }]), 'typing 250 + Enter posts scale 250', posted());
  ev(box(), 'blur');
  await settle();
  check(posted().length === 1, 'blur after Enter does not post again', posted());
  posts.length = 0;
  box().value = '20'; ev(box(), 'input'); key(box(), 'Enter');
  await settle();
  check(posted().length === 0, '20 + Enter posts nothing', posted());
  check(/Scale must be 50 to 10000 mm per square\./.test(status()) && q('[data-status]').className.indexOf('--error') > 0, 'and shows the range error', status());
  check(box().value === '250' && slider().value === '250' && words() === '250 mm per square', 'and puts the boxes back to 250', [box().value, slider().value, words()]);
  box().value = ''; key(box(), 'Enter');
  await settle();
  check(posted().length === 0 && box().value === '250', 'an empty box posts nothing and resets', [posted(), box().value]);
  box().value = '400'; ev(box(), 'blur');
  await settle();
  check(same(posted(), [{ action: 'scale', body: { cellMm: 400 } }]), 'blur in the number box posts the value', posted());
  posts.length = 0;
  box().value = '9999'; key(box(), 'Enter');
  await settle();
  check(posted().length === 1 && /would not fit the grid/.test(status()), 'a refused scale (409) shows the server message', status());
  check(box().value === '400' && slider().value === '400' && words() === '400 mm per square', 'and the boxes go back to the layout value', [box().value, slider().value]);

  // ---- Arrange selection --------------------------------------------------------
  const arrSel = () => q('[data-arrange-selection]'), arrAll = () => q('[data-arrange-all]');
  check(arrSel().disabled && arrSel().textContent.trim() === 'Arrange selection', 'Arrange selection is disabled with nothing selected');
  check(!arrAll().disabled && arrAll().textContent.trim() === 'Arrange all…', 'Arrange all… is always enabled');
  posts.length = 0;
  await select([{ entryId: 'B', cell: '' }, { entryId: 'A', cell: '' }]);
  check(!arrSel().disabled, 'selecting fixtures enables Arrange selection');
  click(arrSel());
  await settle();
  const dlg = q('[data-ask]');
  check(!!dlg && /Arrange 2 fixtures from 3D\?/.test(dlg.textContent) && /1 of them were placed by hand and will move to their MVR positions\./.test(dlg.textContent), 'a hand-placed fixture in the selection asks first', dlg && dlg.textContent);
  check(posted().length === 0, 'nothing is posted before the answer', posted());
  check(q('[data-ask-ok]').textContent.trim() === 'Arrange', 'the confirm button says Arrange');
  click(q('[data-ask-cancel]'));
  await settle();
  check(!q('[data-ask]') && posted().length === 0, 'Cancel posts nothing');
  click(arrSel());
  await settle();
  click(q('[data-ask-ok]'));
  await settle();
  check(same(posted(), [{ action: 'arrange', body: { ids: ['B', 'A'] } }]), 'confirm posts arrange for both ids in selection order', posted());
  check(status() === 'Arranged 3 fixture(s) from 3D. 1 had no position and stayed where they were.', 'status names the count and the no-position fixtures', status());

  // Only automatic fixtures: no dialog. A selected cell counts as its parent.
  posts.length = 0;
  B.reply(2, []);
  await select([{ entryId: 'BAR', cell: 'c2' }, { entryId: 'C', cell: '' }, { entryId: 'BAR', cell: 'c3' }]);
  click(arrSel());
  await settle();
  check(!q('[data-ask]'), 'automatic fixtures only: no dialog');
  check(same(posted(), [{ action: 'arrange', body: { ids: ['BAR', 'C'] } }]), 'cells count as their parent fixture, each once', posted());
  check(status() === 'Arranged 2 fixture(s) from 3D.', 'status without a no-position tail', status());

  // ---- Arrange all ---------------------------------------------------------------
  posts.length = 0;
  await select([]);
  check(arrSel().disabled, 'clearing the selection disables Arrange selection again');
  click(arrAll());
  await settle();
  const d2 = q('[data-ask]');
  check(!!d2 && /Arrange every fixture from 3D\?/.test(d2.textContent) && /Fixtures you placed by hand move to their MVR positions\. Placed groups and objects stay\. No DMX changes\./.test(d2.textContent) && q('[data-ask-ok]').textContent.trim() === 'Arrange all', 'Arrange all… always asks', d2 && d2.textContent);
  click(q('[data-ask-cancel]'));
  await settle();
  check(posted().length === 0, 'Cancel posts nothing');
  B.reply(4, []);
  click(arrAll());
  await settle();
  click(q('[data-ask-ok]'));
  await settle();
  check(same(posted(), [{ action: 'arrange', body: { all: true, confirm: 'ARRANGE' } }]), 'confirm posts all + ARRANGE', posted());
  check(status() === 'Arranged 4 fixture(s) from 3D.', 'status counts the arranged fixtures', status());

  // ---- the panel works in edit mode too -------------------------------------------
  click(q('[data-view-menu]'));
  click(q('[data-edit-toggle]'));
  await settle();
  check(!q('[data-plan-panel]').hidden && !arrAll().disabled && !slider().disabled, 'the panel stays usable in edit mode');

  console.log(failures ? failures + ' failure(s)' : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
