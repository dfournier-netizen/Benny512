package patch

import "time"

// This file holds the schema-v4 "commit" model: the device settings a patch
// entry actually found on the fixture it is committed to ("as-found"), the
// device-level settings the entry INTENDS ("intended"), and the pure
// per-field diff between the two that the Reconcile screen renders.
//
// --- why two sides, and where each one lives -------------------------------
//
// The owner's workflow is shop-then-site: configure a rig in the shop,
// commit each patch entry to the fixture that answers for it, then on site
// confirm every light landed where it was supposed to — and when one dies,
// decommit the dead fixture and commit a substitute, which must then be
// configured to match what the original was.
//
// That last sentence is the whole reason IntendedSettings exists. "Take on
// all the old settings of the original fixture" is only implementable if
// those settings live on the ENTRY (which survives the substitution) rather
// than on the device record (which does not). Decommit therefore clears
// AsFound and leaves Intended alone; commit fills AsFound and leaves
// Intended alone.
//
// IntendedSettings deliberately holds ONLY the device-level settings that
// have no existing home on Entry. Address, universe, footprint and the
// personality's human mode NAME are already Entry.StartAddress,
// Entry.Universe, Entry.Footprint and Entry.Mode — the fields the Entries
// screen edits. Duplicating them here would create two sources of truth for
// a fixture's address, which is exactly the failure this screen exists to
// prevent. So the diff below reads those four straight off the Entry, and
// IntendedSettings adds only personality INDEX, dimmer curve, the pan/tilt
// booleans and the device label.
//
// --- "read as 0" is not "never read" ---------------------------------------
//
// Every stored setting is a small struct with an explicit Known companion
// boolean, never a bare number. A DMX address of 0, a dimmer curve index of
// 0 and a pan-invert of false are all real, meaningful values a device can
// genuinely report, and this codebase has been bitten four separate times by
// a numeric/boolean JSON field whose real zero was erased (sensor range
// bounds, Entry.Universe's `undefined - n` = NaN universe-sort bug, a
// fix-all response rendering "fixed undefined of N", and a toggle's
// enabled:false). Accordingly NOTHING in this file carries `omitempty` —
// not the Known booleans, not the values, not even the strings. Every key is
// always on the wire, and Known is the only thing that says whether the
// value beside it means anything.
//
// --- when, not just what ---------------------------------------------------
//
// Each setting carries its own At timestamp. "As-found in the shop three
// weeks ago" and "as-found ten minutes ago on site" are the difference
// between a patch the owner trusts and one he re-walks by hand, and the two
// have to be distinguishable per field, not per entry: a commit that reads
// the address fine but gets a NACK on the dimmer curve, followed by a
// re-read an hour later that finally gets the curve, leaves the two fields
// genuinely read at different times. Err carries WHY a field is unknown, so
// "this fixture does not support CURVE" reads as a fact about the fixture
// rather than as a bug in the app.
//
// At is a time.Time and therefore always marshals (a zero time is
// "0001-01-01T00:00:00Z", never an absent key). Consumers must look at Known
// first; At on a Known==false setting is meaningless, not a claim.

// SettingUint16 is one 16-bit device setting (DMX address, universe,
// footprint). See the file comment for why Known/At/Err are always present.
type SettingUint16 struct {
	Known bool      `json:"known"`
	Value uint16    `json:"value"`
	At    time.Time `json:"at"`
	Err   string    `json:"err"`
}

// SettingIndex is one RDM indexed choice (DMX personality, dimmer curve): a
// 1-based current index, the count of available choices, and the device's
// own human label for the current index when it could be fetched.
//
// CountKnown is separate from Known because a device can answer the choice
// GET (giving a current index) while its per-index DESCRIPTION GET NACKs —
// current known, label unknown — and because a count of 0 would otherwise be
// indistinguishable from "we never asked".
type SettingIndex struct {
	Known      bool      `json:"known"`
	Value      uint8     `json:"value"`
	Count      uint8     `json:"count"`
	CountKnown bool      `json:"countKnown"`
	Label      string    `json:"label"`
	At         time.Time `json:"at"`
	Err        string    `json:"err"`
}

// SettingBool is one boolean device setting (pan invert, tilt invert,
// pan/tilt swap). Known is emphatically NOT redundant with Value==false:
// "this fixture reports pan invert OFF" and "we never managed to read pan
// invert" are different facts, and only one of them is worth pushing a
// change against.
type SettingBool struct {
	Known bool      `json:"known"`
	Value bool      `json:"value"`
	At    time.Time `json:"at"`
	Err   string    `json:"err"`
}

// SettingText is one free-text device setting (device label).
type SettingText struct {
	Known bool      `json:"known"`
	Value string    `json:"value"`
	At    time.Time `json:"at"`
	Err   string    `json:"err"`
}

// IntendedSettings are the device-level settings this entry intends its
// fixture to be configured with — the settings a substitute fixture inherits
// when it is committed to this entry. See the file comment for why address/
// universe/footprint/mode-name are NOT here.
type IntendedSettings struct {
	Personality SettingIndex `json:"personality"`
	DimmerCurve SettingIndex `json:"dimmerCurve"`
	PanInvert   SettingBool  `json:"panInvert"`
	TiltInvert  SettingBool  `json:"tiltInvert"`
	PanTiltSwap SettingBool  `json:"panTiltSwap"`
	DeviceLabel SettingText  `json:"deviceLabel"`
}

// AsFoundSettings is what was actually read off the committed device. UID
// records WHICH device these readings came from — deliberately duplicated
// from Entry.ConfirmedUID so a stale AsFound block can never be silently
// attributed to a newly committed fixture: ReadFromUID below is what the
// diff checks before trusting any of it.
type AsFoundSettings struct {
	// UID is the RDM UID (string form) these readings came off. Empty means
	// nothing has ever been read for this entry.
	UID string `json:"uid"`
	// ReadAt is when the most recent read PASS ran, whatever it managed to
	// resolve. Per-field At timestamps are the authoritative "when" for any
	// individual value (a field that NACKed in this pass keeps whatever it
	// had, including nothing); ReadAt is the coarse "when did we last look
	// at this fixture at all" the UI leads with.
	ReadAt time.Time `json:"readAt"`

	Universe     SettingUint16 `json:"universe"`
	StartAddress SettingUint16 `json:"startAddress"`
	Footprint    SettingUint16 `json:"footprint"`
	Personality  SettingIndex  `json:"personality"`
	DimmerCurve  SettingIndex  `json:"dimmerCurve"`
	PanInvert    SettingBool   `json:"panInvert"`
	TiltInvert   SettingBool   `json:"tiltInvert"`
	PanTiltSwap  SettingBool   `json:"panTiltSwap"`
	DeviceLabel  SettingText   `json:"deviceLabel"`
}

// ReadFromUID reports whether this AsFound block was read off uid. A commit
// to a different fixture leaves stale readings unattributable, which is the
// whole point — a substituted fixture must never inherit the DEAD one's
// as-found values and read as "matching".
func (a AsFoundSettings) ReadFromUID(uid string) bool {
	return uid != "" && a.UID == uid
}

// --- the diff --------------------------------------------------------------

// DiffField identifies one comparable setting. These are wire identifiers:
// the client sends them back verbatim in a push/adopt request, so they are
// stable API surface, not display strings.
type DiffField string

// Diff fields, in the order DiffEntry emits them (address first — it is the
// one the owner checks on every single fixture on a show site).
const (
	FieldStartAddress DiffField = "startAddress"
	FieldUniverse     DiffField = "universe"
	FieldFootprint    DiffField = "footprint"
	FieldPersonality  DiffField = "personality"
	FieldDimmerCurve  DiffField = "dimmerCurve"
	FieldPanInvert    DiffField = "panInvert"
	FieldTiltInvert   DiffField = "tiltInvert"
	FieldPanTiltSwap  DiffField = "panTiltSwap"
	FieldDeviceLabel  DiffField = "deviceLabel"
)

// DiffState classifies one field's comparison.
type DiffState string

// Diff states.
const (
	// DiffMatch: both sides known and equal. Nothing to do.
	DiffMatch DiffState = "match"
	// DiffDiffers: both sides known and unequal. This is the one that gets
	// an "Apply to fixture" button — and NOTHING is written to the fixture
	// without that explicit press (this project's standing Apply-to-confirm
	// contract; writing an address to the wrong fixture on a show site is a
	// real cost).
	DiffDiffers DiffState = "differs"
	// DiffIntendedUnset: the fixture told us a value but the patch has no
	// intention for this field. This is the SHOP case — the owner
	// configured the light by hand and wants the patch to learn what he
	// did, via "Adopt as intended" (a patch-only write; it never touches
	// the fixture).
	DiffIntendedUnset DiffState = "intended_unset"
	// DiffUnread: the fixture has not told us this value — never asked, or
	// the read failed (FoundErr says which). Never treated as a difference:
	// an unread field is not evidence of a misconfigured light.
	DiffUnread DiffState = "unread"
)

// ValueKind tells the client HOW to render a field's numbers. It exists
// because of one hard project invariant: server-generated user-facing
// strings must never state a universe number. Universe display is 0-based
// on the wire and base-dependent on screen, and JS's UI.formatUniverse /
// UI.parseUniverse are the single conversion point. So this package emits
// STRUCTURED values plus the kind, and the presentation layer composes the
// sentence — there is deliberately no rendered "universe 3" string anywhere
// in a DiffLine.
type ValueKind string

// Value kinds.
const (
	// KindUniverse: a raw Art-Net Port-Address. The client MUST run it
	// through UI.formatUniverse before showing it and must never reformat
	// an already-displayed universe string under a new base.
	KindUniverse ValueKind = "universe"
	// KindNumber: a plain integer shown as-is (DMX address, footprint).
	KindNumber ValueKind = "number"
	// KindIndex: a 1-based RDM choice index, shown with its label and count
	// when known.
	KindIndex ValueKind = "index"
	// KindBool: shown as words, never as colour alone.
	KindBool ValueKind = "bool"
	// KindText: shown verbatim.
	KindText ValueKind = "text"
)

// DiffLine is one field's intended-vs-as-found comparison.
//
// The Intended*/Found* value fields are a flat set rather than a nested
// value object because a nested one would need its own presence flag per
// side anyway, and flattening keeps the JS renderer a single switch on
// ValueKind. Read them according to Kind: KindUniverse/KindNumber/KindIndex
// use *Num, KindBool uses *Bool, KindText uses *Text. As everywhere else in
// this file, no field carries `omitempty`: a found value of 0 and a found
// bool of false are real readings.
type DiffLine struct {
	Field DiffField `json:"field"`
	// Label is a STATIC field name ("DMX address", "Dimmer curve") — never
	// a composed sentence, and never one containing a universe number. See
	// ValueKind's doc comment.
	Label string    `json:"label"`
	Kind  ValueKind `json:"kind"`
	State DiffState `json:"state"`
	// Pushable is whether this field can be written to the fixture over
	// RDM at all. Universe and footprint are false: a fixture's Art-Net
	// universe is a property of the node/DMX line feeding it, and footprint
	// follows from the personality. A DiffDiffers line with Pushable false
	// is still worth showing — it means "go move the DMX line", which is
	// exactly the kind of thing the owner needs to discover in the shop
	// rather than on site — it just gets no Apply button.
	Pushable bool `json:"pushable"`

	IntendedKnown bool   `json:"intendedKnown"`
	IntendedNum   int64  `json:"intendedNum"`
	IntendedBool  bool   `json:"intendedBool"`
	IntendedText  string `json:"intendedText"`
	// IntendedLabel is the human label for an intended KindIndex value
	// (e.g. the mode name), when the patch knows one.
	IntendedLabel string `json:"intendedLabel"`

	FoundKnown bool   `json:"foundKnown"`
	FoundNum   int64  `json:"foundNum"`
	FoundBool  bool   `json:"foundBool"`
	FoundText  string `json:"foundText"`
	// FoundLabel is the device's own label for an as-found KindIndex value.
	FoundLabel string `json:"foundLabel"`
	// FoundCount / FoundCountKnown are how many choices the device offers
	// for a KindIndex field — what makes "personality 2" legible as "2 of
	// 3". Zero is a real (if odd) reading, hence the companion boolean.
	FoundCount      uint8 `json:"foundCount"`
	FoundCountKnown bool  `json:"foundCountKnown"`
	// FoundAt is when this specific field was read. Zero time when
	// FoundKnown is false.
	FoundAt time.Time `json:"foundAt"`
	// FoundErr is why the field is unread, verbatim from the RDM layer
	// ("NACK: unsupported PID", a timeout, ...). Empty when the field was
	// read fine or was never attempted.
	FoundErr string `json:"foundErr"`
}

// diffLabels are the static, presentation-safe names for each field. They
// contain no numbers and therefore cannot violate the never-state-a-
// universe-number invariant; the number itself travels in IntendedNum/
// FoundNum for the client to format.
var diffLabels = map[DiffField]string{
	FieldStartAddress: "DMX address",
	FieldUniverse:     "Universe",
	FieldFootprint:    "Channel count",
	FieldPersonality:  "DMX mode",
	FieldDimmerCurve:  "Dimmer curve",
	FieldPanInvert:    "Pan invert",
	FieldTiltInvert:   "Tilt invert",
	FieldPanTiltSwap:  "Pan/tilt swap",
	FieldDeviceLabel:  "Device label",
}

// PushableFields is the set of fields an "Apply to fixture" press can
// actually write over RDM. Exported so internal/web's push handler and this
// package's diff agree on one list rather than drifting apart.
func PushableFields() map[DiffField]bool {
	return map[DiffField]bool{
		FieldStartAddress: true,
		FieldPersonality:  true,
		FieldDimmerCurve:  true,
		FieldPanInvert:    true,
		FieldTiltInvert:   true,
		FieldPanTiltSwap:  true,
		FieldDeviceLabel:  true,
	}
}

// DiffEntry compares e's intended settings against its as-found readings and
// returns one DiffLine per comparable field, always in the same order and
// always the full set (a field with nothing on either side still appears, as
// DiffUnread — the owner needs to see that the curve was never read, not to
// have the row silently vanish).
//
// Pure: no RDM, no HTTP, no clock. The returned slice is built with
// make([]DiffLine, 0, ...) and is never nil — a nil slice marshals to JSON
// `null` and has crashed the Patch screen before.
//
// If e's as-found block was read off a DIFFERENT device than e is currently
// committed to (see AsFoundSettings.ReadFromUID), every found side reads as
// unknown. That is what makes substitution safe: commit fixture B to an
// entry that still carries fixture A's readings and the diff says "unread",
// never "matches".
func DiffEntry(e Entry) []DiffLine {
	lines := make([]DiffLine, 0, len(diffLabels))
	af := e.AsFound
	if !af.ReadFromUID(e.ConfirmedUID) {
		af = AsFoundSettings{}
	}

	// Address/universe/footprint: intended comes off the Entry's own
	// fields, not off IntendedSettings — see the file comment.
	//
	// StartAddress 0 and Footprint 0 read as "intended not set" here, which
	// matches what the rest of this package already means by them:
	// collision.go's KindInvalidAddress treats a 0 address as a
	// not-yet-addressed entry and KindZeroFootprint treats a 0 footprint as
	// an unresolved import. Universe has no such sentinel — universe 0 is
	// the first perfectly ordinary Art-Net universe (see Entry.Universe's
	// doc comment and the sort bug it records) — so its intended side is
	// ALWAYS known.
	lines = append(lines, numLine(FieldStartAddress, KindNumber, true,
		e.StartAddress != 0, int64(e.StartAddress), af.StartAddress))
	lines = append(lines, numLine(FieldUniverse, KindUniverse, false,
		true, int64(e.Universe), af.Universe))
	lines = append(lines, numLine(FieldFootprint, KindNumber, false,
		e.Footprint != 0, int64(e.Footprint), af.Footprint))

	lines = append(lines, indexLine(FieldPersonality, e.Intended.Personality, af.Personality, e.Mode))
	lines = append(lines, indexLine(FieldDimmerCurve, e.Intended.DimmerCurve, af.DimmerCurve, ""))
	lines = append(lines, boolLine(FieldPanInvert, e.Intended.PanInvert, af.PanInvert))
	lines = append(lines, boolLine(FieldTiltInvert, e.Intended.TiltInvert, af.TiltInvert))
	lines = append(lines, boolLine(FieldPanTiltSwap, e.Intended.PanTiltSwap, af.PanTiltSwap))
	lines = append(lines, textLine(FieldDeviceLabel, e.Intended.DeviceLabel, af.DeviceLabel))
	return lines
}

// classify is the one place the four DiffStates are decided, so every field
// type answers the question identically.
func classify(intendedKnown, foundKnown, equal bool) DiffState {
	switch {
	case !foundKnown:
		return DiffUnread
	case !intendedKnown:
		return DiffIntendedUnset
	case equal:
		return DiffMatch
	default:
		return DiffDiffers
	}
}

func base(f DiffField, kind ValueKind, pushable bool) DiffLine {
	return DiffLine{Field: f, Label: diffLabels[f], Kind: kind, Pushable: pushable}
}

func numLine(f DiffField, kind ValueKind, pushable bool, intendedKnown bool, intended int64, found SettingUint16) DiffLine {
	l := base(f, kind, pushable)
	l.IntendedKnown, l.IntendedNum = intendedKnown, intended
	l.FoundKnown, l.FoundNum, l.FoundAt, l.FoundErr = found.Known, int64(found.Value), found.At, found.Err
	l.State = classify(intendedKnown, found.Known, intended == int64(found.Value))
	return l
}

func indexLine(f DiffField, intended, found SettingIndex, intendedNameFallback string) DiffLine {
	l := base(f, KindIndex, true)
	l.IntendedKnown, l.IntendedNum, l.IntendedLabel = intended.Known, int64(intended.Value), intended.Label
	// A patch entry can carry a mode NAME (Entry.Mode) without ever having
	// had a personality INDEX committed — a GDTF/MVR import gives the
	// former and never the latter. Showing that name is genuinely useful
	// ("intended: Standard 20ch, found: 8ch Basic"), but it is NOT an
	// intention this screen can push, because there is no index to SET. So
	// it fills the label only; IntendedKnown stays false and the line stays
	// DiffIntendedUnset rather than claiming a comparison it cannot make.
	if !intended.Known && l.IntendedLabel == "" {
		l.IntendedLabel = intendedNameFallback
	}
	l.FoundKnown, l.FoundNum, l.FoundLabel = found.Known, int64(found.Value), found.Label
	l.FoundCount, l.FoundCountKnown = found.Count, found.CountKnown
	l.FoundAt, l.FoundErr = found.At, found.Err
	l.State = classify(intended.Known, found.Known, intended.Value == found.Value)
	return l
}

func boolLine(f DiffField, intended, found SettingBool) DiffLine {
	l := base(f, KindBool, true)
	l.IntendedKnown, l.IntendedBool = intended.Known, intended.Value
	l.FoundKnown, l.FoundBool, l.FoundAt, l.FoundErr = found.Known, found.Value, found.At, found.Err
	l.State = classify(intended.Known, found.Known, intended.Value == found.Value)
	return l
}

func textLine(f DiffField, intended, found SettingText) DiffLine {
	l := base(f, KindText, true)
	l.IntendedKnown, l.IntendedText = intended.Known, intended.Value
	l.FoundKnown, l.FoundText, l.FoundAt, l.FoundErr = found.Known, found.Value, found.At, found.Err
	l.State = classify(intended.Known, found.Known, intended.Value == found.Value)
	return l
}

// AdoptAsIntended copies the as-found value of the named fields into e's
// intended settings, stamping them at `now`. Only the fields IntendedSettings
// actually owns can be adopted (see the file comment): address, universe and
// footprint are Entry fields the Entries screen edits, and silently
// rewriting a patched address from whatever a fixture happens to be set to
// is precisely the mistake this screen exists to catch — so those are
// rejected here rather than quietly ignored, and the caller reports them.
//
// Returns the fields it actually adopted. A field whose as-found side is
// unknown is skipped (adopting "we never read it" as an intention would
// manufacture data).
func (e *Entry) AdoptAsIntended(fields []DiffField, now time.Time) []DiffField {
	done := make([]DiffField, 0, len(fields))
	if !e.AsFound.ReadFromUID(e.ConfirmedUID) {
		return done
	}
	for _, f := range fields {
		switch f {
		case FieldPersonality:
			if e.AsFound.Personality.Known {
				e.Intended.Personality = adoptIndex(e.AsFound.Personality, now)
				done = append(done, f)
			}
		case FieldDimmerCurve:
			if e.AsFound.DimmerCurve.Known {
				e.Intended.DimmerCurve = adoptIndex(e.AsFound.DimmerCurve, now)
				done = append(done, f)
			}
		case FieldPanInvert:
			if e.AsFound.PanInvert.Known {
				e.Intended.PanInvert = SettingBool{Known: true, Value: e.AsFound.PanInvert.Value, At: now}
				done = append(done, f)
			}
		case FieldTiltInvert:
			if e.AsFound.TiltInvert.Known {
				e.Intended.TiltInvert = SettingBool{Known: true, Value: e.AsFound.TiltInvert.Value, At: now}
				done = append(done, f)
			}
		case FieldPanTiltSwap:
			if e.AsFound.PanTiltSwap.Known {
				e.Intended.PanTiltSwap = SettingBool{Known: true, Value: e.AsFound.PanTiltSwap.Value, At: now}
				done = append(done, f)
			}
		case FieldDeviceLabel:
			if e.AsFound.DeviceLabel.Known {
				e.Intended.DeviceLabel = SettingText{Known: true, Value: e.AsFound.DeviceLabel.Value, At: now}
				done = append(done, f)
			}
		}
	}
	// Mode is Entry's own human-readable mirror of the personality index —
	// adopting a personality without it would leave the Entries screen
	// showing a stale mode name next to a freshly adopted index.
	if e.Intended.Personality.Known && e.Intended.Personality.Label != "" {
		e.Mode = e.Intended.Personality.Label
	}
	return done
}

func adoptIndex(src SettingIndex, now time.Time) SettingIndex {
	return SettingIndex{
		Known: true, Value: src.Value,
		Count: src.Count, CountKnown: src.CountKnown,
		Label: src.Label, At: now,
	}
}

// Decommit breaks e's relation to its committed fixture: the UID and every
// as-found reading go, the match state drops back to unresolved, and the
// INTENDED settings are left completely untouched. That asymmetry is the
// point — intended settings belong to the patch entry, as-found belongs to
// the device that was there, and substitution works precisely because
// decommitting the dead fixture does not take the entry's configuration
// with it.
func (e *Entry) Decommit() {
	e.ConfirmedUID = ""
	e.MatchState = MatchStateUnresolved
	e.AsFound = AsFoundSettings{}
}
