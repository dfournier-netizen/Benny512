# Claude Design brief — Benny512 UI system

**Written:** 2026-08-16 21:48
**How to use:** paste everything below the line into Claude Design, and attach your logo in the same message — use `D LUX Clean.svg` (the 19 KB version; the 263 KB one is too heavy to embed in an offline app, though attach both if you want Design to compare).

**Your logo files are now saved permanently at** `RDM App\design\logo\` — uploads don't survive a session, so I copied them there.

**Palette extracted from `D LUX Clean.svg`** (already written into the prompt below so Design doesn't have to guess):

| Hex | Role in the logo |
|---|---|
| `#26a39a` | primary teal |
| `#06958b` | deep teal |
| `#52b7b1` | light teal |
| `#2a4d85` | navy |
| `#c80100` | red |
| `#1c1815` | near-black |
| `#fefefe` | off-white |

*Reminder to Dom: the deliverables land in `C:\Users\CDT_LD\Claude\Projects\RDM App\design\<timestamp>\`. Once they're there, tell me and I'll have a drone wire them into the app.*

---

I need a complete visual design system for an existing, working web application. I am **not** asking for pictures or mockup images — I need **real, production-ready CSS and SVG assets** that a developer will drop directly into the app, plus static HTML reference pages that demonstrate them.

## The application

**Benny512** is a utility for entertainment lighting technicians. It runs as a small server on a Windows PC plugged into a venue's lighting network; the user opens its web UI from a browser on that PC, a laptop, or a phone on the same network. It talks to lighting hardware (DMX/Art-Net/RDM gateways, wireless transmitters, and the light fixtures themselves) — discovering devices, reading and changing their settings, monitoring sensors, and analyzing network traffic.

**Who uses it:** professional lighting technicians, during setup and troubleshooting. Often in a dark venue, sometimes up a ladder holding a phone in one hand, frequently under time pressure before a show. This is a working tool, not a consumer app. Clarity and speed of reading beat decoration every time.

**Aesthetic direction: professional console / touring gear.** It should feel like the web UI of a piece of rack-mounted show equipment or a lighting console — dark charcoal, restrained accent color, crisp dense data tables, tight disciplined grid. Think ETC / grandMA / high-end network gear. It should look purposeful and disappear into the work. Avoid: playful illustration, marketing gradients, oversized hero sections, rounded-bubbly friendliness, anything that wastes vertical space.

**Branding:** I'm attaching my existing logo (D LUX). Build the palette to harmonize with it — do not design a new logo or mascot, and don't redraw mine. Use it in the app header and as the browser tab icon (it will need a simplified/small-size treatment for a favicon — provide one).

Its palette is:

| Hex | Role in logo |
|---|---|
| `#26a39a` | primary teal |
| `#06958b` | deep teal |
| `#52b7b1` | light teal |
| `#2a4d85` | navy |
| `#c80100` | red |
| `#1c1815` | near-black |
| `#fefefe` | off-white |

Derive the UI palette from these. Suggested direction, but use your judgment: the near-black as the base surface family, teal as the functional accent (selection, focus, active state), navy as a secondary/informational tone. **Reserve red strictly for error and destructive actions** — it must never appear decoratively, or it loses its meaning in an app where a red state means something is actually wrong. You'll need to extend this into a full scale (multiple surface elevations, borders, muted/secondary text, plus semantic colors for warning and success that harmonize but are distinguishable from the brand teal and red). Verify and report contrast ratios — the logo's mid-teals may be too low-contrast for text on dark surfaces and may need lightened variants for that use.

## Hard technical constraints — these are not negotiable

1. **Vanilla CSS only.** No Tailwind, no Bootstrap, no framework classes, no preprocessor (no SASS/LESS), no build step. Plain `.css` files the app serves as-is.
2. **Fully offline.** The app runs on isolated show networks with no internet. **No Google Fonts, no CDN links, no `@import` from a URL, no external images.** Use a system font stack only. All graphics must be inline SVG or local SVG files.
3. **No icon fonts.** Icons must be SVG.
4. **Do not write application JavaScript.** The app's behavior already exists and works. You may write tiny inline JS in the demo HTML pages purely to toggle demo states (e.g. show a tab switching), but clearly separate it and don't design around new behavior.
5. **Class naming:** prefix every class `b5-` (e.g. `b5-table`, `b5-badge--warn`). Use a simple BEM-ish convention: `b5-block`, `b5-block__element`, `b5-block--modifier`. Avoid generic names like `.card` or `.btn` that could collide with existing markup.
6. **Design tokens as CSS custom properties** on `:root`, named systematically: `--b5-color-*`, `--b5-space-*`, `--b5-font-*`, `--b5-radius-*`, `--b5-border-*`, `--b5-shadow-*`, `--b5-z-*`. Every component style must reference tokens, never hardcoded hex values or pixel spacing. I want to retheme by editing tokens alone.
7. **Dark theme is the primary and default theme.** Include a light theme as a token override block (`[data-theme="light"]`) for daylight/outdoor use, but optimize for dark.

## Accessibility — a standing requirement, not a nice-to-have

- **WCAG AA contrast minimum** for all text and meaningful UI. State this explicitly for each color pair in your spec.
- **Never use color as the only signal.** Every status (OK / warning / error / stale / pending) must also carry a text label, glyph, or pattern. This is critical — techs work in colored venue light, and color-blindness is common.
- **Dyslexia-friendly typography:** generous line height (≥1.5 body), slightly increased letter spacing on small text, no justified text, no all-caps for anything longer than a short label, clear distinction between similar glyphs (choose a system stack where `1`/`l`/`I` and `0`/`O` are distinguishable; note the stack you chose and why).
- **Touch targets:** minimum 44×44px anywhere; the phone "walk" mode uses 72–84px primary buttons. Nothing critical in the top corners of a phone layout.
- **Visible focus states** on every interactive element, keyboard-navigable.
- Data-dense tables must remain readable at arm's length in a dark room — favor size and contrast over density where they conflict.

## Responsive requirement

The **same pages** must work on a desktop monitor and a phone in portrait, with no horizontal scrolling on either. Design mobile-adapted layouts for data tables (e.g. collapse to stacked rows/cards below a breakpoint) rather than letting them scroll sideways. One screen ("Rig Walk") is phone-first but must also render well centered on a desktop, not stretched full-width.

## Screens to cover

1. **Nodes** — table of discovered network gateways: name, IP, ports with universe assignments, status flags, last-seen. Selecting one opens a detail pane with editable configuration (names, per-port universe, IP settings).
2. **Devices** — the main table: every discovered lighting device across the rig. Columns include manufacturer, model, class badge, DMX address with occupied channel range, universe, node/port. Has filter controls (by node, universe, device class) and sort controls (address, model, UID). Selecting a device opens a detail panel.
3. **Device detail panel** — sectioned: *Info* (read-only spec fields), *Parameters* (editable fields — text, numeric steppers, dropdowns, checkboxes, and an advanced raw hex-entry field), *Sensors* (gauges — see below), *Status* (severity-tagged message list). This panel renders in two presentations from the same markup: a **tab strip** on desktop, and **large collapsible accordions** on phone.
4. **Rig Walk** — phone-first walkthrough. One device at a time, huge "N of M" counter, large device identity readout, the full device detail below it, and a fixed bottom bar with full-width Previous/Next buttons. Plus Confirmed / Problem status buttons and a progress summary.
5. **Analyzer** — live scrolling log of network packets, with filter controls, expandable rows revealing hex dumps and decoded field lists. Needs a monospace treatment for data, and must stay readable while updating rapidly.
6. **Send** — a 512-channel DMX control grid (numeric entry + some faders), universe selector, start/stop controls.
7. **Settings** — plain form: network interface picker, intervals, file paths, toggles.

## Components to design (this is the real deliverable)

Provide styles and demo markup for each:

- **Data table** — dense, sortable headers (with sort-direction indicators), row hover/selected states, a stale/inactive row treatment, and its stacked mobile variant.
- **Status badges** — device class labels, and state badges (OK, warning, error, pending, stale, unknown). Text + glyph, not color alone.
- **Sensor gauge** — horizontal bar showing: a full declared range, a shaded "normal" band within it, a prominent current-value marker, thin tick marks for lowest/highest recorded values, plus a numeric readout with units. Must have a clear out-of-normal-range state signaled by pattern/border/glyph as well as color. Also design a fallback for sensors that declare no valid range (numeric readout only).
- **Editable field with Apply/Revert** — this is the app's most important interaction pattern and appears everywhere. Every editable field stages changes and requires an explicit Apply; it must be obvious at a glance when a field is *dirty/pending* versus committed. Design: text input, numeric stepper, dropdown, checkbox/toggle, and a monospace hex input — each in default, focused, dirty/pending, applying (in-flight), success, error, and disabled/read-only states.
- **Buttons** — primary, secondary, danger (for destructive/risky actions like changing a device's IP), plus the oversized touch variant for walk mode.
- **Tabs** (desktop) and **accordion sections** (mobile) that render the same content.
- **Filter and sort control bar** — multiple combinable dropdowns with a visible active-filter summary and a clear-all action.
- **Progress / counter display** for the walk mode ("12 of 48", plus confirmed/problem/remaining tallies).
- **Inline alerts and toasts** — including a prominent caution banner style used to warn that certain operations are unverified/risky.
- **Empty states** — no devices found, no traffic captured, no sensors reported. Keep them terse and useful (what to do next), not cute.
- **Loading/pending indicators** — including a subtle inline "waiting on a slow device" state, since some hardware responds slowly.
- **Hex dump / monospace data block** for packet inspection.
- **App shell** — header with logo and connection status, primary navigation across the six screens (a horizontal bar on desktop; something thumb-reachable on phone), and the overall page frame.
- **Icon set (SVG)** — the functional icons this UI needs: navigation icons for the six screens, plus sort ascending/descending, filter, warning, error, OK/check, pending/clock, refresh, expand/collapse chevrons, identify (a flash/beacon idea), signal strength, network node, export/download, apply/confirm, revert/undo. Consistent stroke weight and grid. Deliver as an SVG sprite plus individual files.

## Deliverables and file management — follow this exactly

Create a folder named with today's date and time in this format: `design/YYYY-MM-DD_HHMM/` (e.g. `design/2026-08-17_0930/`). **Put everything inside it**, using exactly these filenames:

```
design/<timestamp>/
├── README.md                  ← what's here, how to use it, what changed if iterating
├── benny512-tokens.css        ← :root custom properties ONLY (dark) + [data-theme="light"] overrides
├── benny512-components.css    ← all component styles, referencing tokens only
├── benny512-layout.css        ← app shell, navigation, grid, responsive breakpoints
├── icons/
│   ├── benny512-icons.svg     ← single <symbol> sprite, ids named b5-icon-<name>
│   └── <name>.svg             ← one file per icon, same names
├── logo/
│   └── (my logo, optimized/cleaned as SVG if possible, plus a favicon-ready version)
├── mockups/
│   ├── nodes.html
│   ├── devices.html
│   ├── device-detail.html
│   ├── rig-walk.html
│   ├── analyzer.html
│   ├── send.html
│   ├── settings.html
│   └── components.html        ← a kitchen-sink page showing EVERY component in EVERY state
└── design-spec.md             ← token reference table, component usage rules, contrast ratios,
                                  breakpoints, do's and don'ts
```

Rules for these files:

- Every mockup HTML must be **standalone and openable directly in a browser** — linking only to the three local CSS files and the local SVG assets, with realistic sample data (real-sounding fixture names, plausible DMX addresses, UIDs in the format `7A70:12345678`, IP addresses like `2.11.90.2`).
- **`components.html` is the most important file** — it's the reference a developer works from. Show every component in every state, each labeled with its class names.
- Do **not** modify or create files outside your timestamped folder.
- If you iterate later, create a **new timestamped folder** — never overwrite a previous one.
- In `README.md`, list anything you were unsure about or that needs a decision from me.

## What I'll do with it

A developer will lift the three CSS files and the icons into the app's static assets and re-skin the existing markup to match your class names. So: **keep the class vocabulary small, systematic, and predictable**, and make sure `design-spec.md` states plainly which class goes on which element for each component. Where an existing pattern would be hard to retrofit, say so in the README rather than silently designing something exotic.
