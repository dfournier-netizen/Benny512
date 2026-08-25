// Package patch implements Benny512's Phase 2a patch model: an ordered list
// of patch entries (what the owner INTENDS to have on the rig — name,
// fixture type, universe/address, footprint, position, console fixture
// number), JSON persistence beside the exe, collision detection (overlapping
// channel ranges, footprint overflow, duplicate fixture numbers, invalid
// footprints), a tiered confidence-scored matcher that reconciles patch
// entries against live RDM-discovered devices (architecture doc §3.1), and a
// channel-level rig-check sequencer that drives DMXOutputEngine (architecture
// doc §3.2).
//
// Layering rule (mirrors internal/walk and internal/capture): entry.go,
// collision.go and match.go are pure — stdlib only, no RDM/HTTP/artnet
// dependency, fully unit-testable without any network or session plumbing.
// rigcheck.go is the one file in this package that reaches into
// internal/session/internal/artnet to actually drive DMX output; internal/web
// is the only caller that knows about HTTP, and it is the only caller that
// resolves a patch entry's ConfirmedUID to a real rdm.UID or a live
// session.NodeRef.
//
// Scope note: CSV/MVR import is explicitly out of scope for Phase 2a (Dom
// deprioritized it). The Entry/Patch shape below is deliberately import-
// agnostic — every field is a plain string/number, nothing here assumes RDM
// or a particular file format — so a future importer (CSV, MVR, GDTF) is
// just another producer of []Entry that slots in beside AdoptFromDiscovered
// without this package changing shape.
//
// Persistence choice: ONE active patch per install (mirrors internal/walk's
// "Dom is one tech with one phone, one active session" precedent — here,
// one tech with one active show patch at a time). Supporting multiple named
// patches would mean threading a patch-name selector through every REST
// endpoint and the whole UI for a feature nobody asked for yet; the Patch
// struct's Name field and SchemaVersion leave room to grow into that later
// without a breaking file-format change.
package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// CurrentSchemaVersion is stamped onto every Patch this package writes.
// migrate() below upgrades any older (or missing/zero) version on load —
// "old files must always open" is a hard rule carried over from the
// project's other codebase (see the file's package doc comment and
// CLAUDE.md's "tolerant reader" convention).
const CurrentSchemaVersion = 1

// MatchState records a patch entry's reconciliation state, persisted so a
// user-confirmed pairing is never re-litigated across sessions (task ask:
// "confirmed pairings persist in the patch file so re-matching is instant
// next session").
type MatchState string

// Match states.
const (
	// MatchStateUnresolved is the zero value: no confirmed pairing. The
	// entry participates in automatic tiered matching on every Reconcile
	// call.
	MatchStateUnresolved MatchState = ""
	// MatchStateConfirmed means ConfirmedUID has been set (either
	// automatically, for a high-confidence Tier-1 address match promoted by
	// the caller, or explicitly by the user via a Tier-3 manual confirm) and
	// should be trusted ahead of any fresh scoring.
	MatchStateConfirmed MatchState = "confirmed"
	// MatchStateRejected means the user explicitly dismissed every proposed
	// candidate for this entry (Tier-3: "none of these are right") — kept
	// distinct from MatchStateUnresolved so Reconcile doesn't keep
	// re-proposing the same rejected candidate on every call. A rejected
	// entry still participates in scoring (in case new devices appear) but
	// its previous top candidate is not treated as a repeat suggestion by
	// the UI layer.
	MatchStateRejected MatchState = "rejected"
)

// Entry is one patch entry: what the owner intends to have at a given
// universe/address, plus (once reconciled) which live RDM device answers
// for it. Every field is optional/zero-valuable — a hand-entered patch
// starts sparse and fills in over time (task ask: "tolerant reader — every
// field optional").
type Entry struct {
	// ID is a stable identifier assigned once at creation (see NewEntryID)
	// and never reused — every other reference to this entry (collision
	// findings, reconcile rows, rig-check ordering) is by ID, so reordering
	// or renaming an entry never breaks a cross-reference.
	ID string `json:"id"`

	Name string `json:"name,omitempty"`
	// FixtureType is free text, typically "Manufacturer Model" (e.g.
	// "Chauvet Rogue Outcast 2X Wash") but tolerated as just a model name —
	// the matcher's fuzzy comparison (see match.go) is token-based
	// specifically so either shape works.
	FixtureType string `json:"fixtureType,omitempty"`
	// Mode is the personality/mode name (DMX_PERSONALITY's human label),
	// independent of FixtureType/Footprint since one fixture type can have
	// several modes with different footprints.
	Mode string `json:"mode,omitempty"`
	// Footprint is the number of DMX channels this entry occupies, starting
	// at StartAddress. Zero is tolerated (a splitter/gateway/data device) —
	// see DetectCollisions for how zero-footprint entries are flagged
	// without being treated as a hard error.
	Footprint uint16 `json:"footprint,omitempty"`
	// Universe is the Art-Net Port-Address (raw value, 0-32767) this entry
	// is patched into — matches the vocabulary/type every other screen in
	// this app already uses for "universe" (see internal/walk.Device.
	// PortAddress).
	Universe uint16 `json:"universe,omitempty"`
	// StartAddress is the 1-based DMX slot this entry starts at.
	StartAddress uint16 `json:"startAddress,omitempty"`
	// Position is a free-text location label (e.g. "US Truss 3", "SR Boom").
	Position string `json:"position,omitempty"`
	// FixtureNumber is the console channel/FixtureID concept — free text
	// (consoles vary: "101", "1.01", "A12") rather than numeric, per task
	// ask.
	FixtureNumber string `json:"fixtureNumber,omitempty"`
	Notes         string `json:"notes,omitempty"`

	// --- reconciliation state (persisted so it's never re-litigated) ---

	// ConfirmedUID is the RDM UID (string form, e.g. "1900:00000042") this
	// entry has been confirmed paired to, or "" if unpaired. Kept as a
	// plain string (not rdm.UID) so this package stays free of any RDM
	// dependency — internal/web parses/formats it at the boundary.
	ConfirmedUID string     `json:"confirmedUid,omitempty"`
	MatchState   MatchState `json:"matchState,omitempty"`
}

// entryIDCounter guarantees NewEntryID uniqueness even when called twice
// within the same nanosecond (observed as flaky on fast CI-like sandboxes
// that call it in a tight loop, e.g. AdoptFromDiscovered building many
// entries back to back).
var entryIDCounter uint64

// NewEntryID returns a fresh, stable, never-reused entry ID — every REST
// handler that creates an entry (manual add, adopt-from-discovered) calls
// this rather than letting the client supply an ID, so IDs stay an internal
// implementation detail the UI never has to invent or validate.
func NewEntryID() string {
	n := atomic.AddUint64(&entryIDCounter, 1)
	return fmt.Sprintf("e%d-%d", time.Now().UnixNano(), n)
}

// EndAddress returns the last DMX slot this entry occupies (StartAddress +
// Footprint - 1). Only meaningful when Footprint > 0; callers should check
// that first (mirrors internal/walk.FormatAddressRange's footprint-0
// handling).
func (e Entry) EndAddress() int {
	if e.Footprint == 0 {
		return int(e.StartAddress)
	}
	return int(e.StartAddress) + int(e.Footprint) - 1
}

// Patch is an ordered list of entries plus metadata. Order is significant —
// it's the default rig-check walk order and the default Patch-screen table
// order (task ask: "ordered entries").
type Patch struct {
	SchemaVersion int       `json:"schemaVersion,omitempty"`
	Name          string    `json:"name,omitempty"`
	CreatedAt     time.Time `json:"createdAt,omitempty"`
	ModifiedAt    time.Time `json:"modifiedAt,omitempty"`
	Entries       []Entry   `json:"entries,omitempty"`
}

// migrate upgrades p in place to CurrentSchemaVersion. There is only one
// schema version today; this function exists so a future field rename/
// reshape has a single, tested choke point rather than ad hoc version
// checks scattered through the codebase (the "old files must always open"
// lesson this package's doc comment credits).
func migrate(p *Patch) {
	if p.SchemaVersion <= 0 {
		p.SchemaVersion = 1
	}
	// Future: switch p.SchemaVersion { case 1: ...; p.SchemaVersion = 2 }
	p.SchemaVersion = CurrentSchemaVersion
}

// IndexOf returns the index of the entry with the given ID, or -1.
func (p Patch) IndexOf(id string) int {
	for i, e := range p.Entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// clone returns a deep-enough copy for safe hand-out across the mutex
// boundary.
func clonePatch(p Patch) Patch {
	cp := p
	cp.Entries = append([]Entry(nil), p.Entries...)
	return cp
}

// --- persistence -----------------------------------------------------------

// Store guards one active Patch plus its on-disk persistence — same
// tmp+rename atomic-write pattern as internal/walk.Store, same "only one
// active at a time" model (see package doc comment for why).
type Store struct {
	mu    sync.Mutex
	path  string // empty disables persistence (tests)
	patch *Patch
}

// NewStore builds a Store persisting to path (pass "" to disable
// persistence). If path holds a valid (or migratable) patch file, it's
// loaded immediately. A malformed file is tolerated by starting empty rather
// than crashing the server — the corrupt file is left on disk untouched
// (never silently overwritten) so it can be inspected/recovered by hand.
func NewStore(path string) *Store {
	st := &Store{path: path}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var p Patch
			// encoding/json's default Unmarshal already ignores unknown
			// fields and leaves absent fields at their zero value — that IS
			// the "tolerant reader" (task ask), no DisallowUnknownFields
			// call here (unlike decodeJSON's request-body path in
			// internal/web, which deliberately wants strictness for typos
			// in a live API call, not for a file that must always open).
			if json.Unmarshal(data, &p) == nil {
				migrate(&p)
				st.patch = &p
			}
		}
	}
	return st
}

// Get returns a defensive copy of the active patch, or ok=false if none has
// ever been created/loaded.
func (st *Store) Get() (Patch, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.patch == nil {
		return Patch{}, false
	}
	return clonePatch(*st.patch), true
}

// EnsureActive returns the active patch, creating an empty one (stamped
// with CreatedAt/ModifiedAt now) if none exists yet — the lazy-init path for
// "add my first entry" without a separate explicit "create a patch" step.
func (st *Store) EnsureActive() Patch {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.patch == nil {
		now := time.Now()
		st.patch = &Patch{SchemaVersion: CurrentSchemaVersion, Name: "Patch", CreatedAt: now, ModifiedAt: now}
		st.persistLocked()
	}
	return clonePatch(*st.patch)
}

// Replace installs p as the active patch (CreatedAt preserved if already
// set and non-zero, else stamped now; ModifiedAt always stamped now),
// discarding whatever was active, and persists it. Used by fresh-create
// (manual "start a new patch") and AdoptFromDiscovered's fresh-create mode.
func (st *Store) Replace(p Patch) Patch {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.ModifiedAt = now
	p.SchemaVersion = CurrentSchemaVersion
	st.patch = &p
	st.persistLocked()
	return clonePatch(*st.patch)
}

// ErrNoPatch is returned by Mutate when no patch is active. Callers that
// want lazy-init semantics should call EnsureActive first (or use Mutate
// only after EnsureActive/Replace has run at least once).
var ErrNoPatch = errNoPatch{}

type errNoPatch struct{}

func (errNoPatch) Error() string { return "patch: no active patch" }

// Mutate runs fn against the live patch under the store's lock, stamping
// ModifiedAt and persisting afterward if fn returns nil — same choke-point
// pattern as internal/walk.Store.Mutate, for the same reason (every
// handler-level mutation goes through one place so ModifiedAt/persistence
// never drifts out of sync, and fn must not perform slow I/O).
func (st *Store) Mutate(fn func(*Patch) error) (Patch, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.patch == nil {
		return Patch{}, ErrNoPatch
	}
	if err := fn(st.patch); err != nil {
		return Patch{}, err
	}
	st.patch.ModifiedAt = time.Now()
	st.persistLocked()
	return clonePatch(*st.patch), nil
}

// Clear discards the active patch (if any) and removes the on-disk file —
// mirrors internal/walk.Store.Clear exactly, for the same reason: the
// full-reset flow's "everything, including the patch" (task ask) must not
// leave a stale file for the next server start to accidentally resurrect.
func (st *Store) Clear() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.patch = nil
	if st.path != "" {
		_ = os.Remove(st.path)
	}
}

func (st *Store) persistLocked() {
	if st.path == "" || st.patch == nil {
		return
	}
	data, err := json.MarshalIndent(st.patch, "", "  ")
	if err != nil {
		return
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, st.path)
}
