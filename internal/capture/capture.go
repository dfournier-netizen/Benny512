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
)

// String renders the direction.
func (d Direction) String() string {
	if d == DirOut {
		return "out"
	}
	return "in"
}

// HexThreshold is the default size below which raw hex is retained in full;
// above it, hex is omitted (Size still reports the true length) to keep
// capture memory bounded regardless of individual packet size. RDM datagrams
// top out around 260 bytes (24-byte header + 231-byte max PDL + checksum),
// so every RDM/ToD entry's full hex is always retained regardless of this
// threshold — it only ever trims the occasional oversized ArtDmx/PollReply.
const HexThreshold = 600

// DefaultCapacity is the general ring buffer's default entry count.
const DefaultCapacity = 10000

// DefaultRDMCapacity is the RDM-only ring's default entry count — sized for
// a multi-hour bench session's worth of GET/SET/DISCOVERY/ToD exchanges.
// ArtDmx at 40Hz would flush a shared 10k-entry ring of RDM records in
// minutes; routing RDM/ToD traffic into a dedicated, much larger ring (each
// entry is small — at most a few hundred bytes of hex/decoded strings) means
// a long session's RDM history survives regardless of how much DMX chatter
// happens alongside it. See IsRDMKind for which opcodes route here.
const DefaultRDMCapacity = 100000

// DefaultBatchInterval is the server-side throttle for Subscribe deliveries.
const DefaultBatchInterval = 100 * time.Millisecond

// Entry is one decoded packet summary.
type Entry struct {
	Seq      uint64
	Time     time.Time
	Dir      Direction
	Peer     netip.AddrPort
	Kind     string // artnet opcode name, e.g. "ArtDmx", "ArtRdm"
	Universe uint16 // raw Port-Address value, when applicable; 0 otherwise
	Size     int
	Key      string // short human summary of key fields (e.g. "seq=3 len=512" or UID/PID for RDM)
	HexTrunc bool
	Hex      string // hex of raw bytes, present only when Size <= HexThreshold

	// RDM carries the full decoded detail for an ArtRdm entry (Kind ==
	// "ArtRdm"), nil for every other Kind. See rdmdetail.go.
	RDM *RDMDetail `json:"RDM,omitempty"`
	// Tod carries decoded detail for ArtTodRequest/ArtTodData/ArtTodControl
	// entries, nil otherwise. See rdmdetail.go.
	Tod *TodDetail `json:"Tod,omitempty"`
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
	if e.Size > HexThreshold {
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
// RDM-only ring, see IsRDMKind) decodes it exactly once — undecodable bytes
// still produce an Entry with Kind "unknown" rather than being dropped.
func DecodeEntry(dir Direction, peer netip.AddrPort, raw []byte) Entry {
	e := Entry{Dir: dir, Peer: peer, Size: len(raw), Kind: "unknown"}
	if len(raw) <= HexThreshold {
		e.Hex = hexEncode(raw)
	}
	pkt, err := artnet.Decode(raw)
	if err == nil {
		e.Kind = kindName(pkt.Kind)
		e.Universe, e.Key = summarize(pkt)
		attachRDMDetail(&e, pkt)
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
	default:
		return "Unknown"
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
