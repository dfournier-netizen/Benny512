// console_i2c2_test.js — Console-lite I2c2 attribute controls (colour §9,
// range segments §7, wheel slots §8, shutter §10, shaper §11, and the
// physical-unit fix), run as the browser runs them: the real index.html in
// minidom.js, the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js and console.js, against a real Benny512 server
// (internal/web/console_i2c2_test.go) with real Robe BMFL Spot (B1, B2) and
// LEDBeam 100 (L1, L2) profiles and two blade fixtures (SH1, SH2).
//
// usage: node console_i2c2_test.js <jsDir> <baseURL> <idsJSON>
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
// Anything the controls throw (a render inside a promise) is a failure,
// with its message, not a silent half-drawn panel.
const thrown = [];
process.on('unhandledRejection', e => thrown.push(String(e && e.message || e)));
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(what, fn, ms = 4000) {
  const end = Date.now() + ms;
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
const url = new URL(base);
const calls = [];
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p), body: opts.body ? JSON.parse(opts.body) : undefined };
    calls.push(rec);
    const res = await fetch(new URL(p, base), opts);
    rec.status = res.status;
    rec.res = await res.clone().text();
    return res;
  },
};
const ctx = vm.createContext(sandbox);
for (const f of ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-controls.js', 'console.js']) {
  vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
}
const get = expr => vm.runInContext(expr, ctx);
const PS = () => get('ProgrammerSync');
const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
const last = p => { const l = posts(p); return l[l.length - 1]; };
const SET = '/api/programmer/set';
const q = sel => document.querySelector(sel);
const attr = name => { const s = PS().state(); for (const g of s.groups) for (const a of g.attributes) if (a.attribute === name) return a; return null; };
const T = n => ({ entryId: ids[n], cell: '' });
async function select(...names) {
  await PS().act('select', { action: 'set', targets: names.map(T) });
  await until('the selection ' + names.join('+') + ' to arrive', () => PS().state().selection.length === names.length);
  await sleep(150);
}
const settled = async () => { await sleep(120); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); };
const NB = ' ';
const kd = (el, key, extra) => el.dispatchEvent(new Event('keydown', Object.assign({ bubbles: true, key }, extra || {})));
const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
const input = (el, v, type) => { el.value = String(v); el.dispatchEvent(new Event(type || 'input', { bubbles: true })); };
const qa = sel => document.querySelectorAll(sel);
const held = (name, n) => { const a = attr(name); const c = a && a.channels.find(x => x.entryId === ids[n]); return c ? c.value : undefined; };
const serverSet = async body => {
  const r = await server('POST', SET, body);
  if (r.status !== 200) check(false, 'direct server write ' + JSON.stringify(body), r);
  await until('the browser to see revision ' + (r.body && r.body.revision), () => PS().revision() >= r.body.revision);
  await sleep(700);
  get('ConsoleControls').refresh();
};
const tab = async k => { click(q('[data-tab-group="' + k + '"]')); await until('the ' + k + ' panel', () => q('[data-panel="' + k + '"]'), 2000); };
const sect = async (name, fn) => { try { await fn(); } catch (e) { check(false, name + ': ' + (e && e.stack || e)); } };
const iconOf = el => (el ? el.querySelectorAll('.b5-con-ic').map(i => i.innerHTML).join(' ') : '').match(/#b5-icon-([a-z-]+)/g) || [];

(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));

  // --- physical units: rotate / spin are speeds, never degrees -----------------
  await sect('units', async () => {
    await select('SH1');
    await tab('beam');
    await serverSet({ targets: [T('SH1')], attribute: 'Gobo1', functionIndex: 1, dmx: 200 });
    input(q('[data-readout-units]'), 'phys', 'change');
    const ro = q('[data-readout="Gobo1"] .b5-cc-val');
    check(ro && ro.textContent === '8' + NB + '°/s', 'Gobo1WheelSpin at 200 reads 8 °/s (GDTF AngularSpeed), not a bare number or degrees', ro && ro.textContent);
    const cap = q('[data-fader-caption="Gobo1#1"]');
    check(!!cap && /-60–60 °\/s/.test(cap.textContent), 'the spin fader names its physical range in °/s', cap && cap.textContent);
    await tab('shaper');
    await serverSet({ targets: [T('SH1')], attribute: 'ShaperRot', dmx: 255 });
    const sr = q('[data-readout="ShaperRot"] .b5-cc-val');
    check(sr && sr.textContent === '45°', 'ShaperRot (GDTF Angle) still reads degrees: 45°', sr && sr.textContent);
    input(q('[data-readout-units]'), 'pct', 'change');
    await select('B1');
    await tab('beam');
    const bc = q('[data-fader-caption="Gobo1#4"]');
    check(!!bc && /°\/s/.test(bc.textContent) && !/°(?!\/s)/.test(bc.textContent), 'the BMFL Gobo1WheelSpin fader is labelled °/s, never °', bc && bc.textContent);
  });

  // --- range rail (§7) -----------------------------------------------------------
  await sect('range rail', async () => {
    await select('SH1');
    await tab('beam');
    const labels = qa('[data-rail="Gobo1#0"] [data-seg]');
    check(labels.length === 2 && /Gobo1WheelSpin/.test(labels[1].textContent) && /DMX 128–255/.test(labels[1].textContent), 'the Gobo1 channel has a range rail: one label button per function with its DMX range', labels.map(l => l.textContent));
    check(qa('[data-rail="Gobo1#0"] [data-seg-bar]').length === 2, 'and one proportional segment per function');
    click(q('[data-seg="Gobo1#1"]'));
    await settled();
    check(same(last(SET).body, { targets: [T('SH1')], attribute: 'Gobo1', functionIndex: 1, dmx: 192 }), 'a label tap jumps to the function\'s stated default (192), not a midpoint', last(SET).body);
    await until('SH1 Gobo1 192', () => held('Gobo1', 'SH1') === 192);
    await sleep(700); get('ConsoleControls').refresh();
    check(/IN$/.test(q('[data-seg-state="Gobo1#1"]').textContent) && /is-in/.test(q('[data-seg-bar="Gobo1#1"]').className), 'the segment holding the value says IN (word + fill)', q('[data-seg-state="Gobo1#1"]').textContent);
    await select('B1', 'B2');
    await tab('beam');
    click(q('[data-seg="Gobo1#4"]'));
    await settled();
    check(same(last(SET).body, { targets: [T('B1'), T('B2')], attribute: 'Gobo1', functionIndex: 4, dmx: 200 }), 'BMFL Gobo1WheelSpin (default outside it): the tap goes to its first value, 200', last(SET).body);
    await serverSet({ targets: [T('B2')], attribute: 'Gobo1', slot: 3, functionIndex: 0 });
    check(q('[data-seg-state="Gobo1#4"]').textContent === ' · IN on 1 of 2', 'a split selection reads "IN on 1 of 2"', q('[data-seg-state="Gobo1#4"]').textContent);
  });

  // --- wheel slots (§8) --------------------------------------------------------------
  await sect('wheel slots', async () => {
    await select('SH1');
    await tab('beam');
    const open = q('[data-slot="Gobo1#0#1"]'), dots = q('[data-slot="Gobo1#0#2"]');
    check(open && iconOf(open).join().includes('slot-open') && !/not reported/.test(open.textContent), 'the Open slot is a real slot with the slot-open glyph (not a missing-media fallback)', open && [iconOf(open), open.textContent]);
    check(dots && /is-unreported/.test(dots.className) && /media not reported/.test(dots.textContent) && iconOf(dots).join().includes('gobo-placeholder') && /Dots/.test(dots.textContent) && /Slot 2/.test(dots.textContent),
      'a gobo slot with no media: dashed, generic gobo glyph, "media not reported", name and slot number', dots && [dots.className, dots.textContent]);
    await tab('colour');
    click(q('[data-colour-mode="wheel"]'));
    await until('the colour wheel cards', () => q('[data-slot="Color1#0#1"]'), 2000);
    const myst = q('[data-slot="Color1#0#1"]'), congo = q('[data-slot="Color1#0#2"]');
    check(myst && /colour not reported/.test(myst.textContent) && iconOf(myst).join().includes('colour-slot-placeholder') && /is-unreported/.test(myst.className), 'a colour slot with no stated colour: placeholder disc + "colour not reported"', myst && myst.textContent);
    check(congo && !!congo.querySelector('.b5-cc-swatch__c') && congo.querySelector('.b5-cc-swatch__c').style.background === '#2a1f8f', 'a colour slot with a stated colour shows it as capped content inside a full-contrast frame', congo && congo.innerHTML);
    await select('B1', 'B2');
    await tab('beam');
    const s3 = q('[data-slot="Gobo1#0#3"]');
    check(s3 && /image 15020291 · not shown/.test(s3.textContent) && iconOf(s3).join().includes('gobo-placeholder'), 'a slot with media names the file and says it is not shown (no invented artwork)', s3 && s3.textContent);
    check(q('[data-slot-state="Gobo1#0#3"]').textContent === 'IN on 1 of 2' && s3.getAttribute('aria-pressed') === 'false', 'B2 alone in slot 3: "IN on 1 of 2", not pressed', [q('[data-slot-state="Gobo1#0#3"]').textContent, s3.getAttribute('aria-pressed')]);
  });

  // --- shutter (§10) ---------------------------------------------------------------------
  await sect('shutter', async () => {
    await select('L1', 'L2');
    await tab('beam');
    const g = q('[data-shutter-group="Shutter1#0"]');
    check(!!g && g.getAttribute('role') === 'radiogroup' && qa('[data-shutter-group="Shutter1#0"] [role="radio"]').length === 2, 'Shutter1 has an OPEN / CLOSED radio group');
    check(!q('[data-set="Shutter1#0#Shutter closed"]') && !q('[data-set="Shutter1#0#Shutter Open"]'), 'the open/closed sets are not drawn a second time as plain set buttons');
    const closed = q('[data-shutter="Shutter1#0#closed"]');
    check(closed && /CLOSED/.test(closed.textContent) && iconOf(closed).join().includes('shutter-closed') && iconOf(q('[data-shutter="Shutter1#0#open"]')).join().includes('shutter-open'), 'each choice has its glyph AND its word');
    click(closed);
    await settled();
    check(same(last(SET).body, { targets: [T('L1'), T('L2')], attribute: 'Shutter1', functionIndex: 0, set: 'Shutter closed' }), 'CLOSED writes the file\'s "Shutter closed" set', last(SET).body);
    await until('both closed', () => held('Shutter1', 'L1') <= 31 && held('Shutter1', 'L2') <= 31);
    await sleep(700); get('ConsoleControls').refresh();
    check(q('[data-shutter="Shutter1#0#closed"]').getAttribute('aria-checked') === 'true' && q('[data-shutter-state="Shutter1#0"]').textContent === 'CLOSED · beam blocked', 'CLOSED is checked and says "CLOSED · beam blocked"', q('[data-shutter-state="Shutter1#0"]').textContent);
    await serverSet({ targets: [T('L2')], attribute: 'Shutter1', functionIndex: 5, set: 'Shutter Open' });
    check(q('[data-shutter-state="Shutter1#0"]').textContent === 'MIXED · closed on 1 of 2' && qa('[data-shutter-group="Shutter1#0"] [aria-checked="true"]').length === 0, 'split: "MIXED · closed on 1 of 2", neither choice checked', q('[data-shutter-state="Shutter1#0"]').textContent);
    const sc = q('[data-fader-caption="Shutter1#1"]');
    check(!!sc && /Hz/.test(sc.textContent), 'the strobe fader states Hz (or "Hz not reported")', sc && sc.textContent);
  });

  // --- colour (§9) -----------------------------------------------------------------------
  await sect('colour', async () => {
    await select('L1', 'B1');
    await tab('colour');
    const caps = q('[data-colour-caps]').textContent;
    check(/1 mix RGBW/.test(caps) && /1 mix CMY/.test(caps) && /CTO/.test(caps) && /CTC/.test(caps), 'capability counts: "1 mix RGBW", "1 mix CMY", CTO and CTC', caps);
    check(/Red \(1 of 2\), Green \(1 of 2\), Blue \(1 of 2\), White \(1 of 2\)/.test(q('[data-colour-emitters]').textContent), 'the emitters are named from the profile, with how many of the selection have each', q('[data-colour-emitters]').textContent);
    const modes = qa('[data-colour-mode]').map(b => b.getAttribute('data-colour-mode'));
    check(same(modes, ['mix', 'wheel', 'temp']) && q('[data-colour-mode="mix"]').getAttribute('role') === 'tab', 'Mix / Wheel / CTO-CTC mode tabs, only those the selection has', modes);
    click(q('[data-colour-mode="mix"]'));
    await until('the Mix panel', () => q('[data-colour-panel="mix"]'), 2000);
    check(!q('[data-colour-input]'), 'no native colour input (its system dialog is not capped for a dark venue)');
    const field = q('[data-colour-field]');
    check(!!field && !!field.querySelector('.b5-cc-mix__content'), 'a hue/saturation field with capped content', field && field.childNodes.map(c => c.className));
    if (field) {
      field.getBoundingClientRect = () => ({ left: 0, top: 0, width: 360, height: 100, right: 360, bottom: 100, x: 0, y: 0 });
      const b0 = posts(SET).length;
      field.dispatchEvent(new Event('pointerdown', { bubbles: true, clientX: 0, clientY: 0, pointerId: 3 }));
      field.dispatchEvent(new Event('pointerup', { bubbles: true, clientX: 0, clientY: 0, pointerId: 3 }));
      await settled();
      const w = posts(SET).slice(b0).map(c => c.body).sort((a, b) => a.attribute < b.attribute ? -1 : 1);
      check(same(w, [
        { targets: [T('L1')], attribute: 'ColorAdd_B', fraction: 0 }, { targets: [T('L1')], attribute: 'ColorAdd_G', fraction: 0 }, { targets: [T('L1')], attribute: 'ColorAdd_R', fraction: 1 },
        { targets: [T('B1')], attribute: 'ColorSub_C', fraction: 0 }, { targets: [T('B1')], attribute: 'ColorSub_M', fraction: 1 }, { targets: [T('B1')], attribute: 'ColorSub_Y', fraction: 1 },
      ]), 'top-left of the field (hue 0, full saturation) writes red as each fixture has it', w);
      await until('red on L1', () => held('ColorAdd_R', 'L1') === 255 && held('ColorAdd_G', 'L1') === 0);
      await sleep(700); get('ConsoleControls').refresh();
      check(/DMX values, not measured colour/.test(q('[data-colour-dmx]').textContent) && /R 100 % · G 0 % · B 0 %/.test(q('[data-colour-dmx]').textContent), 'the field reads the first selected fixture\'s DMX values, not a measured colour', q('[data-colour-dmx]').textContent);
    }
    const css = fs.readFileSync(path.join(jsDir, '..', 'css', 'screens-console-controls.css'), 'utf8');
    check(/\.b5-cc-mix__content\s*\{[^}]*filter:\s*brightness\(var\(--b5-preview-max-brightness\)\)/.test(css) && /\.b5-cc-swatch__c\s*\{[^}]*filter:\s*brightness\(var\(--b5-preview-max-brightness\)\)/.test(css),
      'picker content and swatches are capped by the dark-venue preview multiplier (theme-contract)');
    // wheel-only: nearest-slot words before/with the write
    await select('SH1');
    await tab('colour');
    check(/1 wheel only/.test(q('[data-colour-caps]').textContent) && same(qa('[data-colour-mode]').map(b => b.getAttribute('data-colour-mode')), ['wheel']), 'a wheel-only fixture: "1 wheel only" and only the Wheel mode', q('[data-colour-caps]').textContent);
    const f2 = q('[data-colour-field]');
    if (f2) {
      f2.getBoundingClientRect = () => ({ left: 0, top: 0, width: 360, height: 100, right: 360, bottom: 100, x: 0, y: 0 });
      f2.dispatchEvent(new Event('pointerdown', { bubbles: true, clientX: 240, clientY: 0, pointerId: 4 }));
      check(/→ nearest slot: .*Congo blue/.test(q('[data-colour-nearest]').textContent), 'blue picked: "→ nearest slot: … Congo blue" (the stated colour; the unstated slot is never chosen)', q('[data-colour-nearest]').textContent);
      f2.dispatchEvent(new Event('pointerup', { bubbles: true, clientX: 240, clientY: 0, pointerId: 4 }));
      await settled();
      check(same(last(SET).body, { targets: [T('SH1')], attribute: 'Color1', functionName: 'Color1', slot: 2 }), 'and writes that slot', last(SET).body);
    } else check(false, 'a wheel-only selection gets the picker field');
  });

  // --- shaper (§11) -----------------------------------------------------------------------
  await sect('shaper', async () => {
    await select('SH1', 'SH2');
    await tab('shaper');
    await serverSet({ targets: [T('SH1'), T('SH2')], attribute: 'Blade1A', dmx: 255 });
    await serverSet({ targets: [T('SH1')], attribute: 'Blade2B', dmx: 0 });
    await serverSet({ targets: [T('SH2')], attribute: 'Blade2B', dmx: 255 });
    const d = q('[data-shaper-diagram]');
    check(!!d && d.getAttribute('role') === 'img', 'the shaper diagram is a described image');
    const lab = d ? d.getAttribute('aria-label') : '';
    check(/geometry not reported/.test(lab) && /Blade 1 \(drawn top\): A 100 %, B 0 %/.test(lab) && /Blade 2 \(drawn right\): A 0 %, B MIXED/.test(lab), 'its words name each blade, its side and both ends; a split end says MIXED', lab);
    check(d && />1<\/text>/.test(d.innerHTML) && />2<\/text>/.test(d.innerHTML) && />A<\/text>/.test(d.innerHTML) && />B<\/text>/.test(d.innerHTML) && /is-mixed/.test(d.innerHTML), 'blades are numbered outside the beam with A/B ends (never fill alone); the mixed blade is dashed');
    const names = qa('[data-panel="shaper"] .b5-cc-attrname').map(n => n.textContent);
    check(['Blade 1 · end A', 'Blade 1 · end B', 'Blade 2 · end A', 'Blade 2 · end B', 'Assembly rotate'].every(n => names.includes(n)), 'every end has a labelled A/B fader (the same information as the diagram)', names);
    const f = q('[data-fader="Blade1A#0"]');
    input(f, 128, 'change');
    await settled();
    await until('Blade1A 128', () => held('Blade1A', 'SH1') === 128);
    await sleep(700); get('ConsoleControls').refresh();
    check(/Blade 1 \(drawn top\): A 50 %/.test(q('[data-shaper-diagram]').getAttribute('aria-label')), 'moving the A fader redraws the diagram and its words (A 50 %)', q('[data-shaper-diagram]').getAttribute('aria-label'));
  });

  check(thrown.length === 0, 'nothing threw during the whole run', thrown);
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
