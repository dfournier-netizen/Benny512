# G3a verification — 2026-11-17

Checked the mockup `mockup_2026-11-17_2309.html` in headless Chromium (Playwright 1.63,
chromium-1243), loaded straight from disk (`file://`). Re-run with:

```
node docs/design/gdtf-library/build-mockup.mjs mockup_2026-11-17_2309.html
node docs/design/gdtf-library/check.mjs mockup_2026-11-17_2309.html
python3 -I docs/design/gdtf-library/shrink-evidence.py
```

Full results: `evidence/checks.json`.

## Results

- **96 render runs pass:** 16 scenes × 1024×768, 1440×900, 1920×1080 × Dark and Daylight. Each
  scene was reached through the mockup's own Scene control. In every run: the scene drawn is the
  one picked, its screen is visible, there are **no console errors or page errors**, and there is
  **no page-level horizontal scroll** (`scrollWidth − clientWidth ≤ 0`). Wide tables scroll
  inside their own box if they ever need to.
- **Contrast: 78 unique text and boundary pairs measured, 0 failures.** Measured on the rendered
  colours, not the token file: every visible text element's computed colour against the first
  opaque background behind it (translucent layers composited, ancestor opacity applied), using
  WCAG relative luminance with unrounded thresholds: 4.5:1 for normal text, 3:1 for large text,
  and 3:1 for the borders of the new parts that carry meaning (status box, match card, progress
  bar, logo placeholder, NEW tags). Lowest passing pairs: the status-box border in Daylight 5.07:1;
  the route box's note text in Daylight 5.85:1; the selected search tab in Daylight 6.00:1. No
  text sits on a background image.
  - **One failure found and fixed:** the new status box first used `border-default`, 1.55:1 on the
    canvas in Dark (1.53:1 in Daylight). It now uses `border-control`.
  - **Exempt, listed rather than passed:** the disabled **Sync now** button (shown in scenes 1
    and 3 with the reason in words beside it) is 3.01:1 in Dark and 2.10:1 in Daylight, from
    production's 0.45 opacity for disabled buttons. WCAG exempts inactive controls.
  - **Not measured:** aria-hidden decorative text. The only instance is production's “·” in the
    Devices list link badge (3.75:1 Dark, 4.00:1 Daylight): existing production, unchanged here.
  - A first run reported ARM and DISARM at 4.49:1 and 4.09:1 in Daylight at 1920: they were
    measured mid-way through production's colour transition right after the theme switch. With a
    600 ms settle both pass; nothing was changed for it.
  - **No new colour values.** `src/mockup.css` uses production roles (`--b5-color-*`) only;
    every colour on screen comes from `benny512-tokens.css` / `console-tokens.css`.
- **Keyboard:** Tab walks in scenes 1, 5, 9, 12 and 16 at 1440×900 (32 to 43 stops each) reach
  the app chrome, the screen, the open Fixture library panel or Device inspector, and the mockup
  controls. **Every stop is visible and shows a focus indicator** (outline or focus shadow).
- **Scene switching:** Next pressed with Enter steps s2 … s16 in order and is disabled on the
  last scene; Previous returns to s1; **Search by name** in scene 15 leads to scene 16.
- **No production file changed:** `git diff --stat 7e8b73b HEAD -- . ':!docs'` is empty.

## Screenshots (`evidence/`, 256-colour PNG)

`1440-dark-s1`, `1440-dark-s5` (refusal), `1440-dark-s7` (waiting + unreadable),
`1440-dark-s9` (offline search + existing confirm step), `1440-dark-s11` (8 MB refusal),
`1440-dark-s13` (likely mode), `1440-dark-s16` (name search, no RDM data, zero model ID),
`1024-dark-s3` (syncing), `1024-light-s5` (Daylight refusal), `1920-dark-s12` (exact match).

Looked at by eye as well as checked: the 1024 Settings and Device inspector, the refusal in both
themes, the library panel at 1024 and 1440, and every Devices match scene. Fixes made from
looking: scroll targets landed under the sticky chrome (now offset); the mockup controls took two
rows at 1024 (now one, with the scene note shortened to one line and kept as its tooltip); the
no-match message used the large empty-state block and pushed the name search out of view (now a
compact info alert); the identity rows briefly showed IDs production does not show there (back
to production's rows; the IDs are in the GDTF group's caption).

## Limits

- **Design only.** No Go, JS or CSS in the app changed, so the Go gates and a `--demo` render
  proof do not apply to this chunk. A disposable `--demo` build was run only to look at the real
  Settings, Devices inspector and Fixture library panel, so the mockup copies the real chrome and
  markup. The mockup sends nothing.
- **Laptop widths only.** The phone and tablet layouts of these screens are not drawn; the
  production screens' own responsive rules apply in G3b and need their own check there.
- Headless Chromium is a real browser render, not a screen-reader, touch/glove or dark-venue
  brightness check. Focus order was checked for the five busiest scenes, not all sixteen.
- Data is made up in the API's shapes. The fields marked NEW (README) do not exist yet; G3b must
  either add them or drop the line that needs them.
