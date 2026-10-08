# Colour-theme contract for C9

`console-tokens.css` is one importable block after the existing token file. It defines dark/light semantic values and measurement additions. Existing low-contrast muted text, translucent focus ring and light accent are intentionally overridden. Component CSS uses roles; palette ramps remain private to the base token file. SVG is theme-independent `currentColor` with no style elements or descendant IDs.

## Pair validation

The exact allowed backgrounds are the `surfaces` array in `contrast.json`: canvas, surface, raised, overlay, inset, hover, selected, grid, XY and each tone fill. Every role has every allowed pair recorded, with unrounded pass/fail; the Markdown report gives the worst case.

- Primary text ≥7:1 on all allowed surfaces.
- Secondary, muted, source/caption and tone text ≥4.5:1 on all allowed surfaces, including selected and tone fills.
- Essential borders, icons, tracks and focus ≥3:1. Track/thumb contrast is checked against the track and every allowed surface. Thumb border is its contrast against the outside track/surface, not against the interior of the thumb.
- Disarm text ≥4.5:1 against the dedicated slab; aim ≥7. Its danger outline, not the slab fill alone, supplies the essential boundary. Generic text roles may not sit on this slab.
- `text-on-accent` is for a solid high-contrast thumb/content fill only. It is not for `accent-fill`, which is a low-luminance surface tint; use normal text there. Any future solid accent button requires its own paired solid fill role.
- Disabled role is exempt numerically but always comes with reason text in readable secondary text. Decorative grid lines and separators are exempt and may never encode essential position or state alone.
- Simulated background is a two-endpoint stripe pattern, both endpoints in the surface test set. Sizes, focus-shadow geometry and preview multiplier have no standalone colour ratio; their constituent colours are checked.

Only opaque sRGB colours are accepted for theme fields in the first C9 version. If alpha is later allowed, resolve it over every permitted background before comparing; checking the source RGBA as if opaque is invalid. Never round before deciding PASS. Error: “Muted text on overlay is 3.9:1; needs 4.5:1.” Present each failed pair and block Save until repaired. Preview invalid drafts locally with an explicit `Theme needs changes` word, retaining a working Revert control.

## Dark venue limits

Dark themes: canvas and surface relative luminance Y ≤0.020; raised/overlay/inset/hover/selected and tone fills Y ≤0.035. This package's canvas retains the existing near-black warm base. Light theme is labelled daylight and is not appropriate for dark-venue checking.

The live-preview multiplier defaults to 0.12 and cannot exceed 0.15 in a dark theme. It operates on sRGB content before compositing; cap the resulting area to the theme overlay luminance as a second check. State borders/text sit outside the dimmed area. Preview OFF is the default and must be available without changing theme. It is impossible to certify dark adaptation from a CSS multiplier or screenshot; device black level and backlight still require physical review.

Large regions brighter than overlay are rejected in dark mode, except text strokes, focus/state outlines and individual intentional controls. Never fill a whole armed bar with its bright border colour. Any permitted exception is documented by component and area; the coloured picker content in these mockups stays below the overlay luminance after dimming.

## Tone separation

Proposed project rule (not a WCAG requirement): pairwise Euclidean **ΔE in OKLab ≥0.04** for armed/danger/warn/ok/accent borders (OKLab L in 0–1). Additionally reject exact RGB equality. This avoids identical tone assignments without pretending colour alone can carry meaning. Calculate after gamut conversion from the accepted sRGB fields. If a future monochrome theme is wanted, it requires an explicit exception to this colour-separation rule; shape/words remain mandatory regardless.

Armed uses a separate violet role, warning amber, danger red, ok green, accent teal. This keeps status categories semantically distinct. All must also pass contrast against their own fills and every allowed surface. A larger colour distance never compensates for insufficient contrast.

## Non-themeable structure

Themes may only set accepted colour roles and the bounded preview intensity. They cannot override font size, hit targets, visibility, opacity of state text, label copy, border width/style, glyphs, order badges, hatch structure or focus behavior. Armed/disarmed; OPEN/CLOSED; SET/default; ON/OFF remain distinguishable in greyscale.

Map `.b5-bigbtn--go/--stop`, pill tones, `.b5-range-touch` thumbs and `.b5-statecard.is-armed` away from ramps during integration. Map normal essential buttons to `border-control`; retain border-default/subtle only for decorative separators. Do not expose custom themes until production components use the validated roles everywhere they appear.
