// theme_switch_test.js — C9a: the Dark / Daylight theme choice, run by
// internal/web/console_c9_test.go against the LITERAL index.html, theme.js,
// ui.js, settings.js and theme-settings.js the browser loads. Zero deps.
//
// WHAT IT PROTECTS (owner request, Dom 2026-10-09; theme-contract.md)
//  1. Each browser keeps its own theme (localStorage), Dark by default, and
//     a missing, unknown or unreadable stored value falls back to Dark.
//  2. The theme is set before first paint: theme.js is a blocking script in
//     <head>, and it sets data-theme as it runs.
//  3. Settings offers Dark (default) and Daylight, says in words that
//     Daylight is not for dark-venue checking and that the choice is for
//     this browser only, and applies a choice at once with no reload.
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { makeWindow, Event } = require('./minidom.js');

const jsDir = path.join(__dirname, '..');
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + detail : ''));
}
const read = f => fs.readFileSync(path.join(jsDir, f), 'utf8');
let themeSrc = '';
try { themeSrc = read('theme.js'); } catch (e) { check(false, 'static/js/theme.js exists', e.message); }

// --- 2. index.html loads theme.js blocking, in <head> ----------------------
const html = fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8');
const head = html.slice(0, html.indexOf('</head>'));
const tag = (head.match(/<script[^>]*src="\/js\/theme\.js"[^>]*>/) || [''])[0];
check(tag !== '', 'index.html loads /js/theme.js inside <head>', 'no <script src="/js/theme.js"> before </head>');
check(tag !== '' && !/\b(async|defer|type="module")\b/.test(tag), 'theme.js is a blocking script (runs before first paint)', tag);
check(/<script src="\/js\/theme-settings\.js"><\/script>/.test(html), 'index.html loads theme-settings.js');

// --- 1. theme.js on its own, as the <head> runs it ------------------------
function boot(store, opts) {
  opts = opts || {};
  const attrs = { 'data-theme': 'dark' };
  const props = {};
  const documentElement = {
    getAttribute: k => (k in attrs ? attrs[k] : null),
    setAttribute: (k, v) => { attrs[k] = String(v); },
    style: { setProperty: (k, v) => { props[k] = v; }, removeProperty: k => { delete props[k]; }, getPropertyValue: k => props[k] || '' },
  };
  const reloads = [];
  const localStorage = {
    getItem: k => { if (opts.readThrows) throw new Error('SecurityError'); return k in store ? store[k] : null; },
    setItem: (k, v) => { if (opts.writeThrows) throw new Error('QuotaExceededError'); store[k] = String(v); },
    removeItem: k => { delete store[k]; },
  };
  const window = { localStorage, location: { reload: () => reloads.push(1) } };
  window.window = window;
  const ctx = vm.createContext({ window, document: { documentElement }, localStorage: window.localStorage, console, JSON });
  let threw = null;
  try { vm.runInContext(themeSrc, ctx, { filename: 'theme.js' }); } catch (e) { threw = e; }
  let T;
  try { T = vm.runInContext('B5Theme', ctx); } catch (e) { T = undefined; }
  return { attrs, props, store, reloads, threw, T };
}

if (themeSrc) {
  let b = boot({});
  check(!b.threw && b.attrs['data-theme'] === 'dark', 'nothing stored: Dark', b.threw || b.attrs['data-theme']);
  b = boot({ 'b5-theme': 'light' });
  check(b.attrs['data-theme'] === 'light', 'stored Daylight is applied as the head script runs', b.attrs['data-theme']);
  b = boot({ 'b5-theme': 'purple' });
  check(b.attrs['data-theme'] === 'dark', 'an unknown stored value falls back to Dark', b.attrs['data-theme']);
  b = boot({ 'b5-theme': 'light' }, { readThrows: true });
  check(!b.threw && b.attrs['data-theme'] === 'dark', 'unreadable storage (private window, blocked site data) falls back to Dark without an error', b.threw || b.attrs['data-theme']);

  b = boot({});
  const T = b.T;
  check(T && typeof T.choose === 'function' && typeof T.current === 'function', 'B5Theme exposes choose() and current()');
  if (T) {
    const ok = T.choose('light');
    check(b.attrs['data-theme'] === 'light' && T.current() === 'light', 'choose("light") applies Daylight at once', b.attrs['data-theme']);
    check(b.store['b5-theme'] === 'light' && ok === true, 'choose("light") remembers it in this browser\'s localStorage', JSON.stringify(b.store));
    T.choose('dark');
    check(b.attrs['data-theme'] === 'dark' && b.store['b5-theme'] === 'dark', 'choose("dark") goes back to Dark and remembers it');
    check(b.reloads.length === 0, 'switching never reloads the page');
  }
  // A custom theme this browser uses is applied before the first paint
  // from the copy kept in localStorage; a copy that does not match, or that
  // carries anything but opaque #rrggbb, is not applied (Dark instead).
  const cached = { id: 't1', name: 'FOH', base: 'dark', colors: { 'text-muted': '#c0b8aa', 'accent-border': '#86d6d0' }, preview: 0.1 };
  b = boot({ 'b5-theme': 'custom:t1', 'b5-theme-custom': JSON.stringify(cached) });
  check(b.attrs['data-theme'] === 'dark' && b.props['--b5-color-text-muted'] === '#c0b8aa' && b.props['--b5-color-accent-border'] === '#86d6d0' && b.props['--b5-preview-max-brightness'] === '0.1',
    'a stored custom theme is painted as the head script runs (its base block plus its roles on <html>)', JSON.stringify(b.props));
  b = boot({ 'b5-theme': 'custom:t2', 'b5-theme-custom': JSON.stringify(cached) });
  check(b.attrs['data-theme'] === 'dark' && Object.keys(b.props).length === 0, 'a stored custom choice whose copy is another theme falls back to Dark', JSON.stringify(b.props));
  b = boot({ 'b5-theme': 'custom:t1', 'b5-theme-custom': JSON.stringify(Object.assign({}, cached, { colors: { 'text-muted': 'rgba(0,0,0,.5)' } })) });
  check(Object.keys(b.props).length === 0 && b.attrs['data-theme'] === 'dark', 'a copy carrying a non-opaque value is never painted', JSON.stringify(b.props));
  b = boot({ 'b5-theme': 'custom:t1', 'b5-theme-custom': '{not json' });
  check(!b.threw && b.attrs['data-theme'] === 'dark', 'a damaged copy falls back to Dark without an error');
  b = boot({ 'b5-theme': 'custom:t1', 'b5-theme-custom': JSON.stringify(cached) });
  if (b.T && b.T.sync) {
    const what = b.T.sync([]);
    check(what === 'deleted' && b.attrs['data-theme'] === 'dark' && Object.keys(b.props).length === 0 && b.store['b5-theme'] === 'dark',
      'sync(): the server no longer has the theme this browser uses — back to Dark, and remembered', what);
  } else check(false, 'B5Theme.sync exists');

  b = boot({}, { writeThrows: true });
  if (b.T) {
    const ok = b.T.choose('light');
    check(b.attrs['data-theme'] === 'light' && ok === false, 'storage that refuses a write: the theme still applies now, and choose() reports it was not remembered', ok);
  }
}

// --- 3. The Settings choice (real index.html + settings.js) ---------------
(async () => {
  const { window, document } = makeWindow();
  document.loadHTML(html);
  const sandbox = {
    console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, Math, Date, URL,
    window, document, Event, localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
    matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
    location: { reload: () => { sandbox.reloaded = true; } },
    Api: new Proxy({}, { get: (t, k) => async () => (k === 'getNICs' ? [] : k === 'getSettings' ? { timeoutProfiles: {}, universeProtocols: [] } : k === 'getThemes' ? { roles: [], labels: {}, bases: { dark: {}, light: {} }, preview: 0.12, themes: [] } : null) }),
  };
  window.location = sandbox.location;
  const ctx = vm.createContext(sandbox);
  for (const f of ['theme.js', 'ui.js', 'settings.js', 'theme-settings.js']) {
    try { vm.runInContext(read(f), ctx, { filename: f }); } catch (e) { check(false, 'load ' + f, e.message); }
  }
  const get = expr => { try { return vm.runInContext(expr, ctx); } catch (e) { return undefined; } };
  try { get('SettingsScreen').init(); } catch (e) { check(false, 'SettingsScreen.init', e.message); }
  try { get('ThemeSettings').init(); } catch (e) { check(false, 'ThemeSettings.init', e && e.message); }
  await new Promise(r => setTimeout(r, 30));
  const sel = document.getElementById('themeSelect');
  check(!!sel, 'Settings has a Theme choice (#themeSelect)');
  const root = document.getElementById('screen-settings');
  const text = root ? root.textContent.replace(/\s+/g, ' ') : '';
  if (sel) {
    const opts = sel.querySelectorAll('option').map(o => ({ v: o.getAttribute('value'), t: o.textContent }));
    const dark = opts.find(o => o.v === 'dark'), light = opts.find(o => o.v === 'light');
    check(dark && /Dark/.test(dark.t) && /default/i.test(dark.t), 'option Dark (default)', JSON.stringify(opts));
    check(light && /Daylight/.test(light.t) && /not for dark-venue checking/.test(light.t), 'option Daylight says "not for dark-venue checking"', JSON.stringify(opts));
    check(sel.value === 'dark', 'the choice starts on this browser\'s theme (Dark)', sel.value);
  }
  check(/this browser only/i.test(text), 'Settings says the theme is for this browser only', text.slice(0, 200));
  check(/not part of Apply settings|applies at once/i.test(text), 'Settings says the theme applies at once, outside Apply settings');
  if (sel) {
    sel.value = 'light';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    check(document.documentElement.getAttribute('data-theme') === 'light', 'choosing Daylight applies it live', document.documentElement.getAttribute('data-theme'));
    check(window.localStorage.getItem('b5-theme') === 'light', 'and stores it for this browser');
    check(!sandbox.reloaded, 'with no reload');
    const warn = root.textContent.replace(/\s+/g, ' ');
    check(/Daylight is not for dark-venue checking/.test(warn), 'with Daylight on, a warning says in words it is not for dark-venue checking');
    sel.value = 'dark';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    check(document.documentElement.getAttribute('data-theme') === 'dark' && window.localStorage.getItem('b5-theme') === 'dark', 'choosing Dark switches back');
  }
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})();
