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
//           <Matrix>...</Matrix>              (composed into a world
//                                               location — see "World
//                                               location" below)
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
// Address -> universe/startAddress conversion.
//
// CONVENTION MISMATCH (confirmed bug, fixed here — see git history/task
// notes for the postmortem): the MVR/GDTF spec's absolute DMX address N is
// 1-based and flattened across 512-channel universes using a 1-based
// universe count (universe 1 = addresses 1-512, universe 2 = 513-1024,
// ...). Benny512's OWN canonical internal representation, however, is the
// 0-based Art-Net Port-Address — see patch.Entry.Universe's doc comment
// ("Art-Net Port-Address (raw value, 0-32767)") and
// artnet.PortAddressFromRaw, which every consumer of Entry.Universe
// (internal/patch/rigcheck.go, internal/session/dmxout.go) feeds straight
// through with NO +1/-1 adjustment anywhere else in the app. Manually
// entered and adopt-from-RDM entries were always correct because they were
// authored/derived directly in 0-based terms; only this MVR path was
// wrong, converting to MVR's 1-based universe numbering and then storing
// that mismatched number as if it were the canonical 0-based value — every
// MVR-imported fixture landed ONE UNIVERSE TOO HIGH on the wire.
//
//   MVR-1-based universe = floor((N-1)/512) + 1   (the spec's own number)
//   canonical 0-based    = floor((N-1)/512)       (what this module now
//                                                   returns — subtract 1
//                                                   from the spec formula,
//                                                   do NOT add it)
//   startAddress          = ((N-1) % 512) + 1       (unchanged either way —
//                                                     already 1-based both
//                                                     in MVR and in Entry)
//
// e.g. N=513 -> MVR calls this "universe 2" (its own 1-based notation) but
// this module returns universe=1, startAddress=1 — the 0-based canonical
// pair that, once patched, actually drives Art-Net universe 1 (raw
// Port-Address 1), matching what Vectorworks/MVR shows as "universe 2" in
// its 1-based UI. The display layer (UI.formatUser/parseUser in ui.js,
// driven by the Settings "Art-Net starting universe"
// which defaults to 1) re-adds that +1 ONLY at the presentation boundary —
// callers of this module must never re-apply a +1/-1 themselves, or the
// conversion silently doubles up again.
//
// World location (Console-lite C2). Primary text: MVR 1.6 / DIN SPEC 15801
// (github.com/mvrdevelopment/spec, mvr-spec.md at 098d3791):
//   - "Node Definition: Matrix", Table 35: right-handed, Z up, 1 unit = 1 mm;
//     the value is a 4x3 matrix written {u1,u2,u3}{v1,v2,v3}{w1,w2,w3}
//     {o1,o2,o3}; when the node is missing it is {1,0,0}{0,1,0}{0,0,1}{0,0,0}.
//   - Tables 17, 20, 22, 26, 28, 30, 32, 34: the Matrix of a SceneObject,
//     GroupObject, Fixture, Truss, Support, VideoScreen, Projector is "the
//     location (and orientation) of the object inside the parent coordinate
//     system"; a Layer's Matrix defines "local coordinate space for the
//     objects inside" and may only carry elevation (Table 17).
// So a fixture's world transform is the composition of every ancestor's
// Matrix with its own: world = Layer ∘ ... ∘ GroupObject ∘ Truss ∘ Fixture.
//
// What the spec does NOT state is whether u/v/w are rows or columns. This
// reads them the way the reference implementation does — libMVRgdtf
// (github.com/mvrdevelopment/libMVRgdtf, 9f5a6420) GdtfConverter::
// ConvertMatrix stores u, v, w, o as rows 0..3 and VWTransformMatrix::
// PointTransform maps p to x·u + y·v + z·w + o — i.e. u, v, w are the
// object's local X, Y, Z axes expressed in the parent's coordinates. That
// reading is consistent with real exports (a Capture 2023.1.6 boom fixture
// writes u = (0,0,1): its local X axis points up). Unverified against any
// statement in the spec itself.
//
// Deliberate refusals (a plausible number never stands in for "no answer"):
//   - a Fixture with NO <Matrix> of its own gets location.known = false,
//     even though Table 35 would make it the parent's origin: an exporter
//     that omits the node has not placed the fixture, and planting every such
//     fixture at 0,0,0 would be a guess (open owner question). An ANCESTOR
//     without a Matrix is the identity, as the spec says — that is ordinary.
//   - an empty <Matrix/> is the default (libMVRgdtf does the same).
//   - a malformed Matrix (anything but four {x,y,z} rows of finite numbers,
//     incl. GDTF-style 4-wide rows) on the fixture OR any ancestor: unknown.
//   - a Fixture with <ChildPosition> is placed relative to a geometry of its
//     parent's GDTF (Table 26), which this parser cannot resolve: unknown.
//   - a basis that is not a rotation (degenerate, non-perpendicular axes, or
//     mirrored, det < 0): position known, rotation unknown.
// Each refusal is a warning naming the fixture.
//
// Euler angles: MVR defines none (the matrix is the data). location.rotX/
// rotY/rotZ are degrees for R = Rz(rotZ)·Ry(rotY)·Rx(rotX) acting on column
// vectors, where R's columns are the normalized world u, v, w; rotY in
// [-90, 90], and at the ±90 gimbal lock rotX is 0. Exact for that stated
// convention; the convention itself is our choice (patch.Location).
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

  // absoluteToUniverseAddress: see file header formula. Returns the
  // CANONICAL 0-based universe (not MVR's own 1-based notation) — do not
  // add 1 here; see the file header's "CONVENTION MISMATCH" note for why.
  function absoluteToUniverseAddress(n) {
    const universe = Math.floor((n - 1) / 512);
    const startAddress = ((n - 1) % 512) + 1;
    return { universe, startAddress };
  }

  // positionNames maps an MVR Scene/Positions UUID to its operator-facing
  // name. Fixture.Position is a reference UUID, not a useful label; layer
  // name remains the fallback for exporters that omit the collection.
  function positionNamesByUUID(doc) {
    const out = {};
    Array.from(doc.getElementsByTagName('Position')).forEach(el => {
      const uuid = fieldValue(el, ['uuid', 'UUID', 'Uuid']).toLowerCase();
      const name = fieldValue(el, ['name', 'Name']);
      if (uuid && name) out[uuid] = name;
    });
    return out;
  }

  // --- world location -----------------------------------------------------

  const IDENTITY = { u: [1, 0, 0], v: [0, 1, 0], w: [0, 0, 1], o: [0, 0, 0] };
  const FLOAT_RE = /^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/;

  // matrixOf(el) -> { ok: true, m, present } | { ok: false, reason }. Only a
  // DIRECT <Matrix> child counts (a nested Geometry's Matrix is not el's).
  function matrixOf(el) {
    const mEl = childByTag(el, 'Matrix');
    if (!mEl) return { ok: true, m: IDENTITY, present: false };
    const text = (mEl.textContent || '').trim();
    if (text === '') return { ok: true, m: IDENTITY, present: true };
    const rows = text.match(/^\{([^{}]*)\}\s*\{([^{}]*)\}\s*\{([^{}]*)\}\s*\{([^{}]*)\}$/);
    if (!rows) return { ok: false, reason: 'its <Matrix> is not four {x,y,z} rows' };
    const vecs = [];
    for (let i = 1; i <= 4; i++) {
      const parts = rows[i].split(',').map(p => p.trim());
      if (parts.length !== 3 || !parts.every(p => FLOAT_RE.test(p))) {
        return { ok: false, reason: 'its <Matrix> is not four {x,y,z} rows of numbers' };
      }
      const nums = parts.map(Number);
      if (!nums.every(Number.isFinite)) {
        return { ok: false, reason: 'its <Matrix> holds a number out of range' };
      }
      vecs.push(nums);
    }
    return { ok: true, m: { u: vecs[0], v: vecs[1], w: vecs[2], o: vecs[3] }, present: true };
  }

  // compose(parent, child): child's transform expressed in parent's frame,
  // carried into parent's own parent frame (row reading, see header):
  // axes rotate by parent's basis; the origin also gains parent's offset.
  function compose(parent, child) {
    const rot = a => [0, 1, 2].map(k => a[0] * parent.u[k] + a[1] * parent.v[k] + a[2] * parent.w[k]);
    const o = rot(child.o).map((x, k) => x + parent.o[k]);
    return { u: rot(child.u), v: rot(child.v), w: rot(child.w), o };
  }

  // xformInto(parentX, el): the transform el's children live in, or the
  // reason it cannot be known. Failure is sticky for the whole subtree.
  function xformInto(parentX, el, what) {
    if (!parentX.ok) return parentX;
    const mx = matrixOf(el);
    if (!mx.ok) return { ok: false, reason: `${what} ${mx.reason}` };
    return { ok: true, m: compose(parentX.m, mx.m) };
  }

  function norm(a) { return Math.sqrt(a[0] * a[0] + a[1] * a[1] + a[2] * a[2]); }
  function dot(a, b) { return a[0] * b[0] + a[1] * b[1] + a[2] * b[2]; }
  function cross(a, b) { return [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]]; }

  // rotationOf(m) -> { ok, rotX, rotY, rotZ } | { ok: false, reason } — the
  // header's Euler convention. Scale is divided out; anything that is not a
  // proper rotation once normalized is refused, not approximated.
  function rotationOf(m) {
    const lens = [norm(m.u), norm(m.v), norm(m.w)];
    if (lens.some(l => !(l > 1e-9))) return { ok: false, reason: 'its orientation axes are degenerate' };
    const u = m.u.map(x => x / lens[0]), v = m.v.map(x => x / lens[1]), w = m.w.map(x => x / lens[2]);
    if (Math.abs(dot(u, v)) > 1e-3 || Math.abs(dot(u, w)) > 1e-3 || Math.abs(dot(v, w)) > 1e-3) {
      return { ok: false, reason: 'its orientation axes are not perpendicular' };
    }
    if (dot(u, cross(v, w)) <= 0) return { ok: false, reason: 'its orientation is mirrored' };
    const deg = 180 / Math.PI;
    // atan2 rather than asin(−u.z): well-conditioned near ±90.
    const rotY = Math.atan2(-u[2], Math.hypot(u[0], u[1]));
    let rotX, rotZ;
    if (Math.abs(u[2]) < 1 - 1e-6) {
      rotX = Math.atan2(v[2], w[2]);
      rotZ = Math.atan2(u[1], u[0]);
    } else {
      rotX = 0;
      rotZ = Math.atan2(-v[0], v[1]);
    }
    return { ok: true, rotX: rotX * deg + 0, rotY: rotY * deg + 0, rotZ: rotZ * deg + 0 };
  }

  const UNKNOWN_LOCATION = Object.freeze({ known: false, x: 0, y: 0, z: 0, rotationKnown: false, rotX: 0, rotY: 0, rotZ: 0 });

  // locationOf -> { location, warning } (warning '' when nothing to say).
  // A fixture without its own Matrix is reported by the caller in one
  // summary line rather than once per fixture.
  function locationOf(fixtureEl, xform, label) {
    const unknown = Object.assign({}, UNKNOWN_LOCATION);
    if (firstChildText(fixtureEl, ['ChildPosition'])) {
      return { location: unknown, warning: `fixture "${label}": position not imported — it is placed on a geometry of its parent's GDTF (<ChildPosition>), which this importer cannot resolve`, noMatrix: false };
    }
    if (!xform.ok) {
      return { location: unknown, warning: `fixture "${label}": position not imported — a containing object's position cannot be read (${xform.reason})`, noMatrix: false };
    }
    const own = matrixOf(fixtureEl);
    if (!own.ok) {
      return { location: unknown, warning: `fixture "${label}": position not imported — ${own.reason}`, noMatrix: false };
    }
    if (!own.present) return { location: unknown, warning: '', noMatrix: true };
    const world = compose(xform.m, own.m);
    const loc = { known: true, x: world.o[0], y: world.o[1], z: world.o[2], rotationKnown: false, rotX: 0, rotY: 0, rotZ: 0 };
    const r = rotationOf(world);
    if (!r.ok) {
      return { location: loc, warning: `fixture "${label}": position imported, rotation not — ${r.reason}`, noMatrix: false };
    }
    loc.rotationKnown = true;
    loc.rotX = r.rotX; loc.rotY = r.rotY; loc.rotZ = r.rotZ;
    return { location: loc, warning: '', noMatrix: false };
  }

  // parseFixture -> { fixture, skipReason } — exactly one of the two is set.
  function parseFixture(fixtureEl, layerName, positionNames, xform) {
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
	const positionUUID = firstChildText(fixtureEl, ['Position', 'position']).toLowerCase();
	const position = positionNames[positionUUID] || layerName || '';
    const { universe, startAddress } = absoluteToUniverseAddress(chosen.value);

    const placed = locationOf(fixtureEl, xform, label);
    const warnings = [];
    if (placed.warning) warnings.push(placed.warning);
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
        position,
        location: placed.location,
        warnings,
      },
      noMatrix: placed.noMatrix,
    };
  }

  // walk: recursively descends Layer/Group containers collecting Fixture
  // elements. A named Scene/Positions UUID wins; layerName is a compatible
  // fallback for MVR exporters that provide only layer organization.
  // xform is the world transform el's content lives in (see "World
  // location" in the file header); every container child composes its own
  // Matrix onto it before descending.
  function walk(el, layerName, positionNames, fixtures, warnings, xform, stats) {
    directContent(el).forEach(child => {
      const tag = child.tagName || child.localName;
      if (tag === 'Fixture') {
        const result = parseFixture(child, layerName, positionNames, xform);
        if (result.fixture) {
          fixtures.push(result.fixture);
          if (result.noMatrix) stats.noMatrix++;
        } else warnings.push(result.skipReason);
      } else if (tag === 'Layer') {
        const name = fieldValue(child, ['name', 'Name']);
        walk(child, name || layerName, positionNames, fixtures, warnings,
          xformInto(xform, child, `layer "${name || '(unnamed)'}"`), stats);
      } else {
        // Group or any other container kind (Truss, Support, ...): recurse
        // transparently in case fixtures are nested inside, without
        // changing the tracked Layer-name label.
        const name = fieldValue(child, ['name', 'Name']);
        walk(child, layerName, positionNames, fixtures, warnings,
          xformInto(xform, child, `${tag} "${name || '(unnamed)'}"`), stats);
      }
    });
  }

  // parseGeneralSceneDescriptionXml(xmlString) -> {
  //   fixtures: [{ name, uuid, gdtfSpec, gdtfMode, fixtureId, universe,
  //                startAddress, position, location, warnings: string[] }],
  //   location: { known, x, y, z, rotationKnown, rotX, rotY, rotZ } —
  //             patch.Location's wire shape (mm / degrees, header above)
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
    const stats = { noMatrix: 0 };
    if (layersEl) walk(layersEl, null, positionNamesByUUID(doc), fixtures, warnings, { ok: true, m: IDENTITY }, stats);
    if (stats.noMatrix > 0) {
      warnings.push(`${stats.noMatrix} fixture(s) carry no <Matrix>, so their positions were not imported`);
    }

    return { fixtures, warnings };
  }

  return { parseGeneralSceneDescriptionXml };
})();
