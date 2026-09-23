# ERA discovery mechanism analysis — September 17, 2026

## Revised working interpretation

The strongest working hypothesis is a discovery/validation problem triggered by the suspect's participation, possibly including lost/failed DISC_MUTE exchanges or inappropriate discovery responses. This is more precise than saying the fixture has four identities. LOG34 shows that the multiplicity varies, and the extra UIDs have no recorded directed RDM replies. Their presence in ArtTodData proves gateway advertisement, not a successful serial exchange with an independent responder.

The user reports LOG33 discovery during the suspect's power cycle and LOG34 discovery after reset. We observe seven healthy identities during LOG33 and a return of false entries in LOG34. Power-state timing is operator context; the capture does not contain power telemetry or serial discovery packets.

## A concrete mechanism can generate the persistent false UID

Under a deliberately simplified, bit-aligned OR model of received discovery data:

```
4D50:001158FE  (suspect with intermittent directed replies)
4D50:00115918  (real, healthy ERA on the same line; DMX address 169)
------------- bitwise OR
4D50:001159FE  (extra present in every affected discovery)
```

This is not merely an OR of the printed UIDs. Encoding both complete discovery bodies according to ANSI E1.20-2025, ORing their encoded UID AND encoded checksum bytes, and then decoding produces a byte-for-byte valid discovery body for 4D50:001159FE.

The discovery checksum is the sum of the twelve masked UID bytes (Table 7-1), equivalently 6*255 plus the sum of the six UID bytes for canonical encoding:

| UID | Correct discovery checksum |
| --- | --- |
| 4D50:001158FE | 0x07FE |
| 4D50:00115918 | 0x0719 |
| 4D50:001159FE | 0x07FF |

Here 0x07FE OR 0x0719 = 0x07FF, and the actual encoded-byte calculation passes too. The same result is possible with the suspect plus healthy 0011591C (address 85), or 00115940 (address 253).

**Limits:** RS-485 collisions are analog events, not guaranteed Boolean OR operations. Timing, receiver thresholds, drivers and framing matter. This calculation supplies one possible received pattern, not evidence that the gateway actually received it. Moreover, healthy 001158EA plus healthy 0011591C can mathematically yield the same false UID; the arithmetic alone is not unique evidence against the suspect. The power-cycle comparison is what links the observed anomaly to the suspect's presence.

An exhaustive check of every 2-8 member subset of the eight real-UID candidates, using aligned OR and AND, finds 18 ways to obtain 001159FE; all pass the encoded checksum. It finds **zero ways** to obtain 0011593E, 0011597E, 001159BE, 001159BC or 001159FC. Therefore simple aligned combination of otherwise correct replies cannot explain the whole observed family. Timing/framing corruption, noncanonical responses, range/mute faults, or gateway implementation behavior remain open.

## Why the one-bit observation alone was insufficient

Changing 001158FE to 001159FE changes the discovery checksum from 0x07FE to 0x07FF. A single UID-bit flip while leaving the checksum intact fails validation. Thus a claim of "one stuck UID bit" is incomplete unless it also explains the checksum change or a decoder accepting an invalid checksum.

Also, healthy 001158FF and phantom 001159FE have the same additive discovery checksum (0x07FF). This is another illustration of checksum ambiguity, not evidence that 001158FF is the culprit.

## The decisive missing exchange is DISC_MUTE

The standard's discovery procedure follows a plausible discovery response with a directed DISC_MUTE request. A real responder should acknowledge it and stop answering further DISC_UNIQUE_BRANCH searches. The standard explicitly allows collision conditions to create apparent identities that do not exist. The Open Lighting implementation guidance likewise adds a discovered UID after successful mute and describes lost mute, failure to mute and out-of-range responses as separate failure modes.

That makes the useful next question: **When the gateway thinks it has found 001159FE, what happens on its directed DISC_MUTE?** No such serial exchange is visible in these Art-Net captures. All of these remain distinguishable possibilities:

- No mute reply, but the gateway retains the apparent identity.
- A corrupted, stale or misattributed mute reply is accepted.
- A bad responder genuinely answers/matches an alias or mishandles discovery ranges/mute state.
- A proxy or other discovery implementation defect creates the table entry.

A failed mute does not by itself explain every UID variant. Nor does mere participation in normal discovery collisions imply that the suspect is faulty: collisions are expected and must be resolved. Its uniquely poor directed response rate is independent supporting evidence of a localized fault or interoperability problem.

## Tests that distinguish mechanisms

1. Independently read the suspect's UID from its display/label if available. Current identification of 001158FE rests on its valid directed replies; a physical check anchors the identity separately from the gateway.
2. On a short known-good, terminated run, discover **the suspect alone** on the original EN4. If extras persist, collisions between distinct fixtures cannot explain them. Fixture-generated corruption, gateway decoding and the electrical path remain possible.
3. If solo discovery is clean, add a known-good fixture, preferably 00115918 for the concrete calculation above, and repeat; compare with the healthy control alone. This tests whether multi-fixture interaction is required. It does not assume OR behavior must occur or predict every ghost.
4. Repeat suspect/control solo tests through an independent controller to localize fixture versus original gateway/path.
5. For a conclusive protocol trace, capture serial DISC_UNIQUE_BRANCH bounds/responses plus DISC_MUTE requests/ACKs, including source UID and transaction matching. Specifically check whether false UIDs are added without a valid matching mute ACK, whether the suspect responds outside its range, and whether it continues replying after a valid mute.

## Separate local code finding — not the source of these tables

`Benny512/internal/rdm/codec.go`, dubChecksum, sums only the six decoded UID bytes. E1.20 Table 7-1 requires the sum of the twelve encoded UID bytes. DecodeDUBResponse also recomputes from the recovered UID rather than the received EUID. The canonical encoded/decoded checksum differs by 0x05FA. Existing round-trip tests can agree with the same incorrect convention.

A repository search finds these DUB encode/decode helpers called only by tests/fuzz tests, not the live Art-Net discovery path. The affected gateway tables already contain the extras in raw bytes. This is a real, separate helper defect to fix before relying on native serial DUB handling, but changing it cannot repair the EN4 tables seen here. No application code was changed in this investigation.

## Sources and reproduction

- Supplied `ANSI_E1.20_2025.pdf`, section 7.3 (PDF page 49), Tables 7-1/7-2 (PDF pages 51-52), and section 7.6.3 (PDF pages 53-54).
- Open Lighting Project, RDM Discovery: https://wiki.openlighting.org/index.php/RDM_Discovery . Used for implementation/failure-mode cross-check, not as a replacement for the standard.
- LOG29-34 raw ArtTodData and ArtRdm captures; the earlier investigation reports contain the per-capture evidence.
- Run `python docs/evidence/analysis/analyze_era_discovery.py` for the exact encoded-byte example, exhaustive model limits, and single-bit/checksum check. These are offline arithmetic experiments, not hardware reproduction.
