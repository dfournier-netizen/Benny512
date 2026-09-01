package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// This file adds typed codecs for the E1.20 "service life" and "reset"
// family of standard PIDs (§10.8 Lamp/Device Settings, §10.11.2 Reset
// Device) Phase D's backend pass wires typed Client support for. Every wire
// layout here is transcribed directly from the ANSI E1.20-2025 PDF text
// (§10.8.1-10.8.6, §10.11.2), not inferred by pattern-matching against a
// sibling PID the way internal/rdm/dimmer.go's E1.37-1 codecs had to be —
// these are CONFIRMED, not TODO(hardware).

// ErrBadServiceLifeLength is returned when a service-life/reset PID's
// parameter data isn't the length its wire layout requires.
var ErrBadServiceLifeLength = errors.New("rdm: unexpected service-life/reset parameter data length")

// DecodeUint32Counter decodes the shared 4-byte big-endian UINT32 shape
// DEVICE_HOURS (0x0400), LAMP_HOURS (0x0401), LAMP_STRIKES (0x0402) and
// DEVICE_POWER_CYCLES (0x0405) all use for both their GET response and SET
// request Parameter Data (E1.20 §10.8.1/10.8.2/10.8.3/10.8.6). name is used
// only to make a length-mismatch error legible.
func DecodeUint32Counter(data []byte, name string) (uint32, error) {
	if len(data) != 4 {
		return 0, fmt.Errorf("%w: %s wants 4 bytes, got %d", ErrBadServiceLifeLength, name, len(data))
	}
	return binary.BigEndian.Uint32(data), nil
}

// EncodeUint32Counter is DecodeUint32Counter's inverse, for SET requests and
// tests/demo-mode fixtures.
func EncodeUint32Counter(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// LampState is LAMP_STATE's (0x0403) 1-byte enum, per E1.20 Table A-8.
type LampState byte

// Lamp State Defines (Table A-8).
const (
	LampOff        LampState = 0x00 // "No demonstrable light output"
	LampOn         LampState = 0x01
	LampStrike     LampState = 0x02 // "Arc-Lamp ignite"
	LampStandby    LampState = 0x03 // "Arc-Lamp Reduced Power Mode"
	LampNotPresent LampState = 0x04 // "Lamp not installed"
	LampError      LampState = 0x7F
	// Manufacturer-specific Lamp State range is 0x80-0xDF per Table A-8;
	// 0xE0-0xFF is unassigned.
)

// String renders a human label for the well-known Table A-8 values, and a
// hex fallback (distinguishing the manufacturer-specific band) for anything
// else.
func (s LampState) String() string {
	switch s {
	case LampOff:
		return "Off"
	case LampOn:
		return "On"
	case LampStrike:
		return "Strike"
	case LampStandby:
		return "Standby"
	case LampNotPresent:
		return "Not Present"
	case LampError:
		return "Error"
	default:
		if s >= 0x80 && s <= 0xDF {
			return fmt.Sprintf("Manufacturer-Specific (0x%02X)", byte(s))
		}
		return fmt.Sprintf("Unknown (0x%02X)", byte(s))
	}
}

// DecodeLampState decodes LAMP_STATE's 1-byte GET response.
func DecodeLampState(data []byte) (LampState, error) {
	if len(data) != 1 {
		return 0, fmt.Errorf("%w: LAMP_STATE wants 1 byte, got %d", ErrBadServiceLifeLength, len(data))
	}
	return LampState(data[0]), nil
}

// EncodeLampState is DecodeLampState's inverse.
func EncodeLampState(s LampState) []byte { return []byte{byte(s)} }

// ResetMode is RESET_DEVICE's (0x1001) 1-byte SET-only Parameter Data, per
// E1.20 §10.11.2. There is no GET form of this PID at all — see
// params.Client.ResetDevice's doc comment.
type ResetMode byte

// Reset Device values (E1.20 §10.11.2's PD table — the only two legal
// values; anything else is not defined by the spec).
const (
	ResetWarm ResetMode = 0x01
	ResetCold ResetMode = 0xFF
)

// String renders a human label.
func (m ResetMode) String() string {
	switch m {
	case ResetWarm:
		return "Warm"
	case ResetCold:
		return "Cold"
	default:
		return fmt.Sprintf("Unknown (0x%02X)", byte(m))
	}
}

// IsValid reports whether m is one of the two spec-defined reset modes.
func (m ResetMode) IsValid() bool { return m == ResetWarm || m == ResetCold }

// EncodeResetDevice builds RESET_DEVICE's 1-byte SET request Parameter Data.
func EncodeResetDevice(m ResetMode) []byte { return []byte{byte(m)} }

// DecodeFactoryDefaults decodes FACTORY_DEFAULTS' (0x0090) 1-byte boolean
// GET response (E1.20 §10.5.6: "True/False (1/0)" — whether the device is
// CURRENTLY set to its factory defaults, not a command).
func DecodeFactoryDefaults(data []byte) (bool, error) {
	if len(data) != 1 {
		return false, fmt.Errorf("%w: FACTORY_DEFAULTS wants 1 byte, got %d", ErrBadServiceLifeLength, len(data))
	}
	return data[0] != 0, nil
}
