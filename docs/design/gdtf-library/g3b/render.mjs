// G3b render-proofs: the real GDTF library screens on a disposable --demo
// build, in Chromium. Design/verification tool only (never a production test).
//
//   node docs/design/gdtf-library/g3b/render.mjs <harness.test> <render-data dir> <out dir> <part>
//
// <harness.test>: `go test -c -o harness.test ./cmd/benny512` (its
// TestRenderHarness serves buildDemo with a GDTF library folder; see that
// file). <render-data dir>: written by
// `BENNY512_RENDER_GDTF_OUT=<dir> go test -run TestWriteRenderLibrary ./internal/gdtfshare`
// (real syncs against the fake GDTF Share). <part>: settings | library | devices.
// Each run copies the state folder into a fresh temp dir and starts the
// harness with HTTPS_PROXY/HTTP_PROXY on a closed port, so any conversation
// with GDTF Share fails as offline and nothing reaches gdtf-share.com.
//
// Checks per run: no console errors, no page-level horizontal scroll, the
// scene's words present, and WCAG contrast of every visible text in the
// screen part under test (rendered colours; 4.5:1, 3:1 for large text;
// aria-disabled/disabled controls counted apart, as G3a did). Keyboard: Tab
// through the part; every stop shows a focus indicator.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn, execSync } from 'node:child_process';
import { createRequire } from 'node:module';

const [harness, dataDir, outDir, part] = process.argv.slice(2);
if (!part) { console.error('usage: render.mjs <harness.test> <render-data dir> <out dir> settings|library|devices'); process.exit(2); }
const root = process.env.PLAYWRIGHT_DIR || execSync('npm root -g').toString().trim();
const { chromium } = createRequire(path.join(root, '/'))('playwright');
fs.mkdirSync(outDir, { recursive: true });
const SIZES = [[1024, 768], [1440, 900], [1920, 1080]];
const THEMES = ['dark', 'light'];
let port = 5890;

async function startHarness(state) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'g3b-'));
  const gdtf = path.join(tmp, 'GDTF');
  fs.cpSync(path.join(dataDir, state), gdtf, { recursive: true });
  const addr = `127.0.0.1:${++port}`;
  const env = { ...process.env, HTTPS_PROXY: 'http://127.0.0.1:9', HTTP_PROXY: 'http://127.0.0.1:9', NO_PROXY: '',
    BENNY512_RENDER_GDTF: gdtf, BENNY512_RENDER_DIR: tmp, BENNY512_RENDER_ADDR: addr };
  const p = spawn(harness, ['-test.run', 'TestRenderHarness', '-test.timeout', '0'], { cwd: tmp, env, stdio: ['ignore', 'pipe', 'pipe'] });
  let log = ''; p.stdout.on('data', d => { log += d; }); p.stderr.on('data', d => { log += d; });
  for (let i = 0; i < 100; i++) {
    try { const r = await fetch(`http://${addr}/api/version`); if (r.ok) break; } catch (e) { /* not yet */ }
    await new Promise(r => setTimeout(r, 100));
  }
  return { url: `http://${addr}`, stop: () => { p.kill('SIGKILL'); }, log: () => log, tmp };
}

async function post(url, body, method) {
  const r = await fetch(url, { method: method || 'POST', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
  return { status: r.status, body: await r.text() };
}

// In the page: contrast of every visible text inside the given roots.
function measure(sels) {
  const parse = c => { const m = c.match(/rgba?\(([^)]+)\)/); if (!m) return null; const p = m[1].split(/[ ,/]+/).filter(Boolean).map(Number); return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }; };
  const over = (f, b) => ({ r: f.r * f.a + b.r * (1 - f.a), g: f.g * f.a + b.g * (1 - f.a), b: f.b * f.a + b.b * (1 - f.a), a: 1 });
  const lum = c => [c.r, c.g, c.b].map(v => v / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const hex = c => '#' + [c.r, c.g, c.b].map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
  const canvas = parse(getComputedStyle(document.body).backgroundColor);
  const visible = el => { const r = el.getBoundingClientRect(); if (r.width < 1 || r.height < 1) return false; const cs = getComputedStyle(el); return cs.visibility !== 'hidden' && cs.display !== 'none'; };
  function backdrop(el) {
    const layers = [];
    for (let e = el; e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c && c.a > 0) { layers.push(c); if (c.a >= 1) break; } }
    let bg = layers.length && layers[layers.length - 1].a >= 1 ? layers.pop() : (canvas && canvas.a ? canvas : { r: 0, g: 0, b: 0, a: 1 });
    for (let i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
    return bg;
  }
  const opacityOf = el => { let o = 1; for (let e = el; e; e = e.parentElement) o *= Number(getComputedStyle(e).opacity); return o; };
  const out = [];
  for (const s of sels) for (const rootEl of document.querySelectorAll(s)) {
    for (const el of [rootEl, ...rootEl.querySelectorAll('*')]) {
      if (!visible(el)) continue;
      if (![...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim()) || el.closest('[aria-hidden="true"]')) continue;
      const cs = getComputedStyle(el); const bg = backdrop(el);
      let fg = parse(cs.color); fg = over({ ...fg, a: fg.a * opacityOf(el) }, bg);
      const size = parseFloat(cs.fontSize), bold = Number(cs.fontWeight) >= 700;
      const min = size >= 24 || (size >= 18.66 && bold) ? 3 : 4.5;
      out.push({ fg: hex(fg), bg: hex(bg), ratio: Math.round(ratio(fg, bg) * 100) / 100, min,
        disabled: !!el.closest('button:disabled,[aria-disabled="true"],input:disabled,select:disabled'),
        sel: el.tagName.toLowerCase() + (el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\s+/)[0] : ''), text: el.textContent.trim().slice(0, 40) });
    }
  }
  return out;
}

const results = { part, runs: [], focus: [], actions: [], pairs: {} };
let failures = 0;
const browser = await chromium.launch();

async function openPage(url, w, h, theme, tab, routes) {
  const ctx = await browser.newContext({ viewport: { width: w, height: h } });
  await ctx.addInitScript(([t, tab]) => { localStorage.setItem('b5-theme', t); localStorage.setItem('benny512.tab', tab); }, [theme, tab]);
  const page = await ctx.newPage();
  const errors = [];
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', e => errors.push(e.message));
  if (routes) await routes(page);
  await page.goto(url);
  await page.waitForTimeout(700);
  return { page, ctx, errors };
}

function record(name, w, h, theme, extra, m, errors, ok) {
  const fails = m.filter(t => !t.disabled && t.ratio < t.min);
  for (const t of m) {
    const k = `${theme}|${t.fg}|${t.bg}|${t.min}|${t.disabled ? 'd' : ''}`;
    const p = results.pairs[k] || { theme, fg: t.fg, bg: t.bg, ratio: t.ratio, min: t.min, disabled: t.disabled, count: 0, example: `${t.sel} “${t.text}”` };
    p.count++; results.pairs[k] = p;
  }
  const good = ok && fails.length === 0 && errors.length === 0 && extra.hscroll <= 0;
  if (!good) failures++;
  results.runs.push({ name, size: `${w}x${h}`, theme, ok: good, ...extra, consoleErrors: errors.splice(0), measured: m.length, textFails: fails.slice(0, 6) });
}

// tabWalk: focus the first focusable control in startSel, then Tab on
// while focus stays inside scopeSel; every stop must show a focus ring.
async function tabWalk(page, startSel, scopeSel, max) {
  await page.evaluate(sel => {
    const el = document.querySelector(sel).querySelector('button, input, select, a[href], [tabindex]:not([tabindex="-1"]), summary');
    if (el) el.focus();
  }, startSel);
  await page.keyboard.press('Shift+Tab');
  const stops = [];
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab');
    const s = await page.evaluate(scope => {
      const el = document.activeElement; if (!el || el === document.body) return null;
      const cs = getComputedStyle(el); const r = el.getBoundingClientRect();
      const ring = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || (cs.boxShadow && cs.boxShadow !== 'none');
      return { inScope: !!el.closest(scope), label: (el.getAttribute('aria-label') || el.textContent || el.id || '').trim().replace(/\s+/g, ' ').slice(0, 40), ring, size: [Math.round(r.width), Math.round(r.height)] };
    }, scopeSel);
    if (!s || !s.inScope) break;
    stops.push(s);
  }
  return stops;
}

// ---------------------------------------------------------------------------
if (part === 'settings') {
  const SCENES = [
    // name, state folder, save a login?, page routes (only for what the demo cannot produce), words to find
    { name: 'not-set-up', state: 'empty', login: false, words: ['Not set up', 'No GDTF Share login saved', 'Which network sync uses', 'Save login', 'Powered by GDTF'] },
    { name: 'ok', state: 'ok', login: true, words: ['Up to date', 'Library up to date', 'user dfournier', 'Manufacturers you follow (14)', 'Not found on GDTF Share', 'Follow “Martin Professional” instead', 'GDTF Share has no similar name either.'] },
    { name: 'partial', state: 'partial', login: true, words: ['Partly synced', 'Waiting to download (1)', 'answered HTTP 503', 'Files that could not be read (1)', 'JDC2', 'User file'] },
    { name: 'offline', state: 'offline', login: true, words: ['Offline', 'offline · last synced', 'used as they are'] },
    { name: 'refused', state: 'refused', login: true, words: ['Sync refused', 'Sync refused: the only route to the internet is the lighting network adapter', 'Go to Art-Net network adapter', 'no adapter is bound in this mode'] },
    // The demo binds no lighting adapter and cannot be mid-sync on demand,
    // so these two rewrite the REAL status answer (only the named fields).
    { name: 'refused-auto-adapter (status rewritten)', state: 'refused', login: true,
      rewrite: s => { s.lightingAdapters = [{ protocol: 'Art-Net', bound: true, name: 'Ethernet', addresses: ['192.168.1.40/24'], auto: true, sameAsArtNet: false }, { protocol: 'sACN', bound: true, name: 'Ethernet', addresses: ['192.168.1.40/24'], auto: false, sameAsArtNet: true }]; },
      words: ['picked automatically', 'Ethernet (192.168.1.40)', 'instead of “(default / any)”'] },
    { name: 'syncing (status rewritten)', state: 'ok', login: true,
      rewrite: s => { s.running = true; s.progress = { done: 7, total: 15, manufacturer: 'Robe', manufacturerIndex: 9, manufacturerCount: 12 }; },
      words: ['Syncing', 'File 8 of 15 · now Robe (manufacturer 9 of 12)', 'Syncing now', 'A sync is running.'] },
  ];
  for (const sc of SCENES) {
    const h = await startHarness(sc.state);
    if (sc.login) await post(h.url + '/api/gdtf/login', { user: 'dfournier', password: 'render-only-not-a-real-password' });
    const routes = sc.rewrite ? async page => {
      await page.route('**/api/gdtf/status', async route => {
        const resp = await route.fetch(); const s = await resp.json(); sc.rewrite(s);
        await route.fulfill({ response: resp, body: JSON.stringify(s) });
      });
    } : null;
    for (const [w, hh] of SIZES) for (const theme of THEMES) {
      const { page, ctx, errors } = await openPage(h.url, w, hh, theme, 'settings', routes);
      await page.evaluate(() => { document.getElementById('setGdtfHead').scrollIntoView({ block: 'start' }); window.scrollBy(0, -140); });
      await page.waitForTimeout(150);
      const extra = await page.evaluate(words => ({
        hscroll: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        missing: words.filter(x => !(document.getElementById('gdtfSection').innerText + ' ' + document.getElementById('aboutSection').innerText).toLowerCase().includes(x.toLowerCase())),
        sections: [...document.querySelectorAll('#screen-settings .b5-step-section__head')].map(e => e.innerText.split('\n')[0].replace(/\s+/g, ' ').trim()),
        password: document.documentElement.innerHTML.includes('render-only-not-a-real-password'),
      }), sc.words);
      const m = await page.evaluate(measure, ['#gdtfSection', '#aboutSection']);
      record(sc.name, w, hh, theme, extra, m, errors, extra.missing.length === 0 && !extra.password);
      if (w === 1440 && theme === 'dark' || (w === 1024 && sc.name === 'refused-auto-adapter (status rewritten)')) {
        await page.screenshot({ path: path.join(outDir, `settings-${w}-${theme}-${sc.name.split(' ')[0]}.png`) });
      }
      if (w === 1440 && theme === 'dark' && sc.name === 'ok') {
        await page.evaluate(() => { document.getElementById('setAboutHead').scrollIntoView({ block: 'start' }); window.scrollBy(0, -400); });
        await page.waitForTimeout(100);
        await page.screenshot({ path: path.join(outDir, `settings-1440-dark-about.png`) });
      }
      await ctx.close();
    }
    // Keyboard and real actions, once per scene at 1440 dark.
    const { page, ctx, errors } = await openPage(h.url, 1440, 900, 'dark', 'settings', routes);
    const stops = await tabWalk(page, '#gdtfSection', '#gdtfSection, #aboutSection', 80).catch(e => { results.focus.push({ error: e.message }); return []; });
    const noRing = stops.filter(s => !s.ring);
    results.focus.push({ scene: sc.name, stops: stops.length, noRing, first: stops.slice(0, 8).map(s => s.label) });
    if (noRing.length || !stops.length) failures++;
    if (sc.name === 'refused') {
      await page.click('[data-gdtf-goto-nic]');
      const f = await page.evaluate(() => document.activeElement && document.activeElement.id);
      results.actions.push({ scene: sc.name, action: 'Go to Art-Net network adapter', focused: f, ok: f === 'nicSelect' });
      if (f !== 'nicSelect') failures++;
    }
    if (sc.name === 'ok') {
      // Real: Sync now -> POST /api/gdtf/sync -> the sync runs, cannot reach
      // GDTF Share (proxy on a closed port) and the screen says offline.
      await page.click('[data-gdtf-sync]');
      let seen = '';
      for (let i = 0; i < 40; i++) { await page.waitForTimeout(250); seen = await page.evaluate(() => document.querySelector('[data-gdtf-state]').dataset.gdtfState); if (seen === 'offline') break; }
      const words = await page.evaluate(() => document.querySelector('.b5-gdtf-status').innerText.replace(/\s+/g, ' '));
      results.actions.push({ scene: sc.name, action: 'Sync now (real; GDTF Share unreachable on purpose)', state: seen, words, ok: seen === 'offline' && /offline · last synced/.test(words) });
      if (seen !== 'offline') failures++;
      // Real: Stop following + Follow "X" instead act at once.
      await page.click('[data-gdtf-instead="Martin"]');
      await page.waitForTimeout(500);
      const followed = JSON.parse((await post(h.url + '/api/gdtf/status', undefined, 'GET')).body).followed;
      results.actions.push({ scene: sc.name, action: 'Follow “Martin Professional” instead', followed, ok: followed.includes('Martin Professional') && !followed.includes('Martin') });
      if (!followed.includes('Martin Professional')) failures++;
      await page.click('[data-gdtf-unfollow="ADJ"]');
      await page.waitForTimeout(500);
      const after = JSON.parse((await post(h.url + '/api/gdtf/status', undefined, 'GET')).body);
      const kept = after.files;
      results.actions.push({ scene: sc.name, action: 'Stop following ADJ', followed: after.followed.length, filesStill: kept, ok: !after.followed.includes('ADJ') && kept === 18 });
      if (after.followed.includes('ADJ') || kept !== 18) failures++;
      await page.screenshot({ path: path.join(outDir, 'settings-1440-dark-after-actions.png') });
    }
    if (sc.name === 'not-set-up') {
      await page.fill('#gdtfUser', 'dfournier');
      await page.fill('#gdtfPass', 'render-only-not-a-real-password');
      await page.click('[data-gdtf-save]');
      let seen = '';
      for (let i = 0; i < 40; i++) { await page.waitForTimeout(250); seen = await page.evaluate(() => document.querySelector('[data-gdtf-state]').dataset.gdtfState); if (seen === 'offline') break; }
      const t = await page.evaluate(() => document.getElementById('gdtfShareRoot').innerText.replace(/\s+/g, ' '));
      const leaked = await page.evaluate(() => document.documentElement.innerHTML.includes('render-only-not-a-real-password') || [...document.querySelectorAll('input')].some(i => i.value === 'render-only-not-a-real-password'));
      results.actions.push({ scene: sc.name, action: 'Save login (real) starts the first sync', state: seen, saved: /Saved/.test(t), leaked, ok: seen === 'offline' && /Saved/.test(t) && !leaked });
      if (seen !== 'offline' || leaked) failures++;
      await page.screenshot({ path: path.join(outDir, 'settings-1440-dark-after-save-login.png') });
    }
    if (errors.length) { failures++; results.actions.push({ scene: sc.name, consoleErrors: errors }); }
    await ctx.close();
    h.stop();
  }
}

// ---------------------------------------------------------------------------
if (part === 'library') {
  const h = await startHarness('ok');
  await post(h.url + '/api/gdtf/login', { user: 'dfournier', password: 'render-only-not-a-real-password' });
  async function openLibrary(page) {
    await page.click('[data-showmenu], button:has-text("Show")');
    await page.click('[data-library]');
    await page.waitForSelector('dialog[open] [data-gdtf-add-toggle]');
    await page.click('[data-gdtf-add-toggle]');
    await page.waitForSelector('[data-gdtf-add] [data-gdtf-count]');
    await page.waitForTimeout(300);
  }
  async function searchFor(page, q) {
    await page.fill('#gdtfQuery', q);
    await page.waitForTimeout(600);
  }
  const SCENES = [
    { name: 'local-search', source: 'library', q: 'wash', words: ['Add from GDTF library', 'On this computer (18 files)', 'Works offline', 'Manufacturer file', 'User file', 'Long name: Robe Wash LED', 'Modes: Mode 1 · 20 ch; Mode 2 · 26 ch', '2 files match “wash”'] },
    { name: 'over-20MB', source: 'library', q: 'mac ultra', words: ['Too large for the Fixture Library: 21.0 MB', 'takes GDTF files up to 20 MB'] },
    { name: 'share-search', source: 'share', q: 'sharpy', words: ['GDTF Share (online)', 'Not downloaded', 'Clay Paky is not followed: this downloads this one file only.', 'Download and import…', 'Long name: known once downloaded'] },
  ];
  for (const sc of SCENES) for (const [w, hh] of SIZES) for (const theme of THEMES) {
    const { page, ctx, errors } = await openPage(h.url, w, hh, theme, 'devices');
    await openLibrary(page);
    if (sc.source === 'share') { await page.click('[data-gdtf-source="share"]'); await page.waitForTimeout(300); }
    await searchFor(page, sc.q);
    await page.evaluate(() => document.getElementById('gdtfAddHead').scrollIntoView({ block: 'start' }));
    const extra = await page.evaluate(words => ({
      hscroll: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      dialogHscroll: (d => d.scrollWidth - d.clientWidth)(document.querySelector('dialog[open]')),
      missing: words.filter(x => !document.querySelector('dialog[open]').innerText.includes(x)),
    }), sc.words);
    const m = await page.evaluate(measure, ['dialog[open] [data-gdtf-add]', 'dialog[open] [data-library-import]', 'dialog[open] [data-library-status]']);
    record(sc.name, w, hh, theme, extra, m, errors, extra.missing.length === 0 && extra.dialogHscroll <= 0);
    if (w === 1440 && theme === 'dark' || w === 1024 && theme === 'light') await page.screenshot({ path: path.join(outDir, `library-${w}-${theme}-${sc.name}.png`) });
    await ctx.close();
  }
  // Keyboard + the real import path, at 1440 dark.
  const { page, ctx, errors } = await openPage(h.url, 1440, 900, 'dark', 'devices');
  await openLibrary(page);
  await searchFor(page, 'wash');
  const stops = await tabWalk(page, '[data-gdtf-add]', '[data-gdtf-add]', 40);
  results.focus.push({ scene: 'add-from-gdtf', stops: stops.length, noRing: stops.filter(s => !s.ring), first: stops.map(s => s.label) });
  if (!stops.length || stops.some(s => !s.ring)) failures++;
  await page.click('[data-gdtf-rid="1001"] [data-gdtf-import]');
  await page.waitForSelector('[data-apply-library]', { timeout: 5000 }).catch(() => {});
  const confirm = await page.evaluate(() => ({ status: document.querySelector('[data-library-status]').innerText, area: document.querySelector('[data-library-import]').innerText.replace(/\s+/g, ' ') }));
  await page.screenshot({ path: path.join(outDir, 'library-1440-dark-confirm.png') });
  const okConfirm = /Review the import, then apply/.test(confirm.status) && /From the GDTF library · Manufacturer file/.test(confirm.area) && /Robe@Wash@DMX v1\.3 \(fw 1\.0\)\.gdtf/.test(confirm.area);
  results.actions.push({ action: 'Import… (real bytes -> real parser -> existing confirm step)', ...confirm, ok: okConfirm });
  if (!okConfirm) failures++;
  await page.click('[data-apply-library]');
  await page.waitForTimeout(800);
  const lib = JSON.parse((await post(h.url + '/api/library', undefined, 'GET')).body);
  const rec = lib.records.find(r => r.model === 'Wash');
  const label = rec && rec.sourceFiles[0] && rec.sourceFiles[0].uploaderLabel;
  const modes = rec ? rec.modes.map(m => `${m.name}:${m.footprint}`) : [];
  await page.click('[data-gdtf-add-toggle]').catch(() => {});
  if (!(await page.isVisible('[data-gdtf-add] #gdtfQuery'))) await page.click('[data-gdtf-add-toggle]');
  await searchFor(page, 'wash');
  const tagged = await page.evaluate(() => document.querySelector('[data-gdtf-rid="1001"]').innerText.includes('In your Fixture library'));
  const okImport = label === 'Manufacturer' && modes.join() === 'Mode 1:20,Mode 2:26' && tagged;
  results.actions.push({ action: 'Apply import (real POST /api/library/import)', label, modes, inFixtureLibraryAfter: tagged, ok: okImport });
  if (!okImport) failures++;
  await page.screenshot({ path: path.join(outDir, 'library-1440-dark-after-import.png') });
  // Real single pull: GDTF Share cannot be reached on purpose -> honest refusal.
  await page.click('[data-gdtf-source="share"]'); await page.waitForTimeout(300);
  await searchFor(page, 'sharpy');
  await page.click('[data-gdtf-rid="1601"] [data-gdtf-import]');
  await page.waitForTimeout(1500);
  const pullMsg = await page.evaluate(() => document.querySelector('[data-library-status]').innerText);
  const okPull = /Not imported: Sharpy: .*GDTF Share could not be reached/.test(pullMsg) && !(await page.isVisible('[data-apply-library]'));
  results.actions.push({ action: 'Download and import (real pull, offline on purpose)', status: pullMsg, ok: okPull });
  if (!okPull) failures++;
  await page.screenshot({ path: path.join(outDir, 'library-1440-dark-pull-offline.png') });
  // The deliberate offline pull answers 503, which Chromium logs as a failed
  // resource; that one is expected. Anything else is a failure.
  const unexpected = errors.filter(e => !/Failed to load resource: the server responded with a status of 503/.test(e));
  results.actions.push({ consoleErrors: errors, expected: 'one 503 from the deliberate offline pull', unexpected });
  if (unexpected.length || errors.length > 1) failures++;
  await ctx.close();
  h.stop();
}

await browser.close();
results.pairsList = Object.values(results.pairs).sort((a, b) => a.ratio - b.ratio);
delete results.pairs;
results.summary = { runs: results.runs.length, failedRuns: results.runs.filter(r => !r.ok).length, failures,
  pairs: results.pairsList.length, pairFails: results.pairsList.filter(p => !p.disabled && p.ratio < p.min).length,
  lowestPair: results.pairsList.find(p => !p.disabled) };
fs.writeFileSync(path.join(outDir, `checks-${part}.json`), JSON.stringify(results, null, 1));
console.log(JSON.stringify(results.summary, null, 1));
for (const r of results.runs.filter(r => !r.ok)) console.log('FAILED RUN', JSON.stringify(r).slice(0, 600));
for (const f of results.focus.filter(f => f.noRing.length || !f.stops)) console.log('FOCUS', JSON.stringify(f).slice(0, 400));
for (const a of results.actions.filter(a => a.ok === false || a.consoleErrors)) console.log('ACTION', JSON.stringify(a).slice(0, 400));
process.exit(failures ? 1 : 0);
