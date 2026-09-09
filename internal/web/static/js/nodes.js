// nodes.js — Nodes screen: device-grouped accordion + detail pane +
// editable node/network configuration (Phase 1c+: ArtAddress/ArtInput/
// ArtIpProg via internal/web/node_config.go).
//
// Device grouping + sorting: owner's original ask — "let's start grouping
// ports on the same device into a drop down or some other logical block.
// e.g. our EN4 has 4 ports, so link them all together. Maybe even make our
// inspector pane select per device, with the ability to pick which port is
// targeted" — first landed on the Devices screen, then the owner clarified
// it belongs here instead ("apply that same sorting and feature set... to
// the nodes tab, not devices... revert the changes to the device tab"). See
// deviceGroups() below for the IP-grouping rationale (shared with
// devices.js's now-reverted picker) and compareIPNumeric() for the
// numeric-vs-lexicographic sort fix.
//
// ===========================================================================
// UNIVERSE NOTATION (this round — bench defect, EN4 on the owner's rig)
// ===========================================================================
// Reported: "In the Nodes tab, the universe values in Benny512 are
// displaying as 1 less than what the faceplate says." Two separate defects
// were behind that one symptom, and this file was the ONLY screen in the app
// that called neither universe formatter nor parser (25 universe
// references, zero conversions):
//
//  (a) DISPLAY SHOWED A NIBBLE, NOT A PORT-ADDRESS. Both the accordion's
//      port rows and the inspector's static info table rendered
//      `outputAddress & 0x0F` — the Art-Net *Universe nibble* only, not the
//      full 15-bit Port-Address (Net<<8 | Sub-Net<<4 | Universe) that
//      server.go's nodePortJSON actually sends (artnet.PortAddress.RawValue,
//      internal/artnet/packet.go) and that every other screen displays. On a
//      Net 0 / Sub-Net 0 rig the nibble happens to equal the Port-Address,
//      so the only symptom visible on the bench was the missing 1-based
//      offset — but a port at Sub-Net 1 read `0` here and `17` on Patch: the
//      same port, two numbers. Fixed by formatting the raw Port-Address the
//      server sent, through UI.formatArtnet, exactly once (DESIGN.md
//      rule 5).
//
//  (b) THE EDITOR EDITED RAW ART-NET FIELDS. The config section exposed
//      Net (0-127), Sub-Net (0-15) and a per-port 0-15 Universe nibble as
//      three separate raw wire fields. Owner's decision, already made:
//      ONE universe field per port, in his display base, so a `1` on the
//      EN4 faceplate reads `1` here. Benny512 derives Net and Sub-Net at
//      send time (deriveAddressing below).
//
// CANONICAL-VALUE RULE. configState holds the canonical 0-based
// Port-Address, never a displayed string.
//
// NODES IS A PROTOCOL SCREEN: it shows the RAW Art-Net universe, as one
// flat 15-bit Port-Address (0-32767), never decomposed into Net/Sub-Net/
// Universe on screen. Universe 17 is typed and read as 17. That is what a
// gateway's faceplate shows, and agreeing with the faceplate is this
// screen's entire job. The show's own numbering lives on Patch, Rig Check
// and Rig Walk; Devices shows both. UI.parseArtnet normalises on the
// way in, UI.formatArtnet on the way out, and a starting-universe change
// rewrites only the DOM's text (syncUniverseFieldsToBase) — it never
// re-parses what is already on screen. Reformatting a stale displayed string
// under a new base is a silent wire-value shift, and here that writes a
// wrong universe to a real gateway mid-show.
//
// THE SHARED-BLOCK CONSTRAINT (verified against internal/artnet/
// nodeconfig.go this round). ArtAddress (OpCode 0x6000) carries ONE
// NetSwitch (offset 12) and ONE SubSwitch (offset 104) per packet, and a
// packet targets one BindIndex (offset 13). Only SwIn[4] (96-99) and
// SwOut[4] (100-103) are per port, and each of those is a 4-bit Universe
// nibble. So a node's ports cannot span arbitrary universes: every port on
// one bind index must sit inside a single contiguous 16-universe block. When
// the universes typed here cannot be expressed that way we say which ports
// conflict and refuse to send — no clamping, no picking a winner, no partial
// write. Sending output to the wrong fixtures on a show is worse than an
// honest refusal.
//
// Rules (architecture rev 5 §4, adopted from Rackmaster's pitfalls, and the
// task brief's MANDATORY UI rules):
//  - oninput mutates state only; full re-render happens on onchange/explicit
//    refresh.
//  - re-render preserves the selected row/port and scroll position.
//  - the *editable config section* is built once per node selection and is
//    NOT touched by the periodic (WS-triggered) node-list refresh — only
//    the read-only info/ports block re-renders on every poll tick. This is
//    what keeps a half-typed short name or IP field from being clobbered
//    out from under Dom every ~3s by the node's own ArtPollReply traffic.
//  - every universe edit is STAGED. Nothing on this screen writes a universe
//    to a node until "Save names & addressing" is pressed.
//
// Styling follows internal/web/static/css/DESIGN.md (the shared screen kit
// taken from Reconcile and Rig Check): state is carried by a word first,
// then an icon, then a border weight; unknowns are named out loud rather
// than printed as a plausible 0; wide content scrolls inside its own box.
const NodesScreen = (() => {
  let nodes = [];
  let selectedKey = null;   // "ip|bindIndex" — the NodeKey currently backing the inspector
  let selectedPortIndex = null; // zero-based physical port slot within that bind
  let selectedIP = null;    // IP of the currently focused device group (accordion selection)
  let detailBuiltFor = null; // key whose config section is currently in the DOM
  // The editor tab and staged port values survive background node refreshes.
  let editorTab = 'port';

  // Per-node editable config, keyed by node key. Populated once per
  // selection from the node's current known values; mutated in place by
  // oninput handlers; read back by the Save/Apply actions. Note some fields
  // (merge mode, per-port input-enable) have no read-back in this API —
  // nodePortJSON carries no such state — so they start at a sane default
  // and are clearly hinted as "not read from the node" rather than implying
  // they reflect current hardware state.
  let configState = {};
  const networkState = {};
  const networkDraft = n => networkState[n.ip] || (networkState[n.ip] = {ip:"",mask:"",gateway:"",dhcp:false,ipApplied:false,ipArmed:false});
  let actionStatus = {}; // key -> { addressing, merge, input, ip }

  function keyOf(n) { return n.ip + '|' + n.bindIndex; }
  function targetKey() { return selectedKey === null ? null : selectedKey + '|' + selectedPortIndex; }
  function selectPort(key, index, ip) {
    selectedKey = key; selectedPortIndex = index; selectedIP = ip;
    render();
  }

  // --- universe notation ---------------------------------------------------
  // The ONE place a universe number becomes text on this screen, and the ONE
  // place a typed one becomes a number (DESIGN.md rule 5). Both take/return
  // the canonical 0-based Art-Net Port-Address, never a displayed string.

  // PORT_ADDRESS_MAX is Art-Net's 15-bit Port-Address ceiling —
  // Net 127 : Sub-Net 15 : Universe 15 == 0x7FFF (internal/artnet/packet.go's
  // PortAddress/PortAddressFromRaw). Not 15: that is the Universe nibble
  // alone, which is what this screen used to validate against.
  const PORT_ADDRESS_MAX = 0x7FFF;
  const UNIVERSES_PER_BLOCK = 16;

  const uni = (raw) => UI.formatArtnet(raw);

  // portCanonical: the port's full Port-Address for one direction, exactly as
  // the server sent it (server.go serialises artnet.PortAddress.RawValue()).
  // No masking — masking is what produced defect (a).
  function portCanonical(p, dir) {
    return (dir === 'input' ? p.inputAddress : p.outputAddress) || 0;
  }

  // blockOf: which 16-universe block a canonical Port-Address falls in. Two
  // universes are reachable from one ArtAddress packet iff their blocks match
  // — the block IS (Net<<4 | Sub-Net), the two per-node fields.
  function blockOf(canonical) { return Math.floor(canonical / UNIVERSES_PER_BLOCK); }

  // blockRangeLabel: a block named the way the tech reads universes, in his
  // own base — "1–16" at base 1, "0–15" at base 0.
  function blockRangeLabel(block) {
    const lo = block * UNIVERSES_PER_BLOCK;
    return uni(lo) + '–' + uni(lo + UNIVERSES_PER_BLOCK - 1);
  }

  // --- Device grouping (IP) + numeric sort ---------------------------------
  // An Art-Net node's ports all share one physical box. Some gateways (e.g.
  // Obsidian's EN4, per the bench capture reproduced in demo.go's
  // realEN4PortReplies) advertise that box as several ArtPollReply "nodes"
  // at the SAME IP, one per physical port, each its own BindIndex — so
  // grouping on NodeKey (IP, BindIndex), which is what distinguishes
  // genuinely separate node identities elsewhere in this app (see
  // internal/session/artnetsession.go), would still split that box into
  // several top-level entries. IP alone is the key that reunites both
  // shapes: a single-reply, NumPorts=4-style node (one nodes[] entry, many
  // ports — demo.go's en4Reply at 2.11.90.2) and a bind-per-port node (many
  // nodes[] entries at one IP, one port each — realEN4PortReplies at
  // 2.11.90.4) both collapse to one device group either way. Two distinct
  // physical nodes coincidentally sharing an IP is not a real Art-Net
  // scenario (nodes address a LAN individually), so this is safe.
  function deviceGroups() {
    const order = [];
    const byIP = {};
    nodes.forEach(n => {
      if (!byIP[n.ip]) { byIP[n.ip] = { ip: n.ip, entries: [] }; order.push(n.ip); }
      byIP[n.ip].entries.push(n);
    });
    const groups = order.map(ip => {
      const g = byIP[ip];
      const ports = [];
      g.entries.forEach(n => {
        const own = n.ports || [];
        own.forEach(p => {
          ports.push({
            key: keyOf(n), ip: n.ip, bindIndex: n.bindIndex, index: p.index,
            // Sort key only — the flat Port-Address (net/sub/universe packed
            // into one number). Numeric, never string-compared (see
            // compareIPNumeric's doc comment on the sibling bug this guards
            // against).
            portAddress: p.output ? p.outputAddress : (p.input ? p.inputAddress : 0),
            // nodeShortName / nameIsPerPort: ArtPollReply's ShortName is a
            // NODE field (one per reply / per bind index) — there is no
            // per-port name anywhere in the Art-Net wire format. See
            // portNameOf() below for what that means for the display.
            nodeShortName: n.shortName || '',
            nameIsPerPort: own.length === 1,
            raw: p,
          });
        });
      });
      ports.sort((a, b) => a.portAddress - b.portAddress);
      return { ip: g.ip, entries: g.entries, ports, name: deviceGroupName(g.entries) };
    });
    groups.sort((a, b) => compareIPNumeric(a.ip, b.ip));
    return groups;
  }

  // compareIPNumeric: sort dotted-quad IPs by their numeric octet values,
  // NOT lexicographically. A plain string/array .sort() would put
  // "2.11.90.10" before "2.11.90.2" (character '1' < '2'), exactly the
  // sibling bug the owner already flagged on another screen ("string-
  // sorting numbers"). Comparing octet-by-octet as integers instead makes
  // .10 sort after .2, as a human expects.
  function compareIPNumeric(a, b) {
    const pa = a.split('.').map(Number);
    const pb = b.split('.').map(Number);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
      const d = (pa[i] || 0) - (pb[i] || 0);
      if (d) return d;
    }
    return 0;
  }

  // portNameOf — Task 2, "add the 'short name' to the output selection area
  // so I can see which each port was named".
  //
  // WHAT ACTUALLY EXISTS ON THE WIRE: ArtPollReply carries ShortName (18
  // bytes) and LongName (64 bytes) and NOTHING per port — PortTypes,
  // GoodInput, GoodOutput, SwIn and SwOut are the only per-port arrays, and
  // none of them is a label. server.go's nodePortJSON therefore has no name
  // field, and inventing one here would be exactly the kind of plausible
  // fiction DESIGN.md rule 3 forbids.
  //
  // What IS real, and is what the owner is actually looking at on his EN4:
  // a bind-per-port gateway sends one ArtPollReply per physical port, each
  // with its own BindIndex and its own ShortName — the bench capture in
  // demo.go's realEN4PortReplies has ShortName "Port 1"/"Port 2"/"Port 3"
  // across three binds of one box. For that shape the node short name IS the
  // port's name, and we say so. For the other real shape (one reply,
  // NumPorts=4 — demo.go's en4Reply) one ShortName covers all four ports, so
  // we show it labelled as the node's name shared by every port, rather than
  // repeating it as if each port had been named individually.
  function portNameOf(p) {
    if (!p.nodeShortName) return { text: '', perPort: false };
    return { text: p.nodeShortName, perPort: !!p.nameIsPerPort };
  }

  // deviceGroupName prefers a name shared by every NodeKey contributing to
  // this IP (the common case: one node, or a bind-per-port gateway that
  // repeats its own long name on every reply — demo.go's realEN4PortReplies
  // all report LongName "NETRON EN4" despite distinct per-port ShortNames).
  // Falling back to the first entry's own name keeps a group labeled even
  // when nothing agrees.
  function deviceGroupName(entries) {
    const longNames = Array.from(new Set(entries.map(n => n.longName).filter(Boolean)));
    if (longNames.length === 1) return longNames[0];
    const shortNames = Array.from(new Set(entries.map(n => n.shortName).filter(Boolean)));
    if (shortNames.length === 1) return shortNames[0];
    return entries[0].shortName || entries[0].longName || entries[0].ip;
  }

  // deviceStatusPill/deviceLastSeen: device-group rollups of the same
  // per-node facts nodeStatusPill already reports (below) — a physical
  // device is "stale" if any contributing NodeKey is (any port could be the
  // one that stopped answering), "RDM" only if every contributing NodeKey
  // reports RDM-capable. Each is a kit pill: a word, then an icon, then a
  // border — never colour alone (DESIGN.md rule 1).
  function deviceStatusPill(g) {
    if (g.entries.some(n => n.stale)) {
      return pill('md', 'warn', 'status-warning', 'Stale · stopped answering');
    }
    if (g.entries.every(n => n.rdmCapable)) return pill('md', 'ok', 'status-ok', 'RDM capable');
    return pill('md', 'open', '', 'No RDM reported');
  }

  function deviceLastSeen(g) {
    const times = g.entries.map(n => n.lastSeen).filter(Boolean);
    if (!times.length) return null;
    return times.reduce((a, b) => (new Date(a) > new Date(b) ? a : b));
  }

  // pill: the kit's one way to draw a state — skin, optional tone, always a
  // word (DESIGN.md "A pill always contains a word"). An untoned pill is
  // honest; never borrow a neighbouring state's colour.
  function pill(skin, tone, icon, text, solid) {
    const cls = ['b5-pill', 'b5-pill--' + skin];
    if (tone) cls.push('b5-pill--' + tone);
    if (solid) cls.push('b5-pill--solid');
    return `<span class="${cls.join(' ')}">${icon ? UI.icon(icon) : ''}${escapeHtml(text)}</span>`;
  }

  async function refresh() {
    nodes = await Api.getNodes();
    render();
  }

  // nodeStatusPill combines the two facts the app already tracked
  // (rdmCapable, stale) into one status marker with an explicit text label
  // (never colour alone) instead of two separate badge columns.
  function nodeStatusPill(n) {
    if (n.stale) return pill('md', 'warn', 'status-warning', 'Stale · stopped answering');
    if (n.rdmCapable) return pill('md', 'ok', 'status-ok', 'RDM capable');
    return pill('md', 'open', '', 'No RDM reported');
  }

  // --- Device accordion (grouped, sorted) ----------------------------------

  function render() {
    const groups = deviceGroups();
    renderAccordion(groups);

    const el = document.getElementById('nodeDetail');
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) {
      selectedKey = null; selectedPortIndex = null; selectedIP = null;
      detailBuiltFor = null;
      el.innerHTML = `<div class="b5-panel__body"><div class="b5-empty">${UI.icon('network-node')}<span class="b5-empty__title">No node selected</span><span class="b5-empty__body">Choose a node to edit.</span></div></div>`;
      return;
    }
    if (selectedPortIndex!==null && !(n.ports||[]).some(p=>p.index===selectedPortIndex)) {
      delete configState[selectedKey];
      selectedKey=null; selectedIP=null; selectedPortIndex=null; detailBuiltFor=null;
      renderAccordion(groups);
      el.innerHTML='<div class="b5-panel__body">The selected port is no longer reported. Choose a node again.</div>';
      return;
    }
    if (detailBuiltFor !== targetKey()) {
      detailBuiltFor = targetKey();
      if (!configState[selectedKey]) initConfigState(n);
      buildDetailShell(n);
    } else {
      renderInfoStatic(n);
    }
  }

  // The left pane lists physical nodes only. Ports appear once, in the editor.
  function renderAccordion(groups) {
    const el = document.getElementById('nodesAccordion');
    if (!el) return;
    const scrollTop = el.scrollTop;
    if (selectedKey && !groups.some(g => g.entries.some(n => keyOf(n) === selectedKey))) {
      selectedKey=null; selectedPortIndex=null; selectedIP=null;
    }
    const focusedIP=document.activeElement && document.activeElement.dataset && document.activeElement.dataset.nodeIp;
    el.innerHTML = groups.map(g => `
      <button type="button" class="b5-statecard b5-nodes-parent ${g.ip===selectedIP?'is-armed':''}" data-node-ip="${escapeHtml(g.ip)}" aria-pressed="${g.ip===selectedIP}">
        <strong class="b5-statecard__name">${escapeHtml(g.name)}</strong>
        <span class="b5-statecard__meta">${escapeHtml(g.ip)} · ${g.ports.length} ports</span>
        <span class="b5-statecard__state">${deviceStatusPill(g)}${g.ip===selectedIP?pill('tag','accent','','Selected'):''}</span>
      </button>`).join('') || '<p class="b5-board__empty">No nodes found. Check the interface and refresh.</p>';
    el.querySelectorAll('[data-node-ip]').forEach(button=>button.addEventListener('click',()=>{
      const g=groups.find(g=>g.ip===button.dataset.nodeIp);
      if(g.ip===selectedIP) return;
      const p=g.ports[0];
      if(!p) editorTab='node';
      selectPort(p?p.key:keyOf(g.entries[0]),p?p.index:null,g.ip);
    }));
    if(focusedIP) {const button=[...el.querySelectorAll('[data-node-ip]')].find(b=>b.dataset.nodeIp===focusedIP);if(button)button.focus({preventScroll:true});}
    el.scrollTop=scrollTop;
  }

  function setEditorTab(tab) {
    editorTab=tab;
    for(const kind of ['node','port']) {
      const button=byId('nodeTab-'+kind),panel=byId('nodePanel-'+kind);
      if(button) {button.setAttribute('aria-selected',String(kind===tab));button.classList.toggle('is-active',kind===tab);}
      if(panel) panel.hidden=kind!==tab;
    }
    const picker=byId('nodePortPicker'),info=byId('nodeInfoStatic');
    if(picker) picker.hidden=tab!=='port';
    if(info) info.hidden=tab!=='port';
  }

  function buildDetailShell(n) {
    const el = document.getElementById('nodeDetail');
    const group=deviceGroups().find(g=>g.ip===n.ip);
    el.innerHTML = `
      <div class="b5-panel__header">
        <div><h2 class="b5-panel__title" id="nodeEditorTitle">${escapeHtml(group.name)}</h2><span class="b5-caption">${escapeHtml(n.ip)}</span></div>
        <button class="b5-btn b5-btn--sm" id="btnCloseNodeEditor">Close editor</button>
      </div>
      <div class="b5-nodes-tabs" role="tablist" aria-label="Node editor">
        <button class="b5-btn" id="nodeTab-node" role="tab" aria-controls="nodePanel-node">Node settings</button>
        <button class="b5-btn" id="nodeTab-port" role="tab" aria-controls="nodePanel-port">Port settings</button>
      </div>
      <div class="b5-panel__body b5-stack b5-nodes-editor__body">
        <div id="nodePortPicker" class="b5-field"><label for="nodeDetailPortTarget" class="b5-field__label">Port</label>
          <select class="b5-select" id="nodeDetailPortTarget">${group.ports.map(p=>`<option value="${escapeHtml(p.key+'|'+p.index)}" ${p.key===selectedKey&&p.index===selectedPortIndex?'selected':''}>${escapeHtml(p.nameIsPerPort&&p.nodeShortName?p.nodeShortName:'Port '+p.index)}${group.entries.length>1?' · bind '+p.bindIndex:''} · ${escapeHtml(portUniverseLabel(p.raw))}</option>`).join('')||'<option>No ports reported</option>'}</select>
        </div>
        <div id="nodeInfoStatic"></div>
        <div id="nodeConfigSection"></div>
      </div>
    `;
    byId('btnCloseNodeEditor').addEventListener('click', () => {selectedKey=null;selectedPortIndex=null;selectedIP=null;render();});
    byId('nodeDetailPortTarget').addEventListener('change',e=>{const p=group.ports.find(p=>p.key+'|'+p.index===e.target.value);if(p)selectPort(p.key,p.index,p.ip);});
    for(const kind of ['node','port']) byId('nodeTab-'+kind).addEventListener('click',()=>setEditorTab(kind));
    renderInfoStatic(n);
    renderConfigSection(n);
    setEditorTab(editorTab);
  }

  // portDirectionLabel/portUniverseLabel: a node port is an input OR an
  // output — never both at once operationally (Dom: "they're either inputs
  // or outputs, never both at the same time. Bidirectionality only applies
  // to their ethernet ports, and that's not something we need to report").
  // A port's *advertised capability* (PortTypes bits) can still list both,
  // so when that happens both universes are shown, clearly labeled, rather
  // than picking one and hiding the other.
  // portTypeHex formats the raw, undecoded ArtPollReply PortTypes byte
  // (server.go's nodePortJSON.portTypeRaw) so a port that reports neither
  // the input nor output bit is diagnosable at a glance instead of a dead
  // end (bench report: every port on an Obsidian EN4 showed "n/a" for
  // input/output). This is display-only — the input/output decode itself
  // lives in internal/session/artnetsession.go, not here.
  function portTypeHex(p) {
    const v = p.portTypeRaw || 0;
    return '0x' + v.toString(16).padStart(2, '0').toUpperCase();
  }

  function portDirectionLabel(p) {
    if (p.input && p.output) return 'Input + Output';
    if (p.output) return 'Output';
    if (p.input) return 'Input';
    return `Direction not reported (PortTypes ${portTypeHex(p)})`;
  }

  // portUniverseLabel — DEFECT (a), fixed. Formats the FULL Port-Address the
  // server sent, through UI.formatArtnet, so this reads the same number
  // Patch, Send, Devices and Rig Check read for the same port. It used to
  // print `outputAddress & 0x0F`, the Universe nibble alone, which silently
  // dropped Net and Sub-Net.
  function portUniverseLabel(p) {
    if (p.input && p.output) {
      return `universe in ${uni(p.inputAddress)} / out ${uni(p.outputAddress)}`;
    }
    if (p.output) return `universe ${uni(p.outputAddress)}`;
    if (p.input) return `universe ${uni(p.inputAddress)}`;
    // Rule 3: an unreported direction has no universe to report either. Do
    // not print a plausible 0 (or, worse, a plausible 1).
    return 'universe not reported';
  }

  function renderInfoStatic(n) {
    if(keyOf(n)!==selectedKey)return;
    const target = document.getElementById('nodeInfoStatic');
    if (!target) return;
    const ports = (n.ports || []).filter(p => p.index === selectedPortIndex);
    const portRows = ports.map(p => `
      <tr>
        <td data-label="Port">${p.index}</td>
        <td data-label="Direction">${escapeHtml(portDirectionLabel(p))}</td>
        <td data-label="Universe" class="b5-text-mono">${escapeHtml(portUniverseLabel(p))}</td>
        <td data-label="RDM">${p.rdmEnabled ? pill('tag', 'ok', '', 'RDM on') : pill('tag', 'open', '', 'Not reported')}</td>
      </tr>`).join('');
    target.innerHTML = `
      <details><summary>Reported state · ${ports.map(p=>escapeHtml(portDirectionLabel(p))+' · '+escapeHtml(portUniverseLabel(p))).join('')}</summary>
        ${nodeStatusPill(n)}
        <div class="b5-scrollbox">
          <table class="b5-table b5-table--responsive">
            <thead><tr><th>Port</th><th>Direction</th><th>Universe</th><th>RDM</th></tr></thead>
            <tbody>${portRows || '<tr><td colspan="4">This bind reports no ports.</td></tr>'}</tbody>
          </table>
        </div>
      </details>
    `;
  }

  // --- editable config section (built once per selection) -----------------

  function initConfigState(n) {
    const ports = n.ports || [];
    configState[keyOf(n)] = {
      shortName: n.shortName || '',
      longName: n.longName || '',
      ports: ports.map(p => ({
        index: p.index,
        // CANONICAL 15-bit Port-Addresses, straight from the wire values the
        // server sent — never a nibble, never a displayed string. The
        // display base is applied only when these reach the DOM.
        universeIn: portCanonical(p, 'input'),
        universeOut: portCanonical(p, 'output'),
        universeInApplied: portCanonical(p, 'input'),
        universeOutApplied: portCanonical(p, 'output'),
        // uniError holds the reason the last thing typed into this port's
        // universe field was not usable. A rejected keystroke never becomes
        // a number: the last good canonical value stays put and the save is
        // refused, rather than a typo silently becoming universe 0.
        uniError: '',
        input: p.input, output: p.output,
        portTypeRaw: p.portTypeRaw,
        // direction selects which of universeIn/universeOut is currently
        // being displayed/edited (report task: "In node config, editing the
        // universe edits the value for the port's current direction;
        // toggling direction switches which field is being edited/
        // displayed"). Defaults to Output when a port advertises both
        // capabilities (the common case for a lighting-distribution node);
        // fixed to whichever single direction the port actually supports
        // otherwise.
        direction: p.output ? 'output' : 'input',
        mergeMode: 'htp',           // no read-back from ArtPollReply — default/editable only
        mergeModeApplied: 'htp',    // last value actually sent, for dirty/Apply tracking
        inputEnabled: true,         // no read-back from ArtPollReply — default/editable only
      })),
      // ip/mask/gateway/dhcp are staged by oninput; ipApplied gates the
      // arm-then-confirm flow behind an explicit Apply step first (task
      // rule: every field needs Apply, and "IP config keeps its existing
      // extra arm-then-confirm step ON TOP OF Apply" — so IP config alone
      // gets three deliberate steps: Apply, Arm, Confirm/send).
      ip: '', mask: '', gateway: '', dhcp: false, ipApplied: false, ipArmed: false,
    };
    actionStatus[keyOf(n)] = { addressing: '', merge: '', input: '', direction: '', rdm: '', ip: '' };
  }

  // --- addressing derivation + the shared-block constraint ------------------

  // programmedPorts: the ports whose universe this save will actually write,
  // and which direction each is written in. A port that advertises neither
  // direction has nothing to program (its SwIn/SwOut stay "leave unchanged"),
  // and a dual-capability port is programmed only in the direction currently
  // selected — the direction the tech is looking at and editing. The other
  // direction is left untouched rather than being rewritten from a value
  // nobody typed.
  function programmedPorts(st, onlyIndex) {
    const out = [];
    st.ports.forEach((p, i) => {
      if (i > 3) return; // ArtAddress has exactly four SwIn/SwOut slots
      if (onlyIndex !== undefined && p.index !== onlyIndex) return;
      if (!p.input && !p.output) return;
      const dir = p.direction === 'input' && p.input ? 'input' : (p.output ? 'output' : 'input');
      out.push({
        i, index: p.index, dir,
        canonical: dir === 'input' ? p.universeIn : p.universeOut,
        error: p.uniError,
      });
    });
    return out;
  }

  // deriveAddressing turns the canonical per-port Port-Addresses into the
  // ArtAddress fields that actually go on the wire, or explains why they
  // cannot be expressed.
  //
  // ArtAddress carries ONE NetSwitch and ONE SubSwitch for the whole packet
  // (internal/artnet/nodeconfig.go, offsets 12 and 104) and one BindIndex
  // (offset 13); only SwIn[4]/SwOut[4] are per port, and each is a 4-bit
  // Universe nibble. Net<<4|Sub-Net is therefore a per-node 16-universe
  // block that every programmed port must sit inside. When they do not, this
  // returns ok:false with a message naming the offending ports and the block
  // they would have to share — and saveAddressing sends nothing. No clamping,
  // no truncation, no partial write.
  function deriveAddressing(st, onlyIndex) {
    const used = programmedPorts(st, onlyIndex);
    const swIn = [null, null, null, null];
    const swOut = [null, null, null, null];

    const bad = used.filter(u => u.error);
    if (bad.length) {
      return {
        ok: false,
        message: 'Nothing was sent. ' + bad.map(u => `Port ${u.index}: ${u.error}`).join(' ')
          + ` Universes are entered as the raw ${UI.universeScheme('artnet')} — the same number the gateway's own faceplate shows; the valid range is `
          + `${UI.artnetInputAttrs().min}–${UI.artnetInputAttrs().max}.`,
      };
    }
    if (!used.length) {
      // Nothing to address — names only. Leave Net/Sub-Net/SwIn/SwOut
      // untouched rather than programming a block nobody asked for.
      return { ok: true, netSwitch: null, subSwitch: null, swIn, swOut, used, block: null };
    }

    const blocks = {};
    used.forEach(u => {
      const b = blockOf(u.canonical);
      (blocks[b] = blocks[b] || []).push(u);
    });
    const keys = Object.keys(blocks);
    if (keys.length > 1) {
      const parts = keys
        .map(Number)
        .sort((a, b) => a - b)
        .map(b => `${blocks[b].map(u => `port ${u.index} (universe ${uni(u.canonical)})`).join(' and ')} in ${blockRangeLabel(b)}`);
      return {
        ok: false,
        message: 'Nothing was sent — these universes cannot be set together on one node. '
          + 'Art-Net puts a single Net and Sub-Net on the whole node (only the last step of the '
          + 'universe number is per port), so all of this node’s ports have to sit inside one '
          + `16-universe block. Right now: ${parts.join('; ')}. `
          + 'Move the ports into one block, or address them from separate nodes, then save again.',
      };
    }

    const block = Number(keys[0]);
    const net = (block >> 4) & 0x7F;
    const sub = block & 0x0F;
    used.forEach(u => {
      const nibble = u.canonical % UNIVERSES_PER_BLOCK;
      if (u.dir === 'input') swIn[u.index] = nibble; else swOut[u.index] = nibble;
    });
    return { ok: true, netSwitch: net, subSwitch: sub, swIn, swOut, used, block };
  }

  function deriveSelectedAddressing(n, st, portIndex) {
    if(portIndex!==null && !(n.ports||[]).some(p=>p.index===portIndex)) return {ok:false,message:'Port is no longer reported. Refresh and select a port again.'};
    const d=deriveAddressing(st,portIndex);
    if(!d.ok || !d.used.length) return d;
    // Net/Sub-Net are shared by a bind. A port-only save must not move
    // an unselected port (or the other advertised direction) to a new block.
    const dir=d.used[0].dir;
    const conflicts=(n.ports||[]).filter(p=>['input','output'].some(side=>
      p[side] && !(p.index===portIndex&&side===dir) && blockOf(portCanonical(p,side))!==d.block));
    if(conflicts.length) return {ok:false,message:'Nothing sent. This universe changes the shared 16-universe block and would move other port addresses. Change the node’s shared block on the gateway first.'};
    return d;
  }

  function resultText(res) {
    let t = res.confirmed ? `confirmed (${res.elapsedMs}ms)` : `no confirmation received (${res.elapsedMs}ms)`;
    if (res.warning) t += ` — ${res.warning}`;
    return t;
  }

  const statusTarget=(key,section,port)=>section==='ip'?key.split('|')[0]+'|ip':key+'|'+(['names'].includes(section)?section:port+'|'+section);
  function setStatus(key, section, msg, port=selectedPortIndex) {
    actionStatus[statusTarget(key,section,port)] = msg;
    if(statusTarget(key,section,port)!==statusTarget(selectedKey||'',section,selectedPortIndex)) return; // a late reply must not paint another node's editor
    const el = document.getElementById('status-' + section);
    if (el) el.textContent = msg;
  }

  // renderAddressingPreview: the honest, live "what will actually be sent"
  // readout under the port list. It is where the shared-block constraint
  // becomes visible BEFORE the tech presses Save, and where a conflict is
  // spelled out in his own numbering. Rebuilt on every universe keystroke —
  // it lives in its own element, so it never disturbs the field being typed
  // into.
  function renderAddressingPreview(n) {
    const area = document.getElementById('addrPreview');
    if (!area) return;
    const st = configState[keyOf(n)];
    const d = deriveSelectedAddressing(n, st, selectedPortIndex);
    if (!d.ok) {
      area.innerHTML = `
        <div class="b5-alert b5-alert--error">
          ${UI.icon('status-error')}
          <div>
            <p class="b5-alert__title">These universes cannot be saved together</p>
            <p class="b5-alert__body">${escapeHtml(d.message)}</p>
          </div>
        </div>`;
      return;
    }
    if (!d.used.length) {
      area.innerHTML = `<div class="b5-inset"><p class="b5-inset__head">What will be sent</p><p class="b5-note">Names only. None of this bind’s ports advertises an input or output direction, so no universe is programmed.</p></div>`;
      return;
    }
    const list = d.used.map(u => `port ${u.index} → universe ${uni(u.canonical)} (${u.dir})`).join(', ');
    area.innerHTML = `
      <div class="b5-inset">
        <p class="b5-inset__head">${UI.icon('apply')}What will be sent</p>
        <p class="b5-note">${escapeHtml(list)}. Other port addresses stay unchanged.</p>
      </div>`;
  }

  function renderConfigSection(n) {
    const key = keyOf(n);
    if(key!==selectedKey)return;
    const net=networkDraft(n);
    const st = configState[key];
    const target = document.getElementById('nodeConfigSection');
    if (!target) return;
    const ua = UI.artnetInputAttrs();
    const nameFields=`<div class="b5-field">
          <label class="b5-field__label" for="cfgShortName">Short name</label>
          <input id="cfgShortName" class="b5-input" type="text" maxlength="18" value="${escapeHtml(st.shortName)}">
          <span class="b5-field__hint">${st.ports.length>1?'Names are shared by all ports on this bind.':'Name reported for this port’s bind.'}</span>
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="cfgLongName">Long name</label>
          <input id="cfgLongName" class="b5-input" type="text" maxlength="64" value="${escapeHtml(st.longName)}">
        </div>`;

    target.innerHTML = `
      <div class="b5-row">
        <div>
          <p class="b5-caption">Verify sent changes on the gateway; a reply alone is not proof.</p>
          <button id="btnReloadConfig" type="button" class="b5-btn b5-btn--sm">${UI.icon('refresh')}Reload current values</button>
        </div>
      </div>

      <section class="b5-step-section" id="nodePanel-port" role="tabpanel" aria-labelledby="nodeTab-port" ${editorTab==='port'?'':'hidden'}>
        <h3 class="b5-step-section__head" id="nodeCfgHead">
          Port settings
          <span class="b5-step-section__note">staged — nothing is sent until you press Save</span>
        </h3>

        ${st.ports.length<=1?nameFields:''}

        <div class="b5-board__list b5-nodes-portcfg">
          ${st.ports.map((p, i) => p.index===selectedPortIndex?renderPortConfigCard(p, i, ua, st.ports.length===1):'').join('')
            || '<p class="b5-board__empty">This bind reports no ports, so there is nothing here to address.</p>'}
        </div>

        <div id="addrPreview"></div>

        <span class="b5-field__hint">Merge and input-enable values are not read back from the node.</span>

        <div class="b5-row">
          <button id="btnSaveAddressing" class="b5-btn b5-btn--primary">${UI.icon('apply')}${st.ports.length>1?'Save port addressing':'Save names &amp; addressing'}</button>
          <button id="btnSaveInput" class="b5-btn" ${st.ports.length===1&&st.ports[0].input?'':'disabled'}>Save input enable</button>
        </div>
        <div class="b5-row"><span class="b5-field__status" id="status-addressing"></span></div>
        <div class="b5-row"><span class="b5-field__status" id="status-merge"></span></div>
        <div class="b5-row"><span class="b5-field__status" id="status-input"></span></div>
        <div class="b5-row"><span class="b5-field__status" id="status-direction"></span></div>
        <div class="b5-row"><span class="b5-field__status" id="status-rdm"></span></div>
      </section>

      <section class="b5-step-section" id="nodePanel-node" role="tabpanel" aria-labelledby="nodeTab-node" ${editorTab==='node'?'':'hidden'}>
        ${st.ports.length>1?nameFields+'<button class="b5-btn" id="btnSaveNames">Save node names</button><p id="status-names" role="status"></p>':''}
        <h3 class="b5-step-section__head" id="nodeIpHead">
          IP configuration
          <span class="b5-step-section__note">Affects the whole node · apply, arm, confirm</span>
        </h3>
        <label class="b5-toggle"><input id="cfgDhcp" type="checkbox" ${net.dhcp ? 'checked' : ''}><span class="b5-toggle__track"></span>DHCP</label>
        <div class="b5-field">
          <label class="b5-field__label" for="cfgIp">Static IP</label>
          <input id="cfgIp" class="b5-input b5-input--mono" type="text" placeholder="e.g. 2.11.90.5" value="${escapeHtml(net.ip)}" ${net.dhcp ? 'disabled' : ''}>
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="cfgMask">Subnet mask</label>
          <input id="cfgMask" class="b5-input b5-input--mono" type="text" placeholder="e.g. 255.0.0.0" value="${escapeHtml(net.mask)}" ${net.dhcp ? 'disabled' : ''}>
        </div>
        <div class="b5-field">
          <label class="b5-field__label" for="cfgGateway">Gateway</label>
          <input id="cfgGateway" class="b5-input b5-input--mono" type="text" placeholder="optional" value="${escapeHtml(net.gateway)}" ${net.dhcp ? 'disabled' : ''}>
        </div>
        <div id="ipConfirmArea"></div>
        <div class="b5-row"><span class="b5-field__status" id="status-ip"></span></div>
      </section>
    `;

    // --- names & addressing: oninput mutates state only ---
    byId('cfgShortName').addEventListener('input', e => { st.shortName = e.target.value; });
    byId('cfgLongName').addEventListener('input', e => { st.longName = e.target.value; });
    // Direction toggle switches which of universeIn/universeOut the single
    // Universe field shows/edits — a structural change (which field is live),
    // so it re-renders rather than merely mutating state, same as the DHCP
    // toggle further down.
    target.querySelectorAll('.cfg-dir').forEach(sel => {
      sel.addEventListener('change', e => {
        const i = +e.target.dataset.i;
        st.ports[i].direction = e.target.value;
        renderConfigSection(n);
      });
    });
    // THE normalisation point for a typed universe. UI.parseArtnet turns the
    // display-base number into the canonical 0-based Port-Address that gets
    // stored; nothing else on this screen does arithmetic on a universe.
    // A value outside the real Port-Address range (or not a whole number) is
    // recorded as an error and does NOT overwrite the last good canonical
    // value — a half-typed "1" on the way to "12" must not become a staged
    // universe 1 that gets clamped and sent.
    target.querySelectorAll('.cfg-universe').forEach(inp => {
      inp.addEventListener('input', e => {
        const i = +e.target.dataset.i;
        const p = st.ports[i];
        const raw = String(e.target.value).trim();
        const num = Number(raw);
        const attrs = UI.artnetInputAttrs();
        if (raw === '' || !Number.isFinite(num) || !Number.isInteger(num)) {
          p.uniError = `“${raw}” is not a whole universe number.`;
        } else if (num < attrs.min || num > attrs.max) {
          p.uniError = `${num} is outside the Art-Net universe range (${attrs.min}–${attrs.max}).`;
        } else {
          p.uniError = '';
          const canonical = UI.parseArtnet(raw);
          if (p.direction === 'input' && p.input) p.universeIn = canonical;
          else p.universeOut = canonical;
        }
        // Only these two derived elements change — never the field being
        // typed into, so focus and caret position are untouched.
        updatePortDirtyMarkers(st);
        renderAddressingPreview(n);
      });
    });
    target.querySelectorAll('.cfg-input-en').forEach(inp => {
      inp.addEventListener('change', e => { st.ports[+e.target.dataset.i].inputEnabled = e.target.checked; });
    });
    // Merge mode: onchange on the select only stages the pending value (dirty
    // marker appears) — it does NOT apply. A separate Apply button per port
    // sends the single-shot AcCommand; Revert discards the staged change.
    // (Fixes the earlier "merge mode applies immediately on change" bug
    // flagged in the UI audit — every editable field requires explicit
    // confirmation.)
    target.querySelectorAll('.cfg-merge').forEach(sel => {
      sel.addEventListener('change', e => {
        const i = +e.target.dataset.i;
        st.ports[i].mergeMode = e.target.value;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.btn-apply-merge').forEach(btn => {
      btn.addEventListener('click', async e => {
        const i = +e.target.closest('button').dataset.i;
        const sentMode=st.ports[i].mergeMode;
        if(await setMergeMode(n, st.ports[i].index, sentMode)) st.ports[i].mergeModeApplied = sentMode;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.btn-revert-merge').forEach(btn => {
      btn.addEventListener('click', e => {
        const i = +e.target.closest('button').dataset.i;
        st.ports[i].mergeMode = st.ports[i].mergeModeApplied;
        renderConfigSection(n);
      });
    });
    target.querySelectorAll('.btn-set-direction').forEach(btn => {
      btn.addEventListener('click', async e => {
        const button = e.currentTarget;
        const i = +button.dataset.i;
        const direction = button.dataset.direction;
        await setPortDirection(n, st.ports[i].index, direction);
      });
    });
    target.querySelectorAll('.btn-set-rdm').forEach(btn => {
      btn.addEventListener('click', async e => {
        const button = e.currentTarget;
        const i = +button.dataset.i;
        const enabled = button.dataset.enabled === 'true';
        await setPortRDM(n, st.ports[i].index, enabled);
      });
    });

    if(byId('btnSaveNames'))byId('btnSaveNames').addEventListener('click',()=>saveNames(n));
    byId('btnSaveAddressing').addEventListener('click', () => saveAddressing(n));
    byId('btnSaveInput').addEventListener('click', () => saveInputEnabled(n));
    byId('btnReloadConfig').addEventListener('click', () => { if(editorTab==='node')delete networkState[n.ip];else initConfigState(n); renderConfigSection(n); });

    // --- IP config: DHCP toggle re-renders (structural change: disables
    // static fields), everything else is oninput-state / explicit confirm.
    // Any edit here re-dirties ipApplied, so re-editing after Apply forces
    // re-applying before Arm/Confirm are reachable again.
    byId('cfgDhcp').addEventListener('change', e => { net.dhcp = e.target.checked; net.ipApplied = false; net.ipArmed = false; renderConfigSection(n); });
    if (!net.dhcp) {
      // Re-editing after Apply/Arm collapses the confirm box back to
      // "needs Apply" — this only touches the separate #ipConfirmArea
      // subtree (renderIPConfirmArea), never the field being typed into, so
      // it doesn't disturb focus/cursor position while typing.
      const dirtyIP = () => { net.ipApplied = false; net.ipArmed = false; renderIPConfirmArea(n); };
      byId('cfgIp').addEventListener('input', e => { net.ip = e.target.value; dirtyIP(); });
      byId('cfgMask').addEventListener('input', e => { net.mask = e.target.value; dirtyIP(); });
      byId('cfgGateway').addEventListener('input', e => { net.gateway = e.target.value; dirtyIP(); });
    }
    renderIPConfirmArea(n);
    renderAddressingPreview(n);

    // Restore any status text already recorded for this node.
    ['addressing', 'names', 'merge', 'input', 'direction', 'rdm', 'ip'].forEach(section => {
      const s = actionStatus[statusTarget(key,section,selectedPortIndex)];
      if (s) setStatus(key, section, s);
    });
  }

  // renderPortConfigCard: one port's editable universe/merge/input, as a
  // state card rather than a table row. Its state word is "Staged change" vs
  // "Matches the node", reinforced by the card's left-border weight.
  function renderPortConfigCard(p, i, ua, canSetInput=true) {
    const both = p.input && p.output;
    const known = p.input || p.output;
    const dirCell = both
      ? `<div class="b5-field">
           <label class="b5-field__label" for="cfgDir${i}">Universe direction being edited</label>
           <select id="cfgDir${i}" class="b5-select cfg-dir" data-i="${i}">
             <option value="output" ${p.direction === 'output' ? 'selected' : ''}>Output</option>
             <option value="input" ${p.direction === 'input' ? 'selected' : ''}>Input</option>
           </select>
           <span class="b5-field__hint">This port advertises both. Only the direction selected here has its universe written when you save; the other is left as the node has it. This does not change the physical port direction; use the control below for that.</span>
         </div>`
      : '';
    const dirWord = p.output && !p.input ? 'Output' : (p.input && !p.output ? 'Input' : (both ? 'Input + Output' : `Direction not reported (PortTypes ${portTypeHex(p)})`));
    const canonical = p.direction === 'input' && p.input ? p.universeIn : p.universeOut;
    const applied = p.direction === 'input' && p.input ? p.universeInApplied : p.universeOutApplied;
    const uniDirty = !p.uniError && canonical !== applied;
    const uniCell = known
      ? `<div class="b5-field ${p.uniError ? 'b5-field--error' : (uniDirty ? 'b5-field--dirty' : '')}">
           <label class="b5-field__label" for="cfgUni${i}">Universe <span class="b5-caption">(${escapeHtml(UI.universeScheme('artnet'))})</span></label>
           <input id="cfgUni${i}" class="b5-input b5-numinput b5-input--mono cfg-universe" data-i="${i}" type="number" min="${ua.min}" max="${ua.max}" value="${escapeHtml(uni(canonical))}" inputmode="numeric">
           <span class="b5-field__status" id="cfgUniStatus${i}">${p.uniError ? escapeHtml(p.uniError) : (uniDirty ? 'Staged — not sent yet' : '')}</span>
           <span class="b5-field__hint">Uses your selected universe display base.</span>
         </div>`
      : `<p class="b5-note">This port advertises neither an input nor an output (PortTypes ${escapeHtml(portTypeHex(p))}), so it has no universe to set.</p>`;
    const mergeDirty = p.mergeMode !== p.mergeModeApplied;
    const mergeCell = p.output
      ? `<div class="b5-field ${mergeDirty ? 'b5-field--dirty' : ''}">
           <label class="b5-field__label" for="cfgMerge${i}">Merge mode</label>
           <div class="b5-field__row">
             <select id="cfgMerge${i}" class="b5-select cfg-merge" data-i="${i}"><option value="htp" ${p.mergeMode === 'htp' ? 'selected' : ''}>HTP</option><option value="ltp" ${p.mergeMode === 'ltp' ? 'selected' : ''}>LTP</option></select>
             <span class="b5-field__actions">
               <button type="button" class="b5-btn b5-btn--sm b5-btn--primary btn-apply-merge" data-i="${i}" ${mergeDirty ? '' : 'disabled'}>${UI.icon('apply')}Send merge mode</button>
               ${mergeDirty ? `<button type="button" class="b5-btn b5-btn--sm b5-btn--ghost btn-revert-merge" data-i="${i}">${UI.icon('revert')}Revert</button>` : ''}
             </span>
           </div>
           <span class="b5-field__status">${mergeDirty ? 'Staged — not sent yet' : 'Not read from the node'}</span>
         </div>`
      : '';
    // inputEnabled has no ArtPollReply read-back (nodePortJSON carries no
    // such field, and there is none to carry — Phase 1c+ notes: this is not
    // confirmed hardware state). The checkbox pre-checks to a hinted default
    // so the field is immediately usable, but the text next to it makes clear
    // that default is a guess, not a report, so it can't be misread as
    // "confirmed off" if unchecked or "confirmed on" if checked — the exact
    // "invented value that looks confirmed" failure mode this guards against.
    const inputCell = p.input
      ? `<div class="b5-field">
           <label class="b5-checkbox"><input class="cfg-input-en" data-i="${i}" type="checkbox" ${p.inputEnabled ? 'checked' : ''} ${canSetInput?'':'disabled'}>Input enabled</label>
           <span class="b5-field__hint">${canSetInput?'Not read from the node. Apply with Save input enable.':'Unavailable per port on this bind: ArtInput rewrites every port’s enable state.'}</span>
         </div>`
      : '';
    const directionCell = known
      ? `<div class="b5-field">
           <p class="b5-field__label">Physical port direction</p>
           <div class="b5-field__row">
             <span class="b5-field__status">Reported: ${escapeHtml(dirWord)}</span>
             <span class="b5-field__actions">
               <button type="button" class="b5-btn b5-btn--sm btn-set-direction" data-i="${i}" data-direction="output">Set output</button>
               <button type="button" class="b5-btn b5-btn--sm btn-set-direction" data-i="${i}" data-direction="input">Set input</button>
             </span>
           </div>
           <span class="b5-field__hint">Setting input also clears this port’s subscriber list.</span>
         </div>`
      : '';
    const rdmCell = p.output
      ? `<div class="b5-field">
           <p class="b5-field__label">RDM on this port</p>
           <div class="b5-field__row">
             <span class="b5-field__status">Reported: ${p.rdmEnabled ? 'enabled' : 'disabled'}</span>
             <span class="b5-field__actions">
               <button type="button" class="b5-btn b5-btn--sm b5-btn--primary btn-set-rdm" data-i="${i}" data-enabled="true">Enable RDM</button>
               <button type="button" class="b5-btn b5-btn--sm b5-btn--danger btn-set-rdm" data-i="${i}" data-enabled="false">Disable RDM</button>
             </span>
           </div>
           <span class="b5-field__hint"></span>
         </div>`
      : '';
    const statePill = p.uniError
      ? pill('md', 'danger', 'status-error', 'Universe not valid')
      : (uniDirty || mergeDirty ? pill('md', 'accent', 'status-pending', 'Staged — not sent yet', true)
        : pill('md', 'ok', 'status-ok', 'Matches what the node reports'));
    const cls = p.uniError ? 'is-danger' : (uniDirty || mergeDirty ? 'is-armed' : (known ? 'is-ok' : 'is-open'));
    return `
      <article class="b5-statecard ${cls}">
        <div class="b5-statecard__top">
          <div class="b5-statecard__id">
            <strong class="b5-statecard__name">Port ${p.index}</strong>
            <span class="b5-statecard__meta">${escapeHtml(dirWord)}</span>
          </div>
        </div>
        <div class="b5-statecard__state">${statePill}</div>
        ${dirCell}
        ${uniCell}
        ${directionCell}
        ${mergeCell}
        ${inputCell}
        ${rdmCell}
      </article>`;
  }

  // updatePortDirtyMarkers refreshes only the staged/valid words and status
  // lines on the port cards, without rebuilding the inputs — so it can run on
  // every keystroke without stealing focus.
  function updatePortDirtyMarkers(st) {
    st.ports.forEach((p, i) => {
      const status = document.getElementById('cfgUniStatus' + i);
      if (!status) return;
      const canonical = p.direction === 'input' && p.input ? p.universeIn : p.universeOut;
      const applied = p.direction === 'input' && p.input ? p.universeInApplied : p.universeOutApplied;
      const dirty = !p.uniError && canonical !== applied;
      status.textContent = p.uniError ? p.uniError : (dirty ? 'Staged — not sent yet' : '');
      const field = status.parentElement;
      if (field && field.classList) {
        field.classList.toggle('b5-field--error', !!p.uniError);
        field.classList.toggle('b5-field--dirty', dirty);
      }
    });
  }

  // syncUniverseFieldsToBase — DESIGN.md rule 5, the part that is easy to get
  // wrong. When the display base changes under a STAGED edit, the canonical
  // value in configState must not move: only the text in the box is
  // rewritten, from that canonical value, through UI.formatArtnet. Reading
  // the box back and re-parsing it under the new base would shift the staged
  // wire value by one — and this screen writes universes to a real gateway.
  function syncUniverseFieldsToBase() {
    const n = nodes.find(x => keyOf(x) === selectedKey);
    if (!n) return;
    const st = configState[keyOf(n)];
    if (!st) return;
    const ua = UI.artnetInputAttrs();
    st.ports.forEach((p, i) => {
      const inp = document.getElementById('cfgUni' + i);
      if (!inp) return;
      inp.min = ua.min;
      inp.max = ua.max;
      if (p.uniError) return; // an invalid draft is left exactly as typed
      const canonical = p.direction === 'input' && p.input ? p.universeIn : p.universeOut;
      inp.value = UI.formatArtnet(canonical);
    });
    renderAddressingPreview(n);
  }

  // renderIPConfirmArea is a three-stage gate before anything reaches the
  // wire — Apply (stages the edit, same as every other field), then Arm,
  // then Confirm — because a mis-set IP can strand a node off the show
  // network, this is the one field the task brief calls out for extra
  // ceremony on top of the baseline Apply-to-confirm rule.
  function renderIPConfirmArea(n) {
    const key = keyOf(n);
    if(key!==selectedKey)return;
    const st = networkDraft(n);
    const area = document.getElementById('ipConfirmArea');
    if (!area) return;
    if (!st.ipApplied) {
      area.innerHTML = `
        <div class="b5-row">
          <button id="btnApplyIP" class="b5-btn b5-btn--primary">${UI.icon('apply')}Apply</button>
          <span class="b5-field__hint">Sending still requires Arm and Confirm.</span>
        </div>`;
      byId('btnApplyIP').addEventListener('click', () => { st.ipApplied = true; renderIPConfirmArea(n); });
    } else if (!st.ipArmed) {
      area.innerHTML = `
        <div class="b5-row">
          ${pill('md', 'ok', 'status-ok', 'Applied — staged, still not sent')}
          <button id="btnArmIP" class="b5-btn b5-btn--danger">Arm send…</button>
          <button id="btnRevertIP" class="b5-btn b5-btn--ghost">${UI.icon('revert')}Revert</button>
        </div>`;
      byId('btnArmIP').addEventListener('click', () => { st.ipArmed = true; renderIPConfirmArea(n); });
      byId('btnRevertIP').addEventListener('click', () => { delete networkState[n.ip]; renderConfigSection(n); });
    } else {
      area.innerHTML = `
        <div class="b5-alert b5-alert--warning">
          ${UI.icon('status-warning')}
          <div>
            <p class="b5-alert__title">Confirm IP change</p>
            <p class="b5-alert__body">A mis-set IP can strand this node off the show network. Confirm: ${st.dhcp ? 'switch to DHCP' : `static ${escapeHtml(st.ip || '(unchanged)')} / ${escapeHtml(st.mask || '(unchanged)')}${st.gateway ? ' / gw ' + escapeHtml(st.gateway) : ''}`}</p>
            <div class="b5-row">
              <button id="btnConfirmIP" class="b5-btn b5-btn--danger">Yes, send now</button>
              <button id="btnCancelIP" class="b5-btn b5-btn--ghost">Cancel</button>
            </div>
          </div>
        </div>
      `;
      byId('btnConfirmIP').addEventListener('click', () => sendIPConfig(n));
      byId('btnCancelIP').addEventListener('click', () => { st.ipArmed = false; renderIPConfirmArea(n); });
    }
  }

  async function saveNames(n) {
    const key=keyOf(n),st=configState[key];setStatus(key,'names','sending…');
    try {const res=await Api.setNodeAddress(n.ip,{bindIndex:n.bindIndex,shortName:st.shortName,longName:st.longName});setStatus(key,'names',resultText(res));}
    catch(e){setStatus(key,'names','error: '+e.message);}
  }

  async function saveAddressing(n) {
    const key = keyOf(n);
    const portStatus=(section,msg)=>setStatus(key,section,msg,portIndex);
    const portIndex=selectedPortIndex;
    const st = configState[key];
    const derived = deriveSelectedAddressing(n, st, selectedPortIndex);
    if (!derived.ok) {
      // REFUSE. Nothing is sent, nothing is clamped, no partial configuration
      // is written. A wrong universe here points a gateway's output at the
      // wrong fixtures on a show.
      portStatus('addressing', derived.message);
      renderAddressingPreview(n);
      return;
    }
    const body = {
      bindIndex: n.bindIndex, ...(st.ports.length<=1?{shortName:st.shortName,longName:st.longName}:{}),
      netSwitch: derived.netSwitch, subSwitch: derived.subSwitch,
      swIn: derived.swIn, swOut: derived.swOut,
    };
    portStatus('addressing', 'sending…');
    try {
      const res = await Api.setNodeAddress(n.ip, body);
      // Only now does the staged canonical value become the baseline the
      // "Staged — not sent yet" marker is measured against.
      derived.used.forEach(u => {
        const p = st.ports[u.i];
        if (u.dir === 'input') p.universeInApplied = u.canonical;
        else p.universeOutApplied = u.canonical;
      });
      if(key===selectedKey) updatePortDirtyMarkers(st);
      portStatus('addressing', resultText(res));
    } catch (e) {
      portStatus('addressing', 'error: ' + e.message);
    }
  }

  async function setMergeMode(n, portIndex, mode) {
    const key = keyOf(n);
    const portStatus=(section,msg)=>setStatus(key,section,msg,portIndex);
    const cmd = (mode === 'ltp' ? 'merge_ltp_' : 'merge_htp_') + portIndex;
    portStatus('merge', `setting port ${portIndex} to ${mode.toUpperCase()}…`);
    try {
      const res = await Api.setNodeAddress(n.ip, { bindIndex: n.bindIndex, command: cmd });
      portStatus('merge', `port ${portIndex} ${mode.toUpperCase()}: ` + resultText(res));
      return true;
    } catch (e) {
      portStatus('merge', 'error: ' + e.message);
      return false;
    }
  }

  async function setPortDirection(n, portIndex, direction) {
    const key = keyOf(n);
    const portStatus=(section,msg)=>setStatus(key,section,msg,portIndex);
    const word = direction === 'input' ? 'input' : 'output';
    if (!confirm(`Set port ${portIndex} on ${n.shortName || n.ip} to ${word}?${word === 'input' ? '\n\nThis also flushes that port’s subscriber list.' : ''}\n\nThis sends an ArtAddress command to the node now.`)) return;
    portStatus('direction', `setting port ${portIndex} to ${word}…`);
    try {
      const res = await Api.setNodeAddress(n.ip, { bindIndex: n.bindIndex, command: `direction_${word === 'input' ? 'rx' : 'tx'}_${portIndex}` });
      portStatus('direction', `port ${portIndex} ${word}: ` + resultText(res));
    } catch (e) {
      portStatus('direction', 'error: ' + e.message);
    }
  }

  async function setPortRDM(n, portIndex, enabled) {
    const key = keyOf(n);
    const portStatus=(section,msg)=>setStatus(key,section,msg,portIndex);
    const word = enabled ? 'enable' : 'disable';
    if (!confirm(`${enabled ? 'Enable' : 'Disable'} RDM on port ${portIndex} of ${n.shortName || n.ip}?\n\nThis sends an ArtAddress command to the node now.`)) return;
    portStatus('rdm', `${word} RDM on port ${portIndex}…`);
    try {
      const res = await Api.setNodeAddress(n.ip, { bindIndex: n.bindIndex, command: `rdm_${word}_${portIndex}` });
      portStatus('rdm', `port ${portIndex} RDM ${enabled ? 'enabled' : 'disabled'}: ` + resultText(res));
    } catch (e) {
      portStatus('rdm', 'error: ' + e.message);
    }
  }

  async function saveInputEnabled(n) {
    const key = keyOf(n);
    const portStatus=(section,msg)=>setStatus(key,section,msg,portIndex);
    const portIndex=selectedPortIndex;
    const st = configState[key];
    const p=st.ports.find(p=>p.index===selectedPortIndex);
    if(st.ports.length!==1 || !p || !p.input) {setStatus(key,'input','Nothing sent. ArtInput cannot safely change only this port.');return;}
    const enabled = [0, 1, 2, 3].map(i => i===p.index ? p.inputEnabled : false);
    portStatus('input', 'sending…');
    try {
      const res = await Api.setNodeInput(n.ip, { bindIndex: n.bindIndex, enabled });
      portStatus('input', resultText(res));
    } catch (e) {
      portStatus('input', 'error: ' + e.message);
    }
  }

  async function sendIPConfig(n) {
    const key = keyOf(n);
    const st = networkDraft(n);
    setStatus(key, 'ip', 'sending…');
    try {
      const res = await Api.setNodeIPConfig(n.ip, {
        bindIndex: n.bindIndex, ip: st.ip, mask: st.mask, gateway: st.gateway, dhcp: st.dhcp,
      });
      setStatus(key, 'ip', resultText(res));
    } catch (e) {
      setStatus(key, 'ip', 'error: ' + e.message);
    }
    st.ipArmed = false;
    st.ipApplied = false;
    renderIPConfirmArea(n);
  }

  function byId(id) { return document.getElementById(id); }

  function init() {
    document.getElementById('btnRefreshNodes').addEventListener('click', refresh);
    Live.on('node', () => refresh());
    // A display-base change re-renders the read-only universe displays from
    // their raw wire values, and rewrites the STAGED editor fields' text from
    // their canonical values — never from what is already in the box.
    window.addEventListener('b5-universe-base-changed', () => {
      render();
      syncUniverseFieldsToBase();
    });
    refresh();
  }

  // Test seam: the JS suites in static/js/testdata drive this screen through
  // its own handlers, exactly as the browser does, and need to seed the node
  // list without an HTTP round trip.
  return { init, refresh, __setNodesForTest: (v) => { nodes = v; }, __configStateForTest: () => configState, __deriveForTest: (st) => deriveAddressing(st), __deriveSelectedForTest: deriveSelectedAddressing, __selectPortForTest: selectPort, __networkDraftForTest: networkDraft };
})();

function escapeHtml(s) {
  if (s === undefined || s === null) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}
