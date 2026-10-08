package web

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// This file is chunk C3's wire-level proof, written against the HTTP surface
// only (literal JSON bodies, the real handlers, packets counted and decoded
// at session.FakeTransport under FakeClock, and sACN read off a real UDP
// socket by octet offset), so it compiles and runs against the pre-C3 tree
// too: every failure it produces there is the proof that the old code did
// not do this.
//
// The owner's rules under test (Dom, 2026-10-06/07):
//   - one engine owns every universe; the master Arm governs ALL DMX data;
//   - the Arm lease is kept alive by ANY connected browser and lost only when
//     every browser has been silent for 5 s; explicit Disarm always blacks out;
//   - lease loss does what Settings says: Blackout (default) or Hold last look;
//   - each universe outputs Art-Net, sACN, or both, sACN on its own adapter;
//   - sources layer per channel: tests < programmer < raw < identify.

type c3Status struct {
	State           string `json:"state"`
	Simulated       bool   `json:"simulated"`
	LeaseLossAction string `json:"leaseLossAction"`
	Browsers        int    `json:"browsers"`
	Error           string `json:"error"`
	LastDisarm      string `json:"lastDisarm"`
}

func c3Post(t *testing.T, h *testHarness, path string, body any) []byte {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "POST", path, body)
	if rr.Code != 200 {
		t.Errorf("POST %s: status %d: %s", path, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return rr.Body.Bytes()
}

func c3Output(t *testing.T, h *testHarness) c3Status {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/output", nil)
	var st c3Status
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &st) != nil {
		t.Errorf("GET /api/output: status %d: %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return st
}

func c3Arm(t *testing.T, h *testHarness, client string) {
	t.Helper()
	c3Post(t, h, "/api/output/arm", map[string]any{"client": client})
}

func c3Heartbeat(t *testing.T, h *testHarness, client string) {
	t.Helper()
	c3Post(t, h, "/api/output/heartbeat", map[string]any{"client": client})
}

// c3Frame posts a full raw frame for one universe the way the Send screen
// does (POST /api/dmx), with the given 1-based channel levels.
func c3Frame(t *testing.T, h *testHarness, universe uint16, levels map[int]byte) {
	t.Helper()
	f := make([]byte, 512)
	for ch, v := range levels {
		f[ch-1] = v
	}
	c3Post(t, h, "/api/dmx", map[string]any{"universe": universe, "channels": f})
}

// c3Settings reads the current settings as a generic map, applies edits,
// and POSTs the whole object back (POST /api/settings replaces it).
func c3Settings(t *testing.T, h *testHarness, edits map[string]any) int {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/settings", nil)
	cur := map[string]any{}
	if err := json.Unmarshal(rr.Body.Bytes(), &cur); err != nil {
		t.Fatalf("GET /api/settings: %v", err)
	}
	for k, v := range edits {
		cur[k] = v
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/settings", cur)
	return rr.Code
}

type c3Dmx struct {
	at   time.Duration
	data []byte
}

// c3ArtDmx returns every ArtDmx datagram for one Port-Address in sent,
// decoded from the real bytes.
func c3ArtDmx(t *testing.T, sent []session.SentPacket, universe uint16) [][]byte {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(universe)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, sp := range sent {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindDmx {
			continue
		}
		if sp.Packet.Dmx.SubUni == pa.SubUni() && sp.Packet.Dmx.Net == pa.Net {
			out = append(out, append([]byte(nil), sp.Packet.Dmx.Data...))
		}
	}
	return out
}

func c3AllArtDmx(sent []session.SentPacket) int {
	n := 0
	for _, sp := range sent {
		if sp.DecodeErr == nil && sp.Packet.Kind == artnet.KindDmx {
			n++
		}
	}
	return n
}

func c3AllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// TestC3DisarmedSendsNoDMXFromAnyFeature: with the master output disarmed,
// every feature that produces DMX is exercised and not one ArtDmx datagram
// may reach the transport.
func TestC3DisarmedSendsNoDMXFromAnyFeature(t *testing.T) {
	h := newHarness(t)
	seedOneFixture(t, h, 0)
	h.tport.TakeSent()

	c3Frame(t, h, 1, map[int]byte{1: 200})
	// The Console's tests (C7: the old Rig Check routes are retired).
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC 1", 0, 10)); rr.Code != 200 {
		t.Fatalf("create entry: %d %s", rr.Code, rr.Body.String())
	}
	startTests(t, h, "dimmer_sine")
	b := c3Post(t, h, "/api/dmx/identify/arm", map[string]any{"protocol": "artnet", "from": 7, "to": 7})
	var arm struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(b, &arm)
	c3Post(t, h, "/api/dmx/identify/start", map[string]any{"token": arm.Token})
	for i := 0; i < 40; i++ {
		h.clock.Advance(25 * time.Millisecond)
	}
	if n := c3AllArtDmx(h.tport.TakeSent()); n != 0 {
		t.Errorf("disarmed: %d ArtDmx datagrams reached the wire from raw levels, the tests and Universe Identify; want 0", n)
	}
	if st := c3Output(t, h); st.State != "disarmed" {
		t.Errorf("output state = %q, want disarmed", st.State)
	}
}

// TestC3ArmPutsPacketsOnTheWire: arming lets the same frame out.
func TestC3ArmPutsPacketsOnTheWire(t *testing.T) {
	h := newHarness(t)
	c3Frame(t, h, 0, map[int]byte{1: 200})
	h.clock.Advance(100 * time.Millisecond)
	if n := c3AllArtDmx(h.tport.TakeSent()); n != 0 {
		t.Errorf("before arm: %d ArtDmx datagrams, want 0", n)
	}
	c3Arm(t, h, "laptop")
	h.clock.Advance(100 * time.Millisecond)
	frames := c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) < 4 {
		t.Fatalf("armed for 100 ms: %d ArtDmx datagrams for universe 0, want at least 4 (40 Hz)", len(frames))
	}
	if frames[len(frames)-1][0] != 200 {
		t.Errorf("slot 1 on the wire = %d, want 200", frames[len(frames)-1][0])
	}
	if st := c3Output(t, h); st.State != "armed" {
		t.Errorf("output state = %q, want armed", st.State)
	}
}

// TestC3LeaseHeldByAnyBrowserLostWhenAllSilent: two browsers; the laptop goes
// quiet but the phone keeps heartbeating, so output stays armed for as long
// as the phone does. When the phone also goes quiet for 5 s the lease is
// lost, and with the default setting (Blackout) the rig gets zero frames and
// then silence.
func TestC3LeaseHeldByAnyBrowserLostWhenAllSilent(t *testing.T) {
	h := newHarness(t)
	c3Frame(t, h, 0, map[int]byte{1: 200})
	c3Arm(t, h, "laptop")
	c3Heartbeat(t, h, "phone")
	for i := 0; i < 12; i++ { // 12 s: the laptop has been silent for far longer than 5 s
		h.clock.Advance(time.Second)
		c3Heartbeat(t, h, "phone")
	}
	if st := c3Output(t, h); st.State != "armed" || st.Browsers != 1 {
		t.Errorf("phone still heartbeating: state=%q browsers=%d, want armed with 1 browser", st.State, st.Browsers)
	}
	h.tport.TakeSent()
	h.clock.Advance(4900 * time.Millisecond)
	if st := c3Output(t, h); st.State != "armed" {
		t.Errorf("4.9 s after the last heartbeat: state=%q, want still armed", st.State)
	}
	h.clock.Advance(200 * time.Millisecond)
	st := c3Output(t, h)
	if st.State != "disarmed" || st.LastDisarm != "lease" || st.LeaseLossAction != "blackout" {
		t.Errorf("5.1 s after the last heartbeat: state=%q lastDisarm=%q action=%q, want disarmed by lease with the default blackout action", st.State, st.LastDisarm, st.LeaseLossAction)
	}
	frames := c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) < 3 {
		t.Fatalf("lease loss: %d frames, want the live frames then 3 zero frames", len(frames))
	}
	for i, f := range frames[len(frames)-3:] {
		if !c3AllZero(f) {
			t.Errorf("lease-loss blackout frame %d is not all zero (slot 1 = %d)", i+1, f[0])
		}
	}
	h.clock.Advance(2 * time.Second)
	if n := c3AllArtDmx(h.tport.TakeSent()); n != 0 {
		t.Errorf("after the lease-loss blackout: %d more ArtDmx datagrams, want silence", n)
	}
}

// TestC3LeaseLossHoldLastLook: with Settings → Hold last look, losing the
// lease keeps the last frames on the wire unchanged; live edits do not reach
// the wire until someone re-arms; Disarm then blacks out.
func TestC3LeaseLossHoldLastLook(t *testing.T) {
	h := newHarness(t)
	if code := c3Settings(t, h, map[string]any{"leaseLossAction": "hold"}); code != 200 {
		t.Fatalf("POST /api/settings leaseLossAction=hold: status %d", code)
	}
	c3Frame(t, h, 0, map[int]byte{1: 200})
	c3Arm(t, h, "laptop")
	h.clock.Advance(5100 * time.Millisecond)
	if st := c3Output(t, h); st.State != "holding" {
		t.Errorf("lease lost with Hold: state=%q, want holding", st.State)
	}
	h.tport.TakeSent()
	c3Frame(t, h, 0, map[int]byte{1: 50})
	h.clock.Advance(time.Second)
	frames := c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) < 30 {
		t.Fatalf("holding for 1 s: %d frames, want the held look retransmitted at 40 Hz", len(frames))
	}
	for _, f := range frames {
		if f[0] != 200 {
			t.Fatalf("holding: slot 1 = %d on the wire; a live edit reached the wire while the lease was lost (want the held 200)", f[0])
		}
	}
	c3Arm(t, h, "phone")
	h.clock.Advance(50 * time.Millisecond)
	frames = c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) == 0 || frames[len(frames)-1][0] != 50 {
		t.Errorf("after re-arm the live edit (50) must reach the wire; got %v frames", len(frames))
	}
	c3Post(t, h, "/api/output/disarm", map[string]any{"client": "phone"})
	frames = c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) != 3 {
		t.Errorf("disarm: %d frames, want exactly 3 zero frames", len(frames))
	}
	for _, f := range frames {
		if !c3AllZero(f) {
			t.Errorf("disarm frame not all zero")
		}
	}
}

// TestC3UnloadBeaconRule: a page unload says goodbye. While another browser
// is still heartbeating, output stays armed; the last browser's goodbye loses
// the lease at once, without waiting 5 s.
func TestC3UnloadBeaconRule(t *testing.T) {
	h := newHarness(t)
	c3Frame(t, h, 0, map[int]byte{1: 200})
	c3Arm(t, h, "laptop")
	c3Heartbeat(t, h, "phone")
	c3Post(t, h, "/api/output/goodbye", map[string]any{"client": "laptop"})
	if st := c3Output(t, h); st.State != "armed" {
		t.Errorf("laptop unloaded while the phone is connected: state=%q, want armed", st.State)
	}
	h.tport.TakeSent()
	c3Post(t, h, "/api/output/goodbye", map[string]any{"client": "phone"})
	if st := c3Output(t, h); st.State != "disarmed" || st.LastDisarm != "lease" {
		t.Errorf("last browser unloaded: state=%q lastDisarm=%q, want disarmed by lease immediately", st.State, st.LastDisarm)
	}
	frames := c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) != 3 || !c3AllZero(frames[0]) {
		t.Errorf("last browser unloaded: %d frames, want 3 zero frames", len(frames))
	}
}

// TestC3StopAllOutputDisarms: the strip's Stop all output is Disarm.
func TestC3StopAllOutputDisarms(t *testing.T) {
	h := newHarness(t)
	c3Frame(t, h, 0, map[int]byte{1: 200})
	c3Arm(t, h, "laptop")
	h.clock.Advance(100 * time.Millisecond)
	h.tport.TakeSent()
	c3Post(t, h, "/api/output/stop", nil)
	if st := c3Output(t, h); st.State != "disarmed" || st.LastDisarm != "operator" {
		t.Errorf("after Stop all output: state=%q lastDisarm=%q, want disarmed by the operator", st.State, st.LastDisarm)
	}
	frames := c3ArtDmx(t, h.tport.TakeSent(), 0)
	if len(frames) != 3 {
		t.Errorf("Stop all output: %d frames, want 3 zero frames", len(frames))
	}
	c3Frame(t, h, 0, map[int]byte{1: 99})
	h.clock.Advance(time.Second)
	if n := c3AllArtDmx(h.tport.TakeSent()); n != 0 {
		t.Errorf("after Stop all output a new Send frame put %d datagrams on the wire, want 0", n)
	}
}

// TestC3CompositionRawOverTests: the Console's tests light a fixture on
// universe 0 and 2; a raw frame on universe 0 owns that whole universe, so
// universe 0 carries the raw frame — even after the tests recompute — and
// universe 2 still carries the test.
func TestC3CompositionRawOverTests(t *testing.T) {
	h := newHarness(t)
	for _, u := range []uint16{0, 2} {
		if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", jdcLikeEntryRequest("JDC", u, 1)); rr.Code != 200 {
			t.Fatalf("create entry: %d %s", rr.Code, rr.Body.String())
		}
	}
	c3Arm(t, h, "laptop")
	c3Post(t, h, "/api/tests/fade", map[string]any{"fadeMs": 0})
	hold := func(level int) {
		c3Post(t, h, "/api/tests/set", map[string]any{"tests": []map[string]any{{"kind": "dimmer_toggle", "max": level, "on": true}}, "scope": map[string]any{"kind": "all"}})
	}
	hold(255)
	c3Frame(t, h, 0, map[int]byte{2: 10, 100: 7})
	// The tests recompute after the raw frame arrived (a level change) — in
	// a single shared buffer the later writer would wipe the raw frame.
	hold(128)
	h.tport.TakeSent()
	h.clock.Advance(100 * time.Millisecond)
	sent := h.tport.TakeSent()
	u0 := c3ArtDmx(t, sent, 0)
	u2 := c3ArtDmx(t, sent, 2)
	if len(u0) == 0 || len(u2) == 0 {
		t.Fatalf("universe 0: %d frames, universe 2: %d frames; want both on the wire", len(u0), len(u2))
	}
	last0, last2 := u0[len(u0)-1], u2[len(u2)-1]
	if last0[0] != 0 || last0[1] != 10 || last0[99] != 7 {
		t.Errorf("universe 0 slots 1,2,100 = %d,%d,%d; want the raw frame 0,10,7 (raw outranks tests)", last0[0], last0[1], last0[99])
	}
	if last2[0] != 128 {
		t.Errorf("universe 2 slot 1 (dimmer) = %d; want the test's 128, untouched by the raw frame on universe 0", last2[0])
	}
}

// TestC3IdentifyOwnsOnlyItsUniverse: Identify on Art-Net 1 takes that
// universe over exclusively while raw output continues on universe 0, and is
// accepted while other output is running.
func TestC3IdentifyOwnsOnlyItsUniverse(t *testing.T) {
	h := newHarness(t)
	c3Arm(t, h, "laptop")
	c3Frame(t, h, 0, map[int]byte{1: 200})
	c3Frame(t, h, 1, map[int]byte{5: 99})
	b := c3Post(t, h, "/api/dmx/identify/arm", map[string]any{"protocol": "artnet", "from": 1, "to": 1})
	var arm struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(b, &arm)
	c3Post(t, h, "/api/dmx/identify/start", map[string]any{"token": arm.Token})
	h.tport.TakeSent()
	h.clock.Advance(100 * time.Millisecond)
	sent := h.tport.TakeSent()
	u0, u1 := c3ArtDmx(t, sent, 0), c3ArtDmx(t, sent, 1)
	if len(u0) == 0 || len(u1) == 0 {
		t.Fatalf("universe 0: %d frames, universe 1: %d frames; want both", len(u0), len(u1))
	}
	if u0[len(u0)-1][0] != 200 {
		t.Errorf("universe 0 slot 1 = %d, want the raw 200 (identify must not touch it)", u0[len(u0)-1][0])
	}
	f := u1[len(u1)-1]
	if f[0] != 255 || f[4] != 0 {
		t.Errorf("universe 1 slots 1,5 = %d,%d; want identify's 255,0 (identify owns the universe exclusively)", f[0], f[4])
	}
}

// TestC3ArtNetAndSACNOnOneUniverse: a universe set to "both" goes out as
// ArtDmx on the Art-Net transport and as E1.31 on the sACN socket, with
// identical slot data; disarm then sends zero frames on both and E1.31
// Stream_Terminated (ANSI E1.31 Section 6.2.6), then silence.
func TestC3ArtNetAndSACNOnOneUniverse(t *testing.T) {
	h := newHarness(t)
	ln := sacnLoopback(t, h, 100)
	if code := c3Settings(t, h, map[string]any{"universeProtocols": []any{map[string]any{"universe": 0, "protocol": "both"}}}); code != 200 {
		t.Fatalf("POST /api/settings universeProtocols: status %d", code)
	}
	levels := map[int]byte{1: 11, 2: 22, 300: 33, 512: 44}
	c3Frame(t, h, 0, levels)
	c3Arm(t, h, "laptop")
	h.clock.Advance(100 * time.Millisecond)
	art := c3ArtDmx(t, h.tport.TakeSent(), 0)
	e131 := drainE131(t, ln, 200*time.Millisecond)
	if len(art) == 0 || len(e131) == 0 {
		t.Fatalf("both protocols: %d ArtDmx, %d E1.31 datagrams; want both on the wire", len(art), len(e131))
	}
	if e131[0].universe != 1 {
		t.Errorf("E1.31 universe = %d, want 1 (show universe 1 at sACN start 1)", e131[0].universe)
	}
	if !bytes.Equal(art[len(art)-1], e131[len(e131)-1].slots) {
		t.Errorf("slot data differs between Art-Net and sACN on the same universe")
	}
	c3Post(t, h, "/api/output/disarm", map[string]any{"client": "laptop"})
	art = c3ArtDmx(t, h.tport.TakeSent(), 0)
	e131 = drainE131(t, ln, 200*time.Millisecond)
	if len(art) != 3 {
		t.Errorf("disarm: %d ArtDmx, want 3 zero frames", len(art))
	}
	zero, term := 0, 0
	for _, p := range e131 {
		switch {
		case p.terminated:
			term++
		case p.allZero:
			zero++
		default:
			t.Errorf("disarm: a non-zero E1.31 data packet followed Disarm")
		}
	}
	if zero != 3 || term != 3 {
		t.Errorf("disarm on sACN: %d zero frames and %d Stream_Terminated, want 3 and 3", zero, term)
	}
	h.clock.Advance(2 * time.Second)
	if n := c3AllArtDmx(h.tport.TakeSent()); n != 0 {
		t.Errorf("after disarm: %d ArtDmx, want silence", n)
	}
	if p := drainE131(t, ln, 100*time.Millisecond); len(p) != 0 {
		t.Errorf("after disarm: %d E1.31 datagrams, want silence", len(p))
	}
}

// TestC3OutputSettingsPersistWithBackup: the lease-loss action, the sACN
// adapter and the per-universe protocols live in benny512-settings.json with
// the same .bak discipline as every other setting.
func TestC3OutputSettingsPersistWithBackup(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatal(err)
	}
	if code := c3Settings(t, h, map[string]any{"leaseLossAction": "hold", "sacnNic": "eth1",
		"universeProtocols": []any{map[string]any{"universe": 4, "protocol": "sacn"}}}); code != 200 {
		t.Fatalf("first save: status %d", code)
	}
	if code := c3Settings(t, h, map[string]any{"leaseLossAction": "blackout"}); code != 200 {
		t.Fatalf("second save: status %d", code)
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no .bak after the second save: %v", err)
	}
	for _, want := range []string{`"leaseLossAction": "blackout"`, `"sacnNic": "eth1"`, `"universeProtocols"`, `"protocol": "sacn"`} {
		if !bytes.Contains(cur, []byte(want)) {
			t.Errorf("settings file lacks %s:\n%s", want, cur)
		}
	}
	if !bytes.Contains(bak, []byte(`"leaseLossAction": "hold"`)) {
		t.Errorf(".bak does not hold the preceding save:\n%s", bak)
	}
	loaded, err := LoadSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(loaded)
	if !bytes.Contains(b, []byte(`"leaseLossAction":"blackout"`)) || !bytes.Contains(b, []byte(`"universe":4`)) {
		t.Errorf("reloaded settings lost the output fields: %s", b)
	}
}

// TestC3OutputSettingsRefuseUnknownValues: a protocol or action the engine
// does not know is refused with the server's own sentence, never mapped.
func TestC3OutputSettingsRefuseUnknownValues(t *testing.T) {
	h := newHarness(t)
	for _, edit := range []map[string]any{
		{"leaseLossAction": "fade"},
		{"universeProtocols": []any{map[string]any{"universe": 0, "protocol": "dmx"}}},
		{"universeProtocols": []any{map[string]any{"universe": 40000, "protocol": "artnet"}}},
	} {
		if code := c3Settings(t, h, edit); code != 400 {
			t.Errorf("POST /api/settings %v: status %d, want 400", edit, code)
		}
	}
}

// loopbackInterfaceName finds this machine's loopback adapter by flag, so the
// sACN-adapter test does not hard-code "lo".
func loopbackInterfaceName(t *testing.T) string {
	t.Helper()
	ifs, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces here: %v", err)
	}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				return i.Name
			}
		}
	}
	t.Skip("no IPv4 loopback adapter on this machine")
	return ""
}

// TestC3SACNUsesItsOwnAdapter: with an sACN adapter chosen on Settings, the
// sACN socket is bound to that adapter's address while Art-Net stays on the
// Art-Net transport.
func TestC3SACNUsesItsOwnAdapter(t *testing.T) {
	h := newHarness(t)
	ln := sacnLoopback(t, h, 100)
	lo := loopbackInterfaceName(t)
	if code := c3Settings(t, h, map[string]any{"sacnNic": lo, "universeProtocols": []any{map[string]any{"universe": 0, "protocol": "both"}}}); code != 200 {
		t.Fatalf("POST /api/settings sacnNic=%s: status %d", lo, code)
	}
	c3Frame(t, h, 0, map[int]byte{1: 1})
	c3Arm(t, h, "laptop")
	h.clock.Advance(50 * time.Millisecond)
	if n := len(c3ArtDmx(t, h.tport.TakeSent(), 0)); n == 0 {
		t.Errorf("Art-Net transport carried no ArtDmx for universe 0")
	}
	if err := ln.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_, from, err := ln.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no E1.31 datagram arrived: %v", err)
	}
	if !from.IP.IsLoopback() {
		t.Errorf("E1.31 source address %v is not on the chosen sACN adapter %s", from.IP, lo)
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/output", nil)
	var st struct {
		SACNAdapter string `json:"sacnAdapter"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &st)
	if !strings.Contains(st.SACNAdapter, lo) {
		t.Errorf("GET /api/output sacnAdapter = %q, want it to name the chosen adapter %s", st.SACNAdapter, lo)
	}
}
