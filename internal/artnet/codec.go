package artnet

import (
	"bytes"
	"errors"
	"fmt"

	"benny512/internal/bytesio"
	"benny512/internal/rdm"
)

// IDBytes is the literal "Art-Net\0" packet ID.
var IDBytes = []byte("Art-Net\x00")

// Port is the standard Art-Net UDP port, 0x1936.
const Port uint16 = 0x1936

const pollReplyMinimumLength = 207

// Decode error sentinels. Use errors.Is against these; DecodeError carries
// the OpCode (when known) and other diagnostic detail.
var (
	ErrTooShort             = errors.New("artnet: packet too short")
	ErrInvalidID            = errors.New("artnet: invalid packet ID")
	ErrAddressCountMismatch = errors.New("artnet: address count mismatch")
	ErrUIDCountMismatch     = errors.New("artnet: UID count mismatch")
	ErrSubCountMismatch     = errors.New("artnet: sub-device count mismatch")
	ErrMalformedRDMPayload  = errors.New("artnet: malformed RDM payload")
)

// DecodeError carries the OpCode (nil if not yet known, e.g. ID mismatch)
// plus length diagnostics alongside one of the Err* sentinels.
type DecodeError struct {
	Err    error
	OpCode *uint16
	Need   int
	Have   int
	Detail string
}

func (e *DecodeError) Error() string {
	msg := e.Err.Error()
	if e.OpCode != nil {
		msg = fmt.Sprintf("%s (opcode 0x%04X)", msg, *e.OpCode)
	}
	if e.Need > 0 || e.Have > 0 {
		msg = fmt.Sprintf("%s: need %d, have %d", msg, e.Need, e.Have)
	}
	if e.Detail != "" {
		msg = fmt.Sprintf("%s: %s", msg, e.Detail)
	}
	return msg
}

func (e *DecodeError) Unwrap() error { return e.Err }

func tooShort(opCode *uint16, need, have int) error {
	return &DecodeError{Err: ErrTooShort, OpCode: opCode, Need: need, Have: have}
}

func opCodePtr(v uint16) *uint16 { return &v }

// Decode sniffs the "Art-Net\0" ID and OpCode of b and dispatches to a typed
// packet. Unrecognized OpCodes decode to Kind == KindUnknown rather than
// throwing, so the decoder stays usable on packets from newer spec revisions
// or vendor extensions this package doesn't model.
func Decode(b []byte) (Packet, error) {
	if len(b) < 10 {
		return Packet{}, tooShort(nil, 10, len(b))
	}
	if !bytes.Equal(b[0:8], IDBytes) {
		return Packet{}, &DecodeError{Err: ErrInvalidID, Detail: fmt.Sprintf("% X", b[0:8])}
	}
	opCode := uint16(b[8]) | uint16(b[9])<<8 // OpCode is LE

	switch opCode {
	case 0x2000:
		p, err := decodePoll(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindPoll, Poll: p}, nil
	case 0x2100:
		p, err := decodePollReply(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindPollReply, PollReply: p}, nil
	case 0x5000:
		p, err := decodeDmx(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindDmx, Dmx: p}, nil
	case 0x8000:
		p, err := decodeTodRequest(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindTodRequest, TodRequest: p}, nil
	case 0x8100:
		p, err := decodeTodData(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindTodData, TodData: p}, nil
	case 0x8200:
		p, err := decodeTodControl(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindTodControl, TodControl: p}, nil
	case 0x8300:
		p, err := decodeRdm(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindRdm, Rdm: p}, nil
	case 0x8400:
		p, err := decodeRdmSub(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindRdmSub, RdmSub: p}, nil
	case 0x9700:
		p, err := decodeTimeCode(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindTimeCode, TimeCode: p}, nil
	case 0x6000:
		p, err := decodeAddress(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindAddress, Address: p}, nil
	case 0x7000:
		p, err := decodeInput(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindInput, Input: p}, nil
	case 0xf800:
		p, err := decodeIpProg(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindIpProg, IpProg: p}, nil
	case 0xf900:
		p, err := decodeIpProgReply(b)
		if err != nil {
			return Packet{}, err
		}
		return Packet{Kind: KindIpProgReply, IpProgReply: p}, nil
	default:
		var payload []byte
		if len(b) > 10 {
			payload = append([]byte{}, b[10:]...)
		}
		return Packet{Kind: KindUnknown, UnknownOpCode: opCode, UnknownPayload: payload}, nil
	}
}

// Encode serializes packet to its on-wire byte representation. Encode is
// total over a well-typed Packet value: out-of-range field values are
// clamped/truncated on the wire rather than rejected.
func Encode(packet Packet) []byte {
	switch packet.Kind {
	case KindPoll:
		return encodePoll(packet.Poll)
	case KindPollReply:
		return encodePollReply(packet.PollReply)
	case KindDmx:
		return encodeDmx(packet.Dmx)
	case KindTodRequest:
		return encodeTodRequest(packet.TodRequest)
	case KindTodData:
		return encodeTodData(packet.TodData)
	case KindTodControl:
		return encodeTodControl(packet.TodControl)
	case KindRdm:
		return encodeRdm(packet.Rdm)
	case KindRdmSub:
		return encodeRdmSub(packet.RdmSub)
	case KindTimeCode:
		return encodeTimeCode(packet.TimeCode)
	case KindAddress:
		return encodeAddress(packet.Address)
	case KindInput:
		return encodeInput(packet.Input)
	case KindIpProg:
		return encodeIpProg(packet.IpProg)
	case KindIpProgReply:
		return encodeIpProgReply(packet.IpProgReply)
	default:
		w := bytesio.NewWriter()
		w.WriteBytes(IDBytes)
		w.WriteU16LE(packet.UnknownOpCode)
		w.WriteBytes(packet.UnknownPayload)
		return w.Bytes()
	}
}

// --- shared helpers ---

// writeHeader writes the common ID+OpCode+ProtVer header (10+2 bytes). Not
// used by ArtPollReply, which has no ProtVer bytes at all.
func writeHeader(w *bytesio.Writer, opCode, protocolVersion uint16) {
	w.WriteBytes(IDBytes)
	w.WriteU16LE(opCode)
	w.WriteU16BE(protocolVersion)
}

// pad truncates/zero-pads b to exactly length bytes before writing a
// fixed-size field, so an encode call can never index out of bounds even if
// a caller hand-built a struct with a wrong-length slice.
func pad(b []byte, length int) []byte {
	if len(b) == length {
		return b
	}
	if len(b) > length {
		return b[:length]
	}
	out := make([]byte, length)
	copy(out, b)
	return out
}

// wrapReaderErr converts an escaping *bytesio.ReadError into a tooShort
// fallback — this should never happen given each function's upfront length
// guard — so no internal error type ever escapes the public API.
func wrapReaderErr(opCode uint16, err error) error {
	if err == nil {
		return nil
	}
	var de *DecodeError
	if errors.As(err, &de) {
		return err
	}
	var re *bytesio.ReadError
	if errors.As(err, &re) {
		return tooShort(opCodePtr(opCode), -1, -1)
	}
	return err
}

// --- ArtPoll (0x2000) — 24 bytes current / 14 bytes minimum accepted ---

func decodePoll(b []byte) (Poll, error) {
	if len(b) < 14 {
		return Poll{}, tooShort(opCodePtr(0x2000), 14, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return Poll{}, wrapReaderErr(0x2000, err)
	}
	flags, err := r.ReadU8()
	if err != nil {
		return Poll{}, wrapReaderErr(0x2000, err)
	}
	diagPriority, err := r.ReadU8()
	if err != nil {
		return Poll{}, wrapReaderErr(0x2000, err)
	}
	// Offsets 14-21 are lenient: missing trailing fields default to zero.
	u16OrZero := func() uint16 {
		v, err := r.ReadU16BE()
		if err != nil {
			return 0
		}
		return v
	}
	targetTop := u16OrZero()
	targetBottom := u16OrZero()
	esta := u16OrZero()
	oem := u16OrZero()
	return Poll{
		ProtocolVersion:         protocolVersion,
		Flags:                   flags,
		DiagPriority:            diagPriority,
		TargetPortAddressTop:    targetTop,
		TargetPortAddressBottom: targetBottom,
		EstaManufacturer:        esta,
		Oem:                     oem,
	}, nil
}

func encodePoll(p Poll) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x2000, p.ProtocolVersion)
	w.WriteU8(p.Flags)
	w.WriteU8(p.DiagPriority)
	w.WriteU16BE(p.TargetPortAddressTop)
	w.WriteU16BE(p.TargetPortAddressBottom)
	w.WriteU16BE(p.EstaManufacturer)
	w.WriteU16BE(p.Oem)
	return w.Bytes()
}

// --- ArtPollReply (0x2100) — 239 bytes current / 207 bytes minimum accepted ---

func decodePollReply(b []byte) (PollReply, error) {
	if len(b) < pollReplyMinimumLength {
		return PollReply{}, tooShort(opCodePtr(0x2100), pollReplyMinimumLength, len(b))
	}
	r := bytesio.NewReaderAt(b, 10) // no ProtVer — header is ID+OpCode only
	ip, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	port, err := r.ReadU16LE()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	versInfoHi, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	versInfoLo, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	netSwitch, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	subSwitch, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	oem, err := r.ReadU16BE()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	ubeaVersion, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	status1, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	estaManufacturer, err := r.ReadU16LE() // LE here, opposite of ArtPoll
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	shortName, err := r.ReadFixedString(18)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	longName, err := r.ReadFixedString(64)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	nodeReport, err := r.ReadFixedString(64)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	numPorts, err := r.ReadU16BE()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	portTypes, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	goodInput, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	goodOutputA, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	swIn, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	swOut, err := r.ReadBytes(4)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	acnPriority, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	swMacro, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	swRemote, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	spare, err := r.ReadBytes(3)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	style, err := r.ReadU8()
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	mac, err := r.ReadBytes(6)
	if err != nil {
		return PollReply{}, wrapReaderErr(0x2100, err)
	}
	// --- 207-byte minimum-accepted boundary; everything below is lenient ---
	bytesOrZero := func(n int) []byte {
		v, err := r.ReadBytes(n)
		if err != nil {
			return make([]byte, n)
		}
		return v
	}
	u8OrZero := func() byte {
		v, err := r.ReadU8()
		if err != nil {
			return 0
		}
		return v
	}
	u16BEOrZero := func() uint16 {
		v, err := r.ReadU16BE()
		if err != nil {
			return 0
		}
		return v
	}

	bindIP := bytesOrZero(4)
	bindIndex := u8OrZero()
	status2 := u8OrZero()
	goodOutputB := bytesOrZero(4)
	status3 := u8OrZero()
	defaultRespUID := bytesOrZero(6)
	user := u16BEOrZero()
	refreshRate := u16BEOrZero()
	backgroundQueuePolicy := u8OrZero()
	filler := bytesOrZero(10)

	out := PollReply{
		Port: port, VersInfoHi: versInfoHi, VersInfoLo: versInfoLo,
		NetSwitch: netSwitch, SubSwitch: subSwitch, Oem: oem, UbeaVersion: ubeaVersion,
		Status1: status1, EstaManufacturer: estaManufacturer, ShortName: shortName,
		LongName: longName, NodeReport: nodeReport, NumPorts: numPorts,
		AcnPriority: acnPriority, SwMacro: swMacro, SwRemote: swRemote,
		Style: style, Status2: status2, Status3: status3,
		User: user, RefreshRate: refreshRate, BackgroundQueuePolicy: backgroundQueuePolicy,
		BindIndex: bindIndex,
	}
	copy(out.IPAddress[:], ip)
	copy(out.PortTypes[:], portTypes)
	copy(out.GoodInput[:], goodInput)
	copy(out.GoodOutputA[:], goodOutputA)
	copy(out.SwIn[:], swIn)
	copy(out.SwOut[:], swOut)
	copy(out.Spare[:], spare)
	copy(out.MAC[:], mac)
	copy(out.BindIP[:], bindIP)
	copy(out.GoodOutputB[:], goodOutputB)
	copy(out.DefaultRespUID[:], defaultRespUID)
	copy(out.Filler[:], filler)
	return out, nil
}

func encodePollReply(p PollReply) []byte {
	w := bytesio.NewWriter()
	w.WriteBytes(IDBytes)
	w.WriteU16LE(0x2100) // no ProtVer bytes follow
	w.WriteBytes(pad(p.IPAddress[:], 4))
	w.WriteU16LE(p.Port)
	w.WriteU8(p.VersInfoHi)
	w.WriteU8(p.VersInfoLo)
	w.WriteU8(p.NetSwitch)
	w.WriteU8(p.SubSwitch)
	w.WriteU16BE(p.Oem)
	w.WriteU8(p.UbeaVersion)
	w.WriteU8(p.Status1)
	w.WriteU16LE(p.EstaManufacturer)
	w.WriteFixedString(p.ShortName, 18)
	w.WriteFixedString(p.LongName, 64)
	w.WriteFixedString(p.NodeReport, 64)
	w.WriteU16BE(p.NumPorts)
	w.WriteBytes(pad(p.PortTypes[:], 4))
	w.WriteBytes(pad(p.GoodInput[:], 4))
	w.WriteBytes(pad(p.GoodOutputA[:], 4))
	w.WriteBytes(pad(p.SwIn[:], 4))
	w.WriteBytes(pad(p.SwOut[:], 4))
	w.WriteU8(p.AcnPriority)
	w.WriteU8(p.SwMacro)
	w.WriteU8(p.SwRemote)
	w.WriteBytes(pad(p.Spare[:], 3))
	w.WriteU8(p.Style)
	w.WriteBytes(pad(p.MAC[:], 6))
	w.WriteBytes(pad(p.BindIP[:], 4))
	w.WriteU8(p.BindIndex)
	w.WriteU8(p.Status2)
	w.WriteBytes(pad(p.GoodOutputB[:], 4))
	w.WriteU8(p.Status3)
	w.WriteBytes(pad(p.DefaultRespUID[:], 6))
	w.WriteU16BE(p.User)
	w.WriteU16BE(p.RefreshRate)
	w.WriteU8(p.BackgroundQueuePolicy)
	w.WriteBytes(pad(p.Filler[:], 10))
	return w.Bytes()
}

// --- ArtDmx (0x5000) — 18-530 bytes ---

func decodeDmx(b []byte) (Dmx, error) {
	if len(b) < 18 {
		return Dmx{}, tooShort(opCodePtr(0x5000), 18, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	sequence, err := r.ReadU8()
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	physical, err := r.ReadU8()
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	subUni, err := r.ReadU8()
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	net, err := r.ReadU8()
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	length, err := r.ReadU16BE() // "should" be even — accept odd on receive
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	take := int(length)
	if take > r.Remaining() {
		take = r.Remaining()
	}
	data, err := r.ReadBytes(take)
	if err != nil {
		return Dmx{}, wrapReaderErr(0x5000, err)
	}
	return Dmx{ProtocolVersion: protocolVersion, Sequence: sequence, Physical: physical, SubUni: subUni, Net: net, Data: data}, nil
}

func encodeDmx(p Dmx) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x5000, p.ProtocolVersion)
	w.WriteU8(p.Sequence)
	w.WriteU8(p.Physical)
	w.WriteU8(p.SubUni)
	w.WriteU8(p.Net)
	data := p.Data
	if len(data)%2 != 0 {
		data = append(append([]byte{}, data...), 0) // always send even
	}
	length := len(data)
	if length > 0xFFFF {
		length = 0xFFFF
	}
	w.WriteU16BE(uint16(length))
	w.WriteBytes(data)
	return w.Bytes()
}

// --- ArtTodRequest (0x8000) — 24-56 bytes ---

func decodeTodRequest(b []byte) (TodRequest, error) {
	if len(b) < 24 {
		return TodRequest{}, tooShort(opCodePtr(0x8000), 24, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	filler1, err := r.ReadU8()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	filler2, err := r.ReadU8()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	spare, err := r.ReadBytes(7)
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	net, err := r.ReadU8()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	command, err := r.ReadU8()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	addCount, err := r.ReadU8()
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	if r.Remaining() < int(addCount) {
		return TodRequest{}, &DecodeError{Err: ErrAddressCountMismatch, OpCode: opCodePtr(0x8000), Detail: fmt.Sprintf("declared %d, available %d", addCount, r.Remaining())}
	}
	address, err := r.ReadBytes(int(addCount))
	if err != nil {
		return TodRequest{}, wrapReaderErr(0x8000, err)
	}
	out := TodRequest{ProtocolVersion: protocolVersion, Filler1: filler1, Filler2: filler2, Net: net, Command: command, Address: address}
	copy(out.Spare[:], spare)
	return out, nil
}

func encodeTodRequest(p TodRequest) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x8000, p.ProtocolVersion)
	w.WriteU8(p.Filler1)
	w.WriteU8(p.Filler2)
	w.WriteBytes(pad(p.Spare[:], 7))
	w.WriteU8(p.Net)
	w.WriteU8(p.Command)
	count := len(p.Address)
	if count > 255 {
		count = 255
	}
	w.WriteU8(byte(count))
	w.WriteBytes(p.Address[:count])
	return w.Bytes()
}

// --- ArtTodData (0x8100) — 28+ bytes (BindIndex position unverified) ---

func decodeTodData(b []byte) (TodData, error) {
	if len(b) < 28 {
		return TodData{}, tooShort(opCodePtr(0x8100), 28, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	rdmVersion, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	port, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	spare, err := r.ReadBytes(7)
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	net, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	commandResponse, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	address, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	uidTotal, err := r.ReadU16BE()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	blockCount, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	uidCount, err := r.ReadU8()
	if err != nil {
		return TodData{}, wrapReaderErr(0x8100, err)
	}
	neededBytes := int(uidCount) * 6
	if r.Remaining() < neededBytes {
		return TodData{}, &DecodeError{Err: ErrUIDCountMismatch, OpCode: opCodePtr(0x8100), Detail: fmt.Sprintf("declared %d, available %d", uidCount, r.Remaining()/6)}
	}
	tod := make([]rdm.UID, 0, uidCount)
	for i := 0; i < int(uidCount); i++ {
		ub, err := r.ReadBytes(6)
		if err != nil {
			return TodData{}, wrapReaderErr(0x8100, err)
		}
		uid, _ := rdm.UIDFromBytes(ub)
		tod = append(tod, uid)
	}
	out := TodData{ProtocolVersion: protocolVersion, RdmVersion: rdmVersion, Port: port, Net: net, CommandResponse: commandResponse, Address: address, UidTotal: uidTotal, BlockCount: blockCount, Tod: tod}
	copy(out.Spare[:], spare)
	return out, nil
}

func encodeTodData(p TodData) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x8100, p.ProtocolVersion)
	w.WriteU8(p.RdmVersion)
	w.WriteU8(p.Port)
	w.WriteBytes(pad(p.Spare[:], 7))
	w.WriteU8(p.Net)
	w.WriteU8(p.CommandResponse)
	w.WriteU8(p.Address)
	w.WriteU16BE(p.UidTotal)
	w.WriteU8(p.BlockCount)
	count := len(p.Tod)
	if count > 255 {
		count = 255
	}
	w.WriteU8(byte(count))
	for _, uid := range p.Tod[:count] {
		w.WriteBytes(uid.Bytes())
	}
	return w.Bytes()
}

// --- ArtTodControl (0x8200) — 24 bytes fixed ---

func decodeTodControl(b []byte) (TodControl, error) {
	if len(b) < 24 {
		return TodControl{}, tooShort(opCodePtr(0x8200), 24, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	filler1, err := r.ReadU8()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	filler2, err := r.ReadU8()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	spare, err := r.ReadBytes(7)
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	net, err := r.ReadU8()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	command, err := r.ReadU8()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	address, err := r.ReadU8()
	if err != nil {
		return TodControl{}, wrapReaderErr(0x8200, err)
	}
	out := TodControl{ProtocolVersion: protocolVersion, Filler1: filler1, Filler2: filler2, Net: net, Command: command, Address: address}
	copy(out.Spare[:], spare)
	return out, nil
}

func encodeTodControl(p TodControl) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x8200, p.ProtocolVersion)
	w.WriteU8(p.Filler1)
	w.WriteU8(p.Filler2)
	w.WriteBytes(pad(p.Spare[:], 7))
	w.WriteU8(p.Net)
	w.WriteU8(p.Command)
	w.WriteU8(p.Address)
	return w.Bytes()
}

// --- ArtRdm (0x8300) — 24 + RDM data ---

func decodeRdm(b []byte) (Rdm, error) {
	if len(b) < 24 {
		return Rdm{}, tooShort(opCodePtr(0x8300), 24, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	rdmVersion, err := r.ReadU8()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	filler2, err := r.ReadU8()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	spare, err := r.ReadBytes(7)
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	net, err := r.ReadU8()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	command, err := r.ReadU8()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	address, err := r.ReadU8()
	if err != nil {
		return Rdm{}, wrapReaderErr(0x8300, err)
	}
	rdmData := r.ReadRemaining()
	out := Rdm{ProtocolVersion: protocolVersion, RdmVersion: rdmVersion, Filler2: filler2, Net: net, Command: command, Address: address, RdmData: rdmData}
	copy(out.Spare[:], spare)
	return out, nil
}

func encodeRdm(p Rdm) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x8300, p.ProtocolVersion)
	w.WriteU8(p.RdmVersion)
	w.WriteU8(p.Filler2)
	w.WriteBytes(pad(p.Spare[:], 7))
	w.WriteU8(p.Net)
	w.WriteU8(p.Command)
	w.WriteU8(p.Address)
	w.WriteBytes(p.RdmData)
	return w.Bytes()
}

// --- ArtRdmSub (0x8400) — 32 + (2 * SubCount) bytes ---

func decodeRdmSub(b []byte) (RdmSub, error) {
	if len(b) < 32 {
		return RdmSub{}, tooShort(opCodePtr(0x8400), 32, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	rdmVersion, err := r.ReadU8()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	filler2, err := r.ReadU8()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	uidBytes, err := r.ReadBytes(6)
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	spare1, err := r.ReadU8()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	commandClass, err := r.ReadU8()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	parameterID, err := r.ReadU16BE()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	subDevice, err := r.ReadU16BE()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	subCount, err := r.ReadU16BE()
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	spare2to5, err := r.ReadBytes(4)
	if err != nil {
		return RdmSub{}, wrapReaderErr(0x8400, err)
	}
	neededBytes := int(subCount) * 2
	if r.Remaining() < neededBytes {
		return RdmSub{}, &DecodeError{Err: ErrSubCountMismatch, OpCode: opCodePtr(0x8400), Detail: fmt.Sprintf("declared %d, available %d", subCount, r.Remaining()/2)}
	}
	data := make([]uint16, 0, subCount)
	for i := 0; i < int(subCount); i++ {
		v, err := r.ReadU16BE()
		if err != nil {
			return RdmSub{}, wrapReaderErr(0x8400, err)
		}
		data = append(data, v)
	}
	uid, _ := rdm.UIDFromBytes(uidBytes)
	out := RdmSub{ProtocolVersion: protocolVersion, RdmVersion: rdmVersion, Filler2: filler2, UID: uid, Spare1: spare1, CommandClass: commandClass, ParameterID: parameterID, SubDevice: subDevice, Data: data}
	copy(out.Spare2to5[:], spare2to5)
	return out, nil
}

func encodeRdmSub(p RdmSub) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x8400, p.ProtocolVersion)
	w.WriteU8(p.RdmVersion)
	w.WriteU8(p.Filler2)
	w.WriteBytes(pad(p.UID.Bytes(), 6))
	w.WriteU8(p.Spare1)
	w.WriteU8(p.CommandClass)
	w.WriteU16BE(p.ParameterID)
	w.WriteU16BE(p.SubDevice)
	count := len(p.Data)
	if count > 0xFFFF {
		count = 0xFFFF
	}
	w.WriteU16BE(uint16(count))
	w.WriteBytes(pad(p.Spare2to5[:], 4))
	for _, v := range p.Data[:count] {
		w.WriteU16BE(v)
	}
	return w.Bytes()
}

// --- ArtTimeCode (0x9700) — 19 bytes fixed ---

func decodeTimeCode(b []byte) (TimeCode, error) {
	if len(b) < 19 {
		return TimeCode{}, tooShort(opCodePtr(0x9700), 19, len(b))
	}
	r := bytesio.NewReaderAt(b, 10)
	protocolVersion, err := r.ReadU16BE()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	filler1, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	streamId, err := r.ReadU8() // offset 13 — not a second filler byte
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	frames, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	seconds, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	minutes, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	hours, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	typ, err := r.ReadU8()
	if err != nil {
		return TimeCode{}, wrapReaderErr(0x9700, err)
	}
	return TimeCode{ProtocolVersion: protocolVersion, Filler1: filler1, StreamId: streamId, Frames: frames, Seconds: seconds, Minutes: minutes, Hours: hours, Type: typ}, nil
}

func encodeTimeCode(p TimeCode) []byte {
	w := bytesio.NewWriter()
	writeHeader(w, 0x9700, p.ProtocolVersion)
	w.WriteU8(p.Filler1)
	w.WriteU8(p.StreamId)
	w.WriteU8(p.Frames)
	w.WriteU8(p.Seconds)
	w.WriteU8(p.Minutes)
	w.WriteU8(p.Hours)
	w.WriteU8(p.Type)
	return w.Bytes()
}
