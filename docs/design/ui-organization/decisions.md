# W1b decisions

This is a design package, not production implementation. Source: `brief_2026-10-19_1557.md`; W1 component specifications win on shared chrome. All sample records are invented. Owner answers in this chat supersede the brief's provisional recommendations.

## Owner decisions received

| Topic | Owner answer | Consequence |
|---|---|---|
| Navigation | Show / Network groups | Show: Patch, Console, Tests, Rig Walk. Network: Nodes, Devices, Analyzer. Settings last. No renamed or merged screens. |
| Settings | Index + scrolling form | One staged form, section anchors, always-reachable Apply bar. |
| Dock / Tools | Right-only; Raw DMX and Tools in Controls | Patch, Devices and Nodes share a right inspector; W1 windows may float. |
| Tests / shortcuts | Full selection tools; no global shortcuts | Preserve W1's full tools. No global single-letter bindings. |
| Tile colour | Programmer colour + name/outline | Record a W1 follow-up; do not alter W1 assets in W1b. Effective source remains separate. |
| Row defaults | Comfortable; other defaults as recommended | 48 px rows initially; Compact is optional. Inspector opens on row inspection; diagnostics start closed once devices exist. Scenes deliberately show an opened inspector and checked rows. |
| Nodes | Shared inspector | Configuration still stages until Apply. |
| Fixture controls | “i guess controls only on the main console screen” | No global Controls button or always-open controls on other screens. Console remains one nav action away. |

## Designer calls within delegated latitude

| Choice | Decision and reason |
|---|---|
| Section order | Tools → selection if relevant → list/workspace → existing group-fader bar; working data begins immediately. |
| Screen headings | Visually hidden h1, nav names the screen; retain the accessible heading without a repeated visual title. |
| Density | Comfortable 48 px, Compact 40 px; coarse targets force 48 px rows and ≥44 px controls; no text shrinking. |
| Widths | W1 inspector 440 px; range 320…60% of workspace; the table gets the remainder. A narrow list owns horizontal scroll rather than hiding columns. |
| Sticky regions | Table headers stick within each list; toolbar, Settings Apply and Rig Walk transport sit outside the scrolling body; document never scrolls. |
| Primary actions | Keep Add entry, Discover, Refresh, capture Pause, walk actions, Apply and DISARM visible. |
| Secondary actions | Import / Export / Adopt discovered use named menus. New show uses More. A separated destructive action retains its existing confirmation. |
| Patch row actions | Inspect via the underlined Name button (keyboard equivalent to row inspection); move Edit and reorder to inspector; Delete is last under Entry actions. Checkboxes never change Console fixture selection. |
| Patch columns | Keep universe, address/footprint, FID, name, short type and RDM match; unknown values say Not reported. No hidden essential columns. |
| Truncation | Ellipsize names/type only, with complete text in title/accessibility; FID and numeric columns retain reserved widths. |
| Checked rows | Use W1 selection-count grammar; Selection menu offers select all / clear. No new bulk edit/delete operation is invented. |
| Help | Field-local named ? disclosures replace static paragraphs. Keep effect timing visible as word tags, not hidden in help. |
| Show hints | Omit a new global hints setting; individual help remains independently accessible. No new default is introduced. |
| Settings | Section index + single scroll container; 200 px index, 176 px at 1024. Group related fields in two columns only where adequate. Danger zone remains last. |
| Devices | Discovery + common filters first; detailed filter options under Filters; clear-memory action under More after separator. Inspector retains all parameter routes and exceptions. |
| Nodes | Keep list + detail relationship, reuse dock chrome; port configuration grouped by numbered ports, no protocol change. |
| Analyzer | Traffic tabs and capture/filter controls above an internally scrolling packet table; detail below with existing RDM filters reachable. |
| Rig Walk | Two focused columns with a fixed-in-layout 60 px Previous/Next row, counter outside detail scrolling; scope/order and verdict routes retained. |
| Hierarchy | No cards around every row or field; separators divide tasks. Glyphs are supplemental and labels carry meaning. Reuse existing assets; no new icons needed. |
| Copy | Keep ports, BiDi, circuit / draw / W, ARM / DISARM and BLACKOUT. Universe display calls the literal production UI formatters. |

## Verdict on every §4 recommendation

| Brief item | Verdict | Reason / adaptation |
|---|---|---|
| 4.1.1 W1 header, no duplicate headings | Accepted | Reuse W1 source styles; retain hidden h1. |
| 4.1.2 One toolbar, destructive menu separation | Accepted | All six screens show named tools and separated destructive routes. |
| 4.1.3 Help on demand | Adapted | Named field disclosures; omit global Show hints. |
| 4.1.4 List + inspector | Accepted | Patch, Devices, Nodes; Analyzer retains packet detail because its decoding task differs. |
| 4.1.5 Sticky / dense rows | Adapted | Owner chose Comfortable default; Compact remains optional, coarse controls never shrink. |
| 4.1.6 Navigation | Accepted by owner | Group labels chosen; divider and existing-order alternatives remain review scenes. |
| 4.2 Patch table and columns | Accepted | 48 px default; first row directly below tools/selection. |
| 4.2 Patch inspection / bulk bar | Adapted | Explicit inspect button, row background cue and checkboxes; no unapproved bulk behavior. |
| 4.2 Patch toolbar | Accepted | New show in More; replace patch separated from merge. |
| 4.2 Universe help | Adapted | ? in list footer keeps header compact and leaves header navigation predictable. |
| 4.2 Settings index | Accepted by owner | One form avoids uncertainty about pending changes across sections. |
| 4.2 Settings help / timing / danger | Accepted | Restart / Next ARM tags remain visible; full reset retains typed RESET. |
| 4.2 Devices toolbar / memory / diagnostics / detail | Accepted | Diagnostics closed by owner-approved default once devices exist. |
| 4.2 Nodes list / detail | Accepted by owner | Shared inspector with Apply-to-confirm. |
| 4.2 Analyzer | Accepted | Table owns scrolling; controls remain reachable at both laptop sizes. |
| 4.2 Rig Walk | Accepted | Counter and transport stay in reach; details own overflow. |

## Fixed contracts

No changes to ARM/DISARM, lease, output semantics, Blackout, the 60 px slab, Apply-to-confirm, signed-off exceptions, confirmation steps, group-fader behavior, W1 component specs or theme editor. No features removed, screens merged, controls moved to another screen, or production files modified. The swatch follow-up is recorded separately in owner-questions.md and awaits W1 implementation.
