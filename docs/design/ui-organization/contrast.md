# W1b contrast verification

Measured computed role colours in installed Edge using WCAG relative luminance. No new palette roles. Text threshold 4.5:1; essential boundaries/focus/selection threshold 3:1. Existing W1 header/Go-Stop colours remain unchanged. Greyscale scenes also retain word, check, border and shape cues. Decorative separators are not used as essential boundaries.

| Theme | Foreground role | Background role | Ratio | Required | Result |
|---|---|---|---:|---:|---|
| dark | text-primary | bg-canvas | 16.93 | 4.5 | Pass |
| dark | text-primary | bg-surface | 15.49 | 4.5 | Pass |
| dark | text-primary | bg-surface-raised | 14.35 | 4.5 | Pass |
| dark | text-primary | bg-surface-overlay | 13.14 | 4.5 | Pass |
| dark | text-primary | bg-inset | 17.39 | 4.5 | Pass |
| dark | text-primary | bg-pressed | 10.93 | 4.5 | Pass |
| dark | text-secondary | bg-canvas | 11.23 | 4.5 | Pass |
| dark | text-secondary | bg-surface | 10.27 | 4.5 | Pass |
| dark | text-secondary | bg-pressed | 7.25 | 4.5 | Pass |
| dark | text-danger | bg-surface-overlay | 7.33 | 4.5 | Pass |
| dark | border-control | bg-canvas | 6.92 | 3 | Pass |
| dark | border-control | bg-surface | 6.33 | 3 | Pass |
| dark | border-control | bg-surface-raised | 5.86 | 3 | Pass |
| dark | border-control | bg-inset | 7.10 | 3 | Pass |
| dark | border-control | bg-surface-overlay | 5.37 | 3 | Pass |
| dark | accent-border | bg-canvas | 10.80 | 3 | Pass |
| dark | accent-border | bg-pressed | 6.97 | 3 | Pass |
| dark | border-focus | bg-surface | 12.17 | 3 | Pass |
| dark | text-primary | bg-hover | 13.14 | 4.5 | Pass |
| dark | text-secondary | bg-inset | 11.53 | 4.5 | Pass |
| dark | border-control | bg-hover | 5.37 | 3 | Pass |
| light | text-primary | bg-canvas | 14.93 | 4.5 | Pass |
| light | text-primary | bg-surface | 17.48 | 4.5 | Pass |
| light | text-primary | bg-surface-raised | 16.19 | 4.5 | Pass |
| light | text-primary | bg-surface-overlay | 17.63 | 4.5 | Pass |
| light | text-primary | bg-inset | 13.88 | 4.5 | Pass |
| light | text-primary | bg-pressed | 11.93 | 4.5 | Pass |
| light | text-secondary | bg-canvas | 9.23 | 4.5 | Pass |
| light | text-secondary | bg-surface | 10.81 | 4.5 | Pass |
| light | text-secondary | bg-pressed | 7.38 | 4.5 | Pass |
| light | text-danger | bg-surface-overlay | 8.28 | 4.5 | Pass |
| light | border-control | bg-canvas | 5.07 | 3 | Pass |
| light | border-control | bg-surface | 5.94 | 3 | Pass |
| light | border-control | bg-surface-raised | 5.50 | 3 | Pass |
| light | border-control | bg-inset | 4.72 | 3 | Pass |
| light | border-control | bg-surface-overlay | 5.99 | 3 | Pass |
| light | accent-border | bg-canvas | 6.46 | 3 | Pass |
| light | accent-border | bg-pressed | 5.17 | 3 | Pass |
| light | border-focus | bg-surface | 7.57 | 3 | Pass |
| light | text-primary | bg-hover | 13.88 | 4.5 | Pass |
| light | text-secondary | bg-inset | 8.58 | 4.5 | Pass |
| light | border-control | bg-hover | 4.72 | 3 | Pass |

All 42 pairs pass. Exact RGB and unrounded ratios are in contrast.json. These cover field/help text, inspector/table surfaces, pressed/hover surfaces, timing tags, danger menu text, control outlines, focus and selected/inspected boundaries. Native checkbox internals use browser rendering; physical contrast and screen-reader checks remain for integration.
