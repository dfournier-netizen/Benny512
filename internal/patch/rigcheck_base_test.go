package patch

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// Console-lite C4a puts a BASE source (every patched channel at its profile
// default) under Rig Check's tests. This replays the pre-C3 golden scenario
// with the base source live on the same engine — the golden fixtures plus
// one fixture with a known default on a universe Rig Check tests (3) and one
// on a universe it never touches (5) — and pins exactly what changes:
//
//  1. Every datagram of the golden is still sent, byte for byte, at the same
//     fake-clock time and in the same order, EXCEPT its ArtDmx Sequence byte:
//     the base keeps a universe's stream alive across Rig Check runs, so the
//     per-universe sequence continues instead of restarting.
//  2. Every additional datagram carries exactly the base frame of its
//     universe: at the Arm before Rig Check starts, after Rig Check releases
//     a universe (where the golden's stream fell silent, the base frame now
//     follows), on every tick between runs, and on universe 5 throughout.
//
// So on universes Rig Check tests, its output is identical while it runs;
// between runs and on untested universes the wire now shows profile
// defaults instead of nothing.

const artDmxSequenceIndex = 12 // "Art-Net\0", OpCode, ProtVer (Art-Net 4, ArtDmx)

func baseExtraEntries() []Entry {
	def := func(v uint32) map[uint16]ChannelFunction {
		return map[uint16]ChannelFunction{1: {Source: SourceGDTF, Attribute: "Dimmer", DMXFrom: 0, DMXTo: 255,
			HasDefault: true, Default: v, DefaultByteCount: 1, ChannelSets: make([]ChannelSet, 0)}}
	}
	return []Entry{
		{ID: "z3", Universe: 3, StartAddress: 100, Footprint: 1, ChannelFunctions: def(77)},
		{ID: "y5", Universe: 5, StartAddress: 1, Footprint: 1, ChannelFunctions: def(99)},
	}
}

func TestRigCheckGoldenUnderBaseSource(t *testing.T) {
	want, err := os.ReadFile(rigCheckGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	clock := session.NewFakeClock(time.Time{})
	tr := session.NewFakeTransport()
	dmx := session.NewDMXOutputEngine(session.DMXConfig{Clock: clock, Transport: tr, Rate: 40, Lease: -1})
	rc := NewRigCheck(dmx)
	pg := NewProgrammer(dmx, 1)
	pg.Sync("show", append(goldenEntries(), baseExtraEntries()...), true)
	got := rigCheckGoldenScenario(t, rc, clock, tr, func() { dmx.Arm("golden") })

	base := map[string][]byte{}
	for _, u := range []uint16{0, 3, 5} {
		f := make([]byte, 512)
		switch u {
		case 3:
			f[99] = 77
		case 5:
			f[0] = 99
		}
		pa, _ := artnet.PortAddressFromRaw(u)
		base[fmt.Sprintf("%d/%d", pa.Net, pa.SubUni())] = f
	}
	type frame struct{ line, masked, step string }
	parse := func(lines []string) []frame {
		var out []frame
		step := ""
		for _, l := range lines {
			if l == "" {
				continue
			}
			if strings.HasPrefix(l, "# ") {
				step = l
				continue
			}
			f := strings.Fields(l)
			b, err := hex.DecodeString(f[2])
			if err != nil || len(b) <= artDmxSequenceIndex {
				t.Fatalf("unparseable golden line %q", l)
			}
			b[artDmxSequenceIndex] = 0
			out = append(out, frame{line: l, masked: f[0] + " " + f[1] + " " + hex.EncodeToString(b), step: step})
		}
		return out
	}
	golden, live := parse(strings.Split(string(want), "\n")), parse(got)
	gi, extra := 0, map[string]int{}
	for _, f := range live {
		if gi < len(golden) && f.masked == golden[gi].masked && f.step == golden[gi].step {
			gi++
			continue
		}
		b, _ := hex.DecodeString(strings.Fields(f.line)[2])
		p, err := artnet.Decode(b)
		if err != nil || p.Kind != artnet.KindDmx {
			t.Fatalf("%s: an added datagram is not ArtDmx: %q", f.step, f.line)
		}
		key := fmt.Sprintf("%d/%d", p.Dmx.Net, p.Dmx.SubUni)
		if wantBase, ok := base[key]; !ok || !bytes.Equal(p.Dmx.Data, wantBase) {
			next := "end of golden"
			if gi < len(golden) {
				next = golden[gi].line
			}
			t.Fatalf("%s: an added datagram is not the base frame of its universe:\n got: %.120s\nnext golden: %.120s", f.step, f.line, next)
		}
		extra[f.step+" universe "+key]++
	}
	if gi != len(golden) {
		t.Fatalf("golden datagram %d of %d never sent under the base source: %.120s", gi+1, len(golden), golden[gi].line)
	}
	for k, n := range extra {
		t.Logf("base-only datagrams: %3d  %s", n, k)
	}
}
