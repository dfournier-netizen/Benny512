# Benny512 — Session Handoff Brief (current)

**Last updated:** 2026-09-22 13:01:39 -0400

## Current delta — Rig Check fade-time transitions and ERA LOG32–34 audit

The previously deferred Rig Check fade-time work is now implemented. The Rig Check UI defaults to a **1 second** fade and offers Snap (0 seconds) plus 250 ms through 30 seconds. The setting applies to manual/test level transitions, entry and scope changes, and leaves waveform rate unchanged. Continuous values interpolate as complete 16-bit values; discrete shutter/wheel/control values remain discrete. Stop and the watchdog still blackout immediately. The setting is included in saved Rig Check presets. Focused fade tests and the affected Go packages pass; JavaScript syntax checks pass.

The ERA investigation now includes LOG32, LOG33 and LOG34. LOG33 showed exactly seven healthy fixtures while the suspect was power cycling. LOG34 showed the anomaly returning after reset: 11 then 10 advertised identities, with changing extras (`001159BC`, `001159FC`, `0011597E`, `001159FE`) while the same seven healthy fixtures answered 28/28. The consistent `001159FE` can be generated as a valid E1.20 discovery collision pattern from encoded responses, but this is a hypothesis rather than captured serial evidence. See `docs/evidence/analysis/ERA-LOG32-investigation.md`, `docs/evidence/analysis/ERA-LOG33-investigation.md`, `docs/evidence/analysis/ERA-LOG34-investigation.md`, and `docs/evidence/analysis/ERA-discovery-mechanism-analysis.md`.

## Current delta — September 17: port advertisement/reply reporting completed

The ERA-related software follow-up is implemented: Devices → **Gateway advertisements and RDM replies** reports each cached gateway port's advertised UIDs, answered UIDs, reads pending, not-read entries, and entries with no response after automatic attempts. See the final entry for implementation, evidence, release and remaining limitations. This supersedes the older statements below that this reporting is not built.

Latest executable: **`Benny512/dist/benny512_091726_0535AM.exe`**. It is a build of the current uncommitted worktree at HEAD `981ed63`, including the pre-existing params/probe-cache changes; those files were not changed by this reporting task. No commit was made. Affected-package tests, browser behavior tests, build and vet pass. Full-suite validation has one reproducible adapter-dependent sACN test failure; do not describe this build as passing every test. Hard-reload the browser after switching executables.

ERA root cause remains unproven. Read `docs/evidence/analysis/ERA-investigation-2026-09-17.md` and the September 17 correction entry before relying on the older categorical hardware conclusions.

## What Benny512 is

Benny512 is Dom Fournier's Windows-hosted, LAN-accessible web instrument for entertainment-lighting networks. A single `benny512.exe` serves a browser UI and provides Art-Net/ArtRDM, RDM discovery/control, packet analysis, DMX output, patch/reconcile, attribute-level Rig Check, browser-side MVR/GDTF import, and a persistent Fixture Library. It now also has saved show workspaces, rig baselines, patch-based offline rehearsal, and a searchable/exportable Fixture Library. **Art-Net only:** the sACN diagnostic monitor was removed at Dom’s request; no sACN feature or mapping decision remains open. RDMnet remains unimplemented. It has no external runtime dependencies and ships as a timestamped self-contained exe.

The operator is a lighting professional, not a programmer. These are working instruments: retain information density, high contrast, dyslexia-friendly type, keyboard access, and the explicit **Apply-to-confirm** contract. Identify, Send/Rig Check faders, and Rig Check test toggles are the signed-off exceptions.

## Current repository state

- Repository: `RDM App\\Benny512`; latest committed HEAD is **`1752b7c`**, “Ballyhoo gets its own rate bounds, 0.005-2.5 Hz.” The worktree is clean. Commits since `6dad245`: `46b9cb9` fixture-type scope, `a15c3b5`/`bfbd2c6` the sACN encoder and its wire-format correction, `f391de6` gitignore, `a81bdcb`/`cf52265` the sACN transport, `27e7576` sACN output in Rig Check, `b586361` its browser half, `4dc0f40` the Function check tab, `b1233b2` durable settings, `1752b7c` Ballyhoo bounds.
- The current build from HEAD is **`dist\\benny512_091626_1115AM.exe`** (14,286,848 bytes), SHA-256 `9D8DDB52F4A6378EE43667EA2B69ECE2C95424165EFC1149146BCC7C42353B0E`. No runtime show/library files were changed by development QA. Older releases were **pruned to the newest four on 2026-09-16 13:31:49 -0400**, at Dom's explicit instruction, per the standing max-4 FIFO convention; nine executables were permanently deleted. Only `.exe` files were removed — every runtime JSON was left untouched and verified afterwards.
- All source changes from the prior session and the other agent are committed. The current tree passes `go test -buildvcs=false -count=1 ./...` and `go vet -buildvcs=false ./...`. The other agent's commit reports three consecutive Linux-sandbox race passes; Windows-native race testing remains unavailable without MinGW GCC. Hardware/bench verification remains outstanding for universe notation and gateway behavior.
- The Go run exposed and fixed a Windows-only test cleanup defect: `TestDemoLogArmedBeforeStartCapturesSeedTraffic` left its temporary RDM log open. The test now closes its server exactly as production shutdown does.
- **Race testing: RUN AND PASSING**, September 8. `go test -race -buildvcs=false -count=1 ./...` was run **three consecutive times, all clean**, alongside `go build`, `go vet`, and a full `-count=1` pass at **995 tests**. The Windows MinGW blocker was not solved; it was routed around — the race suite runs in the **Linux cloud sandbox**, on the identical tree. Read this as a genuine spot check of the race detector's findings, NOT as the race gate having moved to a permanent home. A Windows-native race run still has value if the MinGW prerequisite is ever met, because the Windows scheduler and the Windows-only code paths are not what the sandbox exercised.

## Local installation / drive structure

The folder containing the running executable is Benny512’s runtime data root. For the current local build that is:

```text
C:\Users\CDT_LD\Claude\Projects\RDM App\Benny512\dist\
  benny512_091626_1115AM.exe          # launch this build
  benny512-settings.json              # Settings screen, durable since b1233b2
  benny512-settings.json.bak          # preceding successful settings save
  benny512-sacn.json                  # sACN CID, start universe, priority, unicast
  benny512-sacn.json.bak              # preceding successful sACN save
  benny512-patch.json                 # default/legacy saved show
  benny512-patch.json.bak             # preceding successful save, created on later edits
  benny512-patch.json.patches\        # extra named saved shows (created on first New show)
    p-<generated-id>.json
    p-<generated-id>.json.bak         # this named show's preceding save
  benny512-patch.json.patches.active  # ID of the show to reopen at launch
  benny512-library.json               # Fixture Library shared across all saved shows
  benny512-rigwalk.json               # Rig Walk resume state
```

Keep those files beside the exe when moving or backing up an installation. New saved shows never overwrite `benny512-patch.json`. Show tools → Reset this show clears only the active show and retains a one-save recovery copy; Settings → Full Reset explicitly erases ALL saved shows, their recovery copies, and Rig Walk state, but retains `benny512-library.json`. Close Benny512 before manually restoring exported JSON into its runtime paths. Back up the complete data directory, not just the executable.

## September 8: E1.37-2 wire formats corrected against the primary text

Committed as **`55264ec`**. Seven files: `internal/params/ipconfig.go`, `ipconfig_test.go`, the new `ipconfig_appendixb_test.go`, `internal/web/network.go`, `network_test.go`, `internal/capture/rdmdetail.go`, `rdmdetail_test.go`.

`ipconfig.go` had carried a doc comment admitting its layouts were “UNVERIFIED against ANSI/ESTA E1.37-2 primary text or a real device.” Checked against ANSI E1.37-2:2015 (R2021) including its Appendix B worked example, that reading was wrong in three places:

- **`LIST_INTERFACES` §4.1** is a packed list of **48-bit** descriptors (32-bit Interface Identifier + 16-bit hardware type), not bare 4-byte IDs. Its guard was `len(data)%4 != 0`, which **cannot** catch the error: Appendix B's real two-interface response is 12 bytes, and 12 is a multiple of 4 as well as of 6. It decoded three phantom interfaces (1, 65536, 131073) from two real ones, so every per-interface GET that followed carried a fabricated identifier and earned `NR_DATA_OUT_OF_RANGE`. That surfaced as “this gateway doesn't do E1.37-2 properly,” which is the wrong conclusion and the reason it went unchased.
- **`IPV4_CURRENT_ADDRESS` §4.6** is PDL 10: interface ID, address, netmask as a **one-byte prefix length**, DHCP status. The old code required ≥ 12 bytes and read a 4-byte dotted mask, so a conforming reply was rejected outright and the DHCP status discarded.
- **`IPV4_STATIC_ADDRESS` §4.7** is PDL 9 in both directions. The old code built a 12-byte SET with a dotted mask — a malformed write to a gateway's network configuration.

The netmask is a prefix length everywhere now, converted to dotted form only at a presentation boundary. `PrefixLenFromMask` **refuses** a non-contiguous mask rather than bit-counting one the operator did not ask for. `DHCPStatusKnown` separates “this reply had no such field” from `DHCP_STATUS_UNKNOWN`, which is a real answer meaning the device cannot tell.

**Both fake responders were enforcing the defect.** They were written from the same misreading as the code they exercised, so the suite passed against a build that could not talk to a conforming device — the **eleventh** instance on this project of a test proving only that one side agrees with itself. `ipconfig_appendixb_test.go` therefore asserts against the standard's own printed bytes in both directions, and was confirmed to fail against the unfixed code: 3 interfaces decoded instead of 2, the 9-byte reply rejected on length, and `000000010a000020ff000000` sent where Appendix B says `000000010a00002008`. Two capture tests changed with it because their fixtures *were* the malformed payloads.

Verified: build, vet, three consecutive `-race` runs clean; **995 passing**, up from a 991 baseline.

### The `b512-commit-e137.bat` helper — what it was, and why

For most of September 8 the commit was blocked by a **stale `.git\\index.lock`**, dated Sept 7 18:54 — roughly eighteen hours old, left by a git process that crashed, not one that was running (`git status` read fine throughout, so nothing was corrupt). Deleting a file inside a connected folder needs Dom's approval, and the machine went offline before that could be asked; when it came back, the agent's shell into the machine could no longer mount the folder (see below).

So the work was handed over as two files dropped into `RDM App\\`:

- **`b512-commit-e137.bat`** — removed the stale lock, `git add`ed **only the seven E1.37 paths** (deliberately *not* `git add -A`), committed with the message file, then printed the log and the remaining dirty count.
- **`b512-e137-commitmsg.txt`** — the commit message, including the proof-of-failure bytes.

Dom ran the `.bat`; the commit landed as `55264ec`. **Both files are single-use and should be deleted.** The narrow `git add` was the whole point: at the time the tree also held ~68 uncommitted files from a concurrent agent session, and `git add -A` would have swept a feature branch's worth of unrelated work into a commit whose message is about E1.37.

**Known environment limitation, September 8:** the agent's Linux shell into Dom's machine failed to mount the connected folder (`sandbox-helper: no Plan9 drive shares mounted under /mnt/.virtiofs-root/shared`), and did not recover across reconnects. The file tools (list/stage/commit) kept working throughout — that is how the fix and the helper script reached the disk, and how the commit was later confirmed by reading `.git/logs/HEAD` directly. Only the **shell** was unavailable, which is what blocks running `git`, `go build` or `go test` on the machine itself. If this recurs: file transfer still works, so hand shell-dependent steps to Dom as a script rather than declaring the machine unreachable.

**sACN:** its removal from the tree was **deliberate** — confirmed by Dom on September 8, who was looking to prune it as a feature regardless. Not an accidental deletion. Worth one check that it went out cleanly rather than leaving orphaned references.

## September 8: completed Nodes, Art-Net-only and Fixture Library batch

- Nodes lists physical parent nodes only on the left. The floating editor has independent scrolling, **Node settings** and **Port settings** tabs, and one port selector inside the editor. Shared-bind names and physical-node IP drafts are node-wide; single-port-bind names stay port-specific. Switching ports preserves drafts. IP changes still require Apply → Arm → Confirm.
- Address saves program only the selected wire slot. Hidden sibling drafts are excluded; changing a shared Net/Sub-Net block is refused if another reported port/direction would move. Sparse wire indexes are retained. A disappeared port closes its stale editor, and delayed status replies stay with their original target.
- ArtInput input-enable writes are intentionally disabled for binds advertising several ports: the command writes every enable bit and there is no reliable readback to preserve siblings. Direction, RDM and merge commands remain individually selectable. This is a safety boundary, not hardware confirmation.
- sACN receiver, package, routes, controls and polling are removed. The supplied E1.31 PDF and older executables remain. ArtAddress's required AcnPriority “no change” field remains so Art-Net configuration cannot inadvertently reprogram a dual-protocol gateway.
- **Fixture library** in the persistent top strip opens a searchable, show-independent library. Import a GDTF or library JSON, save profiles from the active patch, add a profile-backed entry, or explicitly re-profile selected existing entries. Adding does not commit a physical fixture; use Reconcile. Re-profiling preserves addresses and requires checking collisions.
- Original GDTF archives imported through this library are saved inside `benny512-library.json`, deduplicated by SHA-256, included in library JSON exports, and individually downloadable. Harvesting an existing patch saves its profile data, not an original GDTF archive it no longer contains. Current UI limits: 8 MB per GDTF, 120 MB per library import.
- Verification is **operator-confirmed hardware testing per mode**, separate from GDTF provenance. The current assumption was presented to Dom; no contrary answer received. A stamp records time, note and mode-payload hash. Changed footprint/channel data clears an old stamp; identical imports preserve it. Stale reviewed payloads cannot be marked verified. Imported stamps are operator claims/change detection, not manufacturer certification or cryptographic signatures.
- Library writes use synced temporary-file replacement. Import/verification/checked upsert/delete roll back memory on save failure; damaged existing files are not overwritten. A multi-entry “save profiles from patch” reports if a later failure follows earlier successfully saved profiles. Back up/export the library; unlike shows, it does not currently have a preceding-save `.bak`.
- RDM mode names now load automatically: current name in Info, and all mode-selector labels in Parameters, current first. The shared Devices/Rig Walk renderer fetches sequentially, caches successful names, coalesces concurrent requests, backs off failed lookups, and stops remaining lookups when changing fixtures. No “Show all personality names” button is needed. Missing names explicitly read “Mode name unavailable”; no library-derived guess is presented as a device response.

### Verification for the latest batch

Uncached `go test -buildvcs=false -count=1 ./...` passes **all 14 packages** with Node on PATH. Vet, formatting, all browser-script syntax checks and diff whitespace checks pass. Added tests cover selected-port addressing and vanished ports, node-wide IP drafts, library persistence/export/import/source checksums, mode-verification invalidation/stale review/save failures, and automatic mode-name ordering/single-flight/cancellation/backoff.

Disposable demo browser QA confirmed parent-only Nodes, selected-port switching, tab separation, patch-profile harvesting, profile-backed entry creation, real JDC-1 GDTF import (six modes), source checksum, and library survival after process restart. The Ayrton demo's mode selector automatically showed “24ch Extended”, “16ch Standard”, and “8ch Basic”; a non-reporting fixture showed the explicit unavailable state. No physical lighting commands or real show/library mutations were used for QA. Race testing is still blocked by missing compatible MinGW GCC; EN4 and wireless behavior still need the bench.

## September 7 evening: implemented workflow batch (earlier batch)

- Durable patch saves use a synced sibling temporary file and checked rename. The preceding readable save is retained as `.bak`. Disk/callback errors do not publish an edited in-memory patch. A damaged selected named show no longer silently opens the default rig; explicit recovery targets the selected show's backup.
- Show switching/reset/recovery clears Rig Check engine selections and semantic scope. Browser mutations carry a server-generation token; stale tabs receive HTTP 409. Structural patch and Reconcile changes invalidate old test targets. Stop bypasses the show-operation lock. Tokens are optional for legacy API clients; this is stale-edit protection, not authentication.
- Phase slots now resolve in this order: saved entry override (1–512; 0 means auto), GDTF geometry estimate, live RDM sub-device count, labelled single-slot fallback. Geometry metadata is preserved on newly parsed/imported profiles; old profiles may require re-profile or an override. No synthetic patch entries, footprint changes, or per-cell output mappings are created.
- A persistent strip shows show name, NIC, output state, Show tools and Stop all output. Status polling does not refresh the Function-check watchdog. Demo/rehearsal output is labelled simulated.
- Show tools contains a cache-based actionable issues list, fixture selection/groups, saved Function-check presets, baselines with comparison/TXT export, rehearsal, and active-show recovery. Groups/presets/baselines live inside the show JSON, so switches isolate them. Limits: 100 groups, 100 presets, 10 baselines; individual items can be deleted with confirmation and recovered from the preceding save. Presets validate the exact stored fixture IDs and always load with output stopped.
- Selections flow from Patch/Devices into show tools, then into Function check; issue actions open the matching entry/Reconcile/inspector. This is not a wholesale unification of every legacy screen's independent picker.
- Baselines snapshot intended patch configuration, cached fixture configuration/read coverage/reachability, and open issues. Profile maps are hashed to avoid duplicating megabytes in each snapshot. Reports explicitly say CACHE-ONLY: they are not fresh discovery, a node-configuration audit, or physical acceptance certification.
- Offline rehearsal copies 1–1024 active patch entries into a separate in-memory server with fake lighting transport. It preserves uncommitted entries, supports missing-fixture/wrong-address/delayed-response scenarios, stops parent output before launch, and replaces the previous rehearsal. No show/library persistence paths are installed on the child. The delayed scenario uses the existing proxy model: initial ACK_TIMER then slower responses; it does not reproduce real RF saturation. The separate LAN-accessible UI uses an ephemeral port and ends with the parent process.
- That earlier build included receive-only sACN diagnostics. **Superseded September 8: removed at Dom’s request; Benny512 is Art-Net-only.**

### Historical verification for the September 7 batch (before sACN removal)

`go test -buildvcs=false -count=1 ./...` and `go vet -buildvcs=false ./...` pass across all 15 packages. Go formatting, all browser-script syntax checks, and `git diff --check` pass. A five-second sACN decoder fuzz run passed 4,035,528 inputs. Regression coverage includes save failures/recovery, damaged active-show selection, stale browser edits, preset persistence/deleted IDs, baseline drift, GDTF phase identities (including a 16-instance RGBW array), rehearsal isolation, and sACN literal data/sync/discovery, sequencing, termination and stale alternate-start-code handling.

In-app browser QA used a disposable `--demo` executable under `%TEMP%\\benny512-workflow-qa`, never the installation's real show data. Verified group save, baseline/no-change report, exact two-fixture scope, preset save/load with output stopped, fault-injected rehearsal launch, and a 390px-wide dialog without page overflow. Native `prompt()` failed in this browser; new workspace prompts/confirmations now use in-app dialogs. A later attempt to exercise the legacy Function-check Start confirmation hit a browser-control/CDP timeout, so the final global-Stop live-toggle browser check is NOT claimed passed. Existing backend output-stop tests pass. Race testing remains blocked by missing MinGW GCC; no hardware/sACN multicast bench test was performed.

See `Benny512/WORKFLOWS.md` for the concise operator path and implementation boundaries.

## What the latest hardware evidence means

- **Paladin Cube discovery is resolved as a hardware issue, not a Benny512 defect**, per Dom’s confirmation. Do not reopen it as an active app bug.
- Separately, LOG25 exposed a discovery robustness defect that is now fixed in `9b9e8a4`: an empty ToD 225 ms after `AtcFlush` is a node that has just cleared its cache and has not finished discovery, not proof the port is empty. A plain cached `ArtTodRequest` may still complete immediately with an empty answer.
- LOG24 proved Reconcile commit with eight GLP JDC-1s. It also showed wasted unsupported-PID probes; these are now gated and represented honestly as `not_fitted` where appropriate. A JDC-1 tilts but does not pan.
- CRMX/MoonLite2 proxy buffering remains a shared-resource problem. The promising untried experiment is draining `QUEUED_MESSAGE` at proxy UID `4C55:EFAD7AF2`. Wireless SET confirmation may require a settle period longer than Benny512’s roughly five-second window.

## Recent product state

- GDTF/MVR: vendor GDTFs parse correctly. Vectorworks placeholders are the source of the historical JDC-1/Paladin footprint mismatch; repair an imported patch through Fixture Library re-profile, not MVR re-import. MVR’s formerly one-universe-high import is fixed; its manual selected-entry migration remains the safe option because entries carry no source provenance.
- Universes: canonical storage is the 0-based 15-bit Art-Net Port-Address. `UI.formatUniverse` / `UI.parseUniverse` are the only display conversions. Nodes now displays the full Port-Address, not just the low nibble; Analyzer no longer labels packets without a universe as “Universe 1.”
- Rig Check: selection and output-enabled are independent; tests stack in canonical order, work from GDTF defaults, and return a complete status snapshot after each mutation. Reconcile has separate, persistent sorted panes; commit reads/records, while push is the explicit fixture write.
- Saved shows: the original `benny512-patch.json` is the default show; extra named rigs are separate files in its sibling catalog directory and the last active rig is restored at startup. The Patch selector can create/load them without overwriting existing shows. A physical fixture may be committed in more than one saved show; commitment is per patch. Switching/creating a show stops and blackouts Rig Check first.
- Sub-fixture phase spacing uses the override/GDTF/RDM precedence above and advances the next root fixture by that many slots (e.g. a 16-cell Rayzor consumes 16). It does not alter patch entry counts, footprint, reconciliation, or create per-cell DMX mappings.
- Rig Check scope status now echoes the accepted all/universe/position/selection expression. A rejected scope change restores the browser picker to server truth; scope universe values remain canonical until the UI formats them.
- MVR fixture `Position` UUIDs now resolve through the scene’s named Position collection. A named Layer is retained only as a compatibility fallback.
- Analyzer RDM now reports ACK_TIMER collection hits/probes and fallback reissues beside the RDM buffer count, backed by `GET /api/diagnostics/rdm`. Zero counters remain visible.
- Screen system: shared kit is in `internal/web/static/css/benny512-components.css`; read `internal/web/static/css/DESIGN.md` before restyling. Touch controls meet the 44px target on coarse pointers without sacrificing desktop density.

## Per-port configuration work this session

The Art-Net 4 primary PDF is now available as `art-net 4.pdf`; `ANSI E1.31-2025.pdf` is also present. Art-Net 4 confirms:

- ArtInput is a 20-byte packet with a big-endian `NumPorts` at offsets 14–15 and input-disable bit 0 at offsets 16–19.
- ArtAddress `AcDirectionTx0–3` is `0x20–0x23`; `AcDirectionRx0–3` is `0x30–0x33`. Setting input also flushes that port’s subscriber list.
- ArtAddress `AcRdmEnable0–3` is `0xC0–0xC3`; `AcRdmDisable0–3` is `0xD0–0xD3`.

Uncommitted changes add the constants, API command mapping, byte-exact test cases, and Nodes-screen controls. Direction and RDM actions each require a native confirmation and run through the existing unicast ArtAddress / follow-up-ArtPollReply confirmation path. A fresh PollReply confirms that the node replied, not that it applied the command—bench verification on the EN4 is still mandatory. Ports 1–3 are spec-deprecated but have defined wire values and are deliberately exposed for actual four-port hardware.

## Devices workflow simplification

Devices is now a compact picker: target port and Discover sit at the top, destructive discovery-memory clearing is collapsed, and the verbose four-step narration is gone. Selecting **Inspect** opens a floating right-side inspector on desktop instead of a detail section at the bottom of the page. On small screens it becomes a bottom sheet with Previous/Next controls, so the sheet never prevents switching devices. The inspector has an explicit Close control. The UI retains only the brief context needed to safely discover, filter, and clear memory; safety-critical confirmation text remains.

## Operating conventions that prevent repeat defects

- Never use `omitempty` on a numeric/boolean JSON field whose zero is meaningful. Initialize JSON slices with `make([]T, 0)`. A plausible zero is not absence.
- Server-generated user-facing text must not embed universe numbers; display formatting belongs at the presentation boundary. TXT export is the exception.
- `SUPPORTED_PARAMETERS` gates speculative RDM reads. Missing support information means unknown/open, not unsupported. E1.20-required PIDs are never gated.
- Tests must fail on the unfixed behavior and should use literal browser JS/wire JSON at seams. Do not build request and assertion from the same struct or stub out the screen being tested.
- Run the Go gates with `-buildvcs=false`: `gofmt -l .`, `go vet`, `go build`, `go test -count=1 ./...`; run race testing separately when a compatible compiler is installed. Render-prove JS UI changes using the available browser skill; new workspace dialogs deliberately do not depend on native `prompt()` support.
- Capture the local ET timestamp immediately before writing notes; append a detailed Notes entry and rewrite this handoff at every completed phase.

## Next work, in order

1. **DONE, committed as `55264ec`** — see “September 8: E1.37-2 wire formats” below. The network-configuration controls are no longer the known-malformed path they were; the warning to avoid relying on them is withdrawn, subject to bench confirmation against a real EN4.
2. Bench-test EN4 per-port commands, actual multi-cell phase spacing, automatic mode-label traffic on wireless fixtures, and wireless SET settling/proxy queue draining. Paladin Cube's hardware failure remains resolved, not an app bug.
3. Gather UI feedback on the new show tools/rehearsal and legacy Start confirmation in the operator's normal browser. **Claude owns race testing**, including the compatible-compiler prerequisite; await and record the result rather than duplicating that work.
4. Source changes remain uncommitted on purpose; commit only when Dom asks. The new timestamped executable is already built on the PC.

## September 8 (later): probe cache and ACK_TIMER diagnostics — proof, not new code

Both of these were list items expecting implementation work. Both turned out to be **already implemented in the other session's uncommitted branch, and untested**. In each case the missing piece was proof, not code, so the deliverable is a test that fails against the un-wired state — not a rewrite of something that already worked.

**This is now a pattern worth naming.** That branch has real, working machinery with no test coverage behind it. Machinery that is built but never consulted is indistinguishable from machinery that works, from every angle except a packet capture. Anything else picked off the list should be *checked before it is built*.

### Per-device probe cache (list item 2) — `internal/params/probecache_test.go`

`supportedSet`, `unsupportedPIDs`, `ensureAdvertised`, `rememberUnsupported` and the single-flight were all present and correct. Nothing asserted that a gated PID actually **stays off the wire** — `ErrPIDNotAdvertised` appeared in one test file, incidentally. The new tests count transactions **at the responder**, which is the only place “did we send it?” has an honest answer:

- **RDM-LOG24 replayed.** A fixture advertising TILT_INVERT and not PAN_INVERT, asked nine times (LOG24's real count). PAN_INVERT must reach it **zero** times — and TILT_INVERT must still go through, because gating the whole pan/tilt family is the cheap way to pass the first half and would be a silent feature loss with no NACK to reveal it.
- **Fail-open.** A device that NACKs SUPPORTED_PARAMETERS itself must still get its first probe, or a non-conforming fixture becomes invisible precisely *because* it is non-conforming. Asks 2–9 must not repeat.
- **Rescan.** `ForgetDevice` reopens the gate, so a re-flashed fixture is not broken for the life of the process.
- **Required PIDs never gated**, asserted at the wire rather than against the switch statement — a gated `DMX_START_ADDRESS` fails *silently*, because the request is never sent.

Confirmed failing against a cache-disabled build; the fail-open test prints the original bug in its own words: `PAN_INVERT reached the fixture 9 times across 9 asks`.

**Checked and deliberately NOT changed:** `getRaw` gates speculative PIDs and `setRaw` does not. That asymmetry is **correct**. The bulk apply path already refuses per-field on as-found `not_fitted` (`reconcilecommit.go`), and it does so with a `ReadFromUID` guard so a stale not-fitted reading from a substituted-out fixture cannot veto a write to the fixture now in the rig — context `setRaw` does not have. The traffic shapes differ too: GETs go out automatically in bulk on device-detail load, which is what made LOG24 ugly, while a SET is one deliberate write, and for a single user-initiated SET the device's own NACK is more informative than a local refusal. **Do not “fix” this asymmetry.**

### ACK_TIMER counters on Diagnostics (list item 3) — `internal/web/diagnostics_test.go`, `analyzer.js`

Seven counters existed, were genuinely incremented, were routed at `GET /api/diagnostics/rdm`, and were tested at the session layer. Two gaps:

- **The failure signal was on the wire and not on the screen.** The status line showed collected/attempted and reissues, then discarded the other four counters it had just fetched. A reissue alone is ambiguous — it can be ordinary policy. A reissue *following a collect timeout* is the expensive failure path, so “reissued 12” left an operator unable to tell a busy line from a responder that had stopped answering. `ackTimerCollectTimeouts` and `proxyBufferFull` now appear in the status line, **only when non-zero**, so the healthy case stays quiet. The zeros stay on the wire regardless.
- **Nothing tested the endpoint.** `handleRDMDiagnostics`'s own doc comment promises “meaningful zeros stay explicit … must never disappear from the wire”, and that was a comment with no test behind it. `patch_test.go` guarded three of seven zeros; the rest were unprotected. Diagnostics is the worst screen for that failure, because **zero is the answer the operator wants** — a vanished key reads “reissued undefined” on the exact screen someone opened to find out whether the *rig* is broken.

Also added a **Go↔JS key boundary test**: it reads the literal `analyzer.js` and requires every diagnostics property the JS touches to be a key the handler actually emits. The struct spells it `AckTimerTimeouts`, the wire spells it `ackTimerCollectTimeouts` — legal, and exactly the two-spellings-two-files shape that has bitten this project ten-plus times. Only a both-sides test catches a rename on either end. Confirmed failing both ways: old status line, and a renamed JSON tag.

Note for whoever touches that test: its first version scanned the whole file and false-alarmed, because `d` is also the loop variable for capture rows (`d.pid`, `d.destUid`). It is now scoped to the status-line block and says so. Re-scope it if the status line is restructured; do not delete it.

Verified: build, vet, `node --check`, three consecutive `-race` runs clean. **1002 passing** (991 → 995 → 999 → 1002 across the three pieces of work).

## What the concurrent agent session built (reconstructed)

That session is no longer running, so this is reconstructed from the tree and from the `WORKFLOWS.md` it left — which is a **user-facing manual, not an engineering log**. Treat it as evidence of intent, not as a verified account of what works. Its 68 files remain **uncommitted**.

- **Saved shows / workspace** — multiple named shows, per-show entries, groups, presets and baselines, with a shared Fixture Library across all of them. `internal/web/workspace.go`, `workspace.js`, `workspace.css`, `showguard.go`.
- **Show tools** — Needs attention, selection & groups, test presets, rig baselines with TXT export, show recovery. Durable saves via synced temp-file replacement with one `.bak` of the preceding save.
- **Offline rehearsal** — a child UI labelled REHEARSAL on a fake lighting transport, for fault scenarios without touching a rig. `cmd/benny512/rehearsal.go`.
- **Fixture Library** — import GDTF/library JSON, save profiles from the active patch, mark-verified per mode with change detection, export including retained original GDTFs deduplicated by SHA-256. `internal/web/library.go`, `internal/library/`, `library.js`. Limits: 8 MB per GDTF, 120 MB per library import. **No library `.bak` yet** — exported backups are the only recovery.
- **Patch layer** — `catalog.go`, `durable.go`, `phasecount.go` (sub-fixture-aware phase spacing; `0` = automatic counting), `asfound.go` with the three-state as-found model including `not_fitted`.
- **Reconcile commit path** — `reconcilecommit.go`, including the push gate described above.
- **Diagnostics endpoint** — `diagnostics.go` (counters existed; the screen and tests were the gap, now closed).
- **Design-system sweep** — CSS split into `screens-devices/nodes/tools.css` plus `DESIGN.md`, and a near-rewrite of `nodes.js` (+251/−277).
- **sACN removed** — deliberate, confirmed by Dom.

**Unverified by this session:** none of the above has had a review pass, and the branch's test coverage is uneven — items 2 and 3 above were both found untested. A sweep for other build-but-never-consulted gaps is worth doing **before** this lands, not after.

## September 8: the Paladin Cube discovery change is REVERTED

**The Paladin Cubes were faulty hardware.** Dom established this after the fact. They were undiscoverable on any port with any cable because they were not answering — which is exactly what bench capture RDM-LOG25 showed. Benny512 reported an empty rig because the rig was, from the gateway's point of view, empty. **It was right.**

Commit `9b9e8a4` had changed discovery so that a `uidTotal=0` ToD arriving immediately after an `ArtTodControl` AtcFlush no longer completed the discovery. The reasoning was protocol-based: AtcFlush means "flush your ToD and run a full discovery", a real discovery walks a 48-bit UID space and takes seconds, so an empty table 225 ms later is the just-flushed one rather than an answer. The trade was stated and accepted at the time — **latency on a genuinely empty port, in exchange for never reporting "no fixtures" when fixtures are present.**

With the fixtures proven dead, the second half of that trade buys nothing and the first half is a real regression: every genuinely empty port now waits out the full discovery window before saying so, which on a rig walk is a lot of dead time spent confirming what the gateway already answered correctly in a quarter of a second. So it is reverted.

**The protocol argument is not disproven — only unevidenced.** It is recorded here deliberately: if an empty-then-populated ToD is ever observed from a gateway with **healthy** fixtures on the line, this is known ground rather than a rediscovery. Restoring the change would need that capture first. Do not restore it on reasoning alone; that is how it got in.

Mechanics: reverted via `b512-revert-paladin.bat` (a single-use helper, delete after running — see the E1.37 entry above for why these scripts exist). It touches only `internal/session/rdmdiscovery.go` and `rdmdiscovery_test.go`, restores `TestEmptyToDCompletesImmediately`, and removes `rdmdiscoveryempty_test.go`. Suite **1002 → 999**; build, vet and three consecutive `-race` runs clean.

**Worth carrying forward as a method note, not a reproach:** the discovery change was diagnosed, argued from the standard, tested, and wrong — because the hardware was never ruled out first. The capture was read carefully and the fixtures were assumed healthy. When a whole rig of one fixture type fails identically on every port and every cable, **the fixtures are a hypothesis too**, and the cheapest test is another fixture type on the same port.

## September 9: four owner-reported items

Four things Dom asked for after a working session with the app. All four
verified: build, vet, `node --check` on every browser script, and three
consecutive `-race` runs clean. **1010 passing** (1001 → 1010 over the batch).
Each fix has a test confirmed to FAIL against the unfixed code.

### 1. Devices inspector clipped into the title bar

Two independent causes; fixing either alone leaves a broken screen.

- **Both components had opted out of the z-index scale.** `.b5-show-context`
  hard-coded **40** — numerically `--b5-z-modal`, so a page-chrome strip was
  claiming the dialog layer — against `.b5-inspector`'s hard-coded **20**.
  Hand-picked numbers in two different files is how two components end up in
  the wrong order with neither one looking wrong on its own. Both now use
  `--b5-z-*` tokens.
- **The inspector anchored to the VIEWPORT top**, so even with the layering
  fixed its own bar (and its close button) would still start behind the
  sticky chrome. It now offsets by the chrome's **measured** height,
  published as `--b5-chrome-height` by a small ResizeObserver in `app.js`.

A constant was rejected deliberately: `.b5-show-context` sets
`flex-wrap: wrap` and caps the show name at 35vw, so on a tablet in portrait
the chrome is two rows tall — which is the venue case, i.e. exactly where a
constant would fail.

### 2. Universe numbering, rebuilt

**The old model is gone.** There was one global `universeBase` (0|1) applied
to every screen at once, through a single `formatUniverse` whose name did not
say WHICH numbering it produced. That is why the Nodes tab read one less than
the EN4's faceplate and no call site looked wrong: every screen called the
one function, and that function could only be correct for some of them.

**New setting: Art-Net starting universe** — a free number 0-32767, being the
Art-Net universe that the show's **universe 1** lives on. Default 0 (user 1 =
Art-Net 0), which reproduces the old `universeBase: 1` correlation exactly,
so an existing rig reads identically after the upgrade. Free rather than a
0/1 toggle because a show handed the block 100-139 numbers its own universes
1-40, which no toggle can express.

**Storage is unchanged** and this is the important part: `Entry.Universe`,
`artnet.PortAddress` and every other internal value stay the raw 15-bit
Art-Net Port-Address. Nothing re-routes, no show is migrated. Per Dom: the
patch universe and the Art-Net universe are *correlated, not directly
related*, and the starting universe does that correlation.

Per screen:

| Screen | Shows |
| --- | --- |
| Nodes, Analyzer | raw Art-Net Port-Address — ONE flat number, never decomposed into Net/Sub-Net/Universe on screen. Type 17, get Port-Address 17. |
| Patch, Rig Check, Function check, Rig Walk, Send | the show's own universe, from 1 |
| Devices | **both** — `17 (Art-Net 16)` |

`UI.formatUniverse`/`parseUniverse`/`universeBaseLabel`/`setUniverseBase`
were **removed, not renamed**, and replaced by two pairs named for what they
return: `formatUser`/`parseUser` and `formatArtnet`/`parseArtnet`, plus
`formatBoth` for Devices. Deleting the ambiguous name is what forced all 113
call sites to declare intent — a rename would have preserved the defect.

Decisions recorded so they are not re-litigated:

- **A universe below the starting universe has NO user number.**
  `artnetToUser` returns null and the screen shows an em dash plus the
  Art-Net value marked outside the show's range. Never a negative: "universe
  -94" reads as an arithmetic bug and invites someone to "fix" it. Same
  honesty rule as every other place on this project where a plausible number
  stood in for "no answer".
- **Analyzer = raw Art-Net** (Dom did not name it). A packet capture
  renumbered by a display setting is not evidence of anything.
- **Send = show universe** (Dom did not name it). It is an operator action,
  scoped the way Rig Check is. Both are cheap to flip.
- **Legacy migration:** a settings file carrying `universeBase` maps once as
  `start = 1 - universeBase`, then the legacy field is DROPPED so a later
  save cannot re-apply it and shift the rig a second time. An explicit
  `artnetStartUniverse` always wins over a legacy value sent alongside it.
- The patch **TXT** export uses the show's numbering (it is the printed patch
  sheet); the **JSON** export stays canonical wire data.

`TestEachScreenUsesItsOwnNumbering` asserts each screen calls the conversion
belonging to what it is FOR, and was confirmed failing by pointing Nodes back
at the user formatter — which reproduces the original bug exactly.

### 3. GDTF imports now put ALL modes in the Fixture Library

The library's own GDTF import was already correct. The gap was every OTHER
entry point: Patch's "Import GDTF…" and MVR import both parse every mode,
apply one, and discarded the rest.

That loss is unrecoverable, which is why it matters: `patch.Entry` stores a
single mode (`Mode`, `Footprint`, `ChannelFunctions`), so **import is the
only moment the other modes exist**. Harvesting the show afterwards can only
ever return the one mode each entry actually uses.

One shared builder now — `MvrImport.libraryDocFromGdtf` — used by all three
paths, including `library.js`, which previously hand-rolled the record inline.
MVR import also surfaces the GDTF files it resolved, with their original
archives, so those are retained too. Both new calls are **best-effort and run
after the patch is written**: the operator asked to patch fixtures, and a
library failure must not cost them that. Reported in the status line, never
thrown.

**No retroactive expansion**, per Dom — already-imported GDTFs are re-imported
by hand rather than adding a feature to avoid one afternoon's work.

### 4. Pan/tilt invert are toggles, not hex fields

**Spec correction first: these are NOT E1.37-1 PIDs.** Several comments in
this repository said so. E1.37-1's Table A-1 contains no 0x0600-0x0602 at
all — they are E1.20 §10.10.1-3. Verified against the primary text
(`ANSI_E1.20_2025.pdf`) on 2026-09-09: GET response PDL 0x01, PD "Off/On
(0/1)", SET request PDL 0x01, GET and SET both allowed. The wrong citations
are corrected.

The cause was structural, not three missing entries. E1.20 §10.4.2 defines
PARAMETER_DESCRIPTION only for MANUFACTURER-specific PIDs, so a standard PID
can never be self-describing however precisely the standard specifies it.
With only two states — "the device described it" and "unknown bytes" — every
standard PID without a dedicated screen landed in the second and rendered as
a raw hex box.

`ParamDescriptor.SpecDefined` is the missing third state: **the standard
describes it**. The editor already renders a toggle for `DS_BOOLEAN`, so
there is no new UI; the row is now tagged "per ANSI E1.20" instead of the
misleading "raw / unverified".

One change beyond the minimum, and the reason: the question "do we have a
typed layout?" was spelled `!desc.SelfDescribing` in FOUR places (GetParam,
SetParam, decodeSetValue, and the JS editor). Adding a second knowledge
source to each independently would have produced a descriptor that RENDERS
as a toggle but still demands raw bytes on SET — a control that looks right
and refuses to work. It is now one predicate, `ParamDescriptor.Typed()`.

### Housekeeping

`internal/session/rdmdiscovery.go` and `rdmdiscovery_test.go` were the only
CRLF files in an otherwise all-LF tree and were the only two `gofmt` flagged.
Normalized to LF. **The diff is line endings only** — gofmt made no other
change to either file.

## Operating rules — READ BEFORE CHANGING ANYTHING

These are Dom's standing conventions. They are not style preferences; each
one exists because something went wrong without it. **A session that is not
a Claude session will not infer any of these — follow them explicitly.**

### Files and versioning

- **Iterate, never overwrite.** Every meaningful version of a file is a NEW file, timestamped. Keep a **maximum of 4 copies** per family and prune the oldest first (FIFO).
- **Timestamps must be captured, not invented** — read the real clock (`date`, `America/New_York`) on Dom's machine immediately before writing the file. Never approximate one from context.
- **The handoff file is the deliberate exception:** `Benny512 — HANDOFF (current).md` is overwritten in place. Dom wants exactly one authoritative current-state file. Stale handoffs are a hazard; the append-only Project Notes file is the history.
- **`DLux Rackmaster` is READ-ONLY inspiration. Never modify it.**

### Updating the handoff is mandatory

Update it **in the same session as the work**, not "later". It is the only
thing carrying state between sessions and agents. Record what changed, why,
what was verified and how, and anything deliberately NOT done with the
reason. A decision not written down gets re-litigated or silently reversed.

### Engineering

- **Root-cause culture.** Diagnose, do not patch around. A feature that does not work gets **removed**, not defended.
- **A test must fail against the unfixed code.** Capture the proof-of-failure output. A test that passes both ways proves nothing, and this project has shipped several.
- **Tests must exercise the real thing.** Load the literal JS the browser loads; feed real bytes to the real handler. A test that builds its request from the same struct it asserts on proves only that one side agrees with itself — that has been this project's most-repeated defect, now **eleven** instances.
- **Minimal, surgical changes. No refactoring of working systems.**
- **Zero external dependencies.** Hand-rolled WebSocket, hand-rolled ZIP reader. Keep it that way.
- **Accuracy over assumption.** Verify against primary specs; mark uncertainty plainly rather than presenting a guess as a reading. Ask clarifying questions before writing code.
- **Commit after each step** when working through a list.

### Vocabulary (Dom's, and it matters on a call sheet)

**ports**, not jacks. **BiDi**, not Bi. Power is **circuit** / **draw** / **W**.

### Accessibility — a standing constraint, not a nice-to-have

High contrast, dyslexia-friendly, and **never colour as the sole signal**.
Assume a dark venue, a tablet, and gloves.

## Open items

Items 1-3 closed earlier; 14-17 are the September 9 batch, also closed. The
numbering is kept so earlier notes and commit messages still refer to the
same things.

1. ~~E1.37-2 wire formats.~~ **DONE**, `55264ec`.
2. ~~Per-device probe cache.~~ **DONE** — was already built; the gap was proof.
3. ~~ACK_TIMER counters on Diagnostics.~~ **DONE** — counters existed; the failure signal was not on screen and the endpoint was untested.
4. **Per-port RDM on/off, and the input-enable question.** The Art-Net 4 spec PDF is in the RDM App folder. Input-enable is currently refused on shared multi-port binds because ArtInput rewrites every enable bit and there is no sibling readback; the open question is whether the spec offers a per-port route. Dom can run Wireshark alongside the EN4's own configuration software to see what that tool actually sends. **Not urgent — curiosity-driven.**
5. **Scope readback.**
6. **`patch.Entry` has no provenance**, so automatic import migration remains unsafe.
7. ~~`resolveSupportedSet` single-flight.~~ Already implemented and tested.
8. **Server-side min/max normalisation.**
9. **`git gc`** — dangling commits are harmless; run when convenient.
10. **Rig Check sticky bar.**
11. **E1.37-1 PRESET_INFO / POWER_ON_SELF_TEST layouts**, some E1.37-2 PIDs and the DNS set, and `ENUM_LABEL` remain intentionally incomplete or weakly confirmed.
12. **The removed “Fix all address mismatches” Reconcile endpoint is still callable.** Dom's call whether it returns or is removed.
13. **Dom may nominate more user-facing PIDs** (`classification.go`, one entry each).
14. ~~Devices inspector clipping.~~ **DONE**.
15. ~~Universe numbering rebuild.~~ **DONE**.
16. ~~All GDTF modes to the Fixture Library.~~ **DONE**.
17. ~~Pan/tilt invert toggles.~~ **DONE**.

### Cross-cutting

- **Bench work outstanding:** EN4 per-port commands, multi-cell phase spacing, wireless mode-label traffic and wireless SET settling all still need the bench. The E1.37-2 network-configuration controls are spec-correct but **not yet confirmed against a real gateway** — verify on hardware before relying on them in the field.
- **The universe rework wants a bench pass too:** confirm the Nodes tab now matches the EN4 faceplate exactly, and that Patch/Rig Check read the show's numbering, with the starting universe set both to 0 and to a non-zero block.
- **Race testing runs in the Linux sandbox**, not Windows-native. Treat it as a genuine spot check, not as the gate having moved home.
- **Agent shell access to Dom's machine has been unavailable since September 8.** See the next section — it is the reason for everything left undone below.

## BLOCKED ON TOOLING — pick these up once the shell mounts

Everything here is *ready to do* and was left undone only because no channel
in the September 8-9 sessions could run a command on Dom's machine. **Do
these first in a session where `device_bash` works.** None of them needs a
decision from Dom; they are chores with a known answer.

### The blockage, so nobody re-diagnoses it

`device_bash` fails on **every** command — including a bare `echo`, which
touches no folder — with:

```
sandbox-helper: no Plan9 drive shares mounted under /mnt/.virtiofs-root/shared
```

That is a precondition check aborting before the shell runs, not a per-path
failure. `get_device_info` reports **no `scratchFolder`**, which the API only
populates once `device_bash` has succeeded in a session — so the shell never
ran in those sessions, even though it worked earlier in the same session on
September 8 (a tarball was extracted through it). A working share went away
mid-session and did not come back across an app restart **or** an app update
(1.46388.4 → 1.49585.0, Electron 42 → 44). Dom is on a **laptop**; a
sleep/resume cycle is the most likely trigger.

What was tried and did NOT help: restarting the desktop app; the app's own
update. What is expected to help: **starting a new task linked to the
computer** — a session cannot re-bind its own share, only a fresh one can. A
reboot clears wedged hypervisor state if a new task alone is not enough.

Two channels that are NOT substitutes, so don't spend time on them:

- **Deletion** — `device_request_delete_permission` grants `rm` *inside*
  `device_bash`, so it is downstream of the same blockage.
- **The local Filesystem MCP server** — announced and running on Dom's
  machine, but every one of its tools errors in these sessions: it declares
  JSON-Schema **draft-07** and the session validator accepts **2020-12
  only**. Not fixable from Dom's side.

File transfer (`device_list_dir` / `device_stage_files` /
`device_commit_files`) worked throughout and is how all 38 files of the
September 9 batch reached the tree. **Write files directly with those. Do
NOT batch file writes into a script for Dom to run** — that spends his
attention on something the tools already do, and he has said so.

### Chores waiting on a shell

1. **Delete the spent single-use helpers** from `RDM App\` (all are used up;
   several are scripts written only because the shell was down):
   `b512-commit-sept9.bat`, `b512-commit-sept9-msg.txt`, `b512-snapshot.bat`,
   `b512-now.zip`, `b512-items134.zip`, `b512-apply-items134.bat`,
   `b512-revert-paladin.bat`, `b512-revert-paladin-msg.txt`,
   `b512-e137fix.tgz`, `b512-commit-e137.bat`, `b512-e137-commitmsg.txt`.
   Also check `b512-check.tgz` and `b512-now.tgz`, likely stale from earlier
   sessions. Deleting needs `device_request_delete_permission` first.
2. **Delete the empty `internal/sacn/` directory.** sACN was pruned
   deliberately and its files are gone, but git does not track empty
   directories so the folder remained. It is harmless to the build and
   actively misleading to read — it looks like sACN came back.
3. **Run `go build ./...` and `go test ./...` natively on Windows.** This has
   NEVER been done for the September 9 batch. What *was* done: the full suite
   ran in the Linux cloud container against a tree verified byte-identical to
   Dom's — all 124 Go/CSS/HTML files and 23 JS files size-matched, eight
   diffed byte-for-byte, and every mtime accounted for as either pre-snapshot
   or an agent write. That is strong evidence, **not** a native run. Close
   the gap.
4. **Windows-native `-race` is still blocked** on a GCC/MinGW-compatible cgo
   compiler; Visual Studio `cl.exe` rejects the GCC-only flags. The sandbox
   runs are a genuine spot check of the race detector's findings, not the
   gate having moved home. Resolve the compiler prerequisite if a native run
   is ever wanted.

### Not blocked on tooling — blocked on hardware

5. **Bench-verify the universe rework.** Confirm the Nodes tab now matches
   the EN4 faceplate exactly, and that Patch / Rig Check / Rig Walk read the
   show's numbering — with the Art-Net starting universe set both to 0 and to
   a non-zero block. This is the change most likely to be judged wrong by
   eye, and the one the owner reported.
6. **Bench-verify the pan/tilt toggles** on a real fixture that advertises
   them (a JDC-1 advertises TILT_INVERT and not the other two, which is also
   a good check that the per-PID gate still asks for what the device has).
7. **Bench-verify the E1.37-2 network-configuration controls** against a real
   gateway. They are spec-correct against the primary text but have never
   touched hardware, and this PID rewrites a gateway's network config.

**Why this handoff is overwritten:** Dom explicitly requested one authoritative current-state file. The append-only Project Notes file is the history and safety record; stale handoffs are a hazard.

## 2026-09-15 22:01 ET — owner correction and overnight handoff

Dom confirms the current patch's fixture types, addresses, and universes are accurate. Some Paladin Cubes were mixed up in the physical rig, so individual commit identities may not line up exactly; do not use that mismatch to rewrite the patch or infer a software defect.

The next implementation priority is **Rig Check by fixture type**, ahead of fades, sACN and Universe Identify, so Dom can exercise it on the live rehearsal rig tomorrow. The intended key is the exact trimmed saved Entry.FixtureType, grouped across modes/universes/positions; blank types are omitted and stale keys fail closed. This must be reviewed by tomorrow's agent before use on the production rig. The last agent prepared fixture-type scope tests but exhausted its usage budget before completing the backend/UI implementation; do not claim this feature is shipped until the tests and browser behavior pass.

The pan/tilt fallback request remains: when a GDTF does not provide a moving-fixture pan/tilt default, assume coarse 128 and fine 0; an explicit GDTF value wins. Keep this separate from fixture-type scope and verify it.

The dimmer-flash sequence is uncertain per Dom and moves to the tail of the queue. Do not diagnose it from the earlier deselection hypothesis; reproduce the exact sequence with outbound DMX frames before changing the engine.

**Tomorrow's review note:** independently review every change from this session, especially scope validation, output isolation, fixture-type grouping, patch identity assumptions and fallback defaults, before connecting or driving the live rig. Run the focused tests, browser syntax/tests, full Go test/vet/build gates, and inspect the diff. Treat any missing gate as a stop signal.

Fresh executable built from commit `46b9cb9`: `dist\\benny512_091526_102838a.exe`, 14,071,296 bytes, SHA-256 `5D9D6C091B36C0B5119B9194D1A80F1D8D001FA5FDB31C41E7C6132565F02132`. Existing releases and runtime data were not overwritten.

## 2026-09-15 — sACN output work started

Dom authorized proceeding with sACN output for Rig Check. Initial review found no retained sACN sender, encoder, or `internal/sacn` package in the current tree or reachable history. The existing Rig Check path constructs Art-Net packets directly through `session.DMXOutputEngine`; sACN is therefore being developed as a protocol-aware transport foundation first, with literal E1.31 packet tests before any live Rig Check toggle is exposed. Do not ship or use an sACN option until packet fields, universe mapping, sequencing, destination policy, stop/blackout and cross-protocol isolation are tested. Commit and executable release are pending completion of those gates.

First sACN chunk complete, commit `a15c3b5`: new `internal/sacn` E1.31 Data Packet encoder with literal field/length/payload tests. The encoder now matches the primary text's 638-byte layout, framing PDU length 0x04d, DMP PDU length 0x20b, property count 513 and universe range 1-63999. Rig Check is not yet wired to select this transport; do not expose it on a live rig until the transport integration and stop/blackout tests land.

## 2026-09-16 15:03:00 -0400 — OPEN PROBLEM: Martin ERA 800 RDM fault on 2.11.90.1 port 1

Short statement; the full data record is in the Project Notes entry of the same
timestamp (census tables, every reply verbatim, request counts, the collision
tests and the bench procedure).

**This is not a Benny512 defect.** The app's reads are correct, sequential and
correctly retried throughout both captures. Do not spend app-side effort on it
beyond the reporting item below.

### The problem in one paragraph

Node `2.11.90.1` Port-Address 21 advertises **11 Martin UIDs**. Dom has **8
ERAs** physically on that port. Seven answer perfectly. One answers almost
nothing. **Three are phantoms that have never answered anything, in either
capture, two days apart.** `11 - 3 = 8`, Dom's count exactly. The comparison
port `2.11.90.6/11` carries 8 ERAs, all healthy, and is clean.

### The suspect unit

**`4D50:001158FE` — DMX address 1, universe 21, node 2.11.90.1 port 1.**

The address is derived: both ports run the same eight-fixture plan at 42-slot
spacing (1, 43, 85, 127, 169, 211, 253, 295), and **address 1 is the only one
missing** from the seven that answer on `.90.1`. The unit has never reported
its own start address.

- Answers ~3% of requests (2 of 63 in LOG30; 6 of 64 in LOG29), all with valid
  checksums, so it is genuinely present and genuinely answering.
- **Has never once answered DEVICE_INFO** — 24 asks across two captures.
  DEVICE_INFO is mandatory under E1.20, so the responder is non-conformant.
- Only ever ACKed MANUFACTURER_LABEL ("MARTIN"). NACKs `0x0070` /
  `0x0011` as UNKNOWN_PID, while its seven healthy siblings advertise `0x0070`.
- Model and firmware unreadable. The other fifteen ERAs all report model
  `0x009B`, software `0x000000F0`.

### The three phantoms

`4D50:0011597E`, `4D50:001159BE`, `4D50:001159FE` — advertised in the ToD,
asked 48-63 times each, **zero replies ever**, present in both captures,
surviving AtcFlush and fresh discovery, and present nowhere else in the rig.

### Next action is Dom's, on the bench

Quarantine the fixture at **DMX address 1 on the `.90.1` run** onto an isolated
data line and discover it. If several UIDs appear, one unit is advertising
multiple identities — a manufacturer defect that explains the phantoms and the
3% response rate in one stroke, and one worth reporting to Martin with the
logs. If one UID appears, the phantoms belong to the EN4 and the next question
is whether they survive a discovery with that unit removed. Capture RDM either
way; an isolated-unit capture is worth more than the three rig captures
combined.

Dom's own hypothesis — two fixtures shipped sharing a UID, which E1.20 forbids
— is tested against the data in the Notes entry. Summary: no duplicate UIDs and
no duplicate start addresses appear in LOG30, and LOG30 has zero malformed
packets, so there is no corruption signature on that line. That does not rule a
collision out (two mostly-silent responders collide into silence, not garbage),
but nothing in the captures supports it either. The bench test settles it.

### The one app-side item this generates

**Benny512 should report advertised-versus-answering per port.** "This gateway
advertises 11 devices on this port; 3 never answered" would have surfaced the
phantoms in seconds instead of across three captures and two sessions. The
honest-reporting discipline already applied elsewhere in this app, pointed at
discovery. Bounded, high value, not yet built. `86e0f16`'s attempt cap already
bounds the wasted traffic (222 requests in LOG30 alone) but says nothing on
screen.

## Where the documents live now

This project's working documents are in the repo, not beside it:

| Path | What |
| --- | --- |
| `CLAUDE.md` | Operating rules. Loaded automatically; read it first. |
| `docs/HANDOFF.md` | This file. Current state, overwritten in place. |
| `docs/notes/YYYY-MM.md` | Append-only history, split by month. |
| `docs/decisions/` | One file per durable decision. |
| `docs/reference/` | Architecture, protocol and PID research. |
| `docs/evidence/captures/` | Raw RDM captures from real rigs. |

`main` stays releasable; work happens on a short-lived branch per chunk with a
PR. Versions are tags and the Windows executable attaches to a GitHub Release
with its SHA-256 -- **executables are no longer committed and `dist/` is
ignored**, so the timestamped-exe FIFO ritual no longer applies to builds.
