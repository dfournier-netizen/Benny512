# W1b component additions

Read W1 `component-specs.md` first. Do not replace its header, toolbar/menu grammar, ON toggle, dock/floating chrome or selection count. This file specifies only screen contents and components W1 did not define.

## Screen toolbar contents

One nonwrapping W1 toolbar row; primary actions left, secondary named menus, flexible space, View/More right. Fine buttons 38–40 px; coarse ≥`--b5-size-touch-min` in both axes. If labels exceed width, this toolbar alone scrolls. Never compress the output header or controls to fit a toolbar. Normal tab order; menu Up/Down/Home/End, Escape restores its trigger, outside click closes. Any destructive item is separated and retains its exact production confirmation.

Patch: show picker, Add entry, Import (MVR/GDTF), Adopt discovered (merge / separator / replace patch…), Export (JSON/TXT), View (row density), More (New show…). Devices: node/port, Discover, class, text filter, Filters (node, universe, sort, clear), More (separator / clear memory…). Nodes: Refresh, More. Analyzer: All traffic/RDM, Pause, kind, universe, Export, More (separator / clear list). Rig Walk: scope, order, Identify off/all off, End walk, Export. Settings: staged-changes context, More.

## Help disclosure and field tags

Native details/summary with visible “?” and an accessible name “Help: [field]”. Enter/Space opens it; full explanation follows in reading order, normal tab traversal, no focus trap or hover dependency. Open help expands only its local scrolling pane. Keep safety-critical timing out of help: dashed outlined **Restart** / **Next ARM** tags immediately beside the relevant label. Art-Net adapter uses Restart; sACN adapter uses Next ARM, matching current settings.js. Tags use primary text and opaque surface; colour conveys no extra meaning.

## Sticky table header and density

Semantic table/caption-equivalent labelled scroll region; th scope=col, sticky top=0, opaque surface and 2 px essential boundary. The scroll region has a keyboard stop, owns both axes and never grows the document. Comfortable 48 px rows is the owner-approved default; Compact 40 px fine-pointer, coarse 48 px minimum. Native checkbox inside a labelled ≥44 px coarse target. A 800 px table minimum means the 1024 inspector-open scene may scroll horizontally inside its list; all columns remain reachable.

Universe, address/footprint, FID, name, type, match remain present in Patch. Short type/name may ellipsize with full title/accessible text; identifiers and addresses retain allocated widths. Do not collapse noncontiguous FIDs into a range. Selected checkbox rows use solid outlined count + check + bold; inspected row has independent left boundary. The Name button is the non-drag keyboard route to inspection. Production can also make row-background clicks invoke that same route while ignoring checkboxes and other controls.

Density is a browser-local presentation preference, not part of the show. The review changes it for the current scene only; persistence/version migration belongs to integration. Reuse W1's check/bold/2 px border ON grammar for any toggle, and role=menuitemcheckbox/aria-checked for density choices.

## Inspector contents

Use W1 §4 unchanged: 440 px initial, 320…60% bounds, right dock, independent body scroll, Dock/Float/collapse/close, keyboard title movement and resize, coarse gutter clearance, browser-local geometry. Opening an inspected record focuses its title; Close restores its row. No inspector initially until a row is inspected. Review scenes explicitly demonstrate the open state. Empty-state copy: “Select a [patch entry/device/node] to inspect it.” Unknown values must read “not reported”.

| Screen | Inspector reading order / routes |
|---|---|
| Patch | FID, full name/type, universe formats, address/footprint, match; Edit → staged fields + Apply/Discard; reorder up/down; separator; Entry actions → Delete with existing confirmation. |
| Devices | Model/name, UID, class/manufacturer, address, node/port, unknown firmware; existing Identify exception; staged address editing; supported parameters, sensors/status and all other existing control/confirmation routes. |
| Nodes | Name/IP/manufacturer/firmware; staged short name; numbered port disclosures with Art-Net formatting; Apply/Discard; existing network-configuration fields and safeguards. |

The review illustrates representative fields and links to the full existing editors; it does not reimplement every PID, port capability, or config form. Production must embed existing editor bodies rather than remove capabilities that are not pictured.

## Settings section index

Left, 200 px (176 px at 1024), its own overflow when required; right, one scrollable form. Native anchor links: Network, Universe numbering, sACN, Output, Capture & logging, Theme, Danger zone. Anchors scroll only the form. Production marks current visible section using aria-current=location (review shows the initial Network marker). Two-column field groups only within adequate content width. 20–28 px inline padding; no decorative card per field.

One Apply/Discard bar outside the form scroll, visually sticky to that screen's bottom and above group faders. Changes stage, Apply commits, Discard restores. Existing theme-editor exceptions stay intact. Full reset remains last, separated, with its typed RESET confirmation and existing terminal shutdown behavior; do not add or remove a confirmation step.

## Analyzer and Rig Walk overflow

Analyzer: capture tools never scroll off; packet table scrolls; 130 px packet-detail region owns detail overflow. Both traffic modes retain all current RDM filter/paired-view/export routes. Rig Walk: counter above scrolling detail, Previous/Next 60 px below it; verdict buttons 60 px, explicit words. Keep scope/order, retry identify, per-device controls, notes, auto-advance and exports. No new automatic advancement behavior.

## Reuse and asset policy

No new colour roles or SVGs are needed. Reuse W1 and production role tokens; contrast.md reports every new use pairing. Borders/grid rules that are decorative do not communicate state. Essential boundaries, focus, checked/inspected state and timing use opaque existing role colours and words. SVG remains the copied, unmodified production sprite with no generated metadata. No W1 swatch/tab/Fit edits occur in this package.
