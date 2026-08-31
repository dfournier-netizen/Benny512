// mvrimport.js — orchestration layer for Phase 2b MVR/GDTF patch import.
// Ties together the three modules built separately (mvrzip.js: generic zip
// reader; mvrparse.js: MVR GeneralSceneDescription.xml -> fixture records;
// gdtfparse.js: GDTF description.xml -> modes/footprint) into one call the
// UI layer (patch.js) can use: raw .mvr bytes in, entryRequest-shaped patch
// entries out. This module owns no UI and touches no DOM.
//
// Pipeline per file:
//   1. Open the outer .mvr as a zip (mvrzip.js).
//   2. Find GeneralSceneDescription.xml (case-insensitive last-path-segment
//      match — some exporters nest it, some vary its casing) and parse it
//      (mvrparse.js) for the fixture list.
//   3. For each fixture, resolve its <GDTFSpec> against the outer zip's
//      entry names (exact match, then with a ".gdtf" suffix appended, then a
//      case-insensitive last-path-segment match) and, once resolved, open
//      that entry as a zip too (a .gdtf file is itself a zip) and parse its
//      description.xml (gdtfparse.js) for modes/footprint.
//   4. Resolved GDTF files are parsed once and cached by resolved zip-entry
//      name — MVR shows routinely repeat the same fixture type dozens of
//      times.
//
// Never abort the whole import over one bad fixture: an unresolvable GDTF
// reference or a GDTFMode name that doesn't match any mode in the resolved
// GDTF file each downgrade that one fixture to footprint 0 plus a warning,
// rather than throwing. Only a totally unreadable/non-zip file, or a zip
// with no GeneralSceneDescription.xml at all, produces a top-level Error.
//
// Also exports parseGdtfFile(arrayBuffer) — single-.gdtf-file import (task
// ask, single-GDTF match-to-fixture-type flow) for the case a GDTFSpec
// reference is genuinely absent from an .mvr archive and the tech supplies
// the .gdtf by hand afterward; see its doc comment below. patch.js is the
// only caller and owns matching it against existing patch entries by
// fixtureType plus the apply-to-confirm preview/write flow — this module
// only parses.
const MvrImport = (() => {
  function lastSegment(name) {
    const parts = String(name).split(/[\\/]/);
    return parts[parts.length - 1];
  }

  function findByLastSegmentCI(names, wantName) {
    const want = wantName.toLowerCase();
    return names.find(n => lastSegment(n).toLowerCase() === want) || null;
  }

  function findGsdName(names) {
    return findByLastSegmentCI(names, 'GeneralSceneDescription.xml');
  }

  // resolveGdtfName: try, in order, an exact name match, an exact match with
  // ".gdtf" appended, then a case-insensitive last-path-segment match
  // against both the spec value as-is and with ".gdtf" appended. Returns
  // null if nothing in the archive matches any of those.
  function resolveGdtfName(names, gdtfSpec) {
    if (!gdtfSpec) return null;
    if (names.includes(gdtfSpec)) return gdtfSpec;
    const withExt = gdtfSpec + '.gdtf';
    if (names.includes(withExt)) return withExt;

    const specLower = gdtfSpec.toLowerCase();
    const withExtLower = withExt.toLowerCase();
    const match = names.find(n => {
      const seg = lastSegment(n).toLowerCase();
      return seg === specLower || seg === withExtLower;
    });
    return match || null;
  }

  // buildFallbackEntry: shared shape for both "GDTF file not found" and
  // "GDTF parse failed" cases — footprint 0, fixtureType falls back to the
  // fixture's own name (no GDTF-derived fixtureType is available), notes
  // carries the specific reason alongside any of the fixture's own
  // (mvrparse.js-level) warnings.
  function buildFallbackEntry(fixture, reason) {
    const noteParts = (fixture.warnings || []).slice();
    if (reason) noteParts.push(reason);
    return {
      name: fixture.name,
      fixtureType: fixture.name,
      mode: fixture.gdtfMode,
      footprint: 0,
      universe: fixture.universe,
      startAddress: fixture.startAddress,
      position: fixture.position,
      fixtureNumber: fixture.fixtureId,
      notes: noteParts.join('; '),
    };
  }

  // Returns { entries: [entryRequest-shaped objects], warnings: string[] }
  async function parseMvrFile(arrayBuffer) {
    let outerZip;
    try {
      outerZip = await MvrZip.openZip(arrayBuffer);
    } catch (e) {
      throw new Error('could not read file as an MVR (zip) archive: ' + e.message);
    }

    const gsdName = findGsdName(outerZip.names);
    if (!gsdName) {
      throw new Error('not a valid MVR file — no GeneralSceneDescription.xml found');
    }

    const gsdBytes = await outerZip.read(gsdName);
    const gsdXml = new TextDecoder('utf-8').decode(gsdBytes);
    const parsedGsd = MvrParse.parseGeneralSceneDescriptionXml(gsdXml);

    const warnings = parsedGsd.warnings.slice();

    // gdtfCache: resolved zip-entry name -> parsed GdtfParse result. Many
    // MVR fixture instances reference the same .gdtf file; parse it once.
    const gdtfCache = new Map();

    async function getParsedGdtf(resolvedName) {
      if (gdtfCache.has(resolvedName)) return gdtfCache.get(resolvedName);
      const gdtfBytes = await outerZip.read(resolvedName);
      const innerZip = await MvrZip.openZip(gdtfBytes);
      const descName = findByLastSegmentCI(innerZip.names, 'description.xml');
      if (!descName) {
        throw new Error(`no description.xml found inside GDTF file "${resolvedName}"`);
      }
      const descBytes = await innerZip.read(descName);
      const descXml = new TextDecoder('utf-8').decode(descBytes);
      const result = GdtfParse.parseDescriptionXml(descXml);
      gdtfCache.set(resolvedName, result);
      return result;
    }

    const entries = [];
    for (const fixture of parsedGsd.fixtures) {
      const label = fixture.name || fixture.uuid || '(unnamed fixture)';

      const resolvedName = resolveGdtfName(outerZip.names, fixture.gdtfSpec);
      if (!resolvedName) {
        const reason = `Fixture "${label}": GDTF file "${fixture.gdtfSpec}" not found in archive`;
        warnings.push(...(fixture.warnings || []));
        warnings.push(reason);
        entries.push(buildFallbackEntry(fixture, reason));
        continue;
      }

      let gdtf;
      try {
        gdtf = await getParsedGdtf(resolvedName);
      } catch (e) {
        const reason = `Fixture "${label}": failed to parse GDTF file "${resolvedName}": ${e.message}`;
        warnings.push(...(fixture.warnings || []));
        warnings.push(reason);
        entries.push(buildFallbackEntry(fixture, reason));
        continue;
      }

      warnings.push(...(fixture.warnings || []));

      const mode = (gdtf.modes || []).find(m => m.name === fixture.gdtfMode);
      let footprint, notes;
      const ownNotes = (fixture.warnings || []).slice();
      if (mode) {
        footprint = mode.footprint;
        notes = ownNotes.join('; ');
      } else {
        footprint = 0;
        const reason = `Fixture "${label}": DMX mode "${fixture.gdtfMode}" not found in GDTF file`;
        warnings.push(reason);
        ownNotes.push(reason);
        notes = ownNotes.join('; ');
      }

      entries.push({
        name: fixture.name,
        fixtureType: gdtf.fixtureType,
        mode: fixture.gdtfMode,
        footprint,
        universe: fixture.universe,
        startAddress: fixture.startAddress,
        position: fixture.position,
        fixtureNumber: fixture.fixtureId,
        notes,
      });
    }

    return { entries, warnings };
  }

  // parseGdtfFile: single-.gdtf-file import (task ask: "import a single
  // .gdtf file and match it to a fixture type" — the motivating case is a
  // GDTF referenced by an MVR but absent from that MVR's archive, supplied
  // by hand afterward). A .gdtf file is itself a zip (mvrzip.js's own doc
  // comment); this opens it directly rather than looking inside an outer
  // .mvr. Returns { manufacturer, model, fixtureType, modes: [{name,
  // footprint}] } — same shape gdtfparse.js already produces, so the
  // orchestration/matching logic (patch.js) is identical to the MVR path's
  // per-fixture GDTF resolution. Throws a plain Error with a clear message
  // for: not a zip at all, a zip with no description.xml, or a
  // description.xml that fails to parse — never silently returns a partial
  // result (task ask: "never crash", but also never invent data — a file
  // that isn't a valid GDTF must say so, not pretend to have zero modes).
  async function parseGdtfFile(arrayBuffer) {
    let zip;
    try {
      zip = await MvrZip.openZip(arrayBuffer);
    } catch (e) {
      throw new Error('could not read file as a GDTF (zip) archive: ' + e.message);
    }
    const descName = zip.names.find(n => {
      const seg = n.split(/[\\/]/).pop();
      return seg.toLowerCase() === 'description.xml';
    });
    if (!descName) {
      throw new Error('not a valid GDTF file — no description.xml found inside the archive');
    }
    let descBytes;
    try {
      descBytes = await zip.read(descName);
    } catch (e) {
      throw new Error('could not read description.xml from the GDTF archive: ' + e.message);
    }
    const descXml = new TextDecoder('utf-8').decode(descBytes);
    // GdtfParse.parseDescriptionXml throws its own clear Error for
    // malformed XML / missing <GDTF>/<FixtureType> — let it propagate
    // as-is, no need to wrap twice.
    return GdtfParse.parseDescriptionXml(descXml);
  }

  return { parseMvrFile, parseGdtfFile };
})();
