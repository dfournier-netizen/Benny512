package web

import (
	"net/http"
	"testing"
	"time"
)

// J1c (owner 2026-10-13 09:31): "Master should be an overall dimmer. If
// master is at 0 but the sub-fixtures are at full: no output. If master is
// at full, SparkLED at full and pixels at 0: we see only the SparkLED."
// Effective part output = master x part, without double dimming: a part
// under the master channel's geometry is dimmed by that channel on the
// fixture; any other part is multiplied in software. On the J1 rig the RZ
// SparkLED sits under the Head (governed), the RGBW pixels on the Base
// (multiplied); the JD has no master channel (all multiplied).
//
// Universe 3, RZ at 31: head 31-32, shutter 33, pixels 34-37 / 38-41 /
// 42-45, SparkLED 46-47. JD at 1: Beam Module 1, Plate Module 3, beam
// pixels 16-18.

func j1cFaders(t *testing.T, r *c4aRig) map[string]j1Fader {
	t.Helper()
	var fv struct{ Faders []j1Fader }
	r.get(t, "/api/faders", &fv)
	out := map[string]j1Fader{}
	parents := map[string]string{}
	for _, f := range fv.Faders {
		if f.Kind == "type" {
			parents[f.ID] = f.Label
		}
	}
	for _, f := range fv.Faders {
		k := f.Label
		if f.Kind == "part" {
			k = parents[f.Parent] + " / " + f.Label
		}
		out[k] = f
	}
	return out
}

func TestJ1c_MasterMultipliesTheParts(t *testing.T) {
	r := newJ1Rig(t)
	r.arm(t)
	fd := j1cFaders(t, r)
	rz, rgbw, spark := fd["Test Rayzor-like bar"].ID, fd["Test Rayzor-like bar / RGBW Pixel"].ID, fd["Test Rayzor-like bar / Spark LED Strobe Module"].ID
	f := r.wire(t, 3)
	wantSlots(t, "armed: RZ head, pixel 1 dark", f, 31, 0, 0, 50, 0, 0, 0, 0)
	wantSlots(t, "armed: SparkLED dark", f, 46, 0, 0)

	// Parts at full, master at rest (0): no output — the SparkLED through
	// the head dimmer on the fixture (its own channel is at full), the
	// pixels in software.
	r.fade(t, rgbw, 1)
	r.fade(t, spark, 1)
	f = r.wire(t, 3)
	wantSlots(t, "parts full, master 0: head dimmer 0", f, 31, 0, 0)
	wantSlots(t, "parts full, master 0: pixels multiplied by 0", f, 34, 0, 0, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "parts full, master 0: SparkLED at its level (the head dims it on the fixture)", f, 46, 255, 255)
	r.fade(t, rz, 1)
	f = r.wire(t, 3)
	wantSlots(t, "master full: head", f, 31, 255, 255)
	wantSlots(t, "master full: pixels", f, 34, 255, 255, 255, 255)
	wantSlots(t, "master full: SparkLED", f, 46, 255, 255)
	// "Master full, SparkLED full, pixels at 0: we see only the SparkLED."
	r.fade(t, rgbw, 0)
	f = r.wire(t, 3)
	wantSlots(t, "pixels 0: pixels dark", f, 34, 0, 0, 0, 0)
	wantSlots(t, "pixels 0: SparkLED and head stay", f, 46, 255, 255)
	wantSlots(t, "pixels 0: head", f, 31, 255, 255)
	// Moving the Master never overwrites the parts' levels; it multiplies.
	r.fade(t, rgbw, 1)
	r.fade(t, rz, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "master 50 %: head at 50 %", f, 31, 128, 0)
	wantSlots(t, "master 50 %: pixels 255 x 128/255 (software)", f, 34, 128, 128, 128, 128)
	wantSlots(t, "master 50 %: SparkLED keeps its own level (the head dims it on the fixture)", f, 46, 255, 255)
	r.fade(t, rgbw, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "pixels 50 % under master 50 %: 255 x 128/255 x 128/255", f, 34, 64, 64, 64, 64)
	wantSlots(t, "the Master is untouched by a part", f, 31, 128, 0)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)

	// JD: no master channel, so every part is multiplied in software.
	r.fade(t, fd["Test JDC-like strobe / Plate"].ID, 1)
	f = r.wire(t, 3)
	wantSlots(t, "JD Plate full, Master 0: Plate Module dimmer 0", f, 3, 0)
	r.fade(t, fd["Test JDC-like strobe"].ID, 0.5)
	f = r.wire(t, 3)
	wantSlots(t, "JD Master 50 %: Plate 255 x 128/255; Beam Module (its part at rest) 0", f, 1, 0, 255, 128)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)

	// Console: whole-fixture Dimmer is the master level only.
	res := r.set(t, map[string]any{"targets": []any{r.target("RZ")}, "attribute": "Dimmer", "fraction": 1})
	if len(res.Applied) != 2 {
		t.Errorf("whole RZ Dimmer applied to %d channels, want 2 (the head dimmer and the virtual master)", len(res.Applied))
	}
	f = r.wire(t, 3)
	wantSlots(t, "whole RZ Dimmer full: head", f, 31, 255, 255)
	wantSlots(t, "whole RZ Dimmer full: parts keep their rest (0)", f, 34, 0, 0, 0, 0)
	r.fade(t, rgbw, 1)
	r.fade(t, spark, 1)
	f = r.wire(t, 3)
	wantSlots(t, "parts up under the Console master: pixels", f, 34, 255, 255, 255, 255)

	// Lowlight of the whole RZ (stepped to the ER): through the master
	// once — head and pixels at 20 %, the SparkLED not scaled on top (the
	// head dims it on the fixture).
	r.set(t, map[string]any{"targets": []any{r.target("ER")}, "attribute": "Dimmer", "dmx": 255})
	r.selectNames(t, "ER", "RZ")
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true, "lowlight": true}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"step": "next"}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "RZ lowlit: head at 20 %", f, 31, 51, 51)
	wantSlots(t, "RZ lowlit: pixels at 20 % (software, once)", f, 34, 51, 51, 51, 51)
	wantSlots(t, "RZ lowlit: SparkLED not scaled on top", f, 46, 255, 255)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": false, "lowlight": false}, nil)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "all"}, nil)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)

	// A stored group of the whole RZ is its master level.
	r.selectNames(t, "RZ")
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Bars"}, nil)
	var gid string
	var fv struct{ Faders []j1Fader }
	r.get(t, "/api/faders", &fv)
	for _, g := range fv.Faders {
		if g.Kind == "group" {
			gid = g.ID
		}
	}
	r.fade(t, gid, 1)
	r.fade(t, rgbw, 1)
	f = r.wire(t, 3)
	wantSlots(t, "group fader full: head", f, 31, 255, 255)
	wantSlots(t, "group fader full + pixels part full: pixels", f, 34, 255, 255, 255, 255)
	wantSlots(t, "group fader full: SparkLED part at rest", f, 46, 0, 0)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)

	// A dimmer test on the whole RZ drives the master; every part is held
	// open, so each shows once at the test's level.
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 128}}})
	f = r.wire(t, 3)
	wantSlots(t, "dimmer test: head carries it", f, 31, 128, 128)
	wantSlots(t, "dimmer test: pixels white x the virtual master's 128", f, 34, 128, 128, 128, 128)
	wantSlots(t, "dimmer test: SparkLED held open (the head dims it)", f, 46, 255, 255)
	r.testsPost(t, "clear", map[string]any{})
}

// TestJ1c_HighlightOpensACellsMaster (owner 2026-10-13): Highlight on a
// single cell opens its fixture's master and its part dimmer where that
// applies, as Tests do; Highlight of a whole fixture lights every part.
func TestJ1c_HighlightOpensACellsMaster(t *testing.T) {
	r := newJ1Rig(t)
	jd, rz := r.ids["JD"], r.ids["RZ"]
	r.arm(t)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(rz, "RGBW Pixel:4")}}, nil)
	r.post(t, "/api/programmer/highlight", map[string]any{"highlight": true}, nil)
	f := r.wire(t, 3)
	wantSlots(t, "RZ pixel highlighted: head opened", f, 31, 255, 255)
	wantSlots(t, "RZ pixel highlighted: lit (its own level and the master open)", f, 34, 255, 255, 255, 255)
	wantSlots(t, "RZ pixel highlighted: the other pixels stay dark", f, 38, 0, 0, 0, 0)
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(jd, "Plate Pixel:3")}}, nil)
	f = r.wire(t, 3)
	wantSlots(t, "JD plate pixel highlighted: Plate Module dimmer opened, Beam Module left alone", f, 1, 0, 255, 255)
	r.selectNames(t, "RZ")
	f = r.wire(t, 3)
	wantSlots(t, "whole RZ highlighted: head", f, 31, 255, 255)
	wantSlots(t, "whole RZ highlighted: every pixel", f, 34, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255)
	wantSlots(t, "whole RZ highlighted: SparkLED", f, 46, 255, 255)
}

// TestJ1c_LegacyProfileCountsPartsAsGoverned: a profile imported before
// J1c has no geometry tree; every part of a fixture with a master channel
// then counts as dimmed by it on the fixture (no software multiply), and
// the type fader says so.
func TestJ1c_LegacyProfileCountsPartsAsGoverned(t *testing.T) {
	r := newJ1Rig(t)
	legacy := j1Entries()[1].(map[string]any)
	delete(legacy, "geometryParents")
	legacy["name"], legacy["fixtureType"], legacy["startAddress"] = "RZL", "Test Rayzor-like bar (legacy)", 101
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{legacy}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	r.arm(t)
	fd := j1cFaders(t, r)
	if !fd["Test Rayzor-like bar (legacy)"].TreeUnknown || fd["Test Rayzor-like bar"].TreeUnknown || fd["Test JDC-like strobe"].TreeUnknown {
		t.Errorf("treeUnknown: legacy %v, RZ %v, JD %v; want true, false, false (the JD has no master channel to assume anything of)",
			fd["Test Rayzor-like bar (legacy)"].TreeUnknown, fd["Test Rayzor-like bar"].TreeUnknown, fd["Test JDC-like strobe"].TreeUnknown)
	}
	r.fade(t, fd["Test Rayzor-like bar (legacy) / RGBW Pixel"].ID, 1)
	time.Sleep(0)
	f := r.wire(t, 3)
	wantSlots(t, "legacy: pixels at their part level, not multiplied by the master at rest", f, 104, 255, 255, 255, 255)
	wantSlots(t, "legacy: head at rest (dark on the fixture)", f, 101, 0, 0)
}
