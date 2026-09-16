package web

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// This file is the real-socket proof that selecting sACN takes a universe OFF
// Art-Net, and that switching back terminates the E1.31 stream properly.
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

// TestRigCheckSACNTakesTheUniverseOffTheArtNetWire is the isolation proof over
// a real socket: with sACN selected, E1.31 datagrams arrive and ArtDmx stops
// for that universe; switching back reverses it and emits Stream_Terminated.
func TestRigCheckSACNTakesTheUniverseOffTheArtNetWire(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	// Counted AFTER the harness: newHarness starts a registry goroutine that
	// lives for the process, which is not this feature's business. What must
	// not leak is anything the sACN path itself starts.
	before := runtime.NumGoroutine()
	ln := sacnLoopback(t, h, 150)
	seedOneFixture(t, h, 0) // Art-Net Port-Address 0 = show universe 1 = sACN universe 1

	// --- Art-Net first, so there is a stream to take off the air ----------
	if got := startRigCheck(t, h, "artnet").Protocol; got != "artnet" {
		t.Fatalf("state protocol = %q, want \"artnet\"", got)
	}
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("no ArtDmx for universe 0 on the Art-Net run; the rest of this test would prove nothing")
	}
	if pkts := drainE131(t, ln, 50*time.Millisecond); len(pkts) != 0 {
		t.Fatalf("%d E1.31 datagrams arrived during an Art-Net-only run", len(pkts))
	}

	// --- switch that same universe to sACN --------------------------------
	if got := startRigCheck(t, h, "sacn").Protocol; got != "sacn" {
		t.Fatalf("state protocol = %q, want \"sacn\"", got)
	}
	// The switch itself blacks out the outgoing Art-Net stream, which is one
	// last legitimate ArtDmx frame. Drain it, then watch the wire.
	h.tport.TakeSent()
	drainE131(t, ln, 50*time.Millisecond)

	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n != 0 {
		t.Fatalf("universe 0 is STILL on the Art-Net wire after selecting sACN: %d ArtDmx frames in 250ms", n)
	}
	live := drainE131(t, ln, 200*time.Millisecond)
	if len(live) == 0 {
		t.Fatal("no E1.31 datagrams arrived while sACN was selected")
	}
	for i, p := range live {
		if p.universe != 1 {
			t.Fatalf("packet %d is for sACN universe %d, want 1 (Art-Net 0 -> show 1 -> sACN 1)", i, p.universe)
		}
		if p.priority != 150 {
			t.Fatalf("packet %d has priority %d, want the configured 150", i, p.priority)
		}
		if p.startCode != 0x00 {
			t.Fatalf("packet %d has START Code 0x%02x, want 0x00 (Null/DMX512-A)", i, p.startCode)
		}
		if p.terminated {
			t.Fatalf("packet %d carries Stream_Terminated while output is live", i)
		}
		if p.slots[0] != 255 || p.slots[3] != 255 {
			t.Fatalf("packet %d has slots 1..4 = %d,%d,%d,%d, want the rig check level 255 across the footprint",
				i, p.slots[0], p.slots[1], p.slots[2], p.slots[3])
		}
		if p.slots[4] != 0 {
			t.Fatalf("packet %d lit slot 5, which is outside the fixture's footprint", i)
		}
	}
	// Sequence numbers increment by one per packet (Section 6.2.5).
	for i := 1; i < len(live); i++ {
		if live[i].sequence != live[i-1].sequence+1 {
			t.Fatalf("sequence went %d -> %d between packets %d and %d", live[i-1].sequence, live[i].sequence, i-1, i)
		}
	}

	// --- and back to Art-Net ----------------------------------------------
	if got := startRigCheck(t, h, "artnet").Protocol; got != "artnet" {
		t.Fatalf("state protocol = %q after switching back, want \"artnet\"", got)
	}
	tail := drainE131(t, ln, 200*time.Millisecond)
	terminated := 0
	zeroBeforeTerminate := 0
	for _, p := range tail {
		if p.terminated {
			terminated++
			continue
		}
		if terminated == 0 && p.allZero {
			zeroBeforeTerminate++
		}
	}
	if terminated != 3 {
		t.Fatalf("switching away from sACN sent %d Stream_Terminated packets, want 3 (ANSI E1.31-2025 §6.2.6: \"Three packets containing this bit set to 1 shall be sent by sources upon terminating sourcing of a universe\"); the whole tail was %d packets", terminated, len(tail))
	}
	if zeroBeforeTerminate < 3 {
		t.Fatalf("only %d all-zero frames preceded the termination, want at least 3 so the rig is dark whatever the receiver does on data loss", zeroBeforeTerminate)
	}
	for _, p := range tail {
		if p.universe != 1 {
			t.Fatalf("a termination packet was addressed to sACN universe %d, want 1", p.universe)
		}
	}

	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("Art-Net did not resume for universe 0 after switching back")
	}
	if pkts := drainE131(t, ln, 100*time.Millisecond); len(pkts) != 0 {
		t.Fatalf("%d more E1.31 datagrams arrived after switching back to Art-Net", len(pkts))
	}

	h.srv.RigCheck.Stop()
	for i := 0; i < 50 && runtime.NumGoroutine() > before; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: %d before, %d after", before, after)
	}
}

// TestStopAllOutputTerminatesTheSACNStream is the fix for the path
// workspace.go's handleStopAllOutput used to take: DMX.Blackout() + DMX.Stop()
// know nothing about E1.31, so without RigCheck.Stop terminating the stream a
// receiver would hold the zeroed universe until E131_NETWORK_DATA_LOSS_TIMEOUT
// (2.5 seconds, ANSI E1.31-2025 §6.7.1 and Appendix A).
func TestStopAllOutputTerminatesTheSACNStream(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	ln := sacnLoopback(t, h, 100)
	seedOneFixture(t, h, 0)
	startRigCheck(t, h, "sacn")
	drainE131(t, ln, 100*time.Millisecond)

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/output/stop", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/output/stop: status=%d body=%s", rr.Code, rr.Body.String())
	}

	pkts := drainE131(t, ln, 200*time.Millisecond)
	terminated := 0
	for _, p := range pkts {
		if p.terminated {
			terminated++
		}
	}
	if terminated != 3 {
		t.Fatalf("POST /api/output/stop emitted %d Stream_Terminated packets, want 3; receivers would hold the stream for 2.5s", terminated)
	}
	if pkts[len(pkts)-1].universe != 1 {
		t.Fatalf("last packet addressed sACN universe %d, want 1", pkts[len(pkts)-1].universe)
	}
}

// TestRigCheckWatchdogTerminatesTheSACNStream: the pattern engine's
// client-liveness watchdog is the other way output ends without anyone
// pressing stop.
func TestRigCheckWatchdogTerminatesTheSACNStream(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	ln := sacnLoopback(t, h, 100)
	seedOneFixture(t, h, 0)
	startRigCheck(t, h, "sacn")

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/start", map[string]any{
		"scopeKind": "all",
		"tests":     []map[string]any{{"kind": "dimmer_sine", "rateHz": 1, "max": 255}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern start: status=%d body=%s", rr.Code, rr.Body.String())
	}
	drainE131(t, ln, 100*time.Millisecond)

	// Nobody polls the pattern surface, so the watchdog fires. Advance in
	// steps and drain as we go: a live pattern emits a frame per tick, and
	// six seconds of them in one uninterrupted blast overruns the socket's
	// receive buffer -- which would silently eat the very packets this test
	// is looking for.
	var pkts []e131Packet
	for i := 0; i < 12; i++ {
		h.clock.Advance(500 * time.Millisecond)
		pkts = append(pkts, drainE131(t, ln, 20*time.Millisecond)...)
	}
	terminated := 0
	for _, p := range pkts {
		if p.terminated {
			terminated++
		}
	}
	if terminated != 3 {
		t.Fatalf("the watchdog emitted %d Stream_Terminated packets, want 3; an abandoned browser tab would leave the stream up", terminated)
	}
}
