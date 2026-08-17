// patch.js — Phase 2a Patch screen: entry table (add/edit/delete/reorder),
// collision warnings, the patch<->RDM Reconcile view (diff grouped by
// state, per-row/bulk fix, Identify assist), and channel-level Rig Check
// controls.
//
// Rules (same as every other screen, architecture rev 5 §4): oninput
// mutates local draft state only, re-render on onchange/explicit action;
// focus+scroll preserved (the entries table filter/sort re-render only the
// table body, never the input/select that has focus); accessibility (never
// color as the sole signal — every status pairs text with any color/badge);
// vocabulary "ports" not "jacks". Apply-to-confirm: the entry add/edit form
// only sends on an explicit Save click (oninput just updates the draft);
// reconcile fix/fix-all/adopt actions are explicit, confirmed clicks
// (native confirm() dialogs listing exactly what will change, mirroring
// walk.js's End Walk pattern) — nothing here ever fires an RDM SET as a
// side effect of typing or navigating.
//
// Safety (task ask, item 4): leaving the Patch screen or the page unloading
// always best-effort stops the rig check (blackout + stop transmitting),
// mirroring walk.js's identify-off beacon.
const PatchScreen = (() => {
  let active = false; // this screen is the current tab
  let view = 'entries'; // 'entries' | 'reconcile' | 'rigcheck'
  let patchData = { active: false };
  let collisions = [];
  let reconcile = null;
  let rigCheckState = null;
  let statusMsg = '';

  // Entries table state.
  let filterText = '';
  let sortMode = 'address';
  let editingEntry = null; // null | 'new' | entry id being edited
  let entryDraft = null;

  // Rig check setup state.
  let rcScopeKind = sessionStorage.getItem('benny512.patch.rcScopeKind') || 'all';
  let rcScopeUniverse = 0;
  let rcSelection = {}; // entryId -> bool
  let rcMode = sessionStorage.getItem('benny512.patch.rcMode') || 'highlight';
  let rcLevel = 255;
  let rcStarting = false;

  // --- lifecycle ------------------------------------------------------------

  function init() {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden' && active) Api.rigCheckStopBeacon();
    });
    window.addEventListener('pagehide', () => {
      if (active) Api.rigCheckStopBeacon();
    });
  }

  function onEnterScreen() {
    active = true;
    refresh();
  }

  function onLeaveScreen() {
    active = false;
    // Best-effort, always — whether or not the Rig Check sub-view was the
    // one on screen, the running check must never keep lighting the rig
    // after the tech has navigated away (task ask: "never leave the rig
    // lit"). A no-op server-side if nothing is running.
    Api.rigCheckStop().catch(() => {});
  }

  // --- data refresh -----------------------------------------------------

  async function refresh() {
    try {
      patchData = await Api.getPatch();
    } catch (e) {
      statusMsg = 'error: ' + e.message;
    }
    try {
      collisions = patchData.active ? await Api.getPatchCollisions() : [];
    } catch (e) { /* best-effort */ }
    if (view === 'reconcile') await refreshReconcile();
    if (view === 'rigcheck') await refreshRigCheck();
    render();
  }

  async function refreshReconcile() {
    try {
      reconcile = await Api.getPatchReconcile();
    } catch (e) {
      statusMsg = 'error: ' + e.message;
    }
  }

  async function refreshRigCheck() {
    try {
      rigCheckState = await Api.getRigCheckState();
    } catch (e) { /* best-effort */ }
  }

  function setView(v) {
    view = v;
    render();
    if (v === 'reconcile' && !reconcile) refreshReconcile().then(render);
    if (v === 'rigcheck') refreshRigCheck().then(render);
  }

  function setStatus(msg) {
    statusMsg = msg;
    const el = document.getElementById('patchStatusMsg');
    if (el) el.textContent = msg;
  }

  // --- root render --------------------------------------------------------

  function render() {
    const el = document.getElementById('patchRoot');
    if (!el) return;
    el.innerHTML = `
      <div class="detail-tabs" id="patchViewTabs">
        <button class="detail-tab-btn" data-view="entries">Entries</button>
        <button class="detail-tab-btn" data-view="reconcile">Reconcile</button>
        <button class="detail-tab-btn" data-view="rigcheck">Rig Check</button>
      </div>
      <div class="hint" id="patchStatusMsg">${escapeHtml(statusMsg)}</div>
      <div id="patchViewBody"></div>
    `;
    document.querySelectorAll('#patchViewTabs .detail-tab-btn').forEach(b => {
      b.classList.toggle('active', b.dataset.view === view);
      b.addEventListener('click', () => setView(b.dataset.view));
    });
    const body = document.getElementById('patchViewBody');
    if (view === 'entries') renderEntries(body);
    else if (view === 'reconcile') renderReconcile(body);
    else renderRigCheck(body);
  }

  // ============================================================
  // Entries view
  // ============================================================

  function renderEntries(body) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    body.innerHTML = `
      <div class="panel-toolbar">
        <button id="btnNewPatch">New patch…</button>
        <button id="btnAddEntry">Add entry</button>
        <button id="btnAdoptMerge">Adopt discovered (merge)</button>
        <button id="btnAdoptFresh">Adopt discovered (replace patch)</button>
        <span class="hint">${p ? escapeHtml(p.name || '(unnamed)') + ' — ' + entries.length + ' entr' + (entries.length === 1 ? 'y' : 'ies') : 'No patch yet — add an entry or adopt the discovered rig to start one.'}</span>
      </div>
      <div class="panel-toolbar">
        <label>Filter: <input type="text" id="patchFilter" placeholder="name, type, fixture #…" value="${escapeHtml(filterText)}"></label>
        <label>Sort: <select id="patchSort">
          <option value="address">Universe + address</option>
          <option value="name">Name</option>
          <option value="type">Fixture type</option>
          <option value="fixtureNumber">Fixture number</option>
        </select></label>
        <button id="btnExportPatchJson">Export JSON</button>
        <button id="btnExportPatchTxt">Export TXT</button>
      </div>
      ${renderCollisionBanner(collisions)}
      <table class="data-table" id="patchEntriesTable">
        <thead><tr>
          <th>Universe</th><th>Address</th><th>Name</th><th>Fixture type</th><th>Fixture #</th><th>RDM match</th><th></th>
        </tr></thead>
        <tbody></tbody>
      </table>
      <div id="patchEntryEditor"></div>
    `;
    document.getElementById('patchSort').value = sortMode;

    document.getElementById('btnNewPatch').addEventListener('click', async () => {
      const name = prompt('New patch name (this discards the current patch):', 'Patch');
      if (name === null) return;
      try {
        patchData = await Api.newPatch(name);
        setStatus('new patch created');
        editingEntry = null; entryDraft = null;
        await refresh();
      } catch (e) { setStatus('error: ' + e.message); }
    });
    document.getElementById('btnAddEntry').addEventListener('click', openNewEntry);
    document.getElementById('btnAdoptMerge').addEventListener('click', () => runAdopt('merge'));
    document.getElementById('btnAdoptFresh').addEventListener('click', () => runAdopt('fresh'));
    document.getElementById('btnExportPatchJson').addEventListener('click', () => window.open(Api.patchExportUrl('json'), '_blank'));
    document.getElementById('btnExportPatchTxt').addEventListener('click', () => window.open(Api.patchExportUrl('txt'), '_blank'));

    const filterInput = document.getElementById('patchFilter');
    filterInput.addEventListener('input', (e) => { filterText = e.target.value; renderEntriesTableBody(); });
    document.getElementById('patchSort').addEventListener('change', (e) => { sortMode = e.target.value; renderEntriesTableBody(); });

    renderEntriesTableBody();
    renderEntryEditor();
  }

  async function runAdopt(mode) {
    const verb = mode === 'fresh' ? 'REPLACE the current patch with' : 'merge into the current patch';
    if (!confirm(`Adopt the currently discovered rig — ${verb} entries built from live RDM devices (name/type/footprint/universe/address from what's on the network right now, UID pre-confirmed)?`)) return;
    try {
      patchData = await Api.patchAdopt(mode);
      setStatus('adopted from discovered rig');
      await refresh();
    } catch (e) { setStatus('error: ' + e.message); }
  }

  // renderCollisionBanner: text+glyph, never color alone (accessibility
  // standing constraint) — every finding line names its own severity in
  // words, the banner styling is a secondary cue only.
  function renderCollisionBanner(findings) {
    if (!findings.length) return '';
    return `
      <div class="caution-banner">
        <strong>&#9888; ${findings.length} patch issue${findings.length === 1 ? '' : 's'} found</strong>
        <ul class="patch-finding-list">
          ${findings.map(f => `<li>[${escapeHtml(f.severity.toUpperCase())}] ${escapeHtml(f.message)}</li>`).join('')}
        </ul>
      </div>
    `;
  }

  function indexFindingsByEntry(findings) {
    const idx = {};
    findings.forEach(f => (f.entryIds || []).forEach(id => { (idx[id] = idx[id] || []).push(f); }));
    return idx;
  }

  function filterEntries(entries, text) {
    const q = text.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter(e => [e.name, e.fixtureType, e.fixtureNumber, e.position, e.notes]
      .some(v => (v || '').toLowerCase().includes(q)));
  }

  function sortEntries(entries, mode) {
    const out = entries.slice();
    const cmpStr = (a, b) => (a || '').localeCompare(b || '');
    switch (mode) {
      case 'name': out.sort((a, b) => cmpStr(a.name, b.name)); break;
      case 'type': out.sort((a, b) => cmpStr(a.fixtureType, b.fixtureType)); break;
      case 'fixtureNumber': out.sort((a, b) => cmpStr(a.fixtureNumber, b.fixtureNumber)); break;
      case 'address':
      default:
        out.sort((a, b) => (a.universe - b.universe) || (a.startAddress - b.startAddress) || cmpStr(a.name, b.name));
    }
    return out;
  }

  // renderEntriesTableBody re-renders ONLY the <tbody> — called from the
  // filter input's oninput and the sort select's onchange so neither loses
  // focus/selection from a full-screen re-render (task ask: "focus+scroll
  // preserved").
  function renderEntriesTableBody() {
    const tbody = document.querySelector('#patchEntriesTable tbody');
    if (!tbody) return;
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const findingsByEntry = indexFindingsByEntry(collisions);
    const filtered = filterEntries(entries, filterText);
    const sorted = sortEntries(filtered, sortMode);
    if (!sorted.length) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty-hint">${entries.length ? 'No entries match the filter.' : 'No entries yet — Add entry, or Adopt the discovered rig.'}</td></tr>`;
      return;
    }
    tbody.innerHTML = sorted.map(e => renderEntryRow(e, findingsByEntry[e.id] || [])).join('');
    tbody.querySelectorAll('[data-edit]').forEach(btn => btn.addEventListener('click', () => openEditEntry(entries.find(x => x.id === btn.dataset.edit))));
    tbody.querySelectorAll('[data-delete]').forEach(btn => btn.addEventListener('click', () => deleteEntry(btn.dataset.delete, entries)));
    tbody.querySelectorAll('[data-move-up]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveUp, -1, entries)));
    tbody.querySelectorAll('[data-move-down]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveDown, 1, entries)));
  }

  function renderEntryRow(e, findings) {
    const issue = findings.length ? `<div class="hint field-error">&#9888; ${findings.map(f => escapeHtml(f.kind)).join(', ')}</div>` : '';
    const match = e.confirmedUid
      ? `<span class="badge yes">confirmed</span>`
      : (e.matchState === 'rejected' ? '<span class="badge no">rejected candidates</span>' : '<span class="hint">unresolved</span>');
    return `
      <tr data-entry-id="${escapeHtml(e.id)}">
        <td>${e.universe}</td>
        <td>${escapeHtml(Api.formatAddressRange(e.startAddress, e.footprint, true))}${issue}</td>
        <td>${escapeHtml(e.name || '—')}</td>
        <td>${escapeHtml(e.fixtureType || '—')}</td>
        <td>${escapeHtml(e.fixtureNumber || '—')}</td>
        <td>${match}</td>
        <td>
          <button data-edit="${escapeHtml(e.id)}">Edit</button>
          <button data-move-up="${escapeHtml(e.id)}" title="Move up">&uarr;</button>
          <button data-move-down="${escapeHtml(e.id)}" title="Move down">&darr;</button>
          <button data-delete="${escapeHtml(e.id)}" class="walk-btn-danger">Delete</button>
        </td>
      </tr>
    `;
  }

  async function deleteEntry(id, entries) {
    const e = entries.find(x => x.id === id);
    if (!confirm(`Delete patch entry "${e ? (e.name || e.fixtureType || id) : id}"? This cannot be undone.`)) return;
    try {
      patchData = await Api.deletePatchEntry(id);
      setStatus('entry deleted');
      collisions = patchData.active ? await Api.getPatchCollisions() : [];
      renderEntriesTableBody();
    } catch (e2) { setStatus('error: ' + e2.message); }
  }

  async function moveEntry(id, delta, entries) {
    const idx = entries.findIndex(x => x.id === id);
    const dest = idx + delta;
    if (idx < 0 || dest < 0 || dest >= entries.length) return;
    const order = entries.map(x => x.id);
    const tmp = order[idx]; order[idx] = order[dest]; order[dest] = tmp;
    try {
      patchData = await Api.reorderPatch(order);
      renderEntriesTableBody();
    } catch (e) { setStatus('error: ' + e.message); }
  }

  // --- entry add/edit form (Apply-to-confirm: oninput mutates entryDraft
  // only; nothing is sent to the server until Save is clicked) -----------

  function openNewEntry() {
    editingEntry = 'new';
    entryDraft = { name: '', fixtureType: '', mode: '', footprint: 0, universe: 0, startAddress: 1, position: '', fixtureNumber: '', notes: '' };
    renderEntryEditor();
  }

  function openEditEntry(e) {
    if (!e) return;
    editingEntry = e.id;
    entryDraft = {
      name: e.name || '', fixtureType: e.fixtureType || '', mode: e.mode || '',
      footprint: e.footprint || 0, universe: e.universe || 0, startAddress: e.startAddress || 1,
      position: e.position || '', fixtureNumber: e.fixtureNumber || '', notes: e.notes || '',
    };
    renderEntryEditor();
  }

  function closeEntryEditor() {
    editingEntry = null;
    entryDraft = null;
    renderEntryEditor();
  }

  function renderEntryEditor() {
    const container = document.getElementById('patchEntryEditor');
    if (!container) return;
    if (editingEntry === null) {
      container.innerHTML = '';
      return;
    }
    const d = entryDraft;
    container.innerHTML = `
      <div class="settings-form patch-entry-form">
        <h4>${editingEntry === 'new' ? 'Add entry' : 'Edit entry'}</h4>
        <label>Name <input id="peName" type="text" maxlength="64"></label>
        <label>Fixture type <input id="peType" type="text" maxlength="80" placeholder="e.g. Chauvet Rogue Outcast 2X Wash"></label>
        <label>Mode / personality <input id="peMode" type="text" maxlength="40"></label>
        <label>Footprint (DMX channels) <input id="peFootprint" type="number" min="0" max="512"></label>
        <label>Universe (Port-Address) <input id="peUniverse" type="number" min="0" max="32767"></label>
        <label>Start address <input id="peAddress" type="number" min="1" max="512"></label>
        <label>Position <input id="pePosition" type="text" maxlength="60" placeholder="e.g. US Truss 3"></label>
        <label>Fixture number <input id="peFixtureNumber" type="text" maxlength="20" placeholder="console channel/FixtureID"></label>
        <label>Notes <input id="peNotes" type="text" maxlength="200"></label>
        <div class="panel-toolbar">
          <button id="peSave">${editingEntry === 'new' ? 'Add entry' : 'Save changes'}</button>
          <button id="peCancel" class="walk-btn-ghost">Cancel</button>
        </div>
        <span class="hint" id="peError"></span>
      </div>
    `;
    const bind = (id, key, isNum) => {
      const el = document.getElementById(id);
      el.value = d[key];
      el.addEventListener('input', () => { d[key] = isNum ? Number(el.value) : el.value; });
    };
    bind('peName', 'name', false);
    bind('peType', 'fixtureType', false);
    bind('peMode', 'mode', false);
    bind('peFootprint', 'footprint', true);
    bind('peUniverse', 'universe', true);
    bind('peAddress', 'startAddress', true);
    bind('pePosition', 'position', false);
    bind('peFixtureNumber', 'fixtureNumber', false);
    bind('peNotes', 'notes', false);

    document.getElementById('peCancel').addEventListener('click', closeEntryEditor);
    document.getElementById('peSave').addEventListener('click', async () => {
      const errEl = document.getElementById('peError');
      if (!d.startAddress || d.startAddress < 1 || d.startAddress > 512) {
        errEl.textContent = 'start address must be 1-512'; return;
      }
      try {
        if (editingEntry === 'new') {
          patchData = await Api.createPatchEntry(d);
          setStatus('entry added');
        } else {
          patchData = await Api.updatePatchEntry(editingEntry, d);
          setStatus('entry saved');
        }
        editingEntry = null; entryDraft = null;
        collisions = patchData.active ? await Api.getPatchCollisions() : [];
        render();
      } catch (e) {
        errEl.textContent = 'error: ' + e.message;
      }
    });
  }

  // ============================================================
  // Reconcile view
  // ============================================================

  const RECONCILE_GROUPS = [
    { status: 'address_mismatch', title: 'Address mismatch — paired, but the device is live at a different address' },
    { status: 'ambiguous', title: 'Ambiguous — multiple candidates, needs your call' },
    { status: 'missing', title: 'Missing — patched, no matching device found' },
    { status: 'unpatched', title: 'Unpatched — device found, not in the patch' },
    { status: 'matched', title: 'Matched' },
  ];

  function renderReconcile(body) {
    if (!reconcile) {
      body.innerHTML = '<p class="hint">Loading reconcile report… (this resolves every device\'s live DMX address, may take a moment on a large rig)</p>';
      return;
    }
    const byStatus = {};
    reconcile.rows.forEach(r => { (byStatus[r.status] = byStatus[r.status] || []).push(r); });
    const entriesById = {};
    if (patchData.active) (patchData.patch.entries || []).forEach(e => { entriesById[e.id] = e; });

    body.innerHTML = `
      <div class="panel-toolbar">
        <button id="btnReconcileRefresh">Refresh</button>
        <button id="btnFixAll">Fix all address mismatches…</button>
        <button id="btnExportReconcileJson">Export JSON</button>
        <button id="btnExportReconcileTxt">Export TXT</button>
        <span class="hint">Generated ${new Date(reconcile.generatedAt).toLocaleTimeString()}</span>
      </div>
      ${RECONCILE_GROUPS.map(g => renderReconcileGroup(g, byStatus[g.status] || [], entriesById)).join('')}
    `;
    document.getElementById('btnReconcileRefresh').addEventListener('click', async () => { reconcile = null; render(); await refreshReconcile(); render(); });
    document.getElementById('btnFixAll').addEventListener('click', onFixAll);
    document.getElementById('btnExportReconcileJson').addEventListener('click', () => window.open(Api.patchReconcileExportUrl('json'), '_blank'));
    document.getElementById('btnExportReconcileTxt').addEventListener('click', () => window.open(Api.patchReconcileExportUrl('txt'), '_blank'));

    wireReconcileRowActions(entriesById);
  }

  function reconcileRowLabel(row, entriesById) {
    if (row.entryId) {
      const e = entriesById[row.entryId];
      return e ? (e.name || e.fixtureType || e.id) : row.entryId;
    }
    return '(unpatched device)';
  }

  function renderEvidence(evidence) {
    if (!evidence || !evidence.length) return '';
    return `<ul class="reconcile-evidence">${evidence.map(ev => `<li>${escapeHtml(ev.kind)}: ${escapeHtml(ev.detail)}</li>`).join('')}</ul>`;
  }

  function renderReconcileGroup(g, rows, entriesById) {
    return `
      <details class="reconcile-group" ${rows.length && g.status !== 'matched' ? 'open' : ''}>
        <summary>${escapeHtml(g.title)} (${rows.length})</summary>
        ${rows.length ? rows.map(r => renderReconcileRow(r, entriesById)).join('') : '<p class="empty-hint">none</p>'}
      </details>
    `;
  }

  function renderReconcileRow(row, entriesById) {
    const label = reconcileRowLabel(row, entriesById);
    const conf = (row.confidence * 100).toFixed(0);
    let actions = '';
    if (row.status === 'address_mismatch' && row.entryId && row.deviceUid) {
      actions = `
        <button data-fix-entry="${escapeHtml(row.entryId)}" data-fix-device="${escapeHtml(row.deviceUid)}">Fix device address</button>
        <button data-reject-entry="${escapeHtml(row.entryId)}" class="walk-btn-ghost">Not this device</button>
      `;
    } else if (row.status === 'ambiguous' && row.entryId) {
      actions = (row.candidates || []).map(c => `
        <div class="reconcile-candidate">
          <span>${escapeHtml(c.deviceUid)} — ${(c.confidence * 100).toFixed(0)}%</span>
          <button data-confirm-entry="${escapeHtml(row.entryId)}" data-confirm-device="${escapeHtml(c.deviceUid)}">This one</button>
          <button data-identify-device="${escapeHtml(c.deviceUid)}">Identify</button>
          ${renderEvidence(c.evidence)}
        </div>
      `).join('') + `<button data-reject-entry="${escapeHtml(row.entryId)}" class="walk-btn-ghost">None of these</button>`;
    }
    return `
      <div class="reconcile-row">
        <div class="reconcile-row-head">
          <strong>${escapeHtml(label)}</strong>
          <span class="hint">${row.deviceUid ? 'device ' + escapeHtml(row.deviceUid) : ''} ${row.confidence ? '· confidence ' + conf + '%' : ''}</span>
        </div>
        ${renderEvidence(row.evidence)}
        <div class="reconcile-row-actions">${actions}</div>
      </div>
    `;
  }

  function wireReconcileRowActions(entriesById) {
    document.querySelectorAll('[data-fix-entry]').forEach(btn => btn.addEventListener('click', async () => {
      const entry = entriesById[btn.dataset.fixEntry];
      const label = entry ? (entry.name || entry.fixtureType || entry.id) : btn.dataset.fixEntry;
      if (!confirm(`Set device ${btn.dataset.fixDevice}'s DMX start address to match "${label}"'s patched address? This sends an RDM SET to the fixture now.`)) return;
      try {
        await Api.reconcileFix(btn.dataset.fixEntry, btn.dataset.fixDevice);
        setStatus('address fixed and pairing confirmed');
        await refreshReconcile();
        render();
      } catch (e) { setStatus('error: ' + e.message); }
    }));
    document.querySelectorAll('[data-confirm-entry]').forEach(btn => btn.addEventListener('click', async () => {
      try {
        await Api.reconcileConfirm(btn.dataset.confirmEntry, btn.dataset.confirmDevice);
        setStatus('pairing confirmed');
        await refreshReconcile();
        render();
      } catch (e) { setStatus('error: ' + e.message); }
    }));
    document.querySelectorAll('[data-reject-entry]').forEach(btn => btn.addEventListener('click', async () => {
      try {
        await Api.reconcileReject(btn.dataset.rejectEntry);
        setStatus('candidates dismissed');
        await refreshReconcile();
        render();
      } catch (e) { setStatus('error: ' + e.message); }
    }));
    document.querySelectorAll('[data-identify-device]').forEach(btn => btn.addEventListener('click', async () => {
      const uid = btn.dataset.identifyDevice;
      try {
        await Api.identify(uid, true);
        setStatus('identify on for ' + uid + ' (auto-off in 5s)');
        setTimeout(() => { Api.identify(uid, false).catch(() => {}); }, 5000);
      } catch (e) { setStatus('error: ' + e.message); }
    }));
  }

  async function onFixAll() {
    let preview;
    try {
      preview = await Api.reconcileFixAll(false);
    } catch (e) { setStatus('error: ' + e.message); return; }
    if (!preview.count) { setStatus('no address mismatches to fix'); return; }
    const lines = preview.items.map(i => `  ${i.entryName}: ${i.from || '—'} → ${i.to}`).join('\n');
    if (!confirm(`Fix ${preview.count} device address(es) to match the patch?\n\n${lines}\n\nThis sends an RDM SET to each device now.`)) return;
    try {
      const result = await Api.reconcileFixAll(true);
      setStatus(`fixed ${result.applied} of ${result.count}`);
      await refreshReconcile();
      render();
    } catch (e) { setStatus('error: ' + e.message); }
  }

  // ============================================================
  // Rig Check view
  // ============================================================

  function renderRigCheck(body) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const st = rigCheckState || { running: false, mode: rcMode, level: rcLevel };

    body.innerHTML = `
      <div class="panel-toolbar">
        <label>Scope: <select id="rcScopeKind">
          <option value="all">Whole patch</option>
          <option value="universe">One universe</option>
          <option value="selection">Selection</option>
        </select></label>
        <div id="rcScopeValueWrap"></div>
      </div>
      <div class="panel-toolbar">
        <label>Mode: <select id="rcMode">
          <option value="highlight">Highlight (this fixture up, rest dark)</option>
          <option value="all_channels">All channels to level</option>
          <option value="step_channel">Step one channel</option>
        </select></label>
        <label>Level: <input type="range" id="rcLevel" min="0" max="255" value="${st.level || rcLevel}"> <span id="rcLevelVal">${st.level || rcLevel}</span></label>
        ${st.running
        ? '<button id="rcStop" class="walk-btn-danger">Stop</button>'
        : `<button id="rcStart" class="walk-btn-primary" ${rcStarting ? 'disabled' : ''}>${rcStarting ? 'Starting…' : 'Start'}</button>`}
        <button id="rcBlackout" class="walk-btn-danger">Blackout</button>
      </div>
      <p class="hint">Safety: stopping, leaving this screen, or closing the tab always blacks out and stops output.</p>
      <div id="rcActiveWrap">${st.running ? renderRigCheckActive(st) : '<p class="hint">Not running. Choose a scope and mode, then Start.</p>'}</div>
    `;

    const scopeKindSel = document.getElementById('rcScopeKind');
    scopeKindSel.value = rcScopeKind;
    document.getElementById('rcMode').value = rcMode;
    renderRigCheckScopeValue(entries);

    scopeKindSel.addEventListener('change', (e) => {
      rcScopeKind = e.target.value;
      sessionStorage.setItem('benny512.patch.rcScopeKind', rcScopeKind);
      renderRigCheckScopeValue(entries);
    });
    document.getElementById('rcMode').addEventListener('change', async (e) => {
      rcMode = e.target.value;
      sessionStorage.setItem('benny512.patch.rcMode', rcMode);
      if (st.running) {
        try { rigCheckState = await Api.rigCheckMode(rcMode); render(); } catch (err) { setStatus('error: ' + err.message); }
      }
    });
    const levelInput = document.getElementById('rcLevel');
    levelInput.addEventListener('input', (e) => {
      rcLevel = Number(e.target.value);
      document.getElementById('rcLevelVal').textContent = String(rcLevel);
    });
    levelInput.addEventListener('change', async () => {
      if (st.running) {
        try { rigCheckState = await Api.rigCheckLevel(rcLevel); renderRigCheckActiveInPlace(); } catch (e) { setStatus('error: ' + e.message); }
      }
    });

    const startBtn = document.getElementById('rcStart');
    if (startBtn) startBtn.addEventListener('click', onRigCheckStart);
    const stopBtn = document.getElementById('rcStop');
    if (stopBtn) stopBtn.addEventListener('click', async () => {
      try { rigCheckState = await Api.rigCheckStop(); render(); } catch (e) { setStatus('error: ' + e.message); }
    });
    document.getElementById('rcBlackout').addEventListener('click', async () => {
      try { await Api.rigCheckBlackout(); setStatus('blackout'); } catch (e) { setStatus('error: ' + e.message); }
    });

    if (st.running) wireRigCheckActiveHandlers();
  }

  function renderRigCheckScopeValue(entries) {
    const wrap = document.getElementById('rcScopeValueWrap');
    if (!wrap) return;
    if (rcScopeKind === 'universe') {
      wrap.innerHTML = `<label>Universe (Port-Address) <input type="number" id="rcScopeUniverse" min="0" max="32767" value="${rcScopeUniverse}"></label>`;
      document.getElementById('rcScopeUniverse').addEventListener('input', (e) => { rcScopeUniverse = Number(e.target.value); });
    } else if (rcScopeKind === 'selection') {
      wrap.innerHTML = `<div class="rc-selection-list">${entries.map(e => `
        <label class="rc-selection-item"><input type="checkbox" data-rc-select="${escapeHtml(e.id)}" ${rcSelection[e.id] ? 'checked' : ''}> ${escapeHtml(e.name || e.fixtureType || e.id)} (U${e.universe}/${e.startAddress})</label>
      `).join('') || '<span class="hint">no entries</span>'}</div>`;
      wrap.querySelectorAll('[data-rc-select]').forEach(cb => cb.addEventListener('change', (e) => {
        rcSelection[cb.dataset.rcSelect] = e.target.checked;
      }));
    } else {
      wrap.innerHTML = '';
    }
  }

  async function onRigCheckStart() {
    const body = { scopeKind: rcScopeKind, mode: rcMode, level: rcLevel };
    if (rcScopeKind === 'universe') body.universe = rcScopeUniverse;
    if (rcScopeKind === 'selection') body.entryIds = Object.keys(rcSelection).filter(id => rcSelection[id]);
    rcStarting = true;
    render();
    try {
      rigCheckState = await Api.rigCheckStart(body);
      rcStarting = false;
      render();
    } catch (e) {
      rcStarting = false;
      setStatus('error: ' + e.message);
      render();
    }
  }

  function renderRigCheckActive(st) {
    const chInfo = (st.mode === 'step_channel' && st.currentChannel)
      ? ` &middot; channel ${st.currentChannel} (offset ${st.channelOffset})`
      : '';
    return `
      <div class="walk-card">
        <div class="walk-counter">${st.entryIndex + 1} of ${st.entryCount}</div>
        <div class="walk-model">${escapeHtml(st.currentEntryName || '—')}</div>
        <div class="hint">mode: ${escapeHtml(st.mode)} &middot; level: ${st.level}${chInfo}</div>
      </div>
      <div class="walk-navbar">
        <button id="rcPrev" ${st.entryIndex <= 0 ? 'disabled' : ''}>&larr; Previous</button>
        <button id="rcNext" ${st.entryIndex >= st.entryCount - 1 ? 'disabled' : ''}>Next &rarr;</button>
      </div>
      ${st.mode === 'step_channel' ? `
      <div class="panel-toolbar">
        <button id="rcChPrev">&larr; Prev channel</button>
        <button id="rcChNext">Next channel &rarr;</button>
      </div>` : ''}
    `;
  }

  function renderRigCheckActiveInPlace() {
    const wrap = document.getElementById('rcActiveWrap');
    if (!wrap || !rigCheckState || !rigCheckState.running) return;
    wrap.innerHTML = renderRigCheckActive(rigCheckState);
    wireRigCheckActiveHandlers();
  }

  function wireRigCheckActiveHandlers() {
    const prev = document.getElementById('rcPrev');
    const next = document.getElementById('rcNext');
    if (prev) prev.addEventListener('click', async () => { try { rigCheckState = await Api.rigCheckPrevious(); renderRigCheckActiveInPlace(); } catch (e) { setStatus('error: ' + e.message); } });
    if (next) next.addEventListener('click', async () => { try { rigCheckState = await Api.rigCheckNext(); renderRigCheckActiveInPlace(); } catch (e) { setStatus('error: ' + e.message); } });
    const chPrev = document.getElementById('rcChPrev');
    const chNext = document.getElementById('rcChNext');
    if (chPrev) chPrev.addEventListener('click', async () => { try { rigCheckState = await Api.rigCheckChannel(-1); renderRigCheckActiveInPlace(); } catch (e) { setStatus('error: ' + e.message); } });
    if (chNext) chNext.addEventListener('click', async () => { try { rigCheckState = await Api.rigCheckChannel(1); renderRigCheckActiveInPlace(); } catch (e) { setStatus('error: ' + e.message); } });
  }

  return { init, onEnterScreen, onLeaveScreen };
})();
