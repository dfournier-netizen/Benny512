# Owner answers and review options

Answers below came directly from the owner in this chat. These are settled design directions, not pending questions and not permission to change output behavior. Alternatives remain documented to make the decisions reviewable on a phone.

| Question | Options | Recommendation | Owner answer |
|---|---|---|---|
| Navigation? | A. Show / Network groups; B. reordered tabs with divider; C. current order | A | **A** |
| Settings organization? | A. index + scrolling form; B. one section at a time | A | **A** |
| Dock sides? | A. right only; B. left or right | A | **A** |
| Raw DMX / Tools? | A. stay in Controls; B. separate windows | A | **A** |
| Selection on Tests? | A. full Lasso/Add/Select; B. click only | A | **A** |
| New global shortcuts? | A. none; B. guarded shortcuts | A | **A** |
| Tile colour? | A. programmer + name/outline; B. effective output + source; C. omit | A | **A** |
| Row default? | A. Compact 44 px proposed; B. Comfortable; C. preserve current defaults | A originally | **B**; final spec Comfortable 48 px, Compact 40 px |
| Inspector opening? | A. on row inspection; B. initially open; C. preserve current defaults | A | **A**, via “other defaults as recommended” |
| Device diagnostics default once devices exist? | A. closed; B. open; C. preserve current defaults | A | **A**, via “other defaults as recommended” |
| Nodes detail? | A. shared inspector; B. current detail pane | A | **A** |
| Feature removal / cross-screen moves / merges? | A. keep all routes and screens; B. propose a specific removal or move | A | Owner asked about always-available fixture controls, then clarified **controls only on main Console**. No removal or move proposed. |

## Dependency map and W1 follow-up

- Patch, Devices, Nodes rely on the approved right-only dock and unchanged W1 dock/float grammar. They do not rely on colour swatches, Raw DMX, Tests selection, or shortcuts.
- All six screens reuse W1 header/output and toolbar/menu/toggle grammar unchanged. No W1 component change is required by W1b.
- Console / Tests navigation destinations depend on W1. W1 source files remain untouched. The standalone W1b preview explains this when either destination is selected.
- **Swatch follow-up, approved direction:** programmer colour with a colour name and outline. Options: name + outline (recommended/approved), brightness floor, or omit. Keep large-preview luminance caps; implement and contrast-check the small-swatch fix in a W1 follow-up, not here.
- **Beam/Gobo follow-up:** break at slash (recommended), use “Beam”, or widen its tab. No choice is needed for W1b because it introduces no family tabs. W1 review remains necessary before production integration.
- **Plan Fit follow-up:** explanatory hint when the tile floor prevents fitting (recommended), document Compact as the no-scroll option, or redesign Fit through a separately approved behavior proposal. No W1b screen depends on Plan Fit.

No further answer is required to review these assets. Production integration still requires visual owner review and the existing engineering gates; approving a design does not certify hardware behavior.
