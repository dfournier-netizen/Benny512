# 0004 — Plan positions are exact; snapping and Scale are views of them

**Decided:** Dom (owner), 2026-11-17 12:05–12:20 -0500. **Recorded:** 2026-11-17 12:23:25 -0500. Chunks M1–M3. Reverses part of C2/C2b (positions in whole cells, collisions resolved by moving the later fixture, objects in half cells).

## Owner request and answers

"Rearrange based on 3D anytime, with a scale slider to adjust spacing for large rigs. Scale should just change the distance a single grid square represents, not fixture tile size. Allow free placement instead of locking to a grid. Grid visibility and grid snapping toggleable." Answers: Arrange acts on the **selection, or every fixture** when nothing is selected, hand placements included after a confirm; **Scale moves everything** on the plot (fixtures, hand placements, groups, objects); with **snapping on, overlapping tiles are nudged apart, with it off they may overlap**; snapping is to **half squares**.

## Decision

- Stored positions (`layoutItem.Col/Row`, object geometry) are exact fractional cells and may overlap. Derive/refresh place a fixture exactly at `(X − originX)/CellMM, (originY − Y)/CellMM` (to 1/1000 cell); no rounding, no free-cell search; outside ±999 → "out-of-range", left Unplaced.
- `CellMM` is the plan's **Scale** (mm per square). `scale` multiplies every plan position by old/new around the origin; the Unplaced grid and tile sizes (W/H) do not change; a scale that would push anything past ±999 is refused whole. Choosing a scale ends J1's automatic-derive ownership.
- `arrange` hands chosen entries (or all, confirm ARRANGE) back to automatic placement and places them; fixtures without a position are named (`noPosition`) and left alone.
- Snapping is a **Console view**: with it on, each fixture/group is drawn on the nearest half square, and an overlapping tile on the free half-square spot nearest its exact position (stacking order decides who keeps a spot); drags and arrow keys move from the drawn spot in half squares. Storage is never nudged, so changing Scale back and forth is exact. (M2 adds the Snap and Grid switches; M3 the Arrange button and Scale slider.)

## Why storage is not nudged

A nudge written back would drift on every Scale change and could never be undone; computing it on display keeps the MVR-derived truth intact.

## Rejected

Storing millimetres instead of cells: equivalent (cells × CellMM), but every API, test and Console path speaks cells; keeping cells as fractions changed far less working code.

## Revisit if

Display nudging is too slow on very large rigs (it is per layer, per render), or the owner wants a nudged position kept as the real one ("pin as drawn").
