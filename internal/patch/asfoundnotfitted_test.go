package patch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// This file covers the schema-v5 third as-found state introduced for bench
// capture RDM-LOG24: "not fitted" — the fixture's own SUPPORTED_PARAMETERS
// does not list the PID behind this setting, so it has no such hardware.
// See asfound.go's file comment.

// TestMigrate_V4FileLoadsWithSettingStatesUnknown is the "old files must
// always open" rule for the v5 bump, and specifically the DIRECTION of the
// migration: a v4 show file has no "state" key on any setting, and every
// one of them must come back "unknown". Coming back "not_fitted" would be
// the app inventing knowledge it never had — claiming a fixture has no pan
// on the strength of a file that only ever recorded that we did not ask.
func TestMigrate_V4FileLoadsWithSettingStatesUnknown(t *testing.T) {
	v4 := []byte(`{
	  "schemaVersion": 4,
	  "name": "Site Patch",
	  "entries": [
	    {"id":"e1","name":"SL Boom 1","footprint":30,"universe":0,"startAddress":41,
	     "confirmedUid":"676C:00000001","matchState":"confirmed","channelFunctions":{},
	     "asFound":{
	       "uid":"676C:00000001","readAt":"2026-08-12T10:00:00Z",
	       "startAddress":{"known":true,"value":41,"at":"2026-08-12T10:00:00Z","err":""},
	       "panInvert":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"NACK: unsupported PID"},
	       "dimmerCurve":{"known":true,"value":0,"count":4,"countKnown":true,"label":"Linear","at":"2026-08-12T10:00:00Z","err":""}
	     },
	     "intended":{
	       "panInvert":{"known":true,"value":true,"at":"2026-08-12T10:00:00Z","err":""},
	       "tiltInvert":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":""}
	     }}
	  ]
	}`)
	var p Patch
	if err := json.Unmarshal(v4, &p); err != nil {
		t.Fatalf("a v4 file must still unmarshal: %v", err)
	}
	migrate(&p)

	if p.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d after migrate, want %d", p.SchemaVersion, CurrentSchemaVersion)
	}
	if CurrentSchemaVersion != 5 {
		t.Errorf("CurrentSchemaVersion = %d, want 5 — the as-found not-fitted state's bump", CurrentSchemaVersion)
	}
	e := p.Entries[0]

	// Pre-v5 data untouched, including a genuine read of ZERO.
	if e.ConfirmedUID != "676C:00000001" || e.StartAddress != 41 {
		t.Errorf("v4 fields were disturbed by migration: %+v", e)
	}
	if !e.AsFound.DimmerCurve.Known || e.AsFound.DimmerCurve.Value != 0 || e.AsFound.DimmerCurve.Count != 4 {
		t.Errorf("a real as-found zero was lost by migration: %+v", e.AsFound.DimmerCurve)
	}

	// The states themselves.
	for _, tc := range []struct {
		name string
		got  SettingState
		want SettingState
	}{
		{"asFound.startAddress (was read)", e.AsFound.StartAddress.State, SettingRead},
		{"asFound.dimmerCurve (was read as 0)", e.AsFound.DimmerCurve.State, SettingRead},
		{"asFound.panInvert (v4 recorded a NACK)", e.AsFound.PanInvert.State, SettingUnknown},
		{"asFound.tiltInvert (v4 file never mentions it)", e.AsFound.TiltInvert.State, SettingUnknown},
		{"asFound.deviceLabel (v4 file never mentions it)", e.AsFound.DeviceLabel.State, SettingUnknown},
		{"intended.panInvert (a stored intention)", e.Intended.PanInvert.State, SettingRead},
		{"intended.tiltInvert (no intention)", e.Intended.TiltInvert.State, SettingUnknown},
	} {
		if tc.got != tc.want {
			t.Errorf("%s state = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	// The specific thing that must never happen. A v4 file's silence is not
	// evidence about the fixture's hardware.
	for f, st := range map[DiffField]SettingState{
		FieldPanInvert:   e.AsFound.PanInvert.State,
		FieldTiltInvert:  e.AsFound.TiltInvert.State,
		FieldPanTiltSwap: e.AsFound.PanTiltSwap.State,
		FieldDimmerCurve: e.AsFound.DimmerCurve.State,
	} {
		if st == SettingNotFitted {
			t.Errorf("%s came out of a v4 file as %q — migration invented knowledge the file never contained", f, st)
		}
	}
	// The v4 NACK string survives: "unknown" still says why.
	if e.AsFound.PanInvert.Err == "" {
		t.Error("migration discarded the v4 file's reason for an unknown setting")
	}
}

// TestNormalizeSettingState_KnownAndNotFittedCannotBothBeTrue: a
// hand-edited (or future-written) file that claims a setting is both read
// and not fitted must resolve to READ — there is a value in hand, and
// showing "this fixture has no pan" beside a pan value it just reported
// would be the worst of the two wrong answers.
func TestNormalizeSettingState_KnownAndNotFittedCannotBothBeTrue(t *testing.T) {
	hostile := []byte(`{
	  "schemaVersion": 5,
	  "entries": [
	    {"id":"e1","name":"x","channelFunctions":{},
	     "asFound":{"uid":"676C:00000001",
	       "panInvert":{"known":true,"value":true,"at":"2026-09-02T05:00:00Z","err":"","state":"not_fitted"},
	       "tiltInvert":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"","state":"wat"}}}
	  ]
	}`)
	var p Patch
	if err := json.Unmarshal(hostile, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	migrate(&p)
	e := p.Entries[0]
	if e.AsFound.PanInvert.State != SettingRead {
		t.Errorf("known:true + state:not_fitted resolved to %q, want %q", e.AsFound.PanInvert.State, SettingRead)
	}
	if e.AsFound.TiltInvert.State != SettingUnknown {
		t.Errorf("an unrecognized state resolved to %q, want %q — anything we cannot interpret is unknown, never a claim about the fixture",
			e.AsFound.TiltInvert.State, SettingUnknown)
	}
}

// TestSettingState_MarshalsExplicitlyIncludingItsZeroes asserts the
// MARSHALLED BYTES. A struct assertion would read Value==false and
// State=="" identically to a present key, and this codebase has been bitten
// seven times by an `omitempty` on a field whose zero is real data — the
// not-fitted reading is precisely such a field: known:false, value:false
// and a state that carries all the meaning.
func TestSettingState_MarshalsExplicitlyIncludingItsZeroes(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	af := AsFoundSettings{
		UID: "676C:00000001", ReadAt: now,
		PanInvert:   NotFittedBool(),
		PanTiltSwap: NotFittedBool(),
		// A fixture that reports pan invert OFF — a real, meaningful false
		// that must stay distinguishable from the not-fitted one above.
		TiltInvert:  SettingBool{Known: true, Value: false, At: now, State: SettingRead},
		DimmerCurve: NotFittedIndex(),
		DeviceLabel: NotFittedText(),
		Footprint:   NotFittedUint16(),
	}
	b, err := json.Marshal(af)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"panInvert":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"panTiltSwap":{"known":false,"value":false,"at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"tiltInvert":{"known":true,"value":false,"at":"2026-09-02T05:00:00Z","err":"","state":"read"}`,
		`"footprint":{"known":false,"value":0,"at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"deviceLabel":{"known":false,"value":"","at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"}`,
		`"count":0,"countKnown":false,"label":"","at":"0001-01-01T00:00:00Z","err":"","state":"not_fitted"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled as-found is missing %s\ngot: %s", want, got)
		}
	}
	// The `state` key must be on the wire UNCONDITIONALLY, including for a
	// bare zero-valued setting. An `omitempty` on State would drop the key
	// exactly when a consumer most needs to see something is wrong, and
	// would read back as a valid-looking absence rather than a bad value.
	for _, z := range []any{SettingBool{}, SettingUint16{}, SettingIndex{}, SettingText{}} {
		zb, err := json.Marshal(z)
		if err != nil {
			t.Fatalf("marshal %T: %v", z, err)
		}
		if !strings.Contains(string(zb), `"state":""`) {
			t.Errorf("%T dropped its state key when zero-valued (an omitempty?): %s", z, zb)
		}
	}
	// Likewise the decommitted/normalized empty block: explicit "unknown"
	// on every setting, never an absent key.
	eb, err := json.Marshal(EmptyAsFound())
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if n := strings.Count(string(eb), `"state":"unknown"`); n != 9 {
		t.Errorf("normalized empty as-found carries %d explicit unknown states, want 9 (one per setting): %s", n, eb)
	}

	// Round-trips without losing the state.
	var back AsFoundSettings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.PanInvert.State != SettingNotFitted || back.TiltInvert.State != SettingRead {
		t.Errorf("states did not round-trip: pan=%q tilt=%q", back.PanInvert.State, back.TiltInvert.State)
	}
}

// TestDiffEntry_NotFittedOutranksAnIntention: an entry that INTENDS pan
// invert on, committed to a fixture that has no pan, is a real rig error
// the owner should see stated plainly. It must not read as "differs" (there
// is nothing to apply, and an Apply button would send a guaranteed-NACK
// SET), and it must not read as "unread" (we know exactly what is going on).
func TestDiffEntry_NotFittedOutranksAnIntention(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", Name: "SL Boom 1", ConfirmedUID: "676C:00000001",
		Universe: 0, StartAddress: 41, Footprint: 30,
		Intended: IntendedSettings{
			PanInvert:   SettingBool{Known: true, Value: true, At: now, State: SettingRead},
			TiltInvert:  SettingBool{Known: true, Value: true, At: now, State: SettingRead},
			DimmerCurve: SettingIndex{Known: true, Value: 2, At: now, State: SettingRead},
		},
		AsFound: AsFoundSettings{
			UID: "676C:00000001", ReadAt: now,
			PanInvert:   NotFittedBool(),
			PanTiltSwap: NotFittedBool(),
			TiltInvert:  SettingBool{Known: true, Value: true, At: now, State: SettingRead},
			DimmerCurve: SettingIndex{Known: true, Value: 3, Count: 4, CountKnown: true, At: now, State: SettingRead},
		},
	}
	lines := map[DiffField]DiffLine{}
	for _, l := range DiffEntry(e) {
		lines[l.Field] = l
	}
	if got := lines[FieldPanInvert].State; got != DiffNotFitted {
		t.Errorf("panInvert (intended on, fixture has no pan) = %q, want %q", got, DiffNotFitted)
	}
	if got := lines[FieldPanTiltSwap].State; got != DiffNotFitted {
		t.Errorf("panTiltSwap = %q, want %q", got, DiffNotFitted)
	}
	// The rest of the diff still works exactly as before.
	if got := lines[FieldTiltInvert].State; got != DiffMatch {
		t.Errorf("tiltInvert = %q, want %q", got, DiffMatch)
	}
	if got := lines[FieldDimmerCurve].State; got != DiffDiffers {
		t.Errorf("dimmerCurve (intended 2, found 3) = %q, want %q", got, DiffDiffers)
	}
	// A not-fitted line must never claim a device reading.
	if l := lines[FieldPanInvert]; l.FoundKnown || l.FoundErr != "" {
		t.Errorf("not-fitted line claims a reading or an error: %+v", l)
	}
	// Every field still appears — the row must not vanish.
	if len(DiffEntry(e)) != len(diffLabels) {
		t.Errorf("DiffEntry returned %d lines, want the full set of %d", len(DiffEntry(e)), len(diffLabels))
	}
}

// TestDiffEntry_SubstitutionGuardStillWinsOverNotFitted: the substitution
// guard is the property that must survive this change. As-found readings
// belonging to a DIFFERENT device than the entry is now committed to are
// discarded wholesale — and "not fitted" is a reading like any other. A
// dead JDC-1's "this fixture has no pan" must never be attributed to the
// moving light substituted in for it.
func TestDiffEntry_SubstitutionGuardStillWinsOverNotFitted(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", ConfirmedUID: "676C:00000002", // the SUBSTITUTE
		AsFound: AsFoundSettings{
			UID: "676C:00000001", ReadAt: now, // the fixture that died
			PanInvert:  NotFittedBool(),
			TiltInvert: SettingBool{Known: true, Value: true, At: now, State: SettingRead},
		},
	}
	for _, l := range DiffEntry(e) {
		if l.State != DiffUnread && l.State != DiffIntendedUnset {
			t.Errorf("%s = %q on a block read off a different UID; every found side must be unknown", l.Field, l.State)
		}
		if l.State == DiffNotFitted {
			t.Errorf("%s inherited the DEAD fixture's not-fitted reading — the substitution guard is broken", l.Field)
		}
	}
	// And with the matching UID it comes straight back.
	e.ConfirmedUID = "676C:00000001"
	for _, l := range DiffEntry(e) {
		if l.Field == FieldPanInvert && l.State != DiffNotFitted {
			t.Errorf("panInvert = %q for the device it was actually read off, want %q", l.State, DiffNotFitted)
		}
	}
}

// TestAdoptAsIntended_NeverAdoptsANotFittedSetting is an invariant lock,
// not a proof of the original defect — "not fitted" did not exist before
// this change, and AdoptAsIntended's existing Known check already refuses
// it. It is here so a later relaxation of that check (to State != unknown,
// say) cannot quietly start adopting absent hardware as an intention.
//
// Adopt copies what the
// fixture reported into the patch's intentions. There is nothing to copy
// from hardware that does not exist, and adopting it would write a false
// intention onto the entry that the next substitute fixture would inherit.
func TestAdoptAsIntended_NeverAdoptsANotFittedSetting(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", ConfirmedUID: "676C:00000001",
		AsFound: AsFoundSettings{
			UID: "676C:00000001", ReadAt: now,
			PanInvert:  NotFittedBool(),
			TiltInvert: SettingBool{Known: true, Value: true, At: now, State: SettingRead},
		},
	}
	done := e.AdoptAsIntended([]DiffField{FieldPanInvert, FieldTiltInvert}, now)
	for _, f := range done {
		if f == FieldPanInvert {
			t.Error("adopted a not-fitted setting as an intention — that manufactures an intention from absent hardware")
		}
	}
	if e.Intended.PanInvert.Known || e.Intended.PanInvert.State == SettingRead {
		t.Errorf("intended panInvert = %+v after adopting a not-fitted reading, want untouched", e.Intended.PanInvert)
	}
	if !e.Intended.TiltInvert.Known || e.Intended.TiltInvert.State != SettingRead {
		t.Errorf("intended tiltInvert = %+v, want an adopted real read", e.Intended.TiltInvert)
	}
}

// TestStateOf_MapsEveryDiffField is likewise a completeness lock rather
// than a defect proof: it pins the DiffField -> setting mapping the
// push guard depends on. A field this map forgot would silently fall
// through to "unknown" and let a guaranteed-NACK SET onto the wire.
func TestStateOf_MapsEveryDiffField(t *testing.T) {
	af := AsFoundSettings{
		UID:          "676C:00000001",
		Universe:     NotFittedUint16(),
		StartAddress: NotFittedUint16(),
		Footprint:    NotFittedUint16(),
		Personality:  NotFittedIndex(),
		DimmerCurve:  NotFittedIndex(),
		PanInvert:    NotFittedBool(),
		TiltInvert:   NotFittedBool(),
		PanTiltSwap:  NotFittedBool(),
		DeviceLabel:  NotFittedText(),
	}
	for f := range diffLabels {
		if got := af.StateOf(f); got != SettingNotFitted {
			t.Errorf("StateOf(%q) = %q, want %q — the field is missing from the mapping", f, got, SettingNotFitted)
		}
	}
	var empty AsFoundSettings
	if got := empty.StateOf(FieldPanInvert); got != SettingUnknown {
		t.Errorf("StateOf on an empty block = %q, want %q", got, SettingUnknown)
	}
}

// TestDecommit_LeavesAnExplicitlyUnknownBlock: decommit clears every
// reading, and "cleared" must mean explicitly unknown on the wire, not an
// empty-string state and not a lingering not-fitted claim about a fixture
// this entry no longer has.
func TestDecommit_LeavesAnExplicitlyUnknownBlock(t *testing.T) {
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	e := Entry{
		ID: "e1", ConfirmedUID: "676C:00000001",
		AsFound: AsFoundSettings{
			UID: "676C:00000001", ReadAt: now,
			PanInvert:  NotFittedBool(),
			TiltInvert: SettingBool{Known: true, Value: true, At: now, State: SettingRead},
		},
	}
	e.Decommit()
	if e.AsFound != EmptyAsFound() {
		t.Errorf("decommit left %+v, want the normalized empty block", e.AsFound)
	}
	b, err := json.Marshal(e.AsFound)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"state":""`) {
		t.Errorf("a decommitted block carries an empty state on the wire:\n%s", b)
	}
	if strings.Contains(string(b), `"not_fitted"`) {
		t.Errorf("a decommitted block still claims a fixture has no hardware:\n%s", b)
	}
}
