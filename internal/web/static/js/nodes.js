// nodes.js — Nodes screen: table + detail pane + editable node/network
// configuration (Phase 1c+: ArtAddress/ArtInput/ArtIpProg via
// internal/web/node_config.go).
//
// Rules (architecture rev 5 §4, adopted from Rackmaster's pitfalls, and the
// task brief's MANDATORY UI rules):
//  - oninput mutates state only; full re-render happens on onchange/explicit
//    refresh.
//  - re-render preserves the selected row and scroll position.
//  - the *editable config section* is built once per node selection and is
//    NOT touched by the periodic (WS-triggered) node-list refresh — only
//    the read-only info/ports block re-renders on every poll tick. This is
//    what keeps a half-typed short name or IP field from being clobbered
//    out from under Dom every ~3s by the node's own ArtPollReply traffic.
const NodesScreen = (() => {
  let nodes = [];
  let selectedKey = null;   // "ip|bindIndex"
  let detailBuiltFor = null; // key whose config section is currently in the DOM

  // Per-node editable config, keyed by node key. Populated once per
  // selection from the node's current known values; mutated in place by
  // oninput handlers; read back by the Save/Apply actions. Note some fields
  // (merge mode, per-port input-enable) have no read-back in this API —
  // nodePortJSON carries no such state — so they start at a sane default
  // and are clearly hinted as "not read from the node" rather than implying
  // they reflect current hardware state.
  let configState = {};
  let actionStatus = {}; // key -> { addressing, merge, input, ip }

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

    const el = document.getElementById('nodeDetail');
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) {
      detailBuiltFor = null;
      el.innerHTML = '<p class="empty-hint">Select a node to see its ports and universes.</p>';
      return;
    }
    if (detailBuiltFor !== selectedKey) {
      detailBuiltFor = selectedKey;
      if (!configState[selectedKey]) initConfigState(n);
      buildDetailShell(n);
    } else {
      renderInfoStatic(n);
    }
  }

  // --- read-only info/ports block (rebuilt every refresh) -----------------

  function buildDetailShell(n) {
    const el = document.getElementById('nodeDetail');
    el.innerHTML = `
      <h3>${escapeHtml(n.longName || n.shortName)}</h3>
      <div id="nodeInfoStatic"></div>
      <div id="nodeConfigSection"></div>
    `;
    renderInfoStatic(n);
    renderConfigSection(n);
  }

  function renderInfoStatic(n) {
    const target = document.getElementById('nodeInfoStatic');
    if (!target) return;
    const portRows = (n.ports || []).map(p => `
      <tr>
        <td>${p.index}</td>
        <td>${p.input ? 'in' : ''} ${p.output ? 'out' : ''}</td>
        <td>${p.inputAddress}</td>
        <td>${p.outputAddress}</td>
        <td>${p.rdmEnabled ? 'yes' : 'no'}</td>
      </tr>`).join('');
    target.innerHTML = `
      <div class="field-row"><label>IP</label><span>${escapeHtml(n.ip)} (bind ${n.bindIndex})</span></div>
      <div class="field-row"><label>Style</label><span>${escapeHtml(n.style)}</span></div>
      <div class="field-row"><label>Fixtures seen</label><span>${n.fixtureCount}</span></div>
      <table class="data-table">
        <thead><tr><th>Port</th><th>Dir</th><th>In addr</th><th>Out addr</th><th>BiDi</th></tr></thead>
        <tbody>${portRows}</tbody>
      </table>
    `;
  }

  // --- editable config section (built once per selection) -----------------

  function initConfigState(n) {
    const ports = n.ports || [];
    const firstRaw = ports.length ? ports[0].outputAddress : 0;
    configState[keyOf(n)] = {
      shortName: n.shortName || '',
      longName: n.longName || '',
      netSwitch: (firstRaw >> 8) & 0x7F,
      subSwitch: (firstRaw >> 4) & 0x0F,
      ports: ports.map(p => ({
        index: p.index,
        universeIn: p.inputAddress & 0x0F,
        universeOut: p.outputAddress & 0x0F,
        input: p.input, output: p.output,
        mergeMode: 'htp',      // no read-back from ArtPollReply — default/editable only
        inputEnabled: true,    // no read-back from ArtPollReply — default/editable only
      })),
      ip: '', mask: '', gateway: '', dhcp: false, ipArmed: false,
    };
    actionStatus[keyOf(n)] = { addressing: '', merge: '', input: '', ip: '' };
  }

  function resultText(res) {
    let t = res.confirmed ? `confirmed (${res.elapsedMs}ms)` : `no confirmation received (${res.elapsedMs}ms)`;
    if (res.warning) t += ` — ${res.warning}`;
    return t;
  }

  function setStatus(key, section, msg) {
    actionStatus[key][section] = msg;
    const el = document.getElementById('status-' + section);
    if (el) el.textContent = msg;
  }

  function renderConfigSection(n) {
    const key = keyOf(n);
    const st = configState[key];
    const target = document.getElementById('nodeConfigSection');
    if (!target) return;

    target.innerHTML = `
      <div class="caution-banner">
        <strong>Node configuration — unverified against real hardware.</strong>
        ArtAddress/ArtInput/ArtIpProg wire formats here have not been confirmed
        against a real node's own capture. Verify results on the node's own
        display/web UI before relying on any change made here.
        <button id="btnReloadConfig" type="button">Reload current values</button>
      </div>

      <h4>Names &amp; addressing</h4>
      <div class="field-row"><label>Short name</label>
        <input id="cfgShortName" type="text" maxlength="18" value="${escapeHtml(st.shortName)}">
      </div>
      <div class="field-row"><label>Long name</label>
        <input id="cfgLongName" type="text" maxlength="64" value="${escapeHtml(st.longName)}">
      </div>
      <div class="field-row"><label>Net (0-127)</label>
        <input id="cfgNet" type="number" min="0" max="127" value="${st.netSwitch}">
      </div>
      <div class="field-row"><label>Sub-Net (0-15)</label>
        <input id="cfgSub" type="number" min="0" max="15" value="${st.subSwitch}">
      </div>
      <table class="data-table">
        <thead><tr><th>Port</th><th>Universe in</th><th>Universe out</th><th>Merge mode</th><th>Input enabled</th></tr></thead>
        <tbody>
          ${st.ports.map((p, i) => `
            <tr>
              <td>${p.index}</td>
              <td>${p.input ? `<input class="cfg-uni-in" data-i="${i}" type="number" min="0" max="15" value="${p.universeIn}">` : '<span class="hint">n/a</span>'}</td>
              <td>${p.output ? `<input class="cfg-uni-out" data-i="${i}" type="number" min="0" max="15" value="${p.universeOut}">` : '<span class="hint">n/a</span>'}</td>
              <td>${p.output ? `<select class="cfg-merge" data-i="${i}"><option value="htp" ${p.mergeMode === 'htp' ? 'selected' : ''}>HTP</option><option value="ltp" ${p.mergeMode === 'ltp' ? 'selected' : ''}>LTP</option></select>` : '<span class="hint">n/a</span>'}</td>
              <td>${p.input ? `<input class="cfg-input-en" data-i="${i}" type="checkbox" ${p.inputEnabled ? 'checked' : ''}>` : '<span class="hint">n/a</span>'}</td>
            </tr>`).join('')}
        </tbody>
      </table>
      <span class="hint">Merge mode and input-enable have no read-back from ArtPollReply — the values shown are editable defaults, not confirmed current state.</span>
      <div class="field-row">
        <button id="btnSaveAddressing">Save names &amp; addressing</button>
        <button id="btnSaveInput">Save input enable</button>
        <span class="hint" id="status-addressing"></span>
      </div>
      <div class="field-row"><span class="hint" id="status-merge"></span></div>
      <div class="field-row"><span class="hint" id="status-input"></span></div>

      <h4>IP configuration</h4>
      <div class="field-row"><label>DHCP</label>
        <input id="cfgDhcp" type="checkbox" ${st.dhcp ? 'checked' : ''}>
      </div>
      <div class="field-row"><label>Static IP</label>
        <input id="cfgIp" type="text" placeholder="e.g. 2.11.90.5" value="${escapeHtml(st.ip)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div class="field-row"><label>Subnet mask</label>
        <input id="cfgMask" type="text" placeholder="e.g. 255.0.0.0" value="${escapeHtml(st.mask)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div class="field-row"><label>Gateway</label>
        <input id="cfgGateway" type="text" placeholder="optional" value="${escapeHtml(st.gateway)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div id="ipConfirmArea"></div>
      <div class="field-row"><span class="hint" id="status-ip"></span></div>
    `;

    // --- names & addressing: oninput mutates state only ---
    byId('cfgShortName').addEventListener('input', e => { st.shortName = e.target.value; });
    byId('cfgLongName').addEventListener('input', e => { st.longName = e.target.value; });
    byId('cfgNet').addEventListener('input', e => { st.netSwitch = clampInt(e.target.value, 0, 127, st.netSwitch); });
    byId('cfgSub').addEventListener('input', e => { st.subSwitch = clampInt(e.target.value, 0, 15, st.subSwitch); });
    target.querySelectorAll('.cfg-uni-in').forEach(inp => {
      inp.addEventListener('input', e => { st.ports[+e.target.dataset.i].universeIn = clampInt(e.target.value, 0, 15, 0); });
    });
    target.querySelectorAll('.cfg-uni-out').forEach(inp => {
      inp.addEventListener('input', e => { st.ports[+e.target.dataset.i].universeOut = clampInt(e.target.value, 0, 15, 0); });
    });
    target.querySelectorAll('.cfg-input-en').forEach(inp => {
      inp.addEventListener('change', e => { st.ports[+e.target.dataset.i].inputEnabled = e.target.checked; });
    });
    // Merge mode applies immediately (one ArtAddress command per port,
    // independent of the addressing Save button) — see task brief: it's a
    // single-shot AcCommand, not a field the addressing packet batches.
    target.querySelectorAll('.cfg-merge').forEach(sel => {
      sel.addEventListener('change', async e => {
        const i = +e.target.dataset.i;
        st.ports[i].mergeMode = e.target.value;
        await setMergeMode(n, st.ports[i].index, e.target.value);
      });
    });

    byId('btnSaveAddressing').addEventListener('click', () => saveAddressing(n));
    byId('btnSaveInput').addEventListener('click', () => saveInputEnabled(n));
    byId('btnReloadConfig').addEventListener('click', () => { initConfigState(n); renderConfigSection(n); });

    // --- IP config: DHCP toggle re-renders (structural change: disables
    // static fields), everything else is oninput-state / explicit confirm ---
    byId('cfgDhcp').addEventListener('change', e => { st.dhcp = e.target.checked; renderConfigSection(n); });
    if (!st.dhcp) {
      byId('cfgIp').addEventListener('input', e => { st.ip = e.target.value; });
      byId('cfgMask').addEventListener('input', e => { st.mask = e.target.value; });
      byId('cfgGateway').addEventListener('input', e => { st.gateway = e.target.value; });
    }
    renderIPConfirmArea(n);

    // Restore any status text already recorded for this node.
    ['addressing', 'merge', 'input', 'ip'].forEach(section => {
      const s = actionStatus[key] && actionStatus[key][section];
      if (s) setStatus(key, section, s);
    });
  }

  function renderIPConfirmArea(n) {
    const key = keyOf(n);
    const st = configState[key];
    const area = document.getElementById('ipConfirmArea');
    if (!area) return;
    if (!st.ipArmed) {
      area.innerHTML = `<button id="btnArmIP">Apply IP configuration…</button>`;
      byId('btnArmIP').addEventListener('click', () => { st.ipArmed = true; renderIPConfirmArea(n); });
    } else {
      area.innerHTML = `
        <div class="ip-confirm-box">
          A mis-set IP can strand this node off the show network. Confirm:
          ${st.dhcp ? 'switch to DHCP' : `static ${escapeHtml(st.ip || '(unchanged)')} / ${escapeHtml(st.mask || '(unchanged)')}${st.gateway ? ' / gw ' + escapeHtml(st.gateway) : ''}`}
          <div class="field-row">
            <button id="btnConfirmIP">Yes, send now</button>
            <button id="btnCancelIP">Cancel</button>
          </div>
        </div>
      `;
      byId('btnConfirmIP').addEventListener('click', () => sendIPConfig(n));
      byId('btnCancelIP').addEventListener('click', () => { st.ipArmed = false; renderIPConfirmArea(n); });
    }
  }

  async function saveAddressing(n) {
    const key = keyOf(n);
    const st = configState[key];
    const swIn = [null, null, null, null], swOut = [null, null, null, null];
    st.ports.forEach((p, i) => {
      if (i > 3) return;
      swIn[i] = p.universeIn; swOut[i] = p.universeOut;
    });
    const body = {
      bindIndex: n.bindIndex, shortName: st.shortName, longName: st.longName,
      netSwitch: st.netSwitch, subSwitch: st.subSwitch, swIn, swOut,
    };
    setStatus(key, 'addressing', 'sending…');
    try {
      const res = await Api.setNodeAddress(n.ip, body);
      setStatus(key, 'addressing', resultText(res));
    } catch (e) {
      setStatus(key, 'addressing', 'error: ' + e.message);
    }
  }

  async function setMergeMode(n, portIndex, mode) {
    const key = keyOf(n);
    const cmd = (mode === 'ltp' ? 'merge_ltp_' : 'merge_htp_') + portIndex;
    setStatus(key, 'merge', `setting port ${portIndex} to ${mode.toUpperCase()}…`);
    try {
      const res = await Api.setNodeAddress(n.ip, { bindIndex: n.bindIndex, command: cmd });
      setStatus(key, 'merge', `port ${portIndex} ${mode.toUpperCase()}: ` + resultText(res));
    } catch (e) {
      setStatus(key, 'merge', 'error: ' + e.message);
    }
  }

  async function saveInputEnabled(n) {
    const key = keyOf(n);
    const st = configState[key];
    const enabled = [0, 1, 2, 3].map(i => (st.ports[i] ? st.ports[i].inputEnabled : false));
    setStatus(key, 'input', 'sending…');
    try {
      const res = await Api.setNodeInput(n.ip, { bindIndex: n.bindIndex, enabled });
      setStatus(key, 'input', resultText(res));
    } catch (e) {
      setStatus(key, 'input', 'error: ' + e.message);
    }
  }

  async function sendIPConfig(n) {
    const key = keyOf(n);
    const st = configState[key];
    setStatus(key, 'ip', 'sending…');
    try {
      const res = await Api.setNodeIPConfig(n.ip, {
        bindIndex: n.bindIndex, ip: st.ip, mask: st.mask, gateway: st.gateway, dhcp: st.dhcp,
      });
      setStatus(key, 'ip', resultText(res));
    } catch (e) {
      setStatus(key, 'ip', 'error: ' + e.message);
    }
    st.ipArmed = false;
    renderIPConfirmArea(n);
  }

  function byId(id) { return document.getElementById(id); }
  function clampInt(v, lo, hi, fallback) {
    const n = parseInt(v, 10);
    if (!Number.isFinite(n)) return fallback;
    return Math.min(hi, Math.max(lo, n));
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
