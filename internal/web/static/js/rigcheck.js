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

  // scope is an input control, but the server echoes its accepted expression
  // on every snapshot. That matters after a rejected change or another
  // client changes the test: the controls must return to the server truth,
  // not preserve a browser-side guess. Persisted per session for a useful
  // first visit only; an accepted server scope wins after that.
  let scopeKind = sessionStorage.getItem('benny512.rigcheck.scopeKind') || 'all';
  let scopeUniverse = 0;
  let scopePosition = '';
  let scopeSelection = {}; // entryId -> bool, for scopeKind 'selection'
  let scopeFixtureType = '';
  let scopeFixtureTypeDraft = '';

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

  // ---- output protocol (Art-Net / sACN) ----------------------------------
  //
  // The pattern engine and the classic channel walk share ONE armed protocol
  // (internal/patch/rigcheckout.go's rigOutput): exactly one protocol is live
  // for a given universe at a time, by design. Before this control existed
  // the Function check simply inherited whatever a Channel-check Start last
  // armed, and said nothing about it — so the tab could be driving sACN with
  // nothing on screen saying so. It now carries the choice on POST
  // .../pattern/output and paints itself from the server's echoed
  // `protocol`, which is the only authority on what is actually on the wire.
  //
  // Apply-to-confirm, exactly as the Rig Check page does it, and for the
  // same reason: the signed-off direct-action exceptions on this screen (the
  // test toggles, the parameter faders) all ADJUST SOMETHING ALREADY LIVE by
  // feel, where an intermediate value on the way to the intended one is
  // harmless. A protocol has no intermediate value — switching it while
  // output flows terminates one stream (sACN: three zero frames and three
  // Stream_Terminated packets, ANSI E1.31-2025 6.2.6) and opens another. So a
  // press stages, and Apply commits.
  //
  // The vocabulary is UI.RC_PROTOCOLS, shared with patch.js and checked
  // against patch.NormalizeProtocol by internal/web/sacn_ui_test.go. There is
  // no protocol table in this file.
  let fcProtocolArmed = 'artnet';
  let fcProtocolDraft = 'artnet';
  let fcProtocolApplying = false;
  // fcProtocolTouched: the operator has applied a protocol HERE. Until then
  // the armed protocol simply follows the server's echo, so arriving on this
  // tab during a live sACN run shows sACN without anyone pressing anything.
  let fcProtocolTouched = false;
  // mounted: true between attach() and leave(). Guards enter()'s scope push
  // so it happens once per visit to the sub-tab rather than on every
  // re-render of the owning screen.
  let mounted = false;

  window.addEventListener('b5-show-changed', () => {
    snap = null; scopeKind = 'all'; scopeUniverse = 0; scopePosition = '';
    scopeSelection = {}; scopeFixtureType = ''; scopeFixtureTypeDraft = ''; mounted = false; expanded = {};
    fcProtocolArmed = 'artnet'; fcProtocolDraft = 'artnet'; fcProtocolApplying = false; fcProtocolTouched = false;
  });

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

  // ---- server calls (every one re-renders from the returned snapshot) ----

  function scopeBody() {
    const b = { scopeKind };
    if (scopeKind === 'universe') b.universe = scopeUniverse;
    if (scopeKind === 'position') b.position = scopePosition;
    if (scopeKind === 'selection') b.entryIds = Object.keys(scopeSelection).filter(id => scopeSelection[id]);
    if (scopeKind === 'fixtureType') b.fixtureType = scopeFixtureTypeDraft;
    return b;
  }

  function syncScopeFromSnapshot() {
    if (!snap || !snap.scopeKind) return;
    const kind = snap.scopeKind;
    if (!['all', 'universe', 'position', 'selection'].includes(kind)) return;
    scopeKind = kind;
    scopeUniverse = Number(snap.scopeUniverse || 0);
    scopePosition = snap.scopePosition || '';
    scopeSelection = {};
    (snap.scopeEntryIds || []).forEach(id => { scopeSelection[id] = true; });
    scopeFixtureType = snap.scopeFixtureType || sessionStorage.getItem('benny512.rigcheck.fixtureType') || '';
    scopeFixtureTypeDraft = scopeFixtureType;
    sessionStorage.setItem('benny512.rigcheck.scopeKind', scopeKind);
  }

  // syncProtocolFromSnapshot: adopt the server's echo. While output flows the
  // echo IS the armed protocol — there is no daylight between "what is on the
  // wire" and "what the next start sends" until something is staged here.
  function syncProtocolFromSnapshot() {
    const p = snap && snap.protocol;
    if (!UI.isProtocol(p)) return;
    if ((snap && snap.outputEnabled) || !fcProtocolTouched) {
      fcProtocolArmed = p;
      if (!fcProtocolApplying) fcProtocolDraft = p;
    }
  }

  // snapProtocolToServer: after a REFUSAL the control returns to server
  // truth — the same discipline the scope echo above follows, for the same
  // reason. A browser-side guess the server rejected must not stay on screen
  // looking armed, or the next press just repeats the refusal.
  function snapProtocolToServer() {
    const p = snap && snap.protocol;
    if (!UI.isProtocol(p)) return;
    fcProtocolTouched = false;
    fcProtocolArmed = p;
    fcProtocolDraft = p;
  }

  // apply: the single funnel every mutator goes through. It exists so that
  // "re-render from the returned snapshot, never from local state" is one
  // line that cannot be forgotten at a call site — and so an error leaves
  // the LAST GOOD snapshot on screen instead of a half-applied guess.
  async function apply(fn) {
    errMsg = '';
    try {
      snap = await fn();
      syncScopeFromSnapshot();
      syncProtocolFromSnapshot();
      watchdogFired = false;
      syncPolling();
      return true;
    } catch (e) {
      errMsg = e && e.message ? e.message : String(e);
      // Re-read the truth: a rejected mutation means the server's state is
      // whatever it was, and the screen must show that, not the attempt.
      try { snap = await Api.getPattern(); syncScopeFromSnapshot(); syncProtocolFromSnapshot(); scopeFixtureTypeDraft = scopeFixtureType; } catch (e2) { /* keep last snapshot */ }
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
    const acceptedFixtureType = scopeFixtureType || sessionStorage.getItem('benny512.rigcheck.fixtureType') || '';
    sessionStorage.setItem('benny512.rigcheck.scopeKind', scopeKind);
    const ok = await apply(() => Api.patternSetScope(scopeBody()));
    if (!ok) {
      scopeFixtureType = acceptedFixtureType;
      scopeFixtureTypeDraft = acceptedFixtureType;
      errMsg = 'Nothing is patched there, so the scope was left as it was. ' + errMsg;
      render();
    } else if (scopeKind === 'fixtureType') {
      scopeFixtureType = scopeFixtureTypeDraft;
      sessionStorage.setItem('benny512.rigcheck.fixtureType', scopeFixtureType);
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

  // setOutput: the start/stop call. Starting NAMES the armed protocol rather
  // than relying on the server's "absent means keep what is armed" default —
  // this screen has a protocol control now, so it says what it means, exactly
  // as patch.js's rigCheckStartBody does. Stopping never carries one.
  async function setOutput(on) {
    return apply(() => Api.patternSetOutput(on, on ? fcProtocolArmed : undefined));
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
      syncScopeFromSnapshot();
      syncProtocolFromSnapshot();
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
      s.protocol || '',
      s.fadeMs,
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
        snap = await Api.getPattern();
        if (!snap.scopeKind) snap = await Api.patternSetScope(scopeBody());
        syncScopeFromSnapshot();
        syncProtocolFromSnapshot();
    } catch (e) {
      // A scope that resolves to nothing is a 422 — legitimate and worth
      // saying plainly rather than leaving a blank screen.
      errMsg = e && e.message ? e.message : String(e);
      try { snap = await Api.getPattern(); syncScopeFromSnapshot(); syncProtocolFromSnapshot(); } catch (e2) { /* leave snap as-is */ }
    }
    syncPolling();
    render();
  }

  function leave() { mounted = false; stopPolling(); containerEl = null; }

  async function refreshStatus() {
    try { snap = await Api.getPattern(); syncScopeFromSnapshot(); syncProtocolFromSnapshot(); } catch (e) { /* best effort */ }
    syncPolling();
    return snap;
  }

  function outputEnabled() { return !!(snap && snap.outputEnabled); }
  function selectedCount() { return snap ? (snap.tests || []).length : 0; }

  async function stopOutput() { await setOutput(false); }

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
      ${renderProtocol()}
      ${renderFade()}
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
    return `${name} (U${UI.formatUser(e.universe)}/${e.startAddress})`;
  }

  // ---- scope --------------------------------------------------------------

  function renderScope() {
    const entries = host.getEntries() || [];
    const positions = Array.from(new Set(entries.map(e => e.position).filter(Boolean))).sort();
    const kinds = [
      { id: 'all', label: 'Whole rig' },
      { id: 'universe', label: 'One universe' },
      { id: 'position', label: 'One position' },
      { id: 'fixtureType', label: 'Fixture type' },
      { id: 'selection', label: 'Pick fixtures' },
    ];
    const ua = UI.userInputAttrs();
    let value = '';
    if (scopeKind === 'universe') {
      value = `
        <div class="b5-rcp-scope__value">
          <label class="b5-field__label" for="rcpScopeUniverse">Universe <span class="b5-text-muted b5-text-xs">(${escapeHtml(UI.universeScheme('user'))})</span></label>
          <input type="number" id="rcpScopeUniverse" class="b5-input b5-numinput" min="${ua.min}" max="${ua.max}" value="${UI.formatUser(scopeUniverse)}">
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
              <span>${escapeHtml(e.name || e.fixtureType || e.id)} <span class="b5-text-muted b5-text-xs">U${UI.formatUser(e.universe)}/${e.startAddress}</span></span>
            </label>`).join('') : '<span class="b5-text-muted b5-text-sm">no entries in this patch</span>'}
        </div>`;
    } else if (scopeKind === 'fixtureType') {
      const opts = snap.fixtureTypes || [];
      value = `<div class="b5-rcp-scope__value"><label class="b5-field__label" for="rcpScopeFixtureType">Fixture type</label><select id="rcpScopeFixtureType" class="b5-select"><option value="">Choose a fixture type</option>${opts.map(o => `<option value="${escapeHtml(o.key)}" ${o.key === scopeFixtureTypeDraft ? 'selected' : ''}>${escapeHtml(o.label)} (${o.count})</option>`).join('')}</select><button type="button" class="b5-btn b5-btn--secondary" id="rcpScopeApply">Apply fixture type</button></div>`;
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
    if (p.rate) {
      const rb = rateBoundsFor(t.kind);
      rows.push(slider(t.id, 'rateHz', 'Rate', t.rateHz, rb.min, rb.max, rb.step, ' Hz'));
    }
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

  // decimalsFor: how many decimal places a step needs to be written exactly.
  function decimalsFor(step) {
    const s = String(step);
    const dot = s.indexOf('.');
    return dot < 0 ? 0 : s.length - dot - 1;
  }

  function slider(testId, field, label, value, min, max, step, suffix) {
    // Show as many decimals as the step actually has. A fixed 2 places was
    // fine while every fractional step was 0.05, but it rounds a 0.005 step's
    // values into each other -- 0.005 and 0.01 both reading "0.01" is a
    // readout that lies about where the control is.
    const shown = step < 1 ? Number(value).toFixed(decimalsFor(step)) : String(value);
    return `
      <div class="b5-param">
        <span class="b5-param__label">${escapeHtml(label)} <span class="b5-text-mono b5-param__value" data-rcp-out="${escapeHtml(testId)}:${field}">${escapeHtml(shown)}${suffix || ''}</span></span>
        <input type="range" class="b5-range-touch" data-rcp-slider="${escapeHtml(testId)}:${field}" data-suffix="${suffix || ''}" min="${min}" max="${max}" step="${step}" value="${value}">
      </div>`;
  }

  // ---- output bar ---------------------------------------------------------

  // ---- output protocol ----------------------------------------------------

  // renderProtocol: which wire this check is on RIGHT NOW, stated in WORDS,
  // plus the staged choice and its Apply. Read in a dark venue on a tablet at
  // arm's length, so the state word ("LIVE ON sACN", "STOPPED - armed for
  // Art-Net") and the per-option word carry the whole signal: desaturate this
  // section and it still reads. Screen-kit components only — b5-step-section,
  // b5-segmented/b5-seg, b5-pill, b5-btn — so every control keeps the 44px
  // touch minimum the kit already sets, and no new CSS is introduced.
  //
  // It sits directly above the START button that carries the choice, because
  // the press and its consequence belong in one field of view.
  function renderProtocol() {
    const running = !!snap.outputEnabled;
    const liveLabel = UI.protocolLabel(snap.protocol || fcProtocolArmed);
    const armedLabel = UI.protocolLabel(fcProtocolArmed);
    const draftLabel = UI.protocolLabel(fcProtocolDraft);
    const dirty = fcProtocolDraft !== fcProtocolArmed;

    const statePill = running
      ? `<span class="b5-pill b5-pill--lg b5-pill--ok b5-pill--solid">${UI.icon('status-ok')}LIVE ON ${escapeHtml(liveLabel)}</span>`
      : `<span class="b5-pill b5-pill--lg b5-pill--open">${UI.icon('status-pending')}STOPPED &middot; armed for ${escapeHtml(armedLabel)}</span>`;

    const options = UI.RC_PROTOCOLS.map(pr => {
      const isDraft = pr.id === fcProtocolDraft;
      const isArmed = pr.id === fcProtocolArmed;
      const word = UI.protocolOptionWord(running, isArmed, isDraft);
      const tone = isArmed ? ' b5-pill--ok' : isDraft ? ' b5-pill--warn' : ' b5-pill--open';
      return `<button type="button" class="b5-seg ${isDraft ? 'is-on' : ''}" data-rcp-protocol="${pr.id}" aria-pressed="${isDraft}" ${fcProtocolApplying ? 'disabled' : ''}>${escapeHtml(pr.label)} <span class="b5-pill b5-pill--tag${tone}">${word}</span></button>`;
    }).join('');

    const applyRow = dirty
      ? `<div class="b5-row" style="margin-top:var(--b5-space-3)">
          <span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}Not applied yet &mdash; ${escapeHtml(armedLabel)} is still ${running ? 'on the wire' : 'armed'}</span>
          <button type="button" id="rcpProtocolApply" class="b5-btn b5-btn--primary" ${fcProtocolApplying ? 'disabled' : ''}>${fcProtocolApplying ? UI.spinner() : UI.icon('apply')}Apply ${escapeHtml(draftLabel)}</button>
          <button type="button" id="rcpProtocolRevert" class="b5-btn b5-btn--ghost" ${fcProtocolApplying ? 'disabled' : ''}>${UI.icon('revert')}Revert</button>
        </div>
        <p class="b5-caption">${running
        ? `Applying stops output on ${escapeHtml(armedLabel)} &mdash; receivers are told the stream has ended &mdash; and restarts the same tests on ${escapeHtml(draftLabel)}. Your selection is untouched.`
        : 'Nothing goes on the wire until START. Apply decides which protocol START uses.'}</p>`
      : '';

    return `
      <section class="b5-step-section" aria-labelledby="rcpProtocolHead">
        <h2 class="b5-step-section__head" id="rcpProtocolHead">Output protocol
          <span class="b5-step-section__note" id="rcpProtocolState">${statePill}</span>
        </h2>
        <div class="b5-segmented" role="group" aria-label="Output protocol">${options}</div>
        ${applyRow}
        <p class="b5-rcp-scope__note">This is the same armed protocol the Channel check uses &mdash; one wire is live at a time, never both. The sACN start universe, priority and unicast override are set on the Settings screen.</p>
      </section>`;
  }

  // wireProtocol / applyProtocol: the staging half and the commit half. The
  // press handler sends NOTHING — that is the whole point of the contract.
  function wireProtocol(el) {
    el.querySelectorAll('[data-rcp-protocol]').forEach(b => b.addEventListener('click', () => {
      const id = b.dataset.rcpProtocol;
      if (!UI.isProtocol(id) || id === fcProtocolDraft) return;
      fcProtocolDraft = id;
      render();
    }));
    const applyBtn = el.querySelector('#rcpProtocolApply');
    if (applyBtn) applyBtn.addEventListener('click', applyProtocol);
    const revertBtn = el.querySelector('#rcpProtocolRevert');
    if (revertBtn) revertBtn.addEventListener('click', () => { fcProtocolDraft = fcProtocolArmed; render(); });
  }

  // applyProtocol: while output is LIVE this is a real operation on a rig, so
  // it is confirmed BY NAME first. The single output call does the whole
  // switch server-side, which is what terminates the outgoing sACN stream
  // properly instead of just going quiet.
  //
  // While nothing is running there is nothing to tell the server — the
  // protocol travels as a field on the next START — so Apply moves the staged
  // choice to ARMED and says so. It is still the commit step: the press that
  // changed the buttons decided nothing on its own.
  async function applyProtocol() {
    if (fcProtocolDraft === fcProtocolArmed) return;
    const running = !!(snap && snap.outputEnabled);
    const from = UI.protocolLabel(fcProtocolArmed);
    const to = UI.protocolLabel(fcProtocolDraft);
    const want = fcProtocolDraft;
    if (running && !confirm(`Switch live output from ${from} to ${to}?\n\nOutput stops on ${from} (receivers are told the stream has ended) and restarts on ${to} with the same tests selected. Real fixtures change state now.`)) return;

    let ok = true;
    if (running) {
      fcProtocolApplying = true;
      render();
      ok = await apply(() => Api.patternSetOutput(true, want));
      fcProtocolApplying = false;
    }
    if (ok) {
      fcProtocolArmed = want;
      fcProtocolDraft = want;
      fcProtocolTouched = true;
      host.setStatus(running ? ('output restarted on ' + to) : ('armed for ' + to + ' — nothing goes on the wire until START'));
    } else {
      // apply() has already re-read the server and left its refusal in
      // errMsg VERBATIM (renderErrors shows it whole — the 422 for a scope
      // that cannot be expressed on sACN NAMES the universe, and that name
      // is the only part that tells a tech what to change). Server truth
      // wins on the control itself.
      snapProtocolToServer();
    }
    render();
  }

  // renderOutputBar: START and STOP, both always present and both always in
  // the same place — the owner must never have to hunt for STOP, and a
  // control that appears/disappears moves its neighbour under a moving
  // thumb. Sticky to the bottom edge so it is thumb-reachable on a tablet
  // however far down the test grid is scrolled.
  function renderFade() {
    const ms = Number.isFinite(snap.fadeMs) ? snap.fadeMs : 1000;
    const choices = [0, 250, 500, 1000, 2000, 3000, 5000, 10000, 30000];
    if (!choices.includes(ms)) choices.push(ms);
    choices.sort((a, b) => a - b);
    return `<div class="b5-field" style="margin-bottom:var(--space-3)">
      <label for="rcpFade">Fade time</label>
      <select id="rcpFade" aria-describedby="rcpFadeHint">${choices.map(v => `<option value="${v}"${v === ms ? ' selected' : ''}>${v === 0 ? 'Snap (0 s)' : `${v / 1000} s`}</option>`).join('')}</select>
      <p id="rcpFadeHint" class="b5-text-sm b5-text-muted">Applies to level changes and entering or leaving continuous tests. Rate still sets the test speed. STOP stays immediate.</p>
    </div>`;
  }

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

    const fade = el.querySelector('#rcpFade');
    if (fade) fade.addEventListener('change', () => apply(() => Api.patternSetFade(Number(fade.value))));

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
      if (k === 'fixtureType') { scopeFixtureTypeDraft = ''; render(); return; }
      await setScope();
    }));

    const uni = el.querySelector('#rcpScopeUniverse');
    if (uni) uni.addEventListener('change', async (e) => {
      // A universe-base flip re-renders this input. Chromium can still emit
      // the old focused input's queued change afterward; it must never write
      // a stale display-base value into the newly-rendered scope.
      if (!e.target.isConnected) return;
      scopeUniverse = UI.parseUser(e.target.value);
      await setScope();
    });

    const ft = el.querySelector('#rcpScopeFixtureType');
    if (ft) ft.addEventListener('change', e => { scopeFixtureTypeDraft = e.target.value; });
    const fa = el.querySelector('#rcpScopeApply');
    if (fa) fa.addEventListener('click', async () => { if (!scopeFixtureTypeDraft) { errMsg = 'choose a fixture type'; render(); return; } await setScope(); });

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

    wireProtocol(el);

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
      : scopeKind === 'universe' ? `universe ${UI.formatUser(scopeUniverse)}`
        : scopeKind === 'position' ? `position "${scopePosition}"`
      : scopeKind === 'fixtureType' ? `fixture type "${scopeFixtureType}"`
          : `${total} picked fixture${total === 1 ? '' : 's'}`;
    const proto = UI.protocolLabel(fcProtocolArmed);
    if (!confirm(`Let output flow over ${proto}: ${n} test${n === 1 ? '' : 's'} on ${scopeWord} (${total} fixture${total === 1 ? '' : 's'}). This moves real fixtures now.`)) return;
    starting = true;
    render();
    const ok = await setOutput(true);
    starting = false;
    // A refused start (a scope that cannot be expressed on the chosen
    // protocol is a 422) leaves the server armed on whatever it was armed
    // on. Show that, not the attempt; errMsg already carries the server's
    // own words.
    if (!ok) snapProtocolToServer();
    render();
    host.setStatus(ok ? ('output running on ' + proto) : ('error: ' + errMsg));
  }

  return { init, attach, enter, leave, render, refreshStatus, outputEnabled, selectedCount, stopOutput, stopPolling };
})();
