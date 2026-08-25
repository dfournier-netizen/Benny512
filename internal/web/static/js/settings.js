// settings.js — Settings screen: NIC picker, poll interval, timeout
// profile, capture limit. NIC list is populated from GET /api/nics
// (internal/transport.ListInterfaces) — Phase 1c+ closes the gap flagged
// in the earlier architect review notes ("stub"); current.nic is matched
// against the real interface list by name, falling back to a synthesized
// option if the previously-saved NIC name isn't present on this machine
// (e.g. settings.json carried over from another host).
const SettingsScreen = (() => {
  let current = null;
  let nics = [];

  async function refresh() {
    [current, nics] = await Promise.all([Api.getSettings(), Api.getNICs().catch(() => [])]);
    render();
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
    nicSel.value = prevValue || current.nic || '';
    document.getElementById('pollInterval').value = current.pollIntervalMs || 3000;
    document.getElementById('captureLimit').value = current.captureLimit || 10000;
    document.getElementById('logRdmPath').value = current.logRdmPath || '';
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
    };
    try {
      await Api.postSettings(payload);
      current = payload;
      status.innerHTML = UI.icon('status-ok') + 'applied';
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
    document.getElementById('btnSaveSettings').addEventListener('click', save);
    buildResetPanel();
    refresh();
  }

  return { init };
})();
