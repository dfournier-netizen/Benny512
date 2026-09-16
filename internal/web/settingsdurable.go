// This file makes web.Settings permanent. Until it existed, Settings lived
// only in memory: every value on the Settings screen — the NIC, the poll
// interval, the capture limit, the RDM log path, the Art-Net starting
// universe — was reset by New on every launch, so a venue laptop forgot its
// own configuration each time it was opened. The owner's instruction was
// "make all settings permanent until changed by the user", and this is that.
//
// The file is benny512-settings.json beside the executable (see
// cmd/benny512's settingsStorePath), alongside benny512-patch.json,
// benny512-library.json and benny512-sacn.json. It is written with exactly
// the discipline internal/patch/durable.go established for the show: marshal
// first, keep the previous file as path+".bak", then land the new bytes via
// a synced temporary file and a checked rename, so a power cut mid-save
// cannot leave a truncated settings file where a good one was.
//
// Two policies here are deliberate and differ from each other:
//
//   - A DAMAGED FILE IS NEVER DESTROYED BY A READ. Load returns defaults in
//     memory and a non-nil error naming the problem, and leaves the bytes on
//     disk exactly as they are. This is internal/library.NewStore's position
//     ("must not become the library. Start empty; leave the file alone") and
//     it matters more here than there: the one artifact that can explain why
//     an installation lost its settings is the file that failed to parse, and
//     a startup that overwrites it destroys the only evidence.
//
//   - A DAMAGED FILE IS NOT PROMOTED TO .bak BY A WRITE. Save still replaces
//     a damaged file — the user editing settings and pressing Apply is
//     entitled to fix things — but it does not copy the damage over the
//     recovery copy, because that would throw away the last version that
//     actually parsed. internal/patch and internal/library instead refuse the
//     save outright; that is right for a show and a library, which hold work
//     that cannot be retyped, and wrong for eight scalar settings, which can.
//
// The on-disk shape is Settings itself, marshalled with the very same
// `json:"..."` tags GET/POST /api/settings uses. There is deliberately no
// separate on-disk struct: this project's recurring defect is one field
// spelled two ways in two files, and a private mirror of Settings is exactly
// the thing that drifts. settingsdurable_test.go asserts the literal keys in
// the file's bytes so a tag rename cannot pass silently.

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// settingsStore owns the settings file. It deliberately does NOT hold a copy
// of the current settings: Server.settings under Server.settingsMu is the one
// in-memory value, and a store that shadowed it would be a second source of
// truth to drift against. All this type owns is the path and a mutex
// serializing writes to it.
//
// A store built with an empty path is in-memory only: Load returns defaults
// and Save is a no-op returning nil. That is what web.New installs, so every
// test, --demo, and every offline-rehearsal child server touches no file at
// all until cmd/benny512 calls SetSettingsStorePath in real mode.
type settingsStore struct {
	mu   sync.Mutex
	path string
}

// newSettingsStore builds a store over path ("" for in-memory only). It does
// not read anything; call Load for that.
func newSettingsStore(path string) *settingsStore { return &settingsStore{path: path} }

// Path reports the file this store persists to ("" when in memory only).
func (st *settingsStore) Path() string { return st.path }

// Load reads the settings file.
//
// A MISSING file returns defaults and a nil error, and writes nothing: a
// fresh installation has no settings file and must not be told it has a
// problem, nor have one created for it before the user has saved anything.
//
// An UNREADABLE or MALFORMED file returns defaults and a NON-NIL error, and
// leaves the file untouched. The caller is expected to report that error
// rather than swallow it — the server still comes up, on defaults, but the
// user is told their settings did not load and their file is still there to
// inspect.
//
// Unknown and extra fields in an otherwise valid file are ignored (plain
// encoding/json Unmarshal, no DisallowUnknownFields), so a file written by a
// newer build, or one carrying a setting since removed, still opens.
func (st *settingsStore) Load() (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return loadSettingsFile(st.path)
}

// Save persists s, and returns the error rather than logging it, because the
// caller has to decide what to do about a failure. Server.handlePostSettings
// treats a failed save as fatal to the request — it does NOT publish the
// edited settings in memory and answers 500 with this error — so the running
// server is always on the settings it was last actually able to write. That
// is internal/patch and internal/sacn's rule, and the reason it matters is
// that the alternative lies: a UI that says "saved" over an unwritable disk
// sends someone home believing their rig is configured.
func (st *settingsStore) Save(s Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return saveSettingsFile(st.path, s)
}

// LoadSettingsFile reads the persisted settings at path WITHOUT installing
// them on a Server, returning defaults plus a non-nil error on any problem
// (see settingsStore.Load, which this shares its implementation with).
//
// It exists for exactly one caller: cmd/benny512 needs to know which NIC the
// user last chose BEFORE it builds anything, because the Art-Net socket is
// bound during construction and cannot be rebound afterwards. Everything else
// goes through Server.SetSettingsStorePath.
func LoadSettingsFile(path string) (Settings, error) { return newSettingsStore(path).Load() }

func loadSettingsFile(path string) (Settings, error) {
	out := defaultSettings()
	if path == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("settings not loaded from %s (running on defaults; the file is left untouched): %w", path, err)
	}
	var loaded Settings
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return out, fmt.Errorf("settings file %s is damaged (running on defaults; the file is left untouched so it can be inspected or repaired): %w", path, err)
	}
	// A file written by a build that still had the universeBase notation
	// switch is migrated here, exactly once, on the way in — see
	// migrateUniverseSetting, whose contract (an explicit
	// artnetStartUniverse always wins; the legacy field is dropped and never
	// written back) is unchanged and not re-implemented here.
	migrateUniverseSetting(&loaded)
	// The same range POST /api/settings enforces. A value outside it can only
	// have been hand-edited into the file, and running the whole application
	// on a universe correlation the API would have refused is worse than
	// coming up on defaults and saying so.
	if loaded.ArtnetStartUniverse < 0 || loaded.ArtnetStartUniverse > 32767 {
		return out, fmt.Errorf("settings file %s is damaged (running on defaults; the file is left untouched): artnetStartUniverse must be an Art-Net Port-Address in 0-32767, got %d", path, loaded.ArtnetStartUniverse)
	}
	// TimeoutProfiles is a map the rest of this package indexes without a nil
	// check, and JSON has no way to distinguish "absent" from "empty object"
	// on the way back in. defaultSettings makes it an empty map; a load must
	// do the same, or a settings file saved before any per-node profile was
	// chosen would come back with a nil map where a fresh server has one.
	if loaded.TimeoutProfiles == nil {
		loaded.TimeoutProfiles = map[string]string{}
	}
	return loaded, nil
}

func saveSettingsFile(path string, s Settings) error {
	if path == "" {
		return nil
	}
	// Never written back. migrateUniverseSetting already nils this on the way
	// through POST and Load; clearing it here too makes "the legacy field
	// cannot be re-applied on a later save and silently renumber a rig" a
	// property of the writer rather than a thing every caller must remember.
	s.LegacyUniverseBase = nil
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings not saved: %w", err)
	}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		// Only a file that still PARSES becomes the recovery copy. Promoting a
		// damaged one would overwrite the last known-good .bak with the
		// damage, which is the opposite of what a backup is for. A damaged
		// current file is still replaced below — see this file's header.
		var probe Settings
		if json.Unmarshal(previous, &probe) == nil {
			if err := settingsAtomicWrite(path+".bak", previous); err != nil {
				return fmt.Errorf("settings backup failed: %w", err)
			}
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("settings not saved: %w", readErr)
	}
	if err := settingsAtomicWrite(path, data); err != nil {
		return fmt.Errorf("settings not saved: %w", err)
	}
	return nil
}

// settingsAtomicWrite mirrors internal/patch/durable.go's atomicWrite and
// internal/sacn's copy of it: flush a sibling temporary file, then rename it
// over the destination, so a failed or torn write never replaces a good file.
func settingsAtomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".benny-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
