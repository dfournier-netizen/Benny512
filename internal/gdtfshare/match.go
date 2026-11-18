package gdtfshare

import (
	"fmt"
	"sort"
	"strings"
)

// MatchQuery is a discovered device's RDM identity. The fields have the
// widths Benny512 already uses for them: rdm.UID.ManufacturerID and
// registry.Fixture.ManufacturerID (16 bits), and params.DeviceInfo's
// DeviceModelID (16), SoftwareVersionID (32) and CurrentPersonality (8).
// SoftwareVersionID and Personality are nil when not known.
type MatchQuery struct {
	ManufacturerID    uint16
	DeviceModelID     uint16
	SoftwareVersionID *uint32
	Personality       *uint8
}

// How a candidate matched.
const (
	MatchedSoftwareVersion = "software-version" // manufacturer + model + software version
	MatchedModel           = "model"            // manufacturer + model
)

// Mode states for a candidate. Only "found" is a certain answer; "likely"
// and "other-versions" are guesses by the most-likely rule (see Match) and
// always carry ModeWarning, ModeBasis and the ranked ModeChoices.
const (
	ModeNotAsked      = "not-asked"      // the query gave no personality
	ModeFound         = "found"          // the mode the file maps it to, with no disagreement
	ModeLikely        = "likely"         // the file's versions disagree: the most likely mode
	ModeOtherVersions = "other-versions" // the device's version maps none: from the file's other versions
	ModeNotMapped     = "not-mapped"     // the file maps no mode for that personality
)

// MatchCandidate is one library file that matches. Entry carries GDTF
// Share's listing, UploaderLabel (Manufacturer/User) included.
type MatchCandidate struct {
	Entry
	// SHA256 is the library file's local checksum.
	SHA256      string       `json:"sha256"`
	MatchedOn   string       `json:"matchedOn"`
	Description *Description `json:"description"`
	// DMXMode is the mode for the query's personality when ModeState is
	// "found", "likely" or "other-versions", else "". ModeInFile false: the
	// file links the personality to a mode it does not have.
	DMXMode    string `json:"dmxMode"`
	ModeState  string `json:"modeState"`
	ModeInFile bool   `json:"modeInFile"`
	// ModeChoices: for a guess ("likely", "other-versions"), every mode
	// the counted versions map the personality to, most likely first (the
	// alternatives stay listed). Empty otherwise.
	ModeChoices []string `json:"modeChoices"`
	// ModeBasis says in words what DMXMode rests on, e.g. "likely: 3 of 4
	// software version(s) that map personality 2 map it to Standard".
	ModeBasis string `json:"modeBasis"`
	// ModeWarning is set for every guess: it says the mode is not certain
	// and, for "other-versions", which versions it came from.
	ModeWarning string `json:"modeWarning"`
	// ModeFromVersions are the software versions (0x%08X, "(no ID)" for a
	// version the file gives no ID) that map the personality to DMXMode.
	ModeFromVersions []string `json:"modeFromVersions"`
	Note             string   `json:"note"`
}

// MatchResult is Match's answer. Candidates is empty, never null, when no
// file matches. NotYetRead and Unreadable count library files Match could
// not look at, so "no match" can be told apart from "not looked".
type MatchResult struct {
	Candidates []MatchCandidate `json:"candidates"`
	NotYetRead int              `json:"notYetRead"`
	Unreadable int              `json:"unreadable"`
	Note       string           `json:"note"`
}

// Match finds library files for a device's RDM identity, from the RDM
// data read out of each file's description.xml (describe.go). It never
// guesses from names: a device whose manufacturer ID and device model ID
// are not both in some file's <FTRDM> gets no candidates (name search is
// Search's job, and the UI says which it used).
//
// Ranking, best first:
//
//  1. "software-version": the file's FTRDM has the device's ManufacturerID
//     and DeviceModelID AND lists the device's SoftwareVersionID. The mode
//     for the device's personality comes from that software version's own
//     DMXPersonality list ("found"). If that version maps none, the mode
//     comes from the file's OTHER software versions by the most-likely rule
//     ("other-versions", with a warning naming them; owner, round 4). The
//     tier stays "software-version": matchedOn says how the FILE matched,
//     which is still by the device's own version; modeState says where the
//     MODE came from, so the fallback shows there without hiding that the
//     file does list the device's firmware.
//  2. "model": ManufacturerID and DeviceModelID match, and the device's
//     software version is not known or not listed in the file. When every
//     listed version that maps the personality maps it to one mode, that is
//     "found"; when they disagree, the most likely mode is given as
//     "likely" with a warning (owner, round 4), never as "found".
//
// THE MOST-LIKELY RULE (rankModes). Over the software versions in play,
// each mode scores the number of distinct software versions that map the
// personality to it. Most versions wins; a tie goes to the mode mapped by
// the highest SoftwareVersionID (a version the file gives no ID counts
// below every ID); a remaining tie goes to the mode name, A to Z (byte
// order). ModeBasis states the count ("k of n"), ModeChoices keeps every
// mode in that order, and ModeWarning says it is a guess.
//
// Within each tier: files uploaded by the manufacturer before user uploads
// (then Unknown; owner decision, round 4), then the most recently modified,
// then rid. Every
// candidate keeps its GDTF Share listing and Manufacturer/User label.
//
// The zero rule (describe.go) holds here too: a file's 0 is not given, so
// a query ID of 0 can match nothing, and a software version of 0 in a file
// is never an exact match.
func (s *Service) Match(q MatchQuery) (MatchResult, error) {
	res := MatchResult{Candidates: make([]MatchCandidate, 0)}
	if !s.Enabled() {
		return res, ErrDisabled
	}
	if q.ManufacturerID == 0 || q.DeviceModelID == 0 {
		res.Note = "An RDM ID of 0 matches no GDTF file: in a GDTF file 0 means the ID is not given"
	}
	s.mu.Lock()
	idx, err := s.indexLocked()
	if err != nil {
		s.mu.Unlock()
		return res, err
	}
	for _, e := range idx.Files {
		switch {
		case needsDescription(e):
			res.NotYetRead++
			continue
		case e.Description.State != DescRead:
			res.Unreadable++
			continue
		}
		if c, ok := matchFile(e, q); ok {
			res.Candidates = append(res.Candidates, c)
		}
	}
	s.mu.Unlock()
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		a, b := res.Candidates[i], res.Candidates[j]
		if ta, tb := tierOf(a.MatchedOn), tierOf(b.MatchedOn); ta != tb {
			return ta < tb
		}
		if la, lb := labelOrder(a.UploaderLabel), labelOrder(b.UploaderLabel); la != lb {
			return la < lb
		}
		if a.LastModified != b.LastModified {
			return a.LastModified > b.LastModified
		}
		return a.RID < b.RID
	})
	return res, nil
}

func tierOf(matchedOn string) int {
	if matchedOn == MatchedSoftwareVersion {
		return 0
	}
	return 1
}

func labelOrder(label string) int {
	switch label {
	case LabelManufacturer:
		return 0
	case LabelUser:
		return 1
	}
	return 2
}

func hexSW(v uint32) string { return fmt.Sprintf("0x%08X", v) }

func matchFile(e IndexEntry, q MatchQuery) (MatchCandidate, bool) {
	r := e.Description.RDM
	if r == nil || r.ManufacturerID == nil || r.DeviceModelID == nil ||
		*r.ManufacturerID != q.ManufacturerID || *r.DeviceModelID != q.DeviceModelID {
		return MatchCandidate{}, false
	}
	c := MatchCandidate{Entry: e.Entry, SHA256: e.SHA256, MatchedOn: MatchedModel, Description: e.Description,
		ModeState: ModeNotAsked, ModeChoices: make([]string, 0), ModeFromVersions: make([]string, 0)}
	var exact *RDMSoftwareVersion
	if q.SoftwareVersionID != nil {
		for i := range r.SoftwareVersions {
			if id := r.SoftwareVersions[i].ID; id != nil && *id == *q.SoftwareVersionID {
				exact = &r.SoftwareVersions[i]
				break
			}
		}
		if exact != nil {
			c.MatchedOn = MatchedSoftwareVersion
		} else {
			c.Note = "This file does not list software version " + hexSW(*q.SoftwareVersionID)
		}
	}
	if q.Personality == nil {
		return c, true
	}
	p := *q.Personality
	addNote := func(n string) {
		if c.Note != "" {
			c.Note += ". "
		}
		c.Note += n
	}
	set := func(state string, votes []modeVote, n int) {
		c.ModeState, c.DMXMode, c.ModeInFile = state, votes[0].name, votes[0].inFile
		c.ModeFromVersions = versionTexts(votes[0].versions)
		if state != ModeFound {
			for _, v := range votes {
				c.ModeChoices = append(c.ModeChoices, v.name)
			}
		}
		c.ModeBasis = fmt.Sprintf("%d of %d software version(s) that map personality %d map it to %s", len(votes[0].versions), n, p, votes[0].name)
		if state != ModeFound {
			c.ModeBasis = "likely: " + c.ModeBasis
		}
		if len(votes) > 1 && len(votes[1].versions) == len(votes[0].versions) {
			if votes[0].top != votes[1].top {
				c.ModeBasis += fmt.Sprintf(" (tied with %s; chosen because the highest software version, %s, maps it)", votes[1].name, hexSW(uint32(votes[0].top)))
			} else {
				c.ModeBasis += fmt.Sprintf(" (tied with %s; chosen by mode name, A to Z)", votes[1].name)
			}
		}
	}
	if exact != nil {
		own, n := rankModes([]RDMSoftwareVersion{*exact}, p)
		switch {
		case len(own) == 1:
			set(ModeFound, own, n)
			c.ModeBasis = fmt.Sprintf("software version %s maps personality %d to %s", hexSW(*exact.ID), p, own[0].name)
		case len(own) > 1:
			set(ModeLikely, own, n)
			c.ModeWarning = fmt.Sprintf("Software version %s maps personality %d to more than one mode (%s); %s is a guess, not a certain answer",
				hexSW(*exact.ID), p, strings.Join(c.ModeChoices, ", "), c.DMXMode)
		default:
			others := make([]RDMSoftwareVersion, 0, len(r.SoftwareVersions))
			for i := range r.SoftwareVersions {
				if &r.SoftwareVersions[i] != exact {
					others = append(others, r.SoftwareVersions[i])
				}
			}
			votes, n := rankModes(others, p)
			if len(votes) == 0 {
				c.ModeState = ModeNotMapped
				addNote(fmt.Sprintf("This file maps no mode for personality %d in any software version", p))
				break
			}
			set(ModeOtherVersions, votes, n)
			c.ModeBasis = strings.Replace(c.ModeBasis, " software version(s)", " other software version(s)", 1)
			c.ModeWarning = fmt.Sprintf("Software version %s (the device's) maps no mode for personality %d; %s comes from software version(s) %s of this file, not from %s, so it is a guess",
				hexSW(*exact.ID), p, c.DMXMode, strings.Join(c.ModeFromVersions, ", "), hexSW(*exact.ID))
		}
	} else {
		votes, n := rankModes(r.SoftwareVersions, p)
		switch {
		case len(votes) == 0:
			c.ModeState = ModeNotMapped
			addNote(fmt.Sprintf("This file maps no mode for personality %d", p))
		case len(votes) == 1:
			set(ModeFound, votes, n)
		default:
			set(ModeLikely, votes, n)
			c.ModeWarning = fmt.Sprintf("This file's software versions disagree on personality %d (%s); %s is the most likely, not a certain answer",
				p, strings.Join(c.ModeChoices, ", "), c.DMXMode)
		}
	}
	if c.ModeState == ModeFound && !c.ModeInFile {
		addNote(fmt.Sprintf("The file links personality %d to mode %q, which is not among its DMX modes", p, c.DMXMode))
	}
	return c, true
}

// modeVote is one mode's support for a personality: the distinct software
// versions mapping it there (nil = a version with no ID).
type modeVote struct {
	name     string
	versions []*uint32
	top      int64 // highest version ID among them; -1 when none has an ID
	inFile   bool
}

// rankModes applies the most-likely rule (Match's doc comment) to the
// personality p over vs. It returns the modes most likely first and n, the
// number of versions that map p to any mode.
func rankModes(vs []RDMSoftwareVersion, p uint8) ([]modeVote, int) {
	by := map[string]*modeVote{}
	n := 0
	for i := range vs {
		v := &vs[i]
		counted := map[string]bool{}
		for _, pe := range v.Personalities {
			if pe.Value == nil || *pe.Value != p || pe.DMXMode == "" {
				continue
			}
			mv := by[pe.DMXMode]
			if mv == nil {
				mv = &modeVote{name: pe.DMXMode, top: -1}
				by[pe.DMXMode] = mv
			}
			mv.inFile = mv.inFile || pe.ModeInFile
			if counted[pe.DMXMode] {
				continue
			}
			counted[pe.DMXMode] = true
			mv.versions = append(mv.versions, v.ID)
			if v.ID != nil && int64(*v.ID) > mv.top {
				mv.top = int64(*v.ID)
			}
		}
		if len(counted) > 0 {
			n++
		}
	}
	out := make([]modeVote, 0, len(by))
	for _, mv := range by {
		out = append(out, *mv)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.versions) != len(b.versions) {
			return len(a.versions) > len(b.versions)
		}
		if a.top != b.top {
			return a.top > b.top
		}
		return a.name < b.name
	})
	return out, n
}

// versionTexts writes software versions lowest first, "(no ID)" before any.
func versionTexts(ids []*uint32) []string {
	sorted := append([]*uint32(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i] == nil || sorted[j] == nil {
			return sorted[i] == nil && sorted[j] != nil
		}
		return *sorted[i] < *sorted[j]
	})
	out := make([]string, 0, len(sorted))
	for _, id := range sorted {
		if id == nil {
			out = append(out, "(no ID)")
		} else {
			out = append(out, hexSW(*id))
		}
	}
	return out
}
