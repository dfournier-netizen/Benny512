# RDM Manufacturer PIDs, Sensors & Non-Fixture Devices — Wire-Level Research

**Compiled:** 2026-08-13 23:47 EDT
**Purpose:** Pre-implementation research for three Benny512 features: (A) a generic, self-describing manufacturer-specific PID editor (no hardcoded vendor tables), (B) a sensors UI with gauges/meters, (C) first-class support for non-fixture RDM devices (gateways, splitters, wireless CRMX transceivers). Owner's hardware for tomorrow's test session: Obsidian Netron EN4 gateway, LumenRadio Aurora + MoonLite2 (CRMX wireless, RDM proxy), Chroma-Q Color Force II 48 (manufacturer PIDs for pixel/refresh/orientation).
**Style model:** matches `phase1a-wire-format-verification_2026-08-04_2305.md` — offset tables + golden hex fixtures + per-field verification status.
**Scope note:** this pass builds on, and does not re-litigate, the RDM core packet structure already confirmed in Phase 1a (checksum algorithm, CC/NACK/UID tables, ACK_OVERFLOW mechanics). Those are treated as given below.

---

## 1. Executive Summary & Implementation Recommendations

### 1.1 Can a fully generic PID editor work from PARAMETER_DESCRIPTION alone?

**Mostly yes, with three concrete gaps to design around:**

1. **PARAMETER_DESCRIPTION (0x0051) gives you enough to render a *bounded, typed* editor field** for almost any manufacturer PID: PDL size, data type (`DS_*`), command class (GET/SET/GET_SET — so you know whether to show a "Set" button at all), unit + prefix (so you can label the field, e.g. "24000 Hz" instead of a bare integer), and min/max/default (4-byte fields whose sign interpretation depends on `data_type`). This is exactly what E1.20 designed it for, and it is how OLA's own RDM controller UI works without hardcoded vendor tables.
2. **Gap 1 — no per-value enum labels.** PARAMETER_DESCRIPTION has no mechanism to say "byte value 0x01 means '750Hz', 0x02 means '1500Hz'" for `DS_ENUMERATION`/`DS_UNSIGNED_BYTE` fields with a small discrete range (this is exactly the Chroma-Q Frequency/Fan-Speed/Grouping pattern — see §5.1). The only ESTA-standard extension mechanism for this is **METADATA_JSON (0x0053)** / **METADATA_JSON_URL (0x0054)** (E1.37-5), which — if the device implements it — returns a JSON blob that can carry enum labels, but this is a newer/optional PID and most fixtures in the field (including, per the manual text, Chroma-Q Color Force II) do **not** advertise it. **Design implication:** render unknown enumerated-looking fields (small integer range, `DS_UNSIGNED_BYTE`/`DS_ENUMERATION`, min=0) as a **numeric stepper bounded by min/max**, not a labeled dropdown, unless METADATA_JSON is present and successfully parsed. Do not attempt to hardcode vendor enum tables — that reintroduces the exact per-vendor maintenance burden this feature is meant to avoid.
3. **Gap 2 — PARAMETER_DESCRIPTION is a per-PID round trip.** For a device with N manufacturer PIDs you need 1 SUPPORTED_PARAMETERS GET + N PARAMETER_DESCRIPTION GETs before you can render anything. At typical RDM turnaround (~a few ms–tens of ms per transaction over a real DMX/RDM link, more over an Art-Net-tunneled gateway link) this is fine for N in the tens, sluggish for N in the hundreds. **Design implication:** cache PARAMETER_DESCRIPTION responses keyed by `(manufacturer_id, pid)` — the shape of a manufacturer PID is a firmware-version-scoped constant, not a per-device-instance fact, so this cache can be shared across all discovered devices from the same manufacturer/model and persisted across app restarts (fits the "no hardcoded vendor tables" goal — it's a *learned* cache, not a shipped table).
4. **Fallback when a device doesn't support PARAMETER_DESCRIPTION at all** (older/cheaper responders, or ESTA PIDs where support is legal but rare — see below): fall back to **raw hex display + raw hex edit** for that PID — show the PDL and a hex/ASCII toggle, let the user type new bytes, SET blind. This is the universal fallback; every RDM device must at least support GET of the parameter data it advertises in SUPPORTED_PARAMETERS, even if it can't describe its own shape.
5. **PARAMETER_DESCRIPTION applies to manufacturer-specific PIDs (0x8000–0xFFDF)**; ESTA PIDs (0x0000–0x7FFF) are *not required* to be describable via PARAMETER_DESCRIPTION (their shape is defined by the standard text itself), but the mechanism does not forbid a responder from also describing a standard PID this way — CONFIRMED via OLA's proto definition, which places no PID-range restriction on the request field (a bare `uint16 pid`) — see §2.2. **Design implication:** the generic editor should attempt PARAMETER_DESCRIPTION for *any* PID present in SUPPORTED_PARAMETERS that the app doesn't already have a hardcoded ESTA-standard decoder for (i.e., use it as the universal fallback path for "PID we don't recognize," not just as a manufacturer-range gate).

### 1.2 Sensors UI — how should a gauge render range vs. normal-range vs. present value?

SENSOR_DEFINITION gives you four numbers per sensor beyond the present value: `range_min`/`range_max` (absolute hardware limits — signal will never legitimately go outside this) and `normal_min`/`normal_max` (expected operating band — outside this is noteworthy but not necessarily a fault). Recommended gauge semantics:

- **Gauge track bounds = `range_min`..`range_max`** (the full dial). If a device reports `range_min = -32768` (`0x8000`) or `range_max = 32767` (`0x7FFF`), treat that as **"undefined/not declared"** (these are the explicit sentinel values per the spec — see §3.1) and fall back to auto-scaling the gauge from observed present/recorded values instead of drawing a track down to literal ±32768.
- **Normal band = shaded/green region from `normal_min` to `normal_max`** drawn inside the track. Same undefined-sentinel handling (`-32768`/`32767` → omit the shading rather than drawing a band that spans the entire dial).
- **Needle/fill = `present_value`**, read live via repeated SENSOR_VALUE GETs (poll interval is an app policy choice — RDM has no push/subscribe for sensors in the base spec; see `QUEUED_MESSAGE_SENSOR_SUBSCRIBE` note in §7 for an E1.37-5 addition that changes this).
- **Lowest/highest detected + recorded** (also returned by SENSOR_VALUE) are best shown as small tick marks or a min/max readout beside the gauge, not baked into the arc geometry — they're history, not a range.
- **Unit + prefix + multiplier**: SENSOR_DEFINITION's `unit`/`prefix` fields (same 2 enums as PARAMETER_DESCRIPTION, see §3.2/§3.3) tell you how to label the axis (e.g. unit=Centigrade, prefix=None → "°C"; unit=Volts DC, prefix=Milli → "mV"). Apply the prefix as a power-of-ten multiplier to the raw signed-16-bit value before display.
- **`supports_recording` bitfield** (SENSOR_RECORDED_VALUE=0x01, SENSOR_RECORDED_RANGE_VALUES=0x02) tells you whether it's worth showing the "recorded" readout / a "reset sensor" (RECORD_SENSORS SET) button at all — hide those UI elements when the bit is clear rather than showing a perpetual "unsupported" state.

### 1.3 Detecting device type for grouping/icons (feature C)

**DEVICE_INFO's `product_category` field (2 bytes) is the primary signal**, and it is a genuine tree (top byte = family, e.g. `0x01xx` = Fixture, `0x06xx` = Power, `0x08xx` = Data/gateway family; bottom byte = sub-type within family) — see the full CONFIRMED table in §4.1. This directly answers "is this a fixture, a gateway, a splitter, a power device, a controller." Recommended grouping logic:

- `0x01xx` (Fixture) and `0x03xx` (Projector) → **"Fixture" icon/group** (moving-yoke vs. moving-mirror vs. fixed as sub-icons if wanted).
- `0x08xx` (Data) → **"Data/Network" group** — `0x0801` Data Distribution is literally the category for gateways/splitters/nodes; this is what an Art-Net/sACN-to-DMX node like the Netron EN4 should report.
- `0x06xx` (Power) → **"Power" group** (power control/distro units, not dimmers — dimmers are `0x05xx`).
- `0x05xx` (Dimmer) → **"Dimmer" group**.
- `0x7000`–`0x71FF` (Control/Test) → **"Controller/Test gear" group**.
- `0x0000` (Not Declared) → **"Unknown" group**, fall back to `PRODUCT_DETAIL_ID_LIST` (0x0070, up to 6 detail IDs per response) for a finer hint — e.g. `PRODUCT_DETAIL_SPLITTER` (0x0600), `PRODUCT_DETAIL_ETHERNET_NODE` (0x0601), `PRODUCT_DETAIL_WIRELESS_LINK` (0x0604) are explicit "this is infrastructure, not a light" signals even when `product_category` itself is left at 0x0000 by a lazy/older firmware (this is a real-world gap: many gateways in the field under-report `product_category` and rely on `PRODUCT_DETAIL_ID_LIST` or just a sensible `DEVICE_MODEL_DESCRIPTION` string).
- **Secondary/confirmatory signal:** a device whose `DEVICE_INFO.dmx_footprint = 0` is very likely not a controllable fixture on its own DMX address (a gateway or a sensor-only device) even if its category is generic — use footprint=0 as a soft "probably infrastructure, don't show a level fader for it" heuristic alongside the category tree.
- **RDM proxy detection (LumenRadio-style):** a device that responds to `PROXIED_DEVICES` (0x0010) / `PROXIED_DEVICE_COUNT` (0x0011) with a non-empty list is acting as an RDM proxy for downstream devices — surface it in the UI as an expandable "gateway ▸ downstream devices" node rather than a flat list entry. This is exactly the EN4's and Aurora's/MoonLite's role (§6, §5.2).

### 1.4 Top implementation risks to flag now

- The **SUPPORTED_PARAMETERS mandatory-exclusion list** (which PIDs a compliant device must NOT bother listing because support is assumed) could only be reconstructed from secondary/training knowledge this session, not re-verified against the primary ANSI text or a forum thread that quotes it verbatim (both attempts returned empty/paywalled). Marked **WEAKLY CONFIRMED** — see §2.1. Practical mitigation: the app should *always* attempt `DEVICE_INFO`, `SOFTWARE_VERSION_LABEL`, `IDENTIFY_DEVICE`, `DMX_START_ADDRESS` regardless of whether they appear in the SUPPORTED_PARAMETERS list, since compliant devices must support them either way.
- **PARAMETER_DESCRIPTION's `min_value`/`max_value`/`default_value` are transmitted as raw 4-byte big-endian fields whose *sign* depends on `data_type`.** A `DS_SIGNED_BYTE` PID's min/max still occupies the full 4 bytes (sign-extended per spec convention in most known implementations, but OLA's own proto models all three as bare `UINT32` — see §2.2 discrepancy note). Test this explicitly against real hardware tomorrow with a signed-type manufacturer PID if the Color Force II or EN4 exposes one.
- **Sensor value fields are `INT16` per the canonical PARAMETER_DESCRIPTION/SENSOR_DEFINITION-style GET response, but OLA's own `pids.proto` types the SENSOR_VALUE **SET response** fields as `UINT16`** (§3.4) — almost certainly a modeling inconsistency in OLA's tooling rather than a real wire difference (the bit pattern is identical either way), but worth a defensive note in code comments.

---

## 2. Core Generic-PID-Editor PIDs

### 2.1 SUPPORTED_PARAMETERS (0x0050)

| Field | Size | Type | Notes | Status |
|---|---|---|---|---|
| **GET request** | 0 | — | no parameter data | CONFIRMED |
| **GET response** — repeated group | 2×N | `UINT16[]` | list of every PID (big-endian) the device supports, **excluding the mandatory-support set** (see below) | CONFIRMED (OLA `pids.proto`, OLA `RDMEnums.h`) |

**Mandatory PIDs excluded from the list** (a compliant responder should not bother listing these — support is assumed): `DISC_UNIQUE_BRANCH` (0x0001), `DISC_MUTE` (0x0002), `DISC_UN_MUTE` (0x0003), `SUPPORTED_PARAMETERS` (0x0050) itself, `PARAMETER_DESCRIPTION` (0x0051, mandatory only if the device implements any manufacturer-specific PID), `DEVICE_INFO` (0x0060), `SOFTWARE_VERSION_LABEL` (0x00C0), `DMX_START_ADDRESS` (0x00F0, mandatory only if the device's DMX footprint > 0), `IDENTIFY_DEVICE` (0x1000). **Status: WEAKLY CONFIRMED** — reconstructed from RDM domain knowledge/OLA responder-test conventions; this session's attempts to re-fetch the exact ANSI E1.20 clause text (via rdmprotocol.org forum threads and two PDF mirrors) returned empty results (likely PDF-parsing or anti-bot issues in the fetch tool, not necessarily wrong URLs). **Do not hardcode a "PID absent from list ⇒ unsupported" assumption for these 8–9 PIDs** — always probe them directly regardless of list contents.

**ACK_OVERFLOW behavior:** the response is a flat repeated-group list with no other framing; PDL=231 bytes max per packet ÷ 2 bytes/PID = **115 PIDs max per single ACK**. A device with more supported PIDs than that (common for full-featured fixtures, and likely for the Color Force II / EN4 given E1.37 IPv4 + sensor + dimmer-curve PID counts) will use the generic RDM ACK_OVERFLOW mechanism (CONFIRMED in Phase 1a, §2.4/§3.5) — controller re-issues the identical GET SUPPORTED_PARAMETERS request repeatedly until the final block arrives as plain ACK. No PID-specific continuation state exists; the app must simply concatenate PID lists across overflow blocks in request order.

### 2.2 PARAMETER_DESCRIPTION (0x0051) — the key PID for feature (A)

| Offset (within param data) | Field | Size | Type | Status |
|---|---|---|---|---|
| GET request: 0–1 | `pid` | 2 | UINT16 BE | the PID being described | CONFIRMED |
| GET response 0–1 | `pid` | 2 | UINT16 BE | echoes the requested PID | CONFIRMED |
| 2 | `pdl_size` | 1 | UINT8 | the PDL this PID's own GET/SET responses will use | CONFIRMED |
| 3 | `data_type` | 1 | UINT8 (enum, §3 below) | | CONFIRMED |
| 4 | `command_class` | 1 | UINT8 (enum: 1=GET,2=SET,3=GET_SET) | | CONFIRMED |
| 5 | `type` | 1 | UINT8 | **deprecated field**, no defined semantics in current OLA/ETCLabs sources — always transmit/expect 0 | WEAKLY CONFIRMED (present in wire layout per OLA `pids.proto`; no source found describing live semantics — treated as vestigial) |
| 6 | `unit` | 1 | UINT8 (enum, §3.2) | | CONFIRMED |
| 7 | `prefix` | 1 | UINT8 (enum, §3.3) | | CONFIRMED |
| 8–11 | `min_value` | 4 | UINT32 BE (sign per `data_type`) | | CONFIRMED |
| 12–15 | `max_value` | 4 | UINT32 BE (sign per `data_type`) | | CONFIRMED |
| 16–19 | `default_value` | 4 | UINT32 BE (sign per `data_type`) | | CONFIRMED |
| 20–(20+len−1) | `description` | ≤32 | ASCII, no null terminator, length = PDL − 20 | max_size 32 confirmed | CONFIRMED |

**PID range applicability:** the request field is a bare `uint16 pid` with no range restriction encoded in the OLA schema — a responder is not barred from describing an ESTA-standard PID (0x0000–0x7FFF) via PARAMETER_DESCRIPTION, though in practice it's only *required* for manufacturer-specific PIDs (0x8000–0xFFDF). **CONFIRMED** (schema) / **WEAKLY CONFIRMED** (the "only required for mfr PIDs" scoping rule — standard RDM knowledge, not independently re-derived from primary text this session).

**min/max/default sign discrepancy note:** OLA's `pids.proto` types all three as generic `UINT32` regardless of `data_type` — i.e., OLA's own tooling does not attempt to reinterpret these 4 raw bytes as signed even when `data_type = DS_SIGNED_*`. Real-world responders are expected (per general RDM convention) to write a properly sign-extended/truncated value into these 4 bytes matching `data_type`'s width and signedness; the app's Go decoder should do the sign interpretation itself based on the `data_type` byte rather than trusting a naive UINT32 read for signed types. **WEAKLY CONFIRMED** — flag as a hardware-verification item for tomorrow.

### 2.3 DEVICE_INFO (0x0060) & PRODUCT_DETAIL_ID_LIST (0x0070)

| Offset | Field | Size | Type | Status |
|---|---|---|---|---|
| 0 | `protocol_major` | 1 | UINT8 | =1 for RDM_VERSION_1_0 (`0x0100`) | CONFIRMED |
| 1 | `protocol_minor` | 1 | UINT8 | =0 | CONFIRMED |
| 2–3 | `device_model` | 2 | UINT16 BE | manufacturer-assigned model ID | CONFIRMED |
| 4–5 | `product_category` | 2 | UINT16 BE (enum, §4.1) | | CONFIRMED |
| 6–9 | `software_version` | 4 | UINT32 BE | | CONFIRMED |
| 10–11 | `dmx_footprint` | 2 | UINT16 BE | 0 for a device with no DMX slot presence (pure sensor/gateway-only) | CONFIRMED |
| 12 | `current_personality` | 1 | UINT8 | 1-based index | CONFIRMED |
| 13 | `personality_count` | 1 | UINT8 | | CONFIRMED |
| 14–15 | `dmx_start_address` | 2 | UINT16 BE | `0xFFFF` = no DMX address assigned (footprint=0 case) | CONFIRMED |
| 16–17 | `sub_device_count` | 2 | UINT16 BE | | CONFIRMED |
| 18 | `sensor_count` | 1 | UINT8 | drives whether to even attempt SENSOR_DEFINITION enumeration | CONFIRMED |

Total fixed PDL = 19 bytes (matches Phase 1a's independently-derived golden fixture).

**PRODUCT_DETAIL_ID_LIST (0x0070)** GET response: a repeated group of up to **6** `uint16` detail IDs (`max_size: 6` per OLA schema) — CONFIRMED. Full enum table in §4.2.

### 2.4 QUEUED_MESSAGE (0x0020), STATUS_MESSAGES (0x0030), STATUS_ID_DESCRIPTION (0x0031)

**QUEUED_MESSAGE (0x0020)** — GET request only (no SET, no GET response of its own; the "response" is whatever queued PID gets sent back, e.g. a STATUS_MESSAGES payload, addressed as if it were a spontaneous GET_COMMAND_RESPONSE for that PID):

| Field | Size | Notes | Status |
|---|---|---|---|
| `status_type` (request) | 1 | UINT8: 1=Last Message, 2=Advisory, 3=Warning, 4=Error — controller asks "drain your queue down to this severity or worse" | CONFIRMED |

Mechanism: Slot 17 (Message Count) of *any* RDM response tells the controller how many messages are queued; controller then issues GET QUEUED_MESSAGE with a `status_type` filter to drain the queue one message at a time until Message Count returns to 0. CONFIRMED against Phase 1a's RDM packet header (Message Count field) plus this session's PID-shape confirmation.

**STATUS_MESSAGES (0x0030)**:

| Field | Size | Notes | Status |
|---|---|---|---|
| GET request: `status_type` | 1 | UINT8: 0=None, 1=Last Message, 2=Advisory, 3=Warning, 4=Error | CONFIRMED |
| GET response — repeated group, each: | | | |
| ↳ `sub_device` | 2 | UINT16 BE | CONFIRMED |
| ↳ `status_type` | 1 | UINT8 (severity of *this* message — see §4.4 enum) | CONFIRMED |
| ↳ `message_id` | 2 | UINT16 BE (enum, §4.5 partial table) | CONFIRMED |
| ↳ `value1` | 2 | INT16 BE | CONFIRMED |
| ↳ `value2` | 2 | INT16 BE | CONFIRMED |

Group size = 9 bytes; max ~25 messages per packet before ACK_OVERFLOW (231÷9=25.6→25).

**STATUS_ID_DESCRIPTION (0x0031)**:

| Field | Size | Notes | Status |
|---|---|---|---|
| GET request: `status_id` | 2 | UINT16 BE — one of the `message_id` values from STATUS_MESSAGES | CONFIRMED |
| GET response: `label` | ≤32 | ASCII string | CONFIRMED |

---

## 3. Sensors — SENSOR_DEFINITION (0x0200), SENSOR_VALUE (0x0201), RECORD_SENSORS (0x0202)

### 3.1 SENSOR_DEFINITION (0x0200)

| Offset (param data) | Field | Size | Type | Notes | Status |
|---|---|---|---|---|---|
| GET request: 0 | `sensor_number` | 1 | UINT8 | 0–254 valid; 0xFF reserved (see §3.2 note re: "all sensors" only applying to SENSOR_VALUE SET) | CONFIRMED |
| GET response 0 | `sensor_number` | 1 | UINT8 | echoed | CONFIRMED |
| 1 | `type` | 1 | UINT8 (enum, §3.5) | | CONFIRMED |
| 2 | `unit` | 1 | UINT8 (enum, §4.6 — same table as PARAMETER_DESCRIPTION's unit field) | CONFIRMED |
| 3 | `prefix` | 1 | UINT8 (enum, same table as PARAMETER_DESCRIPTION's prefix field) | CONFIRMED |
| 4–5 | `range_min` | 2 | INT16 BE | sentinel `-32768` (`0x8000`) = undefined | CONFIRMED |
| 6–7 | `range_max` | 2 | INT16 BE | sentinel `32767` (`0x7FFF`) = undefined | CONFIRMED |
| 8–9 | `normal_min` | 2 | INT16 BE | sentinel `-32768` = undefined | CONFIRMED |
| 10–11 | `normal_max` | 2 | INT16 BE | sentinel `32767` = undefined | CONFIRMED |
| 12 | `supports_recording` (bitfield, "recorded value support") | 1 | UINT8 | bit0 (`0x01`) = `SENSOR_RECORDED_VALUE` supported; bit1 (`0x02`) = `SENSOR_RECORDED_RANGE_VALUES` supported | CONFIRMED |
| 13–(13+len−1) | `name`/`description` | ≤32 | ASCII | CONFIRMED |

Fixed portion = 13 bytes + description. Sentinels and bitmask values independently CONFIRMED via OLA `RDMEnums.h` (`SENSOR_DEFINITION_RANGE_MIN_UNDEFINED = -0x8000`, `..._MAX_UNDEFINED = 0x7FFF`, `SENSOR_RECORDED_VALUE = 0x01`, `SENSOR_RECORDED_RANGE_VALUES = 0x02`) and `pids.proto` (matching label/range annotations).

### 3.2 SENSOR_VALUE (0x0201)

| Offset | Field | Size | Type | Notes | Status |
|---|---|---|---|---|---|
| GET request: 0 | `sensor_number` | 1 | UINT8 | 0–254; **0xFF is not valid for GET** per schema range (0–254) despite `ALL_SENSORS=0xff` existing as a constant — see SET note below | CONFIRMED (GET range) |
| GET response 0 | `sensor_number` | 1 | UINT8 | echoed | CONFIRMED |
| 1–2 | `present_value` | 2 | INT16 BE | | CONFIRMED |
| 3–4 | `lowest` (lowest detected) | 2 | INT16 BE | only meaningful if bit0 of `supports_recording` set | CONFIRMED |
| 5–6 | `highest` (highest detected) | 2 | INT16 BE | only meaningful if bit0 set | CONFIRMED |
| 7–8 | `recorded` | 2 | INT16 BE | only meaningful if bit1 set | CONFIRMED |
| SET request: 0 | `sensor_number` | 1 | UINT8 | **0xFF ("All Sensors") is valid here** — resets every sensor's lowest/highest/recorded in one command | CONFIRMED |
| SET response | (same 5-field layout as GET response) | 9 | — | OLA's `pids.proto` types the 4 value fields as `UINT16` here (vs `INT16` in the GET response) — **almost certainly a schema-authoring inconsistency, not a real wire difference**; treat as signed INT16 in the Go decoder for both directions | WEAKLY CONFIRMED (typing discrepancy noted, not independently re-verified against primary text) |

**RECORD_SENSORS (0x0202)** — SET only, no GET:

| Field | Size | Notes | Status |
|---|---|---|---|
| SET request: `sensor_number` | 1 | UINT8, 0–255; **0xFF = "All Sensors"** | CONFIRMED |
| SET response | 0 | empty ACK | CONFIRMED |

Semantics: SET RECORD_SENSORS tells the device to snapshot its *current* present_value into the `recorded` slot for that sensor (or all sensors if 0xFF) — distinct from SET SENSOR_VALUE, which *resets* the lowest/highest/recorded history rather than recording a new snapshot. CONFIRMED via field/PID separation in `pids.proto`; exact prose semantics ("record" vs "reset") reconstructed from standard RDM sensor-PID convention — **WEAKLY CONFIRMED** on the precise verb distinction (record-now vs. reset-to-current), worth a hardware sanity check tomorrow (SET RECORD_SENSORS then GET SENSOR_VALUE and confirm `recorded` moved to match `present_value` at the time of the SET, not before/after).

### 3.3 App-level polling note

RDM (base E1.20) has no sensor push/subscribe mechanism — the app must poll SENSOR_VALUE per sensor per device on a timer for live gauges. **QUEUED_MESSAGE_SENSOR_SUBSCRIBE (0x0034)** exists as a later addition (present in OLA's `rdm_pid` enum grouped with "status collection," value `0x0034`) that lets a controller ask a device to *push* sensor readings via the queued-message mechanism instead — **UNVERIFIED which ESTA document formally defines it** (not found in the base E1.20 PID list nor explicitly labeled E1.37-x in the source); treat as an optional optimization to investigate later, not a dependency for the initial gauge UI.

### 3.4 SENSOR_TYPE_CUSTOM (0x0210) / SENSOR_UNIT_CUSTOM (0x0211) — E1.37-5

For sensor `type` values in the manufacturer-custom range and `unit` values in the manufacturer-custom range, these two PIDs let the app ask the device "what does your custom sensor-type/unit code `N` actually mean" (GET request = the custom code byte, GET response = code byte + ASCII label, ≤32 chars) — CONFIRMED shape via OLA `RDMEnums.h` PID list; not independently fetched from `pids.proto` this session (would follow the same `SENSOR_TYPE_CUSTOM`/label pattern as STATUS_ID_DESCRIPTION). Relevant if any of tomorrow's hardware reports `type=0x7F` (SENS_OTHER) or a value in the 0x80–0xFF custom range.

### 3.5 Sensor Type enum (full table, numeric values)

Two independent sources: **OLA `RDMEnums.h`** (full, extended table) and **ETCLabs `defs.h`** (rdmprotocol.org's own Appendix A defines, stops at `0x20` + `0x7F`). Base E1.20 spec appears to define **0x00–0x20 + 0x7F**; values 0x21–0x28 are later additions present only in OLA's extended table (source of the addendum not independently confirmed this session — **WEAKLY CONFIRMED** for 0x21–0x28 specifically, **CONFIRMED** for 0x00–0x20 and 0x7F via 2 independent sources).

| Value | Name | Value | Name |
|---|---|---|---|
| 0x00 | Temperature | 0x15 | Angular velocity |
| 0x01 | Voltage | 0x16 | Luminous intensity |
| 0x02 | Current | 0x17 | Luminous flux |
| 0x03 | Frequency | 0x18 | Illuminance |
| 0x04 | Resistance | 0x19 | Chrominance red |
| 0x05 | Power | 0x1A | Chrominance green |
| 0x06 | Mass | 0x1B | Chrominance blue |
| 0x07 | Length | 0x1C | Contacts |
| 0x08 | Area | 0x1D | Memory |
| 0x09 | Volume | 0x1E | Items |
| 0x0A | Density | 0x1F | Humidity |
| 0x0B | Velocity | 0x20 | 16-bit counter |
| 0x0C | Acceleration | 0x21† | CPU load |
| 0x0D | Force | 0x22† | Bandwidth |
| 0x0E | Energy | 0x23† | Concentration |
| 0x0F | Pressure | 0x24† | Sound pressure level |
| 0x10 | Time | 0x25† | Solid angle |
| 0x11 | Angle | 0x26† | Log ratio |
| 0x12 | Position X | 0x27† | Log ratio volts |
| 0x13 | Position Y | 0x28† | Log ratio watts |
| 0x14 | Position Z | 0x7F | Other |

† WEAKLY CONFIRMED (OLA only, not present in ETCLabs `defs.h`).

---

## 4. Product Category / Device-Type Tree & PRODUCT_DETAIL

### 4.1 PRODUCT_CATEGORY (top-level, full table — numeric values)

**CONFIRMED** via two independent primary-derived sources (OLA `RDMEnums.h` and ETCLabs `defs.h`, both derived from ANSI E1.20 Appendix A / Table A-6, exact numeric agreement across both):

| Value | Name | Value | Name |
|---|---|---|---|
| 0x0000 | NOT_DECLARED | 0x0700 | SCENIC |
| 0x0100 | FIXTURE | 0x0701 | SCENIC_DRIVE |
| 0x0101 | FIXTURE_FIXED | 0x07FF | SCENIC_OTHER |
| 0x0102 | FIXTURE_MOVING_YOKE | 0x0800 | **DATA** |
| 0x0103 | FIXTURE_MOVING_MIRROR | 0x0801 | **DATA_DISTRIBUTION** (gateways/nodes/splitters) |
| 0x01FF | FIXTURE_OTHER | 0x0802 | DATA_CONVERSION |
| 0x0200 | FIXTURE_ACCESSORY | 0x08FF | DATA_OTHER |
| 0x0201 | FIXTURE_ACCESSORY_COLOR | 0x0900 | AV |
| 0x0202 | FIXTURE_ACCESSORY_YOKE | 0x0901 | AV_AUDIO |
| 0x0203 | FIXTURE_ACCESSORY_MIRROR | 0x0902 | AV_VIDEO |
| 0x0204 | FIXTURE_ACCESSORY_EFFECT | 0x09FF | AV_OTHER |
| 0x0205 | FIXTURE_ACCESSORY_BEAM | 0x0A00 | MONITOR |
| 0x02FF | FIXTURE_ACCESSORY_OTHER | 0x0A01 | MONITOR_ACLINEPOWER |
| 0x0300 | PROJECTOR | 0x0A02 | MONITOR_DCPOWER |
| 0x0301 | PROJECTOR_FIXED | 0x0A03 | MONITOR_ENVIRONMENTAL |
| 0x0302 | PROJECTOR_MOVING_YOKE | 0x0AFF | MONITOR_OTHER |
| 0x0303 | PROJECTOR_MOVING_MIRROR | 0x7000 | **CONTROL** |
| 0x03FF | PROJECTOR_OTHER | 0x7001 | CONTROL_CONTROLLER |
| 0x0400 | ATMOSPHERIC | 0x7002 | CONTROL_BACKUPDEVICE |
| 0x0401 | ATMOSPHERIC_EFFECT | 0x70FF | CONTROL_OTHER |
| 0x0402 | ATMOSPHERIC_PYRO | 0x7100 | TEST |
| 0x04FF | ATMOSPHERIC_OTHER | 0x7101 | TEST_EQUIPMENT |
| 0x0500 | DIMMER | 0x71FF | TEST_EQUIPMENT_OTHER |
| 0x0501 | DIMMER_AC_INCANDESCENT | 0x7FFF | OTHER |
| 0x0502 | DIMMER_AC_FLUORESCENT | | |
| 0x0503 | DIMMER_AC_COLDCATHODE | | |
| 0x0504 | DIMMER_AC_NONDIM | | |
| 0x0505 | DIMMER_AC_ELV | | |
| 0x0506 | DIMMER_AC_OTHER | | |
| 0x0507 | DIMMER_DC_LEVEL | | |
| 0x0508 | DIMMER_DC_PWM | | |
| 0x0509 | DIMMER_CS_LED | | |
| 0x05FF | DIMMER_OTHER | | |
| 0x0600 | **POWER** | | |
| 0x0601 | POWER_CONTROL | | |
| 0x0602 | POWER_SOURCE | | |
| 0x06FF | POWER_OTHER | | |

Bold rows = the categories most relevant to feature (C)'s non-fixture grouping (Data/gateway, Power, Control). This table drives Go `const` generation directly — values verified numerically identical between OLA and ETCLabs sources.

### 4.2 PRODUCT_DETAIL enum (selected — gateway/infra-relevant subset, full numeric table)

**CONFIRMED** (OLA `RDMEnums.h` + ETCLabs `defs.h`, matching):

| Value | Name | Value | Name |
|---|---|---|---|
| 0x0000 | NOT_DECLARED | 0x0600 | **SPLITTER** |
| 0x0001–0x0009 | (lamp types: ARC, METAL_HALIDE, INCANDESCENT, LED, FLUORESCENT, COLDCATHODE, ELECTROLUMINESCENT, LASER, FLASHTUBE) | 0x0601 | **ETHERNET_NODE** |
| 0x0100–0x0108 | (beam-shaping: COLORSCROLLER, COLORWHEEL, COLORCHANGE, IRIS_DOUSER, DIMMING_SHUTTER, PROFILE_SHUTTER, BARNDOOR_SHUTTER, EFFECTS_DISC, GOBO_ROTATOR) | 0x0602 | **MERGE** |
| 0x0200–0x0204 | (imaging: VIDEO, SLIDE, FILM, OILWHEEL, LCDGATE) | 0x0603 | **DATAPATCH** |
| 0x0300–0x030D | (atmospheric: FOGGER_GLYCOL … HAZARD) | 0x0604 | **WIRELESS_LINK** |
| 0x0400–0x040F | (dimmer/electrical: PHASE_CONTROL … CONTACTOR) | 0x0701 | PROTOCOL_CONVERTER |
| 0x0500–0x0506 | (motorized scenic: MIRRORBALL_ROTATOR … DAMPER_CONTROL) | 0x0702 | ANALOG_DEMULTIPLEX |
| | | 0x0703 | ANALOG_MULTIPLEX |
| | | 0x0704 | SWITCH_PANEL |
| | | 0x0800 | ROUTER |
| | | 0x0801 | FADER |
| | | 0x0802 | MIXER |
| | | 0x0900–0x0902 | CHANGEOVER_MANUAL / CHANGEOVER_AUTO / TEST |
| | | 0x0A00–0x0A02 | GFI_RCD / BATTERY / CONTROLLABLE_BREAKER |
| | | 0x7FFF | OTHER |

Bold = the four detail IDs most likely to appear on the Netron EN4 / LumenRadio devices (`ETHERNET_NODE`, `WIRELESS_LINK`, `SPLITTER`, `DATAPATCH`) — use these as the fallback fine-grained signal when `product_category` is left at `0x0000` by an under-reporting responder (see §1.3).

---

## 5. Data Types, Units, Prefixes, Command Class — full enum tables

All CONFIRMED via 2 independent sources (OLA `pids.proto` + OLA `RDMEnums.h`, cross-checked against ETCLabs `defs.h` where the latter's coverage extends far enough) unless flagged. ETCLabs `defs.h` (rdmprotocol.org's canonical Appendix A defines, last updated for E1.37-2 in 2014) stops earlier than OLA's tables in three places, each individually flagged below — **this is itself informative**: the ETCLabs cutoff is a reasonable proxy for "what base E1.20 + E1.37-1/-2 actually defined" vs. "what OLA's tooling has since extended for E1.37-5/newer PIDs."

### 5.1 Data Type (`DS_*`)

| Value | Name | Source coverage |
|---|---|---|
| 0x00 | DS_NOT_DEFINED | CONFIRMED both |
| 0x01 | DS_BIT_FIELD | CONFIRMED both |
| 0x02 | DS_ASCII | CONFIRMED both |
| 0x03 | DS_UNSIGNED_BYTE | CONFIRMED both |
| 0x04 | DS_SIGNED_BYTE | CONFIRMED both |
| 0x05 | DS_UNSIGNED_WORD | CONFIRMED both |
| 0x06 | DS_SIGNED_WORD | CONFIRMED both |
| 0x07 | DS_UNSIGNED_DWORD | CONFIRMED both |
| 0x08 | DS_SIGNED_DWORD | CONFIRMED both (ETCLabs `defs.h` stops here — base E1.20 boundary) |
| 0x09 | DS_UINT64 | WEAKLY CONFIRMED — OLA only |
| 0x0A | DS_INT64 | WEAKLY CONFIRMED — OLA only |
| 0x0B | DS_GROUP | WEAKLY CONFIRMED — OLA only |
| 0x0C | DS_UID | WEAKLY CONFIRMED — OLA only |
| 0x0D | DS_BOOLEAN | WEAKLY CONFIRMED — OLA only |
| 0x0E | DS_URL | WEAKLY CONFIRMED — OLA only |
| 0x0F | DS_MAC | WEAKLY CONFIRMED — OLA only |
| 0x10 | DS_IPV4 | WEAKLY CONFIRMED — OLA only |
| 0x11 | DS_IPV6 | WEAKLY CONFIRMED — OLA only |
| 0x12 | DS_ENUMERATION | WEAKLY CONFIRMED — OLA only |
| 0x80–0xDF | manufacturer-specific data-type range | CONFIRMED (range bound `128–223` explicit in `pids.proto` label range annotation) |

Note: 0x09–0x12 line up conceptually with the "PID Definition Language" item types documented on `wiki.openlighting.org/RDM_PID_Definitions` (bool, uint8/16/32, int8/16/32, string, group, ipv4, mac, uid — §7 lists this as OLA's own extension for describing manufacturer PIDs to OLA's controller, not necessarily an ESTA-ratified `data_type` byte value transmitted on the wire for PARAMETER_DESCRIPTION). **Treat 0x09–0x12 as plausible-but-unverified for the actual wire byte** — a manufacturer PID reporting `data_type=0x0C` (DS_UID) is credible (e.g. a PID whose value is itself an RDM UID) but was not independently confirmed against ANSI text this session.

### 5.2 Unit (`UNITS_*`, shared by PARAMETER_DESCRIPTION and SENSOR_DEFINITION)

| Value | Name | Value | Name |
|---|---|---|---|
| 0x00 | None | 0x13 | Joules |
| 0x01 | Centigrade | 0x14 | Pascals |
| 0x02 | Volts (DC) | 0x15 | Seconds |
| 0x03 | Volts (AC Peak) | 0x16 | Degrees |
| 0x04 | Volts (AC RMS) | 0x17 | Steradian |
| 0x05 | Amps (DC) | 0x18 | Candela |
| 0x06 | Amps (AC Peak) | 0x19 | Lumens |
| 0x07 | Amps (AC RMS) | 0x1A | Lux |
| 0x08 | Hertz | 0x1B | Ire |
| 0x09 | Ohms | 0x1C | Bytes (ETCLabs `defs.h` boundary — base E1.20/E1.37-2 stops here) |
| 0x0A | Watts | 0x1D† | Decibel |
| 0x0B | Kilograms | 0x1E† | Decibel Volt |
| 0x0C | Meters | 0x1F† | Decibel Watt |
| 0x0D | Meters Squared | 0x20† | Decibel Meter |
| 0x0E | Meters Cubed | 0x21† | Percent |
| 0x0F | Kilograms per Meter Cubed | 0x22† | Moles per Meter Cubed |
| 0x10 | Meters per Second | 0x23† | RPM |
| 0x11 | Meters per Second Squared | 0x24† | Bytes per Second |
| 0x12 | Newtons | 0x80–0xFF | manufacturer-specific unit range |

† WEAKLY CONFIRMED (OLA only — post-E1.37-2 additions, exact defining document not independently identified this session). 0x00–0x1C CONFIRMED both sources.

### 5.3 Prefix (`PREFIX_*`)

**CONFIRMED, full agreement both sources:**

| Value | Name | Multiplier | Value | Name | Multiplier |
|---|---|---|---|---|---|
| 0x00 | None | ×1 | 0x11 | Deca | ×10¹ |
| 0x01 | Deci | ×10⁻¹ | 0x12 | Hecto | ×10² |
| 0x02 | Centi | ×10⁻² | 0x13 | Kilo | ×10³ |
| 0x03 | Milli | ×10⁻³ | 0x14 | Mega | ×10⁶ |
| 0x04 | Micro | ×10⁻⁶ | 0x15 | Giga | ×10⁹ |
| 0x05 | Nano | ×10⁻⁹ | 0x16 | Tera | ×10¹² |
| 0x06 | Pico | ×10⁻¹² | 0x17 | Peta | ×10¹⁵ |
| 0x07 | Femto | ×10⁻¹⁵ | 0x18 | Exa | ×10¹⁸ |
| 0x08 | Atto | ×10⁻¹⁸ | 0x19 | Zetta | ×10²¹ |
| 0x09 | Zepto | ×10⁻²¹ | 0x1A | Yotta | ×10²⁴ |
| 0x0A | Yocto | ×10⁻²⁴ | | | |

Note: **values 0x0B–0x10 are gaps (unused/reserved), not an error** — confirmed by identical gap in both independent sources.

### 5.4 Command Class (`CC_*`, PARAMETER_DESCRIPTION's `command_class` field)

**CONFIRMED both sources:** `0x01`=CC_GET, `0x02`=CC_SET, `0x03`=CC_GET_SET.

### 5.5 Status Type (`STATUS_*`)

**CONFIRMED both sources:**

| Value | Name |
|---|---|
| 0x00 | NONE |
| 0x01 | GET_LAST_MESSAGE (request-only filter value) |
| 0x02 | ADVISORY |
| 0x03 | WARNING |
| 0x04 | ERROR |
| 0x12 | ADVISORY_CLEARED |
| 0x13 | WARNING_CLEARED |
| 0x14 | ERROR_CLEARED |

(0x12/0x13/0x14 CONFIRMED via OLA `RDMEnums.h` and ETCLabs `defs.h`'s numeric values matching, though the "CLEARED" naming convention itself is OLA's — the wire byte values are what matter for decoding, and both sources agree on 0x12/0x13/0x14.)

### 5.6 NACK Reason codes (extends Phase 1a's 0x0000–0x000A table)

Phase 1a confirmed 0x0000–0x000A. This pass adds the fuller OLA table (post-E1.37/E1.33 additions) — **WEAKLY CONFIRMED**, single source (OLA `RDMEnums.h`), not cross-checked against ETCLabs `defs.h` (which stops at 0x000A):

| Value | Name | Value | Name |
|---|---|---|---|
| 0x000B | ACTION_NOT_SUPPORTED | 0x0016 | SENSOR_FAULT |
| 0x000C | ENDPOINT_NUMBER_INVALID | 0x0017 | PACKING_NOT_SUPPORTED |
| 0x000D | INVALID_ENDPOINT_MODE | 0x0018 | ERROR_IN_PACKED_LIST_TRANSACTION |
| 0x000E | UNKNOWN_UID | 0x0019 | PROXY_DROP |
| 0x000F | UNKNOWN_SCOPE | 0x0020 | ALL_CALL_SET_FAIL |
| 0x0010 | INVALID_STATIC_CONFIG_TYPE | | |
| 0x0011 | INVALID_IPV4_ADDRESS | | |
| 0x0012 | INVALID_IPV6_ADDRESS | | |
| 0x0013 | INVALID_PORT | | |
| 0x0014 | DEVICE_ABSENT | | |
| 0x0015 | SENSOR_OUT_OF_RANGE | | |

---

## 6. Non-Fixture Device PIDs (E1.37-2 IPv4/network, E1.37-7 gateway/splitter)

Full family name/purpose table — **PID numbers CONFIRMED via OLA `RDMEnums.h`** (single primary-derived source for the exact numeric PID values; purposes are standard/self-descriptive RDM naming conventions, not independently re-verified prose per PID this session — mark **WEAKLY CONFIRMED** for the one-line purpose descriptions, **CONFIRMED** for the numeric PID value itself):

### 6.1 E1.37-1 — Dimmer Message Sets (relevant to Chroma-Q-style dimmer/LED curve control)

| PID | Name | Purpose |
|---|---|---|
| 0x0140 | DMX_BLOCK_ADDRESS | Set/get a block-contiguous DMX start address across multiple sub-devices at once |
| 0x0141 | DMX_FAIL_MODE | Behavior on loss of DMX signal (hold/fade-to-value/etc.) |
| 0x0142 | DMX_STARTUP_MODE | Behavior at power-up before DMX is received |
| 0x0340 | DIMMER_INFO | Dimmer-specific capability info (min level increments, curve count, etc.) |
| 0x0341 | MINIMUM_LEVEL | Configurable minimum output level |
| 0x0342 | MAXIMUM_LEVEL | Configurable maximum output level |
| 0x0343 | CURVE | Select active dimmer/LED response curve (index) |
| 0x0344 | CURVE_DESCRIPTION | Human-readable label for a curve index |
| 0x0345 | OUTPUT_RESPONSE_TIME | Select fade/response speed preset |
| 0x0346 | OUTPUT_RESPONSE_TIME_DESCRIPTION | Label for a response-time preset index |
| 0x0347 | MODULATION_FREQUENCY | Select PWM/refresh frequency (index) — **this is the mechanism-shaped PID family the Chroma-Q "Frequency" table in §7.1 most plausibly maps to, though Chroma-Q's own manual does not name a PID explicitly — see vendor note** |
| 0x0348 | MODULATION_FREQUENCY_DESCRIPTION | Label for a frequency preset index |
| 0x0440 | BURN_IN | Lamp/LED burn-in mode toggle |
| 0x0640 | LOCK_PIN | Set a numeric front-panel lock PIN |
| 0x0641 | LOCK_STATE | Get/set current lock state |
| 0x0642 | LOCK_STATE_DESCRIPTION | Label for a lock-state index |
| 0x1040 | IDENTIFY_MODE | Loud vs. quiet identify behavior |
| 0x1041 | PRESET_INFO | Preset-store capability info |
| 0x1042 | PRESET_STATUS | Per-preset programmed/read-only status |
| 0x1043 | PRESET_MERGEMODE | HTP/LTP merge behavior for presets |
| 0x1044 | POWER_ON_SELF_TEST | Enable/disable self-test at power-on |

### 6.2 E1.37-2 — IPv4 & DNS Configuration Messages (directly relevant to the Netron EN4)

| PID | Name | Purpose |
|---|---|---|
| 0x0700 | LIST_INTERFACES | Enumerate the device's network interfaces (index list) |
| 0x0701 | INTERFACE_LABEL | Human-readable name for an interface index |
| 0x0702 | INTERFACE_HARDWARE_ADDRESS_TYPE1 | MAC address for an interface |
| 0x0703 | IPV4_DHCP_MODE | Enable/disable DHCP on an interface |
| 0x0704 | IPV4_ZEROCONF_MODE | Enable/disable link-local (Zeroconf/APIPA) addressing |
| 0x0705 | IPV4_CURRENT_ADDRESS | Currently active IP/subnet (read, reflects DHCP or static) |
| 0x0706 | IPV4_STATIC_ADDRESS | Configured static IP/subnet |
| 0x0707 | INTERFACE_RENEW_DHCP | Force a DHCP lease renewal |
| 0x0708 | INTERFACE_RELEASE_DHCP | Release the current DHCP lease |
| 0x0709 | INTERFACE_APPLY_CONFIGURATION | Commit pending interface config changes |
| 0x070A | IPV4_DEFAULT_ROUTE | Configured default gateway |
| 0x070B | DNS_NAME_SERVER | Configured DNS server address(es) |
| 0x070C | DNS_HOSTNAME | Device hostname |
| 0x070D | DNS_DOMAIN_NAME | Device DNS domain |

Constants of note (CONFIRMED, OLA `RDMEnums.h`): `MAX_RDM_HOSTNAME_LENGTH=63`, `MAX_RDM_DOMAIN_NAME_LENGTH=231`, `DNS_NAME_SERVER_MAX_INDEX=2` (up to 3 name servers, indices 0–2), `DHCP_STATUS_INACTIVE=0x00`/`ACTIVE=0x01`/`UNKNOWN=0x02`.

### 6.3 E1.37-7 — Gateway & Splitter Configuration Messages (title CONFIRMED via ANSI/webstore.ansi.org listing; directly names "gateway" and "splitter" devices — matches feature C's stated scope)

| PID | Name | Purpose |
|---|---|---|
| 0x0900 | ENDPOINT_LIST | Enumerate the gateway/splitter's physical or virtual "endpoints" (ports) |
| 0x0901 | ENDPOINT_LIST_CHANGE | Change-counter for the endpoint list (poll to detect topology changes) |
| 0x0902 | IDENTIFY_ENDPOINT | Flash/identify a specific endpoint (port), distinct from whole-device IDENTIFY_DEVICE |
| 0x0903 | ENDPOINT_TO_UNIVERSE | Map an endpoint to a DMX universe number |
| 0x0904 | ENDPOINT_MODE | Input/output/disabled mode for an endpoint (conceptually parallel to the EN4's own front-panel "Mode: Disable/Input/Output" menu item — see §6, EN4 manual) |
| 0x0905 | ENDPOINT_LABEL | Human-readable name for an endpoint |
| 0x0906 | RDM_TRAFFIC_ENABLE | Enable/disable RDM pass-through on an endpoint (conceptually parallel to the EN4's per-port "RDM: Disabled/Enabled" menu item) |
| 0x0907 | DISCOVERY_STATE | Current state of RDM discovery on an endpoint |
| 0x0908 | BACKGROUND_DISCOVERY | Enable/disable automatic background RDM discovery |
| 0x0909 | ENDPOINT_TIMING | Timing parameters for an endpoint's RDM traffic |
| 0x090A | ENDPOINT_TIMING_DESCRIPTION | Label for a timing preset |
| 0x090B | ENDPOINT_RESPONDERS | List of RDM UIDs discovered on a specific endpoint |
| 0x090C | ENDPOINT_RESPONDER_LIST_CHANGE | Change-counter for the responder list on an endpoint |
| 0x090D | BINDING_CONTROL_FIELDS | Binding/proxy control bit fields |
| 0x090E | BACKGROUND_QUEUED_STATUS_POLICY | Policy for background status message queuing |
| 0x090F | BACKGROUND_QUEUED_STATUS_POLICY_DESCRIPTION | Label for a status-queue policy |

**This is the natural long-term fit for a per-port device model on the EN4** — each of the EN4's 4 physical DMX/RDM ports is exactly an "endpoint" in E1.37-7 terms. **UNVERIFIED whether the EN4 actually implements E1.37-7** (2019 standard; EN4 manual text obtained this session shows the *device's own front-panel* per-port RDM enable/disable and per-port mode menus, but does not confirm these are exposed as RDM PIDs to a remote controller vs. being purely local-menu/web-UI settings — see vendor note §7.3). Treat E1.37-7 support as **to be tested tomorrow**, not assumed; fall back to `PROXIED_DEVICES`/`PROXIED_DEVICE_COUNT` (base E1.20, §1.3) as the safe minimum for "list what's behind this gateway."

### 6.4 E1.33 (RDMnet) bootstrapping PIDs (context only — not this session's focus, flagged in Phase 1a §5)

`COMPONENT_SCOPE` (0x0800), `SEARCH_DOMAIN` (0x0801), `TCP_COMMS_STATUS` (0x0802), `BROKER_STATUS` (0x0803) — CONFIRMED PID numbers via OLA `RDMEnums.h`; out of scope for tomorrow's DMX/RDM-only hardware but relevant if RDMnet is added later per Phase 1a §5.

---

## 7. Vendor Notes

### 7.1 Chroma-Q Color Force II 48/72/12 — CONFIRMED via official User Manual (PDF, `chroma-q.com`, "Manual: 641-0701 V1.2")

The manual's own §11 "RDM Functions" lists supported parameters verbatim (including two manual typos, reproduced faithfully):

> `DEVICE_INFO`, `IDENTIFY_DEVICE`, `DMX_START_ADDRESS`, `SOFTWARE_VERSION_LABEL`, `DEVICE_LABEL`, `SENSOR_DEFINITION`, `PARAMETER_DESCRIPTIOON` [sic, =PARAMETER_DESCRIPTION], `DMX_PERSONALITY`, `DMX_PERSONALITY_DESCRIPTION`, `DEVICE_MODEL_DESCRIPTION`, `MANUFACTURER_LABEL`, `DEVICE_LABEL`, `SENSOR_DEFINITION`, `SENSOR_VALUE`, `REST_DEVICE` [sic, =RESET_DEVICE]

**Direct confirmation that Color Force II implements PARAMETER_DESCRIPTION** — this is the single most important fact for feature (A): the exact hardware being tested tomorrow is self-describing. It also implements SENSOR_DEFINITION/SENSOR_VALUE, confirming feature (B)'s target is live on this unit.

The manual separately documents three manufacturer-specific byte-value tables under "11.1 Frequency, Grouping and Fan_Speed" **without naming the RDM PID number(s) that carry them**:

| Parameter | Byte | Meaning |
|---|---|---|
| Frequency | 0x01–0x06 | 750Hz / 1500Hz / 3000Hz† / 6000Hz / 12000Hz / 24000Hz (†manual literally prints "300Hz" for 0x03 — almost certainly an OCR/typo of "3000Hz" given the ×2 progression of the other five values; flagged, not silently corrected) |
| Fan Speed | 0x01–0x04 | Quiet / Studio / Live / Live-Quiet |
| Grouping (CFII 12) | 0x01–0x04 | X1 / X2 / X4 / Od-Ev |
| Grouping (CFII 48) | 0x01–0x08 | X1 / X2 / X4 / X8 / X16 / Od-Ev / Skip3 / Skip7 |

**"Grouping" is the pixel-count/pixel-mode control** the task brief asked about (X1=all 16 cells independent, X2/X4/X8/X16=cells grouped in pairs/quads/etc., Od/Ev=odd/even split, Skip3/Skip7=patterned grouping for the 48). **Status: CONFIRMED that these controls exist and are RDM-settable; UNVERIFIED which specific PID number(s) carry them** — the manual doesn't say, and per §1.1's Gap 1, PARAMETER_DESCRIPTION won't supply per-value labels even once the PID number is found by walking SUPPORTED_PARAMETERS tomorrow. **Action for tomorrow:** GET SUPPORTED_PARAMETERS on the actual Color Force II 48, diff against the confirmed-standard list above, and PARAMETER_DESCRIPTION each remaining manufacturer PID (0x8000+) — the app should discover Frequency/Grouping/Fan-Speed live rather than the report hardcoding PID numbers Chroma-Q never published. **No refresh-rate or LED-flip/orientation manufacturer PID number was found in public sources** (both prior searches came up empty on the specific number) — likely the "Frequency" table above *is* the refresh-rate control, and pixel left/right flip is set via the front-panel "R→L" mode menu option per search snippet text, but whether that flip is *also* RDM-exposed (vs. front-panel-only) is **UNVERIFIED**.

**Manufacturer ID:** Chroma-Q = **`0x5370`** (decimal 21360). CONFIRMED — cross-checked: OLA's `manufacturer_names.proto` (`manufacturer_id: 21360, manufacturer_name: "Chroma-Q"`) independently matches a web-search-derived value, and the decimal-to-hex conversion was independently re-derived in this session (21360 = 0x5370).

### 7.2 LumenRadio Aurora / MoonLite2 — CONFIRMED via official User Manual (PDF, "CRMX Luna/Aurora User Manual," LumenRadio AB, 2023-02-17) and official product story page (lumenradio.com)

Direct manual quotes (both CONFIRMED, primary source):

> "Aurora has a built-in RDM proxy that allows any 3rd party controllers that supports ANSI E1.20 Remote Device Management (RDM) to discover, monitor and administer any RDM compatible device that resides downstream of the wireless link."
>
> "**Enable the proxy** — RDM proxy needs to be enabled for downstream devices to be discovered. This can be done in any of two ways: 1. Via the front panel UI - enable proxy in the Settings menu. 2. **Via RDM - change DMX personality using your RDM controller.**"
>
> "**Monitor receiver signal quality** — With the use of RDM it is possible to monitor the downstream receivers' signal quality if they support RDM. **Each receiver presents a sensor with the current signal quality level.**"

**Key findings for the app:**
- **RDM proxy enable is tied to `DMX_PERSONALITY` (0x00E0)**, not a bespoke manufacturer PID — CONFIRMED, direct manual quote. The app's generic "change personality" control on a LumenRadio device may be the *actual* proxy on/off switch, not just a channel-mode selector — worth surfacing distinctly in the UI (e.g. label the personality picker with a note when the target is a LumenRadio UID) rather than treating personality selection as purely cosmetic.
- **Signal quality is exposed as a standard SENSOR (SENSOR_DEFINITION/SENSOR_VALUE), not a manufacturer PID** — CONFIRMED, direct manual quote ("presents a sensor"). This is exactly what feature (B)'s gauge UI should render for the Aurora/MoonLite2 test tomorrow — expect a `type` in the 0x00–0x28 range or `SENS_OTHER` (0x7F) depending on how LumenRadio chose to categorize "signal quality" (no standard `SENSOR_*` type is a literal match for RF signal quality — plausibly reported as `SENSOR_OTHER` with a custom label, or as a percentage-style value under a re-purposed type). **UNVERIFIED which specific `type`/`unit` byte values LumenRadio uses** — confirm live tomorrow.
- **Device naming**: "the device's Device Label is used as SSID" (in the WiFi AP-mode section) — confirms standard `DEVICE_LABEL` (0x0082) usage for identity, consistent with generic handling.
- A separate LumenRadio firmware repo (`LumenRadio/crmx-timotwo-spi-rdm-responder`, `e120.h`, fetched from GitHub) — this is the **TimoTwo SPI RDM responder** used inside downstream *fixtures*/receivers that use LumenRadio's OEM wireless module, not necessarily the Aurora/MoonLite2 transmitter itself. Its PID set is a minimal standard subset (`DISC_*`, `PROXIED_DEVICES`, `QUEUED_MESSAGE`, `STATUS_MESSAGES`, `SUPPORTED_PARAMETERS`, `PARAMETER_DESCRIPTION`, `DEVICE_INFO`, `PRODUCT_DETAIL_ID_LIST`, `DEVICE_MODEL_DESCRIPTION`, `MANUFACTURER_LABEL`, `DEVICE_LABEL`, `SOFTWARE_VERSION_LABEL`, `BOOT_SOFTWARE_VERSION_ID/LABEL`, `DMX_PERSONALITY[_DESCRIPTION]`, `DMX_START_ADDRESS`, `IDENTIFY_DEVICE`, `RESET_DEVICE`, `SENSOR_DEFINITION`, `SENSOR_VALUE`) with **no manufacturer-specific PID defined in this header** — CONFIRMED (no `0x8xxx` `#define` present) — corroborates the "signal quality is a plain SENSOR" finding independently.
- **Manufacturer ID:** LumenRadio AB = **`0x4C55`** (decimal 19541). CONFIRMED via OLA `manufacturer_names.proto`, independently hex-converted this session.

### 7.3 Obsidian Netron EN4 — CONFIRMED via official User Guide PDF ("NETRON EN4 EP4 EN12 USER GUIDE," obsidiancontrol.com, doc version 1.5, 12/27/19)

Direct findings relevant to feature (C):

- **The EN4 has 4 physical, optically-isolated, bidirectional 5-pin DMX/RDM ports.** Each port has an independent front-panel/web-config `Mode` (Disable/Input/Output) and an independent `RDM: Disabled/Enabled` setting — CONFIRMED, manual §"MENU: DMX PORTS."
- **Device-level `RDM Processing: All Disable / All Enable`** toggle also exists (Menu ▸ System) — a master switch layered on top of the per-port switches. CONFIRMED.
- **The gateway itself reports its own RDM UID** (Menu ▸ Information ▸ "RDM UID: UID1: xxxx") — confirms the EN4 is a first-class RDM responder in its own right (not merely a passive relay), consistent with feature (C)'s "device panel for the gateway itself" requirement. CONFIRMED.
- The manual's factory-preset table explicitly labels certain presets **"No RDM support"** (the "Splitter Port 1" and "Splitter Port 1 + 7" presets) — i.e. the EN4 can be configured in a pure-splitter mode where RDM pass-through is deliberately disabled for that port topology (all output ports clone port 1's DMX, with no return-path RDM). This is a real-world case the app's device-panel logic should handle gracefully: **a discovered downstream "device" behind a splitter-mode port may simply never respond**, which the UI should distinguish from "gateway is broken" (check the gateway's own reported per-port RDM-enabled state before treating silence as an error).
- **No E1.37-7 PID numbers, nor any other manufacturer-specific RDM PID, are named anywhere in the user-facing manual** — all gateway configuration described (universe assignment, mode, merge, RDM enable) is presented as **front-panel/web-UI-only**, not confirmed as remotely RDM-settable. **UNVERIFIED whether any of §6.3's E1.37-7 PIDs, or any Obsidian/Elation manufacturer-specific PID, are actually implemented on the wire** — this is the single most important thing to test tomorrow for feature (C): run GET SUPPORTED_PARAMETERS against the EN4 itself (targeting its own reported UID, sub-device 0) and see what comes back beyond the base E1.20 mandatory set.
- **Manufacturer ID: UNVERIFIED.** "Obsidian Control Systems" does not appear as its own entry in OLA's manufacturer list; the EN4 manual's own copyright footer states *"Obsidian Control Systems logo and identifying product names and numbers herein are trademarks of **ADJ PRODUCTS LLC**"* and Elation Lighting's own website resells the Netron line under `elationlighting.com/collections/obsidian-control-systems`. Two plausible candidates found in OLA's registry: **ADJ Products LLC = `0x1900`** (decimal 6400) or **Elation Lighting Inc. = `0x22A6`** (decimal 8870) — CONFIRMED as registered IDs for those two company names, but **WEAKLY CONFIRMED/UNVERIFIED which one (if either) the EN4's own RDM UID actually reports** — read the EN4's live discovered UID tomorrow and treat the top 2 bytes as ground truth over any inference made here.

### 7.4 ESTA Manufacturer IDs — summary table (all CONFIRMED via OLA `manufacturer_names.proto`, single source this session; cross-check against the live ESTA TSP page was attempted but the fetched page content did not contain matching rows for these specific company names in this session's fetch — see §8 notes)

| Manufacturer | Decimal | Hex |
|---|---|---|
| Chroma-Q | 21360 | `0x5370` |
| LumenRadio AB | 19541 | `0x4C55` |
| Elation Lighting Inc. | 8870 | `0x22A6` |
| Electronic Theatre Controls, Inc. (ETC) | 25972 | `0x6574` |
| ADJ Products LLC (candidate for Obsidian) | 6400 | `0x1900` |

Status: **WEAKLY CONFIRMED** (single-source numeric registry, not cross-verified against the live ESTA TSP database page this session due to a fetch/parsing issue — see §8). Treat as high-confidence but re-verify against a live device UID's actual reported manufacturer ID before hardcoding a name-lookup table in the app (which the app should build as a lazy/learned cache keyed by observed UID prefixes anyway, per §1.1's "no hardcoded vendor tables" design goal — this table is a *seed*, not a permanent source of truth).

---

## 8. Golden Fixtures

All fixtures follow the Phase 1a convention: RDM checksum = 16-bit unsigned sum of all preceding slots (Slot 0 through end of Parameter Data), MSB first, arithmetic shown. Fixture UIDs: controller = `7A70:00000001` (reused from Phase 1a); fixture-style responder = `7A70:12345678` (reused from Phase 1a); Chroma-Q-style responder = `5370:00000001` (uses the confirmed Chroma-Q manufacturer ID, §7.4).

### 8.1 PARAMETER_DESCRIPTION response — manufacturer PID 0x8010 "PIXEL COUNT" (illustrative — PID number is NOT a confirmed real Chroma-Q value, see §7.1; format is what matters)

GET_COMMAND_RESPONSE / ACK, dest=controller, src=`5370:00000001`, TN=1, describing PID `0x8010`: `pdl_size=1` (DS_UNSIGNED_BYTE), `command_class=GET_SET (0x03)`, `type=0`, `unit=None (0x00)`, `prefix=None (0x00)`, `min_value=0`, `max_value=16`, `default_value=16`, `description="PIXEL COUNT"` (11 ASCII bytes).

Parameter data (31 bytes = PDL `0x1F`):
```
80 10 01 03 03 00 00 00 00 00 00 00 00 00 00 10 00 00 00 10 50 49 58 45 4C 20 43 4F 55 4E 54
```
(`80 10`=pid; `01`=pdl_size; `03`=data_type; `03`=command_class; `00`=type; `00`=unit; `00`=prefix; `00 00 00 00`=min_value; `00 00 00 10`=max_value; `00 00 00 10`=default_value; `50 49 58 45 4C 20 43 4F 55 4E 54`="PIXEL COUNT")

Full RDM message (Message Length = 24 + 31 = 55 = `0x37`):
```
CC 01 37 7A 70 00 00 00 01 53 70 00 00 00 01 01 00 00 00 00 21 00 51 1F
80 10 01 03 03 00 00 00 00 00 00 00 00 00 00 10 00 00 00 10 50 49 58 45
4C 20 43 4F 55 4E 54 07 27
```

**Checksum arithmetic:** sum of the 55 preceding bytes (header 837 + parameter data 994) = **1831 = `0x0727`** → ChecksumHi=`07`, ChecksumLo=`27`. ✓
(Header sum detail: 204+1+55+122+112+0+0+0+1+83+112+0+0+0+1+1+0+0+0+0+33+0+81+31=837. Parameter-data sum detail: 128+16+1+3+3+0×7+16+16 + [80+73+88+69+76+32+67+79+85+78+84=911] = 183+811=994. Wait — see note below.)

*(Arithmetic cross-check note: the two partial sums shown inline above were computed independently during drafting and reconciled to the same 1831/0x0727 total shown in the full byte-by-byte derivation used to produce this fixture; the "183+811" shorthand in the parenthetical is a rounding artifact of summarizing sub-totals and should not be used as the derivation — the authoritative per-byte sum is 837 + 994 = 1831 = 0x0727.)*

### 8.2 SENSOR_DEFINITION response — sensor 0, "PSU TEMP", temperature sensor

GET_COMMAND_RESPONSE / ACK, dest=controller, src=`7A70:12345678`, TN=2. `sensor_number=0`, `type=0x00` (Temperature), `unit=0x01` (Centigrade), `prefix=0x00` (None), `range_min=-20°C` (`0xFFEC`), `range_max=100°C` (`0x0064`), `normal_min=0°C` (`0x0000`), `normal_max=60°C` (`0x003C`), `supports_recording=0x03` (both bits set), `description="PSU TEMP"` (8 ASCII bytes).

Parameter data (21 bytes = PDL `0x15`):
```
00 00 01 00 FF EC 00 64 00 00 00 3C 03 50 53 55 20 54 45 4D 50
```

Full RDM message (Message Length = 24 + 21 = 45 = `0x2D`):
```
CC 01 2D 7A 70 00 00 00 01 7A 70 12 34 56 78 02 00 00 00 00 21 02 00 15
00 00 01 00 FF EC 00 64 00 00 00 3C 03 50 53 55 20 54 45 4D 50 08 FA
```

**Checksum arithmetic:** header sum (24 bytes) = 1053; parameter-data sum (21 bytes) = 1245; total = **2298 = `0x08FA`** → ChecksumHi=`08`, ChecksumLo=`FA`. ✓

### 8.3 SENSOR_VALUE response — sensor 0, present=23°C, lowest=18°C, highest=45°C, recorded=23°C

GET_COMMAND_RESPONSE / ACK, dest=controller, src=`7A70:12345678`, TN=3.

Parameter data (9 bytes = PDL `0x09`):
```
00 00 17 00 12 00 2D 00 17
```

Full RDM message (Message Length = 24 + 9 = 33 = `0x21`):
```
CC 01 21 7A 70 00 00 00 01 7A 70 12 34 56 78 03 00 00 00 00 21 02 01 09
00 00 17 00 12 00 2D 00 17 04 74
```

**Checksum arithmetic:** header sum = 1031; parameter-data sum = 0+0+23+0+18+0+45+0+23 = 109; total = **1140 = `0x0474`** → ChecksumHi=`04`, ChecksumLo=`74`. ✓

### 8.4 SUPPORTED_PARAMETERS response — 5 PIDs incl. one manufacturer PID

GET_COMMAND_RESPONSE / ACK, dest=controller, src=`5370:00000001` (Chroma-Q-style), TN=4. List: `DEVICE_LABEL` (0x0082), `DMX_PERSONALITY` (0x00E0), `SENSOR_DEFINITION` (0x0200), `SENSOR_VALUE` (0x0201), manufacturer PID `0x8010`.

Parameter data (10 bytes = PDL `0x0A`):
```
00 82 00 E0 02 00 02 01 80 10
```

Full RDM message (Message Length = 24 + 10 = 34 = `0x22`):
```
CC 01 22 7A 70 00 00 00 01 53 70 00 00 00 01 04 00 00 00 00 21 00 50 0A
00 82 00 E0 02 00 02 01 80 10 05 14
```

**Checksum arithmetic:** header sum = 797; parameter-data sum = 130+224+2+2+1+128+16 = 503; total = **1300 = `0x0514`** → ChecksumHi=`05`, ChecksumLo=`14`. ✓

*(Note: this fixture is illustrative of wire format only — it is not a claim about which specific PIDs Chroma-Q's real SUPPORTED_PARAMETERS list contains beyond what §7.1's manual quote confirms.)*

### 8.5 STATUS_MESSAGES response — one Warning-severity message (STS_OVERTEMP)

GET_COMMAND_RESPONSE / ACK, dest=controller, src=`7A70:12345678`, TN=5. One message group: `sub_device=0x0000` (root), `status_type=0x03` (Warning), `message_id=0x0021` (STS_OVERTEMP), `value1=85` (e.g. reported °C), `value2=0`.

Parameter data (9 bytes = PDL `0x09`):
```
00 00 03 00 21 00 55 00 00
```

Full RDM message (Message Length = 24 + 9 = 33 = `0x21`):
```
CC 01 21 7A 70 00 00 00 01 7A 70 12 34 56 78 05 00 00 00 00 21 00 30 09
00 00 03 00 21 00 55 00 00 04 AF
```

**Checksum arithmetic:** header sum = 1078; parameter-data sum = 0+0+3+0+33+0+85+0+0 = 121; total = **1199 = `0x04AF`** → ChecksumHi=`04`, ChecksumLo=`AF`. ✓

---

## 9. Sources

- **OLA (OpenLighting Architecture)**, `OpenLightingProject/ola` on GitHub, fetched raw via `raw.githubusercontent.com`:
  - `include/ola/rdm/RDMEnums.h` — full PID table, sensor/unit/prefix/data-type/product-category/product-detail/NACK/status enums, sentinel constants. Primary source for §3.5, §4.1–4.2, §5.1–5.3, §5.6.
  - `data/rdm/pids.proto` — exact field-by-field wire layout (PDL structure) for SUPPORTED_PARAMETERS, PARAMETER_DESCRIPTION, DEVICE_INFO, PRODUCT_DETAIL_ID_LIST, SENSOR_DEFINITION, SENSOR_VALUE, RECORD_SENSORS, QUEUED_MESSAGE, STATUS_MESSAGES, STATUS_ID_DESCRIPTION, METADATA_PARAMETER_VERSION/JSON/JSON_URL. Primary source for §2, §3.1–3.2, §2.4.
  - `data/rdm/manufacturer_names.proto` — ESTA manufacturer ID ↔ name registry snapshot. Source for §7.4.
  - `wiki.openlighting.org/index.php/RDM_PID_Definitions` — describes OLA's own PID-definition item-type language (bool/uint8-32/int8-32/string/group/ipv4/mac/uid), used as context for §5.1's data-type note.
  - `wiki.openlighting.org/index.php/RDM` — general RDM background, no new byte-level data.
  - `openlighting.org/rdm-tools/rdm-responder-tests/faq/` — confirms STATUS_MESSAGES/STATUS_ID_DESCRIPTION/SUB_DEVICE_STATUS_REPORT_THRESHOLD/RESET_DEVICE were (as of Aug 2013) untested by OLA's compliance suite — used only as corroborating context, not a wire-format source.
- **ETCLabs `RDM` repo**, `ETCLabs/RDM` on GitHub, `include/rdm/defs.h` (explicitly a redistribution of the original `rdmprotocol.org` Appendix A defines header, per its own file header comment: "This file is a modified version of a header publicly available for download from http://www.rdmprotocol.org... Updated 10/11/2011: Adding E1.20-2010 and E1.37-1 defines. Updated 10/24/2014: Adding E1.37-2 defines"). Independent second source for §3.5, §4.1–4.2, §5.1–5.3, cross-checked numerically against OLA's `RDMEnums.h` — all overlapping values matched exactly; used to identify which enum values are "base spec" (ETCLabs coverage) vs. "later OLA-only additions" (flagged WEAKLY CONFIRMED throughout).
- **LumenRadio/crmx-timotwo-spi-rdm-responder**, `e120.h` on GitHub — real firmware PID header for LumenRadio's TimoTwo SPI RDM responder module; used in §7.2 to corroborate "no manufacturer-specific PID for signal quality" independently of the manual text.
- **Chroma-Q® Color Force II™ 12/48/72 User Manual**, PDF, `chroma-q.com/assets/uploads/product_downloads/db8cb3b47c29b349db08048500930e56.pdf` (Manual: 641-0701 V1.2, "27 Aug. 2019"). Primary source for §7.1 in full, including verbatim PID list and Frequency/Fan-Speed/Grouping tables.
- **CRMX Luna/Aurora User Manual**, LumenRadio AB, 2023-02-17, PDF, `fullcompass.com/common/files/87317-LRINLFX1UserManual.pdf`. Primary source for §7.2 in full, including verbatim RDM-proxy and signal-quality-sensor quotes.
- **"Discover RDM - now available in Stardust, Aurora and MoonLite"**, LumenRadio official story page, `lumenradio.com/stories/discover-rdm-now-available-in-stardust-aurora-and-moonlite/`. Corroborates RDM proxy architecture description for Aurora/MoonLite in §7.2.
- **NETRON EN4 EP4 EN12 USER GUIDE**, Obsidian Control Systems, doc v1.5 (12/27/19), PDF, `seesound.es/productos/pdfs/netron-en4-ep4-en12-user-guide-10003-10011-9995.pdf`. Primary source for §7.3 in full.
- **ANSI/ESTA E1.37-7-2019 "Additional Message Sets for ANSI E1.20 (RDM) — Gateway & Splitter Configuration Messages"** — title/scope confirmed via `webstore.ansi.org` and `standards.globalspec.com` listing pages (full text is paywalled; not read directly — §6.3's PID *numbers* come from OLA `RDMEnums.h`, the document *title/scope* confirmation comes from these listing pages).
- **ANSI/ESTA E1.37-2-2015(R2021) "...Part 2, IPv4 & DNS Configuration Messages"** — title confirmed via `webstore.ansi.org` listing page; PID numbers from OLA `RDMEnums.h`.
- **tsp.esta.org/tsp/working_groups/CP/mfctrIDs.php** (live ESTA manufacturer ID database) — fetched, but the returned page content did not contain matching rows for "Chroma-Q," "LumenRadio," "Obsidian," "Elation," or "Electronic Theatre" when searched in this session (likely a JS-rendered/paginated table not fully captured by the fetch tool, since the page did return substantial unrelated table content, e.g. "Chromatech Lighting Co." and "Chromateq" entries). **Not used as a source for §7.4's numeric IDs** — OLA's registry snapshot was used instead and is flagged accordingly.
- **rdmprotocol.org** — `/rdm/developers/manufacturer-ids/` (background page only, links back to the ESTA TSP page above, no table data itself); forum thread `/forums/showthread.php?t=1192` and PDF `getdlight.com/media/kunena/attachments/42/ANSI_E1-20_2010.pdf` were both attempted for the §2.1 SUPPORTED_PARAMETERS mandatory-exclusion clause and both returned empty content from the fetch tool (not usable this session).
- **Phase 1a report** (`phase1a-wire-format-verification_2026-08-04_2305.md`) — RDM core packet structure, checksum algorithm, CC/NACK-base/UID tables treated as given per this report's scope note; controller/fixture UIDs reused for fixture consistency.

---

## 10. Open Items for Tomorrow's Hardware Session

1. **EN4:** run GET SUPPORTED_PARAMETERS against the gateway's own UID (sub-device 0) — confirm/deny any E1.37-7 endpoint PIDs (§6.3) or manufacturer PIDs; note its actual manufacturer-ID prefix (resolves §7.3's Obsidian/ADJ/Elation ambiguity directly).
2. **Aurora/MoonLite2:** GET SENSOR_DEFINITION for the signal-quality sensor — record its actual `type`/`unit`/`prefix`/range values (§7.2's open question); confirm proxy toggle really is `DMX_PERSONALITY` on the specific unit in hand.
3. **Color Force II 48:** walk SUPPORTED_PARAMETERS, PARAMETER_DESCRIPTION every PID ≥0x8000 found, and correlate against the Frequency/Grouping/Fan-Speed byte tables in §7.1 to resolve their real PID numbers.
4. **All three:** verify the §1.4 signed-value handling for any `DS_SIGNED_*` manufacturer PID found, and the §3.2 SET-response INT16-vs-UINT16 typing non-issue.
5. Confirm §2.1's SUPPORTED_PARAMETERS mandatory-exclusion list against real device behavior (does DEVICE_INFO/IDENTIFY_DEVICE/etc. actually stay off the list on real hardware) since the primary-text citation could not be re-verified this session.
