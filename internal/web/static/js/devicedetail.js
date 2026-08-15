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

  function renderInfoSection(container, f) {
    const uid = f.uid;
    const st = infoCache[uid];
    if (!st || (st.loading && st.deviceInfo === undefined)) {
      container.innerHTML = '<p class="hint">Loading…</p>';
      return;
    }
    const di = st.deviceInfo;
    let detailsHtml = '<span class="hint">none reported</span>';
    if (st.prodDetailHex) {
      const hex = st.prodDetailHex;
      const names = [];
      for (let i = 0; i + 4 <= hex.length; i += 4) {
        const code = parseInt(hex.slice(i, i + 4), 16);
        names.push(PRODUCT_DETAIL_NAMES[code] || `0x${code.toString(16).toUpperCase().padStart(4, '0')}`);
      }
      if (names.length) detailsHtml = names.map(n => `<span class="badge detail-badge">${escapeHtml(n)}</span>`).join(' ');
    }
    const effectiveMfr = st.mfrLabelVal || f.manufacturerName;
    const modelText = st.modelVal || (di ? `0x${di.DeviceModelID.toString(16).toUpperCase().padStart(4, '0')}` : '—');
    container.innerHTML = `
      <div class="field-row"><label>Manufacturer</label><span>${escapeHtml(effectiveMfr)} (0x${f.manufacturerId.toString(16).toUpperCase().padStart(4, '0')})</span></div>
      <div class="field-row"><label>Model / fixture type</label><span>${escapeHtml(modelText)}</span></div>
      <div class="field-row"><label>Manufacturer label (device-reported)</label><span>${st.mfrLabelVal ? escapeHtml(st.mfrLabelVal) : '<span class="hint">not reported by device</span>'}</span></div>
      <div class="field-row"><label>Software version</label><span>${st.swVersion ? escapeHtml(st.swVersion) : '—'}</span></div>
      <div class="field-row"><label>Node / port</label><span>${escapeHtml(f.nodeIp)} (bind ${f.bindIndex}) / addr ${f.portAddress}</span></div>
      <div class="field-row"><label>DMX footprint</label><span>${di ? di.DMXFootprint : '—'}</span></div>
      <div class="field-row"><label>DMX start address</label><span>${di ? Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true) : '—'}</span></div>
      <div class="field-row"><label>Personality</label><span>${di ? `${di.CurrentPersonality} of ${di.PersonalityCount}` : '—'}</span></div>
      <div class="field-row"><label>Sub-devices</label><span>${di ? di.SubDeviceCount : '—'}</span></div>
      <div class="field-row"><label>Sensors</label><span>${di ? di.SensorCount : '—'}</span></div>
      <div class="field-row"><label>Product details</label><span>${detailsHtml}</span></div>
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
  // id.
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
      container.innerHTML = '<p class="hint">Loading…</p>';
      return;
    }
    const di = st.di, lbl = st.lbl || '', pers = st.pers, identOn = !!st.identOn;

    container.innerHTML = '';
    const standard = document.createElement('div');
    standard.className = 'device-detail-standard';
    container.appendChild(standard);

    standard.innerHTML = `
      <h4>Standard parameters</h4>
      <div class="field-row" data-field="label"><label>Device label</label></div>
      ${opts.hideAddressField ? '' : `<div class="field-row" data-field="address"><label>Start address${di ? ` <span class="hint">(${Api.formatAddressRange(di.DMXStartAddress, di.DMXFootprint, true)})</span>` : ''}</label></div>`}
      <div class="field-row" data-field="personality"><label>Personality</label></div>
      <div class="field-row" data-field="identify"><label>Identify</label></div>
    `;

    const labelField = buildTextApplyField(lbl, !!di, 32);
    standard.querySelector('[data-field="label"]').appendChild(labelField.wrap);
    wireApplyField(labelField, lbl, async (v) => { await saveParam(uid, 'device_label', v, statusSetter); }, statusSetter);

    if (!opts.hideAddressField) {
      const addrField = buildNumberApplyField(di && di.DMXFootprint ? di.DMXStartAddress : '', !!(di && di.DMXFootprint), 1, 512);
      standard.querySelector('[data-field="address"]').appendChild(addrField.wrap);
      wireApplyField(addrField, di && di.DMXFootprint ? String(di.DMXStartAddress) : '', async (v) => {
        await saveParam(uid, 'dmx_start_address', parseInt(v, 10), statusSetter);
      }, statusSetter);
    }

    const personalityLabels = st.personality || {};
    const persField = buildSelectApplyField(
      pers ? Array.from({ length: pers.Count }, (_, i) => i + 1).map(i => ({ value: i, label: personalityLabels[i] ? `${i} — ${personalityLabels[i]}` : String(i) })) : [],
      pers ? pers.Current : null, !!pers);
    standard.querySelector('[data-field="personality"]').appendChild(persField.wrap);
    wireApplyField(persField, pers ? String(pers.Current) : '', async (v) => {
      await saveParam(uid, 'dmx_personality', parseInt(v, 10), statusSetter);
    }, statusSetter);

    const identRow = standard.querySelector('[data-field="identify"]');
    const identCb = document.createElement('input');
    identCb.type = 'checkbox'; identCb.checked = identOn;
    identRow.appendChild(identCb);
    // fxIdentify is the one deliberate Apply-to-confirm exception (a
    // momentary physical action, not a persisted parameter) — same
    // sign-off as the pre-existing Devices screen.
    identCb.addEventListener('change', async () => {
      try {
        await Api.identify(uid, identCb.checked);
        statusSetter('identify ' + (identCb.checked ? 'on' : 'off'));
      } catch (err) {
        statusSetter('error: ' + err.message);
      }
    });

    renderDimmerFields(standard, uid, st, statusSetter);

    const mfrSection = document.createElement('div');
    mfrSection.className = 'device-detail-mfr';
    mfrSection.innerHTML = `
      <h4>Manufacturer &amp; unrecognized parameters</h4>
      <div class="panel-toolbar">
        <button class="btn-introspect" ${st.introspecting ? 'disabled' : ''}>${st.introspecting ? 'Introspecting…' : 'Introspect'}</button>
        <span class="hint introspect-status">${introspectStatusText(st)}</span>
      </div>
      <div class="param-rows"></div>
    `;
    container.appendChild(mfrSection);
    mfrSection.querySelector('.btn-introspect').addEventListener('click', () => startIntrospect(uid));

    const rowsEl = mfrSection.querySelector('.param-rows');
    if (!st.descriptors.length) {
      rowsEl.innerHTML = '<p class="hint">Click Introspect to walk SUPPORTED_PARAMETERS + PARAMETER_DESCRIPTION (also surfaces any standard ESTA PID this app has no typed decoder for, via the same raw-hex fallback).</p>';
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
    wrap.className = 'device-detail-dimmer';
    const container = wrap; // rows below append into wrap; wrap is only
    // attached to outerContainer at the end, and only if at least one
    // dimmer field actually resolved (a device that implements none of
    // E1.37-1 gets no empty "Dimmer" heading).
    let any = false;

    function indexedRow(title, choice, labels, pidGet, cacheNote) {
      if (!choice) return;
      any = true;
      const row = document.createElement('div');
      row.className = 'field-row';
      const label = document.createElement('label');
      label.textContent = title;
      row.appendChild(label);
      const opts = Array.from({ length: choice.Count }, (_, i) => i + 1)
        .map(i => ({ value: i, label: (labels && labels[i]) ? `${i} — ${labels[i]}` : String(i) }));
      const field = buildSelectApplyField(opts, choice.Current, true);
      row.appendChild(field.wrap);
      if (!labels || Object.keys(labels).length < choice.Count) {
        const note = document.createElement('span');
        note.className = 'hint';
        note.textContent = ' (fetching labels…)';
        row.appendChild(note);
      }
      wireApplyField(field, String(choice.Current), pidGet, statusSetter);
      container.appendChild(row);
    }

    indexedRow('Dimmer curve', st.curve, st.curveLabels, async (v) => saveParam(uid, 'curve', parseInt(v, 10), statusSetter));
    indexedRow('Output response time', st.ort, st.ortLabels, async (v) => saveParam(uid, 'output_response_time', parseInt(v, 10), statusSetter));
    indexedRow('Modulation frequency', st.modFreq, st.modFreqLabels, async (v) => saveParam(uid, 'modulation_frequency', parseInt(v, 10), statusSetter));

    if (st.minLevel) {
      any = true;
      const row = document.createElement('div');
      row.className = 'field-row';
      row.innerHTML = '<label>Minimum level (rise / fall)</label>';
      const wrapField = document.createElement('span');
      wrapField.className = 'apply-field';
      const riseInput = document.createElement('input');
      riseInput.type = 'number'; riseInput.min = 0; riseInput.max = 65535; riseInput.value = st.minLevel.Increasing;
      riseInput.style.width = '6em';
      const fallInput = document.createElement('input');
      fallInput.type = 'number'; fallInput.min = 0; fallInput.max = 65535; fallInput.value = st.minLevel.Decreasing;
      fallInput.style.width = '6em';
      wrapField.appendChild(riseInput);
      wrapField.appendChild(document.createTextNode(' / '));
      wrapField.appendChild(fallInput);
      row.appendChild(wrapField);
      const applyBtn = document.createElement('button');
      applyBtn.className = 'btn-apply'; applyBtn.textContent = 'Apply'; applyBtn.disabled = true;
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
      row.appendChild(applyBtn);
      container.appendChild(row);
    }

    if (st.maxLevel !== null && st.maxLevel !== undefined) {
      any = true;
      const row = document.createElement('div');
      row.className = 'field-row';
      row.innerHTML = '<label>Maximum level</label>';
      const field = buildNumberApplyField(st.maxLevel, true, 0, 65535);
      row.appendChild(field.wrap);
      wireApplyField(field, String(st.maxLevel), async (v) => saveParam(uid, 'maximum_level', parseInt(v, 10), statusSetter), statusSetter);
      container.appendChild(row);
    }

    if (st.identMode !== null && st.identMode !== undefined) {
      any = true;
      const row = document.createElement('div');
      row.className = 'field-row';
      row.innerHTML = '<label>Identify mode</label>';
      const field = buildSelectApplyField([{ value: 0, label: 'Quiet' }, { value: 1, label: 'Loud' }], st.identMode, true);
      row.appendChild(field.wrap);
      wireApplyField(field, String(st.identMode), async (v) => saveParam(uid, 'identify_mode', v === '1', statusSetter), statusSetter);
      container.appendChild(row);
    }

    if (any) {
      const h = document.createElement('h4');
      h.textContent = 'Dimmer (E1.37-1)';
      wrap.insertBefore(h, wrap.firstChild);
      outerContainer.appendChild(wrap);
    }
  }

  // --- apply-to-confirm field builders ------------------------------------
  // These build a self-contained {wrap, input, applyBtn, revertBtn,
  // dirtyBadge} bundle via createElement (never document.getElementById),
  // so the SAME shared component can be mounted twice in the document at
  // once (Devices tab + Rig Walk) with zero id collisions.

  function buildTextApplyField(value, enabled, maxLength) {
    const wrap = document.createElement('span');
    wrap.className = 'apply-field';
    const input = document.createElement('input');
    input.type = 'text'; input.value = value; input.disabled = !enabled;
    if (maxLength) input.maxLength = maxLength;
    wrap.appendChild(input);
    return finishApplyField(wrap, input);
  }
  function buildNumberApplyField(value, enabled, min, max) {
    const wrap = document.createElement('span');
    wrap.className = 'apply-field';
    const input = document.createElement('input');
    input.type = 'number'; input.value = value; input.disabled = !enabled;
    if (min !== undefined) input.min = min;
    if (max !== undefined) input.max = max;
    wrap.appendChild(input);
    return finishApplyField(wrap, input);
  }
  function buildSelectApplyField(options, current, enabled) {
    const wrap = document.createElement('span');
    wrap.className = 'apply-field';
    const input = document.createElement('select');
    input.disabled = !enabled;
    options.forEach(o => {
      const opt = document.createElement('option');
      opt.value = o.value; opt.textContent = o.label;
      if (o.value === current) opt.selected = true;
      input.appendChild(opt);
    });
    wrap.appendChild(input);
    return finishApplyField(wrap, input);
  }
  function finishApplyField(wrap, input) {
    const dirtyBadge = document.createElement('span');
    dirtyBadge.className = 'badge dirty-badge'; dirtyBadge.textContent = 'pending'; dirtyBadge.style.display = 'none';
    const applyBtn = document.createElement('button');
    applyBtn.type = 'button'; applyBtn.className = 'btn-apply'; applyBtn.textContent = 'Apply'; applyBtn.disabled = true;
    const revertBtn = document.createElement('button');
    revertBtn.type = 'button'; revertBtn.className = 'btn-revert'; revertBtn.textContent = 'Revert'; revertBtn.style.display = 'none';
    wrap.appendChild(dirtyBadge);
    wrap.appendChild(applyBtn);
    wrap.appendChild(revertBtn);
    return { wrap, input, applyBtn, revertBtn, dirtyBadge };
  }

  // wireApplyField: oninput/onchange only toggles Apply/Revert/dirty
  // visibility (never sends anything); Apply reads the live value at click
  // time and calls onApply(value). This is the project-wide Apply-to-
  // confirm pattern (task ask: "Apply-to-confirm on every editable field").
  function wireApplyField(field, baseline, onApply, statusSetter) {
    const { input, applyBtn, revertBtn, dirtyBadge } = field;
    const refresh = () => {
      const dirty = input.value !== baseline;
      applyBtn.disabled = !dirty;
      revertBtn.style.display = dirty ? '' : 'none';
      dirtyBadge.style.display = dirty ? '' : 'none';
    };
    input.addEventListener('input', refresh);
    input.addEventListener('change', refresh);
    revertBtn.addEventListener('click', () => { input.value = baseline; refresh(); });
    applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      try {
        await onApply(input.value);
        baseline = input.value;
        refresh();
      } catch (e) {
        if (statusSetter) statusSetter('error: ' + e.message);
        applyBtn.disabled = false;
      }
    });
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
    row.className = 'param-row';
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
    meta.className = 'param-row-meta';
    meta.innerHTML = `
      <span class="pid-hex">0x${desc.pid}</span>
      <span class="param-label">${escapeHtml(label)}</span>
      <span class="hint">${escapeHtml(desc.dataTypeName)}${rangeHint ? ' · ' + escapeHtml(rangeHint) : ''}</span>
      ${!editable ? '<span class="badge ro-badge">read-only</span>' : ''}
      ${!desc.selfDescribing ? '<span class="badge adv-badge">raw / unverified</span>' : ''}
    `;
    row.appendChild(meta);

    const field = document.createElement('div');
    field.className = 'param-row-field';
    row.appendChild(field);

    if (!valState) {
      field.innerHTML = '<span class="hint">loading…</span>';
      return row;
    }
    if (!valState.ok) {
      field.innerHTML = `<span class="hint">error: ${escapeHtml(valState.err)}</span>`;
      return row;
    }
    const v = valState.val;

    if (desc.dataType === DS.BOOLEAN || (desc.dataType === DS.BIT_FIELD && desc.pdlSize === 1)) {
      const checked = desc.dataType === DS.BOOLEAN ? v.int !== 0 : (v.hex && parseInt(v.hex.slice(0, 2), 16) !== 0);
      const cb = document.createElement('input');
      cb.type = 'checkbox'; cb.checked = checked; cb.disabled = !editable;
      field.appendChild(cb);
      if (editable) {
        wireRowApply(field, cb, true, checked, (checkedNow) => {
          const value = desc.dataType === DS.BOOLEAN ? (checkedNow ? 1 : 0) : (checkedNow ? '01' : '00');
          return saveDeviceParam(uid, desc, value, row, statusSetter);
        }, statusSetter);
      }
    } else if (v.kind === 'string') {
      const input = document.createElement('input');
      input.type = 'text'; input.value = v.str || ''; input.maxLength = 32; input.disabled = !editable;
      field.appendChild(input);
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
      const input = document.createElement('input');
      input.type = 'text'; input.className = 'hex-input'; input.placeholder = 'hex bytes, e.g. DEAD';
      input.value = v.hex || ''; input.disabled = !editable;
      field.appendChild(input);
      if (editable) {
        wireRowApply(field, input, false, v.hex || '', (val) => saveDeviceParam(uid, desc, val.replace(/\s+/g, ''), row, statusSetter), statusSetter);
      }
    }
    return row;
  }

  function wireRowApply(field, inputEl, isCheckbox, baselineValue, onApply, statusSetter) {
    const applyBtn = document.createElement('button');
    applyBtn.type = 'button'; applyBtn.className = 'btn-apply'; applyBtn.textContent = 'Apply'; applyBtn.disabled = true;
    const revertBtn = document.createElement('button');
    revertBtn.type = 'button'; revertBtn.className = 'btn-revert'; revertBtn.textContent = 'Revert'; revertBtn.style.display = 'none';
    const dirtyBadge = document.createElement('span');
    dirtyBadge.className = 'badge dirty-badge'; dirtyBadge.textContent = 'pending'; dirtyBadge.style.display = 'none';

    const baseline = isCheckbox ? !!baselineValue : String(baselineValue);
    const current = () => (isCheckbox ? inputEl.checked : inputEl.value);
    const refresh = () => {
      const dirty = current() !== baseline;
      applyBtn.disabled = !dirty;
      revertBtn.style.display = dirty ? '' : 'none';
      dirtyBadge.style.display = dirty ? '' : 'none';
    };
    inputEl.addEventListener('input', refresh);
    inputEl.addEventListener('change', refresh);

    applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      try {
        await onApply(current());
      } catch (e) {
        if (statusSetter) statusSetter('error: ' + e.message);
      }
      applyBtn.disabled = false;
    });
    revertBtn.addEventListener('click', () => {
      if (isCheckbox) inputEl.checked = baseline; else inputEl.value = baseline;
      refresh();
    });

    field.appendChild(dirtyBadge);
    field.appendChild(applyBtn);
    field.appendChild(revertBtn);
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
    const input = document.createElement('input');
    input.type = 'number'; input.value = value; input.disabled = !editable;
    const bounded = desc.min !== 0 || desc.max !== 0;
    if (bounded) { input.min = desc.min; input.max = desc.max; }
    wrap.appendChild(input);
    if (desc.unitSuffix) {
      const suf = document.createElement('span');
      suf.className = 'hint'; suf.textContent = ' ' + desc.unitSuffix;
      wrap.appendChild(suf);
    }
    const errEl = document.createElement('span');
    errEl.className = 'hint field-error';
    wrap.appendChild(errEl);

    input.addEventListener('input', () => {
      const n = Number(input.value);
      if (bounded && (n < desc.min || n > desc.max)) {
        errEl.textContent = `must be ${desc.min}–${desc.max}`;
      } else {
        errEl.textContent = '';
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
      container.innerHTML = '<p class="hint">Loading sensors…</p>';
      return;
    }
    if (st.error) {
      container.innerHTML = `<p class="hint">error: ${escapeHtml(st.error)}</p>`;
      return;
    }
    if (!st.readings.length) {
      container.innerHTML = '<p class="empty-hint">This device reports no sensors.</p>';
      return;
    }
    container.innerHTML = '';
    st.readings.forEach(r => container.appendChild(renderGauge(uid, r)));
  }

  function renderGauge(uid, r) {
    const wrap = document.createElement('div');
    wrap.className = 'gauge-card' + (r.hasRange && !r.inNormalBand ? ' out-of-band' : '');

    const header = document.createElement('div');
    header.className = 'gauge-header';
    header.innerHTML = `
      <span class="gauge-title">${escapeHtml(r.description || r.typeName)}</span>
      <span class="gauge-value">${escapeHtml(r.presentFormatted)}</span>
      ${(r.hasRange && !r.inNormalBand) ? '<span class="badge warn-badge">⚠ outside normal range</span>' : ''}
    `;
    wrap.appendChild(header);

    if (r.hasRange) {
      wrap.appendChild(buildGaugeSVG(r));
    } else {
      const plain = document.createElement('div');
      plain.className = 'hint';
      plain.textContent = 'range undeclared by device — showing raw value only.';
      wrap.appendChild(plain);
    }

    const meta = document.createElement('div');
    meta.className = 'gauge-meta hint';
    const parts = [];
    if (r.recordsRange) parts.push(`lowest ${formatSensorRaw(r, r.lowest)} · highest ${formatSensorRaw(r, r.highest)}`);
    if (r.recordsValue) parts.push(`recorded ${formatSensorRaw(r, r.recorded)}`);
    meta.textContent = parts.join(' · ');
    wrap.appendChild(meta);

    const actions = document.createElement('div');
    actions.className = 'gauge-actions';
    if (r.recordsValue) {
      const btn = document.createElement('button');
      btn.textContent = 'Record';
      btn.addEventListener('click', async () => {
        try { await Api.recordDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
        catch (e) { /* best-effort */ }
      });
      actions.appendChild(btn);
    }
    if (r.recordsValue || r.recordsRange) {
      const btn = document.createElement('button');
      btn.textContent = 'Reset';
      btn.addEventListener('click', async () => {
        try { await Api.resetDeviceSensors(uid, r.number); await refreshSensorsNow(uid); }
        catch (e) { /* best-effort */ }
      });
      actions.appendChild(btn);
    }
    wrap.appendChild(actions);

    return wrap;
  }

  async function refreshSensorsNow(uid) {
    const readings = await Api.getDeviceSensors(uid);
    sensorsCache[uid] = { readings, loading: false, error: null };
    notify();
  }

  function formatSensorRaw(r, raw) { return formatValue(raw, r.unit, r.prefix); }

  function buildGaugeSVG(r) {
    const div = document.createElement('div');
    const min = r.rangeMin || 0, max = r.rangeMax || 0;
    const span = Math.max(1, max - min);
    const pct = (v) => Math.min(100, Math.max(0, ((v - min) / span) * 100));

    const trackX = 4, trackW = 92;
    const toX = (v) => trackX + (pct(v) / 100) * trackW;

    let normalBand = '';
    if (r.hasNormalBand) {
      const x1 = toX(r.normalMin || 0), x2 = toX(r.normalMax || 0);
      normalBand = `<rect x="${x1}" y="18" width="${Math.max(0, x2 - x1)}" height="14" class="gauge-normal-band" />`;
    }

    let ticks = '';
    if (r.recordsRange) {
      ticks += `<line x1="${toX(r.lowest)}" x2="${toX(r.lowest)}" y1="14" y2="36" class="gauge-tick gauge-tick-low" />`;
      ticks += `<line x1="${toX(r.highest)}" x2="${toX(r.highest)}" y1="14" y2="36" class="gauge-tick gauge-tick-high" />`;
    }
    if (r.recordsValue) {
      const rx = toX(r.recorded);
      ticks += `<rect x="${rx - 1.2}" y="15" width="2.4" height="20" transform="rotate(45 ${rx} 25)" class="gauge-tick-recorded" />`;
    }

    const px = toX(r.present);
    const outOfBand = r.hasNormalBand ? !r.inNormalBand : false;
    const patternId = `hatch-${r.number}-${Math.random().toString(36).slice(2, 8)}`;
    const marker = outOfBand
      ? `<rect x="${px - 3}" y="12" width="6" height="26" class="gauge-marker gauge-marker-warn" fill="url(#${patternId})" />`
      : `<polygon points="${px},12 ${px - 4},22 ${px + 4},22" class="gauge-marker" /><line x1="${px}" y1="18" x2="${px}" y2="36" class="gauge-marker-line" />`;

    div.innerHTML = `
      <svg viewBox="0 0 100 40" class="gauge-svg" role="img" aria-label="${escapeHtml(r.description)} gauge, present value ${escapeHtml(r.presentFormatted)}">
        <defs>
          <pattern id="${patternId}" width="3" height="3" patternTransform="rotate(45)" patternUnits="userSpaceOnUse">
            <rect width="3" height="3" class="gauge-hatch-bg" />
            <line x1="0" y1="0" x2="0" y2="3" class="gauge-hatch-line" />
          </pattern>
        </defs>
        <rect x="${trackX}" y="18" width="${trackW}" height="14" class="gauge-track" />
        ${normalBand}
        ${ticks}
        ${marker}
      </svg>
      <div class="gauge-range-labels hint">
        <span>${min}</span><span>${max}${r.unitSuffix ? ' ' + r.unitSuffix : ''}</span>
      </div>
    `;
    return div;
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
    container.innerHTML = `
      <div class="panel-toolbar">
        <label>Severity: <select class="status-filter">
          <option value="advisory" ${st.filter === 'advisory' ? 'selected' : ''}>Advisory</option>
          <option value="warning" ${st.filter === 'warning' ? 'selected' : ''}>Warning</option>
          <option value="error" ${st.filter === 'error' ? 'selected' : ''}>Error</option>
        </select></label>
        <button class="btn-refresh-status">Refresh</button>
        <span class="hint status-hint">${st.loading ? 'loading…' : (st.error ? ('error: ' + escapeHtml(st.error)) : `${st.messages.length} message(s)`)}</span>
      </div>
      <table class="data-table">
        <thead><tr><th>Sub-device</th><th>Type</th><th>Message ID</th><th>Value 1</th><th>Value 2</th></tr></thead>
        <tbody>${st.messages.map(m => `
          <tr>
            <td>${m.subDevice}</td>
            <td>${escapeHtml(m.typeName)}</td>
            <td>0x${m.messageId.toString(16).toUpperCase().padStart(4, '0')}</td>
            <td>${m.value1}</td>
            <td>${m.value2}</td>
          </tr>`).join('') || '<tr><td colspan="5" class="hint">no messages at this severity</td></tr>'}
        </tbody>
      </table>
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
