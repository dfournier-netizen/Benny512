package gdtfshare

import (
	"os"
	"strings"
	"testing"
)

type libFile struct {
	rid          int64
	manufacturer string
	fixture      string
	uploader     string // "Manuf." | "User"
	lastModified int64
	data         []byte
}

// newLibrary lays files down as a sync would (files/<rid>.gdtf and an
// index without descriptions, as G1 wrote it), then reads them through
// the real reader with Backfill.
func newLibrary(t *testing.T, files []libFile) *Service {
	t.Helper()
	s := New(Config{Root: t.TempDir()})
	m := map[int64]IndexEntry{}
	for _, f := range files {
		if err := os.MkdirAll(s.st.filesDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.st.absFile(f.rid), f.data, 0o600); err != nil {
			t.Fatal(err)
		}
		label := LabelManufacturer
		if f.uploader == "User" {
			label = LabelUser
		}
		m[f.rid] = IndexEntry{Entry: Entry{RID: f.rid, Manufacturer: f.manufacturer, Fixture: f.fixture, Uploader: f.uploader,
			UploaderLabel: label, LastModified: f.lastModified, Modes: make([]Mode, 0)},
			File: s.st.relFile(f.rid), LocalSize: int64(len(f.data))}
	}
	if err := s.saveIndex(m); err != nil {
		t.Fatal(err)
	}
	return s
}

func rdmDesc(name, ftrdm string) string {
	return `<GDTF DataVersion="1.2"><FixtureType Name="` + name + `" LongName="` + name + ` long">
<DMXModes><DMXMode Name="Standard"/><DMXMode Name="Extended"/></DMXModes>
<Protocols>` + ftrdm + `</Protocols></FixtureType></GDTF>`
}

func matchLibrary(t *testing.T) *Service {
	t.Helper()
	z := func(xml string) []byte { return zipOf(t, map[string]string{"description.xml": xml}) }
	s := newLibrary(t, []libFile{
		{1, "Elation", "Cube A", "Manuf.", 1700000000, z(rdmDesc("Cube A", `<FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093">
  <SoftwareVersionID Value="0x00010200"><DMXPersonality Value="0x1" DMXMode="Standard"/><DMXPersonality Value="0x2" DMXMode="Extended"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x00020000"><DMXPersonality Value="0x1" DMXMode="Extended"/><DMXPersonality Value="0x3" DMXMode="Ghost"/></SoftwareVersionID>
</FTRDM>`))},
		{2, "Elation", "Cube B (user edit)", "User", 1710000000, z(rdmDesc("Cube B", `<FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093">
  <SoftwareVersionID Value="0x00030000"><DMXPersonality Value="0x1" DMXMode="Standard"/></SoftwareVersionID>
</FTRDM>`))},
		{3, "Elation", "Other model", "Manuf.", 1700000000, z(rdmDesc("Other", `<FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1094"/>`))},
		{4, "GLP", "JDC1 Strobe", "Manuf.", 1700000000, z(rdmDesc("JDC1 Strobe", `<FTRDM DeviceModelID="0x0" ManufacturerID="0x676C"/>`))},
		{5, "Elation", "Cube no RDM", "Manuf.", 1700000000, z(`<GDTF><FixtureType Name="Cube A"/></GDTF>`)},
		{6, "Elation", "Cube broken", "Manuf.", 1700000000, zipOf(t, map[string]string{"thumbnail.png": "x"})},
	})
	if n, err := s.Backfill(); err != nil || n != 6 {
		t.Fatalf("backfill %d %v", n, err)
	}
	return s
}

func u32(v uint32) *uint32 { return &v }
func u8(v uint8) *uint8    { return &v }

func rids(c []MatchCandidate) []int64 {
	out := make([]int64, 0, len(c))
	for _, x := range c {
		out = append(out, x.RID)
	}
	return out
}

func TestMatchExactSoftwareVersionBeatsModelOnly(t *testing.T) {
	s := matchLibrary(t)
	// The User file lists software version 0x30000; the Manufacturer file
	// does not. The exact match comes first even though Manufacturer files
	// otherwise lead.
	r, err := s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093, SoftwareVersionID: u32(0x30000)})
	if err != nil {
		t.Fatal(err)
	}
	if got := rids(r.Candidates); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("order = %v, want [2 1]", got)
	}
	if r.Candidates[0].MatchedOn != MatchedSoftwareVersion || r.Candidates[1].MatchedOn != MatchedModel {
		t.Fatalf("matchedOn = %q, %q", r.Candidates[0].MatchedOn, r.Candidates[1].MatchedOn)
	}
	if !strings.Contains(r.Candidates[1].Note, "0x00030000") {
		t.Fatalf("a model-only candidate says the software version is not listed: %q", r.Candidates[1].Note)
	}
	// Without a software version both are model matches: Manufacturer first.
	r, _ = s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093})
	if got := rids(r.Candidates); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("model-only order = %v, want [1 2]", got)
	}
	for _, c := range r.Candidates {
		if c.MatchedOn != MatchedModel || c.ModeState != ModeNotAsked {
			t.Fatalf("candidate %d = %+v", c.RID, c)
		}
	}
	if r.Unreadable != 1 || r.NotYetRead != 0 {
		t.Fatalf("counts unreadable %d not-yet-read %d", r.Unreadable, r.NotYetRead)
	}
}

func TestMatchPersonalityToMode(t *testing.T) {
	s := matchLibrary(t)
	q := func(sw *uint32, p uint8) MatchCandidate {
		t.Helper()
		r, err := s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093, SoftwareVersionID: sw, Personality: u8(p)})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range r.Candidates {
			if c.RID == 1 {
				return c
			}
		}
		t.Fatalf("rid 1 not a candidate: %+v", r)
		return MatchCandidate{}
	}
	// Exact software version: that version's own map.
	if c := q(u32(0x00020000), 1); c.MatchedOn != MatchedSoftwareVersion || c.ModeState != ModeFound || c.DMXMode != "Extended" || !c.ModeInFile {
		t.Fatalf("sw 0x20000 p1 = %+v", c)
	}
	if c := q(u32(0x00010200), 1); c.ModeState != ModeFound || c.DMXMode != "Standard" {
		t.Fatalf("sw 0x10200 p1 = %+v", c)
	}
	// The device's version maps no mode for personality 2; another version
	// does (owner, round 4: fall back to it, with a warning naming it).
	if c := q(u32(0x00020000), 2); c.MatchedOn != MatchedSoftwareVersion || c.ModeState != ModeOtherVersions || c.DMXMode != "Extended" ||
		strings.Join(c.ModeFromVersions, "|") != "0x00010200" ||
		!strings.Contains(c.ModeWarning, "0x00010200") || !strings.Contains(c.ModeWarning, "0x00020000") || c.ModeBasis == "" {
		t.Fatalf("sw 0x20000 p2 = %+v", c)
	}
	// Model only: versions agree on p2 (only one maps it) -> found.
	if c := q(nil, 2); c.MatchedOn != MatchedModel || c.ModeState != ModeFound || c.DMXMode != "Extended" || c.ModeBasis == "" || c.ModeWarning != "" {
		t.Fatalf("model p2 = %+v", c)
	}
	// Model only: versions disagree on p1, one each -> the likely mode is
	// the one the highest software version (0x00020000) maps; both stay
	// listed, and it says it is a guess.
	if c := q(nil, 1); c.ModeState != ModeLikely || c.DMXMode != "Extended" || strings.Join(c.ModeChoices, "|") != "Extended|Standard" ||
		!strings.Contains(c.ModeBasis, "1 of 2") || !strings.Contains(c.ModeBasis, "highest software version, 0x00020000") || c.ModeWarning == "" || strings.Join(c.ModeFromVersions, "|") != "0x00020000" {
		t.Fatalf("model p1 = %+v", c)
	}
	// A link to a mode the file does not have is reported as such.
	if c := q(u32(0x00020000), 3); c.ModeState != ModeFound || c.DMXMode != "Ghost" || c.ModeInFile {
		t.Fatalf("sw 0x20000 p3 = %+v", c)
	}
	if c := q(nil, 9); c.ModeState != ModeNotMapped || c.DMXMode != "" || len(c.ModeChoices) != 0 {
		t.Fatalf("p9 = %+v", c)
	}
}

func TestMatchNoRDMMatchIsEmptyNotANameGuess(t *testing.T) {
	s := matchLibrary(t)
	for name, q := range map[string]MatchQuery{
		"unknown model":         {ManufacturerID: 0x22A6, DeviceModelID: 0x7777},
		"unknown manufacturer":  {ManufacturerID: 0x1234, DeviceModelID: 0x1093},
		"model 0 (JDC1's 0x0)":  {ManufacturerID: 0x676C, DeviceModelID: 0},
		"manufacturer 0, model": {ManufacturerID: 0, DeviceModelID: 0x1093},
	} {
		r, err := s.Match(q)
		if err != nil {
			t.Fatal(err)
		}
		if r.Candidates == nil || len(r.Candidates) != 0 {
			t.Fatalf("%s: candidates = %+v, want an empty list", name, r.Candidates)
		}
	}
	r, _ := s.Match(MatchQuery{ManufacturerID: 0x676C, DeviceModelID: 0})
	if !strings.Contains(r.Note, "not given") {
		t.Fatalf("a 0 in the query is explained: %q", r.Note)
	}
}

func TestMatchKeepsLabelsAndDescription(t *testing.T) {
	s := matchLibrary(t)
	r, _ := s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093})
	want := map[int64]string{1: LabelManufacturer, 2: LabelUser}
	for _, c := range r.Candidates {
		if c.UploaderLabel != want[c.RID] || c.Uploader == "" {
			t.Fatalf("rid %d label %q uploader %q", c.RID, c.UploaderLabel, c.Uploader)
		}
		if c.Description == nil || c.Description.LongName == "" || c.Fixture == "" {
			t.Fatalf("rid %d carries its names: %+v", c.RID, c)
		}
	}
	if len(r.Candidates) != 2 {
		t.Fatalf("candidates = %v", rids(r.Candidates))
	}
}

func TestMatchCountsFilesNotYetRead(t *testing.T) {
	s := newLibrary(t, []libFile{{1, "Elation", "Cube", "Manuf.", 1, zipOf(t, map[string]string{"description.xml": rdmDesc("Cube", `<FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093"/>`)})}})
	r, err := s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093})
	if err != nil {
		t.Fatal(err)
	}
	if r.NotYetRead != 1 || len(r.Candidates) != 0 {
		t.Fatalf("before backfill: %+v", r)
	}
	if _, err := s.Backfill(); err != nil {
		t.Fatal(err)
	}
	if r, _ = s.Match(MatchQuery{ManufacturerID: 0x22A6, DeviceModelID: 0x1093}); r.NotYetRead != 0 || len(r.Candidates) != 1 {
		t.Fatalf("after backfill: %+v", r)
	}
}

// TestMatchLikelyModeRule pins the most-likely rule (owner, round 4): the
// mode mapped by the most software versions; ties to the mode mapped by the
// highest software version ID (a version with no ID counts as lowest);
// then the mode name A-Z.
func TestMatchLikelyModeRule(t *testing.T) {
	s := newLibrary(t, []libFile{{7, "Acme", "Rule", "Manuf.", 1, zipOf(t, map[string]string{"description.xml": rdmDesc("Rule", `<FTRDM ManufacturerID="0x1111" DeviceModelID="0x2222">
  <SoftwareVersionID Value="0x10"><DMXPersonality Value="1" DMXMode="A"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x20"><DMXPersonality Value="1" DMXMode="A"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x30"><DMXPersonality Value="1" DMXMode="A"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x40"><DMXPersonality Value="1" DMXMode="B"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x50"><DMXPersonality Value="2" DMXMode="Zeta"/><DMXPersonality Value="2" DMXMode="Alpha"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x0"><DMXPersonality Value="3" DMXMode="X"/></SoftwareVersionID>
  <SoftwareVersionID Value="0x05"><DMXPersonality Value="3" DMXMode="Y"/></SoftwareVersionID>
</FTRDM>`)})}})
	if _, err := s.Backfill(); err != nil {
		t.Fatal(err)
	}
	one := func(sw *uint32, p uint8) MatchCandidate {
		t.Helper()
		r, err := s.Match(MatchQuery{ManufacturerID: 0x1111, DeviceModelID: 0x2222, SoftwareVersionID: sw, Personality: u8(p)})
		if err != nil || len(r.Candidates) != 1 {
			t.Fatalf("match: %v %+v", err, r)
		}
		return r.Candidates[0]
	}
	// Most versions win, though the highest version (0x40) says B.
	if c := one(nil, 1); c.ModeState != ModeLikely || c.DMXMode != "A" || !strings.Contains(c.ModeBasis, "3 of 4") ||
		strings.Join(c.ModeChoices, "|") != "A|B" || strings.Join(c.ModeFromVersions, "|") != "0x00000010|0x00000020|0x00000030" {
		t.Fatalf("p1 = %+v", c)
	}
	// Same count, same version: the name decides.
	if c := one(nil, 2); c.ModeState != ModeLikely || c.DMXMode != "Alpha" || strings.Join(c.ModeChoices, "|") != "Alpha|Zeta" ||
		!strings.Contains(c.ModeBasis, "by mode name") {
		t.Fatalf("p2 = %+v", c)
	}
	// Same count: a version with an ID beats one without.
	if c := one(nil, 3); c.ModeState != ModeLikely || c.DMXMode != "Y" || strings.Join(c.ModeFromVersions, "|") != "0x00000005" {
		t.Fatalf("p3 = %+v", c)
	}
	// The device's own version decides when it maps the personality once.
	if c := one(u32(0x40), 1); c.MatchedOn != MatchedSoftwareVersion || c.ModeState != ModeFound || c.DMXMode != "B" || c.ModeWarning != "" {
		t.Fatalf("sw 0x40 p1 = %+v", c)
	}
	// Its version maps nothing for p2: the other versions decide, by the
	// same rule, and the warning names them.
	if c := one(u32(0x10), 2); c.MatchedOn != MatchedSoftwareVersion || c.ModeState != ModeOtherVersions || c.DMXMode != "Alpha" ||
		!strings.Contains(c.ModeWarning, "0x00000050") || strings.Join(c.ModeChoices, "|") != "Alpha|Zeta" {
		t.Fatalf("sw 0x10 p2 = %+v", c)
	}
	// Its own version maps p2 to two modes: still a guess, said so.
	if c := one(u32(0x50), 2); c.MatchedOn != MatchedSoftwareVersion || c.ModeState != ModeLikely || c.DMXMode != "Alpha" || c.ModeWarning == "" {
		t.Fatalf("sw 0x50 p2 = %+v", c)
	}
}
