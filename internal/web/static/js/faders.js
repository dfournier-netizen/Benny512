// faders.js — the always-visible GROUP FADER BAR (Console-lite G3; server is
// G2, internal/web/faders.go). Owner: "Always visible, on the bottom of the
// screen." One bar on every screen, collapsible to a thin strip whose state
// is remembered per browser.
//
// One fader per fixture type + mode and one per stored group, from
// GET /api/faders. Each fader SETS its fixtures' dimmer level (0-100 %);
// untouched reads "—" and says "untouched" — a word, never only a colour.
// Release gives a fader's fixtures back; Release all gives back every one.
//
// Writes mirror programmer.js (ProgrammerSync): one at a time, each with the
// fader revision this browser last saw (X-Benny-Faders) and the show token
// (api.js). A refusal as stale (409) re-reads once and retries once. A drag
// is rate-limited per fader to at most one write every 34 ms (< 30/s),
// latest value wins, and the value at release is always sent. Every change
// on the server is announced as {"type":"faders"} and re-read here.
//
// Layout: a fixed bar at the bottom, above the phone's bottom nav
// (--b5-nav-height, app.js). Its height is published as --b5-faders-height
// so sticky bottom bars sit above it and the page body reserves room for it
// (faders.css).
const Faders = (() => {
  const KEY = 'benny512.faders.collapsed';
  const LANE_MS = 34;
  const HINT = 'Faders move the programmer-level dimmer; nothing reaches the rig until ARM';
  let state = null, known = null, running = null, again = false;
  let chain = Promise.resolve();
  let root = null, armed = null, collapsed = false;
  const lanes = new Map();   // fader id -> {pending, busy, last, timer}
  const local = new Map();   // fader id -> level shown while this browser moves it
  let dragging = null;       // {id, track}
  const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const now = () => Date.now();

  function remembered() {
    try { return localStorage.getItem(KEY) === '1'; } catch (_) { return false; }
  }
  function remember(v) {
    try { localStorage.setItem(KEY, v ? '1' : '0'); } catch (_) { /* private mode: not remembered */ }
  }

  // --- reads (single flight, as programmer.js) --------------------------------
  function refresh() {
    if (running) { again = true; return running; }
    running = (async () => {
      try {
        let reads = 0;
        do {
          again = false;
          const s = await Api.getFaders();
          reads++;
          if (known !== null && s.revision < known && reads < 3) { again = true; continue; }
          state = s;
          known = s.revision;
        } while (again);
        render();
        return state;
      } finally {
        running = null;
      }
    })();
    return running;
  }

  // --- writes: serialized, one stale retry ------------------------------------
  function act(action, body) {
    const once = async () => {
      const res = await Api.fadersAction(action, body, known);
      if (res && typeof res.revision === 'number') known = res.revision;
      return res;
    };
    const run = async () => {
      try {
        let res;
        try {
          res = await once();
        } catch (e) {
          if (e.status !== 409) throw e;
          await refresh();
          res = await once();
        }
        say('');
        refresh().catch(() => {});
        return res;
      } catch (e) {
        say(e.message);
        await refresh().catch(() => {});
        throw e;
      }
    };
    const p = chain.then(run, run);
    chain = p.catch(() => {});
    return p;
  }

  function lane(id, level) {
    let l = lanes.get(id);
    if (!l) { l = { pending: null, busy: false, last: 0, timer: null }; lanes.set(id, l); }
    l.pending = level;
    pump(id, l);
  }
  function pump(id, l) {
    if (l.busy || l.pending === null) return;
    const wait = l.last + LANE_MS - now();
    if (wait > 0) {
      if (!l.timer) l.timer = setTimeout(() => { l.timer = null; pump(id, l); }, wait);
      return;
    }
    const level = l.pending;
    l.pending = null;
    l.busy = true;
    l.last = now();
    act('set', { id, level }).catch(() => {}).finally(() => {
      l.busy = false;
      if (l.pending === null && !(dragging && dragging.id === id)) local.delete(id);
      pump(id, l);
    });
  }

  function say(msg) {
    const el = root && root.querySelector('[data-faders-status]');
    if (el) el.textContent = msg;
  }

  // --- drawing ------------------------------------------------------------------
  const pctText = lv => (lv === null || lv === undefined) ? '—' : Math.round(lv * 100) + ' %';
  function levelOf(f) { return local.has(f.id) ? local.get(f.id) : (f.touched ? f.level : null); }

  function faderHTML(f) {
    const lv = levelOf(f);
    const pct = lv === null ? 0 : Math.round(lv * 100);
    const nc = f.notControllable.length;
    return `<div class="b5-fader${lv === null ? ' is-untouched' : ''}" data-gfader-id="${esc(f.id)}" role="group" aria-label="${esc(f.label)}">
      <div class="b5-fader__label" data-gfader-label title="${esc(f.label)}">${esc(f.label)}</div>
      <div class="b5-fader__meta" data-gfader-count>${f.count} ${f.count === 1 ? 'fixture' : 'fixtures'}</div>
      ${nc ? `<div class="b5-fader__meta b5-fader__nc" title="${esc(f.notControllable.join(', '))}">${nc} not controllable</div>` : ''}
      <div class="b5-fader__value" data-gfader-value>${pctText(lv)}</div>
      <div class="b5-fader__track" data-gfader-track role="slider" tabindex="0" aria-label="${esc(f.label)} dimmer level"
        aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct}" aria-valuetext="${lv === null ? 'untouched' : pct + ' percent'}">
        <div class="b5-fader__fill" style="height:${pct}%"></div>
        <div class="b5-fader__thumb" style="bottom:${pct}%"></div>
      </div>
      <button type="button" class="b5-btn b5-btn--sm b5-fader__release" data-gfader-release ${f.touched || local.has(f.id) ? '' : 'disabled'} aria-label="Release ${esc(f.label)}">Release</button>
    </div>`;
  }

  // paint updates one fader in place (during a drag the bar is not rebuilt).
  function paint(id) {
    const el = root.querySelector('[data-gfader-id="' + id + '"]');
    const f = state && state.faders.find(x => x.id === id);
    if (!el || !f) return;
    const lv = levelOf(f);
    const pct = lv === null ? 0 : Math.round(lv * 100);
    el.querySelector('[data-gfader-value]').textContent = pctText(lv);
    const tr = el.querySelector('[data-gfader-track]');
    tr.setAttribute('aria-valuenow', String(pct));
    tr.setAttribute('aria-valuetext', lv === null ? 'untouched' : pct + ' percent');
    el.querySelector('.b5-fader__fill').style.height = pct + '%';
    el.querySelector('.b5-fader__thumb').style.bottom = pct + '%';
    el.classList.toggle('is-untouched', lv === null);
    el.querySelector('[data-gfader-release]').disabled = !(f.touched || local.has(id));
  }

  // shape is what a rebuild would change; levels alone are painted in place,
  // so focus and an in-progress drag keep their element.
  let drawnShape = null;
  const shapeOf = faders => JSON.stringify(faders.map(f => [f.id, f.kind, f.label, f.count, f.notControllable]));

  function renderHint() {
    const h = root && root.querySelector('[data-faders-hint]');
    if (!h) return;
    const st = armed !== null ? armed : (state && state.output ? state.output.state : null);
    h.hidden = st === 'armed';
  }

  function render() {
    if (!root) return;
    if (dragging) return; // rebuilt on release
    const body = root.querySelector('[data-faders-body]');
    const focused = document.activeElement && document.activeElement.closest && document.activeElement.closest('[data-gfader-id]');
    const focusId = focused ? focused.getAttribute('data-gfader-id') : null;
    const scroll = root.querySelector('[data-faders-rows]');
    const left = scroll ? scroll.scrollLeft : 0;
    const faders = (state && state.faders) || [];
    root.querySelector('[data-faders-release-all]').disabled = !faders.some(f => f.touched);
    renderHint();
    const shape = shapeOf(faders);
    if (shape === drawnShape) { faders.forEach(f => paint(f.id)); return; }
    drawnShape = shape;
    const types = faders.filter(f => f.kind === 'type'), groups = faders.filter(f => f.kind === 'group');
    const section = (kind, title, list, empty) => `<section class="b5-faders__section" data-faders-section="${kind}">
      <h3 class="b5-faders__heading">${title}</h3>
      <div class="b5-faders__row">${list.length ? list.map(faderHTML).join('') : `<p class="b5-faders__empty">${empty}</p>`}</div></section>`;
    body.querySelector('[data-faders-rows]').innerHTML =
      section('type', 'Fixture types', types, 'No fixtures patched.') +
      section('group', 'Groups', groups, 'No stored groups.');
    const rows = body.querySelector('[data-faders-rows]');
    rows.scrollLeft = left;
    if (focusId) {
      const t = root.querySelector('[data-gfader-id="' + focusId + '"] [data-gfader-track]');
      if (t && t.focus) t.focus();
    }
  }

  function setCollapsed(v) {
    collapsed = v;
    const t = root.querySelector('[data-faders-toggle]');
    t.textContent = 'Faders · ' + (v ? 'show' : 'hide');
    t.setAttribute('aria-expanded', v ? 'false' : 'true');
    root.querySelector('[data-faders-body]').hidden = v;
    root.classList.toggle('is-collapsed', v);
  }

  // --- input ----------------------------------------------------------------------
  function move(id, level) {
    level = Math.min(1, Math.max(0, level));
    local.set(id, level);
    paint(id);
    lane(id, level);
  }
  function levelAt(track, clientY) {
    const r = track.getBoundingClientRect();
    if (!r || !r.height) return null;
    return Math.round((1 - (clientY - r.top) / r.height) * 10000) / 10000;
  }
  function onKey(e) {
    const tr = e.target.closest && e.target.closest('[data-gfader-track]');
    if (!tr) return;
    const id = tr.closest('[data-gfader-id]').getAttribute('data-gfader-id');
    const f = state.faders.find(x => x.id === id);
    const cur = Math.round((levelOf(f) || 0) * 100);
    const steps = { ArrowUp: 1, ArrowRight: 1, ArrowDown: -1, ArrowLeft: -1, PageUp: 10, PageDown: -10 };
    let pct;
    if (e.key in steps) pct = cur + steps[e.key];
    else if (e.key === 'Home') pct = 0;
    else if (e.key === 'End') pct = 100;
    else return;
    e.preventDefault();
    move(id, Math.min(100, Math.max(0, pct)) / 100);
  }
  function onDown(e) {
    const tr = e.target.closest && e.target.closest('[data-gfader-track]');
    if (!tr) return;
    const id = tr.closest('[data-gfader-id]').getAttribute('data-gfader-id');
    const lv = levelAt(tr, e.clientY);
    if (lv === null) return;
    e.preventDefault();
    if (tr.setPointerCapture && e.pointerId !== undefined) { try { tr.setPointerCapture(e.pointerId); } catch (_) { /* not capturable */ } }
    if (tr.focus) tr.focus();
    dragging = { id, track: tr };
    move(id, lv);
  }
  function onMove(e) {
    if (!dragging) return;
    const lv = levelAt(dragging.track, e.clientY);
    if (lv !== null) move(dragging.id, lv);
  }
  function onUp(e) {
    if (!dragging) return;
    const { id, track } = dragging;
    const lv = e.clientY !== undefined ? levelAt(track, e.clientY) : null;
    dragging = null;
    // The final value is always sent, even if it equals the last write.
    move(id, lv !== null ? lv : (local.has(id) ? local.get(id) : 0));
    render();
  }

  function publishHeight() {
    const measure = () => {
      const r = root.getBoundingClientRect();
      document.documentElement.style.setProperty('--b5-faders-height', (r && r.height ? r.height : 0) + 'px');
    };
    measure();
    if (typeof ResizeObserver === 'function') new ResizeObserver(measure).observe(root);
    window.addEventListener('resize', measure);
  }

  function init() {
    root = document.getElementById('faderBar');
    if (!root || root.querySelector('[data-faders-toggle]')) return;
    root.innerHTML = `<div class="b5-faders__head">
        <button type="button" class="b5-btn b5-btn--sm b5-faders__toggle" data-faders-toggle aria-controls="faderBarBody">Faders · hide</button>
        <span class="b5-faders__status" data-faders-status role="status"></span>
        <button type="button" class="b5-btn b5-btn--sm" data-faders-release-all disabled>Release all</button>
      </div>
      <div class="b5-faders__body" id="faderBarBody" data-faders-body>
        <p class="b5-faders__hint" data-faders-hint>${HINT}</p>
        <div class="b5-faders__rows" data-faders-rows></div>
      </div>`;
    root.querySelector('[data-faders-toggle]').addEventListener('click', () => { setCollapsed(!collapsed); remember(collapsed); });
    root.querySelector('[data-faders-release-all]').addEventListener('click', () => { local.clear(); act('release-all', {}).catch(() => {}); });
    root.addEventListener('click', e => {
      const b = e.target.closest && e.target.closest('[data-gfader-release]');
      if (!b) return;
      const id = b.closest('[data-gfader-id]').getAttribute('data-gfader-id');
      local.delete(id);
      act('release', { id }).catch(() => {});
    });
    root.addEventListener('keydown', onKey);
    root.addEventListener('pointerdown', onDown);
    root.addEventListener('pointermove', onMove);
    root.addEventListener('pointerup', onUp);
    root.addEventListener('pointercancel', onUp);
    setCollapsed(remembered());
    publishHeight();
    window.addEventListener('b5-output', e => { armed = e.detail ? e.detail.state : null; renderHint(); });
    window.addEventListener('b5-show-changed', () => { local.clear(); refresh().catch(() => {}); });
    Live.on('faders', msg => { if (msg.revision !== known) refresh().catch(() => {}); });
    Live.on('connected', () => { refresh().catch(() => {}); });
    refresh().catch(e => say(e.message));
  }

  return { init, refresh, state: () => state };
})();
