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
//           <ChannelFunction Name="Pan" Attribute="Pan"/>
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

  function parseChannelFunction(cfEl) {
    return {
      name: cfEl.getAttribute('Name') || '',
      attribute: cfEl.getAttribute('Attribute') || '',
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

  function parseMode(modeEl) {
    const name = modeEl.getAttribute('Name') || '';
    const channelsContainer = childByTag(modeEl, 'DMXChannels');
    const channelEls = channelsContainer ? childrenByTag(channelsContainer, 'DMXChannel') : [];
    const channels = channelEls.map(parseDmxChannel);

    let footprint = 0;
    channels.forEach(ch => {
      ch.offsets.forEach(off => { if (off > footprint) footprint = off; });
    });

    return { name, footprint, channels };
  }

  // parseDescriptionXml(xmlString) -> {
  //   manufacturer: string,
  //   model: string,
  //   fixtureType: string,   // "Manufacturer Model", same convention as
  //                          // patch.go's handlePatchAdopt
  //                          // (fixtureType := strings.TrimSpace(mfr+" "+model))
  //   modes: [{ name, footprint, channels: [{ offsets: number[],
  //             logicalChannels: [{ attribute, functions: [{name,attribute}] }] }] }]
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
