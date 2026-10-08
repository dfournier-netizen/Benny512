# Console-lite — plan and progress (branch `feat/console-lite`)

**Created:** 2026-10-06 23:57:30 -0400. Update the Progress table at the end of every chunk; this file is how a new session resumes.

Goal: a console-lite station for testing and flashing a rig (not show playback). Graphical selection on a 2D grid, live attribute control, one unified output engine behind a master Arm/Disarm, Rig Check folded in as test sequences.

## Owner decisions (Dom, 2026-10-06)
- **GDTF importer:** read every DMX channel, every logical channel and channel function with its DMX range, every channel set, and wheels/slots (colours, media). Existing profiles re-read from stored GDTF files.
- **Positions:** add real positions (MVR `<Matrix>` and manual). Layout = simple 2D grid with z-height layers; fixtures or groups placed on it. Layers are used for display AND selection.
- **Selection:** groups or individual fixtures; multi-cell fixtures selectable as parent AND as individual sub-fixtures.
- **Controls:** pan/tilt XY pad plus individual faders, both live in tandem; colour picker; gobo/colour-wheel slot buttons PLUS faders for sub-functions (gobo shake, rotate, scroll, etc.); focus, shapers, dimmer — all aspects of the fixture.
- **Programmer:** values persist until the programmer is cleared; every channel sits at its profile default until modified. Highlight/lowlight, locate, fan, group storing, presets per attribute family.
- **Unprofiled fixtures:** offer only what RDM reports, plus raw-DMX faders per channel for testing.
- **Output:** ONE unified output engine for everything that emits DMX (Console, Rig Check tests, Send, Universe Identify, /api/dmx) — no merging between screens; their functions fold into Console-lite. Master Arm/Disarm governs all data flow; all controls are live while armed (Arm is the confirm step). Arm drops if the controlling browser is silent for 5 s or more. Disarm blacks out. **sACN stays** alongside Art-Net, moved into the unified engine.
- **Rig Check:** folded in as a Tests panel acting on the grid selection, layered under manual values (manual wins), with chainable test sequences; old Rig Check screen retired at parity.
- **MIDI encoders:** last chunk. **Colour themes incl. custom:** later; noted in the UI brief.
- **UI:** functional, not polished; a separate design agent builds the assets from `docs/design/console-lite-ui-brief.md`.

## Chunks (each is one or more commits, gates green, docs updated)
| # | Chunk | Depends on |
|---|-------|-----------|
| C1 | GDTF importer: all channel functions, ranges, channel sets, wheels; schema bump; library re-read | — |
| C2 | Positions + layout model: MVR Matrix import, manual placement, per-show grid layout with z-layers | — |
| C3 | Unified output engine: single owner of all universes, Art-Net + sACN per universe, master Arm/Disarm with 5 s lease, blackout on disarm; migrate Rig Check, Send, Identify, /api/dmx | — |
| C4 | Programmer (server): fixture + sub-fixture selection, per-attribute values over defaults, highlight/lowlight, locate, fan, clear, groups, presets per attribute family | C1, C3 |
| C5 | Tests panel: Rig Check engine as a layer under the programmer; test sequences | C3, C4 |
| C6 | Console-lite UI (functional): grid, selection, attribute controls, raw DMX for unprofiled | C1, C2, C4 |
| C7 | Retire Send / Rig Check screens at parity | C5, C6 |
| C8 | MIDI encoders (Web MIDI, zero deps) | C6 |
| C9 | Themes (custom theme) — with the design agent's assets | C6 |

## Delivery to Dom's desktop
The desktop clone has no shell for the agent and remote tools may not write inside `.git`. Each chunk's changed files are staged under `dist\_incoming\<chunk>\` (dist/ is gitignored) with a `b512-<chunk>-console-lite.bat` in the repo root that checks the tree is clean, switches to `feat/console-lite`, copies the files in, adds by path and commits. Run the scripts in order: `b512-commit-unresponsive.bat` first (it also creates `feat/console-lite`), then C1, C2, …

## Progress
| Chunk | State | Notes |
|-------|-------|-------|
| C1 | done 2026-10-07 00:22:51 -0400 | GDTF importer: all functions/ranges/sets/wheels, schema 6, library re-read. Open owner questions: per-type profile storage (BMFL entry 41→161 KB), Rig Check use of GDTF 1.0 channel defaults, library schema bump, licence of the two Robe test extracts (libMVRgdtf, MVR SDK licence). |
| C2 | done 2026-10-07 00:44:55 -0400 | Entry.location from MVR <Matrix> (composed, mm, Z-up), schema 7; POST /api/patch/locations re-import (confirm UPDATE LOCATIONS); per-show layout in Workspace JSON with Z layers; GET/POST /api/patch/layout. Open owner questions: no-Matrix = unknown (not 0,0,0)?; 500 mm cell / 1 m layer gap defaults; re-derive refreshing its own placements; licence of the Capture demo extract. |
| C3 | next — awaiting owner answers | |
