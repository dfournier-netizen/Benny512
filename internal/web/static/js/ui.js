// ui.js — small shared presentation helpers for the Benny512 design system
// retheme. Pure markup/DOM builders only: nothing here fetches, mutates
// server state, or owns event semantics beyond the generic Apply-to-confirm
// wiring every screen already implemented independently (nodes.js,
// devicedetail.js, walk.js each had their own copy of the same
// dirty/apply/revert dance — consolidated here so the b5-field state
// modifiers (--dirty/--applying/--success/--error) are applied consistently
// everywhere instead of hand-rolled per call site). No behavior change: the
// same oninput-stages/onchange-or-click-commits contract every screen
// already followed.
const UI = (() => {
  function icon(name, cls) {
    return `<svg${cls ? ` class="${cls}"` : ''}><use href="/icons/benny512-icons.svg#b5-icon-${name}"/></svg>`;
  }

  const STATUS_ICON = { ok: 'status-ok', warning: 'status-warning', error: 'status-error', pending: 'status-pending' };

  // badge: status in {ok,warning,error,pending,stale,unknown}. Text is
  // always required — color is never the only signal (design-spec "Do").
  function badge(status, text) {
    const ic = STATUS_ICON[status] ? icon(STATUS_ICON[status]) : '';
    return `<span class="b5-badge b5-badge--${status}">${ic}${escapeHtml(text)}</span>`;
  }

  function tag(text, variant) {
    const cls = variant ? ` b5-tag--${variant}` : '';
    return `<span class="b5-tag${cls}">${escapeHtml(text)}</span>`;
  }

  function spinner() { return '<span class="b5-spinner"></span>'; }

  // --- universe numbering ---------------------------------------------------
  // Every universe this app stores or transmits is the RAW Art-Net
  // Port-Address: the flat 15-bit Net:Sub-Net:Universe value, 0-32767
  // (Entry.Universe, artnet.PortAddress, walk's PortAddress). That never
  // changes, and nothing here writes to it.
  //
  // What changes is how a screen NAMES a universe, and there are exactly two
  // legitimate answers:
  //
  //   Art-Net universe — the flat Port-Address, what is actually on the
  //     wire and what a gateway's own faceplate shows. Protocol-facing
  //     screens (Nodes, Analyzer) use this, and show it as ONE number:
  //     universe 17 is typed and read as 17, never decomposed into
  //     "Net 0, Sub-Net 1, Universe 1" on screen.
  //
  //   User universe — what the show calls it, starting at 1. Operator-facing
  //     screens (Patch, Rig Check / Function check, Rig Walk, Send) use
  //     this, because a patch sheet says "universe 1" and the operator
  //     should not have to translate.
  //
  //   Devices shows BOTH, because it is the screen where a physical port
  //     and a patched fixture meet and the two numberings have to be
  //     reconciled by eye.
  //
  // artnetStart is Settings.artnetStartUniverse: the Art-Net universe that
  // USER UNIVERSE 1 lives on. Default 0, so user 1 = Art-Net 0. Set it to 1
  // and user 1 = Art-Net 1. Set it to 100 and a show handed the block
  // 100-139 numbers its own universes 1-40, which is the case a plain
  // 0-or-1 toggle could never express.
  //
  // This replaces the old `universeBase` (0|1) notation switch. That was one
  // function, formatUniverse, whose name did not say WHICH numbering it
  // produced, applied globally to every screen at once -- so the Nodes tab
  // disagreed with the gateway faceplate and no call site was obviously
  // wrong. The two conversions below are deliberately named for what they
  // return, so a screen cannot use one while meaning the other.
  let artnetStart = 0;
  function getArtnetStart() { return artnetStart; }
  function setArtnetStart(v) {
    v = Number(v);
    if (!Number.isFinite(v) || v < 0 || v > 32767) v = 0;
    v = Math.floor(v);
    if (v === artnetStart) return;
    artnetStart = v;
    window.dispatchEvent(new CustomEvent('b5-universe-base-changed', { detail: { artnetStart: v } }));
  }

  // artnetToUser: raw Port-Address -> user universe, or NULL when the raw
  // universe sits BELOW the show's starting universe and therefore has no
  // user number at all.
  //
  // Null rather than a negative: with a start of 100, a node port on Art-Net
  // 5 is genuinely outside this show's block. "Universe -94" reads as an
  // arithmetic bug and invites someone to "fix" it; the honest answer is
  // that the port is real and outside the range, which formatUser says in
  // words.
  function artnetToUser(raw) {
    const n = Math.floor(Number(raw) || 0);
    const u = n - artnetStart + 1;
    return u >= 1 ? u : null;
  }
  // userToArtnet: user universe -> raw Port-Address. Clamped to the wire
  // range; never returns a negative Port-Address.
  function userToArtnet(user) {
    const n = Math.floor(Number(user) || 0);
    return Math.min(32767, Math.max(0, n + artnetStart - 1));
  }

  // formatUser / parseUser: the operator-facing pair.
  function formatUser(raw) {
    const u = artnetToUser(raw);
    return u === null ? OUTSIDE_SHOW : String(u);
  }
  function parseUser(displayed) { return userToArtnet(displayed); }

  // --- sACN universe numbering ---------------------------------------------
  // A THIRD numbering, and it is named like the other two: it says what it
  // RETURNS. formatUser returns the show's own number, formatArtnet returns
  // the Art-Net Port-Address, formatSacn returns the sACN universe. That
  // naming rule is not style — one ambiguous `formatUniverse` applied across
  // every screen is what shipped a universe off-by-one here.
  //
  // This is the browser-side twin of sacn.ArtnetPortAddressToSACNUniverse
  // (internal/sacn/mapping.go), and it must agree with it exactly, because
  // the server REFUSES a scope it cannot map (422) and this screen's whole
  // job is to say so before the operator presses START.
  //
  //   showUniverse = raw - artnetStart + 1
  //   sacnUniverse = sacnStart + showUniverse - 1
  //
  // sacnStart is sacn.Settings.StartUniverse (GET/POST /api/sacn): the sACN
  // universe the show's universe 1 lives on. Default 1, because sACN
  // universes start at 1 — ANSI E1.31-2025 reserves 0.
  let sacnStart = 1;
  const SACN_MIN_UNIVERSE = 1;
  const SACN_MAX_UNIVERSE = 63999; // sacn.maxUniverse / codec.go's ErrInvalidUniverse bound
  function getSacnStart() { return sacnStart; }
  function setSacnStart(v) {
    v = Number(v);
    if (!Number.isFinite(v) || v < SACN_MIN_UNIVERSE || v > SACN_MAX_UNIVERSE) v = 1;
    v = Math.floor(v);
    if (v === sacnStart) return;
    sacnStart = v;
    window.dispatchEvent(new CustomEvent('b5-sacn-start-changed', { detail: { sacnStart: v } }));
  }

  // artnetToSacn: raw Port-Address -> sACN universe, or NULL when that
  // universe has no sACN universe at all.
  //
  // NULL, NEVER A CLAMP. Art-Net Port-Address 0 is perfectly legal and is
  // this app's default show universe 1; sACN universe 0 does not exist. With
  // an Art-Net starting universe above 0, a fixture patched below it is a
  // show universe of 0 or less and maps outside sACN's range entirely.
  // Clamping to 1 would light the WRONG universe on a real rig and nothing
  // on screen would say so. The server refuses the same case with a 422
  // naming the universe; this returns null so the screen can warn first.
  //
  // Takes an optional explicit start pair so a settings form can describe
  // the value currently in the box, before it is applied — the same reason
  // settings.js's renderUniverseNotes composes from the pending value.
  function artnetToSacn(raw, opts) {
    opts = opts || {};
    const aStart = opts.artnetStart === undefined ? artnetStart : Math.floor(Number(opts.artnetStart) || 0);
    const sStart = opts.sacnStart === undefined ? sacnStart : Math.floor(Number(opts.sacnStart) || 0);
    const n = Math.floor(Number(raw) || 0);
    const show = n - aStart + 1;
    const u = sStart + show - 1;
    if (!Number.isFinite(u) || u < SACN_MIN_UNIVERSE || u > SACN_MAX_UNIVERSE) return null;
    return u;
  }

  // formatSacn: the sACN universe as a string, or NO_SACN_UNIVERSE in words.
  // Rule 3 of the screen kit — name the unknown, never print a plausible 0.
  const NO_SACN_UNIVERSE = 'no sACN universe';
  function formatSacn(raw, opts) {
    const u = artnetToSacn(raw, opts);
    return u === null ? NO_SACN_UNIVERSE : String(u);
  }

  // sacnMulticastAddress: the IPv4 multicast group an sACN universe is
  // transmitted to, per ANSI E1.31-2025 Table 9-10 — octet 1 is 239, octet 2
  // is 255, octet 3 is the universe high byte, octet 4 is the low byte. The
  // browser-side twin of sacn.MulticastIP (sender.go). Returns '' for a
  // universe that does not exist, so a caller cannot print a group for one.
  function sacnMulticastAddress(sacnUniverse) {
    const u = Math.floor(Number(sacnUniverse) || 0);
    if (u < SACN_MIN_UNIVERSE || u > SACN_MAX_UNIVERSE) return '';
    return `239.255.${(u >> 8) & 0xff}.${u & 0xff}`;
  }

  // formatArtnet / parseArtnet: the protocol-facing pair. Identity in both
  // directions -- the flat Port-Address, shown and typed as itself.
  function formatArtnet(raw) { return String(Math.floor(Number(raw) || 0)); }
  function parseArtnet(displayed) {
    const n = Math.floor(Number(displayed) || 0);
    return Math.min(32767, Math.max(0, n));
  }

  // OUTSIDE_SHOW is what a user-universe field shows for a raw universe
  // below the starting universe. An em dash, not a number and not an empty
  // cell: empty reads as "not loaded yet".
  const OUTSIDE_SHOW = '\u2014';

  // formatBoth: Devices' rendering, where a physical port's Art-Net universe
  // and the show's own numbering both matter and must be reconcilable at a
  // glance. Art-Net is named explicitly because it is the one an operator
  // will compare against a gateway faceplate.
  function formatBoth(raw) {
    const n = Math.floor(Number(raw) || 0);
    const u = artnetToUser(n);
    return u === null
      ? `Art-Net ${n} (outside show range)`
      : `${u} (Art-Net ${n})`;
  }

  // Input bounds. userInputAttrs starts at 1 because there is no user
  // universe 0; artnetInputAttrs spans the whole wire range.
  function userInputAttrs() {
    return { min: 1, max: 32767 - artnetStart + 1 };
  }
  function artnetInputAttrs() { return { min: 0, max: 32767 }; }

  // universeScheme: the short note a screen appends to a "Universe" field
  // label so which numbering is in play is never left to inference. This is
  // the ambiguity the old single formatUniverse created.
  function universeScheme(kind) {
    if (kind === 'artnet') return 'Art-Net universe';
    return artnetStart === 0
      ? 'show universe (user 1 = Art-Net 0)'
      : `show universe (user 1 = Art-Net ${artnetStart})`;
  }

  // --- generic Apply-to-confirm field (DOM builder) --------------------------
  // buildApplyField returns {wrap, input, applyBtn, revertBtn, status}. wrap
  // is a complete .b5-field (label + input row + Apply/Revert actions +
  // status line) ready to append into any container. wireApplyField then
  // attaches the standard oninput-stages/Apply-commits/Revert-discards
  // behavior, toggling the field's state modifier class exactly like the
  // design system's .b5-field--dirty/--applying/--success/--error states.
  function buildApplyField(opts) {
    opts = opts || {};
    const wrap = document.createElement('div');
    wrap.className = 'b5-field';
    if (opts.readonly) wrap.classList.add('b5-field--readonly');

    if (opts.label) {
      const label = document.createElement('label');
      label.className = 'b5-field__label';
      label.textContent = opts.label;
      wrap.appendChild(label);
    }

    const row = document.createElement('div');
    row.className = 'b5-field__row';

    let input;
    if (opts.kind === 'select') {
      input = document.createElement('select');
      input.className = 'b5-select';
      (opts.options || []).forEach(o => {
        const op = document.createElement('option');
        op.value = o.value; op.textContent = o.label;
        if (o.value === opts.value) op.selected = true;
        input.appendChild(op);
      });
    } else {
      input = document.createElement('input');
      input.type = opts.kind === 'number' ? 'number' : 'text';
      input.className = 'b5-input' + (opts.mono ? ' b5-input--mono' : '');
      input.value = opts.value === undefined || opts.value === null ? '' : opts.value;
      if (opts.maxLength) input.maxLength = opts.maxLength;
      if (opts.min !== undefined) input.min = opts.min;
      if (opts.max !== undefined) input.max = opts.max;
    }
    input.disabled = !opts.enabled;
    if (opts.readonly) input.readOnly = true;
    // opts.name stamps a stable data-b5-field identifier on the live
    // control (input or select) — a caller whose container gets rebuilt out
    // from under an in-progress edit (an unrelated WS-driven data update
    // forcing a re-render of the section this field lives in) can use it to
    // find "was this exact field focused, with what draft value/caret" just
    // before tearing the DOM down, and restore that after — see
    // devicedetail.js's captureFieldFocus/restoreFieldFocus. Optional: a
    // caller that never rebuilds its container while this field could be
    // focused (most of nodes.js's config fields, which are built once per
    // node selection, not on every refresh) has no need for it.
    if (opts.name) input.dataset.b5Field = opts.name;
    row.appendChild(input);

    let applyBtn = null, revertBtn = null;
    if (!opts.readonly) {
      const actions = document.createElement('span');
      actions.className = 'b5-field__actions';
      applyBtn = document.createElement('button');
      applyBtn.type = 'button'; applyBtn.className = 'b5-btn b5-btn--sm b5-btn--primary'; applyBtn.disabled = true;
      applyBtn.innerHTML = icon('apply') + 'Apply';
      revertBtn = document.createElement('button');
      revertBtn.type = 'button'; revertBtn.className = 'b5-btn b5-btn--sm b5-btn--ghost';
      revertBtn.innerHTML = icon('revert') + 'Revert';
      revertBtn.style.display = 'none';
      actions.appendChild(applyBtn);
      actions.appendChild(revertBtn);
      row.appendChild(actions);
    }
    wrap.appendChild(row);

    const status = document.createElement('span');
    status.className = 'b5-field__status';
    status.style.display = 'none';
    wrap.appendChild(status);

    if (opts.hint) {
      const hint = document.createElement('span');
      hint.className = 'b5-field__hint';
      hint.textContent = opts.hint;
      wrap.appendChild(hint);
    }

    return { wrap, input, applyBtn, revertBtn, status };
  }

  // wireApplyField: oninput/onchange only toggles dirty/Apply/Revert
  // visibility (never sends anything); Apply reads the live value at click
  // time, calls onApply(value), and reflects applying/success/error back
  // onto the field's own state — same Apply-to-confirm contract every
  // screen already used, just centralized.
  function wireApplyField(field, baseline, onApply, statusSetter) {
    const { wrap, input, applyBtn, revertBtn, status } = field;
    const refresh = () => {
      const dirty = input.value !== baseline;
      wrap.classList.remove('b5-field--applying', 'b5-field--success', 'b5-field--error');
      wrap.classList.toggle('b5-field--dirty', dirty);
      if (applyBtn) applyBtn.disabled = !dirty;
      if (revertBtn) revertBtn.style.display = dirty ? '' : 'none';
      status.style.display = dirty ? '' : 'none';
      status.textContent = dirty ? 'Unsaved change' : '';
    };
    input.addEventListener('input', refresh);
    input.addEventListener('change', refresh);
    if (revertBtn) revertBtn.addEventListener('click', () => { input.value = baseline; refresh(); });
    if (applyBtn) applyBtn.addEventListener('click', async () => {
      applyBtn.disabled = true;
      wrap.classList.remove('b5-field--dirty');
      wrap.classList.add('b5-field--applying');
      status.style.display = '';
      status.innerHTML = spinner() + 'Applying…';
      try {
        await onApply(input.value);
        baseline = input.value;
        wrap.classList.remove('b5-field--applying');
        wrap.classList.add('b5-field--success');
        status.innerHTML = icon('status-ok') + 'Applied';
        setTimeout(() => { wrap.classList.remove('b5-field--success'); refresh(); }, 1500);
      } catch (e) {
        wrap.classList.remove('b5-field--applying');
        wrap.classList.add('b5-field--error');
        status.innerHTML = icon('status-error') + ('Error: ' + e.message);
        if (statusSetter) statusSetter('error: ' + e.message);
        applyBtn.disabled = false;
      }
    });
    refresh();
    return refresh;
  }

  return {
    icon, badge, tag, spinner, buildApplyField, wireApplyField,
    getArtnetStart, setArtnetStart,
    artnetToUser, userToArtnet,
    formatUser, parseUser, userInputAttrs,
    formatArtnet, parseArtnet, artnetInputAttrs,
    getSacnStart, setSacnStart, artnetToSacn, formatSacn, sacnMulticastAddress,
    SACN_MIN_UNIVERSE, SACN_MAX_UNIVERSE, NO_SACN_UNIVERSE,
    formatBoth, universeScheme, OUTSIDE_SHOW,
  };
})();
