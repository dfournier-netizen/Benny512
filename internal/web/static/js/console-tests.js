// console-tests.js — the Console's TESTS panel, chunk C6c: Rig Check folded
// into the Console (owner decision 2026-10-06). It is a thin rendering of the
// C5 server (internal/web/tests.go):
//
//   GET  /api/tests[?kind=&group=&layer=]   the whole view (or another
//                                           scope's catalog)
//   POST /api/tests/set {tests, scope?}     the hand-picked (ad-hoc) tests
//   POST /api/tests/clear | fade {fadeMs}
//   POST /api/tests/sequence-save {id?, name, steps} | sequence-rename | sequence-delete
//   POST /api/tests/run-start {id} | run-next | run-back | run-pause |
//        run-resume | run-stop | run-jump {step}
//
// RULES THIS FILE KEEPS
//  1. The server view is the only state. Every write answers with the whole
//     view, which replaces this page's copy; nothing here is optimistic.
//  2. One tests layer per station, shared by every browser. Every change
//     broadcasts {"type":"tests","revision":N}; a browser whose revision
//     differs re-reads. Writes carry the revision this page last saw
//     (X-Benny-Tests) and are sent one at a time, so a write based on a view
//     another browser has already changed is refused (409), the view is
//     re-read and the refusal is said in words.
//  3. Tests sit UNDER manual values: a channel set by hand in the programmer
//     wins. Nothing reaches the rig unless the master output is Armed. Test
//     toggles and their parameters are direct-action (the signed-off
//     Apply-to-confirm exception for Rig Check toggles).
//  4. Every state is a word first (ON/OFF, NOW, testing/skipped), then an
//     icon and a border; never colour alone. No native prompt()/confirm():
//     in-app dialogs only.
//
// The presentation tables (test names, hints, which parameters a kind takes)
// are the old Rig Check screen's, moved here when that screen was retired
// (C7), so a test reads as it always has; a kind with no phrase still renders
// under the server's own name, marked "name from file".
const ConsoleTests = (() => {
  const FADES = [0, 250, 500, 1000, 2000, 3000, 5000, 10000, 30000];
  const SCOPES = [['selection', 'Selection'], ['group', 'Stored group'], ['layer', 'Layer'], ['all', 'All fixtures']];
  const st = {
    view: null,        // last GET/POST /api/tests body
    known: null,       // its revision
    open: true,        // panel expanded
    msg: '', tone: '', // status line
    expanded: {},      // test id -> settings open
    fixtures: {},      // test id -> per-fixture list open
    editor: null,      // {id, name, steps} while a sequence is being edited
    catalogs: {},      // scope key -> available[] (sequence editor)
    deadline: 0,       // Date.now() when the running auto step advances
    selKey: '',        // programmer selection signature last seen
  };
  const els = {};
  let opts = { layers: () => [] };
  let running = null, again = false, chain = Promise.resolve(), ticker = null;

  try { st.open = localStorage.getItem('benny512.console.testsOpen') !== 'closed'; } catch (_) { /* storage blocked: stay open */ }

  // --- small DOM helpers (same shape as console.js's) -------------------------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const k of Object.keys(props)) {
        const v = props[k];
        if (v === undefined || v === null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = String(v);
        else if (k === 'icon') el.innerHTML = UI.icon(v);
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
  function ic(name) { return h('span', { class: 'b5-con-ic', 'aria-hidden': 'true', icon: name }); }
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }
  function btn(label, attrs, onclick, o) {
    o = o || {};
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn' + (o.cls ? ' ' + o.cls : '') }, attrs || {}), o.icon ? ic(o.icon) : null, label);
    if (o.disabled) b.disabled = true;
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function seg(label, on, attrs, onclick, disabled) {
    const b = h('button', Object.assign({ type: 'button', class: 'b5-seg' + (on ? ' is-on' : ''), 'aria-pressed': on ? 'true' : 'false' }, attrs || {}), label);
    if (disabled) b.disabled = true;
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function select(attrs, options, value, onchange) {
    const s = h('select', Object.assign({ class: 'b5-select' }, attrs || {}));
    options.forEach(([v, label]) => {
      const o = h('option', { value: v, text: label });
      if (String(v) === String(value)) o.setAttribute('selected', '');
      s.appendChild(o);
    });
    s.value = String(value);
    if (onchange) s.addEventListener('change', () => onchange(s.value));
    return s;
  }

  // ask({title, body, field:{label, value}, ok, danger}): the in-app dialog
  // (native prompt() fails in the embedded browser). Resolves to the trimmed
  // text, true for a plain confirm, or null on cancel.
  function ask(o) {
    return new Promise(resolve => {
      let done = false;
      const input = o.field ? h('input', { class: 'b5-input', type: 'text', maxlength: o.field.maxLength || 80, 'data-ask-input': '' }) : null;
      if (input) input.value = o.field.value || '';
      const err = h('p', { class: 'b5-field__error', role: 'alert' });
      const okBtn = btn(o.ok || 'Confirm', { 'data-ask-ok': '' }, null, { cls: o.danger ? 'b5-btn--danger' : 'b5-btn--primary' });
      const cancel = btn('Cancel', { 'data-ask-cancel': '' });
      const dlg = h('dialog', { class: 'b5-workspace-dialog b5-con-dialog', 'data-ask': '', 'aria-label': o.title },
        h('h3', { text: o.title }),
        o.body ? h('p', { class: 'b5-note', text: o.body }) : null,
        input ? h('label', { class: 'b5-field' }, h('span', { class: 'b5-field__label', text: o.field.label }), input) : null,
        err, h('div', { class: 'b5-row b5-con-dialog__actions' }, cancel, okBtn));
      const finish = v => {
        if (done) return;
        done = true;
        try { if (dlg.open) dlg.close(); } catch (_) { /* closed */ }
        dlg.remove();
        resolve(v);
      };
      okBtn.addEventListener('click', () => {
        if (!input) return finish(true);
        const v = input.value.trim();
        if (!v) { err.textContent = 'Type ' + o.field.label.toLowerCase() + ' first.'; return; }
        finish(v);
      });
      cancel.addEventListener('click', () => finish(null));
      dlg.addEventListener('cancel', ev => { ev.preventDefault(); finish(null); });
      if (input) input.addEventListener('keydown', ev => { if (ev.key === 'Enter') { ev.preventDefault(); okBtn.click(); } });
      document.body.appendChild(dlg);
      dlg.showModal();
      (input || okBtn).focus();
    });
  }

  // --- presentation tables ------------------------------------------------------
  // Moved here verbatim from the retired Rig Check screen (rigcheck.js, C7),
  // so a test reads exactly as it did there.
  const GROUPS = [
    { id: 'dimmer', label: 'Dimmer' },
    { id: 'position', label: 'Position' },
    { id: 'colour', label: 'Colour' },
    { id: 'beam', label: 'Beam' },
    { id: 'focus', label: 'Focus' },
    { id: 'shaper', label: 'Shaper' },
    { id: 'other', label: 'Other' },
  ];

  // KIND_LABEL / TARGET_LABEL / KIND_HINT are PRESENTATION ONLY — a human
  // phrase for a wire id the server already sent us. They never decide which
  // tests exist (that is available[]); a kind missing from these tables still
  // renders, using the server's own label, flagged as "name from file".
  const KIND_LABEL = {
    dimmer_sine: 'Dimmer',
    dimmer_toggle: 'Dimmer on/off',
    move_extreme: 'Move to',
    ballyhoo: 'Ballyhoo',
    colour_wheel_step: 'Colour wheel — step slots',
    colour_mix_sweep: 'Colour mix — sweep',
    colour_fade: 'Colour mix — fade hues',
    frost: 'Frost',
    gobo_step: 'Gobo wheel — step slots',
    gobo_rotate: 'Gobo wheel — rotate',
    prism_in_out: 'Prism in/out',
    prism_spin: 'Prism spin',
    animation_spin: 'Animation wheel spin',
    manual_value: 'Hold',
    shaper_individual: 'Shapers — one at a time',
    shaper_all: 'Shapers — all together',
    shaper_rotate: 'Shaper assembly rotate',
  };

  const TARGET_LABEL = {
    pan_max: 'pan max', pan_min: 'pan min', pan_centre: 'pan centre',
    tilt_max: 'tilt max', tilt_min: 'tilt min', tilt_centre: 'tilt centre',
    focus_max: 'focus far', focus_min: 'focus near',
    zoom_max: 'zoom wide', zoom_min: 'zoom narrow',
    focus: 'focus', zoom: 'zoom',
  };

  const KIND_HINT = {
    dimmer_sine: 'Drives the dimmer between min and max.',
    dimmer_toggle: 'Holds the dimmer at max (on) or min (off). No cycle — flip it by hand.',
    move_extreme: 'Parks one axis at a fixed point. Hold it there to check travel.',
    ballyhoo: 'Continuous pan/tilt sweep.',
    colour_wheel_step: 'Steps the colour wheel through its named slots.',
    colour_mix_sweep: 'Sweeps the RGB mix channels together.',
    colour_fade: 'Fades the RGB mix through a hue cycle.',
    frost: 'Drives this frost flag between min and max.',
    gobo_step: 'Steps this gobo wheel through its named slots.',
    gobo_rotate: 'Rotates this gobo wheel continuously.',
    prism_in_out: 'Swings the prism between out and in.',
    prism_spin: 'Spins the prism continuously.',
    animation_spin: 'Spins the animation wheel continuously.',
    manual_value: 'Holds this channel at a fixed value you set.',
    shaper_individual: 'Runs each shaper blade in turn, one at a time.',
    shaper_all: 'Moves every shaper blade together.',
    shaper_rotate: 'Rotates the whole shaper assembly.',
  };

  // ENUMERATED_KINDS produce one test per real GDTF function on the rig
  // (one per frost flag, one per gobo wheel), so their label is the fixture
  // file's own name for that function and must be shown, not replaced.
  const ENUMERATED_KINDS = { frost: true, gobo_step: true, gobo_rotate: true };

  // STATIC_KINDS mirrors isStaticPatternKind (testpattern.go): these have no
  // cycle, so they take neither a waveform nor a rate, and the server
  // REJECTS a non-zero phase on them with a 400. The phase and waveform
  // controls are therefore not rendered for them at all — the owner is never
  // shown a control whose only outcome is an error.
  const STATIC_KINDS = { move_extreme: true, manual_value: true, dimmer_toggle: true };

  // PARAMS_FOR: which parameter controls a kind actually uses, from the
  // engine's own per-kind value computation (testpattern.go's patternValue
  // switch). Anything not listed here is not rendered rather than rendered
  // and silently ignored.
  const PARAMS_FOR = {
    dimmer_sine: { rate: true, range: true },
    dimmer_toggle: { range: true, on: true },
    move_extreme: {},
    ballyhoo: { rate: true, range: true },
    colour_wheel_step: { rate: true, range: true },
    colour_mix_sweep: { rate: true, range: true },
    colour_fade: { rate: true, range: true },
    frost: { rate: true, range: true },
    gobo_step: { rate: true, range: true },
    gobo_rotate: { rate: true, range: true, direction: true },
    prism_in_out: { rate: true, range: true },
    prism_spin: { rate: true, range: true, direction: true },
    animation_spin: { rate: true, range: true, direction: true },
    manual_value: { value: true },
    shaper_individual: { rate: true, range: true },
    shaper_all: { rate: true, range: true },
    shaper_rotate: { rate: true, range: true, direction: true },
  };

  // RATE_BOUNDS: the Rate slider's range, per kind. Every rate-bearing test
  // shares DEFAULT_RATE_BOUNDS; a kind listed here overrides it.
  //
  // Ballyhoo is the one override, at Dom's instruction after a real gig: the
  // shared minimum was still too fast to watch a moving head through, and the
  // shared maximum threw fixtures around harder than a rig check ever needs
  // to. So its minimum is a TENTH of the shared one and its maximum is HALF,
  // giving 0.005-2.5 Hz -- a 200-second sweep at the slow end, 0.4 seconds at
  // the fast end. The step drops with the minimum, because a 0.05 step cannot
  // express 0.005 and a control whose smallest increment is ten times its
  // minimum is a control that cannot reach its own low end.
  //
  // Deliberately per-kind and NOT a change to the shared bounds: every other
  // rate-bearing test keeps the range it was tuned and signed off with.
  const DEFAULT_RATE_BOUNDS = { min: 0.05, max: 5, step: 0.05 };
  const RATE_BOUNDS = {
    ballyhoo: { min: 0.005, max: 2.5, step: 0.005 },
  };

  function rateBoundsFor(kind) { return RATE_BOUNDS[kind] || DEFAULT_RATE_BOUNDS; }

  function paramsFor(kind) { return PARAMS_FOR[kind] || { rate: true, range: true }; }
  function isStatic(kind) { return !!STATIC_KINDS[kind]; }

  // testLabel: the human phrase for one available[] row, plus whether that
  // phrase is the app's own or a raw name repeated out of the fixture file.
  // available[].labelFromGdtf false means the server fell back to the GDTF
  // attribute name — the app is echoing the file, not naming the feature,
  // and the tile says so rather than passing it off as a considered label.
  function testLabel(a) {
    const kindWord = KIND_LABEL[a.kind];
    if (ENUMERATED_KINDS[a.kind]) {
      const base = kindWord || a.kind;
      // GDTF's own name for the function often already contains the kind
      // word ("Frost Light" for a frost test), and "Frost — Frost Light"
      // reads like a bug. Only prefix when the file's name does not already
      // say what this is.
      const same = a.label && a.label.toLowerCase().indexOf(String(base).toLowerCase().split(' ')[0]) === 0;
      return { text: same ? a.label : `${base} — ${a.label}`, fromFile: !a.labelFromGdtf };
    }
    if (a.target && TARGET_LABEL[a.target] && kindWord) {
      return { text: `${kindWord} ${TARGET_LABEL[a.target]}`, fromFile: false };
    }
    if (kindWord && !a.target) return { text: kindWord, fromFile: false };
    // No phrase of our own for this kind/target combination: show what the
    // server called it and mark it, rather than inventing a name.
    return { text: a.label || a.id, fromFile: true };
  }

  function groups() { return GROUPS; }
  function labelOf(a) { return testLabel(a); }
  function rateBounds(kind) { return rateBoundsFor(kind); }
  function hint(kind) { return KIND_HINT[kind] || ''; }
  function fadeWord(ms) { return ms === 0 ? 'Snap (0 s)' : (ms / 1000) + ' s'; }

  // --- spec helpers ---------------------------------------------------------------
  const testId = t => t.target ? t.kind + ':' + t.target : t.kind;
  // specOf: the request shape of a stored or reported test, every field.
  function specOf(t) {
    return {
      kind: t.kind, rateHz: t.rateHz || 0, min: t.min || 0, max: t.max === undefined ? 255 : t.max,
      target: t.target || '', direction: t.direction || '', value: t.value || 0, on: !!t.on,
      waveform: t.waveform || 'sine', offsetMin: t.offsetMin || 0, offsetMax: t.offsetMax || 0,
    };
  }
  // specForAvailable: what a newly turned-on test starts with. rateHz 0 asks
  // the engine for its per-kind default; max 255 so the span is not zero.
  function specForAvailable(a) {
    return {
      kind: a.kind, rateHz: 0, min: 0, max: 255, target: a.target || '',
      direction: paramsFor(a.kind).direction ? 'cw' : '', value: 128, on: true,
      waveform: 'sine', offsetMin: 0, offsetMax: 0,
    };
  }
  const v = () => st.view;
  const runActive = () => !!(st.view && st.view.run && st.view.run.active);
  const adHoc = () => ((st.view && st.view.adHoc) || []).map(specOf);
  function statusById() {
    const m = {};
    ((st.view && st.view.tests) || []).forEach(t => { m[t.id] = t; });
    return m;
  }
  function targetName(entryId, cell) {
    const t = ((st.view && st.view.targets) || []).find(x => x.entryId === entryId && (x.cell || '') === (cell || ''));
    return t ? t.name : 'a fixture no longer in scope';
  }
  function storedGroups() {
    const p = typeof ProgrammerSync !== 'undefined' ? ProgrammerSync.state() : null;
    return (p && p.storedGroups) || [];
  }
  function layerList() {
    try { return (opts.layers && opts.layers()) || []; } catch (_) { return []; }
  }
  function scopeWords(sc) {
    if (!sc) return 'the selection';
    if (sc.kind === 'group') { const g = storedGroups().find(x => x.id === sc.group); return 'group ' + (g ? g.name : '(no longer stored)'); }
    if (sc.kind === 'layer') { const l = layerList().find(x => x.id === sc.layer); return 'layer ' + (l ? l.name : '(no longer on the plan)'); }
    if (sc.kind === 'all') return 'every patched fixture';
    return 'the Console selection';
  }

  // --- server -----------------------------------------------------------------------
  // refresh re-reads the view; calls arriving while one is in flight fold
  // into one more read after it, so the last read is never older than the
  // last announcement.
  function refresh() {
    if (running) { again = true; return running; }
    running = (async () => {
      try {
        do {
          again = false;
          setView(await Api.getTests());
        } while (again);
        render();
        return st.view;
      } catch (e) {
        say('the tests could not be read (' + e.message + ').', 'error');
        return null;
      } finally {
        running = null;
      }
    })();
    return running;
  }

  // setView takes a received view: its revision, and the moment the running
  // auto step advances (the countdown runs from it, not from each render).
  function setView(view) {
    st.view = view;
    st.known = view.revision;
    if (view.run && view.run.active && typeof view.run.remainingMs === 'number') st.deadline = Date.now() + view.run.remainingMs;
  }

  // act posts one tests action with the revision this page last saw. body
  // may be a function: it is then built when the write is sent, from the
  // view the previous write answered with, so two quick edits in a row
  // never send a list that has lost the first one.
  function act(action, body, done) {
    const run = async () => {
      try {
        const res = await Api.testsAction(action, typeof body === 'function' ? body() : body, st.known);
        setView(res);
        st.msg = ''; st.tone = '';
        if (done) say(typeof done === 'function' ? done(res) : done);
        render();
        return res;
      } catch (e) {
        await refresh();
        say(e.message, 'error');
        return null;
      }
    };
    const p = chain.then(run, run);
    chain = p.catch(() => {});
    return p;
  }

  function say(msg, tone) {
    st.msg = msg || ''; st.tone = tone || '';
    renderStatus();
  }

  // --- rendering -----------------------------------------------------------------
  function mount(container, o) {
    opts = Object.assign(opts, o || {});
    els.root = container;
    clear(container);
    els.toggle = h('button', { type: 'button', class: 'b5-btn b5-ct-toggle', 'data-tests-toggle': '', 'aria-controls': 'conTestsBody' });
    els.toggle.addEventListener('click', () => {
      st.open = !st.open;
      try { localStorage.setItem('benny512.console.testsOpen', st.open ? 'open' : 'closed'); } catch (_) { /* fine */ }
      render();
    });
    els.summary = h('span', { class: 'b5-ct-headword', 'data-tests-summary': '' });
    els.status = h('p', { class: 'b5-con-status', role: 'status', 'data-tests-status': '' });
    els.body = h('div', { class: 'b5-ct-body', id: 'conTestsBody', 'data-tests-body': '' });
    container.appendChild(h('h2', { class: 'b5-board__head b5-ct-head' }, els.toggle, els.summary));
    container.appendChild(els.status);
    container.appendChild(els.body);
    render();
    refresh();
  }

  function render() {
    if (!els.root) return;
    els.toggle.setAttribute('aria-expanded', st.open ? 'true' : 'false');
    clear(els.toggle);
    els.toggle.appendChild(ic(st.open ? 'chevron-collapse' : 'chevron-expand'));
    els.toggle.appendChild(document.createTextNode(st.open ? 'Tests · hide' : 'Tests · show'));
    renderSummary();
    renderStatus();
    clear(els.body);
    els.body.hidden = !st.open;
    syncTicker();
    if (!st.open) return;
    const view = v();
    if (!view) { els.body.appendChild(h('p', { class: 'b5-board__empty', text: 'Loading the tests…' })); return; }
    els.body.appendChild(h('p', { class: 'b5-caption b5-ct-rule', 'data-tests-rule': '' },
      'Tests act on the scope below and sit under manual values: a channel you set by hand wins over a test. They reach the rig only while output is Armed.'));
    if (view.owner === 'rigcheck') {
      els.body.appendChild(h('p', { class: 'b5-note b5-ct-warn', 'data-tests-owner': '' }, ic('status-warning'),
        'A test preset loaded from Show tools is driving the tests right now. Turning a test on here, or running a sequence, takes them back.'));
    }
    if (view.run && view.run.active) els.body.appendChild(renderRun());
    else if (view.run && view.run.endedReason) {
      els.body.appendChild(h('p', { class: 'b5-caption', 'data-run-ended': '' }, 'The last sequence run ended: ' + view.run.endedReason + '.'));
    }
    els.body.appendChild(renderScope());
    els.body.appendChild(renderCatalog());
    const warn = renderWarnings();
    if (warn) els.body.appendChild(warn);
    els.body.appendChild(st.editor ? renderEditor() : renderSequences());
  }

  function renderSummary() {
    if (!els.summary) return;
    clear(els.summary);
    const view = v();
    if (!view) return;
    let word, icon;
    if (runActive()) { word = (view.run.paused ? 'Sequence paused' : 'Sequence running') + ' · step ' + (view.run.step + 1) + ' of ' + view.run.stepCount; icon = view.run.paused ? 'status-pending' : 'status-ok'; }
    else if ((view.adHoc || []).length) { word = view.adHoc.length + (view.adHoc.length === 1 ? ' test on' : ' tests on'); icon = 'status-ok'; }
    else { word = 'No tests on'; icon = 'status-pending'; }
    els.summary.appendChild(h('span', { class: 'b5-pill b5-pill--sm' + (icon === 'status-ok' ? ' b5-pill--accent' : '') }, ic(icon), word));
  }

  function renderStatus() {
    if (!els.status) return;
    clear(els.status);
    els.status.className = 'b5-con-status' + (st.tone ? ' b5-con-status--' + st.tone : '');
    if (!st.msg) return;
    const parts = st.tone === 'error' ? [ic('status-error'), h('strong', { text: 'Not done: ' }), st.msg] : [ic('status-ok'), st.msg];
    parts.forEach(p => els.status.appendChild(typeof p === 'string' ? document.createTextNode(p) : p));
  }

  // --- the running sequence -------------------------------------------------------
  function remainingWords() {
    const r = v().run;
    if (r.advance.mode !== 'auto') return r.paused ? 'Paused on a manual step.' : 'Manual step: press Next to go on.';
    const ms = r.paused ? (r.remainingMs || 0) : Math.max(0, st.deadline - Date.now());
    const s = (ms / 1000).toFixed(1) + ' s';
    return r.paused ? 'Paused with ' + s + ' left on this step.' : 'Next step in ' + s + '.';
  }
  function syncTicker() {
    const view = v();
    const live = !!(view && view.run && view.run.active && !view.run.paused && view.run.advance.mode === 'auto');
    if (live && !ticker) {
      ticker = setInterval(() => {
        const el = els.root && els.root.querySelector('[data-run-remaining]');
        if (el && runActive()) el.textContent = remainingWords();
      }, 250);
    } else if (!live && ticker) { clearInterval(ticker); ticker = null; }
  }

  function renderRun() {
    const r = v().run;
    const seq = (v().sequences || []).find(s => s.id === r.sequenceId);
    const steps = seq ? seq.steps : [];
    const last = r.step >= r.stepCount - 1;
    const jump = select({ 'data-run-jump-step': '', 'aria-label': 'Step to jump to' },
      Array.from({ length: r.stepCount }, (_, i) => [i, 'Step ' + (i + 1) + (steps[i] ? ' · ' + steps[i].name : '')]), r.step);
    const list = h('ol', { class: 'b5-ct-steps', 'data-run-steps': '' });
    for (let i = 0; i < r.stepCount; i++) {
      const s = steps[i];
      const now = i === r.step;
      list.appendChild(h('li', { class: 'b5-ct-step' + (now ? ' is-now' : ''), 'data-run-step': i, 'aria-current': now ? 'step' : null },
        h('span', { class: 'b5-ct-step__word', text: now ? 'NOW' : (i < r.step ? 'done' : 'next') }),
        h('span', { text: 'Step ' + (i + 1) + (s ? ' · ' + s.name : '') }),
        s ? h('span', { class: 'b5-caption', text: ' · ' + (s.advance.mode === 'auto' ? 'auto ' + s.advance.seconds + ' s' : 'manual') }) : null));
    }
    return h('section', { class: 'b5-ct-run' + (r.paused ? ' is-paused' : ''), 'aria-label': 'Running sequence', 'data-run': '' },
      h('p', { class: 'b5-ct-run__title' }, ic(r.paused ? 'status-pending' : 'status-ok'),
        h('strong', { text: (r.paused ? 'Paused: ' : 'Running: ') + r.sequenceName })),
      h('p', { class: 'b5-ct-run__step', 'data-run-step-word': '', text: 'Step ' + (r.step + 1) + ' of ' + r.stepCount + ' · ' + r.stepName }),
      h('p', { class: 'b5-ct-run__left', 'data-run-remaining': '', 'aria-live': 'polite', text: remainingWords() }),
      h('div', { class: 'b5-row b5-ct-run__buttons', role: 'group', 'aria-label': 'Sequence controls' },
        btn('Back', { 'data-run-back': '' }, () => act('run-back', {}), { disabled: r.step === 0 }),
        btn('Next', { 'data-run-next': '' }, () => act('run-next', {}), { disabled: last, cls: 'b5-btn--primary' }),
        r.paused ? btn('Resume', { 'data-run-resume': '' }, () => act('run-resume', {}), { icon: 'status-ok' })
          : btn('Pause', { 'data-run-pause': '' }, () => act('run-pause', {}), { icon: 'status-pending' }),
        btn('Stop sequence', { 'data-run-stop': '' }, () => act('run-stop', {}, 'Stopped the sequence; its tests are off.'), { cls: 'b5-btn--danger', icon: 'status-error' })),
      h('div', { class: 'b5-row b5-ct-run__buttons' },
        h('label', { class: 'b5-con-inline' }, h('span', { text: 'Jump to ' }), jump),
        btn('Jump', { 'data-run-jump': '' }, () => act('run-jump', { step: Number(jump.value) }))),
      list);
  }

  // --- scope, fade ------------------------------------------------------------------
  function setScope(sc) {
    return act('set', () => ({ tests: adHoc(), scope: sc }), 'Tests now act on ' + scopeWords(sc) + '.');
  }
  function renderScope() {
    const view = v();
    const sc = view.scope || { kind: 'selection' };
    const locked = runActive();
    const kinds = h('div', { class: 'b5-segmented b5-ct-scope', role: 'group', 'aria-label': 'Test scope' });
    SCOPES.forEach(([k, label]) => kinds.appendChild(seg(label, sc.kind === k, { 'data-scope-kind': k }, () => {
      if (k === sc.kind) return;
      if (k === 'group') {
        const g = storedGroups()[0];
        if (!g) return say('There are no stored groups yet; select fixtures and use Store group first.', 'error');
        return setScope({ kind: 'group', group: g.id });
      }
      if (k === 'layer') {
        const l = layerList()[0];
        if (!l) return say('The plan has no layers yet.', 'error');
        return setScope({ kind: 'layer', layer: l.id });
      }
      return setScope({ kind: k });
    }, locked)));
    const row = h('div', { class: 'b5-row b5-ct-scoperow' }, kinds);
    if (sc.kind === 'group') {
      const s = select({ 'data-scope-group': '', 'aria-label': 'Stored group' }, storedGroups().map(g => [g.id, g.name]), sc.group, val => setScope({ kind: 'group', group: val }));
      s.disabled = locked;
      row.appendChild(s);
    }
    if (sc.kind === 'layer') {
      const s = select({ 'data-scope-layer': '', 'aria-label': 'Layer' }, layerList().map(l => [l.id, l.name]), sc.layer, val => setScope({ kind: 'layer', layer: val }));
      s.disabled = locked;
      row.appendChild(s);
    }
    const fades = FADES.slice();
    if (!fades.includes(view.fadeMs)) fades.push(view.fadeMs);
    fades.sort((a, b) => a - b);
    const fade = select({ 'data-fade': '', 'aria-describedby': 'conTestsFadeHint' }, fades.map(ms => [ms, fadeWord(ms)]), view.fadeMs,
      val => act('fade', { fadeMs: Number(val) }, 'Fade time is now ' + fadeWord(Number(val)) + '.'));
    const targets = view.targets || [];
    const names = targets.slice(0, 6).map(t => t.name).join(', ') + (targets.length > 6 ? ', +' + (targets.length - 6) + ' more' : '');
    return h('section', { class: 'b5-ct-section', 'aria-label': 'Scope and fade' },
      h('h3', { class: 'b5-group__head', text: 'Scope' }),
      row,
      locked ? h('p', { class: 'b5-caption', text: 'A sequence is running: each step sets its own scope. Stop it to choose a scope here.' }) : null,
      h('p', { class: 'b5-caption', 'data-scope-targets': '' },
        targets.length ? 'Testing ' + targets.length + (targets.length === 1 ? ' fixture: ' : ' fixtures: ') + names + '.' : (view.note || 'No fixture is in this scope.')),
      (view.ignored || []).length ? h('p', { class: 'b5-caption', text: view.ignored.length + ' left out: ' + view.ignored.map(i => i.reason).filter((x, i, a) => a.indexOf(x) === i).join(' ') }) : null,
      h('label', { class: 'b5-con-inline b5-ct-fade' }, h('span', { text: 'Fade time ' }), fade),
      h('p', { class: 'b5-caption', id: 'conTestsFadeHint', text: 'Applies to level changes and to tests starting or stopping. Disarm stays immediate.' }));
  }

  // --- the catalog ------------------------------------------------------------------
  function renderCatalog() {
    const view = v();
    const avail = view.available || [];
    const status = statusById();
    const onIds = new Set(runActive() ? (view.tests || []).map(t => t.id) : adHoc().map(testId));
    const sec = h('section', { class: 'b5-ct-section', 'aria-label': 'Tests', 'data-catalog': '' },
      h('div', { class: 'b5-row b5-ct-cathead' },
        h('h3', { class: 'b5-group__head', text: 'Tests for this scope · ' + onIds.size + ' on' }),
        btn('All tests off', { 'data-tests-clear': '' }, () => act('clear', {}, 'Every test is off.'), { disabled: !onIds.size && !runActive(), cls: 'b5-btn--sm' })));
    if (runActive()) sec.appendChild(h('p', { class: 'b5-caption', text: 'The running sequence chooses the tests; stop it to turn tests on and off by hand.' }));
    if (!avail.length) {
      sec.appendChild(h('p', { class: 'b5-board__empty', 'data-catalog-empty': '', text: (view.note || 'The fixtures in this scope report no testable functions.') + ' Tests come from what each fixture\'s profile says it can do.' }));
      return sec;
    }
    const byGroup = {};
    avail.forEach(a => { (byGroup[a.group] = byGroup[a.group] || []).push(a); });
    const known = new Set(groups().map(g => g.id));
    const order = groups().filter(g => byGroup[g.id]).concat(Object.keys(byGroup).filter(k => !known.has(k)).map(k => ({ id: k, label: k })));
    for (const g of order) {
      const grid = h('div', { class: 'b5-tilegrid b5-ct-tiles' });
      byGroup[g.id].forEach(a => grid.appendChild(tile(a, onIds.has(a.id), status[a.id])));
      sec.appendChild(h('div', { class: 'b5-group b5-ct-group' }, h('h4', { class: 'b5-group__head', text: g.label }), grid));
    }
    return sec;
  }

  function coverage(a, on, s) {
    const total = (v().targets || []).length;
    if (on && s) {
      const notes = [s.appliedCount + ' of ' + s.totalScope + ' testing'];
      if (s.skippedCount) notes.push(s.skippedCount + ' skipped');
      if (s.inferredCount) notes.push(s.inferredCount + ' from RDM');
      if (s.missingDetailCount) notes.push(s.missingDetailCount + ' no slot data');
      return notes.join(' · ');
    }
    return a.fixtureCount + ' of ' + total + (total === 1 ? ' fixture has this' : ' fixtures have this');
  }

  function toggleTest(a, on) {
    return act('set', () => {
      const list = adHoc().filter(t => testId(t) !== a.id);
      if (on) list.push(specForAvailable(a));
      return { tests: list };
    });
  }
  function editTest(id, change) {
    return act('set', () => ({ tests: adHoc().map(t => testId(t) === id ? Object.assign(t, change) : t) }));
  }

  function tile(a, on, s) {
    const lbl = labelOf(a);
    const locked = runActive();
    const open = !!st.expanded[a.id];
    const t = h('div', { class: 'b5-tile b5-ct-tile' + (on ? ' is-on' : ''), 'data-test-tile': a.id });
    const toggle = h('button', { type: 'button', class: 'b5-tile__toggle', 'data-test-toggle': a.id, 'aria-pressed': on ? 'true' : 'false' },
      h('span', { class: 'b5-pill b5-pill--sm' + (on ? ' b5-pill--accent b5-pill--solid' : '') }, on ? ic('status-ok') : null, h('span', { class: 'b5-tile__stateword', text: on ? 'ON' : 'OFF' })),
      h('span', { class: 'b5-tile__label' }, lbl.text, lbl.fromFile ? h('span', { class: 'b5-fromfile', title: 'This name is repeated from the fixture file, not written by this app', text: ' name from file' }) : null),
      h('span', { class: 'b5-caption', 'data-test-coverage': a.id, text: coverage(a, on, s) }));
    if (locked) toggle.disabled = true;
    toggle.addEventListener('click', () => toggleTest(a, !on));
    t.appendChild(toggle);
    const more = h('button', { type: 'button', class: 'b5-tile__more', 'data-test-more': a.id, 'aria-expanded': open ? 'true' : 'false', 'aria-label': 'Settings for ' + lbl.text },
      ic(open ? 'chevron-collapse' : 'chevron-expand'), h('span', { text: 'Settings' }));
    more.addEventListener('click', () => { st.expanded[a.id] = !open; render(); });
    t.appendChild(more);
    if (open) {
      const panel = h('div', { class: 'b5-tile__panel' });
      const spec = adHoc().find(x => testId(x) === a.id);
      if (on && spec && !locked) panel.appendChild(params(a.id, Object.assign(specOf(spec), s ? { rateHz: s.rateHz, waveform: s.waveform } : {})));
      else panel.appendChild(h('p', { class: 'b5-caption', text: locked ? 'The running sequence sets this test\'s parameters.' : 'Turn this test ON to set its rate, levels, waveform and phase. It is live at once and reaches the rig only while output is Armed.' }));
      if (on && s && (s.entries || []).length) panel.appendChild(fixtureList(a.id, s));
      t.appendChild(panel);
    }
    return t;
  }

  function fixtureList(id, s) {
    const open = !!st.fixtures[id];
    const b = h('button', { type: 'button', class: 'b5-btn b5-btn--sm', 'data-test-fixtures': id, 'aria-expanded': open ? 'true' : 'false' },
      ic(open ? 'chevron-collapse' : 'chevron-expand'), 'Per fixture · ' + s.entries.length);
    b.addEventListener('click', () => { st.fixtures[id] = !open; render(); });
    const wrap = h('div', { class: 'b5-ct-fixtures' }, b);
    if (open) {
      const ul = h('ul', { class: 'b5-ct-fixlist' });
      s.entries.forEach(e => {
        const words = [e.applied ? 'testing' : 'skipped — no such function on this fixture'];
        if (e.applied && e.inferred) words.push('slots from RDM, not the profile');
        if (e.applied && e.detailMissing) words.push('no slot data');
        if (e.applied && e.phaseDegrees) words.push('phase ' + Math.round(e.phaseDegrees) + '°');
        ul.appendChild(h('li', { 'data-fixture-status': e.entryId + (e.cell ? '/' + e.cell : '') },
          ic(e.applied ? 'status-ok' : 'status-pending'), h('strong', { text: targetName(e.entryId, e.cell) }), ' · ' + words.join(' · ')));
      });
      wrap.appendChild(ul);
    }
    return wrap;
  }

  function slider(id, field, label, value, min, max, step, suffix) {
    const out = h('span', { class: 'b5-text-mono b5-param__value', text: String(value) + (suffix || '') });
    const input = h('input', { type: 'range', class: 'b5-range-touch', 'data-param': id + ':' + field, min, max, step, 'aria-label': label });
    input.value = String(value);
    input.addEventListener('input', () => { out.textContent = input.value + (suffix || ''); });
    input.addEventListener('change', () => editTest(id, { [field]: Number(input.value) }));
    return h('div', { class: 'b5-param' }, h('span', { class: 'b5-param__label' }, label + ' ', out), input);
  }
  function choice(id, field, label, options, value) {
    const g = h('div', { class: 'b5-segmented b5-segmented--sm', role: 'group', 'aria-label': label });
    options.forEach(([val, word]) => g.appendChild(seg(word, value === val, { 'data-param-choice': id + ':' + field + ':' + String(val) }, () => editTest(id, { [field]: val }))));
    return h('div', { class: 'b5-param' }, h('span', { class: 'b5-param__label', text: label }), g);
  }
  function params(id, t) {
    const p = paramsFor(t.kind);
    const box = h('div', { class: 'b5-ct-params' }, h('p', { class: 'b5-caption', text: (hint(t.kind) + ' Changes apply at once.').trim() }));
    if (p.on) box.appendChild(choice(id, 'on', 'State', [[true, 'On (max)'], [false, 'Off (min)']], !!t.on));
    if (p.value) box.appendChild(slider(id, 'value', 'Value', t.value, 0, 255, 1));
    if (p.rate) { const rb = rateBounds(t.kind); box.appendChild(slider(id, 'rateHz', 'Rate', t.rateHz, rb.min, rb.max, rb.step, ' Hz')); }
    if (p.range) { box.appendChild(slider(id, 'min', 'Min level', t.min, 0, 255, 1)); box.appendChild(slider(id, 'max', 'Max level', t.max, 0, 255, 1)); }
    if (p.direction) box.appendChild(choice(id, 'direction', 'Direction', [['cw', 'Clockwise'], ['ccw', 'Counter-clockwise']], t.direction === 'ccw' ? 'ccw' : 'cw'));
    if (!isStatic(t.kind)) {
      box.appendChild(choice(id, 'waveform', 'Waveform', [['sine', 'Sine (smooth)'], ['snap', 'Snap (hard)']], t.waveform === 'snap' ? 'snap' : 'sine'));
      const spread = (t.offsetMin || 0) === 0 && (t.offsetMax || 0) === 360 ? 'chase' : ((t.offsetMin || 0) === 0 && (t.offsetMax || 0) === 0 ? 'unison' : 'custom');
      const g = h('div', { class: 'b5-segmented b5-segmented--sm', role: 'group', 'aria-label': 'Phase across the fixtures' },
        seg('Unison', spread === 'unison', { 'data-param-phase': id + ':unison' }, () => editTest(id, { offsetMin: 0, offsetMax: 0 })),
        seg('Chase (0–360°)', spread === 'chase', { 'data-param-phase': id + ':chase' }, () => editTest(id, { offsetMin: 0, offsetMax: 360 })));
      box.appendChild(h('div', { class: 'b5-param' }, h('span', { class: 'b5-param__label', text: 'Phase across the fixtures' + (spread === 'custom' ? ' · ' + t.offsetMin + '° to ' + t.offsetMax + '°' : '') }), g));
    }
    return box;
  }

  // --- contested slots and base-state warnings -----------------------------------
  function renderWarnings() {
    const view = v();
    const items = [];
    const status = statusById();
    const word = id => { const a = (view.available || []).find(x => x.id === id); return a ? labelOf(a).text : (status[id] ? labelOf(status[id]).text : id); };
    (view.contested || []).forEach(c => {
      items.push(h('li', { 'data-contested': c.universe + '/' + c.channel },
        ic('status-warning'), h('strong', { text: targetName(c.entryId, c.cell) }),
        ' · universe ' + UI.formatUser(c.universe) + ' channel ' + c.channel + ' is driven by more than one test: ' + (c.tests || []).map(word).join(' and ') + '.'));
    });
    const bs = view.baseState || {};
    (bs.shutterUnknownEntries || []).forEach(id => {
      items.push(h('li', { 'data-shutter-unknown': id }, ic('status-warning'), h('strong', { text: targetName(id, '') }),
        ' · the profile does not say where its shutter is open, so it is left at 0 and may not give light.'));
    });
    if (!items.length) return null;
    return h('section', { class: 'b5-ct-section b5-ct-warnings', 'aria-label': 'Contested slots and warnings' },
      h('h3', { class: 'b5-group__head', text: 'Contested slots and warnings · ' + items.length }),
      h('ul', { class: 'b5-ct-fixlist' }, items));
  }

  // --- sequences ----------------------------------------------------------------------
  function renderSequences() {
    const view = v();
    const seqs = view.sequences || [];
    const r = view.run || {};
    const list = h('ul', { class: 'b5-ct-seqlist', 'data-seq-list': '' });
    seqs.forEach(s => {
      const isRunning = r.active && r.sequenceId === s.id;
      list.appendChild(h('li', { class: 'b5-ct-seq' + (isRunning ? ' is-now' : ''), 'data-seq': s.id },
        h('span', { class: 'b5-ct-seq__name' }, h('strong', { text: s.name }), h('span', { class: 'b5-caption', text: ' · ' + s.steps.length + (s.steps.length === 1 ? ' step' : ' steps') + (isRunning ? ' · RUNNING' : '') })),
        h('span', { class: 'b5-row' },
          btn(isRunning ? 'Restart' : 'Run', { 'data-seq-run': s.id }, () => act('run-start', { id: s.id }, 'Started ' + s.name + '.'), { cls: 'b5-btn--sm b5-btn--primary', icon: 'status-ok' }),
          btn('Edit…', { 'data-seq-edit': s.id }, () => openEditor(s), { cls: 'b5-btn--sm', disabled: isRunning }),
          btn('Rename…', { 'data-seq-rename': s.id }, async () => {
            const name = await ask({ title: 'Rename sequence', field: { label: 'Sequence name', value: s.name, maxLength: (view.limits && view.limits.nameLength) || 80 }, ok: 'Rename' });
            if (name !== null) act('sequence-rename', { id: s.id, name }, 'Renamed the sequence to ' + name + '.');
          }, { cls: 'b5-btn--sm' }),
          btn('Delete…', { 'data-seq-delete': s.id }, async () => {
            if (await ask({ title: 'Delete the sequence ' + s.name + '?', body: 'Only the stored sequence goes; nothing on the rig changes.', ok: 'Delete', danger: true })) act('sequence-delete', { id: s.id }, 'Deleted ' + s.name + '.');
          }, { cls: 'b5-btn--sm b5-btn--danger', disabled: isRunning }))));
    });
    return h('section', { class: 'b5-ct-section', 'aria-label': 'Test sequences' },
      h('div', { class: 'b5-row b5-ct-cathead' },
        h('h3', { class: 'b5-group__head', text: 'Sequences · ' + seqs.length }),
        btn('New sequence…', { 'data-seq-new': '' }, () => openEditor(null), { cls: 'b5-btn--sm' })),
      seqs.length ? list : h('p', { class: 'b5-caption', text: 'No sequences yet. A sequence chains steps of tests; each step has its own scope, fade and advance (by hand or after a time).' }));
  }

  function newStep(n) {
    const sc = (v() && v().scope) || { kind: 'selection' };
    return { name: 'Step ' + n, tests: adHoc(), scope: { kind: sc.kind, group: sc.group || '', layer: sc.layer || '' }, fadeMs: null, advance: { mode: 'manual', seconds: 0 } };
  }
  function openEditor(s) {
    st.editor = s
      ? { id: s.id, name: s.name, steps: s.steps.map(x => ({ name: x.name, tests: (x.tests || []).map(specOf), scope: Object.assign({ kind: 'selection', group: '', layer: '' }, x.scope || {}), fadeMs: x.fadeMs === undefined ? null : x.fadeMs, advance: { mode: x.advance && x.advance.mode === 'auto' ? 'auto' : 'manual', seconds: (x.advance && x.advance.seconds) || 0 } })) }
      : { id: '', name: '', steps: [newStep(1)] };
    render();
  }
  const scopeKey = sc => sc.kind + '|' + (sc.group || '') + '|' + (sc.layer || '');
  // catalogFor: the tests a step's scope offers (GET /api/tests?kind=…, which
  // changes nothing), cached per scope while the editor is open.
  function catalogFor(sc) {
    const k = scopeKey(sc);
    if (st.catalogs[k]) return st.catalogs[k];
    st.catalogs[k] = { loading: true, list: [] };
    Api.getTests({ kind: sc.kind, group: sc.group, layer: sc.layer })
      .then(res => { st.catalogs[k] = { loading: false, list: res.available || [] }; if (st.editor) render(); })
      .catch(e => { st.catalogs[k] = { loading: false, list: [], error: e.message }; if (st.editor) render(); });
    return st.catalogs[k];
  }

  function renderEditor() {
    const ed = st.editor;
    const lim = v().limits || {};
    const name = h('input', { class: 'b5-input', type: 'text', maxlength: lim.nameLength || 80, 'data-seq-name': '', 'aria-label': 'Sequence name' });
    name.value = ed.name;
    name.addEventListener('input', () => { ed.name = name.value; });
    const steps = h('ol', { class: 'b5-ct-edsteps' });
    ed.steps.forEach((s, i) => steps.appendChild(stepEditor(s, i)));
    return h('section', { class: 'b5-ct-section b5-ct-editor', 'aria-label': 'Sequence editor', 'data-seq-editor': '' },
      h('h3', { class: 'b5-group__head', text: ed.id ? 'Edit sequence' : 'New sequence' }),
      h('label', { class: 'b5-field' }, h('span', { class: 'b5-field__label', text: 'Sequence name' }), name),
      steps,
      h('div', { class: 'b5-row' },
        btn('Add step', { 'data-step-add': '' }, () => { ed.steps.push(newStep(ed.steps.length + 1)); render(); }, { disabled: ed.steps.length >= (lim.steps || 64) })),
      h('div', { class: 'b5-row b5-con-dialog__actions' },
        btn('Cancel', { 'data-seq-cancel': '' }, () => { st.editor = null; st.catalogs = {}; render(); }),
        btn('Save sequence', { 'data-seq-save': '' }, () => saveEditor(), { cls: 'b5-btn--primary' })));
  }

  function stepEditor(s, i) {
    const ed = st.editor;
    const lim = v().limits || {};
    const name = h('input', { class: 'b5-input', type: 'text', maxlength: lim.nameLength || 80, 'data-step-name': i, 'aria-label': 'Step ' + (i + 1) + ' name' });
    name.value = s.name;
    name.addEventListener('input', () => { s.name = name.value; });
    const kind = select({ 'data-step-scope': i, 'aria-label': 'Step ' + (i + 1) + ' scope' }, SCOPES, s.scope.kind, val => {
      s.scope = { kind: val, group: val === 'group' ? ((storedGroups()[0] || {}).id || '') : '', layer: val === 'layer' ? ((layerList()[0] || {}).id || '') : '' };
      render();
    });
    const scopeRow = h('div', { class: 'b5-row' }, h('label', { class: 'b5-con-inline' }, h('span', { text: 'Scope ' }), kind));
    if (s.scope.kind === 'group') scopeRow.appendChild(select({ 'data-step-group': i, 'aria-label': 'Group' }, storedGroups().map(g => [g.id, g.name]), s.scope.group, val => { s.scope.group = val; render(); }));
    if (s.scope.kind === 'layer') scopeRow.appendChild(select({ 'data-step-layer': i, 'aria-label': 'Layer' }, layerList().map(l => [l.id, l.name]), s.scope.layer, val => { s.scope.layer = val; render(); }));
    const fade = select({ 'data-step-fade': i, 'aria-label': 'Step ' + (i + 1) + ' fade' }, [['', 'Keep the fade in effect']].concat(FADES.map(ms => [ms, fadeWord(ms)])), s.fadeMs === null ? '' : s.fadeMs,
      val => { s.fadeMs = val === '' ? null : Number(val); });
    const secs = h('input', { class: 'b5-input b5-numinput', type: 'number', min: lim.minAutoSeconds || 0.5, max: lim.maxAutoSeconds || 3600, step: 0.5, 'data-step-seconds': i, 'aria-label': 'Seconds before the next step' });
    secs.value = String(s.advance.seconds || 10);
    secs.addEventListener('input', () => { s.advance.seconds = Number(secs.value); });
    const adv = select({ 'data-step-advance': i, 'aria-label': 'Step ' + (i + 1) + ' advance' }, [['manual', 'By hand (Next)'], ['auto', 'Automatically after']], s.advance.mode, val => {
      s.advance = { mode: val, seconds: val === 'auto' ? (Number(secs.value) || 10) : 0 };
      render();
    });
    // the step's tests
    const cat = catalogFor(s.scope);
    const chips = h('ul', { class: 'b5-ct-chips' });
    s.tests.forEach((t, j) => {
      const a = cat.list.find(x => x.id === testId(t)) || t;
      chips.appendChild(h('li', { class: 'b5-ct-chip', 'data-step-test': i + ':' + testId(t) }, labelOf(a).text,
        btn('Remove', { 'data-step-test-remove': i + ':' + j, 'aria-label': 'Remove ' + labelOf(a).text + ' from step ' + (i + 1) }, () => { s.tests.splice(j, 1); render(); }, { cls: 'b5-btn--sm' })));
    });
    const have = new Set(s.tests.map(testId));
    const addable = cat.list.filter(a => !have.has(a.id));
    const pick = select({ 'data-step-test-pick': i, 'aria-label': 'Test to add to step ' + (i + 1) },
      addable.length ? addable.map(a => [a.id, labelOf(a).text]) : [['', cat.loading ? 'Loading the tests…' : 'No more tests for this scope']], addable.length ? addable[0].id : '');
    pick.disabled = !addable.length;
    const testsBox = h('div', { class: 'b5-ct-steptests' },
      h('span', { class: 'b5-field__label', text: 'Tests in this step · ' + s.tests.length }),
      s.tests.length ? chips : h('p', { class: 'b5-caption', text: 'No tests yet: add at least one.' }),
      h('div', { class: 'b5-row' }, pick,
        btn('Add test', { 'data-step-test-add': i }, () => {
          const a = addable.find(x => x.id === pick.value);
          if (a) { s.tests.push(specForAvailable(a)); render(); }
        }, { cls: 'b5-btn--sm', disabled: !addable.length }),
        btn('Use the tests on now', { 'data-step-copy': i }, () => { s.tests = adHoc(); render(); }, { cls: 'b5-btn--sm', disabled: !adHoc().length })));
    return h('li', { class: 'b5-ct-edstep', 'data-step-editor': i },
      h('div', { class: 'b5-row b5-ct-edstep__head' },
        h('strong', { text: 'Step ' + (i + 1) }),
        btn('Up', { 'data-step-up': i }, () => { [ed.steps[i - 1], ed.steps[i]] = [ed.steps[i], ed.steps[i - 1]]; render(); }, { cls: 'b5-btn--sm', disabled: i === 0 }),
        btn('Down', { 'data-step-down': i }, () => { [ed.steps[i + 1], ed.steps[i]] = [ed.steps[i], ed.steps[i + 1]]; render(); }, { cls: 'b5-btn--sm', disabled: i === ed.steps.length - 1 }),
        btn('Remove step', { 'data-step-remove': i }, () => { ed.steps.splice(i, 1); render(); }, { cls: 'b5-btn--sm b5-btn--danger', disabled: ed.steps.length === 1 })),
      h('label', { class: 'b5-field' }, h('span', { class: 'b5-field__label', text: 'Step name' }), name),
      scopeRow,
      testsBox,
      h('div', { class: 'b5-row' }, h('label', { class: 'b5-con-inline' }, h('span', { text: 'Fade ' }), fade)),
      h('div', { class: 'b5-row' }, h('label', { class: 'b5-con-inline' }, h('span', { text: 'Advance ' }), adv),
        s.advance.mode === 'auto' ? h('label', { class: 'b5-con-inline' }, secs, h('span', { text: ' seconds' })) : null));
  }

  async function saveEditor() {
    const ed = st.editor;
    const steps = ed.steps.map(s => ({
      name: s.name.trim(),
      tests: s.tests.map(specOf),
      scope: { kind: s.scope.kind, group: s.scope.kind === 'group' ? s.scope.group : '', layer: s.scope.kind === 'layer' ? s.scope.layer : '' },
      fadeMs: s.fadeMs,
      advance: s.advance.mode === 'auto' ? { mode: 'auto', seconds: Number(s.advance.seconds) } : { mode: 'manual', seconds: 0 },
    }));
    const body = { name: ed.name.trim(), steps };
    if (ed.id) body.id = ed.id;
    const res = await act('sequence-save', body, 'Saved the sequence ' + body.name + '.');
    if (res) { st.editor = null; st.catalogs = {}; render(); }
  }

  // --- live sync -------------------------------------------------------------------------
  function onTests(msg) {
    if (msg.revision !== st.known) refresh();
  }
  function init() {
    if (typeof Live !== 'undefined') {
      Live.on('tests', onTests);
      Live.on('connected', () => { if (els.root) refresh(); });
      // A layer renamed elsewhere renames it here too.
      Live.on('layout', () => { if (els.root) render(); });
    }
    // The catalog of a "selection" scope follows the Console selection even
    // while no test is on (the server only announces a tests change once
    // tests are pushed), so a new selection re-reads it.
    if (typeof ProgrammerSync !== 'undefined') {
      ProgrammerSync.onChange(p => {
        const key = JSON.stringify(((p && p.selection) || []).map(x => x.entryId + '/' + (x.cell || ''))) + '|' + JSON.stringify(((p && p.storedGroups) || []).map(g => g.id + g.name));
        if (key === st.selKey) return;
        st.selKey = key;
        if (els.root) refresh();
      });
    }
    window.addEventListener('b5-show-changed', () => { st.editor = null; st.catalogs = {}; st.expanded = {}; if (els.root) refresh(); });
  }
  init();
  return { mount, refresh, paramsFor, _state: st };
})();
