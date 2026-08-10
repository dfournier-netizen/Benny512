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
// capture memory bounded regardless of individual packet size.
const HexThreshold = 600

// DefaultCapacity is the ring buffer's default entry count.
const DefaultCapacity = 10000

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
}

// Filter narrows what Subscribe/Snapshot returns. Zero-value fields mean
// "no filter on this dimension".
type Filter struct {
	Kind     string // exact opcode match, empty = any
	Universe uint16
	HasUniv  bool
	Source   netip.Addr
	HasSrc   bool
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

// AddPacket is a convenience wrapper: decode a raw datagram's opcode kind
// via artnet.Decode (best-effort; undecodable bytes still get an Entry with
// Kind "unknown") and add it.
func (r *Ring) AddPacket(dir Direction, peer netip.AddrPort, raw []byte) Entry {
	e := Entry{Dir: dir, Peer: peer, Size: len(raw), Kind: "unknown"}
	if len(raw) <= HexThreshold {
		e.Hex = hexEncode(raw)
	}
	pkt, err := artnet.Decode(raw)
	if err == nil {
		e.Kind = kindName(pkt.Kind)
		e.Universe, e.Key = summarize(pkt)
	}
	return r.Add(e)
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
