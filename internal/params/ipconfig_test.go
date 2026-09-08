package params

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"

	"benny512/internal/rdm"
)

func TestIPv4ConfigRoundTrip(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 1} // ADJ/Obsidian-candidate, per report §7.3
	staticIP := [4]byte{2, 11, 90, 2}
	staticPrefix := byte(16) // 255.255.0.0, as E1.37-2 carries it: a prefix length
	dhcpMode := byte(rdm.DHCPStatusInactive)
	applied := false

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDListInterfaces:
			// Packed 6-byte descriptors: 32-bit ID + 16-bit hardware type
			// (E1.37-2 §4.1). This responder previously emitted bare 4-byte
			// IDs, i.e. it simulated a NON-CONFORMING device, which is how the
			// 4-byte decoder passed its own test.
			b := make([]byte, 12)
			binary.BigEndian.PutUint32(b[0:4], 1)
			binary.BigEndian.PutUint16(b[4:6], 0x0001) // Ethernet
			binary.BigEndian.PutUint32(b[6:10], 2)
			binary.BigEndian.PutUint16(b[10:12], 0x0001)
			return b, false, 0
		case rdm.PIDInterfaceLabel:
			b := append([]byte{}, msg.ParameterData[:4]...)
			return append(b, []byte("eth0")...), false, 0
		case rdm.PIDIPv4CurrentAddress, rdm.PIDIPv4StaticAddress:
			if msg.CommandClass == rdm.SetCommand {
				// SET IPV4_STATIC_ADDRESS is PDL 0x09 (§4.7). A conforming
				// responder is entitled to reject anything else, so assert the
				// length here rather than tolerating the old 12-byte form.
				if len(msg.ParameterData) != 9 {
					t.Errorf("SET IPV4_STATIC_ADDRESS PDL = %d, want 9 (interface ID + address + 1-byte prefix)",
						len(msg.ParameterData))
					return nil, true, rdm.NackFormatError
				}
				copy(staticIP[:], msg.ParameterData[4:8])
				staticPrefix = msg.ParameterData[8]
				return nil, false, 0
			}
			// GET IPV4_STATIC_ADDRESS response is PDL 0x09;
			// IPV4_CURRENT_ADDRESS appends a DHCP Status byte, PDL 0x0a (§4.6).
			b := make([]byte, 9, 10)
			copy(b[0:4], msg.ParameterData[:4])
			copy(b[4:8], staticIP[:])
			b[8] = staticPrefix
			if msg.ParameterID == rdm.PIDIPv4CurrentAddress {
				b = append(b, dhcpMode)
			}
			return b, false, 0
		case rdm.PIDIPv4DHCPMode:
			if msg.CommandClass == rdm.SetCommand {
				dhcpMode = msg.ParameterData[4]
				return nil, false, 0
			}
			b := append([]byte{}, msg.ParameterData[:4]...)
			return append(b, dhcpMode), false, 0
		case rdm.PIDInterfaceApplyConfiguration:
			applied = true
			return nil, false, 0
		case rdm.PIDDNSHostname:
			if msg.CommandClass == rdm.SetCommand {
				return nil, false, 0
			}
			return []byte("netron-en4"), false, 0
		case rdm.PIDDNSNameServer:
			b := []byte{msg.ParameterData[0], 8, 8, 8, 8}
			return b, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var ifaces []Interface
	var err error
	runAsync(t, clock, func() { ifaces, err = client.ListInterfaces(context.Background()) })
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(ifaces) != 2 || ifaces[0].ID != 1 || ifaces[1].ID != 2 {
		t.Fatalf("ifaces=%+v, want two descriptors with IDs 1 and 2", ifaces)
	}
	if ifaces[0].HardwareType != 0x0001 || ifaces[0].HardwareTypeLabel() != "Ethernet" {
		t.Fatalf("ifaces[0]=%+v, want hardware type 0x0001 Ethernet", ifaces[0])
	}

	var label string
	runAsync(t, clock, func() { label, err = client.InterfaceLabel(context.Background(), 1) })
	if err != nil || label != "eth0" {
		t.Fatalf("label=%q err=%v", label, err)
	}

	var cfg IPv4Config
	runAsync(t, clock, func() { cfg, err = client.IPv4StaticAddress(context.Background(), 1) })
	if err != nil {
		t.Fatalf("IPv4StaticAddress: %v", err)
	}
	if cfg.IP != netip.AddrFrom4(staticIP) {
		t.Fatalf("cfg=%+v", cfg)
	}

	newIP := netip.MustParseAddr("2.11.90.50")
	runAsync(t, clock, func() { err = client.SetIPv4StaticAddress(context.Background(), 1, newIP, 24) })
	if err != nil {
		t.Fatalf("SetIPv4StaticAddress: %v", err)
	}
	if staticIP != newIP.As4() {
		t.Fatalf("device-side IP = %v, want %v", staticIP, newIP.As4())
	}
	if staticPrefix != 24 {
		t.Fatalf("device-side prefix = /%d, want /24", staticPrefix)
	}

	runAsync(t, clock, func() { err = client.SetIPv4DHCPMode(context.Background(), 1, true) })
	if err != nil {
		t.Fatalf("SetIPv4DHCPMode: %v", err)
	}
	if dhcpMode != byte(rdm.DHCPStatusActive) {
		t.Fatalf("dhcpMode=%d", dhcpMode)
	}

	runAsync(t, clock, func() { err = client.ApplyInterfaceConfiguration(context.Background(), 1) })
	if err != nil || !applied {
		t.Fatalf("ApplyInterfaceConfiguration: err=%v applied=%v", err, applied)
	}

	var hostname string
	runAsync(t, clock, func() { hostname, err = client.DNSHostname(context.Background()) })
	if err != nil || hostname != "netron-en4" {
		t.Fatalf("hostname=%q err=%v", hostname, err)
	}

	var ns netip.Addr
	runAsync(t, clock, func() { ns, err = client.DNSNameServer(context.Background(), 0) })
	if err != nil || ns != netip.MustParseAddr("8.8.8.8") {
		t.Fatalf("ns=%v err=%v", ns, err)
	}
}

func TestDNSNameServerIndexBound(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x1901, DeviceID: 1}
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		return nil, true, rdm.NackDataOutOfRange
	})
	var err error
	runAsync(t, clock, func() {
		_, err = client.DNSNameServer(context.Background(), 5)
	})
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}
