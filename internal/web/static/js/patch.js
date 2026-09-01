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
  // collisions: findings from GET /api/patch/collisions. Every fetch of it
  // below is guarded with `|| []` — found during Phase C render-proofing
  // against a real ~20MB show file: internal/patch.DetectCollisions
  // (internal/patch/collision.go) used to declare `var findings []Finding`
  // and never initialize it when a patch had zero findings, so
  // encoding/json marshalled that nil slice as JSON `null` rather than
  // `[]` (a plain `[]Finding` var is nil until the first append). The
  // client then crashed on the very next render (renderCollisionBanner did
  // `findings.length` on `null`) for ANY clean patch, MVR-imported or
  // not. The root cause is now fixed server-side (collision.go builds
  // findings with `make([]Finding, 0)`, backed by a test asserting the
  // marshalled JSON is `[]`, not just len==0) — these `|| []` guards stay
  // as defense-in-depth, not because the server bug is still open.
  // Not the same bug as the Task-1 universe off-by-one.
  let collisions = [];
  let reconcile = null;
  let rigCheckState = null;
  let statusMsg = '';

  // Entries table state.
  let filterText = '';
  let sortMode = 'address';
  let editingEntry = null; // null | 'new' | entry id being edited
  let entryDraft = null;

  // Multi-select state (task ask, item 4): entryId -> true for every
  // checked row. Independent of editingEntry — selecting rows never
  // disturbs a single-entry edit in progress except that once 2+ rows are
  // selected the floating editor's bulk-edit mode takes over the panel
  // (single-edit resumes automatically once selection drops back below 2).
  // lastClickedEntryId anchors shift-click range selection against
  // whatever order the table is CURRENTLY sorted/filtered into (task ask:
  // "shift-click range selection if cheap").
  let selectedIds = {};
  let lastClickedEntryId = null;
  let bulkDraft = { universe: '', position: '' };
  let bulkApplying = false;

  function selectedCount() { return Object.keys(selectedIds).filter(id => selectedIds[id]).length; }
  function clearSelection() { selectedIds = {}; lastClickedEntryId = null; }

  // MVR import state (Apply-to-confirm: parsing a file only builds a
  // preview; nothing is sent to the server until the preview's own
  // "Import (merge/replace patch)" button is clicked — same contract as
  // runAdopt's confirm() dialog, just richer since an import preview has a
  // fixture count and a warning list to show rather than one line of text).
  let mvrFileInput = null;
  let mvrPreview = null; // null | { fileName, entries, warnings }
  let mvrImporting = false;

  // Single-GDTF import state (task ask: "import a single .gdtf file and
  // match it to a fixture type" — the motivating case is a GDTFSpec an MVR
  // referenced but didn't embed, supplied by hand afterward so the entries
  // that landed at footprint 0 can pick up their real mode/footprint).
  // Apply-to-confirm: parsing only builds a preview; nothing is written
  // until the preview's own "Apply" button is clicked, same contract as
  // the MVR import preview above.
  let gdtfFileInput = null;
  let gdtfPreview = null; // null | { fileName, parsed, selectedModeIndex, matches }
  let gdtfApplying = false;

  // Rig check setup state.
  let rcScopeKind = sessionStorage.getItem('benny512.patch.rcScopeKind') || 'all';
  let rcScopeUniverse = 0;
  let rcSelection = {}; // entryId -> bool
  let rcMode = sessionStorage.getItem('benny512.patch.rcMode') || 'highlight';
  let rcLevel = 255;
  let rcStarting = false;

  // Function Check (Phase 2b) state — the attribute-group pattern engine,
  // additive alongside the classic per-entry rig check above (task ask:
  // "keep today's channel-level rig check working and reachable"). A
  // separate sub-tab (rcSubView) inside the Rig Check screen so neither
  // mode's state/DOM interferes with the other's.
  let rcSubView = sessionStorage.getItem('benny512.patch.rcSubView') || 'classic'; // 'classic' | 'function'
  let fcScopeKind = sessionStorage.getItem('benny512.patch.fcScopeKind') || 'all'; // all|universe|position|selection
  let fcScopeUniverse = 0;
  let fcScopePosition = '';
  let fcSelection = {}; // entryId -> bool, scopeKind 'selection'
  let fcGroup = sessionStorage.getItem('benny512.patch.fcGroup') || 'dimmer';
  // fcKindByGroup: last-chosen pattern kind per group, so switching groups
  // and back doesn't lose the pick.
  let fcKindByGroup = { dimmer: 'dimmer_sine', position: 'ballyhoo', colour: 'colour_wheel_step', beam: 'frost', focus: 'manual_value', shaper: 'shaper_individual' };
  // fcParams: the draft tunables for the NEXT start (or the next adjust of
  // a running pattern) — oninput mutates this only, per the file's
  // standing oninput/onchange contract; onchange (or the explicit Start/
  // Apply click) is what actually calls the server.
  let fcParams = { rateHz: 0.5, min: 0, max: 255, target: '', direction: 'cw', value: 128 };
  let fcStarting = false;
  let fcAttrData = null; // last GET /api/patch/attributes result, scoped to the current scope's entry ids
  let fcAttrLoading = false;
  // patternState: last known GET/POST .../pattern response. null until the
  // Function Check sub-tab has loaded once.
  let patternState = null;
  let patternPollTimer = null;

  // --- lifecycle ------------------------------------------------------------

  function init() {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden' && active) {
        Api.rigCheckStopBeacon();
        stopPatternHeartbeat();
      }
    });
    window.addEventListener('pagehide', () => {
      if (active) {
        Api.rigCheckStopBeacon();
        stopPatternHeartbeat();
      }
    });
    ensureMvrFileInput();
    ensureGdtfFileInput();
    // Universe base changed on the Settings screen — re-render every
    // universe number currently on screen (notation only, no data refetch
    // needed: patchData already holds the true 0-based values).
    window.addEventListener('b5-universe-base-changed', () => { if (active) render(); });
  }

  // ensureMvrFileInput: created once and kept off-screen (never rebuilt on
  // re-render, so it survives renderEntries() replacing patchViewBody's
  // innerHTML) — clicked programmatically by the "Import MVR…" toolbar
  // button. display:none is fine; Playwright/browsers still allow
  // setInputFiles on a hidden <input type=file>.
  function ensureMvrFileInput() {
    if (mvrFileInput) return mvrFileInput;
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.mvr';
    input.id = 'mvrFileInput';
    input.style.display = 'none';
    input.addEventListener('change', onMvrFileChosen);
    document.body.appendChild(input);
    mvrFileInput = input;
    return input;
  }

  async function onMvrFileChosen(e) {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    try {
      const buf = await file.arrayBuffer();
      const result = await MvrImport.parseMvrFile(buf);
      mvrPreview = { fileName: file.name, entries: result.entries, warnings: result.warnings };
      setStatus(`parsed ${result.entries.length} fixture(s) from ${file.name}`);
      render();
    } catch (err) {
      setStatus('error: ' + err.message);
    } finally {
      // Reset so choosing the exact same file again still fires 'change'.
      e.target.value = '';
    }
  }

  // ensureGdtfFileInput: same pattern as ensureMvrFileInput above — created
  // once, kept off-screen, survives re-renders, clicked programmatically by
  // the "Import GDTF…" toolbar button.
  function ensureGdtfFileInput() {
    if (gdtfFileInput) return gdtfFileInput;
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.gdtf';
    input.id = 'gdtfFileInput';
    input.style.display = 'none';
    input.addEventListener('change', onGdtfFileChosen);
    document.body.appendChild(input);
    gdtfFileInput = input;
    return input;
  }

  async function onGdtfFileChosen(e) {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    try {
      const buf = await file.arrayBuffer();
      const parsed = await MvrImport.parseGdtfFile(buf);
      const candidateGroups = computeGdtfMatchGroups(parsed);
      gdtfPreview = {
        fileName: file.name, parsed, selectedModeIndex: 0,
        candidateGroups,
        // Auto-selected only when there's exactly one distinct matching
        // fixture-type group — 0 or 2+ always require an explicit choice
        // (0: nothing to choose; 2+: ambiguous, never auto-picked — see
        // computeGdtfMatchGroups' doc comment).
        selectedGroupIndex: candidateGroups.length === 1 ? 0 : null,
      };
      const totalMatches = candidateGroups.reduce((n, g) => n + g.entries.length, 0);
      const warnSuffix = (parsed.warnings || []).length ? ` — ${parsed.warnings.length} geometry warning${parsed.warnings.length === 1 ? '' : 's'}` : '';
      setStatus(`parsed "${parsed.fixtureType || '(unnamed fixture type)'}" from ${file.name} — ` +
        `${parsed.modes.length} mode${parsed.modes.length === 1 ? '' : 's'}, ${totalMatches} matching patch entr${totalMatches === 1 ? 'y' : 'ies'}` +
        `${candidateGroups.length > 1 ? ` across ${candidateGroups.length} distinct fixture types — pick one below` : ''}${warnSuffix}`);
      render();
    } catch (err) {
      gdtfPreview = null;
      setStatus('error: ' + err.message);
      render();
    } finally {
      e.target.value = '';
    }
  }

  // --- GDTF <-> patch fixture-type matching (Bug 2: "let's make it so GDTF
  // imports match fixtures in the patch more easily") ---------------------
  //
  // internal/patch/match.go already has an asymmetric token-containment
  // fuzzy matcher for patch<->RDM reconcile (normalizeTokens +
  // tokenContainment there). This ports that SAME design to the browser
  // rather than inventing a second, divergent notion of "similar name" —
  // Go code isn't callable from this client-side import flow, so it's a
  // deliberate hand-port, not a shared import, but the algorithm is
  // identical: lowercase, treat every run of non-alphanumeric characters as
  // a separator (so case/spacing/`-`/`_`/`.` never matter), and compare by
  // token-set containment rather than raw string equality.
  //
  // One addition beyond match.go's normalizeTokens, needed for this task's
  // examples ("JDC1" == "JDC 1" == "JDC-1", "ERA800" == "ERA 800"): split
  // at every letter<->digit boundary too, so "jdc1" tokenizes to "jdc","1"
  // the same as "jdc 1" does. match.go's own normalizeTokens has been
  // extended with the identical split (see that file) so RDM-reconcile
  // fuzzy matching benefits from the same leniency, keeping the two
  // matchers' notion of "same token" in sync even though they're separate
  // implementations.
  function fuzzyTokenSet(s) {
    const norm = String(s || '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, ' ')
      .replace(/([a-z])(\d)/g, '$1 $2')
      .replace(/(\d)([a-z])/g, '$1 $2')
      .trim();
    return norm === '' ? new Set() : new Set(norm.split(/\s+/));
  }

  // tokenContainment: fraction of ref's tokens that also appear in text —
  // same asymmetric definition as match.go's tokenContainment (a superset
  // side isn't penalized for extra words the other side lacks).
  function tokenContainment(text, ref) {
    if (ref.size === 0) return 0;
    let hit = 0;
    ref.forEach(t => { if (text.has(t)) hit++; });
    return hit / ref.size;
  }

  // MIN_SUBSET_TOKENS: a subset match additionally requires the shorter
  // side to carry at least this many tokens, so a single generic word
  // ("Strobe") can never by itself count as "the same fixture" — the
  // critical design constraint from the task brief: a loose match that
  // silently picks the WRONG fixture type is far worse than a miss (wrong
  // footprint + wrong channel map applied to a real fixture).
  const MIN_SUBSET_TOKENS = 2;

  // matchGdtfFixtureType: true when gdtfType and entryType are "the same
  // fixture" per the owner's tolerant-matching ask — exact match after
  // normalization, OR one side's full token set is contained in the
  // other's (a shorter name matching a longer one: "JDC 1" inside "GLP
  // JDC1 Strobe", "Era800" inside "ERA 800 Performance"). Returns a reason
  // string (for the "what matched and why" UI) or null for no match.
  function matchGdtfFixtureType(gdtfType, entryType) {
    const a = fuzzyTokenSet(gdtfType);
    const b = fuzzyTokenSet(entryType);
    if (a.size === 0 || b.size === 0) return null;
    if (tokenContainment(a, b) === 1 && tokenContainment(b, a) === 1) {
      return 'exact match (case/spacing/punctuation ignored)';
    }
    const [smaller, smallerLabel, larger] = a.size <= b.size ? [a, 'GDTF', b] : [b, 'patch entry', a];
    if (smaller.size < MIN_SUBSET_TOKENS) return null;
    if (tokenContainment(larger, smaller) === 1) {
      return `${smallerLabel} name "${Array.from(smaller).join(' ')}" is a subset of the other`;
    }
    return null;
  }

  // computeGdtfMatchGroups: groups current patch entries by their DISTINCT
  // (verbatim) fixtureType string, tests each distinct group against the
  // parsed GDTF's fixtureType via matchGdtfFixtureType, and returns every
  // group that matched — [{ fixtureType, reason, entries }]. Deliberately
  // grouped rather than a flat entry list: the loose matcher above can
  // legitimately find MORE THAN ONE distinct fixture-type string matching
  // the same GDTF (e.g. a rig with both "JDC 1" and, mistakenly, an
  // unrelated "JDC 12 RGB" patched) — auto-applying to all of them would
  // silently apply the wrong footprint/channel map to real fixtures, which
  // the task brief calls out as worse than missing a match entirely. The
  // caller (onGdtfFileChosen/renderGdtfImportPreview) surfaces >1 group as
  // an explicit "which fixture is this?" choice rather than ever merging
  // them.
  function computeGdtfMatchGroups(parsed) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const want = parsed.fixtureType || '';
    if (!want.trim()) return [];
    const byType = new Map(); // verbatim fixtureType -> entries[]
    entries.forEach(e => {
      const ft = e.fixtureType || '';
      if (!ft.trim()) return;
      if (!byType.has(ft)) byType.set(ft, []);
      byType.get(ft).push(e);
    });
    const groups = [];
    byType.forEach((es, ft) => {
      const reason = matchGdtfFixtureType(want, ft);
      if (reason) groups.push({ fixtureType: ft, reason, entries: es });
    });
    return groups;
  }

  function onEnterScreen() {
    active = true;
    refresh();
  }

  function onLeaveScreen() {
    active = false;
    stopPatternHeartbeat();
    // Best-effort, always — whether or not the Rig Check sub-view was the
    // one on screen, the running check must never keep lighting the rig
    // after the tech has navigated away (task ask: "never leave the rig
    // lit"). A no-op server-side if nothing is running — covers both the
    // classic per-entry check AND a running function-pattern (POST
    // .../rigcheck/stop stops either, per the pattern engine's contract).
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
      collisions = patchData.active ? (await Api.getPatchCollisions() || []) : [];
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
    // patternState is fetched regardless of which Rig Check sub-view is on
    // screen — the Channel check sub-view needs to know a pattern is
    // running too, so it can warn rather than let a click surface a raw
    // 409 (task ask: "handle gracefully rather than showing a raw
    // error"). The heartbeat poll itself only runs while the Function
    // check sub-view is the one visible (ensurePatternHeartbeat/
    // stopPatternHeartbeat around sub-tab switches) — this one-off GET is
    // also a valid heartbeat touch but isn't relied on as one.
    try {
      patternState = await Api.getPattern();
      if (patternState.running && rcSubView === 'function') ensurePatternHeartbeat();
    } catch (e) { /* best-effort */ }
    if (rcSubView === 'function') await refreshFunctionCheckAttributes();
  }

  function setView(v) {
    if (v !== 'rigcheck') stopPatternHeartbeat();
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
          <button id="btnImportMvr" class="b5-btn b5-btn--sm">Import MVR&hellip;</button>
          <button id="btnImportGdtf" class="b5-btn b5-btn--sm">Import GDTF&hellip;</button>
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
      <div id="mvrImportPreview"></div>
      <div id="gdtfImportPreview"></div>
      <div class="b5-panel">
        <div class="b5-panel__body--flush">
          <table class="b5-table b5-table--responsive" id="patchEntriesTable">
            <thead><tr>
              <th><label class="b5-visually-hidden" for="patchSelectAll">Select all in view</label><input type="checkbox" id="patchSelectAll" aria-label="Select all in view"></th>
              <th>Universe</th><th>Address</th><th>Name</th><th>Fixture type</th><th>Fixture #</th><th>RDM match</th><th>Actions</th>
            </tr></thead>
            <tbody></tbody>
          </table>
        </div>
      </div>
      <div id="patchEntryEditor" class="b5-patch-editor"></div>
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
    document.getElementById('btnImportMvr').addEventListener('click', () => ensureMvrFileInput().click());
    document.getElementById('btnImportGdtf').addEventListener('click', () => ensureGdtfFileInput().click());
    document.getElementById('btnExportPatchJson').addEventListener('click', () => window.open(Api.patchExportUrl('json'), '_blank'));
    document.getElementById('btnExportPatchTxt').addEventListener('click', () => window.open(Api.patchExportUrl('txt'), '_blank'));

    const filterInput = document.getElementById('patchFilter');
    filterInput.addEventListener('input', (e) => { filterText = e.target.value; renderEntriesTableBody(); });
    document.getElementById('patchSort').addEventListener('change', (e) => { sortMode = e.target.value; renderEntriesTableBody(); });

    renderEntriesTableBody();
    renderEntryEditor();
    renderMvrImportPreview();
    renderGdtfImportPreview();
  }

  // --- MVR import preview (Apply-to-confirm: parseMvrFile() above only
  // builds this preview; nothing reaches the server until Import
  // (merge/replace) is clicked here) --------------------------------------

  function renderMvrImportPreview() {
    const container = document.getElementById('mvrImportPreview');
    if (!container) return;
    if (!mvrPreview) {
      container.innerHTML = '';
      return;
    }
    const { fileName, entries, warnings } = mvrPreview;
    container.innerHTML = `
      <div class="b5-panel" style="margin-bottom:var(--b5-space-4)">
        <div class="b5-panel__header"><h3 class="b5-panel__title">Import preview — ${escapeHtml(fileName)}</h3></div>
        <div class="b5-panel__body b5-stack">
          <p class="b5-text-sm">${entries.length} fixture${entries.length === 1 ? '' : 's'} parsed${warnings.length ? `, ${warnings.length} warning${warnings.length === 1 ? '' : 's'}` : ''}.</p>
          ${warnings.length ? `
            <div class="b5-alert b5-alert--caution">
              ${UI.icon('status-warning')}
              <div>
                <p class="b5-alert__title">${warnings.length} warning${warnings.length === 1 ? '' : 's'}</p>
                <ul style="margin:var(--b5-space-2) 0 0; padding-left:1.2em">
                  ${warnings.map(w => `<li class="b5-text-sm">${escapeHtml(w)}</li>`).join('')}
                </ul>
              </div>
            </div>
          ` : ''}
          <div class="b5-panel__body--flush" style="max-height:40vh;overflow-y:auto">
            <table class="b5-table b5-table--responsive">
              <thead><tr><th>Universe</th><th>Address</th><th>Name</th><th>Fixture type</th><th>Footprint</th></tr></thead>
              <tbody>
                ${entries.map(e => `
                  <tr>
                    <td data-label="Universe">${UI.formatUniverse(e.universe)}</td>
                    <td data-label="Address">${e.startAddress || '—'}</td>
                    <td data-label="Name">${escapeHtml(e.name || '—')}</td>
                    <td data-label="Fixture type">${escapeHtml(e.fixtureType || '—')}</td>
                    <td data-label="Footprint">${e.footprint || 0}</td>
                  </tr>
                `).join('')}
              </tbody>
            </table>
          </div>
          <div class="b5-row">
            <button id="mvrImportMerge" class="b5-btn b5-btn--sm b5-btn--primary" ${mvrImporting ? 'disabled' : ''}>Import (merge)</button>
            <button id="mvrImportFresh" class="b5-btn b5-btn--sm" ${mvrImporting ? 'disabled' : ''}>Import (replace patch)</button>
            <button id="mvrImportCancel" class="b5-btn b5-btn--sm b5-btn--ghost" ${mvrImporting ? 'disabled' : ''}>Cancel</button>
            ${mvrImporting ? `<span class="b5-inline-wait">${UI.spinner()}Importing…</span>` : ''}
          </div>
        </div>
      </div>
    `;
    document.getElementById('mvrImportMerge').addEventListener('click', () => runMvrImport('merge'));
    document.getElementById('mvrImportFresh').addEventListener('click', () => runMvrImport('fresh'));
    document.getElementById('mvrImportCancel').addEventListener('click', () => {
      mvrPreview = null;
      renderMvrImportPreview();
    });
  }

  async function runMvrImport(mode) {
    if (!mvrPreview) return;
    const verb = mode === 'fresh' ? 'REPLACE the current patch with' : 'merge into the current patch';
    if (!confirm(`Import ${mvrPreview.entries.length} fixture(s) from "${mvrPreview.fileName}" — ${verb} entries parsed from this MVR file?`)) return;
    mvrImporting = true;
    renderMvrImportPreview();
    try {
      patchData = await Api.patchImport(mode, mvrPreview.entries);
      setStatus(`imported ${mvrPreview.entries.length} entr${mvrPreview.entries.length === 1 ? 'y' : 'ies'} from ${mvrPreview.fileName}`);
      mvrPreview = null;
      mvrImporting = false;
      await refresh();
    } catch (e) {
      mvrImporting = false;
      setStatus('error: ' + e.message);
      renderMvrImportPreview();
    }
  }

  // --- single-GDTF import preview (task ask: import one .gdtf file, match
  // it to a fixture type, apply to matching entries — apply-to-confirm,
  // same contract as the MVR import preview above: parsing only builds
  // this preview, nothing is written until "Apply" is clicked) -----------

  function renderGdtfImportPreview() {
    const container = document.getElementById('gdtfImportPreview');
    if (!container) return;
    if (!gdtfPreview) {
      container.innerHTML = '';
      return;
    }
    const { fileName, parsed, selectedModeIndex, candidateGroups, selectedGroupIndex } = gdtfPreview;
    const modes = parsed.modes || [];
    const mode = modes[selectedModeIndex] || null;
    const fixtureType = parsed.fixtureType || '(unnamed fixture type)';
    const selectedGroup = selectedGroupIndex != null ? candidateGroups[selectedGroupIndex] : null;
    const matches = selectedGroup ? selectedGroup.entries : [];

    let body;
    if (!modes.length) {
      body = `
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__title">No DMX modes found in this GDTF file</p>
          <p class="b5-text-sm">The file parsed, but "${escapeHtml(fixtureType)}" has no &lt;DMXMode&gt; entries — there is nothing to apply.</p></div>
        </div>
      `;
    } else if (!candidateGroups.length) {
      body = `
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__title">No patch entries match this fixture type</p>
          <p class="b5-text-sm">No entry's fixture type resembles "${escapeHtml(fixtureType)}" (case, spacing, "-"/"_"/"." and digit-joining are all ignored — "JDC1"/"JDC 1"/"JDC-1" all count as the same). Nothing to apply — check the entry's "Fixture type" field.</p></div>
        </div>
      `;
    } else if (candidateGroups.length > 1 && !selectedGroup) {
      // Ambiguous: more than one DISTINCT patch fixture-type string loosely
      // matches this GDTF. Never auto-apply to all of them (task brief:
      // "loose matching that silently picks the WRONG fixture type is far
      // worse than a miss") — show the candidates and require an explicit
      // pick before anything below becomes available.
      body = `
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__title">${candidateGroups.length} different fixture types in the patch resemble "${escapeHtml(fixtureType)}"</p>
          <p class="b5-text-sm">Pick which one this GDTF actually describes — applying to the wrong one would give those fixtures the wrong footprint and channel map.</p></div>
        </div>
        <div class="b5-stack">
          ${candidateGroups.map((g, i) => `
            <label class="b5-checkbox" style="align-items:flex-start">
              <input type="radio" name="gdtfGroupPick" value="${i}">
              <span>
                <strong>"${escapeHtml(g.fixtureType)}"</strong> — ${g.entries.length} entr${g.entries.length === 1 ? 'y' : 'ies'}<br>
                <span class="b5-text-sm b5-text-muted">Matched via: ${escapeHtml(g.reason)}</span>
              </span>
            </label>
          `).join('')}
        </div>
      `;
    } else {
      body = `
        <p class="b5-text-sm">${matches.length} patch entr${matches.length === 1 ? 'y matches' : 'ies match'} fixture type "${escapeHtml(selectedGroup.fixtureType)}" <span class="b5-text-muted">(matched via: ${escapeHtml(selectedGroup.reason)})</span>.</p>
        ${candidateGroups.length > 1 ? `<button id="gdtfChangeGroup" class="b5-btn b5-btn--sm b5-btn--ghost">Choose a different fixture type&hellip;</button>` : ''}
        ${modes.length > 1 ? `
          <div class="b5-field">
            <label class="b5-field__label" for="gdtfModeSelect">DMX mode to apply</label>
            <select id="gdtfModeSelect" class="b5-select">
              ${modes.map((m, i) => `<option value="${i}" ${i === selectedModeIndex ? 'selected' : ''}>${escapeHtml(m.name || '(unnamed mode)')} — ${m.footprint} ch</option>`).join('')}
            </select>
          </div>
        ` : `<p class="b5-text-sm b5-text-muted">Mode: ${escapeHtml(mode.name || '(unnamed mode)')} — ${mode.footprint} channels (only mode in this file).</p>`}
        <div class="b5-panel__body--flush" style="max-height:40vh;overflow-y:auto">
          <table class="b5-table b5-table--responsive">
            <thead><tr><th>Name</th><th>Universe</th><th>Address</th><th>Footprint (old &rarr; new)</th></tr></thead>
            <tbody>
              ${matches.map(e => `
                <tr>
                  <td data-label="Name">${escapeHtml(e.name || e.fixtureType || e.id)}</td>
                  <td data-label="Universe">${UI.formatUniverse(e.universe)}</td>
                  <td data-label="Address">${e.startAddress || '—'}</td>
                  <td data-label="Footprint">${e.footprint || 0} &rarr; <strong>${mode.footprint}</strong></td>
                </tr>
              `).join('')}
            </tbody>
          </table>
        </div>
        <div class="b5-row">
          <button id="gdtfApply" class="b5-btn b5-btn--sm b5-btn--primary" ${gdtfApplying ? 'disabled' : ''}>Apply mode "${escapeHtml(mode.name || '(unnamed)')}" to ${matches.length} entr${matches.length === 1 ? 'y' : 'ies'}&hellip;</button>
          <button id="gdtfCancel" class="b5-btn b5-btn--sm b5-btn--ghost" ${gdtfApplying ? 'disabled' : ''}>Cancel</button>
          ${gdtfApplying ? `<span class="b5-inline-wait">${UI.spinner()}Applying…</span>` : ''}
        </div>
      `;
    }

    container.innerHTML = `
      <div class="b5-panel" style="margin-bottom:var(--b5-space-4)">
        <div class="b5-panel__header"><h3 class="b5-panel__title">Import GDTF — ${escapeHtml(fileName)}</h3></div>
        <div class="b5-panel__body b5-stack">
          <p class="b5-text-sm b5-text-muted">${escapeHtml(parsed.manufacturer || '—')} / ${escapeHtml(parsed.model || '—')}</p>
          ${(parsed.warnings || []).length ? `
            <div class="b5-alert b5-alert--caution">
              ${UI.icon('status-warning')}
              <div><p class="b5-alert__title">${parsed.warnings.length} geometry warning${parsed.warnings.length === 1 ? '' : 's'}</p>
              <ul style="margin:var(--b5-space-2) 0 0; padding-left:1.2em">
                ${parsed.warnings.map(w => `<li class="b5-text-sm">${escapeHtml(w)}</li>`).join('')}
              </ul></div>
            </div>
          ` : ''}
          ${body}
          ${(!matches.length || !modes.length) && !(candidateGroups.length > 1 && !selectedGroup) ? `<div class="b5-row"><button id="gdtfCancel" class="b5-btn b5-btn--sm b5-btn--ghost">Close</button></div>` : ''}
        </div>
      </div>
    `;
    const modeSel = document.getElementById('gdtfModeSelect');
    if (modeSel) modeSel.addEventListener('change', (e) => {
      gdtfPreview.selectedModeIndex = Number(e.target.value);
      renderGdtfImportPreview();
    });
    document.querySelectorAll('#gdtfCancel').forEach(btn => btn.addEventListener('click', () => {
      gdtfPreview = null;
      renderGdtfImportPreview();
    }));
    document.querySelectorAll('input[name="gdtfGroupPick"]').forEach(r => r.addEventListener('change', (e) => {
      gdtfPreview.selectedGroupIndex = Number(e.target.value);
      renderGdtfImportPreview();
    }));
    const changeGroupBtn = document.getElementById('gdtfChangeGroup');
    if (changeGroupBtn) changeGroupBtn.addEventListener('click', () => {
      gdtfPreview.selectedGroupIndex = null;
      renderGdtfImportPreview();
    });
    const applyBtn = document.getElementById('gdtfApply');
    if (applyBtn) applyBtn.addEventListener('click', runGdtfApply);
  }

  async function runGdtfApply() {
    if (!gdtfPreview) return;
    const { parsed, selectedModeIndex, candidateGroups, selectedGroupIndex } = gdtfPreview;
    const selectedGroup = selectedGroupIndex != null ? candidateGroups[selectedGroupIndex] : null;
    const matches = selectedGroup ? selectedGroup.entries : [];
    const mode = (parsed.modes || [])[selectedModeIndex];
    if (!mode || !matches.length) return;
    const n = matches.length;
    const lines = matches.slice(0, 25)
      .map(e => `  ${e.name || e.fixtureType || e.id}: footprint ${e.footprint || 0} → ${mode.footprint}`)
      .join('\n');
    const more = n > 25 ? `\n  ...and ${n - 25} more` : '';
    if (!confirm(
      `Apply mode "${mode.name || '(unnamed)'}" (${mode.footprint} channels) from "${parsed.fixtureType}" to ${n} matching ` +
      `patch entr${n === 1 ? 'y' : 'ies'}? Only mode/footprint change — name, universe, address, position, fixture ` +
      `number and any confirmed RDM pairing are left exactly as they are.\n\n${lines}${more}`
    )) return;
    gdtfApplying = true;
    renderGdtfImportPreview();
    const errors = [];
    for (const e of matches) {
      const draft = {
        name: e.name || '', fixtureType: e.fixtureType || '', mode: mode.name || '',
        footprint: mode.footprint || 0, universe: e.universe || 0, startAddress: e.startAddress || 1,
        position: e.position || '', fixtureNumber: e.fixtureNumber || '', notes: e.notes || '',
        // Apply this GDTF mode's channel functions too — a re-import is
        // exactly the case that SHOULD replace whatever channel-function
        // data (if any) the entry had before, same as it replaces footprint.
        channelFunctions: mode.channelFunctions || {},
      };
      try {
        patchData = await Api.updatePatchEntry(e.id, draft);
      } catch (err) {
        errors.push((e.name || e.fixtureType || e.id) + ': ' + err.message);
      }
    }
    gdtfApplying = false;
    setStatus(errors.length ? `applied GDTF mode to ${n - errors.length} of ${n}, ${errors.length} error(s)` : `applied GDTF mode to ${n} entries`);
    try { collisions = patchData.active ? (await Api.getPatchCollisions() || []) : []; } catch (e) { /* best-effort */ }
    if (!errors.length) gdtfPreview = null;
    render();
    if (errors.length) setStatus('error applying to some entries: ' + errors.join('; '));
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

  // composeFindingMessage: the client-side mirror of internal/web/patch.go's
  // composeFindingText — the one place a finding's structured fields become
  // the sentence a human reads on screen. f.message (from GET
  // /api/patch/collisions) is server-authored English but, by contract (see
  // internal/patch/collision.go's Finding.Message doc comment), never states
  // a bare universe number — the number is always display-base-shifted here
  // via UI.formatUniverse, from the canonical f.universe field, so the
  // banner and the Universe column always agree by construction. Every Kind
  // that needs to state a universe (today, just "overlap") gets a case
  // below; any other Kind's f.message is already safe to show verbatim and
  // needs no composing.
  function composeFindingMessage(f) {
    if (f.kind === 'overlap') {
      return `${f.message} in universe ${UI.formatUniverse(f.universe)}`;
    }
    return f.message;
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
            ${findings.map(f => `<li class="b5-text-sm">${severityBadge(f.severity)} ${escapeHtml(composeFindingMessage(f))}</li>`).join('')}
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

  // uniOf/addrOf: coerce an entry's canonical universe/startAddress to a
  // real number before any arithmetic comparison. The server now always
  // sends both fields explicitly (entry.go's Universe/StartAddress dropped
  // `omitempty`, fixing the bug where a canonical universe-0 entry had its
  // "universe" key omitted entirely, e.universe arrived as `undefined`,
  // and a bare `a.universe - b.universe` evaluated to NaN — sorting
  // universe-0/display-"1" rows out of order while every other universe
  // sorted fine). These helpers are kept anyway as defense in depth (this
  // project's established practice — see sensorReadingJSON's HasRange/
  // HasNormalBand precedent) against any future producer of entry-shaped
  // data (an importer, a hand-crafted fixture, a stale cached response)
  // that omits the key again. `|| 0` is safe here since 0 is the only
  // falsy canonical value either field can legitimately hold.
  function uniOf(e) { return e.universe || 0; }
  function addrOf(e) { return e.startAddress || 0; }

  function sortEntries(entries, mode) {
    const out = entries.slice();
    const cmpStr = (a, b) => (a || '').localeCompare(b || '');
    switch (mode) {
      case 'name': out.sort((a, b) => cmpStr(a.name, b.name)); break;
      case 'type': out.sort((a, b) => cmpStr(a.fixtureType, b.fixtureType)); break;
      case 'fixtureNumber': out.sort((a, b) => cmpStr(a.fixtureNumber, b.fixtureNumber)); break;
      case 'address':
      default:
        out.sort((a, b) => (uniOf(a) - uniOf(b)) || (addrOf(a) - addrOf(b)) || cmpStr(a.name, b.name));
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
    // Selection can only ever reference entries that still exist (a
    // deleted/replaced entry's id must not linger and silently count
    // toward "N selected").
    const liveIds = new Set(entries.map(x => x.id));
    Object.keys(selectedIds).forEach(id => { if (!liveIds.has(id)) delete selectedIds[id]; });
    if (!sorted.length) {
      tbody.innerHTML = `<tr><td colspan="8"><div class="b5-empty">${UI.icon('nav-devices')}<span class="b5-empty__title">${entries.length ? 'No entries match the filter' : 'No entries yet'}</span><span class="b5-empty__body">${entries.length ? 'Try a different filter.' : 'Add entry, or Adopt the discovered rig.'}</span></div></td></tr>`;
      updateSelectAllCheckbox([]);
      renderEntryEditor();
      return;
    }
    tbody.innerHTML = sorted.map(e => renderEntryRow(e, findingsByEntry[e.id] || [])).join('');
    tbody.querySelectorAll('[data-edit]').forEach(btn => btn.addEventListener('click', () => openEditEntry(entries.find(x => x.id === btn.dataset.edit))));
    tbody.querySelectorAll('[data-delete]').forEach(btn => btn.addEventListener('click', () => deleteEntry(btn.dataset.delete, entries)));
    tbody.querySelectorAll('[data-move-up]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveUp, -1, entries)));
    tbody.querySelectorAll('[data-move-down]').forEach(btn => btn.addEventListener('click', () => moveEntry(btn.dataset.moveDown, 1, entries)));
    tbody.querySelectorAll('[data-row-select]').forEach(cb => cb.addEventListener('click', (evt) => onRowSelectClick(evt, sorted)));
    updateSelectAllCheckbox(sorted);
    renderEntryEditor();
  }

  // onRowSelectClick: plain click toggles just this row; shift-click
  // selects the whole range between the last row clicked (in the CURRENT
  // sorted/filtered view order) and this one — the "shift-click range
  // selection if cheap" ask. Click, not change, so shiftKey is visible.
  function onRowSelectClick(evt, sortedInView) {
    const id = evt.currentTarget.dataset.rowSelect;
    if (evt.shiftKey && lastClickedEntryId) {
      const ids = sortedInView.map(x => x.id);
      const a = ids.indexOf(lastClickedEntryId);
      const b = ids.indexOf(id);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        const checked = evt.currentTarget.checked;
        for (let i = lo; i <= hi; i++) {
          if (checked) selectedIds[ids[i]] = true; else delete selectedIds[ids[i]];
        }
        renderEntriesTableBody();
        return;
      }
    }
    if (evt.currentTarget.checked) selectedIds[id] = true; else delete selectedIds[id];
    lastClickedEntryId = id;
    updateSelectAllCheckbox(sortedInView);
    renderEntryEditor();
  }

  function updateSelectAllCheckbox(sortedInView) {
    const all = document.getElementById('patchSelectAll');
    if (!all) return;
    const total = sortedInView.length;
    const checkedCount = sortedInView.filter(e => selectedIds[e.id]).length;
    all.checked = total > 0 && checkedCount === total;
    all.indeterminate = checkedCount > 0 && checkedCount < total;
    all.onclick = () => {
      if (all.checked) sortedInView.forEach(e => { selectedIds[e.id] = true; });
      else sortedInView.forEach(e => { delete selectedIds[e.id]; });
      renderEntriesTableBody();
    };
  }

  function renderEntryRow(e, findings) {
    const issue = findings.length ? `<div class="b5-field__error">${UI.icon('status-warning')}${findings.map(f => escapeHtml(f.kind)).join(', ')}</div>` : '';
    const match = e.confirmedUid
      ? UI.badge('ok', 'Confirmed')
      : (e.matchState === 'rejected' ? UI.badge('warning', 'Rejected candidates') : '<span class="b5-text-muted b5-text-sm">Unresolved</span>');
    const selected = !!selectedIds[e.id];
    return `
      <tr data-entry-id="${escapeHtml(e.id)}" class="${selected ? 'b5-patch-row--selected' : ''}">
        <td data-label="Select"><label class="b5-visually-hidden" for="sel-${escapeHtml(e.id)}">Select ${escapeHtml(e.name || e.fixtureType || e.id)}</label><input type="checkbox" id="sel-${escapeHtml(e.id)}" data-row-select="${escapeHtml(e.id)}" ${selected ? 'checked' : ''} aria-label="Select ${escapeHtml(e.name || e.fixtureType || e.id)}"></td>
        <td data-label="Universe">${UI.formatUniverse(e.universe)}</td>
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
      collisions = patchData.active ? (await Api.getPatchCollisions() || []) : [];
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
    clearSelection();
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

  // setPatchEditorOpen: toggles the body-level class the CSS reserve rule
  // (app.css, mirroring --b5-walk-bar-reserve) keys off of, so the fixed
  // floating panel never covers content with nothing left to scroll to
  // reach it — the exact regression app.css's own walk-bar comment warns
  // about ("previously bitten by fixed bars covering content").
  function setPatchEditorOpen(open) {
    document.body.classList.toggle('b5-patch-editor-open', open);
  }

  // renderEntryEditor: ONE floating panel (#patchEntryEditor, fixed —
  // see app.css) that renders one of three things depending on state:
  //   - 2+ rows selected  -> bulk-edit mode (wins over a single edit in
  //     progress, since selecting a second row while editing entry A is
  //     read as "actually, I want to bulk-edit these")
  //   - editingEntry set  -> single add/edit form (existing functionality,
  //     unchanged, just now living in a floating panel instead of the
  //     bottom of the page)
  //   - neither           -> empty/closed, no space reserved
  function renderEntryEditor() {
    const container = document.getElementById('patchEntryEditor');
    if (!container) return;
    if (selectedCount() >= 2) {
      setPatchEditorOpen(true);
      renderBulkEditor(container);
      return;
    }
    if (editingEntry === null) {
      container.innerHTML = '';
      setPatchEditorOpen(false);
      return;
    }
    setPatchEditorOpen(true);
    const d = entryDraft;
    const ua = UI.universeInputAttrs();
    container.innerHTML = `
      <div class="b5-panel b5-patch-editor__panel">
        <div class="b5-panel__header">
          <h3 class="b5-panel__title">${editingEntry === 'new' ? 'Add entry' : 'Edit entry'}</h3>
          <button id="peClose" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Close editor">Close</button>
        </div>
        <div class="b5-panel__body b5-grid-2">
          <div class="b5-field"><label class="b5-field__label" for="peName">Name</label><input id="peName" class="b5-input" type="text" maxlength="64"></div>
          <div class="b5-field"><label class="b5-field__label" for="peType">Fixture type</label><input id="peType" class="b5-input" type="text" maxlength="80" placeholder="e.g. Chauvet Rogue Outcast 2X Wash"></div>
          <div class="b5-field"><label class="b5-field__label" for="peMode">Mode / personality</label><input id="peMode" class="b5-input" type="text" maxlength="40"></div>
          <div class="b5-field"><label class="b5-field__label" for="peFootprint">Footprint (DMX channels)</label><input id="peFootprint" class="b5-input" type="number" min="0" max="512"></div>
          <div class="b5-field"><label class="b5-field__label" for="peUniverse">Universe (${UI.universeBaseLabel()})</label><input id="peUniverse" class="b5-input" type="number" min="${ua.min}" max="${ua.max}"></div>
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
    bind('peAddress', 'startAddress', true);
    bind('pePosition', 'position', false);
    bind('peFixtureNumber', 'fixtureNumber', false);
    bind('peNotes', 'notes', false);
    // Universe is the one field whose ON-SCREEN value is display-base
    // converted (UI.formatUniverse) while entryDraft.universe stays the
    // true 0-based wire value at all times — every other reader of
    // entryDraft (peSave below, bulk editor) sees the canonical number.
    const uniEl = document.getElementById('peUniverse');
    uniEl.value = UI.formatUniverse(d.universe);
    uniEl.addEventListener('input', () => { d.universe = UI.parseUniverse(uniEl.value); });

    document.getElementById('peCancel').addEventListener('click', closeEntryEditor);
    document.getElementById('peClose').addEventListener('click', closeEntryEditor);
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
        collisions = patchData.active ? (await Api.getPatchCollisions() || []) : [];
        render();
      } catch (e) {
        errEl.innerHTML = UI.icon('status-error') + ('error: ' + escapeHtml(e.message));
      }
    });
  }

  // --- bulk edit (task ask, item 4: "the motivating case: move 4
  // fixtures to universe 13 at once") — Apply-to-confirm: each field has
  // its own Apply button, and Apply always shows a native confirm()
  // stating exactly what will change before looping PUT
  // /api/patch/entries/{id} once per selected entry (no batch endpoint —
  // the brief allows a client-side loop, and at patch-list scale this is
  // cheap and gives per-entry error isolation for free). Start addresses
  // are never touched by bulk edit. ------------------------------------

  function renderBulkEditor(container) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const ids = Object.keys(selectedIds).filter(id => selectedIds[id]);
    const selected = ids.map(id => entries.find(e => e.id === id)).filter(Boolean);
    const n = selected.length;
    const ua = UI.universeInputAttrs();
    container.innerHTML = `
      <div class="b5-panel b5-patch-editor__panel">
        <div class="b5-panel__header">
          <h3 class="b5-panel__title">Bulk edit — ${n} entries selected</h3>
          <button id="bulkClose" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Close editor">Close</button>
        </div>
        <div class="b5-panel__body b5-stack">
          <p class="b5-text-sm b5-text-muted">${selected.map(e => escapeHtml(e.name || e.fixtureType || e.id)).join(', ')}</p>
          <div class="b5-field">
            <label class="b5-field__label" for="bulkUniverse">Move to universe (${UI.universeBaseLabel()})</label>
            <div class="b5-field__row">
              <input id="bulkUniverse" class="b5-input" type="number" min="${ua.min}" max="${ua.max}" placeholder="unchanged" value="${escapeHtml(bulkDraft.universe)}">
              <span class="b5-field__actions"><button id="bulkApplyUniverse" class="b5-btn b5-btn--sm b5-btn--primary" ${bulkApplying ? 'disabled' : ''}>${UI.icon('apply')}Apply</button></span>
            </div>
            <span class="b5-field__hint">Start addresses are left unchanged — only the universe moves.</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="bulkPosition">Set position label</label>
            <div class="b5-field__row">
              <input id="bulkPosition" class="b5-input" type="text" maxlength="60" placeholder="unchanged" value="${escapeHtml(bulkDraft.position)}">
              <span class="b5-field__actions"><button id="bulkApplyPosition" class="b5-btn b5-btn--sm b5-btn--primary" ${bulkApplying ? 'disabled' : ''}>${UI.icon('apply')}Apply</button></span>
            </div>
          </div>
          <div class="b5-row">
            <button id="bulkClearSelection" class="b5-btn b5-btn--sm b5-btn--ghost" ${bulkApplying ? 'disabled' : ''}>Clear selection</button>
            ${bulkApplying ? `<span class="b5-inline-wait">${UI.spinner()}Applying…</span>` : ''}
          </div>
          <span class="b5-field__error" id="bulkError"></span>
        </div>
      </div>
    `;
    document.getElementById('bulkClose').addEventListener('click', () => { clearSelection(); renderEntriesTableBody(); });
    document.getElementById('bulkClearSelection').addEventListener('click', () => { clearSelection(); renderEntriesTableBody(); });
    document.getElementById('bulkUniverse').addEventListener('input', (e) => { bulkDraft.universe = e.target.value; });
    document.getElementById('bulkPosition').addEventListener('input', (e) => { bulkDraft.position = e.target.value; });
    document.getElementById('bulkApplyUniverse').addEventListener('click', () => runBulkApply('universe', selected));
    document.getElementById('bulkApplyPosition').addEventListener('click', () => runBulkApply('position', selected));
  }

  async function runBulkApply(field, selected) {
    const errEl = document.getElementById('bulkError');
    errEl.innerHTML = '';
    const n = selected.length;
    let confirmMsg, mutate;
    if (field === 'universe') {
      const raw = bulkDraft.universe;
      if (raw === '' || raw === null || raw === undefined) { errEl.innerHTML = UI.icon('status-error') + 'enter a universe'; return; }
      const canonical = UI.parseUniverse(raw);
      confirmMsg = `Move ${n} entries to universe ${UI.formatUniverse(canonical)}? Start addresses stay unchanged.`;
      mutate = (draft) => { draft.universe = canonical; };
    } else {
      const pos = bulkDraft.position;
      if (!pos.trim()) { errEl.innerHTML = UI.icon('status-error') + 'enter a position'; return; }
      confirmMsg = `Set position to "${pos}" for ${n} entries?`;
      mutate = (draft) => { draft.position = pos; };
    }
    if (!confirm(confirmMsg)) return;
    bulkApplying = true;
    renderEntryEditor();
    const errors = [];
    for (const e of selected) {
      const draft = {
        name: e.name || '', fixtureType: e.fixtureType || '', mode: e.mode || '',
        footprint: e.footprint || 0, universe: e.universe || 0, startAddress: e.startAddress || 1,
        position: e.position || '', fixtureNumber: e.fixtureNumber || '', notes: e.notes || '',
      };
      mutate(draft);
      try {
        patchData = await Api.updatePatchEntry(e.id, draft);
      } catch (err) {
        errors.push((e.name || e.fixtureType || e.id) + ': ' + err.message);
      }
    }
    bulkApplying = false;
    bulkDraft = { universe: '', position: '' };
    setStatus(errors.length ? `applied to ${n - errors.length} of ${n}, ${errors.length} error(s)` : `applied to ${n} entries`);
    // Collision detection is server-side; re-fetch so the banner reflects
    // the just-applied bulk change (task ask, item 4: "collision
    // detection ... just ensure the UI refreshes the collision banner").
    try { collisions = patchData.active ? (await Api.getPatchCollisions() || []) : []; } catch (e) { /* best-effort */ }
    if (errors.length) errEl.innerHTML = UI.icon('status-error') + errors.join('; ');
    render();
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

  // renderRigCheck: the Rig Check screen's own sub-tab switcher between the
  // original channel-level check ("Channel check", unchanged) and the new
  // attribute-group pattern engine ("Function check", Phase 2b — task ask:
  // additive, not a replacement). A plain button pair rather than the
  // b5-tabs kit component: b5-tabs is reserved for the outer
  // Entries/Reconcile/Rig Check switcher one level up, and nesting the same
  // component two deep reads as confusing in the kit's own styling.
  function renderRigCheck(body) {
    body.innerHTML = `
      <div class="b5-row" style="margin-bottom:var(--b5-space-3)" role="tablist" aria-label="Rig check mode">
        <button id="rcSubClassic" class="b5-btn b5-btn--sm ${rcSubView === 'classic' ? 'b5-btn--primary' : ''}" role="tab" aria-selected="${rcSubView === 'classic'}">Channel check</button>
        <button id="rcSubFunction" class="b5-btn b5-btn--sm ${rcSubView === 'function' ? 'b5-btn--primary' : ''}" role="tab" aria-selected="${rcSubView === 'function'}">Function check</button>
      </div>
      <div id="rcSubBody"></div>
    `;
    document.getElementById('rcSubClassic').addEventListener('click', () => setRcSubView('classic'));
    document.getElementById('rcSubFunction').addEventListener('click', () => setRcSubView('function'));
    const sub = document.getElementById('rcSubBody');
    if (rcSubView === 'function') renderFunctionCheck(sub);
    else renderClassicRigCheck(sub);
  }

  function setRcSubView(v) {
    if (v === rcSubView) return;
    if (v !== 'function') stopPatternHeartbeat();
    rcSubView = v;
    sessionStorage.setItem('benny512.patch.rcSubView', v);
    render();
    if (v === 'function') {
      Api.getPattern().then(st => { patternState = st; if (st.running) ensurePatternHeartbeat(); renderRigCheck(document.getElementById('patchViewBody')); }).catch(() => {});
      refreshFunctionCheckAttributes().then(() => renderRigCheck(document.getElementById('patchViewBody')));
    }
  }

  function renderClassicRigCheck(body) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const st = rigCheckState || { running: false, mode: rcMode, level: rcLevel };
    // A running Function-check pattern and the classic per-entry check are
    // mutually exclusive server-side (POST .../rigcheck/mode|level|next
    // 409s while a pattern runs — verified against the live server, not
    // assumed). Rather than let a click surface that as a raw error (task
    // ask: "handle gracefully"), the classic controls are disabled up
    // front with an explanatory banner and a direct Stop-the-pattern
    // button whenever one is running.
    const patternRunning = !!(patternState && patternState.running);

    body.innerHTML = `
      ${patternRunning ? `
        <div class="b5-alert b5-alert--caution" style="margin-bottom:var(--b5-space-4)">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">A Function check pattern is running</p>
            <p class="b5-alert__body">Stop it before using the channel-level check — they can't run at the same time.</p>
            <button id="rcStopPatternFromClassic" class="b5-btn b5-btn--sm b5-btn--danger" style="margin-top:var(--b5-space-2)">Stop pattern</button>
          </div>
        </div>
      ` : ''}
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden" for="rcScopeKind">Scope</label>
          <select id="rcScopeKind" class="b5-select" style="width:auto" ${patternRunning ? 'disabled' : ''}>
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
          <select id="rcMode" class="b5-select" style="width:auto" ${patternRunning ? 'disabled' : ''}>
            <option value="highlight">Mode: Highlight (this fixture up, rest dark)</option>
            <option value="all_channels">Mode: All channels to level</option>
            <option value="step_channel">Mode: Step one channel</option>
          </select>
          <label class="b5-text-sm" style="display:flex;align-items:center;gap:8px">Level
            <input type="range" id="rcLevel" class="b5-range-touch" min="0" max="255" value="${st.level || rcLevel}" ${patternRunning ? 'disabled' : ''}>
            <span class="b5-text-mono" id="rcLevelVal">${st.level || rcLevel}</span>
          </label>
          ${st.running
        ? '<button id="rcStop" class="b5-btn b5-btn--sm b5-btn--danger">Stop</button>'
        : `<button id="rcStart" class="b5-btn b5-btn--sm b5-btn--primary" ${rcStarting || patternRunning ? 'disabled' : ''}>${rcStarting ? UI.spinner() + 'Starting…' : 'Start'}</button>`}
          <button id="rcBlackout" class="b5-btn b5-btn--sm b5-btn--danger">Blackout</button>
        </div>
      </div>
      <div class="b5-alert b5-alert--info" style="margin-bottom:var(--b5-space-4)">
        ${UI.icon('status-pending')}
        <div><p class="b5-alert__body">Safety: stopping, leaving this screen, or closing the tab always blacks out and stops output.</p></div>
      </div>
      <div id="rcActiveWrap">${st.running ? renderRigCheckActive(st) : `<div class="b5-empty">${UI.icon('status-pending')}<span class="b5-empty__title">Not running</span><span class="b5-empty__body">Choose a scope and mode, then Start.</span></div>`}</div>
    `;

    const stopPatternBtn = document.getElementById('rcStopPatternFromClassic');
    if (stopPatternBtn) stopPatternBtn.addEventListener('click', async () => {
      try { await Api.rigCheckStop(); patternState = null; setStatus('function test stopped'); render(); } catch (e) { setStatus('error: ' + e.message); }
    });

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
      const ua = UI.universeInputAttrs();
      wrap.innerHTML = `<label class="b5-visually-hidden" for="rcScopeUniverse">Universe</label><input type="number" id="rcScopeUniverse" class="b5-input" style="width:10em" min="${ua.min}" max="${ua.max}" value="${UI.formatUniverse(rcScopeUniverse)}" placeholder="Universe">`;
      document.getElementById('rcScopeUniverse').addEventListener('input', (e) => { rcScopeUniverse = UI.parseUniverse(e.target.value); });
    } else if (rcScopeKind === 'selection') {
      wrap.innerHTML = `<div class="b5-stack" style="margin-top:var(--b5-space-2)">${entries.map(e => `
        <label class="b5-checkbox"><input type="checkbox" data-rc-select="${escapeHtml(e.id)}" ${rcSelection[e.id] ? 'checked' : ''}>${escapeHtml(e.name || e.fixtureType || e.id)} (U${UI.formatUniverse(e.universe)}/${e.startAddress})</label>
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

  // ============================================================
  // Function Check (Phase 2b): attribute-group pattern engine UI.
  // ============================================================
  //
  // Owner's three settled decisions (task brief) drive the whole design:
  //   1. Strict safe mode — a pattern only ever touches the group/kind the
  //      tech explicitly started; nothing here ever "helpfully" raises a
  //      dimmer or moves an axis to make a test visible.
  //   2. Mixed rigs — scope can include fixtures without the tested
  //      function; those are skipped server-side and the skip is always
  //      shown ("N of M fixtures"), never hidden.
  //   3. RDM-inferred attribute data is visible, never presented as
  //      authoritative — every function/entry carries its source, and the
  //      per-entry inferred flag on a running pattern is surfaced.
  //
  // The backend's granularity is GROUP+KIND, not per-function: one running
  // pattern always drives every fixture in scope that has ANY function in
  // the pattern's attribute group (server-computed — see `group` on
  // patternStatusJSON). There is no wire field to say "touch Pan but not
  // Tilt" except where a kind's own `target` grammar already expresses
  // that choice (move_extreme's axis, frost's light/heavy, manual_value's
  // focus/zoom). So "toggles for individual functions" is implemented
  // two ways, both honest about what the toggle actually does:
  //   - For a target-bearing kind, the function toggles ARE the target
  //     picker (single-select — the wire only carries one target at a
  //     time).
  //   - For every other kind, the group's functions are listed with their
  //     own "N of M" counts and provenance so the tech can see exactly
  //     what a Start click is about to touch before committing — informational,
  //     since e.g. shaper_all vs shaper_individual (not a function
  //     checkbox) is how "one at a time" vs "all together" is actually
  //     chosen for that group.

  const FC_GROUPS = [
    { id: 'dimmer', label: 'Dimmer' },
    { id: 'position', label: 'Position' },
    { id: 'colour', label: 'Colour' },
    { id: 'beam', label: 'Beam' },
    { id: 'focus', label: 'Focus' },
    { id: 'shaper', label: 'Shaper' },
  ];

  // FC_KINDS: every pattern kind the backend accepts, grouped, with just
  // enough shape metadata to drive the generic control renderer below
  // (hasRate/hasRange/hasDirection/hasValue/targets). Labels are the tech-
  // facing description; kind ids are the exact wire values.
  const FC_KINDS = {
    dimmer: [
      { id: 'dimmer_sine', label: 'Sine pulse', hint: 'Smooth breathing between min and max.', hasRate: true, hasRange: true },
      { id: 'dimmer_snap', label: 'Snap on/off', hint: 'Hard cut between min and max — good for finding a flickering dimmer.', hasRate: true, hasRange: true },
      { id: 'dimmer_toggle', label: 'Slow toggle', hint: 'Slow on/off, easier to watch across a whole rig.', hasRate: true, hasRange: true },
    ],
    position: [
      { id: 'ballyhoo', label: 'Ballyhoo sweep', hint: 'Continuous figure-8 pan/tilt sweep.', hasRate: true },
      {
        id: 'move_extreme', label: 'Move to extreme', hint: 'Drive one axis to a fixed extreme — hold to check range/travel.',
        hasTarget: true, targetLabel: 'Axis and extreme',
        targets: [
          { id: 'pan_max', label: 'Pan max' }, { id: 'pan_min', label: 'Pan min' }, { id: 'pan_centre', label: 'Pan centre' },
          { id: 'tilt_max', label: 'Tilt max' }, { id: 'tilt_min', label: 'Tilt min' }, { id: 'tilt_centre', label: 'Tilt centre' },
        ],
      },
    ],
    colour: [
      { id: 'colour_wheel_step', label: 'Colour wheel — step slots', hint: 'Steps through each named colour-wheel slot in turn.', hasRate: true },
      { id: 'colour_mix_sweep', label: 'RGB mix — sweep', hint: 'Sweeps the RGB mix channels between min and max together.', hasRate: true, hasRange: true },
      { id: 'colour_fade', label: 'RGB mix — fade through hues', hint: 'Fades RGB mix through a hue cycle.', hasRate: true },
    ],
    beam: [
      {
        id: 'frost', label: 'Frost', hint: 'Sets frost to a fixed level so you can eyeball beam softening.',
        hasTarget: true, targetLabel: 'Frost level',
        targets: [{ id: '', label: 'Clear (off)' }, { id: 'light', label: 'Light' }, { id: 'heavy', label: 'Heavy' }],
      },
      { id: 'prism_in_out', label: 'Prism in/out', hint: 'Toggles prism between open and in.', hasRate: true },
      { id: 'prism_spin', label: 'Prism rotate', hint: 'Spins the prism continuously.', hasRate: true, hasDirection: true },
      { id: 'animation_spin', label: 'Animation wheel spin', hint: 'Spins the animation/effects wheel continuously.', hasRate: true, hasDirection: true },
    ],
    focus: [
      {
        id: 'manual_value', label: 'Manual focus/zoom', hint: 'Holds focus or zoom at a fixed value you set.',
        hasTarget: true, targetLabel: 'Channel', hasValue: true,
        targets: [{ id: 'focus', label: 'Focus' }, { id: 'zoom', label: 'Zoom' }],
      },
    ],
    shaper: [
      { id: 'shaper_individual', label: 'Shapers — one at a time', hint: 'Cycles each shaper blade individually — the single-parameter test.', hasRate: true },
      { id: 'shaper_all', label: 'Shapers — all together', hint: 'Moves every shaper blade at once — the several-at-once test.', hasRate: true },
      { id: 'shaper_rotate', label: 'Shaper assembly rotate', hint: 'Rotates the whole shaper assembly.', hasRate: true, hasDirection: true },
    ],
  };

  function fcKindMeta(kindId) {
    for (const g of FC_GROUPS) {
      const found = (FC_KINDS[g.id] || []).find(k => k.id === kindId);
      if (found) return found;
    }
    return null;
  }

  function fcScopedEntries(allEntries) {
    switch (fcScopeKind) {
      case 'universe': return allEntries.filter(e => (e.universe || 0) === fcScopeUniverse);
      case 'position': return allEntries.filter(e => (e.position || '') === fcScopePosition);
      case 'selection': return allEntries.filter(e => fcSelection[e.id]);
      case 'all':
      default: return allEntries;
    }
  }

  function fcPositions(allEntries) {
    const set = new Set();
    allEntries.forEach(e => { if (e.position) set.add(e.position); });
    return Array.from(set).sort();
  }

  async function refreshFunctionCheckAttributes() {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const scoped = fcScopedEntries(entries);
    // An empty scope (e.g. a universe with nothing patched in it, or no
    // fixtures ticked in Selection scope) must show zero counts everywhere
    // — NOT the whole patch's counts. Api.getPatchAttributes treats an
    // empty ids array the same as "omitted" (server default: every
    // entry), so a truly empty scope is short-circuited here rather than
    // sent to the server at all.
    if (!scoped.length) {
      fcAttrData = { entries: [], summary: { totalFixtures: 0, groups: [] } };
      return;
    }
    fcAttrLoading = true;
    try {
      fcAttrData = await Api.getPatchAttributes(scoped.map(e => e.id));
    } catch (e) {
      fcAttrData = null;
    }
    fcAttrLoading = false;
  }

  // --- watchdog heartbeat -------------------------------------------------
  // The pattern engine blacks out and stops a running pattern after ~5s
  // without a start/adjust/GET-pattern touch (server safety net so a
  // closed browser can never leave a rig lit or a head swinging). Polling
  // GET .../rigcheck/pattern IS that touch, so this poll must run well
  // inside 5s for as long as a pattern is running — 2s gives a wide
  // margin even if a request is slow. The poll is also how the UI learns
  // a pattern ended (elsewhere: STOP click, another tab, or the watchdog
  // itself) and updates the status panel in place.
  const FC_HEARTBEAT_MS = 2000;
  function ensurePatternHeartbeat() {
    if (patternPollTimer) return;
    patternPollTimer = setInterval(async () => {
      if (!active || rcSubView !== 'function') { stopPatternHeartbeat(); return; }
      try {
        patternState = await Api.getPattern();
      } catch (e) {
        return; // best-effort — a transient fetch failure doesn't kill the heartbeat loop itself, next tick retries
      }
      if (!patternState.running) {
        stopPatternHeartbeat();
        if (patternState.lastEndReason === 'watchdog') {
          setStatus('Function test stopped automatically: the safety watchdog ended it after ~5s with no response from this page (rig is now blacked out).');
        }
        renderRigCheck(document.getElementById('patchViewBody'));
        return;
      }
      renderFunctionCheckActiveInPlace();
    }, FC_HEARTBEAT_MS);
  }
  function stopPatternHeartbeat() {
    if (patternPollTimer) { clearInterval(patternPollTimer); patternPollTimer = null; }
  }

  // --- render --------------------------------------------------------------

  function renderFunctionCheck(container) {
    const p = patchData.active ? patchData.patch : null;
    const allEntries = (p && p.entries) || [];
    const scoped = fcScopedEntries(allEntries);
    const running = !!(patternState && patternState.running);

    // Preserve which accordion groups were open across a full re-render
    // (scroll/focus preservation, task ask — a re-render otherwise blows
    // away every open <details>).
    const openGroups = {};
    document.querySelectorAll('#fcAttrTree > details[data-group]').forEach(d => { openGroups[d.dataset.group] = d.open; });
    const scrollY = window.scrollY;

    // Bug 3 fix: "Rig Check finds no attributes at all" traced to entries
    // whose ChannelFunctions map is genuinely empty — most commonly a patch
    // built/imported before this project's function-aware Rig Check
    // foundation existed (a v1 patch file migrates every entry to an empty,
    // never-nil ChannelFunctions map — see entry.go's
    // normalizeChannelFunctions — there is no backfill path for those
    // entries short of re-importing or applying a GDTF). The MVR/single-GDTF
    // import paths themselves DO populate and persist ChannelFunctions
    // correctly end to end (verified against the real Schaeffler show file:
    // imported entries came back from GET /api/patch and GET
    // /api/patch/attributes with populated groups) — so the six-group
    // accordion silently reading "no fixture in scope has a ... function"
    // for every group, with no explanation, was the actual UX bug: nothing
    // told the tech WHY it was empty or what to do about it.
    const fcWithChannelData = scoped.filter(e => e.channelFunctions && Object.keys(e.channelFunctions).length > 0).length;
    let noDataBanner = '';
    if (scoped.length > 0 && fcWithChannelData === 0) {
      noDataBanner = `
        <div class="b5-alert b5-alert--caution" style="margin-bottom:var(--b5-space-4)">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">None of the ${scoped.length} fixture${scoped.length === 1 ? '' : 's'} in scope have channel-function data</p>
            <p class="b5-text-sm">Rig Check's Function check needs each fixture's resolved channel functions (from a GDTF import, or RDM SLOT_INFO) — these entries have none, most likely because they were patched before this existed, or were hand-added without a GDTF match. Re-import via MVR, or use "Import GDTF&hellip;" on the Entries tab to apply a GDTF mode to these fixture types.</p>
            <button id="fcGoToEntries" class="b5-btn b5-btn--sm" style="margin-top:var(--b5-space-2)">Go to Entries</button>
          </div>
        </div>
      `;
    } else if (scoped.length > 0 && fcWithChannelData < scoped.length) {
      noDataBanner = `
        <div class="b5-alert b5-alert--caution" style="margin-bottom:var(--b5-space-4)">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__body">${scoped.length - fcWithChannelData} of ${scoped.length} fixtures in scope have no channel-function data yet, so they're absent from every group below — re-import via MVR or apply a GDTF to those fixture types for full coverage.</p></div>
        </div>
      `;
    }

    container.innerHTML = `
      <div class="b5-fc-stopbar">
        <span class="b5-text-sm b5-fc-stopbar__status">${running ? UI.badge('warning', 'Pattern running') : UI.badge('pending', 'Idle')}</span>
        <button id="fcStop" class="b5-btn b5-btn--danger" ${running ? '' : 'disabled'}>${UI.icon('status-error')}STOP</button>
        <button id="fcBlackout" class="b5-btn b5-btn--danger b5-btn--ghost">Blackout</button>
      </div>
      <div class="b5-alert b5-alert--info" style="margin-bottom:var(--b5-space-4)">
        ${UI.icon('status-pending')}
        <div><p class="b5-alert__body">Safe mode: only the group and function you start below are touched — nothing is auto-raised to "make it visible." Leaving this screen, closing the tab, or 5s of no response from this page always stops and blacks out.</p></div>
      </div>
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden" for="fcScopeKind">Scope</label>
          <select id="fcScopeKind" class="b5-select" style="width:auto" ${running ? 'disabled' : ''}>
            <option value="all">Scope: Whole rig</option>
            <option value="universe">Scope: One universe</option>
            <option value="position">Scope: One position</option>
            <option value="selection">Scope: Selected fixtures</option>
          </select>
          <div id="fcScopeValueWrap"></div>
        </div>
        <span class="b5-filterbar__summary">${scoped.length} fixture${scoped.length === 1 ? '' : 's'} in scope</span>
      </div>
      <div id="fcActiveWrap">${running ? renderFunctionCheckActive(patternState) : ''}</div>
      ${noDataBanner}
      <div class="b5-accordion" id="fcAttrTree"></div>
      <div id="fcActionPanel" style="margin-top:var(--b5-space-4)"></div>
    `;

    const goToEntriesBtn = document.getElementById('fcGoToEntries');
    if (goToEntriesBtn) goToEntriesBtn.addEventListener('click', () => setView('entries'));

    const scopeSel = document.getElementById('fcScopeKind');
    scopeSel.value = fcScopeKind;
    scopeSel.addEventListener('change', async (e) => {
      fcScopeKind = e.target.value;
      sessionStorage.setItem('benny512.patch.fcScopeKind', fcScopeKind);
      renderFcScopeValue(allEntries);
      await refreshFunctionCheckAttributes();
      renderFcAttrTree(fcScopedEntries(allEntries).length, openGroups);
      renderFcActionPanel();
    });
    renderFcScopeValue(allEntries);

    document.getElementById('fcStop').addEventListener('click', onFcStop);
    document.getElementById('fcBlackout').addEventListener('click', async () => {
      try { await Api.rigCheckBlackout(); setStatus('blackout'); stopPatternHeartbeat(); patternState = null; renderRigCheck(document.getElementById('patchViewBody')); } catch (e) { setStatus('error: ' + e.message); }
    });

    renderFcAttrTree(scoped.length, openGroups);
    renderFcActionPanel();
    if (running) wireFunctionCheckActiveHandlers();

    window.scrollTo(0, scrollY);
  }

  function renderFcScopeValue(allEntries) {
    const wrap = document.getElementById('fcScopeValueWrap');
    if (!wrap) return;
    const running = !!(patternState && patternState.running);
    if (fcScopeKind === 'universe') {
      const ua = UI.universeInputAttrs();
      wrap.innerHTML = `<label class="b5-visually-hidden" for="fcScopeUniverse">Universe</label><input type="number" id="fcScopeUniverse" class="b5-input" style="width:10em" min="${ua.min}" max="${ua.max}" value="${UI.formatUniverse(fcScopeUniverse)}" placeholder="Universe" ${running ? 'disabled' : ''}>`;
      document.getElementById('fcScopeUniverse').addEventListener('change', async (e) => {
        fcScopeUniverse = UI.parseUniverse(e.target.value);
        await refreshFunctionCheckAttributes();
        renderRigCheck(document.getElementById('patchViewBody'));
      });
    } else if (fcScopeKind === 'position') {
      const positions = fcPositions(allEntries);
      wrap.innerHTML = `<label class="b5-visually-hidden" for="fcScopePosition">Position</label><select id="fcScopePosition" class="b5-select" style="width:auto" ${running ? 'disabled' : ''}>
        ${positions.length ? positions.map(pos => `<option value="${escapeHtml(pos)}" ${pos === fcScopePosition ? 'selected' : ''}>${escapeHtml(pos)}</option>`).join('') : '<option value="">(no positions patched)</option>'}
      </select>`;
      if (!fcScopePosition && positions.length) fcScopePosition = positions[0];
      const sel = document.getElementById('fcScopePosition');
      sel.value = fcScopePosition;
      sel.addEventListener('change', async (e) => {
        fcScopePosition = e.target.value;
        await refreshFunctionCheckAttributes();
        renderRigCheck(document.getElementById('patchViewBody'));
      });
    } else if (fcScopeKind === 'selection') {
      wrap.innerHTML = `<div class="b5-stack" style="margin-top:var(--b5-space-2)">${allEntries.map(e => `
        <label class="b5-checkbox"><input type="checkbox" data-fc-select="${escapeHtml(e.id)}" ${fcSelection[e.id] ? 'checked' : ''} ${running ? 'disabled' : ''}>${escapeHtml(e.name || e.fixtureType || e.id)} (U${UI.formatUniverse(e.universe)}/${e.startAddress})</label>
      `).join('') || '<span class="b5-text-muted b5-text-sm">no entries</span>'}</div>`;
      wrap.querySelectorAll('[data-fc-select]').forEach(cb => cb.addEventListener('change', async (e) => {
        fcSelection[cb.dataset.fcSelect] = e.target.checked;
        await refreshFunctionCheckAttributes();
        renderRigCheck(document.getElementById('patchViewBody'));
      }));
    } else {
      wrap.innerHTML = '';
    }
  }

  // renderFcAttrTree: the six-group attribute menu (task ask). Each group
  // is a native <details>/.b5-accordion__trigger item (keyboard/AT support
  // for free, matching the established Reconcile-view pattern); its
  // summary line always states the "N of M fixtures" count so a mixed rig
  // is legible before any group is even opened.
  function renderFcAttrTree(scopeTotal, openGroups) {
    const tree = document.getElementById('fcAttrTree');
    if (!tree) return;
    if (fcAttrLoading || !fcAttrData) {
      tree.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading attributes…</span>`;
      return;
    }
    const summaryByGroup = {};
    (fcAttrData.summary.groups || []).forEach(g => { summaryByGroup[g.group] = g; });
    tree.innerHTML = FC_GROUPS.map(g => {
      const gs = summaryByGroup[g.id];
      const count = gs ? gs.count : 0;
      const kinds = FC_KINDS[g.id] || [];
      const funcs = gs ? gs.functions : [];
      return `
        <details class="b5-accordion__item" data-group="${g.id}" ${openGroups[g.id] ? 'open' : ''}>
          <summary class="b5-accordion__trigger">
            <span>${escapeHtml(g.label)} — ${count} of ${scopeTotal} fixture${scopeTotal === 1 ? '' : 's'}</span>
            ${UI.icon('chevron-expand')}
          </summary>
          <div class="b5-accordion__panel b5-stack">
            ${count === 0 ? `<p class="b5-text-muted b5-text-sm">No fixture in scope has a ${escapeHtml(g.label)} function.</p>` : `
              <ul class="b5-fc-funclist">
                ${funcs.map(f => `<li>${escapeHtml(f.attribute)} <span class="b5-text-muted b5-text-xs">— ${f.count} of ${scopeTotal}</span>${fcFunctionProvenanceBadges(g.id, f.attribute)}</li>`).join('')}
              </ul>
              <div class="b5-row" role="radiogroup" aria-label="${escapeHtml(g.label)} test">
                ${kinds.map(k => `<button type="button" class="b5-btn b5-btn--sm ${fcKindByGroup[g.id] === k.id ? 'b5-btn--primary' : ''}" data-fc-kind="${g.id}:${k.id}" aria-pressed="${fcKindByGroup[g.id] === k.id}">${escapeHtml(k.label)}</button>`).join('')}
              </div>
            `}
          </div>
        </details>
      `;
    }).join('');
    tree.querySelectorAll('[data-fc-kind]').forEach(btn => btn.addEventListener('click', () => {
      const [groupId, kindId] = btn.dataset.fcKind.split(':');
      fcGroup = groupId;
      fcKindByGroup[groupId] = kindId;
      sessionStorage.setItem('benny512.patch.fcGroup', fcGroup);
      const meta = fcKindMeta(kindId);
      fcParams.target = meta && meta.targets ? meta.targets[0].id : '';
      renderFcActionPanel();
    }));
  }

  // fcFunctionProvenanceBadges: per attribute, whether ANY entry in scope
  // supplies it via RDM-inferred data (task ask, item 3 — "that must be
  // visible, never passing as authoritative"). Text-labelled badge, never
  // color alone.
  function fcFunctionProvenanceBadges(groupId, attribute) {
    if (!fcAttrData || !fcAttrData.entries) return '';
    let inferred = false;
    fcAttrData.entries.forEach(e => (e.groups || []).forEach(g => {
      if (g.group !== groupId) return;
      (g.functions || []).forEach(f => { if (f.attribute === attribute && f.source === 'rdm-inferred') inferred = true; });
    }));
    return inferred ? ' ' + UI.badge('unknown', 'RDM-inferred') : '';
  }

  // renderFcActionPanel: the tunables for the currently-picked group+kind
  // (rate/range/direction/target/value, whichever the kind declares) plus
  // the Start button. oninput mutates fcParams only; the value commits on
  // Start (or, for a running pattern, on each control's onchange via
  // patternAdjust — a whole-value replace per the pattern contract, so
  // every adjust call resends the complete current fcParams).
  function renderFcActionPanel() {
    const panel = document.getElementById('fcActionPanel');
    if (!panel) return;
    const kindId = fcKindByGroup[fcGroup];
    const meta = fcKindMeta(kindId);
    const running = !!(patternState && patternState.running);
    const runningThisKind = running && patternState.kind === kindId;
    if (!meta) { panel.innerHTML = ''; return; }

    if (running && !runningThisKind) {
      panel.innerHTML = `
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__body">"${escapeHtml((fcKindMeta(patternState.kind) || {}).label || patternState.kind)}" is running. Stop it before starting a different test.</p></div>
        </div>
      `;
      return;
    }

    panel.innerHTML = `
      <div class="b5-panel">
        <div class="b5-panel__header"><h3 class="b5-panel__title">${escapeHtml(meta.label)}</h3></div>
        <div class="b5-panel__body b5-stack">
          <p class="b5-text-sm b5-text-muted">${escapeHtml(meta.hint || '')}</p>
          ${meta.hasTarget ? `
            <div class="b5-field">
              <span class="b5-field__label">${escapeHtml(meta.targetLabel || 'Target')}</span>
              <div class="b5-row" role="radiogroup" aria-label="${escapeHtml(meta.targetLabel || 'Target')}">
                ${meta.targets.map(t => `<button type="button" class="b5-btn b5-btn--sm ${fcParams.target === t.id ? 'b5-btn--primary' : ''}" data-fc-target="${escapeHtml(t.id)}" aria-pressed="${fcParams.target === t.id}">${escapeHtml(t.label)}</button>`).join('')}
              </div>
            </div>
          ` : ''}
          ${meta.hasValue ? `
            <div class="b5-field">
              <label class="b5-field__label" for="fcValue">Value</label>
              <div class="b5-row" style="align-items:center;gap:8px">
                <input type="range" id="fcValue" class="b5-range-touch" min="0" max="255" value="${fcParams.value}">
                <span class="b5-text-mono" id="fcValueOut">${fcParams.value}</span>
              </div>
            </div>
          ` : ''}
          ${meta.hasRate ? `
            <div class="b5-field">
              <label class="b5-field__label" for="fcRate">Rate (Hz)</label>
              <div class="b5-row" style="align-items:center;gap:8px">
                <input type="range" id="fcRate" class="b5-range-touch" min="0.05" max="5" step="0.05" value="${fcParams.rateHz}">
                <span class="b5-text-mono" id="fcRateOut">${Number(fcParams.rateHz).toFixed(2)} Hz</span>
              </div>
            </div>
          ` : ''}
          ${meta.hasRange ? `
            <div class="b5-field">
              <label class="b5-field__label" for="fcMin">Min level</label>
              <div class="b5-row" style="align-items:center;gap:8px">
                <input type="range" id="fcMin" class="b5-range-touch" min="0" max="255" value="${fcParams.min}">
                <span class="b5-text-mono" id="fcMinOut">${fcParams.min}</span>
              </div>
            </div>
            <div class="b5-field">
              <label class="b5-field__label" for="fcMax">Max level</label>
              <div class="b5-row" style="align-items:center;gap:8px">
                <input type="range" id="fcMax" class="b5-range-touch" min="0" max="255" value="${fcParams.max}">
                <span class="b5-text-mono" id="fcMaxOut">${fcParams.max}</span>
              </div>
            </div>
          ` : ''}
          ${meta.hasDirection ? `
            <div class="b5-field">
              <span class="b5-field__label">Direction</span>
              <div class="b5-row" role="radiogroup" aria-label="Direction">
                <button type="button" class="b5-btn b5-btn--sm ${fcParams.direction === 'cw' ? 'b5-btn--primary' : ''}" data-fc-direction="cw" aria-pressed="${fcParams.direction === 'cw'}">Clockwise</button>
                <button type="button" class="b5-btn b5-btn--sm ${fcParams.direction === 'ccw' ? 'b5-btn--primary' : ''}" data-fc-direction="ccw" aria-pressed="${fcParams.direction === 'ccw'}">Counter-clockwise</button>
              </div>
            </div>
          ` : ''}
          <div class="b5-row">
            ${runningThisKind
        ? `<span class="b5-text-sm b5-text-muted">Running — adjust a control above and it applies live.</span>`
        : `<button id="fcStart" class="b5-btn b5-btn--primary" ${fcStarting ? 'disabled' : ''}>${fcStarting ? UI.spinner() + 'Starting…' : UI.icon('apply') + 'Start test'}</button>`}
          </div>
          <span class="b5-field__error" id="fcError"></span>
        </div>
      </div>
    `;

    panel.querySelectorAll('[data-fc-target]').forEach(btn => btn.addEventListener('click', () => {
      fcParams.target = btn.dataset.fcTarget;
      if (runningThisKind) { onFcAdjust(); } else { renderFcActionPanel(); }
    }));
    panel.querySelectorAll('[data-fc-direction]').forEach(btn => btn.addEventListener('click', () => {
      fcParams.direction = btn.dataset.fcDirection;
      if (runningThisKind) { onFcAdjust(); } else { renderFcActionPanel(); }
    }));
    const valueInput = document.getElementById('fcValue');
    if (valueInput) {
      valueInput.addEventListener('input', (e) => { fcParams.value = Number(e.target.value); document.getElementById('fcValueOut').textContent = String(fcParams.value); });
      valueInput.addEventListener('change', () => { if (runningThisKind) onFcAdjust(); });
    }
    const rateInput = document.getElementById('fcRate');
    if (rateInput) {
      rateInput.addEventListener('input', (e) => { fcParams.rateHz = Number(e.target.value); document.getElementById('fcRateOut').textContent = Number(fcParams.rateHz).toFixed(2) + ' Hz'; });
      rateInput.addEventListener('change', () => { if (runningThisKind) onFcAdjust(); });
    }
    const minInput = document.getElementById('fcMin');
    const maxInput = document.getElementById('fcMax');
    if (minInput) {
      minInput.addEventListener('input', (e) => { fcParams.min = Number(e.target.value); document.getElementById('fcMinOut').textContent = String(fcParams.min); });
      minInput.addEventListener('change', () => { if (runningThisKind) onFcAdjust(); });
    }
    if (maxInput) {
      maxInput.addEventListener('input', (e) => { fcParams.max = Number(e.target.value); document.getElementById('fcMaxOut').textContent = String(fcParams.max); });
      maxInput.addEventListener('change', () => { if (runningThisKind) onFcAdjust(); });
    }
    const startBtn = document.getElementById('fcStart');
    if (startBtn) startBtn.addEventListener('click', onFcStart);
  }

  // onFcStart: starting a pattern moves real fixtures (task ask: "make
  // starting deliberate"). A native confirm() naming the exact scope and
  // test, mirroring every other apply-to-confirm action in this file.
  async function onFcStart() {
    const p = patchData.active ? patchData.patch : null;
    const allEntries = (p && p.entries) || [];
    const scoped = fcScopedEntries(allEntries);
    const kindId = fcKindByGroup[fcGroup];
    const meta = fcKindMeta(kindId);
    if (!meta) return;
    const errEl = document.getElementById('fcError');
    if (errEl) errEl.innerHTML = '';
    if (!scoped.length) {
      if (errEl) errEl.innerHTML = UI.icon('status-error') + 'nothing in scope — widen the scope first';
      return;
    }
    const scopeLabel = fcScopeKind === 'all' ? 'the whole rig'
      : fcScopeKind === 'universe' ? `universe ${UI.formatUniverse(fcScopeUniverse)}`
        : fcScopeKind === 'position' ? `position "${fcScopePosition}"`
          : `${scoped.length} selected fixture${scoped.length === 1 ? '' : 's'}`;
    if (!confirm(`Start "${meta.label}" (${fcGroup}) on ${scopeLabel}? This moves real fixture output now. Only fixtures with a ${fcGroup} function are touched — everything else is left untouched.`)) return;

    const body = {
      scopeKind: fcScopeKind, kind: kindId,
      rateHz: fcParams.rateHz, min: fcParams.min, max: fcParams.max,
      target: fcParams.target, direction: fcParams.direction, value: fcParams.value,
    };
    if (fcScopeKind === 'universe') body.universe = fcScopeUniverse;
    if (fcScopeKind === 'position') body.position = fcScopePosition;
    if (fcScopeKind === 'selection') body.entryIds = scoped.map(e => e.id);

    fcStarting = true;
    renderFcActionPanel();
    try {
      patternState = await Api.patternStart(body);
      fcStarting = false;
      ensurePatternHeartbeat();
      renderRigCheck(document.getElementById('patchViewBody'));
    } catch (e) {
      fcStarting = false;
      if (errEl) errEl.innerHTML = UI.icon('status-error') + escapeHtml(e.message);
      else setStatus('error: ' + e.message);
      renderFcActionPanel();
    }
  }

  // onFcAdjust: a running pattern's tunables are a whole-value replace
  // (patternAdjust contract — kind cannot change this way). Every control
  // change resends the complete current fcParams so nothing is silently
  // reset to a default the tech didn't choose.
  async function onFcAdjust() {
    try {
      patternState = await Api.patternAdjust({
        rateHz: fcParams.rateHz, min: fcParams.min, max: fcParams.max,
        target: fcParams.target, direction: fcParams.direction, value: fcParams.value,
      });
      renderFunctionCheckActiveInPlace();
    } catch (e) {
      setStatus('error: ' + e.message);
    }
  }

  async function onFcStop() {
    try {
      await Api.rigCheckStop();
      stopPatternHeartbeat();
      patternState = null;
      setStatus('function test stopped');
      renderRigCheck(document.getElementById('patchViewBody'));
    } catch (e) { setStatus('error: ' + e.message); }
  }

  // renderFunctionCheckActive: the running-pattern status card — always
  // shows applied/skipped counts against the scope total (task ask, item
  // 2: "show the count"), the RDM-inferred count (item 3), and the
  // missing-ChannelSet-detail count (a colour wheel/gobo wheel with no
  // ChannelSet data is sweeping a numeric range rather than stepping real
  // named slots — the tech should know that's what "step" means here).
  function renderFunctionCheckActive(st) {
    const meta = fcKindMeta(st.kind) || { label: st.kind };
    const elapsedS = ((st.elapsedMs || 0) / 1000).toFixed(1);
    return `
      <div class="b5-card">
        <div class="b5-row" style="justify-content:space-between;flex-wrap:wrap">
          <strong>${escapeHtml(meta.label)}</strong>
          <span class="b5-text-muted b5-text-sm">${elapsedS}s elapsed</span>
        </div>
        <div class="b5-row" style="margin-top:var(--b5-space-2);flex-wrap:wrap;gap:8px">
          ${UI.badge('ok', `${st.appliedCount} of ${st.totalScope} fixtures applied`)}
          ${st.skippedCount ? UI.badge('warning', `${st.skippedCount} skipped (no ${escapeHtml(st.group || fcGroup)} function)`) : ''}
          ${st.inferredCount ? UI.badge('unknown', `${st.inferredCount} RDM-inferred (approximate)`) : ''}
          ${st.missingDetailCount ? UI.badge('warning', `${st.missingDetailCount} sweeping a range — no named slot data`) : ''}
        </div>
        <p class="b5-text-sm b5-text-muted" style="margin-top:var(--b5-space-2)">rate ${st.rateHz} Hz${st.target ? ` &middot; target ${escapeHtml(st.target)}` : ''}${meta.hasValue ? ` &middot; value ${st.value}` : ''}${meta.hasRange ? ` &middot; ${st.min}&ndash;${st.max}` : ''}${meta.hasDirection ? ` &middot; ${st.direction === 'ccw' ? 'counter-clockwise' : 'clockwise'}` : ''}</p>
        ${st.entries && st.entries.length ? `
          <details class="b5-accordion__item" style="margin-top:var(--b5-space-2)">
            <summary class="b5-accordion__trigger"><span>Per-fixture detail (${st.entries.length})</span>${UI.icon('chevron-expand')}</summary>
            <div class="b5-accordion__panel">
              <ul class="b5-fc-funclist">
                ${st.entries.map(en => fcEntryStatusLine(en)).join('')}
              </ul>
            </div>
          </details>
        ` : ''}
      </div>
    `;
  }

  function fcEntryStatusLine(en) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const e = entries.find(x => x.id === en.entryId);
    const label = e ? (e.name || e.fixtureType || e.id) : en.entryId;
    const status = en.applied ? UI.badge('ok', 'Applied') : UI.badge('pending', 'Skipped');
    const inferred = en.inferred ? ' ' + UI.badge('unknown', 'RDM-inferred') : '';
    const missing = en.detailMissing ? ' ' + UI.badge('warning', 'No slot data') : '';
    return `<li>${escapeHtml(label)} — ${status}${inferred}${missing}</li>`;
  }

  function renderFunctionCheckActiveInPlace() {
    const wrap = document.getElementById('fcActiveWrap');
    if (!wrap) return;
    if (!patternState || !patternState.running) { wrap.innerHTML = ''; return; }
    wrap.innerHTML = renderFunctionCheckActive(patternState);
    wireFunctionCheckActiveHandlers();
    // Refresh the stop bar's status badge in place too, without touching
    // scroll/focus elsewhere on the panel.
    const statusEl = document.querySelector('.b5-fc-stopbar__status');
    if (statusEl) statusEl.innerHTML = UI.badge('warning', 'Pattern running');
  }

  function wireFunctionCheckActiveHandlers() {
    // Currently no interactive controls inside the active-status card
    // itself (Stop/Blackout live in the sticky stop bar, adjust controls
    // live in the action panel) — kept as a named hook, mirroring
    // wireRigCheckActiveHandlers, so future per-fixture actions (e.g.
    // Identify on a skipped fixture) have an obvious place to wire up.
  }

  return { init, onEnterScreen, onLeaveScreen };
})();
