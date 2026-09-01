package artnet

import (
	"bytes"
	"errors"
	"testing"
)

// nodeconfig_test.go has two kinds of coverage:
//
//  1. Structural round-trip / malformed-input tests (decode(encode(x)) == x,
//     TooShort rejection) — these catch decoder/encoder disagreement with
//     EACH OTHER, but nothing else: a self-consistent pair of bugs (encoder
//     and decoder sharing the same wrong bit mapping) sails straight through
//     them. That is exactly what happened before this file was corrected:
//     the old IpProgProgramIP=0x01/IpProgSetDefault=0x04 constants were
//     wrong, but every round-trip test here passed anyway, because encode
//     and decode both used the same wrong constants consistently. A real
//     Obsidian Netron EN4 (RDM-LOG19 bench session) is what caught it.
//
//  2. Byte-exact golden-fixture tests (below, clearly marked) — these
//     assert the literal wire bytes a real node receives/sent, DERIVED FROM
//     THE ART-NET 4 SPEC TEXT ITSELF (art-net.org.uk/downloads/art-net.pdf's
//     ArtAddress/ArtIpProg/ArtIpProgReply Packet Definition tables, cross-
//     checked against Wireshark's packet-artnet.c dissector), never from
//     this package's own encoder — that is the entire lesson of the
//     IpProgProgramIP/IpProgSetDefault bug: a fixture built the same way as
//     the code it's meant to catch just confirms the code's assumption
//     instead of testing it. One golden fixture is additionally cross-
//     checked against a real device's captured reply bytes (RDM-LOG19:
//     `...19360000a9fe6b010000`).

// --- ArtAddress -------------------------------------------------------------

func TestArtAddressRoundTrip(t *testing.T) {
	p := Address{
		ProtocolVersion: DefaultProtocolVersion,
		NetSwitch:       ProgramSwitch(5),
		BindIndex:       1,
		ShortName:       "EN4-Port1",
		LongName:        "Obsidian Netron EN4",
		SwIn:            [4]SwitchEntry{ProgramSwitch(0), ProgramSwitch(1), NoChangeSwitch, ProgramSwitch(3)},
		SwOut:           [4]SwitchEntry{ProgramSwitch(0), NoChangeSwitch, ProgramSwitch(2), ProgramSwitch(3)},
		SubSwitch:       ProgramSwitch(0),
		AcnPriority:     AcnPriorityNoChange,
		Command:         AcMergeHTP0,
	}
	enc := Encode(Packet{Kind: KindAddress, Address: p})
	if len(enc) != addressFixedLength {
		t.Fatalf("encoded length = %d, want %d", len(enc), addressFixedLength)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Kind != KindAddress {
		t.Fatalf("kind = %v", dec.Kind)
	}
	if dec.Address != p {
		t.Fatalf("got %+v want %+v", dec.Address, p)
	}
}

func TestArtAddressSwitchEntryBits(t *testing.T) {
	e := ProgramSwitch(9)
	if !e.ShouldProgram() {
		t.Fatal("expected ShouldProgram true")
	}
	if e.Value() != 9 {
		t.Fatalf("value = %d", e.Value())
	}
	if NoChangeSwitch.ShouldProgram() {
		t.Fatal("NoChangeSwitch should not request programming")
	}
}

func TestArtAddressTooShort(t *testing.T) {
	full := Encode(Packet{Kind: KindAddress, Address: Address{ProtocolVersion: DefaultProtocolVersion}})
	_, err := Decode(full[:len(full)-1])
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrTooShort) {
		t.Fatalf("err=%v", err)
	}
}

func TestArtAddressCommandConstants(t *testing.T) {
	// Sanity: LTP/HTP/protocol-select/clear-buffer families are 4 wide
	// (one per port) and non-overlapping, per the task brief's requirement
	// to cover "merge LTP/HTP, cancel merge, clear buffers, direction/
	// protocol flags".
	all := []AcCommand{
		AcNone, AcCancelMerge, AcLedNormal, AcLedMute, AcLedLocate, AcResetRxFlags,
		AcMergeLTP0, AcMergeLTP1, AcMergeLTP2, AcMergeLTP3,
		AcMergeHTP0, AcMergeHTP1, AcMergeHTP2, AcMergeHTP3,
		AcArtNetSel0, AcArtNetSel1, AcArtNetSel2, AcArtNetSel3,
		AcAcnSel0, AcAcnSel1, AcAcnSel2, AcAcnSel3,
		AcClearOp0, AcClearOp1, AcClearOp2, AcClearOp3,
	}
	seen := map[AcCommand]bool{}
	for _, c := range all {
		if seen[c] {
			t.Fatalf("duplicate AcCommand value 0x%02X", byte(c))
		}
		seen[c] = true
	}
}

// TestArtAddressCommandWireValuesFromSpec is a byte-exact golden-fixture
// test: every value here is copied from the Art-Net 4 spec PDF's
// ArtAddress Command table text (cross-checked against Wireshark's
// packet-artnet.c ARTNET_AC_* constants), NOT read back from this
// package's own AcCommand constants — asserting AcMergeHTP0==AcMergeHTP0
// would trivially pass no matter what wrong value the constant held, which
// is exactly how the previous nodeconfig_test.go missed the IpProg bug.
func TestArtAddressCommandWireValuesFromSpec(t *testing.T) {
	cases := []struct {
		name string
		got  AcCommand
		want byte
	}{
		{"AcMergeLtp0", AcMergeLTP0, 0x10},
		{"AcMergeHtp0", AcMergeHTP0, 0x50},   // was wrongly 0x20 before this fix
		{"AcArtNetSel0", AcArtNetSel0, 0x60}, // was wrongly 0x30 before this fix
		{"AcAcnSel0", AcAcnSel0, 0x70},       // was wrongly 0x40 before this fix
		{"AcClearOp0", AcClearOp0, 0x90},     // was wrongly 0x60 before this fix
	}
	for _, c := range cases {
		if byte(c.got) != c.want {
			t.Errorf("%s = 0x%02X, want 0x%02X (per Art-Net 4 spec ArtAddress Command table)", c.name, byte(c.got), c.want)
		}
	}
}

// TestArtAddressAcnPriorityWireOffset is a byte-exact golden-fixture test
// for offset 105 (the byte immediately before Command at offset 106),
// which the spec names AcnPriority, not the "SwVideo — deprecated,
// transmit 0" a previous reading of this file called it. Sending 0 there
// (Go's zero value, which is exactly what every ArtAddress-sending call in
// this codebase did before this fix) tells a real node to reprogram its
// sACN priority to 0.
func TestArtAddressAcnPriorityWireOffset(t *testing.T) {
	p := Address{ProtocolVersion: DefaultProtocolVersion, AcnPriority: 0x7B, Command: AcLedLocate}
	enc := Encode(Packet{Kind: KindAddress, Address: p})
	if got := enc[105]; got != 0x7B {
		t.Fatalf("byte at offset 105 = 0x%02X, want 0x7B (AcnPriority)", got)
	}
	if got := enc[106]; got != byte(AcLedLocate) {
		t.Fatalf("byte at offset 106 = 0x%02X, want Command 0x%02X", got, byte(AcLedLocate))
	}
}

// --- ArtInput ---------------------------------------------------------------

func TestArtInputRoundTrip(t *testing.T) {
	p := Input{
		ProtocolVersion: DefaultProtocolVersion,
		BindIndex:       2,
		NumPorts:        4,
		InputStates:     [4]InputDisable{0x00, 0x01, 0x00, 0x01},
	}
	enc := Encode(Packet{Kind: KindInput, Input: p})
	if len(enc) != inputFixedLength {
		t.Fatalf("encoded length = %d, want %d", len(enc), inputFixedLength)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Kind != KindInput || dec.Input != p {
		t.Fatalf("got %+v want %+v", dec.Input, p)
	}
	if !dec.Input.InputStates[1].Disabled() || dec.Input.InputStates[0].Disabled() {
		t.Fatalf("disabled bits wrong: %+v", dec.Input.InputStates)
	}
}

// TestArtInputWireLayout is a byte-exact golden-fixture test for the
// NumPorts field (Wireshark packet-artnet.c: hf_artnet_input_num_ports at
// offset 14-15, UINT16 BE) — a field a previous reading of this file
// omitted entirely, which would have misread every real ArtInput packet's
// Input[] bytes from the wrong offset.
func TestArtInputWireLayout(t *testing.T) {
	p := Input{
		ProtocolVersion: DefaultProtocolVersion,
		Filler1:         0x00,
		BindIndex:       0x03,
		NumPorts:        4,
		InputStates:     [4]InputDisable{0x00, 0xFF, 0x01, 0x00},
	}
	enc := Encode(Packet{Kind: KindInput, Input: p})
	if len(enc) != 20 {
		t.Fatalf("encoded length = %d, want 20", len(enc))
	}
	want := []byte{
		0x00,       // 12 Filler1
		0x03,       // 13 BindIndex
		0x00, 0x04, // 14-15 NumPorts BE
		0x00, 0xFF, 0x01, 0x00, // 16-19 Input[4]
	}
	got := enc[12:]
	if !bytes.Equal(got, want) {
		t.Fatalf("post-header bytes = % X, want % X", got, want)
	}
}

func TestArtInputDisabledIsWholeByteNonZero(t *testing.T) {
	// Wireshark's packet-artnet.c registers artnet.input.disabled with
	// FT_BOOLEAN mask 0xff — the whole byte is the disable flag, not a
	// single low bit.
	if InputDisable(0x01).Disabled() != true {
		t.Fatal("0x01 should be disabled")
	}
	if InputDisable(0x80).Disabled() != true {
		t.Fatal("0x80 should be disabled (whole-byte semantics, not bit 0)")
	}
	if InputDisable(0x00).Disabled() != false {
		t.Fatal("0x00 should not be disabled")
	}
}

func TestArtInputTooShort(t *testing.T) {
	full := Encode(Packet{Kind: KindInput, Input: Input{ProtocolVersion: DefaultProtocolVersion}})
	_, err := Decode(full[:len(full)-1])
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err=%v", err)
	}
}

// --- ArtIpProg / ArtIpProgReply ----------------------------------------------

func TestArtIpProgRoundTrip(t *testing.T) {
	p := IpProg{
		ProtocolVersion: DefaultProtocolVersion,
		Command:         IpProgEnable | IpProgProgramIP | IpProgProgramSubnetMask,
		ProgIP:          [4]byte{192, 168, 1, 50},
		ProgSubnetMask:  [4]byte{255, 255, 255, 0},
	}
	enc := Encode(Packet{Kind: KindIpProg, IpProg: p})
	if len(enc) != ipProgFixedLength {
		t.Fatalf("encoded length = %d, want %d", len(enc), ipProgFixedLength)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Kind != KindIpProg || dec.IpProg != p {
		t.Fatalf("got %+v want %+v", dec.IpProg, p)
	}
	if !dec.IpProg.Command.has(IpProgEnable) {
		t.Fatalf("expected Enable bit set: %+v", dec.IpProg.Command)
	}
}

// has is a tiny unexported test helper mirroring how callers will check
// individual command bits (IpProgCommand has no exported "has bit" method
// since bitwise & at the call site is idiomatic Go for a bitfield type;
// this wrapper just keeps the assertion above readable).
func (c IpProgCommand) has(bit IpProgCommand) bool { return c&bit != 0 }

// TestArtIpProgProgramIPAndMaskWireBytes is THE golden-fixture test for the
// bug this file exists to catch (RDM-LOG19). The expected command byte,
// 0x86, is derived directly from the Art-Net 4 spec's ArtIpProg Command bit
// table text — 0x80 (bit 7, "Enable any programming") | 0x04 (bit 2,
// "Program IP address") | 0x02 (bit 1, "Program subnet mask") — NOT from
// this package's IpProgEnable/IpProgProgramIP/IpProgProgramSubnetMask
// constants. The bench-captured bug packet was 0x83
// (0x80|0x02|0x01-under-the-old-wrong-mapping): this test's whole point is
// that 0x83 must NOT be what gets sent for a program-IP-and-mask request.
func TestArtIpProgProgramIPAndMaskWireBytes(t *testing.T) {
	p := IpProg{
		ProtocolVersion: DefaultProtocolVersion,
		Command:         IpProgEnable | IpProgProgramIP | IpProgProgramSubnetMask,
		ProgIP:          [4]byte{2, 11, 90, 12},
		ProgSubnetMask:  [4]byte{255, 255, 0, 0},
	}
	enc := Encode(Packet{Kind: KindIpProg, IpProg: p})
	const wantCommand = 0x86
	if got := enc[14]; got != wantCommand {
		t.Fatalf("Command byte (offset 14) = 0x%02X, want 0x%02X per Art-Net 4 spec (0x80 enable | 0x04 program-IP | 0x02 program-mask)", got, wantCommand)
	}
	if got := enc[14]; got == 0x83 {
		t.Fatalf("Command byte = 0x83 — this is RDM-LOG19's exact bug byte (enable|program-subnet-mask|program-PORT-to-0, never programs IP)")
	}
	wantIP := []byte{2, 11, 90, 12}
	if !bytes.Equal(enc[16:20], wantIP) {
		t.Fatalf("ProgIP bytes (offset 16-19) = % X, want % X", enc[16:20], wantIP)
	}
	wantSM := []byte{255, 255, 0, 0}
	if !bytes.Equal(enc[20:24], wantSM) {
		t.Fatalf("ProgSubnetMask bytes (offset 20-23) = % X, want % X", enc[20:24], wantSM)
	}
	// ProgPort (offset 24-25) is deprecated and must always be zero — this
	// package no longer exposes a Go field that could accidentally set it.
	if got := enc[24:26]; !bytes.Equal(got, []byte{0, 0}) {
		t.Fatalf("ProgPort bytes (offset 24-25) = % X, want zero (deprecated field, never programmed)", got)
	}
}

// TestArtIpProgSetDefaultWireBit is a byte-exact golden-fixture test
// proving "return to defaults" sets bit 3 (0x08), not bit 2 (0x04, which
// is "Program IP address" — the exact confusion in the pre-fix constants,
// where IpProgSetDefault sat on 0x04).
func TestArtIpProgSetDefaultWireBit(t *testing.T) {
	p := IpProg{ProtocolVersion: DefaultProtocolVersion, Command: IpProgEnable | IpProgSetDefault}
	enc := Encode(Packet{Kind: KindIpProg, IpProg: p})
	const wantCommand = 0x88
	if got := enc[14]; got != wantCommand {
		t.Fatalf("Command byte = 0x%02X, want 0x%02X (0x80 enable | 0x08 set-default)", got, wantCommand)
	}
	if got := enc[14]; got&0x04 != 0 {
		t.Fatalf("Command byte 0x%02X has bit 2 (Program IP) set — SetDefault must not collide with ProgramIP", got)
	}
}

// TestArtIpProgProgramGatewayWireBytes is a byte-exact golden-fixture test
// for gateway programming — previously believed not to exist in this
// package's wire-format reading at all ("ArtIpProg has no gateway field").
// Bit 4 (0x10, "Program default gateway") and the ProgGateway field's
// offset (26-29) are both derived from the spec text, not from this
// package's own encoder.
func TestArtIpProgProgramGatewayWireBytes(t *testing.T) {
	p := IpProg{
		ProtocolVersion: DefaultProtocolVersion,
		Command:         IpProgEnable | IpProgProgramGateway,
		ProgGateway:     [4]byte{2, 11, 90, 1},
	}
	enc := Encode(Packet{Kind: KindIpProg, IpProg: p})
	const wantCommand = 0x90
	if got := enc[14]; got != wantCommand {
		t.Fatalf("Command byte = 0x%02X, want 0x%02X (0x80 enable | 0x10 program-gateway)", got, wantCommand)
	}
	wantGW := []byte{2, 11, 90, 1}
	if !bytes.Equal(enc[26:30], wantGW) {
		t.Fatalf("ProgGateway bytes (offset 26-29) = % X, want % X", enc[26:30], wantGW)
	}
}

// TestArtIpProgDecodeReportsCorrectFlags is a decode-side golden-fixture
// test: constructs the raw Command byte by hand from spec-derived bit
// values (not via this package's constants) and asserts the decoder's
// derived flags — this is what internal/capture/nodeconfigdetail.go's
// Enable/EnableDHCP/SetDefault/ProgramGateway/ProgramSubnetMask/ProgramIP
// fields are built from, and what a bench log's `command=0x86 (...)` line
// reports. Before this fix, decoding the real bug packet 0x83 printed
// "programSubnetMask=true programIP=true" — a decode built on the same
// wrong bit mapping as the encoder, confirming the bug instead of catching
// it (the ArtRdm start-code episode's pattern, repeated).
func TestArtIpProgDecodeReportsCorrectFlags(t *testing.T) {
	// 0x86 = 0x80 (enable) | 0x04 (program IP) | 0x02 (program subnet mask),
	// bytes taken directly from the spec table, not from IpProgCommand consts.
	raw := IpProgCommand(0x86)
	if raw&IpProgEnable == 0 {
		t.Error("expected Enable bit set for 0x86")
	}
	if raw&IpProgProgramIP == 0 {
		t.Error("expected ProgramIP bit set for 0x86")
	}
	if raw&IpProgProgramSubnetMask == 0 {
		t.Error("expected ProgramSubnetMask bit set for 0x86")
	}
	if raw&IpProgSetDefault != 0 {
		t.Error("did not expect SetDefault bit set for 0x86")
	}
	if raw&IpProgProgramGateway != 0 {
		t.Error("did not expect ProgramGateway bit set for 0x86")
	}
	if raw&IpProgEnableDHCP != 0 {
		t.Error("did not expect EnableDHCP bit set for 0x86")
	}

	// The old bug byte, 0x83 = 0x80 (enable) | 0x02 (program subnet mask) |
	// 0x01 (program port — deprecated, spec bit 0). Confirm it does NOT
	// read as "program IP" under the corrected mapping (it did under the
	// old one, which is exactly why the bug shipped silently).
	bugByte := IpProgCommand(0x83)
	if bugByte&IpProgProgramIP != 0 {
		t.Error("0x83 must not decode as ProgramIP under the corrected bit mapping")
	}
}

func TestArtIpProgReplyRoundTrip(t *testing.T) {
	p := IpProgReply{
		ProtocolVersion: DefaultProtocolVersion,
		CurrentIP:       [4]byte{2, 11, 90, 2},
		CurrentSubnet:   [4]byte{255, 255, 0, 0},
		CurrentPort:     0x1936,
		Status:          IpProgReplyDHCPEnabled,
		CurrentGateway:  [4]byte{169, 254, 107, 1},
	}
	enc := Encode(Packet{Kind: KindIpProgReply, IpProgReply: p})
	if len(enc) != ipProgReplyFixedLength {
		t.Fatalf("encoded length = %d, want %d", len(enc), ipProgReplyFixedLength)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Kind != KindIpProgReply || dec.IpProgReply != p {
		t.Fatalf("got %+v want %+v", dec.IpProgReply, p)
	}
	if !dec.IpProgReply.Status.DHCPEnabled() {
		t.Fatal("expected DHCPEnabled true")
	}
}

// TestArtIpProgReplyDecodesRealBenchCapture is a byte-exact golden-fixture
// test built from RDM-LOG19's actual captured ArtIpProgReply, not a
// synthetic one this package generated. The bench log showed:
//
//	IN  ArtIpProgReply  currentIP=2.11.90.4  currentSubnet=255.255.0.0  currentPort=6454
//
// with trailing bytes `...19360000a9fe6b010000` (offsets 24-33). Decoded
// per the spec layout: ProgPort=0x1936 (6454), Status=0x00, Spare2=0x00,
// then a9:fe:6b:01 — 169.254.107.1, a link-local address — lands exactly
// on CurrentGateway (offset 28-31), then 00:00 on the final 2 spare bytes.
// A previous reading of this file had no gateway field at all and silently
// discarded these exact bytes as unread spare.
func TestArtIpProgReplyDecodesRealBenchCapture(t *testing.T) {
	// Build the full 34-byte ArtIpProgReply exactly as captured, using the
	// package's own header writer for ID/OpCode/ProtVer (those bytes are
	// not in dispute) and the literal captured tail for everything from
	// ProgPort onward.
	p := IpProgReply{ProtocolVersion: DefaultProtocolVersion}
	enc := Encode(Packet{Kind: KindIpProgReply, IpProgReply: p})
	// Overwrite offset 16 onward with the real bench bytes:
	// 16-19 CurrentIP, 20-23 CurrentSubnet, 24-25 ProgPort, 26 Status,
	// 27 Spare2, 28-31 CurrentGateway, 32-33 Spare.
	tail := []byte{
		2, 11, 90, 4, // CurrentIP
		255, 255, 0, 0, // CurrentSubnet
		0x19, 0x36, // ProgPort BE = 6454
		0x00,             // Status
		0x00,             // Spare2
		169, 254, 107, 1, // CurrentGateway
		0x00, 0x00, // Spare
	}
	copy(enc[16:], tail)

	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got := dec.IpProgReply
	wantIP := [4]byte{2, 11, 90, 4}
	wantSubnet := [4]byte{255, 255, 0, 0}
	wantGW := [4]byte{169, 254, 107, 1}
	if got.CurrentIP != wantIP {
		t.Errorf("CurrentIP = %v, want %v", got.CurrentIP, wantIP)
	}
	if got.CurrentSubnet != wantSubnet {
		t.Errorf("CurrentSubnet = %v, want %v", got.CurrentSubnet, wantSubnet)
	}
	if got.CurrentPort != 6454 {
		t.Errorf("CurrentPort = %d, want 6454", got.CurrentPort)
	}
	if got.Status.DHCPEnabled() {
		t.Error("expected DHCPEnabled false")
	}
	if got.CurrentGateway != wantGW {
		t.Errorf("CurrentGateway = %v, want %v (169.254.107.1, from the real captured trailing bytes)", got.CurrentGateway, wantGW)
	}
}

func TestArtIpProgTooShort(t *testing.T) {
	full := Encode(Packet{Kind: KindIpProg, IpProg: IpProg{ProtocolVersion: DefaultProtocolVersion}})
	_, err := Decode(full[:len(full)-1])
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err=%v", err)
	}
}

func TestArtIpProgReplyTooShort(t *testing.T) {
	full := Encode(Packet{Kind: KindIpProgReply, IpProgReply: IpProgReply{ProtocolVersion: DefaultProtocolVersion}})
	_, err := Decode(full[:len(full)-1])
	if !errors.Is(err, ErrTooShort) {
		t.Fatalf("err=%v", err)
	}
}

// --- property-style round trips across the seeded generator -----------------

func TestArtAddressPropertyRoundTrip(t *testing.T) {
	g := newSeededGenerator(0xA1)
	for i := 0; i < 200; i++ {
		p := Address{
			ProtocolVersion: g.uint16(),
			NetSwitch:       SwitchEntry(g.uint8()),
			BindIndex:       g.uint8(),
			ShortName:       randomFieldString(18, g),
			LongName:        randomFieldString(64, g),
			SubSwitch:       SwitchEntry(g.uint8()),
			AcnPriority:     g.uint8(),
			Command:         AcCommand(g.uint8()),
		}
		for j := 0; j < 4; j++ {
			p.SwIn[j] = SwitchEntry(g.uint8())
			p.SwOut[j] = SwitchEntry(g.uint8())
		}
		enc := Encode(Packet{Kind: KindAddress, Address: p})
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("iter %d: Decode: %v", i, err)
		}
		if dec.Address != p {
			t.Fatalf("iter %d: got %+v want %+v", i, dec.Address, p)
		}
	}
}

func TestArtIpProgPropertyRoundTrip(t *testing.T) {
	g := newSeededGenerator(0xB2)
	for i := 0; i < 200; i++ {
		p := IpProg{
			ProtocolVersion: g.uint16(),
			Command:         IpProgCommand(g.uint8()),
		}
		copy(p.ProgIP[:], randomBytes(4, g))
		copy(p.ProgSubnetMask[:], randomBytes(4, g))
		copy(p.ProgGateway[:], randomBytes(4, g))
		enc := Encode(Packet{Kind: KindIpProg, IpProg: p})
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("iter %d: Decode: %v", i, err)
		}
		if dec.IpProg != p {
			t.Fatalf("iter %d: got %+v want %+v", i, dec.IpProg, p)
		}
	}
}
