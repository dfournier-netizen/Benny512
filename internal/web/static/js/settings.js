// settings.js — Settings screen: NIC picker, poll interval, universe
// numbering base, capture limit, RDM log path, and the danger-zone full
// reset. NIC list is populated from GET /api/nics
// (internal/transport.ListInterfaces); current.nic is matched against the
// real interface list by name, falling back to a synthesized option if the
// previously-saved NIC name isn't present on this machine (e.g.
// settings.json carried over from another host).
//
// CONVERTED TO THE SHARED SCREEN KIT (css/DESIGN.md). The markup is built
// here rather than in index.html — the same move analyzer.js made, and for
// the same reason: a screen cannot use the kit if its skeleton is frozen in
// a static file. Every id the old markup carried is unchanged.
//
// Two things about this screen make it different from the others:
//
//  1. IT IS THE SOURCE OF THE UNIVERSE DISPLAY BASE. UI.setUniverseBase is
//     what fires 'b5-universe-base-changed', and every other screen listens
//     for it to re-render its universe numbers (and, on Nodes and Send, to
//     rewrite a staged editor field from its canonical value). So this
//     screen must keep calling it on load AND on save — and it does both,
//     below, exactly where it did before.
//
//  2. APPLY-TO-CONFIRM IS SCREEN-WIDE, NOT PER-FIELD. Every control here
//     is oninput-mutates-the-DOM-only until "Apply settings" is pressed;
//     save() is the one and only place any setting takes effect. The kit's
//     per-field Apply (UI.buildApplyField) would be the wrong shape here —
//     these settings are saved as ONE document by POST /api/settings, so a
//     per-field Apply would send the other fields' half-typed values with
//     it. What is added instead is an honest UNSAVED marker: the step head
//     and the action bar both say, in a word, that there are staged changes
//     the server has not been told about.
//
// The "Default timeout profile" control that used to sit in this screen's
// static markup has been replaced by a plain statement of fact. It was
// never read and never written: settings.js's save() has always sent
// `timeoutProfiles: current.timeoutProfiles` verbatim, and Settings.
// TimeoutProfiles (server.go) is a per-node-key map, not a global default,
// so the select could not have driven it even if something had read it. A
// control that does nothing is worse than no control at all in the dark —
// rule 3, name the unknown rather than showing a plausible one.
const SettingsScreen = (() => {
  let current = null;
  let nics = [];

  // baseline: the last SAVED settings, as strings, keyed by element id. The
  // dirty marker compares live inputs against this — never against the
  // previous keystroke, so reverting a field by hand clears the marker.
  let baseline = {};

  const FIELD_IDS = ['nicSelect', 'pollInterval', 'captureLimit', 'logRdmPath', 'universeBase'];

  async function refresh() {
    [current, nics] = await Promise.all([Api.getSettings(), Api.getNICs().catch(() => [])]);
    render();
  }

  // --- markup ---------------------------------------------------------------

  function screenHtml() {
    return `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Settings</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">Nothing here takes effect until you press Apply.</span>
      </div>

      <section class="b5-step-section" aria-labelledby="setNetHead">
        <h2 class="b5-step-section__head" id="setNetHead">
          <span class="b5-step-num">1</span> Network
          <span class="b5-step-section__note" id="setNetNote">which card Benny512 talks Art-Net on</span>
        </h2>
        <div class="b5-group">
          <h3 class="b5-group__head">Interface</h3>
          <div class="b5-field">
            <label class="b5-field__label" for="nicSelect">Network interface (NIC)</label>
            <select id="nicSelect" class="b5-select"></select>
            <span class="b5-field__hint">If nodes never answer a poll, this is the first thing to check — it must be the card on the lighting network, not the one with the internet on it.</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="pollInterval">Poll interval (ms)</label>
            <input type="number" id="pollInterval" class="b5-input b5-input--mono" min="500" step="500">
            <span class="b5-field__hint">How often an ArtPoll goes out. Lower finds a node that just came up sooner; higher is quieter on a busy show network.</span>
          </div>
        </div>
        <div class="b5-inset">
          <p class="b5-inset__head">${UI.icon('status-pending')}RDM timeout profile</p>
          <p class="b5-note">There is no global default to set here. A timeout profile (Direct or Wireless&nbsp;BiDi proxy) is held per node, keyed by that node&rsquo;s own address, and is chosen on the Nodes screen for the node you are working through. This screen used to show a &ldquo;Default timeout profile&rdquo; picker that was wired to nothing at all &mdash; it has been removed rather than left looking like it did something.</p>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setUniHead">
        <h2 class="b5-step-section__head" id="setUniHead">
          <span class="b5-step-num">2</span> Universe numbering
          <span class="b5-step-section__note" id="setUniNote"></span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="universeBase">Universe numbering starts at</label>
          <select id="universeBase" class="b5-select">
            <option value="0">Art-Net native &mdash; first universe is 0</option>
            <option value="1">Industry standard &mdash; first universe is 1</option>
          </select>
        </div>
        <div class="b5-inset">
          <p class="b5-inset__head">${UI.icon('apply')}What this changes, and what it never changes</p>
          <p class="b5-note" id="universeBaseExample"></p>
          <p class="b5-note">Display only. Every universe number shown or typed anywhere in Benny512 &mdash; Patch, Send, Devices, Rig Walk, Rig Check, the Analyzer and every import preview &mdash; is renumbered from this base the moment you apply it. Nothing sent on the wire or stored in the patch changes. Obsidian EN4 and Vectorworks number from 1; Art-Net itself numbers from 0, hence the choice.</p>
          <p class="b5-note">One exception, deliberately: the Nodes screen&rsquo;s port <em>configuration</em> block still shows Art-Net&rsquo;s own raw Net / Sub-Net / Universe fields, because those are the three numbers printed on the gateway&rsquo;s own faceplate. The universes it reports and programs are renumbered like everything else.</p>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setCapHead">
        <h2 class="b5-step-section__head" id="setCapHead">
          <span class="b5-step-num">3</span> Capture &amp; logging
          <span class="b5-step-section__note">what the Analyzer keeps, and what goes to disk</span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="captureLimit">Capture limit (entries)</label>
          <input type="number" id="captureLimit" class="b5-input b5-input--mono" min="100" step="100">
          <span class="b5-field__hint">The in-memory ring the Analyzer reads from. Older packets are dropped once it is full.</span>
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="logRdmPath">Continuous RDM log file (optional)</label>
          <input type="text" id="logRdmPath" class="b5-input b5-input--mono" placeholder="e.g. C:\\Benny512\\rdm-log.txt">
          <span class="b5-field__hint">Appends every RDM/ToD exchange to this file as it happens, so a long bench session isn't limited by the in-memory buffer above. Blank means no file is written. Rotates automatically by size.</span>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setResetHead">
        <h2 class="b5-step-section__head" id="setResetHead">
          <span class="b5-step-num">4</span> Danger zone &mdash; full reset
          <span class="b5-step-section__note">deletes the patch and shuts Benny512 down</span>
        </h2>
        <div class="b5-stack" id="resetPanelRoot"></div>
      </section>

      <section class="b5-actionbar" aria-label="Apply settings">
        <div class="b5-actionbar__status">
          <span class="b5-actionbar__title">${UI.icon('apply')}Settings</span>
          <span id="settingsDirtyPill"></span>
          <span class="b5-text-muted b5-text-sm" id="settingsStatus"></span>
        </div>
        <div class="b5-actionbar__buttons">
          <button id="btnRevertSettings" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Discard changes</button>
          <button id="btnSaveSettings" class="b5-btn b5-btn--primary">${UI.icon('apply')}Apply settings</button>
        </div>
      </section>
    `;
  }

  function render() {
    if (!current) return;
    const nicSel = document.getElementById('nicSelect');
    const prevValue = nicSel.value || current.nic;
    nicSel.innerHTML = '';
    const blank = document.createElement('option');
    blank.value = ''; blank.textContent = '(default / any)';
    nicSel.appendChild(blank);
    nics.forEach(nic => {
      const opt = document.createElement('option');
      opt.value = nic.name;
      const addrs = (nic.ipv4 || []).join(', ');
      opt.textContent = `${nic.displayName || nic.name}${addrs ? ' — ' + addrs : ''}${nic.up ? '' : ' (down)'}`;
      nicSel.appendChild(opt);
    });
    if (current.nic && ![...nicSel.options].some(o => o.value === current.nic)) {
      const opt = document.createElement('option');
      opt.value = current.nic;
      opt.textContent = current.nic + ' (not present on this machine)';
      nicSel.appendChild(opt);
    }
    if (!nics.length) {
      const opt = document.createElement('option');
      opt.value = '';
      // Rule 3: an empty picker must say WHY it is empty, not sit there
      // looking like the machine has no network cards.
      opt.textContent = 'no interfaces reported by this machine';
      nicSel.appendChild(opt);
    }
    nicSel.value = prevValue || current.nic || '';
    document.getElementById('pollInterval').value = current.pollIntervalMs || 3000;
    document.getElementById('captureLimit').value = current.captureLimit || 10000;
    document.getElementById('logRdmPath').value = current.logRdmPath || '';
    const base = (current.universeBase === 0) ? 0 : 1;
    const universeSel = document.getElementById('universeBase');
    if (universeSel) universeSel.value = String(base);
    UI.setUniverseBase(base);
    captureBaseline();
    renderDirty();
    renderUniverseNotes();
  }

  // renderUniverseNotes: the active notation stated as a WORKED EXAMPLE
  // rather than a base number, because "0-based" and "1-based" are the two
  // things that look identical at arm's length. Every number in it is
  // produced by UI.formatUniverse from a canonical wire value — this screen
  // never does its own +1/-1 (DESIGN.md rule 5). Called from render() and
  // again live whenever the picker moves, so the sentence describes the
  // choice currently in the box, not the one last saved.
  function renderUniverseNotes(pendingBase) {
    const sel = document.getElementById('universeBase');
    const base = pendingBase !== undefined
      ? pendingBase
      : (sel ? (parseInt(sel.value, 10) === 0 ? 0 : 1) : UI.getUniverseBase());
    // The examples describe the PENDING choice, which may not be the base
    // UI is running on yet, so they are composed from the base explicitly
    // rather than through UI.formatUniverse's ambient one.
    const shown = (raw) => String(raw + base);
    const note = document.getElementById('setUniNote');
    if (note) {
      note.textContent = base === 0
        ? 'Art-Net native, 0-based' + (base === UI.getUniverseBase() ? '' : ' — not applied yet')
        : 'industry standard, 1-based' + (base === UI.getUniverseBase() ? '' : ' — not applied yet');
    }
    const ex = document.getElementById('universeBaseExample');
    if (ex) {
      ex.textContent =
        `With this setting, the first Art-Net universe on the wire (Port-Address 0) is shown and typed as ` +
        `${shown(0)}, and Port-Address 12 is shown as ${shown(12)}. ` +
        (base === UI.getUniverseBase()
          ? 'This is the numbering in use right now.'
          : 'This is not in use yet — press Apply settings below to switch every screen over.');
    }
  }

  // --- dirty tracking (staged, never sent) ---------------------------------

  function captureBaseline() {
    baseline = {};
    FIELD_IDS.forEach(id => {
      const el = document.getElementById(id);
      if (el) baseline[id] = String(el.value);
    });
  }

  function dirtyFields() {
    return FIELD_IDS.filter(id => {
      const el = document.getElementById(id);
      return el && String(el.value) !== baseline[id];
    });
  }

  function renderDirty() {
    const pill = document.getElementById('settingsDirtyPill');
    const revert = document.getElementById('btnRevertSettings');
    if (!pill) return;
    const n = dirtyFields().length;
    if (n) {
      pill.innerHTML = `<span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}${n} unsaved change${n === 1 ? '' : 's'}</span>`;
    } else {
      pill.innerHTML = `<span class="b5-pill b5-pill--md b5-pill--ok">${UI.icon('status-ok')}Saved</span>`;
    }
    if (revert) revert.disabled = n === 0;
  }

  // save() is the one and only place any setting takes effect — every field
  // above is oninput/onchange-mutates-the-DOM-only until "Apply settings" is
  // clicked (task rule: nothing commits without explicit confirmation).
  async function save() {
    const status = document.getElementById('settingsStatus');
    const payload = {
      nic: document.getElementById('nicSelect').value,
      pollIntervalMs: parseInt(document.getElementById('pollInterval').value, 10) || 3000,
      captureLimit: parseInt(document.getElementById('captureLimit').value, 10) || 10000,
      timeoutProfiles: (current && current.timeoutProfiles) || {},
      logRdmPath: document.getElementById('logRdmPath').value.trim(),
      universeBase: parseInt(document.getElementById('universeBase').value, 10) === 0 ? 0 : 1,
    };
    try {
      await Api.postSettings(payload);
      current = payload;
      // The one line the whole app hangs off: this fires
      // 'b5-universe-base-changed', which every other screen listens for.
      UI.setUniverseBase(payload.universeBase);
      status.innerHTML = UI.icon('status-ok') + 'applied';
      captureBaseline();
      renderDirty();
      renderUniverseNotes();
    } catch (e) {
      status.innerHTML = UI.icon('status-error') + ('error: ' + e.message);
    }
  }

  // --- Danger zone: full reset (POST /api/reset) --------------------------
  // Built once (not part of the settings-form render() above) — the RESET
  // text field is a controlled-by-attribute, not controlled-by-state,
  // input: oninput mutates only the confirm button's disabled attribute
  // directly, never re-renders this subtree, so the field never loses
  // focus/cursor position while Dom is mid-typing (the standing
  // oninput-mutates-state-only / re-render-on-onchange rule, applied here
  // as "don't re-render at all for a plain enable/disable toggle").
  // Gating on the typed word is the deliberate confirmation Dom asked for,
  // on top of (not instead of) the ordinary Apply-to-confirm contract —
  // there's no separate Apply step because the disabled-until-exact-match
  // button IS that step.
  function buildResetPanel() {
    const root = document.getElementById('resetPanelRoot');
    if (!root) return;
    root.innerHTML = `
      <div class="b5-alert b5-alert--danger">
        ${UI.icon('status-warning')}
        <div>
          <p class="b5-alert__title">This deletes the patch and shuts Benny512 down</p>
          <p class="b5-alert__body">
            Full reset permanently deletes the patch and the Rig Walk session (on disk and in memory) &mdash;
            if your patch is hand-built, export it first (Patch tab &rarr; Export) or it is gone for good.
            It also clears every discovered node and device, empties the Art-Net/RDM capture buffers, closes
            any RDM log file, and resets these Settings to their defaults. Benny512 then shuts down and has
            to be relaunched &mdash; this is not the same as the Devices tab's "Clear discovered devices",
            which keeps all of that.
          </p>
        </div>
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="resetConfirmInput">Type RESET to confirm</label>
        <input id="resetConfirmInput" class="b5-input b5-input--mono" type="text" autocomplete="off" spellcheck="false" placeholder="RESET">
        <span class="b5-field__hint">The button below stays disabled until this box says exactly RESET. That typed word IS the confirmation &mdash; there is no second dialog.</span>
      </div>
      <div class="b5-row">
        <button id="btnFullReset" class="b5-btn b5-btn--danger" disabled>${UI.icon('status-warning')}Reset and shut down</button>
        <span class="b5-text-muted b5-text-sm" id="resetStatus"></span>
      </div>
    `;
    const input = document.getElementById('resetConfirmInput');
    const btn = document.getElementById('btnFullReset');
    input.addEventListener('input', () => { btn.disabled = input.value !== 'RESET'; });
    btn.addEventListener('click', doFullReset);
  }

  async function doFullReset() {
    const btn = document.getElementById('btnFullReset');
    const input = document.getElementById('resetConfirmInput');
    const status = document.getElementById('resetStatus');
    if (!btn || !input || input.value !== 'RESET') return;
    btn.disabled = true;
    input.disabled = true;
    status.innerHTML = UI.spinner() + 'Resetting…';
    try {
      const res = await Api.fullReset();
      // The WebSocket is about to drop as the process exits (by design) —
      // stop the normal reconnect-with-backoff churn before it ever fires,
      // then replace the screen with a clear terminal end state instead of
      // leaving "Reconnecting…" spinning forever against a dead server.
      Live.enterShutdown();
      showResetTerminalState(res);
    } catch (e) {
      status.innerHTML = UI.icon('status-error') + ('error: ' + e.message);
      btn.disabled = input.value !== 'RESET';
      input.disabled = false;
    }
  }

  // showResetTerminalState renders a modal (.b5-dialog — the design
  // system's "used sparingly" overlay, exactly the sparing/high-stakes case
  // it's for) stating plainly what happened: every deleted path, any
  // deletion errors surfaced rather than swallowed (task ask: "a file that
  // couldn't be deleted matters"), and that the process has exited and
  // needs relaunching. No close button — there is nothing left running to
  // return to.
  function showResetTerminalState(res) {
    const deleted = (res && res.deleted) || [];
    const errors = (res && res.errors) || [];
    const overlay = document.createElement('div');
    overlay.className = 'b5-dialog-backdrop';
    overlay.id = 'resetTerminalOverlay';
    overlay.innerHTML = `
      <div class="b5-dialog" role="alertdialog" aria-modal="true" aria-labelledby="resetTerminalTitle">
        <h2 class="b5-dialog__title" id="resetTerminalTitle">${UI.icon('status-ok')}Benny512 has shut down</h2>
        <div class="b5-dialog__body">
          <p>Full reset completed.${deleted.length ? ' Deleted: ' + deleted.map(escapeHtml).join(', ') + '.' : ' Nothing needed deleting on disk (no patch/walk session file was on disk to begin with).'}</p>
          ${errors.length ? `
            <div class="b5-alert b5-alert--danger" style="margin-top:var(--b5-space-3)">
              ${UI.icon('status-error')}
              <div><p class="b5-alert__title">Some files could not be deleted</p><p class="b5-alert__body">${errors.map(escapeHtml).join('<br>')}</p></div>
            </div>` : ''}
          <p style="margin-top:var(--b5-space-3)">The application has exited. Relaunch Benny512 to continue.</p>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);
  }

  function init() {
    const screen = document.getElementById('screen-settings');
    if (screen) screen.innerHTML = screenHtml();
    document.getElementById('btnSaveSettings').addEventListener('click', save);
    document.getElementById('btnRevertSettings').addEventListener('click', () => {
      // Discard is local only: it repaints the form from `current`, the
      // last thing the SERVER told us, and sends nothing.
      render();
      const status = document.getElementById('settingsStatus');
      if (status) status.textContent = 'changes discarded';
    });
    // Staged edits only: typing never sends. The dirty marker and the
    // universe worked-example are the only things that move.
    FIELD_IDS.forEach(id => {
      const el = document.getElementById(id);
      if (!el) return;
      el.addEventListener('input', () => { renderDirty(); if (id === 'universeBase') renderUniverseNotes(); });
      el.addEventListener('change', () => { renderDirty(); if (id === 'universeBase') renderUniverseNotes(); });
    });
    buildResetPanel();
    // Baseline the empty form before the first paint, so the dirty marker
    // never flashes "5 unsaved changes" in the gap before GET /api/settings
    // answers. render() re-baselines against the real values.
    captureBaseline();
    renderDirty();
    renderUniverseNotes();
    refresh();
  }

  return { init };
})();
