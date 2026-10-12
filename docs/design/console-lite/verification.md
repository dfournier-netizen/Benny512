# Design verification

The original D0 review below is historical evidence from 2026-10-08. The refreshed standalone package follows I2f; it does not certify integrated production behavior.

## I2f refresh — 2026-10-09

- 18 scenes at 360×844, 390×844, 1024×768, 820×1180 and 1440×900, each in dark, light preview and greyscale/dim: **270 layout checks passed**. No body horizontal overflow, Disarm inside viewport, no grid/summary overlap and no visible button/select/summary below 44×44. Parameter inputs honor the fine-pointer 28 px exception; the coarse-pointer 44 px rule remains specified but physical touch hardware was not exercised. See `evidence/refresh-layout-checks.json`.
- Refreshed dark screenshots use `evidence/refresh-390-<scene>.jpg` for all scenes, plus seven desktop additions. Original filenames and `layout-checks.json` describe D0, not the new layouts.
- 360 px command interaction: disarmed Reset focuses Cancel, hides Send, disables HOLD with a reason; Escape restores Reset. Arming exposes the one-second confirmation copy. Disarm remains enabled. More stays open after Highlight, shows Highlight ON under the count and ends above the footer. Additional interaction evidence records focus and bounds.
- Visual review corrected portrait layer/group rows collapsing under grid sizing. Sequence CURRENT now uses a leading rule and normal surface, removing D0's brighter-row exception. P/T and universe-chip proposals are omitted from baseline mockups.
- All five package scripts, including the server, pass Node syntax checks; the asset generator runs successfully and `git diff --check` passes. Semantic palette/SVG definitions remain unchanged; the generator only adds deferred-state labels to the asset proof sheet. The 1,240 contrast pairs still pass.
- Go/gofmt remain unavailable on PATH; required Go gates and disposable production `--demo` build cannot run here. Browser checks use the loopback standalone server. No production code, runtime configuration or show data changed. Device brightness/glove and screen-reader checks remain outstanding.
- Light/custom previews remain C9 preparation. Resolved I2e decisions and open I2f follow-ups are listed in `revision-notes.md`; they are not inferred from sample mockups.

## Original D0 evidence (historical)

## Automated and browser evidence

- 72 new interface symbols + eight fixture symbols parse as XML; 80 unique IDs. Existing sprite remains intact. New symbols have no style elements or descendant IDs and use `currentColor`/none.
- 126 role/theme rows, **1,240 contrast pairs**, zero failures. `contrast.json` records all pairs and unrounded values; `contrast.md` is the summary. Both themes pass the proposed 0.04 OKLab tone-separation threshold; `theme-validation.json` records all 20 tone pairs and dark surface luminance.
- All five JavaScript files pass `node --check`; asset generator completes successfully; `git diff --check` passes for tracked changes.
- **132 responsive render checks**: eleven scenes × four viewports × dark/light/greyscale-dim. Exact widths/heights, no page-body horizontal overflow, Disarm within viewport, no selection-summary overlap, no visible button/select/summary smaller than 44×44. See `evidence/layout-checks.json`.
- Real-browser screenshots: `evidence/<width>-<scene>.jpg`; light colour examples for each width; greyscale/dim phone examples per scene. HTML remains the full-resolution review surface for theme combinations not saved as individual screenshots.
- Reset opens a non-modal confirmation, focuses Cancel, keeps Disarm enabled and reachable, Escape closes and restores Reset focus. See `evidence/reset-confirmation.jpg`. Production integration must also revalidate targets before sending.
- Browser-rendered defects found and corrected: the three-row fixture grid needed a non-shrinking minimum height to avoid overlapping its summary; native light scrollbars needed the dark colour scheme; phone tools lost redundant icons to fit one row; landscape group faders start collapsed to leave work space; small selection buttons received a minimum width as well as height.

The physical input/output engine is not connected to these mockups. They use fictional fixture data. No live show or runtime configuration was opened or modified.

## Dark-venue and greyscale review

**Physical device minimum brightness remains unverified.** The browser cannot establish the user's display backlight, OLED black level, glare, dark adaptation, glove accuracy or arm's-length readability. The review's `Dim-screen simulation` uses a 0.45 CSS brightness multiplier; this is a visual stress approximation, not a claim that every display was tested at minimum brightness. Review the HTML on the intended phone and tablet before production signoff.

Greyscale/dim screenshots were inspected for every scene on the phone. All four requested sizes were also rendered under this mode and checked for geometry. Larger-size scene screenshots are preserved for review. At the deliberately dimmed level, captions are the first information to become difficult; the word, border and order cues remain structurally distinct. Do not shrink captions further.

| Scene (all four viewport variants available) | What survives desaturation / changes made | Bright-area review in dark theme |
|---|---|---|
| Empty, disarmed | DISARMED word, outlined Arm versus rectangular stop, explicit “No fixtures patched”; empty groups/tests state | No large fill above overlay |
| Armed mixed selection | ARMED lock and thick master border; selected parent outline versus selected cell tab; numbered order, P/T, MIXED range | Selected fixture and cell fills above overlay are individual buttons; no bright full master fill |
| Colour mixed capabilities | RGBW/wheel counts, emitter names, SET/default; nearest-slot words remain after hue disappears | Picker content capped at 0.12 sRGB multiplier (maximum Y≈0.0134, below overlay Y≈0.0202); selected buttons are bounded controls |
| Beam / missing gobo media | Slot number/name, dashed missing-media border, explicit “media not reported”, IN count; OPEN/CLOSED differ by glyph and word | No large fill above overlay; placeholder is a stroke drawing |
| Shaper | Numbered blade diagram and labelled A/B sliders provide equivalent information; no fill-only blade identity | Thin blade faces use selected fill; each face is smaller than a standard button, with readable outlines |
| Tests and sequence | CURRENT word, leading rule and step count; ON word and tile outline; override explanation | CURRENT row uses selected fill over an area larger than a small button: **flagged exception**. Use a leading rule + normal surface if the device review finds this too bright |
| Lease loss / blackout | Broken link, dashed master border and literal “Rig blacked out”; distinct from normal disarm | No bright master fill; Arm/stop remain bounded controls |
| Lease loss / held look | LOOK HELD and “Last look held” remain visibly different from blackout in greyscale | Same as above; no claim of blackout based solely on local link loss |
| Simulated | Persistent hatch, SIMULATED / not a real rig words, Rehearsal label; does not depend on colour | Hatch alternates surface/overlay, never exceeds overlay |
| Unprofiled raw bank | RDM-only header, relative and absolute addresses, “not reported” labels, SET/default; bank owns scrolling | Small thumb faces are brighter; no large bank fill above overlay |
| Fixture command / confirmation | Named targets, explicit consequence, Cancel and Reset verbs; caution boundary; stop remains reachable | Confirmation fill equals overlay; no white/light modal flash |

Warning and accent surface tints were lowered below overlay luminance during review. Selected fill is Y≈0.0293 and is restricted to individual selected controls or the flagged current-step row; never use it as the background of a whole pane. Text, strokes, thumb faces and individual stop buttons are intentional brighter areas. The daylight theme is intentionally bright and excluded from dark-venue certification.

## Remaining integration and environmental checks

- Go/gofmt are not installed or available on PATH in this workspace. Repository gates `gofmt`, vet, native build, full tests, race tests and Windows cross-build could not run. There are no Go or production web-source changes in this package.
- A disposable `--demo` application build could not be produced without Go. Standalone HTML was rendered against the repository's existing token file plus proposed additions. Repeat production demo render checks after integrating the assets and routing kit roles.
- Hardware brightness/glove review and real screen-reader testing remain outstanding. DOM/keyboard checks do not constitute complete assistive-technology certification.
- C9 custom-theme enforcement and production interaction changes are specified, not shipped here. Edges-in fan mapping, highlight stepping, shape editors, readout mode conversion and server target revalidation must use the real controllers during integration.
