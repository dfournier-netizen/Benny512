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

// TestRigCheckStartWithoutAProtocolFieldIsUnchanged is the backwards
// compatibility contract, asserted against a raw JSON body with no protocol
// key at all -- not against a struct with a zero value, which would not prove
// the same thing about a browser's request.
func TestRigCheckStartWithoutAProtocolFieldIsUnchanged(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", map[string]any{
		"scopeKind": "all", "mode": "all_channels", "level": 255,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	st := decodeState(t, rr.Body.Bytes())
	if st.Protocol != "artnet" {
		t.Fatalf("protocol = %q for a request with no protocol field, want \"artnet\"", st.Protocol)
	}
	if !st.Running || st.Mode != "all_channels" || st.Level != 255 {
		t.Fatalf("a protocol-less start behaved differently: %+v", st)
	}

	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("a protocol-less start put nothing on the Art-Net wire")
	}

	// And GET reports the same thing.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck", nil)
	if got := decodeState(t, rr.Body.Bytes()).Protocol; got != "artnet" {
		t.Fatalf("GET /api/patch/rigcheck reports protocol %q, want \"artnet\"", got)
	}
}

func TestRigCheckStartRejectsAnUnknownProtocol(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)

	for _, bad := range []string{"sACN", "e131", "ArtNet", "art-net", "udp"} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", map[string]any{
			"scopeKind": "all", "mode": "all_channels", "level": 255, "protocol": bad,
		})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("protocol %q: status=%d, want 400; body=%s", bad, rr.Code, rr.Body.String())
		}
		if got := errorText(t, rr.Body.Bytes()); !strings.Contains(got, bad) {
			t.Fatalf("protocol %q: error %q does not name the value that was rejected", bad, got)
		}
		if h.srv.RigCheck.State().Running {
			t.Fatalf("protocol %q was rejected but the rig check started anyway", bad)
		}
	}
}

// TestRigCheckRefusesAnUnmappableUniverseOverSACN is the API half of "refuse
// rather than clamp": the response must be a refusal that names the universe,
// not a run on a universe nobody asked for.
func TestRigCheckRefusesAnUnmappableUniverseOverSACN(t *testing.T) {
	for _, tc := range []struct {
		name          string
		artnetStart   int
		sacnStart     int
		entryUniverse uint16
		mustMention   []string
	}{
		{
			name: "show universe falls below 1", artnetStart: 100, sacnStart: 1, entryUniverse: 0,
			mustMention: []string{"Art-Net Port-Address 0", "show universe -99"},
		},
		{
			name: "sACN universe runs past 63999", artnetStart: 0, sacnStart: 63999, entryUniverse: 1,
			mustMention: []string{"Art-Net Port-Address 1", "sACN universe 64000"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.RigCheck.Stop)
			seedOneFixture(t, h, tc.entryUniverse)

			rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", Settings{
				PollIntervalMS: 1000, CaptureLimit: 100, ArtnetStartUniverse: tc.artnetStart,
			})
			if rr.Code != http.StatusOK {
				t.Fatalf("POST /api/settings: status=%d body=%s", rr.Code, rr.Body.String())
			}
			rr = doJSON(t, h.srv.Handler(), "POST", "/api/sacn", sacnConfigJSON{
				StartUniverse: tc.sacnStart, Priority: 100,
			})
			if rr.Code != http.StatusOK {
				t.Fatalf("POST /api/sacn: status=%d body=%s", rr.Code, rr.Body.String())
			}

			rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/start", map[string]any{
				"scopeKind": "all", "mode": "all_channels", "level": 255, "protocol": "sacn",
			})
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d, want 422; body=%s", rr.Code, rr.Body.String())
			}
			msg := errorText(t, rr.Body.Bytes())
			for _, want := range tc.mustMention {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q does not name %q", msg, want)
				}
			}
			if h.srv.RigCheck.State().Running {
				t.Fatal("the start was refused but the rig check is running")
			}
			if _, live := h.srv.RigCheck.LiveProtocolFor(tc.entryUniverse); live {
				t.Fatalf("universe %d was started anyway", tc.entryUniverse)
			}
		})
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
