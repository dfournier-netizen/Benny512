package gdtfshare

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Chunk G2: what Benny512 reads from each downloaded file's description.xml.
//
// Source for every element and attribute name below: the GDTF specification
// (DIN SPEC 15800), gdtf-spec.md in https://github.com/mvrdevelopment/spec at
// commit 098d3791f77f0895bd859adf01864b4826e2006f (2026-04-09, "GDTF Version
// 1.2 Draft 1"):
//
//   - Table 3, Fixture Type Node Attributes: Name, ShortName, LongName,
//     Manufacturer, FixtureTypeID (a GUID).
//   - "Supported Protocol Collect", <Protocols>, Table 69: its RDM child is
//     the XML node <FTRDM>, not mandatory.
//   - Table 70, RDM Attributes: ManufacturerID (Hex, "Manufacturer ESTA ID"),
//     DeviceModelID (Hex, "Unique device model ID").
//   - Table 71: <SoftwareVersionID>, children of FTRDM, attribute Value (Hex).
//   - Table 72: <DMXPersonality>, children of SoftwareVersionID, attributes
//     Value (Hex) and DMXMode (Name: "Link to the DMX Mode that can be used
//     with this software version").
//   - Table 1, XML Attribute Value Types: Hex is "Number in hexadecimal
//     notation; Default value: 0". The spec does not say whether a "0x"
//     prefix is required; real files carry one ("0x22A6"), so it is accepted
//     and not required.
//   - The archive's description file has the static name "description.xml"
//     (spec revision history).
//
// THE ZERO RULE. An RDM ID of 0 in a GDTF file is "not given", never "ID 0".
// Evidence: the spec makes 0 the Hex default, so an attribute the author
// never filled in reads as 0; and a real GDTF Share download, GLP
// "JDC1 Strobe" (GLP@JDC1_Strobe@test1.gdtf), carries
// <FTRDM DeviceModelID="0x0" ManufacturerID="0x676C"/> — GLP's ESTA ID with
// a model ID that is plainly unfilled. Treating that 0 as a model would match
// every device that reports model 0 to a strobe. So a 0 ManufacturerID,
// DeviceModelID, SoftwareVersionID or DMXPersonality Value is stored as null
// (the raw text is kept beside it as evidence), and a null never matches.
// A missing <FTRDM> means "no RDM data" (RDM null), not an RDM block of zeros.

// descReader is the version of the description reader. An index entry with
// no description (a pre-G2 entry) or one read by an older reader is read
// again by Backfill; bump it when the reader learns something new.
const descReader = 1

// maxDescriptionBytes caps what is read of one description.xml. Real files
// are a few hundred KB (Paladin Cube 174 KB, JDC1 254 KB); the cap only
// stops a damaged or hostile archive from filling memory.
const maxDescriptionBytes = 16 << 20

// Description states.
const (
	DescRead       = "read"
	DescUnreadable = "unreadable"
)

// Description is what the reader found in one file. State "unreadable"
// keeps the file in the library and says why in Reason; it never removes
// the file and never stops the other files being read.
type Description struct {
	State  string    `json:"state"`
	Reason string    `json:"reason"`
	Reader int       `json:"reader"`
	ReadAt time.Time `json:"readAt"`

	DataVersion   string `json:"dataVersion"`
	Name          string `json:"name"`
	ShortName     string `json:"shortName"`
	LongName      string `json:"longName"`
	Manufacturer  string `json:"manufacturer"`
	FixtureTypeID string `json:"fixtureTypeId"`
	// DMXModes are the names of the file's DMX modes, in file order, so a
	// personality's mode link can be checked against what the file has.
	DMXModes []string `json:"dmxModes"`
	// RDM is null when the file has no <FTRDM>: "no RDM data".
	RDM *RDMInfo `json:"rdm"`
}

// RDMInfo is a file's <FTRDM>. Each ID is null when the file does not give
// one (absent, 0, or not a hex number); the *Text field keeps what the file
// said. Problems lists, in words, anything that could not be used.
type RDMInfo struct {
	ManufacturerID     *uint16              `json:"manufacturerId"`
	ManufacturerIDText string               `json:"manufacturerIdText"`
	DeviceModelID      *uint16              `json:"deviceModelId"`
	DeviceModelIDText  string               `json:"deviceModelIdText"`
	SoftwareVersions   []RDMSoftwareVersion `json:"softwareVersions"`
	Problems           []string             `json:"problems"`
}

// RDMSoftwareVersion is one <SoftwareVersionID>. ID has
// params.DeviceInfo.SoftwareVersionID's width (32 bits).
type RDMSoftwareVersion struct {
	ID            *uint32          `json:"id"`
	IDText        string           `json:"idText"`
	Personalities []RDMPersonality `json:"personalities"`
}

// RDMPersonality is one <DMXPersonality>: an RDM DMX personality number
// (params.DeviceInfo.CurrentPersonality's width, 8 bits) and the DMX mode
// it links to. ModeInFile is false when no DMX mode of that name is in the
// file (a broken link), so the UI can say so rather than offer it.
type RDMPersonality struct {
	Value      *uint8 `json:"value"`
	ValueText  string `json:"valueText"`
	DMXMode    string `json:"dmxMode"`
	ModeInFile bool   `json:"modeInFile"`
}

// readDescription reads the GDTF file at file. It always returns a
// Description; State says whether it could be read. ReadAt is the caller's.
func readDescription(file string) Description {
	d, err := describe(file)
	if err != nil {
		return Description{State: DescUnreadable, Reason: err.Error(), Reader: descReader, DMXModes: make([]string, 0)}
	}
	d.State, d.Reader = DescRead, descReader
	return d
}

func describe(file string) (Description, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return Description{}, fmt.Errorf("not a readable GDTF (zip) archive: %v", err)
	}
	defer zr.Close()
	// The same entry the browser import reads (mvrimport.js parseGdtfFile):
	// the first whose last path segment is description.xml, any case.
	var zf *zip.File
	for _, f := range zr.File {
		seg := f.Name
		if i := strings.LastIndexAny(seg, `/\`); i >= 0 {
			seg = seg[i+1:]
		}
		if strings.EqualFold(seg, "description.xml") {
			zf = f
			break
		}
	}
	if zf == nil {
		return Description{}, errors.New("not a GDTF file: there is no description.xml inside the archive")
	}
	tooBig := fmt.Errorf("description.xml is larger than %d MB, so it was not read", maxDescriptionBytes>>20)
	if zf.UncompressedSize64 > maxDescriptionBytes {
		return Description{}, tooBig
	}
	rc, err := zf.Open()
	if err != nil {
		return Description{}, fmt.Errorf("description.xml could not be opened: %v", err)
	}
	defer rc.Close()
	// The header's size can lie; never read more than the cap plus one byte.
	raw, err := io.ReadAll(io.LimitReader(rc, maxDescriptionBytes+1))
	if err != nil {
		return Description{}, fmt.Errorf("description.xml could not be decompressed: %v", err)
	}
	if len(raw) > maxDescriptionBytes {
		return Description{}, tooBig
	}
	return parseDescriptionXML(raw)
}

// parseDescriptionXML streams description.xml and keeps only what G2 needs,
// reading to the end so a file broken anywhere is reported as broken.
func parseDescriptionXML(raw []byte) (Description, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bufio.NewReader(bytes.NewReader(raw)))
	d := Description{DMXModes: make([]string, 0)}
	var stack []string
	// inRDM: inside the first <FTRDM> of the first <FixtureType>.
	var rootSeen, ftSeen, ftrdmSeen, inFirstFT, inRDM bool
	var sv *RDMSoftwareVersion
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Description{}, fmt.Errorf("description.xml is not well-formed XML: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			stack = append(stack, name)
			depth := len(stack)
			if depth == 1 {
				if rootSeen || name != "GDTF" {
					return Description{}, fmt.Errorf("description.xml's root element is <%s>, not <GDTF>", name)
				}
				rootSeen = true
				d.DataVersion = attr(t, "DataVersion")
				continue
			}
			if depth == 2 && name == "FixtureType" {
				// One fixture type per file; a second is ignored.
				inFirstFT = !ftSeen
				if !ftSeen {
					ftSeen = true
					d.Name, d.ShortName, d.LongName = attr(t, "Name"), attr(t, "ShortName"), attr(t, "LongName")
					d.Manufacturer, d.FixtureTypeID = attr(t, "Manufacturer"), attr(t, "FixtureTypeID")
				}
				continue
			}
			if depth < 3 || stack[1] != "FixtureType" || !inFirstFT {
				continue
			}
			p := strings.Join(stack[2:], "/")
			switch p {
			case "DMXModes/DMXMode":
				d.DMXModes = append(d.DMXModes, attr(t, "Name"))
			case "Protocols/FTRDM":
				if ftrdmSeen {
					d.RDM.Problems = append(d.RDM.Problems, "the file has more than one <FTRDM>; only the first is used")
					continue
				}
				ftrdmSeen, inRDM = true, true
				r := &RDMInfo{SoftwareVersions: make([]RDMSoftwareVersion, 0), Problems: make([]string, 0)}
				r.ManufacturerIDText, r.DeviceModelIDText = attr(t, "ManufacturerID"), attr(t, "DeviceModelID")
				if v, ok := hexValue(r.ManufacturerIDText, 16, "ManufacturerID", &r.Problems); ok {
					u := uint16(v)
					r.ManufacturerID = &u
				}
				if v, ok := hexValue(r.DeviceModelIDText, 16, "DeviceModelID", &r.Problems); ok {
					u := uint16(v)
					r.DeviceModelID = &u
				}
				d.RDM = r
			case "Protocols/FTRDM/SoftwareVersionID":
				if !inRDM {
					continue
				}
				v := RDMSoftwareVersion{IDText: attr(t, "Value"), Personalities: make([]RDMPersonality, 0)}
				if n, ok := hexValue(v.IDText, 32, "SoftwareVersionID Value", &d.RDM.Problems); ok {
					u := uint32(n)
					v.ID = &u
				}
				d.RDM.SoftwareVersions = append(d.RDM.SoftwareVersions, v)
				sv = &d.RDM.SoftwareVersions[len(d.RDM.SoftwareVersions)-1]
			case "Protocols/FTRDM/SoftwareVersionID/DMXPersonality":
				if sv == nil {
					continue
				}
				pe := RDMPersonality{ValueText: attr(t, "Value"), DMXMode: attr(t, "DMXMode")}
				if n, ok := hexValue(pe.ValueText, 8, "DMXPersonality Value", &d.RDM.Problems); ok {
					u := uint8(n)
					pe.Value = &u
				}
				sv.Personalities = append(sv.Personalities, pe)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				switch strings.Join(stack, "/") {
				case "GDTF/FixtureType/Protocols/FTRDM/SoftwareVersionID":
					sv = nil
				case "GDTF/FixtureType/Protocols/FTRDM":
					inRDM, sv = false, nil
				}
				if len(stack) == 2 && stack[1] == "FixtureType" {
					inFirstFT = false
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !rootSeen {
		return Description{}, errors.New("description.xml has no <GDTF> root element")
	}
	if !ftSeen {
		return Description{}, errors.New("description.xml has no <FixtureType>")
	}
	if d.RDM != nil {
		modes := make(map[string]bool, len(d.DMXModes))
		for _, m := range d.DMXModes {
			modes[m] = true
		}
		for i := range d.RDM.SoftwareVersions {
			ps := d.RDM.SoftwareVersions[i].Personalities
			for j := range ps {
				ps[j].ModeInFile = ps[j].DMXMode != "" && modes[ps[j].DMXMode]
			}
		}
	}
	return d, nil
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name && a.Name.Space == "" {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// hexValue reads a GDTF Hex attribute (Table 1: hexadecimal notation,
// default 0). ok is false when the value is not given: empty, 0 (the zero
// rule, see the file comment) or unusable — the last is named in problems.
func hexValue(text string, bits int, what string, problems *[]string) (uint64, bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, false
	}
	digits := s
	if len(digits) > 2 && (digits[:2] == "0x" || digits[:2] == "0X") {
		digits = digits[2:]
	}
	v, err := strconv.ParseUint(digits, 16, bits)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			*problems = append(*problems, fmt.Sprintf("%s %q does not fit in %d bits, so it is not used", what, text, bits))
		} else {
			*problems = append(*problems, fmt.Sprintf("%s %q is not a hexadecimal number, so it is not used", what, text))
		}
		return 0, false
	}
	if v == 0 {
		return 0, false
	}
	return v, true
}

// Backfill reads every library file that has no description yet (an index
// written before G2) or one from an older reader, and saves the index. An
// entry is read once: after this, it has a Description (read or
// unreadable) and is skipped. Run calls it at start-up before the first
// sync. It holds the GDTF Share conversation lock while it works, so it
// never races a sync or pull over index.json. It returns how many files it
// read.
func (s *Service) Backfill() (int, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	s.netMu.Lock()
	defer s.netMu.Unlock()
	files, err := s.indexMap()
	if err != nil {
		return 0, err
	}
	rids := make([]int64, 0)
	for rid, e := range files {
		if needsDescription(e) {
			rids = append(rids, rid)
		}
	}
	sort.Slice(rids, func(i, j int) bool { return rids[i] < rids[j] })
	for i, rid := range rids {
		e := files[rid]
		e.Description = s.describeRID(rid)
		files[rid] = e
		if (i+1)%indexSaveEvery == 0 {
			if err := s.saveIndex(files); err != nil {
				return i + 1, err
			}
		}
	}
	if len(rids) > 0 {
		if err := s.saveIndex(files); err != nil {
			return len(rids), err
		}
		s.cfg.Logf("GDTF: read the description of %d library file(s)", len(rids))
	}
	return len(rids), nil
}

func needsDescription(e IndexEntry) bool {
	return e.Description == nil || e.Description.Reader < descReader
}

// describeRID reads one library file's description.xml.
func (s *Service) describeRID(rid int64) *Description {
	d := readDescription(s.st.absFile(rid))
	d.ReadAt = s.now()
	return &d
}
