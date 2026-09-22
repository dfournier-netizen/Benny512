# Benny512 — Hardware Test Checklist

Generated 2026-08-16 22:37. Bench-usable on a phone: each item is a
do/look-for/capture/means block. Work top to bottom by priority. Don't skip
the setup section — several items only surface at the second or third
topology.

Companion doc: `Benny512_handoff_2026-08-14_2217.md` (§9 has the same open
questions in narrative form; this file is the checklist version, kept in
sync — if you resolve something here, update both).

---

## 0. Bench setup (do this once, in this order)

1. **Topology A — direct.** Plug fixture(s) straight into an Obsidian
   Netron EN4 port. This is the baseline: no wireless proxy in the path.
2. **Topology B — behind Aurora.** Move the same fixture(s) onto a
   LumenRadio Aurora CRMX transmitter fed by an EN4 port.
   - **RDM proxy must be enabled on the Aurora unit itself** (its own
     config, not Benny512) or downstream fixtures will not discover at
     all. Check this first if ToD comes back empty.
3. **Topology C — behind MoonLite2.** Same fixture(s), same EN4 port, now
   through a LumenRadio MoonLite2 instead of the Aurora.
   - Same RDM-proxy-enabled requirement as step 2.
4. At each topology: run Discover, then Identify every discovered device,
   confirming physical fixture identifies. Expect ACK_TIMER responses to be
   the **normal** path through B and C, not an edge case — don't treat a
   deferred/ACK_TIMER response through the wireless proxies as a bug by
   itself.
5. Turn on continuous RDM logging (Settings tab → "Continuous RDM log
   file") for the whole session before starting. It captures every
   RDM/ToD exchange to disk as it happens — you want this running before
   you start clicking, not after something looks wrong.

---

## PRIORITY 1 — blocks trusting anything the app reports

### 1.1 ACK_TIMER time unit (10 ms vs 1 ms)
- **What**: E1.20 spec says ACK_TIMER's Estimated Response Time field is in
  10 ms increments. An earlier reference doc this project used said
  milliseconds. Benny512 currently defaults `RDMConfig.AckTimerUnit` to
  10 ms. A wrong unit is a 10x error — worst case on the wireless rig
  (Topology B/C) where ACK_TIMER is the normal path.
- **Look for**: On the Analyzer → RDM tab, find an ACK_TIMER response
  (paired request/response view). Note the Estimated Response Time value
  Benny512 decoded, then time how long the fixture actually takes to
  respond with the real answer (stopwatch or RTT column).
- **Capture**: Screenshot of the ACK_TIMER row with its decoded wait time,
  plus the RTT of the follow-up GET that actually got the ACK. Export the
  RDM buffer (JSON) for that window.
- **Means**: If decoded wait ≈ actual wait, 10 ms is correct, done. If
  actual wait is ~10x the decoded value, the unit is 1 ms and
  `AckTimerUnit` needs to flip.

### 1.2 Art-Net 4 RDM status bits (Status1 bit 1, Status3 bit 5, GoodOutputB bit 7)
- **What**: These bits in ArtPollReply are decoded best-effort — meaning
  matters for showing "this node supports RDM" / "this port is RDM
  capable" correctly.
- **Look for**: Nodes tab, each node's RDM column and per-port RDM
  indicator. Cross-check against the EN4's own web UI or documentation for
  whether RDM is actually enabled on that port.
- **Capture**: Screenshot of Nodes tab RDM column next to EN4's own admin
  page RDM setting for the same port. Export an ArtPollReply capture
  (Analyzer → All traffic, filter ArtPollReply) showing the raw bytes.
- **Means**: If Benny512's RDM indicator matches the EN4's actual RDM
  state on every port across all 4 ports, the bit reads are confirmed.
  Any mismatch — note which bit, which port, which direction (false
  positive vs false negative).

### 1.3 Obsidian's real ESTA manufacturer ID (0x1900 vs 0x22A6)
- **What**: Demo/static tables currently assume `0x1900` (ADJ's ID) for
  Obsidian gear; `0x22A6` is Elation's actual ID. This affects the
  manufacturer-name fallback shown anywhere Benny512 doesn't have a live
  MANUFACTURER_LABEL from the device.
- **Look for**: Devices tab → select an Obsidian-branded device (EN4 root
  device, if it answers RDM at all) → look at the raw UID's manufacturer
  prefix in the detail panel, and separately fetch MANUFACTURER_LABEL live.
- **Capture**: Screenshot of the device detail panel showing full UID hex
  and the live MANUFACTURER_LABEL text.
- **Means**: Compare the UID's top 2 bytes against 0x1900 and 0x22A6.
  Whichever it actually is, update the static ESTA table entry for
  Obsidian. This also feeds Phase 2a's fuzzy patch matcher (see item 3.1
  below) — a wrong static fallback name would show up as a spurious
  "Unpatched" or a failed corroboration match.

### 1.4 internal/artnet/nodeconfig.go — all three packets, wholesale
- **What**: ArtAddress (0x6000), ArtIpProg (0xf800/0xf900), ArtInput
  (0x7000) were implemented from general Art-Net 4 recollection, not a
  primary spec fetch or byte capture. Every byte offset, the 107-byte
  ArtAddress total length, the AcCommand enum values (0x00-0x63), the
  SwitchEntry bit-7-as-write-enable convention, and both ArtIpProg's
  34-byte layout and its Art-Net-4-vs-base-spec Command bit meaning are
  all unverified. This is the single largest unverified surface in the
  codebase.
- **Do**: On Topology A (EN4 direct — this is remote node config, wireless
  proxies are irrelevant), from Nodes tab detail panel:
  1. Rename a port's short name and long name via Benny512, apply, then
     re-poll and confirm the EN4 actually shows the new name (on its own
     admin UI if it has one, or via a fresh ArtPollReply capture).
  2. Change one port's universe/sub-net/net via Benny512, confirm the EN4
     actually moved that port's universe (test by sending DMX and watching
     the fixture on the new universe).
  3. Try AcLedLocate / AcLedNormal, confirm the EN4's status LED actually
     changes behavior.
  4. If the EN4 exposes any IP-change controls, try ArtIpProg (low risk:
     do this LAST and be ready to power-cycle/hard-reset the EN4 if it
     goes unreachable — some Art-Net nodes require a specific "Enable
     Programming" flag pattern that this implementation may not send
     correctly).
- **Capture**: Packet capture (Analyzer export, JSON) of each request and
  the EN4's actual reply for all four sub-tests above. If you have
  Wireshark or similar available independently, a raw pcap of the same
  exchange is even better corroboration.
- **Means**: Any of the four sub-tests failing to take effect on the EN4,
  or the EN4 returning a NACK/malformed reply, pinpoints exactly which
  packet/offset is wrong — note which sub-test failed and paste the raw
  bytes from the capture into the report back to the architect.

---

## PRIORITY 2 — needed before Phase 1d's E1.37 features are trustworthy

### 2.1 E1.37-2 IP-config wire shape (internal/params/ipconfig.go)
- **What**: LIST_INTERFACES, and the IPv4/DNS get/set PIDs, are decoded
  using "interface-ID-prefixed request/response" shape modeled on OLA's
  e137_2 responder convention — not verified against ANSI/ESTA E1.37-2
  primary text or any real device. Also unverified: whether the EN4's own
  root device implements E1.37-2 at all.
- **Do**: Devices tab → EN4 root device (if RDM-capable) → try
  LIST_INTERFACES first (read-only, safe). If it returns data, try the
  DNS/IPv4 GET PIDs. Do NOT attempt SET on IP-config PIDs against
  live-network hardware unless you're prepared to lose reachability to
  that device.
- **Capture**: Raw hex of the LIST_INTERFACES response, and screenshot of
  however Benny512 renders it (typed view if recognized, hex fallback via
  Introspect/GetParam otherwise per report §5.1's DS_* fallback path).
- **Means**: If LIST_INTERFACES NACKs outright, the EN4 doesn't implement
  E1.37-2 and this whole section is moot for this device — note that and
  move on. If it ACKs with data, compare the byte count/shape against what
  ipconfig.go expects (4-byte interface IDs, flat array) — a length
  mismatch means the interface-ID-prefix assumption is wrong.

### 2.2 E1.37-1 dimmer PID layouts (internal/rdm/dimmer.go)
- **What**: CURVE / OUTPUT_RESPONSE_TIME / MODULATION_FREQUENCY (index +
  count GET-response, single-byte SET), their *_DESCRIPTION siblings
  (echoed index + ASCII label), MINIMUM_LEVEL (5-byte hysteresis shape),
  MAXIMUM_LEVEL (2-byte scalar), and IDENTIFY_MODE (1-byte Loud/Quiet
  enum, 0x00/0x01 assignment unconfirmed) are all best-reading
  implementations — the research report only gave PID numbers, not byte
  layouts, for this whole family.
- **Do**: This needs an actual dimmer/dimmable fixture that supports
  E1.37-1 (the Chroma-Q Color Force 48 is the most likely candidate in
  Dom's kit if it's dimmer-class). Devices tab → select it → try each of
  CURVE, OUTPUT_RESPONSE_TIME, MODULATION_FREQUENCY, MINIMUM_LEVEL,
  MAXIMUM_LEVEL, IDENTIFY_MODE from the PID editor, GET first.
- **Capture**: Raw response bytes for each PID (Analyzer RDM view, export
  JSON) alongside what Benny512 decoded it as.
- **Means**: A clean decode (no `ErrBadDimmerLength`) with sane-looking
  values (e.g. MINIMUM_LEVEL's 5 bytes forming a plausible
  rising/falling/on-below-min triple) is weak positive evidence, not
  proof — decoders fail closed on wrong length but a coincidentally
  correct length with wrong field meaning would decode "successfully" and
  still be wrong. Cross-check against the fixture's own display/manual if
  it exposes curve/response-time settings physically. Any
  `ErrBadDimmerLength` is definitive proof the shape is wrong — capture
  the exact byte count returned.

### 2.3 ArtTodData BindIndex offset
- **What**: Genuinely unresolved in the spec sources this project had
  access to. Codec currently retains the raw spare bytes rather than
  guessing at a BindIndex field position.
- **Do**: Capture an ArtTodData packet from a node with more than one bind
  (a node presenting sub-devices/multiple root UIDs, if any device in
  Dom's kit does this — the EN4 with all 4 ports active discovering
  devices is the most likely candidate).
- **Capture**: Raw ArtTodData bytes (Analyzer export) with the spare-byte
  region highlighted/noted.
- **Means**: If a byte in the spare region varies in a way that
  correlates with which port/bind produced the ToD, that's the
  BindIndex — note its offset and value pattern. If Dom's kit has no
  multi-bind node, this can't be resolved on this bench pass; say so
  explicitly rather than guessing.

---

## PRIORITY 3 — Phase 2a (patch/reconcile) — new this round

### 3.1 Do real devices' footprint/model strings match patch entries well enough for Tier-2 fuzzy matching?
- **What**: This is Phase 2a's own new unknown. The patch↔RDM matcher
  scores a candidate pair on address (0.40) plus type corroboration:
  footprint (0.20), manufacturer (0.15), model (0.25), fuzzy
  token-containment on manufacturer/model text. The demo data was hand-
  tuned to avoid accidental token overlaps between fixture types (see
  handoff/session notes on the demo patch iteration) — real hardware's
  actual MANUFACTURER_LABEL/DEVICE_MODEL_DESCRIPTION strings may not be
  as clean, or may collide in ways the demo didn't surface.
- **Do**: On any topology, once several real fixtures are discovered:
  1. Devices tab → note down each device's live MANUFACTURER_LABEL and
     DEVICE_MODEL_DESCRIPTION text exactly as reported (these only
     populate after Benny512 has actually issued the GETs — browse each
     device's detail panel once to warm the cache, same as
     `warmDemoDeviceCaches` does for --demo).
  2. Patch tab → build patch entries for those same fixtures using
     Fixture Type text that's a reasonable human description (e.g. what
     you'd naturally type from a rental house paperwork, not necessarily
     copy-pasted from the device's own label).
  3. Run Reconcile. Check: does a correctly-patched, correctly-addressed
     fixture come back Matched with confidence near 1.0? Does an
     intentionally-mis-addressed one come back AddressMismatch (not
     Missing) — i.e. did corroboration alone clear the 0.25
     proposedThreshold?
  4. Try two fixtures of the identical model/type, deliberately swap their
     patched addresses (or leave one unaddressed vs the other addressed).
     Confirm Reconcile flags Ambiguous rather than silently guessing wrong,
     when corroboration can't disambiguate address alone.
- **Capture**: Screenshot of the Reconcile view showing the resulting
  classifications, plus the raw MANUFACTURER_LABEL/DEVICE_MODEL_DESCRIPTION
  text noted in step 1 (so a scoring miss can be diagnosed against exact
  wording later).
- **Means**: If real fixture-type text routinely fails to fuzzy-match its
  own device's labels (i.e. legitimately-matched pairs coming back Missing
  or low-confidence Unpatched instead of Matched/AddressMismatch), the
  token-containment approach or the weight tuning needs revisiting —
  capture the exact strings involved so the architect can see what broke
  the match instead of re-guessing.

---

## PRIORITY 4 — lower risk / judgment calls to sanity-check, not re-derive

These ("1b judgment calls") were made as reasonable engineering choices
rather than spec-mandated behavior. They probably don't need bytes-level
verification, just a sanity pass that nothing on real hardware violates
the assumption:

- **Retransmit TN reuse**: retransmits during an ACK_TIMER wait reuse the
  original Transaction Number; overflow continuations get fresh TNs. Watch
  the Analyzer TN column during any ACK_TIMER sequence (expect through
  Topology B/C) — confirm the retry's TN matches the original request, and
  that a subsequent GET_COMMAND for overflow continuation (ACK_OVERFLOW)
  gets a new TN.
- **Overflow hold scope**: ACK_OVERFLOW hold is treated as both per-UID
  and global. If two different devices are mid-overflow-drain
  simultaneously, confirm Benny512 doesn't stall one waiting on the other.
- **Broadcast completion**: broadcast RDM commands (e.g. blackout/rig
  check's use of SET commands, if any go broadcast) complete immediately
  as `ResultBroadcast` rather than waiting for a response. Confirm nothing
  in the UI hangs waiting on a response to a broadcast SET.
- **ToD keying**: ToD is keyed by (IP, PortAddress), not BindIndex, and
  considered complete when the deduped UID count matches UidTotal, not
  when BlockCount is exhausted. If a node has multiple binds sharing one
  PortAddress (see item 2.3 above — same root uncertainty), confirm ToD
  discovery still terminates correctly and doesn't undercount/overcount
  UIDs.

---

## Send these files to the architect

After the bench session, regardless of outcome (confirmed or busted),
send:

- The continuous RDM log file (from Settings → log path), covering the
  whole session across all three topologies.
- Analyzer exports (JSON preferred over TXT — keeps raw bytes) for every
  capture called out above: ACK_TIMER (1.1), ArtPollReply (1.2), device
  detail / UID (1.3), all four nodeconfig.go sub-tests (1.4),
  LIST_INTERFACES (2.1), each dimmer PID (2.2), any multi-bind ArtTodData
  (2.3), and the Reconcile screenshots + label text (3.1).
- A one-line pass/fail per numbered item above (1.1 through 3.1) — even
  "couldn't test, no multi-bind node in the kit" is useful, don't leave
  items silently blank.
- If any of the nodeconfig.go sub-tests (1.4) or the E1.37-2 SET path
  (2.1) caused a device to become unreachable or need a manual reset,
  note that explicitly and separately — those are the two highest-risk
  live-hardware actions on this checklist.
