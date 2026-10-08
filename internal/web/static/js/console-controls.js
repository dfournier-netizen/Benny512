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
//   - Colour: a picker writing what each fixture HAS (see colourWrites);
//   - Shaper: attributes grouped by blade, plus a placeholder diagram;
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
  const GROUP_LABEL = { dimmer: 'Dimmer', position: 'Position', colour: 'Colour', beam: 'Beam', focus: 'Focus', shaper: 'Shaper', other: 'Control / Other' };
  const HIDDEN_FN = /^(NoFeature|Dummy)$/;
  const st = { root: null, tab: null, sig: '', fine: false, models: {}, modelsLoading: false, open: {}, statusTimer: null };
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
  function unit(attr) {
    if (/^(Pan|Tilt|Zoom)/.test(attr) || /Pos$|Rot/.test(attr)) return '°';
    if (/Strobe|Shutter/.test(attr)) return ' Hz';
    return '';
  }
  function physical(attr, f, v) {
    if (!f || !f.hasPhysicalRange || v < f.dmxFrom || v > f.dmxTo || f.dmxTo === f.dmxFrom) return null;
    const p = f.physicalFrom + (v - f.dmxFrom) / (f.dmxTo - f.dmxFrom) * (f.physicalTo - f.physicalFrom);
    return (Math.round(p * 10) / 10) + unit(attr);
  }
  function pct(v, max) { return max ? Math.round(v / max * 100) : 0; }
  function readout(a) {
    const max = v0(a).max;
    if (a.mixed) {
      const vs = a.channels.map(c => c.value);
      return 'MIXED ' + pct(Math.min(...vs), max) + '–' + pct(Math.max(...vs), max) + ' %';
    }
    const v = chanValue(a);
    const fi = activeFn(a);
    const f = fi >= 0 ? v0(a).functions[fi] : null;
    const ph = physical(a.attribute, f, v);
    return v + ' / ' + max + ' · ' + pct(v, max) + ' %' + (ph ? ' · ' + ph : '') + (f && v0(a).functions.length > 1 ? ' · ' + f.name : '');
  }
  function markers(a) {
    const out = [];
    out.push(a.allTouched ? tag('SET', 'accent') : a.touched ? tag('SET on some', 'accent') : tag('default'));
    if (a.mixed) out.push(tag('MIXED', 'warn'));
    if (a.unknownDefault) out.push(tag('default unknown', 'unread'));
    if (a.missing) out.push(tag('on ' + (sel().length - a.missing) + ' of ' + sel().length, 'open'));
    const d = v0(a).detail;
    if (d === 'attribute-only') out.push(tag('RDM slot: no ranges', 'open'));
    else if (d === 'first-function') out.push(tag('first function only', 'open'));
    return out;
  }
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
  // A fader over one function's DMX range, writing fractions within it.
  function functionFader(a, fi, key, vi) {
    const f = v0(a).functions[fi];
    const ref = { functionIndex: fi };
    const span = f.dmxTo - f.dmxFrom;
    const input = h('input', { type: 'range', class: 'b5-range-touch', min: f.dmxFrom, max: f.dmxTo, step: 1, 'data-fader': key,
      'aria-label': a.attribute + ' · ' + f.name + ' (' + f.dmxFrom + '–' + f.dmxTo + ')' });
    const val = h('span', { class: 'b5-text-mono b5-param__value', 'data-fader-value': key });
    const state = h('span', { class: 'b5-cc-fnstate', 'data-fader-state': key });
    let touchedAt = 0;
    const show = (v, active) => {
      val.textContent = active ? v + ' · ' + pct(v - f.dmxFrom, span) + ' %' + (physical(a.attribute, f, v) ? ' · ' + physical(a.attribute, f, v) : '') : 'not active';
      state.textContent = active ? 'ACTIVE' : '';
    };
    const push = v => {
      touchedAt = now();
      show(v, true);
      (reg.get(a.attribute + '#pad') || { local() {} }).local(v);
      lane(key, 'set', Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { fraction: span ? (v - f.dmxFrom) / span : 0 }));
    };
    input.addEventListener('input', () => push(Number(input.value)));
    input.addEventListener('change', () => push(Number(input.value)));
    const step = d => () => {
      const s = st.fine ? 1 : Math.max(1, Math.round(span / 100));
      const v = Math.max(f.dmxFrom, Math.min(f.dmxTo, Number(input.value) + d * s));
      input.value = v;
      push(v);
    };
    const row = h('div', { class: 'b5-param b5-cc-fader', 'data-function-row': key },
      h('span', { class: 'b5-param__label' }, h('span', { text: f.name }), h('span', { class: 'b5-caption', text: ' DMX ' + f.dmxFrom + '–' + f.dmxTo }), ' ', state, ' ', val),
      h('div', { class: 'b5-cc-faderrow' },
        btn('−', { 'aria-label': 'Step ' + f.name + ' down', 'data-step-down': key }, step(-1)), input,
        btn('+', { 'aria-label': 'Step ' + f.name + ' up', 'data-step-up': key }, step(1))));
    const update = at => {
      const v = chanValue(at), active = activeFn(at) === fi;
      if (now() - touchedAt < ACTIVE_MS) return;
      input.value = active ? v : f.dmxFrom;
      input.setAttribute('aria-valuetext', active ? v + ' (' + pct(v - f.dmxFrom, span) + ' %)' : 'not active');
      show(v, active);
    };
    reg.set(key, { attr: a.attribute, vi, update, local: v => { if (now() - touchedAt >= ACTIVE_MS) { input.value = v; show(v, true); } } });
    update(a);
    return row;
  }

  // A press-and-hold button (Control channels): fires after HOLD_MS of a
  // continuous press, by pointer or by Space/Enter held down.
  function holdButton(label, attrs, fire) {
    const fill = h('span', { class: 'b5-cc-hold__fill', 'aria-hidden': 'true' });
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn b5-btn--sm b5-cc-hold', 'aria-description': 'Hold for 0.75 seconds to send' }, attrs || {}),
      fill, h('span', { class: 'b5-cc-hold__word', text: 'HOLD 0.75 s · ' }), label);
    let timer = null;
    const start = ev => {
      if (ev && ev.preventDefault) ev.preventDefault();
      if (timer) return;
      b.classList.add('is-holding');
      timer = setTimeout(() => { timer = null; b.classList.remove('is-holding'); fire(); }, HOLD_MS);
    };
    const stop = () => { if (timer) { clearTimeout(timer); timer = null; } b.classList.remove('is-holding'); };
    b.addEventListener('pointerdown', start);
    ['pointerup', 'pointerleave', 'pointercancel'].forEach(t => b.addEventListener(t, stop));
    b.addEventListener('keydown', ev => { if ((ev.key === ' ' || ev.key === 'Enter') && !ev.repeat) start(ev); });
    b.addEventListener('keyup', ev => { if (ev.key === ' ' || ev.key === 'Enter') stop(); });
    b.addEventListener('click', ev => { if (ev && ev.preventDefault) ev.preventDefault(); });
    return b;
  }

  function slotLabel(s) {
    const n = s.hasWheelSlot ? 'Slot ' + s.wheelSlot : '';
    const name = s.hasSlotDetail && s.slotName ? s.slotName : s.name;
    return [n, name].filter(Boolean).join(' · ');
  }

  // attributeCard: everything for one attribute.
  function attributeCard(a, group) {
    const control = group === 'other';
    const head = h('summary', { class: 'b5-cc-attrhead' },
      h('strong', { class: 'b5-cc-attrname', text: a.attribute }),
      h('span', { class: 'b5-text-mono b5-cc-readout', 'data-readout': a.attribute, text: readout(a) }),
      h('span', { class: 'b5-cc-markers', 'data-markers': a.attribute }, markers(a)));
    const body = h('div', { class: 'b5-cc-attrbody' });
    const lbl = rdmLabel(a);
    if (lbl) body.appendChild(h('p', { class: 'b5-caption' }, 'RDM slot label: ', h('span', { class: 'b5-fromfile', text: lbl })));
    a.variants.forEach((_, vi) => {
      const av = vview(a, vi);
      const into = a.variants.length > 1 ? h('section', { class: 'b5-cc-variant', 'data-variant': a.attribute + '#' + vi }, h('h5', { class: 'b5-cc-varhead', text: 'For ' + layoutName(a, vi) })) : body;
      if (into !== body) body.appendChild(into);
      functionControls(av, vi, control, into);
    });
    if (control) body.appendChild(h('p', { class: 'b5-note', text: 'Control channels have no faders: a sweep would pass through reset and lamp ranges. Hold a button for 0.75 s to send it.' }));
    const card = h('details', { class: 'b5-cc-attr', 'data-attr': a.attribute }, head, body);
    card.open = st.open[a.attribute] !== false;
    card.addEventListener('toggle', () => { st.open[a.attribute] = card.open; });
    reg.set(a.attribute + '#card', { attr: a.attribute, update: at => {
      const r = card.querySelector('[data-readout]');
      if (r) r.textContent = readout(at);
      const m = card.querySelector('[data-markers]');
      if (m) { clear(m); markers(at).forEach(x => m.appendChild(x)); }
    } });
    return card;
  }

  // functionControls: slot buttons, set buttons, function faders (or HOLD
  // buttons on a Control channel) for one channel layout of an attribute.
  function functionControls(a, vi, control, body) {
    const fns = v0(a).functions;
    const k = i => a.attribute + '#' + (vi ? 'v' + vi + ':' : '') + i;
    // A slot or set button is IN when every channel of this layout sits in
    // its DMX range (in that function, for a slot).
    const mark = (b, fi, s) => {
      reg.set('btn#' + k(fi) + '#' + (s.hasWheelSlot ? s.wheelSlot : s.name), { attr: a.attribute, vi, update: at => {
        const v = chanValue(at);
        const inIt = !at.mixed && at.channels.length > 0 && (!s.hasWheelSlot || activeFn(at) === fi) && v >= s.dmxFrom && v <= s.dmxTo;
        b.setAttribute('aria-pressed', inIt ? 'true' : 'false');
        b.classList.toggle('is-on', !!inIt);
      } });
      reg.get('btn#' + k(fi) + '#' + (s.hasWheelSlot ? s.wheelSlot : s.name)).update(a);
    };
    fns.forEach((f, i) => {
      if (HIDDEN_FN.test(f.attribute)) return;
      const ref = { functionIndex: i };
      const slots = f.sets.filter(s => s.hasWheelSlot);
      const named = f.sets.filter(s => !s.hasWheelSlot && s.name);
      const box = h('div', { class: 'b5-cc-fn', 'data-function': k(i) });
      const plainSelection = slots.length && f.attribute === a.attribute;
      const single = f.dmxTo <= f.dmxFrom;
      if (fns.filter(x => !HIDDEN_FN.test(x.attribute)).length > 1 || slots.length || named.length) {
        box.appendChild(h('p', { class: 'b5-cc-fnname' }, h('strong', { text: f.name }), h('span', { class: 'b5-caption', text: ' · DMX ' + f.dmxFrom + '–' + f.dmxTo + (f.modeMaster ? ' · needs ' + f.modeMaster : '') })));
      }
      if (slots.length) {
        const row = h('div', { class: 'b5-cc-slots', role: 'group', 'aria-label': f.name + ' slots' });
        slots.forEach(s => {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { slot: s.wheelSlot });
          const lab = h('span', { class: 'b5-cc-slot__text' }, h('span', { class: 'b5-cc-slot__name', text: slotLabel(s) }),
            s.mediaFileName ? h('span', { class: 'b5-cc-slot__media', text: 'image ' + s.mediaFileName + ' (not shown yet)' }) : null);
          const sw = s.hasSRGB ? h('span', { class: 'b5-cc-swatch', 'aria-hidden': 'true', style: { background: s.srgb } }) : h('span', { class: 'b5-cc-swatch b5-cc-swatch--none', 'aria-hidden': 'true', text: '?' });
          const b = control ? holdButton(slotLabel(s), { 'data-slot': k(i) + '#' + s.wheelSlot }, () => write('set', body0, 'Sent ' + slotLabel(s) + '.'))
            : h('button', { type: 'button', class: 'b5-cc-slot', 'data-slot': k(i) + '#' + s.wheelSlot, 'aria-pressed': 'false' }, sw, lab);
          if (!control) b.addEventListener('click', () => write('set', body0));
          mark(b, i, s);
          row.appendChild(b);
        });
        box.appendChild(row);
      }
      if (named.length) {
        const row = h('div', { class: 'b5-cc-sets', role: 'group', 'aria-label': f.name + ' states' });
        named.forEach(s => {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { set: s.name });
          const b = control ? holdButton(s.name, { 'data-set': k(i) + '#' + s.name }, () => write('set', body0, 'Sent ' + s.name + '.'))
            : btn(s.name, { 'data-set': k(i) + '#' + s.name, 'aria-pressed': 'false' }, () => write('set', body0));
          mark(b, i, s);
          row.appendChild(b);
        });
        box.appendChild(row);
      }
      if (control) {
        if (!slots.length && !named.length) {
          const body0 = Object.assign({ targets: tgt(a), attribute: a.attribute }, ref, { fraction: 0 });
          box.appendChild(holdButton(f.name, { 'data-hold-fn': k(i) }, () => write('set', body0, 'Sent ' + f.name + '.')));
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
    const axes = [pan, tilt].map(a => a && { a, fi: ownFn(a), f: v0(a).functions[ownFn(a)], ref: fnRef(a, ownFn(a)) });
    const frac = ax => { if (!ax) return 0.5; const v = chanValue(ax.a); return ax.f.dmxTo > ax.f.dmxFrom ? Math.max(0, Math.min(1, (v - ax.f.dmxFrom) / (ax.f.dmxTo - ax.f.dmxFrom))) : 0; };
    let fx = frac(axes[0]), fy = frac(axes[1]), touchedAt = 0;
    const dot = h('span', { class: 'b5-cc-pad__dot', 'aria-hidden': 'true' });
    const read = h('p', { class: 'b5-text-mono b5-cc-padread', 'data-pad-readout': '' });
    const pad = h('div', { class: 'b5-cc-pad', tabindex: '0', role: 'application', 'data-xypad': '', 'aria-label': 'Pan and tilt pad: arrow keys move, Shift for big steps' },
      h('span', { class: 'b5-cc-pad__h', 'aria-hidden': 'true' }), h('span', { class: 'b5-cc-pad__v', 'aria-hidden': 'true' }),
      h('span', { class: 'b5-cc-pad__ax b5-cc-pad__ax--x', text: 'Pan →' }), h('span', { class: 'b5-cc-pad__ax b5-cc-pad__ax--y', text: 'Tilt ↑' }), dot);
    const dmx = (ax, f) => Math.round(ax.f.dmxFrom + f * (ax.f.dmxTo - ax.f.dmxFrom));
    const draw = () => {
      dot.style.setProperty('left', (fx * 100) + '%');
      dot.style.setProperty('top', ((1 - fy) * 100) + '%');
      const part = (name, ax, f) => ax ? name + ' ' + dmx(ax, f) + ' (' + Math.round(f * 100) + ' %' + (physical(ax.a.attribute, ax.f, dmx(ax, f)) ? ', ' + physical(ax.a.attribute, ax.f, dmx(ax, f)) : '') + ')' : name + ' not on these fixtures';
      read.textContent = part('Pan', axes[0], fx) + ' · ' + part('Tilt', axes[1], fy);
      pad.setAttribute('aria-valuetext', read.textContent);
    };
    const go = (nx, ny) => {
      touchedAt = now();
      nx = Math.max(0, Math.min(1, nx)); ny = Math.max(0, Math.min(1, ny));
      if (axes[0] && nx !== fx && axes[0].ref) { lane('Pan#' + axes[0].fi, 'set', Object.assign({ targets: tgt(axes[0].a), attribute: 'Pan' }, axes[0].ref, { fraction: nx })); (reg.get('Pan#' + axes[0].fi) || { local() {} }).local(dmx(axes[0], nx)); }
      if (axes[1] && ny !== fy && axes[1].ref) { lane('Tilt#' + axes[1].fi, 'set', Object.assign({ targets: tgt(axes[1].a), attribute: 'Tilt' }, axes[1].ref, { fraction: ny })); (reg.get('Tilt#' + axes[1].fi) || { local() {} }).local(dmx(axes[1], ny)); }
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
      if (st.fine) go(drag.fx0 + (ev.clientX - drag.x0) / drag.r.width * 0.1, drag.fy0 - (ev.clientY - drag.y0) / drag.r.height * 0.1);
      else go((ev.clientX - drag.r.left) / drag.r.width, 1 - (ev.clientY - drag.r.top) / drag.r.height);
    });
    const end = () => { drag = null; };
    pad.addEventListener('pointerup', end);
    pad.addEventListener('pointercancel', end);
    pad.addEventListener('keydown', ev => {
      const step = ev.shiftKey ? 0.05 : st.fine ? 1 / 65535 * 16 : 0.005;
      const d = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] }[ev.key];
      if (!d) return;
      if (ev.preventDefault) ev.preventDefault();
      go(fx + d[0], fy + d[1]);
    });
    // Tandem: the faders tell the pad when they move.
    if (axes[0]) reg.set('Pan#pad', { local: v => { fx = (v - axes[0].f.dmxFrom) / Math.max(1, axes[0].f.dmxTo - axes[0].f.dmxFrom); draw(); } });
    if (axes[1]) reg.set('Tilt#pad', { local: v => { fy = (v - axes[1].f.dmxFrom) / Math.max(1, axes[1].f.dmxTo - axes[1].f.dmxFrom); draw(); } });
    reg.set('pad', { update: () => {
      if (now() - touchedAt < ACTIVE_MS || drag) return;
      const p = findAttr('Pan'), t = findAttr('Tilt');
      if (axes[0] && p) { axes[0].a = p; fx = frac(axes[0]); }
      if (axes[1] && t) { axes[1].a = t; fy = frac(axes[1]); }
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
    return h('section', { class: 'b5-cc-position', 'aria-label': 'Pan and tilt' }, pad, read,
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
  function colourPanel() {
    const caps = colourCaps();
    const n = { mix: caps.mixT.size, cmy: caps.cmyT.size, wheel: caps.wheelOnly.size };
    if (!n.mix && !n.cmy && !n.wheel) return h('p', { class: 'b5-note', 'data-colour-none': '', text: 'No selected fixture mixes colour or has a colour wheel with stated colours; use the faders below.' });
    const capLine = [n.mix ? n.mix + ' mix RGB' : '', n.cmy ? n.cmy + ' mix CMY' : '', n.wheel ? n.wheel + ' wheel only (nearest slot)' : ''].filter(Boolean).join(' · ');
    const swatch = h('span', { class: 'b5-cc-swatch b5-cc-swatch--big', 'aria-hidden': 'true' });
    const picker = h('input', { type: 'color', class: 'b5-cc-colorinput', 'data-colour-input': '', 'aria-label': 'Pick a colour', value: '#ffffff' });
    const apply = (hex, final) => {
      const rgb = hexRGB(hex);
      if (!rgb) return;
      swatch.style.setProperty('background', hex);
      const { writes, nearest } = colourWrites(rgb);
      writes.forEach(w => lane(w.key, 'set', w.body));
      if (final && nearest.length) say('Wheel-only fixtures → nearest slot: ' + [...new Set(nearest)].join(', ') + '.');
    };
    picker.addEventListener('input', () => apply(picker.value, false));
    picker.addEventListener('change', () => apply(picker.value, true));
    const presets = [['White', '#ffffff'], ['Red', '#ff0000'], ['Orange', '#ff8000'], ['Yellow', '#ffff00'], ['Green', '#00ff00'], ['Cyan', '#00ffff'], ['Blue', '#0000ff'], ['Magenta', '#ff00ff']];
    const row = h('div', { class: 'b5-cc-swatches', role: 'group', 'aria-label': 'Quick colours' });
    presets.forEach(([name, hex]) => row.appendChild(h('button', { type: 'button', class: 'b5-cc-quick', 'data-colour': hex, onclick: () => { picker.value = hex; apply(hex, true); } },
      h('span', { class: 'b5-cc-swatch', 'aria-hidden': 'true', style: { background: hex } }), name)));
    return h('section', { class: 'b5-cc-colour', 'aria-label': 'Colour picker' },
      h('p', { class: 'b5-cc-capline', 'data-colour-caps': '' }, h('strong', { text: 'Can do: ' }), capLine),
      h('div', { class: 'b5-row' }, h('label', { class: 'b5-row' }, swatch, h('span', { text: 'Pick' }), picker)),
      row,
      h('p', { class: 'b5-caption', text: 'DMX values, not measured colour. RGB fixtures get R, G and B (white, amber and UV stay on their own faders); CMY fixtures get C = 1 − R, M = 1 − G, Y = 1 − B; wheel-only fixtures go to the nearest stated slot colour.' }));
  }

  // --- Shaper -------------------------------------------------------------------
  function bladeOf(name) {
    const m = /^(Blade|Shaper)(\d+)/.exec(name);
    return m ? 'Blade ' + m[2] : 'Assembly';
  }
  function shaperDiagram() {
    const depth = n => { const a = findAttr('Blade' + n + 'A') || findAttr('Shaper' + n + 'A'); return a ? pct(chanValue(a), v0(a).max) / 2 : 0; };
    return h('div', { class: 'b5-cc-shaper', 'aria-hidden': 'true' },
      h('span', { class: 'b5-cc-shaper__b b5-cc-shaper__b--1', style: { height: depth(1) + '%' } }),
      h('span', { class: 'b5-cc-shaper__b b5-cc-shaper__b--2', style: { width: depth(2) + '%' } }),
      h('span', { class: 'b5-cc-shaper__b b5-cc-shaper__b--3', style: { height: depth(3) + '%' } }),
      h('span', { class: 'b5-cc-shaper__b b5-cc-shaper__b--4', style: { width: depth(4) + '%' } }),
      h('span', { class: 'b5-cc-shaper__word', text: 'diagram placeholder' }));
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
  function ask(o) {
    // In-app dialog, the same contract as console.js's (native prompt()
    // fails in the embedded browser).
    return new Promise(resolve => {
      let done = false;
      const input = o.field ? h('input', { class: 'b5-input', type: 'text', maxlength: 80, 'data-ask-input': '' }) : null;
      if (input) input.value = o.field.value || '';
      const err = h('p', { class: 'b5-field__error', role: 'alert' });
      const ok = btn(o.ok || 'Confirm', { 'data-ask-ok': '' }, null, o.danger ? 'b5-btn--danger' : 'b5-btn--primary');
      const cancel = btn('Cancel', { 'data-ask-cancel': '' });
      const dlg = h('dialog', { class: 'b5-workspace-dialog b5-con-dialog', 'data-ask': '', 'aria-label': o.title }, h('h3', { text: o.title }),
        o.body ? h('p', { class: 'b5-note', text: o.body }) : null,
        input ? h('label', { class: 'b5-field' }, h('span', { class: 'b5-field__label', text: o.field.label }), input) : null, err,
        h('div', { class: 'b5-row b5-con-dialog__actions' }, cancel, ok));
      const finish = v => { if (done) return; done = true; try { if (dlg.open) dlg.close(); } catch (_) { /* closed */ } dlg.remove(); resolve(v); };
      ok.addEventListener('click', () => {
        if (!input) return finish(true);
        const v = input.value.trim();
        if (!v) { err.textContent = 'Type a name first.'; return; }
        finish(v);
      });
      cancel.addEventListener('click', () => finish(null));
      dlg.addEventListener('cancel', ev => { ev.preventDefault(); finish(null); });
      document.body.appendChild(dlg);
      dlg.showModal();
      (input || ok).focus();
    });
  }
  function toolbar(group) {
    const p = prog();
    const hl = (p && p.highlight) || {};
    const label = GROUP_LABEL[group] || group;
    const clearRow = h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Clear the programmer' },
      h('span', { class: 'b5-cc-rowlabel', text: 'Clear' }),
      btn(label + ' on selection', { 'data-clear-group': group }, () => write('clear', { scope: 'selection', group }, r => 'Cleared ' + r.released + ' ' + label + ' channel(s) of the selection.')),
      btn('Selection values', { 'data-clear-selection-values': '' }, () => write('clear', { scope: 'selection' }, r => 'Cleared ' + r.released + ' channel(s) of the selection.')),
      btn('Everything', { 'data-clear-all': '' }, () => write('clear', { scope: 'all' }, r => 'Cleared the whole programmer (' + r.released + ' channel(s)) and the selection.')));
    const pctIn = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, 'data-lowlight-percent': '', 'aria-label': 'Lowlight level in percent' });
    pctIn.value = hl.lowlightPercent !== undefined ? hl.lowlightPercent : 20;
    const lowRow = h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Lowlight' },
      h('button', { type: 'button', class: 'b5-seg' + (hl.lowlight ? ' is-on' : ''), 'aria-pressed': hl.lowlight ? 'true' : 'false', 'data-lowlight': '',
        onclick: () => write('highlight', { lowlight: !hl.lowlight }) }, hl.lowlight ? 'Lowlight: ON' : 'Lowlight: OFF'),
      h('label', { class: 'b5-row' }, pctIn, h('span', { text: '%' })),
      btn('Set level', { 'data-lowlight-set': '' }, () => {
        const v = Number(pctIn.value);
        if (!(v >= 0 && v <= 100) || v !== Math.round(v)) return say('The lowlight level is a whole percentage from 0 to 100.', 'error');
        write('highlight', { lowlightPercent: v }, 'Lowlight level set to ' + v + ' %.');
      }),
      h('span', { class: 'b5-caption', text: 'Lowlight dims everything not selected, only while Highlight (summary bar) is ON.' }));
    const fine = h('button', { type: 'button', class: 'b5-seg' + (st.fine ? ' is-on' : ''), 'aria-pressed': st.fine ? 'true' : 'false', 'data-fine': '', title: 'Fine: the ± buttons step one DMX value and the pad moves a tenth as far',
      onclick: () => { st.fine = !st.fine; fine.textContent = st.fine ? 'Fine: ON' : 'Fine: OFF'; fine.classList.toggle('is-on', st.fine); fine.setAttribute('aria-pressed', st.fine ? 'true' : 'false'); } }, st.fine ? 'Fine: ON' : 'Fine: OFF');
    clearRow.appendChild(fine);
    const low = h('details', { class: 'b5-cc-tool', 'data-lowlight-tool': '' }, h('summary', { text: 'Lowlight · ' + (hl.lowlight ? 'ON ' + hl.lowlightPercent + ' %' : 'OFF') }), lowRow);
    low.open = !!st.open['#low'];
    low.addEventListener('toggle', () => { st.open['#low'] = low.open; });
    return { top: h('div', { class: 'b5-cc-toolbar' }, clearRow), bottom: h('div', { class: 'b5-cc-toolbar b5-cc-toolbar--tools' }, presetPanel(group), fanPanel(group), low) };
  }
  function fanPanel(group) {
    const attrs = attrsOf(group);
    const aSel = h('select', { class: 'b5-select', 'data-fan-attr': '' });
    attrs.forEach(a => aSel.appendChild(h('option', { value: a.attribute, text: a.attribute })));
    const fSel = h('select', { class: 'b5-select', 'data-fan-fn': '' });
    const fill = () => {
      clear(fSel);
      const a = attrs.find(x => x.attribute === aSel.value);
      fSel.appendChild(h('option', { value: '', text: 'whole channel' }));
      if (a) v0(a).functions.forEach((f, i) => { if (!HIDDEN_FN.test(f.attribute) && fnRef(a, i)) fSel.appendChild(h('option', { value: String(i), text: f.name + ' (' + f.dmxFrom + '–' + f.dmxTo + ')' })); });
      fSel.value = '';
    };
    aSel.addEventListener('change', fill);
    if (attrs.length) aSel.value = attrs[0].attribute;
    fill();
    const from = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, value: 0, 'data-fan-from': '', 'aria-label': 'Fan from, percent' });
    const to = h('input', { type: 'number', class: 'b5-input b5-cc-num', min: 0, max: 100, step: 1, value: 100, 'data-fan-to': '', 'aria-label': 'Fan to, percent' });
    from.value = '0'; to.value = '100';
    const shape = h('select', { class: 'b5-select', 'data-fan-shape': '' },
      h('option', { value: 'linear', text: 'Linear: first → last' }), h('option', { value: 'reverse', text: 'Reverse: last → first' }), h('option', { value: 'mirror', text: 'Mirror: centre out' }));
    shape.value = 'linear';
    const go = () => {
      const a = attrs.find(x => x.attribute === aSel.value);
      if (!a) return say('Pick an attribute to fan.', 'error');
      const body = { attribute: a.attribute };
      if (fSel.value !== '') Object.assign(body, fnRef(a, Number(fSel.value)));
      Object.assign(body, { shape: shape.value, from: { fraction: Number(from.value) / 100 }, to: { fraction: Number(to.value) / 100 } });
      write('fan', body, r => 'Fanned ' + a.attribute + ' over ' + ((r && r.applied) || []).length + ' channel(s) in selection order.');
    };
    const d = h('details', { class: 'b5-cc-tool', 'data-fan': '' }, h('summary', { text: 'Fan' }),
      attrs.length ? h('div', { class: 'b5-cc-fanform' },
        h('label', { class: 'b5-row' }, h('span', { text: 'Attribute' }), aSel), h('label', { class: 'b5-row' }, h('span', { text: 'Function' }), fSel),
        h('label', { class: 'b5-row' }, h('span', { text: 'From %' }), from), h('label', { class: 'b5-row' }, h('span', { text: 'To %' }), to),
        h('label', { class: 'b5-row' }, h('span', { text: 'Shape' }), shape), btn('Fan', { 'data-fan-go': '' }, go, 'b5-btn--primary'),
        h('p', { class: 'b5-caption', text: 'Spreads values across the selection in its selection order (the numbers on the grid).' })) : h('p', { class: 'b5-note', text: 'Nothing to fan in this group.' }));
    d.open = !!st.open['#fan'];
    d.addEventListener('toggle', () => { st.open['#fan'] = d.open; });
    return d;
  }
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
    list.forEach(pr => row.appendChild(btn(pr.name, { 'data-preset': pr.id, title: pr.fixtures + ' fixture(s), ' + pr.channels + ' channel(s)' }, () => write('presets/recall', { id: pr.id }, recallReport))));
    if (!list.length) row.appendChild(h('span', { class: 'b5-caption', text: 'No ' + label + ' presets yet.' }));
    const manage = h('div', { class: 'b5-cc-presetmanage' });
    list.forEach(pr => manage.appendChild(h('div', { class: 'b5-row', 'data-preset-row': pr.id }, h('strong', { text: pr.name }),
      btn('Overwrite', { 'data-preset-overwrite': pr.id }, () => write('presets/overwrite', { id: pr.id }, 'Overwrote ' + pr.name + ' with the selection\'s ' + label + ' values.')),
      btn('Rename…', { 'data-preset-rename': pr.id }, async () => { const n = await ask({ title: 'Rename preset', field: { label: 'Preset name', value: pr.name }, ok: 'Rename' }); if (n) write('presets/rename', { id: pr.id, name: n }, 'Renamed.'); }),
      btn('Delete…', { 'data-preset-delete': pr.id }, async () => { if (await ask({ title: 'Delete preset ' + pr.name + '?', ok: 'Delete', danger: true })) write('presets/delete', { id: pr.id }, 'Deleted ' + pr.name + '.'); }, 'b5-btn--danger'))));
    const d = h('details', { class: 'b5-cc-tool', 'data-presets': group }, h('summary', { text: label + ' presets · ' + list.length }),
      row,
      h('div', { class: 'b5-row' }, btn('Store ' + label + ' preset…', { 'data-preset-store': group }, async () => {
        const n = await ask({ title: 'Store a ' + label + ' preset', body: 'Stores the ' + label + ' values the programmer holds for the selection.', field: { label: 'Preset name' }, ok: 'Store' });
        if (n) write('presets/store', { name: n, family: group }, 'Stored ' + label + ' preset ' + n + '.');
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
    clear(els.body);
    const p = prog();
    const groups = groupsOf();
    const raw = (p && p.raw) || [];
    if (!p || !sel().length) {
      els.body.appendChild(h('div', { class: 'b5-empty' }, h('span', { class: 'b5-empty__title', text: 'Nothing selected' }),
        h('span', { class: 'b5-empty__body', text: 'Select fixtures, cells, a group or a layer on the grid; their controls appear here.' })));
      return;
    }
    if (p.output && !p.output.live) els.body.appendChild(h('p', { class: 'b5-cc-output', 'data-output-note': '' }, ic('status-warning'), p.output.note || 'Output is not live.'));
    if (groups.length) {
      if (!groups.some(g => g.group === st.tab)) st.tab = groups[0].group;
      const tabs = h('div', { class: 'b5-cc-tabs', role: 'tablist', 'aria-label': 'Attribute groups' });
      groups.forEach(g => {
        const set = g.attributes.filter(a => a.touched).length;
        tabs.appendChild(h('button', { type: 'button', role: 'tab', class: 'b5-cc-tab' + (g.group === st.tab ? ' is-on' : ''), 'aria-selected': g.group === st.tab ? 'true' : 'false', 'data-tab-group': g.group,
          onclick: () => { st.tab = g.group; refresh(true); } }, h('span', { text: GROUP_LABEL[g.group] || g.label }), h('span', { class: 'b5-cc-tabcount', text: set ? ' · ' + set + ' set' : '' })));
      });
      els.body.appendChild(tabs);
      const panel = h('div', { class: 'b5-cc-panel', role: 'tabpanel', 'data-panel': st.tab });
      const tools = toolbar(st.tab);
      panel.appendChild(tools.top);
      const attrs = attrsOf(st.tab);
      if (st.tab === 'position') { const pp = positionPanel(); if (pp) panel.appendChild(pp); }
      if (st.tab === 'colour') panel.appendChild(colourPanel());
      if (st.tab === 'shaper') {
        panel.appendChild(shaperDiagram());
        const byBlade = new Map();
        attrs.forEach(a => { const b = bladeOf(a.attribute); byBlade.set(b, (byBlade.get(b) || []).concat([a])); });
        byBlade.forEach((list, b) => panel.appendChild(h('section', { class: 'b5-cc-blade', 'data-blade': b }, h('h4', { text: b }), list.map(a => attributeCard(a, st.tab)))));
      } else {
        attrs.forEach(a => panel.appendChild(attributeCard(a, st.tab)));
      }
      panel.appendChild(tools.bottom);
      els.body.appendChild(panel);
    }
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
    const p = prog();
    ((p && p.raw) || []).forEach(r => r.offsets.forEach(o => { const c = reg.get('raw#' + r.entryId + '#' + o.offset); if (c) c.update(o); }));
    const set = {};
    groupsOf().forEach(g => { set[g.group] = g.attributes.filter(a => a.touched).length; });
    els.body.querySelectorAll('[data-tab-group]').forEach(t => {
      const n = set[t.getAttribute('data-tab-group')];
      const c = t.querySelector('.b5-cc-tabcount');
      if (c) c.textContent = n ? ' · ' + n + ' set' : '';
    });
  }

  // refresh: rebuild when the selection's shape changed, else update values
  // in place (a control being dragged keeps its local value).
  function refresh(force) {
    if (!els.body || typeof ProgrammerSync === 'undefined') return;
    needModels();
    const sig = signature();
    if (force || sig !== st.sig) { st.sig = signature(); render(); } else update();
    // C8 hook: the MIDI encoder strip follows the open tab and the values.
    if (typeof ConsoleMIDI !== 'undefined') ConsoleMIDI.refresh();
  }

  function mount(region) {
    if (!region) return;
    st.root = region;
    clear(region);
    els.status = h('div', { class: 'b5-cc-status', role: 'status', 'aria-live': 'polite', 'data-cc-status': '' });
    els.body = h('div', { class: 'b5-cc-body', 'data-cc-body': '' });
    region.appendChild(h('h2', { class: 'b5-board__head' },
      h('span', { class: 'b5-board__title', text: 'Controls' }),
      h('span', { class: 'b5-board__sub', text: 'live for the selection — ARM decides whether it reaches the rig' })));
    region.appendChild(els.status);
    // C8 hook: the MIDI encoder strip (console-midi.js) sits between the
    // status line and the controls.
    if (typeof ConsoleMIDI !== 'undefined') { const m = h('section', { class: 'b5-midi', 'data-midi-region': '', 'aria-label': 'MIDI encoders' }); region.appendChild(m); ConsoleMIDI.mount(m); }
    region.appendChild(els.body);
    st.sig = '';
    refresh(true);
  }

  // C8 hook: lane is exported so MIDI encoders (console-midi.js) write
  // through the same throttled, latest-wins, single-flight writer.
  return { mount, refresh, colourWrites, _state: st, lane };
})();
