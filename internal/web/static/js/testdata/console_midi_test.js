// console_midi_test.js — MIDI encoders (Console-lite C8) run as the browser
// runs them: the real index.html in minidom.js and the LITERAL api.js,
// ws.js, programmer.js, ui.js, console-controls.js, console-midi.js and
// console.js against a real Benny512 server (internal/web/console_c8_test.go)
// with real vendor GDTF profiles. navigator.requestMIDIAccess is a fake whose
// MIDIInput delivers real MIDI byte arrays (status, data1, data2) through
// onmidimessage, exactly as Web MIDI does. Checks are on the exact
// programmer writes each decode mode produces, on what the server holds
// afterwards, and on the mapping the server persisted (POST /api/midi).
//
// usage: node console_midi_test.js <jsDir> <baseURL> <idsJSON>
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
async function server(method, p, body) {
  const r = await fetch(new URL(p, base), { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text();
  return { status: r.status, body: t ? JSON.parse(t) : null };
}

// --- fake Web MIDI -----------------------------------------------------------
class FakeInput {
  constructor(id, name) { this.id = id; this.name = name; this.type = 'input'; this.state = 'connected'; this.connection = 'closed'; this.onmidimessage = null; }
  open() { this.connection = 'open'; return Promise.resolve(this); }
  // emit delivers one MIDI message the way Chrome does: a MIDIMessageEvent
  // whose data is a Uint8Array of the raw bytes.
  emit(bytes, timeStamp) { if (this.onmidimessage) this.onmidimessage({ data: Uint8Array.from(bytes), timeStamp: timeStamp === undefined ? 0 : timeStamp }); }
}
function fakeMIDI() {
  const access = { inputs: new Map(), outputs: new Map(), sysexEnabled: false, onstatechange: null };
  const calls = [];
  return {
    access, calls,
    navigator: { requestMIDIAccess: opts => { calls.push(opts); return Promise.resolve(access); } },
    plug(inp) { inp.state = 'connected'; access.inputs.set(inp.id, inp); if (access.onstatechange) access.onstatechange({ port: inp }); },
    unplug(inp) { inp.state = 'disconnected'; access.inputs.delete(inp.id); if (access.onstatechange) access.onstatechange({ port: inp }); },
  };
}

// --- a browser context ----------------------------------------------------------
function browser(opts) {
  const { window, document, CustomEvent } = makeWindow();
  document.loadHTML(fs.readFileSync(path.join(jsDir, '..', 'index.html'), 'utf8'));
  window.isSecureContext = opts.secure;
  const url = new URL(base);
  const calls = [];
  const sandbox = {
    console, setTimeout, clearTimeout, setInterval: () => 0, clearInterval() {}, Promise, JSON, URL, URLSearchParams, btoa, Math, Date, Uint8Array,
    WebSocket, window, document, Event, CustomEvent, navigator: opts.navigator,
    location: opts.location || { protocol: url.protocol, host: url.host, hostname: url.hostname, href: base + '/' },
    localStorage: window.localStorage, getComputedStyle: window.getComputedStyle,
    fetch: async (p, o) => {
      o = o || {};
      const rec = { method: o.method || 'GET', path: String(p), body: o.body ? JSON.parse(o.body) : undefined };
      calls.push(rec);
      const res = await fetch(new URL(p, base), o);
      rec.status = res.status;
      rec.res = await res.clone().text();
      return res;
    },
  };
  const ctx = vm.createContext(sandbox);
  for (const f of opts.scripts) vm.runInContext(fs.readFileSync(path.join(jsDir, f), 'utf8'), ctx, { filename: f });
  return { ctx, document, calls, get: expr => vm.runInContext(expr, ctx) };
}

(async () => {
  // --- 1. not a secure context: plain words, and Web MIDI is never asked ----------
  {
    const midi = fakeMIDI();
    const b = browser({ secure: false, navigator: midi.navigator, scripts: ['console-midi.js'], location: { protocol: 'http:', host: '192.168.1.20:8080', hostname: '192.168.1.20', href: 'http://192.168.1.20:8080/' } });
    const region = b.document.createElement('section');
    b.document.body.appendChild(region);
    b.get('ConsoleMIDI').mount(region);
    await until('the insecure message', () => region.querySelector('[data-midi-phase="insecure"]'));
    const words = region.textContent;
    check(/not secure/.test(words) && /http:\/\/192\.168\.1\.20:8080/.test(words) && /http:\/\/localhost:8080\//.test(words) && /Plug the MIDI controller into the station computer/.test(words),
      'over plain http on the LAN the strip says in words that MIDI cannot work here, names this address and the station\'s http://localhost:8080/', words);
    check(!region.querySelector('[data-midi-connect]'), 'there is no Connect MIDI button where MIDI cannot work');
    await b.get('ConsoleMIDI').connect();
    check(midi.calls.length === 0, 'and requestMIDIAccess is never called, not even by connect()', midi.calls.length);
  }
  // --- 2. secure, but a browser without Web MIDI (Safari) ------------------------------
  {
    const b = browser({ secure: true, navigator: {}, scripts: ['console-midi.js'] });
    const region = b.document.createElement('section');
    b.document.body.appendChild(region);
    b.get('ConsoleMIDI').mount(region);
    await until('the unsupported message', () => region.querySelector('[data-midi-phase="unsupported"]'));
    check(/Chrome and Edge support it; Safari does not/.test(region.textContent), 'a browser with no Web MIDI is told Chrome and Edge have it and Safari does not', region.textContent);
  }

  // --- 3. the station's own browser -------------------------------------------------------
  const midi = fakeMIDI();
  const B = browser({ secure: true, navigator: midi.navigator, scripts: ['api.js', 'ws.js', 'programmer.js', 'ui.js', 'console-controls.js', 'console-midi.js', 'console.js'] });
  const { document, calls, get } = B;
  const q = sel => document.querySelector(sel);
  const qa = sel => document.querySelectorAll(sel);
  const click = el => el.dispatchEvent(new Event('click', { bubbles: true }));
  const PS = () => get('ProgrammerSync');
  const posts = p => calls.filter(c => c.method === 'POST' && c.path === p);
  const SET = '/api/programmer/set';
  const MIDI = '/api/midi';
  const T = n => ({ entryId: ids[n], cell: '' });
  const held = async (name, n) => { const s = (await server('GET', '/api/programmer')).body; for (const g of s.groups) for (const a of g.attributes) if (a.attribute === name) return a.channels.find(c => c.entryId === ids[n]).value; return null; };
  const settled = async () => { await sleep(90); await until('writes to settle', () => !calls.some(c => c.method === 'POST' && c.status === undefined)); await sleep(40); };
  async function select(...names) {
    await PS().act('select', { action: 'set', targets: names.map(n => T(n)) });
    await until('the selection ' + names.join('+'), () => PS().state().selection.length === names.length && q('[data-cc-body]').textContent.length > 0);
    await sleep(80);
  }
  async function tab(group) {
    await until('the ' + group + ' tab', () => q('[data-tab-group="' + group + '"]'));
    click(q('[data-tab-group="' + group + '"]'));
    await until('the ' + group + ' panel', () => q('[data-panel="' + group + '"]'));
  }
  const encAttr = i => (q('[data-enc-attr="' + i + '"]') || { textContent: '(no cell)' }).textContent;
  const lastMIDIPost = () => { const l = posts(MIDI); return l[l.length - 1]; };
  const xt = new FakeInput('in-1', 'X-TOUCH MINI');
  let ts = 0; // message time stamps (ms), far apart unless a check says otherwise
  const send = (bytes, at) => { ts += 1000; xt.emit(bytes, at === undefined ? ts : at); };
  // turn: one message, then the write it causes (or null), settled.
  async function turn(bytes) {
    const before = posts(SET).length;
    send(bytes);
    await settled();
    const w = posts(SET).slice(before);
    return w.length ? w[w.length - 1].body : null;
  }

  await until('the WebSocket and the programmer to load', () => PS().state());
  get('ConsoleScreen').onEnterScreen();
  await until('the MIDI strip to mount in the Controls region', () => q('[data-controls-region] [data-midi-region] [data-midi-connect]'));
  await sleep(50);
  check(midi.calls.length === 0, 'mounting asks nothing of Web MIDI: access is requested only on Connect MIDI', midi.calls.length);
  check(/Not connected/.test(q('[data-midi-phase]').textContent) && /plugged into the computer running this browser/.test(q('[data-midi-phase]').textContent), 'before connecting the strip says so, and that the controller must be plugged into this computer', q('[data-midi-phase]').textContent);

  midi.plug(xt);
  click(q('[data-midi-connect]'));
  await until('MIDI to connect', () => q('[data-midi-phase="ready"]') && /Using X-TOUCH MINI/.test(q('[data-midi-phase]').textContent));
  check(midi.calls.length === 1 && same(midi.calls[0], { sysex: false }), 'Connect MIDI asks once, without SysEx', midi.calls);
  check(typeof xt.onmidimessage === 'function' && xt.connection === 'open', 'the only input is picked and opened');

  await select('B1', 'B2');
  await tab('position');
  await until('the encoder strip on Position', () => encAttr(1) === 'Pan');
  check(encAttr(1) === 'Pan' && encAttr(2) === 'Tilt' && encAttr(3) === 'PositionMSpeed' && /nothing on this bank/.test(encAttr(4)), 'Position: encoder 1 = Pan, 2 = Tilt, 3 = PositionMSpeed, 4 = nothing', [1, 2, 3, 4].map(encAttr));
  check(qa('[data-enc-tags="1"]')[0] && /not bound/.test(q('[data-enc-tags="1"]').textContent), 'an encoder with no MIDI binding says "not bound"');
  check(/Output is not armed/.test(q('[data-midi-strip]').textContent), 'the strip says output is not armed (moves change the programmer only)');

  // --- learn: each mode from what the encoder sends --------------------------------------
  async function learnTurn(enc, right, left) {
    click(q('[data-midi-learn-turn="' + enc + '"]'));
    check(/turn it RIGHT/.test(q('[data-midi-learn-words]').textContent), 'learn ' + enc + ': asks to turn RIGHT', q('[data-midi-learn-words]').textContent);
    const n = posts(MIDI).length;
    right.forEach(b => send(b));
    if (left) {
      check(/now turn it LEFT/.test((q('[data-midi-learn-words]') || { textContent: '' }).textContent), 'learn ' + enc + ': then LEFT');
      left.forEach(b => send(b));
    }
    await settled();
    return posts(MIDI).length > n ? lastMIDIPost() : null;
  }
  const CC = (n, v, ch) => [0xB0 | ((ch || 1) - 1), n, v];
  let p = await learnTurn(1, [CC(16, 1), CC(16, 1), CC(16, 2)], [CC(16, 127), CC(16, 127), CC(16, 126)]);
  check(p && p.status === 200 && same(p.body.profile.encoders[0], { channel: 1, control: 16, mode: 'twos', invert: false, push: null }) && p.body.profile.deviceName === 'X-TOUCH MINI',
    'learn: right 1,1,2 then left 127,127,126 binds encoder 1 = channel 1 CC 16 two\'s complement, saved with the device name', p && p.body);
  p = await learnTurn(2, [CC(17, 65), CC(17, 65), CC(17, 66)], [CC(17, 63), CC(17, 62), CC(17, 63)]);
  check(p && p.body.profile.encoders[1].mode === 'offset' && p.body.profile.encoders[1].control === 17, 'learn: 65,65,66 / 63,62,63 = binary offset', p && p.body.profile.encoders[1]);
  p = await learnTurn(3, [CC(18, 1), CC(18, 1), CC(18, 1)], [CC(18, 65), CC(18, 65), CC(18, 66)]);
  check(p && p.body.profile.encoders[2].mode === 'signbit', 'learn: 1,1,1 / 65,65,66 = signed bit (65 is −1 there, not two\'s complement −63)', p && p.body.profile.encoders[2]);
  p = await learnTurn(4, [CC(19, 40), CC(19, 41), CC(19, 42)], [CC(19, 41), CC(19, 40), CC(19, 39)]);
  check(p && p.body.profile.encoders[3].mode === 'absolute', 'learn: 40,41,42 / 41,40,39 = absolute', p && p.body.profile.encoders[3]);
  p = await learnTurn(5, [CC(1, 64), CC(33, 0), CC(33, 9)]);
  check(p && same(p.body.profile.encoders[4], { channel: 1, control: 1, mode: 'absolute14', invert: false, push: null }), 'learn: CC 1 with CC 33 (its n+32 pair) = 14-bit absolute', p && p.body.profile.encoders[4]);
  p = await learnTurn(6, [CC(20, 1), CC(20, 1), CC(20, 1)], [CC(20, 1), CC(20, 1), CC(20, 1)]);
  check(p === null && /NOT bound/.test(q('[data-midi-say]').textContent), 'learn: bytes that do not tell the kind of encoder are refused, not guessed (nothing saved)', q('[data-midi-say]').textContent);
  // push and bank buttons
  click(q('[data-midi-learn-push="1"]'));
  send(CC(16, 1)); // a turn of the encoder is not a press
  send([0x90, 32, 127]);
  await settled();
  check(same(lastMIDIPost().body.profile.encoders[0].push, { kind: 'note', channel: 1, number: 32 }), 'learn push: Note On 32 is encoder 1\'s push (a turn during push learn is ignored)', lastMIDIPost().body.profile.encoders[0]);
  click(q('[data-midi-learn-bankNext]'));
  send([0x90, 33, 100]);
  await settled();
  click(q('[data-midi-learn-bankPrev]'));
  send(CC(50, 127));
  await settled();
  check(same(lastMIDIPost().body.profile.bankNext, { kind: 'note', channel: 1, number: 33 }) && same(lastMIDIPost().body.profile.bankPrev, { kind: 'cc', channel: 1, number: 50 }), 'learn: bank forward = note 33, bank back = CC 50', lastMIDIPost().body.profile);
  // encoder 6 by hand
  q('[data-midi-ch="6"]').value = '1';
  q('[data-midi-cc="6"]').value = '21';
  q('[data-midi-mode="6"]').value = 'twos';
  click(q('[data-midi-apply="6"]'));
  await settled();
  check(same(lastMIDIPost().body.profile.encoders[5], { channel: 1, control: 21, mode: 'twos', invert: false, push: null }), 'encoder 6 set by hand: channel 1, CC 21, two\'s complement', lastMIDIPost().body.profile.encoders[5]);
  const savedNow = (await server('GET', MIDI)).body;
  check(savedNow.persisted === true && same(savedNow.profile, lastMIDIPost().body.profile), 'the server holds exactly the last mapping and says it is persisted', savedNow);

  // --- decode modes → exact programmer writes -----------------------------------------------------
  // Pan/Tilt are 16-bit (one step = 655 = 1 %), PositionMSpeed 8-bit (3).
  let w = await turn(CC(16, 1));
  check(same(w, { targets: [T('B1'), T('B2')], attribute: 'Pan', functionIndex: 0, dmx: 32768 + 655 }), 'twos: CC 16 value 1 → Pan +1 step: one write {targets B1+B2, Pan, functionIndex 0, dmx 33423}', w);
  w = await turn(CC(16, 3));
  check(w && w.dmx === 33423 + 3 * 655, 'twos: value 3 → +3 steps (35388)', w);
  w = await turn(CC(16, 126));
  check(w && w.dmx === 35388 - 2 * 655, 'twos: value 126 → −2 steps (34078)', w);
  await until('the server to hold Pan 34078 on both', async () => (await held('Pan', 'B1')) === 34078 && (await held('Pan', 'B2')) === 34078);
  check((await held('Pan', 'B1')) === 34078 && (await held('Pan', 'B2')) === 34078, 'the server holds Pan 34078 on B1 and B2');
  w = await turn(CC(17, 66));
  check(same(w, { targets: [T('B1'), T('B2')], attribute: 'Tilt', functionIndex: 0, dmx: 32768 + 2 * 655 }), 'offset: CC 17 value 66 → Tilt +2 steps (34078)', w);
  w = await turn(CC(17, 63));
  check(w && w.dmx === 34078 - 655, 'offset: value 63 → −1 step (33423)', w);
  w = await turn(CC(18, 2));
  check(same(w, { targets: [T('B1'), T('B2')], attribute: 'PositionMSpeed', functionIndex: 0, dmx: 6 }), 'signbit: CC 18 value 2 → PositionMSpeed (8-bit) +2 steps of 3 = 6', w);
  w = await turn(CC(18, 65));
  check(w && w.dmx === 3, 'signbit: value 65 → −1 step (3)', w);
  w = await turn(CC(18, 64 + 10));
  check(w && w.dmx === 0, 'signbit: value 74 → −10 steps, stopped at the bottom of the function (0)', w);
  w = await turn(CC(18, 64));
  check(w === null, 'signbit: value 64 (minus zero) writes nothing', w);

  // absolute and 14-bit on the Colour tab: enc 4 = ColorMacro1, enc 5 = ColorMixMSpeed.
  // I2c2 (§9): the Colour tab draws its open mode's cards (Mix on a BMFL: C, M, Y)
  // then the other colour channels, and the encoders follow exactly that order,
  // so enc 4 / 5 are ColorMacro1 / ColorMixMSpeed (8-bit, one function each: the
  // same 3-count steps the CMY checks used).
  await tab('colour');
  await until('the strip to follow the Colour tab', () => encAttr(1) === 'ColorSub_C');
  const cards = qa('[data-panel="colour"] [data-attr]').map(c => c.getAttribute('data-attr'));
  check(cards.length > 0 && same([1, 2, 3, 4, 5, 6, 7, 8].map(encAttr).slice(0, cards.length), cards.slice(0, 8)), 'Colour: the encoders take the tab\'s attributes in the order the cards are drawn', [[1, 2, 3, 4, 5, 6, 7, 8].map(encAttr), cards]);
  check(encAttr(4) === 'ColorMacro1' && encAttr(5) === 'ColorMixMSpeed', 'Colour on a BMFL: encoder 4 = ColorMacro1, 5 = ColorMixMSpeed');
  w = await turn(CC(19, 50));
  check(w === null, 'absolute: the first value only says where the control is (no write)', w);
  w = await turn(CC(19, 53));
  check(same(w, { targets: [T('B1'), T('B2')], attribute: 'ColorMacro1', functionIndex: 0, dmx: 9 }), 'absolute: 50 → 53 = +3 steps (ColorMacro1 9)', w);
  w = await turn(CC(19, 51));
  check(w && w.dmx === 3, 'absolute: 53 → 51 = −2 steps (3)', w);
  w = await turn(CC(19, 120));
  check(w && w.dmx === 0, 'absolute wrap guard: 51 → 120 is read as −59 (the short way round), stopped at 0 — not +69', w);
  w = await turn(CC(19, 127));
  check(w && w.dmx === 21, 'absolute: 120 → 127 = +7 steps (21)', w);
  w = await turn(CC(19, 1));
  check(w && w.dmx === 27, 'absolute wrap guard: 127 → 1 is +2 (an endless encoder passing the top), 27', w);
  w = await turn(CC(19, 127));
  check(w && w.dmx === 21, 'absolute wrap guard: 1 → 127 is −2 (21)', w);
  w = await turn(CC(19, 127));
  check(w && w.dmx === 24, 'absolute: 127 again (pinned at the top) = +1 step (24)', w);
  // 14-bit: 128 fine values = one step (3 on an 8-bit channel).
  send(CC(1, 64)); send(CC(33, 0)); await settled();
  w = await turn(CC(33, 64));
  check(same(w, { targets: [T('B1'), T('B2')], attribute: 'ColorMixMSpeed', functionIndex: 0, dmx: 2 }), '14-bit: baseline 64/0, then LSB 64 alone = +64/128 step → ColorMixMSpeed 1.5 → 2', w);
  let before = posts(SET).length;
  send(CC(1, 65)); send(CC(33, 0)); await settled();
  w = posts(SET).slice(before);
  check(w.length === 1 && w[0].body.dmx === 3, '14-bit: MSB 65 + LSB 0 (8320) = +0.5 step → 3, one write', w.map(x => x.body));
  before = posts(SET).length;
  send(CC(1, 64)); send(CC(33, 127)); await settled();
  check(posts(SET).length === before, '14-bit: a crossing down to 64/127 (8319, −1/128 step) writes nothing — the MSB is not read alone as a jump to 8192', posts(SET).slice(before).map(x => x.body));
  before = posts(SET).length;
  send(CC(1, 66)); await sleep(60); await settled();
  w = posts(SET).slice(before);
  check(w.length === 1 && w[0].body.dmx === 6, '14-bit: a lone MSB 66 is read after 20 ms with LSB 0 (8448 = +129/128 step) → 6', w.map(x => x.body));
  await until('the server to hold ColorMixMSpeed 6', async () => (await held('ColorMixMSpeed', 'B2')) === 6);

  // --- push toggles fine for that encoder ----------------------------------------------------
  await tab('position');
  await until('Position again', () => encAttr(1) === 'Pan');
  send([0x90, 32, 127]); send([0x80, 32, 0]); await settled();
  check(q('[data-enc-fine="1"]').getAttribute('aria-pressed') === 'true' && /FINE/.test(q('[data-enc-tags="1"]').textContent) && !/FINE/.test(q('[data-enc-tags="2"]').textContent),
    'pushing encoder 1 (Note On 32) turns FINE on for encoder 1 only; the Note Off changes nothing');
  await sleep(650); // a new gesture: read Pan from the programmer again
  w = await turn(CC(16, 1));
  // I2c: fine is 1:10 of a normal step over the whole value (owner rule,
  // 2026-10-08), superseding 1/4096 (16): 655 / 10 -> 66.
  check(w && w.dmx === 34078 + 66, 'fine: one step on a 16-bit Pan is a tenth of 655 = 66 (34144)', w);
  send([0x90, 32, 127]); await settled();
  check(q('[data-enc-fine="1"]').getAttribute('aria-pressed') === 'false', 'pushing again turns fine off');
  click(q('[data-enc-fine="2"]'));
  check(q('[data-enc-fine="2"]').getAttribute('aria-pressed') === 'true', 'the on-screen Fine button does the same for encoder 2');
  click(q('[data-enc-fine="2"]'));

  // --- acceleration (off by default) ----------------------------------------------------------
  check(lastMIDIPost().body.profile.acceleration.enabled === false, 'acceleration is off by default');
  q('[data-midi-accel]').checked = true;
  q('[data-midi-accel-ms]').value = '100';
  q('[data-midi-accel-strength]').value = '5';
  click(q('[data-midi-accel-apply]'));
  await settled();
  check(same(lastMIDIPost().body.profile.acceleration, { enabled: true, thresholdMs: 100, strength: 5 }), 'acceleration on: 100 ms, strength 5, saved', lastMIDIPost().body.profile.acceleration);
  await sleep(650);
  const t0 = 1e9;
  before = posts(SET).length;
  xt.emit(CC(18, 1), t0); await settled();
  xt.emit(CC(18, 1), t0 + 5); await settled();
  xt.emit(CC(18, 1), t0 + 2000); await settled();
  w = posts(SET).slice(before).map(x => x.body.dmx);
  check(same(w, [3, 3 + 18, 3 + 18 + 3]), 'acceleration: a click 5 ms after the last moves 6× (18), one 2 s later moves 1× (3)', w);
  q('[data-midi-accel]').checked = false;
  click(q('[data-midi-accel-apply]'));
  await settled();

  // --- mixed selection: same steps from each fixture's own value, clamped per function --------
  await select('B1', 'L1');
  await server('POST', SET, { targets: [T('B1')], attribute: 'Pan', dmx: 1000 });
  await server('POST', SET, { targets: [T('L1')], attribute: 'Pan', dmx: 64000 });
  await until('the browser to see the mixed Pan', () => { const a = PS().state().groups.find(g => g.group === 'position').attributes.find(x => x.attribute === 'Pan'); return a && a.mixed; });
  await tab('position');
  await sleep(650);
  before = posts(SET).length;
  send(CC(16, 2)); await settled();
  let mixed = posts(SET).slice(before).map(x => x.body);
  check(same(mixed, [
    { targets: [T('B1')], attribute: 'Pan', functionIndex: 0, dmx: 2310 },
    { targets: [T('L1')], attribute: 'Pan', functionIndex: 0, dmx: 65310 },
  ]), 'mixed: +2 steps moves B1 1000 → 2310 and L1 64000 → 65310 (same delta from each own value, one write each)', mixed);
  before = posts(SET).length;
  send(CC(16, 1)); await settled();
  mixed = posts(SET).slice(before).map(x => x.body);
  check(same(mixed, [
    { targets: [T('B1')], attribute: 'Pan', functionIndex: 0, dmx: 2965 },
    { targets: [T('L1')], attribute: 'Pan', functionIndex: 0, dmx: 65535 },
  ]), 'mixed: one more step — B1 2965, L1 stops at the top of its Pan function (65535)', mixed);
  await until('the server to hold the mixed nudge', async () => (await held('Pan', 'B1')) === 2965 && (await held('Pan', 'L1')) === 65535);
  check(/MIXED/.test(q('[data-enc-val="1"]').textContent), 'the strip reads MIXED for a mixed attribute', q('[data-enc-val="1"]').textContent);

  // --- banks on a tab with more attributes than encoders --------------------------------------
  await select('B1', 'B2');
  await tab('beam');
  await until('the Beam strip', () => encAttr(1) === 'Effects1Rate');
  const beam = PS().state().groups.find(g => g.group === 'beam').attributes.map(a => a.attribute);
  const pages = Math.ceil(beam.length / 8);
  check(pages >= 2 && new RegExp('Bank 1 of ' + pages).test(q('[data-midi-bank-words]').textContent), 'Beam has ' + beam.length + ' attributes: Bank 1 of ' + pages, q('[data-midi-bank]') && q('[data-midi-bank]').textContent);
  send([0x90, 33, 127]); await settled();
  check(new RegExp('Bank 2 of ' + pages).test(q('[data-midi-bank-words]').textContent) && encAttr(1) === beam[8], 'bank forward (note 33): Bank 2, encoder 1 = ' + beam[8], [q('[data-midi-bank-words]').textContent, encAttr(1)]);
  const k = beam.indexOf('Shutter1');
  check(k >= 8 && k < 16 && encAttr(k - 8 + 1) === 'Shutter1', 'Shutter1 is on bank 2, encoder ' + (k - 7), k);
  if (k - 8 === 5) {
    // Shutter1 on a BMFL sits at 32 in "Shutter1" (0–63); Strobe starts at 64.
    w = await turn(CC(21, 20));
    check(same(w, { targets: [T('B1'), T('B2')], attribute: 'Shutter1', functionIndex: 0, dmx: 63 }), 'Shutter1 +20 steps stops at 63, the top of the Open function — it never slides into Strobe', w);
  }
  send(CC(50, 0)); await settled();
  check(new RegExp('Bank 2 of ').test(q('[data-midi-bank-words]').textContent), 'bank back button released (CC 50 value 0) does nothing');
  send(CC(50, 127)); await settled();
  check(/Bank 1 of /.test(q('[data-midi-bank-words]').textContent) && encAttr(1) === 'Effects1Rate', 'bank back (CC 50 127) returns to bank 1');
  click(q('[data-midi-bank-next]'));
  check(/Bank 2 of /.test(q('[data-midi-bank-words]').textContent), 'the on-screen Bank ▶ button pages too');
  click(q('[data-midi-bank-prev]'));

  // --- Control / Other: no encoders -------------------------------------------------------------
  await tab('other');
  await until('the Control strip', () => q('[data-midi-why]') && /no encoders/.test(q('[data-midi-why]').textContent));
  before = posts(SET).length;
  send(CC(16, 5)); await settled();
  check(posts(SET).length === before && !q('[data-enc="1"]'), 'Control / Other has no encoder cells and a turn writes nothing');

  // --- hot-plug ------------------------------------------------------------------------------------
  await tab('position');
  midi.unplug(xt);
  await until('the unplugged words', () => /X-TOUCH MINI was unplugged/.test(q('[data-midi-phase]').textContent));
  check(xt.onmidimessage === null, 'unplugging detaches the input');
  const nano = new FakeInput('in-2', 'nanoKONTROL2');
  midi.plug(nano);
  await sleep(30);
  check(/X-TOUCH MINI was unplugged/.test(q('[data-midi-phase]').textContent) && typeof nano.onmidimessage !== 'function', 'another device plugged in is listed, not silently taken over');
  const back = new FakeInput('in-3', 'X-TOUCH MINI');
  midi.plug(back);
  await until('the device to come back', () => /Using X-TOUCH MINI/.test(q('[data-midi-phase]').textContent));
  check(typeof back.onmidimessage === 'function' && qa('[data-midi-input] option').length === 2, 'the same device plugged back in (new id, same name) is picked up again; both devices are in the list');
  await sleep(650);
  before = posts(SET).length;
  back.emit(CC(16, 1), 5e9); await settled();
  const again = posts(SET).slice(before).map(x => x.body);
  check(again.length === 2 && again.every(b => b.attribute === 'Pan' && b.targets.length === 1), 'and its encoders drive the programmer again (Pan on B1 and B2, which now differ: one write each)', again);

  // --- output: still disarmed ------------------------------------------------------------------------
  const out = (await server('GET', '/api/programmer')).body.output;
  check(out.live === false, 'output stayed disarmed through every encoder move (the Go side counts the wire)', out);

  console.log(failures ? failures + ' FAILED' : 'ALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.log('  FAIL  harness error: ' + (e && e.stack || e)); process.exit(1); });
