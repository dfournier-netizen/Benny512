package session

import (
	"errors"
	"net/netip"
	"sync"

	"benny512/internal/artnet"
)

// ArtNetUDPPort is Art-Net's fixed UDP port, 6454 (0x1936).
const ArtNetUDPPort uint16 = 6454

// Inbound is one received UDP datagram: the raw payload plus the source
// address. Engines decode it themselves — the transport stays dumb.
type Inbound struct {
	Data []byte
	From netip.AddrPort
}

// Transport is the wire-level dependency injected into every engine. It
// carries raw UDP payload bytes; it knows nothing about Art-Net.
//
// Inbound returns the shared receive channel. Engines consume it via their
// Run method, or callers may pump packets in synchronously with
// HandleInbound (which is what Run does internally, and what the tests use
// to stay deterministic).
type Transport interface {
	// Send unicasts data to dst.
	Send(data []byte, dst netip.AddrPort) error
	// Broadcast sends data to the transport's configured broadcast address
	// (directed subnet broadcast in production, per architecture rev 5 §3).
	Broadcast(data []byte) error
	// Inbound is the receive channel; closed when the transport shuts down.
	Inbound() <-chan Inbound
}

// SentPacket records one outbound datagram captured by FakeTransport. The
// Art-Net decode is done eagerly for test convenience; DecodeErr is non-nil
// if the bytes were not a valid Art-Net packet (which is itself a bug worth
// asserting on).
type SentPacket struct {
	Data      []byte
	Dst       netip.AddrPort
	Broadcast bool
	Packet    artnet.Packet
	DecodeErr error
}

// ErrTransportClosed is returned by FakeTransport.Send/Broadcast after Close.
var ErrTransportClosed = errors.New("session: transport closed")

// FakeTransport is an in-memory Transport for tests and for the Phase 1c
// offline/demo mode. It captures every outbound datagram (decoded) and lets
// a test inject inbound datagrams.
//
// OnSend, if set, is invoked after the packet is recorded and after the
// transport's own lock is released. It is called on the sending goroutine
// while the calling engine still holds its own mutex, so an OnSend hook must
// never re-enter the engine directly — it should schedule the reply through
// the Clock instead. The scripted responder used by the tests does exactly
// that, which is also how reply latency is modelled.
type FakeTransport struct {
	mu        sync.Mutex
	sent      []SentPacket
	inbound   chan Inbound
	closed    bool
	sendErr   error
	broadcast netip.AddrPort

	// OnSend is a hook for scripted request→response fakes.
	OnSend func(SentPacket)

	// sentCh is signalled (non-blocking, best-effort) once per recorded
	// datagram — see SentSignal below.
	sentCh chan struct{}
}

// NewFakeTransport returns an empty FakeTransport with a generously buffered
// inbound channel.
func NewFakeTransport() *FakeTransport {
	return &FakeTransport{
		inbound:   make(chan Inbound, 256),
		broadcast: netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), ArtNetUDPPort),
		sentCh:    make(chan struct{}, 64),
	}
}

// SentSignal returns a channel that receives one value shortly after every
// outbound datagram FakeTransport records (Send or Broadcast), independent
// of whatever OnSend hook is (or isn't) installed. It exists so a driving
// test loop can block for real — no CPU spent, no fixed iteration budget —
// until there is new wire traffic worth reacting to (typically: advance the
// fake clock so a scripted response's Clock.AfterFunc callback can fire),
// rather than racing a spin-and-Gosched loop against however the OS
// scheduler happens to be treating the sending goroutine at that moment.
// Signals may coalesce (the channel is small and non-blocking to send on: a
// slow consumer drops rather than blocking the sender), which is safe here
// because Advance fires every timer up to its target in one call, so waking
// up "at least once more" is all a consumer ever needs — it isn't counting
// sends.
func (t *FakeTransport) SentSignal() <-chan struct{} { return t.sentCh }

// SetSendError makes every subsequent Send/Broadcast fail with err (nil
// clears it), for exercising transmit-failure paths.
func (t *FakeTransport) SetSendError(err error) {
	t.mu.Lock()
	t.sendErr = err
	t.mu.Unlock()
}

// Send implements Transport.
func (t *FakeTransport) Send(data []byte, dst netip.AddrPort) error {
	return t.record(data, dst, false)
}

// Broadcast implements Transport.
func (t *FakeTransport) Broadcast(data []byte) error {
	return t.record(data, t.broadcast, true)
}

func (t *FakeTransport) record(data []byte, dst netip.AddrPort, broadcast bool) error {
	cp := append([]byte(nil), data...)
	pkt, err := artnet.Decode(cp)
	sp := SentPacket{Data: cp, Dst: dst, Broadcast: broadcast, Packet: pkt, DecodeErr: err}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrTransportClosed
	}
	if t.sendErr != nil {
		err := t.sendErr
		t.mu.Unlock()
		return err
	}
	t.sent = append(t.sent, sp)
	hook := t.OnSend
	t.mu.Unlock()

	select {
	case t.sentCh <- struct{}{}:
	default:
	}

	if hook != nil {
		hook(sp)
	}
	return nil
}

// Inbound implements Transport.
func (t *FakeTransport) Inbound() <-chan Inbound { return t.inbound }

// Deliver pushes a datagram onto the inbound channel, for tests that
// exercise an engine's Run pump.
func (t *FakeTransport) Deliver(in Inbound) {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return
	}
	t.inbound <- in
}

// Close closes the inbound channel and fails further sends.
func (t *FakeTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	close(t.inbound)
}

// Sent returns a snapshot of every datagram sent so far.
func (t *FakeTransport) Sent() []SentPacket {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]SentPacket(nil), t.sent...)
}

// TakeSent returns and clears the captured datagrams — the usual way tests
// assert "exactly these packets went out since the last check".
func (t *FakeTransport) TakeSent() []SentPacket {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.sent
	t.sent = nil
	return out
}

// SentCount reports how many datagrams have been captured (and not taken).
func (t *FakeTransport) SentCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sent)
}
