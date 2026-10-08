package library

import (
	"os"
	"path/filepath"
	"testing"

	"benny512/internal/patch"
)

// TestLegacyVerifiedModeSurvivesChannelDetailUpgrade: a mode the operator
// verified on hardware under a pre-C1 build must still be verified after
// upgrading. The verification stamp is a hash of the mode's marshalled
// payload (modeHash), and C1 adds keys to that payload; without care every
// verified mode in every owner's library would silently lose its stamp on
// first load (normalizeMode clears a stamp whose hash no longer matches).
//
// testdata/legacy_v1_verified_library.json was WRITTEN BY THE PRE-C1 CODE
// (library.Store.UpsertChecked + VerifyMode on the commit before C1), not
// hand-built, so its verifiedHash is the real legacy hash.
func TestLegacyVerifiedModeSurvivesChannelDetailUpgrade(t *testing.T) {
	src, err := os.ReadFile("testdata/legacy_v1_verified_library.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lib.json")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewStore(path)
	rec, ok := st.GetByKey(KeyFor("Robe lighting s.r.o.", "BMFL Spot"))
	if !ok || len(rec.Modes) != 1 {
		t.Fatalf("legacy library did not load: %v %+v", ok, rec)
	}
	m := rec.Modes[0]
	const legacyHash = "56b9254a0bf8aff4c22a348fcf3b2dcb2c76a4d51a24904652f56cb7a299c4bc"
	if m.VerifiedHash != legacyHash || m.VerifiedAt.IsZero() || m.VerificationNote == "" {
		t.Errorf("operator verification lost on upgrade: hash=%q at=%v note=%q (want hash %s kept)",
			m.VerifiedHash, m.VerifiedAt, m.VerificationNote, legacyHash)
	}
}

// TestHarvestWithoutDetailDoesNotEraseRereadDetail: after a library mode has
// been re-read with full channel detail, "Save profiles from this patch"
// over entries imported before C1 sends the SAME profile minus the detail.
// That is re-observing the same data with less knowledge, not new data, and
// must not throw the detail away. A genuinely different profile still
// replaces it.
func TestHarvestWithoutDetailDoesNotEraseRereadDetail(t *testing.T) {
	st := NewStore("")
	first := patch.ChannelFunction{Source: patch.SourceGDTF, Attribute: "Shutter1", FunctionName: "Shutter1", DMXTo: 63,
		ChannelSets: []patch.ChannelSet{{Name: "Shutter open", DMXFrom: 32}}}
	detailed := first
	detailed.ByteCount, detailed.FunctionsKnown = 1, true
	detailed.Functions = []patch.FunctionRange{{Attribute: "Shutter1", Name: "Shutter1", DMXTo: 63, Sets: []patch.SetRange{}},
		{Attribute: "Shutter1Strobe", Name: "Shutter1Strobe", DMXFrom: 64, DMXTo: 255, Sets: []patch.SetRange{}}}
	mode := func(cf patch.ChannelFunction, wheelsKnown bool) Mode {
		return Mode{Name: "Std", Footprint: 1, ChannelFunctions: map[uint16]patch.ChannelFunction{1: cf}, WheelsKnown: wheelsKnown,
			Origin: Origin{Source: ProvenanceGDTF}}
	}
	st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(detailed, true)}})

	_, outcome := st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(first, false)}})
	rec, _ := st.Find("Robe", "BMFL")
	if got := rec.Modes[0].ChannelFunctions[1]; !got.FunctionsKnown || len(got.Functions) != 2 || !rec.Modes[0].WheelsKnown {
		t.Errorf("a detail-less harvest of the same profile erased the re-read detail (outcome %s): %+v", outcome, rec.Modes[0])
	}
	if outcome != OutcomeUnchanged {
		t.Errorf("outcome = %s, want %s — nothing new was learned", outcome, OutcomeUnchanged)
	}

	changed := first
	changed.Attribute = "Dimmer"
	st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(changed, false)}})
	rec, _ = st.Find("Robe", "BMFL")
	if got := rec.Modes[0].ChannelFunctions[1]; got.Attribute != "Dimmer" || got.FunctionsKnown {
		t.Errorf("a genuinely different profile did not replace the stored one: %+v", got)
	}
}

// TestHarvestWithoutDetailKeepsRereadGDTF10Default: C1b carries a GDTF 1.0
// DMXChannel Default into the first-function fields, so a mode re-read from
// a 1.0 file now knows a resting value its pre-C1b patch entries do not.
// Harvesting those entries is still "the same profile, known less" — it
// must not replace the re-read mode.
func TestHarvestWithoutDetailKeepsRereadGDTF10Default(t *testing.T) {
	st := NewStore("")
	old := patch.ChannelFunction{Source: patch.SourceGDTF, Attribute: "Shutter1", FunctionName: "Shutter1", DMXTo: 63, ChannelSets: []patch.ChannelSet{}}
	reread := old
	reread.HasDefault, reread.Default, reread.DefaultByteCount = true, 32, 1
	reread.ByteCount, reread.FunctionsKnown = 1, true
	reread.Functions = []patch.FunctionRange{{Attribute: "Shutter1", Name: "Shutter1", DMXTo: 255, HasDefault: true, Default: 32, Sets: []patch.SetRange{}}}
	mode := func(cf patch.ChannelFunction) Mode {
		return Mode{Name: "Std", Footprint: 1, ChannelFunctions: map[uint16]patch.ChannelFunction{1: cf}, Origin: Origin{Source: ProvenanceGDTF}}
	}
	st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(reread)}})
	if _, outcome := st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(old)}}); outcome != OutcomeUnchanged {
		t.Errorf("harvesting a pre-C1b entry over its re-read GDTF 1.0 mode: outcome %s, want %s", outcome, OutcomeUnchanged)
	}
	rec, _ := st.Find("Robe", "BMFL")
	if got := rec.Modes[0].ChannelFunctions[1]; !got.FunctionsKnown || !got.HasDefault || got.Default != 32 {
		t.Errorf("the re-read mode's detail/default was replaced: %+v", got)
	}
	// A harvested entry that states a DIFFERENT default is a real change.
	conflicting := old
	conflicting.HasDefault, conflicting.Default, conflicting.DefaultByteCount = true, 0, 1
	if _, outcome := st.Upsert(Record{Manufacturer: "Robe", Model: "BMFL", Modes: []Mode{mode(conflicting)}}); outcome != OutcomeUpdated {
		t.Errorf("a harvested entry with a different stated default: outcome %s, want %s", outcome, OutcomeUpdated)
	}
}
