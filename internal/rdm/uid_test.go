package rdm

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDescriptionFormat(t *testing.T) {
	uid := UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678}
	if got := uid.String(); got != "7A70:12345678" {
		t.Fatalf("got %q", got)
	}
}

func TestParseFromString(t *testing.T) {
	uid, ok := ParseUID("7A70:12345678")
	if !ok {
		t.Fatal("expected ok")
	}
	if uid.ManufacturerID != 0x7A70 || uid.DeviceID != 0x12345678 {
		t.Fatalf("got %+v", uid)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := []string{"not-a-uid", "7A70:123456", "7A7:12345678", "7A70-12345678", ""}
	for _, c := range cases {
		if _, ok := ParseUID(c); ok {
			t.Fatalf("expected reject for %q", c)
		}
	}
}

func TestBytesRoundTrip(t *testing.T) {
	uid := UID{ManufacturerID: 0xBEEF, DeviceID: 0xCAFE1234}
	b := uid.Bytes()
	want := []byte{0xBE, 0xEF, 0xCA, 0xFE, 0x12, 0x34}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("got %v want %v", b, want)
		}
	}
	decoded, err := UIDFromBytes(b)
	if err != nil || decoded != uid {
		t.Fatalf("got %+v, %v", decoded, err)
	}
}

func TestInvalidByteCountThrows(t *testing.T) {
	_, err := UIDFromBytes([]byte{1, 2, 3})
	if !errors.Is(err, ErrInvalidUIDByteCount) {
		t.Fatalf("expected ErrInvalidUIDByteCount, got %v", err)
	}
}

func TestBroadcastConstants(t *testing.T) {
	if BroadcastAll.String() != "FFFF:FFFFFFFF" || !BroadcastAll.IsBroadcast() {
		t.Fatalf("bad broadcastAll: %+v", BroadcastAll)
	}
	mfrBroadcast := Broadcast(0x7A70)
	if mfrBroadcast.String() != "7A70:FFFFFFFF" || !mfrBroadcast.IsBroadcast() {
		t.Fatalf("bad mfr broadcast: %+v", mfrBroadcast)
	}
	if (UID{ManufacturerID: 0x7A70, DeviceID: 1}).IsBroadcast() {
		t.Fatal("expected not broadcast")
	}
}

func TestComparable(t *testing.T) {
	a := UID{ManufacturerID: 0x0001, DeviceID: 0xFFFFFFFF}
	b := UID{ManufacturerID: 0x0002, DeviceID: 0x00000000}
	if !a.Less(b) {
		t.Fatal("expected a < b")
	}
	c := UID{ManufacturerID: 0x0001, DeviceID: 0x00000001}
	d := UID{ManufacturerID: 0x0001, DeviceID: 0x00000002}
	if !c.Less(d) {
		t.Fatal("expected c < d")
	}
}

func TestHashableAndEquatable(t *testing.T) {
	a := UID{ManufacturerID: 1, DeviceID: 2}
	b := UID{ManufacturerID: 1, DeviceID: 2}
	if a != b {
		t.Fatal("expected equal")
	}
	set := map[UID]struct{}{}
	set[a] = struct{}{}
	set[b] = struct{}{}
	if len(set) != 1 {
		t.Fatalf("expected set size 1, got %d", len(set))
	}
}

func TestJSONRoundTrip(t *testing.T) {
	uid := UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678}
	data, err := json.Marshal(uid)
	if err != nil {
		t.Fatal(err)
	}
	var decoded UID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != uid {
		t.Fatalf("got %+v", decoded)
	}
}
