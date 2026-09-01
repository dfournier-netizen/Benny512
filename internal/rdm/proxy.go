package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// This file adds a typed decoder for PROXIED_DEVICE_COUNT's (0x0011) GET
// response, transcribed from the ANSI E1.20-2025 PDF text (§8.4.1): 3 bytes,
// Device Count (16-bit) + List Change (1 byte, 0/1) — CONFIRMED, not
// inferred. Package registry uses this to populate Fixture's structured
// proxy-status fields (report/Phase D task 1's "expose proxy status as
// structured data" ask); see internal/web/server.go's fixtureJSON for the
// JSON shape this feeds.

// ErrBadProxiedDeviceCountLength is returned when PROXIED_DEVICE_COUNT's
// parameter data isn't the 3-byte shape E1.20 §8.4.1 defines.
var ErrBadProxiedDeviceCountLength = errors.New("rdm: unexpected PROXIED_DEVICE_COUNT parameter data length")

// ProxiedDeviceCount is PROXIED_DEVICE_COUNT's decoded GET response.
type ProxiedDeviceCount struct {
	Count       uint16
	ListChanged bool
}

// DecodeProxiedDeviceCount decodes PROXIED_DEVICE_COUNT's 3-byte GET
// response (E1.20 §8.4.1).
func DecodeProxiedDeviceCount(data []byte) (ProxiedDeviceCount, error) {
	if len(data) != 3 {
		return ProxiedDeviceCount{}, fmt.Errorf("%w: wants 3 bytes, got %d", ErrBadProxiedDeviceCountLength, len(data))
	}
	return ProxiedDeviceCount{
		Count:       binary.BigEndian.Uint16(data[0:2]),
		ListChanged: data[2] != 0,
	}, nil
}

// EncodeProxiedDeviceCount is DecodeProxiedDeviceCount's inverse, for tests
// and the demo-mode fake responder.
func EncodeProxiedDeviceCount(v ProxiedDeviceCount) []byte {
	b := make([]byte, 3)
	binary.BigEndian.PutUint16(b[0:2], v.Count)
	if v.ListChanged {
		b[2] = 1
	}
	return b
}
