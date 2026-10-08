package session

import (
	"bytes"
	"testing"
)

// Console-lite C4a: the BASE source (profile defaults for every patched
// channel) sits under every other source, and ReplaceSource swaps a source's
// whole claim set in one step. Frames are decoded at FakeTransport with
// artnet.Decode (dmxFor), never re-encoded here.

func lf(slots map[int]byte) LayerFrame {
	var f LayerFrame
	for ch, v := range slots {
		f.Values[ch-1] = v
		f.Owned[ch-1] = true
	}
	return f
}

func TestBaseSourceIsUnderTestsProgrammerAndRaw(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	if err := e.ReplaceSource(SourceBase, map[uint16]LayerFrame{0: lf(map[int]byte{1: 1, 2: 1, 3: 1, 4: 1, 5: 1})}); err != nil {
		t.Fatal(err)
	}
	_ = e.SetChannels(SourceTests, 0, 2, []byte{10, 10, 10})
	_ = e.SetChannels(SourceProgrammer, 0, 3, []byte{20, 20})
	_ = e.SetChannels(SourceRaw, 0, 4, []byte{30})
	e.Arm("a")
	f := dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 || !bytes.Equal(f[0].Data[:6], []byte{1, 10, 20, 30, 1, 0}) {
		t.Fatalf("slots 1-6 = %v; want base 1, tests 10, programmer 20, raw 30, base 1, unclaimed 0", f)
	}
}

func TestReplaceSourceSwapsAtomicallyAndReleasesOmittedUniverses(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{1: 50}), 1: lf(map[int]byte{7: 70})})
	e.Arm("a")
	tr.TakeSent()
	// Universe 0 changes value, universe 1 is omitted (released).
	if err := e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{1: 60})}); err != nil {
		t.Fatal(err)
	}
	sent := tr.TakeSent()
	u0 := dmxFor(t, sent, 0)
	if len(u0) != 1 || u0[0].Data[0] != 60 {
		t.Fatalf("universe 0 after replace: %v, want one immediate frame with slot 1 = 60", u0)
	}
	u1 := dmxFor(t, sent, 1)
	if len(u1) != BlackoutFrameCount || !isZero(u1[0].Data) {
		t.Fatalf("universe 1 released by omission: %d frames, want %d zero frames retiring the lit stream", len(u1), BlackoutFrameCount)
	}
	// Replacing with an identical claim transmits nothing outside the tick.
	_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{1: 60})})
	if n := len(tr.TakeSent()); n != 0 {
		t.Errorf("an unchanged replace sent %d packets; want none", n)
	}
	if err := e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0x8000: lf(map[int]byte{1: 1})}); err == nil {
		t.Error("a universe beyond 0x7FFF was accepted")
	}
	if SourceBase.String() != "base" {
		t.Errorf("SourceBase.String() = %q", SourceBase.String())
	}
}

func TestReplaceSourceWhileDisarmedSendsNothing(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	_ = e.ReplaceSource(SourceBase, map[uint16]LayerFrame{0: lf(map[int]byte{1: 9})})
	if n := tr.SentCount(); n != 0 {
		t.Fatalf("disarmed engine sent %d packets", n)
	}
	e.Arm("a")
	f := dmxFor(t, tr.TakeSent(), 0)
	if len(f) != 1 || f[0].Data[0] != 9 {
		t.Fatalf("base frame after Arm: %v", f)
	}
}
