package session

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"benny512/internal/artnet"
)

// DMX output defaults. 40 Hz sits just under DMX512-A's ~44 Hz ceiling
// (protocol reference §3.4) and is the rate most consoles ship with.
const (
	DefaultDMXRate  = 40.0
	DMXUniverseSize = 512
)

// Errors returned by DMXOutputEngine.
var (
	ErrUniverseNotStarted = errors.New("session: universe not started")
	ErrFrameTooLong       = errors.New("session: DMX frame longer than 512 slots")
	ErrChannelOutOfRange  = errors.New("session: DMX channel out of range (1-512)")
)

// DMXConfig configures a DMXOutputEngine.
type DMXConfig struct {
	Transport Transport
	Clock     Clock
	// Rate in Hz; defaults to 40. Ignored if Interval is set.
	Rate float64
	// Interval overrides Rate when non-zero.
	Interval time.Duration
	// ProtocolVersion defaults to 14.
	ProtocolVersion uint16
	// Physical is ArtDmx's informational physical-port byte.
	Physical byte
	// DisableSequencing transmits Sequence = 0 on every frame, which tells
	// receivers "this source does not sequence" (protocol reference §3.4).
	// Default (false) sequences 1..255 with wrap, per universe.
	DisableSequencing bool
}

type universe struct {
	addr     artnet.PortAddress
	dst      netip.AddrPort // zero value ⇒ broadcast
	data     [DMXUniverseSize]byte
	slots    int
	sequence byte
	frames   uint64
}

// DMXOutputEngine owns one 512-slot frame buffer per started universe and
// retransmits every buffer on each tick — including unchanged ones, since
// Art-Net receivers time out a source that goes quiet for ~0.8-1 s
// (protocol reference §3.4). Sequence numbers are per universe.
type DMXOutputEngine struct {
	cfg      DMXConfig
	interval time.Duration

	mu        sync.Mutex
	universes map[uint16]*universe
	running   bool
	timer     Timer
	ticks     uint64
	sendErr   error
}

// NewDMXOutputEngine builds an engine. Transport and Clock are required.
func NewDMXOutputEngine(cfg DMXConfig) *DMXOutputEngine {
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = artnet.DefaultProtocolVersion
	}
	interval := cfg.Interval
	if interval <= 0 {
		rate := cfg.Rate
		if rate <= 0 {
			rate = DefaultDMXRate
		}
		interval = time.Duration(float64(time.Second) / rate)
	}
	return &DMXOutputEngine{
		cfg:       cfg,
		interval:  interval,
		universes: make(map[uint16]*universe),
	}
}

// Interval reports the effective tick period.
func (e *DMXOutputEngine) Interval() time.Duration { return e.interval }

// Clock returns the Clock this engine was built with. Added for the
// function-aware Rig Check test-pattern engine (internal/patch/
// testpattern.go): a time-varying pattern (sine fade, ballyhoo, wheel
// stepping, a spin) needs its own tick source, and reusing this exact
// Clock — rather than constructing a second RealClock — is what makes a
// test's single FakeClock deterministically drive BOTH the DMX engine's own
// retransmit ticks and the pattern engine's value recomputation in lockstep
// (see patch.NewRigCheck's doc comment). Never nil: NewDMXOutputEngine
// always defaults cfg.Clock to RealClock{} before storing it.
func (e *DMXOutputEngine) Clock() Clock { return e.cfg.Clock }

// TickCount reports how many ticks have fired.
func (e *DMXOutputEngine) TickCount() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ticks
}

// LastSendError returns the most recent transmit error, if any.
func (e *DMXOutputEngine) LastSendError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sendErr
}

// Start begins the tick cycle. The first frame goes out on the first tick,
// one interval from now, not immediately — so a caller can Start then load
// frames without emitting a stale all-zero frame.
func (e *DMXOutputEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return
	}
	e.running = true
	e.scheduleLocked()
}

// Stop halts transmission. Frame buffers and sequence counters are retained,
// so a Stop/Start pair resumes rather than resets.
func (e *DMXOutputEngine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running = false
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
}

// Blackout zeroes every currently started universe's buffer and pushes it
// immediately (outside the tick cycle), without changing which universes
// are started or the engine's running state — the same "zero and push now,
// don't wait for the next tick" discipline patch.RigCheck's own Stop/
// Blackout already apply to its own bookkeeping (internal/patch/
// rigcheck.go), generalized here so a caller that needs to guarantee
// nothing this engine drives is left lit (e.g. the full-reset flow, task
// ask: "never leave the rig lit") can zero everything, not just whatever
// one caller's own started-set happens to know about — RigCheck.Stop only
// zeroes the universes IT started; a universe lit via a direct /api/dmx
// send outside any rig check would otherwise survive a RigCheck.Stop
// untouched.
func (e *DMXOutputEngine) Blackout() {
	e.mu.Lock()
	defer e.mu.Unlock()
	zero := [DMXUniverseSize]byte{}
	for _, u := range e.universes {
		u.data = zero
	}
	e.transmitAllLocked()
}

func (e *DMXOutputEngine) scheduleLocked() {
	if !e.running {
		return
	}
	e.timer = e.cfg.Clock.AfterFunc(e.interval, e.tick)
}

func (e *DMXOutputEngine) tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}
	e.ticks++
	e.transmitAllLocked()
	e.scheduleLocked()
}

// StartUniverse begins transmitting Port-Address addr to dst. A zero dst
// means broadcast. slots is the number of DMX slots to transmit; <= 0 means
// the full 512. Art-Net requires an even slot count (protocol reference
// §3.4), so odd values are rounded up. Calling StartUniverse again for an
// already-started universe re-targets it without disturbing the frame buffer
// or the sequence counter.
func (e *DMXOutputEngine) StartUniverse(addr artnet.PortAddress, dst netip.AddrPort, slots int) {
	if slots <= 0 || slots > DMXUniverseSize {
		slots = DMXUniverseSize
	}
	if slots%2 != 0 {
		slots++
	}
	if slots < 2 {
		slots = 2
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	key := addr.RawValue()
	if u, ok := e.universes[key]; ok {
		u.dst = dst
		u.slots = slots
		return
	}
	e.universes[key] = &universe{addr: addr, dst: dst, slots: slots}
}

// StopUniverse removes a universe and its frame buffer.
func (e *DMXOutputEngine) StopUniverse(addr artnet.PortAddress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.universes, addr.RawValue())
}

// Universes lists the started Port-Addresses, ordered by raw value.
func (e *DMXOutputEngine) Universes() []artnet.PortAddress {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]artnet.PortAddress, 0, len(e.universes))
	for _, u := range e.universes {
		out = append(out, u.addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RawValue() < out[j].RawValue() })
	return out
}

// SetFrame replaces a universe's buffer starting at slot 1. Slots beyond
// len(data) are zeroed — this is a whole-frame set, not a merge.
func (e *DMXOutputEngine) SetFrame(addr artnet.PortAddress, data []byte) error {
	if len(data) > DMXUniverseSize {
		return fmt.Errorf("%w: %d", ErrFrameTooLong, len(data))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	u, ok := e.universes[addr.RawValue()]
	if !ok {
		return fmt.Errorf("%w: %v", ErrUniverseNotStarted, addr)
	}
	u.data = [DMXUniverseSize]byte{}
	copy(u.data[:], data)
	return nil
}

// SetChannels writes values into a universe starting at the 1-based DMX
// address start, leaving every other slot untouched.
func (e *DMXOutputEngine) SetChannels(addr artnet.PortAddress, start int, values []byte) error {
	if start < 1 || start > DMXUniverseSize {
		return fmt.Errorf("%w: start=%d", ErrChannelOutOfRange, start)
	}
	if start-1+len(values) > DMXUniverseSize {
		return fmt.Errorf("%w: start=%d len=%d", ErrChannelOutOfRange, start, len(values))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	u, ok := e.universes[addr.RawValue()]
	if !ok {
		return fmt.Errorf("%w: %v", ErrUniverseNotStarted, addr)
	}
	copy(u.data[start-1:], values)
	return nil
}

// Frame returns a copy of a universe's current 512-slot buffer.
func (e *DMXOutputEngine) Frame(addr artnet.PortAddress) ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	u, ok := e.universes[addr.RawValue()]
	if !ok {
		return nil, false
	}
	out := make([]byte, DMXUniverseSize)
	copy(out, u.data[:])
	return out, true
}

// FrameCount reports how many ArtDmx packets have been sent for a universe.
func (e *DMXOutputEngine) FrameCount(addr artnet.PortAddress) (uint64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	u, ok := e.universes[addr.RawValue()]
	if !ok {
		return 0, false
	}
	return u.frames, true
}

// SendNow transmits every started universe immediately, outside the tick
// cycle (used for an instant-feedback "blackout" or "send" button).
func (e *DMXOutputEngine) SendNow() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transmitAllLocked()
}

func (e *DMXOutputEngine) transmitAllLocked() {
	keys := make([]uint16, 0, len(e.universes))
	for k := range e.universes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		e.transmitLocked(e.universes[k])
	}
}

func (e *DMXOutputEngine) transmitLocked(u *universe) {
	seq := byte(0)
	if !e.cfg.DisableSequencing {
		// 1..255 with wrap; 0 is reserved for "sequencing disabled".
		if u.sequence == 0 || u.sequence == 255 {
			u.sequence = 1
		} else {
			u.sequence++
		}
		seq = u.sequence
	}

	pkt := artnet.Packet{Kind: artnet.KindDmx, Dmx: artnet.Dmx{
		ProtocolVersion: e.cfg.ProtocolVersion,
		Sequence:        seq,
		Physical:        e.cfg.Physical,
		SubUni:          u.addr.SubUni(),
		Net:             u.addr.Net,
		Data:            u.data[:u.slots],
	}}
	data := artnet.Encode(pkt)

	var err error
	if u.dst.IsValid() && u.dst.Addr().IsValid() && !u.dst.Addr().IsUnspecified() {
		err = e.cfg.Transport.Send(data, u.dst)
	} else {
		err = e.cfg.Transport.Broadcast(data)
	}
	if err != nil {
		e.sendErr = err
		return
	}
	u.frames++
}
