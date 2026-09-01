package rdm

import (
	"reflect"
	"testing"
)

func TestPowerStateRoundTrip(t *testing.T) {
	for _, p := range []PowerState{PowerStateFullOff, PowerStateShutdown, PowerStateStandby, PowerStateNormal} {
		if !p.IsValid() {
			t.Fatalf("%v.IsValid() = false, want true", p)
		}
		got, err := DecodePowerState(EncodePowerState(p))
		if err != nil {
			t.Fatalf("DecodePowerState: %v", err)
		}
		if got != p {
			t.Fatalf("round-trip = %v, want %v", got, p)
		}
	}
	if PowerState(0x03).IsValid() {
		t.Fatalf("0x03.IsValid() = true, want false (not one of the four Table A-11 states)")
	}
	if _, err := DecodePowerState([]byte{0x00, 0x01}); err == nil {
		t.Fatal("DecodePowerState with 2 bytes: want error, got nil")
	}
}

func TestPowerStateString(t *testing.T) {
	cases := map[PowerState]string{
		PowerStateFullOff: "Full Off", PowerStateShutdown: "Shutdown",
		PowerStateStandby: "Standby", PowerStateNormal: "Normal",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("%02X.String() = %q, want %q", byte(p), got, want)
		}
	}
	if got := PowerState(0x42).String(); got != "Unknown (0x42)" {
		t.Errorf("unknown PowerState.String() = %q, want hex fallback", got)
	}
}

func TestSelfTestActiveDecode(t *testing.T) {
	active, err := DecodeSelfTestActive([]byte{0x01})
	if err != nil || !active {
		t.Fatalf("DecodeSelfTestActive([1]) = %v, %v, want true, nil", active, err)
	}
	active, err = DecodeSelfTestActive([]byte{0x00})
	if err != nil || active {
		t.Fatalf("DecodeSelfTestActive([0]) = %v, %v, want false, nil", active, err)
	}
	if _, err := DecodeSelfTestActive(nil); err == nil {
		t.Fatal("DecodeSelfTestActive(nil): want error, got nil")
	}
}

func TestEncodePerformSelfTest(t *testing.T) {
	if got := EncodePerformSelfTest(SelfTestOff); !reflect.DeepEqual(got, []byte{0x00}) {
		t.Errorf("EncodePerformSelfTest(SelfTestOff) = %v, want [0x00]", got)
	}
	if got := EncodePerformSelfTest(SelfTestAll); !reflect.DeepEqual(got, []byte{0xFF}) {
		t.Errorf("EncodePerformSelfTest(SelfTestAll) = %v, want [0xFF]", got)
	}
	if got := EncodePerformSelfTest(SelfTestNumber(0x03)); !reflect.DeepEqual(got, []byte{0x03}) {
		t.Errorf("EncodePerformSelfTest(3) = %v, want [0x03]", got)
	}
}

func TestSelfTestDescriptionDecode(t *testing.T) {
	// Number byte + ASCII label, per E1.20 §10.11.5 (1-33 bytes: 1 + up to
	// 32 label bytes).
	data := append([]byte{0x02}, []byte("Pan/Tilt Sweep")...)
	got, err := DecodeSelfTestDescription(data)
	if err != nil {
		t.Fatalf("DecodeSelfTestDescription: %v", err)
	}
	want := SelfTestDescription{Number: 0x02, Label: "Pan/Tilt Sweep"}
	if got != want {
		t.Fatalf("DecodeSelfTestDescription = %+v, want %+v", got, want)
	}
	// Trailing NUL padding must not leak into Label.
	padded := append([]byte{0x01}, append([]byte("Lamp"), 0x00, 0x00)...)
	got, err = DecodeSelfTestDescription(padded)
	if err != nil {
		t.Fatalf("DecodeSelfTestDescription (padded): %v", err)
	}
	if got.Label != "Lamp" {
		t.Fatalf("Label = %q, want %q (NUL padding must be trimmed)", got.Label, "Lamp")
	}
	if _, err := DecodeSelfTestDescription(nil); err == nil {
		t.Fatal("DecodeSelfTestDescription(nil): want error, got nil")
	}
}

func TestEncodeSelfTestDescriptionRequest(t *testing.T) {
	if got := EncodeSelfTestDescriptionRequest(0x05); !reflect.DeepEqual(got, []byte{0x05}) {
		t.Errorf("EncodeSelfTestDescriptionRequest(5) = %v, want [0x05]", got)
	}
}

func TestEncodeCapturePresetSceneOnly(t *testing.T) {
	got := EncodeCapturePreset(0x1234, nil)
	want := []byte{0x12, 0x34}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EncodeCapturePreset(scene-only) = %v, want %v", got, want)
	}
}

func TestEncodeCapturePresetWithTiming(t *testing.T) {
	got := EncodeCapturePreset(0x0001, &PresetTiming{UpFadeTime: 0x0002, DownFadeTime: 0x0003, WaitTime: 0x0004})
	want := []byte{0x00, 0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EncodeCapturePreset(with timing) = %v, want %v", got, want)
	}
}

func TestPresetPlaybackModeString(t *testing.T) {
	if got := PresetPlaybackOff.String(); got != "Off" {
		t.Errorf("PresetPlaybackOff.String() = %q, want Off", got)
	}
	if got := PresetPlaybackAll.String(); got != "All" {
		t.Errorf("PresetPlaybackAll.String() = %q, want All", got)
	}
	if got := PresetPlaybackMode(7).String(); got != "Scene 7" {
		t.Errorf("PresetPlaybackMode(7).String() = %q, want Scene 7", got)
	}
}

func TestPresetPlaybackRoundTrip(t *testing.T) {
	// Level 0 is real data ("preset scaled at 0") — not to be conflated with
	// "no level", a wire-encoding analogue of the JSON omitempty-drops-zero
	// bug class this project has been bitten by before.
	cases := []PresetPlayback{
		{Mode: PresetPlaybackOff, Level: 0xFF},
		{Mode: PresetPlaybackAll, Level: 0x00},
		{Mode: PresetPlaybackMode(42), Level: 0x80},
	}
	for _, p := range cases {
		got, err := DecodePresetPlayback(EncodePresetPlayback(p))
		if err != nil {
			t.Fatalf("DecodePresetPlayback: %v", err)
		}
		if got != p {
			t.Fatalf("round-trip = %+v, want %+v", got, p)
		}
	}
	if _, err := DecodePresetPlayback([]byte{0x00, 0x00}); err == nil {
		t.Fatal("DecodePresetPlayback with 2 bytes: want error, got nil")
	}
}

func TestSelfTestStatusString(t *testing.T) {
	cases := map[SelfTestStatus]string{
		SelfTestStatusNoSupport: "Not supported", SelfTestStatusNotRun: "Not run since last power cycle",
		SelfTestStatusAborted: "Aborted", SelfTestStatusActive: "Active",
		SelfTestStatusPass: "Pass", SelfTestStatusFail: "Fail",
		SelfTestStatusNoAnalysis: "Complete (no analysis)", SelfTestStatusResultCode: "Complete (result code available)",
		SelfTestStatusOther: "Other (refer to manufacturer)",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%02X.String() = %q, want %q", byte(s), got, want)
		}
	}
}

// TestDecodeSelfTestEnhanced exercises the exact worked example from the
// ANSI E1.20-2025 PDF's own §10.11.8 text (page ~122-123): a responder that
// declares a manufacturer-specific Result Code Enumeration PID 0x8FFE and
// four packed-list entries (0x01 NOTRUN/cap 0x02, 0x02 PASS/cap 0x00, 0x0A
// ACTIVE/cap 0x01, 0xFF/SELF_TEST_ALL NOSUPPORT/cap 0x07), all with
// ResultCode 0x0000.
func TestDecodeSelfTestEnhanced(t *testing.T) {
	data := []byte{
		0x8F, 0xFE, // Manufacturer-Specific Result Code Enumeration PID
		0x01, 0x01, 0x00, 0x02, 0x00, 0x00, // Self Test 0x01: NOT_RUN, cap 0x0002, rc 0
		0x02, 0x04, 0x00, 0x00, 0x00, 0x00, // Self Test 0x02: PASS, cap 0x0000, rc 0
		0x0A, 0x03, 0x00, 0x01, 0x00, 0x00, // Self Test 0x0A: ACTIVE, cap 0x0001, rc 0
		0xFF, 0x00, 0x00, 0x07, 0x00, 0x00, // SELF_TEST_ALL: NOSUPPORT, cap 0x0007, rc 0
	}
	got, err := DecodeSelfTestEnhanced(data)
	if err != nil {
		t.Fatalf("DecodeSelfTestEnhanced: %v", err)
	}
	if got.ManufacturerResultCodePID != 0x8FFE {
		t.Errorf("ManufacturerResultCodePID = 0x%04X, want 0x8FFE", got.ManufacturerResultCodePID)
	}
	want := []SelfTestEnhancedEntry{
		{Number: 0x01, Status: SelfTestStatusNotRun, Capability: 0x0002, ResultCode: 0},
		{Number: 0x02, Status: SelfTestStatusPass, Capability: 0x0000, ResultCode: 0},
		{Number: 0x0A, Status: SelfTestStatusActive, Capability: 0x0001, ResultCode: 0},
		{Number: SelfTestAll, Status: SelfTestStatusNoSupport, Capability: 0x0007, ResultCode: 0},
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("Entries = %+v, want %+v", got.Entries, want)
	}
	// Table 10-3's Capability bits: Self Test 0x0A declares auto-terminate
	// (bit 0), the others don't.
	if got.Entries[2].Capability&SelfTestCapAutoTerminate == 0 {
		t.Error("Self Test 0x0A should declare SelfTestCapAutoTerminate")
	}
	if got.Entries[0].Capability&SelfTestCapAutoTerminate != 0 {
		t.Error("Self Test 0x01 should NOT declare SelfTestCapAutoTerminate")
	}
}

func TestDecodeSelfTestEnhancedNoEntries(t *testing.T) {
	// Header only (no manufacturer result-code PID, no self tests declared)
	// must decode to an empty, non-nil Entries slice.
	got, err := DecodeSelfTestEnhanced([]byte{0x00, 0x00})
	if err != nil {
		t.Fatalf("DecodeSelfTestEnhanced: %v", err)
	}
	if got.Entries == nil {
		t.Fatal("Entries is nil, want an empty non-nil slice")
	}
	if len(got.Entries) != 0 {
		t.Fatalf("len(Entries) = %d, want 0", len(got.Entries))
	}
}

func TestDecodeSelfTestEnhancedBadLength(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0x00},                         // shorter than the 2-byte header
		{0x00, 0x00, 0x01, 0x02, 0x03}, // remainder not a multiple of 6
	} {
		if _, err := DecodeSelfTestEnhanced(data); err == nil {
			t.Fatalf("DecodeSelfTestEnhanced(%v): want error, got nil", data)
		}
	}
}
