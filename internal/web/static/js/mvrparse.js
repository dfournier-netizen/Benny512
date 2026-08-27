// mvrparse.js — pure XML parsing layer for an MVR file's
// GeneralSceneDescription.xml (Phase 2b MVR/GDTF patch import). Does NOT
// open any zip itself — mvrzip.js already extracted this XML's text out of
// the outer .mvr archive; combining mvrzip + mvrparse + gdtfparse (resolving
// each fixture's GDTFSpec entry, matching GDTFMode against the GDTF file's
// modes) is the orchestration layer built by a separate agent after this
// one. This module's only job: XML text in, plain fixture records out.
//
// MVR structure this parses:
//   <GeneralSceneDescription><Scene><Layers>
//     <Layer name="..." uuid="...">          (recursive: a Layer's content
//       <ChildList>                           can itself contain nested
//         <Fixture name="..." uuid="...">      Layers/Groups)
//           <Matrix>...</Matrix>              (ignored by explicit design —
//                                               no 3D/position math here)
//           <GDTFSpec>Some Fixture.gdtf</GDTFSpec>
//           <GDTFMode>Mode 1</GDTFMode>
//           <Addresses><Address break="1">513</Address></Addresses>
//           <FixtureID>1</FixtureID>
//         </Fixture>
//         <Layer>...</Layer>
//       </ChildList>
//     </Layer>
//   </Layers></Scene></GeneralSceneDescription>
//
// MVR is inconsistent about attribute-vs-child-element for the "same" piece
// of data across different tools' exports (and across spec revisions), so
// every field below is looked up defensively: attribute first (checking
// both the documented casing and the capitalized variant seen in some
// exports), then a same-named child element's text content.
//
// Address -> universe/startAddress conversion mirrors Benny512's own
// architecture notes for absolute-DMX-address flattening: address N is
// 1-based and flattened across 512-channel universes, so
//   universe      = floor((N-1)/512) + 1
//   startAddress  = ((N-1) % 512) + 1
// (verbatim — e.g. N=513 -> universe 2, startAddress 1; do not re-derive a
// different formula here).
const MvrParse = (() => {
  function parseXml(xmlString) {
    const doc = new DOMParser().parseFromString(xmlString, 'application/xml');
    const perr = doc.getElementsByTagName('parsererror')[0];
    if (perr) {
      throw new Error('malformed MVR XML: ' + perr.textContent.trim().split('\n')[0]);
    }
    return doc;
  }

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

  // firstAttr/firstChildText/fieldValue: defensive attribute-or-child-text
  // lookup — see file header. `names` is tried in order; first non-empty
  // hit wins.
  function firstAttr(el, names) {
    for (const n of names) {
      const v = el.getAttribute(n);
      if (v !== null && v.trim() !== '') return v.trim();
    }
    return '';
  }

  function firstChildText(el, names) {
    for (const n of names) {
      const c = childByTag(el, n);
      if (c) {
        const t = (c.textContent || '').trim();
        if (t) return t;
      }
    }
    return '';
  }

  function fieldValue(el, names) {
    const a = firstAttr(el, names);
    if (a) return a;
    return firstChildText(el, names);
  }

  // directContent: the elements "inside" el for walking purposes. A Layer's
  // real content sits under a <ChildList> wrapper; the top-level <Layers>
  // element has no such wrapper and directly contains <Layer> elements.
  // Transparently unwrapping ChildList here lets the same recursive walk
  // handle both shapes uniformly.
  function directContent(el) {
    const childList = childByTag(el, 'ChildList');
    return Array.from((childList || el).children);
  }

  // parseAddresses: returns { chosen: {brk, value} | null, extraBreaks: number[] }
  // — the lowest-break Address entry present (typically break="1"; the
  // "break" attribute defaults to 1 per MVR/GDTF convention when absent),
  // plus every other distinct break value found so the caller can warn
  // rather than silently drop the fact that more addressing existed.
  function parseAddresses(fixtureEl) {
    const addressesEl = childByTag(fixtureEl, 'Addresses');
    if (!addressesEl) return { chosen: null, extraBreaks: [] };

    const entries = childrenByTag(addressesEl, 'Address').map(addrEl => {
      const brkAttr = addrEl.getAttribute('break');
      const brk = brkAttr !== null && brkAttr.trim() !== '' ? parseInt(brkAttr, 10) : 1;
      const value = parseInt((addrEl.textContent || '').trim(), 10);
      return { brk: Number.isFinite(brk) ? brk : 1, value };
    }).filter(e => Number.isFinite(e.value));

    if (entries.length === 0) return { chosen: null, extraBreaks: [] };

    entries.sort((a, b) => a.brk - b.brk);
    const chosen = entries[0];
    const extraBreaks = entries.slice(1).map(e => e.brk);
    return { chosen, extraBreaks };
  }

  // absoluteToUniverseAddress: see file header formula.
  function absoluteToUniverseAddress(n) {
    const universe = Math.floor((n - 1) / 512) + 1;
    const startAddress = ((n - 1) % 512) + 1;
    return { universe, startAddress };
  }

  // parseFixture -> { fixture, skipReason } — exactly one of the two is set.
  function parseFixture(fixtureEl, layerName) {
    const name = fieldValue(fixtureEl, ['name', 'Name']);
    const uuid = fieldValue(fixtureEl, ['uuid', 'UUID', 'Uuid']);
    const label = name || uuid || '(unnamed fixture)';

    const gdtfSpec = fieldValue(fixtureEl, ['GDTFSpec', 'gdtfSpec']);
    if (!gdtfSpec) {
      return { skipReason: `fixture "${label}" skipped: missing <GDTFSpec> (no GDTF file reference)` };
    }

    const { chosen, extraBreaks } = parseAddresses(fixtureEl);
    if (!chosen) {
      return { skipReason: `fixture "${label}" skipped: missing/empty <Addresses><Address> (no DMX address)` };
    }

    const gdtfMode = fieldValue(fixtureEl, ['GDTFMode', 'gdtfMode']);
    const fixtureId = fieldValue(fixtureEl, ['FixtureID', 'ID', 'fixtureId', 'FixtureId']);
    const { universe, startAddress } = absoluteToUniverseAddress(chosen.value);

    const warnings = [];
    if (!gdtfMode) {
      warnings.push(`fixture "${label}": missing <GDTFMode> — DMX mode/footprint cannot be resolved`);
    }
    if (extraBreaks.length > 0) {
      warnings.push(
        `fixture "${label}": additional address break(s) [${extraBreaks.join(', ')}] present beyond ` +
        `break=${chosen.brk}, which is the one used here (lowest break only) — multi-break addressing ` +
        `is not otherwise represented`
      );
    }

    return {
      fixture: {
        name,
        uuid,
        gdtfSpec,
        gdtfMode,
        fixtureId,
        universe,
        startAddress,
        position: layerName || '',
        warnings,
      },
    };
  }

  // walk: recursively descends Layer/Group containers collecting Fixture
  // elements. layerName tracks the nearest enclosing NAMED <Layer> (best-
  // effort Position label, task explicit design choice) — a Layer without
  // its own name leaves the previously-tracked name in effect; unnamed
  // container kinds (Group, etc.) never change it.
  function walk(el, layerName, fixtures, warnings) {
    directContent(el).forEach(child => {
      const tag = child.tagName || child.localName;
      if (tag === 'Fixture') {
        const result = parseFixture(child, layerName);
        if (result.fixture) fixtures.push(result.fixture);
        else warnings.push(result.skipReason);
      } else if (tag === 'Layer') {
        const name = fieldValue(child, ['name', 'Name']);
        walk(child, name || layerName, fixtures, warnings);
      } else {
        // Group or any other container kind (Truss, Support, ...): recurse
        // transparently in case fixtures are nested inside, without
        // changing the tracked Layer-name label.
        walk(child, layerName, fixtures, warnings);
      }
    });
  }

  // parseGeneralSceneDescriptionXml(xmlString) -> {
  //   fixtures: [{ name, uuid, gdtfSpec, gdtfMode, fixtureId, universe,
  //                startAddress, position, warnings: string[] }],
  //   warnings: string[]   // top-level: one entry per skipped <Fixture>
  // }
  function parseGeneralSceneDescriptionXml(xmlString) {
    const doc = parseXml(xmlString);
    const rootEl = doc.getElementsByTagName('GeneralSceneDescription')[0];
    if (!rootEl) throw new Error('malformed MVR XML: no <GeneralSceneDescription> root element');

    const sceneEl = childByTag(rootEl, 'Scene');
    const layersEl = sceneEl ? childByTag(sceneEl, 'Layers') : null;

    const fixtures = [];
    const warnings = [];
    if (layersEl) walk(layersEl, null, fixtures, warnings);

    return { fixtures, warnings };
  }

  return { parseGeneralSceneDescriptionXml };
})();
