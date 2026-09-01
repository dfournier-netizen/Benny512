package patch

import "testing"

// TestGroupForAttribute_ConfirmedNames pins the CONFIRMED entries (real
// GDTF sample evidence, see taxonomy.go's doc comment) to their groups.
func TestGroupForAttribute_ConfirmedNames(t *testing.T) {
	cases := []struct {
		attr string
		want AttributeGroup
	}{
		{"Dimmer", GroupDimmer},
		{"Pan", GroupPosition},
		{"Tilt", GroupPosition},
		{"Zoom", GroupFocus},
		{"Blade1A", GroupShaper},
		{"Blade1Rot", GroupShaper},
	}
	for _, c := range cases {
		if got := GroupForAttribute(c.attr); got != c.want {
			t.Errorf("GroupForAttribute(%q) = %v, want %v", c.attr, got, c.want)
		}
		if !IsVerifiedAttributePrefix(c.attr) {
			t.Errorf("IsVerifiedAttributePrefix(%q) = false, want true (CONFIRMED entry)", c.attr)
		}
	}
}

// TestGroupForAttribute_UnverifiedNames covers the brief's canonical
// example names this package's best-reading (not primary-source-checked)
// table maps — these must still resolve to a sensible group, and must be
// flagged as unverified.
func TestGroupForAttribute_UnverifiedNames(t *testing.T) {
	cases := []struct {
		attr string
		want AttributeGroup
	}{
		{"ColorAdd_R", GroupColour},
		{"Gobo1", GroupBeam},
		{"Frost1", GroupBeam},
		{"Prism1", GroupBeam},
		{"Shaper1A", GroupShaper},
		{"Iris", GroupBeam},
		{"CTO", GroupColour},
	}
	for _, c := range cases {
		if got := GroupForAttribute(c.attr); got != c.want {
			t.Errorf("GroupForAttribute(%q) = %v, want %v", c.attr, got, c.want)
		}
		if IsVerifiedAttributePrefix(c.attr) {
			t.Errorf("IsVerifiedAttributePrefix(%q) = true, want false (UNVERIFIED entry)", c.attr)
		}
	}
}

// TestGroupForAttribute_UnknownIsOther is the task's explicit rule: an
// attribute this table has never heard of is surfaced as Other, never
// dropped and never mistaken for a zero/absent value.
func TestGroupForAttribute_UnknownIsOther(t *testing.T) {
	for _, attr := range []string{"", "SomeVendorProprietaryThing", "Widget42"} {
		if got := GroupForAttribute(attr); got != GroupOther {
			t.Errorf("GroupForAttribute(%q) = %v, want GroupOther", attr, got)
		}
	}
}

// TestAttributeGroup_String_NeverEmpty guards stage 2's UI: every group
// (including the zero value, in case a caller forgets to set one) must
// render SOME label, never an empty string a UI would show as a blank
// toggle.
func TestAttributeGroup_String_NeverEmpty(t *testing.T) {
	for _, g := range append(append([]AttributeGroup{}, AllGroups...), AttributeGroup("")) {
		if g.String() == "" {
			t.Errorf("AttributeGroup(%q).String() is empty", g)
		}
	}
}

// TestAllGroups_ContainsEverySixPlusOther pins the exact set the task
// brief names (Dimmer, Position, Colour, Beam, Focus, Shaper) plus Other.
func TestAllGroups_ContainsEverySixPlusOther(t *testing.T) {
	want := map[AttributeGroup]bool{
		GroupDimmer: true, GroupPosition: true, GroupColour: true,
		GroupBeam: true, GroupFocus: true, GroupShaper: true, GroupOther: true,
	}
	if len(AllGroups) != len(want) {
		t.Fatalf("AllGroups has %d entries, want %d", len(AllGroups), len(want))
	}
	for _, g := range AllGroups {
		if !want[g] {
			t.Errorf("unexpected group in AllGroups: %v", g)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Errorf("AllGroups missing: %v", want)
	}
}
