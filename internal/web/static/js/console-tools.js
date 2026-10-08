// console-tools.js — the Console's TOOLS panel, chunk C7: the old Send
// screen's functions folded into the Console (owner decision 2026-10-06:
// "Don't merge with the other screens — fold their functions into this
// one"). console.js mounts it in the side pane, under the Tests panel.
//
// TWO TOOLS, both unchanged in behaviour from the Send screen they replace:
//
//  1. RAW UNIVERSE — any universe, patched or not: 512 channel levels (paged,
//     with a jump to any channel), each a number box plus a fader, a
//     per-channel Park, All off and Release all. It is the output engine's
//     raw source (POST /api/dmx, internal/web/server.go): a complete
//     512-slot frame, zeros included, that owns its whole universe above
//     tests and the programmer, and reaches the rig only while the master
//     output is Armed. Release all (POST /api/dmx/stop) lets go of every
//     universe the raw source holds, from any browser.
//  2. UNIVERSE IDENTIFY — universeidentify.js, mounted here as it was on
//     Send: its own range arm, token and 5 s heartbeat lease, raw protocol
//     universe numbers.
//  3. RDM CHANNEL READ — moved from the retired Rig Check view: a fixture
//     committed in Reconcile that has no channel map (no GDTF) can read its
//     slot labels over RDM (POST /api/patch/entries/{id}/rdm-slots). Shown
//     only while such fixtures exist.
//
// Universe numbers: this file keeps the true 0-based wire universe and only
// ever shows or reads the operator's numbering through UI.formatUser /
// UI.parseUser (the presentation boundary). Every state is a word first
// (Park/Parked, Released/Holding), then an icon or a border; never colour
// alone. Leaving the Console stops nothing (C3: output follows the Arm).
const ConsoleTools = (() => {
  const PAGE = 32;                                   // channels per page
  const PAGES = 512 / PAGE;
  const channels = new Array(513).fill(0);           // 1-indexed, [0] unused
  const parked = new Array(513).fill(false);
  const st = {
    open: false,
    universe: 0,       // canonical 0-based wire universe (the only truth)
    page: 0,
    released: null,    // null: nothing sent or released from this page yet
    sentTo: [],        // wire universes this page has sent raw frames to
    msg: '',
    rdmMissing: [],    // committed entries with no channel map
    rdmBusy: false,
    rdmMsg: '',
  };
  const els = {};

  try { st.open = localStorage.getItem('benny512.console.toolsOpen') === 'open'; } catch (_) { /* storage blocked: stay closed */ }

  // --- live-scrub throttle (send.js, verbatim in behaviour) -------------------
  // Trailing-edge, ~30 Hz, coalesced: every mutation marks state dirty and
  // arms a timer unless one is armed or a send is in flight. The send always
  // reads the live `channels` array when it fires, never a snapshot taken
  // when it was scheduled, and a mutation that lands while a send is in
  // flight chains one more send the moment it resolves — so a drag that
  // stops always leaves the rig on the value the fader was released at.
  const SEND_INTERVAL_MS = 33;
  let sendTimer = null;
  let sendInFlight = false;
  let dirty = false;

  function markDirty() {
    dirty = true;
    if (sendTimer || sendInFlight) return;
    sendTimer = setTimeout(fireSend, SEND_INTERVAL_MS);
  }

  async function fireSend() {
    sendTimer = null;
    if (!dirty) return;
    dirty = false;
    sendInFlight = true;
    try {
      await commit();
    } catch (e) {
      say('The raw levels were not sent: ' + e.message);
    } finally {
      sendInFlight = false;
      if (dirty) sendTimer = setTimeout(fireSend, SEND_INTERVAL_MS);
    }
  }

  // flushNow skips the throttle wait for a deliberate one-shot action (All
  // off); with a send in flight it leaves the follow-up to fireSend's chain,
  // still at most one request in flight.
  async function flushNow() {
    if (sendTimer) { clearTimeout(sendTimer); sendTimer = null; }
    if (sendInFlight) return;
    await fireSend();
  }

  // commit sends the full 512-slot frame — every channel, zeros included,
  // parked channels at their frozen values — to the universe in the box.
  async function commit() {
    const u = st.universe;
    const bytes = new Uint8Array(512);
    for (let ch = 1; ch <= 512; ch++) bytes[ch - 1] = channels[ch];
    await Api.sendDmx(u, bytes);
    if (!st.sentTo.includes(u)) st.sentTo.push(u);
    st.released = false;
    renderPill();
  }

  // --- small DOM helpers (same shape as console.js's) -------------------------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const k of Object.keys(props)) {
        const v = props[k];
        if (v === undefined || v === null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = String(v);
        else if (k === 'icon') el.innerHTML = UI.icon(v);
        else if (k.slice(0, 2) === 'on') el.addEventListener(k.slice(2), v);
        else el.setAttribute(k, v === true ? '' : String(v));
      }
    }
    for (const c of [].concat(...kids)) {
      if (c === null || c === undefined || c === false) continue;
      el.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return el;
  }
  function ic(name) { return h('span', { class: 'b5-con-ic', 'aria-hidden': 'true', icon: name }); }
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }
  function btn(label, attrs, onclick, cls, icon) {
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn' + (cls ? ' ' + cls : '') }, attrs || {}), icon ? ic(icon) : null, label);
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function clamp(v) { return Math.max(0, Math.min(255, v)); }
  function say(msg) { st.msg = msg || ''; if (els.status) els.status.textContent = st.msg; }

  // --- mounting -----------------------------------------------------------------
  function mount(container) {
    els.root = container;
    clear(container);
    els.toggle = btn('', { 'data-tools-toggle': '', 'aria-controls': 'conToolsBody' }, () => {
      st.open = !st.open;
      try { localStorage.setItem('benny512.console.toolsOpen', st.open ? 'open' : 'closed'); } catch (_) { /* fine */ }
      renderToggle();
    }, 'b5-ct-toggle');
    els.headword = h('span', { class: 'b5-caption', text: 'raw universe levels · Universe Identify' });
    els.status = h('p', { class: 'b5-con-status', role: 'status', 'data-tools-status': '' });
    els.body = h('div', { class: 'b5-ct-body b5-tools-body', id: 'conToolsBody', 'data-tools-body': '' });
    container.appendChild(h('h2', { class: 'b5-board__head b5-ct-head' }, els.toggle, els.headword));
    container.appendChild(els.status);
    container.appendChild(els.body);
    els.body.appendChild(rawSection());
    // Universe Identify keeps its own module and ids (universeidentify.js);
    // it is built once and only hidden with the panel, so its heartbeat and
    // pagehide/b5-show-changed handlers never lose their elements.
    els.identify = h('div', { class: 'b5-tools-identify', 'data-tools-identify': '' });
    els.identify.innerHTML = UniverseIdentify.html('h3');
    els.body.appendChild(els.identify);
    UniverseIdentify.init();
    els.rdm = h('section', { class: 'b5-tools-rdm', 'aria-labelledby': 'toolsRdmHead', 'data-tools-rdm': '', hidden: true });
    els.body.appendChild(els.rdm);
    window.addEventListener('b5-show-changed', () => { st.rdmMsg = ''; refreshRdm(); });
    window.addEventListener('b5-universe-base-changed', syncUniverseDisplay);
    syncUniverseDisplay();
    renderPage();
    renderToggle();
  }

  function rawSection() {
    const ua = UI.userInputAttrs();
    els.universe = h('input', { type: 'number', id: 'toolsUniverse', class: 'b5-input b5-input--mono', min: ua.min, max: ua.max, 'data-tools-universe': '' });
    els.universe.value = UI.formatUser(st.universe);
    els.universe.addEventListener('input', () => {
      st.universe = UI.parseUser(els.universe.value);
      // The note and caption only — never the box, which is being typed in.
      renderUniverseNote();
    });
    els.note = h('span', { class: 'b5-step-section__note', 'data-tools-universe-note': '' });
    els.hint = h('p', { class: 'b5-caption', 'data-tools-universe-hint': '' });
    els.pageSel = h('select', { class: 'b5-select', id: 'toolsPage', 'data-tools-page': '' });
    for (let p = 0; p < PAGES; p++) els.pageSel.appendChild(h('option', { value: p, text: 'Channels ' + (p * PAGE + 1) + '–' + (p * PAGE + PAGE) }));
    els.pageSel.addEventListener('change', () => goPage(Number(els.pageSel.value)));
    els.prev = btn('Previous', { 'data-tools-page-prev': '', 'aria-label': 'Previous page of channels' }, () => goPage(st.page - 1), 'b5-btn--sm');
    els.next = btn('Next', { 'data-tools-page-next': '', 'aria-label': 'Next page of channels' }, () => goPage(st.page + 1), 'b5-btn--sm');
    els.jump = h('input', { type: 'number', id: 'toolsJump', class: 'b5-input b5-input--mono b5-tools-jump', min: 1, max: 512, step: 1, placeholder: '1–512', 'data-tools-jump': '' });
    els.jump.addEventListener('keydown', ev => { if (ev.key === 'Enter') { ev.preventDefault(); jump(); } });
    const go = btn('Go', { 'data-tools-jump-go': '' }, () => jump(), 'b5-btn--sm');
    els.tally = h('span', { class: 'b5-step-section__note', 'data-tools-tally': '' });
    els.grid = h('div', { class: 'b5-dmx-grid b5-tools-grid', 'data-tools-grid': '' });
    els.grid.addEventListener('input', onInput);
    els.grid.addEventListener('change', onChange);
    els.grid.addEventListener('click', onGridClick);
    els.pill = h('span', { 'data-tools-pill': '' });
    const allOffBtn = btn('All off', { 'data-tools-alloff': '' }, allOff, 'b5-btn--danger', 'revert');
    const releaseBtn = btn('Release all', { 'data-tools-release': '' }, release, '', 'status-pending');
    return h('section', { class: 'b5-tools-raw', 'aria-labelledby': 'toolsRawHead', 'data-tools-raw': '' },
      h('h3', { class: 'b5-step-section__head', id: 'toolsRawHead' }, 'Raw universe ', els.note),
      h('div', { class: 'b5-alert b5-alert--caution' }, ic('status-warning'),
        h('div', null,
          h('p', { class: 'b5-alert__title', text: 'Raw levels override everything else on their universe' }),
          h('p', { class: 'b5-alert__body', text: 'Every channel transmits live while you drag, zeros included — a channel pulled down to 0 goes dark immediately, it does not hold its last value. Park a channel to freeze it against a stray drag; a parked channel still transmits at its frozen value, park does not hand it back to a console. Confirm no board is patched to this universe before sending.' }),
          h('p', { class: 'b5-alert__body', text: 'Output follows the master Arm in the top strip: nothing here reaches the rig unless output is Armed, and Disarm blacks out. Once you send on a universe, raw levels own all of it — above the programmer and any test there — until Release all, which lets go of every universe raw levels hold, from any browser.' }))),
      h('div', { class: 'b5-toolbar' },
        h('div', { class: 'b5-toolbar__row' }, h('label', { class: 'b5-field__label', for: 'toolsUniverse', text: 'Universe' }), els.universe),
        els.hint,
        h('div', { class: 'b5-toolbar__row b5-tools-pager' },
          els.prev,
          h('label', { class: 'b5-visually-hidden', for: 'toolsPage', text: 'Channel page' }), els.pageSel,
          els.next,
          h('label', { class: 'b5-field__label', for: 'toolsJump', text: 'Jump to channel' }), els.jump, go)),
      h('p', { class: 'b5-caption b5-tools-tallyline' }, 'Channel levels: ', els.tally),
      h('div', { class: 'b5-scrollbox' }, els.grid),
      h('div', { class: 'b5-actionbar b5-tools-actions', 'aria-label': 'Raw output' },
        h('div', { class: 'b5-actionbar__status' }, els.pill),
        h('div', { class: 'b5-actionbar__buttons' }, allOffBtn, releaseBtn)));
  }

  // --- rendering ------------------------------------------------------------------
  function renderToggle() {
    if (!els.toggle) return;
    els.toggle.setAttribute('aria-expanded', st.open ? 'true' : 'false');
    clear(els.toggle);
    els.toggle.appendChild(ic(st.open ? 'chevron-collapse' : 'chevron-expand'));
    els.toggle.appendChild(document.createTextNode(st.open ? 'Tools · hide' : 'Tools · show'));
    els.body.hidden = !st.open;
  }

  // renderUniverseNote: the head note and the caption, from the canonical
  // number. (Send rewrote only the note while typing, so its caption named
  // the previous universe's Port-Address until the numbering changed.)
  function renderUniverseNote() {
    if (els.note) els.note.textContent = 'universe ' + UI.formatUser(st.universe) + ' · ' + UI.universeScheme('user');
    if (els.hint) {
      els.hint.textContent =
        `Numbering is the ${UI.universeScheme('user')} — the same numbering as Patch and Rig Walk. Nodes and the Analyzer show the raw Art-Net universe. ` +
        `Universe ${UI.formatUser(st.universe)} here is Art-Net Port-Address ${st.universe} on the wire. Any universe can be used, patched or not. ` +
        'Change the numbering on the Settings screen.';
    }
  }

  // syncUniverseDisplay rewrites the box, the note and the caption from the
  // canonical number — on mount and on every numbering change — never by
  // re-reading what the box shows under the old numbering.
  function syncUniverseDisplay() {
    if (!els.universe) return;
    const ua = UI.userInputAttrs();
    els.universe.min = ua.min; els.universe.max = ua.max;
    els.universe.value = UI.formatUser(st.universe);
    renderUniverseNote();
    renderPill();
  }

  function renderPill() {
    if (!els.pill) return;
    const held = st.sentTo.map(u => UI.formatUser(u)).join(', ');
    const [tone, icon, word] = st.released === null ? ['unread', 'status-pending', 'Nothing sent from this page']
      : st.released ? ['open', 'status-pending', 'Released']
        : ['ok', 'status-ok', 'Holding raw levels · universe ' + held];
    clear(els.pill);
    els.pill.appendChild(h('span', { class: 'b5-pill b5-pill--lg b5-pill--' + tone, 'data-tools-pillword': '' }, ic(icon), word));
  }

  function renderTally() {
    if (!els.tally) return;
    let up = 0, park = 0;
    for (let ch = 1; ch <= 512; ch++) { if (channels[ch] > 0) up++; if (parked[ch]) park++; }
    els.tally.textContent = `${up} channel${up === 1 ? '' : 's'} above zero · ${park} parked`;
  }

  // renderPage draws the PAGE cells of the current page from the 512-slot
  // state. Every channel keeps its level and park across page changes; the
  // page only decides which cells are on screen.
  function renderPage() {
    clear(els.grid);
    const first = st.page * PAGE + 1;
    for (let ch = first; ch < first + PAGE; ch++) {
      const cell = h('div', { class: 'b5-dmx-cell', 'data-ch': ch });
      // Park is a real <button aria-pressed>: the WORD Park/Parked, the
      // pressed state and the cell's border carry it — never hue alone.
      cell.appendChild(h('span', { class: 'b5-dmx-cell__ch', text: String(ch).padStart(3, '0') }));
      cell.appendChild(h('button', { type: 'button', class: 'b5-btn b5-btn--sm b5-btn--ghost b5-btn--block b5-tools-dmxpark dmx-park', 'data-ch': ch, 'aria-pressed': 'false',
        title: 'Park: freeze this channel and ignore drags. It still transmits at its frozen value — this protects against a stray drag (a hazer, a dowser), it does not hand the channel back to a console.' },
        h('span', { class: 'b5-dmx-cell__park-label', text: 'Park' })));
      cell.appendChild(h('input', { type: 'number', min: 0, max: 255, value: channels[ch], 'data-ch': ch, class: 'dmx-num', 'aria-label': 'Channel ' + ch + ' level' }));
      cell.appendChild(h('input', { type: 'range', min: 0, max: 255, value: channels[ch], 'data-ch': ch, class: 'dmx-fader b5-dmx-cell__range b5-range-touch', 'aria-label': 'Channel ' + ch + ' fader' }));
      cell.querySelector('.dmx-num').value = String(channels[ch]);
      cell.querySelector('.dmx-fader').value = String(channels[ch]);
      cell.classList.toggle('is-active', channels[ch] > 0);
      els.grid.appendChild(cell);
      paintParked(ch);
    }
    els.pageSel.value = String(st.page);
    els.prev.disabled = st.page === 0;
    els.next.disabled = st.page === PAGES - 1;
    renderTally();
  }

  function goPage(p) {
    if (!(p >= 0 && p < PAGES) || p === st.page) return;
    st.page = p;
    renderPage();
  }

  // jump: show the page holding the typed channel and put the focus on its
  // level box. Anything but a whole channel number 1–512 is refused in words.
  function jump() {
    const raw = String(els.jump.value).trim();
    const ch = /^\d+$/.test(raw) ? Number(raw) : NaN;
    if (!(ch >= 1 && ch <= 512)) { say('Type a channel from 1 to 512 to jump to.'); return; }
    say('');
    st.page = Math.floor((ch - 1) / PAGE);
    renderPage();
    const cell = cellFor(ch);
    const box = cell && cell.querySelector('.dmx-num');
    if (box) { box.focus(); if (cell.scrollIntoView) cell.scrollIntoView({ block: 'nearest' }); }
  }

  function cellFor(ch) { return els.grid ? els.grid.querySelector('.b5-dmx-cell[data-ch="' + ch + '"]') : null; }

  // paintParked: a parked channel's inputs are disabled so drags and stray
  // keystrokes cannot reach it, and its button says Parked.
  function paintParked(ch) {
    const cell = cellFor(ch);
    if (!cell) return;
    const on = parked[ch];
    cell.classList.toggle('is-parked', on);
    cell.querySelector('.dmx-num').disabled = on;
    cell.querySelector('.dmx-fader').disabled = on;
    cell.querySelector('.b5-dmx-cell__park-label').textContent = on ? 'Parked' : 'Park';
    cell.querySelector('.dmx-park').setAttribute('aria-pressed', on ? 'true' : 'false');
  }

  // --- input ---------------------------------------------------------------------
  function onGridClick(e) {
    const b = e.target.closest && e.target.closest('.dmx-park');
    if (!b) return;
    const ch = parseInt(b.dataset.ch, 10);
    if (!ch) return;
    // Parking never itself sends: the frozen value is already in the frame.
    parked[ch] = !parked[ch];
    paintParked(ch);
    renderTally();
  }

  function onInput(e) {
    const t = e.target;
    if (!t.classList || t.classList.contains('dmx-park')) return;
    const ch = parseInt(t.dataset.ch, 10);
    if (!ch || parked[ch]) return;
    const val = clamp(parseInt(t.value, 10) || 0);
    channels[ch] = val;
    const cell = t.closest('.b5-dmx-cell');
    cell.querySelector('.dmx-num').value = String(val);
    cell.querySelector('.dmx-fader').value = String(val);
    cell.classList.toggle('is-active', val > 0);
    renderTally();
    markDirty();
  }

  // change (blur/Enter) makes sure a send is scheduled even when an input
  // method fires change without a preceding input.
  function onChange(e) {
    const t = e.target;
    if (!t.classList || t.classList.contains('dmx-park')) return;
    const ch = parseInt(t.dataset.ch, 10);
    if (!ch || parked[ch]) return;
    markDirty();
  }

  // allOff: every unparked channel to 0, sent at once. Parked channels are
  // exempt — that is the point of park.
  function allOff() {
    for (let ch = 1; ch <= 512; ch++) {
      if (parked[ch]) continue;
      channels[ch] = 0;
      const cell = cellFor(ch);
      if (cell) {
        cell.querySelector('.dmx-num').value = '0';
        cell.querySelector('.dmx-fader').value = '0';
        cell.classList.remove('is-active');
      }
    }
    renderTally();
    markDirty();
    return flushNow();
  }

  // release: the raw source lets go of every universe it holds (POST
  // /api/dmx/stop). They fall back to the tests or the programmer, or leave
  // the wire with zero frames. It does not disarm and does not touch
  // Universe Identify. The levels on this page stay as they are, ready to be
  // sent again by the next move.
  async function release() {
    try {
      await Api.dmxRelease();
      st.released = true;
      st.sentTo = [];
      say('');
    } catch (e) {
      say('Release was not confirmed: ' + e.message);
    }
    renderPill();
  }

  // --- RDM channel read (from the retired Rig Check view) ------------------------
  // rdmMissing: committed to an RDM device, a footprint, and no channel map.
  function rdmMissing(entries) {
    return (entries || []).filter(e => e.confirmedUid && !Object.keys(e.channelFunctions || {}).length && e.footprint > 0);
  }
  async function refreshRdm() {
    try {
      const p = await Api.getPatch();
      st.rdmMissing = rdmMissing(p && p.active && p.patch ? p.patch.entries : []);
    } catch (_) {
      st.rdmMissing = []; // the Console's own layout read reports a lost server
    }
    renderRdm();
  }
  function renderRdm() {
    if (!els.rdm) return;
    clear(els.rdm);
    const n = st.rdmMissing.length;
    els.rdm.hidden = !n && !st.rdmMsg;
    if (els.rdm.hidden) return;
    els.rdm.appendChild(h('h3', { class: 'b5-step-section__head', id: 'toolsRdmHead', text: 'RDM channel map' }));
    if (n) {
      const b = btn('Read RDM channels for ' + n + ' committed fixture' + (n === 1 ? '' : 's'), { 'data-tools-rdm-read': '' }, readRdm);
      b.disabled = st.rdmBusy;
      els.rdm.appendChild(h('div', { class: 'b5-row' }, b));
      els.rdm.appendChild(h('p', { class: 'b5-caption', text: 'For fixtures committed in Reconcile that have no GDTF channel map: reads the slot labels each fixture reports over RDM, so its channels get names and controls. Names: ' + st.rdmMissing.map(e => e.name || e.id).join(', ') + '.' }));
    }
    if (st.rdmMsg) els.rdm.appendChild(h('p', { class: 'b5-caption', role: 'status', 'data-tools-rdm-msg': '', text: st.rdmMsg }));
  }
  async function readRdm() {
    if (st.rdmBusy) return;
    st.rdmBusy = true; st.rdmMsg = ''; renderRdm();
    const failures = [];
    let read = 0;
    for (const e of st.rdmMissing.slice()) {
      try { await Api.readPatchRDMSlots(e.id); read++; }
      catch (err) { failures.push((e.name || e.id) + ': ' + err.message); }
    }
    st.rdmBusy = false;
    st.rdmMsg = (read ? 'Read the RDM channels of ' + read + ' fixture' + (read === 1 ? '' : 's') + '. ' : '') +
      (failures.length ? 'Not read — ' + failures.join('; ') : '');
    await refreshRdm();
    // New channel names change the Console's controls: re-read the plan.
    if (read && typeof ConsoleScreen !== 'undefined') ConsoleScreen.load();
  }

  // onEnter: the Console came on screen — re-read Universe Identify's status
  // (another browser may have changed it), exactly as Send did, and which
  // fixtures still lack a channel map.
  function onEnter() { if (els.root) { UniverseIdentify.refresh(); refreshRdm(); } }

  return {
    mount, onEnter,
    _state: st,
    _levels: () => channels.slice(1),
    _parked: () => parked.slice(1),
  };
})();
