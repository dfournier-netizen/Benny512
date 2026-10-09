// console_controls_test.js — the Console's attribute controls (C6b) run as
// the browser runs them: the real index.html in minidom.js, the LITERAL
// api.js, ws.js, programmer.js, ui.js, console-controls.js and console.js,
// against a real Benny512 server (internal/web/console_c6b_test.go) with
// real vendor GDTF profiles (Robe BMFL Spot x2, Robe LEDBeam 100, Elation
// Paladin Cube) and one unprofiled 4-channel fixture. Every request is
// recorded with its answer; checks are on exact payloads and on what the
// server holds afterwards.
//
// usage: node console_controls_test.js <jsDir> <baseURL> <idsJSON>
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
async function server(method, p, body, headers) {
  const r = await fetch(new URL(p, base), { method, headers: Object.assign({ 'Content-Type': 'application/json' }, headers || {}), body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  return { status: r.status, body: t ? JSON.parse(t) : null };
}

const { window, document, CustomEvent } = makeWindow();
document.loadHTML(fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8'));
const url = new URL(base);
const calls = [];
let hook = null; // (rec) => Response | null: lets a check stand in for the server
let holdGets = 0; // ms to hold every GET /api/programmer answer back
const sandbox = {
  console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date,
  WebSocket, window, document, Event, CustomEvent, location: { protocol: url.protocol, host: url.host, href: base + '/' },
  localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
  fetch: async (p, opts) => {
    opts = opts || {};
    const rec = { method: opts.method || 'GET', path: String(p), headers: Object.assign({}, opts.headers || {}), body: opts.body ? JSON.parse(opts.body) : undefined };
    calls.push(rec);
    const fake = hook && hook(rec);
    const res = fake || await fetch(new URL(p, base), opts);
    rec.status = res.status;
    rec.res = await res.clone().text();
    if (rec.method === 'GET' && rec.path === '/api/programmer' && holdGets) await sleep(holdGets);
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
const qa = sel => document.querySelectorAll(sel);
const click = (el, init) => el.dispatchEvent(new Event('click', Object.assign({ bubbles: true }, init || {})));
const input = (el, v, type) => { el.value = String(v); el.dispatchEvent(new Event(type || 'input', { bubbles: true })); };
const attr = (name) => { const s = PS().state(); for (const g of s.groups) for (const a of g.attributes) if (a.attribute === name) return a; return null; };
const T = (n, cell) => ({ entryId: ids[n], cell: cell || '' });
async function select(...names) {
  await PS().act('select', { action: 'set', targets: names.map(n => T(n)) });
  await until('the selection ' + names.join('+') + ' to be drawn', () => (PS().state().selection.length === names.length) && (names.length === 0 || q('[data-cc-body]').textContent.length > 0));
  await sleep(80);
}
async function tab(group) {
  await until('the ' + group + ' tab', () => q('[data-tab-group="' + group + '"]'));
  click(q('[data-tab-group="' + group + '"]'));
  await until('the ' + group + ' panel', () => q('[data-panel="' + group + '"]'));
}
const settled = async () => { await sleep(120); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); };

(async () => {
  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the controls region to mount', () => q('[data-cc-body]'));
  check(!!q('[data-controls-region] [data-cc-status]') && !/arrive next/.test(q('[data-controls-region]').textContent), 'console.js mounts the controls into its Controls region (the C6a placeholder is replaced)');
  check(/Nothing selected/.test(q('[data-cc-body]').textContent), 'with nothing selected the controls say so in words');

  // --- unprofiled fixture: raw DMX only -----------------------------------
  await select('G1');
  await until('the raw panel (fixture models loaded)', () => qa('[data-raw-panel] [data-raw]').length === 4);
  check(qa('[data-tab-group]').length === 0, 'an unprofiled fixture gets no attribute tabs');
  const raws = qa('[data-raw-panel] [data-raw]');
  check(raws.length === 4, 'an unprofiled 4-channel fixture gets one raw fader per channel (4)', raws.length);
  const RAW = '/api/programmer/raw';
  const r1 = raws[0];
  const before = posts(RAW).length;
  for (let v = 1; v <= 40; v++) input(r1, v);
  await sleep(10);
  const burst = posts(RAW).length - before;
  input(r1, 40, 'change');
  await settled();
  await until('the server to hold raw ch 1 = 40', async () => { const s = (await server('GET', '/api/programmer')).body; return s.raw[0].offsets[0].value === 40 && s.raw[0].offsets[0].touched; });
  check(burst <= 2, '40 fader steps in one burst send at most 2 writes at once (rate-limited lane)', burst);
  check(same(last(RAW).body, { entryId: ids.G1, writes: [{ offset: 1, value: 40 }] }), 'the value at release is the last raw write: {entryId, writes:[{offset:1, value:40}]}', last(RAW).body);
  click(q('[data-raw-release="raw#' + ids.G1 + '#1"]'));
  await settled();
  check(same(last(RAW).body, { entryId: ids.G1, writes: [{ offset: 1, release: true }] }), 'Release posts {writes:[{offset, release:true}]}', last(RAW).body);

  // --- tabs follow the selection ----------------------------------------------
  await select('B1', 'L1');
  // I2c (component-specs §14) supersedes "tabs are exactly the groups the
  // selection has": all seven families are drawn; the ones the selection
  // lacks say "— not on these fixtures" and are aria-disabled. The owner
  // named the other group "Control" (was "Control / Other").
  const tabs = qa('[data-tab-group]').filter(t => t.getAttribute('aria-disabled') !== 'true').map(t => t.getAttribute('data-tab-group'));
  const groups = PS().state().groups.map(g => g.group);
  check(same(tabs, groups) && !tabs.includes('shaper') && q('[data-tab-group="shaper"]').getAttribute('aria-disabled') === 'true', 'the usable tabs are exactly the groups the selection has (Shaper drawn but not on a BMFL Spot + LEDBeam)', tabs);
  check(/^Control/.test(q('[data-tab-group="other"]').textContent), 'the other group is labelled Control');

  // --- position: XY pad in tandem with the faders ---------------------------------
  await tab('position');
  const pad = q('[data-xypad]');
  check(!!pad && !!q('[data-fader="Pan#0"]') && !!q('[data-fader="Tilt#0"]'), 'Position has the XY pad AND Pan/Tilt faders');
  pad.getBoundingClientRect = () => ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200, x: 0, y: 0 });
  const setsBefore = posts(SET).length;
  pad.dispatchEvent(new Event('pointerdown', { bubbles: true, clientX: 50, clientY: 150, pointerId: 3, button: 0 }));
  pad.dispatchEvent(new Event('pointerup', { bubbles: true, clientX: 50, clientY: 150, pointerId: 3 }));
  check(/25\u00a0%/.test(q('[data-fader-value="Pan#0"]').textContent) && /25\u00a0%/.test(q('[data-fader-value="Tilt#0"]').textContent), 'tandem: a pad tap moves the Pan and Tilt faders at once (25 %), before the server answers',
    [q('[data-fader-value="Pan#0"]').textContent, q('[data-fader-value="Tilt#0"]').textContent]);
  await settled();
  const padWrites = posts(SET).slice(setsBefore).map(c => c.body);
  check(same(padWrites, [
    { targets: [T('B1'), T('L1')], attribute: 'Pan', functionName: 'Pan', fraction: 0.25 },
    { targets: [T('B1'), T('L1')], attribute: 'Tilt', functionIndex: 0, fraction: 0.25 },
  ]), 'the pad writes Pan and Tilt as fractions of their own function, to the fixtures that have them (Pan by name: the two types list different functions)', padWrites);
  await until('the server to hold pan/tilt at 25 %', () => attr('Pan') && attr('Pan').value === 16384 && attr('Tilt').value === 16384);
  check(attr('Pan').value === 16384 && attr('Tilt').value === 16384 && attr('Pan').allTouched, 'the server holds Pan = Tilt = 16384 on both fixtures, touched');
  check(/SET/.test(q('[data-markers="Pan"]').textContent) && /16\u00a0384 \/ 65\u00a0535/.test(q('[data-readout="Pan"]').textContent), 'the Pan card says SET and reads 16 384 / 65 535 (I2c grouped digits)', q('[data-readout="Pan"]').textContent);
  check(/°/.test(q('[data-pad-readout]').textContent), 'the pad reads out degrees where the profile gives a physical range', q('[data-pad-readout]').textContent);

  input(q('[data-fader="Pan#0"]'), 49152);
  check(/Pan 75\u00a0% · 49\u00a0152 \/ 65\u00a0535/.test(q('[data-pad-readout]').textContent), 'tandem: moving the Pan fader moves the pad', q('[data-pad-readout]').textContent);
  await settled();

  // Rate limit on a drag, and the final value always last.
  const tilt = q('[data-fader="Tilt#0"]');
  const tb = posts(SET).length;
  for (let v = 20000; v < 20600; v += 10) input(tilt, v);
  const tburst = posts(SET).length - tb;
  input(tilt, 30000, 'change');
  await settled();
  // The two types have different Tilt layouts: the Tilt fader of the first
  // layout drives the fixtures with that layout (B1 here).
  const tiltOf = id => attr('Tilt').channels.find(c => c.entryId === id).value;
  await until('Tilt = 30000 on the server', () => tiltOf(ids.B1) === 30000);
  check(tburst <= 2, '60 Tilt steps in one burst send at most 2 writes at once', tburst);
  check(last(SET).body.attribute === 'Tilt' && Math.round(last(SET).body.fraction * 65535) === 30000 && same(last(SET).body.targets, [T('B1')]) && last(SET).body.functionIndex === 0, 'the release value is the last Tilt write (its own layout\'s fixtures, by function index) and the server holds 30000', last(SET).body);
  check(posts(SET).length - tb <= 4, 'the whole drag cost at most 4 writes, not 61', posts(SET).length - tb);

  click(q('[data-fine]'));
  check(q('[data-fine]').getAttribute('aria-pressed') === 'true' && /ON/.test(q('[data-fine]').textContent), 'Fine toggle says Fine: ON');
  click(q('[data-step-up="Tilt#0"]'));
  await settled();
  await until('Tilt + 1', () => tiltOf(ids.B1) === 30001);
  check(tiltOf(ids.B1) === 30001, 'with Fine on, + steps exactly one DMX value on a 16-bit channel (30000 -> 30001)');
  click(q('[data-fine]'));

  // --- colour: the picker writes what each fixture has ---------------------------
  await tab('colour');
  check(/1 mix RGB/.test(q('[data-colour-caps]').textContent) && /1 mix CMY/.test(q('[data-colour-caps]').textContent), 'the capability line counts each mapping', q('[data-colour-caps]').textContent);
  const cb = posts(SET).length;
  click(q('[data-colour="#ff0000"]'));
  await settled();
  const colourWrites = posts(SET).slice(cb).map(c => c.body).sort((a, b) => a.attribute < b.attribute ? -1 : 1);
  check(same(colourWrites, [
    { targets: [T('L1')], attribute: 'ColorAdd_B', fraction: 0 },
    { targets: [T('L1')], attribute: 'ColorAdd_G', fraction: 0 },
    { targets: [T('L1')], attribute: 'ColorAdd_R', fraction: 1 },
    { targets: [T('B1')], attribute: 'ColorSub_C', fraction: 0 },
    { targets: [T('B1')], attribute: 'ColorSub_M', fraction: 1 },
    { targets: [T('B1')], attribute: 'ColorSub_Y', fraction: 1 },
  ]), 'red: the LEDBeam gets R=1 G=0 B=0 (W untouched), the BMFL gets C=0 M=1 Y=1 — one write per attribute, to the fixture that has it', colourWrites);
  await until('the server to hold the red mix', () => attr('ColorAdd_R').value === 255 && attr('ColorSub_M').value === 255 && attr('ColorSub_C').value === 0);
  check(attr('ColorAdd_W').touched === false, 'the white emitter was not touched by the picker');
  check(!calls.slice(-12).some(c => c.status === 409), 'no write in the colour burst was refused as stale');

  // Wheel-only fixture: nearest stated slot colour, by the literal mapping,
  // on the BMFL's real colour wheel (its CMY taken away), then sent.
  const realView = PS().state();
  const wheelOnly = JSON.parse(JSON.stringify(realView));
  wheelOnly.selection = wheelOnly.selection.filter(s => s.entryId === ids.B1);
  wheelOnly.groups.forEach(g => { g.attributes = g.attributes.filter(a => !/^ColorSub_|^ColorAdd_/.test(a.attribute)).map(a => Object.assign(a, { channels: a.channels.filter(c => c.entryId === ids.B1) })).filter(a => a.channels.length); });
  const sandboxState = get('(s => { const real = ProgrammerSync.state; ProgrammerSync.state = () => s; return real; })')(wheelOnly);
  const wheelPlan = get('ConsoleControls').colourWrites([1, 0, 0]);
  get('(real => { ProgrammerSync.state = real; })')(sandboxState);
  const ww = wheelPlan.writes.filter(w => w.body.attribute === 'Color1');
  check(ww.length === 1 && same(ww[0].body, { targets: [T('B1')], attribute: 'Color1', functionName: 'Color1', slot: 2 }) && /Deep Red/.test(wheelPlan.nearest.join()),
    'a wheel-only fixture gets the nearest stated slot (red -> slot 2 "C01 (Deep Red)", #ff392a) in its plain selection function', ww.map(w => w.body).concat(wheelPlan.nearest));
  const sent = await server('POST', SET, ww[0] && ww[0].body);
  const c1 = sent.body && sent.body.applied && sent.body.applied.find(a => a.attribute === 'Color1');
  check(sent.status === 200 && c1 && c1.value >= 33410 && c1.value <= 35979, 'the server accepts that write and lands inside "Deep red - Indexing" (33410-35979)', c1 || sent);

  // --- beam: wheel slots, function faders, mode-master report -----------------------
  await tab('beam');
  const slot = q('[data-slot="Gobo1#0#3"]');
  check(!!slot && /Slot 3/.test(slot.textContent) && /G02/.test(slot.textContent) && /image 15020291/.test(slot.textContent), 'Gobo1 slot buttons show slot number, name and the media file name as text', slot && slot.textContent);
  check(!q('[data-fader="Gobo1#0"]') && !!q('[data-fader="Gobo1#2"]') && !!q('[data-fader="Gobo1#4"]'), 'no fader for the plain gobo selection; one for each other function (shake, wheel spin …)');
  click(slot);
  await settled();
  check(same(last(SET).body, { targets: [T('B1')], attribute: 'Gobo1', functionIndex: 0, slot: 3 }), 'a slot tap posts {attribute:Gobo1, functionIndex:0, slot:3} to the fixture that has it', last(SET).body);
  await until('slot 3 shown IN', () => q('[data-slot="Gobo1#0#3"]').getAttribute('aria-pressed') === 'true');
  check(q('[data-slot="Gobo1#0#3"]').getAttribute('aria-pressed') === 'true', 'the slot in the beam is marked (aria-pressed, IN)');
  const rot = q('[data-fader="Gobo1Pos#1"]');
  check(!!rot && /Gobo1PosRotate/.test(rot.getAttribute('aria-label')) && /0–65535/.test(rot.getAttribute('aria-label')), 'Gobo1Pos has a rotate fader labelled by function name and DMX range', rot && rot.getAttribute('aria-label'));
  input(rot, 16384, 'change');
  await settled();
  check(same(last(SET).body, { targets: [T('B1')], attribute: 'Gobo1Pos', functionIndex: 1, fraction: 16384 / 65535 }), 'a function fader writes a fraction within that function', last(SET).body);
  const mm = JSON.parse(last(SET).res).modeMasters || [];
  const notes = mm.map(m => m.note).filter(Boolean);
  check(mm.length > 0, 'the server reported the mode master it had to move (Gobo1 for Gobo1PosRotate)', mm);
  check(notes.length > 0 && notes.every(n => q('[data-cc-status]').textContent.includes(n)), 'the controls show the server\'s own mode-master sentence', [notes, q('[data-cc-status]').textContent]);

  // Two fixture types, two Shutter1 layouts: each gets its own section, and
  // its buttons write to that type only, by its own function index.
  check(!!q('[data-variant="Shutter1#0"]') && !!q('[data-variant="Shutter1#1"]') && /LEDBeam/i.test(q('[data-variant="Shutter1#1"]').textContent), 'a mixed-type attribute is split per channel layout, each named by its fixture type', q('[data-variant="Shutter1#1"]') && q('[data-variant="Shutter1#1"] h5').textContent);
  // I2c2 (§10): the file's "Shutter closed" / "Shutter Open" sets are now the
  // OPEN / CLOSED radio group of the shutter, not plain set buttons.
  click(q('[data-shutter="Shutter1#1#closed"]'));
  await settled();
  check(same(last(SET).body, { targets: [T('L1')], attribute: 'Shutter1', functionIndex: 0, set: 'Shutter closed' }), 'the LEDBeam\'s "Shutter closed" posts to the LEDBeam only, by its own function index', last(SET).body);
  await until('L1 shutter closed', () => { const c = attr('Shutter1').channels.find(x => x.entryId === ids.L1); return c.value <= 31 && c.touched; });
  check(attr('Shutter1').channels.find(x => x.entryId === ids.B1).touched === false, 'the BMFL\'s shutter was not touched');

  // --- control channels: hold to fire, no faders -------------------------------------
  await select('B1');
  await tab('other');
  check(qa('[data-panel="other"] input[type="range"]').length === 0, 'the Control tab has no faders at all');
  const lamp = q('[data-set="Control1#12#Lamp On"]');
  check(!!lamp && /HOLD 0\.75 s/.test(lamp.textContent), 'Lamp On is a HOLD 0.75 s button', lamp && lamp.textContent);
  const lb = posts(SET).length;
  lamp.dispatchEvent(new Event('pointerdown', { bubbles: true, pointerId: 9 }));
  await sleep(400);
  lamp.dispatchEvent(new Event('pointerup', { bubbles: true, pointerId: 9 }));
  click(lamp);
  await sleep(1400);
  check(posts(SET).length === lb, 'a 0.4 s press (and a plain click) sends nothing');
  lamp.dispatchEvent(new Event('pointerdown', { bubbles: true, pointerId: 9 }));
  await sleep(900);
  lamp.dispatchEvent(new Event('pointerup', { bubbles: true, pointerId: 9 }));
  await settled();
  check(posts(SET).length === lb + 1 && same(last(SET).body, { targets: [T('B1')], attribute: 'Control1', functionIndex: 12, set: 'Lamp On' }), 'held 0.9 s (past the 0.75 s hold): posts {attribute:Control1, functionIndex:12, set:"Lamp On"}', last(SET).body);
  await until('Control1 at Lamp On', () => attr('Control1').value >= 130 && attr('Control1').value <= 139);
  check(attr('Control1').value >= 130 && attr('Control1').value <= 139, 'the server holds Control1 inside Lamp On (130-139)', attr('Control1').value);

  // --- toolbar: clear, fan, presets, lowlight ----------------------------------------
  await select('B1', 'B2');
  await tab('position');
  // I2d §15: Fan lives in the action bar's panel (Fan… opens it).
  click(q('[data-fan-open]'));
  await until('the fan panel', () => q('[data-fan-attr]'));
  const fa = q('[data-fan-attr]'), ff = q('[data-fan-fn]');
  fa.value = 'Pan'; fa.dispatchEvent(new Event('change', { bubbles: true }));
  ff.value = '';
  q('[data-fan-from]').value = '0'; q('[data-fan-to]').value = '100'; // shape: Linear is the default choice
  click(q('[data-fan-go]'));
  await settled();
  check(same(last('/api/programmer/fan').body, { attribute: 'Pan', shape: 'linear', from: { fraction: 0 }, to: { fraction: 1 } }), 'Fan posts {attribute, shape, from, to}', last('/api/programmer/fan').body);
  await until('the fan on the server', () => attr('Pan').mixed);
  const pans = attr('Pan').channels.map(c => c.entryId + '=' + c.value);
  check(same(pans, [ids.B1 + '=0', ids.B2 + '=65535']), 'the server fanned Pan in selection order: B1 = 0, B2 = 65535', pans);
  check(/MIXED/.test(q('[data-readout="Pan"]').textContent) && /MIXED/.test(q('[data-markers="Pan"]').textContent), 'a mixed attribute reads MIXED with its range', q('[data-readout="Pan"]').textContent);

  click(q('[data-preset-store="position"]'));
  await until('the preset dialog', () => q('[data-ask-input]'));
  q('[data-ask-input]').value = 'Fan out';
  click(q('[data-ask-ok]'));
  await settled();
  check(same(last('/api/programmer/presets/store').body, { name: 'Fan out', family: 'position' }), 'Store preset posts {name, family}');
  await until('the preset button', () => q('[data-preset]'));
  await PS().act('clear', { scope: 'selection', group: 'position' });
  await until('position cleared', () => !attr('Pan').touched);
  click(q('[data-preset]'));
  await settled();
  check(same(last('/api/programmer/presets/recall').body, { id: q('[data-preset]').getAttribute('data-preset') }), 'tap a preset posts recall {id}');
  await until('the recall report', () => /Recalled \d+ channel/.test(q('[data-cc-status]').textContent));
  check(/Recalled \d+ channel/.test(q('[data-cc-status]').textContent), 'the recall report is shown', q('[data-cc-status]').textContent);
  await until('the recalled fan', () => attr('Pan').mixed);

  click(q('[data-clear-open]')); // I2d §15: Clear… opens the scope chooser
  await until('the clear scopes', () => q('[data-clear-group="position"]'));
  click(q('[data-clear-group="position"]'));
  await settled();
  check(same(last('/api/programmer/clear').body, { scope: 'selection', group: 'position' }), 'Clear Position on selection posts {scope:selection, group:position}');
  click(q('[data-lowlight]'));
  await settled();
  check(same(last('/api/programmer/highlight').body, { lowlight: true }), 'Lowlight toggle posts {lowlight:true}');
  await until('Lowlight ON', () => /ON/.test(q('[data-lowlight]').textContent));
  click(q('[data-lowlight-level-open]')); // I2d §15: the level sits in the action bar's panel
  await until('the lowlight level', () => q('[data-lowlight-percent]'));
  q('[data-lowlight-percent]').value = '35';
  click(q('[data-lowlight-set]'));
  await settled();
  check(same(last('/api/programmer/highlight').body, { lowlightPercent: 35 }), 'Set level posts {lowlightPercent:35}');
  await until('35 % on the server', () => PS().state().highlight.lowlightPercent === 35);

  // --- stale revision: retried once, never more ---------------------------------------
  const stale = await server('POST', SET, { attribute: 'Dimmer', fraction: 0.5 }, { 'X-Benny-Programmer': '1' });
  check(stale.status === 409, 'a write with a stale revision is refused by the server (409)', stale.status);
  let fakes = 1;
  hook = rec => (rec.method === 'POST' && rec.path === SET && fakes-- > 0) ? new Response(JSON.stringify(stale.body), { status: 409, headers: { 'Content-Type': 'application/json' } }) : null;
  await tab('dimmer');
  const db = posts(SET).length;
  input(q('[data-fader="Dimmer#0"]'), 32768, 'change');
  await settled();
  await until('Dimmer 32768', () => attr('Dimmer').value === 32768);
  const tries = posts(SET).slice(db);
  check(tries.length === 2 && tries[0].status === 409 && tries[1].status === 200, 'one stale refusal is retried once and lands', tries.map(t => t.status));
  fakes = 5;
  const db2 = posts(SET).length;
  input(q('[data-fader="Dimmer#0"]'), 1000, 'change');
  await settled();
  await sleep(200);
  hook = null;
  check(posts(SET).length - db2 === 2, 'two stale refusals in a row: two attempts, no more', posts(SET).length - db2);
  check(/another browser/.test(q('[data-cc-status]').textContent), 'and the server\'s sentence is shown', q('[data-cc-status]').textContent);

  // programmer.js: a re-read that left before this browser's own write landed
  // must not wind the revision back (the next write would be stale).
  holdGets = 150;
  const n409 = calls.filter(c => c.status === 409).length;
  await PS().act('set', { attribute: 'Dimmer', fraction: 0.1 });
  await PS().act('set', { attribute: 'Dimmer', fraction: 0.2 });
  await sleep(200);
  await PS().act('set', { attribute: 'Dimmer', fraction: 0.3 }).catch(() => {});
  holdGets = 0;
  await settled();
  check(calls.filter(c => c.status === 409).length === n409, 'a slow re-read never winds the revision back: three writes, no 409', calls.filter(c => c.status === 409).length - n409);

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
