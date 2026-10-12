// theme-settings.js — the Theme section of the Settings screen (C9): the
// per-browser choice (Dark, Daylight, or a custom theme) and the custom
// theme editor.
//
// The theme is a property of THIS BROWSER, not of the station, so unlike
// every other Settings field it is not staged for Apply: choosing applies it
// at once and remembers it in this browser (js/theme.js). Custom themes
// themselves are station settings (GET/POST/DELETE /api/themes) so every
// browser can choose them; the server validates every save against the
// theme contract and never trusts this page's own check.
//
// THE EDITOR edits the accepted colour roles only — opaque #rrggbb — and the
// bounded live-preview intensity (theme-contract.md "Non-themeable
// structure": no sizes, borders, glyphs or words). Every change is checked
// at once with B5Theme.validate (the same contract as the server, tested to
// agree) and previewed on this page only. A draft that fails is labelled
// "Theme needs changes", lists each failed pair in words, and cannot be
// saved; Revert is in a box that keeps this browser's own theme, so it stays
// readable whatever the draft does. Leaving Settings ends the preview.
const ThemeSettings = (() => {
  let info = null;        // GET /api/themes
  let loadError = '';
  let draft = null;       // { id, name, base, colors, preview } being edited
  let opened = null;      // the draft as the editor opened it (Revert target)
  let previewing = false;

  const GROUPS = [
    ['Backgrounds', r => r.indexOf('bg-') === 0],
    ['Text', r => r.indexOf('text-') === 0],
    ['Borders and focus', r => r.indexOf('border-') === 0],
    ['Tones', r => /^(ok|warn|danger|accent|info)-/.test(r)],
    ['Output state', r => /^(armed-|disarm-|arm-pill-|simulated-|lease-lost-)/.test(r)],
    ['Fixtures', r => r.indexOf('fixture-') === 0],
    ['Values', r => r.indexOf('value-') === 0],
    ['Grid, faders and XY pad', () => true],
  ];
  const esc = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $ = id => document.getElementById(id);
  const label = r => (info && info.labels && info.labels[r]) || r;
  const themes = () => (info && info.themes) || [];
  const byId = id => themes().find(t => t.id === id) || null;
  const baseWord = b => (b === 'light' ? 'Daylight' : 'Dark');
  const plain = t => ({ id: t.id, name: t.name, base: t.base, colors: t.colors, preview: t.preview });

  // --- the choice -----------------------------------------------------------
  function daylightNote() {
    const el = $('themeDaylightNote');
    if (!el) return;
    const c = B5Theme.currentCustom();
    const light = B5Theme.current() === 'light' || (c && c.base === 'light');
    el.innerHTML = light ? `
      <div class="b5-alert b5-alert--warning" role="note">
        ${UI.icon('status-warning')}
        <div>
          <p class="b5-alert__title">Daylight is not for dark-venue checking</p>
          <p class="b5-alert__body">It lights the whole screen brightly, for daylight and outdoor work. Switch this browser back to Dark before checking a rig in a dark venue.</p>
        </div>
      </div>` : '';
  }

  function setStatus(text) { const s = $('themeStatus'); if (s) s.textContent = text; }

  function fillSelect() {
    const sel = $('themeSelect');
    if (!sel) return;
    sel.innerHTML = `<option value="dark">Dark (default)</option><option value="light">Daylight &mdash; not for dark-venue checking</option>` +
      themes().map(t => {
        const bad = t.failures && t.failures.length;
        return `<option value="custom:${esc(t.id)}"${bad ? ' disabled' : ''}>${esc(t.name)} &mdash; custom, on ${baseWord(t.base)}${t.base === 'light' ? ', not for dark-venue checking' : ''}${bad ? ' (needs changes)' : ''}</option>`;
      }).join('');
    sel.value = B5Theme.current();
    const custom = sel.value.indexOf('custom:') === 0;
    ['data-theme-edit', 'data-theme-delete', 'data-theme-export'].forEach(a => {
      const b = document.querySelector('[' + a + ']');
      if (b) b.disabled = !custom;
    });
  }

  function onChoose() {
    const sel = $('themeSelect');
    const v = sel.value;
    let remembered;
    if (v.indexOf('custom:') === 0) {
      const t = byId(v.slice(7));
      if (!t || (t.failures && t.failures.length)) { fillSelect(); return; }
      remembered = B5Theme.choose(v, plain(t));
    } else {
      remembered = B5Theme.choose(v);
    }
    setStatus(remembered ? '' : 'Applied to this page, but this browser would not store it: the next load starts on Dark.');
    fillSelect();
    daylightNote();
  }

  async function refresh() {
    try {
      info = await Api.getThemes();
      loadError = '';
    } catch (e) {
      loadError = e.message;
    }
    const what = info ? B5Theme.sync(info.themes) : null;
    if (what === 'deleted') setStatus('The custom theme this browser used was deleted on this station; this browser is back on Dark.');
    if (what === 'invalid') setStatus('The custom theme this browser used now needs changes (it no longer passes the contract); this browser is back on Dark.');
    const note = $('themeLoadError');
    if (note) note.textContent = loadError ? 'Custom themes could not be read: ' + loadError : '';
    fillSelect();
    daylightNote();
  }

  // --- the editor ---------------------------------------------------------
  function editorRows() {
    const roles = info.roles.slice();
    const taken = {};
    return GROUPS.map(([title, test]) => {
      const rows = roles.filter(r => !taken[r] && test(r));
      rows.forEach(r => { taken[r] = true; });
      if (!rows.length) return '';
      return `<details class="b5-theme-group" data-theme-group="${esc(title)}"><summary>${esc(title)} <span class="b5-text-muted b5-text-sm" data-theme-group-count></span></summary>
        ${rows.map(r => `<div class="b5-theme-row" data-theme-row="${r}">
          <label class="b5-theme-row__label" for="themeHex-${r}">${esc(label(r))}</label>
          <input type="color" data-role="${r}" aria-label="${esc(label(r))} colour">
          <input type="text" id="themeHex-${r}" class="b5-input b5-input--mono" data-role-hex="${r}" spellcheck="false" autocomplete="off" maxlength="7">
          <span class="b5-theme-row__fail" data-role-fail="${r}"></span>
        </div>`).join('')}
      </details>`;
    }).join('');
  }

  // The verdict box keeps THIS BROWSER's theme: its roles are pinned on it
  // as custom properties, so a draft can never make Revert unreadable.
  function pinVerdict() {
    const box = document.querySelector('[data-theme-verdict]');
    if (!box || !info) return;
    const c = B5Theme.currentCustom();
    const base = c ? c.base : (B5Theme.current() === 'light' ? 'light' : 'dark');
    const values = Object.assign({}, info.bases[base], c ? c.colors : {});
    Object.keys(values).forEach(r => box.style.setProperty('--b5-color-' + r, values[r]));
  }

  function openEditor(d) {
    if (!info) return;
    draft = JSON.parse(JSON.stringify(d));
    opened = JSON.parse(JSON.stringify(d));
    const ed = document.querySelector('[data-theme-editor]');
    ed.hidden = false;
    ed.innerHTML = `
      <h3 class="b5-group__head">${d.id ? 'Edit custom theme' : 'New custom theme'}</h3>
      <div class="b5-theme-verdict" data-theme-verdict role="status" aria-live="polite">
        <p class="b5-theme-verdict__word" data-theme-verdict-word></p>
        <ul class="b5-theme-failures" data-theme-failures></ul>
        <div class="b5-row">
          <button type="button" class="b5-btn b5-btn--primary" data-theme-save>${UI.icon('apply')}Save theme</button>
          <button type="button" class="b5-btn" data-theme-revert>${UI.icon('revert')}Revert</button>
          <button type="button" class="b5-btn b5-btn--ghost" data-theme-close>Close editor</button>
        </div>
        <p class="b5-text-sm" data-theme-save-status></p>
      </div>
      <p class="b5-caption">What you change shows on this page at once, for this browser only, until you save or revert. Leaving Settings ends the preview.</p>
      <div class="b5-field">
        <label class="b5-field__label" for="themeName">Name</label>
        <input id="themeName" class="b5-input" type="text" maxlength="40" autocomplete="off">
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="themeBase">Start from</label>
        <select id="themeBase" class="b5-select">
          <option value="dark">Dark</option>
          <option value="light">Daylight &mdash; not for dark-venue checking</option>
        </select>
        <span class="b5-field__hint">Changing this starts every colour again from that theme. A theme on Dark must also keep the dark-venue brightness limits.</span>
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="themePreview">Live-preview intensity</label>
        <input id="themePreview" class="b5-input b5-input--mono" type="number" step="0.01" min="0.01">
        <span class="b5-field__hint" data-theme-preview-hint></span>
      </div>
      <p class="b5-caption">Only colours can change here. Sizes, borders, glyphs, words and the state shapes stay as they are in every theme, so ARMED / DISARMED and every state word stay readable in greyscale.</p>
      ${editorRows()}
      <div class="b5-inset">
        <p class="b5-inset__head">${UI.icon('status-pending')}What these checks can and cannot say</p>
        <p class="b5-note">The checks are arithmetic on the colour values: every pair against every surface it may sit on (unrounded WCAG contrast), the dark-venue brightness limits for a theme on Dark, and that the tone outlines stay apart (OKLab difference at least 0.04, never the same colour). They do not certify a screen for a dark venue: it is impossible to certify dark adaptation from a CSS multiplier or screenshot; device black level and backlight still require physical review.</p>
      </div>`;
    pinVerdict();
    ed.querySelector('[data-theme-save]').addEventListener('click', save);
    ed.querySelector('[data-theme-revert]').addEventListener('click', revert);
    ed.querySelector('[data-theme-close]').addEventListener('click', closeEditor);
    $('themeName').addEventListener('input', () => { draft.name = $('themeName').value; update(false); });
    $('themeBase').addEventListener('change', () => {
      const b = $('themeBase').value;
      draft.base = b;
      draft.colors = Object.assign({}, info.bases[b]);
      if (b === 'dark' && !(draft.preview <= info.previewMax.dark)) draft.preview = info.preview;
      fillEditor();
      update(true);
    });
    $('themePreview').addEventListener('input', () => { draft.preview = parseFloat($('themePreview').value); update(true); });
    ed.querySelectorAll('[data-role]').forEach(inp => inp.addEventListener('input', () => {
      const r = inp.getAttribute('data-role');
      draft.colors[r] = String(inp.value).toLowerCase();
      const hex = ed.querySelector('[data-role-hex="' + r + '"]');
      if (hex) hex.value = draft.colors[r];
      update(true);
    }));
    ed.querySelectorAll('[data-role-hex]').forEach(inp => inp.addEventListener('input', () => {
      const r = inp.getAttribute('data-role-hex');
      draft.colors[r] = String(inp.value).trim().toLowerCase();
      const col = ed.querySelector('[data-role="' + r + '"]');
      if (col && /^#[0-9a-f]{6}$/.test(draft.colors[r])) col.value = draft.colors[r];
      update(true);
    }));
    fillEditor();
    update(false);
    if (ed.scrollIntoView) ed.scrollIntoView({ block: 'start' });
  }

  function fillEditor() {
    const ed = document.querySelector('[data-theme-editor]');
    $('themeName').value = draft.name || '';
    $('themeBase').value = draft.base;
    $('themePreview').value = String(draft.preview);
    $('themePreview').max = String(info.previewMax[draft.base]);
    ed.querySelector('[data-theme-preview-hint]').textContent = draft.base === 'dark'
      ? `How bright colour previews (the colour picker's swatches) may be: more than 0, at most ${info.previewMax.dark} on Dark, and never brighter than overlay. Default ${info.preview}.`
      : `How bright colour previews (the colour picker's swatches) may be: more than 0, at most ${info.previewMax.light}. Default ${info.preview}.`;
    info.roles.forEach(r => {
      const v = draft.colors[r] || '';
      const col = ed.querySelector('[data-role="' + r + '"]');
      const hex = ed.querySelector('[data-role-hex="' + r + '"]');
      if (col && /^#[0-9a-f]{6}$/.test(v)) col.value = v;
      if (hex) hex.value = v;
    });
  }

  function failures() {
    const f = B5Theme.validate(draft.base, draft.colors, draft.preview, info.roles, info.labels);
    if (!String(draft.name || '').trim()) f.unshift({ kind: 'name', role: '', against: '', message: 'Name the theme before saving it.' });
    return f;
  }

  function update(repaint) {
    const ed = document.querySelector('[data-theme-editor]');
    if (!ed || !draft) return;
    const f = failures();
    const contractFails = f.filter(x => x.kind !== 'name');
    const word = ed.querySelector('[data-theme-verdict-word]');
    const box = ed.querySelector('[data-theme-verdict]');
    box.classList.toggle('is-fail', contractFails.length > 0);
    word.innerHTML = contractFails.length
      ? `${UI.icon('status-warning')}<strong>Theme needs changes</strong> &mdash; ${contractFails.length} check${contractFails.length === 1 ? '' : 's'} failed. Save is blocked until every pair passes.`
      : `${UI.icon('status-ok')}<strong>Every pair passes</strong>${f.length ? ' &mdash; name the theme to save it.' : '.'}`;
    ed.querySelector('[data-theme-failures]').innerHTML = f.map(x => `<li>${esc(x.message)}</li>`).join('');
    const save = ed.querySelector('[data-theme-save]');
    save.disabled = f.length > 0;
    // Per-row markers, in words.
    const hit = {};
    contractFails.forEach(x => { if (x.role) hit[x.role] = (hit[x.role] || 0) + 1; if (x.against) hit[x.against] = (hit[x.against] || 0) + 1; });
    ed.querySelectorAll('[data-role-fail]').forEach(el => {
      const r = el.getAttribute('data-role-fail');
      el.textContent = hit[r] ? `in ${hit[r]} failed check${hit[r] === 1 ? '' : 's'}` : '';
      const row = el.parentNode;
      if (row && row.classList) row.classList.toggle('is-fail', !!hit[r]);
    });
    ed.querySelectorAll('[data-theme-group]').forEach(g => {
      const n = g.querySelectorAll('.b5-theme-row.is-fail').length;
      const c = g.querySelector('[data-theme-group-count]');
      if (c) c.textContent = n ? `· ${n} need${n === 1 ? 's' : ''} changes` : '';
    });
    if (repaint) {
      // Preview what can be painted: only well-formed colours.
      const ok = {};
      Object.keys(draft.colors).forEach(r => { if (/^#[0-9a-f]{6}$/.test(draft.colors[r])) ok[r] = draft.colors[r]; });
      B5Theme.preview({ base: draft.base, colors: ok, preview: draft.preview > 0 ? draft.preview : undefined });
      previewing = true;
    }
  }

  function endPreview() {
    if (previewing) { B5Theme.endPreview(); previewing = false; }
  }

  function revert() {
    if (!opened) return;
    draft = JSON.parse(JSON.stringify(opened));
    endPreview();
    fillEditor();
    update(false);
    const st = document.querySelector('[data-theme-save-status]');
    if (st) st.textContent = 'Reverted: this page shows this browser\'s theme again.';
  }

  function closeEditor() {
    endPreview();
    draft = null;
    opened = null;
    const ed = document.querySelector('[data-theme-editor]');
    if (ed) { ed.hidden = true; ed.innerHTML = ''; }
  }

  async function save() {
    if (!draft) return;
    const f = failures();
    const st = document.querySelector('[data-theme-save-status]');
    if (f.length) { if (st) st.textContent = 'Not saved: ' + f[0].message; return; }
    const body = { id: draft.id || '', name: draft.name.trim(), base: draft.base, colors: draft.colors, preview: draft.preview };
    if (st) st.textContent = 'Saving…';
    try {
      const saved = await Api.saveTheme(body);
      previewing = false;
      B5Theme.choose('custom:' + saved.id, plain(saved));
      closeEditor();
      await refresh();
      setStatus(`Saved “${saved.name}” on this station. This browser now uses it; other browsers can choose it here.`);
    } catch (e) {
      const list = e.data && e.data.failures;
      if (st) st.textContent = 'Not saved: ' + e.message;
      if (list && list.length) {
        const ul = document.querySelector('[data-theme-failures]');
        if (ul) ul.innerHTML = list.map(x => `<li>${esc(x.message)}</li>`).join('');
      }
    }
  }

  async function del() {
    const v = $('themeSelect').value;
    const t = v.indexOf('custom:') === 0 ? byId(v.slice(7)) : null;
    if (!t) return;
    const yes = await UI.ask({ title: `Delete the theme “${t.name}”?`, body: 'It is removed from this station. A browser using it goes back to Dark the next time it opens Settings or reloads.', ok: 'Delete theme', danger: true });
    if (yes !== true) return;
    try {
      await Api.deleteTheme(t.id);
      if (B5Theme.current() === 'custom:' + t.id) B5Theme.choose('dark');
      if (draft && draft.id === t.id) closeEditor();
      await refresh();
      setStatus(`Deleted “${t.name}”.`);
    } catch (e) {
      setStatus('Not deleted: ' + e.message);
    }
  }

  // --- export / import (a small JSON file) ---------------------------------
  function exportTheme() {
    const v = $('themeSelect').value;
    const t = v.indexOf('custom:') === 0 ? byId(v.slice(7)) : null;
    if (!t) return;
    const text = JSON.stringify({ benny512Theme: 1, name: t.name, base: t.base, colors: t.colors, preview: t.preview }, null, 2);
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
    a.download = t.name.replace(/[^\w.-]+/g, '_') + '.benny512-theme.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  async function importFile(file) {
    let d;
    try {
      d = JSON.parse(await file.text());
    } catch (e) {
      setStatus('Not imported: the file is not JSON.');
      return;
    }
    if (!d || d.benny512Theme !== 1 || (d.base !== 'dark' && d.base !== 'light') || !d.colors || typeof d.colors !== 'object') {
      setStatus('Not imported: this is not a Benny512 theme file.');
      return;
    }
    const colors = {};
    Object.keys(d.colors).forEach(k => { colors[k] = String(d.colors[k]).trim().toLowerCase(); });
    openEditor({ id: '', name: String(d.name || '').slice(0, 40), base: d.base, colors, preview: typeof d.preview === 'number' ? d.preview : info.preview });
    setStatus('Imported into the editor. Check it, then save it to this station.');
  }

  function render() {
    const root = $('themeSettingsRoot');
    if (!root) return;
    root.innerHTML = `
      <div class="b5-field">
        <label class="b5-field__label" for="themeSelect">Theme for this browser</label>
        <select id="themeSelect" class="b5-select"></select>
        <span class="b5-field__hint">Applies at once, to this browser only &mdash; it is not part of Apply settings. Each phone, tablet or laptop keeps its own choice, stored in that browser, so a phone outdoors can use Daylight while the laptop at front of house stays Dark. A browser that cannot store it (a private window, blocked site data) starts on Dark.</span>
      </div>
      <div id="themeDaylightNote"></div>
      <p class="b5-text-muted b5-text-sm" id="themeStatus" role="status"></p>
      <div class="b5-group">
        <h3 class="b5-group__head">Custom themes</h3>
        <p class="b5-caption">Saved on this station, so every browser can choose them above. Choosing one changes only that browser. A custom theme changes colours only, and is saved only when every pair passes.</p>
        <div class="b5-row b5-theme-actions">
          <button type="button" class="b5-btn" data-theme-new>${UI.icon('apply')}New custom theme</button>
          <button type="button" class="b5-btn" data-theme-edit disabled>Edit</button>
          <button type="button" class="b5-btn b5-btn--danger" data-theme-delete disabled>Delete&hellip;</button>
          <button type="button" class="b5-btn" data-theme-export disabled>Export</button>
          <button type="button" class="b5-btn" data-theme-import>Import&hellip;</button>
          <input type="file" accept=".json,application/json" data-theme-file hidden>
        </div>
        <p class="b5-text-sm" id="themeLoadError" role="alert"></p>
        <div class="b5-theme-editor" data-theme-editor hidden></div>
      </div>`;
    $('themeSelect').addEventListener('change', onChoose);
    root.querySelector('[data-theme-new]').addEventListener('click', () => {
      if (!info) return;
      const c = B5Theme.currentCustom();
      const base = c ? c.base : (B5Theme.current() === 'light' ? 'light' : 'dark');
      openEditor({ id: '', name: '', base, colors: Object.assign({}, info.bases[base]), preview: info.preview });
    });
    root.querySelector('[data-theme-edit]').addEventListener('click', () => {
      const v = $('themeSelect').value;
      const t = v.indexOf('custom:') === 0 ? byId(v.slice(7)) : null;
      if (t) openEditor(plain(t));
    });
    root.querySelector('[data-theme-delete]').addEventListener('click', del);
    root.querySelector('[data-theme-export]').addEventListener('click', exportTheme);
    const file = root.querySelector('[data-theme-file]');
    root.querySelector('[data-theme-import]').addEventListener('click', () => file.click());
    file.addEventListener('change', () => { if (file.files && file.files[0]) importFile(file.files[0]); file.value = ''; });
    fillSelect();
    daylightNote();
  }

  function init() {
    render();
    refresh();
  }
  // Leaving Settings ends a preview (app.js); the draft stays in the editor.
  function onLeaveScreen() { endPreview(); }
  return { init, refresh, onLeaveScreen, importFile };
})();
