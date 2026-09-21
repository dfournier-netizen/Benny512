package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"sync"
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

func TestUniverseIdentifyArtNetRangeIsolationAndZero(t *testing.T) {
	for _, bounds := range [][2]int{{0, 2}, {511, 512}} {
		t.Run(fmt.Sprintf("%d-%d", bounds[0], bounds[1]), func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			seedOneFixture(t, h, 900)
			// Dormant manual frames include both an overlapping and an unrelated
			// universe. The tool must not transmit, erase or resume either one.
			for _, raw := range []uint16{1, 800} {
				pa, _ := artnet.PortAddressFromRaw(raw)
				h.srv.DMX.StartUniverse(pa, netip.AddrPort{}, 512)
				_ = h.srv.DMX.SetFrame(pa, bytes.Repeat([]byte{73}, 512))
			}
			lease := armIdentify(t, h, "artnet", bounds[0], bounds[1])
			h.clock.Advance(100 * time.Millisecond)
			if h.tport.SentCount() != 0 {
				t.Fatal("Arm transmitted before Identify on")
			}
			identifyCall(t, h, "start", lease, 200)
			h.clock.Advance(100 * time.Millisecond)
			checkIdentifyArtNet(t, h.tport.TakeSent(), bounds[0], bounds[1], false)
			identifyCall(t, h, "stop", nil, 200)
			checkIdentifyArtNet(t, h.tport.TakeSent(), bounds[0], bounds[1], true)
			h.clock.Advance(time.Second)
			if h.tport.SentCount() != 0 || h.srv.DMX.OutputRunning() {
				t.Fatal("disarm left output running or resumed manual Send")
			}
			for _, raw := range []uint16{1, 800} {
				pa, _ := artnet.PortAddressFromRaw(raw)
				f, ok := h.srv.DMX.Frame(pa)
				if !ok || !bytes.Equal(f, bytes.Repeat([]byte{73}, 512)) {
					t.Fatalf("Identify altered dormant manual universe %d", raw)
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
		if h.tport.SentCount() != 0 || len(drainE131(t, ln, time.Millisecond)) != 0 {
			t.Fatal("sACN selection emitted Art-Net or disarm left sACN transmitting")
		}
	}
}

func TestUniverseIdentifyOwnershipAndStaleRequests(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	seedOneFixture(t, h, 400)
	lease := armIdentify(t, h, "artnet", 4, 5)
	for _, path := range []string{"/api/dmx", "/api/dmx/start", "/api/patch/rigcheck/start", "/api/patch/rigcheck/pattern/output", "/api/patch/rigcheck/pattern/start"} {
		if rr := doJSON(t, h.srv.Handler(), "POST", path, map[string]any{}); rr.Code != 409 {
			t.Fatalf("armed Identify did not reject %s: %d", path, rr.Code)
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
	checkIdentifyArtNet(t, h.tport.TakeSent(), 10, 10, false)
}

func TestUniverseIdentifyRefusesExistingOutput(t *testing.T) {
	for _, protocol := range []string{"artnet", "sacn", "manual"} {
		t.Run(protocol, func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			if protocol == "manual" {
				pa, _ := artnet.PortAddressFromRaw(10)
				h.srv.DMX.StartUniverse(pa, netip.AddrPort{}, 512)
				h.srv.DMX.Start()
				t.Cleanup(h.srv.DMX.Stop)
			} else {
				if protocol == "sacn" {
					sacnLoopback(t, h, 100)
				}
				seedOneFixture(t, h, 0)
				startRigCheck(t, h, protocol)
			}
			identifyCall(t, h, "arm", map[string]any{"protocol": "artnet", "from": 1, "to": 2}, 409)
		})
	}
}

func TestUniverseIdentifyLeaseStopPathsAndFailures(t *testing.T) {
	for _, action := range []string{"lease", "dmx-stop", "all-stop", "show-change", "close", "send-error"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			lease := armIdentify(t, h, "artnet", 1, 2)
			identifyCall(t, h, "start", lease, 200)
			h.tport.TakeSent()
			switch action {
			case "lease":
				h.clock.Advance(4 * time.Second)
				identifyCall(t, h, "heartbeat", lease, 200)
				h.clock.Advance(4 * time.Second)
				if !identifySnapshot(t, h).Running {
					t.Fatal("valid heartbeat failed to extend lease")
				}
				// Status polling must NOT extend the lease.
				h.clock.Advance(time.Second)
			case "dmx-stop", "all-stop":
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
				h.tport.SetSendError(errors.New("link down"))
				h.clock.Advance(25 * time.Millisecond)
			}
			if st := identifySnapshot(t, h); st.Armed || st.Running {
				t.Fatalf("%s left Identify armed: %+v", action, st)
			}
			identifyCall(t, h, "start", lease, 409)
			h.tport.TakeSent()
			h.clock.Advance(time.Second)
			if h.tport.SentCount() != 0 {
				t.Fatal("packets after disarm")
			}
		})
	}
}

func TestUniverseIdentifyArmRacesManualStart(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	t.Cleanup(h.srv.DMX.Stop)
	pa, _ := artnet.PortAddressFromRaw(800)
	h.srv.DMX.StartUniverse(pa, netip.AddrPort{}, 512)
	var wg sync.WaitGroup
	for _, path := range []string{identifyURL + "/arm", "/api/dmx/start"} {
		wg.Go(func() {
			doJSON(t, h.srv.Handler(), "POST", path, map[string]any{"protocol": "artnet", "from": 1, "to": 2})
		})
	}
	wg.Wait()
	if identifySnapshot(t, h).Armed == h.srv.DMX.OutputRunning() {
		t.Fatal("exactly one of Identify Arm and manual Start must succeed")
	}
}

func TestUniverseIdentifyStartupFailuresAndRehearsal(t *testing.T) {
	for _, protocol := range []string{"artnet", "sacn"} {
		t.Run(protocol, func(t *testing.T) {
			h := newHarness(t)
			t.Cleanup(h.srv.Close)
			lease := armIdentify(t, h, protocol, 1, 2)
			if protocol == "artnet" {
				h.tport.SetSendError(errors.New("link down"))
			} else {
				h.srv.OutputBindIP = net.ParseIP("::1") // sender requires IPv4
			}
			identifyCall(t, h, "start", lease, 500)
			st := identifySnapshot(t, h)
			if st.Armed || st.Running || st.Error == "" || h.tport.SentCount() != 0 {
				t.Fatalf("failed start did not disarm: %+v", st)
			}
			identifyCall(t, h, "start", lease, 409)
		})
	}
	h := newHarness(t)
	h.srv.Simulation = true
	identifyCall(t, h, "arm", map[string]any{"protocol": "sacn", "from": 1, "to": 2}, 409)
	if st := identifySnapshot(t, h); st.Armed || st.Running {
		t.Fatalf("rehearsal armed a real socket: %+v", st)
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
