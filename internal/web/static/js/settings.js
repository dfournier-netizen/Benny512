// settings.js — Settings screen: NIC picker, poll interval, timeout
// profile, capture limit. NIC list itself currently comes from the same
// /api/settings payload's echo (server owns the authoritative list via
// internal/transport.ListInterfaces in a future iteration); for now the
// picker is populated from whatever the server currently reports selected
// plus a manual-entry fallback, since Phase 1c's REST surface does not yet
// expose a dedicated NIC-list endpoint (see architect review notes).
const SettingsScreen = (() => {
  let current = null;

  async function refresh() {
    current = await Api.getSettings();
    render();
  }

  function render() {
    if (!current) return;
    const nicSel = document.getElementById('nicSelect');
    if (![...nicSel.options].some(o => o.value === current.nic)) {
      const opt = document.createElement('option');
      opt.value = current.nic;
      opt.textContent = current.nic || '(default / any)';
      nicSel.appendChild(opt);
    }
    nicSel.value = current.nic || '';
    document.getElementById('pollInterval').value = current.pollIntervalMs || 3000;
    document.getElementById('captureLimit').value = current.captureLimit || 10000;
  }

  async function save() {
    const status = document.getElementById('settingsStatus');
    const payload = {
      nic: document.getElementById('nicSelect').value,
      pollIntervalMs: parseInt(document.getElementById('pollInterval').value, 10) || 3000,
      captureLimit: parseInt(document.getElementById('captureLimit').value, 10) || 10000,
      timeoutProfiles: (current && current.timeoutProfiles) || {},
    };
    try {
      await Api.postSettings(payload);
      current = payload;
      status.textContent = 'saved';
    } catch (e) {
      status.textContent = 'error: ' + e.message;
    }
  }

  function init() {
    document.getElementById('btnSaveSettings').addEventListener('click', save);
    refresh();
  }

  return { init };
})();
