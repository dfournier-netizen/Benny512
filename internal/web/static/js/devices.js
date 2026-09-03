// devices.js — Devices screen (formerly "Fixtures"): every RDM responder is
// first-class here, not just fixtures — gateways, splitters, dimmers,
// wireless radios. Detail panel is the shared DeviceDetail component (see
// devicedetail.js — task ask: "extract the device-detail rendering into ONE
// shared module... Devices tab: behavior unchanged for the user; it now
// renders the shared component"), presented here in its "desktop" tab-strip
// mode (Info/Parameters/Sensors/Status as a tab shell). Rig Walk (walk.js)
// renders the SAME component in its "walk" accordion mode.
//
// Rules (architecture rev 5 §4 / task brief "UI rules (MANDATORY)"):
//  - oninput mutates state only; full re-render happens on onchange.
//  - re-render preserves the selected row, scroll position and focus.
//  - dark high-contrast, system fonts, generous sizing.
//
// 2026-09 screen-kit conversion (css/DESIGN.md). This is a restyle: not one
// control changed what it does. What changed:
//
//  * The screen now reads top to bottom as four numbered .b5-step-sections —
//    "1 Target node and port", "2 Clear discovered devices", "3 Devices
//    found", "4 The device you picked" — instead of five unrelated panels
//    with the destructive Clear buttons floating in the middle of a filter
//    bar. The markup is built HERE rather than in index.html so the kit can
//    be used at all; every element id the old markup carried is preserved
//    exactly, because those ids are this file's only contract with the rest
//    of the app.
//
//  * The device list is no longer a six-column <table>. It is a
//    .b5-board__list of .b5-statecard rows. DESIGN.md's worked example is
//    this exact conversion, and the reason is rule 4: at 820px the old
//    table's Address and Node/Port columns fell off the right edge of the
//    panel, so on a tablet held under a truss the two columns a tech
//    actually walks a rig by were the two he could not see. A stacked card
//    has no minimum width.
//
//  * State is a word, then an icon, then a border weight (rule 1). A device
//    that is not answering is a .b5-statecard.is-danger (10px left rule)
//    carrying a "Not answering" pill AND the server's finished sentence.
//    The device currently open in the detail pane is .is-current — a thick
//    accent frame plus the word "Open below" plus aria-current, three
//    independent signals, none of which is hue.
//
//  * Unknowns are named, never printed as a plausible number (rule 3): a
//    device whose DEVICE_INFO has not come back yet says "address not read
//    yet" or "reading…", not "—" and certainly not "addr 0".
//
//  * Universes go through UI.formatUniverse (rule 5) everywhere they reach
//    the screen: the port picker's labels, the universe filter's option
//    labels, the arm/confirm sentence naming the exact port to be cleared,
//    and every card's meta line. They already did before this change; the
//    conversion kept every one of them and added the card meta line as the
//    only new site.
const DevicesScreen = (() => {
  // DEVICE_CLASS_TONE maps this screen's own vocabulary (RDM device classes)
  // onto the kit's TONE words, in one table, the way DESIGN.md asks and the
  // way reconcile.js's DIFF_TONE does it. A class with no tone gets no tone
  // class at all — an untoned pill is honest, and borrowing a neighbouring
  // state's colour is explicitly forbidden.
  const DEVICE_CLASS_TONE = {
    Fixture: 'info',
    'Gateway/Node': 'accent',
    Splitter: 'accent',
    'Dimmer/Power': 'accent',
    Wireless: 'accent',
    Controller: 'accent',
    Other: '',
    Unknown: '',
  };
  const DEVICE_CLASS_ICON = {
    Fixture: 'nav-devices',
    'Gateway/Node': 'network-node',
    Splitter: 'network-node',
    'Dimmer/Power': 'signal',
    Wireless: 'signal',
    Controller: 'nav-send',
    Other: 'status-pending',
    Unknown: 'status-pending',
  };

  function classPill(cls) {
    const tone = DEVICE_CLASS_TONE[cls];
    const icon = DEVICE_CLASS_ICON[cls] || 'status-pending';
    return `<span class="b5-pill b5-pill--md${tone ? ` b5-pill--${tone}` : ''}">${UI.icon(icon)}${escapeHtml(cls || 'Unknown class')}</span>`;
  }

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
  // field to stage here — the "port" scope IS the node/port select's
  // currently selected port (selectedPort, below), so arming just freezes
  // which port that confirm click will act on). Auto-disarms after a few
  // seconds of inactivity so a stale armed button left on screen can't be
  // mis-clicked later.
  //
  // UNCHANGED by the screen-kit conversion, deliberately and completely: the
  // arm kinds, the 8-second timeout, the exact confirm sentences (which name
  // the port by node name, IP and universe), and the disarm-on-selection-
  // change in setSelectedPort are all byte-identical to before. Only the
  // markup they render into moved.
  let clearArmed = null; // null | 'all' | 'port'
  let clearArmedScope = null; // the {ip,bindIndex,portAddress} frozen at arm time, for 'port'
  let clearBusy = false;
  let clearMsg = '';
  let clearArmTimer = null;
  const CLEAR_ARM_TIMEOUT_MS = 8000;

  // --- Node/port picker -----------------------------------------------------
  // A flat <select> of every node/port, one <option> per port, driving
  // Discover and the port-scoped Clear controls. (The "group ports on one
  // physical device into an expandable block, inspector selects per device"
  // feature briefly lived here — owner clarified it belongs on the Nodes
  // screen instead ("apply that same sorting and feature set... to the
  // nodes tab, not devices... revert the changes to the device tab"); see
  // nodes.js for the grouped accordion + per-device port picker.)
  //
  // selectedPort is the {ip,bindIndex,portAddress} of the currently
  // targeted port — the value Discover and armClear('port') act on.
  let selectedPort = null;

  function selectedNodePort() { return selectedPort; }

  function portKey(s) { return s ? `${s.ip}|${s.bindIndex}|${s.portAddress}` : ''; }

  // portOptions flattens `nodes` (one entry per NodeKey, each with its own
  // ports[]) into one flat list, one entry per port, for the <select>.
  function portOptions() {
    const list = [];
    nodes.forEach(n => {
      (n.ports || []).forEach(p => {
        const name = n.shortName || n.longName || n.ip;
        list.push({
          ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress, index: p.index,
          label: `${name} (${n.ip}) — Port ${p.index} — universe ${UI.formatUniverse(p.outputAddress)}`,
        });
      });
    });
    return list;
  }

  function setSelectedPort(port) {
    selectedPort = port;
    if (clearArmed === 'port') disarmClear(); else renderClearGroup();
  }

  function renderNodeSelect() {
    const sel = document.getElementById('fixtureNodeSelect');
    if (!sel) return;
    const opts = portOptions();
    // If the current selection no longer exists (a node/port disappeared)
    // or nothing is selected yet, default to the first available port so
    // Discover/Clear always have a sane, visible target.
    const stillValid = selectedPort && opts.some(o => portKey(o) === portKey(selectedPort));
    if (!stillValid) selectedPort = opts.length ? opts[0] : null;
    sel.innerHTML = opts.length
      ? opts.map(o => `<option value="${escapeHtml(portKey(o))}" ${selectedPort && portKey(selectedPort) === portKey(o) ? 'selected' : ''}>${escapeHtml(o.label)}</option>`).join('')
      : '<option value="">No nodes discovered yet</option>';
    const note = document.getElementById('devicePickerNote');
    if (note) {
      note.textContent = opts.length
        ? `${opts.length} port${opts.length === 1 ? '' : 's'} to choose from. Universes are ${UI.universeBaseLabel()}.`
        : 'No Art-Net node has answered a poll yet. Check the NIC chosen in Settings is on the node’s network and that the gateway is powered, then press Refresh on the Nodes screen.';
    }
    renderClearGroup();
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
    const section = document.getElementById('clearDevicesSection');
    if (!group) return;
    const sel = selectedNodePort();
    // The armed state changes the SECTION's frame, not just the button's
    // colour — a thick accent border plus the word "Confirm" plus the exact
    // sentence naming the target, so the armed state survives greyscale.
    if (section) section.classList.toggle('is-armed', !!clearArmed);
    if (clearBusy) {
      group.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Clearing…</span>`;
    } else if (clearArmed === 'port') {
      group.innerHTML = `
        <span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}Confirm</span>
        <button id="btnClearConfirm" type="button" class="b5-btn b5-btn--danger b5-clear-armed">${UI.icon('status-warning')}Yes, clear devices on ${nodePortLabel(clearArmedScope)}</button>
        <button id="btnClearCancel" type="button" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Cancel</button>
        <p class="b5-note">This forgets what Benny512 discovered on that one port. It does not change a single fixture — press Discover again to find them all back.</p>`;
    } else if (clearArmed === 'all') {
      group.innerHTML = `
        <span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}Confirm</span>
        <button id="btnClearConfirm" type="button" class="b5-btn b5-btn--danger b5-clear-armed">${UI.icon('status-warning')}Yes, clear ALL discovered devices (every port)</button>
        <button id="btnClearCancel" type="button" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Cancel</button>
        <p class="b5-note">This forgets every device on every port. It does not change a single fixture — press Discover again to find them back, one port at a time.</p>`;
    } else {
      group.innerHTML = `
        <button id="btnClearPort" type="button" class="b5-btn b5-btn--danger" ${sel ? '' : 'disabled'}>${UI.icon('revert')}Clear this port</button>
        <button id="btnClearAll" type="button" class="b5-btn b5-btn--danger">${UI.icon('revert')}Clear ALL ports</button>
        <p class="b5-note">Clears what Benny512 remembers finding, nothing on any fixture. “This port” means the one chosen in step 1: ${sel ? nodePortLabel(sel) : 'no port is selected yet'}. Each button asks you to confirm, and stops asking after 8 seconds.</p>`;
    }
    if (statusRow) {
      statusRow.innerHTML = clearMsg
        ? `<p class="b5-note" id="clearDevicesMsg">${escapeHtml(clearMsg)}</p>`
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
    renderNodeSelect();
    refreshFilterOptions();
  }

  async function refreshFixtures() {
    fixtures = await Api.getFixtures();
    refreshFilterOptions();
    render();
    renderNodeSelect();
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
    // Rule 5: the summary names the universe the tech reads on his gateway,
    // not the raw wire value the filter is keyed on.
    if (universeFilter !== '') parts.push(`universe=${UI.formatUniverse(universeFilter)}`);
    const summaryEl = document.getElementById('deviceFilterSummary');
    const clearBtn = document.getElementById('btnClearDeviceFilters');
    const shown = filteredFixtures().length;
    if (summaryEl) {
      summaryEl.textContent = parts.length
        ? `Filtering by ${parts.join(', ')} — showing ${shown} of ${fixtures.length} device(s)`
        : (fixtures.length ? `Showing all ${fixtures.length} device(s)` : 'Nothing discovered yet');
    }
    if (clearBtn) clearBtn.style.display = parts.length ? '' : 'none';
    const head = document.getElementById('deviceListCount');
    if (head) {
      head.textContent = fixtures.length
        ? `${shown} shown of ${fixtures.length} discovered`
        : 'none discovered yet';
    }
  }

  // addressLabel: rule 3. DEVICE_INFO is fetched lazily and may never come
  // back for a device behind a dead wireless link, so this has three honest
  // answers and no fourth: the real range, "reading…" while the probe is in
  // flight, and "address not read yet" when it is not. It never prints a
  // plausible 0, and it never prints a bare dash that reads as "none".
  function addressLabel(f) {
    const info = DeviceDetail._caches.infoCache[f.uid];
    const di = info && info.deviceInfo;
    if (di) {
      // Rule 3, the exact case DESIGN.md names: never "addr 0".
      //
      // A device with a DMX footprint of 0 occupies no DMX slots and
      // therefore HAS no start address — a gateway, a splitter, a wireless
      // radio. Api.formatAddressRange documents that it renders the
      // footprint-0 case as "the address alone, no parens", which for such a
      // device is the string "0". api.js is behaving exactly as specified
      // there (a footprint-0 device that DOES report an address is a real
      // case it must still print), so the fix belongs at this call site: it
      // must not ask the question at all when there is no footprint. "addr
      // 0" on a card reads as "this responder is patched at address 0", and
      // a tech walking a rig would go looking for it.
      //
      // Observed on the demo rig before this change: the Netron EN4 gateway
      // (UID 1900:00000001, footprint 0) printed "addr 0" on its card and
      // "Currently 0" as the Start address hint in the detail pane, while
      // the Start address input itself — correctly — rendered blank and
      // disabled. Two of the three said a number; only the input was honest.
      if (!di.DMXFootprint) return '<span class="b5-text-muted">no DMX footprint</span>';
      return escapeHtml(Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true));
    }
    return classifying[f.uid]
      ? '<span class="b5-text-muted">reading address…</span>'
      : '<span class="b5-text-muted">address not read yet</span>';
  }

  // manufacturerLabel / modelLabel: rule 3 again. The old table cells printed
  // '…' or '—' here — a single character that says nothing about WHICH of
  // "still reading" and "the device never told us" is true. These say it.
  function manufacturerLabel(f) {
    if (f.manufacturer || f.manufacturerName) return escapeHtml(f.manufacturer || f.manufacturerName);
    return classifying[f.uid]
      ? '<span class="b5-text-muted">reading manufacturer…</span>'
      : '<span class="b5-text-muted">manufacturer not reported</span>';
  }

  function modelLabel(f) {
    if (f.model) return escapeHtml(f.model);
    return (classifying[f.uid] && !f.modelDescriptionKnown && !f.hasDeviceInfo)
      ? 'Reading model…'
      : 'Model not reported';
  }

  // unreachableNoteHTML renders the server's finished sentence verbatim.
  //
  // Round 4's bench symptom was a row that simply stopped filling in once the
  // fixture behind a CRMX link stopped answering — at the bench that is
  // indistinguishable from Benny512 being broken. The server composes the
  // sentence (unreachableNote in internal/web/server.go) so the wording is
  // identical here, in device detail and in Rig Walk; this only places it.
  //
  // The pill and the sentence are both text, per the standing accessibility
  // constraint that color is never the sole signal.
  function unreachableNoteHTML(f) {
    if (!f || !f.unreachable) return '';
    const retry = f.retryAt ? ` Next try ${new Date(f.retryAt).toLocaleTimeString()}.` : '';
    const text = (f.unreachableNote || 'Not answering through its wireless proxy.') + retry;
    return `<p class="b5-note">${escapeHtml(text)}</p>`;
  }

  function modelCell(f) {
    if (classifying[f.uid] && !f.modelDescriptionKnown && !f.hasDeviceInfo) return '…';
    return f.model || '—';
  }

  // deviceCardHtml: DESIGN.md's worked example applied to one device.
  //   tone   — is-danger when the device stopped answering, is-current when
  //            it is the one open in step 4, otherwise the settled hairline.
  //   word   — the class pill, plus a "Not answering" pill, plus "Open below"
  //            on the current one. Every state has a word before it has a hue.
  //   action — a real <button> with a label that says what it does, never a
  //            clickable row.
  function deviceCardHtml(f) {
    const current = f.uid === selectedUID;
    const tone = f.unreachable ? ' is-danger' : (current ? ' is-current' : ' is-ok');
    const meta = [
      manufacturerLabel(f),
      `UID <span class="b5-text-mono">${escapeHtml(f.uid)}</span>`,
      // Rule 5: formatted here, at the presentation boundary, from the raw
      // 0-based Art-Net Port-Address — the same number the detail pane's
      // "Node / port" row, the Patch table and the Analyzer all format.
      `Universe ${escapeHtml(UI.formatUniverse(f.portAddress))}`,
      `addr ${addressLabel(f)}`,
      `node ${escapeHtml(f.nodeIp)}`,
    ].join(' · ');
    return `
      <article class="b5-statecard${tone}" data-uid="${escapeHtml(f.uid)}"${current ? ' aria-current="true"' : ''}>
        <div class="b5-statecard__top">
          <span class="b5-linkbadge${current ? '' : ' b5-linkbadge--none'}" aria-hidden="true">${current ? UI.icon('nav-devices') : '·'}</span>
          <div class="b5-statecard__id">
            <strong class="b5-statecard__name">${modelLabel(f)}</strong>
            <span class="b5-statecard__meta">${meta}</span>
          </div>
        </div>
        <div class="b5-statecard__state">
          ${classPill(f.class)}
          ${f.unreachable ? `<span class="b5-pill b5-pill--md b5-pill--danger">${UI.icon('status-error')}Not answering</span>` : ''}
          ${current ? `<span class="b5-pill b5-pill--md b5-pill--accent">${UI.icon('chevron-expand')}Open below</span>` : ''}
        </div>
        ${unreachableNoteHTML(f)}
        <div class="b5-statecard__actions">
          <button type="button" class="b5-btn${current ? '' : ' b5-btn--primary'} btn-open-device" data-uid="${escapeHtml(f.uid)}"${current ? ' disabled' : ''}>
            ${UI.icon(current ? 'status-ok' : 'chevron-expand')}${current ? 'Open below' : 'Open this device'}
          </button>
        </div>
      </article>
    `;
  }

  function render() {
    updateFilterSummary();
    const list = document.getElementById('deviceList');
    if (!list) return;
    const scrollTop = list.scrollTop;
    const items = visibleFixtures();
    if (!items.length) {
      list.innerHTML = fixtures.length
        ? `<p class="b5-board__empty">No device matches the filters set above. Clear the filters to see all ${fixtures.length} discovered device(s).</p>`
        : `<p class="b5-board__empty">No device has been discovered yet. Pick a node and port in step 1 and press Discover — every RDM responder that answers on that port lands here, fixtures and infrastructure alike.</p>`;
      return;
    }
    list.innerHTML = items.map(deviceCardHtml).join('');
    list.querySelectorAll('.btn-open-device').forEach(btn => {
      btn.addEventListener('click', () => { selectDevice(btn.dataset.uid); });
    });
    list.scrollTop = scrollTop;
  }

  function selectDevice(uid) {
    if (selectedUID === uid) return;
    selectedUID = uid;
    activeTab = 'info';
    render();
    renderDetail();
    DeviceDetail.select(uid, sectionsForActiveTab());
    const pane = document.getElementById('fixtureDetail');
    if (pane && pane.scrollIntoView) pane.scrollIntoView({ block: 'start' });
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
    if (!el) return;
    const f = fixtures.find(x => x.uid === selectedUID);
    const head = document.getElementById('deviceDetailNote');
    if (!f) {
      if (head) head.textContent = 'nothing picked yet';
      el.innerHTML = `<p class="b5-board__empty">No device is open. Press “Open this device” on a card in step 3 to read and edit its RDM parameters, sensors and status.</p>`;
      return;
    }
    if (head) head.textContent = `${modelCell(f)} · UID ${f.uid}`;
    el.innerHTML = `
      <article class="b5-statecard is-current">
        <div class="b5-statecard__top">
          <span class="b5-linkbadge" aria-hidden="true">${UI.icon('nav-devices')}</span>
          <div class="b5-statecard__id">
            <strong class="b5-statecard__name">${escapeHtml(modelCell(f))}</strong>
            <span class="b5-statecard__meta">${manufacturerLabel(f)} · UID <span class="b5-text-mono">${escapeHtml(f.uid)}</span> · Universe ${escapeHtml(UI.formatUniverse(f.portAddress))} · node ${escapeHtml(f.nodeIp)}</span>
          </div>
        </div>
        <div class="b5-statecard__state">${classPill(f.class)}</div>
        <div class="b5-statecard__actions">
          <button id="btnExportDeviceJson" type="button" class="b5-btn">${UI.icon('export')}Export this device’s RDM capture (JSON)</button>
          <button id="btnExportDeviceTxt" type="button" class="b5-btn">${UI.icon('export')}Export as TXT</button>
        </div>
      </article>

      <div class="b5-tabs" style="margin-top:var(--b5-space-4)">
        <div class="b5-tabs__list" id="deviceTabs" role="tablist">
          <button class="b5-tabs__tab detail-tab-btn" data-tab="info" role="tab">Info</button>
          <button class="b5-tabs__tab detail-tab-btn" data-tab="params" role="tab">Parameters</button>
          <button class="b5-tabs__tab detail-tab-btn" data-tab="sensors" role="tab">Sensors</button>
          <button class="b5-tabs__tab detail-tab-btn" data-tab="status" role="tab">Status</button>
        </div>
        <div class="b5-tabs__panel" id="deviceTabBody"></div>
      </div>
      <p class="b5-note" id="fxStatus"></p>
    `;
    document.getElementById('btnExportDeviceJson').addEventListener('click', () => {
      window.open(Api.exportUrl('json', { uid: f.uid }), '_blank');
    });
    document.getElementById('btnExportDeviceTxt').addEventListener('click', () => {
      window.open(Api.exportUrl('txt', { uid: f.uid }), '_blank');
    });
    el.querySelectorAll('.detail-tab-btn').forEach(btn => {
      const on = btn.dataset.tab === activeTab;
      btn.classList.toggle('is-active', on);
      btn.setAttribute('aria-selected', on ? 'true' : 'false');
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

  // --- markup ---------------------------------------------------------------
  // Built here rather than in index.html so this screen can use the kit's
  // step sections. Every id below existed in the old markup and is unchanged;
  // #fixturesTable is gone because the table it named is gone (see the file
  // header), and #deviceList / #clearDevicesSection / #devicePickerNote /
  // #deviceListCount / #deviceDetailNote are new.

  function screenHtml() {
    return `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Devices</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">Every RDM responder on the line — fixtures and infrastructure alike.</span>
      </div>

      <section class="b5-step-section" aria-labelledby="devPickHead">
        <h2 class="b5-step-section__head" id="devPickHead">
          <span class="b5-step-num">1</span> Target node and port
          <span class="b5-step-section__note">what Discover and “Clear this port” act on</span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row">
            <label class="b5-visually-hidden" for="fixtureNodeSelect">Node and port</label>
            <select id="fixtureNodeSelect" class="b5-select" style="flex:1 1 260px;min-width:0"><option value="">No nodes discovered yet</option></select>
            <button id="btnDiscover" type="button" class="b5-btn b5-btn--primary">${UI.icon('signal')}Discover on this port</button>
            <span class="b5-inline-wait" id="discoverStatus"></span>
          </div>
          <p class="b5-caption" id="devicePickerNote"></p>
        </div>
      </section>

      <section class="b5-step-section" id="clearDevicesSection" aria-labelledby="devClearHead">
        <h2 class="b5-step-section__head" id="devClearHead">
          <span class="b5-step-num">2</span> Clear discovered devices
          <span class="b5-step-section__note">arm, then confirm — nothing is sent to any fixture</span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row" id="clearDevicesGroup"></div>
        </div>
        <div id="clearDevicesStatusRow"></div>
      </section>

      <section class="b5-step-section" aria-labelledby="devListHead">
        <h2 class="b5-step-section__head" id="devListHead">
          <span class="b5-step-num">3</span> Devices found
          <span class="b5-step-section__note" id="deviceListCount">none discovered yet</span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row">
            <label class="b5-visually-hidden" for="deviceClassFilter">Class</label>
            <select id="deviceClassFilter" class="b5-select">
              <option value="">Class: all</option>
              <option value="Fixture">Fixture</option>
              <option value="Gateway/Node">Gateway/Node</option>
              <option value="Splitter">Splitter</option>
              <option value="Dimmer/Power">Dimmer/Power</option>
              <option value="Wireless">Wireless</option>
              <option value="Controller">Controller</option>
              <option value="Other">Other</option>
              <option value="Unknown">Unknown</option>
            </select>
            <label class="b5-visually-hidden" for="deviceNodeFilter">Node</label>
            <select id="deviceNodeFilter" class="b5-select"><option value="">Node: all</option></select>
            <label class="b5-visually-hidden" for="deviceUniverseFilter">Universe</label>
            <select id="deviceUniverseFilter" class="b5-select"><option value="">Universe: all</option></select>
            <label class="b5-visually-hidden" for="deviceSort">Sort</label>
            <select id="deviceSort" class="b5-select">
              <option value="address">Sort: DMX address (low&rarr;high)</option>
              <option value="address_desc">Sort: DMX address (high&rarr;low)</option>
              <option value="model">Sort: Model / fixture type</option>
              <option value="uid">Sort: UID</option>
            </select>
            <button id="btnClearDeviceFilters" type="button" class="b5-btn b5-btn--ghost" style="display:none;">${UI.icon('revert')}Clear filters</button>
          </div>
          <p class="b5-caption" id="deviceFilterSummary"></p>
        </div>
        <div class="b5-board__list" id="deviceList"></div>
      </section>

      <section class="b5-step-section" aria-labelledby="devDetailHead">
        <h2 class="b5-step-section__head" id="devDetailHead">
          <span class="b5-step-num">4</span> The device you picked
          <span class="b5-step-section__note" id="deviceDetailNote">nothing picked yet</span>
        </h2>
        <div id="fixtureDetail"></div>
      </section>
    `;
  }

  function init() {
    const screen = document.getElementById('screen-devices');
    if (screen) screen.innerHTML = screenHtml();

    document.getElementById('btnDiscover').addEventListener('click', discover);
    // Changing the port selection while a 'port'-scope clear is armed would
    // let a confirm click fire against a port the tech isn't looking at any
    // more — setSelectedPort (wired to the select's 'change' below) disarms
    // rather than silently retargeting.
    const nodePortSel = document.getElementById('fixtureNodeSelect');
    if (nodePortSel) {
      nodePortSel.addEventListener('change', (e) => {
        const opt = portOptions().find(o => portKey(o) === e.target.value);
        setSelectedPort(opt || null);
      });
    }
    renderClearGroup();
    renderDetail();
    // A clear can be triggered from any open browser (task ask) — refresh
    // this one's list either way; our own confirmClear() already awaits
    // refreshFixtures() itself, so this is a harmless extra refresh when
    // it's our own action, and the only refresh when it's someone else's.
    Live.on('devices_cleared', () => { refreshFixtures(); });
    // Universe base changed on the Settings screen — refresh the universe
    // filter dropdown's labels, the device/port picker's universe labels,
    // every card's meta line and the open detail pane, in place.
    window.addEventListener('b5-universe-base-changed', () => { refreshFilterOptions(); render(); renderNodeSelect(); renderDetail(); });

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
    // Label input inside it) at all. render() (the device list) has no
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
    if (!selectedPort) { status.textContent = 'no device/port selected'; return; }
    const { ip, bindIndex, portAddress } = selectedPort;
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
