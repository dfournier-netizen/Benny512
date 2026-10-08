package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

const identifyURL = "/api/dmx/identify"

func identifyCall(t *testing.T, h *testHarness, action string, body any, want int) []byte {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", identifyURL+"/"+action, body)
	if rr.Code != want {
		t.Fatalf("identify %s: status %d, want %d: %s", action, rr.Code, want, rr.Body.String())
	}
	return rr.Body.Bytes()
}

func armIdentify(t *testing.T, h *testHarness, protocol string, from, to int) map[string]string {
	t.Helper()
	b := identifyCall(t, h, "arm", map[string]any{"protocol": protocol, "from": from, "to": to}, 200)
	var st struct {
		identifyStatus
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &st); err != nil || !st.Armed || st.Running || st.Token == "" {
		t.Fatalf("arm did not return an armed, idle lease: %s (%v)", b, err)
	}
	return map[string]string{"token": st.Token}
}

func identifySnapshot(t *testing.T, h *testHarness) identifyStatus {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", identifyURL, nil)
	var st identifyStatus
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &st) != nil || bytes.Contains(rr.Body.Bytes(), []byte(`"token"`)) {
		t.Fatalf("invalid public status: %s", rr.Body.String())
	}
	return st
}

func TestUniverseIdentifyRejectsImplicitOrInvalidScope(t *testing.T) {
	h := newHarness(t)
	seedOneFixture(t, h, 400)
	for _, body := range []any{
		nil, map[string]any{},
		map[string]any{"protocol": "artnet"},
		map[string]any{"protocol": "artnet", "from": nil, "to": 2},
		map[string]any{"protocol": "artnet", "to": 2},
		map[string]any{"protocol": "artnet", "from": 1},
		map[string]any{"from": 1, "to": 2},
		map[string]any{"protocol": "Art-Net", "from": 1, "to": 2},
		map[string]any{"protocol": "sacn", "from": 0, "to": 2},
		map[string]any{"protocol": "artnet", "from": -1, "to": 2},
		map[string]any{"protocol": "artnet", "from": 3, "to": 2},
		map[string]any{"protocol": "artnet", "from": 512, "to": 513},
		map[string]any{"protocol": "sacn", "from": 513, "to": 513},
		map[string]any{"protocol": "artnet", "from": 1.5, "to": 2},
		map[string]any{"protocol": "artnet", "from": "", "to": 2},
		map[string]any{"protocol": "artnet", "from": 1, "to": 2, "scopeKind": "all"},
	} {
		identifyCall(t, h, "arm", body, 400)
		if st := identifySnapshot(t, h); st.Armed || st.Running {
			t.Fatalf("invalid request armed output: %+v, body %+v", st, body)
		}
	}
	identifyCall(t, h, "start", map[string]string{"token": ""}, 409)
	h.clock.Advance(time.Second)
	if h.tport.SentCount() != 0 {
		t.Fatal("validation sent packets to the seeded patch")
	}
}

// Inspect literal wire offsets, independent of the production frame builder.
func checkIdentifyArtNet(t *testing.T, packets []session.SentPacket, from, to int, zero bool) {
	t.Helper()
	seen := map[int]int{}
	for _, sp := range packets {
		b := sp.Data
		if len(b) != 530 || !bytes.Equal(b[:8], []byte("Art-Net\x00")) || b[8] != 0 || b[9] != 0x50 {
			t.Fatalf("unexpected packet: %x", b)
		}
		u := int(b[15])<<8 | int(b[14])
		if u < from || u > to {
			t.Fatalf("Identify emitted universe %d outside explicit range %d..%d", u, from, to)
		}
		seen[u]++
		for ch, v := range b[18:] {
			want := byte(0)
			if !zero && (u == 0 || ch+1 == u) {
				want = 255
			}
			if v != want {
				t.Fatalf("universe %d channel %d = %d, want %d", u, ch+1, v, want)
			}
		}
	}
	if len(seen) != to-from+1 {
		t.Fatalf("sent %d universes, want %d: %v", len(seen), to-from+1, seen)
	}
}

// inRange keeps only the ArtDmx datagrams whose Port-Address is in from..to.
func inRange(packets []session.SentPacket, from, to int) []session.SentPacket {
	var out []session.SentPacket
	for _, sp := range packets {
		if len(sp.Data) < 16 {
			continue
		}
		if u := int(sp.Data[15])<<8 | int(sp.Data[14]); u >= from && u <= to {
			out = append(out, sp)
		}
	}
	return out
}

// TestUniverseIdentifyArtNetRangeIsolationAndZero: Identify drives exactly
// its range, exclusively, while Send's frames carry on elsewhere; when it
// ends, a universe nobody else drives gets zero frames and one Send drives
// falls straight back to Send's frame (C3: Identify is a source of the one
// output engine, not a separate engine).
func TestUniverseIdentifyArtNetRangeIsolationAndZero(t *testing.T) {
	for _, bounds := range [][2]int{{0, 2}, {511, 512}} {
		t.Run(fmt.Sprintf("%d-%d", bounds[0], bounds[1]), func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			seedOneFixture(t, h, 900)
			h.srv.DMX.Arm("test")
			// Manual frames on an overlapping universe (1 or 511) and an
			// unrelated one (800).
			overlap := uint16(bounds[0] + 1)
			for _, raw := range []uint16{overlap, 800} {
				if err := h.srv.DMX.SetFrame(session.SourceRaw, raw, bytes.Repeat([]byte{73}, 512)); err != nil {
					t.Fatal(err)
				}
			}
			lease := armIdentify(t, h, "artnet", bounds[0], bounds[1])
			h.clock.Advance(100 * time.Millisecond)
			for _, sp := range inRange(h.tport.TakeSent(), bounds[0], bounds[1]) {
				if u := int(sp.Data[15])<<8 | int(sp.Data[14]); u != int(overlap) {
					t.Fatalf("setting the range transmitted universe %d before Identify on", u)
				}
			}
			identifyCall(t, h, "start", lease, 200)
			h.clock.Advance(100 * time.Millisecond)
			sent := h.tport.TakeSent()
			checkIdentifyArtNet(t, inRange(sent, bounds[0], bounds[1]), bounds[0], bounds[1], false)
			if n := len(inRange(sent, 800, 800)); n == 0 {
				t.Fatal("Identify stopped the unrelated manual universe")
			}
			identifyCall(t, h, "stop", nil, 200)
			stop := inRange(h.tport.TakeSent(), bounds[0], bounds[1])
			var others []session.SentPacket
			back := 0
			for _, sp := range stop {
				if u := int(sp.Data[15])<<8 | int(sp.Data[14]); u == int(overlap) {
					if sp.Data[18] != 73 {
						t.Fatalf("universe %d did not fall back to its manual frame when Identify ended", u)
					}
					back++
				} else {
					others = append(others, sp)
				}
			}
			if back != 1 {
				t.Fatalf("overlapping universe: %d frames at Identify off, want its manual frame once", back)
			}
			if len(others) != 3*(bounds[1]-bounds[0]) {
				t.Fatalf("Identify off: %d zero frames for the universes nobody else drives, want 3 each", len(others))
			}
			for _, sp := range others {
				for _, v := range sp.Data[18:] {
					if v != 0 {
						t.Fatal("Identify off sent a non-zero frame on a universe nobody else drives")
					}
				}
			}
			h.clock.Advance(time.Second)
			for _, sp := range inRange(h.tport.TakeSent(), bounds[0], bounds[1]) {
				if u := int(sp.Data[15])<<8 | int(sp.Data[14]); u != int(overlap) {
					t.Fatalf("universe %d still transmitted after Identify off", u)
				}
			}
			for _, raw := range []uint16{overlap, 800} {
				pa, _ := artnet.PortAddressFromRaw(raw)
				f, ok := h.srv.DMX.Frame(pa)
				if !ok || !bytes.Equal(f, bytes.Repeat([]byte{73}, 512)) {
					t.Fatalf("Identify altered manual universe %d", raw)
				}
			}
		})
	}
}

func TestUniverseIdentifySACNRawNumbersCadenceAndTermination(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	ln := sacnLoopback(t, h, 123)
	// Neither numbering setting may affect this tool's wire universe or slot.
	h.srv.settings.ArtnetStartUniverse = 100
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/sacn", sacnConfigJSON{StartUniverse: 200, Priority: 123, UnicastTo: "127.0.0.1"}); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	seedOneFixture(t, h, 999)
	h.srv.DMX.Arm("test")
	for _, bounds := range [][2]int{{1, 3}, {511, 512}} {
		lease := armIdentify(t, h, "sacn", bounds[0], bounds[1])
		if p := drainE131(t, ln, time.Millisecond); len(p) != 0 {
			t.Fatal("sACN Arm emitted packets")
		}
		identifyCall(t, h, "start", lease, 200)
		h.clock.Advance(100 * time.Millisecond)
		live := drainE131(t, ln, 10*time.Millisecond)
		seen := map[int]int{}
		for _, p := range live {
			u := int(p.universe)
			if u < bounds[0] || u > bounds[1] || p.priority != 123 || p.terminated || p.startCode != 0 {
				t.Fatalf("wrong sACN target/header: %+v", p)
			}
			seen[u]++
			for ch, v := range p.slots {
				want := byte(0)
				if ch+1 == u {
					want = 255
				}
				if v != want {
					t.Fatalf("sACN %d channel %d = %d, want %d", u, ch+1, v, want)
				}
			}
		}
		for u := bounds[0]; u <= bounds[1]; u++ {
			if seen[u] != 3 {
				t.Fatalf("sACN %d: initial burst %d, want 3", u, seen[u])
			}
		}
		h.clock.Advance(800 * time.Millisecond)
		if pkts := drainE131(t, ln, 10*time.Millisecond); len(pkts) != bounds[1]-bounds[0]+1 {
			t.Fatalf("keepalive packet count = %d", len(pkts))
		}
		identifyCall(t, h, "stop", nil, 200)
		zero, term := map[int]int{}, map[int]int{}
		for _, p := range drainE131(t, ln, 10*time.Millisecond) {
			u := int(p.universe)
			if u < bounds[0] || u > bounds[1] || !p.allZero {
				t.Fatalf("stop escaped range or sent nonzero slots: %+v", p)
			}
			if p.terminated {
				if zero[u] != 3 {
					t.Fatalf("termination preceded blackout on %d", u)
				}
				term[u]++
			} else {
				zero[u]++
			}
		}
		for u := bounds[0]; u <= bounds[1]; u++ {
			if zero[u] != 3 || term[u] != 3 {
				t.Fatalf("sACN %d stop = %d zeros, %d terminations", u, zero[u], term[u])
			}
		}
		h.clock.Advance(time.Second)
		// Since C4a the armed engine's base source keeps the seeded
		// fixture's universe (999) on Art-Net at its defaults; that is the
		// only Art-Net allowed here. Identify on sACN must add none.
		base, _ := artnet.PortAddressFromRaw(999)
		for _, sp := range h.tport.TakeSent() {
			if sp.Packet.Kind != artnet.KindDmx || sp.Packet.Dmx.Net != base.Net || sp.Packet.Dmx.SubUni != base.SubUni() {
				t.Fatal("sACN selection emitted Art-Net beyond the patched universe's base state")
			}
		}
		if len(drainE131(t, ln, time.Millisecond)) != 0 {
			t.Fatal("disarm left sACN transmitting")
		}
	}
}

// TestUniverseIdentifyOwnershipAndStaleRequests: since C3 other output is
// not refused while Identify is set (the engine composes them); a second
// range still needs this one turned off first, and a stale token never
// starts or extends a newer range.
func TestUniverseIdentifyOwnershipAndStaleRequests(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	seedOneFixture(t, h, 400)
	h.srv.DMX.Arm("test")
	lease := armIdentify(t, h, "artnet", 4, 5)
	for _, path := range []string{"/api/dmx", "/api/tests/set", "/api/tests/clear"} {
		if rr := doJSON(t, h.srv.Handler(), "POST", path, map[string]any{}); rr.Code == 409 {
			t.Fatalf("Identify still refuses %s with 409: %s", path, rr.Body.String())
		}
	}
	identifyCall(t, h, "arm", map[string]any{"protocol": "sacn", "from": 9, "to": 10}, 409)
	identifyCall(t, h, "start", lease, 200)
	identifyCall(t, h, "stop", nil, 200)
	newLease := armIdentify(t, h, "artnet", 10, 10)
	identifyCall(t, h, "start", lease, 409)
	identifyCall(t, h, "heartbeat", lease, 409)
	h.tport.TakeSent()
	identifyCall(t, h, "start", newLease, 200)
	checkIdentifyArtNet(t, inRange(h.tport.TakeSent(), 10, 10), 10, 10, false)
}

// TestUniverseIdentifyCoexistsWithOtherOutput: arming Identify while the
// tests layer or the raw universe levels run is no longer refused (C3).
func TestUniverseIdentifyCoexistsWithOtherOutput(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	h.srv.DMX.Arm("test")
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", 0, 1)); rr.Code != http.StatusOK {
		t.Fatalf("create entry: %d %s", rr.Code, rr.Body.String())
	}
	startTests(t, h, "dimmer_sine")
	if err := h.srv.DMX.SetFrame(session.SourceRaw, 10, []byte{1}); err != nil {
		t.Fatal(err)
	}
	armIdentify(t, h, "artnet", 1, 2)
}

// TestUniverseIdentifyLeaseStopPathsAndFailures: Identify's own lease, a
// show change and server Close end it. Stop all output (Disarm) silences the
// wire but leaves Identify set, like every source; releasing Send's frames
// and a transmit error do not touch it.
func TestUniverseIdentifyLeaseStopPathsAndFailures(t *testing.T) {
	for _, action := range []string{"lease", "show-change", "close", "dmx-stop", "all-stop", "send-error"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			h.srv.DMX.Arm("test")
			lease := armIdentify(t, h, "artnet", 1, 2)
			identifyCall(t, h, "start", lease, 200)
			h.tport.TakeSent()
			ends := true
			switch action {
			case "lease":
				h.clock.Advance(4 * time.Second)
				identifyCall(t, h, "heartbeat", lease, 200)
				h.srv.DMX.Heartbeat("test")
				h.clock.Advance(4 * time.Second)
				h.srv.DMX.Heartbeat("test")
				if !identifySnapshot(t, h).Running {
					t.Fatal("valid heartbeat failed to extend lease")
				}
				// Status polling must NOT extend the lease.
				h.clock.Advance(time.Second)
				h.srv.DMX.Heartbeat("test")
			case "dmx-stop", "all-stop":
				ends = false
				path := "/api/dmx/stop"
				if action == "all-stop" {
					path = "/api/output/stop"
				}
				if rr := doJSON(t, h.srv.Handler(), "POST", path, nil); rr.Code != 200 {
					t.Fatal(rr.Body.String())
				}
			case "show-change":
				doJSON(t, h.srv.Handler(), "POST", "/api/patch/new", map[string]any{"name": "Next show"})
			case "close":
				h.srv.Close()
			case "send-error":
				ends = false
				h.tport.SetSendError(errors.New("link down"))
				h.clock.Advance(25 * time.Millisecond)
				if h.srv.outputStatus().Error == "" {
					t.Fatal("a transmit error is not reported as an output error")
				}
				h.tport.SetSendError(nil)
			}
			st := identifySnapshot(t, h)
			if !ends {
				if !st.Running {
					t.Fatalf("%s turned Identify off: %+v", action, st)
				}
				return
			}
			if st.Armed || st.Running {
				t.Fatalf("%s left Identify on: %+v", action, st)
			}
			identifyCall(t, h, "start", lease, 409)
			h.tport.TakeSent()
			h.clock.Advance(time.Second)
			if n := len(inRange(h.tport.TakeSent(), 1, 2)); n != 0 {
				t.Fatalf("%d packets in the identified range after Identify ended", n)
			}
		})
	}
}

// TestUniverseIdentifyStartupFailuresAndRehearsal: a wire failure is now the
// master output's error (Identify itself starts — it is a source); and in
// rehearsal sACN Identify works without opening any socket, because the
// simulated engine's sACN goes to an in-memory link.
func TestUniverseIdentifyStartupFailuresAndRehearsal(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	h.srv.DMX.Arm("test")
	h.srv.OutputBindIP = net.ParseIP("::1") // the sACN socket requires IPv4
	lease := armIdentify(t, h, "sacn", 1, 2)
	identifyCall(t, h, "start", lease, 200)
	if st := h.srv.outputStatus(); st.Error == "" {
		t.Fatalf("an sACN socket that cannot open is not reported: %+v", st)
	}

	r := newHarness(t)
	r.srv.Simulation = true
	t.Cleanup(r.srv.Close)
	r.srv.DMX.Arm("test")
	lease = armIdentify(t, r, "sacn", 1, 2)
	identifyCall(t, r, "start", lease, 200)
	r.clock.Advance(100 * time.Millisecond)
	r.srv.simSACN.mu.Lock()
	n := r.srv.simSACN.sent
	r.srv.simSACN.mu.Unlock()
	if n == 0 {
		t.Fatal("simulated sACN Identify sent nothing to the in-memory link")
	}
	if st := r.srv.outputStatus(); st.Error != "" || !strings.Contains(st.SACNAdapter, "simulated") {
		t.Fatalf("simulated sACN: error %q adapter %q", st.Error, st.SACNAdapter)
	}
}

func TestUniverseIdentifyUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	if out, err := exec.Command(node, "static/js/testdata/universeidentify_test.js").CombinedOutput(); err != nil {
		t.Fatalf("Universe Identify UI: %v\n%s", err, out)
	}
}
