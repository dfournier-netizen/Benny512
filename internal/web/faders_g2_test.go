package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Console-lite G2 — group faders, server side — at the HTTP surface, on the
// G1 rig (BMFL "B1"/"B2" Mode 1 with a real 16-bit Dimmer; LEDBeam "L1";
// Paladin Cube "P1" in "Cells 24CH": three RGBW cells, no dimmer; the
// unprofiled "Generic 4ch" G1; the spec-derived RGB-only "S1") plus "P2":
// the same real Paladin file in its third mode, "RGB 3CH", at universe 1
// slot 150 — so one fixture type is patched in two modes. Every profile
// goes through the literal gdtfparse.js and POST /api/patch/import; every
// level is read back as ArtDmx decoded at session.FakeTransport.

// g2ModeHarnessJS is channelDetailHarnessJS with the mode chosen by index.
var g2ModeHarnessJS = strings.Replace(channelDetailHarnessJS, "parsed.modes[0]", "parsed.modes[Number(process.argv[5])]", 1)

func vendorEntryMode(t *testing.T, extract string, mode int, name string, universe, start int) map[string]any {
	t.Helper()
	nodePath := nodeOrSkip(t)
	abs, _ := filepath.Abs("static/js")
	script := filepath.Join(t.TempDir(), "mode.js")
	if err := os.WriteFile(script, []byte(g2ModeHarnessJS), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nodePath, script, filepath.Join(abs, "testdata", "tinydom.js"), filepath.Join(abs, "gdtfparse.js"),
		filepath.Join(abs, "testdata", extract), strconv.Itoa(mode)).CombinedOutput()
	if err != nil {
		t.Fatalf("gdtfparse.js: %v\n%s", err, out)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	body["name"], body["universe"], body["startAddress"] = name, universe, start
	return body
}

type g2Fader struct {
	ID              string
	Kind            string
	Label           string
	Count           int
	Controllable    int
	NotControllable []string
	Level           *float64
	Touched         bool
	LastMovedOrder  uint64
}

type g2Faders struct {
	Revision uint64
	Faders   []g2Fader
}

func newG2Rig(t *testing.T) *c4aRig {
	t.Helper()
	r := newG1Rig(t)
	p2 := vendorEntryMode(t, "paladin_cube_real_extract.xml", 2, "P2", 1, 150)
	if rr := r.do(t, "POST", "/api/patch/import", map[string]any{"mode": "merge", "entries": []any{p2}}, nil); rr.Code != http.StatusOK {
		t.Fatalf("import P2: %d %s", rr.Code, rr.Body.String())
	}
	var pr struct {
		Patch struct{ Entries []struct{ ID, Name string } }
	}
	_ = json.Unmarshal(r.do(t, "GET", "/api/patch", nil, nil).Body.Bytes(), &pr)
	for _, e := range pr.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	return r
}

func (r *c4aRig) faders(t *testing.T) g2Faders {
	t.Helper()
	rr := r.do(t, "GET", "/api/faders", nil, nil)
	var v g2Faders
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &v) != nil {
		t.Fatalf("GET /api/faders: %d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if h := rr.Header().Get("X-Benny-Faders"); h != strconv.FormatUint(v.Revision, 10) {
		t.Fatalf("X-Benny-Faders %q, body revision %d", h, v.Revision)
	}
	return v
}

// fader finds a fader by label.
func (v g2Faders) fader(t *testing.T, label string) g2Fader {
	t.Helper()
	for _, f := range v.Faders {
		if f.Label == label {
			return f
		}
	}
	labels := make([]string, 0)
	for _, f := range v.Faders {
		labels = append(labels, f.Label)
	}
	t.Fatalf("no fader %q among %q", label, labels)
	return g2Fader{}
}

func (r *c4aRig) fade(t *testing.T, id string, level float64) {
	t.Helper()
	r.post(t, "/api/faders/set", map[string]any{"id": id, "level": level}, nil)
}

// typeLabels maps a fixture name to its type fader's label.
func (r *c4aRig) typeOf(t *testing.T, name string) (fixtureType, mode string) {
	t.Helper()
	var fx struct {
		Fixtures []struct{ EntryID, FixtureType, Mode string }
	}
	_ = json.Unmarshal(r.do(t, "GET", "/api/programmer/fixtures", nil, nil).Body.Bytes(), &fx)
	for _, f := range fx.Fixtures {
		if f.EntryID == r.ids[name] {
			return f.FixtureType, f.Mode
		}
	}
	t.Fatalf("no fixture %s", name)
	return "", ""
}

// TestFaderTypeFadersAndModes: one fader per type+mode (the mode named only
// for the type patched in two), each driving exactly its fixtures — a real
// 16-bit dimmer over its full range, a virtual dimmer as white — and the
// fixtures with neither listed as not controllable.
func TestFaderTypeFadersAndModes(t *testing.T) {
	r := newG2Rig(t)
	bmfl, bmode := r.typeOf(t, "B1")
	pal, pmode1 := r.typeOf(t, "P1")
	_, pmode2 := r.typeOf(t, "P2")
	gen, _ := r.typeOf(t, "G1")
	v := r.faders(t)
	fb := v.fader(t, bmfl)
	if fb.ID != "type:"+bmfl+"|"+bmode || fb.Kind != "type" || fb.Count != 2 || fb.Controllable != 2 || fb.Touched || fb.Level != nil || len(fb.NotControllable) != 0 {
		t.Errorf("BMFL fader: %+v", fb)
	}
	fp1, fp2 := v.fader(t, pal+" — "+pmode1), v.fader(t, pal+" — "+pmode2)
	if fp1.ID == fp2.ID || fp1.Count != 1 || fp2.Count != 1 || fp1.Controllable != 1 || fp2.Controllable != 1 {
		t.Errorf("two Paladin modes = two faders: %+v / %+v", fp1, fp2)
	}
	fg := v.fader(t, gen)
	if fg.Controllable != 0 || strings.Join(fg.NotControllable, ",") != "G1" {
		t.Errorf("Generic 4ch (no dimmer, no RGB) must list G1 as not controllable: %+v", fg)
	}

	r.arm(t)
	r.fade(t, fb.ID, 0.5) // round(0.5 x 65535) = 32768
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 dimmer from the BMFL fader", u0, 40, 0x80, 0x00)
	wantSlots(t, "B2 dimmer from the BMFL fader", u0, 81, 0x80, 0x00)
	u1 := r.wire(t, 1)
	wantSlots(t, "L1 dimmer untouched (default 0)", u1, 14, 0)
	p1Cells(t, "P1 untouched", u1, 0, 0, 0, 0)

	r.fade(t, fp1.ID, 1)
	r.fade(t, fp2.ID, 0.5) // virtual 8-bit: 128 -> white 128
	u1 = r.wire(t, 1)
	p1Cells(t, "P1 (Cells 24CH fader at full): white full", u1, 65535, 65535, 65535, 65535)
	wantSlots(t, "P2 (RGB 3CH fader at half): white half", u1, 150, 128, 128, 128)
	if f := r.faders(t).fader(t, fb.Label); !f.Touched || f.Level == nil || *f.Level != 0.5 || f.LastMovedOrder == 0 {
		t.Errorf("moved fader state: %+v", f)
	}

	r.post(t, "/api/faders/release", map[string]any{"id": fb.ID}, nil)
	wantSlots(t, "B1 released: base default 0", r.wire(t, 0), 40, 0, 0)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	u1 = r.wire(t, 1)
	p1Cells(t, "release all: P1 at defaults", u1, 0, 0, 0, 0)
	wantSlots(t, "release all: P2 at defaults", u1, 150, 0, 0, 0)
	for _, f := range r.faders(t).Faders {
		if f.Touched || f.Level != nil {
			t.Errorf("after release all: %+v", f)
		}
	}
	if rr := r.do(t, "POST", "/api/faders/set", map[string]any{"id": fb.ID, "level": 1.5}, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("level 1.5: %d", rr.Code)
	}
	if rr := r.do(t, "POST", "/api/faders/set", map[string]any{"id": "type:nope|x", "level": 1}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown fader: %d", rr.Code)
	}
}

// TestFaderStoredGroupWithCell: a stored group is a fader; a cell member is
// that cell only; the fader follows the group's rename and delete.
func TestFaderStoredGroupWithCell(t *testing.T) {
	r := newG2Rig(t)
	p1 := r.ids["P1"]
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{cellTarget(p1, "Beam 2:0"), r.target("B2"), r.target("G1")}}, nil)
	var pv struct{ StoredGroups []struct{ ID string } }
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "Mix"}, &pv)
	gid := pv.StoredGroups[0].ID
	f := r.faders(t).fader(t, "Mix")
	if f.ID != "group:"+gid || f.Kind != "group" || f.Count != 3 || f.Controllable != 2 || strings.Join(f.NotControllable, ",") != "G1" {
		t.Errorf("group fader: %+v", f)
	}
	r.arm(t)
	r.fade(t, f.ID, 1)
	u1 := r.wire(t, 1)
	wantSlots(t, "P1 cell 1 untouched", u1, 101, 0, 0, 0, 0, 0, 0, 0, 0)
	wantSlots(t, "P1 cell 2 white full", u1, 109, 255, 255, 255, 255, 255, 255, 255, 255)
	wantSlots(t, "P1 cell 3 untouched", u1, 117, 0, 0, 0, 0, 0, 0, 0, 0)
	u0 := r.wire(t, 0)
	wantSlots(t, "B2 full", u0, 81, 255, 255)
	wantSlots(t, "B1 not in the group", u0, 40, 0, 0)

	r.post(t, "/api/programmer/groups/rename", map[string]any{"id": gid, "name": "Front"}, nil)
	if f2 := r.faders(t).fader(t, "Front"); f2.ID != f.ID || !f2.Touched {
		t.Errorf("renamed group fader: %+v", f2)
	}
	r.post(t, "/api/programmer/groups/delete", map[string]any{"id": gid}, nil)
	for _, x := range r.faders(t).Faders {
		if x.ID == f.ID {
			t.Errorf("deleted group's fader still listed: %+v", x)
		}
	}
	wantSlots(t, "deleted group's fader releases B2", r.wire(t, 0), 81, 0, 0)
}

// TestFaderLayering: tests < faders < programmer on the dimmer.
func TestFaderLayering(t *testing.T) {
	r := newG2Rig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	var pv struct{ StoredGroups []struct{ ID string } }
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "B1 only"}, &pv)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 200}},
		"scope": map[string]any{"kind": "group", "group": pv.StoredGroups[0].ID}})
	testDimmer := append([]byte(nil), slots(r.wire(t, 0), 40, 41)...)
	if testDimmer[0] == 0 {
		t.Fatalf("dimmer test not on the wire: %v", testDimmer)
	}
	bmfl, _ := r.typeOf(t, "B1")
	r.fade(t, r.faders(t).fader(t, bmfl).ID, 0.25) // round(0.25 x 65535) = 16384
	wantSlots(t, "fader beats the running test", r.wire(t, 0), 40, 0x40, 0x00)
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Dimmer", "dmx": 1000})
	wantSlots(t, "programmer beats the fader", r.wire(t, 0), 40, 3, 232)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "selection", "group": "dimmer"}, nil)
	wantSlots(t, "programmer released: the fader again", r.wire(t, 0), 40, 0x40, 0x00)
	r.post(t, "/api/faders/release-all", map[string]any{}, nil)
	wantSlots(t, "fader released: the test again", r.wire(t, 0), 40, testDimmer...)
}

// TestFaderOverlapMostRecentlyMoved: a fixture in two moved faders follows
// the most recently moved; releasing it hands the fixture back.
func TestFaderOverlapMostRecentlyMoved(t *testing.T) {
	r := newG2Rig(t)
	r.selectNames(t, "B1")
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "B1 only"}, nil)
	r.arm(t)
	bmfl, _ := r.typeOf(t, "B1")
	v := r.faders(t)
	typ, grp := v.fader(t, bmfl).ID, v.fader(t, "B1 only").ID
	r.fade(t, typ, 0.25)
	r.fade(t, grp, 0.75) // round(0.75 x 65535) = 49151
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 follows the group (moved last)", u0, 40, 0xBF, 0xFF)
	wantSlots(t, "B2 follows the type fader", u0, 81, 0x40, 0x00)
	r.fade(t, typ, 0.5)
	wantSlots(t, "type fader moved last: B1 follows it", r.wire(t, 0), 40, 0x80, 0x00)
	r.post(t, "/api/faders/release", map[string]any{"id": typ}, nil)
	u0 = r.wire(t, 0)
	wantSlots(t, "type released: B1 handed back to the group", u0, 40, 0xBF, 0xFF)
	wantSlots(t, "type released: B2 to its default", u0, 81, 0, 0)
	r.post(t, "/api/faders/release", map[string]any{"id": grp}, nil)
	wantSlots(t, "group released: B1 to its default", r.wire(t, 0), 40, 0, 0)
}

// TestFaderRevisionBroadcastShowSwitchAndDisarmed: stale revision 409, every
// change broadcast as {"type":"faders"}, cleared on a show switch, nothing
// on the wire while disarmed.
func TestFaderRevisionBroadcastShowSwitchAndDisarmed(t *testing.T) {
	r := newG2Rig(t)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	ws := dialTestWS(t, ts, r.h.srv)
	bmfl, _ := r.typeOf(t, "B1")
	v := r.faders(t)
	id, rev := v.fader(t, bmfl).ID, v.Revision

	r.fade(t, id, 1)
	r.h.clock.Advance(200 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Fatalf("%d ArtDmx frames while disarmed", n)
	}
	if got := fadersMessages(ws, 500*time.Millisecond); len(got) != 1 || got[0] != rev+1 {
		t.Errorf("WebSocket faders messages %v, want [%d]", got, rev+1)
	}
	rr := r.do(t, "POST", "/api/faders/set", map[string]any{"id": id, "level": 0}, map[string]string{"X-Benny-Faders": strconv.FormatUint(rev, 10)})
	if rr.Code != http.StatusConflict {
		t.Errorf("stale revision: %d %s, want 409", rr.Code, rr.Body.String())
	}
	r.arm(t)
	wantSlots(t, "armed: the stored fader level", r.wire(t, 0), 40, 255, 255)
	rr = r.do(t, "POST", "/api/faders/set", map[string]any{"id": id, "level": 0.5}, map[string]string{"X-Benny-Faders": strconv.FormatUint(rev+1, 10)})
	if rr.Code != http.StatusOK || rr.Header().Get("X-Benny-Faders") != strconv.FormatUint(rev+2, 10) {
		t.Errorf("current revision: %d header %q, want 200 and %d", rr.Code, rr.Header().Get("X-Benny-Faders"), rev+2)
	}

	r.post(t, "/api/patch/new", map[string]any{"name": "Next show"}, nil)
	if got := fadersMessages(ws, 500*time.Millisecond); len(got) == 0 {
		t.Errorf("no faders broadcast on the show switch")
	}
	if f := r.faders(t); len(f.Faders) != 0 {
		t.Errorf("an empty show has faders: %+v", f.Faders)
	}
	r.h.tport.TakeSent()
	r.h.clock.Advance(200 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Errorf("%d ArtDmx frames of the old show's faders after the switch", n)
	}
}

// fadersMessages collects every {"type":"faders"} revision within d.
func fadersMessages(c *wsTestClient, settle time.Duration) []uint64 {
	// Waits for the first {"type":"faders"} message (deadline, not delay),
	// then collects for settle.
	return c.typedMessages("faders", settle, true)
}
