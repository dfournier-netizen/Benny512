package artnet

import "benny512/internal/bytesio"

// This file implements the three Art-Net 4 remote node-configuration
// packets referenced in lighting-protocols-reference_2026-08-03_0002.md
// §3.3's opcode table (ArtAddress 0x6000, ArtIpProg 0xf800/ArtIpProgReply
// 0xf900, ArtInput 0x7000).
//
// RE-VERIFIED 2026-09-01 (RDM-LOG19 bench session): a real Obsidian Netron
// EN4 confirmed the previous ArtIpProg Command bitfield was wrong — it had
// "Program IP Address" and "Set to defaults" on the wrong bits, so every
// ProgramIP call silently asked the node to reprogram its UDP port to 0
// instead of its address. The bit layout below (ArtIpProg/ArtIpProgReply,
// and the ArtAddress Command/AcCommand table) has been re-derived directly
// from the Art-Net 4 specification PDF (art-net.org.uk/downloads/art-net.pdf,
// "ArtIpProg Packet Definition" and "ArtAddress Packet Definition" tables),
// cross-checked against Wireshark's actively-maintained packet-artnet.c
// dissector (which independently encodes the same spec), and — for
// ArtIpProgReply specifically — against a real reply's captured trailing
// bytes (`...19360000a9fe6b010000`, decoded below). ArtAddress's SwIn/SwOut/
// NetSwitch/SubSwitch bit-7 write-enable convention was independently
// confirmed against the spec's verbatim field text ("This value is ignored
// unless bit 7 is high... Send 0x00 to reset this value to the physical
// switch setting").
//
// ArtInput's layout (NumPorts field, whole-byte "disabled" semantics) could
// NOT be confirmed against the primary spec PDF directly this session — the
// PDF's ArtInput section (page 75) falls past this fetch tooling's
// extraction cutoff every time it was tried. The ArtInput layout below is
// sourced from Wireshark's packet-artnet.c dissector alone (every other
// packet that dissector encoded was independently confirmed byte-for-byte
// against the primary PDF text, so it is trusted as a secondary source
// here), NOT from the primary spec text or a byte capture. Treat ArtInput as
// "best available reading, one source, unconfirmed on hardware" and verify
// it on the bench before relying on it — a wrong guess here costs a rig
// session exactly like the ArtIpProg bug did.

// --- ArtAddress (OpCode 0x6000) --------------------------------------------

// AcCommand is ArtAddress's Command byte (offset 106) — a closed set of
// single-shot actions, not a bitfield (report/spec convention: one command
// value per packet). Values below are re-derived directly from the Art-Net
// 4 spec PDF's ArtAddress Command table and cross-confirmed byte-for-byte
// against Wireshark's packet-artnet.c dissector's ARTNET_AC_* constants
// (both sources agree exactly). Corrects a previous best-recollection
// reading that put AcMergeHTP*/AcArtNetSel*/AcAcnSel*/AcClearOp* on the
// wrong values entirely (0x20/0x30/0x40/0x60 instead of the real
// 0x50/0x60/0x70/0x90) — those wrong values would have collided with other
// real commands (e.g. the old 0x30 for AcArtNetSel0 is actually
// AcDirectionRx0, "set port 0 to input").
//
// Per spec, only the port-0 command of each per-port family below is
// current — the spec marks the corresponding port-1..3 variants
// (Ac...1/Ac...2/Ac...3, same family, +1/+2/+3 from the port-0 value)
// "deprecated" (an Art-Net 4 node is not required to honor them for ports
// other than 0), but their wire values are still fully defined by the spec
// and kept here for a node confirmed to still support them.
type AcCommand byte

// ArtAddress Command values.
const (
	AcNone AcCommand = 0x00
	// AcCancelMerge cancels any active DMX-merge on the target port(s),
	// reverting to a single source.
	AcCancelMerge AcCommand = 0x01
	AcLedNormal   AcCommand = 0x02
	AcLedMute     AcCommand = 0x03
	AcLedLocate   AcCommand = 0x04
	// AcResetRxFlags clears the node's accumulated Rx/Tx error/status flags.
	AcResetRxFlags AcCommand = 0x05

	// AcMergeLTP0-3 set one output port's DMX-merge mode to LTP
	// (Latest Takes Precedence); AcMergeHTP0-3 (the default) sets HTP
	// (Highest Takes Precedence). Port index 0-3 selects which of the
	// node's four ports the command applies to (ports 1-3 deprecated).
	AcMergeLTP0 AcCommand = 0x10
	AcMergeLTP1 AcCommand = 0x11
	AcMergeLTP2 AcCommand = 0x12
	AcMergeLTP3 AcCommand = 0x13
	AcMergeHTP0 AcCommand = 0x50
	AcMergeHTP1 AcCommand = 0x51
	AcMergeHTP2 AcCommand = 0x52
	AcMergeHTP3 AcCommand = 0x53

	// AcArtNetSel0-3 select Art-Net as the protocol (DMX512 and RDM) for
	// one port (the spec's default); AcAcnSel0-3 select sACN (E1.31) for
	// DMX512 with RDM still carried over Art-Net — the "direction/protocol
	// flags" the task brief calls out (ports 1-3 deprecated). UNVERIFIED
	// whether any device in Dom's kit actually implements dual Art-Net/
	// sACN protocol selection via this mechanism (most single-protocol
	// nodes will simply NACK/ignore it).
	AcArtNetSel0 AcCommand = 0x60
	AcArtNetSel1 AcCommand = 0x61
	AcArtNetSel2 AcCommand = 0x62
	AcArtNetSel3 AcCommand = 0x63
	AcAcnSel0    AcCommand = 0x70
	AcAcnSel1    AcCommand = 0x71
	AcAcnSel2    AcCommand = 0x72
	AcAcnSel3    AcCommand = 0x73

	// AcClearOp0-3 clear (zero) the DMX output buffer for one port — the
	// task brief's "clear buffers" (ports 1-3 deprecated).
	AcClearOp0 AcCommand = 0x90
	AcClearOp1 AcCommand = 0x91
	AcClearOp2 AcCommand = 0x92
	AcClearOp3 AcCommand = 0x93
)

// SwitchEntry is one byte of ArtAddress's SwIn[]/SwOut[]/NetSwitch/
// SubSwitch fields: bit 7 set means "program this port/value", bits 0-3 (or
// 0-6 for NetSwitch) carry the new value; bit 7 clear means "leave
// unchanged" and the low bits are ignored. CONFIRMED against the Art-Net 4
// spec PDF's verbatim field text for all four fields: "This value is
// ignored unless bit 7 is high. i.e. to program a value 0x07, send the
// value as 0x87. Send 0x00 to reset this value to the physical switch
// setting."
type SwitchEntry byte

// Program builds a SwitchEntry that programs value (masked to 4 bits, or 7
// for NetSwitch — callers pass the already-masked value).
func ProgramSwitch(value byte) SwitchEntry { return SwitchEntry(value | 0x80) }

// NoChangeSwitch is the "leave this port/value unchanged" sentinel.
const NoChangeSwitch SwitchEntry = 0x00

// ShouldProgram reports whether bit 7 (the write-enable bit) is set.
func (s SwitchEntry) ShouldProgram() bool { return s&0x80 != 0 }

// Value returns the low-order value bits (mask depends on field — 0x0F for
// SwIn/SwOut/SubSwitch, 0x7F for NetSwitch; callers apply their own mask).
func (s SwitchEntry) Value() byte { return byte(s) &^ 0x80 }

// Address is ArtAddress (OpCode 0x6000) — remote node configuration: names,
// per-port universe assignment (SwIn/SwOut), Net/Sub-Net switch, and a
// single Command action per packet. Fixed 107-byte layout, confirmed
// against the Art-Net 4 spec PDF's ArtAddress Packet Definition table and
// cross-checked against Wireshark's packet-artnet.c dissector:
//
//	12    NetSwitch    SwitchEntry (7-bit Net value)
//	13    BindIndex    UINT8 (1-based; 0 or 1 both mean "the node's first/only bind")
//	14-31 ShortName    18-byte fixed string (same convention as ArtPollReply)
//	32-95 LongName     64-byte fixed string
//	96-99 SwIn[4]       SwitchEntry per port (Universe nibble)
//	100-103 SwOut[4]    SwitchEntry per port (Universe nibble)
//	104   SubSwitch     SwitchEntry (Sub-Net nibble)
//	105   AcnPriority   UINT8 — sACN (E1.31) priority, 0-200; 255 = no
//	                    change. NOT a deprecated/spare "SwVideo" byte as a
//	                    previous reading of this file had it: sending 0
//	                    here (the previous code's implicit zero-value
//	                    default) would ask a real node to reprogram its
//	                    sACN priority to 0 on every ArtAddress packet.
//	                    session.ArtNetSession now defaults this to 255.
//	106   Command       AcCommand
type Address struct {
	ProtocolVersion uint16
	NetSwitch       SwitchEntry
	BindIndex       byte
	ShortName       string
	LongName        string
	SwIn            [4]SwitchEntry
	SwOut           [4]SwitchEntry
	SubSwitch       SwitchEntry
	// AcnPriority is offset 105 — see the struct doc comment above.
	// AcnPriorityNoChange (255) leaves the node's current priority as-is.
	AcnPriority byte
	Command     AcCommand
}

// AcnPriorityNoChange is the "leave sACN priority unchanged" sentinel value
// for Address.AcnPriority, per the Art-Net 4 spec.
const AcnPriorityNoChange byte = 255

const addressFixedLength = 107

func decodeAddress(b []byte) (Address, error) {
	if len(b) < addressFixedLength {
		return Address{}, tooShort(opCodePtr(0x6000), addressFixedLength, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	netSwitch, err := r.ReadU8()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	bindIndex, err := r.ReadU8()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	shortName, err := r.ReadFixedString(18)
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	longName, err := r.ReadFixedString(64)
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	swIn, err := r.ReadBytes(4)
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	swOut, err := r.ReadBytes(4)
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	subSwitch, err := r.ReadU8()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	acnPriority, err := r.ReadU8()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	command, err := r.ReadU8()
	if err != nil {
		return Address{}, wrapReaderErr(0x6000, err)
	}
	out := Address{
		ProtocolVersion: protocolVersion, NetSwitch: SwitchEntry(netSwitch), BindIndex: bindIndex,
		ShortName: shortName, LongName: longName, SubSwitch: SwitchEntry(subSwitch),
		AcnPriority: acnPriority, Command: AcCommand(command),
	}
	for i := 0; i < 4; i++ {
		out.SwIn[i] = SwitchEntry(swIn[i])
		out.SwOut[i] = SwitchEntry(swOut[i])
	}
	return out, nil
}

func encodeAddress(p Address) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x6000, p.ProtocolVersion)
	w.WriteU8(byte(p.NetSwitch))
	w.WriteU8(p.BindIndex)
	w.WriteFixedString(p.ShortName, 18)
	w.WriteFixedString(p.LongName, 64)
	for i := 0; i < 4; i++ {
		w.WriteU8(byte(p.SwIn[i]))
	}
	for i := 0; i < 4; i++ {
		w.WriteU8(byte(p.SwOut[i]))
	}
	w.WriteU8(byte(p.SubSwitch))
	w.WriteU8(p.AcnPriority)
	w.WriteU8(byte(p.Command))
	return w.Bytes()
}

// --- ArtInput (OpCode 0x7000) ----------------------------------------------

// InputDisable is one byte of ArtInput's Input[] field. SOURCE: Wireshark's
// packet-artnet.c dissector registers this field with FT_BOOLEAN mask 0xff
// ("Disabled", artnet.input.disabled) — i.e. the whole byte is the disable
// flag (any non-zero value means disabled), not a single low bit. NOT
// independently confirmed against the primary spec PDF text this session
// (see file doc comment) — verify on hardware before relying on any value
// other than plain 0x00/0x01.
type InputDisable byte

// Disabled reports whether the byte is non-zero (see InputDisable's doc
// comment on why this isn't a single-bit test).
func (i InputDisable) Disabled() bool { return i != 0 }

// Input is ArtInput (OpCode 0x7000) — per-port DMX input enable/disable.
// Fixed 20-byte layout, per Wireshark's packet-artnet.c dissector (NOT
// independently confirmed against the primary spec PDF text this session —
// see file doc comment). Corrects a previous best-recollection reading that
// omitted the NumPorts field entirely, which would have misaligned every
// byte from Input[] onward and put a real node's byte 0 of Input[] where
// this package expected BindIndex's continuation:
//
//	12    Filler1     UINT8, spare, transmit 0
//	13    BindIndex   UINT8 (same convention as ArtAddress)
//	14-15 NumPorts    UINT16 BE — number of ports being controlled
//	16-19 Input[4]    InputDisable per port
type Input struct {
	ProtocolVersion uint16
	Filler1         byte
	BindIndex       byte
	NumPorts        uint16
	InputStates     [4]InputDisable
}

const inputFixedLength = 20

func decodeInput(b []byte) (Input, error) {
	if len(b) < inputFixedLength {
		return Input{}, tooShort(opCodePtr(0x7000), inputFixedLength, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	filler1, err := r.ReadU8()
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	bindIndex, err := r.ReadU8()
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	numPorts, err := r.ReadU16BE()
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	states, err := r.ReadBytes(4)
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	out := Input{ProtocolVersion: protocolVersion, Filler1: filler1, BindIndex: bindIndex, NumPorts: numPorts}
	for i := 0; i < 4; i++ {
		out.InputStates[i] = InputDisable(states[i])
	}
	return out, nil
}

func encodeInput(p Input) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x7000, p.ProtocolVersion)
	w.WriteU8(p.Filler1)
	w.WriteU8(p.BindIndex)
	w.WriteU16BE(p.NumPorts)
	for i := 0; i < 4; i++ {
		w.WriteU8(byte(p.InputStates[i]))
	}
	return w.Bytes()
}

// --- ArtIpProg (OpCode 0xf800) / ArtIpProgReply (OpCode 0xf900) -----------

// IpProgCommand is ArtIpProg's Command byte (offset 14) — a bitfield.
// RE-VERIFIED 2026-09-01 directly against the Art-Net 4 spec PDF's
// "ArtIpProg Packet Definition" Command field table, and cross-confirmed
// against Wireshark's packet-artnet.c dissector (hf_artnet_ip_prog_command_*
// bitmasks — both sources agree exactly):
//
//	bit 7 (0x80)  Enable any programming
//	bit 6 (0x40)  Enable DHCP (if set, ignore lower bits)
//	bit 5 (0x20)  Not used, transmit as zero
//	bit 4 (0x10)  Program default gateway
//	bit 3 (0x08)  Return all three parameters to default
//	bit 2 (0x04)  Program IP address
//	bit 1 (0x02)  Program subnet mask
//	bit 0 (0x01)  Program port (DEPRECATED — see IpProg's struct doc
//	              comment; this package does not expose a constant for
//	              this bit and never sets it)
//
// This corrects a previous best-recollection reading that had
// IpProgSetDefault on 0x04 (actually "Program IP Address") and
// IpProgProgramIP on 0x01 (actually the deprecated "Program Port" bit) —
// the exact defect a 2026-09-01 bench session caught live: every
// session.ArtNetSession.ProgramIP call built Command = 0x83
// (Enable|ProgramSubnetMask|the-bit-this-package-called-ProgramIP), which a
// real Obsidian Netron EN4 correctly read as "enable + program subnet mask
// + program UDP port to 0" — never touching the node's IP address, and
// asking it to zero its Art-Net port on every call.
type IpProgCommand byte

// ArtIpProg Command bits.
const (
	// IpProgEnable must be set for the packet to be treated as a
	// programming request; if clear, a conformant node treats the packet
	// as a status query ("If all bits are clear, this is an enquiry
	// only.") and MUST reply with ArtIpProgReply reflecting its current
	// configuration without changing anything.
	IpProgEnable IpProgCommand = 0x80
	// IpProgEnableDHCP requests the node switch to DHCP addressing.
	IpProgEnableDHCP IpProgCommand = 0x40
	// bit 5 (0x20) is spec "Not used, transmit as zero" — deliberately no
	// constant here; Filler-style bits are never set by this package.
	// IpProgProgramGateway requests ProgGateway be applied. Previously
	// believed not to exist in this build's wire-format reading (the old
	// doc comment warned ArtIpProg "has no gateway field") — the spec
	// confirms bit 4 and a dedicated ProgGateway field at offset 26-29 (see
	// IpProg's struct doc comment), and a real ArtIpProgReply capture's
	// trailing bytes independently confirm the field's position.
	IpProgProgramGateway IpProgCommand = 0x10
	// IpProgSetDefault requests the node reset all three parameters (IP,
	// subnet mask, gateway) to factory default.
	IpProgSetDefault IpProgCommand = 0x08
	// IpProgProgramIP requests ProgIP be applied.
	IpProgProgramIP IpProgCommand = 0x04
	// IpProgProgramSubnetMask requests ProgSubnetMask be applied.
	IpProgProgramSubnetMask IpProgCommand = 0x02
)

// IpProg is ArtIpProg (OpCode 0xf800) — remote IP/subnet/gateway/DHCP
// programming. Fixed 34-byte layout, re-verified 2026-09-01 directly
// against the Art-Net 4 spec PDF's ArtIpProg Packet Definition table and
// cross-checked against Wireshark's packet-artnet.c dissector:
//
//	12    Filler1      UINT8, spare
//	13    Filler2      UINT8, spare
//	14    Command      IpProgCommand bitfield
//	15    Filler4      UINT8, spare
//	16-19 ProgIP        [4]byte — new static IP (if IpProgProgramIP set)
//	20-23 ProgSubnetMask [4]byte — new subnet mask (if
//	                    IpProgProgramSubnetMask set)
//	24-25 ProgPort      UINT16 BE — (Deprecated) new UDP port. Never
//	                    programmed by this package (see IpProgCommand's
//	                    doc comment) — always transmitted as 0, and
//	                    IpProg no longer exposes a Go field for it: a
//	                    previous reading had this same offset labeled
//	                    "ProgIP"/bit assigned to it, which is exactly the
//	                    bug this file exists to not repeat.
//	26-29 ProgGateway   [4]byte — new default gateway (if
//	                    IpProgProgramGateway set)
//	30-33 Spare         [4]byte
type IpProg struct {
	ProtocolVersion uint16
	Filler1         byte
	Filler2         byte
	Command         IpProgCommand
	Filler4         byte
	ProgIP          [4]byte
	ProgSubnetMask  [4]byte
	ProgGateway     [4]byte
	Spare           [4]byte
}

const ipProgFixedLength = 34

func decodeIpProg(b []byte) (IpProg, error) {
	if len(b) < ipProgFixedLength {
		return IpProg{}, tooShort(opCodePtr(0xf800), ipProgFixedLength, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	filler1, err := r.ReadU8()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	filler2, err := r.ReadU8()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	command, err := r.ReadU8()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	filler4, err := r.ReadU8()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	progIP, err := r.ReadBytes(4)
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	progSM, err := r.ReadBytes(4)
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	if err := r.Skip(2); err != nil { // ProgPort (deprecated, not modeled)
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	progGW, err := r.ReadBytes(4)
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	spare, err := r.ReadBytes(4)
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	out := IpProg{
		ProtocolVersion: protocolVersion, Filler1: filler1, Filler2: filler2,
		Command: IpProgCommand(command), Filler4: filler4,
	}
	copy(out.ProgIP[:], progIP)
	copy(out.ProgSubnetMask[:], progSM)
	copy(out.ProgGateway[:], progGW)
	copy(out.Spare[:], spare)
	return out, nil
}

func encodeIpProg(p IpProg) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0xf800, p.ProtocolVersion)
	w.WriteU8(p.Filler1)
	w.WriteU8(p.Filler2)
	w.WriteU8(byte(p.Command))
	w.WriteU8(p.Filler4)
	w.WriteBytes(pad(p.ProgIP[:], 4))
	w.WriteBytes(pad(p.ProgSubnetMask[:], 4))
	w.WriteU16BE(0) // ProgPort — deprecated, always transmit 0 (never programmed)
	w.WriteBytes(pad(p.ProgGateway[:], 4))
	w.WriteBytes(pad(p.Spare[:], 4))
	return w.Bytes()
}

// IpProgReplyStatus is ArtIpProgReply's Status byte (offset 26). RE-VERIFIED
// 2026-09-01 against the spec PDF's Status field bit table (bit 7 = 0, bit 6
// = DHCP enabled, bits 5-0 = 0) and Wireshark's
// hf_artnet_ip_prog_reply_status_dhcp_enable mask (0x40) — matches the
// previous reading exactly, no change needed here.
type IpProgReplyStatus byte

// IpProgReplyDHCPEnabled reports whether bit 6 (DHCP active) is set.
const IpProgReplyDHCPEnabled IpProgReplyStatus = 0x40

// DHCPEnabled reports whether the DHCP-active bit is set.
func (s IpProgReplyStatus) DHCPEnabled() bool { return s&IpProgReplyDHCPEnabled != 0 }

// IpProgReply is ArtIpProgReply (OpCode 0xf900) — a node's current IP
// configuration, sent in response to ArtIpProg (whether or not
// IpProgEnable was set). Fixed 34-byte layout, re-verified 2026-09-01
// against the spec PDF's ArtIpProgReply Packet Definition table and
// Wireshark's packet-artnet.c dissector, and independently confirmed
// against a real reply's captured trailing bytes
// (`...19360000a9fe6b010000`, RDM-LOG19): ProgPort=0x1936 (6454),
// Status=0x00, Spare2=0x00, then a9:fe:6b:01 (169.254.107.1, a link-local
// address — a device with no gateway configured reporting one) lands
// exactly on CurrentGateway, then 00:00 on the final 2 spare bytes —
// confirming this offset layout byte-for-byte:
//
//	12-15 Filler1-4     4 spare bytes
//	16-19 CurrentIP      [4]byte — current IP
//	20-23 CurrentSubnet  [4]byte — current subnet mask
//	24-25 CurrentPort    UINT16 BE — (Deprecated) current UDP port
//	26    Status         IpProgReplyStatus bitfield
//	27    Spare2         1 spare byte
//	28-31 CurrentGateway [4]byte — current default gateway
//	32-33 Spare          2 spare bytes
//
// This corrects a previous reading that had no gateway field at all and
// silently discarded bytes 27-33 as unread spare — exactly the bytes that
// carry the node's actual gateway.
type IpProgReply struct {
	ProtocolVersion uint16
	CurrentIP       [4]byte
	CurrentSubnet   [4]byte
	CurrentPort     uint16
	Status          IpProgReplyStatus
	CurrentGateway  [4]byte
}

const ipProgReplyFixedLength = 34

func decodeIpProgReply(b []byte) (IpProgReply, error) {
	if len(b) < ipProgReplyFixedLength {
		return IpProgReply{}, tooShort(opCodePtr(0xf900), ipProgReplyFixedLength, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	if err := r.Skip(4); err != nil { // Filler1-4
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	progIP, err := r.ReadBytes(4)
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	progSM, err := r.ReadBytes(4)
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	progPort, err := r.ReadU16BE()
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	status, err := r.ReadU8()
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	if err := r.Skip(1); err != nil { // Spare2
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	progGW, err := r.ReadBytes(4)
	if err != nil {
		return IpProgReply{}, wrapReaderErr(0xf900, err)
	}
	// Final 2 spare bytes intentionally not read (nothing to do with
	// them); ReaderAt bounds are still enforced by the fixed-length guard
	// above.
	out := IpProgReply{
		ProtocolVersion: protocolVersion, CurrentPort: progPort, Status: IpProgReplyStatus(status),
	}
	copy(out.CurrentIP[:], progIP)
	copy(out.CurrentSubnet[:], progSM)
	copy(out.CurrentGateway[:], progGW)
	return out, nil
}

func encodeIpProgReply(p IpProgReply) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0xf900, p.ProtocolVersion)
	w.WriteBytes(make([]byte, 4)) // Filler1-4
	w.WriteBytes(pad(p.CurrentIP[:], 4))
	w.WriteBytes(pad(p.CurrentSubnet[:], 4))
	w.WriteU16BE(p.CurrentPort)
	w.WriteU8(byte(p.Status))
	w.WriteBytes(make([]byte, 1)) // Spare2
	w.WriteBytes(pad(p.CurrentGateway[:], 4))
	w.WriteBytes(make([]byte, 2)) // final Spare
	return w.Bytes()
}
