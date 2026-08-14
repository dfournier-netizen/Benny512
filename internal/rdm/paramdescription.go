package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadParameterDescription is returned when PARAMETER_DESCRIPTION
// parameter data is shorter than its 20-byte fixed portion.
var ErrBadParameterDescription = errors.New("rdm: malformed PARAMETER_DESCRIPTION data")

// ParameterDescription is PARAMETER_DESCRIPTION's (0x0051) GET response
// (report §2.2). Wire layout, all CONFIRMED except where noted:
//
//	0-1   pid            UINT16 BE
//	2     pdl_size       UINT8
//	3     data_type      UINT8 (DataType)
//	4     command_class  UINT8 (PDCommandClass)
//	5     type           UINT8 — deprecated/vestigial, always 0 (WEAKLY CONFIRMED semantics)
//	6     unit           UINT8 (Unit)
//	7     prefix         UINT8 (Prefix)
//	8-11  min_value       UINT32 BE, sign per data_type
//	12-15 max_value       UINT32 BE, sign per data_type
//	16-19 default_value   UINT32 BE, sign per data_type
//	20-.. description     ASCII, no NUL terminator, length = PDL-20, max 32
type ParameterDescription struct {
	PID          ParameterID
	PDLSize      byte
	DataType     DataType
	CommandClass PDCommandClass
	// Type is the deprecated/vestigial byte at offset 5; round-tripped
	// verbatim rather than dropped, per report §2.2's "no defined semantics
	// in current sources" note.
	Type   byte
	Unit   Unit
	Prefix Prefix
	// MinValue/MaxValue/DefaultValue are decoded as a 32-bit two's-complement
	// value when DataType.IsSigned(), else as an unsigned 32-bit magnitude —
	// see DataType.IsSigned's doc comment for the confirmation caveat on this
	// interpretation (report §1.4/§2.2).
	MinValue     int64
	MaxValue     int64
	DefaultValue int64
	Description  string
}

// signExtend32 interprets raw as either a sign-extended int32 or a plain
// unsigned magnitude, per dt.IsSigned().
func signExtend32(raw uint32, dt DataType) int64 {
	if dt.IsSigned() {
		return int64(int32(raw))
	}
	return int64(raw)
}

func truncate32(v int64) uint32 {
	return uint32(v)
}

// DecodeParameterDescription parses a PARAMETER_DESCRIPTION GET response.
func DecodeParameterDescription(data []byte) (ParameterDescription, error) {
	if len(data) < 20 {
		return ParameterDescription{}, fmt.Errorf("%w: want >=20 bytes, got %d", ErrBadParameterDescription, len(data))
	}
	dt := DataType(data[3])
	return ParameterDescription{
		PID:          ParameterID(binary.BigEndian.Uint16(data[0:2])),
		PDLSize:      data[2],
		DataType:     dt,
		CommandClass: PDCommandClass(data[4]),
		Type:         data[5],
		Unit:         Unit(data[6]),
		Prefix:       Prefix(data[7]),
		MinValue:     signExtend32(binary.BigEndian.Uint32(data[8:12]), dt),
		MaxValue:     signExtend32(binary.BigEndian.Uint32(data[12:16]), dt),
		DefaultValue: signExtend32(binary.BigEndian.Uint32(data[16:20]), dt),
		Description:  string(data[20:]),
	}, nil
}

// EncodeParameterDescription is the inverse of DecodeParameterDescription.
// Description longer than 32 bytes is truncated (RDM label convention).
func EncodeParameterDescription(d ParameterDescription) []byte {
	desc := d.Description
	if len(desc) > 32 {
		desc = desc[:32]
	}
	b := make([]byte, 20+len(desc))
	binary.BigEndian.PutUint16(b[0:2], uint16(d.PID))
	b[2] = d.PDLSize
	b[3] = byte(d.DataType)
	b[4] = byte(d.CommandClass)
	b[5] = d.Type
	b[6] = byte(d.Unit)
	b[7] = byte(d.Prefix)
	binary.BigEndian.PutUint32(b[8:12], truncate32(d.MinValue))
	binary.BigEndian.PutUint32(b[12:16], truncate32(d.MaxValue))
	binary.BigEndian.PutUint32(b[16:20], truncate32(d.DefaultValue))
	copy(b[20:], desc)
	return b
}

// EncodeParameterDescriptionRequest builds a PARAMETER_DESCRIPTION GET
// request's parameter data (report §2.2: bare 2-byte pid, no range
// restriction encoded on the wire).
func EncodeParameterDescriptionRequest(pid ParameterID) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(pid))
	return b
}
