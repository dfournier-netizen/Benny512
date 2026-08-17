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

  return { icon, badge, tag, spinner, buildApplyField, wireApplyField };
})();
