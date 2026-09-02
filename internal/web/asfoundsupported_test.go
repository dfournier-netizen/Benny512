package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// This file is the regression suite for bench capture RDM-LOG24
// (2026-09-02): eight GLP JDC-1 fixtures, 23 minutes, 333 packets, 138 ACK
// and 26 NACK — every NACK an UNKNOWN_PID, every one avoidable. Benny512's
// as-found pass asked each fixture for PAN_INVERT (9x) and PAN_TILT_SWAP
// (9x) although the JDC-1's own SUPPORTED_PARAMETERS response lists neither
// (it lists TILT_INVERT, which ACKed all nine times, because a JDC-1 tilts
// and does not pan), and the two other offenders in the capture —
// PRODUCT_DETAIL_ID_LIST and PROXIED_DEVICE_COUNT — are PIDs this pass must
// never ask for at all.
//
// The tests below assert on the REQUESTS ACTUALLY SENT, not merely on the
// result: a gate that skipped the round trip and a gate that made it and
// discarded the answer would produce the same AsFoundSettings, and only one
// of them fixes the problem the capture recorded (on the CRMX/MoonLite2
// wireless proxy three other bench logs document, a shared buffer full of
// pointless transactions is what stops that proxy answering for unrelated
// devices).

// jdc1SupportedPIDs is the GLP JDC-1's real SUPPORTED_PARAMETERS response
// from RDM-LOG24: sixteen ESTA PIDs plus five manufacturer-specific ones.
//
// Note what is NOT in it and yet must still be asked for: DEVICE_INFO
// (0x0060) and DMX_START_ADDRESS (0x00F0). Both are E1.20 required PIDs,
// and §10.4.1 says PIDs in the minimum-support list "shall not be reported"
// here — so their absence from this list means nothing whatsoever, and a
// gate that read absence as "not implemented" would blank a core field on a
// perfectly healthy fixture. TestAsFound_CorePIDsAreNeverGated pins that.
func jdc1SupportedPIDs() []rdm.ParameterID {
	return []rdm.ParameterID{
		rdm.PIDDeviceModelDescription,    // 0x0080
		rdm.PIDManufacturerLabel,         // 0x0081
		rdm.PIDDeviceLabel,               // 0x0082
		rdm.PIDDMXPersonality,            // 0x00E0
		rdm.PIDDeviceHours,               // 0x0400
		rdm.PIDTiltInvert,                // 0x0601 — listed; PAN_INVERT/PAN_TILT_SWAP are not
		rdm.PIDResetDevice,               // 0x1001
		rdm.PIDSensorDefinition,          // 0x0200
		rdm.PIDSensorValue,               // 0x0201
		rdm.PIDDisplayInvert,             // 0x0500
		rdm.PIDCurve,                     // 0x0343
		rdm.PIDCurveDescription,          // 0x0344
		rdm.PIDDMXPersonalityDescription, // 0x00E1
		rdm.PIDFactoryDefaults,           // 0x0090
		rdm.PIDSoftwareVersionLabel,      // 0x00C0
		rdm.PIDSlotInfo,                  // 0x0120
		0x8001, 0x8002, 0x8003, 0x8004, 0x8005,
	}
}

// jdc1Recorder is a scripted JDC-1 that also records every GET it is asked
// for, in order, so a test can assert on the wire traffic itself.
type jdc1Recorder struct {
	mu   sync.Mutex
	gets []rdm.ParameterID
	sets []rdm.ParameterID
	// supported is the list this device answers GET SUPPORTED_PARAMETERS
	// with. nackSupported makes it NACK that GET instead — the "we have no
	// supported-parameter data at all" case.
	supported     []rdm.ParameterID
	nackSupported bool
}

func (d *jdc1Recorder) count(pid rdm.ParameterID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, p := range d.gets {
		if p == pid {
			n++
		}
	}
	return n
}

func (d *jdc1Recorder) setCount(pid rdm.ParameterID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, p := range d.sets {
		if p == pid {
			n++
		}
	}
	return n
}

func (d *jdc1Recorder) sentPIDs() []rdm.ParameterID {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]rdm.ParameterID(nil), d.gets...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// wireJDC1 installs a responder for uid that answers exactly like the
// fixture in RDM-LOG24: the listed PIDs ACK, everything else NACKs
// UNKNOWN_PID — which is what makes an ungated probe show up as a NACK in
// the assertions below rather than being quietly absorbed.
func (h *testHarness) wireJDC1(uid rdm.UID, d *jdc1Recorder) {
	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		d.mu.Lock()
		if msg.CommandClass == rdm.GetCommand {
			d.gets = append(d.gets, msg.ParameterID)
		} else if msg.CommandClass == rdm.SetCommand {
			d.sets = append(d.sets, msg.ParameterID)
		}
		d.mu.Unlock()

		var data []byte
		nack := false
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			if d.nackSupported {
				nack = true
			} else {
				data = rdm.EncodeSupportedParameters(d.supported)
			}
		case rdm.PIDDeviceInfo:
			// Required PID, absent from SUPPORTED_PARAMETERS by design.
			data = encodeJDC1DeviceInfo()
		case rdm.PIDDMXStartAddress:
			data = []byte{0x00, 0x29} // 41
		case rdm.PIDDeviceLabel:
			data = []byte("JDC1 SL Boom")
		case rdm.PIDTiltInvert:
			data = []byte{0x01}
		case rdm.PIDCurve:
			data = []byte{0x02, 0x04} // curve 2 of 4
		case rdm.PIDCurveDescription:
			data = append([]byte{msg.ParameterData[0]}, []byte("Square Law")...)
		case rdm.PIDDMXPersonalityDescription:
			data = append([]byte{msg.ParameterData[0], 0x00, 0x1E}, []byte("Extended")...)
		default:
			nack = true
		}

		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
		}
		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
			PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice,
			CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
		}
		if nack {
			resp.PortIDOrResponseType = byte(rdm.ResponseNackReason)
			resp.ParameterData = []byte{byte(rdm.NackUnknownPID >> 8), byte(rdm.NackUnknownPID)}
		}
		h.clock.AfterFunc(time.Millisecond, func() { h.rdmc.HandleRDMResponse(resp) })
	}
}

func encodeJDC1DeviceInfo() []byte {
	return []byte{
		0x01, 0x00, // protocol 1.0
		0x00, 0x2A, // model
		0x05, 0x10, // product category
		0x00, 0x00, 0x00, 0x01, // software version
		0x00, 0x1E, // footprint 30
		0x02,       // current personality
		0x03,       // personality count
		0x00, 0x29, // start address 41
		0x00, // sub-device count hi
		0x00, // sub-device count lo
		0x00, // sensor count
	}
}

// seedJDC1 registers one JDC-1 on the seeded node and wires its responder.
func seedJDC1(t *testing.T, h *testHarness, d *jdc1Recorder) rdm.UID {
	t.Helper()
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x676C, DeviceID: 0x000001} // GLP
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireJDC1(uid, d)
	return uid
}

// commitToJDC1 creates one patch entry and commits it to uid, returning the
// entry ID and the commit response.
func commitToJDC1(t *testing.T, h *testHarness, uid rdm.UID) (string, commitResponse) {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries",
		entryRequest{Name: "SL Boom 1", Universe: 0, StartAddress: 41, Footprint: 30})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	rr = h.runHTTPAsync(t, "POST", "/api/patch/reconcile/"+id+"/commit", commitRequest{DeviceUID: uid.String()})
	if rr.Code != http.StatusOK {
		t.Fatalf("commit: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp commitResponse
	mustUnmarshal(t, rr, &resp)
	if resp.ReadError != "" {
		t.Fatalf("as-found read failed outright: %s", resp.ReadError)
	}
	return id, resp
}

// TestAsFound_LOG24_NeverAsksForWhatTheFixtureDoesNotAdvertise is the
// capture, reproduced. A device answering with the JDC-1's real 21-PID list
// must be asked for none of RDM-LOG24's four wasted PIDs, must still be
// asked for the settings it does advertise, and must end up reporting the
// pan settings as "not fitted" rather than as a failed read.
func TestAsFound_LOG24_NeverAsksForWhatTheFixtureDoesNotAdvertise(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{supported: jdc1SupportedPIDs()}
	uid := seedJDC1(t, h, dev)

	_, resp := commitToJDC1(t, h, uid)

	// --- the traffic ------------------------------------------------------
	//
	// Asserting on requests, not on results: skipping the round trip and
	// making it then discarding the answer are indistinguishable in the
	// stored settings, and only the first one fixes the capture.
	for _, banned := range []struct {
		pid  rdm.ParameterID
		name string
	}{
		{rdm.PIDPanInvert, "PAN_INVERT (0x0600)"},
		{rdm.PIDPanTiltSwap, "PAN_TILT_SWAP (0x0602)"},
		{rdm.PIDProductDetailIDList, "PRODUCT_DETAIL_ID_LIST (0x0070)"},
		{rdm.PIDProxiedDeviceCount, "PROXIED_DEVICE_COUNT (0x0011)"},
	} {
		if n := dev.count(banned.pid); n != 0 {
			t.Errorf("as-found pass sent %d GET(s) for %s — the fixture's own SUPPORTED_PARAMETERS does not list it, so every one of those is a guaranteed NACK UNKNOWN_PID (RDM-LOG24)\nall GETs sent: %v",
				n, banned.name, dev.sentPIDs())
		}
	}
	for _, wanted := range []struct {
		pid  rdm.ParameterID
		name string
	}{
		{rdm.PIDTiltInvert, "TILT_INVERT (0x0601)"},
		{rdm.PIDCurve, "CURVE (0x0343)"},
		{rdm.PIDDeviceLabel, "DEVICE_LABEL (0x0082)"},
	} {
		if n := dev.count(wanted.pid); n == 0 {
			t.Errorf("as-found pass never asked for %s, which this fixture DOES advertise — the gate has over-reached and is now hiding real settings\nall GETs sent: %v",
				wanted.name, dev.sentPIDs())
		}
	}
	// One SUPPORTED_PARAMETERS for the whole pass. The gate resolves it
	// once, sequentially, before the concurrent fan-out precisely so the
	// seven parallel reads cannot each spend their own (params'
	// resolveSupportedSet reads its "already attempted" flag under an
	// RLock and sets it afterwards, so concurrent entrants race it).
	if n := dev.count(rdm.PIDSupportedParameters); n != 1 {
		t.Errorf("GET SUPPORTED_PARAMETERS sent %d times in one as-found pass, want exactly 1 — the list is cached per UID and must be resolved once, before the fan-out\nall GETs sent: %v",
			n, dev.sentPIDs())
	}

	// --- what the owner sees ---------------------------------------------
	byField := map[patch.DiffField]patch.DiffLine{}
	for _, l := range resp.Entry.Diff {
		byField[l.Field] = l
	}
	for _, f := range []patch.DiffField{patch.FieldPanInvert, patch.FieldPanTiltSwap} {
		l := byField[f]
		if l.State != patch.DiffNotFitted {
			t.Errorf("%s state = %q, want %q — \"this fixture has no pan\" is a fact about the fixture, not a failed read",
				f, l.State, patch.DiffNotFitted)
		}
		if l.FoundErr != "" {
			t.Errorf("%s carries an error string %q — not-fitted is not an error and must not read as one", f, l.FoundErr)
		}
		if l.FoundKnown {
			t.Errorf("%s reports FoundKnown=true for hardware the fixture does not have: %+v", f, l)
		}
	}
	if l := byField[patch.FieldTiltInvert]; l.State == patch.DiffNotFitted || !l.FoundKnown || !l.FoundBool {
		t.Errorf("tiltInvert = %+v, want a real read of true — a JDC-1 tilts, and TILT_INVERT is on its supported list", l)
	}
	if l := byField[patch.FieldDimmerCurve]; !l.FoundKnown || l.FoundNum != 2 {
		t.Errorf("dimmerCurve = %+v, want a real read of 2", l)
	}
	if l := byField[patch.FieldDeviceLabel]; !l.FoundKnown || l.FoundText != "JDC1 SL Boom" {
		t.Errorf("deviceLabel = %+v, want a real read", l)
	}
	if resp.Entry.NotFittedCount != 2 {
		t.Errorf("notFittedCount = %d, want 2 (pan invert and pan/tilt swap)", resp.Entry.NotFittedCount)
	}
	// Not-fitted must NOT inflate the unread badge: "settings we could not
	// read" is a row to go and investigate, "settings this fixture does not
	// have" is a finished one.
	for _, l := range resp.Entry.Diff {
		if l.Field == patch.FieldPanInvert && l.State == patch.DiffUnread {
			t.Error("panInvert counted as unread — see NotFittedCount's doc comment")
		}
	}
}

// TestAsFound_LOG24_TotalGETCount is the traffic budget for one as-found
// pass against the RDM-LOG24 fixture, pinned as a number so a future field
// added to the pass cannot quietly reintroduce a blind probe.
//
// Before this change the pass cost 10 GETs: SUPPORTED_PARAMETERS (spent by
// params' own CURVE gate), DEVICE_INFO, DMX_START_ADDRESS, DEVICE_LABEL,
// CURVE, CURVE_DESCRIPTION, DMX_PERSONALITY_DESCRIPTION, TILT_INVERT — and
// PAN_INVERT and PAN_TILT_SWAP, both NACK UNKNOWN_PID, both avoidable.
// After it, 8: the same list without those two.
func TestAsFound_LOG24_TotalGETCount(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{supported: jdc1SupportedPIDs()}
	uid := seedJDC1(t, h, dev)
	commitToJDC1(t, h, uid)

	want := []rdm.ParameterID{
		rdm.PIDSupportedParameters,       // 0x0050
		rdm.PIDDeviceInfo,                // 0x0060
		rdm.PIDDeviceLabel,               // 0x0082
		rdm.PIDDMXPersonalityDescription, // 0x00E1
		rdm.PIDDMXStartAddress,           // 0x00F0
		rdm.PIDCurve,                     // 0x0343
		rdm.PIDCurveDescription,          // 0x0344
		rdm.PIDTiltInvert,                // 0x0601
	}
	got := dev.sentPIDs()
	if len(got) != len(want) {
		t.Errorf("as-found pass sent %d GETs, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("GET %d = 0x%04X, want 0x%04X (full traffic: %v)", i, uint16(got[min(i, len(got)-1)]), uint16(want[i]), got)
		}
	}
}

// TestAsFound_LOG24_NotFittedStateIsOnTheWire asserts the MARSHALLED BYTES
// of the persisted patch. A struct-field assertion here would pass
// vacuously against the exact bug it guards: an `omitempty` on State (or on
// Known beside it) reads back as the zero value either way, and a later
// agent renders the owner's screen off these bytes.
func TestAsFound_LOG24_NotFittedStateIsOnTheWire(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{supported: jdc1SupportedPIDs()}
	uid := seedJDC1(t, h, dev)
	commitToJDC1(t, h, uid)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get patch: status=%d", rr.Code)
	}
	body := rr.Body.String()

	// The whole not-fitted reading, byte for byte: known:false and a real
	// false value beside an explicit state. No key may be missing.
	for _, want := range []string{
		`"panInvert":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"panTiltSwap":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"tiltInvert":{"known":true,"value":true,`,
		`"state":"read"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("persisted patch is missing %s on the wire\ngot: %s", want, body)
		}
	}
	// And nothing may be left in the empty-string state the v4 shape would
	// unmarshal to — that is neither of the three states and would render
	// as a blank chip.
	if strings.Contains(body, `"state":""`) {
		t.Errorf("a setting reached the wire with an empty state:\n%s", body)
	}
}

// TestAsFound_NoSupportedParametersMeansUnknownNotNotFitted is, like
// TestAsFound_CorePIDsAreNeverGated, a guard against the wrong fix rather
// than a proof of the original defect: it passes against the unfixed code
// and fails against a gate that treats an unanswered SUPPORTED_PARAMETERS
// as "supports nothing" — verified by writing that gate.
//
// It is the trap the
// brief names: absence from SUPPORTED_PARAMETERS is only meaningful if we
// actually have that device's list. A responder that NACKs the list itself
// (legal — E1.20 does not require SUPPORTED_PARAMETERS to be answerable by
// every responder in every state, and real proxies drop it) must be asked
// for everything, exactly as before the gate existed, and every setting it
// then fails to answer must read "unknown", never "not fitted".
func TestAsFound_NoSupportedParametersMeansUnknownNotNotFitted(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{nackSupported: true}
	uid := seedJDC1(t, h, dev)

	_, resp := commitToJDC1(t, h, uid)

	for _, pid := range []rdm.ParameterID{rdm.PIDPanInvert, rdm.PIDTiltInvert, rdm.PIDPanTiltSwap, rdm.PIDCurve} {
		if dev.count(pid) == 0 {
			t.Errorf("0x%04X was never asked for although this device gave us NO supported-parameter list — \"we don't know what it supports\" must never be read as \"it supports nothing\"\nall GETs sent: %v",
				uint16(pid), dev.sentPIDs())
		}
	}
	for _, l := range resp.Entry.Diff {
		if l.State == patch.DiffNotFitted {
			t.Errorf("%s reported as not-fitted on a device whose SUPPORTED_PARAMETERS never answered — that is inventing knowledge: %+v", l.Field, l)
		}
	}
	// The pan settings NACKed on the wire here, so they are unknown WITH a
	// reason — the pre-gate behaviour, unchanged.
	for _, l := range resp.Entry.Diff {
		if l.Field == patch.FieldPanInvert {
			if l.State != patch.DiffUnread {
				t.Errorf("panInvert state = %q, want %q", l.State, patch.DiffUnread)
			}
			if l.FoundErr == "" {
				t.Error("a NACKed read must still say why it is unknown")
			}
		}
	}
}

// TestAsFound_CorePIDsAreNeverGated is the E1.20 §10.4.1 half of the rule.
// It is an OVER-REACH guard, not a proof of the original defect: it passes
// against the unfixed code (which asked for everything) and fails against a
// gate widened to cover required PIDs — verified by widening it.
// The JDC-1's list contains neither DEVICE_INFO nor DMX_START_ADDRESS,
// because required PIDs "shall not be reported" there — so a gate that
// treated absence as "not implemented" would blank the two most important
// fields on the Reconcile screen for a completely healthy fixture. This is
// the same line internal/params' isSpeculativePID draws.
func TestAsFound_CorePIDsAreNeverGated(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{supported: jdc1SupportedPIDs()}
	uid := seedJDC1(t, h, dev)

	_, resp := commitToJDC1(t, h, uid)

	for _, pid := range []rdm.ParameterID{rdm.PIDDeviceInfo, rdm.PIDDMXStartAddress} {
		if dev.count(pid) == 0 {
			t.Errorf("0x%04X was gated away, but it is an E1.20 required PID and is therefore absent from SUPPORTED_PARAMETERS BY SPEC — its absence means nothing\nall GETs sent: %v",
				uint16(pid), dev.sentPIDs())
		}
	}
	byField := map[patch.DiffField]patch.DiffLine{}
	for _, l := range resp.Entry.Diff {
		byField[l.Field] = l
	}
	if l := byField[patch.FieldStartAddress]; !l.FoundKnown || l.FoundNum != 41 {
		t.Errorf("startAddress = %+v, want a real read of 41", l)
	}
	if l := byField[patch.FieldFootprint]; !l.FoundKnown || l.FoundNum != 30 {
		t.Errorf("footprint = %+v, want a real read of 30 (from DEVICE_INFO)", l)
	}
}

// TestReconcilePush_RefusesANotFittedField: the push endpoint is the only
// one here that writes to a fixture, and a SET for hardware the device has
// just told us it does not have is the same wasted transaction in the other
// direction — with a guaranteed NACK at the end of it.
func TestReconcilePush_RefusesANotFittedField(t *testing.T) {
	h := newHarness(t)
	dev := &jdc1Recorder{supported: jdc1SupportedPIDs()}
	uid := seedJDC1(t, h, dev)
	id, _ := commitToJDC1(t, h, uid)

	// Give the entry an intention for a setting the fixture does not have,
	// so the push has something it would otherwise try to write.
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].Intended.PanInvert = patch.SettingBool{Known: true, Value: true, State: patch.SettingRead}
		return nil
	}); err != nil {
		t.Fatalf("seed intention: %v", err)
	}

	before := dev.count(rdm.PIDPanInvert)
	rr := h.runHTTPAsync(t, "POST", "/api/patch/reconcile/"+id+"/push", pushRequest{Fields: []string{"panInvert"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("push: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp pushResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Applied != 0 {
		t.Errorf("applied = %d, want 0 — there is nothing on this fixture to write to", resp.Applied)
	}
	if len(resp.Results) != 1 || resp.Results[0].OK || resp.Results[0].Error == "" {
		t.Errorf("push result = %+v, want a refusal with a reason", resp.Results)
	}
	if n := dev.setCount(rdm.PIDPanInvert); n != 0 {
		t.Errorf("push put %d SET PAN_INVERT on the wire — the fixture has no pan and every one of those is a guaranteed NACK", n)
	}
	if after := dev.count(rdm.PIDPanInvert); after != before {
		t.Errorf("push spent %d PAN_INVERT GET transaction(s) (the confirming re-read) on a fixture that has no pan", after-before)
	}
}
