// Package library implements Benny512's Fixture Library: a persistent,
// rig-independent store of everything that is generic to a DEVICE TYPE
// (manufacturer + model), so a fixture that has already been characterised
// once — by importing its GDTF, or by observing it live over RDM — never
// has to be characterised again on the next show.
//
// Why this package exists, and how it differs from internal/patch:
//
//	internal/patch  = THIS RIG, THIS SHOW. Where a fixture is patched,
//	                  what it is called on the console, which live RDM UID
//	                  answers for it. One patch file per rig; the owner
//	                  saves/loads a different one per job.
//	internal/library = THIS FIXTURE TYPE, EVERYWHERE. Manufacturer, model,
//	                  RDM identity numbers, which PIDs the type supports,
//	                  and each DMX mode's footprint + channel map. One
//	                  library file, shared across every rig, persisting
//	                  underneath whichever patch happens to be loaded.
//
// Keying (owner decision, not open for redesign): ONE record per
// manufacturer + model, with that type's DMX modes NESTED INSIDE it — not
// one record per (type, mode) pair. A Robe iForte is one library entry that
// happens to have eleven modes, not eleven library entries.
//
// Layering rule (mirrors internal/patch's own): this package is pure —
// stdlib plus internal/patch's model types only. No HTTP, no RDM, no
// session/artnet. internal/web is the only caller that knows about HTTP.
// It imports internal/patch solely to REUSE patch.ChannelFunction (a mode's
// per-offset channel map is field-for-field the same thing a patch entry's
// ChannelFunctions map holds — defining a parallel type here would
// guarantee the two drift) and to reuse patch's fixture-name matching (see
// match.go in this package for the important caveat there).
//
// Reset exemption (explicit owner requirement): the full "wipe everything"
// reset in internal/web/reset.go must NOT clear this store. The library is
// the one thing the owner accumulates across jobs and would be furious to
// lose; a reset is about clearing THIS rig's state, and a fixture type's
// channel map is not this rig's state. See internal/web/library.go's
// libraryStores comment for how that exemption is enforced structurally
// rather than by remembering not to add a line to handleReset.
package library

import (
	"strings"
	"time"

	"benny512/internal/patch"
)

// CurrentSchemaVersion is stamped onto every Library this package writes.
// migrate() (store.go) upgrades any older or missing version on load —
// "old files must always open" is the same hard rule internal/patch's
// package doc comment states, and it matters more here than there: a
// library file is a thing the owner HANDS TO A COWORKER (see Export), so a
// file written by an older or newer build is an ordinary, expected event,
// not a corruption.
const CurrentSchemaVersion = 1

// FileFormat is the self-identifying "format" string every exported library
// document carries and every import is checked against. A JSON document
// with no format key, or a different one, is rejected outright rather than
// half-imported: without this, handing someone the wrong .json (a patch
// export, say — same folder, same extension, similar name) would import as
// a valid-looking empty library and silently replace their real one.
const FileFormat = "benny512-fixture-library"

// Provenance records where one piece of a library record came from. The
// owner's rule (carried over from patch.ChannelFunctionSource, which draws
// exactly this distinction for the same reason) is that authoritative data
// must be structurally impossible to confuse with observed/inferred data:
// a GDTF file states a fixture's channel layout, whereas watching a fixture
// answer RDM only tells you what that one unit did on that one night.
type Provenance string

// Provenances. ProvenanceUnknown is the zero value and means the writer
// never said — real producers always set one of the others explicitly.
const (
	ProvenanceUnknown Provenance = ""
	// ProvenanceGDTF: read out of a GDTF fixture-type definition.
	ProvenanceGDTF Provenance = "gdtf"
	// ProvenanceRDM: observed live from a device over RDM.
	ProvenanceRDM Provenance = "rdm-observed"
	// ProvenanceManual: typed in by a human.
	ProvenanceManual Provenance = "manual"
)

// Origin is the provenance stamp attached to each independently-sourced
// piece of a Record — "where this came from and when". Deliberately a
// per-piece field rather than one per record: a type's identity commonly
// comes from a GDTF import while its supported-PID list can only ever come
// from live RDM observation, and a single record-level "source: gdtf" would
// be a lie about half the record.
type Origin struct {
	// Source is never omitted from the JSON (no `omitempty`): an absent
	// source sitting next to real data is exactly the silent-guess failure
	// mode Provenance exists to prevent — every consumer must be able to
	// read Source before trusting what it is stamped on.
	Source Provenance `json:"source"`
	// Detail is free text naming the specific origin — a GDTF filename, an
	// RDM UID, "hand-entered". Genuinely optional, so `omitempty` is fine.
	Detail string `json:"detail,omitempty"`
	// At is when this piece was recorded. A zero time means "never
	// stamped"; time.Time is a struct, so `omitempty` would do nothing here
	// anyway and is deliberately not written.
	At time.Time `json:"at"`
}

// Mode is one DMX personality of a fixture type: its name, how many DMX
// slots it occupies, and what each of those slots does.
type Mode struct {
	VerifiedAt       time.Time `json:"verifiedAt"`
	VerificationNote string    `json:"verificationNote"`
	VerifiedHash     string    `json:"verifiedHash"`
	// Name is the personality/mode name as the manufacturer spells it
	// ("Standard", "Extended 32ch"). Matched case-insensitively when
	// merging (see mergeModes) so a re-import with different casing
	// updates the existing mode rather than duplicating it.
	Name string `json:"name"`
	// Footprint is the mode's DMX channel count. Deliberately NO
	// `omitempty`: footprint 0 is real, meaningful data here for exactly
	// the reason patch.Entry.Footprint documents (a data-only device, or a
	// GDTF import that resolved no DMX channels) and this codebase has
	// been bitten three times by a zero erased from the wire. An absent
	// key still unmarshals to 0, so dropping `omitempty` costs nothing for
	// old files and only fixes what a PRESENT zero looks like.
	Footprint uint16 `json:"footprint"`
	// ChannelFunctions is the per-DMX-offset channel map, keyed by
	// 1-based offset WITHIN THIS MODE'S FOOTPRINT — the identical
	// convention (and the identical value type) as
	// patch.Entry.ChannelFunctions, so a library mode can be applied to a
	// patch entry by assignment with no translation layer that could
	// introduce a bug. Never nil once it has been through this package
	// (see normalizeMode): a present-but-empty map is a real state
	// ("resolved, found nothing") and marshals as `{}`, whereas nil would
	// marshal as `null` and every JS caller would need a defensive guard.
	ChannelFunctions map[uint16]patch.ChannelFunction `json:"channelFunctions"`
	// Origin is where this mode's data came from.
	Origin Origin `json:"origin"`
}

// Record is one fixture TYPE: the library's unit of storage. See the
// package doc comment for the owner's manufacturer+model keying decision.
type Record struct {
	SourceFiles []SourceFile `json:"sourceFiles"`
	// Manufacturer and Model together are the record's identity, stored
	// verbatim as the best-known human spelling (this is what a UI shows).
	// Key below is the machine-comparable form.
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	// Key is the normalised identity — case- and whitespace-folded
	// "manufacturer|model", stable across sessions and safe in a URL path
	// segment after escaping. It is the record's primary key for GET/
	// DELETE and for import reconciliation's exact-hit fast path. It is
	// NOT the fuzzy matcher: tolerant, punctuation-insensitive matching
	// ("JDC 1" ~ "GLP JDC-1") is a separate concern handled in match.go.
	// Always recomputed by this package on write (see normalizeRecord), so
	// a hand-edited or third-party file with a wrong/absent key still
	// imports correctly.
	Key string `json:"key"`
	// RDMManufacturerID is the ESTA manufacturer ID from the device's RDM
	// UID. Deliberately NO `omitempty`: 0x0000 is a real, allocated-in-
	// principle value and, more importantly, an omitted key here is
	// indistinguishable from a genuine zero — which is the whole defect
	// class this codebase has hit three times. Whether the value is known
	// at all is carried explicitly by the companion boolean below rather
	// than being smuggled through the zero value.
	RDMManufacturerID uint16 `json:"rdmManufacturerId"`
	// RDMManufacturerIDKnown says whether RDMManufacturerID means
	// anything. Deliberately NO `omitempty` — `false` is the meaningful
	// half of a boolean, and omitting it would make "we have never seen
	// this type over RDM" invisible on the wire.
	RDMManufacturerIDKnown bool `json:"rdmManufacturerIdKnown"`
	// RDMDeviceModelID is DEVICE_INFO's device model ID. Same no-omitempty
	// reasoning as RDMManufacturerID above.
	RDMDeviceModelID      uint16 `json:"rdmDeviceModelId"`
	RDMDeviceModelIDKnown bool   `json:"rdmDeviceModelIdKnown"`
	// RDMIdentityOrigin is where the two RDM IDs above came from.
	RDMIdentityOrigin Origin `json:"rdmIdentityOrigin"`
	// SupportedPIDs is every RDM Parameter ID this type has been OBSERVED
	// to support, ascending and deduplicated. Stored as plain uint16 (not
	// rdm.ParameterID) to keep this package free of any RDM dependency,
	// exactly as internal/patch keeps ConfirmedUID a plain string for the
	// same reason. Never nil — always `make([]uint16, 0)` — so it marshals
	// as `[]`, never `null`.
	SupportedPIDs []uint16 `json:"supportedPids"`
	// PIDOrigin is where SupportedPIDs came from. In practice always
	// ProvenanceRDM: a GDTF file says nothing about RDM PIDs.
	PIDOrigin Origin `json:"pidOrigin"`
	// Modes are this type's DMX personalities (owner's nesting decision).
	// Never nil — always `make([]Mode, 0)`.
	Modes []Mode `json:"modes"`
	// IdentityOrigin is where the manufacturer/model spelling came from.
	IdentityOrigin Origin `json:"identityOrigin"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Library is the whole store, and — byte for byte — the exported file
// format the owner hands to a coworker. Export writes this struct and
// Import reads it, so there is exactly one shape to document and one shape
// to keep compatible; there is deliberately no separate "export envelope"
// that could drift from what is actually persisted.
type Library struct {
	// Format is FileFormat. Checked on import before anything else — see
	// FileFormat's doc comment for why a self-identifying file matters
	// when the file's whole purpose is to be emailed around.
	Format string `json:"format"`
	// SchemaVersion is CurrentSchemaVersion at write time.
	SchemaVersion int `json:"schemaVersion"`
	// AppVersion/GeneratedAt are informational provenance for a shared
	// file (which build wrote it, when) and are ignored on import.
	AppVersion  string    `json:"appVersion,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	CreatedAt   time.Time `json:"createdAt"`
	ModifiedAt  time.Time `json:"modifiedAt"`
	// Records is the library's contents, sorted by Key for a stable,
	// diffable file. Never nil — always `make([]Record, 0)`, so an empty
	// library exports as `"records": []` and a coworker's JSON parser (or
	// this project's own frontend) never sees `null`.
	Records []Record `json:"records"`
}

// --- normalisation ----------------------------------------------------

// foldKeyPart lowercases s and collapses every run of whitespace to a
// single space, trimming the ends. This is a STORAGE-IDENTITY fold, not a
// fixture-name normaliser: it deliberately does nothing clever with
// punctuation or letter/digit boundaries, because the tolerant, punctuation-
// insensitive comparison this project already owns lives in internal/patch
// and is reused as-is (see match.go). Keeping the two jobs separate is what
// stops this file from quietly becoming a second, subtly-different
// normaliser.
func foldKeyPart(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// KeyFor builds a Record's storage key from its identity. The "|"
// separator can never appear in a folded part ambiguously — a literal "|"
// typed into a manufacturer name is preserved, but manufacturer and model
// are always joined in that order with exactly one separator, so parsing is
// never required (the key is only ever compared, never split).
func KeyFor(manufacturer, model string) string {
	return foldKeyPart(manufacturer) + "|" + foldKeyPart(model)
}

// normalizeMode makes m safe to marshal and to hand out: non-nil channel
// map, non-nil ChannelSets on every channel function in it (patch's own
// invariant — a nil slice there marshals to `null`).
func normalizeMode(m Mode) Mode {
	if m.ChannelFunctions == nil {
		m.ChannelFunctions = make(map[uint16]patch.ChannelFunction)
	}
	for off, cf := range m.ChannelFunctions {
		if cf.ChannelSets == nil {
			cf.ChannelSets = make([]patch.ChannelSet, 0)
			m.ChannelFunctions[off] = cf
		}
	}
	if m.VerifiedHash == "" || m.VerifiedHash != modeHash(m) {
		m.VerifiedHash = ""
		m.VerifiedAt = time.Time{}
		m.VerificationNote = ""
	}
	return m
}

// normalizeRecord recomputes r's key and enforces every non-nil-slice /
// non-nil-map invariant this package promises. Called on every path that
// installs a record (Upsert, Import, load-from-disk) — construction by a
// caller's struct literal is normal and must not be able to produce a
// record that marshals `null` anywhere.
func normalizeRecord(r Record) Record {
	r.SourceFiles = mergeSourceFiles(nil, r.SourceFiles)
	r.Key = KeyFor(r.Manufacturer, r.Model)
	if r.SupportedPIDs == nil {
		r.SupportedPIDs = make([]uint16, 0)
	} else {
		r.SupportedPIDs = dedupePIDs(r.SupportedPIDs)
	}
	if r.Modes == nil {
		r.Modes = make([]Mode, 0)
	}
	for i := range r.Modes {
		r.Modes[i] = normalizeMode(r.Modes[i])
	}
	return r
}

// dedupePIDs returns pids ascending with duplicates removed, always as a
// fresh non-nil slice (so the caller's backing array is never aliased into
// the store).
func dedupePIDs(pids []uint16) []uint16 {
	seen := make(map[uint16]struct{}, len(pids))
	out := make([]uint16, 0, len(pids))
	for _, p := range pids {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sortPIDs(out)
	return out
}

func sortPIDs(p []uint16) {
	// Insertion sort: PID lists are short (a fixture supporting more than
	// a few dozen PIDs is unheard of) and this avoids pulling sort's
	// closure machinery into a hot merge path.
	for i := 1; i < len(p); i++ {
		v := p[i]
		j := i - 1
		for j >= 0 && p[j] > v {
			p[j+1] = p[j]
			j--
		}
		p[j+1] = v
	}
}

// cloneRecord returns a deep copy — every map and slice reachable from a
// Record is copied, so a value handed across the store's mutex boundary can
// never be mutated by (or alias into) the live store. patch.Store's Get
// only shallow-copies its entry slice because patch.Entry's map is treated
// as immutable-once-installed there; this package's records are merged
// field by field, so the deeper copy is required rather than merely tidy.
func cloneRecord(r Record) Record {
	out := r
	out.SourceFiles = make([]SourceFile, len(r.SourceFiles))
	for i, f := range r.SourceFiles {
		out.SourceFiles[i] = f
		out.SourceFiles[i].Data = append([]byte(nil), f.Data...)
	}
	out.SupportedPIDs = append(make([]uint16, 0, len(r.SupportedPIDs)), r.SupportedPIDs...)
	out.Modes = make([]Mode, len(r.Modes))
	for i, m := range r.Modes {
		cm := m
		cm.ChannelFunctions = make(map[uint16]patch.ChannelFunction, len(m.ChannelFunctions))
		for off, cf := range m.ChannelFunctions {
			ccf := cf
			ccf.ChannelSets = append(make([]patch.ChannelSet, 0, len(cf.ChannelSets)), cf.ChannelSets...)
			cm.ChannelFunctions[off] = ccf
		}
		out.Modes[i] = cm
	}
	return out
}
