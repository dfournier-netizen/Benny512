package patch

import "strings"

// Short fixture-type names for fader labels (J1b, owner 2026-10-12 20:11:
// "JDC1 · Plate" instead of "Custom GLP JDC1 Strobe · Plate"; the full name
// stays in the accessible label and tooltip).
//
// SOURCE, in order:
//  1. The GDTF FixtureType ShortName ("Shortened name of the fixture
//     type", GDTF 1.2 / DIN SPEC 15800 FixtureType attributes) the importer
//     read — Entry.FixtureTypeShortName, kept in the show file's profile
//     cache. The real Paladin Cube file says "PalCube", the Robe BMFL
//     "BMFL Spot". gdtfparse.js falls back to the FixtureType Name (the
//     model, manufacturer excluded) when ShortName is empty.
//  2. Otherwise (a show imported before J1b, a library or hand-entered
//     entry) the fixture type with a leading "Custom" and then a KNOWN
//     manufacturer prefix stripped. The stored fixture type is
//     "Manufacturer Model" joined with one space, and the manufacturer is
//     not stored on its own, so only names on the list below are
//     recognised; anything else is kept whole rather than guessed.
//
// A stored short name also loses a leading "Custom". A name that would be
// left empty is kept whole.

// knownManufacturers: GDTF Manufacturer values seen in real files or in the
// owner's shows, longest first where one is a prefix of another.
var knownManufacturers = []string{
	"Robe lighting s.r.o.", "Robe Lighting",
	"Martin Professional", "Chauvet Professional", "Chauvet DJ", "Elation Professional",
	"High End Systems", "Clay Paky", "Claypaky", "Vari-Lite", "Vari*Lite", "Philips Vari-Lite",
	"Robert Juliat", "JB-Lighting", "JB Lighting", "American DJ", "Chroma-Q", "Ayrton",
	"Martin", "Chauvet", "Elation", "Robe", "GLP", "ETC", "SGM", "Astera", "ACME", "ADJ",
	"Prolights", "Showtec", "Cameo",
}

// ShortTypeName is the short name of fixture type typ whose GDTF short name
// is stored ("" = not known) — see the file comment.
func ShortTypeName(typ, stored string) string {
	if s := stripCustom(strings.TrimSpace(stored)); s != "" {
		return s
	}
	full := strings.TrimSpace(typ)
	s := stripCustom(full)
	for _, m := range knownManufacturers {
		if len(s) > len(m) && strings.EqualFold(s[:len(m)], m) && s[len(m)] == ' ' {
			s = strings.TrimSpace(s[len(m):])
			break
		}
	}
	if s == "" {
		return full
	}
	return s
}

func stripCustom(s string) string {
	if len(s) > 7 && strings.EqualFold(s[:7], "custom ") {
		return strings.TrimSpace(s[7:])
	}
	return s
}
