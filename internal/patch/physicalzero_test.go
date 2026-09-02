package patch

import (
	"encoding/json"
	"strings"
	"testing"
)

// A GDTF PhysicalFrom of 0 is real data, not an absent value: a dimmer's
// physical range starts at 0%, a frost's at 0, a zoom's beam angle can
// legitimately be 0 at one end. Marshalling it away is this project's
// oldest defect class (see entry.go's "NEVER omitempty on a numeric whose
// zero is real" rule) and this is the last instance of it in the GDTF
// channel-data family.
//
// The asymmetry is what makes it a seam rather than a cosmetic issue:
// internal/web's channelFunctionRequest/channelSetRequest declare these two
// fields WITHOUT omitempty, so the browser sends "physicalFrom":0 and the
// server accepts it — then serves it back with the key missing entirely.
// A value survives the trip into the server and is erased on the way out,
// so client and server disagree about a channel whose physical range starts
// at zero. The Fixture Library's export carries patch.ChannelFunction
// verbatim, so the same zero also vanishes from a library file handed to a
// coworker.
//
// Asserted on the MARSHALLED BYTES: a struct field reading 0.0 is exactly
// what an omitted key unmarshals to, so a struct-level assertion passes
// vacuously against this bug.
func TestChannelFunction_MarshalJSON_ZeroPhysicalRangeSurvives(t *testing.T) {
	cf := ChannelFunction{
		Source:       SourceGDTF,
		Attribute:    "Dimmer",
		FunctionName: "Dimmer",
		DMXFrom:      0,
		DMXTo:        255,
		PhysicalFrom: 0, // 0% — a real, stated physical value
		PhysicalTo:   100,
		ChannelSets: []ChannelSet{{
			Name:         "Closed",
			DMXFrom:      0,
			PhysicalFrom: 0, // likewise real
			PhysicalTo:   0, // a set whose whole physical range is zero
		}},
	}
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`"physicalFrom":0`,
		`"physicalTo":0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled ChannelFunction is missing %s — a stated zero physical value was erased\ngot: %s", want, got)
		}
	}

	// And the round trip must be lossless in both directions, which is the
	// property the browser actually depends on.
	var back ChannelFunction
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.PhysicalTo != 100 || back.PhysicalFrom != 0 {
		t.Errorf("round trip altered the physical range: got from=%v to=%v", back.PhysicalFrom, back.PhysicalTo)
	}
}
