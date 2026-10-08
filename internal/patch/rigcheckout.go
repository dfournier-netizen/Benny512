package patch

import (
	"fmt"

	"benny512/internal/session"
)

// This file is Rig Check's output boundary: the one place it hands frames to
// the unified output engine (session.DMXOutputEngine, chunk C3).
//
// Rig Check is the engine's session.SourceTests, the Tests layer: the
// Function check's test patterns, the classic channel walk and (C5) the
// Console-lite tests and test sequences layer UNDER the programmer and raw
// DMX, so a manual value always wins.
//
// What it claims (C5, owner-approved): ONLY the channels of the fixtures —
// or cells — in its scope, never whole universes. Within that claim it
// writes exactly the values it always has (rigcheck_golden_test.go pins them
// byte for byte); every other slot of a tested universe shows whatever lies
// beneath — the base source's profile defaults — or the programmer above it.
// Until C5 it claimed every slot of every universe in its scope, which forced
// untested fixtures sharing a universe with a tested one to 0.
//
// Every publish goes through session.DMXOutputEngine.PublishSource: the whole
// claim is replaced in one locked step and every affected universe is pushed
// at once, so a scope or step change can never leave a frame in which the
// tests layer has let go (a release-then-claim gap).
//
// What Rig Check no longer decides: the wire protocol (each universe's
// Art-Net / sACN / both routing is the engine's, set on Settings), and
// whether anything reaches the wire at all (the master Arm).

// slotClaim is the set of slots Rig Check owns on one universe.
type slotClaim = [session.DMXUniverseSize]bool

// rigOutput is RigCheck's output target. Every method is called with
// RigCheck.mu held, so it carries no lock of its own.
type rigOutput struct {
	dmx  *session.DMXOutputEngine
	live map[uint16]bool // raw Port-Address -> in the current run's universes
	// claim is what the last publish claimed, per universe — what a
	// blackout zeroes and stopAll releases.
	claim map[uint16]*slotClaim
}

func newRigOutput(dmx *session.DMXOutputEngine) *rigOutput {
	return &rigOutput{dmx: dmx, live: map[uint16]bool{}, claim: map[uint16]*slotClaim{}}
}

// startUniverse admits one universe to the run. It claims and transmits
// nothing by itself: the first publish does both, atomically.
func (o *rigOutput) startUniverse(raw uint16) error {
	if raw > 0x7FFF {
		return fmt.Errorf("%w: %d", session.ErrUniverseOutOfRange, raw)
	}
	o.live[raw] = true
	return nil
}

// setFrames publishes frames over claim — only the claimed slots of each
// started universe — and pushes them at once (Rig Check never waits for the
// next tick to show a change). force restarts the sACN three-packet burst
// even for an unchanged frame — Blackout uses it, because "the hard all-off
// that always works" must put a packet on the wire even if the frame it
// replaces was already zero.
func (o *rigOutput) setFrames(frames map[uint16][]byte, claim map[uint16]*slotClaim, force bool) {
	out := make(map[uint16]session.LayerFrame, len(claim))
	kept := make(map[uint16]*slotClaim, len(claim))
	for raw, mask := range claim {
		if !o.live[raw] || mask == nil {
			continue
		}
		f := session.LayerFrame{Owned: *mask}
		data := frames[raw]
		for i, owned := range mask {
			if owned && i < len(data) {
				f.Values[i] = data[i]
			}
		}
		out[raw] = f
		kept[raw] = mask
	}
	_ = o.dmx.PublishSource(session.SourceTests, out, force)
	o.claim = kept
}

// blackout zeroes every slot of the current claim and pushes it at once.
func (o *rigOutput) blackout() {
	o.setFrames(nil, o.claim, true)
}

// stopAll releases every universe Rig Check claims. Classic Rig Check
// blacks out first (RigCheck.stopLocked), so on a universe nobody else drives
// the last frame on the wire is already zero and the engine retires the
// stream without adding anything; the Tests layer releases directly, so the
// sources beneath (base defaults, the programmer) show at once.
func (o *rigOutput) stopAll() {
	raws := make([]uint16, 0, len(o.live)+len(o.claim))
	for raw := range o.live {
		raws = append(raws, raw)
	}
	for raw := range o.claim {
		if !o.live[raw] {
			raws = append(raws, raw)
		}
	}
	o.dmx.Release(session.SourceTests, raws...)
	o.live = map[uint16]bool{}
	o.claim = map[uint16]*slotClaim{}
}

// claimEntry adds e's slots to claim: offsets (1-based within the
// footprint) when given — a cell — else the whole footprint. A slot outside
// 1-512 is never claimed.
func claimEntry(claim map[uint16]*slotClaim, e Entry, offsets []uint16) {
	mark := func(off uint16) {
		ch := int(e.StartAddress) + int(off) - 1
		if off < 1 || ch < 1 || ch > session.DMXUniverseSize {
			return
		}
		c := claim[e.Universe]
		if c == nil {
			c = &slotClaim{}
			claim[e.Universe] = c
		}
		c[ch-1] = true
	}
	if offsets != nil {
		for _, off := range offsets {
			mark(off)
		}
		return
	}
	for off := uint16(1); off <= e.Footprint && off != 0; off++ {
		mark(off)
	}
}
