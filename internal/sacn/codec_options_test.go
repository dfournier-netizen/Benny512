package sacn

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// optionsCID is an arbitrary CID for the Options tests. Only octets 22-37 of
// the packet depend on it.
var optionsCID = [16]byte{
	0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
	0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
}

// TestOptionBitValuesMatchSection626 pins the Option* constants to the bit
// positions printed in ANSI E1.31-2025 Section 6.2.6 and in the Options row of
// Table 4-1 / Table B-13:
//
//	Bit 7 = Preview_Data
//	Bit 6 = Stream_Terminated
//	Bit 5 = Force_Synchronization
//
// The expected values are written as literal octets, not as 1<<n.
func TestOptionBitValuesMatchSection626(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  byte
		want byte
	}{
		{"Preview_Data is bit 7", OptionPreviewData, 0x80},
		{"Stream_Terminated is bit 6", OptionStreamTerminated, 0x40},
		{"Force_Synchronization is bit 5", OptionForceSynchronization, 0x20},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got 0x%02x, want 0x%02x", tc.name, tc.got, tc.want)
		}
	}
}

// TestEncodeDataPacketWithOptionsWritesOctet112 checks that the Options
// argument lands in octet 112 and nowhere else, comparing against a literal
// octet string.
func TestEncodeDataPacketWithOptionsWritesOctet112(t *testing.T) {
	for _, tc := range []struct {
		name        string
		options     byte
		wantOctet   string
		wantDecimal int
	}{
		{"no options", 0, "00", 0},
		{"stream terminated", OptionStreamTerminated, "40", 64},
		{"preview data", OptionPreviewData, "80", 128},
		{"preview and terminated", OptionPreviewData | OptionStreamTerminated, "c0", 192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := EncodeDataPacketWithOptions(nil, optionsCID, "Benny512", 100, 7, 1, tc.options)
			if err != nil {
				t.Fatalf("EncodeDataPacketWithOptions: %v", err)
			}
			if got := hex.EncodeToString(p[112:113]); got != tc.wantOctet {
				t.Errorf("octet 112 (Options) = %s, want %s", got, tc.wantOctet)
			}
			if int(p[112]) != tc.wantDecimal {
				t.Errorf("octet 112 = %d, want %d", p[112], tc.wantDecimal)
			}

			// Everything either side of octet 112 must be untouched by the
			// Options argument.
			ref, err := EncodeDataPacketWithOptions(nil, optionsCID, "Benny512", 100, 7, 1, 0)
			if err != nil {
				t.Fatalf("reference encode: %v", err)
			}
			if !bytes.Equal(p[:112], ref[:112]) {
				t.Error("octets 0..111 changed when Options changed")
			}
			if !bytes.Equal(p[113:], ref[113:]) {
				t.Error("octets 113..637 changed when Options changed")
			}
		})
	}
}

// TestEncodeDataPacketKeepsOptionsZero guards the no-options wrapper: the
// existing call sites must keep emitting an Options field of 0, which Section
// 6.2.6 requires for bits 0-4 in every case.
func TestEncodeDataPacketKeepsOptionsZero(t *testing.T) {
	p, err := EncodeDataPacket(nil, optionsCID, "Benny512", 100, 7, 1)
	if err != nil {
		t.Fatalf("EncodeDataPacket: %v", err)
	}
	if p[112] != 0x00 {
		t.Errorf("octet 112 (Options) = 0x%02x, want 0x00", p[112])
	}
}

// TestTerminationPacketLiteralBytes transcribes the whole framing prefix of a
// Stream_Terminated packet from the standard's field tables and compares octet
// for octet. Values that come from the standard rather than from this package:
// preamble/post-amble sizes and the ACN Packet Identifier (Table 4-1), the PDU
// Flags & Length values and vectors (Table 4-1 and Appendix A), the DMP layer
// constants (Table 7-8), and the Options octet 0x40 (Section 6.2.6, bit 6).
func TestTerminationPacketLiteralBytes(t *testing.T) {
	want, err := hex.DecodeString(strings.Join([]string{
		"0010",                             // 0-1     Preamble Size
		"0000",                             // 2-3     Post-amble Size
		"4153432d45312e3137000000",         // 4-15    ACN Packet Identifier
		"726e",                             // 16-17   Root Flags & Length
		"00000004",                         // 18-21   VECTOR_ROOT_E131_DATA
		"112233445566778899aabbccddeeff00", // 22-37   CID
		"7258",                             // 38-39   Framing Flags & Length
		"00000002",                         // 40-43   VECTOR_E131_DATA_PACKET
		"42656e6e79353132",                 // 44-51   Source Name "Benny512"
		strings.Repeat("00", 56),           // 52-107  Source Name null padding
		"64",                               // 108     Priority 100
		"0000",                             // 109-110 Synchronization Address
		"07",                               // 111     Sequence Number 7
		"40",                               // 112     Options, Stream_Terminated
		"0001",                             // 113-114 Universe 1
		"720b",                             // 115-116 DMP Flags & Length
		"02",                               // 117     VECTOR_DMP_SET_PROPERTY
		"a1",                               // 118     Address Type & Data Type
		"0000",                             // 119-120 First Property Address
		"0001",                             // 121-122 Address Increment
		"0201",                             // 123-124 Property Value Count (513)
		"00",                               // 125     DMX512-A START Code
	}, ""))
	if err != nil {
		t.Fatalf("bad transcription: %v", err)
	}
	if len(want) != 126 {
		t.Fatalf("transcribed prefix is %d octets, want 126", len(want))
	}

	p, err := EncodeDataPacketWithOptions(nil, optionsCID, "Benny512", 100, 7, 1, OptionStreamTerminated)
	if err != nil {
		t.Fatalf("EncodeDataPacketWithOptions: %v", err)
	}
	if len(p) != PacketLen {
		t.Fatalf("packet is %d octets, want %d", len(p), PacketLen)
	}
	if !bytes.Equal(p[:126], want) {
		if i := firstDiff(p[:126], want); i >= 0 {
			t.Fatalf("octet %d: got 0x%02x, want 0x%02x\ngot  %s\nwant %s",
				i, p[i], want[i], hex.EncodeToString(p[:126]), hex.EncodeToString(want))
		}
		t.Fatal("termination packet prefix does not match the transcribed octets")
	}
}
