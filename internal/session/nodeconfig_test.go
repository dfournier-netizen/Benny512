package session

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
)

func seedNode(t *testing.T, s *ArtNetSession, ip string) NodeKey {
	t.Helper()
	addr := netip.MustParseAddr(ip)
	s.HandlePollReply(artnet.PollReply{
		IPAddress: addr.As4(), ShortName: "Node", LongName: "Test Node",
		NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02,
	}, netip.AddrPortFrom(addr, ArtNetUDPPort))
	return NodeKey{IP: addr, BindIndex: 1}
}

// byteOf is a convenience for building *byte literals in test tables.
func byteOf(v byte) *byte { return &v }

func TestSetNodeNamesConfirmedViaOnSend(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.20")

	// Wire the FakeTransport so the moment SetNodeNames' ArtAddress packet
	// is sent, we synchronously reply with a fresh ArtPollReply — this
	// models "the node replies promptly" without needing a goroutine/clock
	// dance, since OnSend fires synchronously on the same call stack as
	// Send, strictly after sendAndAwaitConfirm has already registered its
	// waiter (see nodeconfig.go's ordering).
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindAddress {
			return
		}
		addr := netip.MustParseAddr("10.0.0.20")
		s.HandlePollReply(artnet.PollReply{
			IPAddress: addr.As4(), ShortName: sp.Packet.Address.ShortName, LongName: sp.Packet.Address.LongName,
			NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}, Status1: 0x02,
		}, netip.AddrPortFrom(addr, ArtNetUDPPort))
	}

	res, err := s.SetNodeNames(context.Background(), key, "NewShort", "New Long Name")
	if err != nil {
		t.Fatalf("SetNodeNames: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true")
	}
	if res.Updated.ShortName != "NewShort" || res.Updated.LongName != "New Long Name" {
		t.Fatalf("updated=%+v", res.Updated)
	}

	sent := tr.Sent()
	var found bool
	for _, sp := range sent {
		if sp.Packet.Kind == artnet.KindAddress {
			found = true
			if sp.Broadcast {
				t.Fatal("ArtAddress should be unicast to the node, not broadcast")
			}
			if sp.Packet.Address.NetSwitch.ShouldProgram() || sp.Packet.Address.SubSwitch.ShouldProgram() {
				t.Fatalf("SetNodeNames should not request Net/Sub-Net programming: %+v", sp.Packet.Address)
			}
		}
	}
	if !found {
		t.Fatal("no ArtAddress packet observed")
	}
}

func TestSetNodeNamesTimeout(t *testing.T) {
	s, clock, tport := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.21")
	// No OnSend hook: the node never replies.

	type result struct {
		res ConfigResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.SetNodeNames(context.Background(), key, "X", "Y")
		done <- result{res, err}
	}()

	// Driven by real, blocking synchronization, not a spin loop bounded by
	// an iteration count. A fixed-count Gosched spin can burn through its
	// whole budget in low-single-digit milliseconds of real CPU time,
	// starving the goroutine above of any timeslice at all under
	// contention (confirmed by dumping goroutine stacks on a captured
	// failure in the sibling internal/web package: the goroutine sat
	// "runnable", never scheduled, the entire time this loop kept
	// "making progress" spending iterations that meant nothing) — a real
	// scheduler race exactly like racing a wall-clock deadline is, just
	// losing in the opposite direction. tport.SentSignal() reacts the
	// instant SetNodeNames actually puts a request on the wire; the 1ms
	// real ticker is the fallback that keeps fake time moving between
	// sends (an ack-timer/backoff retry the controller schedules on its
	// own has no fresh send to react to until the clock is already past
	// its deadline). Neither is a spin: both block for real between
	// events, so this goroutine holds no CPU while the other one needs to
	// run. 30s is a deadlock backstop only, never a completion budget.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	backstop := time.NewTimer(30 * time.Second)
	defer backstop.Stop()
	for {
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("SetNodeNames: %v", r.err)
			}
			if r.res.Confirmed {
				t.Fatal("expected Confirmed false (no reply ever sent)")
			}
			return
		case <-tport.SentSignal():
			clock.Advance(100 * time.Millisecond)
		case <-ticker.C:
			clock.Advance(100 * time.Millisecond)
		case <-backstop.C:
			t.Fatal("timed out waiting for SetNodeNames to resolve (no progress for 30s — a real hang, not scheduling jitter)")
			return
		}
	}
}

func TestSetNodeNamesUnknownNode(t *testing.T) {
	s, _, _ := newSession(t, ArtNetConfig{})
	_, err := s.SetNodeNames(context.Background(), NodeKey{IP: netip.MustParseAddr("10.0.0.99")}, "X", "Y")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestSetPortAddressesFieldSelection(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.22")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindAddress {
			return
		}
		addr := netip.MustParseAddr("10.0.0.22")
		s.HandlePollReply(artnet.PollReply{IPAddress: addr.As4(), NumPorts: 1, PortTypes: [4]byte{0x80}}, netip.AddrPortFrom(addr, ArtNetUDPPort))
	}

	update := PortAddressUpdate{
		NetSwitch: byteOf(3),
		SwOut:     [4]*byte{byteOf(5), nil, nil, nil},
		Command:   artnet.AcMergeLTP0,
	}
	res, err := s.SetPortAddresses(context.Background(), key, update)
	if err != nil {
		t.Fatalf("SetPortAddresses: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true")
	}

	var got artnet.Address
	for _, sp := range tr.Sent() {
		if sp.Packet.Kind == artnet.KindAddress {
			got = sp.Packet.Address
		}
	}
	if !got.NetSwitch.ShouldProgram() || got.NetSwitch.Value() != 3 {
		t.Fatalf("NetSwitch = %+v, want programmed to 3", got.NetSwitch)
	}
	if got.SubSwitch.ShouldProgram() {
		t.Fatalf("SubSwitch should be untouched: %+v", got.SubSwitch)
	}
	if !got.SwOut[0].ShouldProgram() || got.SwOut[0].Value() != 5 {
		t.Fatalf("SwOut[0] = %+v, want programmed to 5", got.SwOut[0])
	}
	for i := 1; i < 4; i++ {
		if got.SwOut[i].ShouldProgram() {
			t.Fatalf("SwOut[%d] should be untouched: %+v", i, got.SwOut[i])
		}
	}
	if got.Command != artnet.AcMergeLTP0 {
		t.Fatalf("Command = %v, want AcMergeLTP0", got.Command)
	}
}

func TestSetInputEnabled(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.23")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindInput {
			return
		}
		addr := netip.MustParseAddr("10.0.0.23")
		s.HandlePollReply(artnet.PollReply{IPAddress: addr.As4(), NumPorts: 4, PortTypes: [4]byte{0x80, 0x80, 0x80, 0x80}}, netip.AddrPortFrom(addr, ArtNetUDPPort))
	}
	res, err := s.SetInputEnabled(context.Background(), key, [4]bool{true, false, true, false})
	if err != nil {
		t.Fatalf("SetInputEnabled: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true")
	}
	var got artnet.Input
	for _, sp := range tr.Sent() {
		if sp.Packet.Kind == artnet.KindInput {
			got = sp.Packet.Input
		}
	}
	if got.InputStates[0].Disabled() || !got.InputStates[1].Disabled() || got.InputStates[2].Disabled() || !got.InputStates[3].Disabled() {
		t.Fatalf("InputStates = %+v", got.InputStates)
	}
}

// TestProgramIPStaticAddressAndGateway replaces the previous
// "...AndGatewayWarning" test — ProgramIP no longer returns a "gateway not
// sent" warning; the Art-Net 4 spec confirms ArtIpProg has bit 4
// ("Program default gateway") and a dedicated ProgGateway field, so a real
// gateway address supplied here is now actually sent on the wire.
func TestProgramIPStaticAddressAndGateway(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.24")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		// Model the node's ArtIpProgReply confirmation path.
		s.handleIPProgReply(artnet.IpProgReply{CurrentIP: [4]byte{10, 0, 0, 50}}, netip.AddrPortFrom(netip.MustParseAddr("10.0.0.24"), ArtNetUDPPort))
	}

	ip := netip.MustParseAddr("10.0.0.50")
	mask := netip.MustParseAddr("255.255.255.0")
	gateway := netip.MustParseAddr("10.0.0.1")
	res, err := s.ProgramIP(context.Background(), key, ip, mask, gateway, false)
	if err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true")
	}
	if res.Warning != "" {
		t.Fatalf("expected no warning now that gateway programming is implemented, got %q", res.Warning)
	}

	var got artnet.IpProg
	for _, sp := range tr.Sent() {
		if sp.Packet.Kind == artnet.KindIpProg {
			got = sp.Packet.IpProg
		}
	}
	if got.ProgIP != [4]byte{10, 0, 0, 50} {
		t.Fatalf("ProgIP = %v", got.ProgIP)
	}
	if got.ProgSubnetMask != [4]byte{255, 255, 255, 0} {
		t.Fatalf("ProgSubnetMask = %v", got.ProgSubnetMask)
	}
	if got.ProgGateway != [4]byte{10, 0, 0, 1} {
		t.Fatalf("ProgGateway = %v, want the requested gateway to actually be sent", got.ProgGateway)
	}
	if got.Command&artnet.IpProgEnable == 0 || got.Command&artnet.IpProgProgramIP == 0 ||
		got.Command&artnet.IpProgProgramSubnetMask == 0 || got.Command&artnet.IpProgProgramGateway == 0 {
		t.Fatalf("Command = 0x%02X, missing expected bits", byte(got.Command))
	}
	if byte(got.Command) != 0x80|0x10|0x04|0x02 {
		t.Fatalf("Command = 0x%02X, want 0x96 (enable|program-gateway|program-IP|program-mask)", byte(got.Command))
	}
	if got.Command&artnet.IpProgEnableDHCP != 0 {
		t.Fatal("DHCP bit should not be set for a static-IP request")
	}
}

func TestProgramIPDHCP(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "10.0.0.25")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		s.handleIPProgReply(artnet.IpProgReply{}, netip.AddrPortFrom(netip.MustParseAddr("10.0.0.25"), ArtNetUDPPort))
	}
	res, err := s.ProgramIP(context.Background(), key, netip.Addr{}, netip.Addr{}, netip.Addr{}, true)
	if err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true")
	}
	if res.Warning != "" {
		t.Fatalf("no gateway requested, expected no warning, got %q", res.Warning)
	}
	var got artnet.IpProg
	for _, sp := range tr.Sent() {
		if sp.Packet.Kind == artnet.KindIpProg {
			got = sp.Packet.IpProg
		}
	}
	if got.Command&artnet.IpProgEnableDHCP == 0 {
		t.Fatal("expected DHCP bit set")
	}
	if got.Command&artnet.IpProgProgramIP != 0 || got.Command&artnet.IpProgProgramSubnetMask != 0 {
		t.Fatal("DHCP request should not also request static IP/mask programming")
	}
}
