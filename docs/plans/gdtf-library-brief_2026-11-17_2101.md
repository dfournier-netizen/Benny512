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
