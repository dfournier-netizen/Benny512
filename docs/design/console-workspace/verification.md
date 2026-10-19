# Workspace design verification — 2026-10-19

Verified the standalone W1 artifacts in installed Chrome headless, served from loopback with a separate temporary browser profile. These checks use fictional fixtures and no production controllers or show data. Headless Chromium is a real browser render, but not physical touch/glove, screen-reader or rig verification.

## Results

- **576 layout cases pass:** 18 scenes × four laptop sizes × Dark / Daylight / greyscale / greyscale-plus-dim × fine-pointer / explicit coarse-target preview. See `evidence/browser-checks.json`. No document overflow, Disarm outside viewport, grid under dock, overlapping fixture/cell tiles, clipped intensity track, out-of-workspace floating window, or undersized coarse-pointer button/select/summary.
- **14 interaction checks pass:** 20 px dock resizing, Float removing an empty dock, 20 px floating resize, focused-title movement/clamping, collapse, hide/focus return, reopening/title focus, shared Console/Tests selection, TEST tag, values-OFF retaining test state, Cancel-only disarmed command, Escape focus restore, Disarm hiding Send, and saved window geometry after reload.
- **62 new contrast pairs pass** in Dark/Daylight. Text on both intensity track/fill is ≥7:1; ordinary new-role text ≥4.5:1; opaque window/dock/drop/track boundaries ≥3:1. All pairs and exact unrounded values are in `contrast.json`. Shadow and grid lines are decorative. Existing production token values remain unchanged.
- **19 SVG symbols** parse as XML, have unique new IDs, no production ID collisions, and no embedded metadata, C2PA, descendant IDs, style elements or embedded images. New icons are stroke/currentColor, 24 grid, 1.75 width. Proof sheet includes 14/18/24/32 px.
- All seven JavaScript/module files pass `node --check`. Both generation commands run successfully. No external dependencies added. Whitespace/diff checks pass.

## Measured default workspace

Fine-pointer default, 440 px dock, Plan + Fit, retained sample programmer look:

| Browser viewport | Grid width × height | Toolbar |
|---|---|---|
| 1024×768 | 584×484 | one row; Active layer selector |
| 1280×800 | 840×516 | one row; layer segments |
| 1440×900 | 1000×616 | one row; layer segments |
| 1920×1080 | 1480×796 | one row; layer segments |

The 60 px Disarm slab retains the existing primary-target rule; this gives slightly less grid height than the reference's 40 px Stop. The 1024 composition recovers space by avoiding the reference's two-row toolbar. Fit honors tile/target floors and permits internal grid scrolling; it does not promise every fixture fits at unreadable scale.

## Saved renders and corrections

`evidence/<width>-<scene>.png` covers all nine requested scenes at all four sizes (36 screenshots). Additional renders cover the 320 px dock, Daylight Control, coarse cell targets, greyscale/dim Compact, and Dark/Daylight icon sheets: 42 screenshots total.

Visual inspection covered the 1024 default header/toolbar, stacked/collapsed dock and floating Groups, separate Tests screen, coarse cells, Daylight and dim state distinctions. Fixes found through rendering/checks: cropped percentage labels in 64 px tiles; an erroneous check on an OFF Fine button; low-contrast inherited SVG fills; a visually-hidden status paragraph growing the document; 36 px window actions winning over coarse target styles; and four-cell coarse targets overlapping packed neighbors. Cells now expand column/row floors together. Intensity fill uses low-luminance roles and retains readable words; a mixed cell fixture has a range instead of a false zero/average. Test examples use unclaimed values, preserving programmer priority.

## Limits / next verification

- Go and gofmt are unavailable on PATH here. Required gofmt, vet, native build, full/race suites and Windows cross-build could not run. No Go or production web source changed. A disposable production `--demo` build could not be made, so this is standalone artifact verification.
- Real coarse-pointer input, drag/drop with gloves, pointer-cancel/Escape during a drag, physical minimum-brightness/dark adaptation, screen-reader navigation and long translated labels require device/integration checks. The coarse query reproduces target geometry, not a hardware certification.
- Fixture/cell composed intensity and source ownership are a proposed **data contract**, not a verified production endpoint. Production must use engine composition, report mixed ranges and show “not reported” when data is absent. The preview's authored sample values do not establish telemetry support.
- Only demonstrative interactions are implemented. Profile resolution, actual XY/shaper editing, commands, raw DMX, MIDI, scope changes and test/sequence scheduling remain production-controller responsibilities. This package sends nothing.
- OPEN-W2/T1/K1/L1 and right-only docking are reasoned proposals in `component-specs.md`, awaiting owner review. No production approval is inferred. Phone/tablet, group-fader design and theme editor remain unchanged.

Reproduce assets with `build-package.mjs` and `prepare-mockups.mjs`. Serve repository root via the existing loopback `serve.mjs`. `inspect-preview.html` runs design-only geometry/keyboard checks in a browser and prints JSON; it is not a production test harness or output endpoint.
