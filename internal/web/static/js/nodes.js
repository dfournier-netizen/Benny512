// nodes.js — Nodes screen: device-grouped accordion + detail pane +
// editable node/network configuration (Phase 1c+: ArtAddress/ArtInput/
// ArtIpProg via internal/web/node_config.go).
//
// Device grouping + sorting (this round): owner's original ask — "let's
// start grouping ports on the same device into a drop down or some other
// logical block. e.g. our EN4 has 4 ports, so link them all together. Maybe
// even make our inspector pane select per device, with the ability to pick
// which port is targeted" — first landed on the Devices screen, then the
// owner clarified it belongs here instead ("apply that same sorting and
// feature set... to the nodes tab, not devices... revert the changes to the
// device tab"). See deviceGroups() below for the IP-grouping rationale
// (shared with devices.js's now-reverted picker) and compareIPNumeric() for
// the numeric-vs-lexicographic sort fix.
//
// Rules (architecture rev 5 §4, adopted from Rackmaster's pitfalls, and the
// task brief's MANDATORY UI rules):
//  - oninput mutates state only; full re-render happens on onchange/explicit
//    refresh.
//  - re-render preserves the selected row/port and scroll position.
//  - the *editable config section* is built once per node selection and is
//    NOT touched by the periodic (WS-triggered) node-list refresh — only
//    the read-only info/ports block re-renders on every poll tick. This is
//    what keeps a half-typed short name or IP field from being clobbered
//    out from under Dom every ~3s by the node's own ArtPollReply traffic.
const NodesScreen = (() => {
  let nodes = [];
  let selectedKey = null;   // "ip|bindIndex" — the NodeKey currently backing the inspector
  let selectedIP = null;    // IP of the currently focused device group (accordion selection)
  let detailBuiltFor = null; // key whose config section is currently in the DOM
  // expandedGroups tracks which device-group <details> the user has opened,
  // keyed by IP, surviving the accordion's own re-renders (same pattern
  // devices.js used for its now-reverted picker, and walk.js's
  // expandedSections) — a re-render must not silently collapse a group the
  // tech just opened, and periodic refresh() rebuilds this accordion every
  // ~3s (WS 'node' events), so this matters far more here than a
  // one-shot picker.
  let expandedGroups = {};

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

  // --- Device grouping (IP) + numeric sort ---------------------------------
  // An Art-Net node's ports all share one physical box. Some gateways (e.g.
  // Obsidian's EN4, per the bench capture reproduced in demo.go's
  // realEN4PortReplies) advertise that box as several ArtPollReply "nodes"
  // at the SAME IP, one per physical port, each its own BindIndex — so
  // grouping on NodeKey (IP, BindIndex), which is what distinguishes
  // genuinely separate node identities elsewhere in this app (see
  // internal/session/artnetsession.go), would still split that box into
  // several top-level entries. IP alone is the key that reunites both
  // shapes: a single-reply, NumPorts=4-style node (one nodes[] entry, many
  // ports — demo.go's en4Reply at 2.11.90.2) and a bind-per-port node (many
  // nodes[] entries at one IP, one port each — realEN4PortReplies at
  // 2.11.90.4) both collapse to one device group either way. Two distinct
  // physical nodes coincidentally sharing an IP is not a real Art-Net
  // scenario (nodes address a LAN individually), so this is safe.
  function deviceGroups() {
    const order = [];
    const byIP = {};
    nodes.forEach(n => {
      if (!byIP[n.ip]) { byIP[n.ip] = { ip: n.ip, entries: [] }; order.push(n.ip); }
      byIP[n.ip].entries.push(n);
    });
    const groups = order.map(ip => {
      const g = byIP[ip];
      const ports = [];
      g.entries.forEach(n => {
        (n.ports || []).forEach(p => {
          ports.push({
            key: keyOf(n), ip: n.ip, bindIndex: n.bindIndex, index: p.index,
            // Sort key only — the flat port-address (net/sub/universe packed
            // into one number), not the display nibble. Numeric, never
            // string-compared (see compareIPNumeric's doc comment on the
            // sibling bug this guards against).
            portAddress: p.output ? p.outputAddress : (p.input ? p.inputAddress : 0),
            raw: p,
          });
        });
      });
      ports.sort((a, b) => a.portAddress - b.portAddress);
      return { ip: g.ip, entries: g.entries, ports, name: deviceGroupName(g.entries) };
    });
    groups.sort((a, b) => compareIPNumeric(a.ip, b.ip));
    return groups;
  }

  // compareIPNumeric: sort dotted-quad IPs by their numeric octet values,
  // NOT lexicographically. A plain string/array .sort() would put
  // "2.11.90.10" before "2.11.90.2" (character '1' < '2'), exactly the
  // sibling bug the owner already flagged on another screen ("string-
  // sorting numbers"). Comparing octet-by-octet as integers instead makes
  // .10 sort after .2, as a human expects.
  function compareIPNumeric(a, b) {
    const pa = a.split('.').map(Number);
    const pb = b.split('.').map(Number);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
      const d = (pa[i] || 0) - (pb[i] || 0);
      if (d) return d;
    }
    return 0;
  }

  // deviceGroupName prefers a name shared by every NodeKey contributing to
  // this IP (the common case: one node, or a bind-per-port gateway that
  // repeats its own long name on every reply — demo.go's realEN4PortReplies
  // all report LongName "NETRON EN4" despite distinct per-port ShortNames).
  // Falling back to the first entry's own name keeps a group labeled even
  // when nothing agrees.
  function deviceGroupName(entries) {
    const longNames = Array.from(new Set(entries.map(n => n.longName).filter(Boolean)));
    if (longNames.length === 1) return longNames[0];
    const shortNames = Array.from(new Set(entries.map(n => n.shortName).filter(Boolean)));
    if (shortNames.length === 1) return shortNames[0];
    return entries[0].shortName || entries[0].longName || entries[0].ip;
  }

  // deviceStatusBadge/deviceLastSeen: device-group rollups of the same
  // per-node facts nodeStatusBadge already reported (below) — a physical
  // device is "stale" if any contributing NodeKey is (any port could be the
  // one that stopped answering), "RDM" only if every contributing NodeKey
  // reports RDM-capable.
  function deviceStatusBadge(g) {
    if (g.entries.some(n => n.stale)) return UI.badge('stale', 'Stale');
    if (g.entries.every(n => n.rdmCapable)) return UI.badge('ok', 'RDM');
    return UI.badge('unknown', 'No RDM');
  }

  function deviceLastSeen(g) {
    const times = g.entries.map(n => n.lastSeen).filter(Boolean);
    if (!times.length) return null;
    return times.reduce((a, b) => (new Date(a) > new Date(b) ? a : b));
  }

  async function refresh() {
    nodes = await Api.getNodes();
    render();
  }

  // nodeStatusBadge combines the two facts the app already tracked
  // (rdmCapable, stale) into one status column with an explicit text label
  // (never color alone) instead of two separate badge columns.
  function nodeStatusBadge(n) {
    if (n.stale) return UI.badge('stale', 'Stale');
    if (n.rdmCapable) return UI.badge('ok', 'RDM');
    return UI.badge('unknown', 'No RDM');
  }

  // --- Device accordion (grouped, sorted) ----------------------------------

  function render() {
    const groups = deviceGroups();
    renderAccordion(groups);

    const el = document.getElementById('nodeDetail');
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) {
      detailBuiltFor = null;
      el.innerHTML = `<div class="b5-panel__body"><div class="b5-empty">${UI.icon('network-node')}<span class="b5-empty__title">No node selected</span><span class="b5-empty__body">Select a device to see its ports and universes.</span></div></div>`;
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

  function renderAccordion(groups) {
    const el = document.getElementById('nodesAccordion');
    if (!el) return;
    const scrollTop = el.scrollTop;
    // If the current selection no longer exists (a node disappeared) default
    // to nothing selected rather than silently pointing at a stale key.
    if (selectedKey && !groups.some(g => g.entries.some(n => keyOf(n) === selectedKey))) {
      selectedKey = null;
      selectedIP = null;
    }
    if (!groups.length) {
      el.innerHTML = `<p class="b5-text-muted b5-text-sm" style="margin:var(--b5-space-3)">No nodes discovered yet.</p>`;
      return;
    }
    const focusedKey = document.activeElement && document.activeElement.name === 'nodePortTarget' ? document.activeElement.value : null;
    el.innerHTML = groups.map(renderDeviceGroup).join('');
    // ROOT CAUSE (found verifying this round): per the HTML spec, a <details>
    // element that is PARSED with the `open` attribute already present still
    // queues a 'toggle' event — it is not limited to genuine user clicks or
    // script setting .open on an already-connected element. render() re-parses
    // every expanded group's `<details open>` from scratch via innerHTML on
    // every call, so a 'toggle' listener that itself calls render() (the
    // earlier version of this code did, to auto-default the inspector to a
    // device's first port on expand) re-queues a fresh 'toggle' for every
    // open group on every one of those re-renders — an unbounded cascade
    // across all open groups that pegs the main thread (confirmed via a
    // MutationObserver: >1800 render() calls inside 300ms with 4 groups
    // open). This listener therefore only tracks open/closed state; it must
    // never call render() (or anything that touches this <details>'s own
    // markup) from inside 'toggle'. Picking a port (the explicit port-target
    // control below) is what selects a device now — opening a group alone
    // does not.
    el.querySelectorAll('details.b5-accordion__item').forEach(d => {
      d.addEventListener('toggle', () => { expandedGroups[d.dataset.ip] = d.open; });
      // Opening a device with nothing targeted in it yet defaults the
      // inspector to that device's first port — "select per device" (task
      // ask) shouldn't require a second click on a port row too. Driven off
      // 'click' on the summary (a genuine, trusted user gesture) rather than
      // 'toggle' (see the ROOT CAUSE comment above for why 'toggle' itself
      // must stay render()-free); setTimeout(0) lets the browser's own
      // open/close default action land first so d.open reflects the click's
      // actual result before this reads it.
      const summary = d.querySelector('summary');
      if (summary) {
        summary.addEventListener('click', () => {
          setTimeout(() => {
            if (d.open && !(selectedKey && d.dataset.ip === selectedIP)) {
              const g = groups.find(x => x.ip === d.dataset.ip);
              if (g && g.ports.length) {
                selectedKey = g.ports[0].key;
                selectedIP = g.ip;
                render();
              }
            }
          }, 0);
        });
      }
    });
    el.querySelectorAll('input[name="nodePortTarget"]').forEach(r => {
      r.addEventListener('change', () => {
        selectedKey = r.value;
        selectedIP = r.dataset.ip;
        render();
      });
    });
    if (focusedKey) {
      const toRefocus = el.querySelector(`input[name="nodePortTarget"][value="${CSS.escape(focusedKey)}"]`);
      if (toRefocus) toRefocus.focus();
    }
    el.scrollTop = scrollTop;
  }

  function renderDeviceGroup(g) {
    const open = expandedGroups[g.ip] || g.ip === selectedIP;
    const lastSeen = deviceLastSeen(g);
    return `
      <details class="b5-accordion__item" data-ip="${escapeHtml(g.ip)}" ${open ? 'open' : ''}>
        <summary class="b5-accordion__trigger">
          <span>${escapeHtml(g.name)} <span class="b5-text-muted b5-text-sm">(${escapeHtml(g.ip)})</span>
            ${UI.tag(`${g.ports.length} port${g.ports.length === 1 ? '' : 's'}`)}
            ${deviceStatusBadge(g)}
            <span class="b5-text-muted b5-text-sm">${lastSeen ? 'last seen ' + new Date(lastSeen).toLocaleTimeString() : ''}</span>
          </span>
          ${UI.icon('chevron-expand')}
        </summary>
        <div class="b5-accordion__panel">
          <table class="b5-table b5-table--responsive">
            <thead><tr><th></th><th>Port</th><th>Direction</th><th>Universe</th><th>RDM</th></tr></thead>
            <tbody>${g.ports.map(p => renderPortRow(g, p)).join('') || '<tr><td colspan="5">This device has no ports.</td></tr>'}</tbody>
          </table>
        </div>
      </details>`;
  }

  // renderPortRow is the explicit "which port is targeted" control (owner's
  // original ask). Its radio value is the port's owning NodeKey — a
  // bind-per-port device (demo.go's realEN4PortReplies) has one NodeKey per
  // port, so picking a port here switches which NodeKey backs the entire
  // inspector below (names, addressing, merge, input-enable, IP config all
  // apply to one NodeKey at a time — there is no finer-grained wire
  // primitive to target). A single-reply multi-port node (demo.go's
  // en4Reply) has one NodeKey for all its ports, so every row in that
  // device shares one radio value/key — selecting any of them targets the
  // same node, whose own ports table (renderConfigSection, unchanged)
  // already edits each port's universe/merge/input individually.
  function renderPortRow(g, p) {
    const checked = selectedKey === p.key;
    // Only worth calling out which NodeKey (bind index) a port came from
    // when the group actually spans more than one — the bind-per-port
    // gateway shape. A single-node group would just repeat "Port N" (the
    // port's own name is already shown), which reads as noise rather than
    // disambiguation.
    const bindNote = g.entries.length > 1 ? ` (bind ${p.bindIndex})` : '';
    return `
      <tr class="${checked ? 'is-selected' : ''}">
        <td data-label="Target"><label class="b5-checkbox"><input type="radio" name="nodePortTarget" value="${escapeHtml(p.key)}" data-ip="${escapeHtml(g.ip)}" ${checked ? 'checked' : ''}><span class="b5-visually-hidden">Target port ${p.index}${bindNote}</span></label></td>
        <td data-label="Port">${p.index}${bindNote}</td>
        <td data-label="Direction">${escapeHtml(portDirectionLabel(p.raw))}</td>
        <td data-label="Universe">${escapeHtml(portUniverseLabel(p.raw))}</td>
        <td data-label="RDM">${p.raw.rdmEnabled ? UI.badge('ok', 'Yes') : UI.badge('unknown', 'No')}</td>
      </tr>`;
  }

  // --- read-only info/ports block (rebuilt every refresh) -----------------

  function buildDetailShell(n) {
    const el = document.getElementById('nodeDetail');
    const group = deviceGroups().find(g => g.ip === n.ip);
    // devicePortPicker: when this physical device is more than one NodeKey
    // (bind-per-port shape), surface the same port-target control inline in
    // the inspector header too, so "which port" is visible without having
    // to look back up at the accordion — selecting here just proxies to the
    // same radios (same name, same value semantics).
    const portPicker = (group && group.entries.length > 1) ? `
      <div class="b5-field" style="margin-top:var(--b5-space-2)">
        <label class="b5-field__label" for="nodeDetailPortTarget">Target port</label>
        <select id="nodeDetailPortTarget" class="b5-select" style="max-width:20em">
          ${group.ports.map(p => `<option value="${escapeHtml(p.key)}" ${p.key === selectedKey ? 'selected' : ''}>Port ${p.index} (bind ${p.bindIndex})</option>`).join('')}
        </select>
      </div>` : '';
    el.innerHTML = `
      <div class="b5-panel__header">
        <h2 class="b5-panel__title">${escapeHtml(n.longName || n.shortName)}</h2>
        ${nodeStatusBadge(n)}
      </div>
      <div class="b5-panel__body b5-stack">
        ${portPicker}
        <div id="nodeInfoStatic"></div>
        <div id="nodeConfigSection"></div>
      </div>
    `;
    const picker = document.getElementById('nodeDetailPortTarget');
    if (picker) {
      picker.addEventListener('change', (e) => {
        selectedKey = e.target.value;
        render();
      });
    }
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
          <p class="b5-alert__title">Node configuration — partially verified against real hardware</p>
          <p class="b5-alert__body">ArtIpProg's Command bits and gateway field were corrected and confirmed against a real node capture (RDM-LOG19) and the Art-Net 4 spec. ArtAddress and ArtInput wire formats have not been confirmed the same way — ArtInput in particular is sourced from a single secondary reference, not a primary spec fetch or capture this session. Verify results on the node's own display/web UI before relying on any change made here.</p>
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
