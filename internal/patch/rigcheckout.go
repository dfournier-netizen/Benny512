package patch

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
)

// This file is Rig Check's output boundary: the one place that knows which
// wire protocol a universe is currently being driven on, and the only place
// that talks to either transmitter.
//
// WHY THIS IS NOT INSIDE DMXOutputEngine. The obvious-looking change --
// have session.DMXOutputEngine.transmitLocked also emit an E1.31 packet --
// is wrong three times over, and each reason was checked against the code
// before this file was written:
//
//  1. DMXOutputEngine.universes (session/dmxout.go) is ONE GLOBAL SET shared
//     by Rig Check, the test-pattern engine and POST /api/dmx. A protocol
//     switch made there is not a Rig Check setting at all: every universe any
//     screen ever started would start going out on both protocols, including
//     universes Rig Check has never heard of.
//  2. transmitLocked holds e.mu across a synchronous UDP write, and
//     SendNow is called straight from HTTP handler goroutines. A second
//     synchronous write in there doubles the lock hold time on every tick for
//     every universe, whether or not anyone asked for sACN.
//  3. While a pattern is live each universe is ALREADY transmitted twice per
//     interval (recomputePatternLocked calls SendNow, and the engine's own
//     tick fires independently). Dual-protocol sending inside the engine
//     multiplies that, not adds to it.
//
// So Rig Check owns a narrow target instead, and exactly ONE protocol is live
// for a given universe at a time. Selecting sACN does not mean "also send
// sACN": a universe driven over sACN is never registered with
// DMXOutputEngine at all, so the engine's tick has nothing to transmit for
// it. Switching protocol while output is live goes through RigCheck's normal
// stop path first, which runs the outgoing protocol's proper shutdown --
// Art-Net: zero frame, then StopUniverse; sACN: Sender.Stop, which is three
// zero frames followed by three Stream_Terminated packets.

// Protocol names the wire protocol Rig Check transmits on.
type Protocol string

// The protocols Rig Check can drive. ProtocolArtNet is the default and is
// exactly what every caller got before sACN existed.
const (
	ProtocolArtNet Protocol = "artnet"
	ProtocolSACN   Protocol = "sacn"
)

// NormalizeProtocol maps an API-supplied string onto a Protocol. An empty
// string is Art-Net, which is what makes a request with no protocol field
// behave exactly as it did before this feature. Anything else is an error --
// never a silent fallback to Art-Net, because "I asked for sACN and got
// Art-Net" is precisely the failure a rig tech cannot see from the console.
func NormalizeProtocol(s string) (Protocol, error) {
	switch Protocol(s) {
	case "", ProtocolArtNet:
		return ProtocolArtNet, nil
	case ProtocolSACN:
		return ProtocolSACN, nil
	}
	return "", fmt.Errorf("patch: protocol must be %q or %q, got %q", ProtocolArtNet, ProtocolSACN, s)
}

// ErrSACNNotConfigured is returned when sACN output is requested on a server
// that has no sACN binding wired (every Server has one; this is the guard for
// a directly-constructed RigCheck in a test or a future embedder).
var ErrSACNNotConfigured = errors.New("patch: sACN output is not configured on this server")

// --- sACN cadence --------------------------------------------------------
//
// Art-Net's retransmit cadence is the DMX engine's own tick. sACN needs its
// own, and ANSI E1.31-2025 Section 6.6.2 specifies it precisely:
//
//	"For a given universe number, transmitting devices shall transmit Null
//	START Code data only when that data changes, with the following
//	exceptions: 1. Three packets containing the non-changing Property Values
//	... shall be sent before the initiation of transmission suppression.
//	2. Thereafter, a single keep-alive packet shall be transmitted at
//	intervals of between 800mS and 1000mS."
//
// That is the whole cadence, and it is why this is not simply "send at 40Hz
// like Art-Net": E1.31 explicitly asks sources NOT to flood unchanged data.
const (
	// SACNSuppressionBurst is exception 1's three packets: after a frame
	// changes, it goes out three times before transmission suppression
	// begins. It doubles as datagram-loss redundancy, the same reasoning
	// sacn.StopZeroFrameCount already documents.
	SACNSuppressionBurst = 3

	// SACNBurstInterval is the spacing between those three packets, and the
	// tick period of the refresh timer generally. 25ms is 40Hz -- the same
	// rate session.DefaultDMXRate uses, which sits just under DMX512-A's
	// ~44Hz ceiling. Section 6.6.1 forbids exceeding the E1.11 maximum
	// refresh rate unless the user configures it, so the burst is paced
	// rather than sent back-to-back.
	SACNBurstInterval = 25 * time.Millisecond

	// SACNKeepAliveInterval is exception 2's keep-alive. 850ms sits inside
	// the required 800-1000ms window with margin at both ends, and is
	// comfortably under E131_NETWORK_DATA_LOSS_TIMEOUT (2.5 seconds,
	// Appendix A), which Section 6.7.1 makes the point at which a receiver
	// declares the source disconnected.
	SACNKeepAliveInterval = 850 * time.Millisecond
)

// SACNStream is the subset of *sacn.Sender this package uses. It is an
// interface so internal/patch does not import internal/sacn (keeping the
// dependency arrow pointing one way) and so a test can observe the exact
// call sequence -- Send, Stop, Close -- without a socket.
type SACNStream interface {
	Send(universe uint16, payload []byte) error
	Stop(universe uint16) error
	Close() error
}

// SACNBinding is everything RigCheck needs from the server to drive sACN,
// supplied by internal/web (which owns both numbering settings and the NIC).
type SACNBinding struct {
	// Open opens the sending socket. It is called lazily, the first time a
	// run actually needs sACN, and the stream it returns is Closed on the
	// next stop -- so a server that never checks a rig over sACN never opens
	// a second UDP socket.
	Open func() (SACNStream, error)
	// Universe maps a raw Art-Net Port-Address to the sACN universe it is
	// transmitted on, or returns an error naming the universe it cannot map.
	// See sacn.ArtnetPortAddressToSACNUniverse.
	Universe func(raw uint16) (uint16, error)
}

func (b SACNBinding) ready() bool { return b.Open != nil && b.Universe != nil }

// rigOutput is RigCheck's output target. Every method is called with
// RigCheck.mu held, so it carries no lock of its own; the refresh timer's
// callback is a RigCheck method that takes that same mutex first.
type rigOutput struct {
	dmx    *session.DMXOutputEngine
	clock  session.Clock
	onTick func()

	proto   Protocol
	binding SACNBinding

	artnetLive map[uint16]bool   // raw Port-Address -> registered with DMXOutputEngine
	sacnLive   map[uint16]uint16 // raw Port-Address -> sACN universe
	stream     SACNStream

	frames   map[uint16][]byte    // last frame handed to us, per raw Port-Address
	burst    map[uint16]int       // Section 6.6.2 exception 1 packets still owed
	lastSent map[uint16]time.Time // for exception 2's keep-alive window

	timer   session.Timer
	sendErr error
}

func newRigOutput(dmx *session.DMXOutputEngine, clock session.Clock) *rigOutput {
	return &rigOutput{
		dmx: dmx, clock: clock, proto: ProtocolArtNet,
		artnetLive: map[uint16]bool{}, sacnLive: map[uint16]uint16{},
		frames: map[uint16][]byte{}, burst: map[uint16]int{}, lastSent: map[uint16]time.Time{},
	}
}

// validate checks, before anything is stopped or started, that every universe
// in a proposed scope can actually be driven on proto. This is what makes
// "refuse rather than clamp" observable to the caller: a scope containing one
// unmappable universe fails the whole start, with the offending universe
// named, and the previous run is left untouched.
func (o *rigOutput) validate(proto Protocol, raws []uint16) error {
	if proto != ProtocolSACN {
		return nil
	}
	if !o.binding.ready() {
		return ErrSACNNotConfigured
	}
	for _, raw := range raws {
		if _, err := o.binding.Universe(raw); err != nil {
			return err
		}
	}
	return nil
}

// startUniverse brings one raw Port-Address live on the current protocol.
func (o *rigOutput) startUniverse(raw uint16) error {
	pa, err := artnet.PortAddressFromRaw(raw)
	if err != nil {
		return err
	}
	if o.proto != ProtocolSACN {
		o.dmx.StartUniverse(pa, netip.AddrPort{}, session.DMXUniverseSize)
		o.artnetLive[raw] = true
		return nil
	}
	if !o.binding.ready() {
		return ErrSACNNotConfigured
	}
	su, err := o.binding.Universe(raw)
	if err != nil {
		return err
	}
	if o.stream == nil {
		s, err := o.binding.Open()
		if err != nil {
			return err
		}
		o.stream = s
	}
	o.sacnLive[raw] = su
	return nil
}

// setFrames publishes a whole recomputed frame set. force bypasses Section
// 6.6.2's change-detection for the sACN path -- Blackout uses it, because
// "the hard all-off that always works" must put a packet on the wire even if
// the frame it is replacing was already zero.
func (o *rigOutput) setFrames(frames map[uint16][]byte, force bool) {
	if o.proto == ProtocolSACN {
		now := o.clock.Now()
		for raw, data := range frames {
			if _, live := o.sacnLive[raw]; !live {
				continue
			}
			if !force && o.burst[raw] == 0 && bytes.Equal(o.frames[raw], data) {
				continue // Section 6.6.2: transmit Null START Code data only when it changes
			}
			o.frames[raw] = data
			o.burst[raw] = SACNSuppressionBurst
		}
		o.refresh(now)
		return
	}
	for raw, data := range frames {
		if pa, err := artnet.PortAddressFromRaw(raw); err == nil {
			_ = o.dmx.SetFrame(pa, data)
		}
	}
	o.dmx.SendNow()
}

// refresh sends whatever the cadence says is due now and re-arms the timer.
func (o *rigOutput) refresh(now time.Time) {
	for raw, su := range o.sacnLive {
		data := o.frames[raw]
		if data == nil {
			continue
		}
		if o.burst[raw] == 0 && now.Sub(o.lastSent[raw]) < SACNKeepAliveInterval {
			continue
		}
		if err := o.stream.Send(su, data); err != nil {
			o.sendErr = err
			continue
		}
		o.lastSent[raw] = now
		if o.burst[raw] > 0 {
			o.burst[raw]--
		}
	}
	o.arm()
}

// arm schedules the next refresh, and is the only place that does. It is a
// no-op unless sACN universes are actually live, which is what stops the
// timer chain (and its goroutine) dead at stopAll.
func (o *rigOutput) arm() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	if o.proto != ProtocolSACN || len(o.sacnLive) == 0 || o.onTick == nil {
		return
	}
	o.timer = o.clock.AfterFunc(SACNBurstInterval, o.onTick)
}

// stopAll takes every live universe off the air on its own protocol and
// releases the sACN socket. Callers blackout first (RigCheck.stopLocked
// does), so this is the terminate half only.
func (o *rigOutput) stopAll() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	for raw, su := range o.sacnLive {
		if o.stream != nil {
			// Three zero frames then three Stream_Terminated packets
			// (sacn.Sender.Stop). Without this a receiver would hold the
			// zeroed stream until E131_NETWORK_DATA_LOSS_TIMEOUT -- 2.5
			// seconds, Section 6.7.1 -- before declaring the source gone.
			if err := o.stream.Stop(su); err != nil {
				o.sendErr = err
			}
		}
		delete(o.sacnLive, raw)
	}
	if o.stream != nil {
		if err := o.stream.Close(); err != nil {
			o.sendErr = err
		}
		o.stream = nil
	}
	for raw := range o.artnetLive {
		if pa, err := artnet.PortAddressFromRaw(raw); err == nil {
			o.dmx.StopUniverse(pa)
		}
	}
	o.artnetLive = map[uint16]bool{}
	o.frames = map[uint16][]byte{}
	o.burst = map[uint16]int{}
	o.lastSent = map[uint16]time.Time{}
}

// liveProtocolFor reports which protocol a raw Port-Address is currently
// being driven on, and whether it is driven at all. It exists for the
// isolation test: "never both at once" has to be assertable.
func (o *rigOutput) liveProtocolFor(raw uint16) (Protocol, bool) {
	_, sacn := o.sacnLive[raw]
	artnetOn := o.artnetLive[raw]
	switch {
	case sacn && artnetOn:
		// Structurally impossible -- startUniverse writes exactly one of the
		// two maps -- but reported rather than hidden if it ever happens.
		return "", true
	case sacn:
		return ProtocolSACN, true
	case artnetOn:
		return ProtocolArtNet, true
	}
	return "", false
}
