// app.js — tab switching + module bootstrap.
(function () {
  // Desktop (#tabs) and mobile bottom-bar (#tabsMobile) are two separate
  // <nav> elements carrying duplicate [data-tab] buttons (design-system
  // pattern: render both, only one is visible per breakpoint via CSS) — both
  // sets must reflect the active tab together.
  const tabs = document.querySelectorAll('[data-tab]');
  const screens = document.querySelectorAll('.screen');
  let currentTab = null;

  function activate(name) {
    // Leaving Rig Walk (to any other tab) must never leave a fixture
    // flashing — task ask: "turn Identify OFF ... when the user leaves Rig
    // Walk, navigates away". WalkScreen.onLeaveScreen is a no-op if no walk
    // session is active.
    //
    // ROOT CAUSE (2026-08-15, "Rig Walk is a blank page"): this used to
    // gate both calls behind `window.WalkScreen`. WalkScreen is declared as
    // a top-level `const` in walk.js (a classic, non-module <script>, same
    // as every other *Screen module here) — per JS semantics, a top-level
    // let/const creates a global-scope binding but is NEVER added as a
    // property of `window` (only `var`/function declarations are). So
    // `window.WalkScreen` was always undefined, both guards always
    // evaluated false, and WalkScreen.onEnterScreen() — the only thing that
    // actually calls refresh()/render() and populates #walkRoot — never
    // ran, on any device, on every navigation to the tab. WalkScreen.init()
    // below is NOT gated this way, which is why the safety-net
    // pagehide/visibilitychange listeners it wires still worked and made
    // the bug look partial rather than an obvious crash. Every other
    // screen module (NodesScreen, DevicesScreen, ...) is referenced by its
    // bare identifier, never through `window.`; WalkScreen now matches that
    // same convention (script load order in index.html guarantees walk.js
    // has already run by the time app.js executes, so no existence guard
    // is needed here any more than the other five screens have one).
    if (currentTab === 'walk' && name !== 'walk') {
      WalkScreen.onLeaveScreen();
    }
    // Patch screen's Rig Check sub-view drives live DMX output — leaving
    // the tab must always stop it (task ask, item 4: "never leave the rig
    // lit"), same discipline and same top-level-const-not-on-window
    // gotcha noted above for WalkScreen: reference PatchScreen by its bare
    // identifier, never window.PatchScreen.
    if (currentTab === 'patch' && name !== 'patch') {
      PatchScreen.onLeaveScreen();
    }
    if (currentTab === 'send' && name !== 'send') {
      SendScreen.onLeaveScreen();
    }
    tabs.forEach(t => t.classList.toggle('is-active', t.dataset.tab === name));
    screens.forEach(s => s.classList.toggle('active', s.id === 'screen-' + name));
    // Rig Walk's own fixed b5-walk-bar and the phone bottom tab bar both
    // sit at the screen's bottom edge (design-system components, not a bug
    // introduced here) — hide the tab bar while walk mode owns that band,
    // matching the immersive/one-handed intent of Rig Walk. See app.css.
    document.body.classList.toggle('b5-walk-mode', name === 'walk');
    localStorage.setItem('benny512.tab', name);
    currentTab = name;
    if (name === 'walk') {
      WalkScreen.onEnterScreen();
    }
    if (name === 'patch') {
      PatchScreen.onEnterScreen();
    }
    if (name === 'send') {
      SendScreen.onEnterScreen();
    }
  }

  tabs.forEach(t => t.addEventListener('click', () => activate(t.dataset.tab)));
  window.addEventListener('b5-navigate', e => activate(e.detail));

  // publishChromeHeight: keep --b5-chrome-height equal to the real height of
  // the sticky app chrome (.b5-header + .b5-show-context), so a fixed
  // overlay can start BELOW it instead of underneath it.
  //
  // This exists because the Devices inspector is position:fixed and was
  // anchored to the viewport top, which put its own bar -- and its close
  // button -- behind the show-context strip. A constant would be wrong
  // exactly where it hurts: .b5-show-context sets flex-wrap:wrap and caps
  // the show name at 35vw, so on a tablet in portrait the chrome is two rows
  // tall, and a tablet in a dark venue is the job this app is for.
  //
  // ResizeObserver where available, a resize listener otherwise; both are
  // built-ins, so this keeps the project's zero-dependency rule.
  function publishChromeHeight() {
    const header = document.querySelector('.b5-header');
    const context = document.querySelector('.b5-show-context');
    const measure = () => {
      let h = 0;
      if (header) h += header.getBoundingClientRect().height;
      // The strip is emptied (not removed) when there is no show context, so
      // measure it rather than assuming it is always a row tall.
      if (context) h += context.getBoundingClientRect().height;
      document.documentElement.style.setProperty('--b5-chrome-height', h + 'px');
    };
    measure();
    if (typeof ResizeObserver === 'function') {
      const ro = new ResizeObserver(measure);
      if (header) ro.observe(header);
      if (context) ro.observe(context);
    }
    window.addEventListener('resize', measure);
  }
  publishChromeHeight();

  // 'fixtures' migrates to 'devices' (Phase 1c+ screen rename) for anyone
  // with a stale localStorage value from before this change.
  let initial = localStorage.getItem('benny512.tab') || 'nodes';
  if (initial === 'fixtures') initial = 'devices';

  // The Art-Net starting universe must be settled BEFORE any screen's first
  // render — a stale paint that flips a moment later reads as the numbers
  // "jumping" on load. GET /api/settings is fast (in-memory on the server)
  // and best-effort: any failure keeps the built-in default of 0 (user
  // universe 1 = Art-Net universe 0) rather than blocking the whole app.
  Api.getSettings().then(s => UI.setArtnetStart(s.artnetStartUniverse)).catch(() => {}).then(() => {
    activate(initial);

    NodesScreen.init();
    DevicesScreen.init();
    PatchScreen.init();
    AnalyzerScreen.init();
    SendScreen.init();
    WalkScreen.init();
    SettingsScreen.init();
    Workspace.init();
  });
})();
