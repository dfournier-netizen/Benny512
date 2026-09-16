// settings.js — Settings screen: NIC picker, poll interval, universe
// numbering base, the sACN output configuration, capture limit, RDM log
// path, and the danger-zone full reset. NIC list is populated from GET /api/nics
// (internal/transport.ListInterfaces); current.nic is matched against the
// real interface list by name, falling back to a synthesized option if the
// previously-saved NIC name isn't present on this machine (e.g.
// settings.json carried over from another host).
//
// CONVERTED TO THE SHARED SCREEN KIT (css/DESIGN.md). The markup is built
// here rather than in index.html — the same move analyzer.js made, and for
// the same reason: a screen cannot use the kit if its skeleton is frozen in
// a static file. Every id the old markup carried is unchanged.
//
// Three things about this screen make it different from the others:
//
//  1. IT IS THE SOURCE OF THE ART-NET STARTING UNIVERSE. UI.setArtnetStart is
//     what fires 'b5-universe-base-changed', and every other screen listens
//     for it to re-render its universe numbers (and, on Nodes and Send, to
//     rewrite a staged editor field from its canonical value). So this
//     screen must keep calling it on load AND on save — and it does both,
//     below, exactly where it did before.
//
//  2. APPLY-TO-CONFIRM IS SCREEN-WIDE, NOT PER-FIELD. Every control here
//     is oninput-mutates-the-DOM-only until "Apply settings" is pressed;
//     save() is the one and only place any setting takes effect. The kit's
//     per-field Apply (UI.buildApplyField) would be the wrong shape here —
//     these settings are saved as ONE document by POST /api/settings, so a
//     per-field Apply would send the other fields' half-typed values with
//     it. What is added instead is an honest UNSAVED marker: the step head
//     and the action bar both say, in a word, that there are staged changes
//     the server has not been told about.
//
//  3. THE sACN CONFIGURATION IS A SECOND DOCUMENT. Start universe, priority
//     and unicast override are persisted by their own endpoint
//     (GET/POST /api/sacn, internal/web/sacn.go) with their own validation,
//     so Apply sends two requests, not one. They are HERE rather than on the
//     Rig Check page because they are properties of the INSTALLATION — the
//     same three numbers every run — exactly like the Art-Net starting
//     universe above them. Which protocol a given run uses is the opposite,
//     a field on that run's start request, and lives on Rig Check beside the
//     Start button that carries it.
//
// The "Default timeout profile" control that used to sit in this screen's
// static markup has been replaced by a plain statement of fact. It was
// never read and never written: settings.js's save() has always sent
// `timeoutProfiles: current.timeoutProfiles` verbatim, and Settings.
// TimeoutProfiles (server.go) is a per-node-key map, not a global default,
// so the select could not have driven it even if something had read it. A
// control that does nothing is worse than no control at all in the dark —
// rule 3, name the unknown rather than showing a plausible one.
const SettingsScreen = (() => {
  let current = null;
  let nics = [];
  // sacnCurrent: the last SAVED sACN configuration from GET /api/sacn —
  // {startUniverse, priority, unicastTo} and nothing else. The E1.31
  // Component Identifier is deliberately not part of this shape in either
  // direction: it is generated and persisted server-side, a receiver tells
  // sources apart by it (ANSI E1.31-2025 Section 6.2.3), and the server's
  // decoder rejects a body that carries one.
  let sacnCurrent = null;

  // baseline: the last SAVED settings, as strings, keyed by element id. The
  // dirty marker compares live inputs against this — never against the
  // previous keystroke, so reverting a field by hand clears the marker.
  let baseline = {};

  const FIELD_IDS = ['nicSelect', 'pollInterval', 'captureLimit', 'logRdmPath', 'artnetStartUniverse',
    'sacnStartUniverse', 'sacnPriority', 'sacnUnicastTo'];

  // The bounds internal/sacn/settings.go enforces, restated here so the
  // inputs cannot offer a value the server will refuse. Priority's low end
  // is 1 rather than the standard's 0 on purpose — sacn.Sender's
  // Config.Priority is a byte whose zero means "use the default of 100", so
  // a stored 0 would come out of the socket as 100. See MinPriority's
  // comment in settings.go; the store refuses it and says why.
  const SACN_MIN_PRIORITY = 1;
  const SACN_MAX_PRIORITY = 200;
  const SACN_DEFAULT_PRIORITY = 100;

  async function refresh() {
    [current, nics, sacnCurrent] = await Promise.all([
      Api.getSettings(),
      Api.getNICs().catch(() => []),
      // Best-effort: an sACN store that cannot be read leaves the form on
      // its defaults rather than blanking the whole Settings screen.
      Api.getSACNConfig().catch(() => null),
    ]);
    render();
  }

  // --- markup ---------------------------------------------------------------

  function screenHtml() {
    return `
      <div class="b5-page-header">
        <h1 class="b5-page-header__title">Settings</h1>
        <span class="b5-page-header__meta b5-text-muted b5-text-sm">Nothing here takes effect until you press Apply.</span>
      </div>

      <section class="b5-step-section" aria-labelledby="setNetHead">
        <h2 class="b5-step-section__head" id="setNetHead">
          <span class="b5-step-num">1</span> Network
          <span class="b5-step-section__note" id="setNetNote">which card Benny512 talks Art-Net on</span>
        </h2>
        <div class="b5-group">
          <h3 class="b5-group__head">Interface</h3>
          <div class="b5-field">
            <label class="b5-field__label" for="nicSelect">Network interface (NIC)</label>
            <select id="nicSelect" class="b5-select"></select>
            <span class="b5-field__hint">If nodes never answer a poll, this is the first thing to check — it must be the card on the lighting network, not the one with the internet on it.</span>
            <span class="b5-field__hint"><strong>Takes effect when Benny512 restarts.</strong> The Art&nbsp;Net socket is opened once, against the card chosen at launch, and cannot be moved while the program is running &mdash; so saving a different card here changes what the next launch binds, not this one. Restart to use it. (A <code>--iface</code> given on the command line overrides this setting for that run, and if the saved card is missing on the next machine Benny512 picks one automatically and says so rather than refusing to start.)</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="pollInterval">Poll interval (ms)</label>
            <input type="number" id="pollInterval" class="b5-input b5-input--mono" min="500" step="500">
            <span class="b5-field__hint">How often an ArtPoll goes out. Lower finds a node that just came up sooner; higher is quieter on a busy show network.</span>
          </div>
        </div>
        <div class="b5-inset">
          <p class="b5-inset__head">${UI.icon('status-pending')}RDM timeout profile</p>
          <p class="b5-note">There is no global default to set here. A timeout profile (Direct or Wireless&nbsp;BiDi proxy) is held per node, keyed by that node&rsquo;s own address, and is chosen on the Nodes screen for the node you are working through. This screen used to show a &ldquo;Default timeout profile&rdquo; picker that was wired to nothing at all &mdash; it has been removed rather than left looking like it did something.</p>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setUniHead">
        <h2 class="b5-step-section__head" id="setUniHead">
          <span class="b5-step-num">2</span> Universe numbering
          <span class="b5-step-section__note" id="setUniNote"></span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="artnetStartUniverse">Art-Net starting universe</label>
          <input id="artnetStartUniverse" class="b5-input" type="number" min="0" max="32767" step="1">
          <p class="b5-caption">The Art-Net universe that this show&rsquo;s <strong>universe 1</strong> lives on.</p>
        </div>
        <div class="b5-inset">
          <p class="b5-inset__head">${UI.icon('apply')}What this changes, and what it never changes</p>
          <p class="b5-note" id="universeExample"></p>
          <p class="b5-note">Correlation only. Nothing sent on the wire changes, and nothing stored in the patch changes &mdash; every universe Benny512 saves or transmits stays the raw Art-Net Port-Address. This setting only decides what the show&rsquo;s own universes are <em>called</em>.</p>
          <p class="b5-note"><strong>Which screens show which number.</strong> <em>Nodes</em> and the <em>Analyzer</em> show the raw Art-Net universe &mdash; one flat number, 0&ndash;32767, matching what a gateway&rsquo;s faceplate shows; universe 17 is typed and read as 17, never split into Net / Sub-Net / Universe. <em>Patch</em>, <em>Rig Check</em>, <em>Function check</em>, <em>Rig Walk</em> and <em>Send</em> show the show&rsquo;s own numbering. <em>Devices</em> shows both, because that is where a physical port and a patched fixture meet.</p>
          <p class="b5-note">A node port on an Art-Net universe <em>below</em> the starting universe has no show number and is shown as its Art-Net universe, marked outside the show&rsquo;s range &mdash; never as a negative.</p>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setSacnHead">
        <h2 class="b5-step-section__head" id="setSacnHead">
          <span class="b5-step-num">3</span> sACN output
          <span class="b5-step-section__note" id="setSacnNote"></span>
        </h2>
        <p class="b5-caption">Whether a run goes out on Art-Net or sACN is chosen on the <strong>Rig Check</strong> screen, next to the Start button that carries it &mdash; it is a property of a run. What is set here is how this installation sources sACN when a run asks for it: the same three numbers every time.</p>
        <div class="b5-group">
          <h3 class="b5-group__head">Universes and priority</h3>
          <div class="b5-field">
            <label class="b5-field__label" for="sacnStartUniverse">sACN starting universe</label>
            <input id="sacnStartUniverse" class="b5-input b5-input--mono" type="number" min="1" max="63999" step="1">
            <span class="b5-field__hint">The sACN universe this show&rsquo;s <strong>universe 1</strong> is sourced as. sACN universes start at 1 &mdash; ANSI E1.31-2025 reserves universe 0, so there is no 0 to choose.</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="sacnPriority">Priority</label>
            <input id="sacnPriority" class="b5-input b5-input--mono" type="number" min="1" max="200" step="1">
            <span class="b5-field__hint">Stamped on every packet (E1.31 Section 6.2.3). 100 is the value a source without variable priority transmits, and is the default. Higher wins on a console that merges by priority. 0 is refused by this build and the server says why.</span>
          </div>
          <div class="b5-field">
            <label class="b5-field__label" for="sacnUnicastTo">Unicast override (optional)</label>
            <input id="sacnUnicastTo" class="b5-input b5-input--mono" type="text" autocomplete="off" spellcheck="false" placeholder="leave blank to multicast">
            <span class="b5-field__hint">Blank means multicast, which is what almost every rig wants. An IPv4 address here sends every universe to that one node instead &mdash; useful on a network where multicast is blocked or where one gateway is the only receiver.</span>
          </div>
        </div>
        <div class="b5-inset">
          <p class="b5-inset__head">${UI.icon('apply')}What this mapping actually produces</p>
          <p class="b5-note" id="sacnExample"></p>
          <p class="b5-note" id="sacnRefusal"></p>
          <p class="b5-note">Nothing stored is rewritten. Every universe Benny512 saves stays the raw Art-Net Port-Address; the sACN number is worked out at the output boundary, once per universe, when a run starts. A universe that lands outside 1&ndash;63999 is <strong>refused, never clamped</strong> &mdash; a clamp would light the wrong universe on a real rig and nothing would say so.</p>
          <p class="b5-note">The E1.31 Component Identifier that identifies this installation to receivers is generated once by the server and kept beside the executable. It is not shown or settable here on purpose: it exists to stay the same across restarts, and a source identity a browser could set is a source identity a browser could impersonate.</p>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setCapHead">
        <h2 class="b5-step-section__head" id="setCapHead">
          <span class="b5-step-num">4</span> Capture &amp; logging
          <span class="b5-step-section__note">what the Analyzer keeps, and what goes to disk</span>
        </h2>
        <div class="b5-field">
          <label class="b5-field__label" for="captureLimit">Capture limit (entries)</label>
          <input type="number" id="captureLimit" class="b5-input b5-input--mono" min="100" step="100">
          <span class="b5-field__hint">The in-memory ring the Analyzer reads from. Older packets are dropped once it is full.</span>
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="logRdmPath">Continuous RDM log file (optional)</label>
          <input type="text" id="logRdmPath" class="b5-input b5-input--mono" placeholder="e.g. C:\\Benny512\\rdm-log.txt">
          <span class="b5-field__hint">Appends every RDM/ToD exchange to this file as it happens, so a long bench session isn't limited by the in-memory buffer above. Blank means no file is written. Rotates automatically by size.</span>
        </div>
      </section>

      <section class="b5-step-section" aria-labelledby="setResetHead">
        <h2 class="b5-step-section__head" id="setResetHead">
          <span class="b5-step-num">5</span> Danger zone &mdash; full reset
          <span class="b5-step-section__note">deletes the patch and shuts Benny512 down</span>
        </h2>
        <div class="b5-stack" id="resetPanelRoot"></div>
      </section>

      <section class="b5-actionbar" aria-label="Apply settings">
        <div class="b5-actionbar__status">
          <span class="b5-actionbar__title">${UI.icon('apply')}Settings</span>
          <span id="settingsDirtyPill"></span>
          <span class="b5-text-muted b5-text-sm" id="settingsStatus"></span>
        </div>
        <div class="b5-actionbar__buttons">
          <button id="btnRevertSettings" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Discard changes</button>
          <button id="btnSaveSettings" class="b5-btn b5-btn--primary">${UI.icon('apply')}Apply settings</button>
        </div>
      </section>
    `;
  }

  function render() {
    if (!current) return;
    const nicSel = document.getElementById('nicSelect');
    const prevValue = nicSel.value || current.nic;
    nicSel.innerHTML = '';
    const blank = document.createElement('option');
    blank.value = ''; blank.textContent = '(default / any)';
    nicSel.appendChild(blank);
    nics.forEach(nic => {
      const opt = document.createElement('option');
      opt.value = nic.name;
      const addrs = (nic.ipv4 || []).join(', ');
      opt.textContent = `${nic.displayName || nic.name}${addrs ? ' — ' + addrs : ''}${nic.up ? '' : ' (down)'}`;
      nicSel.appendChild(opt);
    });
    if (current.nic && ![...nicSel.options].some(o => o.value === current.nic)) {
      const opt = document.createElement('option');
      opt.value = current.nic;
      opt.textContent = current.nic + ' (not present on this machine)';
      nicSel.appendChild(opt);
    }
    if (!nics.length) {
      const opt = document.createElement('option');
      opt.value = '';
      // Rule 3: an empty picker must say WHY it is empty, not sit there
      // looking like the machine has no network cards.
      opt.textContent = 'no interfaces reported by this machine';
      nicSel.appendChild(opt);
    }
    nicSel.value = prevValue || current.nic || '';
    document.getElementById('pollInterval').value = current.pollIntervalMs || 3000;
    document.getElementById('captureLimit').value = current.captureLimit || 10000;
    document.getElementById('logRdmPath').value = current.logRdmPath || '';
    const start = Number.isFinite(current.artnetStartUniverse) ? current.artnetStartUniverse : 0;
    const startInput = document.getElementById('artnetStartUniverse');
    if (startInput) startInput.value = String(start);
    UI.setArtnetStart(start);

    const sacn = sacnCurrent || { startUniverse: 1, priority: SACN_DEFAULT_PRIORITY, unicastTo: '' };
    const sacnStartInput = document.getElementById('sacnStartUniverse');
    if (sacnStartInput) sacnStartInput.value = String(sacn.startUniverse || 1);
    const sacnPriorityInput = document.getElementById('sacnPriority');
    if (sacnPriorityInput) sacnPriorityInput.value = String(sacn.priority || SACN_DEFAULT_PRIORITY);
    const sacnUnicastInput = document.getElementById('sacnUnicastTo');
    if (sacnUnicastInput) sacnUnicastInput.value = sacn.unicastTo || '';
    UI.setSacnStart(sacn.startUniverse || 1);

    captureBaseline();
    renderDirty();
    renderUniverseNotes();
    renderSacnNotes();
  }

  // renderUniverseNotes: the active correlation stated as a WORKED EXAMPLE
  // rather than as a number, because a starting universe is exactly the kind
  // of off-by-one that looks right at arm's length and is wrong on the
  // truss. It names a real pair in both directions.
  //
  // Composed from the PENDING value explicitly, not through UI.formatUser's
  // ambient one — the sentence has to describe the choice currently in the
  // box, including before it is applied.
  function renderUniverseNotes(pendingStart) {
    const input = document.getElementById('artnetStartUniverse');
    let start = pendingStart;
    if (start === undefined) {
      const typed = input ? parseInt(input.value, 10) : NaN;
      start = Number.isFinite(typed) ? typed : UI.getArtnetStart();
    }
    const valid = Number.isFinite(start) && start >= 0 && start <= 32767;
    const applied = valid && start === UI.getArtnetStart();

    const note = document.getElementById('setUniNote');
    if (note) {
      note.textContent = !valid
        ? 'enter an Art-Net universe between 0 and 32767'
        : `show universe 1 = Art-Net ${start}` + (applied ? '' : ' — not applied yet');
    }

    const ex = document.getElementById('universeExample');
    if (!ex) return;
    if (!valid) {
      ex.textContent = 'An Art-Net starting universe must be a Port-Address between 0 and 32767.';
      return;
    }
    // Two concrete rows, one in each direction, because the mistake this
    // setting invites is reading the correlation backwards.
    ex.textContent =
      `With this setting: Patch universe 1 goes out on Art-Net universe ${start}, ` +
      `and Patch universe 5 goes out on Art-Net universe ${start + 4}. ` +
      `A node port reported on Art-Net universe ${start + 16} is Patch universe 17. ` +
      (applied
        ? 'This is the numbering in use right now.'
        : 'This is not in use yet — press Apply settings below to switch every screen over.');
  }

  // renderSacnNotes: the same worked-example discipline renderUniverseNotes
  // uses, for the same reason — a starting universe is exactly the kind of
  // off-by-one that looks right at arm's length and is wrong on the truss.
  //
  // It is composed from the value CURRENTLY IN THE BOX, applied or not, and
  // every number goes through UI.formatSacn / UI.sacnMulticastAddress rather
  // than being worked out here. The refusal line is the one that matters:
  // Art-Net Port-Address 0 is legal and is this app's default show universe
  // 1, and sACN has no universe 0, so the combination of an Art-Net starting
  // universe above 0 and a low sACN start produces universes that cannot be
  // expressed at all. The server answers 422 naming them; this says so
  // first.
  function renderSacnNotes() {
    const startInput = document.getElementById('sacnStartUniverse');
    const priorityInput = document.getElementById('sacnPriority');
    const unicastInput = document.getElementById('sacnUnicastTo');
    const ex = document.getElementById('sacnExample');
    const refusal = document.getElementById('sacnRefusal');
    const note = document.getElementById('setSacnNote');
    if (!startInput || !ex) return;

    const typedStart = parseInt(startInput.value, 10);
    const typedPriority = priorityInput ? parseInt(priorityInput.value, 10) : NaN;
    const unicast = unicastInput ? unicastInput.value.trim() : '';
    const startValid = Number.isFinite(typedStart) && typedStart >= UI.SACN_MIN_UNIVERSE && typedStart <= UI.SACN_MAX_UNIVERSE;
    const priorityValid = Number.isFinite(typedPriority) && typedPriority >= SACN_MIN_PRIORITY && typedPriority <= SACN_MAX_PRIORITY;
    const applied = startValid && sacnCurrent && typedStart === sacnCurrent.startUniverse;

    if (note) {
      note.textContent = !startValid
        ? `enter an sACN universe between ${UI.SACN_MIN_UNIVERSE} and ${UI.SACN_MAX_UNIVERSE}`
        : !priorityValid
          ? `enter a priority between ${SACN_MIN_PRIORITY} and ${SACN_MAX_PRIORITY}`
          : `show universe 1 = sACN universe ${typedStart}` + (applied ? '' : ' — not applied yet');
    }

    if (!startValid) {
      ex.textContent = `An sACN starting universe must be between ${UI.SACN_MIN_UNIVERSE} and ${UI.SACN_MAX_UNIVERSE}. There is no sACN universe 0 to fall back to.`;
      if (refusal) refusal.textContent = '';
      return;
    }

    // Two concrete rows in the same direction the operator reads them, with
    // the destination named — multicast group or the unicast override — so
    // "where does this actually go" is answered on the screen that sets it.
    const artnetStart = UI.getArtnetStart();
    const opts = { artnetStart, sacnStart: typedStart };
    const rawOne = UI.userToArtnet(1);
    const rawFive = UI.userToArtnet(5);
    const oneSacn = UI.artnetToSacn(rawOne, opts);
    const fiveSacn = UI.artnetToSacn(rawFive, opts);
    const where = u => (unicast
      ? `unicast to ${unicast}`
      : `multicast to ${UI.sacnMulticastAddress(u)}`);
    ex.textContent =
      `With this setting: show universe 1 (Art-Net Port-Address ${rawOne}) is sourced as sACN universe ` +
      `${UI.formatSacn(rawOne, opts)}${oneSacn === null ? '' : `, ${where(oneSacn)}`}. ` +
      `Show universe 5 (Art-Net Port-Address ${rawFive}) is sACN universe ` +
      `${UI.formatSacn(rawFive, opts)}${fiveSacn === null ? '' : `, ${where(fiveSacn)}`}. ` +
      (unicast
        ? 'Every universe goes to that one address; no multicast group is joined.'
        : 'Multicast groups follow ANSI E1.31-2025 Table 9-10 — 239.255, then the universe high byte, then its low byte.') +
      (applied ? ' This is the configuration in use right now.' : ' This is not saved yet — press Apply settings below.');

    // The refusal line always says something TRUE, because the case it warns
    // about cannot be detected from this screen: show universes 1 and 5
    // always map to a legal sACN universe when the start is >= 1. What
    // cannot is a fixture patched BELOW the Art-Net starting universe —
    // outside this show's own range — and Settings does not hold the patch.
    // A line that only appears when this screen can prove a problem would
    // therefore never appear at all, which is worse than no line: it would
    // read as "checked, nothing wrong". So it states the rule and points at
    // the screen that can check it against real fixtures.
    if (refusal) {
      refusal.textContent = artnetStart > 0
        ? `A fixture patched on an Art-Net Port-Address below ${artnetStart} is outside this show's range and has no sACN universe at all — sACN universes start at 1, and this mapping would put it at or below 0. Rig Check refuses to start on sACN while one is in scope and names it; the Rig Check screen lists this show's actual universes and what each becomes.`
        : `With an Art-Net starting universe of 0, every universe this show can patch maps to a legal sACN universe at this start. The Rig Check screen lists this show's actual universes and what each becomes.`;
    }
  }

  // sacnPayload: the ONE place the POST /api/sacn body is built. Exactly the
  // three keys internal/web's sacnConfigJSON names — the server's decoder
  // sets DisallowUnknownFields, so an extra or renamed key is a flat 400
  // rather than a silent no-op, and internal/web/sacn_ui_test.go pins these
  // keys against that struct's tags in both directions.
  function sacnPayload() {
    return {
      startUniverse: sacnIntFrom('sacnStartUniverse', UI.SACN_MIN_UNIVERSE, UI.SACN_MAX_UNIVERSE, 1),
      priority: sacnIntFrom('sacnPriority', SACN_MIN_PRIORITY, SACN_MAX_PRIORITY, SACN_DEFAULT_PRIORITY),
      unicastTo: (document.getElementById('sacnUnicastTo') || { value: '' }).value.trim(),
    };
  }

  // sacnIntFrom reads one bounded integer field, falling back to the
  // documented default rather than sending NaN. Out-of-range values are
  // still sent as typed where they parse, so the server's own message — the
  // one that explains WHY 0 is refused — is what the operator reads.
  function sacnIntFrom(id, min, max, fallback) {
    const el = document.getElementById(id);
    const n = el ? parseInt(el.value, 10) : NaN;
    if (!Number.isFinite(n)) return fallback;
    return n;
  }

  // --- dirty tracking (staged, never sent) ---------------------------------

  function captureBaseline() {
    baseline = {};
    FIELD_IDS.forEach(id => {
      const el = document.getElementById(id);
      if (el) baseline[id] = String(el.value);
    });
  }

  function dirtyFields() {
    return FIELD_IDS.filter(id => {
      const el = document.getElementById(id);
      return el && String(el.value) !== baseline[id];
    });
  }

  function renderDirty() {
    const pill = document.getElementById('settingsDirtyPill');
    const revert = document.getElementById('btnRevertSettings');
    if (!pill) return;
    const n = dirtyFields().length;
    if (n) {
      pill.innerHTML = `<span class="b5-pill b5-pill--md b5-pill--warn">${UI.icon('status-warning')}${n} unsaved change${n === 1 ? '' : 's'}</span>`;
    } else {
      pill.innerHTML = `<span class="b5-pill b5-pill--md b5-pill--ok">${UI.icon('status-ok')}Saved</span>`;
    }
    if (revert) revert.disabled = n === 0;
  }

  // save() is the one and only place any setting takes effect — every field
  // above is oninput/onchange-mutates-the-DOM-only until "Apply settings" is
  // clicked (task rule: nothing commits without explicit confirmation).
  async function save() {
    const status = document.getElementById('settingsStatus');
    const payload = {
      nic: document.getElementById('nicSelect').value,
      pollIntervalMs: parseInt(document.getElementById('pollInterval').value, 10) || 3000,
      captureLimit: parseInt(document.getElementById('captureLimit').value, 10) || 10000,
      timeoutProfiles: (current && current.timeoutProfiles) || {},
      logRdmPath: document.getElementById('logRdmPath').value.trim(),
      artnetStartUniverse: (() => {
        const n = parseInt(document.getElementById('artnetStartUniverse').value, 10);
        return Number.isFinite(n) && n >= 0 && n <= 32767 ? n : 0;
      })(),
    };
    try {
      await Api.postSettings(payload);
      current = payload;
      // The one line the whole app hangs off: this fires
      // 'b5-universe-base-changed', which every other screen listens for.
      UI.setArtnetStart(payload.artnetStartUniverse);
      // The sACN configuration is a SEPARATE document with its own endpoint
      // and its own validation (POST /api/sacn), so it is a second call, not
      // a field on the settings body. It is sent after the settings save
      // succeeds and its errors are reported in the server's own words —
      // the priority-0 refusal in particular explains itself and would be
      // worthless paraphrased.
      sacnCurrent = await Api.postSACNConfig(sacnPayload());
      UI.setSacnStart(sacnCurrent.startUniverse);
      status.innerHTML = UI.icon('status-ok') + 'applied';
      captureBaseline();
      renderDirty();
      renderUniverseNotes();
      renderSacnNotes();
    } catch (e) {
      status.innerHTML = UI.icon('status-error') + ('error: ' + e.message);
      // A half-applied save must not leave the form claiming to be clean.
      renderDirty();
      renderSacnNotes();
    }
  }

  // --- Danger zone: full reset (POST /api/reset) --------------------------
  // Built once (not part of the settings-form render() above) — the RESET
  // text field is a controlled-by-attribute, not controlled-by-state,
  // input: oninput mutates only the confirm button's disabled attribute
  // directly, never re-renders this subtree, so the field never loses
  // focus/cursor position while Dom is mid-typing (the standing
  // oninput-mutates-state-only / re-render-on-onchange rule, applied here
  // as "don't re-render at all for a plain enable/disable toggle").
  // Gating on the typed word is the deliberate confirmation Dom asked for,
  // on top of (not instead of) the ordinary Apply-to-confirm contract —
  // there's no separate Apply step because the disabled-until-exact-match
  // button IS that step.
  function buildResetPanel() {
    const root = document.getElementById('resetPanelRoot');
    if (!root) return;
    root.innerHTML = `
      <div class="b5-alert b5-alert--danger">
        ${UI.icon('status-warning')}
        <div>
          <p class="b5-alert__title">Erase ALL saved shows and shut down</p>
          <p class="b5-alert__body">
            Full reset permanently deletes ALL saved shows, their recovery copies, and the Rig Walk session &mdash;
            if your patch is hand-built, export it first (Patch tab &rarr; Export) or it is gone for good.
            It also clears every discovered node and device, empties the Art-Net/RDM capture buffers, closes
            any RDM log file, and resets these Settings to their defaults. Benny512 then shuts down and has
            to be relaunched &mdash; this is not the same as the Devices tab's "Clear discovered devices",
            which keeps all of that.
          </p>
        </div>
      </div>
      <div class="b5-field">
        <label class="b5-field__label" for="resetConfirmInput">Type RESET to confirm</label>
        <input id="resetConfirmInput" class="b5-input b5-input--mono" type="text" autocomplete="off" spellcheck="false" placeholder="RESET">
        <span class="b5-field__hint">The button below stays disabled until this box says exactly RESET. That typed word IS the confirmation &mdash; there is no second dialog.</span>
      </div>
      <div class="b5-row">
        <button id="btnFullReset" class="b5-btn b5-btn--danger" disabled>${UI.icon('status-warning')}Reset and shut down</button>
        <span class="b5-text-muted b5-text-sm" id="resetStatus"></span>
      </div>
    `;
    const input = document.getElementById('resetConfirmInput');
    const btn = document.getElementById('btnFullReset');
    input.addEventListener('input', () => { btn.disabled = input.value !== 'RESET'; });
    btn.addEventListener('click', doFullReset);
  }

  async function doFullReset() {
    const btn = document.getElementById('btnFullReset');
    const input = document.getElementById('resetConfirmInput');
    const status = document.getElementById('resetStatus');
    if (!btn || !input || input.value !== 'RESET') return;
    btn.disabled = true;
    input.disabled = true;
    status.innerHTML = UI.spinner() + 'Resetting…';
    try {
      const res = await Api.fullReset();
      // The WebSocket is about to drop as the process exits (by design) —
      // stop the normal reconnect-with-backoff churn before it ever fires,
      // then replace the screen with a clear terminal end state instead of
      // leaving "Reconnecting…" spinning forever against a dead server.
      Live.enterShutdown();
      showResetTerminalState(res);
    } catch (e) {
      status.innerHTML = UI.icon('status-error') + ('error: ' + e.message);
      btn.disabled = input.value !== 'RESET';
      input.disabled = false;
    }
  }

  // showResetTerminalState renders a modal (.b5-dialog — the design
  // system's "used sparingly" overlay, exactly the sparing/high-stakes case
  // it's for) stating plainly what happened: every deleted path, any
  // deletion errors surfaced rather than swallowed (task ask: "a file that
  // couldn't be deleted matters"), and that the process has exited and
  // needs relaunching. No close button — there is nothing left running to
  // return to.
  function showResetTerminalState(res) {
    const deleted = (res && res.deleted) || [];
    const errors = (res && res.errors) || [];
    const overlay = document.createElement('div');
    overlay.className = 'b5-dialog-backdrop';
    overlay.id = 'resetTerminalOverlay';
    overlay.innerHTML = `
      <div class="b5-dialog" role="alertdialog" aria-modal="true" aria-labelledby="resetTerminalTitle">
        <h2 class="b5-dialog__title" id="resetTerminalTitle">${UI.icon('status-ok')}Benny512 has shut down</h2>
        <div class="b5-dialog__body">
          <p>Full reset completed.${deleted.length ? ' Deleted: ' + deleted.map(escapeHtml).join(', ') + '.' : ' Nothing needed deleting on disk (no patch/walk session file was on disk to begin with).'}</p>
          ${errors.length ? `
            <div class="b5-alert b5-alert--danger" style="margin-top:var(--b5-space-3)">
              ${UI.icon('status-error')}
              <div><p class="b5-alert__title">Some files could not be deleted</p><p class="b5-alert__body">${errors.map(escapeHtml).join('<br>')}</p></div>
            </div>` : ''}
          <p style="margin-top:var(--b5-space-3)">The application has exited. Relaunch Benny512 to continue.</p>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);
  }

  function init() {
    const screen = document.getElementById('screen-settings');
    if (screen) screen.innerHTML = screenHtml();
    document.getElementById('btnSaveSettings').addEventListener('click', save);
    document.getElementById('btnRevertSettings').addEventListener('click', () => {
      // Discard is local only: it repaints the form from `current`, the
      // last thing the SERVER told us, and sends nothing.
      render();
      const status = document.getElementById('settingsStatus');
      if (status) status.textContent = 'changes discarded';
    });
    // Staged edits only: typing never sends. The dirty marker and the
    // universe worked-example are the only things that move.
    FIELD_IDS.forEach(id => {
      const el = document.getElementById(id);
      if (!el) return;
      const restate = () => {
        renderDirty();
        // The Art-Net starting universe moves BOTH worked examples: the sACN
        // mapping is composed on top of it.
        if (id === 'artnetStartUniverse') { renderUniverseNotes(); renderSacnNotes(); }
        if (id.indexOf('sacn') === 0) renderSacnNotes();
      };
      el.addEventListener('input', restate);
      el.addEventListener('change', restate);
    });
    buildResetPanel();
    // Baseline the empty form before the first paint, so the dirty marker
    // never flashes "5 unsaved changes" in the gap before GET /api/settings
    // answers. render() re-baselines against the real values.
    captureBaseline();
    renderDirty();
    renderUniverseNotes();
    renderSacnNotes();
    refresh();
  }

  return { init };
})();
