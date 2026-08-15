// devices.js — Devices screen (formerly "Fixtures"): every RDM responder is
// first-class here, not just fixtures — gateways, splitters, dimmers,
// wireless radios. Table groups/filters by DeviceClass; detail panel is
// sectioned Info / Parameters / Sensors / Status.
//
// Rules (architecture rev 5 §4 / task brief "UI rules (MANDATORY)"):
//  - oninput mutates state only; full re-render happens on onchange.
//  - re-render preserves the selected row, scroll position and focus.
//  - dark high-contrast, system fonts, generous sizing (inherited from
//    style.css) — no new inline styling assumptions here.
const DevicesScreen = (() => {
  // RDM PARAMETER_DESCRIPTION data_type byte values (report §5.1 / rdm.DataType).
  const DS = {
    BIT_FIELD: 0x01, ASCII: 0x02, UBYTE: 0x03, SBYTE: 0x04,
    UWORD: 0x05, SWORD: 0x06, UDWORD: 0x07, SDWORD: 0x08,
    BOOLEAN: 0x0D, ENUMERATION: 0x12,
  };

  // Sparse PRODUCT_DETAIL id -> label table, the infra-relevant subset from
  // the research report §4.2 (full table not worth shipping — this is only
  // ever shown as a supplementary hint in the Info tab, not used for any
  // classification decision, which the Go side already owns).
  const PRODUCT_DETAIL_NAMES = {
    0x0600: 'Splitter', 0x0601: 'Ethernet Node', 0x0602: 'Merge', 0x0603: 'Datapatch',
    0x0604: 'Wireless Link', 0x0701: 'Protocol Converter', 0x0702: 'Analog Demultiplex',
    0x0703: 'Analog Multiplex', 0x0704: 'Switch Panel', 0x0800: 'Router', 0x0801: 'Fader',
    0x0802: 'Mixer',
  };

  const CLASS_BADGE = {
    'Fixture': 'cls-fixture', 'Gateway/Node': 'cls-gateway', 'Splitter': 'cls-splitter',
    'Dimmer/Power': 'cls-dimmer', 'Wireless': 'cls-wireless', 'Controller': 'cls-controller',
    'Other': 'cls-other', 'Unknown': 'cls-unknown',
  };

  let nodes = [];
  let fixtures = [];
  let selectedUID = null;
  let classFilter = '';
  let activeTab = 'info'; // info | params | sensors | status

  // Per-UID caches, kept for the life of the page (cheap; matches the
  // backend's own "learned cache" philosophy for descriptors).
  let infoCache = {};     // uid -> { deviceInfo, label, mfrLabel, model, swVersion, personality, ident, productDetailHex, proxiedCount }
  let paramsCache = {};   // uid -> { descriptors: [...], values: {pid: {...}}, introspecting: bool, progress: {done,total} }
  let sensorsCache = {};  // uid -> { readings: [...], loading, error }
  let statusCache = {};   // uid -> { filter, messages, loading, error }
  let classifying = {};   // uid -> true while a background device_info fetch is in flight

  let sensorsSubscribedUID = null;

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
  }

  async function refreshFixtures() {
    fixtures = await Api.getFixtures();
    render();
    classifyUnknown();
  }

  // classifyUnknown fires a background classification probe for every
  // device the registry hasn't classified yet (report §1.3: Class stays
  // "Unknown" until DEVICE_INFO/PRODUCT_DETAIL_ID_LIST/
  // PROXIED_DEVICE_COUNT have been fetched at least once). DEVICE_INFO
  // alone is enough for most families, but registry.ClassifyDevice needs
  // PRODUCT_DETAIL_ID_LIST too for the Data family's Splitter/Wireless-Link
  // sub-cases (else it falls through to the generic "Gateway/Node" bucket)
  // and PROXIED_DEVICE_COUNT for wireless-proxy detection when category is
  // left NOT_DECLARED (confirmed against the demo server: the LumenRadio
  // Aurora demo device only classifies as "Wireless" once PID 0x0011 has
  // been probed) — so all three are fetched together here, once per
  // device, so the table's class badges are accurate without requiring the
  // user to open every device's Info tab first.
  function classifyUnknown() {
    const targets = fixtures.filter(f => f.class === 'Unknown' && !classifying[f.uid]);
    if (!targets.length) return;
    targets.forEach(f => { classifying[f.uid] = true; });
    Promise.allSettled(targets.map(f => Promise.allSettled([
      Api.getParam(f.uid, 'device_info').then(r => {
        infoCache[f.uid] = Object.assign({}, infoCache[f.uid], { deviceInfo: r.value });
      }),
      Api.getDeviceParam(f.uid, '0070'), // PRODUCT_DETAIL_ID_LIST — cached server-side by reclassify
      Api.getDeviceParam(f.uid, '0011'), // PROXIED_DEVICE_COUNT — likewise
    ]))).then(async () => {
      targets.forEach(f => { classifying[f.uid] = false; });
      fixtures = await Api.getFixtures();
      render();
    });
  }

  function filteredFixtures() {
    if (!classFilter) return fixtures;
    return fixtures.filter(f => f.class === classFilter);
  }

  // Note: params.DeviceInfo has no JSON struct tags, so encoding/json
  // marshals its exported field names verbatim (PascalCase) rather than
  // camelCase — confirmed against the running server (curl
  // /api/fixture/{uid}/param/device_info), not assumed. Every DeviceInfo
  // field access in this file uses that exact casing.
  function addressLabel(f) {
    const info = infoCache[f.uid] && infoCache[f.uid].deviceInfo;
    if (!info) return classifying[f.uid] ? '…' : '—';
    if (!info.DMXFootprint) return '—';
    const start = info.DMXStartAddress;
    const end = start + info.DMXFootprint - 1;
    return info.DMXFootprint > 1 ? `${start}–${end}` : `${start}`;
  }

  function render() {
    const tbody = document.querySelector('#fixturesTable tbody');
    const scrollTop = tbody.parentElement.scrollTop;
    tbody.innerHTML = '';
    filteredFixtures().forEach(f => {
      const tr = document.createElement('tr');
      if (f.uid === selectedUID) tr.classList.add('selected');
      const badgeCls = CLASS_BADGE[f.class] || 'cls-unknown';
      tr.innerHTML = `
        <td><span class="badge cls-badge ${badgeCls}">${escapeHtml(f.class)}</span>${f.isWirelessProxy ? ' <span class="badge proxy-badge">proxy</span>' : ''}</td>
        <td>${escapeHtml(f.manufacturerName)}</td>
        <td>${escapeHtml(f.uid)}</td>
        <td>${addressLabel(f)}</td>
        <td>${escapeHtml(f.nodeIp)} / ${f.portAddress}</td>
      `;
      tr.addEventListener('click', () => { selectDevice(f.uid); });
      tbody.appendChild(tr);
    });
    tbody.parentElement.scrollTop = scrollTop;
  }

  function selectDevice(uid) {
    if (selectedUID === uid) return;
    unsubscribeSensors();
    selectedUID = uid;
    activeTab = 'info';
    render();
    renderDetail();
  }

  // --- detail panel: tab shell -------------------------------------------

  function renderDetail() {
    const el = document.getElementById('fixtureDetail');
    const f = fixtures.find(x => x.uid === selectedUID);
    if (!f) {
      el.innerHTML = '<p class="empty-hint">Select a device to view its RDM parameters, sensors and status.</p>';
      return;
    }
    const badgeCls = CLASS_BADGE[f.class] || 'cls-unknown';
    el.innerHTML = `
      <h3>${escapeHtml(f.manufacturerName)} — ${escapeHtml(f.uid)}
        <span class="badge cls-badge ${badgeCls}">${escapeHtml(f.class)}</span></h3>
      <div class="panel-toolbar">
        <strong class="hint">RDM capture export (this device):</strong>
        <button id="btnExportDeviceJson">Export JSON</button>
        <button id="btnExportDeviceTxt">Export TXT</button>
      </div>
      <div class="detail-tabs" id="deviceTabs">
        <button class="detail-tab-btn" data-tab="info">Info</button>
        <button class="detail-tab-btn" data-tab="params">Parameters</button>
        <button class="detail-tab-btn" data-tab="sensors">Sensors</button>
        <button class="detail-tab-btn" data-tab="status">Status</button>
      </div>
      <div id="deviceTabBody"></div>
    `;
    document.getElementById('btnExportDeviceJson').addEventListener('click', () => {
      window.open(Api.exportUrl('json', { uid: f.uid }), '_blank');
    });
    document.getElementById('btnExportDeviceTxt').addEventListener('click', () => {
      window.open(Api.exportUrl('txt', { uid: f.uid }), '_blank');
    });
    el.querySelectorAll('.detail-tab-btn').forEach(btn => {
      btn.classList.toggle('active', btn.dataset.tab === activeTab);
      btn.addEventListener('click', () => {
        if (activeTab === btn.dataset.tab) return;
        if (activeTab === 'sensors') unsubscribeSensors();
        activeTab = btn.dataset.tab;
        renderDetail();
      });
    });
    renderTabBody(f);
  }

  function renderTabBody(f) {
    switch (activeTab) {
      case 'info': renderInfoTab(f); break;
      case 'params': renderParamsTab(f); break;
      case 'sensors': renderSensorsTab(f); break;
      case 'status': renderStatusTab(f); break;
    }
  }

  // --- Info tab ------------------------------------------------------------

  async function renderInfoTab(f) {
    const body = document.getElementById('deviceTabBody');
    body.innerHTML = '<p class="hint">Loading…</p>';
    const uid = f.uid;

    const [deviceInfo, mfrLabel, model, swVersion, prodDetail] = await Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'manufacturer_label'),
      Api.getParam(uid, 'device_model_description'),
      Api.getParam(uid, 'software_version_label'),
      Api.getDeviceParam(uid, '0070'), // PRODUCT_DETAIL_ID_LIST — raw-hex fallback decode
    ]);
    if (selectedUID !== uid || activeTab !== 'info') return;

    const di = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    infoCache[uid] = Object.assign({}, infoCache[uid], { deviceInfo: di });

    let proxyInfoHtml = '';
    if (f.isWirelessProxy) {
      let count = '—';
      try {
        const r = await Api.getDeviceParam(uid, '0011'); // PROXIED_DEVICE_COUNT
        if (r.hex && r.hex.length >= 4) count = parseInt(r.hex.slice(0, 4), 16);
      } catch (e) { /* leave as — */ }
      if (selectedUID !== uid || activeTab !== 'info') return;
      proxyInfoHtml = `<div class="field-row"><label>Proxied devices</label><span>${count}</span></div>`;
    }

    let detailsHtml = '<span class="hint">none reported</span>';
    if (prodDetail.status === 'fulfilled' && prodDetail.value.hex) {
      const hex = prodDetail.value.hex;
      const names = [];
      for (let i = 0; i + 4 <= hex.length; i += 4) {
        const code = parseInt(hex.slice(i, i + 4), 16);
        names.push(PRODUCT_DETAIL_NAMES[code] || `0x${code.toString(16).toUpperCase().padStart(4, '0')}`);
      }
      if (names.length) detailsHtml = names.map(n => `<span class="badge detail-badge">${escapeHtml(n)}</span>`).join(' ');
    }

    body.innerHTML = `
      <div class="field-row"><label>Manufacturer</label><span>${escapeHtml(f.manufacturerName)} (0x${f.manufacturerId.toString(16).toUpperCase().padStart(4, '0')})</span></div>
      <div class="field-row"><label>Model</label><span>${model.status === 'fulfilled' ? escapeHtml(model.value.value) : '—'}</span></div>
      <div class="field-row"><label>Manufacturer label</label><span>${mfrLabel.status === 'fulfilled' ? escapeHtml(mfrLabel.value.value) : '—'}</span></div>
      <div class="field-row"><label>Software version</label><span>${swVersion.status === 'fulfilled' ? escapeHtml(swVersion.value.value) : '—'}</span></div>
      <div class="field-row"><label>Node / port</label><span>${escapeHtml(f.nodeIp)} (bind ${f.bindIndex}) / addr ${f.portAddress}</span></div>
      <div class="field-row"><label>DMX footprint</label><span>${di ? di.DMXFootprint : '—'}</span></div>
      <div class="field-row"><label>DMX start address</label><span>${di && di.DMXFootprint ? di.DMXStartAddress : '—'}</span></div>
      <div class="field-row"><label>Personality</label><span>${di ? `${di.CurrentPersonality} of ${di.PersonalityCount}` : '—'}</span></div>
      <div class="field-row"><label>Sub-devices</label><span>${di ? di.SubDeviceCount : '—'}</span></div>
      <div class="field-row"><label>Sensors</label><span>${di ? di.SensorCount : '—'}</span></div>
      <div class="field-row"><label>Product details</label><span>${detailsHtml}</span></div>
      ${proxyInfoHtml}
    `;
  }

  // --- Parameters tab: standard PIDs + generic manufacturer editor --------

  async function renderParamsTab(f) {
    const body = document.getElementById('deviceTabBody');
    body.innerHTML = '<p class="hint">Loading…</p>';
    const uid = f.uid;

    const [deviceInfo, label, personality, ident] = await Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'device_label'),
      Api.getParam(uid, 'dmx_personality'),
      Api.getParam(uid, 'identify_device'),
    ]);
    if (selectedUID !== uid || activeTab !== 'params') return;

    const di = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    const lbl = label.status === 'fulfilled' ? label.value.value : '';
    const pers = personality.status === 'fulfilled' ? personality.value.value : null;
    const identOn = ident.status === 'fulfilled' ? ident.value.value : false;

    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });

    body.innerHTML = `
      <h4>Standard parameters</h4>
      <div class="field-row"><label>Device label</label>
        <span class="apply-field">
          <input id="fxLabel" type="text" value="${escapeHtml(lbl)}" ${di ? '' : 'disabled'}>
          <span id="fxLabelDirty" class="badge dirty-badge" style="display:none;">pending</span>
          <button id="fxLabelApply" class="btn-apply" disabled>Apply</button>
          <button id="fxLabelRevert" class="btn-revert" style="display:none;">Revert</button>
        </span>
      </div>
      <div class="field-row"><label>Start address</label>
        <span class="apply-field">
          <input id="fxStartAddr" type="number" min="1" max="512" value="${di && di.DMXFootprint ? di.DMXStartAddress : ''}" ${di && di.DMXFootprint ? '' : 'disabled'}>
          <span id="fxStartAddrDirty" class="badge dirty-badge" style="display:none;">pending</span>
          <button id="fxStartAddrApply" class="btn-apply" disabled>Apply</button>
          <button id="fxStartAddrRevert" class="btn-revert" style="display:none;">Revert</button>
        </span>
      </div>
      <div class="field-row"><label>Personality</label>
        <span class="apply-field">
          <select id="fxPersonality" ${pers ? '' : 'disabled'}>
            ${pers ? Array.from({ length: pers.Count }, (_, i) => i + 1).map(i =>
              `<option value="${i}" ${i === pers.Current ? 'selected' : ''}>${i}</option>`).join('') : ''}
          </select>
          <span id="fxPersonalityDirty" class="badge dirty-badge" style="display:none;">pending</span>
          <button id="fxPersonalityApply" class="btn-apply" disabled>Apply</button>
          <button id="fxPersonalityRevert" class="btn-revert" style="display:none;">Revert</button>
        </span>
      </div>
      <div class="field-row"><label>Identify</label>
        <input id="fxIdentify" type="checkbox" ${identOn ? 'checked' : ''}>
        <span class="hint">live — flashes/strobes the fixture immediately, matches walking the rig physically identifying it</span>
      </div>
      <div class="field-row"><span id="fxStatus" class="hint"></span></div>

      <h4>Manufacturer parameters</h4>
      <div class="panel-toolbar">
        <button id="btnIntrospect" ${st.introspecting ? 'disabled' : ''}>${st.introspecting ? 'Introspecting…' : 'Introspect'}</button>
        <span class="hint" id="introspectStatus">${introspectStatusText(st)}</span>
      </div>
      <div id="paramRows"></div>
    `;

    // Standard fields: oninput/onchange stages the value only (never
    // commits) — a per-field Apply button sends it, Revert discards it.
    // fxIdentify is the one deliberate exception (see report): it's a
    // momentary physical action (flash the fixture), not a persisted
    // parameter, so staging it behind Apply would defeat its purpose during
    // a bench walk.
    wireApplyField('fxLabel', lbl, async (v) => saveParam(uid, 'device_label', v));
    wireApplyField('fxStartAddr', di && di.DMXFootprint ? di.DMXStartAddress : '', async (v) => saveParam(uid, 'dmx_start_address', parseInt(v, 10)));
    wireApplyField('fxPersonality', pers ? pers.Current : '', async (v) => saveParam(uid, 'dmx_personality', parseInt(v, 10)));

    document.getElementById('fxIdentify').addEventListener('change', async (e) => {
      try {
        await Api.identify(uid, e.target.checked);
        setFxStatus('identify ' + (e.target.checked ? 'on' : 'off'));
      } catch (err) {
        setFxStatus('error: ' + err.message);
      }
    });
    document.getElementById('btnIntrospect').addEventListener('click', () => startIntrospect(uid));

    // Load whatever descriptors are already cached server-side (no wire
    // traffic — GET /api/device/{uid}/params is cached-only), then render.
    try {
      const descs = await Api.getDeviceParams(uid);
      if (selectedUID !== uid || activeTab !== 'params') return;
      st.descriptors = descs;
      await loadParamValues(uid, descs);
      if (selectedUID !== uid || activeTab !== 'params') return;
      renderParamRows(uid);
    } catch (e) {
      document.getElementById('paramRows').innerHTML = `<p class="hint">error: ${escapeHtml(e.message)}</p>`;
    }
  }

  function introspectStatusText(st) {
    if (st.introspecting && st.progress) return `describing ${st.progress.done}/${st.progress.total} (0x${st.progress.pid})`;
    if (st.introspecting) return 'starting…';
    if (st.descriptors.length) return `${st.descriptors.length} parameter(s) known`;
    return 'no manufacturer parameters resolved yet';
  }

  async function loadParamValues(uid, descs) {
    const st = paramsCache[uid];
    const results = await Promise.allSettled(descs.map(d => Api.getDeviceParam(uid, d.pid)));
    results.forEach((r, i) => {
      const pid = descs[i].pid;
      st.values[pid] = r.status === 'fulfilled' ? { ok: true, val: r.value } : { ok: false, err: r.reason.message };
    });
  }

  async function startIntrospect(uid) {
    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });
    st.introspecting = true;
    st.progress = null;
    if (selectedUID === uid && activeTab === 'params') renderParamsTab(fixtures.find(f => f.uid === uid));
    try {
      await Api.introspectDevice(uid);
    } catch (e) {
      st.introspecting = false;
      if (selectedUID === uid && activeTab === 'params') renderParamsTab(fixtures.find(f => f.uid === uid));
    }
    // Completion/progress arrives over WS — see wireLiveUpdates below.
  }

  function renderParamRows(uid) {
    const rowsEl = document.getElementById('paramRows');
    if (!rowsEl) return;
    const st = paramsCache[uid];
    if (!st.descriptors.length) {
      rowsEl.innerHTML = '<p class="hint">Click Introspect to walk SUPPORTED_PARAMETERS + PARAMETER_DESCRIPTION.</p>';
      return;
    }
    rowsEl.innerHTML = '';
    const table = document.createElement('div');
    table.className = 'param-editor';
    st.descriptors.slice().sort((a, b) => a.pid.localeCompare(b.pid)).forEach(desc => {
      table.appendChild(renderParamRow(uid, desc, st.values[desc.pid]));
    });
    rowsEl.appendChild(table);
  }

  function renderParamRow(uid, desc, valState) {
    const row = document.createElement('div');
    row.className = 'param-row';
    const label = desc.label || `PID 0x${desc.pid}`;
    const editable = desc.supportsSet;
    const unitHint = desc.unitSuffix ? ` ${desc.unitSuffix}` : '';
    const rangeHint = (desc.min !== 0 || desc.max !== 0) ? `[${desc.min}, ${desc.max}]${unitHint}` : '';

    const meta = document.createElement('div');
    meta.className = 'param-row-meta';
    meta.innerHTML = `
      <span class="pid-hex">0x${desc.pid}</span>
      <span class="param-label">${escapeHtml(label)}</span>
      <span class="hint">${escapeHtml(desc.dataTypeName)}${rangeHint ? ' · ' + escapeHtml(rangeHint) : ''}</span>
      ${!editable ? '<span class="badge ro-badge">read-only</span>' : ''}
      ${!desc.selfDescribing ? '<span class="badge adv-badge">raw / unverified</span>' : ''}
    `;
    row.appendChild(meta);

    const field = document.createElement('div');
    field.className = 'param-row-field';
    row.appendChild(field);

    if (!valState) {
      field.innerHTML = '<span class="hint">loading…</span>';
      return row;
    }
    if (!valState.ok) {
      field.innerHTML = `<span class="hint">error: ${escapeHtml(valState.err)}</span>`;
      return row;
    }
    const v = valState.val; // paramValueJSON

    // --- pick an input widget per report §1.1 / task brief ---
    // Every widget below stages its edit locally and requires the row's
    // Apply button (added by wireRowApply) to actually send it — task rule:
    // "nothing commits on change/blur; each edit stages a pending value and
    // an Apply button commits it."
    if (desc.dataType === DS.BOOLEAN || (desc.dataType === DS.BIT_FIELD && desc.pdlSize === 1)) {
      const checked = desc.dataType === DS.BOOLEAN ? v.int !== 0 : (v.hex && parseInt(v.hex.slice(0, 2), 16) !== 0);
      const cb = document.createElement('input');
      cb.type = 'checkbox'; cb.checked = checked; cb.disabled = !editable;
      field.appendChild(cb);
      if (editable) {
        wireRowApply(field, cb, true, checked, (checkedNow) => {
          const value = desc.dataType === DS.BOOLEAN ? (checkedNow ? 1 : 0) : (checkedNow ? '01' : '00');
          return saveDeviceParam(uid, desc, value, row);
        });
      }
    } else if (v.kind === 'string') {
      const input = document.createElement('input');
      input.type = 'text'; input.value = v.str || ''; input.maxLength = 32; input.disabled = !editable;
      field.appendChild(input);
      if (editable) {
        wireRowApply(field, input, false, v.str || '', (val) => saveDeviceParam(uid, desc, val, row));
      }
    } else if (v.kind === 'int') {
      const { wrap, input } = numericStepper(desc, v.int, editable);
      field.appendChild(wrap);
      if (editable) {
        wireRowApply(field, input, false, v.int, (val) => {
          const n = Number(val);
          if (!Number.isFinite(n) || (desc.min !== 0 || desc.max !== 0) && (n < desc.min || n > desc.max)) {
            throw new Error(`must be ${desc.min}–${desc.max}`);
          }
          return saveDeviceParam(uid, desc, n, row);
        });
      }
    } else if (desc.dataType === DS.ENUMERATION || looksBoundedRaw(desc)) {
      // report §1.1 gap 2: no per-value enum labels are available from
      // PARAMETER_DESCRIPTION alone, so an enumerated/unknown-shaped field
      // renders as a bounded numeric stepper, not a labeled dropdown — and
      // since the backend still treats this DataType as raw bytes (not one
      // of the typed numeric kinds), we encode/decode hex on this side.
      const width = desc.pdlSize || byteWidthFor(desc.max) || 1;
      const current = v.hex ? parseInt(v.hex, 16) || 0 : 0;
      const { wrap, input } = numericStepper(desc, current, editable);
      field.appendChild(wrap);
      if (editable) {
        wireRowApply(field, input, false, current, (val) => saveDeviceParam(uid, desc, hexEncode(Number(val), width), row));
      }
    } else {
      // Universal fallback (report §1.1 item 4): raw hex in/out, clearly
      // marked advanced/unverified via the badge above.
      const input = document.createElement('input');
      input.type = 'text'; input.className = 'hex-input'; input.placeholder = 'hex bytes, e.g. DEAD';
      input.value = v.hex || ''; input.disabled = !editable;
      field.appendChild(input);
      if (editable) {
        wireRowApply(field, input, false, v.hex || '', (val) => saveDeviceParam(uid, desc, val.replace(/\s+/g, ''), row));
      }
    }
    return row;
  }

  // wireRowApply appends Apply/Revert/dirty-badge controls to field for one
  // manufacturer-PID row's control (checkbox or text/number input), staging
  // edits until Apply is clicked — see renderParamRow's call sites. onApply
  // may throw (e.g. numericStepper's range check) to reject the apply
  // without sending anything; saveDeviceParam itself never throws (it
  // reports errors via setFxStatus and returns), and on success it
  // re-renders the whole row from the server's confirmed value, which is
  // what re-baselines the dirty state — this helper doesn't need to track a
  // post-apply baseline itself.
  function wireRowApply(field, inputEl, isCheckbox, baselineValue, onApply) {
    const applyBtn = document.createElement('button');
    applyBtn.type = 'button'; applyBtn.className = 'btn-apply'; applyBtn.textContent = 'Apply'; applyBtn.disabled = true;
    const revertBtn = document.createElement('button');
    revertBtn.type = 'button'; revertBtn.className = 'btn-revert'; revertBtn.textContent = 'Revert'; revertBtn.style.display = 'none';
    const dirtyBadge = document.createElement('span');
    dirtyBadge.className = 'badge dirty-badge'; dirtyBadge.textContent = 'pending'; dirtyBadge.style.display = 'none';

    const baseline = isCheckbox ? !!baselineValue : String(baselineValue);
    const current = () => (isCheckbox ? inputEl.checked : inputEl.value);
    const refresh = () => {
      const dirty = current() !== baseline;
      applyBtn.disabled = !dirty;
      revertBtn.style.display = dirty ? '' : 'none';
      dirtyBadge.style.display = dirty ? '' : 'none';
    };
    inputEl.addEventListener('input', refresh);
    inputEl.addEventListener('change', refresh);

    applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      try {
        await onApply(current());
      } catch (e) {
        setFxStatus('error: ' + e.message);
      }
      // On success saveDeviceParam replaces this whole row via
      // renderParamRows(); on failure re-enable so the user can retry.
      applyBtn.disabled = false;
    });
    revertBtn.addEventListener('click', () => {
      if (isCheckbox) inputEl.checked = baseline; else inputEl.value = baseline;
      refresh();
    });

    field.appendChild(dirtyBadge);
    field.appendChild(applyBtn);
    field.appendChild(revertBtn);
  }

  // looksBoundedRaw flags a raw-kind field (bit field, group, UID, URL, MAC,
  // IPv4/6, unrecognized) that nonetheless declares a small numeric
  // range — the task brief's "bounded stepper for enumerated/unknown"
  // instruction, kept narrow (declared max within 2 bytes' worth) so a UID
  // or IPv4-shaped field doesn't get misrendered as a 4-billion-wide slider.
  function looksBoundedRaw(desc) {
    return desc.selfDescribing && desc.max > desc.min && desc.max <= 0xFFFF;
  }

  function byteWidthFor(max) {
    if (max <= 0xFF) return 1;
    if (max <= 0xFFFF) return 2;
    return 4;
  }

  function hexEncode(n, width) {
    return (n >>> 0).toString(16).padStart(width * 2, '0');
  }

  // numericStepper builds the input widget only; it no longer saves
  // anything itself — the caller wires Apply/Revert via wireRowApply, which
  // reads the input's live value when Apply is clicked. Live range-error
  // text still updates on every keystroke (pure local feedback, not a
  // save/re-render, so it doesn't run afoul of the oninput-mutates-only
  // rule).
  function numericStepper(desc, value, editable) {
    const wrap = document.createElement('span');
    const input = document.createElement('input');
    input.type = 'number'; input.value = value; input.disabled = !editable;
    const bounded = desc.min !== 0 || desc.max !== 0;
    if (bounded) { input.min = desc.min; input.max = desc.max; }
    wrap.appendChild(input);
    if (desc.unitSuffix) {
      const suf = document.createElement('span');
      suf.className = 'hint'; suf.textContent = ' ' + desc.unitSuffix;
      wrap.appendChild(suf);
    }
    const errEl = document.createElement('span');
    errEl.className = 'hint field-error';
    wrap.appendChild(errEl);

    input.addEventListener('input', () => {
      const n = Number(input.value);
      if (bounded && (n < desc.min || n > desc.max)) {
        errEl.textContent = `must be ${desc.min}–${desc.max}`;
      } else {
        errEl.textContent = '';
      }
    });
    return { wrap, input };
  }

  async function saveDeviceParam(uid, desc, value, rowEl) {
    try {
      await Api.setDeviceParam(uid, desc.pid, value);
      const r = await Api.getDeviceParam(uid, desc.pid);
      const st = paramsCache[uid];
      if (st) st.values[desc.pid] = { ok: true, val: r };
      setFxStatus('saved 0x' + desc.pid);
      if (selectedUID === uid && activeTab === 'params') renderParamRows(uid);
    } catch (e) {
      setFxStatus('error saving 0x' + desc.pid + ': ' + e.message);
    }
  }

  // wireApplyField wires the Apply-to-confirm pattern for one standard
  // field: baseId + baseId+'Apply'/'Revert'/'Dirty' must already exist in
  // the DOM. The input/select's own oninput/onchange event only toggles the
  // Apply/Revert/dirty-badge visibility (a tiny direct DOM update, not a
  // re-render, so focus/cursor position is never disturbed) — the value is
  // only ever sent to the server when Apply is clicked, reading the field's
  // live value at that moment.
  function wireApplyField(baseId, originalValue, onApply) {
    const input = document.getElementById(baseId);
    const applyBtn = document.getElementById(baseId + 'Apply');
    const revertBtn = document.getElementById(baseId + 'Revert');
    const dirtyBadge = document.getElementById(baseId + 'Dirty');
    if (!input || !applyBtn) return;
    let original = String(originalValue);

    const refreshDirty = () => {
      const dirty = input.value !== original;
      applyBtn.disabled = !dirty;
      if (revertBtn) revertBtn.style.display = dirty ? '' : 'none';
      if (dirtyBadge) dirtyBadge.style.display = dirty ? '' : 'none';
    };
    input.addEventListener('input', refreshDirty);
    input.addEventListener('change', refreshDirty);

    applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      try {
        await onApply(input.value);
        setFxStatus('applied ' + baseId.replace('fx', '').toLowerCase());
      } catch (e) {
        setFxStatus('error: ' + e.message);
        applyBtn.disabled = false;
        return;
      }
      // Re-baseline: the just-applied value becomes the new "original", so
      // the field goes back to clean without needing a full tab re-render.
      original = input.value;
      refreshDirty();
    });
    if (revertBtn) {
      revertBtn.addEventListener('click', () => {
        input.value = original;
        refreshDirty();
      });
    }
  }

  async function saveParam(uid, pid, value) {
    try {
      await Api.setParam(uid, pid, value);
      setFxStatus('saved');
    } catch (e) {
      setFxStatus('error: ' + e.message);
    }
  }

  function setFxStatus(msg) {
    const el = document.getElementById('fxStatus');
    if (el) el.textContent = msg;
  }

  // --- Sensors tab: gauges + live WS updates -------------------------------

  async function renderSensorsTab(f) {
    const body = document.getElementById('deviceTabBody');
    body.innerHTML = '<p class="hint">Loading sensors…</p>';
    const uid = f.uid;
    try {
      const readings = await Api.getDeviceSensors(uid);
      if (selectedUID !== uid || activeTab !== 'sensors') return;
      sensorsCache[uid] = { readings, loading: false, error: null };
      renderSensorGauges(uid);
      subscribeSensors(uid);
    } catch (e) {
      if (selectedUID !== uid || activeTab !== 'sensors') return;
      body.innerHTML = `<p class="hint">error: ${escapeHtml(e.message)}</p>`;
    }
  }

  function renderSensorGauges(uid) {
    const body = document.getElementById('deviceTabBody');
    if (!body) return;
    const st = sensorsCache[uid];
    if (!st || !st.readings.length) {
      body.innerHTML = '<p class="empty-hint">This device reports no sensors.</p>';
      return;
    }
    body.innerHTML = '';
    st.readings.forEach(r => body.appendChild(renderGauge(uid, r)));
  }

  function renderGauge(uid, r) {
    const wrap = document.createElement('div');
    wrap.className = 'gauge-card' + (r.hasRange && !r.inNormalBand ? ' out-of-band' : '');

    const header = document.createElement('div');
    header.className = 'gauge-header';
    header.innerHTML = `
      <span class="gauge-title">${escapeHtml(r.description || r.typeName)}</span>
      <span class="gauge-value">${escapeHtml(r.presentFormatted)}</span>
      ${(r.hasRange && !r.inNormalBand) ? '<span class="badge warn-badge">⚠ outside normal range</span>' : ''}
    `;
    wrap.appendChild(header);

    if (r.hasRange) {
      wrap.appendChild(buildGaugeSVG(r));
    } else {
      const plain = document.createElement('div');
      plain.className = 'hint';
      plain.textContent = 'range undeclared by device — showing raw value only.';
      wrap.appendChild(plain);
    }

    const meta = document.createElement('div');
    meta.className = 'gauge-meta hint';
    const parts = [];
    if (r.recordsRange) parts.push(`lowest ${formatSensorRaw(r, r.lowest)} · highest ${formatSensorRaw(r, r.highest)}`);
    if (r.recordsValue) parts.push(`recorded ${formatSensorRaw(r, r.recorded)}`);
    meta.textContent = parts.join(' · ');
    wrap.appendChild(meta);

    const actions = document.createElement('div');
    actions.className = 'gauge-actions';
    if (r.recordsValue) {
      const btn = document.createElement('button');
      btn.textContent = 'Record';
      btn.addEventListener('click', async () => {
        try { await Api.recordDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
        catch (e) { setSensorsStatus(uid, 'error: ' + e.message); }
      });
      actions.appendChild(btn);
    }
    if (r.recordsValue || r.recordsRange) {
      const btn = document.createElement('button');
      btn.textContent = 'Reset';
      btn.addEventListener('click', async () => {
        try { await Api.resetDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
        catch (e) { setSensorsStatus(uid, 'error: ' + e.message); }
      });
      actions.appendChild(btn);
    }
    wrap.appendChild(actions);

    return wrap;
  }

  function setSensorsStatus(uid, msg) {
    if (selectedUID !== uid || activeTab !== 'sensors') return;
    const body = document.getElementById('deviceTabBody');
    if (body) {
      let el = body.querySelector('.sensors-status');
      if (!el) {
        el = document.createElement('div');
        el.className = 'sensors-status hint';
        body.prepend(el);
      }
      el.textContent = msg;
    }
  }

  async function refreshSensorsNow(uid) {
    const readings = await Api.getDeviceSensors(uid);
    sensorsCache[uid] = { readings, loading: false, error: null };
    if (selectedUID === uid && activeTab === 'sensors') renderSensorGauges(uid);
  }

  // formatSensorRaw applies the sensor's declared unit+prefix to a raw
  // INT16 field (lowest/highest/recorded — present already arrives
  // pre-formatted as presentFormatted from the server).
  function formatSensorRaw(r, raw) {
    return formatValue(raw, r.unit, r.prefix);
  }

  function buildGaugeSVG(r) {
    const div = document.createElement('div');
    // sensorReadingJSON's rangeMin/rangeMax/normalMin/normalMax carry
    // `omitempty` (confirmed against the running server: a genuine 0 value
    // — e.g. the Chroma-Q demo's PSU-voltage sensor's rangeMin — is absent
    // from the JSON entirely, not sent as 0) so a missing key must default
    // to 0, not be treated as "no data" (hasRange/hasNormalBand already
    // carry that signal explicitly).
    const min = r.rangeMin || 0, max = r.rangeMax || 0;
    const span = Math.max(1, max - min);
    const pct = (v) => Math.min(100, Math.max(0, ((v - min) / span) * 100));

    const trackX = 4, trackW = 92; // percent-space track inset, mapped via viewBox below
    const toX = (v) => trackX + (pct(v) / 100) * trackW;

    let normalBand = '';
    if (r.hasNormalBand) {
      const x1 = toX(r.normalMin || 0), x2 = toX(r.normalMax || 0);
      normalBand = `<rect x="${x1}" y="18" width="${Math.max(0, x2 - x1)}" height="14" class="gauge-normal-band" />`;
    }

    let ticks = '';
    if (r.recordsRange) {
      ticks += `<line x1="${toX(r.lowest)}" x2="${toX(r.lowest)}" y1="14" y2="36" class="gauge-tick gauge-tick-low" />`;
      ticks += `<line x1="${toX(r.highest)}" x2="${toX(r.highest)}" y1="14" y2="36" class="gauge-tick gauge-tick-high" />`;
    }
    if (r.recordsValue) {
      const rx = toX(r.recorded);
      ticks += `<rect x="${rx - 1.2}" y="15" width="2.4" height="20" transform="rotate(45 ${rx} 25)" class="gauge-tick-recorded" />`;
    }

    const px = toX(r.present);
    const outOfBand = r.hasNormalBand ? !r.inNormalBand : false;
    const patternId = `hatch-${r.number}`;
    // Out-of-band marker is distinguished by shape+pattern, not color alone
    // (accessibility requirement): a wider hatch-filled block instead of the
    // normal thin triangle, plus the "⚠ outside normal range" text badge in
    // the header above.
    const marker = outOfBand
      ? `<rect x="${px - 3}" y="12" width="6" height="26" class="gauge-marker gauge-marker-warn" fill="url(#${patternId})" />`
      : `<polygon points="${px},12 ${px - 4},22 ${px + 4},22" class="gauge-marker" /><line x1="${px}" y1="18" x2="${px}" y2="36" class="gauge-marker-line" />`;

    div.innerHTML = `
      <svg viewBox="0 0 100 40" class="gauge-svg" role="img" aria-label="${escapeHtml(r.description)} gauge, present value ${escapeHtml(r.presentFormatted)}">
        <defs>
          <pattern id="${patternId}" width="3" height="3" patternTransform="rotate(45)" patternUnits="userSpaceOnUse">
            <rect width="3" height="3" class="gauge-hatch-bg" />
            <line x1="0" y1="0" x2="0" y2="3" class="gauge-hatch-line" />
          </pattern>
        </defs>
        <rect x="${trackX}" y="18" width="${trackW}" height="14" class="gauge-track" />
        ${normalBand}
        ${ticks}
        ${marker}
      </svg>
      <div class="gauge-range-labels hint">
        <span>${min}</span><span>${max}${r.unitSuffix ? ' ' + r.unitSuffix : ''}</span>
      </div>
    `;
    return div;
  }

  function subscribeSensors(uid) {
    if (sensorsSubscribedUID === uid) return;
    unsubscribeSensors();
    sensorsSubscribedUID = uid;
    Live.send({ type: 'subscribe_sensors', uid });
  }

  function unsubscribeSensors() {
    if (!sensorsSubscribedUID) return;
    Live.send({ type: 'unsubscribe_sensors', uid: sensorsSubscribedUID });
    sensorsSubscribedUID = null;
  }

  // --- Status tab ------------------------------------------------------------

  async function renderStatusTab(f) {
    const body = document.getElementById('deviceTabBody');
    const uid = f.uid;
    const st = statusCache[uid] || (statusCache[uid] = { filter: 'advisory', messages: [], loading: true, error: null });
    body.innerHTML = `
      <div class="panel-toolbar">
        <label>Severity: <select id="statusFilter">
          <option value="advisory" ${st.filter === 'advisory' ? 'selected' : ''}>Advisory</option>
          <option value="warning" ${st.filter === 'warning' ? 'selected' : ''}>Warning</option>
          <option value="error" ${st.filter === 'error' ? 'selected' : ''}>Error</option>
        </select></label>
        <button id="btnRefreshStatus">Refresh</button>
        <span class="hint" id="statusHint">${st.loading ? 'loading…' : ''}</span>
      </div>
      <table class="data-table" id="statusTable">
        <thead><tr><th>Sub-device</th><th>Type</th><th>Message ID</th><th>Value 1</th><th>Value 2</th></tr></thead>
        <tbody></tbody>
      </table>
    `;
    document.getElementById('statusFilter').addEventListener('change', (e) => {
      st.filter = e.target.value;
      loadStatus(uid);
    });
    document.getElementById('btnRefreshStatus').addEventListener('click', () => loadStatus(uid));
    loadStatus(uid);
  }

  async function loadStatus(uid) {
    const st = statusCache[uid];
    st.loading = true;
    const hint = document.getElementById('statusHint');
    if (hint) hint.textContent = 'loading…';
    try {
      const msgs = await Api.getDeviceStatus(uid, st.filter);
      st.messages = msgs; st.loading = false; st.error = null;
    } catch (e) {
      st.loading = false; st.error = e.message;
    }
    if (selectedUID !== uid || activeTab !== 'status') return;
    const hint2 = document.getElementById('statusHint');
    if (hint2) hint2.textContent = st.error ? ('error: ' + st.error) : `${st.messages.length} message(s)`;
    const tbody = document.querySelector('#statusTable tbody');
    if (!tbody) return;
    tbody.innerHTML = st.messages.map(m => `
      <tr>
        <td>${m.subDevice}</td>
        <td>${escapeHtml(m.typeName)}</td>
        <td>0x${m.messageId.toString(16).toUpperCase().padStart(4, '0')}</td>
        <td>${m.value1}</td>
        <td>${m.value2}</td>
      </tr>`).join('') || '<tr><td colspan="5" class="hint">no messages at this severity</td></tr>';
  }

  // --- shared unit/prefix formatting (mirrors internal/rdm/format.go) -----

  const PREFIX_MULT = {
    0x00: 1, 0x01: 1e-1, 0x02: 1e-2, 0x03: 1e-3, 0x04: 1e-6, 0x05: 1e-9,
    0x06: 1e-12, 0x07: 1e-15, 0x08: 1e-18, 0x09: 1e-21, 0x0A: 1e-24,
    0x11: 1e1, 0x12: 1e2, 0x13: 1e3, 0x14: 1e6, 0x15: 1e9, 0x16: 1e12,
    0x17: 1e15, 0x18: 1e18, 0x19: 1e21, 0x1A: 1e24,
  };
  const UNIT_SUFFIX = {
    0x00: '', 0x01: '°C', 0x02: 'V', 0x03: 'V', 0x04: 'V', 0x05: 'A', 0x06: 'A', 0x07: 'A',
    0x08: 'Hz', 0x09: 'Ω', 0x0A: 'W', 0x0B: 'kg', 0x0C: 'm', 0x0D: 'm²', 0x0E: 'm³',
    0x0F: 'kg/m³', 0x10: 'm/s', 0x11: 'm/s²', 0x12: 'N', 0x13: 'J', 0x14: 'Pa', 0x15: 's',
    0x16: '°', 0x17: 'sr', 0x18: 'cd', 0x19: 'lm', 0x1A: 'lx', 0x1B: 'IRE', 0x1C: 'B',
    0x1D: 'dB', 0x1E: 'dBV', 0x1F: 'dBW', 0x20: 'dBm', 0x21: '%', 0x22: 'mol/m³', 0x23: 'RPM', 0x24: 'B/s',
  };

  function formatValue(raw, unit, prefix) {
    const mult = PREFIX_MULT[prefix] || 1;
    const scaled = raw * mult;
    const suffix = UNIT_SUFFIX[unit] || '';
    const numStr = Math.abs(scaled) < 1000 && !Number.isInteger(scaled) ? scaled.toFixed(2) : String(Math.round(scaled));
    return suffix ? `${numStr} ${suffix}` : numStr;
  }

  // --- live WS updates -------------------------------------------------------

  function wireLiveUpdates() {
    Live.on('node', () => refreshNodes());

    Live.on('introspect_progress', (msg) => {
      const st = paramsCache[msg.kind];
      if (!st) return;
      st.progress = msg.introspect;
      if (selectedUID === msg.kind && activeTab === 'params') {
        const el = document.getElementById('introspectStatus');
        if (el) el.textContent = introspectStatusText(st);
      }
    });

    Live.on('introspect_complete', async (msg) => {
      const st = paramsCache[msg.kind] || (paramsCache[msg.kind] = { descriptors: [], values: {}, introspecting: false, progress: null });
      st.introspecting = false;
      if (msg.err) {
        if (selectedUID === msg.kind && activeTab === 'params') {
          const el = document.getElementById('introspectStatus');
          if (el) el.textContent = 'error: ' + msg.err;
        }
        return;
      }
      st.descriptors = msg.descriptors || [];
      await loadParamValues(msg.kind, st.descriptors);
      if (selectedUID === msg.kind && activeTab === 'params') {
        renderParamsTab(fixtures.find(x => x.uid === msg.kind));
      }
    });

    Live.on('sensor_values', (msg) => {
      if (selectedUID !== msg.kind || activeTab !== 'sensors') return;
      sensorsCache[msg.kind] = { readings: msg.sensors, loading: false, error: null };
      renderSensorGauges(msg.kind);
    });
  }

  function init() {
    document.getElementById('btnDiscover').addEventListener('click', discover);
    const filterSel = document.getElementById('deviceClassFilter');
    if (filterSel) {
      filterSel.addEventListener('change', (e) => { classFilter = e.target.value; render(); });
    }
    wireLiveUpdates();
    refreshNodes();
    refreshFixtures();
  }

  async function discover() {
    const sel = document.getElementById('fixtureNodeSelect');
    const status = document.getElementById('discoverStatus');
    if (!sel.value) { status.textContent = 'no node/port selected'; return; }
    const { ip, bindIndex, portAddress } = JSON.parse(sel.value);
    status.textContent = 'discovering…';
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
