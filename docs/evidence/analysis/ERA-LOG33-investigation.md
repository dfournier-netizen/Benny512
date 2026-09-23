# ERA investigation — LOG33 power-cycle discovery

User reports discovery on the affected port while the suspect fixture was power cycling. Power state/timing is operator context, not telemetry contained in the packet log. Analysis is offline; no application code or hardware settings changed.

## Observations

- Capture: September 17, 2026, 07:28:24.914–07:28:56.491.
- 07:28:48.434, line 286: AtcFlush to 2.11.90.1, raw Port-Address 21.
- 07:28:55.820, line 291: TodFull with uidTotal=7, blockCount=0 and exactly seven UID entries in the raw bytes, 7.386 seconds after the flush.
- The seven UIDs are exactly the seven healthy responders from LOG32: 4D50:001158EA, 001158FF, 00115907, 00115918, 0011591C, 0011592D, 00115940.
- The entire four-identity cluster disappears together: genuine suspect 4D50:001158FE plus extras 4D50:0011597E, 001159BE, 001159FE. No replacement extra UID appears.
- Each remaining fixture receives DEVICE_INFO, SUPPORTED_PARAMETERS, DEVICE_MODEL_DESCRIPTION and MANUFACTURER_LABEL. All 28 requests receive matching ACKs, matched by peer, source/destination UID, TN and PID. Latency: 4–34 ms; zero raw ArtRdm checksum failures.
- All seven core reads finish at 07:28:56.491, just 671 ms after the table arrives. No requests to any of the missing identities appear.
- The other ERA port is not rediscovered/read in this capture. Its eight fixtures are known from previous captures, not independently verified here.

## Interpretation

This is stronger evidence than the prior UID resemblance: changing the suspect's power state coincides with precisely the affected cluster disappearing while all seven known-good identities remain and answer. Combined with the user's timing, it strongly ties the excess identities to the suspect being active, or its interaction with this line/gateway. It supports treating four advertised identities as one suspect physical fixture plus three false entries, not four independently failed physical fixtures.

It also demonstrates that this gateway can publish a clean seven-device table on the affected port. The extras are not unavoidable UI duplicates or entries that persist through every flush regardless of fixture state.

It does not yet prove the fixture deliberately transmits multiple UIDs, identify the faulty component, or demonstrate that the power cycle repairs the fault. Only one table is captured, and the log ends 671 ms after its arrival; there is no post-boot rediscovery in this file. An unpowered fixture and a physically bypassed fixture are different electrical conditions.

## Next discriminating capture

Once the fixture has fully booted, rediscover the same port again:

- Eight UIDs including 001158FE, all answering: apparent recovery after power cycle; repeat discovery to check persistence.
- Eleven UIDs with the original cluster: repeatable return of the anomaly with the active suspect.
- Eight UIDs but 001158FE silent: extra identities clear but the communication fault remains.
- Seven UIDs: suspect still not discovered; do not interpret as recovery.

If the cluster returns, compare the suspect and a healthy control separately on the same short known-good cable/termination through an independent controller. This distinguishes a fixture-following fault from interaction with the original gateway/path.

Reproduce the raw counts with `python docs/evidence/analysis/analyze_era.py 33`.
