// console.js — the Console-lite screen, chunk C6a: the screen shell, the
// SELECTION GRID and LAYOUT EDIT MODE. Attribute controls are chunk C6b; this
// file leaves a marked, empty controls region for them.
//
// WHAT IT SHOWS. GET /api/patch/layout (layout.go): the show's 2D plan in
// Z layers. Every patch entry is on it — placed by hand, derived from MVR
// positions, or automatically in the system "Unplaced" layer — so every
// fixture is selectable. Groups can be placed as one tile. Objects (truss,
// labels, areas, reference marks) are drawn but are never selectable.
//
// SELECTION. Every selection gesture is one POST /api/programmer/select
// through ProgrammerSync (programmer.js): one programmer per station, shared
// by every browser, so the grid always draws the SERVER's selection — this
// browser's taps and every other browser's arrive the same way, through the
// programmer revision refresh. Tap = select only that; the Add toggle (no
// modifier keys needed with gloves) or Shift/Ctrl/Cmd+tap = toggle in or out.
// Selection order is kept by the server and drawn as a number on each tile
// (fan uses it). Multi-cell fixtures: the tile selects the parent; its cell
// strip selects one cell (directly when the cells fit at the current zoom,
// otherwise through a cell picker with full-size buttons).
//
// EDIT MODE is an explicit toggle, OFF by default, so a gloved tap never
// moves a fixture. While it is on, taps pick an item to edit instead of
// selecting, drags move items (pointer events: mouse, pen and touch), arrow
// keys nudge the focused item, and the edit panel offers resize/rotate, pin
// and reset-to-auto, layer moves, derive/refresh from MVR positions, layers
// and objects. Every edit is one POST /api/patch/layout/{action} whose
// answer is the whole new layout. Layout edits change no DMX.
//
// Leaving this screen changes nothing on the wire (C3 rule): output, the
// programmer and the selection all carry on.
const ConsoleScreen = (() => {
  const ZOOMS = [64, 96, 128, 160];   // px per grid cell
  const DRAG_SLOP = 8;                // px of travel before a press is a drag
  const st = {
    built: false, active: false, stale: true, loading: false,
    layout: null,          // last GET/POST /api/patch/layout body
    cells: {},             // entryId -> [{id, name, index}] (GET /api/programmer/fixtures)
    view: 'all',           // 'all' or a layer id
    zoom: 1,               // index into ZOOMS
    addMode: false,
    edit: false,
    editId: null,          // the item the edit panel works on
    picker: null,          // entryId whose cell picker is open
  };
  const els = {};
  let drag = null;

  // --- small DOM helpers ------------------------------------------------------
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    if (props) {
      for (const k of Object.keys(props)) {
        const v = props[k];
        if (v === undefined || v === null || v === false) continue;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = String(v);
        else if (k === 'icon') el.innerHTML = UI.icon(v);
        else if (k === 'style') Object.keys(v).forEach(s => el.style.setProperty(s, v[s]));
        else if (k.slice(0, 2) === 'on') el.addEventListener(k.slice(2), v);
        else el.setAttribute(k, v === true ? '' : String(v));
      }
    }
    append(el, kids);
    return el;
  }
  function append(el, kids) {
    for (const c of [].concat(...kids)) {
      if (c === null || c === undefined || c === false) continue;
      el.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return el;
  }
  function ic(name) { return h('span', { class: 'b5-con-ic', 'aria-hidden': 'true', icon: name }); }
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); }
  function btn(label, opts, onclick) {
    opts = opts || {};
    const b = h('button', Object.assign({ type: 'button', class: 'b5-btn' + (opts.cls ? ' ' + opts.cls : '') }, opts.attrs || {}),
      opts.icon ? ic(opts.icon) : null, label);
    if (opts.disabled) b.disabled = true;
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function seg(label, on, attrs, onclick) {
    const b = h('button', Object.assign({ type: 'button', class: 'b5-seg' + (on ? ' is-on' : ''), 'aria-pressed': on ? 'true' : 'false' }, attrs || {}), label);
    if (onclick) b.addEventListener('click', onclick);
    return b;
  }
  function touchMin() {
    try {
      const v = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--b5-size-touch-min'));
      if (v > 0) return v;
    } catch (_) { /* no computed style (tests): fall through */ }
    return 44;
  }
  function cellPx() { return ZOOMS[st.zoom]; }

  // --- in-app dialog (native prompt() fails in the embedded browser) --------
  // ask({title, body, field:{label, value, maxLength}, ok, danger}) resolves
  // to the trimmed field text, true for a plain confirm, or null on cancel.
  function ask(o) {
    return new Promise(resolve => {
      let done = false;
      const input = o.field ? h('input', { class: 'b5-input', type: 'text', maxlength: o.field.maxLength || 80, 'data-ask-input': '' }) : null;
      if (input) input.value = o.field.value || '';
      const err = h('p', { class: 'b5-field__error', role: 'alert', 'data-ask-error': '' });
      const okBtn = btn(o.ok || 'Confirm', { cls: o.danger ? 'b5-btn--danger' : 'b5-btn--primary', attrs: { 'data-ask-ok': '' } });
      const cancelBtn = btn('Cancel', { attrs: { 'data-ask-cancel': '' } });
      const dlg = h('dialog', { class: 'b5-workspace-dialog b5-con-dialog', 'data-ask': '', 'aria-label': o.title },
        h('h3', { text: o.title }),
        o.body ? h('p', { class: 'b5-note', text: o.body }) : null,
        input ? h('label', { class: 'b5-field' }, h('span', { class: 'b5-field__label', text: o.field.label }), input) : null,
        err,
        h('div', { class: 'b5-row b5-con-dialog__actions' }, cancelBtn, okBtn));
      const finish = v => {
        if (done) return;
        done = true;
        try { if (dlg.open) dlg.close(); } catch (_) { /* already closed */ }
        dlg.remove();
        resolve(v);
      };
      okBtn.addEventListener('click', () => {
        if (!input) return finish(true);
        const v = input.value.trim();
        if (!v) { err.textContent = 'Type ' + o.field.label.toLowerCase() + ' first.'; return; }
        finish(v);
      });
      cancelBtn.addEventListener('click', () => finish(null));
      dlg.addEventListener('cancel', ev => { ev.preventDefault(); finish(null); });
      if (input) input.addEventListener('keydown', ev => { if (ev.key === 'Enter') { ev.preventDefault(); okBtn.click(); } });
      document.body.appendChild(dlg);
      dlg.showModal();
      (input || okBtn).focus();
    });
  }

  // --- status line ------------------------------------------------------------
  function status(msg, tone) {
    if (!els.status) return;
    clear(els.status);
    els.status.className = 'b5-con-status' + (tone ? ' b5-con-status--' + tone : '');
    if (!msg) return;
    if (tone === 'error') append(els.status, [ic('status-error'), h('strong', { text: 'Not done: ' }), msg]);
    else append(els.status, [ic('status-ok'), msg]);
  }

  // --- derived data -----------------------------------------------------------
  function prog() { return (typeof ProgrammerSync !== 'undefined' && ProgrammerSync.state()) || null; }
  function selection() { const p = prog(); return (p && p.selection) || []; }
  const key = (entryId, cell) => entryId + '\u0000' + (cell || '');
  function selOrder() {
    const m = new Map();
    selection().forEach((s, i) => m.set(key(s.entryId, s.cell), i + 1));
    return m;
  }
  function layers() { return (st.layout && st.layout.layout.layers) || []; }
  function items() { return (st.layout && st.layout.layout.items) || []; }
  function entryById(id) { return ((st.layout && st.layout.entries) || []).find(e => e.id === id) || null; }
  function itemById(id) { return items().find(it => it.id === id) || null; }
  function layerById(id) { return layers().find(l => l.id === id) || null; }
  function storedGroups() {
    const p = prog();
    if (p && p.storedGroups) return p.storedGroups;
    return (st.layout && st.layout.groups) || [];
  }
  function groupById(id) { return storedGroups().find(g => g.id === id) || null; }
  function groupMembers(g) {
    if (!g) return [];
    if (g.members && g.members.length) return g.members;
    return (g.entryIds || []).map(id => ({ entryId: id, cell: '' }));
  }
  function staleIds() { return new Set(((st.layout && st.layout.stale) || []).map(s => s.itemId)); }
  function virtualIds() { return new Set(((st.layout && st.layout.unplaced) || []).map(u => u.itemId).filter(Boolean)); }
  function unplacedReason(entryId) {
    const u = ((st.layout && st.layout.unplaced) || []).find(x => x.entryId === entryId);
    return u ? u.reason : '';
  }
  function itemName(it) {
    if (!it) return '';
    if (it.kind === 'entry') {
      const e = entryById(it.ref);
      return e ? (e.name || 'Unnamed fixture') : 'Fixture no longer in the patch';
    }
    if (it.kind === 'group') {
      const g = groupById(it.ref);
      return g ? g.name : 'Group no longer stored';
    }
    const t = objectType(it.objectType);
    return (t ? t.label : it.objectType) + (it.text ? ' · ' + it.text : '');
  }
  function objectType(name) { return ((st.layout && st.layout.objectTypes) || []).find(t => t.type === name) || null; }
  // The layer new things land on: the shown layer, else the first one.
  function targetLayer() {
    if (st.view !== 'all' && layerById(st.view)) return st.view;
    return layers().length ? layers()[0].id : '';
  }
  // bounds of the items on one layer, in cells (objects by the cells their
  // geometry covers). colFrom, when given, is a column origin shared by every
  // layer shown, so columns line up from one layer to the next.
  function bounds(list, colFrom) {
    if (!list.length) return { c0: colFrom === undefined ? 0 : colFrom, r0: 0, c1: (colFrom || 0) + 4, r1: 1 };
    let c0 = Infinity, r0 = Infinity, c1 = -Infinity, r1 = -Infinity;
    for (const it of list) {
      c0 = Math.min(c0, it.col); r0 = Math.min(r0, it.row);
      c1 = Math.max(c1, it.col + it.w); r1 = Math.max(r1, it.row + it.h);
    }
    if (colFrom !== undefined) c0 = Math.min(c0, colFrom);
    return { c0, r0, c1: Math.max(c1, c0 + 4), r1 };
  }
  function fmtM(mm) { return (mm / 1000).toFixed(1) + ' m'; }

  // --- building the screen ----------------------------------------------------
  function build() {
    const root = document.getElementById('consoleRoot');
    if (!root || st.built) return;
    st.built = true;
    els.root = root;
    els.layers = h('div', { class: 'b5-con-chips', role: 'group', 'aria-label': 'Layers shown' });
    els.groups = h('div', { class: 'b5-con-chips', role: 'group', 'aria-label': 'Stored groups' });
    els.add = seg('', false, { 'data-add-mode': '', title: 'Add mode: taps add to the selection or take out of it' }, () => { st.addMode = !st.addMode; renderTools(); });
    els.selAll = btn('Select all', { cls: 'b5-btn--sm', attrs: { 'data-select-all': '' } }, () => selectAll());
    els.selNone = btn('Select none', { cls: 'b5-btn--sm', attrs: { 'data-select-none': '' } }, () => sel({ action: 'none' }));
    els.zoomOut = btn('Zoom out', { cls: 'b5-btn--sm', attrs: { 'data-zoom-out': '' } }, () => zoom(-1));
    els.zoomIn = btn('Zoom in', { cls: 'b5-btn--sm', attrs: { 'data-zoom-in': '' } }, () => zoom(1));
    els.reload = btn('Reload layout', { cls: 'b5-btn--sm', icon: 'refresh', attrs: { 'data-reload': '' } }, () => load());
    els.editToggle = seg('', false, { 'data-edit-toggle': '' }, () => setEdit(!st.edit));
    els.status = h('p', { class: 'b5-con-status', role: 'status', 'data-status': '' });
    els.modebar = h('div', { class: 'b5-modebar b5-con-modebar', 'data-modebar': '', hidden: true },
      ic('status-warning'),
      h('span', { class: 'b5-modebar__label', text: 'Editing the layout — taps pick an item to edit and drags move it. Nothing is selected and no light moves.' }),
      btn('Done editing', { cls: 'b5-btn--primary', attrs: { 'data-edit-done': '' } }, () => setEdit(false)));
    els.grid = h('div', { class: 'b5-con-gridbox', 'data-gridbox': '', tabindex: '-1' });
    els.picker = h('div', { class: 'b5-con-picker', 'data-cellpicker': '', hidden: true });
    els.edit = h('div', { class: 'b5-con-edit', 'data-editpanel': '', hidden: true });
    els.summary = h('section', { class: 'b5-actionbar b5-con-summary', 'aria-label': 'Selection', 'data-summary': '' });
    append(root, [
      h('div', { class: 'b5-page-header' },
        h('h1', { class: 'b5-page-header__title', text: 'Console' }),
        h('span', { class: 'b5-page-header__meta b5-caption', text: 'Select fixtures on the plan. Leaving this screen keeps output, the programmer and the selection as they are.' })),
      h('section', { class: 'b5-toolbar b5-con-toolbar', 'aria-label': 'Selection tools' },
        h('div', { class: 'b5-toolbar__row b5-con-row' }, h('span', { class: 'b5-con-rowlabel', text: 'Layers' }), els.layers),
        h('div', { class: 'b5-toolbar__row b5-con-row' }, els.add, els.selAll, els.selNone,
          h('span', { class: 'b5-con-sep', 'aria-hidden': 'true' }), els.zoomOut, els.zoomIn, els.reload, els.editToggle),
        h('div', { class: 'b5-toolbar__row b5-con-row' }, h('span', { class: 'b5-con-rowlabel', text: 'Groups' }), els.groups)),
      els.status,
      els.modebar,
      h('div', { class: 'b5-con-main' },
        h('section', { class: 'b5-con-pane b5-con-pane--grid', 'aria-labelledby': 'conGridHead' },
          h('h2', { class: 'b5-board__head', id: 'conGridHead' },
            h('span', { class: 'b5-board__title', text: 'Selection grid' }),
            h('span', { class: 'b5-board__sub', text: 'the rig seen from above, one plan per height layer' })),
          els.grid, els.picker),
        h('section', { class: 'b5-con-pane b5-con-pane--side', 'aria-label': 'Layout editing and controls' },
          els.edit,
          h('div', { class: 'b5-con-controls', 'data-controls-region': '' },
            h('h2', { class: 'b5-board__head' },
              h('span', { class: 'b5-board__title', text: 'Controls' }),
              h('span', { class: 'b5-board__sub', text: 'dimmer, position, colour, beam, focus, shaper' })),
            h('div', { class: 'b5-empty b5-con-placeholder' },
              h('span', { class: 'b5-empty__title', text: 'Attribute controls arrive next' }),
              h('span', { class: 'b5-empty__body', text: 'This area is reserved for the attribute controls (chunk C6b). The selection you make on the grid is already live in the programmer.' }))))),
      els.summary,
    ]);
    els.grid.addEventListener('click', onGridClick);
    els.grid.addEventListener('pointerdown', onPointerDown);
    els.grid.addEventListener('pointermove', onPointerMove);
    els.grid.addEventListener('pointerup', onPointerUp);
    els.grid.addEventListener('pointercancel', onPointerCancel);
    els.grid.addEventListener('keydown', onGridKey);
    els.picker.addEventListener('click', onGridClick);
    renderTools();
  }

  // --- loading ----------------------------------------------------------------
  async function load() {
    if (st.loading) return;
    st.loading = true;
    try {
      // The show token rides on GET /api/patch (api.js): every layout and
      // programmer write that follows then carries it, so an edit made from
      // a tab that is looking at another show is refused (409), like every
      // other /api/patch mutation.
      try { await Api.getPatch(); } catch (_) { /* the layout read reports it */ }
      const [layout, fx] = await Promise.all([Api.getLayout(), Api.getProgrammerFixtures().catch(() => null)]);
      st.layout = layout;
      st.cells = {};
      if (fx && fx.fixtures) fx.fixtures.forEach(f => { st.cells[f.entryId] = f.cells || []; });
      st.stale = false;
      if (st.view !== 'all' && !layerById(st.view)) st.view = 'all';
      if (typeof ProgrammerSync !== 'undefined') ProgrammerSync.refresh().catch(() => {});
      renderAll();
    } catch (e) {
      status('the layout could not be read (' + e.message + ').', 'error');
    } finally {
      st.loading = false;
    }
  }

  // --- rendering ----------------------------------------------------------------
  function renderAll() {
    renderTools();
    renderLayers();
    renderGroups();
    renderGrid();
    renderPicker();
    renderEdit();
    renderSummary();
  }

  function renderTools() {
    if (!els.add) return;
    els.add.className = 'b5-seg' + (st.addMode ? ' is-on' : '');
    els.add.setAttribute('aria-pressed', st.addMode ? 'true' : 'false');
    els.add.textContent = st.addMode ? 'Add mode: ON' : 'Add mode: OFF';
    els.editToggle.className = 'b5-seg' + (st.edit ? ' is-on' : '');
    els.editToggle.setAttribute('aria-pressed', st.edit ? 'true' : 'false');
    els.editToggle.textContent = st.edit ? 'Edit layout: ON' : 'Edit layout: OFF';
    const ly = st.view !== 'all' ? layerById(st.view) : null;
    els.selAll.textContent = ly ? 'Select all on ' + ly.name : 'Select all fixtures';
    els.zoomOut.disabled = st.zoom === 0;
    els.zoomIn.disabled = st.zoom === ZOOMS.length - 1;
    els.modebar.hidden = !st.edit;
    els.root.classList.toggle('is-editing', st.edit);
  }

  function countOn(layerId) {
    return items().filter(it => it.layer === layerId && it.kind !== 'object').length;
  }
  function renderLayers() {
    clear(els.layers);
    if (!st.layout) return;
    els.layers.appendChild(seg('All layers', st.view === 'all', { 'data-layer-chip': 'all' }, () => setView('all')));
    for (const l of layers()) {
      els.layers.appendChild(seg(l.name + ' · ' + countOn(l.id), st.view === l.id, { 'data-layer-chip': l.id }, () => setView(l.id)));
    }
  }
  function setView(v) { st.view = v; st.picker = null; renderTools(); renderLayers(); renderGrid(); renderPicker(); renderEdit(); }
  function zoom(d) {
    st.zoom = Math.max(0, Math.min(ZOOMS.length - 1, st.zoom + d));
    renderTools();
    renderGrid();
  }

  function groupState(g) {
    const order = selOrder();
    const m = groupMembers(g);
    const n = m.filter(t => order.has(key(t.entryId, t.cell))).length;
    if (!m.length) return { n, total: 0, word: 'empty' };
    if (n === m.length) return { n, total: m.length, word: 'all selected' };
    if (n) return { n, total: m.length, word: n + ' of ' + m.length + ' selected' };
    return { n, total: m.length, word: 'not selected' };
  }
  function renderGroups() {
    clear(els.groups);
    const gs = storedGroups();
    if (!gs.length) {
      els.groups.appendChild(h('span', { class: 'b5-caption', text: 'No stored groups yet — select fixtures, then Store group.' }));
      return;
    }
    for (const g of gs) {
      const s = groupState(g);
      const b = seg('', s.total > 0 && s.n === s.total, { 'data-group-chip': g.id }, ev => selectGroup(g.id, ev));
      append(b, [h('span', { text: g.name }), h('span', { class: 'b5-con-chipnote', text: ' · ' + s.word })]);
      els.groups.appendChild(b);
    }
  }

  function renderGrid() {
    if (!els.grid) return;
    clear(els.grid);
    if (!st.layout) {
      els.grid.appendChild(h('p', { class: 'b5-board__empty', text: 'Loading the layout…' }));
      return;
    }
    if (!st.layout.active) {
      els.grid.appendChild(h('p', { class: 'b5-board__empty', text: 'No show is open. Create or import a show on the Patch screen, then come back here.' }));
      return;
    }
    if (!st.layout.entries.length) {
      els.grid.appendChild(h('p', { class: 'b5-board__empty', text: 'This show has no fixtures yet. Add or import fixtures on the Patch screen.' }));
      return;
    }
    const cell = cellPx();
    els.grid.style.setProperty('--b5-con-cell', cell + 'px');
    const stale = staleIds();
    const shown = st.view === 'all' ? layers() : layers().filter(l => l.id === st.view);
    const colFrom = Math.min(...items().filter(it => !stale.has(it.id) && shown.some(l => l.id === it.layer)).map(it => it.col), 0);
    for (const ly of shown) {
      const list = items().filter(it => it.layer === ly.id && !stale.has(it.id));
      const b = bounds(list, colFrom);
      const n = list.filter(it => it.kind !== 'object').length;
      const height = ly.system ? 'fixtures with no place on the plan yet' : (ly.zKnown ? 'height ' + fmtM(ly.zMin) + ' to ' + fmtM(ly.zMax) : 'height not set');
      const canvas = h('div', { class: 'b5-con-canvas', 'data-canvas': ly.id,
        style: { width: ((b.c1 - b.c0) * cell) + 'px', height: ((b.r1 - b.r0) * cell) + 'px',
          'background-position': (-b.c0 * cell) + 'px ' + (-b.r0 * cell) + 'px' } });
      const sorted = list.slice().sort((a, c) => (a.kind === 'object') - (c.kind === 'object') || a.order - c.order);
      // Objects first, underneath: they are drawn, never tapped.
      for (const it of sorted.filter(x => x.kind === 'object')) canvas.appendChild(objectEl(it, b, cell));
      for (const it of sorted.filter(x => x.kind !== 'object')) canvas.appendChild(it.kind === 'group' ? groupTile(it, b, cell) : entryTile(it, b, cell));
      els.grid.appendChild(h('section', { class: 'b5-con-layer' + (ly.system ? ' b5-con-layer--unplaced' : ''), 'data-layer-block': ly.id, 'aria-label': 'Layer ' + ly.name },
        h('div', { class: 'b5-con-layerhead' },
          h('h3', { class: 'b5-con-layername' }, ly.name, h('span', { class: 'b5-caption', text: ' · ' + n + (n === 1 ? ' item' : ' items') + ' · ' + height })),
          btn('Select this layer', { cls: 'b5-btn--sm', attrs: { 'data-select-layer': ly.id } })),
        h('div', { class: 'b5-con-canvaswrap' }, canvas)));
    }
    if (stale.size) els.grid.appendChild(h('p', { class: 'b5-note', text: stale.size + ' layout item(s) point at something no longer in the show and are not drawn; remove them in edit mode or rebuild the layout.' }));
    applySelection();
  }

  function place(it, b, cell) {
    return { left: ((it.col - b.c0) * cell) + 'px', top: ((it.row - b.r0) * cell) + 'px', width: (it.w * cell) + 'px', height: (it.h * cell) + 'px' };
  }

  function entryTile(it, b, cell) {
    const e = entryById(it.ref) || { name: 'Unknown fixture', fixtureNumber: '', fixtureType: '' };
    const cells = st.cells[it.ref] || [];
    const markers = [];
    if (it.mode === 'auto') markers.push('auto');
    if (it.layer === 'unplaced') {
      const r = unplacedReason(it.ref);
      markers.push(r === 'no-location' ? 'no position' : r === 'not-derived' ? 'not derived' : 'unplaced');
    }
    const main = h('button', { type: 'button', class: 'b5-con-tile__main', 'data-select-entry': it.ref, 'aria-pressed': 'false' },
      // Fixture class glyph: a placeholder until the class is derived from
      // the profile (UI brief 4.2: never a guessed class) — "?".
      h('span', { class: 'b5-con-glyph', 'aria-hidden': 'true', title: e.fixtureType || 'fixture type not known', style: { transform: 'rotate(' + (it.rot || 0) + 'deg)' }, text: '?' }),
      h('span', { class: 'b5-con-tile__name', text: e.name || 'Unnamed fixture' }),
      h('span', { class: 'b5-con-tile__meta' },
        h('span', { class: 'b5-con-badge', 'data-badge': '', hidden: true }),
        h('span', { text: (e.fixtureNumber ? '#' + e.fixtureNumber : 'no number') + (markers.length ? ' · ' + markers.join(' · ') : '') })),
      h('span', { class: 'b5-visually-hidden', 'data-selword': '' }));
    const tile = h('div', { class: 'b5-con-tile b5-con-tile--entry' + (cells.length ? ' has-cells' : ''), 'data-item-id': it.id, 'data-kind': 'entry', 'data-entry-id': it.ref, style: place(it, b, cell) }, main);
    if (cells.length) {
      const tm = touchMin();
      const w = it.w * cell - 8, hgt = it.h * cell;
      if (hgt >= 2 * tm && cells.length * tm <= w) {
        const strip = h('div', { class: 'b5-con-cells', role: 'group', 'aria-label': 'Cells of ' + (e.name || 'fixture') });
        cells.forEach(c => strip.appendChild(h('button', { type: 'button', class: 'b5-con-cell', 'data-select-cell': c.id, 'data-entry-id': it.ref, 'aria-pressed': 'false', 'aria-label': (e.name || 'fixture') + ' cell ' + c.index + ' (' + c.name + ')' },
          h('span', { class: 'b5-con-cell__n', text: String(c.index) }), h('span', { class: 'b5-con-badge b5-con-badge--cell', 'data-badge': '', hidden: true }))));
        tile.appendChild(strip);
      } else if (hgt >= 2 * tm) {
        tile.appendChild(h('button', { type: 'button', class: 'b5-con-cellsbtn', 'data-open-cells': it.ref, 'aria-expanded': st.picker === it.ref ? 'true' : 'false' },
          h('span', { class: 'b5-con-cellsbtn__label', 'data-cells-label': '', text: cells.length + ' cells' })));
      } else {
        tile.appendChild(h('span', { class: 'b5-con-cellsnote', 'data-cells-label': '', text: cells.length + ' cells' }));
      }
    }
    return tile;
  }

  function groupTile(it, b, cell) {
    const g = groupById(it.ref);
    const n = groupMembers(g).length;
    const main = h('button', { type: 'button', class: 'b5-con-tile__main', 'data-select-group': it.ref, 'aria-pressed': 'false' },
      h('span', { class: 'b5-con-glyph b5-con-glyph--group', 'aria-hidden': 'true', text: 'GRP' }),
      h('span', { class: 'b5-con-tile__name', text: g ? g.name : 'Group no longer stored' }),
      h('span', { class: 'b5-con-tile__meta' },
        h('span', { class: 'b5-con-badge', 'data-badge': '', hidden: true }),
        h('span', { text: 'group · ' + n + (n === 1 ? ' member' : ' members') })),
      h('span', { class: 'b5-con-tile__meta', 'data-group-word': '', text: '' }));
    return h('div', { class: 'b5-con-tile b5-con-tile--group', 'data-item-id': it.id, 'data-kind': 'group', 'data-group-id': it.ref, style: place(it, b, cell) }, main);
  }

  // Objects: drawn with plain positioned boxes (no SVG needed). In edit
  // mode each gets a full-size handle button at its anchor so it can be
  // picked, dragged and nudged; otherwise it ignores the pointer.
  function objectEl(it, b, cell) {
    const g = it.geometry || { col: it.col, row: it.row };
    const x = (g.col - b.c0) * cell, y = (g.row - b.r0) * cell;
    const t = objectType(it.objectType);
    const shape = t ? t.shape : 'point';
    const wrap = h('div', { class: 'b5-con-obj b5-con-obj--' + it.objectType, 'data-item-id': it.id, 'data-kind': 'object' });
    let ax = x, ay = y;
    if (shape === 'line' && g.col2 !== undefined) {
      const x2 = (g.col2 - b.c0) * cell, y2 = (g.row2 - b.r0) * cell;
      const len = Math.hypot(x2 - x, y2 - y), ang = Math.atan2(y2 - y, x2 - x) * 180 / Math.PI;
      wrap.appendChild(h('div', { class: 'b5-con-obj__line', style: { left: x + 'px', top: y + 'px', width: len + 'px', transform: 'rotate(' + ang + 'deg)' } }));
      ax = (x + x2) / 2; ay = (y + y2) / 2;
    } else if (shape === 'rect' && g.w !== undefined) {
      ax = x + g.w * cell / 2; ay = y + g.h * cell / 2;
      wrap.appendChild(h('div', { class: 'b5-con-obj__rect', style: { left: x + 'px', top: y + 'px', width: (g.w * cell) + 'px', height: (g.h * cell) + 'px' } },
        h('span', { class: 'b5-con-obj__word', text: it.text || (t ? t.label : 'Area') })));
    } else {
      wrap.appendChild(h('div', { class: 'b5-con-obj__text', style: { left: x + 'px', top: y + 'px', transform: 'rotate(' + (it.rot || 0) + 'deg)' }, text: it.text || '' }));
    }
    if (shape === 'line') wrap.appendChild(h('span', { class: 'b5-con-obj__word b5-con-obj__word--line', style: { left: ax + 'px', top: ay + 'px' }, text: it.text || (it.objectType === 'truss' ? 'Truss' : 'Mark') }));
    if (st.edit) {
      wrap.appendChild(h('button', { type: 'button', class: 'b5-con-obj__handle' + (st.editId === it.id ? ' is-target' : ''), 'data-edit-object': it.id,
        style: { left: ax + 'px', top: ay + 'px' }, 'aria-label': 'Edit ' + itemName(it) }, (t ? t.label.split(' ')[0] : 'Object')));
    }
    return wrap;
  }

  // applySelection redraws only the selection marks (focus stays where it is
  // while other browsers change the selection).
  function applySelection() {
    if (!els.grid) return;
    const order = selOrder();
    const word = n => 'selected, number ' + n;
    els.grid.querySelectorAll('[data-select-entry]').forEach(b => {
      const n = order.get(key(b.getAttribute('data-select-entry'), '')) || 0;
      const tile = b.closest('[data-item-id]');
      const cellsSel = (st.cells[b.getAttribute('data-select-entry')] || []).filter(c => order.has(key(b.getAttribute('data-select-entry'), c.id))).length;
      b.setAttribute('aria-pressed', n ? 'true' : 'false');
      tile.classList.toggle('is-selected', !!n);
      tile.classList.toggle('has-cells-selected', !!cellsSel);
      tile.classList.toggle('is-edit-target', st.edit && st.editId === tile.getAttribute('data-item-id'));
      setBadge(b.querySelector('[data-badge]'), n);
      const sw = b.querySelector('[data-selword]');
      if (sw) sw.textContent = n ? ', ' + word(n) : (cellsSel ? ', ' + cellsSel + ' cells selected' : ', not selected');
      const lbl = tile.querySelector('[data-cells-label]');
      if (lbl) {
        const total = (st.cells[b.getAttribute('data-select-entry')] || []).length;
        lbl.textContent = cellsSel ? cellsSel + ' of ' + total + ' cells selected' : total + ' cells';
      }
    });
    els.grid.querySelectorAll('[data-select-cell]').forEach(b => {
      const n = order.get(key(b.getAttribute('data-entry-id'), b.getAttribute('data-select-cell'))) || 0;
      b.setAttribute('aria-pressed', n ? 'true' : 'false');
      b.classList.toggle('is-selected', !!n);
      setBadge(b.querySelector('[data-badge]'), n);
    });
    els.grid.querySelectorAll('[data-select-group]').forEach(b => {
      const s = groupState(groupById(b.getAttribute('data-select-group')));
      const all = s.total > 0 && s.n === s.total;
      const tile = b.closest('[data-item-id]');
      b.setAttribute('aria-pressed', all ? 'true' : 'false');
      tile.classList.toggle('is-selected', all);
      tile.classList.toggle('is-partial', s.n > 0 && !all);
      tile.classList.toggle('is-edit-target', st.edit && st.editId === tile.getAttribute('data-item-id'));
      const gw = b.querySelector('[data-group-word]');
      if (gw) gw.textContent = s.word;
      setBadge(b.querySelector('[data-badge]'), 0);
    });
  }
  function setBadge(el, n) {
    if (!el) return;
    el.hidden = !n;
    clear(el);
    if (n) append(el, [ic('status-ok'), String(n)]);
  }

  // --- cell picker ------------------------------------------------------------
  function renderPicker() {
    if (!els.picker) return;
    clear(els.picker);
    const e = st.picker ? entryById(st.picker) : null;
    const cells = st.picker ? (st.cells[st.picker] || []) : [];
    if (!e || !cells.length || st.edit) { els.picker.hidden = true; return; }
    els.picker.hidden = false;
    const order = selOrder();
    const mark = n => n ? [ic('status-ok'), h('span', { class: 'b5-con-pickn', text: '#' + n })] : [];
    const whole = order.get(key(e.id, '')) || 0;
    const row = h('div', { class: 'b5-con-pickrow' },
      h('button', { type: 'button', class: 'b5-seg' + (whole ? ' is-on' : ''), 'aria-pressed': whole ? 'true' : 'false', 'data-select-entry': e.id }, mark(whole), 'Whole fixture'));
    for (const c of cells) {
      const n = order.get(key(e.id, c.id)) || 0;
      row.appendChild(h('button', { type: 'button', class: 'b5-seg' + (n ? ' is-on' : ''), 'aria-pressed': n ? 'true' : 'false', 'data-select-cell': c.id, 'data-entry-id': e.id },
        mark(n), 'Cell ' + c.index, h('span', { class: 'b5-con-chipnote', text: ' · ' + c.name })));
    }
    append(els.picker, [
      h('div', { class: 'b5-con-pickhead' },
        h('h3', { class: 'b5-con-layername', text: 'Cells of ' + (e.name || 'fixture') + (e.fixtureNumber ? ' · #' + e.fixtureNumber : '') }),
        btn('Close cells', { cls: 'b5-btn--sm', attrs: { 'data-close-cells': '' } }, () => { st.picker = null; renderPicker(); renderGrid(); })),
      h('p', { class: 'b5-caption', text: st.addMode ? 'Add mode is ON: each tap adds a cell or takes it out.' : 'Tap a cell to select only it; turn Add mode on to pick several.' }),
      row]);
  }

  // --- selection gestures -----------------------------------------------------
  function sel(body) {
    if (typeof ProgrammerSync === 'undefined') return Promise.resolve();
    return ProgrammerSync.act('select', body).then(() => status(''), e => status(e.message, 'error'));
  }
  const adding = ev => st.addMode || !!(ev && (ev.shiftKey || ev.ctrlKey || ev.metaKey));
  function selectTarget(entryId, cell, ev) {
    return sel({ action: adding(ev) ? 'toggle' : 'set', targets: [{ entryId, cell: cell || '' }] });
  }
  function selectGroup(id, ev) {
    if (!adding(ev)) return sel({ action: 'set', group: id });
    const s = groupState(groupById(id));
    return sel({ action: s.total > 0 && s.n === s.total ? 'remove' : 'add', group: id });
  }
  function selectLayer(id, ev) { return sel({ action: adding(ev) ? 'add' : 'set', layer: id }); }
  function selectAll() {
    if (st.view !== 'all' && layerById(st.view)) return selectLayer(st.view);
    return sel({ action: 'all' });
  }

  function onGridClick(ev) {
    const t = ev.target && ev.target.closest ? ev.target.closest('button') : null;
    if (!t) return;
    if (t.hasAttribute('data-select-layer')) return selectLayer(t.getAttribute('data-select-layer'), ev);
    if (t.hasAttribute('data-close-cells')) return;
    if (st.edit) {
      const holder = t.closest('[data-item-id]');
      const id = t.getAttribute('data-edit-object') || (holder && holder.getAttribute('data-item-id'));
      if (id) editPick(id);
      return;
    }
    if (t.hasAttribute('data-open-cells')) {
      const id = t.getAttribute('data-open-cells');
      st.picker = st.picker === id ? null : id;
      renderPicker();
      t.setAttribute('aria-expanded', st.picker === id ? 'true' : 'false');
      return;
    }
    if (t.hasAttribute('data-select-cell')) return selectTarget(t.getAttribute('data-entry-id'), t.getAttribute('data-select-cell'), ev);
    if (t.hasAttribute('data-select-entry')) return selectTarget(t.getAttribute('data-select-entry'), '', ev);
    if (t.hasAttribute('data-select-group')) return selectGroup(t.getAttribute('data-select-group'), ev);
  }

  // --- edit mode: drag, nudge, panel -------------------------------------------
  function setEdit(on) {
    st.edit = !!on;
    if (!st.edit) st.editId = null;
    st.picker = null;
    drag = null;
    renderTools();
    renderGrid();
    renderPicker();
    renderEdit();
  }
  function editPick(id) {
    st.editId = id;
    applySelection();
    els.grid.querySelectorAll('[data-edit-object]').forEach(b => b.classList.toggle('is-target', b.getAttribute('data-edit-object') === id));
    renderEdit();
  }
  function dragHolder(ev) {
    const t = ev.target && ev.target.closest ? ev.target : null;
    if (!t) return null;
    const handle = t.closest('[data-edit-object]');
    if (handle) return { id: handle.getAttribute('data-edit-object'), el: handle.closest('[data-item-id]') };
    const tile = t.closest('.b5-con-tile');
    return tile ? { id: tile.getAttribute('data-item-id'), el: tile } : null;
  }
  function onPointerDown(ev) {
    if (!st.edit) return;
    if (ev.button !== undefined && ev.button !== 0) return;
    const hold = dragHolder(ev);
    if (!hold) return;
    const it = itemById(hold.id);
    if (!it) return;
    drag = { id: it.id, el: hold.el, pointerId: ev.pointerId, x0: ev.clientX, y0: ev.clientY, moved: false };
    try { els.grid.setPointerCapture(ev.pointerId); } catch (_) { /* not capturable (tests) */ }
  }
  function onPointerMove(ev) {
    if (!drag || !st.edit || ev.pointerId !== drag.pointerId) return;
    const dx = ev.clientX - drag.x0, dy = ev.clientY - drag.y0;
    if (!drag.moved && Math.hypot(dx, dy) < DRAG_SLOP) return;
    drag.moved = true;
    if (ev.preventDefault) ev.preventDefault();
    drag.el.style.setProperty('transform', 'translate(' + dx + 'px, ' + dy + 'px)');
    drag.el.classList.add('is-dragging');
  }
  function endDrag() {
    if (drag && drag.el) { drag.el.style.removeProperty('transform'); drag.el.classList.remove('is-dragging'); }
    drag = null;
  }
  function onPointerCancel() { endDrag(); }
  function onPointerUp(ev) {
    if (!drag || ev.pointerId !== drag.pointerId) return;
    const d = drag;
    const dx = ev.clientX - d.x0, dy = ev.clientY - d.y0;
    endDrag();
    try { els.grid.releasePointerCapture(ev.pointerId); } catch (_) { /* fine */ }
    if (!st.edit || !d.moved) return;
    const it = itemById(d.id);
    if (!it) return;
    const step = it.kind === 'object' ? 0.5 : 1;
    const dc = Math.round(dx / cellPx() / step) * step, dr = Math.round(dy / cellPx() / step) * step;
    st.editId = it.id;
    if (dc || dr) shift(it, dc, dr);
    else renderEdit();
  }
  function onGridKey(ev) {
    if (!st.edit) return;
    const dirs = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
    const d = dirs[ev.key];
    if (!d) return;
    const hold = dragHolder(ev);
    if (!hold) return;
    const it = itemById(hold.id);
    if (!it) return;
    if (ev.preventDefault) ev.preventDefault();
    const step = it.kind === 'object' ? 0.5 : 1;
    st.editId = it.id;
    shift(it, d[0] * step, d[1] * step);
  }

  // shift moves an item by whole cells (objects: half cells).
  function shift(it, dc, dr) {
    if (it.kind === 'object') return edit('object-update', { id: it.id, geometry: moved(it.geometry, dc, dr) }, 'Moved ' + itemName(it) + '.');
    return edit('move', { id: it.id, col: it.col + dc, row: it.row + dr }, 'Moved ' + itemName(it) + '.');
  }
  function geom(g) {
    const out = { col: g.col, row: g.row };
    ['col2', 'row2', 'w', 'h'].forEach(k => { if (g[k] !== undefined && g[k] !== null) out[k] = g[k]; });
    return out;
  }
  function moved(g, dc, dr) {
    const out = geom(g);
    out.col += dc; out.row += dr;
    if (out.col2 !== undefined) { out.col2 += dc; out.row2 += dr; }
    return out;
  }

  // edit posts one layout action; the answer is the whole new layout.
  async function edit(action, body, done) {
    try {
      const resp = await Api.layoutAction(action, body);
      st.layout = resp;
      if (st.editId && !itemById(st.editId)) st.editId = null;
      if (st.view !== 'all' && !layerById(st.view)) st.view = 'all';
      renderAll();
      status(typeof done === 'function' ? done(resp) : done);
      return resp;
    } catch (e) {
      status(e.message, 'error');
      return null;
    }
  }

  function renderEdit() {
    if (!els.edit) return;
    clear(els.edit);
    els.edit.hidden = !st.edit;
    if (!st.edit || !st.layout || !st.layout.active) return;
    const it = st.editId ? itemById(st.editId) : null;
    els.edit.appendChild(h('h2', { class: 'b5-board__head' },
      h('span', { class: 'b5-board__title', text: 'Edit layout' }),
      h('span', { class: 'b5-board__sub', text: 'changes the plan only — no DMX is sent' })));
    els.edit.appendChild(it ? itemPanel(it) : h('p', { class: 'b5-note', 'data-edit-empty': '', text: 'Tap an item on the grid to move, resize or edit it. Drag to move; arrow keys nudge the focused item.' }));
    els.edit.appendChild(createPanel());
    els.edit.appendChild(layerPanel());
    els.edit.appendChild(derivePanel());
  }

  function group(title, ...kids) {
    return h('div', { class: 'b5-group b5-con-editgroup' }, h('h3', { class: 'b5-group__head', text: title }), ...kids);
  }
  function layerSelect(current, attr) {
    const s = h('select', { class: 'b5-select', [attr]: '' });
    layers().forEach(l => {
      const o = h('option', { value: l.id, text: l.name });
      if (l.id === current) o.setAttribute('selected', '');
      s.appendChild(o);
    });
    s.value = current;
    return s;
  }

  function itemPanel(it) {
    const name = itemName(it);
    const isVirtual = virtualIds().has(it.id);
    const ly = layerById(it.layer);
    const where = it.kind === 'object'
      ? 'at column ' + it.geometry.col + ', row ' + it.geometry.row
      : 'at column ' + it.col + ', row ' + it.row + ' · ' + it.w + '×' + it.h + ' cells · turned ' + (it.rot || 0) + '°';
    const mode = it.kind === 'entry' ? (it.mode === 'auto' ? 'placed automatically' : 'placed by hand') : 'placed by hand';
    const nudges = h('div', { class: 'b5-con-nudge', role: 'group', 'aria-label': 'Move' },
      btn('Left', { cls: 'b5-btn--sm', attrs: { 'data-nudge': 'left' } }, () => shift(it, it.kind === 'object' ? -0.5 : -1, 0)),
      btn('Up', { cls: 'b5-btn--sm', attrs: { 'data-nudge': 'up' } }, () => shift(it, 0, it.kind === 'object' ? -0.5 : -1)),
      btn('Down', { cls: 'b5-btn--sm', attrs: { 'data-nudge': 'down' } }, () => shift(it, 0, it.kind === 'object' ? 0.5 : 1)),
      btn('Right', { cls: 'b5-btn--sm', attrs: { 'data-nudge': 'right' } }, () => shift(it, it.kind === 'object' ? 0.5 : 1, 0)));
    const lsel = layerSelect(it.layer, 'data-item-layer');
    const layerRow = h('div', { class: 'b5-row' },
      h('label', { class: 'b5-con-inline' }, h('span', { text: 'Layer ' }), lsel),
      btn('Move to layer', { cls: 'b5-btn--sm', attrs: { 'data-move-layer': '' } }, () => {
        if (lsel.value === it.layer) return status('It is already on that layer.', 'error');
        if (it.kind === 'object') return edit('object-update', { id: it.id, layer: lsel.value }, 'Moved ' + name + ' to another layer.');
        return edit('move', { id: it.id, layer: lsel.value }, 'Moved ' + name + ' to another layer.');
      }));
    const parts = [
      h('p', { class: 'b5-con-editname' }, h('strong', { text: name }), h('span', { class: 'b5-caption', text: ' · ' + (it.kind === 'entry' ? 'fixture' : it.kind) + ' · ' + (ly ? ly.name : 'unknown layer') + ' · ' + where + ' · ' + mode })),
      nudges, layerRow,
    ];
    if (it.kind !== 'object') {
      parts.push(h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Size and turn' },
        btn('Wider', { cls: 'b5-btn--sm', attrs: { 'data-size': 'w+' } }, () => edit('move', { id: it.id, w: it.w + 1 }, 'Resized ' + name + '.')),
        btn('Narrower', { cls: 'b5-btn--sm', attrs: { 'data-size': 'w-' }, disabled: it.w <= 1 }, () => edit('move', { id: it.id, w: it.w - 1 }, 'Resized ' + name + '.')),
        btn('Taller', { cls: 'b5-btn--sm', attrs: { 'data-size': 'h+' } }, () => edit('move', { id: it.id, h: it.h + 1 }, 'Resized ' + name + '.')),
        btn('Shorter', { cls: 'b5-btn--sm', attrs: { 'data-size': 'h-' }, disabled: it.h <= 1 }, () => edit('move', { id: it.id, h: it.h - 1 }, 'Resized ' + name + '.')),
        btn('Turn 90°', { cls: 'b5-btn--sm', attrs: { 'data-rotate': '' } }, () => edit('move', { id: it.id, rot: ((it.rot || 0) + 90) % 360 }, 'Turned ' + name + '.'))));
    }
    const actions = h('div', { class: 'b5-row' });
    if (it.kind === 'entry') {
      if (it.mode === 'auto') actions.appendChild(btn('Pin here', { cls: 'b5-btn--sm', attrs: { 'data-pin': '' } }, () => edit('pin', { id: it.id }, 'Pinned ' + name + ': refresh will not move it.')));
      else actions.appendChild(btn('Back to automatic', { cls: 'b5-btn--sm', attrs: { 'data-reset-auto': '' } }, () => edit('reset-auto', { id: it.id }, 'Handed ' + name + ' back to automatic placement.')));
    }
    if (!isVirtual) {
      actions.appendChild(btn('Bring to front', { cls: 'b5-btn--sm', attrs: { 'data-order': 'front' } }, () => edit('item-order', { id: it.id, to: 'front' }, 'Stacked ' + name + ' on top.')));
      actions.appendChild(btn('Send to back', { cls: 'b5-btn--sm', attrs: { 'data-order': 'back' } }, () => edit('item-order', { id: it.id, to: 'back' }, 'Stacked ' + name + ' underneath.')));
    }
    if (it.kind === 'object') {
      const t = objectType(it.objectType);
      if (t && t.shape === 'rect') {
        parts.push(h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Size' },
          btn('Wider', { cls: 'b5-btn--sm', attrs: { 'data-size': 'w+' } }, () => edit('object-update', { id: it.id, geometry: Object.assign(geom(it.geometry), { w: it.geometry.w + 0.5 }) }, 'Resized ' + name + '.')),
          btn('Narrower', { cls: 'b5-btn--sm', attrs: { 'data-size': 'w-' }, disabled: it.geometry.w <= 0.5 }, () => edit('object-update', { id: it.id, geometry: Object.assign(geom(it.geometry), { w: it.geometry.w - 0.5 }) }, 'Resized ' + name + '.')),
          btn('Taller', { cls: 'b5-btn--sm', attrs: { 'data-size': 'h+' } }, () => edit('object-update', { id: it.id, geometry: Object.assign(geom(it.geometry), { h: it.geometry.h + 0.5 }) }, 'Resized ' + name + '.')),
          btn('Shorter', { cls: 'b5-btn--sm', attrs: { 'data-size': 'h-' }, disabled: it.geometry.h <= 0.5 }, () => edit('object-update', { id: it.id, geometry: Object.assign(geom(it.geometry), { h: it.geometry.h - 0.5 }) }, 'Resized ' + name + '.'))));
      }
      if (t && t.shape === 'line') {
        const end = (dc, dr) => () => edit('object-update', { id: it.id, geometry: Object.assign(geom(it.geometry), { col2: it.geometry.col2 + dc, row2: it.geometry.row2 + dr }) }, 'Moved the end of ' + name + '.');
        parts.push(h('div', { class: 'b5-row', role: 'group', 'aria-label': 'Move the far end' },
          h('span', { class: 'b5-caption', text: 'Far end:' }),
          btn('Left', { cls: 'b5-btn--sm', attrs: { 'data-end': 'left' } }, end(-0.5, 0)),
          btn('Up', { cls: 'b5-btn--sm', attrs: { 'data-end': 'up' } }, end(0, -0.5)),
          btn('Down', { cls: 'b5-btn--sm', attrs: { 'data-end': 'down' } }, end(0, 0.5)),
          btn('Right', { cls: 'b5-btn--sm', attrs: { 'data-end': 'right' } }, end(0.5, 0))));
      }
      if (t && t.rotatable) actions.appendChild(btn('Turn 90°', { cls: 'b5-btn--sm', attrs: { 'data-rotate': '' } }, () => edit('object-update', { id: it.id, rot: ((it.rot || 0) + 90) % 360 }, 'Turned ' + name + '.')));
      actions.appendChild(btn('Edit text…', { cls: 'b5-btn--sm', attrs: { 'data-object-text': '' } }, async () => {
        const v = await ask({ title: 'Text for ' + name, field: { label: 'Text', value: it.text || '', maxLength: (st.layout.limits && st.layout.limits.objectTextMax) || 80 }, ok: 'Save' });
        if (v !== null) edit('object-update', { id: it.id, text: v }, 'Saved the text.');
      }));
      actions.appendChild(btn('Duplicate', { cls: 'b5-btn--sm', attrs: { 'data-object-duplicate': '' } }, () => edit('object-duplicate', { id: it.id }, r => { if (r.result && r.result.id) editPick(r.result.id); return 'Duplicated ' + name + '.'; })));
      actions.appendChild(btn('Delete object…', { cls: 'b5-btn--sm b5-btn--danger', attrs: { 'data-object-delete': '' } }, async () => {
        if (await ask({ title: 'Delete ' + name + '?', body: 'Only the drawing is removed; no fixture changes.', ok: 'Delete', danger: true })) edit('object-delete', { id: it.id }, 'Deleted ' + name + '.');
      }));
    } else if (!isVirtual) {
      const what = it.kind === 'entry' ? 'It goes back to the Unplaced layer and stays selectable.' : 'The group itself stays stored.';
      actions.appendChild(btn('Take off the plan…', { cls: 'b5-btn--sm', attrs: { 'data-remove': '' } }, async () => {
        if (await ask({ title: 'Take ' + name + ' off the plan?', body: what, ok: 'Take off' })) edit('remove', { id: it.id }, 'Took ' + name + ' off the plan.');
      }));
    }
    parts.push(actions);
    parts.push(btn('Stop editing this item', { cls: 'b5-btn--sm b5-btn--ghost', attrs: { 'data-edit-close': '' } }, () => { st.editId = null; applySelection(); renderEdit(); renderGrid(); }));
    return group('Selected item', ...parts);
  }

  // free cell on a layer for a new group placement: first cell, reading
  // order, not covered by a fixture or group (objects never block a cell).
  function freeCell(layerId) {
    const taken = new Set();
    items().filter(it => it.layer === layerId && it.kind !== 'object').forEach(it => {
      for (let c = it.col; c < it.col + it.w; c++) for (let r = it.row; r < it.row + it.h; r++) taken.add(c + ',' + r);
    });
    for (let r = 0; r < 999; r++) for (let c = 0; c < 16; c++) if (!taken.has(c + ',' + r)) return { col: c, row: r };
    return { col: 0, row: 0 };
  }

  function createPanel() {
    const layer = targetLayer();
    const ly = layerById(layer);
    const types = (st.layout.objectTypes || []);
    const below = () => {
      const list = items().filter(it => it.layer === layer);
      if (!list.length) return { c: 0, r: 0 };
      const b = bounds(list);
      return { c: b.c0, r: b.r1 };
    };
    const geometryFor = (t, p) => {
      if (t.shape === 'line' && t.type === 'mark') return { col: p.c, row: p.r, col2: p.c, row2: p.r + 3 };
      if (t.shape === 'line') return { col: p.c, row: p.r + 0.5, col2: p.c + 4, row2: p.r + 0.5 };
      if (t.shape === 'rect') return { col: p.c, row: p.r, w: 4, h: 2 };
      return { col: p.c, row: p.r };
    };
    const row = h('div', { class: 'b5-row' });
    types.forEach(t => row.appendChild(btn('Add ' + t.label.toLowerCase(), { cls: 'b5-btn--sm', attrs: { 'data-add-object': t.type } }, async () => {
      let text = '';
      if (t.textRequired) {
        text = await ask({ title: 'New ' + t.label.toLowerCase(), field: { label: 'Text', value: '', maxLength: (st.layout.limits && st.layout.limits.objectTextMax) || 80 }, ok: 'Add' });
        if (text === null) return;
      }
      edit('object-create', { objectType: t.type, layer, text, rot: 0, geometry: geometryFor(t, below()) },
        r => { if (r.result && r.result.id) editPick(r.result.id); return 'Added ' + t.label.toLowerCase() + ' on ' + (ly ? ly.name : 'the layer') + '.'; });
    })));
    const placed = new Set(items().filter(it => it.kind === 'group').map(it => it.ref));
    const free = storedGroups().filter(g => !placed.has(g.id));
    const gsel = h('select', { class: 'b5-select', 'data-place-group-select': '' });
    free.forEach(g => gsel.appendChild(h('option', { value: g.id, text: g.name })));
    if (free.length) gsel.value = free[0].id;
    const grow = h('div', { class: 'b5-row' },
      free.length ? h('label', { class: 'b5-con-inline' }, h('span', { text: 'Group ' }), gsel) : h('span', { class: 'b5-caption', text: storedGroups().length ? 'Every stored group is already on the plan.' : 'No stored groups to place.' }),
      btn('Place group', { cls: 'b5-btn--sm', attrs: { 'data-place-group': '' }, disabled: !free.length }, () => {
        const p = freeCell(layer);
        const g = groupById(gsel.value);
        edit('place', { kind: 'group', ref: gsel.value, layer, col: p.col, row: p.row }, 'Placed group ' + (g ? g.name : '') + '.');
      }));
    return group('Add to ' + (ly ? ly.name : 'the plan'),
      h('p', { class: 'b5-caption', text: 'New items land on the layer shown (or the first layer when all are shown), below what is there.' }),
      row, grow);
  }

  function layerPanel() {
    const ls = layers();
    const list = h('ol', { class: 'b5-con-layerlist' });
    ls.forEach((l, i) => {
      const n = items().filter(it => it.layer === l.id && !(l.system && virtualIds().has(it.id))).length;
      const reorder = d => () => {
        const ids = ls.map(x => x.id);
        const j = i + d;
        [ids[i], ids[j]] = [ids[j], ids[i]];
        edit('layer-reorder', { order: ids }, 'Moved layer ' + l.name + '.');
      };
      list.appendChild(h('li', { class: 'b5-con-layeritem', 'data-layer-row': l.id },
        h('span', { class: 'b5-con-layeritem__name' }, h('strong', { text: l.name }), h('span', { class: 'b5-caption', text: l.system ? ' · system layer, cannot be deleted' : '' })),
        h('span', { class: 'b5-row' },
          btn('Rename…', { cls: 'b5-btn--sm', attrs: { 'data-layer-rename': l.id } }, async () => {
            const v = await ask({ title: 'Rename layer', field: { label: 'Layer name', value: l.name, maxLength: (st.layout.limits && st.layout.limits.objectTextMax) || 80 }, ok: 'Rename' });
            if (v !== null) edit('layer-rename', { id: l.id, name: v }, 'Renamed the layer to ' + v + '.');
          }),
          btn('Up', { cls: 'b5-btn--sm', attrs: { 'data-layer-up': l.id }, disabled: i === 0 }, reorder(-1)),
          btn('Down', { cls: 'b5-btn--sm', attrs: { 'data-layer-down': l.id }, disabled: i === ls.length - 1 }, reorder(1)),
          l.system ? null : btn('Delete…', { cls: 'b5-btn--sm b5-btn--danger', attrs: { 'data-layer-delete': l.id } }, async () => {
            const body = n ? 'It holds ' + n + ' item(s); they are taken off the plan with it. Fixtures among them go back to the Unplaced layer.' : 'It is empty.';
            if (await ask({ title: 'Delete layer ' + l.name + '?', body, ok: 'Delete layer', danger: true })) edit('layer-delete', { id: l.id, removeItems: n > 0 }, 'Deleted layer ' + l.name + '.');
          }))));
    });
    return group('Layers', list,
      btn('New layer…', { cls: 'b5-btn--sm', attrs: { 'data-layer-create': '' } }, async () => {
        const v = await ask({ title: 'New layer', field: { label: 'Layer name', value: '', maxLength: 80 }, ok: 'Create' });
        if (v !== null) edit('layer-create', { name: v }, 'Created layer ' + v + '.');
      }));
  }

  function deriveSummary(r) {
    const res = r && r.result;
    if (!res) return 'Done.';
    return 'From positions: ' + (res.placed || []).length + ' placed, ' + (res.moved || []).length + ' moved, ' + (res.unchanged || 0) + ' unchanged, ' +
      (res.notPlaced || []).length + ' left unplaced, ' + (res.layersCreated || 0) + ' new layer(s).';
  }
  function derivePanel() {
    const derived = !!(st.layout && st.layout.layout.derived);
    const located = ((st.layout && st.layout.entries) || []).filter(e => e.location && e.location.known).length;
    return group('From MVR positions',
      h('p', { class: 'b5-caption', text: located + ' of ' + st.layout.entries.length + ' fixtures have a position from the MVR file.' +
        (derived ? ' This plan was derived at ' + st.layout.layout.cellMm + ' mm per cell.' : ' The plan has not been derived from positions yet.') }),
      h('div', { class: 'b5-row' },
        btn('Derive from positions', { cls: 'b5-btn--sm', attrs: { 'data-derive': '' } }, () => edit('derive', {}, deriveSummary)),
        btn('Refresh from positions', { cls: 'b5-btn--sm', attrs: { 'data-refresh': '' }, disabled: !derived }, () => edit('refresh', {}, deriveSummary))),
      h('div', { class: 'b5-row' },
        btn('Rebuild from positions…', { cls: 'b5-btn--sm b5-btn--danger', attrs: { 'data-rebuild': '' } }, async () => {
          if (await ask({ title: 'Rebuild the plan from positions?', body: 'Every layer, hand placement, placed group and object is discarded and the plan is rebuilt from the MVR positions. No DMX changes.', ok: 'Rebuild', danger: true })) {
            edit('derive', { replace: true, confirm: 'REPLACE' }, deriveSummary);
          }
        }),
        btn('All fixtures back to automatic…', { cls: 'b5-btn--sm', attrs: { 'data-reset-all': '' } }, async () => {
          if (await ask({ title: 'Hand every fixture back to automatic placement?', body: 'Fixtures you placed by hand will move to their derived spot (or the Unplaced layer).', ok: 'Reset all' })) {
            edit('reset-auto', { all: true, confirm: 'RESET' }, r => ((r.result && r.result.reset) || 0) + ' placement(s) handed back to automatic.');
          }
        })));
  }

  // --- summary bar ---------------------------------------------------------------
  function renderSummary() {
    if (!els.summary) return;
    clear(els.summary);
    const p = prog();
    const s = selection();
    const cells = s.filter(x => x.cell).length;
    const hl = !!(p && p.highlight && p.highlight.on);
    const label = x => (x.name || 'Unnamed fixture') + (x.cell ? ' cell ' + (x.cellIndex || '?') + (x.cellName ? ' (' + x.cellName + ')' : '') : '');
    const shown = s.slice(0, 8).map((x, i) => (i + 1) + ' ' + label(x));
    const names = s.length ? shown.join(' · ') + (s.length > 8 ? ' · +' + (s.length - 8) + ' more' : '') : 'Tap a fixture, a cell, a group or a layer to select it.';
    append(els.summary, [
      h('div', { class: 'b5-actionbar__status b5-con-summary__status' },
        h('span', { class: 'b5-actionbar__title', text: 'Selection' }),
        h('span', { class: 'b5-pill b5-pill--md' + (s.length ? ' b5-pill--accent' : ''), 'data-sel-count': '' },
          ic(s.length ? 'status-ok' : 'status-pending'),
          s.length ? s.length + ' selected' + (cells ? ' · ' + cells + (cells === 1 ? ' cell' : ' cells') : '') : 'Nothing selected'),
        h('span', { class: 'b5-con-summary__names', 'data-sel-names': '', title: s.map((x, i) => (i + 1) + ' ' + label(x)).join(', ') }, names)),
      h('div', { class: 'b5-actionbar__buttons b5-con-summary__buttons' },
        btn('Clear selection', { attrs: { 'data-clear-selection': '' }, disabled: !s.length }, () => sel({ action: 'none' })),
        btn('Store group…', { attrs: { 'data-store-group': '' }, disabled: !s.length }, async () => {
          const name = await ask({ title: 'Store the selection as a group', body: 'The group keeps the fixtures and cells in this selection order.', field: { label: 'Group name', value: '', maxLength: 80 }, ok: 'Store group' });
          if (name === null) return;
          try { await ProgrammerSync.act('groups/store', { name }); status('Stored group ' + name + '.'); }
          catch (e) { status(e.message, 'error'); }
        }),
        seg(hl ? 'Highlight: ON' : 'Highlight: OFF', hl, { 'data-highlight': '' }, async () => {
          try { await ProgrammerSync.act('highlight', { highlight: !hl }); status(''); }
          catch (e) { status(e.message, 'error'); }
        }),
        btn('Locate', { attrs: { 'data-locate': '' }, disabled: !s.length, icon: 'identify' }, async () => {
          try { await ProgrammerSync.act('locate', {}); status('Located the selection: open white, centred (reaches the rig only while output is armed).'); }
          catch (e) { status(e.message, 'error'); }
        })),
    ]);
  }

  // --- phone layout: keep the sticky summary above the fixed bottom nav ------
  // The mobile nav is position:fixed at the bottom; a sticky bottom:0 bar sits
  // UNDER it (the Send screen's action bar does exactly that at 390 px). Its
  // real height is published so the bar's bottom offset clears it.
  function publishNavHeight() {
    const nav = document.getElementById('tabsMobile');
    if (!nav || !nav.getBoundingClientRect) return;
    const measure = () => {
      const r = nav.getBoundingClientRect();
      document.documentElement.style.setProperty('--b5-con-nav-reserve', (r && r.height ? r.height : 0) + 'px');
    };
    measure();
    if (typeof ResizeObserver === 'function') new ResizeObserver(measure).observe(nav);
    window.addEventListener('resize', measure);
  }

  // --- lifecycle ------------------------------------------------------------------
  let inited = false;
  function init() {
    if (inited) return;
    inited = true;
    build();
    publishNavHeight();
    if (typeof ProgrammerSync !== 'undefined') {
      ProgrammerSync.onChange(() => {
        if (!st.built) return;
        applySelection();
        renderGroups();
        renderPicker();
        renderSummary();
        if (st.edit) renderEdit();
      });
    }
    window.addEventListener('b5-show-changed', () => {
      st.stale = true; st.editId = null; st.picker = null;
      if (st.active) load();
    });
  }
  function onEnterScreen() {
    init();
    st.active = true;
    renderAll();
    load();
  }
  // Leaving changes nothing on the wire: no stop, no blackout, no deselect.
  // An unfinished drag is dropped (it was never sent).
  function onLeaveScreen() {
    st.active = false;
    endDrag();
  }

  return { init, onEnterScreen, onLeaveScreen, load, _state: st };
})();
