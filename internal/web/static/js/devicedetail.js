// devicedetail.js — the single shared renderer for one RDM device's full
// detail panel (Info / Parameters incl. generic manufacturer-PID editor and
// the new E1.37-1 dimmer fields / Sensors with gauges / Status), used by
// BOTH the Devices tab and Rig Walk (task ask: "extract the device-detail
// rendering into ONE shared module... single source of truth — no
// duplicated panel logic"). Callers own their own chrome (Devices: tab
// strip; Rig Walk: big collapsible accordions for one-handed phone use) and
// call the four render*Section functions below into their own containers;
// this module owns all the fetching, caching, WS subscription and
// debounce/cancellation policy so neither caller has to re-implement it.
//
// Rules (same as every other screen, architecture rev 5 §4): oninput
// mutates staged state only, full re-render on onchange/explicit Apply;
// nothing commits without an explicit Apply click (identify is the one
// documented exception — a momentary physical action, not a persisted
// parameter). Vocabulary: "ports" not "jacks".
//
// Markup (2026-08-16 retheme): every field below is built via
// UI.buildApplyField/UI.wireApplyField (ui.js) so the b5-field state
// modifiers (--dirty/--applying/--success/--error) are consistent with
// every other screen — same Apply-to-confirm contract, new shared builder.
//
// --- Probe fan-out / debounce policy (task ask: "a tech tapping Next
// rapidly must not queue dozens of RDM round-trips") ---
// select(uid, sections) is the single entry point both callers use to say
// "this is the device the visible panel should be showing, and these are
// the sections currently expanded/active". A *reselect* (uid changes) is
// debounced SETTLE_MS before any wire traffic fires, and every async
// result checks a generation counter before touching shared state or
// calling back into the caller — a device the user has already navigated
// past can never clobber what's on screen for the device that replaced it,
// and its in-flight RDM round-trip (already sent — RDM has no cancel) is
// simply ignored on arrival. Re-selecting the SAME uid with a different
// `sections` set (an accordion opened, a tab switched) applies immediately,
// no debounce — only the initial settle after a *device* change is
// throttled. Sensor WS subscriptions strictly follow `selectedUID`:
// unsubscribed on every reselect before the new selection is recorded, and
// (re)subscribed only once settle fires with sensors in view.
const DeviceDetail = (() => {
  // RDM PARAMETER_DESCRIPTION data_type byte values (report §5.1 / rdm.DataType).
  const DS = {
    BIT_FIELD: 0x01, ASCII: 0x02, UBYTE: 0x03, SBYTE: 0x04,
    UWORD: 0x05, SWORD: 0x06, UDWORD: 0x07, SDWORD: 0x08,
    BOOLEAN: 0x0D, ENUMERATION: 0x12,
  };

  const PRODUCT_DETAIL_NAMES = {
    0x0600: 'Splitter', 0x0601: 'Ethernet Node', 0x0602: 'Merge', 0x0603: 'Datapatch',
    0x0604: 'Wireless Link', 0x0701: 'Protocol Converter', 0x0702: 'Analog Demultiplex',
    0x0703: 'Analog Multiplex', 0x0704: 'Switch Panel', 0x0800: 'Router', 0x0801: 'Fader',
    0x0802: 'Mixer',
  };

  const SETTLE_MS = 300; // task ask: "~250-400ms"

  // --- shared per-UID caches (task ask: "reuse the existing per-UID
  // caches") — single source of truth for both Devices and Rig Walk. ---
  const infoCache = {};     // uid -> { deviceInfo, loading }
  const paramsCache = {};   // uid -> { lbl, di, pers, personalityDescs, identOn, dimmer:{...}, descriptors, values, introspecting, progress, loading }
  const sensorsCache = {};  // uid -> { readings, loading, error }
  const statusCache = {};   // uid -> { filter, messages, loading, error }
  // serviceLifeCache/actionsCache: Phase D task 2/3's dedicated
  // service-life fields and destructive-action capability surface — see
  // ensureServiceLife/ensureActions below.
  const serviceLifeCache = {}; // uid -> { loading, loaded, error, deviceHours, lampHours, lampStrikes, lampState, devicePowerCycles }
  const actionsCache = {};     // uid -> { loading, loaded, error, resetDevice, factoryDefaults, lastNote }
  // deviceControlCache: E1.20 §10.11 "Device Control" — the four
  // owner-promoted controls beyond IDENTIFY_DEVICE/RESET_DEVICE (already
  // covered above): POWER_STATE, PERFORM_SELFTEST (+ its SELF_TEST_
  // DESCRIPTION/SELFTEST_ENHANCED companions, folded server-side into
  // selfTests), CAPTURE_PRESET, PRESET_PLAYBACK. Same best-effort,
  // independently-optional shape as serviceLifeCache — see
  // ensureDeviceControl/renderDeviceControlSection below.
  const deviceControlCache = {}; // uid -> { loading, loaded, error, powerState, selfTestActive, selfTests, selfTestsKnown, presetPlayback, capturePresetSupported, lastNote }
  // networkCache: E1.37-2 IPv4 & DNS Configuration — GET /network's
  // best-effort bundle (see internal/web/network.go's networkJSON). Same
  // Known/Supported-discriminates-absence shape as actionsCache: a device
  // that doesn't advertise LIST_INTERFACES at all (most fixtures) simply
  // never gets this section rendered — "absent, not broken" (task brief).
  const networkCache = {}; // uid -> { loading, loaded, error, known, supported, interfaces, dns, lastNote }
  // networkEditState: per-uid staged edits for the network section's own
  // fields — initialized once from the first successful GET /network (see
  // initNetworkEditState) and never clobbered by a later re-fetch, same
  // "oninput mutates staged state only" rule every other editable field in
  // this app follows.
  const networkEditState = {}; // uid -> { interfaces: { [id]: {ip, mask, applied} }, dnsHostname, dnsDomain }
  // supportedCache: Phase D task 3's client-side probe gate — GET
  // /supported-parameters resolved (and cached) once per uid, never
  // refetched on a later render (task ask: "cache per device so you don't
  // re-fetch on every render"), unlike infoCache/paramsCache's own base
  // fields, which deliberately DO refresh on every ensureInfo/ensureParams
  // call — SUPPORTED_PARAMETERS is a firmware-scoped fact for the life of
  // this session, not something that changes render to render.
  const supportedCache = {};   // uid -> { resolved, known, pids: Set<hex>, promise }
  // personalityDescCache: uid -> { [personalityIndex]: {Index, DMXFootprint,
  // Description} }, shared between the Info section (current personality
  // only, task ask: cheap default) and the Parameters section's dropdown /
  // "Show all" action (task ask: full list only on explicit request) so the
  // two never independently GET the same index — a single by-index cache
  // is the single source of truth, same rule as the other per-UID caches.
  const personalityDescCache = {};

  // fetchPersonalityDescription issues (or reuses a cached) GET
  // DMX_PERSONALITY_DESCRIPTION for one index. Never checks stillCurrent
  // itself — callers do that after awaiting, before touching their own
  // state — because the result is still worth caching even for a selection
  // that moved on meanwhile (the device didn't stop having that personality).
  async function fetchPersonalityDescription(uid, index) {
    const cache = personalityDescCache[uid] || (personalityDescCache[uid] = {});
    if (index in cache) return cache[index];
    const res = await Api.getParam(uid, 'dmx_personality_description', { index });
    cache[index] = res.value;
    return res.value;
  }

  let selectedUID = null;
  let selectGen = 0;
  let settleTimer = null;
  let sensorsSubscribedUID = null;
  // liveFieldState closes the residual focus-race left open by the previous
  // round's captureFieldFocus/restoreFieldFocus fix: that mechanism only
  // samples document.activeElement at the instant a section tears down, so
  // a native blur — e.g. clicking Introspect while Device Label has an
  // uncommitted edit — moves activeElement to the clicked button *before*
  // that button's own handler fires notify('params'), and the in-progress
  // draft is lost with nothing ever having captured it. liveFieldState is
  // kept current independently of momentary focus: updated on every 'input'
  // (tracks the latest keystroke) and 'focusin' (gives a freshly-clicked
  // field a baseline before it's typed in) on any [data-b5-field] inside the
  // Parameters container, via trackLiveFieldState below. captureFieldFocus
  // falls back to it whenever activeElement isn't one of our own fields.
  let liveFieldState = null;
  let liveFieldTrackedContainer = null;
  // destructiveArmed: Phase D task 3's arm-then-confirm gate for warm/cold
  // RESET_DEVICE and FACTORY_DEFAULTS — same shape as the Devices screen's
  // clear-devices arm/confirm (devices.js) and the node IP editor's
  // Apply->Arm->Confirm gate (nodes.js), reimplemented here rather than
  // shared because those two files are owned by other concurrent work.
  // null | { uid, kind: 'warm'|'cold'|'factory' }. Auto-disarms after
  // DESTRUCTIVE_ARM_TIMEOUT_MS of inactivity, and disarms immediately on
  // any device reselect (see select() below) so a confirm click can never
  // land on a device the tech has since navigated away from.
  let destructiveArmed = null;
  let destructiveBusy = false;
  let destructiveArmTimer = null;
  const DESTRUCTIVE_ARM_TIMEOUT_MS = 8000;
  // controlArmed: the same arm-then-confirm gate as destructiveArmed above,
  // generalized for the E1.20 §10.11 device-control actions (POWER_STATE /
  // PERFORM_SELFTEST / CAPTURE_PRESET / PRESET_PLAYBACK) — a separate
  // variable rather than reusing destructiveArmed because that one's kind
  // values ('warm'/'cold'/'factory') and confirmDestructive's dispatch are
  // owned by the Reset & factory defaults panel; this section's actions are
  // shaped differently (each carries a payload — the value/test#/scene/
  // mode being applied — and a caller-supplied confirm-button label, not a
  // fixed per-kind sentence). null | { uid, kind, payload, label }. kind is
  // 'power' | 'preset' | 'capture' | 'selftest:<N>' | 'selftest:manual' —
  // the per-test-number suffix on 'selftest:*' lets each self test's own
  // Run button arm independently of the others (only one is ever armed at
  // once, same as every other arm-then-confirm gate in this app, but the
  // suffix means confirming test 2 can never accidentally read as test 1's
  // confirm because a re-render happened to still show test 1's label
  // stale). Auto-disarms after DESTRUCTIVE_ARM_TIMEOUT_MS, and disarms on
  // any device reselect (see select() below) — same rules as destructiveArmed.
  let controlArmed = null;
  let controlBusy = false;
  let controlArmTimer = null;
  // networkArmed: the same arm-then-confirm gate as destructiveArmed/
  // controlArmed above, for the E1.37-2 network-config writes (Task 2's
  // "everything that writes is behind arm-then-confirm" rule — a mis-set
  // static IP or DHCP toggle can strand this device off the show network,
  // the same stranding risk nodes.js's own IP-config editor guards
  // against). null | { uid, kind: 'static'|'dhcp', id, payload, label }.
  let networkArmed = null;
  let networkBusy = false;
  let networkArmTimer = null;
  // subscribers: (scope) => void, called after any async cache update.
  // scope names which section's data just changed — 'info' | 'params' |
  // 'sensors' | 'status' — so a caller showing only one of those at a time
  // (Devices tab's active tab; Rig Walk's expanded accordions) can update
  // just that section instead of tearing down and rebuilding everything on
  // screen for a change that section doesn't even display. This is the fix
  // for the bench report "Device Label kicks you out mid-typing": before
  // this, a 'sensors'-only update (the periodic WS sensor poll, subscribed
  // as soon as the Sensors section/accordion has ever been opened for this
  // device) called every subscriber unscoped, and both callers responded by
  // rebuilding the *entire currently visible* section/screen regardless of
  // whether it had anything to do with sensors — which, when that section
  // was Parameters, replaced the Device Label <input> out from under
  // whatever the tech was mid-typing. scope is omitted (undefined) only for
  // updates that genuinely don't know which single section changed; callers
  // treat that as "safe to do a full refresh" rather than skip it.
  const subscribers = [];

  function onUpdate(fn) { subscribers.push(fn); }
  function notify(scope) { subscribers.forEach(fn => { try { fn(scope); } catch (e) { /* one bad subscriber must not break the others */ } }); }

  function stillCurrent(uid, gen) { return uid === selectedUID && gen === selectGen; }

  // --- focus/caret preservation across a re-render that must still happen
  // within the SAME section (task ask: "preserving focus and caret position
  // across a re-render that genuinely must happen is legitimate") ---------
  //
  // Keyed off data-b5-field, stamped by UI.buildApplyField's opts.name (or
  // set by hand on a field devicedetail.js builds itself, e.g. the generic
  // manufacturer-PID rows and the minimum-level pair). captureFieldFocus is
  // called immediately before a section's container is torn down;
  // restoreFieldFocus after it's rebuilt. A field the rebuild didn't
  // recreate (e.g. it became disabled/removed) is simply not restored to —
  // there's nothing to clobber in that case anyway.
  function captureFieldFocus(container) {
    const active = document.activeElement;
    if (active && container.contains(active) && active.dataset && active.dataset.b5Field) {
      const state = { name: active.dataset.b5Field, value: active.value };
      if (typeof active.selectionStart === 'number') {
        state.selectionStart = active.selectionStart;
        state.selectionEnd = active.selectionEnd;
      }
      return state;
    }
    // activeElement isn't one of our fields — most often because a native
    // blur already fired (clicking Introspect, clicking another field's
    // label, etc.). Fall back to whatever liveFieldState last recorded for
    // a field that still exists in this container, so an edit-in-progress
    // survives a rebuild it didn't itself trigger. Confirmed stale states
    // can't leak in: liveFieldState is reset to null on every device change
    // (see select()), and the querySelector below only matches a field this
    // exact container currently owns.
    if (liveFieldState && container.querySelector(`[data-b5-field="${CSS.escape(liveFieldState.name)}"]`)) {
      return liveFieldState;
    }
    return null;
  }

  // trackLiveFieldState wires the delegated 'focusin'/'input' listeners that
  // keep liveFieldState current (see its doc comment above). container is a
  // stable node across re-renders — renderParamsSection only replaces its
  // children — so this only needs to bind once; guarded here rather than in
  // every renderParamsSection call.
  function trackLiveFieldState(container) {
    if (liveFieldTrackedContainer === container) return;
    liveFieldTrackedContainer = container;
    const capture = (e) => {
      const t = e.target;
      if (!t || !t.dataset || !t.dataset.b5Field) return;
      const state = { name: t.dataset.b5Field, value: t.value };
      if (typeof t.selectionStart === 'number') {
        state.selectionStart = t.selectionStart;
        state.selectionEnd = t.selectionEnd;
      }
      liveFieldState = state;
    };
    container.addEventListener('focusin', capture);
    container.addEventListener('input', capture);
  }

  function restoreFieldFocus(container, state) {
    if (!state) return;
    let el;
    try { el = container.querySelector(`[data-b5-field="${CSS.escape(state.name)}"]`); } catch (e) { return; }
    if (!el) return;
    el.value = state.value;
    el.focus();
    if (typeof state.selectionStart === 'number' && el.setSelectionRange) {
      try { el.setSelectionRange(state.selectionStart, state.selectionEnd); } catch (e) { /* some input types (e.g. number) don't support selection ranges */ }
    }
    // Re-run the field's own dirty/Apply-button bookkeeping (wireApplyField/
    // wireRowApply both listen for 'input') so the restored draft shows as
    // unsaved/Apply-enabled exactly as it did before the rebuild, not as a
    // freshly-loaded baseline value.
    el.dispatchEvent(new Event('input', { bubbles: true }));
  }

  // select is the fetch/subscription entry point — see file doc comment.
  function select(uid, sections) {
    sections = sections || {};
    if (uid !== selectedUID) {
      unsubscribeSensors();
      disarmDestructive(); // an armed warm/cold/factory-reset confirm belongs to the device it was armed against, never carried to the next selection
      disarmControl(); // ditto for an armed §10.11 device-control confirm (power state / self test / preset)
      disarmNetwork(); // ditto for an armed E1.37-2 network-config confirm (static IP / DHCP)
      selectedUID = uid;
      selectGen++;
      liveFieldState = null; // a draft belongs to the device it was typed against, never carried to the next selection
      if (settleTimer) { clearTimeout(settleTimer); settleTimer = null; }
      if (!uid) return;
      const gen = selectGen;
      settleTimer = setTimeout(() => {
        settleTimer = null;
        if (gen !== selectGen) return; // superseded before settling — never fires the probe fan-out
        applySections(uid, gen, sections);
      }, SETTLE_MS);
      return;
    }
    // Same device: an accordion opened or a tab switched — apply
    // immediately (deliberate user action on an already-settled selection,
    // not the rapid-Next case the debounce guards against).
    applySections(uid, selectGen, sections);
  }

  function deselect() { select(null, {}); }

  function applySections(uid, gen, sections) {
    if (sections.info) ensureInfo(uid, gen);
    if (sections.params) ensureParams(uid, gen);
    if (sections.sensors) { ensureSensors(uid, gen); subscribeSensors(uid); }
    if (sections.status) ensureStatus(uid, gen);
  }

  function subscribeSensors(uid) {
    if (sensorsSubscribedUID === uid) return;
    if (sensorsSubscribedUID) Live.send({ type: 'unsubscribe_sensors', uid: sensorsSubscribedUID });
    sensorsSubscribedUID = uid;
    Live.send({ type: 'subscribe_sensors', uid });
  }
  function unsubscribeSensors() {
    if (!sensorsSubscribedUID) return;
    Live.send({ type: 'unsubscribe_sensors', uid: sensorsSubscribedUID });
    sensorsSubscribedUID = null;
  }

  // --- Phase D task 3: client-side speculative-probe gate -------------------
  // The server already refuses to put a speculative PID on the wire when
  // this UID's own SUPPORTED_PARAMETERS omits it (params.Client.getRaw's
  // ensureAdvertised gate) — so RDM traffic itself was already protected
  // before this file changed. What this section fixes is the browser side
  // of the same bug (report bench finding: "191 wasted requests in one
  // bench session"): devicedetail.js was still firing the HTTP request
  // itself, unconditionally, for every optional PID on every device-detail
  // load, and eating a guaranteed-fail round-trip for it. ensureSupportedSet
  // resolves (and, unlike ensureInfo/ensureParams's own base fields, PERMANENTLY
  // caches per uid — task ask: "cache per device so you don't re-fetch on
  // every render") this UID's advertised-PID set so callers can skip the
  // request entirely rather than send it and wait for a guaranteed error.
  async function ensureSupportedSet(uid) {
    let c = supportedCache[uid];
    if (c && c.resolved) return c;
    if (c && c.promise) return c.promise;
    c = supportedCache[uid] = supportedCache[uid] || {};
    c.promise = Api.getSupportedParameters(uid).then((res) => {
      c.resolved = true;
      c.known = !!res.known;
      c.pids = new Set((res.pids || []).map((p) => p.toUpperCase()));
      delete c.promise;
      return c;
    }, () => {
      // The GET itself failing (network hiccup, etc.) is not the device
      // telling us anything — fall back to "unknown", same as a device
      // that NACKs/times out on SUPPORTED_PARAMETERS server-side.
      c.resolved = true;
      c.known = false;
      c.pids = new Set();
      delete c.promise;
      return c;
    });
    return c.promise;
  }

  // pidProbeAllowed: false only when `supported` positively confirms pidHex
  // is NOT in this UID's own SUPPORTED_PARAMETERS. Critical fallback (task
  // brief, verbatim): "when known:false ... fall back to today's behavior
  // — do not turn an optimization into a regression". A device whose
  // SUPPORTED_PARAMETERS couldn't be resolved at all always returns true
  // here, exactly like every PID did before this gate existed.
  function pidProbeAllowed(supported, pidHex) {
    if (!supported || !supported.known) return true;
    return supported.pids.has(pidHex.toUpperCase());
  }

  // --- Phase D task 3: destructive-action arm-then-confirm ------------------

  function armDestructive(uid, kind) {
    destructiveArmed = { uid, kind };
    clearTimeout(destructiveArmTimer);
    destructiveArmTimer = setTimeout(disarmDestructive, DESTRUCTIVE_ARM_TIMEOUT_MS);
    if (uid === selectedUID) notify('params');
  }

  function disarmDestructive() {
    if (!destructiveArmed) return;
    destructiveArmed = null;
    clearTimeout(destructiveArmTimer);
    destructiveArmTimer = null;
    notify('params');
  }

  // confirmDestructive fires the armed action's actual write — mirrors
  // devices.js's confirmClear(). Sends the server's required
  // {"confirm":"RESET"} body (Api.resetDevice/setFactoryDefaults do this
  // unconditionally; the arm step above is what actually protects the
  // click on this side).
  async function confirmDestructive(uid, statusSetter) {
    if (!destructiveArmed || destructiveArmed.uid !== uid) return;
    const kind = destructiveArmed.kind;
    clearTimeout(destructiveArmTimer);
    destructiveArmTimer = null;
    destructiveBusy = true;
    notify('params');
    const ac = actionsCache[uid] || (actionsCache[uid] = {});
    try {
      const res = kind === 'factory' ? await Api.setFactoryDefaults(uid) : await Api.resetDevice(uid, kind);
      ac.lastNote = res.note || 'Done.';
      statusSetter(kind === 'factory' ? 'factory defaults sent' : `reset (${kind}) sent`);
    } catch (e) {
      ac.lastNote = 'Error: ' + e.message;
      statusSetter('error: ' + e.message);
    }
    destructiveBusy = false;
    destructiveArmed = null;
    notify('params');
  }

  // --- E1.20 §10.11 device-control arm-then-confirm --------------------------
  // Same shape as armDestructive/disarmDestructive/confirmDestructive above,
  // generalized to carry an arbitrary payload + confirm-button label per
  // action rather than a fixed kind->sentence mapping — see controlArmed's
  // doc comment for why this is a separate gate.
  function armControl(uid, kind, payload, label) {
    controlArmed = { uid, kind, payload, label };
    clearTimeout(controlArmTimer);
    controlArmTimer = setTimeout(disarmControl, DESTRUCTIVE_ARM_TIMEOUT_MS);
    if (uid === selectedUID) notify('params');
  }

  function disarmControl() {
    if (!controlArmed) return;
    controlArmed = null;
    clearTimeout(controlArmTimer);
    controlArmTimer = null;
    notify('params');
  }

  // confirmControl fires the armed action's actual write, dispatching on
  // controlArmed.kind's prefix (kind for self tests carries a per-test
  // suffix — see controlArmed's doc comment — so this matches by prefix,
  // not exact equality).
  async function confirmControl(uid, statusSetter) {
    if (!controlArmed || controlArmed.uid !== uid) return;
    const { kind, payload } = controlArmed;
    clearTimeout(controlArmTimer);
    controlArmTimer = null;
    controlBusy = true;
    notify('params');
    const dc = deviceControlCache[uid] || (deviceControlCache[uid] = {});
    try {
      if (kind === 'power') await Api.setPowerState(uid, payload.value);
      else if (kind.startsWith('selftest:')) await Api.setSelfTest(uid, payload.test);
      else if (kind === 'preset') await Api.setPresetPlayback(uid, payload.mode, payload.level);
      else if (kind === 'capture') await Api.capturePreset(uid, payload.scene, payload.timing);
      dc.lastNote = null;
      statusSetter('applied');
    } catch (e) {
      dc.lastNote = 'Error: ' + e.message;
      statusSetter('error: ' + e.message);
    }
    controlBusy = false;
    controlArmed = null;
    dc.loading = false; // allow ensureDeviceControl to run again and re-baseline
    if (uid === selectedUID) ensureDeviceControl(uid, selectGen);
    notify('params');
  }

  // --- E1.37-2 network configuration arm-then-confirm ------------------------
  // Same shape as armControl/disarmControl/confirmControl above.
  function armNetwork(uid, kind, id, payload, label) {
    networkArmed = { uid, kind, id, payload, label };
    clearTimeout(networkArmTimer);
    networkArmTimer = setTimeout(disarmNetwork, DESTRUCTIVE_ARM_TIMEOUT_MS);
    if (uid === selectedUID) notify('params');
  }

  function disarmNetwork() {
    if (!networkArmed) return;
    networkArmed = null;
    clearTimeout(networkArmTimer);
    networkArmTimer = null;
    notify('params');
  }

  async function confirmNetwork(uid, statusSetter) {
    if (!networkArmed || networkArmed.uid !== uid) return;
    const { kind, id, payload } = networkArmed;
    clearTimeout(networkArmTimer);
    networkArmTimer = null;
    networkBusy = true;
    notify('params');
    const st = networkCache[uid] || (networkCache[uid] = {});
    try {
      const res = kind === 'dhcp'
        ? await Api.setNetworkDHCP(uid, id, payload.enable)
        : await Api.setNetworkStatic(uid, id, payload.ip, payload.mask);
      st.lastNote = res.note || 'Sent and accepted.';
      statusSetter('network config sent');
      const es = networkEditState[uid];
      if (es && es.interfaces[id]) es.interfaces[id].applied = false;
    } catch (e) {
      st.lastNote = 'Error: ' + e.message;
      statusSetter('error: ' + e.message);
    }
    networkBusy = false;
    networkArmed = null;
    st.loading = false; // allow ensureNetwork to run again and re-baseline current/static values
    if (uid === selectedUID) ensureNetwork(uid, selectGen);
    notify('params');
  }

  // initNetworkEditState seeds this uid's staged edit fields from the first
  // successful GET /network — once only (never re-clobbered by a later
  // background refresh, exactly like every other editable field's
  // baseline in this file).
  function initNetworkEditState(uid, res) {
    if (networkEditState[uid]) return;
    const es = networkEditState[uid] = {
      interfaces: {},
      dnsHostname: (res.dns && res.dns.hostnameKnown) ? res.dns.hostname : '',
      dnsDomain: (res.dns && res.dns.domainKnown) ? res.dns.domain : '',
    };
    (res.interfaces || []).forEach((ifc) => {
      es.interfaces[ifc.id] = {
        ip: ifc.staticKnown ? ifc.staticIp : '',
        mask: ifc.staticKnown ? ifc.staticMask : '',
        applied: false,
      };
    });
  }

  async function ensureNetwork(uid, gen) {
    const st = networkCache[uid] || (networkCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    try {
      const res = await Api.getDeviceNetwork(uid);
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.known = res.known;
      st.supported = res.supported;
      st.interfaces = res.interfaces || [];
      st.dns = res.dns || { nameServers: [] };
      st.loading = false;
      st.loaded = true;
      st.error = null;
      initNetworkEditState(uid, res);
    } catch (e) {
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.loading = false;
      st.error = e.message;
    }
    if (stillCurrent(uid, gen)) notify('params');
  }

  // --- Service life (Phase D task 2) ----------------------------------------
  // Independently-optional per field (E1.20 §10.8's DEVICE_HOURS/LAMP_HOURS/
  // LAMP_STRIKES/LAMP_STATE/DEVICE_POWER_CYCLES), same pattern the E1.37-1
  // dimmer fields already use: one GET /service-life bundles all five
  // best-effort server-side (see internal/web/device.go's serviceLifeJSON),
  // so this needs no per-field client fan-out or gating of its own.
  async function ensureServiceLife(uid, gen) {
    const st = serviceLifeCache[uid] || (serviceLifeCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    try {
      const res = await Api.getServiceLife(uid);
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      Object.assign(st, res, { loading: false, loaded: true, error: null });
    } catch (e) {
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.loading = false;
      st.error = e.message;
    }
    if (stillCurrent(uid, gen)) notify('params');
  }

  async function saveServiceLifeField(uid, field, value, statusSetter) {
    await Api.setServiceLife(uid, field, value);
    statusSetter('applied ' + field);
    const st = serviceLifeCache[uid];
    if (st) st.loading = false; // allow ensureServiceLife to run again and re-baseline
    if (uid === selectedUID) ensureServiceLife(uid, selectGen);
  }

  // --- Destructive-action capability surface (Phase D task 3) ---------------
  // GET /actions resolves (and server-side caches) whether RESET_DEVICE/
  // FACTORY_DEFAULTS are advertised at all. known:false MUST read as "don't
  // know" here too — see deviceActionsJSON's doc comment — so a device this
  // hasn't resolved for yet simply shows neither button rather than a false
  // "not supported".
  async function ensureActions(uid, gen) {
    const st = actionsCache[uid] || (actionsCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    try {
      const res = await Api.getDeviceActions(uid);
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.resetDevice = res.resetDevice;
      st.factoryDefaults = res.factoryDefaults;
      st.loaded = true;
      st.error = null;
    } catch (e) {
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.error = e.message;
    }
    st.loading = false;
    if (stillCurrent(uid, gen)) notify('params');
  }

  // --- E1.20 §10.11 "Device Control" -----------------------------------------
  // One GET bundles POWER_STATE / PERFORM_SELFTEST (active flag) / the
  // SELFTEST_ENHANCED-derived self-test roster (with SELF_TEST_DESCRIPTION
  // labels already resolved server-side) / PRESET_PLAYBACK / whether
  // CAPTURE_PRESET is advertised — same best-effort bundling as
  // ensureServiceLife, so this needs no per-field client fan-out either.
  async function ensureDeviceControl(uid, gen) {
    const st = deviceControlCache[uid] || (deviceControlCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    try {
      const res = await Api.getDeviceControl(uid);
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      Object.assign(st, res, { loading: false, loaded: true, error: null });
    } catch (e) {
      if (!stillCurrent(uid, gen)) { st.loading = false; return; }
      st.loading = false;
      st.error = e.message;
    }
    if (stillCurrent(uid, gen)) notify('params');
  }

  // --- Info section --------------------------------------------------------

  async function ensureInfo(uid, gen) {
    const st = infoCache[uid] || (infoCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    // Phase D task 4: PRODUCT_DETAIL_ID_LIST (0070) is optional (never in
    // E1.20 Table A-3's required list) and was one of the three unconditional
    // speculative-probe sites the bench report flagged — gate it on this
    // UID's own SUPPORTED_PARAMETERS, resolved in parallel with the four
    // always-safe core fields below so a device with no cached answer yet
    // pays no extra latency for it.
    const supportedP = ensureSupportedSet(uid);
    const corePromise = Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'manufacturer_label'),
      Api.getParam(uid, 'device_model_description'),
      Api.getParam(uid, 'software_version_label'),
    ]);
    const supported = await supportedP;
    if (!stillCurrent(uid, gen)) return;
    const prodDetailPromise = pidProbeAllowed(supported, '0070')
      ? Api.getDeviceParam(uid, '0070').then((v) => ({ status: 'fulfilled', value: v }), (e) => ({ status: 'rejected', reason: e }))
      : Promise.resolve({ status: 'rejected', reason: new Error('not advertised') });
    const [deviceInfo, mfrLabel, model, swVersion] = await corePromise;
    const prodDetail = await prodDetailPromise;
    if (!stillCurrent(uid, gen)) return;
    st.loading = false;
    st.deviceInfo = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    st.mfrLabelVal = mfrLabel.status === 'fulfilled' ? mfrLabel.value.value : '';
    st.modelVal = model.status === 'fulfilled' ? model.value.value : '';
    st.swVersion = swVersion.status === 'fulfilled' ? swVersion.value.value : '';
    st.prodDetailHex = prodDetail.status === 'fulfilled' ? prodDetail.value.hex : '';
    notify('info');

    // DMX_PERSONALITY_DESCRIPTION for the CURRENT personality only (task
    // ask, owner's real-world complaint: a bare "Personality 5 of 9" gave
    // no way to see which mode that is or reconcile it with the fixture's
    // own menu). Deliberately one GET, not one per personality — these
    // devices sit behind a bandwidth-constrained wireless proxy that is
    // already saturating, so the full 1..Count mode list is fetched only on
    // explicit request (see loadAllPersonalityLabels / the Parameters
    // section's "Show all modes" action), never eagerly on every panel
    // open. Best-effort: a device that NACKs this PID just keeps the bare
    // "5 of 9" fallback in renderInfoSection.
    const di = st.deviceInfo;
    if (di && di.CurrentPersonality) {
      try {
        const desc = await fetchPersonalityDescription(uid, di.CurrentPersonality);
        if (!stillCurrent(uid, gen)) return;
        st.currentPersonalityDesc = desc;
      } catch (e) {
        if (!stillCurrent(uid, gen)) return;
        st.currentPersonalityDesc = null;
      }
      notify('info');
    }
  }

  // noteDeviceInfo lets a caller that independently fetched DEVICE_INFO for
  // some other reason (Devices screen's background classification sweep,
  // which needs DEVICE_INFO/PRODUCT_DETAIL_ID_LIST for every "Unknown"-
  // class device regardless of whether its detail panel is open) feed the
  // result into the same shared cache the Info section reads from — so the
  // Devices table's address column populates without waiting for the user
  // to open that device's detail panel, exactly like before this refactor.
  function noteDeviceInfo(uid, deviceInfo) {
    const st = infoCache[uid] || (infoCache[uid] = {});
    st.deviceInfo = deviceInfo;
  }

  function infoRow(label, valueHtml) {
    return `<div><span class="b5-text-muted b5-text-sm">${escapeHtml(label)}</span><br>${valueHtml}</div>`;
  }

  // formatPersonalitySummary renders the owner-actionable line (task ask:
  // `Personality 5/9 — "13ch Extended" (13 slots)`) when DMX_PERSONALITY_
  // DESCRIPTION resolved for the current personality, falling back to the
  // old bare "5 of 9" when it hasn't (still loading, or the device NACKed
  // it — most fixtures do implement it, but it's not universal).
  function formatPersonalitySummary(di, desc) {
    const base = `${di.CurrentPersonality} of ${di.PersonalityCount}`;
    if (!desc || !desc.Description) return base;
    return `Personality ${di.CurrentPersonality}/${di.PersonalityCount} — "${desc.Description}" (${desc.DMXFootprint} slots)`;
  }

  function renderInfoSection(container, f) {
    const uid = f.uid;
    const st = infoCache[uid];
    if (!st || (st.loading && st.deviceInfo === undefined)) {
      container.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading…</span>`;
      return;
    }
    const di = st.deviceInfo;
    let detailsHtml = '<span class="b5-text-muted b5-text-sm">none reported</span>';
    if (st.prodDetailHex) {
      const hex = st.prodDetailHex;
      const names = [];
      for (let i = 0; i + 4 <= hex.length; i += 4) {
        const code = parseInt(hex.slice(i, i + 4), 16);
        names.push(PRODUCT_DETAIL_NAMES[code] || `0x${code.toString(16).toUpperCase().padStart(4, '0')}`);
      }
      if (names.length) detailsHtml = names.map(n => UI.tag(n)).join(' ');
    }
    const effectiveMfr = st.mfrLabelVal || f.manufacturerName;
    const modelText = st.modelVal || (di ? `0x${di.DeviceModelID.toString(16).toUpperCase().padStart(4, '0')}` : '—');
    // Proxy status badge (Phase D task 1): PROXIED_DEVICES/PROXIED_DEVICE_
    // COUNT are Hidden-tier now (never a generic-editor row), but the owner
    // asked that the fact not vanish — surfaced here from the same
    // structured fixtureJSON fields the Devices table's classification
    // sweep already populates (f.proxiedDeviceCount/.proxiedDeviceCountKnown/
    // .proxiedListChanged). A UI.tag, never a bare color swatch (design-spec
    // "never color as the sole signal") — the text itself carries the fact.
    let proxyBadgeHtml = '';
    if (f.proxiedDeviceCountKnown && f.proxiedDeviceCount > 0) {
      const n = f.proxiedDeviceCount;
      proxyBadgeHtml = `<div style="margin-bottom:var(--b5-space-3)">${UI.tag(`Proxy — ${n} device${n === 1 ? '' : 's'}`, 'info')}${f.proxiedListChanged ? ' ' + UI.badge('warning', 'Proxied device list changed — re-discover to refresh it') : ''}</div>`;
    }
    // f.portAddress is the canonical, 0-based Art-Net Port-Address — routed
    // through UI.formatUniverse so this line matches the same fixture's
    // "U<n>" cell on the Devices table (devices.js) and the node/port
    // picker's label at whatever display base Settings has active. A raw,
    // unshifted number here was the exact bug class the owner has already
    // flagged twice elsewhere: this row and the Devices table row it sits
    // directly beneath disagreeing about the same fixture's universe at any
    // base other than 0.
    container.innerHTML = `
      ${proxyBadgeHtml}
      <div class="b5-grid-2">
        ${infoRow('Manufacturer', `${escapeHtml(effectiveMfr)} (0x${f.manufacturerId.toString(16).toUpperCase().padStart(4, '0')})`)}
        ${infoRow('Model / fixture type', escapeHtml(modelText))}
        ${infoRow('Manufacturer label (device-reported)', st.mfrLabelVal ? escapeHtml(st.mfrLabelVal) : '<span class="b5-text-muted b5-text-sm">not reported by device</span>')}
        ${infoRow('Software version', st.swVersion ? escapeHtml(st.swVersion) : '—')}
        ${infoRow('Node / port', `${escapeHtml(f.nodeIp)} (bind ${f.bindIndex}) / universe ${UI.formatUniverse(f.portAddress)}`)}
        ${infoRow('DMX footprint', di ? String(di.DMXFootprint) : '—')}
        ${infoRow('DMX start address', di ? escapeHtml(Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true)) : '—')}
        ${infoRow('Personality', di ? escapeHtml(formatPersonalitySummary(di, st.currentPersonalityDesc)) : '—')}
        ${infoRow('Sub-devices', di ? String(di.SubDeviceCount) : '—')}
        ${infoRow('Sensors', di ? String(di.SensorCount) : '—')}
      </div>
      <div style="margin-top:var(--b5-space-4)">
        <span class="b5-text-muted b5-text-sm">Product details</span><br>${detailsHtml}
      </div>
    `;
  }

  // --- Parameters section: standard PIDs + E1.37-1 dimmer PIDs + generic
  // manufacturer/ESTA-fallback editor --------------------------------------

  // dimmerFields describes the optional standard E1.37-1 fields probed
  // alongside the always-present label/address/personality/identify quartet
  // (task ask: "typed support for E1.37-1 dimmer PIDs... MINIMUM_LEVEL,
  // MAXIMUM_LEVEL, plus IDENTIFY_MODE if cheap"). Each is independently
  // optional — a device that NACKs a given PID (most fixtures won't
  // implement all of these) simply omits that row rather than showing an
  // error, exactly like the always-probed base fields already do via
  // Promise.allSettled.
  // SPECULATIVE_PARAM_FETCHERS names the six optional E1.37-1/E1.20 PIDs
  // this section fans out speculatively, paired with the 4-hex PID each
  // corresponds to (see internal/rdm/message.go / pids_ext.go) so
  // pidProbeAllowed can gate each individually against this UID's own
  // SUPPORTED_PARAMETERS (Phase D task 3 — the bench report's second named
  // site). This list is NOT the tier/visibility mechanism Task 1 forbids
  // hardcoding — it exists only because these six specifically get typed,
  // dedicated fields (renderDimmerFields) rather than the generic
  // Introspect-driven descriptor path every OTHER PID (including any future
  // owner-promoted one) goes through untouched.
  const SPECULATIVE_PARAM_FETCHERS = [
    { key: 'curve', pid: '0343', name: 'curve' },
    { key: 'ort', pid: '0345', name: 'output_response_time' },
    { key: 'modFreq', pid: '0347', name: 'modulation_frequency' },
    { key: 'minLevel', pid: '0341', name: 'minimum_level' },
    { key: 'maxLevel', pid: '0342', name: 'maximum_level' },
    { key: 'identMode', pid: '1040', name: 'identify_mode' },
  ];

  async function ensureParams(uid, gen) {
    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });
    if (st.loading) return;
    st.loading = true;
    // Phase D task 4: the E1.37-1 dimmer-set + IDENTIFY_MODE fan-out below
    // was the bench report's largest named speculative-probe site — gated
    // the same way ensureInfo's PRODUCT_DETAIL_ID_LIST fetch is above,
    // resolved in parallel with the four always-safe core fields.
    const supportedP = ensureSupportedSet(uid);
    const corePromise = Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'device_label'),
      Api.getParam(uid, 'dmx_personality'),
      Api.getParam(uid, 'identify_device'),
    ]);
    const supported = await supportedP;
    if (!stillCurrent(uid, gen)) return;
    const specResults = await Promise.allSettled(SPECULATIVE_PARAM_FETCHERS.map((f) =>
      pidProbeAllowed(supported, f.pid) ? Api.getParam(uid, f.name) : Promise.reject(new Error('not advertised'))));
    const [deviceInfo, label, personality, ident] = await corePromise;
    if (!stillCurrent(uid, gen)) return;
    st.loading = false;
    st.di = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    st.lbl = label.status === 'fulfilled' ? label.value.value : '';
    st.pers = personality.status === 'fulfilled' ? personality.value.value : null;
    st.identOn = ident.status === 'fulfilled' ? ident.value.value : false;
    SPECULATIVE_PARAM_FETCHERS.forEach((f, i) => {
      st[f.key] = specResults[i].status === 'fulfilled' ? specResults[i].value.value : null;
    });
    notify('params');

    // Phase D task 2/3: service life + destructive-action capability, each
    // its own independently-cached fetch — fired here (not awaited) so a
    // slow/NACKing device doesn't hold up the fields above.
    ensureServiceLife(uid, gen);
    ensureActions(uid, gen);
    ensureDeviceControl(uid, gen);
    ensureNetwork(uid, gen);

    // Cached descriptors (no wire traffic — matches the pre-existing
    // "Introspect is user-triggered, not automatic" rule, task ask: "never
    // fired automatically for every device during a walk").
    try {
      const descs = await Api.getDeviceParams(uid);
      if (!stillCurrent(uid, gen)) return;
      st.descriptors = descs;
      await loadParamValues(uid, descs, gen, supported);
    } catch (e) { /* best-effort */ }
    if (stillCurrent(uid, gen)) notify('params');

    // Labeled-dropdown companions (the new CURVE_DESCRIPTION/OUTPUT_
    // RESPONSE_TIME_DESCRIPTION/MODULATION_FREQUENCY_DESCRIPTION) — fetched
    // lazily, one GET per index, only once the base value resolved a Count
    // (task ask: "where a companion exists, fetch it and render a labeled
    // dropdown"). Each is independent and best-effort: a device that NACKs
    // the description PID (or doesn't implement it) falls back to a bare
    // numeric dropdown.
    //
    // DMX_PERSONALITY_DESCRIPTION is deliberately NOT in this eager batch
    // (task ask: these devices sit behind a bandwidth-constrained wireless
    // proxy that is already saturating — firing one GET per personality,
    // up to Count of them, on every panel open is exactly the eager
    // fan-out that makes that worse). Only the current personality's
    // description is fetched by default, via ensureInfo's single GET
    // (shared into this cache below); the full list is loaded only on the
    // explicit "Show all personality names" action — see
    // loadAllPersonalityLabels.
    if (st.pers && st.pers.Current) {
      st.personality = st.personality || {};
      if (!(st.pers.Current in st.personality)) {
        try {
          const desc = await fetchPersonalityDescription(uid, st.pers.Current);
          if (!stillCurrent(uid, gen)) return;
          st.personality[st.pers.Current] = desc.Description;
        } catch (e) { /* best-effort: NACK/unimplemented falls back to a bare number */ }
      }
    }
    await Promise.allSettled([
      loadIndexedLabels(uid, gen, 'curveLabels', st.curve, 'curve_description', d => d.Description),
      loadIndexedLabels(uid, gen, 'ortLabels', st.ort, 'output_response_time_description', d => d.Description),
      loadIndexedLabels(uid, gen, 'modFreqLabels', st.modFreq, 'modulation_frequency_description', d => d.Description),
    ]);
    if (stillCurrent(uid, gen)) notify('params');
  }

  // loadAllPersonalityLabels is the explicit user action (task ask: "make
  // the full list an explicit user action") that fetches every remaining
  // personality's DMX_PERSONALITY_DESCRIPTION, one GET per still-unresolved
  // index — the eager fan-out ensureParams/ensureInfo deliberately do not do
  // on their own. Goes through fetchPersonalityDescription's shared cache,
  // so an index the Info section (or an earlier click) already resolved is
  // never re-fetched.
  async function loadAllPersonalityLabels(uid) {
    const st = paramsCache[uid];
    if (!st || !st.pers) return;
    const gen = selectGen;
    st.personality = st.personality || {};
    const need = [];
    for (let i = 1; i <= st.pers.Count; i++) if (!(i in st.personality)) need.push(i);
    if (!need.length) return;
    const results = await Promise.allSettled(need.map(i => fetchPersonalityDescription(uid, i)));
    if (!stillCurrent(uid, gen)) return;
    results.forEach((r, idx) => {
      if (r.status === 'fulfilled') st.personality[need[idx]] = r.value.Description;
    });
    notify('params');
  }

  // loadIndexedLabels fetches `choice.Count` description entries (indices
  // 1..Count) for one "current + count" PID family and stashes them at
  // st[cacheKey] = {1: "Linear", 2: "S-Curve", ...}. DMX_PERSONALITY uses
  // Index+DMXFootprint+Description (personality has a footprint field the
  // dimmer PIDs don't); the three dimmer families use Index+Description —
  // extractLabel isolates the one field every shape needs.
  async function loadIndexedLabels(uid, gen, cacheKey, choice, pidName, extractLabel) {
    if (!choice || !choice.Count) return;
    const st = paramsCache[uid];
    if (!st) return;
    st[cacheKey] = st[cacheKey] || {};
    const need = [];
    for (let i = 1; i <= choice.Count; i++) if (!(i in st[cacheKey])) need.push(i);
    if (!need.length) return;
    const results = await Promise.allSettled(need.map(i => Api.getParam(uid, pidName, { index: i })));
    if (!stillCurrent(uid, gen)) return;
    results.forEach((r, idx) => {
      if (r.status === 'fulfilled') st[cacheKey][need[idx]] = extractLabel(r.value.value);
    });
  }

  // loadParamValues bulk-GETs every descriptor's current value. `supported`
  // (optional — Phase D task 4's third named speculative-probe site) is
  // this UID's already-resolved SUPPORTED_PARAMETERS set, when the caller
  // has one on hand (ensureParams does); when omitted (the introspect_complete
  // WS handler, below — a completed Introspect always means SUPPORTED_
  // PARAMETERS just resolved moments ago server-side, so this is a cheap
  // cache hit, never a fresh probe) it's resolved here instead. Every one of
  // these descriptor PIDs came FROM this same UID's SUPPORTED_PARAMETERS in
  // the first place (Introspect only ever walks it), so in the overwhelming
  // majority of cases this gate is a no-op affirming what's already true;
  // it's real protection only for a manufacturer-scoped descriptor cache hit
  // (descCache is keyed on (manufacturerID, PID), shared across every
  // same-manufacturer device) resolving a PID THIS specific device instance
  // doesn't actually list.
  async function loadParamValues(uid, descs, gen, supported) {
    const st = paramsCache[uid];
    const known = supported || await ensureSupportedSet(uid);
    if (!stillCurrent(uid, gen)) return;
    const results = await Promise.allSettled(descs.map(d =>
      pidProbeAllowed(known, d.pid) ? Api.getDeviceParam(uid, d.pid) : Promise.reject(new Error('not advertised'))));
    if (!stillCurrent(uid, gen)) return;
    results.forEach((r, i) => {
      const pid = descs[i].pid;
      st.values[pid] = r.status === 'fulfilled' ? { ok: true, val: r.value } : { ok: false, err: r.reason.message };
    });
  }

  function introspectStatusText(st) {
    if (st.introspecting && st.progress) return `describing ${st.progress.done}/${st.progress.total} (0x${st.progress.pid})`;
    if (st.introspecting) return 'starting…';
    // Counts only what's actually shown as a row somewhere (Fixture
    // settings above, or this section below) — st.descriptors itself can
    // additionally hold a Hidden-tier entry resolved by some unrelated
    // on-demand fetch (see the rowsEl fallback-text comment below), which
    // must never inflate this count.
    const shown = st.descriptors.filter(d => d.tier !== 'hidden').length;
    if (shown) return `${shown} parameter(s) known`;
    return 'no manufacturer/unrecognized-PID parameters resolved yet';
  }

  async function startIntrospect(uid) {
    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });
    st.introspecting = true;
    st.progress = null;
    notify('params');
    try {
      await Api.introspectDevice(uid);
    } catch (e) {
      st.introspecting = false;
      notify('params');
    }
    // Completion/progress arrives over WS — see wireLiveUpdates below.
  }

  // renderParamsSection is the Parameters section shared by both consumers.
  // `statusSetter(msg)` lets the caller show Apply/error feedback in its
  // own chrome (Devices: a dedicated status line; Rig Walk: the walk
  // screen's shared status line) without this module owning any global DOM
  // id. `opts.hideAddressField` (Rig Walk only) suppresses this section's
  // own Start-address editor when the caller already renders a dedicated,
  // walk-session-aware address quick-fix elsewhere on screen (walk.js's
  // card header) — task ask: "keep its walk chrome" for that field, so it
  // stays the one address editor in Rig Walk rather than two independent
  // Apply-to-confirm controls silently racing each other.
  function renderParamsSection(container, f, statusSetter, opts) {
    opts = opts || {};
    const uid = f.uid;
    trackLiveFieldState(container);
    const st = paramsCache[uid];
    if (!st || (st.loading && st.di === undefined)) {
      container.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading…</span>`;
      return;
    }
    const di = st.di, lbl = st.lbl || '', pers = st.pers, identOn = !!st.identOn;

    // Captured before the teardown below so a re-render that genuinely must
    // happen within this same section (see the notify() scoping above —
    // this now only fires for 'params'-scoped updates: the base fields
    // resolving, introspect progress/completion for THIS device, an Apply
    // elsewhere in this section) doesn't cost the tech whatever they're
    // mid-typing in one of this section's own fields. Restored at the
    // bottom of this function, once every field below has been rebuilt.
    const focusState = captureFieldFocus(container);

    container.innerHTML = '';
    const standard = document.createElement('div');
    standard.className = 'b5-stack';
    container.appendChild(standard);

    const labelField = UI.buildApplyField({ label: 'Device label', value: lbl, enabled: !!di, maxLength: 32, name: 'device_label' });
    standard.appendChild(labelField.wrap);
    UI.wireApplyField(labelField, lbl, async (v) => { await saveParam(uid, 'device_label', v, statusSetter); }, statusSetter);

    if (!opts.hideAddressField) {
      const addrHint = di ? `Currently ${Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true)}` : undefined;
      const addrField = UI.buildApplyField({
        label: 'Start address', kind: 'number', mono: true, min: 1, max: 512,
        value: di && di.DMXFootprint ? di.DMXStartAddress : '', enabled: !!(di && di.DMXFootprint), hint: addrHint,
        name: 'dmx_start_address',
      });
      standard.appendChild(addrField.wrap);
      UI.wireApplyField(addrField, di && di.DMXFootprint ? String(di.DMXStartAddress) : '', async (v) => {
        await saveParam(uid, 'dmx_start_address', parseInt(v, 10), statusSetter);
      }, statusSetter);
    }

    const personalityLabels = st.personality || {};
    const persField = UI.buildApplyField({
      label: 'Personality', kind: 'select', enabled: !!pers,
      value: pers ? pers.Current : null,
      options: pers ? Array.from({ length: pers.Count }, (_, i) => i + 1).map(i => ({ value: i, label: personalityLabels[i] ? `${i} — ${personalityLabels[i]}` : String(i) })) : [],
      name: 'dmx_personality',
    });
    standard.appendChild(persField.wrap);
    UI.wireApplyField(persField, pers ? String(pers.Current) : '', async (v) => {
      await saveParam(uid, 'dmx_personality', parseInt(v, 10), statusSetter);
    }, statusSetter);

    // "Show all personality names" is the explicit user action for the full
    // 1..Count DMX_PERSONALITY_DESCRIPTION list (task ask: fetching every
    // mode's name is desirable but must not fire on load — these devices sit
    // behind a bandwidth-constrained wireless proxy that is already
    // saturating). Only offered once there's more than one personality to
    // name and at least one is still unresolved; disappears once the
    // dropdown above is fully labeled.
    const labeledCount = Object.keys(personalityLabels).length;
    if (pers && pers.Count > 1 && labeledCount < pers.Count) {
      const loadAllWrap = document.createElement('div');
      loadAllWrap.className = 'b5-row';
      loadAllWrap.style.marginTop = 'calc(var(--b5-space-2) * -1)'; // sits right under the Personality field
      loadAllWrap.innerHTML = `<button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-load-all-personalities">Show all ${pers.Count} personality names</button>`;
      standard.appendChild(loadAllWrap);
      const loadAllBtn = loadAllWrap.querySelector('button');
      loadAllBtn.addEventListener('click', async () => {
        loadAllBtn.disabled = true;
        loadAllBtn.innerHTML = UI.spinner() + 'Loading…';
        await loadAllPersonalityLabels(uid);
        // Re-render happens via notify() -> caller's subscriber; if this
        // exact panel is still mounted with nothing changed (e.g. every
        // fetch failed), leave the button usable again rather than stuck
        // spinning forever.
        loadAllBtn.disabled = false;
        loadAllBtn.textContent = `Show all ${pers.Count} personality names`;
      });
    }

    // fxIdentify is the one deliberate Apply-to-confirm exception (a
    // momentary physical action, not a persisted parameter) — a plain
    // b5-toggle that commits on change, same sign-off as before the retheme.
    const identWrap = document.createElement('div');
    identWrap.className = 'b5-field';
    identWrap.innerHTML = `
      <label class="b5-toggle">
        <input type="checkbox" ${identOn ? 'checked' : ''}>
        <span class="b5-toggle__track"></span>
        Identify (flashes/highlights the physical fixture)
      </label>
    `;
    standard.appendChild(identWrap);
    identWrap.querySelector('input').addEventListener('change', async (e) => {
      try {
        await Api.identify(uid, e.target.checked);
        statusSetter('identify ' + (e.target.checked ? 'on' : 'off'));
      } catch (err) {
        statusSetter('error: ' + err.message);
      }
    });

    renderDimmerFields(standard, uid, st, statusSetter);

    // Phase D task 2: dedicated service-life fields (DEVICE_HOURS/LAMP_
    // HOURS/LAMP_STRIKES/LAMP_STATE/DEVICE_POWER_CYCLES) — own panel, each
    // field independently optional (see renderServiceLifeSection).
    renderServiceLifeSection(container, uid, statusSetter);

    // E1.20 §10.11 "Device Control" — POWER_STATE / self test / preset
    // playback / capture preset, arm-then-confirm (see renderDeviceControlSection).
    renderDeviceControlSection(container, uid, lbl || uid, statusSetter);

    // E1.37-2 IPv4 & DNS Configuration — interfaces/static IP/DHCP/DNS,
    // arm-then-confirm on every write, entirely absent for a device that
    // doesn't advertise LIST_INTERFACES (see renderNetworkSection).
    renderNetworkSection(container, uid, lbl || uid, statusSetter);

    // Phase D task 1: any PID classification.go tags TierPromoted that
    // ALSO doesn't already have a dedicated field above (pan/tilt invert,
    // display invert/level today — whatever the owner tags Promoted next,
    // automatically, with no JS change) gets its own clearly-labeled
    // section instead of being buried alphabetically among dozens of
    // manufacturer PIDs below. Populated only once Introspect has run — see
    // that button's section below — same lazy-on-request timing every
    // OTHER descriptor-sourced field already follows; the change here is
    // placement/prominence once resolved, not when it resolves.
    const promotedDescs = st.descriptors.filter(d => d.tier === 'promoted');
    // TierHidden is never supposed to reach this list at all (isEditorTarget
    // excludes it server-side) — the standard-tier filter below excludes it
    // too, purely as defense in depth, per Task 1: "Hidden -> not rendered
    // as a parameter row at all" must hold even if that server invariant
    // ever slips.
    const standardDescs = st.descriptors.filter(d => d.tier !== 'promoted' && d.tier !== 'hidden');

    if (promotedDescs.length) {
      const promotedSection = document.createElement('div');
      promotedSection.className = 'b5-panel b5-panel--promoted';
      promotedSection.style.marginTop = 'var(--b5-space-5)';
      promotedSection.innerHTML = `
        <div class="b5-panel__header">
          <h3 class="b5-panel__title">Fixture settings</h3>
        </div>
        <div class="b5-panel__body b5-stack promoted-rows"></div>
      `;
      container.appendChild(promotedSection);
      const promotedRowsEl = promotedSection.querySelector('.promoted-rows');
      promotedDescs.slice().sort((a, b) => (a.label || a.pid).localeCompare(b.label || b.pid)).forEach(desc => {
        promotedRowsEl.appendChild(renderParamRow(uid, desc, st.values[desc.pid], statusSetter));
      });
    }

    // Phase D task 3: warm/cold reset + factory defaults, arm-then-confirm.
    renderDestructiveActionsSection(container, uid, lbl || uid, statusSetter);

    const mfrSection = document.createElement('div');
    mfrSection.className = 'b5-panel';
    mfrSection.style.marginTop = 'var(--b5-space-5)';
    mfrSection.innerHTML = `
      <div class="b5-panel__header">
        <h3 class="b5-panel__title">Manufacturer &amp; unrecognized parameters</h3>
        <button class="b5-btn b5-btn--sm btn-introspect" ${st.introspecting ? 'disabled' : ''}>${st.introspecting ? UI.spinner() + 'Introspecting…' : 'Introspect'}</button>
      </div>
      <div class="b5-panel__body b5-stack">
        <span class="b5-text-muted b5-text-sm introspect-status">${escapeHtml(introspectStatusText(st))}</span>
        <div class="param-rows b5-stack"></div>
      </div>
    `;
    container.appendChild(mfrSection);
    mfrSection.querySelector('.btn-introspect').addEventListener('click', () => startIntrospect(uid));

    // Note: st.descriptors can be non-empty here even before Introspect has
    // ever run — a Hidden-tier PID resolved on demand by some OTHER fetch
    // (e.g. ensureInfo's PRODUCT_DETAIL_ID_LIST GET) lands in the server's
    // shared per-UID descriptor cache too, and GET /params returns whatever
    // that cache holds, not only what Introspect put there. Such a PID is
    // filtered out of both standardDescs and promotedDescs above, so this
    // must check those two — not the raw st.descriptors.length — or a
    // device with only a stray Hidden entry would wrongly claim its
    // (nonexistent) promoted fields cover everything.
    const rowsEl = mfrSection.querySelector('.param-rows');
    if (!standardDescs.length && !promotedDescs.length) {
      rowsEl.innerHTML = '<p class="b5-text-muted b5-text-sm">Click Introspect to walk SUPPORTED_PARAMETERS + PARAMETER_DESCRIPTION (also surfaces any standard ESTA PID this app has no typed decoder for, via the same raw-hex fallback).</p>';
    } else if (!standardDescs.length) {
      rowsEl.innerHTML = '<p class="b5-text-muted b5-text-sm">Every parameter Introspect found is shown in a dedicated field above.</p>';
    } else {
      standardDescs.slice().sort((a, b) => a.pid.localeCompare(b.pid)).forEach(desc => {
        rowsEl.appendChild(renderParamRow(uid, desc, st.values[desc.pid], statusSetter));
      });
    }

    restoreFieldFocus(container, focusState);
  }

  // LAMP_STATE's well-known Table A-8 values (E1.20 §10.8.4), for the
  // settable dropdown below. Manufacturer-specific (0x80-0xDF) and any
  // other value this device currently reports gets appended as its own
  // option in renderServiceLifeSection so the dropdown never silently
  // discards the device's actual current value.
  const LAMP_STATE_OPTIONS = [
    { value: 0x00, label: 'Off' }, { value: 0x01, label: 'On' },
    { value: 0x02, label: 'Strike' }, { value: 0x03, label: 'Standby' },
    { value: 0x04, label: 'Not present' }, { value: 0x7F, label: 'Error' },
  ];

  // renderServiceLifeSection appends the Phase D task 2 "service life"
  // panel (DEVICE_HOURS/LAMP_HOURS/LAMP_STRIKES/LAMP_STATE/DEVICE_POWER_
  // CYCLES) — each field independently optional, per serviceLifeCache's
  // best-effort shape (see ensureServiceLife/serviceLifeJSON). A field this
  // device doesn't support is simply omitted (never a spurious zero, never
  // an error banner) — and if EVERY field is unsupported, the whole panel
  // is omitted too (task ask: "degrade gracefully", not "show five
  // 'unsupported' lines for a device none of this applies to").
  function renderServiceLifeSection(outerContainer, uid, statusSetter) {
    const st = serviceLifeCache[uid];
    if (!st || (!st.loaded && !st.error)) return; // still loading, or never fetched (Sensors/Status tab active) — nothing to show yet

    const wrap = document.createElement('div');
    wrap.className = 'b5-stack';
    let any = false;

    const counters = [
      { key: 'deviceHours', label: 'Device hours' },
      { key: 'lampHours', label: 'Lamp hours' },
      { key: 'lampStrikes', label: 'Lamp strikes' },
      { key: 'devicePowerCycles', label: 'Device power cycles' },
    ];
    counters.forEach(c => {
      const fs = st[c.key];
      if (!fs || !fs.known) return;
      any = true;
      const field = UI.buildApplyField({ label: c.label, kind: 'number', enabled: true, value: fs.value, min: 0, max: 4294967295, name: 'servicelife_' + c.key });
      UI.wireApplyField(field, String(fs.value), async (v) => {
        const n = Math.trunc(Number(v));
        if (!Number.isFinite(n) || n < 0 || n > 4294967295) throw new Error('must be a whole number 0-4294967295');
        await saveServiceLifeField(uid, c.key, n, statusSetter);
      }, statusSetter);
      wrap.appendChild(field.wrap);
    });

    const ls = st.lampState;
    if (ls && ls.known) {
      any = true;
      const options = LAMP_STATE_OPTIONS.slice();
      if (!options.some(o => o.value === ls.value)) {
        options.push({ value: ls.value, label: ls.label || `0x${ls.value.toString(16).toUpperCase().padStart(2, '0')}` });
      }
      const field = UI.buildApplyField({ label: 'Lamp state', kind: 'select', enabled: true, value: ls.value, options, name: 'servicelife_lampState' });
      UI.wireApplyField(field, String(ls.value), async (v) => {
        await saveServiceLifeField(uid, 'lampState', parseInt(v, 10), statusSetter);
      }, statusSetter);
      wrap.appendChild(field.wrap);
    }

    if (!any) return; // every field NACKed/unsupported — nothing worth a panel for

    const h = document.createElement('h3');
    h.className = 'b5-panel__title';
    h.style.marginTop = 'var(--b5-space-5)';
    h.textContent = 'Service life';
    outerContainer.appendChild(h);
    outerContainer.appendChild(wrap);
  }

  // --- E1.20 §10.11 "Device Control" rendering --------------------------------
  // POWER_STATE's four Table A-11 values get a labeled dropdown (spec-defined
  // enum — the project's own rule: only render labels the spec actually
  // defines). A value this device currently reports outside those four still
  // gets its own dropdown option (own label if PowerState.String() resolved
  // one, else hex) so the control never silently discards the device's real
  // current state.
  const POWER_STATE_OPTIONS = [
    { value: 0x00, label: 'Full Off' },
    { value: 0x01, label: 'Shutdown' },
    { value: 0x02, label: 'Standby' },
    { value: 0xFF, label: 'Normal' },
  ];

  // controlActionRow renders either the normal (unarmed) controls for one
  // §10.11 action, or — while that exact `kind` is armed for `uid` — a
  // Confirm/Cancel row naming the action (armed.label), matching
  // renderDestructiveActionsSection's actionsRow shape one level down (per
  // control, not per panel, since several independent controls share this
  // one section). buildControlsHtml()/wireControls(row) build the unarmed
  // state; callers arm via armControl(uid, kind, payload, label) from
  // inside wireControls' own event handlers.
  function controlActionRow(uid, kind, buildControlsHtml, wireControls, statusSetter) {
    const armed = controlArmed && controlArmed.uid === uid && controlArmed.kind === kind ? controlArmed : null;
    const row = document.createElement('div');
    row.className = 'b5-row';
    if (controlBusy && armed) {
      row.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Sending…</span>`;
    } else if (armed) {
      row.innerHTML = `
        <span class="b5-badge b5-badge--warning">${UI.icon('status-warning')}Confirm</span>
        <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-ctrl-confirm">${escapeHtml(armed.label)}</button>
        <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-ctrl-cancel">${UI.icon('revert')}Cancel</button>
      `;
      row.querySelector('.btn-ctrl-confirm').addEventListener('click', () => confirmControl(uid, statusSetter));
      row.querySelector('.btn-ctrl-cancel').addEventListener('click', disarmControl);
    } else {
      row.innerHTML = buildControlsHtml();
      wireControls(row);
    }
    return row;
  }

  // renderPowerStateField: POWER_STATE (§10.11.3), omitted entirely when
  // this device doesn't answer GET POWER_STATE at all.
  function renderPowerStateField(uid, st, deviceLabel, statusSetter) {
    if (!st.powerState || !st.powerState.known) return null;
    const wrap = document.createElement('div');
    wrap.className = 'b5-field';
    wrap.innerHTML = '<label class="b5-field__label">Power state</label>';
    const current = st.powerState.value;
    const options = POWER_STATE_OPTIONS.slice();
    if (!options.some(o => o.value === current)) {
      options.push({ value: current, label: st.powerState.label || `0x${current.toString(16).toUpperCase().padStart(2, '0')}` });
    }
    const actions = controlActionRow(uid, 'power', () => `
      <select class="b5-select ctrl-power-select">${options.map(o => `<option value="${o.value}" ${o.value === current ? 'selected' : ''}>${escapeHtml(o.label)}</option>`).join('')}</select>
      <button type="button" class="b5-btn b5-btn--sm b5-btn--danger ctrl-power-apply">Set power state</button>
    `, (row) => {
      row.querySelector('.ctrl-power-apply').addEventListener('click', () => {
        const v = parseInt(row.querySelector('.ctrl-power-select').value, 10);
        if (v === current) return;
        const opt = options.find(o => o.value === v);
        armControl(uid, 'power', { value: v }, `Yes, set ${deviceLabel} power state to ${opt ? opt.label : v}`);
      });
    }, statusSetter);
    wrap.appendChild(actions);
    const hint = document.createElement('span');
    hint.className = 'b5-field__hint';
    hint.textContent = 'Full Off / Shutdown can leave the fixture unresponsive until it is reset or power-cycled.';
    wrap.appendChild(hint);
    return wrap;
  }

  // Self Test Status -> badge status (design-spec "never color as the sole
  // signal" — the badge always carries its own text label too, from
  // entry.statusLabel/rdm.SelfTestStatus.String() server-side).
  function selfTestBadgeStatus(statusCode) {
    if (statusCode === 4) return 'ok';       // STS_PASS
    if (statusCode === 5) return 'error';    // STS_FAIL
    if (statusCode === 3) return 'warning';  // STS_ACTIVE
    return 'unknown';
  }

  // renderSelfTestSection: PERFORM_SELFTEST (§10.11.4) + its SELF_TEST_
  // DESCRIPTION/SELFTEST_ENHANCED companions (§10.11.5/10.11.8), omitted
  // entirely when this device doesn't answer GET PERFORM_SELFTEST at all.
  // When SELFTEST_ENHANCED resolved a roster (st.selfTestsKnown), each test
  // gets its own labeled Run button (label from SELF_TEST_DESCRIPTION when
  // that resolved, else a bare "Self test N" — never an invented label).
  // Otherwise (SELFTEST_ENHANCED not advertised — E1.20 §10.11.8's own text:
  // there is no other way to learn which test numbers exist without
  // invoking each one) falls back to a bounded numeric stepper, this
  // project's own rule for an enumerated PID with no spec-defined labels.
  function renderSelfTestSection(uid, st, deviceLabel, statusSetter) {
    if (!st.selfTestActive || !st.selfTestActive.known) return null;
    const wrap = document.createElement('div');
    wrap.className = 'b5-stack';
    const label = document.createElement('span');
    label.className = 'b5-field__label';
    label.textContent = 'Self test';
    wrap.appendChild(label);

    const activeNow = !!st.selfTestActive.value;
    const statusLine = document.createElement('div');
    statusLine.innerHTML = UI.badge(activeNow ? 'warning' : 'ok', activeNow ? 'A self test is currently running' : 'No self test running');
    wrap.appendChild(statusLine);

    if (st.selfTestsKnown && st.selfTests && st.selfTests.length) {
      st.selfTests.forEach((entry) => {
        const row = document.createElement('div');
        row.className = 'b5-row';
        row.style.marginTop = 'var(--b5-space-2)';
        row.style.alignItems = 'center';
        const name = entry.description ? `${entry.number} — ${entry.description}` : `Self test ${entry.number}`;
        const nameEl = document.createElement('span');
        nameEl.style.minWidth = '14rem';
        nameEl.style.display = 'inline-block';
        nameEl.textContent = name;
        row.appendChild(nameEl);
        const badge = document.createElement('span');
        badge.innerHTML = UI.badge(selfTestBadgeStatus(entry.statusCode), entry.statusLabel);
        row.appendChild(badge);
        const kind = 'selftest:' + entry.number;
        const actions = controlActionRow(uid, kind, () => `<button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-run-selftest">Run</button>`, (r) => {
          r.querySelector('.btn-run-selftest').addEventListener('click', () => {
            armControl(uid, kind, { test: entry.number }, `Yes, run "${name}" on ${deviceLabel}`);
          });
        }, statusSetter);
        row.appendChild(actions);
        wrap.appendChild(row);
      });
    } else {
      const row = document.createElement('div');
      row.className = 'b5-row';
      row.style.marginTop = 'var(--b5-space-2)';
      const numInput = document.createElement('input');
      numInput.type = 'number'; numInput.min = '1'; numInput.max = '254'; numInput.value = '1';
      numInput.className = 'b5-input b5-input--mono';
      numInput.style.width = '6rem';
      row.appendChild(numInput);
      const actions = controlActionRow(uid, 'selftest:manual', () => `<button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-run-selftest-manual">Run</button>`, (r) => {
        r.querySelector('.btn-run-selftest-manual').addEventListener('click', () => {
          const n = parseInt(numInput.value, 10);
          if (!Number.isFinite(n) || n < 1 || n > 254) return;
          armControl(uid, 'selftest:manual', { test: n }, `Yes, run self test ${n} on ${deviceLabel}`);
        });
      }, statusSetter);
      row.appendChild(actions);
      wrap.appendChild(row);
      const hint = document.createElement('span');
      hint.className = 'b5-field__hint';
      hint.textContent = 'This fixture doesn’t advertise SELFTEST_ENHANCED, so Benny512 has no way to learn which self test numbers it implements without invoking one — check the fixture’s own manual for the number to use.';
      wrap.appendChild(hint);
    }

    // Turning tests off is the safe direction (E1.20 §10.11.4: SELF_TEST_OFF
    // stops whatever is running) — ungated, immediate, same "remedial
    // actions don't need arm-then-confirm ceremony" reasoning the Reset
    // panel's own Cancel button already uses.
    const offRow = document.createElement('div');
    offRow.className = 'b5-row';
    offRow.style.marginTop = 'var(--b5-space-2)';
    const offBtn = document.createElement('button');
    offBtn.type = 'button';
    offBtn.className = 'b5-btn b5-btn--sm b5-btn--ghost';
    offBtn.textContent = 'Turn off self test';
    offBtn.disabled = !activeNow;
    offBtn.addEventListener('click', async () => {
      offBtn.disabled = true;
      try {
        await Api.setSelfTest(uid, 0);
        statusSetter('self test off');
      } catch (e) {
        statusSetter('error: ' + e.message);
      }
      if (uid === selectedUID) ensureDeviceControl(uid, selectGen);
    });
    offRow.appendChild(offBtn);
    wrap.appendChild(offRow);

    return wrap;
  }

  // renderPresetPlaybackField: PRESET_PLAYBACK (§10.11.7), omitted entirely
  // when this device doesn't answer GET PRESET_PLAYBACK at all. Mode's two
  // sentinel values (Off/All, Table A-7) are labeled options; anything else
  // is "Scene #" with a numeric field, matching the spec's own "individual
  // Scene number" wording rather than a synthetic per-scene label list this
  // app has no way to know.
  function renderPresetPlaybackField(uid, st, deviceLabel, statusSetter) {
    if (!st.presetPlayback || !st.presetPlayback.known) return null;
    const wrap = document.createElement('div');
    wrap.className = 'b5-field';
    wrap.innerHTML = '<label class="b5-field__label">Preset playback</label>';
    const current = st.presetPlayback.mode;
    const level = st.presetPlayback.level;
    const isScene = current !== 0 && current !== 0xFFFF;
    const actions = controlActionRow(uid, 'preset', () => `
      <select class="b5-select ctrl-preset-mode">
        <option value="0" ${current === 0 ? 'selected' : ''}>Off (normal DMX)</option>
        <option value="65535" ${current === 0xFFFF ? 'selected' : ''}>All (looped sequence)</option>
        <option value="scene" ${isScene ? 'selected' : ''}>Scene #…</option>
      </select>
      <input type="number" min="1" max="65534" class="b5-input b5-input--mono ctrl-preset-scene" style="width:6rem" value="${isScene ? current : 1}" ${isScene ? '' : 'hidden'}>
      <input type="number" min="0" max="255" class="b5-input b5-input--mono ctrl-preset-level" style="width:5rem" value="${level}" title="Master level 0-255 (255 = full)">
      <button type="button" class="b5-btn b5-btn--sm b5-btn--danger ctrl-preset-apply">Set</button>
    `, (row) => {
      const modeSel = row.querySelector('.ctrl-preset-mode');
      const sceneInput = row.querySelector('.ctrl-preset-scene');
      modeSel.addEventListener('change', () => { sceneInput.hidden = modeSel.value !== 'scene'; });
      row.querySelector('.ctrl-preset-apply').addEventListener('click', () => {
        let mode;
        if (modeSel.value === 'scene') mode = parseInt(sceneInput.value, 10);
        else mode = parseInt(modeSel.value, 10);
        const lvl = parseInt(row.querySelector('.ctrl-preset-level').value, 10);
        if (!Number.isFinite(mode) || mode < 0 || mode > 0xFFFF) return;
        if (!Number.isFinite(lvl) || lvl < 0 || lvl > 255) return;
        const modeLabel = mode === 0 ? 'Off' : mode === 0xFFFF ? 'All' : `Scene ${mode}`;
        armControl(uid, 'preset', { mode, level: lvl }, `Yes, set ${deviceLabel} preset playback to ${modeLabel}`);
      });
    }, statusSetter);
    wrap.appendChild(actions);
    const hint = document.createElement('span');
    hint.className = 'b5-field__hint';
    hint.textContent = 'Recalls a pre-recorded scene — this changes the fixture’s live output as soon as it’s accepted.';
    wrap.appendChild(hint);
    return wrap;
  }

  // renderCapturePresetField: CAPTURE_PRESET (§10.11.6), omitted entirely
  // when this device's SUPPORTED_PARAMETERS doesn't list it (SET_COMMAND
  // only — no GET form exists, so "known" here comes from
  // capturePresetSupported, not a field value the way every other §10.11
  // control above works).
  function renderCapturePresetField(uid, st, deviceLabel, statusSetter) {
    const sup = st.capturePresetSupported;
    if (!sup || !sup.known || !sup.supported) return null;
    const wrap = document.createElement('div');
    wrap.className = 'b5-field';
    wrap.innerHTML = '<label class="b5-field__label">Capture preset</label>';
    const actions = controlActionRow(uid, 'capture', () => `
      <input type="number" min="0" max="65535" value="1" class="b5-input b5-input--mono ctrl-capture-scene" style="width:6rem" placeholder="Scene #">
      <label class="b5-toggle" style="margin:0 var(--b5-space-2)"><input type="checkbox" class="ctrl-capture-timing"><span class="b5-toggle__track"></span>Include fade/wait</label>
      <input type="number" min="0" max="65535" value="0" class="b5-input b5-input--mono ctrl-capture-up" style="width:6rem" placeholder="Up fade (0.1s)" hidden>
      <input type="number" min="0" max="65535" value="0" class="b5-input b5-input--mono ctrl-capture-down" style="width:6rem" placeholder="Down fade (0.1s)" hidden>
      <input type="number" min="0" max="65535" value="0" class="b5-input b5-input--mono ctrl-capture-wait" style="width:6rem" placeholder="Wait (0.1s)" hidden>
      <button type="button" class="b5-btn b5-btn--sm b5-btn--danger ctrl-capture-apply">Capture</button>
    `, (row) => {
      const timingToggle = row.querySelector('.ctrl-capture-timing');
      const timingInputs = [row.querySelector('.ctrl-capture-up'), row.querySelector('.ctrl-capture-down'), row.querySelector('.ctrl-capture-wait')];
      timingToggle.addEventListener('change', () => timingInputs.forEach((i) => { i.hidden = !timingToggle.checked; }));
      row.querySelector('.ctrl-capture-apply').addEventListener('click', () => {
        const scene = parseInt(row.querySelector('.ctrl-capture-scene').value, 10);
        if (!Number.isFinite(scene) || scene < 0 || scene > 0xFFFF) return;
        let timing = null;
        if (timingToggle.checked) {
          timing = {
            upFadeTime: parseInt(row.querySelector('.ctrl-capture-up').value, 10) || 0,
            downFadeTime: parseInt(row.querySelector('.ctrl-capture-down').value, 10) || 0,
            waitTime: parseInt(row.querySelector('.ctrl-capture-wait').value, 10) || 0,
          };
        }
        armControl(uid, 'capture', { scene, timing }, `Yes, capture scene ${scene} on ${deviceLabel} (overwrites any existing preset at that number)`);
      });
    }, statusSetter);
    wrap.appendChild(actions);
    const hint = document.createElement('span');
    hint.className = 'b5-field__hint';
    hint.textContent = 'Overwrites the fixture’s stored preset at this scene number with its current output — RDM has no way to read a preset back, so the previous contents can’t be recovered from here.';
    wrap.appendChild(hint);
    return wrap;
  }

  // renderDeviceControlSection assembles the four §10.11 controls above into
  // one "Device control" panel, each independently optional exactly like
  // renderServiceLifeSection — omitted entirely when the device answered
  // none of them (task rule: "each control degrades independently... no
  // spurious zeros and no broken pane").
  function renderDeviceControlSection(outerContainer, uid, deviceLabel, statusSetter) {
    const st = deviceControlCache[uid];
    if (!st || (!st.loaded && !st.error)) return;

    const fields = [];
    const pf = renderPowerStateField(uid, st, deviceLabel, statusSetter); if (pf) fields.push(pf);
    const sf = renderSelfTestSection(uid, st, deviceLabel, statusSetter); if (sf) fields.push(sf);
    const pp = renderPresetPlaybackField(uid, st, deviceLabel, statusSetter); if (pp) fields.push(pp);
    const cp = renderCapturePresetField(uid, st, deviceLabel, statusSetter); if (cp) fields.push(cp);
    if (!fields.length) return;

    const h = document.createElement('h3');
    h.className = 'b5-panel__title';
    h.style.marginTop = 'var(--b5-space-5)';
    h.textContent = 'Device control';
    outerContainer.appendChild(h);
    const wrap = document.createElement('div');
    wrap.className = 'b5-stack';
    fields.forEach((f) => wrap.appendChild(f));
    outerContainer.appendChild(wrap);

    if (st.lastNote) {
      const msg = document.createElement('p');
      msg.className = 'b5-text-sm b5-text-muted';
      msg.style.marginTop = 'var(--b5-space-2)';
      msg.textContent = st.lastNote;
      outerContainer.appendChild(msg);
    }
  }

  // renderDestructiveActionsSection appends the Phase D task 3 warm/cold
  // RESET_DEVICE + FACTORY_DEFAULTS panel, arm-then-confirm (see
  // armDestructive/confirmDestructive above) — omitted entirely for a
  // device where neither action resolved as supported (actionsCache's
  // known:false/supported:false), same "don't clutter the pane with
  // buttons that would just NACK" rule the rest of this file follows.
  // deviceLabel is the exact string the confirm button names (task ask:
  // "the confirm must name the specific device").
  function renderDestructiveActionsSection(outerContainer, uid, deviceLabel, statusSetter) {
    const ac = actionsCache[uid];
    if (!ac || (!ac.loaded && !ac.error)) return;
    const resetSupported = !!(ac.resetDevice && ac.resetDevice.known && ac.resetDevice.supported);
    const factorySupported = !!(ac.factoryDefaults && ac.factoryDefaults.known && ac.factoryDefaults.supported);
    if (!resetSupported && !factorySupported) return;

    const panel = document.createElement('div');
    panel.className = 'b5-panel b5-panel--destructive';
    panel.style.marginTop = 'var(--b5-space-5)';
    panel.innerHTML = `<div class="b5-panel__header"><h3 class="b5-panel__title">Reset &amp; factory defaults</h3></div>`;

    const body = document.createElement('div');
    body.className = 'b5-panel__body b5-stack';
    panel.appendChild(body);

    if (resetSupported) {
      // Spec-honest copy (owner brief, verbatim requirement): there is no
      // RDM PID that reveals whether a fixture actually treats warm and
      // cold differently, and RESET_DEVICE always clears Discovery Mute —
      // both facts stated plainly rather than implied by two buttons that
      // might look like a meaningful choice on their own.
      const note = document.createElement('p');
      note.className = 'b5-text-muted b5-text-sm';
      note.textContent = 'This fixture advertises RESET_DEVICE with both Warm and Cold modes offered — RDM has no way to confirm it actually treats them differently; some fixtures respond to both identically. Either mode clears the fixture’s Discovery Mute flag, so it will drop off the bus: run Discover again once it comes back.';
      body.appendChild(note);
    }

    const armed = destructiveArmed && destructiveArmed.uid === uid ? destructiveArmed.kind : null;
    const actionsRow = document.createElement('div');
    actionsRow.className = 'b5-row';
    body.appendChild(actionsRow);

    if (destructiveBusy && armed) {
      actionsRow.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Sending…</span>`;
    } else if (armed) {
      const confirmLabel = armed === 'factory'
        ? `Yes, reset ${escapeHtml(deviceLabel)} to factory defaults`
        : `Yes, ${armed === 'warm' ? 'warm' : 'cold'}-reset ${escapeHtml(deviceLabel)}`;
      actionsRow.innerHTML = `
        <span class="b5-badge b5-badge--warning">${UI.icon('status-warning')}Confirm</span>
        <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-destructive-confirm">${confirmLabel}</button>
        <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-destructive-cancel">${UI.icon('revert')}Cancel</button>
      `;
      actionsRow.querySelector('.btn-destructive-confirm').addEventListener('click', () => confirmDestructive(uid, statusSetter));
      actionsRow.querySelector('.btn-destructive-cancel').addEventListener('click', disarmDestructive);
    } else {
      let html = '';
      if (resetSupported) {
        html += `<button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-reset-warm">Warm reset</button>`;
        html += `<button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-reset-cold">Cold reset</button>`;
      }
      if (factorySupported) {
        html += `<button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-factory-defaults">Factory defaults</button>`;
      }
      actionsRow.innerHTML = html;
      const bw = actionsRow.querySelector('.btn-reset-warm'); if (bw) bw.addEventListener('click', () => armDestructive(uid, 'warm'));
      const bc = actionsRow.querySelector('.btn-reset-cold'); if (bc) bc.addEventListener('click', () => armDestructive(uid, 'cold'));
      const bf = actionsRow.querySelector('.btn-factory-defaults'); if (bf) bf.addEventListener('click', () => armDestructive(uid, 'factory'));
    }

    if (ac.lastNote) {
      const msg = document.createElement('p');
      msg.className = 'b5-text-sm b5-text-muted';
      msg.style.marginTop = 'var(--b5-space-2)';
      msg.textContent = ac.lastNote;
      body.appendChild(msg);
    }

    outerContainer.appendChild(panel);
  }

  // --- E1.37-2 network configuration (IPv4/DHCP/DNS) -------------------------
  // isValidIPv4/isValidNetmask: client-side validation so a malformed
  // address never even reaches Arm, let alone the wire (task brief:
  // "Validate input client-side... a malformed IPv4 address or netmask
  // must be rejected with a clear message rather than sent").
  function isValidIPv4(s) {
    if (typeof s !== 'string') return false;
    const parts = s.trim().split('.');
    if (parts.length !== 4) return false;
    return parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255);
  }
  // isValidNetmask additionally requires the address's bits to be a
  // contiguous run of 1s followed by 0s (a standard IPv4 subnet mask) —
  // catches e.g. "255.0.255.0", which isValidIPv4 alone would accept as a
  // well-formed address but is not a valid netmask.
  function isValidNetmask(s) {
    if (!isValidIPv4(s)) return false;
    const bits = s.trim().split('.').map((o) => Number(o).toString(2).padStart(8, '0')).join('');
    return /^1*0*$/.test(bits);
  }

  // renderNetworkSection appends the E1.37-2 IPv4/DHCP/DNS panel — entirely
  // omitted (task brief: "absent, not broken") unless GET /network resolved
  // LIST_INTERFACES as advertised (networkCache[uid].supported); a device
  // that hasn't answered yet, NACKed, or plainly doesn't implement E1.37-2
  // (the overwhelming majority of fixtures) shows nothing here at all.
  function renderNetworkSection(outerContainer, uid, deviceLabel, statusSetter) {
    const st = networkCache[uid];
    if (!st || (!st.loaded && !st.error) || !st.supported) return;
    const es = networkEditState[uid];
    if (!es) return;

    const panel = document.createElement('div');
    panel.className = 'b5-panel';
    panel.style.marginTop = 'var(--b5-space-5)';
    panel.innerHTML = `
      <div class="b5-panel__header"><h3 class="b5-panel__title">Network configuration (E1.37-2)</h3></div>
      <div class="b5-panel__body b5-stack">
        <div class="b5-alert b5-alert--caution">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Unverified against real hardware</p>
            <p class="b5-alert__body">These E1.37-2 IPv4/DNS packet layouts are Benny512's best reading of the common wire convention, not confirmed against ANSI/ESTA E1.37-2 primary text or a real device. <strong>A mis-set IP address or subnet mask can strand this device off the show network</strong>, reachable afterward only from its own front panel/display if it has one. Double-check every value on the device's own display or web UI before — and after — applying a change here.</p>
          </div>
        </div>
        <div class="network-interfaces b5-stack"></div>
        <div class="network-dns"></div>
      </div>
    `;
    outerContainer.appendChild(panel);

    const ifWrap = panel.querySelector('.network-interfaces');
    (st.interfaces || []).forEach((ifc) => ifWrap.appendChild(renderNetworkInterface(uid, ifc, deviceLabel, statusSetter)));

    const dnsWrap = panel.querySelector('.network-dns');
    const dnsEl = renderNetworkDNS(uid, st, statusSetter);
    if (dnsEl) dnsWrap.appendChild(dnsEl);

    if (st.lastNote) {
      const msg = document.createElement('p');
      msg.className = 'b5-text-sm b5-text-muted';
      msg.textContent = st.lastNote;
      panel.querySelector('.b5-panel__body').appendChild(msg);
    }
  }

  // renderNetworkInterface builds one interface's sub-panel. Every field
  // degrades independently (task brief: "no spurious zeros") — a Known:
  // false field is simply omitted, never shown as a fake 0/blank that could
  // be mistaken for a confirmed value.
  function renderNetworkInterface(uid, ifc, deviceLabel, statusSetter) {
    const es = networkEditState[uid].interfaces[ifc.id] || (networkEditState[uid].interfaces[ifc.id] = { ip: '', mask: '', applied: false });
    const box = document.createElement('div');
    box.className = 'b5-panel';
    box.style.background = 'var(--b5-surface-2, transparent)';

    const title = ifc.labelKnown ? `Interface ${ifc.id} — ${escapeHtml(ifc.label)}` : `Interface ${ifc.id}`;
    const rows = [];
    if (ifc.hardwareAddressKnown) {
      rows.push(infoRow('Hardware address', `<span class="b5-input--mono">${escapeHtml(ifc.hardwareAddressHex)}</span> <span class="b5-text-muted b5-text-sm">(raw hex — byte layout not confirmed as a MAC)</span>`));
    }
    if (ifc.currentKnown) {
      rows.push(infoRow('Current address', `${escapeHtml(ifc.currentIp)} / ${escapeHtml(ifc.currentMask)}`));
    }
    if (ifc.dhcpKnown) {
      rows.push(infoRow('DHCP', escapeHtml(ifc.dhcpStatus)));
    }

    box.innerHTML = `
      <div class="b5-panel__header"><h4 class="b5-panel__title" style="font-size:var(--b5-text-md)">${title}</h4></div>
      <div class="b5-panel__body b5-stack">
        <div class="b5-grid-2">${rows.join('')}</div>
        <div class="static-ip-editor"></div>
        <div class="dhcp-toggle"></div>
      </div>
    `;
    const body = box.querySelector('.b5-panel__body');
    if (!rows.length) {
      const none = document.createElement('p');
      none.className = 'b5-text-muted b5-text-sm';
      none.textContent = 'This device advertises the interface but did not answer any of the address/DHCP fields Benny512 asked for.';
      body.insertBefore(none, body.firstChild);
    }

    // --- static address editor (only when IPV4_STATIC_ADDRESS is advertised) ---
    if (ifc.staticKnown || es.ip !== '' || es.mask !== '') {
      const editorWrap = box.querySelector('.static-ip-editor');
      const ipInvalid = es.ip !== '' && !isValidIPv4(es.ip);
      const maskInvalid = es.mask !== '' && !isValidNetmask(es.mask);
      editorWrap.innerHTML = `
        <div class="b5-grid-2">
          <div class="b5-field">
            <label class="b5-field__label" for="net-ip-${ifc.id}">Static IP address</label>
            <input id="net-ip-${ifc.id}" class="b5-input b5-input--mono" type="text" placeholder="e.g. 2.11.90.5" value="${escapeHtml(es.ip)}">
            <span id="net-ip-error-${ifc.id}">${ipInvalid ? `<span class="b5-field__error">${UI.icon('status-error')}Not a valid IPv4 address</span>` : ''}</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="net-mask-${ifc.id}">Subnet mask</label>
            <input id="net-mask-${ifc.id}" class="b5-input b5-input--mono" type="text" placeholder="e.g. 255.255.0.0" value="${escapeHtml(es.mask)}">
            <span id="net-mask-error-${ifc.id}">${maskInvalid ? `<span class="b5-field__error">${UI.icon('status-error')}Not a valid subnet mask</span>` : ''}</span>
          </div>
        </div>
        <div class="static-ip-confirm"></div>
      `;
      // input handlers update the per-field error span in place (never
      // recreate the <input> itself — that would drop focus/caret mid-
      // keystroke, the exact bug class trackLiveFieldState/captureFieldFocus
      // exist to prevent elsewhere in this file) alongside the Apply/Arm/
      // Confirm ladder below, so an invalid address is flagged live as the
      // tech types, not only once Apply is attempted.
      editorWrap.querySelector(`#net-ip-${ifc.id}`).addEventListener('input', (e) => {
        es.ip = e.target.value; es.applied = false;
        if (uid === selectedUID) disarmNetwork();
        const invalid = es.ip !== '' && !isValidIPv4(es.ip);
        editorWrap.querySelector(`#net-ip-error-${ifc.id}`).innerHTML = invalid ? `<span class="b5-field__error">${UI.icon('status-error')}Not a valid IPv4 address</span>` : '';
        renderNetworkInterfaceStaticConfirm(uid, ifc, deviceLabel, statusSetter, editorWrap);
      });
      editorWrap.querySelector(`#net-mask-${ifc.id}`).addEventListener('input', (e) => {
        es.mask = e.target.value; es.applied = false;
        if (uid === selectedUID) disarmNetwork();
        const invalid = es.mask !== '' && !isValidNetmask(es.mask);
        editorWrap.querySelector(`#net-mask-error-${ifc.id}`).innerHTML = invalid ? `<span class="b5-field__error">${UI.icon('status-error')}Not a valid subnet mask</span>` : '';
        renderNetworkInterfaceStaticConfirm(uid, ifc, deviceLabel, statusSetter, editorWrap);
      });
      renderNetworkInterfaceStaticConfirm(uid, ifc, deviceLabel, statusSetter, editorWrap);
    }

    // --- DHCP toggle (only when IPV4_DHCP_MODE is advertised) ---
    if (ifc.dhcpKnown) {
      const toggleWrap = box.querySelector('.dhcp-toggle');
      renderNetworkDHCPToggle(uid, ifc, deviceLabel, statusSetter, toggleWrap);
    }

    return box;
  }

  // renderNetworkInterfaceStaticConfirm: the Apply -> Arm -> Confirm ladder
  // for one interface's static IP/mask fields — matches nodes.js's own IP
  // editor ceremony exactly (task brief: "matching... nodes.js's IP-config
  // editor"), since a mis-set static address is the single field in this
  // whole app most likely to strand a device off the network.
  function renderNetworkInterfaceStaticConfirm(uid, ifc, deviceLabel, statusSetter, editorWrap) {
    const es = networkEditState[uid].interfaces[ifc.id];
    const area = editorWrap.querySelector('.static-ip-confirm');
    if (!area) return;
    const ipInvalid = es.ip !== '' && !isValidIPv4(es.ip);
    const maskInvalid = es.mask !== '' && !isValidNetmask(es.mask);
    const dirty = es.ip !== (ifc.staticIp || '') || es.mask !== (ifc.staticMask || '');
    const armedHere = networkArmed && networkArmed.uid === uid && networkArmed.kind === 'static' && networkArmed.id === ifc.id;

    if (!dirty) { area.innerHTML = ''; return; }
    if (ipInvalid || maskInvalid || !es.ip || !es.mask) {
      area.innerHTML = `<p class="b5-field__hint">Enter a complete, valid IPv4 address and subnet mask to apply a change.</p>`;
      return;
    }
    if (networkBusy && armedHere) {
      area.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Sending…</span>`;
      return;
    }
    if (!es.applied) {
      area.innerHTML = `<div class="b5-row"><button type="button" class="b5-btn b5-btn--sm b5-btn--primary btn-net-apply">${UI.icon('apply')}Apply</button><span class="b5-field__hint">stages this address; sending still requires arming + confirming.</span></div>`;
      area.querySelector('.btn-net-apply').addEventListener('click', () => { es.applied = true; renderNetworkInterfaceStaticConfirm(uid, ifc, deviceLabel, statusSetter, editorWrap); });
    } else if (!armedHere) {
      area.innerHTML = `
        <div class="b5-row">
          ${UI.badge('ok', 'Applied')}
          <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-net-arm">Arm send…</button>
          <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-net-revert">${UI.icon('revert')}Revert</button>
        </div>`;
      area.querySelector('.btn-net-arm').addEventListener('click', () => armNetwork(uid, 'static', ifc.id, { ip: es.ip, mask: es.mask }, deviceLabel));
      area.querySelector('.btn-net-revert').addEventListener('click', () => {
        es.ip = ifc.staticIp || ''; es.mask = ifc.staticMask || ''; es.applied = false;
        notify('params');
      });
    } else {
      area.innerHTML = `
        <div class="b5-alert b5-alert--warning" style="margin-top:var(--b5-space-2)">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Confirm static IP change</p>
            <p class="b5-alert__body">A mis-set address can strand <strong>${escapeHtml(deviceLabel)}</strong> off the show network. Confirm: set interface ${ifc.id} to <strong>${escapeHtml(es.ip)} / ${escapeHtml(es.mask)}</strong>.</p>
            <div class="b5-row" style="margin-top:var(--b5-space-2)">
              <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-net-confirm">Yes, send now</button>
              <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-net-cancel">Cancel</button>
            </div>
          </div>
        </div>`;
      area.querySelector('.btn-net-confirm').addEventListener('click', () => confirmNetwork(uid, statusSetter));
      area.querySelector('.btn-net-cancel').addEventListener('click', disarmNetwork);
    }
  }

  // renderNetworkDHCPToggle: a plain toggle + arm-then-confirm (two-stage,
  // matching the Reset & factory defaults panel's ceremony) rather than the
  // three-stage Apply->Arm->Confirm above — a checkbox has no free-text
  // "malformed input" failure mode to stage/validate, so Apply would add a
  // step without adding safety.
  function renderNetworkDHCPToggle(uid, ifc, deviceLabel, statusSetter, wrap) {
    const armedHere = networkArmed && networkArmed.uid === uid && networkArmed.kind === 'dhcp' && networkArmed.id === ifc.id;
    const currentlyOn = ifc.dhcpStatus === 'active';
    if (networkBusy && armedHere) {
      wrap.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Sending…</span>`;
      return;
    }
    if (armedHere) {
      const toState = networkArmed.payload.enable ? 'ON' : 'OFF';
      wrap.innerHTML = `
        <div class="b5-alert b5-alert--warning">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Confirm DHCP change</p>
            <p class="b5-alert__body">Switching DHCP ${toState} on <strong>${escapeHtml(deviceLabel)}</strong> can change its address and, if no DHCP server answers, strand it off the network. Confirm?</p>
            <div class="b5-row" style="margin-top:var(--b5-space-2)">
              <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-dhcp-confirm">Yes, send now</button>
              <button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-dhcp-cancel">Cancel</button>
            </div>
          </div>
        </div>`;
      wrap.querySelector('.btn-dhcp-confirm').addEventListener('click', () => confirmNetwork(uid, statusSetter));
      wrap.querySelector('.btn-dhcp-cancel').addEventListener('click', disarmNetwork);
      return;
    }
    wrap.innerHTML = `<label class="b5-toggle"><input type="checkbox" ${currentlyOn ? 'checked' : ''}><span class="b5-toggle__track"></span>DHCP</label>`;
    wrap.querySelector('input').addEventListener('change', (e) => {
      const enable = e.target.checked;
      // Reflect the pre-toggle state until confirmed — armNetwork triggers
      // a re-render (notify('params')) that redraws this control from
      // networkArmed, not from the checkbox's own transient DOM state.
      e.target.checked = currentlyOn;
      armNetwork(uid, 'dhcp', ifc.id, { enable }, deviceLabel);
    });
  }

  // renderNetworkDNS: device-global hostname/domain (E1.37-2 §6.2's DNS_
  // HOSTNAME/DNS_DOMAIN_NAME are not per-interface) — plain Apply fields,
  // not arm-then-confirm: unlike a static IP or DHCP toggle, a bad hostname
  // cannot itself take the device off the network. DNS_NAME_SERVER is
  // read-only here (no confirmed SET layout — see internal/web/network.go's
  // doc comment) so shown as a plain list, never editable.
  function renderNetworkDNS(uid, st, statusSetter) {
    if (!st.dns || !st.dns.supported) return null;
    const es = networkEditState[uid];
    const wrap = document.createElement('div');
    wrap.className = 'b5-stack';
    wrap.style.marginTop = 'var(--b5-space-3)';
    const h = document.createElement('h4');
    h.className = 'b5-panel__title';
    h.style.fontSize = 'var(--b5-text-md)';
    h.textContent = 'DNS';
    wrap.appendChild(h);

    const hostField = UI.buildApplyField({ label: 'Hostname', value: es.dnsHostname, enabled: true, maxLength: 63, name: 'network_dns_hostname' });
    wrap.appendChild(hostField.wrap);
    UI.wireApplyField(hostField, es.dnsHostname, async (v) => {
      es.dnsHostname = v;
      await Api.setNetworkDNS(uid, v, es.dnsDomain);
      statusSetter('DNS hostname applied');
    }, statusSetter);

    const domainField = UI.buildApplyField({ label: 'Domain', value: es.dnsDomain, enabled: true, maxLength: 231, name: 'network_dns_domain' });
    wrap.appendChild(domainField.wrap);
    UI.wireApplyField(domainField, es.dnsDomain, async (v) => {
      es.dnsDomain = v;
      await Api.setNetworkDNS(uid, es.dnsHostname, v);
      statusSetter('DNS domain applied');
    }, statusSetter);

    if (st.dns.nameServers && st.dns.nameServers.length) {
      const list = document.createElement('div');
      list.innerHTML = `<span class="b5-text-muted b5-text-sm">Name servers (read-only — no confirmed layout to write this back)</span><br>` +
        st.dns.nameServers.map((n) => `<span class="b5-input--mono">[${n.index}] ${escapeHtml(n.ip)}</span>`).join(' &nbsp; ');
      wrap.appendChild(list);
    }
    return wrap;
  }

  // renderDimmerFields appends the E1.37-1 dimmer-PID rows (task ask: "the
  // dimmer curve ask") — each is independently optional (omitted when the
  // device NACKed/doesn't implement it, per the same Promise.allSettled
  // pattern the base fields already use). CURVE/OUTPUT_RESPONSE_TIME/
  // MODULATION_FREQUENCY render as labeled dropdowns when their companion
  // *_DESCRIPTION PID resolved labels (task ask: "labeled dropdown instead
  // of a bare number"), else a bare numeric dropdown of 1..Count.
  function renderDimmerFields(outerContainer, uid, st, statusSetter) {
    const wrap = document.createElement('div');
    wrap.className = 'b5-stack';
    let any = false;

    function indexedRow(name, title, choice, labels, pidGet) {
      if (!choice) return;
      any = true;
      const opts = Array.from({ length: choice.Count }, (_, i) => i + 1)
        .map(i => ({ value: i, label: (labels && labels[i]) ? `${i} — ${labels[i]}` : String(i) }));
      const hint = (!labels || Object.keys(labels).length < choice.Count) ? 'fetching labels…' : undefined;
      const field = UI.buildApplyField({ label: title, kind: 'select', enabled: true, value: choice.Current, options: opts, hint, name });
      UI.wireApplyField(field, String(choice.Current), pidGet, statusSetter);
      wrap.appendChild(field.wrap);
    }

    indexedRow('curve', 'Dimmer curve', st.curve, st.curveLabels, async (v) => saveParam(uid, 'curve', parseInt(v, 10), statusSetter));
    indexedRow('output_response_time', 'Output response time', st.ort, st.ortLabels, async (v) => saveParam(uid, 'output_response_time', parseInt(v, 10), statusSetter));
    indexedRow('modulation_frequency', 'Modulation frequency', st.modFreq, st.modFreqLabels, async (v) => saveParam(uid, 'modulation_frequency', parseInt(v, 10), statusSetter));

    if (st.minLevel) {
      any = true;
      const field = document.createElement('div');
      field.className = 'b5-field';
      field.innerHTML = `
        <label class="b5-field__label">Minimum level (rise / fall)</label>
        <div class="b5-field__row">
          <input class="b5-input" type="number" min="0" max="65535" value="${st.minLevel.Increasing}" style="max-width:8em" data-b5-field="minimum_level_rise">
          <span class="b5-text-muted">/</span>
          <input class="b5-input" type="number" min="0" max="65535" value="${st.minLevel.Decreasing}" style="max-width:8em" data-b5-field="minimum_level_fall">
          <span class="b5-field__actions"><button class="b5-btn b5-btn--sm b5-btn--primary" disabled>${UI.icon('apply')}Apply</button></span>
        </div>
      `;
      const [riseInput, fallInput] = field.querySelectorAll('input');
      const applyBtn = field.querySelector('button');
      const refresh = () => { applyBtn.disabled = (Number(riseInput.value) === st.minLevel.Increasing && Number(fallInput.value) === st.minLevel.Decreasing); };
      riseInput.addEventListener('input', refresh);
      fallInput.addEventListener('input', refresh);
      applyBtn.addEventListener('click', async () => {
        try {
          await Api.setParam(uid, 'minimum_level', { Increasing: Number(riseInput.value), Decreasing: Number(fallInput.value), OnBelowMin: st.minLevel.OnBelowMin });
          statusSetter('applied minimum level');
          st.minLevel = { Increasing: Number(riseInput.value), Decreasing: Number(fallInput.value), OnBelowMin: st.minLevel.OnBelowMin };
          notify('params');
        } catch (e) { statusSetter('error: ' + e.message); }
      });
      wrap.appendChild(field);
    }

    if (st.maxLevel !== null && st.maxLevel !== undefined) {
      any = true;
      const field = UI.buildApplyField({ label: 'Maximum level', kind: 'number', enabled: true, value: st.maxLevel, min: 0, max: 65535, name: 'maximum_level' });
      UI.wireApplyField(field, String(st.maxLevel), async (v) => saveParam(uid, 'maximum_level', parseInt(v, 10), statusSetter), statusSetter);
      wrap.appendChild(field.wrap);
    }

    if (st.identMode !== null && st.identMode !== undefined) {
      any = true;
      const field = UI.buildApplyField({ label: 'Identify mode', kind: 'select', enabled: true, value: st.identMode, options: [{ value: 0, label: 'Quiet' }, { value: 1, label: 'Loud' }], name: 'identify_mode' });
      UI.wireApplyField(field, String(st.identMode), async (v) => saveParam(uid, 'identify_mode', v === '1', statusSetter), statusSetter);
      wrap.appendChild(field.wrap);
    }

    if (any) {
      const h = document.createElement('h3');
      h.className = 'b5-panel__title';
      h.style.marginTop = 'var(--b5-space-5)';
      h.textContent = 'Dimmer (E1.37-1)';
      outerContainer.appendChild(h);
      outerContainer.appendChild(wrap);
    }
  }

  async function saveParam(uid, pid, value, statusSetter) {
    await Api.setParam(uid, pid, value);
    statusSetter('applied ' + pid);
    // Re-fetch this device's params section so the field re-baselines
    // against the server's confirmed value (mirrors the generic
    // manufacturer-PID row's post-apply re-render).
    const st = paramsCache[uid];
    if (st) st.loading = false; // allow ensureParams to run again
    if (uid === selectedUID) ensureParams(uid, selectGen);
  }

  // --- generic manufacturer / unrecognized-ESTA-PID row -------------------
  // Unchanged from the pre-refactor Devices screen (task ask: "Generic ESTA
  // PID rendering... surface it using the PID-name table for the label plus
  // a raw-hex get/set fallback"): DescribeParam/Introspect on the Go side
  // already treat any SUPPORTED_PARAMETERS entry with no typed decoder —
  // manufacturer-range OR unrecognized standard — identically, and
  // toParamDescriptorJSON now fills the Label from the PID-name table when
  // the device itself didn't describe the PID, so this rendering code needs
  // no ESTA-vs-manufacturer branch at all.

  function renderParamRow(uid, desc, valState, statusSetter) {
    const row = document.createElement('div');
    row.className = 'b5-card';
    const label = desc.label || `PID 0x${desc.pid}`;
    // A self-describing PID's editability follows its PARAMETER_
    // DESCRIPTION-reported command_class. A PID that isn't self-describing
    // (no PARAMETER_DESCRIPTION, or the device NACKed it — the generic-
    // ESTA-PID / unrecognized-manufacturer-PID fallback path, task ask:
    // "a raw-hex get/set fallback") always gets an editable raw-hex field:
    // the server's SetParam bypasses the command-class gate entirely for
    // non-self-describing PIDs (blind SET, exactly as the research report
    // recommends — "let the user type new bytes, SET blind"), so the UI
    // must offer that control rather than silently rendering read-only.
    const editable = desc.selfDescribing ? desc.supportsSet : true;
    const unitHint = desc.unitSuffix ? ` ${desc.unitSuffix}` : '';
    const rangeHint = (desc.min !== 0 || desc.max !== 0) ? `[${desc.min}, ${desc.max}]${unitHint}` : '';

    const meta = document.createElement('div');
    meta.className = 'b5-row';
    meta.style.marginBottom = 'var(--b5-space-2)';
    meta.innerHTML = `
      <span class="b5-text-mono b5-text-xs b5-text-muted">0x${desc.pid}</span>
      <strong class="b5-text-sm">${escapeHtml(label)}</strong>
      <span class="b5-text-muted b5-text-xs">${escapeHtml(desc.dataTypeName)}${rangeHint ? ' · ' + escapeHtml(rangeHint) : ''}</span>
      ${!editable ? UI.tag('read-only') : ''}
      ${!desc.selfDescribing ? UI.tag('raw / unverified', 'warn') : ''}
    `;
    row.appendChild(meta);

    const field = document.createElement('div');
    row.appendChild(field);

    if (!valState) {
      field.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}loading…</span>`;
      return row;
    }
    if (!valState.ok) {
      field.innerHTML = `<span class="b5-field__error">${UI.icon('status-error')}error: ${escapeHtml(valState.err)}</span>`;
      return row;
    }
    const v = valState.val;
    // A stable per-PID field name (captureFieldFocus/restoreFieldFocus) so
    // an in-progress edit on one of these generic manufacturer/unrecognized
    // rows survives a re-render of the whole Parameters section the same
    // way the fixed-position fields above it do.
    const fieldName = 'pid_' + desc.pid;

    if (desc.dataType === DS.BOOLEAN || (desc.dataType === DS.BIT_FIELD && desc.pdlSize === 1)) {
      const checked = desc.dataType === DS.BOOLEAN ? v.int !== 0 : (v.hex && parseInt(v.hex.slice(0, 2), 16) !== 0);
      field.innerHTML = `<label class="b5-toggle"><input type="checkbox" ${checked ? 'checked' : ''} ${editable ? '' : 'disabled'}><span class="b5-toggle__track"></span></label>`;
      const cb = field.querySelector('input');
      cb.dataset.b5Field = fieldName;
      if (editable) {
        wireRowApply(field, cb, true, checked, (checkedNow) => {
          const value = desc.dataType === DS.BOOLEAN ? (checkedNow ? 1 : 0) : (checkedNow ? '01' : '00');
          return saveDeviceParam(uid, desc, value, row, statusSetter);
        }, statusSetter);
      }
    } else if (v.kind === 'string') {
      field.innerHTML = `<input class="b5-input" type="text" maxlength="32" value="${escapeHtml(v.str || '')}" ${editable ? '' : 'disabled'}>`;
      const input = field.querySelector('input');
      input.dataset.b5Field = fieldName;
      if (editable) {
        wireRowApply(field, input, false, v.str || '', (val) => saveDeviceParam(uid, desc, val, row, statusSetter), statusSetter);
      }
    } else if (v.kind === 'int') {
      const { wrap, input } = numericStepper(desc, v.int, editable);
      field.appendChild(wrap);
      input.dataset.b5Field = fieldName;
      if (editable) {
        wireRowApply(field, input, false, v.int, (val) => {
          const n = Number(val);
          if (!Number.isFinite(n) || (desc.min !== 0 || desc.max !== 0) && (n < desc.min || n > desc.max)) {
            throw new Error(`must be ${desc.min}–${desc.max}`);
          }
          return saveDeviceParam(uid, desc, n, row, statusSetter);
        }, statusSetter);
      }
    } else if (desc.dataType === DS.ENUMERATION || looksBoundedRaw(desc)) {
      const width = desc.pdlSize || byteWidthFor(desc.max) || 1;
      const current = v.hex ? parseInt(v.hex, 16) || 0 : 0;
      const { wrap, input } = numericStepper(desc, current, editable);
      field.appendChild(wrap);
      input.dataset.b5Field = fieldName;
      if (editable) {
        wireRowApply(field, input, false, current, (val) => saveDeviceParam(uid, desc, hexEncode(Number(val), width), row, statusSetter), statusSetter);
      }
    } else {
      field.innerHTML = `<input class="b5-input b5-input--mono" type="text" placeholder="hex bytes, e.g. DEAD" value="${escapeHtml(v.hex || '')}" ${editable ? '' : 'disabled'}>`;
      const input = field.querySelector('input');
      input.dataset.b5Field = fieldName;
      if (editable) {
        wireRowApply(field, input, false, v.hex || '', (val) => saveDeviceParam(uid, desc, val.replace(/\s+/g, ''), row, statusSetter), statusSetter);
      }
    }
    return row;
  }

  function wireRowApply(field, inputEl, isCheckbox, baselineValue, onApply, statusSetter) {
    const actions = document.createElement('span');
    actions.className = 'b5-field__actions';
    actions.style.marginLeft = 'var(--b5-space-2)';
    const applyBtn = document.createElement('button');
    applyBtn.type = 'button'; applyBtn.className = 'b5-btn b5-btn--sm b5-btn--primary'; applyBtn.disabled = true;
    applyBtn.innerHTML = UI.icon('apply') + 'Apply';
    const revertBtn = document.createElement('button');
    revertBtn.type = 'button'; revertBtn.className = 'b5-btn b5-btn--sm b5-btn--ghost';
    revertBtn.innerHTML = UI.icon('revert') + 'Revert';
    revertBtn.style.display = 'none';
    actions.appendChild(applyBtn);
    actions.appendChild(revertBtn);
    field.appendChild(actions);

    const status = document.createElement('span');
    status.className = 'b5-field__status';
    status.style.display = 'block';
    field.appendChild(status);

    const baseline = isCheckbox ? !!baselineValue : String(baselineValue);
    const current = () => (isCheckbox ? inputEl.checked : inputEl.value);
    const refresh = () => {
      const dirty = current() !== baseline;
      applyBtn.disabled = !dirty;
      revertBtn.style.display = dirty ? '' : 'none';
      status.textContent = dirty ? 'Unsaved change' : '';
    };
    inputEl.addEventListener('input', refresh);
    inputEl.addEventListener('change', refresh);

    applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      status.innerHTML = UI.spinner() + 'Applying…';
      try {
        await onApply(current());
        status.innerHTML = UI.icon('status-ok') + 'Applied';
        setTimeout(() => { status.textContent = ''; }, 1500);
      } catch (e) {
        status.innerHTML = UI.icon('status-error') + ('Error: ' + e.message);
        if (statusSetter) statusSetter('error: ' + e.message);
      }
      applyBtn.disabled = false;
    });
    revertBtn.addEventListener('click', () => {
      if (isCheckbox) inputEl.checked = baseline; else inputEl.value = baseline;
      refresh();
    });
  }

  function looksBoundedRaw(desc) {
    return desc.selfDescribing && desc.max > desc.min && desc.max <= 0xFFFF;
  }
  function byteWidthFor(max) {
    if (max <= 0xFF) return 1;
    if (max <= 0xFFFF) return 2;
    return 4;
  }
  function hexEncode(n, width) {
    return (n >>> 0).toString(16).padStart(width * 2, '0');
  }

  function numericStepper(desc, value, editable) {
    const wrap = document.createElement('span');
    wrap.className = 'b5-row';
    wrap.style.gap = 'var(--b5-space-2)';
    const input = document.createElement('input');
    input.type = 'number'; input.className = 'b5-input'; input.style.maxWidth = '10em'; input.value = value; input.disabled = !editable;
    const bounded = desc.min !== 0 || desc.max !== 0;
    if (bounded) { input.min = desc.min; input.max = desc.max; }
    wrap.appendChild(input);
    if (desc.unitSuffix) {
      const suf = document.createElement('span');
      suf.className = 'b5-text-muted b5-text-xs'; suf.textContent = desc.unitSuffix;
      wrap.appendChild(suf);
    }
    const errEl = document.createElement('span');
    errEl.className = 'b5-field__error';
    wrap.appendChild(errEl);

    input.addEventListener('input', () => {
      const n = Number(input.value);
      if (bounded && (n < desc.min || n > desc.max)) {
        errEl.innerHTML = UI.icon('status-error') + `must be ${desc.min}–${desc.max}`;
      } else {
        errEl.innerHTML = '';
      }
    });
    return { wrap, input };
  }

  async function saveDeviceParam(uid, desc, value, rowEl, statusSetter) {
    try {
      await Api.setDeviceParam(uid, desc.pid, value);
      const r = await Api.getDeviceParam(uid, desc.pid);
      const st = paramsCache[uid];
      if (st) st.values[desc.pid] = { ok: true, val: r };
      if (statusSetter) statusSetter('saved 0x' + desc.pid);
      notify('params');
    } catch (e) {
      if (statusSetter) statusSetter('error saving 0x' + desc.pid + ': ' + e.message);
    }
  }

  // --- Sensors section: gauges + live WS updates ---------------------------

  async function ensureSensors(uid, gen) {
    const st = sensorsCache[uid] || (sensorsCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    try {
      const readings = await Api.getDeviceSensors(uid);
      if (!stillCurrent(uid, gen)) return;
      sensorsCache[uid] = { readings, loading: false, error: null };
      notify('sensors');
    } catch (e) {
      if (!stillCurrent(uid, gen)) return;
      sensorsCache[uid] = { readings: [], loading: false, error: e.message };
      notify('sensors');
    }
  }

  function renderSensorsSection(container, f) {
    const uid = f.uid;
    const st = sensorsCache[uid];
    if (!st || st.loading) {
      container.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading sensors…</span>`;
      return;
    }
    if (st.error) {
      container.innerHTML = `<span class="b5-field__error">${UI.icon('status-error')}error: ${escapeHtml(st.error)}</span>`;
      return;
    }
    if (!st.readings.length) {
      container.innerHTML = `<div class="b5-empty">${UI.icon('status-pending')}<span class="b5-empty__title">No sensors reported</span><span class="b5-empty__body">This device did not declare any sensor parameters.</span></div>`;
      return;
    }
    container.innerHTML = '';
    const stack = document.createElement('div');
    stack.className = 'b5-stack';
    container.appendChild(stack);
    st.readings.forEach(r => stack.appendChild(renderGauge(uid, r)));
  }

  function renderGauge(uid, r) {
    const outOfRange = r.hasRange && r.hasNormalBand && !r.inNormalBand;
    const wrap = document.createElement('div');
    wrap.className = 'b5-card' + (outOfRange ? ' b5-panel--out-of-range' : '');

    const gauge = document.createElement('div');
    gauge.className = 'b5-gauge' + (outOfRange ? ' b5-gauge--out-of-range' : '') + (r.hasRange ? '' : ' b5-gauge--no-range');

    let trackHtml = '';
    if (r.hasRange) trackHtml = buildGaugeTrackHtml(r);

    gauge.innerHTML = `
      <div class="b5-gauge__head">
        <span class="b5-gauge__label">${escapeHtml(r.description || r.typeName)}</span>
        <span class="b5-gauge__reading">${escapeHtml(r.presentFormatted)}</span>
      </div>
      ${trackHtml}
      ${outOfRange ? `<span class="b5-gauge__flag">${UI.icon('status-warning')}Out of normal range</span>` : ''}
      ${!r.hasRange ? '<span class="b5-text-muted b5-text-sm">Range undeclared by device — showing raw value only.</span>' : ''}
    `;
    wrap.appendChild(gauge);

    const metaParts = [];
    if (r.recordsRange) metaParts.push(`lowest ${formatSensorRaw(r, r.lowest)} · highest ${formatSensorRaw(r, r.highest)}`);
    if (r.recordsValue) metaParts.push(`recorded ${formatSensorRaw(r, r.recorded)}`);
    if (metaParts.length) {
      const meta = document.createElement('div');
      meta.className = 'b5-text-muted b5-text-xs';
      meta.style.marginTop = 'var(--b5-space-2)';
      meta.textContent = metaParts.join(' · ');
      wrap.appendChild(meta);
    }

    if (r.recordsValue || r.recordsRange) {
      const actions = document.createElement('div');
      actions.className = 'b5-row';
      actions.style.marginTop = 'var(--b5-space-2)';
      if (r.recordsValue) {
        const btn = document.createElement('button');
        btn.className = 'b5-btn b5-btn--sm';
        btn.textContent = 'Record';
        btn.addEventListener('click', async () => {
          try { await Api.recordDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
          catch (e) { /* best-effort */ }
        });
        actions.appendChild(btn);
      }
      const resetBtn = document.createElement('button');
      resetBtn.className = 'b5-btn b5-btn--sm b5-btn--ghost';
      resetBtn.textContent = 'Reset';
      resetBtn.addEventListener('click', async () => {
        try { await Api.resetDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
        catch (e) { /* best-effort */ }
      });
      actions.appendChild(resetBtn);
      wrap.appendChild(actions);
    }

    return wrap;
  }

  async function refreshSensorsNow(uid) {
    const readings = await Api.getDeviceSensors(uid);
    sensorsCache[uid] = { readings, loading: false, error: null };
    notify('sensors');
  }

  function formatSensorRaw(r, raw) { return formatValue(raw, r.unit, r.prefix); }

  // buildGaugeTrackHtml builds the b5-gauge__track markup (band + ticks +
  // present marker, all positioned by inline left/width percentages, per
  // design-spec's gauge component) — a straight port of the previous SVG
  // gauge's math onto the design system's div-based gauge.
  function buildGaugeTrackHtml(r) {
    const min = r.rangeMin || 0, max = r.rangeMax || 0;
    const span = Math.max(1, max - min);
    const pct = (v) => Math.min(100, Math.max(0, ((v - min) / span) * 100));

    let band = '';
    if (r.hasNormalBand) {
      const x1 = pct(r.normalMin || 0), x2 = pct(r.normalMax || 0);
      band = `<div class="b5-gauge__band" style="left:${x1}%;width:${Math.max(0, x2 - x1)}%"></div>`;
    }

    let ticks = '';
    if (r.recordsRange) {
      ticks += `<div class="b5-gauge__tick" style="left:${pct(r.lowest)}%"></div>`;
      ticks += `<div class="b5-gauge__tick" style="left:${pct(r.highest)}%"></div>`;
    }
    if (r.recordsValue) {
      ticks += `<div class="b5-gauge__tick b5-gauge__tick--recorded" style="left:${pct(r.recorded)}%"></div>`;
    }

    const marker = `<div class="b5-gauge__marker" style="left:${pct(r.present)}%"></div>`;

    return `
      <div class="b5-gauge__track">${band}${ticks}${marker}</div>
      <div class="b5-gauge__scale"><span>${min}${r.unitSuffix ? ' ' + r.unitSuffix : ''}</span><span>${max}${r.unitSuffix ? ' ' + r.unitSuffix : ''}</span></div>
    `;
  }

  // --- Status section --------------------------------------------------------

  async function ensureStatus(uid, gen, forceFilter) {
    const st = statusCache[uid] || (statusCache[uid] = { filter: 'advisory', messages: [], loading: true, error: null });
    if (forceFilter) st.filter = forceFilter;
    st.loading = true;
    notify('status');
    try {
      const msgs = await Api.getDeviceStatus(uid, st.filter);
      if (!stillCurrent(uid, gen)) return;
      st.messages = msgs; st.loading = false; st.error = null;
    } catch (e) {
      if (!stillCurrent(uid, gen)) return;
      st.loading = false; st.error = e.message;
    }
    notify('status');
  }

  function renderStatusSection(container, f) {
    const uid = f.uid;
    const st = statusCache[uid] || (statusCache[uid] = { filter: 'advisory', messages: [], loading: true, error: null });
    const statusText = st.loading
      ? `<span class="b5-inline-wait">${UI.spinner()}loading…</span>`
      : (st.error ? `<span class="b5-field__error">${UI.icon('status-error')}error: ${escapeHtml(st.error)}</span>` : `<span class="b5-text-muted b5-text-sm">${st.messages.length} message(s)</span>`);
    container.innerHTML = `
      <div class="b5-filterbar">
        <div class="b5-filterbar__group">
          <label class="b5-visually-hidden">Severity</label>
          <select class="b5-select status-filter" style="width:auto">
            <option value="advisory" ${st.filter === 'advisory' ? 'selected' : ''}>Severity: Advisory</option>
            <option value="warning" ${st.filter === 'warning' ? 'selected' : ''}>Severity: Warning</option>
            <option value="error" ${st.filter === 'error' ? 'selected' : ''}>Severity: Error</option>
          </select>
          <button class="b5-btn b5-btn--sm btn-refresh-status">${UI.icon('refresh')}Refresh</button>
        </div>
        <span class="b5-filterbar__summary">${statusText}</span>
      </div>
      ${st.messages.length ? `
      <table class="b5-table b5-table--responsive">
        <thead><tr><th>Sub-device</th><th>Type</th><th>Message ID</th><th>Value 1</th><th>Value 2</th></tr></thead>
        <tbody>${st.messages.map(m => `
          <tr>
            <td data-label="Sub-device">${m.subDevice}</td>
            <td data-label="Type">${escapeHtml(m.typeName)}</td>
            <td data-label="Message ID" class="b5-table__mono">0x${m.messageId.toString(16).toUpperCase().padStart(4, '0')}</td>
            <td data-label="Value 1">${m.value1}</td>
            <td data-label="Value 2">${m.value2}</td>
          </tr>`).join('')}
        </tbody>
      </table>` : (st.loading ? '' : `<div class="b5-empty">${UI.icon('status-ok')}<span class="b5-empty__title">No messages at this severity</span></div>`)}
    `;
    container.querySelector('.status-filter').addEventListener('change', (e) => {
      ensureStatus(uid, selectGen, e.target.value);
    });
    container.querySelector('.btn-refresh-status').addEventListener('click', () => ensureStatus(uid, selectGen));
  }

  // --- shared unit/prefix formatting (mirrors internal/rdm/format.go) -----

  const PREFIX_MULT = {
    0x00: 1, 0x01: 1e-1, 0x02: 1e-2, 0x03: 1e-3, 0x04: 1e-6, 0x05: 1e-9,
    0x06: 1e-12, 0x07: 1e-15, 0x08: 1e-18, 0x09: 1e-21, 0x0A: 1e-24,
    0x11: 1e1, 0x12: 1e2, 0x13: 1e3, 0x14: 1e6, 0x15: 1e9, 0x16: 1e12,
    0x17: 1e15, 0x18: 1e18, 0x19: 1e21, 0x1A: 1e24,
  };
  const UNIT_SUFFIX = {
    0x00: '', 0x01: '°C', 0x02: 'V', 0x03: 'V', 0x04: 'V', 0x05: 'A', 0x06: 'A', 0x07: 'A',
    0x08: 'Hz', 0x09: 'Ω', 0x0A: 'W', 0x0B: 'kg', 0x0C: 'm', 0x0D: 'm²', 0x0E: 'm³',
    0x0F: 'kg/m³', 0x10: 'm/s', 0x11: 'm/s²', 0x12: 'N', 0x13: 'J', 0x14: 'Pa', 0x15: 's',
    0x16: '°', 0x17: 'sr', 0x18: 'cd', 0x19: 'lm', 0x1A: 'lx', 0x1B: 'IRE', 0x1C: 'B',
    0x1D: 'dB', 0x1E: 'dBV', 0x1F: 'dBW', 0x20: 'dBm', 0x21: '%', 0x22: 'mol/m³', 0x23: 'RPM', 0x24: 'B/s',
  };

  function formatValue(raw, unit, prefix) {
    const mult = PREFIX_MULT[prefix] || 1;
    const scaled = raw * mult;
    const suffix = UNIT_SUFFIX[unit] || '';
    const numStr = Math.abs(scaled) < 1000 && !Number.isInteger(scaled) ? scaled.toFixed(2) : String(Math.round(scaled));
    return suffix ? `${numStr} ${suffix}` : numStr;
  }

  // --- live WS updates -------------------------------------------------------
  // Wired once (guarded), regardless of how many times init() is called by
  // however many consumer screens.
  let liveWired = false;
  function wireLiveUpdates() {
    if (liveWired) return;
    liveWired = true;

    // introspect_progress/complete update paramsCache for whichever uid the
    // introspect was started against, even if the tech has since navigated
    // to a different device (so results are ready if they come back) — but
    // only NOTIFY (and thus trigger a caller's re-render) when that uid is
    // the one currently on screen. Skipping notify() for a uid that isn't
    // selectedUID means an introspect a tech kicked off on device A, then
    // walked away from to type device B's label, can no longer force a
    // rebuild of B's Parameters section it has nothing to do with.
    Live.on('introspect_progress', (msg) => {
      const st = paramsCache[msg.kind];
      if (!st) return;
      st.progress = msg.introspect;
      if (msg.kind === selectedUID) notify('params');
    });

    Live.on('introspect_complete', async (msg) => {
      const st = paramsCache[msg.kind] || (paramsCache[msg.kind] = { descriptors: [], values: {}, introspecting: false, progress: null });
      st.introspecting = false;
      if (msg.err) { if (msg.kind === selectedUID) notify('params'); return; }
      st.descriptors = msg.descriptors || [];
      await loadParamValues(msg.kind, st.descriptors, selectGen);
      if (msg.kind === selectedUID) notify('params');
    });

    // sensor_values is the periodic (DefaultSensorPollInterval, currently
    // 2s) background push for every subscribed device — see
    // server.go's handleWS. It keeps arriving for as long as a device's
    // Sensors section/accordion has EVER been opened this selection (there
    // is no auto-unsubscribe on tab/accordion switch, only on selecting a
    // different device — see select()/subscribeSensors doc comment), so a
    // tech who checks Sensors once and then switches to Parameters to edit
    // the Device Label keeps receiving these. Scoping this to 'sensors'
    // (rather than the old unscoped notify()) is the actual fix for the
    // bench report: it stops this push from ever touching the Parameters
    // section's DOM at all, so it cannot be the thing that reset an
    // in-progress edit there.
    Live.on('sensor_values', (msg) => {
      if (msg.kind !== selectedUID) return;
      sensorsCache[msg.kind] = { readings: msg.sensors, loading: false, error: null };
      notify('sensors');
    });
  }

  function init(onUpdateFn) {
    if (onUpdateFn) onUpdate(onUpdateFn);
    wireLiveUpdates();
  }

  return {
    init, onUpdate, select, deselect, noteDeviceInfo,
    renderInfoSection, renderParamsSection, renderSensorsSection, renderStatusSection,
    // exposed for the JS render-proof harness / tests:
    _caches: { infoCache, paramsCache, sensorsCache, statusCache },
  };
})();
