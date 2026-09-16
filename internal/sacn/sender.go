package sacn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
)

// SourceName is the E1.31 Source Name this application transmits in octets
// 44-107 of every packet. It is a fixed product string, not a setting.
const SourceName = "Benny512"

// DefaultPriority is the E1.31 priority used when Config leaves Priority at
// its zero value. 100 is the conventional "normal" priority and is what ANSI
// E1.31-2025 Table B-13 prints in its worked example.
const DefaultPriority byte = 100

// ACNSDTMulticastPort is ACN_SDT_MULTICAST_PORT from ANSI E1.31-2025
// Appendix A. Section 9.1.1 requires it as the UDP destination port for
// multicast traffic and makes it the default for unicast traffic, while
// explicitly permitting configurable alternative ports for unicast.
const ACNSDTMulticastPort = 5568

// FirstSequenceNumber is the Sequence Number (octet 111) this package puts in
// the first packet it sends on a universe.
//
// ANSI E1.31-2025 Section 6.2.5 requires a per-universe sequence that
// increments by one per packet and wraps to zero, but it does not define a
// starting value, so this choice is ours. See the package README note in
// Sender.Stop about the restart caveat that follows from it.
const FirstSequenceNumber byte = 0

// StopZeroFrameCount is how many all-zero E1.31 Data Packets Sender.Stop
// sends before the Stream_Terminated packet.
//
// Three, because a Stream_Terminated packet only puts the receiver into
// network data loss condition (Section 6.7.1); what a receiver then does with
// its outputs -- hold the last look, fade out, or go dark -- is outside the
// scope of the standard. Driving the universe to zero first means the fixtures
// are already dark whatever the receiver chooses. Three matches the standard's
// own redundancy convention for unacknowledged state changes (Section 6.6.2
// requires three packets of non-changing data before transmission suppression
// begins), so a single dropped datagram still leaves two blackout frames.
const StopZeroFrameCount = 3

// terminationPacketCount is how many Stream_Terminated packets Sender.Stop
// sends.
//
// DELIBERATE DIVERGENCE FROM THE STANDARD: Section 6.2.6 says "Three packets
// containing this bit set to 1 shall be sent by sources upon terminating
// sourcing of a universe." Dom specified one. Keeping the count here as a
// named constant makes the divergence auditable and reversible -- setting it
// to 3 is the only change needed to conform.
const terminationPacketCount = 1

// ErrSenderClosed is returned by Send and Stop after Close.
var ErrSenderClosed = errors.New("sacn: sender is closed")

// Config configures a Sender. The zero value is usable except for CID.
type Config struct {
	// CID is the source's Component Identifier (octets 22-37). It is supplied
	// by the caller; this package neither generates nor persists it.
	CID [16]byte

	// Priority is the E1.31 priority (octet 108) for every packet this sender
	// transmits. Zero means DefaultPriority.
	Priority byte

	// Interface is the NIC to transmit from. It is selected elsewhere in the
	// application and passed in. When LocalIP is nil the sender uses this
	// interface's first IPv4 address as its source address. Nil leaves the
	// choice to the operating system's routing table.
	Interface *net.Interface

	// LocalIP overrides the source IPv4 address derived from Interface. When
	// both are nil the socket binds to the unspecified address.
	LocalIP net.IP

	// UnicastTo, when non-nil, is a per-sender destination override: every
	// universe is sent unicast to this IPv4 address instead of to the
	// multicast group from Table 9-10.
	UnicastTo net.IP

	// Port is the UDP destination port. Zero means ACNSDTMulticastPort.
	// Section 9.1.1 only permits an alternative port for unicast.
	Port int
}

// Sender transmits E1.31 Data Packets for one or more universes over a single
// UDP socket, maintaining an independent sequence number per universe.
//
// A Sender is safe for concurrent use across universes. Concurrent Send and
// Stop calls for the *same* universe are a caller-level race: the sequence
// numbers stay well formed, but the ordering of the stop sequence relative to
// live data is then undefined.
type Sender struct {
	cid      [16]byte
	priority byte
	port     int
	unicast  net.IP // nil means multicast
	conn     *net.UDPConn

	mu     sync.Mutex
	next   map[uint16]byte // next Sequence Number per universe
	closed bool
}

// MulticastIP returns the IPv4 multicast address a universe is transmitted to,
// per ANSI E1.31-2025 Table 9-10: octet 1 is 239, octet 2 is 255, octet 3 is
// the universe high byte and octet 4 is the universe low byte.
func MulticastIP(universe uint16) net.IP {
	return net.IPv4(239, 255, byte(universe>>8), byte(universe&0xff))
}

// NewSender opens the sending socket and returns a ready Sender. Close it when
// finished.
func NewSender(cfg Config) (*Sender, error) {
	port := cfg.Port
	if port == 0 {
		port = ACNSDTMulticastPort
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = DefaultPriority
	}

	var unicast net.IP
	if cfg.UnicastTo != nil {
		unicast = cfg.UnicastTo.To4()
		if unicast == nil {
			return nil, fmt.Errorf("sacn: unicast override %v is not an IPv4 address", cfg.UnicastTo)
		}
	}

	localIP, err := resolveLocalIP(cfg)
	if err != nil {
		return nil, err
	}

	lc := net.ListenConfig{}
	// Multicast egress is chosen by the routing table unless IP_MULTICAST_IF
	// says otherwise, so pin it whenever the caller named a NIC and we are
	// actually going to multicast.
	if localIP != nil && unicast == nil {
		var ip4 [4]byte
		copy(ip4[:], localIP)
		lc.Control = func(network, address string, c syscall.RawConn) error {
			var inner error
			if err := c.Control(func(fd uintptr) { inner = setMulticastIf(fd, ip4) }); err != nil {
				return err
			}
			return inner
		}
	}

	bindIP := net.IPv4zero
	if localIP != nil {
		bindIP = localIP
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", (&net.UDPAddr{IP: bindIP}).String())
	if err != nil {
		return nil, fmt.Errorf("sacn: open sending socket: %w", err)
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("sacn: expected a *net.UDPConn, got %T", pc)
	}

	return &Sender{
		cid:      cfg.CID,
		priority: priority,
		port:     port,
		unicast:  unicast,
		conn:     conn,
		next:     make(map[uint16]byte),
	}, nil
}

// resolveLocalIP picks the source address for the sending socket: LocalIP if
// set, otherwise the first IPv4 address of Interface, otherwise nil.
func resolveLocalIP(cfg Config) (net.IP, error) {
	if cfg.LocalIP != nil {
		ip4 := cfg.LocalIP.To4()
		if ip4 == nil {
			return nil, fmt.Errorf("sacn: local address %v is not an IPv4 address", cfg.LocalIP)
		}
		return ip4, nil
	}
	if cfg.Interface == nil {
		return nil, nil
	}
	addrs, err := cfg.Interface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("sacn: read addresses of interface %s: %w", cfg.Interface.Name, err)
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, nil
		}
	}
	return nil, fmt.Errorf("sacn: interface %s has no IPv4 address", cfg.Interface.Name)
}

// Destination returns the UDP address a universe's packets are sent to: the
// unicast override if one was configured, otherwise the Table 9-10 multicast
// group.
func (s *Sender) Destination(universe uint16) *net.UDPAddr {
	if s.unicast != nil {
		return &net.UDPAddr{IP: s.unicast, Port: s.port}
	}
	return &net.UDPAddr{IP: MulticastIP(universe), Port: s.port}
}

// LocalAddr reports the address the sending socket is bound to.
func (s *Sender) LocalAddr() net.Addr { return s.conn.LocalAddr() }

// Priority reports the priority this sender stamps on every packet.
func (s *Sender) Priority() byte { return s.priority }

// Send transmits one E1.31 Data Packet carrying payload on universe. A payload
// shorter than 512 slots is zero padded; a longer one is rejected.
func (s *Sender) Send(universe uint16, payload []byte) error {
	return s.send(universe, payload, 0)
}

// Stop takes a universe off the air: StopZeroFrameCount all-zero data frames
// so the rig is dark regardless of how the receiver handles data loss, then
// terminationPacketCount packet(s) with the Stream_Terminated option bit
// (Section 6.2.6, bit 6) set, then nothing further. The stop packets continue
// the universe's sequence, and the universe's sequence state is discarded
// afterwards so a later Send restarts it cleanly.
//
// Restart caveat: because a restarted universe begins again at
// FirstSequenceNumber, a receiver implementing the Section 6.7.2 discard
// algorithm will drop the first frames of the new stream if the universe was
// stopped on a sequence number in 1..20. Those are data frames in a stream
// that keeps incrementing, so the stream recovers within 20 frames (well under
// a second at any sane refresh rate) rather than failing.
func (s *Sender) Stop(universe uint16) error {
	blackout := make([]byte, maxSlots)
	for i := 0; i < StopZeroFrameCount; i++ {
		if err := s.send(universe, blackout, 0); err != nil {
			return err
		}
	}
	// Section 6.2.6: "Any property values in an E1.31 Data Packet containing
	// this bit shall be ignored", so the payload here is immaterial; zeroes
	// keep it consistent with the frames before it.
	for i := 0; i < terminationPacketCount; i++ {
		if err := s.send(universe, blackout, OptionStreamTerminated); err != nil {
			return err
		}
	}

	s.mu.Lock()
	delete(s.next, universe)
	s.mu.Unlock()
	return nil
}

// Close shuts the sending socket. It does not transmit a stop sequence; call
// Stop for each live universe first. Close is idempotent.
func (s *Sender) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.next = make(map[uint16]byte)
	s.mu.Unlock()
	return s.conn.Close()
}

// send encodes and transmits one packet, allocating the universe's next
// sequence number.
func (s *Sender) send(universe uint16, payload []byte, options byte) error {
	if universe == 0 || universe > maxUniverse {
		return ErrInvalidUniverse
	}
	if len(payload) > maxSlots {
		return fmt.Errorf("sACN payload has %d slots; maximum is %d", len(payload), maxSlots)
	}

	seq, err := s.nextSequence(universe)
	if err != nil {
		return err
	}
	pkt, err := EncodeDataPacketWithOptions(payload, s.cid, SourceName, s.priority, seq, universe, options)
	if err != nil {
		return err
	}
	if _, err := s.conn.WriteToUDP(pkt, s.Destination(universe)); err != nil {
		return fmt.Errorf("sacn: send universe %d: %w", universe, err)
	}
	return nil
}

// nextSequence returns the Sequence Number to stamp on the next packet for
// universe and advances the counter. Per Section 6.2.5 the sequence is
// maintained separately for every universe, increments by one per packet, and
// wraps to zero -- which byte arithmetic does for free.
func (s *Sender) nextSequence(universe uint16) (byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrSenderClosed
	}
	// A universe absent from the map has never been sent on.
	seq, ok := s.next[universe]
	if !ok {
		seq = FirstSequenceNumber
	}
	s.next[universe] = seq + 1
	return seq, nil
}
