package rdm

import "testing"

func TestCurveRoundTrip(t *testing.T) {
	data := EncodeIndexedChoiceSet(3) // SET request shape
	if len(data) != 1 || data[0] != 3 {
		t.Fatalf("EncodeIndexedChoiceSet: got %v", data)
	}
	got, err := DecodeCurve([]byte{3, 5})
	if err != nil {
		t.Fatalf("DecodeCurve: %v", err)
	}
	want := IndexedChoice{Current: 3, Count: 5}
	if got != want {
		t.Fatalf("DecodeCurve = %+v, want %+v", got, want)
	}
}

func TestDecodeCurveBadLength(t *testing.T) {
	if _, err := DecodeCurve([]byte{1}); err == nil {
		t.Fatal("expected error for short CURVE payload")
	}
}

func TestOutputResponseTimeRoundTrip(t *testing.T) {
	got, err := DecodeOutputResponseTime([]byte{2, 4})
	if err != nil {
		t.Fatalf("DecodeOutputResponseTime: %v", err)
	}
	if got != (IndexedChoice{Current: 2, Count: 4}) {
		t.Fatalf("got %+v", got)
	}
}

func TestModulationFrequencyRoundTrip(t *testing.T) {
	got, err := DecodeModulationFrequency([]byte{1, 6})
	if err != nil {
		t.Fatalf("DecodeModulationFrequency: %v", err)
	}
	if got != (IndexedChoice{Current: 1, Count: 6}) {
		t.Fatalf("got %+v", got)
	}
}

func TestCurveDescriptionRoundTrip(t *testing.T) {
	req := EncodeIndexedDescriptionRequest(3)
	if len(req) != 1 || req[0] != 3 {
		t.Fatalf("EncodeIndexedDescriptionRequest: got %v", req)
	}
	data := append([]byte{3}, []byte("S-Curve")...)
	got, err := DecodeCurveDescription(data)
	if err != nil {
		t.Fatalf("DecodeCurveDescription: %v", err)
	}
	want := IndexedDescription{Index: 3, Description: "S-Curve"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestOutputResponseTimeDescriptionRoundTrip(t *testing.T) {
	data := append([]byte{1}, []byte("Fast")...)
	got, err := DecodeOutputResponseTimeDescription(data)
	if err != nil {
		t.Fatalf("DecodeOutputResponseTimeDescription: %v", err)
	}
	if got != (IndexedDescription{Index: 1, Description: "Fast"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestModulationFrequencyDescriptionRoundTrip(t *testing.T) {
	data := append([]byte{2}, []byte("1500Hz")...)
	got, err := DecodeModulationFrequencyDescription(data)
	if err != nil {
		t.Fatalf("DecodeModulationFrequencyDescription: %v", err)
	}
	if got != (IndexedDescription{Index: 2, Description: "1500Hz"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeIndexedDescriptionBadLength(t *testing.T) {
	if _, err := DecodeCurveDescription(nil); err == nil {
		t.Fatal("expected error for empty CURVE_DESCRIPTION payload")
	}
}

func TestMinimumLevelRoundTrip(t *testing.T) {
	v := MinimumLevel{Increasing: 10, Decreasing: 5, OnBelowMin: 1}
	data := EncodeMinimumLevel(v)
	if len(data) != 5 {
		t.Fatalf("EncodeMinimumLevel: want 5 bytes, got %d", len(data))
	}
	got, err := DecodeMinimumLevel(data)
	if err != nil {
		t.Fatalf("DecodeMinimumLevel: %v", err)
	}
	if got != v {
		t.Fatalf("got %+v, want %+v", got, v)
	}
}

func TestDecodeMinimumLevelBadLength(t *testing.T) {
	if _, err := DecodeMinimumLevel([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for short MINIMUM_LEVEL payload")
	}
}

func TestMaximumLevelRoundTrip(t *testing.T) {
	data := EncodeMaximumLevel(65535)
	got, err := DecodeMaximumLevel(data)
	if err != nil {
		t.Fatalf("DecodeMaximumLevel: %v", err)
	}
	if got != 65535 {
		t.Fatalf("got %d", got)
	}
}

func TestDecodeMaximumLevelBadLength(t *testing.T) {
	if _, err := DecodeMaximumLevel([]byte{1}); err == nil {
		t.Fatal("expected error for short MAXIMUM_LEVEL payload")
	}
}

func TestIdentifyModeRoundTrip(t *testing.T) {
	data := EncodeIdentifyMode(IdentifyModeLoud)
	got, err := DecodeIdentifyMode(data)
	if err != nil {
		t.Fatalf("DecodeIdentifyMode: %v", err)
	}
	if got != IdentifyModeLoud {
		t.Fatalf("got %v", got)
	}
	if got.String() != "Loud" {
		t.Fatalf("String() = %q", got.String())
	}
	if IdentifyModeQuiet.String() != "Quiet" {
		t.Fatalf("Quiet.String() = %q", IdentifyModeQuiet.String())
	}
}

func TestDecodeIdentifyModeBadLength(t *testing.T) {
	if _, err := DecodeIdentifyMode(nil); err == nil {
		t.Fatal("expected error for empty IDENTIFY_MODE payload")
	}
}
