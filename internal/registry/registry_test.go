package registry

import (
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

func TestManufacturerName(t *testing.T) {
	if got := ManufacturerName(0x6C74); got != "LumenRadio" {
		t.Errorf("LumenRadio: got %q", got)
	}
	if got := ManufacturerName(0x6574); got != "ETC (Electronic Theatre Controls)" {
		t.Errorf("ETC: got %q", got)
	}
	if got := ManufacturerName(0x0508); got != "Chauvet & Chauvet Professional" {
		t.Errorf("Chauvet: got %q", got)
	}
	if got := ManufacturerName(0x2222); got != "ROBE Lighting" {
		t.Errorf("Robe: got %q", got)
	}
	if got := ManufacturerName(0x4D50); got != "Martin Professional" {
		t.Errorf("Martin: got %q", got)
	}
	if got := ManufacturerName(0xAAAA); got != "Ayrton" {
		t.Errorf("Ayrton: got %q", got)
	}
	if got := ManufacturerName(0x4741); got != "MA Lighting" {
		t.Errorf("MA Lighting: got %q", got)
	}
	if got := ManufacturerName(0xBEEF); got != "Unknown (0xBEEF)" {
		t.Errorf("unknown: got %q", got)
	}
}

func TestRegistryMergeToDAndParams(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()

	a := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	r := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	reg := New(a, r)

	go reg.Run()

	port, _ := artnet.NewPortAddress(0, 0, 1)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1}
	nodeRef := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.5:6454"), Port: port}

	uid1 := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	uid2 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}

	reg.NoteFixture(nodeRef, uid1)
	reg.NoteFixture(nodeRef, uid2)

	deadline := time.Now().Add(2 * time.Second)
	for {
		fx := reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)
		if len(fx) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 fixtures, got %d", len(fx))
		}
		time.Sleep(time.Millisecond)
	}

	f, ok := reg.Fixture(uid1)
	if !ok {
		t.Fatal("expected uid1 fixture")
	}
	if f.ManufacturerName != "LumenRadio" {
		t.Errorf("got manufacturer %q", f.ManufacturerName)
	}

	ref, ok := reg.FixtureNode(uid1)
	if !ok || ref.Port.RawValue() != port.RawValue() {
		t.Fatalf("FixtureNode: %+v ok=%v", ref, ok)
	}
}
