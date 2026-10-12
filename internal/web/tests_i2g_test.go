package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"benny512/internal/patch"
)

// Console-lite I2g — owner answers 2026-10-09 20:17 to the I2f questions.

// TestLegacyPresetLoadsWithoutIsolate (owner, 2026-10-09): a test preset
// saved with the old Rig Check's Isolate on loads with Isolate OFF. Isolate
// is never restored from a preset; only a live action while Armed turns it
// on (POST /api/tests/isolate, the engine's transient source). Before, the
// load handed the stored flag to the tests layer, which then wrote no base
// state: an untested channel of a tested fixture was held at 0 — the old
// Isolate, back without anyone pressing it.
func TestLegacyPresetLoadsWithoutIsolate(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	e := patch.Entry{ID: "a", Name: "Spot", StartAddress: 1, Footprint: 2, ChannelFunctions: map[uint16]patch.ChannelFunction{
		1: {Source: patch.SourceGDTF, Attribute: "Dimmer", DMXFrom: 0, DMXTo: 255},
		2: {Source: patch.SourceGDTF, Attribute: "Pan", DMXFrom: 0, DMXTo: 255},
	}}
	d := showWorkspace{Presets: []savedTestPreset{{
		savedGroup: savedGroup{ID: "legacy", Name: "Old Rig Check", EntryIDs: []string{"a"}},
		Specs:      []patch.PatternSpec{{Kind: patch.PatternMoveExtreme, Params: patch.PatternParams{Target: "pan_max"}}},
		Isolate:    true, // saved by the old Rig Check's Isolate
	}}}
	b, _ := json.Marshal(d)
	h.srv.PatchStore.Replace(patch.Patch{Name: "Show", Entries: []patch.Entry{e}, Workspace: b})
	r := &c4aRig{h: h, ids: map[string]string{}}
	r.arm(t)

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/workspace/load-preset", map[string]string{"id": "legacy"})
	if rr.Code != 200 {
		t.Fatalf("load-preset: %d %s", rr.Code, rr.Body.String())
	}
	var st struct {
		BaseState struct {
			Isolate bool `json:"isolate"`
		} `json:"baseState"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.BaseState.Isolate {
		t.Errorf("the loaded preset's status says Isolate is on; a preset never restores Isolate")
	}
	if h.srv.DMX.Isolating() {
		t.Errorf("the engine's Isolate is on after a preset load; only a live action while Armed turns it on")
	}
	var tv struct {
		Isolate struct {
			On bool `json:"on"`
		} `json:"isolate"`
	}
	if rr := doJSON(t, h.srv.Handler(), "GET", "/api/tests", nil); rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &tv) != nil {
		t.Fatalf("GET /api/tests: %d %s", rr.Code, rr.Body.String())
	} else if tv.Isolate.On {
		t.Errorf("GET /api/tests says Isolate is on after a preset load")
	}
	// On the wire: the pan test runs and the base state opens the
	// untested dimmer (Isolate would hold it at 0), once the fade is done.
	r.advance(t, 1100*time.Millisecond)
	f := r.wire(t, 0)
	wantSlots(t, "pan at the test's max, dimmer opened by the base state (not isolated)", f, 1, 255, 255)
}

// i2gParRig is the I2e rig (BT batten, RG RGB-only at 201-203 on universe
// 2) plus PAR: an LED par with a real Dimmer and RGB at 301-304.
func i2gParRig(t *testing.T) *c4aRig {
	t.Helper()
	r := newI2eTestsRig(t)
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "channelSets": []any{}}
	}
	par := map[string]any{"name": "PAR", "fixtureType": "Spec-derived LED par", "footprint": 4, "universe": 2, "startAddress": 301,
		"channelFunctions": map[string]any{"1": ch("Dimmer"), "2": ch("ColorAdd_R"), "3": ch("ColorAdd_G"), "4": ch("ColorAdd_B")}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{par}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import PAR: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	return r
}

// fadeSamples reads universe u's wire frame every 100 ms (the first 30 ms
// after the change) for n samples: what a receiver sees across a fade.
func (r *c4aRig) fadeSamples(t *testing.T, u uint16, n int) [][]byte {
	t.Helper()
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, r.wire(t, u))
		r.advance(t, 70*time.Millisecond)
	}
	return out
}

// checkFade asserts a sampled fade of one value (intensity per sample) runs
// smoothly from about from to exactly to: no snap at the start, no
// overshoot or reversal on the way, no step larger than maxStep, and the
// end value reached. log is every sample, printed on failure.
func checkFade(t *testing.T, what string, got []int, from, to, maxStep int) {
	t.Helper()
	bad := false
	if d := got[0] - from; d > maxStep || d < -maxStep {
		t.Errorf("%s: 30 ms into the fade the output is %d, want near %d (a snap, not a fade)", what, got[0], from)
		bad = true
	}
	reversed, stepped := false, false
	for i := 1; i < len(got); i++ {
		step := got[i] - got[i-1]
		if !reversed && ((to >= from && step < 0) || (to < from && step > 0)) {
			t.Errorf("%s: the output reverses at sample %d (%d → %d): it must move one way, %d → %d", what, i, got[i-1], got[i], from, to)
			reversed, bad = true, true
		}
		if !stepped && (step > maxStep || step < -maxStep) {
			t.Errorf("%s: the output steps %d → %d at sample %d (more than %d in 100 ms of a 1 s fade)", what, got[i-1], got[i], i, maxStep)
			stepped, bad = true, true
		}
	}
	if last := got[len(got)-1]; last != to {
		t.Errorf("%s: the fade ends at %d, want %d", what, last, to)
		bad = true
	}
	if bad {
		t.Logf("%s: sampled every 100 ms: %v", what, got)
	}
}

// TestDimmerTestWhiteFadesWithTheFadeTime (I2g fade a, owner 2026-10-09):
// with a fade time set, a dimmer test on an LED par with a real dimmer and
// no colour set fades its white in and out like any other test change. The
// par stays under test throughout (a pan test, which it does not have,
// keeps it in scope, with its dimmer opened by the base state). Before, the
// white was claimed and released at once: the par jumped from dark to
// full white as the dimmer test started, and went dark at once as it
// stopped while its dimmer faded.
func TestDimmerTestWhiteFadesWithTheFadeTime(t *testing.T) {
	r := i2gParRig(t)
	r.arm(t)
	r.selectNames(t, "PAR")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 1000})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "move_extreme", "target": "pan_max"}}})
	r.advance(t, 1100*time.Millisecond)
	wantSlots(t, "PAR under a pan test: dimmer opened, no colour set (dark)", r.wire(t, 2), 301, 255, 0, 0, 0)

	intensity := func(what string, fs [][]byte) []int {
		out := make([]int, len(fs))
		for i, f := range fs {
			p := f[300:304]
			if p[1] != p[2] || p[2] != p[3] {
				t.Errorf("%s: sample %d colour %v is not white", what, i, p[1:])
			}
			out[i] = int(p[0]) * int(p[1]) / 255
		}
		return out
	}
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	on := r.fadeSamples(t, 2, 12)
	checkFade(t, "dimmer test on: PAR intensity (dimmer x white)", intensity("on", on), 0, 128, 40)
	wantSlots(t, "dimmer test on, fade done: dimmer at the test's 128, white", on[len(on)-1], 301, 128, 255, 255, 255)

	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "move_extreme", "target": "pan_max"}}})
	off := r.fadeSamples(t, 2, 12)
	checkFade(t, "dimmer test off: PAR intensity (dimmer x white)", intensity("off", off), 128, 0, 40)
	wantSlots(t, "dimmer test off, fade done: dimmer opened again, colour unset", off[len(off)-1], 301, 255, 0, 0, 0)
}

// TestVirtualDimmerTestFadesFromAndToDark (I2g fade b, owner 2026-10-09):
// a fixture or cell with colour mixing, no dimmer of its own and no colour
// set, under a dimmer test, rises smoothly from dark to the test's white
// and falls smoothly from it to dark over the fade time — no overshoot,
// snap or step — while it stays under test. Before, the virtual dimmer
// faded back to "untouched" (full), which with no colour set is full
// white, then was released and went dark: RG went 128 → 245 → 0; and a
// cell going under a dimmer test from untouched started at full white.
func TestVirtualDimmerTestFadesFromAndToDark(t *testing.T) {
	r := i2gParRig(t)
	r.arm(t)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 1000})
	white := func(what string, fs [][]byte, slot, master int) []int {
		out := make([]int, len(fs))
		for i, f := range fs {
			c := f[slot-1 : slot+2]
			if c[0] != c[1] || c[1] != c[2] {
				t.Errorf("%s: sample %d colour %v is not white", what, i, c)
			}
			out[i] = int(c[0])
			if master > 0 {
				out[i] = int(f[master-1]) * int(c[0]) / 255
			}
		}
		return out
	}
	dimmerOn := map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}}
	panOnly := map[string]any{"tests": []any{map[string]any{"kind": "move_extreme", "target": "pan_max"}}}

	// RG: RGB only, no dimmer, no colour set.
	r.selectNames(t, "RG")
	r.testsPost(t, "set", dimmerOn)
	checkFade(t, "RG dimmer test on", white("RG on", r.fadeSamples(t, 2, 12), 201, 0), 0, 128, 40)
	r.testsPost(t, "set", panOnly) // RG stays under test
	checkFade(t, "RG dimmer test off", white("RG off", r.fadeSamples(t, 2, 12), 201, 0), 128, 0, 40)

	// RD: RGB only like RG, but its profile states the colour defaults (0),
	// so the base state writes them and RD is already under test, its
	// virtual dimmer untouched, when the dimmer test starts.
	ch := func(attr string) map[string]any {
		return map[string]any{"source": "gdtf", "attribute": attr, "functionName": attr, "dmxFrom": 0, "dmxTo": 255, "channelSets": []any{},
			"hasDefault": true, "default": 0, "defaultByteCount": 1}
	}
	rd := map[string]any{"name": "RD", "fixtureType": "Spec-derived RGB with defaults", "footprint": 3, "universe": 2, "startAddress": 401,
		"channelFunctions": map[string]any{"1": ch("ColorAdd_R"), "2": ch("ColorAdd_G"), "3": ch("ColorAdd_B")}}
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{rd}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import RD: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	r.get(t, "/api/patch", &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	r.selectNames(t, "RD")
	r.testsPost(t, "set", panOnly)
	r.advance(t, 1100*time.Millisecond)
	wantSlots(t, "RD under a pan test: colour at its stated default (dark)", r.wire(t, 2), 401, 0, 0, 0)
	r.testsPost(t, "set", dimmerOn)
	checkFade(t, "RD dimmer test on", white("RD on", r.fadeSamples(t, 2, 12), 401, 0), 0, 128, 40)
	r.testsPost(t, "set", panOnly)
	checkFade(t, "RD dimmer test off", white("RD off", r.fadeSamples(t, 2, 12), 401, 0), 128, 0, 40)

	// The batten's cells: virtual dimmers under a real master the tests open.
	bt := r.ids["BT"]
	cells := []any{}
	for n := 1; n <= 4; n++ {
		cells = append(cells, cellTarget(bt, fmt.Sprintf("Pixel %d:0", n)))
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": cells}, nil)
	r.testsPost(t, "set", panOnly)
	r.advance(t, 1100*time.Millisecond)
	wantSlots(t, "BT cells under a pan test: master opened, cell 1 colour unset (dark)", r.wire(t, 2), 1, 255, 0, 0, 0)
	r.testsPost(t, "set", dimmerOn)
	checkFade(t, "BT cell 1 dimmer test on (master x white)", white("BT on", r.fadeSamples(t, 2, 12), 2, 1), 0, 128, 40)
	r.testsPost(t, "set", panOnly)
	checkFade(t, "BT cell 1 dimmer test off (master x white)", white("BT off", r.fadeSamples(t, 2, 12), 2, 1), 128, 0, 40)
}

// testsMessages collects the {"type":"tests"} WebSocket messages c receives
// until d has passed: their revisions, and when the first arrived.
func testsMessages(c *wsTestClient, d time.Duration) (revs []uint64, first time.Time) {
	end := time.Now().Add(d)
	for {
		m := c.next(end)
		if m == "" {
			return revs, first
		}
		var msg struct {
			Type     string  `json:"type"`
			Revision *uint64 `json:"revision"`
		}
		if json.Unmarshal([]byte(m), &msg) != nil || msg.Type != "tests" || msg.Revision == nil {
			continue
		}
		if len(revs) == 0 {
			first = time.Now()
		}
		revs = append(revs, *msg.Revision)
	}
}

// TestOverriddenNoteFollowsRawAndIdentifyLive (I2g, owner 2026-10-09): the
// "Overridden by …" note updates at once. Every change of which layer owns
// a tested channel is pushed to every browser as the existing
// {"type":"tests","revision":N} message, coalesced so a raw fader drag does
// not flood the socket. Proven over a real WebSocket: raw universe levels
// set and cleared, Universe Identify started and stopped — each announces a
// new tests revision, and that revision's view names the layer. Before,
// neither raw levels nor Identify announced anything, so an open note
// caught up only on the next tests read.
func TestOverriddenNoteFollowsRawAndIdentifyLive(t *testing.T) {
	r := i2gParRig(t)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	r.arm(t)
	r.selectNames(t, "PAR")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	ws := dialTestWS(t, ts, r.h.srv)
	testsMessages(ws, 150*time.Millisecond) // whatever the set itself announced

	overridden := func(want string) {
		t.Helper()
		var tv i2fTestsView
		r.get(t, "/api/tests", &tv)
		got := []string{}
		for _, tt := range tv.Tests {
			for _, o := range tt.Overridden {
				got = append(got, o.Source)
			}
		}
		if (want == "" && len(got) != 0) || (want != "" && (len(got) != 1 || got[0] != want)) {
			t.Errorf("the tests view says overridden by %v, want [%s]", got, want)
		}
	}
	announced := func(what string, after uint64) uint64 {
		t.Helper()
		start := time.Now()
		revs, first := testsMessages(ws, 400*time.Millisecond)
		if len(revs) == 0 {
			t.Errorf("%s: no {\"type\":\"tests\"} message on the WebSocket within 400 ms (an open note would stay stale until the next tests read)", what)
			return after
		}
		if revs[len(revs)-1] <= after {
			t.Errorf("%s: the tests revision did not move (%v after %d)", what, revs, after)
		}
		t.Logf("%s: announced revision %v after %v", what, revs, first.Sub(start).Round(time.Millisecond))
		return revs[len(revs)-1]
	}
	rev := v.Revision
	frame := func(level byte) {
		f := make([]byte, 512)
		f[300] = level
		if rr := r.do(t, "POST", "/api/dmx", map[string]any{"universe": 2, "channels": f}, nil); rr.Code != http.StatusOK {
			t.Fatalf("POST /api/dmx: %d %s", rr.Code, rr.Body.String())
		}
	}

	// Raw universe levels set: a fader drag of 40 moves in a burst.
	start := time.Now()
	for i := 0; i < 40; i++ {
		frame(byte(i))
	}
	revs, first := testsMessages(ws, 400*time.Millisecond)
	if len(revs) == 0 {
		t.Errorf("raw levels set: no {\"type\":\"tests\"} message on the WebSocket within 400 ms (an open note would stay stale until the next tests read)")
	} else {
		t.Logf("raw levels set: %d tests messages %v, the first after %v", len(revs), revs, first.Sub(start).Round(time.Millisecond))
		if len(revs) > 2 {
			t.Errorf("raw levels set: a 40-move fader drag sent %d tests messages, want them coalesced (at most 2)", len(revs))
		}
		rev = revs[len(revs)-1]
	}
	overridden("raw")

	// Raw universe levels cleared.
	if rr := r.do(t, "POST", "/api/dmx/stop", map[string]any{}, nil); rr.Code != http.StatusOK {
		t.Fatalf("POST /api/dmx/stop: %d", rr.Code)
	}
	rev = announced("raw levels cleared", rev)
	overridden("")

	// Universe Identify started, then stopped.
	var arm struct {
		Token string `json:"token"`
	}
	r.post(t, "/api/dmx/identify/arm", map[string]any{"protocol": "artnet", "from": 2, "to": 2}, &arm)
	r.post(t, "/api/dmx/identify/start", map[string]any{"token": arm.Token}, nil)
	rev = announced("Universe Identify started", rev)
	overridden("identify")
	r.post(t, "/api/dmx/identify/stop", map[string]any{"token": arm.Token}, nil)
	rev = announced("Universe Identify stopped", rev)
	overridden("")

	// The same push for the other layers: programmer SET and clear,
	// Highlight, a group fader moved and released.
	r.set(t, map[string]any{"targets": []any{r.target("PAR")}, "attribute": "Dimmer", "dmx": 40})
	rev = announced("programmer SET", rev)
	overridden("programmer")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	rev = announced("Highlight on", rev)
	overridden("highlight")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)
	rev = announced("Highlight off", rev)
	overridden("programmer")
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "selection"}, nil)
	rev = announced("programmer cleared", rev)
	overridden("")
	fid := ""
	for _, f := range r.faders(t).Faders {
		if strings.Contains(f.Label, "LED par") {
			fid = f.ID
		}
	}
	if fid == "" {
		t.Fatalf("no group fader for the LED par: %+v", r.faders(t).Faders)
	}
	r.fade(t, fid, 0.5)
	rev = announced("group fader moved", rev)
	overridden("group-faders")
	r.post(t, "/api/faders/release", map[string]any{"id": fid}, nil)
	announced("group fader released", rev)
	overridden("")
}
