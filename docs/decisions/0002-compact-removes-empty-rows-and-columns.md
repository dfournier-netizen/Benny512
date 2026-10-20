# 0002 — Compact removes empty rows and columns (not W1's row packing)

**Decided:** owner (Dom), 2026-10-20 15:22 -0400, confirming the K2 build. Recorded 2026-10-20 15:29:10 -0400.

## Decision

The Console's **Compact** spacing removes every empty row **and** every empty
column of a layer and draws the fixtures in what is left. Each fixture keeps
its row and its column order, so lines of fixtures stay lines. It is display
only: the stored layout never changes, and editing the layout always draws
Plan.

## Evidence

W1 `component-specs.md` §5 proposed packing each row left to right. On the
owner's MVR-derived show (K2, 2026-10-19) the trusses run down the plan: the
truss layer uses columns 1–2, 20 and 37–38 across rows 5–57, and the floor
layer columns 0 and 40 across rows 0–62. Each row held one to five fixtures,
so packing rows scattered every truss line across the screen. Removing empty
rows and columns closes the gaps and keeps the lines.

## Rejected

- **Row packing (W1 §5):** breaks vertical lines of fixtures, which is how
  this show — and any show whose trusses run upstage–downstage — is laid out.

## Revisit if

A show's layout has fixtures on nearly every row and column (so Compact
removes nothing useful) and the owner wants a denser view; that would be a
new mode, not a change to this one.
