# ERA investigation — LOG32, September 17, 2026

Expected physical inventory, confirmed by user: **16 ERAs**. Capture window: 07:10:39.987–07:16:28.258. Analysis is offline; no application source, runtime configuration, or lighting output changed.

## Count reconciliation

| Path (raw Art-Net Port-Address) | Physical ERAs | Advertised UIDs | UIDs answering in LOG32 |
| --- | ---: | ---: | ---: |
| 2.11.90.1 / 21 | 8 | 11 | 7 |
| 2.11.90.6 / 11 | 8 | 8 | 8 |
| Total | 16 | 19 | 15 |

Thus 15 working fixtures + one suspect physical fixture correspond to 15 answering identities + four silent advertised identities. The excess is **three**, not four. Four silent identities do not mean four failed physical fixtures.

At LOG32 line 311, the raw ArtTodData includes suspect 4D50:001158FE and extras 4D50:0011597E, 4D50:001159BE, 4D50:001159FE. This table arrives 8.850 seconds after the port's AtcFlush at line 306. The extra UIDs already exist in gateway-supplied bytes; UI duplication cannot explain their creation. A flush request does not itself establish what happened in serial discovery.

## New evidence

1. **The suspect is completely silent this time:** 18 requests, zero replies (nine DEVICE_INFO transmissions). Prior captures had intermittent answers: LOG29 6/64, LOG30 2/63, LOG31 3/28. This capture contains no new identity payload, fault code, or successful recovery from it.
2. **Every other real ERA answers:** 107 requests, 107 replies across 15 UIDs. Each request was matched to a reply by peer, source/destination UID, TN and PID within 1.5 seconds; observed latency is 3–35 ms. The seven working ERAs on the suspect's own port answer 28/28, so the fault remains localized even on that output.
3. **The changing extra identity changes back:** LOG31 had 0011593E; LOG32 again has 001159BE, like LOG29/30. Extras 0011597E and 001159FE persist. This is a recurring small family of identities, not steady accumulation. All four family members across the captures differ only in the top two bits of their last byte. The genuine suspect 001158FE is a different value; this bit observation does not establish a decoding mechanism.
4. **The initial automatic-read pattern is bounded:** between 07:12:20 and 07:12:56 each of the four silent UIDs receives exactly two DEVICE_INFO transactions, three transmissions per transaction. That matches the current reader's two-pass behavior. No other PID is sent to them in that initial phase.
5. **Later traffic is a separate phase:** broader queries begin at 07:14:02.715 (line 8295), after the initial six transmissions per UID. The full capture totals are below. DEVICE_LABEL, DMX_PERSONALITY, IDENTIFY_DEVICE and STATUS_MESSAGES are outside the core-identity PID list, so the core reader alone cannot produce this workload. Device-detail callers are plausible; HTTP provenance/operator actions are not recorded. No 0070/0011 queries occur for these four identities, so the former repeating five-PID classifier signature is absent. This does not prove which browser build ran.

| Silent UID suffix | Requests / replies | DEVICE_INFO transmissions |
| --- | ---: | ---: |
| 001158FE (suspect) | 18 / 0 | 9 |
| 0011597E | 42 / 0 | 18 |
| 001159BE | 41 / 0 | 12 |
| 001159FE | 21 / 0 | 9 |

All 122 outgoing requests to these identities are GETs. The last transaction is cut off by capture end after two transmissions; its final outcome is not recorded. There are zero recomputed ArtRdm checksum failures in LOG32. That says nothing decisive about serial responses discarded by the gateway.

## What this resolves, and the next discriminator

The inventory discrepancy is explained: **19 advertised identities represent a rig known to contain 16 physical ERAs**. The affected port has three excess identities and one known, intermittently reachable real UID. The capture does not prove that the physical fixture itself emits those extra UIDs: the log observes Ethernet tables/replies, not the serial discovery and mute exchanges.

The highest-value next test is to remove the suspect from the data path, reconnect the remaining chain with known-good termination, flush/rediscover, and capture the result. Expected if the anomaly depends on that fixture or its connection: seven advertised/answering ERAs on .90.1, eight on .90.6, fifteen total. Then compare the suspect and a healthy ERA individually using the same short known-good cable on an independent controller/port and the original EN4 port. Failure following the suspect across independent controllers implicates the fixture; failure following one port implicates that path. A serial discovery/mute capture would identify the mechanism more directly.

No UID collapsing or timeout increase is justified by this capture. Historical response evidence may keep an app row marked as having answered even though LOG32 contains no reply from it; these are capture-local counts, not a reconstruction of the live registry.

Reproduce the raw audit with `python docs/evidence/analysis/analyze_era.py 32`. The script accepts optional capture numbers; no arguments now audits LOG29–32. See ERA-investigation-2026-09-17.md for the earlier evidence and corrections.
