package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// J1 (Dom's bench test of v0.2.0, 2026-10-12) on SYNTHETIC profiles with the
// structure of the fixtures in his show (the real GDTFs are third-party and
// stay out of the repository). Universe 3:
//
//	JD "Test JDC-like strobe" 1-18: Beam Module:0 Dimmer 1 (default 0) and
//	   StrobeRate 2 (255); Plate Module:0 Dimmer 3 (0) and RGB 4-6 (255);
//	   Plate Pixel:0/3/6 RGB 7-9, 10-12, 13-15 (255, colour only); Beam
//	   Pixel:0/1/2 Dimmer 16, 17, 18 (0). No master over the whole fixture.
//	RZ "Test Rayzor-like bar" 31-50: Head:0 Dimmer 31-32 (16-bit, default
//	   65535) and Shutter1 33 (50); RGBW Pixel:4/8/12 RGBW 34-37, 38-41,
//	   42-45 (255, colour only); Spark LED Strobe Module:0 Dimmer 46-47
//	   (16-bit, 65535) and Shutter1 48 (50); Head:0 Tilt 49-50 (32768).
//	ER "Test ERA-like spot" 61-65: Head:0 Shutter1 61 (22), Dimmer 62-63
//	   (16-bit, 0); Yoke:0 Pan 64-65 (16-bit, 32768).
//	PC "Test Paladin-like cube" 71-82: Beam 1/2/3:0 RGBW (0), no dimmer.
//
// Every profile goes in through POST /api/patch/import as full GDTF detail;
// levels are read back as ArtDmx at the fake transport.

func j1Channel(inst, attr string, bytes, index int, def uint32) map[string]any {
	max := uint32(1)<<(8*uint(bytes)) - 1
	return map[string]any{"geometryInstance": inst, "source": "gdtf", "attribute": attr, "functionName": attr,
		"dmxFrom": 0, "dmxTo": 255, "channelSets": []any{}, "hasDefault": true, "default": def, "defaultByteCount": bytes,
		"byteCount": bytes, "byteIndex": index, "functionsKnown": true,
		"functions": []any{map[string]any{"logicalAttribute": attr, "attribute": attr, "name": attr, "dmxFrom": 0, "dmxTo": max,
			"hasDefault": true, "default": def, "sets": []any{}}}}
}

// j1Profile builds channelFunctions from (offset, geometry, attribute,
// bytes, default) rows; a 16-bit row takes two offsets.
type j1Row struct {
	off   int
	inst  string
	attr  string
	bytes int
	def   uint32
}

func j1Functions(rows []j1Row) map[string]any {
	out := map[string]any{}
	for _, r := range rows {
		for b := 0; b < r.bytes; b++ {
			out[fmt.Sprint(r.off+b)] = j1Channel(r.inst, r.attr, r.bytes, b, r.def)
		}
	}
	return out
}

func j1RGB(off int, inst string, def uint32, w bool) []j1Row {
	rows := []j1Row{{off, inst, "ColorAdd_R", 1, def}, {off + 1, inst, "ColorAdd_G", 1, def}, {off + 2, inst, "ColorAdd_B", 1, def}}
	if w {
		rows = append(rows, j1Row{off + 3, inst, "ColorAdd_W", 1, def})
	}
	return rows
}

func j1Entries() []any {
	jd := []j1Row{{1, "Beam Module:0", "Dimmer", 1, 0}, {2, "Beam Module:0", "StrobeRate", 1, 255}, {3, "Plate Module:0", "Dimmer", 1, 0}}
	jd = append(jd, j1RGB(4, "Plate Module:0", 255, false)...)
	for i, inst := range []string{"Plate Pixel:0", "Plate Pixel:3", "Plate Pixel:6"} {
		jd = append(jd, j1RGB(7+3*i, inst, 255, false)...)
	}
	for i := 0; i < 3; i++ {
		jd = append(jd, j1Row{16 + i, fmt.Sprintf("Beam Pixel:%d", i), "Dimmer", 1, 0})
	}
	rz := []j1Row{{1, "Head:0", "Dimmer", 2, 65535}, {3, "Head:0", "Shutter1", 1, 50}}
	for i, inst := range []string{"RGBW Pixel:4", "RGBW Pixel:8", "RGBW Pixel:12"} {
		rz = append(rz, j1RGB(4+4*i, inst, 255, true)...)
	}
	rz = append(rz, j1Row{16, "Spark LED Strobe Module:0", "Dimmer", 2, 65535}, j1Row{18, "Spark LED Strobe Module:0", "Shutter1", 1, 50},
		j1Row{19, "Head:0", "Tilt", 2, 32768})
	er := []j1Row{{1, "Head:0", "Shutter1", 1, 22}, {2, "Head:0", "Dimmer", 2, 0}, {4, "Yoke:0", "Pan", 2, 32768}}
	pc := []j1Row{}
	for i := 0; i < 3; i++ {
		pc = append(pc, j1RGB(1+4*i, fmt.Sprintf("Beam %d:0", i+1), 0, true)...)
	}
	e := func(name, typ string, start, footprint int, rows []j1Row) map[string]any {
		return map[string]any{"name": name, "fixtureType": typ, "mode": "Test", "footprint": footprint, "universe": 3, "startAddress": start,
			"channelFunctions": j1Functions(rows)}
	}
	return []any{
		e("JD", "Test JDC-like strobe", 1, 18, jd),
		e("RZ", "Test Rayzor-like bar", 31, 20, rz),
		e("ER", "Test ERA-like spot", 61, 5, er),
		e("PC", "Test Paladin-like cube", 71, 12, pc),
	}
}

func newJ1Rig(t *testing.T) *c4aRig {
	t.Helper()
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": j1Entries()}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	if err := json.Unmarshal(doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r := &c4aRig{h: h, ids: map[string]string{}}
	for _, e := range resp.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	if len(r.ids) != 4 {
		t.Fatalf("patched %v", r.ids)
	}
	return r
}

// TestJ1_DarkAtArm (owner 2026-10-12): armed with an empty programmer every
// intensity starts at 0 whatever the GDTF default says — real Dimmer
// channels (the Rayzor-like head and SparkLED state 65535) and virtual
// dimmers (colour-only pixels whose colour defaults to 255) alike. Colour,
// position and everything else keep their GDTF defaults.
func TestJ1_DarkAtArm(t *testing.T) {
	r := newJ1Rig(t)
	r.arm(t)
	f := r.wire(t, 3)
	wantSlots(t, "JD: Beam Module dimmer 0, StrobeRate at its default 255, Plate Module dimmer 0", f, 1, 0, 255, 0)
	wantSlots(t, "JD: Plate Module colour keeps its default (its dimmer is a real channel)", f, 4, 255, 255, 255)
	wantSlots(t, "JD: plate pixels (virtual dimmers) dark", f, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "JD: beam pixel dimmers 0", f, 16, 0, 0, 0)
	wantSlots(t, "RZ: head dimmer 0 (GDTF default 65535), shutter at its default 50", f, 31, 0, 0, 50)
	wantSlots(t, "RZ: RGBW pixels (virtual dimmers) dark", f, 34, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "RZ: SparkLED dimmer 0 (GDTF default 65535), shutter 50", f, 46, 0, 0, 50)
	wantSlots(t, "ER: shutter 22, dimmer 0, pan at its default 32768", f, 61, 22, 0, 0, 128, 0)

	// The programmer view says what is on the wire: an untouched intensity
	// channel reads 0, and why.
	r.selectNames(t, "RZ")
	var v struct {
		Groups []struct {
			Attributes []struct {
				Attribute string
				DarkAtArm bool
				Channels  []struct {
					Offset       int
					Value        uint32
					Touched      bool
					DefaultKnown bool
					Default      uint32
				}
			}
		}
	}
	r.get(t, "/api/programmer", &v)
	found := false
	for _, g := range v.Groups {
		for _, a := range g.Attributes {
			if a.Attribute != "Dimmer" {
				continue
			}
			found = true
			if !a.DarkAtArm {
				t.Errorf("RZ Dimmer attribute does not say it is dark at Arm")
			}
			for _, c := range a.Channels {
				if c.Touched || c.Value != 0 || !c.DefaultKnown || c.Default != 0 {
					t.Errorf("RZ Dimmer channel %d untouched reads value %d default %d (known %v), want 0", c.Offset, c.Value, c.Default, c.DefaultKnown)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no Dimmer attribute for RZ")
	}

	// Highlight still brings intensity up (and back down when off).
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "RZ highlighted: head dimmer full", f, 31, 255, 255)
	wantSlots(t, "RZ highlighted: pixel 1 at its colour, full", f, 34, 255, 255, 255, 255)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "RZ highlight off: dark again", f, 31, 0, 0, 50, 0, 0, 0, 0)

	// Locate still brings intensity up, into the programmer.
	r.post(t, "/api/programmer/locate", map[string]any{}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "RZ located: head dimmer full", f, 31, 255, 255)
	wantSlots(t, "RZ located: pixels at their colour, full", f, 34, 255, 255, 255, 255)
}

// TestJ1_DarkAtArmTestsAndFaders: an untouched group fader claims nothing,
// so Tests keep working underneath; a dimmer test still drives its own
// level (no colour set = white); a colour test opens the virtual dimmers of
// what it tests, as it opens real dimmers.
func TestJ1_DarkAtArmTestsAndFaders(t *testing.T) {
	r := newJ1Rig(t)
	r.arm(t)
	if fv := r.faders(t); len(fv.Faders) == 0 {
		t.Fatalf("no faders")
	} else {
		for _, f := range fv.Faders {
			if f.Touched {
				t.Fatalf("fader %s touched at start", f.ID)
			}
		}
	}
	r.selectNames(t, "PC")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	f := r.wire(t, 3)
	wantSlots(t, "PC under a dimmer test at 128: cell 1 white at 128", f, 71, 128, 128, 128, 128)
	r.testsPost(t, "clear", map[string]any{})
	f = r.wire(t, 3)
	wantSlots(t, "PC after the test: dark", f, 71, 0, 0, 0, 0)

	// A colour test on the RZ (colour only pixels): the tests open its
	// dimmers — the head master and the pixels' virtual dimmers — so the
	// colour can be seen.
	r.selectNames(t, "RZ")
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "colour_mix_sweep", "on": true}}})
	lit := false
	for i := 0; i < 8 && !lit; i++ {
		r.advance(t, 300*time.Millisecond)
		f = r.wire(t, 3)
		for _, b := range slots(f, 34, 45) {
			lit = lit || b > 0
		}
	}
	if !lit {
		t.Errorf("a colour test on the RZ left its pixels dark: %v (head %v)", slots(f, 34, 45), slots(f, 31, 32))
	}
	wantSlots(t, "RZ under a colour test: head dimmer opened", f, 31, 255, 255)
	r.testsPost(t, "clear", map[string]any{})
	f = r.wire(t, 3)
	wantSlots(t, "RZ after the colour test: dark again", f, 31, 0, 0, 50, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
}

// TestJ1_JDCPlateFollowsTheFixture: the JDC-like fixture has two part
// dimmers (Beam Module, Plate Module) and no master over the whole fixture.
// Whole-fixture intensity — Dimmer set, the type fader, a dimmer test —
// reaches every part, the plate pixels' virtual dimmers included; a plate
// pixel's own virtual dimmer at 0 makes its colour 0 with the Plate Module
// dimmer on top; a test on a plate pixel opens the PLATE dimmer.
func TestJ1_JDCPlateFollowsTheFixture(t *testing.T) {
	r := newJ1Rig(t)
	jd := r.ids["JD"]
	r.arm(t)
	res := r.set(t, map[string]any{"targets": []any{r.target("JD")}, "attribute": "Dimmer", "dmx": 128})
	if len(res.Applied) != 8 {
		t.Errorf("whole JD Dimmer applied to %d channels, want 8 (2 module dimmers, 3 beam pixels, 3 plate pixels' virtual dimmers)", len(res.Applied))
	}
	f := r.wire(t, 3)
	wantSlots(t, "JD whole Dimmer 128: Beam Module and Plate Module dimmers", f, 1, 128, 255, 128)
	wantSlots(t, "JD whole Dimmer 128: plate pixels at 128 (their virtual dimmers follow)", f, 7, 128, 128, 128, 128, 128, 128, 128, 128, 128)
	wantSlots(t, "JD whole Dimmer 128: beam pixels", f, 16, 128, 128, 128)

	// A plate pixel's own virtual dimmer at 0: its colour goes to 0, the
	// Plate Module dimmer (a real channel) stays on top.
	r.set(t, map[string]any{"targets": []any{cellTarget(jd, "Plate Pixel:0")}, "attribute": "Dimmer", "dmx": 0})
	f = r.wire(t, 3)
	wantSlots(t, "plate pixel 0 virtual 0: its colour 0", f, 7, 0, 0, 0, 128, 128, 128)
	wantSlots(t, "plate pixel 0 virtual 0: Plate Module dimmer still 128", f, 3, 128)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)

	// The type fader moves the whole JD, plate pixels included.
	id := ""
	for _, fd := range r.faders(t).Faders {
		if fd.Kind == "type" && strings.HasPrefix(fd.ID, "type:Test JDC-like strobe|") {
			id = fd.ID
		}
	}
	r.fade(t, id, 1)
	f = r.wire(t, 3)
	wantSlots(t, "JD type fader full: plate pixels lit", f, 7, 255, 255, 255, 255, 255, 255, 255, 255, 255)
	wantSlots(t, "JD type fader full: module dimmers", f, 1, 255, 255, 255)
	r.fade(t, id, 0)
	f = r.wire(t, 3)
	wantSlots(t, "JD type fader 0: plate pixels dark", f, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)

	// A dimmer test on the whole JD drives the plate pixels at its level.
	r.selectNames(t, "JD")
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	f = r.wire(t, 3)
	wantSlots(t, "dimmer test on JD: plate pixels at the test's 128, not held at white", f, 7, 128, 128, 128)
	wantSlots(t, "dimmer test on JD: module dimmers carry the test", f, 1, 128, 255, 128)
	r.testsPost(t, "clear", map[string]any{})

	// A test on one plate pixel opens the Plate Module dimmer, not the Beam's.
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(jd, "Plate Pixel:3")}}, nil)
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	f = r.wire(t, 3)
	wantSlots(t, "test on plate pixel 3: Beam Module dimmer left alone, Plate Module dimmer opened", f, 1, 0, 255, 255)
	wantSlots(t, "test on plate pixel 3: its colour at the test's 128", f, 10, 128, 128, 128)
	r.testsPost(t, "clear", map[string]any{})

	// The Rayzor-like bar keeps its head master: whole-fixture Dimmer is
	// the head and SparkLED dimmers, not the pixels' virtual dimmers.
	res = r.set(t, map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "dmx": 255})
	if len(res.Applied) != 2 {
		t.Errorf("whole RZ Dimmer applied to %d channels, want 2 (head master and SparkLED)", len(res.Applied))
	}
}

type j1Fader struct {
	ID              string
	Kind            string
	Label           string
	Parent          string
	Part            string
	HasParts        bool
	Count           int
	Controllable    int
	NotControllable []string
	Touched         bool
}

// TestJ1_SubFixtureFaders (owner 2026-10-12): for each fixture type + mode
// with more than one intensity part, a Master fader plus one fader per
// part, grouped by geometry name. SET semantics below the programmer, last
// moved wins per channel, Release — G2's, per part. JD (no master): Master
// moves every part together. RZ: Master is the head dimmer; the RGBW
// pixels and the SparkLED are parts. Fixtures with one part keep their one
// fader.
func TestJ1_SubFixtureFaders(t *testing.T) {
	r := newJ1Rig(t)
	r.arm(t)
	var fv struct{ Faders []j1Fader }
	r.get(t, "/api/faders", &fv)
	byLabel := map[string]j1Fader{}
	order := make([]string, 0)
	for _, f := range fv.Faders {
		key := f.Label
		if f.Kind == "part" {
			for _, p := range fv.Faders {
				if p.ID == f.Parent {
					key = p.Label + " / " + f.Label
				}
			}
		}
		byLabel[key] = f
		order = append(order, key)
	}
	want := []string{
		"Test JDC-like strobe", "Test JDC-like strobe / Beam", "Test JDC-like strobe / Plate",
		"Test Rayzor-like bar", "Test Rayzor-like bar / Spark LED Strobe Module", "Test Rayzor-like bar / RGBW Pixel",
		"Test ERA-like spot", "Test Paladin-like cube",
	}
	if strings.Join(order, " | ") != strings.Join(want, " | ") {
		t.Fatalf("faders\n got %q\nwant %q", order, want)
	}
	jd, rz := byLabel["Test JDC-like strobe"], byLabel["Test Rayzor-like bar"]
	if !jd.HasParts || !rz.HasParts || byLabel["Test ERA-like spot"].HasParts || byLabel["Test Paladin-like cube"].HasParts {
		t.Errorf("hasParts: JD %v RZ %v ER %v PC %v, want true true false false", jd.HasParts, rz.HasParts,
			byLabel["Test ERA-like spot"].HasParts, byLabel["Test Paladin-like cube"].HasParts)
	}
	plate, beam := byLabel["Test JDC-like strobe / Plate"], byLabel["Test JDC-like strobe / Beam"]
	if plate.Kind != "part" || plate.Parent != jd.ID || plate.Part != "plate" || plate.Count != 1 || plate.Controllable != 1 {
		t.Errorf("Plate fader = %+v", plate)
	}

	// A part fader moves only its part.
	r.fade(t, plate.ID, 1)
	f := r.wire(t, 3)
	wantSlots(t, "Plate 100 %: Beam Module dimmer untouched (0), Plate Module dimmer full", f, 1, 0, 255, 255)
	wantSlots(t, "Plate 100 %: plate pixels full", f, 7, 255, 255, 255, 255, 255, 255, 255, 255, 255)
	wantSlots(t, "Plate 100 %: beam pixels untouched", f, 16, 0, 0, 0)
	// JD has no master channel: its Master moves every part together; moved
	// last, it wins on every channel.
	r.fade(t, jd.ID, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "JD Master 50 %: both module dimmers", f, 1, 128, 255, 128)
	wantSlots(t, "JD Master 50 %: plate pixels", f, 7, 128, 128, 128)
	wantSlots(t, "JD Master 50 %: beam pixels", f, 16, 128, 128, 128)
	// The Plate moved again wins its channels; the beam keeps the Master's.
	r.fade(t, plate.ID, 1)
	f = r.wire(t, 3)
	wantSlots(t, "Plate moved last: plate full, beam at the Master's 50 %", f, 1, 128, 255, 255)
	wantSlots(t, "Plate moved last: plate pixels full", f, 7, 255, 255, 255)
	// Release the Plate: its channels go back to the Master.
	r.post(t, "/api/faders/release", map[string]any{"id": plate.ID}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "Plate released: back to the Master's 50 %", f, 3, 128)
	wantSlots(t, "Plate released: plate pixels at 50 %", f, 7, 128, 128, 128)
	r.fade(t, beam.ID, 0)
	f = r.wire(t, 3)
	wantSlots(t, "Beam 0: beam dimmers 0, plate untouched by it", f, 1, 0, 255, 128)
	wantSlots(t, "Beam 0: beam pixels 0", f, 16, 0, 0, 0)

	// RZ: Master is the head dimmer only.
	r.fade(t, rz.ID, 1)
	f = r.wire(t, 3)
	wantSlots(t, "RZ Master full: head dimmer", f, 31, 255, 255)
	wantSlots(t, "RZ Master full: pixels stay dark (their own part)", f, 34, 0, 0, 0, 0)
	wantSlots(t, "RZ Master full: SparkLED stays dark (its own part)", f, 46, 0, 0)
	r.fade(t, byLabel["Test Rayzor-like bar / RGBW Pixel"].ID, 1)
	r.fade(t, byLabel["Test Rayzor-like bar / Spark LED Strobe Module"].ID, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "RZ RGBW Pixel full: pixels at their colour", f, 34, 255, 255, 255, 255)
	wantSlots(t, "RZ SparkLED 50 %: 32768", f, 46, 128, 0)
	// Below the programmer: a programmed cell level wins over its part.
	r.set(t, map[string]any{"targets": []any{cellTarget(r.ids["RZ"], "RGBW Pixel:4")}, "attribute": "Dimmer", "dmx": 51})
	f = r.wire(t, 3)
	wantSlots(t, "programmer above the part fader: pixel 1 at 51", f, 34, 51, 51, 51, 51)
	wantSlots(t, "pixel 2 still at its part fader's full", f, 38, 255, 255, 255, 255)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "release all: JD dark again", f, 1, 0, 255, 0, 255, 255, 255, 0, 0, 0)
	wantSlots(t, "release all: RZ dark again", f, 31, 0, 0, 50, 0, 0, 0, 0)
}

// TestFaderBar_J1Parts runs static/js/testdata/faders_j1_test.js: the real
// index.html and the LITERAL fader bar scripts against this real server on
// the J1 rig — a type with parts is one labelled cluster, Master first,
// parts named "<type> · <part>"; one-part types keep their plain fader.
func TestFaderBar_J1Parts(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newJ1Rig(t)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "faders_j1_test.js"), jsDir, ts.URL).CombinedOutput()
	t.Logf("faders_j1_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("faders_j1_test.js failed: %v", err)
	}
}
