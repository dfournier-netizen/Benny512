package transport

import (
	"net/netip"
	"testing"
	"time"
)

// TestUDPLoopbackSmoke opens two UDP transports on loopback and exercises
// Send/Inbound between them. Sandboxed CI environments sometimes block even
// loopback UDP (seccomp/netns restrictions); such a failure is treated as a
// skip rather than a hard failure so this test doesn't flake the suite in an
// environment that simply doesn't allow it.
func TestUDPLoopbackSmoke(t *testing.T) {
	a, err := Listen(Config{BindAddr: netip.MustParseAddr("127.0.0.1"), Port: 0})
	if err != nil {
		t.Skipf("sandbox appears to block UDP sockets: %v", err)
	}
	defer a.Close()

	b, err := Listen(Config{BindAddr: netip.MustParseAddr("127.0.0.1"), Port: 0})
	if err != nil {
		t.Skipf("sandbox appears to block UDP sockets: %v", err)
	}
	defer b.Close()

	bap, err := netip.ParseAddrPort(b.conn.LocalAddr().String())
	if err != nil {
		t.Fatalf("parse b addr: %v", err)
	}

	payload := []byte("hello-artnet")
	if err := a.Send(payload, bap); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case in := <-b.Inbound():
		if string(in.Data) != string(payload) {
			t.Errorf("got %q want %q", in.Data, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound datagram (sandbox networking may be restricted)")
	}
}

func TestUDPSendAfterCloseErrors(t *testing.T) {
	a, err := Listen(Config{BindAddr: netip.MustParseAddr("127.0.0.1"), Port: 0})
	if err != nil {
		t.Skipf("sandbox appears to block UDP sockets: %v", err)
	}
	a.Close()
	err = a.Send([]byte("x"), netip.MustParseAddrPort("127.0.0.1:6454"))
	if err != ErrClosed {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}
