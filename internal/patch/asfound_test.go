package patch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Every test in this file asserts the MARSHALLED JSON BYTES rather than
// struct fields wherever the thing under test is a serialization rule. That
// is not stylistic: `len(x) == 0` is true for both a nil and an empty slice,
// and `e.AsFound.StartAddress.Value == 0` is true for both "the device
// reported 0" and "the key was omitted from the JSON entirely" — so a
// field-level assertion passes vacuously against precisely the bugs these
// tests exist to catch. Only the bytes can tell those cases apart.

// TestEntry_MarshalJSON_AsFoundZeroesSurviveTheWire is the guard for this
// codebase's most-repeated defect: an `omitempty` on a numeric or boolean
// field whose zero is real data. It has bitten four times (sensor range
// bounds, Entry.Universe's universe-sort NaN, fixAllResponse.Applied's
// "fixed undefined of N", and a toggle's enabled:false), and the v4 commit
// model adds forty-odd such fields at once.
//
// A DMX address of 0, a dimmer curve index of 0 and a pan-invert of false
// are all real readings, so every one of these keys must be present with its
// zero value on the wire, next to a `known` flag that says whether it means
// anything.
func TestEntry_MarshalJSON_AsFoundZeroesSurviveTheWire(t *testing.T) {
	e := Entry{
		ID:           "e1",
		ConfirmedUID: "2222:00000001",
		AsFound: AsFoundSettings{
			UID: "2222:00000001",
			// Every value here is deliberately a ZERO that was genuinely
			// read off a device — the case `omitempty` destroys.
			StartAddress: SettingUint16{Known: true, Value: 0},
			Universe:     SettingUint16{Known: true, Value: 0},
			DimmerCurve:  SettingIndex{Known: true, Value: 0, Count: 0, CountKnown: true},
			PanInvert:    SettingBool{Known: true, Value: false},
		},
		Intended: IntendedSettings{
			PanTiltSwap: SettingBool{Known: true, Value: false},
		},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)

	// The two container keys must exist at all. A future refactor that
	// pointer-ised either of them would make them marshal as `null` and
	// every JS reader would need a defensive guard.
	for _, key := range []string{`"asFound":{`, `"intended":{`} {
		if !strings.Contains(got, key) {
			t.Errorf("marshalled entry is missing %s\ngot: %s", key, got)
		}
	}

	// The real zeroes, each paired with its Known companion.
	wantSubstrings := []string{
		`"startAddress":{"known":true,"value":0,`,
		`"universe":{"known":true,"value":0,`,
		`"dimmerCurve":{"known":true,"value":0,"count":0,"countKnown":true,`,
		`"panInvert":{"known":true,"value":false,`,
		`"panTiltSwap":{"known":true,"value":false,`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled entry is missing %q — a real zero was erased from the wire\ngot: %s", want, got)
		}
	}

	// And the never-read fields must ALSO be fully present, so a client can
	// tell "known:false" from "key absent" without guessing.
	if !strings.Contains(got, `"footprint":{"known":false,"value":0,`) {
		t.Errorf("an unread setting must still appear in full with known:false\ngot: %s", got)
	}
	if !strings.Contains(got, `"err":""`) {
		t.Errorf("the err field must always be on the wire\ngot: %s", got)
	}

	// Round-trip: a present zero must come back as a present zero.
	var back Entry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.AsFound.StartAddress.Known || back.AsFound.StartAddress.Value != 0 {
		t.Errorf("round-tripped startAddress = %+v, want Known:true Value:0", back.AsFound.StartAddress)
	}
	if !back.AsFound.PanInvert.Known || back.AsFound.PanInvert.Value {
		t.Errorf("round-tripped panInvert = %+v, want Known:true Value:false", back.AsFound.PanInvert)
	}
}

// TestMigrate_V3FileLoadsWithAsFoundUnread is the "old show files must
// always open" rule for the v3 -> v4 bump. The owner keeps these files
// between the shop and the site, and a v3 file loading with as-found
// silently reading as ZERO rather than UNREAD would be actively dangerous:
// it would claim the fixture reported a DMX address of 0 and a dimmer curve
// of 0 when nothing has ever been read off it.
func TestMigrate_V3FileLoadsWithAsFoundUnread(t *testing.T) {
	// A minimal but realistic v3 file: schemaVersion 3, an entry with a
	// confirmed pairing, and NO "asFound"/"intended" keys anywhere.
	v3 := []byte(`{
	  "schemaVersion": 3,
	  "name": "Shop Patch",
	  "entries": [
	    {"id":"e1","name":"Wash L","footprint":20,"universe":0,"startAddress":41,
	     "confirmedUid":"2222:00000001","matchState":"confirmed","channelFunctions":{}}
	  ]
	}`)
	var p Patch
	if err := json.Unmarshal(v3, &p); err != nil {
		t.Fatalf("a v3 file must still unmarshal: %v", err)
	}
	migrate(&p)

	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d after migrate, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if CurrentSchemaVersion != 5 {
		t.Errorf("CurrentSchemaVersion = %d, want 5 — the commit model's bump (4) plus the as-found not-fitted state (5)", CurrentSchemaVersion)
	}
	e := p.Entries[0]

	// The pre-v4 data must be untouched.
	if e.ConfirmedUID != "2222:00000001" || e.StartAddress != 41 {
		t.Errorf("v3 fields were disturbed by migration: %+v", e)
	}

	// And every as-found reading must be UNREAD, not zero.
	if e.AsFound.UID != "" {
		t.Errorf("AsFound.UID = %q on a migrated v3 entry, want empty", e.AsFound.UID)
	}
	if e.AsFound.StartAddress.Known {
		t.Errorf("AsFound.StartAddress.Known = true on a migrated v3 entry — a value was invented")
	}
	if e.AsFound.DimmerCurve.Known || e.AsFound.PanInvert.Known || e.AsFound.DeviceLabel.Known {
		t.Errorf("a migrated v3 entry claims settings it never read: %+v", e.AsFound)
	}
	if e.Intended.Personality.Known || e.Intended.DimmerCurve.Known {
		t.Errorf("a migrated v3 entry claims intended settings it never had: %+v", e.Intended)
	}

	// The diff must therefore say "not read" for everything the device
	// would have supplied — never "matches" and never "differs".
	for _, l := range DiffEntry(e) {
		if l.FoundKnown {
			t.Errorf("diff line %s reports a found value on a migrated v3 entry: %+v", l.Field, l)
		}
		if l.State != DiffUnread {
			t.Errorf("diff line %s state = %q on a migrated v3 entry, want %q", l.Field, l.State, DiffUnread)
		}
	}
}

// TestDiffEntry_MarshalJSON_IsArrayNotNull: DiffEntry's result is serialized
// straight into the reconcile board's JSON and the client calls .map() on
// it. A nil slice marshals to `null`, which has crashed the Patch screen
// before (see patch.js's collisions comment). len()==0 would be true for
// both, so this asserts the BYTES.
func TestDiffEntry_MarshalJSON_IsArrayNotNull(t *testing.T) {
	b, err := json.Marshal(DiffEntry(Entry{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got[0] != '[' {
		t.Fatalf("DiffEntry marshalled as %q, want a JSON array", got)
	}
	// Even the emptiest possible entry produces the full field set — a
	// setting the app never managed to read must still have a visible row
	// saying so, not silently vanish.
	var lines []DiffLine
	if err := json.Unmarshal(b, &lines); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(lines) != len(diffLabels) {
		t.Errorf("DiffEntry returned %d lines, want all %d fields", len(lines), len(diffLabels))
	}
}

// TestDiffEntry_States walks every state classify() can produce, on the
// field types that reach it.
func TestDiffEntry_States(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", ConfirmedUID: "2222:00000001",
		Universe: 3, StartAddress: 41, Footprint: 20,
		Intended: IntendedSettings{
			Personality: SettingIndex{Known: true, Value: 2},
			PanInvert:   SettingBool{Known: true, Value: true},
		},
		AsFound: AsFoundSettings{
			UID:          "2222:00000001",
			Universe:     SettingUint16{Known: true, Value: 3, At: now},   // match
			StartAddress: SettingUint16{Known: true, Value: 100, At: now}, // differs
			Footprint:    SettingUint16{Known: true, Value: 20, At: now},  // match
			Personality:  SettingIndex{Known: true, Value: 2, At: now},    // match
			DimmerCurve:  SettingIndex{Err: "NACK: unsupported PID"},      // unread, with a reason
			PanInvert:    SettingBool{Known: true, Value: false, At: now}, // differs
			TiltInvert:   SettingBool{Known: true, Value: true, At: now},  // intended_unset
		},
	}
	byField := map[DiffField]DiffLine{}
	for _, l := range DiffEntry(e) {
		byField[l.Field] = l
	}

	want := map[DiffField]DiffState{
		FieldUniverse:     DiffMatch,
		FieldStartAddress: DiffDiffers,
		FieldFootprint:    DiffMatch,
		FieldPersonality:  DiffMatch,
		FieldDimmerCurve:  DiffUnread,
		FieldPanInvert:    DiffDiffers,
		FieldTiltInvert:   DiffIntendedUnset,
		FieldPanTiltSwap:  DiffUnread,
		FieldDeviceLabel:  DiffUnread,
	}
	for f, wantState := range want {
		if got := byField[f].State; got != wantState {
			t.Errorf("%s state = %q, want %q (line: %+v)", f, got, wantState, byField[f])
		}
	}

	// An unread field must carry WHY, so "this fixture has no dimmer curve"
	// reads as a fact about the fixture rather than as a bug in the app.
	if byField[FieldDimmerCurve].FoundErr == "" {
		t.Error("an unread field with a recorded error must surface it in FoundErr")
	}
	// Universe and footprint are not RDM-settable; everything else is.
	if byField[FieldUniverse].Pushable || byField[FieldFootprint].Pushable {
		t.Error("universe/footprint must not be marked pushable — neither is an RDM fixture setting")
	}
	if !byField[FieldStartAddress].Pushable || !byField[FieldDimmerCurve].Pushable {
		t.Error("address and dimmer curve must be pushable")
	}
	// The as-found read time travels with the value.
	if !byField[FieldStartAddress].FoundAt.Equal(now) {
		t.Errorf("FoundAt = %v, want %v", byField[FieldStartAddress].FoundAt, now)
	}
}

// TestDiffEntry_StaleAsFoundFromAnotherFixtureReadsUnread is the
// substitution safety guard, and it is the single most important assertion
// in this file. Commit fixture B to an entry that still carries fixture A's
// readings and every found value must read as UNKNOWN — never as "matches".
// Without it, substituting a light would show a green all-clear against the
// DEAD fixture's settings and the replacement would go out unconfigured.
func TestDiffEntry_StaleAsFoundFromAnotherFixtureReadsUnread(t *testing.T) {
	e := Entry{
		ID: "e1", StartAddress: 41, Universe: 0,
		// Committed to fixture B...
		ConfirmedUID: "2222:00000002",
		// ...but the readings on file came off fixture A.
		AsFound: AsFoundSettings{
			UID:          "2222:00000001",
			StartAddress: SettingUint16{Known: true, Value: 41},
			PanInvert:    SettingBool{Known: true, Value: true},
		},
	}
	for _, l := range DiffEntry(e) {
		if l.FoundKnown {
			t.Errorf("field %s trusted a reading taken off a different fixture: %+v", l.Field, l)
		}
		if l.State == DiffMatch {
			t.Errorf("field %s reports MATCH against another fixture's readings — a substituted light would look configured when it is not", l.Field)
		}
	}

	// Same block, correctly attributed, must be trusted.
	e.ConfirmedUID = "2222:00000001"
	var sawMatch bool
	for _, l := range DiffEntry(e) {
		if l.Field == FieldStartAddress && l.State == DiffMatch {
			sawMatch = true
		}
	}
	if !sawMatch {
		t.Error("correctly-attributed readings must be trusted")
	}
}

// TestDiffEntry_NeverStatesAUniverseNumber guards the documented invariant
// that no server-generated user-facing string states a universe number. The
// sentence is composed at the presentation boundary (JS's UI.formatUniverse)
// from structured fields, because the display base is a user setting and a
// server-rendered number would be wrong under the other base.
func TestDiffEntry_NeverStatesAUniverseNumber(t *testing.T) {
	e := Entry{
		ID: "e1", ConfirmedUID: "u", Universe: 7, StartAddress: 41,
		AsFound: AsFoundSettings{UID: "u", Universe: SettingUint16{Known: true, Value: 9}},
	}
	lines := DiffEntry(e)
	b, err := json.Marshal(lines)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Every human-readable string this package produces for the universe
	// row must be free of a digit adjacent to the word.
	for _, l := range lines {
		for _, s := range []string{l.Label, l.IntendedLabel, l.FoundLabel, l.FoundErr} {
			low := strings.ToLower(s)
			if i := strings.Index(low, "universe"); i >= 0 {
				rest := strings.TrimSpace(low[i+len("universe"):])
				if rest != "" && rest[0] >= '0' && rest[0] <= '9' {
					t.Errorf("field %s produced a string stating a universe number: %q", l.Field, s)
				}
			}
		}
	}

	// The numbers must nonetheless BE there, as numbers, with the kind that
	// tells the client to format them.
	var uniLine DiffLine
	for _, l := range lines {
		if l.Field == FieldUniverse {
			uniLine = l
		}
	}
	if uniLine.Kind != KindUniverse {
		t.Errorf("universe line Kind = %q, want %q — the client cannot know to call UI.formatUniverse without it", uniLine.Kind, KindUniverse)
	}
	if uniLine.IntendedNum != 7 || uniLine.FoundNum != 9 {
		t.Errorf("universe line carries %d/%d, want the raw 0-based 7/9", uniLine.IntendedNum, uniLine.FoundNum)
	}
	// Raw values on the wire, never pre-formatted.
	if !strings.Contains(string(b), `"intendedNum":7`) || !strings.Contains(string(b), `"foundNum":9`) {
		t.Errorf("universe values must reach the client as raw numbers\ngot: %s", b)
	}
}

// TestEntry_Decommit_KeepsIntended: the asymmetry that makes substitution
// work. Decommit clears the relation and every as-found reading, and leaves
// the entry's own intended settings completely alone — including the
// Entry-level address the Entries screen owns.
func TestEntry_Decommit_KeepsIntended(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", Name: "Wash L", StartAddress: 41, Universe: 0, Footprint: 20,
		ConfirmedUID: "2222:00000001", MatchState: MatchStateConfirmed,
		Intended: IntendedSettings{
			Personality: SettingIndex{Known: true, Value: 2, Label: "20ch", At: now},
			PanInvert:   SettingBool{Known: true, Value: true, At: now},
			DeviceLabel: SettingText{Known: true, Value: "SL Boom 1", At: now},
		},
		AsFound: AsFoundSettings{
			UID: "2222:00000001", ReadAt: now,
			StartAddress: SettingUint16{Known: true, Value: 41, At: now},
			PanInvert:    SettingBool{Known: true, Value: true, At: now},
		},
	}
	intendedBefore := e.Intended
	e.Decommit()

	if e.ConfirmedUID != "" {
		t.Errorf("ConfirmedUID = %q after Decommit, want empty", e.ConfirmedUID)
	}
	if e.MatchState != MatchStateUnresolved {
		t.Errorf("MatchState = %q after Decommit, want unresolved", e.MatchState)
	}
	if e.AsFound != EmptyAsFound() {
		t.Errorf("AsFound survived Decommit: %+v", e.AsFound)
	}
	if e.Intended != intendedBefore {
		t.Errorf("Decommit disturbed Intended:\n got %+v\nwant %+v", e.Intended, intendedBefore)
	}
	// The Entry's own patched address is the patch's intention and must
	// never be touched by breaking a device relation.
	if e.StartAddress != 41 || e.Universe != 0 || e.Footprint != 20 {
		t.Errorf("Decommit disturbed the entry's patched address/universe/footprint: %+v", e)
	}
}

// TestEntry_AdoptAsIntended covers the shop direction: the fixture is right,
// and the patch should learn what was configured on it.
func TestEntry_AdoptAsIntended(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", ConfirmedUID: "2222:00000001", StartAddress: 41,
		AsFound: AsFoundSettings{
			UID:         "2222:00000001",
			Personality: SettingIndex{Known: true, Value: 3, Count: 4, CountKnown: true, Label: "20ch Extended"},
			// A zero curve index that was genuinely read: adopting it must
			// produce Known:true / Value:0, not "nothing to adopt".
			DimmerCurve: SettingIndex{Known: true, Value: 0, Count: 3, CountKnown: true},
			PanInvert:   SettingBool{Known: true, Value: true},
			TiltInvert:  SettingBool{Err: "NACK"}, // never read -> not adoptable
		},
	}
	got := e.AdoptAsIntended([]DiffField{
		FieldPersonality, FieldDimmerCurve, FieldPanInvert, FieldTiltInvert,
		// Address is an Entry field the Entries screen owns. Silently
		// rewriting a patched address from whatever a fixture happens to be
		// set to is the exact mistake this screen exists to catch, so it
		// must be refused, not quietly applied.
		FieldStartAddress,
	}, now)

	adopted := map[DiffField]bool{}
	for _, f := range got {
		adopted[f] = true
	}
	for _, want := range []DiffField{FieldPersonality, FieldDimmerCurve, FieldPanInvert} {
		if !adopted[want] {
			t.Errorf("%s was not adopted", want)
		}
	}
	if adopted[FieldTiltInvert] {
		t.Error("an unread setting must not be adopted — that would manufacture an intention from nothing")
	}
	if adopted[FieldStartAddress] {
		t.Error("the patched DMX address must never be adopted from a fixture")
	}
	if e.StartAddress != 41 {
		t.Errorf("StartAddress = %d, want the patch's own 41", e.StartAddress)
	}

	if e.Intended.Personality != (SettingIndex{Known: true, Value: 3, Count: 4, CountKnown: true, Label: "20ch Extended", At: now, State: SettingRead}) {
		t.Errorf("adopted personality = %+v", e.Intended.Personality)
	}
	// The real zero.
	if !e.Intended.DimmerCurve.Known || e.Intended.DimmerCurve.Value != 0 {
		t.Errorf("adopted dimmer curve = %+v, want Known:true Value:0", e.Intended.DimmerCurve)
	}
	// Mode is Entry's human mirror of the personality index and must follow
	// it, or the Entries screen shows a stale mode name.
	if e.Mode != "20ch Extended" {
		t.Errorf("Mode = %q after adopting a personality, want the adopted label", e.Mode)
	}

	// Adopting against readings from a different fixture must do nothing.
	e2 := Entry{
		ID: "e2", ConfirmedUID: "2222:00000002",
		AsFound: AsFoundSettings{UID: "2222:00000001", PanInvert: SettingBool{Known: true, Value: true}},
	}
	if n := e2.AdoptAsIntended([]DiffField{FieldPanInvert}, now); len(n) != 0 {
		t.Errorf("adopted %v from another fixture's readings", n)
	}
}

// TestDiffEntry_IntendedZeroAddressIsNotAnIntention: an entry that has never
// been addressed has StartAddress 0, which collision.go already treats as
// "not yet addressed" (KindInvalidAddress). The diff must agree, or it would
// tell the owner the fixture "differs" from a nonexistent intention and
// offer to write DMX address 0 to it — which SetDMXStartAddress rejects
// anyway, but should never have been offered.
func TestDiffEntry_IntendedZeroAddressIsNotAnIntention(t *testing.T) {
	e := Entry{
		ID: "e1", ConfirmedUID: "u", StartAddress: 0,
		AsFound: AsFoundSettings{UID: "u", StartAddress: SettingUint16{Known: true, Value: 17}},
	}
	for _, l := range DiffEntry(e) {
		if l.Field != FieldStartAddress {
			continue
		}
		if l.IntendedKnown {
			t.Error("a never-addressed entry must not claim an intended DMX address")
		}
		if l.State != DiffIntendedUnset {
			t.Errorf("state = %q, want %q", l.State, DiffIntendedUnset)
		}
	}

	// Universe 0, by contrast, IS a real intention — the first perfectly
	// ordinary Art-Net universe (see Entry.Universe's doc comment and the
	// sort bug it records). It must never read as "unset".
	e2 := Entry{
		ID: "e2", ConfirmedUID: "u", Universe: 0,
		AsFound: AsFoundSettings{UID: "u", Universe: SettingUint16{Known: true, Value: 0}},
	}
	for _, l := range DiffEntry(e2) {
		if l.Field != FieldUniverse {
			continue
		}
		if !l.IntendedKnown || l.State != DiffMatch {
			t.Errorf("universe 0 must be a real, matching intention, got IntendedKnown=%v state=%q", l.IntendedKnown, l.State)
		}
	}
}

// TestPushableFields_MatchesDiff keeps the push handler's writable set and
// the diff's Pushable flag from drifting apart — a diff that offers Apply on
// a field the server will refuse is a dead button, and one that hides Apply
// on a writable field is a feature the owner cannot reach.
func TestPushableFields_MatchesDiff(t *testing.T) {
	pushable := PushableFields()
	for _, l := range DiffEntry(Entry{}) {
		if pushable[l.Field] != l.Pushable {
			t.Errorf("field %s: PushableFields()=%v but DiffLine.Pushable=%v", l.Field, pushable[l.Field], l.Pushable)
		}
	}
	// Guard the count too, so adding a field to one list and not the other
	// is caught even if the new field happens to default correctly.
	if len(pushable) != 7 {
		t.Errorf("PushableFields() has %d entries, want 7 (everything but universe and footprint)", len(pushable))
	}
}
