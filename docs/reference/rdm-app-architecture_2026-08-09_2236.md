# Benny512 — Architecture (rev 5) — Windows-first pivot

**Date:** 2026-08-09 22:36
**Status:** Approved — Windows-primary, Go server + browser UI. Supersedes all prior revs.
**Supersedes:** `rdm-app-architecture_2026-08-04_2246.md` (iOS-first, Swift)
**Companions:** `lighting-protocols-reference_2026-08-03_0002.md` (protocol bytes) · `phase1a-wire-format-verification_2026-08-04_2305.md` (verified offsets + golden fixtures — still authoritative, language-independent)

---

## 1. What changed and why

Dom re-prioritized: **Windows app first, iPhone later.** Delivery model chosen: a **headless server + browser UI** — `benny512.exe` runs on one PC on the lighting network; any device's browser (the same PC, another laptop, a phone on the venue Wi-Fi) opens the UI over LAN. Consequences:

- **Language pivot: Go** (decision #10). Agents cross-compile a single self-contained `benny512.exe` from the Linux sandbox — Dom installs nothing, ever; each update is a new .exe. Go's standard library covers both halves of the app (UDP networking + HTTP server). The Swift implementation (Phase 1a, 113 green tests) is **archived, not deleted**, at `Benny512/archive-swift-v1/LightingKit` — it serves as the reference implementation the Go port must match byte-for-byte. The costly artifacts (wire-format verification report, golden fixtures, test design) are language-independent and carry over unchanged.
- **The iPhone "app" is deferred and may never need to be native:** the browser UI on a phone covers most use; a PWA wrapper or native shell is a Phase 6 option. Apple's multicast entitlement problem evaporates — the *server* touches the network, not the phone.
- **Wi-Fi vs Ethernet is a non-issue** (OS-level), but desktops have multiple NICs → **interface picker is a first-class Settings feature** (bind/broadcast interface selection).
- **DLux Rackmaster reviewed as reference** (decision #11). ⚠️ Per Dom: Rackmaster is **not fully tested or finished — inspiration only.** Its parser designs and field-earned knowledge (Vectorworks MVR quirks, CSV auto-mapping, merge-conflict UX) inform Benny512, but every borrowed idea is re-implemented and independently validated against specs and real files. No Rackmaster code is assumed correct, and nothing in the `DLux Rackmaster` folder is ever modified.

## 2. System shape

```
┌─ Dom's PC (Windows) ──────────────────────────────────────────┐
│  benny512.exe (single file, Go, web UI embedded via go:embed) │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │ core packages (ported from Swift, Linux-tested):        │  │
│  │   bytesio · rdm · artnet   (Layer 1: codecs)            │  │
│  │   sessions: artnetsession · rdmcontroller · dmxout      │  │
│  │   services: registry · params · capture · patch         │  │
│  ├─────────────────────────────────────────────────────────┤  │
│  │ transport: UDP broadcast/unicast :6454 (+multicast for  │  │
│  │   sACN later) — net.PacketConn, per-NIC binding         │  │
│  │ web: HTTP (REST commands) + WebSocket (live streams)    │  │
│  │   serving embedded single-page UI on :5812 (BENny→5812, │  │
│  │   configurable)                                         │  │
│  └─────────────────────────────────────────────────────────┘  │
└───────────────▲───────────────────────▲───────────────────────┘
                │ LAN (http://host:5812)│ UDP 6454 (Art-Net)
        any browser: same PC,       Netron EN4 ──DMX/RDM──▶ fixtures
        laptop, phone on venue      LumenRadio Aurora/Moonlite2
        Wi-Fi                       (wireless RDM proxies)
```

**Core rule carried over from rev 1 (unchanged in spirit):** protocol logic stays pure and platform-free — codecs and state machines never touch sockets or HTTP. They're plain Go packages tested with `go test` in the sandbox against the same golden fixtures. Transports and the web layer wrap around them. This is what made the Swift→Go pivot cheap; it's also what keeps a future native-anything cheap.

**Client/server split for imports:** MVR/GDTF/CSV parsing happens **in the browser** (JS, where Rackmaster's proven designs — JSZip, XML DOM, column auto-mapping — apply directly); the browser sends normalized patch JSON to the server. Server stays lean; heavy file wrangling happens on the machine holding the file.

## 3. Server internals (Go)

Module `benny512`, layout:

```
cmd/benny512/          main: flags, NIC enumeration, server startup, tray-free console log
internal/bytesio/      explicit-endianness readers/writers (LE/BE per field — Art-Net mixes
                       them WITHIN packets; see verification report)
internal/rdm/          UID, RDMMessage, checksum, CC/ResponseType/NACK/PID enums,
                       DUB encode/decode (OR 0xAA / OR 0x55, AND to decode — NOT XOR)
internal/artnet/       10 packet types incl. ArtPollReply's 10-byte header special case,
                       ArtTimeCode 19B/StreamId@13, ArtTodData BindIndex left unresolved
                       (raw spares retained; confirm vs real EN4 in Phase 1d)
internal/session/      artnetsession: ArtPoll cycle, node table, liveness
                       rdmcontroller: per-UID serialized transaction queue, TN matching,
                         ACK_TIMER (normal path for LumenRadio proxies), ACK_OVERFLOW
                         (same-GET repeat, no interleave), configurable timeout profiles
                         (Direct / Wireless-proxy), NACK decode, multi-block ToD assembly
                       dmxout: per-universe frame buffer, ~40 Hz ticker
internal/registry/     nodes + fixtures merged view; ESTA manufacturer table (embedded)
internal/params/       typed PID wrappers (DEVICE_INFO, DMX_START_ADDRESS, PERSONALITY,
                       IDENTIFY, sensors, labels, PROXIED_DEVICES/COUNT)
internal/capture/      bounded ring buffer of decoded packets; filter engine
internal/patch/        patch model, JSON persistence, collision detection, patch↔RDM
                       matcher (tiered: address → type corroboration → manual confirm),
                       rig-check sequencer (function-aware once GDTF data present)
internal/web/          HTTP handlers (REST), WebSocket hub (capture feed, node/fixture
                       state deltas, rig-check progress), go:embed static UI
web/                   the browser UI source (vanilla JS, no build step — Rackmaster-style)
```

- **Concurrency:** one goroutine per listener/session; state owned by single goroutines with channel commands (mirrors the actor design from the Swift plan). `rdmcontroller` serializes per-UID exactly as the RDM spec's interleaving rules require.
- **Dependencies:** stdlib + **one** approved third-party package: a WebSocket library (`coder/websocket`, MIT) unless hand-rolling proves trivial. ZIPFoundation is obsolete (ZIP handling moved to browser JSZip / Go stdlib `archive/zip` if ever server-side). Same "minimal, justified" rule as before.
- **Persistence:** JSON files beside the exe (`benny512-patch.json`, `benny512-settings.json`), tolerant readers, migrate-on-load (a Rackmaster lesson that IS trusted: it's process, not code).
- **Windows realities:** first-run firewall prompt (document for Dom); NIC enumeration via `net.Interfaces()` with friendly names; broadcast on the selected interface's subnet (directed broadcast), fallback 255.255.255.255.

## 4. Browser UI (embedded, vanilla JS)

Same six screens as rev 4 (Nodes · Fixtures/RDM · Patch · Analyzer · Send · Settings), now as a single-page app served by the exe. No frameworks, no build step — one HTML file + JS modules embedded in the binary; Rackmaster proved this style works at scale for Dom's needs (and its documented pitfalls — render-in-oninput focus loss, full-rerender scroll reset — are inherited as *rules*, adopted from day one, not relearned). Live data over WebSocket (analyzer feed throttled server-side to ~10–15 fps render batches); commands over REST. UI carries Dom's vocabulary conventions: **"ports," "BiDi," "circuit/draw/W."**

## 5. What Rackmaster contributes (inspiration register)

⚠️ All of it re-validated independently; none of it assumed working.

| Source idea | Benny512 use | Phase |
|---|---|---|
| MVR parser design (GeneralSceneDescription.xml, Position map, matrix→XYZ, absolute-address→universe/address, `extractFixtureField` Vectorworks quirks) | Browser-side MVR importer | 2b |
| GDTF parser design (description.xml, mode→footprint, geometry power walk) — **extend with `<LogicalChannel>`/`<ChannelFunction>` parsing** (Rackmaster never did channel functions) | Browser-side GDTF importer → function-aware rig check | 2b |
| CSV auto-mapping (synonym header detection, adjustable mapping table, MVR-vs-CSV merge plan with per-field conflict resolution) | CSV importer + patch merge UX | 2a |
| Rig-check validations (universe overlap math, >512 overflow, duplicate IP) | Patch validation | 2a |
| `.dlux.json` import: `lighting.state.instances[]` → patch; `fixtures[]` → fixture types; devices with IP fields → **expected-node list** | Patch import + **"expected vs actual network" diff** (Rackmaster says EN4 at 2.11.90.2 / ArtPoll saw…) | 2c |
| GDTF Share API endpoints (documented in `GDTF_Share_CORS_test.html`) — CORS blocked Rackmaster; a hosted server is a native client, so it's feasible here | Live GDTF Share fixture-profile fetch, server-side | 5 |
| Process lessons: migrate-on-load, real-project validation files, one test file per feature area, timestamped build versioning, two-doc convention (append-only Project Notes + current-state handoff brief) | Adopted for Benny512 outright | now |

## 6. Testing strategy (updated for Go)

Unchanged in substance from rev 4 — golden fixtures (same hex, from the verification report), round-trip property tests, checksum/malformed/fuzz (Go's native fuzzing), scripted-transport state-machine tests (fake `PacketConn`), all run via `go test ./...` in the sandbox. **Port acceptance gate (Phase 1a-Go): the Go codecs must pass the identical fixture set that the archived Swift suite passes — byte-exact.** Added layers: `httptest`-driven REST/WebSocket tests; browser-importer tests run under Node (Rackmaster's jsdom lesson applies: JSZip async hangs in jsdom — test parse logic against hand-built DOM, not full zips). On-hardware checklists (1d, 2) unchanged: EN4 direct, then behind Aurora, then Moonlite2; deliberate mis-address caught and reconciled.

## 7. Phasing (reset)

| Phase | Deliverable | Notes |
|---|---|---|
| **1a-Go** | Port Layer 1 (bytesio, rdm, artnet) + full test suite; byte-equivalence vs golden fixtures | **← current.** Swift archived as reference. |
| **1b** | session layer: artnetsession, rdmcontroller, dmxout + state-machine tests | Sandbox-verified. |
| **1c** | web layer + six-screen UI + NIC picker; first `benny512.exe` cross-compiled and run by Dom | First on-PC milestone — days away from 1a, not weeks. |
| **1d** | Hardware shakeout on the rig (EN4 + Aurora + Moonlite2); ArtTodData BindIndex resolved from real captures | |
| **2a** | Patch model + CSV import + channel-level rig check + patch↔RDM reconcile | |
| **2b** | MVR + GDTF import incl. channel functions → function-aware rig check | |
| **2c** | `.dlux.json` import + expected-vs-actual network diff | |
| **3** | sACN receive/analyze/send + ArtTimeCode + universe discovery | No entitlement drama on Windows. |
| **4** | RDMnet (LLRP doctor, mDNS broker browse, RPT) | |
| **5** | GDTF Share live fetch, capture export (pcap/JSON), E1.37-x PIDs, ArtRdmSub | |
| **6** | iPhone/touch pass: PWA wrapper or native shell over the same server | Decide when we get there. |

**Working model unchanged:** Fable plans/briefs/reviews/gates; agents implement; Dom decides and now just double-clicks the exe.

## 8. Decision record (cumulative)

| # | Decision |
|---|---|
| 1 | Hardware: Netron EN4 primary; LumenRadio Aurora + Moonlite2 (wireless RDM proxies) |
| 2 | ~~iPhone-first~~ → **superseded by #10** |
| 3 | ~~iOS 17+~~ → moot for now (Phase 6) |
| 4 | Name: **Benny512** (Benny the Maine-Coon-coated cat + DMX512) |
| 5 | Apple dev account via coworker → deferred to Phase 6 with iPhone |
| 6 | Patch/rig-check/reconcile in scope (Phase 2a) |
| 7 | CSV + MVR/GDTF import incl. channel functions; 3D data ignored |
| 8 | ~~ZIPFoundation~~ → obsolete; ZIP handled by browser JSZip / Go stdlib. Approved dep now: one WebSocket lib (`coder/websocket`, MIT) if needed |
| 9 | Patch↔RDM matching: tiered heuristic (address → type → manual w/ Identify), UIDs absent from MVR by design |
| 10 | **Windows-primary. Go server + embedded browser UI, LAN-accessible. Single-exe delivery, cross-compiled by agents. Swift Phase 1a archived at `Benny512/archive-swift-v1/` as byte-equivalence reference.** |
| 11 | **Rackmaster = inspiration only** (Dom: not fully tested/finished). Designs borrowed, code re-validated; folder is read-only reference. |
| 12 | UI server port default **5812**, configurable |
