// G3a mockup scenes. Made-up data in the shapes the G1/G2/G2b API returns
// (internal/web/gdtf.go, internal/gdtfshare/types.go, match.go). Anything
// the API does not give today is drawn with a NEW data note (g3-new) naming
// the field it would need. Nothing here talks to a server.
(() => {
  'use strict';
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const icon = n => `<svg aria-hidden="true"><use href="#b5-icon-${n}"/></svg>`;
  const STATUS_ICON = { ok: 'status-ok', warning: 'status-warning', error: 'status-error', pending: 'status-pending' };
  const badge = (st, text) => `<span class="b5-badge b5-badge--${st}">${STATUS_ICON[st] ? icon(STATUS_ICON[st]) : ''}${esc(text)}</span>`;
  const tag = (text, v) => `<span class="b5-tag${v ? ' b5-tag--' + v : ''}">${esc(text)}</span>`;
  const NEW = (field, why) => `<span class="g3-new" title="${esc(why || 'Not in the API today')}">NEW · ${esc(field)}</span>`;
  const n = x => x.toLocaleString('en-US');
  const labelTag = l => l === 'Manufacturer' ? tag('Manufacturer file', 'info') : l === 'User' ? tag('User file') : tag('Uploader unknown');
  const param = (label, html) => `<div class="b5-param"><span class="b5-param__label">${esc(label)}</span><span class="b5-param__value">${html}</span></div>`;

  // ---- made-up library state (gdtfStatusResponse shape) -----------------------
  const FOLLOWED = [
    // name, matched, shareName, manuf, user, local, result
    ['Ayrton', true, 'Ayrton', 96, 11, 107],
    ['Martin', false, '', 0, 0, 0],
    ['Elation', true, 'Elation Professional', 241, 37, 278],
    ['Robe', true, 'Robe Lighting', 212, 25, 237],
    ['Chauvet', true, 'Chauvet Professional', 187, 64, 251],
    ['GLP', true, 'GLP', 58, 9, 67],
    ['Volux', true, 'Volux', 12, 0, 12],
    ['ADJ', true, 'ADJ', 141, 22, 163],
    ['ACME', true, 'ACME', 77, 6, 83],
    ['SGM', true, 'SGM', 64, 7, 71],
    ['Astera', true, 'Astera', 31, 2, 33],
    ['ChromaQ', false, '', 0, 0, 0],
    ['ETC', true, 'ETC', 118, 19, 137],
    ['High End Systems', true, 'High End Systems', 52, 14, 66],
  ];
  const LAST_SYNC = 'Tue 17 Nov 2026, 21:40';
  const FILES = 1505;

  // ---- app chrome --------------------------------------------------------------
  function setNav(tab) {
    document.querySelectorAll('#tabs .b5-nav__item').forEach(b => {
      const on = b.dataset.tab === tab;
      b.classList.toggle('is-active', on);
      if (on) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
    });
    document.querySelectorAll('.screen').forEach(s => s.classList.toggle('active', s.id === 'screen-' + tab));
  }

  // ---- Settings › GDTF Share ---------------------------------------------------
  function statusBlock(s) {
    const st = {
      'no-login': [badge('pending', 'Not set up'), 'No GDTF Share login saved', 'Nothing has been downloaded yet. Enter your login below, then Save login: the first sync starts on its own.'],
      ok: [badge('ok', 'Up to date'), `Library up to date: ${n(FILES)} file(s) for the followed manufacturers`, `Last synced ${LAST_SYNC} · ${n(FILES)} files on this computer · syncs at start-up and every 120 minutes while Benny512 runs`],
      syncing: [badge('pending', 'Syncing'), 'Syncing with GDTF Share', ''],
      offline: [`<span class="b5-badge b5-badge--stale">${icon('lease-lost')}Offline</span>`, `offline · last synced ${LAST_SYNC}`, `GDTF Share could not be reached. The ${n(FILES)} files on this computer are used as they are. Benny512 tries again in 120 minutes, or press Sync now.`],
      refused: [badge('warning', 'Sync refused'), 'the only route to the internet is the lighting network adapter', `Nothing was downloaded. The ${n(FILES)} files on this computer are used as they are. Last synced ${LAST_SYNC}.`],
      partial: [badge('warning', 'Partly synced'), 'Synced with 3 file(s) still to download; the next sync retries them', `Last synced ${LAST_SYNC} · ${n(FILES)} files on this computer`],
    }[s.state];
    let extra = '';
    if (s.state === 'syncing') {
      extra = `<div class="g3-progress">
          <span>File <strong>412</strong> of <strong>1,505</strong> · now Robe Lighting (manufacturer 4 of 12)${NEW('progress.manufacturer', 'Status.progress has done/total files only; naming the manufacturer and "n of m" needs a field')}</span>
          <div class="b5-progress" role="progressbar" aria-label="Sync progress" aria-valuemin="0" aria-valuemax="1505" aria-valuenow="412" aria-valuetext="File 412 of 1,505"><div class="b5-progress__fill" style="width:27.4%"></div></div>
          <span class="g3-status__meta" style="flex-basis:auto">The library stays usable while it syncs: search, import and device matching work on the files already here. Downloads go one at a time, a second apart.</span>
        </div>`;
    }
    if (s.state !== 'no-login') extra += `<span class="g3-status__meta">${icon('status-warning')} <strong>2 followed names matched nothing on GDTF Share</strong> (Martin, ChromaQ): see the list below.</span>`;
    return `<div class="g3-status" role="status" aria-label="GDTF Share status">${st[0]}<span class="g3-status__msg">${esc(st[1])}</span>${st[2] ? `<span class="g3-status__meta">${esc(st[2])}</span>` : ''}${extra}</div>`;
  }

  function routeInset(s) {
    const auto = s.state === 'refused';
    const lighting = auto
      ? `Ethernet (192.168.1.40) — picked automatically, because <em>Art-Net network adapter</em> is set to “(default / any)”`
      : `Ethernet 2 (2.0.0.10) — set in Settings › 1 Network`;
    return `<div class="b5-inset">
      <p class="b5-inset__head">${icon('network-node')}Which network sync uses</p>
      <p class="b5-note"><strong>Lighting network: sync never uses it.</strong> Art-Net: ${lighting}. sACN: same adapter as Art-Net.${NEW('lightingAdapters', 'The adapters sync refuses are known to the server (gdtfLightingNetworks, sacnAdapter) but not in /api/gdtf/status')}</p>
      <p class="b5-note"><strong>Internet:</strong> ${auto ? 'the only route this computer has to gdtf-share.com is the same Ethernet (192.168.1.40) adapter, so sync is refused.' : 'any other adapter with a route to gdtf-share.com. Last sync went out by Wi-Fi (192.168.1.22).'}${NEW('internetRoute', 'The dialer checks the route; reporting which adapter it took needs a field')}</p>
      <p class="b5-note">Sync may run while ARMED. If the only way to the internet is the lighting adapter, Benny512 refuses to sync and says so here; it never sends GDTF Share traffic over the lighting network.</p>
    </div>`;
  }

  function refusalAlert() {
    return `<div class="b5-alert b5-alert--warning" role="alert">${icon('status-warning')}<div>
      <p class="b5-alert__title">Sync refused: the only route to the internet is the lighting network adapter</p>
      <p class="b5-alert__body">Art-Net goes out on Ethernet (192.168.1.40), and that card is also this computer's only way to the internet. Sync never uses the lighting network, so nothing was downloaded. This is on purpose, not a fault.</p>
      <p class="b5-alert__body"><strong>To sync:</strong> in <strong>Settings › 1 Network › Art-Net network adapter</strong>, choose the card that goes to the lighting network instead of “(default / any)”, then restart Benny512. Or give this computer another way to the internet, such as Wi-Fi.</p>
      <div class="g3-actions" style="margin-top:var(--b5-space-3)"><button type="button" class="b5-btn" data-goto="nicSelect">${icon('network-node')}Go to Art-Net network adapter</button></div>
    </div></div>`;
  }

  function loginGroup(s) {
    if (s.state === 'no-login') {
      return `<div class="b5-group"><h3 class="b5-group__head">Login</h3>
        <p class="b5-caption">Your own GDTF Share account (free at gdtf-share.com). It is saved on this computer only, for this Windows user, with the password encrypted. It never goes into show files, library exports, logs or captures, and each Benny512 install needs its own login.</p>
        <div class="g3-fields">
          <div class="b5-field"><label class="b5-field__label" for="gdtfUser">GDTF Share user name</label><input id="gdtfUser" class="b5-input" type="text" autocomplete="username" spellcheck="false"></div>
          <div class="b5-field"><label class="b5-field__label" for="gdtfPass">Password</label><input id="gdtfPass" class="b5-input" type="password" autocomplete="current-password"></div>
        </div>
        <div class="g3-actions"><button type="button" class="b5-btn">${icon('signal')}Test login</button><button type="button" class="b5-btn b5-btn--primary">${icon('apply')}Save login</button>
        <span class="b5-text-muted b5-text-sm" role="status">Test login checks it with GDTF Share without saving it.</span></div>
      </div>`;
    }
    return `<div class="b5-group"><h3 class="b5-group__head">Login</h3>
      <p class="g3-login">${badge('ok', 'Saved')}<span>Saved · user <strong>dfournier</strong></span><span class="b5-text-muted b5-text-sm">password encrypted on this computer, never shown</span></p>
      <div class="g3-actions"><button type="button" class="b5-btn">${icon('signal')}Test login</button><button type="button" class="b5-btn">Change login…</button><button type="button" class="b5-btn b5-btn--ghost">${icon('close')}Remove login</button></div>
    </div>`;
  }

  function syncGroup(s) {
    const pending = s.state === 'partial' ? 3 : 0, unreadable = s.state === 'partial' ? 1 : 0;
    const files = s.state === 'no-login' ? 0 : FILES;
    const last = s.state === 'no-login' ? '<span class="b5-text-muted">never</span>' : esc(LAST_SYNC);
    const busy = s.state === 'syncing';
    const canSync = s.state !== 'no-login' && !busy;
    let why = '';
    if (s.state === 'no-login') why = 'Save a login first.';
    else if (busy) why = 'A sync is running.';
    return `<div class="b5-group"><h3 class="b5-group__head">Sync</h3>
      <div class="b5-grid-2">
        ${param('Last synced', last)}
        ${param('Files on this computer', n(files))}
        ${param('Waiting to download', pending ? `<strong>${pending}</strong> — retried at the next sync` : '0')}
        ${param('Files that could not be read', unreadable ? `<strong>${unreadable}</strong> — kept, see below` : '0')}
      </div>
      <div class="g3-actions"><button type="button" class="b5-btn b5-btn--primary" ${canSync ? '' : 'aria-disabled="true"'}>${icon('refresh')}Sync now</button>${why ? `<span class="b5-text-muted b5-text-sm">${esc(why)}</span>` : ''}</div>
      <p class="b5-caption">Benny512 syncs at start-up and every 120 minutes while it runs. It keeps the newest revision of each fixture only; fixtures already in your Fixture Library or a show keep their own copy.</p>
    </div>`;
  }

  function manufacturerRows(s) {
    return FOLLOWED.map(([name, matched, share, mf, us, local]) => {
      if (s.state === 'no-login') {
        return `<tr><th scope="row">${esc(name)}</th><td colspan="3">${badge('pending', 'Not checked yet')}<span class="g3-sub">The first sync looks it up.</span></td><td><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Stop following ${esc(name)}">${icon('close')}Stop following</button></td></tr>`;
      }
      if (!matched) {
        const similar = name === 'Martin' ? ['Martin Professional'] : ['Chroma-Q'];
        const showSimilar = true;
        return `<tr id="mfr${esc(name)}"><th scope="row">${esc(name)}</th><td>—</td><td>0</td>
          <td>${badge('warning', 'Not found on GDTF Share')}<span class="g3-sub">No manufacturer named “${esc(name)}” on GDTF Share; nothing was downloaded for it.</span>
          ${showSimilar ? `<div class="g3-similar"><span class="b5-text-sm">Similar names on GDTF Share:</span>${similar.map(x => `<button type="button" class="b5-btn b5-btn--sm">Follow “${esc(x)}” instead</button>`).join('')}</div>` : ''}</td>
          <td><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Stop following ${esc(name)}">${icon('close')}Stop following</button></td></tr>`;
      }
      let result = badge('ok', 'Up to date');
      if (s.state === 'partial' && name === 'Chauvet') result = badge('warning', '2 waiting to download');
      if (s.state === 'partial' && name === 'Robe') result = badge('warning', '1 waiting to download');
      if (s.state === 'syncing' && ['ETC', 'High End Systems', 'Ayrton', 'Elation', 'Robe'].includes(name)) result = name === 'Robe' ? badge('pending', 'Syncing now') : badge('ok', 'Up to date');
      const checked = s.state === 'offline' || s.state === 'refused' ? `checked ${LAST_SYNC}` : `checked ${LAST_SYNC}`;
      return `<tr><th scope="row">${esc(name)}${share !== name ? `<span class="g3-sub">on GDTF Share as “${esc(share)}”</span>` : ''}</th>
        <td>${n(mf + us)}<span class="g3-sub">${n(mf)} manufacturer, ${n(us)} user</span></td>
        <td>${n(local)}</td>
        <td>${result}<span class="g3-sub">${esc(checked)}</span></td>
        <td><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost" aria-label="Stop following ${esc(name)}">${icon('close')}Stop following</button></td></tr>`;
    }).join('');
  }

  function manufacturerGroup(s) {
    return `<div class="b5-group"><h3 class="b5-group__head" id="mfrHead">Manufacturers you follow (${FOLLOWED.length})</h3>
      <p class="b5-caption">Every file GDTF Share has for these manufacturers is kept on this computer: files uploaded by the manufacturer and by users, each labelled. For one fixture from anyone else, use <strong>Show › Fixture library › Add from GDTF library</strong>.</p>
      <div class="g3-table-wrap"><table class="b5-table">
        <caption class="b5-visually-hidden">Followed manufacturers and their last sync result</caption>
        <thead><tr><th scope="col">Manufacturer</th><th scope="col">On GDTF Share</th><th scope="col">On this computer</th><th scope="col">Last sync</th><th scope="col"><span class="b5-visually-hidden">Actions</span></th></tr></thead>
        <tbody>${manufacturerRows(s)}</tbody>
      </table></div>
      <div class="g3-addrow">
        <div class="b5-field"><label class="b5-field__label" for="gdtfAddMfr">Follow another manufacturer</label><input id="gdtfAddMfr" class="b5-input" type="text" placeholder="name as GDTF Share spells it" spellcheck="false"></div>
        <button type="button" class="b5-btn">${icon('apply')}Follow</button>
      </div>
      <p class="b5-caption">A new name is looked up at the next sync. A name GDTF Share does not know is reported here with the closest names it has, never dropped and never swapped for you. Stop following keeps the files already downloaded; they are no longer refreshed.</p>
    </div>`;
  }

  function problemsGroup() {
    const pend = [
      ['Maverick Storm 2 Profile', 'Chauvet Professional', 'DMX v1.4', 'Manufacturer', 'GDTF Share download of rid 48211 answered HTTP 503', 2, 'Tue 17 Nov 2026, 19:40'],
      ['Rogue R3X Wash', 'Chauvet Professional', 'Rev 7', 'User', 'GDTF Share sent rid 47102 as an empty or truncated file', 1, 'Tue 17 Nov 2026, 21:40'],
      ['Painte', 'Robe Lighting', 'v1.2 (fw 1.6)', 'Manufacturer', 'GDTF Share could not be reached: connection reset during the download', 1, 'Tue 17 Nov 2026, 21:40'],
    ];
    return `<div class="b5-group"><h3 class="b5-group__head" id="pendHead">Waiting to download (3)</h3>
      <p class="b5-caption">One file that fails never stops the others. Each stays listed here and is tried again at the next sync.</p>
      <div class="g3-table-wrap"><table class="b5-table">
        <caption class="b5-visually-hidden">Downloads waiting for the next sync</caption>
        <thead><tr><th scope="col">Fixture</th><th scope="col">File from</th><th scope="col">Why it is waiting</th><th scope="col">Tries</th></tr></thead>
        <tbody>${pend.map(([f, m, r, l, e, a, since]) => `<tr><th scope="row">${esc(f)}<span class="g3-sub">${esc(m)} · revision “${esc(r)}”</span></th><td>${labelTag(l)}</td><td>${esc(e)}</td><td>${a}<span class="g3-sub">since ${esc(since)}</span></td></tr>`).join('')}</tbody>
      </table></div>
    </div>
    <div class="b5-group"><h3 class="b5-group__head">Files that could not be read (1)</h3>
      <div class="b5-alert b5-alert--warning">${icon('status-warning')}<div>
        <p class="b5-alert__title">Maverick MK3 Profile · Chauvet Professional · revision “1.2b” ${labelTag('User')}</p>
        <p class="b5-alert__body">description.xml is not well-formed XML: XML syntax error on line 2104: unexpected EOF</p>
        <p class="b5-alert__body">The file stays in the library, but Benny512 cannot match it to devices, and importing it will most likely fail for the same reason. A newer revision on GDTF Share replaces it at a later sync.</p>
      </div></div>
      <p class="b5-caption">${NEW('unreadableFiles', 'Status gives the count (unreadable); the file and its reason are in each index entry (description.state/reason) but not listed in /api/gdtf/status')}</p>
    </div>`;
  }

  function creditBlock() {
    return `<div class="g3-credit" aria-label="GDTF credit">
      <div class="g3-logo-ph" role="img" aria-label="Placeholder: the official GDTF logo goes here">official GDTF logo goes here</div>
      <div><p class="g3-credit__text" style="margin:0">Powered by GDTF</p><p class="b5-caption" style="margin:0">Fixture files come from GDTF Share (gdtf-share.com). Every file is labelled as uploaded by its manufacturer or by a user.</p></div>
    </div>`;
  }

  function networkSectionStub() {
    return `<section class="b5-step-section" aria-labelledby="setNetHead">
      <h2 class="b5-step-section__head" id="setNetHead"><span class="b5-step-num">1</span> Network <span class="b5-step-section__note">which card each protocol goes out on</span></h2>
      <div class="b5-group"><h3 class="b5-group__head">Interface</h3>
        <div class="b5-field"><label class="b5-field__label" for="nicSelect">Art-Net network adapter</label>
          <select id="nicSelect" class="b5-select"><option selected>(default / any)</option><option>Ethernet — 192.168.1.40</option><option>Ethernet 2 — 2.0.0.10 (down)</option><option>Wi-Fi — 192.168.1.22 (down)</option></select>
          <span class="b5-field__hint">If nodes never answer a poll, this is the first thing to check — it must be the card on the lighting network, not the one with the internet on it.</span>
          <span class="b5-field__hint"><strong>Also decides GDTF sync:</strong> sync never uses the card chosen here (or picked automatically).${NEW('hint text', 'One added sentence on the existing field')}</span>
        </div>
        <div class="b5-field"><label class="b5-field__label" for="sacnNicSelect">sACN network adapter</label><select id="sacnNicSelect" class="b5-select"><option selected>Same adapter as Art-Net</option></select></div>
      </div>
      <p class="g3-elided">The rest of section 1 is unchanged and not drawn here.</p>
    </section>`;
  }

  function settingsPage(s) {
    const elidedTop = s.state === 'refused'
      ? `<p class="g3-elided">Sections 2–6 (Universe numbering, sACN output, Output, Capture &amp; logging, Theme) are unchanged and not drawn in this mockup.</p>`
      : `<p class="g3-elided">Sections 1–6 (Network, Universe numbering, sACN output, Output, Capture &amp; logging, Theme) are unchanged and not drawn in this mockup. The new GDTF Share section goes after Theme.</p>`;
    return `
      <div class="b5-page-header"><h1 class="b5-page-header__title">Settings</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">Station settings take effect when you press Apply. The theme applies at once, to this browser only.</span></div>
      ${s.state === 'refused' ? networkSectionStub() : ''}
      ${elidedTop}
      <section class="b5-step-section" aria-labelledby="setGdtfHead" id="gdtfSection">
        <h2 class="b5-step-section__head" id="setGdtfHead"><span class="b5-step-num">7</span> GDTF Share <span class="b5-step-section__note">fixture files from gdtf-share.com · these buttons act at once, no Apply needed</span></h2>
        <div class="g3-section-body">
          ${statusBlock(s)}
          ${s.state === 'refused' ? refusalAlert() : ''}
          ${routeInset(s)}
          ${loginGroup(s)}
          ${syncGroup(s)}
          ${s.state === 'partial' ? problemsGroup() : ''}
          ${manufacturerGroup(s)}
          ${creditBlock()}
        </div>
      </section>
      <section class="b5-step-section g3-stub" aria-labelledby="setResetHead">
        <h2 class="b5-step-section__head" id="setResetHead"><span class="b5-step-num">8</span> Danger zone — full reset <span class="b5-step-section__note">deletes the patch and shuts Benny512 down</span></h2>
        <p class="b5-caption">Unchanged, plus one line: <strong>a full reset also clears the GDTF Share login; the downloaded GDTF files are kept.</strong></p>
      </section>
      <section class="b5-step-section" aria-labelledby="setAboutHead" id="aboutSection">
        <h2 class="b5-step-section__head" id="setAboutHead"><span class="b5-step-num">9</span> About Benny512 <span class="b5-step-section__note">new section · proposed home for About</span></h2>
        <div class="b5-grid-2">
          ${param('Version', `v0.3.4 · commit 7e8b73b · built 17 Nov 2026 ${NEW('version endpoint', 'The exe knows its version (--version); no API gives it to the page today')}`)}
          ${param('Data folder', '%APPDATA%\\Benny512 (Fixture Library and GDTF library)')}
        </div>
        ${creditBlock()}
      </section>
      <section class="b5-actionbar" aria-label="Apply settings">
        <div class="b5-actionbar__status"><span class="b5-actionbar__title">${icon('apply')}Settings</span><span class="b5-pill b5-pill--sm b5-pill--ok">${icon('status-ok')}Saved</span><span class="b5-text-muted b5-text-sm">GDTF Share changes are saved as you make them.</span></div>
        <div class="b5-actionbar__buttons"><button class="b5-btn b5-btn--ghost">${icon('revert')}Discard changes</button><button class="b5-btn b5-btn--primary">${icon('apply')}Apply settings</button></div>
      </section>`;
  }

  // ---- Devices -----------------------------------------------------------------
  const DEVICES = {
    esprite: { name: 'Robin Esprite', mfr: 'Robe', mid: 0x5253, model: 0x0A1C, sw: '2.1.0', swid: 0x02010000, pers: 3, persCount: 6, persName: 'Mode 1', foot: 49, uid: '5253:0A1C0041' },
    mac: { name: 'MAC Aura XB', mfr: 'Martin', mid: 0x4D50, model: 0x00F4, sw: '1.5.2', swid: 0x01050002, pers: 2, persCount: 4, persName: 'Standard', foot: 34, uid: '4D50:00F40107' },
    rivale: { name: 'Rivale Profile', mfr: 'Ayrton', mid: 0x4159, model: 0x0310, sw: '2.3.0', swid: 0x02030000, pers: 2, persCount: 3, persName: 'Basic', foot: 31, uid: '4159:03100022' },
    jdc1: { name: 'JDC1', mfr: 'GLP', mid: 0x474C, model: 0x0102, sw: '1.0.12', swid: 0x0001000C, pers: 3, persCount: 5, persName: 'Extended', foot: 68, uid: '474C:01020009' },
  };
  const hex4 = v => '0x' + v.toString(16).toUpperCase().padStart(4, '0');
  const hex8 = v => '0x' + v.toString(16).toUpperCase().padStart(8, '0');

  function deviceCard(d, current) {
    return `<article class="b5-statecard ${current ? 'is-current' : 'is-ok'}" ${current ? 'aria-current="true"' : ''}>
      <div class="b5-statecard__top"><span class="b5-linkbadge${current ? '' : ' b5-linkbadge--none'}" aria-hidden="true">${current ? icon('nav-devices') : '·'}</span>
        <div class="b5-statecard__id"><strong class="b5-statecard__name">${esc(d.name)}</strong>
          <span class="b5-statecard__meta">${esc(d.mfr)} · UID <span class="b5-text-mono">${esc(d.uid)}</span> · Universe 3 (Art-Net 2) · addr 101 (101-${100 + d.foot})</span></div></div>
      <div class="b5-statecard__state"><span class="b5-pill b5-pill--md b5-pill--info">Fixture</span>${current ? '<span class="b5-pill b5-pill--md b5-pill--accent">Inspecting</span>' : ''}</div>
      <div class="b5-statecard__actions"><button type="button" class="b5-btn ${current ? '' : 'b5-btn--primary'}">${icon(current ? 'chevron-collapse' : 'chevron-expand')}${current ? 'Show inspector' : 'Inspect'}</button></div>
    </article>`;
  }

  function devicesPage(cur, inspectorHtml) {
    const list = Object.entries(DEVICES).map(([k, d]) => deviceCard(d, k === cur)).join('');
    return `<div class="b5-page-header"><h1 class="b5-page-header__title">Devices</h1></div>
      <div class="b5-toolbar b5-devices-target" aria-label="Discovery target"><div class="b5-toolbar__row">
        <label class="b5-visually-hidden" for="fixtureNodeSelect">Node and port</label>
        <select id="fixtureNodeSelect" class="b5-select" style="flex:1 1 260px;min-width:0"><option>Port 3 — universe 3 (Art-Net 2) · 2.0.0.31</option></select>
        <button type="button" class="b5-btn b5-btn--primary">${icon('signal')}Discover this port</button>
        <button type="button" class="b5-btn">${icon('signal')}Discover all ports</button>
      </div></div>
      <section aria-labelledby="devListHead">
        <div class="b5-section-head"><h2 id="devListHead">Devices</h2><span class="b5-text-muted">4 shown of 4 discovered</span></div>
        <div class="b5-board__list" id="deviceList">${list}</div>
      </section>
      ${inspectorHtml ? `<aside class="b5-inspector is-open" id="fixtureInspector" aria-label="Device inspector">
        <div class="b5-inspector__bar"><strong>Device inspector</strong><span class="b5-inspector__actions"><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Previous</button><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Next</button><button type="button" class="b5-btn b5-btn--sm b5-btn--ghost">Close</button></span></div>
        <div id="fixtureDetail">${inspectorHtml}</div></aside>` : ''}`;
  }

  function inspector(d, gdtfHtml) {
    return `<article class="b5-statecard is-current"><div class="b5-statecard__top"><span class="b5-linkbadge" aria-hidden="true">${icon('nav-devices')}</span>
        <div class="b5-statecard__id"><strong class="b5-statecard__name">${esc(d.name)}</strong><span class="b5-statecard__meta">${esc(d.mfr)} · UID <span class="b5-text-mono">${esc(d.uid)}</span> · Universe 3 (Art-Net 2) · node 2.0.0.31</span></div></div>
        <div class="b5-statecard__state"><span class="b5-pill b5-pill--md b5-pill--info">Fixture</span></div></article>
      <div class="b5-tabs" style="margin-top:var(--b5-space-4)">
        <div class="b5-tabs__list" role="tablist" aria-label="Device detail">
          <button class="b5-tabs__tab is-active" role="tab" aria-selected="true">Info</button><button class="b5-tabs__tab" role="tab" aria-selected="false" tabindex="-1">Parameters</button><button class="b5-tabs__tab" role="tab" aria-selected="false" tabindex="-1">Sensors</button><button class="b5-tabs__tab" role="tab" aria-selected="false" tabindex="-1">Status</button>
        </div>
        <div class="b5-tabs__panel" role="tabpanel" aria-label="Info">
          <section class="b5-group" aria-labelledby="ddIdentityHead"><h4 class="b5-group__head" id="ddIdentityHead">What this device says it is</h4>
            <div class="b5-grid-2">
              ${param('Manufacturer', `${esc(d.mfr)} (${hex4(d.mid)})`)}
              ${param('Model / fixture type', esc(d.name))}
              ${param('Software version', esc(d.sw))}
              ${param('Personality', `Personality ${d.pers}/${d.persCount} — “${esc(d.persName)}” (${d.foot} slots)`)}
            </div></section>
          <section class="b5-group" aria-labelledby="ddGdtfHead"><h4 class="b5-group__head" id="ddGdtfHead">GDTF file for this device</h4>
            <p class="b5-caption">Looked up in the GDTF library on this computer by the RDM IDs above: manufacturer ${hex4(d.mid)}, model ${hex4(d.model)}, software version ${hex8(d.swid)}, personality ${d.pers}. Nothing is sent to the device.</p>
            ${gdtfHtml}
          </section>
          <section class="b5-group" aria-labelledby="ddPatchHead"><h4 class="b5-group__head" id="ddPatchHead">Where it sits on the line</h4>
            <p class="g3-elided" style="margin:0">Unchanged: node / port, DMX footprint, start address, personality.</p></section>
        </div>
      </div>`;
  }

  // footprint check: the mode's footprint (getList modes) against the device's DMX footprint (DEVICE_INFO).
  const footCheck = (modeFoot, devFoot) => modeFoot === devFoot
    ? `${badge('ok', 'Footprints agree')}<span class="b5-text-sm">the device reports ${devFoot} slots; this mode has ${modeFoot} channels</span>`
    : `${badge('warning', 'Footprints differ')}<span class="b5-text-sm">the device reports ${devFoot} slots; this mode has ${modeFoot} channels. Check the mode on the fixture's own menu.</span>`;

  function matchFound() {
    const d = DEVICES.esprite;
    return `<div class="g3-match">
      <div class="g3-match__head"><strong class="g3-result__name">Robin Esprite</strong><span class="g3-result__mfr">Robe Lighting</span>${labelTag('Manufacturer')}</div>
      <div class="b5-grid-2">
        ${param('Matched on', 'manufacturer ID, model ID and this device’s software version')}
        ${param('Revision', '“DMX v1.3 (fw 2.1)” · GDTF 1.2 · updated 3 Oct 2026')}
      </div>
      <div class="g3-match__mode">${badge('ok', 'Mode found')}<span>Personality ${d.pers} is <strong>Mode 1</strong> · 49 channels</span></div>
      <span class="g3-sub">software version ${hex8(d.swid)} maps personality ${d.pers} to Mode 1</span>
      <div class="g3-match__mode">${footCheck(49, d.foot)}</div>
      <div class="g3-actions"><button type="button" class="b5-btn b5-btn--primary" data-open-import="esprite">${icon('export')}Add to Fixture library…</button><span class="b5-text-sm b5-text-muted">Not in your Fixture library yet${NEW('inFixtureLibrary', 'Derivable: compare the file’s SHA-256 with the Fixture Library’s stored source files')}</span></div>
      <details class="g3-also"><summary>1 more file matches</summary>
        <ul class="g3-match__list"><li><strong>Robin Esprite (custom gobos)</strong> ${labelTag('User')} · revision “v3 gobo pack” · same IDs and software version · Mode 1 found. Listed after the manufacturer’s file.</li></ul></details>
    </div>`;
  }

  function matchLikely() {
    const d = DEVICES.mac;
    return `<div class="g3-match">
      <div class="g3-match__head"><strong class="g3-result__name">MAC Aura XB</strong><span class="g3-result__mfr">Martin Professional</span>${labelTag('Manufacturer')}</div>
      <div class="b5-grid-2">
        ${param('Matched on', 'manufacturer ID and model ID only')}
        ${param('Revision', '“Rev 6” · GDTF 1.1 · updated 12 Jun 2025')}
      </div>
      <p class="b5-caption" style="margin:0">This file does not list software version ${hex8(d.swid)}</p>
      <div class="g3-match__mode">${badge('warning', 'Likely mode — not certain')}<span>Personality ${d.pers} is most likely <strong>Standard</strong> · 34 channels</span></div>
      <div class="b5-alert b5-alert--warning">${icon('status-warning')}<div>
        <p class="b5-alert__title">This file's software versions disagree on personality ${d.pers} (Standard, Standard 16-bit); Standard is the most likely, not a certain answer</p>
        <p class="b5-alert__body"><strong>Why Standard:</strong> likely: 3 of 4 software version(s) that map personality ${d.pers} map it to Standard</p>
      </div></div>
      <div><span class="b5-param__label">Other mode this personality maps to in this file</span>
        <ul class="g3-match__list"><li><strong>Standard 16-bit</strong> · 38 channels</li></ul></div>
      <div class="g3-match__mode">${footCheck(34, d.foot)}</div>
      <div class="g3-actions"><button type="button" class="b5-btn b5-btn--primary">${icon('export')}Add to Fixture library…</button></div>
    </div>`;
  }

  function matchOtherVersions() {
    const d = DEVICES.rivale;
    return `<div class="g3-match">
      <div class="g3-match__head"><strong class="g3-result__name">Rivale Profile</strong><span class="g3-result__mfr">Ayrton</span>${labelTag('Manufacturer')}</div>
      <div class="b5-grid-2">
        ${param('Matched on', 'manufacturer ID, model ID and this device’s software version')}
        ${param('Revision', '“v1.4” · GDTF 1.2 · updated 21 Aug 2026')}
      </div>
      <div class="g3-match__mode">${badge('warning', 'Mode from other versions — not certain')}<span>Personality ${d.pers} is probably <strong>Mode 2</strong> · 27 channels</span></div>
      <div class="b5-alert b5-alert--warning">${icon('status-warning')}<div>
        <p class="b5-alert__title">Software version ${hex8(d.swid)} (the device's) maps no mode for personality ${d.pers}; Mode 2 comes from software version(s) 0x02010000, 0x02020000 of this file, not from ${hex8(d.swid)}, so it is a guess</p>
        <p class="b5-alert__body"><strong>Why Mode 2:</strong> 2 of 2 other software version(s) that map personality ${d.pers} map it to Mode 2</p>
      </div></div>
      <div class="g3-match__mode">${footCheck(27, d.foot)}</div>
      <div class="g3-actions"><button type="button" class="b5-btn b5-btn--primary">${icon('export')}Add to Fixture library…</button></div>
    </div>`;
  }

  function matchNone(withResults) {
    const d = DEVICES.jdc1;
    const head = `<div class="b5-alert b5-alert--info" role="status">${icon('status-pending')}<div>
        <p class="b5-alert__title">No GDTF file in your library has this device's RDM IDs</p>
        <p class="b5-alert__body">None of the ${n(FILES)} files lists manufacturer ${hex4(d.mid)} with model ${hex4(d.model)}. Benny512 does not pick a file by its name on its own. 1 file could not be read, so it was not checked.</p></div></div>
      <div class="g3-search" id="ddNameSearchBox">
        <div class="b5-field"><label class="b5-field__label" for="ddNameSearch">Search the GDTF library by name instead</label><input id="ddNameSearch" class="b5-input" type="search" value="${withResults ? 'JDC1' : 'JDC1'}"></div>
        <button type="button" class="b5-btn ${withResults ? '' : 'b5-btn--primary'}" data-goto-scene="s16">${icon('filter')}Search by name</button>
      </div>
      <p class="b5-caption">Filled in from the device's model name. A file found by name is not confirmed by RDM: check its mode yourself.</p>`;
    if (!withResults) return head;
    const rows = [
      { name: 'JDC1', mfr: 'GLP', label: 'Manufacturer', rev: 'Firmware 1.0.x · DMX v2', rdm: `RDM IDs in this file: manufacturer ${hex4(d.mid)}, but <strong>the file gives no model ID</strong> (it says 0x0), so it cannot match a device by RDM.`, modes: 'Standard · 14 ch; Extended · 68 ch; Pixel · 98 ch', foot: 68 },
      { name: 'JDC1 Strobe (show mode set)', mfr: 'GLP', label: 'User', rev: 'Rev 2', rdm: '<strong>No RDM data in this file.</strong> It can only be found by name.', modes: 'Standard · 14 ch; Extended · 68 ch', foot: 68 },
    ];
    return head + `<p role="status" id="ddNameResults" class="b5-text-sm" style="margin:var(--b5-space-2) 0"><strong>2 files</strong> match “JDC1” by name. Found by name, not by RDM.</p>
      <div class="b5-workspace-list" style="max-height:none">${rows.map(r => `<div class="b5-workspace-item g3-result">
        <div class="g3-result__head"><strong class="g3-result__name">${esc(r.name)}</strong><span class="g3-result__mfr">${esc(r.mfr)}</span>${labelTag(r.label)}${badge('pending', 'Found by name')}</div>
        <span class="b5-text-sm">Revision “${esc(r.rev)}”</span>
        <span class="b5-text-sm">${r.rdm}</span>
        <span class="g3-result__modes">Modes: ${esc(r.modes)}</span>
        <span class="b5-text-sm">The device's personality ${d.pers} (“${esc(d.persName)}”, ${d.foot} slots): this file has a mode of ${r.foot} channels named Extended. Not confirmed.</span>
        <div class="b5-row"><button type="button" class="b5-btn b5-btn--sm">${icon('export')}Add to Fixture library…</button></div>
      </div>`).join('')}</div>`;
  }

  // ---- Fixture Library › Add from GDTF library ----------------------------------
  const LOCAL = [
    { name: 'Robin Esprite', long: 'Robe Robin Esprite LED Profile', mfr: 'Robe Lighting', label: 'Manufacturer', rev: 'DMX v1.3 (fw 2.1)', ver: '1.2', mb: 6.1, date: '3 Oct 2026', modes: [['Mode 1', 49], ['Mode 2', 42], ['Mode 3', 35]] },
    { name: 'Robin Esprite (custom gobos)', long: 'Robin Esprite with tour gobo pack', mfr: 'Robe Lighting', label: 'User', rev: 'v3 gobo pack', ver: '1.2', mb: 6.3, date: '14 Sep 2026', modes: [['Mode 1', 49]] },
    { name: 'Esprite Fresnel', long: 'Robe Esprite Fresnel', mfr: 'Robe Lighting', label: 'Manufacturer', rev: 'v1.0', ver: '1.2', mb: 4.8, date: '9 Jul 2026', modes: [['Mode 1', 41], ['Mode 2', 34]] },
  ];
  const modesText = ms => ms.map(([m, f]) => `${m} · ${f} ch`).join('; ');

  function resultRow(r, opts) {
    const o = opts || {};
    const tooBig = r.mb > 8;
    let action = `<button type="button" class="b5-btn b5-btn--sm" data-import="${esc(r.name)}">${icon('export')}${o.share && !r.here ? 'Download and import…' : 'Import…'}</button>`;
    if (tooBig) action = '';
    return `<div class="b5-workspace-item g3-result">
      <div class="g3-result__head"><strong class="g3-result__name">${esc(r.name)}</strong><span class="g3-result__mfr">${esc(r.mfr)}</span>${labelTag(r.label)}${o.share ? (r.here ? badge('ok', 'On this computer') : badge('pending', 'Not downloaded')) : ''}</div>
      ${r.long ? `<span class="b5-text-sm">Long name: ${esc(r.long)}</span>` : (o.share ? `<span class="b5-text-sm b5-text-muted">Long name: known once downloaded${NEW('longName in share search', 'getList has no LongName; G1 known limit')}</span>` : '')}
      <span class="b5-text-sm">Revision “${esc(r.rev)}” · GDTF ${esc(r.ver)} · ${r.mb.toFixed(1)} MB · updated ${esc(r.date)}</span>
      <span class="g3-result__modes">Modes: ${esc(modesText(r.modes))}</span>
      ${r.note ? `<span class="b5-text-sm">${r.note}</span>` : ''}
      ${tooBig ? `<div class="b5-alert b5-alert--warning g3-refusal">${icon('status-warning')}<div>
          <p class="b5-alert__title">Too large for the Fixture Library: ${r.mb.toFixed(1)} MB</p>
          <p class="b5-alert__body">The Fixture Library takes GDTF files up to 8 MB, so this one cannot be imported. It stays in the GDTF library, which keeps files up to 64 MB, and is not lost.</p></div></div>` : ''}
      ${action ? `<div class="b5-row">${action}</div>` : ''}
    </div>`;
  }

  function libraryDialog(mode) {
    // mode: 'local' | 'share' | 'big'
    const share = mode === 'share';
    let q = 'esprite', rows = LOCAL, count = '3 files match “esprite”', pending = null;
    if (mode === 'local') pending = { file: 'Robe Lighting@Robin Esprite@DMX v1.3 (fw 2.1).gdtf', types: 1 };
    if (share) {
      q = 'sharpy';
      rows = [
        { name: 'Sharpy Plus', mfr: 'Claypaky', label: 'Manufacturer', rev: 'fw 1.4 / DMX 3.2', ver: '1.2', mb: 3.9, date: '2 Feb 2026', modes: [['Standard', 24], ['Vector', 28]], here: false, note: `${icon('status-pending')} Claypaky is not followed: this downloads this one file only.` },
        { name: 'Sharpy Plus', mfr: 'Claypaky', label: 'User', rev: 'tour edit', ver: '1.1', mb: 3.7, date: '30 Nov 2025', modes: [['Standard', 24]], here: false, note: `${icon('status-pending')} Claypaky is not followed: this downloads this one file only.` },
        { name: 'Sharpy Wash 330', mfr: 'Claypaky', label: 'Manufacturer', rev: 'DMX 2.0', ver: '1.1', mb: 2.2, date: '11 Mar 2025', modes: [['Standard', 18]], here: false, note: `${icon('status-pending')} Claypaky is not followed: this downloads this one file only.` },
      ];
      count = '3 files on GDTF Share match “sharpy”';
      pending = { file: 'Claypaky@Sharpy Plus@fw 1.4 _ DMX 3.2.gdtf', types: 1, pulled: true };
    }
    if (mode === 'big') {
      q = 'mac ultra';
      rows = [
        { name: 'MAC Ultra Performance', long: 'Martin MAC Ultra Performance', mfr: 'Martin Professional', label: 'Manufacturer', rev: 'fw 2.4.0', ver: '1.2', mb: 11.4, date: '6 Nov 2026', modes: [['Basic', 41], ['Extended', 52], ['Ludicrous', 76]] },
        { name: 'MAC Ultra Wash', long: 'Martin MAC Ultra Wash', mfr: 'Martin Professional', label: 'Manufacturer', rev: 'fw 2.4.0', ver: '1.2', mb: 7.2, date: '6 Nov 2026', modes: [['Basic', 34], ['Extended', 45]] },
      ];
      count = '2 files match “mac ultra”';
    }
    const tabs = `<div class="g3-lib-source b5-segmented b5-segmented--sm" role="group" aria-label="Search in">
        <button type="button" class="b5-seg${share ? '' : ' is-on'}" aria-pressed="${!share}" data-goto-scene="${share ? 's9' : 's9'}">${share ? '' : icon('status-ok')}On this computer (${n(FILES)} files)</button>
        <button type="button" class="b5-seg${share ? ' is-on' : ''}" aria-pressed="${share}" data-goto-scene="s10">${share ? icon('status-ok') : ''}GDTF Share (online)</button>
      </div>`;
    const sourceNote = share
      ? `<p class="b5-caption">Searches everything on GDTF Share, from the list fetched at the last sync (${LAST_SYNC}), by name and manufacturer. Downloading needs the internet and never uses the lighting network. A fixture from a manufacturer you do not follow is downloaded once, not kept up to date.</p>`
      : `<p class="b5-caption">Searches the GDTF files already on this computer by name, long name and manufacturer. Works offline. Every result says whether its manufacturer or a user uploaded it.</p>`;
    const importArea = pending ? `<div class="b5-inset"><strong>${esc(pending.file)}</strong> · ${pending.types} fixture types${pending.pulled ? `<p>Downloaded from GDTF Share and kept in the GDTF library.</p>` : ''}<p class="b5-text-sm">From the GDTF library · Manufacturer file${NEW('label on the imported fixture', 'Owner round 2: label every imported fixture Manufacturer or User; the Fixture Library record has no field for it yet (the file endpoint sends X-Benny-GDTF-Uploader)')}</p><div class="b5-row"><button class="b5-btn b5-btn--primary">Apply import</button><button class="b5-btn">Cancel</button></div></div>` : '';
    const status = mode === 'big' ? 'Not imported: MAC Ultra Performance is 11.4 MB, and the Fixture Library takes GDTF files up to 8 MB.' : pending ? 'Review the import, then apply. Existing types are merged.' : '';
    return `<div class="b5-row"><h2>Fixture library</h2><button class="b5-btn" data-close-library>Close</button></div>
      <p class="b5-caption">Shared across shows. Saved on this PC. Verification is per mode, by the operator.</p>
      <p data-library-status role="status">${esc(status)}</p>
      <div class="b5-row"><label class="b5-btn">Import GDTF / library JSON<input type="file" class="b5-visually-hidden"></label><button class="b5-btn" aria-pressed="true">${icon('filter')}Add from GDTF library</button><button class="b5-btn">Save profiles from this patch</button><a class="b5-btn" href="#" onclick="return false">Export library JSON</a></div>
      <p class="b5-caption">Exports profiles, verification records, and original GDTFs imported here.</p>
      <div data-library-import>${importArea}</div>
      <section aria-labelledby="gdtfAddHead">
        <h3 id="gdtfAddHead">Add from GDTF library</h3>
        ${tabs}
        ${sourceNote}
        <div class="g3-search"><div class="b5-field"><label class="b5-field__label" for="gdtfQuery">Search name${share ? '' : ', long name'} or manufacturer</label><input id="gdtfQuery" class="b5-input" type="search" value="${esc(q)}"></div></div>
        <p class="b5-text-sm" role="status">${esc(count)}</p>
        <div class="b5-workspace-list">${rows.map(r => resultRow(r, { share })).join('')}</div>
      </section>
      <p class="g3-elided">The library's own records list (search, verified modes, Use in patch) is unchanged below and not drawn here.</p>`;
  }

  // ---- scenes ------------------------------------------------------------------
  const SCENES = [
    { id: 's1', group: 'Settings › GDTF Share', title: '1 · No login yet', tab: 'settings', settings: { state: 'no-login' }, about: 'First run: login fields, Test login, Save login. The route box always says which adapter is the lighting network.' },
    { id: 's2', group: 'Settings › GDTF Share', title: '2 · Logged in, synced', tab: 'settings', settings: { state: 'ok' }, about: '“Saved · user <name>”, never the password; last sync; per-manufacturer counts and result; Sync now; follow / stop following.' },
    { id: 's3', group: 'Settings › GDTF Share', title: '3 · Syncing', tab: 'settings', settings: { state: 'syncing' }, about: 'Progress in words (file x of y); the rest stays usable. Naming the manufacturer needs a new field.' },
    { id: 's4', group: 'Settings › GDTF Share', title: '4 · Offline', tab: 'settings', settings: { state: 'offline' }, about: '“offline · last synced <time>”; the library on this computer is still used.' },
    { id: 's5', group: 'Settings › GDTF Share', title: '5 · Refused: internet is on the lighting adapter', tab: 'settings', settings: { state: 'refused' }, nic: 'Ethernet (192.168.1.40)', scrollTo: 'setGdtfHead', about: 'Owner round 3 #1: refused on purpose; says which adapter, and what to change (Settings › 1 Network › Art-Net network adapter).' },
    { id: 's6', group: 'Settings › GDTF Share', title: '6 · Followed name matched nothing', tab: 'settings', settings: { state: 'ok', unmatched: true }, scrollTo: 'mfrHead', about: '“Martin” and “ChromaQ” matched nothing; the similar names GDTF Share has are offered, never followed for you.' },
    { id: 's7', group: 'Settings › GDTF Share', title: '7 · Downloads waiting, one file unreadable', tab: 'settings', settings: { state: 'partial' }, scrollTo: 'pendHead', about: 'Pending downloads with the reason and tries; one unreadable file with its reason (the list needs a new field).' },
    { id: 's8', group: 'Settings › GDTF Share', title: '8 · Powered by GDTF credit + About', tab: 'settings', settings: { state: 'ok' }, scrollTo: 'aboutSection', about: 'Credit on the GDTF Share section and in a proposed About section. Logo is a labelled placeholder, not drawn.' },
    { id: 's9', group: 'Fixture Library › Add from GDTF library', title: '9 · Search this computer (offline), pick one', tab: 'devices', lib: 'local', about: 'Offline search by name, long name, manufacturer; Manufacturer/User label on every row; Import… runs the existing confirm step (shown above the search).' },
    { id: 's10', group: 'Fixture Library › Add from GDTF library', title: '10 · Search GDTF Share (online), single pull', tab: 'devices', lib: 'share', about: 'Single pull from an unfollowed manufacturer (Claypaky): downloaded once, then the same confirm step.' },
    { id: 's11', group: 'Fixture Library › Add from GDTF library', title: '11 · File over the 8 MB import cap', tab: 'devices', lib: 'big', dlgScrollTo: '#gdtfAddHead', about: 'Honest refusal before trying: the file is 11.4 MB, the Fixture Library takes 8 MB. Open question for Dom: raise the cap?' },
    { id: 's12', group: 'Devices › GDTF match', title: '12 · Exact match (found)', tab: 'devices', device: 'esprite', match: matchFound, about: 'modeState found: the device’s software version maps its personality. Manufacturer file first; the user file listed after.' },
    { id: 's13', group: 'Devices › GDTF match', title: '13 · Likely mode, with warning', tab: 'devices', device: 'mac', match: matchLikely, about: 'modeState likely: modeBasis and modeWarning verbatim; the other modeChoices listed.' },
    { id: 's14', group: 'Devices › GDTF match', title: '14 · Mode from other software versions', tab: 'devices', device: 'rivale', match: matchOtherVersions, about: 'modeState other-versions: the warning names the versions used and the device’s own.' },
    { id: 's15', group: 'Devices › GDTF match', title: '15 · No RDM match', tab: 'devices', device: 'jdc1', match: () => matchNone(false), about: 'Empty candidates: no name guessing. A name search is offered instead, filled in from the device’s model name.' },
    { id: 's16', group: 'Devices › GDTF match', title: '16 · Name search: no RDM data, zero model ID', tab: 'devices', device: 'jdc1', match: () => matchNone(true), inspectorScrollTo: 'ddNameSearchBox', about: 'Files found by name say why RDM could not match them: “the file gives no model ID” (0x0) and “no RDM data”.' },
  ];

  const $ = id => document.getElementById(id);
  const sel = $('g3Scene');
  let groups = {};
  SCENES.forEach(s => { (groups[s.group] = groups[s.group] || []).push(s); });
  sel.innerHTML = Object.entries(groups).map(([g, list]) => `<optgroup label="${esc(g)}">${list.map(s => `<option value="${s.id}">${esc(s.title)}</option>`).join('')}</optgroup>`).join('');

  function publishHeights() {
    const h = $('g3Review') || document.querySelector('.g3-review');
    document.documentElement.style.setProperty('--g3-review-h', h.getBoundingClientRect().height + 'px');
    const header = document.querySelector('.b5-header'), ctx = document.querySelector('.b5-show-context');
    const wide = matchMedia('(min-width: 1024px)').matches;
    const ch = wide ? Math.max(header.getBoundingClientRect().height, ctx.getBoundingClientRect().height) : header.getBoundingClientRect().height + ctx.getBoundingClientRect().height;
    document.documentElement.style.setProperty('--b5-chrome-height', ch + 'px');
  }

  function show(id, focusSwitcher) {
    const s = SCENES.find(x => x.id === id) || SCENES[0];
    sel.value = s.id;
    if (location.hash !== '#' + s.id) history.replaceState(null, '', '#' + s.id);
    $('ctxNic').textContent = s.nic || 'Ethernet 2 (2.0.0.10)';
    const dlg = $('libDialog');
    if (dlg.open) dlg.close();
    if (s.tab === 'settings') {
      $('screen-settings').innerHTML = settingsPage(s.settings);
      $('screen-devices').innerHTML = '';
    } else {
      $('screen-settings').innerHTML = '';
      const d = s.device ? DEVICES[s.device] : null;
      $('screen-devices').innerHTML = devicesPage(s.device || null, d ? inspector(d, s.match()) : '');
    }
    setNav(s.tab);
    if (s.lib) { dlg.innerHTML = libraryDialog(s.lib); dlg.show(); }
    $('g3About').textContent = s.about;
    $('g3About').title = s.about;
    $('g3Prev').disabled = SCENES[0] === s;
    $('g3Next').disabled = SCENES[SCENES.length - 1] === s;
    document.title = `${s.title} · GDTF mockup G3a`;
    publishHeights();
    window.scrollTo(0, 0);
    if (s.scrollTo) { const t = $(s.scrollTo); if (t) t.scrollIntoView({ block: 'start' }); }
    if (s.device) { const t = $(s.inspectorScrollTo || 'ddGdtfHead'); if (t) t.scrollIntoView({ block: 'start' }); window.scrollTo(0, 0); }
    if (s.dlgScrollTo) { const t = dlg.querySelector(s.dlgScrollTo); if (t) t.scrollIntoView({ block: 'start' }); }
    if (focusSwitcher) sel.focus();
    document.body.dataset.scene = s.id;
  }

  sel.addEventListener('change', () => show(sel.value));
  $('g3Prev').addEventListener('click', () => { const i = SCENES.findIndex(s => s.id === sel.value); if (i > 0) show(SCENES[i - 1].id); });
  $('g3Next').addEventListener('click', () => { const i = SCENES.findIndex(s => s.id === sel.value); if (i < SCENES.length - 1) show(SCENES[i + 1].id); });
  $('g3Theme').addEventListener('change', e => { document.documentElement.dataset.theme = e.target.value; });
  $('g3Notes').addEventListener('change', e => document.body.classList.toggle('g3-hide-notes', !e.target.checked));
  // Demonstrative links between scenes; every other button is inert.
  document.addEventListener('click', e => {
    const go = e.target.closest('[data-goto-scene]');
    if (go) { e.preventDefault(); show(go.dataset.gotoScene); return; }
    const to = e.target.closest('[data-goto]');
    if (to) { const t = $(to.dataset.goto); if (t) { t.scrollIntoView({ block: 'center' }); t.focus(); } return; }
    if (e.target.closest('[data-close-library]')) { $('libDialog').close(); return; }
    if (e.target.closest('[data-open-import]')) { show('s9'); }
  });
  $('libDialog').addEventListener('keydown', e => { if (e.key === 'Escape') { e.preventDefault(); $('libDialog').close(); } });
  addEventListener('resize', publishHeights);
  addEventListener('hashchange', () => show(location.hash.slice(1)));
  show((location.hash || '#s1').slice(1));
})();
