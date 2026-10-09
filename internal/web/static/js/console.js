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
// I2b (component-specs §3, §4): sprite fixture glyphs chosen from the
// fixture's capabilities with flag words, parent outline vs cell tab, a
// visible text order list, layer chips as a multi-layer filter (Select
// all / Invert respect it; Ghosts draws hidden layers as dashed words) and
// a Lasso mode toggle, so a finger drag still scrolls the plan unless
// Lasso is ON.
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
    models: {},            // entryId -> FixtureModel (same read): glyph class, flags (I2b)
    // I2b: the layer filter. Empty = every layer shown; otherwise only the
    // layer ids in the set are drawn and selectable (component-specs §4).
    shown: new Set(),
    ghosts: false,         // draw filtered-out layers as dashed, unselectable ghosts
    lasso: false,          // Lasso ON: a drag on the grid draws a selection box
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
  // choose (I2d §14): a NON-modal choice dialog (Disarm stays operable);
  // resolves to the picked value, or null on Cancel / Escape. Cancel has
  // focus first: a destructive choice is never the default.
  function choose(o) {
    return new Promise(resolve => {
      let done = false;
      const cancel = btn('Cancel', { attrs: { 'data-choice': 'cancel' } });
      const dlg = h('dialog', { class: 'b5-con-floatdialog', 'data-choose': '', 'aria-label': o.title }, h('h3', { text: o.title }),
        o.body ? h('p', { class: 'b5-note', text: o.body }) : null,
        h('div', { class: 'b5-row b5-con-dialog__actions' }, cancel,
          o.choices.map(c => btn(c.label, { cls: c.cls, attrs: { 'data-choice': c.value } }, () => finish(c.value)))));
      const finish = v => { if (done) return; done = true; try { if (dlg.open) dlg.close(); } catch (_) { /* closed */ } dlg.remove(); resolve(v); };
      cancel.addEventListener('click', () => finish(null));
      dlg.addEventListener('keydown', ev => { if (ev.key === 'Escape') { if (ev.preventDefault) ev.preventDefault(); finish(null); } });
      document.body.appendChild(dlg);
      dlg.show();
      cancel.focus();
    });
  }
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
  // I2b layer filter helpers. filtered() = some layers are hidden.
  function pruneShown() { [...st.shown].forEach(id => { if (!layerById(id)) st.shown.delete(id); }); }
  function filtered() { return st.shown.size > 0; }
  function isShown(layerId) { return !filtered() || st.shown.has(layerId); }
  function shownLayers() { return layers().filter(l => isShown(l.id)); }
  // The layer new things land on: the first layer shown, else the first one.
  function targetLayer() {
    const s = shownLayers();
    if (filtered() && s.length) return s[0].id;
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
  // Layer height in words (§4: a chip names its z-height; unset = "height
  // unknown"). The Unplaced layer has no height by definition.
  function heightWords(ly) {
    if (ly.system) return 'no position';
    if (!ly.zKnown) return 'height unknown';
    return ly.zMin === ly.zMax ? 'height ' + fmtM(ly.zMin) : 'height ' + fmtM(ly.zMin) + ' to ' + fmtM(ly.zMax);
  }

  // --- fixture class and flags (I2b, component-specs §3) ------------------------
  // The glyph comes from what the fixture's channel map can DO, never from
  // its type name: a class the capabilities do not show is fx-unknown, not
  // a guessed head. First match wins:
  //   Pan/Tilt (position group)        -> moving head
  //   two or more cells                -> multi-cell (pixel bar glyph)
  //   shaper group, or a gobo          -> profile
  //   colour group                     -> wash
  //   shutter/strobe                   -> strobe
  //   dimmer group                     -> dimmer
  //   anything else, or no channel map -> unknown
  const CLASS_WORD = { 'moving-head': 'moving head', wash: 'wash', profile: 'profile', 'pixel-bar': 'multi-cell', strobe: 'strobe', dimmer: 'dimmer', unknown: 'type not known' };
  function fixtureClass(entryId) {
    const m = st.models[entryId];
    const params = ((m && m.parameters) || []).filter(p => !p.virtual);
    const groups = new Set(params.map(p => p.group));
    const attr = re => params.some(p => re.test(p.attribute || ''));
    let g = 'unknown';
    if (groups.has('position')) g = 'moving-head';
    else if (((m && m.cells) || []).length >= 2) g = 'pixel-bar';
    else if (groups.has('shaper') || attr(/^(Gobo|StaticGobo)/)) g = 'profile';
    else if (groups.has('colour')) g = 'wash';
    else if (attr(/^(Shutter|Strobe)/)) g = 'strobe';
    else if (groups.has('dimmer')) g = 'dimmer';
    return { glyph: g, word: CLASS_WORD[g] };
  }
  // flags: the words that go with a dashed or struck body. "No profile" =
  // no channel map at all (raw DMX only); "RDM labels" = every function was
  // inferred from RDM slot labels, not read from a profile; "Not on wire" =
  // not one of its bytes can be sent (no footprint, no start address, or
  // the footprint lies past the end of the universe).
  function fixtureFlags(entryId) {
    const m = st.models[entryId];
    if (!m) return [];
    const out = [];
    const params = (m.parameters || []).filter(p => !p.virtual);
    if (!m.profiled) out.push('No profile');
    else if (params.length && params.every(p => p.source === 'rdm-inferred')) out.push('RDM labels');
    const un = new Set(m.unaddressable || []);
    let reach = 0;
    for (let o = 1; o <= (m.footprint || 0); o++) if (!un.has(o)) reach++;
    if (!reach) out.push('Not on wire');
    return out;
  }

  // --- building the screen ----------------------------------------------------
  function build() {
    const root = document.getElementById('consoleRoot');
    if (!root || st.built) return;
    st.built = true;
    els.root = root;
    els.layers = h('div', { class: 'b5-con-chips', role: 'group', 'aria-label': 'Layers shown' });
    els.groups = h('div', { class: 'b5-con-chips', role: 'group', 'aria-label': 'Stored groups' });
    els.add = seg('', false, { 'data-add-mode': '', title: 'Add mode: taps add to the selection or take out of it' }, () => { st.addMode = !st.addMode; renderTools(); });
    // I2b §4: Lasso is a mode toggle, so an ordinary touch drag still
    // scrolls the plan; with Lasso ON a one-finger drag draws the box.
    els.lassoBtn = seg('', false, { 'data-lasso-toggle': '', title: 'Lasso: drag on the plan to select every fixture inside the box. Escape turns it off.' }, () => setLasso(!st.lasso));
    els.selAll = btn('Select all', { cls: 'b5-btn--sm', icon: 'select-all', attrs: { 'data-select-all': '' } }, () => selectAll());
    els.selNone = btn('Select none', { cls: 'b5-btn--sm', icon: 'select-none', attrs: { 'data-select-none': '' } }, () => sel({ action: 'none' }));
    els.invert = btn('Invert', { cls: 'b5-btn--sm', icon: 'select-invert', attrs: { 'data-select-invert': '', title: 'Invert the selection of the fixtures on the layers shown' } }, () => selectInvert());
    els.ghostBtn = seg('', false, { 'data-ghost-toggle': '', title: 'Ghosts: draw the layers not shown as dashed outlines. They cannot be selected.' }, () => { st.ghosts = !st.ghosts; renderTools(); renderGrid(); });
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
    // I2b §3: the visible text order list (selection order is what Fan
    // uses); the total line is a polite live region.
    els.orderTotal = h('p', { class: 'b5-caption b5-con-order__total', role: 'status', 'data-order-total': '' });
    els.orderList = h('ol', { class: 'b5-con-order__list', 'data-order-list': '' });
    els.order = h('section', { class: 'b5-con-order', 'aria-labelledby': 'conOrderHead' },
      h('h3', { class: 'b5-con-layername', id: 'conOrderHead', text: 'Selection order' }), els.orderTotal, els.orderList);
    els.lassoBox = h('div', { class: 'b5-con-lassobox', 'data-lasso-box': '', 'aria-hidden': 'true', hidden: true }, h('span', { class: 'b5-con-lassobox__word', text: 'Lasso' }));
    els.edit = h('div', { class: 'b5-con-edit', 'data-editpanel': '', hidden: true });
    els.summary = h('section', { class: 'b5-actionbar b5-con-summary', 'aria-label': 'Selection', 'data-summary': '' });
    // I2d §15: the action bar's Clear / Fan / Lowlight-level panel. Built
    // once and kept OUT of the summary, which redraws on every programmer
    // broadcast — a half-typed fan must survive another browser's write.
    els.actPanel = h('section', { class: 'b5-con-actpanel', id: 'conActPanel', 'data-act-panel': '', hidden: true });
    els.actPanel.addEventListener('keydown', ev => { if (ev.key === 'Escape' && st.pop) { if (ev.preventDefault) ev.preventDefault(); closePop(true); } });
    append(root, [
      h('div', { class: 'b5-page-header' },
        h('h1', { class: 'b5-page-header__title', text: 'Console' }),
        h('span', { class: 'b5-page-header__meta b5-caption', text: 'Select fixtures on the plan. Leaving this screen keeps output, the programmer and the selection as they are.' })),
      h('section', { class: 'b5-toolbar b5-con-toolbar', 'aria-label': 'Selection tools' },
        h('div', { class: 'b5-toolbar__row b5-con-row' }, h('span', { class: 'b5-con-rowlabel', text: 'Layers' }), els.layers, els.ghostBtn),
        h('div', { class: 'b5-toolbar__row b5-con-row b5-con-tools', role: 'group', 'aria-label': 'Select' }, els.add, els.lassoBtn, els.selAll, els.selNone, els.invert,
          h('span', { class: 'b5-con-sep', 'aria-hidden': 'true' }), els.zoomOut, els.zoomIn, els.reload, els.editToggle),
        h('div', { class: 'b5-toolbar__row b5-con-row' }, h('span', { class: 'b5-con-rowlabel', text: 'Groups' }), els.groups)),
      els.status,
      els.modebar,
      // I2a: the selection summary sits in flow ABOVE the grid. It was a
      // sticky bottom bar after the panes, so at rest it covered the top
      // of the grid whenever the page was taller than the room left by the
      // strip and the fader bar (1440x900 with the bar open).
      els.summary,
      els.actPanel,
      h('div', { class: 'b5-con-main' },
        h('section', { class: 'b5-con-pane b5-con-pane--grid', 'aria-labelledby': 'conGridHead' },
          h('h2', { class: 'b5-board__head', id: 'conGridHead' },
            h('span', { class: 'b5-board__title', text: 'Selection grid' }),
            h('span', { class: 'b5-board__sub', text: 'the rig seen from above, one plan per height layer' })),
          els.grid, els.picker, els.order, els.lassoBox),
        h('section', { class: 'b5-con-pane b5-con-pane--side', 'aria-label': 'Layout editing and controls' },
          els.edit,
          h('div', { class: 'b5-con-controls', 'data-controls-region': '' },
            h('h2', { class: 'b5-board__head' },
              h('span', { class: 'b5-board__title', text: 'Controls' }),
              h('span', { class: 'b5-board__sub', text: 'dimmer, position, colour, beam, focus, shaper' })),
            h('div', { class: 'b5-empty b5-con-placeholder' },
              h('span', { class: 'b5-empty__title', text: 'Attribute controls arrive next' }),
              h('span', { class: 'b5-empty__body', text: 'This area is reserved for the attribute controls (chunk C6b). The selection you make on the grid is already live in the programmer.' }))))),
    ]);
    // C6c: the Tests panel (console-tests.js) sits under the Controls region.
    els.tests = h('section', { class: 'b5-con-tests', 'data-tests-region': '', 'aria-label': 'Tests' });
    root.querySelector('.b5-con-pane--side').appendChild(els.tests);
    if (typeof ConsoleTests !== 'undefined') ConsoleTests.mount(els.tests, { layers: () => layers() });
    // C7: the Tools panel (console-tools.js: raw universe levels, Universe
    // Identify — the old Send screen's functions) sits under Tests.
    els.tools = h('section', { class: 'b5-con-tests b5-con-tools', 'data-tools-region': '', 'aria-label': 'Tools' });
    root.querySelector('.b5-con-pane--side').appendChild(els.tools);
    if (typeof ConsoleTools !== 'undefined') ConsoleTools.mount(els.tools);
    els.grid.addEventListener('click', onGridClick);
    els.grid.addEventListener('pointerdown', onPointerDown);
    els.grid.addEventListener('pointermove', onPointerMove);
    els.grid.addEventListener('pointerup', onPointerUp);
    els.grid.addEventListener('pointercancel', onPointerCancel);
    els.grid.addEventListener('keydown', onGridKey);
    root.addEventListener('keydown', onLassoKey);
    els.picker.addEventListener('click', onGridClick);
    // C6b: the attribute controls live in console-controls.js and take
    // over the reserved region.
    if (typeof ConsoleControls !== 'undefined') ConsoleControls.mount(root.querySelector('[data-controls-region]'));
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
      st.models = {};
      if (fx && fx.fixtures) fx.fixtures.forEach(f => { st.cells[f.entryId] = f.cells || []; st.models[f.entryId] = f; });
      st.stale = false;
      pruneShown();
      if (typeof ProgrammerSync !== 'undefined') ProgrammerSync.refresh().catch(() => {});
      renderAll();
    } catch (e) {
      status('the layout could not be read (' + e.message + ').', 'error');
    } finally {
      st.loading = false;
      layoutCatchUp();
    }
  }

  // --- live layout sync (C6c) ---------------------------------------------------
  // Every layout edit, from any browser, is broadcast as
  // {"type":"layout","revision":N}. This page re-reads the layout unless it
  // already holds N: its own edit's answer carries the revision, and an
  // announcement that arrives while that edit is still in flight is checked
  // only once the answer lands. A screen not shown just goes stale and
  // reloads on entry.
  let layoutWanted = 0;
  function onLayoutMessage(msg) {
    if (!msg || typeof msg.revision !== 'number') return;
    layoutWanted = Math.max(layoutWanted, msg.revision);
    if (!st.active) { st.stale = true; return; }
    layoutCatchUp();
  }
  function layoutCatchUp() {
    if (!st.active || st.loading || st.editsInFlight) return;
    if (layoutWanted > ((st.layout && st.layout.revision) || 0)) load();
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
    renderOrder();
  }

  function renderTools() {
    if (!els.add) return;
    els.add.className = 'b5-seg' + (st.addMode ? ' is-on' : '');
    els.add.setAttribute('aria-pressed', st.addMode ? 'true' : 'false');
    els.add.textContent = st.addMode ? 'Add mode: ON' : 'Add mode: OFF';
    els.editToggle.className = 'b5-seg' + (st.edit ? ' is-on' : '');
    els.editToggle.setAttribute('aria-pressed', st.edit ? 'true' : 'false');
    els.editToggle.textContent = st.edit ? 'Edit layout: ON' : 'Edit layout: OFF';
    toggleWords(els.lassoBtn, st.lasso, 'lasso', 'Lasso');
    els.lassoBtn.disabled = st.edit;
    toggleWords(els.ghostBtn, st.ghosts, null, 'Ghosts');
    const s = shownLayers();
    const label = !filtered() ? 'Select all fixtures' : s.length === 1 ? 'Select all on ' + s[0].name : 'Select all on ' + s.length + ' layers';
    clear(els.selAll);
    append(els.selAll, [ic('select-all'), label]);
    els.zoomOut.disabled = st.zoom === 0;
    els.zoomIn.disabled = st.zoom === ZOOMS.length - 1;
    els.modebar.hidden = !st.edit;
    els.root.classList.toggle('is-editing', st.edit);
    els.root.classList.toggle('is-lasso', st.lasso && !st.edit);
  }
  // A mode toggle says its state in words: "Lasso: ON".
  function toggleWords(b, on, icon, word) {
    b.className = 'b5-seg' + (on ? ' is-on' : '');
    b.setAttribute('aria-pressed', on ? 'true' : 'false');
    clear(b);
    append(b, [icon ? ic(icon) : null, word + (on ? ': ON' : ': OFF')]);
  }

  function countOn(layerId) {
    return items().filter(it => it.layer === layerId && it.kind !== 'object').length;
  }
  // I2b §4: each layer chip is a 44 px toggle naming its height. "All
  // layers" clears the filter; a layer chip adds that layer to the filter
  // or takes it out (taking the last one out shows every layer again).
  function renderLayers() {
    clear(els.layers);
    if (!st.layout) return;
    els.layers.appendChild(seg('', !filtered(), { 'data-layer-chip': 'all' }, () => setView('all')));
    append(els.layers.lastChild, [ic('layers'), 'All layers']);
    for (const l of layers()) {
      const b = seg('', st.shown.has(l.id), { 'data-layer-chip': l.id }, () => setView(l.id));
      append(b, [h('span', { text: l.name + ' · ' + countOn(l.id) }), h('span', { class: 'b5-con-chipnote', text: ' · ' + heightWords(l) })]);
      els.layers.appendChild(b);
    }
  }
  function setView(v) {
    if (v === 'all') st.shown.clear();
    else if (st.shown.has(v)) st.shown.delete(v);
    else st.shown.add(v);
    st.picker = null;
    renderTools(); renderLayers(); renderGrid(); renderPicker(); renderEdit();
  }
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
      // I2b §4: icon, name and count, then the state in words.
      append(b, [ic('group'), h('span', { text: g.name + ' · ' + s.total }), h('span', { class: 'b5-con-chipnote', text: ' · ' + s.word })]);
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
    // Fixture glyph: 32 px at the two small zooms, 48 px (the design's
    // size) once a cell is 128 px (§3 permits 32-64).
    els.grid.style.setProperty('--b5-con-glyph', (cell >= 128 ? 48 : 32) + 'px');
    // At the smallest zoom a 64 px tile cannot hold a 32 px glyph AND the
    // name and words, so the glyph (decorative) gives way to the words.
    els.grid.classList.toggle('is-compact', cell < 96);
    const stale = staleIds();
    // I2b: filtered-out layers are not drawn — or, with Ghosts ON, drawn as
    // dashed outlines with words that cannot be selected.
    const drawn = layers().filter(l => isShown(l.id) || st.ghosts);
    const colFrom = Math.min(...items().filter(it => !stale.has(it.id) && drawn.some(l => l.id === it.layer)).map(it => it.col), 0);
    for (const ly of drawn) {
      const list = items().filter(it => it.layer === ly.id && !stale.has(it.id));
      const b = bounds(list, colFrom);
      const n = list.filter(it => it.kind !== 'object').length;
      if (!isShown(ly.id)) { els.grid.appendChild(ghostLayer(ly, list, b, cell, n)); continue; }
      const height = ly.system ? 'fixtures with no place on the plan yet' : heightWords(ly);
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
    if (filtered() && !st.ghosts) {
      const hid = layers().length - drawn.length;
      if (hid) els.grid.appendChild(h('p', { class: 'b5-caption b5-con-hiddennote', 'data-hidden-note': '', text: hid + (hid === 1 ? ' layer is' : ' layers are') + ' not shown. Turn Ghosts on to see them as outlines, or tap All layers.' }));
    }
    if (stale.size) els.grid.appendChild(h('p', { class: 'b5-note', text: stale.size + ' layout item(s) point at something no longer in the show and are not drawn; remove them in edit mode or rebuild the layout.' }));
    applySelection();
  }

  // ghostLayer: a filtered-out layer drawn as full-contrast dashed outlines
  // plus words (§3/§4; never faded — the design rejected opacity ghosts on
  // boundary contrast). Nothing in it is a button: ghosts are not
  // selectable while filtered.
  function ghostLayer(ly, list, b, cell, n) {
    const canvas = h('div', { class: 'b5-con-canvas b5-con-canvas--ghost', 'data-ghost-canvas': ly.id,
      style: { width: ((b.c1 - b.c0) * cell) + 'px', height: ((b.r1 - b.r0) * cell) + 'px',
        'background-position': (-b.c0 * cell) + 'px ' + (-b.r0 * cell) + 'px' } });
    for (const it of list.filter(x => x.kind !== 'object')) {
      const isGroup = it.kind === 'group';
      const glyph = isGroup ? 'group' : fixtureClass(it.ref).glyph;
      canvas.appendChild(h('div', { class: 'b5-con-tile b5-con-tile--ghost', 'data-ghost-item': it.id, style: place(it, b, cell) },
        h('div', { class: 'b5-con-tile__main' },
          h('span', { class: 'b5-con-tile__top' }, h('span', { class: 'b5-con-glyph', 'aria-hidden': 'true', icon: 'fx-' + glyph }),
            h('span', { class: 'b5-con-flags b5-con-ghostword', text: 'Outside layer' })),
          h('span', { class: 'b5-con-tile__name', text: itemName(it) }))));
    }
    return h('section', { class: 'b5-con-layer b5-con-layer--ghost', 'data-ghost-layer': ly.id, 'aria-label': 'Layer ' + ly.name + ', not shown: ghosts, not selectable' },
      h('div', { class: 'b5-con-layerhead' },
        h('h3', { class: 'b5-con-layername' }, ly.name, h('span', { class: 'b5-caption', text: ' · ' + n + (n === 1 ? ' item' : ' items') + ' · outside the layers shown · not selectable' }))),
      h('div', { class: 'b5-con-canvaswrap' }, canvas));
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
    // I2b §3: the class glyph from capabilities (fixtureClass), the order
    // badge in the glyph's reserved top-left corner, and the flag words
    // that go with a dashed (no profile, RDM labels) or struck (not on
    // wire) body. The button's accessible name is set in applySelection.
    const cls = fixtureClass(it.ref);
    const flags = fixtureFlags(it.ref);
    // Meta is one line on the tile: the unplaced words lead so they are
    // never the part cut off; the full text is in the accessible name.
    const unplacedWords = markers.filter(m => m !== 'auto');
    const metaText = unplacedWords.concat([e.fixtureNumber ? '#' + e.fixtureNumber : 'no number'], markers.filter(m => m === 'auto')).join(' · ');
    const main = h('button', { type: 'button', class: 'b5-con-tile__main', 'data-select-entry': it.ref, 'aria-pressed': 'false',
      'data-glyph': cls.glyph, 'data-class-word': cls.word, 'data-meta': metaText, 'data-flags': flags.join(', ') },
      h('span', { class: 'b5-con-tile__top' },
        h('span', { class: 'b5-con-glyph b5-con-glyph--' + cls.glyph, 'aria-hidden': 'true', title: (e.fixtureType || 'fixture type not known') + ' · ' + cls.word, icon: 'fx-' + cls.glyph,
          style: { transform: 'rotate(' + (it.rot || 0) + 'deg)' } }),
        h('span', { class: 'b5-con-badge', 'data-badge': '', 'aria-hidden': 'true', hidden: true }),
        flags.length ? h('span', { class: 'b5-con-flags', 'aria-hidden': 'true', 'data-flag-words': '', text: flags.join(' · ') }) : null),
      h('span', { class: 'b5-con-tile__name', text: e.name || 'Unnamed fixture' }),
      h('span', { class: 'b5-con-tile__meta b5-con-tile__meta--line', title: metaText }, h('span', { text: metaText })),
      h('span', { class: 'b5-visually-hidden', 'data-selword': '' }));
    const flagCls = (flags.includes('No profile') || flags.includes('RDM labels') ? ' is-unprofiled' : '') + (flags.includes('Not on wire') ? ' is-offwire' : '') + (it.layer === 'unplaced' ? ' is-unplaced' : '');
    const tile = h('div', { class: 'b5-con-tile b5-con-tile--entry' + (cells.length ? ' has-cells' : '') + flagCls, 'data-item-id': it.id, 'data-kind': 'entry', 'data-entry-id': it.ref, style: place(it, b, cell) }, main);
    if (cells.length) {
      const tm = touchMin();
      const w = it.w * cell - 8, hgt = it.h * cell;
      if (hgt >= 2 * tm && cells.length * tm <= w) {
        const strip = h('div', { class: 'b5-con-cells', role: 'group', 'aria-label': 'Cells of ' + (e.name || 'fixture') });
        cells.forEach(c => strip.appendChild(h('button', { type: 'button', class: 'b5-con-cell', 'data-select-cell': c.id, 'data-entry-id': it.ref, 'aria-pressed': 'false',
          'data-cell-name': (e.name || 'fixture') + ', cell ' + c.index + ' of ' + cells.length + (c.name ? ' (' + c.name + ')' : '') },
          h('span', { class: 'b5-con-cell__n', text: String(c.index) }), h('span', { class: 'b5-con-badge b5-con-badge--cell', 'data-badge': '', 'aria-hidden': 'true', hidden: true }))));
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
    // I2b §3: the group glyph carries its member count as HTML text.
    const main = h('button', { type: 'button', class: 'b5-con-tile__main', 'data-select-group': it.ref, 'aria-pressed': 'false' },
      h('span', { class: 'b5-con-tile__top' },
        h('span', { class: 'b5-con-glyph b5-con-glyph--group', 'aria-hidden': 'true', icon: 'fx-group' }),
        h('span', { class: 'b5-con-glyph__count', 'aria-hidden': 'true', text: String(n) })),
      h('span', { class: 'b5-con-tile__name', text: g ? g.name : 'Group no longer stored' }),
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
    const p = prog();
    // Highlight acts on the selection (C4b): selected marks gain a double
    // outline and the word "highlight" in their accessible name.
    const hl = !!(p && p.highlight && p.highlight.on);
    const word = n => 'selected, order ' + n + (hl ? ', highlight' : '');
    els.grid.querySelectorAll('[data-select-entry]').forEach(b => {
      const id = b.getAttribute('data-select-entry');
      const n = order.get(key(id, '')) || 0;
      const tile = b.closest('[data-item-id]');
      const total = (st.cells[id] || []).length;
      const cellsSel = (st.cells[id] || []).filter(c => order.has(key(id, c.id))).length;
      b.setAttribute('aria-pressed', n ? 'true' : 'false');
      tile.classList.toggle('is-selected', !!n);
      tile.classList.toggle('is-highlight', hl && !!n);
      tile.classList.toggle('has-cells-selected', !!cellsSel);
      tile.classList.toggle('is-edit-target', st.edit && st.editId === tile.getAttribute('data-item-id'));
      setBadge(b.querySelector('[data-badge]'), n);
      const selText = n ? word(n) : 'not selected';
      const cellText = cellsSel ? cellsSel + ' of ' + total + ' cells selected' : '';
      const sw = b.querySelector('[data-selword]');
      if (sw) sw.textContent = ', ' + selText + (cellText ? ', ' + cellText : '');
      // §3 accessible name: "Pixel bar 07, multi-cell, selected, order 4,
      // #7 · auto, RDM labels" — the visible name first.
      const name = (tile.querySelector('.b5-con-tile__name') || {}).textContent || '';
      b.setAttribute('aria-label', [name, b.getAttribute('data-class-word'), selText, cellText, b.getAttribute('data-meta'), b.getAttribute('data-flags')].filter(Boolean).join(', '));
      const lbl = tile.querySelector('[data-cells-label]');
      if (lbl) lbl.textContent = cellsSel ? cellsSel + ' of ' + total + ' cells selected' : total + ' cells';
    });
    els.grid.querySelectorAll('[data-select-cell]').forEach(b => {
      const n = order.get(key(b.getAttribute('data-entry-id'), b.getAttribute('data-select-cell'))) || 0;
      b.setAttribute('aria-pressed', n ? 'true' : 'false');
      b.classList.toggle('is-selected', !!n);
      b.classList.toggle('is-highlight', hl && !!n);
      setBadge(b.querySelector('[data-badge]'), n);
      // "Pixel bar 07, cell 2 of 4 (Red), selected, order 4"
      b.setAttribute('aria-label', b.getAttribute('data-cell-name') + ', ' + (n ? word(n) : 'not selected'));
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
      if (gw) gw.textContent = 'group · ' + s.word;
      const name = (tile.querySelector('.b5-con-tile__name') || {}).textContent || '';
      b.setAttribute('aria-label', name + ', group of ' + s.total + ', ' + s.word);
    });
  }
  // The order badge is decorative text (§3): the accessible name already
  // says "order n"; the 3 px outline, not the badge's colour, says selected.
  function setBadge(el, n) {
    if (!el) return;
    el.hidden = !n;
    el.textContent = n ? String(n) : '';
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
    const g = groupById(id);
    const s = groupState(g);
    const removing = adding(ev) && s.total > 0 && s.n === s.total;
    const done = adding(ev) ? sel({ action: removing ? 'remove' : 'add', group: id }) : sel({ action: 'set', group: id });
    // §4: selecting a group shows any members the layer filter hides.
    if (removing || !filtered()) return done;
    return done.then(() => {
      const out = groupMembers(g).filter(m => { const e = entryById(m.entryId); const it = e && itemById(e.itemId); return it && !isShown(it.layer); });
      if (out.length) status(out.length + ' of ' + s.total + ' members of ' + g.name + ' are on layers not shown; they are selected too: ' +
        out.map(m => { const e = entryById(m.entryId); return (e && e.name) || 'fixture'; }).join(', ') + '.');
    });
  }
  function selectLayer(id, ev) { return sel({ action: adding(ev) ? 'add' : 'set', layer: id }); }
  // visibleTargets: what a layer selection would give for every layer shown,
  // in layer order — the same reading order (row, column, stacking) and
  // group expansion the server uses for one layer (programmer.go
  // layerTargets) — so Select all and Invert respect the filter (§4).
  function visibleTargets(entriesOnly) {
    const out = [], seen = new Set();
    const add = t => { const k = key(t.entryId, t.cell); if (!seen.has(k)) { seen.add(k); out.push({ entryId: t.entryId, cell: t.cell || '' }); } };
    const stale = staleIds();
    for (const ly of shownLayers()) {
      const list = items().filter(it => it.layer === ly.id && it.kind !== 'object' && !stale.has(it.id))
        .sort((a, c) => a.row - c.row || a.col - c.col || a.order - c.order);
      for (const it of list) {
        if (it.kind === 'entry') add({ entryId: it.ref, cell: '' });
        else if (!entriesOnly) groupMembers(groupById(it.ref)).forEach(add);
      }
    }
    return out;
  }
  function selectAll() {
    if (!filtered()) return sel({ action: 'all' });
    const s = shownLayers();
    if (s.length === 1) return selectLayer(s[0].id);
    return sel({ action: 'set', targets: visibleTargets(false) });
  }
  // Invert: every fixture on the layers shown goes in or out (§4).
  function selectInvert() {
    const t = visibleTargets(true);
    if (!t.length) return status('There are no fixtures on the layers shown.', 'error');
    return sel({ action: 'toggle', targets: t });
  }

  // --- lasso (I2b §4) -----------------------------------------------------------
  // With Lasso ON a drag on the grid (any pointer: one finger, pen, mouse)
  // draws a box; on release every fixture tile on a layer shown that the
  // box touches is selected (Add mode or Shift/Ctrl/Cmd: added), in reading
  // order. The grid is touch-action:none only while Lasso is ON (CSS), so
  // with it OFF a finger drag scrolls the plan as before. Escape, or a
  // pointer cancel, drops the box; Escape also turns Lasso off.
  let lasso = null;
  let swallowClick = false;
  function setLasso(on) {
    st.lasso = !!on && !st.edit;
    lassoEnd();
    renderTools();
  }
  function lassoEnd() {
    lasso = null;
    if (els.lassoBox) els.lassoBox.hidden = true;
  }
  function lassoDown(ev) {
    if (ev.button !== undefined && ev.button !== 0) return;
    lasso = { pointerId: ev.pointerId, x0: ev.clientX, y0: ev.clientY, x1: ev.clientX, y1: ev.clientY, moved: false };
    try { els.grid.setPointerCapture(ev.pointerId); } catch (_) { /* not capturable (tests) */ }
  }
  function lassoRect() {
    return { left: Math.min(lasso.x0, lasso.x1), top: Math.min(lasso.y0, lasso.y1), right: Math.max(lasso.x0, lasso.x1), bottom: Math.max(lasso.y0, lasso.y1) };
  }
  function lassoMove(ev) {
    if (!lasso || ev.pointerId !== lasso.pointerId) return;
    lasso.x1 = ev.clientX; lasso.y1 = ev.clientY;
    if (!lasso.moved && Math.hypot(lasso.x1 - lasso.x0, lasso.y1 - lasso.y0) < DRAG_SLOP) return;
    lasso.moved = true;
    if (ev.preventDefault) ev.preventDefault();
    const r = lassoRect();
    els.lassoBox.hidden = false;
    ['left', 'top'].forEach(k => els.lassoBox.style.setProperty(k, r[k] + 'px'));
    els.lassoBox.style.setProperty('width', (r.right - r.left) + 'px');
    els.lassoBox.style.setProperty('height', (r.bottom - r.top) + 'px');
  }
  function lassoUp(ev) {
    if (!lasso || ev.pointerId !== lasso.pointerId) return;
    lasso.x1 = ev.clientX; lasso.y1 = ev.clientY;
    const moved = lasso.moved || Math.hypot(lasso.x1 - lasso.x0, lasso.y1 - lasso.y0) >= DRAG_SLOP;
    const r = lassoRect();
    lassoEnd();
    try { els.grid.releasePointerCapture(ev.pointerId); } catch (_) { /* fine */ }
    if (!moved) return; // a tap: the click selects as usual
    // The click that follows a drag must not also select the tile it ends on.
    swallowClick = true;
    setTimeout(() => { swallowClick = false; }, 0);
    const hit = new Set();
    els.grid.querySelectorAll('[data-layer-block] [data-kind="entry"]').forEach(t => {
      const b = t.getBoundingClientRect();
      if (b.width && b.left < r.right && b.right > r.left && b.top < r.bottom && b.bottom > r.top) hit.add(t.getAttribute('data-entry-id'));
    });
    const targets = visibleTargets(true).filter(t => hit.has(t.entryId));
    if (!targets.length) return status('The lasso box touched no fixtures on the layers shown.', 'error');
    return sel({ action: adding(ev) ? 'add' : 'set', targets });
  }
  function onLassoKey(ev) {
    if (ev.key !== 'Escape' || !st.lasso) return;
    setLasso(false);
  }

  function onGridClick(ev) {
    if (swallowClick) { swallowClick = false; return; }
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
    if (st.edit) { st.lasso = false; lassoEnd(); } // edit drags move items, never lasso
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
    if (!st.edit) { if (st.lasso) lassoDown(ev); return; }
    if (ev.button !== undefined && ev.button !== 0) return;
    const hold = dragHolder(ev);
    if (!hold) return;
    const it = itemById(hold.id);
    if (!it) return;
    drag = { id: it.id, el: hold.el, pointerId: ev.pointerId, x0: ev.clientX, y0: ev.clientY, moved: false };
    try { els.grid.setPointerCapture(ev.pointerId); } catch (_) { /* not capturable (tests) */ }
  }
  function onPointerMove(ev) {
    if (lasso) return lassoMove(ev);
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
  function onPointerCancel() { endDrag(); lassoEnd(); }
  function onPointerUp(ev) {
    if (lasso) return lassoUp(ev);
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
    st.editsInFlight = (st.editsInFlight || 0) + 1; // C6c live sync
    try {
      const resp = await Api.layoutAction(action, body);
      st.layout = resp;
      if (st.editId && !itemById(st.editId)) st.editId = null;
      pruneShown();
      renderAll();
      status(typeof done === 'function' ? done(resp) : done);
      return resp;
    } catch (e) {
      status(e.message, 'error');
      return null;
    } finally {
      st.editsInFlight--;
      layoutCatchUp();
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

  // --- summary bar = the action bar (I2d, component-specs §15) -----------------
  // Status (count + names) then: Clear… (scope chooser), More (phone), and
  // the tools — Highlight ON/OFF with Previous/Next through the selection,
  // Lowlight, Locate, Fan…, Tests, Store group. On a phone the bar stays one
  // line (C6c): count, Clear…, More; the tools sit in More.
  // The bar redraws on every broadcast, so the focused control is found
  // again by its data-* key afterwards (Next can be pressed repeatedly).
  const BAR_KEYS = ['data-hl-prev', 'data-hl-next', 'data-hl-all', 'data-highlight', 'data-lowlight', 'data-lowlight-level-open', 'data-locate',
    'data-clear-open', 'data-fan-open', 'data-tests-jump', 'data-store-group', 'data-summary-more-toggle'];
  function focusKey() {
    const a = document.activeElement;
    if (!a || !els.summary.contains(a)) return null;
    const k = BAR_KEYS.find(x => a.hasAttribute(x));
    return k ? [k, a.getAttribute(k)] : null;
  }
  function renderSummary() {
    if (!els.summary) return;
    const fk = focusKey();
    clear(els.summary);
    const p = prog();
    const s = selection();
    const cells = s.filter(x => x.cell).length;
    const hv = (p && p.highlight) || {};
    const hl = !!hv.on;
    const label = x => (x.name || 'Unnamed fixture') + (x.cell ? ' cell ' + (x.cellIndex || '?') + (x.cellName ? ' (' + x.cellName + ')' : '') : '');
    const shown = s.slice(0, 8).map((x, i) => (i + 1) + ' ' + label(x));
    const names = s.length ? shown.join(' · ') + (s.length > 8 ? ' · +' + (s.length - 8) + ' more' : '') : 'Tap a fixture, a cell, a group or a layer to select it.';
    const more = summaryTools(s, hv, label);
    append(els.summary, [
      h('div', { class: 'b5-actionbar__status b5-con-summary__status' },
        h('span', { class: 'b5-actionbar__title', text: 'Selection' }),
        h('span', { class: 'b5-pill b5-pill--md' + (s.length ? ' b5-pill--accent' : ''), 'data-sel-count': '' },
          ic(s.length ? 'status-ok' : 'status-pending'),
          s.length ? s.length + ' selected' + (cells ? ' · ' + cells + (cells === 1 ? ' cell' : ' cells') : '') : 'Nothing selected'),
        h('span', { class: 'b5-con-summary__names', 'data-sel-names': '', title: s.map((x, i) => (i + 1) + ' ' + label(x)).join(', ') }, names)),
      h('div', { class: 'b5-actionbar__buttons b5-con-summary__buttons' },
        btn('Clear…', { icon: 'clear', cls: 'b5-con-act' + (st.pop === 'clear' ? ' is-on' : ''), attrs: { 'data-clear-open': '', 'aria-expanded': st.pop === 'clear' ? 'true' : 'false', 'aria-controls': 'conActPanel' } }, () => togglePop('clear')),
        btn(hl ? 'More · Highlight ON' : 'More', { cls: 'b5-con-summary__morebtn', attrs: { 'data-summary-more-toggle': '', 'aria-expanded': st.moreOpen ? 'true' : 'false', 'aria-controls': 'conSummaryMore' } },
          () => { st.moreOpen = !st.moreOpen; renderSummary(); }),
        more),
    ]);
    if (fk) {
      const back = els.summary.querySelector('[' + fk[0] + ']');
      if (back && !back.disabled) back.focus();
    }
  }

  // --- selection order list (I2b §3) ---------------------------------------------
  // The visible text list of the selection in order — the order Fan uses.
  // The total line is a polite live region; it is rewritten only when the
  // words change, so programmer broadcasts do not repeat the announcement.
  function renderOrder() {
    if (!els.orderList) return;
    const s = selection();
    const cells = s.filter(x => x.cell).length;
    const fixtures = s.length - cells;
    const total = !s.length ? 'Nothing selected. Tap a fixture, a cell, a group or a layer.'
      : [fixtures ? fixtures + (fixtures === 1 ? ' fixture' : ' fixtures') : '', cells ? cells + (cells === 1 ? ' cell' : ' cells') : ''].filter(Boolean).join(' · ') + ' selected, in this order';
    if (els.orderTotal.textContent !== total) els.orderTotal.textContent = total;
    clear(els.orderList);
    s.forEach((x, i) => els.orderList.appendChild(h('li', { class: 'b5-con-order__item', 'data-order-item': x.entryId + (x.cell ? '/' + x.cell : '') },
      h('span', { class: 'b5-con-order__n', text: String(i + 1) }),
      h('span', { text: (x.name || 'Unnamed fixture') + (x.cell ? ' · cell ' + (x.cellIndex || '?') + (x.cellName ? ' (' + x.cellName + ')' : '') : '') }))));
    els.orderList.hidden = !s.length;
  }

  // summaryTools: the tools inline on wide screens, behind More on a phone.
  function summaryTools(s, hv, label) {
    const hl = !!hv.on;
    const n = s.length;
    const step = hl && typeof hv.step === 'number' && hv.step < n ? hv.step : null;
    const hlAct = async (body, words) => {
      try { await ProgrammerSync.act('highlight', body); status(words || ''); }
      catch (e) { status(e.message, 'error'); }
    };
    // §15: Previous/Next follow selection order and do not wrap; the step
    // line says where Highlight is and "last of n" at the end.
    const stepWords = step === null ? 'Highlight: whole selection (' + n + ')'
      : 'Highlight: ' + (step + 1) + ' of ' + n + ' · ' + label(s[step]) + (step === n - 1 ? ' · last of ' + n : step === 0 ? ' · first' : '');
    const stepping = hl ? h('div', { class: 'b5-con-hlstep', role: 'group', 'aria-label': 'Highlight one at a time' },
      btn('Previous', { icon: 'seq-back', attrs: { 'data-hl-prev': '' }, disabled: !n }, () => hlAct({ step: 'previous' })),
      btn('Next', { icon: 'seq-next', attrs: { 'data-hl-next': '' }, disabled: !n }, () => hlAct({ step: 'next' })),
      step !== null ? btn('All', { attrs: { 'data-hl-all': '', title: 'Highlight the whole selection again' } }, () => hlAct({ step: 'all' })) : null,
      h('span', { class: 'b5-caption b5-con-hlstep__words', role: 'status', 'data-hl-step': '' }, stepWords)) : null;
    const pct = hv.lowlightPercent !== undefined ? hv.lowlightPercent : 20;
    const more = h('div', { class: 'b5-con-summary__more' + (st.moreOpen ? ' is-open' : ''), id: 'conSummaryMore', 'data-summary-more': '' },
        seg([ic('highlight'), hl ? 'Highlight: ON' : 'Highlight: OFF'], hl, { 'data-highlight': '' }, () => hlAct({ highlight: !hl })),
        stepping,
        seg([ic('lowlight'), hv.lowlight ? 'Lowlight: ON · ' + pct + ' %' : 'Lowlight: OFF'], !!hv.lowlight, { 'data-lowlight': '', title: 'Lowlight dims every fixture that is not highlighted, while Highlight is ON' },
          () => hlAct({ lowlight: !hv.lowlight })),
        btn('Level ' + pct + ' %…', { cls: 'b5-con-act' + (st.pop === 'lowlight' ? ' is-on' : ''), attrs: { 'data-lowlight-level-open': '', 'aria-expanded': st.pop === 'lowlight' ? 'true' : 'false', 'aria-controls': 'conActPanel' } }, () => togglePop('lowlight')),
        btn('Locate', { attrs: { 'data-locate': '' }, disabled: !n, icon: 'locate' }, async () => {
          try { await ProgrammerSync.act('locate', {}); status('Located the selection: open white, centred (reaches the rig only while output is armed).'); }
          catch (e) { status(e.message, 'error'); }
        }),
        btn('Fan…', { icon: 'fan-linear', cls: 'b5-con-act' + (st.pop === 'fan' ? ' is-on' : ''), attrs: { 'data-fan-open': '', 'aria-expanded': st.pop === 'fan' ? 'true' : 'false', 'aria-controls': 'conActPanel' }, disabled: n < 2 }, () => togglePop('fan')),
        btn('Tests', { icon: 'test', attrs: { 'data-tests-jump': '', title: 'Go to the Tests panel' } }, () => {
          if (!els.tests) return;
          if (els.tests.scrollIntoView) els.tests.scrollIntoView({ block: 'start' });
          const f = els.tests.querySelector('button, summary, [tabindex]');
          if (f) f.focus();
        }),
        btn('Store group…', { icon: 'store-group', attrs: { 'data-store-group': '' }, disabled: !n }, async () => {
          const name = await ask({ title: 'Store the selection as a group', body: 'The group keeps the fixtures and cells in this selection order.', field: { label: 'Group name', value: '', maxLength: 80 }, ok: 'Store group' });
          if (name === null) return;
          // §14: an existing name asks Replace / Merge / Save as new.
          const same = (((prog() || {}).storedGroups) || []).find(g => g.name.toLowerCase() === name.toLowerCase());
          let action = 'groups/store', body = { name }, words = 'Stored group ' + name + '.';
          if (same) {
            const c = await choose({ title: 'A group named "' + same.name + '" exists', body: 'Merge adds this selection after its members; Replace makes it exactly this selection; Save as new keeps it.',
              choices: [{ label: 'Save as new', value: 'new' }, { label: 'Merge into "' + same.name + '"', value: 'merge' }, { label: 'Replace "' + same.name + '"', value: 'replace', cls: 'b5-con-caution' }] });
            if (c === null) return;
            if (c === 'merge') { action = 'groups/merge'; body = { id: same.id }; words = 'Merged the selection into ' + same.name + '.'; }
            if (c === 'replace') { action = 'groups/update'; body = { id: same.id }; words = 'Replaced ' + same.name + ' with the selection.'; }
          }
          try { await ProgrammerSync.act(action, body); status(words); }
          catch (e) { status(e.message, 'error'); }
        }));
    // Picking a tool closes the phone menu — except stepping, which is
    // pressed again and again.
    more.addEventListener('click', ev => {
      if (ev.target && ev.target.closest && ev.target.closest('[data-hl-prev], [data-hl-next], [data-hl-all]')) return;
      st.moreOpen = false; more.classList.remove('is-open');
    });
    more.addEventListener('keydown', ev => {
      if (ev.key !== 'Escape' || !st.moreOpen) return;
      st.moreOpen = false;
      renderSummary();
      const t = els.summary.querySelector('[data-summary-more-toggle]');
      if (t) t.focus();
    });
    return more;
  }

  // --- the action panel (Clear scopes, Fan, Lowlight level) --------------------
  function togglePop(kind) {
    // The phone's More menu closes when a tool opens its panel. Set here,
    // before the bar redraws: the menu's own click listener runs after this
    // handler, on the element the redraw just replaced.
    st.moreOpen = false;
    if (st.pop === kind) return closePop(true);
    st.pop = kind;
    renderPop();
    renderSummary();
    const f = els.actPanel.querySelector('[data-act-first]') || els.actPanel.querySelector('button, select, input');
    if (f) f.focus();
  }
  function closePop(refocus) {
    const kind = st.pop;
    st.pop = null;
    renderPop();
    renderSummary();
    if (!refocus) return;
    const opener = { clear: '[data-clear-open]', fan: '[data-fan-open]', lowlight: '[data-lowlight-level-open]' }[kind];
    const o = opener && els.summary.querySelector(opener);
    if (o && !(o.offsetParent === null && o.getClientRects && !o.getClientRects().length)) o.focus();
    else { const m = els.summary.querySelector('[data-summary-more-toggle]'); if (m) m.focus(); }
  }
  function renderPop() {
    if (!els.actPanel) return;
    clear(els.actPanel);
    const A = typeof ConsoleControls !== 'undefined' && ConsoleControls.actions;
    if (!st.pop || !A || !selection().length) { st.pop = st.pop && selection().length ? st.pop : null; els.actPanel.hidden = true; return; }
    els.actPanel.hidden = false;
    const done = () => closePop(false);
    const close = btn('Close', { attrs: { 'data-act-close': '' } }, () => closePop(true));
    const fam = A.label();
    const content = {
      clear: () => [h('h3', { id: 'conActHead', text: 'Clear programmer values' }),
        h('p', { class: 'b5-caption', text: 'Clearing hands each channel back to what is underneath: a group fader, a running test, or the profile default. Nothing else to apply.' }),
        A.clearScopes(done)],
      fan: () => [h('h3', { id: 'conActHead', text: 'Fan ' + (fam ? fam + ' ' : '') + 'in selection order' }), A.fanForm(done)],
      lowlight: () => [h('h3', { id: 'conActHead', text: 'Lowlight level' }), A.lowlightForm(done)],
    }[st.pop];
    els.actPanel.setAttribute('aria-labelledby', 'conActHead');
    append(els.actPanel, [...content(), h('div', { class: 'b5-row b5-con-actpanel__close' }, close)]);
  }
  // ConsoleControls calls this after it redraws (the family or the
  // selection's attributes may have changed under an open panel).
  function actionsChanged() { if (st.pop && st.pop !== 'lowlight' && !(els.actPanel && els.actPanel.contains(document.activeElement) && st.pop === 'fan')) renderPop(); }

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
        renderOrder();
        if (st.edit) renderEdit();
        if (typeof ConsoleControls !== 'undefined') ConsoleControls.refresh();
      });
    }
    window.addEventListener('b5-show-changed', () => {
      st.stale = true; st.editId = null; st.picker = null;
      if (st.active) load();
    });
    if (typeof Live !== 'undefined') Live.on('layout', onLayoutMessage); // C6c
  }
  function onEnterScreen() {
    init();
    st.active = true;
    renderAll();
    load();
    if (typeof ConsoleTools !== 'undefined') ConsoleTools.onEnter();
  }
  // Leaving changes nothing on the wire: no stop, no blackout, no deselect.
  // An unfinished drag is dropped (it was never sent).
  function onLeaveScreen() {
    st.active = false;
    endDrag();
    lassoEnd();
  }

  return { init, onEnterScreen, onLeaveScreen, load, actionsChanged, _state: st };
})();
