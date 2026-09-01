package patch

import "testing"

func entryWithFunctions(id string, cfs map[uint16]ChannelFunction) Entry {
	return Entry{ID: id, ChannelFunctions: cfs}
}

// TestResolveEntryGroups_MergesCoarseAndFine: a 16-bit Pan (coarse offset 1,
// fine offset 2, both attribute "Pan") must resolve to ONE ResolvedFunction
// with both offsets, not two separate ones — this is the behavior stage 2's
// UI depends on to show "Pan" once per fixture, not once per DMX byte.
func TestResolveEntryGroups_MergesCoarseAndFine(t *testing.T) {
	e := entryWithFunctions("e1", map[uint16]ChannelFunction{
		1: {Source: SourceGDTF, Attribute: "Pan"},
		2: {Source: SourceGDTF, Attribute: "Pan"},
	})
	groups := ResolveEntryGroups(e)
	if len(groups) != 1 || groups[0].Group != GroupPosition {
		t.Fatalf("groups = %+v, want one GroupPosition", groups)
	}
	fns := groups[0].Functions
	if len(fns) != 1 {
		t.Fatalf("functions = %+v, want 1 merged Pan", fns)
	}
	if fns[0].Attribute != "Pan" || len(fns[0].Offsets) != 2 {
		t.Fatalf("Pan function = %+v, want 2 offsets", fns[0])
	}
	if fns[0].Offsets[0] != 1 || fns[0].Offsets[1] != 2 {
		t.Errorf("offsets not ascending: %v", fns[0].Offsets)
	}
}

// TestResolveEntryGroups_DifferentSourcesNotMerged: an attribute resolved
// from two different provenances (shouldn't normally happen on one entry,
// but must never silently merge if it does — decision 3's hard constraint)
// stays as two separate ResolvedFunctions.
func TestResolveEntryGroups_DifferentSourcesNotMerged(t *testing.T) {
	e := entryWithFunctions("e1", map[uint16]ChannelFunction{
		1: {Source: SourceGDTF, Attribute: "Dimmer"},
		2: {Source: SourceRDMInferred, Attribute: "Dimmer"},
	})
	groups := ResolveEntryGroups(e)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	fns := groups[0].Functions
	if len(fns) != 2 {
		t.Fatalf("expected 2 unmerged functions (different Source), got %+v", fns)
	}
}

// TestResolveEntryGroups_SkipsAbsent: a zero-valued ChannelFunction
// (Source==SourceAbsent) must never surface as a resolved function.
func TestResolveEntryGroups_SkipsAbsent(t *testing.T) {
	e := entryWithFunctions("e1", map[uint16]ChannelFunction{
		1: {}, // zero value: Source == SourceAbsent
	})
	if groups := ResolveEntryGroups(e); len(groups) != 0 {
		t.Errorf("expected no groups for an absent-only entry, got %+v", groups)
	}
}

// TestResolveEntryGroups_UnmappedAttributeGoesToOther: a present-but-
// unrecognized attribute (e.g. an RDM slot this package's bridge table
// couldn't map) still surfaces, in GroupOther — never dropped.
func TestResolveEntryGroups_UnmappedAttributeGoesToOther(t *testing.T) {
	e := entryWithFunctions("e1", map[uint16]ChannelFunction{
		1: {Source: SourceRDMInferred, Attribute: ""},
	})
	groups := ResolveEntryGroups(e)
	if len(groups) != 1 || groups[0].Group != GroupOther {
		t.Fatalf("groups = %+v, want one GroupOther", groups)
	}
}

// TestResolveEntryGroups_NoChannelFunctionsReturnsEmpty: an entry with no
// resolved data at all must not have groups padded in to look
// function-aware — an empty result, not a lie.
func TestResolveEntryGroups_NoChannelFunctionsReturnsEmpty(t *testing.T) {
	e := Entry{ID: "e1", ChannelFunctions: map[uint16]ChannelFunction{}}
	if groups := ResolveEntryGroups(e); len(groups) != 0 {
		t.Errorf("expected empty groups, got %+v", groups)
	}
}

// TestSummarizeSelection_MixedRig is decision (2) made concrete: "12 of 40
// fixtures have Shapers" — here with a small fixture count, but the same
// shape: some fixtures have a Shaper function, some don't, and the summary
// reports the count without blocking or filtering anything.
func TestSummarizeSelection_MixedRig(t *testing.T) {
	entries := []Entry{
		entryWithFunctions("e1", map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Dimmer"},
			2: {Source: SourceGDTF, Attribute: "Blade1A"}, // Shaper
		}),
		entryWithFunctions("e2", map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Dimmer"},
		}),
		entryWithFunctions("e3", map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Dimmer"},
			2: {Source: SourceGDTF, Attribute: "Blade2A"}, // Shaper, different function name
		}),
		Entry{ID: "e4"}, // no channel functions at all — still counts toward TotalFixtures
	}
	summary := SummarizeSelection(entries)
	if summary.TotalFixtures != 4 {
		t.Fatalf("TotalFixtures = %d, want 4", summary.TotalFixtures)
	}

	var dimmerCount, shaperCount int
	var shaperFnCount int
	for _, g := range summary.Groups {
		switch g.Group {
		case GroupDimmer:
			dimmerCount = g.Count
		case GroupShaper:
			shaperCount = g.Count
			shaperFnCount = len(g.Functions)
		}
	}
	if dimmerCount != 3 {
		t.Errorf("Dimmer group count = %d, want 3 of 4", dimmerCount)
	}
	if shaperCount != 2 {
		t.Errorf("Shaper group count = %d, want 2 of 4 (\"2 of 4 fixtures have Shapers\")", shaperCount)
	}
	if shaperFnCount != 2 {
		t.Errorf("Shaper group has %d distinct functions, want 2 (Blade1A, Blade2A each once)", shaperFnCount)
	}
}

// TestSummarizeSelection_EmptySelection: zero entries must not panic and
// must report TotalFixtures=0 with no groups.
func TestSummarizeSelection_EmptySelection(t *testing.T) {
	summary := SummarizeSelection(nil)
	if summary.TotalFixtures != 0 {
		t.Errorf("TotalFixtures = %d, want 0", summary.TotalFixtures)
	}
	if len(summary.Groups) != 0 {
		t.Errorf("Groups = %+v, want empty", summary.Groups)
	}
}

// TestSummarizeSelection_GroupCountIsUnionNotSum: a fixture with BOTH Pan
// and Tilt (both GroupPosition) must count once toward Position's Count,
// not twice — Count is "fixtures with the group present", matching the
// "12 of 40 fixtures have Shapers" phrasing (fixtures, not functions).
func TestSummarizeSelection_GroupCountIsUnionNotSum(t *testing.T) {
	entries := []Entry{
		entryWithFunctions("e1", map[uint16]ChannelFunction{
			1: {Source: SourceGDTF, Attribute: "Pan"},
			2: {Source: SourceGDTF, Attribute: "Tilt"},
		}),
	}
	summary := SummarizeSelection(entries)
	for _, g := range summary.Groups {
		if g.Group == GroupPosition && g.Count != 1 {
			t.Errorf("Position Count = %d, want 1 (union, not sum)", g.Count)
		}
	}
}
