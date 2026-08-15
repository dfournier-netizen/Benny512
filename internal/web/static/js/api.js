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

  // MAX_DMX_ADDRESS/formatAddressRange/compareDevices mirror
  // internal/walk/sort.go's MaxDMXAddress/FormatAddressRange/Less exactly
  // (task ask, item 5: "every UI surface that shows a DMX start address"
  // renders through one shared function; item 4: every device/fixture
  // list's sort routes through one shared comparison) — kept here rather
  // than duplicated per-screen so the Devices table, Rig Walk card, and any
  // future list can never quietly drift out of sync on the compound
  // (universe, address) key, the "confirmed footprint 0 sorts last (never
  // interleaved)" rule, or the overflow-past-512 warning text. See
  // sort_test.go/walk_test.go on the Go side for the source-of-truth
  // behavior this is a straight port of.
  const MAX_DMX_ADDRESS = 512;

  // formatAddressRange(start, footprint, known) -> display string.
  //   !known                 -> "—"
  //   footprint 0 or unknown -> "141" (address alone, no parens)
  //   footprint 1            -> "141 (141)"
  //   footprint >1            -> "141 (141-160)"
  //   end > 512               -> range + " ⚠ overflows past 512" (never
  //                              color alone — accessibility constraint)
  function formatAddressRange(start, footprint, known) {
    if (!known) return '—';
    if (!footprint) return String(start);
    const end = start + footprint - 1;
    const rng = footprint > 1 ? `${start}-${end}` : `${start}`;
    let out = `${start} (${rng})`;
    if (end > MAX_DMX_ADDRESS) out += ' ⚠ overflows past 512';
    return out;
  }

  // hasAddress mirrors walk.SortableDevice.hasAddress: a device only counts
  // as "addressless" (sorts last) once its footprint is *confirmed* 0
  // (footprintKnown), never merely because it hasn't been probed yet —
  // conflating those was a real regression on the Go side (see sort.go's
  // comment on FootprintKnown), so this JS port keeps the same distinction.
  function hasAddress(d) {
    if (!d.addressKnown) return false;
    if (d.footprintKnown && !d.footprint) return false;
    return true;
  }

  function cmpStr(a, b) { return a < b ? -1 : a > b ? 1 : 0; }
  function cmpNum(a, b) { return a - b; }

  // lessByAddress mirrors sort.go's lessByAddress: the compound
  // (portAddress/universe, address) key, addressed devices always ahead of
  // addressless ones in both directions, tiebreak on UID ascending always.
  function compareByAddress(a, b, desc) {
    const aHas = hasAddress(a), bHas = hasAddress(b);
    if (aHas !== bHas) return aHas ? -1 : 1;
    if (!aHas) return cmpStr(a.uid, b.uid);
    if (a.portAddress !== b.portAddress) {
      return desc ? cmpNum(b.portAddress, a.portAddress) : cmpNum(a.portAddress, b.portAddress);
    }
    if (a.address !== b.address) {
      return desc ? cmpNum(b.address, a.address) : cmpNum(a.address, b.address);
    }
    return cmpStr(a.uid, b.uid);
  }

  // compareDevices(a, b, order) -> Array.prototype.sort comparator. `a`/`b`
  // shape: { uid, model, portAddress, address, footprint, addressKnown,
  // footprintKnown, firstSeen (Date|number, optional) }. order: "address"
  // (default) | "address_desc" | "model" | "uid" | "discovery".
  function compareDevices(a, b, order) {
    switch (order) {
      case 'address_desc':
        return compareByAddress(a, b, true);
      case 'model':
        return a.model !== b.model ? cmpStr(a.model, b.model) : cmpStr(a.uid, b.uid);
      case 'uid':
        return cmpStr(a.uid, b.uid);
      case 'discovery': {
        const at = a.firstSeen ? +new Date(a.firstSeen) : 0;
        const bt = b.firstSeen ? +new Date(b.firstSeen) : 0;
        return at !== bt ? cmpNum(at, bt) : cmpStr(a.uid, b.uid);
      }
      case 'address':
      default:
        return compareByAddress(a, b, false);
    }
  }

  return {
    formatAddressRange,
    compareDevices,
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
