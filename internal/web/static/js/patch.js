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
  let patchCatalog = [];
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
  // No `reconcile` mirror here any more: ReconcilePanel (reconcile.js) holds
  // the only copy of the reconcile board, per its rule 1.
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

  // Function check state — the attribute-level test-pattern engine — now
  // lives entirely in rigcheck.js (RigCheckPanel), which owns its own
  // server snapshot, scope and rendering. It is reached through a sub-tab
  // (rcSubView) inside the Rig Check screen so the classic channel-level
  // check keeps working unchanged and reachable (additive, not a
  // replacement).
  //
  // Nothing about the pattern engine's state is mirrored in this file. This
  // screen asks RigCheckPanel.outputEnabled() when it needs to know whether
  // pattern output is flowing (the classic channel-level mutators are
  // mutually exclusive with it server-side), and that answer comes from the
  // panel's last server snapshot rather than from a second copy kept here.
  let rcSubView = sessionStorage.getItem('benny512.patch.rcSubView') || 'classic'; // 'classic' | 'function'

  // --- lifecycle ------------------------------------------------------------

  function init() {
	window.addEventListener('b5-selection', e => {
	  selectedIds = Object.fromEntries(e.detail.map(id => [id, true]));
	});
	window.addEventListener('b5-show-changed', () => {
	  clearSelection(); editingEntry = null; entryDraft = null;
	  mvrPreview = null; gdtfPreview = null; rcSelection = {};
	});
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
    // The Function check sub-screen (rigcheck.js) needs four things this
    // screen owns: the patch's entries, the shared status line, a way back
    // to the Entries tab, and a way to ask for a full re-render.
    RigCheckPanel.init({
      getEntries: () => (patchData.active && patchData.patch ? patchData.patch.entries || [] : []),
      setStatus: (m) => setStatus(m),
      goToEntries: () => setView('entries'),
    });
    // The Reconcile screen (reconcile.js) owns its own server snapshot the
    // same way RigCheckPanel does, so it needs only the shared status line
    // and the word this install uses for a universe — it never formats a
    // universe number itself without going through UI.formatUniverse.
    ReconcilePanel.init({
      setStatus: (m) => setStatus(m),
      getUniverseLabel: () => 'Universe',
      // Every Reconcile mutation can change the PATCH (commit/decommit set
      // ConfirmedUID + MatchState; adopt writes intended settings and Mode),
      // and this screen's Entries table, collision banner and Rig Check
      // scope pickers all render from `patchData`. See refreshPatchDataOnly.
      onPatchChanged: () => refreshPatchDataOnly(),
    });
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
    RigCheckPanel.leave();
    // Best-effort, always — whether or not the Rig Check sub-view was the
    // one on screen, the running check must never keep lighting the rig
    // after the tech has navigated away (task ask: "never leave the rig
    // lit"). A no-op server-side if nothing is running — covers both the
    // classic per-entry check AND a running function-pattern (POST
    // .../rigcheck/stop stops either, per the pattern engine's contract).
    Api.rigCheckStop().catch(() => {});
  }

  // stopPatternHeartbeat: the pattern engine's client-liveness poll lives
  // in RigCheckPanel now; this thin delegate keeps the existing lifecycle
  // call sites (visibilitychange / pagehide / onLeaveScreen / setView)
  // saying exactly what they said before.
  function stopPatternHeartbeat() { RigCheckPanel.stopPolling(); }

  // stopPatternOutputBeforeLeavingRigCheck: the Function check panel's
  // client-liveness heartbeat is what keeps the server's pattern watchdog
  // fed, and this screen stops that heartbeat the moment the panel is no
  // longer on screen (setView away from Rig Check, setRcSubView away from
  // Function). Stopping the heartbeat WITHOUT stopping the output is a seam:
  //
  //   - the rig keeps sweeping for up to PatternWatchdogTimeout (5s) with
  //     nothing on screen saying so, which is exactly the "never leave the
  //     rig lit" hazard this screen's own safety copy promises against;
  //   - then the watchdog blacks out and stamps lastEndReason "watchdog",
  //     i.e. "this page went more than 5s without reaching the server" —
  //     a false diagnosis, since the page is alive and just showing a
  //     different sub-tab;
  //   - and the Channel check sub-view, which renders "A Function check
  //     pattern is running" with its own controls disabled off
  //     RigCheckPanel.outputEnabled(), never learns about that blackout
  //     (its snapshot only refreshes while the panel polls), so it sits
  //     behind a stale banner with Scope/Mode/Start disabled forever.
  //
  // Output therefore ALWAYS ends within 5s of leaving the Rig Check view
  // either way — this just makes it end deterministically, immediately, and
  // honestly ("manual"), and leaves RigCheckPanel's snapshot correct so the
  // Channel check sub-view renders the truth. Selection is untouched: this
  // is POST .../pattern/output false, not a deselect.
  async function stopPatternOutputBeforeLeavingRigCheck() {
    if (!RigCheckPanel.outputEnabled()) return;
    try { await RigCheckPanel.stopOutput(); } catch (e) { /* best-effort; onLeaveScreen's rigCheckStop is the backstop */ }
  }

  // --- data refresh -----------------------------------------------------

  async function refresh() {
    try {
      const data = await Promise.all([Api.getPatch(), Api.getPatches()]);
      patchData = data[0];
      patchCatalog = (data[1] && data[1].patches) || [];
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

  // refreshReconcile: the Reconcile view's data now lives entirely in
  // ReconcilePanel (reconcile.js), which fetches the whole two-pane board in
  // one request and keeps no mirror of it here. This screen only forwards
  // the refresh so the outer tab switcher keeps working.
  async function refreshReconcile() {
    await ReconcilePanel.refresh();
  }

  // refreshPatchDataOnly: refetch THIS screen's own copy of the patch (and
  // its collisions) without disturbing either sub-panel's snapshot.
  //
  // Reconcile's mutators write to the patch — commit/decommit set the
  // entry's ConfirmedUID and MatchState, adopt writes intended settings and
  // the entry's Mode. reconcile.js correctly re-reads its OWN board after
  // every one of them (its rule 1), but the board is not the patch: the
  // Entries table, the collision banner and both Rig Check scope pickers all
  // render from `patchData`, which nothing told to refetch. Committing on
  // Reconcile and then switching to Entries therefore showed the entry still
  // "Unresolved" until the whole Patch tab was left and re-entered. Both
  // halves were self-consistent; the patch snapshot fell between them.
  //
  // Deliberately does NOT re-render while the Reconcile view is the one on
  // screen: the panel has just repainted itself from its own fresh board,
  // and a second render() here would only throw that DOM away (losing the
  // scroll position of a two-pane board mid-commit) to paint the same thing.
  // setView() renders from `patchData` on the way into Entries/Rig Check, so
  // refreshing the data is all that is needed for those to be correct.
  async function refreshPatchDataOnly() {
    try {
      const data = await Promise.all([Api.getPatch(), Api.getPatches()]);
      patchData = data[0];
      patchCatalog = (data[1] && data[1].patches) || [];
    } catch (e) {
      return; // best-effort: keep the last good snapshot rather than blanking the screen
    }
    try {
      collisions = patchData.active ? (await Api.getPatchCollisions() || []) : [];
    } catch (e) { /* best-effort */ }
    if (view !== 'reconcile') render();
  }

  async function refreshRigCheck() {
    try {
      rigCheckState = await Api.getRigCheckState();
    } catch (e) { /* best-effort */ }
    // The pattern engine's status is fetched regardless of which Rig Check
    // sub-view is on screen — the Channel check sub-view needs to know
    // pattern output is flowing too, so it can warn rather than let a click
    // surface a raw 409 ("handle gracefully rather than showing a raw
    // error"). RigCheckPanel owns that snapshot; this just asks it to
    // refresh. The watchdog heartbeat poll itself is the panel's business.
    await RigCheckPanel.refreshStatus();
  }

  async function setView(v) {
    if (v !== 'rigcheck') {
      await stopPatternOutputBeforeLeavingRigCheck();
      RigCheckPanel.leave();
    }
    if (v !== 'reconcile') ReconcilePanel.leave();
    view = v;
    render();
    if (v === 'reconcile') {
      const body = document.getElementById('patchViewBody');
      // enter() paints its own loading state, fetches the board and
      // re-renders — the panel owns that whole cycle (reconcile.js rule 1).
      if (body) ReconcilePanel.enter(body);
    }
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
    else if (view === 'reconcile') { ReconcilePanel.attach(body); ReconcilePanel.render(); }
    else renderRigCheck(body);
  }

  // ============================================================
  // Entries view
  // ============================================================

  // renderEntries — converted to the shared screen kit (css/DESIGN.md).
  //
  // The two old .b5-filterbar rows (the desktop-first chip bar the kit is
  // replacing) are now two numbered .b5-step-section bands with .b5-toolbar
  // rows inside them, so the screen reads top to bottom: what patch is
  // this and where do entries come from (1), then what you are looking at
  // (2), then the entries themselves (3).
  //
  // THE TABLE STAYS A TABLE. DESIGN.md's worked example ends by blessing
  // exactly this case — "genuinely tabular data a tech scans down" — and a
  // 200-fixture patch drawn as .b5-statecards would be about six entries
  // per screen. It keeps .b5-table--responsive + data-label on every cell
  // so it stacks on a narrow pane, and it now sits in a .b5-scrollbox so a
  // wide row scrolls inside its own box and never takes the page sideways
  // (rule 4).
  function renderEntries(body) {
    const p = patchData.active ? patchData.patch : null;
    const entries = (p && p.entries) || [];
    const confirmedCount = entries.filter(e => e.confirmedUid).length;
    const activeRef = patchCatalog.find(ref => ref.active);
    const savedShows = patchCatalog.map(ref => `<option value="${escapeHtml(ref.id)}"${ref.active ? ' selected' : ''}>${escapeHtml(ref.name || '(unnamed show)')}</option>`).join('');
    body.innerHTML = `
      <section class="b5-step-section" aria-labelledby="patchSourceHead">
        <h2 class="b5-step-section__head" id="patchSourceHead">
          <span class="b5-step-num">1</span> This patch
          <span class="b5-step-section__note">${p
            ? escapeHtml(p.name || '(unnamed)') + ' · ' + entries.length + ' entr' + (entries.length === 1 ? 'y' : 'ies') + ' · ' + confirmedCount + ' matched to a real fixture'
            : 'no patch yet'}</span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row">
            <label class="b5-toolbar__search" for="patchShowSelect">
              <span class="b5-visually-hidden">Active show</span>
              <select id="patchShowSelect" class="b5-select" ${savedShows ? '' : 'disabled'}>
                ${savedShows || '<option>No saved show</option>'}
              </select>
            </label>
            <button id="btnCreateSavedPatch" class="b5-btn">New show&hellip;</button>
            <button id="btnAddEntry" class="b5-btn b5-btn--primary">${UI.icon('apply')}Add entry</button>
            <button id="btnAdoptMerge" class="b5-btn">Adopt discovered (merge)</button>
            <button id="btnAdoptFresh" class="b5-btn b5-btn--danger">Adopt discovered (replace patch)</button>
            <button id="btnImportMvr" class="b5-btn">Import MVR&hellip;</button>
            <button id="btnImportGdtf" class="b5-btn">Import GDTF&hellip;</button>
            <button id="btnExportPatchJson" class="b5-btn">${UI.icon('export')}Export JSON</button>
            <button id="btnExportPatchTxt" class="b5-btn">${UI.icon('export')}Export TXT</button>
          </div>
          <p class="b5-caption">Universe numbers on this screen are ${escapeHtml(UI.universeBaseLabel())} &mdash; the same numbering as Send, Nodes, Devices and Rig Walk. Change it on Settings.</p>
        </div>
      </section>

      ${renderCollisionBanner(collisions)}
      <div id="mvrImportPreview"></div>
      <div id="gdtfImportPreview"></div>

      <section class="b5-step-section" aria-labelledby="patchEntriesHead">
        <h2 class="b5-step-section__head" id="patchEntriesHead">
          <span class="b5-step-num">2</span> Entries
          <span class="b5-step-section__note" id="patchEntriesNote"></span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row">
            <label class="b5-toolbar__search">
              <span class="b5-visually-hidden">Filter entries</span>
              <input type="search" id="patchFilter" placeholder="Filter by name, type, position, fixture #…" value="${escapeHtml(filterText)}">
            </label>
            <label class="b5-visually-hidden" for="patchSort">Sort</label>
            <select id="patchSort" class="b5-select">
              <option value="address">Sort: Universe + address</option>
              <option value="name">Sort: Name</option>
              <option value="type">Sort: Fixture type</option>
              <option value="fixtureNumber">Sort: Fixture number</option>
            </select>
          </div>
        </div>
        <div class="b5-panel">
          <div class="b5-panel__body--flush">
            <div class="b5-scrollbox">
              <table class="b5-table b5-table--responsive" id="patchEntriesTable">
                <thead><tr>
                  <th><label class="b5-checkbox"><input type="checkbox" id="patchSelectAll" aria-label="Select all in view"><span class="b5-visually-hidden">Select all in view</span></label></th>
                  <th>Universe</th><th>Address</th><th>Name</th><th>Fixture type</th><th>Fixture #</th><th>RDM match</th><th>Actions</th>
                </tr></thead>
                <tbody></tbody>
              </table>
            </div>
          </div>
        </div>
      </section>
      <div id="patchEntryEditor" class="b5-patch-editor"></div>
    `;
    document.getElementById('patchSort').value = sortMode;

    document.getElementById('btnCreateSavedPatch').addEventListener('click', async () => {
      const name = prompt('Name the new show:', 'New show');
      if (name === null) return;
      try {
        patchData = await Api.createSavedPatch(name);
        setStatus('new show created');
        editingEntry = null; entryDraft = null;
        await refresh();
      } catch (e) { setStatus('error: ' + e.message); }
    });
    const showSelect = document.getElementById('patchShowSelect');
    if (showSelect && activeRef) {
      showSelect.addEventListener('change', async () => {
        if (showSelect.value === activeRef.id) return;
        try {
          patchData = await Api.loadSavedPatch(showSelect.value);
          editingEntry = null; entryDraft = null;
          setStatus('show loaded');
          await refresh();
        } catch (e) {
          setStatus('error: ' + e.message);
          await refresh();
        }
      });
    }
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
          <div class="b5-panel__body--flush b5-scrollbox" style="max-height:40vh">
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
        <div class="b5-panel__body--flush b5-scrollbox" style="max-height:40vh">
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
    // The section head says what is on screen right now, in words — how many
    // of how many, and how many are selected — so the count is never
    // something the eye has to work out from the rows.
    const note = document.getElementById('patchEntriesNote');
    if (note) {
      const sel = selectedCount();
      note.textContent =
        (filterText ? `${sorted.length} of ${entries.length} shown · filtered` : `${entries.length} entr${entries.length === 1 ? 'y' : 'ies'}`) +
        (sel ? ` · ${sel} selected` : '');
    }
    if (!sorted.length) {
      tbody.innerHTML = `<tr><td colspan="8"><div class="b5-empty">${UI.icon('nav-devices')}<span class="b5-empty__title">${entries.length ? 'No entries match the filter' : 'No entries yet'}</span><span class="b5-empty__body">${entries.length ? 'Nothing in this patch matches "' + escapeHtml(filterText) + '". Clear the filter above to see all ' + entries.length + ' entries.' : 'This patch has no fixtures in it. Press "Add entry" to write one by hand, "Adopt discovered" to build entries from what is answering on the network right now, or import an MVR or GDTF file.'}</span></div></td></tr>`;
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
        window.dispatchEvent(new CustomEvent('b5-patch-selection', {detail:Object.keys(selectedIds)}));
        renderEntriesTableBody();
        return;
      }
    }
    if (evt.currentTarget.checked) selectedIds[id] = true; else delete selectedIds[id];
    window.dispatchEvent(new CustomEvent('b5-patch-selection', {detail:Object.keys(selectedIds)}));
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
      window.dispatchEvent(new CustomEvent('b5-patch-selection', {detail:Object.keys(selectedIds)}));
      renderEntriesTableBody();
    };
  }

  // renderEntryRow: one dense scannable row. Every state on it is carried by
  // a WORD in the kit's one pill component (never a bare coloured word, and
  // never colour alone — rule 1), and every "we don't know" says so out loud
  // rather than printing a plausible number (rule 3):
  //
  //   - RDM match: a --ok pill "Confirmed", a --warn pill "Suggestions
  //     rejected", or a dashed --open pill "No fixture matched yet". The
  //     last one used to be the muted grey word "Unresolved", which
  //     desaturates to look exactly like a disabled control.
  //   - Address: an entry with no start address prints "no address set",
  //     not "0". Api.formatAddressRange's own `known` flag is what says so;
  //     it was being passed a hard-coded `true` here, which meant a
  //     never-addressed entry rendered as a real address of 0.
  //   - Name / type / fixture number: an em dash is fine in a dense column
  //     the header already names, but the NAME column falls back to the
  //     fixture type and finally says "unnamed entry", because a blank
  //     leading cell is the one that reads as a broken row.
  function renderEntryRow(e, findings) {
    const issue = findings.length
      ? `<div class="b5-field__error">${UI.icon('status-warning')}${findings.map(f => escapeHtml(f.kind)).join(', ')}</div>`
      : '';
    let match;
    if (e.confirmedUid) {
      // The word stays "Confirmed": it is the server's own vocabulary for
      // this state (patch.Entry.MatchState "confirmed" / ConfirmedUID), it
      // is what Reconcile's own board says, and patch_lifecycle_test.js
      // asserts the Entries table shows it. Only the drawing changed.
      match = `<span class="b5-pill b5-pill--tag b5-pill--ok">${UI.icon('status-ok')}Confirmed</span>`;
    } else if (e.matchState === 'rejected') {
      match = `<span class="b5-pill b5-pill--tag b5-pill--warn">${UI.icon('status-warning')}Suggestions rejected</span>`;
    } else {
      match = `<span class="b5-pill b5-pill--tag b5-pill--open">No fixture matched yet</span>`;
    }
    const hasAddr = !!e.startAddress;
    const addr = hasAddr
      ? escapeHtml(Api.formatAddressRange(e.startAddress, e.footprint, true))
      : '<span class="b5-text-muted">no address set</span>';
    const selected = !!selectedIds[e.id];
    const label = e.name || e.fixtureType || 'unnamed entry';
    return `
      <tr data-entry-id="${escapeHtml(e.id)}" class="${selected ? 'b5-patch-row--selected' : ''}">
        <td data-label="Select"><label class="b5-checkbox"><input type="checkbox" id="sel-${escapeHtml(e.id)}" data-row-select="${escapeHtml(e.id)}" ${selected ? 'checked' : ''} aria-label="Select ${escapeHtml(label)}"><span class="b5-visually-hidden">Select ${escapeHtml(label)}</span></label></td>
        <td data-label="Universe" class="b5-table__mono">${escapeHtml(UI.formatUniverse(e.universe))}</td>
        <td data-label="Address" class="b5-table__mono">${addr}${issue}</td>
        <td data-label="Name">${escapeHtml(e.name || '')}${e.name ? '' : `<span class="b5-text-muted">${escapeHtml(e.fixtureType ? e.fixtureType + ' (no name)' : 'unnamed entry')}</span>`}</td>
        <td data-label="Fixture type">${escapeHtml(e.fixtureType || '—')}</td>
        <td data-label="Fixture #">${escapeHtml(e.fixtureNumber || '—')}</td>
        <td data-label="RDM match">${match}</td>
        <td data-label="Actions">
          <button class="b5-btn b5-btn--sm" data-edit="${escapeHtml(e.id)}">${UI.icon('apply')}Edit</button>
          <button class="b5-btn b5-btn--sm b5-btn--icon" data-move-up="${escapeHtml(e.id)}" aria-label="Move ${escapeHtml(label)} up">&uarr;</button>
          <button class="b5-btn b5-btn--sm b5-btn--icon" data-move-down="${escapeHtml(e.id)}" aria-label="Move ${escapeHtml(label)} down">&darr;</button>
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
    entryDraft = { name: '', fixtureType: '', mode: '', footprint: 0, universe: 0, startAddress: 1, position: '', fixtureNumber: '', notes: '', phaseCount: 0 };
    renderEntryEditor();
  }

  function openEditEntry(e) {
    if (!e) return;
    editingEntry = e.id;
    entryDraft = {
      name: e.name || '', fixtureType: e.fixtureType || '', mode: e.mode || '',
      footprint: e.footprint || 0, universe: e.universe || 0, startAddress: e.startAddress || 1,
      position: e.position || '', fixtureNumber: e.fixtureNumber || '', notes: e.notes || '',
      phaseCount: e.phaseCount || 0,
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
          <span class="b5-pill b5-pill--tag b5-pill--open">Staged &mdash; not saved</span>
          <button id="peClose" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Close editor">Close</button>
        </div>
        <div class="b5-panel__body b5-grid-2">
          <div class="b5-field"><label class="b5-field__label" for="peName">Name</label><input id="peName" class="b5-input" type="text" maxlength="64"></div>
          <div class="b5-field"><label class="b5-field__label" for="peType">Fixture type</label><input id="peType" class="b5-input" type="text" maxlength="80" placeholder="e.g. Chauvet Rogue Outcast 2X Wash"></div>
          <div class="b5-field"><label class="b5-field__label" for="peMode">Mode / personality</label><input id="peMode" class="b5-input" type="text" maxlength="40"></div>
          <div class="b5-field"><label class="b5-field__label" for="peFootprint">Footprint (DMX channels)</label><input id="peFootprint" class="b5-input" type="number" min="0" max="512"></div>
          <div class="b5-field"><label class="b5-field__label" for="pePhaseCount">Phase slots (0 = auto)</label><input id="pePhaseCount" class="b5-input" type="number" min="0" max="512"><span class="b5-caption">Function-test spacing only</span></div>
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
    bind('pePhaseCount', 'phaseCount', true);
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
          <span class="b5-pill b5-pill--tag b5-pill--open">Staged &mdash; each field applies on its own press</span>
          <button id="bulkClose" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Close editor">Close</button>
        </div>
        <div class="b5-panel__body b5-stack">
          <p class="b5-note">These ${n} entries will change: ${selected.map(e => escapeHtml(e.name || e.fixtureType || e.id)).join(', ')}. Typing changes nothing &mdash; each Apply below asks you to confirm exactly what it will do before it writes anything.</p>
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
  //
  // Everything that used to live here — a read-only accordion grouped by
  // matcher status, plus per-row fix / fix-all — now lives in reconcile.js
  // (ReconcilePanel), extracted exactly the way rigcheck.js was and for the
  // same reason: the old view could not express what the owner asked for.
  // See that file's header for the diagnosis; the short version is that its
  // two largest groups ("Matched", "Unpatched") rendered zero buttons, so
  // neither committing a pairing nor breaking one was reachable from the
  // screen named after those two verbs.
  //
  // Deliberately NOT carried over: the "Fix all address mismatches" bulk
  // button. Its endpoint (POST /api/patch/reconcile/fix-all) and tests are
  // untouched and it remains callable, but a button that writes a new DMX
  // address to every mismatched fixture in one press is a different contract
  // from the per-difference "Apply to fixture" the owner settled on, and
  // having both on one screen would have made the safe one look optional.

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
    if (rcSubView === 'function') RigCheckPanel.attach(sub);
    else renderClassicRigCheck(sub);
  }

  async function setRcSubView(v) {
    if (v === rcSubView) return;
    if (v !== 'function') {
      await stopPatternOutputBeforeLeavingRigCheck();
      stopPatternHeartbeat();
    }
    rcSubView = v;
    sessionStorage.setItem('benny512.patch.rcSubView', v);
    render();
    // Entering the Function check hands the panel its container and lets it
    // (re)establish the scope; it renders itself from whatever the server
    // answers with. Leaving it stops the panel's watchdog poll.
    if (v === 'function') RigCheckPanel.enter(document.getElementById('rcSubBody'));
    else RigCheckPanel.leave();
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
    const patternRunning = RigCheckPanel.outputEnabled();

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
      try { await RigCheckPanel.stopOutput(); setStatus('function test output stopped'); await refreshRigCheck(); render(); } catch (e) { setStatus('error: ' + e.message); }
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


  async function focusEntry(targetView, id) {
    await refreshPatchDataOnly();
    await setView(targetView === 'reconcile' ? 'reconcile' : 'entries');
    if (targetView === 'reconcile') ReconcilePanel.focusEntry(id);
    else { const entry=(patchData.patch?.entries||[]).find(e=>e.id===id); if(entry) openEditEntry(entry); }
  }
  async function openFunctionCheck() {
    await refreshPatchDataOnly();
    await RigCheckPanel.refreshStatus();
    rcSubView='function';
    await setView('rigcheck');
  }
  return { init, onEnterScreen, onLeaveScreen, focusEntry, openFunctionCheck };
})();
