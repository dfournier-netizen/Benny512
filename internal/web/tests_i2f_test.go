package web

import (
	"net/http"
	"testing"
	"time"
)

// Console-lite I2f — the tests view says which layer shows each tested
// channel (per-channel source data, from the output engine's own
// composition), so a masked test can say "overridden by SET on n
// fixtures" (component-specs §16). Rig: newI2eTestsRig — the 4-cell RGB
// batten BT with a master dimmer and the RGB-only RG.

type i2fTestsView struct {
	Tests []struct {
		ID      string
		Entries []struct {
			EntryID  string
			Cell     string
			Applied  bool
			Channels []struct {
				Attribute string
				Address   int
				Virtual   bool
				Source    string
			}
		}
		Overridden []struct {
			Source   string
			Fixtures int
			Cells    int
		}
	}
}

func (r *c4aRig) i2fTests(t *testing.T) i2fTestsView {
	t.Helper()
	var v i2fTestsView
	r.get(t, "/api/tests", &v)
	if len(v.Tests) != 1 {
		t.Fatalf("want one test on, got %+v", v.Tests)
	}
	return v
}

// sourcesOf: entry ID (+ "/" + cell) -> the sources of its tested channels.
func (v i2fTestsView) sourcesOf() map[string][]string {
	out := map[string][]string{}
	for _, e := range v.Tests[0].Entries {
		k := e.EntryID
		if e.Cell != "" {
			k += "/" + e.Cell
		}
		for _, c := range e.Channels {
			out[k] = append(out[k], c.Source)
		}
	}
	return out
}

func TestTestsViewReportsChannelSources(t *testing.T) {
	r := newI2eTestsRig(t)
	bt, rg := r.ids["BT"], r.ids["RG"]
	r.arm(t)
	r.selectNames(t, "BT", "RG")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})

	v := r.i2fTests(t)
	src := v.sourcesOf()
	if got := src[bt]; len(got) != 1 || got[0] != "tests" {
		t.Errorf("BT (its master) sources %v, want [tests]", got)
	}
	if got := src[rg]; len(got) != 1 || got[0] != "tests" {
		t.Errorf("RG (its virtual dimmer) sources %v, want [tests]", got)
	}
	for _, e := range v.Tests[0].Entries {
		for _, c := range e.Channels {
			if e.EntryID == bt && (c.Address != 1 || c.Virtual) {
				t.Errorf("BT tested channel %+v, want the master at address 1", c)
			}
			if e.EntryID == rg && (!c.Virtual || c.Address != 0) {
				t.Errorf("RG tested channel %+v, want its virtual dimmer (address 0)", c)
			}
		}
	}
	if n := len(v.Tests[0].Overridden); n != 0 {
		t.Errorf("nothing overrides the test yet, but overridden = %+v", v.Tests[0].Overridden)
	}

	// A manual Dimmer on both: the programmer (SET) now shows on each.
	r.set(t, map[string]any{"targets": []any{r.target("BT"), r.target("RG")}, "attribute": "Dimmer", "dmx": 40})
	v = r.i2fTests(t)
	src = v.sourcesOf()
	if got := src[bt]; len(got) != 1 || got[0] != "programmer" {
		t.Errorf("BT under a manual dimmer: sources %v, want [programmer]", got)
	}
	if got := src[rg]; len(got) != 1 || got[0] != "programmer" {
		t.Errorf("RG under a manual dimmer: sources %v, want [programmer]", got)
	}
	ov := v.Tests[0].Overridden
	if len(ov) != 1 || ov[0].Source != "programmer" || ov[0].Fixtures != 2 || ov[0].Cells != 0 {
		t.Errorf("overridden = %+v, want programmer on 2 fixtures", ov)
	}

	// Highlight sits above the programmer.
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	ov = r.i2fTests(t).Tests[0].Overridden
	if len(ov) != 1 || ov[0].Source != "highlight" || ov[0].Fixtures != 2 {
		t.Errorf("with Highlight on: overridden = %+v, want highlight on 2 fixtures", ov)
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)

	// Cleared: the test shows again.
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)
	r.selectNames(t, "BT", "RG") // Clear all also empties the selection
	v = r.i2fTests(t)
	if n := len(v.Tests[0].Overridden); n != 0 {
		t.Errorf("after Clear: overridden = %+v, want none", v.Tests[0].Overridden)
	}
	if got := v.sourcesOf()[bt]; len(got) != 1 || got[0] != "tests" {
		t.Errorf("after Clear: BT sources %v, want [tests]", got)
	}
}

// TestIsolateHoldsUntestedChannelsAtZero (I2f, §16; owner 2026-10-09):
// Isolate holds every channel of the tested fixtures that no test drives at
// 0 — the PAR's dimmer the base state opens for a colour test — while a
// tested cell's fixture master stays open (owner: tests open it) and a
// manual value still wins. It is safe with Arm/Disarm: refused while
// disarmed, ended by Disarm (re-Arm comes back NOT isolated), by its browser
// leaving, and with the tests.
func TestIsolateHoldsUntestedChannelsAtZero(t *testing.T) {
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
	bt := r.ids["BT"]
	type isoView struct {
		Isolate struct {
			On          bool
			EndedReason string
		}
	}
	isolate := func() (v isoView) { r.get(t, "/api/tests", &v); return }
	r.arm(t)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{r.target("PAR"), cellTarget(bt, "Pixel 1:0")}}, nil)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "colour_mix_sweep"}}})
	f := r.wire(t, 2)
	wantSlots(t, "no Isolate: the base state opens the PAR's dimmer", f, 301, 255)
	wantSlots(t, "no Isolate: the tested cell's master is open", f, 1, 255)

	if rr := r.do(t, "POST", "/api/tests/isolate", map[string]any{"on": true}, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("Isolate without a client id: %d, want 400 (it must end when its browser leaves)", rr.Code)
	}
	r.testsPost(t, "isolate", map[string]any{"on": true, "client": "laptop"})
	if !isolate().Isolate.On {
		t.Fatalf("the view does not say Isolate is on")
	}
	f = r.wire(t, 2)
	wantSlots(t, "Isolate: the PAR's untested dimmer held at 0", f, 301, 0)
	wantSlots(t, "Isolate: the tested cell's master stays open", f, 1, 255)

	// Disarm ends it; re-Arm is not isolated.
	r.post(t, "/api/output/disarm", map[string]any{}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for isolate().Isolate.EndedReason == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if v := isolate(); v.Isolate.On || v.Isolate.EndedReason != "output was disarmed" {
		t.Errorf("after Disarm: isolate %+v, want off, ended because output was disarmed", v.Isolate)
	}
	if rr := r.do(t, "POST", "/api/tests/isolate", map[string]any{"on": true, "client": "laptop"}, nil); rr.Code != http.StatusPreconditionFailed {
		t.Errorf("Isolate while disarmed: %d, want 412 (never kept for a later Arm)", rr.Code)
	}
	r.arm(t)
	wantSlots(t, "re-armed after Disarm: not isolated", r.wire(t, 2), 301, 255)

	// Its browser leaving ends it (another browser keeps output live).
	r.testsPost(t, "isolate", map[string]any{"on": true, "client": "laptop"})
	wantSlots(t, "isolated again", r.wire(t, 2), 301, 0)
	r.post(t, "/api/output/goodbye", map[string]any{"client": "laptop"}, nil)
	wantSlots(t, "its browser left: not isolated", r.wire(t, 2), 301, 255)
	if isolate().Isolate.On {
		t.Errorf("Isolate still on after its browser left")
	}

	// Manual wins over Isolate; clearing the tests ends it.
	r.testsPost(t, "isolate", map[string]any{"on": true, "client": "laptop"})
	r.set(t, map[string]any{"targets": []any{r.target("PAR")}, "attribute": "Dimmer", "dmx": 77})
	wantSlots(t, "a manual dimmer wins over Isolate", r.wire(t, 2), 301, 77)
	r.testsPost(t, "clear", map[string]any{})
	if isolate().Isolate.On {
		t.Errorf("Isolate still on with every test off")
	}
	if rr := r.do(t, "POST", "/api/tests/isolate", map[string]any{"on": true, "client": "laptop"}, nil); rr.Code != http.StatusConflict {
		t.Errorf("Isolate with no test on: %d, want 409", rr.Code)
	}
}
