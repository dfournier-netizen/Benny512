// app.js — tab switching + module bootstrap.
(function () {
  const tabs = document.querySelectorAll('.tab-btn');
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
    tabs.forEach(t => t.classList.toggle('active', t.dataset.tab === name));
    screens.forEach(s => s.classList.toggle('active', s.id === 'screen-' + name));
    localStorage.setItem('benny512.tab', name);
    currentTab = name;
    if (name === 'walk') {
      WalkScreen.onEnterScreen();
    }
    if (name === 'patch') {
      PatchScreen.onEnterScreen();
    }
  }

  tabs.forEach(t => t.addEventListener('click', () => activate(t.dataset.tab)));

  // 'fixtures' migrates to 'devices' (Phase 1c+ screen rename) for anyone
  // with a stale localStorage value from before this change.
  let initial = localStorage.getItem('benny512.tab') || 'nodes';
  if (initial === 'fixtures') initial = 'devices';
  activate(initial);

  NodesScreen.init();
  DevicesScreen.init();
  PatchScreen.init();
  AnalyzerScreen.init();
  SendScreen.init();
  WalkScreen.init();
  SettingsScreen.init();
})();
