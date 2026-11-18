# GDTF library — G3a design mockup

**Status:** for Dom's review. Design only: no production file changes. G3b builds the real
screens only after Dom approves this (plan: `docs/plans/gdtf-library-brief_2026-11-17_2101.md`).

## How to open it

Double-click **`mockup_2026-11-17_2309.html`** (any browser; it is one self-contained file and
needs no server). Mockup controls sit in a dashed strip at the bottom of the window, apart from the app:

- **Scene**: pick any of the 16 scenes, or use **Previous / Next**. Each scene also has its own
  address (`…html#s5`), so a scene can be bookmarked or sent.
- **Theme**: Dark or Daylight (the production theme tokens).
- **Show data notes**: yellow dashed **NEW · field** tags mark anything the server does not
  provide today (list below). Untick to see the screens as Dom would.

Buttons in the screens are inert, except: the two search tabs and **Search by name** move
between scenes; **Go to Art-Net network adapter** jumps to that field; the Fixture library's
**Close** and Escape close it. All names, files, counts and IDs are made up.

The top of the window is the real app chrome (header, output strip, nav), drawn with the
production CSS and icon sprite inlined. The group fader bar is left out. The logo image is
left out to keep the file small.

## Scenes

| # | Screen | What it shows |
|---|---|---|
| 1 | Settings › GDTF Share | No login yet: user name, password, **Test login**, **Save login**; what the login is for and that it stays on this computer, encrypted, per Windows user, never in shows/exports/logs. |
| 2 | Settings › GDTF Share | Logged in and synced: “Saved · user dfournier” (never the password), last sync time, files on this computer, the 14 followed manufacturers with GDTF Share counts (manufacturer / user), files here and last result each, **Sync now**, **Follow** / **Stop following**. |
| 3 | Settings › GDTF Share | Syncing: “File 412 of 1,505”, a progress bar with the same words, and a line saying search, import and device matching keep working. |
| 4 | Settings › GDTF Share | Offline: “offline · last synced Tue 17 Nov 2026, 21:40”; the files here are still used. |
| 5 | Settings › GDTF Share | **Refused because the internet route is the lighting adapter** (round 3 #1): a plain warning that this is on purpose, which adapter, and what to change (**Settings › 1 Network › Art-Net network adapter**, now “(default / any)”), with a button that jumps there. |
| 6 | Settings › GDTF Share | “Martin” and “ChromaQ” matched nothing on GDTF Share; the similar names it has are offered as **Follow “Martin Professional” instead**, never followed automatically. |
| 7 | Settings › GDTF Share | Partly synced: 3 downloads waiting (fixture, Manufacturer/User file, the reason in words, tries, since when) and 1 file that could not be read, with its reason. |
| 8 | Settings › GDTF Share + About | “Powered by GDTF” at the foot of the GDTF Share section and in a new **About Benny512** section. The logo is a labelled placeholder box (“official GDTF logo goes here”), not drawn. |
| 9 | Fixture library › Add from GDTF library | Offline search of this computer's GDTF files by name, long name and manufacturer. Every row: Manufacturer file / User file in words, revision, GDTF version, size, date, modes with channel counts. **Import…** leads to the **existing** confirm step (“Review the import, then apply”, Apply import / Cancel), shown above the search. |
| 10 | Fixture library › Add from GDTF library | **GDTF Share (online)** tab: a single pull of a Claypaky fixture (a manufacturer not followed): “downloads this one file only”, then the same confirm step. |
| 11 | Fixture library › Add from GDTF library | A file over the Fixture Library's 8 MB cap (11.4 MB): the refusal is shown on the row before anyone tries, and in the status line. See open question 1. |
| 12 | Devices › inspector › Info | Exact match (`found`): matched on manufacturer ID, model ID and the device's software version; “Personality 3 is Mode 1 · 49 channels”; the user upload that also matches is listed after the manufacturer's file. |
| 13 | Devices › inspector › Info | `likely`: “Likely mode — not certain”, the server's warning and basis word for word, and the other mode this personality maps to. |
| 14 | Devices › inspector › Info | `other-versions`: the warning names the versions the mode came from and the device's own version it did not. |
| 15 | Devices › inspector › Info | No RDM match: says so, says Benny512 does not pick a file by name on its own, and offers **Search by name** filled in from the device's model name. |
| 16 | Devices › inspector › Info | Name search results that say why RDM could not match them: one file where “the file gives no model ID (it says 0x0)”, one with “No RDM data in this file”. Each is marked **Found by name** and “Not confirmed”. |

## Where things sit, and why

- **Settings › 7 GDTF Share**, a new numbered section after Theme (Danger zone becomes 8, the
  new About 9). Settings already holds the per-install station settings, and the login is per
  install (decision 5). Its buttons act at once (they change nothing on the wire), so the
  section header says “no Apply needed” (open question 3).
- **Fixture library › Add from GDTF library**: a button beside **Import GDTF / library JSON**
  in the existing Fixture library panel (Show menu). Picking a file ends in the same confirm
  step as importing a GDTF from disk, because it *is* the same import path (plan: file bytes →
  `gdtfparse.js` → `POST /api/library/import`).
- **Devices › Device inspector › Info tab**, a new group **GDTF file for this device**, right
  after “What this device says it is”. The match is asked with exactly that device's RDM
  identity (manufacturer ID, model ID, software version ID, current personality), which the
  Info tab already reads from the device, and the inspector is where one device is looked at
  closely. The Patch / Reconcile screens work on patch entries, not one discovered device's
  RDM identity, so they were not used. The group is read-only: nothing is sent to the device.

## Owner decisions shown

| Decision | Where |
|---|---|
| Offline is normal; refresh when connected (round 1 #2) | 4, 9, 3 (“stays usable”) |
| Whole manufacturers, add/remove, unmatched names reported never dropped (round 1 #3, round 3 #2) | 2, 6 |
| Own login per install, never travels (round 1 #5) | 1, 2 |
| Newest revision only (round 2 #1) | 2 (Sync caption) |
| User uploads pulled too, labelled apart (round 2 #2) | 2, 7, 9–16 (every row and match) |
| Sync while ARMED, never over the lighting network (round 2 #3) | route box on 1–8 |
| Refusal is intentional even on the auto-picked adapter; say which adapter and what to change before and when refused (round 3 #1) | route box on 1–8; 5 |
| Full reset clears the login, keeps the files (round 3 #3) | Danger zone line on 1–8 |
| Manufacturer files before user uploads (round 4 #1) | 12 |
| Likely mode with a warning (round 4 #2) | 13 |
| Fall back to other versions with a warning naming them (round 4 #3) | 14 |
| “Powered by GDTF” + logo, logo not drawn | 8 |
| Single-fixture pulls, also from manufacturers not followed | 10 |

## What the API gives, and what is NEW

Everything not marked comes from today's API (`internal/web/gdtf.go`): status (`state`,
`message`, `progress.done/total`, `lastSync`, `files`, `followed`, `manufacturers[]` with
`matched/shareName/shareManufacturerFiles/shareUserFiles/localFiles/failed/similar/checkedAt/notYetChecked`,
`pending[]`, `intervalMinutes`, `unreadable`, `notYetRead`, `login.message`), search
(`in=library|share`, Entry fields incl. `uploaderLabel`, `modes[].dmxfootprint`, `filesize`,
`inLibrary`, `description.longName/rdm`), match (`matchedOn`, `dmxMode`, `modeState`,
`modeBasis`, `modeWarning`, `modeChoices`, `modeFromVersions`, `note`, `notYetRead`,
`unreadable`), and the device's own RDM data (DEVICE_INFO, personality description). Where
the server already has a sentence it is used as it is (refusal, partly synced, pending errors,
unmatched names, unreadable reason, mode warnings and bases; counts formatted). The offline line
is the plan's wording, put together from `state` and `lastSync`.

NEW (tagged in the mockup):

| Tag | What G3b would need |
|---|---|
| `lightingAdapters` | The adapters sync refuses (Art-Net bound at start-up, sACN), with “picked automatically” when Art-Net is “(default / any)”, in `/api/gdtf/status`. The server already knows both (`gdtfLightingNetworks`, `sacnAdapter`). |
| `internetRoute` | Which adapter the last sync left by. Optional; drop it if it is not cheap. |
| `progress.manufacturer` | The manufacturer being synced and “n of m”. Today progress is files done/total only. |
| `unreadableFiles` | The list of unreadable files with their reasons. Today status has only the count; the reasons are in the index. |
| `version endpoint` | Version, commit and build time for About (the exe knows them via `--version`). |
| `inFixtureLibrary` | Whether a match is already in the Fixture Library. Can be worked out in the browser by comparing SHA-256s with the library's stored source files. |
| `label on the imported fixture` | Round 2 asks for every imported fixture to carry its Manufacturer/User label; the Fixture Library record has no field for it yet (the file endpoint already sends `X-Benny-GDTF-Uploader`). |
| `longName in share search` | GDTF Share search covers names only until a file is downloaded (getList has no long name; G1 known limit). |
| `hint text` | One added sentence on the existing Art-Net adapter field, saying it also decides GDTF sync. |

Worked out in the browser from data it already has (not tagged): the **footprint check** in
scenes 12–16 (the mode's channel count from the file against the DMX footprint the device
reports), and the unmatched-names line in the status box.

## Open questions for Dom

1. **8 MB import cap.** A GDTF file over 8 MB cannot go into the Fixture Library; the GDTF
   library itself keeps files up to 64 MB. Scene 11 shows the refusal. The cap exists because
   the Fixture Library keeps a copy of each original GDTF inside its one library file, so big
   files make that file bigger and slower to load. **Raise the cap (to what?), or keep refusing
   and say so as drawn?**
2. **About.** Is a new last section of Settings the right home for About (version + “Powered by
   GDTF”), or would you rather have it in the Show menu?
3. **GDTF Share acts at once.** Save login, Follow / Stop following and Sync now take effect
   when pressed, not on **Apply settings** (they never change what is on the wire). OK?
4. **Stop following.** As drawn (and as G1 behaves), the files already downloaded stay but are
   no longer refreshed. Keep them, or delete them?
5. **A GDTF tag on every device card?** The Devices list could show “GDTF: found / likely /
   none” on each card without opening the inspector. Not drawn; say if you want it.
6. **Footprint check.** Scenes 12–16 compare the mode's channel count with the slots the device
   reports (“Footprints agree / differ”). Useful, or noise?

## Files

- `mockup_2026-11-17_2309.html`: the mockup (built; do not edit by hand).
- `src/shell.html`, `src/scenes.js`, `src/mockup.css`: its source. `mockup.css` adds layout
  only; every colour is a production role.
- `build-mockup.mjs`: inlines the production CSS (every stylesheet `index.html` links, same
  order) and icon sprite into the dated file: `node docs/design/gdtf-library/build-mockup.mjs mockup_2026-11-17_2309.html`.
- `check.mjs` + `shrink-evidence.py`: the render checks and screenshots in `verification.md`.
- `evidence/`: `checks.json` and ten screenshots.
