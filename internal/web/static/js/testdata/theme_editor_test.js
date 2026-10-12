// theme_editor_test.js — C9b: the custom theme editor in Settings, run by
// internal/web/console_c9_test.go against the REAL server and the LITERAL
// index.html, api.js, ui.js, theme.js, settings.js and theme-settings.js.
//   node theme_editor_test.js <jsDir> <baseURL>
//
// WHAT IT PROTECTS (owner request, Dom 2026-10-09; theme-contract.md)
//  1. A draft is checked live with the contract; each failed pair is listed
//     in words ("Muted text on overlay is 3.9:1; needs 4.5:1."), the draft
//     is labelled "Theme needs changes" and Save is blocked.
//  2. A draft previews on this page only; Revert puts this browser's theme
//     back; leaving Settings ends the preview.
//  3. Save goes to the server (station settings), and this browser then
//     uses the theme; Delete asks in the non-modal dialog; a browser whose
//     theme was deleted elsewhere goes back to Dark.
//  4. Export / import of a small JSON file.
'use strict';
const fs = require('fs');
const path = require('path');
const vm = require('vm');
const { makeWindow, Event } = require('./minidom.js');

const [jsDir, base] = process.argv.slice(2);
let failures = 0;
function check(ok, what, detail) {
  if (ok) { console.log('  PASS  ' + what); return; }
  failures++;
  console.log('  FAIL  ' + what + (detail !== undefined ? '\n        ' + detail : ''));
}
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(what, fn, ms) {
  const end = Date.now() + (ms || 3000);
  while (Date.now() < end) {
    try { if (await fn()) return true; } catch (_) { /* not yet */ }
    await sleep(15);
  }
  check(false, 'timed out waiting for ' + what);
  return false;
}
async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  return { status: r.status, body: t ? JSON.parse(t) : null };
}

const { window, document, CustomEvent } = makeWindow();
document.loadHTML(fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8'));
const calls = [];
const blobs = [];
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, Math, Date, URLSearchParams, btoa,
  window, document, Event, CustomEvent, localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
  // nodes.js's global, which settings.js uses for universe rows (not loaded here).
  escapeHtml: s => String(s).replace(/[&<>"']/g, c => '&#' + c.charCodeAt(0) + ';'),
  location: { protocol: 'http:', host: new URL(base).host, href: base + '/' },
  Blob: class { constructor(parts, o) { this.text = parts.join(''); this.type = o && o.type; blobs.push(this); } },
  URL: Object.assign(function (u, b) { return new URL(u, b); }, { createObjectURL: b => 'blob:' + blobs.indexOf(b) }),
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p) };
    calls.push(rec);
    const res = await fetch(new URL(p, base), opts);
    rec.status = res.status;
    return res;
  },
};
window.dispatchEvent = window.dispatchEvent || (() => true);
const ctx = vm.createContext(sandbox);
for (const f of ['theme.js', 'api.js', 'ui.js', 'settings.js', 'theme-settings.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
const get = expr => vm.runInContext(expr, ctx);
const q = sel => document.querySelector(sel);
const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
const input = (el, v, type) => { el.value = String(v); el.dispatchEvent(new Event(type || 'input', { bubbles: true })); };
const rootProp = p => document.documentElement.style.getPropertyValue(p);
const theme = () => document.documentElement.getAttribute('data-theme');
const verdict = () => (q('[data-theme-verdict]') || { textContent: '' }).textContent.replace(/\s+/g, ' ');
const failList = () => (q('[data-theme-failures]') ? q('[data-theme-failures]').querySelectorAll('li').map(li => li.textContent) : []);
const posts = () => calls.filter(c => c.method === 'POST' && c.path === '/api/themes');
const sect = async (name, fn) => { try { await fn(); } catch (e) { check(false, name + ': ' + (e && e.stack || e)); } };

(async () => {
  get('SettingsScreen').init();
  get('ThemeSettings').init();
  await until('the theme list', () => calls.some(c => c.path === '/api/themes' && c.status === 200));
  await sleep(50);

  await sect('new theme from Dark', async () => {
    click(q('[data-theme-new]'));
    await until('the editor', () => q('[data-theme-editor]') && !q('[data-theme-editor]').hidden && q('#themeName'));
    check(q('#themeBase').value === 'dark', 'a new theme starts from this browser\'s theme (Dark)');
    const rows = document.querySelectorAll('[data-role-hex]');
    check(rows.length >= 60, 'the editor lists every colour role (' + rows.length + ')');
    const ed = q('[data-theme-editor]').textContent;
    check(!/font|radius|border width|glyph/i.test(q('[data-theme-editor]').querySelectorAll('label').map(l => l.textContent).join(' ')), 'no size, border width, glyph or font fields');
    check(q('#themePreview') && q('#themePreview').value === '0.12', 'the bounded live-preview intensity, default 0.12', q('#themePreview') && q('#themePreview').value);
    check(/physical review/.test(ed) && !/certif(ied|ies) (for|as)/i.test(ed.replace(/do not certify/i, '')), 'the editor says the checks do not certify a screen and need physical review');
    check(q('[data-theme-save]').disabled && /Name the theme/.test(failList().join(' ')), 'Save is blocked until the theme has a name');
    input(q('#themeName'), 'Warm FOH');
    check(!q('[data-theme-save]').disabled && /Every pair passes/.test(verdict()), 'named, the unchanged Dark copy passes and can be saved', verdict());
  });

  await sect('a failing pair', async () => {
    input(q('[data-role-hex="text-muted"]'), '#7a7062');
    const list = failList();
    check(list.some(m => /^Muted text on overlay is \d\.\d:1; needs 4\.5:1\.$/.test(m)), 'the failed pair is listed in words: "Muted text on overlay is n:1; needs 4.5:1."', list.slice(0, 3).join(' | '));
    check(/Theme needs changes/.test(verdict()), 'the draft is labelled "Theme needs changes"', verdict());
    check(q('[data-theme-save]').disabled, 'Save is disabled');
    check(rootProp('--b5-color-text-muted') === '#7a7062', 'the failing draft previews on this page', rootProp('--b5-color-text-muted'));
    check(window.localStorage.getItem('b5-theme') !== 'custom' && (window.localStorage.getItem('b5-theme') || 'dark') === 'dark', 'the preview is not stored as this browser\'s theme');
    check(/in \d+ failed checks?/.test(q('[data-role-fail="text-muted"]').textContent), 'the role\'s own row says it is in failed checks', q('[data-role-fail="text-muted"]').textContent);
    const n = posts().length;
    click(q('[data-theme-save]'));
    await sleep(80);
    check(posts().length === n, 'pressing Save anyway sends nothing');
    const box = q('[data-theme-verdict]');
    check(box.style.getPropertyValue('--b5-color-text-primary') !== '' && box.style.getPropertyValue('--b5-color-text-muted') === '#b2a99b',
      'the verdict box (with Revert) keeps this browser\'s own colours', box.style.getPropertyValue('--b5-color-text-muted'));
    input(q('#themePreview'), '0.2');
    check(failList().some(m => /Live-preview intensity must be more than 0 and at most 0\.15\./.test(m)), 'a dark preview intensity above 0.15 is refused in words');
    input(q('[data-role-hex="bg-canvas"]'), '#2a2a2a');
    check(failList().some(m => /^Canvas is too bright for a dark theme/.test(m)), 'a canvas too bright for a dark venue is refused in words');
    input(q('[data-role-hex="armed-border"]'), q('[data-role-hex="danger-border"]').value);
    check(failList().some(m => /Armed outline and danger outline are the same colour/.test(m)), 'equal tone outlines are refused in words');
  });

  await sect('Revert', async () => {
    click(q('[data-theme-revert]'));
    check(rootProp('--b5-color-text-muted') === '' && theme() === 'dark', 'Revert ends the preview: this browser\'s Dark again', rootProp('--b5-color-text-muted'));
    check(q('[data-role-hex="text-muted"]').value === '#b2a99b' && q('#themePreview').value === '0.12', 'and puts the draft back as it opened');
    check(/Every pair passes/.test(verdict()), 'which passes', verdict());
  });

  // C9c (owner, 2026-10-09 23:12): Daylight previews at full brightness.
  await sect('Daylight preview intensity', async () => {
    get('B5Theme').choose('light');
    click(q('[data-theme-new]'));
    await until('the editor', () => q('#themeBase') && q('#themeBase').value === 'light');
    check(q('#themePreview').value === '1' && q('#themePreview').max === '1', 'a new theme on Daylight starts at full preview brightness (1), allowed up to 1', q('#themePreview').value + ' max ' + q('#themePreview').max);
    input(q('#themeName'), 'Sun');
    check(!failList().length && !q('[data-theme-save]').disabled, 'and passes', failList().join(' | '));
    check(/Default 1\./.test(q('[data-theme-preview-hint]').textContent), 'the hint gives Daylight\'s default 1', q('[data-theme-preview-hint]').textContent);
    input(q('#themeBase'), 'dark', 'change');
    check(q('#themePreview').value === '0.12' && q('#themePreview').max === '0.15', 'switching the base to Dark goes back to 0.12, at most 0.15', q('#themePreview').value + ' max ' + q('#themePreview').max);
    input(q('#themeBase'), 'light', 'change');
    check(q('#themePreview').value === '1', 'and back to Daylight, 1 again', q('#themePreview').value);
    get('B5Theme').choose('dark');
    click(q('[data-theme-new]'));
    await until('a Dark draft', () => q('#themeBase').value === 'dark');
    check(q('#themePreview').value === '0.12', 'a new theme on Dark still starts at 0.12');
  });

  await sect('leaving Settings ends the preview', async () => {
    input(q('[data-role="accent-border"]'), '#86d6d0');
    check(rootProp('--b5-color-accent-border') === '#86d6d0', 'a colour picked with the colour well previews');
    get('ThemeSettings').onLeaveScreen();
    check(rootProp('--b5-color-accent-border') === '', 'leaving Settings ends it');
    const app = fs.readFileSync(path.join(jsDir, 'app.js'), 'utf8');
    check(/currentTab === 'settings' && name !== 'settings'[^\n]*\n\s*ThemeSettings\.onLeaveScreen\(\)/.test(app), 'app.js calls it when the Settings tab is left');
  });

  let savedId = '';
  await sect('Save', async () => {
    input(q('#themeName'), 'Warm FOH');
    click(q('[data-theme-save]'));
    await until('the save', () => posts().some(c => c.status === 200));
    const list = (await server('GET', '/api/themes')).body.themes;
    check(list.length === 1 && list[0].name === 'Warm FOH' && list[0].colors['accent-border'] === '#86d6d0', 'the theme is saved on the station', JSON.stringify(list.map(t => t.name)));
    savedId = list[0] ? list[0].id : '';
    await until('the list to refresh', () => q('#themeSelect').querySelectorAll('option').length === 3);
    check(window.localStorage.getItem('b5-theme') === 'custom:' + savedId && JSON.parse(window.localStorage.getItem('b5-theme-custom')).id === savedId, 'this browser now uses it (stored with its copy for the next first paint)');
    check(rootProp('--b5-color-accent-border') === '#86d6d0' && theme() === 'dark', 'and shows it');
    check(q('#themeSelect').value === 'custom:' + savedId, 'the choice shows it', q('#themeSelect').value);
    check(q('[data-theme-editor]').hidden, 'the editor closes');
    const opt = q('#themeSelect').querySelectorAll('option').find(o => o.getAttribute('value') === 'custom:' + savedId);
    check(opt && /Warm FOH/.test(opt.textContent) && /custom, on Dark/.test(opt.textContent), 'listed as "Warm FOH — custom, on Dark"', opt && opt.textContent);
  });

  await sect('rename through the editor', async () => {
    click(q('[data-theme-edit]'));
    await until('the editor', () => !q('[data-theme-editor]').hidden && q('#themeName'));
    check(q('#themeName').value === 'Warm FOH' && q('[data-role-hex="accent-border"]').value === '#86d6d0', 'Edit opens the saved theme');
    input(q('#themeName'), 'Front of house');
    click(q('[data-theme-save]'));
    await until('the rename', async () => (await server('GET', '/api/themes')).body.themes[0].name === 'Front of house');
    const list = (await server('GET', '/api/themes')).body.themes;
    check(list.length === 1 && list[0].id === savedId, 'renamed in place, not copied');
  });

  await sect('export / import', async () => {
    await until('the list', () => q('#themeSelect').value === 'custom:' + savedId);
    click(q('[data-theme-export]'));
    const exported = blobs.length ? JSON.parse(blobs[blobs.length - 1].text) : null;
    check(exported && exported.benny512Theme === 1 && exported.name === 'Front of house' && exported.colors['accent-border'] === '#86d6d0' && exported.preview === 0.12, 'Export writes a small JSON file of the theme');
    const file = { text: async () => JSON.stringify(Object.assign({}, exported, { name: 'Imported', colors: Object.assign({}, exported.colors, { 'text-muted': '#7a7062' }) })) };
    await get('ThemeSettings').importFile(file);
    check(!q('[data-theme-editor]').hidden && q('#themeName').value === 'Imported', 'Import opens it in the editor as a new draft');
    check(/Theme needs changes/.test(verdict()) && q('[data-theme-save]').disabled, 'an imported theme that fails is checked like any draft');
    click(q('[data-theme-close]'));
    check(q('[data-theme-editor]').hidden && rootProp('--b5-color-text-muted') === '#b2a99b' && rootProp('--b5-color-accent-border') === '#86d6d0', 'Close ends its preview: this browser\'s saved theme again', [q('[data-theme-editor]').hidden, rootProp('--b5-color-text-muted'), rootProp('--b5-color-accent-border')].join(' '));
  });

  await sect('Delete asks, non-modally', async () => {
    click(q('[data-theme-delete]'));
    await until('the question', () => q('[data-ask]'));
    const dlg = q('[data-ask]');
    check(dlg && /Delete the theme “Front of house”\?/.test(dlg.textContent), 'Delete asks first, naming the theme', dlg && dlg.textContent);
    check(dlg && dlg.open && !dlg.modal, 'in the non-modal dialog (DISARM stays reachable)');
    check(document.activeElement === q('[data-ask-cancel]'), 'Cancel is focused first');
    click(q('[data-ask-ok]'));
    await until('the delete', () => calls.some(c => c.method === 'DELETE' && c.status === 200));
    await until('Dark again', () => theme() === 'dark' && rootProp('--b5-color-accent-border') === '');
    check(window.localStorage.getItem('b5-theme') === 'dark', 'this browser, which used it, is back on Dark');
    check((await server('GET', '/api/themes')).body.themes.length === 0, 'the station no longer has it');
  });

  await sect('deleted in another browser', async () => {
    const dark = (await server('GET', '/api/themes')).body.bases.dark;
    const r = await server('POST', '/api/themes', { name: 'Elsewhere', base: 'dark', colors: dark, preview: 0.12 });
    await get('ThemeSettings').refresh();
    input(q('#themeSelect'), 'custom:' + r.body.id, 'change');
    check(window.localStorage.getItem('b5-theme') === 'custom:' + r.body.id, 'chosen from the list in this browser');
    await server('DELETE', '/api/themes/' + r.body.id);
    await get('ThemeSettings').refresh();
    check(theme() === 'dark' && window.localStorage.getItem('b5-theme') === 'dark', 'after another browser deletes it, this browser goes back to Dark');
    check(/deleted on this station; this browser is back on Dark/.test(q('#themeStatus').textContent), 'and says so in words', q('#themeStatus').textContent);
  });

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})();
