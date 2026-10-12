package session

import "testing"

// TestOwnershipHookFiresOnOwnershipNotValues (I2g): the ownership hook is
// called when which source owns a channel changes — raw levels set and
// cleared, Identify started and stopped, a virtual dimmer claimed — and
// not when only values move under the same ownership (a raw fader drag).
// The Tests view relies on it to announce an "Overridden by …" change at
// once without a socket message per fader move.
func TestOwnershipHookFiresOnOwnershipNotValues(t *testing.T) {
	e, _, _ := newDMX(t, DMXConfig{})
	calls := 0
	e.OnOwnershipChange(func() { calls++ })
	step := func(what string, want int, f func()) {
		t.Helper()
		before := calls
		f()
		if got := calls - before; got != want {
			t.Errorf("%s: the hook ran %d times, want %d", what, got, want)
		}
	}
	step("raw levels set", 1, func() { _ = e.SetFrame(SourceRaw, 0, []byte{10}) })
	step("a raw fader drag (values only, 30 moves)", 0, func() {
		for i := 0; i < 30; i++ {
			_ = e.SetFrame(SourceRaw, 0, []byte{byte(i)})
			e.Push(false, 0)
		}
	})
	step("raw levels cleared", 1, func() { e.ReleaseAll(SourceRaw) })
	step("Identify started", 1, func() { _ = e.SetIdentify(map[Stream][]byte{{WireArtNet, 0}: {255}}) })
	step("Identify stopped", 1, func() { e.ClearIdentify() })
	_ = e.SetVirtualDimmers(map[uint16][]VirtualDimmer{0: {{Key: "k", Channels: []ScaleGroup{{Slots: []int{0}}}}}})
	step("a virtual dimmer claimed", 1, func() { _ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"k": 10}) })
	step("its level moved", 0, func() { _ = e.SetVirtualDimmerLevels(SourceProgrammer, map[string]byte{"k": 90}) })
	step("a programmer SET (a new channel owned)", 1, func() {
		_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{3: 30})})
	})
	step("the same channel, a new value", 0, func() {
		_ = e.ReplaceSource(SourceProgrammer, map[uint16]LayerFrame{0: lf(map[int]byte{3: 99})})
	})
}
