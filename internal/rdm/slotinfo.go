// This file implements Task 3 of the function-aware Rig Check foundation
// (owner brief, stage 1): decoding SLOT_INFO (0x0120) and SLOT_DESCRIPTION
// (0x0121), E1.20 §10.6.4/§10.6.5. Every wire layout, Slot Type value and
// Slot Label ID value below is CONFIRMED against a primary source available
// in this environment — ANSI E1.20-2025, §10.6.4 "Get Slot Info
// (SLOT_INFO)", §10.6.5 "Get Slot Description (SLOT_DESCRIPTION)", Appendix
// C "Slot Info (Normative)", Table C-1 "Slot Type Definitions" and Table
// C-2 "Slot Label ID Definitions" — read directly from that document's
// text, not reconstructed from training-time recollection the way
// dimmer.go's E1.37-1 codecs had to be (that file's own doc comment
// explains why: its source research report gave PID numbers only, no
// per-PID wire layout). Nothing in this file is TODO(hardware): every
// value below is checked against the spec text; NONE of it has been
// checked against a real responder on real hardware, which is a different
// and narrower kind of unverified-ness this file doesn't have — a
// spec-correct decoder can still meet a responder that gets the spec
// wrong, and the caller-facing distinction this feature actually cares
// about (task decision 3) is GDTF-derived vs RDM-INFERRED, not
// spec-confirmed vs hardware-confirmed; see patch.ChannelFunctionSource.
//
// SLOT_INFO (§10.6.4): GET response is a packed list of 5-byte records —
// Slot Offset (16-bit), Slot Type (8-bit), then a 16-bit field whose
// meaning depends on Slot Type: for a PRIMARY slot it IS the Slot Label ID
// (a standardized function code, Table C-2); for any SECONDARY slot type
// (ST_SEC_*) it is instead the DMX512 Slot Offset of the PRIMARY slot this
// one modifies — NOT a label ID at all (§10.6.4, "For secondary types, the
// Slot Label ID field becomes the Slot Offset for the primary slot to which
// it relates"). SlotInfoEntry models this dual meaning with a single raw
// Value field plus two accessors (LabelID/PrimaryOffset) that each panic-free
// return the field reinterpreted for the type it's actually valid for,
// rather than a single "LabelID" name that would silently be wrong data for
// every secondary-type record.
//
// SLOT_DESCRIPTION (§10.6.5): GET request carries a mandatory 2-byte slot
// offset (there is no index-free "describe slot 0" shortcut — a bare
// PDL=0x00 GET is malformed, same footgun class as SELF_TEST_DESCRIPTION;
// params.go's existing ErrSlotDescriptionNeedsIndex guard already enforces
// this at the params.Client boundary, see that file's doc comment). GET
// response is the echoed 2-byte offset plus up to 32 bytes of text.
package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// ErrBadSlotInfoLength is returned when a SLOT_INFO GET response's
// parameter data isn't a whole number of 5-byte records.
var ErrBadSlotInfoLength = errors.New("rdm: SLOT_INFO parameter data is not a whole number of 5-byte records")

// ErrBadSlotDescriptionLength is returned when a SLOT_DESCRIPTION GET
// response is shorter than the mandatory 2-byte echoed offset.
var ErrBadSlotDescriptionLength = errors.New("rdm: SLOT_DESCRIPTION wants >=2 bytes (echoed offset), got fewer")

// SlotType is SLOT_INFO's 8-bit Slot Type field (E1.20 §10.6.4, Table C-1).
// CONFIRMED against ANSI E1.20-2025 Table C-1.
type SlotType byte

// Slot types, Table C-1. ST_PRIMARY marks a slot that directly controls a
// parameter (Coarse byte, for a 16-bit parameter); every ST_SEC_* value
// marks a slot that is a modifier/dependent of some other PRIMARY slot
// (whose offset is carried in that record's Value field — see
// SlotInfoEntry.PrimaryOffset).
const (
	SlotTypePrimary                SlotType = 0x00
	SlotTypeSecondaryFine          SlotType = 0x01
	SlotTypeSecondaryTiming        SlotType = 0x02
	SlotTypeSecondarySpeed         SlotType = 0x03
	SlotTypeSecondaryControl       SlotType = 0x04
	SlotTypeSecondaryQuantum       SlotType = 0x05
	SlotTypeSecondaryRotation      SlotType = 0x06
	SlotTypeSecondaryQuantumRotate SlotType = 0x07
	SlotTypeSecondaryUndefined     SlotType = 0xFF
)

// IsSecondary reports whether t is any ST_SEC_* value (as opposed to
// ST_PRIMARY) — Table C-1 defines exactly one primary type and a family of
// secondary ones, not a bitmask, so this is a direct equality check rather
// than a bit test.
func (t SlotType) IsSecondary() bool { return t != SlotTypePrimary }

// String renders Table C-1's symbolic name, or a hex fallback for a value
// the table doesn't define (a responder is free to send any byte here;
// Table C-1 enumerates 0x00-0x07 and 0xFF only — 0x08-0xFE are simply
// undefined by the spec, not reserved-and-forbidden, so this is a plain
// "unknown", never an error).
func (t SlotType) String() string {
	switch t {
	case SlotTypePrimary:
		return "ST_PRIMARY"
	case SlotTypeSecondaryFine:
		return "ST_SEC_FINE"
	case SlotTypeSecondaryTiming:
		return "ST_SEC_TIMING"
	case SlotTypeSecondarySpeed:
		return "ST_SEC_SPEED"
	case SlotTypeSecondaryControl:
		return "ST_SEC_CONTROL"
	case SlotTypeSecondaryQuantum:
		return "ST_SEC_QUANTUM"
	case SlotTypeSecondaryRotation:
		return "ST_SEC_ROTATION"
	case SlotTypeSecondaryQuantumRotate:
		return "ST_SEC_QUANTUM_ROTATE"
	case SlotTypeSecondaryUndefined:
		return "ST_SEC_UNDEFINED"
	default:
		return fmt.Sprintf("unknown slot type 0x%02X", byte(t))
	}
}

// SlotLabelID is SLOT_INFO's Slot Label ID field, meaningful only on a
// PRIMARY-type record (E1.20 §10.6.4, Table C-2). CONFIRMED against ANSI
// E1.20-2025 Table C-2 for every named constant below.
type SlotLabelID uint16

// Slot Label IDs, Table C-2 (publicly-defined subset — the table also notes
// the ESTA website may list additional publicly-defined IDs beyond this
// document, and reserves 0x8000-0xFFDF for manufacturer-specific IDs; this
// package models only the values Table C-2 itself enumerates).
const (
	// Intensity Functions (0x00xx).
	SDIntensity       SlotLabelID = 0x0001
	SDIntensityMaster SlotLabelID = 0x0002

	// Movement Functions (0x01xx).
	SDPan  SlotLabelID = 0x0101
	SDTilt SlotLabelID = 0x0102

	// Color Functions (0x02xx).
	SDColorWheel        SlotLabelID = 0x0201
	SDColorSubCyan      SlotLabelID = 0x0202
	SDColorSubYellow    SlotLabelID = 0x0203
	SDColorSubMagenta   SlotLabelID = 0x0204
	SDColorAddRed       SlotLabelID = 0x0205
	SDColorAddGreen     SlotLabelID = 0x0206
	SDColorAddBlue      SlotLabelID = 0x0207
	SDColorCorrection   SlotLabelID = 0x0208
	SDColorScroll       SlotLabelID = 0x0209
	SDColorAddLime      SlotLabelID = 0x020A
	SDColorAddIndigo    SlotLabelID = 0x020B
	SDColorAddCyan      SlotLabelID = 0x020C
	SDColorAddDeepRed   SlotLabelID = 0x020D
	SDColorAddDeepBlue  SlotLabelID = 0x020E
	SDColorAddNatWhite  SlotLabelID = 0x020F
	SDColorSemaphore    SlotLabelID = 0x0210
	SDColorAddAmber     SlotLabelID = 0x0211
	SDColorAddWhite     SlotLabelID = 0x0212
	SDColorAddWarmWhite SlotLabelID = 0x0213
	SDColorAddCoolWhite SlotLabelID = 0x0214
	SDColorSubUV        SlotLabelID = 0x0215
	SDColorHue          SlotLabelID = 0x0216
	SDColorSaturation   SlotLabelID = 0x0217
	SDColorAddUV        SlotLabelID = 0x0218

	// Image Functions (0x03xx).
	SDStaticGoboWheel SlotLabelID = 0x0301
	SDRotoGoboWheel   SlotLabelID = 0x0302
	SDPrismWheel      SlotLabelID = 0x0303
	SDEffectsWheel    SlotLabelID = 0x0304

	// Beam Functions (0x04xx).
	SDBeamSizeIris   SlotLabelID = 0x0401
	SDEdge           SlotLabelID = 0x0402
	SDFrost          SlotLabelID = 0x0403
	SDStrobe         SlotLabelID = 0x0404
	SDZoom           SlotLabelID = 0x0405
	SDFramingShutter SlotLabelID = 0x0406
	SDShutterRotate  SlotLabelID = 0x0407
	SDDouser         SlotLabelID = 0x0408
	SDBarnDoor       SlotLabelID = 0x0409

	// Control Functions (0x05xx).
	SDLampControl     SlotLabelID = 0x0501
	SDFixtureControl  SlotLabelID = 0x0502
	SDFixtureSpeed    SlotLabelID = 0x0503
	SDMacro           SlotLabelID = 0x0504
	SDPowerControl    SlotLabelID = 0x0505
	SDFanControl      SlotLabelID = 0x0506
	SDHeaterControl   SlotLabelID = 0x0507
	SDFountainControl SlotLabelID = 0x0508

	// SDUndefined (0xFFFF): "A Slot Label ID of SD_UNDEFINED for a primary
	// slot indicates that the description for that Slot Definition is only
	// available by using the SLOT_DESCRIPTION parameter" (§10.6.4).
	SDUndefined SlotLabelID = 0xFFFF
)

// slotLabelNames backs SlotLabelID.String() — a plain lookup table so an
// unrecognized/manufacturer-specific ID falls through to the numeric
// fallback rather than needing a giant switch kept in sync by hand.
var slotLabelNames = map[SlotLabelID]string{
	SDIntensity: "SD_INTENSITY", SDIntensityMaster: "SD_INTENSITY_MASTER",
	SDPan: "SD_PAN", SDTilt: "SD_TILT",
	SDColorWheel: "SD_COLOR_WHEEL", SDColorSubCyan: "SD_COLOR_SUB_CYAN", SDColorSubYellow: "SD_COLOR_SUB_YELLOW",
	SDColorSubMagenta: "SD_COLOR_SUB_MAGENTA", SDColorAddRed: "SD_COLOR_ADD_RED", SDColorAddGreen: "SD_COLOR_ADD_GREEN",
	SDColorAddBlue: "SD_COLOR_ADD_BLUE", SDColorCorrection: "SD_COLOR_CORRECTION", SDColorScroll: "SD_COLOR_SCROLL",
	SDColorAddLime: "SD_COLOR_ADD_LIME", SDColorAddIndigo: "SD_COLOR_ADD_INDIGO", SDColorAddCyan: "SD_COLOR_ADD_CYAN",
	SDColorAddDeepRed: "SD_COLOR_ADD_DEEP_RED", SDColorAddDeepBlue: "SD_COLOR_ADD_DEEP_BLUE", SDColorAddNatWhite: "SD_COLOR_ADD_NAT_WHITE",
	SDColorSemaphore: "SD_COLOR_SEMAPHORE", SDColorAddAmber: "SD_COLOR_ADD_AMBER", SDColorAddWhite: "SD_COLOR_ADD_WHITE",
	SDColorAddWarmWhite: "SD_COLOR_ADD_WARM_WHITE", SDColorAddCoolWhite: "SD_COLOR_ADD_COOL_WHITE", SDColorSubUV: "SD_COLOR_SUB_UV",
	SDColorHue: "SD_COLOR_HUE", SDColorSaturation: "SD_COLOR_SATURATION", SDColorAddUV: "SD_COLOR_ADD_UV",
	SDStaticGoboWheel: "SD_STATIC_GOBO_WHEEL", SDRotoGoboWheel: "SD_ROTO_GOBO_WHEEL", SDPrismWheel: "SD_PRISM_WHEEL",
	SDEffectsWheel: "SD_EFFECTS_WHEEL",
	SDBeamSizeIris: "SD_BEAM_SIZE_IRIS", SDEdge: "SD_EDGE", SDFrost: "SD_FROST", SDStrobe: "SD_STROBE", SDZoom: "SD_ZOOM",
	SDFramingShutter: "SD_FRAMING_SHUTTER", SDShutterRotate: "SD_SHUTTER_ROTATE", SDDouser: "SD_DOUSER", SDBarnDoor: "SD_BARN_DOOR",
	SDLampControl: "SD_LAMP_CONTROL", SDFixtureControl: "SD_FIXTURE_CONTROL", SDFixtureSpeed: "SD_FIXTURE_SPEED",
	SDMacro: "SD_MACRO", SDPowerControl: "SD_POWER_CONTROL", SDFanControl: "SD_FAN_CONTROL", SDHeaterControl: "SD_HEATER_CONTROL",
	SDFountainControl: "SD_FOUNTAIN_CONTROL",
	SDUndefined:       "SD_UNDEFINED",
}

// IsManufacturerSpecific reports whether id falls in Table C-2's
// manufacturer-specific range (0x8000-0xFFDF) — mirrors
// ParameterID.IsManufacturerSpecific's naming/shape in message.go.
func (id SlotLabelID) IsManufacturerSpecific() bool { return id >= 0x8000 && id <= 0xFFDF }

// String renders Table C-2's symbolic name, "manufacturer-specific" for the
// reserved range, or a numeric fallback for anything else (an ID Table C-2
// doesn't enumerate but that isn't in the manufacturer range either — the
// table's own note that "the ESTA website may document new publicly
// defined Slot Indexes in addition to those enumerated in this document"
// means that's a real, expected case, not a protocol violation).
func (id SlotLabelID) String() string {
	if name, ok := slotLabelNames[id]; ok {
		return name
	}
	if id.IsManufacturerSpecific() {
		return fmt.Sprintf("manufacturer-specific (0x%04X)", uint16(id))
	}
	return fmt.Sprintf("unknown slot label 0x%04X", uint16(id))
}

// SlotInfoEntry is one 5-byte record from a SLOT_INFO response.
type SlotInfoEntry struct {
	Offset uint16
	Type   SlotType
	// Value is the raw 16-bit field following Type — a Slot Label ID when
	// Type==SlotTypePrimary, or the related primary slot's Offset when Type
	// is any secondary type (§10.6.4). Use LabelID()/PrimaryOffset() rather
	// than reading this directly, so which meaning applies is never
	// ambiguous at the call site.
	Value uint16
}

// LabelID returns Value reinterpreted as a Slot Label ID, valid only when
// e.Type==SlotTypePrimary. ok is false for any secondary type — callers
// must not treat a secondary record's Value as a label ID (§10.6.4: "the
// Slot Label ID field becomes the Slot Offset for the primary slot").
func (e SlotInfoEntry) LabelID() (id SlotLabelID, ok bool) {
	if e.Type.IsSecondary() {
		return 0, false
	}
	return SlotLabelID(e.Value), true
}

// PrimaryOffset returns Value reinterpreted as the DMX offset of the
// primary slot this secondary-type record modifies, valid only when
// e.Type.IsSecondary(). ok is false for a primary record.
func (e SlotInfoEntry) PrimaryOffset() (offset uint16, ok bool) {
	if !e.Type.IsSecondary() {
		return 0, false
	}
	return e.Value, true
}

// DecodeSlotInfo decodes a SLOT_INFO GET response: a packed list of 5-byte
// records (§10.6.4 — PDL is variable, 0x00-0xE6, i.e. 0 to 46 records; this
// decoder accepts any length that's a whole multiple of 5, it does not
// separately enforce the PDL ceiling since that's a wire-framing limit the
// transport layer already bounds, not a decode-correctness concern).
func DecodeSlotInfo(data []byte) ([]SlotInfoEntry, error) {
	if len(data)%5 != 0 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrBadSlotInfoLength, len(data))
	}
	out := make([]SlotInfoEntry, 0, len(data)/5)
	for i := 0; i < len(data); i += 5 {
		out = append(out, SlotInfoEntry{
			Offset: binary.BigEndian.Uint16(data[i : i+2]),
			Type:   SlotType(data[i+2]),
			Value:  binary.BigEndian.Uint16(data[i+3 : i+5]),
		})
	}
	return out, nil
}

// EncodeSlotInfo is the inverse of DecodeSlotInfo — mostly useful for tests
// and the demo-mode fake responder (see Task 5's demo data).
func EncodeSlotInfo(entries []SlotInfoEntry) []byte {
	b := make([]byte, 0, len(entries)*5)
	for _, e := range entries {
		var rec [5]byte
		binary.BigEndian.PutUint16(rec[0:2], e.Offset)
		rec[2] = byte(e.Type)
		binary.BigEndian.PutUint16(rec[3:5], e.Value)
		b = append(b, rec[:]...)
	}
	return b
}

// EncodeSlotDescriptionRequest builds SLOT_DESCRIPTION's mandatory 2-byte
// GET request PD (§10.6.5) — the slot offset being asked about. This is the
// payload params.go's ErrSlotDescriptionNeedsIndex guard requires callers
// to supply; see that guard's doc comment for why a bare PDL=0x00 GET is
// rejected before it ever reaches the wire.
func EncodeSlotDescriptionRequest(slotOffset uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, slotOffset)
	return b
}

// SlotDescription is SLOT_DESCRIPTION's GET response: the echoed slot
// offset plus its text label.
type SlotDescription struct {
	SlotOffset uint16
	Label      string
}

// DecodeSlotDescription decodes a SLOT_DESCRIPTION GET response: 2-byte
// echoed Slot Offset + up to 32 bytes of text (§10.6.5). Trailing NUL bytes
// are trimmed defensively — the spec's "text field" doesn't mandate NUL
// padding, but some responders in the wild pad fixed-width label fields
// with NULs, the same defensive trim DecodeSelfTestDescription already
// applies to SELF_TEST_DESCRIPTION's label (devicecontrol.go).
func DecodeSlotDescription(data []byte) (SlotDescription, error) {
	if len(data) < 2 {
		return SlotDescription{}, fmt.Errorf("%w: got %d", ErrBadSlotDescriptionLength, len(data))
	}
	return SlotDescription{
		SlotOffset: binary.BigEndian.Uint16(data[0:2]),
		Label:      strings.TrimRight(string(data[2:]), "\x00"),
	}, nil
}

// EncodeSlotDescription is the inverse of DecodeSlotDescription — for tests
// and the demo-mode fake responder.
func EncodeSlotDescription(d SlotDescription) []byte {
	b := make([]byte, 2+len(d.Label))
	binary.BigEndian.PutUint16(b[0:2], d.SlotOffset)
	copy(b[2:], d.Label)
	return b
}
