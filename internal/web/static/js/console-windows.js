// console-windows.js — K3 (W1 component-specs §4 and §8; owner answers
// 2026-10-18/19): the Console's docked and floating windows, from 1024 px.
//
// WHAT IT DOES. console.js hands each window a way to put its regions into
// the window (attach) and a way to put them back where they live on narrow
// screens (home). From 1024 px every window is either DOCKED in the
// right-edge column — one browser-local width, set by dragging the column's
// edge or with Left/Right on it (20 px), 320 px to 60 % of the workspace —
// or FLOATING over the grid: dragged by its title bar or moved with the
// arrow keys (10 px, Shift 40 px), resized from its corner grip (drag or
// arrows, 20 px, at least 280 x 140). Title bars carry Dock/Float, collapse
// and close. A floating window dragged to within 40 px of the right edge
// docks on release; Escape or pointercancel puts it back. Below 1024 px all
// regions go home and the Console keeps its old stacked layout.
//
// WHAT IT REMEMBERS, per browser and never in the show (localStorage, best
// effort): open/hidden, docked/floating, collapsed, floating x/y/width/
// height and the dock width. Reset restores only these choices: Controls
// open and docked at 440 px, the rest hidden. Closing a window only hides
// it — never output, the programmer, the selection or the tests.
//
// The dock is a column of the workspace grid, so the grid never sits under
// it and Fit (console.js) only measures the room left of it; with no open
// docked window the column goes and the grid takes the full width.
const ConsoleWindows = (() => {
  const KEY = 'benny512.console.windows';
  const DOCK_DEFAULT = 440, DOCK_MIN = 320, MIN_W = 280, MIN_H = 140;
  const wins = [];           // dock order = add order
  let ws = null, dock = null, stack = null, edge = null, hint = null, said = null;
  let opts = { change: () => {}, closed: () => null, keep: () => false };
  let wide = false, dockW = DOCK_DEFAULT, saved = {};
  try { saved = JSON.parse(localStorage.getItem(KEY) || '{}') || {}; } catch (_) { saved = {}; }
  if (typeof saved.dockW === 'number') dockW = saved.dockW;

  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    Object.entries(props || {}).forEach(([k, v]) => {
      if (v === undefined || v === null || v === false) return;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = String(v);
      else if (k === 'icon') el.innerHTML = UI.icon(v);
      else el.setAttribute(k, v === true ? '' : String(v));
    });
    kids.forEach(c => { if (c) el.appendChild(typeof c === 'string' ? document.createTextNode(c) : c); });
    return el;
  }
  const ic = name => h('span', { class: 'b5-win__ic', 'aria-hidden': 'true', icon: name });
  function save() {
    const out = { dockW };
    wins.forEach(w => { out[w.id] = { open: w.open, docked: w.docked, collapsed: w.collapsed, x: w.x, y: w.y, w: w.w, h: w.h }; });
    try { localStorage.setItem(KEY, JSON.stringify(out)); } catch (_) { /* best effort */ }
  }
  function rect(el) { return el && el.getBoundingClientRect ? el.getBoundingClientRect() : { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }; }
  function wsSize() { return { w: ws ? ws.clientWidth || 0 : 0, h: ws ? ws.clientHeight || 0 : 0 }; }
  const px = (el, p) => parseFloat(el.style.getPropertyValue(p)) || 0;

  // mount(workspace, dockEl, o): workspace is the positioned box floating
  // windows live in; dockEl the right-edge column. o.change runs after any
  // layout change; o.closed returns where focus goes when a window closes
  // (the Windows menu); o.keep says the column is needed without a docked
  // window (layout editing).
  function mount(workspace, dockEl, o) {
    ws = workspace; dock = dockEl; opts = Object.assign(opts, o || {});
    ws.classList.add('b5-winspace');
    dock.classList.add('b5-dock');
    edge = h('div', { class: 'b5-dock__edge', 'data-dock-edge': '', role: 'separator', tabindex: '0', 'aria-orientation': 'vertical',
      'aria-label': 'Dock width', 'aria-valuemin': String(DOCK_MIN), title: 'Drag to change the dock width, or use the Left and Right arrow keys (20 px)' });
    stack = h('div', { class: 'b5-dock__stack', 'data-dock-stack': '' });
    said = h('span', { class: 'b5-visually-hidden', role: 'status', 'data-dock-said': '' });
    hint = h('div', { class: 'b5-dock__drop', 'data-dock-drop': '', 'aria-hidden': 'true', hidden: true }, h('span', { text: 'Release to dock on the right' }));
    dock.insertBefore(edge, dock.firstChild);
    dock.appendChild(stack);
    dock.appendChild(said);
    ws.appendChild(hint);
    edge.addEventListener('pointerdown', edgeDown);
    edge.addEventListener('keydown', ev => {
      const d = { ArrowLeft: 20, ArrowRight: -20 }[ev.key];
      if (!d || !wide) return;
      if (ev.preventDefault) ev.preventDefault();
      setDockW(dockW + d, true);
    });
  }

  // add(spec): { id, title, attach(inner), home(), open, docked, w, at } —
  // open/docked/w are the defaults when this browser has nothing saved; at
  // ('left' | 'right') is where a never-moved floating window first appears.
  function add(spec) {
    const s = saved[spec.id] || {};
    const w = Object.assign({ id: spec.id, title: spec.title, spec, attached: false,
      open: spec.open !== false, docked: spec.docked !== false, collapsed: false, x: null, y: null, w: spec.w || 340, h: null },
    pick(s, ['open', 'docked', 'collapsed', 'x', 'y', 'w', 'h']));
    const tid = 'b5-win-title-' + spec.id;
    w.sub = h('span', { class: 'b5-win__sub', 'data-win-sub': '' });
    w.bar = h('div', { class: 'b5-win__bar', 'data-win-bar': spec.id, tabindex: '0', role: 'group', 'aria-labelledby': tid },
      h('span', { class: 'b5-win__grip', 'aria-hidden': 'true', icon: 'drag-handle' }),
      h('span', { class: 'b5-win__title', id: tid }, h('span', { class: 'b5-win__name', text: spec.title }), w.sub));
    w.dockBtn = h('button', { type: 'button', class: 'b5-btn b5-btn--sm b5-win__act', 'data-win-dock': spec.id });
    w.foldBtn = h('button', { type: 'button', class: 'b5-btn b5-btn--sm b5-win__act b5-win__iconbtn', 'data-win-fold': spec.id });
    w.closeBtn = h('button', { type: 'button', class: 'b5-btn b5-btn--sm b5-win__act b5-win__iconbtn', 'data-win-close': spec.id,
      'aria-label': 'Hide the ' + spec.title + ' window', title: 'Hide — the Windows menu brings it back' }, ic('close'));
    w.bar.appendChild(h('span', { class: 'b5-win__acts' }, w.dockBtn, w.foldBtn, w.closeBtn));
    w.inner = h('div', { class: 'b5-win__body', 'data-win-body': spec.id });
    w.grip = h('button', { type: 'button', class: 'b5-win__resize', 'data-win-resize': spec.id,
      'aria-label': 'Resize the ' + spec.title + ' window: drag, or use the arrow keys (20 px)' }, ic('resize-grip'));
    w.el = h('section', { class: 'b5-win', 'data-win': spec.id, 'aria-labelledby': tid, hidden: true }, w.bar, w.inner, w.grip);
    w.dockBtn.addEventListener('click', () => { setDocked(w, !w.docked); if (w.dockBtn.focus) w.dockBtn.focus(); });
    w.foldBtn.addEventListener('click', () => { w.collapsed = !w.collapsed; save(); place(); if (w.foldBtn.focus) w.foldBtn.focus(); });
    w.closeBtn.addEventListener('click', () => {
      setOpen(w, false);
      back();
    });
    w.el.addEventListener('pointerdown', () => front(w), true);
    w.el.addEventListener('focusin', () => front(w));
    w.bar.addEventListener('pointerdown', ev => barDown(w, ev));
    w.bar.addEventListener('keydown', ev => barKey(w, ev));
    w.grip.addEventListener('pointerdown', ev => gripDown(w, ev));
    w.grip.addEventListener('keydown', ev => gripKey(w, ev));
    wins.push(w);
    if (wide) place();
    return w;
  }
  function pick(o, keys) { const r = {}; keys.forEach(k => { if (o[k] !== undefined && o[k] !== null) r[k] = o[k]; }); return r; }
  function byId(id) { return wins.find(w => w.id === id) || null; }
  function setSub(id, text) { const w = byId(id); if (w && w.sub.textContent !== text) w.sub.textContent = text; }

  // setWide(on): from 1024 px, windows; narrower, every region home.
  function setWide(on) {
    on = !!on;
    if (on === wide) { if (on) place(); return; }
    wide = on;
    if (!wide) {
      wins.forEach(w => { w.el.hidden = true; if (w.attached) { w.attached = false; w.spec.home(); } });
      hint.hidden = true;
      ws.classList.remove('is-windowed', 'is-dock-empty');
      ws.style.removeProperty('--b5-dock-w');
      opts.change();
      return;
    }
    place();
  }

  // place: every window where its state says (wide only), each clamped
  // inside the workspace — width and height capped first, then x/y.
  function place() {
    if (!wide || !ws) return;
    ws.classList.add('is-windowed');
    const size = wsSize();
    wins.forEach(w => {
      if (!w.attached) { w.spec.attach(w.inner); w.attached = true; }
      w.el.hidden = !w.open;
      w.el.classList.toggle('is-docked', !!w.docked);
      w.el.classList.toggle('is-floating', !w.docked);
      w.el.classList.toggle('is-collapsed', !!w.collapsed);
      clearKids(w.dockBtn);
      w.dockBtn.appendChild(ic(w.docked ? 'float' : 'dock-right'));
      w.dockBtn.appendChild(h('span', { text: w.docked ? 'Float' : 'Dock' }));
      w.dockBtn.setAttribute('aria-label', w.docked ? 'Float the ' + w.title + ' window over the grid' : 'Dock the ' + w.title + ' window on the right');
      w.foldBtn.setAttribute('aria-expanded', w.collapsed ? 'false' : 'true');
      w.foldBtn.setAttribute('aria-label', (w.collapsed ? 'Expand the ' : 'Collapse the ') + w.title + ' window');
      clearKids(w.foldBtn);
      w.foldBtn.appendChild(ic(w.collapsed ? 'window-expand' : 'window-collapse'));
      w.inner.hidden = !!w.collapsed;
      w.grip.hidden = !!w.docked || !!w.collapsed;
      w.bar.setAttribute('aria-description', w.docked ? 'Docked on the right' : 'Floating: the arrow keys move it by 10 px, Shift and the arrow keys by 40 px');
      if (w.docked) {
        if (w.el.parentNode !== stack) stack.appendChild(w.el);
        ['left', 'top', 'width', 'height', 'max-height'].forEach(p => w.el.style.removeProperty(p));
      } else {
        if (w.el.parentNode !== ws) ws.appendChild(w.el);
        const ww = Math.max(MIN_W, Math.min(w.w || 340, Math.max(MIN_W, size.w - 16)));
        let x = typeof w.x === 'number' ? w.x : (w.spec.at === 'right' ? size.w - ww - 12 : 12);
        let y = typeof w.y === 'number' ? w.y : 12;
        x = Math.max(0, Math.min(x, Math.max(0, size.w - ww)));
        y = Math.max(0, Math.min(y, Math.max(0, size.h - 48)));
        w.el.style.setProperty('left', x + 'px');
        w.el.style.setProperty('top', y + 'px');
        w.el.style.setProperty('width', ww + 'px');
        const room = Math.max(MIN_H, size.h - y - 8);
        if (w.h && !w.collapsed) w.el.style.setProperty('height', Math.max(MIN_H, Math.min(w.h, room)) + 'px');
        else w.el.style.removeProperty('height');
        w.el.style.setProperty('max-height', room + 'px');
      }
    });
    // Docked windows keep the stack order (Controls, Selection order, Groups, MIDI, Tests).
    wins.filter(w => w.el.parentNode === stack).forEach(w => stack.appendChild(w.el));
    ws.classList.toggle('is-dock-empty', !wins.some(w => w.open && w.docked) && !opts.keep());
    setDockW(dockW, false);
    opts.change();
  }
  function clearKids(el) { while (el.firstChild) el.removeChild(el.firstChild); }
  function maxDock() { const s = wsSize(); return s.w ? Math.max(DOCK_MIN, Math.floor(s.w * 0.6)) : 9999; }
  function setDockW(v, commit) {
    const max = maxDock();
    dockW = Math.round(Math.max(DOCK_MIN, Math.min(v, max)));
    if (ws) ws.style.setProperty('--b5-dock-w', dockW + 'px');
    edge.setAttribute('aria-valuemax', String(max));
    edge.setAttribute('aria-valuenow', String(dockW));
    edge.setAttribute('aria-valuetext', dockW + ' px (' + DOCK_MIN + ' to ' + max + ')');
    if (commit) { said.textContent = 'Dock width ' + dockW + ' px'; save(); opts.change(); }
  }
  // cascade: a floating window opened without a remembered place lands
  // 32 px down and right of each floating window already open, so a
  // second window never hides the first one's title bar.
  function cascade(w) {
    if (w.docked || typeof w.x === 'number') return;
    const size = wsSize(), ww = Math.max(MIN_W, Math.min(w.w || 340, Math.max(MIN_W, size.w - 16)));
    const k = wins.filter(o => o !== w && o.open && !o.docked).length;
    w.x = (w.spec.at === 'right' ? size.w - ww - 12 - 32 * k : 12 + 32 * k);
    w.y = 12 + 32 * k;
  }
  function setOpen(w, open) {
    w.open = !!open;
    if (w.open) { w.collapsed = false; cascade(w); }
    save(); place();
    if (w.open) { front(w); if (w.bar.focus) w.bar.focus(); }
  }
  // show(id): opens a window from elsewhere (the selection bar's Tests).
  function show(id, focusBar) {
    const w = byId(id);
    if (!w || !wide) return false;
    if (!w.open || w.collapsed) { if (!w.open) cascade(w); w.open = true; w.collapsed = false; save(); place(); }
    front(w);
    if (focusBar !== false && w.bar.focus) w.bar.focus();
    return true;
  }
  // setDocked: Float starts at the former dock edge, inside the workspace.
  function setDocked(w, docked) {
    w.docked = !!docked;
    if (!w.docked) { const size = wsSize(); w.x = Math.max(0, size.w - dockW); w.y = 12; w.h = null; }
    save(); place();
  }
  function front(w) { wins.forEach(x => x.el.classList.toggle('is-front', x === w)); }

  // --- dragging a floating window; release on the right edge docks it -----
  function barDown(w, ev) {
    if (!wide || w.docked || (ev.target && ev.target.closest && ev.target.closest('button'))) return;
    const wr = rect(ws), r = rect(w.el);
    const dx = ev.clientX - r.left, dy = ev.clientY - r.top, x0 = w.x, y0 = w.y;
    let dropping = false;
    if (w.bar.setPointerCapture && ev.pointerId !== undefined) { try { w.bar.setPointerCapture(ev.pointerId); } catch (_) { /* fine */ } }
    const move = e => {
      const x = Math.max(0, Math.min(e.clientX - wr.left - dx, wr.width - r.width));
      const y = Math.max(0, Math.min(e.clientY - wr.top - dy, wr.height - 48));
      w.el.style.setProperty('left', x + 'px'); w.el.style.setProperty('top', y + 'px');
      dropping = e.clientX > wr.right - 40;
      hint.hidden = !dropping;
    };
    const end = cancel => {
      w.bar.removeEventListener('pointermove', move);
      w.bar.removeEventListener('pointerup', up);
      w.bar.removeEventListener('pointercancel', cancelled);
      document.removeEventListener('keydown', esc, true);
      hint.hidden = true;
      if (cancel) { w.x = x0; w.y = y0; place(); return; }
      if (dropping) { setDocked(w, true); return; }
      w.x = px(w.el, 'left'); w.y = px(w.el, 'top');
      save();
    };
    const up = () => end(false);
    const cancelled = () => end(true);
    const esc = e => { if (e.key === 'Escape') { if (e.preventDefault) e.preventDefault(); if (e.stopPropagation) e.stopPropagation(); end(true); } };
    w.bar.addEventListener('pointermove', move);
    w.bar.addEventListener('pointerup', up);
    w.bar.addEventListener('pointercancel', cancelled);
    document.addEventListener('keydown', esc, true);
  }
  function barKey(w, ev) {
    if (!wide || w.docked || ev.target !== w.bar) return;
    const step = ev.shiftKey ? 40 : 10;
    const m = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[ev.key];
    if (!m) return;
    if (ev.preventDefault) ev.preventDefault();
    w.x = px(w.el, 'left') + m[0];
    w.y = px(w.el, 'top') + m[1];
    place();
    w.x = px(w.el, 'left'); w.y = px(w.el, 'top'); // keep the clamped place
    save();
  }
  // --- resizing a floating window -----------------------------------------------
  function gripDown(w, ev) {
    if (!wide || w.docked) return;
    if (ev.preventDefault) ev.preventDefault();
    const wr = rect(ws), r = rect(w.el), sx = ev.clientX, sy = ev.clientY;
    if (w.grip.setPointerCapture && ev.pointerId !== undefined) { try { w.grip.setPointerCapture(ev.pointerId); } catch (_) { /* fine */ } }
    const move = e => {
      w.el.style.setProperty('width', Math.max(MIN_W, Math.min(r.width + e.clientX - sx, wr.right - r.left)) + 'px');
      w.el.style.setProperty('height', Math.max(MIN_H, Math.min(r.height + e.clientY - sy, wr.bottom - r.top - 8)) + 'px');
    };
    const up = () => {
      w.grip.removeEventListener('pointermove', move); w.grip.removeEventListener('pointerup', up);
      w.w = px(w.el, 'width') || w.w; w.h = px(w.el, 'height') || w.h;
      save();
    };
    w.grip.addEventListener('pointermove', move);
    w.grip.addEventListener('pointerup', up);
  }
  function gripKey(w, ev) {
    const m = { ArrowLeft: [-20, 0], ArrowRight: [20, 0], ArrowUp: [0, -20], ArrowDown: [0, 20] }[ev.key];
    if (!m || !wide || w.docked) return;
    if (ev.preventDefault) ev.preventDefault();
    const r = rect(w.el), size = wsSize();
    w.w = Math.max(MIN_W, Math.min((r.width || w.w || 340) + m[0], Math.max(MIN_W, size.w - (w.x || 0))));
    w.h = Math.max(MIN_H, (r.height || w.h || 300) + m[1]);
    save(); place();
  }
  // --- the dock edge -----------------------------------------------------------
  function edgeDown(ev) {
    if (!wide) return;
    if (ev.preventDefault) ev.preventDefault();
    const wr = rect(ws);
    if (edge.setPointerCapture && ev.pointerId !== undefined) { try { edge.setPointerCapture(ev.pointerId); } catch (_) { /* fine */ } }
    const move = e => setDockW(wr.right - e.clientX, false);
    const up = () => { edge.removeEventListener('pointermove', move); edge.removeEventListener('pointerup', up); setDockW(dockW, true); };
    edge.addEventListener('pointermove', move);
    edge.addEventListener('pointerup', up);
  }

  // menuItems: the toolbar's Windows menu — one check item per window with
  // where it is, then Reset window positions.
  function menuItems() {
    return wins.map(w => ({ label: w.title + (w.open ? (w.docked ? ' · docked' : ' · floating') : ''), checked: w.open, attrs: { 'data-win-toggle': w.id },
      onSelect: () => { setOpen(w, !w.open); if (!w.open) back(); } }))
      .concat([{ separator: true }, { label: 'Reset window positions', icon: 'window-reset', attrs: { 'data-win-reset': '' }, onSelect: () => { reset(); back(); } }]);
  }
  function back() { const t = opts.closed && opts.closed(); if (t && t.focus) t.focus(); }
  // reset: the defaults again — only window choices, nothing on the wire.
  function reset() {
    wins.forEach(w => { const d = w.spec; Object.assign(w, { open: d.open !== false, docked: d.docked !== false, collapsed: false, x: null, y: null, w: d.w || 340, h: null }); });
    dockW = DOCK_DEFAULT;
    save(); place();
  }
  return { mount, add, setWide, place, show, setSub, menuItems, reset, isWide: () => wide, _wins: wins, _dockW: () => dockW };
})();
