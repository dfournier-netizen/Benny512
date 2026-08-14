package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Sensor range/normal-band undefined sentinels (report §3.1, CONFIRMED via
// OLA RDMEnums.h SENSOR_DEFINITION_RANGE_MIN_UNDEFINED/MAX_UNDEFINED).
const (
	SensorRangeUndefinedMin int16 = -32768 // 0x8000
	SensorRangeUndefinedMax int16 = 32767  // 0x7FFF
)

// supports_recording bitfield values (report §3.1, CONFIRMED via OLA
// RDMEnums.h SENSOR_RECORDED_VALUE / SENSOR_RECORDED_RANGE_VALUES).
const (
	SensorSupportsRecordedValue byte = 0x01
	SensorSupportsRecordedRange byte = 0x02
)

// AllSensors is the sensor_number wildcard (0xFF), valid for SENSOR_VALUE
// SET and RECORD_SENSORS SET only — NOT for SENSOR_DEFINITION/SENSOR_VALUE
// GET, whose sensor_number range is 0-254 (report §3.1/§3.2).
const AllSensors byte = 0xFF

var (
	// ErrBadSensorDefinition is returned when SENSOR_DEFINITION data is
	// shorter than its 13-byte fixed portion.
	ErrBadSensorDefinition = errors.New("rdm: malformed SENSOR_DEFINITION data")
	// ErrBadSensorValue is returned when SENSOR_VALUE data isn't exactly 9
	// bytes.
	ErrBadSensorValue = errors.New("rdm: malformed SENSOR_VALUE data")
)

// SensorDefinition is SENSOR_DEFINITION's (0x0200) GET response (report
// §3.1). Fixed portion = 13 bytes + description (<=32 ASCII, ):
//
//	0     sensor_number       UINT8
//	1     type                UINT8 (SensorType)
//	2     unit                UINT8 (Unit)
//	3     prefix              UINT8 (Prefix)
//	4-5   range_min           INT16 BE
//	6-7   range_max           INT16 BE
//	8-9   normal_min          INT16 BE
//	10-11 normal_max          INT16 BE
//	12    supports_recording  UINT8 bitfield
//	13-.. description         ASCII
type SensorDefinition struct {
	SensorNumber      byte
	Type              SensorType
	Unit              Unit
	Prefix            Prefix
	RangeMin          int16
	RangeMax          int16
	NormalMin         int16
	NormalMax         int16
	SupportsRecording byte
	Description       string
}

// RecordsValue reports whether SENSOR_RECORDED_VALUE (lowest/highest) is
// supported — report §1.2: hide the recorded-value UI when clear rather
// than showing a perpetual "unsupported" state.
func (d SensorDefinition) RecordsValue() bool {
	return d.SupportsRecording&SensorSupportsRecordedValue != 0
}

// RecordsRange reports whether SENSOR_RECORDED_RANGE_VALUES (the
// "recorded" snapshot field) is supported.
func (d SensorDefinition) RecordsRange() bool {
	return d.SupportsRecording&SensorSupportsRecordedRange != 0
}

// HasRange reports whether range_min/range_max are declared (not the
// undefined sentinel pair) — report §1.2's gauge-track-bounds guidance.
func (d SensorDefinition) HasRange() bool {
	return !isUndefinedSensorValue(d.RangeMin) && !isUndefinedSensorValue(d.RangeMax)
}

// HasNormalBand reports whether normal_min/normal_max are declared.
func (d SensorDefinition) HasNormalBand() bool {
	return !isUndefinedSensorValue(d.NormalMin) && !isUndefinedSensorValue(d.NormalMax)
}

func isUndefinedSensorValue(v int16) bool {
	return v == SensorRangeUndefinedMin || v == SensorRangeUndefinedMax
}

// DecodeSensorDefinition parses a SENSOR_DEFINITION GET response.
func DecodeSensorDefinition(data []byte) (SensorDefinition, error) {
	if len(data) < 13 {
		return SensorDefinition{}, fmt.Errorf("%w: want >=13 bytes, got %d", ErrBadSensorDefinition, len(data))
	}
	return SensorDefinition{
		SensorNumber:      data[0],
		Type:              SensorType(data[1]),
		Unit:              Unit(data[2]),
		Prefix:            Prefix(data[3]),
		RangeMin:          int16(binary.BigEndian.Uint16(data[4:6])),
		RangeMax:          int16(binary.BigEndian.Uint16(data[6:8])),
		NormalMin:         int16(binary.BigEndian.Uint16(data[8:10])),
		NormalMax:         int16(binary.BigEndian.Uint16(data[10:12])),
		SupportsRecording: data[12],
		Description:       string(data[13:]),
	}, nil
}

// EncodeSensorDefinition is the inverse of DecodeSensorDefinition.
func EncodeSensorDefinition(d SensorDefinition) []byte {
	desc := d.Description
	if len(desc) > 32 {
		desc = desc[:32]
	}
	b := make([]byte, 13+len(desc))
	b[0] = d.SensorNumber
	b[1] = byte(d.Type)
	b[2] = byte(d.Unit)
	b[3] = byte(d.Prefix)
	binary.BigEndian.PutUint16(b[4:6], uint16(d.RangeMin))
	binary.BigEndian.PutUint16(b[6:8], uint16(d.RangeMax))
	binary.BigEndian.PutUint16(b[8:10], uint16(d.NormalMin))
	binary.BigEndian.PutUint16(b[10:12], uint16(d.NormalMax))
	b[12] = d.SupportsRecording
	copy(b[13:], desc)
	return b
}

// EncodeSensorNumberRequest builds the 1-byte request parameter data shared
// by SENSOR_DEFINITION GET, SENSOR_VALUE GET, SENSOR_VALUE SET (0xFF valid)
// and RECORD_SENSORS SET (0xFF valid).
func EncodeSensorNumberRequest(sensorNumber byte) []byte {
	return []byte{sensorNumber}
}

// SensorValue is SENSOR_VALUE's (0x0201) GET/SET response layout (report
// §3.2) — 9 bytes: sensor_number, present_value, lowest, highest, recorded,
// all INT16 BE except sensor_number. Lowest/highest are only meaningful if
// the definition's SensorSupportsRecordedValue bit is set; recorded only if
// SensorSupportsRecordedRange is set.
//
// SET response note (report §3.2): OLA's pids.proto types the SET
// response's four value fields as UINT16 vs. INT16 in the GET response —
// WEAKLY CONFIRMED as a schema-authoring inconsistency rather than a real
// wire difference. This type is used for both directions and always
// interprets the four fields as INT16.
type SensorValue struct {
	SensorNumber byte
	Present      int16
	Lowest       int16
	Highest      int16
	Recorded     int16
}

// DecodeSensorValue parses a 9-byte SENSOR_VALUE response.
func DecodeSensorValue(data []byte) (SensorValue, error) {
	if len(data) != 9 {
		return SensorValue{}, fmt.Errorf("%w: want 9 bytes, got %d", ErrBadSensorValue, len(data))
	}
	return SensorValue{
		SensorNumber: data[0],
		Present:      int16(binary.BigEndian.Uint16(data[1:3])),
		Lowest:       int16(binary.BigEndian.Uint16(data[3:5])),
		Highest:      int16(binary.BigEndian.Uint16(data[5:7])),
		Recorded:     int16(binary.BigEndian.Uint16(data[7:9])),
	}, nil
}

// EncodeSensorValue is the inverse of DecodeSensorValue.
func EncodeSensorValue(v SensorValue) []byte {
	b := make([]byte, 9)
	b[0] = v.SensorNumber
	binary.BigEndian.PutUint16(b[1:3], uint16(v.Present))
	binary.BigEndian.PutUint16(b[3:5], uint16(v.Lowest))
	binary.BigEndian.PutUint16(b[5:7], uint16(v.Highest))
	binary.BigEndian.PutUint16(b[7:9], uint16(v.Recorded))
	return b
}
