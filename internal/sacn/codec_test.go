package sacn

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// tableB13Prefix is octets 0..124 of the E1.31 Data Packet printed in ANSI
// E1.31-2025 Appendix B, Table B-13 ("Universe Synchronization Example E1.31
// Data Packet"). It is transcribed from the standard's own printed field
// contents, NOT read back out of this package's implementation.
//
// DELIBERATE DIVERGENCE: octets 109-110 (Synchronization Address) are 0x0000
// here where the table prints 7962. EncodeDataPacket takes no sync-address
// parameter and emits unsynchronized data packets; the standard defines 0 as
// "not synchronized", so this is legal. That is the only divergence -- every
// other octet in 0..124 matches Table B-13 exactly.
var tableB13Prefix = strings.Join([]string{
	"0010",                             // 0-1     Preamble Size
	"0000",                             // 2-3     Post-amble Size
	"4153432d45312e3137000000",         // 4-15    ACN Packet Identifier "ASC-E1.17"
	"726e",                             // 16-17   Root Flags & Length
	"00000004",                         // 18-21   VECTOR_ROOT_E131_DATA
	"ef07c8dd00644401a3a2459ef8e6143e", // 22-37   CID (example only)
	"7258",                             // 38-39   Framing Flags & Length
	"00000002",                         // 40-43   VECTOR_E131_DATA_PACKET
	"536f757263655f41",                 // 44-51   Source Name "Source_A"
	strings.Repeat("00", 56),           // 52-107  Source Name null padding
	"64",                               // 108     Priority 100
	"0000",                             // 109-110 Synchronization Address (see note)
	"9a",                               // 111     Sequence Number 154
	"00",                               // 112     Options (bit 5 = 0)
	"0001",                             // 113-114 Universe 1
	"720b",                             // 115-116 DMP Flags & Length
	"02",                               // 117     VECTOR_DMP_SET_PROPERTY
	"a1",                               // 118     Address Type & Data Type
	"0000",                             // 119-120 First Property Address
	"0001",                             // 121-122 Address Increment
	"0201",                             // 123-124 Property Value Count (513)
}, "")

// exampleCID is the CID printed in Table B-13.
var exampleCID = [16]byte{
	0xef, 0x07, 0xc8, 0xdd, 0x00, 0x64, 0x44, 0x01,
	0xa3, 0xa2, 0x45, 0x9e, 0xf8, 0xe6, 0x14, 0x3e,
}

func firstDiff(got, want []byte) int {
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			return i
		}
	}
	return -1
}

// TestEncodeDataPacketMatchesStandardTableB13 is the critical conformance
// test: it rebuilds the packet the standard prints and compares octet for
// octet against the standard's own bytes.
func TestEncodeDataPacketMatchesStandardTableB13(t *testing.T) {
	want, err := hex.DecodeString(tableB13Prefix)
	if err != nil {
		t.Fatalf("bad transcription of Table B-13: %v", err)
	}
	if len(want) != 125 {
		t.Fatalf("transcribed Table B-13 prefix is %d octets, want 125", len(want))
	}

	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i % 251)
	}

	p, err := EncodeDataPacket(data, exampleCID, "Source_A", 100, 154, 1)
	if err != nil {
		t.Fatalf("EncodeDataPacket: %v", err)
	}
	if len(p) != 638 {
		t.Fatalf("packet length = %d, want 638", len(p))
	}

	if got := p[:125]; !bytes.Equal(got, want) {
		i := firstDiff(got, want)
		t.Fatalf("octets 0..124 do not match ANSI E1.31-2025 Table B-13\n"+
			"first difference at octet %d: got 0x%02x, want 0x%02x\n"+
			" got: %s\nwant: %s",
			i, got[i], want[i], hex.EncodeToString(got), hex.EncodeToString(want))
	}

	if p[125] != 0x00 {
		t.Fatalf("octet 125 (DMX512-A START Code) = 0x%02x, want 0x00", p[125])
	}
	if !bytes.Equal(p[126:638], data) {
		i := firstDiff(p[126:638], data)
		t.Fatalf("octets 126..637 do not carry the 512 data slots; "+
			"first difference at slot %d: got 0x%02x, want 0x%02x",
			i+1, p[126+i], data[i])
	}
}

func TestEncodeDataPacketLengthIsAlways638(t *testing.T) {
	for _, n := range []int{0, 1, 3, 511, 512} {
		p, err := EncodeDataPacket(make([]byte, n), exampleCID, "Benny512", 100, 7, 42)
		if err != nil {
			t.Fatalf("payload of %d slots: %v", n, err)
		}
		if len(p) != 638 {
			t.Fatalf("payload of %d slots: packet length = %d, want 638", n, len(p))
		}
	}
}

func TestEncodeDataPacketZeroPadsShortPayload(t *testing.T) {
	p, err := EncodeDataPacket([]byte{1, 2, 255}, exampleCID, "Benny512", 100, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	if p[125] != 0x00 {
		t.Fatalf("START Code = 0x%02x, want 0x00", p[125])
	}
	if p[126] != 1 || p[127] != 2 || p[128] != 255 {
		t.Fatalf("slots 1..3 = %v, want [1 2 255]", p[126:129])
	}
	for i := 129; i < 638; i++ {
		if p[i] != 0 {
			t.Fatalf("octet %d = 0x%02x, want 0x00 (short payload must be zero padded)", i, p[i])
		}
	}
}

func TestEncodeDataPacketRejectsOversizePayload(t *testing.T) {
	if _, err := EncodeDataPacket(make([]byte, 513), exampleCID, "", 0, 0, 1); err == nil {
		t.Fatal("expected an error for a 513 slot payload")
	}
	if _, err := EncodeDataPacket(make([]byte, 512), exampleCID, "", 0, 0, 1); err != nil {
		t.Fatalf("512 slots must be accepted: %v", err)
	}
}

func TestEncodeDataPacketRejectsInvalidUniverse(t *testing.T) {
	for _, u := range []uint16{0, 63999 + 1, 65535} {
		_, err := EncodeDataPacket(nil, exampleCID, "", 0, 0, u)
		if !errors.Is(err, ErrInvalidUniverse) {
			t.Fatalf("universe %d: err = %v, want ErrInvalidUniverse", u, err)
		}
	}
	for _, u := range []uint16{1, 63999} {
		if _, err := EncodeDataPacket(nil, exampleCID, "", 0, 0, u); err != nil {
			t.Fatalf("universe %d must be accepted: %v", u, err)
		}
	}
}

// TestEncodeDataPacketSourceNameTruncation checks that an over-long source
// name is cut on a UTF-8 rune boundary and always leaves a null terminator
// inside the 64 octet field.
func TestEncodeDataPacketSourceNameTruncation(t *testing.T) {
	// 1 ASCII octet + 30 three-octet runes = 91 octets. A blind cut at 63
	// octets would land in the middle of the rune starting at octet 61.
	long := "x" + strings.Repeat("あ", 30)

	p, err := EncodeDataPacket(nil, exampleCID, long, 100, 7, 1)
	if err != nil {
		t.Fatal(err)
	}

	field := p[44:108]
	if field[63] != 0 {
		t.Fatalf("source name field has no null terminator: last octet = 0x%02x", field[63])
	}
	name := string(bytes.TrimRight(field, "\x00"))
	if len(name) > 63 {
		t.Fatalf("source name is %d octets, want at most 63", len(name))
	}
	if !utf8.ValidString(name) {
		t.Fatalf("truncation split a multi-byte rune: %q", name)
	}
	if want := "x" + strings.Repeat("あ", 20); name != want {
		t.Fatalf("source name = %q, want %q", name, want)
	}

	// A name that fits is stored verbatim.
	p, err = EncodeDataPacket(nil, exampleCID, "Benny512", 100, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytes.TrimRight(p[44:108], "\x00")); got != "Benny512" {
		t.Fatalf("source name = %q, want %q", got, "Benny512")
	}
}
