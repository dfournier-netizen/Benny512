package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// These tests deliberately go through a REAL FILE ON DISK in t.TempDir()
// rather than asserting a struct against a struct the same code built. This
// project has shipped defects from tests that only proved marshal(unmarshal(x))
// == x; the interesting failures are the ones that only exist once bytes land
// on a filesystem — a renamed json tag, a nil map coming back where an empty
// one went out, a save that reported success without writing, a damaged file
// destroyed by the read that found it.

// customSettings is the value under test everywhere below. Every field is
// non-zero on purpose, ArtnetStartUniverse included: a round-trip test whose
// fixture leaves a field at its zero value cannot tell "persisted correctly"
// apart from "silently dropped".
func customSettings() Settings {
	return Settings{
		NIC:            "en7",
		PollIntervalMS: 3500,
		CaptureLimit:   271,
		TimeoutProfiles: map[string]string{
			"10.0.0.7/1": "WirelessProxy",
			"10.0.0.8/1": "Direct",
		},
		LogRDMPath:          "/var/log/benny512-rdm.log",
		ArtnetStartUniverse: 100,
	}
}

// customSettingsNoLog is customSettings with LogRDMPath cleared, for the
// tests that POST through the handler. A non-empty LogRDMPath makes
// handlePostSettings try to OPEN that file (applyLogRDMPathLocked), which is
// a different subsystem's failure mode and not what those tests are about.
func customSettingsNoLog() Settings {
	s := customSettings()
	s.LogRDMPath = ""
	return s
}

func TestSettingsStore_RoundTripsThroughARealFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	want := customSettings()

	if err := newSettingsStore(path).Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A FRESH store over the same path — nothing carried over in memory.
	got, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.NIC != want.NIC {
		t.Errorf("NIC = %q, want %q", got.NIC, want.NIC)
	}
	if got.PollIntervalMS != want.PollIntervalMS {
		t.Errorf("PollIntervalMS = %d, want %d", got.PollIntervalMS, want.PollIntervalMS)
	}
	if got.CaptureLimit != want.CaptureLimit {
		t.Errorf("CaptureLimit = %d, want %d", got.CaptureLimit, want.CaptureLimit)
	}
	if got.LogRDMPath != want.LogRDMPath {
		t.Errorf("LogRDMPath = %q, want %q", got.LogRDMPath, want.LogRDMPath)
	}
	if got.ArtnetStartUniverse != want.ArtnetStartUniverse {
		t.Errorf("ArtnetStartUniverse = %d, want %d — a non-zero start universe is exactly the field a dropped save looks identical to at the default", got.ArtnetStartUniverse, want.ArtnetStartUniverse)
	}
	if !reflect.DeepEqual(got.TimeoutProfiles, want.TimeoutProfiles) {
		t.Errorf("TimeoutProfiles = %#v, want %#v", got.TimeoutProfiles, want.TimeoutProfiles)
	}
	if got.LegacyUniverseBase != nil {
		t.Errorf("LegacyUniverseBase = %v, want nil — the legacy field must never come back out of a file this build wrote", *got.LegacyUniverseBase)
	}
}

// TestSettingsStore_EmptyTimeoutProfilesSurvivesAsEmptyNotNil pins the
// empty-vs-nil distinction JSON cannot express. defaultSettings() hands out an
// empty map and the rest of this package indexes TimeoutProfiles without a nil
// check, so a load that returned nil where a save wrote {} would be a real,
// latent nil-map difference between "fresh server" and "restarted server".
func TestSettingsStore_EmptyTimeoutProfilesSurvivesAsEmptyNotNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	want := defaultSettings()
	if want.TimeoutProfiles == nil {
		t.Fatal("precondition: defaultSettings must hand out an empty, non-nil map")
	}
	if err := newSettingsStore(path).Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TimeoutProfiles == nil {
		t.Fatal("TimeoutProfiles came back nil; a saved-then-reloaded server must have the same empty map a fresh one does")
	}
	if len(got.TimeoutProfiles) != 0 {
		t.Errorf("TimeoutProfiles = %#v, want empty", got.TimeoutProfiles)
	}
}

// TestSettingsStore_NilTimeoutProfilesLoadsAsEmpty covers the other direction:
// a file with no timeoutProfiles key at all (hand-written, or from a build
// that had not added the field) must still produce a usable map.
func TestSettingsStore_NilTimeoutProfilesLoadsAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	if err := os.WriteFile(path, []byte(`{"nic":"en0","pollIntervalMs":1000,"captureLimit":10,"artnetStartUniverse":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TimeoutProfiles == nil {
		t.Fatal("a file with no timeoutProfiles key must load as an empty map, not nil")
	}
}

// TestSettingsStore_OnDiskJSONKeysAreLiteral reads the file's BYTES and names
// every key it must contain. It exists so that renaming a `json:"..."` tag on
// Settings — the change that produces this project's recurring "two spellings
// in two files" bug — fails here loudly instead of silently orphaning every
// installed settings file in the field.
func TestSettingsStore_OnDiskJSONKeysAreLiteral(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	if err := newSettingsStore(path).Save(customSettings()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	var onDisk map[string]json.RawMessage
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("the settings file is not a JSON object: %v\n%s", err, raw)
	}
	// Spelled out as literals, not derived from the struct tags, on purpose.
	for _, key := range []string{"nic", "pollIntervalMs", "captureLimit", "timeoutProfiles", "logRdmPath", "artnetStartUniverse"} {
		if _, ok := onDisk[key]; !ok {
			t.Errorf("settings file has no %q key; keys present: %v\n%s", key, sortedKeys(onDisk), raw)
		}
	}
	// universeBase is the legacy field. It is read on the way in and must
	// never be written on the way out, or a later load would re-apply it and
	// silently renumber a rig.
	if _, ok := onDisk["universeBase"]; ok {
		t.Errorf("settings file contains the legacy %q key; it must never be written back\n%s", "universeBase", raw)
	}
	// And the values, literally, so a tag that survives but points at the
	// wrong field is caught too.
	assertJSONField(t, raw, onDisk, "nic", `"en7"`)
	assertJSONField(t, raw, onDisk, "pollIntervalMs", `3500`)
	assertJSONField(t, raw, onDisk, "captureLimit", `271`)
	assertJSONField(t, raw, onDisk, "artnetStartUniverse", `100`)
	assertJSONField(t, raw, onDisk, "logRdmPath", `"/var/log/benny512-rdm.log"`)
}

func assertJSONField(t *testing.T, raw []byte, onDisk map[string]json.RawMessage, key, want string) {
	t.Helper()
	got := strings.TrimSpace(string(onDisk[key]))
	if got != want {
		t.Errorf("settings file %q = %s, want %s\n%s", key, got, want, raw)
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSettingsStore_MissingFileIsDefaultsAndWritesNothing: a fresh
// installation has no settings file. It must not be reported as a problem,
// and a read must not create one — an empty benny512-settings.json appearing
// beside the exe before the user has saved anything is noise at best and a
// wrong "this was configured" signal at worst.
func TestSettingsStore_MissingFileIsDefaultsAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benny512-settings.json")

	got, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("a missing settings file must not be an error, got %v", err)
	}
	if !reflect.DeepEqual(got, defaultSettings()) {
		t.Errorf("Load of a missing file = %+v, want defaults %+v", got, defaultSettings())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("reading a missing settings file created %v; it must write nothing", entries)
	}
}

// TestSettingsStore_DamagedFileYieldsDefaultsAndIsNotOverwritten is the one
// that protects the evidence. When the file will not parse, the server comes
// up on defaults, the condition is REPORTED rather than swallowed, and the
// damaged bytes are still there afterwards, byte for byte, for someone to
// look at or repair.
func TestSettingsStore_DamagedFileYieldsDefaultsAndIsNotOverwritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"truncated mid-write", `{"nic":"en0","pollInterval`},
		{"not JSON at all", "\x00\x01 this is not a settings file\n"},
		{"a JSON array", `["nic","en0"]`},
		{"artnetStartUniverse out of the range the API enforces", `{"nic":"en0","artnetStartUniverse":99999}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "benny512-settings.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := newSettingsStore(path).Load()
			if err == nil {
				t.Fatal("a damaged settings file must be reported, not swallowed")
			}
			if !reflect.DeepEqual(got, defaultSettings()) {
				t.Errorf("Load of a damaged file = %+v, want defaults %+v", got, defaultSettings())
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("the damaged settings file is gone after the load that found it: %v", readErr)
			}
			if string(after) != tc.body {
				t.Errorf("the damaged settings file was rewritten by the read.\n got: %q\nwant: %q", after, tc.body)
			}
			if _, statErr := os.Stat(path + ".bak"); statErr == nil {
				t.Error("a load must not create a .bak; only a successful save does")
			}
		})
	}
}

// TestSettingsStore_UnknownFieldsDoNotBreakTheLoad: a file written by a newer
// build, or one carrying a setting since removed, must still open. The known
// fields come through; the rest is ignored.
func TestSettingsStore_UnknownFieldsDoNotBreakTheLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	body := `{
  "nic": "en7",
  "pollIntervalMs": 3500,
  "captureLimit": 271,
  "artnetStartUniverse": 100,
  "somethingFromAFutureBuild": {"deeply": ["nested", 1, true]},
  "aSettingSinceRemoved": "whatever"
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("unknown fields must not break the load: %v", err)
	}
	if got.NIC != "en7" || got.PollIntervalMS != 3500 || got.CaptureLimit != 271 || got.ArtnetStartUniverse != 100 {
		t.Errorf("known fields did not survive an unknown-field load: %+v", got)
	}
}

// TestSettingsStore_KeepsOnePreviousSaveAsBak pins the recovery copy: one
// .bak of the PRECEDING successful save, same as internal/patch/durable.go.
func TestSettingsStore_KeepsOnePreviousSaveAsBak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	st := newSettingsStore(path)

	first := customSettings()
	if err := st.Save(first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the first save must not produce a .bak (there is no preceding version), stat err = %v", err)
	}
	second := first
	second.CaptureLimit = 999
	if err := st.Save(second); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no recovery copy after the second save: %v", err)
	}
	var recovered Settings
	if err := json.Unmarshal(bak, &recovered); err != nil {
		t.Fatalf("the recovery copy is not readable settings: %v\n%s", err, bak)
	}
	if recovered.CaptureLimit != first.CaptureLimit {
		t.Errorf(".bak CaptureLimit = %d, want the preceding save's %d", recovered.CaptureLimit, first.CaptureLimit)
	}
	live, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if live.CaptureLimit != second.CaptureLimit {
		t.Errorf("live CaptureLimit = %d, want the newest save's %d", live.CaptureLimit, second.CaptureLimit)
	}
}

// TestSettingsStore_DamagedFileIsNotPromotedToBak: saving over a damaged file
// must replace it (the user is allowed to fix things by pressing Apply) but
// must NOT copy the damage over the last known-good recovery copy.
func TestSettingsStore_DamagedFileIsNotPromotedToBak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	st := newSettingsStore(path)

	good := customSettings()
	if err := st.Save(good); err != nil {
		t.Fatalf("Save good: %v", err)
	}
	second := good
	second.CaptureLimit = 999
	if err := st.Save(second); err != nil { // now .bak holds `good`
		t.Fatalf("Save second: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"nic":"en0",`), 0o644); err != nil {
		t.Fatal(err)
	}

	third := good
	third.CaptureLimit = 1234
	if err := st.Save(third); err != nil {
		t.Fatalf("saving over a damaged settings file must succeed: %v", err)
	}
	live, err := newSettingsStore(path).Load()
	if err != nil || live.CaptureLimit != 1234 {
		t.Errorf("after saving over damage: %+v err=%v, want CaptureLimit 1234", live, err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("the recovery copy vanished: %v", err)
	}
	var recovered Settings
	if err := json.Unmarshal(bak, &recovered); err != nil {
		t.Fatalf("the damaged file was promoted to .bak, destroying the last good copy: %v\n%s", err, bak)
	}
	if recovered.CaptureLimit != good.CaptureLimit {
		t.Errorf(".bak CaptureLimit = %d, want the last GOOD save's %d", recovered.CaptureLimit, good.CaptureLimit)
	}
}

// --- through the HTTP layer ------------------------------------------------

// TestPostSettings_PersistsToDisk is the end-to-end claim the owner actually
// made: press Apply, and it is still there next time.
func TestPostSettings_PersistsToDisk(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatalf("SetSettingsStorePath: %v", err)
	}

	want := customSettingsNoLog()
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", want); rr.Code != http.StatusOK {
		t.Fatalf("POST /api/settings: status=%d body=%s", rr.Code, rr.Body.String())
	}

	reloaded, err := newSettingsStore(path).Load()
	if err != nil {
		t.Fatalf("Load after POST: %v", err)
	}
	if reloaded.NIC != want.NIC || reloaded.PollIntervalMS != want.PollIntervalMS ||
		reloaded.CaptureLimit != want.CaptureLimit || reloaded.ArtnetStartUniverse != want.ArtnetStartUniverse {
		t.Errorf("on disk after POST = %+v, want %+v", reloaded, want)
	}
	if !reflect.DeepEqual(reloaded.TimeoutProfiles, want.TimeoutProfiles) {
		t.Errorf("TimeoutProfiles on disk = %#v, want %#v", reloaded.TimeoutProfiles, want.TimeoutProfiles)
	}
}

// TestSetSettingsStorePath_LoadsWhatWasSavedBefore is the restart: a file
// written by one server is what the next one comes up on.
func TestSetSettingsStorePath_LoadsWhatWasSavedBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	want := customSettingsNoLog()
	if err := newSettingsStore(path).Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	h := newHarness(t)
	if got := h.srv.SettingsSnapshot(); got.ArtnetStartUniverse != 0 || got.NIC != "" {
		t.Fatalf("precondition: a fresh server must start on defaults, got %+v", got)
	}
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatalf("SetSettingsStorePath: %v", err)
	}
	got := h.srv.SettingsSnapshot()
	if got.NIC != want.NIC || got.ArtnetStartUniverse != want.ArtnetStartUniverse || got.CaptureLimit != want.CaptureLimit {
		t.Errorf("settings after install = %+v, want the saved %+v", got, want)
	}
}

// TestSetSettingsStorePath_DamagedFileReportsAndKeepsServerUsable: the error
// reaches the caller (cmd/benny512 logs it), the server runs on defaults, and
// the file is still there.
func TestSetSettingsStorePath_DamagedFileReportsAndKeepsServerUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benny512-settings.json")
	body := `{"nic":"en0",`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	err := h.srv.SetSettingsStorePath(path)
	if err == nil {
		t.Fatal("a damaged settings file must be reported to the caller")
	}
	if got := h.srv.SettingsSnapshot(); !reflect.DeepEqual(got, defaultSettings()) {
		t.Errorf("settings after a damaged load = %+v, want defaults", got)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != body {
		t.Errorf("the damaged file must survive startup untouched: %q err=%v", after, readErr)
	}
	// Still usable: a later POST succeeds and replaces the damaged file.
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", customSettingsNoLog()); rr.Code != http.StatusOK {
		t.Fatalf("POST after a damaged load: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestPostSettings_FailedSaveKeepsMemoryAndReportsTheRealError is the
// no-lying rule. An unwritable settings directory must produce a 5xx carrying
// the actual error, and must leave the running server on the settings it was
// last able to persist — not on the edit it could not.
func TestPostSettings_FailedSaveKeepsMemoryAndReportsTheRealError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0o500 directory does not deny writes")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o500); err != nil { // r-x: no new files, no rename
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	path := filepath.Join(dir, "benny512-settings.json")

	h := newHarness(t)
	if err := h.srv.SetSettingsStorePath(path); err != nil {
		t.Fatalf("SetSettingsStorePath on a missing file must not fail: %v", err)
	}
	before := h.srv.SettingsSnapshot()

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", customSettingsNoLog())
	if rr.Code < 500 || rr.Code > 599 {
		t.Fatalf("status = %d, want a 5xx; body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "settings not saved") {
		t.Errorf("response body = %s, want the real save error, not a generic one", body)
	}
	after := h.srv.SettingsSnapshot()
	if !reflect.DeepEqual(after, before) {
		t.Errorf("a failed save published an edited state.\nbefore: %+v\n after: %+v", before, after)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed save must not leave a settings file behind, stat err = %v", err)
	}
}

// TestServer_NewHasNoSettingsFile: every Server built by web.New — every test,
// --demo, and every offline-rehearsal child — is in-memory only until
// cmd/benny512 says otherwise. This is the structural reason --demo and the
// rehearsal child cannot touch the installation's settings file.
func TestServer_NewHasNoSettingsFile(t *testing.T) {
	h := newHarness(t)
	if got := h.srv.SettingsStorePath(); got != "" {
		t.Errorf("a Server from web.New has settings store path %q, want \"\"", got)
	}
	if rr := doJSON(t, h.srv.Handler(), "POST", "/api/settings", customSettingsNoLog()); rr.Code != http.StatusOK {
		t.Fatalf("POST against an in-memory store: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := h.srv.SettingsSnapshot(); got.CaptureLimit != customSettings().CaptureLimit {
		t.Errorf("an in-memory store must still accept edits, got %+v", got)
	}
}

// TestSettingsStore_LeavesNoGoroutinesOrTempFiles. Every save opens a
// temporary file, syncs it and renames it; every load opens the real one.
// None of that may leak a descriptor or a goroutine, and — because the temp
// files are created in the SAME directory as the settings file, beside the
// exe — a failed or abandoned save must not litter that directory either.
func TestSettingsStore_LeavesNoGoroutinesOrTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benny512-settings.json")
	st := newSettingsStore(path)

	before := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		s := customSettings()
		s.CaptureLimit = i
		if err := st.Save(s); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
		if _, err := st.Load(); err != nil {
			t.Fatalf("Load %d: %v", i, err)
		}
	}
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines %d -> %d across 200 save/load cycles", before, after)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Errorf("directory contains %v, want exactly the settings file and its .bak — a leftover .benny-settings-* means a temp file was not cleaned up", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, ".benny-settings-") {
			t.Errorf("leftover temp file %q", n)
		}
	}

	// A FAILING save must not litter either: the temp file is removed by the
	// deferred cleanup even when the rename never happens.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if err := newSettingsStore(filepath.Join(locked, "benny512-settings.json")).Save(customSettings()); err == nil {
			t.Fatal("saving into an unwritable directory should fail")
		}
		_ = os.Chmod(locked, 0o700)
		left, err := os.ReadDir(locked)
		if err != nil {
			t.Fatal(err)
		}
		if len(left) != 0 {
			t.Errorf("a failed save left %d file(s) behind in the target directory", len(left))
		}
	}
}
