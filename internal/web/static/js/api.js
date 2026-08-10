// api.js — thin fetch wrappers for the REST surface. No framework, no
// build step (Rackmaster-style, per architecture rev 5 §4).
const Api = (() => {
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
  };
})();
