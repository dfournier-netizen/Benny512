package rdm

import (
	"errors"
	"fmt"

	"benny512/internal/bytesio"
)

// StartCode is RDM slot 0.
const StartCode byte = 0xCC

// SubStartCode is RDM slot 1 (SC_SUB_MESSAGE).
const SubStartCode byte = 0x01

// MaxParameterDataLength is the maximum PDL (slot 23) value.
const MaxParameterDataLength = 231

// MinimumMessageLength is the 24-byte fixed header (slots 0-23) + 0 PD +
// 2-byte checksum.
const MinimumMessageLength = 26

// Decode error sentinels. Use errors.Is against these, or errors.As against
// the accompanying detail types where present.
var (
	ErrTooShort               = errors.New("rdm: message shorter than minimum")
	ErrInvalidStartCode       = errors.New("rdm: invalid start code")
	ErrInvalidSubStartCode    = errors.New("rdm: invalid sub-start code")
	ErrMessageLengthMismatch  = errors.New("rdm: message length mismatch")
	ErrInvalidPDL             = errors.New("rdm: PDL exceeds maximum")
	ErrTruncatedParameterData = errors.New("rdm: truncated parameter data")
	ErrChecksumMismatch       = errors.New("rdm: checksum mismatch")
	ErrInvalidCommandClass    = errors.New("rdm: invalid command class")

	// DUB (Discovery Unique Branch) response decode.
	ErrDUBPreambleTooLong  = errors.New("rdm: DUB preamble too long")
	ErrDUBMissingSeparator = errors.New("rdm: DUB missing separator")
	ErrDUBTooShort         = errors.New("rdm: DUB response too short")
	ErrDUBChecksumMismatch = errors.New("rdm: DUB checksum mismatch")
)

// DecodeError carries diagnostic detail alongside one of the Err* sentinels
// above; use errors.Is(err, rdm.ErrChecksumMismatch) etc. to classify it.
type DecodeError struct {
	Err    error
	Detail string
}

func (e *DecodeError) Error() string {
	if e.Detail == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Err.Error(), e.Detail)
}

func (e *DecodeError) Unwrap() error { return e.Err }

func decodeErr(err error, detail string) error {
	return &DecodeError{Err: err, Detail: detail}
}

// Checksum computes the 16-bit unsigned additive checksum over bytes (RDM's
// checksum is a plain sum, not CRC/XOR).
func Checksum(bytes []byte) uint16 {
	var sum uint32
	for _, b := range bytes {
		sum += uint32(b)
	}
	return uint16(sum)
}

// Encode serializes an RDM message to its on-wire byte representation,
// computing Message Length and checksum. Encode is total: parameter data
// longer than MaxParameterDataLength is silently truncated (clamped) rather
// than rejected, mirroring the reference implementation.
func Encode(m Message) []byte {
	w := bytesio.NewWriter()
	w.WriteU8(StartCode)
	w.WriteU8(SubStartCode)
	pdl := len(m.ParameterData)
	if pdl > MaxParameterDataLength {
		pdl = MaxParameterDataLength
	}
	messageLength := 24 + pdl
	w.WriteU8(byte(messageLength))
	w.WriteBytes(m.DestinationUID.Bytes())
	w.WriteBytes(m.SourceUID.Bytes())
	w.WriteU8(m.TransactionNumber)
	w.WriteU8(m.PortIDOrResponseType)
	w.WriteU8(m.MessageCount)
	w.WriteU16BE(m.SubDevice)
	w.WriteU8(byte(m.CommandClass))
	w.WriteU16BE(uint16(m.ParameterID))
	w.WriteU8(byte(pdl))
	w.WriteBytes(m.ParameterData[:pdl])
	sum := Checksum(w.Bytes())
	w.WriteU16BE(sum)
	return w.Bytes()
}

// Decode parses an on-wire RDM message, validating start codes, declared
// message length, PDL bounds, and checksum.
func Decode(b []byte) (Message, error) {
	if len(b) < MinimumMessageLength {
		return Message{}, decodeErr(ErrTooShort, fmt.Sprintf("need %d, have %d", MinimumMessageLength, len(b)))
	}
	if b[0] != StartCode {
		return Message{}, decodeErr(ErrInvalidStartCode, fmt.Sprintf("0x%02X", b[0]))
	}
	if b[1] != SubStartCode {
		return Message{}, decodeErr(ErrInvalidSubStartCode, fmt.Sprintf("0x%02X", b[1]))
	}

	pdl := int(b[23])
	if pdl > MaxParameterDataLength {
		return Message{}, decodeErr(ErrInvalidPDL, fmt.Sprintf("%d > %d", pdl, MaxParameterDataLength))
	}
	pdEnd := 24 + pdl
	if len(b) < pdEnd+2 {
		have := len(b) - 24
		if have < 0 {
			have = 0
		}
		return Message{}, decodeErr(ErrTruncatedParameterData, fmt.Sprintf("need %d, have %d", pdl, have))
	}

	declaredLength := int(b[2])
	if declaredLength != pdEnd {
		return Message{}, decodeErr(ErrMessageLengthMismatch, fmt.Sprintf("declared %d, expected %d", declaredLength, pdEnd))
	}

	computed := Checksum(b[0:pdEnd])
	expected := uint16(b[pdEnd])<<8 | uint16(b[pdEnd+1])
	if computed != expected {
		return Message{}, decodeErr(ErrChecksumMismatch, fmt.Sprintf("expected 0x%04X, computed 0x%04X", expected, computed))
	}

	cc := CommandClass(b[20])
	if !cc.IsValid() {
		return Message{}, decodeErr(ErrInvalidCommandClass, fmt.Sprintf("0x%02X", b[20]))
	}

	destinationUID := uidFromRawBytes(b[3:9])
	sourceUID := uidFromRawBytes(b[9:15])
	subDevice := uint16(b[18])<<8 | uint16(b[19])
	parameterIDRaw := uint16(b[21])<<8 | uint16(b[22])
	parameterData := make([]byte, pdl)
	copy(parameterData, b[24:pdEnd])

	return Message{
		DestinationUID:       destinationUID,
		SourceUID:            sourceUID,
		TransactionNumber:    b[15],
		PortIDOrResponseType: b[16],
		MessageCount:         b[17],
		SubDevice:            subDevice,
		CommandClass:         cc,
		ParameterID:          ParameterID(parameterIDRaw),
		ParameterData:        parameterData,
	}, nil
}

// --- Discovery Unique Branch (DUB) response encode/decode ---
//
// The DUB response is NOT a standard RDM message (no header/slots) — it's a
// specially OR-masked encoding of just the responder's UID + a checksum of
// that UID, designed so overlapping simultaneous responses garble into
// detectably-invalid data rather than a plausible-looking wrong UID. This is
// OR-encode / AND-decode with masks 0xAA/0x55, NOT XOR.

// dubChecksum is the sum of the 6 UID bytes only (there is no RDM header to
// checksum here).
func dubChecksum(uid UID) uint16 {
	return Checksum(uid.Bytes())
}

// EncodeDUBResponse builds a DUB response: preambleLength bytes of 0xFE
// (clamped to 0...7), then a 0xAA separator, then the 8-byte payload (UID +
// its checksum) OR-encoded as 16 bytes.
func EncodeDUBResponse(uid UID, preambleLength int) []byte {
	clamped := preambleLength
	if clamped < 0 {
		clamped = 0
	}
	if clamped > 7 {
		clamped = 7
	}
	out := make([]byte, 0, clamped+1+16)
	for i := 0; i < clamped; i++ {
		out = append(out, 0xFE)
	}
	out = append(out, 0xAA)
	sum := dubChecksum(uid)
	payload := append(append([]byte{}, uid.Bytes()...), byte(sum>>8), byte(sum))
	for _, d := range payload {
		out = append(out, d|0xAA, d|0x55)
	}
	return out
}

// DecodeDUBResponse decodes a DUB response back into the responder's UID,
// verifying its checksum.
func DecodeDUBResponse(b []byte) (UID, error) {
	idx := 0
	preambleCount := 0
	for idx < len(b) && b[idx] == 0xFE {
		idx++
		preambleCount++
		if preambleCount > 7 {
			return UID{}, decodeErr(ErrDUBPreambleTooLong, "")
		}
	}
	if idx >= len(b) || b[idx] != 0xAA {
		return UID{}, decodeErr(ErrDUBMissingSeparator, "")
	}
	idx++

	const encodedLength = 16 // 8 payload bytes * 2
	if len(b)-idx < encodedLength {
		have := len(b) - idx
		if have < 0 {
			have = 0
		}
		return UID{}, decodeErr(ErrDUBTooShort, fmt.Sprintf("need %d, have %d", encodedLength, have))
	}

	decoded := make([]byte, 8)
	for i := 0; i < 8; i++ {
		e1 := b[idx+i*2]
		e2 := b[idx+i*2+1]
		decoded[i] = e1 & e2
	}

	uid := uidFromRawBytes(decoded[0:6])
	expected := uint16(decoded[6])<<8 | uint16(decoded[7])
	computed := dubChecksum(uid)
	if computed != expected {
		return UID{}, decodeErr(ErrDUBChecksumMismatch, fmt.Sprintf("expected 0x%04X, computed 0x%04X", expected, computed))
	}
	return uid, nil
}

// DiscoveryUniqueBranchRequest builds a DISC_UNIQUE_BRANCH request message
// (a normal RDM message; Parameter Data = 6-byte lower bound + 6-byte upper
// bound UID).
func DiscoveryUniqueBranchRequest(source, lowerBound, upperBound UID, transactionNumber, portID byte) Message {
	pd := append(append([]byte{}, lowerBound.Bytes()...), upperBound.Bytes()...)
	return Message{
		DestinationUID:       BroadcastAll,
		SourceUID:            source,
		TransactionNumber:    transactionNumber,
		PortIDOrResponseType: portID,
		MessageCount:         0,
		SubDevice:            RootDevice,
		CommandClass:         DiscoveryCommand,
		ParameterID:          PIDDiscUniqueBranch,
		ParameterData:        pd,
	}
}
