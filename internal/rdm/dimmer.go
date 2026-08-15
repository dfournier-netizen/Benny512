package rdm

// This file adds typed codecs for the E1.37-1 Dimmer Message Set PIDs Dom
// asked for by name ("dimmer curve" — rdm-pids-sensors-research_2026-08-13_
// 2347.md §6.1 lists the PID numbers, CONFIRMED via OLA RDMEnums.h). The
// research report's §6.1 table gives PID number + a one-line purpose only —
// it does NOT include per-PID byte offsets/golden fixtures the way §2-3 do
// for SUPPORTED_PARAMETERS/PARAMETER_DESCRIPTION/SENSOR_*. Every wire
// layout below is therefore this package's best-reading implementation,
// not independently confirmed:
//
//   - CURVE / OUTPUT_RESPONSE_TIME / MODULATION_FREQUENCY follow the
//     "current index + supported count" GET-response shape and single-byte
//     SET-request shape that DMX_PERSONALITY (0x00E0, CONFIRMED, Phase 1a)
//     already uses on the wire — a reasonable pattern match since all four
//     PIDs are "pick one of N device-defined presets" controls, but
//     TODO(hardware): unverified for this specific PID family.
//   - CURVE_DESCRIPTION / OUTPUT_RESPONSE_TIME_DESCRIPTION /
//     MODULATION_FREQUENCY_DESCRIPTION follow DMX_PERSONALITY_DESCRIPTION's
//     "echoed index + ASCII label" shape, minus DMX_PERSONALITY_
//     DESCRIPTION's DMX-footprint field (a dimmer curve/response-time/
//     frequency preset has no footprint concept to echo).
//     TODO(hardware): unverified.
//   - MINIMUM_LEVEL's 5-byte (increasing/decreasing/on-below-min) shape and
//     MAXIMUM_LEVEL's 2-byte scalar shape are this package's best reading
//     of general E1.37-1 domain convention (MINIMUM_LEVEL is well known to
//     carry hysteresis — separate rising/falling thresholds — while
//     MAXIMUM_LEVEL is a single cap). TODO(hardware): unverified against
//     the research doc, which does not give either PID's byte layout.
//   - IDENTIFY_MODE's 1-byte Loud/Quiet enum is a low-risk reading (the
//     PID's one-line purpose in the report literally says "Loud vs. quiet
//     identify behavior"). TODO(hardware): the 0x00/0x01 value assignment
//     itself is not independently confirmed.
//
// Every decoder below returns ErrBadDimmerLength (wraps into the same
// %w-compatible error family as ErrBadParameterDescription) on a length
// mismatch rather than guessing, so a wrong-layout guess fails loudly
// against real hardware instead of silently misreading bytes.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadDimmerLength is returned when a dimmer-PID GET response's parameter
// data isn't the length this file's codecs expect.
var ErrBadDimmerLength = errors.New("rdm: unexpected E1.37-1 dimmer parameter data length")

// IndexedChoice is the GET response shape this package models for CURVE,
// OUTPUT_RESPONSE_TIME and MODULATION_FREQUENCY: a 1-byte current selection
// (1-based) plus a 1-byte count of how many presets (1..Count) the device
// supports. TODO(hardware): see file doc comment.
type IndexedChoice struct {
	Current byte
	Count   byte
}

func decodeIndexedChoice(data []byte, name string) (IndexedChoice, error) {
	if len(data) != 2 {
		return IndexedChoice{}, fmt.Errorf("%w: %s wants 2 bytes, got %d", ErrBadDimmerLength, name, len(data))
	}
	return IndexedChoice{Current: data[0], Count: data[1]}, nil
}

// DecodeCurve decodes CURVE's (0x0343) GET response. TODO(hardware).
func DecodeCurve(data []byte) (IndexedChoice, error) { return decodeIndexedChoice(data, "CURVE") }

// DecodeOutputResponseTime decodes OUTPUT_RESPONSE_TIME's (0x0345) GET
// response. TODO(hardware).
func DecodeOutputResponseTime(data []byte) (IndexedChoice, error) {
	return decodeIndexedChoice(data, "OUTPUT_RESPONSE_TIME")
}

// DecodeModulationFrequency decodes MODULATION_FREQUENCY's (0x0347) GET
// response. TODO(hardware).
func DecodeModulationFrequency(data []byte) (IndexedChoice, error) {
	return decodeIndexedChoice(data, "MODULATION_FREQUENCY")
}

// EncodeIndexedChoiceSet builds the shared 1-byte SET request (new 1-based
// index) for CURVE/OUTPUT_RESPONSE_TIME/MODULATION_FREQUENCY.
func EncodeIndexedChoiceSet(index byte) []byte { return []byte{index} }

// IndexedDescription is the GET response shape this package models for
// CURVE_DESCRIPTION, OUTPUT_RESPONSE_TIME_DESCRIPTION and
// MODULATION_FREQUENCY_DESCRIPTION: an echoed 1-byte index followed by a
// variable-length ASCII label (no NUL terminator, RDM label convention).
// TODO(hardware): see file doc comment.
type IndexedDescription struct {
	Index       byte
	Description string
}

func decodeIndexedDescription(data []byte, name string) (IndexedDescription, error) {
	if len(data) < 1 {
		return IndexedDescription{}, fmt.Errorf("%w: %s wants >=1 byte, got %d", ErrBadDimmerLength, name, len(data))
	}
	return IndexedDescription{Index: data[0], Description: string(data[1:])}, nil
}

// DecodeCurveDescription decodes CURVE_DESCRIPTION's (0x0344) GET response.
// TODO(hardware).
func DecodeCurveDescription(data []byte) (IndexedDescription, error) {
	return decodeIndexedDescription(data, "CURVE_DESCRIPTION")
}

// DecodeOutputResponseTimeDescription decodes OUTPUT_RESPONSE_TIME_
// DESCRIPTION's (0x0346) GET response. TODO(hardware).
func DecodeOutputResponseTimeDescription(data []byte) (IndexedDescription, error) {
	return decodeIndexedDescription(data, "OUTPUT_RESPONSE_TIME_DESCRIPTION")
}

// DecodeModulationFrequencyDescription decodes MODULATION_FREQUENCY_
// DESCRIPTION's (0x0348) GET response. TODO(hardware).
func DecodeModulationFrequencyDescription(data []byte) (IndexedDescription, error) {
	return decodeIndexedDescription(data, "MODULATION_FREQUENCY_DESCRIPTION")
}

// EncodeIndexedDescriptionRequest builds a *_DESCRIPTION GET request: the
// single index byte being asked about.
func EncodeIndexedDescriptionRequest(index byte) []byte { return []byte{index} }

// MinimumLevel is MINIMUM_LEVEL's (0x0341) GET/SET payload: separate
// rising/falling thresholds plus whether the device should extinguish
// entirely below them. TODO(hardware): see file doc comment.
type MinimumLevel struct {
	Increasing uint16
	Decreasing uint16
	OnBelowMin byte
}

// DecodeMinimumLevel decodes MINIMUM_LEVEL's GET response. TODO(hardware).
func DecodeMinimumLevel(data []byte) (MinimumLevel, error) {
	if len(data) != 5 {
		return MinimumLevel{}, fmt.Errorf("%w: MINIMUM_LEVEL wants 5 bytes, got %d", ErrBadDimmerLength, len(data))
	}
	return MinimumLevel{
		Increasing: binary.BigEndian.Uint16(data[0:2]),
		Decreasing: binary.BigEndian.Uint16(data[2:4]),
		OnBelowMin: data[4],
	}, nil
}

// EncodeMinimumLevel is the inverse of DecodeMinimumLevel.
func EncodeMinimumLevel(v MinimumLevel) []byte {
	b := make([]byte, 5)
	binary.BigEndian.PutUint16(b[0:2], v.Increasing)
	binary.BigEndian.PutUint16(b[2:4], v.Decreasing)
	b[4] = v.OnBelowMin
	return b
}

// DecodeMaximumLevel decodes MAXIMUM_LEVEL's (0x0342) GET response: a
// single 16-bit level cap. TODO(hardware): see file doc comment (lower risk
// than MINIMUM_LEVEL's shape, but still unverified).
func DecodeMaximumLevel(data []byte) (uint16, error) {
	if len(data) != 2 {
		return 0, fmt.Errorf("%w: MAXIMUM_LEVEL wants 2 bytes, got %d", ErrBadDimmerLength, len(data))
	}
	return binary.BigEndian.Uint16(data), nil
}

// EncodeMaximumLevel is the inverse of DecodeMaximumLevel.
func EncodeMaximumLevel(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// IdentifyMode is IDENTIFY_MODE's (0x1040) 1-byte enum. TODO(hardware): the
// 0x00/0x01 value assignment is a best reading, not independently confirmed.
type IdentifyMode byte

// Identify mode values.
const (
	IdentifyModeQuiet IdentifyMode = 0x00
	IdentifyModeLoud  IdentifyMode = 0x01
)

// String renders a human label.
func (m IdentifyMode) String() string {
	if m == IdentifyModeLoud {
		return "Loud"
	}
	return "Quiet"
}

// DecodeIdentifyMode decodes IDENTIFY_MODE's GET response. TODO(hardware).
func DecodeIdentifyMode(data []byte) (IdentifyMode, error) {
	if len(data) != 1 {
		return 0, fmt.Errorf("%w: IDENTIFY_MODE wants 1 byte, got %d", ErrBadDimmerLength, len(data))
	}
	return IdentifyMode(data[0]), nil
}

// EncodeIdentifyMode is the inverse of DecodeIdentifyMode.
func EncodeIdentifyMode(m IdentifyMode) []byte { return []byte{byte(m)} }
