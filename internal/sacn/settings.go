package sacn

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// This file is the persisted sACN configuration: one small JSON file beside
// the exe (benny512-sacn.json), written with the same synced-temp-file and
// checked-rename discipline internal/patch/durable.go uses for the show.
//
// It is deliberately NOT part of web.Settings, and stays separate now that
// web.Settings has a file of its own (internal/web/settingsdurable.go,
// benny512-settings.json). The reason is no longer "web.Settings does not
// persist" -- it does, as of that change -- but RESET SEMANTICS: the full
// reset rewrites benny512-settings.json with defaults, while THIS file is
// exempt from reset entirely, because the CID below is this installation's
// E1.31 source identity and is meant to outlive a reset. Two files with
// opposite lifetimes cannot be one file.

// DefaultStartUniverse is the sACN universe this application puts SHOW
// universe 1 on when nothing has been configured. 1, because sACN universes
// start at 1 (ANSI E1.31-2025 reserves 0) and a rig with no other constraint
// wants its first universe to be its first universe.
const DefaultStartUniverse = 1

// MinPriority / MaxPriority bound the E1.31 priority field (octet 108).
//
// ANSI E1.31-2025 Section 6.2.3 is the governing text: "Sources that do not
// support variable priority shall transmit a priority of 100. No priority
// outside the range of 0 to 200 shall be transmitted on the network.
// Priority increases with numerical value, e.g., 200 is a higher priority
// than 100."
//
// So the standard's range is 0..200, and 0 IS a legal transmitted value.
// MinPriority is nevertheless 1, and that is a deliberate, documented
// limitation of THIS build rather than a reading of the standard: Sender's
// Config.Priority is a byte whose zero value means "use DefaultPriority"
// (see sender.go), so there is currently no way to ask Sender to transmit an
// actual 0. Accepting a 0 here would store a number that comes out of the
// socket as 100 -- a plausible value standing in for an answer we cannot
// give -- so the store refuses it and says why instead.
const (
	MinPriority = 1
	MaxPriority = 200
)

// Settings is the persisted sACN configuration.
type Settings struct {
	// CID is this installation's E1.31 Component Identifier (octets 22-37).
	// It is generated once with crypto/rand on first use and reused on every
	// later launch, so receivers see one stable source identity across
	// restarts rather than a new source every time the app opens. It is
	// owned server-side: it is never exposed or accepted over the HTTP API.
	CID [16]byte
	// StartUniverse is the sACN universe SHOW universe 1 lives on.
	StartUniverse int
	// Priority is the E1.31 priority stamped on every packet.
	Priority int
	// UnicastTo, when non-empty, is an IPv4 destination that replaces the
	// Table 9-10 multicast group. Empty means multicast.
	UnicastTo string
}

// settingsFile is the on-disk shape. CID is hex so the file stays readable
// and diffable; a JSON number array or base64 blob would not.
type settingsFile struct {
	CID           string `json:"cid"`
	StartUniverse int    `json:"startUniverse"`
	Priority      int    `json:"priority"`
	UnicastTo     string `json:"unicastTo"`
}

// Store holds the current sACN configuration and owns its file.
//
// A failed save never publishes: Set validates, writes, and only then swaps
// the in-memory value, so an unwritable disk leaves the running server on the
// settings it was actually last able to persist.
type Store struct {
	mu   sync.Mutex
	path string
	cur  Settings
}

// NewStore loads path, or starts from defaults when path is "" (in-memory
// only -- what every test and every Server built by web.New gets until
// cmd/benny512 calls SetSACNStorePath).
//
// Any problem reading the file -- missing, unreadable, malformed JSON, a CID
// that is not 16 hex-encoded bytes, an out-of-range universe or priority, an
// unparseable unicast address -- is repaired in memory and re-saved, so a
// corrupt file self-heals into a usable one rather than disabling sACN
// output. The returned error reports a FAILED SAVE only; the Store it comes
// with is always usable.
func NewStore(path string) (*Store, error) {
	st := &Store{path: path}
	loaded, repaired := loadSettings(path)
	st.cur = loaded
	if path == "" || !repaired {
		return st, nil
	}
	if err := writeSettings(path, st.cur); err != nil {
		return st, err
	}
	return st, nil
}

// Path reports the file this store persists to ("" when in memory only).
func (st *Store) Path() string { return st.path }

// Get returns a copy of the current settings.
func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cur
}

// Set validates and persists a new start universe, priority and unicast
// destination, keeping the existing CID. On any validation or write failure
// the in-memory settings are left exactly as they were.
func (st *Store) Set(startUniverse, priority int, unicastTo string) (Settings, error) {
	if err := ValidateStartUniverse(startUniverse); err != nil {
		return st.Get(), err
	}
	if err := ValidatePriority(priority); err != nil {
		return st.Get(), err
	}
	unicast, err := ValidateUnicastTo(unicastTo)
	if err != nil {
		return st.Get(), err
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	next := Settings{CID: st.cur.CID, StartUniverse: startUniverse, Priority: priority, UnicastTo: unicast}
	if st.path != "" {
		if err := writeSettings(st.path, next); err != nil {
			return st.cur, err
		}
	}
	st.cur = next
	return st.cur, nil
}

// ValidateStartUniverse bounds the sACN start universe by the range codec.go
// enforces on the wire (ANSI E1.31-2025: universe 0 is reserved).
func ValidateStartUniverse(u int) error {
	if u < 1 || u > maxUniverse {
		return fmt.Errorf("sACN startUniverse must be 1..%d, got %d", maxUniverse, u)
	}
	return nil
}

// ValidatePriority bounds the E1.31 priority field. See MinPriority's comment
// for why the low end is 1 here and not the standard's 0.
func ValidatePriority(p int) error {
	if p == 0 {
		return fmt.Errorf("sACN priority 0 is legal in ANSI E1.31-2025 Section 6.2.3 but this build's sender cannot transmit it (a zero Config.Priority means \"use the default of %d\"); choose %d..%d", DefaultPriority, MinPriority, MaxPriority)
	}
	if p < MinPriority || p > MaxPriority {
		return fmt.Errorf("sACN priority must be %d..%d (ANSI E1.31-2025 Section 6.2.3: \"No priority outside the range of 0 to 200 shall be transmitted on the network\"), got %d", MinPriority, MaxPriority, p)
	}
	return nil
}

// ValidateUnicastTo accepts "" (multicast) or an IPv4 address, and returns
// the canonical form to store.
func ValidateUnicastTo(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", fmt.Errorf("sACN unicastTo %q is not an IP address; leave it empty to multicast", s)
	}
	v4 := ip.To4()
	if v4 == nil {
		return "", fmt.Errorf("sACN unicastTo %q is not an IPv4 address; E1.31 transport here is IPv4 only", s)
	}
	return v4.String(), nil
}

// NewCID generates a fresh Component Identifier. Exported so a caller that
// genuinely wants a new source identity can ask for one explicitly; nothing
// in the normal lifecycle does, which is the point of persisting it.
func NewCID() ([16]byte, error) {
	var cid [16]byte
	if _, err := rand.Read(cid[:]); err != nil {
		return cid, fmt.Errorf("sacn: generate CID: %w", err)
	}
	return cid, nil
}

// defaultSettings builds an unsaved configuration with a brand new CID.
func defaultSettings() Settings {
	cid, err := NewCID()
	if err != nil {
		// crypto/rand.Read does not fail on any platform this ships to; if
		// it ever did, a CID of all zeroes is still a syntactically valid
		// (if unhelpful) one, and refusing to construct a Store here would
		// take the whole server down over a configuration detail.
		cid = [16]byte{}
	}
	return Settings{CID: cid, StartUniverse: DefaultStartUniverse, Priority: int(DefaultPriority)}
}

// loadSettings reads path and returns the settings to use plus whether
// anything had to be repaired (which is what tells NewStore to re-save).
func loadSettings(path string) (Settings, bool) {
	out := defaultSettings()
	if path == "" {
		return out, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, true
	}
	var f settingsFile
	if json.Unmarshal(raw, &f) != nil {
		return out, true
	}

	repaired := false
	if cid, ok := decodeCID(f.CID); ok {
		out.CID = cid
	} else {
		repaired = true // missing or malformed CID regenerates and re-saves
	}
	if ValidateStartUniverse(f.StartUniverse) == nil {
		out.StartUniverse = f.StartUniverse
	} else {
		repaired = true
	}
	if ValidatePriority(f.Priority) == nil {
		out.Priority = f.Priority
	} else {
		repaired = true
	}
	if u, err := ValidateUnicastTo(f.UnicastTo); err == nil {
		out.UnicastTo = u
	} else {
		repaired = true
	}
	return out, repaired
}

func decodeCID(s string) ([16]byte, bool) {
	var cid [16]byte
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(cid) {
		return cid, false
	}
	copy(cid[:], b)
	return cid, true
}

// writeSettings persists s to path with the show file's discipline: the
// previous good file becomes path+".bak", and the new one lands via a synced
// temporary file and a rename, so a torn write never replaces a good file.
func writeSettings(path string, s Settings) error {
	data, err := json.MarshalIndent(settingsFile{
		CID:           hex.EncodeToString(s.CID[:]),
		StartUniverse: s.StartUniverse,
		Priority:      s.Priority,
		UnicastTo:     s.UnicastTo,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("sACN settings not saved: %w", err)
	}
	if previous, readErr := os.ReadFile(path); readErr == nil {
		if err := atomicWrite(path+".bak", previous); err != nil {
			return fmt.Errorf("sACN settings backup failed: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("sACN settings not saved: %w", readErr)
	}
	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("sACN settings not saved: %w", err)
	}
	return nil
}

// atomicWrite mirrors internal/patch/durable.go's atomicWrite: flush a
// sibling temporary file, then rename it over the destination.
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".benny-sacn-*")
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
