package patch

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"benny512/internal/session"
)

// This file pins Rig Check's Art-Net wire output, byte for byte, to what the
// code produced BEFORE the unified output engine existed (C3). The golden
// file testdata/rigcheck_artnet_golden.txt was written by this exact
// scenario run against the pre-C3 tree (commit cde63fd), where Rig Check
// owned its universes in the old shared DMXOutputEngine and started that
// engine's tick itself. The owner's rule for C3 is that Rig Check's
// observable output is identical when it is the only source, so the only
// difference allowed here is the one step the new model adds before
// anything can reach the wire: the master Arm, taken at the same instant the
// old code started its tick.
//
// Every datagram is recorded with its fake-clock offset, so timing (the
// 40 Hz retransmit tick and the immediate push on every change), order,
// sequence numbers and slot data are all covered, not just final levels.

const rigCheckGoldenPath = "testdata/rigcheck_artnet_golden.txt"

func goldenEntries() []Entry {
	return []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 4},
		{ID: "b", Universe: 0, StartAddress: 20, Footprint: 2},
		{ID: "c", Universe: 3, StartAddress: 500, Footprint: 13},
		dimmerEntry("d", 3),
	}
}

// rigCheckGoldenScenario drives every Rig Check output path the Patch
// screen and the Function check use: the classic walk (all modes, cursor
// moves, level, blackout, stop) and the pattern engine (start, live adjust,
// stop). arm is called once, at t=0, before the first call that can emit.
func rigCheckGoldenScenario(t *testing.T, rc *RigCheck, clock *session.FakeClock, tr *session.FakeTransport, arm func()) []string {
	t.Helper()
	start := clock.Now()
	var lines []string
	record := func(step string) {
		lines = append(lines, "# "+step)
		for _, sp := range tr.TakeSent() {
			lines = append(lines, fmt.Sprintf("%d %v %s", clock.Now().Sub(start).Milliseconds(), sp.Dst, hex.EncodeToString(sp.Data)))
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	entries := goldenEntries()
	arm()
	must(rc.Start(entries, ModeHighlight, 200))
	record("classic start highlight 200")
	clock.Advance(60 * time.Millisecond)
	record("advance 60ms")
	must(rc.Next())
	record("next")
	clock.Advance(30 * time.Millisecond)
	record("advance 30ms")
	must(rc.SetMode(ModeStepChannel))
	must(rc.StepChannel(1))
	record("step mode, channel +1")
	must(rc.Next())
	must(rc.Next())
	must(rc.SetLevel(77))
	record("next, next, level 77")
	clock.Advance(55 * time.Millisecond)
	record("advance 55ms")
	must(rc.SetMode(ModeAllChannels))
	record("all channels")
	rc.Blackout()
	record("blackout")
	clock.Advance(40 * time.Millisecond)
	record("advance 40ms")
	rc.Stop()
	record("stop")
	clock.Advance(100 * time.Millisecond)
	record("advance 100ms after stop")

	_, err := rc.StartPattern(entries, PatternSpec{Kind: PatternDimmerSine, Params: PatternParams{RateHz: 2, Max: 255}})
	must(err)
	record("pattern start dimmer sine 2Hz")
	for i := 0; i < 8; i++ {
		clock.Advance(15 * time.Millisecond)
		record(fmt.Sprintf("advance 15ms #%d", i+1))
	}
	_, err = rc.AdjustPattern(PatternParams{RateHz: 1, Max: 128})
	must(err)
	record("adjust 1Hz max 128")
	clock.Advance(80 * time.Millisecond)
	record("advance 80ms")
	rc.StopPatternOutput()
	record("pattern stop")
	clock.Advance(100 * time.Millisecond)
	record("advance 100ms after pattern stop")
	return lines
}

// TestRigCheckArtNetOutputMatchesPreC3Golden replays the scenario through the
// unified engine, armed at t=0, and requires every datagram — bytes, order,
// destination and fake-clock time — to equal the pre-C3 recording.
func TestRigCheckArtNetOutputMatchesPreC3Golden(t *testing.T) {
	want, err := os.ReadFile(rigCheckGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	clock := session.NewFakeClock(time.Time{})
	tr := session.NewFakeTransport()
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: clock, Transport: tr, Rate: 40, Lease: -1})
	rc := NewRigCheck(dmx)
	got := strings.Join(rigCheckGoldenScenario(t, rc, clock, tr, func() { dmx.Arm("golden") }), "\n") + "\n"
	if got == string(want) {
		return
	}
	g, w := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Fatalf("Rig Check wire output differs from the pre-C3 golden at line %d:\n got: %.160s\nwant: %.160s", i+1, gl, wl)
		}
	}
}
