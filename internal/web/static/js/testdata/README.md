# Test data provenance

Third-party files in this folder are test data; index.html loads nothing
from `testdata/`. NOTE: `internal/web/server.go` embeds `all:static`, so
this folder — these extracts included — IS compiled into benny512.exe and
reachable under `/js/testdata/` on the running server (a pre-existing
arrangement, not introduced by these files; flagged to the owner). Each entry gives
the source, the exact revision, and the licence. Extracts are trimmed by
removing whole elements only; every line kept is byte-identical to the
source (CRLF line endings preserved where the source has them).

## robe_bmfl_spot_real_extract.xml

- Vendor: Robe lighting s.r.o., ROBIN BMFL Spot, GDTF DataVersion 1.0.
- Source: `unittest/files/Robe lighting s.r.o.@BMFL Spot@cannot assign 0 to frost function.gdtf`
  in https://github.com/mvrdevelopment/libMVRgdtf at commit
  `9f5a6420c72f24fdd249cfe8133f257026c5590b` (archive SHA-256
  `92c0a3cabfc2df9fbd15bcd708b85f17773b843ae751c12bc556554459056345`),
  file `description.xml` inside it.
- Kept: the `<FixtureType>` line, `<Wheels>`, `<Geometries>`, DMX mode
  "Mode 1 - Standard 16 bit". Removed: AttributeDefinitions,
  PhysicalDescriptions, Models, Mode 2, Revisions, FTPresets, Protocols.
  Extract SHA-256 `b85aad3901dd5009fb359583256e60fffc91700b9202db4c8997706909e83785`.
- Licence: libMVRgdtf's "My Virtual Rig (MVR) SDK License, Version 1.0,
  June 2020" (repository `LICENCE.md`). Kept by owner decision 2026-10-07.

## robe_ledbeam100_real_extract.xml

- Vendor: Robe lighting s.r.o., Robin 100 LEDBeam, GDTF DataVersion 1.0.
- Source: `unittest/files/WrongDmxValue.gdtf` in the same repository and
  commit as above (archive SHA-256
  `9d97d4b320972e66c06a25d476f02af43b929ed50954804d947741c74c936991`),
  file `description.xml` inside it.
- Kept: `<FixtureType>` line, `<Wheels>`, `<Geometries>`, DMX mode "mode 3".
  Extract SHA-256 `188038fdddc4da547f3dc7d4b7cd79132d932f124b98bdccd022e8cf1bce7ddc`.
- Licence: as above (MVR SDK License 1.0). Kept by owner decision 2026-10-07.

## capture_demo_show_real_extract.xml

- Source, revision, checksums and licence are in the file's own header
  comment: `tests/capture_demo_show.mvr` in
  https://github.com/open-stage/python-mvr at commit
  `9bbcabc101dd5981894bce1812f3365bc15f4b56`; python-mvr is MIT licensed
  (Copyright (c) 2023 vanous). Kept by owner decision 2026-10-07.

## paladin_cube_real_extract.xml

- Vendor: Elation, Paladin Cube, GDTF DataVersion 1.2. Supplied by the
  owner from the manufacturer's GDTF (September 2026; see
  `docs/notes/2026-09.md`). Extract SHA-256
  `a60d6c3d8e19deb78f763c43d93cd7757005189068513485274a8d777f203f3c`.
- Source URL, revision and licence were NOT recorded when it was added —
  open for the owner to confirm.

## Generated files (Benny512's own output, no third-party content beyond the above)

- `robe_extracts_legacy_channelfunctions.golden.json` — the pre-C1
  gdtfparse.js's `channelFunctions` for the two Robe extracts.
- `paladin_cube_legacy_channelfunctions.golden.json` — the pre-C1b
  gdtfparse.js's first-function fields for the Paladin Cube extract.
- `tinydom.js` and the `*_test.js` files are this project's own code.
