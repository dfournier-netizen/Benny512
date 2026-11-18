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

// Mode states for a candidate.
const (
	ModeNotAsked  = "not-asked"  // the query gave no personality
	ModeFound     = "found"      // DMXMode is the file's mode for it
	ModeNotMapped = "not-mapped" // the file maps no mode for that personality
	ModeDiffers   = "differs"    // the file's software versions disagree; see ModeChoices
)

// MatchCandidate is one library file that matches. Entry carries GDTF
// Share's listing, UploaderLabel (Manufacturer/User) included.
type MatchCandidate struct {
	Entry
	MatchedOn   string       `json:"matchedOn"`
	Description *Description `json:"description"`
	// DMXMode is the mode for the query's personality when ModeState is
	// "found", else "". ModeInFile false: the file links the personality
	// to a mode it does not have.
	DMXMode     string   `json:"dmxMode"`
	ModeState   string   `json:"modeState"`
	ModeInFile  bool     `json:"modeInFile"`
	ModeChoices []string `json:"modeChoices"`
	Note        string   `json:"note"`
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
//     DMXPersonality list only; if that version maps no mode for it, the
//     answer is "not-mapped" (the note names what other versions map, but
//     no other version's mode is given as this one's).
//  2. "model": ManufacturerID and DeviceModelID match, and the device's
//     software version is not known or not listed in the file. The mode is
//     given only when every listed software version that maps the
//     personality maps it to the same mode ("found"); when they disagree
//     the answer is "differs" with the choices, never a pick.
//
// Within each tier: files uploaded by the manufacturer before user uploads
// (then Unknown), then the most recently modified, then rid. Every
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
	c := MatchCandidate{Entry: e.Entry, MatchedOn: MatchedModel, Description: e.Description,
		ModeState: ModeNotAsked, ModeChoices: make([]string, 0)}
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
	// modesIn: the modes a set of software versions maps p to, deduplicated
	// and sorted, with whether each is in the file.
	modesIn := func(vs []RDMSoftwareVersion) ([]string, map[string]bool) {
		in := map[string]bool{}
		for _, v := range vs {
			for _, pe := range v.Personalities {
				if pe.Value != nil && *pe.Value == p && pe.DMXMode != "" {
					in[pe.DMXMode] = in[pe.DMXMode] || pe.ModeInFile
				}
			}
		}
		names := make([]string, 0, len(in))
		for n := range in {
			names = append(names, n)
		}
		sort.Strings(names)
		return names, in
	}
	addNote := func(n string) {
		if c.Note != "" {
			c.Note += ". "
		}
		c.Note += n
	}
	if exact != nil {
		names, in := modesIn([]RDMSoftwareVersion{*exact})
		if len(names) == 1 {
			c.ModeState, c.DMXMode, c.ModeInFile = ModeFound, names[0], in[names[0]]
		} else if len(names) > 1 {
			c.ModeState, c.ModeChoices = ModeDiffers, names
			addNote(fmt.Sprintf("Software version %s maps personality %d to more than one mode", hexSW(*exact.ID), p))
		} else {
			c.ModeState = ModeNotMapped
			msg := fmt.Sprintf("Software version %s maps no mode for personality %d", hexSW(*exact.ID), p)
			if others, _ := modesIn(r.SoftwareVersions); len(others) > 0 {
				msg += " (other software versions in this file map it to " + strings.Join(others, ", ") + ")"
			}
			addNote(msg)
		}
	} else {
		names, in := modesIn(r.SoftwareVersions)
		switch len(names) {
		case 0:
			c.ModeState = ModeNotMapped
			addNote(fmt.Sprintf("This file maps no mode for personality %d", p))
		case 1:
			c.ModeState, c.DMXMode, c.ModeInFile = ModeFound, names[0], in[names[0]]
		default:
			c.ModeState, c.ModeChoices = ModeDiffers, names
			addNote(fmt.Sprintf("This file's software versions map personality %d to different modes", p))
		}
	}
	if c.ModeState == ModeFound && !c.ModeInFile {
		addNote(fmt.Sprintf("The file links personality %d to mode %q, which is not among its DMX modes", p, c.DMXMode))
	}
	return c, true
}
