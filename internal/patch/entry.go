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
// Persistence choice: one active patch at a time, with a small on-disk
// catalog of named shows. Every existing patch endpoint still operates only
// on the active show; the catalog endpoints switch it explicitly. This keeps
// the normal programming path simple while letting one installation retain
// independent rigs for concurrent shows.
package patch

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CurrentSchemaVersion is stamped onto every Patch this package writes.
// migrate() below upgrades any older (or missing/zero) version on load —
// "old files must always open" is a hard rule carried over from the
// project's other codebase (see the file's package doc comment and
// CLAUDE.md's "tolerant reader" convention).
//
// Version 2 (function-aware Rig Check, foundation phase): added
// Entry.ChannelFunctions (per-DMX-offset attribute resolution — see
// ChannelFunction's doc comment below). A v1 file simply has no
// "channelFunctions" key on any entry, which unmarshals to a nil map —
// migrate() below turns that nil into an explicit empty map so every entry,
// old or new, always has a non-nil ChannelFunctions a caller can range over
// or marshal without a special nil case (see TestMigrate_NilChannelFunctionsBecomesEmptyMap).
//
// Version 3 (GDTF Default/Highlight capture): added ChannelFunction's
// HasDefault/Default/DefaultByteCount and
// HasHighlight/Highlight/HighlightByteCount
// — a channel's GDTF-declared resting value, which Rig Check needs in order
// to make a fixture actually emit light while one channel is under test (a
// real fixture needs both a dimmer at level AND a shutter in its open
// position; driving every untested channel to 0 guarantees darkness). A v2
// file has none of those keys, which unmarshals to HasDefault==false —
// exactly the "the file never said" state, NOT a silently-real default of 0.
// migrate() deliberately invents nothing for a v2 entry (see
// TestMigrate_V2FileLoadsWithDefaultsUnknown).
//
// Version 4 (Reconcile commit/decommit): added Entry.Intended and
// Entry.AsFound (asfound.go) — the device-level settings a patch entry
// intends, and the settings actually read off the fixture it is committed
// to. A v3 file has neither key, so both unmarshal to zero-valued structs in
// which every `Known` companion boolean is false and AsFound.UID is empty —
// i.e. "never read", which is exactly right and is NOT the same as "read as
// 0". A dimmer curve of 0 and a DMX address of 0 are both real values a
// fixture can report, so no numeric or boolean field in either struct
// carries `omitempty` and none of them is ever interpreted without its
// Known flag. As with v2 -> v3, migrate() below deliberately invents nothing
// for a v3 entry (see TestMigrate_V3FileLoadsWithAsFoundUnread); and since
// v4 added no slices or maps, there is no nil normalization to do either.
// Version 5 (as-found "not fitted", bench capture RDM-LOG24, 2026-09-02):
// every stored setting in asfound.go gained an explicit State — "read",
// "not_fitted" or "unknown" — so that "this fixture's own
// SUPPORTED_PARAMETERS does not list PAN_INVERT, therefore it has no pan"
// stops being rendered as a failed read with a NACK string beside it, and
// stops costing a round trip to relearn on every commit. A v4 file has no
// "state" key on any setting; migrate() below normalizes the resulting
// empty string to "unknown", never "not_fitted" — see the v4 -> v5 note in
// migrate() for why that direction is the whole point of the bump.
//
// Version 6 (Console-lite C1, full GDTF channel detail): ChannelFunction
// gained ByteCount/ByteIndex and FunctionsKnown/Functions (every
// ChannelFunction of every LogicalChannel of the owning DMXChannel, with its
// ChannelSets — see FunctionRange), and Entry gained Wheels/WheelsKnown. A
// v5 file has none of those keys: FunctionsKnown and WheelsKnown unmarshal
// to false, which IS the "full channel detail was never imported" mark, and
// migrate() only normalizes the nil slices to empty ones. Nothing is derived
// from the first-function fields — a v5 entry's ranges stay unknown until
// its GDTF is re-imported (see TestMigrate_V5FileLoadsWithChannelDetailNotImported).
//
// Version 7 (Console-lite C2, positions): Entry gained Location — the
// fixture's MVR world position and orientation (see Location). A v6 file has
// no "location" key, which unmarshals to Known == false and RotationKnown ==
// false: exactly "no position was ever imported". migrate() derives nothing
// (the free-text Position label is never parsed for coordinates) — see
// TestMigrate_V6FileLoadsWithLocationUnknown.
//
// Version 8 (Console-lite C1b, profile cache): a FILE-encoding change only.
// Each distinct profile (fixture type + mode + channel map + wheels) is
// stored once in a top-level "profiles" table keyed by content hash, and
// each entry stores "profile": <key> instead of its channel map and wheels
// — see showfile.go. Entry, Patch and every API shape are unchanged. A v7
// or older file (maps inline) is read as before by decodeShowFile; the next
// save writes v8. From v8 on, a file NEWER than this build is refused, not
// opened: an older build reading a v8 file would see no channel maps at all.
const CurrentSchemaVersion = 8

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
	// without being treated as a hard error. Deliberately NO `omitempty`:
	// zero is legitimate, meaningful data here (an unresolved GDTF import
	// or a genuine data-device footprint, exactly what the collision
	// detector's KindZeroFootprint reports on) — `omitempty` on a numeric
	// field erases a real zero from the JSON rather than an absent value.
	// An absent key still unmarshals to zero (see NewStore's "tolerant
	// reader" comment), so dropping `omitempty` costs nothing for old/
	// hand-written files and only fixes what a *present* zero looks like
	// on the wire.
	Footprint uint16 `json:"footprint"`
	// Universe is the Art-Net Port-Address (raw value, 0-32767) this entry
	// is patched into — matches the vocabulary/type every other screen in
	// this app already uses for "universe" (see internal/walk.Device.
	// PortAddress). Deliberately NO `omitempty`: Universe 0 is the first,
	// entirely ordinary Art-Net universe (displayed as "1" under the
	// owner's default 1-based UniverseBase) — not an absent/unset
	// sentinel. `omitempty` here previously erased every universe-0 entry
	// from GET /api/patch's JSON, leaving `e.universe` `undefined`
	// client-side; the sort comparator's `a.universe - b.universe` then
	// produced NaN for those rows, and Array.prototype.sort's unspecified
	// behavior on a NaN-returning comparator silently misplaced them (root
	// cause of the "universes 1-3 intermingled" bug). This project hit
	// this exact defect class before on sensorReadingJSON's range/normal-
	// band fields (see internal/web/device.go) — do not reintroduce it.
	Universe uint16 `json:"universe"`
	// StartAddress is the 1-based DMX slot this entry starts at.
	// Deliberately NO `omitempty`: a hand-entered, not-yet-addressed entry
	// legitimately has StartAddress==0 (DetectCollisions' KindInvalidAddress
	// flags it, rather than treating a missing value specially), and that
	// zero must round-trip as an explicit `"startAddress":0` for the same
	// reason as Universe above.
	StartAddress uint16 `json:"startAddress"`
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

	// Intended and AsFound are the schema-v4 commit model — see asfound.go's
	// file comment for the full design, including why the two are
	// deliberately asymmetric (decommit clears AsFound and leaves Intended
	// alone, which is what makes fixture substitution work) and why address/
	// universe/footprint/mode-name are NOT duplicated into Intended.
	//
	// Neither carries `omitempty`. They are structs, so `omitempty` would
	// not omit them anyway (encoding/json has never treated a struct as
	// empty), but the absent tag is deliberate documentation: every key
	// inside them is likewise always on the wire, because a `false` Known
	// flag and a `0` value are the two halves of one signal and dropping
	// either would recreate the exact defect class Entry.Universe's comment
	// above records.
	Intended IntendedSettings `json:"intended"`
	AsFound  AsFoundSettings  `json:"asFound"`

	// ChannelFunctions is the function-aware Rig Check foundation (see
	// taxonomy.go's package-level doc comment for the feature this
	// supports). Keyed by DMX offset 1-based WITHIN THIS ENTRY'S FOOTPRINT
	// (offset 1 is the entry's first channel, at StartAddress — same
	// convention GDTF's own <DMXChannel Offset="..."> attribute uses, and
	// deliberately NOT an absolute universe address, so the map stays valid
	// across a start-address edit). Deliberately no `omitempty`: a present-
	// but-empty map is a real, meaningful state (channel functions were
	// resolved and none were found — as distinct from field never having
	// been populated at all on an old file, which unmarshals to nil and is
	// normalized to an empty, non-nil map by migrate()). Every ChannelFunction
	// this map holds carries its own Source, so provenance is never a
	// per-entry side-fact that can drift out of sync with what the map
	// actually contains — see ChannelFunction's doc comment.
	ChannelFunctions map[uint16]ChannelFunction `json:"channelFunctions"`

	// Wheels are the GDTF <Wheels> of the fixture type this entry was
	// profiled from (schema v6) — what FunctionRange.Wheel and
	// SetRange.WheelSlot point into. Never nil (make([]Wheel, 0)).
	// WheelsKnown is the only thing that separates "this fixture has no
	// wheels" (true, empty list) from "nobody imported them" (false — every
	// pre-v6 entry, every hand-entered or RDM-adopted one). No `omitempty`
	// on either: the false and the empty list are both the signal.
	Wheels      []Wheel `json:"wheels"`
	WheelsKnown bool    `json:"wheelsKnown"`

	// PhaseWeight is runtime-only Rig Check metadata. A web caller may set it
	// from a committed fixture's RDM DEVICE_INFO sub-device count so one
	// multi-cell fixture consumes several positions when calculating a phase
	// spread. It never changes patch identity, normal scope counts, or the
	// stored show file, and it must never be used to invent DMX channel maps.
	PhaseWeight uint16 `json:"-"`
	// Zero selects automatic profile/RDM detection; a positive count is an
	// operator-confirmed phase-slot override, unrelated to footprint/counts.
	PhaseCount uint16 `json:"phaseCount"`

	// Location is the fixture's world position (schema v7). Independent of
	// the free-text Position label above, which it never reads or changes.
	Location Location `json:"location"`
}

// Location is a fixture's position and orientation in MVR world coordinates
// (MVR 1.6 / DIN SPEC 15801, "Node Definition: Matrix": right-handed, Z up,
// 1 unit = 1 mm), after composing every ancestor transform of the <Fixture>
// (Layer, GroupObject, Truss, ... — each child's Matrix is "inside the parent
// coordinate system"). Produced by the browser's MVR parser (mvrparse.js),
// which documents the composition.
//
// Known false = no position was ever imported (pre-v7 data, hand-entered,
// RDM-adopted, or an MVR fixture whose Matrix could not be read); X/Y/Z are
// then 0 and mean nothing. RotationKnown is separate because a readable
// offset can sit on a basis that is not a rotation (a mirrored or degenerate
// matrix): position known, orientation refused.
//
// RotX/RotY/RotZ are degrees, R = Rz(RotZ)·Ry(RotY)·Rx(RotX) acting on column
// vectors (equivalently: rotate about world X, then world Y, then world Z),
// RotY in [-90, 90]; at RotY = ±90 (gimbal lock) RotX is fixed at 0. MVR does
// not define Euler angles at all — the matrix is the data — so this
// convention is ours, chosen for display (a glyph's plan rotation is RotZ);
// the angles are exact for the stated convention, not approximate.
//
// No `omitempty` anywhere: a fixture at X = 0 or hung at RotY = 0 is real
// data, and the two Known flags are half of the signal.
type Location struct {
	Known         bool    `json:"known"`
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	Z             float64 `json:"z"`
	RotationKnown bool    `json:"rotationKnown"`
	RotX          float64 `json:"rotX"`
	RotY          float64 `json:"rotY"`
	RotZ          float64 `json:"rotZ"`
}

// Validate refuses a Location whose flags and numbers disagree: values
// without Known, rotation without RotationKnown, rotation without position,
// or a non-finite number. It never repairs one.
func (l Location) Validate() error {
	for _, v := range []float64{l.X, l.Y, l.Z, l.RotX, l.RotY, l.RotZ} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("Location values must be finite numbers.")
		}
	}
	if !l.Known && (l.X != 0 || l.Y != 0 || l.Z != 0 || l.RotationKnown) {
		return errors.New("The location carries coordinates or a rotation but says it is not known.")
	}
	if !l.RotationKnown && (l.RotX != 0 || l.RotY != 0 || l.RotZ != 0) {
		return errors.New("The location carries rotation angles but says the rotation is not known.")
	}
	return nil
}

// ChannelFunctionSource records how a ChannelFunction's attribute mapping
// was determined. This is the field the owner's hard constraint on RDM slot
// inference (task brief, decision 3) is built around: an inferred mapping
// must be structurally impossible to mistake for an authoritative one.
// Every ChannelFunction carries this field, there is no separate "trust me"
// path, and the zero value (SourceAbsent) is never emitted for a slot the
// map actually has an entry for — an absent slot simply has no map entry at
// all (see ResolveEntryAttributes in taxonomy.go, which never invents an
// entry to say "absent").
type ChannelFunctionSource string

// Sources. SourceAbsent is the zero value — reachable only via a
// zero-valued ChannelFunction a caller constructed by hand (e.g. a
// zero-value default in a test); real producers (GDTF import, RDM slot
// inference) always set one of the other two explicitly.
const (
	SourceAbsent      ChannelFunctionSource = ""
	SourceGDTF        ChannelFunctionSource = "gdtf"
	SourceRDMInferred ChannelFunctionSource = "rdm-inferred"
)

// ChannelSet is one named value sub-range within a ChannelFunction — GDTF's
// <ChannelSet> (e.g. a gobo wheel's individual gobo choices, each with its
// own DMX range and physical/name label). Never present for an RDM-inferred
// ChannelFunction: RDM's SLOT_INFO/SLOT_DESCRIPTION has no equivalent
// concept, only a single slot-wide text label (see ChannelFunction.RDMSlotLabel).
type ChannelSet struct {
	Name string `json:"name,omitempty"`
	// DMXFrom is the raw DMX value (0-255 for an 8-bit channel; GDTF's own
	// coarse-byte reading of its "x/y" DMXFrom notation) this named
	// sub-range starts at. Deliberately no `omitempty`: a ChannelSet
	// starting at DMX 0 (the common case — a wheel's first slot almost
	// always starts at 0) is real, present data, not an absent value.
	DMXFrom uint32 `json:"dmxFrom"`
	// PhysicalFrom/PhysicalTo carry no `omitempty` for the same reason
	// DMXFrom above does not — see the fuller note on ChannelFunction's
	// pair. A closed-shutter or zero-frost set genuinely spans 0 to 0.
	PhysicalFrom float64 `json:"physicalFrom"`
	PhysicalTo   float64 `json:"physicalTo"`
}

// ChannelFunction is what one DMX offset within a patch entry's footprint
// does: which taxonomy attribute it drives (taxonomy.go), GDTF's function
// name and value-range data when available, or an RDM-inferred
// approximation when it is not — see Source's doc comment for why the two
// can never be confused.
type ChannelFunction struct {
	GeometryInstance string `json:"geometryInstance,omitempty"`
	// Source is never omitted from the JSON (no `omitempty`) — an absent
	// Source next to real Attribute/FunctionName data would be exactly the
	// silent-guess failure mode decision (3) forbids; every consumer of
	// this struct must look at Source before trusting anything else in it.
	Source ChannelFunctionSource `json:"source"`
	// Attribute is the resolved taxonomy attribute — a GDTF standard
	// attribute name verbatim when Source==SourceGDTF (e.g. "Dimmer",
	// "ColorAdd_R"), or this package's best-mapped equivalent name when
	// Source==SourceRDMInferred (see taxonomy.go's RDM slot-label bridge in
	// internal/web/patchattrs.go). Empty only for a genuinely unrecognized
	// slot — still placed in the Other group by GroupForAttribute(""), per
	// the task rule that an unmapped attribute is never silently dropped.
	Attribute string `json:"attribute,omitempty"`
	// FunctionName is GDTF's <ChannelFunction Name="...">, verbatim. Always
	// empty for RDM-inferred entries — RDM's SLOT_INFO carries no function
	// name, only a Slot Label ID (see RDMSlotLabel below for the nearest
	// RDM equivalent, a free-text description, not a function name).
	FunctionName string `json:"functionName,omitempty"`
	// DMXFrom/DMXTo are the raw DMX value range this function is active
	// over (GDTF's ChannelFunction DMXFrom, and the next function's DMXFrom
	// minus one, or the channel's top value for the last function).
	// Deliberately no `omitempty` on DMXFrom: a function starting at DMX 0
	// (extremely common — most fixtures' first ChannelFunction on a channel
	// starts at 0) is real data.
	DMXFrom uint32 `json:"dmxFrom"`
	DMXTo   uint32 `json:"dmxTo"`
	// PhysicalFrom/PhysicalTo are GDTF's PhysicalFrom/PhysicalTo (e.g. pan
	// degrees, percentage). Deliberately no `omitempty`, and the comment
	// that used to justify one here was wrong on both of its counts.
	//
	// It argued that 0.0 is "ambiguous with not provided for physical units
	// anyway". It is not: a dimmer's physical range starts at 0%, a frost's
	// at 0, and a ChannelSet for a closed shutter legitimately has a
	// physical range of 0 to 0. Those are stated facts from the file, and
	// omitempty erased every one of them.
	//
	// It also treated the ambiguity as harmless because GDTF need not
	// supply these. That is backwards — a field that is genuinely sometimes
	// absent is exactly the one whose present-and-zero case must be
	// distinguishable on the wire, and the fix for "sometimes absent" is a
	// companion flag (see HasDefault below), never a silently dropped key.
	//
	// The concrete damage was an asymmetry across the Go/JS seam:
	// internal/web's channelFunctionRequest and channelSetRequest declare
	// both fields WITHOUT omitempty, so the browser sent "physicalFrom":0,
	// the server accepted and stored it, and then served it back with the
	// key missing. A value survived the trip in and was erased on the way
	// out. The Fixture Library exports this struct verbatim, so the same
	// zeroes also vanished from a library file handed to a coworker.
	PhysicalFrom float64 `json:"physicalFrom"`
	PhysicalTo   float64 `json:"physicalTo"`
	// ChannelSets is GDTF's <ChannelSet> children of this ChannelFunction,
	// if any. Deliberately `make([]ChannelSet, 0)`, never a nil slice, at
	// every construction site in this codebase (gdtfparse.js's Go-side
	// counterpart and the demo data both follow this) — see this package's
	// "slices must be make([]T,0)" rule; a nil slice here would marshal to
	// `null` and every JS caller would need a defensive `|| []` instead of
	// being able to just call `.map()`/`.length` on it.
	ChannelSets []ChannelSet `json:"channelSets"`

	// --- GDTF resting values (schema v3) ---------------------------------
	//
	// HasDefault says whether the source file actually stated a Default for
	// this channel. It is NOT redundant with Default != 0: a Default of 0 is
	// real, common, meaningful data (a dimmer resting dark, a shutter resting
	// closed), and this package's hard rule — see Entry.Universe/
	// Entry.Footprint above, and the sensorReadingJSON incident they cite —
	// is that a numeric field whose zero is real data NEVER carries
	// `omitempty`, because `omitempty` erases the real zero and leaves the
	// client reading `undefined`. So Default carries no `omitempty` (a
	// present 0 must appear on the wire as `"default":0`) and HasDefault
	// carries none either (a present `false` is the whole signal). Consumers
	// MUST check HasDefault before using Default; a zero-valued
	// ChannelFunction reads as "unknown", never as "rests at 0".
	HasDefault bool `json:"hasDefault"`
	// Default is GDTF's <ChannelFunction Default="X/Y"> raw value X, in the
	// SAME units as DMXFrom/DMXTo above (gdtfparse.js's one
	// parseDmxValueParts helper produces all three). For an 8-bit channel
	// that is a plain 0-255 byte; for a 16-bit channel it is the full
	// 0-65535 value, NOT a coarse byte — DefaultByteCount is what says which.
	Default uint32 `json:"default"`
	// DefaultByteCount is GDTF's "X/Y" byte count Y (1 for 8-bit, 2 for
	// 16-bit, ...), and it is what makes a multi-byte Default meaningful for
	// BOTH bytes of a coarse+fine channel. Every offset a multi-offset
	// DMXChannel spans receives an identical ChannelFunction record (see
	// gdtfparse.js's channelFunctions rule), so a consumer recovers its own
	// byte from its position in that channel's offsets, which resolve.go's
	// ResolvedFunction.Offsets and testpattern.go's "16-bit (coarse+fine)"
	// design note both fix as (coarse, fine) ascending:
	//
	//	byte(i) = (Default >> (8 * (DefaultByteCount-1-i))) & 0xFF
	//
	// e.g. a 16-bit channel at offsets (5,6) with Default="32768/2" has
	// Default 32768, DefaultByteCount 2 -> 128 at offset 5, 0 at offset 6.
	//
	// Deliberately a scalar and not a pre-decomposed []uint32: this mirrors
	// GDTF's own encoding instead of a derived form, and — the practical
	// reason — a slice field here would have to be non-nil at every
	// ChannelFunction construction site in the codebase to satisfy this
	// package's "slices must be make([]T,0)" rule (a nil slice marshals to
	// `null`, which TestChannelFunction_MarshalJSON_EmptyChannelSetsIsArrayNotNull
	// exists to catch). A scalar has no such failure mode: its zero value is
	// simply "no byte count", which is exactly what HasDefault==false means.
	// 0 here is never real data, but it still gets no `omitempty` — the rest
	// of this struct's numeric fields don't, and an inconsistently-omitted
	// key is its own client-side hazard.
	DefaultByteCount uint16 `json:"defaultByteCount"`
	// HasHighlight/Highlight/HighlightByteCount are the same triple for
	// GDTF's optional <ChannelFunction Highlight="X/Y"> (the "locate/
	// highlight" value). Far fewer files carry it than carry Default, which
	// is exactly why HasHighlight exists rather than a bare number.
	HasHighlight       bool   `json:"hasHighlight"`
	Highlight          uint32 `json:"highlight"`
	HighlightByteCount uint16 `json:"highlightByteCount"`

	// RDMSlotType/RDMSlotLabel carry the raw RDM SLOT_INFO/SLOT_DESCRIPTION
	// evidence this mapping was inferred from — present only when
	// Source==SourceRDMInferred, always empty for GDTF-derived entries.
	// Kept alongside the resolved Attribute specifically so a future UI can
	// show the raw, weaker evidence next to the approximate mapping instead
	// of hiding how little the inference is actually built on.
	RDMSlotType  string `json:"rdmSlotType,omitempty"`
	RDMSlotLabel string `json:"rdmSlotLabel,omitempty"`

	// --- full channel detail (schema v6, Console-lite C1) ----------------
	//
	// Everything above still describes the FIRST ChannelFunction of the
	// first LogicalChannel exactly as it always has — Rig Check reads those
	// fields and they are deliberately left byte-identical (including the
	// legacy DMXTo, which is in the file's raw units and falls back to 255;
	// use Functions for real ranges). The fields below describe the whole
	// owning <DMXChannel>.
	//
	// ByteCount is the number of DMX bytes the owning <DMXChannel> spans
	// (the length of its Offset list: 1 = 8-bit, 2 = 16-bit, ...).
	// ByteIndex is this offset's position in that list, 0 = coarse — GDTF
	// lists Offset "from highest to least significant" (DIN SPEC 15800,
	// Table 58). 0/0 on an entry with FunctionsKnown false means unknown.
	ByteCount int `json:"byteCount"`
	ByteIndex int `json:"byteIndex"`
	// FunctionsKnown says Functions was produced by the full-detail parser.
	// false = "detail not imported" (pre-v6 data, RDM-inferred, hand-made),
	// and Functions is then always empty — never a list derived from the
	// first-function fields above.
	FunctionsKnown bool `json:"functionsKnown"`
	// Functions is EVERY ChannelFunction of EVERY LogicalChannel of the
	// owning DMXChannel, in document order. Every offset of a multi-byte
	// channel carries the same list. Never nil (make([]FunctionRange, 0)).
	Functions []FunctionRange `json:"functions"`
}

// FunctionRange is one GDTF <ChannelFunction> (DIN SPEC 15800, Table 60).
//
// RESOLUTION CONVENTION: every DMX value here — DMXFrom, DMXTo, Default,
// Highlight — is at the owning DMXChannel's FULL resolution: 0..255 for an
// 8-bit channel, 0..65535 for 16-bit, and so on. The file's "value/bytes"
// notation is converted per the spec's DMXValue type (Table 1: byte
// mirroring by default, byte shifting with an "s" suffix) as the reference
// implementation does it (libMVRgdtf GdtfConverter::ConvertDMXValue).
// ModeFrom/ModeTo are at the MASTER channel's resolution.
//
// DMXTo is the next function's DMXFrom - 1 or, for the last function of the
// channel (or when the next one does not start higher, i.e. mode-mastered
// alternatives sharing a start), the channel's maximum (Table 60; "next" is
// taken across LogicalChannels in document order, as libMVRgdtf links
// them).
type FunctionRange struct {
	// LogicalAttribute is the parent <LogicalChannel Attribute>.
	LogicalAttribute string  `json:"logicalAttribute"`
	Attribute        string  `json:"attribute"`
	Name             string  `json:"name"`
	DMXFrom          uint32  `json:"dmxFrom"`
	DMXTo            uint32  `json:"dmxTo"`
	PhysicalFrom     float64 `json:"physicalFrom"`
	PhysicalTo       float64 `json:"physicalTo"`
	// HasDefault/HasHighlight: the file stated one (see
	// ChannelFunction.HasDefault for why the flag exists). Default comes from
	// <ChannelFunction Default> or, for a GDTF 1.0 file, from the owning
	// <DMXChannel Default> (moved to ChannelFunction in GDTF 1.1; the
	// reference implementation applies the channel's value to its
	// functions). Highlight comes from <ChannelFunction Highlight> if present,
	// else <DMXChannel Highlight> (Table 58; "None" = not stated).
	HasDefault   bool   `json:"hasDefault"`
	Default      uint32 `json:"default"`
	HasHighlight bool   `json:"hasHighlight"`
	Highlight    uint32 `json:"highlight"`
	// Wheel is the GDTF <Wheel Name> this function links to, or "".
	Wheel string `json:"wheel"`
	// ModeMaster is the GDTF Node link verbatim ("Head_Color1", or
	// "Head_Gobo1.Gobo1.Gobo1" for a ChannelFunction), "" = none. HasMode is
	// true only when that master resolved to a DMXChannel of the same mode
	// and ModeFrom/ModeTo could be read; a ModeMaster with HasMode false is
	// an unresolved dependency, reported by the parser, never guessed.
	ModeMaster string     `json:"modeMaster"`
	HasMode    bool       `json:"hasMode"`
	ModeFrom   int        `json:"modeFrom"`
	ModeTo     int        `json:"modeTo"`
	Sets       []SetRange `json:"sets"`
}

// SetRange is one GDTF <ChannelSet> (Table 61). DMXFrom/DMXTo are at the
// channel's full resolution; DMXTo is the next set's DMXFrom - 1, or the
// parent function's DMXTo for the last set. WheelSlot is GDTF's 1-based
// WheelSlotIndex into the parent function's Wheel; HasWheelSlot is false
// when the attribute is absent or 0 (vendor files write "0" on sets that
// are not wheel slots).
type SetRange struct {
	Name         string  `json:"name"`
	DMXFrom      uint32  `json:"dmxFrom"`
	DMXTo        uint32  `json:"dmxTo"`
	PhysicalFrom float64 `json:"physicalFrom"`
	PhysicalTo   float64 `json:"physicalTo"`
	HasWheelSlot bool    `json:"hasWheelSlot"`
	WheelSlot    int     `json:"wheelSlot"`
}

// Wheel is one GDTF <Wheel> (Table 11) with its slots in document order —
// slot i (0-based) is WheelSlotIndex i+1. Slots is never nil.
type Wheel struct {
	Name  string      `json:"name"`
	Slots []WheelSlot `json:"slots"`
}

// WheelSlot is one GDTF <Slot> (Table 12). ColorX/ColorY/ColorYY are the
// file's CIE 1931 xyY verbatim (HasColor false when the slot states no
// Color — the spec's default white is NOT filled in). SRGB is a DISPLAY
// SWATCH ONLY, "#rrggbb", converted from x,y alone: Y is ignored because
// vendor files disagree on its scale (Robe BMFL writes 0.145 for a deep
// red and 100.0 for white in the same wheel; Robin 100 LEDBeam writes 100.0
// for every colour). Out-of-gamut components are clipped and the result is
// normalised to full brightness, so it is approximate by construction.
// MediaFileName is the gobo/animation image name (without extension) in
// the archive's ./wheels folder, "" when none.
type WheelSlot struct {
	Name          string  `json:"name"`
	HasColor      bool    `json:"hasColor"`
	ColorX        float64 `json:"colorX"`
	ColorY        float64 `json:"colorY"`
	ColorYY       float64 `json:"colorYY"`
	HasSRGB       bool    `json:"hasSRGB"`
	SRGB          string  `json:"srgb"`
	MediaFileName string  `json:"mediaFileName"`
}

// CloneChannelFunction returns cf with every slice it holds copied (and
// normalised non-nil), so a value handed across a store's mutex boundary
// can never alias the stored one.
func CloneChannelFunction(cf ChannelFunction) ChannelFunction {
	cf.ChannelSets = append(make([]ChannelSet, 0, len(cf.ChannelSets)), cf.ChannelSets...)
	fns := make([]FunctionRange, len(cf.Functions))
	for i, f := range cf.Functions {
		f.Sets = append(make([]SetRange, 0, len(f.Sets)), f.Sets...)
		fns[i] = f
	}
	cf.Functions = fns
	return cf
}

// HasChannelDetail reports whether cf carries any schema-v6 detail.
func (cf ChannelFunction) HasChannelDetail() bool {
	return cf.FunctionsKnown || cf.ByteCount != 0 || cf.ByteIndex != 0 || len(cf.Functions) > 0
}

// preV6ChannelFunction is ChannelFunction's exact pre-schema-v6 field list,
// order and JSON tags. It exists for ONE purpose: stored digests that a
// pre-v6 build computed by hashing a marshalled channel map — the Fixture
// Library's per-mode verification stamp (library.modeHash) and Rig
// Baselines' profile digest (internal/web/workspace.go) — must still match
// after upgrading, or every verified mode would silently lose its stamp and
// every baseline would report every fixture's profile as changed.
type preV6ChannelFunction struct {
	GeometryInstance   string                `json:"geometryInstance,omitempty"`
	Source             ChannelFunctionSource `json:"source"`
	Attribute          string                `json:"attribute,omitempty"`
	FunctionName       string                `json:"functionName,omitempty"`
	DMXFrom            uint32                `json:"dmxFrom"`
	DMXTo              uint32                `json:"dmxTo"`
	PhysicalFrom       float64               `json:"physicalFrom"`
	PhysicalTo         float64               `json:"physicalTo"`
	ChannelSets        []ChannelSet          `json:"channelSets"`
	HasDefault         bool                  `json:"hasDefault"`
	Default            uint32                `json:"default"`
	DefaultByteCount   uint16                `json:"defaultByteCount"`
	HasHighlight       bool                  `json:"hasHighlight"`
	Highlight          uint32                `json:"highlight"`
	HighlightByteCount uint16                `json:"highlightByteCount"`
	RDMSlotType        string                `json:"rdmSlotType,omitempty"`
	RDMSlotLabel       string                `json:"rdmSlotLabel,omitempty"`
}

// PreV6ChannelFunctionsJSON returns m marshalled exactly as a pre-v6 build
// marshalled it, and true — but only when no function in m carries v6
// detail. A map WITH detail returns (nil, false): it is new data, and its
// digest is meant to differ.
func PreV6ChannelFunctionsJSON(m map[uint16]ChannelFunction) ([]byte, bool) {
	legacy := make(map[uint16]preV6ChannelFunction, len(m))
	for off, cf := range m {
		if cf.HasChannelDetail() {
			return nil, false
		}
		legacy[off] = preV6ChannelFunction{
			GeometryInstance: cf.GeometryInstance, Source: cf.Source, Attribute: cf.Attribute, FunctionName: cf.FunctionName,
			DMXFrom: cf.DMXFrom, DMXTo: cf.DMXTo, PhysicalFrom: cf.PhysicalFrom, PhysicalTo: cf.PhysicalTo,
			ChannelSets: cf.ChannelSets,
			HasDefault:  cf.HasDefault, Default: cf.Default, DefaultByteCount: cf.DefaultByteCount,
			HasHighlight: cf.HasHighlight, Highlight: cf.Highlight, HighlightByteCount: cf.HighlightByteCount,
			RDMSlotType: cf.RDMSlotType, RDMSlotLabel: cf.RDMSlotLabel,
		}
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		return nil, false
	}
	return b, true
}

// WithoutChannelDetail returns a copy of m with every schema-v6 field
// cleared — the profile as a pre-v6 import would have recorded it. Used to
// recognise "the same profile, known in less detail" (library merge).
func WithoutChannelDetail(m map[uint16]ChannelFunction) map[uint16]ChannelFunction {
	out := make(map[uint16]ChannelFunction, len(m))
	for off, cf := range m {
		cf = CloneChannelFunction(cf)
		cf.ByteCount, cf.ByteIndex, cf.FunctionsKnown = 0, 0, false
		cf.Functions = make([]FunctionRange, 0)
		out[off] = cf
	}
	return out
}

// CloneWheels returns a deep, non-nil copy of ws.
func CloneWheels(ws []Wheel) []Wheel {
	out := make([]Wheel, len(ws))
	for i, w := range ws {
		w.Slots = append(make([]WheelSlot, 0, len(w.Slots)), w.Slots...)
		out[i] = w
	}
	return out
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
	Workspace     json.RawMessage `json:"workspace,omitempty"`
	SchemaVersion int             `json:"schemaVersion,omitempty"`
	Name          string          `json:"name,omitempty"`
	CreatedAt     time.Time       `json:"createdAt,omitempty"`
	ModifiedAt    time.Time       `json:"modifiedAt,omitempty"`
	Entries       []Entry         `json:"entries,omitempty"`
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
	// v1 -> v2: a v1 file has no "channelFunctions" key at all, so every
	// entry unmarshals with a nil ChannelFunctions map (and every
	// ChannelFunction — none exist yet on a v1 file, but the same
	// normalization applies to any hand-edited/partial file that includes
	// one with a missing "channelSets" key) with a nil ChannelSets slice.
	// Normalize both to non-nil so no caller — this package's own
	// marshaller included — ever has to special-case "map/slice is nil
	// because the file predates this field" versus "map/slice is empty
	// because resolution genuinely found nothing". See this package's
	// "slices must be make([]T,0)" rule in the file doc comment.
	//
	// v2 -> v3: a v2 file has no "hasDefault"/"default"/"defaultByteCount"
	// (or the Highlight equivalents) on any ChannelFunction. Those unmarshal
	// to HasDefault==false and Default==0 — which is precisely the correct
	// "the file never told us this channel's resting value" state, so this
	// migration step deliberately does NOTHING: a v2 entry must come back
	// with its defaults marked UNKNOWN, never silently resting at 0 (see
	// TestMigrate_V2FileLoadsWithDefaultsUnknown). Because v3 added only
	// scalars, there is no new nil-slice normalization to do either — see
	// ChannelFunction.DefaultByteCount's doc comment for why that was a
	// deliberate design choice and not an accident.
	//
	// v3 -> v4: a v3 file has no "intended"/"asFound" key on any entry.
	// Both unmarshal to their zero-valued structs, in which every Known
	// companion boolean is false and AsFound.UID is "" — precisely "this
	// entry has never been committed and nothing has ever been read off a
	// fixture for it". So, like v2 -> v3, this step deliberately does
	// NOTHING: an older show file must load with as-found marked UNREAD,
	// never silently zero (see TestMigrate_V3FileLoadsWithAsFoundUnread).
	// v4 added only structs of scalars — no slices, no maps — so there is
	// no new nil normalization to do here either.
	//
	// v4 -> v5: a v4 file has no "state" key on any setting inside
	// Entry.AsFound/Entry.Intended, so each unmarshals to State=="".
	// normalizeSettingStates (asfound.go) maps that onto "unknown" and pins
	// the Known == (State=="read") invariant. Unlike the three steps above
	// this one does have work to do — but it still invents nothing: "" and
	// every unrecognized value become "unknown", NEVER "not_fitted". An old
	// show file records that we never asked; it must not come back claiming
	// we know the fixture lacks the hardware (see
	// TestMigrate_V4FileLoadsWithSettingStatesUnknown).
	//
	// v5 -> v6: no "functionsKnown"/"functions"/"byteCount"/"byteIndex" on
	// any ChannelFunction and no "wheels"/"wheelsKnown" on any entry. The
	// false Known flags are already the "full channel detail not imported"
	// state; normalizeChannelFunctions only turns the nil slices into empty
	// ones. Like v2 -> v3, nothing is derived from the first-function data.
	//
	// v6 -> v7: no "location" key on any entry; the zero Location is already
	// "not known". Nothing to do, nothing derived from the Position label.
	//
	// v7 -> v8: nothing here — the profile table is resolved by
	// decodeShowFile before migrate runs, and a v7 file's inline maps are
	// already in place.
	normalizeChannelFunctions(p.Entries)
	normalizeSettingStates(p.Entries)
	// Future: switch p.SchemaVersion { case 8: ...; p.SchemaVersion = 9 }
	p.SchemaVersion = CurrentSchemaVersion
}

// normalizeChannelFunctions turns a nil Entry.ChannelFunctions (an entry
// built by a caller that never touched the field — a plain
// `patch.Entry{...}` literal from an older call site, or JSON-unmarshalled
// from a v1 file), any nil ChannelFunction.ChannelSets/Functions (and a
// function's nil Sets), and a nil Entry.Wheels (and a wheel's nil Slots)
// into their non-nil empty equivalents, in place. Called from migrate() (the on-load
// path) AND from every Store method that installs entries into the active
// patch (Replace/Mutate/EnsureActive below) — load is not the only way a
// nil map reaches this package; a fresh `patch.Entry{...}` literal from
// internal/web never sets ChannelFunctions either, and that path never goes
// through migrate(). Idempotent and cheap on already-normalized entries.
func normalizeChannelFunctions(entries []Entry) {
	for i := range entries {
		if entries[i].ChannelFunctions == nil {
			entries[i].ChannelFunctions = make(map[uint16]ChannelFunction)
		}
		for offset, cf := range entries[i].ChannelFunctions {
			if cf.ChannelSets == nil || cf.Functions == nil || functionSetsHaveNil(cf.Functions) {
				entries[i].ChannelFunctions[offset] = CloneChannelFunction(cf)
			}
		}
		if entries[i].Wheels == nil || wheelSlotsHaveNil(entries[i].Wheels) {
			entries[i].Wheels = CloneWheels(entries[i].Wheels)
		}
	}
}

func functionSetsHaveNil(fns []FunctionRange) bool {
	for _, f := range fns {
		if f.Sets == nil {
			return true
		}
	}
	return false
}

func wheelSlotsHaveNil(ws []Wheel) bool {
	for _, w := range ws {
		if w.Slots == nil {
			return true
		}
	}
	return false
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
	cp.Workspace = append(json.RawMessage(nil), p.Workspace...)
	cp.Entries = append([]Entry(nil), p.Entries...)
	for i := range cp.Entries {
		cp.Entries[i].ChannelFunctions = make(map[uint16]ChannelFunction, len(p.Entries[i].ChannelFunctions))
		for offset, cf := range p.Entries[i].ChannelFunctions {
			cp.Entries[i].ChannelFunctions[offset] = CloneChannelFunction(cf)
		}
		cp.Entries[i].Wheels = CloneWheels(p.Entries[i].Wheels)
	}
	return cp
}

// --- persistence -----------------------------------------------------------

// Store guards one active Patch plus its on-disk persistence — same
// tmp+rename atomic-write pattern as internal/walk.Store, same "only one
// active at a time" model (see package doc comment for why).
type Store struct {
	mu         sync.Mutex
	path       string // legacy/default patch path; empty disables persistence (tests)
	activePath string // current named patch path (path for the default show)
	catalogDir string // sibling directory holding additional named patches
	patch      *Patch
}

// NewStore builds a Store persisting to path (pass "" to disable
// persistence). If path holds a valid (or migratable) patch file, it's
// loaded immediately. A malformed file is tolerated by starting empty rather
// than crashing the server — the corrupt file is left on disk untouched
// (never silently overwritten) so it can be inspected/recovered by hand.
func NewStore(path string) *Store {
	st := &Store{path: path, activePath: path}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var p Patch
			// encoding/json's default Unmarshal already ignores unknown
			// fields and leaves absent fields at their zero value — that IS
			// the "tolerant reader" (task ask), no DisallowUnknownFields
			// call here (unlike decodeJSON's request-body path in
			// internal/web, which deliberately wants strictness for typos
			// in a live API call, not for a file that must always open).
			if decoded, err := decodeShowFile(data); err == nil {
				p = decoded
				st.patch = &p
			}
		}
	}
	st.initCatalogActive()
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

// Token returns an opaque value identifying the active patch's current
// version, compared with ==. Every mutation path installs a NEW *Patch
// (Mutate, Replace, load, create, reset, recover, clear) and never edits the
// installed one in place, so the pointer changes on every change; holding
// the returned value keeps the old *Patch reachable, so its address cannot
// be reused by a later one. The Console-lite programmer uses it to rebuild
// its parameter models only when the show actually changed.
func (st *Store) Token() any {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.patch
}

// GetWithToken is Get and Token read under one lock.
func (st *Store) GetWithToken() (Patch, bool, any) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.patch == nil {
		return Patch{}, false, st.patch
	}
	return clonePatch(*st.patch), true, st.patch
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
		// The first mutation persists this draft and reports any write failure.
	}
	return clonePatch(*st.patch)
}

// Replace installs p as the active patch (CreatedAt preserved if already
// set and non-zero, else stamped now; ModifiedAt always stamped now),
// discarding whatever was active, and persists it. Used by fresh-create
// (manual "start a new patch") and AdoptFromDiscovered's fresh-create mode.
func (st *Store) Replace(p Patch) Patch {
	result, _ := st.ReplaceChecked(p) // compatibility for in-memory demo/test builders
	return result
}

// ReplaceChecked is the fallible replacement API used by all HTTP callers.
func (st *Store) ReplaceChecked(p Patch) (Patch, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.ModifiedAt = now
	p.SchemaVersion = CurrentSchemaVersion
	normalizeChannelFunctions(p.Entries)
	normalizeSettingStates(p.Entries)
	p = clonePatch(p)
	if err := savePatch(st.activePath, p); err != nil {
		return Patch{}, err
	}
	st.patch = &p
	return clonePatch(p), nil
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
	next := clonePatch(*st.patch)
	if err := fn(&next); err != nil {
		return Patch{}, err
	}
	next.ModifiedAt = time.Now()
	normalizeChannelFunctions(next.Entries)
	normalizeSettingStates(next.Entries)
	if err := savePatch(st.activePath, next); err != nil {
		return Patch{}, err
	}
	st.patch = &next
	return clonePatch(next), nil
}

// Clear discards every saved show, including the active patch. The full-reset
// flow promises to clear this rig's state, so leaving a non-active saved show
// behind would allow stale fixture commitments to resurrect after restart.
func (st *Store) Clear() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	var errs []error
	if st.path != "" {
		paths := []string{st.path, st.path + ".bak", st.catalogDir + ".active"}
		files, err := os.ReadDir(st.catalogDir)
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
		for _, file := range files {
			name := file.Name()
			id := strings.TrimSuffix(strings.TrimSuffix(name, ".bak"), ".json")
			if !file.IsDir() && validSavedPatchID(id) && (name == id+".json" || name == id+".json.bak") {
				paths = append(paths, filepath.Join(st.catalogDir, name))
			}
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	st.patch = nil
	st.activePath = st.path
	return nil
}
