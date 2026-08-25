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

  // severityBadge maps a collision/finding severity string onto the
  // status-badge vocabulary — text is always shown too (never color alone).
  function severityBadge(sev) {
    const s = (sev || '').toLowerCase();
    const kind = ['ok', 'warning', 'error', 'pending', 'stale', 'unknown'].includes(s) ? s : 'warning';
    return UI.badge(kind, (sev || '').toUpperCase());
  }

  // --- root render --------------------------------------------------------

  function render() {
    const el = document.getElementById('patchRoot');
    if (!el) return;
    el.innerHTML = `
      <div class="b5-tabs">
        <div class="b5-tabs__list" id="patchViewTabs">
          <button class="b5-tabs__tab detail-tab-btn" data-view="entries">Entries</button>
          <button class="b5-tabs__tab detail-tab-btn" data-view="reconcile">Reconcile</button>
          <button class="b5-tabs__tab detail-tab-btn" data-view="rigcheck">Rig Check</button>
        </div>
      </div>
      <span class="b5-text-muted b5-text-sm" id="patchStatusMsg">${escapeHtml(statusMsg)}</span>
      <div id="patchViewBody" style="margin-top:var(--b5-space-4)"></div>
    `;
    document.querySelectorAll('#patchViewTabs .detail-tab-btn').forEach(b => {
      b.classList.toggle('is-active', b.dataset.view === view);
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
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <button id="btnNewPatch" class="b5-btn b5-btn--sm">New patch…</button>
          <button id="btnAddEntry" class="b5-btn b5-btn--sm b5-btn--primary">Add entry</button>
          <button id="btnAdoptMerge" class="b5-btn b5-btn--sm">Adopt discovered (merge)</button>
          <button id="btnAdoptFresh" class="b5-btn b5-btn--sm">Adopt discovered (replace patch)</button>
        </div>
        <span class="b5-filterbar__summary">${p ? escapeHtml(p.name || '(unnamed)') + ' — ' + entries.length + ' entr' + (entries.length === 1 ? 'y' : 'ies') : 'No patch yet — add an entry or adopt the discovered rig to start one.'}</span>
      </div>
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden" for="patchFilter">Filter</label>
          <input type="text" id="patchFilter" class="b5-input" style="width:16em" placeholder="Filter: name, type, fixture #…" value="${escapeHtml(filterText)}">
          <label class="b5-visually-hidden" for="patchSort">Sort</label>
          <select id="patchSort" class="b5-select" style="width:auto">
            <option value="address">Sort: Universe + address</option>
            <option value="name">Sort: Name</option>
            <option value="type">Sort: Fixture type</option>
            <option value="fixtureNumber">Sort: Fixture number</option>
          </select>
          <button id="btnExportPatchJson" class="b5-btn b5-btn--sm">${UI.icon('export')}Export JSON</button>
          <button id="btnExportPatchTxt" class="b5-btn b5-btn--sm">${UI.icon('export')}Export TXT</button>
        </div>
      </div>
      ${renderCollisionBanner(collisions)}
      <div class="b5-panel">
        <div class="b5-panel__body--flush">
          <table class="b5-table b5-table--responsive" id="patchEntriesTable">
            <thead><tr>
              <th>Universe</th><th>Address</th><th>Name</th><th>Fixture type</th><th>Fixture #</th><th>RDM match</th><th>Actions</th>
            </tr></thead>
            <tbody></tbody>
          </table>
        </div>
      </div>
      <div id="patchEntryEditor" style="margin-top:var(--b5-space-4)"></div>
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
      <div class="b5-alert b5-alert--caution" style="margin-bottom:var(--b5-space-4)">
        ${UI.icon('status-warning')}
        <div>
          <p class="b5-alert__title">${findings.length} patch issue${findings.length === 1 ? '' : 's'} found</p>
          <ul style="margin:var(--b5-space-2) 0 0; padding-left:1.2em">
            ${findings.map(f => `<li class="b5-text-sm">${severityBadge(f.severity)} ${escapeHtml(f.message)}</li>`).join('')}
          </ul>
        </div>
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
      tbody.innerHTML = `<tr><td colspan="7"><div class="b5-empty">${UI.icon('nav-devices')}<span class="b5-empty__title">${entries.length ? 'No entries match the filter' : 'No entries yet'}</span><span class="b5-empty__body">${entries.length ? 'Try a different filter.' : 'Add entry, or Adopt the discovered rig.'}</span></div></td></tr>`;
      return;
    }
    tbody.innerHTML = sorted.map(e => renderEntryRow(e, findingsByEntry[e.id] || [])).join('');
    tbody.querySelectorAll('[data-edit]').forEach(btn => btn.addEventListener('click', () => openEditEntry(entries.find(x => x.id === btn.dataset.edit))));
    tbody.querySelectorAll('[data-delete]').forEach(btn => btn.addEventListener('click', () => deleteEntry(btn.dataset.delete, entries)));
    tbody.querySelectorAll('[data-move-up]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveUp, -1, entries)));
    tbody.querySelectorAll('[data-move-down]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveDown, 1, entries)));
  }

  function renderEntryRow(e, findings) {
    const issue = findings.length ? `<div class="b5-field__error">${UI.icon('status-warning')}${findings.map(f => escapeHtml(f.kind)).join(', ')}</div>` : '';
    const match = e.confirmedUid
      ? UI.badge('ok', 'Confirmed')
      : (e.matchState === 'rejected' ? UI.badge('warning', 'Rejected candidates') : '<span class="b5-text-muted b5-text-sm">Unresolved</span>');
    return `
      <tr data-entry-id="${escapeHtml(e.id)}">
        <td data-label="Universe">${e.universe}</td>
        <td data-label="Address" class="b5-table__mono">${escapeHtml(Api.formatAddressRange(e.startAddress, e.footprint, true))}${issue}</td>
        <td data-label="Name">${escapeHtml(e.name || '—')}</td>
        <td data-label="Fixture type">${escapeHtml(e.fixtureType || '—')}</td>
        <td data-label="Fixture #">${escapeHtml(e.fixtureNumber || '—')}</td>
        <td data-label="RDM match">${match}</td>
        <td data-label="Actions">
          <button class="b5-btn b5-btn--sm" data-edit="${escapeHtml(e.id)}">Edit</button>
          <button class="b5-btn b5-btn--sm b5-btn--icon" data-move-up="${escapeHtml(e.id)}" aria-label="Move up">&uarr;</button>
          <button class="b5-btn b5-btn--sm b5-btn--icon" data-move-down="${escapeHtml(e.id)}" aria-label="Move down">&darr;</button>
          <button class="b5-btn b5-btn--sm b5-btn--danger" data-delete="${escapeHtml(e.id)}">Delete</button>
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
      <div class="b5-panel">
        <div class="b5-panel__header"><h3 class="b5-panel__title">${editingEntry === 'new' ? 'Add entry' : 'Edit entry'}</h3></div>
        <div class="b5-panel__body b5-grid-2">
          <div class="b5-field"><label class="b5-field__label" for="peName">Name</label><input id="peName" class="b5-input" type="text" maxlength="64"></div>
          <div class="b5-field"><label class="b5-field__label" for="peType">Fixture type</label><input id="peType" class="b5-input" type="text" maxlength="80" placeholder="e.g. Chauvet Rogue Outcast 2X Wash"></div>
          <div class="b5-field"><label class="b5-field__label" for="peMode">Mode / personality</label><input id="peMode" class="b5-input" type="text" maxlength="40"></div>
          <div class="b5-field"><label class="b5-field__label" for="peFootprint">Footprint (DMX channels)</label><input id="peFootprint" class="b5-input" type="number" min="0" max="512"></div>
          <div class="b5-field"><label class="b5-field__label" for="peUniverse">Universe (Port-Address)</label><input id="peUniverse" class="b5-input" type="number" min="0" max="32767"></div>
          <div class="b5-field"><label class="b5-field__label" for="peAddress">Start address</label><input id="peAddress" class="b5-input" type="number" min="1" max="512"></div>
          <div class="b5-field"><label class="b5-field__label" for="pePosition">Position</label><input id="pePosition" class="b5-input" type="text" maxlength="60" placeholder="e.g. US Truss 3"></div>
          <div class="b5-field"><label class="b5-field__label" for="peFixtureNumber">Fixture number</label><input id="peFixtureNumber" class="b5-input" type="text" maxlength="20" placeholder="console channel/FixtureID"></div>
          <div class="b5-field" style="grid-column:1/-1"><label class="b5-field__label" for="peNotes">Notes</label><input id="peNotes" class="b5-input" type="text" maxlength="200"></div>
        </div>
        <div class="b5-panel__body" style="padding-top:0">
          <div class="b5-row">
            <button id="peSave" class="b5-btn b5-btn--primary">${UI.icon('apply')}${editingEntry === 'new' ? 'Add entry' : 'Save changes'}</button>
            <button id="peCancel" class="b5-btn b5-btn--ghost">Cancel</button>
          </div>
          <span class="b5-field__error" id="peError"></span>
        </div>
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
        errEl.innerHTML = UI.icon('status-error') + 'start address must be 1-512'; return;
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
        errEl.innerHTML = UI.icon('status-error') + ('error: ' + escapeHtml(e.message));
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
      body.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading reconcile report… (this resolves every device's live DMX address, may take a moment on a large rig)</span>`;
      return;
    }
    const byStatus = {};
    reconcile.rows.forEach(r => { (byStatus[r.status] = byStatus[r.status] || []).push(r); });
    const entriesById = {};
    if (patchData.active) (patchData.patch.entries || []).forEach(e => { entriesById[e.id] = e; });

    body.innerHTML = `
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <button id="btnReconcileRefresh" class="b5-btn b5-btn--sm">${UI.icon('refresh')}Refresh</button>
          <button id="btnFixAll" class="b5-btn b5-btn--sm b5-btn--primary">Fix all address mismatches…</button>
          <button id="btnExportReconcileJson" class="b5-btn b5-btn--sm">${UI.icon('export')}Export JSON</button>
          <button id="btnExportReconcileTxt" class="b5-btn b5-btn--sm">${UI.icon('export')}Export TXT</button>
        </div>
        <span class="b5-filterbar__summary">Generated ${new Date(reconcile.generatedAt).toLocaleTimeString()}</span>
      </div>
      <div class="b5-accordion">
        ${RECONCILE_GROUPS.map(g => renderReconcileGroup(g, byStatus[g.status] || [], entriesById)).join('')}
      </div>
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
    return `<ul class="b5-text-muted b5-text-xs" style="margin:var(--b5-space-1) 0 0; padding-left:1.2em">${evidence.map(ev => `<li>${escapeHtml(ev.kind)}: ${escapeHtml(ev.detail)}</li>`).join('')}</ul>`;
  }

  // Reconcile groups use native <details>/<summary> — free open/closed
  // state with built-in keyboard + AT support — styled as a
  // .b5-accordion__item via app.css's marker-suppression rule; group
  // open-by-default logic (findings needing attention default open,
  // "matched" stays collapsed) is unchanged from before the retheme.
  function renderReconcileGroup(g, rows, entriesById) {
    return `
      <details class="b5-accordion__item" ${rows.length && g.status !== 'matched' ? 'open' : ''}>
        <summary class="b5-accordion__trigger"><span>${escapeHtml(g.title)} (${rows.length})</span>${UI.icon('chevron-expand')}</summary>
        <div class="b5-accordion__panel b5-stack">
          ${rows.length ? rows.map(r => renderReconcileRow(r, entriesById)).join('') : '<p class="b5-text-muted b5-text-sm">none</p>'}
        </div>
      </details>
    `;
  }

  function renderReconcileRow(row, entriesById) {
    const label = reconcileRowLabel(row, entriesById);
    const conf = (row.confidence * 100).toFixed(0);
    let actions = '';
    if (row.status === 'address_mismatch' && row.entryId && row.deviceUid) {
      actions = `
        <button class="b5-btn b5-btn--sm b5-btn--primary" data-fix-entry="${escapeHtml(row.entryId)}" data-fix-device="${escapeHtml(row.deviceUid)}">Fix device address</button>
        <button class="b5-btn b5-btn--sm b5-btn--ghost" data-reject-entry="${escapeHtml(row.entryId)}">Not this device</button>
      `;
    } else if (row.status === 'ambiguous' && row.entryId) {
      actions = (row.candidates || []).map(c => `
        <div class="b5-card" style="margin-top:var(--b5-space-2)">
          <div class="b5-row" style="justify-content:space-between">
            <span class="b5-text-mono b5-text-sm">${escapeHtml(c.deviceUid)} — ${(c.confidence * 100).toFixed(0)}%</span>
            <span class="b5-row">
              <button class="b5-btn b5-btn--sm b5-btn--primary" data-confirm-entry="${escapeHtml(row.entryId)}" data-confirm-device="${escapeHtml(c.deviceUid)}">This one</button>
              <button class="b5-btn b5-btn--sm" data-identify-device="${escapeHtml(c.deviceUid)}">${UI.icon('identify')}Identify</button>
            </span>
          </div>
          ${renderEvidence(c.evidence)}
        </div>
      `).join('') + `<button class="b5-btn b5-btn--sm b5-btn--ghost" style="margin-top:var(--b5-space-2)" data-reject-entry="${escapeHtml(row.entryId)}">None of these</button>`;
    }
    return `
      <div class="b5-card">
        <div class="b5-row" style="justify-content:space-between">
          <strong class="b5-text-sm">${escapeHtml(label)}</strong>
          <span class="b5-text-muted b5-text-xs">${row.deviceUid ? 'device ' + escapeHtml(row.deviceUid) : ''} ${row.confidence ? '· confidence ' + conf + '%' : ''}</span>
        </div>
        ${renderEvidence(row.evidence)}
        <div class="b5-row" style="margin-top:var(--b5-space-2)">${actions}</div>
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
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden" for="rcScopeKind">Scope</label>
          <select id="rcScopeKind" class="b5-select" style="width:auto">
            <option value="all">Scope: Whole patch</option>
            <option value="universe">Scope: One universe</option>
            <option value="selection">Scope: Selection</option>
          </select>
          <div id="rcScopeValueWrap"></div>
        </div>
      </div>
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden" for="rcMode">Mode</label>
          <select id="rcMode" class="b5-select" style="width:auto">
            <option value="highlight">Mode: Highlight (this fixture up, rest dark)</option>
            <option value="all_channels">Mode: All channels to level</option>
            <option value="step_channel">Mode: Step one channel</option>
          </select>
          <label class="b5-text-sm" style="display:flex;align-items:center;gap:8px">Level
            <input type="range" id="rcLevel" class="b5-range-touch" min="0" max="255" value="${st.level || rcLevel}">
            <span class="b5-text-mono" id="rcLevelVal">${st.level || rcLevel}</span>
          </label>
          ${st.running
        ? '<button id="rcStop" class="b5-btn b5-btn--sm b5-btn--danger">Stop</button>'
        : `<button id="rcStart" class="b5-btn b5-btn--sm b5-btn--primary" ${rcStarting ? 'disabled' : ''}>${rcStarting ? UI.spinner() + 'Starting…' : 'Start'}</button>`}
          <button id="rcBlackout" class="b5-btn b5-btn--sm b5-btn--danger">Blackout</button>
        </div>
      </div>
      <div class="b5-alert b5-alert--info" style="margin-bottom:var(--b5-space-4)">
        ${UI.icon('status-pending')}
        <div><p class="b5-alert__body">Safety: stopping, leaving this screen, or closing the tab always blacks out and stops output.</p></div>
      </div>
      <div id="rcActiveWrap">${st.running ? renderRigCheckActive(st) : `<div class="b5-empty">${UI.icon('status-pending')}<span class="b5-empty__title">Not running</span><span class="b5-empty__body">Choose a scope and mode, then Start.</span></div>`}</div>
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
      wrap.innerHTML = `<label class="b5-visually-hidden" for="rcScopeUniverse">Universe</label><input type="number" id="rcScopeUniverse" class="b5-input" style="width:10em" min="0" max="32767" value="${rcScopeUniverse}" placeholder="Universe">`;
      document.getElementById('rcScopeUniverse').addEventListener('input', (e) => { rcScopeUniverse = Number(e.target.value); });
    } else if (rcScopeKind === 'selection') {
      wrap.innerHTML = `<div class="b5-stack" style="margin-top:var(--b5-space-2)">${entries.map(e => `
        <label class="b5-checkbox"><input type="checkbox" data-rc-select="${escapeHtml(e.id)}" ${rcSelection[e.id] ? 'checked' : ''}>${escapeHtml(e.name || e.fixtureType || e.id)} (U${e.universe}/${e.startAddress})</label>
      `).join('') || '<span class="b5-text-muted b5-text-sm">no entries</span>'}</div>`;
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
      <div class="b5-card" style="text-align:center">
        <div class="b5-counter">
          <div class="b5-counter__value">${st.entryIndex + 1} of ${st.entryCount}</div>
          <div class="b5-counter__label">${escapeHtml(st.currentEntryName || '—')}</div>
        </div>
        <span class="b5-text-muted b5-text-sm">mode: ${escapeHtml(st.mode)} &middot; level: ${st.level}${chInfo}</span>
      </div>
      <div class="b5-row" style="margin-top:var(--b5-space-4);justify-content:center">
        <button id="rcPrev" class="b5-btn b5-btn--walk b5-btn--secondary" ${st.entryIndex <= 0 ? 'disabled' : ''}>&larr; Previous</button>
        <button id="rcNext" class="b5-btn b5-btn--walk b5-btn--primary" ${st.entryIndex >= st.entryCount - 1 ? 'disabled' : ''}>Next &rarr;</button>
      </div>
      ${st.mode === 'step_channel' ? `
      <div class="b5-row" style="margin-top:var(--b5-space-3);justify-content:center">
        <button id="rcChPrev" class="b5-btn b5-btn--sm">&larr; Prev channel</button>
        <button id="rcChNext" class="b5-btn b5-btn--sm">Next channel &rarr;</button>
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
