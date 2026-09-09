package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
)

// testHarness builds a full Server over session.FakeTransport/FakeClock and
// seeds one node so REST tests have something to query, mirroring how
// cmd/benny512 --demo will wire things.
type testHarness struct {
	srv   *Server
	nodes *session.ArtNetSession
	rdmc  *session.RDMController
	tport *session.FakeTransport
	clock *session.FakeClock
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	// internal/params keeps process-wide, package-level state — the
	// learned PARAMETER_DESCRIPTION cache (descCache, keyed only on
	// manufacturer ID) and per-UID introspection state (uidStates, keyed
	// on the full UID), both intentionally NOT scoped to any one
	// params.Client or session.RDMController (see introspect.go's "shared
	// descriptor cache" doc comment: a manufacturer PID's shape is a
	// firmware-scoped constant meant to be shared across every Client
	// touching that manufacturer's real hardware for the life of the
	// process). That's correct for production, where RDM UIDs are globally
	// unique. It is NOT safe for this package's tests: many _test.go files
	// reuse the same literal manufacturer ID (0x22A6 in devicecontrol_test.go
	// alone) or even the exact same UID (rdm.UID{0x2222, 1} appears in
	// capture_export_test.go, patch_test.go and walk_test.go) across
	// independent test cases, each building its own fresh
	// FakeTransport/FakeClock/RDMController via this harness but sharing
	// that one process-wide cache regardless. A prior test's learned
	// "SUPPORTED_PARAMETERS doesn't list PID X" or "this UID NACKs
	// PARAMETER_DESCRIPTION" fact would silently answer a later,
	// unrelated test's query about a same-numbered but semantically
	// different fake device without ever touching that test's own
	// FakeTransport — which then leaves that test's scripted wire
	// exchange one request short, and the real handler goroutine blocks
	// forever waiting for a response the test's fake responder never
	// sends for it (surfacing as "timed out waiting for HTTP handler to
	// resolve" with no fake-clock progress possible, since the stall
	// isn't a timer at all) — or simply returns a stale cached answer
	// (e.g. CapturePresetSupported=false) left over from a different
	// test's device. Reset unconditionally, before AND after every test
	// (Cleanup covers t.Fatal/panic exits too), so no test's result can
	// depend on what ran before it, in either direction, regardless of
	// -shuffle order or literal UID/manufacturer-ID reuse.
	params.ClearAllDeviceState()
	params.ClearDescriptorCache()
	t.Cleanup(func() {
		params.ClearAllDeviceState()
		params.ClearDescriptorCache()
	})
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()
	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: tport, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(100)
	rdmRing := capture.New(100)
	srv := New(nodes, rdmc, dmx, reg, ring, rdmRing)
	go reg.Run()
	return &testHarness{srv: srv, nodes: nodes, rdmc: rdmc, tport: tport, clock: clock}
}

func (h *testHarness) seedNode(t *testing.T) session.Node {
	t.Helper()
	reply := artnet.PollReply{
		IPAddress: [4]byte{10, 0, 0, 5},
		ShortName: "EN4",
		LongName:  "Netron EN4 Test",
		NumPorts:  1,
		PortTypes: [4]byte{0x80, 0, 0, 0}, // port 0 output
		Status1:   0x02,                   // RDM capable
	}
	h.nodes.HandlePollReply(reply, netip.MustParseAddrPort("10.0.0.5:6454"))
	n, _ := h.nodes.Node(session.NodeKey{IP: netip.MustParseAddr("10.0.0.5"), BindIndex: 1})
	return n
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestGetNodesEmpty(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/nodes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(out))
	}
}

func TestGetNodesReturnsSeeded(t *testing.T) {
	h := newHarness(t)
	h.seedNode(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/nodes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 node, got %d: %s", len(out), rr.Body.String())
	}
	if out[0]["ip"] != "10.0.0.5" {
		t.Errorf("got ip %v", out[0]["ip"])
	}
	if out[0]["rdmCapable"] != true {
		t.Errorf("expected rdmCapable true, got %v", out[0]["rdmCapable"])
	}
}

// TestGetNodesSinglePortEN4NotPaddedToFour is this round's regression test
// for the bench report "every node shows four ports, three of them n/a"
// (RDM-LOG7, 2026-08-26): a real Obsidian EN4 sends one ArtPollReply PER
// PHYSICAL PORT, each its own bind index, each declaring NumPorts=1 with
// only port[0] populated — the theory was that Benny512 renders the
// unused port[1..3] wire slots as phantom ports reporting neither
// input nor output. Investigating found nothing to fix (see this round's
// notes entry for the full trace): internal/session's nodeFromPollReply
// already clamps to NumPorts and only ever builds that many NodePort
// entries, and toNodeJSON below only serializes n.Ports — so this asserts
// that behavior at the HTTP boundary this package owns, using the exact
// field values RDM-LOG7 captured, to lock it in against a regression.
func TestGetNodesSinglePortEN4NotPaddedToFour(t *testing.T) {
	h := newHarness(t)
	ip := netip.MustParseAddr("2.11.90.4")
	// Three bind indices, byte-for-byte the NumPorts/PortTypes/GoodInput/
	// GoodOutputB/SwOut RDM-LOG7 captured for "Port 1"/"Port 2"/"Port 3" of
	// a real NETRON EN4 (see also internal/artnet/codec_test.go's
	// TestGoldenArtPollReplyDecodeRealEN4SinglePort, the same evidence at
	// the decode layer, and cmd/benny512/demo.go's realEN4PortReplies,
	// the same shape wired into --demo for a render-proof screenshot).
	for i, bind := range []byte{1, 2, 3} {
		h.nodes.HandlePollReply(artnet.PollReply{
			IPAddress: ip.As4(), BindIndex: bind,
			ShortName: fmt.Sprintf("Port %d", i+1), LongName: "NETRON EN4",
			NumPorts: 1, PortTypes: [4]byte{0x80, 0, 0, 0},
			GoodInput: [4]byte{0x08, 0, 0, 0}, GoodOutputB: [4]byte{0x40, 0, 0, 0},
			SwIn: [4]byte{byte(i), 0, 0, 0}, SwOut: [4]byte{byte(i), 0, 0, 0},
			Status1: 0xE2,
		}, netip.AddrPortFrom(ip, session.ArtNetUDPPort))
	}

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/nodes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out []nodeJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Three bind indices at one IP means three separate node rows, not one
	// row with four ports — see this round's report on whether that reads
	// correctly to a lighting tech (flagged, not restructured).
	if len(out) != 3 {
		t.Fatalf("expected 3 node rows (one per bind index), got %d: %s", len(out), rr.Body.String())
	}
	for _, n := range out {
		if len(n.Ports) != 1 {
			t.Fatalf("node bind=%d: expected exactly 1 port (NumPorts=1), got %d: %+v — padding to 4 would reproduce the bench-reported phantom n/a ports", n.BindIndex, len(n.Ports), n.Ports)
		}
		p := n.Ports[0]
		if !p.Output || p.Input {
			t.Fatalf("node bind=%d port[0]: expected output=true input=false (PortTypes 0x80), got %+v", n.BindIndex, p)
		}
	}
}

func TestGetFixturesEmptyThenPopulated(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/fixtures", nil)
	var out []map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Fatalf("expected 0 fixtures initially, got %d", len(out))
	}

	node := h.seedNode(t)
	port, _ := artnet.NewPortAddress(0, 0, 0)
	ref := session.NodeRef{Key: node.Key, Addr: node.Addr, Port: port}
	uid := rdm.UID{ManufacturerID: 0x6C74, DeviceID: 42}
	h.srv.Registry.NoteFixture(ref, uid)

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/fixtures", nil)
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 fixture, got %d: %s", len(out), rr.Body.String())
	}
	if out[0]["manufacturerName"] != "LumenRadio" {
		t.Errorf("got manufacturer %v", out[0]["manufacturerName"])
	}
	if out[0]["uid"] != uid.String() {
		t.Errorf("got uid %v want %v", out[0]["uid"], uid.String())
	}
}

func TestPostAndGetSettings(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}

	newSettings := Settings{NIC: "eth0", PollIntervalMS: 5000, CaptureLimit: 5000, TimeoutProfiles: map[string]string{}}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/settings", newSettings)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d body=%s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	var got Settings
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.NIC != "eth0" || got.PollIntervalMS != 5000 {
		t.Errorf("got %+v", got)
	}
}

// TestDefaultSettings_UniverseBase covers Phase A's universe-numbering-base
// setting: a fresh server must default to 1 (industry-standard/Obsidian
// EN4/Vectorworks numbering), the more common expectation, per the task ask.
func TestDefaultSettings_UniverseBase(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}
	var got Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Default 0: show universe 1 = Art-Net universe 0. This reproduces the
	// correlation the previous default (universeBase 1) produced, so an
	// existing rig reads identically after the upgrade.
	if got.ArtnetStartUniverse != 0 {
		t.Errorf("default ArtnetStartUniverse = %d, want 0", got.ArtnetStartUniverse)
	}
}

// TestPostSettings_ArtnetStartUniverse covers the setting that replaced the
// old universeBase (0|1) notation switch.
//
// It is an Art-Net Port-Address, not a base: the value is the Art-Net
// universe that the show's OWN universe 1 lives on. 0 and 1 are the two
// common answers, and the reason it is a free number rather than a toggle is
// the third case — a show handed the block 100-139 numbers its universes
// 1-40, which no 0/1 choice can express.
func TestPostSettings_ArtnetStartUniverse(t *testing.T) {
	h := newHarness(t)

	for _, start := range []int{0, 1, 100, 32767} {
		newSettings := Settings{PollIntervalMS: 3000, CaptureLimit: 1000, TimeoutProfiles: map[string]string{}, ArtnetStartUniverse: start}
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", newSettings)
		if rr.Code != http.StatusOK {
			t.Fatalf("POST start=%d status = %d body=%s", start, rr.Code, rr.Body.String())
		}
		rr = doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
		var got Settings
		json.Unmarshal(rr.Body.Bytes(), &got)
		if got.ArtnetStartUniverse != start {
			t.Errorf("round-tripped ArtnetStartUniverse = %d, want %d", got.ArtnetStartUniverse, start)
		}
	}

	// Outside the Art-Net Port-Address range is refused rather than clamped:
	// silently accepting 32768 and storing something else would renumber a
	// rig without saying so.
	for _, bad := range []int{-1, 32768} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings",
			Settings{PollIntervalMS: 3000, CaptureLimit: 1000, TimeoutProfiles: map[string]string{}, ArtnetStartUniverse: bad})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("POST artnetStartUniverse=%d status = %d, want 400", bad, rr.Code)
		}
	}
}

// TestPostSettings_MigratesLegacyUniverseBase covers a settings payload
// written by a build that still had the universeBase switch.
//
//	universeBase 1 meant "wire universe 0 displays as 1" -> user 1 = Art-Net 0
//	universeBase 0 meant "wire universe 0 displays as 0" -> user 1 = Art-Net 1
//
// so start = 1 - universeBase. Getting this backwards would renumber every
// universe in an existing show by one on first launch, silently, which is
// the single worst outcome this change could have.
func TestPostSettings_MigratesLegacyUniverseBase(t *testing.T) {
	for _, c := range []struct {
		legacy    int
		wantStart int
	}{
		{1, 0},
		{0, 1},
	} {
		h := newHarness(t)
		body := fmt.Sprintf(`{"pollIntervalMs":3000,"captureLimit":1000,"timeoutProfiles":{},"universeBase":%d}`, c.legacy)
		rr := doRaw(t, h.srv.Handler(), "POST", "/api/settings", body)
		if rr.Code != http.StatusOK {
			t.Fatalf("POST legacy universeBase=%d status = %d body=%s", c.legacy, rr.Code, rr.Body.String())
		}
		rr = doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
		var got Settings
		json.Unmarshal(rr.Body.Bytes(), &got)
		if got.ArtnetStartUniverse != c.wantStart {
			t.Errorf("legacy universeBase=%d migrated to start %d, want %d — an off-by-one here "+
				"renumbers every universe in an existing show on first launch",
				c.legacy, got.ArtnetStartUniverse, c.wantStart)
		}
		// The legacy field must not be echoed back, or a later save would
		// re-apply it and shift the rig a second time.
		if bytes.Contains(rr.Body.Bytes(), []byte(`"universeBase"`)) {
			t.Errorf("GET /api/settings still returns universeBase; it must be consumed once "+
				"and dropped, or a later save re-applies it. body=%s", rr.Body.String())
		}
	}

	// An explicit new value always wins over a legacy one sent alongside it.
	h := newHarness(t)
	rr := doRaw(t, h.srv.Handler(), "POST", "/api/settings",
		`{"pollIntervalMs":3000,"captureLimit":1000,"timeoutProfiles":{},"universeBase":0,"artnetStartUniverse":100}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST both status = %d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	var got Settings
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ArtnetStartUniverse != 100 {
		t.Errorf("explicit artnetStartUniverse=100 was overridden by a legacy universeBase; got %d", got.ArtnetStartUniverse)
	}
}

func TestCaptureSnapshot(t *testing.T) {
	h := newHarness(t)
	h.srv.Capture.Add(capture.Entry{Kind: "ArtDmx", Universe: 3, Size: 20})
	h.srv.Capture.Add(capture.Entry{Kind: "ArtPoll", Universe: 0, Size: 14})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/capture/snapshot?kind=ArtDmx", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var entries []capture.Entry
	if err := json.Unmarshal(rr.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "ArtDmx" {
		t.Fatalf("got %+v", entries)
	}
}

func TestDiscoverUnknownNode404(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/discover", discoverRequest{Node: "10.0.0.99", BindIndex: 1, PortAddress: 0})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetParamUnknownFixture404(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("GET", "/api/fixture/6C74:0000002A/param/device_label", nil)
	rr := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetFixtureParamDMXPersonalityDescription is the regression test for
// this fix's server-side gap: devicedetail.js already called
// dmx_personality_description (see loadIndexedLabels' call site), but
// handleGetParam had no case for it, so the request always fell through to
// the switch's default "unknown param" 404 and every personality label in
// the UI silently stayed a bare number. Mirrors device_test.go's
// wireDeviceResponder/runHTTPAsync pattern for a real RDM round trip.
func TestGetFixtureParamDMXPersonalityDescription(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x454C, DeviceID: 1}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)

	descBytes := params.EncodePersonalityDescription(params.PersonalityDescription{
		Index: 5, DMXFootprint: 13, Description: "13ch Extended",
	})
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDDMXPersonalityDescription {
			return descBytes, false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	uidStr := uid.String()
	rr := h.runHTTPAsync(t, "GET", "/api/fixture/"+uidStr+"/param/dmx_personality_description?index=5", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Value params.PersonalityDescription `json:"value"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := params.PersonalityDescription{Index: 5, DMXFootprint: 13, Description: "13ch Extended"}
	if got.Value != want {
		t.Fatalf("got %+v, want %+v", got.Value, want)
	}

	// Missing ?index= must 400, not reach the device at all.
	rr2 := doJSON(t, h.srv.Handler(), "GET", "/api/fixture/"+uidStr+"/param/dmx_personality_description", nil)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("missing-index status = %d, want 400: %s", rr2.Code, rr2.Body.String())
	}
}

func TestIdentifyUnknownFixture404(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/identify", identifyRequest{UID: "6C74:0000002A", On: true})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDMXStartStopAndSend(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: 0, Channels: dmxFrame(map[int]byte{1: 255, 2: 128})})
	if rr.Code != http.StatusOK {
		t.Fatalf("dmx send status=%d body=%s", rr.Code, rr.Body.String())
	}
	frame, ok := h.srv.DMX.Frame(mustPortAddr(t, 0))
	if !ok {
		t.Fatal("expected universe to be started")
	}
	if frame[0] != 255 || frame[1] != 128 {
		t.Errorf("frame[0:2] = %v, %v", frame[0], frame[1])
	}

	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx/start", nil); rr.Code != http.StatusOK {
		t.Fatalf("dmx start status=%d", rr.Code)
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx/stop", nil); rr.Code != http.StatusOK {
		t.Fatalf("dmx stop status=%d", rr.Code)
	}
}

// TestDMXSendZeroActuallyZeroes is the direct regression test for the "send
// channels even at 0" bug: a channel that was previously lit and is now
// sent as 0 in a full-frame payload must actually land at 0, not be left at
// its old value because it was "unmentioned" the way the old map-shaped
// payload allowed.
func TestDMXSendZeroActuallyZeroes(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: 0, Channels: dmxFrame(map[int]byte{1: 200})})
	frame, ok := h.srv.DMX.Frame(mustPortAddr(t, 0))
	if !ok || frame[0] != 200 {
		t.Fatalf("seed: frame[0] = %v ok=%v, want 200/true", frame, ok)
	}

	// Drag channel 1 back down to 0: the client now sends the whole 512-slot
	// frame, channel 1 included as 0, exactly as a real fader-down would.
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: 0, Channels: dmxFrame(nil)})
	if rr.Code != http.StatusOK {
		t.Fatalf("dmx send status=%d body=%s", rr.Code, rr.Body.String())
	}
	frame, ok = h.srv.DMX.Frame(mustPortAddr(t, 0))
	if !ok || frame[0] != 0 {
		t.Fatalf("frame[0] = %v ok=%v, want 0/true (fader dragged to 0 must not stay lit)", frame, ok)
	}
}

func TestDMXRejectsShortFrame(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: 0, Channels: []byte{1, 2, 3}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400 for a non-512-byte frame", rr.Code, rr.Body.String())
	}
}

// dmxFrame builds a full 512-byte DMX frame (all other slots 0) with the
// given 1-based channel overrides, mirroring the always-send-everything
// shape send.js now builds on every commit.
func dmxFrame(overrides map[int]byte) []byte {
	frame := make([]byte, session.DMXUniverseSize)
	for ch, v := range overrides {
		frame[ch-1] = v
	}
	return frame
}

func mustPortAddr(t *testing.T, raw uint16) artnet.PortAddress {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(raw)
	if err != nil {
		t.Fatalf("PortAddressFromRaw: %v", err)
	}
	return pa
}
