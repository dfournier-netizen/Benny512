// console-midi.js — MIDI ENCODERS for the Console (Console-lite C8).
//
// A MIDI controller's endless encoders drive the attributes of the
// Console's CURRENT attribute tab, in the order the cards are drawn:
// Position → encoder 1 Pan, encoder 2 Tilt, …; Colour → its attributes in
// order (R/G/B on an LED fixture, C/M/Y on a CMY one). A tab with more
// attributes than encoders has banks (on screen, and optionally two MIDI
// buttons). The strip under the Controls heading names, in words, what
// every encoder controls right now.
//
// WHERE IT WORKS. Web MIDI (navigator.requestMIDIAccess) exists only in a
// secure context. The station's own browser at http://localhost is one; a
// phone or tablet that opened the station's LAN address over plain http is
// NOT, and the browser hides MIDI there. Chrome and Edge have Web MIDI;
// Safari does not. The controller must be plugged into the computer running
// the browser it is used with. The strip says all of this in plain words
// and never calls requestMIDIAccess where it cannot work. Access is asked
// for only when the operator presses "Connect MIDI".
//
// READING THE BYTES (per encoder, set by Learn or by hand):
//   twos      relative, two's complement: 1..63 = up 1..63, 127..65 = down
//             1..63 (value − 128), 64 = down 64, 0 = nothing;
//   offset    relative, binary offset: value − 64 (65 = up 1, 63 = down 1);
//   signbit   relative, signed bit: bit 6 = down, bits 0..5 = how many
//             (1 = up 1, 65 = down 1);
//   absolute  a 0..127 control turned into steps by the difference from the
//             value before. The first value only says where the control
//             is (no move). Wrap guard: a jump of more than 64 is an endless
//             encoder passing 127→0 (or back), read as the short way round.
//             An encoder pinned at 0 or 127 that sends the same end value
//             again moves one step that way;
//   absolute14 a 14-bit pair: MSB on controller n (0..31), LSB on n+32
//             (MIDI 1.0 control-change pairs). An MSB resets the LSB to 0
//             (MIDI 1.0), so an MSB is held up to 20 ms for its LSB; a lone
//             LSB is a fine move. 128 fine values = one step; the same wrap
//             guard at half the range.
// A push (a note, or a control change above 0) toggles FINE for its encoder.
// Acceleration is off by default; when on, steps that arrive closer
// together than the threshold are multiplied, up to 1.5× (strength 1) …
// 6× (strength 5) as the gap shrinks to 5 ms.
//
// HOW FAR ONE STEP MOVES. Normal: 1 % of the channel's full range (8-bit:
// 3 values; 16-bit: 655). Fine: 1/4096 of the range, at least one value
// (8-bit: 1; 16-bit: 16). That is the fader's ± step in normal mode; fine
// is coarser than the fader's 1-value fine on a 16-bit channel so a fine
// turn still visibly moves a beam.
//
// HOW A TURN IS WRITTEN. A turn is a NUDGE of the value the programmer
// already holds, sent as absolute DMX values through the Console's own
// throttled writer (ConsoleControls.lane: latest value wins, ≤ 30 writes a
// second per lane, the final value always last). Ticks closer together than
// 600 ms are one gesture: its starting values are read once from the
// programmer and the gesture accumulates on them, so the server echoing
// each write back never drags the encoder backwards.
//
// MIXED SELECTIONS — THE RULE. Every selected fixture (or cell) moves by the
// same number of steps from ITS OWN value (a fan stays a fan), and each
// stops at the edges of the channel function it was in when the turn
// started (Shutter "Open" never slides into "Strobe"; a gobo wheel's index
// range never runs into its spin range). Stopping at that edge is a clamp,
// and it is acceptable here where it is not elsewhere: the operator asked
// to nudge relative to where each fixture already is, not for a value the
// code would be inventing — the edge is the nearest value to the requested
// nudge that keeps the fixture doing what it was doing. A channel with no
// value set and no stated default is NOT nudged (there is nothing to nudge
// from) and the strip says so. The step is per channel resolution, so an
// 8-bit and a 16-bit fixture move the same share of their range.
//
// Control / Other has no encoders, for the reason it has no faders: a turn
// would pass through reset and lamp ranges.
//
// OUTPUT. Encoders change the programmer like the faders do. Nothing reaches
// the rig unless output is ARMED (the engine's rule, not this file's).
//
// The mapping (one profile, remembered with the device name it was learned
// on) is saved on the station: GET/POST /api/midi (internal/web/midi.go).
const ConsoleMIDI = (() => {
  const MODE_LABEL = {
    twos: 'Relative · two’s complement (1–63 up, 65–127 down)',
    offset: 'Relative · binary offset (64 ± n)',
    signbit: 'Relative · signed bit (bit 6 = down)',
    absolute: 'Absolute 0–127 (read as steps)',
    absolute14: 'Absolute 14-bit pair (CC n + CC n+32)',
  };
  const MODES = Object.keys(MODE_LABEL);
  const GROUP_LABEL = { dimmer: 'Dimmer', position: 'Position', colour: 'Colour', beam: 'Beam', focus: 'Focus', shaper: 'Shaper', other: 'Control / Other' };
  const IDLE_MS = 600;     // ticks closer than this are one gesture
  const LEARN_N = 3;       // messages per learn phase
  const MSB_WAIT_MS = 20;  // a 14-bit MSB waits this long for its LSB
  const ACCEL_MAX = [1, 1.5, 2, 3, 4, 6]; // by strength 1..5
  const ACCEL_MIN_MS = 5;

  const st = {
    region: null, els: {}, loaded: false, loading: null,
    profile: null, persisted: false, loadError: '',
    phase: 'idle', why: '', access: null, inputs: [], input: null, want: null, lost: '',
    rt: [], bank: {}, gestures: new Map(), learn: null, stripSig: '', note: null,
  };

  // --- DOM helpers ------------------------------------------------------------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const k of Object.keys(props)) {
        const v = props[k];
        if (v === undefined || v === null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = String(v);
        else if (k.slice(0, 2) === 'on') el.addEventListener(k.slice(2), v);
        else el.setAttribute(k, v === true ? '' : String(v));
      }
    }
    for (const c of [].concat(...kids)) {
      if (c === null || c === undefined || c === false) continue;
      el.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return el;
  }
  function clear(el) { while (el && el.firstChild) el.removeChild(el.firstChild); }
  function btn(label, attrs, onclick, cls) {
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn b5-btn--sm' + (cls ? ' ' + cls : '') }, attrs || {}), label);
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function tag(word, tone) { return h('span', { class: 'b5-pill b5-pill--tag' + (tone ? ' b5-pill--' + tone : ''), text: word }); }
  const now = () => Date.now();
  const stamp = () => (typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now());

  // --- the profile ---------------------------------------------------------------
  function defaults() {
    return { deviceName: '', encoders: [null, null, null, null, null, null, null, null], acceleration: { enabled: false, thresholdMs: 100, strength: 2 }, bankPrev: null, bankNext: null };
  }
  const clone = o => JSON.parse(JSON.stringify(o));
  function count() { return st.profile ? st.profile.encoders.length : 8; }
  function rt(i) {
    if (!st.rt[i]) st.rt[i] = { last7: null, last14: null, msb: null, msbTimer: null, lastAt: null, fine: false };
    return st.rt[i];
  }

  async function http(method, path, body) {
    const opts = { method, headers: {} };
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const res = await fetch(path, opts);
    const text = await res.text();
    let data = null;
    if (text) { try { data = JSON.parse(text); } catch (_) { data = text; } }
    if (!res.ok) throw new Error((data && data.error) || ('HTTP ' + res.status));
    return data;
  }
  function load() {
    if (st.loading) return st.loading;
    st.loading = http('GET', '/api/midi').then(r => {
      st.profile = r.profile || defaults();
      st.persisted = !!r.persisted;
      st.loadError = r.loadError || '';
      st.loaded = true;
    }).catch(e => {
      st.profile = st.profile || defaults();
      st.loadError = 'The saved MIDI mapping could not be read (' + e.message + ').';
      st.loaded = true;
    }).then(() => render());
    return st.loading;
  }
  async function save(what) {
    const prof = clone(st.profile);
    if (st.input) prof.deviceName = st.input.name || '';
    try {
      const r = await http('POST', '/api/midi', { profile: prof });
      st.profile = r.profile || prof;
      st.persisted = !!r.persisted;
      st.loadError = '';
      say(what + (st.persisted ? ' Saved on this station.' : ' Kept until Benny512 closes (demo mode saves nothing).'));
    } catch (e) {
      say(what + ' NOT saved: ' + e.message, 'error');
    }
    renderSetup();
    refresh();
  }

  // --- where MIDI can work ---------------------------------------------------------
  function secure() {
    if (typeof window !== 'undefined' && window && typeof window.isSecureContext === 'boolean') return window.isSecureContext;
    const loc = typeof location !== 'undefined' ? location : {};
    const host = String(loc.hostname || loc.host || '').replace(/:\d+$/, '');
    return loc.protocol === 'https:' || host === 'localhost' || host === '127.0.0.1' || host === '[::1]';
  }
  function hasWebMIDI() { return typeof navigator !== 'undefined' && !!navigator && typeof navigator.requestMIDIAccess === 'function'; }
  function localURL() {
    const loc = typeof location !== 'undefined' ? location : {};
    const port = /:(\d+)$/.exec(String(loc.host || ''));
    return 'http://localhost' + (port ? ':' + port[1] : '') + '/';
  }
  function insecureWords() {
    const loc = typeof location !== 'undefined' ? location : {};
    return 'MIDI cannot work on this page. Browsers allow MIDI only on a secure page, and this page was opened over the network at ' +
      (loc.protocol || 'http:') + '//' + (loc.host || 'this address') + ', which browsers treat as not secure. ' +
      'Plug the MIDI controller into the station computer and use the browser there, at ' + localURL() + ' (Chrome or Edge).';
  }
  const PLUG_WORDS = 'The controller must be plugged into the computer running this browser.';

  // --- connecting ------------------------------------------------------------------
  async function connect() {
    if (!secure()) { st.phase = 'insecure'; render(); return; }
    await load();
    if (!hasWebMIDI()) { st.phase = 'unsupported'; render(); return; }
    st.phase = 'asking';
    render();
    try {
      const access = await navigator.requestMIDIAccess({ sysex: false });
      st.access = access;
      st.phase = 'ready';
      access.onstatechange = () => relist();
      relist();
    } catch (e) {
      st.phase = 'denied';
      st.why = e ? [e.name, e.message].filter(Boolean).join(': ') : '';
      render();
    }
  }
  function listInputs() {
    const out = [];
    if (st.access && st.access.inputs) st.access.inputs.forEach(i => { if (i.state !== 'disconnected') out.push(i); });
    return out;
  }
  // relist follows hot-plug: the chosen input is kept (by id, else by
  // name — a device plugged back in may get a new id); with none chosen,
  // the input the saved mapping was learned on is picked, else the only one.
  function relist() {
    st.inputs = listInputs();
    let pick = null;
    if (st.want) {
      pick = st.inputs.find(i => i.id === st.want.id) || st.inputs.find(i => i.name === st.want.name) || null;
      st.lost = pick ? '' : st.want.name;
    } else {
      const saved = st.profile && st.profile.deviceName;
      pick = (saved && st.inputs.find(i => i.name === saved)) || (st.inputs.length === 1 ? st.inputs[0] : null);
      if (pick) st.want = { id: pick.id, name: pick.name };
    }
    attach(pick);
    render();
  }
  function choose(id) {
    const i = st.inputs.find(x => x.id === id);
    st.want = i ? { id: i.id, name: i.name } : null;
    relist();
  }
  function attach(input) {
    if (st.input === input) return;
    if (st.input) {
      try { st.input.onmidimessage = null; } catch (_) { /* gone */ }
    }
    st.input = input;
    st.rt = [];
    st.learn = null;
    if (input) {
      input.onmidimessage = ev => onMessage(ev.data, ev.timeStamp);
      if (typeof input.open === 'function') { try { const p = input.open(); if (p && p.catch) p.catch(() => {}); } catch (_) { /* opened implicitly */ } }
    }
  }

  // --- decoding --------------------------------------------------------------------
  function relative(mode, v) {
    if (mode === 'twos') return v === 0 ? 0 : v < 64 ? v : v - 128;
    if (mode === 'offset') return v - 64;
    if (mode === 'signbit') return (v & 64) ? -(v & 63) : (v & 63);
    return 0;
  }
  // absolute turns an absolute reading into steps (see the file header).
  function absolute(r, key, v, range) {
    const last = r[key];
    r[key] = v;
    if (last === null || last === undefined) return 0;
    let d = v - last;
    if (d > range / 2) d -= range;
    else if (d < -range / 2) d += range;
    if (d === 0 && range === 128) return v === 0 ? -1 : v === 127 ? 1 : 0;
    return d;
  }
  function accel(r, ticks, t) {
    const dt = r.lastAt === null ? Infinity : t - r.lastAt;
    r.lastAt = t;
    const a = st.profile.acceleration;
    if (!a || !a.enabled) return ticks;
    const max = ACCEL_MAX[a.strength] || 1;
    const x = Math.max(0, Math.min(1, (dt - ACCEL_MIN_MS) / Math.max(1, a.thresholdMs - ACCEL_MIN_MS)));
    return ticks * (1 + (max - 1) * (1 - x));
  }

  function onMessage(data, ts) {
    if (!data || data.length < 2 || !st.profile) return;
    const s = data[0] & 0xF0, ch = (data[0] & 0x0F) + 1, d1 = data[1], d2 = data.length > 2 ? data[2] : 0;
    if (s !== 0xB0 && s !== 0x90 && s !== 0x80) return;
    const t = typeof ts === 'number' && ts > 0 ? ts : stamp();
    if (st.learn) { learnFeed(s, ch, d1, d2); return; }
    const kind = s === 0xB0 ? 'cc' : 'note';
    const press = s !== 0x80 && d2 > 0;
    const is = b => b && b.kind === kind && b.channel === ch && b.number === d1;
    const p = st.profile;
    if (is(p.bankPrev)) { if (press) page(-1); return; }
    if (is(p.bankNext)) { if (press) page(1); return; }
    for (let i = 0; i < p.encoders.length; i++) {
      const e = p.encoders[i];
      if (e && is(e.push)) { if (press) toggleFine(i); return; }
    }
    if (s !== 0xB0) return;
    for (let i = 0; i < p.encoders.length; i++) {
      const e = p.encoders[i];
      if (!e || e.channel !== ch) continue;
      if (d1 === e.control) return turn(i, e, 'msb', d2, t);
      if (e.mode === 'absolute14' && d1 === e.control + 32) return turn(i, e, 'lsb', d2, t);
    }
  }
  function turn(i, e, part, v, t) {
    const r = rt(i);
    if (e.mode !== 'absolute14') return move(i, e, e.mode === 'absolute' ? absolute(r, 'last7', v, 128) : relative(e.mode, v), t);
    if (part === 'msb') {
      if (r.msbTimer) clearTimeout(r.msbTimer);
      r.msb = v;
      // MIDI 1.0: an MSB sets the LSB to 0. Wait briefly for the LSB that
      // usually follows, so a crossing (MSB 2/LSB 0 → MSB 1/LSB 127) is
      // not read as a big jump down and back.
      r.msbTimer = setTimeout(() => { r.msbTimer = null; move(i, e, absolute(r, 'last14', r.msb << 7, 16384) / 128, t); }, MSB_WAIT_MS);
      return;
    }
    if (r.msb === null) return; // no MSB yet: this fine half cannot be placed
    if (r.msbTimer) { clearTimeout(r.msbTimer); r.msbTimer = null; }
    move(i, e, absolute(r, 'last14', (r.msb << 7) | v, 16384) / 128, t);
  }
  function move(i, e, ticks, t) {
    const r = rt(i);
    if (!ticks) { r.lastAt = t; return; }
    if (e.invert) ticks = -ticks;
    nudge(i, accel(r, ticks, t));
  }

  // --- what each encoder controls -------------------------------------------------------
  function prog() { return (typeof ProgrammerSync !== 'undefined' && ProgrammerSync.state()) || null; }
  function bladeOf(name) { const m = /^(Blade|Shaper)(\d+)/.exec(name); return m ? 'Blade ' + m[2] : 'Assembly'; }
  function controlsTab() { return (typeof ConsoleControls !== 'undefined' && ConsoleControls._state && ConsoleControls._state.tab) || null; }
  // mapping: the attributes of the Console's current tab, in the order its
  // cards are drawn (Shaper: blade by blade, as console-controls.js draws
  // it), cut into banks of as many attributes as there are encoders.
  function mapping() {
    const p = prog();
    const N = count();
    const out = { tab: null, list: [], attrs: [], bank: 0, pages: 0, N, why: '' };
    if (!p || !p.selection || !p.selection.length) { out.why = 'Nothing selected: select fixtures on the grid and the encoders take the attributes of the open tab.'; return out; }
    const groups = p.groups || [];
    if (!groups.length) { out.why = 'The selection has no profiled attributes (raw DMX channels only), so there is nothing for the encoders.'; return out; }
    let tab = controlsTab();
    if (!groups.some(g => g.group === tab)) tab = groups[0].group;
    out.tab = tab;
    if (tab === 'other') { out.why = 'Control / Other has no encoders: a turn would pass through reset and lamp ranges. Use its HOLD buttons.'; return out; }
    let list = groups.find(g => g.group === tab).attributes;
    if (tab === 'shaper') {
      const by = new Map();
      list.forEach(a => { const b = bladeOf(a.attribute); by.set(b, (by.get(b) || []).concat([a])); });
      list = [].concat(...by.values());
    }
    out.list = list;
    out.pages = Math.max(1, Math.ceil(list.length / N));
    out.bank = Math.max(0, Math.min(st.bank[tab] || 0, out.pages - 1));
    st.bank[tab] = out.bank;
    out.attrs = list.slice(out.bank * N, out.bank * N + N);
    return out;
  }
  function page(d) {
    const m = mapping();
    if (!m.tab || m.pages <= 1) { say('This tab fits on the encoders: there is no other bank.'); return; }
    const next = Math.max(0, Math.min(m.pages - 1, m.bank + d));
    if (next === m.bank) { say('Already on bank ' + (m.bank + 1) + ' of ' + m.pages + '.'); return; }
    st.bank[m.tab] = next;
    const m2 = mapping();
    say('Bank ' + (next + 1) + ' of ' + m.pages + ': ' + m2.attrs.map((a, k) => 'encoder ' + (k + 1) + ' ' + a.attribute).join(', ') + '.');
    refresh();
  }
  function toggleFine(i) {
    const r = rt(i);
    r.fine = !r.fine;
    refresh();
  }
  function globalFine() { return !!(typeof ConsoleControls !== 'undefined' && ConsoleControls._state && ConsoleControls._state.fine); }
  function fineFor(i) { return rt(i).fine || globalFine(); }
  const normalStep = max => Math.max(1, Math.round(max / 100));
  const fineStep = max => Math.max(1, Math.round(max / 4096));

  // --- nudging ---------------------------------------------------------------------------
  function selSig() { const p = prog(); return p ? p.selection.map(s => s.entryId + '/' + s.cell).join(',') : ''; }
  // seed reads a gesture's starting values: one group per (channel layout,
  // function, value), so every target in a group moves together and a
  // mixed selection keeps its spread.
  function seed(a) {
    const groups = new Map();
    const unknown = [];
    const seen = new Set();
    a.channels.forEach(c => {
      const k = c.entryId + '\u0000' + c.cell;
      if (seen.has(k)) return;
      seen.add(k);
      if (!c.touched && !c.defaultKnown) { unknown.push({ entryId: c.entryId, cell: c.cell }); return; }
      const v = a.variants[c.variant];
      const f = c.function >= 0 ? v.functions[c.function] : null;
      const key = c.variant + ':' + c.function + ':' + c.value;
      let g = groups.get(key);
      if (!g) {
        g = { key, targets: [], exact: c.value, sent: c.value, lo: f ? f.dmxFrom : 0, hi: f ? f.dmxTo : v.max, fn: c.function, max: v.max };
        groups.set(key, g);
      }
      g.targets.push({ entryId: c.entryId, cell: c.cell });
    });
    return { attribute: a.attribute, groups: [...groups.values()], unknown, sig: selSig(), at: 0 };
  }
  function gesture(a) {
    let g = st.gestures.get(a.attribute);
    if (!g || now() - g.at >= IDLE_MS || g.sig !== selSig()) {
      g = seed(a);
      st.gestures.set(a.attribute, g);
    }
    g.at = now();
    return g;
  }
  function write(key, body) {
    if (typeof ConsoleControls !== 'undefined' && ConsoleControls.lane) ConsoleControls.lane(key, 'set', body);
    else ProgrammerSync.act('set', body).catch(e => say(e.message, 'error'));
  }
  function nudge(i, ticks) {
    const m = mapping();
    const a = m.attrs[i];
    if (!a) { say('Encoder ' + (i + 1) + ' has nothing to control ' + (m.why ? '(' + m.why + ')' : 'on this bank') + '.'); return; }
    const g = gesture(a);
    const fine = fineFor(i);
    g.groups.forEach(grp => {
      const step = fine ? fineStep(grp.max) : normalStep(grp.max);
      grp.exact = Math.max(grp.lo, Math.min(grp.hi, grp.exact + ticks * step));
      const v = Math.round(grp.exact);
      if (v === grp.sent) return;
      grp.sent = v;
      const body = { targets: grp.targets.slice(), attribute: a.attribute };
      if (grp.fn >= 0) body.functionIndex = grp.fn;
      body.dmx = v;
      write('midi#' + a.attribute + '#' + grp.key, body);
    });
    if (g.unknown.length) say('Encoder ' + (i + 1) + ' (' + a.attribute + '): ' + g.unknown.length + ' selected target(s) have no value set and no stated default, so there is nothing to nudge from. Set them once with the fader.', 'warn');
    drawCells();
  }

  // --- learn ---------------------------------------------------------------------------------
  function startLearn(kind, i) {
    if (!st.input) { say('Connect and pick a controller first.', 'error'); return; }
    if (kind === 'push' && !st.profile.encoders[i]) { say('Bind encoder ' + (i + 1) + '’s turn first, then its push.', 'error'); return; }
    st.learn = { kind, i, phase: 1, ch: null, cc: null, n: 0, right: [], left: [], lsb: false };
    renderLearn();
  }
  function cancelLearn() { st.learn = null; renderLearn(); say('Learn cancelled; nothing changed.'); }
  function learnFeed(s, ch, d1, d2) {
    const L = st.learn;
    if (L.kind === 'turn') {
      if (s !== 0xB0) return;
      if (L.cc === null) { L.ch = ch; L.cc = d1; }
      if (ch !== L.ch) return;
      if (L.cc < 32 && d1 === L.cc + 32) { L.lsb = true; L.n++; }
      else if (d1 === L.cc) { (L.phase === 1 ? L.right : L.left).push(d2); L.n++; }
      else return;
      if (L.phase === 1 && L.n >= LEARN_N) {
        if (L.lsb) return finishTurn('absolute14');
        L.phase = 2;
        L.n = 0;
        renderLearn();
        return;
      }
      if (L.phase === 2 && L.left.length >= LEARN_N) {
        let mode = classify(L.right, L.left);
        if (!mode) mode = classify(L.left, L.right); // turned left first
        finishTurn(mode);
      }
      return;
    }
    const press = s !== 0x80 && d2 > 0;
    if (!press) return;
    if (s === 0xB0 && encoderAt(ch, d1) >= 0) return; // a turn is not a press
    finishButton({ kind: s === 0xB0 ? 'cc' : 'note', channel: ch, number: d1 });
  }
  // classify names the kind of encoder from what it sent turning right (R)
  // then left (L); null when the bytes do not say (never a guess).
  function classify(R, L) {
    const lo = v => v >= 1 && v <= 63, hi = v => v >= 65 && v <= 127;
    if (R.every(v => v > 64) && L.every(v => v < 64)) return 'offset';
    if (R.every(lo) && L.every(hi)) {
      // The smaller reading wins: turning slowly sends small steps, so 127
      // (two's complement −1) and 65 (signed-bit −1) tell the two apart.
      const twos = L.reduce((n, v) => n + (128 - v), 0), sign = L.reduce((n, v) => n + (v - 64), 0);
      return twos < sign ? 'twos' : sign < twos ? 'signbit' : null;
    }
    const up = R.length > 1 && R.every((v, k) => k === 0 || v >= R[k - 1]) && R[R.length - 1] > R[0];
    const down = L.length > 1 && L.every((v, k) => k === 0 || v <= L[k - 1]) && L[L.length - 1] < L[0];
    return up && down ? 'absolute' : null;
  }
  function encoderAt(ch, cc) {
    return st.profile.encoders.findIndex(e => e && e.channel === ch && (e.control === cc || (e.mode === 'absolute14' && e.control + 32 === cc)));
  }
  function freeMessage(kind, ch, num, keep) {
    // Unbind anything else answering to this message; returns what moved.
    const p = st.profile;
    const moved = [];
    const btnIs = b => b && b.kind === kind && b.channel === ch && b.number === num;
    p.encoders.forEach((e, k) => {
      if (!e || keep === 'enc' + k) return;
      if (kind === 'cc' && e.channel === ch && (e.control === num || (e.mode === 'absolute14' && e.control + 32 === num))) { p.encoders[k] = null; moved.push('encoder ' + (k + 1)); return; }
      if (btnIs(e.push) && keep !== 'push' + k) { e.push = null; moved.push('encoder ' + (k + 1) + ' push'); }
    });
    if (btnIs(p.bankPrev) && keep !== 'bankPrev') { p.bankPrev = null; moved.push('bank back'); }
    if (btnIs(p.bankNext) && keep !== 'bankNext') { p.bankNext = null; moved.push('bank forward'); }
    return moved;
  }
  function finishTurn(mode) {
    const L = st.learn;
    st.learn = null;
    renderLearn();
    if (!mode) {
      say('Encoder ' + (L.i + 1) + ' NOT bound: from what it sent (right: ' + L.right.join(', ') + '; left: ' + L.left.join(', ') + ') the kind of encoder cannot be told. Turn slowly, one click at a time, and try again, or set its channel, controller and type by hand.', 'error');
      return;
    }
    const prev = st.profile.encoders[L.i];
    const moved = freeMessage('cc', L.ch, L.cc, 'enc' + L.i);
    if (mode === 'absolute14') moved.push(...freeMessage('cc', L.ch, L.cc + 32, 'enc' + L.i));
    // Its own push survives unless it is now one of the turn's messages.
    const pp = prev && prev.push;
    const clash = pp && pp.kind === 'cc' && pp.channel === L.ch && (pp.number === L.cc || (mode === 'absolute14' && pp.number === L.cc + 32));
    st.profile.encoders[L.i] = { channel: L.ch, control: L.cc, mode, invert: false, push: pp && !clash ? pp : null };
    st.rt[L.i] = null;
    save('Encoder ' + (L.i + 1) + ' bound: channel ' + L.ch + ', controller ' + L.cc + ', ' + MODE_LABEL[mode] + '.' + (moved.length ? ' (Taken from ' + moved.join(', ') + '.)' : ''));
  }
  function finishButton(b) {
    const L = st.learn;
    st.learn = null;
    renderLearn();
    const keep = L.kind === 'push' ? 'push' + L.i : L.kind;
    const moved = freeMessage(b.kind, b.channel, b.number, keep);
    const what = b.kind + ' ' + b.number + ' on channel ' + b.channel;
    if (L.kind === 'push') {
      const e = st.profile.encoders[L.i];
      if (!e) { say('Encoder ' + (L.i + 1) + ' lost its binding; bind its turn first.', 'error'); return; }
      e.push = b;
      save('Encoder ' + (L.i + 1) + ' push = ' + what + ' (toggles fine).' + (moved.length ? ' (Taken from ' + moved.join(', ') + '.)' : ''));
    } else {
      st.profile[L.kind] = b;
      save((L.kind === 'bankPrev' ? 'Bank back' : 'Bank forward') + ' = ' + what + '.' + (moved.length ? ' (Taken from ' + moved.join(', ') + '.)' : ''));
    }
  }

  // --- status ----------------------------------------------------------------------------------
  function say(text, tone) {
    st.note = text ? { text, tone: tone || '' } : null;
    const el = st.els.say;
    if (!el) return;
    el.className = 'b5-midi-say' + (tone ? ' b5-midi-say--' + tone : '');
    el.textContent = text || '';
  }

  // --- rendering -------------------------------------------------------------------------------
  function phaseWords() {
    switch (st.phase) {
      case 'insecure': return { text: insecureWords(), tone: 'warn' };
      case 'unsupported': return { text: 'This browser has no Web MIDI. Chrome and Edge support it; Safari does not. Open Benny512 in Chrome or Edge on the computer the controller is plugged into.', tone: 'warn' };
      case 'denied': return { text: 'The browser refused MIDI access' + (st.why ? ' (' + st.why + ')' : '') + '. Allow MIDI for this site in the browser’s site settings, then press Connect MIDI again.', tone: 'error' };
      case 'asking': return { text: 'Asking the browser for MIDI access — it may show a permission prompt.', tone: '' };
      case 'ready':
        if (st.lost) return { text: st.lost + ' was unplugged. Plug it back in; it reconnects by itself.', tone: 'warn' };
        if (!st.inputs.length) return { text: 'MIDI is on, but no MIDI controller is plugged into this computer. Plug it in; it appears here by itself.', tone: 'warn' };
        if (!st.input) return { text: 'Pick the controller to use.', tone: '' };
        return { text: 'Using ' + (st.input.name || 'the MIDI input') + '.', tone: 'ok' };
      default:
        return { text: 'Not connected. ' + PLUG_WORDS, tone: '' };
    }
  }
  function render() {
    const root = st.region;
    if (!root) return;
    clear(root);
    st.els = {};
    if (!st.loaded) load();
    if (st.phase === 'idle' && !secure()) st.phase = 'insecure';
    else if (st.phase === 'idle' && !hasWebMIDI()) st.phase = 'unsupported';
    const words = phaseWords();
    const head = h('div', { class: 'b5-midi-bar' }, h('strong', { class: 'b5-midi-title', text: 'MIDI encoders' }));
    if (st.phase === 'idle' || st.phase === 'denied') head.appendChild(btn('Connect MIDI', { 'data-midi-connect': '' }, () => connect(), 'b5-btn--primary'));
    if (st.phase === 'ready' && st.inputs.length) {
      const sel = h('select', { class: 'b5-select b5-midi-input', 'data-midi-input': '', 'aria-label': 'MIDI controller' });
      if (!st.input) sel.appendChild(h('option', { value: '', text: 'Pick a controller…' }));
      st.inputs.forEach(i => sel.appendChild(h('option', { value: i.id, text: i.name || i.id })));
      sel.value = st.input ? st.input.id : '';
      sel.addEventListener('change', () => choose(sel.value));
      head.appendChild(sel);
    }
    root.appendChild(head);
    root.appendChild(h('p', { class: 'b5-midi-phase' + (words.tone ? ' b5-midi-phase--' + words.tone : ''), 'data-midi-phase': st.phase, role: 'status' }, words.text));
    if (st.phase === 'insecure' || st.phase === 'unsupported') return;
    st.els.say = h('p', { class: 'b5-midi-say', 'aria-live': 'polite', 'data-midi-say': '' });
    root.appendChild(st.els.say);
    if (st.note) say(st.note.text, st.note.tone);
    if (st.phase !== 'ready') return;
    st.els.learn = h('div', { class: 'b5-midi-learn', 'data-midi-learn': '' });
    st.els.strip = h('div', { class: 'b5-midi-strip', role: 'list', 'aria-label': 'What each encoder controls', 'data-midi-strip': '' });
    st.els.bank = h('div', { class: 'b5-midi-bank', 'data-midi-bank': '' });
    st.els.setup = h('details', { class: 'b5-cc-tool b5-midi-setup', 'data-midi-setup': '' });
    st.els.setup.open = !!st.setupOpen;
    st.els.setup.addEventListener('toggle', () => { st.setupOpen = st.els.setup.open; });
    root.appendChild(st.els.learn);
    root.appendChild(st.els.strip);
    root.appendChild(st.els.bank);
    root.appendChild(st.els.setup);
    st.stripSig = '';
    renderLearn();
    renderSetup();
    drawCells();
  }
  function renderLearn() {
    const el = st.els.learn;
    if (!el) return;
    clear(el);
    const L = st.learn;
    if (!L) { el.hidden = true; return; }
    el.hidden = false;
    let words;
    if (L.kind === 'turn') words = L.phase === 1 ? 'Learning encoder ' + (L.i + 1) + ': turn it RIGHT (clockwise), slowly, a few clicks.' : 'Encoder ' + (L.i + 1) + ': now turn it LEFT (anticlockwise), slowly, a few clicks.';
    else if (L.kind === 'push') words = 'Learning encoder ' + (L.i + 1) + '’s push: press the encoder (or the button that should toggle its fine mode).';
    else words = 'Learning ' + (L.kind === 'bankPrev' ? 'BANK BACK' : 'BANK FORWARD') + ': press the button on the controller.';
    el.appendChild(h('p', { class: 'b5-midi-learn__words', role: 'status', 'data-midi-learn-words': '' }, words));
    el.appendChild(btn('Cancel learn', { 'data-midi-learn-cancel': '' }, cancelLearn));
  }
  function readout(a, i) {
    const g = st.gestures.get(a.attribute);
    let vals, max = a.variants[0].max;
    if (g && now() - g.at < IDLE_MS && g.sig === selSig()) vals = g.groups.map(x => ({ v: x.sent, max: x.max }));
    else vals = a.channels.filter(c => c.touched || c.defaultKnown).map(c => ({ v: c.value, max: a.variants[c.variant].max }));
    if (!vals.length) return 'no value yet';
    const pc = x => Math.round(x.v / (x.max || 1) * 100);
    const ps = vals.map(pc);
    const lo = Math.min(...ps), hi = Math.max(...ps);
    if (vals.every(x => x.v === vals[0].v && x.max === vals[0].max)) return vals[0].v + ' / ' + max + ' · ' + ps[0] + ' %';
    return 'MIXED ' + lo + '–' + hi + ' %';
  }
  function drawCells() {
    const strip = st.els.strip;
    if (!strip || !st.profile) return;
    const m = mapping();
    const enc = st.profile.encoders;
    const sig = JSON.stringify([m.tab, m.bank, m.pages, m.N, m.attrs.map(a => a.attribute), m.why, enc.map(e => !!e)]);
    if (sig !== st.stripSig) {
      st.stripSig = sig;
      clear(strip);
      clear(st.els.bank);
      if (m.why) strip.appendChild(h('p', { class: 'b5-midi-why', 'data-midi-why': '' }, m.why));
      if (m.tab && m.tab !== 'other') {
        for (let i = 0; i < m.N; i++) {
          const a = m.attrs[i];
          const fineB = h('button', { type: 'button', class: 'b5-seg b5-midi-fine', 'data-enc-fine': String(i + 1), 'aria-pressed': 'false', onclick: () => toggleFine(i) }, 'Fine: OFF');
          strip.appendChild(h('div', { class: 'b5-midi-enc' + (a ? '' : ' is-empty'), role: 'listitem', 'data-enc': String(i + 1) },
            h('span', { class: 'b5-midi-enc__num', text: 'Enc ' + (i + 1) }),
            h('span', { class: 'b5-midi-enc__attr', 'data-enc-attr': String(i + 1), text: a ? a.attribute : '— nothing on this bank' }),
            h('span', { class: 'b5-text-mono b5-midi-enc__val', 'data-enc-val': String(i + 1) }),
            h('span', { class: 'b5-midi-enc__tags', 'data-enc-tags': String(i + 1) }),
            fineB));
        }
        if (m.pages > 1) {
          const from = m.bank * m.N + 1, to = Math.min(m.list.length, from + m.N - 1);
          st.els.bank.appendChild(btn('◀ Bank', { 'data-midi-bank-prev': '', 'aria-label': 'Previous bank' }, () => page(-1)));
          st.els.bank.appendChild(h('span', { class: 'b5-midi-bank__words', 'data-midi-bank-words': '' },
            'Bank ' + (m.bank + 1) + ' of ' + m.pages + ' · ' + (GROUP_LABEL[m.tab] || m.tab) + ' attributes ' + from + '–' + to + ' of ' + m.list.length));
          st.els.bank.appendChild(btn('Bank ▶', { 'data-midi-bank-next': '', 'aria-label': 'Next bank' }, () => page(1)));
        }
      }
    }
    const p = prog();
    for (let i = 0; i < m.N; i++) {
      const a = m.attrs[i];
      const val = strip.querySelector('[data-enc-val="' + (i + 1) + '"]');
      const tags = strip.querySelector('[data-enc-tags="' + (i + 1) + '"]');
      const fb = strip.querySelector('[data-enc-fine="' + (i + 1) + '"]');
      if (!val) continue;
      val.textContent = a ? readout(a, i) : '';
      clear(tags);
      if (!enc[i]) tags.appendChild(tag('not bound', 'open'));
      if (fineFor(i)) tags.appendChild(tag(rt(i).fine ? 'FINE' : 'FINE (Controls)', 'accent'));
      if (a && a.channels.some(c => !c.touched && !c.defaultKnown)) tags.appendChild(tag('default unknown', 'unread'));
      fb.textContent = rt(i).fine ? 'Fine: ON' : 'Fine: OFF';
      fb.setAttribute('aria-pressed', rt(i).fine ? 'true' : 'false');
      fb.classList.toggle('is-on', rt(i).fine);
    }
    if (p && p.output && !p.output.live && m.tab && m.tab !== 'other' && !strip.querySelector('[data-midi-unarmed]')) {
      strip.insertBefore(h('p', { class: 'b5-midi-why', 'data-midi-unarmed': '' }, 'Output is not armed: encoder moves change the programmer and reach the rig only when someone presses Arm.'), strip.firstChild);
    } else if (p && p.output && p.output.live) {
      const u = strip.querySelector('[data-midi-unarmed]');
      if (u) u.remove();
    }
  }
  function bindingWords(e) {
    if (!e) return 'not bound';
    return 'channel ' + e.channel + ' · controller ' + e.control + (e.mode === 'absolute14' ? ' + ' + (e.control + 32) : '');
  }
  function buttonWords(b) { return b ? b.kind + ' ' + b.number + ' · channel ' + b.channel : 'not bound'; }
  function renderSetup() {
    const d = st.els.setup;
    if (!d || !st.profile) return;
    clear(d);
    d.appendChild(h('summary', { text: 'MIDI set-up · ' + st.profile.encoders.filter(Boolean).length + ' of ' + count() + ' encoders bound' }));
    const body = h('div', { class: 'b5-midi-setupbody' });
    const p = st.profile;
    if (st.loadError) body.appendChild(h('p', { class: 'b5-midi-say b5-midi-say--error', text: st.loadError }));
    body.appendChild(h('p', { class: 'b5-caption' }, st.persisted ? 'Saved on this station' : 'Demo mode: kept until Benny512 closes',
      p.deviceName ? ' · learned on ' + p.deviceName : '', st.input && p.deviceName && st.input.name !== p.deviceName ? ' (you are using ' + st.input.name + ')' : ''));
    const cnt = h('select', { class: 'b5-select', 'data-midi-count': '', 'aria-label': 'How many encoders' });
    for (let n = 1; n <= 16; n++) cnt.appendChild(h('option', { value: String(n), text: String(n) }));
    cnt.value = String(count());
    cnt.addEventListener('change', () => {
      const n = Number(cnt.value);
      const e = p.encoders.slice(0, n);
      while (e.length < n) e.push(null);
      p.encoders = e;
      save('Encoders: ' + n + '.');
    });
    body.appendChild(h('label', { class: 'b5-row' }, h('span', { text: 'Encoders on the strip' }), cnt));
    p.encoders.forEach((e, i) => {
      const row = h('div', { class: 'b5-midi-encrow', 'data-midi-encrow': String(i + 1) }, h('strong', { text: 'Encoder ' + (i + 1) }), h('span', { class: 'b5-caption', 'data-midi-binding': String(i + 1), text: bindingWords(e) }));
      const chIn = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 1, max: 16, step: 1, 'aria-label': 'Encoder ' + (i + 1) + ' MIDI channel', 'data-midi-ch': String(i + 1) });
      const ccIn = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 127, step: 1, 'aria-label': 'Encoder ' + (i + 1) + ' controller number', 'data-midi-cc': String(i + 1) });
      const mode = h('select', { class: 'b5-select', 'aria-label': 'Encoder ' + (i + 1) + ' type', 'data-midi-mode': String(i + 1) });
      MODES.forEach(k => mode.appendChild(h('option', { value: k, text: MODE_LABEL[k] })));
      chIn.value = e ? String(e.channel) : '1';
      ccIn.value = e ? String(e.control) : '';
      mode.value = e ? e.mode : 'twos';
      const inv = h('input', { type: 'checkbox', 'data-midi-invert': String(i + 1) });
      inv.checked = !!(e && e.invert);
      const apply = () => {
        const ch = Number(chIn.value), cc = Number(ccIn.value);
        if (ccIn.value === '' || !(ch >= 1 && ch <= 16 && ch === Math.round(ch)) || !(cc >= 0 && cc <= 127 && cc === Math.round(cc))) { say('Encoder ' + (i + 1) + ': a channel is 1–16 and a controller 0–127.', 'error'); return; }
        if (mode.value === 'absolute14' && cc > 31) { say('Encoder ' + (i + 1) + ': a 14-bit pair uses controllers 0–31 (n+32 is its fine half).', 'error'); return; }
        const moved = freeMessage('cc', ch, cc, 'enc' + i);
        if (mode.value === 'absolute14') moved.push(...freeMessage('cc', ch, cc + 32, 'enc' + i));
        p.encoders[i] = { channel: ch, control: cc, mode: mode.value, invert: inv.checked, push: e ? e.push : null };
        st.rt[i] = null;
        save('Encoder ' + (i + 1) + ': channel ' + ch + ', controller ' + cc + ', ' + MODE_LABEL[mode.value] + (inv.checked ? ', inverted' : '') + '.' + (moved.length ? ' (Taken from ' + moved.join(', ') + '.)' : ''));
      };
      row.appendChild(h('div', { class: 'b5-row b5-midi-encrow__edit' },
        h('label', { class: 'b5-row' }, h('span', { text: 'Ch' }), chIn), h('label', { class: 'b5-row' }, h('span', { text: 'CC' }), ccIn), mode,
        h('label', { class: 'b5-row b5-midi-check' }, inv, h('span', { text: 'Invert' })),
        btn('Apply', { 'data-midi-apply': String(i + 1) }, apply)));
      row.appendChild(h('div', { class: 'b5-row' },
        btn('Learn turn', { 'data-midi-learn-turn': String(i + 1) }, () => startLearn('turn', i), 'b5-btn--primary'),
        btn('Learn push', { 'data-midi-learn-push': String(i + 1) }, () => startLearn('push', i)),
        h('span', { class: 'b5-caption', 'data-midi-push': String(i + 1), text: 'push: ' + buttonWords(e && e.push) }),
        e ? btn('Unbind', { 'data-midi-unbind': String(i + 1) }, () => { p.encoders[i] = null; st.rt[i] = null; save('Encoder ' + (i + 1) + ' unbound.'); }) : null));
      body.appendChild(row);
    });
    ['bankPrev', 'bankNext'].forEach(k => {
      const label = k === 'bankPrev' ? 'Bank back button' : 'Bank forward button';
      body.appendChild(h('div', { class: 'b5-row b5-midi-encrow', 'data-midi-bankrow': k }, h('strong', { text: label }), h('span', { class: 'b5-caption', text: buttonWords(p[k]) }),
        btn('Learn', { ['data-midi-learn-' + k]: '' }, () => startLearn(k, -1)),
        p[k] ? btn('Unbind', {}, () => { p[k] = null; save(label + ' unbound.'); }) : null));
    });
    const acc = p.acceleration;
    const en = h('input', { type: 'checkbox', 'data-midi-accel': '' });
    en.checked = !!acc.enabled;
    const th = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 10, max: 1000, step: 1, 'data-midi-accel-ms': '', 'aria-label': 'Acceleration threshold in milliseconds' });
    th.value = String(acc.thresholdMs);
    const str = h('select', { class: 'b5-select', 'data-midi-accel-strength': '', 'aria-label': 'Acceleration strength' });
    [1, 2, 3, 4, 5].forEach(n => str.appendChild(h('option', { value: String(n), text: n + ' (up to ' + ACCEL_MAX[n] + '×)' })));
    str.value = String(acc.strength);
    body.appendChild(h('div', { class: 'b5-row b5-midi-encrow', 'data-midi-accelrow': '' },
      h('label', { class: 'b5-row b5-midi-check' }, en, h('strong', { text: 'Acceleration' })),
      h('label', { class: 'b5-row' }, h('span', { text: 'faster than' }), th, h('span', { text: 'ms per click' })),
      h('label', { class: 'b5-row' }, h('span', { text: 'strength' }), str),
      btn('Apply', { 'data-midi-accel-apply': '' }, () => {
        const ms = Number(th.value);
        if (!(ms >= 10 && ms <= 1000 && ms === Math.round(ms))) { say('The acceleration threshold is a whole number of milliseconds from 10 to 1000.', 'error'); return; }
        p.acceleration = { enabled: en.checked, thresholdMs: ms, strength: Number(str.value) };
        save('Acceleration ' + (en.checked ? 'ON (faster than ' + ms + ' ms per click, up to ' + ACCEL_MAX[Number(str.value)] + '×).' : 'OFF.'));
      })));
    body.appendChild(h('p', { class: 'b5-caption', text: 'One click = 1 % of the channel (fine: 1/4096). A mixed selection moves together by the same number of steps from each fixture’s own value, each stopping at the edge of the function it is in. ' + PLUG_WORDS }));
    d.appendChild(body);
  }

  // --- the hooks console-controls.js calls ------------------------------------------------------
  function mount(region) {
    if (!region) return;
    st.region = region;
    render();
  }
  function refresh() {
    if (!st.region) return;
    drawCells();
  }

  return {
    mount, refresh, connect,
    // for tests
    _state: st, _onMessage: onMessage, _classify: classify, _mapping: mapping,
  };
})();
