package web

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Console-lite C2: fixture world positions from MVR <Matrix> (MVR 1.6, DIN
// SPEC 15801, "Node Definition: Matrix", Table 35). Every test here runs the
// LITERAL browser parser (mvrparse.js + mvrimport.js under node, via
// static/js/testdata/mvr_location_dump.js), posts its stdout bytes unmodified
// to the real POST /api/patch/import handler, and reads the result back
// through GET /api/patch as JSON — so the assertions never touch a Go type
// the parser's output was built from.

type dumpedImport struct {
	Entries  []json.RawMessage `json:"entries"`
	Warnings []string          `json:"warnings"`
}

func runMVRDump(t *testing.T, xmlPath, mode string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH")
	}
	abs, err := filepath.Abs(xmlPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "static/js/testdata/mvr_location_dump.js", abs, mode)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mvr dump: %v\n%s", err, stderr.String())
	}
	return out.Bytes()
}

func doBytes(t *testing.T, handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// importDump posts the parser's entries to /api/patch/import as-is (only the
// envelope is added) and returns GET /api/patch's entries as generic maps.
func importDump(t *testing.T, h *testHarness, dump []byte, mode string) []map[string]any {
	t.Helper()
	var d dumpedImport
	if err := json.Unmarshal(dump, &d); err != nil {
		t.Fatalf("dump is not JSON: %v\n%s", err, dump)
	}
	body := []byte(`{"mode":"` + mode + `","entries":[`)
	for i, e := range d.Entries {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, e...)
	}
	body = append(body, "]}"...)
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/import", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("import: status=%d body=%s", rr.Code, rr.Body.String())
	}
	return getPatchEntries(t, h)
}

func getPatchEntries(t *testing.T, h *testHarness) []map[string]any {
	t.Helper()
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	var resp struct {
		Patch struct {
			SchemaVersion int              `json:"schemaVersion"`
			Entries       []map[string]any `json:"entries"`
		} `json:"patch"`
	}
	mustUnmarshal(t, rr, &resp)
	return resp.Patch.Entries
}

func entryByNumber(t *testing.T, entries []map[string]any, fixtureNumber string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["fixtureNumber"] == fixtureNumber {
			return e
		}
	}
	t.Fatalf("no entry with fixtureNumber %q", fixtureNumber)
	return nil
}

type wantLoc struct {
	x, y, z          float64
	rotKnown         bool
	rotX, rotY, rotZ float64
}

func checkLocation(t *testing.T, label string, e map[string]any, want *wantLoc) {
	t.Helper()
	raw, ok := e["location"]
	if !ok {
		t.Errorf("%s: GET /api/patch carries no \"location\" key at all", label)
		return
	}
	loc, ok := raw.(map[string]any)
	if !ok {
		t.Errorf("%s: location is %T, want an object", label, raw)
		return
	}
	for _, k := range []string{"known", "x", "y", "z", "rotationKnown", "rotX", "rotY", "rotZ"} {
		if _, ok := loc[k]; !ok {
			t.Errorf("%s: location has no %q key (no omitempty on meaningful zeros): %v", label, k, loc)
		}
	}
	if want == nil {
		if loc["known"] != false || loc["rotationKnown"] != false {
			t.Errorf("%s: location = %v, want known:false and rotationKnown:false", label, loc)
		}
		for _, k := range []string{"x", "y", "z", "rotX", "rotY", "rotZ"} {
			if loc[k] != 0.0 {
				t.Errorf("%s: unknown location carries %s=%v; an unknown location must not hold a number", label, k, loc[k])
			}
		}
		return
	}
	if loc["known"] != true {
		t.Errorf("%s: location.known = %v, want true (%v)", label, loc["known"], loc)
		return
	}
	near := func(k string, got any, want, tol float64) {
		f, _ := got.(float64)
		if math.Abs(f-want) > tol {
			t.Errorf("%s: %s = %v, want %v (±%g)", label, k, got, want, tol)
		}
	}
	near("x", loc["x"], want.x, 1e-6)
	near("y", loc["y"], want.y, 1e-6)
	near("z", loc["z"], want.z, 1e-6)
	if loc["rotationKnown"] != want.rotKnown {
		t.Errorf("%s: rotationKnown = %v, want %v", label, loc["rotationKnown"], want.rotKnown)
		return
	}
	if want.rotKnown {
		near("rotX", loc["rotX"], want.rotX, 1e-4)
		near("rotY", loc["rotY"], want.rotY, 1e-4)
		near("rotZ", loc["rotZ"], want.rotZ, 1e-4)
	}
}

const deg = 180 / math.Pi

// TestMVRLocation_RealCaptureExtract: a real Capture 2023.1.6 export (trimmed
// extract, source and licence in the file's header comment). Every ancestor of
// every fixture in this file — <Layer> and <GroupObject> — carries no <Matrix>,
// which MVR Table 35 defines as identity ({1,0,0}{0,1,0}{0,0,1}{0,0,0}), so
// each fixture's world position is its own Matrix's o-row verbatim (the
// composition is identity ∘ … ∘ M = M). The rotations are hand-derived from
// the definition of an elementary rotation, NOT from the implementation's
// general extractor. Row reading per the reference implementation
// (libMVRgdtf VWTransformMatrix::PointTransform: p' = x·u + y·v + z·w + o, so
// u, v, w are the object's local X, Y, Z axes in parent coordinates). Euler
// convention under test: R = Rz(rotZ)·Ry(rotY)·Rx(rotX), degrees.
func TestMVRLocation_RealCaptureExtract(t *testing.T) {
	xml := filepath.Join("static", "js", "testdata", "capture_demo_show_real_extract.xml")
	want := map[string]*wantLoc{
		// Alpha Spot 11: u ≈ (0,0,1), v ≈ (0,1,0), w ≈ (−1,0,0) — the local X
		// axis points straight up: Ry(−90°) (Ry(β) maps X to (cosβ,0,−sinβ)).
		// rotY = −90 is the gimbal-lock case; the convention fixes rotX = 0 there.
		"11": {x: -6670.87305, y: -890.867859, z: 5565.0166, rotKnown: true, rotX: 0, rotY: -90, rotZ: 0},
		"12": {x: -6670.87305, y: -890.867859, z: 4565.0166, rotKnown: true, rotX: 0, rotY: -90, rotZ: 0},
		// Alpha Spot 16: u ≈ (0,0,−1), v ≈ (0,1,0), w ≈ (1,0,0): Ry(+90°).
		"16": {x: 6720.64404, y: -890.868164, z: 5565.0166, rotKnown: true, rotX: 0, rotY: 90, rotZ: 0},
		// ALC4 21: u = (1,0,0) and v = (0, cosα, sinα) — a pure rotation about X:
		// α = atan2(0.861084878, −0.508461356) ≈ 120.558°.
		"21": {x: -3373.78198, y: 90.8989639, z: 1053.27075, rotKnown: true, rotX: math.Atan2(0.861084878, -0.508461356) * deg, rotY: 0, rotZ: 0},
		"22": {x: -2373.78198, y: 90.8989639, z: 1053.27075, rotKnown: true, rotX: math.Atan2(0.861084878, -0.508461356) * deg, rotY: 0, rotZ: 0},
		// A.leda 1 (inside a GroupObject without a Matrix): w = (0,0,1) and
		// u = (cosγ, sinγ, 0) — a pure rotation about Z: γ = atan2(−0.938546598, 0.345152915) ≈ −69.808°.
		"1": {x: -3531.79517, y: 1361.95911, z: 5856.77441, rotKnown: true, rotX: 0, rotY: 0, rotZ: math.Atan2(-0.938546598, 0.345152915) * deg},
		"2": {x: -2762.11108, y: 1865.55334, z: 5856.77441, rotKnown: true, rotX: 0, rotY: 0, rotZ: math.Atan2(-0.962733269, 0.270453185) * deg},
		// MMX Spot 47: u = (cosγ, sinγ, ~0) with γ = atan2(−0.707107186, −0.707106948)
		// ≈ −135°; v.z = sinα, w.z = cosα with α = atan2(0.258819103, 0.965925872) = 15°.
		// Check: Rz(γ)Rx(α)·Ŷ = (−sinγ·cosα, cosγ·cosα, sinα) = (0.683, −0.683, 0.2588) = v. ✓
		"47": {x: 6611.44922, y: -3391.25732, z: 7812.87549, rotKnown: true, rotX: math.Atan2(0.258819103, 0.965925872) * deg, rotY: 0, rotZ: math.Atan2(-0.707107186, -0.707106948) * deg},
		"71": {x: 6377.80078, y: -2818.1936, z: 7660.03125, rotKnown: true, rotX: math.Atan2(0.258819103, 0.965925872) * deg, rotY: 0, rotZ: math.Atan2(-0.707107186, -0.707106948) * deg},
	}
	for _, mode := range []string{"resolved", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			entries := importDump(t, h, runMVRDump(t, xml, mode), "fresh")
			if len(entries) != len(want) {
				t.Fatalf("imported %d entries, want %d", len(entries), len(want))
			}
			for num, w := range want {
				checkLocation(t, mode+" fixture "+num, entryByNumber(t, entries, num), w)
			}
		})
	}
}

// nestedSceneXML composes Layer -> GroupObject -> Truss -> Fixture transforms.
// The second fixture's Matrix is MVR 1.6's own Fixture example matrix
// ("Node Definition: Fixture", example). Hand arithmetic, row reading
// p' = x·u + y·v + z·w + o (libMVRgdtf VWTransformMatrix::PointTransform):
//
//	Layer "Rig":    o = (0,0,1000), identity rotation (MVR Table 17: a
//	                Layer Matrix may only carry elevation).
//	GroupObject:    u = (0,1,0), v = (−1,0,0), w = (0,0,1) — 90° about Z —
//	                o = (1000,2000,0).
//	Truss:          identity rotation, o = (100,0,500).
//	Fixture "N1":   identity rotation, o = (10,20,30), inside the Truss.
//	  in group frame: (10,20,30) + (100,0,500)                 = (110,20,530)
//	  in layer frame: 110·(0,1,0) + 20·(−1,0,0) + 530·(0,0,1)
//	                  + (1000,2000,0)                          = (980,2110,530)
//	  in world:       + (0,0,1000)                             = (980,2110,1530)
//	  rotation: group's 90° about Z composed with identities   → rotZ = 90.
//	Fixture "N2":   the spec example, directly inside the GroupObject:
//	  o = (6020.9392, 2838.588955, 4978.134459)
//	  in layer frame: 6020.9392·(0,1,0) + 2838.588955·(−1,0,0)
//	                  + 4978.134459·(0,0,1) + (1000,2000,0)
//	                = (−1838.588955, 8020.9392, 4978.134459)
//	  in world:       z + 1000 = 5978.134459
//	  rotation: world u = 0.158127·(0,1,0) + (−0.987419)·(−1,0,0)
//	                  = (0.987419, 0.158127, 0), w = (0,0,1)
//	            → pure Z rotation, rotZ = atan2(0.158127, 0.987419) ≈ 9.099°.
//	Fixture "N3":   no <Matrix> of its own → location unknown (see
//	                mvrparse.js: Table 35's identity default is NOT used
//	                for the fixture itself).
//	Fixture "N4":   malformed Matrix (three rows) → unknown, never guessed.
//	Fixture "N5":   inside a GroupObject whose Matrix is malformed → unknown.
//	Fixture "N6":   o = (7,8,9) directly in the layer, so world (7,8,1009);
//	                its basis is mirrored (u = −X, det < 0) → position
//	                known, rotation unknown.
const nestedSceneXML = `<?xml version="1.0" encoding="UTF-8"?>
<GeneralSceneDescription verMajor="1" verMinor="6"><Scene><Layers>
  <Layer name="Rig" uuid="11111111-0000-0000-0000-000000000001">
    <Matrix>{1,0,0}{0,1,0}{0,0,1}{0,0,1000}</Matrix>
    <ChildList>
      <GroupObject name="Group" uuid="11111111-0000-0000-0000-000000000002">
        <Matrix>{0,1,0}{-1,0,0}{0,0,1}{1000,2000,0}</Matrix>
        <ChildList>
          <Truss name="Pipe" uuid="11111111-0000-0000-0000-000000000003">
            <Matrix>{1,0,0}{0,1,0}{0,0,1}{100,0,500}</Matrix>
            <Geometries/>
            <ChildList>
              <Fixture name="N1" uuid="11111111-0000-0000-0000-000000000004">
                <Matrix>{1,0,0}{0,1,0}{0,0,1}{10,20,30}</Matrix>
                <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>101</FixtureID>
                <Addresses><Address break="1">1</Address></Addresses>
              </Fixture>
            </ChildList>
          </Truss>
          <Fixture name="N2" uuid="11111111-0000-0000-0000-000000000005">
            <Matrix>{0.158127,-0.987419,0.000000}{0.987419,0.158127,0.000000}{0.000000,0.000000,1.000000}{6020.939200,2838.588955,4978.134459}</Matrix>
            <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>102</FixtureID>
            <Addresses><Address break="1">11</Address></Addresses>
          </Fixture>
        </ChildList>
      </GroupObject>
      <Fixture name="N3" uuid="11111111-0000-0000-0000-000000000006">
        <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>103</FixtureID>
        <Addresses><Address break="1">21</Address></Addresses>
      </Fixture>
      <Fixture name="N4" uuid="11111111-0000-0000-0000-000000000007">
        <Matrix>{1,0,0}{0,1,0}{5,5,5}</Matrix>
        <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>104</FixtureID>
        <Addresses><Address break="1">31</Address></Addresses>
      </Fixture>
      <GroupObject name="Broken" uuid="11111111-0000-0000-0000-000000000008">
        <Matrix>{1,0,0}{0,1,0}{0,0,1}{0,zero,0}</Matrix>
        <ChildList>
          <Fixture name="N5" uuid="11111111-0000-0000-0000-000000000009">
            <Matrix>{1,0,0}{0,1,0}{0,0,1}{1,2,3}</Matrix>
            <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>105</FixtureID>
            <Addresses><Address break="1">41</Address></Addresses>
          </Fixture>
        </ChildList>
      </GroupObject>
      <Fixture name="N6" uuid="11111111-0000-0000-0000-00000000000a">
        <Matrix>{-1,0,0}{0,1,0}{0,0,1}{7,8,9}</Matrix>
        <GDTFSpec>A.gdtf</GDTFSpec><GDTFMode>M</GDTFMode><FixtureID>106</FixtureID>
        <Addresses><Address break="1">51</Address></Addresses>
      </Fixture>
    </ChildList>
  </Layer>
</Layers></Scene></GeneralSceneDescription>`

func TestMVRLocation_NestedTransforms(t *testing.T) {
	xml := filepath.Join(t.TempDir(), "GeneralSceneDescription.xml")
	if err := os.WriteFile(xml, []byte(nestedSceneXML), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := runMVRDump(t, xml, "resolved")
	h := newHarness(t)
	entries := importDump(t, h, dump, "fresh")
	checkLocation(t, "N1 (layer+group+truss)", entryByNumber(t, entries, "101"), &wantLoc{x: 980, y: 2110, z: 1530, rotKnown: true, rotZ: 90})
	checkLocation(t, "N2 (spec example in rotated group)", entryByNumber(t, entries, "102"),
		&wantLoc{x: -1838.588955, y: 8020.9392, z: 5978.134459, rotKnown: true, rotZ: math.Atan2(0.158127, 0.987419) * deg})
	checkLocation(t, "N3 (no Matrix)", entryByNumber(t, entries, "103"), nil)
	checkLocation(t, "N4 (malformed Matrix)", entryByNumber(t, entries, "104"), nil)
	checkLocation(t, "N5 (malformed ancestor Matrix)", entryByNumber(t, entries, "105"), nil)
	checkLocation(t, "N6 (mirrored basis)", entryByNumber(t, entries, "106"), &wantLoc{x: 7, y: 8, z: 1009, rotKnown: false})

	var d dumpedImport
	_ = json.Unmarshal(dump, &d)
	joined := strings.Join(d.Warnings, "\n")
	for _, name := range []string{`"N4"`, `"N5"`, `"N6"`} {
		if !strings.Contains(joined, name) {
			t.Errorf("no import warning names fixture %s; a location the file could not supply must be said, not silently dropped:\n%s", name, joined)
		}
	}
}

// TestPatchLocation_EntryRequestValidation: the wire contract refuses a
// location whose flags and numbers disagree, exactly as it refuses wheels
// sent with wheelsKnown false.
func TestPatchLocation_EntryRequestValidation(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{
		`{"name":"A","startAddress":1,"location":{"known":false,"x":5,"y":0,"z":0,"rotationKnown":false,"rotX":0,"rotY":0,"rotZ":0}}`,
		`{"name":"A","startAddress":1,"location":{"known":false,"x":0,"y":0,"z":0,"rotationKnown":true,"rotX":0,"rotY":0,"rotZ":0}}`,
		`{"name":"A","startAddress":1,"location":{"known":true,"x":0,"y":0,"z":0,"rotationKnown":false,"rotX":12,"rotY":0,"rotZ":0}}`,
		`{"name":"A","startAddress":1,"location":{"known":true,"x":0,"y":0,"z":0,"rotationKnown":true,"rotX":0,"rotY":0,"rotZ":0,"bogus":1}}`,
	} {
		rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(body))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("POST %s: status %d, want 400 (%s)", body, rr.Code, rr.Body.String())
		}
	}
	// A plain field edit (the Patch screen's form sends no location) keeps
	// the stored location, the same rule channelFunctions/wheels follow.
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"A","startAddress":1,"location":{"known":true,"x":1,"y":2,"z":3,"rotationKnown":false,"rotX":0,"rotY":0,"rotZ":0}}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("create with location: %d %s", rr.Code, rr.Body.String())
	}
	id := getPatchEntries(t, h)[0]["id"].(string)
	rr = doBytes(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id, []byte(`{"name":"A renamed","startAddress":1}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	checkLocation(t, "after rename", getPatchEntries(t, h)[0], &wantLoc{x: 1, y: 2, z: 3})
}

// TestMVRLocation_ReimportNeverSilentlyOverwrites: a merge import fills a
// location the show does not know yet but never replaces one it does; the
// explicit /api/patch/locations action (selected entries + confirm, the
// library re-profile pattern) is the only way to change a known location, it
// touches nothing but the location, and it reports old -> new per entry.
func TestMVRLocation_ReimportNeverSilentlyOverwrites(t *testing.T) {
	xml := filepath.Join("static", "js", "testdata", "capture_demo_show_real_extract.xml")
	dump := runMVRDump(t, xml, "resolved")
	h := newHarness(t)
	// The show was patched by hand before the MVR arrived: same address as
	// Alpha Spot 11 (universe 1, start 1), no location.
	rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/entries", []byte(`{"name":"Hand 11","fixtureNumber":"11","fixtureType":"Hand","footprint":8,"universe":1,"startAddress":1}`))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	entries := importDump(t, h, dump, "merge")
	checkLocation(t, "merge fills an unknown location", entryByNumber(t, entries, "11"),
		&wantLoc{x: -6670.87305, y: -890.867859, z: 5565.0166, rotKnown: true, rotY: -90})

	// Move fixture 11 in the file; a second merge must keep the stored one.
	var d dumpedImport
	_ = json.Unmarshal(dump, &d)
	var first map[string]any
	_ = json.Unmarshal(d.Entries[0], &first)
	if first["fixtureNumber"] != "11" {
		t.Fatalf("dump order changed: %v", first["fixtureNumber"])
	}
	loc, _ := first["location"].(map[string]any)
	if loc == nil {
		t.Fatalf("mvrimport.js emitted no location for fixture 11: %s", d.Entries[0])
	}
	loc["x"] = 1234.5
	b, _ := json.Marshal(first)
	d.Entries[0] = b
	movedDump, _ := json.Marshal(d)
	entries = importDump(t, h, movedDump, "merge")
	checkLocation(t, "second merge keeps the stored location", entryByNumber(t, entries, "11"),
		&wantLoc{x: -6670.87305, y: -890.867859, z: 5565.0166, rotKnown: true, rotY: -90})
	before := entryByNumber(t, entries, "11")
	id := before["id"].(string)

	sources, _ := json.Marshal(d.Entries)
	post := func(extra string) *httptest.ResponseRecorder {
		return doBytes(t, h.srv.Handler(), "POST", "/api/patch/locations", []byte(`{"entryIds":["`+id+`"],"sources":`+string(sources)+extra+`}`))
	}
	if rr := post(`,"preview":false,"confirm":""`); rr.Code != http.StatusBadRequest {
		t.Fatalf("apply without confirm: %d %s, want 400", rr.Code, rr.Body.String())
	}
	rr = post(`,"preview":true,"confirm":""`)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rr.Code, rr.Body.String())
	}
	var rep struct {
		Applied        bool `json:"applied"`
		EntriesChanged int  `json:"entriesChanged"`
		Entries        []struct {
			EntryID string         `json:"entryId"`
			Matched bool           `json:"matched"`
			Changed bool           `json:"changed"`
			Old     map[string]any `json:"old"`
			New     map[string]any `json:"new"`
		} `json:"entries"`
	}
	mustUnmarshal(t, rr, &rep)
	if rep.Applied || rep.EntriesChanged != 1 || len(rep.Entries) != 1 || !rep.Entries[0].Matched || !rep.Entries[0].Changed ||
		rep.Entries[0].Old["x"] != -6670.87305 || rep.Entries[0].New["x"] != 1234.5 {
		t.Fatalf("preview report = %s", rr.Body.String())
	}
	checkLocation(t, "preview changes nothing", entryByNumber(t, getPatchEntries(t, h), "11"),
		&wantLoc{x: -6670.87305, y: -890.867859, z: 5565.0166, rotKnown: true, rotY: -90})

	rr = post(`,"preview":false,"confirm":"UPDATE LOCATIONS"`)
	if rr.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body.String())
	}
	after := entryByNumber(t, getPatchEntries(t, h), "11")
	checkLocation(t, "explicit update", after, &wantLoc{x: 1234.5, y: -890.867859, z: 5565.0166, rotKnown: true, rotY: -90})
	delete(before, "location")
	delete(after, "location")
	bb, _ := json.Marshal(before)
	ab, _ := json.Marshal(after)
	if !bytes.Equal(bb, ab) {
		t.Errorf("location update touched more than the location:\nbefore %s\nafter  %s", bb, ab)
	}

	// Two file fixtures on one address cannot say which is ours: refused.
	dup := append([]json.RawMessage{}, d.Entries...)
	dup = append(dup, d.Entries[0])
	dupSources, _ := json.Marshal(dup)
	rr = doBytes(t, h.srv.Handler(), "POST", "/api/patch/locations", []byte(`{"entryIds":["`+id+`"],"sources":`+string(dupSources)+`,"preview":true,"confirm":""}`))
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusConflict {
		t.Errorf("ambiguous source: %d %s, want a refusal", rr.Code, rr.Body.String())
	}
	if rr := doBytes(t, h.srv.Handler(), "POST", "/api/patch/locations", []byte(`{"entryIds":["nope"],"sources":[],"preview":true,"confirm":""}`)); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown entry id: %d %s, want 400", rr.Code, rr.Body.String())
	}
}
