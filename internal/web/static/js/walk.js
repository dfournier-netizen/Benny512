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

  // --- universe notation ----------------------------------------------------
  //
  // DESIGN.md rule 5: every universe on this screen goes through
  // UI.formatUniverse, from the raw 0-based Art-Net Port-Address the server
  // sent, and every typed one comes back through UI.parseUniverse. Never a
  // raw canonical number on screen, never a re-parse of a string already
  // displayed.
  //
  // WHAT WAS WRONG HERE (the open item this conversion was sent to close):
  // three places on this screen printed or accepted a RAW Port-Address and
  // called it a universe.
  //
  //   1. The scope picker's "One universe (Port-Address)" input was a bare
  //      <input min=0 max=32767 value=0> read straight into scopeValue. At
  //      the owner's default base of 1 the box therefore said 0 for a
  //      universe every other screen in the app calls 1 — and worse, typing
  //      the number he reads on Patch ("3") started a walk on wire universe
  //      3, i.e. the one Patch calls 4. The universe he asked for and the
  //      universe he got were one apart, silently, on the screen whose whole
  //      job is walking a rig fixture by fixture.
  //   2. The "One port" picker labelled each port "(addr 12)" — again the
  //      raw Port-Address, next to a Nodes screen that has shown the display
  //      base since its own conversion.
  //   3. The active walk card's kicker printed "U${dev.portAddress}" and its
  //      footer "port-addr ${dev.portAddress}" — the same raw number twice,
  //      on the card he reads while standing under the fixture.
  //
  // This is the same class of defect as nodes.js's masked nibble and
  // analyzer.js's phantom universe: one value spelled two ways. All three
  // now go through uni() below, and scopeUniverseCanonical holds the true
  // 0-based value so a display-base change re-labels it without ever
  // re-reading the box.
  const uni = (raw) => UI.formatUniverse(raw);

  // Setup-screen (pre-walk) state.
  let nodesCache = [];
  let scopeKind = sessionStorage.getItem('benny512.walk.scopeKind') || 'all';
  // scopeUniverseCanonical: the 0-based wire Port-Address the "One universe"
  // scope will start on. THIS is the source of truth; the input only ever
  // shows/accepts the display-base-converted number, exactly as send.js's
  // sendUniverseCanonical and nodes.js's staged port fields do.
  let scopeUniverseCanonical = 0;
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
    // Universe base changed on Settings — re-render so an open device
    // card's "Node / port" line (devicedetail.js's shared Info section,
    // now routed through UI.formatUniverse) reflects the new base
    // immediately instead of showing a stale number until the next section
    // update happens to redraw it.
    window.addEventListener('b5-universe-base-changed', () => render());
    // scope names which section's data changed ('info'/'params'/'sensors'/
    // 'status', or undefined for "not sure, do a full render"). Rig Walk's
    // accordion can have several sections expanded at once, so — unlike the
    // Devices tab's single active-tab check — this only skips work when the
    // changed section's own accordion is collapsed; when it IS expanded,
    // only that one accordion body is rebuilt (updateAccordionSection)
    // rather than the whole walk screen (render()), which used to tear down
    // every other expanded section's DOM — including a Parameters accordion
    // mid-edit on the Device Label — for, say, a 'sensors' push meant for
    // the Sensors accordion. This is the Rig Walk half of the fix for the
    // bench report "Device Label kicks you out mid-typing".
    DeviceDetail.init((scope) => {
      if (!active) return;
      if (!scope) { render(); return; }
      if (!expandedSections[scope]) return; // section not on screen — nothing to redraw
      if (!updateAccordionSection(scope)) render(); // fallback if the accordion DOM isn't there yet
    });
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

  const SCOPE_WORD = {
    all: 'every discovered device',
    node: 'one node',
    port: 'one port',
    universe: 'one universe',
    class: 'one device class',
  };

  function renderSetup(el) {
    el.innerHTML = `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Rig Walk</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">One fixture flashing at a time, in your hand, down the truss.</span>
      </div>
      <p class="b5-note">Landing on a device flashes it (Identify ON); moving on turns it off and flashes the next one &mdash; exactly one fixture is ever lit. Leaving this screen, backgrounding the tab or closing it also turns Identify off, so nothing is left flashing on a truss. The full device panel &mdash; sensors, address, personality, standard and manufacturer parameters, status &mdash; opens below each device.</p>

      <section class="b5-step-section" aria-labelledby="walkScopeHead">
        <h2 class="b5-step-section__head" id="walkScopeHead">
          <span class="b5-step-num">1</span> What to walk
          <span class="b5-step-section__note" id="walkScopeNote">${escapeHtml(SCOPE_WORD[scopeKind] || scopeKind)}</span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="walkScopeKind">Scope</label>
          <select id="walkScopeKind" class="b5-select">
            <option value="all">All devices</option>
            <option value="node">One node</option>
            <option value="port">One port</option>
            <option value="universe">One universe</option>
            <option value="class">One device class</option>
          </select>
        </div>
        <div id="walkScopeValueWrap"></div>
      </section>

      <section class="b5-step-section" aria-labelledby="walkOrderHead">
        <h2 class="b5-step-section__head" id="walkOrderHead">
          <span class="b5-step-num">2</span> What order to walk it in
          <span class="b5-step-section__note">the order you meet the fixtures, not the order they were found</span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="walkOrder">Order</label>
          <select id="walkOrder" class="b5-select">
            <option value="address">Universe + DMX address (low&rarr;high)</option>
            <option value="address_desc">Universe + DMX address (high&rarr;low)</option>
            <option value="model">Model / fixture type</option>
            <option value="uid">UID</option>
            <option value="discovery">Discovery order</option>
          </select>
        </div>
        <label class="b5-choicecard ${fixturesOnly ? 'is-on' : ''}" id="walkFixturesOnlyCard">
          <input type="checkbox" id="walkFixturesOnly" ${fixturesOnly ? 'checked' : ''}>
          <span class="b5-choicecard__box" aria-hidden="true">${fixturesOnly ? UI.icon('status-ok') : ''}</span>
          <span>
            <span class="b5-choicecard__title">Fixtures only &mdash; ${fixturesOnly ? 'ON' : 'OFF'}</span>
            <span class="b5-choicecard__body">${fixturesOnly
              ? 'Splitters, gateways, dimmer racks and anything else that is not a light are skipped. Turn this off to walk every RDM device that answered.'
              : 'Every RDM device that answered is walked, including splitters, gateways and dimmer racks — not just lights.'}</span>
          </span>
        </label>
        <span class="b5-text-muted b5-text-sm" id="walkSetupStatus">${escapeHtml(statusMsg)}</span>
      </section>

      <section class="b5-actionbar" aria-label="Start the walk">
        <div class="b5-actionbar__status">
          <span class="b5-actionbar__title"><span class="b5-step-num">3</span> Start</span>
          <span class="b5-pill b5-pill--lg b5-pill--open">${UI.icon('status-pending')}No walk running</span>
        </div>
        <div class="b5-actionbar__buttons">
          <button id="walkStartBtn" class="b5-bigbtn b5-bigbtn--go" ${starting ? 'disabled' : ''}>${starting ? UI.spinner() + 'STARTING…' : UI.icon('identify') + 'START WALK'}</button>
        </div>
      </section>
    `;
    renderScopeValue();
    document.getElementById('walkScopeKind').value = scopeKind;
    document.getElementById('walkOrder').value = orderMode;

    document.getElementById('walkScopeKind').addEventListener('change', (e) => {
      scopeKind = e.target.value;
      sessionStorage.setItem('benny512.walk.scopeKind', scopeKind);
      const note = document.getElementById('walkScopeNote');
      if (note) note.textContent = SCOPE_WORD[scopeKind] || scopeKind;
      renderScopeValue();
    });
    document.getElementById('walkOrder').addEventListener('change', (e) => {
      orderMode = e.target.value;
      sessionStorage.setItem('benny512.walk.order', orderMode);
    });
    document.getElementById('walkFixturesOnly').addEventListener('change', (e) => {
      fixturesOnly = e.target.checked;
      sessionStorage.setItem('benny512.walk.fixturesOnly', String(fixturesOnly));
      // The choice card repeats its own state as a WORD ("— ON"/"— OFF"),
      // so it has to be repainted, not merely re-tinted (rule 1).
      render();
    });
    document.getElementById('walkStartBtn').addEventListener('click', startWalk);
  }

  function renderScopeValue() {
    const wrap = document.getElementById('walkScopeValueWrap');
    if (!wrap) return;
    switch (scopeKind) {
      case 'node': {
        wrap.innerHTML = nodesCache.length
          ? `<div class="b5-field"><label class="b5-field__label" for="walkScopeNode">Node</label><select id="walkScopeNode" class="b5-select">
          ${nodesCache.map(n => `<option value="${escapeHtml(n.ip)}">${escapeHtml(n.shortName || n.longName || n.ip)} (${escapeHtml(n.ip)})</option>`).join('')}
        </select></div>`
          // Rule 3: an empty picker names what is missing and what to do.
          : `<p class="b5-board__empty">No Art-Net nodes have answered a poll yet, so there is no node to scope to. Check the NIC in Settings and the gateway's power, then come back — or walk "All devices" instead.</p>`;
        break;
      }
      case 'port': {
        const opts = [];
        nodesCache.forEach(n => (n.ports || []).forEach(p => {
          // The port's outputAddress IS an Art-Net Port-Address — the same
          // kind of number the Nodes screen and Patch both show through the
          // display base. It used to be printed raw here as "(addr 12)".
          opts.push(`<option value='${JSON.stringify({ ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress })}'>${escapeHtml(n.shortName || n.ip)} — port ${p.index} (universe ${escapeHtml(uni(p.outputAddress))})</option>`);
        }));
        wrap.innerHTML = opts.length
          ? `<div class="b5-field"><label class="b5-field__label" for="walkScopePort">Port (universes ${escapeHtml(UI.universeBaseLabel())})</label><select id="walkScopePort" class="b5-select">${opts.join('')}</select></div>`
          : `<p class="b5-board__empty">No node has reported a port yet, so there is no port to scope to. Refresh the Nodes screen, or walk "All devices" instead.</p>`;
        break;
      }
      case 'universe': {
        // The ONE place a universe is typed on this screen. The box shows
        // the DISPLAY number and the canonical wire value is kept beside
        // it — never re-derived from the box under a new base.
        const ua = UI.universeInputAttrs();
        wrap.innerHTML = `<div class="b5-field">
          <label class="b5-field__label" for="walkScopeUniverse">Universe (${escapeHtml(UI.universeBaseLabel())})</label>
          <input type="number" id="walkScopeUniverse" class="b5-input b5-input--mono" min="${ua.min}" max="${ua.max}" value="${escapeHtml(uni(scopeUniverseCanonical))}">
          <span class="b5-field__hint" id="walkScopeUniverseHint"></span>
        </div>`;
        const inp = document.getElementById('walkScopeUniverse');
        inp.addEventListener('input', () => {
          scopeUniverseCanonical = UI.parseUniverse(inp.value);
          syncScopeUniverseHint();
        });
        syncScopeUniverseHint();
        break;
      }
      case 'class':
        wrap.innerHTML = `<div class="b5-field"><label class="b5-field__label" for="walkScopeClass">Class</label><select id="walkScopeClass" class="b5-select">
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

  function syncScopeUniverseHint() {
    const hint = document.getElementById('walkScopeUniverseHint');
    if (!hint) return;
    hint.textContent =
      `Universe ${uni(scopeUniverseCanonical)} is Art-Net Port-Address ${scopeUniverseCanonical} on the wire — ` +
      `the same universe Patch, Nodes and Devices call ${uni(scopeUniverseCanonical)}.`;
  }

  async function startWalk() {
    let scopeValue = '';
    switch (scopeKind) {
      case 'node': scopeValue = (document.getElementById('walkScopeNode') || {}).value || ''; break;
      case 'port': scopeValue = (document.getElementById('walkScopePort') || {}).value || ''; break;
      // The wire always gets the CANONICAL 0-based Port-Address, never what
      // the box says.
      case 'universe': scopeValue = String(scopeUniverseCanonical); break;
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
    const doneCount = sum.confirmed + sum.problems;
    const pct = sum.total ? Math.round((doneCount / sum.total) * 100) : 0;

    el.innerHTML = `
      <div class="b5-modebar">
        ${UI.icon('identify')}
        <span class="b5-modebar__label">Rig Walk is running. One fixture is flashing at a time; leaving this screen turns Identify off.</span>
        <button id="walkAllOffBtn" class="b5-btn b5-btn--danger">${UI.icon('status-warning')}Identify off / all off</button>
        <button id="walkEndBtn" class="b5-btn b5-btn--ghost">End walk</button>
      </div>

      <div class="b5-shell--centered">
        <section class="b5-step-section" aria-labelledby="walkProgressHead">
          <h2 class="b5-step-section__head" id="walkProgressHead">
            <span class="b5-step-num">1</span> Where you are
            <span class="b5-step-section__note">${sum.remaining} still to visit of ${sum.total}</span>
          </h2>
          <div class="b5-counter">
            <div class="b5-counter__value">${dev ? (idx + 1) + ' of ' + devices.length : devices.length + ' of ' + devices.length}</div>
            <div class="b5-counter__label">${dev ? escapeHtml(dev.model || dev.uid) : 'Walk complete'}</div>
          </div>
          <div class="b5-progress" style="margin-top:10px"><div class="b5-progress__fill" style="width:${pct}%"></div></div>
          <div class="b5-tally">
            <div class="b5-tally__item b5-tally__item--confirmed"><span class="b5-tally__value">${sum.confirmed}</span><span class="b5-tally__label">Confirmed</span></div>
            <div class="b5-tally__item b5-tally__item--problem"><span class="b5-tally__value">${sum.problems}</span><span class="b5-tally__label">Problem</span></div>
            <div class="b5-tally__item"><span class="b5-tally__value">${sum.remaining}</span><span class="b5-tally__label">Remaining</span></div>
          </div>
        </section>

        <section class="b5-step-section" aria-labelledby="walkDeviceHead">
          <h2 class="b5-step-section__head" id="walkDeviceHead">
            <span class="b5-step-num">2</span> The fixture in front of you
            <span class="b5-step-section__note">${dev ? 'this one is flashing now' : 'nothing left to visit'}</span>
          </h2>
          ${dev ? renderDeviceCard(dev, idx, devices.length) : `<div class="b5-empty">${UI.icon('status-ok')}<span class="b5-empty__title">Walk complete</span><span class="b5-empty__body">Every device has been visited. Export below, or End walk to start a new one.</span></div>`}
          ${dev ? renderDeviceAccordion(dev) : ''}
        </section>

        ${dev ? `
        <section class="b5-step-section" aria-labelledby="walkVerdictHead">
          <h2 class="b5-step-section__head" id="walkVerdictHead">
            <span class="b5-step-num">3</span> What you found
            <span class="b5-step-section__note">recorded against this fixture only</span>
          </h2>
          <div class="b5-row" style="gap:12px">
            <button id="walkConfirmBtn" class="b5-btn b5-btn--walk b5-btn--confirm">${UI.icon('status-ok')}Confirmed</button>
            <button id="walkProblemBtn" class="b5-btn b5-btn--walk b5-btn--danger">${UI.icon('status-warning')}Problem</button>
          </div>
          <div id="walkProblemNoteWrap" class="b5-field__row" style="display:${showProblemNote ? 'flex' : 'none'};margin-top:var(--b5-space-3)">
            <label class="b5-visually-hidden" for="walkProblemNote">What is wrong with this fixture</label>
            <input id="walkProblemNote" class="b5-input" type="text" placeholder="what's wrong? (optional)" maxlength="120">
            <span class="b5-field__actions">
              <button id="walkProblemSubmit" class="b5-btn b5-btn--primary">${UI.icon('apply')}Save problem</button>
              <button id="walkProblemCancel" class="b5-btn b5-btn--ghost">Cancel</button>
            </span>
          </div>
          <label class="b5-choicecard ${session.autoAdvance ? 'is-on' : ''}" style="margin-top:var(--b5-space-3)">
            <input type="checkbox" id="walkAutoAdvanceToggle" ${session.autoAdvance ? 'checked' : ''}>
            <span class="b5-choicecard__box" aria-hidden="true">${session.autoAdvance ? UI.icon('status-ok') : ''}</span>
            <span>
              <span class="b5-choicecard__title">Auto-advance after Confirmed &mdash; ${session.autoAdvance ? 'ON' : 'OFF'}</span>
              <span class="b5-choicecard__body">${session.autoAdvance
                ? 'Pressing Confirmed moves straight to the next fixture and flashes it. Turn this off to stay put after confirming.'
                : 'Pressing Confirmed leaves you on this fixture. Turn this on to be moved to the next one automatically.'}</span>
            </span>
          </label>
        </section>` : ''}

        <section class="b5-step-section" aria-labelledby="walkExportHead">
          <h2 class="b5-step-section__head" id="walkExportHead">
            <span class="b5-step-num">4</span> Hand it over
            <span class="b5-step-section__note">${sum.confirmed} confirmed · ${sum.problems} problem${sum.problems === 1 ? '' : 's'} so far</span>
          </h2>
          <div class="b5-row">
            <button id="walkExportJsonBtn" class="b5-btn">${UI.icon('export')}Export JSON</button>
            <button id="walkExportTxtBtn" class="b5-btn">${UI.icon('export')}Export TXT</button>
          </div>
          <span class="b5-text-muted b5-text-sm" id="walkStatusMsg">${escapeHtml(statusMsg)}</span>
        </section>
      </div>

      <div class="b5-walk-bar">
        <button id="walkPrevBtn" class="b5-btn b5-btn--walk b5-btn--secondary" ${idx <= 0 ? 'disabled' : ''}>&larr; Previous</button>
        <button id="walkNextBtn" class="b5-btn b5-btn--walk b5-btn--primary" ${(idx < 0 || idx >= devices.length - 1) ? 'disabled' : ''}>Next &rarr;</button>
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

  // renderDeviceCard is the core "walk one fixture" view: identity in
  // descending size — model/type, manufacturer, DMX universe/address (the
  // two things a tech reads most), UID, node+port smallest. Status is never
  // color-only: every state pairs a b5-badge color with its own text. The
  // full device panel (accordion) renders separately, below.
  // STATUS_TONE / STATUS_WORD: the walk session's own status enum mapped
  // onto the kit's tone words in ONE table, the way reconcile.js's DIFF_TONE
  // does. A tone says how alarmed to be; the word says what happened, and
  // the word always ships (a pill is never icon-only).
  const STATUS_TONE = { confirmed: 'ok', problem: 'danger' };
  const STATUS_ICON = { confirmed: 'status-ok', problem: 'status-error' };
  const STATUS_WORD = { confirmed: 'Confirmed', problem: 'Problem' };

  function renderDeviceCard(dev, idx, total) {
    const addr = Api.formatAddressRange(dev.dmxStartAddress, dev.dmxFootprint, dev.addressKnown);
    const tone = STATUS_TONE[dev.status] || 'open';
    const ic = STATUS_ICON[dev.status] || 'status-pending';
    const word = STATUS_WORD[dev.status] || 'Not visited yet';
    const cls = dev.status === 'confirmed' ? 'is-ok' : dev.status === 'problem' ? 'is-danger' : 'is-open';
    const identifyBlock = dev.identifyErr
      ? `<div class="b5-alert b5-alert--danger">${UI.icon('status-error')}<div><p class="b5-alert__title">Identify failed &mdash; this fixture is NOT flashing</p><p class="b5-alert__body">${escapeHtml(dev.identifyErr)}</p><button id="walkRetryIdentifyBtn" class="b5-btn" style="margin-top:var(--b5-space-2)">${UI.icon('refresh')}Retry identify</button></div></div>`
      : (dev.identifyOn
        ? `<span class="b5-pill b5-pill--md b5-pill--accent b5-pill--solid">${UI.icon('identify')}Flashing now</span>`
        // Neither an error nor a success: the server has not said this one
        // is identifying. Say that, rather than leaving a silent gap that
        // reads as "it must be on".
        : `<span class="b5-pill b5-pill--md b5-pill--unread">${UI.icon('status-pending')}Not reported as flashing</span>`);

    // Universe: the raw wire Port-Address, through UI.formatUniverse, once,
    // here. Address: through Api.formatAddressRange, which prints "—" when
    // addressKnown is false rather than a plausible 0 — so the meta line
    // says so in words instead.
    const meta = [
      `${escapeHtml(host_universeLabel())} ${escapeHtml(uni(dev.portAddress))}`,
      dev.addressKnown ? `addr ${escapeHtml(addr)}` : 'addr unknown — this fixture has not reported one',
      escapeHtml(dev.manufacturer || 'manufacturer unknown'),
    ].join(' · ');

    return `
      <article class="b5-statecard ${cls}">
        <div class="b5-statecard__top">
          <span class="b5-linkbadge">${idx + 1}</span>
          <div class="b5-statecard__id">
            <strong class="b5-statecard__name">${escapeHtml(dev.model || 'model unknown')}</strong>
            <span class="b5-statecard__meta">${meta}</span>
          </div>
        </div>
        <div class="b5-statecard__state">
          <span class="b5-pill b5-pill--md b5-pill--${tone}">${UI.icon(ic)}${escapeHtml(word)}</span>
          ${identifyBlock}
          <span class="b5-text-mono b5-text-xs">UID ${escapeHtml(dev.uid)}</span>
        </div>
        <p class="b5-caption">${escapeHtml(dev.nodeIp)} bind ${dev.bindIndex} &middot; ${escapeHtml(host_universeLabel())} ${escapeHtml(uni(dev.portAddress))} (Art-Net Port-Address ${dev.portAddress} on the wire)</p>
        ${dev.note ? `<p class="b5-note">Note: ${escapeHtml(dev.note)}</p>` : ''}
        <div id="walkAddrFieldWrap"></div>
      </article>
    `;
  }

  // host_universeLabel: the word this install uses for a universe, kept in
  // one place so the card, the caption and the scope picker cannot drift.
  function host_universeLabel() { return 'Universe'; }

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
      <div class="b5-accordion" id="walkAccordion" style="margin-top:var(--b5-space-4)">
        ${SECTIONS.map(s => `
          <div class="b5-accordion__item">
            <button class="b5-accordion__trigger${expandedSections[s.key] ? ' is-open' : ''}" data-section="${s.key}" aria-expanded="${expandedSections[s.key] ? 'true' : 'false'}">
              <span>${escapeHtml(s.label)}</span>${UI.icon('chevron-expand')}
            </button>
            <div class="b5-accordion__panel" data-section-body="${s.key}" style="display:${expandedSections[s.key] ? '' : 'none'};"></div>
          </div>
        `).join('')}
      </div>
    `;
  }

  function wireAccordionHandlers(dev) {
    const root = document.getElementById('walkAccordion');
    if (!root) return;
    const f = toDetailFixture(dev);

    root.querySelectorAll('.b5-accordion__trigger').forEach(btn => {
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
      renderAccordionSectionBody(root, s.key, f);
    });
  }

  // renderAccordionSectionBody renders one section's body into its
  // accordion panel — the one place that maps a section key to its
  // DeviceDetail render function, shared by wireAccordionHandlers (initial/
  // full draw) and updateAccordionSection (a single section's targeted
  // redraw, see DeviceDetail.init's callback above).
  function renderAccordionSectionBody(root, key, f) {
    const body = root.querySelector(`[data-section-body="${key}"]`);
    if (!body) return false;
    switch (key) {
      case 'info': DeviceDetail.renderInfoSection(body, f); break;
      case 'params': DeviceDetail.renderParamsSection(body, f, setWalkStatus, { hideAddressField: true }); break;
      case 'sensors': DeviceDetail.renderSensorsSection(body, f); break;
      case 'status': DeviceDetail.renderStatusSection(body, f); break;
    }
    return true;
  }

  // updateAccordionSection redraws just one expanded accordion section in
  // place, without touching the rest of the walk screen (the walk progress
  // header, the other accordion sections, the problem-note field, etc.) —
  // see DeviceDetail.init's callback for why this matters. Returns false
  // (asking the caller to fall back to a full render()) when the accordion
  // DOM isn't there to update, e.g. the very first render for a device.
  function updateAccordionSection(key) {
    const root = document.getElementById('walkAccordion');
    if (!root) return false;
    const dev = currentDevice();
    if (!dev) return false;
    return renderAccordionSectionBody(root, key, toDetailFixture(dev));
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
    // generic params-endpoint apply wouldn't do on its own. Built with the
    // shared UI.buildApplyField/UI.wireApplyField (ui.js) — same
    // oninput-stages/Apply-commits contract every field in the app uses.
    const addrWrap = document.getElementById('walkAddrFieldWrap');
    if (addrWrap) {
      const baseline = dev.addressKnown ? String(dev.dmxStartAddress) : '';
      const field = UI.buildApplyField({
        label: 'DMX start address', kind: 'number', mono: true, min: 1, max: 512,
        value: dev.addressKnown ? dev.dmxStartAddress : '', enabled: true,
      });
      addrWrap.appendChild(field.wrap);
      UI.wireApplyField(field, baseline, async (v) => {
        const n = Number(v);
        if (!Number.isFinite(n) || n < 1 || n > 512) throw new Error('address must be 1-512');
        await Api.walkSetAddress(dev.uid, n);
        statusMsg = 'address applied';
        await refresh();
      }, setWalkStatus);
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
