# Console-lite — UI design brief

**Created:** 2026-10-06 23:59:58 -0400
**For:** the design agent building Console-lite's icons, component specs, tokens and mockups.
**Source of truth for product decisions:** `docs/plans/console-lite.md` (owner decisions, 2026-10-06). Where this brief and that file disagree, the plan wins; tell us.
**Status:** brief only. The functional UI (chunk C6) is being built in parallel, "functional, not polished". Your assets replace its placeholders; themes (C9) and MIDI (C8) come later and use what you deliver.

You have never seen this codebase. Everything you need is in this file. Where a fact comes from the code it is stated plainly; where something is not yet decided it is marked **OPEN** and listed in §8.

---

## 1. What this is, who uses it, where

**Benny512** is a single Windows executable that serves a browser UI over the venue LAN. It talks Art-Net, sACN and RDM to the lighting rig: discovery, patching, packet analysis, DMX output, fixture testing. The operator is a **lighting technician, not a programmer**.

**Console-lite** is a new screen for **testing and flashing a rig** — "does every fixture pan, tilt, change colour, drop its gobo, zoom?" — **not show playback**. There are no cues, no timeline, no playback faders. The tech:

1. Arms output.
2. Selects fixtures on a 2D plan of the rig (or picks a group).
3. Pushes attributes directly (dimmer, position, colour, gobo, beam, focus, shaper) or runs automatic test patterns on the selection.
4. Clears, moves on to the next fixtures, disarms when done.

**Environment — design for this, not for a desk:**

- **Dark venue.** Working light is off or low; the tech's eyes are dark-adapted. A bright screen ruins that and lights up the room.
- **Tablet held at arm's length**, standing, often under a truss, other hand on a ladder or a cable. Sometimes a **phone (390 px)**. Sometimes a laptop on a road case.
- **Gloves** — fingertip precision is poor. Mis-taps move real lights over real people.
- **Keyboard** on the laptop, and soon **MIDI encoders** (chunk C8).

### 1.1 Accessibility rules (standing constraints, not preferences)

From the project's design kit (`internal/web/static/css/DESIGN.md`), quoted:

> 1. **Never colour as the sole signal.** Every state is carried by a **word** first, then an icon, then a border weight or style, and only then by hue. The test: screenshot your screen, desaturate it, and check you can still tell the states apart. If you can't, it isn't finished.
> 2. **44px minimum touch target** on anything pressable. […] Never write the literal `44px`; use the token, so a future change to the touch minimum reaches every control.
> 3. **Name the unknown, don't hide it.** A value the app doesn't have is said out loud — "addr unknown", "Not read", "name from file" — never printed as a plausible `0`. Dashed borders mean "nothing is attached here / this is not ours".
> 4. **Wide content scrolls inside its own box.** The page body must never scroll horizontally.
> 5. **Universe numbers go through `UI.formatUniverse` / `UI.parseUniverse`, always**, from the raw 0-based wire number.

> Also standing: high contrast, dyslexia-friendly type (left-aligned, generous line-height, no justified text, no all-caps runs longer than a chip label), and real keyboard-reachable controls with real labels. Use `<button>`, not a clickable `<div>`.

**Rule 5 is stale.** `formatUniverse` no longer exists; one ambiguous formatter caused a shipped off-by-one. The current formatters are each named for the numbering they return:

| Formatter | Returns | Use in Console-lite |
|---|---|---|
| `UI.formatUser(raw)` | the show's own universe number, starting at 1 (or the words "outside this show") | **primary** label everywhere an operator reads a universe |
| `UI.formatArtnet(raw)` | the Art-Net Port-Address as one number (0–32767) | secondary label on the Art-Net badge |
| `UI.formatSacn(raw)` | the sACN universe (1–63999), or a "no sACN universe" phrase | secondary label on the sACN badge |
| `UI.formatBoth(raw)` | both, for the Devices screen | not needed here |

For your mockups: show "Universe 3" as the main label and the wire numbers small beside the protocol badge ("Art-Net 2", "sACN 3"). Never show a universe number that you derived yourself; in mockups just make it plausible.

**Other standing rules that bind your designs:**

- **A pill always contains a word.** There is no icon-only state pill in this app, on purpose.
- **Go and Stop are different shapes.** The existing start control is an outlined **pill** (`.b5-bigbtn--go`); the stop control is a filled **square-cornered slab** (`.b5-bigbtn--stop`). Shape reads before hue in the dark. "The destructive/abort control is never hidden, never disabled, and never moves."
- **Red is reserved** for errors and destructive actions. **Amber** is "this makes the rig behave surprisingly" (caution), not error.
- **Icons** are `<svg><use href="/icons/benny512-icons.svg#b5-icon-NAME"/></svg>`; default rendered size 18 px (`--b5-size-icon-md`).
- **Apply-to-confirm** is the app-wide contract (stage, then commit). Console-lite's exception, decided by the owner: **all controls are live while armed — Arm is the confirm step.**

---

## 2. Screen anatomy and states

### 2.1 Regions

```
┌───────────────────────────────────────────────────────────────┐
│ A  MASTER BAR (sticky top): Arm/Disarm · state word · lease   │
│    · per-universe output strip (collapsible on phone)         │
├──────────────────────────────┬────────────────────────────────┤
│ B  SELECTION                 │ C  PROGRAMMER / CONTROLS       │
│  toolbar: layers, groups,    │  attribute tabs                │
│   parent/cell, select tools  │  Dimmer·Position·Colour·Beam/  │
│  2D grid (fixtures, groups)  │   Gobo·Focus·Shaper·Control    │
│  selection summary + order   │  controls for the active tab   │
│                              │  presets row for that family   │
├──────────────────────────────┴────────────────────────────────┤
│ D  ACTION BAR (sticky bottom): Highlight · Lowlight · Locate  │
│    · Fan · Clear · Store group · Store preset · Tests         │
└───────────────────────────────────────────────────────────────┘
   E  TESTS panel (drawer/sheet over C)   F  RAW DMX (unprofiled)
```

### 2.2 A — Master Arm/Disarm (always visible, every layout)

Arm/Disarm governs **all** DMX leaving Benny512 — Console, tests, the retiring Send screen, Universe Identify, the HTTP DMX API all run through one output engine.

Facts from the plan:
- **Arm** is the confirm step; once armed every control is live.
- **Disarm blacks out** (all universes to zero, then stop).
- **Lease:** Arm drops if the controlling browser is silent for **5 s or more**.
- **sACN stays** alongside Art-Net; each universe goes out on Art-Net, sACN, or both.

States you must design (word first — the words below are proposed; keep them short, all-caps only as chip labels):

| State | Word on the control/pill | Shape & non-colour cues | Notes |
|---|---|---|---|
| Disarmed | `DISARMED · no output` | Arm button = outlined **pill** (go shape) labelled `ARM`; no lock glyph | Default on load. Controls: see OPEN-1 |
| Arming (in flight) | `ARMING…` | spinner in the pill; button not pressable twice | brief |
| Armed | `ARMED · LIVE` | Disarm button = filled **square slab** labelled `DISARM · BLACKOUT`; **armed-lock** glyph in the state pill; thick border round the whole master bar | Disarm must never move, hide, or disable |
| Armed, held by another station | `ARMED by another station` | lock glyph + dashed border; this browser's controls read-only | OPEN-2: can a second browser take over? |
| Lease lost | `DISARMED · connection lost — rig blacked out` | **lease-lost** glyph (broken link/heart-beat), dashed thick border, persists until acknowledged | Shown when this browser's heartbeat fails or on reconnect after the server dropped Arm. Must explain in one sentence what happened and that re-arming is needed |
| Simulated | `SIMULATED · not a real rig` | hatched background (`--b5-color-bg-stale` style), **simulated** glyph | Existing offline-rehearsal mode runs on fake transport (the Workspace screen already says "SIMULATED output"). Must never be mistaken for live |
| Output refused | `NOT SENT · <reason>` | warning glyph | e.g. a universe that has no sACN number — the server refuses it |

Lease indicator: a small heartbeat glyph with the word `link OK` / `link lost`; optional seconds-since-last-ack. Do not animate it in a way that draws the eye in the dark (no pulsing fill).

### 2.3 A — Output status per universe

A strip of compact chips, one per universe in the show, collapsible to a summary on phone ("6 universes · 6 LIVE").

Each chip: `Universe 3` (formatUser) · protocol badge(s) **Art-Net** and/or **sACN** with their wire numbers · state word: `LIVE`, `BLACKOUT`, `IDLE`, `NOT SENT`. Protocol badges are text badges with an icon, not colour-coded alone; Art-Net and sACN must differ by **word and glyph**.

### 2.4 B — Selection grid

- A **simple 2D grid** (top-down plan). Fixtures and groups are placed on it either from the MVR file's position (`<Matrix>`) or manually (drag / nudge). Grid snap on/off.
- **Z-height layers** (e.g. "Floor", "Mid truss +4 m", "Upstage truss +7 m"). Layers are **both a display filter and a selection filter**: you see the active layer(s); out-of-layer fixtures are shown as ghosts or hidden (toggle); select-all respects visible layers.
- **Groups** appear as a placed tile (a group may also be placed on the grid as one object) and in a group list/rail. Tap = select the group's fixtures.
- **Multi-cell fixtures** (pixel bars, multi-head battens): a **parent** glyph containing **cells**. Two modes:
  - *Select parent* (default) — a tap selects the whole fixture; every cell follows.
  - *Select cell* — a tap selects one cell (sub-fixture). Cell selection must read differently from parent selection (e.g. parent = thick outline round the whole glyph; cell = outline + fill tab on that cell only).
- **Multi-select:** tap to add/remove, drag-lasso (touch: two-finger or a "lasso" mode toggle, OPEN-3), select all / none / invert, select by group.
- **Selection order** is kept and shown as a **number badge** on each selected glyph (1, 2, 3 …). Fan uses this order.
- **Selection summary** line: `8 selected · 2 groups · 3 cells` and the order list as text for screen readers.

Grid cell / fixture glyph states (combine; each needs a non-colour cue):

| State | Non-colour cue |
|---|---|
| Unselected | hairline outline |
| Selected (parent) | thick outline + order badge |
| Selected (cell only) | cell tab filled + order badge on cell |
| Has programmer values (touched) | corner notch / "P" tab |
| Test running on it | "T" tab or test glyph |
| Highlighted | double outline + highlight glyph |
| Out of active layer | dashed outline, ~40% opacity |
| Unprofiled (RDM-only) | dashed body + "RDM" micro-label |
| Not on the wire (universe not output / outside show) | strike or hatched body + word in tooltip/summary |
| Unknown class | generic "?" glyph, never a guessed class |

Optional live preview: the glyph's beam area may show the fixture's current colour and intensity as **content**, never as a state signal. Cap its luminance (dark-venue rule) and provide an off switch.

### 2.5 C — Programmer state

- Every channel sits at its **profile default** until touched. Once touched a value **persists until the programmer is cleared**.
- Every control must show **touched vs default**: a `SET` marker (filled dot + word) vs `default` (hollow, muted). Per tab, a count: `Colour · 3 set`.
- **Value source** can be: `default`, `SET` (manual), `TEST` (a running test pattern; tests sit *under* manual — manual wins), `HIGHLIGHT` / `LOCATE` overrides. Design a compact source tag for readouts.
- **Clear** clears the programmer (all, or per attribute family — OPEN-4). Clear is consequential (lights move) but not destructive to data; style as a strong secondary, not red.

### 2.6 D — Action bar tools

| Tool | Behaviour (as decided; details defined by chunk C4) | UI needs |
|---|---|---|
| Highlight | Drives the selection (or the current fixture when stepping) to a bright, visible state | toggle with word `HIGHLIGHT ON/OFF`; step Next/Prev through selection (OPEN-5) |
| Lowlight | Dims the non-highlighted members of the selection | toggle, word state |
| Locate | Sends the selection to a known findable state (open white, centred) | momentary action; result shows as `LOCATE` source tags |
| Fan | Spreads a value across the selection in **selection order** | mode chooser with direction variants: linear (first→last), reverse, centre-out (mirror), edges-in; plus a fan amount control |
| Clear | Clears programmer | see 2.5 |
| Store group | Saves current selection as a group | name entry dialog; replaces/merges confirmation |
| Store preset | Saves the current values of **one attribute family** as a preset | family is the active tab; name entry |

**Presets per attribute family:** each tab shows its family's presets as a row/grid of buttons (`Position · Centre`, `Colour · Deep blue`). A preset button needs: name, family icon, "applies to 6 of 8 selected" when partial.

### 2.7 E — Tests panel (Rig Check folded in)

Rig Check (the existing "Function check" screen) becomes a panel acting on the **current grid selection**, layered **under** manual values. It reuses today's tile grid: each test is a big toggle tile (≥88 px tall) with the word ON/OFF, an icon, border weight and background — four signals — plus a Settings disclosure for rate/min/max/phase.

The tests that exist today (server ids → current labels), grouped:

| Group | Tests |
|---|---|
| Dimmer | Dimmer (sine or snap waveform) · Dimmer on/off |
| Position | Move to (pan/tilt max/min/centre) · Ballyhoo (pan/tilt sweep) |
| Colour | Colour wheel — step slots · Colour mix — sweep · Colour mix — fade hues |
| Beam | Frost · Gobo wheel — step slots · Gobo wheel — rotate · Prism in/out · Prism spin · Animation wheel spin |
| Focus | Move to (focus near/far, zoom narrow/wide) · Hold (fixed value) |
| Shaper | Shapers — one at a time · Shapers — all together · Shaper assembly rotate |

The list is **server-driven** (only tests the selection supports appear; an unknown test still renders with its raw name marked "name from file"). Also existing: an **Isolate** caution choice card (drive only the tested channel; amber treatment) and a **fade time** setting (Snap, 250 ms … 30 s).

**New: test sequences** — an ordered chain of tests. Controls: **Back**, **Next**, **Auto** (advance every N seconds), a step counter `Step 3 of 7 · Gobo wheel — step slots`, and the sequence list with the current step marked by word and position, not colour. Sequence editor: add / remove / reorder steps (drag handle plus up/down buttons for keyboard).

### 2.8 F — Raw DMX for unprofiled fixtures

Fixtures with no GDTF profile offer **only what RDM reports** (personality, footprint, slot labels from RDM slot info if the fixture answers) **plus one raw fader per DMX channel**. Each raw fader: channel number (`ch 1` relative + absolute address), RDM slot label if reported, otherwise the words `not reported`. Header: `No profile · RDM only · 12 channels`. Must work for 1–64+ channels: a scrollable bank, never page-level horizontal scroll.

### 2.9 Layouts

Existing breakpoints in the app: **< 768 px** phone (bottom mobile nav), **768–899** small tablet, **≥ 900** two-pane, **≥ 1024** desktop. Design these three:

| Layout | Viewport for mockups | Structure |
|---|---|---|
| Phone | 390 × 844 portrait | Master bar compact (state word + Arm/Disarm always visible). Grid and Controls are **two views** switched by a segmented control (`Select` / `Control`), selection summary pinned. Action bar sticky bottom; overflow tools in a "More" sheet. Tests as a full-height sheet |
| Tablet | 1024 × 768 landscape **and** 820 × 1180 portrait | Landscape: grid left ~45 %, controls right. Portrait: grid top ~40 %, controls below. Action bar sticky bottom under the thumb |
| Desktop | 1440 × 900 | Grid, controls and Tests panel can all be visible; keyboard shortcuts shown on hover/focus |

Sticky bars use `position: sticky`, never `fixed` (the app has been bitten twice by fixed bars).

---

## 3. Control components

Common rules for every control:

- Hit target ≥ `--b5-size-touch-min` (44 px) on coarse pointers; primary physical-moving controls aim for **56–64 px**.
- Every control has a visible **label**, a **value readout**, and keyboard operation (arrow keys step, Shift+arrow fine, PageUp/Down coarse, Home/End min/max — propose and document).
- Focus ring uses `--b5-shadow-focus` / `--b5-color-border-focus`; must be visible on every surface.

### 3.1 State matrix (design every control in every applicable state)

| State | Meaning | Required cues (word + non-colour) |
|---|---|---|
| Idle / default | at profile default, untouched | hollow `default` marker, muted readout |
| Active (dragging/focused) | being operated | focus ring + enlarged thumb/handle |
| Touched (`SET`) | programmer holds a value | filled marker + word `SET` |
| Test-driven | a test is moving it, no manual value | `TEST` tag; readout live |
| Mixed | selection has different values | word `MIXED`, range band (min–max) on the track, readout `12–80 %` |
| Partial | only some selected fixtures have this attribute | `4 of 6` caption |
| Not reported / unknown | the fixture/profile doesn't say | dashed outline, word `not reported` (or `no physical range in profile`), no fake number |
| Not available | no selected fixture has it | control hidden or collapsed with the words `not on these fixtures` |
| Disabled | cannot be used now (e.g. read-only station) | `text-disabled` + reason word; never only greyed |
| Armed vs disarmed | output is / isn't flowing | panel-level note when disarmed (see OPEN-1); controls themselves keep their own state |

### 3.2 Components

| Component | Spec & behaviour | States / notes |
|---|---|---|
| **Pan/Tilt XY pad** | Square pad, min 240 px on tablet, full-width-minus-gutters on phone (max ~340). Crosshair, centre mark, labelled axes ("Pan", "Tilt"), handle ≥ 44 px. **Paired faders** for Pan and Tilt beside/below it; pad and faders move **in tandem** (one value, two views). Readouts in DMX / % / degrees. | Fine mode: a `FINE` toggle that scales pad travel (e.g. 1:10) and acts on the 16-bit fine byte — OPEN-6. Mixed selection: show each fixture's dot faintly, the handle at the reference (first selected). Invert/swap axes is a per-fixture property, not a pad control |
| **Fader (generic)** | Vertical bank on tablet/desktop, horizontal on phone. Track ≥ 8 px with ≥ 3:1 border contrast; thumb ≥ 44 × 44 coarse / 28 px fine pointer (existing `.b5-range-touch` behaviour). Label above, readout below, source tag (`SET`/`TEST`/`default`). | Mixed range band; 16-bit indicator; `default` tick mark on the track |
| **Dimmer fader** | Generic fader, larger (tallest in the Dimmer tab), with quick buttons `0`, `50 %`, `FULL`. | Highlight override shows as `HIGHLIGHT` source |
| **Colour picker** | Shows **what the fixture can actually do** first: a capability line `Mixes RGBW` / `CMY + wheel` / `Wheel only · 8 slots` / `CTO`. Modes as tabs: **Mix** (hue/saturation field or wheel + per-emitter faders R, G, B, W, A, UV… or C, M, Y), **Wheel** (slot buttons, 3.3), **CTO/CTB** fader. Picking a colour in the field writes the fixture's real channels (RGB, or CMY, or nearest wheel slot for wheel-only fixtures) — the UI must say which (`→ nearest slot: Congo blue`). | Mixed-capability selection: show per-capability counts (`5 mix RGB · 3 wheel only`). Caption: "DMX values, not measured colour" — no claim of colour accuracy. Swatches capped in luminance |
| **Wheel-slot button** (gobo & colour) | ≥ 64 × 64 tablet, ≥ 56 phone. Content: slot image (gobo from GDTF media) or colour swatch (from the GDTF slot colour) + slot name + slot number. **Fallback** when no media/colour: placeholder glyph + name; when no name either: `Slot 4`. Current slot = thick outline + word `IN`. | Mixed: `IN on 3 of 6`. Open slot is a real slot ("Open") |
| **Sub-function faders** | Gobo: index / rotate (cw/ccw/stop zones) / shake / scroll; prism in/out + rotate; frost (one per frost function, e.g. Frost1, Frost2); iris; animation wheel; strobe/shutter. Where the profile defines **named DMX ranges** (channel functions/sets, e.g. "Rotate CW fast → slow", "Stop", "Shake"), the fader track shows **range segments** with their names; a tap on a segment name jumps into it. | Segment labels truncate with full name on focus; named ranges come from the GDTF profile — mark `name from file` |
| **Shutter/strobe** | Two-state `OPEN` / `CLOSED` segmented control + strobe rate fader (Hz when profile gives it). | Closed shutter is a common "why is it dark" cause — make `CLOSED` loud |
| **Focus / Zoom / Iris / Edge** | Generic faders with physical units when the profile gives them (degrees for zoom, % for iris). | `no physical range in profile` |
| **Shaper / blade control** | A **shaper diagram**: the beam circle with four blades (A/B per blade where the fixture has both), each blade draggable in/out and angle; plus the assembly rotate. Faders for each blade as the accessible equivalent. | Fixtures with 0–4 blades or "Shaper" instead of "Blade" naming must both render |
| **Control channel** | Lamp on/off, reset, fan mode, display etc. — **safety**: Lamp off, Reset and any "hold for N s" function require **press-and-hold** (≥ 1.5 s with a visible fill/progress and the word `HOLD TO RESET`) or an explicit confirm — OPEN-7. | Never in the same row as frequently pressed controls. Amber caution styling, red only for lamp-off/reset if owner says |
| **16-bit value display** | Shows both bytes when relevant: `32 768 / 65 535` or `128 · 0 (coarse · fine)`. A `16-bit` micro-badge on the control. | Readout mode switch (below) |
| **Value readout** | Mono font (slashed zero). Modes: **DMX** (0–255 or 0–65535), **%**, **Physical** (°, Hz, m — only when the profile provides physical ranges). Global switch plus long-press per control. | `not reported` instead of a number |
| **Attribute group tabs** | Dimmer · Position · Colour · Beam/Gobo · Focus · Shaper · Control. Each tab: icon + word + `n set` count. Tabs the selection has no channels for show `—` and are skippable. Horizontal scroll inside its own box on phone. | Active tab: thick underline + bold + `aria-selected` |
| **Mixed indicator** | Reusable chip/marker `MIXED` with range text. | Used on faders, slots, pads, tabs |
| **Not-reported indicator** | Reusable dashed chip `not reported`. | Used for missing RDM data, physical units, slot media |
| **Raw DMX fader** | Compact fader cell for unprofiled fixtures (§2.8). | Shows RDM slot label or `not reported` |
| **Sequence transport** | Back · Next · Auto (toggle with interval) · step counter. | Auto running = word `AUTO 5 s` + icon, not colour |

Attribute grouping (from the server's taxonomy, for your tab icons): **Dimmer**; **Position** (pan, tilt, position effects); **Colour** (RGB/CMY mix, colour wheel, CTO/CTB/CTC, HSB, colour macros); **Beam** (gobo wheels, prism, iris, frost, shutter, strobe, douser, animation wheel, beam effects, fog/haze); **Focus** (zoom, focus, edge); **Shaper** (blades, shapers, barn doors); **Other** (everything else — lamp control, reset, macros). The plan's tab names "Beam/Gobo" and "Control" map to the server's `beam` and `other` groups — OPEN-8.

---

## 4. Asset list

Existing sprite: `internal/web/static/icons/benny512-icons.svg`, 22 symbols, `viewBox="0 0 24 24"`, `fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"`. New icons must match that style exactly.

**Existing icons (keep, reuse):** `nav-nodes`, `nav-devices`, `nav-rig-walk`, `nav-analyzer`, `nav-send`, `nav-settings`, `sort-asc`, `sort-desc`, `filter`, `status-warning`, `status-error`, `status-ok`, `status-pending`, `refresh`, `chevron-expand`, `chevron-collapse`, `identify`, `signal`, `network-node`, `export`, `apply`, `revert`.

Sizes: **S** 14 px (`--b5-size-icon-sm`) · **M** 18 px (`--b5-size-icon-md`, default) · **L** 24 px (`--b5-size-icon-lg`) · **XL** 32 px (new, for big buttons/tiles). All drawn on the 24 grid; must stay legible at 14 px.

### 4.1 Icons

| Icon name (`b5-icon-…`) | Purpose | Status | Sizes | Notes |
|---|---|---|---|---|
| `nav-console` | Console-lite tab in main nav | NEW | M, L | distinct from `nav-send` |
| `arm` | Arm output | NEW | L, XL | open/"ready" metaphor; used on the pill-shaped go button |
| `disarm` | Disarm + blackout | NEW | L, XL | used on the square slab; reads as "stop + dark" |
| `armed-lock` | state glyph while armed | NEW | S, M, L | appears inside the ARMED pill |
| `lease-lost` | controller link lost / lease dropped | NEW | M, L | broken link or flat heartbeat |
| `link-ok` | lease heartbeat healthy | NEW | S, M | must differ from `lease-lost` by shape |
| `simulated` | rehearsal / fake transport | NEW | M, L | e.g. dashed fixture or "sandbox" |
| `blackout` | universe chip state BLACKOUT | NEW | S, M | |
| `highlight` | highlight toggle | NEW | M, L | |
| `lowlight` | lowlight toggle | NEW | M, L | visibly the "dim" sibling of highlight |
| `locate` | locate action | NEW | M, L | crosshair/target — must differ from `identify` |
| `fan-linear` | fan first→last | NEW | M, L | direction variants are separate symbols |
| `fan-reverse` | fan last→first | NEW | M, L | mirror of linear |
| `fan-center-out` | fan mirrored from centre | NEW | M, L | |
| `fan-edges-in` | fan from edges to centre | NEW | M, L | |
| `clear` | clear programmer | NEW | M, L | not a trash can (nothing is deleted) |
| `store-group` | store selection as group | NEW | M, L | |
| `store-preset` | store family preset | NEW | M, L | |
| `group` | group object / group list | NEW | S, M | |
| `preset-dimmer` … `preset-control` | preset buttons per family (7) | NEW | S, M | family icon + small preset mark; may be composed from family icons |
| `attr-dimmer` | Dimmer tab | NEW | M, L | |
| `attr-position` | Position tab | NEW | M, L | |
| `attr-colour` | Colour tab | NEW | M, L | must not rely on fill colour |
| `attr-beam` | Beam/Gobo tab | NEW | M, L | |
| `attr-focus` | Focus tab | NEW | M, L | |
| `attr-shaper` | Shaper tab | NEW | M, L | |
| `attr-control` | Control/Other tab | NEW | M, L | |
| `gobo-placeholder` | wheel-slot button with no media | NEW | L, XL | generic gobo disc |
| `colour-slot-placeholder` | colour slot with no colour data | NEW | L, XL | outlined disc + "?" or hatch |
| `slot-open` | the Open slot on any wheel | NEW | L, XL | empty circle |
| `shutter-open` | shutter state OPEN | NEW | M, L | |
| `shutter-closed` | shutter state CLOSED | NEW | M, L | shape must differ from open, not just fill |
| `strobe` | strobe | NEW | M, L | |
| `prism` | prism | NEW | M, L | |
| `frost` | frost | NEW | M, L | |
| `iris` | iris | NEW | M, L | |
| `zoom` | zoom | NEW | M, L | |
| `focus` | focus | NEW | M, L | |
| `shaper` | shaper/blade | NEW | M, L | |
| `gobo-rotate-cw` / `gobo-rotate-ccw` / `gobo-shake` | sub-function chips | NEW | S, M | |
| `lamp` | lamp control | NEW | M, L | used with hold-to-confirm |
| `reset` | fixture reset | NEW | M, L | must not resemble `refresh`/`revert` |
| `layer-up` / `layer-down` | step active z-layer | NEW | M, L | |
| `layers` | layer list / filter | NEW | M | |
| `grid-snap` | snap on/off | NEW | M | state carried by word too |
| `select-parent` | select whole multi-cell fixture | NEW | M, L | |
| `select-cell` | select individual cell | NEW | M, L | |
| `select-all` / `select-none` / `select-invert` / `lasso` | selection tools | NEW | M | |
| `seq-back` / `seq-next` | sequence step | NEW | L, XL | |
| `seq-auto` | auto-advance | NEW | L | |
| `test` | tests panel / "T" glyph state | NEW | S, M, L | |
| `fine` | fine-mode toggle | NEW | M | |
| `midi-connected` / `midi-disconnected` | MIDI encoder state (C8) | NEW | S, M | differ by shape (e.g. slash) |
| `proto-artnet` | Art-Net badge | NEW | S, M | used **with** the word "Art-Net" |
| `proto-sacn` | sACN badge | NEW | S, M | used **with** the word "sACN"; shape differs from Art-Net |
| `theme` | theme switch | NEW | M | |
| `rdm-only` | unprofiled / RDM-only marker | NEW | S, M | |
| `drag-handle` | reorder sequence steps | NEW | M | |
| `status-ok`, `status-warning`, `status-error`, `status-pending`, `identify`, `chevron-*`, `apply`, `revert`, `filter`, `refresh` | general states and actions | EXISTING | as now | reuse; do not redraw |

### 4.2 Fixture glyphs (grid)

Top-down plan glyphs, drawn on a 48 px grid (scales to 32–64 px), stroke-only, `currentColor`, with a reserved **beam area** (for the optional colour preview) and reserved corners for state tabs (order badge top-left, `P`/`T` tabs top-right).

| Glyph | Class | Notes |
|---|---|---|
| `fx-moving-head` | moving head (spot/profile/beam with pan/tilt) | yoke visible |
| `fx-wash` | wash (moving or static) | wide lens |
| `fx-profile` | static profile / ellipsoidal | barrel + shutter hint |
| `fx-pixel-bar` | multi-cell bar / batten | parent outline + N cells; cells individually addressable |
| `fx-strobe` | strobe | |
| `fx-dimmer` | generic dimmer / conventional | |
| `fx-unknown` | class not known | "?"; never substitute a guessed class |
| `fx-group` | a group placed on the grid | stacked outline + count |

How a fixture's class is derived (from its profile's attributes) is not yet built; design for the case where it cannot be decided — `fx-unknown`, never a guess.

### 4.3 Components to spec

XY pad · fader (vertical, horizontal, compact raw-DMX cell) · dimmer fader with quick buttons · range-segment track · wheel-slot button · colour picker (mix field, emitter faders, capability line) · shaper diagram · shutter segmented control · hold-to-confirm button · attribute tab bar · value readout with mode switch and source tag · `MIXED` / `not reported` / `SET` / `TEST` markers · master Arm/Disarm bar · universe chip with protocol badges · fixture glyph with all state overlays · selection-order badge · group tile · preset button · test tile (existing kit, restyled only if needed) · sequence transport + step list · layer chips.

---

## 5. Theming

Today the app is hard-set to `data-theme="dark"`; a light theme exists in the token file but is not exposed. The owner wants **colour themes, including a user-defined custom theme**, later (chunk C9). Everything you deliver must make that possible without redrawing anything.

### 5.1 Rules

1. **No baked colours in SVG.** Strokes and fills use `currentColor` only. A second tone, if needed, uses `currentColor` with `opacity`, or a CSS custom property set by the component — never a hex.
2. **No baked colours in component specs.** Reference semantic role tokens (`--b5-color-text-primary`, `--b5-color-bg-surface`…), never palette ramps (`--b5-color-accent-500`) and never hex.
   *Known gap you should design around:* several existing components (`.b5-bigbtn--go/--stop`, pill tones, `.b5-range-touch` thumb, `.b5-statecard.is-armed`) reference ramp tokens directly, and the light theme only overrides roles. So a theme that redefines only roles would not recolour them. Your token additions should be **roles**, so C9 can route these components through them.
3. Each state remains distinguishable with colour removed (word + shape + border style). A custom theme can therefore never make ARMED look like DISARMED, because the difference was never carried by colour.

### 5.2 Token roles a theme must define

Existing roles (keep names): `--b5-color-bg-canvas`, `-bg-surface`, `-bg-surface-raised`, `-bg-surface-overlay`, `-bg-inset`, `-bg-hover`, `-bg-selected`, `-bg-stale`; `--b5-color-text-primary/-secondary/-muted/-disabled/-accent/-info/-danger/-success/-warning/-on-accent/-on-danger`; `--b5-color-border-subtle/-default/-strong/-accent/-danger/-focus`; `--b5-shadow-focus`.

New roles to propose (names in the `--b5-*` scheme; adjust if you have better, but keep the pattern):

| Role group | Proposed tokens |
|---|---|
| Tone fills & borders (so components stop using ramps) | `--b5-color-ok-border`, `-ok-fill`, `-warn-border`, `-warn-fill`, `-danger-border`, `-danger-fill`, `-accent-border`, `-accent-fill` |
| Armed state | `--b5-color-armed-border`, `--b5-color-armed-fill`, `--b5-color-armed-text`, `--b5-color-disarm-slab-bg`, `--b5-color-disarm-slab-text`, `--b5-color-arm-pill-border`, `--b5-color-arm-pill-text` |
| Simulated / lease | `--b5-color-simulated-bg` (pattern), `--b5-color-simulated-text`, `--b5-color-lease-lost-border` |
| Fixture states (grid) | `--b5-color-fixture-idle`, `-fixture-selected`, `-fixture-cell-selected`, `-fixture-touched`, `-fixture-test`, `-fixture-highlight`, `-fixture-ghost`, `-fixture-unprofiled`, `-fixture-offline` |
| Values | `--b5-color-value-set`, `-value-default`, `-value-test`, `-value-mixed-band` |
| Grid | `--b5-color-grid-bg`, `-grid-line`, `-grid-line-major`, `-grid-layer-label` |
| Controls | `--b5-color-track`, `-track-border`, `-thumb`, `-thumb-border`, `-xypad-bg`, `-xypad-crosshair` |
| Preview | `--b5-preview-max-brightness` (0–1 multiplier for live colour swatches) |
| Sizes (not themed, but new) | `--b5-size-icon-xl` (32px), `--b5-size-control-lg` (56–64px), `--b5-size-tile-min` (88px, today a literal), `--b5-size-xypad-min`, `--b5-size-slot-btn`, `--b5-size-fader-track`, `--b5-size-fixture-glyph` |

Note: icon size tokens currently live in `benny512-components.css`, not the token file; say where you'd put new size tokens.

### 5.3 Contrast requirements (WCAG ratios, checked against every surface level each role can sit on)

| Role | Minimum | Current dark palette (for reference, against surface → overlay) |
|---|---|---|
| Primary text | **7:1** | 17.5 → 14.8 ✓ |
| Secondary text, readouts, captions | **4.5:1** | secondary 9.8 → 8.3 ✓; **muted 5.0 → 4.2 ✗ on overlay** |
| Tone text (ok/warn/danger/info/accent) | **4.5:1** | ok 6.9, warn 7.2, danger 6.3, info 5.4 → 4.6, accent 9.9 ✓ |
| Control boundaries, track borders, icons, focus ring, state borders (WCAG 1.4.11) | **3:1** | focus 7.4 ✓; **border-default 1.4 ✗, border-strong 2.9 ✗, danger-500 border 2.9 ✗** — fine for decoration, **not** for a fader track or a control outline |
| Text on Disarm slab / on accent fill | **4.5:1** (aim 7:1) | on-danger slab 6.8 ✓ |
| Disabled | no minimum, but must be identified by **word** | 1.8 |

So: new control boundaries must not use `border-default`/`border-strong` as-is. Propose a role (e.g. `--b5-color-border-control`) that meets 3:1.

### 5.4 Custom-theme validation (what C9 will enforce — design the rules, we build the checker)

A custom theme is **refused** (not saved) unless:

1. Every role pair in 5.3 meets its minimum on every surface level it is used on.
2. Focus ring ≥ 3:1 against every surface **and** against `--b5-color-bg-selected`.
3. `armed`, `danger`, `warn`, `ok` and `accent` borders are each ≥ 3:1 against the surfaces they sit on, and are not identical to each other (minimum hue/lightness separation — propose a number).
4. Canvas/surface stay dark enough for a dark venue in any "dark" theme (propose a maximum luminance for `bg-canvas`/`bg-surface`; current canvas ≈ 0.5 % relative luminance).
5. Shape/word coding is not themeable: themes change colour only. ARMED/DISARMED, OPEN/CLOSED, SET/default stay distinguishable in greyscale by construction.

The checker reports failures in words ("Muted text on overlay is 3.9:1, needs 4.5:1"), never only by highlighting a swatch.

---

## 6. Deliverables expected back

Put them in `docs/design/console-lite/` (proposed; one folder):

1. **SVG icon set** — one file of `<symbol id="b5-icon-NAME" viewBox="0 0 24 24">` entries, ready to append to `benny512-icons.svg`. 24 px grid, 2 px safe padding, stroke 1.75 (match existing), round caps/joins, `currentColor` only, no fills except where a shape must be solid (then `fill="currentColor"`), no `<style>`, no ids inside symbols that could collide. A preview sheet (PNG or HTML) at 14/18/24/32 px on dark and light backgrounds and in greyscale.
2. **Fixture glyphs** — same rules, 48 grid, with the state-overlay layers shown.
3. **Component specs** — for each component in §3/§4.3: anatomy, measurements in px **and** the token each value maps to, all states from §3.1, keyboard behaviour, ARIA role/labels, phone/tablet/desktop variants. Markup sketches using the existing kit classes where one exists (`.b5-btn`, `.b5-pill`, `.b5-tile`, `.b5-param`, `.b5-segmented`, `.b5-actionbar`, `.b5-bigbtn`).
4. **CSS token additions** — a single block named in the `--b5-*` scheme with dark-theme values, light-theme values, and a note per token on which contrast rule it satisfies.
5. **Screen mockups** — 390 × 844, 1024 × 768, 820 × 1180, 1440 × 900. At least: disarmed empty; armed with a mixed selection of moving heads and a pixel bar (cell selection visible); Colour tab with mixed capabilities; Beam tab with gobo slots including one with no media; Shaper tab; Tests panel with a running sequence; lease-lost; simulated; an unprofiled fixture's raw DMX bank.
6. **Dark-venue check** — every mockup viewed at minimum device brightness and desaturated, with a short note per screen: what stays readable, what was changed. Flag any area brighter than `bg-surface-overlay` larger than a button.
7. **Contrast table** — computed ratios for every new role in both themes.

No external fonts, libraries or images: the app has zero external dependencies. Gobo and colour-slot imagery at runtime comes from the fixture profiles; your placeholders are the fallback.

---

## 7. Non-goals

Cue lists, playback, timecode, effects engine beyond the existing test patterns, 3D visualisation, fixture-profile editing.

---

## 8. Open questions for the owner

1. **Disarmed controls** — editable (programmer changes held, nothing sent) or locked until Arm?
2. **Second browser** — can another station see/take over an armed session, or is it read-only?
3. **Lasso on touch** — mode toggle, two-finger drag, or not needed?
4. **Clear** — all-only, or also per attribute family / per selection?
5. **Highlight** — step Next/Prev through the selection one fixture at a time?
6. **Fine mode** — wanted for the XY pad and faders? Ratio?
7. **Lamp off / Reset safety** — press-and-hold, confirm dialog, or both?
8. **Tab names** — "Beam/Gobo" and "Control" (server groups `beam`, `other`), or split Gobo from Beam?
9. **Mixed values** — moving a fader on a mixed selection: jump all to one value, or move relative?
