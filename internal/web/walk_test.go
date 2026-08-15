package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
	"benny512/internal/walk"
)

// walkFakeDevice is one scripted RDM responder for the walk tests below:
// answers GET/SET DMX_START_ADDRESS and GET/SET IDENTIFY_DEVICE, optionally
// NACKing IDENTIFY_DEVICE to exercise the "wireless proxy failure never
// blocks the walk" path.
type walkFakeDevice struct {
	startAddr    uint16
	identify     bool
	nackIdentify bool
}

// wireWalkDevices installs one OnSend handler answering every uid in
// devices, dispatching by DestinationUID — a multi-device analogue of
// device_test.go's wireDeviceResponder (which only handles one UID).
func (h *testHarness) wireWalkDevices(devices map[rdm.UID]*walkFakeDevice) {
	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		d, ok := devices[msg.DestinationUID]
		if !ok {
			return
		}
		var data []byte
		nack := false
		reason := rdm.NackUnknownPID
		switch msg.ParameterID {
		case rdm.PIDDMXStartAddress:
			if msg.CommandClass == rdm.SetCommand {
				d.startAddr = uint16(msg.ParameterData[0])<<8 | uint16(msg.ParameterData[1])
			} else {
				data = []byte{byte(d.startAddr >> 8), byte(d.startAddr)}
			}
		case rdm.PIDIdentifyDevice:
			if d.nackIdentify {
				nack, reason = true, rdm.NackProxyDrop
			} else if msg.CommandClass == rdm.SetCommand {
				d.identify = msg.ParameterData[0] != 0
			} else {
				v := byte(0)
				if d.identify {
					v = 1
				}
				data = []byte{v}
			}
		default:
			nack = true
		}
		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
		}
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: msg.DestinationUID, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseNackReason), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: []byte{byte(reason >> 8), byte(reason)},
			}
		} else {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: msg.DestinationUID, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
			}
		}
		h.clock.AfterFunc(time.Millisecond, func() { h.rdmc.HandleRDMResponse(resp) })
	}
}

// seedWalkFixtures registers two RDM fixtures on the seeded node/port and
// wires a responder for both, returning their UIDs and fake device state.
func seedWalkFixtures(t *testing.T, h *testHarness) (uidA, uidB rdm.UID, devs map[rdm.UID]*walkFakeDevice) {
	t.Helper()
	node := h.seedNode(t)
	uidA = rdm.UID{ManufacturerID: 0x2222, DeviceID: 1}
	uidB = rdm.UID{ManufacturerID: 0x2222, DeviceID: 2}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uidA)
	h.srv.Registry.NoteFixture(ref, uidB)

	devs = map[rdm.UID]*walkFakeDevice{
		uidA: {startAddr: 21},
		uidB: {startAddr: 1},
	}
	h.wireWalkDevices(devs)
	return uidA, uidB, devs
}

func startWalkAll(t *testing.T, h *testHarness, fixturesOnly bool) walkSessionResponse {
	t.Helper()
	rr := h.runHTTPAsync(t, "POST", "/api/walk/session", startWalkRequest{Order: "address", ScopeKind: "all", FixturesOnly: fixturesOnly})
	if rr.Code != http.StatusOK {
		t.Fatalf("start walk session: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp walkSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestWalkSession_NoneActive(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/walk/session", nil)
	var resp walkSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Active {
		t.Fatal("expected active=false with no session started")
	}
}

// TestWalkStart_OrdersByAddressAndIdentifiesFirst covers: session build
// from scope "all", address-ascending ordering (uidB's start address 1
// sorts before uidA's 21 despite uidA being registered first), and that
// landing on device 0 sends IDENTIFY_DEVICE ON for it only.
func TestWalkStart_OrdersByAddressAndIdentifiesFirst(t *testing.T) {
	h := newHarness(t)
	uidA, uidB, devs := seedWalkFixtures(t, h)

	resp := startWalkAll(t, h, false)
	if !resp.Active || resp.Session == nil {
		t.Fatalf("expected an active session, got %+v", resp)
	}
	if len(resp.Session.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(resp.Session.Devices))
	}
	if resp.Session.Devices[0].UID != uidB.String() {
		t.Errorf("Devices[0] = %s, want %s (lower start address first)", resp.Session.Devices[0].UID, uidB)
	}
	if resp.Session.Devices[1].UID != uidA.String() {
		t.Errorf("Devices[1] = %s, want %s", resp.Session.Devices[1].UID, uidA)
	}
	if resp.Session.Current != 0 {
		t.Errorf("Current = %d, want 0", resp.Session.Current)
	}
	if !resp.Session.Devices[0].IdentifyOn {
		t.Error("device 0 should have IdentifyOn=true after landing on it")
	}
	if resp.Session.Devices[0].IdentifyErr != "" {
		t.Errorf("unexpected identify error: %s", resp.Session.Devices[0].IdentifyErr)
	}
	if !devs[uidB].identify {
		t.Error("wire-level identify was not actually turned on for the fake device")
	}
	if devs[uidA].identify {
		t.Error("device 1 (not yet visited) should not have identify on")
	}
}

// TestWalkGoto_SwapsIdentifyExactlyOne is the core safety property from the
// task: moving from device 0 to device 1 turns identify OFF on device 0 and
// ON on device 1, never both at once.
func TestWalkGoto_SwapsIdentifyExactlyOne(t *testing.T) {
	h := newHarness(t)
	_, _, devs := seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	secondUID := resp.Session.Devices[1].UID

	rr := h.runHTTPAsync(t, "POST", "/api/walk/goto", walkGotoRequest{Index: 1})
	if rr.Code != http.StatusOK {
		t.Fatalf("goto: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Current != 1 {
		t.Fatalf("Current = %d, want 1", got.Session.Current)
	}

	firstUIDParsed, _ := rdm.ParseUID(firstUID)
	secondUIDParsed, _ := rdm.ParseUID(secondUID)
	if devs[firstUIDParsed].identify {
		t.Error("previous device should have identify turned off")
	}
	if !devs[secondUIDParsed].identify {
		t.Error("new current device should have identify turned on")
	}
	if !got.Session.Devices[1].IdentifyOn {
		t.Error("session should reflect IdentifyOn=true for the new current device")
	}
}

// TestWalkStatus_ConfirmedAutoAdvances covers the verification checklist +
// auto-advance requirement together: confirming the current device moves
// the cursor forward and swaps identify, without the caller needing a
// separate goto call.
func TestWalkStatus_ConfirmedAutoAdvances(t *testing.T) {
	h := newHarness(t)
	_, _, devs := seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	secondUID := resp.Session.Devices[1].UID

	rr := h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "confirmed"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Current != 1 {
		t.Fatalf("expected auto-advance to index 1, Current = %d", got.Session.Current)
	}
	if got.Session.Devices[0].Status != walk.StatusConfirmed {
		t.Errorf("Devices[0].Status = %s, want confirmed", got.Session.Devices[0].Status)
	}
	if got.Session.Devices[0].VisitedAt.IsZero() {
		t.Error("VisitedAt should be set once confirmed")
	}
	if got.Summary.Confirmed != 1 || got.Summary.Remaining != 1 {
		t.Errorf("Summary = %+v, want 1 confirmed / 1 remaining", got.Summary)
	}

	firstUIDParsed, _ := rdm.ParseUID(firstUID)
	secondUIDParsed, _ := rdm.ParseUID(secondUID)
	if devs[firstUIDParsed].identify {
		t.Error("confirmed device should have identify off after auto-advance")
	}
	if !devs[secondUIDParsed].identify {
		t.Error("newly-current device should have identify on after auto-advance")
	}
}

// TestWalkAutoAdvance_DisabledStaysPut confirms the toggle: with
// auto-advance off, confirming the current device must not move the cursor.
func TestWalkAutoAdvance_DisabledStaysPut(t *testing.T) {
	h := newHarness(t)
	_, _, _ = seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID

	rr := h.runHTTPAsync(t, "POST", "/api/walk/autoadvance", walkAutoAdvanceRequest{On: false})
	if rr.Code != http.StatusOK {
		t.Fatalf("autoadvance toggle: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "confirmed", Note: "looked right"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Current != 0 {
		t.Fatalf("Current = %d, want 0 (auto-advance disabled)", got.Session.Current)
	}
	if got.Session.Devices[0].Note != "looked right" {
		t.Errorf("Note = %q", got.Session.Devices[0].Note)
	}
}

// TestWalkStatus_ProblemWithNote covers the "Problem (with an optional
// short note)" path and that it does NOT auto-advance (only Confirmed does).
func TestWalkStatus_ProblemDoesNotAutoAdvance(t *testing.T) {
	h := newHarness(t)
	_, _, _ = seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID

	rr := h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "problem", Note: "wrong address, patched as U1/1 but reads U1/5"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Current != 0 {
		t.Errorf("Current = %d, want 0 (problem shouldn't auto-advance)", got.Session.Current)
	}
	if got.Session.Devices[0].Status != walk.StatusProblem {
		t.Errorf("Status = %s, want problem", got.Session.Devices[0].Status)
	}
	if got.Summary.Problems != 1 {
		t.Errorf("Summary.Problems = %d, want 1", got.Summary.Problems)
	}
}

// TestWalkIdentify_FailureIsInlineNotBlocking is the task's key safety
// requirement: an IDENTIFY_DEVICE NACK (the expected wireless-proxy path)
// must not fail the goto request — it shows up as IdentifyErr on the
// device, and a retry endpoint is available.
func TestWalkIdentify_FailureIsInlineNotBlocking(t *testing.T) {
	h := newHarness(t)
	_, _, devs := seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	secondUID := resp.Session.Devices[1].UID
	secondUIDParsed, _ := rdm.ParseUID(secondUID)
	devs[secondUIDParsed].nackIdentify = true

	rr := h.runHTTPAsync(t, "POST", "/api/walk/goto", walkGotoRequest{Index: 1})
	if rr.Code != http.StatusOK {
		t.Fatalf("goto should succeed even though identify NACKs: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Devices[1].IdentifyOn {
		t.Error("IdentifyOn should be false after a NACK")
	}
	if got.Session.Devices[1].IdentifyErr == "" {
		t.Error("IdentifyErr should be populated after a NACK")
	}

	// Retry: still NACKs, still doesn't fail the request.
	rr = h.runHTTPAsync(t, "POST", "/api/walk/identify/retry", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("retry: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var retried walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &retried)
	if retried.Session.Devices[1].IdentifyErr == "" {
		t.Error("retry should still surface the NACK inline")
	}

	// Clear the fault and retry again: should now succeed.
	devs[secondUIDParsed].nackIdentify = false
	rr = h.runHTTPAsync(t, "POST", "/api/walk/identify/retry", nil)
	var recovered walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &recovered)
	if !recovered.Session.Devices[1].IdentifyOn || recovered.Session.Devices[1].IdentifyErr != "" {
		t.Errorf("expected recovery after clearing the fault, got %+v", recovered.Session.Devices[1])
	}
}

// TestWalkIdentifyAllOff_ClearsEverything covers the manual escape hatch.
func TestWalkIdentifyAllOff_ClearsEverything(t *testing.T) {
	h := newHarness(t)
	_, _, devs := seedWalkFixtures(t, h)
	startWalkAll(t, h, false)

	rr := h.runHTTPAsync(t, "POST", "/api/walk/identify/all-off", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("all-off: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Current != -1 {
		t.Errorf("Current = %d, want -1 after all-off", got.Session.Current)
	}
	for uid, d := range devs {
		if d.identify {
			t.Errorf("device %s still has identify on after all-off", uid)
		}
	}
}

// TestWalkAddress_QuickFixAppliesAndUpdatesSnapshot covers the "quick fix in
// place" DMX start-address requirement.
func TestWalkAddress_QuickFixAppliesAndUpdatesSnapshot(t *testing.T) {
	h := newHarness(t)
	_, _, devs := seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	firstUIDParsed, _ := rdm.ParseUID(firstUID)

	rr := h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/address", walkAddressRequest{Value: 145})
	if rr.Code != http.StatusOK {
		t.Fatalf("address fix: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if devs[firstUIDParsed].startAddr != 145 {
		t.Errorf("wire-level start address = %d, want 145", devs[firstUIDParsed].startAddr)
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/walk/session", nil)
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Session.Devices[0].DMXStartAddress != 145 {
		t.Errorf("session snapshot DMXStartAddress = %d, want 145", got.Session.Devices[0].DMXStartAddress)
	}
	if !got.Session.Devices[0].AddressKnown {
		t.Error("AddressKnown should be true after the fix")
	}
}

func TestWalkEnd_ClearsSession(t *testing.T) {
	h := newHarness(t)
	seedWalkFixtures(t, h)
	startWalkAll(t, h, false)

	// /api/walk/end issues a real (fake-clock-driven) IDENTIFY off RDM
	// round-trip before clearing the session, so it needs the clock pumped
	// like every other RDM-backed walk call — a plain doJSON here would
	// block for the full real-time 15s deviceParamTimeout.
	rr := h.runHTTPAsync(t, "POST", "/api/walk/end", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("end: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/walk/session", nil)
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Active {
		t.Fatal("session should be cleared after /api/walk/end")
	}
}

func TestWalkGoto_NoActiveSessionIs404(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/walk/goto", walkGotoRequest{Index: 0})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rr.Code)
	}
}

// --- export ------------------------------------------------------------

func TestWalkExport_JSONFormat(t *testing.T) {
	h := newHarness(t)
	_, _, _ = seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "confirmed"})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/walk/export?format=json", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	var doc walkExportDoc
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal export doc: %v\nbody: %s", err, rr.Body.String())
	}
	if doc.AppVersion == "" {
		t.Error("AppVersion is empty")
	}
	if doc.Summary.Total != 2 || doc.Summary.Confirmed != 1 {
		t.Errorf("Summary = %+v", doc.Summary)
	}
	if len(doc.Session.Devices) != 2 {
		t.Fatalf("expected 2 devices in exported session, got %d", len(doc.Session.Devices))
	}
}

func TestWalkExport_TXTFormatIsReadable(t *testing.T) {
	h := newHarness(t)
	_, _, _ = seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "problem", Note: "reads U1/5 not U1/1"})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/walk/export?format=txt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Benny512 Rig Walk Export", "App version:", "Generated:", "Scope:", "Summary:",
		"PROBLEM", "reads U1/5 not U1/1", "UID ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("TXT export missing %q:\n%s", want, body)
		}
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".txt") {
		t.Errorf("Content-Disposition = %q, want .txt filename", cd)
	}
}

func TestWalkExport_NoSessionIs404(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/walk/export?format=json", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rr.Code)
	}
}

// --- persistence via SetWalkStorePath -----------------------------------

func TestWalkSession_PersistsAcrossServerRestart(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/rigwalk.json"

	h := newHarness(t)
	h.srv.SetWalkStorePath(path)
	_, _, _ = seedWalkFixtures(t, h)
	resp := startWalkAll(t, h, false)
	firstUID := resp.Session.Devices[0].UID
	h.runHTTPAsync(t, "POST", "/api/walk/"+firstUID+"/status", walkStatusRequest{Status: "confirmed"})

	// Simulate a restart: a brand new Server pointed at the same path
	// should resume the session (task ask: "a dropped connection or
	// accidental refresh doesn't lose the walk").
	h2 := newHarness(t)
	h2.srv.SetWalkStorePath(path)
	rr := doJSON(t, h2.srv.Handler(), "GET", "/api/walk/session", nil)
	var got walkSessionResponse
	json.Unmarshal(rr.Body.Bytes(), &got)
	if !got.Active {
		t.Fatal("expected the restarted server to resume the persisted session")
	}
	if got.Session.Devices[0].Status != walk.StatusConfirmed {
		t.Errorf("Devices[0].Status = %s, want confirmed to have survived the restart", got.Session.Devices[0].Status)
	}
}
