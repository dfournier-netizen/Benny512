package session

import (
	"bytes"
	"testing"
)

// C4b: the Highlight source sits between the programmer and raw, and
// Lowlight scales what base..programmer composed — 16-bit values as one
// number, never upward — before highlight and raw write.

func TestHighlightSourceOrderAndLowlightScale(t *testing.T) {
	e, _, tr := newDMX(t, DMXConfig{Lease: -1})
	_ = e.ReplaceSource(SourceBase, map[uint16]LayerFrame{0: lf(map[int]byte{1: 200, 2: 0x80, 3: 0x01, 4: 0, 5: 50, 6: 7})})
	_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{5: 60})})
	_ = e.ReplaceSource(SourceHighlight, map[uint16]LayerFrame{0: lf(map[int]byte{5: 255, 6: 255})})
	_ = e.SetChannels(SourceRaw, 0, 6, []byte{9})
	if err := e.SetLowlight(map[uint16][]ScaleGroup{0: {{Slots: []int{0}}, {Slots: []int{1, 2}}, {Slots: []int{3}}, {Slots: []int{4}}}}, 20); err != nil {
		t.Fatal(err)
	}
	e.Arm("a")
	f := dmxFor(t, tr.TakeSent(), 0)
	// slot 1: 200 -> 40; slots 2-3: 0x8001 = 32769 -> 6553 = 0x1999;
	// slot 4: 0 stays 0; slot 5: highlight 255 written after the scale;
	// slot 6: raw 9 beats highlight 255.
	want := []byte{40, 0x19, 0x99, 0, 255, 9}
	if len(f) != 1 || !bytes.Equal(f[0].Data[:6], want) {
		t.Fatalf("slots 1-6 = %v, want %v", f, want)
	}
	// Ending lowlight and highlight restores the composition exactly.
	_ = e.SetLowlight(nil, 20)
	_ = e.ReplaceSource(SourceHighlight, nil)
	f = dmxFor(t, tr.TakeSent(), 0)
	if len(f) == 0 || !bytes.Equal(f[len(f)-1].Data[:6], []byte{200, 0x80, 0x01, 0, 60, 9}) {
		t.Fatalf("after lowlight/highlight off: %v", f)
	}
	if err := e.SetLowlight(nil, 101); err == nil {
		t.Error("a lowlight above 100% was accepted")
	}
	if SourceHighlight.String() != "highlight" || !(SourceProgrammer < SourceHighlight && SourceHighlight < SourceRaw) {
		t.Error("highlight is not between programmer and raw")
	}
}
