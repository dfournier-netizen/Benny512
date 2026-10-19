# Console Workspace — W1 component addendum

Basis: [ui-brief_2026-10-18_2249.md](ui-brief_2026-10-18_2249.md), owner decisions recorded there, then the current plan. Extends the existing Console component specifications; this package changes presentation only. Production implementation and API work await owner review. Laptop layouts cover 1024×768, 1280×800, 1440×900 and 1920×1080. Phone/tablet keep production behavior; do not apply these breakpoints to them yet.

The old Console-lite package is historical: C9/C9c Dark, Daylight and custom themes now ship, I2g resolved the old test follow-ups, and J1c/J1d define Master × parts. Those later decisions win. This workspace does not reopen them.

## Shared grammar

Native buttons and inputs; real labels, visible focus, normal reading order. ON = check symbol + 2 px accent border + bold + pressed surface, with `aria-pressed=true`. OFF has no check, regular weight and 1 px boundary. No “Lasso OFF/ON” word suffix. ON/OFF words remain on Test tiles because that existing component is unchanged. A glyph never supplies the sole label.

| Measurement | Fine pointer | Coarse pointer |
|---|---:|---:|
| Toolbar button | 38–40 px high; explicit labels | ≥`--b5-size-touch-min` in both axes |
| Permanent Disarm slab | 60 px high | 60 px high |
| ARM pill | ≥44 px high | ≥44 px high |
| Floating title bar | 48 px | 52 px; action targets ≥44×44 |
| Floating window | ≥280×140; max workspace minus 16 px | same; body owns scrolling |
| Resize corner | 28 px with 20 px glyph | ≥44×44 corner |
| Dock edge | 16 px hit strip with visible 4 px grip | 44 px transparent strip, centered on boundary |
| Dock width | 440 default; 320–60% of workspace | same on supported laptop widths |
| Fixture tile at zoom 1 | 92×64; floor 86×64 | ≥104×72; actual column floor grows for cells |
| Cells row | +30 px; ≥22 px segments | +52 px; each segment ≥44×44 |
| Body padding / gaps | 12 / 8 px | same; never shrink text for targets |

The coarse dock edge overlays only the gutter; do not put a selectable tile or window action beneath its 44 px hit strip. On coarse devices leave 22 px clearance on either side of the separator. All menus and windows scroll internally, and the page body has no horizontal or vertical document scroll.

## 1. Header and output cluster (§4.1)

One row: brand, show/adapter, connection, flexible space, output cluster, Show menu. The inset output group is state word/glyph → reserved ARM slot → Disarm slab. Never wrap it, hide Stop, disable it, or move Stop between output states. Keep the ARM slot even while live so Stop does not shift. `Show` holds Show tools and Fixture library; the show remains available in accessible names when visually shortened.

| Width | Composition / drop order |
|---|---|
| 1920 | Full show, adapter, Connected · Shared control, output and Show |
| 1440 | Same; show truncates within its own slot; preview-only notes may use spare space |
| 1280 | Full connection text; omit preview-only notes; limit show slot before touching output |
| 1024 | Adapter and Shared control suffix absent; Connected remains; show slot may ellipsize; brand word and Disarm remain |

For unusually long state words, reserve output width by shortening show first, then hiding the show visually with its accessible name intact. Never shorten ARMED, LIVE, LEASE LOST, LOOK HELD or output-unknown meaning. Retain all existing state words, lease-loss acknowledgement, Shared control semantics, dark-at-Arm and safety behavior. `LINK LOST` never asserts server blackout. Daylight is a supported production theme, not a future preview.

Keyboard: Show opens an accessible menu; Escape closes and restores its button. Master remains operable while menus, windows or non-modal questions are open. Focus order follows the row. Markup: header, labelled output `role=group`, `button.arm`, permanently present `button.stop`.

## 2. Workspace toolbar (§4.2)

Order: layers, Plan/Compact, Fit/−/+, Lasso, Add, Select, View, flexible space, Windows. Layer segments include name, z-height and count; unknown height says “height unknown”. Use `aria-pressed` for each segment. Fit starts ON; manual zoom turns it OFF. Restore remembered spacing per browser across Console and Tests, without storing it in the show.

**1024 proposal:** replace the long layer segment strip with a labelled “Active layer” native selector (All layers, Truss 6.4 m · 44, Floor 0 m · 14, Unplaced · 0). Keep Plan/Compact words, Fit, labelled zoom glyph buttons, Lasso, Add, Select, View and Windows on one row. Omit redundant Spacing label, separators and redundant decorative icons first. Keep View separate rather than nesting another submenu. At 1280+, use layer segments. Coarse targets remain 44 px; if translated labels exceed the row, allow that toolbar's own horizontal scroll rather than body overflow or smaller targets.

Select menu: all fixtures, this layer, none, invert, pick whole fixtures/cells. View: Ghosts, Names on tiles, Live values on tiles checkbox items, Edit layout, Reload layout from MVR. Windows: four checkboxes with docked/floating suffix plus Reset window positions. Disclosures use `aria-haspopup=menu`, `aria-expanded`, Up/Down and Home/End within menu, Escape returning to trigger; Tab remains ordinary. A checked menu uses the check symbol and `aria-checked`; do not depend on an invisible checkbox state.

Lasso selects parents in reading order; Add changes selection aggregation, never output. Edit layout remains explicit. Reload MVR retains existing confirmation and manual-placement rules. Unknown/unplaced fixtures remain selectable. Preview actions never change a real layout.

## 3. Toggle component (§4.3)

`<button class="btn" aria-pressed="true"><svg aria-hidden="true"><use href="console-workspace-icons.svg#b5-icon-check"/></svg>Lasso</button>`. Keep enough horizontal room for the check so toggling does not change button width or push the toolbar. In greyscale, check/bold/border retain ON. Use an opaque role border; disabled controls have a worded explanation and cannot silently toggle. Space/Enter toggles once. No attention animation.

## 4. Dock and floating window (§4.4)

Right dock only is the W1 proposal. Default: Controls open/docked at 440 px; Order, Groups/presets and MIDI hidden. Those open floating initially. Any open window can Dock or Float using a worded title-bar action. Dock disappears when no open docked window remains; grid immediately regains its width and Fit recomputes. Grid and Fit never include space behind a dock.

Docked stack order: Controls → Selection order → Groups → MIDI. Expanded windows share available height equally after collapsed title bars; each body scrolls independently. Collapsed takes title-bar height only and retains all values, tests and selection. Dock width is one browser-local value; Left increases it 20 px, Right decreases it 20 px on the focused separator. Clamp to 320…60%; expose min/max/current width in the separator's accessible description and announce the new width on commit. Markup can use a labelled button with this keyboard contract; production may use a proper focusable separator with `aria-valuenow/min/max`.

Floating chrome: drag grip, visible title/subtitle, Dock, collapse/expand, close. Title bar is a focus stop saying “Controls window; arrows move by 10 px, Shift+arrows by 40 px”. Pointer-down brings front, including when interacting with its inputs. Keyboard focus entering a floating panel also brings it forward, without trapping focus. Window regions are labelled sections, not modal dialogs; never make the page inert. Hidden windows leave focus order; close restores Windows. Reopening focuses the window title bar. Collapse keeps focus on its own toggle; controls in the folded body leave focus order.

Drag only on title-bar background, not its action buttons. Keep the entire visible window inside workspace, including on viewport shrink; cap width/height first, then clamp x/y. Floating max height is workspace −16; scrolling belongs to body. On a drag within 40 px of the right edge, show the dashed, worded “Release to dock on the right” target with normal low-luminance fill. Release docks; pointercancel/Escape restores prior geometry and removes target. Dock button is the keyboard/non-drag equivalent. Float starts at the former dock edge and stays within workspace.

Bottom-right resize grip is a native button labelled with pointer/keyboard instructions. Arrows change width/height by 20 px, minimum 280×140, maximum remaining workspace. Keep its corner visible when body scrolls. On coarse pointers, the 44 px grip has matching body-bottom clearance. A “resize” review scene highlights that grip; it is a static inspectable example, not an ongoing pointer capture.

Per-browser memory, versioned separately from show data: open/hidden, docked/floating, collapsed, float x/y/width/height and dock width. Clamp stale geometry after zoom/resize. Reset positions restores only these UI choices; it never releases faders, clears selection or turns tests off. Preview scenes use separate storage namespaces so examples do not overwrite one another.

Boundary uses `--b5-workspace-window-border` against grid/window/selected surfaces ≥3:1 in both themes. Dock uses `--b5-workspace-dock-border`. Shadow is decorative, never the only boundary. Drop target has dashed ≥3:1 border, words, no animation. Fill stays at existing surface/overlay luminance in Dark.

## 5. Grid, Plan/Compact and Fit (§4.5)

Plan uses existing coordinates, including hand placements. Compact removes empty rows and packs each occupied row left-to-right in plan order; it never wraps a fixture into a different semantic row or mutates stored positions. Stable tie-break is existing reading order/FID. All layers stack independently with a header per layer. It applies on Console and Tests, sharing spacing choice and selection.

Fit uses only grid pane width and height, excluding dock/Tests panel, toolbar, selection row and footer. Plan zoom .85–1.6; Compact .8–1.6. Tile floors win over fit: “Fit” means best permitted fit, not a promise to shrink unreadably. Scroll internally when the layer exceeds that floor. Cells determine tallest row; coarse cell width raises the whole grid column floor, avoiding overlap with the next tile. Changing dock size, visible panels, names or cell target form recomputes Fit.

Grid ground uses existing grid-bg; column lines use decorative grid-line, never an essential location cue. Layer header: bold name, count and “plan spacing from MVR” / “compact — packed in plan order, real spacing ignored”. Keep header text within pane width even when its canvas scrolls. Empty Unplaced: “No unplaced fixtures — every fixture has a position from the MVR.” No invented coordinates. Empty selection is different from empty layer.

## 6. Live fixture tile (§4.6; extends old §3)

Top: 20 px fixture glyph, 15 px bold FID, selection-order badge, optional programmer-colour swatch. Next: 12.5 px short type name (GDTF ShortName with existing fallback), ellipsis only on name. FID never truncates: widen column minimum for a long FID before laying out the layer. Parent is a real button with `aria-pressed`, cells are sibling buttons, never nested in it. HTML fixture region can contain all of them.

Intensity track sits below name/cells with mono percentage and SET/TEST/HL mini-tags. The fill uses a low-luminance surface tint, a high-contrast moving edge, and text ≥7:1 on **both** track and fill. Do not reuse bright text-accent as a large fill. Tags: 10–11 px bold, opaque tag-bg, 1 px outline; TEST uses dashed outline. Full word meanings belong in accessible label/details, e.g. “highlighted”. Text, source and state are independent.

| Tile state | Non-colour evidence |
|---|---|
| Selected parent | 3 px outline, numbered order badge, pressed state |
| Selected cell | 2 px segment outline, its own order number, pressed state |
| Programmer values | SET tag; it may coexist with a stronger effective source |
| Test effectively driving | TEST tag and dashed external outline |
| Highlighted | HL tag plus ring and accessible “highlighted” |
| Ghost / unprofiled / offline | Full-contrast dashed/hatched cues and explicit word in accessible name/details |
| Missing telemetry | “not reported”, no fake zero fill |

Fine-pointer cell segment ≥22 px; coarse form ≥44×44, expanding tile/column and row. If too many cells to fit the permitted column, open a labelled expanded cell bank with ≥44 px targets; keep the parent selectable and expose selected-cell count. Never silently drop segments or compress them below target floor. The review shows a four-cell fixture; production must use actual cell count.

Names OFF removes only the short-type line; keep FID, glyph, order and accessible full type. Live values OFF removes swatch/intensity numeric fill, but essential selection/TEST/HL cues must remain in an independent status rail/accessible label. Do not hide the fact a test is running with the preview. The colour swatch proposal is **programmer colour**, labelled “Programmer colour set”; it does not claim to be fixture-emitted colour or effective test white. It is content, capped by `--b5-preview-max-brightness` in Dark (C9c: Daylight 1). Do not dim its boundary or surrounding text.

**Production data contract, still unverified:** consume engine-composed fixture/cell intensity (Master × parts using geometry rules) and source ownership. Programmer values alone cannot supply this. Mixed multi-part output needs a range or MIXED with reported bounds, not an invented average. When disarmed, use separately identified Arm-preview composition and label “intensity 75 %, not on the wire”; never claim measured output. Missing engine data must show “not reported”. Preview scene percentages/source tags are authored fictional examples. No new endpoint/schema is approved by this design.

## 7. Selection bar (§4.7)

Count pill: check + solid boundary + bold “3 selected”; empty: dashed “Nothing selected”, regular weight. Summary groups by short type and **contiguous** FID runs, e.g. Wash 201–212 (12) · Pixel bar 401.2. Noncontiguous IDs must not be collapsed to a misleading run. Ellipsis only on visible summary; full text in title and accessible name.

Primary actions remain Clear…, Highlight toggle, Locate, Level 20 %…, More. At 1024 reduce summary width first; keep all five labelled actions in one row (show full summary on focus/title). More: Lowlight, Fan, Highlight previous/next, Store group, Open Tests for selection. Existing Clear scopes, absolute writes, one-shot command routing, persistent Locate and per-target Lowlight remain unchanged. Questions remain non-modal below header, Cancel initially focused, Escape restoring trigger, Disarm always reachable. Do not use this redesign to alter output behavior.

## 8. Controls window body (§4.8)

Seven existing family tabs: icon, word, n set. 440 px: seven columns; <420 px: four plus three on second row, never horizontally clipped labels. At 320 px dock width, title subtitle hides before title or actions. Floating minimum is 280 px; same tab wrapping and internally scrolling body. At maximum 60% dock width, preserve content max widths for spatial fields rather than stretching controls to unusable travel. Keyboard roving tabs: Left/Right, Home/End; panel has `role=tabpanel` and `aria-labelledby`.

Only the selected tab body exists. Primary controls remain visible; “Channel details — modes, delay, raw DMX” starts closed. Fold mode slots, delay ranges and low-level channel details, not the main Dimmer/Position operation. No accordion stacks of all seven families.

| Tab | 320/440/max-width body contract |
|---|---|
| Dimmer | Master multiplier + quick 0/50/FULL + Fine, then labelled part faders; no overwriting parts. Full short type/part names remain accessible |
| Position | XY reference and paired labelled native Pan/Tilt; vertical stacking at 320/440; alongside only when adequate width; first selected reference and MIXED |
| Colour | Capability counts, colour content field, labelled emitter faders; rows wrap. Wheels remain selectable with honest missing media |
| Beam/Gobo | Slots wrap, named ranges scroll within their rail, OPEN/CLOSED words + glyphs; unknown Hz/physical values explicit |
| Focus | Focus and beam Zoom separately labelled; do not confuse with grid zoom icons |
| Shaper | Numbered diagram plus matching A/B faders and assembly rotate, capability-specific, scroll internally |
| Control | Commands never appear on Set/Fan faders; Reset/Lamp off confirmation, other functions HOLD 0.75 s, fixed 1 s output, disarmed no queue; retained Store Control preset. Raw DMX/Tools disclosure below |

The scenes show each family at all four viewports and a 320 px dock. Preview part anatomy is illustrative; real controls derive only from supported selection capabilities. Existing keyboard/fine, physical conversions, mixed absolute writes, virtual dimmers, part geometry and default-at-Arm behavior retain production contracts.

## 9. Tests tab (§4.9)

Main navigation: Console then Tests with new nav-tests glyph. Shared engine selection and layer/spacing choices; moving between tabs never clears selection or turns tests off. Left grid plus selection bar/Plan–Compact. W1 proposes full Lasso/Add/Select tools here, sharing their state with Console. At 1024 the right panel is 420 px, at 1440+ up to 560; grid gets remainder. Toolbar/selection may wrap inside the left region; no document growth.

Right, independently scrollable: All tests off; shared-selection sentence; Scope (Selection / Stored group / Layer / All fixtures); Fade; separate Dimmer / Position / Colour / Beam / Focus / Shaper headings and tiles with real support counts; Isolate; Sequences. Keep tile four-signal ON/OFF grammar and separate Settings. Running sources annotate grid tiles even when Controls is closed.

Existing test behavior is binding: programmer wins, masked tests remain ON with source explanation, unset-colour dimmer tests show white for test only, tested cell masters open, white/virtual dimmers fade per I2g, Isolate is transient Armed-only and never restored from legacy preset. Back/Next 60 px and per-step AUTO interval; no running edits or drag reorder. This package does not change tests or implement a new test scheduler.

## Proposed resolutions / integration checklist

| Brief item | W1 proposal |
|---|---|
| OPEN-W1 extension | Right dock only; no left dock competing with the grid |
| OPEN-W2 | Raw DMX/Tools remain in Controls disclosures for now; no extra default window |
| OPEN-T1 | Full Lasso/Add/Select on Tests, sharing state with Console |
| OPEN-K1 | No new global single-letter shortcuts; menu and focused title/grip arrows provide keyboard paths without firing while typing |
| OPEN-L1 | Programmer-colour swatch, explicitly labelled; effective intensity/source uses verified engine data separately |

These are proposed answers for owner review, not newly confirmed owner decisions. Verify the intensity/source API before production integration; the preview's fictional data is not evidence of engine support. Keep phone/tablet unchanged until separately designed. Integrate new symbols additively into the production sprite; never replace existing IDs or transfer embedded metadata.
