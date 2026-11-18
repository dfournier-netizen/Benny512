# GDTF library — plan and agent brief

**Written:** 2026-11-17 21:01 -0500 · **Base:** `main` at `d7c8d2c` (v0.3.4+) · **Status:** all owner decisions taken (two rounds); ready for G1.

## What Dom asked for

A local library of GDTF files that Benny512 keeps in sync with GDTF Share, so
Console-lite can get attributes for any fixture on the rig without hunting for files.
Naming RDM personalities from it is a side benefit.

## Owner decisions (2026-11-17)

1. **Source:** the GDTF Share public API (`gdtf-share.com/apis/public/`): `login.php`,
   `getList.php`, `downloadFile.php?rid=`. "Powered by GDTF" credit plus logo is fine.
2. **Offline is normal.** Files stay on disk and are used offline; Benny512 refreshes them
   whenever it can reach GDTF Share. (Dom's reading of the API's caching terms: consoles
   such as grandMA3 work the same way.)
3. **Whole manufacturers,** plus single-fixture pulls on demand. Starting list:
   Ayrton, Martin, Elation, Robe, Chauvet, GLP, Volux, ADJ, ACME, SGM, Astera, ChromaQ,
   ETC, High End Systems. **The user can add (and remove) manufacturers.**
   The match against GDTF Share's manufacturer names must be checked live; a name that
   matches nothing is reported to the user, never silently dropped.
4. **Benny512's own sync does the first big import.** There is no separate pre-import tool.
5. **Login:** each user enters their own GDTF Share login. It is saved per install and
   never travels: not in show files, library exports, logs, diagnostics or captures.
6. **Library location:** `%APPDATA%\Benny512\GDTF\`, beside the existing shared Fixture
   Library (`benny512-library.json`; see `cmd/benny512/librarypath.go`).
   **The library is not committed to the repo and not embedded in the exe.**
   The repo carries code plus a handful of small test files only.
7. **Test it right:** a fake GDTF Share server in tests, then a bench sync on Dom's laptop.

## How it fits what exists (survey 2026-11-17)

- GDTF parsing is **browser-side** (`internal/web/static/js/gdtfparse.js`). The server stores
  parsed profiles plus the original archive (`internal/library/sourcefiles.go`: base64 in
  the JSON, 8 MB per-file cap) in the one `benny512-library.json`.
- That single-JSON store is right for fixtures in use and wrong for thousands of
  downloaded files. **Keep the two separate:** the GDTF folder is a raw file cache plus an
  index; the Fixture Library stays as it is. Picking a fixture from the GDTF library feeds
  the **existing** import path (browser fetches the bytes from the server → `gdtfparse.js` →
  `POST /api/library/import`). No second parser and no change to how profiles are stored.
- Station settings live in `benny512-settings.json` beside the exe (`internal/web/settingsdurable.go`).
  That is per install, which matches decision 5.
- Rules from `CLAUDE.md` apply in full: stdlib only (`net/http`, `archive/zip`, `encoding/xml`
  are fine), every gate, render-proof for UI, a failing test before each fix, and "a plausible
  value never stands in for no answer".

## Design

**On disk** (`%APPDATA%\Benny512\GDTF\`):
- `files/<rid>.gdtf`: one file per downloaded revision, written to a temp name then renamed,
  so a killed sync never leaves a half file under a real name.
- `index.json`: per file, everything `getList` returned (rid, manufacturer, fixture, revision,
  dates, uploader, rating, GDTF version, creator, uuid, size, modes with footprints), plus
  the local SHA-256, the download time and the RDM data from step G2.
- `sync.json`: the followed manufacturers, the last successful list fetch, the last result
  per manufacturer, and pending or failed downloads. A sync resumes from this.

**Sync (server, Go, new package `internal/gdtfshare`):**
- Log in, then fetch the list once per sync. Diff it against the index for followed
  manufacturers: download new rids, replace superseded ones (newest only: round 2, decision 1), and skip
  unchanged ones. Downloads are **sequential and spaced**, with no parallel requests.
- **Session expiry:** the cookie lapses after two hours of inactivity. On an auth failure,
  log in again once and retry; if that fails, stop and report it.
- **Failures:** a failed download stays pending for next time. One bad file never aborts the
  sync. Offline at start means the library is untouched and the UI says "offline · last
  synced <time>".
- **When:** at start-up if online, then every **120 min** while running (the API docs
  recommend about that), plus "Sync now". Allowed while ARMED, never over the lighting adapter (round 2, decision 3).
- **Single pull:** search the cached list (Name **and** LongName, as the API terms require) and
  download one rid on demand, even for a manufacturer that isn't followed.
- **Credentials:** in `benny512-settings.json`, with the password encrypted by Windows DPAPI
  (`CryptProtectData`, current-user scope, via `syscall`, no deps). On other OSes (tests,
  demo) the password is not stored. It is never returned by any API: the UI gets "saved ·
  user <name>" only.

**RDM matching (G2):** read each file's `description.xml` for the RDM protocol block
(manufacturer ID, device model ID, software versions, DMX personality → mode).
**Verify the element and attribute names against DIN SPEC 15800 / the GDTF spec
(github.com/mvrdevelopment/spec) before coding; do not trust this brief's memory of
them.** Many files have no RDM block; those match by name only, and the UI says so.

**UI (G3), mockup first per standing practice:**
- Settings › **GDTF Share**: login (user, password, Test), followed-manufacturer list
  (add/remove, per-manufacturer file count and last result), Sync now, status line, and the
  "Powered by GDTF" credit.
- Fixture Library › **Add from GDTF library**: search the local index offline, with a
  Manufacturer/User label on every row (API terms), plus "Search GDTF Share" when online for
  a single pull.
- About + both manuals: "Powered by GDTF" + logo. **Logo asset:** Dom to supply, or an agent
  gets it from the GDTF Share site; do not draw one.
- Accessibility rules as always: words, not colour alone, for synced/pending/failed.

## Chunks (one branch + PR each; commit early)

| Chunk | Branch | Scope | Done when |
|---|---|---|---|
| G1 | `feat/gdtf-g1-sync` | `internal/gdtfshare`: client, index, sync, credentials (DPAPI), `/api/gdtf/*` (status, settings, sync, search, file bytes). No UI. | Fake-server tests below pass; all gates |
| G2 | `feat/gdtf-g2-rdm` | RDM block from each file into the index; match discovered devices (manufacturer + model ID → candidates; personality → mode) | Real-file tests (repo's GLP JDC1 / Elation extracts if they carry RDM; otherwise say so) |
| G3a | `docs/gdtf-g3-mockup` | Mockup of the Settings and Library screens | Dom approves |
| G3b | `feat/gdtf-g3-ui` | Build the approved UI, wired to the existing import path | Render-proved on `--demo` |
| G4 | `feat/gdtf-g4-bench` | Dom's bench sync on the laptop; fix what it finds | Real sync of the 14 manufacturers completes; offline restart uses it |

**G1 fake-server tests (each must fail against a stub first):** login good/bad; list
parsing from a recorded-shape response; first sync downloads everything followed; a second
sync downloads nothing; a changed revision is replaced; cookie expiry mid-sync → one re-login
and the sync continues; a download error → pending, and the rest complete; killed mid-file
→ no file under its real name, and resume finishes it; offline → index untouched, status
says so; a followed name that matches nothing → reported; the password never appears in any
API response, log line, show save or library export (grep the outputs).

The API's exact response shapes are not in the repo. G1 starts by fetching
`https://gdtf-share.com/help/developers/gdtf_share_dev/gdtf_share_api_1/index.html` and
recording the documented shapes into the fake server, with the page cited in the commit.
The sandbox cannot reach gdtf-share.com, so G4 is the first real contact.

## Owner decisions, round 2 (2026-11-17 21:05)

1. **Keep the newest revision only.** When GDTF Share has a newer revision, the old file is
   replaced. Shows and Fixture Library entries are unaffected: each library record already
   keeps its own copy of the original archive (`internal/library/sourcefiles.go`) and shows
   store their profiles, so nothing in use depends on the GDTF folder keeping old revisions.
2. **Pull user-uploaded files too,** for followed manufacturers, and label every row and every
   imported fixture Manufacturer or User.
3. **Sync while ARMED is allowed, but never over the lighting network.** Requests go only over
   the internet route, never the adapter selected for Art-Net or sACN. If the only route to
   the internet is the lighting adapter, the sync does not run and says why.

## Progress

| Chunk | State | When | Notes |
|---|---|---|---|
| G1 | Built on `feat/gdtf-g1-sync`, awaiting coordinator review | 2026-11-17 21:36:42 -0500 | Fake-server tests and all gates green. Same fixture = uuid + uploader label. Not yet run against the real GDTF Share (G4). Open for the owner: the API page's "cached files no more than 24 h old" vs decision 2; default names that may not match GDTF Share's spelling. |
| G2 | Built on `feat/gdtf-g2-rdm` (on G1), awaiting coordinator review | 2026-11-17 22:13:43 -0500 | description.xml read into the index (download + one-time backfill; unreadable files kept with the reason); zero RDM ID = not given (GLP JDC1 `DeviceModelID="0x0"`); LongName search; `GET /api/gdtf/match` ranked software-version → model, labels kept, no name fallback. Real files carry RDM IDs but no software versions/personalities, so personality → mode is proved on synthetic files. Open for the owner: within a tier, Manufacturer uploads before User; model-only mode given only when all versions agree. |
| G2b | Built on `feat/gdtf-g2-rdm` (on G2), awaiting coordinator review | 2026-11-17 22:32:24 -0500 | Round-4 answers: "likely" mode with warning/basis/choices when versions disagree; "other-versions" fallback when the device's version maps nothing (tier stays software-version); most-likely rule = most versions, then highest software version ID, then name. |

## Owner decisions, round 3 (2026-11-17 21:53, after G1)

1. **The lighting-network refusal is intentional, including when Art-Net/sACN is on the
   auto-picked adapter that is also the internet route.** G3's Settings › GDTF Share screen
   must say so plainly before the user hits it: which adapter is the lighting network, that
   sync never uses it, and what to change (pick a dedicated lighting adapter) if sync is refused.
2. Unmatched manufacturer names are resolved at G4 from the similar names offered.
3. A full Settings reset clears the GDTF login; the downloaded files are kept. Accepted.

## Owner decisions, round 4 (2026-11-17 22:22, after G2)

1. **Within a match tier, files uploaded by the manufacturer rank before user uploads.** (Already
   G2's behaviour; kept.)
2. **Model-only match where the file's software versions disagree: give the most likely mode,
   with a warning,** instead of no mode. It stays visibly a guess (basis, warning, alternatives listed).
3. **The device's exact software version maps no mode for its personality: fall back to the
   other software versions' mode** by the same rule, with a warning naming those versions.

Most-likely rule (G2b, `internal/gdtfshare/match.go` rankModes): the mode mapped by the most
software versions; ties to the mode mapped by the highest software version ID (a version with
no ID counts lowest); then the mode name A to Z.
