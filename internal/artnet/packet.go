// Package artnet implements Art-Net 4 UDP packet wire codecs for the 10
// packet types used by Benny512 Layer 1.
package artnet

import (
	"errors"
	"fmt"

	"benny512/internal/rdm"
)

// DefaultProtocolVersion is the current Art-Net protocol version (14).
const DefaultProtocolVersion uint16 = 14

// ErrPortAddressOutOfRange is returned by NewPortAddress when a component
// exceeds its bit width.
var ErrPortAddressOutOfRange = errors.New("artnet: port address component out of range")

// PortAddress is Art-Net's 15-bit Port-Address: Net (bits 14-8, 0-127) :
// Sub-Net (bits 7-4, 0-15) : Universe (bits 3-0, 0-15).
type PortAddress struct {
	Net      byte
	SubNet   byte
	Universe byte
}

// NewPortAddress validates each component is within its bit width.
func NewPortAddress(net, subNet, universe byte) (PortAddress, error) {
	if net > 0x7F {
		return PortAddress{}, fmt.Errorf("%w: net=%d", ErrPortAddressOutOfRange, net)
	}
	if subNet > 0x0F {
		return PortAddress{}, fmt.Errorf("%w: subNet=%d", ErrPortAddressOutOfRange, subNet)
	}
	if universe > 0x0F {
		return PortAddress{}, fmt.Errorf("%w: universe=%d", ErrPortAddressOutOfRange, universe)
	}
	return PortAddress{Net: net, SubNet: subNet, Universe: universe}, nil
}

// MaskPortAddress masks each component into range, for decoding already-
// on-the-wire raw bytes (e.g. ArtDmx's separate Net/SubUni fields) where
// out-of-range bits simply shouldn't be set by a conformant sender.
func MaskPortAddress(net, subNet, universe byte) PortAddress {
	return PortAddress{Net: net & 0x7F, SubNet: subNet & 0x0F, Universe: universe & 0x0F}
}

// PortAddressFromRaw decodes a combined 15-bit raw value.
func PortAddressFromRaw(raw uint16) (PortAddress, error) {
	if raw > 0x7FFF {
		return PortAddress{}, fmt.Errorf("%w: raw=%d", ErrPortAddressOutOfRange, raw)
	}
	return PortAddress{
		Net:      byte((raw >> 8) & 0x7F),
		SubNet:   byte((raw >> 4) & 0x0F),
		Universe: byte(raw & 0x0F),
	}, nil
}

// RawValue returns the combined 15-bit value: (net<<8) | (subNet<<4) | universe.
func (p PortAddress) RawValue() uint16 {
	return uint16(p.Net)<<8 | uint16(p.SubNet)<<4 | uint16(p.Universe)
}

// SubUni is the low byte of Port-Address as transmitted in ArtDmx's SubUni
// field: (subNet<<4) | universe.
func (p PortAddress) SubUni() byte { return (p.SubNet << 4) | p.Universe }

// Poll is ArtPoll (OpCode 0x2000). Receivers must accept any packet >= 14
// bytes; missing trailing fields (offsets 14-21) default to zero.
type Poll struct {
	ProtocolVersion uint16
	// Flags: bit 1 = send ArtPollReply on state change. Named "Flags" in the
	// current spec (older/informal name was "TalkToMe").
	Flags                   byte
	DiagPriority            byte
	TargetPortAddressTop    uint16
	TargetPortAddressBottom uint16
	EstaManufacturer        uint16
	Oem                     uint16
}

// PollReply is ArtPollReply (OpCode 0x2100). Has no ProtVer bytes — its
// header is ID+OpCode only (10 bytes), then packet-specific fields start
// immediately. Minimum accepted length is 207 bytes (through offset 206 /
// MAC); fields from BindIP (offset 207) onward default to zero if the
// packet is shorter.
type PollReply struct {
	IPAddress [4]byte
	// Port is always 0x1936 in practice; transmitted little-endian (unusual
	// — same as OpCode, unlike almost everything else in this packet).
	Port        uint16
	VersInfoHi  byte
	VersInfoLo  byte
	NetSwitch   byte
	SubSwitch   byte
	Oem         uint16
	UbeaVersion byte
	Status1     byte
	// EstaManufacturer is transmitted low-byte-first (LE) — opposite of the
	// same-named field in Poll, which is BE.
	EstaManufacturer uint16
	ShortName        string
	LongName         string
	NodeReport       string
	NumPorts         uint16
	PortTypes        [4]byte
	GoodInput        [4]byte
	GoodOutputA      [4]byte
	SwIn             [4]byte
	SwOut            [4]byte
	AcnPriority      byte
	SwMacro          byte
	SwRemote         byte
	Spare            [3]byte
	Style            byte
	MAC              [6]byte
	// --- 207-byte "minimum accepted" boundary is here ---
	BindIP                [4]byte
	BindIndex             byte
	Status2               byte
	GoodOutputB           [4]byte
	Status3               byte
	DefaultRespUID        [6]byte
	User                  uint16
	RefreshRate           uint16
	BackgroundQueuePolicy byte
	Filler                [10]byte
}

// DefaultPollReply returns a PollReply with Port defaulted to 0x1936 and all
// other fields zeroed, matching the reference implementation's default
// initializer.
func DefaultPollReply() PollReply {
	return PollReply{Port: 0x1936}
}

// Dmx is ArtDmx (OpCode 0x5000). Data length "should" be even per spec
// wording (a recommendation, not a hard MUST) — Encode pads with one
// trailing zero byte to make the wire length even; Decode accepts odd
// lengths as-is (lenient).
type Dmx struct {
	ProtocolVersion uint16
	Sequence        byte
	Physical        byte
	// SubUni is the low byte of Port-Address: (subNet<<4) | universe.
	SubUni byte
	// Net is the high 7 bits of Port-Address.
	Net  byte
	Data []byte
}

// PortAddress reconstructs the packet's target Port-Address from SubUni/Net.
func (d Dmx) PortAddress() PortAddress {
	return MaskPortAddress(d.Net, (d.SubUni>>4)&0x0F, d.SubUni&0x0F)
}

// TodRequest is ArtTodRequest (OpCode 0x8000).
type TodRequest struct {
	ProtocolVersion uint16
	Filler1         byte
	Filler2         byte
	Spare           [7]byte
	Net             byte
	Command         byte // 0x00 = TodFull
	// Address holds low bytes of Port-Address per target universe (max 32
	// entries per spec; not enforced on encode, buffer-bounds-checked on decode).
	Address []byte
}

// TodData is ArtTodData (OpCode 0x8100).
//
// Open question: Art-Net 4 added a BindIndex field somewhere in this packet,
// but neither the primary spec fetch nor OLA's actively-maintained
// ArtNetPackets.h confirm its exact byte offset. This type uses the
// OLA-confirmed / Art-Net-3-baseline layout (RdmVer, Port, then 7 raw
// reserved bytes, Net, ...) and exposes those 7 bytes verbatim as Spare
// rather than guessing a BindIndex offset.
type TodData struct {
	ProtocolVersion uint16
	RdmVersion      byte
	Port            byte
	// Spare is offsets 14-20 (7 bytes). BindIndex's position within this
	// range is UNVERIFIED — see type doc comment.
	Spare           [7]byte
	Net             byte
	CommandResponse byte // 0x00 = TodFull, 0xFF = TodNak
	Address         byte
	UidTotal        uint16
	BlockCount      byte
	Tod             []rdm.UID
}

// TodControl is ArtTodControl (OpCode 0x8200), 24 bytes fixed.
type TodControl struct {
	ProtocolVersion uint16
	Filler1         byte
	Filler2         byte
	Spare           [7]byte
	Net             byte
	Command         byte // 0x00 = AtcNone, 0x01 = AtcFlush
	Address         byte
}

// Rdm is ArtRdm (OpCode 0x8300).
//
// RdmData is the wire-format RDM message *excluding* the leading 0xCC RDM
// start code (RDM slot 0) — per the Art-Net 4 spec, ArtRdm's RdmPacket
// field begins at the sub-start code 0x01 (SC_SUB_MESSAGE, RDM slot 1),
// not at slot 0. An earlier revision of this project got this wrong
// (RdmData held the full RDM message starting at 0xCC, and that
// 0xCC-inclusive framing was sent on the wire) — that is a real,
// root-caused bug that was bench-confirmed silently dropping every
// directed ArtRdm GET against a real Obsidian/Elation-family gateway
// (discovery worked — ArtTodData is unaffected by this field — but every
// GET/SET went unanswered, no NACK, because the node read 0xCC where it
// expected the sub-start code 0x01 and discarded the packet).
//
// CORRECTION, confirming evidence: OLA (OpenLightingProject/ola),
// plugins/artnet/ArtNetNode.cpp, in its ArtRdm receive path, verbatim:
//
//	// The Art-Net packet does not include the RDM start code. Prepend that.
//	RDMFrame rdm_response(packet.data, rdm_length, RDMFrame::Options(true));
//
// OLA's own artnet_rdm_s wire struct ends in a bare
// `uint8_t data[ARTNET_MAX_RDM_DATA]` with no start-code byte accounted
// for, consistent with that comment.
//
// Why this project's own wire-format verification missed it:
// phase1a-wire-format-verification's §2.8 offset table (ArtRdm offsets
// 0-23) has no row for this trailing RdmPacket field at all — the
// requirement lives in the spec's prose, not in an offset table, so the
// extraction pass that built that table never captured it. Its §3.8
// "golden" ArtRdm fixture was then built 0xCC-inclusive from the same
// wrong assumption, and this package's tests matched that fixture
// byte-for-byte. Both have since been corrected here. Do NOT "fix" RdmData
// back to 0xCC-inclusive to make it match that old doc/fixture — the doc
// was wrong, not this code.
//
// EncodeRdmPacket strips the leading 0xCC on encode by default (see its
// legacyStartCode parameter — plumbed from cmd/benny512's
// --legacy-rdm-startcode flag via RDMConfig.LegacyRdmStartCode — an
// explicit escape hatch for a node that turns out to actually want the old,
// spec-incorrect 0xCC-inclusive framing; default is spec-correct/off).
// DecodedRDMMessage tolerates BOTH forms on receive regardless of that
// flag: some real nodes in the wild send the 0xCC anyway despite the spec
// excluding it, and a decoder that rejected those would just trade one
// silent failure for another.
type Rdm struct {
	ProtocolVersion uint16
	RdmVersion      byte // 0x01
	Filler2         byte
	Spare           [7]byte
	Net             byte
	Command         byte // 0x00 = ArProcess
	Address         byte
	// RdmData is the RdmPacket field: wire-format RDM message bytes starting
	// at the sub-start code (0x01), per the type doc comment above. Build/
	// read it via EncodeRdmPacket / DecodedRDMMessage rather than by hand.
	RdmData []byte
}

// DecodedRDMMessage decodes RdmData as a full RDM message via rdm.Decode.
//
// Tolerant of both wire forms: if RdmData already begins with the RDM
// start code (0xCC — some nodes include it despite the Art-Net spec
// excluding it), it is decoded as-is; otherwise the 0xCC is prepended
// first, since rdm.Decode always expects a full RDM message starting at
// slot 0. RdmData itself is never mutated — a new slice is allocated when
// prepending is needed.
func (p Rdm) DecodedRDMMessage() (rdm.Message, error) {
	if len(p.RdmData) > 0 && p.RdmData[0] == rdm.StartCode {
		return rdm.Decode(p.RdmData)
	}
	buf := make([]byte, 0, len(p.RdmData)+1)
	buf = append(buf, rdm.StartCode)
	buf = append(buf, p.RdmData...)
	return rdm.Decode(buf)
}

// EncodeRdmPacket builds an Rdm packet by encoding message via rdm.Encode.
//
// rdm.Encode always returns a message starting with the 0xCC start code.
// When legacyStartCode is false (spec-correct; the normal/default choice —
// see the Rdm type's doc comment for why), that leading byte is stripped
// before storing into RdmData, so the ArtRdm payload begins at the RDM
// sub-start code as the spec requires. When legacyStartCode is true, the
// 0xCC is left in place, reproducing this project's pre-fix framing for a
// node bench-confirmed to actually expect it.
func EncodeRdmPacket(message rdm.Message, protocolVersion uint16, net, address byte, legacyStartCode bool) Rdm {
	data := rdm.Encode(message)
	if !legacyStartCode {
		data = data[1:]
	}
	return Rdm{
		ProtocolVersion: protocolVersion,
		RdmVersion:      1,
		Filler2:         0,
		Net:             net,
		Command:         0,
		Address:         address,
		RdmData:         data,
	}
}

// RdmSub is ArtRdmSub (OpCode 0x8400). Endianness of ParameterID/SubDevice/
// Data is WEAKLY CONFIRMED (inferred as BE by consistency with RDM's own
// convention) since the source spec text doesn't state byte order explicitly.
type RdmSub struct {
	ProtocolVersion uint16
	RdmVersion      byte
	Filler2         byte
	// UID is the target RDM device (offsets 14-19, BE).
	UID          rdm.UID
	Spare1       byte
	CommandClass byte
	ParameterID  uint16
	// SubDevice is the first sub-device (RDM convention: 0 = root, 1 = first).
	SubDevice uint16
	Spare2to5 [4]byte
	// Data's length (SubCount, offsets 26-27) is derived on encode, not
	// stored separately, to keep it always consistent with the payload.
	Data []uint16
}

// TimeCode is ArtTimeCode (OpCode 0x9700), 19 bytes fixed. StreamId is at
// offset 13 — the reference doc's "Filler2" is actually StreamId, and there
// is only one filler byte, not two.
type TimeCode struct {
	ProtocolVersion uint16
	Filler1         byte
	StreamId        byte
	Frames          byte
	Seconds         byte
	Minutes         byte
	Hours           byte
	Type            byte
}

// PacketKind tags which concrete packet type a decoded Packet holds.
type PacketKind int

// PacketKind values.
const (
	KindPoll PacketKind = iota
	KindPollReply
	KindDmx
	KindTodRequest
	KindTodData
	KindTodControl
	KindRdm
	KindRdmSub
	KindTimeCode
	KindAddress
	KindInput
	KindIpProg
	KindIpProgReply
	KindUnknown
)

// Packet is a decoded Art-Net packet: a tagged union over the 10 known
// packet types plus Unknown for unrecognized OpCodes. Exactly one of the
// typed fields is populated, selected by Kind; use a type switch on
// AsAny(), or check Kind directly.
type Packet struct {
	Kind PacketKind

	Poll        Poll
	PollReply   PollReply
	Dmx         Dmx
	TodRequest  TodRequest
	TodData     TodData
	TodControl  TodControl
	Rdm         Rdm
	RdmSub      RdmSub
	TimeCode    TimeCode
	Address     Address
	Input       Input
	IpProg      IpProg
	IpProgReply IpProgReply

	// Unknown holds the OpCode and payload (every byte after the 10-byte
	// ID+OpCode header) when Kind == KindUnknown. Payload may or may not
	// have included ProtVer bytes in the original packet's own layout — we
	// can't know, so nothing is assumed.
	UnknownOpCode  uint16
	UnknownPayload []byte
}

// AsAny returns the active field as an interface value, convenient for a
// type switch. Returns the OpCode as a uint16 wrapped struct for Unknown.
type Unknown struct {
	OpCode  uint16
	Payload []byte
}

// AsAny returns the packet's active payload as an any for use in a type
// switch (Poll, PollReply, Dmx, TodRequest, TodData, TodControl, Rdm,
// RdmSub, TimeCode, or Unknown).
func (p Packet) AsAny() any {
	switch p.Kind {
	case KindPoll:
		return p.Poll
	case KindPollReply:
		return p.PollReply
	case KindDmx:
		return p.Dmx
	case KindTodRequest:
		return p.TodRequest
	case KindTodData:
		return p.TodData
	case KindTodControl:
		return p.TodControl
	case KindRdm:
		return p.Rdm
	case KindRdmSub:
		return p.RdmSub
	case KindTimeCode:
		return p.TimeCode
	case KindAddress:
		return p.Address
	case KindInput:
		return p.Input
	case KindIpProg:
		return p.IpProg
	case KindIpProgReply:
		return p.IpProgReply
	default:
		return Unknown{OpCode: p.UnknownOpCode, Payload: p.UnknownPayload}
	}
}
