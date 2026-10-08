package patch

import (
	"benny512/internal/session"
)

// This file is Rig Check's output boundary: the one place it hands frames to
// the unified output engine (session.DMXOutputEngine, chunk C3).
//
// Rig Check is the engine's lowest-priority source, session.SourceTests: the
// Function check's test patterns and the classic channel walk layer UNDER
// the programmer and raw DMX, so a manual value always wins. It claims every
// slot of every universe in its scope — the whole-frame writes it has always
// made — which keeps its output byte-identical to the pre-C3 code when it is
// the only source (rigcheck_golden_test.go pins that).
//
// What Rig Check no longer decides: the wire protocol (each universe's
// Art-Net / sACN / both routing is the engine's, set on Settings), and
// whether anything reaches the wire at all (the master Arm). Before C3 this
// file held a per-run Art-Net-or-sACN switch and Rig Check's own E1.31
// cadence and stop sequence; both moved into the engine, which applies them
// to every source alike.

// rigOutput is RigCheck's output target. Every method is called with
// RigCheck.mu held, so it carries no lock of its own.
type rigOutput struct {
	dmx  *session.DMXOutputEngine
	live map[uint16]bool // raw Port-Address -> claimed by Rig Check
}

func newRigOutput(dmx *session.DMXOutputEngine) *rigOutput {
	return &rigOutput{dmx: dmx, live: map[uint16]bool{}}
}

// startUniverse claims one universe for Rig Check, all-zero until the first
// frame is set. It transmits nothing by itself.
func (o *rigOutput) startUniverse(raw uint16) error {
	if err := o.dmx.SetFrame(session.SourceTests, raw, nil); err != nil {
		return err
	}
	o.live[raw] = true
	return nil
}

// setFrames publishes a whole recomputed frame set and pushes it at once
// (Rig Check never waits for the next tick to show a change). force restarts
// the sACN three-packet burst even for an unchanged frame — Blackout uses it,
// because "the hard all-off that always works" must put a packet on the wire
// even if the frame it replaces was already zero.
func (o *rigOutput) setFrames(frames map[uint16][]byte, force bool) {
	raws := make([]uint16, 0, len(frames))
	for raw, data := range frames {
		if !o.live[raw] {
			continue
		}
		if err := o.dmx.SetFrame(session.SourceTests, raw, data); err == nil {
			raws = append(raws, raw)
		}
	}
	o.dmx.Push(force, raws...)
}

// stopAll releases every universe Rig Check claims. Callers blackout first
// (RigCheck.stopLocked does), so on a universe nobody else drives the last
// frame on the wire is already zero and the engine retires the stream
// without adding anything; on a universe another source still drives, that
// source simply shows through.
func (o *rigOutput) stopAll() {
	raws := make([]uint16, 0, len(o.live))
	for raw := range o.live {
		raws = append(raws, raw)
	}
	o.dmx.Release(session.SourceTests, raws...)
	o.live = map[uint16]bool{}
}
