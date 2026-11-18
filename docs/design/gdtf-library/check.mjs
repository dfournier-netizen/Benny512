// G3a verification: renders every scene of the mockup in Chromium and checks
// what verification.md reports. Design tool only (never a production test).
//
//   node docs/design/gdtf-library/check.mjs mockup_2026-11-17_2309.html
//
// Needs Playwright (resolved from PLAYWRIGHT_DIR, else the global npm root).
// Writes evidence/checks.json and evidence/*.png (palette PNGs are made by
// shrink-evidence.py afterwards).
//
// Contrast method: WCAG 2.x relative luminance on the RENDERED colours —
// each visible text element's computed colour against the first opaque
// background behind it (semi-transparent layers composited), with ancestor
// opacity applied. 4.5:1 for normal text, 3:1 for large text (>= 24 px, or
// >= 18.66 px bold) and for the boundaries of new mockup parts. Disabled
// controls (WCAG-exempt) are counted separately, not passed or failed;
// aria-hidden decorative text is not measured.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { execSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const dir = path.dirname(fileURLToPath(import.meta.url));
const file = process.argv[2];
if (!file) { console.error('usage: node check.mjs mockup_YYYY-MM-DD_HHMM.html'); process.exit(2); }
const root = process.env.PLAYWRIGHT_DIR || execSync('npm root -g').toString().trim();
const { chromium } = createRequire(path.join(root, '/'))('playwright');
const url = 'file://' + path.join(dir, file);
const ev = path.join(dir, 'evidence');
fs.mkdirSync(ev, { recursive: true });

const SIZES = [[1024, 768], [1440, 900], [1920, 1080]];
const THEMES = ['dark', 'light'];
const SHOTS = new Set(['1440-dark-s1', '1440-dark-s5', '1440-dark-s7', '1440-dark-s9', '1440-dark-s11', '1440-dark-s13', '1440-dark-s16', '1024-dark-s3', '1024-light-s5', '1920-dark-s12']);

// Runs in the page: every measured text and boundary pair for the current scene.
function measure() {
  const parse = c => { const m = c.match(/rgba?\(([^)]+)\)/); if (!m) return null; const p = m[1].split(/[ ,/]+/).filter(Boolean).map(Number); return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }; };
  const over = (f, b) => ({ r: f.r * f.a + b.r * (1 - f.a), g: f.g * f.a + b.g * (1 - f.a), b: f.b * f.a + b.b * (1 - f.a), a: 1 });
  const lum = c => [c.r, c.g, c.b].map(v => v / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4).reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const hex = c => '#' + [c.r, c.g, c.b].map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
  const visible = el => { const r = el.getBoundingClientRect(); if (r.width < 1 || r.height < 1) return false; const cs = getComputedStyle(el); return cs.visibility !== 'hidden' && cs.display !== 'none'; };
  const canvas = parse(getComputedStyle(document.body).backgroundColor);
  function backdrop(el) {
    const layers = []; let images = false;
    for (let e = el; e; e = e.parentElement) {
      const cs = getComputedStyle(e);
      if (cs.backgroundImage && cs.backgroundImage !== 'none') images = true;
      const c = parse(cs.backgroundColor);
      if (c && c.a > 0) { layers.push(c); if (c.a >= 1) break; }
      if (e.tagName === 'DIALOG' || e.classList?.contains('b5-inspector')) { /* opaque panels */ }
    }
    let bg = layers.length && layers[layers.length - 1].a >= 1 ? layers.pop() : (canvas && canvas.a ? canvas : { r: 0, g: 0, b: 0, a: 1 });
    for (let i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
    return { bg, images };
  }
  const opacityOf = el => { let o = 1; for (let e = el; e; e = e.parentElement) o *= Number(getComputedStyle(e).opacity); return o; };
  const disabledOf = el => !!el.closest('button:disabled,[aria-disabled="true"],input:disabled,select:disabled');
  const sel = el => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + (el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.') : '');
  const roots = [document.querySelector('.b5-app'), document.getElementById('libDialog')].filter(Boolean);
  const text = [], edges = [];
  for (const rootEl of roots) {
    if (rootEl.tagName === 'DIALOG' && !rootEl.open) continue;
    for (const el of rootEl.querySelectorAll('*')) {
      if (!visible(el)) continue;
      const own = [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim());
      // aria-hidden text is decorative (production's "·" link badge); not measured.
      if (own && !el.closest('[aria-hidden="true"]')) {
        const cs = getComputedStyle(el);
        const { bg, images } = backdrop(el);
        let fg = parse(cs.color); const op = opacityOf(el);
        fg = over({ ...fg, a: fg.a * op }, bg);
        const size = parseFloat(cs.fontSize), bold = Number(cs.fontWeight) >= 700;
        const large = size >= 24 || (size >= 18.66 && bold);
        const r = ratio(fg, bg);
        text.push({ fg: hex(fg), bg: hex(bg), ratio: Math.round(r * 100) / 100, min: large ? 3 : 4.5, disabled: disabledOf(el), images, sel: sel(el), text: el.textContent.trim().slice(0, 50) });
      }
      // Boundaries of the new mockup parts (g3-*) that carry meaning.
      if ([...el.classList].some(c => ['g3-new', 'g3-logo-ph', 'g3-match', 'g3-status'].includes(c)) || (el.classList.contains('b5-progress') && el.closest('.g3-progress'))) {
        const cs = getComputedStyle(el); const { bg } = backdrop(el.parentElement);
        const b = parse(cs.borderTopColor);
        if (b && parseFloat(cs.borderTopWidth) > 0) { const c = over(b, bg); edges.push({ fg: hex(c), bg: hex(bg), ratio: Math.round(ratio(c, bg) * 100) / 100, min: 3, sel: sel(el) }); }
      }
    }
  }
  return { text, edges };
}

const results = { file, method: 'WCAG relative luminance on rendered colours; see the header of check.mjs', runs: [], focus: [], switching: null, contrast: {} };
const browser = await chromium.launch();
const pairs = new Map();
let failures = 0;
for (const [w, h] of SIZES) {
  for (const theme of THEMES) {
    const page = await browser.newPage({ viewport: { width: w, height: h } });
    const errors = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(url + '#s1');
    await page.selectOption('#g3Theme', theme);
    await page.waitForTimeout(600); // let production button transitions finish before the first measurement
    const ids = await page.$$eval('#g3Scene option', os => os.map(o => o.value));
    for (const id of ids) {
      await page.selectOption('#g3Scene', id); // switching through the real control
      await page.waitForTimeout(60);
      const r = await page.evaluate(() => ({
        scene: document.body.dataset.scene,
        hscroll: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        bodyOverflow: document.body.scrollWidth - document.documentElement.clientWidth,
        screenShown: !!document.querySelector('.screen.active') && document.querySelector('.screen.active').getBoundingClientRect().height > 0,
        dialogOpen: document.getElementById('libDialog').open,
        newNotes: document.querySelectorAll('.g3-new').length,
      }));
      const m = await page.evaluate(measure);
      const textFails = m.text.filter(t => !t.disabled && t.ratio < t.min);
      const edgeFails = m.edges.filter(t => t.ratio < t.min);
      for (const t of [...m.text, ...m.edges]) {
        const k = `${theme}|${t.fg}|${t.bg}|${t.min}|${t.disabled ? 'disabled' : ''}`;
        const p = pairs.get(k) || { theme, fg: t.fg, bg: t.bg, ratio: t.ratio, min: t.min, disabled: !!t.disabled, images: !!t.images, count: 0, example: t.sel + ' “' + (t.text || '') + '”' };
        p.count++; pairs.set(k, p);
      }
      const ok = r.scene === id && r.hscroll <= 0 && r.screenShown && errors.length === 0 && textFails.length === 0 && edgeFails.length === 0;
      if (!ok) failures++;
      results.runs.push({ size: `${w}x${h}`, theme, id, ok, ...r, consoleErrors: errors.splice(0), textMeasured: m.text.length, textFails: textFails.slice(0, 8), edgesMeasured: m.edges.length, edgeFails });
      const shot = `${w}-${theme}-${id}`;
      if (SHOTS.has(shot)) await page.screenshot({ path: path.join(ev, shot + '.png') });
    }
    await page.close();
  }
}

// Keyboard: Tab through four scenes; every stop must be visible, show a
// focus indicator, and the walk must reach the scene's own controls.
for (const id of ['s1', 's5', 's9', 's12', 's16']) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  await page.goto(url + '#' + id);
  await page.evaluate(() => document.activeElement && document.activeElement.blur());
  const stops = [];
  for (let i = 0; i < 160; i++) {
    await page.keyboard.press('Tab');
    const s = await page.evaluate(() => {
      const el = document.activeElement; if (!el || el === document.body) return null;
      const cs = getComputedStyle(el); const r = el.getBoundingClientRect();
      const ring = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || (cs.boxShadow && cs.boxShadow !== 'none');
      const where = el.closest('.g3-review') ? 'controls' : el.closest('dialog') ? 'dialog' : el.closest('.b5-inspector') ? 'inspector' : el.closest('.screen') ? 'screen' : 'chrome';
      return { tag: el.tagName.toLowerCase(), label: (el.getAttribute('aria-label') || el.textContent || el.id || '').trim().replace(/\s+/g, ' ').slice(0, 40), ring, inView: r.width > 0 && r.height > 0, where };
    });
    if (!s) continue;
    if (stops.length && stops[0].label === s.label && stops[0].where === s.where && stops.length > 5) break;
    stops.push(s);
  }
  const noRing = stops.filter(s => !s.ring), hidden = stops.filter(s => !s.inView);
  const reached = [...new Set(stops.map(s => s.where))];
  results.focus.push({ id, stops: stops.length, reached, noRing, hidden, first: stops.slice(0, 6).map(s => `${s.where}:${s.label}`) });
  if (noRing.length || hidden.length) failures++;
  await page.close();
}

// Scene switching by keyboard: Next through every scene, then Previous back.
{
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  await page.goto(url + '#s1');
  const seen = [];
  for (let i = 0; i < 15; i++) { await page.focus('#g3Next'); await page.keyboard.press('Enter'); seen.push(await page.evaluate(() => document.body.dataset.scene)); }
  const nextDisabled = await page.$eval('#g3Next', b => b.disabled);
  for (let i = 0; i < 15; i++) { await page.focus('#g3Prev'); await page.keyboard.press('Enter'); }
  const back = await page.evaluate(() => document.body.dataset.scene);
  await page.goto(url + '#s15');
  await page.click('[data-goto-scene="s16"]');
  const linked = await page.evaluate(() => document.body.dataset.scene);
  results.switching = { nextSequence: seen, nextDisabledAtEnd: nextDisabled, backAt: back, searchByNameLeadsTo: linked, ok: seen.join() === 's2,s3,s4,s5,s6,s7,s8,s9,s10,s11,s12,s13,s14,s15,s16' && nextDisabled && back === 's1' && linked === 's16' };
  if (!results.switching.ok) failures++;
  await page.close();
}
await browser.close();

const all = [...pairs.values()].sort((a, b) => a.ratio - b.ratio);
results.contrast = {
  uniquePairs: all.length,
  failing: all.filter(p => !p.disabled && p.ratio < p.min),
  disabledExempt: all.filter(p => p.disabled),
  lowest: all.filter(p => !p.disabled).slice(0, 12),
  overImages: all.filter(p => p.images).length,
};
results.summary = { runs: results.runs.length, runFailures: results.runs.filter(r => !r.ok).length, failures };
fs.writeFileSync(path.join(ev, 'checks.json'), JSON.stringify(results, null, 1) + '\n');
console.log(JSON.stringify(results.summary), 'contrast pairs', all.length, 'failing', results.contrast.failing.length, 'disabled', results.contrast.disabledExempt.length);
for (const f of results.focus) console.log('focus', f.id, f.stops, 'stops', f.reached.join('/'), 'noRing', f.noRing.length, 'hidden', f.hidden.length);
console.log('switching', JSON.stringify(results.switching));
for (const r of results.runs.filter(r => !r.ok).slice(0, 10)) console.log('FAIL', r.size, r.theme, r.id, 'hscroll', r.hscroll, 'errors', r.consoleErrors, 'text', JSON.stringify(r.textFails.slice(0, 3)), 'edges', JSON.stringify(r.edgeFails));
process.exitCode = failures ? 1 : 0;
