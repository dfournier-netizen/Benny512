package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"benny512/internal/patch"
)

// TestReconcileBoard_EmptySlicesMarshalAsArraysNotNull asserts the
// MARSHALLED BYTES, not lengths. `len(x) == 0` is true for a nil slice and
// an empty one alike, so a length assertion here would pass vacuously
// against the exact bug it is written to catch: a nil slice marshals to JSON
// `null`, and reconcile.js calls .map()/.filter()/.length on all three of
// these arrays. This project has already shipped that crash once, from
// DetectCollisions' `var findings []Finding` (see patch.js's collisions
// comment) — the board has three chances to repeat it.
func TestReconcileBoard_EmptySlicesMarshalAsArraysNotNull(t *testing.T) {
	h := newHarness(t)

	// The worst case for this bug: no patch at all and no discovered
	// devices, so every one of the three slices is empty.
	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/board", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board with no patch: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"intended":[]`, `"detected":[]`, `"proposals":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("board response missing %s — a nil slice would marshal as null and crash the screen\ngot: %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("board response contains a JSON null:\n%s", body)
	}

	// And with a patch entry present, that entry's own diff must likewise
	// be an array, never null.
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/board", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board: status=%d", rr.Code)
	}
	body = rr.Body.String()
	if strings.Contains(body, `"diff":null`) {
		t.Errorf("an entry's diff marshalled as null:\n%s", body)
	}
	if !strings.Contains(body, `"diff":[{`) {
		t.Errorf("an uncommitted entry must still carry its full diff array\ngot: %s", body)
	}
	// differsCount/unreadCount must be present even at 0 — 0 differences is
	// the GOOD outcome and the most important number on the screen, and
	// reconcile.js reads them without a `|| 0` guard (the fixAllResponse
	// .Applied "fixed undefined of N" lesson).
	if !strings.Contains(body, `"differsCount":0`) {
		t.Errorf("differsCount:0 must be on the wire\ngot: %s", body)
	}
}

// TestReconcileBoard_NeverStatesAUniverseNumber guards the documented
// invariant at the HTTP boundary: universes reach the client as raw 0-based
// NUMBERS, and the sentence is composed by UI.formatUniverse at the
// presentation boundary. A server-rendered "universe 3" would be wrong under
// the other display base.
func TestReconcileBoard_NeverStatesAUniverseNumber(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Practical 1", Universe: 5, StartAddress: 100, Footprint: 10})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/board", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board: status=%d", rr.Code)
	}
	// Any JSON *string* containing the word "universe" followed by a digit
	// is a violation. The numeric field `"universe":5` is fine and required
	// — it is the value, not a sentence.
	badString := regexp.MustCompile(`"[^"]*[Uu]niverse\s+\d[^"]*"`)
	if m := badString.FindAllString(rr.Body.String(), -1); len(m) > 0 {
		t.Errorf("board response contains server-composed strings stating a universe number: %q", m)
	}
	if !strings.Contains(rr.Body.String(), `"universe":5`) {
		t.Errorf("the raw 0-based universe must reach the client as a number\ngot: %s", rr.Body.String())
	}
}

// TestUpdatePatchEntry_PreservesAsFoundAndIntended: renaming a fixture or
// fixing a typo in its notes must not silently decommit it and throw away
// every setting read off the real light. entryRequest has no asFound/
// intended fields at all, so entryFromRequest always builds them zero-valued
// — without an explicit carry-over in the update handler, one PUT wipes the
// whole commit.
func TestUpdatePatchEntry_PreservesAsFoundAndIntended(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	if rr.Code != http.StatusOK {
		t.Fatalf("create entry: status=%d", rr.Code)
	}
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	// Plant a commit directly on the store — this test is about the update
	// handler, not about the RDM read path.
	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	want := patch.AsFoundSettings{
		UID: "2222:00000001", ReadAt: now,
		StartAddress: patch.SettingUint16{Known: true, Value: 41, At: now},
		PanInvert:    patch.SettingBool{Known: true, Value: true, At: now},
	}
	wantIntended := patch.IntendedSettings{
		Personality: patch.SettingIndex{Known: true, Value: 2, Count: 3, CountKnown: true, Label: "20ch", At: now},
	}
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].ConfirmedUID = "2222:00000001"
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		pp.Entries[idx].AsFound = want
		pp.Entries[idx].Intended = wantIntended
		return nil
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	// An ordinary field edit: rename it and add a note.
	rr = doJSON(t, h.srv.Handler(), "PUT", "/api/patch/entries/"+id,
		entryRequest{Name: "Wash Left", Universe: 0, StartAddress: 41, Footprint: 20, Notes: "swapped lamp"})
	if rr.Code != http.StatusOK {
		t.Fatalf("update entry: status=%d body=%s", rr.Code, rr.Body.String())
	}

	p, _ := h.srv.PatchStore.Get()
	got := p.Entries[p.IndexOf(id)]
	if got.Name != "Wash Left" || got.Notes != "swapped lamp" {
		t.Fatalf("the edit itself did not apply: %+v", got)
	}
	if got.ConfirmedUID != "2222:00000001" || got.MatchState != patch.MatchStateConfirmed {
		t.Errorf("a plain field edit broke the commit: uid=%q state=%q", got.ConfirmedUID, got.MatchState)
	}
	if got.AsFound != want {
		t.Errorf("a plain field edit discarded the as-found readings:\n got %+v\nwant %+v", got.AsFound, want)
	}
	if got.Intended != wantIntended {
		t.Errorf("a plain field edit discarded the intended settings:\n got %+v\nwant %+v", got.Intended, wantIntended)
	}
}

// TestReconcileDecommit_ClearsAsFoundKeepsIntended is the substitution
// payoff at the HTTP boundary: decommit breaks the relation and clears every
// device reading, and leaves the entry's own intended settings — the ones a
// replacement fixture inherits — completely alone.
func TestReconcileDecommit_ClearsAsFoundKeepsIntended(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	wantIntended := patch.IntendedSettings{
		Personality: patch.SettingIndex{Known: true, Value: 2, Count: 3, CountKnown: true, Label: "20ch", At: now},
		PanInvert:   patch.SettingBool{Known: true, Value: true, At: now},
		DeviceLabel: patch.SettingText{Known: true, Value: "SL Boom 1", At: now},
	}
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].ConfirmedUID = "2222:00000001"
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		pp.Entries[idx].Intended = wantIntended
		pp.Entries[idx].AsFound = patch.AsFoundSettings{
			UID: "2222:00000001", ReadAt: now,
			StartAddress: patch.SettingUint16{Known: true, Value: 17, At: now},
			PanInvert:    patch.SettingBool{Known: true, Value: false, At: now},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/decommit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("decommit: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp commitResponse
	mustUnmarshal(t, rr, &resp)
	if resp.Entry.Committed || resp.Entry.CommittedUID != "" {
		t.Errorf("decommit response still reports a commit: %+v", resp.Entry)
	}

	p, _ := h.srv.PatchStore.Get()
	got := p.Entries[p.IndexOf(id)]
	if got.ConfirmedUID != "" || got.MatchState != patch.MatchStateUnresolved {
		t.Errorf("relation survived decommit: uid=%q state=%q", got.ConfirmedUID, got.MatchState)
	}
	if got.AsFound != (patch.AsFoundSettings{}) {
		t.Errorf("as-found survived decommit: %+v", got.AsFound)
	}
	if got.Intended != wantIntended {
		t.Errorf("decommit destroyed the intended settings a substitute must inherit:\n got %+v\nwant %+v", got.Intended, wantIntended)
	}
	if got.StartAddress != 41 {
		t.Errorf("decommit disturbed the patched address: %d", got.StartAddress)
	}

	// Every diff line must now read "unread" — a decommitted entry must
	// never show a stale green all-clear from the fixture that has gone.
	for _, l := range resp.Entry.Diff {
		if l.FoundKnown || l.State == patch.DiffMatch {
			t.Errorf("diff line %s still reports a device reading after decommit: %+v", l.Field, l)
		}
	}
}

// TestReconcilePush_RefusesWhatItCannotDefensiblyWrite. The push endpoint is
// the only one in this group that writes to a fixture, so its refusals
// matter as much as its successes.
func TestReconcilePush_RefusesWhatItCannotDefensiblyWrite(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	// An empty field list must be a 400, never "apply everything". A
	// request that accidentally sent nothing must not become a request that
	// reconfigures the whole fixture.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/push", pushRequest{Fields: []string{}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("push with no fields: status=%d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}

	// Pushing to an entry that is not committed to anything is a conflict,
	// not a silent no-op — there is no fixture to write to.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/push", pushRequest{Fields: []string{"startAddress"}})
	if rr.Code != http.StatusConflict {
		t.Errorf("push on an uncommitted entry: status=%d, want 409 (body=%s)", rr.Code, rr.Body.String())
	}

	// Same for a re-read: nothing to re-read.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/reread", nil)
	if rr.Code != http.StatusConflict {
		t.Errorf("reread on an uncommitted entry: status=%d, want 409 (body=%s)", rr.Code, rr.Body.String())
	}

	// A commit naming a UID that is not a UID must be rejected before
	// anything is stored.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/commit", commitRequest{DeviceUID: "not-a-uid"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("commit with a malformed uid: status=%d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	p, _ := h.srv.PatchStore.Get()
	if got := p.Entries[p.IndexOf(id)]; got.ConfirmedUID != "" {
		t.Errorf("a rejected commit still stored a UID: %q", got.ConfirmedUID)
	}
}

// TestReconcileAdoptIntended_IsPatchOnly: adopt is the shop direction and
// must never touch a fixture. It is also the only mutator here with no
// confirm() in the UI, precisely because of that — so the server has to hold
// up its end.
func TestReconcileAdoptIntended_IsPatchOnly(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].ConfirmedUID = "2222:00000001"
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		pp.Entries[idx].AsFound = patch.AsFoundSettings{
			UID: "2222:00000001", ReadAt: now,
			// A dimmer curve of 0 that was genuinely read. Adopting it must
			// produce Known:true / Value:0 — not "nothing to adopt".
			DimmerCurve: patch.SettingIndex{Known: true, Value: 0, Count: 3, CountKnown: true, At: now},
			PanInvert:   patch.SettingBool{Known: true, Value: true, At: now},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/adopt",
		adoptIntendedRequest{Fields: []string{"dimmerCurve", "panInvert"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("adopt: status=%d body=%s", rr.Code, rr.Body.String())
	}

	p, _ := h.srv.PatchStore.Get()
	got := p.Entries[p.IndexOf(id)]
	if !got.Intended.DimmerCurve.Known || got.Intended.DimmerCurve.Value != 0 {
		t.Errorf("adopted dimmer curve = %+v, want Known:true Value:0 (a real zero, not 'nothing to adopt')", got.Intended.DimmerCurve)
	}
	if !got.Intended.PanInvert.Known || !got.Intended.PanInvert.Value {
		t.Errorf("adopted pan invert = %+v, want Known:true Value:true", got.Intended.PanInvert)
	}
	// The as-found side must be untouched by an adopt — adopt copies, it
	// does not move.
	if !got.AsFound.PanInvert.Known {
		t.Error("adopt disturbed the as-found readings")
	}

	// And the adopted values must reach the wire with their zeroes intact.
	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch", nil)
	if !strings.Contains(rr.Body.String(), `"dimmerCurve":{"known":true,"value":0,"count":3,"countKnown":true`) {
		t.Errorf("adopted zero curve did not survive serialization\ngot: %s", rr.Body.String())
	}

	// An empty field list is a 400 here too.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+id+"/adopt", adoptIntendedRequest{Fields: []string{}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("adopt with no fields: status=%d, want 400", rr.Code)
	}
}

// TestReconcileCommit_StealingADeviceDecommitsTheOtherEntry: two patch
// entries must never claim one fixture. If they did, the next push would
// fight itself — two entries writing different addresses to the same light.
// The entry that loses the fixture keeps its own intended settings, so this
// is a move, not a loss.
func TestReconcileCommit_StealingADeviceDecommitsTheOtherEntry(t *testing.T) {
	h := newHarness(t)
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash R", Universe: 0, StartAddress: 61, Footprint: 20})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	idA := created.Patch.Entries[0].ID
	idB := created.Patch.Entries[1].ID

	now := time.Date(2026, 9, 2, 5, 0, 0, 0, time.UTC)
	intendedA := patch.IntendedSettings{PanInvert: patch.SettingBool{Known: true, Value: true, At: now}}
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(idA)
		pp.Entries[idx].ConfirmedUID = "2222:00000001"
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		pp.Entries[idx].Intended = intendedA
		pp.Entries[idx].AsFound = patch.AsFoundSettings{UID: "2222:00000001", ReadAt: now}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Entry B commits to the fixture entry A holds. There is no such device
	// in this harness's registry, so the READ fails and the response says
	// so — but the RELATION is still recorded, because the owner explicitly
	// said these two belong together and that statement should not be
	// thrown away because the light is currently unplugged.
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/patch/reconcile/"+idB+"/commit", commitRequest{DeviceUID: "2222:00000001"})
	if rr.Code != http.StatusOK {
		t.Fatalf("commit: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp commitResponse
	mustUnmarshal(t, rr, &resp)
	if resp.ReadError == "" {
		t.Error("committing to an unreachable device must report why nothing could be read")
	}

	p, _ := h.srv.PatchStore.Get()
	a := p.Entries[p.IndexOf(idA)]
	b := p.Entries[p.IndexOf(idB)]
	if b.ConfirmedUID != "2222:00000001" {
		t.Errorf("entry B did not take the fixture: %q", b.ConfirmedUID)
	}
	if a.ConfirmedUID != "" {
		t.Errorf("two entries now claim one fixture — entry A still holds %q", a.ConfirmedUID)
	}
	if a.AsFound != (patch.AsFoundSettings{}) {
		t.Errorf("entry A kept stale readings for a fixture it no longer holds: %+v", a.AsFound)
	}
	if a.Intended != intendedA {
		t.Errorf("losing a fixture destroyed entry A's intended settings:\n got %+v\nwant %+v", a.Intended, intendedA)
	}
}

// TestReconcileBoard_CommittedRelationComesFromThePatchNotTheMatcher: the
// board's committed pairing is a STORED FACT, not a score. The old screen
// re-derived pairings from the matcher on every refresh, which is why a
// pairing could never actually be committed — this asserts the new one reads
// the patch.
func TestReconcileBoard_CommittedRelationComesFromThePatchNotTheMatcher(t *testing.T) {
	h := newHarness(t)
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "Wash L", Universe: 0, StartAddress: 41, Footprint: 20})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID

	// No such device is discovered in this harness — the matcher can
	// propose nothing at all — yet the commit must still show.
	if _, err := h.srv.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		pp.Entries[idx].ConfirmedUID = "2222:0000BEEF"
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr = doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/board", nil)
	var bd boardResponse
	mustUnmarshal(t, rr, &bd)
	if len(bd.Intended) != 1 {
		t.Fatalf("expected 1 intended row, got %d", len(bd.Intended))
	}
	row := bd.Intended[0]
	if !row.Committed || row.CommittedUID != "2222:0000BEEF" {
		t.Errorf("committed relation lost: %+v", row)
	}
	// The fixture is not answering, and that must be visible as its own
	// state — distinct from both "committed and present" and "never
	// committed". This is the "it did not make it to site" case.
	if row.DeviceOnline {
		t.Error("DeviceOnline = true for a device that is not discovered")
	}
	// A committed entry must not also be offered a proposal for the same
	// thing — that would invite the owner to re-litigate a decision he has
	// already made.
	for _, p := range bd.Proposals {
		if p.EntryID == id {
			t.Errorf("a committed entry was still offered a proposal: %+v", p)
		}
	}
}

// TestBoardResponse_DiffLineNumericZeroesAreOnTheWire: the board embeds
// patch.DiffLine, and reconcile.js branches on foundKnown/intendedKnown then
// reads the number beside it. Asserting the BYTES here (not the struct) is
// what makes this catch an `omitempty` sneaking onto DiffLine — a struct
// assertion would read 0 either way.
func TestBoardResponse_DiffLineNumericZeroesAreOnTheWire(t *testing.T) {
	h := newHarness(t)
	// Universe 0 with a start address of 0: every numeric on the intended
	// side is a zero, and universe 0 is a REAL universe (Entry.Universe's
	// doc comment and the sort bug it records).
	doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{Name: "U0", Universe: 0, StartAddress: 0, Footprint: 0})

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/patch/reconcile/board", nil)
	body := rr.Body.String()
	for _, want := range []string{
		`"intendedNum":0`, `"foundNum":0`, `"intendedBool":false`, `"foundBool":false`,
		`"foundKnown":false`, `"foundCount":0`, `"foundCountKnown":false`, `"pushable":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("diff line is missing %s on the wire\ngot: %s", want, body)
		}
	}
	// Sanity: the universe line really is there and really is kind
	// "universe", which is what tells the client to run UI.formatUniverse.
	var bd boardResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &bd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, l := range bd.Intended[0].Diff {
		if l.Field == patch.FieldUniverse {
			found = true
			if l.Kind != patch.KindUniverse {
				t.Errorf("universe line kind = %q, want %q", l.Kind, patch.KindUniverse)
			}
			if !l.IntendedKnown {
				t.Error("universe 0 must be a real intention, not an unset one")
			}
		}
	}
	if !found {
		t.Error("no universe line in the diff")
	}
}
