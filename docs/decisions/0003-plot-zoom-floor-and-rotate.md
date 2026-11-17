# 0003 — Console plot: continuous zoom with a low floor, number-only tiles, Rotate

**Decided:** Dom (owner), 2026-11-17 10:17–10:21 -0500. **Recorded:** 2026-11-17 10:29:05 -0500. Chunk L2.

## Decision

- The Console/Tests plot zoom is a cell size in px, continuous from a floor to
  160 px: Ctrl+wheel / trackpad pinch (around the pointer), a slider, and
  − / + (×1.25). This replaces K2's four fixed sizes (64/96/128/160).
- The floor is **28 px on a fine pointer** and the touch minimum (44 px)
  otherwise. **Fit** uses the same floor (it was 86 px, W1 §5), so a whole rig
  can fit.
- Below **56 px** a tile shows its **fixture number only** (`is-tiny`); the
  owner chose "number + intensity bar", and the bar arrives with K5. Selection
  keeps its outline and Highlight its ring; badge, HL tag, type, flags and
  words return when zooming in.
- **Rotate 90° right / left** (View menu) turns the drawn plan a quarter turn
  either way, display only like Compact, remembered per browser, off while
  editing. Both directions exist because which one reads naturally depends on
  the stage orientation of the show, which the app does not know.
- **Drag to pan:** a mouse/pen drag on the plan (Lasso off, not editing)
  scrolls it; the click that ends the drag does not select. Touch keeps native
  scrolling.

## Evidence

Owner's show (see 0002): trusses run down the plan (rows 5–57, a handful of
columns). Compact closed the columns but left ~110 rows across two layers; at
the 86 px Fit floor that is thousands of px of scrolling. On a synthetic show
of that shape at 1440×900, Compact + Rotate fits at 33 px per cell (612 px of
content in a 571 px box), against 1,629 px tall for Compact alone at 28 px.

## Rejected

- *Snap close rows* and *wrap lines side by side* in Compact (owner chose Rotate).
- Keeping the 86 px floor: the plot could never show a whole rig.
- A 28 px floor on touchscreens: below the touch-target minimum.

## Revisit if

K5's intensity bar does not read at 28–55 px tiles, or a show's numbering
reads wrong in both directions (then a per-show orientation setting is the
next step, not more view toggles).
