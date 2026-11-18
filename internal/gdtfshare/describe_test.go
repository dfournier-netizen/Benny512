package gdtfshare

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf builds a GDTF-shaped archive from name -> content.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.gdtf")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fullDescription has every field G2 reads: two software versions, one
// mapping personalities 1 and 2, the other mapping personality 1 to a
// different mode and personality 3 to a mode the file does not have.
const fullDescription = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<GDTF DataVersion="1.2">
  <FixtureType Name="Test Spot" ShortName="TSpot" LongName="Acme Test Spot 700" Manufacturer="Acme" FixtureTypeID="11111111-2222-3333-4444-555555555555" Description="ignored">
    <AttributeDefinitions/>
    <DMXModes>
      <DMXMode Name="Standard" Geometry="Base"><DMXChannels/></DMXMode>
      <DMXMode Name="Extended" Geometry="Base"><DMXChannels/></DMXMode>
    </DMXModes>
    <Protocols>
      <FTRDM ManufacturerID="0x22A6" DeviceModelID="0x1093">
        <SoftwareVersionID Value="0x00010200">
          <DMXPersonality Value="0x1" DMXMode="Standard"/>
          <DMXPersonality Value="0x2" DMXMode="Extended"/>
        </SoftwareVersionID>
        <SoftwareVersionID Value="0x00020000">
          <DMXPersonality Value="0x01" DMXMode="Extended"/>
          <DMXPersonality Value="0x3" DMXMode="Ghost"/>
        </SoftwareVersionID>
      </FTRDM>
    </Protocols>
  </FixtureType>
</GDTF>`

func TestDescriptionReadsEveryField(t *testing.T) {
	d := readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": fullDescription, "thumbnail.png": "x"})))
	if d.State != DescRead || d.Reason != "" || d.Reader != descReader {
		t.Fatalf("state %q reason %q reader %d", d.State, d.Reason, d.Reader)
	}
	if d.DataVersion != "1.2" || d.Name != "Test Spot" || d.ShortName != "TSpot" || d.LongName != "Acme Test Spot 700" ||
		d.Manufacturer != "Acme" || d.FixtureTypeID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("fixture type fields = %+v", d)
	}
	if strings.Join(d.DMXModes, "|") != "Standard|Extended" {
		t.Fatalf("modes = %q", d.DMXModes)
	}
	r := d.RDM
	if r == nil {
		t.Fatal("RDM block not read")
	}
	if r.ManufacturerID == nil || *r.ManufacturerID != 0x22A6 || r.ManufacturerIDText != "0x22A6" {
		t.Fatalf("manufacturer = %v %q", r.ManufacturerID, r.ManufacturerIDText)
	}
	if r.DeviceModelID == nil || *r.DeviceModelID != 0x1093 || r.DeviceModelIDText != "0x1093" {
		t.Fatalf("model = %v %q", r.DeviceModelID, r.DeviceModelIDText)
	}
	if len(r.SoftwareVersions) != 2 {
		t.Fatalf("software versions = %+v", r.SoftwareVersions)
	}
	v1, v2 := r.SoftwareVersions[0], r.SoftwareVersions[1]
	if v1.ID == nil || *v1.ID != 0x00010200 || v2.ID == nil || *v2.ID != 0x00020000 {
		t.Fatalf("software version ids = %v %v", v1.ID, v2.ID)
	}
	if len(v1.Personalities) != 2 || len(v2.Personalities) != 2 {
		t.Fatalf("personalities = %+v / %+v", v1.Personalities, v2.Personalities)
	}
	p := v1.Personalities[1]
	if p.Value == nil || *p.Value != 2 || p.DMXMode != "Extended" || !p.ModeInFile || p.ValueText != "0x2" {
		t.Fatalf("v1 personality 2 = %+v", p)
	}
	if g := v2.Personalities[1]; g.Value == nil || *g.Value != 3 || g.DMXMode != "Ghost" || g.ModeInFile {
		t.Fatalf("a link to a mode the file lacks must say so: %+v", g)
	}
	if len(r.Problems) != 0 {
		t.Fatalf("problems = %q", r.Problems)
	}
}

func TestDescriptionWithoutRDMBlockHasNoRDMData(t *testing.T) {
	for name, xml := range map[string]string{
		"no Protocols":    `<GDTF DataVersion="1.1"><FixtureType Name="A" LongName="A long" Manufacturer="M"><DMXModes><DMXMode Name="M1"/></DMXModes></FixtureType></GDTF>`,
		"Protocols empty": `<GDTF DataVersion="1.1"><FixtureType Name="A" Manufacturer="M"><Protocols><Art-Net/></Protocols></FixtureType></GDTF>`,
	} {
		d := readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": xml})))
		if d.State != DescRead {
			t.Fatalf("%s: state %q (%s)", name, d.State, d.Reason)
		}
		if d.RDM != nil {
			t.Fatalf("%s: a file without <FTRDM> must have no RDM data, got %+v", name, d.RDM)
		}
		if d.Name != "A" {
			t.Fatalf("%s: name %q", name, d.Name)
		}
	}
}

func TestDescriptionZeroIDsAreNotGiven(t *testing.T) {
	xml := `<GDTF DataVersion="1.2"><FixtureType Name="Z" Manufacturer="GLP">
<DMXModes><DMXMode Name="Basic"/></DMXModes>
<Protocols><FTRDM DeviceModelID="0x0" ManufacturerID="0x676C">
  <SoftwareVersionID Value="0x0"><DMXPersonality Value="0x0" DMXMode="Basic"/><DMXPersonality Value="0x4" DMXMode="Basic"/></SoftwareVersionID>
</FTRDM></Protocols></FixtureType></GDTF>`
	d := readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": xml})))
	if d.State != DescRead || d.RDM == nil {
		t.Fatalf("state %q rdm %v (%s)", d.State, d.RDM, d.Reason)
	}
	r := d.RDM
	if r.DeviceModelID != nil {
		t.Fatalf("DeviceModelID 0x0 must be not given, got %d", *r.DeviceModelID)
	}
	if r.DeviceModelIDText != "0x0" {
		t.Fatalf("raw text kept as evidence: %q", r.DeviceModelIDText)
	}
	if r.ManufacturerID == nil || *r.ManufacturerID != 0x676C {
		t.Fatalf("manufacturer = %v", r.ManufacturerID)
	}
	if len(r.SoftwareVersions) != 1 || r.SoftwareVersions[0].ID != nil {
		t.Fatalf("software version 0x0 must be not given: %+v", r.SoftwareVersions)
	}
	ps := r.SoftwareVersions[0].Personalities
	if len(ps) != 2 || ps[0].Value != nil || ps[1].Value == nil || *ps[1].Value != 4 {
		t.Fatalf("personality 0x0 must be not given: %+v", ps)
	}
	// Absent attributes read the same as 0 (the spec's Hex default).
	d2 := readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": `<GDTF><FixtureType Name="Y"><Protocols><FTRDM/></Protocols></FixtureType></GDTF>`})))
	if d2.RDM == nil || d2.RDM.ManufacturerID != nil || d2.RDM.DeviceModelID != nil {
		t.Fatalf("absent IDs = %+v", d2.RDM)
	}
}

func TestDescriptionBadHexIsAProblemNotAValue(t *testing.T) {
	xml := `<GDTF><FixtureType Name="H"><Protocols><FTRDM ManufacturerID="zz12" DeviceModelID="0x1FFFF">
<SoftwareVersionID Value="0x1"><DMXPersonality Value="0x100" DMXMode="X"/></SoftwareVersionID></FTRDM></Protocols></FixtureType></GDTF>`
	d := readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": xml})))
	if d.State != DescRead || d.RDM == nil {
		t.Fatalf("state %q (%s)", d.State, d.Reason)
	}
	if d.RDM.ManufacturerID != nil || d.RDM.DeviceModelID != nil {
		t.Fatalf("unusable IDs must be null: %+v", d.RDM)
	}
	if p := d.RDM.SoftwareVersions[0].Personalities[0]; p.Value != nil {
		t.Fatalf("personality 0x100 does not fit a byte: %+v", p)
	}
	if len(d.RDM.Problems) != 3 {
		t.Fatalf("each unusable value is named: %q", d.RDM.Problems)
	}
}

func TestDescriptionUnreadableSaysWhy(t *testing.T) {
	big := `<GDTF><FixtureType Name="Big" LongName="` + strings.Repeat("a", maxDescriptionBytes) + `"/></GDTF>`
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"not a zip", []byte("this is not a zip archive"), "zip"},
		{"no description.xml", zipOf(t, map[string]string{"thumbnail.png": "x"}), "no description.xml"},
		{"malformed XML", zipOf(t, map[string]string{"description.xml": `<GDTF><FixtureType Name="A"><DMXModes></FixtureType></GDTF>`}), "XML"},
		{"wrong root", zipOf(t, map[string]string{"description.xml": `<MVR/>`}), "<GDTF>"},
		{"no FixtureType", zipOf(t, map[string]string{"description.xml": `<GDTF DataVersion="1.2"/>`}), "<FixtureType>"},
		{"oversized", zipOf(t, map[string]string{"description.xml": big}), "larger than"},
	}
	for _, c := range cases {
		d := readDescription(writeTemp(t, c.data))
		if d.State != DescUnreadable {
			t.Fatalf("%s: state %q, want unreadable", c.name, d.State)
		}
		if !strings.Contains(d.Reason, c.want) {
			t.Fatalf("%s: reason %q does not say %q", c.name, d.Reason, c.want)
		}
		if d.RDM != nil || d.Name != "" {
			t.Fatalf("%s: an unreadable file carries no data: %+v", c.name, d)
		}
	}
	if d := readDescription(filepath.Join(t.TempDir(), "missing.gdtf")); d.State != DescUnreadable || d.Reason == "" {
		t.Fatalf("missing file: %+v", d)
	}
}

func TestDescriptionFoundLikeTheBrowserFindsIt(t *testing.T) {
	// mvrimport.js parseGdtfFile takes the first entry whose last path
	// segment is description.xml, case-insensitively; the server reads the
	// same entry so both sides describe the same file.
	d := readDescription(writeTemp(t, zipOf(t, map[string]string{"Description.XML": `<GDTF><FixtureType Name="Cased"/></GDTF>`})))
	if d.State != DescRead || d.Name != "Cased" {
		t.Fatalf("%+v", d)
	}
	// A UTF-8 byte-order mark is not a reason to refuse a file.
	d = readDescription(writeTemp(t, zipOf(t, map[string]string{"description.xml": "\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"UTF-8\"?><GDTF><FixtureType Name=\"BOM\"/></GDTF>"})))
	if d.State != DescRead || d.Name != "BOM" {
		t.Fatalf("BOM: %+v", d)
	}
}

// TestDescriptionRealGDTFShareFiles reads two real GDTF Share downloads when
// they are present (they are not committed: GDTF Share's files are not
// ours to redistribute). Skipped, not failed, without them.
func TestDescriptionRealGDTFShareFiles(t *testing.T) {
	dir := os.Getenv("BENNY512_GDTF_SAMPLES")
	if dir == "" {
		dir = "/tmp/gdtf-samples"
	}
	paladin := filepath.Join(dir, "Elation@Paladin_Cube@23-02-10_First_Release.gdtf")
	jdc1 := filepath.Join(dir, "GLP@JDC1_Strobe@test1.gdtf")
	for _, p := range []string{paladin, jdc1} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("real GDTF Share sample not present (%s); set BENNY512_GDTF_SAMPLES to their folder", p)
		}
	}
	d := readDescription(paladin)
	if d.State != DescRead || d.RDM == nil {
		t.Fatalf("Paladin Cube: %+v", d)
	}
	if d.RDM.ManufacturerID == nil || *d.RDM.ManufacturerID != 0x22A6 || d.RDM.DeviceModelID == nil || *d.RDM.DeviceModelID != 0x1093 {
		t.Fatalf("Paladin Cube RDM = %+v", d.RDM)
	}
	if len(d.RDM.SoftwareVersions) != 0 {
		t.Fatalf("Paladin Cube lists no software versions: %+v", d.RDM.SoftwareVersions)
	}
	if d.Manufacturer == "" || d.Name == "" || len(d.DMXModes) == 0 {
		t.Fatalf("Paladin Cube fixture type = %q %q modes %d", d.Manufacturer, d.Name, len(d.DMXModes))
	}
	t.Logf("Paladin Cube: Name %q LongName %q ShortName %q Manufacturer %q ID %q, %d modes", d.Name, d.LongName, d.ShortName, d.Manufacturer, d.FixtureTypeID, len(d.DMXModes))

	g := readDescription(jdc1)
	if g.State != DescRead || g.RDM == nil {
		t.Fatalf("JDC1: %+v", g)
	}
	if g.RDM.ManufacturerID == nil || *g.RDM.ManufacturerID != 0x676C {
		t.Fatalf("JDC1 manufacturer = %+v", g.RDM)
	}
	if g.RDM.DeviceModelID != nil || g.RDM.DeviceModelIDText != "0x0" {
		t.Fatalf("JDC1 DeviceModelID=\"0x0\" must read as not given: %+v", g.RDM)
	}
	if g.Name != "JDC1 Strobe" || g.LongName != "GLP JDC1 Strobe" || g.ShortName != "JDC 1" || g.Manufacturer != "GLP" ||
		g.FixtureTypeID != "5F4FBBF2-DFC4-4539-816A-389256689C29" {
		t.Fatalf("JDC1 fixture type = %+v", g)
	}
}
