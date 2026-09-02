// gdtfparse.js — pure XML parsing layer for GDTF fixture-type description
// files (Phase 2b MVR/GDTF patch import). No zip/network/DOM-document-
// loading concerns beyond DOMParser itself: this module takes the already-
// extracted description.xml text (mvrzip.js opens the outer .mvr and, on a
// .gdtf entry's bytes, the .gdtf-as-zip too — that plumbing lives in the
// orchestration layer built after this one) and returns a plain JS object.
//
// GDTF structure this parses (see task spec / GDTF standard):
//   <GDTF><FixtureType Manufacturer="..." Name="...">
//     <DMXModes><DMXMode Name="...">
//       <DMXChannels><DMXChannel Offset="1,2">
//         <LogicalChannel Attribute="Pan">
//           <ChannelFunction Name="Pan" Attribute="Pan" DMXFrom="0/1"
//                             PhysicalFrom="0" PhysicalTo="540">
//             <ChannelSet Name="Slot 1" DMXFrom="0/1"/>
//           </ChannelFunction>
//         </LogicalChannel>
//       </DMXChannels>
//     </DMXMode></DMXModes>
//   </FixtureType></GDTF>
//
// Footprint rule: footprint for a mode = the maximum RESOLVED offset across
// all its channel placements, where "resolved" accounts for GDTF's
// geometry-reference replication mechanism (pixel arrays / repeated cells),
// not just the literal <DMXChannel> list.
//
// Virtual channels and the footprint (the "does a channel with no function
// still take an address?" question, settled with evidence — see
// testdata/gdtf_footprint_test.js's "virtual channel" fixtures):
//
//   * A channel with an ATTRIBUTE of "NoFeature" but a real Offset (e.g.
//     <DMXChannel Offset="62"><LogicalChannel Attribute="NoFeature">) DOES
//     occupy DMX slot 62 and IS counted. Nothing in this module has ever
//     filtered a channel by its attribute name; parseOffsets is the only
//     thing that decides whether a channel places anything, and it looks at
//     Offset alone. "This channel has no function" and "this channel does
//     not exist" are correctly distinguished, and only the second shrinks a
//     footprint.
//   * A channel whose Offset is the literal "None", or absent entirely
//     (GDTF's own default for the attribute), is a VIRTUAL channel: per the
//     GDTF spec it has no DMX placement at all, and it is excluded. This is
//     not a heuristic — it is corroborated by real vendor data in the test
//     file: GLP JDC1's Modes 3/4/5/6 each declare a virtual channel
//     (Offset="") alongside their addressed ones, and each mode's own name
//     states its channel count (68/62/17/11). Those counts are matched
//     EXACTLY by excluding the virtual channel and are each off by one if it
//     is counted. Counting virtual channels would therefore break four
//     independently-corroborated vendor modes to "fix" one.
//
// Mode-name channel counts are used as a CROSS-CHECK ONLY (a warning when
// they disagree with the resolved footprint), never as parser input — see
// parseDeclaredChannelCount and its call site in parseMode.
//
// Why the literal list alone under-counts: GDTF lets a fixture define a
// geometry once (a "template", e.g. one RGBW pixel cell) and instantiate it
// many times via <GeometryReference Geometry="TemplateName"><Break
// DMXBreak="1" DMXOffset="N"/></GeometryReference> — one per physical
// instance, each at its own DMX offset. The template's own channel
// definitions appear ONCE in the mode's <DMXChannels> list (Geometry=
// "TemplateName"), using offsets that are LOCAL to one instance (1, 2, 3...
// for an RGBW cell's R/G/B/W bytes) — they are never real placements by
// themselves. Every actual instance's real offset is (that GeometryReference's
// <Break DMXOffset> - 1) + (the template channel's local offset). A naive
// max-of-literal-offsets read never looks at <GeometryReference>/<Break> at
// all, so it both (a) misses every replicated instance beyond whichever one
// happens to be the template's own local numbering, drastically under-
// counting footprint for any fixture with pixel arrays or repeated cells,
// and (b) can even let two unrelated geometries collide at the same small
// literal offset (a template's own 1/2 numbering stepping on a real
// geometry's actual channels 1/2 elsewhere in the same mode).
//
// resolveGeometryChannels below walks the <Geometries> tree starting from
// each mode's own root geometry (DMXMode/@Geometry), recursing through
// plain child geometries at a fixed offset and through each
// <GeometryReference> hop at an accumulated offset (composing nested
// references), and reads a geometry name's channel *definition* from the
// mode's own <DMXChannels> list only when the walk actually reaches that
// name — so a template name never contributes at its own bare literal
// offset unless the walk reaches it there directly (the common, correct
// case for a fixture with no <GeometryReference> elements at all, which
// this degrades to byte-for-byte). See that function's doc comment for the
// full algorithm and the cycle/depth guard.
//
// channelFunctions rule (Task 1, function-aware Rig Check foundation —
// deliberate simplification, same spirit as the footprint rule above): each
// mode's channelFunctions map is keyed by DMX offset (matching a
// patch.Entry.ChannelFunctions key 1:1 — see internal/patch/entry.go) and
// resolved from ONLY the first <LogicalChannel> and, within it, the first
// <ChannelFunction> of each <DMXChannel> — a channel with multiple
// ChannelFunctions (different behavior over different DMX sub-ranges, e.g.
// a mode-select control channel) collapses to that first function's
// Attribute/Name/range/ChannelSet data for every offset the DMXChannel
// spans (coarse AND any fine bytes get the same resolved attribute, mirroring
// how internal/rdm/slotinfo.go's SLOT_INFO coarse+fine pairs work — both
// paths feed the same taxonomy, see internal/patch/taxonomy.go). Every
// offset in a multi-offset DMXChannel (16-bit+) gets an identical
// ChannelFunction entry; the DMXTo bound for the channel's first function is
// the next ChannelFunction's DMXFrom minus one when a second one exists, or
// 255 (this module never reads a channel's actual bit depth from GDTF's
// <DMXChannel> — that's a further-out-of-scope refinement, not a correctness
// bug for the common single-ChannelFunction-per-channel case this covers).
const GdtfParse = (() => {
  function parseXml(xmlString) {
    const doc = new DOMParser().parseFromString(xmlString, 'application/xml');
    const perr = doc.getElementsByTagName('parsererror')[0];
    if (perr) {
      throw new Error('malformed GDTF XML: ' + perr.textContent.trim().split('\n')[0]);
    }
    return doc;
  }

  // childrenByTag: direct-child elements matching tagName (namespace-
  // agnostic — GDTF files in the wild are plain, unnamespaced XML, but we
  // don't assume the tag isn't prefixed by checking localName too).
  function childrenByTag(el, tagName) {
    const out = [];
    for (let i = 0; i < el.children.length; i++) {
      const c = el.children[i];
      if (c.tagName === tagName || c.localName === tagName) out.push(c);
    }
    return out;
  }

  function childByTag(el, tagName) {
    return childrenByTag(el, tagName)[0] || null;
  }

  // descendantsByTag: any-depth search, used for DMXChannel's Offset
  // attribute and the LogicalChannel/ChannelFunction descent below — GDTF's
  // DMXChannel can (per spec) point at a geometry-tree channel via
  // <LogicalChannel>/<ChannelFunction>, but for this phase's purposes we
  // just want every ChannelFunction under a given DMXChannel regardless of
  // exact nesting depth.
  function descendantsByTag(el, tagName) {
    const out = [];
    const stack = [el];
    while (stack.length) {
      const cur = stack.pop();
      for (let i = 0; i < cur.children.length; i++) {
        const c = cur.children[i];
        if (c.tagName === tagName || c.localName === tagName) out.push(c);
        stack.push(c);
      }
    }
    return out;
  }

  // parseOffsets: GDTF's Offset attribute is a comma-separated list of
  // 1-based byte offsets ("1" for 8-bit, "1,2" for 16-bit, etc.), or the
  // literal string "None" for a channel with no DMX footprint impact.
  // Returns [] for "None"/missing so callers can uniformly skip it when
  // computing footprint.
  function parseOffsets(offsetAttr) {
    if (!offsetAttr) return [];
    const trimmed = offsetAttr.trim();
    if (trimmed === '' || trimmed.toLowerCase() === 'none') return [];
    return trimmed
      .split(',')
      .map(s => parseInt(s.trim(), 10))
      .filter(n => Number.isFinite(n));
  }

  // MAX_DMX_BYTES: sanity clamp on a GDTF "X/Y" value's declared byte count.
  // GDTF permits 1-4 (8/16/24/32-bit); anything outside that is a malformed
  // file and is clamped rather than carried forward as a nonsense byte count
  // (this module's "never crash, degrade" posture).
  const MAX_DMX_BYTES = 4;

  // parseDmxValueParts: the ONE parser for GDTF's "X/Y" DMX-value notation
  // (raw value / byte count), e.g. "0/1", "128/1", "32768/2". Used by
  // DMXFrom (ChannelFunction and ChannelSet) and by Default/Highlight.
  // Returns { present, value, byteCount }:
  //
  //   present   — the attribute was actually there and parsed. This is the
  //               field that makes "the file said 0" distinguishable from
  //               "the file said nothing", which for Default is the whole
  //               point: a resting value of 0 is real data (a dimmer at
  //               zero), and a consumer that cannot tell it from "unknown"
  //               cannot decide whether to drive the channel at all.
  //   value     — the raw value X, verbatim. For byteCount 1 that is a plain
  //               0-255 DMX byte; for byteCount 2 it is the full 16-bit
  //               value (0-65535), NOT a coarse byte — see the multi-byte
  //               note above parseChannelFunction.
  //   byteCount — Y, clamped to 1..MAX_DMX_BYTES, defaulting to 1 when the
  //               notation carries no "/Y" part at all.
  function parseDmxValueParts(s) {
    if (s === null || s === undefined) return { present: false, value: 0, byteCount: 1 };
    const str = String(s).trim();
    if (str === '') return { present: false, value: 0, byteCount: 1 };
    const slash = str.indexOf('/');
    const n = parseInt(slash >= 0 ? str.slice(0, slash) : str, 10);
    if (!Number.isFinite(n)) return { present: false, value: 0, byteCount: 1 };
    let byteCount = 1;
    if (slash >= 0) {
      const b = parseInt(str.slice(slash + 1), 10);
      if (Number.isFinite(b) && b >= 1) byteCount = Math.min(b, MAX_DMX_BYTES);
    }
    return { present: true, value: n, byteCount };
  }

  // parseDmxValue: the pre-existing raw-value-only reading of "X/Y", kept
  // as the DMXFrom accessor it always was so DMXFrom/DMXTo's units are
  // unchanged by this file's Default/Highlight work. Delegates to
  // parseDmxValueParts — there is exactly one "X/Y" parser in this module.
  function parseDmxValue(s) {
    return parseDmxValueParts(s).value;
  }

  // Multi-byte (16-bit+) note. value + byteCount together are what make a
  // Default meaningful for BOTH bytes of a coarse+fine channel, without this
  // module having to pre-decompose anything: every offset a multi-offset
  // DMXChannel spans receives an IDENTICAL channelFunction record (see this
  // file's channelFunctions rule), so a consumer recovers its own byte from
  // its position i in that channel's ascending (coarse, fine) offsets —
  // the ordering internal/patch/resolve.go's ResolvedFunction.Offsets and
  // testpattern.go's "16-bit (coarse+fine)" design note both already fix:
  //
  //     byte(i) = (value >>> (8 * (byteCount - 1 - i))) & 0xff
  //
  // e.g. Offset="5,6" with Default="32768/2" -> value 32768, byteCount 2 ->
  // 128 at offset 5, 0 at offset 6. This mirrors GDTF's own "X/Y" encoding
  // rather than inventing a derived array, and keeps the record field-for-
  // field identical to patch.ChannelFunction's Default/DefaultByteCount
  // (internal/patch/entry.go), whose doc comment records the same formula.

  function parseFloatAttr(s) {
    if (!s) return 0;
    const n = parseFloat(s);
    return Number.isFinite(n) ? n : 0;
  }

  function parseChannelSet(csEl) {
    return {
      name: csEl.getAttribute('Name') || '',
      dmxFrom: parseDmxValue(csEl.getAttribute('DMXFrom')),
      physicalFrom: parseFloatAttr(csEl.getAttribute('PhysicalFrom')),
      physicalTo: parseFloatAttr(csEl.getAttribute('PhysicalTo')),
    };
  }

  // parseChannelFunction: one <ChannelFunction>. Beyond the range/name data
  // this always carried, it reads the two GDTF "resting value" attributes
  // Rig Check needs in order to make a fixture actually emit light while a
  // single channel is under test (a real fixture needs BOTH its dimmer up
  // AND its shutter in the open position; sending 0 to every untested
  // channel guarantees darkness):
  //
  //   Default   — <ChannelFunction Default="X/Y">, the value the fixture
  //               rests at for this function.
  //   Highlight — <ChannelFunction Highlight="X/Y">, GDTF's optional
  //               "highlight/locate" value, present on far fewer files.
  //
  // Both are carried as an explicit has*/value/*Bytes triple rather than a
  // bare number, because 0 is a perfectly real Default (see
  // parseDmxValueParts' `present` doc) and a bare 0 would be
  // indistinguishable from "this file didn't say".
  function parseChannelFunction(cfEl) {
    const def = parseDmxValueParts(cfEl.getAttribute('Default'));
    const hi = parseDmxValueParts(cfEl.getAttribute('Highlight'));
    return {
      name: cfEl.getAttribute('Name') || '',
      attribute: cfEl.getAttribute('Attribute') || '',
      dmxFrom: parseDmxValue(cfEl.getAttribute('DMXFrom')),
      physicalFrom: parseFloatAttr(cfEl.getAttribute('PhysicalFrom')),
      physicalTo: parseFloatAttr(cfEl.getAttribute('PhysicalTo')),
      hasDefault: def.present,
      defaultValue: def.value,
      defaultByteCount: def.present ? def.byteCount : 0,
      hasHighlight: hi.present,
      highlightValue: hi.value,
      highlightByteCount: hi.present ? hi.byteCount : 0,
      channelSets: childrenByTag(cfEl, 'ChannelSet').map(parseChannelSet),
    };
  }

  function parseLogicalChannel(lcEl) {
    const functions = descendantsByTag(lcEl, 'ChannelFunction').map(parseChannelFunction);
    return {
      attribute: lcEl.getAttribute('Attribute') || '',
      functions,
    };
  }

  function parseDmxChannel(chEl) {
    const offsets = parseOffsets(chEl.getAttribute('Offset'));
    const logicalChannels = childrenByTag(chEl, 'LogicalChannel').map(parseLogicalChannel);
    return { offsets, logicalChannels };
  }

  // resolveChannelFunction: the channelFunctions simplification rule from
  // this file's doc comment — first LogicalChannel, first ChannelFunction
  // within it. Returns null when the DMXChannel has no LogicalChannel/
  // ChannelFunction at all (a channel this module can say nothing about —
  // callers must NOT invent a Source==gdtf entry for it, an absent slot
  // stays absent per internal/patch/entry.go's ChannelFunctionSource rule).
  function resolveChannelFunction(logicalChannels) {
    const lc = logicalChannels[0];
    if (!lc) return null;
    const fn = (lc.functions && lc.functions[0]) || null;
    const nextFn = (lc.functions && lc.functions[1]) || null;
    return {
      attribute: lc.attribute || (fn ? fn.attribute : ''),
      functionName: fn ? fn.name : '',
      dmxFrom: fn ? fn.dmxFrom : 0,
      dmxTo: nextFn ? Math.max(nextFn.dmxFrom - 1, fn ? fn.dmxFrom : 0) : 255,
      physicalFrom: fn ? fn.physicalFrom : 0,
      physicalTo: fn ? fn.physicalTo : 0,
      // Default/Highlight: absent (has* false) whenever there is no
      // resolvable ChannelFunction at all, which is the same "the file
      // didn't say" state a ChannelFunction carrying no Default attribute
      // produces — never a fabricated 0.
      hasDefault: fn ? fn.hasDefault : false,
      defaultValue: fn ? fn.defaultValue : 0,
      defaultByteCount: fn ? fn.defaultByteCount : 0,
      hasHighlight: fn ? fn.hasHighlight : false,
      highlightValue: fn ? fn.highlightValue : 0,
      highlightByteCount: fn ? fn.highlightByteCount : 0,
      channelSets: fn ? fn.channelSets : [],
    };
  }

  // GEOMETRY_REFERENCE_TAG: the one tag in <Geometries> that isn't a
  // geometry itself but a pointer at one, elsewhere in the tree.
  const GEOMETRY_REFERENCE_TAG = 'GeometryReference';

  // buildGeometryIndex: Name -> element for every geometry node under
  // <Geometries>, at any depth, EXCLUDING <GeometryReference> nodes
  // themselves (a reference's own Name, e.g. "RGBW Pixel 20", is never
  // looked up — only the template its Geometry attribute points at is).
  // First element with a given Name wins (GDTF requires unique geometry
  // names within a fixture type; a malformed file that violates this just
  // gets whichever occurrence buildGeometryIndex saw first, not an error —
  // consistent with this module's "never crash, degrade" posture).
  function buildGeometryIndex(geometriesEl) {
    const index = new Map();
    if (!geometriesEl) return index;
    const stack = [geometriesEl];
    while (stack.length) {
      const cur = stack.pop();
      for (let i = 0; i < cur.children.length; i++) {
        const c = cur.children[i];
        const tag = c.tagName || c.localName;
        const nm = c.getAttribute('Name');
        if (tag !== GEOMETRY_REFERENCE_TAG && nm && !index.has(nm)) index.set(nm, c);
        stack.push(c);
      }
    }
    return index;
  }

  // MAX_GEOMETRY_DEPTH: recursion-depth backstop for the geometry-tree walk
  // below. A real fixture never nests more than a handful of levels deep;
  // this exists purely so a malformed/cyclic GDTF can never hang or
  // stack-overflow the browser tab even if the visited-name cycle guard
  // somehow doesn't catch it (e.g. a reference chain that keeps
  // introducing new names without ever truly repeating one).
  const MAX_GEOMETRY_DEPTH = 200;

  // resolveGeometryChannels: walks the geometry tree reachable from rootEl
  // (a mode's DMXMode/@Geometry, looked up in geometryIndex by the caller)
  // and returns every { offset, ch } placement this mode's fixture actually
  // uses — including every instance a <GeometryReference> replicates (see
  // this file's top doc comment for why that's necessary and how offsets
  // compose).
  //
  // channelsByGeometryName is this mode's own literal <DMXChannel> list,
  // grouped by the Geometry name each entry declares. Whether a given name's
  // channels end up being a real, once-only placement (the mode's root
  // geometry, or a plain descendant reached by direct child traversal, both
  // at offsetBase 0) or a template replicated N times (reached only via one
  // or more <GeometryReference> hops, offsetBase = the accumulated
  // (breakOffset-1) sum) is decided ENTIRELY by how this walk reaches that
  // name — nothing needs to classify a name as "a template" up front. A
  // geometry that's never referenced anywhere just gets contributed once, at
  // offsetBase 0, wherever the direct-child walk finds it: byte-for-byte the
  // pre-fix behavior for a fixture with no <GeometryReference> elements at
  // all.
  //
  // Multi-break: a <GeometryReference> with more than one <Break> (a
  // multi-universe fixture, one break per DMX universe the geometry spans)
  // picks DMXBreak="1" if present, else the lowest numeric break — same
  // "lowest break" convention mvrparse.js's address resolution already uses
  // for a fixture's own <Addresses>, kept coherent here rather than
  // inventing a second rule — and records a warning naming the ones it
  // didn't use. None of this project's 5 real sample GDTFs exercise
  // multi-break GeometryReferences (checked directly against the ground
  // truth), so this path is implemented per the GDTF spec and kept coherent
  // with the existing convention, but is UNVERIFIED against a real
  // multi-break file.
  //
  // Cycle guard: a Set of geometry names already in the current reference
  // chain is threaded through the recursion; a <GeometryReference> whose
  // target is already an ancestor in that chain is skipped (with a warning)
  // rather than followed — see MAX_GEOMETRY_DEPTH above for the backstop.
  //
  // Pure-array-container guard (regression fix — see this file's top doc
  // comment history): a <Geometry> node can legitimately be BOTH (a) a pixel/
  // element-array container whose ENTIRE content is <GeometryReference>
  // children replicating one template (the pattern this whole function
  // exists to expand — see e.g. "RGBW Cells" below), AND (b) itself carry a
  // literal <DMXChannel> entry of its own in this mode, when the mode wants
  // to expose that array as ONE unified group control (a strobe/dimmer
  // module for the whole array) rather than per-instance addressing.
  // Checked against a real vendor GDTF (Elation Proteus Rayzor 1960,
  // "Extended Pan540/Tilt270" mode): "Spark LED Strobe Module" carries its
  // own literal Shutter1/Dimmer <DMXChannel> entries (real offsets 98, 99-
  // 100) and its only children are 76 <GeometryReference> elements to a
  // per-LED "Spark LED Pixel" template that ALSO has its own literal
  // <DMXChannel> entry (offset 1, local). Expanding those 76 references
  // pushes the resolved footprint out to 176; the fixture's actual DMX
  // address spacing in a real show (independently measured from Vectorworks'
  // back-to-back patch addressing) is 100 — the module-level group control
  // IS the real, addressed control surface for this mode, and the per-LED
  // template's own channel entry is leftover GDTF metadata (needed by this
  // fixture's separate pixel-mapping "Pixels" mode, which addresses this
  // same template through a different, non-wrapped reference) rather than a
  // second, independently-addressed layer for THIS mode.
  //
  // The signal that distinguishes this from a genuine array-needs-expanding
  // case (e.g. this same fixture's "RGBW Cells", which has no literal
  // channel of its own and must be expanded, or its "Standard" mode's
  // "SparkLEDs_Standard" reference, whose direct parent "Head_Standard" has
  // its own literal channels too but is NOT a pure array container — it also
  // holds an unrelated <Beam> child, so its one <GeometryReference> is left
  // to expand normally) is structural, not name-based: a node blocks its OWN
  // <GeometryReference> children from expanding only when BOTH (1) it
  // contributed at least one real (non-"None") placement of its own in this
  // mode, AND (2) every one of its children is a <GeometryReference> — i.e.
  // it has no role in the tree other than being a replication wrapper, so a
  // real channel declared on it is unambiguously a claim on the whole
  // wrapped group, not a sibling detail alongside other unrelated geometry.
  function resolveGeometryChannels(rootEl, channelsByGeometryName, geometryIndex, warnings) {
    const resolved = [];

    // contribute: pushes name's literal channels (if this mode declares
    // any) at offsetBase, and reports whether it actually pushed a real
    // (non-"None"/non-empty) placement — the signal the pure-array-
    // container guard above needs; a name with only a "None"-offset entry
    // (e.g. a virtual/relation-master channel) reports false, same as a
    // name with no entry at all.
    function contribute(name, offsetBase) {
      const chs = channelsByGeometryName.get(name);
      if (!chs) return false;
      let contributedReal = false;
      chs.forEach(ch => {
        ch.offsets.forEach(localOffset => {
          resolved.push({ offset: localOffset + offsetBase, ch });
          contributedReal = true;
        });
      });
      return contributedReal;
    }

    function chooseBreak(refEl, breaks) {
      let chosen = breaks.find(b => b.brk === '1');
      if (!chosen) {
        const sorted = breaks.slice().sort((a, b) => {
          const an = parseInt(a.brk, 10), bn = parseInt(b.brk, 10);
          return (Number.isFinite(an) ? an : Infinity) - (Number.isFinite(bn) ? bn : Infinity);
        });
        chosen = sorted[0];
        if (breaks.length > 1) {
          warnings.push(
            `GeometryReference "${refEl.getAttribute('Name') || '(unnamed)'}": multiple <Break> elements ` +
            `(breaks ${breaks.map(b => b.brk).join(', ')}) — used the lowest break (${chosen.brk}) for footprint ` +
            `resolution, matching the MVR importer's own lowest-break convention.`
          );
        }
      }
      return chosen;
    }

    function walk(el, offsetBase, visited, depth) {
      if (depth > MAX_GEOMETRY_DEPTH) {
        warnings.push(`geometry tree exceeds ${MAX_GEOMETRY_DEPTH} levels at "${el.getAttribute('Name') || '(unnamed)'}" — stopped descending (possible malformed/cyclic GDTF).`);
        return;
      }
      const name = el.getAttribute('Name');
      const contributedReal = name ? contribute(name, offsetBase) : false;

      // pure-array-container guard: only engages when this node itself has
      // real channels of its own AND every child is a <GeometryReference>
      // AND (regression fix — see doc comment history) there is more than
      // one such reference and they all target the SAME geometry name — see
      // the doc comment above resolveGeometryChannels for the real-file
      // case this fixes and why the check is this specific.
      //
      // Why the same-target/count check was added: "every child is a
      // <GeometryReference>" alone is NOT enough to mean "this is a
      // replicated array". GLP JDC1's own vendor file (Mode 1/2/5/6 —
      // ground truth from the fixture's declared channel counts) composes
      // its head geometry ("Head M1" etc.) purely out of <GeometryReference>
      // children too, but each one targets a DIFFERENT shared template
      // ("Beam Module", "Plate Module", "Background Plate" — reused across
      // the fixture's 6 head variants rather than being duplicated). That's
      // plain tree composition via indirection, not per-instance
      // replication, and blocking it dropped those templates' real literal
      // channels entirely (Mode 1 collapsed from 14ch to 7ch, Mode 2 from
      // 23ch to 15ch, etc. — this was the regression). A genuine replicated
      // array (Rayzor's 76 "Spark LED Strobe Module" refs, JDC1's own 12
      // "Single Back Plate"/"Single White Beam" pixel refs) always has
      // multiple references to the *same* target name — that's the actual,
      // structural signal for "array", not merely "all children are
      // references".
      let blockReferenceChildren = false;
      if (contributedReal && el.children.length > 0) {
        blockReferenceChildren = true;
        let sameTargetCount = 0;
        let firstTarget = null;
        for (let i = 0; i < el.children.length; i++) {
          const childEl = el.children[i];
          const t = childEl.tagName || childEl.localName;
          if (t !== GEOMETRY_REFERENCE_TAG) { blockReferenceChildren = false; break; }
          const tgt = childEl.getAttribute('Geometry') || '';
          if (firstTarget === null) firstTarget = tgt;
          if (tgt === firstTarget) sameTargetCount++;
        }
        if (blockReferenceChildren && (sameTargetCount !== el.children.length || el.children.length < 2)) {
          blockReferenceChildren = false;
        }
        if (blockReferenceChildren) {
          warnings.push(
            `Geometry "${name}" has its own DMX channel(s) in this mode and is a pure ` +
            `per-instance array container (every child is a <GeometryReference>) — treated as a ` +
            `single addressed group; its ${el.children.length} replicated instance(s) were not expanded ` +
            `for footprint/channelFunctions (matches real DMX address spacing).`
          );
        }
      }

      for (let i = 0; i < el.children.length; i++) {
        const c = el.children[i];
        const tag = c.tagName || c.localName;
        if (tag !== GEOMETRY_REFERENCE_TAG) {
          walk(c, offsetBase, visited, depth + 1);
          continue;
        }
        if (blockReferenceChildren) continue;
        const refName = c.getAttribute('Geometry') || '';
        const breakEls = childrenByTag(c, 'Break');
        if (!breakEls.length) continue; // no placement info for this reference — nothing to resolve
        const breaks = breakEls.map(b => ({ brk: b.getAttribute('DMXBreak') || '', off: b.getAttribute('DMXOffset') || '' }));
        const chosen = chooseBreak(c, breaks);
        const breakOffsetNum = parseInt(chosen.off, 10);
        if (!Number.isFinite(breakOffsetNum)) continue;
        if (visited.has(refName)) {
          warnings.push(`GeometryReference "${c.getAttribute('Name') || '(unnamed)'}" -> "${refName}": reference cycle detected — skipped.`);
          continue;
        }
        const target = geometryIndex.get(refName);
        if (!target) {
          warnings.push(`GeometryReference "${c.getAttribute('Name') || '(unnamed)'}" targets unknown geometry "${refName}" — skipped.`);
          continue;
        }
        const nextVisited = new Set(visited);
        nextVisited.add(refName);
        walk(target, offsetBase + (breakOffsetNum - 1), nextVisited, depth + 1);
      }
    }

    if (rootEl) walk(rootEl, 0, new Set(), 0);
    return resolved;
  }

  // DECLARED_CHANNEL_COUNT_RE: the "(62ch)" / "62 Ch" / "62-Channel" shapes
  // GDTF mode names use to state their own channel count. Anchored on the
  // "ch" token so a bare number in a mode name ("Mode 4", "Pan540/Tilt270",
  // "Standard 16bit") is never mistaken for a channel count. The LAST match
  // wins: names like "Mode 4 SPix PRO (62ch)" put the count at the end,
  // after other digits that are not counts.
  const DECLARED_CHANNEL_COUNT_RE = /(\d+)\s*(?:-\s*)?(?:ch|chan|channel)s?\b/gi;

  // parseDeclaredChannelCount: the channel count a mode NAME states about
  // itself, or null when the name states none. See the call site in
  // parseMode for why this is only ever a cross-check, never parser input.
  function parseDeclaredChannelCount(modeName) {
    if (!modeName) return null;
    let found = null;
    DECLARED_CHANNEL_COUNT_RE.lastIndex = 0;
    let m;
    while ((m = DECLARED_CHANNEL_COUNT_RE.exec(modeName)) !== null) {
      const n = parseInt(m[1], 10);
      if (Number.isFinite(n) && n > 0) found = n;
    }
    return found;
  }


  // --- Vectorworks placeholder-profile detection -------------------------
  //
  // WHAT THIS IS FOR. An MVR exported from Vectorworks can carry a
  // GENERATED PLACEHOLDER .gdtf in place of the real vendor file — a
  // synthetic fixture type whose only mode is a flat, dense run of
  // single-byte channels with no personality structure at all. It parses
  // perfectly, resolves a footprint, and is wrong: the owner's own show
  // file carried placeholders for two types at 61 and 23 channels where
  // the real vendor files are 62 and 24, and he patched the whole rig one
  // channel short per fixture. Nothing downstream can detect that — the
  // file is internally consistent — so the only defence is to say so at
  // import time, through the same `warnings` channel the mode-name
  // cross-check and the geometry-resolution notes already use.
  //
  // FOOTPRINT IS NOT TOUCHED. Like parseDeclaredChannelCount's cross-check,
  // this only ever ADDS a warning. The resolved footprint is what the file
  // actually states, and guessing a "corrected" one from a placeholder is
  // strictly worse than telling a human to fetch the real GDTF.
  //
  // THE DETECTION CRITERION, and why it cannot fire on a legitimate simple
  // fixture. A mode is flagged only when ALL THREE of the following hold,
  // each of which is independently sufficient to exclude a real vendor
  // mode:
  //
  //   1. MANUFACTURER is exactly "Custom" (case/whitespace-folded).
  //      GDTF's Manufacturer is the vendor: Elation, GLP, Martin, Robe,
  //      Chauvet. "Custom" is what a generator stamps on a fixture type it
  //      invented because it had no vendor file to copy. A real vendor
  //      file never says this.
  //   2. The MODE NAME is generic — "DMX Mode", "Default", "Mode",
  //      "Standard", "Basic", "Normal" and nothing else. Real personality
  //      names identify the personality, and overwhelmingly state their own
  //      channel count: "Cells 24CH", "RGB 3CH", "8bit 4CH",
  //      "Mode 2 Normal (23ch)". A name carrying ANY digit is never
  //      generic by this rule, so every count-bearing vendor mode name is
  //      excluded outright.
  //   3. The mode is STRUCTURALLY FLAT AND LARGE: at least
  //      PLACEHOLDER_MIN_CHANNELS placed channels, not one of which spans
  //      more than a single DMX offset (no 16-bit coarse+fine pair
  //      anywhere), and the resolved offsets are exactly the dense run
  //      1..N with N === the number of placed channels (so no geometry
  //      replication, no gaps, no virtual channels). A fixture with 12+
  //      channels and not a single 16-bit attribute, no repeated cell
  //      geometry, and no gaps is a generated list, not an engineered
  //      personality.
  //
  // Against the concrete false positive that matters — the real Elation
  // Paladin Cube's genuine "RGB 3CH" and "8bit 4CH" modes, which ARE flat,
  // dense and all-8-bit — all three guards fail independently: the
  // manufacturer is "Elation", the names carry digits and a personality,
  // and 3 and 4 channels are far below the 12-channel floor. The same is
  // true of any real 4-channel LED par: even one published by a vendor
  // literally named "Custom" under a mode literally named "Default", four
  // channels cannot reach the floor. The floor is set at 12 because that
  // is comfortably above every legitimately-tiny personality (RGB, RGBW,
  // RGBA+dimmer, CMY) while far below the 23 of the smaller of the two
  // real placeholders — a miss on some hypothetical 8-channel placeholder
  // is the deliberate trade, because a warning that cries wolf on a real
  // fixture trains the owner to ignore every warning this parser emits.
  const PLACEHOLDER_MANUFACTURER = 'custom';
  const PLACEHOLDER_GENERIC_MODE_NAMES = ['dmx mode', 'default', 'mode', 'standard', 'basic', 'normal'];
  const PLACEHOLDER_MIN_CHANNELS = 12;

  function foldName(s) {
    return String(s || '').trim().toLowerCase().replace(/\s+/g, ' ');
  }

  // isFlatGeneratedMode: guard 3 above, computed from the mode's own
  // resolved output — channels[].offsets are the literal <DMXChannel>
  // offsets and footprint is the MAX RESOLVED offset, so footprint ===
  // channel count together with a dense literal 1..N run also proves no
  // geometry replication contributed anything.
  function isFlatGeneratedMode(mode) {
    const placed = mode.channels.filter(ch => ch.offsets.length > 0);
    if (placed.length < PLACEHOLDER_MIN_CHANNELS) return false;
    if (placed.length !== mode.channels.length) return false; // a virtual channel is structure
    if (placed.some(ch => ch.offsets.length !== 1)) return false; // any 16-bit channel is structure
    if (mode.footprint !== placed.length) return false; // replication or gaps
    const seen = new Set();
    placed.forEach(ch => seen.add(ch.offsets[0]));
    if (seen.size !== placed.length) return false;
    for (let i = 1; i <= placed.length; i++) if (!seen.has(i)) return false;
    return true;
  }

  // detectPlaceholderProfiles pushes one warning per flagged mode. Returns
  // nothing; it only ever appends to warnings.
  function detectPlaceholderProfiles(manufacturer, model, modes, warnings) {
    if (foldName(manufacturer) !== PLACEHOLDER_MANUFACTURER) return;
    modes.forEach(mode => {
      if (PLACEHOLDER_GENERIC_MODE_NAMES.indexOf(foldName(mode.name)) < 0) return;
      if (!isFlatGeneratedMode(mode)) return;
      warnings.push(
        `mode "${mode.name}" of "${manufacturer} ${model}" looks like a GENERATED PLACEHOLDER ` +
        `profile, not a real vendor GDTF: manufacturer "${manufacturer}", a generic mode name, and ` +
        `${mode.footprint} channels in one flat 8-bit run with no personality structure. Its ` +
        `${mode.footprint}-channel footprint is used as stated (nothing here guesses a correction), ` +
        `but placeholders are commonly off by a channel or more against the real fixture — patch ` +
        `this type from the manufacturer's own GDTF and re-profile these entries before trusting ` +
        `the addressing.`
      );
    });
  }

  function parseMode(modeEl, geometryIndex, fixtureWarnings) {
    const name = modeEl.getAttribute('Name') || '';
    const channelsContainer = childByTag(modeEl, 'DMXChannels');
    const channelEls = channelsContainer ? childrenByTag(channelsContainer, 'DMXChannel') : [];
    const channels = channelEls.map(parseDmxChannel);

    // channelsByGeometryName: this mode's own literal DMXChannel list,
    // grouped by the Geometry name each entry declares — the data
    // resolveGeometryChannels reads a name's channel definition from,
    // whichever offsetBase the tree walk reaches that name at.
    const channelsByGeometryName = new Map();
    channelEls.forEach((chEl, i) => {
      const geomName = chEl.getAttribute('Geometry') || '';
      if (!channelsByGeometryName.has(geomName)) channelsByGeometryName.set(geomName, []);
      channelsByGeometryName.get(geomName).push(channels[i]);
    });

    const modeWarnings = [];
    const rootGeomName = modeEl.getAttribute('Geometry') || '';
    const rootEl = rootGeomName ? geometryIndex.get(rootGeomName) : null;

    let placements;
    if (rootEl) {
      placements = resolveGeometryChannels(rootEl, channelsByGeometryName, geometryIndex, modeWarnings);
    } else {
      // No usable geometry tree for this mode (missing <Geometries>
      // altogether, or DMXMode/@Geometry doesn't resolve to anything in
      // it) — fall back to the pre-fix behavior: every literal DMXChannel
      // entry at its own absolute offset, no replication. Still exactly
      // correct for a fixture with no <GeometryReference> elements at all;
      // just unable to expand pixel-array-style replication without a
      // geometry tree to walk.
      if (rootGeomName) {
        modeWarnings.push(`mode "${name}": root geometry "${rootGeomName}" not found in <Geometries> — falling back to the literal DMXChannel list (no replication resolved).`);
      }
      placements = [];
      channels.forEach(ch => { ch.offsets.forEach(off => placements.push({ offset: off, ch })); });
    }

    let footprint = 0;
    placements.forEach(p => { if (p.offset > footprint) footprint = p.offset; });

    // INVARIANT (regression backstop — see this file's top doc comment and
    // the task report this was written against): geometry resolution may
    // only ever ADD to the footprint, never remove from it. A literal
    // <DMXChannel Offset="N"> in the mode's own list is a stated fact from
    // the file — the fixture unambiguously uses DMX byte N in this mode —
    // and no amount of tree-walking (a walk that fails to reach a channel's
    // declared Geometry name, a guard that wrongly blocks a reference, a
    // future bug of the same shape) may cause the resolved footprint to
    // fall below it. This is a floor, not a substitute for a correct walk:
    // it protects the footprint NUMBER, but a walk that drops channels
    // still corrupts channelFunctions (Rig Check's per-offset attribute
    // map) for the dropped channels even while this floor keeps the
    // overall footprint looking right — see resolveGeometryChannels' doc
    // comment for the actual walk fix this regression needed.
    let maxLiteralOffset = 0;
    channels.forEach(ch => { ch.offsets.forEach(off => { if (off > maxLiteralOffset) maxLiteralOffset = off; }); });
    if (maxLiteralOffset > footprint) footprint = maxLiteralOffset;

    // Declared-channel-count cross-check (footprint dispute, Task 2). A GDTF
    // mode name very often states its own channel count ("Mode 4 SPix PRO
    // (62ch)"). That is an INDEPENDENT oracle — it comes from the fixture
    // vendor, not from this walk — and when it disagrees with the resolved
    // footprint, one of the two is wrong and a human needs to know.
    //
    // It is deliberately NOT parser input. Overriding a structural walk with
    // a number scraped out of free text would be its own bug, and a worse
    // one: the name is unvalidated, untyped, frequently absent, occasionally
    // stale (vendors rename modes and forget the count), and a fixture
    // patched at a footprint no channel in the file actually occupies is
    // silently wrong on the wire in a way nothing downstream can detect. So
    // the resolved footprint stands and this only ever adds a warning, which
    // mvrimport.js already surfaces to the tech per GDTF file.
    const declared = parseDeclaredChannelCount(name);
    if (declared !== null && declared !== footprint) {
      modeWarnings.push(
        `mode "${name}": the mode name declares ${declared} channel(s) but the file's own ` +
        `<DMXChannels>/<Geometries> structure resolves to ${footprint}. The resolved value is ` +
        `used (a mode name is free text, never authoritative over the structure); check this ` +
        `fixture's patched footprint by hand.`
      );
    }

    // channelFunctions: keyed by RESOLVED offset (matches
    // patch.Entry.ChannelFunctions 1:1 — see the file doc comment's
    // channelFunctions rule). An attribute-less resolution
    // (resolveChannelFunction returned null, or resolved with an empty
    // attribute) contributes nothing — this map only ever holds real,
    // resolved GDTF data, never a placeholder "absent" entry.
    const channelFunctions = {};
    placements.forEach(p => {
      const resolved = resolveChannelFunction(p.ch.logicalChannels);
      if (!resolved || !resolved.attribute) return;
      channelFunctions[p.offset] = {
        source: 'gdtf',
        attribute: resolved.attribute,
        functionName: resolved.functionName,
        dmxFrom: resolved.dmxFrom,
        dmxTo: resolved.dmxTo,
        physicalFrom: resolved.physicalFrom,
        physicalTo: resolved.physicalTo,
        // hasDefault/hasHighlight are the "the file actually said this"
        // flags — see parseDmxValueParts; a resting value of 0 is real data
        // and must never be confused with an absent one. defaultByteCount
        // is GDTF's "X/Y" byte count, which is what keeps a 16-bit default
        // meaningful for the fine byte too — see the multi-byte note above
        // parseChannelFunction for the exact per-offset formula.
        //
        // KEY NAMES HERE ARE THE WIRE CONTRACT, NOT THIS FILE'S INTERNAL
        // SHAPE. This map is sent VERBATIM as entryRequest.channelFunctions
        // (patch.js's runGdtfApply -> PUT /api/patch/entries/{id};
        // mvrimport.js -> POST /api/patch/import), and internal/web's
        // decodeJSON calls DisallowUnknownFields — so every key must be
        // spelled exactly as internal/web/patch.go's channelFunctionRequest
        // (and the JSON tags on patch.ChannelFunction) spells it, or the
        // WHOLE request is rejected with a 400. That spelling is `default`
        // and `highlight` — NOT the defaultValue/highlightValue names
        // parseChannelFunction uses for its own intermediate parse tree
        // above, which never leaves the browser. Emitting the intermediate
        // names here made every GDTF apply and every MVR import that
        // resolved even one channel function fail outright with
        // `json: unknown field "defaultValue"`.
        hasDefault: resolved.hasDefault,
        default: resolved.defaultValue,
        defaultByteCount: resolved.defaultByteCount,
        hasHighlight: resolved.hasHighlight,
        highlight: resolved.highlightValue,
        highlightByteCount: resolved.highlightByteCount,
        channelSets: resolved.channelSets,
      };
    });

    if (modeWarnings.length) fixtureWarnings.push(...modeWarnings);
    return { name, footprint, channels, channelFunctions };
  }

  // parseDescriptionXml(xmlString) -> {
  //   manufacturer: string,
  //   model: string,
  //   fixtureType: string,   // "Manufacturer Model", same convention as
  //                          // patch.go's handlePatchAdopt
  //                          // (fixtureType := strings.TrimSpace(mfr+" "+model))
  //   modes: [{ name, footprint, channels: [{ offsets: number[],
  //             logicalChannels: [{ attribute, functions: [{name,attribute,
  //             dmxFrom,physicalFrom,physicalTo,hasDefault,defaultValue,
  //             defaultByteCount,hasHighlight,highlightValue,
  //             highlightByteCount,channelSets}] }] }],
  //             channelFunctions: { [offset]: {source:'gdtf',attribute,
  //               functionName,dmxFrom,dmxTo,physicalFrom,physicalTo,
  //               hasDefault,default,defaultByteCount,hasHighlight,
  //               highlight,highlightByteCount,channelSets} } }]
  // }
  //
  // NOTE the deliberate key-name difference between the two shapes above:
  // the per-<ChannelFunction> parse tree (functions[]) is INTERNAL and uses
  // defaultValue/highlightValue, whereas channelFunctions is the WIRE shape
  // handed straight to internal/web's entryRequest and therefore uses that
  // struct's own names, `default` and `highlight`. See the comment at the
  // channelFunctions construction site for why a mismatch is not cosmetic.
  function parseDescriptionXml(xmlString) {
    const doc = parseXml(xmlString);
    const gdtfEl = doc.getElementsByTagName('GDTF')[0];
    if (!gdtfEl) throw new Error('malformed GDTF XML: no <GDTF> root element');
    const fixtureTypeEl = childByTag(gdtfEl, 'FixtureType');
    if (!fixtureTypeEl) throw new Error('malformed GDTF XML: no <FixtureType> element under <GDTF>');

    const manufacturer = (fixtureTypeEl.getAttribute('Manufacturer') || '').trim();
    const model = (fixtureTypeEl.getAttribute('Name') || '').trim();
    const fixtureType = (manufacturer + ' ' + model).trim();

    const geometriesEl = childByTag(fixtureTypeEl, 'Geometries');
    const geometryIndex = buildGeometryIndex(geometriesEl);

    const dmxModesEl = childByTag(fixtureTypeEl, 'DMXModes');
    const modeEls = dmxModesEl ? childrenByTag(dmxModesEl, 'DMXMode') : [];
    const warnings = [];
    const modes = modeEls.map(modeEl => parseMode(modeEl, geometryIndex, warnings));

    // Placeholder-profile detection runs once over the finished modes (it
    // needs the manufacturer, which parseMode never sees) and, like the
    // mode-name cross-check inside parseMode, only ever appends a warning.
    detectPlaceholderProfiles(manufacturer, model, modes, warnings);

    // warnings: geometry-resolution notes collected across every mode (a
    // reference cycle, a multi-break reference, an unresolvable root/target
    // geometry name), mode-name channel-count disagreements, and
    // generated-placeholder-profile flags — never fatal, always additive to
    // whatever the caller (mvrimport.js / patch.js) already surfaces for
    // this fixture. There is deliberately ONE warnings channel: a second
    // one would need a second surfacing path in every caller, and the one
    // that got wired up last would be the one nobody sees.
    return { manufacturer, model, fixtureType, modes, warnings };
  }

  return { parseDescriptionXml };
})();
