// nodes.js — Nodes screen: table + detail pane.
// Rules (architecture rev 5 §4, adopted from Rackmaster's pitfalls):
//  - oninput mutates state only; full re-render happens on onchange/explicit refresh.
//  - re-render preserves the selected row and scroll position.
const NodesScreen = (() => {
  let nodes = [];
  let selectedKey = null; // "ip|bindIndex"

  function keyOf(n) { return n.ip + '|' + n.bindIndex; }

  async function refresh() {
    nodes = await Api.getNodes();
    render();
  }

  function render() {
    const tbody = document.querySelector('#nodesTable tbody');
    const scrollTop = tbody.parentElement.scrollTop;
    tbody.innerHTML = '';
    nodes.forEach(n => {
      const tr = document.createElement('tr');
      if (keyOf(n) === selectedKey) tr.classList.add('selected');
      tr.innerHTML = `
        <td>${escapeHtml(n.shortName || n.longName || '(unnamed)')}</td>
        <td>${escapeHtml(n.ip)}</td>
        <td>${n.ports ? n.ports.length : 0}</td>
        <td>${n.rdmCapable ? '<span class="badge yes">RDM</span>' : '<span class="badge no">—</span>'}</td>
        <td>${n.stale ? '<span class="badge stale">stale</span>' : new Date(n.lastSeen).toLocaleTimeString()}</td>
      `;
      tr.addEventListener('click', () => { selectedKey = keyOf(n); render(); });
      tbody.appendChild(tr);
    });
    tbody.parentElement.scrollTop = scrollTop;
    renderDetail();
  }

  function renderDetail() {
    const el = document.getElementById('nodeDetail');
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) {
      el.innerHTML = '<p class="empty-hint">Select a node to see its ports and universes.</p>';
      return;
    }
    const portRows = (n.ports || []).map(p => `
      <tr>
        <td>${p.index}</td>
        <td>${p.input ? 'in' : ''} ${p.output ? 'out' : ''}</td>
        <td>${p.inputAddress}</td>
        <td>${p.outputAddress}</td>
        <td>${p.rdmEnabled ? 'yes' : 'no'}</td>
      </tr>`).join('');
    el.innerHTML = `
      <h3>${escapeHtml(n.longName || n.shortName)}</h3>
      <div class="field-row"><label>IP</label><span>${escapeHtml(n.ip)} (bind ${n.bindIndex})</span></div>
      <div class="field-row"><label>Style</label><span>${escapeHtml(n.style)}</span></div>
      <div class="field-row"><label>Fixtures seen</label><span>${n.fixtureCount}</span></div>
      <table class="data-table">
        <thead><tr><th>Port</th><th>Dir</th><th>In addr</th><th>Out addr</th><th>BiDi</th></tr></thead>
        <tbody>${portRows}</tbody>
      </table>
    `;
  }

  function init() {
    document.getElementById('btnRefreshNodes').addEventListener('click', refresh);
    Live.on('node', () => refresh());
    refresh();
  }

  return { init, refresh };
})();

function escapeHtml(s) {
  if (s === undefined || s === null) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}
