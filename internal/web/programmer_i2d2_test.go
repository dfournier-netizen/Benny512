package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"benny512/internal/session"
)

// Console-lite I2d2 — fixture commands are one-shot (owner decision
// 2026-10-09). Proven at the HTTP surface on the real BMFL Spot profile
// (its Control1 channel carries "Total reset", "Lamp Off" and the
// persistent settings such as "Dimmer curve: Linear"), reading every level
// back as ArtDmx at the fake transport under the fake clock.

// control1 is the 1-based frame slot of an entry's Control1 channel, and
// the channel's profile default.
func (r *c4aRig) control1(t *testing.T, name string, start int) (int, uint32) {
	t.Helper()
	r.selectNames(t, name)
	a := r.view(t).attr("Control1")
	if a == nil || len(a.Channels) != 1 {
		t.Fatalf("%s has no single Control1 channel: %+v", name, a)
	}
	return start + a.Channels[0].Offset - 1, a.Channels[0].Default
}

// heartbeatFor keeps the lease alive while the fake clock runs d.
func (r *c4aRig) heartbeatFor(t *testing.T, d time.Duration) {
	t.Helper()
	for step := time.Second; d > 0; d -= step {
		if d < step {
			step = d
		}
		r.post(t, "/api/output/heartbeat", map[string]any{"client": "c4a"}, nil)
		r.h.clock.Advance(step)
	}
}

func (r *c4aRig) command(t *testing.T, body map[string]any) *c4aSetResult {
	t.Helper()
	rr := r.do(t, "POST", "/api/programmer/command", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/programmer/command %v: %d %s", body, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	var res c4aSetResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil || len(res.Applied) == 0 {
		t.Fatalf("command response %s: %v", rr.Body.String(), err)
	}
	return &res
}

// TestCommandIsOneShot: a Reset goes out for the command window and the
// channel then returns to what it was — B1 to its programmer value, B2
// (never touched) to its profile default. The programmer never holds it.
func TestCommandIsOneShot(t *testing.T) {
	r := newC4aRig(t)
	// I2d3: /set refuses Control values and raw refuses profiled offsets,
	// so B1's Control1 is at its default here; the return to a programmer
	// value is proven at the engine (TestFireCommandReturnsToTheSourceBelow).
	s1, linear := r.control1(t, "B1", 1)
	s2, def2 := r.control1(t, "B2", 42)
	r.selectNames(t, "B1", "B2")
	rev := r.view(t).Revision
	r.arm(t)
	f := r.wire(t, 0)
	if uint32(f[s1-1]) != linear || uint32(f[s2-1]) != def2 {
		t.Fatalf("before: B1 %d B2 %d, want %d and %d", f[s1-1], f[s2-1], linear, def2)
	}

	res := r.command(t, map[string]any{"attribute": "Control1", "set": "Total reset"})
	reset := res.Applied[0].Value
	f = r.wire(t, 0)
	if uint32(f[s1-1]) != reset || uint32(f[s2-1]) != reset {
		t.Errorf("during the command: B1 %d B2 %d, want both %d (Total reset)", f[s1-1], f[s2-1], reset)
	}
	v := r.view(t)
	if v.Revision != rev {
		t.Errorf("a command changed the programmer revision %d -> %d", rev, v.Revision)
	}
	for _, c := range v.attr("Control1").Channels {
		if c.Value == reset {
			t.Errorf("the programmer holds the Reset value on %s: %+v", c.EntryID, c)
		}
	}

	// I2d3 (owner 2026-10-09): the window is 1 s. r.wire has already run
	// 30 ms of it.
	r.heartbeatFor(t, 900*time.Millisecond)
	if f = r.wire(t, 0); uint32(f[s1-1]) != reset {
		t.Errorf("0.96 s in: B1 %d, want the command still on the wire (%d)", f[s1-1], reset)
	}
	r.heartbeatFor(t, 100*time.Millisecond)
	f = r.wire(t, 0)
	if uint32(f[s1-1]) != linear || uint32(f[s2-1]) != def2 {
		t.Errorf("after the window: B1 %d B2 %d, want B1 back at its previous value %d and B2 at its default %d", f[s1-1], f[s2-1], linear, def2)
	}
	// Re-arming must not fire it again.
	r.post(t, "/api/output/disarm", map[string]any{"client": "c4a"}, nil)
	r.arm(t)
	if f = r.wire(t, 0); uint32(f[s1-1]) == reset || uint32(f[s2-1]) == reset {
		t.Errorf("Arm fired the Reset again: B1 %d B2 %d", f[s1-1], f[s2-1])
	}
}

// TestCommandNotQueuedWhileDisarmed: disarmed, a command is refused (412,
// saying so) and nothing fires at the next Arm; Disarm during the window
// drops it too.
func TestCommandNotQueuedWhileDisarmed(t *testing.T) {
	r := newC4aRig(t)
	s1, def1 := r.control1(t, "B1", 1)
	rr := r.do(t, "POST", "/api/programmer/command", map[string]any{"attribute": "Control1", "set": "Lamp Off"}, nil)
	if rr.Code != http.StatusPreconditionFailed || !strings.Contains(rr.Body.String(), "not live") {
		t.Errorf("disarmed command: %d %s, want 412 saying output is not live", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	r.arm(t)
	if f := r.wire(t, 0); uint32(f[s1-1]) != def1 {
		t.Errorf("Arm after a disarmed Lamp Off: B1 Control1 %d, want its default %d (nothing queued)", f[s1-1], def1)
	}
	res := r.command(t, map[string]any{"attribute": "Control1", "set": "Lamp Off"})
	if f := r.wire(t, 0); uint32(f[s1-1]) != res.Applied[0].Value {
		t.Fatalf("armed Lamp Off not on the wire: %d", f[s1-1])
	}
	r.post(t, "/api/output/disarm", map[string]any{"client": "c4a"}, nil)
	r.arm(t)
	if f := r.wire(t, 0); uint32(f[s1-1]) != def1 {
		t.Errorf("Disarm then Arm inside the window: B1 Control1 %d, want its default %d (Disarm drops the command)", f[s1-1], def1)
	}
}

// TestLowlightOnlyNonHighlightedSelection (I2d2, component-specs §15, owner
// 2026-10-09): Lowlight dims only the selected fixtures that are not
// highlighted. A fixture outside the selection is never dimmed ("dimming
// everything but the selection would be solo"); with the whole selection
// highlighted nothing is lowlit; stepping lowlights the rest of the
// selection, the Paladin through its virtual dimmers, and reports the
// unprofiled G1 as having no dimmer.
func TestLowlightOnlyNonHighlightedSelection(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.set(t, map[string]any{"targets": []any{r.target("B1"), r.target("B2")}, "attribute": "Dimmer", "dmx": 50000})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "Dimmer", "dmx": 200})
	r.selectNames(t, "B1", "L1", "P1", "G1")
	var v c4bView
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, &v)
	f := r.wire(t, 0)
	wantSlots(t, "B2 is not selected: never lowlit (50000)", f, 81, 0xC3, 0x50)
	if v.Highlight.Lowlit != 0 {
		t.Errorf("whole selection highlighted: lowlit %d, want 0", v.Highlight.Lowlit)
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	f, f1 := r.wire(t, 0), r.wire(t, 1)
	wantSlots(t, "step to B1: B1 highlighted", f, 40, 255, 255)
	wantSlots(t, "step to B1: B2 still not lowlit", f, 81, 0xC3, 0x50)
	wantSlots(t, "step to B1: L1 (selected, not highlighted) lowlit 200 -> 40", f1, 14, 40)
	if v.Highlight.Lowlit != 2 || strings.Join(v.Highlight.NoDimmer, ",") != r.ids["G1"] {
		t.Errorf("step to B1: lowlit %d (want 2: L1, P1), noDimmer %v (want G1 only)", v.Highlight.Lowlit, v.Highlight.NoDimmer)
	}
}

// cmdView is GET /api/programmer's commands block (I2d2): the one-shot
// commands on the wire now, and the last one that ended.
type cmdView struct {
	Commands struct {
		Active []cmdOne
		Last   *cmdOne
	}
}

type cmdOne struct {
	ID          uint64
	Name        string
	Fixtures    []string
	WindowMs    int64
	RemainingMs int64
	State       string
}

// commandsWhen re-reads until ok holds or 2 s pass (a command dropped by
// Disarm or lease loss reports its end on its own goroutine).
func (r *c4aRig) commandsWhen(t *testing.T, ok func(cmdView) bool) cmdView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		v := r.commands(t)
		if ok(v) || time.Now().After(deadline) {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *c4aRig) commands(t *testing.T) cmdView {
	t.Helper()
	rr := r.do(t, "GET", "/api/programmer", nil, nil)
	var v cmdView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &v) != nil {
		t.Fatalf("GET /api/programmer: %d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return v
}

// TestCommandProgressIsServerState (I2d2, owner 2026-10-09: "the UI shows
// that the command is in progress and when it is done", the same in every
// browser): the programmer read model carries the commands in flight with
// their names, fixtures and time left, and the last one that ended with how
// it ended — "done" when its window ran out, "stopped" when Disarm cut it
// short. The engine's timer decides both.
func TestCommandProgressIsServerState(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1", "B2")
	if v := r.commands(t); len(v.Commands.Active) != 0 || v.Commands.Last != nil {
		t.Fatalf("before any command: %+v, want nothing in flight and no last", v.Commands)
	}
	r.arm(t)
	r.command(t, map[string]any{"attribute": "Control1", "set": "Total reset"})
	v := r.commands(t)
	if len(v.Commands.Active) != 1 {
		t.Fatalf("in flight: %+v, want one command", v.Commands)
	}
	a := v.Commands.Active[0]
	if a.Name != "Total reset" || strings.Join(a.Fixtures, ",") != "B1,B2" || a.State != "sending" || a.WindowMs != 1000 || a.RemainingMs <= 900 || a.RemainingMs > 1000 {
		t.Errorf("in flight: %+v, want Total reset on B1,B2, sending, 1000 ms window, about 1000 ms left", a)
	}
	r.heartbeatFor(t, 600*time.Millisecond)
	if v = r.commands(t); len(v.Commands.Active) != 1 || v.Commands.Active[0].RemainingMs > 400 {
		t.Errorf("0.6 s in: %+v, want it still in flight with about 0.4 s left", v.Commands)
	}
	r.heartbeatFor(t, 500*time.Millisecond)
	v = r.commands(t)
	if len(v.Commands.Active) != 0 || v.Commands.Last == nil || v.Commands.Last.State != "done" || v.Commands.Last.Name != "Total reset" {
		t.Errorf("after the window: %+v, want nothing in flight and the last one done", v.Commands)
	}

	r.command(t, map[string]any{"attribute": "Control1", "set": "Lamp Off"})
	r.post(t, "/api/output/disarm", map[string]any{"client": "c4a"}, nil)
	v = r.commandsWhen(t, func(v cmdView) bool { return len(v.Commands.Active) == 0 })
	if len(v.Commands.Active) != 0 || v.Commands.Last == nil || v.Commands.Last.State != "stopped" || v.Commands.Last.Name != "Lamp Off" {
		t.Errorf("Disarm inside the window: %+v, want nothing in flight and Lamp Off stopped", v.Commands)
	}
}

// TestCommandNotFrozenInHeldLook (I2d2): a lease lost with "Hold last look"
// inside a command's window must not freeze the command into the held look
// — that would keep a Reset or Lamp off on the wire indefinitely, the very
// thing one-shot forbids. Each command channel holds what it returns to.
func TestCommandNotFrozenInHeldLook(t *testing.T) {
	r := newC4aRig(t)
	s1, linear := r.control1(t, "B1", 1) // its default (see TestCommandIsOneShot)
	r.h.srv.DMX.SetLeaseLossAction(session.LeaseLossHold)
	r.arm(t)
	r.h.clock.Advance(4500 * time.Millisecond) // the last heartbeat was the Arm
	reset := r.command(t, map[string]any{"attribute": "Control1", "set": "Total reset"}).Applied[0].Value
	if f := r.wire(t, 0); uint32(f[s1-1]) != reset {
		t.Fatalf("command not on the wire: %d, want %d", f[s1-1], reset)
	}
	r.h.clock.Advance(500 * time.Millisecond) // the lease is lost about 0.5 s into the 1 s window
	if st := r.h.srv.DMX.State(); st != session.StateHolding {
		t.Fatalf("lease lost with Hold: %q, want holding", st)
	}
	if f := r.wire(t, 0); uint32(f[s1-1]) != linear {
		t.Errorf("held look: B1 Control1 %d, want %d (its previous value; the Reset %d is not held)", f[s1-1], linear, reset)
	}
	if v := r.commandsWhen(t, func(v cmdView) bool { return v.Commands.Last != nil }); len(v.Commands.Active) != 0 || v.Commands.Last == nil || v.Commands.Last.State != "stopped" {
		t.Errorf("lease lost: %+v, want the command stopped", v.Commands)
	}
}

// TestSetRefusesCommandValues (I2d3, owner 2026-10-09): /api/programmer/set
// keeps values, so it refuses every Control command value — the same rule
// the one-shot path and the UI use: any function on a Control-family
// channel (taxonomy group "other"), and any Reset or Lamp function on any
// channel. The refusal is a 422 that names /api/programmer/command; nothing
// is stored and the revision does not move. Fan, which also stores values,
// refuses the same way. The view marks each function "command".
func TestSetRefusesCommandValues(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1", "B2")
	rev := r.view(t).Revision
	for _, body := range []map[string]any{
		{"attribute": "Control1", "set": "Total reset"},
		{"attribute": "Control1", "set": "Lamp Off"},
		{"attribute": "Control1", "set": "Dimmer curve: Linear"},
	} {
		rr := r.do(t, "POST", "/api/programmer/set", body, nil)
		if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "/api/programmer/command") {
			t.Errorf("/set %v: %d %s, want 422 naming /api/programmer/command", body, rr.Code, strings.TrimSpace(rr.Body.String()))
		}
	}
	rr := r.do(t, "POST", "/api/programmer/fan", map[string]any{"attribute": "Control1", "shape": "linear", "from": map[string]any{"dmx": 0}, "to": map[string]any{"dmx": 255}}, nil)
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "/api/programmer/command") {
		t.Errorf("/fan on Control1: %d %s, want 422 naming /api/programmer/command", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	v := r.view(t)
	if v.Revision != rev {
		t.Errorf("a refused write moved the revision %d -> %d", rev, v.Revision)
	}
	for _, c := range v.attr("Control1").Channels {
		if c.Touched {
			t.Errorf("Control1 on %s is held after refused writes: %+v", c.EntryID, c)
		}
	}
	if res := r.set(t, map[string]any{"attribute": "Dimmer", "fraction": 0.5}); len(res.Applied) == 0 {
		t.Errorf("a Dimmer /set is still accepted: %+v", res)
	}
	// The read model says which functions are commands, so the UI decides
	// exactly as the server does.
	rv := r.do(t, "GET", "/api/programmer", nil, nil)
	var raw struct {
		Groups []struct {
			Attributes []struct {
				Attribute string
				Variants  []struct {
					Functions []struct {
						Name    string
						Command *bool `json:"command"`
					}
				}
			}
		}
	}
	_ = json.Unmarshal(rv.Body.Bytes(), &raw)
	seen := map[string]bool{}
	for _, g := range raw.Groups {
		for _, a := range g.Attributes {
			for _, va := range a.Variants {
				for _, f := range va.Functions {
					if f.Command == nil {
						t.Fatalf("%s function %q has no command flag", a.Attribute, f.Name)
					}
					if (a.Attribute == "Control1") != *f.Command {
						t.Errorf("%s function %q command=%v, want %v", a.Attribute, f.Name, *f.Command, a.Attribute == "Control1")
					}
					seen[a.Attribute] = true
				}
			}
		}
	}
	if !seen["Control1"] || !seen["Dimmer"] {
		t.Errorf("view had no Control1 or Dimmer functions: %v", seen)
	}
}

// TestLowlightCells (I2d3, owner 2026-10-09: Lowlight works on cells as
// well as whole fixtures). The rule: Lowlight dims every SELECTION TARGET
// that is not highlighted — a whole fixture dims as before (all its
// dimmers), a cell dims only its own dimmer (for the Paladin, its virtual
// dimmer: the cell's colour). Highlight wins where they overlap: a channel
// the highlighted target drives is never lowlit, and a whole-fixture master
// that would also dim a highlighted cell is left alone. Proven on the real
// Paladin Cube (P1, three RGBW cells at 101/109/117, red 40000 each, master
// 255): lowlit 20 % = 8000.
func TestLowlightCells(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	p1 := r.ids["P1"]
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "Dimmer", "dmx": 255})
	r.set(t, map[string]any{"targets": []any{r.target("P1")}, "attribute": "ColorAdd_R", "dmx": 40000})
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Dimmer", "dmx": 50000})
	red := func(what string, c1, c2, c3 uint16) {
		t.Helper()
		f := r.wire(t, 1)
		for i, v := range []uint16{c1, c2, c3} {
			wantSlots(t, fmt.Sprintf("%s: P1 cell %d red", what, i+1), f, 101+8*i, byte(v>>8), byte(v))
		}
	}
	sel := func(targets ...any) {
		r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": targets}, nil)
	}
	var v c4bView

	// Three cells selected, stepped to cell 1: cells 2 and 3 are lowlit.
	sel(cellTarget(p1, "Beam 1:0"), cellTarget(p1, "Beam 2:0"), cellTarget(p1, "Beam 3:0"))
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	red("whole selection highlighted", 40000, 40000, 40000)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	red("cells, stepped to cell 1", 40000, 8000, 8000)
	if v.Highlight.Lowlit != 2 || v.Highlight.LowlitCells != 2 {
		t.Errorf("cells, stepped to cell 1: lowlit %d (cells %d), want 2 (2)", v.Highlight.Lowlit, v.Highlight.LowlitCells)
	}

	// Mixed: B1 whole + P1 cell 2, stepped to B1: only cell 2 is lowlit; the
	// unselected cells 1 and 3 are untouched.
	sel(r.target("B1"), cellTarget(p1, "Beam 2:0"))
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	red("B1 + cell 2, stepped to B1", 40000, 8000, 40000)
	if v.Highlight.Lowlit != 1 || v.Highlight.LowlitCells != 1 {
		t.Errorf("B1 + cell 2: lowlit %d (cells %d), want 1 (1)", v.Highlight.Lowlit, v.Highlight.LowlitCells)
	}

	// Parent and one of its cells: the parent highlighted covers the cell,
	// so nothing is lowlit; the cell highlighted leaves the parent's other
	// cells lowlit, never the highlighted cell.
	sel(r.target("P1"), cellTarget(p1, "Beam 1:0"))
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	red("P1 + its cell 1, stepped to P1", 40000, 40000, 40000)
	if v.Highlight.Lowlit != 0 {
		t.Errorf("P1 + its cell 1, stepped to P1: lowlit %d, want 0", v.Highlight.Lowlit)
	}
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, &v)
	red("P1 + its cell 1, stepped to cell 1", 40000, 8000, 8000)
	if v.Highlight.Lowlit != 1 || v.Highlight.LowlitCells != 0 {
		t.Errorf("P1 + its cell 1, stepped to cell 1: lowlit %d (cells %d), want 1 (0: the whole P1)", v.Highlight.Lowlit, v.Highlight.LowlitCells)
	}
}
