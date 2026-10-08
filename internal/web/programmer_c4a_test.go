package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// Console-lite C4a — the programmer core — proven at the HTTP surface only:
// real vendor profiles parsed by the literal browser gdtfparse.js and
// imported through POST /api/patch/import, the real handlers, and every
// level read back as ArtDmx decoded at session.FakeTransport under
// FakeClock. Nothing here calls programmer code directly, so this file
// compiles and runs against the pre-C4a tree, where every test fails.
//
// The rig (two universes):
//   universe 0: BMFL Spot "B1" at 1 and "B2" at 42 (Mode 1, 41 ch, 16-bit)
//   universe 1: Robin 100 LEDBeam "L1" at 1 (mode 3, 14 ch), Paladin Cube
//               "P1" at 101 (Cells 24CH: three RGBW cells, 16-bit), and an
//               unprofiled "G1" at 201 (footprint 4, no channel map)

type c4aChannel struct {
	EntryID      string
	Cell         string
	Offset       int
	ByteCount    int
	Value        uint32
	Touched      bool
	DefaultKnown bool
	Default      uint32
	Function     int
}

type c4aAttr struct {
	Attribute      string
	Mixed          bool
	Value          *uint32
	Touched        bool
	AllTouched     bool
	UnknownDefault bool
	Missing        int
	Channels       []c4aChannel
}

type c4aView struct {
	Revision uint64
	Output   struct {
		State string
		Live  bool
		Note  string
	}
	Selection []struct{ EntryID, Cell, Name string }
	Groups    []struct {
		Group      string
		Attributes []c4aAttr
	}
	Raw []struct {
		EntryID string
		Offsets []struct {
			Offset  int
			Value   uint32
			Touched bool
		}
	}
	Touched int
	Ignored []struct{ EntryID, Reason string }
}

func (v c4aView) attr(name string) *c4aAttr {
	for _, g := range v.Groups {
		for i := range g.Attributes {
			if g.Attributes[i].Attribute == name {
				return &g.Attributes[i]
			}
		}
	}
	return nil
}

type c4aSetResult struct {
	Revision uint64
	Output   struct {
		State string
		Live  bool
		Note  string
	}
	Applied []struct {
		EntryID      string
		Cell         string
		Offset       int
		Attribute    string
		FunctionName string
		Value        uint32
	}
	ModeMasters []struct {
		EntryID         string
		Offset          int
		ModeMaster      string
		Resolved        bool
		MasterOffset    int
		MasterAttribute string
		Rule            string
		RangeFrom       uint32
		RangeTo         uint32
		Previous        *uint32
		Value           uint32
		Changed         bool
		Note            string
	}
	Skipped []struct {
		EntryID string
		Reason  string
	}
}

type c4aRig struct {
	h   *testHarness
	ids map[string]string
}

func vendorEntry(t *testing.T, extract, name string, universe, start int) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(parseVendorEntryBody(t, extract), &body); err != nil {
		t.Fatal(err)
	}
	body["name"], body["universe"], body["startAddress"] = name, universe, start
	return body
}

func newC4aRig(t *testing.T) *c4aRig {
	t.Helper()
	return newC4aRigWith(t, nil)
}

// newC4aRigWith runs setup on the harness before the rig is imported (C4b:
// a persisted show store, so the show catalog can switch shows).
func newC4aRigWith(t *testing.T, setup func(h *testHarness)) *c4aRig {
	t.Helper()
	h := newHarness(t)
	t.Cleanup(h.srv.Close)
	if setup != nil {
		setup(h)
	}
	entries := []any{
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B1", 0, 1),
		vendorEntry(t, "robe_bmfl_spot_real_extract.xml", "B2", 0, 42),
		vendorEntry(t, "robe_ledbeam100_real_extract.xml", "L1", 1, 1),
		vendorEntry(t, "paladin_cube_real_extract.xml", "P1", 1, 101),
		map[string]any{"name": "G1", "fixtureType": "Generic 4ch", "footprint": 4, "universe": 1, "startAddress": 201},
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/import", map[string]any{"mode": "fresh", "entries": entries}); rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp struct {
		Patch struct {
			Entries []struct{ ID, Name string }
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	r := &c4aRig{h: h, ids: map[string]string{}}
	for _, e := range resp.Patch.Entries {
		r.ids[e.Name] = e.ID
	}
	if len(r.ids) != 5 {
		t.Fatalf("patched %d entries, want 5", len(r.ids))
	}
	return r
}

func (r *c4aRig) do(t *testing.T, method, path string, body any, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	r.h.srv.Handler().ServeHTTP(rr, req)
	return rr
}

func (r *c4aRig) post(t *testing.T, path string, body any, out any) {
	t.Helper()
	rr := r.do(t, "POST", path, body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST %s %v: %d %s", path, body, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if out != nil {
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
	}
}

func (r *c4aRig) view(t *testing.T) c4aView {
	t.Helper()
	rr := r.do(t, "GET", "/api/programmer", nil, nil)
	var v c4aView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &v) != nil {
		t.Fatalf("GET /api/programmer: %d %s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return v
}

func (r *c4aRig) target(name string) map[string]any {
	return map[string]any{"entryId": r.ids[name], "cell": ""}
}

func (r *c4aRig) selectNames(t *testing.T, names ...string) {
	t.Helper()
	targets := make([]any, 0, len(names))
	for _, n := range names {
		targets = append(targets, r.target(n))
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": targets}, nil)
}

func (r *c4aRig) set(t *testing.T, body map[string]any) c4aSetResult {
	t.Helper()
	var res c4aSetResult
	r.post(t, "/api/programmer/set", body, &res)
	return res
}

func (r *c4aRig) arm(t *testing.T) {
	t.Helper()
	r.post(t, "/api/output/arm", map[string]any{"client": "c4a"}, nil)
}

// wire is the newest ArtDmx frame for universe u after one engine tick: the
// composed output as a receiver sees it.
func (r *c4aRig) wire(t *testing.T, u uint16) []byte {
	t.Helper()
	r.h.tport.TakeSent()
	r.h.clock.Advance(30 * time.Millisecond)
	pa, _ := artnet.PortAddressFromRaw(u)
	var last []byte
	for _, sp := range r.h.tport.TakeSent() {
		if sp.DecodeErr == nil && sp.Packet.Kind == artnet.KindDmx && sp.Packet.Dmx.Net == pa.Net && sp.Packet.Dmx.SubUni == pa.SubUni() {
			last = append([]byte(nil), sp.Packet.Dmx.Data...)
		}
	}
	if last == nil {
		t.Fatalf("no ArtDmx frame for universe %d on the wire", u)
	}
	return last
}

// slots returns frame[from..to] (1-based, inclusive).
func slots(f []byte, from, to int) []byte { return f[from-1 : to] }

func wantSlots(t *testing.T, what string, f []byte, from int, want ...byte) {
	t.Helper()
	got := slots(f, from, from+len(want)-1)
	if !bytes.Equal(got, want) {
		t.Errorf("%s: slots %d-%d = %v, want %v", what, from, from+len(want)-1, got, want)
	}
}

func artDmxCount(sent []session.SentPacket) int {
	n := 0
	for _, sp := range sent {
		if sp.DecodeErr == nil && sp.Packet.Kind == artnet.KindDmx {
			n++
		}
	}
	return n
}

// --- tests -------------------------------------------------------------------

// TestProgrammerBaseDefaultsOnWireAfterArm: with nothing touched, arming
// puts every patched channel at its profile default — 16-bit split coarse,
// fine — and unprofiled channels at 0, flagged unknown.
func TestProgrammerBaseDefaultsOnWireAfterArm(t *testing.T) {
	r := newC4aRig(t)
	r.h.clock.Advance(100 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Fatalf("%d ArtDmx frames before Arm", n)
	}
	r.arm(t)
	u0 := r.wire(t, 0)
	for _, base := range []int{1, 42} {
		name := fmt.Sprintf("BMFL at %d", base)
		wantSlots(t, name+" Pan 32768/2", u0, base, 128, 0)
		wantSlots(t, name+" Tilt 32768/2", u0, base+2, 128, 0)
		wantSlots(t, name+" Graphic wheel 1 rotation 128", u0, base+19, 128, 128)
		wantSlots(t, name+" Gobo1Pos 32896/2", u0, base+23, 128, 128)
		wantSlots(t, name+" Prism1Pos 128", u0, base+29, 128)
		wantSlots(t, name+" Shutter1 32", u0, base+38, 32)
		wantSlots(t, name+" Dimmer 0/2", u0, base+39, 0, 0)
	}
	u1 := r.wire(t, 1)
	wantSlots(t, "LEDBeam Pan, Tilt", u1, 1, 128, 0, 128, 0)
	wantSlots(t, "LEDBeam R G B W 255", u1, 7, 255, 255, 255, 255)
	wantSlots(t, "LEDBeam CTC, Color1, Shutter 255, Dimmer 0", u1, 11, 0, 0, 255, 0)
	wantSlots(t, "unprofiled G1 at 0", u1, 201, 0, 0, 0, 0)

	r.selectNames(t, "G1", "L1")
	v := r.view(t)
	if len(v.Raw) != 1 || v.Raw[0].EntryID != r.ids["G1"] || len(v.Raw[0].Offsets) != 4 {
		t.Errorf("unprofiled G1 should offer exactly its 4 raw offsets: %+v", v.Raw)
	}
	if a := v.attr("Dimmer"); a == nil || a.UnknownDefault || len(a.Channels) != 1 || !a.Channels[0].DefaultKnown {
		t.Errorf("LEDBeam Dimmer default should be known: %+v", a)
	}
}

// TestProgrammerSetDimmerFractionOnTwoFixtures: fraction of the whole
// 16-bit channel, split coarse/fine, on both selected fixtures only.
func TestProgrammerSetDimmerFractionOnTwoFixtures(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "B2")
	res := r.set(t, map[string]any{"attribute": "Dimmer", "fraction": 0.75})
	if len(res.Applied) != 2 || res.Applied[0].Value != 49151 || !res.Output.Live {
		t.Fatalf("set Dimmer 0.75: %+v (0.75 x 65535 = 49151.25 -> 49151)", res)
	}
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 dimmer", u0, 40, 191, 255)
	wantSlots(t, "B2 dimmer", u0, 81, 191, 255)
	wantSlots(t, "B1 pan untouched (default)", u0, 1, 128, 0)
	u1 := r.wire(t, 1)
	wantSlots(t, "LEDBeam dimmer untouched", u1, 14, 0)
}

// TestProgrammerGoboByNameShakeAndColourSlot: a channel set by name lands on
// its DMXFrom (a single-state set), a function fraction lands inside that
// function's range, a wheel slot lands on the slot's set.
func TestProgrammerGoboByNameShakeAndColourSlot(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	res := r.set(t, map[string]any{"attribute": "Gobo1", "set": "Gobo 3"})
	if len(res.Applied) != 1 || res.Applied[0].Value != 14 || res.Applied[0].FunctionName != "Gobo1" {
		t.Fatalf("gobo by name: %+v", res)
	}
	wantSlots(t, "Gobo1 = Gobo 3 (DMXFrom 14)", r.wire(t, 0), 23, 14)

	res = r.set(t, map[string]any{"attribute": "Gobo1", "function": "Gobo1SelectShake", "fraction": 0.5})
	if len(res.Applied) != 1 || res.Applied[0].Value != 95 {
		t.Fatalf("gobo shake 0.5 of 60-129: %+v (60 + round(34.5) = 95)", res)
	}
	wantSlots(t, "Gobo1 shake", r.wire(t, 0), 23, 95)

	// Colour wheel slot 2 = "Deep red - Positioning", 4626-9508 at physical
	// -0.5..0.5: the slot centred is the midpoint 7067 (0x1B9B).
	res = r.set(t, map[string]any{"attribute": "Color1", "slot": 2})
	if len(res.Applied) != 1 || res.Applied[0].Value != 7067 {
		t.Fatalf("colour slot 2: %+v", res)
	}
	wantSlots(t, "Color1 slot 2", r.wire(t, 0), 7, 0x1B, 0x9B)

	// Gobo1Pos (index) depends on master function Head_Gobo1.Gobo1.Gobo1
	// (static gobos, 0-31): the shaking Gobo 3 (slot 4) moves to the static
	// Gobo 3 — the same wheel slot — and is reported.
	res = r.set(t, map[string]any{"attribute": "Gobo1Pos", "function": "Gobo1Pos", "physical": 0})
	if len(res.ModeMasters) != 1 || !res.ModeMasters[0].Changed || res.ModeMasters[0].MasterOffset != 23 ||
		res.ModeMasters[0].Value != 14 || res.ModeMasters[0].Rule != "master-function" {
		t.Fatalf("Gobo1Pos mode master: %+v", res.ModeMasters)
	}
	wantSlots(t, "Gobo1 back to static Gobo 3", r.wire(t, 0), 23, 14)
}

// TestProgrammerPhysicalPanDegrees: physical value by linear interpolation
// per fixture type; a value outside one type's range skips that fixture
// with a reason and never clamps.
func TestProgrammerPhysicalPanDegrees(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "L1")
	res := r.set(t, map[string]any{"attribute": "Pan", "physical": 90})
	if len(res.Applied) != 2 {
		t.Fatalf("pan 90: %+v", res)
	}
	// BMFL -270..270: (360/540) x 65535 = 43690; LEDBeam -225..225: 0.7 x 65535 = 45874.5 -> 45875.
	wantSlots(t, "BMFL pan 90 deg", r.wire(t, 0), 1, 170, 170)
	wantSlots(t, "LEDBeam pan 90 deg", r.wire(t, 1), 1, 179, 51)

	res = r.set(t, map[string]any{"attribute": "Pan", "physical": 250})
	if len(res.Applied) != 1 || res.Applied[0].EntryID != r.ids["B1"] || len(res.Skipped) != 1 || res.Skipped[0].EntryID != r.ids["L1"] ||
		!strings.Contains(res.Skipped[0].Reason, "outside") {
		t.Fatalf("pan 250: BMFL applies, LEDBeam (225 max) is skipped with a reason: %+v", res)
	}
	wantSlots(t, "LEDBeam pan unchanged, not clamped", r.wire(t, 1), 1, 179, 51)

	rr := r.do(t, "POST", "/api/programmer/set", map[string]any{"attribute": "Pan", "physical": 400}, nil)
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "Nothing was set") {
		t.Errorf("pan 400 on both: %d %s", rr.Code, rr.Body.String())
	}
}

// TestProgrammerLEDBeamRedNeedsColourModeMaster: Robin 100 LEDBeam's
// ColorAdd_R function needs Head_Color1 at 0 (ModeMaster, ModeFrom/To 0/1).
// With the virtual colour wheel on Red, setting red moves the master to 0
// and says so.
func TestProgrammerLEDBeamRedNeedsColourModeMaster(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "L1")
	if res := r.set(t, map[string]any{"attribute": "Color1", "slot": 13}); len(res.Applied) != 1 || res.Applied[0].Value != 175 {
		t.Fatalf("Color1 slot 13 (Red, 175): %+v", res)
	}
	wantSlots(t, "virtual wheel on Red", r.wire(t, 1), 12, 175)
	res := r.set(t, map[string]any{"attribute": "ColorAdd_R", "fraction": 1})
	if len(res.ModeMasters) != 1 {
		t.Fatalf("red: want one mode-master report, got %+v", res)
	}
	mm := res.ModeMasters[0]
	if !mm.Resolved || !mm.Changed || mm.MasterOffset != 12 || mm.MasterAttribute != "Color1" || mm.Rule != "channel-range" ||
		mm.RangeFrom != 0 || mm.RangeTo != 0 || mm.Previous == nil || *mm.Previous != 175 || mm.Value != 0 || mm.Note == "" {
		t.Fatalf("mode master report: %+v", mm)
	}
	u1 := r.wire(t, 1)
	wantSlots(t, "red full", u1, 7, 255)
	wantSlots(t, "Color1 moved to its ColorAdd range", u1, 12, 0)
}

// TestProgrammerCellAndParentSelection: the Paladin Cube's three RGBW cells
// (GDTF geometries Beam 1/2/3) are selectable on their own; the parent
// addresses every cell's instance of an attribute.
func TestProgrammerCellAndParentSelection(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	rr := r.do(t, "GET", "/api/programmer/fixtures", nil, nil)
	var fx struct {
		Fixtures []struct {
			EntryID string
			Cells   []struct {
				ID    string
				Name  string
				Index int
			}
		}
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &fx) != nil {
		t.Fatalf("fixtures: %d %s", rr.Code, rr.Body.String())
	}
	cells := map[string]int{}
	for _, f := range fx.Fixtures {
		cells[f.EntryID] = len(f.Cells)
		if f.EntryID == r.ids["P1"] && (len(f.Cells) != 3 || f.Cells[1].ID != "Beam 2:0" || f.Cells[1].Name != "Beam 2" || f.Cells[1].Index != 2) {
			t.Errorf("Paladin cells: %+v", f.Cells)
		}
	}
	if cells[r.ids["L1"]] != 0 || cells[r.ids["B1"]] != 0 {
		t.Errorf("a mover's Yoke/Head/Beam are not cells: %v", cells)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "targets": []any{map[string]any{"entryId": r.ids["P1"], "cell": "Beam 2:0"}}}, nil)
	r.set(t, map[string]any{"attribute": "ColorAdd_R", "dmx": 65535})
	u1 := r.wire(t, 1)
	wantSlots(t, "cell 1 red untouched", u1, 101, 0, 0)
	wantSlots(t, "cell 2 red full", u1, 109, 255, 255)
	wantSlots(t, "cell 3 red untouched", u1, 117, 0, 0)

	r.selectNames(t, "P1")
	r.set(t, map[string]any{"attribute": "ColorAdd_G", "fraction": 1})
	u1 = r.wire(t, 1)
	for _, at := range []int{103, 111, 119} {
		wantSlots(t, fmt.Sprintf("parent green on every cell (slot %d)", at), u1, at, 255, 255)
	}
	rr = r.do(t, "POST", "/api/programmer/select", map[string]any{"action": "set", "targets": []any{map[string]any{"entryId": r.ids["P1"], "cell": "Beam 9:0"}}}, nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown cell: %d %s", rr.Code, rr.Body.String())
	}
}

// TestProgrammerMixedFlag: two selected fixtures holding different values
// for one attribute read as mixed, with no single value.
func TestProgrammerMixedFlag(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1", "B2")
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Dimmer", "dmx": 1000})
	v := r.view(t)
	d := v.attr("Dimmer")
	if d == nil || !d.Mixed || d.Value != nil || !d.Touched || d.AllTouched || len(d.Channels) != 2 {
		t.Fatalf("Dimmer across B1 (1000) and B2 (default 0): %+v", d)
	}
	if p := v.attr("Pan"); p == nil || p.Mixed || p.Value == nil || *p.Value != 32768 || p.Touched {
		t.Fatalf("Pan at both defaults: %+v", p)
	}
	if v.Output.Live || v.Output.State != "disarmed" || v.Output.Note == "" {
		t.Errorf("disarmed programmer output: %+v", v.Output)
	}
}

// TestProgrammerLayersOverRigCheckAndClearsBack: a running Rig Check test
// shows on every channel the programmer has not touched; a touched channel
// wins; clearing the group hands it back to the test, and on a universe no
// test drives, back to the profile default. (C7: the test runs through the
// Console's Tests API on a stored group holding B1, where it ran through the
// retired /api/patch/rigcheck/pattern/start with an explicit B1 scope.)
func TestProgrammerLayersOverRigCheckAndClearsBack(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	var pv struct{ StoredGroups []struct{ ID string } }
	r.post(t, "/api/programmer/groups/store", map[string]any{"name": "B1 only"}, &pv)
	r.testsPost(t, "fade", map[string]any{"fadeMs": 0})
	r.testsPost(t, "set", map[string]any{"tests": []any{map[string]any{"kind": "dimmer_toggle", "on": true, "max": 200}},
		"scope": map[string]any{"kind": "group", "group": pv.StoredGroups[0].ID}})
	before := r.wire(t, 0)
	testDimmer := append([]byte(nil), slots(before, 40, 41)...)
	if testDimmer[0] == 0 {
		t.Fatalf("Rig Check dimmer test is not on the wire: %v", testDimmer)
	}
	r.selectNames(t, "B1", "L1")
	r.set(t, map[string]any{"targets": []any{r.target("B1")}, "attribute": "Dimmer", "dmx": 1000})
	r.set(t, map[string]any{"targets": []any{r.target("L1")}, "attribute": "Dimmer", "dmx": 100})
	r.set(t, map[string]any{"attribute": "ColorAdd_R", "fraction": 0})
	r.set(t, map[string]any{"attribute": "Pan", "physical": 0})
	during := r.wire(t, 0)
	wantSlots(t, "programmer dimmer wins over the test", during, 40, 3, 232)
	wantSlots(t, "untouched zoom shows the test's base state", during, 34, before[33], before[34])
	wantSlots(t, "untouched shutter shows the test", during, 39, before[38])
	wantSlots(t, "LEDBeam red at 0", r.wire(t, 1), 7, 0)

	var cleared struct{ Released int }
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "selection", "group": "dimmer"}, &cleared)
	if cleared.Released != 2 {
		t.Errorf("released %d dimmer channels, want 2 (B1, L1)", cleared.Released)
	}
	after := r.wire(t, 0)
	wantSlots(t, "dimmer back to the test", after, 40, testDimmer...)
	wantSlots(t, "pan still the programmer's (home, 32768)", after, 1, 128, 0)
	r.post(t, "/api/programmer/clear", map[string]any{"scope": "selection", "group": "colour"}, nil)
	wantSlots(t, "LEDBeam red back to its default 255", r.wire(t, 1), 7, 255)
	// C5 changed this deliberately (owner-approved Tests layer): Rig Check's
	// tests claim only the channels of the fixtures they test, no longer the
	// whole universe. B2 sits on universe 0 outside the test scope, so it
	// shows its profile default (Pan 32768 = 128,0) where C3/C4a forced the
	// test's 0. The tested B1's slots are unchanged (rigcheck_golden_test.go).
	wantSlots(t, "untested B2 pan at its default beside a running test", after, 42, 128, 0)
}

// TestProgrammerRawDMXOnUnprofiledFixture: raw 8-bit writes on offsets no
// profile covers; a profiled offset is refused (set it by attribute).
func TestProgrammerRawDMXOnUnprofiledFixture(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.post(t, "/api/programmer/raw", map[string]any{"entryId": r.ids["G1"], "writes": []any{map[string]any{"offset": 2, "value": 200}}}, nil)
	wantSlots(t, "G1 offset 2 raw", r.wire(t, 1), 201, 0, 200, 0, 0)
	rr := r.do(t, "POST", "/api/programmer/raw", map[string]any{"entryId": r.ids["B1"], "writes": []any{map[string]any{"offset": 40, "value": 9}}}, nil)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "by attribute") {
		t.Errorf("raw on a profiled channel: %d %s", rr.Code, rr.Body.String())
	}
	r.post(t, "/api/programmer/raw", map[string]any{"entryId": r.ids["G1"], "writes": []any{map[string]any{"offset": 2, "release": true}}}, nil)
	wantSlots(t, "G1 released to base 0", r.wire(t, 1), 202, 0)
}

// TestProgrammerSelectLayerAndGroup: a layout layer selects its placed
// fixtures in reading order (row, then column), never objects; the Unplaced
// layer includes its automatic placements; a saved group selects its
// members in the group's order.
func TestProgrammerSelectLayerAndGroup(t *testing.T) {
	r := newC4aRig(t)
	var lay struct {
		Layout struct {
			Layers []struct{ ID, Name string }
		}
	}
	r.post(t, "/api/patch/layout/layer-create", map[string]any{"name": "Truss"}, &lay)
	truss := ""
	for _, l := range lay.Layout.Layers {
		if l.Name == "Truss" {
			truss = l.ID
		}
	}
	r.post(t, "/api/patch/layout/place", map[string]any{"kind": "entry", "ref": r.ids["L1"], "layer": truss, "col": 3, "row": 0}, nil)
	r.post(t, "/api/patch/layout/place", map[string]any{"kind": "entry", "ref": r.ids["P1"], "layer": truss, "col": 1, "row": 0}, nil)
	r.post(t, "/api/patch/layout/place", map[string]any{"kind": "entry", "ref": r.ids["B2"], "layer": truss, "col": 0, "row": 2}, nil)
	r.post(t, "/api/patch/layout/object-create", map[string]any{"objectType": "label", "layer": truss, "text": "FOH", "geometry": map[string]any{"col": 0, "row": 0}}, nil)
	var v c4aView
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "layer": truss}, &v)
	got := make([]string, 0)
	for _, s := range v.Selection {
		got = append(got, s.Name)
	}
	if strings.Join(got, ",") != "P1,L1,B2" {
		t.Errorf("layer selection = %v, want P1,L1,B2 (row 0 by column, then row 2)", got)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "set", "layer": "unplaced"}, &v)
	if len(v.Selection) != 2 || v.Selection[0].Name != "B1" || v.Selection[1].Name != "G1" {
		t.Errorf("Unplaced layer selection = %+v, want B1, G1", v.Selection)
	}
	var g struct{ ID string }
	r.post(t, "/api/patch/workspace/save-group", map[string]any{"name": "Wash", "entryIds": []string{r.ids["L1"], r.ids["B1"]}}, &g)
	r.post(t, "/api/programmer/select", map[string]any{"action": "add", "group": g.ID}, &v)
	got = got[:0]
	for _, s := range v.Selection {
		got = append(got, s.Name)
	}
	if strings.Join(got, ",") != "B1,G1,L1" {
		t.Errorf("adding group Wash to B1,G1 = %v, want B1,G1,L1 (B1 kept in place)", got)
	}
	r.post(t, "/api/programmer/select", map[string]any{"action": "toggle", "targets": []any{r.target("G1")}}, &v)
	if len(v.Selection) != 2 || v.Selection[1].Name != "L1" {
		t.Errorf("toggle G1 off: %+v", v.Selection)
	}
}

// TestProgrammerTwoBrowsersShareOneRevision: one programmer for every
// browser; a write carrying a stale revision is refused with 409 and
// changes nothing.
func TestProgrammerTwoBrowsersShareOneRevision(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1")
	a := r.view(t)
	rev := strconv.FormatUint(a.Revision, 10)
	rr := r.do(t, "POST", "/api/programmer/set", map[string]any{"attribute": "Dimmer", "dmx": 65535}, map[string]string{"X-Benny-Programmer": rev})
	if rr.Code != 200 {
		t.Fatalf("browser A set: %d %s", rr.Code, rr.Body.String())
	}
	newRev := rr.Header().Get("X-Benny-Programmer")
	b := r.view(t)
	if strconv.FormatUint(b.Revision, 10) != newRev || b.Revision == a.Revision {
		t.Fatalf("browser B revision %d, A got %s after its write (was %d)", b.Revision, newRev, a.Revision)
	}
	if d := b.attr("Dimmer"); d == nil || d.Value == nil || *d.Value != 65535 {
		t.Fatalf("browser B does not see A's dimmer: %+v", d)
	}
	rr = r.do(t, "POST", "/api/programmer/set", map[string]any{"attribute": "Dimmer", "dmx": 0}, map[string]string{"X-Benny-Programmer": rev})
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d %s, want 409", rr.Code, rr.Body.String())
	}
	wantSlots(t, "stale write changed nothing", r.wire(t, 0), 40, 255, 255)
}

// TestProgrammerFollowsTheShow: a structural edit drops a deleted fixture
// from the selection and the programmer; a show switch clears everything,
// and nothing of the old show stays on the wire.
func TestProgrammerFollowsTheShow(t *testing.T) {
	r := newC4aRig(t)
	r.arm(t)
	r.selectNames(t, "B1", "B2")
	r.set(t, map[string]any{"attribute": "Dimmer", "dmx": 65535})
	if rr := r.do(t, "DELETE", "/api/patch/entries/"+r.ids["B2"], nil, nil); rr.Code != 200 {
		t.Fatalf("delete B2: %d %s", rr.Code, rr.Body.String())
	}
	v := r.view(t)
	if len(v.Selection) != 1 || v.Selection[0].Name != "B1" || v.Touched != 1 {
		t.Fatalf("after deleting B2: selection %+v, touched %d", v.Selection, v.Touched)
	}
	u0 := r.wire(t, 0)
	wantSlots(t, "B1 keeps its dimmer", u0, 40, 255, 255)
	wantSlots(t, "B2's channels hold nothing", u0, 81, 0, 0)

	r.post(t, "/api/patch/new", map[string]any{"name": "Next show"}, nil)
	v = r.view(t)
	if len(v.Selection) != 0 || v.Touched != 0 {
		t.Fatalf("after a show switch: selection %+v, touched %d", v.Selection, v.Touched)
	}
	r.h.tport.TakeSent()
	r.h.clock.Advance(200 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Errorf("%d ArtDmx frames of the old show after switching to an empty one", n)
	}
}

// TestProgrammerDisarmedAndHolding: disarmed, values are kept but nothing
// reaches the wire; holding, edits are kept and the response says they are
// not live.
func TestProgrammerDisarmedAndHolding(t *testing.T) {
	r := newC4aRig(t)
	r.selectNames(t, "B1")
	res := r.set(t, map[string]any{"attribute": "Dimmer", "dmx": 65535})
	if res.Output.Live || res.Output.State != "disarmed" || res.Output.Note == "" {
		t.Errorf("disarmed set response: %+v", res.Output)
	}
	r.h.clock.Advance(200 * time.Millisecond)
	if n := artDmxCount(r.h.tport.TakeSent()); n != 0 {
		t.Fatalf("%d ArtDmx frames while disarmed", n)
	}
	r.h.srv.DMX.SetLeaseLossAction(session.LeaseLossHold)
	r.arm(t)
	wantSlots(t, "armed: the stored dimmer", r.wire(t, 0), 40, 255, 255)
	r.h.clock.Advance(6 * time.Second)
	res = r.set(t, map[string]any{"attribute": "Dimmer", "dmx": 0})
	if res.Output.Live || res.Output.State != "holding" || res.Output.Note == "" {
		t.Errorf("holding set response: %+v", res.Output)
	}
	wantSlots(t, "holding keeps the last look", r.wire(t, 0), 40, 255, 255)
}
