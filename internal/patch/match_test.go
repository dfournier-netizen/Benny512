package patch

import "testing"

func rowFor(rep Report, entryID string) (Row, bool) {
	for _, r := range rep.Rows {
		if r.EntryID == entryID {
			return r, true
		}
	}
	return Row{}, false
}

func deviceRowFor(rep Report, uid string) (Row, bool) {
	for _, r := range rep.Rows {
		if r.DeviceUID == uid && r.EntryID == "" {
			return r, true
		}
	}
	return Row{}, false
}

func TestTokenContainment(t *testing.T) {
	// entry (patch free text) is a SUPERSET of the device's single Model
	// field — the common real-world shape (patch says "Chauvet Rogue
	// Outcast 2X Wash", the device's DEVICE_MODEL_DESCRIPTION says just
	// "Rogue Outcast 2X Wash"). Every ref (device) token must appear in
	// text (entry).
	entry := normalizeTokens("Chauvet Rogue Outcast 2X Wash")
	device := normalizeTokens("Rogue Outcast 2X Wash")
	if got := tokenContainment(entry, device); got < 0.99 {
		t.Errorf("containment(entry, device) = %v, want ~1.0 (every device token is present in the patch text)", got)
	}
	// The reverse direction is a partial match: the entry's extra
	// "Chauvet" token isn't in ref, but containment is defined relative to
	// ref's token count, so this is a different (lower) number — not
	// exercised further here, just documented so a future reader isn't
	// surprised tokenContainment(a,b) != tokenContainment(b,a) in general.
	// Punctuation/case/whitespace must not matter.
	a := normalizeTokens("rogue-outcast_2X   WASH!!")
	b := normalizeTokens("Rogue Outcast 2x wash")
	if got := tokenContainment(a, b); got < 0.99 {
		t.Errorf("normalization should ignore case/punctuation/whitespace, got %v", got)
	}
}

func TestReconcile_SimpleAddressMatch(t *testing.T) {
	entries := []Entry{{ID: "e1", Name: "Wash 1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20}}
	devices := []DiscoveredDevice{{UID: "1900:00000001", Universe: 0, StartAddress: 1, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"}}
	rep := Reconcile(entries, devices)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusMatched {
		t.Fatalf("row = %+v, ok=%v, want StatusMatched", row, ok)
	}
	if row.DeviceUID != devices[0].UID {
		t.Errorf("DeviceUID = %q, want %q", row.DeviceUID, devices[0].UID)
	}
}

func TestReconcile_PlantedAddressMismatch(t *testing.T) {
	// Entry believes it's at address 1; the device that corroborates by
	// type is actually sitting at address 50 and nothing is at address 1.
	entries := []Entry{{ID: "e1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20}}
	devices := []DiscoveredDevice{{UID: "1900:00000001", Universe: 0, StartAddress: 50, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"}}
	rep := Reconcile(entries, devices)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusAddressMismatch {
		t.Fatalf("row = %+v, ok=%v, want StatusAddressMismatch", row, ok)
	}
	if row.DeviceUID != devices[0].UID {
		t.Errorf("DeviceUID = %q, want %q (found via type corroboration)", row.DeviceUID, devices[0].UID)
	}
	foundModel := false
	for _, e := range row.Evidence {
		if e.Kind == EvidenceModel {
			foundModel = true
		}
	}
	if !foundModel {
		t.Errorf("evidence should cite the model match: %+v", row.Evidence)
	}
}

func TestReconcile_Missing(t *testing.T) {
	entries := []Entry{{ID: "e1", FixtureType: "Totally Unknown Fixture", Universe: 0, StartAddress: 1, Footprint: 5}}
	rep := Reconcile(entries, nil)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusMissing {
		t.Fatalf("row = %+v, ok=%v, want StatusMissing", row, ok)
	}
}

func TestReconcile_Unpatched(t *testing.T) {
	devices := []DiscoveredDevice{{UID: "1900:00000099", Universe: 0, StartAddress: 100, AddressKnown: true}}
	rep := Reconcile(nil, devices)
	row, ok := deviceRowFor(rep, "1900:00000099")
	if !ok || row.Status != StatusUnpatched {
		t.Fatalf("row = %+v, ok=%v, want StatusUnpatched", row, ok)
	}
}

func TestReconcile_SwappedPair(t *testing.T) {
	// Two entries, two devices, physically swapped: the device sitting at
	// entry A's patched address is actually entry B's fixture type, and
	// vice versa. The matcher must catch the swap via corroboration rather
	// than trusting address alone.
	entries := []Entry{
		{ID: "eA", Name: "Fixture A", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20},
		{ID: "eB", Name: "Fixture B", FixtureType: "Martin MAC Aura XB", Universe: 0, StartAddress: 21, Footprint: 30},
	}
	devices := []DiscoveredDevice{
		// Sits at eA's address but is actually fixture B's type.
		{UID: "uidAtAddr1", Universe: 0, StartAddress: 1, AddressKnown: true, Footprint: 30, FootprintKnown: true, Manufacturer: "Martin", Model: "MAC Aura XB"},
		// Sits at eB's address but is actually fixture A's type.
		{UID: "uidAtAddr21", Universe: 0, StartAddress: 21, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
	}
	rep := Reconcile(entries, devices)

	rowA, ok := rowFor(rep, "eA")
	if !ok {
		t.Fatal("missing row for eA")
	}
	if rowA.DeviceUID != "uidAtAddr21" {
		t.Errorf("eA (Rogue Outcast) should pair with uidAtAddr21 (corroborated by type), got %q status=%s", rowA.DeviceUID, rowA.Status)
	}
	if rowA.Status != StatusAddressMismatch {
		t.Errorf("eA status = %s, want StatusAddressMismatch (device found elsewhere)", rowA.Status)
	}

	rowB, ok := rowFor(rep, "eB")
	if !ok {
		t.Fatal("missing row for eB")
	}
	if rowB.DeviceUID != "uidAtAddr1" {
		t.Errorf("eB (MAC Aura) should pair with uidAtAddr1 (corroborated by type), got %q status=%s", rowB.DeviceUID, rowB.Status)
	}
}

func TestReconcile_DuplicateIdenticalFixturesDifferentAddresses(t *testing.T) {
	// Two identical fixture types, each correctly addressed — must NOT be
	// flagged ambiguous; address match should cleanly disambiguate them.
	entries := []Entry{
		{ID: "e1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20},
		{ID: "e2", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 21, Footprint: 20},
	}
	devices := []DiscoveredDevice{
		{UID: "d1", Universe: 0, StartAddress: 1, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
		{UID: "d2", Universe: 0, StartAddress: 21, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
	}
	rep := Reconcile(entries, devices)
	r1, _ := rowFor(rep, "e1")
	r2, _ := rowFor(rep, "e2")
	if r1.Status != StatusMatched || r1.DeviceUID != "d1" {
		t.Errorf("e1 = %+v, want Matched to d1", r1)
	}
	if r2.Status != StatusMatched || r2.DeviceUID != "d2" {
		t.Errorf("e2 = %+v, want Matched to d2", r2)
	}
}

func TestReconcile_AmbiguousIdenticalFixturesBothMisaddressed(t *testing.T) {
	// Two identical entries, NEITHER device sits at either patched address
	// — corroboration alone can't tell them apart, so both entries must be
	// Ambiguous with both devices listed as candidates.
	entries := []Entry{
		{ID: "e1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20},
		{ID: "e2", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 21, Footprint: 20},
	}
	devices := []DiscoveredDevice{
		{UID: "d1", Universe: 0, StartAddress: 100, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
		{UID: "d2", Universe: 0, StartAddress: 120, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
	}
	rep := Reconcile(entries, devices)
	for _, id := range []string{"e1", "e2"} {
		row, ok := rowFor(rep, id)
		if !ok || row.Status != StatusAmbiguous {
			t.Fatalf("%s = %+v, ok=%v, want StatusAmbiguous", id, row, ok)
		}
		if len(row.Candidates) != 2 {
			t.Errorf("%s candidates = %+v, want 2", id, row.Candidates)
		}
	}
}

func TestReconcile_NoDeviceInfoYet(t *testing.T) {
	// A device that hasn't had DEVICE_INFO fetched yet: FootprintKnown
	// false. Footprint corroboration must be skipped (not treated as a
	// mismatch), while manufacturer/model (fetched independently, e.g. via
	// MANUFACTURER_LABEL) can still corroborate.
	entries := []Entry{{ID: "e1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20}}
	devices := []DiscoveredDevice{{UID: "d1", Universe: 0, StartAddress: 1, AddressKnown: true, FootprintKnown: false, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"}}
	rep := Reconcile(entries, devices)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusMatched {
		t.Fatalf("row = %+v, ok=%v, want StatusMatched even without DEVICE_INFO", row, ok)
	}
	for _, e := range row.Evidence {
		if e.Kind == EvidenceFootprint {
			t.Errorf("footprint evidence should not appear when the device's footprint is unknown: %+v", row.Evidence)
		}
	}
}

func TestReconcile_FuzzyNearMiss(t *testing.T) {
	// Patch text has extra descriptive words the device doesn't report;
	// containment scoring should still recognize the match.
	entries := []Entry{{ID: "e1", FixtureType: "SR Truss Wash - Chauvet Rogue Outcast 2X Wash (silver)", Universe: 0, StartAddress: 1, Footprint: 20}}
	devices := []DiscoveredDevice{
		{UID: "right", Universe: 0, StartAddress: 200, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
		{UID: "wrong", Universe: 0, StartAddress: 201, AddressKnown: true, Footprint: 16, FootprintKnown: true, Manufacturer: "Elation", Model: "Proteus Hybrid"},
	}
	rep := Reconcile(entries, devices)
	row, ok := rowFor(rep, "e1")
	if !ok {
		t.Fatal("missing row for e1")
	}
	if row.Status != StatusAddressMismatch || row.DeviceUID != "right" {
		t.Errorf("row = %+v, want AddressMismatch pairing to %q via fuzzy match", row, "right")
	}
}

func TestReconcile_ConfirmedPairingPersists(t *testing.T) {
	// A previously confirmed pairing must be trusted directly, without
	// re-scoring, even if the device's live address now disagrees.
	entries := []Entry{{ID: "e1", FixtureType: "Anything At All", Universe: 0, StartAddress: 1, Footprint: 5, ConfirmedUID: "uidX", MatchState: MatchStateConfirmed}}
	devices := []DiscoveredDevice{{UID: "uidX", Universe: 0, StartAddress: 99, AddressKnown: true, Manufacturer: "Nothing", Model: "Matching"}}
	rep := Reconcile(entries, devices)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusAddressMismatch || row.DeviceUID != "uidX" {
		t.Fatalf("row = %+v, ok=%v, want AddressMismatch to the confirmed UID despite no type corroboration", row, ok)
	}
	if row.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0 for a confirmed pairing", row.Confidence)
	}
}

func TestReconcile_ConfirmedPairingDeviceGone(t *testing.T) {
	entries := []Entry{{ID: "e1", ConfirmedUID: "uidGone", MatchState: MatchStateConfirmed}}
	rep := Reconcile(entries, nil)
	row, ok := rowFor(rep, "e1")
	if !ok || row.Status != StatusMissing {
		t.Fatalf("row = %+v, ok=%v, want StatusMissing when the confirmed device isn't discovered", row, ok)
	}
}

func TestReconcile_UnpatchedExcludesClaimedAndAmbiguousDevices(t *testing.T) {
	entries := []Entry{{ID: "e1", FixtureType: "Chauvet Rogue Outcast 2X Wash", Universe: 0, StartAddress: 1, Footprint: 20}}
	devices := []DiscoveredDevice{
		{UID: "claimed", Universe: 0, StartAddress: 1, AddressKnown: true, Footprint: 20, FootprintKnown: true, Manufacturer: "Chauvet", Model: "Rogue Outcast 2X Wash"},
		{UID: "genuinely-unpatched", Universe: 0, StartAddress: 300, AddressKnown: true, Manufacturer: "Obsidian", Model: "Netron EN4"},
	}
	rep := Reconcile(entries, devices)
	if _, ok := deviceRowFor(rep, "claimed"); ok {
		t.Error("a device claimed by a Matched row must not also appear as an Unpatched row")
	}
	row, ok := deviceRowFor(rep, "genuinely-unpatched")
	if !ok || row.Status != StatusUnpatched {
		t.Fatalf("row = %+v, ok=%v, want StatusUnpatched", row, ok)
	}
}
