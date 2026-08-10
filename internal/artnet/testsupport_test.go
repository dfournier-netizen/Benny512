package artnet

import (
	"strconv"
	"strings"

	"benny512/internal/rdm"
)

// seededGenerator is a deterministic xorshift64 PRNG so property-based tests
// are reproducible across runs — mirrors the Swift reference's SeededGenerator.
type seededGenerator struct {
	state uint64
}

func newSeededGenerator(seed uint64) *seededGenerator {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &seededGenerator{state: seed}
}

func (g *seededGenerator) next() uint64 {
	x := g.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	g.state = x
	return x
}

func (g *seededGenerator) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(g.next() % uint64(n))
}

func (g *seededGenerator) uint8() byte    { return byte(g.next()) }
func (g *seededGenerator) uint16() uint16 { return uint16(g.next()) }

func hexBytes(s string) []byte {
	fields := strings.Fields(s)
	out := make([]byte, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			panic(err)
		}
		out[i] = byte(v)
	}
	return out
}

func randomBytes(n int, g *seededGenerator) []byte {
	if n < 0 {
		n = 0
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = g.uint8()
	}
	return out
}

func randomUID(g *seededGenerator) rdm.UID {
	return rdm.UID{ManufacturerID: g.uint16(), DeviceID: uint32(g.next())}
}

const fieldChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 -_"

// randomFieldString returns a random printable-ASCII string with no
// embedded NUL, short enough to fit in a fieldSize-byte NUL-terminated
// Art-Net string field.
func randomFieldString(fieldSize int, g *seededGenerator) string {
	maxLen := fieldSize - 1
	if maxLen < 0 {
		maxLen = 0
	}
	bound := maxLen
	if bound < 1 {
		bound = 1
	}
	length := g.intn(bound)
	b := make([]byte, length)
	for i := range b {
		b[i] = fieldChars[g.intn(len(fieldChars))]
	}
	return string(b)
}

func randomArtPoll(g *seededGenerator) Poll {
	return Poll{
		ProtocolVersion:         g.uint16(),
		Flags:                   g.uint8(),
		DiagPriority:            g.uint8(),
		TargetPortAddressTop:    g.uint16(),
		TargetPortAddressBottom: g.uint16(),
		EstaManufacturer:        g.uint16(),
		Oem:                     g.uint16(),
	}
}

func randomArtPollReply(g *seededGenerator) PollReply {
	p := PollReply{
		Port:                  g.uint16(),
		VersInfoHi:            g.uint8(),
		VersInfoLo:            g.uint8(),
		NetSwitch:             g.uint8(),
		SubSwitch:             g.uint8(),
		Oem:                   g.uint16(),
		UbeaVersion:           g.uint8(),
		Status1:               g.uint8(),
		EstaManufacturer:      g.uint16(),
		ShortName:             randomFieldString(18, g),
		LongName:              randomFieldString(64, g),
		NodeReport:            randomFieldString(64, g),
		NumPorts:              g.uint16(),
		AcnPriority:           g.uint8(),
		SwMacro:               g.uint8(),
		SwRemote:              g.uint8(),
		Style:                 g.uint8(),
		BindIndex:             g.uint8(),
		Status2:               g.uint8(),
		Status3:               g.uint8(),
		User:                  g.uint16(),
		RefreshRate:           g.uint16(),
		BackgroundQueuePolicy: g.uint8(),
	}
	copy(p.IPAddress[:], randomBytes(4, g))
	copy(p.PortTypes[:], randomBytes(4, g))
	copy(p.GoodInput[:], randomBytes(4, g))
	copy(p.GoodOutputA[:], randomBytes(4, g))
	copy(p.SwIn[:], randomBytes(4, g))
	copy(p.SwOut[:], randomBytes(4, g))
	copy(p.Spare[:], randomBytes(3, g))
	copy(p.MAC[:], randomBytes(6, g))
	copy(p.BindIP[:], randomBytes(4, g))
	copy(p.GoodOutputB[:], randomBytes(4, g))
	copy(p.DefaultRespUID[:], randomBytes(6, g))
	copy(p.Filler[:], randomBytes(10, g))
	return p
}

func randomArtDmx(g *seededGenerator) Dmx {
	evenLen := g.intn(257) * 2 // keep even so encode doesn't pad (exact round-trip)
	return Dmx{
		ProtocolVersion: g.uint16(),
		Sequence:        g.uint8(),
		Physical:        g.uint8(),
		SubUni:          g.uint8(),
		Net:             g.uint8(),
		Data:            randomBytes(evenLen, g),
	}
}

func randomArtTodRequest(g *seededGenerator) TodRequest {
	count := g.intn(33)
	p := TodRequest{
		ProtocolVersion: g.uint16(),
		Filler1:         g.uint8(),
		Filler2:         g.uint8(),
		Net:             g.uint8(),
		Command:         g.uint8(),
		Address:         randomBytes(count, g),
	}
	copy(p.Spare[:], randomBytes(7, g))
	return p
}

func randomArtTodData(g *seededGenerator) TodData {
	count := g.intn(51)
	tod := make([]rdm.UID, count)
	for i := range tod {
		tod[i] = randomUID(g)
	}
	p := TodData{
		ProtocolVersion: g.uint16(),
		RdmVersion:      g.uint8(),
		Port:            g.uint8(),
		Net:             g.uint8(),
		CommandResponse: g.uint8(),
		Address:         g.uint8(),
		UidTotal:        g.uint16(),
		BlockCount:      g.uint8(),
		Tod:             tod,
	}
	copy(p.Spare[:], randomBytes(7, g))
	return p
}

func randomArtTodControl(g *seededGenerator) TodControl {
	p := TodControl{
		ProtocolVersion: g.uint16(),
		Filler1:         g.uint8(),
		Filler2:         g.uint8(),
		Net:             g.uint8(),
		Command:         g.uint8(),
		Address:         g.uint8(),
	}
	copy(p.Spare[:], randomBytes(7, g))
	return p
}

func randomArtRdm(g *seededGenerator) Rdm {
	length := g.intn(258)
	p := Rdm{
		ProtocolVersion: g.uint16(),
		RdmVersion:      g.uint8(),
		Filler2:         g.uint8(),
		Net:             g.uint8(),
		Command:         g.uint8(),
		Address:         g.uint8(),
		RdmData:         randomBytes(length, g),
	}
	copy(p.Spare[:], randomBytes(7, g))
	return p
}

func randomArtRdmSub(g *seededGenerator) RdmSub {
	count := g.intn(51)
	data := make([]uint16, count)
	for i := range data {
		data[i] = g.uint16()
	}
	p := RdmSub{
		ProtocolVersion: g.uint16(),
		RdmVersion:      g.uint8(),
		Filler2:         g.uint8(),
		UID:             randomUID(g),
		Spare1:          g.uint8(),
		CommandClass:    g.uint8(),
		ParameterID:     g.uint16(),
		SubDevice:       g.uint16(),
		Data:            data,
	}
	copy(p.Spare2to5[:], randomBytes(4, g))
	return p
}

func randomArtTimeCode(g *seededGenerator) TimeCode {
	return TimeCode{
		ProtocolVersion: g.uint16(),
		Filler1:         g.uint8(),
		StreamId:        g.uint8(),
		Frames:          g.uint8(),
		Seconds:         g.uint8(),
		Minutes:         g.uint8(),
		Hours:           g.uint8(),
		Type:            g.uint8(),
	}
}
