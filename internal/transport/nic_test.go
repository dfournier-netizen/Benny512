package transport

import (
	"net/netip"
	"testing"
)

func TestBroadcastAddrFor(t *testing.T) {
	cases := []struct {
		ip     string
		prefix int
		want   string
	}{
		{"192.168.1.42", 24, "192.168.1.255"},
		{"10.0.0.5", 8, "10.255.255.255"},
		{"172.16.5.200", 30, "172.16.5.203"},
		{"192.168.1.1", 32, "192.168.1.1"},
		{"192.168.1.1", 0, "255.255.255.255"},
		{"2.11.90.2", 16, "2.11.255.255"},
	}
	for _, c := range cases {
		ip := netip.MustParseAddr(c.ip)
		got, ok := BroadcastAddrFor(ip, c.prefix)
		if !ok {
			t.Fatalf("%s/%d: not ok", c.ip, c.prefix)
		}
		if got.String() != c.want {
			t.Errorf("%s/%d = %s, want %s", c.ip, c.prefix, got, c.want)
		}
	}
}

func TestBroadcastAddrForRejectsIPv6AndBadPrefix(t *testing.T) {
	v6 := netip.MustParseAddr("::1")
	if _, ok := BroadcastAddrFor(v6, 24); ok {
		t.Error("expected IPv6 to be rejected")
	}
	v4 := netip.MustParseAddr("192.168.1.1")
	if _, ok := BroadcastAddrFor(v4, 33); ok {
		t.Error("expected prefix > 32 to be rejected")
	}
	if _, ok := BroadcastAddrFor(v4, -1); ok {
		t.Error("expected negative prefix to be rejected")
	}
}

func TestListInterfaces(t *testing.T) {
	ifs, err := ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	// The sandbox always has at least loopback with an IPv4 address.
	if len(ifs) == 0 {
		t.Fatal("expected at least one interface with an IPv4 address")
	}
	foundLoopback := false
	for _, i := range ifs {
		if i.Loopback {
			foundLoopback = true
		}
		if len(i.IPv4) == 0 {
			t.Errorf("interface %s reported with no IPv4 addrs", i.Name)
		}
	}
	if !foundLoopback {
		t.Error("expected a loopback interface to be present")
	}
}
