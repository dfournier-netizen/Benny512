package registry

import (
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

func TestClassifyDeviceFixture(t *testing.T) {
	if got := ClassifyDevice(rdm.CategoryFixtureMovingYoke, nil, 20, false); got != ClassFixture {
		t.Fatalf("got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryProjectorFixed, nil, 0, false); got != ClassFixture {
		t.Fatalf("got %v", got)
	}
}

func TestClassifyDeviceGatewayAndSplitter(t *testing.T) {
	if got := ClassifyDevice(rdm.CategoryDataDistribution, nil, 0, false); got != ClassGatewayNode {
		t.Fatalf("plain data-distribution should default to gateway/node, got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryDataDistribution, []rdm.ProductDetail{rdm.DetailSplitter}, 0, false); got != ClassSplitter {
		t.Fatalf("splitter detail should win, got %v", got)
	}
}

func TestClassifyDeviceWirelessProxyOverride(t *testing.T) {
	// Even a device whose category looks like a plain controller should be
	// classed Wireless once it's confirmed to be proxying (report §1.3's
	// PROXIED_DEVICES signal is the strongest one).
	if got := ClassifyDevice(rdm.CategoryControlController, nil, 0, true); got != ClassWireless {
		t.Fatalf("got %v", got)
	}
}

func TestClassifyDeviceNotDeclaredFallback(t *testing.T) {
	if got := ClassifyDevice(rdm.CategoryNotDeclared, []rdm.ProductDetail{rdm.DetailEthernetNode}, 0, false); got != ClassGatewayNode {
		t.Fatalf("got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryNotDeclared, []rdm.ProductDetail{rdm.DetailWirelessLink}, 0, false); got != ClassWireless {
		t.Fatalf("got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryNotDeclared, nil, 0, false); got != ClassUnknown {
		t.Fatalf("got %v", got)
	}
}

func TestClassifyDeviceDimmerPowerAndController(t *testing.T) {
	if got := ClassifyDevice(rdm.CategoryDimmerCSLED, nil, 20, false); got != ClassDimmerPower {
		t.Fatalf("got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryPowerControl, nil, 0, false); got != ClassDimmerPower {
		t.Fatalf("got %v", got)
	}
	if got := ClassifyDevice(rdm.CategoryTestEquipment, nil, 0, false); got != ClassController {
		t.Fatalf("got %v", got)
	}
}

// TestRegistryReclassifiesOnDeviceInfo drives the registry end-to-end
// (ToD update, then a cached DEVICE_INFO GET result) and checks the
// Fixture's Class flips from Unknown to GatewayNode — the path a real
// EN4-style discovery would take.
func TestRegistryReclassifiesOnDeviceInfo(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()
	a := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	r := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	reg := New(a, r)
	go reg.Run()

	port, _ := artnet.NewPortAddress(0, 0, 0)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("2.11.90.2"), BindIndex: 1}
	nodeRef := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("2.11.90.2:6454"), Port: port}
	uid := rdm.UID{ManufacturerID: 0x1900, DeviceID: 1}

	reg.NoteFixture(nodeRef, uid)
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := reg.Fixture(uid); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture never appeared")
		}
		time.Sleep(time.Millisecond)
	}
	f, _ := reg.Fixture(uid)
	if f.Class != ClassUnknown {
		t.Fatalf("expected ClassUnknown before DEVICE_INFO, got %v", f.Class)
	}

	deviceInfo := params.EncodeDeviceInfo(params.DeviceInfo{
		ProtocolVersionMajor: 1, ProductCategory: uint16(rdm.CategoryDataDistribution), DMXFootprint: 0, SubDeviceCount: 16,
	})
	r.Get(nodeRef, uid, rdm.PIDDeviceInfo, nil) // enqueue, but we drive the result via HandleRDMResponse below directly
	resp := rdm.Message{
		DestinationUID: rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1}, SourceUID: uid,
		TransactionNumber: 0, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDDeviceInfo, ParameterData: deviceInfo,
	}
	r.HandleRDMResponse(resp)

	deadline = time.Now().Add(time.Second)
	for {
		f, _ = reg.Fixture(uid)
		if f.Class == ClassGatewayNode {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture never reclassified as gateway/node, last Class=%v", f.Class)
		}
		time.Sleep(time.Millisecond)
	}
	if !f.HasDeviceInfo || f.SubDeviceCount != 16 {
		t.Fatalf("DEVICE_INFO sub-device count = known=%v count=%d, want true/16", f.HasDeviceInfo, f.SubDeviceCount)
	}
}
