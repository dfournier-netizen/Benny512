package rdm

import (
	"bytes"
	"errors"
	"testing"
)

// --- Golden fixture: GET DEVICE_INFO request ---

func TestGoldenGetDeviceInfoRequestDecode(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	if len(b) != 26 {
		t.Fatalf("len=%d", len(b))
	}
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.DestinationUID != (UID{0x7A70, 0x12345678}) {
		t.Fatalf("dest=%v", msg.DestinationUID)
	}
	if msg.SourceUID != (UID{0x7A70, 0x00000001}) {
		t.Fatalf("src=%v", msg.SourceUID)
	}
	if msg.TransactionNumber != 0x00 || msg.PortIDOrResponseType != 0x01 || msg.MessageCount != 0x00 {
		t.Fatalf("hdr=%+v", msg)
	}
	if msg.SubDevice != 0x0000 || msg.CommandClass != GetCommand || msg.ParameterID != PIDDeviceInfo {
		t.Fatalf("fields=%+v", msg)
	}
	if len(msg.ParameterData) != 0 {
		t.Fatalf("pd=%v", msg.ParameterData)
	}
	if msg.MessageLength() != 24 {
		t.Fatalf("len=%d", msg.MessageLength())
	}
}

func TestGoldenGetDeviceInfoRequestEncodeByteExact(t *testing.T) {
	expected := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	msg := Message{
		DestinationUID:       UID{0x7A70, 0x12345678},
		SourceUID:            UID{0x7A70, 0x00000001},
		TransactionNumber:    0x00,
		PortIDOrResponseType: 0x01,
		MessageCount:         0x00,
		SubDevice:            0x0000,
		CommandClass:         GetCommand,
		ParameterID:          PIDDeviceInfo,
		ParameterData:        nil,
	}
	got := Encode(msg)
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestGoldenChecksum0x044F(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00")
	if got := Checksum(b); got != 0x044F {
		t.Fatalf("got 0x%04X", got)
	}
}

// --- Golden fixture: GET_RESPONSE / ACK for DEVICE_INFO ---

func TestGoldenGetResponseDeviceInfoDecode(t *testing.T) {
	b := hexBytes("CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00 04 84")
	if len(b) != 45 {
		t.Fatalf("len=%d", len(b))
	}
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.DestinationUID != (UID{0x7A70, 0x00000001}) || msg.SourceUID != (UID{0x7A70, 0x12345678}) {
		t.Fatalf("uids=%+v", msg)
	}
	if msg.TransactionNumber != 0x00 || msg.PortIDOrResponseType != 0x00 {
		t.Fatalf("hdr=%+v", msg)
	}
	rt, ok := msg.ResponseType()
	if !ok || rt != ResponseACK {
		t.Fatalf("responseType=%v ok=%v", rt, ok)
	}
	if msg.CommandClass != GetCommandResponse || msg.ParameterID != PIDDeviceInfo {
		t.Fatalf("fields=%+v", msg)
	}
	expectedPD := hexBytes("01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00")
	if !bytes.Equal(msg.ParameterData, expectedPD) {
		t.Fatalf("pd=%X want %X", msg.ParameterData, expectedPD)
	}
	if msg.MessageLength() != 43 {
		t.Fatalf("len=%d", msg.MessageLength())
	}
}

func TestGoldenGetResponseDeviceInfoEncodeByteExact(t *testing.T) {
	expected := hexBytes("CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00 04 84")
	pd := hexBytes("01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00")
	msg := Message{
		DestinationUID:       UID{0x7A70, 0x00000001},
		SourceUID:            UID{0x7A70, 0x12345678},
		TransactionNumber:    0x00,
		PortIDOrResponseType: 0x00,
		MessageCount:         0x00,
		SubDevice:            0x0000,
		CommandClass:         GetCommandResponse,
		ParameterID:          PIDDeviceInfo,
		ParameterData:        pd,
	}
	got := Encode(msg)
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestGoldenChecksum0x0484(t *testing.T) {
	b := hexBytes("CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00")
	if got := Checksum(b); got != 0x0484 {
		t.Fatalf("got 0x%04X", got)
	}
}

// --- Golden fixture: DUB request ---

func TestGoldenDUBRequestDecode(t *testing.T) {
	b := hexBytes("CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF 0D EE")
	if len(b) != 38 {
		t.Fatalf("len=%d", len(b))
	}
	msg, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if msg.DestinationUID != BroadcastAll {
		t.Fatalf("dest=%v", msg.DestinationUID)
	}
	if msg.SourceUID != (UID{0x7A70, 0x00000001}) {
		t.Fatalf("src=%v", msg.SourceUID)
	}
	if msg.CommandClass != DiscoveryCommand || msg.ParameterID != PIDDiscUniqueBranch {
		t.Fatalf("fields=%+v", msg)
	}
	if len(msg.ParameterData) != 12 {
		t.Fatalf("pd len=%d", len(msg.ParameterData))
	}
	lower := UID{0x0000, 0x00000000}
	upper := UID{0xFFFF, 0xFFFFFFFF}
	if !bytes.Equal(msg.ParameterData[0:6], lower.Bytes()) || !bytes.Equal(msg.ParameterData[6:12], upper.Bytes()) {
		t.Fatalf("pd=%X", msg.ParameterData)
	}
}

func TestGoldenDUBRequestEncodeByteExact(t *testing.T) {
	expected := hexBytes("CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF 0D EE")
	source := UID{0x7A70, 0x00000001}
	lower := UID{0x0000, 0x00000000}
	upper := UID{0xFFFF, 0xFFFFFFFF}
	msg := DiscoveryUniqueBranchRequest(source, lower, upper, 0x00, 0x01)
	got := Encode(msg)
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestGoldenChecksum0x0DEE(t *testing.T) {
	b := hexBytes("CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF")
	if got := Checksum(b); got != 0x0DEE {
		t.Fatalf("got 0x%04X", got)
	}
}

// --- Corrupted checksum throws ---

func TestCorruptedChecksumThrows(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	b[len(b)-1] ^= 0xFF
	_, err := Decode(b)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

// --- DUB response: worked example ---

func TestDUBResponseWorkedExampleEncode(t *testing.T) {
	uid := UID{0x7A70, 0x12345678}
	encoded := EncodeDUBResponse(uid, 7)
	expected := hexBytes("FE FE FE FE FE FE FE AA FA 7F FA 75 BA 57 BE 75 FE 57 FA 7D AB 55 FE FF")
	if !bytes.Equal(encoded, expected) {
		t.Fatalf("got %X want %X", encoded, expected)
	}
	if len(encoded) != 24 {
		t.Fatalf("len=%d", len(encoded))
	}
}

func TestDUBResponseWorkedExampleDecode(t *testing.T) {
	b := hexBytes("FE FE FE FE FE FE FE AA FA 7F FA 75 BA 57 BE 75 FE 57 FA 7D AB 55 FE FF")
	uid, err := DecodeDUBResponse(b)
	if err != nil {
		t.Fatal(err)
	}
	if uid != (UID{0x7A70, 0x12345678}) {
		t.Fatalf("got %v", uid)
	}
}

func TestDUBResponseChecksum0x01FE(t *testing.T) {
	uid := UID{0x7A70, 0x12345678}
	if got := Checksum(uid.Bytes()); got != 0x01FE {
		t.Fatalf("got 0x%04X", got)
	}
}

func TestDUBResponseORDecodeANDPairwise(t *testing.T) {
	pairs := []struct{ e1, e2, d byte }{
		{0xFA, 0x7F, 0x7A},
		{0xFA, 0x75, 0x70},
		{0xBA, 0x57, 0x12},
		{0xBE, 0x75, 0x34},
		{0xFE, 0x57, 0x56},
		{0xFA, 0x7D, 0x78},
		{0xAB, 0x55, 0x01},
		{0xFE, 0xFF, 0xFE},
	}
	for _, p := range pairs {
		if got := p.e1 & p.e2; got != p.d {
			t.Fatalf("%02X & %02X = %02X, want %02X", p.e1, p.e2, got, p.d)
		}
		if got := p.d | 0xAA; got != p.e1 {
			t.Fatalf("%02X | AA = %02X, want %02X", p.d, got, p.e1)
		}
		if got := p.d | 0x55; got != p.e2 {
			t.Fatalf("%02X | 55 = %02X, want %02X", p.d, got, p.e2)
		}
	}
}

func TestDUBResponsePreambleVariants(t *testing.T) {
	uid := UID{0x1234, 0x56789ABC}
	for preamble := 0; preamble <= 7; preamble++ {
		encoded := EncodeDUBResponse(uid, preamble)
		if len(encoded) != preamble+1+16 {
			t.Fatalf("preamble=%d len=%d", preamble, len(encoded))
		}
		decoded, err := DecodeDUBResponse(encoded)
		if err != nil || decoded != uid {
			t.Fatalf("preamble=%d decoded=%v err=%v", preamble, decoded, err)
		}
	}
}

func TestDUBResponsePreambleLengthClamped(t *testing.T) {
	uid := UID{0x1234, 0x56789ABC}
	tooLong := EncodeDUBResponse(uid, 20)
	if len(tooLong) != 7+1+16 {
		t.Fatalf("len=%d", len(tooLong))
	}
	negative := EncodeDUBResponse(uid, -3)
	if len(negative) != 0+1+16 {
		t.Fatalf("len=%d", len(negative))
	}
}

func TestDUBResponseMissingSeparatorThrows(t *testing.T) {
	b := bytes.Repeat([]byte{0xFE}, 8)
	if _, err := DecodeDUBResponse(b); err == nil {
		t.Fatal("expected error")
	}
}

func TestDUBResponsePreambleTooLongThrows(t *testing.T) {
	b := bytes.Repeat([]byte{0xFE}, 8)
	b = append(b, 0xAA)
	b = append(b, make([]byte, 16)...)
	_, err := DecodeDUBResponse(b)
	if !errors.Is(err, ErrDUBPreambleTooLong) {
		t.Fatalf("expected ErrDUBPreambleTooLong, got %v", err)
	}
}

func TestDUBResponseTruncatedPayloadThrows(t *testing.T) {
	b := bytes.Repeat([]byte{0xFE}, 3)
	b = append(b, 0xAA, 0xFF, 0xFF)
	_, err := DecodeDUBResponse(b)
	if !errors.Is(err, ErrDUBTooShort) {
		t.Fatalf("expected ErrDUBTooShort, got %v", err)
	}
}

func TestDUBResponseChecksumMismatchThrows(t *testing.T) {
	uid := UID{0x7A70, 0x12345678}
	encoded := EncodeDUBResponse(uid, 0)
	encoded[len(encoded)-1] = 0x55
	encoded[len(encoded)-2] = 0xAA
	_, err := DecodeDUBResponse(encoded)
	if !errors.Is(err, ErrDUBChecksumMismatch) {
		t.Fatalf("expected ErrDUBChecksumMismatch, got %v", err)
	}
}

// --- Round-trip: seeded randomized valid RDM messages, >=1000 iterations ---

func TestRandomizedRoundTrip(t *testing.T) {
	g := newSeededGenerator(0xC0FFEE)
	for i := 0; i < 1000; i++ {
		msg := randomRDMMessage(g)
		encoded := Encode(msg)
		decoded, err := Decode(encoded)
		if err != nil {
			t.Fatalf("i=%d err=%v", i, err)
		}
		if decoded.DestinationUID != msg.DestinationUID || decoded.SourceUID != msg.SourceUID ||
			decoded.TransactionNumber != msg.TransactionNumber || decoded.PortIDOrResponseType != msg.PortIDOrResponseType ||
			decoded.MessageCount != msg.MessageCount || decoded.SubDevice != msg.SubDevice ||
			decoded.CommandClass != msg.CommandClass || decoded.ParameterID != msg.ParameterID ||
			!bytes.Equal(decoded.ParameterData, msg.ParameterData) {
			t.Fatalf("i=%d roundtrip mismatch:\n got %+v\nwant %+v", i, decoded, msg)
		}
	}
}

func TestRandomizedDUBRoundTrip(t *testing.T) {
	g := newSeededGenerator(0xD00D)
	for i := 0; i < 1000; i++ {
		uid := randomUID(g)
		preamble := g.intn(8)
		encoded := EncodeDUBResponse(uid, preamble)
		decoded, err := DecodeDUBResponse(encoded)
		if err != nil || decoded != uid {
			t.Fatalf("i=%d decoded=%v err=%v", i, decoded, err)
		}
	}
}

// --- Malformed input: truncation at every length, never crashes ---

func TestTruncationNeverCrashes(t *testing.T) {
	full := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	for l := 0; l <= len(full); l++ {
		_, _ = Decode(full[:l])
	}
}

func TestTruncationBelowMinimumThrowsTooShort(t *testing.T) {
	for l := 0; l < 26; l++ {
		b := make([]byte, l)
		_, err := Decode(b)
		if !errors.Is(err, ErrTooShort) {
			t.Fatalf("len=%d: expected ErrTooShort, got %v", l, err)
		}
	}
}

func TestInvalidStartCodeThrows(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	b[0] = 0x00
	_, err := Decode(b)
	if !errors.Is(err, ErrInvalidStartCode) {
		t.Fatalf("expected ErrInvalidStartCode, got %v", err)
	}
}

func TestInvalidSubStartCodeThrows(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	b[1] = 0x02
	_, err := Decode(b)
	if !errors.Is(err, ErrInvalidSubStartCode) {
		t.Fatalf("expected ErrInvalidSubStartCode, got %v", err)
	}
}

func TestInvalidCommandClassThrows(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	b[20] = 0x99
	if _, err := Decode(b); err == nil {
		t.Fatal("expected error")
	}
}

func TestMessageLengthMismatchThrows(t *testing.T) {
	b := hexBytes("CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F")
	b[2] = 0x19
	if _, err := Decode(b); err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidPDLThrows(t *testing.T) {
	b := make([]byte, 26)
	b[0] = StartCode
	b[1] = SubStartCode
	b[23] = 0xF0 // 240 > 231
	_, err := Decode(b)
	if !errors.Is(err, ErrInvalidPDL) {
		t.Fatalf("expected ErrInvalidPDL, got %v", err)
	}
}

// --- Fuzz-style: 10k random-byte buffers through decode, never crash ---

func TestManyRandomBytesNeverCrash(t *testing.T) {
	g := newSeededGenerator(0xFEEDBEEF)
	for i := 0; i < 10000; i++ {
		l := g.intn(81)
		b := randomBytes(l, g)
		_, _ = Decode(b)
		_, _ = DecodeDUBResponse(b)
	}
}

// --- Encode is total (never throws) even with oversized parameter data ---

func TestEncodeClampsOversizedParameterData(t *testing.T) {
	oversized := bytes.Repeat([]byte{0xAB}, 300)
	msg := Message{
		DestinationUID:       BroadcastAll,
		SourceUID:            UID{1, 1},
		TransactionNumber:    0,
		PortIDOrResponseType: 0,
		MessageCount:         0,
		SubDevice:            0,
		CommandClass:         SetCommand,
		ParameterID:          PIDDeviceLabel,
		ParameterData:        oversized,
	}
	encoded := Encode(msg)
	if len(encoded) != 24+231+2 {
		t.Fatalf("len=%d", len(encoded))
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.ParameterData) != 231 {
		t.Fatalf("pd len=%d", len(decoded.ParameterData))
	}
}
