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

  // parseDmxValue: GDTF's DMXFrom/Default attributes are written "X/Y"
  // (raw DMX value / byte count), e.g. "0/1", "128/2". This module only
  // resolves the raw value (X) — see the file doc comment's
  // channelFunctions rule for why byte count/fine-byte resolution is out
  // of scope.
  function parseDmxValue(s) {
    if (!s) return 0;
    const slash = s.indexOf('/');
    const n = parseInt(slash >= 0 ? s.slice(0, slash) : s, 10);
    return Number.isFinite(n) ? n : 0;
  }

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

  function parseChannelFunction(cfEl) {
    return {
      name: cfEl.getAttribute('Name') || '',
      attribute: cfEl.getAttribute('Attribute') || '',
      dmxFrom: parseDmxValue(cfEl.getAttribute('DMXFrom')),
      physicalFrom: parseFloatAttr(cfEl.getAttribute('PhysicalFrom')),
      physicalTo: parseFloatAttr(cfEl.getAttribute('PhysicalTo')),
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
      // real channels of its own AND every child is a <GeometryReference> —
      // see the doc comment above resolveGeometryChannels for the real-file
      // case this fixes and why the check is this specific.
      let blockReferenceChildren = false;
      if (contributedReal && el.children.length > 0) {
        blockReferenceChildren = true;
        for (let i = 0; i < el.children.length; i++) {
          const t = el.children[i].tagName || el.children[i].localName;
          if (t !== GEOMETRY_REFERENCE_TAG) { blockReferenceChildren = false; break; }
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
  //             dmxFrom,physicalFrom,physicalTo,channelSets}] }] }],
  //             channelFunctions: { [offset]: {source:'gdtf',attribute,
  //               functionName,dmxFrom,dmxTo,physicalFrom,physicalTo,
  //               channelSets} } }]
  // }
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

    // warnings: geometry-resolution notes collected across every mode (a
    // reference cycle, a multi-break reference, an unresolvable root/target
    // geometry name) — never fatal, always additive to whatever the caller
    // (mvrimport.js / patch.js) already surfaces for this fixture.
    return { manufacturer, model, fixtureType, modes, warnings };
  }

  return { parseDescriptionXml };
})();
