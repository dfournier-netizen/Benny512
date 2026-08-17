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

  function init() {
    document.getElementById('btnSaveSettings').addEventListener('click', save);
    refresh();
  }

  return { init };
})();
