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

  function buildGrid() {
    const grid = document.getElementById('dmxGrid');
    grid.innerHTML = '';
    for (let ch = 1; ch <= 512; ch++) {
      const cell = document.createElement('div');
      cell.className = 'b5-dmx-cell';
      cell.dataset.ch = String(ch);
      cell.innerHTML = `
        <span class="b5-dmx-cell__ch">${String(ch).padStart(3, '0')}</span>
        <label class="b5-dmx-cell__park" title="Park: freeze this channel and ignore drags. It still transmits at its frozen value — this protects against a stray drag (a hazer, a dowser), it does not hand the channel back to a console.">
          <input type="checkbox" class="dmx-park" data-ch="${ch}">
          <span class="b5-dmx-cell__park-label">Park</span>
        </label>
        <input type="number" min="0" max="255" value="0" data-ch="${ch}" class="dmx-num">
        <input type="range" min="0" max="255" value="0" data-ch="${ch}" class="dmx-fader b5-dmx-cell__range b5-range-touch">
      `;
      grid.appendChild(cell);
    }
    grid.addEventListener('input', onInput);
    grid.addEventListener('change', onChange);
  }

  function cellFor(ch) {
    return document.querySelector(`.b5-dmx-cell[data-ch="${ch}"]`);
  }

  function onInput(e) {
    const t = e.target;
    if (t.classList.contains('dmx-park')) return; // toggled on 'change' below
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
    markDirty();
  }

  function onChange(e) {
    const t = e.target;
    if (t.classList.contains('dmx-park')) {
      const ch = parseInt(t.dataset.ch, 10);
      if (ch) setParked(ch, t.checked);
      return;
    }
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
  }

  function clamp(v) { return Math.max(0, Math.min(255, v)); }

  // commit sends the full 512-slot frame — every channel, zeros included,
  // parked channels' frozen values included — as one compact request. See
  // server.go's dmxRequest doc comment for the wire-format rationale.
  async function commit() {
    const universe = parseInt(document.getElementById('sendUniverse').value, 10) || 0;
    const bytes = new Uint8Array(512);
    for (let ch = 1; ch <= 512; ch++) bytes[ch - 1] = channels[ch];
    await Api.sendDmx(universe, bytes);
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
    markDirty();
    flushNow();
  }

  function init() {
    buildGrid();
    document.getElementById('btnDmxStart').addEventListener('click', () => Api.dmxStart());
    document.getElementById('btnDmxStop').addEventListener('click', () => Api.dmxStop());
    document.getElementById('btnDmxAllOff').addEventListener('click', allOff);
  }

  return { init };
})();
