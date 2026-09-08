package web

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// wireNetworkDevice scripts a fake responder supporting a one-interface
// E1.37-2 IPv4/DNS profile matching cmd/benny512/demo.go's en4Root
// (interface 1, DHCP inactive, one DNS name server at index 0).
func (h *testHarness) wireNetworkDevice(uid rdm.UID, startIP [4]byte, startMask [4]byte) {
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{
		rdm.PIDListInterfaces, rdm.PIDInterfaceLabel, rdm.PIDIPv4CurrentAddress,
		rdm.PIDIPv4StaticAddress, rdm.PIDIPv4DHCPMode, rdm.PIDInterfaceApplyConfiguration,
		rdm.PIDDNSHostname, rdm.PIDDNSDomainName, rdm.PIDDNSNameServer,
	})
	staticIP := startIP
	// E1.37-2 carries the netmask as a prefix length, not a dotted address
	// (§4.6, §4.7). This responder previously spoke a 12-byte dialect with a
	// 4-byte mask AND rejected anything else as a format error, so it not only
	// tolerated the defect but enforced it: a conforming 9-byte SET would have
	// been NACKed by this fake device.
	staticPrefix := byte(16)
	dhcp := byte(rdm.DHCPStatusInactive)
	hostname := []byte("netron-en4")
	domain := []byte("local")
	applied := 0

	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0, 0
		case rdm.PIDListInterfaces:
			// One 6-byte descriptor: 32-bit ID + 16-bit hardware type (§4.1).
			b := make([]byte, 6)
			binary.BigEndian.PutUint32(b[0:4], 1)
			binary.BigEndian.PutUint16(b[4:6], 0x0001) // Ethernet
			return b, false, 0, 0
		case rdm.PIDInterfaceLabel:
			return append([]byte{0, 0, 0, 1}, []byte("eth0")...), false, 0, 0
		case rdm.PIDIPv4CurrentAddress:
			// PDL 0x0a: interface ID, address, 1-byte prefix, DHCP status.
			b := make([]byte, 10)
			binary.BigEndian.PutUint32(b[0:4], 1)
			copy(b[4:8], staticIP[:])
			b[8] = staticPrefix
			b[9] = dhcp
			return b, false, 0, 0
		case rdm.PIDIPv4StaticAddress:
			if msg.CommandClass == rdm.SetCommand {
				// PDL 0x09 (§4.7). Enforced, so a regression back to the
				// 12-byte form fails here rather than silently passing.
				if len(msg.ParameterData) != 9 {
					return nil, true, rdm.NackFormatError, 0
				}
				copy(staticIP[:], msg.ParameterData[4:8])
				staticPrefix = msg.ParameterData[8]
				return nil, false, 0, 0
			}
			b := make([]byte, 9)
			binary.BigEndian.PutUint32(b[0:4], 1)
			copy(b[4:8], staticIP[:])
			b[8] = staticPrefix
			return b, false, 0, 0
		case rdm.PIDIPv4DHCPMode:
			if msg.CommandClass == rdm.SetCommand {
				if len(msg.ParameterData) != 5 {
					return nil, true, rdm.NackFormatError, 0
				}
				dhcp = msg.ParameterData[4]
				return nil, false, 0, 0
			}
			return append([]byte{0, 0, 0, 1}, dhcp), false, 0, 0
		case rdm.PIDInterfaceApplyConfiguration:
			applied++
			return nil, false, 0, 0
		case rdm.PIDDNSHostname:
			if msg.CommandClass == rdm.SetCommand {
				hostname = append([]byte(nil), msg.ParameterData...)
				return nil, false, 0, 0
			}
			return hostname, false, 0, 0
		case rdm.PIDDNSDomainName:
			if msg.CommandClass == rdm.SetCommand {
				domain = append([]byte(nil), msg.ParameterData...)
				return nil, false, 0, 0
			}
			return domain, false, 0, 0
		case rdm.PIDDNSNameServer:
			if len(msg.ParameterData) < 1 || msg.ParameterData[0] != 0 {
				return nil, true, rdm.NackDataOutOfRange, 0
			}
			return []byte{0, 8, 8, 8, 8}, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})
}

func TestGetDeviceNetwork(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x30}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/network", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got networkJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Known || !got.Supported {
		t.Fatalf("Known/Supported = %v/%v, want true/true", got.Known, got.Supported)
	}
	if len(got.Interfaces) != 1 {
		t.Fatalf("len(Interfaces) = %d, want 1: %+v", len(got.Interfaces), got.Interfaces)
	}
	ifc := got.Interfaces[0]
	if ifc.ID != 1 || !ifc.LabelKnown || ifc.Label != "eth0" {
		t.Errorf("interface = %+v, want id=1 label=eth0", ifc)
	}
	if !ifc.CurrentKnown || ifc.CurrentIP != "2.11.90.2" || ifc.CurrentMask != "255.255.0.0" {
		t.Errorf("current = %+v, want 2.11.90.2/255.255.0.0", ifc)
	}
	if !ifc.StaticKnown || ifc.StaticIP != "2.11.90.2" {
		t.Errorf("static = %+v", ifc)
	}
	if !ifc.DHCPKnown || ifc.DHCPStatus != "inactive" {
		t.Errorf("dhcp = %+v, want known/inactive", ifc)
	}
	if !ifc.ApplySupported {
		t.Errorf("ApplySupported = false, want true")
	}
	if ifc.HardwareAddressKnown {
		t.Errorf("HardwareAddressKnown = true, want false (PID not advertised in this test's SUPPORTED_PARAMETERS)")
	}
	if !got.DNS.Supported || got.DNS.Hostname != "netron-en4" || got.DNS.Domain != "local" {
		t.Errorf("DNS = %+v", got.DNS)
	}
	if len(got.DNS.NameServers) != 1 || got.DNS.NameServers[0].IP != "8.8.8.8" {
		t.Errorf("DNS.NameServers = %+v", got.DNS.NameServers)
	}
}

// TestGetDeviceNetworkDegradesForUnsupportedDevice proves GET /network
// returns 200 with Supported:false (never an error, never a fabricated
// interface list) for a device that doesn't advertise LIST_INTERFACES at
// all — the "absent, not broken" contract the UI relies on to hide this
// section entirely for ordinary fixtures.
func TestGetDeviceNetworkDegradesForUnsupportedDevice(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x31}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel}), false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/network", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got networkJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Known {
		t.Errorf("Known = false, want true (SUPPORTED_PARAMETERS resolved)")
	}
	if got.Supported {
		t.Errorf("Supported = true, want false (LIST_INTERFACES not advertised)")
	}
	if len(got.Interfaces) != 0 {
		t.Errorf("Interfaces = %+v, want empty", got.Interfaces)
	}
}

func TestSetNetworkStaticRequiresConfirm(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x32}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/network/interface/1/static",
		map[string]string{"ip": "2.11.90.50", "mask": "255.255.0.0"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 without confirm", rr.Code)
	}
}

// TestSetNetworkStaticRoundTrip proves a confirmed static-address SET
// actually lands (GET-after-SET reflects it) and that INTERFACE_APPLY_
// CONFIGURATION is sent as a follow-up when the device advertises it — the
// exact round trip the bench-test procedure asks Dom to perform by hand.
func TestSetNetworkStaticRoundTrip(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x33}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/network/interface/1/static",
		map[string]string{"ip": "2.11.90.50", "mask": "255.255.0.0", "confirm": "CONFIRM"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var res networkActionResponseJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Note == "" {
		t.Errorf("Note is empty, want an INTERFACE_APPLY_CONFIGURATION note")
	}

	rr2 := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/network", nil)
	var got networkJSON
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0].StaticIP != "2.11.90.50" {
		t.Fatalf("after SET, static IP = %+v, want 2.11.90.50", got.Interfaces)
	}
}

func TestSetNetworkStaticRejectsBadAddress(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x34}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/network/interface/1/static",
		map[string]string{"ip": "not-an-ip", "mask": "255.255.0.0", "confirm": "CONFIRM"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for malformed IP", rr.Code)
	}
}

func TestSetNetworkDHCPRoundTrip(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x35}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/network/interface/1/dhcp",
		map[string]any{"enable": true, "confirm": "CONFIRM"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr2 := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/network", nil)
	var got networkJSON
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0].DHCPStatus != "active" {
		t.Fatalf("after SET, dhcp = %+v, want active", got.Interfaces)
	}
}

func TestSetNetworkDNS(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 0x36}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireNetworkDevice(uid, [4]byte{2, 11, 90, 2}, [4]byte{255, 255, 0, 0})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/network/dns",
		map[string]string{"hostname": "new-host", "domain": "example.com", "confirm": "CONFIRM"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr2 := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/network", nil)
	var got networkJSON
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DNS.Hostname != "new-host" || got.DNS.Domain != "example.com" {
		t.Fatalf("DNS after SET = %+v", got.DNS)
	}
}
