// Package walk implements Rig Walk mode's server-side session state: an
// ordered list of devices to physically walk, per-device verification
// status, and a simple JSON-file persistence layer so a dropped phone
// connection or accidental refresh doesn't lose progress (task ask:
// "server-side is preferable to browser storage since he may switch devices
// mid-walk").
//
// This package owns no RDM/HTTP knowledge — internal/web builds the device
// list from the registry, drives IDENTIFY_DEVICE and DMX_START_ADDRESS
// against the real controller, and wires this package's Store as the
// session model. Kept separate (mirrors internal/capture's pure ring
// buffer) so the session/status/summary/persistence logic is testable
// without a fake transport.
package walk

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Status is one walk-list device's verification state (task ask: "each
// device gets a status the user can set with big buttons: Confirmed /
// Problem (with an optional short note) / unvisited").
type Status string

// Verification statuses.
const (
	StatusUnvisited Status = "unvisited"
	StatusConfirmed Status = "confirmed"
	StatusProblem   Status = "problem"
)

// Order is how the walk list was sorted when the session was built (task
// ask: "let the user pick the walk order — by universe+DMX address
// ascending is the sensible default; also offer discovery order").
type Order string

// Walk orders.
const (
	OrderAddress   Order = "address"   // Port-Address (universe), then DMX start address ascending
	OrderDiscovery Order = "discovery" // order devices were first seen (ToD assembly order)
)

// Scope records what device set the session was built from, purely for
// display/export — the actual registry filtering happens once, at
// session-build time, in internal/web.
type Scope struct {
	Kind  string `json:"kind"`  // "all" | "node" | "port" | "universe" | "class"
	Label string `json:"label"` // human-readable, e.g. "Node 2.11.90.2" / "Universe (Port-Address) 3" / "Class Fixture"
}

// Device is one entry in a walk session: an identity snapshot (survives the
// device dropping off the network mid-walk, and lets an export be handed to
// the architect without a live server) plus mutable walk state.
type Device struct {
	UID             string `json:"uid"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	Class           string `json:"class"`
	IsWirelessProxy bool   `json:"isWirelessProxy"`
	NodeIP          string `json:"nodeIp"`
	BindIndex       byte   `json:"bindIndex"`
	PortAddress     uint16 `json:"portAddress"` // Art-Net Port-Address (the "universe" the owner walks by)
	DMXStartAddress uint16 `json:"dmxStartAddress"`
	DMXFootprint    uint16 `json:"dmxFootprint"`
	// AddressKnown is false when the device's DMX start address could not
	// be resolved while building the session (NACK/timeout — the fallback
	// path a wireless-proxied device is expected to exercise) — the UI
	// shows "—" rather than a misleading 0.
	AddressKnown bool `json:"addressKnown"`

	Status    Status    `json:"status"`
	Note      string    `json:"note,omitempty"`
	VisitedAt time.Time `json:"visitedAt,omitempty"`

	// IdentifyOn/IdentifyErr reflect the outcome of the most recent
	// IDENTIFY_DEVICE attempt for this device (set by internal/web after
	// every ON/OFF send) — an error here never blocks navigation (task
	// ask: "show it inline without blocking the walk"), it's just surfaced
	// so the UI can offer a retry affordance.
	IdentifyOn  bool   `json:"identifyOn"`
	IdentifyErr string `json:"identifyErr,omitempty"`
}

// Session is one Rig Walk run: an ordered device list plus cursor/settings.
type Session struct {
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Order        Order     `json:"order"`
	Scope        Scope     `json:"scope"`
	FixturesOnly bool      `json:"fixturesOnly"`
	Devices      []Device  `json:"devices"`
	// Current is the index of the device currently under the identify
	// spotlight, -1 if none (a fresh session before its first goto, or
	// after the "all off" escape hatch has been used).
	Current int `json:"current"`
	// AutoAdvance, when true, moves Current to the next device automatically
	// once the current device is marked Confirmed (task ask, with a toggle
	// to disable it).
	AutoAdvance bool `json:"autoAdvance"`
}

// IndexOf returns the index of the device with the given UID (as returned
// by rdm.UID.String()), or -1 if it isn't part of this session.
func (s Session) IndexOf(uid string) int {
	for i, d := range s.Devices {
		if d.UID == uid {
			return i
		}
	}
	return -1
}

// CurrentDevice returns the device at Current, if any.
func (s Session) CurrentDevice() (Device, bool) {
	if s.Current < 0 || s.Current >= len(s.Devices) {
		return Device{}, false
	}
	return s.Devices[s.Current], true
}

// Summary is the running-progress counts the UI's header shows (task ask:
// "18 confirmed, 2 problems, 28 remaining").
type Summary struct {
	Total     int `json:"total"`
	Confirmed int `json:"confirmed"`
	Problems  int `json:"problems"`
	Remaining int `json:"remaining"`
}

// Summary tallies this session's device statuses.
func (s Session) Summary() Summary {
	sum := Summary{Total: len(s.Devices)}
	for _, d := range s.Devices {
		switch d.Status {
		case StatusConfirmed:
			sum.Confirmed++
		case StatusProblem:
			sum.Problems++
		}
	}
	sum.Remaining = sum.Total - sum.Confirmed - sum.Problems
	return sum
}

// ErrNoSession is returned by every Store accessor/mutator below when no
// walk session is currently active.
var ErrNoSession = errors.New("walk: no active session")

// Store guards one live Session plus its on-disk persistence. Only one Rig
// Walk session is active at a time (Dom is one tech with one phone) —
// starting a new session (see Replace) discards whatever was active.
type Store struct {
	mu   sync.Mutex
	path string // empty disables persistence (e.g. unit tests that don't want file I/O)
	sess *Session
}

// NewStore builds a Store persisting to path (pass "" to disable
// persistence). If path already holds a valid session, it's loaded
// immediately so a server restart resumes wherever the tech left off —
// mirrors the "dropped connection or accidental refresh doesn't lose it"
// requirement one level further, across a process restart too.
func NewStore(path string) *Store {
	st := &Store{path: path}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var sess Session
			if json.Unmarshal(data, &sess) == nil {
				st.sess = &sess
			}
		}
	}
	return st
}

// Get returns a defensive copy of the active session, or ok=false if none.
func (st *Store) Get() (Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sess == nil {
		return Session{}, false
	}
	return cloneSession(*st.sess), true
}

// Replace installs a brand new session (CreatedAt/UpdatedAt stamped here),
// discarding whatever was active, and persists it.
func (st *Store) Replace(sess Session) Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	sess.CreatedAt, sess.UpdatedAt = now, now
	st.sess = &sess
	st.persistLocked()
	return cloneSession(*st.sess)
}

// Clear discards the active session; no on-disk file is left behind either,
// so a subsequent restart doesn't resurrect a walk the owner explicitly
// ended.
func (st *Store) Clear() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sess = nil
	if st.path != "" {
		_ = os.Remove(st.path)
	}
}

// Mutate runs fn against the live session under the store's lock, stamping
// UpdatedAt and persisting afterward if fn returns nil. This is the single
// choke point every handler-level mutation (status, cursor, auto-advance
// toggle, address fix, identify outcome) goes through, so UpdatedAt/
// persistence never drifts out of sync across call sites. fn must not
// perform slow I/O (RDM round-trips) — callers do that before/after
// Mutate, never inside it, so one slow wireless-proxied device can't stall
// every other Rig Walk API call.
func (st *Store) Mutate(fn func(*Session) error) (Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sess == nil {
		return Session{}, ErrNoSession
	}
	if err := fn(st.sess); err != nil {
		return Session{}, err
	}
	st.sess.UpdatedAt = time.Now()
	st.persistLocked()
	return cloneSession(*st.sess), nil
}

// persistLocked writes the active session to st.path via a temp-file +
// rename so a process death mid-write can never leave a half-written,
// unparseable session file behind (the same failure mode Replace/Mutate
// exist to protect against in the first place). Best-effort: a write
// failure (e.g. a locked/read-only path) never blocks the in-memory
// session from continuing to work.
func (st *Store) persistLocked() {
	if st.path == "" || st.sess == nil {
		return
	}
	data, err := json.MarshalIndent(st.sess, "", "  ")
	if err != nil {
		return
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, st.path)
}

func cloneSession(s Session) Session {
	cp := s
	cp.Devices = append([]Device(nil), s.Devices...)
	return cp
}
