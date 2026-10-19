
(() => {
'use strict';
const query=new URLSearchParams(location.search),scene=query.get('scene')||'default';
document.documentElement.dataset.theme=query.get('theme')==='light'?'light':'dark';
if(query.get('grey')==='1')document.documentElement.classList.add('greyscale');
if(query.get('dim')==='1')document.documentElement.classList.add('dim-screen');
const icon=name=>'<svg class="ico" aria-hidden="true"><use href="console-workspace-icons.svg#b5-icon-'+name+'"/></svg>';
const oldIcon=name=>'<svg class="ico" aria-hidden="true"><use href="../../../internal/web/static/icons/benny512-icons.svg#b5-icon-'+name+'"/></svg>';
const check=icon('check');
const $ = s => document.querySelector(s);
const $$ = s => [...document.querySelectorAll(s)];
const store = { get(k) { try { return JSON.parse(localStorage.getItem(k)); } catch (_) { return null; } }, set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (_) {} } };

// ---------- Made-up rig (grid columns/rows like the app's layout grid) ----------
const FX = [];
const add = (fid, name, glyph, layer, col, row, w, cells) => FX.push({ id: 'f' + fid, fid, name, glyph, layer, col, row, w: w || 1, cells: cells || 0 });
for (let i = 0; i < 10; i++) add(101 + i, 'Profile', 'fx-profile', 'truss', 2 + i * 2, 0);
for (let i = 0; i < 12; i++) add(201 + i, 'Wash', 'fx-wash', 'truss', 1 + i * 2, 4);
for (let i = 0; i < 8; i++) add(301 + i, 'Beam', 'fx-moving-head', 'truss', 2 + i * 3, 8);
for (let i = 0; i < 6; i++) add(401 + i, 'Pixel bar', 'fx-pixel-bar', 'truss', 1 + i * 4, 9, 2, 4);
for (let i = 0; i < 8; i++) add(501 + i, 'Strobe', 'fx-strobe', 'floor', 2 + i * 3, 1);
for (let i = 0; i < 6; i++) add(601 + i, 'Uplight', 'fx-wash', 'floor', 3 + i * 4, 5);
// The truss layer count says 44; 10+12+8+6 = 36 fixtures + 8 extra profiles off to the side keeps the plan realistic.
for (let i = 0; i < 4; i++) add(111 + i, 'Side profile', 'fx-profile', 'truss', 0, 2 + i * 2);
for (let i = 0; i < 4; i++) add(115 + i, 'Side profile', 'fx-profile', 'truss', 24, 2 + i * 2);
const byId = Object.fromEntries(FX.map(f => [f.id, f]));
const LAYERS = { truss: 'Truss 6.4 m', floor: 'Floor 0 m' };

// ---------- State ----------
const S = {
  sel: [],                // ordered keys: 'f201' or 'f401/2'
  vals: {},               // key -> { dim, col }
  hl: false, tests: new Set(), armed: false,
  layer: 'truss', spacing: 'plan', fit: true, zoom: 1, add: false, lasso: false,
};
// A believable starting look
['f201', 'f202', 'f203'].forEach(k => S.sel.push(k));
['f201', 'f202', 'f203', 'f204'].forEach(k => S.vals[k] = { dim: 0.75, col: '#3d6bff' });
S.vals['f105'] = { dim: 1, col: null };
S.vals['f401/2'] = { dim: 0.5, col: '#ff3a3a' };

const keyFx = k => byId[k.split('/')[0]];
const isSel = k => S.sel.includes(k);
const valOf = k => S.vals[k] || { dim: 0, col: null, unset: true };

// ---------- Grid rendering ----------
const CW = 92, CH = 64, GAP = 10, TH = 64, CELLH = 30, TWMIN = 86; // tile cell at zoom 1
function layoutFor(layer) {
  const fx = FX.filter(f => f.layer === layer);
  let cols = new Set(), rows = new Set();
  fx.forEach(f => { for (let c = 0; c < f.w; c++) cols.add(f.col + c); rows.add(f.row); });
  let maxC = Math.max(...cols), maxR = Math.max(...rows);
  const pos = {};
  if (S.spacing === 'compact') {
    // Compact: empty rows removed; each row packed left to right in plan order.
    const rs = [...rows].sort((a, b) => a - b);
    maxC = 0;
    rs.forEach((r, ri) => {
      let c = 0;
      fx.filter(f => f.row === r).sort((a, b) => a.col - b.col).forEach(f => { pos[f.id] = { c, r: ri }; c += f.w; });
      maxC = Math.max(maxC, c - 1);
    });
    maxR = rs.length - 1;
  } else fx.forEach(f => { pos[f.id] = { c: f.col, r: f.row }; });
  // Each row is as tall as its tallest tile, so tiles never overlap the next row.
  const rowCells = Array.from({ length: maxR + 1 }, () => false);
  fx.forEach(f => { if (f.cells) rowCells[pos[f.id].r] = true; });
  return { fx, pos, rowCells, ncols: maxC + 1, nrows: maxR + 1 };
}
function fitZoom(pane, layouts) {
  const w = pane.clientWidth - 40, h = pane.clientHeight - 30 - layouts.length * 40;
  const nc = Math.max(...layouts.map(l => l.ncols));
  const nr = layouts.reduce((a, l) => a + l.nrows, 0);
  const extra = layouts.reduce((a, l) => a + l.rowCells.filter(Boolean).length * CELLH, 0);
  const z = Math.min(w / (nc * (CW + GAP)), (h - extra) / (nr * (CH + GAP)));
  return Math.max(S.spacing === 'compact' ? 0.8 : 0.85, Math.min(1.6, z));
}
function tileHTML(f, z) {
  const k = f.id, v = valOf(k), selIdx = S.sel.indexOf(k);
  const sel = selIdx >= 0, test = S.tests.size && v.unset && (sel || f.cells && S.sel.some(s => s.startsWith(k + '/')));
  const tags = [];
  if (!v.unset || (f.cells && [1, 2, 3, 4].some(c => S.vals[k + '/' + c]))) tags.push('<span class="tag tag--set">SET</span>');
  if (test) tags.push('<span class="tag tag--test">TEST</span>');
  if (S.hl && sel) tags.push('<span class="tag tag--hl">HL</span>');
  let pct = S.hl&&sel?100:test?80:Math.round((v.dim || 0) * 100);
  const levels=f.cells?Array.from({length:f.cells},(_,i)=>Math.round((valOf(k+'/'+(i+1)).dim||0)*100)):[];
  const mixed=levels.length&&Math.min(...levels)!==Math.max(...levels);
  if(levels.length)pct=Math.max(...levels);
  const intensityWord=mixed?Math.min(...levels)+'–'+Math.max(...levels)+'% MIXED':pct+'%';
  const sw = v.col ? `<span class="t-sw" title="Programmer colour set" style="--sample-colour:${v.col}"></span>` : '';
  const ord = sel ? `<span class="t-order" aria-label="selection ${selIdx + 1}">${selIdx + 1}</span>` : '';
  const names = S.viewNames !== false;
  let cells = '';
  if (f.cells) {
    cells = '<div class="cells">' + Array.from({ length: f.cells }, (_, i) => {
      const ck = k + '/' + (i + 1), cv = valOf(ck), cs = isSel(ck);
      const o = S.sel.indexOf(ck);
      return `<button type="button" class="cell${cs ? ' csel' : ''}" data-key="${ck}" aria-pressed="${cs}" aria-label="${f.name} ${f.fid} cell ${i + 1}${cs ? ', selected ' + (o + 1) : ''}">` +
        `<span class="cf" style="height:${Math.round((cv.dim || 0) * 100)}%;${cv.col ? '--sample-colour:' + cv.col : ''}"></span><span>${cs ? (o + 1) : i + 1}</span></button>`;
    }).join('') + '</div>';
  }
  const live = S.viewLive !== false;
  return `<div class="tile${sel ? ' sel' : ''}${S.hl && sel ? ' hl' : ''}${test ? ' test' : ''}" style="--z:${z}" data-fx="${k}">` +
    `<button type="button" class="t-main" data-key="${k}" aria-pressed="${sel}" aria-label="${f.name} ${f.fid}${sel ? ', selected ' + (selIdx + 1) : ''}, intensity ${intensityWord}, ${S.armed?'sample composed output':'not on the wire; sample Arm preview'}${test?', test driving':''}${S.hl&&sel?', highlighted':''}${!v.unset?', programmer SET':''}">` +
    `<span class="t-row"><svg class="t-glyph"><use href="../../../internal/web/static/icons/benny512-icons.svg#b5-icon-${f.glyph}"/></svg><span class="t-fid">${f.fid}</span>${ord}${live ? sw : ''}</span>` +
    (names ? `<span class="t-name">${f.name}</span>` : '') +
    `</button>` + cells +
    (live || tags.length ? `<div class="t-int"><span class="t-int-fill${pct ? '' : ' zero'}" style="width:${pct}%" aria-hidden="true"></span><span class="t-int-word" aria-hidden="true">${intensityWord}</span><span class="t-tags">${tags.join('')}</span></div>` : '') +
    `</div>`;
}
function renderGrid(paneId) {
  const pane = document.getElementById(paneId);
  if (!pane || !pane.offsetParent) return;
  const layers = S.layer === 'all' ? ['truss', 'floor'] : S.layer === 'unplaced' ? [] : [S.layer];
  if (!layers.length) { pane.innerHTML = '<p class="muted" style="padding:20px">No unplaced fixtures — every fixture has a position from the MVR.</p>'; return; }
  const lays = layers.map(layoutFor);
  const z = S.fit ? fitZoom(pane, lays) : S.zoom;
  if (S.fit) S.zoom = z;
  const coarse=matchMedia('(pointer:coarse)').matches||query.get('coarse')==='1';
  const cw = Math.max(coarse?104:TWMIN, CW * z), ch = Math.max(coarse?72:TH, CH * z), gp = GAP * z;
  const cellExtra=coarse?52:CELLH;
  let html = '';
  layers.forEach((ly, i) => {
    const L = lays[i];
    const rowY = []; let yy = 0;
    for (let r = 0; r < L.nrows; r++) { rowY[r] = yy; yy += ch + (L.rowCells[r] ? cellExtra : 0) + gp; }
    const W = L.ncols * (cw + gp), H = yy;
    html += `<div class="layerblock"><div class="layerhead"><b>${LAYERS[ly]}</b><span>${L.fx.length} fixtures · ${S.spacing === 'compact' ? 'compact — packed in plan order, real spacing ignored' : 'plan spacing from MVR'}</span></div>` +
      `<div class="canvas" data-layer="${ly}" style="width:${W}px;height:${H}px;background-size:${cw + gp}px 100%">`;
    L.fx.forEach(f => {
      const x = L.pos[f.id].c * (cw + gp), y = rowY[L.pos[f.id].r];
      const w = Math.max(f.w * (cw + gp) - gp,coarse&&f.cells?f.cells*48+12:0);
      html += tileHTML(f, z).replace('style="--z', `style="left:${x}px;top:${y}px;width:${w}px;height:${ch + (f.cells ? cellExtra : 0)}px;--z`);
    });
    html += '</div></div>';
  });
  pane.innerHTML = html;
}
function renderAll() {
  renderGrid('gridConsole'); renderGrid('gridTests');
  renderSel(); renderOrder(); renderCtlHeader(); renderTestsHas();
}

// ---------- Selection ----------
function describe(keys) {
  if (!keys.length) return '';
  const groups = {};
  keys.forEach(k => { const f = keyFx(k); const c = k.split('/')[1]; (groups[f.name] = groups[f.name] || []).push(c ? f.fid + '.' + c : String(f.fid)); });
  return Object.entries(groups).map(([n, ids]) => ids.length > 3 ? `${n} ${ids[0]}–${ids[ids.length - 1]} (${ids.length})` : `${n} ${ids.join(', ')}`).join(' · ');
}
function renderSel() {
  const n = S.sel.length;
  [['#selCount', '#selWhat'], ['#selCount2', '#selWhat2']].forEach(([c, w]) => {
    const el = $(c); el.className = 'selcount' + (n ? '' : ' none');
    el.innerHTML = n ? check+' '+n+' selected' : 'Nothing selected';
    $(w).textContent = n ? describe(S.sel) : 'Click fixtures on the grid, or use Select.'; $(w).title=$(w).textContent; $(w).setAttribute('aria-label',$(w).textContent);
  });
}
function renderOrder() { $('#orderList').innerHTML = S.sel.map(k => `<li>${describe([k])}</li>`).join('') || '<li class="muted">Nothing selected</li>'; }
function renderCtlHeader() { $('#ctlFor').textContent = S.sel.length ? `${S.sel.length} selected` : 'nothing selected'; }
function pick(k, additive) {
  if (additive || S.add) { const i = S.sel.indexOf(k); i >= 0 ? S.sel.splice(i, 1) : S.sel.push(k); }
  else S.sel = (S.sel.length === 1 && S.sel[0] === k) ? [] : [k];
  renderAll(); renderAttr();
}
document.addEventListener('click', e => {
  const b = e.target.closest('[data-key]');
  if (b && b.closest('.gridpane')) { if (S._lassoMoved) return; pick(b.dataset.key, e.ctrlKey || e.metaKey || e.shiftKey); }
});

// Lasso: drag on empty grid when Lasso is on (or Shift-drag)
let lz = null;
document.addEventListener('pointerdown', e => {
  const cv = e.target.closest('.canvas');
  if (!cv || e.target.closest('.tile') || !(S.lasso || e.shiftKey)) return;
  const r = cv.getBoundingClientRect();
  lz = { cv, x0: e.clientX - r.left, y0: e.clientY - r.top, el: document.createElement('div') };
  lz.el.className = 'lasso'; cv.appendChild(lz.el); S._lassoMoved = false;
  cv.setPointerCapture(e.pointerId); e.preventDefault();
});
document.addEventListener('pointermove', e => {
  if (!lz) return;
  const r = lz.cv.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
  Object.assign(lz.el.style, { left: Math.min(x, lz.x0) + 'px', top: Math.min(y, lz.y0) + 'px', width: Math.abs(x - lz.x0) + 'px', height: Math.abs(y - lz.y0) + 'px' });
  S._lassoMoved = true;
});
document.addEventListener('pointerup', () => {
  if (!lz) return;
  const box = lz.el.getBoundingClientRect();
  const hits = [...lz.cv.querySelectorAll('.tile')].filter(t => { const b = t.getBoundingClientRect(); return b.left < box.right && b.right > box.left && b.top < box.bottom && b.bottom > box.top; }).map(t => t.dataset.fx);
  if (!S.add) S.sel = [];
  hits.forEach(k => { if (!S.sel.includes(k)) S.sel.push(k); });
  lz.el.remove(); lz = null; renderAll(); renderAttr();
  setTimeout(() => { S._lassoMoved = false; }, 0);
});

// ---------- Toolbar ----------
function segBind(sel, attr, apply) {
  $$(sel + ' button').forEach(b => b.addEventListener('click', () => { apply(b.dataset[attr]); }));
}
function syncSeg(sel, attr, val) { $$(sel + ' button').forEach(b => b.setAttribute('aria-pressed', String(b.dataset[attr] === val))); }
segBind('#layerSeg', 'layer', v => { S.layer = v; syncSeg('#layerSeg', 'layer', v); renderAll(); });
const setSpacing = v => { S.spacing = v; syncSeg('#spacingSeg', 'spacing', v); syncSeg('#spacingSeg2', 'spacing', v); renderAll(); };
segBind('#spacingSeg', 'spacing', setSpacing); segBind('#spacingSeg2', 'spacing', setSpacing);
const press = (el, on) => el.setAttribute('aria-pressed', String(on));
$('#fitBtn').onclick = () => { S.fit = !S.fit; press($('#fitBtn'), S.fit); renderAll(); };
$('#zoomIn').onclick = () => { S.fit = false; press($('#fitBtn'), false); S.zoom = Math.min(1.6, S.zoom * 1.15); renderAll(); };
$('#zoomOut').onclick = () => { S.fit = false; press($('#fitBtn'), false); S.zoom = Math.max(S.spacing==='compact'?0.8:0.85, S.zoom / 1.15); renderAll(); };
$('#lassoBtn').onclick = () => { S.lasso = !S.lasso; press($('#lassoBtn'), S.lasso); };
$('#addBtn').onclick = () => { S.add = !S.add; press($('#addBtn'), S.add); };
$('#hlBtn').onclick = () => { S.hl = !S.hl; press($('#hlBtn'), S.hl); renderAll(); };
$('#armBtn').onclick = () => { S.armed = true; $('#outState').classList.add('armed'); $('#outWord').textContent = 'ARMED · LIVE'; $('#outState use').setAttribute('href', '../../../internal/web/static/icons/benny512-icons.svg#b5-icon-armed-lock'); };
$('#stopBtn').onclick = () => { S.armed = false; $('#outState').classList.remove('armed'); $('#outWord').textContent = 'DISARMED · no output'; $('#outState use').setAttribute('href', '../../../internal/web/static/icons/benny512-icons.svg#b5-icon-disarm'); };
$('[data-act="clear"]').onclick = () => { S.sel = []; S.vals = {}; S.hl = false; press($('#hlBtn'), false); renderAll(); renderAttr(); };

// Menus
let openMenu = null;
function closeMenus() { if (openMenu) { openMenu.m.hidden = true; openMenu.b.setAttribute('aria-expanded', 'false'); openMenu = null; } }
$$('[data-menu]').forEach(b => b.addEventListener('click', e => {
  e.stopPropagation();
  const m = document.getElementById(b.dataset.menu);
  if (openMenu && openMenu.m === m) return closeMenus();
  closeMenus(); syncWinMenu();
  const r = b.getBoundingClientRect();
  m.hidden = false;
  m.style.top = (r.bottom + 4) + 'px';
  m.style.left = Math.min(r.left, innerWidth - m.offsetWidth - 8) + 'px';
  b.setAttribute('aria-expanded', 'true'); openMenu = { m, b };
  m.style.maxHeight=Math.max(120,innerHeight-r.bottom-64)+'px'; const first = m.querySelector('button'); if (first) first.focus();
}));
document.addEventListener('click', e => { if (openMenu && !e.target.closest('.menu')) closeMenus(); });
document.addEventListener('keydown', e => { if(e.key==='Escape'){const trigger=openMenu?.b;closeMenus();trigger?.focus();if($('#notes').open){$('#notes').close();$('#notesBtn').focus();}} if(openMenu&&['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const items=[...openMenu.m.querySelectorAll('button:not(:disabled)')];const i=items.indexOf(document.activeElement),n=e.key==='Home'?0:e.key==='End'?items.length-1:(i+(e.key==='ArrowDown'?1:-1)+items.length)%items.length;items[n]?.focus();} });
$$('#m-select [data-sel]').forEach(b => b.onclick = () => {
  const vis = FX.filter(f => S.layer === 'all' || f.layer === S.layer).map(f => f.id);
  const v = b.dataset.sel;
  if (v === 'all') S.sel = FX.map(f => f.id);
  if (v === 'layer') S.sel = vis;
  if (v === 'none') S.sel = [];
  if (v === 'invert') S.sel = vis.filter(k => !S.sel.includes(k));
  closeMenus(); renderAll(); renderAttr();
});
$$('#m-view [data-view]').forEach(b => b.onclick = () => {
  const on = b.getAttribute('aria-checked') !== 'true';
  b.setAttribute('aria-checked', String(on)); b.querySelector('.mchk').innerHTML = on ? check : '';
  if (b.dataset.view === 'names') S.viewNames = on;
  if (b.dataset.view === 'live') S.viewLive = on;
  renderAll();
});
$('[data-go="tests"]').onclick = () => { closeMenus(); show('tests'); };

// ---------- Windows: floating, resizable, dockable ----------
const WKEY = 'b5workspace.v1.'+scene+'.windows2', DKEY = 'b5workspace.v1.'+scene+'.dockw';
const ORDER = ['fw-controls', 'fw-order', 'fw-groups', 'fw-midi'];
const DEF = {
  'fw-controls': { open: true, docked: true, w: 440 },
  'fw-order': { open: false, docked: false, w: 300, at: 'tl' },
  'fw-groups': { open: false, docked: false, w: 340, at: 'ml' },
  'fw-midi': { open: false, docked: false, w: 320, at: 'bl' },
};
let wstate = store.get(WKEY) || {};
let dockW = store.get(DKEY) || 440;
const st = id => (wstate[id] = Object.assign({}, DEF[id], wstate[id] || {}));
const save = () => { store.set(WKEY, wstate); store.set(DKEY, dockW); };
const wsEl = () => $('#wsConsole');
function defaultXY(id, s) {
  const H = wsEl().clientHeight;
  return s.at === 'tl' ? { x: 12, y: 12 } : s.at === 'ml' ? { x: 12, y: Math.min(250, Math.max(12, H - 260)) } : { x: 12, y: Math.max(12, H - 150) };
}
function placeWin(fw) {
  const s = st(fw.id), ws = wsEl();
  fw.hidden = !s.open;
  fw.classList.toggle('collapsed', !!s.collapsed);
  const cb = fw.querySelector('[data-collapse]'); cb.setAttribute('aria-expanded', String(!s.collapsed));
  cb.querySelector('use').setAttribute('href', s.collapsed ? '../../../internal/web/static/icons/benny512-icons.svg#b5-icon-chevron-expand' : '../../../internal/web/static/icons/benny512-icons.svg#b5-icon-chevron-collapse');
  const name = fw.querySelector('.fw-title').firstChild.textContent;
  const db = fw.querySelector('[data-dock]');
  db.textContent = s.docked ? 'Float' : 'Dock';
  db.setAttribute('aria-label', s.docked ? `Float ${name} over the grid` : `Dock ${name} on the right`);
  if (s.docked) {
    fw.classList.add('docked');
    const stack = $('#dockStack');
    // keep a stable order inside the dock
    const after = ORDER.slice(ORDER.indexOf(fw.id) + 1).map(i => document.getElementById(i)).find(x => x.parentElement === stack);
    if (fw.parentElement !== stack || (after && fw.nextElementSibling !== after)) stack.insertBefore(fw, after || null);
    fw.style.left = fw.style.top = fw.style.width = fw.style.height = '';
  } else {
    fw.classList.remove('docked');
    if (fw.parentElement !== ws) ws.appendChild(fw);
    const d = defaultXY(fw.id, s);
    let x = s.x != null ? s.x : d.x, y = s.y != null ? s.y : d.y;
    const w = Math.max(280, Math.min(s.w || 320, ws.clientWidth - 16));
    x = Math.max(0, Math.min(x, ws.clientWidth - w)); y = Math.max(0, Math.min(y, ws.clientHeight - Math.min(s.h||fw.offsetHeight||140,ws.clientHeight-16)));
    Object.assign(fw.style, { left: x + 'px', top: y + 'px', width: w + 'px', height: s.h && !s.collapsed ? Math.min(s.h, ws.clientHeight - y - 8) + 'px' : '' });
  }
}
function layoutDock() {
  const ws = wsEl(); if(!ws.clientWidth)return; const anyDocked = $$('#dockStack .fw').some(f => !f.hidden);
  dockW = Math.max(320, Math.min(dockW, Math.round(ws.clientWidth * 0.6)));
  ws.style.setProperty('--dockw', dockW + 'px');
  $('#dock').hidden = !anyDocked;
}
function placeAll() { $$('.fw').forEach(placeWin); layoutDock(); renderGrid('gridConsole'); }
const front = fw => { $$('.fw').forEach(x => x.classList.remove('front')); fw.classList.add('front'); };
$$('.fw').forEach(fw => {
  fw.addEventListener('pointerdown', () => front(fw));
  fw.querySelector('[data-close]').onclick = () => { st(fw.id).open = false; save(); placeAll(); $('[data-menu="m-windows"]').focus(); };
  fw.querySelector('[data-collapse]').onclick = () => { const s = st(fw.id); s.collapsed = !s.collapsed; save(); placeAll(); };
  fw.querySelector('[data-dock]').onclick = () => {
    const s = st(fw.id); s.docked = !s.docked;
    if (!s.docked) { const ws = wsEl(); s.x = Math.max(12, ws.clientWidth - (s.w || 320) - 12); s.y = 12; } // floats where it was docked
    save(); placeAll(); fw.querySelector('[data-dock]').focus();
  };
  // Drag a floating window by its title bar; drop it on the right edge to dock it.
  const bar = fw.querySelector('[data-drag]');
  bar.addEventListener('pointerdown', e => {
    if (e.target.closest('button') || st(fw.id).docked) return;
    const ws = wsEl(), wr = ws.getBoundingClientRect(), r = fw.getBoundingClientRect();
    const dx = e.clientX - r.left, dy = e.clientY - r.top;
    bar.setPointerCapture(e.pointerId);
    let hint = false;
    const mv = ev => {
      const x = Math.max(0, Math.min(ev.clientX - wr.left - dx, wr.width - r.width));
      const y = Math.max(0, Math.min(ev.clientY - wr.top - dy, wr.height - r.height));
      fw.style.left = x + 'px'; fw.style.top = y + 'px';
      hint = ev.clientX > wr.right - 40; ws.classList.toggle('dock-hint', hint);
    };
    const up = () => {
      bar.removeEventListener('pointermove', mv); bar.removeEventListener('pointerup', up);
      ws.classList.remove('dock-hint');
      const s = st(fw.id);
      if (hint) s.docked = true; else { s.x = parseFloat(fw.style.left); s.y = parseFloat(fw.style.top); }
      save(); placeAll();
    };
    bar.addEventListener('pointermove', mv); bar.addEventListener('pointerup', up);
  });
  bar.tabIndex = 0;
  bar.setAttribute('aria-label', fw.querySelector('.fw-title').firstChild.textContent + ' window. When floating, arrow keys move it');
  bar.addEventListener('keydown', e => {
    const step = e.shiftKey ? 40 : 10, m = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (!m || st(fw.id).docked) return; e.preventDefault();
    const s = st(fw.id); s.x = parseFloat(fw.style.left) + m[0]; s.y = parseFloat(fw.style.top) + m[1]; save(); placeWin(fw);
  });
  // Resize a floating window from its bottom-right corner (pointer or arrow keys).
  const grip = fw.querySelector('[data-resize]');
  grip.addEventListener('pointerdown', e => {
    e.preventDefault(); e.stopPropagation();
    const ws = wsEl(), r = fw.getBoundingClientRect(), wr = ws.getBoundingClientRect();
    const sx = e.clientX, sy = e.clientY, w0 = r.width, h0 = r.height;
    grip.setPointerCapture(e.pointerId);
    const mv = ev => {
      const w = Math.max(280, Math.min(w0 + ev.clientX - sx, wr.right - r.left - 4));
      const h = Math.max(140, Math.min(h0 + ev.clientY - sy, wr.bottom - r.top - 4));
      fw.style.width = w + 'px'; fw.style.height = h + 'px';
    };
    const up = () => { grip.removeEventListener('pointermove', mv); grip.removeEventListener('pointerup', up); const s = st(fw.id); s.w = parseFloat(fw.style.width); s.h = parseFloat(fw.style.height); save(); };
    grip.addEventListener('pointermove', mv); grip.addEventListener('pointerup', up);
  });
  grip.addEventListener('keydown', e => {
    const m = { ArrowLeft: [-20, 0], ArrowRight: [20, 0], ArrowUp: [0, -20], ArrowDown: [0, 20] }[e.key];
    if (!m) return; e.preventDefault();
    const s = st(fw.id), r = fw.getBoundingClientRect();
    s.w = Math.max(280, r.width + m[0]); s.h = Math.min(wsEl().clientHeight-16,Math.max(140, r.height + m[1])); save(); placeWin(fw);
  });
});
// Dock width: drag the dock's left edge, or arrow keys on it.
(() => {
  const edge = $('#dockEdge');
  edge.addEventListener('pointerdown', e => {
    e.preventDefault(); edge.setPointerCapture(e.pointerId);
    const wr = wsEl().getBoundingClientRect();
    const mv = ev => { dockW = wr.right - ev.clientX; layoutDock(); };
    const up = () => { edge.removeEventListener('pointermove', mv); edge.removeEventListener('pointerup', up); save(); renderGrid('gridConsole'); };
    edge.addEventListener('pointermove', mv); edge.addEventListener('pointerup', up);
  });
  edge.addEventListener('keydown', e => {
    const d = { ArrowLeft: 20, ArrowRight: -20 }[e.key]; if (!d) return; e.preventDefault();
    dockW += d; layoutDock(); save(); renderGrid('gridConsole');
  });
})();
function syncWinMenu() {
  $$('#m-windows [data-win]').forEach(b => { const s = st(b.dataset.win); b.setAttribute('aria-checked', String(!!s.open)); b.querySelector('.mchk').textContent = s.open ? '✓' : ''; b.querySelector('.mwhere').textContent = s.open ? (s.docked ? 'docked' : 'floating') : ''; });
}
$$('#m-windows [data-win]').forEach(b => b.onclick = () => {
  const fw = document.getElementById(b.dataset.win), s = st(fw.id);
  s.open = !s.open; s.collapsed = false; save(); placeAll(); closeMenus(); if(s.open){front(fw);fw.querySelector('[data-drag]').focus();}
});
$('#resetWins').onclick = () => { wstate = {}; dockW = 440; save(); placeAll(); closeMenus(); };

// ---------- Controls window content ----------
const TABS = [['dimmer', 'Dimmer', 'attr-dimmer'], ['position', 'Position', 'attr-position'], ['colour', 'Colour', 'attr-colour'], ['beam', 'Beam/Gobo', 'attr-beam'], ['focus', 'Focus', 'attr-focus'], ['shaper', 'Shaper', 'attr-shaper'], ['control', 'Control', 'attr-control']];
let tab = 'dimmer';
function setCount(t) {
  if (t === 'dimmer') return S.sel.filter(k => S.vals[k] && S.vals[k].dim != null && !S.vals[k].unset).length;
  if (t === 'colour') return S.sel.filter(k => S.vals[k] && S.vals[k].col).length;
  return 0;
}
function renderTabs() {
  $('#attrTabs').innerHTML = TABS.map(([id, w, ic]) => `<button type="button" role="tab" aria-selected="${tab === id}" data-tab="${id}"><svg class="ico"><use href="../../../internal/web/static/icons/benny512-icons.svg#b5-icon-${ic}"/></svg>${w}<span class="n">${setCount(id)} set</span></button>`).join('');
  $$('#attrTabs button').forEach(b => b.onclick = () => { tab = b.dataset.tab; renderAttr(); });
}
function selDim() { const v = S.sel.map(k => valOf(k).dim || 0); return v.length ? v.reduce((a, b) => a + b, 0) / v.length : 0; }
function setVals(fn) { S.sel.forEach(k => { const v = Object.assign({ dim: 0, col: null }, S.vals[k] || {}); delete v.unset; fn(v); S.vals[k] = v; }); renderAll(); renderTabs(); }
function renderAttr() {
  renderTabs();
  const body = $('#attrBody');
  if (!S.sel.length) { body.innerHTML = '<p class="muted">Select fixtures on the grid to control them.</p>'; return; }
  if (tab === 'dimmer') {
    const d = Math.round(selDim() * 100);
    body.innerHTML = `<div class="card"><h4>Dimmer <span class="muted">master — multiplies the parts</span><span class="v" id="dimV">${d} %</span></h4>
      <input type="range" min="0" max="100" value="${d}" id="dimR" aria-label="Dimmer master">
      <div class="row"><button class="btn btn--sm" data-d="0">0</button><button class="btn btn--sm" data-d="50">50 %</button><button class="btn btn--sm" data-d="100">FULL</button><button class="btn btn--sm" style="margin-left:auto" aria-pressed="false"><span class="chk">✓</span><svg class="ico-sm"><use href="../../../internal/web/static/icons/benny512-icons.svg#b5-icon-fine"/></svg>Fine</button></div>
      <details class="more"><summary>Channel details — modes, delay, raw DMX</summary><p class="muted">The function buttons (Dimming modes, Delay time…) and raw DMX readouts from today's card fold away here.</p></details></div>
      <div class="card"><h4>Parts</h4><p class="muted" style="margin:0">Fixtures with parts (e.g. head + SparkLED) list one fader per part here, under the Master.</p></div>`;
    $('#dimR').oninput = e => { const f = e.target.value / 100; $('#dimV').textContent = e.target.value + ' %'; setVals(v => v.dim = f); };
    $$('[data-d]').forEach(b => b.onclick = () => { setVals(v => v.dim = b.dataset.d / 100); renderAttr(); });
  } else if (tab === 'colour') {
    const cols = [['Red', '#ff2a2a'], ['Amber', '#ff9a1a'], ['Yellow', '#ffe14a'], ['Green', '#2ad44a'], ['Cyan', '#2ad4e8'], ['Blue', '#3d6bff'], ['Magenta', '#e83ae0'], ['Lavender', '#b49aff'], ['CTO', '#ffc58a'], ['White', '#ffffff'], ['UV', '#6a2aff'], ['Open', '']];
    body.innerHTML = `<div class="card"><h4>Colour</h4><div class="sw-grid">${cols.map(([n, c]) => `<button type="button" data-c="${c}" style="background:${c || 'transparent'};${c ? 'filter:brightness(.6)' : ''}">${n}</button>`).join('')}</div>
      <details class="more"><summary>Colour wheels, CMY and mix faders</summary><p class="muted">Today's wheel slots and mix faders, folded away.</p></details></div>`;
    $$('[data-c]').forEach(b => b.onclick = () => setVals(v => v.col = b.dataset.c || null));
  } else if (tab === 'position') {
    body.innerHTML = `<div class="card"><h4>Pan / Tilt <span class="v">50 % / 50 %</span></h4><div class="xy" aria-label="Pan and tilt pad"><span class="h"></span></div>
      <div class="row" style="margin-top:8px"><button class="btn btn--sm">Centre</button><button class="btn btn--sm" aria-pressed="false"><span class="chk">✓</span>Fine</button></div></div>`;
  } else {
    const t = TABS.find(x => x[0] === tab)[1];
    body.innerHTML = `<div class="card"><h4>${t}</h4><p class="muted" style="margin:0">Same controls as today; only the open tab is shown, with function details folded under each channel name.</p></div>`;
  }
}

// ---------- Tests ----------
$$('[data-tests] .ttile').forEach(b => b.onclick = () => {
  const on = b.getAttribute('aria-pressed') !== 'true';
  b.setAttribute('aria-pressed', String(on)); b.querySelector('.st').textContent = on ? 'ON' : 'OFF';
  on ? S.tests.add(b) : S.tests.delete(b); renderAll();
});
$('#allOff').onclick = () => { $$('[data-tests] .ttile').forEach(b => { b.setAttribute('aria-pressed', 'false'); b.querySelector('.st').textContent = 'OFF'; }); S.tests.clear(); renderAll(); };
function renderTestsHas() { $$('[data-has]').forEach(s => s.textContent = S.sel.length ? `${S.sel.length} of ${S.sel.length} selected have this` : 'Select fixtures to test'); }

// ---------- Screens ----------
function show(id) {
  $$('.nav [data-screen]').forEach(b => b.dataset.screen === id ? b.setAttribute('aria-current', 'page') : b.removeAttribute('aria-current'));
  $$('.screen').forEach(s => s.classList.toggle('on', s.id === 'scr-' + id));
  renderAll();
}
$$('.nav [data-screen]').forEach(b => b.onclick = () => show(b.dataset.screen));

$('#notesBtn').onclick = () => ($('#notes').show(),$('#notes button').focus());

// Included by prepare-mockups.mjs inside the preview's isolated closure.
// Fictional examples only: no fetch, sockets, MIDI or production state.
const coarse=matchMedia('(pointer:coarse)').matches||query.get('coarse')==='1';
document.documentElement.classList.toggle('coarse-preview',coarse);
const announce=text=>$('#workspaceStatus').textContent=text;
document.body.insertAdjacentHTML('beforeend','<p class="sr" id="workspaceStatus" role="status"></p>');
const newUse=(element,name)=>element?.querySelector('use')?.setAttribute('href','console-workspace-icons.svg#b5-icon-'+name);
newUse($('#fitBtn'),'zoom-fit');
$('#fitBtn').innerHTML=icon('zoom-fit')+'Fit';
$('#zoomIn').innerHTML=icon('zoom-in');$('#zoomOut').innerHTML=icon('zoom-out');
$$('[data-spacing]').forEach(b=>b.insertAdjacentHTML('afterbegin',icon('spacing-'+b.dataset.spacing)));
$$('[data-close]').forEach(b=>b.innerHTML=icon('close'));
$$('[data-resize]').forEach(b=>b.innerHTML=icon('resize-grip'));
$('#resetWins').insertAdjacentHTML('afterbegin',icon('window-reset'));
$$('svg').forEach(svg=>svg.setAttribute('aria-hidden','true'));
$$('.nav button:not([data-screen])').forEach(b=>{b.disabled=true;b.title='Design preview: this existing screen is unchanged';});
$('#layerPicker').addEventListener('change',ev=>{S.layer=ev.target.value;syncSeg('#layerSeg','layer',S.layer);renderAll();});
$$('#layerSeg button').forEach(b=>b.addEventListener('click',()=>$('#layerPicker').value=S.layer));
const originalSetSpacing=setSpacing;
$$('#m-view [data-view="live"]').forEach(b=>b.addEventListener('click',()=>document.documentElement.classList.toggle('hide-values',S.viewLive===false)));
describe=function(keys){
 const groups=new Map();keys.forEach(k=>{const f=keyFx(k);if(!groups.has(f.name))groups.set(f.name,[]);groups.get(f.name).push(k.includes('/')?f.fid+'.'+k.split('/')[1]:f.fid);});
 return [...groups].map(([name,ids])=>{const parents=ids.filter(x=>typeof x==='number').sort((a,b)=>a-b),parts=ids.filter(x=>typeof x==='string'),runs=[];for(let i=0;i<parents.length;){let j=i;while(j+1<parents.length&&parents[j+1]===parents[j]+1)j++;runs.push(j-i>1?parents[i]+'–'+parents[j]+' ('+(j-i+1)+')':parents.slice(i,j+1).join(', '));i=j+1;}return name+' '+[...runs,...parts].join(', ');}).join(' · ');
};
// Preserve Plan/Compact within this browser, shared by Console and Tests.
$$('[data-spacing]').forEach(b=>b.addEventListener('click',()=>store.set('b5workspace.spacing',S.spacing)));
S.spacing=store.get('b5workspace.spacing')||'plan';
if(scene==='compact'||scene==='cells')S.spacing='compact';
syncSeg('#spacingSeg','spacing',S.spacing);syncSeg('#spacingSeg2','spacing',S.spacing);
if(scene==='empty'){S.sel=[];S.vals={};}
if(scene==='unplaced'){S.layer='unplaced';syncSeg('#layerSeg','layer','unplaced');$('#layerPicker').value='unplaced';}
if(scene==='all-layers'){S.layer='all';syncSeg('#layerSeg','layer','all');$('#layerPicker').value='all';}
if(scene==='cells'){S.sel=['f401/2','f401/3'];S.vals['f401/3']={dim:.75,col:'#3d6bff'};}
if(scene==='stacked'){
 Object.assign(st('fw-order'),{open:true,docked:true,collapsed:true});
 Object.assign(st('fw-groups'),{open:true,docked:false,x:24,y:100,w:340,h:250});
}
if(scene==='resize'){
 Object.assign(st('fw-controls'),{open:true,docked:false,x:Math.max(16,innerWidth-580),y:16,w:520,h:400});
 $('#fw-controls').classList.add('resize-example');
}
if(scene==='drop'){
 Object.assign(st('fw-groups'),{open:true,docked:false,x:24,y:80,w:340,h:250});
 $('#wsConsole').classList.add('dock-hint');
}
if(scene==='dock320')dockW=320;
if(scene==='tests'){
 S.sel.forEach(k=>delete S.vals[k]); // Test example uses unclaimed targets; manual values still win.
 S.armed=true;
 $('#outState').classList.add('armed');$('#outWord').textContent='ARMED · LIVE';
 const b=$('[data-tests] .ttile');b.setAttribute('aria-pressed','true');b.querySelector('.st').textContent='ON';S.tests.add(b);
 show('tests');
}
if(scene.startsWith('controls-'))tab=scene.slice(9);
// Full selection tools on Tests; wrappers invoke the existing sample model.
const testsBar=$('#scr-tests .selbar');
testsBar.insertAdjacentHTML('beforeend','<div class="tests-tools"><button class="btn" data-tests-tool="lassoBtn" aria-pressed="false">Lasso</button><button class="btn" data-tests-tool="addBtn" aria-pressed="false">Add</button><button class="btn" data-menu="m-select" aria-haspopup="menu" aria-expanded="false">Select</button></div>');
$$('[data-tests-tool]').forEach(b=>b.onclick=()=>{const original=document.getElementById(b.dataset.testsTool);original.click();b.setAttribute('aria-pressed',original.getAttribute('aria-pressed'));b.innerHTML=(original.getAttribute('aria-pressed')==='true'?check:'')+(b.dataset.testsTool==='lassoBtn'?'Lasso':'Add');});
testsBar.querySelector('[data-menu]').onclick=ev=>{
 const b=ev.currentTarget,m=$('#m-select');closeMenus();const r=b.getBoundingClientRect();m.hidden=false;m.style.top=r.bottom+4+'px';m.style.left=Math.min(r.left,innerWidth-240)+'px';openMenu={m,b};b.setAttribute('aria-expanded','true');m.querySelector('button').focus();
};
// Show every test family separately without inventing production test IDs.
const combined=[...document.querySelectorAll('.tpanel h3')].find(h=>h.textContent.includes('COLOUR'));
combined.textContent='COLOUR';
const mixed=combined.nextElementSibling;
const tileList=[...mixed.querySelectorAll('.ttile')];
const beam=document.createElement('div');beam.className='ttiles';beam.append(tileList[1],tileList[2]);
const focus=document.createElement('div');focus.className='ttiles';focus.append(tileList[3]);
mixed.insertAdjacentHTML('afterend','<h3>BEAM</h3>');mixed.nextElementSibling.after(beam);
beam.insertAdjacentHTML('afterend','<h3>FOCUS</h3>');beam.nextElementSibling.after(focus);
focus.insertAdjacentHTML('afterend','<h3>SHAPER</h3><button class="ttile" type="button" id="shaperTest" aria-pressed="false"><span class="row">'+oldIcon('attr-shaper')+'<b>Blade sweep</b><span class="st">OFF</span></span><span class="muted">3 of 3 sample fixtures supported</span></button>');
$('#shaperTest').onclick=ev=>{const b=ev.currentTarget,on=b.getAttribute('aria-pressed')!=='true';b.setAttribute('aria-pressed',String(on));b.querySelector('.st').textContent=on?'ON':'OFF';on?S.tests.add(b):S.tests.delete(b);renderAll();};
const isolate=$('.tpanel input[type=checkbox]');
isolate.setAttribute('aria-label','Isolate: drive only the tested channel');
isolate.closest('.card').classList.add('isolate-card');
isolate.closest('.card').querySelector('p').textContent='Manual values win. Tested-cell masters stay open. Ends with tests, Disarm, either lease-loss policy or this browser leaving.';
const updateIsolate=()=>{isolate.disabled=!S.armed||!S.tests.size;if(isolate.disabled)isolate.checked=false;};
document.addEventListener('click',()=>queueMicrotask(updateIsolate));updateIsolate();
// Explicit keyboard tab contract; one selected panel, no piled-up families.
function param(label,value,help='3 of 3 sample fixtures',max=100){return '<label class="param">'+label+'<input type="range" min="0" max="'+max+'" value="'+value+'" aria-label="'+label+'"><output>'+value+(max===255?' DMX':' %')+'</output><small>'+help+'</small></label>';}
const details='<details class="more"><summary>Channel details — modes, delay, raw DMX</summary><p class="muted">Modes and delay use named profile ranges. Raw DMX appears here for unprofiled fixtures; unknown footprint offers Read RDM info, no invented faders.</p></details>';
renderAttr=function(){
 renderTabs();const body=$('#attrBody');
 $('#attrTabs').setAttribute('aria-label','Attribute families');
 $$('#attrTabs button').forEach(b=>{b.id='ws-tab-'+b.dataset.tab;b.setAttribute('aria-controls','attrBody');b.tabIndex=b.dataset.tab===tab?0:-1;});
 body.setAttribute('role','tabpanel');body.setAttribute('aria-labelledby','ws-tab-'+tab);
 if(!S.sel.length){body.innerHTML='<p>Select fixtures on the grid to control them.</p>';return;}
 let content='';
 if(tab==='dimmer')content='<h4>Master <span class="muted">multiplies parts</span></h4>'+param('Master',75)+ '<div class="row"><button class="btn" data-quick="0">0</button><button class="btn" data-quick="50">50 %</button><button class="btn" data-quick="100">FULL</button><button class="btn" data-fine aria-pressed="false">'+oldIcon('fine')+'Fine</button></div><h4>Parts</h4>'+param('Beam',100,'Sample part · outermost dimmer; inner dimmers held open')+param('Plate',60,'Sample part · Master multiplies this level')+'<p class="muted">Master 75% × Plate 60% = 45%. Sample type anatomy; real cards use selected fixture capabilities.</p>';
 if(tab==='position')content='<h4>Pan / Tilt</h4><div class="xy" role="img" aria-label="Pan and tilt reference; use labelled sliders"><span class="h"></span></div>'+param('Pan',50,'16-bit · Fine scales the complete value 1:10')+param('Tilt',35,'16-bit · first selected is the reference')+'<button class="btn" data-fine aria-pressed="false">Fine</button>';
 if(tab==='dimmer'&&scene==='cells')content='<h4>Cell intensity · MIXED 50–75 %</h4>'+param('Selected cells',50,'Virtual (scales colour) · first selected reference, absolute writes')+'<p class="muted">Select the whole fixture for its Master. Cell colour × virtual level × part/master composition; no independent cell dimmer when its part has a real dimmer.</p>';
 if(tab==='colour')content='<h4>Colour</h4><p class="muted">RGBW mixing · 3 of 3 sample fixtures · DMX values, not measured colour</p><div class="colour-field" aria-hidden="true"></div>'+param('Red',12)+param('Green',24)+param('Blue',80)+param('White',0);
 if(tab==='beam')content='<h4>Gobo wheel</h4><div class="slot-list"><button class="btn" aria-pressed="false">Open</button><button class="btn" aria-pressed="true">Breakup · IN</button><button class="btn">Slot 3 · media not reported</button></div><p class="muted">Names from file · missing media stays selectable</p><div class="row"><button class="btn">OPEN</button><button class="btn" aria-pressed="true">CLOSED · beam blocked</button></div>'+param('Gobo rotation',36,'Physical range not reported',255)+param('Strobe rate',0,'Hz not reported');
 if(tab==='focus')content='<h4>Focus</h4>'+param('Focus',50,'2 of 3 sample fixtures · physical range not reported')+param('Beam zoom',35,'2 of 3 sample fixtures; unrelated to grid zoom');
 if(tab==='shaper')content='<h4>Shaper · 4 blades</h4><svg class="blade-diagram" viewBox="0 0 200 150" role="img" aria-label="Four labelled blades"><circle cx="100" cy="75" r="55" fill="none" stroke="currentColor"/><path d="M65 30h70v90H65Z" fill="none" stroke="currentColor"/><text x="95" y="23">1</text><text x="145" y="78">2</text><text x="95" y="140">3</text><text x="40" y="78">4</text></svg>'+[1,2,3,4].map(n=>param('Blade '+n+' A',20)+param('Blade '+n+' B',25)).join('')+param('Assembly rotate',50);
 if(tab==='control')content='<h4>Fixture commands</h4><p>Control channels are commands. Fan and Set cannot send them.</p><div class="row"><button class="btn" data-command="Reset">Reset…</button><button class="btn" data-command="Lamp off">Lamp off…</button></div><button class="btn hold-example" '+(S.armed?'':'disabled')+'>HOLD 0.75 s · Lamp on</button><p class="muted">'+(S.armed?'Gesture example · sends once for 1 s in production':'Output is not live; HOLD unavailable. Nothing queued for Arm.')+'</p><button class="btn">Store Control preset</button><details class="more"><summary>Raw DMX / Tools</summary><p>No profile · RDM only · slot labels not reported. No footprint reported: Read RDM info first.</p><button class="btn">Read RDM info</button></details>';
 body.innerHTML='<div class="card">'+content+details+'</div>';
 body.querySelectorAll('input[type=range]').forEach(input=>input.oninput=()=>{input.nextElementSibling.textContent=input.value+(input.max==='255'?' DMX':' %');if(input.getAttribute('aria-label')==='Master'){S.sel.forEach(k=>S.vals[k]={...valOf(k),dim:input.value/100,unset:false});renderAll();}});
 body.querySelectorAll('[data-fine]').forEach(b=>b.onclick=()=>{b.setAttribute('aria-pressed',String(b.getAttribute('aria-pressed')!=='true'));b.innerHTML=(b.getAttribute('aria-pressed')==='true'?check:'')+oldIcon('fine')+'Fine';});
 body.querySelectorAll('[data-quick]').forEach(b=>b.onclick=()=>{S.sel.forEach(k=>S.vals[k]={...valOf(k),dim:Number(b.dataset.quick)/100,unset:false});renderAll();renderAttr();});
 body.querySelectorAll('[data-command]').forEach(b=>b.onclick=()=>openQuestion(b.dataset.command+' fixtures?',S.armed?'Sent once: after 1 s each channel returns to its previous value. Nothing is kept in the programmer.':'Output is not live. Nothing queued for Arm.',b,S.armed?b.dataset.command+' fixtures':null));
};
$('#attrTabs').addEventListener('keydown',ev=>{if(!['ArrowLeft','ArrowRight','Home','End'].includes(ev.key))return;ev.preventDefault();const tabs=$$('#attrTabs button');let i=tabs.indexOf(document.activeElement);i=ev.key==='Home'?0:ev.key==='End'?6:(i+(ev.key==='ArrowRight'?1:-1)+7)%7;tab=tabs[i].dataset.tab;renderAttr();$('#ws-tab-'+tab).focus();});
function openQuestion(title,text,trigger,action){
 let dlg=$('#question');if(!dlg){document.body.insertAdjacentHTML('beforeend','<dialog id="question" aria-labelledby="question-title"><h2 id="question-title"></h2><p></p><div class="row"><button class="btn" data-cancel>Cancel</button><button class="btn" data-confirm></button></div></dialog>');dlg=$('#question');}
 dlg.querySelector('h2').textContent=title;dlg.querySelector('p').textContent=text;const confirm=dlg.querySelector('[data-confirm]');confirm.hidden=!action;confirm.textContent=action||'';dlg.style.top=($('.hdr').getBoundingClientRect().bottom+64)+'px';
 const close=()=>{dlg.close();trigger?.focus();};dlg.querySelector('[data-cancel]').onclick=close;confirm.onclick=()=>{announce(action+' · design only');close();};dlg.onkeydown=ev=>{if(ev.key==='Escape'){ev.preventDefault();close();}};dlg.show();dlg.querySelector('[data-cancel]').focus();
}
$('[data-act="clear"]').onclick=ev=>openQuestion('Clear programmer values','Choose family on selection, selection values, or everything. Production preserves these separate scopes.',ev.currentTarget,'Clear family on selection');
$('[data-act="locate"]').onclick=()=>announce('Locate persists as SET in the programmer · design only');
$('[data-act="level"]').onclick=ev=>openQuestion('Level 20 % on selection','Sets the Master level on this selection. Part levels are retained.',ev.currentTarget,'Set Master to 20 %');
$('#stopBtn').addEventListener('click',()=>{updateIsolate();const dlg=$('#question');if(dlg?.open){dlg.querySelector('[data-confirm]').hidden=true;dlg.querySelector('p').textContent='Output is not live. Nothing queued for Arm.';}if(tab==='control')renderAttr();renderAll();});
$('#armBtn').addEventListener('click',()=>{if(tab==='control')renderAttr();renderAll();});
// Controls narrow-width and collapse icons follow the same symbol grammar.
$$('[data-collapse]').forEach(b=>b.addEventListener('click',()=>{b.querySelector('use').setAttribute('href','console-workspace-icons.svg#b5-icon-window-'+(b.getAttribute('aria-expanded')==='true'?'collapse':'expand'));announce(b.getAttribute('aria-expanded')==='true'?'Window expanded':'Window collapsed');}));
// Initial scenes are proposals, not production output/source telemetry.
document.querySelector('.show').setAttribute('title','Fictional sample show · intensity/source telemetry contract not verified');
syncWinMenu();
if(scene==='cells')requestAnimationFrame(()=>{const pane=$('#gridConsole'),cell=pane.querySelector('.cell.csel');if(cell){const a=pane.getBoundingClientRect(),b=cell.closest('.tile').getBoundingClientRect();pane.scrollTop+=Math.max(0,b.bottom-a.bottom+20);pane.scrollLeft+=Math.max(0,b.right-a.right+20);}});

addEventListener('resize', () => { placeAll(); if (S.fit) renderAll(); });
placeAll();
renderAll(); renderAttr();
if (!store.get('b5workspace.v1.'+scene+'.seenNotes')) { store.set('b5workspace.v1.'+scene+'.seenNotes', true); }
})();
