package capture

import (
	"net/netip"
	"strings"
	"testing"

	"benny512/internal/artnet"
)

// TestIsNodeConfigKind is the routing-classifier contract for the four
// rare, user-initiated node-configuration opcodes report task 1 added
// --lognodes for: they're in, everything else (including the periodic pair
// and the RDM-family kinds) is out.
func TestIsNodeConfigKind(t *testing.T) {
	for _, k := range []string{"ArtAddress", "ArtInput", "ArtIpProg", "ArtIpProgReply"} {
		if !IsNodeConfigKind(k) {
			t.Errorf("IsNodeConfigKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"ArtPoll", "ArtPollReply", "ArtDmx", "ArtRdm", "unknown", ""} {
		if IsNodeConfigKind(k) {
			t.Errorf("IsNodeConfigKind(%q) = true, want false", k)
		}
	}
}

// TestIsPeriodicNodeKind is IsNodeConfigKind's complementary contract for
// the two periodic-background-chatter opcodes.
func TestIsPeriodicNodeKind(t *testing.T) {
	for _, k := range []string{"ArtPoll", "ArtPollReply"} {
		if !IsPeriodicNodeKind(k) {
			t.Errorf("IsPeriodicNodeKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"ArtAddress", "ArtInput", "ArtIpProg", "ArtIpProgReply", "ArtDmx", "ArtRdm", "unknown", ""} {
		if IsPeriodicNodeKind(k) {
			t.Errorf("IsPeriodicNodeKind(%q) = true, want false", k)
		}
	}
}

func encodePollReplyPacket(t *testing.T, p artnet.PollReply) []byte {
	t.Helper()
	return artnet.Encode(artnet.Packet{Kind: artnet.KindPollReply, PollReply: p})
}

// TestDecodeEntry_PollReplyPortFields is the core regression test for the
// bench bug this report task exists to diagnose: every port's raw
// PortTypes/GoodInput/GoodOutputA/GoodOutputB/SwIn/SwOut byte, plus
// NetSwitch/SubSwitch, must survive into PollReplyDetail unchanged and
// alongside their decoded interpretation — never the interpretation alone.
func TestDecodeEntry_PollReplyPortFields(t *testing.T) {
	pr := artnet.PollReply{
		IPAddress: [4]byte{2, 11, 90, 2},
		ShortName: "EN4", LongName: "Netron EN4",
		NumPorts:    4,
		NetSwitch:   0x03,
		SubSwitch:   0x05,
		Status1:     0x02,
		PortTypes:   [4]byte{0x00, 0x00, 0x00, 0x00}, // the reported bug: both bits zero
		GoodInput:   [4]byte{0x80, 0, 0, 0},
		GoodOutputA: [4]byte{0, 0x80, 0, 0},
		GoodOutputB: [4]byte{0, 0, 0x80, 0},
		SwIn:        [4]byte{1, 2, 3, 4},
		SwOut:       [4]byte{5, 6, 7, 8},
	}
	raw := encodePollReplyPacket(t, pr)
	e := DecodeEntry(DirIn, testPeer, raw)

	if e.Kind != "ArtPollReply" {
		t.Fatalf("Kind = %q, want ArtPollReply", e.Kind)
	}
	if e.PollReply == nil {
		t.Fatal("PollReply detail is nil")
	}
	d := e.PollReply
	if d.NumPorts != 4 {
		t.Errorf("NumPorts = %d, want 4", d.NumPorts)
	}
	if d.NetSwitchRaw != 0x03 || d.SubSwitchRaw != 0x05 {
		t.Errorf("NetSwitchRaw/SubSwitchRaw = 0x%02X/0x%02X, want 0x03/0x05", d.NetSwitchRaw, d.SubSwitchRaw)
	}
	if len(d.Ports) != 4 {
		t.Fatalf("got %d ports, want 4 (always all 4 physical slots)", len(d.Ports))
	}
	for i, p := range d.Ports {
		if p.Index != i {
			t.Errorf("Ports[%d].Index = %d, want %d", i, p.Index, i)
		}
		if p.PortTypesRaw != 0 {
			t.Errorf("port %d PortTypesRaw = 0x%02X, want 0x00 (the reported bug)", i, p.PortTypesRaw)
		}
		if p.InputSupported || p.OutputSupported {
			t.Errorf("port %d decoded input=%v output=%v, want both false for PortTypesRaw=0x00", i, p.InputSupported, p.OutputSupported)
		}
	}
	if d.Ports[0].GoodInputRaw != 0x80 {
		t.Errorf("port 0 GoodInputRaw = 0x%02X, want 0x80", d.Ports[0].GoodInputRaw)
	}
	if d.Ports[1].GoodOutputARaw != 0x80 {
		t.Errorf("port 1 GoodOutputARaw = 0x%02X, want 0x80", d.Ports[1].GoodOutputARaw)
	}
	if d.Ports[2].GoodOutputBRaw != 0x80 {
		t.Errorf("port 2 GoodOutputBRaw = 0x%02X, want 0x80", d.Ports[2].GoodOutputBRaw)
	}
	if d.Ports[3].SwInRaw != 4 || d.Ports[3].SwOutRaw != 8 {
		t.Errorf("port 3 SwInRaw/SwOutRaw = %d/%d, want 4/8", d.Ports[3].SwInRaw, d.Ports[3].SwOutRaw)
	}

	// The raw byte must show up in the rendered text next to the decoded
	// interpretation, never the interpretation alone (report task's stated
	// requirement).
	text := FormatEntryText(e)
	for _, want := range []string{"portTypes=0x00", "input=false", "output=false", "netSwitch=0x03", "subSwitch=0x05", "swIn=0x04", "swOut=0x08"} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatEntryText missing %q:\n%s", want, text)
		}
	}
}

// TestDecodeEntry_PollReplyInputOutputBitsDecoded confirms the
// bit7=output/bit6=input decode (matching internal/session's own
// established reading of this field) for a node that correctly advertises
// both directions on one port.
func TestDecodeEntry_PollReplyInputOutputBitsDecoded(t *testing.T) {
	pr := artnet.PollReply{NumPorts: 1, PortTypes: [4]byte{0xC0, 0, 0, 0}}
	raw := encodePollReplyPacket(t, pr)
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.PollReply == nil {
		t.Fatal("PollReply detail is nil")
	}
	p := e.PollReply.Ports[0]
	if !p.InputSupported || !p.OutputSupported {
		t.Errorf("port 0 input=%v output=%v, want both true for PortTypesRaw=0xC0", p.InputSupported, p.OutputSupported)
	}
}

// TestRingHexThresholdNeverTruncatesPollReply confirms the report task's
// explicit ask: an ArtPollReply's full hex must survive HexThreshold, since
// it's the primary evidence for the "n/a" bug. A real ArtPollReply (239
// bytes) is well under HexThreshold=600 regardless, but this pins that fact
// down as a regression test rather than leaving it as an unverified
// assumption.
func TestRingHexThresholdNeverTruncatesPollReply(t *testing.T) {
	pr := artnet.PollReply{NumPorts: 4, ShortName: "EN4", LongName: "Netron EN4 (demo)"}
	raw := encodePollReplyPacket(t, pr)
	if len(raw) > HexThreshold {
		t.Fatalf("a real ArtPollReply is %d bytes, want <= HexThreshold (%d) so its hex is never truncated", len(raw), HexThreshold)
	}
	r := New(10)
	e := r.Add(DecodeEntry(DirIn, testPeer, raw))
	if e.HexTrunc {
		t.Error("HexTrunc = true, want false: ArtPollReply must never be truncated")
	}
	if e.Hex == "" {
		t.Error("Hex is empty, want the full ArtPollReply hex present")
	}
}

// TestDecodeEntry_AddressDetail covers ArtAddress's raw+decoded rendering:
// command byte, per-port SwIn/SwOut, NetSwitch/SubSwitch, names.
func TestDecodeEntry_AddressDetail(t *testing.T) {
	a := artnet.Address{
		NetSwitch: artnet.ProgramSwitch(3), BindIndex: 1,
		ShortName: "Bench-EN4", LongName: "Netron EN4 (demo)",
		SwIn:      [4]artnet.SwitchEntry{artnet.ProgramSwitch(1), artnet.NoChangeSwitch, artnet.NoChangeSwitch, artnet.NoChangeSwitch},
		SubSwitch: artnet.NoChangeSwitch,
		Command:   artnet.AcMergeLTP0,
	}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindAddress, Address: a})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.Kind != "ArtAddress" {
		t.Fatalf("Kind = %q, want ArtAddress", e.Kind)
	}
	if e.NodeConfig == nil {
		t.Fatal("NodeConfig detail is nil")
	}
	d := e.NodeConfig
	if d.SubKind != "Address" {
		t.Errorf("SubKind = %q, want Address", d.SubKind)
	}
	if d.CommandRaw != byte(artnet.AcMergeLTP0) {
		t.Errorf("CommandRaw = 0x%02X, want 0x%02X", d.CommandRaw, byte(artnet.AcMergeLTP0))
	}
	if d.CommandName != "MergeLTP[0]" {
		t.Errorf("CommandName = %q, want MergeLTP[0]", d.CommandName)
	}
	if d.ShortName != "Bench-EN4" || d.LongName != "Netron EN4 (demo)" {
		t.Errorf("ShortName/LongName = %q/%q, want Bench-EN4/Netron EN4 (demo)", d.ShortName, d.LongName)
	}
	if d.SwInRaw[0] != byte(artnet.ProgramSwitch(1)) {
		t.Errorf("SwInRaw[0] = 0x%02X, want 0x%02X", d.SwInRaw[0], byte(artnet.ProgramSwitch(1)))
	}
	if d.NetSwitchRaw != byte(artnet.ProgramSwitch(3)) {
		t.Errorf("NetSwitchRaw = 0x%02X, want 0x%02X", d.NetSwitchRaw, byte(artnet.ProgramSwitch(3)))
	}

	text := FormatEntryText(e)
	for _, want := range []string{"ArtAddress", "MergeLTP[0]", "Bench-EN4", "hex:"} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatEntryText missing %q:\n%s", want, text)
		}
	}
}

// TestDecodeEntry_AddressUnknownCommandFallsBackToHex confirms an AcCommand
// value this package has no confirmed mnemonic for renders as a plain hex
// label rather than a guessed name.
func TestDecodeEntry_AddressUnknownCommandFallsBackToHex(t *testing.T) {
	a := artnet.Address{Command: artnet.AcCommand(0x77)}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindAddress, Address: a})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.NodeConfig.CommandName != "0x77" {
		t.Errorf("CommandName = %q, want 0x77", e.NodeConfig.CommandName)
	}
}

// TestDecodeEntry_InputDetail covers ArtInput's raw+decoded rendering.
func TestDecodeEntry_InputDetail(t *testing.T) {
	in := artnet.Input{BindIndex: 1, InputStates: [4]artnet.InputDisable{1, 0, 1, 0}}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindInput, Input: in})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.Kind != "ArtInput" {
		t.Fatalf("Kind = %q, want ArtInput", e.Kind)
	}
	if e.NodeConfig == nil {
		t.Fatal("NodeConfig detail is nil")
	}
	d := e.NodeConfig
	if d.SubKind != "Input" {
		t.Errorf("SubKind = %q, want Input", d.SubKind)
	}
	want := [4]bool{true, false, true, false}
	if d.InputDisabled != want {
		t.Errorf("InputDisabled = %v, want %v", d.InputDisabled, want)
	}
	if d.InputRaw != [4]byte{1, 0, 1, 0} {
		t.Errorf("InputRaw = %v, want [1 0 1 0]", d.InputRaw)
	}
}

// TestDecodeEntry_IpProgDetail covers ArtIpProg's command-bitfield decode
// alongside its raw byte.
func TestDecodeEntry_IpProgDetail(t *testing.T) {
	p := artnet.IpProg{
		Command:        artnet.IpProgEnable | artnet.IpProgProgramIP | artnet.IpProgProgramSubnetMask,
		ProgIP:         [4]byte{10, 10, 10, 5},
		ProgSubnetMask: [4]byte{255, 255, 255, 0},
	}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindIpProg, IpProg: p})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.Kind != "ArtIpProg" {
		t.Fatalf("Kind = %q, want ArtIpProg", e.Kind)
	}
	d := e.NodeConfig
	if d == nil {
		t.Fatal("NodeConfig detail is nil")
	}
	if !d.Enable || !d.ProgramIP || !d.ProgramSubnetMask || d.EnableDHCP || d.SetDefault {
		t.Errorf("decoded command bits = %+v, want enable/programIP/programSubnetMask true, rest false", d)
	}
	if d.ProgIP != "10.10.10.5" {
		t.Errorf("ProgIP = %q, want 10.10.10.5", d.ProgIP)
	}
	if d.ProgSubnetMask != "255.255.255.0" {
		t.Errorf("ProgSubnetMask = %q, want 255.255.255.0", d.ProgSubnetMask)
	}
	wantRaw := byte(artnet.IpProgEnable | artnet.IpProgProgramIP | artnet.IpProgProgramSubnetMask)
	if d.IpProgCommandRaw != wantRaw {
		t.Errorf("IpProgCommandRaw = 0x%02X, want 0x%02X", d.IpProgCommandRaw, wantRaw)
	}
}

// TestDecodeEntry_IpProgReplyDetail covers ArtIpProgReply's decode.
func TestDecodeEntry_IpProgReplyDetail(t *testing.T) {
	p := artnet.IpProgReply{
		CurrentIP: [4]byte{10, 10, 10, 5}, CurrentSubnet: [4]byte{255, 255, 255, 0},
		CurrentPort: 6454, Status: artnet.IpProgReplyDHCPEnabled,
	}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindIpProgReply, IpProgReply: p})
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.Kind != "ArtIpProgReply" {
		t.Fatalf("Kind = %q, want ArtIpProgReply", e.Kind)
	}
	d := e.NodeConfig
	if d == nil {
		t.Fatal("NodeConfig detail is nil")
	}
	if !d.DHCPEnabled {
		t.Error("DHCPEnabled = false, want true")
	}
	if d.CurrentIP != "10.10.10.5" || d.CurrentSubnet != "255.255.255.0" {
		t.Errorf("CurrentIP/CurrentSubnet = %q/%q, want 10.10.10.5/255.255.255.0", d.CurrentIP, d.CurrentSubnet)
	}
}

// TestDecodeEntry_PollDetail covers ArtPoll's minimal decode, including the
// Flags-bit-1 "send ArtPollReply on state change" interpretation.
func TestDecodeEntry_PollDetail(t *testing.T) {
	p := artnet.Poll{Flags: 0x02, DiagPriority: 0x04}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindPoll, Poll: p})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.Kind != "ArtPoll" {
		t.Fatalf("Kind = %q, want ArtPoll", e.Kind)
	}
	if e.Poll == nil {
		t.Fatal("Poll detail is nil")
	}
	if !e.Poll.SendPollReplyOnChange {
		t.Error("SendPollReplyOnChange = false, want true for Flags bit 1 set")
	}
	if e.Poll.DiagPriority != 0x04 {
		t.Errorf("DiagPriority = %d, want 4", e.Poll.DiagPriority)
	}
}

// TestPeriodicNodeThrottle_CapsAndSuppressesPerPeerKind mirrors
// TestUnknownOpcodeThrottle_CapsAndSuppressesOncePerPair's shape for the
// (peer, kind) keying PeriodicNodeThrottle uses instead of (peer, opcode).
func TestPeriodicNodeThrottle_CapsAndSuppressesPerPeerKind(t *testing.T) {
	const cap = 3
	th := NewPeriodicNodeThrottle(cap)
	mk := func() Entry { return Entry{Dir: DirIn, Peer: testPeer, Kind: "ArtPollReply"} }

	var suppressions, logged int
	for i := 0; i < cap+4; i++ {
		e, ok := th.Consider(mk())
		switch {
		case i < cap:
			if !ok || e.Kind != "ArtPollReply" {
				t.Fatalf("sighting %d: got (%v,%v), want the real entry logged", i+1, e, ok)
			}
			logged++
		case i == cap:
			if !ok || e.Kind == "ArtPollReply" {
				t.Fatalf("sighting %d: want exactly one suppression entry, got (%v,%v)", i+1, e, ok)
			}
			text := FormatEntryText(e)
			for _, want := range []string{testPeer.String(), "ArtPollReply", "suppress"} {
				if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
					t.Errorf("suppression text missing %q:\n%s", want, text)
				}
			}
			suppressions++
			logged++
		default:
			if ok {
				t.Fatalf("sighting %d: want suppressed (ok=false), got ok=true", i+1)
			}
		}
	}
	if suppressions != 1 {
		t.Fatalf("suppressions = %d, want 1", suppressions)
	}
	if logged != cap+1 {
		t.Fatalf("logged = %d, want %d", logged, cap+1)
	}
	if got := th.Count(testPeer, "ArtPollReply"); got != cap+4 {
		t.Errorf("Count = %d, want %d", got, cap+4)
	}
}

// TestPeriodicNodeThrottle_IgnoresNonPeriodicKinds confirms Consider only
// acts on ArtPoll/ArtPollReply — node-config and RDM-family kinds must pass
// through unrecognized by this throttle (they're routed unconditionally by
// IsNodeConfigKind/IsRDMLoggable instead).
func TestPeriodicNodeThrottle_IgnoresNonPeriodicKinds(t *testing.T) {
	th := NewPeriodicNodeThrottle(2)
	for _, k := range []string{"ArtAddress", "ArtInput", "ArtIpProg", "ArtIpProgReply", "ArtRdm", "ArtDmx", "unknown"} {
		if _, ok := th.Consider(Entry{Dir: DirIn, Peer: testPeer, Kind: k}); ok {
			t.Errorf("Consider(Kind=%q) = true, want false", k)
		}
	}
}

// TestPeriodicNodeThrottle_KeyedByPeerAndKindIndependently confirms a
// different peer, or a different kind from the same peer, gets its own
// fresh budget — an ArtPoll broadcast (one peer: 255.255.255.255) and an
// ArtPollReply from a specific node must not share one counter, and two
// different nodes' ArtPollReply must not share one either.
func TestPeriodicNodeThrottle_KeyedByPeerAndKindIndependently(t *testing.T) {
	const cap = 1
	th := NewPeriodicNodeThrottle(cap)
	peerA := testPeer
	peerB := netip.MustParseAddrPort("10.0.0.9:6454")

	if _, ok := th.Consider(Entry{Peer: peerA, Kind: "ArtPollReply"}); !ok {
		t.Fatal("first peerA/ArtPollReply sighting should be logged")
	}
	if _, ok := th.Consider(Entry{Peer: peerA, Kind: "ArtPoll"}); !ok {
		t.Error("a different kind from the same peer should have its own budget")
	}
	if _, ok := th.Consider(Entry{Peer: peerB, Kind: "ArtPollReply"}); !ok {
		t.Error("the same kind from a different peer should have its own budget")
	}
}

// TestNodeConfigRouting_EndToEnd mirrors cmd/benny512's --lognodes tap
// wiring: ArtAddress/ArtInput/ArtIpProg/ArtIpProgReply always reach the RDM
// ring; ArtPoll/ArtPollReply are bounded by PeriodicNodeThrottle; a plain
// ArtDmx reaches neither.
func TestNodeConfigRouting_EndToEnd(t *testing.T) {
	general := New(1000)
	rdmRing := New(1000)
	periodicThrottle := NewPeriodicNodeThrottle(2)

	route := func(e Entry) {
		e = general.Add(e)
		switch {
		case IsRDMLoggable(e):
			rdmRing.Add(e)
		case IsNodeConfigKind(e.Kind):
			rdmRing.Add(e)
		case IsPeriodicNodeKind(e.Kind):
			if logEntry, ok := periodicThrottle.Consider(e); ok {
				rdmRing.Add(logEntry)
			}
		}
	}

	addrRaw := artnet.Encode(artnet.Packet{Kind: artnet.KindAddress, Address: artnet.Address{}})
	route(DecodeEntry(DirOut, testPeer, addrRaw))
	route(DecodeEntry(DirOut, testPeer, addrRaw))
	route(DecodeEntry(DirOut, testPeer, addrRaw))

	pollReplyRaw := encodePollReplyPacket(t, artnet.PollReply{NumPorts: 1})
	for i := 0; i < 5; i++ {
		route(DecodeEntry(DirIn, testPeer, pollReplyRaw))
	}

	route(Entry{Kind: "ArtDmx", Size: 20})

	got := rdmRing.Snapshot(Filter{}, 0)
	var addressCount, pollReplyCount, suppressed, dmxCount int
	for _, e := range got {
		switch e.Kind {
		case "ArtAddress":
			addressCount++
		case "ArtPollReply":
			pollReplyCount++
		case "CaptureSuppressed":
			suppressed++
		case "ArtDmx":
			dmxCount++
		}
	}
	if addressCount != 3 {
		t.Errorf("ArtAddress entries routed = %d, want 3 (every one, unconditionally)", addressCount)
	}
	if pollReplyCount != 2 {
		t.Errorf("ArtPollReply entries routed = %d, want 2 (bounded by cap)", pollReplyCount)
	}
	if suppressed != 1 {
		t.Errorf("suppression entries = %d, want 1", suppressed)
	}
	if dmxCount != 0 {
		t.Errorf("ArtDmx entries routed = %d, want 0 (not part of --lognodes' families)", dmxCount)
	}
}
