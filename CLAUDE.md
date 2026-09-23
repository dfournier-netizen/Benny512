# Benny512 — working agreement

Read this before changing anything. Every rule here exists because something
went wrong without it. A session that does not follow them will produce work
that gets reverted.

**Current state lives in `docs/HANDOFF.md`. Read it second, after this file.**
History is `docs/notes/YYYY-MM.md`, append-only. Do not read a whole month
file to orient yourself — the handoff is the orientation.

## What this is

Benny512 is a Windows-hosted, LAN-accessible web instrument for
entertainment-lighting networks: a single `benny512.exe` serving a browser UI
with Art-Net/ArtRDM, RDM discovery and control, packet analysis, DMX output,
patch/reconcile, attribute-level Rig Check, browser-side MVR/GDTF import and a
persistent Fixture Library.

The operator is a lighting professional, not a programmer. These are working
instruments used in a dark venue, on a tablet, sometimes wearing gloves.

## Engineering rules

- **Root-cause culture.** Diagnose, do not patch around. A feature that does
  not work gets removed, not defended.
- **A test must fail against the unfixed code.** Capture the proof-of-failure
  output and put it in the commit message. A test that passes both ways proves
  nothing, and this project has shipped several.
- **Tests must exercise the real thing.** Load the literal JS the browser
  loads; feed real bytes to the real handler; count transactions at the
  responder. A test that builds its request from the same struct it asserts on
  proves only that one side agrees with itself — **twelve** recorded instances,
  this project's most-repeated defect.
- **Minimal, surgical changes. No refactoring of working systems.**
- **Zero external dependencies.** Hand-rolled WebSocket, hand-rolled ZIP
  reader, stdlib-only Go. Keep it that way.
- **Accuracy over assumption.** Verify against primary specs. Mark uncertainty
  plainly rather than presenting a guess as a reading. Ask clarifying questions
  before writing code.
- Never use `omitempty` on a numeric or boolean JSON field whose zero is
  meaningful. Initialise JSON slices with `make([]T, 0)`. A plausible zero is
  not absence.
- A plausible number must never stand in for "no answer". Refuse and say which
  value could not be produced; do not clamp.
- Server-generated user-facing text must not embed universe numbers. Display
  formatting belongs at the presentation boundary. The patch TXT export is the
  one documented exception.
- `SUPPORTED_PARAMETERS` gates speculative RDM reads. Missing support
  information means unknown/open, not unsupported. E1.20-required PIDs are
  never gated.

## Gates — run all of them, every time

```
gofmt -l .                                   # must be empty
go vet -buildvcs=false ./...
go build -buildvcs=false ./...
go test -buildvcs=false -count=1 ./...
go test -buildvcs=false -race -count=1 ./...
node --check <every browser script touched>
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false ./...
```

`-buildvcs=false` is required or the repo's `.git` trips a VCS-ownership check.

**Render-prove every JS/CSS change** in a real browser against a disposable
`--demo` build in a temp directory — never the installation's real show data.
Four shipped UI defects were caught this way that no Go test could see.

## Branches, versions, releases

- `main` is always releasable. Work happens on a short-lived branch per chunk:
  `fix/<thing>`, `feat/<thing>`, `docs/<thing>`, `chore/<thing>`.
- One chunk per branch, one PR. The PR body carries what changed, why, what was
  verified and how, and anything deliberately not done with the reason — the
  same content a project-notes entry carries.
- **Commit messages are load-bearing here.** State the root cause in plain
  words, cite the section of the primary text that governs it, and include the
  captured proof-of-failure lines.
- Versions are tags: `vMAJOR.MINOR.PATCH`. **CI builds the release**: pushing a
  tag runs the gate suite and, only if it passes, publishes a Release with the
  Windows executable and its SHA-256. **`dist/` is gitignored and binaries are
  never committed.**
- `.github/workflows/gates.yml` runs every gate above on each push and PR, so a
  broken build is caught before anyone runs it. It installs Node deliberately:
  a dozen test files `t.Skip` when node is absent, so a runner without it goes
  green having tested none of the browser seams. The workflow fails instead.
- Local test builds are unchanged and disposable:
  `go build -buildvcs=false -o dist\benny512.exe ./cmd/benny512`. Build,
  double-click, overwrite next time.
- The binary knows what it is: `benny512.exe --version` prints the tag, commit
  and build time, stamped by the release workflow's ldflags. A local build says
  `dev / unknown` rather than claiming a version it does not have.
- Never `git add -A`. Add by path. The tree routinely carries unrelated
  untracked state.

## Vocabulary — it matters on a call sheet

**ports**, not jacks. **BiDi**, not Bi. Power is **circuit** / **draw** / **W**.

## Accessibility — a standing constraint, not a nice-to-have

High contrast, dyslexia-friendly type, keyboard access, and **never colour as
the sole signal**. 44px touch targets on coarse pointers. Assume a dark venue,
a tablet, and gloves.

The **Apply-to-confirm** contract is explicit: anything that changes what is on
the wire stages first and commits on Apply. Identify, the Send/Rig Check faders
and the Rig Check test toggles are the signed-off exceptions.

## Documentation is part of the work, not a follow-up

Update `docs/HANDOFF.md` **in the same session as the change**, and append a
`docs/notes/YYYY-MM.md` entry. Capture the timestamp from the real clock
(`TZ=America/New_York date`) immediately before writing — never approximate one
from context. A decision that is not written down gets re-litigated or silently
reversed.

Durable calls — "we do X because the standard says Y" — go in
`docs/decisions/` as their own file, not buried in a month of notes.

## Files that are NOT in this repo

- **The runtime data directory** (`dist/`): the saved show, the Fixture
  Library, settings and the sACN CID. Real client show data; gitignored.
- **The ANSI/ESTA primary texts** (E1.20, E1.31, E1.37-2, Art-Net 4). Licensed
  documents — they live beside the repo on the owner's machine, not in it.
  Briefs should reference them by path, not commit them.
