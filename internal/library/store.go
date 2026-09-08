package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"
)

// Store guards one Library plus its on-disk persistence. It follows
// internal/patch.Store's pattern deliberately — same mutex-per-store
// discipline, same tmp+rename atomic write, same tolerant load-on-start,
// same migrate hook — with two intentional differences, both forced by
// what a library IS rather than by taste:
//
//  1. There is no "no library yet" state. patch.Store can hold a nil patch
//     (nothing has been created), which is why every one of its mutators
//     has to reckon with ErrNoPatch. An empty library is a perfectly good
//     library, so this store always holds a valid, non-nil Library and
//     Get can never fail. That removes a whole error path from every
//     caller.
//  2. There is no Clear(). patch.Store.Clear exists exclusively so the
//     full reset can wipe the patch; the library is explicitly EXEMPT from
//     reset (see the package doc comment), so the method that would let
//     reset wipe it is simply not offered. That is the exemption enforced
//     structurally: internal/web/reset.go cannot delete this store's
//     contents because this package exposes no way to.
type Store struct {
	mu        sync.Mutex
	path      string // empty disables persistence (tests, and the default server wiring)
	lib       *Library
	deferSave bool
	saveErr   error
}

// NewStore builds a Store persisting to path (pass "" for in-memory only).
// If path holds a valid (or migratable) library file, it is loaded
// immediately. A malformed or wrong-format file is tolerated by starting
// empty rather than crashing the server, and is left on disk untouched —
// never overwritten by subsequent writes — so it can be recovered by hand.
func NewStore(path string) *Store {
	st := &Store{path: path, lib: newLibrary()}
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var lib Library
	// Tolerant reader, exactly as patch.NewStore: encoding/json's default
	// Unmarshal ignores unknown fields and leaves absent ones zero. No
	// DisallowUnknownFields here — that strictness belongs on a live API
	// request body (internal/web's decodeJSON), not on a file that must
	// always open.
	if json.Unmarshal(data, &lib) != nil {
		return st
	}
	if err := validate(&lib); err != nil {
		// A file that is JSON but is not one of ours (a patch export, say)
		// must not become the library. Start empty; leave the file alone.
		return st
	}
	migrate(&lib)
	st.lib = &lib
	return st
}

func newLibrary() *Library {
	now := time.Now()
	return &Library{
		Format:        FileFormat,
		SchemaVersion: CurrentSchemaVersion,
		CreatedAt:     now,
		ModifiedAt:    now,
		Records:       make([]Record, 0),
	}
}

// ErrBadFormat is returned when a document's "format" is not FileFormat.
var ErrBadFormat = errors.New("not a Benny512 fixture library file (missing or wrong \"format\")")

// ErrUnsupportedSchema is returned when a document's schemaVersion is newer
// than this build understands. Older versions are migrated, never rejected.
var ErrUnsupportedSchema = errors.New("library file schema is newer than this build understands")

// validate checks a decoded document's envelope and contents WITHOUT
// mutating any store. Import calls this before touching anything, which is
// what makes "reject a malformed file rather than half-import it"
// structurally true rather than a promise.
func validate(lib *Library) error {
	if lib.Format != FileFormat {
		return ErrBadFormat
	}
	if lib.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: file is schema %d, this build understands up to %d", ErrUnsupportedSchema, lib.SchemaVersion, CurrentSchemaVersion)
	}
	seen := make(map[string]int, len(lib.Records))
	for i, r := range lib.Records {
		if err := validateSourceFiles(r.SourceFiles); err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
		if foldKeyPart(r.Manufacturer) == "" && foldKeyPart(r.Model) == "" {
			return fmt.Errorf("record %d has neither a manufacturer nor a model", i)
		}
		k := KeyFor(r.Manufacturer, r.Model)
		if prev, dup := seen[k]; dup {
			return fmt.Errorf("records %d and %d are both %q / %q — a library holds one record per manufacturer+model", prev, i, r.Manufacturer, r.Model)
		}
		seen[k] = i
	}
	return nil
}

// migrate upgrades lib in place to CurrentSchemaVersion and enforces this
// package's non-nil invariants on everything that came off disk or out of
// an imported document. There is only one schema version today; the hook
// exists so a future reshape has a single tested choke point, the same
// reason patch.migrate exists.
func migrate(lib *Library) {
	if lib.SchemaVersion <= 0 {
		lib.SchemaVersion = 1
	}
	lib.Format = FileFormat
	if lib.Records == nil {
		lib.Records = make([]Record, 0)
	}
	for i := range lib.Records {
		lib.Records[i] = normalizeRecord(lib.Records[i])
	}
	sortRecords(lib.Records)
	// Future: switch lib.SchemaVersion { case 1: ...; lib.SchemaVersion = 2 }
	lib.SchemaVersion = CurrentSchemaVersion
}

func sortRecords(rs []Record) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Key < rs[j].Key })
}

// Get returns a deep copy of the whole library. Never fails — see the
// Store doc comment's point 1.
func (st *Store) Get() Library {
	st.mu.Lock()
	defer st.mu.Unlock()
	return cloneLibrary(*st.lib)
}

// Export returns the library as the document to hand to a coworker: the
// same struct Get returns, restamped with GeneratedAt/AppVersion so a
// shared file self-identifies which build wrote it and when.
func (st *Store) Export(appVersion string, now time.Time) Library {
	lib := st.Get()
	lib.Format = FileFormat
	lib.SchemaVersion = CurrentSchemaVersion
	lib.AppVersion = appVersion
	lib.GeneratedAt = now
	return lib
}

// List returns a deep copy of every record, sorted by Key. Never nil.
func (st *Store) List() []Record {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Record, len(st.lib.Records))
	for i, r := range st.lib.Records {
		out[i] = cloneRecord(r)
	}
	return out
}

// GetByKey returns the record with exactly this key.
func (st *Store) GetByKey(key string) (Record, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if i := st.indexOfKeyLocked(key); i >= 0 {
		return cloneRecord(st.lib.Records[i]), true
	}
	return Record{}, false
}

// Find returns the record matching manufacturer+model tolerantly (see
// Record.Matches) — the lookup a GDTF import or a live RDM discovery does
// to ask "do I already know this fixture type?". An exact key hit always
// wins over a fuzzy one.
func (st *Store) Find(manufacturer, model string) (Record, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if i := st.matchLocked(manufacturer, model); i >= 0 {
		return cloneRecord(st.lib.Records[i]), true
	}
	return Record{}, false
}

func (st *Store) indexOfKeyLocked(key string) int {
	for i, r := range st.lib.Records {
		if r.Key == key {
			return i
		}
	}
	return -1
}

// matchLocked resolves manufacturer+model to a record index: exact key
// first (cheap and unambiguous), tolerant match second.
func (st *Store) matchLocked(manufacturer, model string) int {
	if i := st.indexOfKeyLocked(KeyFor(manufacturer, model)); i >= 0 {
		return i
	}
	for i, r := range st.lib.Records {
		if r.Matches(manufacturer, model) {
			return i
		}
	}
	return -1
}

// Upsert merges rec into the library, matching an existing record
// tolerantly (so re-importing "GLP JDC1 Strobe" updates the "GLP / JDC-1"
// record rather than creating a near-duplicate). Returns the resulting
// stored record and what happened to it.
//
// Merge rules, all of them "never lose what is already known":
//
//   - Identity spelling: the EXISTING manufacturer/model wins, because the
//     record's key is derived from it and silently re-keying a record
//     under the owner's feet would orphan every UI reference to it. An
//     empty existing field is filled in from the incoming one.
//   - RDM IDs: an incoming KNOWN id replaces whatever was there (a live
//     device is authoritative about its own numbers); an incoming unknown
//     id never erases a known one.
//   - SupportedPIDs: union, ascending. PIDs are only ever added — a device
//     that did not answer SUPPORTED_PARAMETERS tonight has not lost the
//     PIDs it answered with last month.
//   - Modes: matched by case-folded name. An incoming mode replaces the
//     existing one of the same name, EXCEPT that an incoming empty channel
//     map never erases an existing populated one (an RDM-observed mode
//     knows its footprint but not its channel layout, and must not wipe
//     the layout a GDTF import already supplied).
func (st *Store) Upsert(rec Record) (Record, UpsertOutcome) {
	r, outcome, _ := st.UpsertChecked(rec)
	return r, outcome
}

// UpsertChecked rolls back in-memory changes if saving fails.
func (st *Store) UpsertChecked(rec Record) (Record, UpsertOutcome, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := validateSourceFiles(rec.SourceFiles); err != nil {
		return Record{}, OutcomeUnchanged, err
	}
	before := cloneLibrary(*st.lib)
	st.saveErr = nil
	r, outcome := st.upsertLocked(rec, time.Now())
	if st.saveErr != nil {
		st.lib = &before
		return Record{}, OutcomeUnchanged, st.saveErr
	}
	return r, outcome, nil
}

// UpsertOutcome says what Upsert did.
type UpsertOutcome string

// Upsert outcomes.
const (
	// OutcomeAdded: no existing record matched; a new one was stored.
	OutcomeAdded UpsertOutcome = "added"
	// OutcomeUpdated: an existing record matched and gained something.
	OutcomeUpdated UpsertOutcome = "updated"
	// OutcomeUnchanged: an existing record matched and the merge produced
	// a byte-identical record, so nothing was written and UpdatedAt was
	// left alone. Reported to the user as "skipped" on import — silently
	// bumping a timestamp for a no-op would make every re-import of the
	// same file look like it did work.
	OutcomeUnchanged UpsertOutcome = "unchanged"
)

func (st *Store) upsertLocked(rec Record, now time.Time) (Record, UpsertOutcome) {
	rec = normalizeRecord(cloneRecord(rec))
	i := st.matchLocked(rec.Manufacturer, rec.Model)
	if i < 0 {
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now
		}
		rec.UpdatedAt = now
		st.lib.Records = append(st.lib.Records, rec)
		sortRecords(st.lib.Records)
		st.touchLocked(now)
		return cloneRecord(rec), OutcomeAdded
	}
	before := st.lib.Records[i]
	merged := mergeRecord(before, rec)
	if recordsEquivalent(before, merged) {
		return cloneRecord(before), OutcomeUnchanged
	}
	merged.UpdatedAt = now
	st.lib.Records[i] = merged
	st.touchLocked(now)
	return cloneRecord(merged), OutcomeUpdated
}

// recordsEquivalent compares two records ignoring UpdatedAt (which the
// merge is about to stamp) — the "did this actually change anything?"
// question Upsert answers with OutcomeUnchanged.
func recordsEquivalent(a, b Record) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func mergeRecord(dst, src Record) Record {
	out := cloneRecord(dst)
	out.SourceFiles = mergeSourceFiles(out.SourceFiles, src.SourceFiles)
	if foldKeyPart(out.Manufacturer) == "" && foldKeyPart(src.Manufacturer) != "" {
		out.Manufacturer = src.Manufacturer
	}
	if foldKeyPart(out.Model) == "" && foldKeyPart(src.Model) != "" {
		out.Model = src.Model
	}
	out.Key = KeyFor(out.Manufacturer, out.Model)
	if src.IdentityOrigin.Source != ProvenanceUnknown && out.IdentityOrigin.Source == ProvenanceUnknown {
		out.IdentityOrigin = src.IdentityOrigin
	}
	if src.RDMManufacturerIDKnown {
		out.RDMManufacturerID, out.RDMManufacturerIDKnown = src.RDMManufacturerID, true
	}
	if src.RDMDeviceModelIDKnown {
		out.RDMDeviceModelID, out.RDMDeviceModelIDKnown = src.RDMDeviceModelID, true
	}
	if src.RDMManufacturerIDKnown || src.RDMDeviceModelIDKnown {
		if src.RDMIdentityOrigin.Source != ProvenanceUnknown {
			out.RDMIdentityOrigin = src.RDMIdentityOrigin
		}
	}
	if len(src.SupportedPIDs) > 0 {
		out.SupportedPIDs = dedupePIDs(append(out.SupportedPIDs, src.SupportedPIDs...))
		if src.PIDOrigin.Source != ProvenanceUnknown {
			out.PIDOrigin = src.PIDOrigin
		}
	}
	out.Modes = mergeModes(out.Modes, src.Modes)
	if out.CreatedAt.IsZero() {
		out.CreatedAt = src.CreatedAt
	}
	return out
}

// mergeModes folds src's modes into dst's, matching case-insensitively on
// name. See Upsert's doc comment for the "an empty incoming channel map
// never erases a populated one" rule.
func mergeModes(dst, src []Mode) []Mode {
	out := append(make([]Mode, 0, len(dst)+len(src)), dst...)
	for _, sm := range src {
		sm = normalizeMode(sm)
		found := -1
		for i, dm := range out {
			if foldKeyPart(dm.Name) == foldKeyPart(sm.Name) {
				found = i
				break
			}
		}
		if found < 0 {
			out = append(out, sm)
			continue
		}
		merged := sm
		if len(sm.ChannelFunctions) == 0 && len(out[found].ChannelFunctions) > 0 {
			merged.ChannelFunctions = out[found].ChannelFunctions
			// The mode's origin describes its channel map, so keep the
			// origin of the map that survived.
			merged.Origin = out[found].Origin
		} else if sameModePayload(out[found], merged) && out[found].Origin.Source == merged.Origin.Source {
			// RE-OBSERVING THE SAME DATA IS NOT NEW DATA. When the incoming
			// mode's payload (footprint + channel map) is byte-identical to
			// the stored one AND came from the same kind of source, keep the
			// stored Origin — timestamp included — rather than restamping
			// it. Without this, any producer that stamps Origin.At with
			// time.Now() (POST /api/library/from-patch does, and so would a
			// live RDM observer) makes every repeat of an idempotent
			// operation come back OutcomeUpdated, which is precisely the
			// "silently bumping a timestamp for a no-op would make every
			// re-import of the same file look like it did work" failure
			// OutcomeUnchanged exists to prevent. The Source equality guard
			// keeps this from hiding a genuine provenance change (the same
			// channel map newly corroborated by a GDTF file rather than by
			// RDM observation is a real update, and still reports as one).
			merged.Origin = out[found].Origin
		}
		if merged.Name == "" {
			merged.Name = out[found].Name
		}
		if sameModePayload(out[found], merged) && merged.VerifiedHash == "" {
			merged.VerifiedHash = out[found].VerifiedHash
			merged.VerifiedAt = out[found].VerifiedAt
			merged.VerificationNote = out[found].VerificationNote
		}
		if merged.VerifiedHash != "" && merged.VerifiedHash != modeHash(merged) {
			merged.VerifiedHash = ""
			merged.VerifiedAt = time.Time{}
			merged.VerificationNote = ""
		}
		out[found] = merged
	}
	sort.SliceStable(out, func(i, j int) bool { return foldKeyPart(out[i].Name) < foldKeyPart(out[j].Name) })
	return out
}

// sameModePayload compares the two halves of a Mode that describe the
// FIXTURE rather than the bookkeeping — name, footprint and channel map —
// ignoring Origin entirely. See its call site in mergeModes.
func sameModePayload(a, b Mode) bool {
	a.Origin, b.Origin = Origin{}, Origin{}
	a.VerifiedAt, b.VerifiedAt = time.Time{}, time.Time{}
	a.VerifiedHash, b.VerifiedHash = "", ""
	a.VerificationNote, b.VerificationNote = "", ""
	return reflect.DeepEqual(a, b)
}

// Delete removes the record with exactly this key. Reports whether
// anything was removed (a delete of a key that is already gone is not an
// error — it is the state the caller wanted).
func (st *Store) Delete(key string) bool {
	deleted, _ := st.DeleteChecked(key)
	return deleted
}

func (st *Store) DeleteChecked(key string) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	i := st.indexOfKeyLocked(key)
	if i < 0 {
		return false, nil
	}
	before := cloneLibrary(*st.lib)
	st.lib.Records = append(st.lib.Records[:i], st.lib.Records[i+1:]...)
	st.touchLocked(time.Now())
	if st.saveErr != nil {
		st.lib = &before
		return false, st.saveErr
	}
	return true, nil
}

func (st *Store) touchLocked(now time.Time) {
	st.lib.ModifiedAt = now
	st.lib.SchemaVersion = CurrentSchemaVersion
	st.lib.Format = FileFormat
	if st.lib.Records == nil {
		st.lib.Records = make([]Record, 0)
	}
	st.persistLocked()
}

// persistLocked writes the library with the same tmp+rename atomic-write
// discipline patch.Store uses: a torn write can never leave the library
// half-written, which matters more here than for a patch (the patch is
// this show; the library is every show).
func (st *Store) persistLocked() {
	if st.deferSave {
		return
	}
	st.saveErr = st.saveLocked()
}
func (st *Store) saveLocked() error {
	if st.path == "" || st.lib == nil {
		return nil
	}
	if old, err := os.ReadFile(st.path); err == nil {
		var previous Library
		if json.Unmarshal(old, &previous) != nil || validate(&previous) != nil {
			return fmt.Errorf("library file is damaged; back it up and restore a valid export before saving")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(st.lib, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(st.path), ".benny-library-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, st.path)
}

func cloneLibrary(lib Library) Library {
	out := lib
	out.Records = make([]Record, len(lib.Records))
	for i, r := range lib.Records {
		out.Records[i] = cloneRecord(r)
	}
	return out
}
