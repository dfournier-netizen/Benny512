// This file implements Task 2 of the function-aware Rig Check foundation
// (owner brief, stage 1): the attribute taxonomy that groups GDTF attribute
// names (and, via internal/web/patchattrs.go's RDM slot-label bridge, RDM
// SLOT_INFO labels) into the six function groups Rig Check's per-function
// toggles operate on — Dimmer, Position, Colour, Beam, Focus, Shaper — plus
// a catch-all "Other" group for anything this table doesn't yet recognize
// (task rule: unmapped attributes are surfaced, never silently dropped).
//
// Design requirement (owner-approved brief, same shape as
// internal/params/classification.go's pidTiers table, referenced directly
// in the brief): a DATA TABLE, not hardcoded per-attribute logic, so the
// owner extending it later ("he will extend it") is a one-line addition to
// attributeTaxonomy below.
//
// Sourcing (the brief requires this to be stated plainly — "say where you
// got the GDTF attribute names"):
//
//   - CONFIRMED entries are backed by a real, primary source available in
//     this environment: the two GDTF fixture files embedded in the sample
//     .mvr this project was handed
//     ("Custom@Light_Instr_GLP_JDC1_Strobe.gdtf" and
//     "Custom@Light_Instr_Elation_Paladin_Cube.gdtf" — extracted and
//     inspected directly, description.xml's own <AttributeDefinitions>
//     block read verbatim). Those two files' <Attributes> list is small
//     (Dimmer, Pan, Tilt, Zoom, Blade1..4 A/Rot) but is real, spec-shaped
//     GDTF XML, not a guess — and notably each Attribute's own Feature=
//     value in that file self-declares its group (e.g. Blade*
//     Feature="Shapers.Shapers", Pan/Tilt Feature="Position.PanTilt",
//     Zoom Feature="Focus.Focus", Dimmer Feature="Dimmer.Dimmer"),
//     independently corroborating this table's Position/Focus/Shaper/Dimmer
//     grouping choices for exactly the attributes it covers.
//   - One wrinkle worth flagging explicitly: that sample file's own
//     filename is prefixed "Custom@", and it names its framing-shutter
//     blades "Blade1A"/"Blade1Rot" etc, NOT the "Shaper1A"/"Shaper1Rot"
//     names the task brief itself uses as the canonical example
//     ("Shaper*"). Real-world GDTF exports do not reliably use the
//     publicly-defined attribute names — this is direct, primary evidence
//     of exactly that, not a hypothetical — so this table maps BOTH: the
//     brief's canonical "Shaper*" prefix (UNVERIFIED — see below) and the
//     "Blade*" prefix this real file actually uses (CONFIRMED, same
//     Shaper group, on the strength of that file's own Feature=
//     "Shapers.Shapers" declaration).
//   - UNVERIFIED entries are this package's best reading of the publicly
//     documented GDTF standard attribute list (DIN SPEC 15800's Attribute
//     Definitions, e.g. ColorAdd_R/G/B, Gobo1, Frost1, Prism1, Shutter1,
//     Iris, CTO/CTB) from training-time knowledge, NOT checked against a
//     primary GDTF specification document — no GDTF spec PDF was available
//     in this environment (unlike RDM's E1.20, which was — see
//     internal/rdm/slotinfo.go, entirely CONFIRMED against that primary
//     source). Every UNVERIFIED entry is marked as such in
//     attributeTaxonomy below; nothing here claims to be gospel. A wrong
//     UNVERIFIED entry fails safe: the attribute still gets a group (worst
//     case, the wrong one), it is never dropped, and stage 2's UI is
//     expected to show groups/functions as a resolved fact regardless — an
//     owner who spots a wrong grouping fixes one table row, exactly per
//     this file's design goal.
package patch

import "strings"

// AttributeGroup is one of Rig Check's per-function toggle groups.
type AttributeGroup string

// Groups. GroupOther is the catch-all — every attribute this table has no
// prefix entry for lands here rather than being dropped (task rule).
const (
	GroupDimmer   AttributeGroup = "dimmer"
	GroupPosition AttributeGroup = "position"
	GroupColour   AttributeGroup = "colour"
	GroupBeam     AttributeGroup = "beam"
	GroupFocus    AttributeGroup = "focus"
	GroupShaper   AttributeGroup = "shaper"
	GroupOther    AttributeGroup = "other"
)

// AllGroups is every group in a fixed, stable display order — Other last,
// since it is the "everything else" bucket. Stage 2's UI iterates this
// rather than inventing its own group ordering.
var AllGroups = []AttributeGroup{
	GroupDimmer, GroupPosition, GroupColour, GroupBeam, GroupFocus, GroupShaper, GroupOther,
}

// String renders a capitalized display label.
func (g AttributeGroup) String() string {
	switch g {
	case GroupDimmer:
		return "Dimmer"
	case GroupPosition:
		return "Position"
	case GroupColour:
		return "Colour"
	case GroupBeam:
		return "Beam"
	case GroupFocus:
		return "Focus"
	case GroupShaper:
		return "Shaper"
	default:
		return "Other"
	}
}

// attributePrefixEntry is one row of attributeTaxonomy: attribute names
// starting with Prefix belong to Group. GDTF attribute names commonly carry
// a numeric/lettered suffix for multi-instance functions (Gobo1, Gobo2,
// ColorMacro1, Shaper1A, Shaper1Rot, ColorAdd_R) — a prefix match is the
// correct data shape for that, not an exact-string map (which would need a
// new row per numbered instance, defeating the "one-line addition" goal).
type attributePrefixEntry struct {
	Prefix   string
	Group    AttributeGroup
	Verified bool // true only for a CONFIRMED entry — see file doc comment
}

// attributeTaxonomy is the single source of truth GroupForAttribute
// consults, and the table the owner extends (task ask, same shape as
// pidTiers in internal/params/classification.go). Ordering does not affect
// correctness here — every prefix that could plausibly overlap another maps
// to the SAME group (e.g. "Color" is a strict prefix of "ColorAdd_", but
// both are GroupColour) — so this is authored in a readable grouping order,
// not a longest-prefix-first order.
var attributeTaxonomy = []attributePrefixEntry{
	// --- Dimmer -----------------------------------------------------------
	{"Dimmer", GroupDimmer, true}, // CONFIRMED: real GDTF sample, Feature="Dimmer.Dimmer"

	// --- Position -----------------------------------------------------------
	{"Pan", GroupPosition, true},  // CONFIRMED: real GDTF sample, Feature="Position.PanTilt"
	{"Tilt", GroupPosition, true}, // CONFIRMED: real GDTF sample, Feature="Position.PanTilt"
	{"XYZ_X", GroupPosition, false},
	{"XYZ_Y", GroupPosition, false},
	{"XYZ_Z", GroupPosition, false},
	{"Rot_X", GroupPosition, false},
	{"Rot_Y", GroupPosition, false},
	{"Rot_Z", GroupPosition, false},
	{"Position", GroupPosition, false}, // catches PositionEffect/PositionMSpeed etc.

	// --- Colour (GDTF spells it "Color") ------------------------------------
	{"ColorAdd_", GroupColour, false},
	{"ColorSub_", GroupColour, false},
	{"ColorRGB_", GroupColour, false},
	{"ColorMacro", GroupColour, false},
	{"ColorWheel", GroupColour, false},
	{"ColorEffects", GroupColour, false},
	{"CTO", GroupColour, false},
	{"CTB", GroupColour, false},
	{"CTC", GroupColour, false},
	{"HSB_", GroupColour, false},
	{"CIE_", GroupColour, false},
	{"Color", GroupColour, false}, // fallback for any other Color* GDTF attribute

	// --- Beam -----------------------------------------------------------
	{"StaticGobo", GroupBeam, false},
	{"GoboWheel", GroupBeam, false},
	{"Gobo", GroupBeam, false},
	{"Prism", GroupBeam, false},
	{"Iris", GroupBeam, false},
	{"Frost", GroupBeam, false},
	{"Fog", GroupBeam, false},
	{"Haze", GroupBeam, false},
	{"Shutter", GroupBeam, false},
	{"Strobe", GroupBeam, false},
	{"Douser", GroupBeam, false},
	{"BeamEffect", GroupBeam, false},
	{"Effects", GroupBeam, false},
	// Added for stage 2's test-pattern engine (internal/patch/
	// testpattern.go, PatternAnimationSpin): an animation/effects wheel's
	// rotate/index attribute. UNVERIFIED — not present in either real GDTF
	// sample file this package's other entries are CONFIRMED against (see
	// file doc comment); this package's best reading of the public GDTF
	// standard attribute list ("AnimationWheel1", "AnimationIndexRotate",
	// etc, all sharing this prefix).
	{"Animation", GroupBeam, false},

	// --- Focus -----------------------------------------------------------
	{"Zoom", GroupFocus, true}, // CONFIRMED: real GDTF sample, Feature="Focus.Focus"
	{"Focus", GroupFocus, false},
	{"Edge", GroupFocus, false},

	// --- Shaper -----------------------------------------------------------
	// "Blade" is CONFIRMED against the real GDTF sample's own
	// Feature="Shapers.Shapers" declaration for Blade1A..Blade4Rot. "Shaper"
	// is UNVERIFIED — it is the brief's own canonical example prefix and
	// this package's best reading of the public GDTF attribute list, but
	// (see file doc comment) is NOT what the one real sample available in
	// this environment actually uses.
	{"Blade", GroupShaper, true},
	{"Shaper", GroupShaper, false},
	{"BarnDoor", GroupShaper, false},
}

// GroupForAttribute resolves attr (a GDTF standard attribute name, or this
// package's RDM-inferred equivalent — see ChannelFunction.Attribute) to its
// taxonomy group via longest-known-prefix match. An attribute with no
// matching prefix (including the empty string, e.g. a genuinely unresolved
// RDM slot) returns GroupOther — never a zero value that could be mistaken
// for "no group", satisfying the task rule that unknown attributes are
// surfaced, not dropped.
func GroupForAttribute(attr string) AttributeGroup {
	best := GroupOther
	bestLen := -1
	for _, e := range attributeTaxonomy {
		if strings.HasPrefix(attr, e.Prefix) && len(e.Prefix) > bestLen {
			best = e.Group
			bestLen = len(e.Prefix)
		}
	}
	return best
}

// IsVerifiedAttributePrefix reports whether attr matched a CONFIRMED
// (rather than UNVERIFIED-best-reading) row of attributeTaxonomy. Exposed
// for stage 2's UI/tests to be able to flag an UNVERIFIED grouping visibly,
// per the task brief's "mark them UNVERIFIED plainly" instruction — this
// package does not silently launder an unverified guess into the same
// on-screen presentation as a confirmed one.
func IsVerifiedAttributePrefix(attr string) bool {
	bestLen := -1
	verified := false
	for _, e := range attributeTaxonomy {
		if strings.HasPrefix(attr, e.Prefix) && len(e.Prefix) > bestLen {
			bestLen = len(e.Prefix)
			verified = e.Verified
		}
	}
	return verified
}
