# Phase 1a — Wire-Format Verification Report (Art-Net 4 §3 / RDM E1.20 §2)

**Scope:** cross-check of `lighting-protocols-reference_2026-08-03_0002.md` §2 (RDM) and §3 (Art-Net) against primary/authoritative sources. sACN and RDMnet out of scope per instructions.

---

## 1. Executive Summary

**Overall confidence: HIGH for RDM core packet structure and ArtDmx/ArtPoll; MEDIUM for ArtPollReply/ArtTimeCode (corrected below); LOW/UNVERIFIED for one field in ArtTodData (Art-Net 4's BindIndex position).**

### Discrepancies found in the reference doc (fix before coding)

1. **ArtPollReply has NO ProtVerHi/ProtVerLo field at all.** The reference doc's §3.1 "common header (all Art-Net packets)" table implies every packet starts with ID(8)+OpCode(2)+ProtVerHi(1)+ProtVerLo(1)=12 bytes. This is **false for ArtPollReply** — confirmed against the primary Art-Net 4 PDF text: field 3 after OpCode is `IP Address[4]` directly, no protocol-version bytes anywhere in the packet. The codec must special-case ArtPollReply's header as ID(8)+OpCode(2)=10 bytes only.
2. **ArtTimeCode has only ONE filler byte, not two.** Reference doc listed offsets 12–13 as `Filler1/Filler2` (2 bytes) before `Frames` at offset 14. The primary spec shows: offset 12 = `Filler1`, offset 13 = **`StreamId`** (not a second filler), and `Frames` moves to offset 14. Total packet is **19 bytes**, not 20. This resolves the reference doc's own flagged uncertainty ("StreamId... check the current PDF for its exact offset") — **StreamId is at byte offset 13**.
3. **DUB response encoding is bitwise OR/AND, not XOR.** Both the reference doc (§2.6) and the task brief describe the mask operation as "XOR'd against a fixed mask." Verified by (a) mathematical derivation and (b) a real firmware test vector (see §3 Golden Fixtures): the actual operation is **encode: `byte1 = D | 0xAA`, `byte2 = D | 0x55`; decode: `D = byte1 & byte2`**. Implementing this with XOR will silently produce corrupted UIDs. This is the single highest-risk correction in this report.
4. **ArtPollReply's `EstaMan` field is transmitted low-byte-first (LE)**, while the *same-named* field in `ArtPoll` is transmitted high-byte-first (BE: `EstaManHi` then `EstaManLo`). Reference doc doesn't call this out. `ArtPollReply->Oem` is BE in the same packet where `EstaMan` is LE — byte order is **not consistent even within a single packet**; every multi-byte field must be checked individually against the spec, not inferred from a "mostly BE" rule.
5. **ArtPollReply->Port is little-endian** ("Transmitted low byte first" per spec), same as OpCode. Reference doc's endianness gotcha note only called out OpCode as LE — Port needed the same warning.
6. **ArtPoll's flags byte is named `Flags` in the current spec, not `TalkToMe`** (that was an older/informal name). Same bit semantics (bit 1 = auto-reply-on-state-change). Cosmetic, but matters if you're matching field names against the PDF.

### Unverified

- **Exact byte offset of `BindIndex` in the current Art-Net 4 `ArtTodData` packet.** The Art-Net 4 changelog explicitly states "BindIndex added to ArtTodData," but the primary PDF fetch was truncated before reaching this section (see Sources), and OLA's actively-maintained `ArtNetPackets.h` struct (fetched directly) has **no `bind_index` field in `artnet_toddata_s` at all** — it still ends `...net, command_response, address, uid_total, block_count, uid_count, tod[]`. Two plausible explanations: OLA hasn't implemented this Art-Net 4 addition yet, or BindIndex silently reuses one of the 7 `Spare` bytes. **Do not hardcode a BindIndex offset for ArtTodData until this is confirmed against the full PDF.** The golden fixture below uses the pre-BindIndex (Art-Net 3 / OLA-confirmed) layout and flags this explicitly.
- ArtRdmSub field endianness (ParameterId/SubDevice/SubCount/Data) is inferred by consistency with RDM's own big-endian convention ("As per RDM specification" in the source text implies value semantics, not explicitly byte order) — WEAKLY CONFIRMED, not CONFIRMED.

Everything else in reference doc §2 (RDM packet structure, checksum algorithm, CC/NACK/UID tables) matched primary/independent sources exactly — see per-field tables below.

---

## 2. Per-Packet Field Tables

### 2.1 Art-Net Common Header

| Offset | Field | Size | Endian | Notes | Status | Source |
|---|---|---|---|---|---|---|
| 0–7 | ID | 8 | — | ASCII `"Art-Net"` + `0x00` | CONFIRMED | Art-Net 4 PDF (primary) |
| 8–9 | OpCode | 2 | **LE** | "Transmitted low byte first" | CONFIRMED | Art-Net 4 PDF |
| 10 | ProtVerHi | 1 | — | Present in most packets | CONFIRMED (except ArtPollReply — see below) | Art-Net 4 PDF |
| 11 | ProtVerLo | 1 | — | Current value = 14 | CONFIRMED | Art-Net 4 PDF |

⚠️ **ArtPollReply does not have offsets 10–11.** Its header is ID(8)+OpCode(2)=10 bytes, then packet-specific fields start immediately at offset 10.

### 2.2 ArtPoll (0x2000) — full packet, 24 bytes current / 14 bytes minimum accepted

| Offset | Field | Size | Endian | Values/Notes | Status | Source |
|---|---|---|---|---|---|---|
| 0–9 | Header | 10 | | ID+OpCode | CONFIRMED | Art-Net 4 PDF |
| 10 | ProtVerHi | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 11 | ProtVerLo | 1 | | =14 | CONFIRMED | Art-Net 4 PDF |
| 12 | Flags | 1 | | bit0=deprecated(0), bit1=send ArtPollReply on state change, bit2=send diagnostics, bit3=diag unicast, bit4=disable VLC, bit5=enable Targeted Mode, bits6-7=unused. **Named "Flags" not "TalkToMe" in current spec.** | CONFIRMED (name corrected) | Art-Net 4 PDF |
| 13 | DiagPriority | 1 | | lowest priority of diagnostics msgs to send | CONFIRMED | Art-Net 4 PDF |
| 14 | TargetPortAddressTopHi | 1 | BE (Hi first) | only used if Targeted Mode | CONFIRMED | Art-Net 4 PDF |
| 15 | TargetPortAddressTopLo | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 16 | TargetPortAddressBottomHi | 1 | BE | | CONFIRMED | Art-Net 4 PDF |
| 17 | TargetPortAddressBottomLo | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 18 | EstaManHi | 1 | BE (Hi first) | | CONFIRMED | Art-Net 4 PDF |
| 19 | EstaManLo | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 20 | OemHi | 1 | BE | | CONFIRMED | Art-Net 4 PDF |
| 21 | OemLo | 1 | | | CONFIRMED | Art-Net 4 PDF |

Receivers must accept any packet ≥14 bytes; missing trailing fields (offsets 14–21) are assumed zero. This is a **receive-side leniency requirement**, not optional.

### 2.3 ArtPollReply (0x2100) — 239 bytes current / 207 bytes minimum accepted

No ProtVer fields (see discrepancy #1). Offsets below computed by summing confirmed field sizes from the primary PDF (cross-checked against OLA's `artnet_reply_s`, which matches through offset 206/MAC, and against the doc's own stated "minimum 207 bytes" = exactly through the MAC[6] field, offset 0–206).

| Offset | Field | Size | Endian | Notes | Status | Source |
|---|---|---|---|---|---|---|
| 0–7 | ID | 8 | | | CONFIRMED | Art-Net 4 PDF |
| 8–9 | OpCode | 2 | LE | =0x2100 | CONFIRMED | Art-Net 4 PDF |
| 10–13 | IP Address | 4 | BE (MSB first) | | CONFIRMED | Art-Net 4 PDF |
| 14–15 | Port | 2 | **LE** | always 0x1936, low byte first | CONFIRMED | Art-Net 4 PDF |
| 16 | VersInfoH | 1 | | node firmware ver (not protocol ver) | CONFIRMED | Art-Net 4 PDF |
| 17 | VersInfoL | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 18 | NetSwitch | 1 | | bits14-8 of Port-Address | CONFIRMED | Art-Net 4 PDF |
| 19 | SubSwitch | 1 | | bits7-4 of Port-Address | CONFIRMED | Art-Net 4 PDF |
| 20 | OemHi | 1 | BE | | CONFIRMED | Art-Net 4 PDF |
| 21 | OemLo (Oem) | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 22 | UbeaVersion | 1 | | 0 if UBEA not programmed | CONFIRMED | Art-Net 4 PDF |
| 23 | Status1 | 1 | | bit1=RDM capable, bit0=UBEA present, bits4-5=Port-Addr prog authority, bits6-7=indicator state | CONFIRMED | Art-Net 4 PDF |
| 24 | EstaManLo | 1 | **LE** (Lo first — opposite of ArtPoll!) | | CONFIRMED | Art-Net 4 PDF |
| 25 | EstaManHi | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 26–43 | ShortName / PortName | 18 | | null-terminated, max 17 chars+null | CONFIRMED | Art-Net 4 PDF |
| 44–107 | LongName | 64 | | null-terminated, max 63 chars+null | CONFIRMED | Art-Net 4 PDF |
| 108–171 | NodeReport | 64 | | `"#xxxx [yyyy] zzzz..."` | CONFIRMED | Art-Net 4 PDF |
| 172 | NumPortsHi | 1 | | reserved, currently 0 | CONFIRMED | Art-Net 4 PDF |
| 173 | NumPortsLo | 1 | | max 4 | CONFIRMED | Art-Net 4 PDF |
| 174–177 | PortTypes[4] | 4 | | bit7=can output, bit6=can input, bits0-5=protocol | CONFIRMED | Art-Net 4 PDF |
| 178–181 | GoodInput[4] | 4 | | | CONFIRMED | Art-Net 4 PDF |
| 182–185 | GoodOutputA[4] | 4 | | (was `GoodOutput` pre-Art-Net4) | CONFIRMED | Art-Net 4 PDF |
| 186–189 | SwIn[4] | 4 | | low nibble = Universe bits3-0 | CONFIRMED | Art-Net 4 PDF |
| 190–193 | SwOut[4] | 4 | | | CONFIRMED | Art-Net 4 PDF |
| 194 | AcnPriority | 1 | | (was `SwVideo` pre-Art-Net4) | CONFIRMED | Art-Net 4 PDF |
| 195 | SwMacro | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 196 | SwRemote | 1 | | | CONFIRMED | Art-Net 4 PDF |
| 197–199 | Spare x3 | 3 | | transmit 0 | CONFIRMED | Art-Net 4 PDF |
| 200 | Style | 1 | | 0=StNode,1=StController,2=StMedia,3=StRoute,4=StBackup,5=StConfig,6=StVisual | CONFIRMED | Art-Net 4 PDF |
| 201–206 | MAC[6] | 6 | | 0 if unavailable | CONFIRMED | Art-Net 4 PDF |
| — | **[minimum-accepted packet ends here, 207 bytes]** | | | | | |
| 207–210 | BindIp[4] | 4 | | root device IP if bound | CONFIRMED | Art-Net 4 PDF |
| 211 | BindIndex | 1 | | 0 or 1 = root device | CONFIRMED | Art-Net 4 PDF |
| 212 | Status2 | 1 | | bit3=15-bit Port-Addr support, bit2=DHCP capable, bit1=DHCP configured, bit0=web config | CONFIRMED | Art-Net 4 PDF |
| 213–216 | GoodOutputB[4] | 4 | | bit7=RDM disabled, bit5=discovery not running | CONFIRMED | Art-Net 4 PDF |
| 217 | Status3 | 1 | | bits6-7=failsafe state, bit4=LLRP support, bit2=RDMnet support | CONFIRMED | Art-Net 4 PDF |
| 218–223 | DefaultRespUID[6] | 6 | | RDMnet/LLRP default responder UID | CONFIRMED | Art-Net 4 PDF |
| 224–225 | UserHi/UserLo | 2 | BE | app-specific | CONFIRMED | Art-Net 4 PDF |
| 226–227 | RefreshRateHi/Lo | 2 | BE | max ArtDmx Hz this node accepts | CONFIRMED | Art-Net 4 PDF |
| 228 | BackgroundQueuePolicy | 1 | | 0-4 defined, 5-250 mfr, 251-255 reserved | CONFIRMED | Art-Net 4 PDF |
| 229–238 | Filler | 10 | | transmit 0 | CONFIRMED | Art-Net 4 PDF |

### 2.4 ArtDmx (0x5000) — 18–530 bytes

| Offset | Field | Size | Endian | Values/Notes | Status | Source |
|---|---|---|---|---|---|---|
| 0–11 | Header | 12 | | ID+OpCode+ProtVer | CONFIRMED | Art-Net 4 PDF |
| 12 | Sequence | 1 | | 0x01–0xff increments, 0x00=disabled | CONFIRMED | Art-Net 4 PDF |
| 13 | Physical | 1 | | informational input port, not routing | CONFIRMED | Art-Net 4 PDF |
| 14 | SubUni | 1 | | low byte of Port-Address (SubNet<<4\|Universe) | CONFIRMED | Art-Net 4 PDF |
| 15 | Net | 1 | | top 7 bits of Port-Address | CONFIRMED | Art-Net 4 PDF |
| 16 | LengthHi | 1 | BE | | CONFIRMED | Art-Net 4 PDF |
| 17 | LengthLo | 1 | | 2–512, **"should" be even (recommendation, not hard MUST — be lenient on receive)** | CONFIRMED, wording nuance noted | Art-Net 4 PDF |
| 18… | Data[Length] | 2–512 | | slot 1..N values, slot 0/STARTcode implicit 0x00 | CONFIRMED | Art-Net 4 PDF |

### 2.5 ArtTodRequest (0x8000) — 24–56 bytes

| Offset | Field | Size | Notes | Status | Source |
|---|---|---|---|---|---|
| 0–11 | Header | 12 | | CONFIRMED | Art-Net 3 PDF (RDM section identical text/order in Art-Net 4 per changelog — no revisions noted for this packet) |
| 12 | Filler1 | 1 | pad to match ArtPoll | CONFIRMED | Art-Net 3 PDF |
| 13 | Filler2 | 1 | | CONFIRMED | Art-Net 3 PDF |
| 14–20 | Spare1–7 | 7 | transmit 0 | CONFIRMED | Art-Net 3 PDF; matches OLA `artnet_todrequest_s` |
| 21 | Net | 1 | top 7 bits Port-Address | CONFIRMED | Art-Net 3 PDF |
| 22 | Command | 1 | 0x00=TodFull (send entire TOD) | CONFIRMED | Art-Net 3 PDF |
| 23 | AddCount | 1 | max 32 | CONFIRMED | Art-Net 3 PDF |
| 24…(24+AddCount-1) | Address[AddCount] | 0–32 | low byte Port-Address per target universe | CONFIRMED | Art-Net 3 PDF |

### 2.6 ArtTodData (0x8100) — 28+ bytes (BindIndex position unverified, see §1)

| Offset | Field | Size | Notes | Status | Source |
|---|---|---|---|---|---|
| 0–11 | Header | 12 | | CONFIRMED | Art-Net 3 PDF |
| 12 | RdmVer | 1 | 0x00=RDM DRAFT V1.0, 0x01=RDM STANDARD V1.0 | CONFIRMED | Art-Net 3 PDF |
| 13 | Port | 1 | physical port, range 1-4 | CONFIRMED | Art-Net 3 PDF |
| 14–20 | Spare1–7 | 7 | ⚠️ Art-Net 4 changelog says BindIndex was added here somewhere; exact position **UNVERIFIED** — see §1 | UNVERIFIED | — |
| 21 | Net | 1 | | CONFIRMED (Art-Net 3 baseline) | Art-Net 3 PDF |
| 22 | CommandResponse | 1 | 0x00=TodFull, 0xff=TodNak | CONFIRMED | Art-Net 3 PDF |
| 23 | Address | 1 | low byte of Port-Address | CONFIRMED | Art-Net 3 PDF |
| 24 | UidTotalHi | 1 | BE | CONFIRMED | Art-Net 3 PDF |
| 25 | UidTotalLo | 1 | | CONFIRMED | Art-Net 3 PDF |
| 26 | BlockCount | 1 | 0-based index when UidTotal>200 | CONFIRMED | Art-Net 3 PDF |
| 27 | UidCount | 1 | UIDs in this packet, max 200 | CONFIRMED | Art-Net 3 PDF |
| 28…(28+6·UidCount-1) | TOD[UidCount] | 6·UidCount | array of 48-bit RDM UIDs | CONFIRMED | Art-Net 3 PDF |

### 2.7 ArtTodControl (0x8200) — 24 bytes fixed

| Offset | Field | Size | Notes | Status | Source |
|---|---|---|---|---|---|
| 0–11 | Header | 12 | | CONFIRMED | Art-Net 3 PDF |
| 12 | Filler1 | 1 | | CONFIRMED | Art-Net 3 PDF |
| 13 | Filler2 | 1 | | CONFIRMED | Art-Net 3 PDF |
| 14–20 | Spare1–7 | 7 | | CONFIRMED | Art-Net 3 PDF |
| 21 | Net | 1 | | CONFIRMED | Art-Net 3 PDF |
| 22 | Command | 1 | 0x00=AtcNone, 0x01=AtcFlush (flush TOD + full discovery) | CONFIRMED | Art-Net 3 PDF |
| 23 | Address | 1 | | CONFIRMED | Art-Net 3 PDF |

### 2.8 ArtRdm (0x8300) — 24 + RDM data

| Offset | Field | Size | Notes | Status | Source |
|---|---|---|---|---|---|
| 0–11 | Header | 12 | | CONFIRMED | Art-Net 3 PDF |
| 12 | RdmVer | 1 | 0x01=RDM STANDARD V1.0 | CONFIRMED | Art-Net 3 PDF |
| 13 | Filler2 | 1 | pad to match ArtPoll | CONFIRMED | Art-Net 3 PDF |
| 14–20 | Spare1–7 | 7 | | CONFIRMED | Art-Net 3 PDF; matches OLA `artnet_rdm_s` |
| 21 | Net | 1 | | CONFIRMED | Art-Net 3 PDF |
| 22 | Command | 1 | 0x00=ArProcess (Process RDM Packet) | CONFIRMED | Art-Net 3 PDF |
| 23 | Address | 1 | | CONFIRMED | Art-Net 3 PDF |
| 24… | RdmPacket[Vari] | variable | raw RDM message **excluding DMX start code slot but INCLUDING the 0xCC RDM start code byte** — i.e. starts at RDM Slot 0 | CONFIRMED | Art-Net 3 PDF |

Note: legacy `libartnet/packets.h` (`artnet_rdm_s`) has **no `Net` field** and 8 spares instead of 7+Net — that header is stale (Art-Net II era). Do not use it as a reference for current byte layout.

### 2.9 ArtRdmSub (0x8400) — 32 + (2·SubCount) bytes

| Offset | Field | Size | Endian | Notes | Status | Source |
|---|---|---|---|---|---|---|---|
| 0–11 | Header | 12 | | | CONFIRMED | Art-Net 3 PDF |
| 12 | RdmVer | 1 | | | CONFIRMED | Art-Net 3 PDF |
| 13 | Filler2 | 1 | | | CONFIRMED | Art-Net 3 PDF |
| 14–19 | UID[6] | 6 | BE | target RDM device | CONFIRMED | Art-Net 3 PDF |
| 20 | Spare1 | 1 | | | CONFIRMED | Art-Net 3 PDF |
| 21 | CommandClass | 1 | | Get/Set/GetResponse/SetResponse per RDM | CONFIRMED | Art-Net 3 PDF |
| 22–23 | ParameterId | 2 | BE (inferred) | as per RDM spec | WEAKLY CONFIRMED | Art-Net 3 PDF (byte order not explicit in text) |
| 24–25 | SubDevice | 2 | BE (inferred) | first subdevice, RDM convention (0=root,1=first) | WEAKLY CONFIRMED | Art-Net 3 PDF |
| 26–27 | SubCount | 2 | BE (inferred) | # subdevices packed; 0 illegal | WEAKLY CONFIRMED | Art-Net 3 PDF |
| 28–31 | Spare2–5 | 4 | | | CONFIRMED | Art-Net 3 PDF |
| 32…(32+2·N-1) | Data[N] | 2·N | BE (inferred) | N=0 for Get/SetResponse, N=SubCount for Set/GetResponse | CONFIRMED size rule / WEAKLY CONFIRMED endian | Art-Net 3 PDF |

### 2.10 ArtTimeCode (0x9700) — 19 bytes fixed

| Offset | Field | Size | Notes | Status | Source |
|---|---|---|---|---|---|
| 0–11 | Header | 12 | | CONFIRMED | Art-Net 4 PDF (primary) |
| 12 | Filler1 | 1 | transmit 0, receiver ignores | CONFIRMED | Art-Net 4 PDF |
| **13** | **StreamId** | 1 | 0x00 = master stream; identifies independent timecode streams | **CONFIRMED — resolves reference doc's open question** | Art-Net 4 PDF |
| 14 | Frames | 1 | 0–29 depending on Type | CONFIRMED | Art-Net 4 PDF |
| 15 | Seconds | 1 | 0–59 | CONFIRMED | Art-Net 4 PDF |
| 16 | Minutes | 1 | 0–59 | CONFIRMED | Art-Net 4 PDF |
| 17 | Hours | 1 | 0–23 | CONFIRMED | Art-Net 4 PDF |
| 18 | Type | 1 | 0=Film(24fps) 1=EBU(25fps) 2=DF(29.97fps) 3=SMPTE(30fps) | CONFIRMED | Art-Net 4 PDF |

### 2.11 RDM Packet Structure (ANSI E1.20)

| Slot(s) | Field | Size | Endian | Notes | Status | Source |
|---|---|---|---|---|---|---|
| 0 | START Code | 1 | | 0xCC | CONFIRMED | OLA `RDMPacket.h` (`START_CODE=0xcc`) |
| 1 | Sub-Start Code | 1 | | 0x01 = SC_SUB_MESSAGE | CONFIRMED | OLA `RDMPacket.h` (`SUB_START_CODE=0x01`) |
| 2 | Message Length | 1 | | Slot0 through end of Parameter Data, excludes checksum | CONFIRMED | OLA `RDMCommand::MessageLength()` semantics |
| 3–8 | Destination UID | 6 | | | CONFIRMED | OLA `RDMCommandHeader` |
| 9–14 | Source UID | 6 | | | CONFIRMED | OLA `RDMCommandHeader` |
| 15 | Transaction Number | 1 | | | CONFIRMED | OLA `RDMCommandHeader` |
| 16 | Port ID (req) / Response Type (resp) | 1 | | | CONFIRMED | OLA `RDMCommandHeader` field named `port_id`, overloaded per direction |
| 17 | Message Count | 1 | | | CONFIRMED | OLA `RDMCommandHeader` |
| 18–19 | Sub-Device | 2 | BE | 0x0000=root, 0xFFFF=all(SET only) | CONFIRMED | OLA `RDMEnums.h` (`ROOT_RDM_DEVICE=0`, `ALL_RDM_SUBDEVICES=0xffff`) |
| 20 | Command Class | 1 | | | CONFIRMED | OLA `RDMEnums.h` |
| 21–22 | Parameter ID | 2 | BE | | CONFIRMED | OLA `RDMCommandHeader` |
| 23 | PDL | 1 | | 0–231 | CONFIRMED | OLA `RDMCommandHeader`; 24+231+2=257 arithmetic checks out |
| 24…(24+PDL−1) | Parameter Data | 0–231 | | | CONFIRMED | — |
| last 2 slots | Checksum | 2 | BE (MSB first) | 16-bit unsigned sum, Slot0..end of PD | CONFIRMED | OLA `RDMCommand::CalculateChecksum`; independently corroborated by erg.abdn.ac.uk RDM technical page |

**Command Classes (Slot 20):** `0x10` DISCOVER_COMMAND, `0x11` DISCOVER_COMMAND_RESPONSE, `0x20` GET_COMMAND, `0x21` GET_COMMAND_RESPONSE, `0x30` SET_COMMAND, `0x31` SET_COMMAND_RESPONSE — **CONFIRMED**, exact match against OLA `RDMEnums.h`.

**NACK reason codes 0x0000–0x000A:** UNKNOWN_PID, FORMAT_ERROR, HARDWARE_FAULT, PROXY_REJECT, WRITE_PROTECT, UNSUPPORTED_COMMAND_CLASS, DATA_OUT_OF_RANGE, BUFFER_FULL, PACKET_SIZE_UNSUPPORTED, SUB_DEVICE_OUT_OF_RANGE, PROXY_BUFFER_FULL — **CONFIRMED**, exact match against OLA `RDMEnums.h` (`rdm_nack_reason`). Note: OLA's enum continues well past 0x000A (E1.37/E1.33 additions up to 0x0020) — worth pulling into the PID/reason table in a later phase if the app needs those.

**Response Type values (Slot 16 in responses):** ACK=0x00, ACK_TIMER=0x01, NACK_REASON=0x02, ACK_OVERFLOW=0x03 — CONFIRMED via OLA `RDMEnums.h` (`ACK_OVERFLOW=3` explicit constant) plus universal cross-implementation convention; the other three values were not independently re-derived from a second source in this pass (WEAKLY CONFIRMED for ACK/ACK_TIMER/NACK_REASON specifically, CONFIRMED for ACK_OVERFLOW).

### 2.12 UID (48-bit)

| Bits | Field | Notes | Status | Source |
|---|---|---|---|---|
| 47–32 | Manufacturer ID | ESTA-assigned; 0x8000+ = prototype/unregistered | CONFIRMED | OLA `UID::FromString` (expects `MMMM:DDDDDDDD` hex, 4+8 chars) |
| 31–0 | Device ID | mfr-assigned | CONFIRMED | OLA `UID.cpp` |

Broadcast UIDs `FFFF:FFFFFFFF` (all) and `MMMM:FFFFFFFF` (mfr-specific) — CONFIRMED by convention, consistent across all sources reviewed.

### 2.13 Discovery Unique Branch (DUB)

**Request parameter data (12 bytes):** Lower Bound UID (6) + Upper Bound UID (6) — CONFIRMED via OLA `RDMCommand.h` function signature `NewDiscoveryUniqueBranchRequest(source, lower, upper, transaction_number, port_id)`, consistent with universal RDM literature.

**Response encoding (corrected from reference doc — see Discrepancy #3):**

| Element | Value/Rule | Status | Source |
|---|---|---|---|
| Preamble | 0 to 7 bytes of `0xFE` | CONFIRMED | esp_dmx firmware test vector (7 bytes used); standard RDM knowledge |
| Separator | 1 byte `0xAA` | CONFIRMED | esp_dmx test vector |
| Data payload (pre-encoding) | 8 bytes = 6-byte UID + 2-byte checksum (checksum = 16-bit sum of the 6 UID bytes only, **not** a full RDM-header checksum since there's no header) | CONFIRMED | Derived + verified against test vector |
| Encoding per data byte D | Two bytes transmitted: `E1 = D \| 0xAA`, `E2 = D \| 0x55` | **CONFIRMED (corrects "XOR" framing)** | Mathematical derivation + esp_dmx test vector, verified byte-for-byte (see §3) |
| Decoding | `D = E1 & E2` | CONFIRMED | Same derivation/verification |
| Total response length | 8 (max preamble+sep) + 16 (encoded data) = up to 24 bytes | CONFIRMED | esp_dmx test vector is exactly 24 bytes |

---

## 3. Golden Fixtures

All fixtures use `41 72 74 2D 4E 65 74 00` for the `"Art-Net\0"` ID and current ProtVer `00 0E` (14) where applicable. Test UIDs: controller = `7A70:00000001`, fixture = `7A70:12345678`.

### 3.1 ArtPoll (0x2000) — 24 bytes

```
41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00
```
| Bytes | Field | Value |
|---|---|---|
| [0:8] | ID | "Art-Net\0" |
| [8:10] | OpCode (LE) | 0x2000 |
| [10] | ProtVerHi | 0x00 |
| [11] | ProtVerLo | 0x0E (14) |
| [12] | Flags | 0x02 (bit1 set: reply on state change) |
| [13] | DiagPriority | 0x00 |
| [14:18] | TargetPortAddress Top/Bottom | 0x00 (targeted mode disabled) |
| [18:20] | EstaMan Hi/Lo | 0x0000 |
| [20:22] | Oem Hi/Lo | 0x0000 |

### 3.2 ArtPollReply (0x2100) — 239 bytes

```
41 72 74 2D 4E 65 74 00 00 21 0A 00 00 32 36 19 00 01 00 00 00 00 00 02
00 00 42 65 6E 6E 79 35 31 32 00 00 00 00 00 00 00 00 00 00 42 65 6E 6E
79 35 31 32 20 4E 6F 64 65 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 23 30 30 30 31 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 01 80 00 00 00 00
00 00 00 80 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 64 00 00 00 00
00 00 02 00 00 00 00 01 0A 00 00 32 01 08 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 2C 00 00 00 00 00 00 00 00 00 00 00
```
Key field-to-offset mapping (full table in §2.3): ID[0:8], OpCode[8:10]=0x2100(LE), IP[10:14]=10.0.0.50, Port[14:16]=0x1936(LE, "36 19"), VersInfo[16:18]=0.1, NetSwitch/SubSwitch[18:20]=0/0, Oem[20:22]=0, Ubea[22]=0, Status1[23]=0x02(RDM-capable), EstaMan(LE)[24:26]=0, ShortName[26:44]="Benny512", LongName[44:108]="Benny512 Node", NodeReport[108:172]="#0001", NumPorts[172:174]=0x0001, PortTypes[174:178]=80 00 00 00, GoodInput[178:182]=0, GoodOutputA[182:186]=80 00 00 00, SwIn/SwOut[186:194]=0, AcnPriority[194]=0x64, SwMacro/SwRemote/Spare[195:200]=0, Style[200]=0x00(StNode), MAC[201:207]=02:00:00:00:00:01, BindIp[207:211]=10.0.0.50, BindIndex[211]=1, Status2[212]=0x08, GoodOutputB[213:217]=0, Status3[217]=0, DefaultRespUID[218:224]=0, User[224:226]=0, RefreshRate[226:228]=0x002C(44Hz), BackgroundQueuePolicy[228]=0, Filler[229:239]=0.

### 3.3 ArtDmx (0x5000) — 22 bytes, universe 0, 4 channels

```
41 72 74 2D 4E 65 74 00 00 50 00 0E 01 00 00 00 00 04 FF 00 7F 01
```
Sequence=1, Physical=0, SubUni=0, Net=0, Length=0x0004 (BE), Data=[255,0,127,1].

### 3.4 ArtTimeCode (0x9700) — 19 bytes

```
41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01 03
```
Filler1=0, **StreamId[13]=0x00**, Frames=4, Seconds=3, Minutes=2, Hours=1, Type=3(SMPTE).

### 3.5 ArtTodRequest (0x8000) — 25 bytes

```
41 72 74 2D 4E 65 74 00 00 80 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00
```
Net=0, Command=0x00(TodFull), AddCount=1, Address=[0x00].

### 3.6 ArtTodData (0x8100) — 34 bytes, 1 UID reported

```
41 72 74 2D 4E 65 74 00 00 81 00 0E 01 01 00 00 00 00 00 00 00 00 00 00
00 01 00 01 7A 70 12 34 56 78
```
RdmVer=1, Port=1, Net=0, CommandResponse=0(TodFull), Address=0, UidTotal=0x0001, BlockCount=0, UidCount=1, TOD=[7A70:12345678]. (BindIndex not represented — see §1 UNVERIFIED note.)

### 3.7 ArtTodControl (0x8200) — 24 bytes

```
41 72 74 2D 4E 65 74 00 00 82 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00
```
Net=0, Command=0x01(AtcFlush), Address=0.

### 3.8 ArtRdm (0x8300) carrying an RDM GET DEVICE_INFO request — 50 bytes total

**RDM message (26 bytes): GET DEVICE_INFO, dest=7A70:12345678, src=7A70:00000001**

| Slot | Field | Value |
|---|---|---|
| 0 | START Code | 0xCC |
| 1 | Sub-Start Code | 0x01 |
| 2 | Message Length | 0x18 (24) |
| 3–8 | Dest UID | 7A 70 12 34 56 78 |
| 9–14 | Src UID | 7A 70 00 00 00 01 |
| 15 | TN | 0x00 |
| 16 | Port ID | 0x01 |
| 17 | Message Count | 0x00 |
| 18–19 | Sub-Device | 00 00 |
| 20 | CC | 0x20 (GET_COMMAND) |
| 21–22 | PID | 00 60 (DEVICE_INFO) |
| 23 | PDL | 0x00 |
| 24–25 | Checksum | 0x04 0x4F |

**Checksum arithmetic:** sum of slots 0–23 (24 bytes) =
`0xCC+0x01+0x18+0x7A+0x70+0x12+0x34+0x56+0x78+0x7A+0x70+0x00+0x00+0x00+0x01+0x00+0x01+0x00+0x00+0x00+0x20+0x00+0x60+0x00`
`= 204+1+24+122+112+18+52+86+120+122+112+0+0+0+1+0+1+0+0+0+32+0+96+0 = 1103 = 0x044F` → ChecksumHi=`0x04`, ChecksumLo=`0x4F`. ✓

RDM message hex: `CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F`

**Wrapped in ArtRdm:**
```
41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
04 4F
```
RdmVer=1, Filler2=0, Spare1-7=0, Net=0, Command=0x00(ArProcess), Address=0, then the 26-byte RDM message above.

### 3.9 Raw RDM Response — GET_RESPONSE / ACK for DEVICE_INFO — 45 bytes

Responder (7A70:12345678) replies to controller (7A70:00000001), TN echoed =0x00, PDL=19 (standard DEVICE_INFO parameter data).

| Slot | Field | Value |
|---|---|---|
| 0 | START Code | 0xCC |
| 1 | Sub-Start Code | 0x01 |
| 2 | Message Length | 0x2B (43) |
| 3–8 | Dest UID (controller) | 7A 70 00 00 00 01 |
| 9–14 | Src UID (responder) | 7A 70 12 34 56 78 |
| 15 | TN | 0x00 |
| 16 | Response Type | 0x00 (ACK) |
| 17 | Message Count | 0x00 |
| 18–19 | Sub-Device | 00 00 |
| 20 | CC | 0x21 (GET_COMMAND_RESPONSE) |
| 21–22 | PID | 00 60 (DEVICE_INFO) |
| 23 | PDL | 0x13 (19) |
| 24–42 | Parameter Data (19 bytes, DEVICE_INFO) | see below |
| 43–44 | Checksum | 0x04 0x84 |

DEVICE_INFO parameter data (19 bytes): RDM Protocol Version=1.0 (`01 00`), Device Model ID=`00 01`, Product Category=`01 01` (FIXTURE_FIXED), Software Version ID=`01 00 00 00`, DMX Footprint=`00 04`, Current Personality=`01`, Personality Count=`04`, DMX Start Address=`00 01`, Sub-Device Count=`00 00`, Sensor Count=`00`.

**Checksum arithmetic:** header slots 0–23 sum = 1141; parameter-data bytes sum = 15; total = 1156 = `0x0484` → ChecksumHi=`0x04`, ChecksumLo=`0x84`. ✓

Full hex: `CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00 04 84`

### 3.10 ArtRdmSub (0x8400) — SET DMX_START_ADDRESS across 2 subdevices — 36 bytes

```
41 72 74 2D 4E 65 74 00 00 84 00 0E 01 00 7A 70 12 34 56 78 00 30 00 F0
00 01 00 02 00 00 00 00 00 01 00 05
```
RdmVer=1, Filler2=0, UID=7A70:12345678, Spare1=0, CommandClass=0x30(SET_COMMAND), ParameterId=0x00F0(DMX_START_ADDRESS), SubDevice=1, SubCount=2, Spare2-5=0, Data=[0x0001, 0x0005] (subdevice1→address1, subdevice2→address5).

### 3.11 DUB Request — DISC_UNIQUE_BRANCH, full-range branch — 38 bytes

Dest=broadcast `FFFF:FFFFFFFF`, Src=controller `7A70:00000001`, PD = Lower `0000:00000000` + Upper `FFFF:FFFFFFFF`.

`CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF 0D EE`

Checksum arithmetic: sum of 36 header+PD bytes = 3566 = `0x0DEE`. ✓ (Message Length slot2 = 24+12 = `0x24`.)

### 3.12 DUB Response — encoded reply from 7A70:12345678 — 24 bytes

Pre-encoding 8-byte payload = UID `7A 70 12 34 56 78` + checksum (sum of UID bytes = 510 = `0x01FE`) = `7A 70 12 34 56 78 01 FE`.

Encoding each byte D → (`D|0xAA`, `D|0x55`):

| D | D\|0xAA | D\|0x55 |
|---|---|---|
| 0x7A | 0xFA | 0x7F |
| 0x70 | 0xFA | 0x75 |
| 0x12 | 0xBA | 0x57 |
| 0x34 | 0xBE | 0x75 |
| 0x56 | 0xFE | 0x57 |
| 0x78 | 0xFA | 0x7D |
| 0x01 | 0xAB | 0x55 |
| 0xFE | 0xFE | 0xFF |

Full response (7×0xFE preamble + 0xAA separator + 16 encoded bytes):
`FE FE FE FE FE FE FE AA FA 7F FA 75 BA 57 BE 75 FE 57 FA 7D AB 55 FE FF`

Verified by decoding back (`E1 & E2`) for every pair — reproduces `7A 70 12 34 56 78 01 FE` exactly (shown pair-by-pair during derivation; spot-check: `0xFA & 0x7F = 0x7A` ✓, `0xFE & 0xFF = 0xFE` ✓).

---

## 4. Implementation Notes

**Endianness map:**
| Field | Packet | Endian |
|---|---|---|
| OpCode | all Art-Net | LE |
| Port | ArtPollReply | LE |
| EstaMan | ArtPollReply | LE (Lo, Hi) |
| EstaMan | ArtPoll | BE (Hi, Lo) — **inconsistent with ArtPollReply, check both individually** |
| Oem | ArtPoll, ArtPollReply | BE |
| TargetPortAddress Top/Bottom | ArtPoll | BE |
| Length | ArtDmx | BE |
| UidTotal | ArtTodData | BE |
| RefreshRate, User | ArtPollReply | BE |
| Sub-Device, PID, Checksum | RDM (all messages) | BE, no exceptions found |
| ParameterId, SubDevice, SubCount, Data | ArtRdmSub | BE (inferred, not explicit in spec text — WEAKLY CONFIRMED) |

**Min/max packet sizes:**
| Packet | Min | Max |
|---|---|---|
| ArtPoll | 14 (legacy-accept) | 24 (current) |
| ArtPollReply | 207 (legacy-accept) | 239 (current) |
| ArtDmx | 18 | 530 |
| ArtTodRequest | 24 | 56 (AddCount≤32) |
| ArtTodData | 28 | 1228 (UidCount≤200) |
| ArtTodControl | 24 | 24 (fixed) |
| ArtRdm | 24 | ~281 (24 + max 257-byte RDM message) |
| ArtRdmSub | 32 | 32 + 2·SubCount (no protocol-level cap; MTU-bound) |
| ArtTimeCode | 19 | 19 (fixed) |
| RDM message | 26 (0 PD) | 257 (231 PD) |

**Quirks to encode as parser rules, not assumptions:**
- **ArtPollReply has no ProtVer bytes** — do not run it through a generic "read 12-byte header" path.
- **ArtDmx Length "should" be even** — accept odd lengths on receive (spec uses "should," not "shall"); always send even.
- **ArtPollReply length has grown across spec revisions** (207→239 bytes as RDMnet/LLRP/BackgroundQueue fields were appended). Parse by walking fields in order and treating anything past the declared UDP payload length as zero, rather than requiring an exact 239-byte packet.
- **ArtTimeCode StreamId is at byte 13**, not a second filler byte — this was the reference doc's flagged open question, now resolved.
- **DUB response uses OR-encode / AND-decode with masks 0xAA/0x55, not XOR.** Get this wrong and DUB parsing silently produces garbage UIDs that happen to "look" plausible.
- **ArtTodData's BindIndex position is unresolved** — treat as a TODO with an explicit test that will fail loudly (not silently misparse) until confirmed against the full Art-Net 4 PDF §"ArtTodData packet definition" (page ~87 per its own TOC).
- RDM Message Length (slot 2) excludes the checksum; total on-wire slot count for the message is `MessageLength + 2`.

---

## 5. Sources

- **Art-Net 4 Specification** (Artistic Licence Holdings Ltd., "Art-Net 4 Protocol Release V1.4 Document Revision 1.4dp 23/10/2025"): https://art-net.org.uk/downloads/art-net.pdf — fetched and read directly (PDF-to-text). Coverage obtained: full common header, Table 1 OpCodes, ArtPoll, ArtPollReply, ArtIpProg, ArtTimeCode, ArtCommand, ArtDmx, ArtSync, start of ArtNzs. **Fetch was truncated by tooling before reaching the ArtTodRequest/ArtTodData/ArtTodControl/ArtRdm/ArtRdmSub sections (~pages 85–94)** — those were cross-checked instead against the Art-Net 3 spec (below), which has textually identical RDM-tunneling sections per the Art-Net 4 changelog (only additions noted are BindIndex-in-ArtTodData and RDM Fifo params in ArtRdm, neither of which was in scope to pin down exactly).
- **Art-Net 3 Specification** (Artistic Licence, "Art-Net 3 Protocol Release V1.4 Document Revision 1.4be 19/12/2011"), mirrored by Chauvet Professional: https://www.chauvetprofessional.com/wp-content/uploads/2015/06/Art-Net-3-Instructions.pdf — fetched and read directly. Primary source for ArtTodRequest, ArtTodData, ArtTodControl, ArtRdm, ArtRdmSub field tables.
- **OpenLighting Architecture (OLA)** source, GitHub `OpenLightingProject/ola` (master branch, fetched raw):
  - `plugins/artnet/ArtNetPackets.h` — Art-Net struct layouts, cross-check for field order/sizes.
  - `include/ola/rdm/RDMPacket.h` — `RDMCommandHeader`, START_CODE/SUB_START_CODE constants.
  - `include/ola/rdm/RDMCommand.h` — RDMCommandClass enum, DUB/Mute/UnMute request helpers.
  - `include/ola/rdm/RDMEnums.h` — full PID table, NACK reasons, ACK_OVERFLOW constant, product category/detail enums.
  - `common/rdm/UID.cpp` — UID string format confirmation.
- **libartnet** `artnet/packets.h` (GitHub `OpenLightingProject/libartnet`) — consulted, found **stale/pre-Art-Net-3** for RDM opcodes (no Net field); used only to confirm it should NOT be relied on.
- **someweisguy/esp_dmx** (ESP32 ANSI E1.11/E1.20 implementation) — surfaced via web search a hard-coded DUB discovery-response test vector (`{0xfe×7, 0xaa, 0xaf,0x55,0xea,0xf5,0xba,0x57,0xbb,0xdd,0xbf,0x55,0xba,0xdf,0xaa,0x5d,0xbb,0x7d}`), used to empirically confirm the OR/AND masking algorithm against independent mathematical derivation.
- **wiki.openlighting.org**: `RDM` and `ArtNet,_RDM_and_Packet_Interleaving` pages — consulted for ACK_OVERFLOW interleaving behavior (confirms reference doc §2.4/§3.5 narrative, no new byte-level data).
- **en.wikipedia.org/wiki/RDM_(lighting)** and **erg.abdn.ac.uk/users/gorry/eg3576/RDM-link.html** — consulted for general RDM/checksum corroboration, no conflicting information found.
- **ETCLabs/RDM** (`defs.h`) and **art-net.org.uk** per-opcode HTML pages — **attempted, not reachable/not fetched** in this pass (time-boxed after the two PDF sources above yielded sufficient primary-source coverage); if BindIndex-in-ArtTodData needs resolving before Phase 1b, these are the next sources to try, along with re-fetching the Art-Net 4 PDF with a page-range-limited tool.

---

# CORRECTION NOTICE — added 2026-08-25

**This report contains an error in §2.8 and §3.8 (ArtRdm, OpCode 0x8300). It was found against real hardware on 2026-08-25 and has been fixed in the code as of commit `988d647`. Do not trust §3.8's golden fixture.**

## What is wrong

§2.8's field table covers ArtRdm offsets 0–23 and marks them CONFIRMED. Those rows are correct and remain trustworthy.

**The table has no row for the trailing `RdmPacket` field, and that field's framing was never verified.** The Art-Net specification states that `RdmPacket` carries the RDM data packet **excluding** the DMX StartCode — the payload begins at the RDM sub-start code `0x01` (SC_SUB_MESSAGE), not at `0xCC`.

That requirement appears in the spec's prose rather than in the offset table, so the table-extraction pass that produced §2 had no reason to surface it. §3.8's golden fixture was then constructed from the same assumption, which is why it is not independent evidence and why it did not catch the error.

## Independent confirmation

OLA (`OpenLightingProject/ola`), `plugins/artnet/ArtNetNode.cpp`, in its ArtRdm receive path, verbatim:

> `// The Art-Net packet does not include the RDM start code. Prepend that.`

OLA's `artnet_rdm_s` struct (`plugins/artnet/ArtNetPackets.h`) ends in a bare `uint8_t data[ARTNET_MAX_RDM_DATA]` with no start-code byte accounted for.

## Observed hardware symptom

Against an Obsidian/Elation-family gateway at 2.11.90.4 (Art-Net protocol version 14), sending the `0xCC`-inclusive form produced: RDM discovery working normally (ArtTodControl → ArtTodData returned fixture UID `22A6:004D05BF`), and **every** directed ArtRdm GET silently ignored — 42 requests across 7 PIDs, 3 retries each, zero responses and zero NACKs. Discovery is unaffected because ArtTodControl/ArtTodData carry no RDM payload.

## §3.8 corrected

The fixture is titled "ArtRdm (0x8300) carrying an RDM GET DEVICE_INFO request — **50 bytes total**". The correct packet is **49 bytes**.

**Wrong (as printed in §3.8 — do not use):**
```
41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
04 4F
```

**Correct:**
```
41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
04 4F
```

The RDM message itself is unchanged — same UIDs, same TN, same PortID, same CC/PID/PDL, same checksum `04 4F` (the checksum covers the RDM message from its own slot 0, including the `0xCC`, regardless of where the Art-Net payload starts). Only the Art-Net payload's starting point moves.

§3.9's raw RDM response fixture is **unaffected** — it documents a raw RDM message, not an Art-Net-wrapped one, and correctly begins at `0xCC`.

## What the code does now

- **Send:** spec-correct, `0xCC` stripped.
- **Receive:** tolerant — accepts a payload starting with either `0x01` or `0xCC`, since some nodes do include the start code and rejecting those would trade one silent failure for another.
- **`--legacy-rdm-startcode`** (default off) restores the old outbound framing for A/B testing at a bench; the active mode is logged once at startup so a captured log self-identifies which framing produced it.
- The 50-byte form above is retained in the test suite as `TestGoldenArtRdmEncodeByteExactLegacy`, guarding the legacy flag — **not** as a claim about correct behavior.

## Standing lesson for future verification passes

A verification pass that extracts offset tables will silently miss requirements stated in prose, and a golden fixture built by that same pass inherits the blind spot rather than catching it. Where a field's **framing** matters as much as its offset, check an independent implementation's actual wire behavior. Every offset in this report was confirmed correctly; what got missed was a field the table never had a row for.

Full detail in `Benny512 — Project Notes.md`, entry "2026-08-25 (later) — Phase 1d first hardware contact: the ArtRdm start-code bug".
