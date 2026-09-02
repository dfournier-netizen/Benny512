package web

// reconcilecommit.go — the Reconcile screen's commit model (patch schema
// v4). This is the HTTP surface behind the owner's own description of what
// the screen is for:
//
//	"...controls to 'commit' and create a relation between the two, or to
//	 'decommit' and break that relation. We'll then store the RDM UID and
//	 fixture settings to the patch data (things like dimmer curve, address,
//	 current DMX Mode, etc). This way, I can setup my rig in the shop, test
//	 and configure, then on site, confirm each light made it where it needed
//	 to go as it was supposed to. Now, if for some reason I am not able to
//	 get that fixture and need to substitute in another of the same type,
//	 I'd like to be able to 'decommit' the old and 'commit' the new —
//	 taking on all the old settings of the original fixture, ensuring it's
//	 configured as it should."
//
// Endpoints (all under /api/patch/reconcile):
//
//	GET  /board                  -> boardResponse (the two-pane view model)
//	POST /{id}/commit  {deviceUid}        -> commitResponse
//	POST /{id}/decommit                   -> commitResponse
//	POST /{id}/reread                     -> commitResponse
//	POST /{id}/push    {fields:[...]}     -> pushResponse
//	POST /{id}/adopt   {fields:[...]}     -> commitResponse
//
// --- the one rule that governs all of them ---------------------------------
//
// COMMIT NEVER WRITES TO A FIXTURE. It records the relation and READS the
// device's current settings into the entry as "as-found", then the client
// shows a per-field diff against what the patch intends. A difference
// reaches the light only through /push, and only for the fields explicitly
// named in that request — this project's standing Apply-to-confirm contract,
// and not optional here: writing a DMX address to the wrong fixture on a
// show site is a real cost, so there is deliberately no code path in this
// file where a read, a commit, a decommit or a board refresh can emit an RDM
// SET.
//
// The one deliberate asymmetry is decommit: it clears the UID and every
// as-found reading and leaves INTENDED settings untouched (patch.Entry.
// Decommit). That is what makes substitution work at all — see asfound.go's
// file comment.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"benny512/internal/params"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/registry"
)

// --- reading as-found off a live device ------------------------------------

// readAsFound issues the GETs that make up one "as-found" pass against uid
// and returns what it managed to resolve. It NEVER returns an error for a
// field the device would not answer: a NACK on CURVE means "this fixture has
// no dimmer curve", which is a fact about the fixture and belongs in that
// field's Err string, not in an HTTP error that would make the whole commit
// look like it failed. Only a device that cannot be reached at all is a
// hard error, and that is reported by the caller.
//
// One DEVICE_INFO GET carries the footprint AND the personality index+count
// (E1.20 §10.5.1), so those two cost no extra round trip. The DMX address is
// deliberately NOT taken from DEVICE_INFO's copy of it even though it is
// sitting right there: the rest of this app resolves a device's live address
// with a dedicated GET DMX_START_ADDRESS (resolveWalkAddresses, which Rig
// Walk and the reconcile matcher both go through), and a device whose
// DEVICE_INFO carries a stale address would make the Reconcile screen's two
// panes disagree about the same fixture — the single most confusing thing
// this screen could possibly do. One source of truth per fact, even at the
// cost of a round trip.
//
// The per-field reads are fanned out concurrently, in the same shape as
// handlePatchReconcileFixAll's SET fan-out.
func (s *Server) readAsFound(ctx context.Context, uid rdm.UID) (patch.AsFoundSettings, error) {
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		return patch.AsFoundSettings{}, fmt.Errorf("device %s not currently reachable", uid)
	}
	client := params.New(s.RDM, node, uid)
	now := time.Now()
	af := patch.AsFoundSettings{UID: uid.String(), ReadAt: now}

	// Universe is NOT an RDM read. A fixture has no idea what Art-Net
	// universe feeds it; the universe is a property of the node port the
	// device answered discovery on, which the registry already knows. It is
	// still genuinely "as-found" — a light on the wrong universe is exactly
	// the kind of thing this screen exists to surface — it just is not
	// pushable, and DiffEntry marks it so.
	if f, ok := s.Registry.Fixture(uid); ok {
		af.Universe = patch.SettingUint16{Known: true, Value: f.Port.RawValue(), At: now}
	} else {
		af.Universe = patch.SettingUint16{Err: "device is not in the discovered-device registry"}
	}

	// Phase 1: everything that needs no prior answer, concurrently.
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		info    params.DeviceInfo
		infoErr error
	)
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	sub := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, deviceParamTimeout)
	}

	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.DeviceInfo(c)
		mu.Lock()
		info, infoErr = v, err
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.DMXStartAddress(c)
		mu.Lock()
		if err != nil {
			af.StartAddress = patch.SettingUint16{Err: err.Error()}
		} else {
			af.StartAddress = patch.SettingUint16{Known: true, Value: v, At: now}
		}
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.Curve(c)
		mu.Lock()
		af.DimmerCurve = indexSetting(uint8(v.Current), uint8(v.Count), now, err)
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.PanInvert(c)
		mu.Lock()
		af.PanInvert = boolSetting(v, now, err)
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.TiltInvert(c)
		mu.Lock()
		af.TiltInvert = boolSetting(v, now, err)
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.PanTiltSwap(c)
		mu.Lock()
		af.PanTiltSwap = boolSetting(v, now, err)
		mu.Unlock()
	})
	run(func() {
		c, cancel := sub()
		defer cancel()
		v, err := client.DeviceLabel(c)
		mu.Lock()
		af.DeviceLabel = textSetting(v, now, err)
		mu.Unlock()
	})
	wg.Wait()

	if infoErr != nil {
		e := infoErr.Error()
		af.Footprint = patch.SettingUint16{Err: e}
		af.Personality = patch.SettingIndex{Err: e}
	} else {
		af.Footprint = patch.SettingUint16{Known: true, Value: info.DMXFootprint, At: now}
		af.Personality = patch.SettingIndex{
			Known: true, Value: info.CurrentPersonality,
			Count: info.PersonalityCount, CountKnown: true, At: now,
		}
	}

	// Phase 2: the human labels for whichever indices phase 1 resolved. A
	// failure here leaves the index Known and the label empty — the value
	// is still trustworthy, the fixture just would not name it, and the UI
	// falls back to showing the bare index rather than dropping the row.
	//
	// A second WaitGroup rather than reusing the first: re-zeroing a
	// sync.WaitGroup by assignment copies a lock, which `go vet`'s copylocks
	// check rightly refuses.
	var wg2 sync.WaitGroup
	run2 := func(fn func()) {
		wg2.Add(1)
		go func() { defer wg2.Done(); fn() }()
	}
	if af.Personality.Known {
		run2(func() {
			c, cancel := sub()
			defer cancel()
			d, err := client.DMXPersonalityDescription(c, af.Personality.Value)
			if err == nil {
				mu.Lock()
				af.Personality.Label = d.Description
				mu.Unlock()
			}
		})
	}
	if af.DimmerCurve.Known {
		run2(func() {
			c, cancel := sub()
			defer cancel()
			d, err := client.CurveDescription(c, af.DimmerCurve.Value)
			if err == nil {
				mu.Lock()
				af.DimmerCurve.Label = d.Description
				mu.Unlock()
			}
		})
	}
	wg2.Wait()
	return af, nil
}

// indexSetting/boolSetting/textSetting fold one (value, err) read into its
// stored shape. err != nil is recorded as Known:false plus the reason —
// never as a zero value that would read as a real setting. This is the whole
// point of the Known companion booleans (asfound.go's file comment).
func indexSetting(current, count uint8, now time.Time, err error) patch.SettingIndex {
	if err != nil {
		return patch.SettingIndex{Err: err.Error()}
	}
	return patch.SettingIndex{Known: true, Value: current, Count: count, CountKnown: true, At: now}
}

func boolSetting(v bool, now time.Time, err error) patch.SettingBool {
	if err != nil {
		return patch.SettingBool{Err: err.Error()}
	}
	return patch.SettingBool{Known: true, Value: v, At: now}
}

func textSetting(v string, now time.Time, err error) patch.SettingText {
	if err != nil {
		return patch.SettingText{Err: err.Error()}
	}
	return patch.SettingText{Known: true, Value: v, At: now}
}

// --- the two-pane board ----------------------------------------------------

// intendedPaneJSON is one row of the LEFT pane: a patch entry (what the
// owner intends to have), plus whether it is committed and to what.
type intendedPaneJSON struct {
	EntryID       string `json:"entryId"`
	Name          string `json:"name"`
	FixtureType   string `json:"fixtureType"`
	Position      string `json:"position"`
	FixtureNumber string `json:"fixtureNumber"`
	// Universe is the raw 0-based Art-Net Port-Address. It is emitted as a
	// NUMBER, never as a formatted string: this server must never generate
	// a user-facing string stating a universe number, and JS's
	// UI.formatUniverse is the single conversion point. Same for every
	// other universe-valued field in this file.
	Universe     uint16 `json:"universe"`
	StartAddress uint16 `json:"startAddress"`
	Footprint    uint16 `json:"footprint"`
	Mode         string `json:"mode"`

	// Committed / CommittedUID are the relation. Committed is NOT redundant
	// with CommittedUID != "": it is the flag the client branches on, and
	// keeping the two together means a future UID format change cannot turn
	// "committed" into a string comparison scattered through the JS.
	Committed    bool   `json:"committed"`
	CommittedUID string `json:"committedUid"`
	// DeviceOnline says whether the committed device is currently among the
	// discovered devices. A committed entry whose fixture is not answering
	// is the "it did not make it to site" case and must look different from
	// both "committed and present" and "never committed".
	DeviceOnline bool `json:"deviceOnline"`

	// Diff is the per-field intended-vs-as-found comparison, always the
	// full field set and never nil (patch.DiffEntry builds it with
	// make([]DiffLine, 0, n) — a nil slice marshals to JSON `null` and has
	// crashed the Patch screen before).
	Diff []patch.DiffLine `json:"diff"`
	// DiffersCount / UnreadCount summarise Diff so the pane can badge a row
	// without the client re-deriving what the server already computed.
	// Neither carries `omitempty`: 0 differences is the good outcome and
	// the most important number on the screen.
	DiffersCount int `json:"differsCount"`
	UnreadCount  int `json:"unreadCount"`
	// AsFoundAt is when the last as-found pass ran. Zero time when the
	// entry has never been committed — "as-found in the shop three weeks
	// ago" and "as-found ten minutes ago on site" are different facts and
	// the owner's whole workflow turns on the difference.
	AsFoundAt time.Time `json:"asFoundAt"`
}

// detectedPaneJSON is one row of the RIGHT pane: a device discovered on the
// RDM line, and which patch entry (if any) has committed to it.
type detectedPaneJSON struct {
	UID          string `json:"uid"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Label        string `json:"label"`
	Universe     uint16 `json:"universe"`
	// StartAddress / AddressKnown: a device whose DMX_START_ADDRESS GET
	// NACKed or timed out resolves as UNKNOWN rather than as 0 — 0 would be
	// a lie the address column could not distinguish from a real reading
	// (this is resolveWalkAddresses' existing policy, reused verbatim).
	StartAddress   uint16 `json:"startAddress"`
	AddressKnown   bool   `json:"addressKnown"`
	Footprint      uint16 `json:"footprint"`
	FootprintKnown bool   `json:"footprintKnown"`

	// CommittedToEntryID is "" when no patch entry has committed to this
	// device — that is the "detected but unmatched" state the right pane
	// has to show plainly.
	CommittedToEntryID string `json:"committedToEntryId"`
	CommittedToName    string `json:"committedToName"`
}

// proposalJSON is one matcher suggestion. Suggestions are NEVER applied
// automatically — they are offered for the owner to accept or reject, and
// where the matcher flags ambiguity every candidate arrives with its own
// evidence, because "why did this match?" is the only thing standing
// between a tired tech and committing the wrong light.
type proposalJSON struct {
	EntryID string `json:"entryId"`
	// Status is patch.RowStatus verbatim ("matched", "address_mismatch",
	// "ambiguous", "missing").
	Status     string           `json:"status"`
	Candidates []candidateJSON  `json:"candidates"`
	Evidence   []patch.Evidence `json:"evidence"`
}

type candidateJSON struct {
	DeviceUID  string           `json:"deviceUid"`
	Confidence float64          `json:"confidence"`
	Evidence   []patch.Evidence `json:"evidence"`
}

type boardResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Intended    []intendedPaneJSON `json:"intended"`
	Detected    []detectedPaneJSON `json:"detected"`
	Proposals   []proposalJSON     `json:"proposals"`
}

// handleReconcileBoard builds the whole two-pane view model in one request.
// Deliberately one endpoint rather than three: the panes have to agree about
// which entry owns which device, and joining three independently-timed
// responses client-side is how that agreement quietly stops holding.
func (s *Server) handleReconcileBoard(w http.ResponseWriter, r *http.Request) {
	p, _ := s.PatchStore.Get() // no active patch -> zero entries, still a valid (empty) left pane
	devices := s.buildDiscoveredDevices()
	fixtures := s.Registry.Devices()
	byUID := make(map[string]registry.Fixture, len(fixtures))
	for _, f := range fixtures {
		byUID[f.UID.String()] = f
	}
	online := make(map[string]bool, len(devices))
	for _, d := range devices {
		online[d.UID] = true
	}

	// committedBy maps a device UID to the entry that has committed to it.
	// Built from the patch, not from the matcher: a commit is a stored
	// fact, not a score.
	committedBy := make(map[string]patch.Entry, len(p.Entries))
	for _, e := range p.Entries {
		if e.ConfirmedUID != "" {
			committedBy[e.ConfirmedUID] = e
		}
	}

	intended := make([]intendedPaneJSON, 0, len(p.Entries))
	for _, e := range p.Entries {
		diff := patch.DiffEntry(e)
		differs, unread := 0, 0
		for _, d := range diff {
			switch d.State {
			case patch.DiffDiffers:
				differs++
			case patch.DiffUnread:
				unread++
			}
		}
		row := intendedPaneJSON{
			EntryID: e.ID, Name: e.Name, FixtureType: e.FixtureType,
			Position: e.Position, FixtureNumber: e.FixtureNumber,
			Universe: e.Universe, StartAddress: e.StartAddress,
			Footprint: e.Footprint, Mode: e.Mode,
			Committed: e.ConfirmedUID != "", CommittedUID: e.ConfirmedUID,
			DeviceOnline: online[e.ConfirmedUID],
			Diff:         diff, DiffersCount: differs, UnreadCount: unread,
			AsFoundAt: e.AsFound.ReadAt,
		}
		intended = append(intended, row)
	}

	detected := make([]detectedPaneJSON, 0, len(devices))
	for _, d := range devices {
		row := detectedPaneJSON{
			UID: d.UID, Manufacturer: d.Manufacturer, Model: d.Model,
			Universe: d.Universe, StartAddress: d.StartAddress, AddressKnown: d.AddressKnown,
			Footprint: d.Footprint, FootprintKnown: d.FootprintKnown,
		}
		// The device's own DEVICE_LABEL, but only if some earlier screen
		// already fetched it — registry.Fixture.Params is a cache of
		// whatever has been asked for, and "absent from the map" means "not
		// yet fetched", never "the label is empty". This pane deliberately
		// does NOT issue a GET to fill it: building the board must stay a
		// cheap, read-only operation over what is already known, and the
		// label is a nicety next to the UID, not something worth a
		// round-trip per device on a 200-fixture rig.
		if f, ok := byUID[d.UID]; ok {
			if raw, cached := f.Params[rdm.PIDDeviceLabel]; cached {
				row.Label = string(raw)
			}
		}
		if e, ok := committedBy[d.UID]; ok {
			row.CommittedToEntryID = e.ID
			row.CommittedToName = patch.EntryLabel(e)
		}
		detected = append(detected, row)
	}

	// Proposals come from the existing weighted matcher (internal/patch/
	// match.go) unchanged. Only rows that still need a human decision are
	// forwarded: an entry already committed has nothing to propose, and an
	// unpatched-device row is already visible as an uncommitted right-pane
	// item.
	rep := patch.Reconcile(p.Entries, devices)
	proposals := make([]proposalJSON, 0, len(rep.Rows))
	for _, row := range rep.Rows {
		if row.EntryID == "" || row.Status == patch.StatusMissing {
			continue
		}
		if e := committedBy[row.DeviceUID]; e.ID == row.EntryID {
			continue // already committed to exactly what the matcher would suggest
		}
		if idx := p.IndexOf(row.EntryID); idx >= 0 && p.Entries[idx].ConfirmedUID != "" {
			continue // this entry has already been committed, by hand or otherwise
		}
		cands := make([]candidateJSON, 0, len(row.Candidates)+1)
		for _, c := range row.Candidates {
			cands = append(cands, candidateJSON{DeviceUID: c.DeviceUID, Confidence: c.Confidence, Evidence: c.Evidence})
		}
		if len(cands) == 0 && row.DeviceUID != "" {
			cands = append(cands, candidateJSON{DeviceUID: row.DeviceUID, Confidence: row.Confidence, Evidence: row.Evidence})
		}
		if len(cands) == 0 {
			continue
		}
		ev := row.Evidence
		if ev == nil {
			ev = make([]patch.Evidence, 0)
		}
		proposals = append(proposals, proposalJSON{
			EntryID: row.EntryID, Status: string(row.Status),
			Candidates: cands, Evidence: ev,
		})
	}

	writeJSON(w, http.StatusOK, boardResponse{
		GeneratedAt: time.Now(), Intended: intended, Detected: detected, Proposals: proposals,
	})
}

// --- commit / decommit / re-read -------------------------------------------

type commitRequest struct {
	DeviceUID string `json:"deviceUid"`
}

// commitResponse is what commit, decommit, re-read and adopt all answer
// with: the full patch (so the client's entry list stays in step without a
// second fetch) plus this entry's fresh diff.
type commitResponse struct {
	Patch patchResponse    `json:"patch"`
	Entry intendedPaneJSON `json:"entry"`
	// ReadError is set when the relation was recorded but the device could
	// not be read at all (unreachable, every GET timed out). The commit
	// still stands — the owner explicitly said these two belong together,
	// and that statement should not be thrown away because the light is
	// currently unplugged — but the client must say so rather than showing
	// an empty diff as though everything matched.
	ReadError string `json:"readError"`
}

// handleReconcileCommit is the owner's "commit": record the relation, then
// READ the device into the entry as as-found. It writes nothing to the
// fixture. See this file's doc comment.
func (s *Server) handleReconcileCommit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req commitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	uid, ok := rdm.ParseUID(req.DeviceUID)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad device uid %q", req.DeviceUID))
		return
	}

	// Committing device B to entry E while entry F is still committed to B
	// would leave two entries claiming one fixture, and the next push would
	// fight itself. F is decommitted first — and F keeps its own intended
	// settings, so this is a move, not a loss.
	var stolenFrom string
	if _, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		for i := range pp.Entries {
			if i != idx && pp.Entries[i].ConfirmedUID == uid.String() {
				stolenFrom = patch.EntryLabel(pp.Entries[i])
				pp.Entries[i].Decommit()
			}
		}
		// A commit to a DIFFERENT device must drop the previous device's
		// readings before the new ones land — otherwise a field the new
		// fixture will not answer would keep the old fixture's value and
		// read as "matching". patch.Entry.Decommit does exactly that and
		// leaves Intended alone, which is the substitution path.
		pp.Entries[idx].Decommit()
		pp.Entries[idx].ConfirmedUID = uid.String()
		pp.Entries[idx].MatchState = patch.MatchStateConfirmed
		return nil
	}); err != nil {
		writePatchStoreError(w, err)
		return
	}

	readErr := s.refreshAsFound(r.Context(), id, uid)
	s.writeCommitResponse(w, id, readErr, stolenFrom)
}

// handleReconcileReread re-runs the as-found pass against the device this
// entry is already committed to, without touching the relation. This is what
// turns "I just pushed the address" into "the diff now reads as matching" —
// and it is a read, so it is safe to press at any time.
func (s *Server) handleReconcileReread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, patch.ErrNoPatch)
		return
	}
	idx := p.IndexOf(id)
	if idx < 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown patch entry %q", id))
		return
	}
	uid, ok := rdm.ParseUID(p.Entries[idx].ConfirmedUID)
	if !ok {
		writeError(w, http.StatusConflict, fmt.Errorf("patch entry %q is not committed to a device", id))
		return
	}
	readErr := s.refreshAsFound(r.Context(), id, uid)
	s.writeCommitResponse(w, id, readErr, "")
}

// handleReconcileDecommit breaks the relation. The UID and every as-found
// reading go; the entry's INTENDED settings stay exactly as they were, ready
// for the substitute fixture to inherit.
func (s *Server) handleReconcileDecommit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries[idx].Decommit()
		return nil
	}); err != nil {
		writePatchStoreError(w, err)
		return
	}
	s.writeCommitResponse(w, id, "", "")
}

// refreshAsFound runs one as-found pass and stores it on the entry. Returns
// a human-readable reason when the device could not be read at all, or ""
// on success. Note that a per-FIELD failure is not a failure here — it is
// recorded in that field's Err and the pass still succeeds.
func (s *Server) refreshAsFound(ctx context.Context, entryID string, uid rdm.UID) string {
	af, err := s.readAsFound(ctx, uid)
	if err != nil {
		return err.Error()
	}
	if _, mErr := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(entryID)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", entryID)
		}
		// Guard against the entry having been decommitted or re-committed
		// elsewhere while this read was in flight: readings are only ever
		// stored against the device they actually came off.
		if pp.Entries[idx].ConfirmedUID != af.UID {
			return nil
		}
		pp.Entries[idx].AsFound = af
		return nil
	}); mErr != nil {
		return mErr.Error()
	}
	return ""
}

// writeCommitResponse re-reads the entry from the store (never from a stale
// local copy) and answers with the patch plus that entry's fresh pane row.
func (s *Server) writeCommitResponse(w http.ResponseWriter, id, readErr, stolenFrom string) {
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, patch.ErrNoPatch)
		return
	}
	idx := p.IndexOf(id)
	if idx < 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown patch entry %q", id))
		return
	}
	e := p.Entries[idx]
	diff := patch.DiffEntry(e)
	differs, unread := 0, 0
	for _, d := range diff {
		switch d.State {
		case patch.DiffDiffers:
			differs++
		case patch.DiffUnread:
			unread++
		}
	}
	if stolenFrom != "" && readErr == "" {
		// Deliberately a fact about ENTRIES, never about universes or
		// addresses — this string is server-generated and user-facing, and
		// this server never states a universe number.
		readErr = "this fixture was committed to " + stolenFrom + "; that entry has been decommitted"
	}
	writeJSON(w, http.StatusOK, commitResponse{
		Patch: toPatchResponse(p),
		Entry: intendedPaneJSON{
			EntryID: e.ID, Name: e.Name, FixtureType: e.FixtureType,
			Position: e.Position, FixtureNumber: e.FixtureNumber,
			Universe: e.Universe, StartAddress: e.StartAddress,
			Footprint: e.Footprint, Mode: e.Mode,
			Committed: e.ConfirmedUID != "", CommittedUID: e.ConfirmedUID,
			DeviceOnline: s.deviceOnline(e.ConfirmedUID),
			Diff:         diff, DiffersCount: differs, UnreadCount: unread,
			AsFoundAt: e.AsFound.ReadAt,
		},
		ReadError: readErr,
	})
}

func (s *Server) deviceOnline(uidStr string) bool {
	uid, ok := rdm.ParseUID(uidStr)
	if !ok {
		return false
	}
	_, found := s.Registry.Fixture(uid)
	return found
}

// --- push: the only path that writes to a fixture --------------------------

type pushRequest struct {
	// Fields names exactly which settings to write. There is no "all"
	// shorthand and no empty-means-everything: every field the client wants
	// applied must be named, because a request that accidentally sent
	// nothing would otherwise become a request that reconfigured the whole
	// fixture.
	Fields []string `json:"fields"`
}

type pushResultJSON struct {
	Field string `json:"field"`
	// OK is the outcome. No `omitempty`: false is the result that matters.
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type pushResponse struct {
	Results []pushResultJSON `json:"results"`
	// Applied / Requested are counts. Neither carries `omitempty` — 0
	// applied (every SET errored) is real, meaningful data, and this
	// codebase has already shipped a "fixed undefined of N" from exactly
	// that mistake on fixAllResponse.Applied.
	Applied   int `json:"applied"`
	Requested int `json:"requested"`
	// Entry is the entry AFTER an automatic re-read, so the client can
	// paint the now-matching diff without a second round trip. Re-reading
	// here is not a second write — it is the confirmation that the write
	// landed, which is the whole reason the owner presses the button.
	Entry     intendedPaneJSON `json:"entry"`
	ReadError string           `json:"readError"`
}

// handleReconcilePush writes the named fields' INTENDED values to the
// committed fixture. This is the only function in this file that emits an
// RDM SET, and it runs only from an explicit per-difference press in the UI
// (Apply-to-confirm). A field whose intended side is unknown is refused
// rather than guessed: there is no defensible value to write.
func (s *Server) handleReconcilePush(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req pushRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Fields) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("fields is required and must name at least one setting"))
		return
	}
	p, ok := s.PatchStore.Get()
	if !ok {
		writeError(w, http.StatusNotFound, patch.ErrNoPatch)
		return
	}
	idx := p.IndexOf(id)
	if idx < 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown patch entry %q", id))
		return
	}
	e := p.Entries[idx]
	uid, ok := rdm.ParseUID(e.ConfirmedUID)
	if !ok {
		writeError(w, http.StatusConflict, fmt.Errorf("patch entry %q is not committed to a device", id))
		return
	}
	node, found := s.Registry.FixtureNode(uid)
	if !found {
		writeParamError(w, fmt.Errorf("device %s not currently reachable", uid))
		return
	}
	client := params.New(s.RDM, node, uid)

	pushable := patch.PushableFields()
	results := make([]pushResultJSON, 0, len(req.Fields))
	applied := 0
	for _, name := range req.Fields {
		f := patch.DiffField(name)
		res := pushResultJSON{Field: name}
		switch {
		case !pushable[f]:
			res.Error = "this setting cannot be written to a fixture over RDM"
		default:
			ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
			err := s.pushField(ctx, client, e, f)
			cancel()
			if err != nil {
				res.Error = err.Error()
			} else {
				res.OK = true
				applied++
			}
		}
		results = append(results, res)
	}

	// Re-read so the response carries the fixture's ACTUAL post-write state
	// rather than an optimistic assumption that the SET did what it said.
	readErr := s.refreshAsFound(r.Context(), id, uid)

	p2, _ := s.PatchStore.Get()
	entry := intendedPaneJSON{EntryID: id}
	if i2 := p2.IndexOf(id); i2 >= 0 {
		e2 := p2.Entries[i2]
		diff := patch.DiffEntry(e2)
		differs, unread := 0, 0
		for _, d := range diff {
			switch d.State {
			case patch.DiffDiffers:
				differs++
			case patch.DiffUnread:
				unread++
			}
		}
		entry = intendedPaneJSON{
			EntryID: e2.ID, Name: e2.Name, FixtureType: e2.FixtureType,
			Position: e2.Position, FixtureNumber: e2.FixtureNumber,
			Universe: e2.Universe, StartAddress: e2.StartAddress,
			Footprint: e2.Footprint, Mode: e2.Mode,
			Committed: e2.ConfirmedUID != "", CommittedUID: e2.ConfirmedUID,
			DeviceOnline: s.deviceOnline(e2.ConfirmedUID),
			Diff:         diff, DiffersCount: differs, UnreadCount: unread,
			AsFoundAt: e2.AsFound.ReadAt,
		}
	}
	writeJSON(w, http.StatusOK, pushResponse{
		Results: results, Applied: applied, Requested: len(req.Fields),
		Entry: entry, ReadError: readErr,
	})
}

// pushField writes one field's intended value. Every case refuses to write
// when the intention is unknown — "the patch never said" is not a value.
func (s *Server) pushField(ctx context.Context, client *params.Client, e patch.Entry, f patch.DiffField) error {
	switch f {
	case patch.FieldStartAddress:
		if e.StartAddress == 0 {
			return fmt.Errorf("this patch entry has no DMX address set")
		}
		return client.SetDMXStartAddress(ctx, e.StartAddress)
	case patch.FieldPersonality:
		if !e.Intended.Personality.Known {
			return fmt.Errorf("this patch entry has no intended DMX mode recorded")
		}
		return client.SetDMXPersonality(ctx, e.Intended.Personality.Value)
	case patch.FieldDimmerCurve:
		if !e.Intended.DimmerCurve.Known {
			return fmt.Errorf("this patch entry has no intended dimmer curve recorded")
		}
		return client.SetCurve(ctx, e.Intended.DimmerCurve.Value)
	case patch.FieldPanInvert:
		if !e.Intended.PanInvert.Known {
			return fmt.Errorf("this patch entry has no intended pan invert recorded")
		}
		return client.SetPanInvert(ctx, e.Intended.PanInvert.Value)
	case patch.FieldTiltInvert:
		if !e.Intended.TiltInvert.Known {
			return fmt.Errorf("this patch entry has no intended tilt invert recorded")
		}
		return client.SetTiltInvert(ctx, e.Intended.TiltInvert.Value)
	case patch.FieldPanTiltSwap:
		if !e.Intended.PanTiltSwap.Known {
			return fmt.Errorf("this patch entry has no intended pan/tilt swap recorded")
		}
		return client.SetPanTiltSwap(ctx, e.Intended.PanTiltSwap.Value)
	case patch.FieldDeviceLabel:
		if !e.Intended.DeviceLabel.Known {
			return fmt.Errorf("this patch entry has no intended device label recorded")
		}
		return client.SetDeviceLabel(ctx, e.Intended.DeviceLabel.Value)
	default:
		return fmt.Errorf("unknown setting %q", string(f))
	}
}

// --- adopt: the shop direction ---------------------------------------------

type adoptIntendedRequest struct {
	Fields []string `json:"fields"`
}

// handleReconcileAdoptIntended copies as-found values into the entry's
// INTENDED settings. This is the shop half of the owner's workflow — "set up
// my rig in the shop, test and configure" — where the fixture is right and
// the patch should learn what he did. It is a PATCH-ONLY write: it never
// touches a fixture, which is what makes it the safe opposite of push.
func (s *Server) handleReconcileAdoptIntended(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req adoptIntendedRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Fields) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("fields is required and must name at least one setting"))
		return
	}
	fields := make([]patch.DiffField, 0, len(req.Fields))
	for _, f := range req.Fields {
		fields = append(fields, patch.DiffField(f))
	}
	if _, err := s.PatchStore.Mutate(func(pp *patch.Patch) error {
		idx := pp.IndexOf(id)
		if idx < 0 {
			return fmt.Errorf("unknown patch entry %q", id)
		}
		pp.Entries[idx].AdoptAsIntended(fields, time.Now())
		return nil
	}); err != nil {
		writePatchStoreError(w, err)
		return
	}
	s.writeCommitResponse(w, id, "", "")
}
