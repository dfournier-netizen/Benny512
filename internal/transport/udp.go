// Package transport implements the real UDP transport used in production:
// session.Transport over a net.PacketConn bound to a chosen network
// interface, plus NIC enumeration for the Settings screen.
//
// Layering rule (architecture rev 5 §2): this is the only package in Benny512
// that touches a socket for Art-Net traffic. Everything above it (session
// engines, registry, params) is transport-agnostic and is tested against
// session.FakeTransport instead.
package transport

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"benny512/internal/session"
)

// ErrClosed is returned by Send/Broadcast after Close.
var ErrClosed = errors.New("transport: closed")

// UDP implements session.Transport over a bound net.PacketConn. It always
// listens on ArtNetUDPPort (6454); production code binds a single NIC's
// address so multi-homed machines don't answer or broadcast out the wrong
// interface.
type UDP struct {
	conn      net.PacketConn
	broadcast netip.AddrPort

	mu      sync.Mutex
	closed  bool
	inbound chan session.Inbound

	wg sync.WaitGroup
}

// Config selects how the UDP transport binds.
type Config struct {
	// BindAddr is the local IPv4 address to bind (e.g. a NIC's address).
	// The zero value binds INADDR_ANY (0.0.0.0), which receives on every
	// interface but whose Broadcast still requires a usable BroadcastAddr.
	BindAddr netip.Addr
	// Port defaults to session.ArtNetUDPPort (6454).
	Port uint16
	// BroadcastAddr is the directed-subnet broadcast address to send to.
	// Falls back to the global 255.255.255.255 if unset/invalid.
	BroadcastAddr netip.Addr
	// InboundBuffer sizes the channel returned by Inbound; defaults to 256.
	InboundBuffer int
}

// Listen opens a UDP socket per cfg and starts the receive loop. Callers
// must call Close when done.
func Listen(cfg Config) (*UDP, error) {
	port := cfg.Port
	if port == 0 {
		port = session.ArtNetUDPPort
	}
	bindIP := "0.0.0.0"
	if cfg.BindAddr.IsValid() && !cfg.BindAddr.IsUnspecified() {
		bindIP = cfg.BindAddr.String()
	}
	laddr := fmt.Sprintf("%s:%d", bindIP, port)

	conn, err := net.ListenPacket("udp4", laddr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen %s: %w", laddr, err)
	}

	bcast := cfg.BroadcastAddr
	if !bcast.IsValid() {
		bcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	}

	buf := cfg.InboundBuffer
	if buf <= 0 {
		buf = 256
	}

	u := &UDP{
		conn:      conn,
		broadcast: netip.AddrPortFrom(bcast, port),
		inbound:   make(chan session.Inbound, buf),
	}
	u.wg.Add(1)
	go u.recvLoop()
	return u, nil
}

// LocalAddr returns the bound local address.
func (u *UDP) LocalAddr() net.Addr { return u.conn.LocalAddr() }

// BroadcastAddr returns the configured broadcast destination.
func (u *UDP) BroadcastAddr() netip.AddrPort { return u.broadcast }

func (u *UDP) recvLoop() {
	defer u.wg.Done()
	defer close(u.inbound)
	buf := make([]byte, 65535)
	for {
		n, addr, err := u.conn.ReadFrom(buf)
		if err != nil {
			return // socket closed
		}
		var from netip.AddrPort
		if ua, ok := addr.(*net.UDPAddr); ok {
			a, ok2 := netip.AddrFromSlice(ua.IP.To4())
			if !ok2 {
				a, _ = netip.AddrFromSlice(ua.IP)
			}
			from = netip.AddrPortFrom(a, uint16(ua.Port))
		}
		data := make([]byte, n)
		copy(data, buf[:n])

		u.mu.Lock()
		closed := u.closed
		u.mu.Unlock()
		if closed {
			return
		}
		select {
		case u.inbound <- session.Inbound{Data: data, From: from}:
		default:
			// Drop rather than block the socket read loop; a stalled
			// consumer should not cause UDP receive-buffer overflow upstream.
		}
	}
}

// Send implements session.Transport.
func (u *UDP) Send(data []byte, dst netip.AddrPort) error {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return ErrClosed
	}
	u.mu.Unlock()
	_, err := u.conn.WriteTo(data, net.UDPAddrFromAddrPort(dst))
	return err
}

// Broadcast implements session.Transport.
func (u *UDP) Broadcast(data []byte) error {
	return u.Send(data, u.broadcast)
}

// Inbound implements session.Transport.
func (u *UDP) Inbound() <-chan session.Inbound { return u.inbound }

// Close shuts down the socket and stops the receive loop.
func (u *UDP) Close() error {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return nil
	}
	u.closed = true
	u.mu.Unlock()
	err := u.conn.Close()
	u.wg.Wait()
	return err
}

var _ session.Transport = (*UDP)(nil)
