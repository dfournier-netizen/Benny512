// ui_menu_test.js — K1: the shared menu (UI.menu, W1 component-specs §2) run
// against the LITERAL ui.js the browser loads (internal/web/ui_menu_k1_test.go).
// Zero dependencies (minidom.js).
//
// WHAT IT PROTECTS
//  1. The trigger is a real button announcing a menu (aria-haspopup=menu)
//     and its state (aria-expanded), and the panel is role=menu.
//  2. Opening focuses the first item; Up/Down/Home/End move within the menu;
//     Escape closes it and returns focus to the trigger; Tab closes it.
//  3. A checkable item is a menuitemcheckbox with aria-checked AND a visible
//     check symbol (never an invisible state); disabled items are skipped.
//  4. Choosing an item closes the menu and runs its action; a press outside
//     closes it without running anything.
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
const { window, document } = makeWindow();
const ctx = vm.createContext({ window, document, console, localStorage: window.localStorage });
vm.runInContext(fs.readFileSync(path.join(JS, 'ui.js'), 'utf8') + '\nthis.UI = UI;', ctx, { filename: 'ui.js' });
const UI = ctx.UI;
const key = (el, k, extra) => el.dispatchEvent(new Event('keydown', Object.assign({ key: k, bubbles: true, cancelable: true }, extra || {})));

const ran = [];
const m = UI.menu({ label: 'Show', attrs: { 'data-showmenu': '' }, items: [
  { label: 'Show tools…', attrs: { 'data-tools': '' }, onSelect: () => ran.push('tools') },
  { label: 'Skipped', disabled: true },
  { separator: true },
  { label: 'Ghosts', checked: true, onSelect: () => ran.push('ghosts') },
  { label: 'Fixture library…', attrs: { 'data-library': '' }, danger: true, onSelect: () => ran.push('library') },
] });
const outside = document.createElement('div');
document.body.appendChild(m.el); document.body.appendChild(outside);
const t = m.button, p = m.panel;

check(t.tagName.toLowerCase() === 'button' && t.getAttribute('type') === 'button', 'the trigger is a native button');
check(t.getAttribute('aria-haspopup') === 'menu' && t.getAttribute('aria-expanded') === 'false', 'the trigger announces a closed menu', [t.getAttribute('aria-haspopup'), t.getAttribute('aria-expanded')]);
check(t.hasAttribute('data-showmenu') && /Show/.test(t.textContent), 'the trigger carries its label and attributes', t.textContent);
check(p.getAttribute('role') === 'menu' && p.hidden, 'the panel is a hidden role=menu until opened');

t.click();
const items = [...p.querySelectorAll('button')];
check(!p.hidden && t.getAttribute('aria-expanded') === 'true', 'a click opens it and sets aria-expanded');
check(items.length === 4 && items.map(b => b.getAttribute('role')).join() === 'menuitem,menuitem,menuitemcheckbox,menuitem', 'items have menu roles (checkable = menuitemcheckbox)', items.map(b => b.getAttribute('role')));
check(!!p.querySelector('[role="separator"]'), 'a separator is a role=separator rule');
check(document.activeElement === items[0], 'opening focuses the first item', document.activeElement && document.activeElement.textContent);
const ghosts = items[2];
check(ghosts.getAttribute('aria-checked') === 'true' && !!ghosts.querySelector('.b5-menu__check svg'), 'a checked item has aria-checked AND a visible check symbol');
check(items[0].hasAttribute('data-tools') && items[3].hasAttribute('data-library'), 'item attributes are kept (data-tools / data-library hooks)');
check(items[3].classList.contains('b5-menu__item--danger'), 'a danger item is marked by its own class (its words stay)');

key(items[0], 'ArrowDown');
check(document.activeElement === ghosts, 'ArrowDown skips the disabled item', document.activeElement && document.activeElement.textContent);
key(ghosts, 'End');
check(document.activeElement === items[3], 'End goes to the last item');
key(items[3], 'ArrowDown');
check(document.activeElement === items[0], 'ArrowDown wraps to the first item');
key(items[0], 'ArrowUp');
check(document.activeElement === items[3], 'ArrowUp wraps to the last item');
key(items[3], 'Home');
check(document.activeElement === items[0], 'Home goes to the first item');
key(items[0], 'Escape');
check(p.hidden && t.getAttribute('aria-expanded') === 'false' && document.activeElement === t, 'Escape closes and returns focus to the trigger');
check(ran.length === 0, 'navigating runs nothing', ran);

t.click();
[...p.querySelectorAll('button')][0].click();
check(p.hidden && ran.join() === 'tools', 'choosing an item closes the menu and runs its action', ran);

t.click();
document.dispatchEvent(Object.assign(new Event('pointerdown', { bubbles: true }), {}));
outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
check(p.hidden && ran.join() === 'tools', 'a press outside closes without running anything', ran);

t.click();
key([...p.querySelectorAll('button')][0], 'Tab');
check(p.hidden, 'Tab closes the menu');

console.log(failures ? `\n${failures} FAILED` : '\nall passed');
process.exit(failures ? 1 : 0);
