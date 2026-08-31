// devices.js — Devices screen (formerly "Fixtures"): every RDM responder is
// first-class here, not just fixtures — gateways, splitters, dimmers,
// wireless radios. Table groups/filters by DeviceClass; detail panel is the
// shared DeviceDetail component (see devicedetail.js — task ask: "extract
// the device-detail rendering into ONE shared module... Devices tab:
// behavior unchanged for the user; it now renders the shared component"),
// presented here in its "desktop" tab-strip mode (Info/Parameters/Sensors/
// Status as a tab shell). Rig Walk (walk.js) renders the SAME component in
// its "walk" accordion mode.
//
// Rules (architecture rev 5 §4 / task brief "UI rules (MANDATORY)"):
//  - oninput mutates state only; full re-render happens on onchange.
//  - re-render preserves the selected row, scroll position and focus.
//  - dark high-contrast, system fonts, generous sizing — b5- design-system
//    tokens/components throughout (2026-08-16 retheme).
const DevicesScreen = (() => {
  // Fixture is the "notable" device class (tag--info); infrastructure
  // classes (gateways, splitters, dimmers, wireless, controllers, other,
  // unknown) render as the neutral tag — same two-tier vocabulary the
  // design system's .b5-tag/.b5-tag--info define (was 8 hardcoded colors
  // before the retheme; the class name itself, always shown as text, still
  // carries the full distinction).
  function classTagVariant(cls) { return cls === 'Fixture' ? 'info' : undefined; }

  let nodes = [];
  let fixtures = [];
  let selectedUID = null;
  // Filters combine (AND) — class + node + universe (task ask, item 3: the
  // Devices table already aggregates every node/port's devices in one
  // rig-wide list, per Registry.Devices()/handleGetFixtures; these three
  // filters narrow that list without ever re-scoping the underlying fetch).
  // Persisted to sessionStorage (item 4: "persist sort/filter choice for
  // the session") rather than localStorage, which the rest of this app
  // reserves for cross-restart preferences like the active tab.
  let classFilter = sessionStorage.getItem('benny512.devices.classFilter') || '';
  let nodeFilter = sessionStorage.getItem('benny512.devices.nodeFilter') || '';
  let universeFilter = sessionStorage.getItem('benny512.devices.universeFilter') || '';
  let sortOrder = sessionStorage.getItem('benny512.devices.sort') || 'address';
  let activeTab = 'info'; // info | params | sensors | status

  // classifying tracks the background device_info/PRODUCT_DETAIL_ID_LIST
  // classification sweep below — unrelated to DeviceDetail's own caches.
  let classifying = {};

  // --- Clear discovered devices (POST /api/devices/clear) -----------------
  // Arm-then-confirm, same shape as the node-config IP editor's Apply →
  // Arm → Confirm gate (nodes.js) minus the separate Apply step (there's no
  // field to stage here — the "port" scope IS the device/port picker's
  // currently selected port (selectedScope, below), so arming just freezes
  // which port that confirm click will act on). Auto-disarms after a few
  // seconds of inactivity so a stale armed button left on screen can't be
  // mis-clicked later.
  let clearArmed = null; // null | 'all' | 'port'
  let clearArmedScope = null; // the {ip,bindIndex,portAddress} frozen at arm time, for 'port'
  let clearBusy = false;
  let clearMsg = '';
  let clearArmTimer = null;
  const CLEAR_ARM_TIMEOUT_MS = 8000;

  // --- Device/port picker ---------------------------------------------------
  // An Art-Net node's ports all share one physical box. Some gateways (e.g.
  // Obsidian's EN4, per the bench capture reproduced in demo.go's
  // realEN4PortReplies) advertise that box as several ArtPollReply "nodes"
  // at the SAME IP, one per physical port, each its own BindIndex — so
  // grouping on NodeKey (IP, BindIndex), which is what distinguishes
  // genuinely separate node identities elsewhere in this app (see
  // internal/session/artnetsession.go), would still split that box into
  // several top-level entries. IP alone is the key that reunites both
  // shapes: a single-reply, NumPorts=4-style node (one nodes[] entry, many
  // ports) and a bind-per-port node (many nodes[] entries at one IP, one
  // port each) both collapse to one device group either way. Two distinct
  // physical nodes coincidentally sharing an IP is not a real Art-Net
  // scenario (nodes address a LAN individually), so this is safe.
  //
  // selectedScope is the {ip,bindIndex,portAddress} of the currently
  // targeted port — the value Discover and armClear('port') act on. It
  // replaces reading a <select>'s value; the picker is now an accordion of
  // device groups, each containing one selectable port per row.
  let selectedScope = null;
  // expandedGroups tracks which device-group <details> the user has opened,
  // keyed by IP, surviving the accordion's own re-renders (same pattern as
  // walk.js's expandedSections) — a re-render must not silently collapse a
  // group the tech just opened.
  let expandedGroups = {};

  function selectedNodePort() { return selectedScope; }

  function portScopeKey(s) { return s ? `${s.ip}|${s.bindIndex}|${s.portAddress}` : ''; }

  // deviceGroups collapses `nodes` (one entry per NodeKey, i.e. per
  // IP+BindIndex, each with its own ports[]) into one entry per physical
  // device (grouped by IP alone — see the doc comment on selectedScope
  // above), each carrying every port from every NodeKey at that IP.
  function deviceGroups() {
    const order = [];
    const byIP = {};
    nodes.forEach(n => {
      if (!byIP[n.ip]) { byIP[n.ip] = { ip: n.ip, entries: [], ports: [] }; order.push(n.ip); }
      const g = byIP[n.ip];
      g.entries.push(n);
      (n.ports || []).forEach(p => {
        g.ports.push({ ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress, index: p.index, rdmEnabled: p.rdmEnabled, output: p.output, ownerName: n.shortName || n.longName || n.ip });
      });
    });
    return order.map(ip => {
      const g = byIP[ip];
      return { ip, entries: g.entries, ports: g.ports, name: deviceGroupName(g.entries) };
    });
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

  function discoveredCountForPort(port) {
    return fixtures.filter(f => f.nodeIp === port.ip && f.portAddress === port.portAddress).length;
  }

  function setSelectedScope(scope, opts) {
    selectedScope = scope;
    if (scope) expandedGroups[scope.ip] = true;
    if (!(opts && opts.silent)) {
      if (clearArmed === 'port') disarmClear(); else renderClearGroup();
    }
  }

  function renderDevicePicker() {
    const el = document.getElementById('devicePickerAccordion');
    if (!el) return;
    const groups = deviceGroups();
    // If the current selection no longer exists (a node/port disappeared)
    // or nothing is selected yet, default to the first available port so
    // Discover/Clear always have a sane, visible target.
    const stillValid = selectedScope && groups.some(g => g.ports.some(p =>
      p.ip === selectedScope.ip && p.bindIndex === selectedScope.bindIndex && p.portAddress === selectedScope.portAddress));
    if (!stillValid) {
      const first = groups.find(g => g.ports.length);
      selectedScope = first ? first.ports[0] : null;
      if (selectedScope) expandedGroups[selectedScope.ip] = true;
    }
    if (!groups.length) {
      el.innerHTML = `<p class="b5-text-muted b5-text-sm" style="margin:var(--b5-space-3)">No nodes discovered yet.</p>`;
      return;
    }
    const focusedKey = document.activeElement && document.activeElement.name === 'devicePortRadio' ? document.activeElement.value : null;
    el.innerHTML = groups.map(renderDeviceGroup).join('');
    el.querySelectorAll('details.b5-accordion__item').forEach(d => {
      d.addEventListener('toggle', () => { expandedGroups[d.dataset.ip] = d.open; });
    });
    el.querySelectorAll('input[name="devicePortRadio"]').forEach(r => {
      r.addEventListener('change', () => {
        setSelectedScope({ ip: r.dataset.ip, bindIndex: Number(r.dataset.bind), portAddress: Number(r.dataset.port) });
      });
    });
    if (focusedKey) {
      const toRefocus = el.querySelector(`input[name="devicePortRadio"][value="${CSS.escape(focusedKey)}"]`);
      if (toRefocus) toRefocus.focus();
    }
    renderClearGroup();
  }

  function renderDeviceGroup(g) {
    const discovered = fixtures.filter(f => f.nodeIp === g.ip).length;
    const open = expandedGroups[g.ip] || g.ports.some(p => selectedScope && p.ip === selectedScope.ip && p.bindIndex === selectedScope.bindIndex && p.portAddress === selectedScope.portAddress);
    return `
      <details class="b5-accordion__item" data-ip="${escapeHtml(g.ip)}" ${open ? 'open' : ''}>
        <summary class="b5-accordion__trigger">
          <span>${escapeHtml(g.name)} <span class="b5-text-muted b5-text-sm">(${escapeHtml(g.ip)})</span>
            ${UI.tag(`${g.ports.length} port${g.ports.length === 1 ? '' : 's'}`)}
            ${discovered ? UI.tag(`${discovered} discovered`, 'info') : ''}
          </span>
          ${UI.icon('chevron-expand')}
        </summary>
        <div class="b5-accordion__panel">
          ${g.ports.map(p => renderPortRow(g, p)).join('') || '<p class="b5-text-muted b5-text-sm">This device has no ports.</p>'}
        </div>
      </details>`;
  }

  function renderPortRow(g, p) {
    const key = portScopeKey(p);
    const checked = selectedScope && portScopeKey(selectedScope) === key;
    const count = discoveredCountForPort(p);
    // Only worth calling out which NodeKey (bind index) a port came from
    // when the group actually spans more than one — the bind-per-port
    // gateway shape (see the doc comment above deviceGroups). A single-node
    // group would just repeat "Port N" (the port's own name is already
    // shown), which reads as noise rather than disambiguation.
    const ownerNote = g.entries.length > 1 ? ` (bind ${p.bindIndex})` : '';
    return `
      <label class="b5-checkbox b5-port-pick${checked ? ' is-selected' : ''}">
        <input type="radio" name="devicePortRadio" value="${escapeHtml(key)}" data-ip="${escapeHtml(p.ip)}" data-bind="${p.bindIndex}" data-port="${p.portAddress}" ${checked ? 'checked' : ''}>
        <span>Port ${p.index}${ownerNote} &mdash; universe ${UI.formatUniverse(p.portAddress)}
          ${p.rdmEnabled ? '' : UI.tag('RDM off', 'warn')}
          ${count ? UI.tag(`${count} discovered`, 'info') : UI.tag('none discovered')}
        </span>
      </label>`;
  }

  function nodePortLabel(scope) {
    if (!scope) return '(no port selected)';
    const n = nodes.find(x => x.ip === scope.ip && x.bindIndex === scope.bindIndex);
    const name = n ? (n.shortName || n.longName || n.ip) : scope.ip;
    return `${escapeHtml(name)} (${escapeHtml(scope.ip)}) universe ${UI.formatUniverse(scope.portAddress)}`;
  }

  function armClear(kind) {
    if (kind === 'port') {
      clearArmedScope = selectedNodePort();
      if (!clearArmedScope) return;
    }
    clearArmed = kind;
    clearMsg = '';
    renderClearGroup();
    clearTimeout(clearArmTimer);
    clearArmTimer = setTimeout(disarmClear, CLEAR_ARM_TIMEOUT_MS);
  }

  function disarmClear() {
    clearArmed = null;
    clearArmedScope = null;
    clearTimeout(clearArmTimer);
    renderClearGroup();
  }

  async function confirmClear() {
    const kind = clearArmed;
    const scope = clearArmedScope;
    clearTimeout(clearArmTimer);
    clearBusy = true;
    renderClearGroup();
    try {
      let res;
      if (kind === 'all') {
        res = await Api.clearDevicesAll();
        clearMsg = `Cleared ${res.cleared} device${res.cleared === 1 ? '' : 's'} across all ports (${res.todCleared} discovery table${res.todCleared === 1 ? '' : 's'} reset).`;
      } else {
        res = await Api.clearDevicesPort(scope.ip, scope.portAddress);
        clearMsg = `Cleared ${res.cleared} device${res.cleared === 1 ? '' : 's'} on ${nodePortLabel(scope)}.`;
      }
      await refreshFixtures();
    } catch (e) {
      clearMsg = 'error: ' + e.message;
    }
    clearBusy = false;
    clearArmed = null;
    clearArmedScope = null;
    renderClearGroup();
  }

  function renderClearGroup() {
    const group = document.getElementById('clearDevicesGroup');
    const statusRow = document.getElementById('clearDevicesStatusRow');
    if (!group) return;
    const sel = selectedNodePort();
    if (clearBusy) {
      group.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Clearing…</span>`;
    } else if (clearArmed === 'port') {
      group.innerHTML = `
        <span class="b5-badge b5-badge--warning">${UI.icon('status-warning')}Confirm</span>
        <button id="btnClearConfirm" class="b5-btn b5-btn--sm b5-btn--danger b5-clear-armed">Yes, clear devices on ${nodePortLabel(clearArmedScope)}</button>
        <button id="btnClearCancel" class="b5-btn b5-btn--sm b5-btn--ghost">${UI.icon('revert')}Cancel</button>`;
    } else if (clearArmed === 'all') {
      group.innerHTML = `
        <span class="b5-badge b5-badge--warning">${UI.icon('status-warning')}Confirm</span>
        <button id="btnClearConfirm" class="b5-btn b5-btn--sm b5-btn--danger b5-clear-armed">Yes, clear ALL discovered devices (every port)</button>
        <button id="btnClearCancel" class="b5-btn b5-btn--sm b5-btn--ghost">${UI.icon('revert')}Cancel</button>`;
    } else {
      group.innerHTML = `
        <span class="b5-text-muted b5-text-sm">Clear discovered devices:</span>
        <button id="btnClearPort" class="b5-btn b5-btn--sm b5-btn--danger" ${sel ? '' : 'disabled'} title="Clear only the devices discovered on the port selected above">Clear this port</button>
        <button id="btnClearAll" class="b5-btn b5-btn--sm b5-btn--danger" title="Clear every discovered device on every port">Clear ALL ports</button>`;
    }
    if (statusRow) {
      statusRow.innerHTML = clearMsg
        ? `<p class="b5-text-sm b5-text-muted" style="margin:var(--b5-space-2) 0 0" id="clearDevicesMsg">${escapeHtml(clearMsg)}</p>`
        : '';
    }
    const btnPort = document.getElementById('btnClearPort');
    if (btnPort) btnPort.addEventListener('click', () => armClear('port'));
    const btnAll = document.getElementById('btnClearAll');
    if (btnAll) btnAll.addEventListener('click', () => armClear('all'));
    const btnConfirm = document.getElementById('btnClearConfirm');
    if (btnConfirm) btnConfirm.addEventListener('click', confirmClear);
    const btnCancel = document.getElementById('btnClearCancel');
    if (btnCancel) btnCancel.addEventListener('click', disarmClear);
  }

  async function refreshNodes() {
    nodes = await Api.getNodes();
    renderDevicePicker();
    refreshFilterOptions();
  }

  async function refreshFixtures() {
    fixtures = await Api.getFixtures();
    refreshFilterOptions();
    render();
    renderDevicePicker();
    classifyUnknown();
  }

  function nodeFilterKey(ipLike) {
    return `${ipLike.nodeIp || ipLike.ip}|${ipLike.bindIndex}`;
  }

  function refreshFilterOptions() {
    const nodeSel = document.getElementById('deviceNodeFilter');
    if (nodeSel) {
      nodeSel.innerHTML = '<option value="">Node: all</option>';
      nodes.forEach(n => {
        const opt = document.createElement('option');
        opt.value = nodeFilterKey(n);
        opt.textContent = n.shortName || n.longName || n.ip;
        nodeSel.appendChild(opt);
      });
      nodeSel.value = nodeFilter;
      if (nodeSel.value !== nodeFilter) { nodeFilter = ''; persistFilters(); }
    }
    const uniSel = document.getElementById('deviceUniverseFilter');
    if (uniSel) {
      const universes = Array.from(new Set(fixtures.map(f => f.portAddress))).sort((a, b) => a - b);
      uniSel.innerHTML = '<option value="">Universe: all</option>';
      universes.forEach(u => {
        const opt = document.createElement('option');
        opt.value = String(u);
        opt.textContent = `Universe ${UI.formatUniverse(u)}`;
        uniSel.appendChild(opt);
      });
      uniSel.value = universeFilter;
      if (uniSel.value !== universeFilter) { universeFilter = ''; persistFilters(); }
    }
  }

  function persistFilters() {
    sessionStorage.setItem('benny512.devices.classFilter', classFilter);
    sessionStorage.setItem('benny512.devices.nodeFilter', nodeFilter);
    sessionStorage.setItem('benny512.devices.universeFilter', universeFilter);
    sessionStorage.setItem('benny512.devices.sort', sortOrder);
  }

  // classifyUnknown fires a background classification probe for every
  // device the registry hasn't classified yet — unchanged from before the
  // refactor; independent of DeviceDetail's own probing.
  function classifyUnknown() {
    const targets = fixtures.filter(f => f.class === 'Unknown' && !classifying[f.uid]);
    if (!targets.length) return;
    targets.forEach(f => { classifying[f.uid] = true; });
    Promise.allSettled(targets.map(f => Promise.allSettled([
      Api.getParam(f.uid, 'device_info').then(r => DeviceDetail.noteDeviceInfo(f.uid, r.value)),
      Api.getDeviceParam(f.uid, '0070'),
      Api.getDeviceParam(f.uid, '0011'),
      Api.getDeviceParam(f.uid, '0081'),
      Api.getDeviceParam(f.uid, '0080'),
    ]))).then(async () => {
      targets.forEach(f => { classifying[f.uid] = false; });
      fixtures = await Api.getFixtures();
      render();
    });
  }

  function filteredFixtures() {
    return fixtures.filter(f => {
      if (classFilter && f.class !== classFilter) return false;
      if (nodeFilter && nodeFilterKey(f) !== nodeFilter) return false;
      if (universeFilter !== '' && String(f.portAddress) !== universeFilter) return false;
      return true;
    });
  }

  function sortKeyFor(f) {
    const info = DeviceDetail._caches.infoCache[f.uid];
    const di = info && info.deviceInfo;
    return {
      uid: f.uid,
      model: f.model || '',
      portAddress: f.portAddress,
      address: di ? di.DMXStartAddress : 0,
      footprint: di ? di.DMXFootprint : 0,
      footprintKnown: !!di,
      addressKnown: !!di,
    };
  }

  function visibleFixtures() {
    const list = filteredFixtures().slice();
    list.sort((a, b) => Api.compareDevices(sortKeyFor(a), sortKeyFor(b), sortOrder));
    return list;
  }

  function updateFilterSummary() {
    const parts = [];
    if (classFilter) parts.push(`class=${classFilter}`);
    if (nodeFilter) {
      const n = nodes.find(x => nodeFilterKey(x) === nodeFilter);
      parts.push(`node=${n ? (n.shortName || n.ip) : nodeFilter}`);
    }
    if (universeFilter !== '') parts.push(`universe=${universeFilter}`);
    const summaryEl = document.getElementById('deviceFilterSummary');
    const clearBtn = document.getElementById('btnClearDeviceFilters');
    const shown = filteredFixtures().length;
    if (summaryEl) {
      summaryEl.textContent = parts.length
        ? `Filtering by ${parts.join(', ')} — showing ${shown} of ${fixtures.length} device(s)`
        : (fixtures.length ? `Showing all ${fixtures.length} device(s)` : '');
    }
    if (clearBtn) clearBtn.style.display = parts.length ? '' : 'none';
  }

  function addressLabel(f) {
    const info = DeviceDetail._caches.infoCache[f.uid];
    const di = info && info.deviceInfo;
    if (!di) return classifying[f.uid] ? '…' : '—';
    return Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true);
  }

  function manufacturerCell(f) {
    if (classifying[f.uid] && !f.manufacturerLabelKnown) return '…';
    return f.manufacturer || f.manufacturerName || '—';
  }

  // unreachableNoteHTML renders the server's finished sentence verbatim.
  //
  // Round 4's bench symptom was a row that simply stopped filling in once the
  // fixture behind a CRMX link stopped answering — at the bench that is
  // indistinguishable from Benny512 being broken. The server composes the
  // sentence (unreachableNote in internal/web/server.go) so the wording is
  // identical here, in device detail and in Rig Walk; this only places it.
  //
  // The tag and the sentence are both text, per the standing accessibility
  // constraint that color is never the sole signal.
  function unreachableNoteHTML(f) {
    if (!f || !f.unreachable) return '';
    const retry = f.retryAt ? ` Next try ${new Date(f.retryAt).toLocaleTimeString()}.` : '';
    const text = (f.unreachableNote || 'Not answering through its wireless proxy.') + retry;
    return `<div class="b5-device__note">${escapeHtml(text)}</div>`;
  }

  function modelCell(f) {
    if (classifying[f.uid] && !f.modelDescriptionKnown && !f.hasDeviceInfo) return '…';
    return f.model || '—';
  }

  function render() {
    updateFilterSummary();
    const tbody = document.querySelector('#fixturesTable tbody');
    const scrollTop = tbody.parentElement.scrollTop;
    tbody.innerHTML = '';
    const list = visibleFixtures();
    if (!list.length) {
      tbody.innerHTML = `<tr><td colspan="6"><div class="b5-empty">${UI.icon('nav-devices')}<span class="b5-empty__title">No devices found</span><span class="b5-empty__body">Check that nodes are discovered and universes are assigned, or clear active filters.</span></div></td></tr>`;
      return;
    }
    list.forEach(f => {
      const tr = document.createElement('tr');
      if (f.uid === selectedUID) tr.classList.add('is-selected');
      if (f.unreachable) tr.classList.add('is-unreachable');
      const classCell = UI.tag(f.class, classTagVariant(f.class)) +
        (f.unreachable ? ' ' + UI.tag('Not answering', 'warn') : '');
      tr.innerHTML = `
        <td data-label="Class">${classCell}</td>
        <td data-label="Manufacturer">${escapeHtml(manufacturerCell(f))}${unreachableNoteHTML(f)}</td>
        <td data-label="Model/Type">${escapeHtml(modelCell(f))}</td>
        <td data-label="UID" class="b5-table__mono">${escapeHtml(f.uid)}</td>
        <td data-label="Address" class="b5-table__mono">${addressLabel(f)}</td>
        <td data-label="Node/Port">${escapeHtml(f.nodeIp)} / U${UI.formatUniverse(f.portAddress)}</td>
      `;
      tr.addEventListener('click', () => { selectDevice(f.uid); });
      tbody.appendChild(tr);
    });
    tbody.parentElement.scrollTop = scrollTop;
  }

  function selectDevice(uid) {
    if (selectedUID === uid) return;
    selectedUID = uid;
    activeTab = 'info';
    render();
    renderDetail();
    DeviceDetail.select(uid, sectionsForActiveTab());
  }

  // sectionsForActiveTab: desktop mode shows exactly one section at a time
  // (the active tab), so only that section is "in view" for DeviceDetail's
  // fetch/subscribe policy — task ask: "Introspection... lazy per section"
  // and "Sensor WS subscriptions must follow the selected device... never
  // fired automatically for every device".
  function sectionsForActiveTab() {
    return { info: activeTab === 'info', params: activeTab === 'params', sensors: activeTab === 'sensors', status: activeTab === 'status' };
  }

  // --- detail panel: tab shell -------------------------------------------

  function renderDetail() {
    const el = document.getElementById('fixtureDetail');
    const f = fixtures.find(x => x.uid === selectedUID);
    if (!f) {
      el.innerHTML = `<div class="b5-panel__body"><div class="b5-empty">${UI.icon('nav-devices')}<span class="b5-empty__title">No device selected</span><span class="b5-empty__body">Select a device to view/edit its RDM parameters, sensors and status.</span></div></div>`;
      return;
    }
    el.innerHTML = `
      <div class="b5-panel__header">
        <h2 class="b5-panel__title">${escapeHtml(manufacturerCell(f))} — <span class="b5-text-mono">${escapeHtml(f.uid)}</span></h2>
        ${UI.tag(f.class, classTagVariant(f.class))}
      </div>
      <div class="b5-panel__body">
        <div class="b5-row" style="margin-bottom:var(--b5-space-4)">
          <strong class="b5-text-sm">RDM capture export (this device):</strong>
          <button id="btnExportDeviceJson" class="b5-btn b5-btn--sm">${UI.icon('export')}Export JSON</button>
          <button id="btnExportDeviceTxt" class="b5-btn b5-btn--sm">${UI.icon('export')}Export TXT</button>
        </div>
        <div class="b5-tabs">
          <div class="b5-tabs__list" id="deviceTabs">
            <button class="b5-tabs__tab detail-tab-btn" data-tab="info">Info</button>
            <button class="b5-tabs__tab detail-tab-btn" data-tab="params">Parameters</button>
            <button class="b5-tabs__tab detail-tab-btn" data-tab="sensors">Sensors</button>
            <button class="b5-tabs__tab detail-tab-btn" data-tab="status">Status</button>
          </div>
          <div class="b5-tabs__panel" id="deviceTabBody"></div>
        </div>
        <span class="b5-text-muted b5-text-sm" id="fxStatus"></span>
      </div>
    `;
    document.getElementById('btnExportDeviceJson').addEventListener('click', () => {
      window.open(Api.exportUrl('json', { uid: f.uid }), '_blank');
    });
    document.getElementById('btnExportDeviceTxt').addEventListener('click', () => {
      window.open(Api.exportUrl('txt', { uid: f.uid }), '_blank');
    });
    el.querySelectorAll('.detail-tab-btn').forEach(btn => {
      btn.classList.toggle('is-active', btn.dataset.tab === activeTab);
      btn.addEventListener('click', () => {
        if (activeTab === btn.dataset.tab) return;
        activeTab = btn.dataset.tab;
        renderDetail();
        DeviceDetail.select(f.uid, sectionsForActiveTab());
      });
    });
    renderTabBody(f);
  }

  function setFxStatus(msg) {
    const el = document.getElementById('fxStatus');
    if (el) el.textContent = msg;
  }

  function renderTabBody(f) {
    const body = document.getElementById('deviceTabBody');
    if (!body) return;
    switch (activeTab) {
      case 'info': DeviceDetail.renderInfoSection(body, f); break;
      case 'params': DeviceDetail.renderParamsSection(body, f, setFxStatus); break;
      case 'sensors': DeviceDetail.renderSensorsSection(body, f); break;
      case 'status': DeviceDetail.renderStatusSection(body, f); break;
    }
  }

  function init() {
    document.getElementById('btnDiscover').addEventListener('click', discover);
    // Changing the port selection while a 'port'-scope clear is armed would
    // let a confirm click fire against a port the tech isn't looking at any
    // more — setSelectedScope (wired from each port radio's 'change' in
    // renderDevicePicker) disarms rather than silently retargeting.
    renderClearGroup();
    // A clear can be triggered from any open browser (task ask) — refresh
    // this one's table either way; our own confirmClear() already awaits
    // refreshFixtures() itself, so this is a harmless extra refresh when
    // it's our own action, and the only refresh when it's someone else's.
    Live.on('devices_cleared', () => { refreshFixtures(); });
    // Universe base changed on the Settings screen — refresh the universe
    // filter dropdown's labels, the device/port picker's universe labels,
    // and the table's Node/Port column in place.
    window.addEventListener('b5-universe-base-changed', () => { refreshFilterOptions(); render(); renderDevicePicker(); });

    const classSel = document.getElementById('deviceClassFilter');
    if (classSel) {
      classSel.value = classFilter;
      classSel.addEventListener('change', (e) => { classFilter = e.target.value; persistFilters(); render(); });
    }
    const nodeSel = document.getElementById('deviceNodeFilter');
    if (nodeSel) {
      nodeSel.addEventListener('change', (e) => { nodeFilter = e.target.value; persistFilters(); render(); });
    }
    const uniSel = document.getElementById('deviceUniverseFilter');
    if (uniSel) {
      uniSel.addEventListener('change', (e) => { universeFilter = e.target.value; persistFilters(); render(); });
    }
    const sortSel = document.getElementById('deviceSort');
    if (sortSel) {
      sortSel.value = sortOrder;
      sortSel.addEventListener('change', (e) => { sortOrder = e.target.value; persistFilters(); render(); });
    }
    const clearBtn = document.getElementById('btnClearDeviceFilters');
    if (clearBtn) {
      clearBtn.addEventListener('click', () => {
        classFilter = ''; nodeFilter = ''; universeFilter = '';
        persistFilters();
        if (classSel) classSel.value = '';
        if (nodeSel) nodeSel.value = '';
        if (uniSel) uniSel.value = '';
        render();
      });
    }

    // DeviceDetail.init registers this screen's re-render callback — fires
    // on every async cache update (param resolved, sensor pushed, status
    // loaded, introspect progressed) regardless of which screen is
    // currently visible; each render function below is a cheap no-op
    // when this screen/tab isn't the active one (renderDetail/render just
    // re-touch DOM that's already correct, and the underlying selectedUID
    // guards mean a Rig Walk-driven update never fires wire traffic here).
    //
    // scope names which section's data changed ('info'/'params'/'sensors'/
    // 'status', or undefined for "not sure, refresh regardless"). This
    // desktop shell shows exactly one tab at a time, so a scoped update that
    // doesn't match the active tab has nothing to redraw here — skipping
    // renderTabBody in that case is the fix for the bench report "Device
    // Label kicks you out mid-typing": a 'sensors' push while activeTab is
    // 'params' no longer touches the Parameters tab's DOM (and the Device
    // Label input inside it) at all. render() (the devices table) has no
    // editable fields and stays cheap to always refresh.
    DeviceDetail.init((scope) => {
      render();
      if (selectedUID && (!scope || scope === activeTab)) {
        renderTabBody(fixtures.find(x => x.uid === selectedUID) || { uid: selectedUID });
      }
    });

    refreshNodes();
    refreshFixtures();
  }

  async function discover() {
    const status = document.getElementById('discoverStatus');
    if (!selectedScope) { status.textContent = 'no device/port selected'; return; }
    const { ip, bindIndex, portAddress } = selectedScope;
    status.innerHTML = UI.spinner() + 'discovering…';
    try {
      const res = await Api.discover(ip, bindIndex, portAddress);
      status.textContent = `found ${res.uids.length} device(s)${res.complete ? '' : ' (incomplete)'}`;
      await refreshFixtures();
    } catch (e) {
      status.textContent = 'error: ' + e.message;
    }
  }

  return { init, refreshNodes, refreshFixtures };
})();
