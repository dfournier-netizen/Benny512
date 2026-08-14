package rdm

import (
	"bytes"
	"errors"
	"testing"
)

// Golden fixtures transcribed verbatim from
// rdm-pids-sensors-research_2026-08-13_2347.md §8 (checksum arithmetic
// independently re-verified by Decode, which validates the checksum).

// --- §8.1: PARAMETER_DESCRIPTION response, manufacturer PID 0x8010 ---

func TestGoldenParameterDescriptionDecode(t *testing.T) {
	b := hexBytes("CC 01 37 7A 70 00 00 00 01 53 70 00 00 00 01 01 00 00 00 00 21 00 51 1F " +
		"80 10 01 03 03 00 00 00 00 00 00 00 00 00 00 10 00 00 00 10 50 49 58 45 " +
		"4C 20 43 4F 55 4E 54 07 27")
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ParameterID != PIDParameterDescription {
		t.Fatalf("pid=%v", msg.ParameterID)
	}
	pd, err := DecodeParameterDescription(msg.ParameterData)
	if err != nil {
		t.Fatal(err)
	}
	want := ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: DSUnsignedByte, CommandClass: PDCommandClassGetSet,
		Type: 0, Unit: UnitNone, Prefix: PrefixNone,
		MinValue: 0, MaxValue: 16, DefaultValue: 16, Description: "PIXEL COUNT",
	}
	if pd != want {
		t.Fatalf("got %+v want %+v", pd, want)
	}
}

func TestGoldenParameterDescriptionEncodeByteExact(t *testing.T) {
	want := hexBytes("80 10 01 03 03 00 00 00 00 00 00 00 00 00 00 10 00 00 00 10 50 49 58 45 4C 20 43 4F 55 4E 54")
	got := EncodeParameterDescription(ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: DSUnsignedByte, CommandClass: PDCommandClassGetSet,
		Unit: UnitNone, Prefix: PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16,
		Description: "PIXEL COUNT",
	})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestParameterDescriptionSignedRoundTrip(t *testing.T) {
	// DS_SIGNED_WORD with a negative min: -100..100, default 0. Exercises
	// the sign-interpretation path report §1.4/§2.2 flags for hardware
	// verification.
	pd := ParameterDescription{
		PID: 0x8020, PDLSize: 2, DataType: DSSignedWord, CommandClass: PDCommandClassGetSet,
		Unit: UnitCentigrade, Prefix: PrefixNone, MinValue: -100, MaxValue: 100, DefaultValue: 0,
		Description: "OFFSET",
	}
	enc := EncodeParameterDescription(pd)
	dec, err := DecodeParameterDescription(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != pd {
		t.Fatalf("got %+v want %+v", dec, pd)
	}
	if dec.MinValue != -100 {
		t.Fatalf("min=%d", dec.MinValue)
	}
}

func TestParameterDescriptionRequestEncode(t *testing.T) {
	got := EncodeParameterDescriptionRequest(0x8010)
	want := []byte{0x80, 0x10}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestParameterDescriptionMalformed(t *testing.T) {
	_, err := DecodeParameterDescription(make([]byte, 19))
	if !errors.Is(err, ErrBadParameterDescription) {
		t.Fatalf("err=%v", err)
	}
}

// --- §8.2: SENSOR_DEFINITION response, sensor 0 "PSU TEMP" ---

func TestGoldenSensorDefinitionDecode(t *testing.T) {
	b := hexBytes("CC 01 2D 7A 70 00 00 00 01 7A 70 12 34 56 78 02 00 00 00 00 21 02 00 15 " +
		"00 00 01 00 FF EC 00 64 00 00 00 3C 03 50 53 55 20 54 45 4D 50 08 FA")
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ParameterID != PIDSensorDefinition {
		t.Fatalf("pid=%v", msg.ParameterID)
	}
	sd, err := DecodeSensorDefinition(msg.ParameterData)
	if err != nil {
		t.Fatal(err)
	}
	want := SensorDefinition{
		SensorNumber: 0, Type: SensorTemperature, Unit: UnitCentigrade, Prefix: PrefixNone,
		RangeMin: -20, RangeMax: 100, NormalMin: 0, NormalMax: 60,
		SupportsRecording: 0x03, Description: "PSU TEMP",
	}
	if sd != want {
		t.Fatalf("got %+v want %+v", sd, want)
	}
	if !sd.RecordsValue() || !sd.RecordsRange() {
		t.Fatalf("expected both recording bits set: %+v", sd)
	}
	if !sd.HasRange() || !sd.HasNormalBand() {
		t.Fatalf("expected declared range/band: %+v", sd)
	}
}

func TestGoldenSensorDefinitionEncodeByteExact(t *testing.T) {
	want := hexBytes("00 00 01 00 FF EC 00 64 00 00 00 3C 03 50 53 55 20 54 45 4D 50")
	got := EncodeSensorDefinition(SensorDefinition{
		SensorNumber: 0, Type: SensorTemperature, Unit: UnitCentigrade, Prefix: PrefixNone,
		RangeMin: -20, RangeMax: 100, NormalMin: 0, NormalMax: 60,
		SupportsRecording: 0x03, Description: "PSU TEMP",
	})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestSensorDefinitionUndefinedSentinels(t *testing.T) {
	sd := SensorDefinition{
		RangeMin: SensorRangeUndefinedMin, RangeMax: SensorRangeUndefinedMax,
		NormalMin: SensorRangeUndefinedMin, NormalMax: SensorRangeUndefinedMax,
	}
	if sd.HasRange() || sd.HasNormalBand() {
		t.Fatalf("expected undefined range/band: %+v", sd)
	}
}

func TestSensorDefinitionMalformed(t *testing.T) {
	_, err := DecodeSensorDefinition(make([]byte, 12))
	if !errors.Is(err, ErrBadSensorDefinition) {
		t.Fatalf("err=%v", err)
	}
}

// --- §8.3: SENSOR_VALUE response ---

func TestGoldenSensorValueDecode(t *testing.T) {
	b := hexBytes("CC 01 21 7A 70 00 00 00 01 7A 70 12 34 56 78 03 00 00 00 00 21 02 01 09 " +
		"00 00 17 00 12 00 2D 00 17 04 74")
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ParameterID != PIDSensorValue {
		t.Fatalf("pid=%v", msg.ParameterID)
	}
	sv, err := DecodeSensorValue(msg.ParameterData)
	if err != nil {
		t.Fatal(err)
	}
	want := SensorValue{SensorNumber: 0, Present: 23, Lowest: 18, Highest: 45, Recorded: 23}
	if sv != want {
		t.Fatalf("got %+v want %+v", sv, want)
	}
}

func TestGoldenSensorValueEncodeByteExact(t *testing.T) {
	want := hexBytes("00 00 17 00 12 00 2D 00 17")
	got := EncodeSensorValue(SensorValue{SensorNumber: 0, Present: 23, Lowest: 18, Highest: 45, Recorded: 23})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestSensorValueNegativeRoundTrip(t *testing.T) {
	v := SensorValue{SensorNumber: 5, Present: -15, Lowest: -40, Highest: 10, Recorded: -15}
	enc := EncodeSensorValue(v)
	dec, err := DecodeSensorValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != v {
		t.Fatalf("got %+v want %+v", dec, v)
	}
}

func TestSensorValueMalformed(t *testing.T) {
	_, err := DecodeSensorValue(make([]byte, 8))
	if !errors.Is(err, ErrBadSensorValue) {
		t.Fatalf("err=%v", err)
	}
	_, err = DecodeSensorValue(make([]byte, 10))
	if !errors.Is(err, ErrBadSensorValue) {
		t.Fatalf("err=%v", err)
	}
}

func TestEncodeSensorNumberRequest(t *testing.T) {
	got := EncodeSensorNumberRequest(AllSensors)
	if !bytes.Equal(got, []byte{0xFF}) {
		t.Fatalf("got %X", got)
	}
}

// --- §8.4: SUPPORTED_PARAMETERS response ---

func TestGoldenSupportedParametersDecode(t *testing.T) {
	b := hexBytes("CC 01 22 7A 70 00 00 00 01 53 70 00 00 00 01 04 00 00 00 00 21 00 50 0A " +
		"00 82 00 E0 02 00 02 01 80 10 05 14")
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ParameterID != PIDSupportedParameters {
		t.Fatalf("pid=%v", msg.ParameterID)
	}
	pids, err := DecodeSupportedParameters(msg.ParameterData)
	if err != nil {
		t.Fatal(err)
	}
	want := []ParameterID{PIDDeviceLabel, PIDDMXPersonality, PIDSensorDefinition, PIDSensorValue, 0x8010}
	if len(pids) != len(want) {
		t.Fatalf("got %v want %v", pids, want)
	}
	for i := range want {
		if pids[i] != want[i] {
			t.Fatalf("got %v want %v", pids, want)
		}
	}
}

func TestGoldenSupportedParametersEncodeByteExact(t *testing.T) {
	want := hexBytes("00 82 00 E0 02 00 02 01 80 10")
	got := EncodeSupportedParameters([]ParameterID{PIDDeviceLabel, PIDDMXPersonality, PIDSensorDefinition, PIDSensorValue, 0x8010})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestSupportedParametersMalformed(t *testing.T) {
	_, err := DecodeSupportedParameters([]byte{0x00})
	if !errors.Is(err, ErrBadSupportedParameters) {
		t.Fatalf("err=%v", err)
	}
}

func TestIsMandatorySupportedParameter(t *testing.T) {
	if !IsMandatorySupportedParameter(PIDDeviceInfo) {
		t.Fatal("DEVICE_INFO should be mandatory")
	}
	if IsMandatorySupportedParameter(PIDDeviceLabel) {
		t.Fatal("DEVICE_LABEL should not be mandatory")
	}
}

func TestParameterIDIsManufacturerSpecific(t *testing.T) {
	if !ParameterID(0x8010).IsManufacturerSpecific() {
		t.Fatal("0x8010 should be manufacturer-specific")
	}
	if ParameterID(0x0060).IsManufacturerSpecific() {
		t.Fatal("DEVICE_INFO should not be manufacturer-specific")
	}
	if ParameterID(0xFFE0).IsManufacturerSpecific() {
		t.Fatal("0xFFE0 is outside the manufacturer range (broadcast/reserved)")
	}
}

// --- §8.5: STATUS_MESSAGES response ---

func TestGoldenStatusMessagesDecode(t *testing.T) {
	b := hexBytes("CC 01 21 7A 70 00 00 00 01 7A 70 12 34 56 78 05 00 00 00 00 21 00 30 09 " +
		"00 00 03 00 21 00 55 00 00 04 AF")
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ParameterID != PIDStatusMessages {
		t.Fatalf("pid=%v", msg.ParameterID)
	}
	got, err := DecodeStatusMessages(msg.ParameterData)
	if err != nil {
		t.Fatal(err)
	}
	want := []StatusMessage{{SubDevice: 0, Type: StatusWarning, MessageID: 0x0021, Value1: 85, Value2: 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestGoldenStatusMessagesEncodeByteExact(t *testing.T) {
	want := hexBytes("00 00 03 00 21 00 55 00 00")
	got := EncodeStatusMessages([]StatusMessage{{SubDevice: 0, Type: StatusWarning, MessageID: 0x0021, Value1: 85, Value2: 0}})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %X want %X", got, want)
	}
}

func TestStatusMessagesMalformed(t *testing.T) {
	_, err := DecodeStatusMessages(make([]byte, 8))
	if !errors.Is(err, ErrBadStatusMessages) {
		t.Fatalf("err=%v", err)
	}
}

func TestStatusMessagesRequestEncode(t *testing.T) {
	if got := EncodeStatusMessagesRequest(StatusWarning); !bytes.Equal(got, []byte{0x03}) {
		t.Fatalf("got %X", got)
	}
	if got := EncodeQueuedMessageRequest(StatusAdvisory); !bytes.Equal(got, []byte{0x02}) {
		t.Fatalf("got %X", got)
	}
}

func TestStatusIDDescriptionRoundTrip(t *testing.T) {
	req := EncodeStatusIDDescriptionRequest(0x0021)
	if !bytes.Equal(req, []byte{0x00, 0x21}) {
		t.Fatalf("req=%X", req)
	}
	if got := DecodeStatusIDDescriptionResponse([]byte("Overtemperature")); got != "Overtemperature" {
		t.Fatalf("got %q", got)
	}
}

// --- PRODUCT_DETAIL_ID_LIST ---

func TestProductDetailIDListRoundTrip(t *testing.T) {
	ids := []ProductDetail{DetailEthernetNode, DetailSplitter}
	enc := EncodeProductDetailIDList(ids)
	dec, err := DecodeProductDetailIDList(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec) != 2 || dec[0] != DetailEthernetNode || dec[1] != DetailSplitter {
		t.Fatalf("got %v", dec)
	}
}

func TestProductDetailIDListMalformed(t *testing.T) {
	_, err := DecodeProductDetailIDList([]byte{0x00})
	if !errors.Is(err, ErrBadProductDetailIDList) {
		t.Fatalf("err=%v", err)
	}
	// 7 entries exceeds max_size=6.
	_, err = DecodeProductDetailIDList(make([]byte, 14))
	if !errors.Is(err, ErrBadProductDetailIDList) {
		t.Fatalf("err=%v", err)
	}
}

func TestProductDetailIsInfrastructure(t *testing.T) {
	if !DetailEthernetNode.IsInfrastructure() || !DetailSplitter.IsInfrastructure() || !DetailWirelessLink.IsInfrastructure() {
		t.Fatal("expected infrastructure detail IDs to report true")
	}
	if DetailBattery.IsInfrastructure() {
		t.Fatal("battery is not an infrastructure signal")
	}
}

// --- Value formatting helper ---

func TestFormatValueDeciVolts(t *testing.T) {
	if got := FormatValue(235, UnitVoltsDC, PrefixDeci); got != "23.5 V" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatValueNoUnit(t *testing.T) {
	if got := FormatValue(42, UnitNone, PrefixNone); got != "42" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatValueKiloHertz(t *testing.T) {
	if got := FormatValue(24, UnitHertz, PrefixKilo); got != "24000 Hz" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatSensorRangeValueUndefined(t *testing.T) {
	if got := FormatSensorRangeValue(SensorRangeUndefinedMin, UnitCentigrade, PrefixNone); got != "undefined" {
		t.Fatalf("got %q", got)
	}
	if got := FormatSensorRangeValue(60, UnitCentigrade, PrefixNone); got != "60 °C" {
		t.Fatalf("got %q", got)
	}
}

// --- DataType.IsSigned / sign extension coverage ---

func TestSignExtend32(t *testing.T) {
	if got := signExtend32(0xFFFFFF9C, DSSignedByte); got != -100 { // -100 as int32
		t.Fatalf("got %d", got)
	}
	if got := signExtend32(0x0000009C, DSUnsignedByte); got != 156 {
		t.Fatalf("got %d", got)
	}
}
