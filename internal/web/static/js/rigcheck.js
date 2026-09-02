// rigcheck.js — the Rig Check "Function check" screen: stackable attribute
// test patterns, driven entirely by the server's test-pattern engine
// (internal/patch/testpattern.go, HTTP surface in internal/web/patch.go).
//
// Extracted out of patch.js's Function Check section, which was built to the
// engine's older one-pattern-at-a-time contract and could not express what
// the owner actually asked for:
//
//   "I'd like to be able to stack tests on top of each other, so let's turn
//    each into a simple toggle button, and allow me to run multiple at once.
//    Workflow should be along the lines of: Select testing scope (universe,
//    whole rig, etc.), then pick which tests I want, then run. Once running,
//    I should still be able to toggle tests live — the start button just
//    allows output to flow, it doesn't limit other configuration. Same goes
//    for the stop button — should stop output, but not deselect any tests."
//
// That is exactly the engine's own model, so this screen is a thin, literal
// rendering of it. Three rules hold the whole file together:
//
//  1. THE SERVER SNAPSHOT IS THE ONLY STATE. `snap` below is the last
//     response from ANY pattern endpoint — every one of them (select, tests,
//     scope, isolate, output, and the plain GET) answers with the identical
//     full status object. Nothing here keeps a local mirror of which tests
//     are on, what their parameters are, or whether output is flowing; every
//     mutator does `snap = await Api.…(); render();`. This is the fix for
//     the owner's "it doesn't reflect in the UI unless I fire a test and end
//     it, then the UI fixes itself" — the old code kept its own fcParams /
//     fcKindByGroup selection mirror and only reconciled with the server on
//     start/stop, so the two drifted. There is deliberately no optimistic
//     update anywhere in this file: a toggle press paints ONLY what came
//     back.
//
//  2. NOTHING IS SELECTED BY US, EVER. The old screen pre-picked one kind
//     per group (fcKindByGroup's initializer), which is what the owner saw
//     as "one test for each attribute is toggled on from the start". This
//     file sends a select ONLY from a press on that test's own toggle. The
//     first render after entering the screen sets the SCOPE (which selects
//     nothing and moves nothing) and paints whatever the engine reports —
//     on a fresh server, an empty selection with output off.
//
//  3. THE TEST GRID COMES FROM `available[]`. That array is the engine's
//     enumeration of the tests the fixtures CURRENTLY IN SCOPE actually
//     support, already in canonical group order, and it changes when the
//     scope changes. No test list is hardcoded here: KIND_LABEL/TARGET_LABEL
//     below are presentation only (a human phrase for a wire id), and a kind
//     this file has no phrase for still renders — labelled with whatever the
//     server called it and marked as such.
//
// Selection and output being orthogonal is load-bearing in the layout too:
// the toggles are never disabled while output flows, and Stop never touches
// a toggle. Start/Stop live together in a sticky bottom bar so Stop is under
// the thumb at all times and does not move as the grid above it reflows.
//
// Tablet-first: this is used standing up, in the dark, on a tablet held at
// arm's length under a truss. Toggle tiles are big (>=88px tall, full 44px+
// touch minimum on every control), state is carried by an icon AND the words
// ON/OFF AND a border AND a background — never by colour alone — and the
// primary controls sit at the bottom edge where a thumb reaches.
//
// Apply-to-confirm: the project's standing contract, with the signed-off
// exception the owner asked for. Test toggles and the parameter faders are
// direct-action (no Apply button); the one confirm() left is on Start,
// because that is the press that puts real light and real movement into a
// room full of people.
const RigCheckPanel = (() => {
  // host: supplied by patch.js — the screen that owns the tab this panel
  // renders inside. Kept to the three things this panel genuinely cannot
  // know: the patch's entries, where to put the shared status line, and how
  // to send the user to the Entries tab.
  let host = { getEntries: () => [], setStatus: () => {}, goToEntries: () => {} };

  // snap: the last full status snapshot from the server. THE source of truth
  // for selection, parameters, coverage, contention, base state and output
  // state. null only before the first fetch. See rule 1 in the file comment.
  let snap = null;

  // scope: the only genuinely client-side state here, and it is an INPUT to
  // the server rather than a mirror of anything it reports — the status
  // object echoes a resolved fixture count (totalScope) but not the scope
  // expression that produced it, so the expression lives here and is re-sent
  // whenever it changes. Persisted per session like the other Rig Check
  // scope pickers in patch.js.
  let scopeKind = sessionStorage.getItem('benny512.rigcheck.scopeKind') || 'all';
  let scopeUniverse = 0;
  let scopePosition = '';
  let scopeSelection = {}; // entryId -> bool, for scopeKind 'selection'

  // expanded: which test tiles have their parameter panel open. Pure
  // presentation, deliberately not on the server.
  let expanded = {};
  // busy: testId -> true while that tile's own request is in flight, so a
  // double-tap in the dark cannot fire two conflicting selects.
  let busy = {};
  let errMsg = '';
  let watchdogFired = false;
  let containerEl = null;
  let pollTimer = null;
  let starting = false;
  // mounted: true between attach() and leave(). Guards enter()'s scope push
  // so it happens once per visit to the sub-tab rather than on every
  // re-render of the owning screen.
  let mounted = false;

  // ---- taxonomy / presentation ------------------------------------------

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

  // ---- server calls (every one re-renders from the returned snapshot) ----

  function scopeBody() {
    const b = { scopeKind };
    if (scopeKind === 'universe') b.universe = scopeUniverse;
    if (scopeKind === 'position') b.position = scopePosition;
    if (scopeKind === 'selection') b.entryIds = Object.keys(scopeSelection).filter(id => scopeSelection[id]);
    return b;
  }

  // apply: the single funnel every mutator goes through. It exists so that
  // "re-render from the returned snapshot, never from local state" is one
  // line that cannot be forgotten at a call site — and so an error leaves
  // the LAST GOOD snapshot on screen instead of a half-applied guess.
  async function apply(fn) {
    errMsg = '';
    try {
      snap = await fn();
      watchdogFired = false;
      syncPolling();
      return true;
    } catch (e) {
      errMsg = e && e.message ? e.message : String(e);
      // Re-read the truth: a rejected mutation means the server's state is
      // whatever it was, and the screen must show that, not the attempt.
      try { snap = await Api.getPattern(); } catch (e2) { /* keep last snapshot */ }
      return false;
    } finally {
      render();
    }
  }

  // setScope: a scope that resolves to no fixtures is a 422 and the server
  // keeps the scope it had. Say that plainly — otherwise the fixture count
  // on screen (still the OLD scope's, because that is what the server
  // reports) silently disagrees with the universe in the box.
  async function setScope() {
    sessionStorage.setItem('benny512.rigcheck.scopeKind', scopeKind);
    const ok = await apply(() => Api.patternSetScope(scopeBody()));
    if (!ok) {
      errMsg = 'Nothing is patched there, so the scope was left as it was. ' + errMsg;
      render();
    }
  }

  function specForAvailable(a) {
    // Parameters a newly-selected test starts with. These are INPUTS chosen
    // here (the server has no opinion about a test it has never been told
    // about) — everything after this first press is read back out of
    // snap.tests[]. rateHz 0 asks the engine for its own per-kind default
    // rather than this file guessing one; max 255 matters because min==max
    // is a span of zero, i.e. a test that visibly does nothing.
    const p = paramsFor(a.kind);
    const spec = {
      kind: a.kind, target: a.target || '', rateHz: 0,
      min: 0, max: 255, value: 128, on: true,
      direction: p.direction ? 'cw' : '',
      waveform: 'sine', offsetMin: 0, offsetMax: 0,
    };
    return spec;
  }

  function specForTest(t, overrides) {
    // Re-selecting a selected test replaces its parameters wholesale, so a
    // parameter edit resends the test's CURRENT server-reported spec with
    // one field changed — never a locally accumulated copy.
    const spec = {
      kind: t.kind, target: t.target || '', rateHz: t.rateHz,
      min: t.min, max: t.max, value: t.value, on: t.on,
      direction: t.direction || '', waveform: t.waveform || 'sine',
      offsetMin: t.offsetMin || 0, offsetMax: t.offsetMax || 0,
    };
    return Object.assign(spec, overrides || {});
  }

  async function toggleTest(a, on) {
    if (busy[a.id]) return;
    busy[a.id] = true;
    render();
    const spec = on ? specForAvailable(a) : specForTest(selectedById()[a.id] || { kind: a.kind, target: a.target, rateHz: 0, min: 0, max: 255, value: 0, on: false, waveform: 'sine' });
    await apply(() => Api.patternSelect(spec, on));
    delete busy[a.id];
    render();
  }

  async function editParam(t, overrides) {
    await apply(() => Api.patternSelect(specForTest(t, overrides), true));
  }

  async function setOutput(on) {
    await apply(() => Api.patternSetOutput(on));
  }

  // ---- watchdog heartbeat -------------------------------------------------
  // GET .../rigcheck/pattern is not only a read: it is the client-liveness
  // touch the engine's watchdog counts. More than ~5s without one while
  // output flows and the server blacks out and stops on its own, reporting
  // lastEndReason "watchdog". So this poll is a SAFETY REQUIREMENT, not a
  // convenience — it must run for as long as output is enabled, and 2s
  // leaves a wide margin. It doubles as how the screen learns about a stop
  // that happened elsewhere (another tab, the watchdog, a leave-screen).
  const HEARTBEAT_MS = 2000;

  function syncPolling() {
    if (snap && snap.outputEnabled) startPolling();
    else stopPolling();
  }

  function startPolling() {
    if (pollTimer) return;
    pollTimer = setInterval(async () => {
      if (!containerEl || !document.body.contains(containerEl)) { stopPolling(); return; }
      let next;
      try {
        next = await Api.getPattern();
      } catch (e) {
        return; // transient — the next tick retries; the watchdog is the backstop
      }
      const before = snap;
      snap = next;
      if (!snap.outputEnabled) {
        stopPolling();
        if (snap.lastEndReason === 'watchdog') {
          watchdogFired = true;
          host.setStatus('Output stopped by the safety watchdog: this page went more than 5s without reaching the server. The rig is blacked out. Your test selection is untouched — press START to resume.');
        }
        render();
        return;
      }
      // Output still flowing: refresh only the live numbers unless the shape
      // of the screen actually changed, so a fader being dragged or a tile
      // being read is never yanked out from under a thumb.
      if (shapeSignature(before) !== shapeSignature(snap)) render();
      else refreshLive();
    }, HEARTBEAT_MS);
  }

  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  // shapeSignature: everything a full re-render would change structurally.
  // Live counters (elapsedMs, applied counts) are deliberately absent — they
  // are patched in place by refreshLive.
  function shapeSignature(s) {
    if (!s) return '';
    return [
      s.outputEnabled ? '1' : '0',
      s.baseState && s.baseState.isolate ? '1' : '0',
      (s.available || []).map(a => a.id + ':' + a.fixtureCount).join(','),
      (s.tests || []).map(t => [t.id, t.rateHz, t.min, t.max, t.value, t.on, t.waveform, t.offsetMin, t.offsetMax, t.direction].join('|')).join(','),
      (s.contested || []).length,
    ].join('~');
  }

  function refreshLive() {
    if (!containerEl) return;
    const el = containerEl.querySelector('#rcpElapsed');
    if (el) el.textContent = ((snap.elapsedMs || 0) / 1000).toFixed(1) + 's';
    (snap.tests || []).forEach(t => {
      const cov = containerEl.querySelector(`[data-rcp-coverage="${cssEscape(t.id)}"]`);
      if (cov) cov.textContent = coverageText(t);
    });
  }

  function cssEscape(s) { return String(s).replace(/["\\]/g, '\\$&'); }

  // ---- lifecycle ----------------------------------------------------------

  function init(h) { host = Object.assign(host, h || {}); }

  // attach: called on every render of the owning screen while the Function
  // check sub-tab is visible. The first call of a visit does the scope push
  // (enter); later ones just repaint from the snapshot already in hand.
  function attach(container) {
    containerEl = container;
    if (!mounted) { mounted = true; enter(container); return; }
    render(container);
  }

  // enter: called when the Function check sub-tab becomes visible. Pushes
  // the current scope (which selects nothing and outputs nothing — it only
  // tells the engine which fixtures the available[] enumeration should be
  // computed over) and paints whatever comes back.
  async function enter(container) {
    containerEl = container;
    errMsg = '';
    try {
      snap = await Api.patternSetScope(scopeBody());
    } catch (e) {
      // A scope that resolves to nothing is a 422 — legitimate and worth
      // saying plainly rather than leaving a blank screen.
      errMsg = e && e.message ? e.message : String(e);
      try { snap = await Api.getPattern(); } catch (e2) { /* leave snap as-is */ }
    }
    syncPolling();
    render();
  }

  function leave() { mounted = false; stopPolling(); containerEl = null; }

  async function refreshStatus() {
    try { snap = await Api.getPattern(); } catch (e) { /* best effort */ }
    syncPolling();
    return snap;
  }

  function outputEnabled() { return !!(snap && snap.outputEnabled); }
  function selectedCount() { return snap ? (snap.tests || []).length : 0; }

  async function stopOutput() { await apply(() => Api.patternSetOutput(false)); }

  function selectedById() {
    const m = {};
    if (snap) (snap.tests || []).forEach(t => { m[t.id] = t; });
    return m;
  }

  function coverageText(t) {
    const total = t.totalScope || 0;
    return `runs on ${t.appliedCount} of ${total} fixture${total === 1 ? '' : 's'}`;
  }

  // ---- render -------------------------------------------------------------

  function render(container) {
    if (container) containerEl = container;
    const el = containerEl;
    if (!el) return;
    if (!snap) {
      el.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading rig check&hellip;</span>`;
      return;
    }
    const sel = selectedById();
    el.innerHTML = `
      ${renderErrors()}
      ${renderWarnings()}
      ${renderScope()}
      ${renderIsolate()}
      ${renderGrid(sel)}
      ${renderOutputBar()}
    `;
    wire();
  }

  function renderErrors() {
    if (!errMsg) return '';
    return `
      <div class="b5-alert b5-alert--danger b5-rcp-alert" role="alert">
        ${UI.icon('status-error')}
        <div>
          <p class="b5-alert__title">That didn't go through</p>
          <p class="b5-alert__body">${escapeHtml(errMsg)}</p>
        </div>
      </div>`;
  }

  // renderWarnings: the three things that make a tech think "this app is
  // broken" when in fact the rig or the fixture file is telling them
  // something. All three are surfaced up top, in the owner's language.
  function renderWarnings() {
    const bs = snap.baseState || {};
    const out = [];

    if (watchdogFired) {
      out.push(`
        <div class="b5-alert b5-alert--caution b5-rcp-alert" role="status">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Output was stopped automatically</p>
            <p class="b5-alert__body">This page went more than 5 seconds without reaching the server, so the safety watchdog blacked the rig out. Nothing was deselected — press START to pick up where you left off.</p>
          </div>
        </div>`);
    }

    // contested[]: two selected tests writing the same DMX slot. The last in
    // canonical order wins, which is the single most common reason a tech
    // says "I turned this test on and nothing happened".
    const contested = snap.contested || [];
    if (contested.length) {
      const byPair = {};
      contested.forEach(c => {
        const key = (c.tests || []).join(' vs ');
        byPair[key] = (byPair[key] || 0) + 1;
      });
      const lines = Object.keys(byPair).map(k => {
        const names = k.split(' vs ');
        const winner = names[names.length - 1];
        return `<li>${escapeHtml(names.map(idLabel).join(' and '))} both drive ${byPair[k]} channel${byPair[k] === 1 ? '' : 's'} &mdash; <strong>${escapeHtml(idLabel(winner))}</strong> wins, the other has no effect there.</li>`;
      });
      out.push(`
        <div class="b5-alert b5-alert--caution b5-rcp-alert" role="status">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Two tests are fighting over the same channels</p>
            <ul class="b5-rcp-list">${lines.join('')}</ul>
            <p class="b5-alert__body">Turn one of them off to see the other.</p>
          </div>
        </div>`);
    }

    // shutterUnknownEntries: these fixtures will not emit light no matter
    // what test runs, and it is not the test's fault — the fixture file
    // never says which value opens the shutter.
    const su = bs.shutterUnknownEntries || [];
    if (su.length) {
      out.push(`
        <div class="b5-alert b5-alert--caution b5-rcp-alert" role="status">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">${su.length} fixture${su.length === 1 ? '' : 's'} will not make light</p>
            <p class="b5-alert__body">Their profile never says which value opens the shutter, so it is left at 0. The test itself is fine &mdash; ${su.length === 1 ? 'this fixture' : 'these fixtures'} just won't light up:</p>
            <ul class="b5-rcp-list">${su.map(id => `<li>${escapeHtml(entryLabel(id))}</li>`).join('')}</ul>
          </div>
        </div>`);
    }

    // defaultsUnknownCount is worth saying but is not urgent — it rides as
    // a quiet line under the scope count (renderScope) rather than as a
    // third alert box competing with the two above it for the top of a
    // 768px-tall landscape tablet screen.

    return out.join('');
  }

  function idLabel(id) {
    const a = (snap.available || []).find(x => x.id === id);
    if (a) return testLabel(a).text;
    return id;
  }

  function entryLabel(entryId) {
    const e = (host.getEntries() || []).find(x => x.id === entryId);
    if (!e) return entryId;
    const name = e.name || e.fixtureType || e.id;
    return `${name} (U${UI.formatUniverse(e.universe)}/${e.startAddress})`;
  }

  // ---- scope --------------------------------------------------------------

  function renderScope() {
    const entries = host.getEntries() || [];
    const positions = Array.from(new Set(entries.map(e => e.position).filter(Boolean))).sort();
    const kinds = [
      { id: 'all', label: 'Whole rig' },
      { id: 'universe', label: 'One universe' },
      { id: 'position', label: 'One position' },
      { id: 'selection', label: 'Pick fixtures' },
    ];
    const ua = UI.universeInputAttrs();
    let value = '';
    if (scopeKind === 'universe') {
      value = `
        <div class="b5-rcp-scope__value">
          <label class="b5-field__label" for="rcpScopeUniverse">Universe <span class="b5-text-muted b5-text-xs">(${escapeHtml(UI.universeBaseLabel())})</span></label>
          <input type="number" id="rcpScopeUniverse" class="b5-input b5-numinput" min="${ua.min}" max="${ua.max}" value="${UI.formatUniverse(scopeUniverse)}">
        </div>`;
    } else if (scopeKind === 'position') {
      if (!scopePosition && positions.length) scopePosition = positions[0];
      value = `
        <div class="b5-rcp-scope__value">
          <label class="b5-field__label" for="rcpScopePosition">Position</label>
          <select id="rcpScopePosition" class="b5-select">
            ${positions.length ? positions.map(p => `<option value="${escapeHtml(p)}" ${p === scopePosition ? 'selected' : ''}>${escapeHtml(p)}</option>`).join('') : '<option value="">(no positions patched)</option>'}
          </select>
        </div>`;
    } else if (scopeKind === 'selection') {
      value = `
        <div class="b5-rcp-scope__value b5-picklist">
          ${entries.length ? entries.map(e => `
            <label class="b5-pickrow">
              <input type="checkbox" data-rcp-pick="${escapeHtml(e.id)}" ${scopeSelection[e.id] ? 'checked' : ''}>
              <span>${escapeHtml(e.name || e.fixtureType || e.id)} <span class="b5-text-muted b5-text-xs">U${UI.formatUniverse(e.universe)}/${e.startAddress}</span></span>
            </label>`).join('') : '<span class="b5-text-muted b5-text-sm">no entries in this patch</span>'}
        </div>`;
    }

    const total = snap.totalScope || 0;
    const unknownDefaults = (snap.baseState && snap.baseState.defaultsUnknownCount) || 0;
    return `
      <section class="b5-step-section" aria-labelledby="rcpScopeHead">
        <h2 class="b5-step-section__head" id="rcpScopeHead"><span class="b5-step-num">1</span> Testing scope</h2>
        <div class="b5-segmented" role="group" aria-label="Testing scope">
          ${kinds.map(k => `
            <button type="button" class="b5-seg ${scopeKind === k.id ? 'is-on' : ''}" data-rcp-scope="${k.id}" aria-pressed="${scopeKind === k.id}">${escapeHtml(k.label)}</button>
          `).join('')}
        </div>
        ${value}
        <p class="b5-rcp-scope__count">${total} fixture${total === 1 ? '' : 's'} in scope &middot; ${(snap.available || []).length} test${(snap.available || []).length === 1 ? '' : 's'} available</p>
        ${unknownDefaults ? `<p class="b5-rcp-scope__note">${unknownDefaults} channel${unknownDefaults === 1 ? ' is' : 's are'} sitting at 0 because the fixture file gave no resting value for ${unknownDefaults === 1 ? 'it' : 'them'}. Nothing is guessed.</p>` : ''}
      </section>`;
  }

  // ---- isolate ------------------------------------------------------------

  function renderIsolate() {
    const on = !!(snap.baseState && snap.baseState.isolate);
    return `
      <section class="b5-step-section">
        <label class="b5-choicecard b5-choicecard--caution ${on ? 'is-on' : ''}">
          <input type="checkbox" id="rcpIsolate" ${on ? 'checked' : ''}>
          <span class="b5-choicecard__box" aria-hidden="true">${on ? UI.icon('status-ok') : ''}</span>
          <span>
            <span class="b5-choicecard__title">Isolate: drive only the tested channel &mdash; ${on ? 'ON' : 'OFF'}</span>
            <span class="b5-choicecard__body">Everything else is held at zero, so you can prove which channel does what. The fixture will probably make no visible light while this is on.</span>
          </span>
        </label>
      </section>`;
  }

  // ---- test grid ----------------------------------------------------------

  function renderGrid(sel) {
    const available = snap.available || [];
    if (!available.length) {
      return `
        <section class="b5-step-section" aria-labelledby="rcpTestsHead">
          <h2 class="b5-step-section__head" id="rcpTestsHead"><span class="b5-step-num">2</span> Tests</h2>
          <div class="b5-empty">
            ${UI.icon('status-pending')}
            <span class="b5-empty__title">No tests available for this scope</span>
            <span class="b5-empty__body">The tests offered here come from what the fixtures in scope actually say they can do. Widen the scope, or give these fixtures a GDTF profile on the Entries tab.</span>
            <button id="rcpGoEntries" class="b5-btn" style="margin-top:var(--b5-space-3)">Go to Entries</button>
          </div>
        </section>`;
    }
    const byGroup = {};
    available.forEach(a => { (byGroup[a.group] = byGroup[a.group] || []).push(a); });
    const known = {};
    GROUPS.forEach(g => { known[g.id] = true; });
    const order = GROUPS.filter(g => byGroup[g.id]).concat(
      Object.keys(byGroup).filter(k => !known[k]).map(k => ({ id: k, label: k })));

    return `
      <section class="b5-step-section" aria-labelledby="rcpTestsHead">
        <h2 class="b5-step-section__head" id="rcpTestsHead"><span class="b5-step-num">2</span> Tests <span class="b5-step-section__note">${(snap.tests || []).length} on</span></h2>
        ${order.map(g => `
          <div class="b5-group">
            <h3 class="b5-group__head">${escapeHtml(g.label)}</h3>
            <div class="b5-tilegrid">
              ${byGroup[g.id].map(a => renderTile(a, sel[a.id])).join('')}
            </div>
          </div>
        `).join('')}
      </section>`;
  }

  function renderTile(a, t) {
    const on = !!t;
    const lbl = testLabel(a);
    const open = !!expanded[a.id];
    const isBusy = !!busy[a.id];
    const coverage = on
      ? coverageText(t)
      : `${a.fixtureCount} of ${snap.totalScope || 0} fixture${(snap.totalScope || 0) === 1 ? '' : 's'} have this`;
    const notes = [];
    if (on && t.skippedCount) notes.push(`${t.skippedCount} skipped`);
    if (on && t.inferredCount) notes.push(`${t.inferredCount} RDM-inferred`);
    if (on && t.missingDetailCount) notes.push(`${t.missingDetailCount} no slot data`);
    return `
      <div class="b5-tile ${on ? 'is-on' : ''}" data-rcp-tile="${escapeHtml(a.id)}">
        <button type="button" class="b5-tile__toggle" data-rcp-toggle="${escapeHtml(a.id)}" aria-pressed="${on}" ${isBusy ? 'disabled' : ''}>
          <span class="b5-pill b5-pill--sm${on ? ' b5-pill--accent b5-pill--solid' : ''}">${isBusy ? UI.spinner() : (on ? UI.icon('status-ok') : '')}<span class="b5-tile__stateword">${on ? 'ON' : 'OFF'}</span></span>
          <span class="b5-tile__label">${escapeHtml(lbl.text)}${lbl.fromFile ? ' <span class="b5-fromfile" title="This name is repeated from the fixture file, not written by this app">name from file</span>' : ''}</span>
          <span class="b5-caption" data-rcp-coverage="${escapeHtml(a.id)}">${escapeHtml(coverage)}</span>
          ${notes.length ? `<span class="b5-tile__notes">${escapeHtml(notes.join(' · '))}</span>` : ''}
        </button>
        <button type="button" class="b5-tile__more" data-rcp-more="${escapeHtml(a.id)}" aria-expanded="${open}" aria-label="Settings for ${escapeHtml(lbl.text)}">${UI.icon(open ? 'chevron-collapse' : 'chevron-expand')}<span>Settings</span></button>
        ${open ? `<div class="b5-tile__panel">${on ? renderParams(t) : `<p class="b5-text-sm b5-text-muted">Turn this test ON to set its rate, levels, waveform and phase. Turning it on does not move anything &mdash; only START lets output flow.</p>`}</div>` : ''}
      </div>`;
  }

  function renderParams(t) {
    const p = paramsFor(t.kind);
    const stat = isStatic(t.kind);
    const rows = [];
    if (p.on) {
      rows.push(`
        <div class="b5-param">
          <span class="b5-param__label">State</span>
          <div class="b5-segmented b5-segmented--sm" role="group" aria-label="Dimmer state">
            <button type="button" class="b5-seg ${t.on ? 'is-on' : ''}" data-rcp-on="${escapeHtml(t.id)}:1" aria-pressed="${t.on}">On (max)</button>
            <button type="button" class="b5-seg ${!t.on ? 'is-on' : ''}" data-rcp-on="${escapeHtml(t.id)}:0" aria-pressed="${!t.on}">Off (min)</button>
          </div>
        </div>`);
    }
    if (p.value) rows.push(slider(t.id, 'value', 'Value', t.value, 0, 255, 1));
    if (p.rate) rows.push(slider(t.id, 'rateHz', 'Rate', t.rateHz, 0.05, 5, 0.05, ' Hz'));
    if (p.range) {
      rows.push(slider(t.id, 'min', 'Min level', t.min, 0, 255, 1));
      rows.push(slider(t.id, 'max', 'Max level', t.max, 0, 255, 1));
    }
    if (p.direction) {
      rows.push(`
        <div class="b5-param">
          <span class="b5-param__label">Direction</span>
          <div class="b5-segmented b5-segmented--sm" role="group" aria-label="Direction">
            <button type="button" class="b5-seg ${t.direction !== 'ccw' ? 'is-on' : ''}" data-rcp-dir="${escapeHtml(t.id)}:cw" aria-pressed="${t.direction !== 'ccw'}">Clockwise</button>
            <button type="button" class="b5-seg ${t.direction === 'ccw' ? 'is-on' : ''}" data-rcp-dir="${escapeHtml(t.id)}:ccw" aria-pressed="${t.direction === 'ccw'}">Counter-cw</button>
          </div>
        </div>`);
    }
    if (!stat) {
      // Waveform is a property of every continuous test, not a separate
      // kind — so it is a switch on the test, never a second tile.
      rows.push(`
        <div class="b5-param">
          <span class="b5-param__label">Waveform</span>
          <div class="b5-segmented b5-segmented--sm" role="group" aria-label="Waveform">
            <button type="button" class="b5-seg ${t.waveform !== 'snap' ? 'is-on' : ''}" data-rcp-wave="${escapeHtml(t.id)}:sine" aria-pressed="${t.waveform !== 'snap'}">Sine (smooth)</button>
            <button type="button" class="b5-seg ${t.waveform === 'snap' ? 'is-on' : ''}" data-rcp-wave="${escapeHtml(t.id)}:snap" aria-pressed="${t.waveform === 'snap'}">Snap (hard)</button>
          </div>
        </div>`);
      // Phase spread: fixture i of n sits at min + (max-min)*i/n degrees, so
      // 0/360 across the selection is a chase that wraps seamlessly. Only
      // rendered for continuous kinds — the server 400s a non-zero phase on
      // a static one, and a control whose only outcome is an error has no
      // business on this screen.
      rows.push(`
        <div class="b5-param">
          <span class="b5-param__label">Phase spread across the fixtures</span>
          <div class="b5-rcp-phase">
            <label class="b5-rcp-phase__field">from
              <input type="number" class="b5-input b5-numinput" data-rcp-phase="${escapeHtml(t.id)}:offsetMin" value="${t.offsetMin || 0}" step="15" min="-3600" max="3600">&deg;
            </label>
            <label class="b5-rcp-phase__field">to
              <input type="number" class="b5-input b5-numinput" data-rcp-phase="${escapeHtml(t.id)}:offsetMax" value="${t.offsetMax || 0}" step="15" min="-3600" max="3600">&deg;
            </label>
            <button type="button" class="b5-btn b5-btn--sm" data-rcp-phaseset="${escapeHtml(t.id)}:0:0">Unison</button>
            <button type="button" class="b5-btn b5-btn--sm" data-rcp-phaseset="${escapeHtml(t.id)}:0:360">Chase</button>
          </div>
          <span class="b5-param__hint">0&deg; to 0&deg; is everything together. 0&deg; to 360&deg; spreads one full cycle across the fixtures &mdash; a chase that wraps.</span>
        </div>`);
    }
    return `
      <p class="b5-rcp-hint">${escapeHtml(KIND_HINT[t.kind] || '')} Changes apply immediately, even while output is running.</p>
      ${rows.join('')}`;
  }

  function slider(testId, field, label, value, min, max, step, suffix) {
    const shown = step < 1 ? Number(value).toFixed(2) : String(value);
    return `
      <div class="b5-param">
        <span class="b5-param__label">${escapeHtml(label)} <span class="b5-text-mono b5-param__value" data-rcp-out="${escapeHtml(testId)}:${field}">${escapeHtml(shown)}${suffix || ''}</span></span>
        <input type="range" class="b5-range-touch" data-rcp-slider="${escapeHtml(testId)}:${field}" data-suffix="${suffix || ''}" min="${min}" max="${max}" step="${step}" value="${value}">
      </div>`;
  }

  // ---- output bar ---------------------------------------------------------

  // renderOutputBar: START and STOP, both always present and both always in
  // the same place — the owner must never have to hunt for STOP, and a
  // control that appears/disappears moves its neighbour under a moving
  // thumb. Sticky to the bottom edge so it is thumb-reachable on a tablet
  // however far down the test grid is scrolled.
  function renderOutputBar() {
    const on = !!snap.outputEnabled;
    const n = (snap.tests || []).length;
    const elapsed = ((snap.elapsedMs || 0) / 1000).toFixed(1);
    return `
      <section class="b5-actionbar" aria-label="Output">
        <div class="b5-actionbar__status">
          <span class="b5-actionbar__title"><span class="b5-step-num">3</span> Output</span>
          <span class="b5-pill b5-pill--lg${on ? ' b5-pill--ok b5-pill--solid' : ''}">${on ? UI.icon('status-ok') : UI.icon('status-pending')}${on ? 'LIVE' : 'STOPPED'}</span>
          <span class="b5-text-sm b5-text-muted">${n} test${n === 1 ? '' : 's'} on${on ? ` &middot; <span id="rcpElapsed">${elapsed}s</span>` : ''}</span>
        </div>
        <div class="b5-actionbar__buttons">
          <button type="button" id="rcpStart" class="b5-bigbtn b5-bigbtn--go" ${on || starting ? 'disabled' : ''}>${starting ? UI.spinner() : UI.icon('status-ok')}START</button>
          <button type="button" id="rcpStop" class="b5-bigbtn b5-bigbtn--stop">${UI.icon('status-error')}STOP</button>
        </div>
      </section>`;
  }

  // ---- wiring -------------------------------------------------------------

  function wire() {
    const el = containerEl;
    if (!el) return;

    el.querySelectorAll('[data-rcp-scope]').forEach(b => b.addEventListener('click', async () => {
      const k = b.dataset.rcpScope;
      if (k === scopeKind) return;
      scopeKind = k;
      sessionStorage.setItem('benny512.rigcheck.scopeKind', scopeKind);
      // Picking a scope KIND must never fire a request that is bound to
      // fail. Land on a value that actually exists in this patch (the
      // lowest universe patched, the first position) instead of on 0 / "",
      // and for a hand-picked list simply repaint the picker and wait —
      // an empty scope is a 422 and an error banner the moment the owner
      // touches the control is not an answer to "which universe?".
      const entries = host.getEntries() || [];
      if (k === 'universe') {
        const universes = Array.from(new Set(entries.map(e => e.universe || 0))).sort((x, y) => x - y);
        if (!universes.includes(scopeUniverse)) scopeUniverse = universes.length ? universes[0] : 0;
        if (!universes.length) { errMsg = 'nothing patched yet'; render(); return; }
      }
      if (k === 'position') {
        const positions = Array.from(new Set(entries.map(e => e.position).filter(Boolean))).sort();
        if (!positions.includes(scopePosition)) scopePosition = positions[0] || '';
        if (!positions.length) { errMsg = 'no fixture in this patch has a position'; render(); return; }
      }
      if (k === 'selection' && !Object.keys(scopeSelection).some(id => scopeSelection[id])) { render(); return; }
      await setScope();
    }));

    const uni = el.querySelector('#rcpScopeUniverse');
    if (uni) uni.addEventListener('change', async (e) => { scopeUniverse = UI.parseUniverse(e.target.value); await setScope(); });

    const pos = el.querySelector('#rcpScopePosition');
    if (pos) pos.addEventListener('change', async (e) => { scopePosition = e.target.value; await setScope(); });

    el.querySelectorAll('[data-rcp-pick]').forEach(cb => cb.addEventListener('change', async (e) => {
      scopeSelection[cb.dataset.rcpPick] = e.target.checked;
      if (!Object.keys(scopeSelection).some(id => scopeSelection[id])) { errMsg = 'pick at least one fixture'; render(); return; }
      await setScope();
    }));

    const iso = el.querySelector('#rcpIsolate');
    if (iso) iso.addEventListener('change', async (e) => { await apply(() => Api.patternSetIsolate(e.target.checked)); });

    const goEntries = el.querySelector('#rcpGoEntries');
    if (goEntries) goEntries.addEventListener('click', () => host.goToEntries());

    el.querySelectorAll('[data-rcp-toggle]').forEach(b => b.addEventListener('click', () => {
      const id = b.dataset.rcpToggle;
      const a = (snap.available || []).find(x => x.id === id);
      if (!a) return;
      toggleTest(a, !selectedById()[id]);
    }));

    el.querySelectorAll('[data-rcp-more]').forEach(b => b.addEventListener('click', () => {
      const id = b.dataset.rcpMore;
      expanded[id] = !expanded[id];
      render();
    }));

    const parse = (raw) => {
      const i = raw.lastIndexOf(':');
      return [raw.slice(0, i), raw.slice(i + 1)];
    };

    el.querySelectorAll('[data-rcp-slider]').forEach(inp => {
      const [id, field] = parse(inp.dataset.rcpSlider);
      const out = el.querySelector(`[data-rcp-out="${cssEscape(id)}:${field}"]`);
      // oninput paints the readout only (the file-wide contract); the value
      // commits on change — i.e. when the thumb is released.
      inp.addEventListener('input', () => {
        if (!out) return;
        const step = Number(inp.step);
        out.textContent = (step < 1 ? Number(inp.value).toFixed(2) : inp.value) + (inp.dataset.suffix || '');
      });
      inp.addEventListener('change', () => {
        const t = selectedById()[id];
        if (!t) return;
        const v = Number(inp.value);
        editParam(t, { [field]: field === 'rateHz' ? v : Math.round(v) });
      });
    });

    el.querySelectorAll('[data-rcp-wave]').forEach(b => b.addEventListener('click', () => {
      const [id, w] = parse(b.dataset.rcpWave);
      const t = selectedById()[id];
      if (t) editParam(t, { waveform: w });
    }));
    el.querySelectorAll('[data-rcp-dir]').forEach(b => b.addEventListener('click', () => {
      const [id, d] = parse(b.dataset.rcpDir);
      const t = selectedById()[id];
      if (t) editParam(t, { direction: d });
    }));
    el.querySelectorAll('[data-rcp-on]').forEach(b => b.addEventListener('click', () => {
      const [id, v] = parse(b.dataset.rcpOn);
      const t = selectedById()[id];
      if (t) editParam(t, { on: v === '1' });
    }));
    el.querySelectorAll('[data-rcp-phase]').forEach(inp => inp.addEventListener('change', () => {
      const [id, field] = parse(inp.dataset.rcpPhase);
      const t = selectedById()[id];
      if (t) editParam(t, { [field]: Number(inp.value) || 0 });
    }));
    el.querySelectorAll('[data-rcp-phaseset]').forEach(b => b.addEventListener('click', () => {
      const raw = b.dataset.rcpPhaseset;
      const parts = raw.split(':');
      const max = parts.pop(), min = parts.pop();
      const id = parts.join(':');
      const t = selectedById()[id];
      if (t) editParam(t, { offsetMin: Number(min), offsetMax: Number(max) });
    }));

    const startBtn = el.querySelector('#rcpStart');
    if (startBtn) startBtn.addEventListener('click', onStart);
    const stopBtn = el.querySelector('#rcpStop');
    if (stopBtn) stopBtn.addEventListener('click', async () => {
      // No confirm: stopping is the safe direction, and it must be instant.
      // It stops output only — every selected test stays selected.
      await setOutput(false);
      host.setStatus('output stopped — tests still selected');
    });

  }

  // onStart: the one confirm() left on this screen. Letting output flow puts
  // real light and real movement into a room; the dialog names the scope and
  // the number of tests so the press is deliberate. Stop is never confirmed.
  async function onStart() {
    const n = (snap.tests || []).length;
    const total = snap.totalScope || 0;
    if (!n) { errMsg = 'no tests selected — turn at least one test on first'; render(); return; }
    const scopeWord = scopeKind === 'all' ? 'the whole rig'
      : scopeKind === 'universe' ? `universe ${UI.formatUniverse(scopeUniverse)}`
        : scopeKind === 'position' ? `position "${scopePosition}"`
          : `${total} picked fixture${total === 1 ? '' : 's'}`;
    if (!confirm(`Let output flow: ${n} test${n === 1 ? '' : 's'} on ${scopeWord} (${total} fixture${total === 1 ? '' : 's'}). This moves real fixtures now.`)) return;
    starting = true;
    render();
    await setOutput(true);
    starting = false;
    render();
    host.setStatus('output running');
  }

  return { init, attach, enter, leave, render, refreshStatus, outputEnabled, selectedCount, stopOutput, stopPolling };
})();
