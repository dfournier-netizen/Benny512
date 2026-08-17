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

  async function refreshNodes() {
    nodes = await Api.getNodes();
    const sel = document.getElementById('fixtureNodeSelect');
    const prevValue = sel.value;
    sel.innerHTML = '';
    nodes.forEach(n => {
      (n.ports || []).forEach(p => {
        const opt = document.createElement('option');
        opt.value = JSON.stringify({ ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress });
        opt.textContent = `${n.shortName || n.ip} — port ${p.index} (addr ${p.outputAddress})`;
        sel.appendChild(opt);
      });
    });
    if (prevValue) sel.value = prevValue;
    refreshFilterOptions();
  }

  async function refreshFixtures() {
    fixtures = await Api.getFixtures();
    refreshFilterOptions();
    render();
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
        opt.textContent = `Universe ${u}`;
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
      tr.innerHTML = `
        <td data-label="Class">${UI.tag(f.class, classTagVariant(f.class))}</td>
        <td data-label="Manufacturer">${escapeHtml(manufacturerCell(f))}</td>
        <td data-label="Model/Type">${escapeHtml(modelCell(f))}</td>
        <td data-label="UID" class="b5-table__mono">${escapeHtml(f.uid)}</td>
        <td data-label="Address" class="b5-table__mono">${addressLabel(f)}</td>
        <td data-label="Node/Port">${escapeHtml(f.nodeIp)} / ${f.portAddress}</td>
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
    DeviceDetail.init(() => {
      render();
      if (selectedUID) renderTabBody(fixtures.find(x => x.uid === selectedUID) || { uid: selectedUID });
    });

    refreshNodes();
    refreshFixtures();
  }

  async function discover() {
    const sel = document.getElementById('fixtureNodeSelect');
    const status = document.getElementById('discoverStatus');
    if (!sel.value) { status.textContent = 'no node/port selected'; return; }
    const { ip, bindIndex, portAddress } = JSON.parse(sel.value);
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
