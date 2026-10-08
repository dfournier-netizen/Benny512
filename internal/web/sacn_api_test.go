package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func decodeState(t *testing.T, body []byte) rigCheckStateJSON {
	t.Helper()
	var st rigCheckStateJSON
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal rig check state: %v\n%s", err, body)
	}
	return st
}

func errorText(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal error body: %v\n%s", err, body)
	}
	return m["error"]
}

// TestRigCheckStartWithoutAProtocolFieldStillWorks: a start with no protocol
// key behaves as before and, once the master output is armed, reaches the
// Art-Net wire (a universe with no protocol row is Art-Net).
func TestRigCheckStartWithoutAProtocolFieldStillWorks(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)
	h.srv.DMX.Arm("test")

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", map[string]any{
		"scopeKind": "all", "mode": "all_channels", "level": 255,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	st := decodeState(t, rr.Body.Bytes())
	if !st.Running || st.Mode != "all_channels" || st.Level != 255 {
		t.Fatalf("a protocol-less start behaved differently: %+v", st)
	}
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("a protocol-less start put nothing on the Art-Net wire")
	}
}

// TestRigCheckRefusesAProtocolField: since C3 Rig Check does not choose a
// wire protocol (each universe's routing is a Setting). A request that still
// names one — any value — is refused with a sentence that says where the
// choice went, never silently run on Art-Net, on both endpoints.
func TestRigCheckRefusesAProtocolField(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)
	for _, p := range []string{"sacn", "artnet"} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", map[string]any{
			"scopeKind": "all", "mode": "all_channels", "level": 255, "protocol": p,
		})
		if rr.Code != http.StatusBadRequest || !strings.Contains(errorText(t, rr.Body.Bytes()), "Settings") {
			t.Fatalf("rigcheck/start protocol %q: status=%d body=%s; want 400 pointing to Settings", p, rr.Code, rr.Body.String())
		}
		rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", map[string]any{"enabled": true, "protocol": p})
		if rr.Code != http.StatusBadRequest || !strings.Contains(errorText(t, rr.Body.Bytes()), "Settings") {
			t.Fatalf("pattern/output protocol %q: status=%d body=%s; want 400 pointing to Settings", p, rr.Code, rr.Body.String())
		}
	}
	if h.srv.RigCheck.State().Running {
		t.Fatal("a refused start left the rig check running")
	}
}

// --- GET/POST /api/sacn ---------------------------------------------------

func TestSACNConfigRoundTrip(t *testing.T) {
	h := newHarness(t)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/sacn", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got sacnConfigJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != (sacnConfigJSON{StartUniverse: 1, Priority: 100, UnicastTo: ""}) {
		t.Fatalf("defaults = %+v, want startUniverse 1, priority 100, no unicast", got)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/sacn", sacnConfigJSON{StartUniverse: 250, Priority: 200, UnicastTo: "10.2.3.4"})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != (sacnConfigJSON{StartUniverse: 250, Priority: 200, UnicastTo: "10.2.3.4"}) {
		t.Fatalf("POST echoed %+v", got)
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/sacn", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.StartUniverse != 250 || got.Priority != 200 || got.UnicastTo != "10.2.3.4" {
		t.Fatalf("GET after POST = %+v", got)
	}
}

// TestSACNConfigNeverExposesOrAcceptsTheCID: the CID is generated and owned
// server-side. It must not appear in a response, and a client must not be able
// to set it.
func TestSACNConfigNeverExposesOrAcceptsTheCID(t *testing.T) {
	h := newHarness(t)

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/sacn", nil)
	if strings.Contains(strings.ToLower(rr.Body.String()), "cid") {
		t.Fatalf("GET /api/sacn leaks the CID: %s", rr.Body.String())
	}

	before := h.srv.SACNSettings.Get().CID
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/sacn", map[string]any{
		"startUniverse": 5, "priority": 100, "unicastTo": "",
		"cid": "000102030405060708090a0b0c0d0e0f",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("a request carrying a cid got status=%d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if h.srv.SACNSettings.Get().CID != before {
		t.Fatal("a client changed the CID")
	}
	if h.srv.SACNSettings.Get().StartUniverse != 1 {
		t.Fatal("a rejected request still applied its other fields")
	}
}

func TestSACNConfigRejectsOutOfRangeValues(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		name string
		body sacnConfigJSON
		want string
	}{
		{"universe 0 does not exist", sacnConfigJSON{StartUniverse: 0, Priority: 100}, "1..63999"},
		{"universe past the top", sacnConfigJSON{StartUniverse: 64000, Priority: 100}, "1..63999"},
		{"priority past the standard's range", sacnConfigJSON{StartUniverse: 1, Priority: 201}, "0 to 200"},
		{"unicast is not an address", sacnConfigJSON{StartUniverse: 1, Priority: 100, UnicastTo: "hello"}, "not an IP address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doJSON(t, h.srv.Handler(), "POST", "/api/sacn", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if got := errorText(t, rr.Body.Bytes()); !strings.Contains(got, tc.want) {
				t.Fatalf("error %q does not explain the limit (%q)", got, tc.want)
			}
		})
	}
}
