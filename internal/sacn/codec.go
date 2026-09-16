// Package sacn encodes ANSI E1.31 (sACN) packets.
package sacn

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrInvalidUniverse = errors.New("sACN universe must be 1..63999")

// EncodeDataPacket returns one E1.31 Data Packet carrying a DMX512 payload.
// The payload is copied into the packet and padded with zeroes when shorter
// than 512 slots. A payload longer than 512 slots is rejected.
func EncodeDataPacket(payload []byte, cid [16]byte, source string, priority byte, sequence byte, universe uint16) ([]byte, error) {
	if universe == 0 || universe > 63999 {
		return nil, ErrInvalidUniverse
	}
	if len(payload) > 512 {
		return nil, fmt.Errorf("sACN payload has %d slots; maximum is 512", len(payload))
	}
	if len(source) > 64 {
		source = source[:64]
	}
	b := make([]byte, 638)
	b[0], b[1] = 0x00, 0x10
	copy(b[4:16], []byte("ASC-E1.17\x00\x00\x00\x00\x00\x00\x00"))
	putFlagsLen(b[16:18], 0x026)
	b[18], b[19], b[20], b[21] = 0, 0, 0, 2
	copy(b[22:38], cid[:])
	putFlagsLen(b[38:40], 0x04d)
	b[40], b[41], b[42], b[43] = 0, 0, 0, 2
	copy(b[44:108], source)
	b[108] = priority
	// 109..110: reserved (zero)
	b[111] = sequence
	// 112: options (zero)
	binary.BigEndian.PutUint16(b[113:115], universe)
	putFlagsLen(b[115:117], 0x20b)
	b[117] = 2    // DMP Set Property message
	b[118] = 0xa1 // address/data type: 16-bit, relative, incrementing
	binary.BigEndian.PutUint16(b[119:121], 0)
	binary.BigEndian.PutUint16(b[121:123], 513)
	b[123] = 0 // DMX start code
	copy(b[125:], payload)
	return b, nil
}

func putFlagsLen(dst []byte, n int) { binary.BigEndian.PutUint16(dst, uint16(0x7000|n)) }
