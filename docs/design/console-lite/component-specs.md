# Console-lite component specifications

This is the implementation contract for the existing Console. The HTML mockups illustrate visual treatment; their sample interactions do not replace the production controllers. Product decisions and corrections are in [README.md](README.md). Refreshed for I2f on 2026-10-09; [revision-notes.md](revision-notes.md) separates shipped behavior from deferred proposals. The owner addendum [decisions-after-handoff.md](decisions-after-handoff.md) is the decision source. Light/custom theme previews prepare C9; production currently ships dark only.

## Shared anatomy and measurements

Every value control has: visible label → capability/scope → input surface → mono readout → source word → optional reason. Keep the label and readout visible while dragging. Use left-aligned system type, body line height 1.55, 15–16 px body, 13 px captions and 20–22 px main values. Never substitute a number for missing data. Unsupported attributes collapse to “not on these fixtures”; partially supported attributes remain operable with a count.

| Dimension | Pixels | Token / rule |
|---|---:|---|
| Minimum pressable box | 44 | `--b5-size-touch-min`, buttons and other pressable controls; horizontal fader inputs alone use 28 px on fine pointers, 44 px on coarse pointers |
| Primary movement / Arm / Disarm | 60 | `--b5-size-control-lg` |
| Test tile minimum height | 88 | `--b5-size-tile-min` |
| XY pad tablet minimum / maximum | 240 / 340 | `--b5-size-xypad-min` / `--b5-size-xypad-max` |
| Wheel slot tablet / phone | 64 / 56 | `--b5-size-slot-btn`, override to 56 px below 768 |
| Track width | 8 | `--b5-size-fader-track` |
| Thumb visible mouse / touch | 28 / 44 | `--b5-size-thumb-mouse` / `--b5-size-touch-min`; input box 28 px on fine pointers, 44 px on coarse pointers |
| Fader travel / dimmer travel | 176 / 240 | `--b5-size-fader-length` / `--b5-size-fader-dimmer-length` |
| Fixture glyph | 48 (32–64 permitted) | `--b5-size-fixture-glyph`; independent 44 px minimum pressable box |
| Icon S / M / L / XL | 14 / 18 / 24 / 32 | existing `--b5-size-icon-sm/md/lg`; new `--b5-size-icon-xl` |
| Selected / state border | 3 | `--b5-size-state-border`; idle uses existing thin 1 px token |
| Focus ring | 3, with 3 px clearance | `--b5-shadow-focus`; opaque `--b5-color-border-focus` |
| Group fader minimum column | 160 | `--b5-size-group-fader-width` |
| Control gap / panel gutter | 8 / 16 | `--b5-space-2` / `--b5-space-4`; phone gutter 12 = `--b5-space-3` |
| Corners | 3 / 999 / 0 | `--b5-radius-sm/full/none`; Arm pill, Disarm square |

New sizes belong in `benny512-tokens.css` with the other measurements. Move existing icon-size declarations from components into that token section during C9 only if doing so preserves their effective values. Do not duplicate touch minimum in component CSS.

## State contract S (applies to every value control below)

| State | Visible treatment | Accessible / interaction behavior |
|---|---|---|
| Default | hollow circle + `default`; profile-default tick, muted but ≥4.5:1 readout | `aria-valuetext` includes “profile default”; known zero remains zero |
| Active | opaque focus ring, handle grows within existing hit box | pointer capture; no layout jump; cancel capture on pointer cancel/blur |
| SET | filled dot + `SET`; family count increments | announce source on commit, not every incoming frame |
| TEST (deferred readout marker) | proposed double-border `TEST` tag | manual touch establishes SET; shipped Tests panel explains masking, but parameter TEST source tags remain deferred |
| MIXED | `MIXED`, min–max text and dashed range band | do not announce a fake average. Native slider has first selected reference value, explicitly described as reference, until first write |
| Partial | `4 of 6 selected` below label | only supported fixtures receive the value; explain omitted fixtures in details |
| Unknown | dashed box + `not reported`, no thumb position claiming a value | disable physical conversion only; known DMX control may remain active |
| Unavailable | collapsed line `not on these fixtures` | skip in tab traversal; expandable capability explanation |
| Disabled | reason in words; dashed surface, disabled text | no write; use `aria-disabled` on a focusable explanation wrapper, native disabled on input if appropriate |
| Disarmed | panel note `Disarmed · values retained, nothing sent` | controls remain editable. Arm resumes composed state. Never relabel every source as “disarmed” |
| Overrides (deferred readout markers) | proposed `HIGHLIGHT`, `GROUP`, `RAW` or `IDENTIFY` | show effective and retained values separately when implemented; Locate writes SET, with no LOCATE source |

`MIXED` and Partial are independent of source. Unknown and unavailable differ. Arm state is independent of programmer state. Icons are decorative (`aria-hidden`) beside visible words, never the sole name. All status pills contain words.

## Keyboard contract K

Native range inputs remain the accessible equivalent for every spatial control. ArrowRight/Up increases, Left/Down decreases by 1% of the valid function span (round to at least one DMX count); Shift+arrow uses one tenth of that increment (at least one count). PageUp/Down uses 10%; Home/End selects function min/max. Fine ON scales pointer displacement 1:10 and makes step buttons advance one DMX count; it acts on the complete 8/16-bit value. No wrap at min/max. The existing production XY shortcut uses Shift for coarse stepping; integration must change its handler and hint together to meet this contract.

Sliders show keyboard hints on focus/hover; this is optional help, not the only label. A numeric edit alternative permits exact values. Escape cancels an uncommitted text edit. It cannot undo motion already emitted while armed. Unmodified app shortcuts never run inside text inputs, sliders or dialogs. Tab follows visible reading order. Buttons use native Space/Enter; toggles use `aria-pressed`. No global single-key blackout shortcut is proposed without owner agreement; the permanent Disarm button is always keyboard reachable.

## Component catalogue

Each entry inherits S, K and the dimensions above unless an exception is stated. P = phone <768; T = tablet 768–1299; D = desktop ≥1300 for the optional third panel. The app's existing 900 px two-pane breakpoint remains.

### 1. Master output bar (`.b5-bigbtn`, `.b5-pill`)

Anatomy: state word/glyph, link status, Arm slot and permanently anchored Disarm slab. Universe disclosure is a deferred proposal (§2). Master sticky top; stop control rightmost and never hidden, disabled or moved, including when disarmed, reconnecting or a dialog is open. Arm has its own slot beside/above the slab. Repeating Disarm is safe. Every dialog is non-modal below the master strip, Cancel focused first including name dialogs, Escape closing and restoring trigger focus. Disarm remains reachable. The Settings full-reset shutdown overlay is the exception: the process has exited.

| Output state | Copy / shape |
|---|---|
| Disarmed | `DISARMED · no output`, outlined ARM pill; value-retention sentence |
| Arming | `ARMING…`; static pending glyph plus optional low-motion spinner; Arm cannot submit twice; Disarm remains enabled |
| Armed | `ARMED · LIVE`, closed-lock glyph, 3 px master border; DISARM / BLACKOUT square slab |
| Shared browser | `Shared control · link OK`; every connected station can operate the same programmer |
| Local link lost, server unknown | `LINK LOST · output state unknown`; local writes unavailable; “Another connected browser may still be keeping output live.” Never claim blackout |
| Server lease lost, blackout confirmed | `DISARMED · LEASE LOST`; broken link, 3 px dashed border; “All browsers went quiet. Rig blacked out. Restore the link and re-arm to resume.” |
| Server lease lost, hold confirmed | `LEASE LOST · LOOK HELD`; same dashed border, separate held word; “Last look held. Restore the link and re-arm to control.” |
| Simulated | `SIMULATED · not a real rig`; hatch always visible, same safety affordances with simulated output labels |
| Output refused | per-universe `NOT SENT · reason`; warning icon. Global state may remain armed for other universes |

Lease lost notification persists until Acknowledge; Acknowledge never Arms. Restore/reconnect does not silently Arm. Use `role=status`, polite announcements for normal transitions, `role=alert` once for lease loss/refusal, no repeated heartbeat announcements. P: 12 px state, 60 px stop. While armed, hide the show name visually, retaining its accessible name; Show tools names the show. Labels never overlap controls. Stop in-flight commands before holding a lost-lease look; hold their return values, never the command value.

### 2. Universe chip and protocol badges — deferred proposal

Label `Universe 3` via `UI.formatUser(raw)`, followed by icon + Art-Net + `UI.formatArtnet(raw)` and icon + sACN + `UI.formatSacn(raw)`. No duplicated hand conversion. State LIVE / BLACKOUT / IDLE / NOT SENT / HELD with words. NOT SENT includes the server reason in wrapping details. Chip is status text, not a false button; only disclosure uses a button/summary ≥44. P: count summary; T/D: locally scrolling strip. The two protocol glyphs intentionally have different geometry.

### 3. Fixture glyph, overlays and order badge

P/T tile marks and the Live preview toggle remain deferred; reserved sprite areas are preparation, not shipped state. 48-grid geometry reserves x=2–12,y=2–12 for order and x=34–46,y=2–12 for P/T. Body is centered below that band. `data-beam` regions allow capped colour content; absent beam metadata means no invented preview. Source files use `currentColor`; components inject actual fixture colour only in beam areas. Disable preview via a labelled `Live preview OFF/ON` toggle, default OFF.

Unselected: 1 px outline. Parent selected: 3 px outline + order badge. Cell selected: 3 px cell outline + filled top tab + cell order. Future P and T marks may coexist. Highlight: double outline + word in accessible name. Ghost: default OFF; when enabled, full-contrast dashed + “Outside layer”; not selectable while filtered. Unprofiled: dashed body + RDM word. Not on wire: no addressable byte; hatched or struck body + `Not on wire` summary. Unknown class always uses `fx-unknown`, not a guessed head. Group glyph includes count as HTML text overlay.

Use buttons in a labelled region, not an ARIA grid unless full roving-row/column navigation is implemented. Accessible name: “Pixel bar 07, cell 2 of 4, selected, order 4.” Maintain a visible text order list. Announce selection totals on change. Order badge is redundant decorative text for assistive technology. For N cells generate actual component child buttons; the sprite's four cells are a drawing reference, never four hardcoded logical cells. P/T/D use identical state grammar; if cells cannot fit, open an expanded cell bank rather than shrinking targets.

### 4. Layers, group tiles and selection tools

Layer chips are 44 px toggles with `aria-pressed` and a named z-height; unset height says `height unknown`. Filter affects drawing and selection. Select All respects visible layers; None empties selection; Invert toggles only visible fixtures. Ghost display OFF/ON is independent of selection. Group tile includes icon, name and count; selecting a group shows any members excluded by filters. Layout edit OFF by default; drag/nudge needs an explicit edit toggle so lasso does not move fixtures. Grid snap word always present.

Touch lasso uses a Lasso ON/OFF mode toggle, one-finger drag, Escape/cancel exits. It selects parents only, in reading order. Ordinary touch drag pans the plan; keyboard selection and group controls are equivalent. P: tools scroll inside their toolbar or More sheet. T/D: visible layer rail, selection tools and group rail; grid never causes body horizontal scroll.

### 5. XY pad and paired Pan/Tilt faders

Square 240–340 px T/D; P fills width minus two 12 px gutters, maximum 340. Axes labelled Pan/Tilt; centre tick, ≥44 px handle. First selected fixture is the reference, others use dashed dots; MIXED text and range remain. Pointer capture updates pad and paired faders from one model; no two sources of truth. Fine controls motion 1:10 without jumping when enabled. Missing physical range keeps DMX/% and says `no physical range in profile`.

Use a labelled `role=group` for spatial input plus two native sliders, not `role=application`. Pad keyboard arrows map x/y; Shift fine; Home/End are handled on individual faders only to avoid ambiguous simultaneous extremes. P: sliders below pad. T landscape: alongside if ≥400 px available; portrait: beside pad. D three-column: faders below. Invert/swap axes remain per-fixture properties, not global pad shortcuts.

### 6. Faders and virtual dimmers

Horizontal native faders throughout P/T/D, with label, value, scope and source. Vertical banks remain a deferred design proposal. Fine-pointer parameter input height is 28 px; coarse-pointer input height is 44 px. Buttons and quick 0 / 50% / FULL targets stay at least 44×44. K and exact numeric entry apply. Mixed writes set one absolute value on supported selection targets.

Every RGB-mixing fixture or cell without its own dimmer has a virtual dimmer, labelled “virtual (scales colour)”, including cells under a real master. Cell output = colour × virtual level × master. Whole-fixture intensity drives the master when present; select cells to adjust their own levels. A claimed virtual dimmer with unset colour uses white. A part with neither colour mixing nor a dimmer says “no dimmer”; never invent a channel. Focus/zoom/iris/edge show profile units or an honest unknown mapping.

### 7. Range-segment track and sub-functions

Each named function/set segment has proportional track geometry and a separate minimum-touch label button. Very small intervals still receive full-size labels in a scrolling rail. Truncate long labels visually but disclose full name on focus/hover and in accessible name. Mark `name from file`. Click jumps to the segment's documented default if inside the range, otherwise the first valid value; never guess the midpoint of Reset or Lamp off ranges. Server metadata from patch.IsCommandFunction determines commands: every Control-family function and Reset/Lamp on any channel. Commands never use a range, numeric Set or Fan. Reset and Lamp off use §12 dialogs; other commands use HOLD 0.75 s. Set and Fan reject command values with 422 directing the caller to /api/programmer/command.

Current segment has a thick underline + `IN`; mixed shows `IN on n of m`. Keyboard operates labels as ordinary buttons and value with K. Multiple Frost functions stay separate by actual profile name. Prism, rotation, gobo shake/scroll/index and animation use the same contract. P local horizontal rail; T/D wrap labels without compressing the track.

### 8. Wheel-slot buttons

Minimum 64×64 T/D, 56×56 P, expand vertically for label and state. Image/content, slot name, number and `IN` word; partial `IN on 3 of 6`. Missing media: generic gobo glyph + `media not reported`; missing colour: hatched/unknown disc + `colour not reported`; missing name: `Slot 4`. Open is a genuine selectable slot, not a missing-media fallback. Name-from-file marker accompanies profile names. Placeholder previews never invent actual artwork. Native buttons, `aria-pressed` only where one shared state exists; mixed state uses described count. Arrow traversal optional; Tab/Space works. Slots wrap 3 columns P, 4+ T/D; no body overflow.

### 9. Colour picker

Capability counts precede controls: RGB/W/A/UV, CMY, CTO/CTB/CTC or wheel-only. Mix/Wheel/temperature modes only appear when supported. Hue/saturation field uses the fixture's real emitters, with labelled emitter sliders as keyboard equivalent. Show “DMX values, not measured colour.” For wheel-only selection say `→ nearest slot: Congo blue` before/with the write; no continuous-mixing claim. An unknown slot colour cannot be chosen by nearest-colour matching. Swatches are content, capped by preview multiplier, state carried outside them. Mixed/partial apply per emitter. P field above sliders, T/D field and emitter bank may sit side by side. Native mode tabs with labelled panels.

### 10. Shutter/strobe segmented control

`.b5-segmented` contains OPEN and CLOSED radio choices (`radiogroup`, actual radio inputs or roving radio buttons). Closed includes crossed shutter glyph, thick outline, bold `CLOSED · beam blocked` message. Mixed says `MIXED · closed on n of m`; neither choice falsely selected. Arrows switch segments, Space selects; separate rate slider uses K and profile Hz or `Hz not reported`. Shutter state never inferred from dimmer intensity. Same P/T/D anatomy; segments each minimum 44 px.

### 11. Shaper diagram

220–240 px beam circle; actual 0–4 blades, with separate A/B endpoints only if present. Each drag handle ≥44 px and labelled; blade movement and angle resolve to actual profile channels. Selection outlines annotate Blade/Shaper number outside beam. Assembly rotate is separate. Accessible sliders duplicate every endpoint/angle and assembly channel. No `role=application`; diagram description names the clipped sides. Unknown geometry: labelled generic blade diagram with `geometry not reported`, never fake optics. P diagram over a single-column fader list; T/D two columns. No blades: collapse with `not on these fixtures`. Mixed endpoints use dashed outlines + MIXED and value range.

### 12. Fixture commands — one-shot and confirmation

Reset and Lamp off open a non-modal native dialog below the master: real targets and consequences, Cancel focused first, Escape closes and restores focus, Disarm reachable. Revalidate selection, capability, session and command before sending; changed targets require a fresh deliberate confirmation. Amber caution styling, never a numeric/fader control.

Armed copy: “Sent once: after 1 s each channel returns to its previous value. Nothing is kept in the programmer.” Disarmed copy: “Output is not live”; only Cancel is shown. Nothing is queued for Arm. Re-arming an open dialog may reveal the send action after revalidation.

Lamp On, Home position off and other Control commands keep a ≥60 px “HOLD 0.75 s · <function>” button. Space/pointer-down starts monotonic progress; release, pointercancel, blur, Escape or focus loss before threshold cancels. Fire once per press, requiring release to re-arm. Disable while disarmed with a worded reason. The 0.75 s input gesture is separate from the owner's fixed **1 s output window**. Profile timing is not imported here and must not override this decision.

POST /api/programmer/command sends once, restores previous value or profile default after 1 s, never stores/replays at Arm. Under Control status, server state is shared by every browser: “SENDING <command> to <fixtures> — … in n s”, then “DONE … the channel is back to its previous value” or “STOPPED … cut short because output stopped”. Use words and glyphs. Lease loss with Hold last look stops commands before holding their return values.

### 13. Value readout, 16-bit and source markers

Monospace, slashed zero; one DMX / % / Physical selector per browser, default %. Per-control override menus are deferred proposals; no long-press override is shipped. Physical only where profile provides a valid mapping; non-linear/multi-function mappings use the active function. Display full 16-bit `32 768 / 65 535`; optional byte view `128 · 0 (coarse · fine)`, with a `16-bit` badge. Format from complete integer then split for display. Unit and source remain visible. `output for` associates input. Do not use `aria-live` for high-frequency test values; announce source changes and a throttled focused value instead.

Shipped readouts show SET/default/MIXED/Partial; TEST and override source tags remain deferred (S). MIXED is outlined text plus range, never a fake `0`; not-reported marker dashed, wrapping reason. Readouts are not pressable unless a real value editor is exposed. P/T/D typography stays readable; shrink surrounding spacing before the font.

### 14. Attribute tabs and presets

Seven labels map to server `dimmer`, `position`, `colour`, `beam`, `focus`, `shaper`, `other` (Control). `role=tablist`, buttons with `role=tab`, roving tabindex, `aria-selected`, labelled panel; Left/Right and Home/End navigate. Active tab bold with 3 px underline. Each has icon, word, `n set`; unsupported has `—` and readable reason, skipped during arrow traversal. No split Gobo tab.

Store Control preset remains available by owner decision; command values are not held in the programmer. Do not promise that a Control preset stores or replays commands.

Preset button: family glyph + name + `applies to 6 of 8 selected` if partial. Recall writes live while armed. Store captures the active family only, name entry in labelled dialog. Existing name shows Replace / Save as new; group storage can also Merge. Destructive replacement requires explicit choice. P tabs and presets scroll in their own boxes; T/D wrap presets if space allows.

### 15. Selection action bar: Highlight, Lowlight, Locate, Fan, Clear

In flow **above the grid**, never sticky at the bottom; selection summary is not pinned. Minimum buttons 44×44, gap 8 px. Phone bar contains selection count, Clear… and More only. Count wraps cells onto a second line inside a rounded box; Highlight ON appears as glyph + words below the count. More retains its plain label. Highlight, Locate and Tests are in More; toggling Highlight/Lowlight keeps it open, revealing Previous/Next in place. Menu ends above group faders/navigation and supports 360 px phones.

Highlight stepping follows selection order. Lowlight defaults to 20% and dims only non-highlighted selection targets: a cell's own dimmer or all dimmers for a whole fixture. Highlight wins overlaps; leave a cell-less master unchanged if any cell is highlighted. Coverage copy: “Dims 2 selected cells not highlighted.” or “Dims 1 selected fixture and 1 cell not highlighted.” Highlighting all selection means nothing is lowlit.

Locate persists into the programmer as SET. Fan offers linear, reverse, centre-out and edges-in with words/glyphs and selection order; Control tab explains “Control channels are commands.” Clear preserves family-on-selection, selection values and everything scopes, with fallback-source explanation; Everything clears selection as before. Store group and presets retain explicit names/Replace/Merge/Save as new choices. All dialogs follow §12 focus behavior.

### 16. Tests, virtual dimmers, masking and Isolate

Server-driven inventory with capability counts; ≥88 px glyph tiles, worded ON/OFF, aria-pressed and separate Settings details. Unknown tests keep their file name and generic glyph. Settings expose labelled rate/min/max/phase and fade choices (default 1 s).

Dimmer tests reach whole fixtures through a real master when present, selected cells through their own virtual dimmer, and RGB-only fixtures through their own virtual dimmer. Per-fixture copy: “through its virtual dimmer (scales colour)”. Tests open tested cells' masters, including colour tests; programmer values above still win. Wherever a dimmer test drives unset colour, show white **for the test only**: whole masters over cells, virtual dimmers and real dimmer + RGB fixtures. Never store the white; release it with the test. A programmer colour wins. Explain this in Settings. Both choices were confirmed by the owner in I2f; they are no longer pending.

A masked test stays ON. Output-engine per-channel composition supplies “Overridden by SET on 2 fixtures.” or counts split between fixtures/cells, beside a glyph and dashed rule. Other overriding layers are worded “a group fader”, “Highlight”, “a fixture command”, “raw universe levels”, “Universe Identify”. Per-fixture details identify the affected attribute, e.g. “overridden by SET (Dimmer)”. This panel explanation ships; parameter TEST/override source badges remain deferred.

Isolate uses the in-flow kit caution card: “Isolate: drive only the tested channel — ON/OFF”. Untested channels of tested fixtures are held at 0; manual values still win, tested-cell masters stay open and dimmer-test white still shows. Available only Armed with a test on; otherwise explain why. It ends with tests, Disarm, either lease-loss policy (never hold isolation) or departure of its initiating browser. Then explain, e.g. “Isolate ended by itself: output was disarmed.”

Open I2f follow-ups remain in the plan: legacy test presets can load old Isolate; the test white claim currently releases immediately, with fade behavior still under review; raw/Identify changes refresh masked explanations on the next Tests read. These limitations are not new approved behavior.

P uses a sheet below Master, above bottom bars, with Close/Escape and independent scroll; restore focus to More if the menu closed. T can replace controls; D third pane may remain open. Keep Master accessible. Inventory retains existing server test IDs across Dimmer, Position, Colour, Beam, Focus and Shaper.

### 17. Sequence transport and stopped editor

Back / Next are ≥60 px primary targets with directional glyphs. AUTO says “AUTO 8 s”; on manual steps it is unavailable with a reason. The interval belongs to each step and is set in the editor, with no global transport interval. Step counter and ordered list include CURRENT word, aria-current=step and a leading rule on the normal surface. Boundaries never silently loop; unavailable steps explain their reason.

Edit is disabled while running, with “Stop the sequence to edit”. Stopped editor rows have worded Move up / Move down / Add step after / Remove step controls, ≥44 px, no drag handle or drag behavior. Keep focus on the moved step and announce its new position. Label per-step interval and distinguish test edits from programmer Clear.

### 18. Raw DMX bank

Horizontal faders throughout. Header “No profile · RDM only · 12 channels”: RDM only means a committed RDM device. Show personality/footprint only when reported. Relative channel and absolute address accompany value/source; all slot labels say “not reported” because reading RDM slots produces a profiled entry. Never fabricate a Dimmer label or infer footprint. Unknown footprint offers Read RDM info and no fake faders.

Support internally scrolling 1–64+ channel banks without body overflow. Fine-pointer parameter height 28 px, coarse-pointer 44 px; all buttons remain ≥44×44. K and exact entry apply. Raw universe Tools claim a whole universe and are separately labelled; fixture raw controls never silently take that claim.

### 19. Persistent group-fader bank

One fader per type+mode and stored group, ≥160 px column, labelled value/source/Release. SET claims dimmer; default/Released claims nothing, not zero. Last moved overlapping fader wins, programmer above wins. Use native horizontal range/K and group-specific Release names. Summary stays visible across screens; collapse never releases.

Default collapsed unless remembered by this browser or viewport ≥1300×1080. That threshold is an implementation fill-in for “height permits”, not a new owner requirement. Expand into its own scrolling bank; lower bars remain in normal flow beneath the work area.

### 20. MIDI station panel

Role-based link pill shows midi-connected/disconnected glyph with “MIDI connected” / “MIDI disconnected”. Label each mapping “Encoder n”, its selected attribute and readout. Fine uses the fine glyph and ON/OFF words; 1:10 applies to the complete value. Connection failure explains why with readable text. Layout wraps mapping rows on phones, keeping ≥44 px mapping/Fine controls and source/readout visible. Mockups are illustrative; no Web MIDI is opened. Production MIDI remains browser-local at localhost.

## Markup sketches for integration

```html
<div class="b5-param">
  <label for="pan">Pan</label>
  <span class="b5-pill">● SET</span>
  <input id="pan" class="b5-range-touch" type="range" min="0" max="65535"
         aria-describedby="pan-help" aria-valuetext="32 768 DMX, manual set">
  <output for="pan">32 768 / 65 535</output>
  <p id="pan-help" class="b5-caption">16-bit · 4 of 6 selected</p>
</div>
<div class="b5-segmented" role="group" aria-label="Selection mode">
  <button class="b5-seg is-on" aria-pressed="true">Select parent</button>
  <button class="b5-seg" aria-pressed="false">Select cell</button>
</div>
<div class="b5-actionbar">
  <button class="b5-btn" aria-pressed="false">Highlight OFF</button>
  <button class="b5-btn">Clear…</button>
</div>
<button class="b5-tile is-on" aria-pressed="true">
  <svg aria-hidden="true"><use href="/icons/benny512-icons.svg#b5-icon-test"/></svg>
  <span>Gobo wheel — step slots</span><strong>ON</strong>
</button>
<button class="b5-bigbtn b5-bigbtn--go">ARM</button>
<button class="b5-bigbtn b5-bigbtn--stop">DISARM · BLACKOUT</button>
```

## Responsive composition

360/390×844: compact master; Select/Control segmented view; selection summary and count/Clear/More action bar in flow above grid; scrollable work area; group summary and existing mobile nav stack below. Tests replaces the work area, not the master. More is a sheet with real buttons and focus restoration. When an on-screen keyboard opens, work area shrinks; stop remains visible. 200% text zoom may collapse to a single pane and internally scroll bars; never clip labels to preserve a rigid screenshot.

1024×768: 45/55 grid and programmer. Independent work-area scrolling, footer group bank can collapse for additional height. 820×1180: selection occupies roughly 40% of remaining work area, controls 60%; within the selection region tools sit beside the plan to preserve plan height. 1440×900: grid/controls/Tests at 40/38/22 proportions; each pane scrolls. Bars use `position:sticky`, never fixed. Safe-area padding is applied to the footer using `env(safe-area-inset-bottom)` in production.

