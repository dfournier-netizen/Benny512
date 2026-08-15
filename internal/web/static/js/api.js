// api.js — thin fetch wrappers for the REST surface. No framework, no
// build step (Rackmaster-style, per architecture rev 5 §4).
const Api = (() => {
  function stripEmpty(obj) {
    const out = {};
    Object.keys(obj).forEach(k => { if (obj[k] !== '' && obj[k] !== null && obj[k] !== undefined) out[k] = obj[k]; });
    return out;
  }

  async function req(method, path, body) {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (e) { data = text; }
    }
    if (!res.ok) {
      const msg = (data && data.error) ? data.error : ('HTTP ' + res.status);
      throw new Error(msg);
    }
    return data;
  }

  return {
    getNodes: () => req('GET', '/api/nodes'),
    getFixtures: () => req('GET', '/api/fixtures'),
    discover: (node, bindIndex, portAddress) =>
      req('POST', '/api/discover', { node, bindIndex, portAddress }),
    getParam: (uid, pid) => req('GET', `/api/fixture/${encodeURIComponent(uid)}/param/${pid}`),
    setParam: (uid, pid, value) => req('POST', `/api/fixture/${encodeURIComponent(uid)}/param/${pid}`, { value }),
    identify: (uid, on) => req('POST', '/api/identify', { uid, on }),
    sendDmx: (universe, channels) => req('POST', '/api/dmx', { universe, channels }),
    dmxStart: () => req('POST', '/api/dmx/start'),
    dmxStop: () => req('POST', '/api/dmx/stop'),
    getSettings: () => req('GET', '/api/settings'),
    postSettings: (s) => req('POST', '/api/settings', s),
    captureSnapshot: (params) => {
      const qs = new URLSearchParams(params || {}).toString();
      return req('GET', '/api/capture/snapshot' + (qs ? '?' + qs : ''));
    },
    rdmCaptureSnapshot: (params) => {
      const qs = new URLSearchParams(stripEmpty(params || {})).toString();
      return req('GET', '/api/capture/rdm/snapshot' + (qs ? '?' + qs : ''));
    },
    // exportUrl builds the download URL for GET /api/capture/export — the
    // caller navigates/window.opens it directly so the browser handles the
    // Content-Disposition:attachment download rather than fetching it here.
    exportUrl: (format, params) => {
      const qs = new URLSearchParams(Object.assign({ format }, stripEmpty(params || {}))).toString();
      return '/api/capture/export?' + qs;
    },

    // --- Phase 1c+: device-centric (generic PIDs, sensors, status) ---
    getDeviceParams: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/params`),
    getDeviceParam: (uid, pid) => req('GET', `/api/device/${encodeURIComponent(uid)}/param/${pid}`),
    setDeviceParam: (uid, pid, value) => req('POST', `/api/device/${encodeURIComponent(uid)}/param/${pid}`, { value }),
    introspectDevice: (uid) => req('POST', `/api/device/${encodeURIComponent(uid)}/introspect`),
    getDeviceSensors: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/sensors`),
    recordDeviceSensors: (uid, sensor) => req('POST', `/api/device/${encodeURIComponent(uid)}/sensors/record`, { sensor }),
    resetDeviceSensors: (uid, sensor) => req('POST', `/api/device/${encodeURIComponent(uid)}/sensors/reset`, { sensor }),
    getDeviceStatus: (uid, filter) => req('GET', `/api/device/${encodeURIComponent(uid)}/status` + (filter ? `?filter=${filter}` : '')),

    // --- Phase 1c+: node/network configuration ---
    setNodeAddress: (ip, body) => req('POST', `/api/node/${encodeURIComponent(ip)}/address`, body),
    setNodeIPConfig: (ip, body) => req('POST', `/api/node/${encodeURIComponent(ip)}/ipconfig`, body),
    setNodeInput: (ip, body) => req('POST', `/api/node/${encodeURIComponent(ip)}/input`, body),
    getNICs: () => req('GET', '/api/nics'),

    // --- Rig Walk mode (phone-optimized device walkthrough) ---
    getWalkSession: () => req('GET', '/api/walk/session'),
    startWalk: (body) => req('POST', '/api/walk/session', body),
    endWalk: () => req('POST', '/api/walk/end'),
    walkGoto: (index) => req('POST', '/api/walk/goto', { index }),
    walkAutoAdvance: (on) => req('POST', '/api/walk/autoadvance', { on }),
    walkSetStatus: (uid, status, note) => req('POST', `/api/walk/${encodeURIComponent(uid)}/status`, { status, note }),
    walkSetAddress: (uid, value) => req('POST', `/api/walk/${encodeURIComponent(uid)}/address`, { value }),
    walkIdentifyRetry: () => req('POST', '/api/walk/identify/retry'),
    walkIdentifyAllOff: () => req('POST', '/api/walk/identify/all-off'),
    // walkIdentifyOffBeacon fires the lightweight "turn off the current
    // device" call directly via fetch(keepalive:true), bypassing req()'s
    // JSON-response parsing — used from pagehide/visibilitychange handlers
    // where the page may already be gone before a normal response arrives
    // (task ask: "never leave a fixture flashing" when the tech navigates
    // away or the browser tab dies mid-walk).
    walkIdentifyOffBeacon: () => {
      try { fetch('/api/walk/identify/off', { method: 'POST', keepalive: true }); } catch (e) { /* best-effort */ }
    },
    walkExportUrl: (format) => '/api/walk/export?format=' + format,
  };
})();
