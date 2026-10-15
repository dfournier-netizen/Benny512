# J1 — first bench test of v0.2.0 (Dom, 2026-10-12): reproduction and diagnosis

**Written:** 2026-10-12 11:17:39 -0400, branch `feat/intensity-faders` from `b6d7ee4`.

Dom's real show (90 fixtures, schema 8: 8 × GLP JDC1 "Mode 4 SPix PRO (62ch)", 16 × Elation Rayzor 1960 "Extended Pan540 Tilt270", 16 × Martin ERA 800 "Basic", 24 × Chauvet Rogue Outcast 2X Wash "22Ch", 26 × Elation Paladin Cube "Cells 24CH") was loaded locally through the real show decoder into the real `patch.Programmer` and `session.DMXOutputEngine` (fake transport), armed, and the frame read back. The show file and its profiles stay out of the repository (third-party GDTF data); the diagnostic tests that read it (`internal/patch/zz_domshow_local_test.go`, `DOMSHOW=<path>`) are local only and never committed. Committed tests use small synthetic profiles with the same structure.

## 1. MVR positions: every fixture Unplaced after a fresh import

Every imported entry has `location.known` with X/Y/Z and rotation, but the layout is only built by `POST /api/patch/layout/derive`, which only the Console's Edit layout → "Derive from positions" button sends. A fresh show has no layout (`derived: false`), so every fixture sits in the automatic Unplaced grid with the reason "not-derived". Nothing in the import path (`/api/patch/import`, `/api/patch/locations`) touches the layout. Not a parse problem: the positions are there; the derive step was never automatic.

## 2. Armed empty programmer — what the rig showed

Armed, nothing touched (before the fix), first fixture of three types:

| Fixture | Intensity part | On the wire |
|---|---|---|
| JDC1 405 | Beam Module Dimmer ch3, Plate Module Dimmer ch8, Beam Pixel 0–11 Dimmers ch51–62 | 0 (GDTF default 0) |
| JDC1 405 | 12 Plate Pixel virtual dimmers (RGB ch15–50) | colour 255/255/255 each — **full white** |
| Rayzor 516 | Head dimmer ch10–11 (16-bit) | 65535 — **full** (GDTF default) |
| Rayzor 516 | Spark LED Strobe Module dimmer ch99–100 | 65535 — **full** |
| Rayzor 516 | 19 RGBW Pixel virtual dimmers | colour 255 each — **full** |
| ERA 116 | Dimmer ch2–3 | 0 (GDTF default 0; Shutter1 default 22 = open) |

432 of the show's 662 intensity parts were above zero at Arm. Two causes: real Dimmer channels rest at their GDTF default (the Rayzor states full), and an untouched virtual dimmer was full by rule (G1/I2d4: "untouched = full = the profile look unchanged"), so a colour that defaults to 255 is lit. Owner decision 2026-10-12: **dark at Arm**.

## 3. JDC1 plate stays at full

Reproduced through the engine on fixture 405:

- Plate Pixel 0 selected as a cell, its virtual dimmer set to 0 → ch15–17 go to 0. The per-cell path works.
- The whole JDC1 selected, Dimmer 0 → 14 channels applied: ch3, ch8 and the 12 beam pixels. **The 12 plate pixels' virtual dimmers are not among them**; ch15–50 stay 255.
- The JDC1 type fader at 0 → the same 14 channels; ch15–50 stay 255. At 100 % → ch3, ch8 and ch51–62 go to 255; the plate pixels are unchanged.
- A dimmer test on the whole JDC1 pulses ch3, ch8 and the beam pixels but holds all 12 plate pixels (and the Plate Module colour) at the test white, 255 — they never pulse.
- A test on Plate Pixel 0 opens ch3 (the **Beam** Module dimmer) as the cell's "master".

**Root cause (hypothesis confirmed).** I2d4's rule "a fixture with a real master Dimmer: whole-fixture intensity is the master; the cells' virtual dimmers are reached only by selecting the cells" (`FixtureModel.hasMasterDimmer`, `inScope`) decides "has a master" as *any* real Dimmer channel that is not a cell's. The JDC1 has two such channels — the Beam Module and Plate Module dimmers — and neither is a master over the whole fixture; each is the dimmer of one part. So every whole-fixture intensity control (Dimmer set, fan, type fader, Lowlight of the whole fixture, dimmer tests) left the plate pixels out, and with untouched = full the plate shone at full whatever Dom did to the fixture. The cell test's "master" was simply the first such channel (ch3).

**"Strobe still lit and not controlled by the JDC1 fader"** is this same defect: the JDC1 fader drives ch3/ch8/ch51–62 but never the plate pixels, and those were at full from Arm (item 2). Under a dimmer test the plate pixels were held at the test white.

Not determinable here: whether the real JDC1's Plate Module dimmer (ch8) masters the plate pixels in this mode. With ch8 at 0 at Arm, Dom saw the plate lit, which suggests it does not (or that the pixels add to it) — Dom to confirm on the fixture. The fix below does not depend on it: a cell's virtual dimmer at 0 makes its colour output 0, and ch8 stays on top as a real channel.

## 4. Fix plan (owner-approved items 2 and 4)

- MVR: derive automatically on import, location re-import, and whenever the layout was never derived and fixtures have positions; refresh auto placements only, never manual ones.
- Dark at Arm: every intensity (real Dimmer and virtual) rests at 0; colour, position and everything else keep their GDTF defaults.
- Master rule: a fixture-level Dimmer is the fixture's MASTER only when it is on a geometry part of its own (no cells share its part) and it is the only such part, or the only one on a body geometry (Base, Body, Head, Yoke, Main). The JDC1 then has no master: whole-fixture intensity reaches every part, plate pixels included.
- Sub-fixture faders: per fixture type, a Master fader plus one per intensity part, grouped by geometry name with instance indices stripped. Owner-approved 2026-10-12 and built in J1d (§5).

## 5. Re-verified on Dom's real show after the fixes

**Written:** 2026-10-12 14:10:22 -0400, at `352a573`. Same method as above (real show decoder, real `patch.Programmer` and `session.DMXOutputEngine`, fake transport, armed, frame read back), plus a temporary copy of the show file opened by a real build in a temporary data directory for the layout and the fader bar. Raw output: session scratchpad `J1-domshow-after.txt`, `J1-render-domshow.txt`.

**(a) MVR positions (7ecd6ea).** The show file has 90 located entries and no layout. Opened by the build: `derived: true`, `autoDerived: true`, 90 layout items, **0 Unplaced**, layers "Height 0 mm" (26) and "Height 6426–6477 mm" (64) — the Console grid shows every fixture.

**(b) Dark at Arm (1eb0ba3).** Armed, empty programmer, faders untouched: **0 of 662 intensity parts above zero** (was 432). First fixture of each type:

| Fixture | Intensity on the wire |
|---|---|
| JDC1 405 | ch3 (Beam Module) 0, ch8 (Plate Module) 0, ch51–62 (beam pixels) 0, plate pixels ch15–50 colour out 0 |
| Rayzor 516 | head ch10–11 0/65535, SparkLED ch99–100 0/65535, 19 RGBW pixels ch22–97 colour out 0 |
| ERA 116 | Dimmer ch2–3 0/65535 |

Non-intensity channels keep their GDTF defaults (JDC1 ch1–2 = 128, Rayzor ch9 Shutter 50, ch13–14 = 107/132).

**(c) JDC1 plate (2193d3b).** Parts: Beam (13 dimmers: Beam Module + 12 beam pixels), Plate (13: Plate Module + 12 plate pixels' virtual dimmers); no master. Whole-fixture Dimmer 0 applies to **26** channels (was 14), the 12 plate virtual dimmers included. JDC1 type fader at 100 %: ch3, ch8 and ch15–62 all 255; at 0: all 0. A dimmer test on the whole JDC1 pulses all 12 plate virtual dimmers; a test on Plate Pixel:0 opens **ch8** (Plate Module), not ch3.

**(d) Sub-fixture faders (05e93d2, 352a573).** Faders on the real show: ERA (one), **Rayzor Master + Spark LED Strobe Module + RGBW Pixel**, Outcast (one), **JDC1 Master + Beam + Plate**, Paladin Cube (one). Each alone at 100 % on JDC1 405 / Rayzor 516:

| Fader | Drives |
|---|---|
| JDC1 Master | ch3, ch8, ch15–50 (plate colour out), ch51–62 — every part |
| JDC1 · Beam | ch3 and ch51–62 only |
| JDC1 · Plate | ch8 and ch15–50 only |
| Rayzor Master | head ch10–11 only |
| Rayzor · Spark LED Strobe Module | ch99–100 only |
| Rayzor · RGBW Pixel | the 19 pixels' colour ch22–97 only |

## 6. J1b — owner answers (2026-10-12 20:11), re-verified on the real show

**Written:** 2026-10-12 20:44:11 -0400, at `fbe7632`. Same method; raw output `J1b-domshow-after.txt` (session scratchpad).

- **Plate pixels have no virtual dimmers** (the Plate Module dimmer masters them on the fixture). JDC1 405: 0 virtual dimmers; parts Beam (ch3 + ch51–62) and Plate (ch8 only); no master. The show now has 566 intensity parts (the 96 plate-pixel virtual dimmers are gone); armed with an empty programmer **0 are above zero**. At Arm the plate pixels sit at their GDTF colour (255) behind ch8 = 0.
- **The Master drives every part.** Each fader alone at 100 %:

| Fader | Drives |
|---|---|
| JDC1 Master | ch3, ch8, ch51–62 (plate pixels follow ch8) |
| JDC1 · Beam | ch3 and ch51–62 only |
| JDC1 · Plate | ch8 only |
| Rayzor Master | head ch10–11, the 19 pixels' colour ch22–97 (their virtual dimmers), SparkLED ch99–100 |
| Rayzor · Spark LED Strobe Module | ch99–100 only |
| Rayzor · RGBW Pixel | ch22–97 only |

- **Console, whole fixture:** JDC1 Dimmer reaches 14 channels (ch3, ch8, 12 beam pixels); Rayzor 21 (head, SparkLED, 19 pixel virtual dimmers). Dimmer on Plate Pixel:0 is refused: "Cell Plate Pixel of GLP JDC1 Strobe has no Dimmer."
- **Tests:** a dimmer test on the whole Rayzor drives all 19 pixel virtual dimmers; on the whole JDC1 none (it has none) and its 13 colour scopes show white while unset. A test on Plate Pixel:0 drives ch15–17 and **ch8**; on RGBW Pixel:21 its virtual dimmer, with the head (ch10–11) held open.
- **Short labels:** ERA 800 Performance, Proteus Rayzor 1960 (+ Spark LED Strobe Module, RGBW Pixel), Rogue Outcast 2X Wash, JDC1 Strobe (+ Beam, Plate), Paladin Cube — derived (the show predates kept short names).

## 7. J1c — the Master as a multiplier (owner 2026-10-13 09:31), on the real show

**Written:** 2026-10-13 10:09:01 -0400, at `4e92803`. Same method; raw output `J1c-domshow-after.txt`. Dom's show file was imported before J1c and carries **no geometry tree**, and his MVR/GDTF files are not in the folders connected to this session, so the show could not be re-imported with its tree here: the Rayzor runs under the legacy rule and which Rayzor parts sit under Head_Extended is **not yet determined** (re-run the harness on his re-imported show).

- 590 intensity parameters (the 566 of §6 + 24 virtual masters: 8 JDC1, 16 Rayzor); armed, empty programmer, faders untouched: **0 above zero**.
- **JDC1 405** — no master channel, so both parts are multiplied in software (virtual master over ch3, ch51–62 and ch8):

| Faders | On the wire |
|---|---|
| Beam 100 %, Master at rest | ch3 0, ch51–62 0 |
| Beam 100 %, Master 50 % | ch3 128, ch51–62 128 |
| Plate 100 %, Master at rest | ch8 0 |
| Plate 100 %, Master 50 % | ch8 128 |

- **Rayzor 516** — legacy (no tree): the Master fader is marked treeUnknown; the SparkLED and the RGBW pixels count as governed by the head dimmer (no software multiply):

| Faders | On the wire |
|---|---|
| RGBW Pixel 100 %, Master at rest | pixels ch22–97 255, head ch10–11 0 |
| RGBW Pixel 100 %, Master 50 % | pixels 255, head 128/0 (32 768) |
| Spark 100 %, Master at rest | SparkLED ch99–100 255, head 0 |
| Spark 100 %, Master 50 % | SparkLED 255, head 128/0 |

  Whether this is dark on the fixture at Master 0 depends on the head dimmer actually governing the pixels and the SparkLED; the tree from his re-import decides it.
- **Highlight on a cell** (temp copy in a real build): RGBW Pixel:21 → 3 channels (its virtual dimmer, the head, the virtual master); a JDC1 plate pixel → 5 (its RGB, ch8, the virtual master).

## 8. J1d — no double dimming inside a part (owner 2026-10-13 10:39), on the real show

**Written:** 2026-10-13 11:06:10 -0400, branch `feat/intensity-polish` at `4c5c106`. Same method; raw output `J1d-domshow-after.txt`. Dom's show still has **no geometry tree** (not re-imported), so as stored nothing changes; to exercise the rule on his real JDC1 profile the harness also gives it a **synthetic** tree built from its own instance names (each beam/plate pixel under its part's non-cell dimmer geometry, the modules on a root). Whether the real JDC1 GDTF nests the beam pixels under the Beam Module is **not yet read**.

- Rule: inside one part, a dimmer whose geometry descends from the geometry of another dimmer of the same part is inner. A part fader sets the outermost dimmer(s) and holds the inner ones open (weakest claim); the software master multiplies only outer dimmers.
- **JDC1 405**, as stored (no tree): inner none; software master over ch3, ch51–62, ch8 — J1c unchanged:

| Faders | ch3 | ch8 | ch51–62 |
|---|---|---|---|
| Master 100 %, Beam 50 % | 128 | 0 | 128 each (25 % on the fixture if the pixels sit under ch3) |
| Beam 100 %, Master 50 % | 128 | 0 | 128 each |

- **JDC1 405**, synthetic tree: inner ch51–62; software master over ch3 and ch8 only:

| Faders | ch3 | ch8 | ch51–62 |
|---|---|---|---|
| Master 100 %, Beam 50 % | 128 | 0 | 255 each (held open: 50 % on the fixture) |
| Beam 100 %, Master 50 % | 128 | 0 | 255 each |
| released | 0 | 0 | 0 |

- **Rayzor whole-fixture Dimmer card:** no longer MIXED at equal levels (16-bit head 32 768 and 8-bit virtual master 128 read "50 %"); J1c showed "MIXED 0–0 %" at rest.
- **Re-run 2026-10-13 12:52:35 -0400 at `14ca193`** (`J1d-domshow-final.txt`): unchanged frames as above; 590 intensity parameters, 0 above zero at Arm. Fader flags: treeUnknown on the Rayzor Master (J1c note) and on the JDC1 **Beam** part (Beam Module + 12 beam-pixel dimmers, no tree: "Every dimmer of this part takes this level — re-import the GDTF to refine."); not on the Plate (one dimmer), the Rayzor SparkLED (one) or RGBW Pixel (virtual dimmers only).
