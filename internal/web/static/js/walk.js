// walk.js — Rig Walk mode: a phone-optimized, one-device-at-a-time
// walkthrough. The owner holds his phone and walks the physical rig; the
// server auto-drives IDENTIFY_DEVICE (ON for whatever's on screen, OFF for
// whatever was) so exactly one fixture is ever flashing, plus a big-button
// verification checklist (Confirmed/Problem/note) and export.
//
// Task ask ("in rig walk mode, we need access to the full device UI"): the
// device card below the walk chrome is now the SAME shared DeviceDetail
// component (see devicedetail.js) the Devices tab uses, rendered in its
// "walk" presentation — large collapsible accordions instead of small tabs,
// sensible default (only Parameters, the address/personality section,
// starts expanded) so the card stays scannable at arm's length, big touch
// targets, generous type. Everything on the Devices tab is reachable here:
// sensor gauges, DMX address/personality, label, identify, standard PIDs
// incl. the new E1.37-1 dimmer fields, manufacturer/generic-ESTA PIDs with
// Introspect + raw-hex fallback, and status messages.
//
// Rules (same as every other screen, architecture rev 5 §4): oninput
// mutates local state only (noteDraft/addressDraft below), full re-render
// happens on onchange/explicit action. This screen leans on that harder
// than most — since almost every action is a server round-trip that
// replaces the whole session object, staged text fields (the problem note)
// are deliberately NOT part of the server session; they live in this
// module's own closure state until the user explicitly submits/applies
// them, so a slow wireless-proxied identify round-trip elsewhere on screen
// never clobbers what the tech is mid-typing.
//
// Probe fan-out: device selection (Next/Previous/goto) flows through
// DeviceDetail.select(uid, sections), which debounces ~300ms and cancels
// stale results — see devicedetail.js's file doc comment. A tech tapping
// Next repeatedly never queues probes for devices they've already moved
// past.
const WalkScreen = (() => {
  let active = false;
  let session = null;   // walk.Session JSON, or null
  let summary = null;   // walk.Summary JSON, or null
  let statusMsg = '';

  // Setup-screen (pre-walk) state.
  let nodesCache = [];
  let scopeKind = sessionStorage.getItem('benny512.walk.scopeKind') || 'all';
  let orderMode = sessionStorage.getItem('benny512.walk.order') || 'address';
  let fixturesOnly = sessionStorage.getItem('benny512.walk.fixturesOnly') !== 'false';
  let starting = false;

  // Active-walk staged state (never sent until an explicit action).
  let showProblemNote = false;
  let noteDraft = '';

  // Accordion expand/collapse state — task ask: "sensible default (start
  // collapsed except the most-used section — address/personality)". Kept
  // across Next/Previous within one walk session (a tech who opens Sensors
  // once probably wants it open for the next fixture too), reset to the
  // default whenever a NEW device is actually selected is deliberately NOT
  // done — persisting the tech's own preference beats re-collapsing on
  // every step.
  let expandedSections = { info: false, params: true, sensors: false, status: false };
  let currentDeviceUID = null;

  // --- lifecycle ------------------------------------------------------------

  function init() {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden' && active) {
        Api.walkIdentifyOffBeacon();
      }
    });
    window.addEventListener('pagehide', () => {
      if (active) Api.walkIdentifyOffBeacon();
    });
    DeviceDetail.init(() => { if (active) render(); });
  }

  function onEnterScreen() {
    refresh();
  }

  function onLeaveScreen() {
    if (active) {
      Api.walkIdentifyOffBeacon();
    }
    DeviceDetail.deselect();
    currentDeviceUID = null;
  }

  async function refresh() {
    try {
      const resp = await Api.getWalkSession();
      active = resp.active;
      session = resp.session || null;
      summary = resp.summary || null;
    } catch (e) {
      statusMsg = 'error: ' + e.message;
    }
    if (!active) {
      if (!nodesCache.length) {
        try { nodesCache = await Api.getNodes(); } catch (e) { /* setup screen still usable with "all" scope */ }
      }
    }
    syncDeviceSelection();
    render();
  }

  // syncDeviceSelection tells DeviceDetail which device the walk card is
  // currently showing (task ask: debounce/cancel/subscribe-follows-
  // selection). Called on every refresh (goto/status/address/auto-advance
  // all end in refresh()) — a no-op when the device hasn't actually changed
  // (DeviceDetail.select itself short-circuits same-uid calls to "apply
  // sections immediately", see devicedetail.js).
  function syncDeviceSelection() {
    const dev = currentDevice();
    const uid = dev ? dev.uid : null;
    if (uid !== currentDeviceUID) currentDeviceUID = uid;
    if (uid) DeviceDetail.select(uid, expandedSections);
    else DeviceDetail.deselect();
  }

  function currentDevice() {
    if (!active || !session) return null;
    const devices = session.devices || [];
    const idx = session.current;
    return idx >= 0 && idx < devices.length ? devices[idx] : null;
  }

  // toDetailFixture adapts walk.Device's JSON shape into the {uid,
  // manufacturerId, manufacturerName, nodeIp, bindIndex, portAddress}
  // shape DeviceDetail's Info section expects (mirrors fixtureJSON's
  // shape from the Devices tab, minus the fields Info doesn't use).
  // manufacturerId is derived from the UID's own manufacturer-ID prefix
  // (first 4 hex digits) since walk.Device doesn't carry it separately —
  // Info section's live MANUFACTURER_LABEL GET fetch supersedes this
  // fallback the moment it resolves either way.
  function toDetailFixture(dev) {
    const mfrHex = (dev.uid || '0000:00000000').split(':')[0];
    return {
      uid: dev.uid,
      manufacturerId: parseInt(mfrHex, 16) || 0,
      manufacturerName: dev.manufacturer || 'Unknown',
      nodeIp: dev.nodeIp, bindIndex: dev.bindIndex, portAddress: dev.portAddress,
    };
  }

  // --- render dispatch --------------------------------------------------------

  function render() {
    const el = document.getElementById('walkRoot');
    if (!el) return;
    if (active && session) {
      renderActive(el);
    } else {
      renderSetup(el);
    }
  }

  // --- setup screen -----------------------------------------------------------

  function renderSetup(el) {
    el.innerHTML = `
      <div class="walk-setup">
        <h2>Rig Walk</h2>
        <p class="hint">Pick what to walk. Landing on a device flashes it (Identify ON); moving on turns it off and flashes the next one — exactly one fixture at a time. The full device panel — sensors, address, personality, standard and manufacturer parameters, status — is available below each device as expandable sections.</p>

        <div class="walk-field">
          <label>Scope</label>
          <select id="walkScopeKind">
            <option value="all">All devices</option>
            <option value="node">One node</option>
            <option value="port">One port</option>
            <option value="universe">One universe (Port-Address)</option>
            <option value="class">One device class</option>
          </select>
        </div>
        <div id="walkScopeValueWrap"></div>

        <div class="walk-field">
          <label>Order</label>
          <select id="walkOrder">
            <option value="address">Universe + DMX address (low&rarr;high)</option>
            <option value="address_desc">Universe + DMX address (high&rarr;low)</option>
            <option value="model">Model / fixture type</option>
            <option value="uid">UID</option>
            <option value="discovery">Discovery order</option>
          </select>
        </div>

        <div class="walk-field walk-checkbox">
          <label><input type="checkbox" id="walkFixturesOnly" ${fixturesOnly ? 'checked' : ''}> Fixtures only (uncheck for all RDM devices)</label>
        </div>

        <button id="walkStartBtn" class="walk-btn-primary" ${starting ? 'disabled' : ''}>${starting ? 'Starting…' : 'Start Walk'}</button>
        <div class="hint" id="walkSetupStatus">${escapeHtml(statusMsg)}</div>
      </div>
    `;
    renderScopeValue();
    document.getElementById('walkScopeKind').value = scopeKind;
    document.getElementById('walkOrder').value = orderMode;

    document.getElementById('walkScopeKind').addEventListener('change', (e) => {
      scopeKind = e.target.value;
      sessionStorage.setItem('benny512.walk.scopeKind', scopeKind);
      renderScopeValue();
    });
    document.getElementById('walkOrder').addEventListener('change', (e) => {
      orderMode = e.target.value;
      sessionStorage.setItem('benny512.walk.order', orderMode);
    });
    document.getElementById('walkFixturesOnly').addEventListener('change', (e) => {
      fixturesOnly = e.target.checked;
      sessionStorage.setItem('benny512.walk.fixturesOnly', String(fixturesOnly));
    });
    document.getElementById('walkStartBtn').addEventListener('click', startWalk);
  }

  function renderScopeValue() {
    const wrap = document.getElementById('walkScopeValueWrap');
    if (!wrap) return;
    switch (scopeKind) {
      case 'node': {
        wrap.innerHTML = `<div class="walk-field"><label>Node</label><select id="walkScopeNode">
          ${nodesCache.map(n => `<option value="${escapeHtml(n.ip)}">${escapeHtml(n.shortName || n.longName || n.ip)} (${escapeHtml(n.ip)})</option>`).join('')}
        </select></div>`;
        break;
      }
      case 'port': {
        const opts = [];
        nodesCache.forEach(n => (n.ports || []).forEach(p => {
          opts.push(`<option value='${JSON.stringify({ ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress })}'>${escapeHtml(n.shortName || n.ip)} — port ${p.index} (addr ${p.outputAddress})</option>`);
        }));
        wrap.innerHTML = `<div class="walk-field"><label>Port</label><select id="walkScopePort">${opts.join('')}</select></div>`;
        break;
      }
      case 'universe':
        wrap.innerHTML = `<div class="walk-field"><label>Universe (Port-Address)</label><input type="number" id="walkScopeUniverse" min="0" max="32767" value="0"></div>`;
        break;
      case 'class':
        wrap.innerHTML = `<div class="walk-field"><label>Class</label><select id="walkScopeClass">
          <option value="Fixture">Fixture</option>
          <option value="Gateway/Node">Gateway/Node</option>
          <option value="Splitter">Splitter</option>
          <option value="Dimmer/Power">Dimmer/Power</option>
          <option value="Wireless">Wireless</option>
          <option value="Controller">Controller</option>
          <option value="Other">Other</option>
          <option value="Unknown">Unknown</option>
        </select></div>`;
        break;
      default:
        wrap.innerHTML = '';
    }
  }

  async function startWalk() {
    let scopeValue = '';
    switch (scopeKind) {
      case 'node': scopeValue = (document.getElementById('walkScopeNode') || {}).value || ''; break;
      case 'port': scopeValue = (document.getElementById('walkScopePort') || {}).value || ''; break;
      case 'universe': scopeValue = (document.getElementById('walkScopeUniverse') || {}).value || '0'; break;
      case 'class': scopeValue = (document.getElementById('walkScopeClass') || {}).value || ''; break;
    }
    starting = true;
    statusMsg = '';
    render();
    try {
      await Api.startWalk({ order: orderMode, scopeKind, scopeValue, fixturesOnly });
      starting = false;
      await refresh();
    } catch (e) {
      starting = false;
      statusMsg = 'error: ' + e.message;
      render();
    }
  }

  // --- active walk screen -------------------------------------------------

  function renderActive(el) {
    const devices = session.devices || [];
    const idx = session.current;
    const dev = idx >= 0 && idx < devices.length ? devices[idx] : null;
    const sum = summary || { total: devices.length, confirmed: 0, problems: 0, remaining: devices.length };

    el.innerHTML = `
      <div class="walk-topbar">
        <button id="walkAllOffBtn" class="walk-btn-danger">Identify off / all off</button>
        <button id="walkEndBtn" class="walk-btn-ghost">End walk</button>
      </div>
      <div class="walk-progress">${sum.confirmed} confirmed &middot; ${sum.problems} problem${sum.problems === 1 ? '' : 's'} &middot; ${sum.remaining} remaining <span class="hint">(of ${sum.total})</span></div>

      ${dev ? renderDeviceCard(dev, idx, devices.length) : '<p class="empty-hint">Walk complete — every device has been visited. Export below, or End walk to start a new one.</p>'}

      ${dev ? `
      <div class="walk-checklist">
        <button id="walkConfirmBtn" class="walk-btn-confirm">&#10003; Confirmed</button>
        <button id="walkProblemBtn" class="walk-btn-problem">&#9888; Problem</button>
      </div>
      <div id="walkProblemNoteWrap" class="walk-note-wrap" style="display:${showProblemNote ? 'flex' : 'none'};">
        <input id="walkProblemNote" type="text" placeholder="what's wrong? (optional)" maxlength="120">
        <button id="walkProblemSubmit" class="walk-btn-secondary">Save problem</button>
        <button id="walkProblemCancel" class="walk-btn-ghost">Cancel</button>
      </div>
      <div class="walk-field walk-checkbox">
        <label><input type="checkbox" id="walkAutoAdvanceToggle" ${session.autoAdvance ? 'checked' : ''}> Auto-advance to next device after Confirmed</label>
      </div>
      ` : ''}

      ${dev ? renderDeviceAccordion(dev) : ''}

      <div class="walk-export">
        <span class="hint">Export for the architect:</span>
        <button id="walkExportJsonBtn">Export JSON</button>
        <button id="walkExportTxtBtn">Export TXT</button>
      </div>
      <div class="hint" id="walkStatusMsg">${escapeHtml(statusMsg)}</div>

      <div class="walk-navbar">
        <button id="walkPrevBtn" class="walk-nav-btn" ${idx <= 0 ? 'disabled' : ''}>&larr; Previous</button>
        <button id="walkNextBtn" class="walk-nav-btn" ${(idx < 0 || idx >= devices.length - 1) ? 'disabled' : ''}>Next &rarr;</button>
      </div>
    `;
    wireActiveHandlers(dev, idx, devices);
    if (dev) wireAccordionHandlers(dev);

    const noteInput = document.getElementById('walkProblemNote');
    if (noteInput) {
      noteInput.value = noteDraft;
      if (showProblemNote) noteInput.focus();
    }
  }

  // renderDeviceCard is the core "walk one fixture" view: huge counter,
  // then identity in descending size — model/type, manufacturer, DMX
  // universe/address (the two things a tech reads most, biggest after the
  // counter — task ask), UID, node+port smallest. Status is never
  // color-only: every state pairs a color with text/a glyph. The full
  // device panel (accordion) renders separately, below — see
  // renderDeviceAccordion.
  function renderDeviceCard(dev, idx, total) {
    const addr = Api.formatAddressRange(dev.dmxStartAddress, dev.dmxFootprint, dev.addressKnown);
    const statusClass = dev.status === 'confirmed' ? 'walk-status-confirmed' : dev.status === 'problem' ? 'walk-status-problem' : 'walk-status-unvisited';
    const statusGlyph = dev.status === 'confirmed' ? '✓ CONFIRMED' : dev.status === 'problem' ? '⚠ PROBLEM' : 'UNVISITED';
    const identifyBlock = dev.identifyErr
      ? `<div class="walk-identify-error">&#9888; identify failed: ${escapeHtml(dev.identifyErr)}<br><button id="walkRetryIdentifyBtn" class="walk-btn-secondary">Retry identify</button></div>`
      : (dev.identifyOn ? '<div class="walk-identify-ok">&#9679; identifying</div>' : '');
    return `
      <div class="walk-card ${statusClass}">
        <div class="walk-counter">${idx + 1} of ${total}</div>
        <div class="walk-status-pill">${statusGlyph}</div>
        <div class="walk-address">U${dev.portAddress} / ${addr}</div>
        <div class="walk-model">${escapeHtml(dev.model || '—')}</div>
        <div class="walk-mfr">${escapeHtml(dev.manufacturer || '—')}</div>
        <div class="walk-meta">UID ${escapeHtml(dev.uid)}</div>
        <div class="walk-meta">${escapeHtml(dev.nodeIp)} bind ${dev.bindIndex} &middot; port-addr ${dev.portAddress}</div>
        ${identifyBlock}
        ${dev.note ? `<div class="walk-note-display">note: ${escapeHtml(dev.note)}</div>` : ''}
        <div class="walk-address-fix">
          <label>DMX start address</label>
          <span class="apply-field">
            <input id="walkAddrInput" type="number" min="1" max="512" value="${dev.addressKnown ? dev.dmxStartAddress : ''}">
            <span id="walkAddrDirty" class="badge dirty-badge" style="display:none;">pending</span>
            <button id="walkAddrApply" class="btn-apply" disabled>Apply</button>
            <button id="walkAddrRevert" class="btn-revert" style="display:none;">Revert</button>
          </span>
        </div>
      </div>
    `;
  }

  // renderDeviceAccordion is the shared device-detail panel, task ask's
  // "sub-view after a device is selected... phone-optimized: sections as
  // large collapsible accordions rather than small tabs, big touch
  // targets, generous type". Each section header is a full-width button
  // (large touch target); the section body is only rendered (and its data
  // only fetched — see wireAccordionHandlers) while expanded.
  const SECTIONS = [
    { key: 'info', label: 'Info' },
    { key: 'params', label: 'Parameters (address, personality, standard & manufacturer PIDs)' },
    { key: 'sensors', label: 'Sensors' },
    { key: 'status', label: 'Status' },
  ];

  function renderDeviceAccordion(dev) {
    return `
      <div class="walk-accordion" id="walkAccordion">
        ${SECTIONS.map(s => `
          <div class="walk-accordion-item">
            <button class="walk-accordion-header" data-section="${s.key}" aria-expanded="${expandedSections[s.key] ? 'true' : 'false'}">
              <span class="walk-accordion-chevron">${expandedSections[s.key] ? '▾' : '▸'}</span> ${s.label}
            </button>
            <div class="walk-accordion-body" data-section-body="${s.key}" style="display:${expandedSections[s.key] ? '' : 'none'};"></div>
          </div>
        `).join('')}
      </div>
    `;
  }

  function wireAccordionHandlers(dev) {
    const root = document.getElementById('walkAccordion');
    if (!root) return;
    const f = toDetailFixture(dev);

    root.querySelectorAll('.walk-accordion-header').forEach(btn => {
      btn.addEventListener('click', () => {
        const key = btn.dataset.section;
        expandedSections[key] = !expandedSections[key];
        // Deliberate user action on an already-settled selection — applies
        // immediately (no debounce), and only for the section just opened
        // (task ask: "Introspection should be... lazy per section").
        DeviceDetail.select(dev.uid, expandedSections);
        render();
      });
    });

    SECTIONS.forEach(s => {
      if (!expandedSections[s.key]) return;
      const body = root.querySelector(`[data-section-body="${s.key}"]`);
      if (!body) return;
      switch (s.key) {
        case 'info': DeviceDetail.renderInfoSection(body, f); break;
        case 'params': DeviceDetail.renderParamsSection(body, f, setWalkStatus, { hideAddressField: true }); break;
        case 'sensors': DeviceDetail.renderSensorsSection(body, f); break;
        case 'status': DeviceDetail.renderStatusSection(body, f); break;
      }
    });
  }

  function setWalkStatus(msg) {
    statusMsg = msg;
    const el = document.getElementById('walkStatusMsg');
    if (el) el.textContent = msg;
  }

  function wireActiveHandlers(dev, idx, devices) {
    document.getElementById('walkAllOffBtn').addEventListener('click', async () => {
      try { await Api.walkIdentifyAllOff(); } catch (e) { statusMsg = 'error: ' + e.message; }
      await refresh();
    });
    document.getElementById('walkEndBtn').addEventListener('click', async () => {
      if (!confirm('End this walk? You can still export results until you start a new one.')) return;
      try { await Api.endWalk(); } catch (e) { statusMsg = 'error: ' + e.message; }
      showProblemNote = false;
      noteDraft = '';
      DeviceDetail.deselect();
      currentDeviceUID = null;
      await refresh();
    });
    document.getElementById('walkExportJsonBtn').addEventListener('click', () => window.open(Api.walkExportUrl('json'), '_blank'));
    document.getElementById('walkExportTxtBtn').addEventListener('click', () => window.open(Api.walkExportUrl('txt'), '_blank'));

    const prevBtn = document.getElementById('walkPrevBtn');
    const nextBtn = document.getElementById('walkNextBtn');
    if (prevBtn) prevBtn.addEventListener('click', () => gotoIndex(idx - 1));
    if (nextBtn) nextBtn.addEventListener('click', () => gotoIndex(idx + 1));

    if (!dev) return;

    document.getElementById('walkConfirmBtn').addEventListener('click', () => setStatus(dev.uid, 'confirmed', ''));
    document.getElementById('walkProblemBtn').addEventListener('click', () => { showProblemNote = true; render(); });

    const noteInput = document.getElementById('walkProblemNote');
    if (noteInput) noteInput.addEventListener('input', (e) => { noteDraft = e.target.value; });

    const problemSubmit = document.getElementById('walkProblemSubmit');
    if (problemSubmit) problemSubmit.addEventListener('click', () => setStatus(dev.uid, 'problem', noteDraft));
    const problemCancel = document.getElementById('walkProblemCancel');
    if (problemCancel) problemCancel.addEventListener('click', () => { showProblemNote = false; noteDraft = ''; render(); });

    const autoAdvanceToggle = document.getElementById('walkAutoAdvanceToggle');
    if (autoAdvanceToggle) autoAdvanceToggle.addEventListener('change', async (e) => {
      try { await Api.walkAutoAdvance(e.target.checked); } catch (err) { statusMsg = 'error: ' + err.message; }
      await refresh();
    });

    const retryBtn = document.getElementById('walkRetryIdentifyBtn');
    if (retryBtn) retryBtn.addEventListener('click', async () => {
      try { await Api.walkIdentifyRetry(); } catch (e) { statusMsg = 'error: ' + e.message; }
      await refresh();
    });

    // Quick DMX start-address fix — the one address editor Rig Walk shows
    // (the shared Parameters accordion hides its own copy of this field —
    // see renderDeviceAccordion/hideAddressField) since this one is
    // walk-session-aware: applying here also updates the walk card's
    // header address immediately via the normal refresh() below, which a
    // generic params-endpoint apply wouldn't do on its own. Apply-to-
    // confirm rule unchanged: oninput only toggles dirty/Apply state.
    const addrInput = document.getElementById('walkAddrInput');
    const applyBtn = document.getElementById('walkAddrApply');
    const revertBtn = document.getElementById('walkAddrRevert');
    const dirtyBadge = document.getElementById('walkAddrDirty');
    if (addrInput && applyBtn) {
      const baseline = dev.addressKnown ? String(dev.dmxStartAddress) : '';
      const refreshDirty = () => {
        const dirty = addrInput.value !== baseline;
        applyBtn.disabled = !dirty;
        revertBtn.style.display = dirty ? '' : 'none';
        dirtyBadge.style.display = dirty ? '' : 'none';
      };
      addrInput.addEventListener('input', refreshDirty);
      revertBtn.addEventListener('click', () => { addrInput.value = baseline; refreshDirty(); });
      applyBtn.addEventListener('click', async () => {
        const n = Number(addrInput.value);
        if (!Number.isFinite(n) || n < 1 || n > 512) {
          statusMsg = 'address must be 1-512';
          render();
          return;
        }
        applyBtn.disabled = true;
        try {
          await Api.walkSetAddress(dev.uid, n);
          statusMsg = 'address applied';
          await refresh();
        } catch (e) {
          statusMsg = 'error: ' + e.message;
          applyBtn.disabled = false;
        }
      });
    }
  }

  async function gotoIndex(i) {
    try { await Api.walkGoto(i); } catch (e) { statusMsg = 'error: ' + e.message; }
    await refresh();
  }

  async function setStatus(uid, status, note) {
    try {
      await Api.walkSetStatus(uid, status, note);
      showProblemNote = false;
      noteDraft = '';
    } catch (e) {
      statusMsg = 'error: ' + e.message;
    }
    await refresh();
  }

  return { init, onEnterScreen, onLeaveScreen };
})();
