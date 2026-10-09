// faders_default_test.js — I2a: the group fader bar's starting state, run on
// the LITERAL faders.js in the real index.html (minidom.js).
// (docs/design/console-lite/component-specs.md §19; owner brief I2a)
//  1. With no remembered choice the bar starts COLLAPSED (still a visible
//     bar) on phones, tablets and ordinary desktops, e.g. 1440x900, so it
//     leaves the work area free; it starts open only on a desktop tall
//     enough to keep it.
//  2. A remembered choice ('0' open / '1' collapsed) always wins.
//  3. Blocked storage (private mode) falls back to the default, no throw.
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
function boot({ stored, tall, blocked }) {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(fs.readFileSync(path.join(JS, '..', 'index.html'), 'utf8'));
  const store = new Map();
  if (stored !== undefined) store.set('benny512.faders.collapsed', stored);
  const localStorage = blocked
    ? { getItem() { throw new Error('SecurityError'); }, setItem() { throw new Error('SecurityError'); } }
    : { getItem: k => (store.has(k) ? store.get(k) : null), setItem: (k, v) => store.set(k, String(v)) };
  const queries = [];
  const ctx = vm.createContext({
    console, setTimeout, clearTimeout, Promise, JSON, Math, Date, window, document, Event, CustomEvent, localStorage,
    matchMedia: q => { queries.push(q); return { matches: !!tall, media: q }; },
    Api: { getFaders: async () => ({ revision: 1, output: { state: 'disarmed' }, faders: [] }) },
    Live: { on() {} },
  });
  vm.runInContext(fs.readFileSync(path.join(JS, 'faders.js'), 'utf8') + '\nthis.Faders = Faders;', ctx, { filename: 'faders.js' });
  ctx.Faders.init();
  const toggle = document.querySelector('[data-faders-toggle]');
  const body = document.querySelector('[data-faders-body]');
  return { toggle, body, store, queries, collapsed: !!body.hidden && /show/.test(toggle.textContent) && toggle.getAttribute('aria-expanded') === 'false' };
}
(async () => {
  let b = boot({});
  check(b.collapsed, 'no remembered choice, ordinary desktop (1440x900): the bar starts collapsed', b.toggle.textContent);
  check(!!b.toggle, 'collapsed is still a visible bar with its Faders · show toggle');
  b.toggle.dispatchEvent(new Event('click', { bubbles: true }));
  check(b.store.get('benny512.faders.collapsed') === '0' && !b.body.hidden, 'opening it is remembered for this browser', [...b.store]);
  b = boot({ tall: true });
  check(!b.collapsed && b.queries.some(q => /min-width/.test(q) && /min-height/.test(q)), 'no remembered choice on a tall desktop: the bar starts open', b.queries);
  b = boot({ stored: '0' });
  check(!b.collapsed, 'remembered open (0) wins over the collapsed default');
  b = boot({ stored: '1', tall: true });
  check(b.collapsed, 'remembered collapsed (1) wins over the open default');
  b = boot({ blocked: true });
  check(b.collapsed, 'blocked storage: falls back to the default without throwing');
  console.log(failures ? `${failures} failure(s)` : 'all passed');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  threw: ' + (e && e.stack || e)); process.exit(1); });
