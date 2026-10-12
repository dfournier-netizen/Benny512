package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
)

// Console-lite C5 — Rig Check folded in as the TESTS layer plus test
// sequences — proven at the HTTP surface: the real BMFL / LEDBeam / Paladin
// profiles parsed by the literal gdtfparse.js and imported through
// POST /api/patch/import (newC4aRig), the real handlers, and every level read
// back as ArtDmx decoded at session.FakeTransport under FakeClock.
//
// Slot facts used below (C4a's TestProgrammerBaseDefaultsOnWireAfterArm pins
// them): BMFL at 1 — Pan 1-2, Tilt 3-4, Shutter1 39 (default 32), Dimmer
// 40-41 (default 0); BMFL B2 at 42 — Pan 42-43 (default 128,0), Shutter1 80.
// Paladin P1 at 101 on universe 1 — three RGBW cells, 16-bit: cell 1 at
// 101-108, cell 2 ("Beam 2:0") at 109-116, cell 3 at 117-124; R/G/B/W
// default 0. LEDBeam L1 at 1 on universe 1 — Dimmer 14 (default 0).

type c5Target struct {
	EntryID string
	Cell    string
	Name    string
}

type c5Tests struct {
	Revision uint64
	Scope    struct{ Kind, Group, Layer string }
	Targets  []c5Target
	Note     string
	Output   struct {
		State string
		Live  bool
	}
	OutputEnabled bool
	FadeMs        int64
	Available     []struct {
		ID           string
		Kind         string
		FixtureCount int
	}
	Tests []struct {
		ID      string
		Entries []struct {
			EntryID      string
			Cell         string
			Applied      bool
			Virtual      bool
			PhaseDegrees float64
		}
	}
	Sequences []struct {
		ID    string
		Name  string
		Steps []json.RawMessage
	}
	Run struct {
		Active       bool
		SequenceID   string
		SequenceName string
		Step         int
		StepCount    int
		StepName     string
		Paused       bool
		RemainingMs  int64
		EndedReason  string
	}
}

func (r *c4aRig) tests(t *testing.T) c5Tests {
	t.Helper()
	rr := r.do(t, "GET", "/api/tests", nil, nil)
	var v c5Tests
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &v) != nil {
		t.Fatalf("GET /api/tests: %d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if h := rr.Header().Get("X-Benny-Tests"); h != strconv.FormatUint(v.Revision, 10) {
		t.Fatalf("X-Benny-Tests header %q does not carry revision %d", h, v.Revision)
	}
	return v
}

func (r *c4aRig) testsPost(t *testing.T, action string, body any) c5Tests {
	t.Helper()
	var v c5Tests
	r.post(t, "/api/tests/"+action, body, &v)
	return v
}

// frames returns every ArtDmx frame for universe u since the last take.
func (r *c4aRig) frames(t *testing.T, u uint16) [][]byte {
	t.Helper()
	pa, _ := artnet.PortAddressFromRaw(u)
	var out [][]byte
	for _, sp := range r.h.tport.TakeSent() {
		if sp.DecodeErr == nil && sp.Packet.Kind == artnet.KindDmx && sp.Packet.Dmx.Net == pa.Net && sp.Packet.Dmx.SubUni == pa.SubUni() {
			out = append(out, append([]byte(nil), sp.Packet.Dmx.Data...))
		}
	}
	return out
}

func lastFrame(t *testing.T, what string, fs [][]byte) []byte {
	t.Helper()
	if len(fs) == 0 {
		t.Fatalf("%s: no ArtDmx frame on the wire", what)
	}
	return fs[len(fs)-1]
}

// advance moves the fake clock in steps under the 5 s Arm lease, with a
// heartbeat between them, as a connected browser would.
func (r *c4aRig) advance(t *testing.T, d time.Duration) {
	t.Helper()
	for d > 0 {
		step := d
		if step > time.Second {
			step = time.Second
		}
		r.post(t, "/api/output/heartbeat", map[string]any{"client": "c4a"}, nil)
		r.h.clock.Advance(step)
		d -= step
	}
}

func (r *c4aRig) cellTarget(name, cell string) map[string]any {
	return map[string]any{"entryId": r.ids[name], "cell": cell}
}

// TestTestsOnProgrammerSelection: with no scope given, a test acts on the
// programmer selection — the selected BMFLs' dimmers go full, nothing else
// does — and the phase spread follows SELECTION order (B2 first), not patch
// address order.
func TestTestsOnProgrammerSelection(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B2", "B1")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	if v.Scope.Kind != "selection" || len(v.Targets) != 2 || v.Targets[0].EntryID != r.ids["B2"] || v.Targets[1].EntryID != r.ids["B1"] {
		t.Fatalf("scope/targets = %+v %+v, want the programmer selection B2, B1", v.Scope, v.Targets)
	}
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 dimmer full", u0, 40, 255, 255)
	wantSlots(t, "B2 dimmer full", u0, 81, 255, 255)
	wantSlots(t, "L1 dimmer stays at its base default", r.wire(t, 1), 14, 0)
	found := false
	for _, a := range v.Available {
		if a.Kind == "dimmer_toggle" {
			found = a.FixtureCount == 2
		}
	}
	if !found {
		t.Errorf("available should offer dimmer_toggle over 2 fixtures: %+v", v.Available)
	}

	v = r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_sine", "offsetMin": 0, "offsetMax": 360}}})
	if len(v.Tests) != 1 || len(v.Tests[0].Entries) != 2 {
		t.Fatalf("tests = %+v", v.Tests)
	}
	phase := map[string]float64{}
	for _, e := range v.Tests[0].Entries {
		phase[e.EntryID] = e.PhaseDegrees
	}
	if phase[r.ids["B2"]] != 0 || phase[r.ids["B1"]] != 180 {
		t.Errorf("phase B2=%v B1=%v, want 0 and 180 (selection order, B2 selected first)", phase[r.ids["B2"]], phase[r.ids["B1"]])
	}
}

// TestTestsCellScope: a test addressed to one cell drives only that cell's
// channels.
func TestTestsCellScope(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{r.cellTarget("P1", "Beam 2:0")}}, nil)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "colour_mix_sweep", "waveform": "snap", "rateHz": 0.25}}})
	if len(v.Targets) != 1 || v.Targets[0].Cell != "Beam 2:0" {
		t.Fatalf("targets = %+v, want the one cell", v.Targets)
	}
	if len(v.Tests) != 1 || len(v.Tests[0].Entries) != 1 || v.Tests[0].Entries[0].Cell != "Beam 2:0" || !v.Tests[0].Entries[0].Applied {
		t.Fatalf("test status should name the cell: %+v", v.Tests)
	}
	u1 := r.wire(t, 1)
	wantSlots(t, "cell 2 R G B full", u1, 109, 255, 255, 255, 255, 255, 255)
	wantSlots(t, "cell 1 untouched", u1, 101, 0, 0, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "cell 3 untouched", u1, 117, 0, 0, 0, 0, 0, 0, 0, 0)
}

// TestTestsUnscopedFixtureShowsBaseDefault: the tests claim only the
// channels of the fixtures they test. B2 shares universe 0 with the tested
// B1 but is not in scope, so it shows its profile defaults (base source)
// instead of the tests' zeros — and the other way round. (C7: the first half
// ran through the retired /api/patch/rigcheck/pattern/tests.)
func TestTestsUnscopedFixtureShowsBaseDefault(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.selectNames(t, "B1")
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	u0 := r.wire(t, 0)
	wantSlots(t, "tested B1 dimmer full", u0, 40, 255, 255)
	wantSlots(t, "untested B2 Pan at its default", u0, 42, 128, 0)
	wantSlots(t, "untested B2 Tilt at its default", u0, 44, 128, 0)
	wantSlots(t, "untested B2 Shutter1 at its default", u0, 80, 32)

	// And with the selection moved to B2.
	r.selectNames(t, "B2")
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	u0 = r.wire(t, 0)
	wantSlots(t, "tested B2 dimmer full", u0, 81, 255, 255)
	wantSlots(t, "untested B1 Pan at its default", u0, 1, 128, 0)
	wantSlots(t, "untested B1 dimmer at its default", u0, 40, 0, 0)
}

// TestTestsManualValueWinsThenTestResumes: a programmer value on a channel
// wins over the running test there; clearing it shows the test again.
func TestTestsManualValueWinsThenTestResumes(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	wantSlots(t, "test drives the dimmer", r.wire(t, 0), 40, 255, 255)
	r.set(t, map[string]any{"attribute": "Dimmer", "dmx": 1000})
	u0 := r.wire(t, 0)
	wantSlots(t, "the programmer value wins", u0, 40, 3, 232)
	wantSlots(t, "the test still opens the shutter", u0, 39, 32)
	// Clear the selection's values (clear "all" also empties the selection,
	// which the tests would follow and release).
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "selection"}, nil)
	wantSlots(t, "the test shows again after clear", r.wire(t, 0), 40, 255, 255)
}

// TestTestsDisarmedSendsNothing: tests are configured and run, but nothing
// reaches the wire until the master Arm.
func TestTestsDisarmedSendsNothing(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	if !v.OutputEnabled || v.Output.Live {
		t.Errorf("tests should render (outputEnabled) but not be live while disarmed: %+v %+v", v.OutputEnabled, v.Output)
	}
	r.h.tport.TakeSent()
	r.h.clock.Advance(500 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Fatalf("%d ArtDmx frames while disarmed", n)
	}
	r.arm(t)
	wantSlots(t, "after Arm the test is on the wire", r.wire(t, 0), 40, 255, 255)
}

func c5Step(name string, test map[string]any, advance map[string]any) map[string]any {
	return map[string]any{"name": name, "tests": []any{test}, "scope": map[string]any{"kind": "selection"}, "fadeMs": 0, "advance": advance}
}

func c5ThreeSteps() []any {
	return []any{
		c5Step("Dimmer check", map[string]any{"kind": "dimmer_toggle", "on": true}, map[string]any{"mode": "manual"}),
		c5Step("Tilt max", map[string]any{"kind": "move_extreme", "target": "tilt_max"}, map[string]any{"mode": "auto", "seconds": 2}),
		c5Step("Pan max", map[string]any{"kind": "move_extreme", "target": "pan_max"}, map[string]any{"mode": "auto", "seconds": 1.5}),
	}
}

// TestTestSequenceSaveRunNextBackAuto: a saved three-step sequence run with
// Next / Back / Pause / Resume and auto-advance, with exact step boundaries
// under the fake clock and every step read back from the wire.
func TestTestSequenceSaveRunNextBackAuto(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	v := r.testsPost(t, "sequence-save", map[string]any{"name": "Mover check", "steps": c5ThreeSteps()})
	if len(v.Sequences) != 1 || v.Sequences[0].Name != "Mover check" || len(v.Sequences[0].Steps) != 3 {
		t.Fatalf("sequences = %+v", v.Sequences)
	}
	id := v.Sequences[0].ID
	// Stored in the show's workspace beside groups and presets.
	p, _ := r.h.srv.PatchStore.Get()
	if !bytes.Contains(p.Workspace, []byte(`"testSequences"`)) || !bytes.Contains(p.Workspace, []byte("Mover check")) {
		t.Fatalf("the sequence is not in the workspace JSON: %s", p.Workspace)
	}

	rev := v.Revision
	step := func(want int, what string) c5Tests {
		t.Helper()
		v := r.tests(t)
		if !v.Run.Active || v.Run.Step != want {
			t.Fatalf("%s: run = %+v, want active at step %d", what, v.Run, want)
		}
		if v.Revision <= rev {
			t.Fatalf("%s: revision %d did not advance past %d", what, v.Revision, rev)
		}
		rev = v.Revision
		return v
	}
	r.testsPost(t, "run-start", map[string]any{"id": id})
	if v := step(0, "start"); v.Run.StepName != "Dimmer check" || v.Run.StepCount != 3 || v.Run.SequenceID != id {
		t.Fatalf("run = %+v", v.Run)
	}
	wantSlots(t, "step 1 dimmer full", r.wire(t, 0), 40, 255, 255)

	r.testsPost(t, "run-next", nil)
	step(1, "next")
	u0 := r.wire(t, 0) // 30 ms into step 2
	wantSlots(t, "step 2 tilt max", u0, 3, 255, 255)
	wantSlots(t, "step 2 pan at its default", u0, 1, 128, 0)

	r.h.tport.TakeSent()
	r.advance(t, 2000*time.Millisecond-30*time.Millisecond-time.Millisecond)
	if v := r.tests(t); v.Run.Step != 1 || v.Run.RemainingMs != 1 {
		t.Fatalf("1 ms before the boundary: run = %+v, want step 1 with 1 ms remaining", v.Run)
	}
	wantSlots(t, "still step 2 just before the boundary", lastFrame(t, "before boundary", r.frames(t, 0)), 3, 255, 255)
	r.h.clock.Advance(time.Millisecond)
	step(2, "auto-advance at exactly 2 s")
	f := lastFrame(t, "at boundary", r.frames(t, 0))
	wantSlots(t, "step 3 pan max at the boundary", f, 1, 255, 255)
	wantSlots(t, "step 3 tilt back at its default", f, 3, 128, 0)

	r.testsPost(t, "run-back", nil)
	step(1, "back")
	r.testsPost(t, "run-pause", nil)
	if v := r.tests(t); !v.Run.Paused || v.Run.RemainingMs != 2000 {
		t.Fatalf("paused: %+v, want 2000 ms remaining", v.Run)
	}
	rev = r.tests(t).Revision
	r.advance(t, 5*time.Second)
	if v := r.tests(t); v.Run.Step != 1 || !v.Run.Paused {
		t.Fatalf("paused run advanced: %+v", v.Run)
	}
	r.testsPost(t, "run-resume", nil)
	step(1, "resume")
	r.advance(t, 2*time.Second)
	step(2, "auto-advance after resume")
	r.advance(t, 1500*time.Millisecond)
	v = r.tests(t)
	if v.Run.Active || v.Run.EndedReason != "finished" {
		t.Fatalf("after the last auto step the run should end: %+v", v.Run)
	}
	u0 = r.wire(t, 0)
	wantSlots(t, "finished: B1 back at its base pan", u0, 1, 128, 0)
	wantSlots(t, "finished: B1 back at its base dimmer", u0, 40, 0, 0)

	// Jump, and Next / Back clamp at the ends.
	r.testsPost(t, "run-start", map[string]any{"id": id})
	r.testsPost(t, "run-jump", map[string]any{"step": 2})
	if v := r.tests(t); v.Run.Step != 2 {
		t.Fatalf("jump: %+v", v.Run)
	}
	r.testsPost(t, "run-pause", nil)
	r.testsPost(t, "run-next", nil)
	if v := r.tests(t); v.Run.Step != 2 || !v.Run.Active {
		t.Fatalf("next on the last step should stay there: %+v", v.Run)
	}
	rr := r.do(t, "POST", "/api/tests/run-jump", map[string]any{"step": 3}, nil)
	if rr.Code != http.StatusBadRequest || !strings.HasSuffix(strings.TrimSpace(rr.Body.String()), `."}`) {
		t.Errorf("jump past the end: %d %s, want 400 with a finished sentence", rr.Code, rr.Body.String())
	}
	r.testsPost(t, "run-stop", nil)
	if v := r.tests(t); v.Run.Active || v.OutputEnabled {
		t.Errorf("stop should end the run and release the tests layer: %+v outputEnabled=%v", v.Run, v.OutputEnabled)
	}
	wantSlots(t, "stopped: base dimmer", r.wire(t, 0), 40, 0, 0)
}

// TestTestSequenceStepSwapIsAtomic: changing step never puts a frame on the
// wire in which the tested fixture falls back to the base (dimmer 0): the
// tests layer is swapped in one step, never released and re-claimed.
func TestTestSequenceStepSwapIsAtomic(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	v := r.testsPost(t, "sequence-save", map[string]any{"name": "Swap", "steps": c5ThreeSteps()})
	r.testsPost(t, "run-start", map[string]any{"id": v.Sequences[0].ID})
	r.h.clock.Advance(100 * time.Millisecond)
	r.h.tport.TakeSent()
	r.testsPost(t, "run-next", nil)            // dimmer test -> tilt max (dimmer held full by the tests layer)
	r.h.clock.Advance(2050 * time.Millisecond) // auto-advance to pan max inside this window
	fs := r.frames(t, 0)
	if len(fs) < 10 {
		t.Fatalf("only %d frames captured", len(fs))
	}
	for i, f := range fs {
		if f[39] != 255 || f[40] != 255 {
			t.Fatalf("frame %d of %d during the step swaps has B1 dimmer %d,%d — a gap where the tests layer let go", i+1, len(fs), f[39], f[40])
		}
	}
	if v := r.tests(t); v.Run.Step != 2 {
		t.Fatalf("expected to have auto-advanced to step 3: %+v", v.Run)
	}
}

// TestTestSequenceStorageRules: rename, delete, limits and refusals answer
// in finished sentences; a sequence lives in the workspace and so shares its
// preceding-save recovery.
func TestTestSequenceStorageRules(t *testing.T) {
	dir := t.TempDir()
	r := newC4aRigWith(t, func(h *testHarness) { h.srv.SetPatchStorePath(filepath.Join(dir, "show.json")) })
	sentence := func(rr *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		var e struct{ Error string }
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if rr.Code != code || !strings.HasSuffix(e.Error, ".") {
			t.Errorf("%s: %d %q, want %d with a finished sentence", what, rr.Code, e.Error, code)
		}
	}
	sentence(r.do(t, "POST", "/api/tests/sequence-save", map[string]any{"name": "", "steps": c5ThreeSteps()}, nil), 400, "empty name")
	sentence(r.do(t, "POST", "/api/tests/sequence-save", map[string]any{"name": "X", "steps": []any{}}, nil), 400, "no steps")
	sentence(r.do(t, "POST", "/api/tests/sequence-save", map[string]any{"name": "X", "steps": []any{
		c5Step("bad", map[string]any{"kind": "no_such_test"}, map[string]any{"mode": "manual"})}}, nil), 400, "unknown test kind")
	sentence(r.do(t, "POST", "/api/tests/sequence-save", map[string]any{"name": "X", "steps": []any{
		c5Step("bad", map[string]any{"kind": "dimmer_toggle"}, map[string]any{"mode": "auto", "seconds": 0})}}, nil), 400, "auto with 0 s")
	sentence(r.do(t, "POST", "/api/tests/sequence-save", map[string]any{"name": "X", "steps": []any{
		map[string]any{"name": "g", "tests": []any{map[string]any{"kind": "dimmer_toggle"}}, "scope": map[string]any{"kind": "group", "group": "nope"}, "advance": map[string]any{"mode": "manual"}}}}, nil), 400, "unknown group")

	a := r.testsPost(t, "sequence-save", map[string]any{"name": "A", "steps": c5ThreeSteps()})
	idA := a.Sequences[0].ID
	b := r.testsPost(t, "sequence-save", map[string]any{"name": "B", "steps": c5ThreeSteps()})
	if len(b.Sequences) != 2 {
		t.Fatalf("sequences = %+v", b.Sequences)
	}
	v := r.testsPost(t, "sequence-rename", map[string]any{"id": idA, "name": "A2"})
	if v.Sequences[0].Name != "A2" {
		t.Errorf("rename: %+v", v.Sequences)
	}
	r.selectNames(t, "B1")
	r.testsPost(t, "run-start", map[string]any{"id": idA})
	sentence(r.do(t, "POST", "/api/tests/sequence-delete", map[string]any{"id": idA}, nil), 409, "delete the running sequence")
	sentence(r.do(t, "POST", "/api/tests/set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle"}}}, nil), 409, "ad-hoc set while a sequence runs")
	r.testsPost(t, "run-stop", nil)
	v = r.testsPost(t, "sequence-delete", map[string]any{"id": idA})
	if len(v.Sequences) != 1 || v.Sequences[0].Name != "B" {
		t.Errorf("delete: %+v", v.Sequences)
	}
	sentence(r.do(t, "POST", "/api/tests/sequence-delete", map[string]any{"id": idA}, nil), 404, "delete twice")
	// Recover restores the preceding save, sequences included: A2 is back.
	r.post(t, "/api/patch/recover", map[string]any{"confirm": "RECOVER"}, nil)
	if v := r.tests(t); len(v.Sequences) != 2 {
		t.Errorf("after Recover the preceding save's sequences should be back: %+v", v.Sequences)
	}
	// A stale show token is refused like every show-bound write.
	rr := r.do(t, "POST", "/api/tests/clear", nil, map[string]string{"X-Benny-Show": "1"})
	if rr.Code != http.StatusConflict {
		t.Errorf("stale show token: %d %s", rr.Code, rr.Body.String())
	}
	// A stale tests revision is refused.
	rr = r.do(t, "POST", "/api/tests/clear", nil, map[string]string{"X-Benny-Tests": "1"})
	if rr.Code != http.StatusConflict {
		t.Errorf("stale tests revision: %d %s", rr.Code, rr.Body.String())
	}
}

// TestTestSequenceGroupScopeAndSelectionFollow: a step scoped to a stored
// group acts on the group whatever is selected; an ad-hoc test on the
// programmer selection follows the selection live.
func TestTestSequenceGroupScopeAndSelectionFollow(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "L1")
	var pv struct{ StoredGroups []struct{ ID string } }
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Beams"}, &pv)
	gid := pv.StoredGroups[0].ID
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	v := r.testsPost(t, "sequence-save", map[string]any{"name": "G", "steps": []any{
		map[string]any{"name": "Beams dimmer", "tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}, "scope": map[string]any{"kind": "group", "group": gid}, "fadeMs": 0, "advance": map[string]any{"mode": "manual"}},
	}})
	r.selectNames(t, "B1")
	r.testsPost(t, "run-start", map[string]any{"id": v.Sequences[0].ID})
	wantSlots(t, "group member L1 dimmer full", r.wire(t, 1), 14, 255)
	wantSlots(t, "selected-but-not-in-group B1 at base", r.wire(t, 0), 40, 0, 0)
	r.testsPost(t, "run-stop", nil)

	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true}}})
	wantSlots(t, "ad-hoc on B1", r.wire(t, 0), 40, 255, 255)
	before := r.tests(t).Revision
	r.selectNames(t, "B2")
	v = r.tests(t)
	if v.Revision == before || len(v.Targets) != 1 || v.Targets[0].EntryID != r.ids["B2"] {
		t.Fatalf("the tests should follow the new selection: rev %d->%d targets %+v", before, v.Revision, v.Targets)
	}
	u0 := r.wire(t, 0)
	wantSlots(t, "B2 now tested", u0, 81, 255, 255)
	wantSlots(t, "B1 released to base", u0, 40, 0, 0)
	r.selectNames(t)
	if v := r.tests(t); v.OutputEnabled || v.Note == "" {
		t.Errorf("with nothing selected the layer is released and says why: outputEnabled=%v note=%q", v.OutputEnabled, v.Note)
	}
	wantSlots(t, "nothing selected: B2 at base", r.wire(t, 0), 81, 0, 0)
}

// TestTestsRunStateAcrossBrowsers runs the literal ws.js as two browsers
// against a real HTTP + WebSocket server: browser B learns of browser A's
// run start and Next through the "tests" broadcast alone and reads the same
// revision and step.
func TestTestsRunStateAcrossBrowsers(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newC4aRig(t)
	r.selectNames(t, "B1")
	v := r.testsPost(t, "sequence-save", map[string]any{"name": "Two", "steps": c5ThreeSteps()})
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "tests_sync_test.js"), jsDir, ts.URL, v.Sequences[0].ID).CombinedOutput()
	if err != nil {
		t.Fatalf("tests_sync_test.js: %v\n%s", err, out)
	}
	var res struct {
		ARevision uint64 `json:"aRevision"`
		BRevision uint64 `json:"bRevision"`
		BSeen     uint64 `json:"bSeen"`
		BStep     int    `json:"bStep"`
		BActive   bool   `json:"bActive"`
		Stale     int    `json:"stale"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.BSeen != res.ARevision || res.BRevision != res.ARevision || res.BStep != 1 || !res.BActive {
		t.Errorf("browser B did not follow A's run: %+v", res)
	}
	if res.Stale != http.StatusConflict {
		t.Errorf("a write with A's old revision: %d, want 409", res.Stale)
	}
	_ = fmt.Sprint()
}
