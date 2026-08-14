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
	staticMask := [4]byte{255, 255, 0, 0}
	dhcpMode := byte(rdm.DHCPStatusInactive)
	applied := false

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDListInterfaces:
			b := make([]byte, 8)
			binary.BigEndian.PutUint32(b[0:4], 1)
			binary.BigEndian.PutUint32(b[4:8], 2)
			return b, false, 0
		case rdm.PIDInterfaceLabel:
			b := append([]byte{}, msg.ParameterData[:4]...)
			return append(b, []byte("eth0")...), false, 0
		case rdm.PIDIPv4CurrentAddress, rdm.PIDIPv4StaticAddress:
			if msg.CommandClass == rdm.SetCommand {
				copy(staticIP[:], msg.ParameterData[4:8])
				copy(staticMask[:], msg.ParameterData[8:12])
				return nil, false, 0
			}
			b := make([]byte, 12)
			copy(b[0:4], msg.ParameterData[:4])
			copy(b[4:8], staticIP[:])
			copy(b[8:12], staticMask[:])
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

	var ifaces []uint32
	var err error
	runAsync(t, clock, func() { ifaces, err = client.ListInterfaces(context.Background()) })
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(ifaces) != 2 || ifaces[0] != 1 || ifaces[1] != 2 {
		t.Fatalf("ifaces=%v", ifaces)
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
	newMask := netip.MustParseAddr("255.255.0.0")
	runAsync(t, clock, func() { err = client.SetIPv4StaticAddress(context.Background(), 1, newIP, newMask) })
	if err != nil {
		t.Fatalf("SetIPv4StaticAddress: %v", err)
	}
	if staticIP != newIP.As4() {
		t.Fatalf("device-side IP = %v, want %v", staticIP, newIP.As4())
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
