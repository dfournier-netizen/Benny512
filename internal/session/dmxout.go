package session

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/sacn"
)

// DMXOutputEngine is Benny512's ONE output engine (Console-lite chunk C3,
// owner decisions 2026-10-06/07). Every feature that produces DMX — Rig
// Check's tests and channel walk, the Send screen and POST /api/dmx,
// Universe Identify, and the coming Console-lite programmer — writes into
// this engine as a SOURCE; nothing else in the program transmits ArtDmx or
// E1.31 data. What reaches the wire is decided here and only here:
//
//   - COMPOSITION. Sources hold per-channel ownership on SHOW universes (the
//     raw Art-Net Port-Address every stored universe already is). The
//     transmitted frame starts all-zero and each source, in fixed priority
//     order low→high — tests, programmer, raw — overwrites the channels it
//     owns. A higher source wins on every channel it owns and nowhere else.
//     Universe Identify sits above all of them and works on WIRE streams,
//     not show universes: while it runs it owns its whole stream exclusively.
//   - ROUTING. Each show universe goes out on Art-Net, sACN, or both
//     (SetRouting; unlisted universes are Art-Net, which is what every
//     universe was before sACN came back). Art-Net is broadcast on the
//     Art-Net transport, bound to the Art-Net adapter at startup; sACN goes
//     out on its own socket, opened on the sACN adapter (SACNBinding).
//   - THE MASTER ARM. Nothing above reaches the wire unless the engine is
//     armed. While disarmed the engine transmits no DMX data at all — no
//     ArtDmx, no E1.31 data — and sources keep composing silently, so every
//     control stays live as selected and Arm is the confirm step. ArtPoll,
//     RDM and node configuration are not DMX output and do not pass through
//     here; they are untouched by the Arm.
//   - THE LEASE. The Arm is held alive by heartbeats from ANY connected
//     browser (Heartbeat, keyed by a per-page client id). It is lost only
//     when every browser has been silent for the lease (5 s), or when the
//     last browser says goodbye on unload. What happens then is the
//     operator's Settings choice: Blackout (= Disarm, the default) or Hold
//     last look (StateHolding: the frames on the wire at that instant keep
//     being retransmitted, unchanged, and no live edit reaches the wire
//     until someone presses Arm again).
//
// WIRE RULES (each one justified where it is implemented):
//   - Art-Net: every live stream is retransmitted on every tick (40 Hz) and
//     immediately on a source's Push, exactly as the old engine did.
//   - sACN: ANSI E1.31 Section 6.6.2 transmission suppression — three packets
//     after any change, then a keep-alive every 850 ms.
//   - Disarm (operator, Stop all output, or lease loss with Blackout): every
//     live stream gets BlackoutFrameCount (3) all-zero frames immediately;
//     each sACN stream then gets three Stream_Terminated packets (Section
//     6.2.6); then nothing further. See disarmLocked.
//   - A stream that stops being needed while armed (its last source let go,
//     a protocol switched off, Identify ended) is retired: Art-Net gets three
//     zero frames if its last frame on the wire was not already all zero;
//     sACN gets the full three-zero, three-terminate sequence. See retireLocked.

// DMX output defaults. 40 Hz sits just under DMX512-A's ~44 Hz ceiling
// (protocol reference §3.4) and is the rate most consoles ship with.
const (
	DefaultDMXRate  = 40.0
	DMXUniverseSize = 512
)

// DefaultLease is the master Arm's lease: the Arm is lost once every
// connected browser has been silent this long (owner decision: 5 s).
const DefaultLease = 5 * time.Second

// BlackoutFrameCount is how many all-zero frames each live stream receives
// when the engine disarms or retires a lit stream. Three, for the reason
// internal/sacn.StopZeroFrameCount gives: a zero frame is unacknowledged UDP,
// and ANSI E1.31 Section 6.6.2 uses three packets as its own redundancy for
// an unacknowledged state change, so one dropped datagram still leaves two.
// They go out back to back in the Disarm call itself — bounded (at most six
// datagrams per sACN universe, three per Art-Net universe), never deferred to
// a later tick a re-arm could race, and the same shape as the hardware-
// verified sACN stop sequence Rig Check used before C3.
const BlackoutFrameCount = 3

// sACN cadence, ANSI E1.31 Section 6.6.2 (quoted in internal/patch's pre-C3
// rigcheckout.go and unchanged here): after a change, three packets of the
// new data; thereafter one keep-alive every 800-1000 ms. 850 ms sits inside
// that window with margin at both ends and well under the 2.5 s
// E131_NETWORK_DATA_LOSS_TIMEOUT (Section 6.7.1, Appendix A). The burst is
// paced at the engine tick (25 ms) rather than sent back to back, because
// Section 6.6.1 forbids exceeding the E1.11 maximum refresh rate unless the
// user configures it.
const (
	SACNSuppressionBurst  = 3
	SACNKeepAliveInterval = 850 * time.Millisecond
	// SACNTerminationCount: Section 6.2.6 — "Three packets containing this
	// bit set to 1 shall be sent by sources upon terminating sourcing of a
	// universe."
	SACNTerminationCount = 3
)

// sacnOpenRetry bounds how often a failed sACN socket open is retried while
// armed, so an adapter that has gone away does not cost an open per tick.
const sacnOpenRetry = time.Second

// Errors returned by DMXOutputEngine.
var (
	ErrFrameTooLong       = errors.New("session: DMX frame longer than 512 slots")
	ErrChannelOutOfRange  = errors.New("session: DMX channel out of range (1-512)")
	ErrUniverseOutOfRange = errors.New("session: universe is not an Art-Net Port-Address (0-32767)")
)

// Source is one producer of DMX inside the engine, in priority order: a
// higher Source wins on every channel it owns.
type Source int

// The layered sources, lowest priority first. Universe Identify is not one
// of these: it owns whole wire streams exclusively (SetIdentify).
const (
	// SourceTests is Rig Check: the Function check's test patterns and the
	// classic channel walk. It claims every slot of every universe in its
	// scope, which keeps its output identical to the pre-C3 whole-frame
	// writes when it is the only source.
	SourceTests Source = iota
	// SourceProgrammer is the Console-lite programmer (chunk C4). It claims
	// exactly the channels it has set.
	SourceProgrammer
	// SourceRaw is the Send screen and POST /api/dmx: a full 512-slot frame,
	// zeros included, so it claims the whole universe it sends.
	SourceRaw
	numSources
)

func (s Source) String() string {
	switch s {
	case SourceTests:
		return "tests"
	case SourceProgrammer:
		return "programmer"
	case SourceRaw:
		return "raw"
	}
	return fmt.Sprintf("source(%d)", int(s))
}

func (s Source) valid() bool { return s >= 0 && s < numSources }

// OutputProtocols is the set of wire protocols a show universe goes out on.
type OutputProtocols uint8

const (
	OutputArtNet OutputProtocols = 1 << iota
	OutputSACN
	OutputBoth = OutputArtNet | OutputSACN
)

// WireProtocol names one wire protocol of a Stream.
type WireProtocol uint8

const (
	WireArtNet WireProtocol = 1
	WireSACN   WireProtocol = 2
)

func (p WireProtocol) String() string {
	if p == WireSACN {
		return "sacn"
	}
	return "artnet"
}

// Stream is one universe on one wire protocol: an Art-Net Port-Address, or an
// sACN universe number (1-63999).
type Stream struct {
	Protocol WireProtocol
	Universe uint16
}

// ArmState is the master output state.
type ArmState string

const (
	StateDisarmed ArmState = "disarmed"
	StateArmed    ArmState = "armed"
	StateHolding  ArmState = "holding"
)

// LeaseLossAction is what the engine does when the Arm lease is lost.
type LeaseLossAction string

const (
	LeaseLossBlackout LeaseLossAction = "blackout"
	LeaseLossHold     LeaseLossAction = "hold"
)

// SACNParams are an open sACN socket's source identity and destination
// rules, frozen for the life of that socket.
type SACNParams struct {
	CID      [16]byte
	Priority byte
	// UnicastTo, when valid, replaces the ANSI E1.31 Table 9-10 multicast
	// group for every universe.
	UnicastTo netip.Addr
	// Port is the UDP destination port; 0 means sacn.ACNSDTMulticastPort.
	Port uint16
	// Adapter is a human-readable description of the network adapter the
	// socket is bound to, for status display only.
	Adapter string
}

// SACNLink is one open sACN socket, bound to the sACN adapter.
type SACNLink interface {
	Send(data []byte, dst netip.AddrPort) error
	Close() error
}

// SACNBinding is everything the engine needs to source sACN, supplied by the
// layer that owns the settings (internal/web).
type SACNBinding struct {
	// Open opens the sACN socket on the configured adapter. It is called
	// lazily, the first time an armed engine has an sACN packet to send, and
	// the link is closed when the engine disarms — so an adapter or CID
	// change takes effect at the next Arm.
	Open func() (SACNLink, SACNParams, error)
	// Universe maps a show universe (raw Art-Net Port-Address) to its sACN
	// universe, or refuses with an error naming why. Never clamps.
	Universe func(raw uint16) (uint16, error)
}

// DMXConfig configures a DMXOutputEngine.
type DMXConfig struct {
	// Transport carries Art-Net. Production binds it to the Art-Net adapter.
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
	// DisableSequencing transmits Sequence = 0 on every ArtDmx frame, which
	// tells receivers "this source does not sequence" (protocol reference
	// §3.4). Default (false) sequences 1..255 with wrap, per universe.
	DisableSequencing bool
	// SACN wires sACN output. Without it, a universe routed to sACN reports
	// an output error and goes out on nothing but Art-Net (if also routed).
	SACN SACNBinding
	// Lease is the Arm lease; zero means DefaultLease. Negative disables
	// lease expiry (unit tests of features other than the lease only).
	Lease time.Duration
	// Simulated marks an engine whose transports are fakes (--demo and the
	// offline rehearsal). It changes nothing on the wire path; it is
	// reported so the UI can label the output as simulated.
	Simulated bool
}

type layer struct {
	values [DMXUniverseSize]byte
	owned  [DMXUniverseSize]bool
}

type showUniverse struct {
	layers [numSources]*layer
}

func (u *showUniverse) empty() bool {
	for _, l := range u.layers {
		if l != nil {
			return false
		}
	}
	return true
}

func (u *showUniverse) compose() [DMXUniverseSize]byte {
	var out [DMXUniverseSize]byte
	for _, l := range u.layers {
		if l == nil {
			continue
		}
		for i := range out {
			if l.owned[i] {
				out[i] = l.values[i]
			}
		}
	}
	return out
}

// wireStream is the transmit state of one Stream while it is on the wire.
type wireStream struct {
	key      Stream
	show     int // originating show universe, or -1 for Identify / held frames
	seq      byte
	sent     bool
	last     [DMXUniverseSize]byte
	burst    int
	lastSent time.Time
}

type desiredStream struct {
	frame [DMXUniverseSize]byte
	show  int
}

// DMXOutputEngine — see the file comment.
type DMXOutputEngine struct {
	cfg      DMXConfig
	interval time.Duration
	lease    time.Duration

	mu       sync.Mutex
	shows    map[uint16]*showUniverse
	identify map[Stream][DMXUniverseSize]byte
	routing  map[uint16]OutputProtocols
	action   LeaseLossAction

	state      ArmState
	lastDisarm string
	clients    map[string]time.Time
	hold       map[Stream][DMXUniverseSize]byte
	active     map[Stream]*wireStream
	timer      Timer
	ticks      uint64

	link        SACNLink
	linkParams  SACNParams
	linkFailAt  time.Time
	sendErr     error
	errText     string
	unmapped    map[uint16]bool
	lastAdapter string
}

// NewDMXOutputEngine builds a disarmed engine. Transport is required.
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
	lease := cfg.Lease
	if lease == 0 {
		lease = DefaultLease
	}
	return &DMXOutputEngine{
		cfg: cfg, interval: interval, lease: lease,
		shows: map[uint16]*showUniverse{}, identify: map[Stream][DMXUniverseSize]byte{},
		routing: map[uint16]OutputProtocols{}, action: LeaseLossBlackout,
		state: StateDisarmed, clients: map[string]time.Time{}, active: map[Stream]*wireStream{},
		unmapped: map[uint16]bool{},
	}
}

// Interval reports the effective tick period.
func (e *DMXOutputEngine) Interval() time.Duration { return e.interval }

// Clock returns the Clock this engine was built with. Rig Check's pattern
// engine ticks on this same Clock, so a test's single FakeClock drives both
// in lockstep. Never nil.
func (e *DMXOutputEngine) Clock() Clock { return e.cfg.Clock }

// Simulated reports whether this engine's transports are fakes.
func (e *DMXOutputEngine) Simulated() bool { return e.cfg.Simulated }

// SetSACNBinding wires (or rewires) sACN. The new binding is used from the
// next socket open, i.e. the next Arm.
func (e *DMXOutputEngine) SetSACNBinding(b SACNBinding) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.SACN = b
}

// SetRouting replaces the per-universe protocol table. A universe absent from
// m goes out on Art-Net only. While armed the change takes effect at once:
// a stream that is no longer wanted is retired properly (sACN is terminated),
// and a newly wanted one starts.
func (e *DMXOutputEngine) SetRouting(m map[uint16]OutputProtocols) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routing = make(map[uint16]OutputProtocols, len(m))
	for k, v := range m {
		if v&OutputBoth != 0 {
			e.routing[k] = v & OutputBoth
		}
	}
	e.passLocked(passMode{})
}

// SetLeaseLossAction sets what losing the lease does. Anything other than
// LeaseLossHold is Blackout.
func (e *DMXOutputEngine) SetLeaseLossAction(a LeaseLossAction) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if a != LeaseLossHold {
		a = LeaseLossBlackout
	}
	e.action = a
}

// --- sources ----------------------------------------------------------------

func (e *DMXOutputEngine) layerLocked(src Source, raw uint16) *layer {
	u := e.shows[raw]
	if u == nil {
		u = &showUniverse{}
		e.shows[raw] = u
	}
	if u.layers[src] == nil {
		u.layers[src] = &layer{}
	}
	return u.layers[src]
}

func checkSource(src Source, raw uint16) error {
	if !src.valid() {
		return fmt.Errorf("session: unknown output source %d", int(src))
	}
	if raw > 0x7FFF {
		return fmt.Errorf("%w: %d", ErrUniverseOutOfRange, raw)
	}
	return nil
}

// SetFrame replaces src's contribution to show universe raw with a whole
// frame: src claims all 512 slots, and slots beyond len(data) are zero. It
// does not transmit; call Push for an immediate send, or let the tick do it.
func (e *DMXOutputEngine) SetFrame(src Source, raw uint16, data []byte) error {
	if err := checkSource(src, raw); err != nil {
		return err
	}
	if len(data) > DMXUniverseSize {
		return fmt.Errorf("%w: %d", ErrFrameTooLong, len(data))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	l := e.layerLocked(src, raw)
	l.values = [DMXUniverseSize]byte{}
	copy(l.values[:], data)
	for i := range l.owned {
		l.owned[i] = true
	}
	return nil
}

// SetChannels sets values for src starting at the 1-based address start and
// adds exactly those channels to src's claim on raw, leaving every other
// channel's value and claim untouched.
func (e *DMXOutputEngine) SetChannels(src Source, raw uint16, start int, values []byte) error {
	if err := checkSource(src, raw); err != nil {
		return err
	}
	if start < 1 || start > DMXUniverseSize || start-1+len(values) > DMXUniverseSize {
		return fmt.Errorf("%w: start=%d len=%d", ErrChannelOutOfRange, start, len(values))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	l := e.layerLocked(src, raw)
	for i, v := range values {
		l.values[start-1+i] = v
		l.owned[start-1+i] = true
	}
	return nil
}

// ReleaseChannels removes count channels from src's claim on raw, starting
// at the 1-based address start, and transmits the result at once.
func (e *DMXOutputEngine) ReleaseChannels(src Source, raw uint16, start, count int) error {
	if err := checkSource(src, raw); err != nil {
		return err
	}
	if start < 1 || count < 0 || start-1+count > DMXUniverseSize {
		return fmt.Errorf("%w: start=%d count=%d", ErrChannelOutOfRange, start, count)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	u := e.shows[raw]
	if u == nil || u.layers[src] == nil {
		return nil
	}
	l := u.layers[src]
	claimed := false
	for i := start - 1; i < start-1+count; i++ {
		l.owned[i] = false
		l.values[i] = 0
	}
	for _, o := range l.owned {
		claimed = claimed || o
	}
	if !claimed {
		u.layers[src] = nil
		if u.empty() {
			delete(e.shows, raw)
		}
	}
	e.passLocked(passMode{shows: map[uint16]bool{raw: true}})
	return nil
}

// Release removes src's whole claim on each listed universe and transmits
// the result at once: universes other sources still own fall back to them,
// and a universe nobody owns any more leaves the wire (see retireLocked).
func (e *DMXOutputEngine) Release(src Source, raws ...uint16) {
	if !src.valid() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	scope := map[uint16]bool{}
	for _, raw := range raws {
		u := e.shows[raw]
		if u == nil || u.layers[src] == nil {
			continue
		}
		u.layers[src] = nil
		if u.empty() {
			delete(e.shows, raw)
		}
		scope[raw] = true
	}
	if len(scope) > 0 {
		e.passLocked(passMode{shows: scope})
	}
}

// ReleaseAll removes every claim src holds.
func (e *DMXOutputEngine) ReleaseAll(src Source) {
	e.mu.Lock()
	raws := make([]uint16, 0, len(e.shows))
	for raw, u := range e.shows {
		if src.valid() && u.layers[src] != nil {
			raws = append(raws, raw)
		}
	}
	e.mu.Unlock()
	e.Release(src, raws...)
}

// Push transmits the listed show universes' streams now, outside the tick —
// the "zero and push now, don't wait for the next tick" discipline Rig Check
// has always applied to every change it makes. force restarts the sACN
// three-packet burst even when the frame did not change, so a blackout puts a
// packet on the wire even if the frame it replaces was already zero. A no-op
// unless armed.
func (e *DMXOutputEngine) Push(force bool, raws ...uint16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	scope := make(map[uint16]bool, len(raws))
	for _, r := range raws {
		scope[r] = true
	}
	e.passLocked(passMode{shows: scope, force: force})
}

// SetIdentify replaces Universe Identify's whole set of streams. While a
// stream is in this set Identify owns it exclusively: its frame goes out
// as given, whatever any source holds for the show universe behind it. The
// change is transmitted at once.
func (e *DMXOutputEngine) SetIdentify(frames map[Stream][]byte) error {
	next := make(map[Stream][DMXUniverseSize]byte, len(frames))
	for k, v := range frames {
		if len(v) > DMXUniverseSize {
			return fmt.Errorf("%w: %d", ErrFrameTooLong, len(v))
		}
		if k.Protocol == WireArtNet && k.Universe > 0x7FFF {
			return fmt.Errorf("%w: %d", ErrUniverseOutOfRange, k.Universe)
		}
		if k.Protocol == WireSACN && (k.Universe < 1 || k.Universe > 63999) {
			return fmt.Errorf("%w", sacn.ErrInvalidUniverse)
		}
		if k.Protocol != WireArtNet && k.Protocol != WireSACN {
			return fmt.Errorf("session: unknown wire protocol %d", k.Protocol)
		}
		var f [DMXUniverseSize]byte
		copy(f[:], v)
		next[k] = f
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.identify = next
	e.passLocked(passMode{identify: true})
	return nil
}

// ClearIdentify ends Universe Identify: each of its streams falls back to the
// composed show universe behind it, or leaves the wire.
func (e *DMXOutputEngine) ClearIdentify() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.identify) == 0 {
		return
	}
	e.identify = map[Stream][DMXUniverseSize]byte{}
	e.passLocked(passMode{identify: true})
}

// --- the master Arm and its lease -------------------------------------------

// Arm lets DMX reach the wire. It counts as a heartbeat from client. From
// StateHolding it resumes live output at once (held streams no longer wanted
// are retired). Arm clears a previous output error so a new one is fresh.
func (e *DMXOutputEngine) Arm(client string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Clock.Now()
	e.clients[client] = now
	if e.state == StateArmed {
		return
	}
	wasDisarmed := e.state == StateDisarmed
	e.state = StateArmed
	e.hold = nil
	e.lastDisarm = ""
	e.errText = ""
	e.sendErr = nil
	e.linkFailAt = time.Time{}
	e.passLocked(passMode{tick: true})
	if wasDisarmed || e.timer == nil {
		e.scheduleLocked()
	}
}

// Disarm blacks out and stops all DMX data. It ALWAYS blacks out: every
// stream on the wire (live or held) gets the blackout sequence (see
// disarmLocked). Sources are untouched, so re-arming restores the live look.
func (e *DMXOutputEngine) Disarm() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disarmLocked("operator")
}

// Heartbeat records that client is connected. It keeps an armed engine's
// lease alive; it never re-arms a disarmed or holding engine.
func (e *DMXOutputEngine) Heartbeat(client string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Clock.Now()
	e.clients[client] = now
	e.pruneClientsLocked(now)
}

// Goodbye drops client from the lease at once (a page unload). If no other
// browser has been heard within the lease window, the lease is lost now —
// the same outcome as the last browser going silent, without waiting.
func (e *DMXOutputEngine) Goodbye(client string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.clients, client)
	if e.state == StateArmed && !e.leaseAliveLocked(e.cfg.Clock.Now()) {
		e.leaseLostLocked()
	}
}

func (e *DMXOutputEngine) leaseAliveLocked(now time.Time) bool {
	if e.lease < 0 {
		return true
	}
	for _, t := range e.clients {
		if now.Sub(t) < e.lease {
			return true
		}
	}
	return false
}

func (e *DMXOutputEngine) pruneClientsLocked(now time.Time) {
	if e.lease < 0 {
		return
	}
	for c, t := range e.clients {
		if now.Sub(t) >= e.lease {
			delete(e.clients, c)
		}
	}
}

// leaseLostLocked applies the operator's lease-loss choice.
func (e *DMXOutputEngine) leaseLostLocked() {
	if e.action != LeaseLossHold {
		e.disarmLocked("lease")
		return
	}
	// Hold last look: freeze exactly what is on the wire now. A stream that
	// has been created but not yet transmitted holds the frame it was about
	// to send.
	desired := e.desiredLocked()
	e.hold = map[Stream][DMXUniverseSize]byte{}
	for k, ws := range e.active {
		if ws.sent {
			e.hold[k] = ws.last
		} else if d, ok := desired[k]; ok {
			e.hold[k] = d.frame
		}
	}
	e.state = StateHolding
}

// disarmLocked is the one path to StateDisarmed. Wire rule: every stream on
// the wire — plus any stream the engine was about to start — gets
// BlackoutFrameCount all-zero frames now; each sACN stream then gets
// SACNTerminationCount Stream_Terminated packets (ANSI E1.31 Section 6.2.6:
// "Three packets containing this bit set to 1 shall be sent by sources upon
// terminating sourcing of a universe"), so a receiver enters network data
// loss at once instead of holding the zeroed stream for the 2.5 s
// E131_NETWORK_DATA_LOSS_TIMEOUT (Section 6.7.1). The zeros come first
// because what a receiver does on data loss (hold, fade, dark) is outside
// the standard; zero frames make the rig dark whatever it chooses. Art-Net
// has no termination message: a node that stops receiving keeps its last
// frame (or does whatever its own data-loss setting says), which is why the
// zeros matter there too. Then the sACN socket is closed and nothing more is
// sent until the next Arm.
func (e *DMXOutputEngine) disarmLocked(reason string) {
	if e.state == StateDisarmed {
		if reason == "operator" {
			e.lastDisarm = reason
		}
		return
	}
	targets := map[Stream]*wireStream{}
	if e.state == StateArmed {
		for k := range e.desiredLocked() {
			targets[k] = e.streamLocked(k, -1)
		}
	}
	for k, ws := range e.active {
		targets[k] = ws
	}
	keys := sortedStreams(targets)
	zero := [DMXUniverseSize]byte{}
	for _, k := range keys {
		ws := targets[k]
		for i := 0; i < BlackoutFrameCount; i++ {
			e.sendLocked(ws, zero, 0)
		}
		if k.Protocol == WireSACN {
			for i := 0; i < SACNTerminationCount; i++ {
				e.sendLocked(ws, zero, sacn.OptionStreamTerminated)
			}
		}
	}
	e.active = map[Stream]*wireStream{}
	e.hold = nil
	e.closeLinkLocked()
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.state = StateDisarmed
	e.lastDisarm = reason
}

// --- the tick and the transmit pass -------------------------------------------

func (e *DMXOutputEngine) scheduleLocked() {
	if e.state == StateDisarmed {
		return
	}
	e.timer = e.cfg.Clock.AfterFunc(e.interval, e.tick)
}

func (e *DMXOutputEngine) tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == StateDisarmed {
		return
	}
	e.ticks++
	if e.state == StateArmed && !e.leaseAliveLocked(e.cfg.Clock.Now()) {
		e.leaseLostLocked()
		if e.state == StateDisarmed {
			return
		}
	}
	e.passLocked(passMode{tick: true})
	e.scheduleLocked()
}

// passMode says which Art-Net streams a pass transmits. sACN streams follow
// their own Section 6.6.2 cadence on every pass.
type passMode struct {
	tick     bool            // every Art-Net stream
	shows    map[uint16]bool // Art-Net streams of these show universes
	identify bool            // Identify-owned Art-Net streams
	force    bool            // restart the sACN burst for the streams of `shows`
}

// desiredLocked is what should be on the wire right now. Armed: every show
// universe some source claims, on each protocol its routing names, with
// Identify's streams overriding exclusively. Holding: the held frames.
func (e *DMXOutputEngine) desiredLocked() map[Stream]desiredStream {
	out := map[Stream]desiredStream{}
	switch e.state {
	case StateHolding:
		for k, f := range e.hold {
			out[k] = desiredStream{frame: f, show: -1}
		}
		return out
	case StateDisarmed:
		return out
	}
	e.unmapped = map[uint16]bool{}
	for raw, u := range e.shows {
		frame := u.compose()
		protos, ok := e.routing[raw]
		if !ok {
			protos = OutputArtNet
		}
		if protos&OutputArtNet != 0 {
			out[Stream{WireArtNet, raw}] = desiredStream{frame: frame, show: int(raw)}
		}
		if protos&OutputSACN != 0 {
			if e.cfg.SACN.Universe == nil {
				e.unmapped[raw] = true
				continue
			}
			su, err := e.cfg.SACN.Universe(raw)
			if err != nil {
				e.unmapped[raw] = true
				continue
			}
			out[Stream{WireSACN, su}] = desiredStream{frame: frame, show: int(raw)}
		}
	}
	for k, f := range e.identify {
		out[k] = desiredStream{frame: f, show: -1}
	}
	return out
}

func (e *DMXOutputEngine) streamLocked(k Stream, show int) *wireStream {
	if ws, ok := e.active[k]; ok {
		return ws
	}
	ws := &wireStream{key: k, show: show}
	if k.Protocol == WireSACN {
		ws.seq = sacn.FirstSequenceNumber
	}
	return ws
}

func (e *DMXOutputEngine) passLocked(m passMode) {
	if e.state == StateDisarmed {
		return
	}
	now := e.cfg.Clock.Now()
	desired := e.desiredLocked()

	// Retire every stream that is no longer wanted, before sending anything
	// new, so a protocol switch terminates the old stream first.
	for _, k := range sortedStreams(e.active) {
		if _, ok := desired[k]; !ok {
			e.retireLocked(e.active[k])
			delete(e.active, k)
		}
	}

	pass := true
	for _, k := range sortedDesired(desired) {
		d := desired[k]
		ws := e.streamLocked(k, d.show)
		e.active[k] = ws
		// A stream whose owner changed (Identify started or ended on it)
		// goes out at once rather than waiting for the tick.
		handover := ws.show != d.show
		ws.show = d.show
		switch k.Protocol {
		case WireArtNet:
			send := m.tick || handover || (d.show >= 0 && m.shows[uint16(d.show)]) || (d.show < 0 && m.identify)
			if send && !e.sendLocked(ws, d.frame, 0) {
				pass = false
			}
		case WireSACN:
			// Section 6.6.2: transmit Null START Code data when it changes —
			// three packets — then a keep-alive every 850 ms.
			if !ws.sent || ws.last != d.frame || (m.force && d.show >= 0 && m.shows[uint16(d.show)]) {
				ws.burst = SACNSuppressionBurst
			}
			if ws.burst > 0 || now.Sub(ws.lastSent) >= SACNKeepAliveInterval {
				if e.sendLocked(ws, d.frame, 0) {
					if ws.burst > 0 {
						ws.burst--
					}
				} else {
					pass = false
				}
			}
		}
	}
	if m.tick && pass && e.errText == "" {
		e.sendErr = nil
	}
}

// retireLocked takes one stream off the wire while the engine stays armed.
// Art-Net: if the last frame on the wire was not already all zero, three
// zero frames, so a stream released by its last source never leaves a node
// holding a lit look (Rig Check pushes its own zero frame before releasing,
// so its output is unchanged by this rule). sACN: three zero frames and
// three Stream_Terminated packets, exactly the pre-C3 sacn.Sender.Stop
// sequence.
func (e *DMXOutputEngine) retireLocked(ws *wireStream) {
	if !ws.sent {
		return
	}
	zero := [DMXUniverseSize]byte{}
	if ws.key.Protocol == WireArtNet {
		if ws.last == zero {
			return
		}
		for i := 0; i < BlackoutFrameCount; i++ {
			e.sendLocked(ws, zero, 0)
		}
		return
	}
	for i := 0; i < BlackoutFrameCount; i++ {
		e.sendLocked(ws, zero, 0)
	}
	for i := 0; i < SACNTerminationCount; i++ {
		e.sendLocked(ws, zero, sacn.OptionStreamTerminated)
	}
}

// sendLocked encodes and transmits one packet on ws, reporting success.
func (e *DMXOutputEngine) sendLocked(ws *wireStream, frame [DMXUniverseSize]byte, options byte) bool {
	now := e.cfg.Clock.Now()
	var err error
	if ws.key.Protocol == WireArtNet {
		err = e.sendArtNetLocked(ws, frame)
	} else {
		err = e.sendSACNLocked(ws, frame, options)
	}
	if err != nil {
		e.sendErr = err
		return false
	}
	ws.sent = true
	ws.last = frame
	ws.lastSent = now
	return true
}

func (e *DMXOutputEngine) sendArtNetLocked(ws *wireStream, frame [DMXUniverseSize]byte) error {
	pa, err := artnet.PortAddressFromRaw(ws.key.Universe)
	if err != nil {
		return err
	}
	seq := byte(0)
	if !e.cfg.DisableSequencing {
		// 1..255 with wrap; 0 is reserved for "sequencing disabled".
		if ws.seq == 0 || ws.seq == 255 {
			ws.seq = 1
		} else {
			ws.seq++
		}
		seq = ws.seq
	}
	data := artnet.Encode(artnet.Packet{Kind: artnet.KindDmx, Dmx: artnet.Dmx{
		ProtocolVersion: e.cfg.ProtocolVersion,
		Sequence:        seq,
		Physical:        e.cfg.Physical,
		SubUni:          pa.SubUni(),
		Net:             pa.Net,
		Data:            frame[:],
	}})
	return e.cfg.Transport.Broadcast(data)
}

func (e *DMXOutputEngine) sendSACNLocked(ws *wireStream, frame [DMXUniverseSize]byte, options byte) error {
	if err := e.openLinkLocked(); err != nil {
		return err
	}
	// ANSI E1.31 Section 6.2.5: one sequence per universe, +1 per packet,
	// wrapping — byte arithmetic. Stop packets continue the sequence.
	seq := ws.seq
	pkt, err := sacn.EncodeDataPacketWithOptions(frame[:], e.linkParams.CID, sacn.SourceName, e.linkParams.Priority, seq, ws.key.Universe, options)
	if err != nil {
		return err
	}
	ws.seq++
	port := e.linkParams.Port
	if port == 0 {
		port = sacn.ACNSDTMulticastPort
	}
	var dst netip.AddrPort
	if e.linkParams.UnicastTo.IsValid() {
		dst = netip.AddrPortFrom(e.linkParams.UnicastTo, port)
	} else {
		// ANSI E1.31 Table 9-10: 239.255.<universe high>.<universe low>.
		dst = netip.AddrPortFrom(netip.AddrFrom4([4]byte{239, 255, byte(ws.key.Universe >> 8), byte(ws.key.Universe)}), port)
	}
	return e.link.Send(pkt, dst)
}

func (e *DMXOutputEngine) openLinkLocked() error {
	if e.link != nil {
		return nil
	}
	if e.cfg.SACN.Open == nil {
		e.errText = "sACN output is not available in this build configuration."
		return errors.New(e.errText)
	}
	now := e.cfg.Clock.Now()
	if !e.linkFailAt.IsZero() && now.Sub(e.linkFailAt) < sacnOpenRetry {
		return errors.New(e.errText)
	}
	link, params, err := e.cfg.SACN.Open()
	if err != nil {
		e.linkFailAt = now
		e.errText = fmt.Sprintf("The sACN network adapter could not be opened, so no sACN is being sent: %v.", err)
		return err
	}
	e.link, e.linkParams, e.linkFailAt = link, params, time.Time{}
	e.lastAdapter = params.Adapter
	e.errText = ""
	return nil
}

func (e *DMXOutputEngine) closeLinkLocked() {
	if e.link == nil {
		return
	}
	if err := e.link.Close(); err != nil {
		e.sendErr = err
	}
	e.link = nil
}

func sortedStreams(m map[Stream]*wireStream) []Stream {
	out := make([]Stream, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStreamKeys(out)
	return out
}

func sortedDesired(m map[Stream]desiredStream) []Stream {
	out := make([]Stream, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStreamKeys(out)
	return out
}

func sortStreamKeys(s []Stream) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Protocol != s[j].Protocol {
			return s[i].Protocol < s[j].Protocol
		}
		return s[i].Universe < s[j].Universe
	})
}

// --- status and reads -------------------------------------------------------

// StreamStatus is one stream on the wire.
type StreamStatus struct {
	Protocol string `json:"protocol"`
	Universe uint16 `json:"universe"`
	// Show is the originating show universe (raw Art-Net Port-Address), or
	// -1 for a stream Identify owns or a held frame.
	Show int `json:"show"`
}

// OutputStatus is a snapshot of the master output.
type OutputStatus struct {
	State           ArmState
	Simulated       bool
	LeaseLossAction LeaseLossAction
	// Browsers is how many distinct browsers were heard within the lease.
	Browsers int
	// LeaseRemaining is how long until the lease expires with no further
	// heartbeat (0 when disarmed or holding).
	LeaseRemaining time.Duration
	// Error is the current output error in words, "" when there is none.
	Error string
	// LastDisarm is why output last went to Disarmed: "operator", "lease",
	// or "" when it has not been disarmed since the last Arm.
	LastDisarm string
	Streams    []StreamStatus
	// Unmapped lists show universes routed to sACN that have no sACN
	// universe at the current start universes; they are not sent on sACN.
	Unmapped []uint16
	// SACNAdapter describes the adapter the open sACN socket is bound to
	// ("" when none has been opened since startup).
	SACNAdapter string
}

// Status reports the master output.
func (e *DMXOutputEngine) Status() OutputStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Clock.Now()
	st := OutputStatus{
		State: e.state, Simulated: e.cfg.Simulated, LeaseLossAction: e.action,
		LastDisarm: e.lastDisarm, Streams: make([]StreamStatus, 0, len(e.active)),
		Unmapped: make([]uint16, 0), SACNAdapter: e.lastAdapter,
	}
	var newest time.Time
	for _, t := range e.clients {
		if e.lease < 0 || now.Sub(t) < e.lease {
			st.Browsers++
		}
		if t.After(newest) {
			newest = t
		}
	}
	if e.state == StateArmed && e.lease > 0 && !newest.IsZero() {
		if rem := e.lease - now.Sub(newest); rem > 0 {
			st.LeaseRemaining = rem
		}
	}
	for _, k := range sortedStreams(e.active) {
		st.Streams = append(st.Streams, StreamStatus{Protocol: k.Protocol.String(), Universe: k.Universe, Show: e.active[k].show})
	}
	if e.state == StateArmed {
		e.desiredLocked() // refresh the unmapped set against current settings
		for raw := range e.unmapped {
			st.Unmapped = append(st.Unmapped, raw)
		}
		sort.Slice(st.Unmapped, func(i, j int) bool { return st.Unmapped[i] < st.Unmapped[j] })
	}
	switch {
	case e.errText != "":
		st.Error = e.errText
	case e.sendErr != nil && e.state != StateDisarmed:
		st.Error = fmt.Sprintf("A DMX packet could not be sent: %v.", e.sendErr)
	case len(st.Unmapped) > 0:
		st.Error = "A universe set to sACN has no sACN universe at the current start universes, so it is not being sent on sACN. Check the sACN starting universe on Settings."
	}
	return st
}

// State reports the master output state.
func (e *DMXOutputEngine) State() ArmState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// Frame returns the composed 512-slot frame for show universe addr — what
// would go out for it while armed, Identify aside — and whether any source
// claims it.
func (e *DMXOutputEngine) Frame(addr artnet.PortAddress) ([]byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	u, ok := e.shows[addr.RawValue()]
	if !ok {
		return nil, false
	}
	f := u.compose()
	return append([]byte(nil), f[:]...), true
}

// Universes lists the show universes any source claims, ordered by raw value.
func (e *DMXOutputEngine) Universes() []artnet.PortAddress {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]artnet.PortAddress, 0, len(e.shows))
	for raw := range e.shows {
		if pa, err := artnet.PortAddressFromRaw(raw); err == nil {
			out = append(out, pa)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RawValue() < out[j].RawValue() })
	return out
}

// OutputRunning reports whether DMX is on the wire: armed or holding, with
// at least one stream live. A read-only query, not a heartbeat.
func (e *DMXOutputEngine) OutputRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state != StateDisarmed && len(e.active) > 0
}

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
