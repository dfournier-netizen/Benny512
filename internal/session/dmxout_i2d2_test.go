package session

import (
	"testing"
	"time"
)

// TestFireCommandReturnsToTheSourceBelow (I2d2/I2d3): a one-shot command
// claims its channel for the window and then the channel shows what the
// sources below show again — a programmer value where one is held (slot 1:
// 57), nothing (0) where none is. Since I2d3 /api/programmer/set refuses
// Control values, this is proven here at the engine, where the layering
// lives.
func TestFireCommandReturnsToTheSourceBelow(t *testing.T) {
	e, clock, tr := newDMX(t, DMXConfig{})
	var prog LayerFrame
	prog.Values[0], prog.Owned[0] = 57, true
	if err := e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: prog}); err != nil {
		t.Fatal(err)
	}
	e.Arm("laptop")
	var cmd LayerFrame
	cmd.Values[0], cmd.Owned[0] = 205, true
	cmd.Values[1], cmd.Owned[1] = 205, true
	ended := make(chan bool, 1)
	if err := e.FireCommand(map[uint16]LayerFrame{0: cmd}, time.Second, func(done bool) { ended <- done }); err != nil {
		t.Fatal(err)
	}
	last := func() []byte {
		tr.TakeSent()
		clock.Advance(30 * time.Millisecond)
		f := dmxFor(t, tr.TakeSent(), 0)
		if len(f) == 0 {
			t.Fatal("no frame on the wire")
		}
		return f[len(f)-1].Data
	}
	if d := last(); d[0] != 205 || d[1] != 205 {
		t.Fatalf("during the window: %v, want 205 205", d[:2])
	}
	e.Heartbeat("laptop")
	clock.Advance(time.Second)
	if d := last(); d[0] != 57 || d[1] != 0 {
		t.Errorf("after the window: slot 1 %d slot 2 %d, want 57 (programmer) and 0 (nothing below)", d[0], d[1])
	}
	select {
	case done := <-ended:
		if !done {
			t.Errorf("the window ran out but the command reported stopped")
		}
	default:
		t.Errorf("the command never reported its end")
	}
}
