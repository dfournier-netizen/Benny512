package artnet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"benny512/internal/rdm"
)

// --- Golden fixture: ArtPoll ---

func TestGoldenArtPollDecode(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00")
	if len(b) != 24 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Kind != KindPoll {
		t.Fatalf("expected KindPoll, got %v", pkt.Kind)
	}
	p := pkt.Poll
	if p.ProtocolVersion != 0x000E || p.Flags != 0x02 || p.DiagPriority != 0x00 {
		t.Fatalf("hdr=%+v", p)
	}
	if p.TargetPortAddressTop != 0 || p.TargetPortAddressBottom != 0 || p.EstaManufacturer != 0 || p.Oem != 0 {
		t.Fatalf("fields=%+v", p)
	}
}

func TestGoldenArtPollEncodeByteExact(t *testing.T) {
	// NOTE: the report's fixture is 24 bytes, but the field table only
	// defines fields through offset 21 (Oem Lo) = 22 bytes total; the 2
	// trailing zero bytes are extraneous, undocumented padding. Compare
	// against the field-table-consistent 22-byte prefix, mirroring the
	// Swift reference test's documented fixture correction.
	expected := hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00")
	p := Poll{ProtocolVersion: 0x000E, Flags: 0x02, DiagPriority: 0}
	got := Encode(Packet{Kind: KindPoll, Poll: p})
	if !bytes.Equal(got, expected[:22]) {
		t.Fatalf("got %X want %X", got, expected[:22])
	}
}

func TestArtPollMinimumAcceptedLength14(t *testing.T) {
	full := hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 03")
	if len(full) != 14 {
		t.Fatalf("len=%d", len(full))
	}
	pkt, err := Decode(full)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.Poll
	if p.Flags != 0x02 || p.DiagPriority != 0x03 || p.TargetPortAddressTop != 0 || p.EstaManufacturer != 0 || p.Oem != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestArtPollBelow14BytesThrows(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02")
	if len(b) != 13 {
		t.Fatalf("len=%d", len(b))
	}
	_, err := Decode(b)
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrTooShort) || de.OpCode == nil || *de.OpCode != 0x2000 {
		t.Fatalf("expected tooShort opcode 0x2000, got %v", err)
	}
}

// --- Golden fixture: ArtPollReply ---

// artPollReplyHex is reconstructed field-by-field per the Swift reference
// test's documented correction: the wire-format report's raw §3.2 hex blob
// is short 5 bytes vs. its own field table (234 vs 239), so this fixture
// uses the corrected 239-byte hex from the Swift test file, not the report's
// raw hex.
const artPollReplyHex = `
41 72 74 2D 4E 65 74 00 00 21 0A 00 00 32 36 19 00 01 00 00 00 00 00 02
00 00 42 65 6E 6E 79 35 31 32 00 00 00 00 00 00 00 00 00 00 42 65 6E 6E
79 35 31 32 20 4E 6F 64 65 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 23 30 30 30 31 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00
00 00 00 00 00 01 80 00 00 00 00 00 00 00 80 00 00 00 00 00 00 00 00 00
00 00 64 00 00 00 00 00 00 02 00 00 00 00 01 0A 00 00 32 01 08 00 00 00
00 00 00 00 00 00 00 00 00 00 00 2C 00 00 00 00 00 00 00 00 00 00 00
`

func TestGoldenArtPollReplyDecode(t *testing.T) {
	b := hexBytes(artPollReplyHex)
	if len(b) != 239 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Kind != KindPollReply {
		t.Fatalf("kind=%v", pkt.Kind)
	}
	p := pkt.PollReply
	if p.IPAddress != [4]byte{10, 0, 0, 50} {
		t.Fatalf("ip=%v", p.IPAddress)
	}
	if p.Port != 0x1936 || p.VersInfoHi != 0 || p.VersInfoLo != 1 {
		t.Fatalf("vers=%+v", p)
	}
	if p.NetSwitch != 0 || p.SubSwitch != 0 || p.Oem != 0 || p.UbeaVersion != 0 || p.Status1 != 0x02 {
		t.Fatalf("hdr2=%+v", p)
	}
	if p.EstaManufacturer != 0 {
		t.Fatalf("esta=%v", p.EstaManufacturer)
	}
	if p.ShortName != "Benny512" || p.LongName != "Benny512 Node" || p.NodeReport != "#0001" {
		t.Fatalf("names=%+v", p)
	}
	if p.NumPorts != 1 {
		t.Fatalf("numPorts=%v", p.NumPorts)
	}
	if p.PortTypes != [4]byte{0x80, 0, 0, 0} || p.GoodInput != [4]byte{0, 0, 0, 0} || p.GoodOutputA != [4]byte{0x80, 0, 0, 0} {
		t.Fatalf("ports=%+v", p)
	}
	if p.SwIn != [4]byte{0, 0, 0, 0} || p.SwOut != [4]byte{0, 0, 0, 0} {
		t.Fatalf("sw=%+v", p)
	}
	if p.AcnPriority != 0x64 || p.SwMacro != 0 || p.SwRemote != 0 {
		t.Fatalf("acn=%+v", p)
	}
	if p.Spare != [3]byte{0, 0, 0} || p.Style != 0x00 {
		t.Fatalf("spare/style=%+v", p)
	}
	if p.MAC != [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01} {
		t.Fatalf("mac=%v", p.MAC)
	}
	if p.BindIP != [4]byte{10, 0, 0, 50} || p.BindIndex != 1 {
		t.Fatalf("bind=%+v", p)
	}
	if p.Status2 != 0x08 || p.GoodOutputB != [4]byte{0, 0, 0, 0} || p.Status3 != 0 {
		t.Fatalf("status=%+v", p)
	}
	if p.DefaultRespUID != [6]byte{0, 0, 0, 0, 0, 0} || p.User != 0 {
		t.Fatalf("uid/user=%+v", p)
	}
	if p.RefreshRate != 0x002C || p.BackgroundQueuePolicy != 0 {
		t.Fatalf("refresh=%+v", p)
	}
	if p.Filler != [10]byte{} {
		t.Fatalf("filler=%v", p.Filler)
	}
}

func TestGoldenArtPollReplyEncodeByteExact(t *testing.T) {
	b := hexBytes(artPollReplyHex)
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	got := Encode(pkt)
	if !bytes.Equal(got, b) {
		t.Fatalf("got %X\nwant %X", got, b)
	}
}

func TestArtPollReplyMinimumAcceptedLength207(t *testing.T) {
	full := hexBytes(artPollReplyHex)
	truncated := full[:207]
	pkt, err := Decode(truncated)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.PollReply
	if p.BindIP != [4]byte{0, 0, 0, 0} || p.BindIndex != 0 || p.Status2 != 0 || p.RefreshRate != 0 {
		t.Fatalf("expected zeroed trailing fields, got %+v", p)
	}
	if p.MAC != [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01} || p.Style != 0x00 {
		t.Fatalf("expected fields through MAC decoded, got %+v", p)
	}
}

func TestArtPollReplyBelow207BytesThrows(t *testing.T) {
	full := hexBytes(artPollReplyHex)
	truncated := full[:206]
	_, err := Decode(truncated)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x2100 || de.Need != 207 {
		t.Fatalf("expected tooShort opcode 0x2100 need 207, got %v", err)
	}
}

func TestArtPollReplyHasNoProtVerBytes(t *testing.T) {
	b := hexBytes(artPollReplyHex)
	if !bytes.Equal(b[10:14], []byte{10, 0, 0, 50}) {
		t.Fatalf("got %v", b[10:14])
	}
}

// realEN4Port1Hex is byte-for-byte the "Port 1" ArtPollReply RDM-LOG7
// captured from a real Obsidian EN4 at the bench (2026-08-26 session,
// investigating "every node shows four ports, three of them n/a"): one
// bind index per physical port, NumPorts=1, only port[0] populated —
// port[1..3] are on-the-wire padding the node itself sends as zero.
const realEN4Port1Hex = "4172742d4e6574000021020b5a043619030100002a2000e2a622506f727420310000000000000000000000004e4554524f4e20454e340000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002330303031205b313338395d205263506f7765724f6b0000000000000000000000000000000000000000000000000000000000000000000000000000000000000001800000000800000000000000000000000000000000000000000000424c8a57476a020b5a0401dd400000003c22a606d5067f000000280000000000000000000000"

// TestGoldenArtPollReplyDecodeRealEN4SinglePort proves the decode layer
// carries the wire's own NumPorts=1 faithfully rather than inventing
// anything for slots 1-3: those fixed [4]byte arrays are decoded as-is
// (all zero, exactly what the node sent), and it is NumPorts — not the
// arrays' fixed length — that says how many are real. The bench report's
// hypothesis was that Benny512 renders slots beyond NumPorts as phantom
// "n/a" ports; this fixture is the actual evidence packet, kept here so
// that hypothesis is checked against real bytes, not paraphrase. (The
// place that turns NumPorts into a port *list* is
// internal/session/artnetsession.go's nodeFromPollReply, not this
// package — see internal/web/server_test.go's
// TestGetNodesSinglePortEN4NotPaddedToFour for proof of that step using
// this same fixture, and this round's notes entry for the full trace
// showing nodeFromPollReply already clamps to NumPorts and has since its
// initial implementation.)
func TestGoldenArtPollReplyDecodeRealEN4SinglePort(t *testing.T) {
	b, err := hex.DecodeString(realEN4Port1Hex)
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Kind != KindPollReply {
		t.Fatalf("kind=%v", pkt.Kind)
	}
	p := pkt.PollReply
	if p.NumPorts != 1 {
		t.Fatalf("numPorts=%d, want 1 (per RDM-LOG7)", p.NumPorts)
	}
	if p.PortTypes != [4]byte{0x80, 0, 0, 0} {
		t.Fatalf("portTypes=%v, want only slot 0 populated (0x80 = output)", p.PortTypes)
	}
	if p.GoodInput != [4]byte{0x08, 0, 0, 0} {
		t.Fatalf("goodInput=%v, want only slot 0 populated", p.GoodInput)
	}
	if p.GoodOutputB != [4]byte{0x40, 0, 0, 0} {
		t.Fatalf("goodOutputB=%v, want only slot 0 populated", p.GoodOutputB)
	}
	if p.BindIndex != 1 {
		t.Fatalf("bindIndex=%d, want 1", p.BindIndex)
	}
	if p.ShortName != "Port 1" || p.LongName != "NETRON EN4" {
		t.Fatalf("names=%+v", p)
	}
}

// --- Golden fixture: ArtDmx ---

func TestGoldenArtDmxDecode(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 50 00 0E 01 00 00 00 00 04 FF 00 7F 01")
	if len(b) != 22 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.Dmx
	if p.ProtocolVersion != 0x000E || p.Sequence != 1 || p.Physical != 0 || p.SubUni != 0 || p.Net != 0 {
		t.Fatalf("%+v", p)
	}
	if !bytes.Equal(p.Data, []byte{255, 0, 127, 1}) {
		t.Fatalf("data=%v", p.Data)
	}
}

func TestGoldenArtDmxEncodeByteExact(t *testing.T) {
	expected := hexBytes("41 72 74 2D 4E 65 74 00 00 50 00 0E 01 00 00 00 00 04 FF 00 7F 01")
	p := Dmx{ProtocolVersion: 0x000E, Sequence: 1, Physical: 0, SubUni: 0, Net: 0, Data: []byte{255, 0, 127, 1}}
	got := Encode(Packet{Kind: KindDmx, Dmx: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtDmxOddLengthAcceptedOnDecodeButPaddedOnEncode(t *testing.T) {
	p := Dmx{Data: []byte{1, 2, 3}}
	encoded := Encode(Packet{Kind: KindDmx, Dmx: p})
	pkt, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pkt.Dmx.Data, []byte{1, 2, 3, 0}) {
		t.Fatalf("data=%v", pkt.Dmx.Data)
	}
}

func TestArtDmxBelow18BytesThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x50)
	pkt = append(pkt, make([]byte, 5)...)
	if len(pkt) != 15 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x5000 {
		t.Fatalf("expected tooShort opcode 0x5000, got %v", err)
	}
}

// --- Golden fixture: ArtTimeCode ---

func TestGoldenArtTimeCodeDecode(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01 03")
	if len(b) != 19 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.TimeCode
	if p.Filler1 != 0 || p.StreamId != 0 || p.Frames != 4 || p.Seconds != 3 || p.Minutes != 2 || p.Hours != 1 || p.Type != 3 {
		t.Fatalf("%+v", p)
	}
}

func TestGoldenArtTimeCodeEncodeByteExact(t *testing.T) {
	expected := hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01 03")
	p := TimeCode{ProtocolVersion: 0x000E, Filler1: 0, StreamId: 0, Frames: 4, Seconds: 3, Minutes: 2, Hours: 1, Type: 3}
	got := Encode(Packet{Kind: KindTimeCode, TimeCode: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtTimeCodeStreamIdAtOffset13(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 05 04 03 02 01 03")
	if b[13] != 0x05 {
		t.Fatalf("b[13]=%v", b[13])
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.TimeCode.StreamId != 0x05 {
		t.Fatalf("streamId=%v", pkt.TimeCode.StreamId)
	}
}

func TestArtTimeCodeIsExactly19Bytes(t *testing.T) {
	p := TimeCode{}
	got := Encode(Packet{Kind: KindTimeCode, TimeCode: p})
	if len(got) != 19 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestArtTimeCodeBelow19BytesThrows(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01")
	if len(b) != 18 {
		t.Fatalf("len=%d", len(b))
	}
	_, err := Decode(b)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x9700 || de.Need != 19 {
		t.Fatalf("expected tooShort opcode 0x9700 need 19, got %v", err)
	}
}

// --- Golden fixture: ArtTodRequest ---

func TestGoldenArtTodRequestDecode(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 80 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00")
	if len(b) != 25 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.TodRequest
	if p.Net != 0 || p.Command != 0x00 {
		t.Fatalf("%+v", p)
	}
	if !bytes.Equal(p.Address, []byte{0x00}) {
		t.Fatalf("address=%v", p.Address)
	}
}

func TestGoldenArtTodRequestEncodeByteExact(t *testing.T) {
	expected := hexBytes("41 72 74 2D 4E 65 74 00 00 80 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00")
	p := TodRequest{ProtocolVersion: 0x000E, Net: 0, Command: 0, Address: []byte{0x00}}
	got := Encode(Packet{Kind: KindTodRequest, TodRequest: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtTodRequestBelow24BytesThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x80)
	pkt = append(pkt, make([]byte, 13)...)
	if len(pkt) != 23 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x8000 || de.Need != 24 {
		t.Fatalf("expected tooShort opcode 0x8000 need 24, got %v", err)
	}
}

func TestArtTodRequestAddressCountMismatchThrows(t *testing.T) {
	// Declares AddCount=5 but supplies 0 trailing bytes.
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x80)         // OpCode LE
	pkt = append(pkt, 0x00, 0x0E)         // ProtVer BE
	pkt = append(pkt, 0, 0)               // filler1, filler2
	pkt = append(pkt, make([]byte, 7)...) // spare
	pkt = append(pkt, 0, 0, 5)            // net, command, AddCount=5
	if len(pkt) != 24 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	if !errors.Is(err, ErrAddressCountMismatch) {
		t.Fatalf("expected ErrAddressCountMismatch, got %v", err)
	}
}

// --- Golden fixture: ArtTodData ---

func TestGoldenArtTodDataDecode(t *testing.T) {
	b := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 81 00 0E 01 01 00 00 00 00 00 00 00 00 00 00
	00 01 00 01 7A 70 12 34 56 78
	`)
	if len(b) != 34 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.TodData
	if p.RdmVersion != 1 || p.Port != 1 || p.Net != 0 || p.CommandResponse != 0 || p.Address != 0 {
		t.Fatalf("%+v", p)
	}
	if p.Spare != [7]byte{} {
		t.Fatalf("spare=%v", p.Spare)
	}
	if p.UidTotal != 1 || p.BlockCount != 0 {
		t.Fatalf("tot/blk=%+v", p)
	}
	want := []rdm.UID{{ManufacturerID: 0x7A70, DeviceID: 0x12345678}}
	if !reflect.DeepEqual(p.Tod, want) {
		t.Fatalf("tod=%v want %v", p.Tod, want)
	}
}

func TestGoldenArtTodDataEncodeByteExact(t *testing.T) {
	expected := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 81 00 0E 01 01 00 00 00 00 00 00 00 00 00 00
	00 01 00 01 7A 70 12 34 56 78
	`)
	p := TodData{
		ProtocolVersion: 0x000E, RdmVersion: 1, Port: 1, Net: 0, CommandResponse: 0, Address: 0,
		UidTotal: 1, BlockCount: 0, Tod: []rdm.UID{{ManufacturerID: 0x7A70, DeviceID: 0x12345678}},
	}
	got := Encode(Packet{Kind: KindTodData, TodData: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtTodDataBelow28BytesThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x81)
	pkt = append(pkt, make([]byte, 17)...)
	if len(pkt) != 27 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x8100 || de.Need != 28 {
		t.Fatalf("expected tooShort opcode 0x8100 need 28, got %v", err)
	}
}

func TestArtTodDataUidCountMismatchThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x81)
	pkt = append(pkt, 0x00, 0x0E)
	pkt = append(pkt, 1, 1)               // rdmVersion, port
	pkt = append(pkt, make([]byte, 7)...) // spare
	pkt = append(pkt, 0, 0, 0)            // net, commandResponse, address
	pkt = append(pkt, 0x00, 0x0A)         // uidTotal = 10
	pkt = append(pkt, 0)                  // blockCount
	pkt = append(pkt, 10)                 // uidCount = 10, no bytes follow
	if len(pkt) != 28 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	if !errors.Is(err, ErrUIDCountMismatch) {
		t.Fatalf("expected ErrUIDCountMismatch, got %v", err)
	}
}

// --- Golden fixture: ArtTodControl ---

// artTodControlHex is the Swift reference test's corrected 24-byte fixture
// (the report's raw hex has one spurious extra zero byte).
const artTodControlHex = "41 72 74 2D 4E 65 74 00 00 82 00 0E 00 00 00 00 00 00 00 00 00 00 01 00"

func TestGoldenArtTodControlDecode(t *testing.T) {
	b := hexBytes(artTodControlHex)
	if len(b) != 24 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.TodControl
	if p.Net != 0 || p.Command != 0x01 || p.Address != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestGoldenArtTodControlEncodeByteExact(t *testing.T) {
	expected := hexBytes(artTodControlHex)
	p := TodControl{ProtocolVersion: 0x000E, Net: 0, Command: 1, Address: 0}
	got := Encode(Packet{Kind: KindTodControl, TodControl: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtTodControlBelow24BytesThrows(t *testing.T) {
	b := hexBytes(artTodControlHex)[:23]
	_, err := Decode(b)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x8200 || de.Need != 24 {
		t.Fatalf("expected tooShort opcode 0x8200 need 24, got %v", err)
	}
}

// --- Golden fixture: ArtRdm carrying GET DEVICE_INFO ---
//
// Spec-correct as of the wire-format bug fix: ArtRdm's RdmPacket field
// begins at the RDM sub-start code (0x01), excluding the leading 0xCC RDM
// start code — see artnet.Rdm's doc comment for the full story (this
// project shipped the 0xCC-inclusive framing below until a real-hardware
// bench session against an Obsidian/Elation-family gateway showed every
// directed ArtRdm GET silently dropped because of it). The corresponding
// legacy (0xCC-inclusive, pre-fix) 50-byte form is exercised separately in
// TestGoldenArtRdmEncodeByteExactLegacy.

func TestGoldenArtRdmDecode(t *testing.T) {
	b := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
	01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
	04 4F
	`)
	if len(b) != 49 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.Rdm
	if p.RdmVersion != 1 || p.Filler2 != 0 || p.Net != 0 || p.Command != 0 || p.Address != 0 {
		t.Fatalf("%+v", p)
	}
	if p.Spare != [7]byte{} {
		t.Fatalf("spare=%v", p.Spare)
	}
	if len(p.RdmData) != 25 || p.RdmData[0] != 0x01 {
		t.Fatalf("rdmData=%v", p.RdmData)
	}
	msg, err := p.DecodedRDMMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.DestinationUID != (rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678}) {
		t.Fatalf("dest=%v", msg.DestinationUID)
	}
	if msg.SourceUID != (rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x00000001}) {
		t.Fatalf("src=%v", msg.SourceUID)
	}
	if msg.CommandClass != rdm.GetCommand || msg.ParameterID != rdm.PIDDeviceInfo {
		t.Fatalf("cc/pid=%+v", msg)
	}
}

func TestGoldenArtRdmEncodeByteExact(t *testing.T) {
	expected := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
	01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
	04 4F
	`)
	msg := rdm.Message{
		DestinationUID:       rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678},
		SourceUID:            rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x00000001},
		TransactionNumber:    0,
		PortIDOrResponseType: 0x01,
		MessageCount:         0,
		SubDevice:            0,
		CommandClass:         rdm.GetCommand,
		ParameterID:          rdm.PIDDeviceInfo,
		ParameterData:        nil,
	}
	p := EncodeRdmPacket(msg, 0x000E, 0, 0, false)
	got := Encode(Packet{Kind: KindRdm, Rdm: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

// TestGoldenArtRdmEncodeByteExactLegacy is the same GET DEVICE_INFO message
// as TestGoldenArtRdmEncodeByteExact, but with legacyStartCode=true — the
// --legacy-rdm-startcode escape hatch. It must reproduce this project's
// original (spec-incorrect) 0xCC-inclusive 50-byte framing byte-for-byte,
// since that is exactly the framing a node bench-confirmed to need it would
// be expecting.
func TestGoldenArtRdmEncodeByteExactLegacy(t *testing.T) {
	expected := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00
	CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00
	04 4F
	`)
	if len(expected) != 50 {
		t.Fatalf("len=%d", len(expected))
	}
	msg := rdm.Message{
		DestinationUID:       rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678},
		SourceUID:            rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x00000001},
		TransactionNumber:    0,
		PortIDOrResponseType: 0x01,
		MessageCount:         0,
		SubDevice:            0,
		CommandClass:         rdm.GetCommand,
		ParameterID:          rdm.PIDDeviceInfo,
		ParameterData:        nil,
	}
	p := EncodeRdmPacket(msg, 0x000E, 0, 0, true)
	if len(p.RdmData) != 26 || p.RdmData[0] != 0xCC {
		t.Fatalf("legacy rdmData=%X", p.RdmData)
	}
	got := Encode(Packet{Kind: KindRdm, Rdm: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
	// Legacy-framed wire bytes must still decode via the tolerant receive
	// path (decode accepts a payload starting 0xCC as-is).
	pkt, err := Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	msgBack, err := pkt.Rdm.DecodedRDMMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msgBack.ParameterID != rdm.PIDDeviceInfo || msgBack.CommandClass != rdm.GetCommand {
		t.Fatalf("roundtrip mismatch: %+v", msgBack)
	}
}

// --- ArtRdm 0xCC start-code framing: dedicated regression tests for the
// wire-format bug fix (RdmPacket excludes the DMX start code per Art-Net 4;
// see artnet.Rdm's doc comment) ---

// rdmDeviceInfoRequest returns a message with ParameterData explicitly
// []byte{} (not nil) so it compares equal, via reflect.DeepEqual, to a
// message that has round-tripped through rdm.Decode — Decode always
// allocates ParameterData with make([]byte, pdl), which is non-nil even at
// pdl==0.
func rdmDeviceInfoRequest() rdm.Message {
	return rdm.Message{
		DestinationUID:       rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x004D05BF},
		SourceUID:            rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 0x00000001},
		TransactionNumber:    3,
		PortIDOrResponseType: 1,
		CommandClass:         rdm.GetCommand,
		ParameterID:          rdm.PIDDeviceInfo,
		ParameterData:        []byte{},
	}
}

// TestEncodeRdmPacketStripsStartCode: legacyStartCode=false must drop
// rdm.Encode's leading 0xCC before it lands in RdmData.
func TestEncodeRdmPacketStripsStartCode(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	full := rdm.Encode(msg)
	p := EncodeRdmPacket(msg, DefaultProtocolVersion, 0, 0, false)
	if len(p.RdmData) != len(full)-1 {
		t.Fatalf("len(RdmData)=%d want %d", len(p.RdmData), len(full)-1)
	}
	if p.RdmData[0] != rdm.SubStartCode {
		t.Fatalf("RdmData[0]=0x%02X want sub-start code 0x%02X", p.RdmData[0], rdm.SubStartCode)
	}
	if !bytes.Equal(p.RdmData, full[1:]) {
		t.Fatalf("RdmData=%X want %X", p.RdmData, full[1:])
	}
}

// TestEncodeRdmPacketLegacyKeepsStartCode: legacyStartCode=true must keep
// rdm.Encode's output byte-for-byte, 0xCC included.
func TestEncodeRdmPacketLegacyKeepsStartCode(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	full := rdm.Encode(msg)
	p := EncodeRdmPacket(msg, DefaultProtocolVersion, 0, 0, true)
	if !bytes.Equal(p.RdmData, full) {
		t.Fatalf("RdmData=%X want %X", p.RdmData, full)
	}
}

// TestDecodedRDMMessageAcceptsSubStartCodeFirst: the spec-correct receive
// case — RdmData begins at 0x01 (SC_SUB_MESSAGE), no 0xCC.
func TestDecodedRDMMessageAcceptsSubStartCodeFirst(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	full := rdm.Encode(msg)
	p := Rdm{RdmData: append([]byte(nil), full[1:]...)}
	got, err := p.DecodedRDMMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("got %+v want %+v", got, msg)
	}
}

// TestDecodedRDMMessageAcceptsStartCodeFirst: the tolerant case — some real
// nodes send RdmData already 0xCC-prefixed despite the spec excluding it;
// DecodedRDMMessage must decode that as-is rather than double-prepending.
func TestDecodedRDMMessageAcceptsStartCodeFirst(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	full := rdm.Encode(msg)
	p := Rdm{RdmData: append([]byte(nil), full...)}
	got, err := p.DecodedRDMMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("got %+v want %+v", got, msg)
	}
}

// TestDecodedRDMMessageDoesNotMutateCaller guards the "copy, don't mutate
// the caller's slice in place" requirement: decoding a 0x01-first RdmData
// (which requires prepending 0xCC internally) must leave the caller's
// backing array untouched.
func TestDecodedRDMMessageDoesNotMutateCaller(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	full := rdm.Encode(msg)
	rdmData := append([]byte(nil), full[1:]...)
	before := append([]byte(nil), rdmData...)
	p := Rdm{RdmData: rdmData}
	if _, err := p.DecodedRDMMessage(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rdmData, before) {
		t.Fatalf("RdmData mutated: got %X want %X", rdmData, before)
	}
}

// TestRoundTripEncodeRdmPacketDecodedRDMMessage covers EncodeRdmPacket ->
// wire -> Decode -> DecodedRDMMessage for both legacyStartCode settings,
// confirming the reframing is transparent to the decoded RDM message.
func TestRoundTripEncodeRdmPacketDecodedRDMMessage(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		msg := rdmDeviceInfoRequest()
		p := EncodeRdmPacket(msg, DefaultProtocolVersion, 0, 0, legacy)
		wire := Encode(Packet{Kind: KindRdm, Rdm: p})
		pkt, err := Decode(wire)
		if err != nil {
			t.Fatalf("legacy=%v: %v", legacy, err)
		}
		got, err := pkt.Rdm.DecodedRDMMessage()
		if err != nil {
			t.Fatalf("legacy=%v: %v", legacy, err)
		}
		if !reflect.DeepEqual(got, msg) {
			t.Fatalf("legacy=%v: got %+v want %+v", legacy, got, msg)
		}
	}
}

// TestReframingLeavesChecksumAndMessageLengthUntouched: the RDM message
// itself (message-length slot and trailing checksum) must not change
// between the two framings — only where the Art-Net payload starts. This
// re-derives rdm.Encode(msg) as the reference and checks that
// EncodeRdmPacket's output, with 0xCC re-prepended where it was stripped,
// is byte-identical to it in both modes.
func TestReframingLeavesChecksumAndMessageLengthUntouched(t *testing.T) {
	msg := rdmDeviceInfoRequest()
	reference := rdm.Encode(msg) // CC 01 <len> ... <checksum:2>
	wantMessageLength := reference[2]
	wantChecksum := reference[len(reference)-2:]

	for _, legacy := range []bool{false, true} {
		p := EncodeRdmPacket(msg, DefaultProtocolVersion, 0, 0, legacy)
		full := p.RdmData
		if !legacy {
			full = append([]byte{rdm.StartCode}, p.RdmData...)
		}
		if !bytes.Equal(full, reference) {
			t.Fatalf("legacy=%v: reframed message %X want %X", legacy, full, reference)
		}
		if full[2] != wantMessageLength {
			t.Fatalf("legacy=%v: message length %d want %d", legacy, full[2], wantMessageLength)
		}
		if !bytes.Equal(full[len(full)-2:], wantChecksum) {
			t.Fatalf("legacy=%v: checksum %X want %X", legacy, full[len(full)-2:], wantChecksum)
		}
	}
}

func TestArtRdmBelow24BytesThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x83)
	pkt = append(pkt, make([]byte, 13)...)
	if len(pkt) != 23 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x8300 || de.Need != 24 {
		t.Fatalf("expected tooShort opcode 0x8300 need 24, got %v", err)
	}
}

// --- Golden fixture: raw RDM GET_RESPONSE embedded via ArtRdm ---

func TestGoldenRawRDMGetResponseViaArtRdm(t *testing.T) {
	rdmBytes := hexBytes("CC 01 2B 7A 70 00 00 00 01 7A 70 12 34 56 78 00 00 00 00 00 21 00 60 13 01 00 00 01 01 01 01 00 00 00 00 04 01 04 00 01 00 00 00 04 84")
	if len(rdmBytes) != 45 {
		t.Fatalf("len=%d", len(rdmBytes))
	}
	msg, err := rdm.Decode(rdmBytes)
	if err != nil {
		t.Fatal(err)
	}
	artRdm := EncodeRdmPacket(msg, 0x000E, 0, 0, false)
	wire := Encode(Packet{Kind: KindRdm, Rdm: artRdm})
	pkt, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	// EncodeRdmPacket (legacyStartCode=false, spec-correct) strips the
	// leading 0xCC before it goes on the wire, so the round-tripped
	// RdmData is rdmBytes minus its first byte, not rdmBytes itself.
	if !bytes.Equal(pkt.Rdm.RdmData, rdmBytes[1:]) {
		t.Fatalf("rdmData=%X want %X", pkt.Rdm.RdmData, rdmBytes[1:])
	}
	roundTripped, err := pkt.Rdm.DecodedRDMMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTripped, msg) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", roundTripped, msg)
	}
}

// --- Golden fixture: ArtRdmSub ---

func TestGoldenArtRdmSubDecode(t *testing.T) {
	b := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 84 00 0E 01 00 7A 70 12 34 56 78 00 30 00 F0
	00 01 00 02 00 00 00 00 00 01 00 05
	`)
	if len(b) != 36 {
		t.Fatalf("len=%d", len(b))
	}
	pkt, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	p := pkt.RdmSub
	if p.RdmVersion != 1 || p.Filler2 != 0 {
		t.Fatalf("%+v", p)
	}
	if p.UID != (rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678}) {
		t.Fatalf("uid=%v", p.UID)
	}
	if p.Spare1 != 0 || p.CommandClass != 0x30 || p.ParameterID != 0x00F0 || p.SubDevice != 1 {
		t.Fatalf("%+v", p)
	}
	if p.Spare2to5 != [4]byte{0, 0, 0, 0} {
		t.Fatalf("spare2to5=%v", p.Spare2to5)
	}
	if !reflect.DeepEqual(p.Data, []uint16{0x0001, 0x0005}) {
		t.Fatalf("data=%v", p.Data)
	}
}

func TestGoldenArtRdmSubEncodeByteExact(t *testing.T) {
	expected := hexBytes(`
	41 72 74 2D 4E 65 74 00 00 84 00 0E 01 00 7A 70 12 34 56 78 00 30 00 F0
	00 01 00 02 00 00 00 00 00 01 00 05
	`)
	p := RdmSub{
		ProtocolVersion: 0x000E, RdmVersion: 1, Filler2: 0,
		UID:    rdm.UID{ManufacturerID: 0x7A70, DeviceID: 0x12345678},
		Spare1: 0, CommandClass: 0x30, ParameterID: 0x00F0, SubDevice: 1,
		Data: []uint16{0x0001, 0x0005},
	}
	got := Encode(Packet{Kind: KindRdmSub, RdmSub: p})
	if !bytes.Equal(got, expected) {
		t.Fatalf("got %X want %X", got, expected)
	}
}

func TestArtRdmSubBelow32BytesThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x84)
	pkt = append(pkt, make([]byte, 20)...)
	_, err := Decode(pkt)
	var de *DecodeError
	if !errors.As(err, &de) || de.OpCode == nil || *de.OpCode != 0x8400 || de.Need != 32 {
		t.Fatalf("expected tooShort opcode 0x8400 need 32, got %v", err)
	}
}

func TestArtRdmSubCountMismatchThrows(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0x00, 0x84)
	pkt = append(pkt, 0x00, 0x0E)
	pkt = append(pkt, 1, 0) // rdmVersion, filler2
	pkt = append(pkt, rdm.BroadcastAll.Bytes()...)
	pkt = append(pkt, 0)          // spare1
	pkt = append(pkt, 0x30)       // commandClass
	pkt = append(pkt, 0x00, 0xF0) // parameterID
	pkt = append(pkt, 0x00, 0x01) // subDevice
	pkt = append(pkt, 0x00, 0x05) // subCount = 5, no data follows
	pkt = append(pkt, 0, 0, 0, 0) // spare2-5
	if len(pkt) != 32 {
		t.Fatalf("len=%d", len(pkt))
	}
	_, err := Decode(pkt)
	if !errors.Is(err, ErrSubCountMismatch) {
		t.Fatalf("expected ErrSubCountMismatch, got %v", err)
	}
}

// --- DUB request/response through the RDM layer, sanity cross-check at ArtNet layer via ArtRdm ---

func TestDUBRequestGoldenViaArtRdm(t *testing.T) {
	dubBytes := hexBytes("CC 01 24 FF FF FF FF FF FF 7A 70 00 00 00 01 00 01 00 00 00 10 00 01 0C 00 00 00 00 00 00 FF FF FF FF FF FF 0D EE")
	msg, err := rdm.Decode(dubBytes)
	if err != nil {
		t.Fatal(err)
	}
	artRdm := EncodeRdmPacket(msg, 0x000E, 0, 0, false)
	wire := Encode(Packet{Kind: KindRdm, Rdm: artRdm})
	pkt, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	// See TestGoldenRawRDMGetResponseViaArtRdm: spec-correct encode strips
	// the leading 0xCC, so RdmData is dubBytes minus its first byte.
	if !bytes.Equal(pkt.Rdm.RdmData, dubBytes[1:]) {
		t.Fatalf("got %X want %X", pkt.Rdm.RdmData, dubBytes[1:])
	}
}

// --- Malformed / truncation: every packet type, every truncation length never crashes ---

func TestTruncationNeverCrashesAllFixtures(t *testing.T) {
	fixtures := [][]byte{
		hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00"),
		hexBytes(artPollReplyHex),
		hexBytes("41 72 74 2D 4E 65 74 00 00 50 00 0E 01 00 00 00 00 04 FF 00 7F 01"),
		hexBytes("41 72 74 2D 4E 65 74 00 00 97 00 0E 00 00 04 03 02 01 03"),
		hexBytes("41 72 74 2D 4E 65 74 00 00 80 00 0E 00 00 00 00 00 00 00 00 00 00 00 01 00"),
		hexBytes("41 72 74 2D 4E 65 74 00 00 81 00 0E 01 01 00 00 00 00 00 00 00 00 00 00 00 01 00 01 7A 70 12 34 56 78"),
		hexBytes(artTodControlHex),
		hexBytes("41 72 74 2D 4E 65 74 00 00 83 00 0E 01 00 00 00 00 00 00 00 00 00 00 00 CC 01 18 7A 70 12 34 56 78 7A 70 00 00 00 01 00 01 00 00 00 20 00 60 00 04 4F"),
		hexBytes("41 72 74 2D 4E 65 74 00 00 84 00 0E 01 00 7A 70 12 34 56 78 00 30 00 F0 00 01 00 02 00 00 00 00 00 01 00 05"),
	}
	for _, fixture := range fixtures {
		for l := 0; l <= len(fixture); l++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic decoding truncated fixture (len %d): %v", l, r)
					}
				}()
				_, _ = Decode(fixture[:l])
			}()
		}
	}
}

func TestInvalidIDThrows(t *testing.T) {
	b := hexBytes("41 72 74 2D 4E 65 74 00 00 20 00 0E 02 00 00 00 00 00 00 00 00 00 00 00")
	b[0] = 0x00
	_, err := Decode(b)
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestUnknownOpCodeDecodesToUnknownRatherThanThrowing(t *testing.T) {
	pkt := append([]byte{}, IDBytes...)
	pkt = append(pkt, 0xFF, 0x7F) // OpCode LE = 0x7FFF
	pkt = append(pkt, 0xDE, 0xAD, 0xBE, 0xEF)
	decoded, err := Decode(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != KindUnknown || decoded.UnknownOpCode != 0x7FFF {
		t.Fatalf("%+v", decoded)
	}
	if !bytes.Equal(decoded.UnknownPayload, []byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Fatalf("payload=%v", decoded.UnknownPayload)
	}
	if got := Encode(decoded); !bytes.Equal(got, pkt) {
		t.Fatalf("got %X want %X", got, pkt)
	}
}

func TestBelow10BytesThrows(t *testing.T) {
	for l := 0; l < 10; l++ {
		b := make([]byte, l)
		_, err := Decode(b)
		if !errors.Is(err, ErrTooShort) {
			t.Fatalf("len=%d: expected ErrTooShort, got %v", l, err)
		}
	}
}

// --- Fuzz-style: 10k random-byte buffers through the top-level decoder, never crash ---

func TestFuzzRandomBytesNeverCrash(t *testing.T) {
	g := newSeededGenerator(0xA11CE)
	for i := 0; i < 10000; i++ {
		l := g.intn(301)
		b := randomBytes(l, g)
		_, _ = Decode(b)
	}
}

func TestFuzzValidIDRandomOpcodeAndPayloadNeverCrash(t *testing.T) {
	g := newSeededGenerator(0xB0B0)
	opcodes := []uint16{0x2000, 0x2100, 0x5000, 0x8000, 0x8100, 0x8200, 0x8300, 0x8400, 0x9700}
	for i := 0; i < 10000; i++ {
		pkt := append([]byte{}, IDBytes...)
		opCode := opcodes[g.intn(len(opcodes))]
		pkt = append(pkt, byte(opCode&0xFF), byte((opCode>>8)&0xFF))
		payloadLen := g.intn(261)
		pkt = append(pkt, randomBytes(payloadLen, g)...)
		_, _ = Decode(pkt)
	}
}

// --- Round-trip: seeded randomized valid packets per type, >=1000 iterations each ---

func TestRoundTripArtPoll(t *testing.T) {
	g := newSeededGenerator(1)
	for i := 0; i < 1000; i++ {
		p := randomArtPoll(g)
		pkt, err := Decode(Encode(Packet{Kind: KindPoll, Poll: p}))
		if err != nil || !reflect.DeepEqual(pkt.Poll, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.Poll, p, err)
		}
	}
}

func TestRoundTripArtPollReply(t *testing.T) {
	g := newSeededGenerator(2)
	for i := 0; i < 1000; i++ {
		p := randomArtPollReply(g)
		pkt, err := Decode(Encode(Packet{Kind: KindPollReply, PollReply: p}))
		if err != nil || !reflect.DeepEqual(pkt.PollReply, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.PollReply, p, err)
		}
	}
}

func TestRoundTripArtDmx(t *testing.T) {
	g := newSeededGenerator(3)
	for i := 0; i < 1000; i++ {
		p := randomArtDmx(g)
		pkt, err := Decode(Encode(Packet{Kind: KindDmx, Dmx: p}))
		if err != nil || !reflect.DeepEqual(pkt.Dmx, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.Dmx, p, err)
		}
	}
}

func TestRoundTripArtTodRequest(t *testing.T) {
	g := newSeededGenerator(4)
	for i := 0; i < 1000; i++ {
		p := randomArtTodRequest(g)
		pkt, err := Decode(Encode(Packet{Kind: KindTodRequest, TodRequest: p}))
		if err != nil || !reflect.DeepEqual(pkt.TodRequest, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.TodRequest, p, err)
		}
	}
}

func TestRoundTripArtTodData(t *testing.T) {
	g := newSeededGenerator(5)
	for i := 0; i < 1000; i++ {
		p := randomArtTodData(g)
		pkt, err := Decode(Encode(Packet{Kind: KindTodData, TodData: p}))
		if err != nil || !reflect.DeepEqual(pkt.TodData, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.TodData, p, err)
		}
	}
}

func TestRoundTripArtTodControl(t *testing.T) {
	g := newSeededGenerator(6)
	for i := 0; i < 1000; i++ {
		p := randomArtTodControl(g)
		pkt, err := Decode(Encode(Packet{Kind: KindTodControl, TodControl: p}))
		if err != nil || !reflect.DeepEqual(pkt.TodControl, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.TodControl, p, err)
		}
	}
}

func TestRoundTripArtRdm(t *testing.T) {
	g := newSeededGenerator(7)
	for i := 0; i < 1000; i++ {
		p := randomArtRdm(g)
		pkt, err := Decode(Encode(Packet{Kind: KindRdm, Rdm: p}))
		if err != nil || !reflect.DeepEqual(pkt.Rdm, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.Rdm, p, err)
		}
	}
}

func TestRoundTripArtRdmSub(t *testing.T) {
	g := newSeededGenerator(8)
	for i := 0; i < 1000; i++ {
		p := randomArtRdmSub(g)
		pkt, err := Decode(Encode(Packet{Kind: KindRdmSub, RdmSub: p}))
		if err != nil || !reflect.DeepEqual(pkt.RdmSub, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.RdmSub, p, err)
		}
	}
}

func TestRoundTripArtTimeCode(t *testing.T) {
	g := newSeededGenerator(9)
	for i := 0; i < 1000; i++ {
		p := randomArtTimeCode(g)
		pkt, err := Decode(Encode(Packet{Kind: KindTimeCode, TimeCode: p}))
		if err != nil || !reflect.DeepEqual(pkt.TimeCode, p) {
			t.Fatalf("i=%d got %+v want %+v err=%v", i, pkt.TimeCode, p, err)
		}
	}
}

// --- Endianness spot-checks ---

func TestOpCodeIsLittleEndian(t *testing.T) {
	b := Encode(Packet{Kind: KindPoll, Poll: Poll{}})
	if b[8] != 0x00 || b[9] != 0x20 {
		t.Fatalf("opcode bytes = %02X %02X", b[8], b[9])
	}
}

func TestArtPollEstaManIsBigEndian(t *testing.T) {
	b := Encode(Packet{Kind: KindPoll, Poll: Poll{EstaManufacturer: 0x1234}})
	if b[18] != 0x12 || b[19] != 0x34 {
		t.Fatalf("esta bytes = %02X %02X", b[18], b[19])
	}
}

func TestArtPollReplyEstaManIsLittleEndian(t *testing.T) {
	b := Encode(Packet{Kind: KindPollReply, PollReply: PollReply{EstaManufacturer: 0x1234}})
	if b[24] != 0x34 || b[25] != 0x12 {
		t.Fatalf("esta bytes = %02X %02X", b[24], b[25])
	}
}

func TestArtPollReplyPortIsLittleEndian(t *testing.T) {
	b := Encode(Packet{Kind: KindPollReply, PollReply: PollReply{Port: 0x1936}})
	if b[14] != 0x36 || b[15] != 0x19 {
		t.Fatalf("port bytes = %02X %02X", b[14], b[15])
	}
}
