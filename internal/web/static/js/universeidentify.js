// A bounded Send-tab tool. Raw protocol numbers deliberately bypass show
// numbering, patch scopes, saved selections and the manual Send frame buffer.
const UniverseIdentify = (() => {
  let state = null, token = '', busy = false, epoch = 0, timer = null;
  let message = '';
  const el = id => document.getElementById(id);

  function html() {
    return `<section class="b5-step-section b5-identify" aria-labelledby="identifyHead">
      <h2 class="b5-step-section__head" id="identifyHead">Universe Identify</h2>
      <div class="b5-toolbar">
        <div class="b5-toolbar__row">
          <div class="b5-field">
          <label class="b5-field__label" for="identifyProtocol">Protocol</label>
          <select id="identifyProtocol" class="b5-input">
            <option value="artnet">Art-Net</option><option value="sacn">sACN</option>
          </select>
          </div>
          <div class="b5-field">
          <label class="b5-field__label" for="identifyFrom">First universe</label>
          <input id="identifyFrom" class="b5-input b5-input--mono" type="number" min="0" max="512" step="1" placeholder="Enter first" aria-describedby="identifyHint">
          </div>
          <div class="b5-field">
          <label class="b5-field__label" for="identifyTo">Last universe</label>
          <input id="identifyTo" class="b5-input b5-input--mono" type="number" min="0" max="512" step="1" placeholder="Enter last" aria-describedby="identifyHint">
          </div>
        </div>
        <p class="b5-caption" id="identifyHint">Enter an inclusive range using raw protocol universe numbers, independent of Settings numbering. Universe N (1–512): channel N = 255, all other channels = 0. Art-Net universe 0: all 512 channels = 255. sACN starts at 1. Numbers above 512 are unavailable in this tool.</p>
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div><p class="b5-alert__title">This test drives full universes</p>
          <p class="b5-alert__body">Only your entered range is used; the patch never supplies targets. Stop manual Send and Rig Check first. Arm validates the range without sending, then Identify on starts output. Editing either endpoint or the protocol disarms. Off / Disarm sends zeros to the identified range and releases it; previous output is not restored. Leaving Send or losing the browser heartbeat also disarms.</p></div>
        </div>
        <div class="b5-actionbar b5-identify__actions">
          <div class="b5-actionbar__status"><span id="identifyStatus" role="status" aria-live="polite"></span></div>
          <div class="b5-actionbar__buttons">
            <button id="identifyArm" class="b5-btn">Arm range</button>
            <button id="identifyStart" class="b5-bigbtn b5-bigbtn--go" disabled>Identify on</button>
            <button id="identifyStop" class="b5-bigbtn b5-bigbtn--stop">Off / Disarm</button>
          </div>
        </div>
        <p id="identifyMessage" class="b5-caption" role="alert"></p>
      </div>
    </section>`;
  }

  function range() {
    const protocol = el('identifyProtocol').value;
    const first = el('identifyFrom').value.trim(), last = el('identifyTo').value.trim();
    if (!/^\d+$/.test(first) || !/^\d+$/.test(last)) throw new Error('Enter both universe endpoints as whole numbers.');
    const from = Number(first), to = Number(last), min = protocol === 'sacn' ? 1 : 0;
    if (!['artnet', 'sacn'].includes(protocol) || from < min || to > 512 || from > to) {
      throw new Error(`Enter a range from ${min} to 512 with first universe no greater than last.`);
    }
    return { protocol, from, to };
  }

  function render() {
    if (!el('identifyStatus')) return;
    const armed = state && state.armed, running = state && state.running;
    el('identifyStatus').textContent = !state ? 'Status unknown' : !armed ? 'Disarmed · output off' :
      `${running ? 'Identifying' : 'Armed · output off'} · ${state.protocol === 'artnet' ? 'Art-Net' : 'sACN'} ${state.from}–${state.to}${!token ? ' · another session' : ''}`;
    el('identifyStatus').className = `b5-pill b5-pill--lg ${armed ? 'b5-pill--warn b5-pill--solid' : 'b5-pill--open'}`;
    let valid = true;
    try { range(); } catch (e) { valid = false; }
    el('identifyArm').disabled = busy || !state || armed || !valid;
    el('identifyStart').disabled = busy || !armed || running || !token;
    el('identifyMessage').textContent = message || (state && state.error) || '';
    const min = el('identifyProtocol').value === 'sacn' ? 1 : 0;
    el('identifyFrom').min = min;
    el('identifyTo').min = min;
  }

  function cancelHeartbeat() {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  }

  function heartbeat() {
    cancelHeartbeat();
    const version = epoch;
    timer = setTimeout(async () => {
      timer = null;
      if (!token || version !== epoch) return;
      try {
        const next = await Api.universeIdentifyHeartbeat(token);
        if (version !== epoch) return;
        state = next;
        render();
        heartbeat();
      } catch (e) {
        if (version !== epoch) return;
        message = e.message;
        await disarm(false);
      }
    }, 1000);
  }

  async function refresh() {
    if (!el('identifyStatus') || busy || token) return;
    const version = epoch;
    try {
      const next = await Api.universeIdentifyStatus();
      if (version === epoch) state = next;
    } catch (e) { if (version === epoch) { state = null; message = e.message; } }
    render();
  }

  async function arm() {
    if (busy || !state || state.armed) return;
    let target;
    try { target = range(); } catch (e) { message = e.message; render(); return; }
    const version = ++epoch;
    busy = true; message = ''; render();
    try {
      const next = await Api.universeIdentifyArm(target);
      if (version !== epoch) { await Api.universeIdentifyStop(); return; }
      token = next.token;
      state = next;
      heartbeat();
    } catch (e) { if (version === epoch) { state = null; message = e.message; } }
    finally { busy = false; render(); }
    if (!state) await refresh();
  }

  async function start() {
    if (busy || !token || !state || !state.armed || state.running) return;
    const version = epoch;
    busy = true; message = ''; render();
    try {
      const next = await Api.universeIdentifyStart(token);
      if (version === epoch) state = next;
    } catch (e) {
      if (version === epoch) { message = e.message; await disarm(false); }
    } finally { busy = false; render(); }
  }

  async function disarm(clearMessage = true) {
    const version = ++epoch;
    token = ''; cancelHeartbeat();
    state = null;
    if (clearMessage) message = '';
    render();
    try {
      const next = await Api.universeIdentifyStop();
      if (version === epoch) state = next;
    } catch (e) { if (version === epoch) message = `Stop not confirmed: ${e.message}. The server disarms after 5 seconds without a heartbeat.`; }
    render();
  }

  function edited() {
    // Input edits never arm or start. They invalidate an in-flight Arm too.
    if (busy || token || (state && state.armed)) { void disarm(); return; }
    message = ''; render();
  }

  function leave() {
    if (busy || token || (state && state.armed)) void disarm();
  }

  function init() {
    el('identifyArm').addEventListener('click', arm);
    el('identifyStart').addEventListener('click', start);
    el('identifyStop').addEventListener('click', () => disarm());
    el('identifyFrom').addEventListener('input', edited);
    el('identifyTo').addEventListener('input', edited);
    el('identifyProtocol').addEventListener('change', edited);
    window.addEventListener('pagehide', () => {
      if (busy || token || (state && state.armed)) {
        ++epoch; token = ''; cancelHeartbeat(); state = null;
        Api.universeIdentifyStopBeacon();
      }
    });
    window.addEventListener('pageshow', refresh);
    window.addEventListener('b5-show-changed', leave);
    render();
    void refresh();
  }

  return { html, init, refresh, disarm, leave };
})();
