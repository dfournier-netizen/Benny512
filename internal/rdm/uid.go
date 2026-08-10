// Package rdm implements ANSI E1.20 RDM message and Discovery Unique Branch
// (DUB) wire codecs.
package rdm

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidUIDByteCount is returned by UIDFromBytes when the input is not
// exactly 6 bytes.
var ErrInvalidUIDByteCount = errors.New("rdm: invalid UID byte count")

// UID is a 48-bit RDM Unique ID: a 16-bit ESTA-assigned Manufacturer ID
// (bits 47-32) plus a 32-bit manufacturer-assigned Device ID (bits 31-0).
// Text form is "MMMM:DDDDDDDD" (uppercase hex).
type UID struct {
	ManufacturerID uint16
	DeviceID       uint32
}

// BroadcastAll is the all-devices broadcast UID: FFFF:FFFFFFFF.
var BroadcastAll = UID{ManufacturerID: 0xFFFF, DeviceID: 0xFFFFFFFF}

// Broadcast returns the manufacturer-specific broadcast UID MMMM:FFFFFFFF.
// Per spec, responders never ACK a broadcast — they just act on it.
func Broadcast(manufacturerID uint16) UID {
	return UID{ManufacturerID: manufacturerID, DeviceID: 0xFFFFFFFF}
}

// IsBroadcast reports whether the device ID is the broadcast value.
func (u UID) IsBroadcast() bool { return u.DeviceID == 0xFFFFFFFF }

// Bytes returns the big-endian 6-byte wire representation (manufacturer ID
// hi,lo then device ID hi..lo), matching RDM slots 3-8 / 9-14.
func (u UID) Bytes() []byte {
	return []byte{
		byte(u.ManufacturerID >> 8),
		byte(u.ManufacturerID),
		byte(u.DeviceID >> 24),
		byte(u.DeviceID >> 16),
		byte(u.DeviceID >> 8),
		byte(u.DeviceID),
	}
}

// UIDFromBytes parses a 6-byte big-endian wire representation.
func UIDFromBytes(b []byte) (UID, error) {
	if len(b) != 6 {
		return UID{}, fmt.Errorf("%w: %d", ErrInvalidUIDByteCount, len(b))
	}
	return uidFromRawBytes(b), nil
}

// uidFromRawBytes is the non-validating internal constructor for codec use
// where the caller has already guaranteed exactly 6 bytes.
func uidFromRawBytes(b []byte) UID {
	_ = b[5] // bounds check hint
	return UID{
		ManufacturerID: uint16(b[0])<<8 | uint16(b[1]),
		DeviceID:       uint32(b[2])<<24 | uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5]),
	}
}

// ParseUID parses the "MMMM:DDDDDDDD" text form. It returns ok=false for any
// malformed input rather than an error, mirroring string-parsing convention.
func ParseUID(s string) (UID, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 8 {
		return UID{}, false
	}
	m, err := strconv.ParseUint(parts[0], 16, 16)
	if err != nil {
		return UID{}, false
	}
	d, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return UID{}, false
	}
	return UID{ManufacturerID: uint16(m), DeviceID: uint32(d)}, true
}

// String renders the "MMMM:DDDDDDDD" uppercase hex text form.
func (u UID) String() string {
	return fmt.Sprintf("%04X:%08X", u.ManufacturerID, u.DeviceID)
}

// Compare orders UIDs by manufacturer ID then device ID; returns <0, 0, >0.
func Compare(a, b UID) int {
	if a.ManufacturerID != b.ManufacturerID {
		if a.ManufacturerID < b.ManufacturerID {
			return -1
		}
		return 1
	}
	switch {
	case a.DeviceID < b.DeviceID:
		return -1
	case a.DeviceID > b.DeviceID:
		return 1
	default:
		return 0
	}
}

// Less reports whether u sorts before other (manufacturer ID, then device ID).
func (u UID) Less(other UID) bool { return Compare(u, other) < 0 }
