# Entertainment Lighting Data Protocols — Technical Reference for App Development

**Compiled:** 2026-08-03 00:02 EDT
**Purpose:** Handoff reference for an engineering agent building a mobile app that sends, receives, and analyzes packets for DMX512-A, RDM, Art-Net (incl. RDM-over-Art-Net and Art-Net Timecode), sACN (E1.31), and RDMnet (E1.33).
**Compiled by:** Claude, from ANSI/ESTA standards text, ESTA's free TSP document archive, and public protocol documentation (Art-Net spec by Artistic Licence, openlighting.org, ETC RDMnet implementation docs). Byte-level tables below are reconstructed from these sources — verify exact bit offsets against the authoritative standard before shipping firmware-critical code. Source links are in the References section at the end.

---

## 0. How These Protocols Relate

```
DMX512-A (ANSI E1.11)  ─── physical serial bus, one-way, 250 kbit/s, up to 512 channels/universe
   │
   ├── RDM (ANSI E1.20) ── bidirectional EXTENSION of DMX512-A (uses "EF1" half-duplex mode
   │                        on the SAME wire pair, alternate START code 0xCC). Adds device
   │                        discovery + GET/SET parameter messaging. Still a serial-bus protocol.
   │
   ├── Art-Net (Artistic Licence, not an ANSI standard) ── UDP/IP transport that carries:
   │      • DMX512-A universes (ArtDmx)              → IP-network equivalent of the DMX cable
   │      • RDM messages (ArtRdm/ArtTod*)             → tunnels RDM messages/discovery over IP
   │      • SMPTE/EBU timecode (ArtTimeCode)          → unrelated payload piggybacked on same UDP transport
   │
   ├── sACN / E1.31 (ANSI E1.31) ── UDP/IP transport (unicast or multicast) that carries
   │      DMX512-A universe data using the ACN (Architecture for Control Networks) root/framing/
   │      DMP layer structure. Does NOT carry RDM. Multicast-native, one universe per group.
   │
   └── RDMnet / E1.33 (ANSI E1.33) ── the "real" RDM-over-IP successor. TCP-based Broker
          architecture + UDP LLRP for bootstrapping. Purpose-built for many-to-many RDM control
          over IP, superseding the ad-hoc RDM-over-Art-Net tunneling above.

Art-Net Timecode (ArtTimeCode) ── not related to DMX/RDM data at all; it's a distribution
   mechanism for SMPTE/EBU/Film/DF timecode, riding on the same Art-Net UDP packet framing
   and port (6454) purely because Art-Net already had node discovery + a convenient UDP channel.
```

**Practical implication for the app:** a single "packet sniffer/analyzer" screen needs three separate parser front-ends — raw serial DMX/RDM (only reachable via a USB-DMX or Wi-Fi-DMX interface, not from a phone's native network stack), Art-Net UDP (port 6454), and sACN UDP (port 5568, unicast or multicast). RDMnet needs mDNS/DNS-SD browsing plus TCP socket handling, which is a different code path entirely from the UDP broadcast/multicast listeners used for the others.

---

## 1. DMX512-A (ANSI E1.11) — Quick Reference

Full spec already delivered separately (`ANSI-ESTA_E1-11_2008R2018.pdf`, plus notes on ANSI E1.11-2024 changes). Summary for context:

- **Physical layer:** RS-485 (EIA-485-A), 250 kbit/s asynchronous serial, 5-pin XLR (pins 1=common, 2=data–, 3=data+, 4/5=optional secondary link).
- **Packet = Reset Sequence (Break + Mark-After-Break + START code) + up to 512 data slots.**
- **Slot format:** 1 start bit (low) + 8 data bits (LSB first) + 2 stop bits (high) = 11 bits/slot, no parity.
- **START Code (slot 0)** identifies the packet type:
  - `0x00` (NULL) = standard dimmer/generic levels, slots 1–512 = channel values 0–255.
  - `0xCC` = RDM (see Section 2).
  - `0x17` = ASCII text packet; `0x90` = UTF-8 text packet; `0xCF` = System Information Packet (SIP); `0x55` = test packet.
- **Timing:** Break ≥ 92 µs (Tx) / ≥ 88 µs (Rx min), MAB 12 µs typ., break-to-break 1204 µs typical minimum for full 512-slot packet. Effective refresh rate range ~1 Hz–830 Hz depending on slot count; standard full-universe refresh ≈ 44 Hz max.
- **Universe** = one DMX512-A data link, max 512 data slots (channels), addressed 1–512.

---

## 2. RDM — Remote Device Management (ANSI E1.20-2010)

RDM is **not a separate wire protocol** — it's a bidirectional messaging scheme that runs on the same DMX512-A physical link, using DMX512-A's Alternate START Code mechanism (EF1 topology per E1.11 Annex B). A controller and responders take turns transmitting on the same pair; only one controller is active on a link at a time, and it operates as a **polled system** — responders never speak unless addressed.

### 2.1 UID (Unique ID) — 48 bits total

| Bits | Field | Notes |
|---|---|---|
| 47–32 (2 bytes) | ESTA-assigned Manufacturer ID | Registered with ESTA; e.g. `0x0000`–`0x7fff` normal range. High bit set (`0x8000`+) = "prototype/unregistered" range, not for shipping product. |
| 31–0 (4 bytes) | Device ID | Manufacturer-assigned, unique within that Manufacturer ID |

- Text representation: `MMMM:DDDDDDDD` (hex), e.g. `7A70:FFFFFF00`.
- **Broadcast UIDs:**
  - All-devices broadcast: `FFFF:FFFFFFFF`
  - Manufacturer-specific broadcast: `MMMM:FFFFFFFF` (only devices with that Manufacturer ID respond — and per spec, responders never ACK a broadcast, they just act on it).
- App note: maintain a local manufacturer-ID lookup table (ESTA publishes an assigned list) so discovered UIDs can be shown with a human-readable manufacturer name.

### 2.2 RDM Packet Structure (sits inside a DMX512-A packet with START code 0xCC)

| Slot(s) | Field | Size | Notes |
|---|---|---|---|
| 0 | START Code | 1 | Always `0xCC` (registered RDM Alternate START Code) |
| 1 | Sub-Start Code | 1 | `0x01` = SC_SUB_MESSAGE (standard RDM); other values reserved |
| 2 | Message Length | 1 | Total slot count from Slot 0 to end of Parameter Data (excludes checksum) |
| 3–8 | Destination UID | 6 | |
| 9–14 | Source UID | 6 | |
| 15 | Transaction Number (TN) | 1 | Increments each new message from a given source; used to match request/response |
| 16 | Port ID (request) / Response Type (response) | 1 | See §2.4 for Response Type values |
| 17 | Message Count | 1 | Responder tells controller how many queued messages are pending (drives use of `QUEUED_MESSAGE` PID) |
| 18–19 | Sub-Device | 2 | `0x0000` = root/top-level device; `0xFFFF` = "all sub-devices" (SET only) |
| 20 | Command Class (CC) | 1 | See §2.3 |
| 21–22 | Parameter ID (PID) | 2 | See §2.5 |
| 23 | Parameter Data Length (PDL) | 1 | 0–231 |
| 24…(24+PDL−1) | Parameter Data | 0–231 | |
| last 2 slots | Checksum | 2 | 16-bit unsigned sum of all preceding slots (Slot 0 through end of Parameter Data), MSB first |

Max total RDM packet size: 24 + 231 + 2 = 257 slots (well under the 513-slot DMX limit — RDM packets are short compared to full DMX frames).

### 2.3 Command Classes (CC), Slot 20

| Value | Name | Meaning |
|---|---|---|
| `0x10` | DISCOVERY_COMMAND | Controller → responder(s), part of discovery algorithm |
| `0x11` | DISCOVERY_COMMAND_RESPONSE | Responder → controller |
| `0x20` | GET_COMMAND | Controller requests a parameter value |
| `0x21` | GET_COMMAND_RESPONSE | Responder returns the value |
| `0x30` | SET_COMMAND | Controller writes a parameter value |
| `0x31` | SET_COMMAND_RESPONSE | Responder acknowledges the write |

### 2.4 Response Type (Slot 16 in a response packet)

| Value | Name | Meaning |
|---|---|---|
| `0x00` | ACK | Success, full response fits in this packet |
| `0x01` | ACK_TIMER | Responder needs more time; Parameter Data = 2-byte estimated ms delay before controller should retry |
| `0x02` | NACK_REASON | Command failed; Parameter Data = 2-byte NACK reason code (see below) |
| `0x03` | ACK_OVERFLOW | Response data is too large for one packet; controller must re-issue the same GET for the same PID to receive the next block. Responder sends `ACK_OVERFLOW` on every block except the last, which is `ACK`. **Important:** a responder must abort a partial overflow transfer if it receives a command for a *different* PID before the transfer completes — the app's RDM client must not interleave unrelated commands mid-overflow-sequence. |

Common **NACK reason codes** (2-byte value in Parameter Data when Response Type = NACK_REASON):

| Code | Meaning |
|---|---|
| `0x0000` | UNKNOWN_PID |
| `0x0001` | FORMAT_ERROR |
| `0x0002` | HARDWARE_FAULT |
| `0x0003` | PROXY_REJECT |
| `0x0004` | WRITE_PROTECT |
| `0x0005` | UNSUPPORTED_COMMAND_CLASS |
| `0x0006` | DATA_OUT_OF_RANGE |
| `0x0007` | BUFFER_FULL |
| `0x0008` | PACKET_SIZE_UNSUPPORTED |
| `0x0009` | SUB_DEVICE_OUT_OF_RANGE |
| `0x000A` | PROXY_BUFFER_FULL |

### 2.5 PID (Parameter ID) Categories & Common PIDs

PIDs are 16-bit. Ranges `0x0000–0x7FFF` are ESTA-defined (this standard + E1.37 series addenda); `0x8000–0xFFDF` are manufacturer-specific (must be paired with the manufacturer's own definitions, not portable across brands); a few values at the top are reserved for future/manufacturer-test use.

**Discovery family (always Command Class = DISCOVERY):**

| PID | Name | Purpose |
|---|---|---|
| `0x0001` | DISC_UNIQUE_BRANCH | Core of the binary-search discovery algorithm — see §2.6 |
| `0x0002` | DISC_MUTE | Silences a found responder so it stops answering further branch queries |
| `0x0003` | DISC_UN_MUTE | Un-silences (used to reset discovery state) |

**Network management:**

| PID | Name |
|---|---|
| `0x0010` | PROXIED_DEVICES |
| `0x0011` | PROXIED_DEVICE_COUNT |
| `0x0015` | COMMS_STATUS |

**Status collection:**

| PID | Name |
|---|---|
| `0x0020` | QUEUED_MESSAGE |
| `0x0030` | STATUS_MESSAGES |
| `0x0031` | STATUS_ID_DESCRIPTION |
| `0x0032` | CLEAR_STATUS_ID |
| `0x0033` | SUB_DEVICE_STATUS_REPORT_THRESHOLD |

**Device info & configuration (the ones you'll query most for a "fixture inspector" screen):**

| PID | Name |
|---|---|
| `0x0050` | SUPPORTED_PARAMETERS — GET this first for any device; returns the list of every other PID it supports |
| `0x0051` | PARAMETER_DESCRIPTION — self-describing metadata for manufacturer-specific PIDs (type, unit, min/max, etc.) |
| `0x0060` | DEVICE_INFO — protocol version, device model ID, product category, footprint, personality, DMX address, sub-device count, sensor count |
| `0x0070` | PRODUCT_DETAIL_ID_LIST |
| `0x0080` | DEVICE_MODEL_DESCRIPTION |
| `0x0081` | MANUFACTURER_LABEL |
| `0x0082` | DEVICE_LABEL |
| `0x0090` | FACTORY_DEFAULTS |
| `0x00A0` | LANGUAGE_CAPABILITIES |
| `0x00B0` | LANGUAGE |
| `0x00C0` | SOFTWARE_VERSION_LABEL |
| `0x00C1` | BOOT_SOFTWARE_VERSION_ID |
| `0x00C2` | BOOT_SOFTWARE_VERSION_LABEL |
| `0x00E0` | DMX_PERSONALITY — GET/SET current "mode" (channel layout) + total personality count |
| `0x00E1` | DMX_PERSONALITY_DESCRIPTION |
| `0x00F0` | DMX_START_ADDRESS — GET/SET the fixture's DMX start address remotely (one of the most-used RDM features in the field) |
| `0x0120` | SLOT_INFO |
| `0x0121` | SLOT_DESCRIPTION |
| `0x0122` | DEFAULT_SLOT_VALUE |

**Sensors:**

| PID | Name |
|---|---|
| `0x0200` | SENSOR_DEFINITION |
| `0x0201` | SENSOR_VALUE |
| `0x0202` | RECORD_SENSORS |

**Power/Lamp/Device settings:**

| PID | Name |
|---|---|
| `0x0400` | DEVICE_HOURS |
| `0x0401` | LAMP_HOURS |
| `0x0402` | LAMP_STRIKES |
| `0x0403` | LAMP_STATE |
| `0x0404` | LAMP_ON_MODE |
| `0x0405` | DEVICE_POWER_CYCLES |
| `0x0500` | DISPLAY_INVERT |
| `0x0501` | DISPLAY_LEVEL |
| `0x0600` | PAN_INVERT |
| `0x0601` | TILT_INVERT |
| `0x0602` | PAN_TILT_SWAP |
| `0x0603` | REAL_TIME_CLOCK |

**Control:**

| PID | Name |
|---|---|
| `0x1000` | IDENTIFY_DEVICE — most useful PID for a mobile app: SET this to flash/identify a fixture so the user can visually confirm which physical unit they've selected |
| `0x1001` | RESET_DEVICE |
| `0x1010` | POWER_STATE |
| `0x1020` | PERFORM_SELFTEST |
| `0x1021` | SELF_TEST_DESCRIPTION |
| `0x1030` | CAPTURE_PRESET |
| `0x1031` | PRESET_PLAYBACK |

Note: the additional message sets **E1.37-1** (dimmer-specific PIDs), **E1.37-2** (IPv4/DNS config PIDs for network-capable RDM devices), and later E1.37-x parts extend this table further — worth a follow-up research pass if the app needs full dimmer-rack or IP-configuration support.

### 2.6 Discovery Algorithm (binary search)

RDM has no collision-detection hardware — discovery relies on a clever protocol trick:

1. Controller sends `DISC_UNIQUE_BRANCH` (PID `0x0001`) with Parameter Data = a lower-bound UID and upper-bound UID (12 bytes total: 6+6).
2. Any **unmuted** responder whose UID falls within that range replies — but the reply is NOT a normal RDM packet. It's specially encoded (a run of `0xFE` preamble bytes, then the UID and checksum each XOR'd against a fixed mask and interleaved with its complement) specifically so that if **two or more** responders reply simultaneously, the resulting garbled/overlapping signal can still be detected as "a collision happened" even though it can't be decoded as a valid UID.
3. If the controller gets a clean, single valid response → that UID is discovered. Controller sends `DISC_MUTE` (PID `0x0002`) to that UID so it stops answering further branch queries, adds it to the Table of Devices (ToD), and continues.
4. If the controller detects a collision (garbled response) → it splits the UID range in half and re-sends `DISC_UNIQUE_BRANCH` for each half, recursing (binary tree search) until each remaining branch yields either silence (no devices in that range) or exactly one clean response.
5. If no response at all → that branch is empty, backtrack.
6. Process repeats until the full 48-bit UID space allocated to that link has been walked with no un-muted responders left.

**App implication:** true DUB (Discovery Unique Branch) collision detection requires access to the raw serial waveform — a phone cannot do this over Wi-Fi/Art-Net/sACN. For any IP-based RDM feature, your app must delegate discovery to a gateway node (Art-Net node or RDMnet gateway) that has actual RS-485 hardware and DUB-decoding capability, then just consume the resulting Table of Devices. See §3.5 and §5.

### 2.7 RDM Physical/Timing Notes

RDM shares DMX512-A's Break/MAB rules but has some tighter observation window requirements (the Break must not be shortened by more than 22 µs to stay compatible with up to 4 in-line devices in series). RDM traffic and normal NULL-START-code DMX traffic interleave on the same wire; RDM's use of the link is time-boxed so it doesn't starve normal DMX refresh.

---

## 3. Art-Net (Artistic Licence — industry de facto standard, not ANSI)

Current version: **Art-Net 4**. UDP-based, typically broadcast or unicast/multicast on the local subnet, **fixed port 6454 (0x1936)**.

### 3.1 Common Packet Header (all Art-Net packets)

| Offset | Field | Size | Notes |
|---|---|---|---|
| 0–7 | ID | 8 bytes | ASCII `"Art-Net"` followed by a null byte (`0x00`) |
| 8–9 | OpCode | 2 bytes | **Little-endian** (unusual — most of the rest of the protocol's multi-byte fields are big-endian/network order) |
| 10 | ProtVerHi | 1 | Protocol version high byte (currently `0`) |
| 11 | ProtVerLo | 1 | Protocol version low byte (currently `14`) |
| 12+ | (packet-specific payload) | — | |

⚠️ **Endianness gotcha for the parser:** OpCode is little-endian; almost every other multi-byte field in Art-Net (lengths, universe numbers in ArtAddress, etc.) is big-endian. Don't assume a single byte order for the whole packet.

### 3.2 Port-Address / Universe Addressing

Art-Net's addressable "Port-Address" is 15 bits, split as:

| Bits | Field | Range |
|---|---|---|
| 14–8 | Net | 0–127 |
| 7–4 | Sub-Net | 0–15 |
| 3–0 | Universe | 0–15 |

Net + Sub-Net + Universe together give up to 32,768 possible universes. In ArtDmx packets, `SubUni` (1 byte = Sub-Net<<4 | Universe) and `Net` (1 byte) are sent as separate fields (see below) rather than pre-combined.

### 3.3 Key OpCodes

| OpCode (hex) | Name | Purpose |
|---|---|---|
| `0x2000` | OpPoll | Discovery request, broadcast by a controller to find nodes |
| `0x2100` | OpPollReply | Node's response — IP, ports, universe assignments, status, product info |
| `0x2300` | OpDiagData | Diagnostics/logging |
| `0x2400` | OpCommand | Text-based proprietary command channel |
| `0x2700`/`0x2800` | OpDataRequest / OpDataReply | Generic file/data retrieval |
| `0x5000` | OpDmx (a.k.a. ArtDmx) | The core DMX-data-over-IP packet |
| `0x5100` | OpNzs (ArtNzs) | Non-zero-start-code DMX data (e.g. text packets) other than RDM |
| `0x5200` | OpSync (ArtSync) | Forces all nodes to latch buffered ArtDmx frames simultaneously |
| `0x6000` | OpAddress (ArtAddress) | Remote node (re)configuration — names, net/sub-net/universe assignment, merge mode |
| `0x7000` | OpInput (ArtInput) | Enable/disable a node's DMX inputs |
| `0x8000` | OpTodRequest (ArtTodRequest) | Request a node's cached RDM Table of Devices |
| `0x8100` | OpTodData (ArtTodData) | Node returns its RDM Table of Devices |
| `0x8200` | OpTodControl (ArtTodControl) | Tell a node to run RDM discovery (AtoD) or flush its ToD |
| `0x8300` | OpRdm (ArtRdm) | Carries a single non-discovery RDM message (GET/SET request or response) |
| `0x8400` | OpRdmSub (ArtRdmSub) | Compressed multi-sub-device RDM GET/SET in one packet |
| `0x9700` | OpTimeCode (ArtTimeCode) | SMPTE/EBU/Film/DF timecode distribution — see §3.6 |
| `0x9800` | OpTimeSync | Real-time date/clock sync |
| `0x9900` | OpTrigger (ArtTrigger) | Remote trigger/macro firing |
| `0xf800`/`0xf900` | OpIpProg / OpIpProgReply | Remote IP configuration of a node |

(Video-data opcodes `0xa0xx` and legacy Mac-address opcodes `0xf0xx/0xf1xx` exist but are deprecated/niche — low priority for a first build.)

### 3.4 ArtDmx (OpCode `0x5000`) — the workhorse packet

| Offset | Field | Size | Notes |
|---|---|---|---|
| 0–11 | Header (ID, OpCode, ProtVer) | 12 | see §3.1 |
| 12 | Sequence | 1 | 1–255, wraps; `0` = sequencing disabled. Lets receiver detect/reorder out-of-order UDP delivery |
| 13 | Physical | 1 | Informational — which physical DMX port on the sending device originated this, not used for routing |
| 14 | SubUni | 1 | Low byte of Port-Address: `(Sub-Net<<4) | Universe` |
| 15 | Net | 1 | High byte of Port-Address (bits 14–8) |
| 16 | LengthHi | 1 | Big-endian data length |
| 17 | LengthLo | 1 | Data length, 2–512, **must be even** |
| 18… | Data | 2–512 | DMX slot values 1..N for this universe (slot 0/START code is implicit = 0x00) |

Recommended max transmit rate ≈ 44 Hz per universe (matches DMX512-A's own max refresh). Nodes that stop receiving ArtDmx typically hold/repeat the last frame for ~0.8–1 s (implementation-dependent "keep-alive" behavior, not formally mandated) before considering the source lost.

### 3.5 RDM over Art-Net (ArtTodRequest / ArtTodData / ArtTodControl / ArtRdm / ArtRdmSub)

Art-Net doesn't tunnel raw RDM discovery — that stays on the node's physical DMX/RDM port, which does the real DUB collision-search (§2.6) in hardware. Art-Net's job is to (a) trigger that discovery remotely and (b) relay the resulting device list and subsequent GET/SET traffic over IP:

1. **App wants to (re)discover devices on a node's port** → send `ArtTodControl` (OpCode `0x8200`) with Command = `AtoD` (`0x01`) targeting that Net/Sub-Net/Universe. The node runs full RDM discovery on its physical port.
2. **Node reports results** → node sends `ArtTodData` (OpCode `0x8100`) containing `RdmVer`, Port, `BindIndex`, `Net`, `CommandResponse` (`TodFull`=full list / `TodNak`=couldn't complete), `Address` (Sub-Net/Universe), `UidTotal`, `BlockCount`, `UidCount`, and an array of 6-byte UIDs (may span multiple `ArtTodData` packets via BlockCount if the device count is large).
3. **App can also proactively ask** "what's your current cached ToD?" (without forcing new discovery) via `ArtTodRequest` (OpCode `0x8000`) — node replies with `ArtTodData` from its cache.
4. **Normal GET/SET traffic to a discovered UID** → wrap the raw RDM message bytes (§2.2's Slot 0 onward, i.e. starting at the `0xCC` START code) inside `ArtRdm` (OpCode `0x8300`). Fields: `RdmVer` (`0x01`), `Filler`, `Net`, `Command` (`0x00` = Process RDM Packet), then the RDM message bytes as `Data[]`. Response comes back the same way, addressed to the requesting controller's IP.
5. **ArtRdmSub** (OpCode `0x8400`) is an optimization: instead of one full RDM packet per sub-device, it lets a controller GET/SET the *same* PID across a contiguous range of sub-devices in a single UDP packet — fields include `RdmVer`, `UID` (dest), `CommandClass`, `ParameterId`, `SubDevice` (starting sub-device), `SubCount`, and then `SubCount` × 16-bit data values.

**Known protocol wrinkle (matters for your client state machine):** if a responder answers a GET with `ACK_OVERFLOW` (§2.4), the controller must keep re-issuing the *same* GET for the *same* PID with no other RDM traffic interleaved to that responder until the overflow sequence completes, or the responder aborts the transfer. Because Art-Net nodes proxy multiple controllers/traffic sources, an app-level RDM client needs its own serialization/backoff logic here rather than assuming the transport guarantees ordering.

### 3.6 ArtTimeCode (OpCode `0x9700`)

Unrelated to DMX/RDM payload — pure timecode distribution, broadcast over the same UDP transport.

| Offset | Field | Size | Notes |
|---|---|---|---|
| 0–11 | Header | 12 | see §3.1 |
| 12–13 | Filler1/Filler2 | 2 | Transmit as zero |
| 14 | Frames | 1 | 0–29 (range depends on Type) |
| 15 | Seconds | 1 | 0–59 |
| 16 | Minutes | 1 | 0–59 |
| 17 | Hours | 1 | 0–23 |
| 18 | Type | 1 | `0`=Film (24fps), `1`=EBU (25fps), `2`=DF (Drop-Frame, 29.97fps), `3`=SMPTE (30fps) |

A `StreamId` field was added in a later spec revision to allow multiple independent timecode streams on one network — check the current Art-Net 4 PDF for its exact offset if you need multi-stream support, since it was a late addition to the header layout.

### 3.7 ArtPoll / ArtPollReply (device discovery)

- **ArtPoll** (`0x2000`): broadcast by a controller/app to find nodes on the network. Contains a `TalkToMe` flags byte (bit 1 = "reply on state change" i.e. keep sending ArtPollReply whenever the node's status changes, not just once) and a diagnostics `Priority` filter.
- **ArtPollReply** (`0x2100`): the payload-heavy response — node's IP address, port, firmware version, `ShortName`/`LongName`, `NodeReport` (status text), `NumPortsHi/Lo`, per-port `PortTypes[4]`, `GoodInput[4]`/`GoodOutput[4]` status bytes, `SwIn[4]`/`SwOut[4]` (universe assigned per port), `Style` (node/controller/media server/etc.), `MAC[6]`, `BindIp`, `Status1-3` bit fields (including RDM-capability and RDM-discovery-in-progress flags added in later revisions), `EstaMan` code (2-byte manufacturer code, same registry ESTA uses elsewhere). This is your primary "what's on my network" inventory packet — parse it fully for a device-discovery screen.

---

## 4. sACN / Streaming ACN (ANSI E1.31, current edition E1.31-2018)

UDP-based, **port 5568**, layered on the broader ACN (Architecture for Control Networks) packet structure. Unlike Art-Net, sACN is natively multicast: one IPv4 multicast group per universe, so receivers subscribe only to the universes they care about instead of receiving (or discarding) everyone's broadcast traffic.

### 4.1 Layered Packet Structure

sACN packets nest three (or more) layers, each with its own "Flags & Length" field:

```
Root Layer (ACN)
 └─ Framing Layer (E1.31)
     └─ DMP Layer (for Data packets)          [Data Packet]
     -- or --
     └─ Universe Discovery Layer               [Universe Discovery Packet]
     -- or --
     (Framing Layer only, no further layer)     [Synchronization Packet]
```

**Flags & Length field pattern** (appears at the start of Root, Framing, and DMP layers): 2 bytes where the top 4 bits are fixed flags (`0x7` prefix, i.e. top nibble = `0111`) and the bottom 12 bits are the length in bytes of everything from this field to the end of that layer (inclusive).

### 4.2 Root Layer (38 bytes)

| Field | Size | Notes |
|---|---|---|
| Preamble Size | 2 | Fixed `0x0010` |
| Postamble Size | 2 | Fixed `0x0000` |
| ACN Packet Identifier | 12 | Fixed ASCII-ish magic: `"ASC-E1.17\0\0\0"` |
| Flags & Length | 2 | See §4.1 |
| Vector | 4 | `VECTOR_ROOT_E131_DATA` for Data/Sync packets, `VECTOR_ROOT_E131_EXTENDED` for Universe Discovery packets |
| CID | 16 | Component Identifier — a **UUID (RFC 4122)** unique per physical source device, meant to be stable for the device's lifetime (store in non-volatile memory). This is your primary "which console/software sent this" identity key for the analyzer UI — much more reliable than IP address, which can change. |

### 4.3 Framing Layer (Data Packet variant, 77 bytes)

| Field | Size | Notes |
|---|---|---|
| Flags & Length | 2 | |
| Vector | 4 | `VECTOR_E131_DATA_PACKET` |
| Source Name | 64 | UTF-8, null-padded — human-readable source name, show this in the UI |
| Priority | 1 | 0–200, default 100. Used for HTP/LTP-style multi-source merge arbitration at the network level — highest priority wins |
| Reserved | 2 | Should be 0 |
| Sequence Number | 1 | Detects duplicate/out-of-order packets |
| Options | 1 | Bit 7 = Preview_Data (don't actually output, just simulate), Bit 6 = Stream_Terminated (source is explicitly ending this universe stream — treat as graceful "source gone," don't wait for timeout) |
| Universe | 2 | DMX universe number, 1–63999 (0 and 64000–65535 reserved; 63999 is reserved for the Synchronization universe unless overridden) |

### 4.4 DMP Layer (Data Packet payload, up to 523 bytes)

| Field | Size | Notes |
|---|---|---|
| Flags & Length | 2 | |
| Vector | 1 | `VECTOR_DMP_SET_PROPERTY` (`0x02`) |
| Address Type & Data Type | 1 | Fixed `0xa1` |
| First Property Address | 2 | Fixed `0x0000` |
| Address Increment | 2 | Fixed `0x0001` |
| Property Value Count | 2 | 1 + number of DMX slots present (start code + data) |
| Property Values | 1–513 | **First byte is the DMX512-A START code** (`0x00` for normal levels), followed by up to 512 data slot values |

### 4.5 Synchronization Packet (Framing-layer-only, total 49 bytes)

Used to hold multiple universes' Data Packets in a receiver's buffer and then latch them all to output at the exact same instant (avoids visible tearing across universes during a fast chase). Framing layer here uses Vector `VECTOR_E131_EXTENDED_SYNCHRONIZATION`, with fields Sequence Number + Synchronization Address (the universe number that, when a Data Packet's Options bit or matching universe reference is seen, triggers the latch). A `Synchronization Address` of 0 means "not using sync" for that source.

### 4.6 Universe Discovery Packet

Framing layer Vector = `VECTOR_E131_EXTENDED_DISCOVERY`, followed by a Universe Discovery Layer listing all universe numbers this source is currently actively transmitting — sent periodically (every ~10 s is common practice) so passive listeners can build a "what universes exist on this network" inventory without needing to see live data traffic. Good for a "network scan" feature in the app that doesn't require joining every possible multicast group first.

### 4.7 Multicast Addressing (IPv4)

Per E1.31 §9.3.1: multicast group = `239.255.<Universe Hi Byte>.<Universe Lo Byte>`. E.g. universe 1 → `239.255.0.1`; universe 500 → `239.255.1.244`. **App implication:** to receive a specific universe, join that exact multicast group rather than sniffing broadcast traffic — this is the main behavioral difference from Art-Net's typical broadcast model, and it matters for mobile: joining IP multicast groups on iOS requires the Multicast Networking entitlement (`com.apple.developer.networking.multicast`) from Apple, which is a special-request entitlement — flag this early as a possible App Store/TestFlight blocker if targeting iOS.

---

## 5. RDMnet (ANSI E1.33) — RDM over IP, the "real" successor

Where §3.5's RDM-over-Art-Net is a tunneling bolt-on, RDMnet is a ground-up redesign for RDM control over IP networks with proper many-to-many topology (multiple controllers, thousands of devices) instead of DMX/RDM's original single-controller-per-serial-link model.

### 5.1 Component Types

| Component | Role |
|---|---|
| **Broker** | Central TCP server per "Scope" (see §5.4). Handles client connection, discovery, and message routing between all other components. Analogous to an MQTT broker or chat server, conceptually. |
| **RPT Device** | An IP-native device, or an IP↔DMX/RDM gateway proxying physical-bus fixtures, that responds to RDM commands |
| **RPT Controller** | The "console"/app role — issues RDM GET/SET/DISCOVERY requests |
| **EPT Client** | Uses the Extensible Packet Transport for arbitrary non-RDM manufacturer data over the same Broker infrastructure — low priority for a first build |
| **LLRP Target** | Any device implementing LLRP so it can be bootstrapped/recovered even before it has full IP connectivity |
| **LLRP Manager** | The tool (could be your app) that performs LLRP bootstrapping |

### 5.2 The Four Sub-Protocols

1. **LLRP (Low Level Recovery Protocol)** — UDP multicast, used to configure/recover basic IP settings (and a minimal RDM PID set) on devices that are unconfigured or misconfigured and thus not yet reachable via normal IP/TCP. Deliberately NOT scalable/routable/general-purpose — it's a bootstrap/rescue protocol only. Because it's so lightweight, it shows up even in networks that are otherwise pure Art-Net or sACN, as a standalone IP-config tool. Good candidate for an early "network doctor" feature in the app.
2. **Broker Protocol** — TCP. Handles client connection handshake, scope validation, and client discovery (which other Clients/Devices/Controllers are on this Broker). Brokers advertise themselves via **DNS-SD/mDNS/Bonjour**, service type `_rdmnet._tcp` — this is exactly the discovery mechanism iOS/Android both support natively (NSNetService / NetworkServiceDiscovery), making RDMnet actually easier to discover from a phone than Art-Net's broadcast-based ArtPoll.
3. **RPT (RDM Packet Transport)** — the workhorse: carries RDM Request/Notification/Status messages over each Client's TCP connection to the Broker, which routes them to the right destination Component. Supports full multi-controller concurrency (unlike raw RDM's collision-avoidance-via-single-controller model). An RPT Device that's actually a gateway will still have to do real DUB discovery (§2.6) on its physical DMX side and report results up through RPT.
4. **EPT (Extensible Packet Transport)** — generic, non-RDM, manufacturer-defined payload transport riding the same Broker connections. Out of scope unless a specific integration needs it.

### 5.3 UID Handling — Static vs Dynamic

- Devices can present a normal **static** RDM UID (§2.1, Manufacturer ID + Device ID) if they have one, OR
- Request a **Dynamic UID** from the Broker at connect time, keyed off a persistent 128-bit **Device UUID/CID** (distinct from the 48-bit RDM UID) that the device generates/stores once. This solves the problem of IP-networked "virtual" RDM devices (e.g. software fixtures, or physical fixtures behind a gateway that outnumber the gateway's available static UID pool) needing unique 48-bit UIDs without ESTA having to hand out huge manufacturer ID blocks.
- Broker messages exist for "Request Dynamic UID Assignment" and "Fetch Dynamic UID Assignment List" — relevant PIDs/messages to implement if the app needs to manage or display dynamically-assigned devices.

### 5.4 Scope

A string identifier (default value is literally `"default"`) that segments a logical RDMnet control system on a shared physical network — think of it like an SSID for RDM traffic. Brokers advertise their Scope in their DNS-SD record; a Controller (your app) only connects to Brokers whose Scope matches what the user has configured, so multiple independent lighting systems can coexist on one LAN/venue network without cross-talk.

### 5.5 App Implications

- Needs mDNS/Bonjour browsing (`_rdmnet._tcp`) — both iOS and Android have native APIs for this, more phone-friendly than Art-Net's broadcast-polling model.
- Needs persistent TCP connections to one or more Brokers (not just fire-and-forget UDP), so the app needs proper connection lifecycle/reconnect handling, unlike the stateless UDP listeners for Art-Net/sACN.
- LLRP bootstrapping is UDP multicast on a fixed, reserved address/port pair defined in the spec — implement this narrowly (a handful of PIDs only: things like `COMPONENT_SCOPE`, `SEARCH_DOMAIN`, `TCP_COMMS_STATUS`) rather than trying to reuse the full RDM PID table from §2.5, since LLRP explicitly restricts itself to a minimal message set.
- ETC publishes an open-source C library implementation (`ETCLabs/RDMnet` on GitHub) — worth reviewing as a reference implementation for wire-format edge cases the spec text alone won't make obvious.

---

## 6. App-Level Design Notes (cross-protocol)

- **Socket types needed:** raw UDP broadcast/unicast listener (Art-Net), UDP multicast join/listener (sACN), TCP client with reconnect logic (RDMnet Broker), UDP multicast (RDMnet LLRP). True DMX/RDM serial access is only possible via an external USB-DMX or Wi-Fi-DMX interface accessory — there's no native serial DMX hardware on a phone, so that path is "talk to a third-party interface's own app-facing protocol" rather than direct bus access.
- **iOS-specific:** local network access (Art-Net, sACN, RDMnet Broker discovery) requires the Local Network privacy permission prompt (`NSLocalNetworkUsageDescription`) plus Bonjour service-type declarations (`NSBonjourServices`) for RDMnet's `_rdmnet._tcp`. IP multicast (sACN) additionally needs the Multicast Networking entitlement, which Apple grants only on request — start that process early if targeting iOS, it's not automatic.
- **Endianness:** Art-Net mixes little-endian (OpCode) and big-endian (most everything else) — write protocol-specific (de)serializers, don't share a single "network byte order" assumption across all five protocols. sACN/ACN and RDM are consistently big-endian/network order internally.
- **Packet rate / analyzer buffering:** DMX and Art-Net/sACN-carried-DMX top out around 44 Hz per universe; a "live packet analyzer" view should throttle UI redraw independently of capture rate (capture everything, render at ~10–15 fps) to stay responsive on a phone.
- **RDM discovery over IP is always delegated**, never performed locally by the app — the app triggers discovery on a gateway/node (ArtTodControl for Art-Net, or the equivalent RPT/gateway-device request for RDMnet) and consumes the resulting device table, since actual DUB collision detection (§2.6) requires physical RS-485 access the phone doesn't have.
- **Source identity for analysis:** prefer sACN's CID (UUID) and RDMnet's Device UUID over IP address for tracking "which source is this" across a session, since IP can change (DHCP) but these identifiers are meant to be stable.

---

## 7. References

- ANSI E1.11-2008(R2018) & E1.11-2024 — DMX512-A: `https://tsp.esta.org/tsp/documents/docs/ANSI-ESTA_E1-11_2008R2018.pdf` and `https://tsp.esta.org/tsp/documents/docs/ANSI%20E1.11%20-%202024.pdf` (both hosted free by ESTA)
- ANSI E1.20-2010 — RDM: preview/purchase at `webstore.ansi.org`; practical field-level references: `wiki.openlighting.org/index.php/RDM`, `wiki.openlighting.org/index.php/RDM_PID_Definitions`, `wiki.openlighting.org/index.php/ArtNet,_RDM_and_Packet_Interleaving`
- Art-Net 4 Specification (Artistic Licence Holdings Ltd.): `https://art-net.org.uk/downloads/art-net.pdf`
- ANSI E1.31-2016/2018 — sACN: `https://tsp.esta.org/tsp/documents/docs/E1-31-2016.pdf` (free ESTA copy); packet diagrams also at `wiki.openlighting.org/index.php/E1.31`
- ANSI E1.33-2019 — RDMnet: preview at `webstore.ansi.org`; reference implementation `github.com/ETCLabs/RDMnet`, Broker service `github.com/ETCLabs/RDMnetBroker`
- ANSI E1.37 series (E1.37-1 dimmer PIDs, E1.37-2 IPv4/DNS PIDs, etc.) — not detailed above, flagged as a follow-up research item if the app needs dimmer-rack-specific or IP-config RDM features.

**Caveat:** several field-level tables above (RDM packet slots, Art-Net packet offsets, sACN layer byte counts) were reconstructed from secondary technical sources and open-source implementations rather than a direct read of the paywalled ANSI PDFs for E1.20/E1.31/E1.33. They match multiple independent sources consistently, but before writing wire-format-critical parsing/encoding code, budget time to cross-check exact byte offsets against a primary copy of E1.20-2010, E1.31-2018, and E1.33-2019 (all purchasable from webstore.ansi.org) — a single off-by-one in a length field will silently corrupt every packet downstream.
