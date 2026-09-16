// Package autoread gives every RDM UID Benny512 learns about one automatic,
// server-side identity read — whichever way it was learned, and whether or
// not a browser is connected.
//
// WHY THIS EXISTS (RDM-LOG30, 2026-09-16)
//
// A node's Table of Devices can arrive long after discovery has finished with
// that port. On 2.11.90.2 Port-Address 31 the flush at 14:04:37.881 drew
// three empty ToDs back in 211 ms, 228 ms and 223 ms; discovery accepted
// "the port is empty" and moved on; the real table then arrived unsolicited
// at 14:04:49.899 (11 UIDs, +11.554 s) and 14:04:50.913 (14 UIDs,
// +12.568 s). 2.11.90.6 Port-Address 13 reproduced it independently: empty at
// +210 ms and +222 ms, then 4 UIDs at +6.979 s.
//
// session.RDMController already handled the late table correctly
// (mergeUnsolicitedTodLocked emits EventToDUpdate), and registry.Registry
// already recorded the UIDs (mergeToD). Nothing then read them. The only
// thing in the whole application that issued the per-device identity GETs was
// devices.js's classifyUnknown(), in the browser, for rows the Devices screen
// happened to be showing — so those 29 devices sat in the list with null
// data until an operator opened Inspect on one, at which point it resolved
// instantly, because nothing was ever wrong with the fixture.
//
// WHAT THIS PACKAGE GUARANTEES
//
//  1. Serialized. One device is read at a time, by one goroutine, and each
//     PID within a device's pass is strictly request-reply-request through
//     session.RDMController. An RDM line is a shared half-duplex bus; reading
//     devices in parallel would be a regression on a real rig, not a speedup.
//  2. Idempotent. The ledger is keyed on (node IP, bind index, Port-Address,
//     UID) — registry's own fixture identity — so a UID already read is not
//     re-read when the next ToD names it again. RDM-LOG30's browser sweep
//     re-asked DEVICE_INFO 173 times across the session.
//  3. Bounded. A device that does not answer DEVICE_INFO is retried at most
//     MaxAttempts times and then left in StateGaveUp: explicitly unread. It
//     is never given a fabricated value, and it is never quietly forgotten.
//     RDM-LOG30's four silent responders (4D50:001158FE, 4D50:0011597E,
//     4D50:001159BE, 4D50:001159FE) absorbed 48-63 requests each.
//  4. Gated. The read goes through params.Client, so the SUPPORTED_PARAMETERS
//     probe gate applies unchanged — no speculative PID is reintroduced, and
//     no E1.20-required PID is gated.
//
// Layering: this package drives session.RDMController through params.Client
// and is told about UIDs by registry.Registry's SetOnDeviceSeen hook. It is
// wired in internal/web (Server.New/Run); nothing here knows about HTTP.
package autoread

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// State is what the reader knows about one (node port, UID) pair. It is a
// string so it can go straight onto the Devices JSON without a second
// vocabulary: the browser must be able to tell "queued", "being read now"
// and "we gave up" apart, and the empty value must be distinguishable from
// all three.
type State string

// The reader's states. Every one of them is a statement the UI can print.
const (
	// StateUnknown means this reader has never heard of the device. It is
	// the zero value, and it is what a caller gets for a fixture that
	// reached the registry by some path that does not go through a ToD.
	StateUnknown State = ""
	// StatePending: queued, nothing sent yet.
	StatePending State = "pending"
	// StateReading: this device's pass is on the wire now.
	StateReading State = "reading"
	// StateRead: DEVICE_INFO ACKed. The row is filled in.
	StateRead State = "read"
	// StateGaveUp: MaxAttempts passes and DEVICE_INFO never answered. The
	// device stays in the list, explicitly unread. Nothing is guessed.
	StateGaveUp State = "gaveUp"
)

// defaultMaxAttempts is how many core-identity passes one UID gets before
// the reader stops on its own.
//
// Two, not more, and the arithmetic is deliberate. Each pass's DEVICE_INFO is
// already one request plus session.RDMConfig's own retries, so two passes
// cost at most 8 DEVICE_INFO packets to a silent device. RDM-LOG30 shows the
// browser sweep spending 9-12 DEVICE_INFO requests and 48-63 requests in
// total on each of four responders that never answered any of them — and
// re-spending them on every refresh of the Devices screen, without bound.
// Two bounded passes for the life of a discovery is the whole budget here.
const defaultMaxAttempts = 2

// defaultPassTimeout bounds one device's whole core-identity pass. It is
// generous relative to six sequential GETs on a healthy device (RDM-LOG30
// shows those completing 4-5 ms apart) and exists only so a device that
// stalls mid-pass cannot hold the single reader goroutine forever.
const defaultPassTimeout = 45 * time.Second

// Config is the reader's wiring. Ctrl is the only required field.
type Config struct {
	// Ctrl is the controller every read goes through — the same one the
	// operator's own reads use, so this contends for the line on equal
	// terms rather than beside it.
	Ctrl *session.RDMController
	// MaxAttempts overrides defaultMaxAttempts. Zero means the default;
	// a negative value means "never stop", which nothing should ask for.
	MaxAttempts int
	// PassTimeout overrides defaultPassTimeout.
	PassTimeout time.Duration
	// OnPass, if non-nil, is called after every completed pass. It is a
	// test and diagnostics seam; it is called from the reader goroutine and
	// must not block.
	OnPass func(Pass)
}

// Pass reports one completed core-identity pass.
type Pass struct {
	Node     session.NodeRef
	UID      rdm.UID
	Attempt  int
	State    State
	Result   params.CoreIdentityResult
	Finished time.Time
}

// Stats is a snapshot of what the reader has done, for diagnostics.
type Stats struct {
	Known   int
	Pending int
	Read    int
	GaveUp  int
	Passes  int
}

// Key identifies one device on one node port — the same identity
// registry.Registry keys its fixture table on.
type Key struct {
	IP   netip.Addr
	Bind byte
	Port uint16
	UID  rdm.UID
}

// KeyFor builds a Key from the pieces a caller normally has.
func KeyFor(ip netip.Addr, bind byte, port artnet.PortAddress, uid rdm.UID) Key {
	return Key{IP: ip, Bind: bind, Port: port.RawValue(), UID: uid}
}

type entry struct {
	node     session.NodeRef
	uid      rdm.UID
	state    State
	attempts int
}

// Reader is the background identity reader. The zero value is not usable;
// build one with New and run it with Run.
type Reader struct {
	cfg Config

	mu      sync.Mutex
	entries map[Key]*entry
	queue   []Key
	passes  int

	// wake carries one bit: "the queue may be non-empty". Buffered to 1 and
	// sent non-blocking, so Note never blocks its caller — which is
	// Registry's own event loop.
	wake chan struct{}
}

// New builds a Reader. It starts nothing; call Run.
func New(cfg Config) *Reader {
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.PassTimeout <= 0 {
		cfg.PassTimeout = defaultPassTimeout
	}
	return &Reader{
		cfg:     cfg,
		entries: make(map[Key]*entry),
		wake:    make(chan struct{}, 1),
	}
}

// Note tells the reader a ToD named this UID on this node port. It is the
// callback registry.Registry.SetOnDeviceSeen is given.
//
// It never blocks and it never drops: a UID already in the ledger — queued,
// being read, read, or given up on — is left exactly as it is, which is what
// makes a second, third or fifteenth ToD naming the same device free. A UID
// the reader has not seen before is appended to the queue.
func (r *Reader) Note(node session.NodeRef, uid rdm.UID) {
	k := KeyFor(node.Key.IP, node.Key.BindIndex, node.Port, uid)
	r.mu.Lock()
	if _, ok := r.entries[k]; ok {
		r.mu.Unlock()
		return
	}
	r.entries[k] = &entry{node: node, uid: uid, state: StatePending}
	r.queue = append(r.queue, k)
	r.mu.Unlock()
	r.signal()
}

func (r *Reader) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run drains the queue, one device at a time, until ctx is cancelled.
//
// Exactly one goroutine — this one — ever issues a background read, which is
// where the serialization guarantee comes from. It owns no socket and no
// timer: cancelling ctx ends the current pass through the controller's own
// context plumbing and returns.
func (r *Reader) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		k, e, ok := r.take()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-r.wake:
				continue
			}
		}
		r.readOne(ctx, k, e)
	}
}

// take pops the next pending device, marking it StateReading under the lock
// so a concurrent Note or Forget sees an accurate state.
func (r *Reader) take() (Key, entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.queue) > 0 {
		k := r.queue[0]
		r.queue = r.queue[1:]
		e, ok := r.entries[k]
		if !ok || e.state != StatePending {
			// Forgotten, or already handled, between queueing and now.
			continue
		}
		e.state = StateReading
		return k, *e, true
	}
	return Key{}, entry{}, false
}

// readOne runs one core-identity pass and files the outcome.
func (r *Reader) readOne(ctx context.Context, k Key, e entry) {
	passCtx, cancel := context.WithTimeout(ctx, r.cfg.PassTimeout)
	res := params.New(r.cfg.Ctrl, e.node, e.uid).ReadCoreIdentity(passCtx)
	cancel()

	// The values themselves need no plumbing from here: every GET went
	// through the controller, so registry.Registry has already cached each
	// ACK off the EventCommandComplete stream. What this function decides is
	// only whether to ask again.
	r.mu.Lock()
	cur, still := r.entries[k]
	if !still {
		// Forgotten mid-pass (an explicit Inspect, a devices clear, a full
		// reset). The pass's answers are still in the registry — they were
		// real — but this ledger entry is gone and must not be resurrected.
		r.passes++
		p := Pass{Node: e.node, UID: e.uid, Attempt: e.attempts + 1, State: StateUnknown, Result: res, Finished: time.Now()}
		r.mu.Unlock()
		r.report(p)
		return
	}
	if cur.state != StateReading {
		// Reset mid-pass and re-queued. Leave the new state alone.
		r.passes++
		p := Pass{Node: e.node, UID: e.uid, Attempt: e.attempts + 1, State: cur.state, Result: res, Finished: time.Now()}
		r.mu.Unlock()
		r.report(p)
		return
	}
	cur.attempts++
	switch {
	case res.HaveDeviceInfo:
		cur.state = StateRead
	case ctx.Err() != nil:
		// Shutdown, not a refusal by the device. Do not spend an attempt on
		// the rig's behalf for something this process did.
		cur.attempts--
		cur.state = StatePending
		r.queue = append(r.queue, k)
	case r.cfg.MaxAttempts > 0 && cur.attempts >= r.cfg.MaxAttempts:
		// Bounded, and stated. StateGaveUp is the honest answer the Devices
		// screen prints; it is not a zero dressed up as a reading.
		cur.state = StateGaveUp
	default:
		cur.state = StatePending
		r.queue = append(r.queue, k)
	}
	r.passes++
	p := Pass{Node: e.node, UID: e.uid, Attempt: cur.attempts, State: cur.state, Result: res, Finished: time.Now()}
	requeued := cur.state == StatePending
	r.mu.Unlock()
	if requeued {
		r.signal()
	}
	r.report(p)
}

func (r *Reader) report(p Pass) {
	if r.cfg.OnPass != nil {
		r.cfg.OnPass(p)
	}
}

// State reports what the reader knows about one device, and how many passes
// it has spent on it.
func (r *Reader) State(k Key) (State, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[k]
	if !ok {
		return StateUnknown, 0
	}
	return e.state, e.attempts
}

// Forget drops every ledger entry for uid, on every node port, so the next
// mention of it starts from zero attempts. This is the cap reset an explicit
// Inspect is entitled to: an operator asking for a device by name outranks
// the reader's own budget. Returns how many entries were dropped.
//
// It deliberately does NOT cancel a pass already in flight — a read on the
// wire is left to finish, and readOne notices the entry has gone.
func (r *Reader) Forget(uid rdm.UID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k := range r.entries {
		if k.UID == uid {
			delete(r.entries, k)
			n++
		}
	}
	return n
}

// ForgetPort drops every ledger entry on one node port, matched on IP and
// Port-Address alone — the same granularity, and the same deliberate
// disregard for BindIndex, as registry.ClearDevicesOnPort and
// session.RDMController.ClearToDPort, which POST /api/devices/clear drives
// in the same breath.
func (r *Reader) ForgetPort(ip netip.Addr, port artnet.PortAddress) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k := range r.entries {
		if k.IP == ip && k.Port == port.RawValue() {
			delete(r.entries, k)
			n++
		}
	}
	return n
}

// Reset empties the ledger entirely: every device is unknown again, and the
// next ToD re-reads the rig from scratch. This is what POST /api/devices/clear
// (scope "all") and the full reset call, alongside registry.ClearDevices and
// RDMController.ClearToD — a cleared device table with a full ledger behind it
// would be a rig that never fills in again.
func (r *Reader) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = make(map[Key]*entry)
	r.queue = nil
}

// Stats snapshots the ledger for the diagnostics endpoint.
func (r *Reader) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Stats{Known: len(r.entries), Passes: r.passes}
	for _, e := range r.entries {
		switch e.state {
		case StatePending, StateReading:
			st.Pending++
		case StateRead:
			st.Read++
		case StateGaveUp:
			st.GaveUp++
		}
	}
	return st
}
