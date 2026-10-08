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

## Owner decisions (Dom, 2026-10-08)
- **Arm is a functional gate only**, not a stage that primes values. Empty programmer → fixtures at profile defaults. Priority (high → low): cues (future; selectable priority) > programmer > profile defaults. Tests stay under the programmer; raw universe and Identify stay above it. Disarm keeps all state; Arm resumes it. (2026-10-08 08:25:29 -0400)
- **Raw universe faders** keep their whole-universe claim.
- **Group faders** (owner answers 2026-10-08 14:55:16 -0400): each fader SETS dimmer level 0–100% for its fixtures (not a scaling master); sits below the programmer; one fader per fixture type+mode automatically (two modes = two faders) plus one per stored group; always visible at the bottom of the screen. Orchestrator fill-ins: order base < tests < group faders < programmer; an untouched fader claims nothing, a moved fader claims its fixtures' dimmer until Released; a fixture in two moved faders follows the most recently moved; bar visible on every screen, collapsible.
- **Virtual dimmer** (owner): fixtures with RGB mixing but no dimmer channel get a virtual dimmer that drives the RGB LEDs directly. Orchestrator fill-in: it scales the composed colour everywhere (Console dimmer, group faders, highlight, lowlight); with no colour set it means white.

## Design owner clarifications (2026-10-08 16:32:39 -0400)

For the design package, Dom chose touch lasso mode toggle; Previous/Next highlight stepping; 10× fine mode; separate Clear family and Clear all (preserve the existing selection-only scope as well); Beam/Gobo and Control labels; mixed faders set a single absolute value. **Lamp off and Reset use confirmation dialogs**, superseding the earlier 0.75 s pointer-hold interaction for these commands when the design is integrated. Other profile-timed commands retain their actual DMX dwell requirements. These are design decisions, not changes already shipped to the functional UI.

## Chunks (each is one or more commits, gates green, docs updated)
| # | Chunk | Depends on |
|---|-------|-----------|
| C1 | GDTF importer: all channel functions, ranges, channel sets, wheels; schema bump; library re-read | — |
| C2 | Positions + layout model: MVR Matrix import, manual placement, per-show grid layout with z-layers | — |
| C3 | Unified output engine: single owner of all universes, Art-Net + sACN per universe, master Arm/Disarm with 5 s lease, blackout on disarm; migrate Rig Check, Send, Identify, /api/dmx | — |
| C4a | Programmer core (server): selection (fixture, cell, group, layer, order kept), attribute-level set with function/range/set resolution, base-defaults source, clear, mode-master handling, live sync to every browser | C1, C3 |
| C4b | Programmer tools: highlight/lowlight, locate, fan, group store (cells), presets per attribute family | C4a |
| C5 | Tests panel: Rig Check engine as a layer under the programmer; test sequences | C3, C4 |
| C6a | Console screen shell, selection grid (layers, groups, cells, Unplaced, objects), layout edit mode | C2b, C4 |
| C6b | Attribute controls (XY pad + faders, colour picker, wheel/gobo slots, sub-function faders, focus/zoom/shapers, control), programmer toolbar, raw DMX panel | C6a |
| C7 | Retire Send / Rig Check screens at parity | C5, C6 |
| C8 | MIDI encoders (Web MIDI, zero deps) | C6 |
| G1 | Virtual dimmer for RGB fixtures without a dimmer channel | done 2026-10-08 15:35:44 -0400 (2ea6c65) |
| G2 | Group faders server side (source, API, WS) | done 2026-10-08 15:35:44 -0400 (6587121) |
| G3 | Bottom fader bar UI | done 2026-10-08 15:35:44 -0400 (48308c3, aa94085) |
| C9 | Themes (custom theme) — with the design agent's assets | C6 |

## Delivery to Dom's desktop
The desktop clone has no shell for the agent and remote tools may not write inside `.git`. Each chunk's changed files are staged under `dist\_incoming\<chunk>\` (dist/ is gitignored) with a `b512-<chunk>-console-lite.bat` in the repo root that checks the tree is clean, switches to `feat/console-lite`, copies the files in, adds by path and commits. Run the scripts in order: `b512-commit-unresponsive.bat` first (it also creates `feat/console-lite`), then C1, C2, …

## Progress
| Chunk | State | Notes |
|-------|-------|-------|
| Design assets | delivered 2026-10-08 16:32:39 -0400 | `docs/design/console-lite/`: 72 icons, 8 fixture glyphs, specs, dark/light semantic tokens, 44 responsive mockups, 1,240 contrast pairs pass, 132 browser geometry checks pass. C9 production integration still pending; physical minimum-brightness and Go gates unavailable here. |
| C1 | done 2026-10-07 00:22:51 -0400 | GDTF importer: all functions/ranges/sets/wheels, schema 6, library re-read. Open owner questions: per-type profile storage (BMFL entry 41→161 KB), Rig Check use of GDTF 1.0 channel defaults, library schema bump, licence of the two Robe test extracts (libMVRgdtf, MVR SDK licence). |
| C2 | done 2026-10-07 00:44:55 -0400 | Entry.location from MVR <Matrix> (composed, mm, Z-up), schema 7; POST /api/patch/locations re-import (confirm UPDATE LOCATIONS); per-show layout in Workspace JSON with Z layers; GET/POST /api/patch/layout. Open owner questions: no-Matrix = unknown (not 0,0,0)?; 500 mm cell / 1 m layer gap defaults; re-derive refreshing its own placements; licence of the Capture demo extract. |
| C1b | done 2026-10-07 15:25:44 -0400 | Profile cache per fixture type/mode (entries reference it); GDTF 1.0 channel-level defaults used by Rig Check/programmer; library schema bump |
| C2b | done 2026-10-07 15:25:44 -0400 | Layout editing: unplaced fixtures auto-placed so they're selectable, auto/manual flag per placement (re-derive moves only auto), objects (truss, label, area, reference mark; extensible) |
| C5 | done 2026-10-07 23:31:17 -0400 (acb0b2a) | Rig Check → Tests layer on the programmer selection (claims only scoped fixtures' channels — deliberate change from whole-universe takeover), test sequences |
| C6a | done 2026-10-07 23:31:17 -0400 (a68c1ab) | Console screen + nav, layout grid (layers, groups, cells, Unplaced, objects), selection via /api/programmer/select, edit mode off by default, summary bar (clear, store group, highlight, locate). Gaps: layout edits don't sync live to other browsers (reload button); glyphs placeholder; at 390 px the strip overlaps the header and the summary bar is large. |
| C7 | done 2026-10-08 08:15:26 -0400 | Console Tools: any-universe raw faders + park (Send parity), Universe Identify; then retire Send screen and the Patch→Rig Check view |
| C8 | done 2026-10-08 08:15:26 -0400 | MIDI encoders via Web MIDI (secure context: works on the station via localhost, not over plain-http LAN) |
| C6b | done 2026-10-08 00:21:30 -0400 (bbe397e) | attribute controls, programmer toolbar (fan, presets, clear by family, lowlight %), raw DMX panel |
| C6c | done 2026-10-08 00:21:30 -0400 (072c4cf) | Tests panel UI on /api/tests (ad-hoc + sequences), live layout sync to all browsers (layout revision + WS), 390 px fixes (strip overlapping header, oversized summary bar) |
| C4b | done 2026-10-07 16:25:21 -0400 (027ba16) | Highlight overlay (above programmer, below raw), lowlight scale 20% default, locate, fan linear/reverse/mirror, groups with cells, per-family presets with by-type recall. |
| C4a | done 2026-10-07 16:07:13 -0400 (123e3aa) | Layering decided by orchestrator: base (profile defaults for every patched channel) < tests < programmer (touched channels only) < raw < identify, so tests stay visible under untouched programmer channels. |
| C3 | done 2026-10-07 15:34:55 -0400 | (engine: session.DMXOutputEngine is the only DMX sender; sources tests<programmer<raw<identify; strip ARM/DISARM; GET/POST /api/output; Settings: lease-loss action, sACN NIC, per-universe protocols). Open owner questions: Send claims whole universe; protocols per install vs per show; Disarm keeps sources; preset load live; fold Identify lease into master; remove unused sacn.Sender; Send action bar overlaps tab bar at 390 px (pre-existing). Previous notes: Unified output engine. Owner answers 2026-10-07: one station, one output; many browsers control the same programmer; Arm lease kept alive by ANY connected browser, lost only when all are silent ≥5 s; lease-loss action is a Settings choice (hold last look / blackout); Disarm on any browser always blacks out; Art-Net AND sACN simultaneously per universe, each with its own network adapter. |
