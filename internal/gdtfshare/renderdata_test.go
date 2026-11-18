package gdtfshare

// TestWriteRenderLibrary is not a check: with BENNY512_RENDER_GDTF_OUT set to
// an empty folder it writes GDTF library folders for the G3b render-proofs,
// each made by REAL syncs and pulls against the fake GDTF Share in this
// package, so every index.json, sync.json and file in them is what this
// package writes. cmd/benny512's TestRenderHarness serves the --demo build
// with one of them as its GDTF library. Skipped when the variable is unset,
// which is every ordinary test run. All names, IDs and files are made up.
//
//	<out>/ok       synced, nothing waiting
//	<out>/partial  one download waiting (HTTP 503) and one unreadable file
//	<out>/offline  synced, then a sync that could not reach GDTF Share
//	<out>/refused  synced, then a sync refused: the route was the lighting adapter
//	<out>/empty    an empty folder: never synced
//
// The RDM blocks match the --demo devices (cmd/benny512/demo.go: every one
// reports DeviceModelID 1, software version 0x01000000, personality 1):
// Robe 0x2222 "Wash" -> found (footprints agree, 20); Ayrton 0xAAAA "Spot" ->
// likely (the file's versions disagree); Martin 0x4D50 "Beam" -> mode from
// other versions (footprints differ: 18 vs the device's 16); Chroma-Q 0x5370
// "Color Force II 48" -> found, footprints differ (22 vs 20); Elation 0x454C
// "Par" -> no RDM match (one file gives model ID 0x0, one has no RDM data),
// for the name search.

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func renderGDTF(t *testing.T, name, longName, modes, ftrdm string, pad int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("description.xml")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<GDTF DataVersion="1.2"><FixtureType Name=%q ShortName=%q LongName=%q Manufacturer="Render" FixtureTypeID="00000000-0000-0000-0000-000000000000">
<Geometries><Geometry Name="Body"/></Geometries><DMXModes>%s</DMXModes><Protocols>%s</Protocols></FixtureType></GDTF>`, name, name, longName, modes, ftrdm)
	if pad > 0 {
		// Stored, not deflated, so the file really is this big.
		pw, err := zw.CreateHeader(&zip.FileHeader{Name: "models/padding.bin", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write(bytes.Repeat([]byte{0x5A}, pad))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func modeList(pairs ...any) []map[string]any {
	out := make([]map[string]any, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, map[string]any{"name": pairs[i], "dmxfootprint": pairs[i+1]})
	}
	return out
}

// dmxModes: "name", channels, "name", channels, ... -> DMX modes with that
// many plain channels each, so the browser's parser reads the same
// footprints the list gives.
func dmxModes(pairs ...any) string {
	s := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		ch := ""
		for c := 1; c <= pairs[i+1].(int); c++ {
			ch += fmt.Sprintf(`<DMXChannel DMXBreak="1" Geometry="Body" Offset="%d"><LogicalChannel Attribute="Ch%d"><ChannelFunction Name="Ch%d 1" Attribute="Ch%d" DMXFrom="0/1" PhysicalFrom="0"/></LogicalChannel></DMXChannel>`, c, c, c, c)
		}
		s += fmt.Sprintf(`<DMXMode Name=%q Geometry="Body"><DMXChannels>%s</DMXChannels></DMXMode>`, pairs[i], ch)
	}
	return s
}

func renderShare(t *testing.T) *fakeShare {
	f := newFakeShare(t)
	add := func(rid int64, mfr, fixture, rev, uploader string, lm int64, data []byte, modes []map[string]any) {
		f.add(fakeFile{RID: rid, Manufacturer: mfr, Fixture: fixture, Revision: rev, Uploader: uploader,
			UUID: fmt.Sprintf("render-%d", rid), LastModified: lm, Data: data, Modes: modes})
	}
	const sw1 = `0x01000000`
	// Robe: found (device Wash, footprint 20), plus a user upload of it.
	wash := `<FTRDM ManufacturerID="0x2222" DeviceModelID="0x0001"><SoftwareVersionID Value="` + sw1 + `"><DMXPersonality Value="0x1" DMXMode="Mode 1"/><DMXPersonality Value="0x2" DMXMode="Mode 2"/></SoftwareVersionID></FTRDM>`
	add(1001, "Robe", "Wash", "DMX v1.3 (fw 1.0)", "Manuf.", 1790000000, renderGDTF(t, "Wash", "Robe Wash LED", dmxModes("Mode 1", 20, "Mode 2", 26), wash, 0), modeList("Mode 1", 20, "Mode 2", 26))
	add(1002, "Robe", "Wash (tour edit)", "tour v2", "User", 1789000000, renderGDTF(t, "Wash", "Robe Wash LED, tour gobos", dmxModes("Mode 1", 20), wash, 0), modeList("Mode 1", 20))
	add(1003, "Robe", "Esprite", "v1.0", "Manuf.", 1788000000, renderGDTF(t, "Esprite", "Robe Robin Esprite", dmxModes("Mode 1", 49, "Mode 2", 42), "", 0), modeList("Mode 1", 49, "Mode 2", 42))
	// Ayrton: likely (versions disagree on personality 1; the device's
	// version is not listed).
	spot := `<FTRDM ManufacturerID="0xAAAA" DeviceModelID="0x0001">
<SoftwareVersionID Value="0x02000000"><DMXPersonality Value="0x1" DMXMode="Standard"/></SoftwareVersionID>
<SoftwareVersionID Value="0x02010000"><DMXPersonality Value="0x1" DMXMode="Standard"/></SoftwareVersionID>
<SoftwareVersionID Value="0x02020000"><DMXPersonality Value="0x1" DMXMode="Standard 16-bit"/></SoftwareVersionID></FTRDM>`
	add(1101, "Ayrton", "Spot", "Rev 6", "Manuf.", 1780000000, renderGDTF(t, "Spot", "Ayrton Spot", dmxModes("Standard", 24, "Standard 16-bit", 28), spot, 0), modeList("Standard", 24, "Standard 16-bit", 28))
	// GDTF Share spells these two differently from the followed names.
	beam := `<FTRDM ManufacturerID="0x4D50" DeviceModelID="0x0001">
<SoftwareVersionID Value="` + sw1 + `"><DMXPersonality Value="0x2" DMXMode="Mode B"/></SoftwareVersionID>
<SoftwareVersionID Value="0x00090000"><DMXPersonality Value="0x1" DMXMode="Mode A"/></SoftwareVersionID>
<SoftwareVersionID Value="0x00080000"><DMXPersonality Value="0x1" DMXMode="Mode A"/></SoftwareVersionID></FTRDM>`
	add(1201, "Martin Professional", "Beam", "fw 1.0", "Manuf.", 1785000000, renderGDTF(t, "Beam", "Martin Beam", dmxModes("Mode A", 18, "Mode B", 22), beam, 0), modeList("Mode A", 18, "Mode B", 22))
	add(1202, "Martin Professional", "MAC Ultra Performance", "fw 2.4.0", "Manuf.", 1786000000,
		renderGDTF(t, "MAC Ultra Performance", "Martin MAC Ultra Performance", dmxModes("Basic", 41, "Extended", 52), "", 21<<20), modeList("Basic", 41, "Extended", 52))
	cf := `<FTRDM ManufacturerID="0x5370" DeviceModelID="0x0001"><SoftwareVersionID Value="` + sw1 + `"><DMXPersonality Value="0x1" DMXMode="Std"/></SoftwareVersionID></FTRDM>`
	add(1301, "Chroma-Q", "Color Force II 48", "v3", "Manuf.", 1784000000, renderGDTF(t, "Color Force II 48", "Chroma-Q Color Force II 48", dmxModes("Std", 22), cf, 0), modeList("Std", 22))
	// Elation: no RDM match for the demo Par (0x454C, model 1).
	add(1401, "Elation", "Par", "DMX v2", "Manuf.", 1783000000, renderGDTF(t, "Par", "Elation Par", dmxModes("8ch", 8, "4ch", 4), `<FTRDM ManufacturerID="0x454C" DeviceModelID="0x0"/>`, 0), modeList("8ch", 8, "4ch", 4))
	add(1402, "Elation", "Par (show set)", "Rev 2", "User", 1782000000, renderGDTF(t, "Par", "Elation Par, show set", dmxModes("8ch", 8), "", 0), modeList("8ch", 8))
	// One file each for the other followed names, as GDTF Share spells them.
	for i, m := range []string{"Chauvet", "GLP", "Volux", "ADJ", "ACME", "SGM", "Astera", "ETC", "High End Systems"} {
		rid := int64(1500 + i)
		add(rid, m, m+" Fixture", "v1", "Manuf.", 1770000000+int64(i), renderGDTF(t, m+" Fixture", m+" Fixture long name", dmxModes("Standard", 16), "", 0), modeList("Standard", 16))
	}
	// Not followed: only reachable by a single pull.
	add(1601, "Clay Paky", "Sharpy", "fw 1.4 / DMX 3.2", "Manuf.", 1760000000, renderGDTF(t, "Sharpy", "Clay Paky Sharpy", dmxModes("Standard", 16, "Vector", 20), "", 0), modeList("Standard", 16, "Vector", 20))
	add(1602, "Clay Paky", "Sharpy", "tour edit", "User", 1750000000, renderGDTF(t, "Sharpy", "Clay Paky Sharpy (tour)", dmxModes("Standard", 16), "", 0), modeList("Standard", 16))
	return f
}

func TestWriteRenderLibrary(t *testing.T) {
	out := os.Getenv("BENNY512_RENDER_GDTF_OUT")
	if out == "" {
		t.Skip("set BENNY512_RENDER_GDTF_OUT to write the G3b render-proof GDTF folders")
	}
	write := func(state string, lighting bool, prep func(f *fakeShare), after func(s *Service, f *fakeShare)) {
		f := renderShare(t)
		if prep != nil {
			prep(f)
		}
		var nets []netip.Prefix
		s, _ := newTestService(t, f, func(c *Config) {
			c.Root = filepath.Join(out, state)
			c.LightingNetworks = func() []netip.Prefix { return nets }
		})
		if _, err := s.Sync(context.Background()); err != nil {
			t.Fatalf("%s: first sync: %v", state, err)
		}
		for _, rid := range []int64{1201, 1202, 1301} { // Martin Professional, Chroma-Q: single pulls
			if _, err := s.Pull(context.Background(), rid); err != nil {
				t.Fatalf("%s: pull %d: %v", state, rid, err)
			}
		}
		if lighting {
			nets = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
		}
		if after != nil {
			after(s, f)
		}
		t.Logf("%s: %+v", state, s.Status().LastResult)
	}
	write("ok", false, nil, nil)
	write("partial", false, func(f *fakeShare) {
		f.failRID[1002] = 503
		f.files = append(f.files, fakeFile{RID: 1599, Manufacturer: "GLP", Fixture: "JDC2", Revision: "1.2b", Uploader: "User",
			UUID: "render-1599", LastModified: 1770000100, Data: zipOf(t, map[string]string{"description.xml": `<GDTF DataVersion="1.2"><FixtureType Name="JDC2">`})})
	}, nil)
	write("offline", false, nil, func(s *Service, f *fakeShare) {
		f.srv.Close()
		_, _ = s.Sync(context.Background())
	})
	write("refused", true, nil, func(s *Service, f *fakeShare) {
		_, _ = s.Sync(context.Background())
	})
	if err := os.MkdirAll(filepath.Join(out, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
}
