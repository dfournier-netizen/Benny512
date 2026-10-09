// console-controls.js — the Console's ATTRIBUTE CONTROLS (Console-lite C6b),
// mounted by console.js into its "Controls" region.
//
// Everything here reads GET /api/programmer (ProgrammerSync.state()) and
// writes through ProgrammerSync.act, so every browser drives and sees the
// same programmer. All controls are live: the master ARM governs output.
//
// WHAT IS DRAWN, per attribute group the selection actually has (tabs:
// Dimmer, Position, Colour, Beam, Focus, Shaper, Control/Other):
//   - one card per attribute, with its value (shared value or MIXED with the
//     range), SET / default, "default unknown", "on n of m";
//   - per GDTF channel function (NoFeature/Dummy placeholders hidden):
//       * wheel-slot sets -> slot buttons (slot number, slot name, sRGB
//         swatch when the profile has one, media file name as text);
//       * other named channel sets -> set buttons;
//       * a fader over the function's DMX range (fraction-within-function
//         writes), unless the function is the channel's plain slot
//         selection or a single DMX value (then it is a button);
//   - Position: an XY pad (pan X, tilt Y; up = higher tilt value) working
//     in tandem with the Pan/Tilt faders, fine mode, Centre and Home;
//   - a channel with several functions: a range rail (§7) — proportional
//     segments plus one label button per function, from the file;
//   - Shutter(n): OPEN / CLOSED radio group from the file's sets (§10);
//   - Colour: capability counts, emitter names, Mix / Wheel / CTO-CTB
//     modes, a hue/saturation field writing what each fixture HAS (see
//     colourWrites), capped to the dark-venue preview brightness (§9);
//   - Shaper: a numbered blade diagram plus labelled A/B faders (§11);
//   - Control/Other: NO faders (a sweep would pass through reset and lamp
//     ranges) — every function and set is a HOLD-1.5-s button.
// Plus a toolbar (Clear, Lowlight, Fan, family presets) and a raw-DMX panel
// for offsets with no profile (unprofiled fixtures: their only controls).
//
// WRITES. Fader drags go through one lane per control: latest value wins,
// at most ~30 writes a second, one in flight per lane, and the value at
// release always goes out last. A write refused as stale (another browser
// moved the programmer) is retried once on the refreshed revision, never
// more. Mode-master moves and skipped targets the server reports are shown
// in its own words.
const ConsoleControls = (() => {
  const LANE_MS = 34;          // >= 33 ms between writes of one lane (< 30/s)
  const HOLD_MS = 750;         // press-and-hold for Control buttons (owner, 2026-10-08: 0.75 s)
  const ACTIVE_MS = 600;       // a control just touched keeps its local value
  // I2c (component-specs §14): seven attribute families, always drawn in
  // this order, each with its sprite icon; owner-confirmed names
  // "Beam/Gobo" and "Control" (server groups beam / other).
  const FAMILIES = [['dimmer', 'Dimmer'], ['position', 'Position'], ['colour', 'Colour'], ['beam', 'Beam/Gobo'], ['focus', 'Focus'], ['shaper', 'Shaper'], ['other', 'Control']];
  const FAMILY_ICON = { dimmer: 'attr-dimmer', position: 'attr-position', colour: 'attr-colour', beam: 'attr-beam', focus: 'attr-focus', shaper: 'attr-shaper', other: 'attr-control' };
  const GROUP_LABEL = Object.fromEntries(FAMILIES);
  const HIDDEN_FN = /^(NoFeature|Dummy)$/;
  const UNITS_KEY = 'b5.consoleControls.readout'; // per-browser readout choice (§13)
  const st = { root: null, tab: null, sig: '', fine: false, units: loadUnits(), models: {}, modelsLoading: false, open: {}, statusTimer: null, colourMode: null };
  function loadUnits() {
    try { const u = localStorage.getItem(UNITS_KEY); if (u === 'pct' || u === 'dmx' || u === 'phys') return u; } catch (_) { /* no storage */ }
    return 'pct';
  }
  const reg = new Map();     // control key -> {update(attr), local(v)}
  const lanes = new Map();
  let els = {};

  // --- DOM helpers (same shape as console.js's) ----------------------------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const k of Object.keys(props)) {
        const v = props[k];
        if (v === undefined || v === null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = String(v);
        else if (k === 'icon') el.innerHTML = UI.icon(v);
        else if (k === 'style') Object.keys(v).forEach(s => el.style.setProperty(s, v[s]));
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
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }
  function ic(name) { return h('span', { class: 'b5-con-ic', 'aria-hidden': 'true', icon: name }); }
  function btn(label, attrs, onclick, cls) {
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn b5-btn--sm' + (cls ? ' ' + cls : '') }, attrs || {}), label);
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function tag(word, tone) { return h('span', { class: 'b5-pill b5-pill--tag' + (tone ? ' b5-pill--' + tone : ''), text: word }); }
  const now = () => Date.now();

  // --- state helpers ---------------------------------------------------------
  function prog() { return ProgrammerSync.state() || null; }
  // I2d2: fixture commands are one-shot and go out only while output is
  // LIVE (armed); the server refuses them otherwise and nothing is queued
  // for a later Arm. liveNow follows the strip's heartbeat (workspace.js
  // 'b5-output') once one has arrived, else the programmer's own answer.
  let outState;
  function liveNow() {
    if (outState !== undefined) return outState === 'armed';
    const p = prog();
    return !!(p && p.output && p.output.live);
  }
  const CMD_SECONDS = 1; // patch.CommandWindow (owner 2026-10-09: 1 s); the server answers windowMs
  const NOT_LIVE = 'Output is not live: commands are sent only while output is armed, and nothing is kept to send later.';
  const liveSubs = new Set(); // open command dialogs redraw on a change
  // drawOutputNote: the Controls panel's output note (State S), from the
  // strip's heartbeat once one has arrived, else the programmer's answer.
  function drawOutputNote() {
    const live = liveNow();
    const p = prog();
    const state = outState !== undefined ? outState : (p && p.output && p.output.state);
    document.querySelectorAll('[data-output-note]').forEach(n => {
      n.hidden = live;
      const t = n.querySelector('[data-output-note-text]');
      if (t) t.textContent = state === 'disarmed' ? 'Disarmed · values retained, nothing sent' : ((outState === undefined && p && p.output && p.output.note) || 'Output is not live.');
    });
  }
  function syncLive() {
    const live = liveNow();
    drawOutputNote();
    document.querySelectorAll('.b5-cc-hold').forEach(b => { b.disabled = !live; });
    document.querySelectorAll('[data-hold-why]').forEach(p => { p.hidden = live; });
    liveSubs.forEach(fn => fn());
  }
  if (typeof window !== 'undefined' && window.addEventListener) {
    window.addEventListener('b5-output', e => {
      const s = e && e.detail ? e.detail.state : 'unknown';
      if (s === outState) return;
      outState = s;
      syncLive();
    });
  }
  function sel() { const p = prog(); return (p && p.selection) || []; }
  function groupsOf() { const p = prog(); return (p && p.groups) || []; }
  function attrsOf(group) { const g = groupsOf().find(x => x.group === group); return g ? g.attributes : []; }
  function findAttr(name) {
    for (const g of groupsOf()) for (const a of g.attributes) if (a.attribute === name) return a;
    return null;
  }
  function v0(a) { return a.variants[0]; }
  function sameFns(a) {
    const names = v => v.functions.map(f => f.name).join('\u0000');
    return a.variants.every(v => names(v) === names(v0(a)));
  }
  // fnRef names function i of variant 0 so every fixture type understands
  // it: by index when every variant has the same functions, by name when
  // the name is unique in every variant, otherwise not at all (null).
  function fnRef(a, i) {
    if (sameFns(a)) return { functionIndex: i };
    const name = v0(a).functions[i].name;
    if (a.variants.every(v => v.functions.filter(f => f.name === name).length === 1)) return { functionName: name };
    return null;
  }
  function chanValue(a) {
    if (!a.mixed && a.value !== null && a.value !== undefined) return a.value;
    return a.channels.length ? a.channels[0].value : 0;
  }
  function activeFn(a) {
    if (!a.channels.length) return -1;
    const f = a.channels[0].function;
    return a.channels.every(c => c.function === f && c.variant === a.channels[0].variant) ? f : -1;
  }
  // unit: the PhysicalUnit the GDTF attribute definitions (DIN SPEC 15800,
  // attribute definitions XML) give the FUNCTION's attribute — physical
  // values of a channel function are in its own attribute's unit. Rotate
  // and spin functions are AngularSpeed (°/s), never an angle; shake,
  // strobe and pulse functions are Frequency (Hz). An attribute the
  // definitions give no unit gets none: never a guessed ° or %.
  // (Prism(n)Pos is left out: the definitions' text says "angle of indexed
  // rotation" while its XML says AngularSpeed — unresolved, so no unit.)
  const UNIT_RULES = [
    [/^(PanRotate|TiltRotate|BeamShaperPosRotate)$|^(Gobo|AnimationWheel)\d+(PosRotate|SelectSpin)$|^Gobo\d+WheelSpin$|^Color\d+WheelSpin$|^Prism\d+(PosRotate|SelectSpin)$|^(AnimationSystem|Effects)\d+PosRotate$/, ' °/s'],
    [/^(Pan|Tilt|Zoom|DigitalZoom|ZoomModeBeam|ZoomModeSpot|Rot_X|Rot_Y|Rot_Z|ShaperRot|BeamShaperPos)$|^Blade\d+Rot$|^(Gobo|Color)\d+WheelIndex$|^(Gobo|AnimationWheel|AnimationSystem|Effects)\d+Pos$/, '°'],
    [/^StrobeFrequency$|^Shutter\d+Strobe[A-Za-z]*$|^Iris(Strobe|StrobeRandom|PulseOpen|PulseClose|RandomPulseOpen|RandomPulseClose)$|^Frost\d+(PulseOpen|PulseClose|Ramp)$|^(Gobo|AnimationWheel)\d+(PosShake|SelectShake|WheelShake)$|^AnimationSystem\d+PosShake$|^AnimationWheel\d+Random$/, ' Hz'],
    [/^Focus\d+Distance$/, ' m'], [/^StrobeDuration$/, ' s'], [/^AnimationSystem\d+$/, ' %'],
  ];
  function unit(attr) {
    const r = UNIT_RULES.find(x => x[0].test(attr || ''));
    return r ? r[1] : '';
  }
  function physical(attr, f, v) {
    if (!f || !f.hasPhysicalRange || v < f.dmxFrom || v > f.dmxTo || f.dmxTo === f.dmxFrom) return null;
    const p = f.physicalFrom + (v - f.dmxFrom) / (f.dmxTo - f.dmxFrom) * (f.physicalTo - f.physicalFrom);
    return (Math.round(p * 10) / 10) + unit(f.attribute || attr);
  }
  // physLine: a function's physical range in its unit for the fader
  // caption, or "<unit> not reported" when the attribute has a unit but
  // the profile gives no range (e.g. strobe "Hz not reported").
  function physLine(f) {
    const u = unit(f.attribute);
    if (!u) return '';
    if (!f.hasPhysicalRange) return u.trim() + ' not reported';
    return (Math.round(f.physicalFrom * 10) / 10) + '–' + (Math.round(f.physicalTo * 10) / 10) + u;
  }
  function pct(v, max) { return max ? Math.round(v / max * 100) : 0; }

  // --- shared value grammar (component-specs §13, state S) -------------------
  // Every readout is built from the COMPLETE integer, then split for display:
  // DMX "32 768 / 65 535", % of the active function's range (the whole
  // channel when no function is known), the physical value only where the
  // profile gives a range, and for 16-bit channels the bytes
  // "128 · 0 (coarse · fine)" with a 16-bit badge. The global Readout
  // choice (%, DMX, Physical) picks which one is shown large; the rest stay
  // visible in the line under it. Never a number for a value not known.
  const NB = '\u00a0';
  function num(v) { return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, NB); }
  function bytesOf(v, bc) {
    const parts = [];
    for (let i = bc - 1; i >= 0; i--) parts.push(Math.floor(v / Math.pow(256, i)) % 256);
    const names = ['coarse', 'fine', 'ultra'].slice(0, bc);
    return parts.join(' · ') + ' (' + names.join(' · ') + ')';
  }
  // vfmt: the readout of complete value v of a channel with byte count bc
  // and top value max, inside function f (or the whole channel when null).
  function vfmt(attrName, v, max, f, bc) {
    const lo = f ? f.dmxFrom : 0, hi = f ? f.dmxTo : max;
    const ph = physical(attrName, f, v);
    const p = pct(v - lo, hi - lo) + NB + '%';
    const d = num(v) + ' / ' + num(max);
    const main = st.units === 'dmx' ? num(v) : st.units === 'phys' && ph ? ph : p;
    const rest = [st.units === 'dmx' ? null : d, st.units === 'pct' ? null : p, st.units === 'phys' ? null : ph,
      st.units === 'phys' && !ph ? 'no physical range in profile' : null, bc > 1 ? bytesOf(v, bc) : null].filter(Boolean);
    return { main, sub: rest.join(' · '), bits: bc > 1 ? 8 * bc + '-bit' : '' };
  }
  // mixfmt: a MIXED value is its range, never an average (§13, S).
  function mixfmt(vs, max, f) {
    const lo = Math.min(...vs), hi = Math.max(...vs);
    const flo = f ? f.dmxFrom : 0, fhi = f ? f.dmxTo : max;
    const p = pct(lo - flo, fhi - flo) + '–' + pct(hi - flo, fhi - flo) + NB + '%';
    const d = num(lo) + '–' + num(hi) + ' / ' + num(max);
    return { main: 'MIXED ' + (st.units === 'dmx' ? num(lo) + '–' + num(hi) : p), sub: (st.units === 'dmx' ? p : d) + ' · reference (first selected) ' + num(vs[0]) };
  }
  function readoutParts(a) {
    const max = v0(a).max, bc = v0(a).byteCount || 1;
    const fi = activeFn(a);
    const f = fi >= 0 ? v0(a).functions[fi] : null;
    if (a.mixed) return Object.assign(mixfmt(a.channels.map(c => c.value), max, f), { bits: bc > 1 ? 8 * bc + '-bit' : '' });
    const r = vfmt(a.attribute, chanValue(a), max, f, bc);
    if (f && v0(a).functions.length > 1) r.sub += ' · ' + f.name;
    return r;
  }
  function fillReadout(el, a) {
    const r = readoutParts(a);
    clear(el);
    el.appendChild(h('span', { class: 'b5-cc-val', text: r.main }));
    if (r.bits) el.appendChild(h('span', { class: 'b5-pill b5-pill--tag b5-cc-bits', text: r.bits }));
    el.appendChild(h('span', { class: 'b5-cc-valsub', text: r.sub }));
  }
  // Source words (state S): filled dot + SET, hollow circle + default;
  // the dot is decoration, the word is the signal.
  function srcTag(word, set) {
    return h('span', { class: 'b5-pill b5-pill--tag b5-cc-src' + (set ? ' b5-cc-src--set' : ' b5-cc-src--default') },
      h('span', { class: 'b5-cc-src__dot', 'aria-hidden': 'true', text: set ? '●' : '○' }), word);
  }
  function setCount(a) {
    const all = new Set(), set = new Set();
    a.channels.forEach(c => { const k = c.entryId + '\u0000' + c.cell; all.add(k); if (c.touched) set.add(k); });
    return [set.size, all.size];
  }
  function markers(a) {
    const out = [];
    if (a.allTouched) out.push(srcTag('SET', true));
    else if (a.touched) { const [n, m] = setCount(a); out.push(srcTag('SET on ' + n + ' of ' + m, true)); }
    else out.push(srcTag('default', false));
    if (a.mixed) out.push(tag('MIXED', 'warn'));
    if (a.unknownDefault) out.push(tag('default unknown', 'unread'));
    const d = v0(a).detail;
    if (d === 'attribute-only') out.push(tag('RDM slot: no ranges', 'open'));
    else if (d === 'first-function') out.push(tag('first function only', 'open'));
    return out;
  }
  // Partial (S): "4 of 6 selected" below the label; empty when all have it.
  function scopeText(a) { return a.missing ? (sel().length - a.missing) + ' of ' + sel().length + ' selected' : ''; }
  function rdmLabel(a) {
    for (const c of a.channels) {
      const m = st.models[c.entryId];
      const p = m && (m.parameters || []).find(x => x.offset === c.offset);
      if (p && p.rdmSlotLabel) return p.rdmSlotLabel;
    }
    return '';
  }

  // --- status and server reports ---------------------------------------------
  function say(lines, tone) {
    if (!els.status) return;
    clear(els.status);
    els.status.className = 'b5-cc-status' + (tone ? ' b5-cc-status--' + tone : '');
    [].concat(lines).filter(Boolean).forEach(l => els.status.appendChild(h('p', {}, ic(tone === 'error' ? 'status-error' : tone === 'warn' ? 'status-warning' : 'status-ok'), l)));
  }
  // report shows what the server said about a write: mode masters it moved
  // (or could not), and targets it skipped. Silent when there is nothing.
  function report(res) {
    if (!res) return;
    const lines = [];
    const notes = new Set();
    (res.modeMasters || []).forEach(m => {
      const n = m.note || (m.changed ? m.masterAttribute + ' moved to ' + m.value + ' so ' + m.function + ' works.' : '');
      if (n && !notes.has(n)) { notes.add(n); lines.push(n); }
    });
    const skipped = res.skipped || [];
    if (skipped.length) lines.push('Not set on ' + skipped.length + ': ' + skipped[0].reason + (skipped.length > 1 ? ' (and ' + (skipped.length - 1) + ' more)' : ''));
    if (lines.length) say(lines, 'warn');
  }

  // send posts one programmer write; a stale-revision refusal is retried
  // once (ProgrammerSync has re-read the state by the time it throws).
  async function send(action, body) {
    try {
      return await ProgrammerSync.act(action, body);
    } catch (e) {
      if (/another browser/.test(e.message)) return ProgrammerSync.act(action, body);
      throw e;
    }
  }
  async function write(action, body, done) {
    try {
      const res = await send(action, body);
      if (done) say(typeof done === 'function' ? done(res) : done);
      report(res);
      return res;
    } catch (e) {
      say(e.message, 'error');
      return null;
    }
  }
  // lane: latest-wins, rate-limited writes for one control.
  function lane(key, action, body) {
    let l = lanes.get(key);
    if (!l) { l = { pending: null, busy: false, last: 0, timer: null }; lanes.set(key, l); }
    l.pending = { action, body };
    pump(l);
  }
  function pump(l) {
    if (l.busy || !l.pending) return;
    const wait = l.last + LANE_MS - now();
    if (wait > 0) {
      if (!l.timer) l.timer = setTimeout(() => { l.timer = null; pump(l); }, wait);
      return;
    }
    const job = l.pending;
    l.pending = null;
    l.busy = true;
    l.last = now();
    write(job.action, job.body).finally(() => { l.busy = false; pump(l); });
  }

  // Every attribute write names its targets: exactly the selected fixtures
  // and cells that have the attribute, so a mixed selection never reports
  // "not set" for the ones that simply lack it (the card already says
  // "on n of m").
  function tgt(a) {
    const seen = new Set();
    return a.channels.map(c => ({ entryId: c.entryId, cell: c.cell })).filter(t => { const k = t.entryId + '\u0000' + t.cell; if (seen.has(k)) return false; seen.add(k); return true; });
  }

  // vview is attribute a narrowed to one channel layout (variant vi): its
  // channels, their shared value or MIXED. Function controls are built per
  // layout and target only the fixtures with that layout, so every write
  // names the function by its index in that fixture type's own list — a
  // mixed selection of types never needs the types to agree on names.
  function vview(a, vi) {
    const channels = a.channels.filter(c => c.variant === vi).map(c => Object.assign({}, c, { variant: 0 }));
    const vals = channels.map(c => c.value);
    const mixed = vals.some(v => v !== vals[0]);
    return Object.assign({}, a, { variants: [a.variants[vi]], channels, mixed, value: mixed || !vals.length ? null : vals[0] });
  }
  function layoutName(a, vi) {
    const ids = [...new Set(a.channels.filter(c => c.variant === vi).map(c => c.entryId))];
    const types = [...new Set(ids.map(id => (st.models[id] && st.models[id].fixtureType) || 'fixture type not known'))];
    return types.join(', ') + ' · ' + ids.length + (ids.length === 1 ? ' fixture' : ' fixtures');
  }

  // --- generic controls ------------------------------------------------------
  let uid = 0;
  // A fader over one function's DMX range, writing fractions within it
  // (component-specs §6). fi = -1: an attribute with no function list (RDM
  // slot only) gets one fader over its whole channel, no function reference.
  // Keyboard per contract K: arrows 1 % of the function span (at least one
  // count), Shift+arrow a tenth of that, PageUp/Down 10 %, Home/End the
  // function's ends; exact numeric entry beside it. Fine ON: a pointer drag
  // travels 1:10 over the COMPLETE value (no jump on touch; it carries
  // across the coarse/fine bytes because the write is the whole value), and
  // ± step one DMX count. A mixed selection gets one absolute value for all.
  function functionFader(a, fi, key, vi) {
    const whole = fi < 0;
    const f = whole ? { name: a.attribute + ' (whole channel)', dmxFrom: 0, dmxTo: v0(a).max } : v0(a).functions[fi];
    const ref = whole ? {} : { functionIndex: fi };
    const span = f.dmxTo - f.dmxFrom;
    const bc = v0(a).byteCount || 1;
    const id = 'b5-cc-f' + (++uid);
    const clamp = v => Math.max(f.dmxFrom, Math.min(f.dmxTo, v));
    const input = h('input', { type: 'range', id, class: 'b5-range-touch', min: f.dmxFrom, max: f.dmxTo, step: 1, 'data-fader': key,
      'aria-label': a.attribute + ' · ' + f.name + ' (' + f.dmxFrom + '–' + f.dmxTo + ')', 'aria-describedby': id + '-hint' });
    const val = h('output', { class: 'b5-text-mono b5-cc-faderval', for: id, 'data-fader-value': key });
    const state = h('span', { class: 'b5-cc-fnstate', 'data-fader-state': key });
    const band = h('span', { class: 'b5-cc-band', 'aria-hidden': 'true', 'data-mixed-band': key, hidden: true });
    const veil = h('span', { class: 'b5-cc-veil', 'aria-hidden': 'true', 'data-fine-veil': key });
    const exact = h('input', { type: 'number', class: 'b5-input b5-cc-num b5-cc-exact', min: f.dmxFrom, max: f.dmxTo, step: 1, inputmode: 'numeric',
      'data-fader-exact': key, 'aria-label': f.name + ' exact DMX value (' + f.dmxFrom + '–' + f.dmxTo + ')' });
    let touchedAt = 0;
    let shownRef = '';
    const show = (v, active) => {
      if (!active) { val.textContent = 'not active'; state.textContent = ''; return; }
      const r = vfmt(a.attribute, v, v0(a).max, whole ? null : f, bc);
      val.textContent = r.main + ' · ' + r.sub + (r.bits ? ' · ' + r.bits : '');
      state.textContent = whole ? '' : 'ACTIVE';
    };
    const push = v => {
      touchedAt = now();
      shownRef = '';
      show(v, true);
      exact.value = v;
      input.setAttribute('aria-valuetext', num(v) + ' DMX, ' + pct(v - f.dmxFrom, span) + ' %, manual set');
      (reg.get(a.attribute + '#pad') || { local() {} }).local(v);
      lane(key, 'set', Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { fraction: span ? (v - f.dmxFrom) / span : 0 }));
    };
    const goTo = v => { v = clamp(v); input.value = v; push(v); };
    input.addEventListener('input', () => push(Number(input.value)));
    input.addEventListener('change', () => push(Number(input.value)));
    const unit1 = Math.max(1, Math.round(span / 100));
    input.addEventListener('keydown', ev => {
      const small = Math.max(1, Math.round(unit1 / 10)), big = Math.max(1, Math.round(span / 10));
      const by = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[ev.key];
      const cur = Number(input.value);
      let v;
      if (by) v = cur + by * (ev.shiftKey ? small : unit1);
      else if (ev.key === 'PageUp') v = cur + big;
      else if (ev.key === 'PageDown') v = cur - big;
      else if (ev.key === 'Home') v = f.dmxFrom;
      else if (ev.key === 'End') v = f.dmxTo;
      else return;
      if (ev.preventDefault) ev.preventDefault();
      goTo(v);
    });
    // Fine drag: the veil covers the track only while Fine is ON (CSS), so
    // the native thumb never jumps to the touch point.
    let fd = null;
    veil.addEventListener('pointerdown', ev => {
      if (!st.fine) return;
      const r = input.getBoundingClientRect();
      if (!r.width) return;
      fd = { id: ev.pointerId, x0: ev.clientX, w: r.width, v0: Number(input.value) };
      try { veil.setPointerCapture(ev.pointerId); } catch (_) { /* tests */ }
      if (ev.preventDefault) ev.preventDefault();
    });
    veil.addEventListener('pointermove', ev => {
      if (!fd || ev.pointerId !== fd.id) return;
      const v = clamp(Math.round(fd.v0 + (ev.clientX - fd.x0) / fd.w * span * 0.1));
      if (v !== Number(input.value)) { input.value = v; push(v); }
    });
    ['pointerup', 'pointercancel', 'lostpointercapture'].forEach(t => veil.addEventListener(t, () => { fd = null; }));
    exact.addEventListener('keydown', ev => { if (ev.key === 'Escape') { exact.value = input.value; if (ev.preventDefault) ev.preventDefault(); } });
    exact.addEventListener('change', () => {
      const s = String(exact.value).trim();
      const v = Number(s);
      if (s === '' || v !== Math.round(v) || v < f.dmxFrom || v > f.dmxTo) {
        say(f.name + ': type a whole DMX value from ' + f.dmxFrom + ' to ' + f.dmxTo + '; nothing was sent.', 'error');
        exact.value = input.value;
        return;
      }
      goTo(v);
    });
    const step = d => () => goTo(Number(input.value) + d * (st.fine ? 1 : unit1));
    const row = h('div', { class: 'b5-param b5-cc-fader', 'data-function-row': key },
      h('div', { class: 'b5-cc-faderhead' },
        h('label', { class: 'b5-param__label', for: id }, fnIcon(f) ? ic(fnIcon(f)) : null, h('span', { text: f.name }),
          h('span', { class: 'b5-caption', 'data-fader-caption': key, text: ' DMX ' + num(f.dmxFrom) + '–' + num(f.dmxTo) + (physLine(f) ? ' · ' + physLine(f) : '') })),
        state, exact),
      h('div', { class: 'b5-cc-faderrow' },
        btn('−', { 'aria-label': 'Step ' + f.name + ' down', 'data-step-down': key }, step(-1)),
        h('span', { class: 'b5-cc-track' }, band, input, veil),
        btn('+', { 'aria-label': 'Step ' + f.name + ' up', 'data-step-up': key }, step(1))),
      val,
      h('p', { class: 'b5-cc-hint', id: id + '-hint', text: 'Arrows 1 %, Shift+arrow a tenth of that, Page Up/Down 10 %, Home/End the ends.' }));
    const update = at => {
      const v = chanValue(at), active = whole || activeFn(at) === fi;
      // Mixed band: the range the selection spans inside this function.
      const vs = at.channels.map(c => c.value);
      const inFn = active && at.mixed && vs.length > 1 && span > 0;
      band.hidden = !inFn;
      if (inFn) {
        band.style.setProperty('left', (clamp(Math.min(...vs)) - f.dmxFrom) / span * 100 + '%');
        band.style.setProperty('right', (f.dmxTo - clamp(Math.max(...vs))) / span * 100 + '%');
      }
      if (now() - touchedAt < ACTIVE_MS) return;
      input.value = active ? v : f.dmxFrom;
      exact.value = active ? v : '';
      const source = at.allTouched ? 'manual set' : at.touched ? 'set on some' : at.unknownDefault ? 'default not stated' : 'profile default';
      shownRef = inFn ? 'MIXED ' + num(Math.min(...vs)) + '–' + num(Math.max(...vs)) + '; slider shows the first selected fixture\'s ' + num(v) + ' as a reference' : '';
      input.setAttribute('aria-valuetext', !active ? 'not active' : shownRef || num(v) + ' DMX, ' + pct(v - f.dmxFrom, span) + ' %, ' + source);
      show(v, active);
      if (shownRef) val.textContent = mixfmt(vs, v0(at).max, whole ? null : f).main + ' · reference (first selected) ' + num(v);
    };
    reg.set(key, { attr: a.attribute, vi, update, local: v => { if (now() - touchedAt >= ACTIVE_MS) { input.value = v; exact.value = v; show(v, true); } } });
    update(a);
    return row;
  }

  // A press-and-hold button (Control channels): fires after HOLD_MS of a
  // continuous press, by pointer or by Space/Enter held down.
  function holdButton(label, attrs, fire) {
    const fill = h('span', { class: 'b5-cc-hold__fill', 'aria-hidden': 'true' });
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn b5-btn--sm b5-cc-hold', 'aria-description': 'Hold for 0.75 seconds to send once' }, attrs || {}),
      fill, h('span', { class: 'b5-cc-hold__word', text: 'HOLD 0.75 s · ' }), label);
    b.disabled = !liveNow();
    let timer = null;
    const start = ev => {
      if (ev && ev.preventDefault) ev.preventDefault();
      if (timer || b.disabled || !liveNow()) return;
      b.classList.add('is-holding');
      timer = setTimeout(() => { timer = null; b.classList.remove('is-holding'); if (liveNow()) fire(); }, HOLD_MS);
    };
    const stop = () => { if (timer) { clearTimeout(timer); timer = null; } b.classList.remove('is-holding'); };
    b.addEventListener('pointerdown', start);
    ['pointerup', 'pointerleave', 'pointercancel'].forEach(t => b.addEventListener(t, stop));
    b.addEventListener('keydown', ev => {
      if (ev.key === 'Escape') return stop();
      if ((ev.key === ' ' || ev.key === 'Enter') && !ev.repeat) start(ev);
    });
    b.addEventListener('keyup', ev => { if (ev.key === ' ' || ev.key === 'Enter') stop(); });
    b.addEventListener('blur', stop);
    b.addEventListener('click', ev => { if (ev && ev.preventDefault) ev.preventDefault(); });
    return b;
  }

  // I2d2 command progress: the server's GET /api/programmer "commands"
  // (re-read on every {"type":"commands"} broadcast), so every browser shows
  // the same thing, in words: SENDING with the time left while a command is
  // on the wire, then DONE (the channels are back) or STOPPED (Disarm or a
  // lost lease cut it short). The end line shows for CMD_END_MS in a
  // browser that saw the command in flight; a browser that opens later is
  // not told about an old one.
  const CMD_END_MS = 20000;
  const cmdSeen = new Map(); // id -> client time it ends (from remainingMs)
  let cmdEnded = null;       // { view, until }
  let cmdTick = null;
  function cmdWho(c) {
    const f = c.fixtures || [];
    return f.length <= 3 ? f.join(', ') : f.slice(0, 2).join(', ') + ' and ' + (f.length - 2) + ' more';
  }
  function drawCommands() {
    if (!els.cmd) return;
    const p = prog();
    const c = (p && p.commands) || { active: [], last: null };
    const now = Date.now();
    const active = c.active || [];
    active.forEach(a => { if (!cmdSeen.has(a.id)) cmdSeen.set(a.id, now + a.remainingMs); });
    const last = c.last;
    if (last && cmdSeen.has(last.id)) {
      cmdSeen.delete(last.id);
      cmdEnded = { view: last, until: now + CMD_END_MS };
    }
    clear(els.cmd);
    active.forEach(a => {
      const left = Math.max(0, Math.ceil(((cmdSeen.get(a.id) || now) - now) / 1000));
      els.cmd.appendChild(h('p', { 'data-cmd-progress': 'sending' }, ic('status-pending'), h('span', {}, h('strong', { text: 'SENDING' }),
        ' ' + a.name + ' to ' + cmdWho(a) + ' — the channel returns to its previous value in ' + left + ' s.')));
    });
    if (cmdEnded && now < cmdEnded.until && !active.some(a => a.id === cmdEnded.view.id)) {
      const e = cmdEnded.view;
      els.cmd.appendChild(e.state === 'done'
        ? h('p', { 'data-cmd-progress': 'done' }, ic('status-ok'), h('span', {}, h('strong', { text: 'DONE' }), ' ' + e.name + ' to ' + cmdWho(e) + ': sent once; the channel is back to its previous value.'))
        : h('p', { 'data-cmd-progress': 'stopped' }, ic('status-warning'), h('span', {}, h('strong', { text: 'STOPPED' }), ' ' + e.name + ' to ' + cmdWho(e) + ' was cut short because output stopped. Nothing is kept to send later.')));
    }
    els.cmd.hidden = !els.cmd.firstChild;
    if (cmdTick) { clearTimeout(cmdTick); cmdTick = null; }
    if (active.length || (cmdEnded && now < cmdEnded.until)) cmdTick = setTimeout(() => { cmdTick = null; drawCommands(); }, 500);
  }

  // sentOnce: the status line after a one-shot command (I2d2).
  function sentOnce(name) {
    return res => 'Sent ' + name + ' once' + (res && res.windowMs ? ' for ' + res.windowMs / 1000 + ' s' : '') + '; the channel then returns to its previous value.';
  }
  function slotLabel(s) {
    const n = s.hasWheelSlot ? 'Slot ' + s.wheelSlot : '';
    const name = s.hasSlotDetail && s.slotName ? s.slotName : s.name;
    return [n, name].filter(Boolean).join(' · ');
  }
  // fnIcon: the sprite symbol for a channel function's attribute (decor
  // beside its words, never the name). Rotation direction only from the
  // profile's own words; unknown direction draws no direction.
  function fnIcon(f) {
    const a = (f && f.attribute) || '';
    if (/Reset/.test(a)) return 'reset';
    if (/^Lamp/.test(a)) return 'lamp';
    if (/Shake/.test(a)) return 'gobo-shake';
    if (/Rotate|Spin/.test(a)) return /\b(ccw|counter|anti|backwards?)\b/i.test(f.name) ? 'gobo-rotate-ccw' : /\b(cw|clockwise|forwards?)\b/i.test(f.name) ? 'gobo-rotate-cw' : '';
    if (/^Prism/.test(a)) return 'prism';
    if (/^Frost/.test(a)) return 'frost';
    if (/^Iris/.test(a)) return 'iris';
    if (/Zoom/.test(a)) return 'zoom';
    if (/^Focus/.test(a)) return 'focus';
    if (/Strobe/.test(a)) return 'strobe';
    if (/^(Blade|Shaper)/.test(a)) return 'shaper';
    return '';
  }
  // inCount: how many of the layout's channels sit inside [from, to]
  // (inside function fi too, when fi >= 0), of how many.
  function inCount(at, fi, from, to) {
    const n = at.channels.filter(c => (fi < 0 || c.function === fi) && c.value >= from && c.value <= to).length;
    return [n, at.channels.length];
  }
  function inWord(n, m) { return !m || !n ? '' : n === m ? 'IN' : 'IN on ' + n + ' of ' + m; }
  // A fixture command never fires on a single tap and never goes to /set
  // (§7, §12; I2d3). The SERVER decides which functions are commands
  // (patch.IsCommandFunction: every Control-family function, and any Reset
  // or Lamp function) and marks them f.command, so this UI and
  // /api/programmer/set can never disagree.
  const isCommand = f => !!(f && f.command);

  // --- §12 fixture commands: Reset and Lamp off ask first -------------------
  // Owner (2026-10-08): a confirmation dialog for Lamp off and Reset,
  // superseding the 0.75 s hold for those two. Every other Control
  // function keeps the hold (§12 "optional hold component ... for existing
  // non-Reset/Lamp-off controls"). Reset: the function's attribute says so
  // (GDTF FixtureGlobalReset, PanReset, ...) or its name does. Lamp off:
  // the NAME says lamp and off — LampControl also carries "Lamp On" and
  // "Home position off", which are not lamp-off commands.
  function commandKind(attribute, name) {
    if (/Reset/.test(attribute || '') || /\breset\b/i.test(name || '')) return 'reset';
    if (/\blamp\b/i.test(name || '') && /\boff\b/i.test(name || '')) return 'lampoff';
    return null;
  }
  // cmds collects the commands met while a panel renders; render() draws
  // them in one caution section at the end of the panel, away from the
  // frequent controls.
  let cmds = [];
  const cmdDialogs = new Map(); // command key -> its open dialog (one each)
  let cmdSeq = 0;
  // resolveCmd re-reads a command against the CURRENT programmer view: the
  // attribute, its channel layout, the function (by index AND name) and
  // the set/slot must still exist; the targets are that layout's channels
  // now. null when the command no longer applies.
  function resolveCmd(c) {
    const a = findAttr(c.attr);
    if (!a || !a.variants[c.vi]) return null;
    const av = vview(a, c.vi);
    const f = v0(av).functions[c.fi];
    if (!f || f.name !== c.fnName) return null;
    if (c.extra.set !== undefined && !f.sets.some(s => !s.hasWheelSlot && s.name === c.extra.set)) return null;
    if (c.extra.slot !== undefined && !f.sets.some(s => s.hasWheelSlot && s.wheelSlot === c.extra.slot)) return null;
    const targets = tgt(av);
    if (!targets.length) return null;
    return { targets, key: targets.map(t => t.entryId + '/' + t.cell).join(',') };
  }
  function targetName(t) {
    const x = sel().find(s => s.entryId === t.entryId && (s.cell || '') === (t.cell || ''))
      || sel().find(s => s.entryId === t.entryId);
    const name = (x && x.name) || 'Unnamed fixture';
    return t.cell ? name + ' · cell ' + ((x && x.cellIndex) || t.cell) : name;
  }
  function cmdButton(c) {
    const b = h('button', { type: 'button', class: 'b5-btn b5-cc-cmd', 'aria-haspopup': 'dialog', 'data-cmd': c.key, title: c.label + ' — ' + c.attr },
      ic(c.kind === 'reset' ? 'reset' : 'lamp'), h('span', {}, h('span', { class: 'b5-fromfile', text: c.label }), '…'));
    b.addEventListener('click', () => confirmCommand(c, b));
    return b;
  }
  function cmdZone(list) {
    const seen = new Map();
    list.forEach(c => seen.set(c.label, (seen.get(c.label) || 0) + 1));
    return h('section', { class: 'b5-cc-cmdzone', 'data-cmd-zone': '', 'aria-labelledby': 'b5-cc-cmdzone-head' },
      h('h4', { id: 'b5-cc-cmdzone-head' }, ic('status-warning'), 'Fixture commands'),
      h('p', { class: 'b5-caption', text: 'Reset temporarily interrupts fixture control; Lamp off puts the lamp out.' }),
      h('div', { class: 'b5-row b5-cc-cmdrow' }, list.map(c => {
        const b = cmdButton(c);
        if (seen.get(c.label) > 1) b.appendChild(h('span', { class: 'b5-caption', text: ' · ' + c.attr }));
        return b;
      })),
      h('p', { class: 'b5-caption', text: 'Each command asks for confirmation. The selection is checked again before anything is sent.' }));
  }
  // confirmCommand (§12): a NON-modal dialog — the master strip and Disarm
  // stay operable — naming the actual targets and the consequence, Cancel
  // focused, an explicit action word, Escape cancels, focus goes back to
  // the trigger. On confirm it re-reads the programmer and re-resolves the
  // command; if the targets or the command changed it redraws and asks
  // again. The write names its targets explicitly and carries this
  // browser's revision, so a change that lands in between is refused by
  // the server (409) and also asks again — never a silent retry.
  function confirmCommand(c, trigger) {
    const open = cmdDialogs.get(c.key);
    if (open) { const cancel0 = open.querySelector('[data-cmd-cancel]'); if (cancel0) cancel0.focus(); return; }
    const id = 'b5-cc-cmd-' + (++cmdSeq);
    const title = h('h3', { id: id + '-t' });
    const list = h('ul', { class: 'b5-cc-cmdlist', 'data-cmd-targets': '' });
    const what = h('p', { id: id + '-d' });
    const outNote = h('p', { class: 'b5-caption', 'data-cmd-output': '' });
    const changed = h('p', { class: 'b5-cc-cmdchanged', role: 'alert', 'data-cmd-changed': '' });
    const cancel = btn('Cancel', { 'data-cmd-cancel': '' });
    const go = btn(c.kind === 'reset' ? 'Reset fixtures' : 'Switch off lamps', { 'data-cmd-confirm': '' }, null, 'b5-cc-cmd');
    const dlg = h('dialog', { class: 'b5-cc-cmddialog', 'data-cmd-dialog': c.key, 'aria-labelledby': id + '-t', 'aria-describedby': id + '-d' },
      title, what, list, outNote, changed, h('div', { class: 'b5-row b5-con-dialog__actions' }, cancel, go));
    let shown = null;
    let busy = false;
    let repeatKey = false;
    const draw = () => {
      shown = resolveCmd(c);
      clear(list);
      if (!shown) {
        title.textContent = c.label + ' — not available';
        what.textContent = 'The selection no longer has ' + c.label + ' on ' + c.attr + '. Nothing will be sent.';
        go.disabled = true;
        return;
      }
      const n = new Set(shown.targets.map(t => t.entryId)).size;
      title.textContent = c.kind === 'reset' ? 'Reset ' + n + (n === 1 ? ' fixture?' : ' fixtures?') : 'Switch off ' + n + (n === 1 ? ' lamp?' : ' lamps?');
      shown.targets.forEach(t => list.appendChild(h('li', { text: targetName(t) })));
      what.textContent = (c.kind === 'reset'
        ? (n === 1 ? 'This fixture will reset' : 'These fixtures will reset') + ' (' + c.label + '). Their light and movement may stop while they restart.'
        : (n === 1 ? 'This lamp will go out' : 'These lamps will go out') + ' (' + c.label + '). Some lamps need time before they can restart.');
      // I2d2: disarmed, the dialog says so and offers only Cancel — a
      // command is never kept to fire at the next Arm.
      const live = liveNow();
      outNote.textContent = live ? 'Sent once: after ' + CMD_SECONDS + ' s each channel returns to its previous value. Nothing is kept in the programmer.'
        : NOT_LIVE + ' Arm output to send this command.';
      go.hidden = !live;
      go.disabled = !live;
      if (!live && document.activeElement === go) cancel.focus();
    };
    const redraw = () => { if (!busy) draw(); };
    const close = () => {
      if (!cmdDialogs.has(c.key)) return;
      cmdDialogs.delete(c.key);
      liveSubs.delete(redraw);
      try { if (dlg.open) dlg.close(); } catch (_) { /* closed */ }
      dlg.remove();
      // The panel may have redrawn while the dialog was open: the trigger
      // is then a new element with the same key.
      const back = trigger && trigger.isConnected !== false && document.body.contains(trigger) ? trigger
        : (els.body && els.body.querySelector('[data-cmd="' + c.key + '"]'));
      if (back) back.focus();
    };
    cancel.addEventListener('click', close);
    dlg.addEventListener('keydown', ev => {
      if (ev.key === 'Escape') { if (ev.preventDefault) ev.preventDefault(); close(); return; }
      repeatKey = !!ev.repeat;
    });
    dlg.addEventListener('cancel', ev => { if (ev.preventDefault) ev.preventDefault(); close(); });
    go.addEventListener('click', async () => {
      // A held key auto-repeating onto this button is not a decision.
      if (busy || go.disabled || repeatKey) return;
      busy = true;
      try {
        const before = shown;
        await ProgrammerSync.refresh();
        const now = resolveCmd(c);
        if (!liveNow()) { draw(); return; }
        if (!now || !before || now.key !== before.key) {
          draw();
          changed.textContent = 'The selection changed since this opened. Check the list and confirm again.';
          if (go.disabled) cancel.focus();
          return;
        }
        const body = Object.assign({ targets: now.targets, attribute: c.attr, functionIndex: c.fi }, c.extra);
        try {
          const res = await ProgrammerSync.act('command', body);
          close();
          say('Sent ' + c.label + ' once to ' + now.targets.length + ' target(s); the channel then returns to its previous value.');
          report(res);
        } catch (e) {
          if (/another browser/.test(e.message)) {
            draw();
            changed.textContent = 'The programmer changed while sending, so nothing was sent. Check the list and confirm again.';
          } else if (e.status === 412) {
            outState = 'unknown';
            draw();
            changed.textContent = e.message;
          } else changed.textContent = e.message;
        }
      } finally { busy = false; }
    });
    draw();
    liveSubs.add(redraw);
    cmdDialogs.set(c.key, dlg);
    document.body.appendChild(dlg);
    dlg.show();
    cancel.focus();
  }

  // rangeRail (§7): the channel's functions as proportional segments on
  // the channel's whole DMX span (overlapping functions — mode-master
  // dependent — stack in lanes), plus one full-size label button per
  // function, from the file. A tap jumps to the function's own default
  // when the file states one inside it, otherwise its first value; never
  // a guessed midpoint. The segment holding the selection is IN.
  function rangeRail(a, vi, k, fns) {
    const max = v0(a).max || 1;
    const vis = fns.map((f, i) => [f, i]).filter(([f]) => !HIDDEN_FN.test(f.attribute));
    const lanes = [];
    const laneOf = new Map();
    vis.slice().sort((x, y) => x[0].dmxFrom - y[0].dmxFrom).forEach(([f, i]) => {
      let l = lanes.findIndex(end => end < f.dmxFrom);
      if (l < 0) { l = lanes.length; lanes.push(-1); }
      lanes[l] = f.dmxTo;
      laneOf.set(i, l);
    });
    const track = h('div', { class: 'b5-cc-segtrack', 'aria-hidden': 'true', style: { height: (lanes.length * 16 + 4) + 'px' } });
    const labels = h('div', { class: 'b5-cc-seglabels', role: 'group', 'aria-label': a.attribute + ' functions on this channel' });
    const segs = [];
    vis.forEach(([f, i]) => {
      const seg = h('span', { class: 'b5-cc-seg', 'data-seg-bar': k(i), style: { left: f.dmxFrom / (max + 1) * 100 + '%', width: Math.max(0.6, (f.dmxTo - f.dmxFrom + 1) / (max + 1) * 100) + '%', top: (laneOf.get(i) * 16 + 2) + 'px' } });
      track.appendChild(seg);
      const at = f.hasDefault && f.default >= f.dmxFrom && f.default <= f.dmxTo ? f.default : f.dmxFrom;
      const body = Object.assign({ targets: tgt(a), attribute: a.attribute }, { functionIndex: i, dmx: at });
      const state = h('span', { class: 'b5-cc-in', 'data-seg-state': k(i) });
      const words = [fnIcon(f) ? ic(fnIcon(f)) : null, h('span', { class: 'b5-cc-seg__name b5-fromfile', text: f.name }),
        h('span', { class: 'b5-cc-seg__range', text: 'DMX ' + num(f.dmxFrom) + '–' + num(f.dmxTo) + (f.modeMaster ? ' · needs ' + f.modeMaster : '') }), state];
      // I2d3: a command function is only labelled on the rail; its HOLD or
      // Fixture-commands entry is drawn once, by functionControls.
      const kind = isCommand(f) ? commandKind(f.attribute, f.name) : null;
      const b = isCommand(f) ? h('span', { class: 'b5-cc-seglabel b5-cc-seglabel--cmd', 'data-seg': k(i) }, words, h('span', { class: 'b5-caption', text: kind ? ' · in Fixture commands' : ' · HOLD below' }))
        : h('button', { type: 'button', class: 'b5-btn b5-btn--sm b5-cc-seglabel', 'data-seg': k(i), title: f.name + ' — jumps to DMX ' + at, 'aria-pressed': 'false' }, words);
      if (!isCommand(f)) b.addEventListener('click', () => write('set', body));
      labels.appendChild(b);
      segs.push([seg, b, state, i]);
    });
    const cursor = h('span', { class: 'b5-cc-segcursor', 'data-seg-cursor': a.attribute + '#' + vi });
    track.appendChild(cursor);
    reg.set('rail#' + a.attribute + '#' + vi, { attr: a.attribute, vi, update: at => {
      segs.forEach(([seg, b, state, i]) => {
        const [n, m] = inCount(at, i, fns[i].dmxFrom, fns[i].dmxTo);
        const w = inWord(n, m);
        state.textContent = w ? ' · ' + w : '';
        seg.classList.toggle('is-in', n > 0);
        b.classList.toggle('is-on', !!m && n === m);
        if (b.hasAttribute('aria-pressed')) b.setAttribute('aria-pressed', m && n === m ? 'true' : 'false');
      });
      const c0 = at.channels[0];
      cursor.hidden = !c0;
      if (c0) cursor.style.setProperty('left', c0.value / (max + 1) * 100 + '%');
    } });
    reg.get('rail#' + a.attribute + '#' + vi).update(a);
    return h('div', { class: 'b5-cc-rail', 'data-rail': a.attribute + '#' + vi },
      h('p', { class: 'b5-caption', text: 'Functions on this channel, names from the fixture file. A tap jumps to the function\'s stated default, else its first value.' }),
      track, labels);
  }

  // shutterControl (§10): OPEN / CLOSED as a radio group, from the channel
  // sets the file names "open" / "closed" in the Shutter function itself.
  // Nothing is inferred from intensity; with no such sets there is no
  // control. Returns the element and the sets it covers (not drawn again).
  function shutterControl(a, vi, key) {
    const fns = v0(a).functions;
    let open = null, closed = null;
    fns.forEach((f, i) => {
      if (!/^Shutter\d+$/.test(f.attribute)) return;
      f.sets.forEach(s => {
        if (s.hasWheelSlot || !s.name) return;
        if (!closed && /\bclos(e|ed)\b/i.test(s.name)) closed = { i, s };
        else if (!open && /\bopen\b/i.test(s.name)) open = { i, s };
      });
    });
    if (!open || !closed) return null;
    const choice = (c, word, icon) => {
      const body = Object.assign({ targets: tgt(a), attribute: a.attribute }, { functionIndex: c.i, set: c.s.name });
      const b = h('button', { type: 'button', role: 'radio', class: 'b5-seg b5-cc-shut', 'aria-checked': 'false', tabindex: '-1', 'data-shutter': key + '#' + word.toLowerCase(),
        'aria-label': word + ' (' + c.s.name + ', DMX ' + c.s.dmxFrom + '–' + c.s.dmxTo + ')' }, ic(icon), h('span', { class: 'b5-cc-shut__word', text: word }));
      b.addEventListener('click', () => write('set', body));
      return b;
    };
    const bo = choice(open, 'OPEN', 'shutter-open'), bc = choice(closed, 'CLOSED', 'shutter-closed');
    const group = h('div', { class: 'b5-segmented b5-cc-shutter', role: 'radiogroup', 'aria-label': a.attribute + ': OPEN or CLOSED', 'data-shutter-group': key }, bo, bc);
    group.addEventListener('keydown', ev => {
      const to = { ArrowRight: bc, ArrowDown: bc, ArrowLeft: bo, ArrowUp: bo }[ev.key];
      if (!to) return;
      if (ev.preventDefault) ev.preventDefault();
      to.focus();
      to.click();
    });
    const msg = h('p', { class: 'b5-cc-shutstate', 'data-shutter-state': key });
    reg.set('shutter#' + key, { attr: a.attribute, vi, update: at => {
      const [nc, m] = inCount(at, closed.i, closed.s.dmxFrom, closed.s.dmxTo);
      const [no] = inCount(at, open.i, open.s.dmxFrom, open.s.dmxTo);
      const isC = m > 0 && nc === m, isO = m > 0 && no === m;
      bc.setAttribute('aria-checked', isC ? 'true' : 'false'); bc.classList.toggle('is-on', isC);
      bo.setAttribute('aria-checked', isO ? 'true' : 'false'); bo.classList.toggle('is-on', isO);
      bo.setAttribute('tabindex', isC ? '-1' : '0'); bc.setAttribute('tabindex', isC ? '0' : '-1');
      group.classList.toggle('is-closed', isC);
      const c0 = at.channels[0];
      const fnName = c0 && c0.function >= 0 && v0(at).functions[c0.function] ? v0(at).functions[c0.function].name : 'no single function';
      msg.textContent = isC ? 'CLOSED · beam blocked' : isO ? 'OPEN' : nc > 0 ? 'MIXED · closed on ' + nc + ' of ' + m : 'Neither OPEN nor CLOSED · ' + (at.mixed ? 'MIXED values' : 'in ' + fnName);
      msg.classList.toggle('is-closed', isC);
    } });
    reg.get('shutter#' + key).update(a);
    const el = h('div', { class: 'b5-cc-shutbox' }, group, msg,
      h('p', { class: 'b5-caption' }, 'From the file: OPEN = ', h('span', { class: 'b5-fromfile', text: open.s.name }), ', CLOSED = ', h('span', { class: 'b5-fromfile', text: closed.s.name }), '. Strobe rate is its own fader below.'));
    return { el, covers: new Set([open.i + '#' + open.s.name, closed.i + '#' + closed.s.name]) };
  }

  // slotContent (§8): what a wheel slot button shows. Colour wheels: the
  // slot's stated colour as capped content, else a placeholder disc +
  // "colour not reported". Other wheels: a generic gobo glyph — the
  // artwork is never invented — with the media file named, or "media not
  // reported". An Open slot is a real slot with its own glyph.
  function slotContent(f, a, s) {
    const name = (s.hasSlotDetail && s.slotName) || s.name || '';
    const isOpen = /^open\b/i.test(name);
    const colourWheel = /^Color/.test(f.attribute) || /^Color/.test(a.attribute);
    let glyph, note = '', missing = false;
    if (isOpen) glyph = ic('slot-open');
    else if (colourWheel) {
      if (s.hasSRGB) glyph = h('span', { class: 'b5-cc-swatch b5-cc-swatch--slot', 'aria-hidden': 'true' }, h('span', { class: 'b5-cc-swatch__c', style: { background: s.srgb } }));
      else { glyph = ic('colour-slot-placeholder'); note = 'colour not reported'; missing = true; }
    } else {
      glyph = ic('gobo-placeholder');
      if (s.mediaFileName) note = 'image ' + s.mediaFileName + ' · not shown';
      else { note = 'media not reported'; missing = true; }
    }
    return { glyph, note, missing, name: name || ('Slot ' + s.wheelSlot), fromFile: !!name };
  }

  // attributeCard: everything for one attribute.
  function attributeCard(a, group, title) {
    const control = group === 'other';
    // Anatomy (§6): label → scope → readout → source word → reason.
    const virt = a.variants.some(v => v.virtual);
    const ro = h('span', { class: 'b5-text-mono b5-cc-readout', 'data-readout': a.attribute });
    fillReadout(ro, a);
    const head = h('summary', { class: 'b5-cc-attrhead' },
      h('span', { class: 'b5-cc-attrlabel' }, h('strong', { class: 'b5-cc-attrname', text: title || a.attribute }), title ? h('span', { class: 'b5-caption', text: ' ' + a.attribute }) : null,
        virt ? h('span', { class: 'b5-caption', text: ' virtual · RGB' }) : null,
        h('span', { class: 'b5-caption b5-cc-scope', 'data-scope': a.attribute, text: scopeText(a) })),
      ro,
      h('span', { class: 'b5-cc-markers', 'data-markers': a.attribute }, markers(a)));
    const body = h('div', { class: 'b5-cc-attrbody' });
    const lbl = rdmLabel(a);
    if (lbl) body.appendChild(h('p', { class: 'b5-caption' }, 'RDM slot label: ', h('span', { class: 'b5-fromfile', text: lbl })));
    if (group === 'dimmer' && /^Dimmer$/.test(a.attribute)) body.appendChild(dimmerQuick(a));
    a.variants.forEach((_, vi) => {
      const av = vview(a, vi);
      const into = a.variants.length > 1 ? h('section', { class: 'b5-cc-variant', 'data-variant': a.attribute + '#' + vi }, h('h5', { class: 'b5-cc-varhead', text: 'For ' + layoutName(a, vi) })) : body;
      if (into !== body) body.appendChild(into);
      // §10: a Shutter(n) channel gets OPEN / CLOSED first (its strobe
      // and other functions follow as usual).
      const sh = !control && /^Shutter\d+$/.test(a.attribute) ? shutterControl(av, vi, a.attribute + '#' + vi) : null;
      if (sh) into.appendChild(sh.el);
      functionControls(av, vi, control, into, sh ? sh.covers : null);
    });
    if (control) {
      body.appendChild(h('p', { class: 'b5-note', text: 'Control channels have no faders: a sweep would pass through reset and lamp ranges. Hold a button for 0.75 s to send it once; Reset and Lamp off are under Fixture commands and ask first. A command is not kept: after ' + CMD_SECONDS + ' s the channel returns to its previous value.' }));
      const why = h('p', { class: 'b5-note', role: 'status', 'data-hold-why': '', text: NOT_LIVE });
      why.hidden = liveNow();
      body.appendChild(why);
    }
    const card = h('details', { class: 'b5-cc-attr', 'data-attr': a.attribute }, head, body);
    card.open = st.open[a.attribute] !== false;
    card.addEventListener('toggle', () => { st.open[a.attribute] = card.open; });
    reg.set(a.attribute + '#card', { attr: a.attribute, update: at => {
      const r = card.querySelector('[data-readout]');
      if (r) fillReadout(r, at);
      const m = card.querySelector('[data-markers]');
      if (m) { clear(m); markers(at).forEach(x => m.appendChild(x)); }
      const s = card.querySelector('[data-scope]');
      if (s) s.textContent = scopeText(at);
    } });
    return card;
  }

  // Dimmer quick buttons (§6): 0 / 50 % / FULL, SET at once. One write per
  // channel layout, by that layout's own Dimmer function, so FULL is the
  // top of the dimmer function, never the top of some other range.
  function dimmerQuick(a) {
    const go = fr => () => a.variants.forEach((v, vi) => {
      const av = vview(a, vi);
      if (!av.channels.length) return;
      const body = { targets: tgt(av), attribute: a.attribute };
      if (v.functions.length) body.functionIndex = ownFn(av);
      body.fraction = fr;
      write('set', body);
    });
    return h('div', { class: 'b5-row b5-cc-quickrow', role: 'group', 'aria-label': a.attribute + ' quick levels' },
      btn('0', { 'data-dimmer-quick': '0', 'aria-label': a.attribute + ' 0 %' }, go(0)),
      btn('50 %', { 'data-dimmer-quick': '0.5', 'aria-label': a.attribute + ' 50 %' }, go(0.5)),
      btn('FULL', { 'data-dimmer-quick': '1', 'aria-label': a.attribute + ' full' }, go(1)));
  }

  // functionControls: range rail, slot buttons, set buttons, function
  // faders (or HOLD buttons on a Control channel) for one channel layout
  // of an attribute. skip: "fi#set name" of sets another control covers
  // (the shutter OPEN/CLOSED group).
  function functionControls(a, vi, control, body, skip) {
    const fns = v0(a).functions;
    const k = i => a.attribute + '#' + (vi ? 'v' + vi + ':' : '') + i;
    // No function list (RDM slot only): one whole-channel fader, except on
    // Control channels, which never get a fader.
    if (!fns.length && !control) body.appendChild(functionFader(a, -1, k(-1), vi));
    if (!control && fns.filter(x => !HIDDEN_FN.test(x.attribute)).length > 1) body.appendChild(rangeRail(a, vi, k, fns));
    // A slot or set button is IN when the layout's channels sit in its DMX
    // range (in that function, for a slot); "IN on n of m" when only some.
    const mark = (b, fi, s, state) => {
      const key = 'btn#' + k(fi) + '#' + (s.hasWheelSlot ? s.wheelSlot : s.name);
      reg.set(key, { attr: a.attribute, vi, update: at => {
        const [n, m] = inCount(at, s.hasWheelSlot ? fi : -1, s.dmxFrom, s.dmxTo);
        b.setAttribute('aria-pressed', m && n === m ? 'true' : 'false');
        b.classList.toggle('is-on', !!m && n === m);
        b.classList.toggle('is-partial', n > 0 && n < m);
        if (state) state.textContent = inWord(n, m);
      } });
      reg.get(key).update(a);
    };
    const panelControl = control;
    fns.forEach((f, i) => {
      if (HIDDEN_FN.test(f.attribute)) return;
      // I2d3: a command function (server flag) is drawn as the Control
      // panel draws everything — HOLD or Fixture commands, never /set.
      const control = panelControl || isCommand(f);
      const ref = { functionIndex: i };
      const slots = f.sets.filter(s => s.hasWheelSlot);
      const named = f.sets.filter(s => !s.hasWheelSlot && s.name && !(skip && skip.has(i + '#' + s.name)));
      const box = h('div', { class: 'b5-cc-fn', 'data-function': k(i) });
      const plainSelection = slots.length && f.attribute === a.attribute;
      const single = f.dmxTo <= f.dmxFrom;
      if (fns.filter(x => !HIDDEN_FN.test(x.attribute)).length > 1 || slots.length || named.length) {
        box.appendChild(h('p', { class: 'b5-cc-fnname' }, fnIcon(f) ? ic(fnIcon(f)) : null, h('strong', { class: 'b5-fromfile', text: f.name }), h('span', { class: 'b5-caption', text: ' · DMX ' + f.dmxFrom + '–' + f.dmxTo + (f.modeMaster ? ' · needs ' + f.modeMaster : '') })));
      }
      if (slots.length) {
        const row = h('div', { class: 'b5-cc-slots', role: 'group', 'aria-label': f.name + ' slots' });
        slots.forEach(s => {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { slot: s.wheelSlot });
          const kind = control ? commandKind(f.attribute, slotLabel(s)) : null;
          if (kind) { cmds.push({ kind, key: k(i) + '#' + s.wheelSlot, label: slotLabel(s), attr: a.attribute, vi, fi: i, fnName: f.name, extra: { slot: s.wheelSlot } }); return; }
          if (control) {
            const b = holdButton(slotLabel(s), { 'data-slot': k(i) + '#' + s.wheelSlot }, () => write('command', body0, sentOnce(slotLabel(s))));
            mark(b, i, s);
            row.appendChild(b);
            return;
          }
          const sc = slotContent(f, a, s);
          const state = h('span', { class: 'b5-cc-in b5-cc-slot__in', 'data-slot-state': k(i) + '#' + s.wheelSlot });
          const b = h('button', { type: 'button', class: 'b5-cc-slot' + (sc.missing ? ' is-unreported' : ''), 'data-slot': k(i) + '#' + s.wheelSlot, 'aria-pressed': 'false' },
            h('span', { class: 'b5-cc-slot__glyph' }, sc.glyph),
            h('span', { class: 'b5-cc-slot__name' + (sc.fromFile ? ' b5-fromfile' : ''), text: sc.name }),
            h('span', { class: 'b5-cc-slot__num', text: 'Slot ' + s.wheelSlot }),
            sc.note ? h('span', { class: 'b5-cc-slot__media', text: sc.note }) : null,
            state);
          b.addEventListener('click', () => write('set', body0));
          mark(b, i, s, state);
          row.appendChild(b);
        });
        box.appendChild(row);
      }
      if (named.length) {
        const row = h('div', { class: 'b5-cc-sets', role: 'group', 'aria-label': f.name + ' states' });
        named.forEach(s => {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { set: s.name });
          const kind = control ? commandKind(f.attribute, s.name) : null;
          if (kind) { cmds.push({ kind, key: k(i) + '#' + s.name, label: s.name, attr: a.attribute, vi, fi: i, fnName: f.name, extra: { set: s.name } }); return; }
          if (control) {
            const b = holdButton(s.name, { 'data-set': k(i) + '#' + s.name }, () => write('command', body0, sentOnce(s.name)));
            mark(b, i, s);
            row.appendChild(b);
            return;
          }
          const state = h('span', { class: 'b5-cc-in', 'data-set-state': k(i) + '#' + s.name });
          const dir = /\b(ccw|counter|anti|backwards?)\b/i.test(s.name) ? 'gobo-rotate-ccw' : /\b(cw|clockwise|forwards?)\b/i.test(s.name) && /rotat|spin/i.test(s.name + f.attribute) ? 'gobo-rotate-cw' : '';
          const b = btn([dir ? ic(dir) : null, h('span', { text: s.name }), state], { 'data-set': k(i) + '#' + s.name, 'aria-pressed': 'false' }, () => write('set', body0));
          mark(b, i, s, state);
          row.appendChild(b);
        });
        box.appendChild(row);
      }
      if (control) {
        if (!slots.length && !named.length) {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { fraction: 0 });
          const kind = commandKind(f.attribute, f.name);
          if (kind) cmds.push({ kind, key: k(i), label: f.name, attr: a.attribute, vi, fi: i, fnName: f.name, extra: { fraction: 0 } });
          else box.appendChild(holdButton(f.name, { 'data-hold-fn': k(i) }, () => write('command', body0, sentOnce(f.name))));
        }
      } else if (single && !slots.length && !named.length) {
        box.appendChild(btn(f.name, { 'data-fn-button': k(i) }, () => write('set', Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { fraction: 0 }))));
      } else if (!single && !plainSelection) {
        box.appendChild(functionFader(a, i, k(i), vi));
      }
      if (box.firstChild) body.appendChild(box);
    });
  }

  // --- Position: XY pad ---------------------------------------------------------
  function ownFn(a) {
    const i = v0(a).functions.findIndex(f => f.attribute === a.attribute);
    return i >= 0 ? i : 0;
  }
  function positionPanel() {
    const pan = findAttr('Pan'), tilt = findAttr('Tilt');
    if (!pan && !tilt) return null;
    // An attribute known only from RDM (no function list, detail
    // "attribute-only") is driven over its whole channel: no function
    // reference, range 0..max.
    const axes = [pan, tilt].map(a => {
      if (!a) return null;
      if (!v0(a).functions.length) return { a, fi: -1, f: { name: a.attribute, dmxFrom: 0, dmxTo: v0(a).max }, ref: {} };
      return { a, fi: ownFn(a), f: v0(a).functions[ownFn(a)], ref: fnRef(a, ownFn(a)) };
    });
    const frac = ax => { if (!ax) return 0.5; const v = chanValue(ax.a); return ax.f.dmxTo > ax.f.dmxFrom ? Math.max(0, Math.min(1, (v - ax.f.dmxFrom) / (ax.f.dmxTo - ax.f.dmxFrom))) : 0; };
    let fx = frac(axes[0]), fy = frac(axes[1]), touchedAt = 0;
    // MIXED stays shown until this browser moves that axis (then every
    // selected fixture has the one absolute value it wrote).
    const mixedOn = [!!(axes[0] && axes[0].a.mixed), !!(axes[1] && axes[1].a.mixed)];
    const dot = h('span', { class: 'b5-cc-pad__dot', 'aria-hidden': 'true' });
    const others = h('span', { class: 'b5-cc-pad__others', 'aria-hidden': 'true' });
    const pid = 'b5-cc-pad' + (++uid);
    const read = h('p', { class: 'b5-text-mono b5-cc-padread', id: pid + '-read', 'data-pad-readout': '' });
    const hint = h('p', { class: 'b5-cc-hint', id: pid + '-hint', 'data-pad-hint': '', text: 'Arrow keys move Pan (left/right) and Tilt (up/down) 1 % of their range; Shift+arrow, or Fine ON, moves a tenth as far. Home/End are on the faders below.' });
    const pad = h('div', { class: 'b5-cc-pad', tabindex: '0', role: 'group', 'data-xypad': '', 'aria-label': 'Pan and tilt pad', 'aria-describedby': pid + '-read ' + pid + '-hint' },
      h('span', { class: 'b5-cc-pad__h', 'aria-hidden': 'true' }), h('span', { class: 'b5-cc-pad__v', 'aria-hidden': 'true' }), h('span', { class: 'b5-cc-pad__tick', 'aria-hidden': 'true' }),
      h('span', { class: 'b5-cc-pad__ax b5-cc-pad__ax--x', 'aria-hidden': 'true', text: 'Pan →' }), h('span', { class: 'b5-cc-pad__ax b5-cc-pad__ax--y', 'aria-hidden': 'true', text: 'Tilt ↑' }), others, dot);
    const dmx = (ax, f) => Math.round(ax.f.dmxFrom + f * (ax.f.dmxTo - ax.f.dmxFrom));
    // Every other selected fixture is a dashed dot at its own value; the
    // handle is the first selected (the reference).
    const chFrac = (a, c) => {
      const v = a.variants[c.variant] || v0(a);
      const f = c.function >= 0 && v.functions[c.function] ? v.functions[c.function] : { dmxFrom: 0, dmxTo: v.max };
      return f.dmxTo > f.dmxFrom ? Math.max(0, Math.min(1, (c.value - f.dmxFrom) / (f.dmxTo - f.dmxFrom))) : 0;
    };
    const drawOthers = () => {
      clear(others);
      if (!mixedOn[0] && !mixedOn[1]) return;
      const at = new Map();
      [0, 1].forEach(i => { const ax = axes[i]; if (!ax) return; ax.a.channels.forEach(c => { const k = c.entryId + '\u0000' + c.cell; const e = at.get(k) || [null, null]; if (e[i] === null) e[i] = chFrac(ax.a, c); at.set(k, e); }); });
      [...at.values()].slice(1).forEach(e => others.appendChild(h('span', { class: 'b5-cc-pad__other', style: { left: ((e[0] === null ? fx : e[0]) * 100) + '%', top: ((1 - (e[1] === null ? fy : e[1])) * 100) + '%' } })));
    };
    const part = (name, ax, f, i) => {
      if (!ax) return name + ' not on these fixtures';
      const bc = v0(ax.a).byteCount || 1;
      if (mixedOn[i]) return name + ' ' + mixfmt(ax.a.channels.map(c => c.value), v0(ax.a).max, ax.fi >= 0 ? ax.f : null).main + ' (reference ' + num(dmx(ax, f)) + ')';
      const r = vfmt(ax.a.attribute, dmx(ax, f), v0(ax.a).max, ax.fi >= 0 ? ax.f : null, bc);
      const noPhys = !physical(ax.a.attribute, ax.fi >= 0 ? ax.f : null, dmx(ax, f)) && !/no physical/.test(r.sub);
      return name + ' ' + r.main + ' · ' + r.sub + (noPhys ? ' · no physical range in profile' : '');
    };
    const draw = () => {
      dot.style.setProperty('left', (fx * 100) + '%');
      dot.style.setProperty('top', ((1 - fy) * 100) + '%');
      read.textContent = part('Pan', axes[0], fx, 0) + ' — ' + part('Tilt', axes[1], fy, 1);
      drawOthers();
    };
    const go = (nx, ny) => {
      touchedAt = now();
      nx = Math.max(0, Math.min(1, nx)); ny = Math.max(0, Math.min(1, ny));
      if (axes[0] && nx !== fx && axes[0].ref) { mixedOn[0] = false; lane('Pan#' + axes[0].fi, 'set', Object.assign({ targets: tgt(axes[0].a), attribute: 'Pan' }, axes[0].ref, { fraction: nx })); (reg.get('Pan#' + axes[0].fi) || { local() {} }).local(dmx(axes[0], nx)); }
      if (axes[1] && ny !== fy && axes[1].ref) { mixedOn[1] = false; lane('Tilt#' + axes[1].fi, 'set', Object.assign({ targets: tgt(axes[1].a), attribute: 'Tilt' }, axes[1].ref, { fraction: ny })); (reg.get('Tilt#' + axes[1].fi) || { local() {} }).local(dmx(axes[1], ny)); }
      fx = nx; fy = ny;
      draw();
    };
    let drag = null;
    pad.addEventListener('pointerdown', ev => {
      const r = pad.getBoundingClientRect();
      if (!r.width || !r.height) return;
      drag = { id: ev.pointerId, r, x0: ev.clientX, y0: ev.clientY, fx0: fx, fy0: fy };
      try { pad.setPointerCapture(ev.pointerId); } catch (_) { /* tests */ }
      if (ev.preventDefault) ev.preventDefault();
      if (!st.fine) go((ev.clientX - r.left) / r.width, 1 - (ev.clientY - r.top) / r.height);
    });
    // A mouse drag on the pad must not start a text selection on the page.
    pad.addEventListener('mousedown', ev => { if (ev.preventDefault) ev.preventDefault(); });
    pad.addEventListener('pointermove', ev => {
      if (!drag || ev.pointerId !== drag.id) return;
      // Fine: 1:10 of the pointer's travel from where it went down, no jump.
      if (st.fine) go(drag.fx0 + (ev.clientX - drag.x0) / drag.r.width * 0.1, drag.fy0 - (ev.clientY - drag.y0) / drag.r.height * 0.1);
      else go((ev.clientX - drag.r.left) / drag.r.width, 1 - (ev.clientY - drag.r.top) / drag.r.height);
    });
    const end = () => { drag = null; };
    pad.addEventListener('pointerup', end);
    pad.addEventListener('pointercancel', end);
    pad.addEventListener('lostpointercapture', end);
    // Contract K: 1 % of each axis's function span (at least one count);
    // Shift or Fine ON: a tenth of that (at least one count).
    const kstep = (ax, fine) => {
      if (!ax) return 0;
      const span = ax.f.dmxTo - ax.f.dmxFrom;
      if (span <= 0) return 0;
      const unit1 = Math.max(1, Math.round(span / 100));
      return (fine ? Math.max(1, Math.round(unit1 / 10)) : unit1) / span;
    };
    pad.addEventListener('keydown', ev => {
      const fine = ev.shiftKey || st.fine;
      const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, 1], ArrowDown: [0, -1] }[ev.key];
      if (!d) return;
      if (ev.preventDefault) ev.preventDefault();
      go(fx + d[0] * kstep(axes[0], fine), fy + d[1] * kstep(axes[1], fine));
    });
    // Tandem: the faders tell the pad when they move.
    if (axes[0]) reg.set('Pan#pad', { local: v => { mixedOn[0] = false; fx = (v - axes[0].f.dmxFrom) / Math.max(1, axes[0].f.dmxTo - axes[0].f.dmxFrom); draw(); } });
    if (axes[1]) reg.set('Tilt#pad', { local: v => { mixedOn[1] = false; fy = (v - axes[1].f.dmxFrom) / Math.max(1, axes[1].f.dmxTo - axes[1].f.dmxFrom); draw(); } });
    reg.set('pad', { update: () => {
      if (now() - touchedAt < ACTIVE_MS || drag) return;
      const p = findAttr('Pan'), t = findAttr('Tilt');
      if (axes[0] && p) { axes[0].a = p; fx = frac(axes[0]); mixedOn[0] = !!p.mixed; }
      if (axes[1] && t) { axes[1].a = t; fy = frac(axes[1]); mixedOn[1] = !!t.mixed; }
      draw();
    } });
    draw();
    const centre = () => go(0.5, 0.5);
    const home = () => {
      // Home = each channel's own default, grouped so one write serves every
      // channel with the same default; unknown defaults are reported.
      const unknown = [];
      [pan, tilt].forEach(a => {
        if (!a) return;
        const byDefault = new Map();
        a.channels.forEach(c => {
          if (!c.defaultKnown) { unknown.push(a.attribute); return; }
          const list = byDefault.get(c.default) || [];
          list.push({ entryId: c.entryId, cell: c.cell });
          byDefault.set(c.default, list);
        });
        byDefault.forEach((targets, d) => write('set', { targets, attribute: a.attribute, dmx: d }));
      });
      say(unknown.length ? 'Home: ' + [...new Set(unknown)].join(', ') + ' has no stated default on some fixtures; they were left as they are.' : 'Pan and tilt sent to each fixture\'s default.', unknown.length ? 'warn' : '');
    };
    return h('section', { class: 'b5-cc-position', 'aria-label': 'Pan and tilt' }, pad, read, hint,
      h('div', { class: 'b5-row' },
        btn('Centre', { 'data-pad-centre': '' }, centre),
        btn('Home (defaults)', { 'data-pad-home': '' }, home),
        h('span', { class: 'b5-caption', text: 'Up on the pad = higher tilt value; which way that points depends on the fixture.' })));
  }

  // --- Colour picker -------------------------------------------------------------
  const ADD = { r: ['ColorAdd_R', 'ColorRGB_Red'], g: ['ColorAdd_G', 'ColorRGB_Green'], b: ['ColorAdd_B', 'ColorRGB_Blue'] };
  const SUB = { r: 'ColorSub_C', g: 'ColorSub_M', b: 'ColorSub_Y' };
  const targetsOf = tgt;
  const tkey = t => t.entryId + '\u0000' + (t.cell || '');
  function hexRGB(hex) {
    const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
    return m ? [parseInt(m[1], 16) / 255, parseInt(m[2], 16) / 255, parseInt(m[3], 16) / 255] : null;
  }
  // colourCapabilities: per selected fixture/cell, what it can do.
  function colourCaps() {
    const has = name => findAttr(name);
    const rgb = ['r', 'g', 'b'].map(k => ADD[k].map(has).find(Boolean) || null);
    const cmy = ['r', 'g', 'b'].map(k => has(SUB[k]));
    const mixT = new Set(), cmyT = new Set();
    rgb.forEach(a => a && targetsOf(a).forEach(t => mixT.add(tkey(t))));
    cmy.forEach(a => a && targetsOf(a).forEach(t => cmyT.add(tkey(t))));
    const wheels = attrsOf('colour').filter(a => a.variants.some(v => v.functions.some(f => f.wheel && f.sets.some(s => s.hasSRGB && (s.hasWheelSlot || s.name)))));
    const wheelOnly = new Map(); // tkey -> {target, attr}
    wheels.forEach(a => targetsOf(a).forEach(t => { const k = tkey(t); if (!mixT.has(k) && !cmyT.has(k) && !wheelOnly.has(k)) wheelOnly.set(k, { target: t, attr: a }); }));
    return { rgb, cmy, mixT, cmyT, wheelOnly };
  }
  // colourWrites — THE MAPPING from a picked sRGB colour (r, g, b in 0..1):
  //   additive (ColorAdd_R/G/B or ColorRGB_Red/Green/Blue): R = r, G = g, B = b
  //     as fractions of each channel; W, A, UV and other emitters are NOT
  //     touched (their own faders stay as set);
  //   subtractive (ColorSub_C/M/Y): C = 1 - r, M = 1 - g, Y = 1 - b;
  //   a fixture with neither, but a colour wheel whose slots state an sRGB
  //   colour: the slot nearest the pick (Euclidean in sRGB), per fixture,
  //   by slot number (or the set name when the profile gives no slot);
  //   anything else (CTO/CTC, macros) is left to its own faders.
  // Each write is per attribute, targeted at the selected fixtures/cells
  // that have it; the server resolves it per fixture type.
  function colourWrites(rgb) {
    const caps = colourCaps();
    const out = [];
    caps.rgb.forEach((a, i) => { if (a) out.push({ key: 'colour#' + a.attribute, body: { targets: tgt(a), attribute: a.attribute, fraction: rgb[i] } }); });
    caps.cmy.forEach((a, i) => { if (a) out.push({ key: 'colour#' + a.attribute, body: { targets: tgt(a), attribute: a.attribute, fraction: Math.round((1 - rgb[i]) * 10000) / 10000 } }); });
    const nearest = [];
    caps.wheelOnly.forEach(({ target, attr }) => {
      const ch = attr.channels.find(c => c.entryId === target.entryId && c.cell === target.cell);
      const v = attr.variants[ch ? ch.variant : 0];
      let best = null;
      v.functions.forEach(f => {
        if (!f.wheel) return;
        f.sets.forEach(s => {
          const c = s.hasSRGB ? hexRGB(s.srgb) : null;
          if (!c) return;
          const d = Math.hypot(c[0] - rgb[0], c[1] - rgb[1], c[2] - rgb[2]);
          const own = f.attribute === attr.attribute ? 0 : 1e-6; // prefer the plain selection function on a tie
          if (!best || d + own < best.d) best = { d: d + own, f, s };
        });
      });
      if (!best) return;
      const body = { targets: [target], attribute: attr.attribute, functionName: best.f.name };
      if (best.s.hasWheelSlot) body.slot = best.s.wheelSlot; else body.set = best.s.name;
      out.push({ key: 'colour-wheel#' + tkey(target), body, slot: slotLabel(best.s) });
      nearest.push(slotLabel(best.s));
    });
    return { writes: out, nearest };
  }
  // §9 capability words. Emitters by GDTF attribute, named as the
  // attribute definitions' Pretty names (ColorAdd_RY "Amber", _GY "Lime" …).
  const EMITTERS = [['R', 'Red', /^(ColorAdd_R|ColorRGB_Red)$/], ['G', 'Green', /^(ColorAdd_G|ColorRGB_Green)$/], ['B', 'Blue', /^(ColorAdd_B|ColorRGB_Blue)$/],
    ['W', 'White', /^ColorAdd_W$/], ['WW', 'Warm white', /^ColorAdd_WW$/], ['CW', 'Cool white', /^ColorAdd_CW$/], ['A', 'Amber', /^ColorAdd_RY$/], ['L', 'Lime', /^ColorAdd_GY$/],
    ['C', 'Cyan', /^ColorAdd_C$/], ['M', 'Magenta', /^ColorAdd_M$/], ['Y', 'Yellow', /^ColorAdd_Y$/], ['BG', 'Blue-green', /^ColorAdd_GC$/], ['LB', 'Light blue', /^ColorAdd_BC$/],
    ['P', 'Purple', /^ColorAdd_BM$/], ['PK', 'Pink', /^ColorAdd_RM$/], ['UV', 'UV', /^ColorAdd_UV$/]];
  const TEMP = /^(CTO|CTB|CTC)$/;
  const isMixAttr = n => EMITTERS.some(e => e[2].test(n)) || /^ColorSub_/.test(n);
  const isWheelAttr = a => a.variants.some(v => v.functions.some(f => f.wheel && f.sets.some(s => s.hasWheelSlot)));
  function colourProfile() {
    const per = new Map(); // tkey -> {emit:Set, cmy, temp:Set, wheel}
    const get = t => { const k = tkey(t); if (!per.has(k)) per.set(k, { emit: new Set(), cmy: false, temp: new Set(), wheel: false }); return per.get(k); };
    attrsOf('colour').forEach(a => {
      const e = EMITTERS.find(x => x[2].test(a.attribute));
      tgt(a).forEach(t => {
        const p = get(t);
        if (e) p.emit.add(e[0]);
        if (/^ColorSub_[CMY]$/.test(a.attribute)) p.cmy = true;
        if (TEMP.test(a.attribute)) p.temp.add(a.attribute);
        if (isWheelAttr(a)) p.wheel = true;
      });
    });
    const counts = new Map();
    const bump = w => counts.set(w, (counts.get(w) || 0) + 1);
    const emitN = new Map();
    per.forEach(p => {
      const codes = EMITTERS.map(e => e[0]).filter(c => p.emit.has(c));
      codes.forEach(c => emitN.set(c, (emitN.get(c) || 0) + 1));
      if (codes.length) bump('mix ' + codes.filter(c => c.length === 1).join('') + codes.filter(c => c.length > 1).map(c => '+' + c).join(''));
      if (p.cmy) bump('mix CMY');
      if (p.wheel && !codes.length && !p.cmy) bump('wheel only');
      else if (p.wheel) bump('with colour wheel');
      p.temp.forEach(x => bump(x));
    });
    const total = per.size;
    const emitters = EMITTERS.filter(e => emitN.has(e[0])).map(e => e[1] + (emitN.get(e[0]) < total ? ' (' + emitN.get(e[0]) + ' of ' + total + ')' : ''));
    return { counts: [...counts].map(([w, n]) => n + ' ' + w), emitters, total };
  }
  // hsv: the field's hue (0..1 across) and saturation (1 at the top) at
  // full value; brightness belongs to the dimmer.
  function hsv2rgb(hh, s) {
    const i = Math.floor(hh * 6) % 6, f = hh * 6 - Math.floor(hh * 6), p = 1 - s, q = 1 - f * s, t = 1 - (1 - f) * s;
    return [[1, t, p], [q, 1, p], [p, 1, t], [p, q, 1], [t, p, 1], [1, p, q]][i];
  }
  function rgb2hs(c) {
    const mx = Math.max(...c), mn = Math.min(...c), d = mx - mn;
    let hh = 0;
    if (d) hh = mx === c[0] ? ((c[1] - c[2]) / d + 6) % 6 : mx === c[1] ? (c[2] - c[0]) / d + 2 : (c[0] - c[1]) / d + 4;
    return [hh / 6, mx ? d / mx : 0];
  }
  // refColour: the first selected fixture's colour AS DMX VALUES (R, G, B
  // fractions, or 1 − C/M/Y): what the field's handle shows — not a
  // measured colour.
  function refColour() {
    const caps = colourCaps();
    const first = sel()[0];
    const pick = list => list.map(a => { if (!a) return null; const c = a.channels.find(x => first && x.entryId === first.entryId && x.cell === first.cell) || a.channels[0]; const v = a.variants[c.variant] || v0(a); return c.value / (v.max || 255); });
    const add = pick(caps.rgb);
    if (add.every(x => x !== null)) return { rgb: add, words: 'R ' + pct(add[0], 1) + ' % · G ' + pct(add[1], 1) + ' % · B ' + pct(add[2], 1) + ' %', mixed: caps.rgb.some(a => a.mixed) };
    const sub = pick(caps.cmy);
    if (sub.every(x => x !== null)) return { rgb: sub.map(x => 1 - x), words: 'C ' + pct(sub[0], 1) + ' % · M ' + pct(sub[1], 1) + ' % · Y ' + pct(sub[2], 1) + ' %', mixed: caps.cmy.some(a => a.mixed) };
    return null;
  }
  function colourPanel() {
    const caps = colourCaps();
    const prof = colourProfile();
    const all = attrsOf('colour');
    const mixA = all.filter(a => isMixAttr(a.attribute)), wheelA = all.filter(a => !isMixAttr(a.attribute) && isWheelAttr(a)), tempA = all.filter(a => TEMP.test(a.attribute));
    const other = all.filter(a => !mixA.includes(a) && !wheelA.includes(a) && !tempA.includes(a));
    const modes = [];
    if (mixA.length) modes.push(['mix', 'Mix']);
    if (wheelA.length) modes.push(['wheel', 'Wheel']);
    if (tempA.length) modes.push(['temp', [...new Set(tempA.map(a => a.attribute))].join(' / ')]);
    const out = h('section', { class: 'b5-cc-colour', 'aria-label': 'Colour' });
    out.appendChild(h('div', { class: 'b5-cc-capline', 'data-colour-caps': '' }, h('span', { class: 'b5-caption', text: 'Can do: ' }),
      prof.counts.length ? prof.counts.map(w => h('span', { class: 'b5-pill b5-pill--tag', text: w })) : h('span', { text: 'no colour mixing, wheel or temperature channels' })));
    if (prof.emitters.length) out.appendChild(h('p', { class: 'b5-caption', 'data-colour-emitters': '' }, 'Emitters (from the profile): ', h('span', { class: 'b5-fromfile', text: prof.emitters.join(', ') })));
    st.colourCards = null;
    if (!modes.length) {
      out.appendChild(h('p', { class: 'b5-note', 'data-colour-none': '', text: 'No selected fixture mixes colour or has a colour wheel or temperature channel; use the faders below.' }));
      other.forEach(a => out.appendChild(attributeCard(a, 'colour')));
      return out;
    }
    if (!modes.some(m => m[0] === st.colourMode)) st.colourMode = modes[0][0];
    const tabs = h('div', { class: 'b5-cc-modetabs', role: 'tablist', 'aria-label': 'Colour mode' });
    modes.forEach(([k, label]) => {
      const on = k === st.colourMode;
      const t = h('button', { type: 'button', role: 'tab', class: 'b5-seg' + (on ? ' is-on' : ''), id: 'b5-cc-cmode-' + k, 'aria-selected': on ? 'true' : 'false', 'aria-controls': 'b5-cc-cmode-panel', tabindex: on ? '0' : '-1', 'data-colour-mode': k }, label);
      t.addEventListener('click', () => { st.colourMode = k; refresh(true); });
      t.addEventListener('keydown', ev => {
        const i = modes.findIndex(m => m[0] === st.colourMode);
        const to = { ArrowRight: modes[Math.min(modes.length - 1, i + 1)], ArrowLeft: modes[Math.max(0, i - 1)], Home: modes[0], End: modes[modes.length - 1] }[ev.key];
        if (!to) return;
        if (ev.preventDefault) ev.preventDefault();
        st.colourMode = to[0]; refresh(true);
        const n = els.body.querySelector('[data-colour-mode="' + to[0] + '"]'); if (n) n.focus();
      });
      tabs.appendChild(t);
    });
    out.appendChild(tabs);
    const panel = h('div', { class: 'b5-cc-cmodepanel', role: 'tabpanel', id: 'b5-cc-cmode-panel', 'aria-labelledby': 'b5-cc-cmode-' + st.colourMode, 'data-colour-panel': st.colourMode });
    out.appendChild(panel);
    const pickerWanted = st.colourMode === 'mix' || (st.colourMode === 'wheel' && caps.wheelOnly.size);
    if (pickerWanted) panel.appendChild(colourField(caps));
    if (st.colourMode === 'wheel') {
      const noColours = wheelA.filter(a => !a.variants.some(v => v.functions.some(f => f.wheel && f.sets.some(s => s.hasSRGB))));
      if (noColours.length) panel.appendChild(h('p', { class: 'b5-caption', text: noColours.map(a => a.attribute).join(', ') + ': the profile states no slot colours, so the picker cannot choose a slot — use the slot buttons.' }));
    }
    const cards = st.colourMode === 'mix' ? mixA : st.colourMode === 'wheel' ? wheelA : tempA;
    st.colourCards = cards.concat(other).map(a => a.attribute);
    cards.forEach(a => panel.appendChild(attributeCard(a, 'colour')));
    if (other.length) out.appendChild(h('section', { class: 'b5-cc-colother' }, h('h4', { text: 'Other colour channels' }), other.map(a => attributeCard(a, 'colour'))));
    return out;
  }
  // colourField: hue across, saturation up, at full value; the handle
  // shows the first selected fixture's DMX values. The content is capped
  // to the dark-venue preview brightness (theme contract); the handle,
  // words and state sit outside the dimmed area. Keyboard: arrows move
  // hue / saturation 2 %; exact values are the emitter faders below.
  function colourField(caps) {
    let hh = 0, ss = 0;
    const ref = refColour();
    if (ref) [hh, ss] = rgb2hs(ref.rgb);
    const handle = h('span', { class: 'b5-cc-mix__handle', 'aria-hidden': 'true' });
    const content = h('span', { class: 'b5-cc-mix__content', 'aria-hidden': 'true',
      style: { background: 'linear-gradient(to bottom, transparent, #fff), linear-gradient(to right, #f00, #ff0, #0f0, #0ff, #00f, #f0f, #f00)' } });
    const fid = 'b5-cc-mix' + (++uid);
    const read = h('p', { class: 'b5-text-mono b5-cc-mixread', id: fid + '-read', 'data-colour-dmx': '' });
    const nearest = h('p', { class: 'b5-cc-nearest', 'data-colour-nearest': '', 'aria-live': 'polite' });
    const field = h('div', { class: 'b5-cc-mix', role: 'group', tabindex: '0', 'data-colour-field': '', 'aria-label': 'Colour mix field: hue across, saturation up. Exact values: the emitter faders below.', 'aria-describedby': fid + '-read' }, content, handle);
    const words = (rgb, live) => {
      const ws = colourWrites(rgb).nearest;
      nearest.textContent = ws.length ? '→ nearest slot: ' + [...new Set(ws)].join(', ') + (live ? '' : ' (sent)') : '';
    };
    const draw = () => {
      handle.style.setProperty('left', hh * 100 + '%');
      handle.style.setProperty('top', (1 - ss) * 100 + '%');
      const r = refColour();
      read.textContent = r ? 'DMX values, not measured colour · first selected: ' + r.words + (r.mixed ? ' · MIXED across the selection' : '') : 'DMX values, not measured colour · no mixing channels on the first selected';
    };
    let drag = null, touchedAt = 0;
    const go = (nh, ns, final) => {
      touchedAt = now();
      hh = Math.max(0, Math.min(1, nh)); ss = Math.max(0, Math.min(1, ns));
      const rgb = hsv2rgb(Math.min(hh, 0.9999), ss);
      colourWrites(rgb).writes.forEach(w => lane(w.key, 'set', w.body));
      words(rgb, !final);
      draw();
    };
    const at = ev => { const r = drag.r; return [(ev.clientX - r.left) / r.width, 1 - (ev.clientY - r.top) / r.height]; };
    field.addEventListener('pointerdown', ev => {
      const r = field.getBoundingClientRect();
      if (!r.width || !r.height) return;
      drag = { id: ev.pointerId, r };
      try { field.setPointerCapture(ev.pointerId); } catch (_) { /* tests */ }
      if (ev.preventDefault) ev.preventDefault();
      const [x, y] = at(ev); go(x, y, false);
    });
    field.addEventListener('pointermove', ev => { if (drag && ev.pointerId === drag.id) { const [x, y] = at(ev); go(x, y, false); } });
    const end = ev => { if (!drag) return; drag = null; words(hsv2rgb(Math.min(hh, 0.9999), ss), false); if (ev && ev.type === 'pointerup') say(nearest.textContent || 'Colour sent as DMX values.'); };
    ['pointerup', 'pointercancel', 'lostpointercapture'].forEach(t => field.addEventListener(t, end));
    field.addEventListener('keydown', ev => {
      const d = { ArrowLeft: [-0.02, 0], ArrowRight: [0.02, 0], ArrowUp: [0, 0.02], ArrowDown: [0, -0.02] }[ev.key];
      if (!d) return;
      if (ev.preventDefault) ev.preventDefault();
      go(hh + d[0], ss + d[1], true);
    });
    reg.set('colourref', { update: () => {
      if (drag || now() - touchedAt < ACTIVE_MS) return;
      const r = refColour();
      if (r) [hh, ss] = rgb2hs(r.rgb);
      draw();
    } });
    draw();
    const presets = [['White', '#ffffff'], ['Red', '#ff0000'], ['Orange', '#ff8000'], ['Yellow', '#ffff00'], ['Green', '#00ff00'], ['Cyan', '#00ffff'], ['Blue', '#0000ff'], ['Magenta', '#ff00ff']];
    const row = h('div', { class: 'b5-cc-swatches', role: 'group', 'aria-label': 'Quick colours' });
    presets.forEach(([name, hex]) => row.appendChild(h('button', { type: 'button', class: 'b5-cc-quick', 'data-colour': hex, onclick: () => {
      const rgb = hexRGB(hex);
      [hh, ss] = rgb2hs(rgb); touchedAt = now();
      colourWrites(rgb).writes.forEach(w => lane(w.key, 'set', w.body));
      words(rgb, false); draw();
      if (nearest.textContent) say('Wheel-only fixtures ' + nearest.textContent + '.');
    } }, h('span', { class: 'b5-cc-swatch', 'aria-hidden': 'true' }, h('span', { class: 'b5-cc-swatch__c', style: { background: hex } })), name)));
    const n = { mix: caps.mixT.size, cmy: caps.cmyT.size, wheel: caps.wheelOnly.size };
    return h('div', { class: 'b5-cc-picker', 'data-colour-picker': '' }, field, read, nearest, row,
      h('p', { class: 'b5-caption', text: 'DMX values, not measured colour. ' + (n.mix ? 'RGB fixtures get R, G and B (white, amber and UV stay on their own faders). ' : '') +
        (n.cmy ? 'CMY fixtures get C = 1 − R, M = 1 − G, Y = 1 − B. ' : '') + (n.wheel ? 'Wheel-only fixtures go to the nearest stated slot colour; the wheel does not mix.' : '') }));
  }

  // --- Shaper (§11) ---------------------------------------------------------------
  function bladeOf(name) {
    const m = /^(Blade|Shaper)(\d+)/.exec(name);
    return m ? 'Blade ' + m[2] : 'Assembly';
  }
  // Blade n is drawn on a generic side (1 top, 2 right, 3 bottom, 4 left):
  // GDTF Blade(n)A/B "shape the top/right/bottom/left of the beam" without
  // saying which n is which side, so the diagram says geometry not reported.
  const SIDES = ['top', 'right', 'bottom', 'left'];
  function bladeTitle(attr) {
    let m = /^(?:Blade|Shaper)(\d+)(A|B|Rot)$/.exec(attr);
    if (m) return 'Blade ' + m[1] + ' · ' + (m[2] === 'Rot' ? 'angle' : 'end ' + m[2]);
    if (attr === 'ShaperRot') return 'Assembly rotate';
    return '';
  }
  // bladeFrac: an attribute's position in its own function (whole channel
  // when it has none), 0..1, for drawing; null when the attribute is absent.
  function bladeFrac(a) {
    if (!a) return null;
    const fi = ownFn(a);
    const f = v0(a).functions[fi] || { dmxFrom: 0, dmxTo: v0(a).max };
    const span = f.dmxTo - f.dmxFrom;
    return span > 0 ? Math.max(0, Math.min(1, (chanValue(a) - f.dmxFrom) / span)) : 0;
  }
  // shaperDiagram: a numbered blade diagram whose words carry everything
  // (role=img with a description naming each blade, its side and both
  // ends); blades are told apart by number and A/B letters, never by fill.
  // Depth is drawn from the DMX % of each end (0 % drawn as out) — a
  // drawing aid, not measured optics.
  function shaperDiagram() {
    const blades = [];
    for (let n = 1; n <= 8; n++) {
      const A = findAttr('Blade' + n + 'A') || findAttr('Shaper' + n + 'A');
      const B = findAttr('Blade' + n + 'B') || findAttr('Shaper' + n + 'B');
      const R = findAttr('Blade' + n + 'Rot') || findAttr('Shaper' + n + 'Rot');
      if (A || B || R) blades.push({ n, A, B, R });
    }
    const asm = findAttr('ShaperRot');
    const C = 115, Rb = 82, edge = C - Rb, deep = Rb; // 230 px drawing; a blade at 100 % reaches the centre
    const pctOf = a => a ? (a.mixed ? 'MIXED ' + mixfmt(a.channels.map(c => c.value), v0(a).max, null).main.replace(/^MIXED /, '') : pct(bladeFrac(a) * 1000, 1000) + ' %') : 'not on these fixtures';
    let svg = '<svg viewBox="0 0 230 230" width="230" height="230" aria-hidden="true" focusable="false"><circle class="b5-cc-shaper__beam" cx="115" cy="115" r="82"/>';
    const said = [];
    blades.forEach((b, idx) => {
      if (idx > 3) { said.push('Blade ' + b.n + ': ' + ['A ' + pctOf(b.A), 'B ' + pctOf(b.B)].join(', ') + ' (not drawn)'); return; }
      const side = SIDES[idx];
      const da = (bladeFrac(b.A) || 0) * deep, db = (bladeFrac(b.B) || 0) * deep;
      const mixed = (b.A && b.A.mixed) || (b.B && b.B.mixed);
      // drawn for the top side, then rotated to its side
      const pts = [[edge, edge], [230 - edge, edge], [230 - edge, edge + db], [edge, edge + da]].map(p => p.join(',')).join(' ');
      svg += '<g transform="rotate(' + idx * 90 + ' 115 115)"><polygon class="b5-cc-shaper__blade' + (mixed ? ' is-mixed' : '') + '" points="' + pts + '"/></g>';
      // A / B letters stay upright: their spots are rotated, the text is not.
      const rot = (x, y) => { const t = idx * Math.PI / 2, c = Math.round(Math.cos(t)), s = Math.round(Math.sin(t)); return [115 + (x - 115) * c - (y - 115) * s, 115 + (x - 115) * s + (y - 115) * c]; };
      [['A', edge + 8], ['B', 230 - edge - 8]].forEach(([w, x]) => { const p = rot(x, edge - 9); svg += '<text class="b5-cc-shaper__ab" x="' + p[0] + '" y="' + (p[1] + 4) + '" text-anchor="middle">' + w + '</text>'; });
      const lx = [115, 222, 115, 8][idx], ly = [16, 120, 226, 120][idx];
      svg += '<text class="b5-cc-shaper__num" x="' + lx + '" y="' + ly + '" text-anchor="middle">' + b.n + '</text>';
      said.push('Blade ' + b.n + ' (drawn ' + side + '): A ' + pctOf(b.A) + ', B ' + pctOf(b.B) + (b.R ? ', angle ' + pctOf(b.R) : '') + (mixed ? ' — MIXED, dashed' : ''));
    });
    svg += '</svg>';
    const desc = 'Shaper diagram, geometry not reported: blades drawn on generic sides, depth from DMX %. ' + (said.length ? said.join('. ') + '.' : 'No blade channels on these fixtures.') + (asm ? ' Assembly rotate ' + pctOf(asm) + '.' : '');
    const box = h('div', { class: 'b5-cc-shaper', role: 'img', 'aria-label': desc, 'data-shaper-diagram': '' });
    box.innerHTML = svg;
    return h('figure', { class: 'b5-cc-shaperfig' }, box,
      h('figcaption', { class: 'b5-caption', 'data-shaper-desc': '', text: 'Geometry not reported: blade numbers sit outside the beam on generic sides; A/B mark each blade\'s ends; depth drawn from DMX %. ' + said.join(' · ') }));
  }

  // --- raw DMX ----------------------------------------------------------------------
  function rawPanel(list) {
    const box = h('div', { class: 'b5-cc-raw' });
    list.forEach(r => {
      const m = st.models[r.entryId] || {};
      const name = (sel().find(s => s.entryId === r.entryId) || {}).name || m.name || 'fixture';
      const sec = h('section', { class: 'b5-cc-rawfix', 'data-raw-entry': r.entryId },
        h('h4', { class: 'b5-cc-rawhead' }, name, h('span', { class: 'b5-caption', text: m.profiled === false ? ' · No profile · ' + r.offsets.length + ' channels' : ' · ' + r.offsets.length + ' channel(s) with no profile detail' })));
      const bank = h('div', { class: 'b5-cc-rawbank' });
      r.offsets.forEach(o => {
        const key = 'raw#' + r.entryId + '#' + o.offset;
        const addr = m.startAddress ? ' · addr ' + (m.startAddress + o.offset - 1) : '';
        const input = h('input', { type: 'range', class: 'b5-range-touch', min: 0, max: 255, step: 1, 'data-raw': key, 'aria-label': name + ' channel ' + o.offset });
        input.value = o.value;
        const val = h('span', { class: 'b5-text-mono', 'data-raw-value': key, text: String(o.value) });
        const mark = h('span', { 'data-raw-mark': key }, o.touched ? tag('SET', 'accent') : tag('default'));
        let touchedAt = 0;
        const push = () => { touchedAt = now(); val.textContent = input.value; lane(key, 'raw', { entryId: r.entryId, writes: [{ offset: o.offset, value: Number(input.value) }] }); };
        input.addEventListener('input', push);
        input.addEventListener('change', push);
        bank.appendChild(h('div', { class: 'b5-param b5-cc-rawch' },
          h('span', { class: 'b5-param__label' }, 'ch ' + o.offset + addr + ' ', mark, ' ', val),
          input,
          btn('Release', { 'data-raw-release': key, 'aria-label': 'Release channel ' + o.offset + ' back to its default' }, () => write('raw', { entryId: r.entryId, writes: [{ offset: o.offset, release: true }] }, 'Released channel ' + o.offset + '.'))));
        reg.set(key, { update: ro => {
          if (now() - touchedAt < ACTIVE_MS) return;
          input.value = ro.value; val.textContent = String(ro.value);
          clear(mark); mark.appendChild(ro.touched ? tag('SET', 'accent') : tag('default'));
        } });
      });
      sec.appendChild(bank);
      box.appendChild(sec);
    });
    return box;
  }

  // --- toolbar -----------------------------------------------------------------------
  // I2d2: the shared non-modal dialog (ui.js UI.ask): Cancel focused,
  // Escape cancels, focus returns, Disarm stays reachable.
  function ask(o) { return UI.ask(o); }
  // --- §15 action-bar parts (drawn by console.js in the selection bar) -------
  // Clear scopes, the Fan form and the Lowlight level are family-aware, so
  // they are built here and handed to the action bar. done() closes the
  // bar's panel after a write.
  function clearScopes(done) {
    const group = st.tab;
    const label = GROUP_LABEL[group] || group || 'Family';
    const fin = r => { if (done) done(); return r; };
    const scope = (b, words) => h('div', { class: 'b5-con-scope' }, b, h('span', { class: 'b5-caption', text: words }));
    return h('div', { class: 'b5-con-scopes', role: 'group', 'aria-label': 'Clear scope' },
      group ? scope(btn(label + ' on selection', { 'data-clear-group': group }, () => write('clear', { scope: 'selection', group }, r => fin('Cleared ' + r.released + ' ' + label + ' channel(s) of the selection.'))),
        'Only the ' + label + ' values of the selected fixtures.') : null,
      scope(btn('Selection values', { 'data-clear-selection-values': '' }, () => write('clear', { scope: 'selection' }, r => fin('Cleared ' + r.released + ' channel(s) of the selection.'))),
        'Every value of the selected fixtures, all families.'),
      scope(btn('Everything', { 'data-clear-all': '' }, () => write('clear', { scope: 'all' }, r => fin('Cleared the whole programmer (' + r.released + ' channel(s)) and the selection.'))),
        'The whole programmer, every fixture — and the selection is emptied.'));
  }
  function lowlightForm(done) {
    const hl = (prog() && prog().highlight) || {};
    const pctIn = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, 'data-lowlight-percent': '', 'aria-label': 'Lowlight level in percent' });
    pctIn.value = hl.lowlightPercent !== undefined ? hl.lowlightPercent : 20;
    return h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Lowlight level' },
      h('label', { class: 'b5-row' }, h('span', { text: 'Level' }), pctIn, h('span', { text: '%' })),
      btn('Set level', { 'data-lowlight-set': '' }, () => {
        const v = Number(pctIn.value);
        if (!(v >= 0 && v <= 100) || v !== Math.round(v)) return say('The lowlight level is a whole percentage from 0 to 100.', 'error');
        write('highlight', { lowlightPercent: v }, 'Lowlight level set to ' + v + ' %.').then(r => { if (r && done) done(); });
      }),
      h('p', { class: 'b5-caption', text: 'While Highlight is ON, Lowlight dims the selected fixtures and cells that are not highlighted (step with Previous/Next) to this level. Fixtures outside the selection are not touched.' }));
  }

  function toolbar(group) {
    // Fine (contract K, owner-confirmed): 1:10 travel over the whole value.
    const fineWords = () => st.fine ? 'Fine: ON · 1:10' : 'Fine: OFF';
    const fine = h('button', { type: 'button', class: 'b5-seg' + (st.fine ? ' is-on' : ''), 'aria-pressed': st.fine ? 'true' : 'false', 'data-fine': '',
      title: 'Fine: a drag on a fader or the pad travels a tenth as far over the whole value; ± step one DMX value',
      onclick: () => {
        st.fine = !st.fine;
        fine.textContent = fineWords(); fine.classList.toggle('is-on', st.fine); fine.setAttribute('aria-pressed', st.fine ? 'true' : 'false');
        if (els.body) els.body.classList.toggle('is-fine', st.fine);
      } }, fineWords());
    // Readout (§13): which unit the readouts show large, remembered per browser.
    const units = h('select', { class: 'b5-select b5-cc-units', 'data-readout-units': '', 'aria-label': 'Readout unit' },
      h('option', { value: 'pct', text: '%' }), h('option', { value: 'dmx', text: 'DMX' }), h('option', { value: 'phys', text: 'Physical' }));
    units.value = st.units;
    units.addEventListener('change', () => {
      st.units = units.value === 'dmx' || units.value === 'phys' ? units.value : 'pct';
      try { localStorage.setItem(UNITS_KEY, st.units); } catch (_) { /* no storage */ }
      refresh(true);
    });
    const valueRow = h('div', { class: 'b5-row b5-cc-valuebar', role: 'group', 'aria-label': 'Value controls' },
      fine, h('label', { class: 'b5-row' }, h('span', { text: 'Readout' }), units));
    return { top: h('div', { class: 'b5-cc-toolbar' }, valueRow), bottom: h('div', { class: 'b5-cc-toolbar b5-cc-toolbar--tools' }, presetPanel(group)) };
  }
  // §15 fan chooser: four shapes, each a glyph AND a word, as a radio group;
  // every shape is mapped to a server shape explicitly (centre out is the
  // server's "mirror"; edges-in its own shape, never reinterpreted).
  const FAN_SHAPES = [
    ['linear', 'Linear', 'fan-linear', 'first → last', 'linear'],
    ['reverse', 'Reverse', 'fan-reverse', 'last → first', 'reverse'],
    ['centre-out', 'Centre out', 'fan-center-out', 'centre = From, ends = To', 'mirror'],
    ['edges-in', 'Edges in', 'fan-edges-in', 'ends = From, centre = To', 'edges-in'],
  ];
  function fanForm(done) {
    const group = st.tab;
    const attrs = attrsOf(group);
    const ff = st.fanForm || (st.fanForm = { shape: 'linear', attr: '', fn: '', from: '0', to: '100' });
    if (!attrs.length) return h('p', { class: 'b5-note', text: 'Nothing to fan in ' + (GROUP_LABEL[group] || 'this family') + '.' });
    // I2d3: every Control value is a one-shot command, which Fan (it keeps
    // values) refuses on the server.
    if (group === 'other') return h('p', { class: 'b5-note', text: 'Nothing to fan in Control: Control channels are fixture commands, sent once and never kept.' });
    if (!attrs.some(a => a.attribute === ff.attr)) { ff.attr = attrs[0].attribute; ff.fn = ''; }
    const aSel = h('select', { class: 'b5-select', 'data-fan-attr': '' });
    attrs.forEach(a => aSel.appendChild(h('option', { value: a.attribute, text: a.attribute })));
    const fSel = h('select', { class: 'b5-select', 'data-fan-fn': '' });
    const fill = () => {
      clear(fSel);
      const a = attrs.find(x => x.attribute === aSel.value);
      fSel.appendChild(h('option', { value: '', text: 'whole channel' }));
      if (a) v0(a).functions.forEach((f, i) => { if (!HIDDEN_FN.test(f.attribute) && fnRef(a, i)) fSel.appendChild(h('option', { value: String(i), text: f.name + ' (' + f.dmxFrom + '–' + f.dmxTo + ')' })); });
    };
    aSel.value = ff.attr;
    fill();
    fSel.value = ff.fn;
    aSel.addEventListener('change', () => { ff.attr = aSel.value; ff.fn = ''; fill(); fSel.value = ''; });
    fSel.addEventListener('change', () => { ff.fn = fSel.value; });
    const from = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, 'data-fan-from': '', 'aria-label': 'Fan start, percent' });
    const to = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, 'data-fan-to': '', 'aria-label': 'Fan end, percent' });
    from.value = ff.from; to.value = ff.to;
    from.addEventListener('input', () => { ff.from = from.value; });
    to.addEventListener('input', () => { ff.to = to.value; });
    const radios = h('div', { class: 'b5-con-fanshapes', role: 'radiogroup', 'aria-label': 'Fan shape', 'data-fan-shape': '' });
    const opts = FAN_SHAPES.map(([k, word, icon, how]) => {
      const on = ff.shape === k;
      const b = h('button', { type: 'button', role: 'radio', class: 'b5-seg b5-con-fanshape' + (on ? ' is-on' : ''), 'aria-checked': on ? 'true' : 'false', tabindex: on ? '0' : '-1', 'data-fan-shape-opt': k },
        ic(icon), h('span', { class: 'b5-con-fanshape__word', text: word }), h('span', { class: 'b5-caption', text: how }));
      b.addEventListener('click', () => pickShape(k, false));
      b.addEventListener('keydown', ev => {
        const i = FAN_SHAPES.findIndex(x => x[0] === ff.shape);
        const n = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1 }[ev.key];
        if (n === undefined) return;
        if (ev.preventDefault) ev.preventDefault();
        pickShape(FAN_SHAPES[(n + FAN_SHAPES.length) % FAN_SHAPES.length][0], true);
      });
      radios.appendChild(b);
      return b;
    });
    function pickShape(k, focus) {
      ff.shape = k;
      opts.forEach(b => {
        const on = b.getAttribute('data-fan-shape-opt') === k;
        b.classList.toggle('is-on', on); b.setAttribute('aria-checked', on ? 'true' : 'false'); b.setAttribute('tabindex', on ? '0' : '-1');
        if (on && focus) b.focus();
      });
    }
    const order = sel().map((x, i) => (i + 1) + ' ' + (x.name || 'Unnamed fixture') + (x.cell ? ' cell ' + (x.cellIndex || '?') : ''));
    const go = () => {
      const a = attrs.find(x => x.attribute === aSel.value);
      if (!a) return say('Pick an attribute to fan.', 'error');
      const shape = FAN_SHAPES.find(x => x[0] === ff.shape);
      const body = { attribute: a.attribute };
      if (fSel.value !== '') Object.assign(body, fnRef(a, Number(fSel.value)));
      Object.assign(body, { shape: shape[4], from: { fraction: Number(from.value) / 100 }, to: { fraction: Number(to.value) / 100 } });
      write('fan', body, r => 'Fanned ' + a.attribute + ' (' + shape[1] + ') over ' + ((r && r.applied) || []).length + ' channel(s) in selection order.').then(r => { if (r && done) done(); });
    };
    return h('div', { class: 'b5-cc-fanform', 'data-fan': '' },
      radios,
      h('div', { class: 'b5-row b5-con-fanfields' },
        h('label', { class: 'b5-row' }, h('span', { text: 'Attribute' }), aSel), h('label', { class: 'b5-row' }, h('span', { text: 'Function' }), fSel),
        h('label', { class: 'b5-row' }, h('span', { text: 'Start %' }), from), h('label', { class: 'b5-row' }, h('span', { text: 'End %' }), to)),
      h('p', { class: 'b5-caption', 'data-fan-order': '' }, 'Order: ' + (order.length ? order.join(' → ') : 'nothing selected')),
      h('p', { class: 'b5-caption', text: 'Centre out and Edges in need at least 3 in the selection.' }),
      btn('Apply fan', { 'data-fan-go': '' }, go, 'b5-btn--primary'));
  }
  // choose (I2d §14): a NON-modal choice dialog — Disarm stays operable —
  // resolving to the picked choice's value, or null on Cancel / Escape.
  // Cancel has focus first: a destructive choice is never the default.
  // I2d2: the shared non-modal choice dialog (ui.js UI.choose).
  function choose(o) { return UI.choose(o); }
  const PRESET_ICON = { dimmer: 'preset-dimmer', position: 'preset-position', colour: 'preset-colour', beam: 'preset-beam', focus: 'preset-focus', shaper: 'preset-shaper', other: 'preset-control' };
  function presetPanel(group) {
    const label = GROUP_LABEL[group] || group;
    const list = ((prog() && prog().presets) || []).filter(x => x.family === group);
    const recallReport = r => {
      const miss = (r.targets || []).filter(t => t.how === 'nothing');
      const lines = ['Recalled ' + r.applied + ' channel(s).'];
      if (miss.length) lines.push(miss.length + ' selected target(s) got nothing: ' + miss[0].reason);
      return lines.join(' ');
    };
    const row = h('div', { class: 'b5-cc-presets', role: 'group', 'aria-label': label + ' presets' });
    // §14: family glyph + name; "applies to n of m selected" when partial;
    // a preset that applies to none of the selection is disabled and says so.
    const m = sel().length;
    list.forEach(pr => {
      const n = typeof pr.applies === 'number' ? pr.applies : m;
      const words = n === 0 ? 'applies to none of the selection' : n < m ? 'applies to ' + n + ' of ' + m + ' selected' : '';
      const b = btn([ic(PRESET_ICON[group] || 'store-preset'), h('span', { class: 'b5-cc-preset__name', text: pr.name }),
        words ? h('span', { class: 'b5-caption b5-cc-preset__applies', 'data-preset-applies': pr.id, text: words }) : null],
        { 'data-preset': pr.id, title: pr.fixtures + ' fixture(s), ' + pr.channels + ' channel(s)' }, () => write('presets/recall', { id: pr.id }, recallReport), 'b5-cc-preset');
      if (n === 0) b.disabled = true;
      row.appendChild(b);
    });
    if (!list.length) row.appendChild(h('span', { class: 'b5-caption', text: 'No ' + label + ' presets yet.' }));
    const manage = h('div', { class: 'b5-cc-presetmanage' });
    list.forEach(pr => manage.appendChild(h('div', { class: 'b5-row', 'data-preset-row': pr.id }, h('strong', { text: pr.name }),
      btn('Overwrite', { 'data-preset-overwrite': pr.id }, () => write('presets/overwrite', { id: pr.id }, 'Overwrote ' + pr.name + ' with the selection\'s ' + label + ' values.')),
      btn('Rename…', { 'data-preset-rename': pr.id }, async () => { const n = await ask({ title: 'Rename preset', field: { label: 'Preset name', value: pr.name }, ok: 'Rename' }); if (n) write('presets/rename', { id: pr.id, name: n }, 'Renamed.'); }),
      btn('Delete…', { 'data-preset-delete': pr.id }, async () => { if (await ask({ title: 'Delete preset ' + pr.name + '?', ok: 'Delete', danger: true })) write('presets/delete', { id: pr.id }, 'Deleted ' + pr.name + '.'); }, 'b5-btn--danger'))));
    const d = h('details', { class: 'b5-cc-tool', 'data-presets': group }, h('summary', { text: label + ' presets · ' + list.length }),
      row,
      h('div', { class: 'b5-row' }, btn([ic('store-preset'), 'Store ' + label + ' preset…'], { 'data-preset-store': group }, async () => {
        const n = await ask({ title: 'Store a ' + label + ' preset', body: 'Stores only the ' + label + ' values the programmer holds for the selection.', field: { label: 'Preset name' }, ok: 'Store' });
        if (!n) return;
        // §14: an existing name asks Replace / Save as new; replacing is
        // destructive and must be chosen explicitly.
        const same = ((prog() && prog().presets) || []).find(x => x.family === group && x.name.toLowerCase() === n.toLowerCase());
        if (same) {
          const c = await choose({ title: 'A ' + label + ' preset named "' + same.name + '" exists', body: 'Replace it with the selection\'s ' + label + ' values, or keep it and save this as a new preset.',
            choices: [{ label: 'Save as new', value: 'new' }, { label: 'Replace "' + same.name + '"', value: 'replace', cls: 'b5-cc-cmd' }] });
          if (c === 'replace') return write('presets/overwrite', { id: same.id }, 'Replaced ' + same.name + ' with the selection\'s ' + label + ' values.');
          if (c !== 'new') return;
        }
        write('presets/store', { name: n, family: group }, 'Stored ' + label + ' preset ' + n + '.');
      })), manage);
    d.open = st.open['#presets'] !== false;
    d.addEventListener('toggle', () => { st.open['#presets'] = d.open; });
    return d;
  }

  // --- rendering ----------------------------------------------------------------------
  function signature() {
    const p = prog();
    if (!p) return 'none';
    return JSON.stringify([p.selection.map(s => s.entryId + '/' + s.cell),
      p.groups.map(g => [g.group, g.attributes.map(a => [a.attribute, a.missing, a.variants.map(v => v.functions.map(f => f.name + f.dmxFrom + '-' + f.dmxTo).join(','))])]),
      p.raw.map(r => [r.entryId, r.offsets.map(o => o.offset)]), (p.presets || []).map(x => x.id + x.name + x.family), p.highlight && [p.highlight.lowlight, p.highlight.lowlightPercent],
      st.tab, Object.keys(st.models).length]);
  }
  function needModels() {
    const missing = sel().some(s => !st.models[s.entryId]);
    if (!missing || st.modelsLoading) return;
    st.modelsLoading = true;
    Api.getProgrammerFixtures().then(fx => {
      (fx.fixtures || []).forEach(f => { st.models[f.entryId] = f; });
    }).catch(() => {}).then(() => { st.modelsLoading = false; refresh(); });
  }

  function render() {
    reg.clear();
    cmds = [];
    clear(els.body);
    const p = prog();
    const groups = groupsOf();
    const raw = (p && p.raw) || [];
    if (!p || !sel().length) {
      els.body.appendChild(h('div', { class: 'b5-empty' }, h('span', { class: 'b5-empty__title', text: 'Nothing selected' }),
        h('span', { class: 'b5-empty__body', text: 'Select fixtures, cells, a group or a layer on the grid; their controls appear here.' })));
      return;
    }
    // State S: Disarmed is a panel note; values stay editable and retained.
    // I2d2: always drawn and kept true by syncLive — arming does not redraw
    // the panel, so it used to say "Disarmed" while output was live.
    const note = h('p', { class: 'b5-cc-output', 'data-output-note': '' }, ic('status-warning'), h('span', { 'data-output-note-text': '' }));
    els.body.appendChild(note);
    drawOutputNote();
    els.body.classList.toggle('is-fine', st.fine);
    if (groups.length) {
      if (!groups.some(g => g.group === st.tab)) st.tab = groups[0].group;
      // §14: role=tablist, roving tabindex, Left/Right/Home/End over the
      // families the selection has; a family it lacks shows "—" and why,
      // and is skipped.
      const has = new Map(groups.map(g => [g.group, g]));
      const live = FAMILIES.map(f => f[0]).filter(k => has.has(k));
      const tabs = h('div', { class: 'b5-cc-tabs', role: 'tablist', 'aria-label': 'Attribute families' });
      const pick = (k, focus) => { st.tab = k; refresh(true); if (focus) { const t = els.body.querySelector('[data-tab-group="' + k + '"]'); if (t) t.focus(); } };
      FAMILIES.forEach(([k, label]) => {
        const g = has.get(k);
        const on = k === st.tab;
        const count = g ? g.attributes.filter(a => a.touched).length + ' set' : '— not on these fixtures';
        const t = h('button', { type: 'button', role: 'tab', id: 'b5-cc-tab-' + k, class: 'b5-cc-tab' + (on ? ' is-on' : '') + (g ? '' : ' is-none'),
          'aria-selected': on ? 'true' : 'false', 'aria-controls': 'b5-cc-panel', tabindex: on ? '0' : '-1', 'data-tab-group': k,
          'aria-disabled': g ? null : 'true' },
          h('span', { class: 'b5-cc-tab__ic', 'aria-hidden': 'true', icon: FAMILY_ICON[k] }),
          h('span', { class: 'b5-cc-tab__word', text: label }),
          h('span', { class: 'b5-cc-tabcount', text: count }));
        if (g) t.addEventListener('click', () => pick(k, false));
        t.addEventListener('keydown', ev => {
          const i = live.indexOf(st.tab);
          const to = { ArrowRight: live[Math.min(live.length - 1, i + 1)], ArrowLeft: live[Math.max(0, i - 1)], Home: live[0], End: live[live.length - 1] }[ev.key];
          if (!to) return;
          if (ev.preventDefault) ev.preventDefault();
          pick(to, true);
        });
        tabs.appendChild(t);
      });
      els.body.appendChild(tabs);
      // On a phone the row scrolls: keep the selected family in view.
      const onTab = tabs.querySelector('.is-on');
      if (onTab && tabs.scrollWidth > tabs.clientWidth) {
        const a = onTab.getBoundingClientRect(), r = tabs.getBoundingClientRect();
        if (a.left < r.left || a.right > r.right) tabs.scrollLeft += a.left - r.left - 16;
      }
      const panel = h('div', { class: 'b5-cc-panel', role: 'tabpanel', id: 'b5-cc-panel', 'aria-labelledby': 'b5-cc-tab-' + st.tab, 'data-panel': st.tab });
      const tools = toolbar(st.tab);
      panel.appendChild(tools.top);
      const attrs = attrsOf(st.tab);
      if (st.tab === 'position') {
        // §5: the pad and its paired Pan/Tilt faders side by side when the
        // panel is wide enough (CSS container query), pad first on a phone.
        const pp = positionPanel();
        const paired = attrs.filter(a => a.attribute === 'Pan' || a.attribute === 'Tilt');
        if (pp) panel.appendChild(h('div', { class: 'b5-cc-poslayout', 'data-poslayout': '' }, pp, h('div', { class: 'b5-cc-pospair' }, paired.map(a => attributeCard(a, st.tab)))));
        attrs.filter(a => !pp || !paired.includes(a)).forEach(a => panel.appendChild(attributeCard(a, st.tab)));
      }
      if (st.tab === 'colour') panel.appendChild(colourPanel());
      if (st.tab === 'shaper') {
        // §11: the numbered diagram beside (T/D) or over (P) the labelled
        // A/B and angle faders, which carry the same information.
        const fig = h('div', { class: 'b5-cc-shaperdiag', 'data-shaper-slot': '' }, shaperDiagram());
        reg.set('shaper', { update: () => { clear(fig); fig.appendChild(shaperDiagram()); } });
        const byBlade = new Map();
        attrs.forEach(a => { const b = bladeOf(a.attribute); byBlade.set(b, (byBlade.get(b) || []).concat([a])); });
        const list = h('div', { class: 'b5-cc-shaperlist' });
        byBlade.forEach((as, b) => list.appendChild(h('section', { class: 'b5-cc-blade', 'data-blade': b }, h('h4', { text: b }), as.map(a => attributeCard(a, st.tab, bladeTitle(a.attribute))))));
        panel.appendChild(h('div', { class: 'b5-cc-shaperlayout' }, fig, list));
      } else if (st.tab === 'colour') {
        // colourPanel draws the colour cards itself, per mode (§9).
      } else if (st.tab !== 'position') {
        attrs.forEach(a => panel.appendChild(attributeCard(a, st.tab)));
      }
      if (cmds.length) panel.appendChild(cmdZone(cmds));
      panel.appendChild(tools.bottom);
      els.body.appendChild(panel);
    }
    // §15: the action bar's Clear / Fan panels are family-aware.
    if (typeof ConsoleScreen !== 'undefined' && ConsoleScreen.actionsChanged) ConsoleScreen.actionsChanged();
    const unprofiled = raw.filter(r => st.models[r.entryId] && st.models[r.entryId].profiled === false);
    const partial = raw.filter(r => !unprofiled.includes(r));
    if (unprofiled.length) {
      els.body.appendChild(h('section', { class: 'b5-cc-rawsec', 'data-raw-panel': '' },
        h('h3', { class: 'b5-group__head', text: 'Raw DMX — fixtures with no profile' }),
        h('p', { class: 'b5-caption', text: 'Only what the patch knows: one fader per channel. Release hands a channel back to its resting value.' }),
        rawPanel(unprofiled)));
    }
    const adv = h('details', { class: 'b5-cc-tool', 'data-raw-advanced': '' }, h('summary', { text: 'Advanced: raw channels' }),
      partial.length ? rawPanel(partial) : h('p', { class: 'b5-note', text: 'Every channel of the selected profiled fixtures belongs to an attribute; set them above. Only channels with no profile detail can be driven raw.' }));
    adv.open = !!st.open['#raw'];
    adv.addEventListener('toggle', () => { st.open['#raw'] = adv.open; });
    els.body.appendChild(adv);
  }

  function update() {
    for (const g of groupsOf()) for (const a of g.attributes) {
      reg.forEach(c => { if (c.update && c.attr === a.attribute) c.update(c.vi ? vview(a, c.vi) : (c.vi === 0 ? vview(a, 0) : a)); });
    }
    const pad = reg.get('pad');
    if (pad) pad.update();
    const shp = reg.get('shaper');
    if (shp) shp.update();
    const col = reg.get('colourref');
    if (col) col.update();
    const p = prog();
    ((p && p.raw) || []).forEach(r => r.offsets.forEach(o => { const c = reg.get('raw#' + r.entryId + '#' + o.offset); if (c) c.update(o); }));
    const set = {};
    groupsOf().forEach(g => { set[g.group] = g.attributes.filter(a => a.touched).length; });
    els.body.querySelectorAll('[data-tab-group]').forEach(t => {
      const n = set[t.getAttribute('data-tab-group')];
      const c = t.querySelector('.b5-cc-tabcount');
      if (c && n !== undefined) c.textContent = n + ' set';
    });
  }

  // refresh: rebuild when the selection's shape changed, else update values
  // in place (a control being dragged keeps its local value).
  function refresh(force) {
    if (!els.body || typeof ProgrammerSync === 'undefined') return;
    needModels();
    const sig = signature();
    if (force || sig !== st.sig) { st.sig = signature(); render(); } else update();
    drawCommands();
    // C8 hook: the MIDI encoder strip follows the open tab and the values.
    if (typeof ConsoleMIDI !== 'undefined') ConsoleMIDI.refresh();
  }

  function mount(region) {
    if (!region) return;
    st.root = region;
    clear(region);
    els.status = h('div', { class: 'b5-cc-status', role: 'status', 'aria-live': 'polite', 'data-cc-status': '' });
    els.body = h('div', { class: 'b5-cc-body', 'data-cc-body': '' });
    els.cmd = h('div', { class: 'b5-cc-status b5-cc-cmdprogress', role: 'status', 'aria-live': 'polite', 'data-cc-command': '' });
    els.cmd.hidden = true;
    region.appendChild(h('h2', { class: 'b5-board__head' },
      h('span', { class: 'b5-board__title', text: 'Controls' }),
      h('span', { class: 'b5-board__sub', text: 'live for the selection — ARM decides whether it reaches the rig' })));
    region.appendChild(els.status);
    region.appendChild(els.cmd);
    // C8 hook: the MIDI encoder strip (console-midi.js) sits between the
    // status line and the controls.
    if (typeof ConsoleMIDI !== 'undefined') { const m = h('section', { class: 'b5-midi', 'data-midi-region': '', 'aria-label': 'MIDI encoders' }); region.appendChild(m); ConsoleMIDI.mount(m); }
    region.appendChild(els.body);
    st.sig = '';
    refresh(true);
  }

  // C8 hook: lane is exported so MIDI encoders (console-midi.js) write
  // through the same throttled, latest-wins, single-flight writer.
  // colourOrder: the colour attributes the Colour tab draws, in order (null
  // before it has drawn), so the MIDI encoders follow the open mode.
  const colourOrder = () => st.colourCards || null;
  const actions = { family: () => st.tab, label: () => GROUP_LABEL[st.tab] || st.tab || '', clearScopes, fanForm, lowlightForm };
  return { mount, refresh, colourWrites, colourOrder, actions, _state: st, lane };
})();
