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
  let inspectorOpen = false;
  // Filters combine (AND) — class + node + universe (task ask, item 3: the
  // Devices table already aggregates every node/port's devices in one
  // rig-wide list, per Registry.Devices()/handleGetFixtures; these three
  // filters narrow that list without ever re-scoping the underlying fetch).
  // Persisted to sessionStorage (item 4: "persist sort/filter choice for
  // the session") rather than localStorage, which the rest of this app
  // reserves for cross-restart preferences like the active tab.
  let classFilter = sessionStorage.getItem('benny512.devices.classFilter') || '';
  // Older sessions stored IP|BindIndex. A binding is not a physical node.
  let nodeFilter = (sessionStorage.getItem('benny512.devices.nodeFilter') || '').split('|')[0];
  let universeFilter = sessionStorage.getItem('benny512.devices.universeFilter') || '';
  let sortOrder = sessionStorage.getItem('benny512.devices.sort') || 'address';
  let activeTab = 'info'; // info | params | sensors | status

  // classifying tracks the background device_info/PRODUCT_DETAIL_ID_LIST
  // classification sweep below — unrelated to DeviceDetail's own caches.
  let classifying = {};
  let nodesRevision = 0, fixturesRevision = 0;
  let nodesLoaded = false, fixturesLoaded = false;
  let liveTimer = null, liveBusy = false, liveNodes = false, liveFixtures = false;
  let discoveryBusy = false, discoveryStopped = false;

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
  // One group per physical IP, retaining the binding in every wire target.
  //
  // selectedPort is the {ip,bindIndex,portAddress} of the currently
  // targeted port — the value Discover and armClear('port') act on.
  let selectedPort = null;

  function selectedNodePort() { return selectedPort; }

  function portKey(s) { return s ? `${s.ip}|${s.bindIndex}|${s.portAddress}` : ''; }

  function nodeGroups() {
    const groups = new Map();
    nodes.forEach(n => {
      if (!groups.has(n.ip)) groups.set(n.ip, {ip: n.ip, entries: []});
      groups.get(n.ip).entries.push(n);
    });
    return [...groups.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, {numeric: true})).map(g => {
      g.entries.sort((a, b) => a.bindIndex - b.bindIndex);
      // ShortName may name a single binding/port; prefer the parent LongName.
      g.name = g.entries[0].longName || (g.entries.length === 1 ? g.entries[0].shortName : '') || g.ip;
      return g;
    });
  }

  // portOptions flattens `nodes` (one entry per NodeKey, each with its own
  // ports[]) into one flat list, one entry per port, for the <select>.
  function portOptions() {
    const list = [];
    nodeGroups().forEach(g => g.entries.forEach(n => {
      const ports = n.ports || [];
      ports.forEach(p => {
        if (p.output === false) return; // RDM discovery targets DMX outputs.
        const name = ports.length === 1 && n.shortName ? n.shortName : '';
        const local = ports.length > 1 ? `Port ${p.index + 1}` : '';
        const portName = [g.entries.length > 1 ? `Bind ${n.bindIndex}` : '', name || local].filter(Boolean).join(' · ') || 'Port 1';
        list.push({
          ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress, index: p.index,
          group: `${g.name} (${n.ip})`,
          label: `${portName} — universe ${UI.formatUniverse(p.outputAddress)}`,
        });
      });
    }));
    return [...new Map(list.map(p => [portKey(p), p])).values()];
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
      ? nodeGroups().map(g => {
          const own = opts.filter(o => o.ip === g.ip);
          return own.length ? `<optgroup label="${escapeHtml(own[0].group)}">${own.map(o => `<option value="${escapeHtml(portKey(o))}" ${selectedPort && portKey(selectedPort) === portKey(o) ? 'selected' : ''}>${escapeHtml(o.label)} · ${escapeHtml(o.ip)}</option>`).join('')}</optgroup>` : '';
        }).join('')
      : '<option value="">No nodes discovered yet</option>';
    renderClearGroup();
    renderDiscoveryControls();
  }

  function nodePortLabel(scope) {
    if (!scope) return '(no port selected)';
    const n = nodes.find(x => x.ip === scope.ip && x.bindIndex === scope.bindIndex);
    const name = n ? (n.shortName || n.longName || n.ip) : scope.ip;
    return `${escapeHtml(name)} (${escapeHtml(scope.ip)}) universe ${UI.formatUniverse(scope.portAddress)}`;
  }

  function armClear(kind) {
    if (discoveryBusy) return;
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
    renderDiscoveryControls();
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
    renderDiscoveryControls();
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
        <p class="b5-note">Benny512 memory only; fixtures are unchanged.</p>`;
    } else if (clearArmed === 'all') {
      group.innerHTML = `
        <span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}Confirm</span>
        <button id="btnClearConfirm" type="button" class="b5-btn b5-btn--danger b5-clear-armed">${UI.icon('status-warning')}Yes, clear ALL discovered devices (every port)</button>
        <button id="btnClearCancel" type="button" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Cancel</button>
        <p class="b5-note">Benny512 memory only; fixtures are unchanged.</p>`;
    } else {
      group.innerHTML = `
        <button id="btnClearPort" type="button" class="b5-btn b5-btn--danger" ${sel && !discoveryBusy ? '' : 'disabled'}>${UI.icon('revert')}Clear this port</button>
        <button id="btnClearAll" type="button" class="b5-btn b5-btn--danger" ${discoveryBusy ? 'disabled' : ''}>${UI.icon('revert')}Clear ALL ports</button>
        <p class="b5-note">Clears Benny512’s discovered-device memory only. Confirmation expires after 8 seconds.</p>`;
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
    const revision = ++nodesRevision;
    const snapshot = await Api.getNodes();
    if (revision !== nodesRevision) return;
    nodes = snapshot;
    nodesLoaded = true;
    renderNodeSelect();
    refreshFilterOptions();
  }

  async function refreshFixtures(probe = true) {
    const revision = ++fixturesRevision;
    const snapshot = await Api.getFixtures();
    if (revision !== fixturesRevision) return;
    fixtures = snapshot;
    fixturesLoaded = true;
    refreshFilterOptions();
    render();
    renderNodeSelect();
    if (probe) classifyUnknown();
  }

  // Events refresh registry snapshots only. No automatic RDM probe loop, and
  // no inspector redraw that could steal focus from an in-progress edit.
  function scheduleLiveRefresh(wantNodes, wantFixtures) {
    liveNodes ||= wantNodes;
    liveFixtures ||= wantFixtures;
    if (liveTimer !== null || liveBusy) return;
    liveTimer = setTimeout(async () => {
      liveTimer = null;
      liveBusy = true;
      const getNodes = liveNodes, getFixtures = liveFixtures;
      liveNodes = liveFixtures = false;
      try {
        await Promise.all([getNodes ? refreshNodes() : null, getFixtures ? refreshFixtures(false) : null]);
      } catch (e) {
        if (!discoveryBusy) document.getElementById('discoverStatus').textContent = 'Refresh failed: ' + e.message;
      } finally {
        liveBusy = false;
        if (liveNodes || liveFixtures) scheduleLiveRefresh(false, false);
      }
    }, 250);
  }

  function nodeFilterKey(ipLike) {
    return ipLike.nodeIp || ipLike.ip;
  }

  function refreshFilterOptions() {
    const nodeSel = document.getElementById('deviceNodeFilter');
    if (nodeSel) {
      nodeSel.innerHTML = '<option value="">Node: all</option>';
      nodeGroups().forEach(n => {
        const opt = document.createElement('option');
        opt.value = nodeFilterKey(n);
        opt.textContent = `${n.name} (${n.ip})`;
        nodeSel.appendChild(opt);
      });
      nodeSel.value = nodeFilter;
      if (nodesLoaded && nodeSel.value !== nodeFilter) { nodeFilter = ''; persistFilters(); }
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
      if (fixturesLoaded && uniSel.value !== universeFilter) { universeFilter = ''; persistFilters(); }
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
      await refreshFixtures(false);
    }).catch(() => {}); // A subsequent live event/reconnect retries the snapshot.
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
      const n = nodeGroups().find(x => x.ip === nodeFilter);
      parts.push(`node=${n ? n.name + ' (' + n.ip + ')' : nodeFilter}`);
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
          ${current ? `<span class="b5-pill b5-pill--md b5-pill--accent">${UI.icon('chevron-expand')}Inspecting</span>` : ''}
        </div>
        ${unreachableNoteHTML(f)}
        <div class="b5-statecard__actions">
          <button type="button" class="b5-btn${current ? '' : ' b5-btn--primary'} btn-open-device" data-uid="${escapeHtml(f.uid)}">
            ${UI.icon(current ? 'chevron-expand' : 'chevron-expand')}${current ? 'Show inspector' : 'Inspect'}
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
        : `<p class="b5-board__empty">No devices yet. Discover this port or all ports.</p>`;
      return;
    }
    list.innerHTML = items.map(deviceCardHtml).join('');
    list.querySelectorAll('.btn-open-device').forEach(btn => {
      btn.addEventListener('click', () => { selectDevice(btn.dataset.uid); });
    });
    list.scrollTop = scrollTop;
  }

  function selectDevice(uid) {
    if (selectedUID === uid && inspectorOpen) return;
    selectedUID = uid;
    window.dispatchEvent(new CustomEvent('b5-device-selection', {detail:uid}));
    inspectorOpen = true;
    activeTab = 'info';
    render();
    renderDetail();
    DeviceDetail.select(uid, sectionsForActiveTab());
  }

  function selectAdjacentDevice(step) {
    const items = visibleFixtures();
    const at = items.findIndex(f => f.uid === selectedUID);
    const next = items[at + step];
    if (next) selectDevice(next.uid);
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
    const inspector = document.getElementById('fixtureInspector');
    const f = fixtures.find(x => x.uid === selectedUID);
    if (inspector) inspector.classList.toggle('is-open', !!(f && inspectorOpen));
    if (!f) {
      el.innerHTML = '';
      return;
    }
    const items = visibleFixtures();
    const at = items.findIndex(item => item.uid === f.uid);
    const previous = document.getElementById('btnPreviousDevice');
    const next = document.getElementById('btnNextDevice');
    if (previous) previous.disabled = at <= 0;
    if (next) next.disabled = at < 0 || at >= items.length - 1;
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
    const close = document.getElementById('btnCloseDeviceInspector');
    if (close) close.addEventListener('click', () => { inspectorOpen = false; render(); renderDetail(); });
    if (previous) previous.addEventListener('click', () => selectAdjacentDevice(-1));
    if (next) next.addEventListener('click', () => selectAdjacentDevice(1));
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
      <div class="b5-page-header"><h1 class="b5-page-header__title">Devices</h1></div>

      <div class="b5-toolbar b5-devices-target" aria-label="Discovery target">
        <div class="b5-toolbar__row">
            <label class="b5-visually-hidden" for="fixtureNodeSelect">Node and port</label>
            <select id="fixtureNodeSelect" class="b5-select" style="flex:1 1 260px;min-width:0"><option value="">No nodes discovered yet</option></select>
            <button id="btnDiscover" type="button" class="b5-btn b5-btn--primary">${UI.icon('signal')}Discover this port</button>
            <button id="btnDiscoverAll" type="button" class="b5-btn">${UI.icon('signal')}Discover all ports</button>
            <button id="btnStopDiscovery" type="button" class="b5-btn" hidden>Stop after this port</button>
            <span class="b5-inline-wait" id="discoverStatus" role="status" aria-live="polite"></span>
        </div>
      </div>

      <details class="b5-inset b5-devices-clear" id="clearDevicesSection">
        <summary>Clear discovered-device memory</summary>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row" id="clearDevicesGroup"></div>
        </div>
        <div id="clearDevicesStatusRow"></div>
      </details>

      <section aria-labelledby="devListHead">
        <div class="b5-section-head"><h2 id="devListHead">Devices</h2><span class="b5-text-muted" id="deviceListCount">none discovered yet</span></div>
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

      <aside class="b5-inspector" id="fixtureInspector" aria-label="Device inspector" aria-live="polite">
        <div class="b5-inspector__bar"><strong>Device inspector</strong><span class="b5-inspector__actions"><button id="btnPreviousDevice" type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Previous</button><button id="btnNextDevice" type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Next</button><button id="btnCloseDeviceInspector" type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Close</button></span></div>
        <div id="fixtureDetail"></div>
      </aside>
    `;
  }

  function init() {
    const screen = document.getElementById('screen-devices');
    if (screen) screen.innerHTML = screenHtml();

    document.getElementById('btnDiscover').addEventListener('click', discover);
    document.getElementById('btnDiscoverAll').addEventListener('click', () => discoverPorts(portOptions()));
    document.getElementById('btnStopDiscovery').addEventListener('click', () => {
      discoveryStopped = true;
      document.getElementById('discoverStatus').textContent = 'Stopping after the current port…';
    });
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
    Live.on('devices_cleared', () => scheduleLiveRefresh(false, true));
    Live.on('node', () => scheduleLiveRefresh(true, false));
    Live.on('rdm', () => scheduleLiveRefresh(false, true));
    Live.on('connected', () => scheduleLiveRefresh(true, true));
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

    Promise.all([refreshNodes(), refreshFixtures()]).catch(e => {
      document.getElementById('discoverStatus').textContent = 'Refresh failed: ' + e.message;
    });
  }

  function renderDiscoveryControls() {
    const single = document.getElementById('btnDiscover');
    const all = document.getElementById('btnDiscoverAll');
    const stop = document.getElementById('btnStopDiscovery');
    if (single) single.disabled = discoveryBusy || clearBusy || !selectedPort;
    if (all) all.disabled = discoveryBusy || clearBusy || !portOptions().length;
    if (stop) stop.hidden = !discoveryBusy;
  }

  function discover() {
    return discoverPorts(selectedPort ? [{...selectedPort}] : []);
  }

  async function discoverPorts(targets) {
    if (discoveryBusy || clearBusy) return;
    const status = document.getElementById('discoverStatus');
    if (!targets.length) { status.textContent = 'No output ports discovered yet'; return; }
    // Snapshot and serialize. Switching targets or receiving node updates must
    // not redirect an active scan or flush several ports on a gateway at once.
    const queue = targets.map(p => ({...p}));
    discoveryBusy = true;
    discoveryStopped = false;
    disarmClear();
    renderDiscoveryControls();
    const uids = new Set(), errors = [];
    let scanned = 0, incomplete = 0, refreshFailed = false;
    try {
      for (const p of queue) {
        if (discoveryStopped) break;
        status.textContent = `Discovering ${scanned + 1}/${queue.length} — ${p.ip} · ${p.label}`;
        try {
          const res = await Api.discover(p.ip, p.bindIndex, p.portAddress);
          (res.uids || []).forEach(uid => uids.add(uid));
          if (!res.complete) incomplete++;
        } catch (e) {
          errors.push(`${p.ip} · ${p.label}: ${e.message}`);
        }
        scanned++;
        try { await refreshFixtures(); } catch (_) { refreshFailed = true; }
      }
      status.textContent = `${discoveryStopped ? 'Stopped' : 'Done'} — ${scanned}/${queue.length} ports · ${uids.size} unique device(s) · ${errors.length} failed · ${incomplete} incomplete` +
        (errors.length ? '. ' + errors.join('; ') : '') + (refreshFailed ? '. Device-list refresh failed.' : '');
    } finally {
      discoveryBusy = false;
      renderClearGroup();
      renderDiscoveryControls();
    }
  }

  return { init, refreshNodes, refreshFixtures, inspect: selectDevice };
})();
