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

  // portsUniverseSummary is a purely presentational rollup ("N ports · U1–U4")
  // of the same n.ports data the old table already had (no new node state).
  function portsUniverseSummary(n) {
    const ports = n.ports || [];
    if (!ports.length) return '0 ports';
    const universes = [];
    ports.forEach(p => {
      if (p.output) universes.push(p.outputAddress & 0x0F);
      else if (p.input) universes.push(p.inputAddress & 0x0F);
    });
    const countLabel = `${ports.length} port${ports.length === 1 ? '' : 's'}`;
    if (!universes.length) return countLabel;
    const lo = Math.min(...universes), hi = Math.max(...universes);
    return `${countLabel} · ${lo === hi ? 'U' + lo : 'U' + lo + '–U' + hi}`;
  }

  // nodeStatusBadge combines the two facts the app already tracked
  // (rdmCapable, stale) into one status column with an explicit text label
  // (never color alone) instead of two separate badge columns.
  function nodeStatusBadge(n) {
    if (n.stale) return UI.badge('stale', 'Stale');
    if (n.rdmCapable) return UI.badge('ok', 'RDM');
    return UI.badge('unknown', 'No RDM');
  }

  function render() {
    const tbody = document.querySelector('#nodesTable tbody');
    const scrollTop = tbody.parentElement.scrollTop;
    tbody.innerHTML = '';
    nodes.forEach(n => {
      const tr = document.createElement('tr');
      if (keyOf(n) === selectedKey) tr.classList.add('is-selected');
      if (n.stale) tr.classList.add('is-stale');
      tr.innerHTML = `
        <td data-label="Name">${escapeHtml(n.shortName || n.longName || '(unnamed)')}</td>
        <td data-label="IP" class="b5-table__mono">${escapeHtml(n.ip)}</td>
        <td data-label="Ports / Universes">${escapeHtml(portsUniverseSummary(n))}</td>
        <td data-label="Status">${nodeStatusBadge(n)}</td>
        <td data-label="Last seen">${n.lastSeen ? new Date(n.lastSeen).toLocaleTimeString() : '—'}</td>
      `;
      tr.addEventListener('click', () => { selectedKey = keyOf(n); render(); });
      tbody.appendChild(tr);
    });
    tbody.parentElement.scrollTop = scrollTop;

    const el = document.getElementById('nodeDetail');
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) {
      detailBuiltFor = null;
      el.innerHTML = `<div class="b5-panel__body"><div class="b5-empty">${UI.icon('network-node')}<span class="b5-empty__title">No node selected</span><span class="b5-empty__body">Select a node to see its ports and universes.</span></div></div>`;
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
      <div class="b5-panel__header">
        <h2 class="b5-panel__title">${escapeHtml(n.longName || n.shortName)}</h2>
        ${nodeStatusBadge(n)}
      </div>
      <div class="b5-panel__body b5-stack">
        <div id="nodeInfoStatic"></div>
        <div id="nodeConfigSection"></div>
      </div>
    `;
    renderInfoStatic(n);
    renderConfigSection(n);
  }

  // portDirectionLabel/portUniverseLabel: a node port is an input OR an
  // output — never both at once operationally (Dom: "they're either inputs
  // or outputs, never both at the same time. Bidirectionality only applies
  // to their ethernet ports, and that's not something we need to report").
  // A port's *advertised capability* (PortTypes bits) can still list both,
  // so when that happens both universes are shown, clearly labeled, rather
  // than picking one and hiding the other.
  // portTypeHex formats the raw, undecoded ArtPollReply PortTypes byte
  // (server.go's nodePortJSON.portTypeRaw) so a port that reports neither
  // the input nor output bit is diagnosable at a glance instead of a dead
  // end (bench report: every port on an Obsidian EN4 showed "n/a" for
  // input/output). This is display-only — the input/output decode itself
  // lives in internal/session/artnetsession.go, not here.
  function portTypeHex(p) {
    const v = p.portTypeRaw || 0;
    return '0x' + v.toString(16).padStart(2, '0').toUpperCase();
  }

  function portDirectionLabel(p) {
    if (p.input && p.output) return 'Input + Output';
    if (p.output) return 'Output';
    if (p.input) return 'Input';
    return `n/a (PortTypes ${portTypeHex(p)})`;
  }

  function portUniverseLabel(p) {
    if (p.input && p.output) return `in ${p.inputAddress & 0x0F} / out ${p.outputAddress & 0x0F}`;
    if (p.output) return String(p.outputAddress & 0x0F);
    if (p.input) return String(p.inputAddress & 0x0F);
    return '—';
  }

  function renderInfoStatic(n) {
    const target = document.getElementById('nodeInfoStatic');
    if (!target) return;
    const portRows = (n.ports || []).map(p => `
      <tr>
        <td data-label="Port">${p.index}</td>
        <td data-label="Direction">${escapeHtml(portDirectionLabel(p))}</td>
        <td data-label="Universe">${escapeHtml(portUniverseLabel(p))}</td>
        <td data-label="RDM">${p.rdmEnabled ? UI.badge('ok', 'Yes') : UI.badge('unknown', 'No')}</td>
      </tr>`).join('');
    target.innerHTML = `
      <div class="b5-grid-2">
        <div><span class="b5-text-muted b5-text-sm">IP</span><br>${escapeHtml(n.ip)} (bind ${n.bindIndex})</div>
        <div><span class="b5-text-muted b5-text-sm">Style</span><br>${escapeHtml(n.style)}</div>
        <div><span class="b5-text-muted b5-text-sm">Fixtures seen</span><br>${n.fixtureCount}</div>
      </div>
      <table class="b5-table b5-table--responsive">
        <thead><tr><th>Port</th><th>Direction</th><th>Universe</th><th>RDM</th></tr></thead>
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
        portTypeRaw: p.portTypeRaw,
        // direction selects which of universeIn/universeOut is currently
        // being displayed/edited (report task: "In node config, editing the
        // universe edits the value for the port's current direction;
        // toggling direction switches which field is being edited/
        // displayed"). Defaults to Output when a port advertises both
        // capabilities (the common case for a lighting-distribution node);
        // fixed to whichever single direction the port actually supports
        // otherwise.
        direction: p.output ? 'output' : 'input',
        mergeMode: 'htp',           // no read-back from ArtPollReply — default/editable only
        mergeModeApplied: 'htp',    // last value actually sent, for dirty/Apply tracking
        inputEnabled: true,         // no read-back from ArtPollReply — default/editable only
      })),
      // ip/mask/gateway/dhcp are staged by oninput; ipApplied gates the
      // arm-then-confirm flow behind an explicit Apply step first (task
      // rule: every field needs Apply, and "IP config keeps its existing
      // extra arm-then-confirm step ON TOP OF Apply" — so IP config alone
      // gets three deliberate steps: Apply, Arm, Confirm/send).
      ip: '', mask: '', gateway: '', dhcp: false, ipApplied: false, ipArmed: false,
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
      <div class="b5-alert b5-alert--caution">
        ${UI.icon('status-warning')}
        <div>
          <p class="b5-alert__title">Node configuration — unverified against real hardware</p>
          <p class="b5-alert__body">ArtAddress/ArtInput/ArtIpProg wire formats here have not been confirmed against a real node's own capture. Verify results on the node's own display/web UI before relying on any change made here.</p>
          <button id="btnReloadConfig" type="button" class="b5-btn b5-btn--sm" style="margin-top:var(--b5-space-2)">${UI.icon('refresh')}Reload current values</button>
        </div>
      </div>

      <h3 class="b5-panel__title">Names &amp; addressing</h3>
      <div class="b5-field">
        <label class="b5-field__label" for="cfgShortName">Short name</label>
        <input id="cfgShortName" class="b5-input" type="text" maxlength="18" value="${escapeHtml(st.shortName)}">
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="cfgLongName">Long name</label>
        <input id="cfgLongName" class="b5-input" type="text" maxlength="64" value="${escapeHtml(st.longName)}">
      </div>
      <div class="b5-grid-2">
        <div class="b5-field">
          <label class="b5-field__label" for="cfgNet">Net (0-127)</label>
          <input id="cfgNet" class="b5-input" type="number" min="0" max="127" value="${st.netSwitch}">
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="cfgSub">Sub-Net (0-15)</label>
          <input id="cfgSub" class="b5-input" type="number" min="0" max="15" value="${st.subSwitch}">
        </div>
      </div>
      <table class="b5-table b5-table--responsive">
        <thead><tr><th>Port</th><th>Direction</th><th>Universe</th><th>Merge mode</th><th>Input enabled</th></tr></thead>
        <tbody>
          ${st.ports.map((p, i) => {
            const both = p.input && p.output;
            const dirCell = both
              ? `<select class="b5-select cfg-dir" data-i="${i}" style="min-height:32px">
                   <option value="output" ${p.direction === 'output' ? 'selected' : ''}>Output</option>
                   <option value="input" ${p.direction === 'input' ? 'selected' : ''}>Input</option>
                 </select>`
              : p.output ? UI.tag('OUTPUT')
              : p.input ? UI.tag('INPUT')
              // Neither bit set: show the raw PortTypes byte next to "n/a"
              // instead of a dead end — see portTypeHex above.
              : `${UI.tag('n/a')} <span class="b5-text-muted b5-text-sm">PortTypes ${portTypeHex(p)}</span>`;
            const uniValue = p.direction === 'output' ? p.universeOut : p.universeIn;
            const uniCell = (p.input || p.output)
              ? `<input class="b5-input b5-input--mono cfg-universe" data-i="${i}" type="number" min="0" max="15" value="${uniValue}" style="max-width:6em">`
              : '<span class="b5-text-muted b5-text-sm">n/a</span>';
            const mergeDirty = p.mergeMode !== p.mergeModeApplied;
            const mergeCell = p.output
              ? `<div class="b5-field__row">
                   <select class="b5-select cfg-merge" data-i="${i}" style="min-height:32px"><option value="htp" ${p.mergeMode === 'htp' ? 'selected' : ''}>HTP</option><option value="ltp" ${p.mergeMode === 'ltp' ? 'selected' : ''}>LTP</option></select>
                   <span class="b5-field__actions">
                     <button class="b5-btn b5-btn--sm b5-btn--primary btn-apply-merge" data-i="${i}" ${mergeDirty ? '' : 'disabled'}>${UI.icon('apply')}Apply</button>
                     ${mergeDirty ? `<button class="b5-btn b5-btn--sm b5-btn--ghost btn-revert-merge" data-i="${i}">${UI.icon('revert')}Revert</button>` : ''}
                   </span>
                 </div>
                 ${mergeDirty ? '<span class="b5-field__status">Unsaved change</span>' : ''}`
              : '<span class="b5-text-muted b5-text-sm">n/a</span>';
            return `
            <tr>
              <td data-label="Port">${p.index}</td>
              <td data-label="Direction">${dirCell}</td>
              <td data-label="Universe">${uniCell}</td>
              <td data-label="Merge mode">${mergeCell}</td>
              <td data-label="Input enabled">${p.input
                // inputEnabled has no ArtPollReply read-back (nodePortJSON
                // carries no such field, and there is none to carry —
                // Phase 1c+ notes: this is not confirmed hardware state).
                // The checkbox pre-checks to a hinted default so the field
                // is immediately usable, but per-row text makes clear that
                // default is a guess, not a report, so it can't be misread
                // as "confirmed off" if unchecked or "confirmed on" if
                // checked — the exact "invented value that looks
                // confirmed" failure mode this is guarding against.
                ? `<label class="b5-checkbox"><input class="cfg-input-en" data-i="${i}" type="checkbox" ${p.inputEnabled ? 'checked' : ''}></label><span class="b5-text-muted b5-text-sm">not confirmed by node</span>`
                : '<span class="b5-text-muted b5-text-sm">n/a</span>'}</td>
            </tr>`;
          }).join('')}
        </tbody>
      </table>
      <span class="b5-field__hint">Merge mode and input-enable have no read-back from ArtPollReply — the values shown are editable defaults, not confirmed current state. Universe edits above are staged; use "Save names &amp; addressing" below to commit them.</span>
      <div class="b5-row">
        <button id="btnSaveAddressing" class="b5-btn b5-btn--primary">${UI.icon('apply')}Save names &amp; addressing</button>
        <button id="btnSaveInput" class="b5-btn">Save input enable</button>
        <span class="b5-text-muted b5-text-sm" id="status-addressing"></span>
      </div>
      <div class="b5-row"><span class="b5-text-muted b5-text-sm" id="status-merge"></span></div>
      <div class="b5-row"><span class="b5-text-muted b5-text-sm" id="status-input"></span></div>

      <hr class="b5-hr">
      <h3 class="b5-panel__title">IP configuration</h3>
      <label class="b5-toggle"><input id="cfgDhcp" type="checkbox" ${st.dhcp ? 'checked' : ''}><span class="b5-toggle__track"></span>DHCP</label>
      <div class="b5-field">
        <label class="b5-field__label" for="cfgIp">Static IP</label>
        <input id="cfgIp" class="b5-input b5-input--mono" type="text" placeholder="e.g. 2.11.90.5" value="${escapeHtml(st.ip)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="cfgMask">Subnet mask</label>
        <input id="cfgMask" class="b5-input b5-input--mono" type="text" placeholder="e.g. 255.0.0.0" value="${escapeHtml(st.mask)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="cfgGateway">Gateway</label>
        <input id="cfgGateway" class="b5-input b5-input--mono" type="text" placeholder="optional" value="${escapeHtml(st.gateway)}" ${st.dhcp ? 'disabled' : ''}>
      </div>
      <div id="ipConfirmArea"></div>
      <div class="b5-row"><span class="b5-text-muted b5-text-sm" id="status-ip"></span></div>
    `;

    // --- names & addressing: oninput mutates state only ---
    byId('cfgShortName').addEventListener('input', e => { st.shortName = e.target.value; });
    byId('cfgLongName').addEventListener('input', e => { st.longName = e.target.value; });
    byId('cfgNet').addEventListener('input', e => { st.netSwitch = clampInt(e.target.value, 0, 127, st.netSwitch); });
    byId('cfgSub').addEventListener('input', e => { st.subSwitch = clampInt(e.target.value, 0, 15, st.subSwitch); });
    // Direction toggle switches which of universeIn/universeOut the single
    // Universe field below shows/edits — a structural change (which field
    // is live), so it re-renders rather than merely mutating state, same as
    // the DHCP toggle further down.
    target.querySelectorAll('.cfg-dir').forEach(sel => {
      sel.addEventListener('change', e => {
        const i = +e.target.dataset.i;
        st.ports[i].direction = e.target.value;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.cfg-universe').forEach(inp => {
      inp.addEventListener('input', e => {
        const i = +e.target.dataset.i;
        const v = clampInt(e.target.value, 0, 15, 0);
        if (st.ports[i].direction === 'output') st.ports[i].universeOut = v;
        else st.ports[i].universeIn = v;
      });
    });
    target.querySelectorAll('.cfg-input-en').forEach(inp => {
      inp.addEventListener('change', e => { st.ports[+e.target.dataset.i].inputEnabled = e.target.checked; });
    });
    // Merge mode: oninput/onchange on the select only stages the pending
    // value (dirty badge appears) — it does NOT apply. A separate Apply
    // button per port sends the single-shot AcCommand; Revert discards the
    // staged change. (Fixes the earlier "merge mode applies immediately on
    // change" bug flagged in the UI audit — every editable field requires
    // explicit confirmation.)
    target.querySelectorAll('.cfg-merge').forEach(sel => {
      sel.addEventListener('change', e => {
        const i = +e.target.dataset.i;
        st.ports[i].mergeMode = e.target.value;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.btn-apply-merge').forEach(btn => {
      btn.addEventListener('click', async e => {
        const i = +e.target.closest('button').dataset.i;
        await setMergeMode(n, st.ports[i].index, st.ports[i].mergeMode);
        st.ports[i].mergeModeApplied = st.ports[i].mergeMode;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.btn-revert-merge').forEach(btn => {
      btn.addEventListener('click', e => {
        const i = +e.target.closest('button').dataset.i;
        st.ports[i].mergeMode = st.ports[i].mergeModeApplied;
        renderConfigSection(n);
      });
    });

    byId('btnSaveAddressing').addEventListener('click', () => saveAddressing(n));
    byId('btnSaveInput').addEventListener('click', () => saveInputEnabled(n));
    byId('btnReloadConfig').addEventListener('click', () => { initConfigState(n); renderConfigSection(n); });

    // --- IP config: DHCP toggle re-renders (structural change: disables
    // static fields), everything else is oninput-state / explicit confirm.
    // Any edit here re-dirties ipApplied, so re-editing after Apply forces
    // re-applying before Arm/Confirm are reachable again.
    byId('cfgDhcp').addEventListener('change', e => { st.dhcp = e.target.checked; st.ipApplied = false; st.ipArmed = false; renderConfigSection(n); });
    if (!st.dhcp) {
      // Re-editing after Apply/Arm collapses the confirm box back to
      // "needs Apply" — this only touches the separate #ipConfirmArea
      // subtree (renderIPConfirmArea), never the field being typed into, so
      // it doesn't disturb focus/cursor position while typing.
      const dirtyIP = () => { st.ipApplied = false; st.ipArmed = false; renderIPConfirmArea(n); };
      byId('cfgIp').addEventListener('input', e => { st.ip = e.target.value; dirtyIP(); });
      byId('cfgMask').addEventListener('input', e => { st.mask = e.target.value; dirtyIP(); });
      byId('cfgGateway').addEventListener('input', e => { st.gateway = e.target.value; dirtyIP(); });
    }
    renderIPConfirmArea(n);

    // Restore any status text already recorded for this node.
    ['addressing', 'merge', 'input', 'ip'].forEach(section => {
      const s = actionStatus[key] && actionStatus[key][section];
      if (s) setStatus(key, section, s);
    });
  }

  // renderIPConfirmArea is a three-stage gate before anything reaches the
  // wire — Apply (stages the edit, same as every other field), then Arm,
  // then Confirm — because a mis-set IP can strand a node off the show
  // network, this is the one field the task brief calls out for extra
  // ceremony on top of the baseline Apply-to-confirm rule.
  function renderIPConfirmArea(n) {
    const key = keyOf(n);
    const st = configState[key];
    const area = document.getElementById('ipConfirmArea');
    if (!area) return;
    if (!st.ipApplied) {
      area.innerHTML = `
        <div class="b5-row" style="margin-top:var(--b5-space-2)">
          <button id="btnApplyIP" class="b5-btn b5-btn--sm b5-btn--primary">${UI.icon('apply')}Apply</button>
          <span class="b5-field__hint">stages the IP fields above; sending still requires arming + confirming below.</span>
        </div>`;
      byId('btnApplyIP').addEventListener('click', () => { st.ipApplied = true; renderIPConfirmArea(n); });
    } else if (!st.ipArmed) {
      area.innerHTML = `
        <div class="b5-row" style="margin-top:var(--b5-space-2)">
          ${UI.badge('ok', 'Applied')}
          <button id="btnArmIP" class="b5-btn b5-btn--sm b5-btn--danger">Arm send…</button>
          <button id="btnRevertIP" class="b5-btn b5-btn--sm b5-btn--ghost">${UI.icon('revert')}Revert</button>
        </div>`;
      byId('btnArmIP').addEventListener('click', () => { st.ipArmed = true; renderIPConfirmArea(n); });
      byId('btnRevertIP').addEventListener('click', () => { initConfigState(n); renderConfigSection(n); });
    } else {
      area.innerHTML = `
        <div class="b5-alert b5-alert--warning" style="margin-top:var(--b5-space-2)">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Confirm IP change</p>
            <p class="b5-alert__body">A mis-set IP can strand this node off the show network. Confirm: ${st.dhcp ? 'switch to DHCP' : `static ${escapeHtml(st.ip || '(unchanged)')} / ${escapeHtml(st.mask || '(unchanged)')}${st.gateway ? ' / gw ' + escapeHtml(st.gateway) : ''}`}</p>
            <div class="b5-row" style="margin-top:var(--b5-space-2)">
              <button id="btnConfirmIP" class="b5-btn b5-btn--sm b5-btn--danger">Yes, send now</button>
              <button id="btnCancelIP" class="b5-btn b5-btn--sm b5-btn--ghost">Cancel</button>
            </div>
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
    st.ipApplied = false;
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
