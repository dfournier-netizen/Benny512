package params

import (
	"context"
	"encoding/hex"
	"net/netip"
	"testing"

	"benny512/internal/rdm"
)

// The tests in this file assert against the LITERAL BYTES printed in ANSI
// E1.37-2:2015 (R2021) Appendix B, rather than against bytes this package
// also produced. That distinction is the whole point of the file.
//
// TestIPv4ConfigRoundTrip next door is a good test of the client's behaviour
// and a useless test of its wire format: its fake responder is written from
// the same reading of the standard as the code it exercises, so when that
// reading was wrong — 4-byte interface descriptors, a 4-byte dotted netmask
// — the responder was wrong in exactly the same way and the test passed
// against a build that could not talk to a conforming gateway. This is the
// eleventh instance on this project of a test proving only that one side
// agrees with itself.
//
// So: hard-coded hex from the primary text, on both directions of the wire.

// TestDecodeInterfaceList_AppendixB decodes Appendix B's own two-interface
// LIST_INTERFACES response.
//
// The bytes are 00000001 0001 00000002 0001 — twelve of them, describing
// interface 1 (Ethernet) and interface 2 (Ethernet), as 48-bit descriptors
// per §4.1.
//
// Against the previous 4-byte decoder this returns THREE interfaces —
// 0x00000001, 0x00010000, 0x00020001 — and its `len(data)%4 != 0` guard
// cannot notice, because 12 is a multiple of 4 as well as of 6. Two of those
// three IDs do not exist on the device, so every per-interface GET that
// followed carried a fabricated identifier and earned NR_DATA_OUT_OF_RANGE:
// the failure surfaced as "this gateway doesn't support E1.37-2 properly",
// which is the wrong conclusion and the reason nobody chased it.
func TestDecodeInterfaceList_AppendixB(t *testing.T) {
	data := mustHex(t, "00000001000100000002"+"0001")

	got, err := DecodeInterfaceList(data)
	if err != nil {
		t.Fatalf("DecodeInterfaceList on Appendix B's own response: %v", err)
	}
	want := []Interface{
		{ID: 1, HardwareType: 0x0001},
		{ID: 2, HardwareType: 0x0001},
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d interfaces from Appendix B's 12-byte response, want %d — %+v\n"+
			"a 4-byte reading yields three phantom IDs here and its length check waves it through",
			len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("interface[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[0].HardwareTypeLabel() != "Ethernet" {
		t.Errorf("hardware type label = %q, want Ethernet", got[0].HardwareTypeLabel())
	}

	// A descriptor list that is NOT a whole number of 6-byte entries must be
	// rejected rather than truncated, so a non-conforming responder is
	// reported instead of quietly decoding fewer interfaces than it sent.
	if _, err := DecodeInterfaceList(mustHex(t, "0000000100010000")); err == nil {
		t.Error("an 8-byte LIST_INTERFACES response decoded without error; want ErrBadInterfaceList")
	}
}

// TestDecodeIPv4Config_AppendixB decodes Appendix B's IPV4_STATIC_ADDRESS
// GET response, 00000001 0a000020 08: interface 1, 10.0.0.32, prefix /8.
//
// PDL is 9. The previous decoder required >= 12 bytes, so it REJECTED this
// exact reply outright — a conforming device's answer was unreadable, and
// the code path that would have shown the operator their gateway's static
// address returned a length error instead.
//
// The IPV4_CURRENT_ADDRESS form (§4.6) is the same nine bytes plus a DHCP
// Status byte, PDL 10, and is checked here too because that one extra byte
// is the only thing separating the two PIDs on the wire.
func TestDecodeIPv4Config_AppendixB(t *testing.T) {
	cfg, err := DecodeIPv4Config(mustHex(t, "000000010a00002008"))
	if err != nil {
		t.Fatalf("DecodeIPv4Config on Appendix B's 9-byte response: %v — "+
			"a >=12-byte floor rejects a conforming responder", err)
	}
	if cfg.InterfaceID != 1 {
		t.Errorf("interface ID = %d, want 1", cfg.InterfaceID)
	}
	if cfg.IP != netip.MustParseAddr("10.0.0.32") {
		t.Errorf("IP = %v, want 10.0.0.32", cfg.IP)
	}
	if cfg.PrefixLen != 8 {
		t.Errorf("prefix = /%d, want /8", cfg.PrefixLen)
	}
	if cfg.SubnetMask() != netip.MustParseAddr("255.0.0.0") {
		t.Errorf("SubnetMask() = %v, want 255.0.0.0", cfg.SubnetMask())
	}
	// Nine bytes carry no DHCP status. That must read as "not told", never as
	// DHCP_STATUS_UNKNOWN (0), which is a real answer meaning the device
	// cannot tell — see IPv4Config.DHCPStatusKnown.
	if cfg.DHCPStatusKnown {
		t.Errorf("DHCPStatusKnown is true on a 9-byte response that has no such field")
	}

	// IPV4_CURRENT_ADDRESS: the same nine bytes with a DHCP Status byte.
	cur, err := DecodeIPv4Config(mustHex(t, "000000010a0000200801"))
	if err != nil {
		t.Fatalf("DecodeIPv4Config on a 10-byte IPV4_CURRENT_ADDRESS response: %v", err)
	}
	if !cur.DHCPStatusKnown || cur.DHCPStatus != rdm.DHCPStatus(0x01) {
		t.Errorf("DHCP status = %v (known=%v), want 0x01 known", cur.DHCPStatus, cur.DHCPStatusKnown)
	}

	// Eight bytes is short of every conforming form and must be rejected.
	if _, err := DecodeIPv4Config(mustHex(t, "000000010a000020")); err == nil {
		t.Error("an 8-byte IPv4 config decoded without error; want a length error")
	}
}

// TestSetIPv4StaticAddress_AppendixBWireFormat is the one that matters most,
// because it is the only direction where being wrong writes to the device.
//
// Appendix B sets interface 1 to 10.0.0.32/8 with PDL 0x09:
//
//	00000001 0a000020 08
//
// This package previously built twelve bytes, spelling the netmask as a
// 4-byte dotted address — a malformed SET IPV4_STATIC_ADDRESS to a
// conforming responder. This PID rewrites a gateway's network configuration;
// a malformed write is how a node ends up unreachable in a truss, at height,
// on a show day. That is why the assertion here is on the bytes leaving the
// client rather than on whether the fake device liked them.
func TestSetIPv4StaticAddress_AppendixBWireFormat(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 1}
	var sent []byte
	var sentClass rdm.CommandClass

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDIPv4StaticAddress && msg.CommandClass == rdm.SetCommand {
			sent = append([]byte{}, msg.ParameterData...)
			sentClass = msg.CommandClass
			return nil, false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})

	var err error
	runAsync(t, clock, func() {
		err = client.SetIPv4StaticAddress(context.Background(), 1, netip.MustParseAddr("10.0.0.32"), 8)
	})
	if err != nil {
		t.Fatalf("SetIPv4StaticAddress: %v", err)
	}
	if sentClass != rdm.SetCommand {
		t.Fatalf("no SET IPV4_STATIC_ADDRESS reached the responder")
	}

	want := mustHex(t, "000000010a00002008")
	if got := hex.EncodeToString(sent); got != hex.EncodeToString(want) {
		t.Fatalf("SET IPV4_STATIC_ADDRESS payload = %s (PDL %d), want %s (PDL 9)\n"+
			"these are Appendix B's own bytes for 10.0.0.32/8; a 12-byte payload with a "+
			"dotted mask is a malformed write to a real gateway",
			got, len(sent), hex.EncodeToString(want))
	}

	// Out-of-range prefixes must be refused before anything reaches the wire.
	sent = nil
	runAsync(t, clock, func() {
		err = client.SetIPv4StaticAddress(context.Background(), 1, netip.MustParseAddr("10.0.0.32"), 33)
	})
	if err == nil {
		t.Errorf("prefix /33 was accepted; want a refusal before the SET is built")
	}
	if sent != nil {
		t.Errorf("a /33 prefix still put %s on the wire", hex.EncodeToString(sent))
	}
}

// TestPrefixLenFromMask covers the presentation boundary: the UI collects a
// dotted mask because that is what a lighting tech reads off a faceplate,
// and this is the single place it becomes E1.37-2's prefix length.
//
// A non-contiguous mask is REFUSED rather than bit-counted. Counting the
// bits of 255.0.255.0 yields /16, which is a mask the operator did not ask
// for and cannot see they are getting — and, unlike a rejection, it would be
// written to the device.
func TestPrefixLenFromMask(t *testing.T) {
	for _, c := range []struct {
		mask string
		want uint8
	}{
		{"0.0.0.0", 0},
		{"255.0.0.0", 8},
		{"255.255.0.0", 16},
		{"255.255.255.0", 24},
		{"255.255.255.252", 30},
		{"255.255.255.255", 32},
	} {
		got, err := PrefixLenFromMask(netip.MustParseAddr(c.mask))
		if err != nil {
			t.Errorf("PrefixLenFromMask(%s): %v", c.mask, err)
			continue
		}
		if got != c.want {
			t.Errorf("PrefixLenFromMask(%s) = /%d, want /%d", c.mask, got, c.want)
		}
		// And back again, so the two conversions cannot drift apart.
		if back := (IPv4Config{PrefixLen: got}).SubnetMask(); back != netip.MustParseAddr(c.mask) {
			t.Errorf("SubnetMask(/%d) = %v, want %s", got, back, c.mask)
		}
	}

	for _, bad := range []string{"255.0.255.0", "255.255.0.255", "0.0.0.1"} {
		if n, err := PrefixLenFromMask(netip.MustParseAddr(bad)); err == nil {
			t.Errorf("PrefixLenFromMask(%s) = /%d with no error; a non-contiguous mask "+
				"cannot be expressed as a prefix and must be refused, not bit-counted", bad, n)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad test hex %q: %v", s, err)
	}
	return b
}
