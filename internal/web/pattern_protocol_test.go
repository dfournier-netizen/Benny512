package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// This file is the real-socket proof for the FUNCTION CHECK half of
// selectable output: POST /api/patch/rigcheck/pattern/output must be able to
// say which protocol the pattern goes out on, and the status every client
// polls must say which one is actually being transmitted.
//
// It reuses sacn_isolation_test.go's harness deliberately -- the hand-written
// Table B-13 offset parser (parseE131), the loopback UDP listener
// (sacnLoopback), the ArtDmx counter (countArtDmx). Nothing here decodes an
// E1.31 packet with the encoder that produced it, and nothing here asserts a
// JSON field against the struct the server used to build it: the protocol
// claims below are settled by watching two real wires.

// selectOneTest puts a scope and a single selected test on the engine WITHOUT
// letting output flow. It is the "pick your tests first" half of the owner's
// workflow, and it means every assertion after it is about the output call
// alone.
func selectOneTest(t *testing.T, h *testHarness) {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/tests", map[string]any{
		"scopeKind": "all",
		"tests":     []map[string]any{{"kind": "dimmer_sine", "rateHz": 1, "max": 255}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("pattern/tests: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// patternOutput posts the start/stop button's request verbatim -- a raw map,
// so a test can send a body with NO protocol key at all (which is the
// backwards-compatibility case) as distinct from one carrying an empty
// string.
func patternOutput(t *testing.T, h *testHarness, body map[string]any) (int, string) {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/output", body)
	return rr.Code, rr.Body.String()
}

// patternProtocolField reads the `protocol` key out of a pattern status body
// as raw JSON -- not by unmarshalling into patternStatusJSON, which is the
// struct the handler itself built the response from. present is false when
// the key is absent entirely.
func patternProtocolField(t *testing.T, body string) (value string, present bool) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("pattern status is not a JSON object: %v\n%s", err, body)
	}
	raw, ok := m["protocol"]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("pattern status `protocol` is not a string: %v", err)
	}
	return s, true
}

func getPatternStatus(t *testing.T, h *testHarness) string {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/rigcheck/pattern", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET pattern: status=%d body=%s", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// TestPatternOutputProtocolChangesTheWire is the one that matters: a pattern
// armed on Art-Net, asked for sACN by the output call, must actually leave
// the Art-Net wire and appear on the E1.31 one. A `protocol` field that only
// round-trips through the JSON would pass a struct comparison and fail here.
func TestPatternOutputProtocolChangesTheWire(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	ln := sacnLoopback(t, h, 120)
	seedOneFixture(t, h, 0) // Art-Net Port-Address 0 -> show universe 1 -> sACN universe 1

	// Arm Art-Net the only way there is today: a Channel-check Start. This is
	// exactly the situation the Function check tab inherits.
	startRigCheck(t, h, "artnet")
	selectOneTest(t, h)

	if code, body := patternOutput(t, h, map[string]any{"enabled": true}); code != http.StatusOK {
		t.Fatalf("pattern output on the armed protocol: status=%d body=%s", code, body)
	}
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("the pattern is not on the Art-Net wire to begin with; the rest of this test would prove nothing")
	}
	if pkts := drainE131(t, ln, 50*time.Millisecond); len(pkts) != 0 {
		t.Fatalf("%d E1.31 datagrams arrived while the pattern was on Art-Net", len(pkts))
	}

	// --- the whole point: ask the OUTPUT call for sACN --------------------
	code, body := patternOutput(t, h, map[string]any{"enabled": true, "protocol": "sacn"})
	if code != http.StatusOK {
		t.Fatalf("pattern output with protocol \"sacn\": status=%d body=%s", code, body)
	}
	if got, ok := patternProtocolField(t, body); !ok || got != "sacn" {
		t.Fatalf("pattern status protocol = %q present=%v, want \"sacn\"; the tab has no other way to know what is on the wire", got, ok)
	}

	h.tport.TakeSent() // the switch itself blacks Art-Net out: one last legitimate frame
	drainE131(t, ln, 50*time.Millisecond)

	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n != 0 {
		t.Fatalf("universe 0 is STILL on the Art-Net wire after the pattern was asked for sACN: %d ArtDmx frames in 250ms", n)
	}
	live := drainE131(t, ln, 200*time.Millisecond)
	if len(live) == 0 {
		t.Fatal("no E1.31 datagrams arrived after the pattern was asked for sACN")
	}
	for i, p := range live {
		if p.universe != 1 {
			t.Fatalf("packet %d is for sACN universe %d, want 1", i, p.universe)
		}
		if p.priority != 120 {
			t.Fatalf("packet %d has priority %d, want the configured 120", i, p.priority)
		}
		if p.startCode != 0x00 {
			t.Fatalf("packet %d has START Code 0x%02x, want 0x00", i, p.startCode)
		}
		if p.terminated {
			t.Fatalf("packet %d carries Stream_Terminated while the pattern is live", i)
		}
	}

	// --- and back again, which must terminate the stream properly ---------
	if code, body := patternOutput(t, h, map[string]any{"enabled": true, "protocol": "artnet"}); code != http.StatusOK {
		t.Fatalf("pattern output back on artnet: status=%d body=%s", code, body)
	}
	tail := drainE131(t, ln, 200*time.Millisecond)
	terminated, zeroBefore := 0, 0
	for _, p := range tail {
		if p.terminated {
			terminated++
			continue
		}
		if terminated == 0 && p.allZero {
			zeroBefore++
		}
	}
	if terminated != 3 {
		t.Fatalf("switching the pattern away from sACN sent %d Stream_Terminated packets, want 3 (ANSI E1.31-2025 6.2.6); the tail was %d packets", terminated, len(tail))
	}
	if zeroBefore < 3 {
		t.Fatalf("only %d all-zero frames preceded the termination, want at least 3", zeroBefore)
	}
	h.tport.TakeSent()
	h.clock.Advance(250 * time.Millisecond)
	if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
		t.Fatal("Art-Net did not resume for the pattern after switching back")
	}
}

// TestPatternOutputWithoutProtocolKeepsTheArmedProtocol is the
// backwards-compatibility half. A body with NO protocol key must behave
// exactly as it did before this field existed: whatever the last Start armed
// is what the pattern goes out on -- and the status must say so.
func TestPatternOutputWithoutProtocolKeepsTheArmedProtocol(t *testing.T) {
	t.Run("armed on sACN", func(t *testing.T) {
		h := newHarness(t)
		t.Cleanup(h.srv.RigCheck.Stop)
		ln := sacnLoopback(t, h, 100)
		seedOneFixture(t, h, 0)
		startRigCheck(t, h, "sacn")
		selectOneTest(t, h)
		drainE131(t, ln, 50*time.Millisecond)

		code, body := patternOutput(t, h, map[string]any{"enabled": true})
		if code != http.StatusOK {
			t.Fatalf("pattern output with no protocol key: status=%d body=%s", code, body)
		}
		if got, ok := patternProtocolField(t, body); !ok || got != "sacn" {
			t.Fatalf("pattern status protocol = %q present=%v, want \"sacn\" -- an absent field must keep the armed protocol AND still be echoed", got, ok)
		}
		h.tport.TakeSent()
		h.clock.Advance(250 * time.Millisecond)
		if n := countArtDmx(t, h.tport.TakeSent(), 0); n != 0 {
			t.Fatalf("an absent protocol field put %d ArtDmx frames on the wire while sACN was armed", n)
		}
		if pkts := drainE131(t, ln, 200*time.Millisecond); len(pkts) == 0 {
			t.Fatal("an absent protocol field took the pattern off sACN; absent must mean \"keep what is armed\"")
		}
	})

	t.Run("armed on Art-Net", func(t *testing.T) {
		h := newHarness(t)
		t.Cleanup(h.srv.RigCheck.Stop)
		ln := sacnLoopback(t, h, 100)
		seedOneFixture(t, h, 0)
		startRigCheck(t, h, "artnet")
		selectOneTest(t, h)

		code, body := patternOutput(t, h, map[string]any{"enabled": true})
		if code != http.StatusOK {
			t.Fatalf("pattern output with no protocol key: status=%d body=%s", code, body)
		}
		if got, ok := patternProtocolField(t, body); !ok || got != "artnet" {
			t.Fatalf("pattern status protocol = %q present=%v, want \"artnet\"", got, ok)
		}
		h.tport.TakeSent()
		h.clock.Advance(250 * time.Millisecond)
		if n := countArtDmx(t, h.tport.TakeSent(), 0); n == 0 {
			t.Fatal("an absent protocol field took the pattern off Art-Net; this is the byte-for-byte pre-sACN behaviour")
		}
		if pkts := drainE131(t, ln, 50*time.Millisecond); len(pkts) != 0 {
			t.Fatalf("%d E1.31 datagrams arrived for an Art-Net pattern", len(pkts))
		}
	})
}

// TestPatternStatusAlwaysCarriesTheProtocol: GET .../rigcheck/pattern is also
// the 5s watchdog heartbeat, so it is the call the tab makes continuously
// while output flows. The key must be there every time, with no omitempty --
// "artnet" is real data, not an absent value.
func TestPatternStatusAlwaysCarriesTheProtocol(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)

	got, ok := patternProtocolField(t, getPatternStatus(t, h))
	if !ok {
		t.Fatal("GET /api/patch/rigcheck/pattern has no `protocol` key at all -- the Function check tab has nothing to paint the control from")
	}
	if got != "artnet" {
		t.Fatalf("a freshly-started server reports protocol %q, want \"artnet\"", got)
	}
}

// TestPatternOutputUnknownProtocolIsRefused: an unknown value is a 400 in the
// server's own words, never a silent fall back to Art-Net. The message is
// asserted, not just the status code -- DisallowUnknownFields answers 400 for
// an unrecognised KEY too, and that is precisely the unfixed state this test
// has to be able to tell apart.
func TestPatternOutputUnknownProtocolIsRefused(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	seedOneFixture(t, h, 0)
	selectOneTest(t, h)

	code, body := patternOutput(t, h, map[string]any{"enabled": true, "protocol": "sACN"})
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", code, body)
	}
	if !strings.Contains(body, "protocol must be") {
		t.Fatalf("body=%s; want the server's own \"protocol must be ...\" refusal, so a mis-cased value is never read as an unknown field or defaulted", body)
	}
	if st, _ := patternProtocolField(t, getPatternStatus(t, h)); st != "artnet" {
		t.Fatalf("a refused protocol left the server armed on %q", st)
	}
}

// TestPatternOutputUnmappableScopeIs422WithTheServersMessage: the scope is
// fine, the request is fine, and the combination cannot be expressed on
// sACN. 422, and the message NAMES the universe -- that name is the only
// part that tells a tech what to change, so it must survive to the body.
func TestPatternOutputUnmappableScopeIs422WithTheServersMessage(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.RigCheck.Stop)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", Settings{
		PollIntervalMS: 3000, CaptureLimit: 1000, TimeoutProfiles: map[string]string{}, ArtnetStartUniverse: 100,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/settings: status=%d body=%s", rr.Code, rr.Body.String())
	}
	sacnLoopback(t, h, 100) // sACN start universe 1
	seedOneFixture(t, h, 0) // show universe -99 with an Art-Net start of 100
	selectOneTest(t, h)

	code, body := patternOutput(t, h, map[string]any{"enabled": true, "protocol": "sacn"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422; body=%s", code, body)
	}
	for _, want := range []string{
		"sACN universe must be 1..63999",
		"show universe -99 (Art-Net Port-Address 0)",
		"an sACN start universe of 1 and an Art-Net start universe of 100",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the 422 body does not contain %q; it was %s", want, body)
		}
	}

	// Refuse rather than clamp, and leave the rig exactly as it was.
	status := getPatternStatus(t, h)
	var m map[string]any
	if err := json.Unmarshal([]byte(status), &m); err != nil {
		t.Fatal(err)
	}
	if m["outputEnabled"] == true {
		t.Fatalf("the refused start let output flow anyway: %s", status)
	}
	if got, _ := patternProtocolField(t, status); got != "artnet" {
		t.Fatalf("the refused start left the server armed on %q, want the untouched \"artnet\"", got)
	}
}
