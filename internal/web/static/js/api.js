// api.js — thin fetch wrappers for the REST surface. No framework, no
// build step (Rackmaster-style, per architecture rev 5 §4).
const Api = (() => {
  function stripEmpty(obj) {
    const out = {};
    Object.keys(obj).forEach(k => { if (obj[k] !== '' && obj[k] !== null && obj[k] !== undefined) out[k] = obj[k]; });
    return out;
  }

  // bytesToBase64: plain byte array/typed array -> base64 string, matching
  // what Go's encoding/json produces/expects for a []byte field. No atob/
  // btoa polyfill needed — both are standard browser globals.
  function bytesToBase64(bytes) {
    let binary = '';
    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
    return btoa(binary);
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
    // getParam's optional `query` (e.g. {index:3}) drives the *_DESCRIPTION-
    // style dimmer PIDs (CURVE_DESCRIPTION etc.), which need an index
    // alongside {uid}/{pid} — see internal/web/server.go's parseIndexQuery.
    getParam: (uid, pid, query) => req('GET', `/api/fixture/${encodeURIComponent(uid)}/param/${pid}` + (query ? '?' + new URLSearchParams(query).toString() : '')),
    setParam: (uid, pid, value) => req('POST', `/api/fixture/${encodeURIComponent(uid)}/param/${pid}`, { value }),
    identify: (uid, on) => req('POST', '/api/identify', { uid, on }),
    // sendDmx takes a 512-length array/typed-array of raw channel bytes
    // (index 0 = channel 1) and always transmits the whole frame, zeros
    // included — see server.go's dmxRequest doc comment for why the wire
    // format is a base64 []byte rather than the old per-channel-key object.
    // btoa/String.fromCharCode is a browser built-in, not a dependency —
    // consistent with the "vanilla JS, stdlib only" rule.
    sendDmx: (universe, channelBytes) => req('POST', '/api/dmx', { universe, channels: bytesToBase64(channelBytes) }),
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

    // --- Phase D: service life, destructive actions, supported-PID gate ---
    getServiceLife: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/service-life`),
    setServiceLife: (uid, field, value) => req('POST', `/api/device/${encodeURIComponent(uid)}/service-life`, { field, value }),
    getDeviceActions: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/actions`),
    getFactoryDefaults: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/factory-defaults`),
    // setFactoryDefaults/resetDevice always send the server's required
    // {"confirm":"RESET"} tripwire themselves — the UI's own arm-then-
    // confirm gate (devicedetail.js) is what actually protects the click,
    // this is just the fixed wire contract the server demands regardless.
    setFactoryDefaults: (uid) => req('POST', `/api/device/${encodeURIComponent(uid)}/factory-defaults`, { confirm: 'RESET' }),
    resetDevice: (uid, mode) => req('POST', `/api/device/${encodeURIComponent(uid)}/reset`, { mode, confirm: 'RESET' }),
    getSupportedParameters: (uid) => req('GET', `/api/device/${encodeURIComponent(uid)}/supported-parameters`),

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

    // --- Phase 2a: patch model, patch<->RDM reconcile, rig check ---
    getPatch: () => req('GET', '/api/patch'),
    newPatch: (name) => req('POST', '/api/patch/new', { name }),
    createPatchEntry: (entry) => req('POST', '/api/patch/entries', entry),
    updatePatchEntry: (id, entry) => req('PUT', `/api/patch/entries/${encodeURIComponent(id)}`, entry),
    deletePatchEntry: (id) => req('DELETE', `/api/patch/entries/${encodeURIComponent(id)}`),
    reorderPatch: (order) => req('POST', '/api/patch/reorder', { order }),
    getPatchCollisions: () => req('GET', '/api/patch/collisions'),
    getPatchReconcile: () => req('GET', '/api/patch/reconcile'),
    reconcileConfirm: (id, deviceUid) => req('POST', `/api/patch/reconcile/${encodeURIComponent(id)}/confirm`, { deviceUid }),
    reconcileReject: (id) => req('POST', `/api/patch/reconcile/${encodeURIComponent(id)}/reject`),
    reconcileFix: (id, deviceUid) => req('POST', `/api/patch/reconcile/${encodeURIComponent(id)}/fix`, { deviceUid }),
    reconcileFixAll: (confirm) => req('POST', '/api/patch/reconcile/fix-all', { confirm }),
    patchAdopt: (mode) => req('POST', '/api/patch/adopt', { mode }),
    patchImport: (mode, entries) => req('POST', '/api/patch/import', { mode, entries }),
    patchExportUrl: (format) => '/api/patch/export?format=' + format,
    patchReconcileExportUrl: (format) => '/api/patch/reconcile/export?format=' + format,

    getRigCheckState: () => req('GET', '/api/patch/rigcheck'),
    rigCheckStart: (body) => req('POST', '/api/patch/rigcheck/start', body),
    rigCheckStop: () => req('POST', '/api/patch/rigcheck/stop'),
    rigCheckBlackout: () => req('POST', '/api/patch/rigcheck/blackout'),
    rigCheckNext: () => req('POST', '/api/patch/rigcheck/next'),
    rigCheckPrevious: () => req('POST', '/api/patch/rigcheck/previous'),
    rigCheckJump: (index) => req('POST', '/api/patch/rigcheck/jump', { index }),
    rigCheckMode: (mode) => req('POST', '/api/patch/rigcheck/mode', { mode }),
    rigCheckLevel: (level) => req('POST', '/api/patch/rigcheck/level', { level }),
    rigCheckChannel: (delta) => req('POST', '/api/patch/rigcheck/channel', { delta }),
    // rigCheckStopBeacon fires the "stop and blackout" call via
    // fetch(keepalive:true), bypassing req()'s JSON-response parsing — used
    // from pagehide/visibilitychange handlers where the page may already be
    // gone before a normal response arrives (mirrors walkIdentifyOffBeacon's
    // "never leave the rig lit" safety net, task ask item 4).
    rigCheckStopBeacon: () => {
      try { fetch('/api/patch/rigcheck/stop', { method: 'POST', keepalive: true }); } catch (e) { /* best-effort */ }
    },

    // --- Devices screen: clear discovered devices (internal/web/devicesclear.go) ---
    // Deliberately narrow (server-side doc comment): clears only the
    // registry's discovered-device entries + RDM Table-of-Devices for the
    // chosen scope. Capture/logs/learned-PID cache all survive — this is
    // NOT the destructive full reset below.
    clearDevicesAll: () => req('POST', '/api/devices/clear', { scope: 'all' }),
    clearDevicesPort: (ip, portAddress) => req('POST', '/api/devices/clear', { scope: 'port', ip, portAddress }),

    // --- Settings screen: destructive full reset (internal/web/reset.go) ---
    // Deletes the patch and the Rig Walk session (on disk and in memory),
    // then the exe exits — see settings.js for the terminal-state UI this
    // drives. confirm must be the exact string "RESET" or the server
    // 400s with "confirmation required".
    fullReset: () => req('POST', '/api/reset', { confirm: 'RESET' }),
  };
})();
