package session

import (
	"testing"

	"benny512/internal/artnet"
)

// TestVirtualMultiplier (J1c, owner 2026-10-13 09:31: the Master is an
// overall dimmer — master 0 with the parts at full is no output; master at
// full with a part at 0 shows nothing of that part). A Multiplier virtual
// dimmer scales its channels by its level and NEVER fills: channels that
// composed to zero stay zero (the G1 "unset colour = white" rule is a
// colour scope's, not the master's). It is applied after every other
// virtual dimmer, so a pixel's own white fill is multiplied, not undone.
func TestVirtualMultiplier(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{Lease: -1})
	// Slot 1-2: a 16-bit part dimmer at full; slots 3-5: a pixel's RGB
	// (unset, 0) under its own virtual dimmer "px"; slot 6: a part at 0.
	_ = e.ReplaceSource(SourceBase, map[uint16]LayerFrame{0: lf(map[int]byte{1: 255, 2: 255, 3: 0, 4: 0, 5: 0, 6: 0})})
	_ = e.SetVirtualDimmers(map[uint16][]VirtualDimmer{0: {
		{Key: "master", Multiplier: true, Channels: []ScaleGroup{{Slots: []int{0, 1}}, {Slots: []int{2}}, {Slots: []int{3}}, {Slots: []int{4}}, {Slots: []int{5}}}},
		{Key: "px", Channels: []ScaleGroup{{Slots: []int{2}}, {Slots: []int{3}}, {Slots: []int{4}}}},
	}})
	frame := func() []byte {
		pa, _ := artnet.PortAddressFromRaw(0)
		f, _ := e.Frame(pa)
		return f
	}
	want := func(what string, w ...byte) {
		t.Helper()
		f := frame()
		for i, b := range w {
			if f[i] != b {
				t.Errorf("%s: slots 1-%d = %v, want %v", what, len(w), f[:len(w)], w)
				return
			}
		}
	}
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"master": 255, "px": 255})
	want("master full, pixel full (unset colour = white)", 255, 255, 255, 255, 255, 0)
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"master": 128, "px": 255})
	want("master 128: the part dimmer and the pixel's white scaled, the zero part stays zero", 128, 128, 128, 128, 128, 0)
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"master": 255, "px": 0})
	want("master full, pixel 0: the pixel dark, not filled by the master", 255, 255, 0, 0, 0, 0)
	_ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"master": 0, "px": 255})
	want("master 0, parts full: no output", 0, 0, 0, 0, 0, 0)
}
