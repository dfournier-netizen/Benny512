package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrBadSupportedParameters is returned when SUPPORTED_PARAMETERS data
// isn't a whole number of 2-byte PIDs.
var ErrBadSupportedParameters = errors.New("rdm: malformed SUPPORTED_PARAMETERS data")

// mandatorySupportedParameters lists the PIDs a compliant responder is not
// required to include in its SUPPORTED_PARAMETERS list, because support is
// assumed regardless (report §2.1). WEAKLY CONFIRMED — reconstructed from
// RDM domain knowledge / OLA responder-test conventions; this session's
// attempts to re-fetch the exact ANSI E1.20 clause text returned empty
// results. Callers MUST NOT treat a PID's absence from a device's
// SUPPORTED_PARAMETERS list as "unsupported" for anything in this set —
// always probe them directly. See MandatorySupportedParameters / IsMandatory.
var mandatorySupportedParameters = []ParameterID{
	PIDDiscUniqueBranch,
	PIDDiscMute,
	PIDDiscUnMute,
	PIDSupportedParameters,
	PIDParameterDescription, // mandatory only if the device implements any manufacturer-specific PID
	PIDDeviceInfo,
	PIDSoftwareVersionLabel,
	PIDDMXStartAddress, // mandatory only if DMX footprint > 0
	PIDIdentifyDevice,
}

// MandatorySupportedParameters returns the PIDs report §2.1 flags as
// excluded from a compliant SUPPORTED_PARAMETERS list (support assumed).
func MandatorySupportedParameters() []ParameterID {
	return append([]ParameterID(nil), mandatorySupportedParameters...)
}

// IsMandatorySupportedParameter reports whether pid is in the "assumed
// supported, may be legally absent from the list" set.
func IsMandatorySupportedParameter(pid ParameterID) bool {
	for _, p := range mandatorySupportedParameters {
		if p == pid {
			return true
		}
	}
	return false
}

// ManufacturerSpecificPIDMin/Max bound RDM's manufacturer-specific PID
// space (ANSI E1.20, treated as given per report scope note).
const (
	ManufacturerSpecificPIDMin ParameterID = 0x8000
	ManufacturerSpecificPIDMax ParameterID = 0xFFDF
)

// IsManufacturerSpecific reports whether pid falls in the manufacturer PID
// range 0x8000-0xFFDF.
func (p ParameterID) IsManufacturerSpecific() bool {
	return p >= ManufacturerSpecificPIDMin && p <= ManufacturerSpecificPIDMax
}

// DecodeSupportedParameters parses SUPPORTED_PARAMETERS' GET response: a
// flat repeated-group list of PIDs (report §2.1). ACK_OVERFLOW
// reassembly is the controller's job (session.RDMController concatenates
// blocks in receive order before this ever sees the bytes) — this codec has
// no continuation state of its own.
func DecodeSupportedParameters(data []byte) ([]ParameterID, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("%w: odd length %d", ErrBadSupportedParameters, len(data))
	}
	n := len(data) / 2
	out := make([]ParameterID, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ParameterID(binary.BigEndian.Uint16(data[i*2:i*2+2])))
	}
	return out, nil
}

// EncodeSupportedParameters is the inverse of DecodeSupportedParameters.
func EncodeSupportedParameters(pids []ParameterID) []byte {
	b := make([]byte, len(pids)*2)
	for i, p := range pids {
		binary.BigEndian.PutUint16(b[i*2:i*2+2], uint16(p))
	}
	return b
}
