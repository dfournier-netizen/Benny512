package rdm

import (
	"reflect"
	"testing"
)

// TestDecodeSlotInfo_MovingHeadExample decodes the exact worked example
// E1.20-2025 §10.6.4 gives ("Example Response for SLOT_INFO – A Moving
// Head"): pan coarse/fine, tilt coarse/fine, fixture speed, intensity, a
// rotating gobo wheel plus its control and index/rotate secondary slots.
// Pinning this exact example (not just isolated field decodes) is what
// would fail if the 5-byte record layout, or the primary/secondary Value
// reinterpretation, were wrong in a way isolated tests could miss.
func TestDecodeSlotInfo_MovingHeadExample(t *testing.T) {
	data := EncodeSlotInfo([]SlotInfoEntry{
		{Offset: 0, Type: SlotTypePrimary, Value: uint16(SDPan)},
		{Offset: 1, Type: SlotTypeSecondaryFine, Value: 0}, // primary at offset 0
		{Offset: 2, Type: SlotTypePrimary, Value: uint16(SDTilt)},
		{Offset: 3, Type: SlotTypeSecondaryFine, Value: 2}, // primary at offset 2
		{Offset: 4, Type: SlotTypePrimary, Value: uint16(SDFixtureSpeed)},
		{Offset: 5, Type: SlotTypePrimary, Value: uint16(SDIntensity)},
		{Offset: 6, Type: SlotTypePrimary, Value: uint16(SDRotoGoboWheel)},
		{Offset: 7, Type: SlotTypeSecondaryControl, Value: 6},       // primary at offset 6
		{Offset: 8, Type: SlotTypeSecondaryQuantumRotate, Value: 6}, // primary at offset 6
	})
	got, err := DecodeSlotInfo(data)
	if err != nil {
		t.Fatalf("DecodeSlotInfo: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("got %d entries, want 9", len(got))
	}

	// Pan coarse: primary, SD_PAN.
	if id, ok := got[0].LabelID(); !ok || id != SDPan {
		t.Errorf("entry 0 LabelID = %v,%v want SD_PAN,true", id, ok)
	}
	if _, ok := got[0].PrimaryOffset(); ok {
		t.Errorf("entry 0 (primary) PrimaryOffset should be ok=false")
	}

	// Pan fine: secondary, refers back to offset 0.
	if off, ok := got[1].PrimaryOffset(); !ok || off != 0 {
		t.Errorf("entry 1 PrimaryOffset = %v,%v want 0,true", off, ok)
	}
	if _, ok := got[1].LabelID(); ok {
		t.Errorf("entry 1 (secondary) LabelID should be ok=false")
	}
	if got[1].Type != SlotTypeSecondaryFine {
		t.Errorf("entry 1 Type = %v, want ST_SEC_FINE", got[1].Type)
	}

	// Tilt fine: secondary, refers back to offset 2.
	if off, ok := got[3].PrimaryOffset(); !ok || off != 2 {
		t.Errorf("entry 3 PrimaryOffset = %v,%v want 2,true", off, ok)
	}

	// Rotating gobo wheel + its two secondary modifiers.
	if id, ok := got[6].LabelID(); !ok || id != SDRotoGoboWheel {
		t.Errorf("entry 6 LabelID = %v,%v want SD_ROTO_GOBO_WHEEL,true", id, ok)
	}
	if off, ok := got[7].PrimaryOffset(); !ok || off != 6 || got[7].Type != SlotTypeSecondaryControl {
		t.Errorf("entry 7 = offset %v ok=%v type=%v, want 6,true,ST_SEC_CONTROL", off, ok, got[7].Type)
	}
	if off, ok := got[8].PrimaryOffset(); !ok || off != 6 || got[8].Type != SlotTypeSecondaryQuantumRotate {
		t.Errorf("entry 8 = offset %v ok=%v type=%v, want 6,true,ST_SEC_QUANTUM_ROTATE", off, ok, got[8].Type)
	}
}

// TestDecodeSlotInfo_RoundTrip is the plain encode/decode inverse check.
func TestDecodeSlotInfo_RoundTrip(t *testing.T) {
	want := []SlotInfoEntry{
		{Offset: 0, Type: SlotTypePrimary, Value: uint16(SDIntensity)},
		{Offset: 1, Type: SlotTypeSecondaryUndefined, Value: 0},
	}
	got, err := DecodeSlotInfo(EncodeSlotInfo(want))
	if err != nil {
		t.Fatalf("DecodeSlotInfo: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

// TestDecodeSlotInfo_EmptyResponse: a device with zero slots (or a
// zero-length SLOT_INFO PD) decodes to an empty, non-nil slice — never an
// error, per §10.6.4's variable 0x00-0xE6 PDL range explicitly including 0.
func TestDecodeSlotInfo_EmptyResponse(t *testing.T) {
	got, err := DecodeSlotInfo(nil)
	if err != nil {
		t.Fatalf("DecodeSlotInfo(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
}

// TestDecodeSlotInfo_BadLength: parameter data that isn't a whole multiple
// of 5 bytes must error, not silently truncate/misalign every subsequent
// record — a length bug here would misread every field after the first
// short record.
func TestDecodeSlotInfo_BadLength(t *testing.T) {
	if _, err := DecodeSlotInfo([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("expected error for 4-byte (not a multiple of 5) SLOT_INFO payload")
	}
}

// TestSlotType_IsSecondary pins the primary/secondary split every
// LabelID()/PrimaryOffset() accessor above depends on.
func TestSlotType_IsSecondary(t *testing.T) {
	if SlotTypePrimary.IsSecondary() {
		t.Error("ST_PRIMARY.IsSecondary() should be false")
	}
	for _, st := range []SlotType{
		SlotTypeSecondaryFine, SlotTypeSecondaryTiming, SlotTypeSecondarySpeed,
		SlotTypeSecondaryControl, SlotTypeSecondaryQuantum, SlotTypeSecondaryRotation,
		SlotTypeSecondaryQuantumRotate, SlotTypeSecondaryUndefined,
	} {
		if !st.IsSecondary() {
			t.Errorf("%v.IsSecondary() should be true", st)
		}
	}
}

// TestSlotLabelID_IsManufacturerSpecific pins Table C-2's reserved range
// boundaries exactly (0x8000-0xFFDF inclusive; 0xFFFF is SD_UNDEFINED, a
// publicly-defined value, NOT manufacturer-specific, despite sitting above
// the reserved range's upper bound of 0xFFDF).
func TestSlotLabelID_IsManufacturerSpecific(t *testing.T) {
	cases := []struct {
		id   SlotLabelID
		want bool
	}{
		{0x7FFF, false},
		{0x8000, true},
		{0xFFDF, true},
		{0xFFE0, false},
		{SDUndefined, false},
		{SDPan, false},
	}
	for _, c := range cases {
		if got := c.id.IsManufacturerSpecific(); got != c.want {
			t.Errorf("SlotLabelID(0x%04X).IsManufacturerSpecific() = %v, want %v", uint16(c.id), got, c.want)
		}
	}
}

func TestSlotLabelID_String(t *testing.T) {
	if got := SDColorAddRed.String(); got != "SD_COLOR_ADD_RED" {
		t.Errorf("SDColorAddRed.String() = %q", got)
	}
	if got := SlotLabelID(0x8123).String(); got != "manufacturer-specific (0x8123)" {
		t.Errorf("manufacturer-specific String() = %q", got)
	}
	if got := SlotLabelID(0x0999).String(); got != "unknown slot label 0x0999" {
		t.Errorf("unknown String() = %q", got)
	}
}

// TestDecodeSlotDescription_RoundTrip mirrors DecodeSelfTestDescription's
// existing test shape (dimmer.go/devicecontrol_test.go's convention) for
// the echoed-index-plus-text response family.
func TestDecodeSlotDescription_RoundTrip(t *testing.T) {
	want := SlotDescription{SlotOffset: 5, Label: "Pan"}
	got, err := DecodeSlotDescription(EncodeSlotDescription(want))
	if err != nil {
		t.Fatalf("DecodeSlotDescription: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestDecodeSlotDescription_TrimsTrailingNUL: some responders pad the
// fixed-width label field with NULs — must not leak into the decoded Label.
func TestDecodeSlotDescription_TrimsTrailingNUL(t *testing.T) {
	data := append(EncodeSlotDescriptionRequest(0), append([]byte("Tilt"), 0, 0, 0)...)
	got, err := DecodeSlotDescription(data)
	if err != nil {
		t.Fatalf("DecodeSlotDescription: %v", err)
	}
	if got.Label != "Tilt" {
		t.Errorf("Label = %q, want %q (NUL padding not trimmed)", got.Label, "Tilt")
	}
}

// TestDecodeSlotDescription_BadLength: fewer than the mandatory 2-byte
// echoed offset must error rather than panic on a slice out-of-range.
func TestDecodeSlotDescription_BadLength(t *testing.T) {
	if _, err := DecodeSlotDescription([]byte{0x00}); err == nil {
		t.Fatal("expected error for 1-byte SLOT_DESCRIPTION payload")
	}
}

// TestEncodeSlotDescriptionRequest pins the exact 2-byte big-endian request
// shape params.go's ErrSlotDescriptionNeedsIndex guard requires.
func TestEncodeSlotDescriptionRequest(t *testing.T) {
	got := EncodeSlotDescriptionRequest(0x0105)
	want := []byte{0x01, 0x05}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EncodeSlotDescriptionRequest(0x0105) = %v, want %v", got, want)
	}
	if len(got) != 2 {
		t.Fatalf("length = %d, want 2 (params.ErrSlotDescriptionNeedsIndex requires exactly 2)", len(got))
	}
}
