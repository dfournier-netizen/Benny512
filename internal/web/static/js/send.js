// send.js — Send screen: universe picker, 512-channel grid with numeric
// entry + faders, start/stop, all-off.
//
// Rule (architecture rev 5 §4): number/range inputs mutate local state only
// on 'input'; the value is committed to the server on 'change' (blur or
// Enter), never on every keystroke/drag tick — this is what keeps 512
// simultaneous sliders from hammering the REST endpoint.
const SendScreen = (() => {
  const channels = new Array(513).fill(0); // 1-indexed, [0] unused
  let pendingSend = false;

  function buildGrid() {
    const grid = document.getElementById('dmxGrid');
    grid.innerHTML = '';
    for (let ch = 1; ch <= 512; ch++) {
      const cell = document.createElement('div');
      cell.className = 'dmx-cell';
      cell.innerHTML = `
        <div class="ch-num">${ch}</div>
        <input type="number" min="0" max="255" value="0" data-ch="${ch}" class="dmx-num">
        <input type="range" min="0" max="255" value="0" data-ch="${ch}" class="dmx-fader">
      `;
      grid.appendChild(cell);
    }
    grid.addEventListener('input', onInput);
    grid.addEventListener('change', onChange);
  }

  function onInput(e) {
    const ch = parseInt(e.target.dataset.ch, 10);
    if (!ch) return;
    const val = clamp(parseInt(e.target.value, 10) || 0);
    channels[ch] = val;
    // Mirror the paired control's displayed value without a full re-render
    // (state mutation only, per the oninput rule) so num+fader stay in sync
    // while dragging.
    const grid = document.getElementById('dmxGrid');
    const cell = e.target.closest('.dmx-cell');
    cell.querySelector('.dmx-num').value = val;
    cell.querySelector('.dmx-fader').value = val;
  }

  function onChange(e) {
    const ch = parseInt(e.target.dataset.ch, 10);
    if (!ch) return;
    commit();
  }

  function clamp(v) { return Math.max(0, Math.min(255, v)); }

  async function commit() {
    if (pendingSend) return;
    pendingSend = true;
    try {
      const universe = parseInt(document.getElementById('sendUniverse').value, 10) || 0;
      const payload = {};
      for (let ch = 1; ch <= 512; ch++) {
        if (channels[ch] !== 0) payload[ch] = channels[ch];
      }
      await Api.sendDmx(universe, payload);
    } catch (e) {
      console.error('DMX send failed', e);
    } finally {
      pendingSend = false;
    }
  }

  function allOff() {
    for (let ch = 1; ch <= 512; ch++) channels[ch] = 0;
    document.querySelectorAll('.dmx-num').forEach(el => el.value = 0);
    document.querySelectorAll('.dmx-fader').forEach(el => el.value = 0);
    commit();
  }

  function init() {
    buildGrid();
    document.getElementById('btnDmxStart').addEventListener('click', () => Api.dmxStart());
    document.getElementById('btnDmxStop').addEventListener('click', () => Api.dmxStop());
    document.getElementById('btnDmxAllOff').addEventListener('click', allOff);
  }

  return { init };
})();
