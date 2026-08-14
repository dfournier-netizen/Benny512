package artnet

import "benny512/internal/bytesio"

// This file implements the three Art-Net 4 remote node-configuration
// packets referenced in lighting-protocols-reference_2026-08-03_0002.md
// §3.3's opcode table (ArtAddress 0x6000, ArtIpProg 0xf800/ArtIpProgReply
// 0xf900, ArtInput 0x7000) but not previously modeled — that document only
// lists the opcodes and one-line purposes, with no byte-offset tables, and
// neither phase1a-wire-format-verification nor
// rdm-pids-sensors-research cover them either (they're Art-Net-only, no
// RDM equivalent). Layouts below are transcribed from general Art-Net 4
// spec knowledge (the "Node reconfiguration" packet family, which has been
// stable across Art-Net 3/4), NOT independently re-verified against the
// primary spec PDF or a byte capture this session — every struct below
// carries a field-level confidence note, and the package doc comment
// convention (DecodeError/tooShort/wrapReaderErr) is reused unchanged so
// these packets fail closed exactly like the other 10 in codec.go.
//
// TODO(hardware, 2026-08-14 session): verify all three packets' exact byte
// offsets and the ArtAddress/ArtIpProg Command bitfields against a real
// Netron EN4 (the device Dom's kit includes that's most likely to answer
// these) using a packet capture, before shipping remote node config to
// production. Until then, treat encode as "best-effort, likely correct
// skeleton" and decode as "accepts what a spec-conformant node should send,
// may reject a real node's actual reply if any offset below is wrong."

// --- ArtAddress (OpCode 0x6000) --------------------------------------------

// AcCommand is ArtAddress's Command byte (offset 106) — a closed set of
// single-shot actions, not a bitfield (report/spec convention: one command
// value per packet). WEAKLY CONFIRMED / UNVERIFIED: numeric values below
// match this package author's recollection of the Art-Net 4 "Table 15 —
// NodeReport / AcCommand" style enumeration used by the reference SDK and
// several open-source Art-Net stacks (OLA, libartnet), but were not
// re-derived from a primary spec fetch or byte capture this session.
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
	// node's four ports the command applies to.
	AcMergeLTP0 AcCommand = 0x10
	AcMergeLTP1 AcCommand = 0x11
	AcMergeLTP2 AcCommand = 0x12
	AcMergeLTP3 AcCommand = 0x13
	AcMergeHTP0 AcCommand = 0x20
	AcMergeHTP1 AcCommand = 0x21
	AcMergeHTP2 AcCommand = 0x22
	AcMergeHTP3 AcCommand = 0x23

	// AcArtNetSel0-3 select Art-Net as the input protocol for one port;
	// AcAcnSel0-3 select sACN (E1.31) — the "direction/protocol flags"
	// the task brief calls out. UNVERIFIED whether any device in Dom's kit
	// actually implements dual Art-Net/sACN protocol selection via this
	// mechanism (most single-protocol nodes will simply NACK/ignore it).
	AcArtNetSel0 AcCommand = 0x30
	AcArtNetSel1 AcCommand = 0x31
	AcArtNetSel2 AcCommand = 0x32
	AcArtNetSel3 AcCommand = 0x33
	AcAcnSel0    AcCommand = 0x40
	AcAcnSel1    AcCommand = 0x41
	AcAcnSel2    AcCommand = 0x42
	AcAcnSel3    AcCommand = 0x43

	// AcClearOp0-3 clear (zero) the DMX output buffer for one port — the
	// task brief's "clear buffers".
	AcClearOp0 AcCommand = 0x60
	AcClearOp1 AcCommand = 0x61
	AcClearOp2 AcCommand = 0x62
	AcClearOp3 AcCommand = 0x63
)

// SwitchEntry is one byte of ArtAddress's SwIn[]/SwOut[]/NetSwitch/
// SubSwitch fields: bit 7 set means "program this port/value", bits 0-3 (or
// 0-6 for NetSwitch) carry the new value; bit 7 clear means "leave
// unchanged" and the low bits are ignored. UNVERIFIED against a primary
// source this session (the bit7-as-write-enable convention is this
// package's best recollection of how ArtAddress avoids clobbering ports the
// controller doesn't intend to touch), but it is the only sane way to let a
// single ArtAddress packet update a subset of a node's ports.
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
// single Command action per packet. Fixed 107-byte layout (UNVERIFIED
// total length this session — matches this package's recollection, not an
// independently re-measured capture):
//
//	12    NetSwitch   SwitchEntry (7-bit Net value)
//	13    BindIndex   UINT8 (1-based; 0 or 1 both mean "the node's first/only bind")
//	14-31 ShortName   18-byte fixed string (same convention as ArtPollReply)
//	32-95 LongName    64-byte fixed string
//	96-99 SwIn[4]      SwitchEntry per port (Universe nibble)
//	100-103 SwOut[4]   SwitchEntry per port (Universe nibble)
//	104   SubSwitch    SwitchEntry (Sub-Net nibble)
//	105   SwVideo      UINT8 — deprecated, transmit 0
//	106   Command      AcCommand
type Address struct {
	ProtocolVersion uint16
	NetSwitch       SwitchEntry
	BindIndex       byte
	ShortName       string
	LongName        string
	SwIn            [4]SwitchEntry
	SwOut           [4]SwitchEntry
	SubSwitch       SwitchEntry
	SwVideo         byte
	Command         AcCommand
}

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
	swVideo, err := r.ReadU8()
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
		SwVideo: swVideo, Command: AcCommand(command),
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
	w.WriteU8(p.SwVideo)
	w.WriteU8(byte(p.Command))
	return w.Bytes()
}

// --- ArtInput (OpCode 0x7000) ----------------------------------------------

// InputDisable is one byte of ArtInput's Input[] field: bit 0 set disables
// DMX input on that port, other bits reserved (transmit 0, ignore on
// receive). UNVERIFIED against a primary source this session.
type InputDisable byte

// Disabled reports whether bit 0 is set.
func (i InputDisable) Disabled() bool { return i&0x01 != 0 }

// Input is ArtInput (OpCode 0x7000) — per-port DMX input enable/disable.
// Fixed 18-byte layout (UNVERIFIED length/offsets this session):
//
//	12    Filler1    UINT8, spare, transmit 0
//	13    BindIndex  UINT8 (same convention as ArtAddress)
//	14-17 Input[4]   InputDisable per port
type Input struct {
	ProtocolVersion uint16
	Filler1         byte
	BindIndex       byte
	InputStates     [4]InputDisable
}

const inputFixedLength = 18

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
	states, err := r.ReadBytes(4)
	if err != nil {
		return Input{}, wrapReaderErr(0x7000, err)
	}
	out := Input{ProtocolVersion: protocolVersion, Filler1: filler1, BindIndex: bindIndex}
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
	for i := 0; i < 4; i++ {
		w.WriteU8(byte(p.InputStates[i]))
	}
	return w.Bytes()
}

// --- ArtIpProg (OpCode 0xf800) / ArtIpProgReply (OpCode 0xf900) -----------

// IpProgCommand is ArtIpProg's Command byte (offset 14) — a bitfield.
// UNVERIFIED bit assignments this session (recollection of the Art-Net 4
// "ArtIpProg Packet Definition" Command field); confirm against hardware
// before relying on any bit not exercised by a successful round-trip
// against a real node.
type IpProgCommand byte

// ArtIpProg Command bits.
const (
	// IpProgEnable must be set for the packet to be treated as a
	// programming request; if clear, a conformant node treats the packet
	// as a status query and MUST reply with ArtIpProgReply reflecting its
	// current configuration without changing anything.
	IpProgEnable IpProgCommand = 0x80
	// IpProgEnableDHCP requests the node switch to DHCP addressing.
	IpProgEnableDHCP IpProgCommand = 0x40
	// IpProgSetDefault requests the node reset its IP configuration to
	// factory default, ignoring every other bit/field in the packet.
	IpProgSetDefault IpProgCommand = 0x04
	// IpProgProgramSubnetMask requests ProgSM be applied.
	IpProgProgramSubnetMask IpProgCommand = 0x02
	// IpProgProgramIP requests ProgIP be applied.
	IpProgProgramIP IpProgCommand = 0x01
)

// IpProg is ArtIpProg (OpCode 0xf800) — remote IP/subnet/gateway/DHCP
// programming. Fixed 34-byte layout (UNVERIFIED this session):
//
//	12    Filler1   UINT8, spare
//	13    Filler2   UINT8, spare
//	14    Command   IpProgCommand bitfield
//	15    Filler4   UINT8, spare
//	16-19 ProgIP    [4]byte — new static IP (if IpProgProgramIP set)
//	20-23 ProgSM    [4]byte — new subnet mask (if IpProgProgramSubnetMask set)
//	24-25 ProgPort  UINT16 BE — new UDP port (rarely implemented; most nodes
//	                ignore this and stay on 6454)
//	26-33 Spare     [8]byte
//
// Gateway programming: UNVERIFIED whether base Art-Net 4 ArtIpProg carries
// a dedicated default-gateway field at all — this struct has none; several
// real nodes (including, per its own manual, likely the Netron EN4) only
// expose gateway configuration via their web UI or E1.37-2 RDM PIDs (see
// package rdm's PIDIPv4DefaultRoute) rather than ArtIpProg. Treat
// session.ArtNetSession.ProgramIP's "gateway" parameter as best-effort
// until confirmed.
type IpProg struct {
	ProtocolVersion uint16
	Filler1         byte
	Filler2         byte
	Command         IpProgCommand
	Filler4         byte
	ProgIP          [4]byte
	ProgSubnetMask  [4]byte
	ProgPort        uint16
	Spare           [8]byte
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
	progPort, err := r.ReadU16BE()
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	spare, err := r.ReadBytes(8)
	if err != nil {
		return IpProg{}, wrapReaderErr(0xf800, err)
	}
	out := IpProg{
		ProtocolVersion: protocolVersion, Filler1: filler1, Filler2: filler2,
		Command: IpProgCommand(command), Filler4: filler4, ProgPort: progPort,
	}
	copy(out.ProgIP[:], progIP)
	copy(out.ProgSubnetMask[:], progSM)
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
	w.WriteU16BE(p.ProgPort)
	w.WriteBytes(pad(p.Spare[:], 8))
	return w.Bytes()
}

// IpProgReplyStatus is ArtIpProgReply's Status byte (offset 26) —
// UNVERIFIED bit assignment this session.
type IpProgReplyStatus byte

// IpProgReplyDHCPEnabled reports whether bit 6 (DHCP active) is set.
const IpProgReplyDHCPEnabled IpProgReplyStatus = 0x40

// DHCPEnabled reports whether the DHCP-active bit is set.
func (s IpProgReplyStatus) DHCPEnabled() bool { return s&IpProgReplyDHCPEnabled != 0 }

// IpProgReply is ArtIpProgReply (OpCode 0xf900) — a node's current IP
// configuration, sent in response to ArtIpProg (whether or not
// IpProgEnable was set). Fixed 34-byte layout (UNVERIFIED this session,
// mirrors ArtIpProg's shape per this package's recollection):
//
//	12-15 Filler1-4  4 spare bytes
//	16-19 ProgIP     [4]byte — current IP
//	20-23 ProgSM     [4]byte — current subnet mask
//	24-25 ProgPort   UINT16 BE — current UDP port
//	26    Status     IpProgReplyStatus bitfield
//	27-33 Spare      7 spare bytes
type IpProgReply struct {
	ProtocolVersion uint16
	CurrentIP       [4]byte
	CurrentSubnet   [4]byte
	CurrentPort     uint16
	Status          IpProgReplyStatus
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
	// Remaining 7 spare bytes intentionally not read (nothing to do with
	// them); ReaderAt bounds are still enforced by the fixed-length guard
	// above.
	out := IpProgReply{
		ProtocolVersion: protocolVersion, CurrentPort: progPort, Status: IpProgReplyStatus(status),
	}
	copy(out.CurrentIP[:], progIP)
	copy(out.CurrentSubnet[:], progSM)
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
	w.WriteBytes(make([]byte, 7)) // Spare
	return w.Bytes()
}
