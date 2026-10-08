package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
)

// TestConsoleToolsRawUniverseAndIdentify (Console-lite C7) runs
// static/js/testdata/console_c7_test.js: the real index.html in a small DOM
// and the LITERAL api.js, ws.js, programmer.js, ui.js, universeidentify.js,
// console-tests.js, console-tools.js and console.js against this real server
// over real HTTP and WebSocket. The Tools panel replaces the Send screen, so
// every Send function is driven through the new UI — raw faders, number
// boxes, park, paging and jump, All off, Release all, a universe change,
// Universe Identify on and off — and after each step the script asks this
// test what actually reached the FakeTransport (GET /__wire below: the fake
// clock is advanced through the engine's real ticks and the ArtDmx datagrams
// the engine sent are decoded and reported per universe).
func TestConsoleToolsRawUniverseAndIdentify(t *testing.T) {
	nodePath := nodeOrSkip(t)
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	entries := []any{
		map[string]any{"name": "G1", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 0, "startAddress": 1},
		map[string]any{"name": "G2", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 0, "startAddress": 5},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	if err := json.Unmarshal(doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range resp.Patch.Entries {
		ids[e.Name] = e.ID
	}
	idsJSON, _ := json.Marshal(ids)

	var wireMu sync.Mutex
	mux := http.NewServeMux()
	mux.Handle("/", h.srv.Handler())
	// GET /__wire?ms=N (test only): keep the Arm lease alive, advance the
	// fake clock N ms in engine ticks, and report every ArtDmx datagram the
	// engine handed the FakeTransport in that time, per wire universe: how
	// many, and the last one's 512 slots.
	mux.HandleFunc("GET /__wire", func(w http.ResponseWriter, r *http.Request) {
		wireMu.Lock()
		defer wireMu.Unlock()
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		if ms <= 0 {
			ms = 100
		}
		h.srv.DMX.Heartbeat("c7-test")
		h.tport.TakeSent()
		for i := 0; i < ms/25; i++ {
			h.clock.Advance(25 * time.Millisecond)
		}
		type uni struct {
			Frames int   `json:"frames"`
			Last   []int `json:"last"`
		}
		out := map[string]*uni{}
		for _, sp := range h.tport.TakeSent() {
			if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindDmx {
				continue
			}
			raw := int(sp.Packet.Dmx.Net&0x7F)<<8 | int(sp.Packet.Dmx.SubUni)
			k := strconv.Itoa(raw)
			u := out[k]
			if u == nil {
				u = &uni{}
				out[k] = u
			}
			u.Frames++
			u.Last = make([]int, len(sp.Packet.Dmx.Data))
			for i, b := range sp.Packet.Dmx.Data {
				u.Last[i] = int(b)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /__arm", func(w http.ResponseWriter, r *http.Request) {
		h.srv.DMX.Arm("c7-test")
		writeJSON(w, http.StatusOK, map[string]string{"state": "armed"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_c7_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_c7_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_c7_test.js failed: %v", err)
	}
}

// TestC7RetiredEndpointsAreGone: the routes nothing calls after C7 are not
// served any more (the old Rig Check view's /api/patch/rigcheck/* and Send's
// 410 stub /api/dmx/start), and the routes the Console's Tools panel, the
// Tests panel and Show tools still use are.
func TestC7RetiredEndpointsAreGone(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	for _, rt := range []struct{ method, path string }{
		{"POST", "/api/dmx/start"},
		{"GET", "/api/patch/rigcheck"},
		{"POST", "/api/patch/rigcheck/start"},
		{"POST", "/api/patch/rigcheck/stop"},
		{"POST", "/api/patch/rigcheck/blackout"},
		{"POST", "/api/patch/rigcheck/next"},
		{"POST", "/api/patch/rigcheck/previous"},
		{"POST", "/api/patch/rigcheck/jump"},
		{"POST", "/api/patch/rigcheck/mode"},
		{"POST", "/api/patch/rigcheck/level"},
		{"POST", "/api/patch/rigcheck/channel"},
		{"GET", "/api/patch/rigcheck/pattern"},
		{"POST", "/api/patch/rigcheck/pattern/start"},
		{"POST", "/api/patch/rigcheck/pattern/adjust"},
		{"POST", "/api/patch/rigcheck/pattern/tests"},
		{"POST", "/api/patch/rigcheck/pattern/select"},
		{"POST", "/api/patch/rigcheck/pattern/scope"},
		{"POST", "/api/patch/rigcheck/pattern/isolate"},
		{"POST", "/api/patch/rigcheck/pattern/fade"},
		{"POST", "/api/patch/rigcheck/pattern/output"},
	} {
		rr := doJSON(t, h.srv.Handler(), rt.method, rt.path, map[string]any{})
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d (%s); want it gone (404/405)", rt.method, rt.path, rr.Code, strings.TrimSpace(rr.Body.String()))
		}
	}
	frame := make([]byte, 512)
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/dmx", map[string]any{"universe": 3, "channels": frame}},
		{"POST", "/api/dmx/stop", nil},
		{"GET", "/api/dmx/identify", nil},
		{"POST", "/api/dmx/identify/stop", nil},
		{"GET", "/api/tests", nil},
		{"POST", "/api/tests/clear", map[string]any{}},
	} {
		if rr := doJSON(t, h.srv.Handler(), rt.method, rt.path, rt.body); rr.Code != http.StatusOK {
			t.Errorf("%s %s answered %d (%s); the Console still uses it", rt.method, rt.path, rr.Code, strings.TrimSpace(rr.Body.String()))
		}
	}
}

// TestTestsAvailableEnumeratedAndSelectable (ported in C7 from the retired
// TestRigCheckPatternEndpoints_AvailableTestsEnumerated): for a fixture with
// TWO frost functions and a gobo wheel, the Tests view lists one frost test
// per GDTF Frost* function and one gobo step and one gobo rotate test, each
// honest about whether its label came from GDTF — and every listed test can
// be turned on by its own (kind, target).
func TestTestsAvailableEnumeratedAndSelectable(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	pa := mustPort(t)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", goboFrostEntryRequest("Spot 1", pa.RawValue(), 1)); rr.Code != http.StatusOK {
		t.Fatalf("create entry: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/tests?kind=all", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/tests: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"id":"frost:Frost1"`, `"label":"Light Frost"`, `"attribute":"Frost1"`, `"labelFromGdtf":true`,
		`"id":"frost:Frost2"`, `"label":"Frost2"`,
		`"id":"gobo_step:Gobo1"`, `"label":"Gobo Wheel 1"`,
		`"id":"gobo_rotate:Gobo1"`, `"label":"Gobo 1 Rotate"`, `"attribute":"Gobo1WheelSpin"`,
		`"fixtureCount":1`, `"target":"Frost1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tests view missing %s;\ngot %s", want, body)
		}
	}
	var v struct {
		Available []availableTestJSON `json:"available"`
		Tests     []struct {
			ID string `json:"id"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Available {
		if a.ID == "frost:Frost2" && a.LabelFromGDTF {
			t.Errorf("Frost2 has no GDTF ChannelFunction Name, so labelFromGdtf must be false; got %+v", a)
		}
	}
	tests := make([]map[string]any, 0, len(v.Available))
	for _, a := range v.Available {
		tests = append(tests, map[string]any{"kind": a.Kind, "target": a.Target, "max": 255})
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/tests/set", map[string]any{"tests": tests, "scope": map[string]any{"kind": "all"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("turning on every listed test: %d %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Tests) != len(tests) || len(tests) < 4 {
		t.Errorf("%d tests running after turning on all %d listed; want every one", len(v.Tests), len(tests))
	}
}

// TestTestsPhaseAndWaveform (ported in C7 from the retired
// TestRigCheckPattern_PhaseAndWaveformOverREST): a waveform and a phase
// spread reach the engine through the Tests API, and a phase spread on a
// static kind is refused (400), not silently ignored.
func TestTestsPhaseAndWaveform(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	pa := mustPort(t)
	for i := 0; i < 4; i++ {
		doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC", pa.RawValue(), uint16(1+i*3)))
	}
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/tests/set", map[string]any{
		"tests": []map[string]any{{"kind": "dimmer_sine", "rateHz": 1, "max": 255, "waveform": "snap", "offsetMin": 0, "offsetMax": 360}},
		"scope": map[string]any{"kind": "all"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"waveform":"snap"`, `"offsetMax":360`, `"phaseDegrees":90`, `"phaseDegrees":270`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/tests/set", map[string]any{
		"tests": []map[string]any{{"kind": "move_extreme", "target": "tilt_max", "offsetMax": 180}},
		"scope": map[string]any{"kind": "all"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("phase on a static kind: status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
}
