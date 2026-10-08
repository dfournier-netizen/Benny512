package patch

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// This file implements the channel-level rig-check sequencer (architecture
// doc §3.2, task ask item 4): step through patch entries driving a
// session.DMXOutputEngine so a tech can visually confirm each entry lights
// up at the address the patch says it should — the physical-world
// complement to the RDM-based Reconcile report above. Function-aware
// (per-channel-role, e.g. "this is the red channel") waits for GDTF data in
// Phase 2b; today every mode operates on raw channel offsets within an
// entry's footprint.
//
// Unlike entry.go/collision.go/match.go, this file is NOT dependency-free —
// it reaches into internal/session (DMXOutputEngine) and internal/artnet
// (PortAddress) to actually drive output, per the task's explicit
// allowance ("Sequencer in internal/patch (or a sibling package) driving
// DMXOutputEngine"). It is still fully unit-testable without real hardware:
// session.DMXOutputEngine already exports FakeClock/FakeTransport as
// production types (used by --demo, not just tests) — see rigcheck_test.go.
//
// Safety discipline (task ask, mirrors Rig Walk's identify-off precedent):
// Stop and Blackout always zero every touched universe's buffer and push it
// immediately via SendNow before doing anything else, so "did the stop
// actually take" never depends on the DMX engine's ~40Hz retransmit tick
// happening to fire in time. internal/web is responsible for calling Stop
// on every "leaving the screen" and "page unload" signal, exactly like
// walk.go's identify-off beacon.

// Mode selects how RigCheck drives the current entry's channels.
type Mode string

// Modes (task ask, item 4).
const (
	// ModeAllChannels drives EVERY entry in the current scope to Level
	// simultaneously — a fast "is the whole rig patched and responding"
	// sanity check, independent of which entry Next/Previous has selected.
	ModeAllChannels Mode = "all_channels"
	// ModeStepChannel drives exactly one channel of the CURRENT entry (the
	// one at ChannelOffset, 0-based within its footprint) to Level; every
	// other channel in scope is 0. Used to identify which console channel
	// drives which physical function.
	ModeStepChannel Mode = "step_channel"
	// ModeHighlight drives every channel of the CURRENT entry to Level;
	// every other entry in scope is forced to 0 ("current fixture up, all
	// others at zero" — task ask verbatim). Used to visually confirm "this
	// is fixture N" while stepping through the rig.
	ModeHighlight Mode = "highlight"
)

// DefaultLevel is used when Start/SetLevel is called with level 0 — 0 would
// otherwise mean "drive to black", which is never a useful rig-check
// default (task ask implies a level bright enough to see, configurable
// from there).
const DefaultLevel byte = 255

// Errors returned by RigCheck's methods.
var (
	ErrRigCheckNotRunning = errors.New("patch: rig check is not running")
	ErrRigCheckEmptyScope = errors.New("patch: rig check scope has no entries")
	// ErrRigCheckPatternRunning is returned by every CLASSIC (channel-level)
	// mutator — SetMode/SetLevel/Jump/Next/Previous/StepChannel — while the
	// stage 2 test-pattern engine (testpattern.go) is actually DRIVING
	// OUTPUT. The two engines share this RigCheck instance's frame-recompute
	// machinery and its started-universe bookkeeping, and both would race to
	// decide what a shared universe's buffer holds, so a classic action
	// while pattern output flows must fail loudly rather than silently
	// corrupt the pattern's frame with a stale r.mode-driven recompute.
	//
	// Note precisely what this does and does not gate, since it changed with
	// the stackable-tests rework: it is about OUTPUT, not configuration.
	// Selecting, deselecting and re-parameterising test patterns is legal at
	// any time and never returns this (see testpattern.go's "Selection and
	// output are two independent pieces of state" doc section); only the
	// classic channel-level walk's mutators are excluded, and only while
	// pattern output is live. Call StopPatternOutput (or Stop, or start a
	// classic run via Start, which stops pattern output first) to get back
	// to classic mode.
	ErrRigCheckPatternRunning = errors.New("patch: test pattern output is running — stop it first")
	// ErrRigCheckNoPatternRunning is returned by AdjustPattern, the
	// single-test back-compat shorthand, when no test is selected at all.
	ErrRigCheckNoPatternRunning = errors.New("patch: no test pattern is selected")
	// ErrRigCheckAmbiguousTest is returned by AdjustPattern when SEVERAL
	// tests are selected: that call names no test, so there is nothing it
	// could unambiguously mean. Callers with more than one test selected use
	// SelectPatternTest (which names the test it is adjusting) instead.
	ErrRigCheckAmbiguousTest = errors.New("patch: more than one test is selected — adjust a specific test instead")
)

// There is no Rig Check watchdog any more (C3). Before the unified output
// engine, a pattern blacked itself out when no Rig Check page had polled it
// for 5 s. The master Arm's lease now does that job for every source at once,
// with the owner's multi-browser rule (any connected browser keeps it alive)
// and the owner's lease-loss choice (Blackout or Hold last look) — a second,
// per-screen watchdog would contradict both.

// State is a snapshot of RigCheck's current status, safe to serialize
// directly to JSON.
type State struct {
	Running        bool     `json:"running"`
	Mode           Mode     `json:"mode,omitempty"`
	Level          byte     `json:"level"`
	EntryIndex     int      `json:"entryIndex"`
	EntryIDs       []string `json:"entryIds,omitempty"`
	ChannelOffset  int      `json:"channelOffset"`
	CurrentChannel uint16   `json:"currentChannel,omitempty"` // absolute DMX address being driven, ModeStepChannel only
	// PatternRunning is true while the stage 2 test-pattern engine
	// (testpattern.go) is driving output instead of this classic
	// channel-level walk — every
	// field above (Mode/Level/EntryIndex/ChannelOffset/CurrentChannel) is
	// then stale/frozen at whatever it held the instant StartPattern took
	// over (see StartPattern's doc comment); read PatternStatus for the
	// real, live detail. Deliberately no `omitempty`: false (no pattern
	// running, the overwhelmingly common case) is real, meaningful data a
	// client must be able to tell apart from a key that's merely missing.
	PatternRunning bool `json:"patternRunning"`
}

// RigCheck steps through an ordered, scoped list of patch entries, driving
// a session.DMXOutputEngine. One RigCheck instance is meant to live for the
// life of the server (like Server.walkStore) — Start begins a new run,
// discarding whatever was running before (always blackout-and-stop first,
// so a fresh Start never inherits stale lit channels from a previous scope).
type RigCheck struct {
	dmx   *session.DMXOutputEngine
	clock session.Clock // same Clock dmx was built with — see Clock's doc comment (session/dmxout.go) and testpattern.go's package doc comment
	// out is the output boundary (rigcheckout.go): Rig Check's source in the
	// unified output engine. Every frame this file pushes and every universe
	// it claims or releases goes through it. Always non-nil.
	out *rigOutput

	mu      sync.Mutex
	entries []Entry
	started map[uint16]bool // Port-Address raw value -> StartUniverse already called
	mode    Mode
	level   byte
	idx     int
	chOff   int
	running bool

	// --- stage 2: attribute-level test-pattern engine (testpattern.go) ---
	//
	// selection and patternOutput are the two independent halves of that
	// engine's state model (see testpattern.go's doc comment): selection
	// survives every stop, patternOutput is what stop clears.
	selection      *patternSelection
	patternOutput  bool
	patternEpoch   time.Time     // stamped when output was last enabled — the shared time base every active test's waveform is sampled against
	patternTimer   session.Timer // self-rescheduling AfterFunc chain driving patternTick, mirrors DMXOutputEngine's own tick()/scheduleLocked()
	lastPatternEnd string        // "" (never run) | "manual" | "restarted" | "show changed" — see PatternStatus.LastEndReason
	// followSelection: pattern output is on exactly while the selection
	// has a test and a scope (SetOutputFollowsSelection).
	followSelection bool
	// testsLayer is set while the Console-lite Tests API (SetTests) drives
	// this engine: ending its output RELEASES the tests layer so the base and
	// programmer show at once, instead of first blacking out the claimed
	// channels the way Rig Check's own Stop always has.
	testsLayer      bool
	patternFadeTime time.Duration
	patternFrames   map[uint16][]byte
	patternUnits    map[patternUnitKey]patternUnit
	patternFades    map[patternUnitKey]patternFade
}

// NewRigCheck builds a RigCheck driving dmx. dmx is required and typically
// shared with the rest of the server (the same engine Send/DMX use) — Start
// only ever touches the specific universes its scope covers, per-universe
// (StartUniverse/StopUniverse), never DMX.Start()/DMX.Stop() globally, so
// running a rig check never disturbs unrelated universes another screen
// might be driving. The stage 2 pattern engine (testpattern.go) reuses
// dmx.Clock() — never a separate RealClock — specifically so a test's one
// FakeClock deterministically drives both the DMX retransmit tick and every
// pattern's own value recomputation.
func NewRigCheck(dmx *session.DMXOutputEngine) *RigCheck {
	r := &RigCheck{dmx: dmx, clock: dmx.Clock(), started: map[uint16]bool{}, level: DefaultLevel, selection: newPatternSelection(), patternFadeTime: time.Second}
	r.out = newRigOutput(dmx)
	return r
}

// SetOutputFollowsSelection makes the Function check's output follow its
// selection: rendering is on whenever at least one test is selected over a
// non-empty scope, and off when the last test is deselected. This is the
// server half of the owner's C3 rule "all controls are live as selected; Arm
// is the confirm step" — the screen no longer has its own START/STOP, and
// nothing reaches the wire unless the master output is armed. The web layer
// turns it on for the live server; a RigCheck built bare (unit tests) keeps
// the explicit StartPatternOutput/StopPatternOutput model.
func (r *RigCheck) SetOutputFollowsSelection(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.followSelection = on
	r.followSelectionLocked()
}

// scopeUniversesLocked returns the distinct, valid raw Port-Addresses an
// entry list covers, in first-appearance order. An entry whose Universe is
// not a valid Art-Net Port-Address is skipped, not fatal — the same
// tolerance the pre-sACN code had inline.
func scopeUniverses(entries []Entry) []uint16 {
	seen := map[uint16]bool{}
	out := make([]uint16, 0, len(entries))
	for _, e := range entries {
		if seen[e.Universe] {
			continue
		}
		if _, err := artnet.PortAddressFromRaw(e.Universe); err != nil {
			continue
		}
		seen[e.Universe] = true
		out = append(out, e.Universe)
	}
	return out
}

// Start begins a new rig check over entries (already ordered/scoped by the
// caller — internal/web resolves "whole patch" / "one universe" /
// "selection" scope into this slice; see task ask item 4's scope
// selection). Entries with Footprint 0 are kept in the list (so Next/
// Previous can still walk past them for reference) but never light any
// channel, matching DetectCollisions' zero-footprint tolerance.
func (r *RigCheck) Start(entries []Entry, mode Mode, level byte) error {
	if len(entries) == 0 {
		return ErrRigCheckEmptyScope
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	raws := scopeUniverses(entries)

	r.stopLocked("restarted") // always start clean — no stale universes left driving from a previous scope, and this supersedes any running pattern too
	r.testsLayer = false

	r.entries = append([]Entry(nil), entries...)
	r.mode = normalizeMode(mode)
	r.level = level
	if r.level == 0 {
		r.level = DefaultLevel
	}
	r.idx = 0
	r.chOff = 0
	r.running = true

	for _, u := range raws {
		if err := r.out.startUniverse(u); err != nil {
			// scopeUniverses only yields valid Port-Addresses, so this is
			// unreachable today; nothing is left lit if it ever happens.
			r.stopLocked("restarted")
			r.running = false
			return err
		}
		r.started[u] = true
	}
	r.recomputeLocked()
	return nil
}

func normalizeMode(m Mode) Mode {
	switch m {
	case ModeAllChannels, ModeStepChannel, ModeHighlight:
		return m
	default:
		return ModeHighlight
	}
}

// Stop blackout-and-stops: zeroes every touched universe, pushes it
// immediately, then removes those universes from the DMX engine entirely
// (StopUniverse) so nothing keeps retransmitting them on the engine's
// normal tick. Safe to call when not running (no-op beyond an unconditional
// blackout of whatever WAS touched, so a caller can always call Stop
// defensively — task ask: "on leaving the screen, and on page unload").
func (r *RigCheck) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked("manual")
}

// ResetSelection is distinct from Stop: switching shows or changing patch
// addressing invalidates resolved fixture copies as well as stopping output.
func (r *RigCheck) ResetSelection() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked("show changed")
	r.selection = newPatternSelection()
}

// stopLocked is the one choke point every path out of a running rig
// check — classic or pattern — goes through, so the blackout discipline
// (this file's doc comment) and the pattern ticker's cleanup can never be
// forgotten on any of them. reason is recorded as lastPatternEnd ONLY when
// pattern output was actually flowing (see PatternStatus.LastEndReason); it
// is ignored otherwise, so this stays the ordinary, reason-agnostic Stop path
// classic-mode callers already expect.
//
// It stops OUTPUT and nothing else: the pattern engine's SELECTION (scope,
// selected tests, isolate flag) is deliberately untouched, per the owner's
// "stop should stop output, but not deselect any tests". A caller that wants
// the selection gone replaces it via SetPatternTests.
func (r *RigCheck) stopLocked(reason string) {
	if !r.testsLayer {
		r.blackoutLocked()
	}
	// Release every universe Rig Check claims. The engine retires each
	// stream nobody else drives on whichever protocols it was routed to —
	// sACN gets three zero frames and three Stream_Terminated packets
	// (ANSI E1.31 Section 6.2.6) — and a universe another source still drives
	// falls back to that source.
	r.out.stopAll()
	r.started = map[uint16]bool{}
	r.running = false
	r.cancelPatternOutputLocked(reason)
}

// Blackout zeroes every currently-touched universe's buffer and pushes it
// immediately, WITHOUT ending the run (Running stays true, cursor position
// is preserved) — the "hard all-off" panic button (task ask: "a hard
// all-off/blackout that always works") a tech can hit mid-check without
// losing their place.
func (r *RigCheck) Blackout() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.patternOutput {
		// Live pattern output re-asserts its own frame on every tick (see
		// testpattern.go's tick-source doc comment — roughly every 25ms at
		// the default 40Hz), so this method's classic-mode contract above
		// ("Running stays true... WITHOUT ending the run") would make
		// Blackout a one-frame flicker here, not the "hard all-off that
		// always works" panic button its doc comment promises. Ending the
		// pattern — not just zeroing its current frame — is the only way
		// this call can keep that promise while a pattern is live. A
		// deliberate, documented divergence from the classic-mode
		// behaviour above: report this as "manual" (same as a direct
		// Stop), not a distinct reason, since from the caller's point of
		// view it IS a deliberate stop. The Tests layer would otherwise just
		// release (stopLocked); the panic button zeroes its channels first.
		if r.testsLayer {
			r.blackoutLocked()
		}
		r.stopLocked("manual")
		return
	}
	r.blackoutLocked()
}

func (r *RigCheck) blackoutLocked() {
	// Zeroes exactly what Rig Check claims (its scope's channels). force:
	// a blackout must put a packet on the wire even when the frame it
	// replaces was already zero, so E1.31's change-only transmission rule
	// (§6.6.2) does not quietly swallow the panic button.
	r.out.blackout()
}

// SetMode changes the drive mode and re-applies output immediately,
// resetting ChannelOffset to 0 (a channel offset from ModeStepChannel has
// no meaning in another mode).
func (r *RigCheck) SetMode(mode Mode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ErrRigCheckNotRunning
	}
	if r.patternOutput {
		return ErrRigCheckPatternRunning
	}
	r.mode = normalizeMode(mode)
	r.chOff = 0
	r.recomputeLocked()
	return nil
}

// SetLevel changes the drive level (0 is a legitimate level here, unlike
// Start — an explicit "dim to zero without stopping" is a reasonable thing
// to dial to) and re-applies output immediately.
func (r *RigCheck) SetLevel(level byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ErrRigCheckNotRunning
	}
	if r.patternOutput {
		return ErrRigCheckPatternRunning
	}
	r.level = level
	r.recomputeLocked()
	return nil
}

// Jump moves the cursor to a specific entry index and re-applies output.
// Unlike Next/Previous, an out-of-range index is an error (this is the
// explicit "go to entry N" action, not a mashable nav button).
func (r *RigCheck) Jump(index int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ErrRigCheckNotRunning
	}
	if r.patternOutput {
		return ErrRigCheckPatternRunning
	}
	if index < 0 || index >= len(r.entries) {
		return fmt.Errorf("patch: rig check index %d out of range [0,%d)", index, len(r.entries))
	}
	r.idx = index
	r.chOff = 0
	r.recomputeLocked()
	return nil
}

// Next/Previous move one entry, clamping (not erroring) at either end so a
// tech can mash the button without the UI needing to track bounds itself —
// mirrors Rig Walk's Prev/Next button-disable-at-bounds UX, but tolerant
// server-side too.
func (r *RigCheck) Next() error { return r.stepEntry(1) }

// Previous moves the cursor back one entry (see Next's doc comment).
func (r *RigCheck) Previous() error { return r.stepEntry(-1) }

func (r *RigCheck) stepEntry(delta int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ErrRigCheckNotRunning
	}
	if r.patternOutput {
		return ErrRigCheckPatternRunning
	}
	next := r.idx + delta
	if next < 0 {
		next = 0
	}
	if next >= len(r.entries) {
		next = len(r.entries) - 1
	}
	if next == r.idx {
		return nil
	}
	r.idx = next
	r.chOff = 0
	r.recomputeLocked()
	return nil
}

// StepChannel moves ChannelOffset within the current entry's footprint
// (ModeStepChannel only — harmless no-op in other modes beyond updating the
// stored offset, which SetMode resets to 0 anyway on the next mode switch).
// Clamps at the footprint's bounds like Next/Previous.
func (r *RigCheck) StepChannel(delta int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ErrRigCheckNotRunning
	}
	if r.patternOutput {
		return ErrRigCheckPatternRunning
	}
	cur, ok := r.currentEntryLocked()
	if !ok || cur.Footprint == 0 {
		return nil
	}
	next := r.chOff + delta
	if next < 0 {
		next = 0
	}
	if next >= int(cur.Footprint) {
		next = int(cur.Footprint) - 1
	}
	r.chOff = next
	r.recomputeLocked()
	return nil
}

// State returns a snapshot of the current run.
func (r *RigCheck) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := State{
		Running: r.running, Mode: r.mode, Level: r.level, EntryIndex: r.idx, ChannelOffset: r.chOff,
		PatternRunning: r.patternOutput,
	}
	for _, e := range r.entries {
		st.EntryIDs = append(st.EntryIDs, e.ID)
	}
	if r.mode == ModeStepChannel {
		if cur, ok := r.currentEntryLocked(); ok && cur.Footprint > 0 {
			st.CurrentChannel = cur.StartAddress + uint16(r.chOff)
		}
	}
	return st
}

func (r *RigCheck) currentEntryLocked() (Entry, bool) {
	if r.idx < 0 || r.idx >= len(r.entries) {
		return Entry{}, false
	}
	return r.entries[r.idx], true
}

// recomputeLocked rebuilds every touched universe's frame from scratch
// (always starting all-zero) and pushes it immediately — see this file's
// doc comment on why Stop/Blackout don't rely on the engine's own tick.
func (r *RigCheck) recomputeLocked() {
	frames := make(map[uint16][]byte, len(r.started))
	for u := range r.started {
		frames[u] = make([]byte, session.DMXUniverseSize)
	}
	switch r.mode {
	case ModeAllChannels:
		for _, e := range r.entries {
			fillEntry(frames, e, r.level)
		}
	case ModeHighlight:
		if cur, ok := r.currentEntryLocked(); ok {
			fillEntry(frames, cur, r.level)
		}
	case ModeStepChannel:
		if cur, ok := r.currentEntryLocked(); ok {
			fillChannel(frames, cur, r.chOff, r.level)
		}
	}
	claim := map[uint16]*slotClaim{}
	for _, e := range r.entries {
		claimEntry(claim, e, nil)
	}
	r.out.setFrames(frames, claim, false)
}

// fillEntry sets every channel e occupies, in its universe's frame, to
// level. No-op for a zero-footprint or out-of-range entry.
func fillEntry(frames map[uint16][]byte, e Entry, level byte) {
	if e.Footprint == 0 {
		return
	}
	frame, ok := frames[e.Universe]
	if !ok {
		return
	}
	for ch := int(e.StartAddress); ch <= e.EndAddress(); ch++ {
		if ch < 1 || ch > session.DMXUniverseSize {
			continue
		}
		frame[ch-1] = level
	}
}

// fillChannel sets exactly one channel (e.StartAddress+offset, 0-based) in
// e's universe's frame to level.
func fillChannel(frames map[uint16][]byte, e Entry, offset int, level byte) {
	if e.Footprint == 0 || offset < 0 || offset >= int(e.Footprint) {
		return
	}
	frame, ok := frames[e.Universe]
	if !ok {
		return
	}
	ch := int(e.StartAddress) + offset
	if ch < 1 || ch > session.DMXUniverseSize {
		return
	}
	frame[ch-1] = level
}
