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
    if (currentTab === 'walk' && name !== 'walk' && window.WalkScreen) {
      WalkScreen.onLeaveScreen();
    }
    tabs.forEach(t => t.classList.toggle('active', t.dataset.tab === name));
    screens.forEach(s => s.classList.toggle('active', s.id === 'screen-' + name));
    localStorage.setItem('benny512.tab', name);
    currentTab = name;
    if (name === 'walk' && window.WalkScreen) {
      WalkScreen.onEnterScreen();
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
  AnalyzerScreen.init();
  SendScreen.init();
  WalkScreen.init();
  SettingsScreen.init();
})();
