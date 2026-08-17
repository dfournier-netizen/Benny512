package patch

import (
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

func newRigCheckHarness(t *testing.T) (*RigCheck, *session.FakeTransport) {
	t.Helper()
	clock := session.NewFakeClock(time.Time{})
	tr := session.NewFakeTransport()
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: clock, Transport: tr, Rate: 40})
	t.Cleanup(dmx.Stop)
	return NewRigCheck(dmx), tr
}

// lastFrameFor returns the most recently sent ArtDmx frame for the given
// Port-Address raw value among sent packets, and whether one was found.
func lastFrameFor(t *testing.T, sent []session.SentPacket, universe uint16) ([]byte, bool) {
	t.Helper()
	pa, err := artnet.PortAddressFromRaw(universe)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	found := false
	for _, sp := range sent {
		if sp.DecodeErr != nil {
			t.Fatalf("undecodable ArtDmx: %v", sp.DecodeErr)
		}
		if sp.Packet.Kind != artnet.KindDmx {
			continue
		}
		d := sp.Packet.Dmx
		if d.SubUni != pa.SubUni() || d.Net != pa.Net {
			continue
		}
		out = append([]byte(nil), d.Data...)
		found = true
	}
	return out, found
}

func TestRigCheck_HighlightModeLightsOnlyCurrentEntry(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 0, StartAddress: 10, Footprint: 3},
	}
	if err := rc.Start(entries, ModeHighlight, 200); err != nil {
		t.Fatal(err)
	}
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame for universe 0")
	}
	for ch := 1; ch <= 4; ch++ {
		if frame[ch-1] != 200 {
			t.Errorf("channel %d = %d, want 200 (entry a is current)", ch, frame[ch-1])
		}
	}
	for ch := 10; ch <= 12; ch++ {
		if frame[ch-1] != 0 {
			t.Errorf("channel %d = %d, want 0 (entry b is not current)", ch, frame[ch-1])
		}
	}
}

func TestRigCheck_NextMovesHighlightToNextEntry(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 0, StartAddress: 10, Footprint: 3},
	}
	if err := rc.Start(entries, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	if err := rc.Next(); err != nil {
		t.Fatal(err)
	}
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame after Next")
	}
	for ch := 1; ch <= 4; ch++ {
		if frame[ch-1] != 0 {
			t.Errorf("channel %d = %d, want 0 (entry a no longer current)", ch, frame[ch-1])
		}
	}
	for ch := 10; ch <= 12; ch++ {
		if frame[ch-1] != 255 {
			t.Errorf("channel %d = %d, want 255 (entry b now current)", ch, frame[ch-1])
		}
	}
	st := rc.State()
	if st.EntryIndex != 1 {
		t.Errorf("EntryIndex = %d, want 1", st.EntryIndex)
	}
}

func TestRigCheck_NextClampsAtEnd(t *testing.T) {
	rc, _ := newRigCheckHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 1}}
	if err := rc.Start(entries, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	if err := rc.Next(); err != nil {
		t.Fatal(err)
	}
	if err := rc.Next(); err != nil {
		t.Fatal(err)
	}
	if rc.State().EntryIndex != 0 {
		t.Errorf("EntryIndex = %d, want clamped at 0 (only one entry)", rc.State().EntryIndex)
	}
}

func TestRigCheck_StepChannelLightsOneChannelOnly(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 100, Footprint: 5}}
	if err := rc.Start(entries, ModeStepChannel, 128); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	if err := rc.StepChannel(1); err != nil { // offset 0 -> 1, channel 101
		t.Fatal(err)
	}
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	for ch := 100; ch <= 104; ch++ {
		want := byte(0)
		if ch == 101 {
			want = 128
		}
		if frame[ch-1] != want {
			t.Errorf("channel %d = %d, want %d", ch, frame[ch-1], want)
		}
	}
	st := rc.State()
	if st.CurrentChannel != 101 {
		t.Errorf("CurrentChannel = %d, want 101", st.CurrentChannel)
	}
}

func TestRigCheck_StepChannelClampsAtFootprintBounds(t *testing.T) {
	rc, _ := newRigCheckHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 3}}
	if err := rc.Start(entries, ModeStepChannel, 255); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := rc.StepChannel(1); err != nil {
			t.Fatal(err)
		}
	}
	if rc.State().ChannelOffset != 2 {
		t.Errorf("ChannelOffset = %d, want clamped at 2 (footprint 3)", rc.State().ChannelOffset)
	}
	if err := rc.StepChannel(-100); err != nil {
		t.Fatal(err)
	}
	if rc.State().ChannelOffset != 0 {
		t.Errorf("ChannelOffset = %d, want clamped at 0", rc.State().ChannelOffset)
	}
}

func TestRigCheck_AllChannelsModeLightsEveryEntry(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 2},
		{ID: "b", Universe: 0, StartAddress: 20, Footprint: 2},
	}
	if err := rc.Start(entries, ModeAllChannels, 100); err != nil {
		t.Fatal(err)
	}
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("expected a frame")
	}
	for _, ch := range []int{1, 2, 20, 21} {
		if frame[ch-1] != 100 {
			t.Errorf("channel %d = %d, want 100 (all-channels mode lights every entry)", ch, frame[ch-1])
		}
	}
}

func TestRigCheck_MultiUniverseScope(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 2},
		{ID: "b", Universe: 3, StartAddress: 1, Footprint: 2},
	}
	if err := rc.Start(entries, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	sent := tr.TakeSent()
	f0, ok0 := lastFrameFor(t, sent, 0)
	f3, ok3 := lastFrameFor(t, sent, 3)
	if !ok0 || !ok3 {
		t.Fatalf("expected frames on both universes; ok0=%v ok3=%v", ok0, ok3)
	}
	if f0[0] != 255 {
		t.Errorf("universe 0 channel 1 = %d, want 255 (entry a is current)", f0[0])
	}
	if f3[0] != 0 {
		t.Errorf("universe 3 channel 1 = %d, want 0 (entry b not current)", f3[0])
	}
}

func TestRigCheck_StopBlacksOutAndStopsTransmitting(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := rc.Start(entries, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	rc.Stop()
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("Stop must push a final all-zero frame")
	}
	for i, v := range frame {
		if v != 0 {
			t.Fatalf("channel %d = %d after Stop, want 0 (blackout)", i+1, v)
		}
	}
	if rc.State().Running {
		t.Error("Running should be false after Stop")
	}
}

func TestRigCheck_BlackoutKeepsRunningState(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	entries := []Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4}}
	if err := rc.Start(entries, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	rc.Blackout()
	frame, ok := lastFrameFor(t, tr.TakeSent(), 0)
	if !ok {
		t.Fatal("Blackout must push a frame")
	}
	for _, v := range frame {
		if v != 0 {
			t.Fatal("Blackout must zero every channel")
		}
	}
	if !rc.State().Running {
		t.Error("Blackout must not end the run (Running should stay true)")
	}
	// Still steerable after a blackout.
	if err := rc.SetLevel(50); err != nil {
		t.Fatal(err)
	}
	frame2, _ := lastFrameFor(t, tr.TakeSent(), 0)
	if frame2[0] != 50 {
		t.Errorf("channel 1 after SetLevel post-blackout = %d, want 50", frame2[0])
	}
}

func TestRigCheck_ActionsRequireRunning(t *testing.T) {
	rc, _ := newRigCheckHarness(t)
	if err := rc.Next(); err != ErrRigCheckNotRunning {
		t.Errorf("Next() before Start = %v, want ErrRigCheckNotRunning", err)
	}
	if err := rc.Jump(0); err != ErrRigCheckNotRunning {
		t.Errorf("Jump() before Start = %v, want ErrRigCheckNotRunning", err)
	}
	if err := rc.SetLevel(1); err != ErrRigCheckNotRunning {
		t.Errorf("SetLevel() before Start = %v, want ErrRigCheckNotRunning", err)
	}
}

func TestRigCheck_StartEmptyScopeErrors(t *testing.T) {
	rc, _ := newRigCheckHarness(t)
	if err := rc.Start(nil, ModeHighlight, 255); err != ErrRigCheckEmptyScope {
		t.Errorf("Start(nil) = %v, want ErrRigCheckEmptyScope", err)
	}
}

func TestRigCheck_StartTwiceCleansUpPreviousUniverses(t *testing.T) {
	rc, tr := newRigCheckHarness(t)
	if err := rc.Start([]Entry{{ID: "a", Universe: 0, StartAddress: 1, Footprint: 2}}, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	tr.TakeSent()
	// Re-start over a different, non-overlapping universe — universe 0 must
	// end up dark and stopped, not left lit from the previous run.
	if err := rc.Start([]Entry{{ID: "b", Universe: 7, StartAddress: 1, Footprint: 2}}, ModeHighlight, 255); err != nil {
		t.Fatal(err)
	}
	sent := tr.TakeSent()
	f0, ok0 := lastFrameFor(t, sent, 0)
	if !ok0 {
		t.Fatal("expected a blackout frame for the previous run's universe 0")
	}
	for _, v := range f0 {
		if v != 0 {
			t.Fatal("previous universe must be dark after re-Start")
		}
	}
	f7, ok7 := lastFrameFor(t, sent, 7)
	if !ok7 || f7[0] != 255 {
		t.Errorf("universe 7 channel 1 = %v (ok=%v), want 255", f7, ok7)
	}
}
