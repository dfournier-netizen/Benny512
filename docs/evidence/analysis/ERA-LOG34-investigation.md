# ERA investigation — LOG34, discovery after reset

User reports repeating the prior power-cycle test with discovery after reset. Reset timing and boot completion are operator context; the capture itself records two discovery flushes and tables on 2.11.90.1, raw Port-Address 21.

## Results

| Flush | Table received | Delay | Advertised | Identities beyond the seven known-good ERAs |
| --- | --- | ---: | ---: | --- |
| 07:32:56.658 | 07:33:04.561 (line 291) | 7.903 s | 11 | 001158FE, 001159BC, 001159FC, 001159FE |
| 07:34:04.571 | 07:34:13.708 (line 985) | 9.137 s | 10 | 001158FE, 0011597E, 001159FE |

All UID suffixes above have manufacturer prefix 4D50. Both raw ArtTodData packets are TodFull, blockCount=0; their declared counts match the bytes. The same seven healthy identities occur in both tables and in LOG33's seven-device table.

The seven healthy ERAs answer 28/28 requests with ACKs, matched individually by peer, source/destination UID, TN and PID. Reply latency is 4–34 ms. The genuine suspect 001158FE receives six DEVICE_INFO transmissions and never answers. Extras 001159BC, 001159FC and 001159FE each receive six and never answer. New-to-this-capture 0011597E receives two without a reply before capture ends at 07:34:15.208; this transaction's full retry budget is not captured. All recorded requests to these identities are DEVICE_INFO GETs. No ArtRdm checksum failure is present.

## New findings and interpretation

- The reset does not produce an answering eight-fixture port in this capture. The anomaly returns after the clean seven-fixture discovery during the earlier power cycle.
- Two previously unseen extra identities appear: 001159BC and 001159FC. Each differs by 0x02 from previously observed 001159BE/001159FE. The earlier description of variation confined to the upper two bits of the final byte no longer covers all observations; variation now includes bit 1 as well. This is a byte-level observation, not proof of a particular electrical or decoder mechanism.
- The excess count itself varies: three extras in the first table, two in the second. Thus "one fixture appearing as four" describes the first discovery, but is not a fixed multiplicity; the second table has one suspect plus two extras.
- LOG33's seven clean responders followed by LOG34's return of false identities strongly ties the anomaly to the powered suspect or its interaction with the line/gateway. It does not establish that the fixture literally owns/transmits all these UIDs, or rule out serial discovery corruption and gateway handling.
- The changing tables directly contain the extras. Stale app rows cannot account for their appearance in these packets. Conversely, if the UI retains a union of historical rows, it could show more than either individual table; no UI state is captured here.
- On the second discovery, the only subsequent requests are to newly appearing 0011597E. The capture shows no immediate repeated read of known silent UIDs, consistent with the automatic reader's existing attempt limits.

## Next useful experiment

The power-cycle comparison has now supplied its useful result: clean while the suspect is absent from discovery, faulty again afterwards. To localize further, test the suspect alone on a short known-good cable/termination through an independent controller, then a healthy ERA using that identical setup. Compare with the original EN4 port. Multiple identities following the suspect across independent controllers would strongly implicate it; appearance only on the original setup would implicate an interaction with that path. Serial discovery/mute traffic remains the missing evidence for the precise mechanism.

## Reproduction and audit correction

Run `python docs/evidence/analysis/analyze_era.py 34`. The audit previously excluded only a fixed list of known suspect/extra UIDs from its "healthy" count. LOG34's new BC/FC identities exposed that assumption: the old script printed 40 outgoing / 28 incoming as healthy. It now explicitly identifies the fifteen known-good controls and reports all other observed Martin UIDs separately, including newly advertised values. Correct healthy count is 28/28; this is an offline analysis correction, not an app change. No application code, runtime settings, or hardware was changed.
