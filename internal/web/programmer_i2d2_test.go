package web

import (
	"encoding/json"
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
	s1, _ := r.control1(t, "B1", 1)
	s2, def2 := r.control1(t, "B2", 42)
	r.selectNames(t, "B1")
	linear := r.set(t, map[string]any{"attribute": "Control1", "set": "Dimmer curve: Linear"}).Applied[0].Value
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

	r.heartbeatFor(t, 4900*time.Millisecond)
	if f = r.wire(t, 0); uint32(f[s1-1]) != reset {
		t.Errorf("4.9 s in: B1 %d, want the command still on the wire (%d)", f[s1-1], reset)
	}
	r.heartbeatFor(t, 200*time.Millisecond)
	f = r.wire(t, 0)
	if uint32(f[s1-1]) != linear || uint32(f[s2-1]) != def2 {
		t.Errorf("after the window: B1 %d B2 %d, want B1 back at its programmer value %d and B2 at its default %d", f[s1-1], f[s2-1], linear, def2)
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
	if a.Name != "Total reset" || strings.Join(a.Fixtures, ",") != "B1,B2" || a.State != "sending" || a.WindowMs != 5000 || a.RemainingMs <= 4000 || a.RemainingMs > 5000 {
		t.Errorf("in flight: %+v, want Total reset on B1,B2, sending, 5000 ms window, about 5000 ms left", a)
	}
	r.heartbeatFor(t, 3*time.Second)
	if v = r.commands(t); len(v.Commands.Active) != 1 || v.Commands.Active[0].RemainingMs > 2100 {
		t.Errorf("3 s in: %+v, want it still in flight with about 2 s left", v.Commands)
	}
	r.heartbeatFor(t, 2100*time.Millisecond)
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
	s1, _ := r.control1(t, "B1", 1)
	r.selectNames(t, "B1")
	linear := r.set(t, map[string]any{"attribute": "Control1", "set": "Dimmer curve: Linear"}).Applied[0].Value
	r.h.srv.DMX.SetLeaseLossAction(session.LeaseLossHold)
	r.arm(t)
	r.h.clock.Advance(4 * time.Second) // the last heartbeat was the Arm
	reset := r.command(t, map[string]any{"attribute": "Control1", "set": "Total reset"}).Applied[0].Value
	if f := r.wire(t, 0); uint32(f[s1-1]) != reset {
		t.Fatalf("command not on the wire: %d, want %d", f[s1-1], reset)
	}
	r.h.clock.Advance(1100 * time.Millisecond) // the lease is lost 1 s into the window
	if st := r.h.srv.DMX.State(); st != session.StateHolding {
		t.Fatalf("lease lost with Hold: %q, want holding", st)
	}
	if f := r.wire(t, 0); uint32(f[s1-1]) != linear {
		t.Errorf("held look: B1 Control1 %d, want %d (its programmer value; the Reset %d is not held)", f[s1-1], linear, reset)
	}
	if v := r.commandsWhen(t, func(v cmdView) bool { return v.Commands.Last != nil }); len(v.Commands.Active) != 0 || v.Commands.Last == nil || v.Commands.Last.State != "stopped" {
		t.Errorf("lease lost: %+v, want the command stopped", v.Commands)
	}
}
