// app.js — tab switching + module bootstrap.
(function () {
  const tabs = document.querySelectorAll('.tab-btn');
  const screens = document.querySelectorAll('.screen');

  function activate(name) {
    tabs.forEach(t => t.classList.toggle('active', t.dataset.tab === name));
    screens.forEach(s => s.classList.toggle('active', s.id === 'screen-' + name));
    localStorage.setItem('benny512.tab', name);
  }

  tabs.forEach(t => t.addEventListener('click', () => activate(t.dataset.tab)));

  const initial = localStorage.getItem('benny512.tab') || 'nodes';
  activate(initial);

  NodesScreen.init();
  FixturesScreen.init();
  AnalyzerScreen.init();
  SendScreen.init();
  SettingsScreen.init();
})();
