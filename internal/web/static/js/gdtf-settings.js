// gdtf-settings.js — Settings › 7 GDTF Share and Settings › 9 About (G3b2).
// Built to the approved G3a mockup (docs/design/gdtf-library/, scenes 1–8)
// over GET /api/gdtf/status and GET /api/version (internal/web/gdtf.go,
// version.go).
//
// THESE BUTTONS ACT AT ONCE (owner round 5 #3). Save login, Test login,
// Remove login, Follow, Stop following and Sync now take effect when pressed,
// not on Apply settings: none of them changes what is on the wire. The
// section header and the action bar both say so. The inputs here are not
// part of the screen-wide Apply form (settings.js FIELD_IDS), so they never
// count as unsaved station settings.
//
// THE PASSWORD is typed into a password field, sent once, and the field is
// cleared. No endpoint returns it; the screen shows only the server's own
// "Saved · user <name>".
//
// STOP FOLLOWING KEEPS THE FILES (owner round 5 #4): the screen says so.
//
// THE LIGHTING NETWORK (owner round 3 #1): the "Which network sync uses" box
// is always shown, naming the adapters sync never uses (lightingAdapters),
// so the refusal is telegraphed before anyone meets it; a refusal adds the
// guidance and a button that jumps to Settings › 1 Network › Art-Net
// network adapter.
const GdtfSettings = (() => {
  const G = GdtfCommon;
  const esc = G.esc;
  let st = null;          // last GET /api/gdtf/status
  let version = null;     // GET /api/version
  let loadError = '';
  let changingLogin = false;
  let actionMsg = { login: '', sync: '', follow: '' };
  let busy = false;
  let timer = null;
  let fastUntil = 0;      // poll quickly for a while after starting a sync

  const icon = n => UI.icon(n);
  const param = (label, html) => `<div class="b5-param"><span class="b5-param__label">${esc(label)}</span><span class="b5-param__value">${html}</span></div>`;

  // --- status --------------------------------------------------------------

  function unmatched() {
    return (st.manufacturers || []).filter(m => !m.matched && !m.notYetChecked);
  }

  function lastSyncWords() { return G.when(st.lastSync) || 'never'; }

  function statusBlock() {
    const files = G.num(st.files);
    const every = `syncs at start-up and every ${G.num(st.intervalMinutes)} minutes while Benny512 runs`;
    let b, msg, meta = '';
    if (!st.enabled) {
      b = UI.badge('unknown', 'Off'); msg = 'The GDTF library is off in this mode';
      meta = `${st.message}. A demo or rehearsal run never talks to GDTF Share or writes the installation's library.`;
    } else if (st.running) {
      b = UI.badge('pending', 'Syncing'); msg = 'Syncing with GDTF Share';
    } else if (!st.login.saved && (st.state === 'never' || st.state === 'no-login')) {
      b = UI.badge('pending', 'Not set up'); msg = 'No GDTF Share login saved';
      meta = st.files ? `${files} files on this computer are used as they are. Enter your login below to keep them up to date.`
        : 'Nothing has been downloaded yet. Enter your login below, then Save login: the first sync starts on its own.';
    } else {
      switch (st.state) {
        case 'ok':
          b = UI.badge('ok', 'Up to date'); msg = st.message;
          meta = `Last synced ${lastSyncWords()} · ${files} files on this computer · ${every}`; break;
        case 'partial':
          b = UI.badge('warning', 'Partly synced'); msg = st.message;
          meta = `Last synced ${lastSyncWords()} · ${files} files on this computer`; break;
        case 'offline':
          b = `<span class="b5-badge b5-badge--stale">${icon('lease-lost')}Offline</span>`; msg = `offline · last synced ${lastSyncWords()}`;
          meta = `GDTF Share could not be reached. The ${files} files on this computer are used as they are. Benny512 tries again in ${G.num(st.intervalMinutes)} minutes, or press Sync now. Details: ${st.message}`; break;
        case 'refused':
          b = UI.badge('warning', 'Sync refused'); msg = st.message;
          meta = `Nothing was downloaded. The ${files} files on this computer are used as they are. Last synced ${lastSyncWords()}.`; break;
        case 'login-failed':
          b = UI.badge('error', 'Login refused'); msg = st.message;
          meta = `Check the user name and password below, then Test login. The ${files} files on this computer are used as they are.`; break;
        case 'stopped':
          b = UI.badge('warning', 'Stopped'); msg = st.message;
          meta = `The next sync carries on. Last synced ${lastSyncWords()} · ${files} files on this computer`; break;
        case 'never':
          b = UI.badge('pending', 'Not synced yet'); msg = 'Not synced with GDTF Share yet';
          meta = `Press Sync now, or it runs at the next start-up · ${files} files on this computer`; break;
        default:
          b = UI.badge('error', 'Sync failed'); msg = st.message || 'The last sync failed';
          meta = `Last synced ${lastSyncWords()} · ${files} files on this computer`;
      }
    }
    let extra = '';
    if (st.enabled && st.running) {
      const p = st.progress || {};
      let words;
      if (p.total > 0) {
        const n = Math.min(p.done + 1, p.total);
        words = `File <strong>${G.num(n)}</strong> of <strong>${G.num(p.total)}</strong>` +
          (p.manufacturer ? ` · now ${esc(p.manufacturer)} (manufacturer ${G.num(p.manufacturerIndex)} of ${G.num(p.manufacturerCount)})` : '');
      } else {
        words = 'Logging in and fetching the list from GDTF Share';
      }
      const pct = p.total > 0 ? (100 * p.done / p.total).toFixed(1) : 0;
      const text = p.total > 0 ? `File ${G.num(Math.min(p.done + 1, p.total))} of ${G.num(p.total)}` : 'Fetching the list';
      extra = `<div class="b5-gdtf-progress">
          <span>${words}</span>
          <div class="b5-progress" role="progressbar" aria-label="Sync progress" aria-valuemin="0" aria-valuemax="${p.total || 0}" aria-valuenow="${p.done || 0}" aria-valuetext="${esc(text)}"><div class="b5-progress__fill" style="width:${pct}%"></div></div>
          <span class="b5-gdtf-status__meta">The library stays usable while it syncs: search, import and device matching work on the files already here. Downloads go one at a time, a second apart.</span>
        </div>`;
    }
    const um = st.enabled ? unmatched() : [];
    if (um.length) {
      extra += `<span class="b5-gdtf-status__meta">${icon('status-warning')} <strong>${um.length} followed name${um.length === 1 ? '' : 's'} matched nothing on GDTF Share</strong> (${esc(um.map(m => m.name).join(', '))}): see the list below.</span>`;
    }
    return `<div class="b5-gdtf-status" role="status" aria-label="GDTF Share status" data-gdtf-state="${esc(st.enabled ? (st.running ? 'syncing' : st.state) : 'off')}">${b}<span class="b5-gdtf-status__msg">${esc(msg)}</span>${meta ? `<span class="b5-gdtf-status__meta">${esc(meta)}</span>` : ''}${extra}</div>`;
  }

  // --- which network -------------------------------------------------------

  function adapterWords(a) {
    const addrs = (a.addresses || []).map(x => x.replace(/\/\d+$/, '')).join(', ');
    return `${esc(a.name || 'an adapter')}${addrs ? ` (${esc(addrs)})` : ''}`;
  }

  function lightingAdapters() {
    const list = st.lightingAdapters || [];
    return { art: list.find(a => a.protocol === 'Art-Net') || { bound: false }, sacn: list.find(a => a.protocol === 'sACN') || { bound: false, sameAsArtNet: true } };
  }

  function routeBox() {
    const { art, sacn } = lightingAdapters();
    let artWords;
    if (!art.bound) artWords = 'no adapter is bound in this mode (a demo or rehearsal run sends nothing on a real network)';
    else if (art.auto) artWords = `${adapterWords(art)} — picked automatically when Benny512 started, because no adapter is chosen in <em>Art-Net network adapter</em> (or the chosen one was missing)`;
    else artWords = `${adapterWords(art)} — set in Settings › 1 Network`;
    let sacnWords;
    if (sacn.sameAsArtNet) sacnWords = 'same adapter as Art-Net';
    else if (sacn.bound) sacnWords = adapterWords(sacn);
    else sacnWords = `“${esc(sacn.name)}”, which this computer does not have`;
    let internet;
    if (st.state === 'refused' && !st.running) {
      internet = `the only route this computer has to gdtf-share.com is the lighting adapter${art.bound ? ` ${adapterWords(art)}` : ''}, so sync is refused.`;
    } else if (st.internetRoute) {
      const r = st.internetRoute;
      internet = `any other adapter with a route to gdtf-share.com. The last connection went out by ${esc(r.adapter || 'an adapter')} (${esc(r.localAddress)}), ${esc(G.when(r.at))}.`;
    } else {
      internet = 'any other adapter with a route to gdtf-share.com. No connection to GDTF Share has been made since Benny512 started.';
    }
    return `<div class="b5-inset" data-gdtf-route>
      <p class="b5-inset__head">${icon('network-node')}Which network sync uses</p>
      <p class="b5-note"><strong>Lighting network: sync never uses it.</strong> Art-Net: ${artWords}. sACN: ${sacnWords}.</p>
      <p class="b5-note"><strong>Internet:</strong> ${internet}</p>
      <p class="b5-note">Sync may run while ARMED. If the only way to the internet is the lighting adapter, Benny512 refuses to sync and says so here; it never sends GDTF Share traffic over the lighting network.</p>
    </div>`;
  }

  function refusalAlert() {
    const { art } = lightingAdapters();
    const where = art.bound ? `Art-Net goes out on ${adapterWords(art)}, and that card is also this computer's only way to the internet.` : 'The lighting adapter is also this computer\'s only way to the internet.';
    return `<div class="b5-alert b5-alert--warning" role="alert">${icon('status-warning')}<div>
      <p class="b5-alert__title">Sync refused: the only route to the internet is the lighting network adapter</p>
      <p class="b5-alert__body">${where} Sync never uses the lighting network, so nothing was downloaded. This is on purpose, not a fault.</p>
      <p class="b5-alert__body"><strong>To sync:</strong> in <strong>Settings › 1 Network › Art-Net network adapter</strong>, choose the card that goes to the lighting network${art.auto ? ' instead of “(default / any)”' : ''}, then restart Benny512. Or give this computer another way to the internet, such as Wi-Fi.</p>
      <div class="b5-gdtf-actions"><button type="button" class="b5-btn" data-gdtf-goto-nic>${icon('network-node')}Go to Art-Net network adapter</button></div>
    </div></div>`;
  }

  // --- login ---------------------------------------------------------------

  function loginGroup() {
    const l = st.login || {};
    const fields = `<div class="b5-gdtf-fields">
          <div class="b5-field"><label class="b5-field__label" for="gdtfUser">GDTF Share user name</label><input id="gdtfUser" class="b5-input" type="text" autocomplete="username" spellcheck="false" value="${esc(changingLogin ? l.user : '')}"></div>
          <div class="b5-field"><label class="b5-field__label" for="gdtfPass">Password</label><input id="gdtfPass" class="b5-input" type="password" autocomplete="current-password"></div>
        </div>`;
    const msg = `<span class="b5-text-muted b5-text-sm" role="status" data-gdtf-login-msg>${esc(actionMsg.login || (l.saved && !changingLogin ? '' : 'Test login checks it with GDTF Share without saving it.'))}</span>`;
    if (!l.saved || changingLogin) {
      const problem = !l.saved && l.storage && l.storage !== 'none' ? `<div class="b5-alert b5-alert--warning">${icon('status-warning')}<div><p class="b5-alert__body">${esc(l.message)}</p></div></div>` : '';
      return `<div class="b5-group"><h3 class="b5-group__head">Login</h3>
        <p class="b5-caption">Your own GDTF Share account (free at gdtf-share.com). It is saved on this computer only, for this Windows user, with the password encrypted. It never goes into show files, library exports, logs or captures, and each Benny512 install needs its own login.</p>
        ${problem}${fields}
        <div class="b5-gdtf-actions"><button type="button" class="b5-btn" data-gdtf-test>${icon('signal')}Test login</button><button type="button" class="b5-btn b5-btn--primary" data-gdtf-save>${icon('apply')}Save login</button>${changingLogin ? '<button type="button" class="b5-btn b5-btn--ghost" data-gdtf-cancel-change>Cancel</button>' : ''}
        ${msg}</div>
      </div>`;
    }
    const where = l.storage === 'encrypted' ? 'password encrypted on this computer, never shown' : 'password never shown';
    return `<div class="b5-group"><h3 class="b5-group__head">Login</h3>
      <p class="b5-gdtf-login">${UI.badge('ok', 'Saved')}<span>${esc(l.message)}</span><span class="b5-text-muted b5-text-sm">${where}</span></p>
      <div class="b5-gdtf-actions"><button type="button" class="b5-btn" data-gdtf-test>${icon('signal')}Test login</button><button type="button" class="b5-btn" data-gdtf-change>Change login…</button><button type="button" class="b5-btn b5-btn--ghost" data-gdtf-remove>${icon('close')}Remove login</button>
      ${msg}</div>
    </div>`;
  }

  // --- sync ----------------------------------------------------------------

  function syncGroup() {
    const pending = (st.pending || []).length, unreadable = st.unreadable || 0;
    let why = '';
    if (!st.login.saved) why = 'Save a login first.';
    else if (st.running) why = 'A sync is running.';
    const can = !why;
    return `<div class="b5-group"><h3 class="b5-group__head">Sync</h3>
      <div class="b5-grid-2">
        ${param('Last synced', st.lastSync ? esc(G.when(st.lastSync)) : '<span class="b5-text-muted">never</span>')}
        ${param('Files on this computer', G.num(st.files))}
        ${param('Waiting to download', pending ? `<strong>${G.num(pending)}</strong> — retried at the next sync` : '0')}
        ${param('Files that could not be read', unreadable ? `<strong>${G.num(unreadable)}</strong> — kept, see below` : '0')}
      </div>
      <div class="b5-gdtf-actions"><button type="button" class="b5-btn b5-btn--primary" data-gdtf-sync ${can ? '' : 'aria-disabled="true"'}>${icon('refresh')}Sync now</button><span class="b5-text-muted b5-text-sm" role="status" data-gdtf-sync-msg>${esc(why || actionMsg.sync)}</span></div>
      <p class="b5-caption">Benny512 syncs at start-up and every ${G.num(st.intervalMinutes)} minutes while it runs. It keeps the newest revision of each fixture only; fixtures already in your Fixture Library or a show keep their own copy.</p>
    </div>`;
  }

  function problemsGroup() {
    const pend = st.pending || [], bad = st.unreadableFiles || [];
    let html = '';
    if (pend.length) {
      html += `<div class="b5-group"><h3 class="b5-group__head" id="gdtfPendHead">Waiting to download (${G.num(pend.length)})</h3>
      <p class="b5-caption">One file that fails never stops the others. Each stays listed here and is tried again at the next sync.</p>
      <div class="b5-gdtf-table-wrap"><table class="b5-table">
        <caption class="b5-visually-hidden">Downloads waiting for the next sync</caption>
        <thead><tr><th scope="col">Fixture</th><th scope="col">File from</th><th scope="col">Why it is waiting</th><th scope="col">Tries</th></tr></thead>
        <tbody>${pend.map(p => `<tr><th scope="row">${esc(p.fixture)}<span class="b5-gdtf-sub">${esc(p.manufacturer)} · revision “${esc(p.revision)}”</span></th><td>${G.labelTag(p.uploaderLabel)}</td><td>${esc(p.error)}</td><td>${G.num(p.attempts)}<span class="b5-gdtf-sub">since ${esc(G.when(p.since))}</span></td></tr>`).join('')}</tbody>
      </table></div></div>`;
    }
    if (bad.length) {
      html += `<div class="b5-group"><h3 class="b5-group__head">Files that could not be read (${G.num(bad.length)})</h3>
      ${bad.map(u => `<div class="b5-alert b5-alert--warning">${icon('status-warning')}<div>
        <p class="b5-alert__title">${esc(u.fixture)} · ${esc(u.manufacturer)} · revision “${esc(u.revision)}” ${G.labelTag(u.uploaderLabel)}</p>
        <p class="b5-alert__body">${esc(u.reason)}</p>
        <p class="b5-alert__body">The file stays in the library, but Benny512 cannot match it to devices, and importing it will most likely fail for the same reason. A newer revision on GDTF Share replaces it at a later sync.</p>
      </div></div>`).join('')}</div>`;
    }
    return html;
  }

  // --- manufacturers ---------------------------------------------------------

  function manufacturerRows() {
    const p = st.progress || {};
    return (st.manufacturers || []).map(m => {
      const stop = `<button type="button" class="b5-btn b5-btn--sm b5-btn--ghost" data-gdtf-unfollow="${esc(m.name)}" aria-label="Stop following ${esc(m.name)}">${icon('close')}Stop following</button>`;
      if (m.notYetChecked) {
        return `<tr><th scope="row">${esc(m.name)}</th><td colspan="3">${UI.badge('pending', 'Not checked yet')}<span class="b5-gdtf-sub">The next sync looks it up on GDTF Share.</span></td><td>${stop}</td></tr>`;
      }
      if (!m.matched) {
        const similar = m.similar || [];
        return `<tr><th scope="row">${esc(m.name)}</th><td>—</td><td>${G.num(m.localFiles)}</td>
          <td>${UI.badge('warning', 'Not found on GDTF Share')}<span class="b5-gdtf-sub">No manufacturer named “${esc(m.name)}” on GDTF Share; nothing was downloaded for it.</span>
          ${similar.length ? `<div class="b5-gdtf-similar"><span class="b5-text-sm">Similar names on GDTF Share:</span>${similar.map(x => `<button type="button" class="b5-btn b5-btn--sm" data-gdtf-instead="${esc(m.name)}" data-gdtf-to="${esc(x)}">Follow “${esc(x)}” instead</button>`).join('')}</div>`
            : '<span class="b5-gdtf-sub">GDTF Share has no similar name either.</span>'}</td>
          <td>${stop}</td></tr>`;
      }
      let result = UI.badge('ok', 'Up to date');
      if (st.running && p.manufacturer && p.manufacturer.toLowerCase() === (m.shareName || '').toLowerCase()) result = UI.badge('pending', 'Syncing now');
      else if (m.failed > 0) result = UI.badge('warning', `${G.num(m.failed)} waiting to download`);
      return `<tr><th scope="row">${esc(m.name)}${m.shareName && m.shareName !== m.name ? `<span class="b5-gdtf-sub">on GDTF Share as “${esc(m.shareName)}”</span>` : ''}</th>
        <td>${G.num(m.shareManufacturerFiles + m.shareUserFiles)}<span class="b5-gdtf-sub">${G.num(m.shareManufacturerFiles)} manufacturer, ${G.num(m.shareUserFiles)} user</span></td>
        <td>${G.num(m.localFiles)}</td>
        <td>${result}<span class="b5-gdtf-sub">checked ${esc(G.when(m.checkedAt))}</span></td>
        <td>${stop}</td></tr>`;
    }).join('');
  }

  function manufacturerGroup() {
    const n = (st.manufacturers || []).length;
    return `<div class="b5-group"><h3 class="b5-group__head" id="gdtfMfrHead">Manufacturers you follow (${G.num(n)})</h3>
      <p class="b5-caption">Every file GDTF Share has for these manufacturers is kept on this computer: files uploaded by the manufacturer and by users, each labelled. For one fixture from anyone else, use <strong>Show › Fixture library › Add from GDTF library</strong>.</p>
      ${n ? `<div class="b5-gdtf-table-wrap"><table class="b5-table">
        <caption class="b5-visually-hidden">Followed manufacturers and their last sync result</caption>
        <thead><tr><th scope="col">Manufacturer</th><th scope="col">On GDTF Share</th><th scope="col">On this computer</th><th scope="col">Last sync</th><th scope="col"><span class="b5-visually-hidden">Actions</span></th></tr></thead>
        <tbody>${manufacturerRows()}</tbody>
      </table></div>` : '<p class="b5-text-sm">None followed: nothing is kept up to date. Follow a manufacturer below.</p>'}
      <div class="b5-gdtf-addrow">
        <div class="b5-field"><label class="b5-field__label" for="gdtfAddMfr">Follow another manufacturer</label><input id="gdtfAddMfr" class="b5-input" type="text" placeholder="name as GDTF Share spells it" spellcheck="false"></div>
        <button type="button" class="b5-btn" data-gdtf-follow>${icon('apply')}Follow</button>
        <span class="b5-text-muted b5-text-sm" role="status" data-gdtf-follow-msg>${esc(actionMsg.follow)}</span>
      </div>
      <p class="b5-caption">A new name is looked up at the next sync. A name GDTF Share does not know is reported here with the closest names it has, never dropped and never swapped for you. Stop following keeps the files already downloaded; they are no longer refreshed.</p>
    </div>`;
  }

  // creditBlock: "Powered by GDTF". The logo is a labelled placeholder: the
  // official asset is to be supplied, never drawn (plan, G3).
  function creditBlock() {
    return `<div class="b5-gdtf-credit" aria-label="GDTF credit">
      <div class="b5-gdtf-logo-ph" role="img" aria-label="Placeholder: the official GDTF logo goes here">official GDTF logo goes here</div>
      <div><p class="b5-gdtf-credit__text">Powered by GDTF</p><p class="b5-caption">Fixture files come from GDTF Share (gdtf-share.com). Every file is labelled as uploaded by its manufacturer or by a user.</p></div>
    </div>`;
  }

  // --- paint -----------------------------------------------------------------

  function paint() {
    const root = document.getElementById('gdtfShareRoot');
    if (root) {
      if (!st) {
        root.innerHTML = `<div class="b5-gdtf-status" role="status" aria-label="GDTF Share status">${loadError ? UI.badge('error', 'Not read') + `<span class="b5-gdtf-status__msg">The GDTF library status could not be read: ${esc(loadError)}</span>` : UI.badge('pending', 'Reading') + '<span class="b5-gdtf-status__msg">Reading the GDTF library status…</span>'}</div>${creditBlock()}`;
      } else {
        const keep = captureInputs(root);
        root.innerHTML = [
          statusBlock(),
          st.enabled && st.state === 'refused' && !st.running ? refusalAlert() : '',
          routeBox(),
          st.enabled ? loginGroup() : '',
          st.enabled ? syncGroup() : '',
          st.enabled ? problemsGroup() : '',
          st.enabled ? manufacturerGroup() : '',
          creditBlock(),
        ].join('');
        restoreInputs(root, keep);
        wire(root);
      }
    }
    paintAbout();
  }

  // A re-paint while someone types (status polling) keeps what they typed
  // and where the caret was. The password is kept only in the field itself.
  function captureInputs(root) {
    const out = {};
    root.querySelectorAll('input').forEach(i => { out[i.id] = i.value; });
    const a = document.activeElement;
    out._focus = a && root.contains(a) ? (a.id || a.getAttribute('data-gdtf-focus-key') || '') : '';
    out._focusSel = a && root.contains(a) && !a.id ? selectorFor(a) : '';
    return out;
  }
  function selectorFor(el) {
    for (const k of ['data-gdtf-unfollow', 'data-gdtf-instead']) if (el.hasAttribute && el.hasAttribute(k)) return `[${k}="${typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(el.getAttribute(k)) : el.getAttribute(k)}"]`;
    for (const k of ['data-gdtf-test', 'data-gdtf-save', 'data-gdtf-change', 'data-gdtf-remove', 'data-gdtf-sync', 'data-gdtf-follow', 'data-gdtf-goto-nic', 'data-gdtf-cancel-change']) if (el.hasAttribute && el.hasAttribute(k)) return `[${k}]`;
    return '';
  }
  function restoreInputs(root, keep) {
    Object.keys(keep).forEach(id => {
      if (id[0] === '_') return;
      const el = root.querySelector('#' + id);
      if (el && keep[id]) el.value = keep[id];
    });
    let f = null;
    if (keep._focus) f = root.querySelector('#' + keep._focus);
    else if (keep._focusSel) f = root.querySelector(keep._focusSel);
    if (f && f.focus) f.focus({ preventScroll: true });
  }

  function paintAbout() {
    const root = document.getElementById('aboutRoot');
    if (!root) return;
    const v = version && !version.error ? version : null;
    const vWords = version && version.error ? `could not be read: ${esc(version.error)}` : !v ? 'reading…' : v.version === 'dev'
      ? `dev build (not a release) · commit ${esc(v.commit)} · built ${esc(v.built)}`
      : `${esc(v.version)} · commit ${esc(v.commit)} · built ${esc(v.built)}`;
    root.innerHTML = `<div class="b5-grid-2">
        ${param('Version', vWords)}
        ${param('Exactly what benny512.exe --version prints', v ? `<span class="b5-text-mono">${esc(v.line)}</span>` : '—')}
      </div>
      ${creditBlock()}`;
  }

  // --- actions (act at once) -------------------------------------------------

  async function act(key, fn, okWords) {
    if (busy) return;
    busy = true;
    actionMsg[key] = 'Working…';
    paint();
    try {
      const r = await fn();
      actionMsg[key] = typeof okWords === 'function' ? okWords(r) : okWords;
    } catch (e) {
      actionMsg[key] = e.message;
    } finally {
      busy = false;
      await refresh(true);
    }
  }

  function typedLogin() {
    const u = document.getElementById('gdtfUser'), p = document.getElementById('gdtfPass');
    return { user: u ? u.value.trim() : '', password: p ? p.value : '' };
  }
  function clearPassword() { const p = document.getElementById('gdtfPass'); if (p) p.value = ''; }

  function wire(root) {
    const on = (sel, fn) => root.querySelectorAll(sel).forEach(b => b.addEventListener('click', fn));
    on('[data-gdtf-goto-nic]', () => {
      const nic = document.getElementById('nicSelect');
      if (!nic) return;
      if (nic.scrollIntoView) nic.scrollIntoView({ block: 'center' });
      nic.focus();
    });
    on('[data-gdtf-test]', () => {
      const t = typedLogin();
      const showingFields = !!document.getElementById('gdtfPass');
      if (showingFields && (!t.user || !t.password)) { actionMsg.login = 'Enter both the user name and the password to test them.'; paint(); return; }
      act('login', () => showingFields ? Api.gdtfTestLogin(t.user, t.password) : Api.gdtfTestLogin(),
        r => r.ok ? 'GDTF Share accepted the login.' + (showingFields ? ' It is not saved yet.' : '') : `Not accepted: ${r.message}`);
    });
    on('[data-gdtf-save]', () => {
      const t = typedLogin();
      if (!t.user || !t.password) { actionMsg.login = 'Enter both the user name and the password.'; paint(); return; }
      clearPassword();
      changingLogin = false;
      act('login', async () => {
        await Api.gdtfSetLogin(t.user, t.password);
        // The first sync starts on its own (the screen promised so).
        try { await Api.gdtfSync(); fastUntil = Date.now() + 15000; return 'Saved. The first sync has started.'; } catch (e) { return `Saved. The sync did not start: ${e.message}`; }
      }, r => r);
    });
    on('[data-gdtf-change]', () => { changingLogin = true; actionMsg.login = ''; paint(); const u = document.getElementById('gdtfUser'); if (u) u.focus(); });
    on('[data-gdtf-cancel-change]', () => { changingLogin = false; actionMsg.login = ''; paint(); });
    on('[data-gdtf-remove]', () => act('login', () => Api.gdtfClearLogin(), 'Login removed. The files on this computer stay.'));
    on('[data-gdtf-sync]', e => {
      if (e.currentTarget.getAttribute('aria-disabled') === 'true') return;
      act('sync', async () => { await Api.gdtfSync(); fastUntil = Date.now() + 15000; }, 'Sync started.');
    });
    on('[data-gdtf-follow]', () => {
      const el = document.getElementById('gdtfAddMfr');
      const name = el ? el.value.trim() : '';
      if (!name) { actionMsg.follow = 'Type a manufacturer name first.'; paint(); return; }
      if (el) el.value = '';
      act('follow', () => Api.gdtfSetManufacturers((st.followed || []).concat([name])), `Now following “${name}”. It is looked up at the next sync.`);
    });
    on('[data-gdtf-unfollow]', e => {
      const name = e.currentTarget.getAttribute('data-gdtf-unfollow');
      act('follow', () => Api.gdtfSetManufacturers((st.followed || []).filter(n => n !== name)), `Stopped following “${name}”. Its files stay on this computer.`);
    });
    on('[data-gdtf-instead]', e => {
      const from = e.currentTarget.getAttribute('data-gdtf-instead'), to = e.currentTarget.getAttribute('data-gdtf-to');
      act('follow', () => Api.gdtfSetManufacturers((st.followed || []).map(n => n === from ? to : n)), `Now following “${to}” instead of “${from}”. It is looked up at the next sync.`);
    });
    const add = root.querySelector('#gdtfAddMfr');
    if (add) add.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); root.querySelector('[data-gdtf-follow]').click(); } });
  }

  // --- refresh ---------------------------------------------------------------

  function screenActive() {
    const s = document.getElementById('screen-settings');
    return !!(s && s.classList && s.classList.contains('active'));
  }

  // While a sync runs the status is read every 1.5 s; otherwise every 20 s
  // while Settings is the open screen, so a scheduled sync shows up.
  function schedule() {
    clearTimeout(timer);
    const ms = (st && st.running) || Date.now() < fastUntil ? 1500 : 20000;
    timer = setTimeout(() => { if (screenActive() || (st && st.running)) refresh(); else schedule(); }, ms);
  }

  async function refresh(force) {
    if (busy && !force) { schedule(); return; }
    try {
      st = await Api.gdtfStatus();
      loadError = '';
    } catch (e) {
      loadError = e.message;
    }
    paint();
    schedule();
  }

  async function init() {
    paint();
    try { version = await Api.getVersion(); } catch (e) { version = { error: e.message }; }
    await refresh();
  }

  return { init, refresh };
})();
