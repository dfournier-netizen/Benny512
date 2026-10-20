# W1b verification

Standalone design artifacts rendered in installed Microsoft Edge (headless), with a disposable Playwright profile and loopback-only static server. No production controllers, demo show, owner show or hardware connection used. Sandbox Edge could not create its profile; the final successful checks ran with permission outside the sandbox, with server/browser together on port 5838.

- 108 layout cases: 9 scenes (six screens + three nav options) × 2 laptop sizes × 3 modes × fine/coarse geometry. 0 failures.
- 13 interaction checks: 13 pass.
- 42 contrast pairs across Dark and Daylight: 42 pass.
- 0 browser page errors / failed HTTP responses.
- 54 saved native-size PNGs: every scene at 1440×900 and 1024×768 in Dark, Daylight and Dark greyscale.
- All five package JavaScript files pass node --check. No new runtime dependencies; verification uses the installed bundled Playwright.

Layout assertions: no document overflow; DISARM visible and 60 px high; group-fader footer visible; dock excludes list; coarse button/select/summary geometry at least 44×44. Inspector-open tables deliberately own horizontal scrolling at narrow widths. The verifier also checks 90 Patch rows, sticky headers, matching row inspection, independent checked selection, menu Escape/focus restoration, 20 px keyboard dock resizing, Float releasing dock space, collapse hiding its body, staging before Apply, preview-only Apply, internal Settings anchors, retained typed RESET and one-step walk navigation.

Visual inspection covered 1440 Patch Dark, 1024 Settings Daylight, 1024 Devices Daylight, 1024 Rig Walk Dark, 1440 Analyzer greyscale and 1024 Patch Dark. The first pass exposed inherited W1 absolute window positioning overlaying the list, and row-button/help styles overriding coarse minimums. Fixed in W1b screen layout/target rules; W1 sources unchanged. Repeated all layout/interaction/contrast checks after the fixes.

## Measured Patch geometry

| Viewport | First row top | Row height | List width | List height |
|---|---:|---:|---:|---:|
| 1440×900 | 282 px | 48 px | 1000 px | 572 px |
| 1024×768 | 282 px | 48 px | 584 px | 440 px |

## Limits / integration checks

- Design-only package. Production Go gates, disposable --demo build, live controller behavior, output/lease safety and hardware verification were not run. No Go or production source is changed.
- Native table/field controls and explicit coarse-preview geometry are checked; real glove/touch accuracy, physical dark adaptation, screen-reader announcements, pointer cancellation and long translations remain unverified.
- W1 window behavior is specified by reference. This preview demonstrates dock/float, keyboard dock width, collapse/close and focused-title arrow movement. It does not implement W1's entire drag/drop, keyboard resize, storage or geometry-migration engine.
- Existing full PID, port, theme and RDM filter editors are represented by retained routes/representative fields. Import/export, real discovery, capture, Identify and destructive actions show review notices; Apply changes only the scene.
- Settings index initial marker is static; production should track the active section and set aria-current=location. Density and panel geometry are not persisted in this review.
- Console and Tests remain W1 dependencies. Their navigation destinations show an explanatory notice; W1 source assets were not redesigned. The approved swatch name/outline fix, Beam/Gobo label and Plan-Fit hint remain separate W1 follow-ups.
- No global controls panel: owner clarified fixture controls remain on Console. Group-fader footer is the unchanged W1 collapsed placeholder.

Reproduce with node docs/design/ui-organization/verify.cjs. It starts its own server on 5838; serve.mjs starts the gallery on 5837. Verifier paths to bundled Playwright and installed Edge are machine-specific. evidence/browser-checks.json and contrast.json contain the full measured results.
