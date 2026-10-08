# Console-lite component specifications

This is the implementation contract for the existing Console. The HTML mockups illustrate visual treatment; their sample interactions do not replace the production controllers. Product decisions and corrections are in [README.md](README.md).

## Shared anatomy and measurements

Every value control has: visible label → capability/scope → input surface → mono readout → source word → optional reason. Keep the label and readout visible while dragging. Use left-aligned system type, body line height 1.55, 15–16 px body, 13 px captions and 20–22 px main values. Never substitute a number for missing data. Unsupported attributes collapse to “not on these fixtures”; partially supported attributes remain operable with a count.

| Dimension | Pixels | Token / rule |
|---|---:|---|
| Minimum pressable box | 44 | `--b5-size-touch-min`, all pointer types; invisible hit area may surround a smaller visible mark |
| Primary movement / Arm / Disarm | 60 | `--b5-size-control-lg` |
| Test tile minimum height | 88 | `--b5-size-tile-min` |
| XY pad tablet minimum / maximum | 240 / 340 | `--b5-size-xypad-min` / `--b5-size-xypad-max` |
| Wheel slot tablet / phone | 64 / 56 | `--b5-size-slot-btn`, override to 56 px below 768 |
| Track width | 8 | `--b5-size-fader-track` |
| Thumb visible mouse / touch | 28 / 44 | `--b5-size-thumb-mouse` / `--b5-size-touch-min`; hit box always minimum touch size |
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
| TEST | double-border `TEST` tag, current readout | manual touch establishes SET and wins over the test; show the masked test in its panel |
| MIXED | `MIXED`, min–max text and dashed range band | do not announce a fake average. Native slider has first selected reference value, explicitly described as reference, until first write |
| Partial | `4 of 6 selected` below label | only supported fixtures receive the value; explain omitted fixtures in details |
| Unknown | dashed box + `not reported`, no thumb position claiming a value | disable physical conversion only; known DMX control may remain active |
| Unavailable | collapsed line `not on these fixtures` | skip in tab traversal; expandable capability explanation |
| Disabled | reason in words; dashed surface, disabled text | no write; use `aria-disabled` on a focusable explanation wrapper, native disabled on input if appropriate |
| Disarmed | panel note `Disarmed · values retained, nothing sent` | controls remain editable. Arm resumes composed state. Never relabel every source as “disarmed” |
| Overrides | `HIGHLIGHT`, `LOCATE`, `GROUP`, `RAW` or `IDENTIFY` as applicable | show the effective source and the retained manual value separately; do not imply manual control has taken effect under an override |

`MIXED` and Partial are independent of source. Unknown and unavailable differ. Arm state is independent of programmer state. Icons are decorative (`aria-hidden`) beside visible words, never the sole name. All status pills contain words.

## Keyboard contract K

Native range inputs remain the accessible equivalent for every spatial control. ArrowRight/Up increases, Left/Down decreases by 1% of the valid function span (round to at least one DMX count); Shift+arrow uses one tenth of that increment (at least one count). PageUp/Down uses 10%; Home/End selects function min/max. Fine ON scales pointer displacement 1:10 and makes step buttons advance one DMX count; it acts on the complete 8/16-bit value. No wrap at min/max. The existing production XY shortcut uses Shift for coarse stepping; integration must change its handler and hint together to meet this contract.

Sliders show keyboard hints on focus/hover; this is optional help, not the only label. A numeric edit alternative permits exact values. Escape cancels an uncommitted text edit. It cannot undo motion already emitted while armed. Unmodified app shortcuts never run inside text inputs, sliders or dialogs. Tab follows visible reading order. Buttons use native Space/Enter; toggles use `aria-pressed`. No global single-key blackout shortcut is proposed without owner agreement; the permanent Disarm button is always keyboard reachable.

## Component catalogue

Each entry inherits S, K and the dimensions above unless an exception is stated. P = phone <768; T = tablet 768–1299; D = desktop ≥1300 for the optional third panel. The app's existing 900 px two-pane breakpoint remains.

### 1. Master output bar (`.b5-bigbtn`, `.b5-pill`)

Anatomy: state word/glyph, link status, Arm slot, permanently anchored Disarm slab, universe disclosure. Master sticky top; stop control rightmost and never hidden, disabled or moved, including when disarmed, reconnecting or a dialog is open. Arm has its own slot beside/above the slab. Repeating Disarm is safe. Modal integration must preserve an always-operable stop affordance outside the inert region, or duplicate the same clearly labelled stop within each modal at the same screen position; do not trap an operator away from emergency output control.

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

Lease lost notification persists until Acknowledge; Acknowledge never Arms. Restore/reconnect does not silently Arm. Use `role=status`, polite announcements for normal transitions, `role=alert` once for lease loss/refusal, no repeated heartbeat announcements. P: 12 px state, 60 px stop, collapsible universe strip. T/D: expanded chips; label never overlaps the control.

### 2. Universe chip and protocol badges

Label `Universe 3` via `UI.formatUser(raw)`, followed by icon + Art-Net + `UI.formatArtnet(raw)` and icon + sACN + `UI.formatSacn(raw)`. No duplicated hand conversion. State LIVE / BLACKOUT / IDLE / NOT SENT / HELD with words. NOT SENT includes the server reason in wrapping details. Chip is status text, not a false button; only disclosure uses a button/summary ≥44. P: count summary; T/D: locally scrolling strip. The two protocol glyphs intentionally have different geometry.

### 3. Fixture glyph, overlays and order badge

48-grid geometry reserves x=2–12,y=2–12 for order and x=34–46,y=2–12 for P/T. Body is centered below that band. `data-beam` regions allow capped colour content; absent beam metadata means no invented preview. Source files use `currentColor`; components inject actual fixture colour only in beam areas. Disable preview via a labelled `Live preview OFF/ON` toggle, default OFF.

Unselected: 1 px outline. Parent selected: 3 px outline + order badge. Cell selected: 3 px cell outline + filled top tab + cell order. P and T may coexist. Highlight: double outline + word in accessible name. Ghost: dashed + “Outside layer”; not selectable while filtered. Unprofiled: dashed body + RDM word. Not on wire: hatched or struck body + `Not on wire` summary. Unknown class always uses `fx-unknown`, not a guessed head. Group glyph includes count as HTML text overlay.

Use buttons in a labelled region, not an ARIA grid unless full roving-row/column navigation is implemented. Accessible name: “Pixel bar 07, cell 2 of 4, selected, order 4, programmer set.” Maintain a visible text order list. Announce selection totals on change. Order badge is redundant decorative text for assistive technology. For N cells generate actual component child buttons; the sprite's four cells are a drawing reference, never four hardcoded logical cells. P/T/D use identical state grammar; if cells cannot fit, open an expanded cell bank rather than shrinking targets.

### 4. Layers, group tiles and selection tools

Layer chips are 44 px toggles with `aria-pressed` and a named z-height; unset height says `height unknown`. Filter affects drawing and selection. Select All respects visible layers; None empties selection; Invert toggles only visible fixtures. Ghost display OFF/ON is independent of selection. Group tile includes icon, name and count; selecting a group shows any members excluded by filters. Layout edit OFF by default; drag/nudge needs an explicit edit toggle so lasso does not move fixtures. Grid snap word always present.

Touch lasso uses a Lasso ON/OFF mode toggle, one-finger drag, Escape/cancel exits. Ordinary touch drag pans the plan; keyboard selection and group controls are equivalent. P: tools scroll inside their toolbar or More sheet. T/D: visible layer rail, selection tools and group rail; grid never causes body horizontal scroll.

### 5. XY pad and paired Pan/Tilt faders

Square 240–340 px T/D; P fills width minus two 12 px gutters, maximum 340. Axes labelled Pan/Tilt; centre tick, ≥44 px handle. First selected fixture is the reference, others use dashed dots; MIXED text and range remain. Pointer capture updates pad and paired faders from one model; no two sources of truth. Fine controls motion 1:10 without jumping when enabled. Missing physical range keeps DMX/% and says `no physical range in profile`.

Use a labelled `role=group` for spatial input plus two native sliders, not `role=application`. Pad keyboard arrows map x/y; Shift fine; Home/End are handled on individual faders only to avoid ambiguous simultaneous extremes. P: sliders below pad. T landscape: alongside if ≥400 px available; portrait: beside pad. D three-column: faders below. Invert/swap axes remain per-fixture properties, not global pad shortcuts.

### 6. Generic fader, dimmer, Focus/Zoom/Iris/Edge

Label, source tag, track, default tick, optional mixed band, thumb, mono value and capability line. T/D support vertical banks with 176 px travel and `aria-orientation=vertical`; P uses horizontal. Compact panels may use horizontal throughout, as the mockups do. Dimmer has 240 px travel when vertical, large value, separate quick 0 / 50% / FULL buttons ≥44. Quick buttons SET immediately while armed. Virtual dimmer says `virtual · RGB` and exposes the same state grammar.

Focus/zoom/iris/edge use this component with profile units; never assume degrees/% for an unknown physical mapping. Range step uses valid function range. K applies, plus exact numeric entry. Unknown physical does not disable known raw DMX. Mixed movement sets all supported selected targets to the new absolute value, never relative deltas.

### 7. Range-segment track and sub-functions

Each named function/set segment has proportional track geometry and a separate minimum-touch label button. Very small intervals still receive full-size labels in a scrolling rail. Truncate long labels visually but disclose full name on focus/hover and in accessible name. Mark `name from file`. Click jumps to the segment's documented default if inside the range, otherwise the first valid value; never guess the midpoint of Reset or Lamp off ranges. Control commands route through confirmation, regardless of whether reached by a slot, range or numeric input.

Current segment has a thick underline + `IN`; mixed shows `IN on n of m`. Keyboard operates labels as ordinary buttons and value with K. Multiple Frost functions stay separate by actual profile name. Prism, rotation, gobo shake/scroll/index and animation use the same contract. P local horizontal rail; T/D wrap labels without compressing the track.

### 8. Wheel-slot buttons

Minimum 64×64 T/D, 56×56 P, expand vertically for label and state. Image/content, slot name, number and `IN` word; partial `IN on 3 of 6`. Missing media: generic gobo glyph + `media not reported`; missing colour: hatched/unknown disc + `colour not reported`; missing name: `Slot 4`. Open is a genuine selectable slot, not a missing-media fallback. Name-from-file marker accompanies profile names. Placeholder previews never invent actual artwork. Native buttons, `aria-pressed` only where one shared state exists; mixed state uses described count. Arrow traversal optional; Tab/Space works. Slots wrap 3 columns P, 4+ T/D; no body overflow.

### 9. Colour picker

Capability counts precede controls: RGB/W/A/UV, CMY, CTO/CTB/CTC or wheel-only. Mix/Wheel/temperature modes only appear when supported. Hue/saturation field uses the fixture's real emitters, with labelled emitter sliders as keyboard equivalent. Show “DMX values, not measured colour.” For wheel-only selection say `→ nearest slot: Congo blue` before/with the write; no continuous-mixing claim. An unknown slot colour cannot be chosen by nearest-colour matching. Swatches are content, capped by preview multiplier, state carried outside them. Mixed/partial apply per emitter. P field above sliders, T/D field and emitter bank may sit side by side. Native mode tabs with labelled panels.

### 10. Shutter/strobe segmented control

`.b5-segmented` contains OPEN and CLOSED radio choices (`radiogroup`, actual radio inputs or roving radio buttons). Closed includes crossed shutter glyph, thick outline, bold `CLOSED · beam blocked` message. Mixed says `MIXED · closed on n of m`; neither choice falsely selected. Arrows switch segments, Space selects; separate rate slider uses K and profile Hz or `Hz not reported`. Shutter state never inferred from dimmer intensity. Same P/T/D anatomy; segments each minimum 44 px.

### 11. Shaper diagram

220–240 px beam circle; actual 0–4 blades, with separate A/B endpoints only if present. Each drag handle ≥44 px and labelled; blade movement and angle resolve to actual profile channels. Selection outlines annotate Blade/Shaper number outside beam. Assembly rotate is separate. Accessible sliders duplicate every endpoint/angle and assembly channel. No `role=application`; diagram description names the clipped sides. Unknown geometry: labelled generic blade diagram with `geometry not reported`, never fake optics. P diagram over a single-column fader list; T/D two columns. No blades: collapse with `not on these fixtures`. Mixed endpoints use dashed outlines + MIXED and value range.

### 12. Fixture commands and hold-to-confirm variant

Owner choice in this chat: **confirmation dialog** for Lamp off and Reset. Separate caution section away from frequent controls. Button `Reset…` opens non-modal native `<dialog>` labelled “Reset 2 fixtures?”; list actual targets and consequences, Cancel focused initially, explicit “Reset fixtures” action. Escape cancels. Return focus to trigger. Keep the master operable; this dialog does not make the page inert. Revalidate selection, capability, session and target command before send; if changed, refresh the dialog and require another deliberate confirmation. Amber, not red, for these controls. Repeated keyboard activation cannot bypass the dialog.

For a profile-declared “hold for N seconds” function, confirmation accepts the command intent; the engine must still execute the profile's timed DMX behavior. A pointer hold duration is not that DMX dwell time. Preserve the server's function timing.

Optional hold component is specified for existing non-Reset/Lamp-off controls: 60 px button, labelled `HOLD 0.75 s · <function>`, outlined progress fill using semantic accent roles, no pulsing. Continuous Space or pointer-down starts monotonic timer; release, pointercancel, blur, escape or focus loss before threshold cancels; one fire per press; release required to re-arm. Show seconds and progress, never fill alone. Alternative confirmation for users unable to sustain a hold. Do not change the owner's 0.75 s value silently to the brief's older 1.5 s. S applicability: busy, available/partial, disabled-with-reason; default/MIXED numeric states belong to its associated channel readout.

### 13. Value readout, 16-bit and source markers

Monospace, slashed zero; global DMX / % / Physical selector plus per-control override menu on a labelled button. Long-press may open the same menu but cannot be the only route. Physical only where profile provides a valid mapping; non-linear/multi-function mappings use the active function. Display full 16-bit `32 768 / 65 535`; optional byte view `128 · 0 (coarse · fine)`, with a `16-bit` badge. Format from complete integer then split for display. Unit and source remain visible. `output for` associates input. Do not use `aria-live` for high-frequency test values; announce source changes and a throttled focused value instead.

Markers SET/default/TEST follow S. MIXED is outlined text plus range, never a fake `0`; not-reported marker dashed, wrapping reason. Readouts are not pressable unless a real value editor is exposed. P/T/D typography stays readable; shrink surrounding spacing before the font.

### 14. Attribute tabs and presets

Seven labels map to server `dimmer`, `position`, `colour`, `beam`, `focus`, `shaper`, `other` (Control). `role=tablist`, buttons with `role=tab`, roving tabindex, `aria-selected`, labelled panel; Left/Right and Home/End navigate. Active tab bold with 3 px underline. Each has icon, word, `n set`; unsupported has `—` and readable reason, skipped during arrow traversal. No split Gobo tab.

Preset button: family glyph + name + `applies to 6 of 8 selected` if partial. Recall writes live while armed. Store captures the active family only, name entry in labelled dialog. Existing name shows Replace / Save as new; group storage can also Merge. Destructive replacement requires explicit choice. P tabs and presets scroll in their own boxes; T/D wrap presets if space allows.

### 15. Action bar: Highlight, Lowlight, Locate, Fan, Clear

Sticky bottom above group bar/navigation, 44 px minimum, 8 px gaps. Highlight toggle says ON/OFF, exposes Previous/Next that follow selection order (non-wrapping, announces “last of n”); Lowlight value defaults to existing 20%, applies only to the non-highlighted members of selection. Locate is momentary and results carry LOCATE source. Fan chooser shows linear/reverse/centre-out/edges-in glyph plus word, start/end or amount controls and selection order. Map supported server shapes explicitly; edges-in requires a server mapping/extension, never silently reinterpret it as linear.

Clear opens a scope chooser: “<family> on selection”, “Selection values”, “Everything”. Explain fallback sources; Everything also clears selection per existing behavior. Strong secondary styling, not red. No automatic extra Apply step. P keeps Highlight, Locate, Clear, Tests and More; Lowlight, stepping, Fan and storage in More. T/D expose additional tools without moving Disarm.

### 16. Test tile, settings and Isolate

Server-driven test inventory; show supported tests only with support count. Unknown test displays raw name + `name from file` and generic test glyph. 88 px tile with word ON/OFF, icon, 1/3 px border, surface change. Toggle `aria-pressed`; Settings is separate `details`, never a nested button. Settings rate/min/max/phase with real labels; fade choices Snap, 250 ms…30 s, current default 1 s. Manual values win; masked test remains ON with “overridden by SET on n fixtures.” Isolate uses existing `.b5-choicecard--caution`, amber + explanation, not error styling.

Inventory groups: Dimmer (sine/snap, on/off); Position (Move to, Ballyhoo); Colour (wheel steps, mix sweep, hue fade); Beam (Frost, gobo slots/rotate, prism in/out/spin, animation spin); Focus (Move to, Hold); Shaper (one/all blades, assembly rotate). Preserve server IDs and names. P full working-area sheet below Master and above bottom bars, independent vertical scroll; close restores trigger focus. T drawer replaces controls; D third column can remain open. Master output is accessible throughout.

### 17. Sequence transport and ordered editor

Back / Next are 60 px primary step targets using distinct directional glyphs. Auto toggle says `AUTO 5 s` when on; interval input with seconds. Step counter `Step 3 of 7 · Gobo wheel`; `<ol>` with `aria-current=step` and visible CURRENT prefix, 3 px leading rule. Back/Next stop at ends; do not silently loop. Empty sequence says “No steps”; Auto unavailable with reason. Changing selection rechecks support; unsupported steps visibly skipped with reason, or pauses if none can run.

Editor rows have a selected step, drag handle and ≥44 px Move up / Move down / Remove / Add buttons. Maintain focus on moved step and announce new position. Reordering the running current step preserves its identity; removing it pauses before continuing. Clear labels explain test edits versus programmer clear. Same editor P/T/D with rows wrapping; no drag-only path.

### 18. Raw DMX bank

Header `No profile · RDM only · 12 channels`; personality/footprint only when reported. Each `.b5-param` names relative ch, absolute address, RDM slot label or `not reported`, 0–255 value and source. Do not infer channel count from an assumed personality. Bank handles 1 to 64+ channels with internal vertical/horizontal scroll; P two-column compact horizontal controls, T/D configurable vertical bank with at least 160 px cell width if full labels require it. “Not reported” text may wrap. Native sliders follow K. Unknown footprint refuses to invent faders; offers Read RDM info. Raw universe Tools are separately labelled because they claim the whole universe; fixture raw faders must not silently use whole-universe claims.

### 19. Persistent group-fader bank (plan addition)

One fader per type+mode plus each stored group. 160 px min column, label including mode, value, source and Release. SET means the fader claims dimmer; default/Released means no claim, not zero output. Overlaps use last moved. Readout can show `overridden by programmer` per affected count. Native range and K; Release button has group-specific accessible label. Always-visible collapsible summary on every screen; expansion scrolls inside its bank. P and portrait default collapsed, desktop open when height permits; tablet landscape starts collapsed. Collapse does not release. Multiple lower bars stack in normal flow under one sticky footer.

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

390×844: compact master; Select/Control segmented view; pinned selection summary; scrollable work area; sticky action, group summary and existing mobile nav stack. Tests replaces the work area, not the master. More is a sheet with real buttons and focus restoration. When an on-screen keyboard opens, work area shrinks; stop remains visible. 200% text zoom may collapse to a single pane and internally scroll bars; never clip labels to preserve a rigid screenshot.

1024×768: 45/55 grid and programmer. Independent work-area scrolling, footer group bank can collapse for additional height. 820×1180: selection occupies roughly 40% of remaining work area, controls 60%; within the selection region tools sit beside the plan to preserve plan height. 1440×900: grid/controls/Tests at 40/38/22 proportions; each pane scrolls. Bars use `position:sticky`, never fixed. Safe-area padding is applied to the footer using `env(safe-area-inset-bottom)` in production.

