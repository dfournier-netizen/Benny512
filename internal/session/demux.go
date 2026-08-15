package session

import (
	"net/netip"
	"sync"
)

// TapFunc observes one raw datagram in one direction. Used to feed a
// capture ring (or any other passive observer) without this package
// importing anything about capture — internal/capture's own doc comment
// says it "knows nothing about sockets"; the dependency points the other
// way, and TapFunc is the seam a caller outside this package (cmd/benny512)
// wires a capture sink through.
type TapFunc func(data []byte, peer netip.AddrPort)

// Demux solves two problems that come from sharing one production
// Transport (e.g. transport.UDP) across more than one engine, as
// cmd/benny512's real-mode wiring does (ArtNetSession, RDMController and
// DMXOutputEngine all need the same socket):
//
//  1. Transport.Inbound() is a plain Go channel. A channel delivers each
//     value to exactly one reader, never to all of them — so handing the
//     same *transport.UDP to both ArtNetSession.Run and RDMController.Run
//     silently split every inbound datagram between the two loops at
//     random (whichever loop's channel-read happened to win the race for
//     that particular message), rather than delivering it to both. In
//     practice this meant roughly half of all real inbound RDM responses
//     were never seen by RDMController at all — a bug this type exists to
//     close, found while wiring up real-traffic capture (see
//     cmd/benny512's buildReal, which used to hand `udp` to three engines
//     directly).
//  2. Feeding a capture ring real traffic needs one choke point that sees
//     every datagram exactly once regardless of how many engines
//     subscribe. Demux calls OnSend for every outbound Send/Broadcast and
//     OnReceive for every inbound datagram, before fan-out.
//
// Construct with NewDemux, register OnSend/OnReceive, call Subscriber()
// once per engine that needs its own Inbound() view, then Start().
type Demux struct {
	underlying Transport

	OnSend    TapFunc
	OnReceive TapFunc

	mu      sync.Mutex
	subs    []chan Inbound
	started bool
}

// NewDemux wraps t. Register OnSend/OnReceive and call Subscriber() before
// Start.
func NewDemux(t Transport) *Demux {
	return &Demux{underlying: t}
}

// Subscriber returns an independent Transport view: Send/Broadcast delegate
// straight to the underlying transport (tapped via OnSend), and Inbound()
// returns a private channel fed by Demux's single fan-out reader. Call
// before Start — the reader goroutine snapshots the subscriber list once,
// so subscribing after Start has no effect.
func (d *Demux) Subscriber() Transport {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan Inbound, 256)
	d.subs = append(d.subs, ch)
	return &demuxView{d: d, ch: ch}
}

// Start begins the single fan-out reader goroutine. Safe to call at most
// once; later calls are no-ops.
func (d *Demux) Start() {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return
	}
	d.started = true
	subs := append([]chan Inbound(nil), d.subs...)
	d.mu.Unlock()
	go d.pump(subs)
}

func (d *Demux) pump(subs []chan Inbound) {
	in := d.underlying.Inbound()
	for msg := range in {
		if d.OnReceive != nil {
			d.OnReceive(msg.Data, msg.From)
		}
		for _, ch := range subs {
			select {
			case ch <- msg:
			default:
				// A subscriber's own buffer is full; drop rather than block
				// the shared reader (matches transport.UDP's recvLoop's own
				// drop-on-full policy — a stalled engine should never stall
				// the others).
			}
		}
	}
	for _, ch := range subs {
		close(ch)
	}
}

type demuxView struct {
	d  *Demux
	ch chan Inbound
}

func (v *demuxView) Send(data []byte, dst netip.AddrPort) error {
	err := v.d.underlying.Send(data, dst)
	if err == nil && v.d.OnSend != nil {
		v.d.OnSend(data, dst)
	}
	return err
}

func (v *demuxView) Broadcast(data []byte) error {
	err := v.d.underlying.Broadcast(data)
	if err == nil && v.d.OnSend != nil {
		v.d.OnSend(data, netip.AddrPort{})
	}
	return err
}

func (v *demuxView) Inbound() <-chan Inbound { return v.ch }

var _ Transport = (*demuxView)(nil)
