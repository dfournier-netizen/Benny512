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

  let selectedUID = null;
  let selectGen = 0;
  let settleTimer = null;
  let sensorsSubscribedUID = null;
  const subscribers = []; // () => void, called after any async cache update

  function onUpdate(fn) { subscribers.push(fn); }
  function notify() { subscribers.forEach(fn => { try { fn(); } catch (e) { /* one bad subscriber must not break the others */ } }); }

  function stillCurrent(uid, gen) { return uid === selectedUID && gen === selectGen; }

  // select is the fetch/subscription entry point — see file doc comment.
  function select(uid, sections) {
    sections = sections || {};
    if (uid !== selectedUID) {
      unsubscribeSensors();
      selectedUID = uid;
      selectGen++;
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

  // --- Info section --------------------------------------------------------

  async function ensureInfo(uid, gen) {
    const st = infoCache[uid] || (infoCache[uid] = {});
    if (st.loading) return;
    st.loading = true;
    const [deviceInfo, mfrLabel, model, swVersion, prodDetail] = await Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'manufacturer_label'),
      Api.getParam(uid, 'device_model_description'),
      Api.getParam(uid, 'software_version_label'),
      Api.getDeviceParam(uid, '0070'),
    ]);
    if (!stillCurrent(uid, gen)) return;
    st.loading = false;
    st.deviceInfo = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    st.mfrLabelVal = mfrLabel.status === 'fulfilled' ? mfrLabel.value.value : '';
    st.modelVal = model.status === 'fulfilled' ? model.value.value : '';
    st.swVersion = swVersion.status === 'fulfilled' ? swVersion.value.value : '';
    st.prodDetailHex = prodDetail.status === 'fulfilled' ? prodDetail.value.hex : '';
    notify();
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
    container.innerHTML = `
      <div class="b5-grid-2">
        ${infoRow('Manufacturer', `${escapeHtml(effectiveMfr)} (0x${f.manufacturerId.toString(16).toUpperCase().padStart(4, '0')})`)}
        ${infoRow('Model / fixture type', escapeHtml(modelText))}
        ${infoRow('Manufacturer label (device-reported)', st.mfrLabelVal ? escapeHtml(st.mfrLabelVal) : '<span class="b5-text-muted b5-text-sm">not reported by device</span>')}
        ${infoRow('Software version', st.swVersion ? escapeHtml(st.swVersion) : '—')}
        ${infoRow('Node / port', `${escapeHtml(f.nodeIp)} (bind ${f.bindIndex}) / addr ${f.portAddress}`)}
        ${infoRow('DMX footprint', di ? String(di.DMXFootprint) : '—')}
        ${infoRow('DMX start address', di ? escapeHtml(Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true)) : '—')}
        ${infoRow('Personality', di ? `${di.CurrentPersonality} of ${di.PersonalityCount}` : '—')}
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
  async function ensureParams(uid, gen) {
    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });
    if (st.loading) return;
    st.loading = true;
    const [deviceInfo, label, personality, ident, curve, ort, modFreq, minLevel, maxLevel, identMode] = await Promise.allSettled([
      Api.getParam(uid, 'device_info'),
      Api.getParam(uid, 'device_label'),
      Api.getParam(uid, 'dmx_personality'),
      Api.getParam(uid, 'identify_device'),
      Api.getParam(uid, 'curve'),
      Api.getParam(uid, 'output_response_time'),
      Api.getParam(uid, 'modulation_frequency'),
      Api.getParam(uid, 'minimum_level'),
      Api.getParam(uid, 'maximum_level'),
      Api.getParam(uid, 'identify_mode'),
    ]);
    if (!stillCurrent(uid, gen)) return;
    st.loading = false;
    st.di = deviceInfo.status === 'fulfilled' ? deviceInfo.value.value : null;
    st.lbl = label.status === 'fulfilled' ? label.value.value : '';
    st.pers = personality.status === 'fulfilled' ? personality.value.value : null;
    st.identOn = ident.status === 'fulfilled' ? ident.value.value : false;
    st.curve = curve.status === 'fulfilled' ? curve.value.value : null;
    st.ort = ort.status === 'fulfilled' ? ort.value.value : null;
    st.modFreq = modFreq.status === 'fulfilled' ? modFreq.value.value : null;
    st.minLevel = minLevel.status === 'fulfilled' ? minLevel.value.value : null;
    st.maxLevel = maxLevel.status === 'fulfilled' ? maxLevel.value.value : null;
    st.identMode = identMode.status === 'fulfilled' ? identMode.value.value : null;
    notify();

    // Cached descriptors (no wire traffic — matches the pre-existing
    // "Introspect is user-triggered, not automatic" rule, task ask: "never
    // fired automatically for every device during a walk").
    try {
      const descs = await Api.getDeviceParams(uid);
      if (!stillCurrent(uid, gen)) return;
      st.descriptors = descs;
      await loadParamValues(uid, descs, gen);
    } catch (e) { /* best-effort */ }
    if (stillCurrent(uid, gen)) notify();

    // Labeled-dropdown companions (DMX_PERSONALITY_DESCRIPTION and the new
    // CURVE_DESCRIPTION/OUTPUT_RESPONSE_TIME_DESCRIPTION/MODULATION_
    // FREQUENCY_DESCRIPTION) — fetched lazily, one GET per index, only once
    // the base value resolved a Count (task ask: "where a companion
    // exists, fetch it and render a labeled dropdown"). Each is
    // independent and best-effort: a device that NACKs the description PID
    // (or doesn't implement it) falls back to a bare numeric dropdown.
    await Promise.allSettled([
      loadIndexedLabels(uid, gen, 'personality', st.pers, 'dmx_personality_description', d => d.Description),
      loadIndexedLabels(uid, gen, 'curveLabels', st.curve, 'curve_description', d => d.Description),
      loadIndexedLabels(uid, gen, 'ortLabels', st.ort, 'output_response_time_description', d => d.Description),
      loadIndexedLabels(uid, gen, 'modFreqLabels', st.modFreq, 'modulation_frequency_description', d => d.Description),
    ]);
    if (stillCurrent(uid, gen)) notify();
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

  async function loadParamValues(uid, descs, gen) {
    const st = paramsCache[uid];
    const results = await Promise.allSettled(descs.map(d => Api.getDeviceParam(uid, d.pid)));
    if (!stillCurrent(uid, gen)) return;
    results.forEach((r, i) => {
      const pid = descs[i].pid;
      st.values[pid] = r.status === 'fulfilled' ? { ok: true, val: r.value } : { ok: false, err: r.reason.message };
    });
  }

  function introspectStatusText(st) {
    if (st.introspecting && st.progress) return `describing ${st.progress.done}/${st.progress.total} (0x${st.progress.pid})`;
    if (st.introspecting) return 'starting…';
    if (st.descriptors.length) return `${st.descriptors.length} parameter(s) known`;
    return 'no manufacturer/unrecognized-PID parameters resolved yet';
  }

  async function startIntrospect(uid) {
    const st = paramsCache[uid] || (paramsCache[uid] = { descriptors: [], values: {}, introspecting: false, progress: null });
    st.introspecting = true;
    st.progress = null;
    notify();
    try {
      await Api.introspectDevice(uid);
    } catch (e) {
      st.introspecting = false;
      notify();
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
    const st = paramsCache[uid];
    if (!st || (st.loading && st.di === undefined)) {
      container.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Loading…</span>`;
      return;
    }
    const di = st.di, lbl = st.lbl || '', pers = st.pers, identOn = !!st.identOn;

    container.innerHTML = '';
    const standard = document.createElement('div');
    standard.className = 'b5-stack';
    container.appendChild(standard);

    const labelField = UI.buildApplyField({ label: 'Device label', value: lbl, enabled: !!di, maxLength: 32 });
    standard.appendChild(labelField.wrap);
    UI.wireApplyField(labelField, lbl, async (v) => { await saveParam(uid, 'device_label', v, statusSetter); }, statusSetter);

    if (!opts.hideAddressField) {
      const addrHint = di ? `Currently ${Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true)}` : undefined;
      const addrField = UI.buildApplyField({
        label: 'Start address', kind: 'number', mono: true, min: 1, max: 512,
        value: di && di.DMXFootprint ? di.DMXStartAddress : '', enabled: !!(di && di.DMXFootprint), hint: addrHint,
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
    });
    standard.appendChild(persField.wrap);
    UI.wireApplyField(persField, pers ? String(pers.Current) : '', async (v) => {
      await saveParam(uid, 'dmx_personality', parseInt(v, 10), statusSetter);
    }, statusSetter);

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

    const rowsEl = mfrSection.querySelector('.param-rows');
    if (!st.descriptors.length) {
      rowsEl.innerHTML = '<p class="b5-text-muted b5-text-sm">Click Introspect to walk SUPPORTED_PARAMETERS + PARAMETER_DESCRIPTION (also surfaces any standard ESTA PID this app has no typed decoder for, via the same raw-hex fallback).</p>';
    } else {
      st.descriptors.slice().sort((a, b) => a.pid.localeCompare(b.pid)).forEach(desc => {
        rowsEl.appendChild(renderParamRow(uid, desc, st.values[desc.pid], statusSetter));
      });
    }
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

    function indexedRow(title, choice, labels, pidGet) {
      if (!choice) return;
      any = true;
      const opts = Array.from({ length: choice.Count }, (_, i) => i + 1)
        .map(i => ({ value: i, label: (labels && labels[i]) ? `${i} — ${labels[i]}` : String(i) }));
      const hint = (!labels || Object.keys(labels).length < choice.Count) ? 'fetching labels…' : undefined;
      const field = UI.buildApplyField({ label: title, kind: 'select', enabled: true, value: choice.Current, options: opts, hint });
      UI.wireApplyField(field, String(choice.Current), pidGet, statusSetter);
      wrap.appendChild(field.wrap);
    }

    indexedRow('Dimmer curve', st.curve, st.curveLabels, async (v) => saveParam(uid, 'curve', parseInt(v, 10), statusSetter));
    indexedRow('Output response time', st.ort, st.ortLabels, async (v) => saveParam(uid, 'output_response_time', parseInt(v, 10), statusSetter));
    indexedRow('Modulation frequency', st.modFreq, st.modFreqLabels, async (v) => saveParam(uid, 'modulation_frequency', parseInt(v, 10), statusSetter));

    if (st.minLevel) {
      any = true;
      const field = document.createElement('div');
      field.className = 'b5-field';
      field.innerHTML = `
        <label class="b5-field__label">Minimum level (rise / fall)</label>
        <div class="b5-field__row">
          <input class="b5-input" type="number" min="0" max="65535" value="${st.minLevel.Increasing}" style="max-width:8em">
          <span class="b5-text-muted">/</span>
          <input class="b5-input" type="number" min="0" max="65535" value="${st.minLevel.Decreasing}" style="max-width:8em">
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
          notify();
        } catch (e) { statusSetter('error: ' + e.message); }
      });
      wrap.appendChild(field);
    }

    if (st.maxLevel !== null && st.maxLevel !== undefined) {
      any = true;
      const field = UI.buildApplyField({ label: 'Maximum level', kind: 'number', enabled: true, value: st.maxLevel, min: 0, max: 65535 });
      UI.wireApplyField(field, String(st.maxLevel), async (v) => saveParam(uid, 'maximum_level', parseInt(v, 10), statusSetter), statusSetter);
      wrap.appendChild(field.wrap);
    }

    if (st.identMode !== null && st.identMode !== undefined) {
      any = true;
      const field = UI.buildApplyField({ label: 'Identify mode', kind: 'select', enabled: true, value: st.identMode, options: [{ value: 0, label: 'Quiet' }, { value: 1, label: 'Loud' }] });
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

    if (desc.dataType === DS.BOOLEAN || (desc.dataType === DS.BIT_FIELD && desc.pdlSize === 1)) {
      const checked = desc.dataType === DS.BOOLEAN ? v.int !== 0 : (v.hex && parseInt(v.hex.slice(0, 2), 16) !== 0);
      field.innerHTML = `<label class="b5-toggle"><input type="checkbox" ${checked ? 'checked' : ''} ${editable ? '' : 'disabled'}><span class="b5-toggle__track"></span></label>`;
      const cb = field.querySelector('input');
      if (editable) {
        wireRowApply(field, cb, true, checked, (checkedNow) => {
          const value = desc.dataType === DS.BOOLEAN ? (checkedNow ? 1 : 0) : (checkedNow ? '01' : '00');
          return saveDeviceParam(uid, desc, value, row, statusSetter);
        }, statusSetter);
      }
    } else if (v.kind === 'string') {
      field.innerHTML = `<input class="b5-input" type="text" maxlength="32" value="${escapeHtml(v.str || '')}" ${editable ? '' : 'disabled'}>`;
      const input = field.querySelector('input');
      if (editable) {
        wireRowApply(field, input, false, v.str || '', (val) => saveDeviceParam(uid, desc, val, row, statusSetter), statusSetter);
      }
    } else if (v.kind === 'int') {
      const { wrap, input } = numericStepper(desc, v.int, editable);
      field.appendChild(wrap);
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
      if (editable) {
        wireRowApply(field, input, false, current, (val) => saveDeviceParam(uid, desc, hexEncode(Number(val), width), row, statusSetter), statusSetter);
      }
    } else {
      field.innerHTML = `<input class="b5-input b5-input--mono" type="text" placeholder="hex bytes, e.g. DEAD" value="${escapeHtml(v.hex || '')}" ${editable ? '' : 'disabled'}>`;
      const input = field.querySelector('input');
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
      notify();
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
      notify();
    } catch (e) {
      if (!stillCurrent(uid, gen)) return;
      sensorsCache[uid] = { readings: [], loading: false, error: e.message };
      notify();
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
    notify();
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
    notify();
    try {
      const msgs = await Api.getDeviceStatus(uid, st.filter);
      if (!stillCurrent(uid, gen)) return;
      st.messages = msgs; st.loading = false; st.error = null;
    } catch (e) {
      if (!stillCurrent(uid, gen)) return;
      st.loading = false; st.error = e.message;
    }
    notify();
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

    Live.on('introspect_progress', (msg) => {
      const st = paramsCache[msg.kind];
      if (!st) return;
      st.progress = msg.introspect;
      notify();
    });

    Live.on('introspect_complete', async (msg) => {
      const st = paramsCache[msg.kind] || (paramsCache[msg.kind] = { descriptors: [], values: {}, introspecting: false, progress: null });
      st.introspecting = false;
      if (msg.err) { notify(); return; }
      st.descriptors = msg.descriptors || [];
      await loadParamValues(msg.kind, st.descriptors, selectGen);
      notify();
    });

    Live.on('sensor_values', (msg) => {
      if (msg.kind !== selectedUID) return;
      sensorsCache[msg.kind] = { readings: msg.sensors, loading: false, error: null };
      notify();
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
