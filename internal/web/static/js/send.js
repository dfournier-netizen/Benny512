// send.js — Send screen: universe picker, 512-channel grid with numeric
// entry + trim fader, per-channel park toggle, start/stop, all-off.
//
// Live-scrubbing rule (bench feedback, replacing the old commit-on-change
// rule): every DMX slot is transmitted on every send, zeros included — a
// console does the same, and it's what fixes the "fader dragged to 0 stays
// lit at its old value" bug (the old payload only carried non-zero
// channels, and the server only ever touched channels present in it, so a
// channel dragged down to 0 was simply omitted and never got zeroed on the
// rig). Because every send now carries the whole 512-slot frame, and the
// owner wants to see pan/tilt/color move live while dragging rather than
// only on blur, oninput both mutates local state AND schedules a send —
// dragging one fader while others sit at nonzero values is exactly the
// case a full-frame send has to keep working for, so parked channels are
// simply included in the outgoing frame like everything else.
//
// What used to guard the REST endpoint from "512 simultaneous sliders"
// hammering was withholding the send until blur/change. That's gone now
// that we send live — the guard is instead the trailing-edge throttle
// below: sends are coalesced to ~30Hz, at most one request in flight, and
// the throttle always reads live state when it actually fires (never a
// value captured back when it was scheduled), so a fast drag can never
// leave the rig on a stale intermediate value once it stops. See
// markDirty/fireSend.
//
// Park is a per-channel freeze: a parked channel's number/range inputs are
// disabled (so drags can't touch it) and it keeps transmitting at its
// frozen value every send — it is protection against a stray drag (a
// hazer, a dowser) while other channels are being scrubbed, NOT a way to
// hand the channel back to a console. Art-Net universes are all-or-nothing
// while outputting, so there is no "don't touch this one" at the protocol
// level; park is purely a client-side guard against Dom's own mouse.
const SendScreen = (() => {
  const channels = new Array(513).fill(0); // 1-indexed, [0] unused
  const parked = new Array(513).fill(false);

  // --- live-scrub throttle -----------------------------------------------
  // Trailing-edge, ~30Hz, coalesced. Every mutation marks state dirty and
  // arms a timer if one isn't already armed or a send isn't already in
  // flight ("at most one request in flight" + "newest value wins" — the
  // timer/in-flight send always reads the live `channels` array at fire
  // time, never a snapshot taken when it was scheduled, so whichever
  // mutation was most recent when it actually fires is what goes out).
  // Guarantee that the final value after a drag stops always lands: dirty
  // only clears immediately before a send is issued for the state it
  // reflects, and if another mutation arrives while that send is still in
  // flight, the finally-block below re-arms a follow-up the moment it
  // resolves — so the loop cannot terminate on a stale value, only on
  // "nothing changed since the last send actually went out". This is what
  // replaces the old pendingSend flag, which *dropped* a commit outright if
  // one was already in flight — exactly the bug this throttle exists to
  // not have: a fast drag ending mid-request under the old code could
  // leave the rig on whatever value happened to be captured by the request
  // that was in flight, not the value the fader was released at.
  const SEND_INTERVAL_MS = 33; // ~30Hz
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
      console.error('DMX send failed', e);
    } finally {
      sendInFlight = false;
      // Something changed while this send was in flight — chain another
      // send rather than dropping it (see guarantee above).
      if (dirty) sendTimer = setTimeout(fireSend, SEND_INTERVAL_MS);
    }
  }

  // flushNow skips the throttle wait for a deliberate one-shot action (All
  // Off) rather than a drag — if a send is already in flight this just
  // marks dirty (already done by the caller) and lets fireSend's own
  // trailing chain pick it up, still honoring "at most one in flight".
  async function flushNow() {
    if (sendTimer) { clearTimeout(sendTimer); sendTimer = null; }
    if (sendInFlight) return;
    await fireSend();
  }

  // --- screen markup (the kit) ---------------------------------------------
  //
  // Built here rather than in index.html, exactly the way analyzer.js's
  // screenHtml does, so this screen can use the shared kit
  // (css/DESIGN.md): numbered .b5-step-section bands top to bottom, a
  // .b5-toolbar instead of the older desktop-first .b5-filterbar, the
  // Start/Stop pair in the kit's .b5-actionbar (an outlined GO pill and a
  // square STOP slab — two silhouettes, so the shape reads before the hue
  // at arm's length), and the 512-cell grid in its own scroll box so the
  // page body never scrolls sideways (rule 4). Every id the old markup
  // carried is unchanged.
  //
  // WHAT IS DELIBERATELY *NOT* CONVERTED: the grid itself. DESIGN.md's
  // worked example ends by blessing "genuinely tabular data a tech scans
  // down (… the Send grid)" — 512 channels as .b5-statecards would be one
  // screen per eight channels. Density is the feature here, so the cells
  // stay .b5-dmx-cell and only their state markers are brought up to the
  // kit's standard.
  function screenHtml() {
    const ua = UI.universeInputAttrs();
    return `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Send</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">Drive one Art-Net universe by hand.</span>
      </div>

      <section class="b5-step-section" aria-labelledby="sendUniverseHead">
        <h2 class="b5-step-section__head" id="sendUniverseHead">
          <span class="b5-step-num">1</span> Universe to send on
          <span class="b5-step-section__note" id="sendUniverseNote"></span>
        </h2>
        <div class="b5-toolbar">
          <div class="b5-toolbar__row">
            <label class="b5-field__label" for="sendUniverse">Universe</label>
            <input type="number" id="sendUniverse" class="b5-input b5-input--mono"
                   min="${ua.min}" max="${ua.max}" value="${escapeHtml(UI.formatUniverse(0))}">
          </div>
          <p class="b5-caption" id="sendUniverseHint"></p>
        </div>
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Manual send overrides live console output</p>
            <p class="b5-alert__body">Every channel transmits live while you drag, zeros included &mdash; a channel pulled down to 0 goes dark immediately, it does not hold its last value. Park a channel to freeze it against a stray drag; a parked channel still transmits at its frozen value, park does not hand it back to a console. Confirm no board is patched to this universe before sending.</p>
          </div>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="sendGridHead">
        <h2 class="b5-step-section__head" id="sendGridHead">
          <span class="b5-step-num">2</span> Channel levels
          <span class="b5-step-section__note" id="sendGridTally"></span>
        </h2>
        <div class="b5-panel">
          <div class="b5-panel__body">
            <div class="b5-scrollbox">
              <div class="b5-dmx-grid" id="dmxGrid"></div>
            </div>
          </div>
        </div>
      </section>

      <section class="b5-actionbar" aria-label="Output">
        <div class="b5-actionbar__status">
          <span class="b5-actionbar__title"><span class="b5-step-num">3</span> Output</span>
          <span id="sendOutputPill"></span>
        </div>
        <div class="b5-actionbar__buttons">
          <button id="btnDmxAllOff" class="b5-btn b5-btn--danger">${UI.icon('revert')}All off</button>
          <button id="btnDmxStart" class="b5-bigbtn b5-bigbtn--go">${UI.icon('status-ok')}START</button>
          <button id="btnDmxStop" class="b5-bigbtn b5-bigbtn--stop">${UI.icon('status-error')}STOP</button>
        </div>
      </section>
    `;
  }

  // outputState: what this PAGE last told the server to do. The server
  // exposes no "am I transmitting?" endpoint and broadcasts no DMX state
  // (POST /api/dmx/start and /stop both answer with a bare status string —
  // see server.go handleDMXStart/handleDMXStop), so a pill claiming "LIVE"
  // as though it had been read back would be inventing a fact. Rule 3:
  // name the unknown. Before either button is pressed this says so in
  // words rather than guessing "stopped".
  let outputState = 'unknown'; // 'unknown' | 'started' | 'stopped'

  function renderOutputPill() {
    const el = document.getElementById('sendOutputPill');
    if (!el) return;
    if (outputState === 'started') {
      el.innerHTML = `<span class="b5-pill b5-pill--lg b5-pill--ok b5-pill--solid">${UI.icon('status-ok')}SENDING</span>`;
    } else if (outputState === 'stopped') {
      el.innerHTML = `<span class="b5-pill b5-pill--lg b5-pill--open">${UI.icon('status-pending')}STOPPED</span>`;
    } else {
      el.innerHTML = `<span class="b5-pill b5-pill--lg b5-pill--unread">${UI.icon('status-pending')}Not started from this page</span>`;
    }
  }

  // tally: how many channels are above zero and how many are frozen, in
  // words, in the section head — the one thing a tech wants to know about a
  // 512-cell grid without reading all of it.
  function renderTally() {
    const el = document.getElementById('sendGridTally');
    if (!el) return;
    let up = 0, park = 0;
    for (let ch = 1; ch <= 512; ch++) { if (channels[ch] > 0) up++; if (parked[ch]) park++; }
    el.textContent = `${up} channel${up === 1 ? '' : 's'} above zero · ${park} parked`;
  }

  function buildGrid() {
    const grid = document.getElementById('dmxGrid');
    grid.innerHTML = '';
    for (let ch = 1; ch <= 512; ch++) {
      const cell = document.createElement('div');
      cell.className = 'b5-dmx-cell';
      cell.dataset.ch = String(ch);
      // Park is a real <button aria-pressed> rather than a bare checkbox
      // (DESIGN.md: "Use <button>, not a clickable <div>", and rule 2's
      // touch minimum — a 13px native checkbox is not reachable with a
      // gloved thumb on a truss). Its state is carried by the WORD
      // Park/Parked, by the icon, and by the cell's own border/ground —
      // three signals, none of them hue alone.
      cell.innerHTML = `
        <span class="b5-dmx-cell__ch">${String(ch).padStart(3, '0')}</span>
        <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost b5-btn--block b5-tools-dmxpark dmx-park" data-ch="${ch}" aria-pressed="false"
                title="Park: freeze this channel and ignore drags. It still transmits at its frozen value — this protects against a stray drag (a hazer, a dowser), it does not hand the channel back to a console.">
          <span class="b5-dmx-cell__park-label">Park</span>
        </button>
        <input type="number" min="0" max="255" value="0" data-ch="${ch}" class="dmx-num" aria-label="Channel ${ch} level">
        <input type="range" min="0" max="255" value="0" data-ch="${ch}" class="dmx-fader b5-dmx-cell__range b5-range-touch" aria-label="Channel ${ch} fader">
      `;
      grid.appendChild(cell);
    }
    grid.addEventListener('input', onInput);
    grid.addEventListener('change', onChange);
    grid.addEventListener('click', onGridClick);
  }

  // onGridClick handles the park button (a <button> fires 'click', not
  // 'change' — the checkbox it replaced was handled in onChange below).
  function onGridClick(e) {
    const btn = e.target.closest && e.target.closest('.dmx-park');
    if (!btn) return;
    const ch = parseInt(btn.dataset.ch, 10);
    if (!ch) return;
    setParked(ch, !parked[ch]);
    renderTally();
  }

  function cellFor(ch) {
    return document.querySelector(`.b5-dmx-cell[data-ch="${ch}"]`);
  }

  function onInput(e) {
    const t = e.target;
    if (t.classList.contains('dmx-park')) return; // a button, handled in onGridClick
    const ch = parseInt(t.dataset.ch, 10);
    if (!ch || parked[ch]) return;
    const val = clamp(parseInt(t.value, 10) || 0);
    channels[ch] = val;
    // Mirror the paired control's displayed value without a full re-render
    // (state mutation only, per the oninput rule) so num+fader stay in sync
    // while dragging, and flag the cell as active (non-zero) same as the
    // design system's .is-active cell state.
    const cell = t.closest('.b5-dmx-cell');
    cell.querySelector('.dmx-num').value = val;
    cell.querySelector('.dmx-fader').value = val;
    cell.classList.toggle('is-active', val > 0);
    renderTally();
    markDirty();
  }

  function onChange(e) {
    const t = e.target;
    if (t.classList.contains('dmx-park')) return; // a button, handled in onGridClick
    const ch = parseInt(t.dataset.ch, 10);
    if (!ch || parked[ch]) return;
    // Belt-and-suspenders: 'input' already staged+scheduled this value;
    // 'change' (blur/Enter) just makes sure a send is scheduled even if
    // some input method fires change without a preceding input event.
    markDirty();
  }

  // setParked freezes/unfreezes one channel: disables its inputs so drags
  // (and stray keystrokes) can't reach it, and relabels the toggle from
  // "Park" to "Parked" — text changes, not just a color/border change, per
  // the "never colour alone" rule. The channel's last value is left exactly
  // as-is and keeps being transmitted every send; parking never itself
  // triggers a send.
  function setParked(ch, isParked) {
    parked[ch] = isParked;
    const cell = cellFor(ch);
    if (!cell) return;
    cell.classList.toggle('is-parked', isParked);
    cell.querySelector('.dmx-num').disabled = isParked;
    cell.querySelector('.dmx-fader').disabled = isParked;
    cell.querySelector('.b5-dmx-cell__park-label').textContent = isParked ? 'Parked' : 'Park';
    const btn = cell.querySelector('.dmx-park');
    if (btn) btn.setAttribute('aria-pressed', isParked ? 'true' : 'false');
  }

  function clamp(v) { return Math.max(0, Math.min(255, v)); }

  // commit sends the full 512-slot frame — every channel, zeros included,
  // parked channels' frozen values included — as one compact request. See
  // server.go's dmxRequest doc comment for the wire-format rationale.
  // sendUniverseCanonical is the true 0-based wire universe — the single
  // source of truth. The #sendUniverse input only ever shows/accepts the
  // display-base-converted number (UI.formatUniverse/parseUniverse); an
  // oninput on that field updates THIS from the CURRENT base (correct at
  // the moment of typing), so a later base change can just reformat this
  // already-canonical number instead of misreading the old display text
  // under the new base (which would silently shift the wire universe).
  let sendUniverseCanonical = 0;

  function syncUniverseFieldDisplay() {
    const el = document.getElementById('sendUniverse');
    if (!el) return;
    const ua = UI.universeInputAttrs();
    el.min = ua.min; el.max = ua.max;
    el.value = UI.formatUniverse(sendUniverseCanonical);
    // The section head and the caption both restate the ACTIVE notation, so
    // a number on this screen can never be read against the wrong base —
    // and both are rewritten from the canonical value on every base change,
    // never re-parsed from what is already in the box.
    const note = document.getElementById('sendUniverseNote');
    if (note) note.textContent = 'universe ' + UI.formatUniverse(sendUniverseCanonical) + ' · ' + UI.universeBaseLabel();
    const hint = document.getElementById('sendUniverseHint');
    if (hint) {
      hint.textContent =
        `Numbering is ${UI.universeBaseLabel()} — the same numbering as Patch, Devices and Nodes. ` +
        `Universe ${UI.formatUniverse(sendUniverseCanonical)} here is Art-Net Port-Address ${sendUniverseCanonical} on the wire. ` +
        'Change the numbering on the Settings screen.';
    }
  }

  async function commit() {
    const bytes = new Uint8Array(512);
    for (let ch = 1; ch <= 512; ch++) bytes[ch - 1] = channels[ch];
    await Api.sendDmx(sendUniverseCanonical, bytes);
  }

  function allOff() {
    for (let ch = 1; ch <= 512; ch++) {
      if (parked[ch]) continue; // parked channels are exempt — that's the point of park
      channels[ch] = 0;
      const cell = cellFor(ch);
      if (cell) {
        cell.querySelector('.dmx-num').value = 0;
        cell.querySelector('.dmx-fader').value = 0;
        cell.classList.remove('is-active');
      }
    }
    renderTally();
    markDirty();
    flushNow();
  }

  function init() {
    const screen = document.getElementById('screen-send');
    if (screen) screen.innerHTML = screenHtml();
    buildGrid();
    document.getElementById('btnDmxStart').addEventListener('click', async () => {
      try { await Api.dmxStart(); outputState = 'started'; } catch (e) { console.error('DMX start failed', e); }
      renderOutputPill();
    });
    document.getElementById('btnDmxStop').addEventListener('click', async () => {
      try { await Api.dmxStop(); outputState = 'stopped'; } catch (e) { console.error('DMX stop failed', e); }
      renderOutputPill();
    });
    document.getElementById('btnDmxAllOff').addEventListener('click', allOff);
    const uniEl = document.getElementById('sendUniverse');
    syncUniverseFieldDisplay();
    uniEl.addEventListener('input', () => {
      sendUniverseCanonical = UI.parseUniverse(uniEl.value);
      // Head/caption only — never the input's own value, which the tech is
      // still typing into.
      const note = document.getElementById('sendUniverseNote');
      if (note) note.textContent = 'universe ' + UI.formatUniverse(sendUniverseCanonical) + ' · ' + UI.universeBaseLabel();
    });
    window.addEventListener('b5-universe-base-changed', syncUniverseFieldDisplay);
    renderOutputPill();
    renderTally();
  }

  return { init };
})();
