// reconcile.js — the Reconcile screen: two panes, a commit relation between
// them, and a per-field diff you push one press at a time.
//
// Extracted out of patch.js the same way rigcheck.js was, and for the same
// reason: what was there could not express what the owner actually asked
// for. The old view was a read-only accordion grouped by matcher status —
// "Matched (2)", "Missing (4)", "Unpatched (7)" — and a diagnosis of it is
// short: the two groups holding the most rows had ZERO buttons in them. An
// entry the matcher was 100% sure about could not be committed; a detected
// device could not be attached to anything; and a pairing that HAD been
// confirmed rendered as "matched", whose action list was the empty string,
// so there was no way to break it again. The backend matcher was fine. The
// screen just never offered the two verbs the whole feature is named after.
//
//   "I envision it as a window with two separate boxes, one for fixtures in
//    the patch ('intended fixtures') and one for fixtures detected on the
//    RDM line. Along with this, controls to 'commit' and create a relation
//    between the two, or to 'decommit' and break that relation. ... if for
//    some reason I am not able to get that fixture and need to substitute in
//    another of the same type, I'd like to be able to 'decommit' the old and
//    'commit' the new — taking on all the old settings of the original
//    fixture, ensuring it's configured as it should."
//
// Four rules hold this file together:
//
//  1. THE SERVER BOARD IS THE ONLY STATE. `board` below is the last response
//     from GET /api/patch/reconcile/board, and every mutator does
//     `board = null; ...; await refresh(); render();`. Which entry owns
//     which device, what was read off each fixture and how it compares to
//     the patch are all computed server-side (internal/web/reconcilecommit.go
//     + internal/patch/asfound.go) and painted here verbatim. There is no
//     optimistic update anywhere in this file — after a push, what you see
//     is what the fixture answered on the RE-READ, not what we hoped the
//     write did.
//
//  2. COMMIT NEVER WRITES TO A FIXTURE. Committing records the relation and
//     READS the device. A difference reaches the light only through a
//     per-difference "Apply to fixture" press (Apply-to-confirm, this
//     project's standing contract) — one field, one press, one confirm()
//     naming exactly what will change. There is deliberately no "apply
//     everything" button on a diff: writing an address to the wrong fixture
//     on a show site is a real cost.
//
//  3. SUGGESTIONS ARE OFFERED, NEVER APPLIED. The weighted matcher's
//     proposals arrive as chips the owner accepts or rejects. Where it
//     flags ambiguity, EVERY candidate is listed with its own evidence
//     spelled out, because "why did this match?" is the only thing standing
//     between a tired tech and committing the wrong one of two identical
//     lights.
//
//  4. UNIVERSE NUMBERS GO THROUGH UI.formatUniverse, ALWAYS. The server
//     sends raw 0-based Art-Net Port-Addresses as NUMBERS and never composes
//     a sentence containing one. Every universe on this screen is formatted
//     here, at the presentation boundary, from a number — never re-derived
//     from an already-displayed string, which would silently shift the wire
//     value the next time the base setting changed.
//
// Tablet-first, matching rigcheck.js: used standing up, in the dark, on a
// tablet at arm's length under a truss. Rows are big, every action is a
// 44px+ target, and state is never carried by colour alone — a committed row
// says the word COMMITTED, carries a link icon, and is joined to its partner
// by a matching numbered link badge on both sides.
const ReconcilePanel = (() => {
  // host: supplied by patch.js — the three things this panel cannot know.
  let host = { setStatus: () => {}, getUniverseLabel: () => 'Universe' };

  // board: the last full server snapshot. null before the first fetch and
  // between a mutation and its refresh (which is what paints the spinner).
  let board = null;
  let el = null; // the element this panel renders into
  let loadError = '';

  // armed: the half of a pairing the owner has picked first. Exactly one of
  // these is ever non-null. Arming an entry makes every uncommitted DEVICE
  // show a "Commit here" button and vice versa, so a commit is two presses
  // from either direction — which matters because on site he is looking at a
  // physical light and wants to find its patch row, while in the shop he is
  // working down the patch.
  let armedEntryId = null;
  let armedDeviceUid = null;

  // openEntryId: which committed entry has its diff panel expanded. Pure
  // presentation, deliberately not on the server.
  let openEntryId = null;

  // busy: an in-flight mutation. Disables every action so a double-tap on a
  // tablet cannot fire two commits.
  let busy = false;

  // filterText / needsAttentionOnly: on a 200-fixture rig the useful view is
  // "what still needs me", not the whole board.
  let filterText = '';
  let needsAttentionOnly = false;

  function init(h) { host = Object.assign(host, h || {}); }

  function attach(node) { el = node; }

  async function enter(node) {
    el = node;
    board = null;
    render();
    await refresh();
    render();
  }

  function leave() { armedEntryId = null; armedDeviceUid = null; }

  async function refresh() {
    loadError = '';
    try {
      board = await Api.getReconcileBoard();
    } catch (e) {
      board = null;
      loadError = e.message;
    }
  }

  // --- small helpers --------------------------------------------------------

  const esc = (s) => escapeHtml(s == null ? '' : String(s));

  // uni: the ONE place a universe number becomes text on this screen (rule 4
  // above). Takes the raw 0-based wire value the server sent and nothing
  // else — never a string that was already displayed.
  const uni = (raw) => UI.formatUniverse(raw);

  // when: a read timestamp rendered as something a tech can act on. "3 weeks
  // ago" versus "2 min ago" is the difference between a shop reading he
  // should re-check and a site reading he can trust, and that distinction is
  // the owner's whole workflow — so relative age leads, with the clock time
  // beside it.
  function when(iso) {
    if (!iso) return '';
    const t = new Date(iso).getTime();
    if (!isFinite(t) || t <= 0) return '';
    const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
    let rel;
    if (secs < 60) rel = secs + 's ago';
    else if (secs < 3600) rel = Math.round(secs / 60) + ' min ago';
    else if (secs < 86400) rel = Math.round(secs / 3600) + ' h ago';
    else rel = Math.round(secs / 86400) + ' d ago';
    return rel + ' (' + new Date(t).toLocaleTimeString() + ')';
  }

  function entryLabel(row) {
    return row.name || row.fixtureType || row.fixtureNumber || row.entryId;
  }

  function deviceLabel(dev) {
    const parts = [dev.manufacturer, dev.model].filter(Boolean).join(' ');
    return dev.label || parts || dev.uid;
  }

  // linkNumbers: a committed pair gets the SAME number badge on both sides,
  // so the relation is readable at a glance across two panes without drawing
  // a line between them — and readable without colour, which is the
  // accessibility constraint that rules out "just tint the matching rows".
  function linkNumbers() {
    const map = {};
    let n = 0;
    (board.intended || []).forEach(row => {
      if (row.committed && row.committedUid) map[row.committedUid] = ++n;
    });
    return map;
  }

  function matchesFilter(text) {
    if (!filterText) return true;
    return String(text || '').toLowerCase().includes(filterText.toLowerCase());
  }

  // --- render ---------------------------------------------------------------

  function render() {
    if (!el) return;
    if (loadError) {
      el.innerHTML = `<div class="b5-alert b5-alert--error b5-rcb-alert">${UI.icon('status-error')}<div><strong>Could not load the reconcile board.</strong><div class="b5-text-sm">${esc(loadError)}</div></div></div>
        <button id="rcbRetry" class="b5-btn">${UI.icon('refresh')}Try again</button>`;
      const b = el.querySelector('#rcbRetry');
      if (b) b.addEventListener('click', async () => { await refresh(); render(); });
      return;
    }
    if (!board) {
      el.innerHTML = `<span class="b5-inline-wait">${UI.spinner()}Reading the patch and the RDM line… (this resolves every device's live DMX address, so it can take a moment on a large rig)</span>`;
      return;
    }

    const links = linkNumbers();
    const proposalsByEntry = {};
    (board.proposals || []).forEach(p => { proposalsByEntry[p.entryId] = p; });

    el.innerHTML = `
      ${renderToolbar()}
      ${renderArmedBanner()}
      <div class="b5-rcb-panes">
        <section class="b5-rcb-pane" aria-labelledby="rcbIntendedHead">
          <h3 class="b5-rcb-pane__head" id="rcbIntendedHead">
            <span class="b5-rcb-pane__title">Intended fixtures</span>
            <span class="b5-rcb-pane__sub">what the patch says should be there</span>
          </h3>
          <div class="b5-rcb-list">${renderIntendedPane(links, proposalsByEntry)}</div>
        </section>
        <section class="b5-rcb-pane" aria-labelledby="rcbDetectedHead">
          <h3 class="b5-rcb-pane__head" id="rcbDetectedHead">
            <span class="b5-rcb-pane__title">Detected on the RDM line</span>
            <span class="b5-rcb-pane__sub">what actually answered</span>
          </h3>
          <div class="b5-rcb-list">${renderDetectedPane(links)}</div>
        </section>
      </div>
    `;
    wire();
  }

  function renderToolbar() {
    const intended = board.intended || [];
    const detected = board.detected || [];
    const committed = intended.filter(r => r.committed).length;
    const differing = intended.filter(r => r.committed && r.differsCount > 0).length;
    const unclaimed = detected.filter(d => !d.committedToEntryId).length;
    return `
      <div class="b5-rcb-toolbar">
        <div class="b5-rcb-toolbar__row">
          <button id="rcbRefresh" class="b5-btn" ${busy ? 'disabled' : ''}>${UI.icon('refresh')}Refresh</button>
          <label class="b5-rcb-search">
            <span class="b5-visually-hidden">Filter both panes</span>
            <input id="rcbFilter" type="search" placeholder="Filter by name, type, position, UID…" value="${esc(filterText)}">
          </label>
          <button id="rcbAttention" class="b5-rcb-seg2 ${needsAttentionOnly ? 'is-on' : ''}" aria-pressed="${needsAttentionOnly}">
            ${needsAttentionOnly ? UI.icon('status-ok') : UI.icon('filter')}Needs attention only
          </button>
          <button id="rcbExportJson" class="b5-btn">${UI.icon('export')}Export JSON</button>
          <button id="rcbExportTxt" class="b5-btn">${UI.icon('export')}Export TXT</button>
        </div>
        <p class="b5-rcb-tally">
          <strong>${committed}</strong> of <strong>${intended.length}</strong> patch entries committed ·
          <strong>${differing}</strong> with settings that differ ·
          <strong>${unclaimed}</strong> detected fixture${unclaimed === 1 ? '' : 's'} not committed to anything
        </p>
      </div>`;
  }

  function renderArmedBanner() {
    if (armedEntryId) {
      const row = (board.intended || []).find(r => r.entryId === armedEntryId);
      if (!row) return '';
      return `<div class="b5-rcb-armed">
        <span class="b5-rcb-armed__label">Committing <strong>${esc(entryLabel(row))}</strong> — now pick the fixture that answers for it on the right.</span>
        <button class="b5-btn b5-btn--ghost" data-rcb-disarm="1">Cancel</button>
      </div>`;
    }
    if (armedDeviceUid) {
      const dev = (board.detected || []).find(d => d.uid === armedDeviceUid);
      if (!dev) return '';
      return `<div class="b5-rcb-armed">
        <span class="b5-rcb-armed__label">Committing fixture <strong class="b5-text-mono">${esc(dev.uid)}</strong> — now pick the patch entry it belongs to on the left.</span>
        <button class="b5-btn b5-btn--ghost" data-rcb-disarm="1">Cancel</button>
      </div>`;
    }
    return '';
  }

  // --- left pane: intended fixtures -----------------------------------------

  function renderIntendedPane(links, proposalsByEntry) {
    let rows = board.intended || [];
    rows = rows.filter(r => matchesFilter([r.name, r.fixtureType, r.position, r.fixtureNumber, r.committedUid].join(' ')));
    if (needsAttentionOnly) rows = rows.filter(r => !r.committed || r.differsCount > 0 || !r.deviceOnline);
    if (!rows.length) return emptyNote(board.intended.length ? 'Nothing in this pane matches the current filter.' : 'This patch has no entries yet. Add them on the Entries tab.');
    return rows.map(r => renderIntendedRow(r, links, proposalsByEntry[r.entryId])).join('');
  }

  function renderIntendedRow(row, links, proposal) {
    const link = links[row.committedUid];
    const armed = armedEntryId === row.entryId;
    const open = openEntryId === row.entryId;

    // State is spelled out in WORDS first; the icon and border are
    // reinforcement, never the signal itself.
    let stateChip, cls;
    if (!row.committed) {
      stateChip = `<span class="b5-rcb-chip b5-rcb-chip--open">Not committed</span>`;
      cls = 'is-uncommitted';
    } else if (!row.deviceOnline) {
      stateChip = `<span class="b5-rcb-chip b5-rcb-chip--warn">${UI.icon('status-warning')}Committed · fixture not answering</span>`;
      cls = 'is-offline';
    } else if (row.differsCount > 0) {
      stateChip = `<span class="b5-rcb-chip b5-rcb-chip--warn">${UI.icon('status-warning')}Committed · ${row.differsCount} setting${row.differsCount === 1 ? '' : 's'} differ${row.differsCount === 1 ? 's' : ''}</span>`;
      cls = 'is-differs';
    } else {
      stateChip = `<span class="b5-rcb-chip b5-rcb-chip--ok">${UI.icon('status-ok')}Committed · settings match</span>`;
      cls = 'is-matching';
    }

    const meta = [
      row.fixtureType ? esc(row.fixtureType) : '',
      // Universe formatted here and only here, from the raw number.
      `${esc(host.getUniverseLabel())} ${esc(uni(row.universe))}`,
      `addr ${row.startAddress || '—'}`,
      row.footprint ? `${row.footprint} ch` : '',
      row.position ? esc(row.position) : '',
    ].filter(Boolean).join(' · ');

    let actions = '';
    if (row.committed) {
      actions = `
        <button class="b5-btn" data-rcb-reread="${esc(row.entryId)}" ${busy ? 'disabled' : ''}>${UI.icon('refresh')}Re-read fixture</button>
        <button class="b5-btn b5-btn--ghost" data-rcb-decommit="${esc(row.entryId)}" ${busy ? 'disabled' : ''}>Decommit</button>
        <button class="b5-btn" data-rcb-toggle="${esc(row.entryId)}" aria-expanded="${open}">${UI.icon(open ? 'chevron-collapse' : 'chevron-expand')}${open ? 'Hide' : 'Show'} settings</button>`;
    } else if (armedDeviceUid) {
      actions = `<button class="b5-btn b5-btn--primary" data-rcb-commit-entry="${esc(row.entryId)}" ${busy ? 'disabled' : ''}>Commit this entry to the picked fixture</button>`;
    } else {
      actions = `<button class="b5-btn ${armed ? 'b5-btn--primary' : ''}" data-rcb-arm-entry="${esc(row.entryId)}" ${busy ? 'disabled' : ''}>${armed ? 'Picking a fixture…' : 'Commit…'}</button>`;
    }

    return `
      <article class="b5-rcb-card ${cls} ${armed ? 'is-armed' : ''}">
        <div class="b5-rcb-card__top">
          ${link ? `<span class="b5-rcb-link" title="committed pair">${link}</span>` : `<span class="b5-rcb-link b5-rcb-link--none" aria-hidden="true">·</span>`}
          <div class="b5-rcb-card__id">
            <strong class="b5-rcb-card__name">${esc(entryLabel(row))}</strong>
            <span class="b5-rcb-card__meta">${meta}</span>
          </div>
        </div>
        <div class="b5-rcb-card__state">${stateChip}${row.committed ? `<span class="b5-text-mono b5-rcb-uid">${esc(row.committedUid)}</span>` : ''}</div>
        ${row.committed && row.asFoundAt ? `<p class="b5-rcb-readat">Settings last read ${esc(when(row.asFoundAt))}</p>` : ''}
        ${renderProposal(row, proposal)}
        <div class="b5-rcb-card__actions">${actions}</div>
        ${open && row.committed ? renderDiff(row) : ''}
      </article>`;
  }

  // renderProposal: the matcher's suggestion, offered and never applied
  // (rule 3). An ambiguous proposal lists every candidate WITH ITS EVIDENCE,
  // because that is the case where picking wrong means configuring the wrong
  // light — the evidence is the whole reason the row exists rather than the
  // app just choosing.
  function renderProposal(row, proposal) {
    if (row.committed || !proposal || !proposal.candidates || !proposal.candidates.length) return '';
    const ambiguous = proposal.status === 'ambiguous' || proposal.candidates.length > 1;
    const head = ambiguous
      ? `<p class="b5-rcb-suggest__head">${UI.icon('status-warning')}<strong>${proposal.candidates.length} fixtures match this entry about equally well.</strong> Nothing has been committed — pick one, and check the evidence first.</p>`
      : `<p class="b5-rcb-suggest__head">Suggested match — nothing is committed until you say so.</p>`;
    const cands = proposal.candidates.map(c => `
      <div class="b5-rcb-cand">
        <div class="b5-rcb-cand__top">
          <span class="b5-text-mono">${esc(c.deviceUid)}</span>
          <span class="b5-rcb-cand__conf">${Math.round((c.confidence || 0) * 100)}% confidence</span>
        </div>
        <ul class="b5-rcb-why">
          ${(c.evidence || []).length
            ? (c.evidence || []).map(ev => `<li><span class="b5-rcb-why__kind">${esc(ev.kind)}</span> ${esc(ev.detail)}</li>`).join('')
            : '<li>No field-level evidence — this candidate scored only just above the proposal threshold.</li>'}
        </ul>
        <div class="b5-rcb-cand__actions">
          <button class="b5-btn b5-btn--primary" data-rcb-accept="${esc(row.entryId)}" data-rcb-accept-uid="${esc(c.deviceUid)}" ${busy ? 'disabled' : ''}>Commit this one</button>
          <button class="b5-btn" data-rcb-identify="${esc(c.deviceUid)}" ${busy ? 'disabled' : ''}>${UI.icon('identify')}Identify</button>
        </div>
      </div>`).join('');
    return `<div class="b5-rcb-suggest">${head}${cands}
      <button class="b5-btn b5-btn--ghost" data-rcb-reject="${esc(row.entryId)}" ${busy ? 'disabled' : ''}>None of these</button>
    </div>`;
  }

  // --- the diff -------------------------------------------------------------

  const STATE_WORD = {
    match: 'Matches',
    differs: 'Differs',
    intended_unset: 'Patch has no intention',
    unread: 'Not read',
  };

  // valueText: renders one side of one diff line. The universe branch is the
  // reason this function exists as a single choke point (rule 4).
  function valueText(line, side) {
    const known = side === 'intended' ? line.intendedKnown : line.foundKnown;
    const label = side === 'intended' ? line.intendedLabel : line.foundLabel;
    if (!known) {
      // An intended mode NAME with no committable index still reads better
      // than a bare dash — it tells him what the patch believes even though
      // there is no index to push.
      return label ? `<em>${esc(label)}</em> <span class="b5-rcb-note">(name only — no mode number to send)</span>` : '—';
    }
    switch (line.kind) {
      case 'universe':
        return esc(uni(side === 'intended' ? line.intendedNum : line.foundNum));
      case 'number':
        return esc(String(side === 'intended' ? line.intendedNum : line.foundNum));
      case 'index': {
        // "Mode 2 of 3" is legible; a bare "2" is not. The count is only
        // ever shown on the as-found side, because only the device knows
        // how many choices it actually has.
        const n = side === 'intended' ? line.intendedNum : line.foundNum;
        const count = (side === 'found' && line.foundCountKnown) ? ` of ${line.foundCount}` : '';
        return label
          ? `${esc(label)} <span class="b5-rcb-note">(#${esc(String(n))}${esc(count)})</span>`
          : `#${esc(String(n))}<span class="b5-rcb-note">${esc(count)}</span>`;
      }
      case 'bool':
        return (side === 'intended' ? line.intendedBool : line.foundBool) ? 'On' : 'Off';
      default:
        return esc(String(side === 'intended' ? line.intendedText : line.foundText) || '(empty)');
    }
  }

  // renderDiff: one stacked BLOCK per setting, deliberately not a table.
  //
  // This started life as a five-column table (setting / intended / as found /
  // status / action) and render-proofing it in Chromium at 1024x768 killed
  // that outright: with the two panes side by side each card is about 430px
  // wide, so the table overflowed and the ACTION column — the "Apply to
  // fixture" button, the single most important control on this screen — sat
  // entirely outside the visible card, reachable only by finding and dragging
  // a nested horizontal scrollbar. A control that a tablet user cannot see is
  // a control that does not exist.
  //
  // Stacked blocks have no minimum width to overflow, so the Apply button is
  // always visible and always a full-size touch target, and the intended ->
  // found comparison reads as one sentence instead of two columns the eye has
  // to join up.
  function renderDiff(row) {
    const lines = row.diff || [];
    const body = lines.map(line => {
      const word = STATE_WORD[line.state] || line.state;
      let action = '';
      if (line.state === 'differs' && line.pushable) {
        action = `<button class="b5-btn b5-btn--primary" data-rcb-push="${esc(row.entryId)}" data-rcb-push-field="${esc(line.field)}" ${busy ? 'disabled' : ''}>${UI.icon('apply')}Apply to fixture</button>`;
      } else if (line.state === 'differs') {
        action = `<span class="b5-rcb-note">Not settable over RDM — this one is fixed at the node or on the DMX line.</span>`;
      } else if (line.state === 'intended_unset') {
        action = `<button class="b5-btn" data-rcb-adopt="${esc(row.entryId)}" data-rcb-adopt-field="${esc(line.field)}" ${busy ? 'disabled' : ''}>Adopt as intended</button>`;
      } else if (line.state === 'unread' && line.foundErr) {
        action = `<span class="b5-rcb-note">${esc(line.foundErr)}</span>`;
      }
      // The as-found timestamp sits on the field that carries it, because
      // per-field read times genuinely differ — a curve that NACKed on
      // commit and resolved on a later re-read was read at a different
      // moment from the address beside it.
      const at = (line.foundKnown && line.foundAt) ? when(line.foundAt) : '';
      return `
        <div class="b5-rcb-line is-${esc(line.state)}">
          <div class="b5-rcb-line__head">
            <span class="b5-rcb-line__label">${esc(line.label)}</span>
            <span class="b5-rcb-state b5-rcb-state--${esc(line.state)}">${esc(word)}</span>
          </div>
          <dl class="b5-rcb-line__vals">
            <dt>Intended</dt><dd>${valueText(line, 'intended')}</dd>
            <dt>As found</dt><dd>${valueText(line, 'found')}${at ? ` <span class="b5-rcb-note">· read ${esc(at)}</span>` : ''}</dd>
          </dl>
          ${action ? `<div class="b5-rcb-line__action">${action}</div>` : ''}
        </div>`;
    }).join('');

    const adoptable = lines.filter(l => l.state === 'intended_unset').map(l => l.field);
    return `
      <div class="b5-rcb-diff">
        <p class="b5-rcb-diff__lead">
          <strong>Intended</strong> is what this patch entry says. <strong>As found</strong> is what the fixture reported when it was last read.
          Nothing is written to the fixture until you press Apply on that setting.
        </p>
        ${body}
        ${adoptable.length ? `<button class="b5-btn b5-rcb-adoptall" data-rcb-adopt="${esc(row.entryId)}" data-rcb-adopt-field="${esc(adoptable.join(','))}" ${busy ? 'disabled' : ''}>Adopt all ${adoptable.length} as intended (patch only — no fixture is touched)</button>` : ''}
      </div>`;
  }

  // --- right pane: detected on the RDM line ---------------------------------

  function renderDetectedPane(links) {
    let rows = board.detected || [];
    rows = rows.filter(d => matchesFilter([d.uid, d.manufacturer, d.model, d.label, d.committedToName].join(' ')));
    if (needsAttentionOnly) rows = rows.filter(d => !d.committedToEntryId);
    if (!rows.length) return emptyNote(board.detected.length ? 'Nothing in this pane matches the current filter.' : 'No RDM devices have been discovered yet. Run a discovery from the Devices screen.');
    return rows.map(d => renderDetectedRow(d, links)).join('');
  }

  function renderDetectedRow(dev, links) {
    const link = links[dev.uid];
    const armed = armedDeviceUid === dev.uid;
    const claimed = !!dev.committedToEntryId;

    const meta = [
      `${esc(host.getUniverseLabel())} ${esc(uni(dev.universe))}`,
      // "unknown" and 0 are different facts about an address, and this pane
      // must not print a 0 the device never actually reported — the
      // addressKnown companion exists precisely so it does not have to
      // guess. Same policy Rig Walk uses.
      dev.addressKnown ? `addr ${dev.startAddress}` : 'addr unknown',
      dev.footprintKnown ? `${dev.footprint} ch` : '',
    ].filter(Boolean).join(' · ');

    let actions = '';
    if (claimed) {
      actions = `<span class="b5-rcb-note">Committed to <strong>${esc(dev.committedToName)}</strong> — decommit it on the left to free this fixture.</span>`;
    } else if (armedEntryId) {
      actions = `<button class="b5-btn b5-btn--primary" data-rcb-commit-device="${esc(dev.uid)}" ${busy ? 'disabled' : ''}>Commit the picked entry to this fixture</button>
        <button class="b5-btn" data-rcb-identify="${esc(dev.uid)}" ${busy ? 'disabled' : ''}>${UI.icon('identify')}Identify</button>`;
    } else {
      actions = `<button class="b5-btn ${armed ? 'b5-btn--primary' : ''}" data-rcb-arm-device="${esc(dev.uid)}" ${busy ? 'disabled' : ''}>${armed ? 'Picking an entry…' : 'Commit…'}</button>
        <button class="b5-btn" data-rcb-identify="${esc(dev.uid)}" ${busy ? 'disabled' : ''}>${UI.icon('identify')}Identify</button>`;
    }

    return `
      <article class="b5-rcb-card ${claimed ? 'is-matching' : 'is-uncommitted'} ${armed ? 'is-armed' : ''}">
        <div class="b5-rcb-card__top">
          ${link ? `<span class="b5-rcb-link" title="committed pair">${link}</span>` : `<span class="b5-rcb-link b5-rcb-link--none" aria-hidden="true">·</span>`}
          <div class="b5-rcb-card__id">
            <strong class="b5-rcb-card__name">${esc(deviceLabel(dev))}</strong>
            <span class="b5-rcb-card__meta">${meta}</span>
          </div>
        </div>
        <div class="b5-rcb-card__state">
          ${claimed
            ? `<span class="b5-rcb-chip b5-rcb-chip--ok">${UI.icon('status-ok')}Committed</span>`
            : `<span class="b5-rcb-chip b5-rcb-chip--open">Not committed</span>`}
          <span class="b5-text-mono b5-rcb-uid">${esc(dev.uid)}</span>
        </div>
        <div class="b5-rcb-card__actions">${actions}</div>
      </article>`;
  }

  function emptyNote(text) {
    return `<p class="b5-rcb-empty">${esc(text)}</p>`;
  }

  // --- actions --------------------------------------------------------------

  // run: every mutation goes through here, so busy-state, error reporting
  // and the mandatory server refresh can never be forgotten at a call site
  // (rule 1 — nothing is painted from a local guess).
  async function run(okMsg, fn) {
    if (busy) return;
    busy = true;
    render();
    try {
      await fn();
      host.setStatus(okMsg);
    } catch (e) {
      host.setStatus('error: ' + e.message);
    } finally {
      busy = false;
      await refresh();
      render();
    }
  }

  async function commit(entryId, deviceUid) {
    armedEntryId = null;
    armedDeviceUid = null;
    await run('committed', async () => {
      const res = await Api.reconcileCommit(entryId, deviceUid);
      openEntryId = entryId; // open the diff straight away: reading it is the point of committing
      if (res.readError) host.setStatus('committed, but: ' + res.readError);
    });
  }

  function wire() {
    const on = (sel, ev, fn) => el.querySelectorAll(sel).forEach(node => node.addEventListener(ev, fn));

    const refreshBtn = el.querySelector('#rcbRefresh');
    if (refreshBtn) refreshBtn.addEventListener('click', async () => { board = null; render(); await refresh(); render(); });

    const filter = el.querySelector('#rcbFilter');
    if (filter) filter.addEventListener('input', () => {
      filterText = filter.value;
      // Re-render only after the value is captured, then restore focus and
      // caret — the same rule the entries table follows so typing into a
      // filter never loses the cursor mid-word.
      const pos = filter.selectionStart;
      render();
      const again = el.querySelector('#rcbFilter');
      if (again) { again.focus(); try { again.setSelectionRange(pos, pos); } catch (_) { /* search inputs can refuse */ } }
    });

    const exJson = el.querySelector('#rcbExportJson');
    if (exJson) exJson.addEventListener('click', () => window.open(Api.patchReconcileExportUrl('json'), '_blank'));
    const exTxt = el.querySelector('#rcbExportTxt');
    if (exTxt) exTxt.addEventListener('click', () => window.open(Api.patchReconcileExportUrl('txt'), '_blank'));

    const att = el.querySelector('#rcbAttention');
    if (att) att.addEventListener('click', () => { needsAttentionOnly = !needsAttentionOnly; render(); });

    on('[data-rcb-disarm]', 'click', () => { armedEntryId = null; armedDeviceUid = null; render(); });
    on('[data-rcb-arm-entry]', 'click', (e) => { armedEntryId = e.currentTarget.dataset.rcbArmEntry; armedDeviceUid = null; render(); });
    on('[data-rcb-arm-device]', 'click', (e) => { armedDeviceUid = e.currentTarget.dataset.rcbArmDevice; armedEntryId = null; render(); });
    on('[data-rcb-toggle]', 'click', (e) => {
      const id = e.currentTarget.dataset.rcbToggle;
      openEntryId = openEntryId === id ? null : id;
      render();
    });

    on('[data-rcb-commit-device]', 'click', (e) => commit(armedEntryId, e.currentTarget.dataset.rcbCommitDevice));
    on('[data-rcb-commit-entry]', 'click', (e) => commit(e.currentTarget.dataset.rcbCommitEntry, armedDeviceUid));
    on('[data-rcb-accept]', 'click', (e) => commit(e.currentTarget.dataset.rcbAccept, e.currentTarget.dataset.rcbAcceptUid));

    // Decommit is destructive to the relation, so it is confirmed — and the
    // dialog says what SURVIVES, because "will I lose my settings?" is the
    // question that would otherwise stop him from substituting a fixture at
    // the one moment he needs to.
    on('[data-rcb-decommit]', 'click', async (e) => {
      const id = e.currentTarget.dataset.rcbDecommit;
      const row = (board.intended || []).find(r => r.entryId === id);
      const name = row ? entryLabel(row) : id;
      if (!confirm(`Decommit "${name}"?\n\nThis breaks the link to fixture ${row ? row.committedUid : ''} and clears what was read off it.\n\nThe entry's own intended settings are KEPT, so a replacement fixture committed here can be configured to match.\n\nNothing is sent to the fixture.`)) return;
      await run('decommitted — intended settings kept', async () => {
        await Api.reconcileDecommit(id);
        if (openEntryId === id) openEntryId = null;
      });
    });

    on('[data-rcb-reread]', 'click', (e) => {
      const id = e.currentTarget.dataset.rcbReread;
      openEntryId = id;
      return run('fixture re-read', () => Api.reconcileReread(id));
    });

    // The one place this screen writes to a fixture. One field, one press,
    // one confirm naming exactly the change (Apply-to-confirm, rule 2).
    on('[data-rcb-push]', 'click', async (e) => {
      const id = e.currentTarget.dataset.rcbPush;
      const field = e.currentTarget.dataset.rcbPushField;
      const row = (board.intended || []).find(r => r.entryId === id);
      const line = row && (row.diff || []).find(l => l.field === field);
      if (!row || !line) return;
      const from = stripTags(valueText(line, 'found'));
      const to = stripTags(valueText(line, 'intended'));
      if (!confirm(`Set "${line.label}" on fixture ${row.committedUid} from ${from} to ${to}?\n\nThis sends an RDM SET to that fixture now.`)) return;
      await run('applied to fixture', async () => {
        const res = await Api.reconcilePush(id, [field]);
        openEntryId = id;
        const bad = (res.results || []).filter(r => !r.ok);
        if (bad.length) throw new Error(bad.map(r => r.field + ': ' + r.error).join('; '));
      });
    });

    // Adopt is the opposite direction and is NOT confirmed: it writes only
    // to the patch, never to a light, so the cost of an accidental press is
    // an undo on the Entries tab rather than a misconfigured fixture.
    on('[data-rcb-adopt]', 'click', (e) => {
      const id = e.currentTarget.dataset.rcbAdopt;
      const fields = e.currentTarget.dataset.rcbAdoptField.split(',').filter(Boolean);
      openEntryId = id;
      return run('adopted into the patch — no fixture was touched', () => Api.reconcileAdoptIntended(id, fields));
    });

    on('[data-rcb-reject]', 'click', (e) => {
      const id = e.currentTarget.dataset.rcbReject;
      return run('suggestions dismissed', () => Api.reconcileReject(id));
    });

    on('[data-rcb-identify]', 'click', async (e) => {
      const uid = e.currentTarget.dataset.rcbIdentify;
      try {
        await Api.identify(uid, true);
        host.setStatus('identify on for ' + uid + ' (auto-off in 5s)');
        setTimeout(() => { Api.identify(uid, false).catch(() => {}); }, 5000);
      } catch (err) { host.setStatus('error: ' + err.message); }
    });
  }

  // stripTags: the confirm() dialogs are PLAIN TEXT, and valueText returns
  // markup. Rendering that markup into a native dialog would show the user
  // raw HTML at the exact moment they most need to read the sentence
  // carefully, so it is stripped here rather than valueText being duplicated
  // into a text-only twin that could drift out of step with it.
  function stripTags(html) {
    const d = document.createElement('div');
    d.innerHTML = html;
    return (d.textContent || '').replace(/\s+/g, ' ').trim();
  }

  return { init, attach, enter, leave, render, refresh };
})();
