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

// --- RDM-LOG20: a node's reply after ArtIpProg arrives from its NEW address --

// TestProgramIPConfirmedFromNewAddress reproduces RDM-LOG20: EN4 was
// programmed from 2.11.90.4 to 2.11.90.12, that succeeded, and the
// confirming ArtIpProgReply arrived from 2.11.90.12 — the node's NEW
// address — not 2.11.90.4, the address the request was unicast to. Before
// the fix, handleIPProgReply required the reply's source to equal the
// waiter's registered (old) key, so this legitimate confirmation was
// silently discarded and ProgramIP timed out even though the node had
// already changed address successfully.
func TestProgramIPConfirmedFromNewAddress(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "2.11.90.4")
	newAddr := netip.MustParseAddr("2.11.90.12")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		// The node already moved: its reply comes from newAddr, not the
		// old address (key.IP) the request was sent to.
		s.handleIPProgReply(artnet.IpProgReply{CurrentIP: newAddr.As4()}, netip.AddrPortFrom(newAddr, ArtNetUDPPort))
	}

	res, err := s.ProgramIP(context.Background(), key, newAddr, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false)
	if err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed true: the node replied (from its new address) — this must not read as a timeout/no-reply")
	}
}

// TestProgramIPRekeysNodeTableToNewAddress asserts the node table follows a
// successfully-reprogrammed node to its new address: the old NodeKey entry
// is gone (so it can no longer silently swallow a command addressed to it,
// RDM-LOG20's actual failure), the node appears at its new key, and it is
// never present at both — no stale entry, no duplicate ghost.
func TestProgramIPRekeysNodeTableToNewAddress(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "2.11.90.4")
	newAddr := netip.MustParseAddr("2.11.90.12")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		s.handleIPProgReply(artnet.IpProgReply{CurrentIP: newAddr.As4()}, netip.AddrPortFrom(newAddr, ArtNetUDPPort))
	}

	if _, err := s.ProgramIP(context.Background(), key, newAddr, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false); err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}

	if _, ok := s.Node(key); ok {
		t.Fatal("stale entry still present at the node's old address")
	}
	newKey := NodeKey{IP: newAddr, BindIndex: 1}
	moved, ok := s.Node(newKey)
	if !ok {
		t.Fatal("expected the node table to follow the node to its new address")
	}
	if moved.ShortName != "Node" {
		t.Fatalf("expected the moved entry to retain the node's prior data, got %+v", moved)
	}

	count := 0
	for _, n := range s.Nodes() {
		if n.Key.IP == newAddr || n.Key.IP == key.IP {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected the node to appear exactly once across its old/new address, got %d entries: %+v", count, s.Nodes())
	}
}

// TestProgramIPFollowUpReachesNewAddress models the rest of RDM-LOG20: after
// the node table follows the node to its new address, a follow-up ProgramIP
// call addressed to that new key reaches the node (is confirmed and sent to
// the right address) — and the old key, now gone from the table, fails
// loudly (unknown node) instead of silently sending into the void.
func TestProgramIPFollowUpReachesNewAddress(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	key := seedNode(t, s, "2.11.90.4")
	newAddr := netip.MustParseAddr("2.11.90.12")
	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		s.handleIPProgReply(artnet.IpProgReply{CurrentIP: newAddr.As4()}, netip.AddrPortFrom(newAddr, ArtNetUDPPort))
	}
	if _, err := s.ProgramIP(context.Background(), key, newAddr, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false); err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}

	// The old key is gone — a caller that (wrongly) still targets it must
	// fail loudly, not silently swallow the request the way RDM-LOG20's two
	// unanswered retries did.
	if _, err := s.ProgramIP(context.Background(), key, key.IP, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false); err == nil {
		t.Fatal("expected an error targeting the node's old (now-gone) key, not silence")
	}

	// The follow-up, addressed to the node's current (new) key, as the
	// re-keyed Nodes screen now would, reaches it.
	newKey := NodeKey{IP: newAddr, BindIndex: 1}
	res, err := s.ProgramIP(context.Background(), newKey, key.IP, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false)
	if err != nil {
		t.Fatalf("follow-up ProgramIP: %v", err)
	}
	if !res.Confirmed {
		t.Fatal("expected the follow-up ProgramIP to be confirmed")
	}
	var sentToNew bool
	for _, sp := range tr.Sent() {
		if sp.Packet.Kind == artnet.KindIpProg && sp.Dst.Addr() == newAddr {
			sentToNew = true
		}
	}
	if !sentToNew {
		t.Fatal("expected the follow-up command to be addressed to the node's current (new) address")
	}
}

// TestProgramIPRekeysAllBindIndicesAtOldIP reproduces a second bug found
// while browser-proving RDM-LOG20's fix against a bind-per-port node (a
// real EN4 reports 3 bind indices, all from the same IP — see NodeKey's
// doc comment): ProgramIP is only ever called against ONE NodeKey (the
// device's IP editor defaults to BindIndex 1), so only bind 1 had a
// pending waiter to trigger a rekey. An address change is device-wide,
// though — every bind index at the old IP needs to move, or binds 2/3 are
// left exactly as stranded (and, once ArtPoll rediscovers them at the new
// address, exactly as duplicated) as the single-bind bug RDM-LOG20
// reported in the first place.
func TestProgramIPRekeysAllBindIndicesAtOldIP(t *testing.T) {
	s, _, tr := newSession(t, ArtNetConfig{})
	oldIP := netip.MustParseAddr("2.11.90.4")
	newIP := netip.MustParseAddr("2.11.90.12")
	bind1 := seedNode(t, s, "2.11.90.4") // BindIndex 1

	// Seed bind 2 and bind 3 at the same IP directly (seedNode always
	// normalises to bind 1) — mirrors realEN4PortReplies in cmd/benny512's
	// demo.
	for _, bind := range []byte{2, 3} {
		s.HandlePollReply(artnet.PollReply{
			IPAddress: oldIP.As4(), BindIndex: bind,
			ShortName: "Port", LongName: "NETRON EN4",
			NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0}, Status1: 0x02,
		}, netip.AddrPortFrom(oldIP, ArtNetUDPPort))
	}

	tr.OnSend = func(sp SentPacket) {
		if sp.Packet.Kind != artnet.KindIpProg {
			return
		}
		s.handleIPProgReply(artnet.IpProgReply{CurrentIP: newIP.As4()}, netip.AddrPortFrom(newIP, ArtNetUDPPort))
	}

	// ProgramIP is only ever called against bind 1 (the IP editor's key).
	if _, err := s.ProgramIP(context.Background(), bind1, newIP, netip.MustParseAddr("255.255.0.0"), netip.Addr{}, false); err != nil {
		t.Fatalf("ProgramIP: %v", err)
	}

	for _, bind := range []byte{1, 2, 3} {
		if _, ok := s.Node(NodeKey{IP: oldIP, BindIndex: bind}); ok {
			t.Fatalf("bind %d still present at the old IP — stranded, would silently swallow commands", bind)
		}
		if _, ok := s.Node(NodeKey{IP: newIP, BindIndex: bind}); !ok {
			t.Fatalf("bind %d missing at the new IP — the node table did not follow it", bind)
		}
	}

	// No duplicates: every (IP, bind) pair for this device appears exactly
	// once across the whole table.
	seen := map[NodeKey]int{}
	for _, n := range s.Nodes() {
		if n.Key.IP == oldIP || n.Key.IP == newIP {
			seen[NodeKey{BindIndex: n.Key.BindIndex}]++
		}
	}
	for bind, count := range seen {
		if count != 1 {
			t.Fatalf("bind %d appears %d times across old/new IP, want 1", bind.BindIndex, count)
		}
	}
}
