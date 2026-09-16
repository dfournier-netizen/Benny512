package sacn

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestEncodeDataPacketFields(t *testing.T) {
	var cid [16]byte
	for i := range cid {
		cid[i] = byte(i)
	}
	p, err := EncodeDataPacket([]byte{1, 2, 255}, cid, "Benny512", 100, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 638 {
		t.Fatalf("length=%d, want 638", len(p))
	}
	if got := hex.EncodeToString(p[:16]); got != "001000004153432d45312e3137000000" {
		t.Fatalf("header=%s", got)
	}
	if !bytes.Equal(p[22:38], cid[:]) {
		t.Fatal("CID not copied")
	}
	if string(bytes.TrimRight(p[44:108], "\x00")) != "Benny512" {
		t.Fatal("source name")
	}
	if p[108] != 100 || p[111] != 7 || p[113] != 0 || p[114] != 42 {
		t.Fatal("framing fields")
	}
	if p[124] != 0 || p[125] != 1 || p[126] != 2 || p[127] != 255 {
		t.Fatal("DMX payload")
	}
	if p[128] != 0 {
		t.Fatal("payload was not zero padded")
	}
}

func TestEncodeDataPacketRejectsOversize(t *testing.T) {
	if _, err := EncodeDataPacket(make([]byte, 513), [16]byte{}, "", 0, 0, 1); err == nil {
		t.Fatal("expected oversize error")
	}
	if _, err := EncodeDataPacket(nil, [16]byte{}, "", 0, 0, 0); err == nil {
		t.Fatal("expected universe error")
	}
}
