package rdm

import (
	"strconv"
	"strings"
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
func (g *seededGenerator) uint32() uint32 { return uint32(g.next()) }

// hexBytes parses a whitespace/newline-separated hex dump into bytes.
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

func randomUID(g *seededGenerator) UID {
	return UID{ManufacturerID: g.uint16(), DeviceID: g.uint32()}
}

var allCommandClasses = []CommandClass{
	DiscoveryCommand, DiscoveryCommandResponse, GetCommand, GetCommandResponse, SetCommand, SetCommandResponse,
}

func randomRDMMessage(g *seededGenerator) Message {
	pdl := g.intn(MaxParameterDataLength + 1)
	return Message{
		DestinationUID:       randomUID(g),
		SourceUID:            randomUID(g),
		TransactionNumber:    g.uint8(),
		PortIDOrResponseType: g.uint8(),
		MessageCount:         g.uint8(),
		SubDevice:            g.uint16(),
		CommandClass:         allCommandClasses[g.intn(len(allCommandClasses))],
		ParameterID:          ParameterID(g.uint16()),
		ParameterData:        randomBytes(pdl, g),
	}
}
