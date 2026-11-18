// gdtf-devices.js — the Device inspector's "GDTF file for this device" group
// and every Devices card's GDTF tag (G3b4), built to the approved G3a mockup
// (docs/design/gdtf-library/, scenes 12–16) over GET /api/gdtf/match.
//
// THE QUESTION is asked with exactly this device's RDM identity: the
// manufacturer ID from its UID, and DEVICE_INFO's model ID, software version
// ID and current personality. It is read-only: nothing is sent to the
// device. Benny512 NEVER picks a file by name on its own: with no RDM match
// the group says so and offers "Search by name", whose results say they are
// found by name and not confirmed.
//
// THE SERVER'S WORDS are shown word for word: modeBasis and modeWarning
// (G2b) are sentences, never paraphrased. The other modes a guess could be
// (modeChoices) are listed.
//
// THE FOOTPRINT CHECK, owner round 5 #6 option C: the mode's channel count
// (GDTF Share's list for this file) against the DMX footprint the device
// reports. On a guess ("likely", "other-versions") it is always shown,
// "Footprints agree" or "Footprints differ"; on an exact match ("found") it
// is shown only when they differ, as a warning.
//
// NOT FLOODING THE SERVER (round 5 #5, a tag on every card). Lookups are
// cached in this browser by identity (manufacturer, model, software
// version, personality), so 40 identical fixtures cost one request; they
// are made one at a time from a queue, never in parallel; an answer is kept
// for 2 minutes, and dropped at once when the GDTF library or the Fixture
// Library changes ('b5-gdtf-library-changed'). Each answer is a scan of the
// in-memory index on the server; no request reaches GDTF Share.
const GdtfDevices = (() => {
  const G = GdtfCommon;
  const esc = G.esc;
  const TTL = 2 * 60 * 1000;
  const cache = new Map();   // key -> { state: 'pending'|'done'|'off'|'error', res, error, at }
  const queue = [];
  let running = false;
  const names = new Map();   // uid -> { query, results, error, searched }
  let totalFiles = null;

  // identity: the match question for a device, or why there is none.
  // mid: manufacturer ID (UID); di: DEVICE_INFO-like { model, sw, personality } or null.
  function identity(mid, di) {
    if (!di) return { why: 'no-data' };
    if (!mid || !di.model) return { why: 'zero', mid, model: di.model };
    const key = [mid, di.model, di.sw, di.personality || 0].join(':');
    return { key, mid, model: di.model, sw: di.sw, personality: di.personality || 0 };
  }

  function listeners() { window.dispatchEvent(new CustomEvent('b5-gdtf-match', { detail: null })); }

  async function pump() {
    if (running) return;
    running = true;
    while (queue.length) {
      const id = queue.shift();
      const c = cache.get(id.key);
      if (!c || c.state !== 'pending') continue;
      try {
        const res = await Api.gdtfMatch(id.mid, id.model, id.sw, id.personality || undefined);
        cache.set(id.key, { state: 'done', res, at: Date.now() });
      } catch (e) {
        cache.set(id.key, { state: e.status === 503 && /no folder/.test(e.message) ? 'off' : 'error', error: e.message, at: Date.now() });
      }
      repaintTags(id.key);
      listeners();
    }
    running = false;
  }

  function lookup(id) {
    if (!id.key) return null;
    let c = cache.get(id.key);
    if (c && c.state !== 'pending' && Date.now() - c.at > TTL) { cache.delete(id.key); c = null; }
    if (!c) {
      c = { state: 'pending', at: Date.now() };
      cache.set(id.key, c);
      queue.push(id);
      pump();
    }
    return c;
  }

  window.addEventListener('b5-gdtf-library-changed', () => { cache.clear(); totalFiles = null; listeners(); });

  // --- card tags ---------------------------------------------------------------

  function tagWords(id, c) {
    if (id.why === 'no-data') return ['unread', 'GDTF: no RDM data yet'];
    if (id.why === 'zero') return ['unread', 'GDTF: no model ID'];
    if (!c || c.state === 'pending') return ['unread', 'GDTF: checking…'];
    if (c.state === 'off') return ['unread', 'GDTF: library off'];
    if (c.state === 'error') return ['warn', 'GDTF: not checked'];
    const top = c.res.candidates[0];
    if (!top) return ['unread', 'GDTF: none'];
    switch (top.modeState) {
      case 'found': return ['ok', 'GDTF: found'];
      case 'likely': case 'other-versions': return ['warn', 'GDTF: likely'];
      case 'not-mapped': return ['warn', 'GDTF: file, no mode'];
      default: return ['info', 'GDTF: file found'];
    }
  }

  function cardIdentity(f) {
    return identity(f.manufacturerId, f.hasDeviceInfo ? { model: f.deviceModelId, sw: f.softwareVersionId, personality: f.currentPersonality } : null);
  }

  function tagSpan(id, c) {
    const [tone, words] = tagWords(id, c);
    return `<span class="b5-pill b5-pill--md b5-pill--${tone} b5-gdtf-tag" data-gdtf-tag="${esc(id.key || id.why)}" title="GDTF file for this device: see the inspector's Info tab">${esc(words)}</span>`;
  }

  // tagHtml: the card's tag, from the cache (a lookup is queued if needed).
  function tagHtml(f) {
    const id = cardIdentity(f);
    return tagSpan(id, id.key ? lookup(id) : null);
  }

  function repaintTags(key) {
    const [mid, model, sw, personality] = key.split(':').map(Number);
    const [tone, words] = tagWords({ key, mid, model, sw, personality }, cache.get(key));
    document.querySelectorAll(`[data-gdtf-tag="${key}"]`).forEach(el => {
      el.className = `b5-pill b5-pill--md b5-pill--${tone} b5-gdtf-tag`;
      el.textContent = words;
    });
  }

  // --- inspector group ------------------------------------------------------------

  const param = (label, html) => `<div class="b5-param"><span class="b5-param__label">${esc(label)}</span><span class="b5-param__value">${html}</span></div>`;
  const footOf = (c, mode) => { const m = (c.modes || []).find(x => x.name === mode); return m && m.dmxfootprint != null ? m.dmxfootprint : null; };

  function footCheck(c, devFoot, always) {
    if (devFoot == null) return always ? `<div class="b5-gdtf-match__mode">${UI.badge('unknown', 'Footprint not checked')}<span class="b5-text-sm">the device's DMX footprint is not read yet</span></div>` : '';
    const mf = footOf(c, c.dmxMode);
    if (mf == null) return always ? `<div class="b5-gdtf-match__mode">${UI.badge('unknown', 'Footprint not checked')}<span class="b5-text-sm">GDTF Share's list gives no channel count for ${esc(c.dmxMode)}</span></div>` : '';
    if (mf === devFoot) {
      return always ? `<div class="b5-gdtf-match__mode">${UI.badge('ok', 'Footprints agree')}<span class="b5-text-sm">the device reports ${G.num(devFoot)} slots; this mode has ${G.num(mf)} channels</span></div>` : '';
    }
    return `<div class="b5-gdtf-match__mode">${UI.badge('warning', 'Footprints differ')}<span class="b5-text-sm">the device reports ${G.num(devFoot)} slots; this mode has ${G.num(mf)} channels. Check the mode on the fixture's own menu.</span></div>`;
  }

  function revisionWords(c) {
    return `“${esc(c.revision)}” · GDTF ${esc(c.version || '?')}${c.lastModified ? ' · updated ' + esc(G.day(c.lastModified)) : ''}`;
  }

  function modeBlock(c, id, devFoot) {
    const p = id.personality;
    const ch = n => n == null ? 'channels not listed' : `${G.num(n)} channels`;
    let html = '';
    switch (c.modeState) {
      case 'found':
        html = `<div class="b5-gdtf-match__mode">${UI.badge('ok', 'Mode found')}<span>Personality ${p} is <strong>${esc(c.dmxMode)}</strong> · ${ch(footOf(c, c.dmxMode))}</span></div>
          ${c.modeBasis ? `<span class="b5-gdtf-sub">${esc(c.modeBasis)}</span>` : ''}`;
        if (!c.modeInFile) html += `<div class="b5-alert b5-alert--warning">${UI.icon('status-warning')}<div><p class="b5-alert__body">The file links personality ${p} to “${esc(c.dmxMode)}”, a mode it does not have.</p></div></div>`;
        html += footCheck(c, devFoot, false);
        break;
      case 'likely':
      case 'other-versions': {
        const head = c.modeState === 'likely' ? 'Likely mode — not certain' : 'Mode from other versions — not certain';
        const verb = c.modeState === 'likely' ? 'most likely' : 'probably';
        const others = (c.modeChoices || []).filter(m => m !== c.dmxMode);
        html = `<div class="b5-gdtf-match__mode">${UI.badge('warning', head)}<span>Personality ${p} is ${verb} <strong>${esc(c.dmxMode)}</strong> · ${ch(footOf(c, c.dmxMode))}</span></div>
          <div class="b5-alert b5-alert--warning">${UI.icon('status-warning')}<div>
            <p class="b5-alert__title">${esc(c.modeWarning)}</p>
            <p class="b5-alert__body"><strong>Why ${esc(c.dmxMode)}:</strong> ${esc(c.modeBasis)}</p>
          </div></div>
          ${others.length ? `<div><span class="b5-param__label">Other mode${others.length === 1 ? '' : 's'} this personality maps to in this file</span>
            <ul class="b5-gdtf-match__list">${others.map(m => `<li><strong>${esc(m)}</strong> · ${ch(footOf(c, m))}</li>`).join('')}</ul></div>` : ''}
          ${footCheck(c, devFoot, true)}`;
        break;
      }
      case 'not-mapped':
        html = `<div class="b5-gdtf-match__mode">${UI.badge('warning', 'No mode for this personality')}<span>This file maps no mode for personality ${p}.</span></div>${c.note ? `<span class="b5-gdtf-sub">${esc(c.note)}</span>` : ''}`;
        break;
      default:
        html = `<div class="b5-gdtf-match__mode">${UI.badge('unknown', 'Mode not named')}<span>The device's current personality is not known, so no mode is named.</span></div>`;
    }
    return html;
  }

  const matchedWords = c => c.matchedOn === 'software-version' ? 'manufacturer ID, model ID and this device’s software version' : 'manufacturer ID and model ID only';

  function addButton(c) {
    if (c.inFixtureLibrary) return `<span class="b5-text-sm">${UI.icon('status-ok')} Already in your Fixture library</span>`;
    return `<button type="button" class="b5-btn b5-btn--primary" data-gdtf-add-rid="${c.rid}" data-gdtf-add-name="${esc(c.fixture)}">${UI.icon('export')}Add to Fixture library…</button><span class="b5-text-sm b5-text-muted">Not in your Fixture library yet</span>`;
  }

  function candidateHtml(c, id, devFoot) {
    return `<div class="b5-gdtf-match" data-gdtf-candidate="${c.rid}">
      <div class="b5-gdtf-match__head"><strong class="b5-gdtf-result__name">${esc(c.fixture)}</strong><span class="b5-gdtf-result__mfr">${esc(c.manufacturer)}</span>${G.labelTag(c.uploaderLabel)}</div>
      <div class="b5-grid-2">
        ${param('Matched on', esc(matchedWords(c)))}
        ${param('Revision', revisionWords(c))}
      </div>
      ${c.matchedOn === 'model' && id.sw ? `<p class="b5-caption">This file does not list software version ${G.hex(id.sw, 8)}</p>` : ''}
      ${modeBlock(c, id, devFoot)}
      <div class="b5-gdtf-actions">${addButton(c)}</div>
    </div>`;
  }

  function otherWords(c) {
    const mode = c.modeState === 'found' ? `${c.dmxMode} found` : c.modeState === 'likely' || c.modeState === 'other-versions' ? `${c.dmxMode} likely, not certain` : c.modeState === 'not-mapped' ? 'no mode for this personality' : 'mode not named';
    return `<li><strong>${esc(c.fixture)}</strong> ${G.labelTag(c.uploaderLabel)} · revision “${esc(c.revision)}” · matched on ${esc(matchedWords(c))} · ${esc(mode)}. ${addButton(c)}</li>`;
  }

  function nameSearchHtml(uid, modelName, f, di) {
    const ns = names.get(uid) || { query: modelName || '', results: [], searched: false };
    names.set(uid, ns);
    let html = `<div class="b5-gdtf-search" id="ddNameSearchBox">
        <div class="b5-field"><label class="b5-field__label" for="ddNameSearch">Search the GDTF library by name instead</label><input id="ddNameSearch" class="b5-input" type="search" autocomplete="off" spellcheck="false" value="${esc(ns.query)}"></div>
        <button type="button" class="b5-btn${ns.searched ? '' : ' b5-btn--primary'}" data-gdtf-name-search>${UI.icon('filter')}Search by name</button>
      </div>
      <p class="b5-caption">${modelName ? 'Filled in from the device\'s model name. ' : ''}A file found by name is not confirmed by RDM: check its mode yourself.</p>`;
    if (ns.error) html += `<p class="b5-text-sm" role="status">${esc(ns.error)}</p>`;
    if (ns.searched && !ns.error) {
      const n = ns.results.length;
      html += `<p role="status" class="b5-text-sm" id="ddNameResults"><strong>${G.num(n)} file${n === 1 ? '' : 's'}</strong> match “${esc(ns.done)}” by name.${n ? ' Found by name, not by RDM.' : ''}</p>`;
      if (n) html += `<div class="b5-workspace-list b5-gdtf-results">${ns.results.map(r => nameRow(r, f, di)).join('')}</div>`;
    }
    return html;
  }

  function rdmWords(r, f) {
    const d = r.description;
    if (!d || d.state !== 'read') return 'This file has not been read yet, so its RDM data is not known.';
    const rdm = d.rdm;
    if (!rdm) return '<strong>No RDM data in this file.</strong> It can only be found by name.';
    const m = rdm.manufacturerId != null ? G.hex(rdm.manufacturerId, 4) : null;
    if (rdm.deviceModelId == null) {
      const said = rdm.deviceModelIdText ? ` (it says ${esc(rdm.deviceModelIdText)})` : '';
      return `RDM IDs in this file: ${m ? `manufacturer ${m}, but ` : ''}<strong>the file gives no model ID</strong>${said}, so it cannot match a device by RDM.`;
    }
    if (rdm.manufacturerId == null) return `RDM IDs in this file: model ${G.hex(rdm.deviceModelId, 4)}, but <strong>the file gives no manufacturer ID</strong>, so it cannot match a device by RDM.`;
    return `RDM IDs in this file: manufacturer ${m}, model ${G.hex(rdm.deviceModelId, 4)} — not this device's (${G.hex(f.manufacturerId, 4)}, ${G.hex(f.deviceModelId || 0, 4)}).`;
  }

  function nameRow(r, f, di) {
    let fit = '';
    if (di && di.DMXFootprint != null) {
      const same = (r.modes || []).filter(m => m.dmxfootprint === di.DMXFootprint);
      fit = same.length
        ? `The device's personality ${di.CurrentPersonality} (${G.num(di.DMXFootprint)} slots): this file has a mode of ${G.num(di.DMXFootprint)} channels named ${esc(same.map(m => m.name).join(', '))}. Not confirmed.`
        : `No mode in this file has the ${G.num(di.DMXFootprint)} slots the device reports.`;
    }
    return `<div class="b5-workspace-item b5-gdtf-result" data-gdtf-name-rid="${r.rid}">
      <div class="b5-gdtf-result__head"><strong class="b5-gdtf-result__name">${esc(r.fixture)}</strong><span class="b5-gdtf-result__mfr">${esc(r.manufacturer)}</span>${G.labelTag(r.uploaderLabel)}${UI.badge('pending', 'Found by name')}</div>
      <span class="b5-text-sm">Revision “${esc(r.revision)}”</span>
      <span class="b5-text-sm">${rdmWords(r, f)}</span>
      <span class="b5-gdtf-result__modes">Modes: ${esc(G.modesText(r.modes))}</span>
      ${fit ? `<span class="b5-text-sm">${fit}</span>` : ''}
      <div class="b5-row">${r.inFixtureLibrary ? `<span class="b5-text-sm">${UI.icon('status-ok')} Already in your Fixture library</span>` : `<button type="button" class="b5-btn b5-btn--sm" data-gdtf-add-rid="${r.rid}" data-gdtf-add-name="${esc(r.fixture)}">${UI.icon('export')}Add to Fixture library…</button>`}</div>
    </div>`;
  }

  // infoGroupHtml: the "GDTF file for this device" group. f: the Devices
  // list entry; di: DEVICE_INFO as the Info tab read it (null when not
  // read); modelName: the device's own model description, for the name
  // search.
  function infoGroupHtml(f, di, modelName, persDesc) {
    const id = identity(f.manufacturerId, di ? { model: di.DeviceModelID, sw: di.SoftwareVersionID, personality: di.CurrentPersonality } : null);
    const ids = di ? `manufacturer ${G.hex(f.manufacturerId, 4)}, model ${G.hex(di.DeviceModelID, 4)}, software version ${G.hex(di.SoftwareVersionID, 8)}${di.CurrentPersonality ? `, personality ${di.CurrentPersonality}` : ''}` : '';
    let body = '';
    const nameSearch = () => nameSearchHtml(f.uid, modelName, f, di);
    if (id.why === 'no-data') {
      body = `<div class="b5-alert b5-alert--info" role="status">${UI.icon('status-pending')}<div><p class="b5-alert__title">No RDM data from this device yet</p>
        <p class="b5-alert__body">It has not answered DEVICE_INFO, so its model ID is not known and no GDTF file can be looked up by RDM.</p></div></div>${nameSearch()}`;
    } else if (id.why === 'zero') {
      body = `<div class="b5-alert b5-alert--info" role="status">${UI.icon('status-pending')}<div><p class="b5-alert__title">This device reports ${!f.manufacturerId ? 'manufacturer ID 0x0000' : `model ID ${G.hex(di.DeviceModelID, 4)}`}</p>
        <p class="b5-alert__body">In GDTF an ID of 0 means "not given", so no file can match it by RDM. Benny512 does not pick a file by its name on its own.</p></div></div>${nameSearch()}`;
    } else {
      const c = lookup(id);
      if (c.state === 'pending') body = `<span class="b5-inline-wait">${UI.spinner()}Looking it up…</span>`;
      else if (c.state === 'off') body = `<div class="b5-alert b5-alert--info">${UI.icon('status-pending')}<div><p class="b5-alert__title">The GDTF library is off in this mode</p><p class="b5-alert__body">${esc(c.error)}.</p></div></div>`;
      else if (c.state === 'error') body = `<p class="b5-text-sm" role="status">The GDTF library could not be asked: ${esc(c.error)}</p>`;
      else {
        const res = c.res;
        const devFoot = di ? di.DMXFootprint : null;
        if (!res.candidates.length) {
          if (totalFiles == null) noteFiles();
          const skipped = [];
          if (res.unreadable) skipped.push(`${G.num(res.unreadable)} file${res.unreadable === 1 ? '' : 's'} could not be read`);
          if (res.notYetRead) skipped.push(`${G.num(res.notYetRead)} file${res.notYetRead === 1 ? '' : 's'} not read yet`);
          const files = totalFiles == null ? 'the files' : `the ${G.num(totalFiles)} files`;
          body = `<div class="b5-alert b5-alert--info" role="status">${UI.icon('status-pending')}<div>
              <p class="b5-alert__title">No GDTF file in your library has this device's RDM IDs</p>
              <p class="b5-alert__body">None of ${files} lists manufacturer ${G.hex(id.mid, 4)} with model ${G.hex(id.model, 4)}. Benny512 does not pick a file by its name on its own.${skipped.length ? ` ${esc(skipped.join(' and '))}, so ${skipped.length > 1 || res.unreadable + res.notYetRead > 1 ? 'they were' : 'it was'} not checked.` : ''}</p></div></div>
            ${nameSearch()}`;
        } else {
          const [top, ...rest] = res.candidates;
          body = candidateHtml(top, id, devFoot);
          if (rest.length) body += `<details class="b5-gdtf-also"><summary>${G.num(rest.length)} more file${rest.length === 1 ? ' matches' : 's match'}</summary><ul class="b5-gdtf-match__list">${rest.map(otherWords).join('')}</ul><p class="b5-caption">Listed after the first: files uploaded by the manufacturer come before user uploads, then the newest.</p></details>`;
        }
      }
    }
    return `<section class="b5-group" aria-labelledby="ddGdtfHead" data-gdtf-device="${esc(f.uid)}">
      <h4 class="b5-group__head" id="ddGdtfHead">GDTF file for this device</h4>
      ${ids ? `<p class="b5-caption">Looked up in the GDTF library on this computer by the RDM IDs above: ${ids}. Nothing is sent to the device.</p>` : ''}
      ${body}
    </section>`;
  }

  // wire: the group's buttons. Called after the Info tab paints.
  function wire(container, f, di, rerender) {
    const box = container.querySelector('[data-gdtf-device]');
    if (!box) return;
    const btn = box.querySelector('[data-gdtf-name-search]'), input = box.querySelector('#ddNameSearch');
    const run = async () => {
      const ns = names.get(f.uid) || {};
      ns.query = input.value; ns.error = '';
      names.set(f.uid, ns);
      try {
        const res = await Api.gdtfSearch(ns.query, 'library', 50);
        ns.results = res.results || []; ns.searched = true; ns.done = ns.query;
      } catch (e) { ns.error = `Search failed: ${e.message}`; ns.searched = true; }
      rerender();
      const again = document.getElementById('ddNameResults') || document.getElementById('ddNameSearch');
      if (again && again.focus && again.id === 'ddNameSearch') again.focus();
    };
    if (btn) btn.addEventListener('click', run);
    if (input) {
      input.addEventListener('input', () => { const ns = names.get(f.uid) || {}; ns.query = input.value; names.set(f.uid, ns); });
      input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); run(); } });
    }
    box.querySelectorAll('[data-gdtf-add-rid]').forEach(b => b.addEventListener('click', () => {
      if (typeof LibraryPanel !== 'undefined' && LibraryPanel.openGdtfImport) LibraryPanel.openGdtfImport(Number(b.dataset.gdtfAddRid), b.dataset.gdtfAddName);
    }));
  }

  // noteFiles: the library's file count for "None of the N files…", read
  // once (and again after a library change); until then the words say "the
  // files" rather than a number.
  let countAsked = false;
  async function noteFiles() {
    if (totalFiles != null || countAsked) return;
    countAsked = true;
    try { const s = await Api.gdtfStatus(); totalFiles = s.enabled ? s.files : null; if (totalFiles != null) listeners(); } catch (e) { /* the count is optional words */ }
    countAsked = false;
  }

  return { tagHtml, infoGroupHtml, wire, noteFiles, _cache: cache };
})();
