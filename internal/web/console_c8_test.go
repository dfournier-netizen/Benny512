package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsoleMIDI_Encoders (Console-lite C8) runs
// static/js/testdata/console_midi_test.js: the real index.html in a small
// DOM and the LITERAL api.js, ws.js, programmer.js, ui.js,
// console-controls.js, console-midi.js and console.js against this real
// server over real HTTP and WebSocket, with real vendor GDTF profiles, and a
// fake navigator.requestMIDIAccess whose input delivers raw MIDI byte arrays.
// It checks every decode mode's exact programmer writes, learn, banks, the
// encoder push (fine), acceleration, the mixed-selection rule, hot-plug, the
// secure-context refusal and the "no Web MIDI" words. Here, afterwards: the
// mapping the JS saved is on disk with its .bak, and not one ArtDmx datagram
// left the disarmed engine.
func TestConsoleMIDI_Encoders(t *testing.T) {
	nodePath := nodeOrSkip(t)
	r := newC4aRig(t)
	store := filepath.Join(t.TempDir(), "benny512-midi.json")
	if err := r.h.srv.SetMIDIStorePath(store); err != nil {
		t.Fatal(err)
	}
	r.h.tport.TakeSent()
	idsJSON, _ := json.Marshal(r.ids)
	ts := httptest.NewServer(r.h.srv.Handler())
	defer ts.Close()
	jsDir, _ := filepath.Abs("static/js")
	out, err := exec.Command(nodePath, filepath.Join(jsDir, "testdata", "console_midi_test.js"), jsDir, ts.URL, string(idsJSON)).CombinedOutput()
	t.Logf("console_midi_test.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("console_midi_test.js failed: %v", err)
	}

	raw, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("the mapping the browser saved is not on disk: %v", err)
	}
	var f midiFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != 1 || f.Profile == nil {
		t.Fatalf("benny512-midi.json = %s (%v)", raw, err)
	}
	if f.Profile.DeviceName != "X-TOUCH MINI" || len(f.Profile.Encoders) != 8 || f.Profile.Encoders[0] == nil || f.Profile.Encoders[0].Mode != MIDIModeTwos ||
		f.Profile.Encoders[4] == nil || f.Profile.Encoders[4].Mode != MIDIModeAbsolute14 || f.Profile.Encoders[0].Push == nil || f.Profile.Encoders[0].Push.Number != 32 {
		t.Errorf("benny512-midi.json does not hold the learned mapping:\n%s", raw)
	}
	if _, err := os.Stat(store + ".bak"); err != nil {
		t.Errorf("no .bak after the browser saved the mapping more than once: %v", err)
	}
	if n := c3AllArtDmx(r.h.tport.TakeSent()); n != 0 {
		t.Errorf("%d ArtDmx datagrams reached the wire while disarmed; encoder moves must change the programmer only", n)
	}
}

// TestMIDIProfile_DurableThroughTheHandler: POST /api/midi through the real
// handler writes benny512-midi.json with the literal keys, keeps the previous
// file as .bak, survives a restart, refuses an ambiguous mapping without
// touching the file, and never destroys or promotes a damaged file.
func TestMIDIProfile_DurableThroughTheHandler(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benny512-midi.json")
	h := newHarness(t)
	defer h.srv.Close()
	if err := h.srv.SetMIDIStorePath(path); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/midi", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := doJSON(t, h.srv.Handler(), "GET", "/api/midi", nil); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"profile":null,"persisted":true,"loadError":""}` {
		t.Fatalf("fresh GET /api/midi = %d %s", rr.Code, rr.Body.String())
	}
	one := `{"profile":{"deviceName":"X-TOUCH MINI","encoders":[{"channel":1,"control":16,"mode":"twos","invert":false,"push":{"kind":"note","channel":1,"number":32}},null],"acceleration":{"enabled":false,"thresholdMs":100,"strength":2},"bankPrev":null,"bankNext":{"kind":"cc","channel":1,"number":50}}}`
	if rr := post(one); rr.Code != 200 {
		t.Fatalf("POST one: %d %s", rr.Code, rr.Body.String())
	}
	first, _ := os.ReadFile(path)
	for _, key := range []string{`"version": 1`, `"deviceName": "X-TOUCH MINI"`, `"encoders"`, `"channel": 1`, `"control": 16`, `"mode": "twos"`, `"invert": false`, `"push"`, `"kind": "note"`, `"number": 32`, `"acceleration"`, `"thresholdMs": 100`, `"strength": 2`, `"enabled": false`, `"bankPrev": null`, `"bankNext"`} {
		if !strings.Contains(string(first), key) {
			t.Errorf("benny512-midi.json lacks %s:\n%s", key, first)
		}
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a first save made a .bak (%v); there was nothing to keep", err)
	}
	two := strings.Replace(one, `"mode":"twos"`, `"mode":"offset"`, 1)
	if rr := post(two); rr.Code != 200 {
		t.Fatalf("POST two: %d %s", rr.Code, rr.Body.String())
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != string(first) {
		t.Errorf(".bak is not the previous file:\n%s\nwant\n%s", bak, first)
	}
	second, _ := os.ReadFile(path)

	// Ambiguous: encoder 2 on the same controller as encoder 1.
	bad := strings.Replace(two, `null],`, `{"channel":1,"control":16,"mode":"absolute","invert":false,"push":null}],`, 1)
	if rr := post(bad); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Encoder 1 and Encoder 2 answer to the same MIDI message") {
		t.Errorf("a clashing mapping: %d %s", rr.Code, rr.Body.String())
	}
	for _, c := range []struct{ name, body, want string }{
		{"14-bit above 31", strings.Replace(two, `"control":16,"mode":"offset"`, `"control":40,"mode":"absolute14"`, 1), "14-bit pair uses controllers 0 to 31"},
		{"unknown mode", strings.Replace(two, `"mode":"offset"`, `"mode":"relative"`, 1), "is not an encoder mode"},
		{"push = bank button", strings.Replace(two, `"push":{"kind":"note","channel":1,"number":32}`, `"push":{"kind":"cc","channel":1,"number":50}`, 1), "answer to the same MIDI message"},
		{"channel 0", strings.Replace(two, `"channel":1,"control":16`, `"channel":0,"control":16`, 1), "MIDI channel 0 is not 1 to 16"},
		{"no encoders", `{"profile":{"deviceName":"","encoders":[],"acceleration":{"enabled":false,"thresholdMs":100,"strength":2},"bankPrev":null,"bankNext":null}}`, "1 to 16 encoders"},
		{"strength 9", strings.Replace(two, `"strength":2`, `"strength":9`, 1), "strength 9 is not 1 to 5"},
		{"unknown field", strings.Replace(two, `"invert":false`, `"invert":false,"speed":3`, 1), "could not be read"},
	} {
		if rr := post(c.body); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), c.want) {
			t.Errorf("%s: %d %s (want 400 with %q)", c.name, rr.Code, rr.Body.String(), c.want)
		}
	}
	if now, _ := os.ReadFile(path); string(now) != string(second) {
		t.Errorf("a refused POST changed the file")
	}

	// A restart reads the second mapping back.
	h2 := newHarness(t)
	defer h2.srv.Close()
	if err := h2.srv.SetMIDIStorePath(path); err != nil {
		t.Fatal(err)
	}
	var got midiResponse
	rr := doJSON(t, h2.srv.Handler(), "GET", "/api/midi", nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Profile == nil || got.Profile.Encoders[0].Mode != MIDIModeOffset || got.Profile.Encoders[1] != nil || got.Profile.BankNext == nil || !got.Persisted {
		t.Fatalf("after a restart GET /api/midi = %s", rr.Body.String())
	}

	// A damaged file: reported, left untouched, and not promoted to .bak by
	// the next save.
	if err := os.WriteFile(path, []byte(`{"version":1,"profile":{"encoders":`), 0o644); err != nil {
		t.Fatal(err)
	}
	h3 := newHarness(t)
	defer h3.srv.Close()
	if err := h3.srv.SetMIDIStorePath(path); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged file loaded without a word: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"version":1,"profile":{"encoders":` {
		t.Errorf("loading a damaged file changed it: %s", b)
	}
	rr = doJSON(t, h3.srv.Handler(), "GET", "/api/midi", nil)
	if !strings.Contains(rr.Body.String(), `"profile":null`) || !strings.Contains(rr.Body.String(), "damaged") {
		t.Errorf("GET with a damaged file = %s", rr.Body.String())
	}
	req := httptest.NewRequest("POST", "/api/midi", strings.NewReader(one))
	rec := httptest.NewRecorder()
	h3.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST over a damaged file: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != string(first) {
		t.Errorf("the damaged file was promoted over the good .bak: %s", b)
	}

	// --demo / tests: no path, nothing written, persisted false.
	h4 := newHarness(t)
	defer h4.srv.Close()
	rr = doJSON(t, h4.srv.Handler(), "GET", "/api/midi", nil)
	if strings.TrimSpace(rr.Body.String()) != `{"profile":null,"persisted":false,"loadError":""}` {
		t.Errorf("in-memory GET /api/midi = %s", rr.Body.String())
	}
}
