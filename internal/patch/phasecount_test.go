package patch

import "testing"

func TestPhaseCountPrecedenceAndCoarseFine(t *testing.T) {
	e := Entry{ChannelFunctions: map[uint16]ChannelFunction{
		1: {Source: SourceGDTF, Attribute: "ColorAdd_R", GeometryInstance: "cell:0"},
		2: {Source: SourceGDTF, Attribute: "ColorAdd_R", GeometryInstance: "cell:0"},
		3: {Source: SourceGDTF, Attribute: "ColorAdd_G", GeometryInstance: "cell:0"},
		4: {Source: SourceGDTF, Attribute: "ColorAdd_R", GeometryInstance: "cell:3"},
	}}
	if n, source := PhaseCountFor(e, 76); n != 2 || source != "GDTF geometry estimate" {
		t.Fatalf("%d %s", n, source)
	}
	e.PhaseCount = 16
	if n, _ := PhaseCountFor(e, 76); n != 16 {
		t.Fatal(n)
	}
	if n, _ := PhaseCountFor(Entry{}, 12); n != 12 {
		t.Fatal(n)
	}
	if n, _ := PhaseCountFor(Entry{}, 0); n != 1 {
		t.Fatal(n)
	}
}
