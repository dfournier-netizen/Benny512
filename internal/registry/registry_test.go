package registry

import (
	"context"
	"net/netip"
	"sync"
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

// TestUnreachableDeviceIsStatedNotSilentlyIncomplete is the round-4
// regression test, end to end through the real controller.
//
// Dom's bench report was "now I'm not even seeing all of the info for either
// moonlite". The mechanism was that a device behind a saturated CRMX proxy
// answered nothing, so its row simply stopped filling in — with no way for a
// lighting tech to tell "Benny512 has given up on this fixture" from
// "Benny512 is broken". Two properties are asserted here:
//
//  1. Once session's circuit breaker opens, the registry says so, with a
//     retry time, so the UI can put a sentence on screen.
//  2. It does NOT record the PIDs as answered. That is the round-2
//     cache-poisoning bug (a proxy refusal marking ManufacturerLabelKnown)
//     re-appearing in a new place, and it must not.
func TestUnreachableDeviceIsStatedNotSilentlyIncomplete(t *testing.T) {
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()

	a := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	r := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	reg := New(a, r)
	go reg.Run()

	port, _ := artnet.NewPortAddress(0, 0, 3)
	nodeKey := session.NodeKey{IP: netip.MustParseAddr("10.0.0.9"), BindIndex: 1}
	nodeRef := session.NodeRef{Key: nodeKey, Addr: netip.MustParseAddrPort("10.0.0.9:6454"), Port: port}

	proxied := rdm.UID{ManufacturerID: 0x4C55, DeviceID: 0x6DA2C93B}

	// refusing is flipped once the device "comes back", so the same harness
	// covers the recovery leg.
	var mu sync.Mutex
	refusing := true

	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != proxied {
			return
		}
		mu.Lock()
		refuse := refusing
		mu.Unlock()

		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: proxied,
			TransactionNumber: msg.TransactionNumber, SubDevice: msg.SubDevice,
			CommandClass: rdm.GetCommandResponse, ParameterID: msg.ParameterID,
		}
		if refuse {
			resp.PortIDOrResponseType = byte(rdm.ResponseNackReason)
			resp.ParameterData = []byte{
				byte(rdm.NackProxyBufferFull >> 8), byte(rdm.NackProxyBufferFull),
			}
		} else {
			resp.PortIDOrResponseType = byte(rdm.ResponseACK)
			resp.ParameterData = []byte("LumenRadio")
		}
		clock.AfterFunc(time.Millisecond, func() { r.HandleRDMResponse(resp) })
	}

	awaitGet := func(pid rdm.ParameterID) session.Result {
		t.Helper()
		cmd := r.Get(nodeRef, proxied, pid, nil)
		done := make(chan session.Result, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := cmd.Await(ctx)
			if err != nil {
				t.Errorf("GET 0x%04X: %v", uint16(pid), err)
				close(done)
				return
			}
			done <- res
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			select {
			case res := <-done:
				return res
			case <-time.After(time.Millisecond):
			}
			clock.Advance(20 * time.Millisecond)
			if time.Now().After(deadline) {
				t.Fatalf("GET 0x%04X: timed out", uint16(pid))
			}
		}
	}

	// Every command is refused for its whole budget, so after
	// ProxyBreakerTrip of them the breaker opens.
	for i := 0; i < session.ProxyBreakerTrip; i++ {
		if res := awaitGet(rdm.PIDManufacturerLabel); res.Kind != session.ResultProxyBufferFull {
			t.Fatalf("command %d kind = %v, want proxy-buffer-full", i, res.Kind)
		}
	}
	res := awaitGet(rdm.PIDManufacturerLabel)
	if res.Kind != session.ResultDeviceUnreachable {
		t.Fatalf("kind = %v (err %v), want device-unreachable once the breaker is open", res.Kind, res.Err)
	}

	f := awaitFixture(t, reg, proxied, func(f Fixture) bool { return f.ProxyUnreachable })
	if f.ProxyRetryAt.IsZero() {
		t.Error("ProxyRetryAt is zero; the UI has no retry time to show")
	}
	if f.ProxyRefusals < session.ProxyBreakerTrip {
		t.Errorf("ProxyRefusals = %d, want at least %d", f.ProxyRefusals, session.ProxyBreakerTrip)
	}
	// The heart of it: nothing has been settled about this device.
	if f.ManufacturerLabelKnown {
		t.Error("ManufacturerLabelKnown = true for a device that never answered — the round-2 cache-poisoning bug, in a new place")
	}
	if len(f.Params) != 0 {
		t.Errorf("Params = %v, want empty for a device that never answered", f.Params)
	}

	// The device comes back. Wait out the cool-down; the next command is
	// admitted as a probe, answers, and clears the flag.
	mu.Lock()
	refusing = false
	mu.Unlock()
	clock.Advance(session.ProxyBreakerCooldownInitial)

	if res := awaitGet(rdm.PIDManufacturerLabel); res.Kind != session.ResultAck {
		t.Fatalf("kind = %v (err %v), want ack once the device answers again", res.Kind, res.Err)
	}
	f = awaitFixture(t, reg, proxied, func(f Fixture) bool { return !f.ProxyUnreachable })
	if f.ManufacturerLabel != "LumenRadio" {
		t.Errorf("ManufacturerLabel = %q after recovery, want %q", f.ManufacturerLabel, "LumenRadio")
	}
	if !f.ProxyRetryAt.IsZero() {
		t.Errorf("ProxyRetryAt = %v after recovery, want zero", f.ProxyRetryAt)
	}
}

// TestReclassifyDecodesProxiedDeviceCountStructured guards Phase D task 1's
// "expose proxy status as structured data" ask: a PROXIED_DEVICE_COUNT ACK
// must populate Fixture's ProxiedDeviceCount/ProxiedDeviceCountKnown/
// ProxiedListChanged fields fully (not just the pre-existing IsWirelessProxy
// boolean), using the E1.20 §8.4.1-confirmed 3-byte wire shape
// (internal/rdm/proxy.go) rather than the 2-byte guess this case used to
// stop at.
func TestReclassifyDecodesProxiedDeviceCountStructured(t *testing.T) {
	f := &Fixture{}
	data := rdm.EncodeProxiedDeviceCount(rdm.ProxiedDeviceCount{Count: 3, ListChanged: true})
	reclassify(f, rdm.PIDProxiedDeviceCount, data)

	if !f.ProxiedDeviceCountKnown {
		t.Fatal("ProxiedDeviceCountKnown = false, want true after a PROXIED_DEVICE_COUNT ACK")
	}
	if f.ProxiedDeviceCount != 3 {
		t.Errorf("ProxiedDeviceCount = %d, want 3", f.ProxiedDeviceCount)
	}
	if !f.ProxiedListChanged {
		t.Error("ProxiedListChanged = false, want true")
	}
	if !f.IsWirelessProxy {
		t.Error("IsWirelessProxy = false, want true (non-zero proxied count)")
	}
}

// TestReclassifyZeroProxiedDeviceCountKnownButNotWireless proves a
// confirmed-zero PROXIED_DEVICE_COUNT (a device that answers the PID but
// currently proxies nothing) is recorded as Known without flipping
// IsWirelessProxy — "answered, currently zero" must be distinguishable from
// "never asked" downstream.
func TestReclassifyZeroProxiedDeviceCountKnownButNotWireless(t *testing.T) {
	f := &Fixture{}
	data := rdm.EncodeProxiedDeviceCount(rdm.ProxiedDeviceCount{Count: 0})
	reclassify(f, rdm.PIDProxiedDeviceCount, data)

	if !f.ProxiedDeviceCountKnown {
		t.Fatal("ProxiedDeviceCountKnown = false, want true")
	}
	if f.ProxiedDeviceCount != 0 {
		t.Errorf("ProxiedDeviceCount = %d, want 0", f.ProxiedDeviceCount)
	}
	if f.IsWirelessProxy {
		t.Error("IsWirelessProxy = true, want false for a confirmed-zero count")
	}
}

// awaitFixture polls reg until uid's fixture satisfies cond. Registry.Run()
// applies events on its own goroutine, so a command completing does not mean
// the registry has caught up yet.
func awaitFixture(t *testing.T, reg *Registry, uid rdm.UID, cond func(Fixture) bool) Fixture {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if f, ok := reg.Fixture(uid); ok && cond(f) {
			return f
		}
		if time.Now().After(deadline) {
			f, _ := reg.Fixture(uid)
			t.Fatalf("timed out waiting for fixture condition; last = %+v", f)
		}
		time.Sleep(time.Millisecond)
	}
}
