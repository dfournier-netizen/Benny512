// Package sacn encodes ANSI E1.31 (sACN) packets.
package sacn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

var ErrInvalidUniverse = errors.New("sACN universe must be 1..63999")

// Octet offsets of every field of an E1.31 Data Packet, per ANSI E1.31-2025
// Table B-13. Each offset is absolute within the 638 octet packet.
const (
	offPreambleSize    = 0   // 0-1     Preamble Size
	offPostambleSize   = 2   // 2-3     Post-amble Size
	offACNIdentifier   = 4   // 4-15    ACN Packet Identifier
	offRootFlagsLen    = 16  // 16-17   Root Flags & Length
	offRootVector      = 18  // 18-21   Root Vector
	offCID             = 22  // 22-37   CID
	offFramingFlagsLen = 38  // 38-39   Framing Flags & Length
	offFramingVector   = 40  // 40-43   Framing Vector
	offSourceName      = 44  // 44-107  Source Name
	offPriority        = 108 // 108     Priority
	offSyncAddress     = 109 // 109-110 Synchronization Address
	offSequence        = 111 // 111     Sequence Number
	offOptions         = 112 // 112     Options
	offUniverse        = 113 // 113-114 Universe
	offDMPFlagsLen     = 115 // 115-116 DMP Flags & Length
	offDMPVector       = 117 // 117     DMP Vector
	offAddressDataType = 118 // 118     Address Type & Data Type
	offFirstPropAddr   = 119 // 119-120 First Property Address
	offAddressIncr     = 121 // 121-122 Address Increment
	offPropValueCount  = 123 // 123-124 Property Value Count
	offStartCode       = 125 // 125     DMX512-A START Code
	offDMXData         = 126 // 126-637 DMX512-A data slots

	// PacketLen is the encoded size of an E1.31 Data Packet carrying a full
	// 512 slot DMX512-A universe.
	PacketLen = 638

	maxSlots      = 512
	sourceNameLen = 64 // octets, including at least one null terminator
	maxUniverse   = 63999
)

// Constant field values, from ANSI E1.31-2025 Appendix A and Table B-13.
const (
	preambleSize         = 0x0010
	postambleSize        = 0x0000
	vectorRootE131Data   = 0x00000004 // VECTOR_ROOT_E131_DATA
	vectorE131DataPacket = 0x00000002 // VECTOR_E131_DATA_PACKET
	vectorDMPSetProperty = 0x02       // VECTOR_DMP_SET_PROPERTY
	addressDataType      = 0xa1       // 1 octet properties, address incrementing
	firstPropertyAddress = 0x0000     // DMX512-A START Code sits at DMP address 0
	addressIncrement     = 0x0001     // each property is 1 octet
	propertyValueCount   = 0x0201     // 513: START Code + 512 slots
	startCodeDMX512A     = 0x00
	syncAddressNone      = 0x0000 // 0 = this packet is not synchronized
	pduFlags             = 0x7000 // high nibble of every Flags & Length field
)

// Option bits of the E1.31 Data Packet Options field (octet 112), per ANSI
// E1.31-2025 Section 6.2.6. Bits 0 through 4 are reserved for future use and
// "shall be transmitted as 0".
const (
	// OptionPreviewData is bit 7. Set to 1, the data is for visualization or
	// media server preview only and shall not generate live output.
	OptionPreviewData byte = 1 << 7

	// OptionStreamTerminated is bit 6. Set to 1, it tells receivers that the
	// source has terminated transmission of this universe, and that they
	// should enter network data loss condition without waiting for
	// E131_NETWORK_DATA_LOSS_TIMEOUT. Section 6.2.6 also states that any
	// property values in a packet carrying this bit shall be ignored.
	OptionStreamTerminated byte = 1 << 6

	// OptionForceSynchronization is bit 5. Only meaningful for synchronized
	// streams, which this package does not emit.
	OptionForceSynchronization byte = 1 << 5
)

// acnPacketIdentifier is the 12 octet identifier that marks a packet as E1.17.
var acnPacketIdentifier = []byte("ASC-E1.17\x00\x00\x00")

// EncodeDataPacket returns one E1.31 Data Packet carrying a DMX512 payload.
// The payload is copied into the packet and padded with zeroes when shorter
// than 512 slots. A payload longer than 512 slots is rejected.
//
// The packet is emitted unsynchronized: the Synchronization Address field is
// 0, which ANSI E1.31-2025 defines as "not synchronized". The Options field
// is 0; use EncodeDataPacketWithOptions to set it.
func EncodeDataPacket(payload []byte, cid [16]byte, source string, priority byte, sequence byte, universe uint16) ([]byte, error) {
	return EncodeDataPacketWithOptions(payload, cid, source, priority, sequence, universe, 0)
}

// EncodeDataPacketWithOptions is EncodeDataPacket with an explicit value for
// the Options field (octet 112), built from the Option* constants. See ANSI
// E1.31-2025 Section 6.2.6.
func EncodeDataPacketWithOptions(payload []byte, cid [16]byte, source string, priority byte, sequence byte, universe uint16, options byte) ([]byte, error) {
	if universe == 0 || universe > maxUniverse {
		return nil, ErrInvalidUniverse
	}
	if len(payload) > maxSlots {
		return nil, fmt.Errorf("sACN payload has %d slots; maximum is %d", len(payload), maxSlots)
	}

	b := make([]byte, PacketLen)

	// Root layer. Each PDU length counts the octets from its own Flags &
	// Length field to the end of the packet.
	binary.BigEndian.PutUint16(b[offPreambleSize:], preambleSize)
	binary.BigEndian.PutUint16(b[offPostambleSize:], postambleSize)
	copy(b[offACNIdentifier:offRootFlagsLen], acnPacketIdentifier)
	putFlagsLen(b[offRootFlagsLen:], PacketLen-offRootFlagsLen) // 622 -> 0x726e
	binary.BigEndian.PutUint32(b[offRootVector:], vectorRootE131Data)
	copy(b[offCID:offFramingFlagsLen], cid[:])

	// E1.31 framing layer.
	putFlagsLen(b[offFramingFlagsLen:], PacketLen-offFramingFlagsLen) // 600 -> 0x7258
	binary.BigEndian.PutUint32(b[offFramingVector:], vectorE131DataPacket)
	copy(b[offSourceName:offPriority], truncateSourceName(source))
	b[offPriority] = priority
	binary.BigEndian.PutUint16(b[offSyncAddress:], syncAddressNone)
	b[offSequence] = sequence
	b[offOptions] = options
	binary.BigEndian.PutUint16(b[offUniverse:], universe)

	// DMP layer.
	putFlagsLen(b[offDMPFlagsLen:], PacketLen-offDMPFlagsLen) // 523 -> 0x720b
	b[offDMPVector] = vectorDMPSetProperty
	b[offAddressDataType] = addressDataType
	binary.BigEndian.PutUint16(b[offFirstPropAddr:], firstPropertyAddress)
	binary.BigEndian.PutUint16(b[offAddressIncr:], addressIncrement)
	binary.BigEndian.PutUint16(b[offPropValueCount:], propertyValueCount)

	// Property values: the START Code followed by the 512 data slots.
	b[offStartCode] = startCodeDMX512A
	copy(b[offDMXData:], payload)

	return b, nil
}

// putFlagsLen writes a PDU Flags and Length field: the low 12 bits carry the
// PDU length in octets and the high nibble is the fixed 0x7 flags value.
func putFlagsLen(dst []byte, pduLen int) {
	binary.BigEndian.PutUint16(dst, uint16(pduFlags|pduLen&0x0fff))
}

// truncateSourceName limits a source name to sourceNameLen-1 octets so the
// 64 octet field always ends with at least one null terminator, cutting only
// on a UTF-8 rune boundary so that a multi-byte rune is never split.
func truncateSourceName(s string) string {
	const max = sourceNameLen - 1 // 63, leaving room for the null terminator
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
