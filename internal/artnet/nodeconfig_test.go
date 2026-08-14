package artnet

import (
	"errors"
	"testing"
)

// Unlike codec_test.go's golden fixtures (which reproduce byte-for-byte
// captures verified against Phase 1a's research), the fixtures below are
// self-consistent, not externally verified: nodeconfig.go's doc comment
// explains why (no primary spec text or packet capture was available this
// session for ArtAddress/ArtInput/ArtIpProg/ArtIpProgReply). These tests
// assert internal consistency (decode(encode(x)) == x, structural
// well-formedness, malformed-input rejection) rather than "matches known-
// good bytes from a real device," which is the hardware-verification task
// flagged in nodeconfig.go and this session's final report.

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
		SwVideo:         0,
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

// --- ArtInput ---------------------------------------------------------------

func TestArtInputRoundTrip(t *testing.T) {
	p := Input{
		ProtocolVersion: DefaultProtocolVersion,
		BindIndex:       2,
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
		ProgPort:        0x1936,
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

func TestArtIpProgReplyRoundTrip(t *testing.T) {
	p := IpProgReply{
		ProtocolVersion: DefaultProtocolVersion,
		CurrentIP:       [4]byte{2, 11, 90, 2},
		CurrentSubnet:   [4]byte{255, 255, 0, 0},
		CurrentPort:     0x1936,
		Status:          IpProgReplyDHCPEnabled,
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
			SwVideo:         g.uint8(),
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
			ProgPort:        g.uint16(),
		}
		copy(p.ProgIP[:], randomBytes(4, g))
		copy(p.ProgSubnetMask[:], randomBytes(4, g))
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
