package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTodDetail_MarshalJSON_MeaningfulZeroesSurvive closes the last known
// instance in this package of the defect class documented on
// patch.ChannelFunction and listed in the handoff: `omitempty` on a numeric
// field whose zero is real data.
//
// An ArtTodData packet's four decoded fields all have meaningful zeroes:
// an empty rig genuinely reports uidTotal 0, the first block of a Table of
// Devices is block 0, a responder answering rdmVersion 0 is telling us
// something real about itself, and a port of 0 is out of Art-Net's 1-4 range
// and therefore MORE worth showing than a valid one, not less.
//
// This is not hypothetical. RDM-LOG24 — the bench capture of eight GLP JDC-1s
// — contains real ArtTodData packets carrying uidTotal=0 and blockCount=0
// together:
//
//	ArtTodData  command=0x00 (TodFull)  net=0
//	rdmVersion=1 port=1 uidTotal=0 blockCount=0
//
// Under the old tags both of those keys vanished from the JSON entirely and
// the client saw `undefined`, which is how this class has bitten this project
// nine times.
//
// Asserted on the MARSHALLED BYTES rather than the struct, because a struct
// field reading 0 is exactly what an omitted key unmarshals to — a
// field-level assertion passes vacuously against this exact bug.
func TestTodDetail_MarshalJSON_MeaningfulZeroesSurvive(t *testing.T) {
	// The LOG24 shape, with every numeric at its meaningful zero.
	d := TodDetail{
		SubKind:     "Data",
		Net:         0,
		Command:     0x00,
		CommandName: "TodFull",
		Addresses:   []byte{0},
		RdmVersion:  0,
		Port:        0,
		UidTotal:    0,
		BlockCount:  0,
	}

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	for _, want := range []string{
		`"rdmVersion":0`,
		`"port":0`,
		`"uidTotal":0`,
		`"blockCount":0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled TodDetail is missing %s — a real wire reading was erased\ngot: %s", want, got)
		}
	}

	// And the round trip must be lossless, which is the property the client
	// actually depends on: an absent key and a present zero must not both
	// arrive as the same thing.
	var back TodDetail
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.UidTotal != 0 || back.BlockCount != 0 || back.Port != 0 || back.RdmVersion != 0 {
		t.Errorf("round trip altered a zero: %+v", back)
	}

	// A non-zero case, to prove the tags were not simply dropped in a way
	// that breaks ordinary values — the actual LOG24 line.
	d2 := TodDetail{SubKind: "Data", RdmVersion: 1, Port: 1, UidTotal: 8, BlockCount: 0}
	b2, err := json.Marshal(d2)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rdmVersion":1`, `"port":1`, `"uidTotal":8`, `"blockCount":0`} {
		if !strings.Contains(string(b2), want) {
			t.Errorf("marshalled TodDetail is missing %s\ngot: %s", want, string(b2))
		}
	}
}
