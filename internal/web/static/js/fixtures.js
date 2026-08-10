// fixtures.js — Fixtures screen: Discover, fixture table, RDM param detail
// (device info, start address, personality, labels, Identify toggle).
const FixturesScreen = (() => {
  let nodes = [];
  let fixtures = [];
  let selectedUID = null;
  let detailCache = {}; // uid -> {deviceInfo, personality, labels...}

  async function refreshNodes() {
    nodes = await Api.getNodes();
    const sel = document.getElementById('fixtureNodeSelect');
    const prevValue = sel.value;
    sel.innerHTML = '';
    nodes.forEach(n => {
      (n.ports || []).forEach(p => {
        const opt = document.createElement('option');
        opt.value = JSON.stringify({ ip: n.ip, bindIndex: n.bindIndex, portAddress: p.outputAddress });
        opt.textContent = `${n.shortName || n.ip} — port ${p.index} (addr ${p.outputAddress})`;
        sel.appendChild(opt);
      });
    });
    if (prevValue) sel.value = prevValue;
  }

  async function refreshFixtures() {
    fixtures = await Api.getFixtures();
    render();
  }

  async function discover() {
    const sel = document.getElementById('fixtureNodeSelect');
    const status = document.getElementById('discoverStatus');
    if (!sel.value) { status.textContent = 'no node/port selected'; return; }
    const { ip, bindIndex, portAddress } = JSON.parse(sel.value);
    status.textContent = 'discovering…';
    try {
      const res = await Api.discover(ip, bindIndex, portAddress);
      status.textContent = `found ${res.uids.length} device(s)${res.complete ? '' : ' (incomplete)'}`;
      await refreshFixtures();
    } catch (e) {
      status.textContent = 'error: ' + e.message;
    }
  }

  function render() {
    const tbody = document.querySelector('#fixturesTable tbody');
    const scrollTop = tbody.parentElement.scrollTop;
    tbody.innerHTML = '';
    fixtures.forEach(f => {
      const tr = document.createElement('tr');
      if (f.uid === selectedUID) tr.classList.add('selected');
      tr.innerHTML = `
        <td>${escapeHtml(f.manufacturerName)}</td>
        <td>${escapeHtml(f.uid)}</td>
        <td>${f.portAddress}</td>
        <td>—</td>
      `;
      tr.addEventListener('click', () => { selectedUID = f.uid; renderDetail(); render(); });
      tbody.appendChild(tr);
    });
    tbody.parentElement.scrollTop = scrollTop;
  }

  async function renderDetail() {
    const el = document.getElementById('fixtureDetail');
    const f = fixtures.find(x => x.uid === selectedUID);
    if (!f) {
      el.innerHTML = '<p class="empty-hint">Select a fixture to view/edit its RDM parameters.</p>';
      return;
    }
    el.innerHTML = `
      <h3>${escapeHtml(f.manufacturerName)} — ${escapeHtml(f.uid)}</h3>
      <div class="field-row"><label>Loading…</label></div>
    `;
    const uid = f.uid;
    const [deviceInfo, label, personality, ident] = await Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'device_label'),
      Api.getParam(uid, 'dmx_personality'),
      Api.getParam(uid, 'identify_device'),
    ]);
    if (selectedUID !== uid) return; // selection changed while loading

    const di = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    const lbl = label.status === 'fulfilled' ? label.value.value : '';
    const pers = personality.status === 'fulfilled' ? personality.value.value : null;
    const identOn = ident.status === 'fulfilled' ? ident.value.value : false;

    el.innerHTML = `
      <h3>${escapeHtml(f.manufacturerName)} — ${escapeHtml(f.uid)}</h3>
      <div class="field-row"><label>Device label</label>
        <input id="fxLabel" type="text" value="${escapeHtml(lbl)}">
      </div>
      <div class="field-row"><label>Start address</label>
        <input id="fxStartAddr" type="number" min="1" max="512" value="${di ? di.dmxStartAddress : ''}">
      </div>
      <div class="field-row"><label>Personality</label>
        <select id="fxPersonality">
          ${pers ? Array.from({length: pers.count}, (_, i) => i + 1).map(i =>
            `<option value="${i}" ${i === pers.current ? 'selected' : ''}>${i}</option>`).join('') : ''}
        </select>
      </div>
      <div class="field-row"><label>Footprint</label><span>${di ? di.dmxFootprint : '—'}</span></div>
      <div class="field-row"><label>Identify</label>
        <input id="fxIdentify" type="checkbox" ${identOn ? 'checked' : ''}>
      </div>
      <div class="field-row"><span id="fxStatus" class="hint"></span></div>
    `;

    document.getElementById('fxLabel').addEventListener('change', async (e) => {
      await saveParam(uid, 'device_label', e.target.value);
    });
    document.getElementById('fxStartAddr').addEventListener('change', async (e) => {
      await saveParam(uid, 'dmx_start_address', parseInt(e.target.value, 10));
    });
    document.getElementById('fxPersonality').addEventListener('change', async (e) => {
      await saveParam(uid, 'dmx_personality', parseInt(e.target.value, 10));
    });
    document.getElementById('fxIdentify').addEventListener('change', async (e) => {
      try {
        await Api.identify(uid, e.target.checked);
        setFxStatus('identify ' + (e.target.checked ? 'on' : 'off'));
      } catch (err) {
        setFxStatus('error: ' + err.message);
      }
    });
  }

  async function saveParam(uid, pid, value) {
    try {
      await Api.setParam(uid, pid, value);
      setFxStatus('saved');
    } catch (e) {
      setFxStatus('error: ' + e.message);
    }
  }

  function setFxStatus(msg) {
    const el = document.getElementById('fxStatus');
    if (el) el.textContent = msg;
  }

  function init() {
    document.getElementById('btnDiscover').addEventListener('click', discover);
    refreshNodes();
    refreshFixtures();
  }

  return { init, refreshNodes, refreshFixtures };
})();
