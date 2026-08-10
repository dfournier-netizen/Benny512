package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
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
	clock := session.NewFakeClock(time.Time{})
	tport := session.NewFakeTransport()
	nodes := session.NewArtNetSession(session.ArtNetConfig{Transport: tport, Clock: clock})
	rdmc := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Transport: tport, Clock: clock})
	reg := registry.New(nodes, rdmc)
	ring := capture.New(100)
	srv := New(nodes, rdmc, dmx, reg, ring)
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

func TestIdentifyUnknownFixture404(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/identify", identifyRequest{UID: "6C74:0000002A", On: true})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDMXStartStopAndSend(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/dmx", dmxRequest{Universe: 0, Channels: map[string]byte{"1": 255, "2": 128}})
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

func mustPortAddr(t *testing.T, raw uint16) artnet.PortAddress {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(raw)
	if err != nil {
		t.Fatalf("PortAddressFromRaw: %v", err)
	}
	return pa
}
