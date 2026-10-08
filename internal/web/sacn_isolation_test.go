package web

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// This file holds the real-socket E1.31 helpers the C3 output tests use
// (output_c3_test.go). Its pre-C3 tests — "selecting sACN takes a universe
// OFF Art-Net" and Rig Check's own sACN stop/watchdog sequences — asserted
// the one-protocol-per-run model the owner reversed in C3 (a universe now
// goes out on Art-Net, sACN or both, and the stop sequence lives in the
// engine; see internal/session/dmxout_test.go), so they were retired.
//
// The packets are parsed BY HAND below, from octet offsets written out from
// ANSI E1.31-2025 Table B-13 -- not with internal/sacn's decoder, and not by
// re-encoding and comparing. internal/sacn has no decoder anyway, but the
// principle is the one this project keeps relearning: a test that decodes
// what the code under test encoded, using the code under test, cannot see a
// symmetric bug. Every offset and every constant below is a literal.

const (
	e131OffACNIdentifier = 4
	e131OffCID           = 22
	e131OffPriority      = 108
	e131OffSequence      = 111
	e131OffOptions       = 112
	e131OffUniverse      = 113
	e131OffStartCode     = 125
	e131OffData          = 126
	e131PacketLen        = 638

	// Options bit 6, Section 6.2.6: the source has terminated transmission
	// of this universe.
	e131StreamTerminated = 0x40
)

type e131Packet struct {
	universe   uint16
	priority   byte
	sequence   byte
	options    byte
	startCode  byte
	slots      []byte
	terminated bool
	allZero    bool
}

// parseE131 reads the fields this test cares about straight out of the
// datagram, by offset.
func parseE131(t *testing.T, b []byte) e131Packet {
	t.Helper()
	if len(b) != e131PacketLen {
		t.Fatalf("datagram is %d octets, want %d for an E1.31 Data Packet carrying 512 slots", len(b), e131PacketLen)
	}
	if got, want := b[0:2], []byte{0x00, 0x10}; !bytes.Equal(got, want) {
		t.Fatalf("Preamble Size = % x, want % x", got, want)
	}
	if got, want := b[e131OffACNIdentifier:e131OffACNIdentifier+12], []byte("ASC-E1.17\x00\x00\x00"); !bytes.Equal(got, want) {
		t.Fatalf("ACN Packet Identifier = %q, want %q", got, want)
	}
	p := e131Packet{
		universe:  uint16(b[e131OffUniverse])<<8 | uint16(b[e131OffUniverse+1]),
		priority:  b[e131OffPriority],
		sequence:  b[e131OffSequence],
		options:   b[e131OffOptions],
		startCode: b[e131OffStartCode],
		slots:     b[e131OffData:],
	}
	p.terminated = p.options&e131StreamTerminated != 0
	p.allZero = true
	for _, s := range p.slots {
		if s != 0 {
			p.allZero = false
			break
		}
	}
	return p
}

// drainE131 reads every datagram already queued on ln, stopping at the first
// short read timeout.
func drainE131(t *testing.T, ln *net.UDPConn, wait time.Duration) []e131Packet {
	t.Helper()
	var out []e131Packet
	buf := make([]byte, 2048)
	for {
		if err := ln.SetReadDeadline(time.Now().Add(wait)); err != nil {
			t.Fatal(err)
		}
		n, _, err := ln.ReadFromUDP(buf)
		if err != nil {
			return out
		}
		out = append(out, parseE131(t, append([]byte(nil), buf[:n]...)))
	}
}

func countArtDmx(t *testing.T, sent []session.SentPacket, universe uint16) int {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(universe)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, sp := range sent {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindDmx {
			continue
		}
		if sp.Packet.Dmx.SubUni == pa.SubUni() && sp.Packet.Dmx.Net == pa.Net {
			n++
		}
	}
	return n
}

// sacnLoopback points the server's sACN output at a real UDP socket on
// loopback and returns it. Unicast rather than multicast so the test does not
// depend on this machine having a multicast-capable route, and on an
// ephemeral port so it never competes for ACN_SDT_MULTICAST_PORT.
func sacnLoopback(t *testing.T, h *testHarness, priority int) *net.UDPConn {
	t.Helper()
	ln, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skipf("cannot bind a loopback UDP socket here: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	h.srv.sacnPort = ln.LocalAddr().(*net.UDPAddr).Port

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/sacn", sacnConfigJSON{
		StartUniverse: 1, Priority: priority, UnicastTo: "127.0.0.1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/sacn: status=%d body=%s", rr.Code, rr.Body.String())
	}
	return ln
}

func seedOneFixture(t *testing.T, h *testHarness, universe uint16) {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Rig Check subject", FixtureType: "Generic Dimmer", Footprint: 4,
		Universe: universe, StartAddress: 1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func startRigCheck(t *testing.T, h *testHarness, protocol string) rigCheckStateJSON {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", rigCheckStartRequest{
		ScopeKind: "all", Mode: "all_channels", Level: 255, Protocol: protocol,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("rigcheck start (protocol=%q): status=%d body=%s", protocol, rr.Code, rr.Body.String())
	}
	var st rigCheckStateJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("unmarshal rig check state: %v", err)
	}
	return st
}
