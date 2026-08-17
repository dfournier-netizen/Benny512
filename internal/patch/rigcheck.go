package patch

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

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
)

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
}

// RigCheck steps through an ordered, scoped list of patch entries, driving
// a session.DMXOutputEngine. One RigCheck instance is meant to live for the
// life of the server (like Server.walkStore) — Start begins a new run,
// discarding whatever was running before (always blackout-and-stop first,
// so a fresh Start never inherits stale lit channels from a previous scope).
type RigCheck struct {
	dmx *session.DMXOutputEngine

	mu      sync.Mutex
	entries []Entry
	started map[uint16]bool // Port-Address raw value -> StartUniverse already called
	mode    Mode
	level   byte
	idx     int
	chOff   int
	running bool
}

// NewRigCheck builds a RigCheck driving dmx. dmx is required and typically
// shared with the rest of the server (the same engine Send/DMX use) — Start
// only ever touches the specific universes its scope covers, per-universe
// (StartUniverse/StopUniverse), never DMX.Start()/DMX.Stop() globally, so
// running a rig check never disturbs unrelated universes another screen
// might be driving.
func NewRigCheck(dmx *session.DMXOutputEngine) *RigCheck {
	return &RigCheck{dmx: dmx, started: map[uint16]bool{}, level: DefaultLevel}
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
	r.stopLocked() // always start clean — no stale universes left driving from a previous scope

	r.entries = append([]Entry(nil), entries...)
	r.mode = normalizeMode(mode)
	r.level = level
	if r.level == 0 {
		r.level = DefaultLevel
	}
	r.idx = 0
	r.chOff = 0
	r.running = true

	for _, e := range r.entries {
		if r.started[e.Universe] {
			continue
		}
		pa, err := artnet.PortAddressFromRaw(e.Universe)
		if err != nil {
			continue // an invalid universe value in a scoped entry is skipped, not fatal to the whole run
		}
		r.dmx.StartUniverse(pa, netip.AddrPort{}, session.DMXUniverseSize)
		r.started[e.Universe] = true
	}
	r.dmx.Start() // idempotent if the engine's tick loop is already running (e.g. Send screen also using it)
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
	r.stopLocked()
}

func (r *RigCheck) stopLocked() {
	r.blackoutLocked()
	for u := range r.started {
		if pa, err := artnet.PortAddressFromRaw(u); err == nil {
			r.dmx.StopUniverse(pa)
		}
	}
	r.started = map[uint16]bool{}
	r.running = false
}

// Blackout zeroes every currently-touched universe's buffer and pushes it
// immediately, WITHOUT ending the run (Running stays true, cursor position
// is preserved) — the "hard all-off" panic button (task ask: "a hard
// all-off/blackout that always works") a tech can hit mid-check without
// losing their place.
func (r *RigCheck) Blackout() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blackoutLocked()
}

func (r *RigCheck) blackoutLocked() {
	zero := make([]byte, session.DMXUniverseSize)
	for u := range r.started {
		if pa, err := artnet.PortAddressFromRaw(u); err == nil {
			_ = r.dmx.SetFrame(pa, zero)
		}
	}
	r.dmx.SendNow()
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
	st := State{Running: r.running, Mode: r.mode, Level: r.level, EntryIndex: r.idx, ChannelOffset: r.chOff}
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
	for u, data := range frames {
		if pa, err := artnet.PortAddressFromRaw(u); err == nil {
			_ = r.dmx.SetFrame(pa, data)
		}
	}
	r.dmx.SendNow()
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
