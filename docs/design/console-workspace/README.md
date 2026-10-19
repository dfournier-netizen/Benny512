# Console Workspace — W1 design package

Start with [index.html](index.html), the interactive review at 1024×768, 1280×800, 1440×900 and 1920×1080. It follows the [latest 22:49 brief](ui-brief_2026-10-18_2249.md). The two dated reference mockups/briefs are retained unchanged.

This is a laptop design package, using fictional fixtures and authored sample intensity/source data. Production code, engine behavior, show data, phone/tablet layouts, group-fader design and theme editor are unchanged. Owner review of these assets/scenes is the next production-integration gate. Dark, Daylight and custom themes already ship (C9/C9c).

| Deliverable | Contents |
|---|---|
| console-workspace-icons.svg | 19 clean currentColor symbols, 24 grid, 1.75 stroke, no embedded metadata |
| component-specs.md | Header, toolbar, ON grammar, dock/float/resize, tile/data contract, selection, seven Controls families and Tests; keyboard/ARIA, coarse targets and proposed open-question answers |
| tokens-additions.css | New semantic workspace aliases only, Dark/Daylight; existing palette remains private |
| contrast.md / contrast.json | Every permitted new-role pair measured, including percentage text over track/fill |
| assets.html | Every icon at 14/18/24/32, theme/greyscale and toggle proof |
| index.html / screen.html | 18 review scenes × four viewports; Dark/Daylight, greyscale/dim and coarse-target previews |
| verification.md / evidence | Browser measurements, interactions, screenshots and stated physical/device limits |
| build-package.mjs | Rebuild icons, role aliases, proof sheet and contrast from current production C9 tokens |
| prepare-mockups.mjs | Repackages the dated owner reference with authored workspace-scenes.js and workspace.css; dated input remains untouched |

Scenes include all nine requested examples plus 320 px dock, All layers and all seven Controls families. **1024 proposal:** a compact Active layer selector replaces long layer segments; the remaining labelled toolbar stays in one row. The grid excludes dock width from Fit. Only Controls opens initially; other windows are hideable, resizable and browser-persistent. Preview scenes keep separate window-state namespaces to make examples independently reviewable. Reset window positions returns the Controls-only default workspace.

Open choices are proposed explicitly: right-only docking; Raw DMX/Tools within Controls; full selection tools on Tests; no new global shortcuts; programmer-colour swatch. None is presented as an owner decision. Engine-composed fixture/cell intensity and source availability still need production verification; unknown data must say “not reported”.

Serve from repository root: `node docs/design/console-lite/serve.mjs`, then open [Workspace review](http://127.0.0.1:8765/docs/design/console-workspace/). Loopback only. External SVG references require HTTP. Rebuild with `node docs/design/console-workspace/build-package.mjs` and `node docs/design/console-workspace/prepare-mockups.mjs`. No dependencies or output connection.
