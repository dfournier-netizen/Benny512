package patch

import (
	"encoding/json"
	"strings"
	"testing"
)

func findingsOfKind(fs []Finding, kind FindingKind) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func hasEntryID(f Finding, id string) bool {
	for _, e := range f.EntryIDs {
		if e == id {
			return true
		}
	}
	return false
}

func TestDetectCollisions_NoOverlap(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 10},
		{ID: "b", Universe: 0, StartAddress: 11, Footprint: 5}, // adjacent-but-not-overlapping
	}}
	findings := DetectCollisions(p)
	if got := findingsOfKind(findings, KindOverlap); len(got) != 0 {
		t.Errorf("expected no overlap findings for adjacent ranges, got %+v", got)
	}
}

// TestDetectCollisions_CollisionFreeMarshalsToEmptyArray guards against a
// nil-slice regression: a collision-free patch must marshal to JSON `[]`,
// not `null`. len(findings)==0 is true for both a nil slice and an
// allocated-empty slice, so that assertion alone would pass vacuously
// against the bug this test exists to catch — asserting on the actual
// marshalled bytes is the only thing that distinguishes them. This
// reproduces the bug that crashed the Patch screen with "Cannot read
// properties of null (reading 'length')" on every clean patch, because the
// server previously returned `null` for a patch with zero findings.
func TestDetectCollisions_CollisionFreeMarshalsToEmptyArray(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 10},
	}}
	findings := DetectCollisions(p)
	if findings == nil {
		t.Fatal("DetectCollisions returned a nil slice for a collision-free patch; want a non-nil empty slice")
	}
	got, err := json.Marshal(findings)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("json.Marshal(DetectCollisions(...)) = %s, want []", got)
	}
}

func TestDetectCollisions_AdjacentButNotOverlapping(t *testing.T) {
	// a occupies 1-10, b occupies 11-15: end of a is exactly start of b
	// minus one. Must NOT be flagged.
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 1, StartAddress: 1, Footprint: 10},
		{ID: "b", Universe: 1, StartAddress: 11, Footprint: 5},
	}}
	findings := DetectCollisions(p)
	if got := findingsOfKind(findings, KindOverlap); len(got) != 0 {
		t.Fatalf("adjacent ranges falsely flagged as overlapping: %+v", got)
	}
}

func TestDetectCollisions_PartialOverlap(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 1, StartAddress: 1, Footprint: 10}, // 1-10
		{ID: "b", Universe: 1, StartAddress: 5, Footprint: 10}, // 5-14, overlaps 5-10
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 overlap finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.ChannelStart != 5 || f.ChannelEnd != 10 {
		t.Errorf("overlap range = %d-%d, want 5-10", f.ChannelStart, f.ChannelEnd)
	}
	if !hasEntryID(f, "a") || !hasEntryID(f, "b") {
		t.Errorf("overlap finding should name both entries: %+v", f)
	}
}

// TestDetectCollisions_OverlapMessageOmitsUniverseNumber guards the bug this
// comment's sibling doc comment on Finding.Message describes: the collision
// banner (Patch table shows universe N at the configured display base) and
// this Message must never disagree about which universe a fixture is in.
// The only way to guarantee that for every caller — including one that
// falls back to Message verbatim — is for Message to never state a bare
// (display-base-naive) universe number in the first place. Universe 7 is
// chosen deliberately: at display base 0 the correct on-screen number is 7,
// at base 1 it is 8 — if this test only checked for "in universe 6" (a
// stale 0-based-plus-one guess) it could pass by accident.
func TestDetectCollisions_OverlapMessageOmitsUniverseNumber(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Name: "Practical 1", Universe: 7, StartAddress: 100, Footprint: 10}, // 100-109
		{ID: "b", Name: "Practical 2", Universe: 7, StartAddress: 105, Footprint: 10}, // 105-114, overlaps 105-109
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 overlap finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Universe != 7 {
		t.Fatalf("finding.Universe = %d, want 7 (canonical, structured field)", f.Universe)
	}
	if strings.Contains(f.Message, "7") || strings.Contains(strings.ToLower(f.Message), "universe") {
		t.Errorf("Message must never state a bare universe number (display base is not this package's concern): got %q", f.Message)
	}
	if !strings.Contains(f.Message, "Practical 1") || !strings.Contains(f.Message, "Practical 2") {
		t.Errorf("Message should still name both entries: got %q", f.Message)
	}
}

func TestDetectCollisions_FullContainment(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 1, StartAddress: 1, Footprint: 20}, // 1-20
		{ID: "b", Universe: 1, StartAddress: 5, Footprint: 3},  // 5-7, fully inside a
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 overlap finding for full containment, got %d: %+v", len(findings), findings)
	}
	if findings[0].ChannelStart != 5 || findings[0].ChannelEnd != 7 {
		t.Errorf("overlap range = %d-%d, want 5-7 (the contained range)", findings[0].ChannelStart, findings[0].ChannelEnd)
	}
}

func TestDetectCollisions_IdenticalRanges(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 2, StartAddress: 100, Footprint: 8},
		{ID: "b", Universe: 2, StartAddress: 100, Footprint: 8},
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 overlap finding for identical ranges, got %d: %+v", len(findings), findings)
	}
	if findings[0].ChannelStart != 100 || findings[0].ChannelEnd != 107 {
		t.Errorf("overlap range = %d-%d, want 100-107", findings[0].ChannelStart, findings[0].ChannelEnd)
	}
}

func TestDetectCollisions_DifferentUniversesNeverOverlap(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 20},
		{ID: "b", Universe: 1, StartAddress: 1, Footprint: 20}, // same channels, different universe
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 0 {
		t.Errorf("entries in different universes must never collide, got %+v", findings)
	}
}

func TestDetectCollisions_ZeroFootprint(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 0},
	}}
	findings := DetectCollisions(p)
	zf := findingsOfKind(findings, KindZeroFootprint)
	if len(zf) != 1 {
		t.Fatalf("expected 1 zero-footprint finding, got %d", len(zf))
	}
	if zf[0].Severity != SeverityWarning {
		t.Errorf("zero footprint should be a Warning (may be a legitimate splitter/gateway), got %s", zf[0].Severity)
	}
}

func TestDetectCollisions_ZeroFootprintExcludedFromOverlap(t *testing.T) {
	// Two zero-footprint entries at the "same" address must never be
	// reported as overlapping — they occupy no channels.
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 0},
		{ID: "b", Universe: 0, StartAddress: 1, Footprint: 0},
	}}
	findings := findingsOfKind(DetectCollisions(p), KindOverlap)
	if len(findings) != 0 {
		t.Errorf("zero-footprint entries must never overlap, got %+v", findings)
	}
}

func TestDetectCollisions_Overflow(t *testing.T) {
	cases := []struct {
		name         string
		e            Entry
		wantOverflow bool
	}{
		{"address 512 footprint 1 fits exactly", Entry{ID: "a", StartAddress: 512, Footprint: 1}, false},
		{"address 512 footprint >1 overflows", Entry{ID: "a", StartAddress: 512, Footprint: 4}, true},
		{"address 500 footprint 13 fits exactly", Entry{ID: "a", StartAddress: 500, Footprint: 13}, false},
		{"address 500 footprint 14 overflows by one", Entry{ID: "a", StartAddress: 500, Footprint: 14}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Patch{Entries: []Entry{c.e}}
			got := findingsOfKind(DetectCollisions(p), KindOverflow)
			if (len(got) > 0) != c.wantOverflow {
				t.Errorf("overflow findings = %+v, want present=%v", got, c.wantOverflow)
			}
		})
	}
}

func TestDetectCollisions_InvalidAddress(t *testing.T) {
	cases := []struct {
		name  string
		start uint16
		want  bool
	}{
		{"zero is invalid", 0, true},
		{"513 is invalid", 513, true},
		{"1 is valid", 1, false},
		{"512 is valid", 512, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Patch{Entries: []Entry{{ID: "a", StartAddress: c.start, Footprint: 1}}}
			got := findingsOfKind(DetectCollisions(p), KindInvalidAddress)
			if (len(got) > 0) != c.want {
				t.Errorf("start=%d invalid findings=%+v, want present=%v", c.start, got, c.want)
			}
		})
	}
}

func TestDetectCollisions_DuplicateFixtureNumber(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", FixtureNumber: "101", Universe: 0, StartAddress: 1, Footprint: 1},
		{ID: "b", FixtureNumber: "101", Universe: 5, StartAddress: 50, Footprint: 1},
		{ID: "c", FixtureNumber: "102", Universe: 0, StartAddress: 2, Footprint: 1},
	}}
	findings := findingsOfKind(DetectCollisions(p), KindDuplicateFixtureNumber)
	if len(findings) != 1 {
		t.Fatalf("expected 1 duplicate-fixture-number finding, got %d: %+v", len(findings), findings)
	}
	if !hasEntryID(findings[0], "a") || !hasEntryID(findings[0], "b") {
		t.Errorf("duplicate finding should name entries a and b: %+v", findings[0])
	}
	if hasEntryID(findings[0], "c") {
		t.Errorf("entry c has a different fixture number and must not be included: %+v", findings[0])
	}
}

func TestDetectCollisions_EmptyFixtureNumberNeverFlagged(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 1},
		{ID: "b", Universe: 0, StartAddress: 2, Footprint: 1},
	}}
	findings := findingsOfKind(DetectCollisions(p), KindDuplicateFixtureNumber)
	if len(findings) != 0 {
		t.Errorf("entries with no fixture number set must never be flagged as duplicates, got %+v", findings)
	}
}

func TestDetectCollisions_DeterministicOrder(t *testing.T) {
	p := Patch{Entries: []Entry{
		{ID: "a", Universe: 0, StartAddress: 1, Footprint: 10},
		{ID: "b", Universe: 0, StartAddress: 5, Footprint: 10},
	}}
	f1 := DetectCollisions(p)
	f2 := DetectCollisions(p)
	if len(f1) != len(f2) {
		t.Fatalf("non-deterministic finding count: %d vs %d", len(f1), len(f2))
	}
	for i := range f1 {
		if f1[i].Kind != f2[i].Kind || f1[i].Message != f2[i].Message {
			t.Errorf("findings reordered between calls at index %d: %+v vs %+v", i, f1[i], f2[i])
		}
	}
}
