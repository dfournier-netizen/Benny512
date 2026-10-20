// console_k3_test.js — K3: the Console's docked and floating windows (W1
// component-specs §4 and §8; owner answers 2026-10-18/19). Runs the LITERAL
// ui.js, console-windows.js and console.js in the real index.html
// (minidom.js); ProgrammerSync and Api are stand-ins. matchMedia is the
// only thing switched between "narrow" (below 1024 px) and "wide".
//
// WHAT IT PROTECTS
//  1. Below 1024 px nothing changes: every region stays where it was and no
//     window is shown.
//  2. From 1024 px: Controls (with Tools inside it) is open and docked on
//     the right; Selection order and Groups are hidden and float when
//     opened; the stored-groups toolbar row is emptied into the Groups window.
//     (K4 took Tests out of the windows: it is the Tests tab's own panel.)
//  3. The Windows menu lists each window with where it is, opens and closes
//     them, and Reset puts back only the window defaults.
//  4. Title-bar Dock/Float, collapse and close; arrow keys move a floating
//     window (10 px, Shift 40 px); a drag released on the right edge docks
//     it, and Escape during the drag puts it back.
//  5. The dock edge is a separator: Left/Right change the width by 20 px,
//     clamped at 320 px; closing every docked window removes the column.
//  6. The choices are remembered per browser and restored on a new page;
//     going back below 1024 px puts every region home in its old order.
//  7. Nothing here writes to the programmer or the wire.
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
  revision: 1, active: true, name: 'K3 show',
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
  const ProgrammerSync = {
    state: () => state, refresh: () => Promise.resolve(state), onChange: fn => subs.push(fn),
    act: (action, body) => { posts.push({ action, body }); state.revision++; subs.forEach(fn => fn(state)); return Promise.resolve(state); },
  };
  const Api = {
    getPatch: async () => ({}),
    getLayout: async () => JSON.parse(JSON.stringify(layout)),
    getProgrammerFixtures: async () => ({ revision: 1, fixtures: JSON.parse(JSON.stringify(fixtures)) }),
    layoutAction: async () => JSON.parse(JSON.stringify(layout)),
  };
  const media = { wide };
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, Map, Set, window, document, Event, CustomEvent,
    getComputedStyle: window.getComputedStyle, localStorage: window.localStorage, Api, ProgrammerSync, Live: { on() {} },
    matchMedia: q => ({ matches: /min-width: 1024px/.test(q) ? media.wide : false }),
  });
  // console-windows.js is loaded when present, so this file can be run
  // against the K2 tree for the fail-before proof (it then fails on asserts,
  // not on a missing file).
  for (const f of ['ui.js', 'console-windows.js', 'console.js']) {
    const p = path.join(JS, f);
    if (fs.existsSync(p)) vm.runInContext(fs.readFileSync(p, 'utf8'), ctx, { filename: f });
  }
  vm.runInContext('this.ConsoleScreen = ConsoleScreen;', ctx);
  return { ctx, window, document, posts, media };
}

(async () => {
  // ---- 1. narrow: unchanged ------------------------------------------------------
  {
    const B = boot(false);
    const q = s => B.document.querySelector(s);
    B.ctx.ConsoleScreen.onEnterScreen();
    for (let i = 0; i < 20 && !q('[data-select-entry="W1"]'); i++) await tick();
    const side = q('.b5-con-pane--side');
    check([...B.document.querySelectorAll('[data-win]')].every(w => w.hidden), 'below 1024 px no window is shown');
    check(q('[data-controls-region]').parentNode === side && q('[data-tests-region]').parentNode === side && q('[data-tools-region]').parentNode === side, 'below 1024 px Controls, Tests and Tools stay in the side column');
    check(q('.b5-con-order').parentNode === q('.b5-con-pane--grid') && q('.b5-con-grouprow').contains(q('[aria-label="Stored groups"]')), 'below 1024 px the order list stays under the grid and the groups stay in their toolbar row');
  }

  // ---- 2. wide: the defaults ---------------------------------------------------------
  const B = boot(true);
  const { document, posts } = B;
  const q = s => document.querySelector(s);
  const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
  const key = (el, k, init) => el.dispatchEvent(new Event('keydown', Object.assign({ key: k, bubbles: true }, init || {})));
  const ptr = (el, type, x, y) => el.dispatchEvent(new Event(type, { bubbles: true, clientX: x, clientY: y, pointerId: 1, target: el }));
  const win = id => q('[data-win="' + id + '"]');
  const winMenu = () => { const t = q('[data-windows-menu]'); const trig = t.classList.contains('b5-menu__trigger') ? t : t.querySelector('.b5-menu__trigger') || t.closest('.b5-menu').querySelector('.b5-menu__trigger'); if (trig.getAttribute('aria-expanded') !== 'true') click(trig); return trig.closest('.b5-menu'); };
  const menuItem = attr => winMenu().querySelector('[' + attr + ']');
  B.ctx.ConsoleScreen.onEnterScreen();
  for (let i = 0; i < 20 && !q('[data-select-entry="W1"]'); i++) await tick();
  const main = q('.b5-con-main');
  // minidom has no layout: give the workspace a laptop-sized box.
  main.clientWidth = 1400; main.clientHeight = 580;
  check(!!q('[data-windows-menu]'), 'the toolbar has a Windows menu');
  check(!!win('controls') && !win('controls').hidden && win('controls').classList.contains('is-docked') && q('[data-dock-stack]').contains(win('controls')), 'from 1024 px Controls is open and docked on the right');
  check(!!win('controls') && win('controls').contains(q('[data-controls-region]')) && win('controls').contains(q('[data-tools-region]')), 'the Controls window holds the controls, with Tools inside it');
  check(['order', 'groups'].every(id => win(id) && win(id).hidden), 'Selection order and Groups start hidden');
  check(!!win('groups') && win('groups').contains(q('[aria-label="Stored groups"]')) && !q('.b5-con-grouprow').contains(q('[aria-label="Stored groups"]')), 'the stored groups move out of the toolbar row into the Groups window');
  check(main.classList.contains('is-windowed') && !main.classList.contains('is-dock-empty'), 'the workspace has a dock column');
  const sub = q('[data-win="controls"] [data-win-sub]');
  check(!!sub && sub.textContent === 'nothing selected', 'the Controls title bar says what is selected, in words', sub && sub.textContent);
  check(!!q('[data-win-store-group]') && q('[data-win-store-group]').disabled, 'Store group in the Groups window is disabled with nothing selected');

  // ---- 3. the Windows menu --------------------------------------------------------
  const labels = () => [...winMenu().querySelectorAll('[data-win-toggle]')].map(b => b.textContent.trim() + '|' + b.getAttribute('aria-checked'));
  check(same(labels(), ['Controls · docked|true', 'Selection order|false', 'Groups|false']), 'the Windows menu lists each window, checked when open, with where it is', labels());
  check(!!menuItem('data-win-reset'), 'the Windows menu ends with Reset window positions');
  click(menuItem('data-win-toggle="order"'));
  check(!win('order').hidden && win('order').classList.contains('is-floating') && document.activeElement === q('[data-win-bar="order"]'), 'opening Selection order shows it floating and focuses its title bar');
  check(win('order').contains(q('.b5-con-order')), 'the Selection order window holds the order list');
  click(menuItem('data-win-toggle="groups"'));
  const xy = id => [win(id).style.getPropertyValue('left'), win(id).style.getPropertyValue('top')];
  check(!same(xy('order'), xy('groups')), 'a second floating window opens offset from the first, not on top of it', [xy('order'), xy('groups')]);

  // ---- 4. title bars ------------------------------------------------------------------
  const bar = q('[data-win-bar="order"]');
  const x0 = parseFloat(win('order').style.getPropertyValue('left'));
  const y0 = parseFloat(win('order').style.getPropertyValue('top'));
  key(bar, 'ArrowDown');
  check(parseFloat(win('order').style.getPropertyValue('top')) === y0 + 10, 'ArrowDown on a floating title bar moves it 10 px', win('order').style.getPropertyValue('top'));
  key(bar, 'ArrowDown', { shiftKey: true });
  check(parseFloat(win('order').style.getPropertyValue('top')) === y0 + 50, 'Shift+ArrowDown moves it 40 px more', win('order').style.getPropertyValue('top'));
  const fold = q('[data-win-fold="order"]');
  click(fold);
  check(q('[data-win-body="order"]').hidden && fold.getAttribute('aria-expanded') === 'false' && document.activeElement === fold, 'collapse hides the body, says so, and keeps focus on its own toggle');
  click(fold);
  check(!q('[data-win-body="order"]').hidden, 'expand shows the body again');
  // A drag that ends at the right edge docks; Escape during one puts it back.
  ptr(bar, 'pointerdown', x0 + 20, y0 + 10); ptr(bar, 'pointermove', 5, 30);
  check(!q('[data-dock-drop]').hidden, 'dragging to the right edge shows "Release to dock on the right"', q('[data-dock-drop]').textContent);
  key(bar, 'Escape');
  check(q('[data-dock-drop]').hidden && win('order').classList.contains('is-floating'), 'Escape during the drag cancels it: no drop target, still floating');
  ptr(bar, 'pointerdown', x0 + 20, y0 + 10); ptr(bar, 'pointermove', 5, 30); ptr(bar, 'pointerup', 5, 30);
  check(win('order').classList.contains('is-docked') && q('[data-dock-stack]').contains(win('order')), 'releasing on the right edge docks the window');
  const stackIds = () => [...q('[data-dock-stack]').childNodes].map(n => n.getAttribute && n.getAttribute('data-win')).filter(Boolean);
  check(same(stackIds(), ['controls', 'order', 'groups', 'tests'].filter(id => q('[data-dock-stack]').contains(win(id)))) && stackIds()[0] === 'controls', 'the dock keeps its order: Controls first, then Selection order', stackIds());
  const dockBtn = q('[data-win-dock="order"]');
  check(dockBtn.textContent.trim() === 'Float' && /Float the Selection order window/.test(dockBtn.getAttribute('aria-label')), 'a docked window offers Float, in words', dockBtn.textContent);
  click(dockBtn);
  check(win('order').classList.contains('is-floating') && q('[data-win-dock="order"]').textContent.trim() === 'Dock', 'Float puts it back over the grid, and the button then says Dock');

  // ---- 5. the dock edge, and an empty dock ---------------------------------------------
  const edge = q('[data-dock-edge]');
  check(edge.getAttribute('role') === 'separator' && edge.getAttribute('aria-valuenow') === '440' && edge.getAttribute('aria-valuemin') === '320', 'the dock edge is a separator at 440 px, minimum 320', [edge.getAttribute('role'), edge.getAttribute('aria-valuenow')]);
  key(edge, 'ArrowLeft');
  check(edge.getAttribute('aria-valuenow') === '460' && main.style.getPropertyValue('--b5-dock-w') === '460px', 'Left widens the dock by 20 px', edge.getAttribute('aria-valuenow'));
  for (let i = 0; i < 12; i++) key(edge, 'ArrowRight');
  check(edge.getAttribute('aria-valuenow') === '320', 'Right narrows it, never below 320 px', edge.getAttribute('aria-valuenow'));
  click(q('[data-win-close="controls"]'));
  check(win('controls').hidden && main.classList.contains('is-dock-empty'), 'closing the last docked window removes the dock column (the grid takes the width)');
  const trig = q('[data-windows-menu]').classList.contains('b5-menu__trigger') ? q('[data-windows-menu]') : q('.b5-con-winmenu .b5-menu__trigger');
  check(document.activeElement === trig, 'closing a window returns focus to the Windows menu');
  check(win('controls').contains(q('[data-controls-region]')), 'a closed window keeps its contents (only hidden)');

  // ---- 6. memory and reset ---------------------------------------------------------------
  const saved = JSON.parse(B.window.localStorage.getItem('benny512.console.windows') || '{}');
  check(saved.dockW === 320 && saved.controls && saved.controls.open === false && saved.order && saved.order.open === true && saved.order.docked === false, 'window choices are remembered in this browser', saved);
  const B2 = boot(true, { 'benny512.console.windows': B.window.localStorage.getItem('benny512.console.windows') });
  B2.ctx.ConsoleScreen.onEnterScreen();
  await tick();
  const w2 = id => B2.document.querySelector('[data-win="' + id + '"]');
  check(w2('controls').hidden && !w2('order').hidden && w2('order').classList.contains('is-floating') && B2.document.querySelector('[data-dock-edge]').getAttribute('aria-valuenow') === '320', 'a new page in the same browser restores them');
  click(menuItem('data-win-reset'));
  check(!win('controls').hidden && win('controls').classList.contains('is-docked') && ['order', 'groups'].every(id => win(id).hidden) && edge.getAttribute('aria-valuenow') === '440', 'Reset window positions: Controls docked at 440 px, the rest hidden');

  // ---- 7. back below 1024 px --------------------------------------------------------------
  B.media.wide = false;
  B.window.dispatchEvent(new Event('resize'));
  const side = q('.b5-con-pane--side');
  const kids = [...side.childNodes].filter(n => n.nodeType === 1).map(n => n.hasAttribute('data-controls-region') ? 'controls' : n.hasAttribute('data-tests-region') ? 'tests' : n.hasAttribute('data-tools-region') ? 'tools' : null).filter(Boolean);
  check(same(kids, ['controls', 'tests', 'tools']), 'below 1024 px again every region is home in its old order: Controls, Tests, Tools', kids);
  check(q('.b5-con-order').parentNode === q('.b5-con-pane--grid') && q('.b5-con-grouprow').contains(q('[aria-label="Stored groups"]')) && [...document.querySelectorAll('[data-win]')].every(w => w.hidden), 'the order list and the groups are home too, and no window shows');
  check(!main.classList.contains('is-windowed'), 'and the workspace has no dock column');

  check(!posts.length, 'none of this wrote to the programmer', posts.map(p => p.action));
  console.log(failures ? failures + ' FAILURE(S)' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
