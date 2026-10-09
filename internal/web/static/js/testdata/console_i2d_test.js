// console_i2d_test.js — Console-lite I2d: fixture-command confirmation
// (component-specs §12), the action bar (§15) and presets (§14), run as the
// browser runs them: the real index.html in minidom.js, the LITERAL api.js,
// ws.js, programmer.js, ui.js, console-controls.js and console.js, against a
// real Benny512 server (internal/web/console_i2d_test.go) with real Robe
// BMFL Spot (B1, B2) and LEDBeam 100 (L1, L2) profiles.
//
// usage: node console_i2d_test.js <jsDir> <baseURL> <idsJSON>
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


const cmdBtn = label => qa('[data-cmd]').find(b => b.getAttribute('data-cmd').endsWith('#' + label) || (b.textContent || '').indexOf(label) === 0);
const dialogs = () => qa('[data-cmd-dialog]');
// No modal: the master strip and Disarm stay operable while a command asks (§12).
let modalCalls = 0;
{
  const proto = Object.getPrototypeOf(document.createElement('dialog'));
  const orig = proto.showModal;
  proto.showModal = function () { if (this.hasAttribute && (this.hasAttribute('data-cmd-dialog') || this.hasAttribute('data-choose'))) modalCalls++; return orig.call(this); };
}

(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));

  // --- §12 fixture commands: Reset and Lamp off ask first -------------------------
  await sect('commands', async () => {
    await select('B1', 'B2');
    await tab('other');
    const zone = q('[data-cmd-zone]');
    check(!!zone && /Fixture commands/.test(zone.textContent), 'Control has one separate "Fixture commands" caution section');
    const labels = zone ? zone.querySelectorAll('[data-cmd]').map(b => b.textContent) : [];
    check(['Total reset', 'Pan/Tilt reset', 'Lamp Off'].every(l => labels.some(x => x.indexOf(l) === 0)), 'every FixtureGlobalReset set and Lamp Off are commands there', labels);
    check(!qa('.b5-cc-hold').some(b => /reset|Lamp Off/i.test(b.textContent)), 'no Reset or Lamp off is a HOLD button any more');
    const lampOn = q('[data-set="Control1#12#Lamp On"]');
    check(!!lampOn && /HOLD 0\.75 s/.test(lampOn.textContent), 'Lamp On keeps the 0.75 s hold (only Reset and Lamp off ask)', lampOn && lampOn.textContent);

    const n0 = posts(SET).length;
    const total = cmdBtn('Total reset');
    click(total);
    let dlg = q('[data-cmd-dialog]');
    check(!!dlg && dlg.open, 'Total reset… opens a dialog');
    check(modalCalls === 0, 'the dialog is not modal: the page stays operable (Disarm reachable)');
    const t = dlg && q('#' + dlg.getAttribute('aria-labelledby'));
    check(!!t && t.textContent === 'Reset 2 fixtures?', 'it is labelled "Reset 2 fixtures?"', t && t.textContent);
    const items = dlg ? dlg.querySelectorAll('[data-cmd-targets] li').map(li => li.textContent) : [];
    check(items.length === 2 && items.some(x => /B1/.test(x)) && items.some(x => /B2/.test(x)), 'it lists the actual targets', items);
    check(dlg && /may stop while they restart/.test(dlg.textContent), 'and the consequence');
    check(document.activeElement === q('[data-cmd-cancel]'), 'Cancel has focus first');
    click(total);
    check(dialogs().length === 1, 'pressing the trigger again does not open a second dialog');
    kd(dlg, 'Escape');
    check(dialogs().length === 0, 'Escape cancels');
    check(document.activeElement && document.activeElement.getAttribute('data-cmd') === total.getAttribute('data-cmd'), 'focus returns to the trigger');
    await settled();
    check(posts(SET).length === n0, 'nothing was sent by opening, re-pressing or cancelling');

    // The selection changes while the dialog is open: confirming re-checks,
    // redraws and asks again instead of sending.
    click(cmdBtn('Total reset'));
    dlg = q('[data-cmd-dialog]');
    await server('POST', '/api/programmer/select', { action: 'set', targets: [T('B1')] });
    click(q('[data-cmd-confirm]'));
    await until('the dialog to notice the change', () => /changed/.test((q('[data-cmd-changed]') || {}).textContent || ''));
    check(posts(SET).length === n0, 'a changed selection is not sent on the first confirm');
    const t2 = q('#' + q('[data-cmd-dialog]').getAttribute('aria-labelledby'));
    check(t2.textContent === 'Reset 1 fixture?', 'the dialog now says "Reset 1 fixture?"', t2.textContent);
    click(q('[data-cmd-confirm]'));
    await until('the reset to be sent', () => posts(SET).length === n0 + 1);
    const fi = attr('Control1').variants[0].functions.findIndex(f => f.attribute === 'FixtureGlobalReset');
    check(same(last(SET).body, { targets: [T('B1')], attribute: 'Control1', functionIndex: fi, set: 'Total reset' }), 'the second confirm sends Total reset to exactly the listed target', last(SET).body);
    check(dialogs().length === 0, 'and closes');
    await until('Control1 at Total reset', () => { const c = attr('Control1').channels.find(x => x.entryId === ids.B1); return c && c.value >= 200 && c.value <= 209; });

    // A held key auto-repeating onto the action is not a decision.
    await select('B1', 'B2');
    await tab('other');
    const n1 = posts(SET).length;
    click(cmdBtn('Lamp Off'));
    dlg = q('[data-cmd-dialog]');
    const t3 = q('#' + dlg.getAttribute('aria-labelledby'));
    check(t3.textContent === 'Switch off 2 lamps?', 'Lamp Off asks "Switch off 2 lamps?"', t3.textContent);
    kd(q('[data-cmd-confirm]'), 'Enter', { repeat: true });
    click(q('[data-cmd-confirm]'));
    await settled();
    check(posts(SET).length === n1, 'an auto-repeated Enter does not confirm');
    kd(q('[data-cmd-confirm]'), 'Enter');
    click(q('[data-cmd-confirm]'));
    await until('Lamp Off to be sent', () => posts(SET).length === n1 + 1);
    check(last(SET).body.set === 'Lamp Off' && last(SET).body.targets.length === 2, 'a fresh Enter confirms: Lamp Off to both', last(SET).body);
  });

  // --- §15 action bar: Highlight Previous/Next, Clear scopes, Fan shapes -------------
  await sect('action bar', async () => {
    await select('B1', 'B2', 'L1');
    await tab('dimmer');
    check(!q('[data-hl-next]'), 'Previous/Next appear only while Highlight is ON');
    click(q('[data-highlight]'));
    await until('Highlight ON', () => q('[data-hl-next]'));
    check(same(last('/api/programmer/highlight').body, { highlight: true }), 'Highlight posts {highlight:true}');
    check(/whole selection \(3\)/.test(q('[data-hl-step]').textContent), 'the step line says the whole selection is highlighted', q('[data-hl-step]').textContent);
    const next = () => q('[data-hl-next]');
    next().focus();
    click(next());
    await until('step 1 of 3', () => /1 of 3 · B1/.test(q('[data-hl-step]').textContent));
    check(same(last('/api/programmer/highlight').body, { step: 'next' }), 'Next posts {step:"next"}');
    check(PS().state().highlight.step === 0, 'the server highlights the first in selection order', PS().state().highlight.step);
    check(document.activeElement && document.activeElement.hasAttribute('data-hl-next'), 'focus stays on Next after the bar redraws, so it can be pressed again');
    click(next());
    await until('step 2 of 3', () => /2 of 3 · B2/.test(q('[data-hl-step]').textContent));
    click(next());
    await until('step 3 of 3', () => /3 of 3 · L1/.test(q('[data-hl-step]').textContent));
    check(/last of 3/.test(q('[data-hl-step]').textContent), 'the last says "last of 3"', q('[data-hl-step]').textContent);
    click(next());
    await settled();
    await sleep(300);
    check(PS().state().highlight.step === 2 && /3 of 3 · L1/.test(q('[data-hl-step]').textContent), 'Next on the last does not wrap', q('[data-hl-step]').textContent);
    click(q('[data-hl-prev]'));
    await until('step 2 of 3 again', () => /2 of 3 · B2/.test(q('[data-hl-step]').textContent));
    click(q('[data-hl-all]'));
    await until('the whole selection again', () => /whole selection/.test(q('[data-hl-step]').textContent));
    click(q('[data-highlight]'));
    await until('Highlight OFF', () => !q('[data-hl-next]'));

    // Clear… is a scope chooser with labelled scopes.
    click(q('[data-clear-open]'));
    await until('the clear panel', () => q('[data-act-panel]') && !q('[data-act-panel]').hidden);
    const panel = q('[data-act-panel]');
    check(!!panel.querySelector('[data-clear-group="dimmer"]') && /Dimmer on selection/.test(panel.textContent) && !!panel.querySelector('[data-clear-selection-values]') && !!panel.querySelector('[data-clear-all]'),
      'Clear… offers "Dimmer on selection", "Selection values" and "Everything"', panel.textContent);
    check(/group fader, a running test, or the profile default/.test(panel.textContent) && /selection is emptied/.test(panel.textContent), 'it explains the fallback sources and that Everything empties the selection');
    check(q('[data-clear-open]').getAttribute('aria-expanded') === 'true', 'Clear… says it is expanded');
    kd(panel, 'Escape');
    check(q('[data-act-panel]').hidden, 'Escape closes the panel');
    check(document.activeElement && document.activeElement.hasAttribute('data-clear-open'), 'and focus goes back to Clear…');

    // Fan: four shapes, glyph + word, mapped explicitly to server shapes.
    click(q('[data-fan-open]'));
    await until('the fan panel', () => q('[data-fan-shape]'));
    const opts = qa('[data-fan-shape-opt]');
    check(same(opts.map(o => o.getAttribute('data-fan-shape-opt')), ['linear', 'reverse', 'centre-out', 'edges-in']) && q('[data-fan-shape]').getAttribute('role') === 'radiogroup',
      'the fan chooser is a radio group: linear, reverse, centre out, edges in');
    check(same(opts.map(o => iconOf(o)[0]), ['#b5-icon-fan-linear', '#b5-icon-fan-reverse', '#b5-icon-fan-center-out', '#b5-icon-fan-edges-in']) && opts.every(o => /\w/.test(o.textContent)), 'each shape has its glyph AND a word', opts.map(o => o.textContent));
    check(/1 B1 → 2 B2 → 3 L1/.test(q('[data-fan-order]').textContent), 'the panel states the selection order', q('[data-fan-order]').textContent);
    kd(q('[data-fan-shape-opt="linear"]'), 'ArrowRight');
    check(q('[data-fan-shape-opt="reverse"]').getAttribute('aria-checked') === 'true', 'arrow keys move the shape choice');
    click(q('[data-fan-shape-opt="edges-in"]'));
    const fa = q('[data-fan-attr]');
    fa.value = 'Dimmer'; fa.dispatchEvent(new Event('change', { bubbles: true }));
    click(q('[data-fan-go]'));
    await until('the fan post', () => posts('/api/programmer/fan').length === 1);
    check(same(last('/api/programmer/fan').body, { attribute: 'Dimmer', shape: 'edges-in', from: { fraction: 0 }, to: { fraction: 1 } }), 'Edges in posts shape "edges-in" (never linear)', last('/api/programmer/fan').body);
    await until('the panel to close after the fan', () => q('[data-act-panel]').hidden);
    await until('edges-in on the server', () => held('Dimmer', 'B2') > 0);
    check(held('Dimmer', 'B1') === 0 && held('Dimmer', 'L1') === 0 && held('Dimmer', 'B2') === 65535, 'edges in: both ends at the start, the centre at the end', [held('Dimmer', 'B1'), held('Dimmer', 'B2'), held('Dimmer', 'L1')]);
    click(q('[data-fan-open]'));
    await until('the fan panel again', () => q('[data-fan-shape-opt="edges-in"]'));
    check(q('[data-fan-shape-opt="edges-in"]').getAttribute('aria-checked') === 'true', 'the panel remembers the last shape');
    click(q('[data-fan-shape-opt="centre-out"]'));
    click(q('[data-fan-go]'));
    await until('the second fan post', () => posts('/api/programmer/fan').length === 2);
    check(last('/api/programmer/fan').body.shape === 'mirror', 'Centre out is sent as the server\'s "mirror"', last('/api/programmer/fan').body);
  });

  // --- §14 presets: glyph + name + "applies to n of m"; Replace / Save as new -------
  await sect('presets', async () => {
    await select('B1');
    await tab('colour');
    await serverSet({ targets: [T('B1')], attribute: 'Color1', slot: 2 });
    const storePreset = async name => {
      click(q('[data-preset-store="colour"]'));
      await until('the preset name dialog', () => q('[data-ask-input]'));
      q('[data-ask-input]').value = name;
      click(q('[data-ask-ok]'));
    };
    await storePreset('Warm');
    await until('the preset to be stored', () => posts('/api/programmer/presets/store').length === 1);
    check(same(last('/api/programmer/presets/store').body, { name: 'Warm', family: 'colour' }), 'Store posts {name, family}');
    await select('B2', 'L1');
    await tab('colour');
    await until('the preset button', () => q('[data-preset]'));
    const pb = q('[data-preset]');
    check(iconOf(pb)[0] === '#b5-icon-preset-colour' && /Warm/.test(pb.textContent), 'a preset button carries the family glyph and its name', [iconOf(pb), pb.textContent]);
    check(/applies to 1 of 2 selected/.test(pb.textContent), 'a partial preset says "applies to 1 of 2 selected" (B2 by type from B1; L1 not)', pb.textContent);
    await select('L1');
    await tab('colour');
    await until('the redrawn preset', () => q('[data-preset]') && /none/.test(q('[data-preset]').textContent));
    check(q('[data-preset]').disabled && /applies to none of the selection/.test(q('[data-preset]').textContent), 'a preset that applies to none of the selection is disabled and says why');

    await select('B1');
    await tab('colour');
    const n0 = posts('/api/programmer/presets/store').length;
    await storePreset('warm');
    await until('the Replace / Save as new choice', () => q('[data-choose]'));
    check(modalCalls === 0, 'the choice dialog is not modal (Disarm stays reachable)');
    check(document.activeElement === q('[data-choose] [data-choice="cancel"]'), 'Cancel has focus first: replacing is never the default');
    check(!!q('[data-choice="new"]') && /Replace "Warm"/.test(q('[data-choice="replace"]').textContent), 'an existing name offers Save as new and Replace "Warm"');
    click(q('[data-choice="replace"]'));
    await until('the overwrite', () => posts('/api/programmer/presets/overwrite').length === 1);
    check(posts('/api/programmer/presets/store').length === n0 && last('/api/programmer/presets/overwrite').body.id === pb.getAttribute('data-preset'), 'Replace overwrites that preset (no second store)');
    await storePreset('Warm');
    await until('the choice again', () => q('[data-choose]'));
    click(q('[data-choice="new"]'));
    await until('a new store', () => posts('/api/programmer/presets/store').length === n0 + 1);
    check(same(last('/api/programmer/presets/store').body, { name: 'Warm', family: 'colour' }), 'Save as new stores a second preset');

    // Groups: Replace / Merge / Save as new.
    await select('B1', 'B2');
    const storeGroup = async name => {
      click(q('[data-store-group]'));
      await until('the group name dialog', () => q('[data-ask-input]'));
      q('[data-ask-input]').value = name;
      click(q('[data-ask-ok]'));
    };
    await storeGroup('Front');
    await until('the group', () => posts('/api/programmer/groups/store').length === 1);
    await until('the stored group in the view', () => (PS().state().storedGroups || []).length === 1);
    await select('L1');
    await storeGroup('front');
    await until('the group choice', () => q('[data-choose]'));
    check(!!q('[data-choice="merge"]') && !!q('[data-choice="replace"]') && !!q('[data-choice="new"]'), 'an existing group name offers Merge, Replace and Save as new');
    click(q('[data-choice="merge"]'));
    await until('the merge', () => posts('/api/programmer/groups/merge').length === 1);
    await until('three members', () => ((PS().state().storedGroups || [])[0] || {}).members && PS().state().storedGroups[0].members.length === 3);
    check(same(PS().state().storedGroups[0].members.map(m => m.entryId), [ids.B1, ids.B2, ids.L1]), 'Merge adds the selection after the group\'s members');
  });

  check(thrown.length === 0, 'nothing threw during the whole run', thrown);
  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
