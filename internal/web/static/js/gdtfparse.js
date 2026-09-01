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
// Footprint rule (deliberate simplification, task ask): footprint for a mode
// = the maximum numeric offset appearing across all its DMXChannel Offset
// lists, ignoring the literal "None" (a channel with no DMX footprint
// impact). Full GDTF geometry-tree resolution for repeated/multi-instance
// geometries is explicitly out of scope this phase — this is exactly what a
// naive DMXChannel-list read gives you, which is correct for the common
// case.
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

  function parseMode(modeEl) {
    const name = modeEl.getAttribute('Name') || '';
    const channelsContainer = childByTag(modeEl, 'DMXChannels');
    const channelEls = channelsContainer ? childrenByTag(channelsContainer, 'DMXChannel') : [];
    const channels = channelEls.map(parseDmxChannel);

    let footprint = 0;
    channels.forEach(ch => {
      ch.offsets.forEach(off => { if (off > footprint) footprint = off; });
    });

    // channelFunctions: keyed by offset (matches patch.Entry.ChannelFunctions
    // 1:1 — see the file doc comment's channelFunctions rule). An
    // attribute-less resolution (resolveChannelFunction returned null, or
    // resolved with an empty attribute) contributes nothing — this map only
    // ever holds real, resolved GDTF data, never a placeholder "absent"
    // entry (patch.Entry's map itself represents "absent" as a missing key,
    // not a zero-valued one — see ChannelFunctionSource's doc comment).
    const channelFunctions = {};
    channels.forEach(ch => {
      const resolved = resolveChannelFunction(ch.logicalChannels);
      if (!resolved || !resolved.attribute) return;
      ch.offsets.forEach(off => {
        channelFunctions[off] = {
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
    });

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

    const dmxModesEl = childByTag(fixtureTypeEl, 'DMXModes');
    const modeEls = dmxModesEl ? childrenByTag(dmxModesEl, 'DMXMode') : [];
    const modes = modeEls.map(parseMode);

    return { manufacturer, model, fixtureType, modes };
  }

  return { parseDescriptionXml };
})();
