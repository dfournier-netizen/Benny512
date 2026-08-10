// analyzer.js — Analyzer screen: live packet feed via WS "capture" batches,
// throttled server-side to ~10 fps; client renders at <=15fps by simply
// appending each incoming batch (already throttled upstream).
const AnalyzerScreen = (() => {
  const MAX_ROWS = 500;
  let rows = [];
  let paused = false;
  let filterKind = '';
  let filterUniverse = null;

  function matches(e) {
    if (filterKind && e.Kind !== filterKind) return false;
    if (filterUniverse !== null && e.Universe !== filterUniverse) return false;
    return true;
  }

  function onBatch(msg) {
    if (paused) return;
    const batch = (msg.Batch || msg.batch || []).filter(matches);
    if (batch.length === 0) return;
    rows = rows.concat(batch).slice(-MAX_ROWS);
    render();
  }

  function render() {
    const tbody = document.querySelector('#analyzerTable tbody');
    const nearBottom = tbody.parentElement.scrollTop + tbody.parentElement.clientHeight >= tbody.parentElement.scrollHeight - 10;
    tbody.innerHTML = '';
    rows.forEach(e => {
      const tr = document.createElement('tr');
      const t = e.Time ? new Date(e.Time).toLocaleTimeString() : '';
      tr.innerHTML = `
        <td>${t}</td>
        <td>${e.Dir === 1 ? 'out' : 'in'}</td>
        <td>${escapeHtml(e.Kind)}</td>
        <td>${e.Peer ? escapeHtml(String(e.Peer).split(':')[0]) : ''}</td>
        <td>${e.Universe || ''}</td>
        <td>${e.Size}</td>
        <td class="hexcell" title="click to expand hex">${escapeHtml(e.Key)}</td>
      `;
      if (e.Hex) {
        tr.querySelector('.hexcell').addEventListener('click', () => {
          alert(e.Hex);
        });
      }
      tbody.appendChild(tr);
    });
    if (nearBottom) tbody.parentElement.scrollTop = tbody.parentElement.scrollHeight;
  }

  function init() {
    Live.on('capture', onBatch);
    document.getElementById('btnPause').addEventListener('click', (e) => {
      paused = !paused;
      e.target.textContent = paused ? 'Resume' : 'Pause';
    });
    document.getElementById('btnClearAnalyzer').addEventListener('click', () => {
      rows = [];
      render();
    });
    document.getElementById('filterKind').addEventListener('change', (e) => {
      filterKind = e.target.value;
    });
    document.getElementById('filterUniverse').addEventListener('change', (e) => {
      filterUniverse = e.target.value === '' ? null : parseInt(e.target.value, 10);
    });
  }

  return { init };
})();
