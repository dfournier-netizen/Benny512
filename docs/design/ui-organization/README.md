# Benny512 UI organization · W1b

Open **index.html** for the review gallery. Run `node docs/design/ui-organization/serve.mjs` from the package root workspace and visit [the local gallery](http://127.0.0.1:5837/docs/design/ui-organization/index.html).

Six design-only scenes: Patch (90 invented rows, inspected entry, checked rows), Settings (section index and help open), Devices, Nodes, Analyzer, Rig Walk. Three navigation alternatives. Gallery selectors expose 1440×900 / 1024×768, Dark / Daylight / greyscale and coarse-target preview. `evidence/` contains native-size renders and browser-check JSON. Read decisions.md for owner answers; component-specs.md defines only additions to W1.

The owner approved grouped navigation, Settings index, right-only docking, Raw DMX/Tools inside Controls, full Tests selection, no global shortcuts, programmer-colour swatch with name/outline as a separate W1 follow-up, Comfortable rows, inspectors opened by selection, closed device diagnostics once devices exist, shared Nodes inspector, and fixture controls only on Console.

Source repository: `C:\Users\CDT_LD\Documents\GitHub\Benny512\Benny512`. Review built in the chat's writable workspace at `C:\Users\CDT_LD\Documents\ChatGPT\Benny512`; the completed package was then delivered to this repository, with handoff and notes updated. Production and W1 assets are unchanged. The sibling console-workspace styles and internal/web/static tokens, UI helper and sprite here are unchanged copies solely to render this package. They are **not** production changes. To integrate the review into the source repository, copy only `docs/design/ui-organization/`; it resolves existing sibling W1 and production assets at the same relative paths.

The preview has no production controllers, network/output API or real show data. ARM and DISARM display explanatory notices; Apply affects only the scene. Full editing, import/export, discovery, PID forms and capture are representative routes, not functional implementations. Console and Tests destinations explain their W1 dependency instead of duplicating or redesigning them. The group-fader footer is W1's unchanged collapsed placeholder; it does not implement faders.

Re-run `node docs/design/ui-organization/verify.cjs` while the local server runs. The verifier currently points to this machine's bundled Playwright and installed Edge; adjust those paths on another machine. It checks only these standalone review assets, not production or hardware.
