// midi.go — the MIDI ENCODER mapping (Console-lite C8), persisted per
// installation.
//
//	GET  /api/midi   -> {profile: MIDIProfile|null, persisted, loadError}
//	POST /api/midi   {profile: MIDIProfile}  -> the same shape as GET
//
// The browser does everything MIDI (Web MIDI, static/js/console-midi.js):
// the server never sees a MIDI byte. What it keeps is the mapping — which
// channel/controller each encoder is, how its bytes are read, its push
// button, acceleration, the bank buttons — so a station keeps its
// controller set up across launches. One profile is enough for now (owner
// brief): it remembers the device name it was learned on, and the browser
// picks that input automatically when it is plugged in.
//
// The file is benny512-midi.json beside the executable, written with the
// settings file's discipline (settingsdurable.go): marshal first, keep the
// previous file as path+".bak" ONLY when it still parses, land the new bytes
// through a synced temporary file and a rename. A damaged file is never
// destroyed by a read: the server comes up with no profile, says why, and
// leaves the bytes on disk. A failed save is answered 500 and the profile in
// memory is NOT changed, so the screen never says "saved" over a disk that
// refused it.
//
// Real mode only (cmd/benny512): --demo and the offline-rehearsal child keep
// the mapping in memory, for the same reason the Settings file is real-mode
// only — a QA session must not overwrite the station's controller set-up.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// MIDI encoder input modes. The names are the wire values.
const (
	// MIDIModeTwos: relative, two's complement. 1..63 = up 1..63,
	// 127..65 = down 1..63 (128 - value), 64 = down 64, 0 = no move.
	MIDIModeTwos = "twos"
	// MIDIModeOffset: relative, binary offset around 64. value - 64.
	MIDIModeOffset = "offset"
	// MIDIModeSignBit: relative, signed bit. Bit 6 set = down, bits 0-5 =
	// how many steps.
	MIDIModeSignBit = "signbit"
	// MIDIModeAbsolute: an absolute 0..127 controller turned into steps by
	// the difference from the previous value (see console-midi.js).
	MIDIModeAbsolute = "absolute"
	// MIDIModeAbsolute14: a 14-bit absolute pair, MSB on controller n
	// (0..31) and LSB on n+32 (MIDI 1.0 control-change pairs).
	MIDIModeAbsolute14 = "absolute14"
)

var midiModes = map[string]bool{MIDIModeTwos: true, MIDIModeOffset: true, MIDIModeSignBit: true, MIDIModeAbsolute: true, MIDIModeAbsolute14: true}

// MIDIButton is a button on the controller: a note (Note On, velocity > 0)
// or a control change with a value above 0.
type MIDIButton struct {
	Kind    string `json:"kind"`    // "note" | "cc"
	Channel int    `json:"channel"` // 1..16, as printed on controllers
	Number  int    `json:"number"`  // note or controller number, 0..127
}

// MIDIEncoder is one encoder's binding.
type MIDIEncoder struct {
	Channel int    `json:"channel"` // 1..16
	Control int    `json:"control"` // controller number 0..127 (the MSB for absolute14)
	Mode    string `json:"mode"`
	// Invert flips the direction (an encoder that counts the other way).
	Invert bool `json:"invert"`
	// Push, when bound, toggles FINE for this encoder.
	Push *MIDIButton `json:"push"`
}

// MIDIAccel is the optional acceleration: off by default. Turning faster
// than one step per ThresholdMs multiplies each step, up to the factor
// Strength (1..5) names in console-midi.js.
type MIDIAccel struct {
	Enabled     bool `json:"enabled"`
	ThresholdMs int  `json:"thresholdMs"`
	Strength    int  `json:"strength"`
}

// MIDIProfile is the whole mapping. Encoders has one slot per on-screen
// encoder (1..16); a null slot is an encoder with no MIDI binding yet.
type MIDIProfile struct {
	DeviceName   string         `json:"deviceName"`
	Encoders     []*MIDIEncoder `json:"encoders"`
	Acceleration MIDIAccel      `json:"acceleration"`
	BankPrev     *MIDIButton    `json:"bankPrev"`
	BankNext     *MIDIButton    `json:"bankNext"`
}

const midiMaxEncoders = 16

type midiProfileRequestError struct{ msg string }

func (e midiProfileRequestError) Error() string { return e.msg }

func midiBad(format string, a ...any) error {
	return midiProfileRequestError{msg: fmt.Sprintf(format, a...)}
}

func (b *MIDIButton) validate(what string) error {
	if b == nil {
		return nil
	}
	if b.Kind != "note" && b.Kind != "cc" {
		return midiBad("%s: a button is a note or a cc, not %q.", what, b.Kind)
	}
	if b.Channel < 1 || b.Channel > 16 {
		return midiBad("%s: MIDI channel %d is not 1 to 16.", what, b.Channel)
	}
	if b.Number < 0 || b.Number > 127 {
		return midiBad("%s: number %d is not 0 to 127.", what, b.Number)
	}
	return nil
}

// validate refuses a profile the browser could not act on unambiguously:
// unknown modes, out-of-range numbers, and two bindings answering to the
// same message (one turn would then move two attributes, or a push would
// also be read as a turn).
func (p *MIDIProfile) validate() error {
	if p == nil {
		return midiBad("Send a profile.")
	}
	if len(p.DeviceName) > 200 {
		return midiBad("The device name is longer than 200 characters.")
	}
	if len(p.Encoders) < 1 || len(p.Encoders) > midiMaxEncoders {
		return midiBad("A profile has 1 to %d encoders, not %d.", midiMaxEncoders, len(p.Encoders))
	}
	type key struct {
		kind    string
		ch, num int
	}
	used := map[key]string{}
	claim := func(k key, who string) error {
		if prev, ok := used[k]; ok {
			return midiBad("%s and %s answer to the same MIDI message (channel %d, %s %d).", prev, who, k.ch, k.kind, k.num)
		}
		used[k] = who
		return nil
	}
	for i, e := range p.Encoders {
		if e == nil {
			continue
		}
		who := fmt.Sprintf("Encoder %d", i+1)
		if e.Channel < 1 || e.Channel > 16 {
			return midiBad("%s: MIDI channel %d is not 1 to 16.", who, e.Channel)
		}
		if e.Control < 0 || e.Control > 127 {
			return midiBad("%s: controller %d is not 0 to 127.", who, e.Control)
		}
		if !midiModes[e.Mode] {
			return midiBad("%s: %q is not an encoder mode (twos, offset, signbit, absolute, absolute14).", who, e.Mode)
		}
		if e.Mode == MIDIModeAbsolute14 && e.Control > 31 {
			return midiBad("%s: a 14-bit pair uses controllers 0 to 31 (with n+32 as the fine half); %d is not one.", who, e.Control)
		}
		if err := claim(key{"cc", e.Channel, e.Control}, who); err != nil {
			return err
		}
		if e.Mode == MIDIModeAbsolute14 {
			if err := claim(key{"cc", e.Channel, e.Control + 32}, who+" (fine half)"); err != nil {
				return err
			}
		}
	}
	for i, e := range p.Encoders {
		if e == nil || e.Push == nil {
			continue
		}
		who := fmt.Sprintf("Encoder %d push", i+1)
		if err := e.Push.validate(who); err != nil {
			return err
		}
		if err := claim(key{e.Push.Kind, e.Push.Channel, e.Push.Number}, who); err != nil {
			return err
		}
	}
	for _, b := range []struct {
		who string
		b   *MIDIButton
	}{{"Bank back", p.BankPrev}, {"Bank forward", p.BankNext}} {
		if b.b == nil {
			continue
		}
		if err := b.b.validate(b.who); err != nil {
			return err
		}
		if err := claim(key{b.b.Kind, b.b.Channel, b.b.Number}, b.who); err != nil {
			return err
		}
	}
	a := p.Acceleration
	if a.ThresholdMs < 10 || a.ThresholdMs > 1000 {
		return midiBad("Acceleration threshold %d ms is not 10 to 1000.", a.ThresholdMs)
	}
	if a.Strength < 1 || a.Strength > 5 {
		return midiBad("Acceleration strength %d is not 1 to 5.", a.Strength)
	}
	return nil
}

// midiFile is the on-disk shape. Version lets a later build add profiles.
type midiFile struct {
	Version int          `json:"version"`
	Profile *MIDIProfile `json:"profile"`
}

// midiStore owns benny512-midi.json. Its zero value is an in-memory store
// (no path): what web.New gives every test, --demo and rehearsal child.
type midiStore struct {
	mu      sync.Mutex
	path    string
	profile *MIDIProfile
	loadErr string
}

func loadMIDIFile(path string) (*MIDIProfile, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("MIDI mapping not loaded from %s (starting with none; the file is left untouched): %w", path, err)
	}
	var f midiFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("MIDI mapping file %s is damaged (starting with none; the file is left untouched so it can be inspected): %w", path, err)
	}
	if f.Profile == nil {
		return nil, nil
	}
	if err := f.Profile.validate(); err != nil {
		return nil, fmt.Errorf("MIDI mapping file %s is damaged (starting with none; the file is left untouched): %v", path, err)
	}
	return f.Profile, nil
}

func saveMIDIFile(path string, p *MIDIProfile) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(midiFile{Version: 1, Profile: p}, "", "  ")
	if err != nil {
		return fmt.Errorf("MIDI mapping not saved: %w", err)
	}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		// Only a file that still parses becomes the recovery copy.
		var probe midiFile
		if json.Unmarshal(previous, &probe) == nil {
			if err := settingsAtomicWrite(path+".bak", previous); err != nil {
				return fmt.Errorf("MIDI mapping backup failed: %w", err)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("MIDI mapping not saved: %w", readErr)
	}
	if err := settingsAtomicWrite(path, data); err != nil {
		return fmt.Errorf("MIDI mapping not saved: %w", err)
	}
	return nil
}

// SetMIDIStorePath makes the MIDI mapping persistent at path. A damaged or
// unreadable file is reported (and kept on disk); the server runs with no
// profile until one is saved.
func (s *Server) SetMIDIStorePath(path string) error {
	p, err := loadMIDIFile(path)
	s.midi.mu.Lock()
	defer s.midi.mu.Unlock()
	s.midi.path = path
	s.midi.profile = p
	s.midi.loadErr = ""
	if err != nil {
		s.midi.loadErr = err.Error()
	}
	return err
}

type midiResponse struct {
	Profile *MIDIProfile `json:"profile"`
	// Persisted: the mapping is written to a file on this station (false
	// in --demo, where it lasts until the program closes).
	Persisted bool   `json:"persisted"`
	LoadError string `json:"loadError"`
}

func (s *Server) midiView() midiResponse {
	s.midi.mu.Lock()
	defer s.midi.mu.Unlock()
	return midiResponse{Profile: s.midi.profile, Persisted: s.midi.path != "", LoadError: s.midi.loadErr}
}

func (s *Server) handleGetMIDI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.midiView())
}

func (s *Server) handlePostMIDI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile *MIDIProfile `json:"profile"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("the MIDI mapping could not be read: %v", err))
		return
	}
	if req.Profile != nil {
		req.Profile.DeviceName = strings.TrimSpace(req.Profile.DeviceName)
	}
	if err := req.Profile.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.midi.mu.Lock()
	err := saveMIDIFile(s.midi.path, req.Profile)
	if err == nil {
		s.midi.profile = req.Profile
		s.midi.loadErr = ""
	}
	s.midi.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.midiView())
}
