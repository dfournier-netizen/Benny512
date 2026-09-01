package web

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// seedToD feeds an unsolicited ArtTodData into h's RDMController, populating
// its Table-of-Devices cache for (ip, pa) without needing a live Discover
// round-trip — mirrors internal/session's own todDataInbound-based tests,
// but built from RDMController's exported HandleTodData since todDataInbound
// itself is unexported to package session.
//
// HandleTodData also emits an EventToDUpdate that Registry.Run (running on
// its own goroutine, started by newHarness) consumes asynchronously and
// folds into its own fixture table via mergeToD — so a caller that
// immediately clears the registry right after seedToD would otherwise race
// that goroutine: under -race's slower scheduling, the goroutine can (and
// did, before this fix) win and resurrect the very entry the test just
// asserted was cleared. Registry.Run's loop calls handleRDMEvent (the
// synchronous merge) BEFORE republishing on RDMEvents() (see its doc
// comment), so blocking here until that republished event appears is a
// deterministic proof the merge has already happened — not a sleep-and-hope.
func seedToD(t *testing.T, h *testHarness, ip netip.Addr, pa artnet.PortAddress, uids []rdm.UID) {
	t.Helper()
	td := artnet.TodData{
		ProtocolVersion: artnet.DefaultProtocolVersion,
		Net:             pa.Net,
		CommandResponse: session.TodFull,
		Address:         pa.SubUni(),
		UidTotal:        uint16(len(uids)),
		BlockCount:      0,
		Tod:             uids,
	}
	h.rdmc.HandleTodData(td, netip.AddrPortFrom(ip, session.ArtNetUDPPort))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-h.srv.Registry.RDMEvents():
			if ev.Kind == session.EventToDUpdate && ev.Node.Port.RawValue() == pa.RawValue() {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for Registry to finish processing seeded ToD data")
		}
	}
}

func TestDevicesClear_AllWipesEveryDeviceAndToD(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	portA := mustPortAddr(t, 0)
	portB := mustPortAddr(t, 1)
	uid1 := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	uid2 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}

	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: portA}, uid1)
	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: portB}, uid2)
	seedToD(t, h, node.Key.IP, portA, []rdm.UID{uid1})
	seedToD(t, h, node.Key.IP, portB, []rdm.UID{uid2})

	if fx := h.srv.Registry.Devices(); len(fx) != 2 {
		t.Fatalf("seeded %d devices, want 2", len(fx))
	}

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]string{"scope": "all"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got devicesClearResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scope != "all" || got.Cleared != 2 || got.TodCleared != 2 {
		t.Fatalf("response = %+v, want scope=all cleared=2 todCleared=2", got)
	}

	if fx := h.srv.Registry.Devices(); len(fx) != 0 {
		t.Fatalf("devices after clear = %+v, want none", fx)
	}
	if _, ok := h.rdmc.ToD(node.Key.IP, portA); ok {
		t.Error("portA ToD should be cleared")
	}
	if _, ok := h.rdmc.ToD(node.Key.IP, portB); ok {
		t.Error("portB ToD should be cleared")
	}

	// The Art-Net node table is explicitly out of scope for this endpoint.
	if nodes := h.srv.Registry.Nodes(); len(nodes) != 1 {
		t.Fatalf("node table should survive a devices/clear: %+v", nodes)
	}
}

func TestDevicesClear_PortLeavesOtherPortsIntact(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	portA := mustPortAddr(t, 0)
	portB := mustPortAddr(t, 1)
	uid1 := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 1}
	uid2 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}
	uid3 := rdm.UID{ManufacturerID: 0x2222, DeviceID: 3}

	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: portA}, uid1)
	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: portB}, uid2)
	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: portB}, uid3)
	seedToD(t, h, node.Key.IP, portA, []rdm.UID{uid1})
	seedToD(t, h, node.Key.IP, portB, []rdm.UID{uid2, uid3})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]any{
		"scope": "port", "ip": node.Key.IP.String(), "portAddress": portB.RawValue(),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got devicesClearResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scope != "port" || got.Cleared != 2 || got.TodCleared != 1 {
		t.Fatalf("response = %+v, want scope=port cleared=2 todCleared=1", got)
	}

	fx := h.srv.Registry.Devices()
	if len(fx) != 1 || fx[0].UID != uid1 {
		t.Fatalf("devices after port clear = %+v, want only uid1 on portA", fx)
	}
	if _, ok := h.rdmc.ToD(node.Key.IP, portB); ok {
		t.Error("portB ToD should be cleared")
	}
	cached, ok := h.rdmc.ToD(node.Key.IP, portA)
	if !ok || len(cached) != 1 || cached[0] != uid1 {
		t.Fatalf("portA ToD should survive untouched, got %v ok=%v", cached, ok)
	}
}

func TestDevicesClear_UnknownScope400(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]string{"scope": "everything"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400: %s", rr.Code, rr.Body.String())
	}
}

func TestDevicesClear_MalformedBody400(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]any{"scope": "all", "bogusField": 1})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for an unknown JSON field: %s", rr.Code, rr.Body.String())
	}
}

func TestDevicesClear_BadIP400(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]any{"scope": "port", "ip": "not-an-ip", "portAddress": 0})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400: %s", rr.Code, rr.Body.String())
	}
}

func TestDevicesClear_PortAddressOutOfRange400(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]any{"scope": "port", "ip": "10.0.0.5", "portAddress": 40000})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400: %s", rr.Code, rr.Body.String())
	}
}

// TestDevicesClear_DoesNotTouchCaptureDescriptorCacheOrPatch pins down the
// endpoint's deliberately narrow scope (task ask: "keep your logging +
// responses for now") — everything POST /api/reset would wipe but this
// endpoint must not.
func TestDevicesClear_DoesNotTouchCaptureDescriptorCacheOrPatch(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x5370, DeviceID: 1}
	ref := session.NodeRef{Key: node.Key, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	// Seed the process-wide PARAMETER_DESCRIPTION cache + per-UID
	// introspection state via a live GET, same as TestDeviceParamsFlow.
	pd := rdm.ParameterDescription{
		PID: 0x8010, PDLSize: 1, DataType: rdm.DSUnsignedByte, CommandClass: rdm.PDCommandClassGetSet,
		Unit: rdm.UnitNone, Prefix: rdm.PrefixNone, MinValue: 0, MaxValue: 16, DefaultValue: 16,
		Description: "PIXEL COUNT",
	}
	pdBytes := rdm.EncodeParameterDescription(pd)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDParameterDescription:
			return pdBytes, false, 0, 0
		case 0x8010:
			return []byte{7}, false, 0, 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})
	uidStr := uid.String()
	if rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/param/8010", nil); rr.Code != http.StatusOK {
		t.Fatalf("seed GET param status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Seed both capture rings.
	h.srv.Capture.Add(capture.Entry{Kind: "ArtDmx", Size: 10})
	h.srv.RDMCapture.Add(capture.Entry{Kind: "ArtRdm", Size: 20})

	// Seed the patch.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash 1", Universe: 0, StartAddress: 1, Footprint: 4})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed patch entry status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/devices/clear", map[string]string{"scope": "all"})
	if rr.Code != http.StatusOK {
		t.Fatalf("devices/clear status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Capture rings untouched.
	if got := h.srv.Capture.Snapshot(capture.Filter{}, 0); len(got) != 1 {
		t.Errorf("general capture ring after devices/clear = %+v, want 1 entry retained", got)
	}
	if got := h.srv.RDMCapture.Snapshot(capture.Filter{}, 0); len(got) != 1 {
		t.Errorf("RDM capture ring after devices/clear = %+v, want 1 entry retained", got)
	}

	// Descriptor cache / per-UID introspection state untouched: re-register
	// the device (as a fresh Discover would after the clear — GET /api/
	// device/{uid}/params 404s for a UID the registry doesn't know about)
	// and confirm its descriptor is still there without a fresh RDM
	// round-trip (no wireDeviceResponder traffic needed for this GET).
	h.srv.Registry.NoteFixture(ref, uid)
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/device/"+uidStr+"/params", nil)
	var list []paramDescriptorJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal (status=%d body=%s): %v", rr.Code, rr.Body.String(), err)
	}
	found := false
	for _, d := range list {
		if d.PID == "8010" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 0x8010's descriptor to survive devices/clear: %+v", list)
	}

	// Patch untouched.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var patchResp patchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(patchResp.Patch.Entries) != 1 {
		t.Fatalf("patch after devices/clear = %+v, want 1 entry retained", patchResp.Patch)
	}
}

// TestDevicesClearResponse_PortAddressZeroMarshalsExplicit guards the same
// defect class as Entry.Universe: a "port" scope clear on Port-Address
// (universe) 0 — a real, ordinary universe, not an absent value — must
// echo "portAddress":0 explicitly rather than omitting the key. Asserting
// a struct field equals 0 would be vacuous; this asserts the marshalled
// bytes.
func TestDevicesClearResponse_PortAddressZeroMarshalsExplicit(t *testing.T) {
	resp := devicesClearResponse{Scope: "port", IP: "10.0.0.5", PortAddress: 0, Cleared: 1, TodCleared: 1}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"portAddress":0`) {
		t.Errorf("marshalled devices/clear response missing \"portAddress\":0; got %s", got)
	}
}
