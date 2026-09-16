package sacn

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRawSettings reads the on-disk file as plain JSON, without going through
// the Store, so a test asserts what actually landed on disk rather than what
// the writer believes it wrote.
func readRawSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("settings file is not JSON: %v\n%s", err, raw)
	}
	return m
}

// TestCIDIsGeneratedOnceAndSurvivesRestarts is the whole point of persisting
// this file: a receiver distinguishes sources by CID, so a new CID on every
// launch would present the rig with a brand new source every time the app
// opens.
func TestCIDIsGeneratedOnceAndSurvivesRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-sacn.json")

	first, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cid := first.Get().CID
	if cid == ([16]byte{}) {
		t.Fatal("first launch produced an all-zero CID; crypto/rand was not consulted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("first launch did not persist the generated CID: %v", err)
	}

	// The value on disk is what a later launch will read, so check it there
	// too rather than trusting the in-memory copy.
	onDisk := readRawSettings(t, path)
	if got, want := onDisk["cid"], hex.EncodeToString(cid[:]); got != want {
		t.Fatalf("cid on disk = %v, want %q", got, want)
	}

	// Two more "restarts" against the same file.
	for i := 0; i < 2; i++ {
		later, err := NewStore(path)
		if err != nil {
			t.Fatalf("restart %d: NewStore: %v", i, err)
		}
		if later.Get().CID != cid {
			t.Fatalf("restart %d regenerated the CID: %x, want %x", i, later.Get().CID, cid)
		}
	}

	// Editing the other fields must not disturb it either.
	if _, err := first.Set(42, 150, "10.0.0.9"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	after, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore after Set: %v", err)
	}
	got := after.Get()
	if got.CID != cid {
		t.Fatalf("a settings edit regenerated the CID: %x, want %x", got.CID, cid)
	}
	if got.StartUniverse != 42 || got.Priority != 150 || got.UnicastTo != "10.0.0.9" {
		t.Fatalf("settings did not round-trip: %+v", got)
	}
}

func TestDefaultsAreWhatTheStandardAndTheOwnerAsked(t *testing.T) {
	st, err := NewStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got := st.Get()
	if got.StartUniverse != 1 {
		t.Fatalf("default startUniverse = %d, want 1", got.StartUniverse)
	}
	if got.Priority != 100 {
		t.Fatalf("default priority = %d, want 100 (ANSI E1.31-2025 §6.2.3's \"shall transmit a priority of 100\")", got.Priority)
	}
	if got.UnicastTo != "" {
		t.Fatalf("default unicastTo = %q, want \"\" (multicast)", got.UnicastTo)
	}
}

func TestCorruptFileRegeneratesAndResaves(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"not json at all", "{{{ this is not json"},
		{"empty file", ""},
		{"json but the wrong shape", `["startUniverse", 5]`},
		{"cid is not hex", `{"cid":"zzzz","startUniverse":7,"priority":110,"unicastTo":""}`},
		{"cid is the wrong length", `{"cid":"0011","startUniverse":7,"priority":110,"unicastTo":""}`},
		{"cid missing entirely", `{"startUniverse":7,"priority":110,"unicastTo":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			st, err := NewStore(path)
			if err != nil {
				t.Fatalf("NewStore did not survive a corrupt file: %v", err)
			}
			got := st.Get()
			if got.CID == ([16]byte{}) {
				t.Fatal("a corrupt file left the CID all zero instead of regenerating it")
			}
			// It must have been re-saved, or the next launch repeats the work
			// and the CID is not in fact stable.
			onDisk := readRawSettings(t, path)
			if onDisk["cid"] != hex.EncodeToString(got.CID[:]) {
				t.Fatalf("the repaired CID was not written back: file has %v, memory has %x", onDisk["cid"], got.CID)
			}
			reloaded, err := NewStore(path)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if reloaded.Get().CID != got.CID {
				t.Fatalf("the repaired CID is not stable across a reload: %x then %x", got.CID, reloaded.Get().CID)
			}
		})
	}
}

// TestCorruptFileKeepsTheValidFieldsItCan repairs only what is broken. A
// mistyped CID must not also silently reset a start universe the user set.
func TestCorruptFileKeepsTheValidFieldsItCan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(path, []byte(`{"cid":"nothex","startUniverse":77,"priority":33,"unicastTo":"192.168.1.5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got := st.Get()
	if got.StartUniverse != 77 || got.Priority != 33 || got.UnicastTo != "192.168.1.5" {
		t.Fatalf("repairing the CID discarded good settings: %+v", got)
	}
}

func TestSetRejectsOutOfRangeValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	before := st.Get()

	for _, tc := range []struct {
		name          string
		universe      int
		priority      int
		unicast       string
		mustMentioned string
	}{
		{"universe 0 does not exist in E1.31", 0, 100, "", "1..63999"},
		{"universe 64000 is one past the top", 64000, 100, "", "1..63999"},
		{"negative universe", -1, 100, "", "1..63999"},
		{"priority 201 exceeds the standard's range", 1, 201, "", "0 to 200"},
		{"negative priority", 1, -5, "", "0 to 200"},
		{"priority 0 is legal in the standard but this sender cannot transmit it", 1, 0, "", "cannot transmit it"},
		{"unicast that is not an address", 1, 100, "not-an-ip", "not an IP address"},
		{"unicast IPv6", 1, 100, "fe80::1", "IPv4 only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.Set(tc.universe, tc.priority, tc.unicast)
			if err == nil {
				t.Fatalf("Set(%d, %d, %q) was accepted", tc.universe, tc.priority, tc.unicast)
			}
			if !strings.Contains(err.Error(), tc.mustMentioned) {
				t.Fatalf("error %q does not explain the limit (%q)", err.Error(), tc.mustMentioned)
			}
			if got != before {
				t.Fatalf("a rejected Set published %+v; the store must still hold %+v", got, before)
			}
			if st.Get() != before {
				t.Fatalf("a rejected Set mutated the store: %+v, want %+v", st.Get(), before)
			}
		})
	}
}

// TestFailedSaveDoesNotPublishBadInMemoryState is the durability rule the
// show file already follows: if it did not reach the disk, the running server
// must not act as if it had.
func TestFailedSaveDoesNotPublishBadInMemoryState(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "cfg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "s.json")
	st, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := st.Set(10, 120, ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	good := st.Get()

	// Make the containing directory a regular file, so every write below
	// fails at the operating system rather than at a mock.
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte("no longer a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := st.Set(999, 55, "10.1.2.3")
	if err == nil {
		t.Fatal("Set reported success with an unwritable destination")
	}
	if got != good {
		t.Fatalf("a failed save returned %+v; it must return the last settings that actually persisted, %+v", got, good)
	}
	if st.Get() != good {
		t.Fatalf("a failed save published %+v into memory; the store must still hold %+v", st.Get(), good)
	}
	if !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("error %q does not say the settings were not saved", err.Error())
	}
}

// TestSaveKeepsTheRecoveryCopy mirrors the show file's .bak discipline.
func TestSaveKeepsTheRecoveryCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := st.Set(5, 100, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Set(6, 100, ""); err != nil {
		t.Fatal(err)
	}
	bak := readRawSettings(t, path+".bak")
	if got := bak["startUniverse"]; got != float64(5) {
		t.Fatalf("recovery copy holds startUniverse %v, want the preceding save's 5", got)
	}
}

// TestInMemoryStoreTouchesNoDisk covers what web.New builds before
// cmd/benny512 installs a path.
func TestInMemoryStoreTouchesNoDisk(t *testing.T) {
	st, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore(\"\"): %v", err)
	}
	if st.Path() != "" {
		t.Fatalf("Path() = %q, want empty", st.Path())
	}
	if st.Get().CID == ([16]byte{}) {
		t.Fatal("in-memory store has no CID")
	}
	if _, err := st.Set(3, 100, ""); err != nil {
		t.Fatalf("Set on an in-memory store: %v", err)
	}
	if st.Get().StartUniverse != 3 {
		t.Fatalf("in-memory Set did not take: %+v", st.Get())
	}
}
