// analyzer.js — Analyzer screen: "All traffic" (live packet feed via WS
// "capture" batches, throttled server-side to ~10 fps) and a dedicated
// "RDM" sub-view (report task item 3) reading from the RDM-only capture
// ring — filterable by UID/PID/command class/direction, with a
// request/response paired display (matched by TN+UID, round-trip time) and
// full hex on expand. Both views share the live "capture" WS feed for
// near-real-time updates; the RDM view additionally supports an explicit
// Refresh (it pulls the full server-side filtered snapshot, which the
// WS-trickle can't reconstruct after a filter change).
//
// 2026-09 screen-kit conversion (css/DESIGN.md). Three things changed and
// nothing else did:
//
//  1. The screen is built HERE, not in index.html, so it can be expressed in
//     the kit's vocabulary: numbered .b5-step-section bands top to bottom,
//     .b5-toolbar instead of the older desktop-first .b5-filterbar, and each
//     packet table inside its own overflow-x box (rule 4 — the page body
//     never scrolls sideways). Every element id the old markup carried is
//     kept, byte for byte, because that is the entire contract between this
//     file and the rest of the app.
//
//     Both tables STAY tables: DESIGN.md names the Analyzer's packet list as
//     the case where a table is the right answer ("genuinely tabular data a
//     tech scans down") — they keep .b5-table--responsive + data-label on
//     every cell so they stack on a narrow pane.
//
//  2. UNIVERSE NUMBERS GO THROUGH UI.formatUniverse / UI.parseUniverse
//     (rule 5). This file had SIX universe references and ZERO conversions —
//     the same defect class the owner reported off a live bench on the Nodes
//     screen, where every universe read one lower than his gateway
//     faceplate. Two of the six were real, user-facing defects:
//
//       - the "Univ" column printed the raw 0-based wire number, so a packet
//         on the faceplate's "Universe 1" showed as "0";
//       - the universe filter box read the tech's typed number as a wire
//         value, so typing the number written on the gateway filtered the
//         WRONG universe — and silently returned nothing, which at a bench
//         is indistinguishable from "no traffic".
//
//     The other four are not user-facing universes and are deliberately left
//     raw: `filterUniverse` (wire-side state), the `e.Universe !==
//     filterUniverse` comparison (wire vs wire), the id string
//     'filterUniverse', and the empty-state sentence, which contains the
//     word but no number. Converting those would have been the mirror-image
//     bug.
//
//     The column also no longer prints '' for universe 0. A packet kind
//     that carries no universe at all says "no universe"; one that carries
//     a universe the capture did not extract says "universe not recorded".
//     Both are words, per rule 3, and the discriminator is the packet KIND,
//     not a null check — see UNIVERSE_BEARING_KINDS below for why a null
//     check cannot work here.
//
//  3. Row expansion is a real <button>, not a click handler on a <tr>. Same
//     action, same alert(), now keyboard-reachable and a full touch target.
const AnalyzerScreen = (() => {
  const MAX_ROWS = 500;
  let rows = [];
  let paused = false;
  let filterKind = '';
  // filterUniverse: the 0-BASED WIRE universe to match against e.Universe.
  // Never what the tech typed — UI.parseUniverse converts at the input
  // boundary, exactly once, in wireUniverseFilter below.
  let filterUniverse = null;

  let activeView = 'all'; // 'all' | 'rdm'
  let rdmRows = [];       // flat entries from the RDM-only ring
  let rdmFilters = { uid: '', pid: '', cc: '', dir: '' };
  let rdmPaired = true;

  function matches(e) {
    if (filterKind && e.Kind !== filterKind) return false;
    if (filterUniverse !== null && e.Universe !== filterUniverse) return false;
    return true;
  }

  function onBatch(msg) {
    if (paused) return;
    const batch = msg.Batch || msg.batch || [];
    const allBatch = batch.filter(matches);
    if (allBatch.length) {
      rows = rows.concat(allBatch).slice(-MAX_ROWS);
      if (activeView === 'all') renderAll();
    }
    const rdmBatch = batch.filter(e => e.RDM || e.Tod);
    if (rdmBatch.length && rdmMatchesFilters(rdmBatch).length) {
      rdmRows = rdmRows.concat(rdmMatchesFilters(rdmBatch)).slice(-MAX_ROWS);
      if (activeView === 'rdm') renderRDM();
    }
  }

  function rdmMatchesFilters(entries) {
    return entries.filter(e => {
      if (rdmFilters.uid && !(e.RDM && (e.RDM.sourceUid === rdmFilters.uid || e.RDM.destUid === rdmFilters.uid))) return false;
      if (rdmFilters.pid && !(e.RDM && e.RDM.pid === parseInt(rdmFilters.pid, 16))) return false;
      if (rdmFilters.cc && !(e.RDM && e.RDM.commandClass === rdmFilters.cc)) return false;
      if (rdmFilters.dir === 'out' && e.Dir !== 1) return false;
      if (rdmFilters.dir === 'in' && e.Dir !== 0) return false;
      return true;
    });
  }

  // UNIVERSE_BEARING_KINDS — the packet kinds for which capture.Entry's
  // Universe field actually holds a Port-Address.
  //
  // This is NOT a cosmetic list. capture.Entry.Universe is a plain uint16
  // with no `omitempty` and no pointer (internal/capture/capture.go: "raw
  // Port-Address value, when applicable; 0 otherwise"), and
  // capture.summarize() fills it from pkt.Dmx.PortAddress().RawValue() for
  // artnet.KindDmx and returns a literal 0 for every other kind. So the
  // JSON that reaches this file says "Universe": 0 for an ArtPoll exactly
  // as loudly as it does for a genuine ArtDmx on Port-Address 0 — the
  // absence is not representable on the wire, and a JS `undefined`/`null`
  // check can never fire.
  //
  // That matters because of the display base: under the default 1-based
  // notation UI.formatUniverse(0) is the string "1", so testing for absence
  // the impossible way makes every ArtPoll, ArtPollReply and ArtRdm row in
  // the feed claim to be traffic on the tech's Universe 1. That is rule 3's
  // "plausible 0" failure with a base offset stacked on top of it: at a
  // bench, a screen full of rows reading "Universe 1" is indistinguishable
  // from a genuine flood on universe 1, which is precisely the diagnosis
  // the Analyzer exists to make. The kind is the only honest discriminator
  // available, so it is what we use.
  const UNIVERSE_BEARING_KINDS = { ArtDmx: true };

  // KINDS_WITH_UNRECORDED_UNIVERSE — kinds that DO carry a Port-Address on
  // the wire but whose universe capture.summarize() does not extract. These
  // must not say "no universe" (that would be a false statement about the
  // packet); they say the capture did not record one, which is the truth.
  const KINDS_WITH_UNRECORDED_UNIVERSE = {
    ArtRdm: true, ArtRdmSub: true, ArtTodRequest: true,
    ArtTodData: true, ArtTodControl: true, ArtAddress: true, ArtInput: true,
  };

  // universeCell: rule 5 + rule 3 in one place. A packet that carries a
  // universe is shown at the display base the tech's gateway faceplate uses;
  // one that carries none says so in words; one that carries one the capture
  // did not keep says THAT, in different words. Three distinct truths, never
  // collapsed into a number.
  function universeCell(e) {
    if (UNIVERSE_BEARING_KINDS[e.Kind]) {
      if (e.Universe === undefined || e.Universe === null) {
        return '<span class="b5-text-muted">universe not recorded</span>';
      }
      return escapeHtml(UI.formatUniverse(e.Universe));
    }
    if (KINDS_WITH_UNRECORDED_UNIVERSE[e.Kind]) {
      return '<span class="b5-text-muted">universe not recorded</span>';
    }
    return '<span class="b5-text-muted">no universe</span>';
  }

  function dirPill(dir) {
    // Direction is a state: word first, icon second, tone last (rule 1).
    //
    // capture.Direction has THREE values, not two: DirIn(0), DirOut(1) and
    // DirNote(2), the last of which marks an entry that "is not a datagram
    // at all" (internal/capture/capture.go) — a controller note with Size 0
    // and no Peer. Folding it into the `in` branch would put a confident
    // blue "in" pill on a row that never touched the wire, which is a state
    // the app would be inventing. It gets its own word and NO tone: an
    // untoned pill is honest, and DESIGN.md forbids borrowing a
    // neighbouring state's colour.
    if (dir === 1) return `<span class="b5-pill b5-pill--tag b5-pill--accent">${UI.icon('export')}out</span>`;
    if (dir === 0) return `<span class="b5-pill b5-pill--tag b5-pill--info">${UI.icon('signal')}in</span>`;
    return `<span class="b5-pill b5-pill--tag">${UI.icon('status-pending')}note</span>`;
  }

  function renderAll() {
    const tbody = document.querySelector('#analyzerTable tbody');
    if (!tbody) return;
    const box = tbody.closest('.b5-scrollbox') || tbody.parentElement;
    const nearBottom = box.scrollTop + box.clientHeight >= box.scrollHeight - 10;
    tbody.innerHTML = '';
    const countEl = document.getElementById('analyzerAllCount');
    if (countEl) countEl.textContent = rows.length ? `${rows.length} packet${rows.length === 1 ? '' : 's'} held` : 'nothing captured yet';
    if (!rows.length) {
      tbody.innerHTML = `<tr><td colspan="8"><div class="b5-empty">${UI.icon('nav-analyzer')}<span class="b5-empty__title">No traffic captured</span><span class="b5-empty__body">Start a capture on an active universe to see packets here. If a filter above is set, it may be hiding everything that arrived.</span></div></td></tr>`;
      return;
    }
    rows.forEach(e => {
      const tr = document.createElement('tr');
      const t = e.Time ? new Date(e.Time).toLocaleTimeString() : '';
      const expandBtn = e.Hex
        ? `<button type="button" class="b5-btn b5-btn--ghost btn-hex">${UI.icon('chevron-expand')}Full hex</button>`
        : '<span class="b5-text-muted b5-text-sm">no hex captured</span>';
      tr.innerHTML = `
        <td data-label="Time" class="b5-table__mono">${escapeHtml(t)}</td>
        <td data-label="Direction">${dirPill(e.Dir)}</td>
        <td data-label="Kind">${UI.tag(e.Kind)}</td>
        <td data-label="Source" class="b5-table__mono">${e.Peer ? escapeHtml(String(e.Peer).split(':')[0]) : '<span class="b5-text-muted">source unknown</span>'}</td>
        <td data-label="Universe" class="b5-table__mono">${universeCell(e)}</td>
        <td data-label="Size">${escapeHtml(String(e.Size))}</td>
        <td data-label="Key fields" class="b5-text-mono">${escapeHtml(e.Key)}</td>
        <td data-label="Detail">${expandBtn}</td>
      `;
      const btn = tr.querySelector('.btn-hex');
      if (btn) btn.addEventListener('click', () => { alert(e.Hex); });
      tbody.appendChild(tr);
    });
    if (nearBottom) box.scrollTop = box.scrollHeight;
  }

  // --- RDM-focused view ------------------------------------------------------

  async function refreshRDM() {
    const status = document.getElementById('rdmAnalyzerStatus');
    if (status) status.innerHTML = UI.spinner() + 'loading…';
    try {
      const params = { uid: rdmFilters.uid, pid: rdmFilters.pid, cc: rdmFilters.cc, dir: rdmFilters.dir };
      rdmRows = await Api.rdmCaptureSnapshot(params);
      if (status) status.textContent = `${rdmRows.length} entr${rdmRows.length === 1 ? 'y' : 'ies'}`;
      renderRDM();
    } catch (e) {
      if (status) status.innerHTML = `${UI.icon('status-error')}error: ${escapeHtml(e.message)}`;
    }
  }

  // pairEntries mirrors internal/web/capture_export.go's pairExchanges: a
  // request's key is (TN, destUid), a response's key is (TN, sourceUid) —
  // matching keys are one request/response pair.
  function pairEntries(entries) {
    const pending = new Map();
    const order = [];
    entries.forEach(e => {
      if (!e.RDM) { order.push({ request: e }); return; }
      if (!e.RDM.isResponse) {
        const ex = { request: e };
        pending.set(e.RDM.transactionNumber + '|' + e.RDM.destUid, ex);
        order.push(ex);
        return;
      }
      const k = e.RDM.transactionNumber + '|' + e.RDM.sourceUid;
      const ex = pending.get(k);
      if (ex && !ex.response) {
        ex.response = e;
        pending.delete(k);
        return;
      }
      order.push({ response: e });
    });
    return order;
  }

  function renderRDM() {
    const tbody = document.querySelector('#rdmAnalyzerTable tbody');
    if (!tbody) return;
    tbody.innerHTML = '';
    const filtered = rdmMatchesFilters(rdmRows);
    const items = rdmPaired ? pairEntries(filtered) : filtered.map(e => ({ request: e.RDM && !e.RDM.isResponse ? e : null, response: e.RDM && e.RDM.isResponse ? e : e }));

    if (!items.length) {
      tbody.innerHTML = `<tr><td colspan="10"><div class="b5-empty">${UI.icon('nav-analyzer')}<span class="b5-empty__title">No RDM traffic captured</span><span class="b5-empty__body">Adjust the filters above, or start a capture with RDM activity on the network.</span></div></td></tr>`;
      return;
    }
    items.forEach(item => {
      const primary = item.request || item.response;
      if (!primary) return;
      appendRDMRow(tbody, item.request, item.response);
    });
  }

  function appendRDMRow(tbody, req, resp) {
    const e = req || resp;
    const d = e.RDM;
    const tr = document.createElement('tr');
    if (!d) {
      // ToD or decode-error entry.
      const t = e.Time ? new Date(e.Time).toLocaleTimeString() : '';
      tr.innerHTML = `<td data-label="Time" class="b5-table__mono">${escapeHtml(t)}</td><td data-label="Direction">${dirPill(e.Dir)}</td><td data-label="Decoded" colspan="7">${escapeHtml(e.Kind)} ${e.Tod ? escapeHtml(JSON.stringify(e.Tod).slice(0, 120)) : escapeHtml(e.Key || '')}</td><td data-label="Detail"></td>`;
      wireExpand(tr, e, null);
      tbody.appendChild(tr);
      return;
    }
    const t = req ? new Date(req.Time).toLocaleTimeString() : (resp ? new Date(resp.Time).toLocaleTimeString() : '');
    const uidPair = `${escapeHtml(d.sourceUid || '')} → ${escapeHtml(d.destUid || '')}`;
    const rtt = (req && resp) ? `${(new Date(resp.Time) - new Date(req.Time))}ms` : '<span class="b5-text-muted">not measured</span>';
    // The response state is a state: it gets a worded pill, so "no response"
    // survives greyscale instead of being a grey sentence among black ones.
    let respLabel;
    if (resp) {
      const rtName = resp.RDM.responseType || 'response';
      const nack = resp.RDM.responseType === 'NACK_REASON';
      respLabel = `<span class="b5-pill b5-pill--tag ${nack ? 'b5-pill--danger' : 'b5-pill--ok'}">${UI.icon(nack ? 'status-error' : 'status-ok')}${escapeHtml(rtName)}${nack ? ` (${escapeHtml(resp.RDM.nackReasonName || '')})` : ''}</span>`;
    } else if (req) {
      respLabel = `<span class="b5-pill b5-pill--tag b5-pill--unread">${UI.icon('status-pending')}No response captured</span>`;
    } else {
      respLabel = '';
    }
    const decoded = (resp && resp.RDM.decoded) || (req && req.RDM.decoded) || '';
    tr.innerHTML = `
      <td data-label="Time" class="b5-table__mono">${escapeHtml(t)}</td>
      <td data-label="Direction">${req ? dirPill(1) : ''}${resp && req ? ' ' : ''}${resp ? dirPill(0) : ''}</td>
      <td data-label="UID (src → dst)" class="b5-table__mono">${uidPair}</td>
      <td data-label="TN">${escapeHtml(String(d.transactionNumber))}</td>
      <td data-label="CC">${escapeHtml(d.commandClass)}</td>
      <td data-label="PID" class="b5-table__mono">0x${(d.pid || 0).toString(16).toUpperCase().padStart(4, '0')} ${escapeHtml(d.pidName || '')}</td>
      <td data-label="Response">${respLabel}</td>
      <td data-label="Round trip">${rtt}</td>
      <td data-label="Decoded" class="b5-text-mono b5-text-xs">${decoded ? escapeHtml(decoded) : '<span class="b5-text-muted">not decoded</span>'}</td>
      <td data-label="Detail"><button type="button" class="b5-btn b5-btn--ghost btn-hex">${UI.icon('chevron-expand')}Full hex</button></td>
    `;
    wireExpand(tr, req, resp);
    tbody.appendChild(tr);
  }

  // wireExpand: same alert() as before, now hung off a real <button> in the
  // row's Detail cell rather than the <tr> itself — a clickable <tr> is not
  // keyboard-reachable and has no accessible name.
  function wireExpand(tr, req, resp) {
    const btn = tr.querySelector('.btn-hex');
    if (!btn) return;
    btn.addEventListener('click', () => {
      const lines = [];
      if (req) lines.push('REQUEST:\n' + describeEntry(req));
      if (resp) lines.push('RESPONSE:\n' + describeEntry(resp));
      if (!req && !resp) return;
      alert(lines.join('\n\n'));
    });
  }

  function describeEntry(e) {
    const parts = [`kind=${e.Kind} dir=${e.Dir === 1 ? 'out' : 'in'} time=${e.Time}`];
    if (e.RDM) {
      parts.push(`cc=${e.RDM.commandClass} pid=0x${(e.RDM.pid || 0).toString(16)} (${e.RDM.pidName})`);
      if (e.RDM.decoded) parts.push('decoded: ' + e.RDM.decoded);
      if (e.RDM.decodeError) parts.push('DECODE ERROR: ' + e.RDM.decodeError);
      parts.push('paramData: ' + (e.RDM.paramDataHex || '(none)'));
    }
    if (e.Tod) parts.push('tod: ' + JSON.stringify(e.Tod));
    parts.push('hex: ' + (e.Hex || '(omitted)'));
    return parts.join('\n');
  }

  // --- export ------------------------------------------------------------

  function exportParams() {
    return { uid: rdmFilters.uid, pid: rdmFilters.pid, cc: rdmFilters.cc, dir: rdmFilters.dir };
  }

  function doExport(format) {
    window.open(Api.exportUrl(format, exportParams()), '_blank');
  }

  // --- markup ------------------------------------------------------------
  // Built here rather than in index.html so the screen can use the kit. Every
  // id below existed in the old markup and is unchanged.

  function screenHtml() {
    const uni = UI.universeInputAttrs();
    return `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Analyzer</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">What is actually on the wire, as it arrives.</span>
      </div>

      <div class="b5-tabs">
        <div class="b5-tabs__list" id="analyzerViewTabs" role="tablist">
          <button class="b5-tabs__tab detail-tab-btn" data-view="all" role="tab">${UI.icon('nav-analyzer')}All traffic</button>
          <button class="b5-tabs__tab detail-tab-btn" data-view="rdm" role="tab">${UI.icon('nav-devices')}RDM</button>
        </div>
      </div>

      <div id="analyzerAllView">
        <section class="b5-step-section" aria-labelledby="anAllFilterHead">
          <h2 class="b5-step-section__head" id="anAllFilterHead">
            <span class="b5-step-num">1</span> What to capture
            <span class="b5-step-section__note">narrows what is kept — nothing already on screen is re-filtered</span>
          </h2>
          <div class="b5-toolbar">
            <div class="b5-toolbar__row">
              <button id="btnPause" class="b5-btn" aria-pressed="false">${UI.icon('status-pending')}<span id="btnPauseLabel">Pause the feed</span></button>
              <label class="b5-visually-hidden" for="filterKind">Packet kind</label>
              <select id="filterKind" class="b5-select">
                <option value="">Kind: any</option>
                <option value="ArtDmx">ArtDmx</option>
                <option value="ArtPoll">ArtPoll</option>
                <option value="ArtPollReply">ArtPollReply</option>
                <option value="ArtRdm">ArtRdm</option>
                <option value="ArtTodData">ArtTodData</option>
                <option value="ArtTodControl">ArtTodControl</option>
              </select>
              <label class="b5-visually-hidden" for="filterUniverse">Universe</label>
              <input type="number" id="filterUniverse" class="b5-input"
                     min="${uni.min}" max="${uni.max}" placeholder="Universe: any">
              <button id="btnClearAnalyzer" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Clear the list</button>
            </div>
            <p class="b5-caption" id="analyzerUniverseHint">Universe numbers here are ${escapeHtml(UI.universeBaseLabel())} — the same numbering as Devices, Nodes and Patch.</p>
          </div>
        </section>

        <section class="b5-step-section" aria-labelledby="anAllTableHead">
          <h2 class="b5-step-section__head" id="anAllTableHead">
            <span class="b5-step-num">2</span> Packets seen
            <span class="b5-step-section__note" id="analyzerAllCount">nothing captured yet</span>
          </h2>
          <div class="b5-panel">
            <div class="b5-panel__body--flush">
              <div class="b5-scrollbox" style="max-height:60vh">
                <table class="b5-table b5-table--responsive" id="analyzerTable">
                  <thead><tr>
                    <th>Time</th><th>Direction</th><th>Kind</th><th>Source</th><th>Universe</th><th>Size</th><th>Key fields</th><th>Detail</th>
                  </tr></thead>
                  <tbody></tbody>
                </table>
              </div>
            </div>
          </div>
        </section>
      </div>

      <div id="analyzerRDMView" style="display:none;">
        <section class="b5-step-section" aria-labelledby="anRdmFilterHead">
          <h2 class="b5-step-section__head" id="anRdmFilterHead">
            <span class="b5-step-num">1</span> Which RDM traffic
            <span class="b5-step-section__note" id="rdmAnalyzerStatus">not loaded yet</span>
          </h2>
          <div class="b5-toolbar">
            <div class="b5-toolbar__row">
              <label class="b5-visually-hidden" for="rdmFilterUid">UID</label>
              <input type="text" id="rdmFilterUid" class="b5-input b5-input--mono" placeholder="UID e.g. 2222:00000001">
              <label class="b5-visually-hidden" for="rdmFilterPid">PID</label>
              <input type="text" id="rdmFilterPid" class="b5-input b5-input--mono" placeholder="PID hex e.g. 0060" maxlength="4">
              <label class="b5-visually-hidden" for="rdmFilterCC">Command class</label>
              <select id="rdmFilterCC" class="b5-select">
                <option value="">CC: any</option>
                <option value="GET_COMMAND">GET_COMMAND</option>
                <option value="GET_COMMAND_RESPONSE">GET_COMMAND_RESPONSE</option>
                <option value="SET_COMMAND">SET_COMMAND</option>
                <option value="SET_COMMAND_RESPONSE">SET_COMMAND_RESPONSE</option>
                <option value="DISCOVERY_COMMAND">DISCOVERY_COMMAND</option>
                <option value="DISCOVERY_COMMAND_RESPONSE">DISCOVERY_COMMAND_RESPONSE</option>
              </select>
              <label class="b5-visually-hidden" for="rdmFilterDir">Direction</label>
              <select id="rdmFilterDir" class="b5-select">
                <option value="">Direction: any</option>
                <option value="out">out (controller → device)</option>
                <option value="in">in (device → controller)</option>
              </select>
              <button id="btnRDMRefresh" class="b5-btn">${UI.icon('refresh')}Refresh from the buffer</button>
            </div>
          </div>
          <label class="b5-choicecard is-on" id="rdmPairedCard">
            <input type="checkbox" id="rdmPairedView" checked>
            <span class="b5-choicecard__box" aria-hidden="true">${UI.icon('status-ok')}</span>
            <span>
              <span class="b5-choicecard__title">Pair each request with its response — ON</span>
              <span class="b5-choicecard__body">One line per exchange, with the round-trip time. Turn this off to see every packet on its own line in arrival order.</span>
            </span>
          </label>
        </section>

        <section class="b5-step-section" aria-labelledby="anRdmTableHead">
          <h2 class="b5-step-section__head" id="anRdmTableHead">
            <span class="b5-step-num">2</span> RDM exchanges
            <span class="b5-step-section__note">from the RDM-only buffer, which DMX traffic never evicts</span>
          </h2>
          <div class="b5-panel">
            <div class="b5-panel__body--flush">
              <div class="b5-scrollbox">
                <table class="b5-table b5-table--responsive" id="rdmAnalyzerTable">
                  <thead><tr>
                    <th>Time</th><th>Direction</th><th>UID (src &rarr; dst)</th><th>TN</th><th>CC</th><th>PID</th><th>Response</th><th>Round trip</th><th>Decoded</th><th>Detail</th>
                  </tr></thead>
                  <tbody></tbody>
                </table>
              </div>
            </div>
          </div>
        </section>

        <section class="b5-step-section" aria-labelledby="anRdmExportHead">
          <h2 class="b5-step-section__head" id="anRdmExportHead">
            <span class="b5-step-num">3</span> Take it away with you
            <span class="b5-step-section__note">scoped by the filters in step 1</span>
          </h2>
          <div class="b5-toolbar">
            <div class="b5-toolbar__row">
              <button id="btnExportRDMJson" class="b5-btn">${UI.icon('export')}Export JSON</button>
              <button id="btnExportRDMTxt" class="b5-btn">${UI.icon('export')}Export TXT</button>
            </div>
            <p class="b5-note">Exports the RDM-only buffer — the one DMX traffic never evicts — as it stands right now, narrowed by the UID, PID, command class and direction set above.</p>
          </div>
        </section>
      </div>
    `;
  }

  // --- view switching + init --------------------------------------------

  function switchView(view) {
    activeView = view;
    document.querySelectorAll('#analyzerViewTabs .detail-tab-btn').forEach(b => {
      const on = b.dataset.view === view;
      b.classList.toggle('is-active', on);
      b.setAttribute('aria-selected', on ? 'true' : 'false');
    });
    document.getElementById('analyzerAllView').style.display = view === 'all' ? '' : 'none';
    document.getElementById('analyzerRDMView').style.display = view === 'rdm' ? '' : 'none';
    if (view === 'rdm' && rdmRows.length === 0) refreshRDM();
  }

  // wireUniverseFilter: the ONE place a typed universe becomes a wire value.
  function wireUniverseFilter() {
    const inp = document.getElementById('filterUniverse');
    inp.addEventListener('change', () => {
      filterUniverse = inp.value === '' ? null : UI.parseUniverse(inp.value);
    });
  }

  // syncUniverseBase: the display base changed on Settings. The stored filter
  // is a wire value and does NOT move; what changes is how it is written in
  // the input, what min/max the input accepts, the hint sentence, and every
  // universe already painted in the table.
  function syncUniverseBase() {
    const inp = document.getElementById('filterUniverse');
    if (inp) {
      const uni = UI.universeInputAttrs();
      inp.min = uni.min;
      inp.max = uni.max;
      inp.value = filterUniverse === null ? '' : UI.formatUniverse(filterUniverse);
    }
    const hint = document.getElementById('analyzerUniverseHint');
    if (hint) hint.textContent = `Universe numbers here are ${UI.universeBaseLabel()} — the same numbering as Devices, Nodes and Patch.`;
    renderAll();
    renderRDM();
  }

  function setPaused(v) {
    paused = v;
    const btn = document.getElementById('btnPause');
    const lbl = document.getElementById('btnPauseLabel');
    if (!btn) return;
    btn.setAttribute('aria-pressed', paused ? 'true' : 'false');
    btn.classList.toggle('b5-btn--primary', paused);
    if (lbl) lbl.textContent = paused ? 'Paused — resume the feed' : 'Pause the feed';
  }

  function init() {
    const screen = document.getElementById('screen-analyzer');
    if (screen) screen.innerHTML = screenHtml();

    Live.on('capture', onBatch);
    document.getElementById('btnPause').addEventListener('click', () => setPaused(!paused));
    document.getElementById('btnClearAnalyzer').addEventListener('click', () => {
      rows = [];
      renderAll();
    });
    document.getElementById('filterKind').addEventListener('change', (e) => {
      filterKind = e.target.value;
    });
    wireUniverseFilter();
    window.addEventListener('b5-universe-base-changed', syncUniverseBase);

    document.querySelectorAll('#analyzerViewTabs .detail-tab-btn').forEach(btn => {
      btn.addEventListener('click', () => switchView(btn.dataset.view));
    });
    switchView('all');
    renderAll();

    document.getElementById('rdmFilterUid').addEventListener('change', e => { rdmFilters.uid = e.target.value.trim(); refreshRDM(); });
    document.getElementById('rdmFilterPid').addEventListener('change', e => { rdmFilters.pid = e.target.value.trim(); refreshRDM(); });
    document.getElementById('rdmFilterCC').addEventListener('change', e => { rdmFilters.cc = e.target.value; refreshRDM(); });
    document.getElementById('rdmFilterDir').addEventListener('change', e => { rdmFilters.dir = e.target.value; refreshRDM(); });
    document.getElementById('rdmPairedView').addEventListener('change', e => {
      rdmPaired = e.target.checked;
      const card = document.getElementById('rdmPairedCard');
      if (card) {
        card.classList.toggle('is-on', rdmPaired);
        card.querySelector('.b5-choicecard__title').textContent =
          `Pair each request with its response — ${rdmPaired ? 'ON' : 'OFF'}`;
      }
      renderRDM();
    });
    document.getElementById('btnRDMRefresh').addEventListener('click', refreshRDM);
    document.getElementById('btnExportRDMJson').addEventListener('click', () => doExport('json'));
    document.getElementById('btnExportRDMTxt').addEventListener('click', () => doExport('txt'));
  }

  // _test exposes the pure display helpers to the Node test harness. No
  // behavior of the screen depends on this object existing.
  return { init, _test: { universeCell, dirPill, pairEntries, setFilterUniverseFromInput: (v) => { filterUniverse = v === '' ? null : UI.parseUniverse(v); return filterUniverse; }, matches } };
})();
