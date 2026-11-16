# Benny512 — next-work brief for future agents

**Written:** 2026-11-16 16:37:54 -0500, from a review session on Dom's desktop (`domcdtdesktop`).
**Read first:** `CLAUDE.md`, then `docs/HANDOFF.md`. This file does not replace
either; it lists the work that is too big for one session, with what a new
agent needs to start each piece cold.

## 1. Where things stand

Reviewed tree: `main` at `e45a0b0` (= `origin/main` as of the desktop's last
fetch, 2026-10-22), working tree clean, tags `v0.2.1`–`v0.2.4`.

| Check (Linux sandbox, Go 1.26, Node 22) | `main` e45a0b0 | `main` + K1–K4 |
|---|---|---|
| gofmt, go vet, go build | pass | pass |
| `go test -count=1 ./...` (16 packages) | pass | pass |
| `go test -race -count=1 ./...` | pass | not run |
| `node --check` every script under `static/` | pass | pass |
| Windows cross-build | pass | not run |
| Browser smoke, `--demo`, 8 screens × 1440/1024/390 | not run | no console errors, no failed requests, no page-level horizontal scroll |

**K1–K4 were never applied on the desktop.** The four patches sit in
`dist/_incoming/k1..k4/p1.patch.b64` with their `b512-k*.bat` appliers at the
repo root, and the desktop has no `feat/k*` branch. On 2026-10-22
`origin` had only `main` and `codex/console-workspace-design`. In this
session all four applied cleanly, in order, on `e45a0b0`. Whether they were
pushed from the laptop after 2026-10-22 was not checked (the session had no
GitHub access): **run `git fetch` and look before doing anything else.**

Not verified by anyone yet, on any tree: a Windows-native test run, and every
hardware item in section 4.

### Session mechanics that cost time (desktop)

- No shell on the desktop from a cloud session: only list / stage / commit of
  files. Git cannot be run for Dom. Work is delivered the established way:
  `dist/_incoming/<chunk>/p1.patch.b64` + a root `b512-<chunk>.bat` that makes
  the branch and runs `git am`; Dom pushes from GitHub Desktop. Both paths are
  gitignored. Never write tracked files into the working tree directly: the
  appliers stop on uncommitted changes.
- Fast way to get the whole tree without staging ~730 files: stage
  `.git/index`, `.git/objects/pack/*`, `HEAD`, `packed-refs`, `refs/**`;
  `git checkout-index -a` restores every packed blob; `git cat-file
  --batch-check` on the index lists the blobs that are still loose (175 on
  2026-11-16, 139 of them design-package files) and only those need staging by path.
  Comparing index size/mtime with `device_list_dir` proves the tree is clean.
- 23 working files on the desktop have CRLF endings (the ones checked out
  most recently). A Windows-native `gofmt -l .` will list them. CI is on
  Linux and unaffected. If native gates are ever wanted, decide on a
  `.gitattributes` first (not done: needs Dom's Git settings confirmed).

## 2. Owner actions that unblock everything else

1. `git fetch`; if K1–K4 are not on GitHub, run `b512-k1-app-chrome.bat`,
   `-k2-`, `-k3-`, `-k4-` in order, push each branch, merge K1→K4.
2. Tag `v0.3.0` on `main`; CI builds the exe.
3. Bench-test v0.3.0 on the laptop (primary screen) before K5 starts: header
   and Show menu, dock/float/resize, Tests tab, Compact.
4. Then run `b512-l1-review-brief.bat` (this brief + one dead-code removal).

## 3. Projects, in suggested order

### P1. K5 — engine intensity report and live tiles (plan row K5)

*Goal:* a read-only report of the output engine's **composed** intensity per
fixture and per cell, and tiles that show it: intensity bar, SET/TEST tags,
programmer-colour swatch with name + outline, "running sources annotate grid
tiles" on the Tests tab, and the Plan + Fit hint.

*Constraints already decided:* output behaviour must not change; never
substitute programmer values or an unknown zero for an engine value (CLAUDE.md
"a plausible zero is not absence"); the effective source stays separate from
the programmer colour; Dark preview brightness 0.12 made the W1 swatch
near-black (W1 review note).

*Start at:* `internal/session/dmxout.go` (sources, virtual claims,
Multiplier, ownership hook added in I2g), `internal/web/output.go`,
`static/js/console.js` tile rendering, `docs/design/console-workspace/`
component specs, plan rows J1c/J1d for master × part rules.

*Split:* (a) engine report + API + tests fed from real engine frames;
(b) tile bars and tags; (c) swatch; (d) Tests-tab source annotation.

*Found this session, belongs here:* with Fit on, the **Unplaced** layer still
scrolls sideways inside the grid box below 1440 px (canvas 774 px wide in a
530 px box at 1024, 786 px at 1280; fits at 1440). On the demo show the tile
for fixture 103 overflows its 15 px top row by 4 px at 1024 and 1280, clipping
"no position". Reproduce on `--demo`, Console and Tests tabs.

*Done when:* a test reads the report from the engine's own output for a
master × part fixture, a virtual dimmer and a test-driven channel, and fails
against the unbuilt code; render proof at 1024/1440/1920 on demo and a
temporary copy of Dom's show.

### P2. K6–K8 — W1b screens: Patch, Settings, Devices/Nodes, Analyzer/Rig Walk

*Goal:* build `docs/design/ui-organization/` into production, one screen per
branch. Binding owner answers are in that folder's `decisions.md` and
`owner-questions.md`.

*Measured problems the design answers (1440×900, Dom's show):* Patch is
9,256 px tall with 93 px rows, 370 row buttons and a non-sticky header;
Settings is 4,698 px with ~690 words of help; Devices opens on a destructive
disclosure.

*Decided:* Comfortable 48 px rows (Compact 40 optional); shared right
inspector at 440 px (320 px…60 %) on Patch, Devices and Nodes, opened on row
inspection; Settings = index + one scrolling staged form with an
always-reachable Apply bar; device diagnostics closed once devices exist;
fixture controls only on the Console; no global shortcuts; below 1024 px
keep today's layouts.

*Risks:* Apply-to-confirm must survive every move (anything that changes the
wire stages first); Nodes configuration still stages until Apply; the K3
dock code (`static/js/console-windows.js`) is Console-specific — decide
reuse vs. a second inspector before K6, and record it in `docs/decisions/`.

*Suggested order:* K6 Patch (largest win), K7 Settings, K8 Devices/Nodes,
then Analyzer/Rig Walk.

### P3. Close the ACK_TIMER loop with QUEUED_MESSAGE

`internal/session/rdmqueued.go`, the `TODO(bench)` near the end, states the
defect: after an ACK_TIMER Benny512 re-issues the request under a new
transaction number instead of collecting the deferred answer with
GET QUEUED_MESSAGE, so each chain strands one answer in a proxy's queue
(RDM-LOG2: two ACK_TIMERs 313 ms apart, then PROXY_BUFFER_FULL, repeatedly).
The fix needs routing of a drained message to whichever command it answers,
and leaving the current command waiting when it answers something else.
Govern it by the ACK_TIMER and QUEUED_MESSAGE sections of E1.20 (primary
text beside the repo; cite section numbers from it, not from memory), write a
decision record, and prove it against a responder that counts transactions.
Needs a wireless (CRMX) bench pass to confirm. Engine-level change: its own
branch, nothing else in it.

### P4. Rewrite `docs/HANDOFF.md` as a current-state file

It is over 90 KB (about 760 lines once K4 lands) and contradicts itself: it still says agent
shell access is unavailable "since September 8", that source changes are
"uncommitted on purpose", and that `internal/sacn/` is an empty leftover,
while sACN output is back and hardware-verified and the repo is on GitHub
with CI. One paragraph is 12,529 characters. Target: under ~300 lines —
what the app is, state of `main` and open branches, open owner questions,
open items, bench backlog, pointers. Move everything dated before October
into `docs/notes/` (it is already there in substance; verify before
deleting each section). Dom reviews the diff; docs-only branch.

### P5. Two manuals

Dom wants an owner's reference for himself and a short guide for other techs
and LDs. Unverified features are flagged "needs work". Real-rig screenshots
wait until he has nodes running; use `--demo` captures until then and mark
them. Write after v0.3.0 is bench-tested so the screens are stable; the K6–K8
screens will change again, so start with Console, Tests, Rig Walk.

### P6. Open engineering items (from HANDOFF "Open items")

- 4: per-port RDM on/off and whether Art-Net 4 offers a per-port
  input-enable route (curiosity-driven; needs a Wireshark capture of the
  EN4's own tool).
- 5: scope readback. 8: server-side min/max normalisation. 10: sticky bar
  (check whether K2's toolbar already settles it).
- 6: `patch.Entry` has no provenance, so automatic import migration is unsafe.
- 11: E1.37-1 PRESET_INFO / POWER_ON_SELF_TEST, some E1.37-2 PIDs, DNS set,
  ENUM_LABEL incomplete; `internal/rdm/dimmer.go` layouts are all
  `TODO(hardware)`.
- 12: `POST /api/patch/reconcile/fix-all` is still routed
  (`internal/web/patch.go`, `handlePatchReconcileFixAll`) though its button
  was removed. **Owner decision:** bring it back or remove the endpoint.
- Profile storage size (a BMFL profile is ~161 KB) — open owner question
  from C1.
- Unused test helpers flagged by staticcheck: `c3Dmx`
  (`internal/web/output_c3_test.go`) and `countArtDmx`
  (`internal/web/sacn_isolation_test.go`). Left in place: first check whether
  an assertion that should use them is missing (an Art-Net datagram count in
  the sACN isolation tests would be the obvious one).

## 4. Bench backlog (hardware only; nothing here can be closed in a sandbox)

1. v0.3.0 UI on the laptop with the real show.
2. RDM-LOG36 fix: re-test against the faulty ERA 800 on a real node.
3. E1.37-2 network-configuration controls against a real gateway — this PID
   rewrites a gateway's network config.
4. Universe rework: Nodes tab vs. the EN4 faceplate, starting universe 0 and
   non-zero.
5. Pan/tilt invert toggles on a fixture that advertises them (JDC1:
   TILT_INVERT only).
6. EN4 per-port commands, multi-cell phase spacing, wireless mode-label
   traffic, wireless SET settling.
7. Dom's show: re-import the GDTFs so the J1c geometry tree is present.

## 5. Housekeeping (needs a shell or Dom; small)

- After K1–K4 and L1 are merged: `dist/_incoming/` holds 72 spent files
  (3.4 MB) and the root has six `b512-*.bat` appliers. All gitignored; delete
  when convenient (deleting needs Dom's consent prompt or a shell).
- `go.mod` says `go 1.26`; CI installs `1.27.x`. Compatible; align only if a
  1.27 feature is wanted.
- Local branch `codex/console-workspace-design` (b25494f) holds the W1
  design package, whose files are on `main`. Confirm with
  `git branch --merged main` before deleting it locally and on GitHub.
