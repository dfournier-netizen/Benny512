// analyzer.js — Analyzer screen: "All traffic" (live packet feed via WS
// "capture" batches, throttled server-side to ~10 fps) and a dedicated
// "RDM" sub-view (report task item 3) reading from the RDM-only capture
// ring — filterable by UID/PID/command class/direction, with a
// request/response paired display (matched by TN+UID, round-trip time) and
// full hex on expand. Both views share the live "capture" WS feed for
// near-real-time updates; the RDM view additionally supports an explicit
// Refresh (it pulls the full server-side filtered snapshot, which the
// WS-trickle can't reconstruct after a filter change).
const AnalyzerScreen = (() => {
  const MAX_ROWS = 500;
  let rows = [];
  let paused = false;
  let filterKind = '';
  let filterUniverse = null;

  let activeView = 'all'; // 'all' | 'rdm'
  let rdmRows = [];       // flat entries from the RDM-only ring
  let rdmFilters = { uid: '', pid: '', cc: '', dir: '' };
  let rdmPaired = true;
  let expandedSeq = null; // Seq of the row whose hex/decoded detail is expanded

  function matches(e) {
    if (filterKind && e.Kind !== filterKind) return false;
    if (filterUniverse !== null && e.Universe !== filterUniverse) return false;
    return true;
  }

  function onBatch(msg) {
    if (paused) return;
    const batch = msg.Batch || msg.batch || [];
    const allBatch = batch.filter(matches);
    if (allBatch.length) {
      rows = rows.concat(allBatch).slice(-MAX_ROWS);
      if (activeView === 'all') renderAll();
    }
    const rdmBatch = batch.filter(e => e.RDM || e.Tod);
    if (rdmBatch.length && rdmMatchesFilters(rdmBatch).length) {
      rdmRows = rdmRows.concat(rdmMatchesFilters(rdmBatch)).slice(-MAX_ROWS);
      if (activeView === 'rdm') renderRDM();
    }
  }

  function rdmMatchesFilters(entries) {
    return entries.filter(e => {
      if (rdmFilters.uid && !(e.RDM && (e.RDM.sourceUid === rdmFilters.uid || e.RDM.destUid === rdmFilters.uid))) return false;
      if (rdmFilters.pid && !(e.RDM && e.RDM.pid === parseInt(rdmFilters.pid, 16))) return false;
      if (rdmFilters.cc && !(e.RDM && e.RDM.commandClass === rdmFilters.cc)) return false;
      if (rdmFilters.dir === 'out' && e.Dir !== 1) return false;
      if (rdmFilters.dir === 'in' && e.Dir !== 0) return false;
      return true;
    });
  }

  function renderAll() {
    const tbody = document.querySelector('#analyzerTable tbody');
    const nearBottom = tbody.parentElement.scrollTop + tbody.parentElement.clientHeight >= tbody.parentElement.scrollHeight - 10;
    tbody.innerHTML = '';
    if (!rows.length) {
      tbody.innerHTML = `<tr><td colspan="7"><div class="b5-empty">${UI.icon('nav-analyzer')}<span class="b5-empty__title">No traffic captured</span><span class="b5-empty__body">Start a capture on an active universe to see packets here.</span></div></td></tr>`;
      return;
    }
    rows.forEach(e => {
      const tr = document.createElement('tr');
      const t = e.Time ? new Date(e.Time).toLocaleTimeString() : '';
      tr.innerHTML = `
        <td data-label="Time" class="b5-table__mono">${t}</td>
        <td data-label="Dir">${e.Dir === 1 ? 'out' : 'in'}</td>
        <td data-label="Kind">${UI.tag(e.Kind)}</td>
        <td data-label="Src" class="b5-table__mono">${e.Peer ? escapeHtml(String(e.Peer).split(':')[0]) : ''}</td>
        <td data-label="Univ">${e.Universe || ''}</td>
        <td data-label="Size">${e.Size}</td>
        <td data-label="Key fields" class="b5-text-mono hexcell" title="click to expand hex">${escapeHtml(e.Key)}</td>
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

  // --- RDM-focused view ------------------------------------------------------

  async function refreshRDM() {
    const status = document.getElementById('rdmAnalyzerStatus');
    status.innerHTML = UI.spinner() + 'loading…';
    try {
      const params = { uid: rdmFilters.uid, pid: rdmFilters.pid, cc: rdmFilters.cc, dir: rdmFilters.dir };
      rdmRows = await Api.rdmCaptureSnapshot(params);
      status.textContent = `${rdmRows.length} entr${rdmRows.length === 1 ? 'y' : 'ies'}`;
      renderRDM();
    } catch (e) {
      status.innerHTML = `${UI.icon('status-error')}error: ${escapeHtml(e.message)}`;
    }
  }

  // pairEntries mirrors internal/web/capture_export.go's pairExchanges: a
  // request's key is (TN, destUid), a response's key is (TN, sourceUid) —
  // matching keys are one request/response pair.
  function pairEntries(entries) {
    const pending = new Map();
    const order = [];
    entries.forEach(e => {
      if (!e.RDM) { order.push({ request: e }); return; }
      if (!e.RDM.isResponse) {
        const ex = { request: e };
        pending.set(e.RDM.transactionNumber + '|' + e.RDM.destUid, ex);
        order.push(ex);
        return;
      }
      const k = e.RDM.transactionNumber + '|' + e.RDM.sourceUid;
      const ex = pending.get(k);
      if (ex && !ex.response) {
        ex.response = e;
        pending.delete(k);
        return;
      }
      order.push({ response: e });
    });
    return order;
  }

  function renderRDM() {
    const tbody = document.querySelector('#rdmAnalyzerTable tbody');
    if (!tbody) return;
    tbody.innerHTML = '';
    const filtered = rdmMatchesFilters(rdmRows);
    const items = rdmPaired ? pairEntries(filtered) : filtered.map(e => ({ request: e.RDM && !e.RDM.isResponse ? e : null, response: e.RDM && e.RDM.isResponse ? e : e }));

    if (!items.length) {
      tbody.innerHTML = `<tr><td colspan="9"><div class="b5-empty">${UI.icon('nav-analyzer')}<span class="b5-empty__title">No RDM traffic captured</span><span class="b5-empty__body">Adjust filters, or start a capture with RDM activity on the network.</span></div></td></tr>`;
      return;
    }
    items.forEach(item => {
      const primary = item.request || item.response;
      if (!primary) return;
      appendRDMRow(tbody, item.request, item.response);
    });
  }

  function appendRDMRow(tbody, req, resp) {
    const e = req || resp;
    const d = e.RDM;
    const tr = document.createElement('tr');
    tr.className = 'hexcell';
    if (!d) {
      // ToD or decode-error entry.
      const t = e.Time ? new Date(e.Time).toLocaleTimeString() : '';
      tr.innerHTML = `<td data-label="Time" class="b5-table__mono">${t}</td><td data-label="Dir">${e.Dir === 1 ? 'out' : 'in'}</td><td data-label="Decoded" colspan="7">${escapeHtml(e.Kind)} ${e.Tod ? escapeHtml(JSON.stringify(e.Tod).slice(0, 120)) : (e.Key || '')}</td>`;
      wireExpand(tr, e, null);
      tbody.appendChild(tr);
      return;
    }
    const t = req ? new Date(req.Time).toLocaleTimeString() : (resp ? new Date(resp.Time).toLocaleTimeString() : '');
    const uidPair = `${escapeHtml(d.sourceUid || '')} → ${escapeHtml(d.destUid || '')}`;
    const rtt = (req && resp) ? `${(new Date(resp.Time) - new Date(req.Time))}ms` : '';
    const respLabel = resp ? escapeHtml(resp.RDM.responseType || '') + (resp.RDM.responseType === 'NACK_REASON' ? ` (${escapeHtml(resp.RDM.nackReasonName || '')})` : '') : (req ? '(no response captured)' : '');
    const decoded = (resp && resp.RDM.decoded) || (req && req.RDM.decoded) || '';
    tr.innerHTML = `
      <td data-label="Time" class="b5-table__mono">${t}</td>
      <td data-label="Dir">${req ? 'out' : 'in'}${resp && req ? '/in' : ''}</td>
      <td data-label="UID (src → dst)" class="b5-table__mono">${uidPair}</td>
      <td data-label="TN">${d.transactionNumber}</td>
      <td data-label="CC">${escapeHtml(d.commandClass)}</td>
      <td data-label="PID" class="b5-table__mono">0x${(d.pid || 0).toString(16).toUpperCase().padStart(4, '0')} ${escapeHtml(d.pidName || '')}</td>
      <td data-label="Response">${respLabel}</td>
      <td data-label="RTT">${rtt}</td>
      <td data-label="Decoded" class="b5-text-mono b5-text-xs">${escapeHtml(decoded)}</td>
    `;
    wireExpand(tr, req, resp);
    tbody.appendChild(tr);
  }

  function wireExpand(tr, req, resp) {
    tr.title = 'click to expand full hex/decoded detail';
    tr.addEventListener('click', () => {
      const lines = [];
      if (req) lines.push('REQUEST:\n' + describeEntry(req));
      if (resp) lines.push('RESPONSE:\n' + describeEntry(resp));
      if (!req && !resp) return;
      alert(lines.join('\n\n'));
    });
  }

  function describeEntry(e) {
    const parts = [`kind=${e.Kind} dir=${e.Dir === 1 ? 'out' : 'in'} time=${e.Time}`];
    if (e.RDM) {
      parts.push(`cc=${e.RDM.commandClass} pid=0x${(e.RDM.pid || 0).toString(16)} (${e.RDM.pidName})`);
      if (e.RDM.decoded) parts.push('decoded: ' + e.RDM.decoded);
      if (e.RDM.decodeError) parts.push('DECODE ERROR: ' + e.RDM.decodeError);
      parts.push('paramData: ' + (e.RDM.paramDataHex || '(none)'));
    }
    if (e.Tod) parts.push('tod: ' + JSON.stringify(e.Tod));
    parts.push('hex: ' + (e.Hex || '(omitted)'));
    return parts.join('\n');
  }

  // --- export ------------------------------------------------------------

  function exportParams() {
    return { uid: rdmFilters.uid, pid: rdmFilters.pid, cc: rdmFilters.cc, dir: rdmFilters.dir };
  }

  function doExport(format) {
    window.open(Api.exportUrl(format, exportParams()), '_blank');
  }

  // --- view switching + init --------------------------------------------

  function switchView(view) {
    activeView = view;
    document.querySelectorAll('#analyzerViewTabs .detail-tab-btn').forEach(b => {
      b.classList.toggle('is-active', b.dataset.view === view);
    });
    document.getElementById('analyzerAllView').style.display = view === 'all' ? '' : 'none';
    document.getElementById('analyzerRDMView').style.display = view === 'rdm' ? '' : 'none';
    if (view === 'rdm' && rdmRows.length === 0) refreshRDM();
  }

  function init() {
    Live.on('capture', onBatch);
    document.getElementById('btnPause').addEventListener('click', (e) => {
      paused = !paused;
      e.target.textContent = paused ? 'Resume' : 'Pause';
    });
    document.getElementById('btnClearAnalyzer').addEventListener('click', () => {
      rows = [];
      renderAll();
    });
    document.getElementById('filterKind').addEventListener('change', (e) => {
      filterKind = e.target.value;
    });
    document.getElementById('filterUniverse').addEventListener('change', (e) => {
      filterUniverse = e.target.value === '' ? null : parseInt(e.target.value, 10);
    });

    document.querySelectorAll('#analyzerViewTabs .detail-tab-btn').forEach(btn => {
      btn.addEventListener('click', () => switchView(btn.dataset.view));
    });
    switchView('all');
    renderAll();

    document.getElementById('rdmFilterUid').addEventListener('change', e => { rdmFilters.uid = e.target.value.trim(); refreshRDM(); });
    document.getElementById('rdmFilterPid').addEventListener('change', e => { rdmFilters.pid = e.target.value.trim(); refreshRDM(); });
    document.getElementById('rdmFilterCC').addEventListener('change', e => { rdmFilters.cc = e.target.value; refreshRDM(); });
    document.getElementById('rdmFilterDir').addEventListener('change', e => { rdmFilters.dir = e.target.value; refreshRDM(); });
    document.getElementById('rdmPairedView').addEventListener('change', e => { rdmPaired = e.target.checked; renderRDM(); });
    document.getElementById('btnRDMRefresh').addEventListener('click', refreshRDM);
    document.getElementById('btnExportRDMJson').addEventListener('click', () => doExport('json'));
    document.getElementById('btnExportRDMTxt').addEventListener('click', () => doExport('txt'));
  }

  return { init };
})();
