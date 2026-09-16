package sacn

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain fails the package if the tests leave goroutines behind. Sender owns
// a socket but no goroutines, so the count after the run must settle back to
// what it was before it.
func TestMain(m *testing.M) {
	before := runtime.NumGoroutine()
	code := m.Run()
	if code == 0 {
		var after int
		for i := 0; i < 50; i++ {
			after = runtime.NumGoroutine()
			if after <= before {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if after > before {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			fmt.Fprintf(os.Stderr, "goroutine leak: %d before, %d after\n%s", before, after, buf)
			code = 1
		}
	}
	os.Exit(code)
}

// senderTestCID is an arbitrary CID for the transport tests.
var senderTestCID = [16]byte{
	0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
	0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
}

// TestMulticastIPMatchesTable910 checks the universe-to-address derivation
// against addresses worked out by hand from ANSI E1.31-2025 Table 9-10
// (byte 1 = 239, byte 2 = 255, byte 3 = universe hi, byte 4 = universe lo).
// The expected addresses are written as literal strings; none of them is
// computed from the universe number here.
func TestMulticastIPMatchesTable910(t *testing.T) {
	for _, tc := range []struct {
		universe uint16
		want     string
		note     string
	}{
		{1, "239.255.0.1", "lowest legal universe; hi byte 0, lo byte 1"},
		{2, "239.255.0.2", ""},
		{255, "239.255.0.255", "last universe before the hi byte moves"},
		{256, "239.255.1.0", "first universe with a non-zero hi byte"},
		{257, "239.255.1.1", ""},
		{511, "239.255.1.255", ""},
		{512, "239.255.2.0", ""},
		{1000, "239.255.3.232", "1000 = 0x03E8"},
		// Transcribed directly from e131.txt: "Receiver B then joins the
		// correct multicast address for that universe number, 239.255.31.26".
		{7962, "239.255.31.26", "the worked example in Appendix B"},
		{63999, "239.255.249.255", "highest legal universe; 63999 = 0xF9FF"},
	} {
		got := MulticastIP(tc.universe).String()
		if got != tc.want {
			t.Errorf("MulticastIP(%d) = %s, want %s (%s)", tc.universe, got, tc.want, tc.note)
		}
	}
}

// TestACNSDTMulticastPortValue pins the port constant to the literal value in
// Appendix A: "ACN_SDT_MULTICAST_PORT 5568".
func TestACNSDTMulticastPortValue(t *testing.T) {
	if ACNSDTMulticastPort != 5568 {
		t.Errorf("ACNSDTMulticastPort = %d, want 5568", ACNSDTMulticastPort)
	}
}

// TestDestinationMulticastAndUnicastOverride checks that a plain sender aims
// at the Table 9-10 group on port 5568, and that the per-sender unicast
// override replaces the address for every universe while keeping the port.
func TestDestinationMulticastAndUnicastOverride(t *testing.T) {
	mcast, err := NewSender(Config{CID: senderTestCID, LocalIP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	defer mcast.Close()

	for _, tc := range []struct {
		universe uint16
		want     string
	}{
		{1, "239.255.0.1:5568"},
		{256, "239.255.1.0:5568"},
		{63999, "239.255.249.255:5568"},
	} {
		if got := mcast.Destination(tc.universe).String(); got != tc.want {
			t.Errorf("multicast Destination(%d) = %s, want %s", tc.universe, got, tc.want)
		}
	}

	uni, err := NewSender(Config{
		CID:       senderTestCID,
		LocalIP:   net.IPv4(127, 0, 0, 1),
		UnicastTo: net.IPv4(10, 1, 2, 3),
	})
	if err != nil {
		t.Fatalf("NewSender (unicast): %v", err)
	}
	defer uni.Close()

	for _, universe := range []uint16{1, 256, 63999} {
		if got := uni.Destination(universe).String(); got != "10.1.2.3:5568" {
			t.Errorf("unicast Destination(%d) = %s, want 10.1.2.3:5568", universe, got)
		}
	}
}

func TestDefaultPriorityIsOneHundred(t *testing.T) {
	s, err := NewSender(Config{CID: senderTestCID, LocalIP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	defer s.Close()
	if s.Priority() != 100 {
		t.Errorf("default Priority() = %d, want 100", s.Priority())
	}

	s2, err := NewSender(Config{CID: senderTestCID, LocalIP: net.IPv4(127, 0, 0, 1), Priority: 200})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	defer s2.Close()
	if s2.Priority() != 200 {
		t.Errorf("Priority() = %d, want 200", s2.Priority())
	}
}

// ---- loopback harness: a real net.UDPConn receiver, no mocks ----

type harness struct {
	t  *testing.T
	rx *net.UDPConn
	s  *Sender
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	loopback := net.IPv4(127, 0, 0, 1)
	rx, err := net.ListenUDP("udp4", &net.UDPAddr{IP: loopback, Port: 0})
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	if err := rx.SetReadBuffer(1 << 20); err != nil {
		t.Logf("SetReadBuffer: %v (continuing)", err)
	}

	cfg.LocalIP = loopback
	cfg.UnicastTo = loopback
	cfg.Port = rx.LocalAddr().(*net.UDPAddr).Port
	s, err := NewSender(cfg)
	if err != nil {
		rx.Close()
		t.Fatalf("NewSender: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Sender.Close: %v", err)
		}
		if err := rx.Close(); err != nil {
			t.Errorf("receiver Close: %v", err)
		}
	})
	return &harness{t: t, rx: rx, s: s}
}

// recv returns the next datagram that actually arrives on the socket.
func (h *harness) recv() []byte {
	h.t.Helper()
	if err := h.rx.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		h.t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := h.rx.ReadFromUDP(buf)
	if err != nil {
		h.t.Fatalf("expected an sACN packet on the wire, got: %v", err)
	}
	if n != PacketLen {
		h.t.Fatalf("received %d octets, want %d", n, PacketLen)
	}
	return buf[:n]
}

// sendAndRecv sends one frame and reads it straight back, so no test depends
// on the size of a socket buffer.
func (h *harness) sendAndRecv(universe uint16, payload []byte) []byte {
	h.t.Helper()
	if err := h.s.Send(universe, payload); err != nil {
		h.t.Fatalf("Send(%d): %v", universe, err)
	}
	return h.recv()
}

// expectSilence fails if anything at all arrives within d.
func (h *harness) expectSilence(d time.Duration) {
	h.t.Helper()
	if err := h.rx.SetReadDeadline(time.Now().Add(d)); err != nil {
		h.t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := h.rx.ReadFromUDP(buf)
	if err == nil {
		h.t.Fatalf("expected silence after termination, but received a %d octet packet "+
			"(sequence %d, options 0x%02x)", n, buf[111], buf[112])
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		h.t.Fatalf("expected a read timeout, got: %v", err)
	}
}

// wantPrefix builds the expected octets 0..125 of a packet this sender emits.
// Every constant is a literal transcribed from the standard's field tables;
// only the sequence, options and universe octets vary per call.
func wantPrefix(t *testing.T, sequence, options, universeHi, universeLo string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join([]string{
		"0010",                             // 0-1     Preamble Size
		"0000",                             // 2-3     Post-amble Size
		"4153432d45312e3137000000",         // 4-15    ACN Packet Identifier
		"726e",                             // 16-17   Root Flags & Length
		"00000004",                         // 18-21   VECTOR_ROOT_E131_DATA
		"112233445566778899aabbccddeeff00", // 22-37   CID
		"7258",                             // 38-39   Framing Flags & Length
		"00000002",                         // 40-43   VECTOR_E131_DATA_PACKET
		"42656e6e79353132",                 // 44-51   Source Name "Benny512"
		strings.Repeat("00", 56),           // 52-107  Source Name null padding
		"64",                               // 108     Priority 100
		"0000",                             // 109-110 Synchronization Address
		sequence,                           // 111     Sequence Number
		options,                            // 112     Options
		universeHi + universeLo,            // 113-114 Universe
		"720b",                             // 115-116 DMP Flags & Length
		"02",                               // 117     VECTOR_DMP_SET_PROPERTY
		"a1",                               // 118     Address Type & Data Type
		"0000",                             // 119-120 First Property Address
		"0001",                             // 121-122 Address Increment
		"0201",                             // 123-124 Property Value Count (513)
		"00",                               // 125     DMX512-A START Code
	}, ""))
	if err != nil {
		t.Fatalf("bad transcription: %v", err)
	}
	if len(b) != 126 {
		t.Fatalf("transcribed prefix is %d octets, want 126", len(b))
	}
	return b
}

func checkPrefix(t *testing.T, got, want []byte, what string) {
	t.Helper()
	if bytes.Equal(got[:126], want) {
		return
	}
	i := firstDiff(got[:126], want)
	t.Fatalf("%s: octet %d = 0x%02x, want 0x%02x\ngot  %s\nwant %s",
		what, i, got[i], want[i], hex.EncodeToString(got[:126]), hex.EncodeToString(want))
}

// TestLoopbackFirstPacketLiteralBytes sends one real datagram through a real
// socket to a real receiver and compares every octet that arrives against
// octets transcribed from the standard.
func TestLoopbackFirstPacketLiteralBytes(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	payload := make([]byte, 512)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	got := h.sendAndRecv(258, payload) // 258 = 0x0102

	checkPrefix(t, got, wantPrefix(t, "00", "00", "01", "02"), "first packet on universe 258")
	if !bytes.Equal(got[126:], payload) {
		t.Errorf("DMX512-A slots on the wire do not match the payload that was sent")
	}
}

// TestLoopbackShortPayloadIsZeroPadded checks the wire form of a partial
// universe: 513 property values are always transmitted (Table 7-8), so the
// unused slots must arrive as zeroes.
func TestLoopbackShortPayloadIsZeroPadded(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})
	got := h.sendAndRecv(1, []byte{0xff, 0x7f, 0x01})

	if !bytes.Equal(got[126:129], []byte{0xff, 0x7f, 0x01}) {
		t.Errorf("slots 1-3 = % x, want ff 7f 01", got[126:129])
	}
	for i := 129; i < PacketLen; i++ {
		if got[i] != 0 {
			t.Fatalf("slot octet %d = 0x%02x, want 0x00 (short payloads are zero padded)", i, got[i])
		}
	}
}

// TestSequenceIncrementsAndWraps drives 260 real datagrams through the socket
// and checks the Sequence Number octet of each. Section 6.2.5: the sequence
// "shall be incremented by one for every packet sent on that universe" and
// "shall wrap around to zero" past the maximum.
func TestSequenceIncrementsAndWraps(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	// Hand-written checkpoints, especially either side of the wrap.
	checkpoints := map[int]byte{
		0: 0x00, 1: 0x01, 2: 0x02,
		127: 0x7f,
		253: 0xfd, 254: 0xfe, 255: 0xff,
		256: 0x00, 257: 0x01, 258: 0x02, 259: 0x03,
	}

	var prev byte
	for i := 0; i < 260; i++ {
		pkt := h.sendAndRecv(1, nil)
		seq := pkt[111]
		if want, ok := checkpoints[i]; ok && seq != want {
			t.Fatalf("packet %d: sequence = 0x%02x, want 0x%02x", i, seq, want)
		}
		if i > 0 && seq != prev+1 {
			t.Fatalf("packet %d: sequence = %d, want one more than the previous packet's %d",
				i, seq, prev)
		}
		prev = seq
		if pkt[112] != 0x00 {
			t.Fatalf("packet %d: options = 0x%02x, want 0x00", i, pkt[112])
		}
	}
}

// TestUniversesHaveIndependentSequences interleaves two universes on one
// sender. Section 6.2.5: "Sources shall maintain a sequence for each universe
// they transmit." The expected sequence octets are written out by hand.
func TestUniversesHaveIndependentSequences(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	steps := []struct {
		universe uint16
		wantSeq  byte
	}{
		{1, 0x00},
		{2, 0x00},
		{1, 0x01},
		{2, 0x01},
		{1, 0x02},
		{1, 0x03},
		{2, 0x02},
		{7962, 0x00},
		{2, 0x03},
		{7962, 0x01},
	}
	for i, step := range steps {
		pkt := h.sendAndRecv(step.universe, nil)
		if got := pkt[111]; got != step.wantSeq {
			t.Errorf("step %d (universe %d): sequence = 0x%02x, want 0x%02x",
				i, step.universe, got, step.wantSeq)
		}
		wantUniverse := []byte{byte(step.universe >> 8), byte(step.universe)}
		if !bytes.Equal(pkt[113:115], wantUniverse) {
			t.Errorf("step %d: universe octets = % x, want % x", i, pkt[113:115], wantUniverse)
		}
	}
}

// TestStopSequenceEndToEnd exercises the whole blackout-and-terminate run on
// the wire: StopZeroFrameCount all-zero data frames, then exactly one packet
// with the Stream_Terminated bit (Section 6.2.6 bit 6 = 0x40), then silence.
// The sequence must run on unbroken through the stop packets.
func TestStopSequenceEndToEnd(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	live := make([]byte, 512)
	for i := range live {
		live[i] = 0xff
	}
	for i := 0; i < 2; i++ { // sequences 0x00 and 0x01
		h.sendAndRecv(1, live)
	}

	if err := h.s.Stop(1); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if StopZeroFrameCount != 3 {
		t.Fatalf("this test transcribes the expected stop run for StopZeroFrameCount = 3, got %d",
			StopZeroFrameCount)
	}
	// Hand-written expectation for the stop run: sequences continue 02, 03,
	// 04 with options 0x00, then 05 with options 0x40.
	stopRun := []struct {
		seq     string
		options string
	}{
		{"02", "00"},
		{"03", "00"},
		{"04", "00"},
		{"05", "40"},
	}
	for i, want := range stopRun {
		pkt := h.recv()
		checkPrefix(t, pkt, wantPrefix(t, want.seq, want.options, "00", "01"),
			fmt.Sprintf("stop packet %d", i))
		for j := 126; j < PacketLen; j++ {
			if pkt[j] != 0 {
				t.Fatalf("stop packet %d: slot octet %d = 0x%02x, want 0x00", i, j, pkt[j])
			}
		}
	}

	// Nothing further on this universe.
	h.expectSilence(300 * time.Millisecond)

	// A later restart begins again at FirstSequenceNumber.
	restart := h.sendAndRecv(1, live)
	if got := restart[111]; got != FirstSequenceNumber {
		t.Errorf("sequence after restart = 0x%02x, want 0x%02x", got, FirstSequenceNumber)
	}
	if got := restart[112]; got != 0x00 {
		t.Errorf("options after restart = 0x%02x, want 0x00", got)
	}
}

// TestStopLeavesOtherUniversesAlone: stopping one universe must not disturb
// another universe's counter.
func TestStopLeavesOtherUniversesAlone(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	for i := 0; i < 4; i++ {
		h.sendAndRecv(2, nil) // universe 2 reaches sequence 0x03
	}
	if err := h.s.Stop(1); err != nil {
		t.Fatalf("Stop(1): %v", err)
	}
	for i := 0; i < StopZeroFrameCount+1; i++ {
		pkt := h.recv()
		if !bytes.Equal(pkt[113:115], []byte{0x00, 0x01}) {
			t.Fatalf("stop packet %d is for universe octets % x, want 00 01", i, pkt[113:115])
		}
	}
	pkt := h.sendAndRecv(2, nil)
	if got := pkt[111]; got != 0x04 {
		t.Errorf("universe 2 sequence after stopping universe 1 = 0x%02x, want 0x04", got)
	}
}

// TestConcurrentUniversesKeepSeparateCounters sends from several goroutines at
// once, the way the output engine does, and checks afterwards that each
// universe received a complete, gap-free run of sequence numbers.
func TestConcurrentUniversesKeepSeparateCounters(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})

	const (
		universes = 4
		frames    = 50
	)
	done := make(chan struct{})
	seen := make(map[uint16][]byte)
	go func() {
		defer close(done)
		for i := 0; i < universes*frames; i++ {
			pkt := h.recv()
			u := uint16(pkt[113])<<8 | uint16(pkt[114])
			seen[u] = append(seen[u], pkt[111])
		}
	}()

	var wg sync.WaitGroup
	for u := uint16(1); u <= universes; u++ {
		wg.Add(1)
		go func(u uint16) {
			defer wg.Done()
			for i := 0; i < frames; i++ {
				if err := h.s.Send(u, nil); err != nil {
					t.Errorf("Send(%d): %v", u, err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}(u)
	}
	wg.Wait()
	<-done

	for u := uint16(1); u <= universes; u++ {
		got := seen[u]
		if len(got) != frames {
			t.Errorf("universe %d: got %d packets, want %d", u, len(got), frames)
			continue
		}
		for i, seq := range got {
			if want := byte(i); seq != want {
				t.Errorf("universe %d packet %d: sequence = 0x%02x, want 0x%02x", u, i, seq, want)
				break
			}
		}
	}
}

// TestSendRejectsIllegalUniverses: Section 6.2.7 limits universe values to
// 1..63999.
func TestSendRejectsIllegalUniverses(t *testing.T) {
	h := newHarness(t, Config{CID: senderTestCID})
	for _, u := range []uint16{0, 64000, 64214, 65535} {
		if err := h.s.Send(u, nil); !errors.Is(err, ErrInvalidUniverse) {
			t.Errorf("Send(%d) error = %v, want ErrInvalidUniverse", u, err)
		}
	}
	h.expectSilence(100 * time.Millisecond)
}

// TestSendAfterCloseFails and Close is idempotent.
func TestSendAfterClose(t *testing.T) {
	s, err := NewSender(Config{CID: senderTestCID, LocalIP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := s.Send(1, nil); !errors.Is(err, ErrSenderClosed) {
		t.Errorf("Send after Close = %v, want ErrSenderClosed", err)
	}
	if err := s.Stop(1); !errors.Is(err, ErrSenderClosed) {
		t.Errorf("Stop after Close = %v, want ErrSenderClosed", err)
	}
}

// TestNewSenderFromInterface: the caller may hand over a *net.Interface rather
// than an address, and the sender must bind to one of that NIC's IPv4
// addresses.
func TestNewSenderFromInterface(t *testing.T) {
	ifi, err := net.InterfaceByName("lo")
	if err != nil {
		ifi = firstIPv4Interface(t)
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		t.Skipf("cannot read addresses of %s: %v", ifi.Name, err)
	}
	var want string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			want = n.IP.To4().String()
			break
		}
	}
	if want == "" {
		t.Skipf("interface %s has no IPv4 address", ifi.Name)
	}

	s, err := NewSender(Config{CID: senderTestCID, Interface: ifi})
	if err != nil {
		t.Fatalf("NewSender(Interface: %s): %v", ifi.Name, err)
	}
	defer s.Close()
	got := s.LocalAddr().(*net.UDPAddr).IP.String()
	if got != want {
		t.Errorf("bound to %s, want the interface address %s", got, want)
	}
}

func firstIPv4Interface(t *testing.T) *net.Interface {
	t.Helper()
	ifis, err := net.Interfaces()
	if err != nil {
		t.Skipf("net.Interfaces: %v", err)
	}
	for i := range ifis {
		addrs, err := ifis[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				return &ifis[i]
			}
		}
	}
	t.Skip("no interface with an IPv4 address")
	return nil
}

// TestMulticastRoundTripOnRealNIC proves the default (non-override) path end
// to end: a real receiver joins the Table 9-10 group for a universe on a real
// multicast-capable NIC, the Sender is pointed at that same NIC, and the
// octets that arrive are compared against the transcribed literals. It skips
// when the machine has no usable multicast interface.
func TestMulticastRoundTripOnRealNIC(t *testing.T) {
	ifi := firstMulticastInterface(t)

	const universe = 258 // 0x0102 -> 239.255.1.2
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 1, 2), Port: ACNSDTMulticastPort}
	rx, err := net.ListenMulticastUDP("udp4", ifi, group)
	if err != nil {
		t.Skipf("cannot join %s on %s: %v", group, ifi.Name, err)
	}
	defer rx.Close()

	s, err := NewSender(Config{CID: senderTestCID, Interface: ifi})
	if err != nil {
		t.Fatalf("NewSender(Interface: %s): %v", ifi.Name, err)
	}
	defer s.Close()

	if got := s.Destination(universe).String(); got != "239.255.1.2:5568" {
		t.Fatalf("Destination(%d) = %s, want 239.255.1.2:5568", universe, got)
	}

	payload := make([]byte, 512)
	for i := range payload {
		payload[i] = byte(255 - i%256)
	}
	if err := s.Send(universe, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := rx.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := rx.ReadFromUDP(buf)
	if err != nil {
		t.Skipf("no multicast loopback on %s (%v); the unicast loopback tests still cover the socket path",
			ifi.Name, err)
	}
	if n != PacketLen {
		t.Fatalf("received %d octets, want %d", n, PacketLen)
	}
	checkPrefix(t, buf[:n], wantPrefix(t, "00", "00", "01", "02"), "multicast packet")
	if !bytes.Equal(buf[126:n], payload) {
		t.Error("DMX512-A slots received over multicast do not match the payload sent")
	}
}

func firstMulticastInterface(t *testing.T) *net.Interface {
	t.Helper()
	ifis, err := net.Interfaces()
	if err != nil {
		t.Skipf("net.Interfaces: %v", err)
	}
	for i := range ifis {
		f := ifis[i].Flags
		if f&net.FlagUp == 0 || f&net.FlagMulticast == 0 || f&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifis[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				return &ifis[i]
			}
		}
	}
	t.Skip("no up, multicast-capable, IPv4 interface on this machine")
	return nil
}
