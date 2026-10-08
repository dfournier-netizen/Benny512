# Console-lite design package

Start with [index.html](index.html): eleven scenes at **390 × 844**, **1024 × 768**, **820 × 1180** and **1440 × 900**. Select dark/light, greyscale and a dim-screen approximation. Open a screen on its own for responsive review. [assets.html](assets.html) shows every icon at 14/18/24/32 px, glyphs at 32/48/64 px and fixture overlays.

These are local design artifacts, with illustrative interactions and sample data. They do not connect to output. The production Console and output engine have not been changed. SVG files are ready for integration; the mockup script is not production control code.

## Deliverables

| File | Handoff |
|---|---|
| `console-icons.svg` | 72 new, independently styled symbols; append the contents of `defs` to the existing sprite, retaining all existing symbols |
| `fixture-glyphs.svg` | Eight 48-grid symbols; `data-beam` and `data-cell` mark reserved areas; dynamic cells require inline component geometry |
| `assets.html` | Dark/light/greyscale size and state proof sheet |
| `component-specs.md` | Anatomy, dimensions, state matrix, keyboard/ARIA, responsive behavior and markup contracts |
| `console-tokens.css` | Semantic roles, required fixes to existing roles, dark/light values and new sizes |
| `index.html`, `screen.html` | 44 exact-viewport mockups via eleven scene choices × four frames; includes both lease-loss policies and fixture-command confirmation |
| `theme-contract.md` | Custom-theme validation, luminance limits, pair scopes and token integration |
| `contrast.md`, `contrast.json` | Computed worst-pair table plus every measured pair, both themes |
| `verification.md` | Browser checks, dark-venue review and remaining physical-device verification |
| `build-assets.mjs` | Reproducible zero-dependency asset/contrast generator |

Serve the repository over local HTTP (external SVG references are unreliable under `file:`). Run `node docs/design/console-lite/serve.mjs` and open [the design review](http://127.0.0.1:8765/docs/design/console-lite/). The server binds only to loopback. Rebuild generated assets with `node docs/design/console-lite/build-assets.mjs`.

## Decisions and brief corrections

Source precedence: today's direct owner answers, then `docs/plans/console-lite.md`, then this package, then the older brief. Confirmed in this chat: touch lasso toggle; highlight Previous/Next; 10× fine movement; family/all clear; Beam/Gobo and Control tab names; absolute mixed-value writes; **confirmation dialog for Lamp off and Reset**. This last answer supersedes the earlier 0.75-second hold decision for those commands; the functional UI still uses 0.75 s pending integration.

| Brief statement | Correct design behavior |
|---|---|
| One controlling browser; other stations read-only | All connected browsers share the programmer. Show “Shared control”; no exclusive owner/takeover mode |
| This browser missing a heartbeat blacks out | Server loses the lease only after **all** browsers are silent for ≥5 s. A local broken link must not claim the server blacked out |
| Lease lost always means blackout | Respect Settings: Blackout or Hold last look. Distinct LOST/HELD words; acknowledge the message separately from re-arming |
| Disarmed control editing OPEN | Arm is a functional output gate. Values remain editable and retained; Arm resumes composed values; empty programmer uses defaults |
| Clear scope OPEN | Existing API has family-on-selection, selection values and everything. Preserve all three; label scope explicitly |
| C6, C8, group faders not complete | C6/C8 exist; bottom group-fader bar is required on every screen, collapsible, with Release and source explanation |
| Fine mode only manipulates fine byte | Scale travel 1:10 over the complete value. Carry across byte boundaries; never edit the fine byte independently |
| Faded ghosts at ~40% | Use full-contrast dashed ghost outlines plus words; opacity reduction would fail essential boundary contrast |

Group-fader priority is defaults < tests < group faders < programmer; highlight overrides programmer, raw-universe and Identify are higher. Group faders SET dimmer, never scale it. Last moved overlapping fader wins. Untouched/Released claims nothing. Virtual RGB dimmer scales the composed RGB result, using white when colour is unset.

## Integration boundaries

Use existing kit classes and production event/state plumbing. Replace placeholders in the production sprite and Console only in a separate integration change. Route kit ramp references through the semantic roles before exposing light/custom themes. Use `UI.formatUser`, `UI.formatArtnet`, `UI.formatSacn` for real universe labels; the mockup numbers are explicit fictional labels, not a replacement formatter.

The mockups demonstrate view/tab switching, sliders, dialog focus, sequence stepping, local toggles and state presentation. They are not a complete selection engine, XY/shaper editor, automatic sequence scheduler or MIDI implementation. These behaviors are specified in `component-specs.md` for the functional UI.
