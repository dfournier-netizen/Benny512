# ERA 800 investigation: independent audit of LOG29–31

Analysis performed 2026-09-16/17, America/New_York. This supersedes the specific conclusions corrected below, not the historical logs. Application source, runtime data and lighting output were not changed.

## Finding

The failing path is highly localized to `4D50:001158FE` at DMX address 1 on `2.11.90.1`, raw Art-Net Port-Address 21. The captures do not establish whether the root cause is that fixture, its electrical connection, a same-line collision, or the gateway's handling of it. A changing Table of Devices is not proof that the fixture transmits multiple identities.

## Raw-byte verification and corrections

`analyze_era.py` independently extracts destination/source UIDs, PID, transaction number, response type, parameter data and checksums from the recorded hex. Run `python docs/evidence/analysis/analyze_era.py` from the project root. Counts below include retransmissions, not just logical requests.

| Capture | Suspect outgoing / replies | DEVICE_INFO outgoing / ACKs | Other 15 ERAs outgoing / replies |
| --- | ---: | ---: | ---: |
| LOG29 | 64 / 6 | 12 / 0 | 75 / 75 |
| LOG30 | 63 / 2 | 12 / 0 | 168 / 168 |
| LOG31 | 28 / 3 | 4 / 1 | 80 / 80 |

**LOG31 has ONE DEVICE_INFO ACK, not three.** Its three replies are:

- Line 545, 17:19:51.007: PRODUCT_DETAIL_ID_LIST, UNKNOWN_PID NACK, TN 10.
- Line 2018, 17:21:16.875: DEVICE_INFO ACK, TN 84.
- Line 2035, 17:21:16.879: PRODUCT_DETAIL_ID_LIST, UNKNOWN_PID NACK, TN 85.

The DEVICE_INFO payload is `0100009b0509000000f0002a01010001000001`: protocol 1.0, model 0x009B, category 0x0509, software ID 0x000000F0, footprint 42, personality 1/1, address 1, zero subdevices, one sensor. Thus model and software ID are now known and match the healthy ERAs. The earlier statement that these remain unreadable is obsolete. Equal software IDs do not prove byte-identical firmware installations or healthy hardware.

Across all three captures the suspect has 155 outgoing packets and 11 replies: only three ACKs (two manufacturer labels and one DEVICE_INFO), plus eight NACKs. It answered one of 28 DEVICE_INFO transmissions. LOG31 demonstrates intermittent reachability, not recovery.

Every suspect reply has a valid recomputed checksum and matches a preceding request's PID/TN/UID. Replies arrive 4–30 ms after the latest matching request, comfortably inside the 1.5-second timeout. There is no evidence here for fixing this by lengthening Benny512's timeout. The single DEVICE_INFO success was on the first transmission of TN 84; the following NACK arrived in 4 ms, then the next PID was silent through retries. This is not simply a uniformly slow responder.

All fifteen other ERAs have matching aggregate request/reply counts: 323 / 323. Those totals support a sharply localized fault; they do not prove every possible PID or every live condition is healthy.

## What the phantom UID pattern does and does not establish

The raw ArtTodData bytes themselves contain the extras (LOG29 line 36, LOG30 line 26, LOG31 line 316), so they are not solely UI duplicates or a registry BindIndex problem. All three tables contain eleven Martin UIDs. LOG31 replaces `001159BE` with `0011593E`, while `0011597E` and `001159FE` persist. None of those four extra identities replies in these captures.

The four extra values across the captures are unusually structured:

```
0011593E   last byte 00111110
0011597E   last byte 01111110
001159BE   last byte 10111110
001159FE   last byte 11111110
```

They share every bit except the upper two bits of the last byte. This is a useful diagnostic signature to preserve, consistent with a systematic discovery/decoding problem rather than unrelated removed fixtures. It does NOT identify which component produced it or prove a particular bit-corruption mechanism. Do not merge these UIDs in software based on resemblance.

The supplied **ANSI E1.20-2025, section 7.3, PDF page 49** explicitly describes overlapping discovery responses appearing to come from a nonexistent device. Its discovery procedure follows a plausible response with a directed DISC_MUTE exchange. Section 3.3 likewise explains that some discovery collisions can resemble valid packets.

Consequently these earlier claims are unsupported:

- A new phantom UID proves a responder has changing UIDs. It also fits fresh false discovery results or gateway table/decoder errors.
- A UID in ArtTodData proves successful DISC_UNIQUE_BRANCH AND DISC_MUTE. These captures contain the gateway's table, not evidence of its underlying serial exchanges.
- No malformed ArtRdm means no collisions on the DMX line. A gateway can discard a damaged serial response and send no ArtRdm at all; discovery has its own response format. Ethernet checksum validity only establishes integrity of the encapsulated message that reached this capture.
- Several UIDs on an isolated fixture through the same EN4 proves a Martin defect. The EN4, cable and receiver are still in that experiment.
- A responder with several identities necessarily collides with itself. Multiple identities alone do not establish simultaneous electrical transmissions.

The checksum failures in LOG29 are two ArtRdm packets; the three other malformed records are ToD decoding failures. LOG30/31 have no raw ArtRdm checksum failures. None of this resolves serial-side integrity on the ERA run.

## Request storm: additional limit on the cache explanation

LOG31's `0011593E` really does receive 374 requests, including 233 DEVICE_INFO transmissions. However, its traffic contains **25 distinct PIDs**, not merely the five in the retired unknown-device classifier. Examples include software label, device label/personality, dimmer configuration, status, hours, lamp state, power state and preset playback. Current committed `devicedetail.js` has Info/Parameters/service-life/action paths that request these families. The cached-classifier hypothesis can explain the repeating five-PID subset, but does not by itself explain the entire workload or its initiation.

The packet log does not identify HTTP callers, selected tabs, browser code version, or operator actions. Keep the hard-reload experiment, but do not treat it as a proven explanation of every request. To attribute the storm, record HTTP request provenance or reproduce with all browser tabs closed, then one freshly loaded Devices tab, then a selected silent fixture. No reproduction was attempted on the lighting network.

## Next bench experiment

Use a short known-good cable and correct termination, one physical fixture at a time, and one discovery controller. Record the actual UID from the fixture's own display if available.

1. Address-1 suspect on a known-good port/controller (preferably independent of the EN4). Repeat discovery and DEVICE_INFO reads.
2. A healthy ERA on the same cable and same test port as a control.
3. Suspect and control separately on the original `.90.1` port, keeping the test cable unchanged.
4. Rediscover the original run with the suspect removed; if single-fixture tests are clean, add fixtures back incrementally.

Failure following the suspect across independent controllers strongly implicates the fixture. Failure following the original port with a healthy control implicates that port/gateway. Failure only on the assembled run points toward topology, cable, termination or responder interaction. Multiple UIDs through one gateway remains ambiguous until cross-checked. A serial-side RDM analyzer capturing DISC_UNIQUE_BRANCH and DISC_MUTE is the definitive evidence missing from these Ethernet logs.

Separately, Netron's official V3 release notes list RDM/DMX interleaving and Art-Net RDM fixes (NETRON-11/80): https://forum.obsidiancontrol.com/t/netron-v3-firmware/8463 . This is a reason to record the gateway's exact firmware during comparison, not evidence those fixes explain this fault or an instruction to update live hardware.

The earlier profile/default/universe questions remain separate. These captures do not record Rig Check DMX output and cannot establish its emitted levels or explain the dimmer flash.

## Workspace boundary

The repository HEAD was `981ed63`. Existing uncommitted changes were present in `internal/params/coreidentity.go`, `introspect.go`, `params.go`, and `probecachesilence_test.go`; they were left untouched. Source comparisons for the historical build used committed files where those working files differed. No build, app test pass, or hardware verification is claimed.
