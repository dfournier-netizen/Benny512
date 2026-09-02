# Benny512 screen kit

The shared vocabulary extracted from the Reconcile and Rig Check (Function
check) screens, which the owner signed off on. Everything here lives in
`benny512-components.css` under the `BENNY512 SCREEN KIT` banner. Use it; do
not re-invent it on your screen.

Who this is for: whoever is converting one of the remaining screens. Read the
five rules, find the component whose *job* matches yours, copy its markup
exactly, and only then think about anything new.

---

## The five rules

These are not style preferences. This app is used **standing up, in the dark,
on a tablet held at arm's length under a truss**, by a lighting tech whose
hands are busy.

1. **Never colour as the sole signal.** Every state is carried by a **word**
   first, then an icon, then a border weight or style, and only then by hue.
   The test: screenshot your screen, desaturate it, and check you can still
   tell the states apart. If you can't, it isn't finished.
2. **44px minimum touch target** on anything pressable.
   `.b5-btn` already carries `min-height: var(--b5-size-touch-min)`. Do **not**
   write `min-height: 44px` again in a container rule — five such rules existed
   in the code this kit replaced (`__row .b5-btn`, `__actions .b5-btn`, …) and
   all five were dead. Never write the literal `44px`; use the token, so a
   future change to the touch minimum reaches every control.
3. **Name the unknown, don't hide it.** A value the app doesn't have is said
   out loud — "addr unknown", "Not read", "name from file" — never printed as a
   plausible `0`. Dashed borders mean "nothing is attached here / this is not
   ours": `.b5-pill--open`, `.b5-pill--unread`, `.b5-statecard.is-open`,
   `.b5-fromfile`, `.b5-inset`.
4. **Wide content scrolls inside its own box.** The page body must never scroll
   horizontally. Every container here that can hold wide content sets
   `min-width: 0`; keep that when you nest.
5. **Universe numbers go through `UI.formatUniverse` / `UI.parseUniverse`,
   always**, from the raw 0-based wire number — never re-derived from a string
   already on screen. `UI.universeInputAttrs()` gives `min`/`max` for an input
   that edits one, and `UI.universeBaseLabel()` names the active notation.

Also standing: high contrast, dyslexia-friendly type (left-aligned, generous
line-height, no justified text, no all-caps runs longer than a chip label), and
real keyboard-reachable controls with real labels. Use `<button>`, not a
clickable `<div>`.

---

## Components

### Layout: numbered step section

Gives a screen a readable top-to-bottom order instead of unrelated panels.

```html
<section class="b5-step-section" aria-labelledby="scopeHead">
  <h2 class="b5-step-section__head" id="scopeHead">
    <span class="b5-step-num">1</span> Testing scope
    <span class="b5-step-section__note">2 on</span>
  </h2>
  …
</section>
```

Use `.b5-group` + `.b5-group__head` for a labelled band inside a section.

### Layout: two-pane board

Two panes side by side at ≥900px, stacked below. **Every pane needs a
sub-caption** saying in the owner's words what it holds — that is not
decoration, it is the component.

```html
<div class="b5-board">
  <section class="b5-board__pane" aria-labelledby="leftHead">
    <h3 class="b5-board__head" id="leftHead">
      <span class="b5-board__title">Intended fixtures</span>
      <span class="b5-board__sub">what the patch says should be there</span>
    </h3>
    <div class="b5-board__list"> …cards… </div>
  </section>
  …second pane…
</div>
```

An empty pane renders `<p class="b5-board__empty">` naming what is missing and
what to do about it — never nothing.

### State pill — the one way this app draws a state

Pick **one skin**, optionally **one tone**, optionally `--solid`.

| slot | classes |
|---|---|
| skin (required) | `--sm` chip-in-a-tile · `--md` card status · `--lg` status lamp · `--tag` compact word in a dense row |
| tone (optional) | `--ok` `--warn` `--danger` `--info` `--accent` `--open` `--unread` |
| fill (optional) | `--solid` paints the tone's tint behind it |

```html
<span class="b5-pill b5-pill--md b5-pill--warn">
  <svg><use href="/icons/benny512-icons.svg#b5-icon-status-warning"/></svg>
  Committed · 2 settings differ
</span>
```

**A pill always contains a word.** There is no icon-only variant on purpose. A
tone only ever changes border colour and text colour, so it is safe on any
skin; `--warn` and `--accent` additionally thicken the border, and `--open` /
`--unread` dash it, so the state survives greyscale.

If a state has no tone in your table, emit **no** tone class — an untoned pill
is honest. Never borrow a neighbouring state's colour.

### State card — one row in a board pane

The left border is the state marker, and its **width** changes with state as
well as its colour: settled = hairline, needs attention = thick, unattached =
dashed.

```html
<article class="b5-statecard is-warn">
  <div class="b5-statecard__top">
    <span class="b5-linkbadge">3</span>          <!-- or --none for "no partner" -->
    <div class="b5-statecard__id">
      <strong class="b5-statecard__name">CF2 48</strong>
      <span class="b5-statecard__meta">Chroma-Q Color Force II 48 · Universe 3 · addr 1</span>
    </div>
  </div>
  <div class="b5-statecard__state"> …one .b5-pill--md… </div>
  <div class="b5-statecard__actions"> …b5-btn… </div>
</article>
```

States: `.is-ok` `.is-warn` `.is-danger` `.is-open` `.is-armed`. These are
**tone** words, not domain words — map your screen's own vocabulary onto them
in one table in your JS, the way `reconcile.js`'s `DIFF_TONE` does.

`.b5-linkbadge` carries the *same number on both sides* of a related pair, so a
relation is readable across two panes with no line drawn and no colour relied
on.

### Tile grid — a big touch target with an unmistakable on/off

```html
<div class="b5-tilegrid">
  <div class="b5-tile is-on">
    <button class="b5-tile__toggle" aria-pressed="true">
      <span class="b5-pill b5-pill--sm b5-pill--accent b5-pill--solid">
        <svg>…status-ok…</svg><span class="b5-tile__stateword">ON</span>
      </span>
      <span class="b5-tile__label">Ballyhoo</span>
      <span class="b5-caption">runs on 8 of 8 fixtures</span>
      <span class="b5-tile__notes">7 skipped</span>
    </button>
    <button class="b5-tile__more" aria-expanded="true">…chevron…<span>Settings</span></button>
    <div class="b5-tile__panel"> …b5-param rows… </div>
  </div>
</div>
```

The tile is ≥88px tall — double the touch minimum, because a mis-tap here moves
real lights. The state is carried by **four** independent signals: the word
ON/OFF, an icon, the tile's border weight, and its background. Keep all four.
Set `aria-pressed` on the toggle and `aria-expanded` on the disclosure.

### Parameter row

```html
<div class="b5-param">
  <span class="b5-param__label">Rate <span class="b5-text-mono b5-param__value">1.00 Hz</span></span>
  <input type="range" class="b5-range-touch" …>
  <span class="b5-param__hint">0° to 360° spreads one cycle across the fixtures.</span>
</div>
```

`.b5-range-touch` inside a `.b5-param` gets a proper thumb on both pointer
types. `.b5-numinput` is the compact numeric entry that still clears a
fingertip.

### Choice card — one consequential checkbox with a sentence

```html
<label class="b5-choicecard b5-choicecard--caution is-on">
  <input type="checkbox" checked>
  <span class="b5-choicecard__box" aria-hidden="true">…icon when on…</span>
  <span>
    <span class="b5-choicecard__title">Isolate: drive only the tested channel — ON</span>
    <span class="b5-choicecard__body">Everything else is held at zero…</span>
  </span>
</label>
```

Default on-state is the kit's accent. Add `--caution` when the mode makes the
**rig** behave surprisingly (Isolate makes fixtures emit no light) — amber, not
red; red is reserved for errors and destructive actions. Note the title
repeats the state as a word ("— ON"): rule 1.

### Sticky action bar

The primary and destructive actions of a screen, in **fixed, non-moving
positions**, both always present. Sticky, never `position: fixed` — fixed bars
need a body reserve-space dance this app has been bitten by twice.

```html
<section class="b5-actionbar" aria-label="Output">
  <div class="b5-actionbar__status">
    <span class="b5-actionbar__title"><span class="b5-step-num">3</span> Output</span>
    <span class="b5-pill b5-pill--lg b5-pill--ok b5-pill--solid">…icon…LIVE</span>
  </div>
  <div class="b5-actionbar__buttons">
    <button class="b5-bigbtn b5-bigbtn--go">…icon…START</button>
    <button class="b5-bigbtn b5-bigbtn--stop">…icon…STOP</button>
  </div>
</section>
```

`--go` is an outlined **pill**; `--stop` is a filled **square-cornered slab**.
Different silhouettes, deliberately: at arm's length in the dark the shape
reads before the hue does, and a colour-blind reader can never confuse them.
The destructive/abort control is never hidden, never disabled, and never moves.

### Toolbar, mode bar, inset, why-list

- `.b5-toolbar` / `__row` / `__search` — the screen-top control row. New screens
  use this, not the older desktop-first `.b5-filterbar`.
- `.b5-modebar` — a **sticky** reminder of a mode the user entered and must
  leave deliberately, with its own Cancel. Put any icon as a direct child of
  `.b5-modebar`, not inside `__label` (the label is wrapping prose and has no
  icon gap).
- `.b5-inset` / `__head` — "about the thing above", dashed so it never reads as
  another card in the list.
- `.b5-why` / `.b5-why__kind` — the **evidence** behind a conclusion the app is
  offering. Body-sized text, never fine print: this is what stops the wrong
  fixture being configured when two are identical.

### Honest-unknown markers

- `.b5-fromfile` — a label repeated out of a fixture file, not written by this
  app. Say so; do not pass it off as considered wording.
- `.b5-note` — a wrapping explanatory sentence. `.b5-caption` — a single
  supporting line under a label.

### Diff line — intended vs actual, one press to resolve

```html
<div class="b5-diffline is-warn">
  <div class="b5-diffline__head">
    <span class="b5-diffline__label">DMX address</span>
    <span class="b5-pill b5-pill--tag b5-pill--warn">Differs</span>
  </div>
  <dl class="b5-diffline__vals">
    <dt>Intended</dt><dd>1</dd>
    <dt>As found</dt><dd>21 <span class="b5-note">· read 2 min ago</span></dd>
  </dl>
  <div class="b5-diffline__action">
    <button class="b5-btn b5-btn--primary">…icon…Apply to fixture</button>
  </div>
</div>
```

Tones: `.is-ok` `.is-warn` `.is-info` `.is-unread`.

**Do not turn this back into a table.** It was a five-column table once;
render-proofing at 1024×768 showed the Action column — the Apply button — sat
entirely outside the visible card, reachable only via a nested horizontal
scrollbar. A control a tablet user cannot see is a control that does not exist.

### Icons

Every icon is `<svg><use href="/icons/benny512-icons.svg#b5-icon-NAME"/></svg>`,
which `UI.icon(name)` builds for you. `app.css` now sets a **global default
size** (`--b5-size-icon-md`, 18px) so a new call site can no longer ship a
glyph at its raw symbol box — that failure shipped twice in this codebase and
was caught by looking at a screenshot both times, never by an assertion. Size a
new component's icon explicitly only when 18px is wrong for it.

---

## Worked example: a plain table → the system

Before. A read-only table; the status is a bare coloured word; the row's action
is a link; on a 430px-wide pane the last two columns fall off the edge.

```html
<table class="b5-table">
  <thead><tr><th>Port</th><th>Universe</th><th>Status</th><th></th></tr></thead>
  <tbody>
    <tr><td>Port 1</td><td>3</td><td style="color:#c80100">offline</td>
        <td><a href="#" onclick="reset(1)">reset</a></td></tr>
  </tbody>
</table>
```

Four things are wrong: the status is colour-only, the action is a 20px-tall
link, the row can't say "I don't know", and the table overflows its container.

After.

```html
<section class="b5-step-section" aria-labelledby="portsHead">
  <h2 class="b5-step-section__head" id="portsHead">
    <span class="b5-step-num">1</span> Ports
    <span class="b5-step-section__note">1 of 4 not answering</span>
  </h2>

  <div class="b5-board__list">
    <article class="b5-statecard is-danger">
      <div class="b5-statecard__top">
        <span class="b5-linkbadge b5-linkbadge--none" aria-hidden="true">·</span>
        <div class="b5-statecard__id">
          <strong class="b5-statecard__name">Port 1</strong>
          <!-- rule 5: the number came from UI.formatUniverse(row.universe) -->
          <span class="b5-statecard__meta">Universe 3 · addr unknown</span>
        </div>
      </div>
      <div class="b5-statecard__state">
        <span class="b5-pill b5-pill--md b5-pill--danger">
          <svg><use href="/icons/benny512-icons.svg#b5-icon-status-error"/></svg>
          Not answering
        </span>
      </div>
      <div class="b5-statecard__actions">
        <button class="b5-btn b5-btn--danger" data-reset="1">Reset this port</button>
      </div>
    </article>
  </div>
</section>
```

What changed, and why each one:

- **Status**: `.b5-pill--danger` carries the word *and* an icon *and* a border
  colour. Desaturate it and it still reads (rule 1).
- **Row**: `.b5-statecard.is-danger` adds a 10px left rule — a width change, so
  the pane scans in greyscale too.
- **Action**: a real `<button class="b5-btn">`, 44px, keyboard-reachable, with
  a label that says what it does ("Reset this port", not "reset") (rule 2).
- **Unknown**: "addr unknown", not `addr 0`. `--linkbadge--none` says plainly
  that this port is paired with nothing (rule 3).
- **Overflow**: stacked blocks have no minimum width, so nothing falls off the
  edge of a pane and the page never scrolls sideways (rule 4).
- **Universe**: formatted once, at the presentation boundary, from the raw
  0-based number (rule 5).

If the table is genuinely tabular data a tech scans down (the Analyzer's packet
list, the Send grid), keep `.b5-table` — but give it `.b5-table--responsive`
with `data-label` on every `<td>` so it stacks on a narrow pane, and put it in
its own `overflow-x: auto` box.

---

## Deliberately kept different

Two divergences between the two source screens survived the merge, on purpose:

- **`.b5-toolbar__search input` is not `.b5-input`.** A search sitting in the
  control row reads as part of that row (raised surface); `.b5-input` is inset,
  which reads as a form field cut into the page. Same height, same focus ring.
- **`.b5-choicecard--caution` keeps the amber isolate treatment** rather than
  collapsing into the accent on-state. Isolate changes what the rig physically
  does; it should not look like an ordinary preference.

## Known gaps, for whoever hits them

- `.b5-filterbar` (the older desktop-first chip bar, still used by other
  screens) overlaps `.b5-toolbar` in purpose. Fold it in once the screens using
  it have been converted.
- `.b5-range-touch` renders a 28px thumb on a mouse pointer — under the 44px
  rule, but deliberate: `app.css` gates the 44px thumb to coarse pointers so
  Send's 512-cell desktop grid stays usable. Revisit as one decision, not per
  screen.
- The `--solid` pill tints are the dark-theme ramp in both themes (inherited
  from the source screens). The app is currently hard-set to
  `data-theme="dark"`, so nothing is broken today — but check contrast on the
  solid pills before anyone enables the light theme.
