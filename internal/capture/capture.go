// Package capture implements a bounded ring buffer of decoded packet
// summaries, feeding the Analyzer screen. It knows nothing about sockets:
// callers push already-decoded artnet.Packet values (or Direction-tagged
// raw bytes) in; subscribers pull batches out at a throttled interval.
package capture

import (
	"net/netip"
	"sync"
	"time"

	"benny512/internal/artnet"
)

// Direction of one captured datagram.
type Direction int

// Directions.
const (
	DirIn Direction = iota
	DirOut
	// DirNote marks an entry that is not a datagram at all: a controller
	// decision worth recording in the same timeline as the packets it
	// explains. See NoteEntry.
	DirNote
)

// String renders the direction.
func (d Direction) String() string {
	switch d {
	case DirOut:
		return "out"
	case DirNote:
		return "note"
	default:
		return "in"
	}
}

// HexThreshold is the default size below which raw hex is retained in full;
// above it, hex is omitted (Size still reports the true length) to keep
// capture memory bounded regardless of individual packet size. RDM datagrams
// top out around 260 bytes (24-byte header + 231-byte max PDL + checksum),
// so every RDM/ToD entry's full hex is always retained regardless of this
// threshold — it only ever trims the occasional oversized ArtDmx/PollReply.
//
// This threshold is deliberately not applied to an entry that failed to
// decode (DecodeErr != ""): the raw bytes are the only diagnostic evidence
// such an entry carries, and an oversized malformed/garbage datagram is
// exactly the case where hiding them behind a size cap would be most
// counterproductive. See DecodeEntry and Ring.Add.
const HexThreshold = 600

// DefaultCapacity is the general ring buffer's default entry count.
const DefaultCapacity = 10000

// DefaultRDMCapacity is the RDM-only ring's default entry count — sized for
// a multi-hour bench session's worth of GET/SET/DISCOVERY/ToD exchanges.
// ArtDmx at 40Hz would flush a shared 10k-entry ring of RDM records in
// minutes; routing RDM/ToD traffic into a dedicated, much larger ring (each
// entry is small — at most a few hundred bytes of hex/decoded strings) means
// a long session's RDM history survives regardless of how much DMX chatter
// happens alongside it. See IsRDMLoggable for the full routing rule
// (IsRDMKind's four opcodes plus undecodable datagrams) and
// UnknownOpcodeThrottle for the bounded addition of unrecognized-but-valid
// opcodes.
const DefaultRDMCapacity = 100000

// DefaultBatchInterval is the server-side throttle for Subscribe deliveries.
const DefaultBatchInterval = 100 * time.Millisecond

// KindUndecoded and KindUnrecognized are Entry.Kind's two "we don't have
// decoded fields for this" values, and are easy to mistake for each other
// from a diff or a quick skim of a log — they differ only by the case of
// their first letter. They are named here, rather than left as bare string
// literals at every comparison site, specifically so that trap is visible:
//
//   - KindUndecoded: artnet.Decode returned an error — the raw datagram
//     didn't parse as Art-Net at all (bad ID, truncated, malformed known
//     opcode). See Entry.DecodeErr, which is non-empty exactly when Kind is
//     this value.
//   - KindUnrecognized: artnet.Decode succeeded — a valid "Art-Net\0"
//     envelope, but carrying an OpCode this package has no decoder for
//     (vendor extension, newer spec revision, or an opcode like ArtSync
//     this codebase doesn't model). Not a failure; DecodeErr is empty. The
//     actual numeric opcode is in Entry.UnknownOpCode.
//
// Renaming the underlying string values themselves (e.g. to remove the
// case-only distinction) was considered and rejected: both values are
// serialized as-is into the disk log text, the websocket JSON batch, and
// the JSON export, so changing them would be a wire-format change for a
// file the owner already has on disk from past sessions, for a benefit
// (avoiding a case typo in *this package's own source*) that these two
// named constants already provide.
const (
	KindUndecoded    = "unknown"
	KindUnrecognized = "Unknown"
)

// KindNote is Entry.Kind for a NoteEntry — a controller decision recorded
// in the packet timeline rather than a datagram.
const KindNote = "Note"

// NoteEntry builds a zero-byte entry recording something the controller
// decided, for interleaving with the packets in the RDM log.
//
// It exists because of a gap the round-4 bench log exposed: when Benny512
// stops asking a device, it stops *sending*, so the very decision that
// explains a suddenly-quiet log leaves no trace in the log. Reading
// RDM-LOG4 required inferring "the controller gave up here" from an absence
// — exactly the kind of inference an instrument should not require. A note
// line states it.
//
// Size is 0 and Peer is left invalid: nothing went on the wire.
func NoteEntry(at time.Time, text string) Entry {
	return Entry{Time: at, Dir: DirNote, Kind: KindNote, Key: text}
}

// Entry is one decoded packet summary.
type Entry struct {
	Seq      uint64
	Time     time.Time
	Dir      Direction
	Peer     netip.AddrPort
	Kind     string // artnet opcode name, e.g. "ArtDmx", "ArtRdm"; see also KindUndecoded/KindUnrecognized
	Universe uint16 // raw Port-Address value, when applicable; 0 otherwise
	Size     int
	Key      string // short human summary of key fields (e.g. "seq=3 len=512" or UID/PID for RDM)
	HexTrunc bool
	Hex      string // hex of raw bytes, present only when Size <= HexThreshold

	// DecodeErr carries artnet.Decode's error text when the raw datagram
	// didn't decode at all (Kind is then left at KindUndecoded). Empty for
	// every entry that decoded successfully, including a
	// recognized-envelope-but-vendor-opcode packet (Kind ==
	// KindUnrecognized) — that's a legitimate decode, not a failure, and is
	// deliberately left out of this field.
	DecodeErr string `json:"DecodeErr,omitempty"`

	// UnknownOpCode is the raw Art-Net OpCode read from the envelope when
	// Kind == KindUnrecognized; zero for every other Kind (including
	// KindUndecoded — a datagram that failed to decode may not even have a
	// reliably-readable OpCode, depending on where decoding gave up).
	UnknownOpCode uint16 `json:"unknownOpCode,omitempty"`

	// RDM carries the full decoded detail for an ArtRdm entry (Kind ==
	// "ArtRdm"), nil for every other Kind. See rdmdetail.go.
	RDM *RDMDetail `json:"RDM,omitempty"`
	// Tod carries decoded detail for ArtTodRequest/ArtTodData/ArtTodControl
	// entries, nil otherwise. See rdmdetail.go.
	Tod *TodDetail `json:"Tod,omitempty"`

	// Poll carries decoded detail for an ArtPoll entry, nil otherwise. See
	// nodeconfigdetail.go.
	Poll *PollDetail `json:"Poll,omitempty"`
	// PollReply carries decoded detail for an ArtPollReply entry — the
	// primary evidence for the "every port shows n/a" bug class (report
	// task 1) — nil otherwise. See nodeconfigdetail.go.
	PollReply *PollReplyDetail `json:"PollReply,omitempty"`
	// NodeConfig carries decoded detail for the three remote
	// node-configuration packet kinds (ArtAddress, ArtInput, ArtIpProg) and
	// the one reply that answers ArtIpProg (ArtIpProgReply) — grouped under
	// one tagged struct exactly the way Tod groups ArtTodRequest/Data/
	// Control, since these four are the "rare, user-initiated, want a
	// complete record" family the --lognodes flag exists for. Nil for every
	// other Kind. See nodeconfigdetail.go.
	NodeConfig *NodeConfigDetail `json:"NodeConfig,omitempty"`
}

// IsRDMKind reports whether kind is one of the RDM-family Art-Net opcodes
// (ArtRdm, ArtTodRequest, ArtTodData, ArtTodControl) — the set routed into a
// dedicated RDM-only ring so high-rate ArtDmx traffic never evicts RDM
// exchanges from a shared buffer during a long bench session.
func IsRDMKind(kind string) bool {
	switch kind {
	case "ArtRdm", "ArtTodRequest", "ArtTodData", "ArtTodControl":
		return true
	default:
		return false
	}
}

// IsRDMLoggable reports whether e belongs in the dedicated RDM-only ring and
// continuous disk log: either it decoded as one of the four RDM-family
// opcodes (IsRDMKind), or it didn't decode at all (e.DecodeErr != "").
//
// The second half of that OR is the point: a datagram the decoder chokes on
// is exactly the case the RDM diagnostic log exists to catch. Without it, a
// log showing 42 outbound ArtRdm requests and zero inbound entries can't
// tell "the gear never answered" from "the gear answered and we couldn't
// parse the reply" — two problems with opposite fixes.
//
// This is a separate function from IsRDMKind rather than IsRDMKind itself
// growing an "or unknown" branch: IsRDMKind's contract, per its own doc
// comment and TestIsRDMKind (which asserts IsRDMKind("unknown") == false),
// is "one of these four opcodes" — a pure classifier over a kind string.
// Folding in "failed to decode" would need the Entry (for DecodeErr), not
// just the kind string, and would make IsRDMKind's name a lie for every
// existing caller that reads it as an opcode check.
//
// Undecodable datagrams are expected to be rare in real Art-Net traffic on
// this port. If that assumption ever breaks — a misbehaving neighbor
// blasting non-Art-Net UDP at the port, say — every one of those packets
// would land in the RDM-only ring and disk log too. That defeats the
// RDM-only ring's entire reason for existing (see DefaultRDMCapacity's doc
// comment: keeping RDM history safe from high-rate *legitimate* traffic
// evicting it) and would rotate the disk log much sooner than a bench
// session should otherwise need. There is no rate limiting here for that
// case; it would need its own fix if it ever became real.
//
// IsRDMLoggable deliberately does NOT cover Kind == KindUnrecognized (a
// valid envelope, unrecognized opcode). Unlike a genuine decode failure,
// that case can legitimately arrive at frame rate (ArtSync and other
// opcodes this codebase simply doesn't model) and so needs the bounded
// per-(peer,opcode) handling in UnknownOpcodeThrottle instead of an
// unconditional OR here.
func IsRDMLoggable(e Entry) bool {
	return IsRDMKind(e.Kind) || e.DecodeErr != ""
}

// DefaultUnknownOpcodeCap is how many inbound KindUnrecognized entries per
// distinct (peer, opcode) pair get routed into the RDM diagnostic stream
// before UnknownOpcodeThrottle suppresses that pair. Picked to comfortably
// cover "let me see a few of these to confirm what's going on" during a
// bench session without exposing the RDM-only ring to a legitimate
// high-rate opcode (ArtSync, say) it doesn't decode.
const DefaultUnknownOpcodeCap = 15

// UnknownOpcodeThrottle bounds how many inbound "valid Art-Net envelope,
// opcode we don't recognize" entries (Kind == KindUnrecognized) get routed
// into the RDM diagnostic stream, per distinct (peer, opcode) pair.
//
// This exists because of a real bench hypothesis: a gateway that answers
// discovery but never answers directed RDM might be replying with
// something this codebase doesn't decode — which, before this type
// existed, was just as invisible in the disk log as a genuine decode
// failure. Unlike a decode failure, though, a legitimate opcode this
// package hasn't been taught (ArtSync is the standing example) can arrive
// at frame rate, so routing it into the RDM-only ring unconditionally the
// way IsRDMLoggable does for decode failures would defeat that ring's
// entire purpose. Hence a cap: the first DefaultUnknownOpcodeCap sightings
// of a given (peer, opcode) pair are logged in full, the sighting that
// crosses the cap produces exactly one suppression entry saying so, and
// every sighting after that is counted (see Count) but not logged again.
//
// Outbound entries are never eligible (see Consider) — an outbound
// datagram carrying an opcode this codebase's own encoder doesn't produce
// would be a bug in this program, not a device behavior worth capturing in
// a log meant to diagnose the device.
//
// Safe for concurrent use via an internal mutex. As wired today (see
// cmd/benny512), the only caller is the demux's single inbound-reader
// goroutine, so the mutex is uncontended in practice — but Consider/Count
// are exported precisely so something outside this package can drive a
// throttle too, and nothing here enforces single-goroutine use, so it
// takes the lock rather than relying on today's wiring staying the only
// caller forever. A plain map (not sync.Map) is paired with the mutex
// because the increment-then-compare-to-cap sequence in Consider needs to
// happen atomically as one unit; sync.Map's per-operation atomicity
// wouldn't cover that compound check.
type UnknownOpcodeThrottle struct {
	cap int

	mu     sync.Mutex
	counts map[unknownOpcodeKey]int
}

type unknownOpcodeKey struct {
	peer   netip.AddrPort
	opcode uint16
}

// NewUnknownOpcodeThrottle builds a throttle allowing up to cap logged
// entries per (peer, opcode) pair before suppressing (DefaultUnknownOpcodeCap
// if cap <= 0).
func NewUnknownOpcodeThrottle(cap int) *UnknownOpcodeThrottle {
	if cap <= 0 {
		cap = DefaultUnknownOpcodeCap
	}
	return &UnknownOpcodeThrottle{cap: cap, counts: make(map[unknownOpcodeKey]int)}
}

// Consider evaluates e for the bounded unrecognized-opcode diagnostic case.
// Only inbound, Kind == KindUnrecognized entries are eligible — anything
// else returns (Entry{}, false) immediately without touching any state.
// For an eligible entry, ok is true and logEntry is what the caller should
// route into the RDM ring/disk log for exactly two cases: every sighting of
// this (peer, opcode) pair up to and including the cap (logEntry == e), and
// the one sighting that crosses the cap (logEntry is a synthesized
// suppression entry, not e — see suppressionEntry). Every sighting after
// that returns (Entry{}, false): still counted (see Count), never logged
// again.
func (u *UnknownOpcodeThrottle) Consider(e Entry) (logEntry Entry, ok bool) {
	if e.Dir != DirIn || e.Kind != KindUnrecognized {
		return Entry{}, false
	}
	key := unknownOpcodeKey{peer: e.Peer, opcode: e.UnknownOpCode}

	u.mu.Lock()
	u.counts[key]++
	n := u.counts[key]
	u.mu.Unlock()

	switch {
	case n <= u.cap:
		return e, true
	case n == u.cap+1:
		return suppressionEntry(e, u.cap), true
	default:
		return Entry{}, false
	}
}

// Count returns how many times (peer, opcode) has been seen by Consider,
// including sightings suppressed past the cap — exposed for tests, and so
// the running total isn't lost even though it's only ever logged once (in
// the suppression entry's text, at the moment the cap is crossed).
func (u *UnknownOpcodeThrottle) Count(peer netip.AddrPort, opcode uint16) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.counts[unknownOpcodeKey{peer: peer, opcode: opcode}]
}

// suppressionEntry builds the one-shot log entry emitted the instant a
// (peer, opcode) pair crosses the cap. It keeps e's Dir/Peer/Size (Time is
// left zero; Ring.Add stamps it like any other entry) so it sorts and
// displays like a normal capture entry, but replaces Kind/Key with a human
// sentence identifying the pair and clears every field that would
// otherwise make it look like a second real packet (Hex, DecodeErr,
// UnknownOpCode, RDM, Tod).
func suppressionEntry(e Entry, cap int) Entry {
	return Entry{
		Dir:  e.Dir,
		Peer: e.Peer,
		Kind: "CaptureSuppressed",
		Size: e.Size,
		Key: "suppressing further opcode 0x" + hexU16(e.UnknownOpCode) + " from " + e.Peer.String() +
			" after " + itoa(cap) + " logged this session; further instances are being counted but not logged",
	}
}

// IsNodeConfigKind reports whether kind is one of the four rare,
// user-initiated Art-Net remote node-configuration opcodes (ArtAddress,
// ArtInput, ArtIpProg, ArtIpProgReply) — the "owner presses a config button
// at the bench" family --lognodes exists to give a complete record of.
//
// Unlike IsPeriodicNodeKind's two opcodes, these four are not background
// chatter: a real bench session sees at most a handful of them, driven by an
// explicit user action (setting a universe, renaming a node, programming an
// IP address) or its node's reply to one. There is therefore no cap here —
// every sighting is logged in full when --lognodes is set; see
// cmd/benny512's tap wiring. Deliberately excludes ArtPoll/ArtPollReply
// (IsPeriodicNodeKind's job) even though all six opcodes are part of the
// same report-task item: a poll cycle runs continuously for the life of the
// session and would swamp the log if given this function's "always log"
// treatment.
func IsNodeConfigKind(kind string) bool {
	switch kind {
	case "ArtAddress", "ArtInput", "ArtIpProg", "ArtIpProgReply":
		return true
	default:
		return false
	}
}

// IsPeriodicNodeKind reports whether kind is one of the two Art-Net
// node-discovery opcodes (ArtPoll, ArtPollReply) that run continuously in
// the background for the life of a session, rather than being triggered by
// a one-off user action the way IsNodeConfigKind's four opcodes are.
//
// A poll cycle typically repeats every few seconds for as long as the
// program runs; logging every instance unconditionally would swamp a bench
// log and bury the RDM exchanges the file exists for (the same reasoning
// DefaultRDMCapacity's doc comment gives for keeping the RDM-only ring safe
// from high-rate ArtDmx). These two therefore go through
// PeriodicNodeThrottle's bounded per-(peer,kind) cap instead of
// IsNodeConfigKind's unconditional "always log".
func IsPeriodicNodeKind(kind string) bool {
	switch kind {
	case "ArtPoll", "ArtPollReply":
		return true
	default:
		return false
	}
}

// DefaultPeriodicNodeCap is how many ArtPoll/ArtPollReply entries per
// distinct (peer, kind) pair PeriodicNodeThrottle logs before suppressing.
// A real ArtPollReply's per-port fields (PortTypes/GoodInput/GoodOutputA/
// GoodOutputB/SwIn/SwOut — the report task's primary evidence for the
// "every port shows n/a" bug) don't change between one poll cycle and the
// next unless the owner reconfigures the node, so one or two sightings per
// node already gives a bench session everything it needs; a handful more
// gives headroom against catching a reply mid-reconfiguration.
const DefaultPeriodicNodeCap = 3

// PeriodicNodeThrottle bounds how many ArtPoll/ArtPollReply entries
// (IsPeriodicNodeKind) get routed into the RDM diagnostic stream, per
// distinct (peer, kind) pair. Same cap-then-suppress-once shape as
// UnknownOpcodeThrottle — see that type's doc comment for the general
// pattern — but keyed by (peer, Kind string) rather than (peer, opcode
// uint16): these two Kinds are already known-and-decoded (unlike
// UnknownOpcodeThrottle's unrecognized-envelope case), so there is no raw
// opcode number to key on, and the Kind string is exactly the dimension
// that matters (an ArtPoll broadcast and its node's ArtPollReply are two
// independent budgets, each capped against that specific chatter repeating
// for the life of the session).
//
// Both directions are eligible (unlike UnknownOpcodeThrottle, which
// deliberately excludes outbound): ArtPoll is normally this program's own
// outbound broadcast and ArtPollReply a node's inbound answer, and both
// sides of that exchange are exactly what a bench session needs capped
// samples of.
//
// Safe for concurrent use via an internal mutex, matching
// UnknownOpcodeThrottle's own concurrency note.
type PeriodicNodeThrottle struct {
	cap int

	mu     sync.Mutex
	counts map[periodicNodeKey]int
}

type periodicNodeKey struct {
	peer netip.AddrPort
	kind string
}

// NewPeriodicNodeThrottle builds a throttle allowing up to cap logged
// entries per (peer, kind) pair before suppressing (DefaultPeriodicNodeCap
// if cap <= 0).
func NewPeriodicNodeThrottle(cap int) *PeriodicNodeThrottle {
	if cap <= 0 {
		cap = DefaultPeriodicNodeCap
	}
	return &PeriodicNodeThrottle{cap: cap, counts: make(map[periodicNodeKey]int)}
}

// Consider evaluates e for the bounded periodic-node-chatter case. Only
// entries with IsPeriodicNodeKind(e.Kind) are eligible — anything else
// returns (Entry{}, false) immediately without touching any state. For an
// eligible entry, ok is true and logEntry is what the caller should route
// into the RDM ring/disk log for exactly two cases: every sighting of this
// (peer, kind) pair up to and including the cap (logEntry == e), and the one
// sighting that crosses the cap (logEntry is a synthesized suppression
// entry, not e). Every sighting after that returns (Entry{}, false): still
// counted (see Count), never logged again.
func (u *PeriodicNodeThrottle) Consider(e Entry) (logEntry Entry, ok bool) {
	if !IsPeriodicNodeKind(e.Kind) {
		return Entry{}, false
	}
	key := periodicNodeKey{peer: e.Peer, kind: e.Kind}

	u.mu.Lock()
	u.counts[key]++
	n := u.counts[key]
	u.mu.Unlock()

	switch {
	case n <= u.cap:
		return e, true
	case n == u.cap+1:
		return periodicSuppressionEntry(e, u.cap), true
	default:
		return Entry{}, false
	}
}

// Count returns how many times (peer, kind) has been seen by Consider,
// including sightings suppressed past the cap.
func (u *PeriodicNodeThrottle) Count(peer netip.AddrPort, kind string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.counts[periodicNodeKey{peer: peer, kind: kind}]
}

// periodicSuppressionEntry builds the one-shot log entry emitted the instant
// a (peer, kind) pair crosses the cap — the periodic-chatter counterpart of
// suppressionEntry (which keys on opcode number rather than a Kind string
// already known and decoded).
func periodicSuppressionEntry(e Entry, cap int) Entry {
	return Entry{
		Dir:  e.Dir,
		Peer: e.Peer,
		Kind: "CaptureSuppressed",
		Size: e.Size,
		Key: "suppressing further " + e.Kind + " from " + e.Peer.String() +
			" after " + itoa(cap) + " logged this session; further instances are being counted but not logged",
	}
}

// Filter narrows what Subscribe/Snapshot returns. Zero-value fields mean
// "no filter on this dimension".
type Filter struct {
	Kind     string // exact opcode match, empty = any
	Universe uint16
	HasUniv  bool
	Source   netip.Addr
	HasSrc   bool
	HasDir   bool
	Dir      Direction

	// RDM-specific filters, matched only against entries with RDM != nil
	// (an entry with no RDM detail never matches a non-zero RDM filter
	// dimension) — the Analyzer's RDM-focused view (report task item 3).
	UID          string // matches either SourceUID or DestUID, empty = any
	PID          uint16
	HasPID       bool
	CommandClass string // exact match against RDMDetail.CommandClass, empty = any
}

func (f Filter) match(e Entry) bool {
	if f.Kind != "" && e.Kind != f.Kind {
		return false
	}
	if f.HasUniv && e.Universe != f.Universe {
		return false
	}
	if f.HasSrc && e.Peer.Addr() != f.Source {
		return false
	}
	if f.HasDir && e.Dir != f.Dir {
		return false
	}
	if f.UID != "" {
		if e.RDM == nil || (e.RDM.SourceUID != f.UID && e.RDM.DestUID != f.UID) {
			return false
		}
	}
	if f.HasPID {
		if e.RDM == nil || e.RDM.PID != f.PID {
			return false
		}
	}
	if f.CommandClass != "" {
		if e.RDM == nil || e.RDM.CommandClass != f.CommandClass {
			return false
		}
	}
	return true
}

// Ring is a bounded, thread-safe ring buffer of Entry with a batched
// subscribe API. Capacity is fixed at construction.
type Ring struct {
	mu      sync.Mutex
	buf     []Entry
	next    int
	count   int
	seq     uint64
	subs    map[int]*subscriber
	subNext int
}

type subscriber struct {
	filter Filter
	ch     chan []Entry
	closed bool

	stateMu  sync.Mutex
	seqState uint64
}

// New builds a Ring with the given capacity (DefaultCapacity if <= 0).
func New(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{
		buf:  make([]Entry, 0, capacity),
		subs: make(map[int]*subscriber),
	}
}

// Capacity returns the ring's fixed capacity.
func (r *Ring) Capacity() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cap(r.buf)
}

// Add appends one entry, evicting the oldest if full, and fans it out to
// matching subscribers' pending batch (delivered on the next throttle tick
// by whatever drives Subscribe — capture package stays passive; the web
// layer owns the ticker).
func (r *Ring) Add(e Entry) Entry {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Size > HexThreshold && e.DecodeErr == "" {
		e.HexTrunc = true
		e.Hex = ""
	}
	capacity := cap(r.buf)
	if len(r.buf) < capacity {
		r.buf = append(r.buf, e)
	} else {
		r.buf[r.next] = e
		r.next = (r.next + 1) % capacity
	}
	if r.count < capacity {
		r.count++
	}
	r.mu.Unlock()
	return e
}

// Clear empties the ring (full-reset flow, task ask: "everything") without
// disturbing its capacity, subscribers, or the monotonic Seq counter —
// Seq keeps counting up from wherever it was rather than resetting to 0, so
// an existing subscriber's lastSeq high-water mark (see subscriber.
// seqState/Publish) stays a valid comparison against whatever gets Added
// next instead of looking like every future entry was already delivered.
func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = r.buf[:0]
	r.next = 0
	r.count = 0
}

// AddPacket is a convenience wrapper: decode a raw datagram (via
// DecodeEntry) and add it.
func (r *Ring) AddPacket(dir Direction, peer netip.AddrPort, raw []byte) Entry {
	return r.Add(DecodeEntry(dir, peer, raw))
}

// DecodeEntry builds an Entry for one directional raw datagram without
// inserting it into any ring. Exported so a caller feeding more than one
// ring from the same datagram (a general capture ring plus the dedicated
// RDM-only ring, see IsRDMLoggable) decodes it exactly once — undecodable
// bytes still produce an Entry with Kind "unknown" rather than being
// dropped, now carrying the decode error in DecodeErr and its full hex
// (bypassing HexThreshold — see that constant's doc comment) so the entry
// is actually useful for diagnosis instead of a dead end.
func DecodeEntry(dir Direction, peer netip.AddrPort, raw []byte) Entry {
	e := Entry{Dir: dir, Peer: peer, Size: len(raw), Kind: KindUndecoded}
	pkt, err := artnet.Decode(raw)
	if err == nil {
		e.Kind = kindName(pkt.Kind)
		e.Universe, e.Key = summarize(pkt)
		attachRDMDetail(&e, pkt)
		attachNodeConfigDetail(&e, pkt)
		if pkt.Kind == artnet.KindUnknown {
			e.UnknownOpCode = pkt.UnknownOpCode
		}
	} else {
		e.DecodeErr = err.Error()
	}
	if len(raw) <= HexThreshold || e.DecodeErr != "" {
		e.Hex = hexEncode(raw)
	}
	return e
}

// Snapshot returns up to the most recent `limit` entries (0 = all
// retained), oldest first, matching filter.
func (r *Ring) Snapshot(f Filter, limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	ordered := r.orderedLocked()
	out := make([]Entry, 0, len(ordered))
	for _, e := range ordered {
		if f.match(e) {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (r *Ring) orderedLocked() []Entry {
	capacity := cap(r.buf)
	if r.count < capacity {
		out := make([]Entry, r.count)
		copy(out, r.buf[:r.count])
		return out
	}
	out := make([]Entry, capacity)
	copy(out, r.buf[r.next:])
	copy(out[capacity-r.next:], r.buf[:r.next])
	return out
}

// Subscribe registers a filtered subscriber and returns a channel of
// batches plus an unsubscribe func. The channel is buffered; a slow
// consumer that falls behind simply misses being woken until it drains (no
// unbounded growth) — the web layer's throttle ticker is expected to drain
// it every DefaultBatchInterval.
func (r *Ring) Subscribe(f Filter) (<-chan []Entry, func()) {
	r.mu.Lock()
	id := r.subNext
	r.subNext++
	sub := &subscriber{filter: f, ch: make(chan []Entry, 8)}
	r.subs[id] = sub
	r.mu.Unlock()

	cancel := func() {
		r.mu.Lock()
		if s, ok := r.subs[id]; ok && !s.closed {
			s.closed = true
			close(s.ch)
			delete(r.subs, id)
		}
		r.mu.Unlock()
	}
	return sub.ch, cancel
}

// Publish is called by the web layer's throttle ticker (~100ms) to flush
// everything added since the last publish to each subscriber, filtered
// per-subscriber. lastSeq/entries let the caller avoid re-scanning: pass the
// full current snapshot (unfiltered) and the sequence number already
// delivered; Publish computes the delta per subscriber.
func (r *Ring) Publish() {
	r.mu.Lock()
	ordered := r.orderedLocked()
	subsCopy := make([]*subscriber, 0, len(r.subs))
	for _, s := range r.subs {
		subsCopy = append(subsCopy, s)
	}
	r.mu.Unlock()

	for _, s := range subsCopy {
		batch := make([]Entry, 0, len(ordered))
		for _, e := range ordered {
			if e.Seq > s.lastSeq() && s.filter.match(e) {
				batch = append(batch, e)
			}
		}
		if len(batch) == 0 {
			continue
		}
		s.setLastSeq(batch[len(batch)-1].Seq)
		select {
		case s.ch <- batch:
		default:
			// Subscriber's channel is full (consumer stalled); drop this
			// batch rather than block the publisher. The next Publish will
			// include an even bigger catch-up batch once it drains.
		}
	}
}

func (s *subscriber) lastSeq() uint64 {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.seqState
}

func (s *subscriber) setLastSeq(v uint64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.seqState = v
}

func kindName(k artnet.PacketKind) string {
	switch k {
	case artnet.KindPoll:
		return "ArtPoll"
	case artnet.KindPollReply:
		return "ArtPollReply"
	case artnet.KindDmx:
		return "ArtDmx"
	case artnet.KindTodRequest:
		return "ArtTodRequest"
	case artnet.KindTodData:
		return "ArtTodData"
	case artnet.KindTodControl:
		return "ArtTodControl"
	case artnet.KindRdm:
		return "ArtRdm"
	case artnet.KindRdmSub:
		return "ArtRdmSub"
	case artnet.KindTimeCode:
		return "ArtTimeCode"
	case artnet.KindAddress:
		return "ArtAddress"
	case artnet.KindInput:
		return "ArtInput"
	case artnet.KindIpProg:
		return "ArtIpProg"
	case artnet.KindIpProgReply:
		return "ArtIpProgReply"
	default:
		return KindUnrecognized
	}
}

// summarize extracts the universe (raw Port-Address, where applicable) and a
// short human-readable key-fields string for a decoded packet.
func summarize(pkt artnet.Packet) (uint16, string) {
	switch pkt.Kind {
	case artnet.KindDmx:
		pa := pkt.Dmx.PortAddress()
		return pa.RawValue(), "seq=" + itoa(int(pkt.Dmx.Sequence)) + " len=" + itoa(len(pkt.Dmx.Data))
	case artnet.KindRdm:
		msg, err := pkt.Rdm.DecodedRDMMessage()
		if err != nil {
			return 0, "decode-error"
		}
		return 0, "uid=" + msg.SourceUID.String() + "->" + msg.DestinationUID.String() +
			" pid=0x" + hexU16(uint16(msg.ParameterID)) + " cc=0x" + hexByte(byte(msg.CommandClass))
	case artnet.KindTodData:
		return 0, "tod uids=" + itoa(len(pkt.TodData.Tod)) + " total=" + itoa(int(pkt.TodData.UidTotal))
	case artnet.KindPollReply:
		return 0, "node=" + pkt.PollReply.ShortName
	case artnet.KindAddress:
		return 0, "cmd=0x" + hexByte(byte(pkt.Address.Command)) + " short=" + pkt.Address.ShortName
	case artnet.KindInput:
		return 0, "bindIndex=" + itoa(int(pkt.Input.BindIndex))
	case artnet.KindIpProg:
		return 0, "cmd=0x" + hexByte(byte(pkt.IpProg.Command))
	case artnet.KindIpProgReply:
		return 0, "ip=" + formatIPv4(pkt.IpProgReply.CurrentIP)
	case artnet.KindUnknown:
		return 0, "opcode=0x" + hexU16(pkt.UnknownOpCode)
	default:
		return 0, ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func hexByte(b byte) string {
	const hexDigits = "0123456789abcdef"
	return string([]byte{hexDigits[b>>4], hexDigits[b&0xF]})
}

func hexU16(v uint16) string {
	return hexByte(byte(v>>8)) + hexByte(byte(v))
}

func hexEncode(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0xF]
	}
	return string(out)
}
