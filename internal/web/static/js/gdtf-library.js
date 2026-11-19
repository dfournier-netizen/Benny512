// gdtf-library.js — Fixture library › Add from GDTF library (G3b3), built to
// the approved G3a mockup (docs/design/gdtf-library/, scenes 9–11).
//
// TWO SOURCES:
//  - "On this computer": GET /api/gdtf/search?in=library — the GDTF files
//    already downloaded, searched by name, long name and manufacturer.
//    Works offline.
//  - "GDTF Share (online)": GET /api/gdtf/search?in=share — the whole of
//    GDTF Share from the list fetched at the last sync; a file not on this
//    computer is downloaded once by POST /api/gdtf/pull (a single pull, also
//    for a manufacturer nobody follows), never over the lighting network.
//
// ONE IMPORT PATH. Picking a file goes through the EXISTING Fixture Library
// import (plan, "How it fits what exists"): the browser fetches the bytes
// from GET /api/gdtf/file/{rid}, MvrImport.parseGdtfFile (gdtfparse.js)
// reads them, MvrImport.libraryDocFromGdtf builds the record — carrying the
// file's Manufacturer/User label (owner round 2 #2) — and library.js shows
// its existing confirm step ("Review the import, then apply"), whose Apply
// sends POST /api/library/import. There is no second parser and no second
// store.
//
// THE 20 MB CAP (owner round 5 #1). A file over the Fixture Library's cap
// (fixtureLibraryMaxBytes from GET /api/gdtf/status) is refused on its row
// before anyone tries, in words, and again by the downloaded byte count.
//
// Every row says "Manufacturer file" / "User file" in words.
const GdtfLibraryPicker = (() => {
  const G = GdtfCommon;
  const esc = G.esc;
  const st = { source: 'library', query: '', results: [], count: 0, searched: false, error: '', status: null, statusError: '', busyRid: 0 };
  let host = null, hooks = null, debounce = null, seq = 0;
  const LIMIT = 200;

  const maxBytes = () => (st.status && st.status.fixtureLibraryMaxBytes) || 20 * 1024 * 1024;
  const followedShare = () => new Set(((st.status && st.status.manufacturers) || []).filter(m => m.matched).map(m => String(m.shareName || '').toLowerCase()));

  function tooBig(r) { return r.filesize > maxBytes(); }

  function refusalHtml(name, bytes) {
    return `<div class="b5-alert b5-alert--warning b5-gdtf-refusal">${UI.icon('status-warning')}<div>
      <p class="b5-alert__title">Too large for the Fixture Library: ${esc(G.mb(bytes))}</p>
      <p class="b5-alert__body">The Fixture Library takes GDTF files up to ${esc(G.mb(maxBytes()).replace('.0 MB', ' MB'))}, so ${esc(name)} cannot be imported. It stays in the GDTF library and is not lost.</p></div></div>`;
  }

  function row(r) {
    const share = st.source === 'share';
    const d = r.description && r.description.state === 'read' ? r.description : null;
    const longName = d && d.longName ? `<span class="b5-text-sm">Long name: ${esc(d.longName)}</span>`
      : share && !r.inLibrary ? '<span class="b5-text-sm b5-text-muted">Long name: known once downloaded</span>' : '';
    const unfollowed = share && !r.inLibrary && !followedShare().has(String(r.manufacturer || '').toLowerCase());
    const big = tooBig(r);
    const who = `${r.fixture} (${G.labelWords(r.uploaderLabel)}, revision ${r.revision})`;
    let action = '';
    if (!big) {
      const words = share && !r.inLibrary ? 'Download and import…' : 'Import…';
      action = `<div class="b5-row"><button type="button" class="b5-btn b5-btn--sm" data-gdtf-import="${r.rid}" aria-label="${esc(words.replace('…', '') + ' ' + who)}" ${st.busyRid ? 'disabled' : ''}>${UI.icon('export')}${words}</button></div>`;
    }
    return `<div class="b5-workspace-item b5-gdtf-result" data-gdtf-rid="${r.rid}">
      <div class="b5-gdtf-result__head"><strong class="b5-gdtf-result__name">${esc(r.fixture)}</strong><span class="b5-gdtf-result__mfr">${esc(r.manufacturer)}</span>${G.labelTag(r.uploaderLabel)}${share ? (r.inLibrary ? UI.badge('ok', 'On this computer') : UI.badge('pending', 'Not downloaded')) : ''}${r.inFixtureLibrary ? UI.tag('In your Fixture library', 'info') : ''}</div>
      ${longName}
      <span class="b5-text-sm">Revision “${esc(r.revision)}” · GDTF ${esc(r.version || '?')} · ${esc(G.mb(r.filesize))}${r.lastModified ? ' · updated ' + esc(G.day(r.lastModified)) : ''}</span>
      <span class="b5-gdtf-result__modes">Modes: ${esc(G.modesText(r.modes))}</span>
      ${unfollowed ? `<span class="b5-text-sm">${UI.icon('status-pending')} ${esc(r.manufacturer)} is not followed: this downloads this one file only.</span>` : ''}
      ${big ? refusalHtml(r.fixture, r.filesize) : ''}
      ${action}
    </div>`;
  }

  function countWords() {
    if (st.error) return st.error;
    if (!st.searched) return '';
    const where = st.source === 'share' ? ' on GDTF Share' : '';
    const q = st.query.trim();
    const n = st.count;
    let s = `${G.num(n)} file${n === 1 ? '' : 's'}${where} ${q ? `match “${q}”` : 'listed'}`;
    if (n === 1 && q) s = `1 file${where} matches “${q}”`;
    if (n >= LIMIT) s += ` (the first ${LIMIT}; type more of the name to narrow it)`;
    return s;
  }

  function paint() {
    if (!host) return;
    const s = st.status;
    const share = st.source === 'share';
    const files = s ? G.num(s.files) : '…';
    let off = '';
    if (st.statusError) off = `<p class="b5-text-sm" role="status">The GDTF library status could not be read: ${esc(st.statusError)}</p>`;
    else if (s && !s.enabled) off = `<div class="b5-alert b5-alert--info">${UI.icon('status-pending')}<div><p class="b5-alert__title">The GDTF library is off in this mode</p><p class="b5-alert__body">${esc(s.message)}. A demo or rehearsal run has no GDTF library.</p></div></div>`;
    const keepFocus = document.activeElement && host.contains(document.activeElement) ? document.activeElement.id || '' : '';
    const caret = keepFocus === 'gdtfQuery' ? document.activeElement.selectionStart : null;
    host.innerHTML = `<h3 id="gdtfAddHead">Add from GDTF library</h3>
      <div class="b5-gdtf-lib-source b5-segmented b5-segmented--sm" role="group" aria-label="Search in">
        <button type="button" class="b5-seg${share ? '' : ' is-on'}" aria-pressed="${!share}" data-gdtf-source="library">${share ? '' : UI.icon('status-ok')}On this computer (${files} files)</button>
        <button type="button" class="b5-seg${share ? ' is-on' : ''}" aria-pressed="${share}" data-gdtf-source="share">${share ? UI.icon('status-ok') : ''}GDTF Share (online)</button>
      </div>
      ${share
        ? `<p class="b5-caption">Searches everything on GDTF Share, from the list fetched at the last sync${s && s.lastSync ? ` (${esc(G.when(s.lastSync))})` : ' (none yet: sync once first)'}, by name and manufacturer. Downloading needs the internet and never uses the lighting network. A fixture from a manufacturer you do not follow is downloaded once, not kept up to date.</p>`
        : '<p class="b5-caption">Searches the GDTF files already on this computer by name, long name and manufacturer. Works offline. Every result says whether its manufacturer or a user uploaded it.</p>'}
      ${off}
      <div class="b5-gdtf-search"><div class="b5-field"><label class="b5-field__label" for="gdtfQuery">Search name${share ? '' : ', long name'} or manufacturer</label><input id="gdtfQuery" class="b5-input" type="search" autocomplete="off" spellcheck="false" value="${esc(st.query)}"></div></div>
      <p class="b5-text-sm" role="status" data-gdtf-count>${esc(countWords())}</p>
      <div class="b5-workspace-list b5-gdtf-results" data-gdtf-results>${st.results.map(row).join('')}</div>`;
    host.querySelectorAll('[data-gdtf-source]').forEach(b => b.addEventListener('click', () => {
      if (st.source === b.dataset.gdtfSource) return;
      st.source = b.dataset.gdtfSource; st.results = []; st.searched = false;
      paint(); search();
      const again = host.querySelector(`[data-gdtf-source="${st.source}"]`); if (again) again.focus();
    }));
    const input = host.querySelector('#gdtfQuery');
    input.addEventListener('input', () => { st.query = input.value; clearTimeout(debounce); debounce = setTimeout(search, 250); });
    input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); clearTimeout(debounce); search(); } });
    host.querySelectorAll('[data-gdtf-import]').forEach(b => b.addEventListener('click', () => pick(Number(b.dataset.gdtfImport))));
    if (keepFocus) {
      const f = host.querySelector('#' + keepFocus);
      if (f) { f.focus({ preventScroll: true }); if (caret !== null && f.setSelectionRange) f.setSelectionRange(caret, caret); }
    }
  }

  async function search() {
    const my = ++seq;
    if (st.status && !st.status.enabled) { st.results = []; st.searched = false; paint(); return; }
    try {
      const res = await Api.gdtfSearch(st.query, st.source, LIMIT);
      if (my !== seq) return;
      st.results = res.results || []; st.count = res.count; st.error = ''; st.searched = true;
    } catch (e) {
      if (my !== seq) return;
      st.results = []; st.error = `Search failed: ${e.message}`; st.searched = true;
    }
    paint();
  }

  // fileName: the server's own .gdtf name from Content-Disposition, else
  // Manufacturer@Fixture@Revision.gdtf (the same rule, gdtf.go gdtfFileName).
  function fileName(disposition, r) {
    const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(disposition || '');
    if (m) { try { return decodeURIComponent(m[1]); } catch (e) { return m[1]; } }
    const clean = s => String(s || '').replace(/[\\/:*?"<>|@\u0000-\u001f]/g, '_').trim().slice(0, 60);
    return `${clean(r.manufacturer)}@${clean(r.fixture)}@${clean(r.revision)}.gdtf`;
  }

  async function pick(rid) {
    const r = st.results.find(x => x.rid === rid);
    if (!r || st.busyRid) return;
    st.busyRid = rid;
    paint();
    const say = m => hooks && hooks.status(m);
    try {
      let pulled = false;
      if (st.source === 'share' && !r.inLibrary) {
        say(`Downloading ${r.fixture} from GDTF Share…`);
        await Api.gdtfPull(rid);
        pulled = true;
      }
      say(`Reading ${r.fixture}…`);
      const f = await Api.gdtfFileBytes(rid);
      if (f.bytes.byteLength > maxBytes()) {
        say(`Not imported: ${r.fixture} is ${G.mb(f.bytes.byteLength)}, and the Fixture Library takes GDTF files up to ${G.mb(maxBytes()).replace('.0 MB', ' MB')}.`);
        return;
      }
      const label = f.uploader || r.uploaderLabel;
      const name = fileName(f.disposition, r);
      const parsed = await MvrImport.parseGdtfFile(f.bytes);
      hooks.propose({ doc: MvrImport.libraryDocFromGdtf(parsed, name, f.bytes, { uploaderLabel: label }), name, warnings: parsed.warnings || [], gdtfLabel: label, pulled });
    } catch (e) {
      say(`Not imported: ${r.fixture}: ${e.message}`);
    } finally {
      st.busyRid = 0;
      if (st.source === 'share') await search(); else paint();
    }
  }

  // mount: draw into container (library.js's section) and search with the
  // remembered source and words. hooks.propose(pending) hands a built
  // import to library.js's confirm step; hooks.status(text) uses its status
  // line.
  let mounted = Promise.resolve();
  async function mount(container, h) {
    host = container; hooks = h;
    paint();
    mounted = (async () => {
      try { st.status = await Api.gdtfStatus(); st.statusError = ''; } catch (e) { st.statusError = e.message; }
      if (host !== container) return;
      paint();
      await search();
    })();
    return mounted;
  }

  // importFrom: search this computer's files for name and start the import
  // of rid (the Device inspector's "Add to Fixture library…").
  async function importFrom(rid, name) {
    await mounted;
    st.source = 'library'; st.query = name || '';
    await search();
    if (st.results.some(r => r.rid === rid)) await pick(rid);
    else if (hooks) hooks.status(`Not found on this computer: ${name}. Search for it below.`);
  }

  return { mount, importFrom };
})();
