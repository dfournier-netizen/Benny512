package registry

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
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

// --- ClearDevices / ClearDevicesOnPort (discovered-RDM-device cache clear) --

func newTestRegistry() *Registry {
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()
	a := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	r := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	return New(a, r)
}

func TestClearDevicesWipesEveryPortAndReturnsCount(t *testing.T) {
	reg := newTestRegistry()
	go reg.Run()

	portA, _ := artnet.NewPortAddress(0, 0, 0)
	portB, _ := artnet.NewPortAddress(0, 0, 1)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1}
	refA := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.5:6454"), Port: portA}
	refB := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.5:6454"), Port: portB}
	uid1 := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	uid2 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}

	reg.NoteFixture(refA, uid1)
	reg.NoteFixture(refB, uid2)
	waitForFixtureCount(t, reg, 2)

	n := reg.ClearDevices()
	if n != 2 {
		t.Fatalf("ClearDevices returned %d, want 2", n)
	}
	if fx := reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false); len(fx) != 0 {
		t.Fatalf("fixtures after ClearDevices = %d, want 0: %+v", len(fx), fx)
	}
}

func TestClearDevicesOnEmptyRegistryReturnsZero(t *testing.T) {
	reg := newTestRegistry()
	if n := reg.ClearDevices(); n != 0 {
		t.Fatalf("ClearDevices on an empty registry = %d, want 0", n)
	}
}

func TestClearDevicesOnPortLeavesOtherPortsIntact(t *testing.T) {
	reg := newTestRegistry()
	go reg.Run()

	portA, _ := artnet.NewPortAddress(0, 0, 0)
	portB, _ := artnet.NewPortAddress(0, 0, 1)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1}
	addr := netip.MustParseAddrPort("10.0.0.5:6454")
	refA := session.NodeRef{Key: nodeKey, Addr: addr, Port: portA}
	refB := session.NodeRef{Key: nodeKey, Addr: addr, Port: portB}
	uid1 := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	uid2 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}
	uid3 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 3}

	reg.NoteFixture(refA, uid1)
	reg.NoteFixture(refB, uid2)
	reg.NoteFixture(refB, uid3)
	waitForFixtureCount(t, reg, 3)

	n := reg.ClearDevicesOnPort(nodeKey.IP, portB)
	if n != 2 {
		t.Fatalf("ClearDevicesOnPort returned %d, want 2", n)
	}
	fx := reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)
	if len(fx) != 1 || fx[0].UID != uid1 {
		t.Fatalf("fixtures after ClearDevicesOnPort = %+v, want just uid1 on portA", fx)
	}
}

func TestClearDevicesOnPortMissReturnsZero(t *testing.T) {
	reg := newTestRegistry()
	go reg.Run()

	port, _ := artnet.NewPortAddress(0, 0, 0)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1}
	ref := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.5:6454"), Port: port}
	reg.NoteFixture(ref, rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1})
	waitForFixtureCount(t, reg, 1)

	otherPort, _ := artnet.NewPortAddress(0, 0, 5)
	if n := reg.ClearDevicesOnPort(nodeKey.IP, otherPort); n != 0 {
		t.Fatalf("ClearDevicesOnPort for a port with nothing on it = %d, want 0", n)
	}
	if fx := reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false); len(fx) != 1 {
		t.Fatalf("the untouched port's fixture should survive, got %+v", fx)
	}
}

func waitForFixtureCount(t *testing.T, reg *Registry, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := len(reg.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d fixtures", want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestManufacturerLabelAndModelDescriptionCaching drives real GET round-trips
// for MANUFACTURER_LABEL/DEVICE_MODEL_DESCRIPTION/DEVICE_INFO through
// RDMController exactly like the Devices screen's background backfill does,
// and checks Registry caches the task's priority-chain inputs correctly:
// an ACK (including a legitimate empty-string ACK) populates the raw field
// and flips its Known flag; a NACK leaves the raw field empty but still
// flips Known, so callers can tell "not yet attempted" apart from "device
// doesn't report this" without polling forever.
func TestManufacturerLabelAndModelDescriptionCaching(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()

	a := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	r := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	reg := New(a, r)
	go reg.Run()

	port, _ := artnet.NewPortAddress(0, 0, 1)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.6"), BindIndex: 1}
	nodeRef := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.6:6454"), Port: port}

	reportingUID := rdm.UID{ManufacturerID: 0x1900, DeviceID: 1}
	nackingUID := rdm.UID{ManufacturerID: 0xAAAA, DeviceID: 2}
	deviceInfoBytes := params.EncodeDeviceInfo(params.DeviceInfo{ProtocolVersionMajor: 1, DeviceModelID: 0x0042})

	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		var uid rdm.UID
		var data []byte
		nack := false
		switch msg.DestinationUID {
		case reportingUID:
			uid = reportingUID
			switch msg.ParameterID {
			case rdm.PIDManufacturerLabel:
				data = []byte("Obsidian Control Systems")
			case rdm.PIDDeviceModelDescription:
				data = []byte("") // legitimate empty-string ACK
			case rdm.PIDDeviceInfo:
				data = deviceInfoBytes
			default:
				nack = true
			}
		case nackingUID:
			uid = nackingUID
			nack = true
		default:
			return
		}
		respClass := rdm.GetCommandResponse
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseNackReason), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: []byte{byte(rdm.NackUnknownPID >> 8), byte(rdm.NackUnknownPID)},
			}
		} else {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
			}
		}
		clock.AfterFunc(time.Millisecond, func() { r.HandleRDMResponse(resp) })
	}

	// awaitGet issues one GET and drives it to completion: the response is
	// scheduled via clock.AfterFunc above, but FakeClock never advances on
	// its own, so this pumps Advance() from the test goroutine while
	// Command.Await runs on another one (mirrors internal/web's
	// runHTTPAsync helper for the same reason).
	awaitGet := func(uid rdm.UID, pid rdm.ParameterID) {
		t.Helper()
		cmd := r.Get(nodeRef, uid, pid, nil)
		done := make(chan session.Result, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, err := cmd.Await(ctx)
			if err != nil {
				t.Errorf("GET 0x%04X for %s: %v", uint16(pid), uid, err)
				close(done)
				return
			}
			done <- res
		}()
		deadline := time.Now().Add(2 * time.Second)
		for {
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
			clock.Advance(2 * time.Millisecond)
			if time.Now().After(deadline) {
				t.Fatalf("GET 0x%04X for %s: timed out", uint16(pid), uid)
			}
		}
	}

	awaitGet(reportingUID, rdm.PIDManufacturerLabel)
	awaitGet(reportingUID, rdm.PIDDeviceModelDescription)
	awaitGet(reportingUID, rdm.PIDDeviceInfo)
	awaitGet(nackingUID, rdm.PIDManufacturerLabel)
	awaitGet(nackingUID, rdm.PIDDeviceModelDescription)

	// reg.Run() consumes RDMController events on its own goroutine; give it
	// a moment to process the completions above before reading state back.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rep, ok1 := reg.Fixture(reportingUID)
		nk, ok2 := reg.Fixture(nackingUID)
		if ok1 && ok2 && rep.ManufacturerLabelKnown && rep.ModelDescriptionKnown && nk.ManufacturerLabelKnown && nk.ModelDescriptionKnown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for cache: reporting ok=%v %+v, nacking ok=%v %+v", ok1, rep, ok2, nk)
		}
		time.Sleep(time.Millisecond)
	}

	rep, _ := reg.Fixture(reportingUID)
	if rep.ManufacturerLabel != "Obsidian Control Systems" {
		t.Errorf("reporting ManufacturerLabel = %q", rep.ManufacturerLabel)
	}
	if !rep.ManufacturerLabelKnown {
		t.Error("reporting ManufacturerLabelKnown = false, want true")
	}
	// An empty-string ACK must still cache (as "") and mark Known — the
	// fix for the old len(data)>0 gate that would have left this stuck.
	if rep.ModelDescription != "" || !rep.ModelDescriptionKnown {
		t.Errorf("reporting empty-ACK ModelDescription = %q Known=%v, want \"\"/true", rep.ModelDescription, rep.ModelDescriptionKnown)
	}
	if !rep.HasDeviceInfo || rep.DeviceModelID != 0x0042 {
		t.Errorf("reporting DeviceInfo cache = HasDeviceInfo=%v DeviceModelID=0x%04X, want true/0x0042", rep.HasDeviceInfo, rep.DeviceModelID)
	}

	nk, _ := reg.Fixture(nackingUID)
	if nk.ManufacturerLabel != "" || !nk.ManufacturerLabelKnown {
		t.Errorf("nacking ManufacturerLabel = %q Known=%v, want \"\"/true", nk.ManufacturerLabel, nk.ManufacturerLabelKnown)
	}
	if nk.ModelDescription != "" || !nk.ModelDescriptionKnown {
		t.Errorf("nacking ModelDescription = %q Known=%v, want \"\"/true", nk.ModelDescription, nk.ModelDescriptionKnown)
	}
	if nk.HasDeviceInfo {
		t.Errorf("nacking device never got a DEVICE_INFO ACK, HasDeviceInfo should stay false")
	}
}
