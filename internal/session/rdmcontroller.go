package session

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// NodeRef addresses one RDM-capable port on one node: who to send the UDP
// datagram to (Addr), which node identity it belongs to (Key, used for
// serialization and per-node timeout profiles), and which Port-Address the
// RDM traffic is bound for.
type NodeRef struct {
	Key  NodeKey
	Addr netip.AddrPort
	Port artnet.PortAddress
}

// TimeoutProfile bundles the timing behaviour of one class of RDM path.
//
// The distinction is not cosmetic: a direct DMX run answers in single-digit
// milliseconds, while a LumenRadio wireless proxy routinely takes seconds and
// answers with ACK_TIMER first (architecture rev 5 §1.1). Applying direct
// timings to a proxied rig produces spurious timeouts and hammers the link
// with retries.
type TimeoutProfile struct {
	Name string
	// ResponseTimeout is how long to wait for any response to one issued
	// request before retransmitting it.
	ResponseTimeout time.Duration
	// Retries is the number of retransmissions after the first attempt.
	Retries int
	// MaxAckTimer caps a single ACK_TIMER delay. A responder asking for
	// longer is retried at the cap instead (it will simply ACK_TIMER again
	// if it is still busy), which bounds how long one command can sit idle.
	MaxAckTimer time.Duration
	// CommandDeadline is the overall budget for one command, measured from
	// its first transmission — it bounds an unbounded ACK_TIMER chain.
	CommandDeadline time.Duration
}

// ProfileDirect is for responders on a wired DMX/RDM run behind an Art-Net
// gateway (Netron EN4 direct).
var ProfileDirect = TimeoutProfile{
	Name:            "Direct",
	ResponseTimeout: 1500 * time.Millisecond,
	Retries:         2,
	MaxAckTimer:     10 * time.Second,
	CommandDeadline: 15 * time.Second,
}

// ProfileWirelessProxy is for responders reached through a wireless RDM
// proxy (LumenRadio Aurora / Moonlite2), where ACK_TIMER is the normal path.
var ProfileWirelessProxy = TimeoutProfile{
	Name:            "WirelessProxy",
	ResponseTimeout: 5 * time.Second,
	Retries:         3,
	MaxAckTimer:     10 * time.Second,
	CommandDeadline: 60 * time.Second,
}

// SerializationScope selects how aggressively the controller serializes
// outstanding RDM commands.
type SerializationScope int

// Serialization scopes. The zero value, ScopeNodePort, is the default and
// the conservative reading of the spec: an Art-Net gateway proxies a single
// physical RS-485 link per port, so two commands to two different responders
// on the same port still contend for that one wire.
const (
	// ScopeNodePort allows one in-flight command per (node, Port-Address).
	ScopeNodePort SerializationScope = iota
	// ScopeUID allows one in-flight command per responder UID, so different
	// responders proceed in parallel. Faster; only safe on gateways that
	// pipeline their own port correctly.
	ScopeUID
	// ScopeNode allows one in-flight command per node across all its ports.
	ScopeNode
)

// String renders the scope.
func (s SerializationScope) String() string {
	switch s {
	case ScopeNodePort:
		return "node-port"
	case ScopeUID:
		return "uid"
	case ScopeNode:
		return "node"
	default:
		return "unknown"
	}
}

// Errors returned in Result.Err / from the controller API.
var (
	ErrTimeout             = errors.New("session: RDM response timeout")
	ErrDeadlineExceeded    = errors.New("session: RDM command deadline exceeded")
	ErrOverflowPIDMismatch = errors.New("session: ACK_OVERFLOW aborted, responder answered with a different PID")
	ErrPIDMismatch         = errors.New("session: RDM response PID does not match request")
	ErrUnknownResponseType = errors.New("session: unknown RDM response type")
	ErrInvalidCommandClass = errors.New("session: RDM command class must be GET or SET")
	ErrControllerStopped   = errors.New("session: RDM controller stopped")
	ErrTodNak              = errors.New("session: node returned TodNak (discovery did not complete)")
	ErrTodTimeout          = errors.New("session: timed out assembling Table of Devices")
)

// NackError wraps a NACK_REASON response as a typed Go error.
type NackError struct {
	Reason rdm.NackReason
}

func (e *NackError) Error() string {
	return fmt.Sprintf("session: RDM NACK (%s)", NackReasonName(e.Reason))
}

// NackReasonName renders the well-known NACK reason codes; unknown codes
// (E1.37/E1.33 additions and manufacturer space) render as hex.
func NackReasonName(r rdm.NackReason) string {
	switch r {
	case rdm.NackUnknownPID:
		return "UNKNOWN_PID"
	case rdm.NackFormatError:
		return "FORMAT_ERROR"
	case rdm.NackHardwareFault:
		return "HARDWARE_FAULT"
	case rdm.NackProxyReject:
		return "PROXY_REJECT"
	case rdm.NackWriteProtect:
		return "WRITE_PROTECT"
	case rdm.NackUnsupportedCommandClass:
		return "UNSUPPORTED_COMMAND_CLASS"
	case rdm.NackDataOutOfRange:
		return "DATA_OUT_OF_RANGE"
	case rdm.NackBufferFull:
		return "BUFFER_FULL"
	case rdm.NackPacketSizeUnsupported:
		return "PACKET_SIZE_UNSUPPORTED"
	case rdm.NackSubDeviceOutOfRange:
		return "SUB_DEVICE_OUT_OF_RANGE"
	case rdm.NackProxyBufferFull:
		return "PROXY_BUFFER_FULL"
	default:
		return fmt.Sprintf("NACK_0x%04X", uint16(r))
	}
}

// Request is one RDM GET or SET aimed at one responder through one node port.
type Request struct {
	Node NodeRef
	UID  rdm.UID
	// CommandClass must be rdm.GetCommand or rdm.SetCommand.
	CommandClass rdm.CommandClass
	PID          rdm.ParameterID
	SubDevice    uint16
	Data         []byte
	// Profile overrides the per-node and controller-default timeout profile
	// for this one command.
	Profile *TimeoutProfile
}

// ResultKind is the outcome class of a completed command.
type ResultKind int

// Result kinds.
const (
	// ResultAck: the responder answered ACK (possibly after ACK_TIMERs
	// and/or an ACK_OVERFLOW sequence, both of which are transparent).
	ResultAck ResultKind = iota
	// ResultNack: the responder answered NACK_REASON.
	ResultNack
	// ResultTimeout: no response, retries exhausted.
	ResultTimeout
	// ResultDeadlineExceeded: the overall per-command budget ran out,
	// typically an ACK_TIMER chain that never converged.
	ResultDeadlineExceeded
	// ResultAborted: the exchange was abandoned mid-flight (protocol
	// violation by the responder, or controller shutdown).
	ResultAborted
	// ResultBroadcast: the request targeted a broadcast UID, which by spec
	// is never acknowledged; the command completes as soon as it is sent.
	ResultBroadcast
)

// String renders the result kind.
func (k ResultKind) String() string {
	switch k {
	case ResultAck:
		return "ack"
	case ResultNack:
		return "nack"
	case ResultTimeout:
		return "timeout"
	case ResultDeadlineExceeded:
		return "deadline-exceeded"
	case ResultAborted:
		return "aborted"
	case ResultBroadcast:
		return "broadcast"
	default:
		return "unknown"
	}
}

// Result is the typed outcome of one RDM command.
type Result struct {
	Kind    ResultKind
	Request Request
	// Data is the response Parameter Data, with every ACK_OVERFLOW block
	// concatenated in receive order.
	Data       []byte
	NackReason rdm.NackReason
	// MessageCount is the responder's queued-message count from the final
	// response — non-zero means it has status messages waiting for a
	// QUEUED_MESSAGE GET.
	MessageCount byte
	// Blocks counts response packets that contributed Parameter Data
	// (1 for a plain ACK, N for an N-packet ACK_OVERFLOW sequence).
	Blocks int
	// AckTimers counts ACK_TIMER responses received for this command.
	AckTimers int
	// Retransmissions counts response-timeout retries across all issuances.
	Retransmissions int
	Elapsed         time.Duration
	// Response is the final RDM response message, when there was one.
	Response *rdm.Message
	Err      error
}

// Command is the handle returned by Get/Set/Submit.
type Command struct {
	id   uint64
	req  Request
	done chan Result

	// --- controller-owned state, guarded by RDMController.mu ---
	profile      TimeoutProfile
	qkey         queueKey
	tn           byte
	attempt      int
	ackTimers    int
	blocks       [][]byte
	blockCount   int
	overflow     bool
	retransmits  int
	startedAt    time.Time
	deadlineAt   time.Time
	issued       bool
	wireBytes    []byte
	timer        Timer
	deadline     Timer
	gen          uint64
	finished     bool
	finalResult  Result
	messageCount byte
}

// Done delivers the Result exactly once, then closes.
func (c *Command) Done() <-chan Result { return c.done }

// Await blocks until the command completes or ctx is cancelled.
func (c *Command) Await(ctx context.Context) (Result, error) {
	select {
	case r := <-c.done:
		return r, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// Request returns the originating request.
func (c *Command) Request() Request { return c.req }

type queueKey struct {
	ip   netip.Addr
	bind byte
	port uint16
	uid  rdm.UID
}

type cmdQueue struct {
	inflight *Command
	pending  []*Command
}

// RDMConfig configures an RDMController.
type RDMConfig struct {
	Transport Transport
	Clock     Clock
	// ControllerUID is this controller's own RDM UID (source UID of every
	// request). Defaults to 7FF0:00000001 — 0x7FF0 is ESTA's prototyping
	// manufacturer range, correct for a tool that has no assigned ID.
	ControllerUID rdm.UID
	// PortID is slot 16 in requests. RDM numbers controller ports from 1;
	// 0 is not a legal Port ID, so 0 here defaults to 1.
	PortID          byte
	ProtocolVersion uint16
	// DefaultProfile is used for nodes with no specific profile set.
	// Defaults to ProfileDirect.
	DefaultProfile TimeoutProfile
	// Scope defaults to ScopeNodePort.
	Scope SerializationScope
	// AckTimerUnit is the time unit of ACK_TIMER's 2-byte Parameter Data.
	//
	// SPEC AMBIGUITY (flagged for architect review): ANSI E1.20 §6.3.3
	// specifies 10 ms increments, but the project's protocol reference
	// (§2.4) describes the field as a plain millisecond count. The
	// difference is a 10× error in retry pacing on exactly the rig where it
	// matters most (the wireless proxies). It is configurable for that
	// reason; the default follows the standard (10 ms) and must be
	// confirmed against real hardware in Phase 1d.
	AckTimerUnit time.Duration
	// DiscoveryTimeout bounds ToD assembly; the timer restarts on every
	// ArtTodData block received, so it is a quiet-period timeout rather
	// than a hard cap on a long discovery. Defaults to 20 s.
	DiscoveryTimeout time.Duration
	// EventBuffer defaults to 64.
	EventBuffer int
}

// DefaultAckTimerUnit is ACK_TIMER's parameter-data unit per ANSI E1.20.
const DefaultAckTimerUnit = 10 * time.Millisecond

// DefaultDiscoveryTimeout bounds ArtTodData assembly.
const DefaultDiscoveryTimeout = 20 * time.Second

// EventKind classifies an RDM controller Event.
type EventKind int

// RDM event kinds.
const (
	// EventCommandSent fires on each transmission (including retries and
	// ACK_TIMER / ACK_OVERFLOW re-issues) — the hook the Phase 5 capture
	// view will use.
	EventCommandSent EventKind = iota
	// EventCommandComplete carries the final Result.
	EventCommandComplete
	// EventToDUpdate carries a node port's Table of Devices, whether it
	// arrived from a discovery we asked for or unsolicited.
	EventToDUpdate
	// EventQueuedMessages fires when a responder reports MessageCount > 0.
	EventQueuedMessages
)

// String renders the event kind.
func (k EventKind) String() string {
	switch k {
	case EventCommandSent:
		return "command-sent"
	case EventCommandComplete:
		return "command-complete"
	case EventToDUpdate:
		return "tod-update"
	case EventQueuedMessages:
		return "queued-messages"
	default:
		return "unknown"
	}
}

// Event is published on RDMController.Events.
type Event struct {
	Kind EventKind
	Node NodeRef
	UID  rdm.UID
	// Result is set for EventCommandComplete.
	Result *Result
	// UIDs is set for EventToDUpdate.
	UIDs []rdm.UID
	// Complete is set for EventToDUpdate: false while blocks are still
	// outstanding.
	Complete bool
	// MessageCount is set for EventQueuedMessages.
	MessageCount byte
	At           time.Time
}

// RDMStats are cheap counters for the diagnostics screen.
type RDMStats struct {
	Sent            uint64
	Responses       uint64
	StrayResponses  uint64
	AckTimers       uint64
	Overflows       uint64
	Retransmissions uint64
	Timeouts        uint64
	Nacks           uint64
	DroppedEvents   uint64
}

// RDMController is the RDM-over-Art-Net client state machine (architecture
// rev 5 §3): per-scope FIFO queues with a single in-flight command,
// transaction-number assignment and response matching, ACK_TIMER deferral,
// ACK_OVERFLOW reassembly with strict per-UID exclusion, NACK decoding, and
// multi-block Table of Devices assembly.
type RDMController struct {
	cfg RDMConfig

	mu           sync.Mutex
	queues       map[queueKey]*cmdQueue
	inflightByTN map[byte]*Command
	// overflowUID marks UIDs currently mid-ACK_OVERFLOW. Nothing may be
	// sent to such a UID until the sequence finishes — including from a
	// different queue, which is possible when the same responder is
	// reachable through two node ports.
	overflowUID map[rdm.UID]*Command
	profiles    map[NodeKey]TimeoutProfile
	tod         map[todKey]*todEntry
	discoveries map[todKey]*Discovery
	tn          byte
	nextID      uint64
	stopped     bool
	stats       RDMStats

	events chan Event
}

// NewRDMController builds a controller. Transport and Clock are required.
func NewRDMController(cfg RDMConfig) *RDMController {
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = artnet.DefaultProtocolVersion
	}
	if cfg.ControllerUID == (rdm.UID{}) {
		cfg.ControllerUID = rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 0x00000001}
	}
	if cfg.PortID == 0 {
		cfg.PortID = 1
	}
	if cfg.DefaultProfile.ResponseTimeout <= 0 {
		cfg.DefaultProfile = ProfileDirect
	}
	if cfg.AckTimerUnit <= 0 {
		cfg.AckTimerUnit = DefaultAckTimerUnit
	}
	if cfg.DiscoveryTimeout <= 0 {
		cfg.DiscoveryTimeout = DefaultDiscoveryTimeout
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = DefaultEventBufferSize
	}
	return &RDMController{
		cfg:          cfg,
		queues:       make(map[queueKey]*cmdQueue),
		inflightByTN: make(map[byte]*Command),
		overflowUID:  make(map[rdm.UID]*Command),
		profiles:     make(map[NodeKey]TimeoutProfile),
		tod:          make(map[todKey]*todEntry),
		discoveries:  make(map[todKey]*Discovery),
		events:       make(chan Event, cfg.EventBuffer),
	}
}

// Events is the controller's event stream (ToD updates, command lifecycle).
func (c *RDMController) Events() <-chan Event { return c.events }

// Stats returns a snapshot of the counters.
func (c *RDMController) Stats() RDMStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// SetNodeProfile selects a timeout profile for one node — how a wireless
// proxy gets its longer timings without slowing down the direct nodes.
func (c *RDMController) SetNodeProfile(key NodeKey, p TimeoutProfile) {
	c.mu.Lock()
	c.profiles[key] = p
	c.mu.Unlock()
}

// Stop aborts every queued and in-flight command and stops all timers.
func (c *RDMController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	for _, q := range c.queues {
		if q.inflight != nil {
			c.finishLocked(q.inflight, ResultAborted, ErrControllerStopped)
		}
		pending := q.pending
		q.pending = nil
		for _, cmd := range pending {
			c.completeLocked(cmd, ResultAborted, ErrControllerStopped)
		}
	}
	for _, d := range c.discoveries {
		c.finishDiscoveryLocked(d, ErrControllerStopped)
	}
}

// Run pumps the transport's inbound channel into HandleInbound.
func (c *RDMController) Run(ctx context.Context) error {
	in := c.cfg.Transport.Inbound()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p, ok := <-in:
			if !ok {
				return nil
			}
			c.HandleInbound(p)
		}
	}
}

// --- submission -----------------------------------------------------------

// Get queues an RDM GET.
func (c *RDMController) Get(node NodeRef, uid rdm.UID, pid rdm.ParameterID, data []byte) *Command {
	return c.Submit(Request{Node: node, UID: uid, CommandClass: rdm.GetCommand, PID: pid, Data: data})
}

// Set queues an RDM SET.
func (c *RDMController) Set(node NodeRef, uid rdm.UID, pid rdm.ParameterID, data []byte) *Command {
	return c.Submit(Request{Node: node, UID: uid, CommandClass: rdm.SetCommand, PID: pid, Data: data})
}

// Submit queues a request and returns its handle immediately. The command
// starts on the wire as soon as its queue is free and its UID is not
// mid-ACK_OVERFLOW.
func (c *RDMController) Submit(req Request) *Command {
	cmd := &Command{req: req, done: make(chan Result, 1)}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		c.completeLocked(cmd, ResultAborted, ErrControllerStopped)
		return cmd
	}
	if req.CommandClass != rdm.GetCommand && req.CommandClass != rdm.SetCommand {
		c.completeLocked(cmd, ResultAborted, fmt.Errorf("%w: 0x%02X", ErrInvalidCommandClass, byte(req.CommandClass)))
		return cmd
	}

	c.nextID++
	cmd.id = c.nextID
	cmd.profile = c.profileForLocked(req)
	cmd.qkey = c.queueKeyFor(req)

	q := c.queues[cmd.qkey]
	if q == nil {
		q = &cmdQueue{}
		c.queues[cmd.qkey] = q
	}
	q.pending = append(q.pending, cmd)
	c.pumpLocked()
	return cmd
}

func (c *RDMController) profileForLocked(req Request) TimeoutProfile {
	if req.Profile != nil {
		return *req.Profile
	}
	if p, ok := c.profiles[req.Node.Key]; ok {
		return p
	}
	return c.cfg.DefaultProfile
}

func (c *RDMController) queueKeyFor(req Request) queueKey {
	switch c.cfg.Scope {
	case ScopeUID:
		// Keyed by UID alone, so a responder reachable through two node
		// ports still gets strictly one in-flight command.
		return queueKey{uid: req.UID}
	case ScopeNode:
		return queueKey{ip: req.Node.Key.IP, bind: req.Node.Key.BindIndex, port: 0xFFFF}
	default: // ScopeNodePort
		return queueKey{ip: req.Node.Key.IP, bind: req.Node.Key.BindIndex, port: req.Node.Port.RawValue()}
	}
}

// pumpLocked starts whatever commands are now eligible to run.
func (c *RDMController) pumpLocked() {
	if c.stopped {
		return
	}
	for _, q := range c.queues {
		if q.inflight != nil || len(q.pending) == 0 {
			continue
		}
		next := q.pending[0]
		if owner, busy := c.overflowUID[next.req.UID]; busy && owner != next {
			// This UID is mid-ACK_OVERFLOW; the whole queue holds behind it.
			continue
		}
		q.pending = q.pending[1:]
		q.inflight = next
		c.issueLocked(next, true)
	}
}

// --- transaction issuing --------------------------------------------------

// releaseTNLocked drops cmd's transaction-number registration, but only if
// the slot still belongs to cmd. An unissued command's tn field is zero, so
// an unguarded delete would evict whichever command legitimately holds TN 0.
func (c *RDMController) releaseTNLocked(cmd *Command) {
	if cur, ok := c.inflightByTN[cmd.tn]; ok && cur == cmd {
		delete(c.inflightByTN, cmd.tn)
	}
}

func (c *RDMController) nextTNLocked() byte {
	// RDM's Transaction Number is a byte and wraps naturally at 255→0.
	tn := c.tn
	c.tn++
	return tn
}

// issueLocked transmits a command. newTN distinguishes a fresh transaction
// (first send, ACK_TIMER re-issue, ACK_OVERFLOW continuation) from a
// retransmission, which deliberately reuses the TN so a late reply to the
// original attempt still satisfies the command instead of being discarded.
func (c *RDMController) issueLocked(cmd *Command, newTN bool) {
	now := c.cfg.Clock.Now()
	if !cmd.issued {
		cmd.issued = true
		cmd.startedAt = now
		cmd.deadlineAt = now.Add(cmd.profile.CommandDeadline)
		if cmd.profile.CommandDeadline > 0 {
			// The deadline is absolute for the life of the command, so it
			// is not generation-guarded like the per-attempt timers.
			cmd.deadline = c.cfg.Clock.AfterFunc(cmd.profile.CommandDeadline, func() {
				c.onDeadline(cmd)
			})
		}
	}
	if newTN {
		c.releaseTNLocked(cmd)
		cmd.tn = c.nextTNLocked()
		cmd.attempt = 0
	}
	c.inflightByTN[cmd.tn] = cmd

	msg := rdm.Message{
		DestinationUID:       cmd.req.UID,
		SourceUID:            c.cfg.ControllerUID,
		TransactionNumber:    cmd.tn,
		PortIDOrResponseType: c.cfg.PortID,
		SubDevice:            cmd.req.SubDevice,
		CommandClass:         cmd.req.CommandClass,
		ParameterID:          cmd.req.PID,
		ParameterData:        cmd.req.Data,
	}
	pkt := artnet.EncodeRdmPacket(msg, c.cfg.ProtocolVersion, cmd.req.Node.Port.Net, cmd.req.Node.Port.SubUni())
	cmd.wireBytes = artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: pkt})

	c.transmitLocked(cmd)

	if cmd.req.UID.IsBroadcast() {
		// Responders never acknowledge a broadcast (E1.20); waiting for one
		// would guarantee a timeout.
		c.finishLocked(cmd, ResultBroadcast, nil)
		return
	}
	c.armResponseTimerLocked(cmd)
}

func (c *RDMController) transmitLocked(cmd *Command) {
	c.stats.Sent++
	_ = c.cfg.Transport.Send(cmd.wireBytes, cmd.req.Node.Addr)
	c.emitLocked(Event{Kind: EventCommandSent, Node: cmd.req.Node, UID: cmd.req.UID, At: c.cfg.Clock.Now()})
}

func (c *RDMController) armResponseTimerLocked(cmd *Command) {
	cmd.gen++
	gen := cmd.gen
	if cmd.timer != nil {
		cmd.timer.Stop()
	}
	cmd.timer = c.cfg.Clock.AfterFunc(cmd.profile.ResponseTimeout, func() {
		c.onResponseTimeout(cmd, gen)
	})
}

func (c *RDMController) onResponseTimeout(cmd *Command, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd.finished || cmd.gen != gen {
		return
	}
	if cmd.attempt < cmd.profile.Retries {
		cmd.attempt++
		cmd.retransmits++
		c.stats.Retransmissions++
		c.transmitLocked(cmd)
		c.armResponseTimerLocked(cmd)
		return
	}
	c.stats.Timeouts++
	c.finishLocked(cmd, ResultTimeout, ErrTimeout)
}

func (c *RDMController) onDeadline(cmd *Command) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd.finished {
		return
	}
	c.finishLocked(cmd, ResultDeadlineExceeded, ErrDeadlineExceeded)
}

// --- inbound --------------------------------------------------------------

// HandleInbound decodes one datagram and dispatches ArtRdm / ArtTodData.
func (c *RDMController) HandleInbound(in Inbound) {
	pkt, err := artnet.Decode(in.Data)
	if err != nil {
		return
	}
	switch pkt.Kind {
	case artnet.KindRdm:
		msg, err := pkt.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		c.HandleRDMResponse(msg)
	case artnet.KindTodData:
		c.HandleTodData(pkt.TodData, in.From)
	}
}

// HandleRDMResponse folds one decoded RDM response into the state machine.
func (c *RDMController) HandleRDMResponse(msg rdm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !msg.CommandClass.IsResponse() {
		return
	}
	c.stats.Responses++

	cmd := c.inflightByTN[msg.TransactionNumber]
	// Matching requires all three of transaction number, responder UID and
	// command class. A duplicate of an already-completed response finds no
	// in-flight command and is discarded — that is the idempotency rule.
	if cmd == nil || cmd.finished ||
		cmd.req.UID != msg.SourceUID ||
		msg.CommandClass != responseClassFor(cmd.req.CommandClass) {
		c.stats.StrayResponses++
		return
	}

	rt, _ := msg.ResponseType()
	cmd.messageCount = msg.MessageCount
	if msg.MessageCount > 0 {
		c.emitLocked(Event{Kind: EventQueuedMessages, Node: cmd.req.Node, UID: cmd.req.UID,
			MessageCount: msg.MessageCount, At: c.cfg.Clock.Now()})
	}

	if msg.ParameterID != cmd.req.PID {
		// A responder must abort a partial ACK_OVERFLOW transfer if it sees
		// a different PID; if we observe the mirror image (it answers our
		// same-PID re-issue with a different PID) the sequence is dead and
		// the accumulated data is not trustworthy.
		if cmd.overflow {
			c.finishResponseLocked(cmd, ResultAborted, ErrOverflowPIDMismatch, &msg)
			return
		}
		c.finishResponseLocked(cmd, ResultAborted,
			fmt.Errorf("%w: want 0x%04X got 0x%04X", ErrPIDMismatch, uint16(cmd.req.PID), uint16(msg.ParameterID)), &msg)
		return
	}

	switch rt {
	case rdm.ResponseACK:
		if len(msg.ParameterData) > 0 || cmd.blockCount == 0 {
			cmd.blocks = append(cmd.blocks, msg.ParameterData)
			cmd.blockCount++
		}
		c.finishResponseLocked(cmd, ResultAck, nil, &msg)

	case rdm.ResponseACKTimer:
		cmd.ackTimers++
		c.stats.AckTimers++
		delay := c.ackTimerDelay(msg.ParameterData, cmd.profile)
		now := c.cfg.Clock.Now()
		if cmd.profile.CommandDeadline > 0 && now.Add(delay).After(cmd.deadlineAt) {
			// The responder is asking for more time than the command has
			// left; fail now rather than sleeping into a certain deadline.
			c.finishResponseLocked(cmd, ResultDeadlineExceeded, ErrDeadlineExceeded, &msg)
			return
		}
		cmd.gen++
		gen := cmd.gen
		if cmd.timer != nil {
			cmd.timer.Stop()
		}
		cmd.timer = c.cfg.Clock.AfterFunc(delay, func() {
			c.onAckTimerElapsed(cmd, gen)
		})

	case rdm.ResponseNackReason:
		reason := rdm.NackReason(0)
		if len(msg.ParameterData) >= 2 {
			reason = rdm.NackReason(uint16(msg.ParameterData[0])<<8 | uint16(msg.ParameterData[1]))
		}
		c.stats.Nacks++
		cmd.finalResult.NackReason = reason
		c.finishResponseLocked(cmd, ResultNack, &NackError{Reason: reason}, &msg)

	case rdm.ResponseACKOverflow:
		cmd.blocks = append(cmd.blocks, msg.ParameterData)
		cmd.blockCount++
		if !cmd.overflow {
			cmd.overflow = true
			c.stats.Overflows++
			c.overflowUID[cmd.req.UID] = cmd
		}
		now := c.cfg.Clock.Now()
		if cmd.profile.CommandDeadline > 0 && !now.Before(cmd.deadlineAt) {
			c.finishResponseLocked(cmd, ResultDeadlineExceeded, ErrDeadlineExceeded, &msg)
			return
		}
		// Re-issue the identical GET immediately; nothing else may go to
		// this UID until the sequence resolves.
		if cmd.timer != nil {
			cmd.timer.Stop()
			cmd.timer = nil
		}
		c.issueLocked(cmd, true)

	default:
		c.finishResponseLocked(cmd, ResultAborted,
			fmt.Errorf("%w: 0x%02X", ErrUnknownResponseType, byte(rt)), &msg)
	}
}

func (c *RDMController) onAckTimerElapsed(cmd *Command, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd.finished || cmd.gen != gen {
		return
	}
	c.issueLocked(cmd, true)
}

// ackTimerDelay converts ACK_TIMER's 2-byte estimate into a duration,
// clamped to the profile's cap. See RDMConfig.AckTimerUnit for the unit
// ambiguity this deliberately parameterises.
func (c *RDMController) ackTimerDelay(pd []byte, profile TimeoutProfile) time.Duration {
	units := uint16(0)
	if len(pd) >= 2 {
		units = uint16(pd[0])<<8 | uint16(pd[1])
	}
	d := time.Duration(units) * c.cfg.AckTimerUnit
	if profile.MaxAckTimer > 0 && d > profile.MaxAckTimer {
		d = profile.MaxAckTimer
	}
	if d < 0 {
		d = 0
	}
	return d
}

func responseClassFor(cc rdm.CommandClass) rdm.CommandClass {
	switch cc {
	case rdm.GetCommand:
		return rdm.GetCommandResponse
	case rdm.SetCommand:
		return rdm.SetCommandResponse
	case rdm.DiscoveryCommand:
		return rdm.DiscoveryCommandResponse
	default:
		return cc
	}
}

// --- completion -----------------------------------------------------------

func (c *RDMController) finishResponseLocked(cmd *Command, kind ResultKind, err error, msg *rdm.Message) {
	m := *msg
	cmd.finalResult.Response = &m
	c.finishLocked(cmd, kind, err)
}

// finishLocked completes an in-flight command: stops its timers, releases
// its queue slot and any overflow hold, publishes the event, then pumps.
func (c *RDMController) finishLocked(cmd *Command, kind ResultKind, err error) {
	if cmd.finished {
		return
	}
	c.releaseTNLocked(cmd)
	if owner, ok := c.overflowUID[cmd.req.UID]; ok && owner == cmd {
		delete(c.overflowUID, cmd.req.UID)
	}
	if q := c.queues[cmd.qkey]; q != nil && q.inflight == cmd {
		q.inflight = nil
	}
	c.completeLocked(cmd, kind, err)
	c.pumpLocked()
}

// completeLocked builds and delivers the Result. It is also the path for
// commands that never reached the wire (rejected at submission).
func (c *RDMController) completeLocked(cmd *Command, kind ResultKind, err error) {
	if cmd.finished {
		return
	}
	cmd.finished = true
	cmd.gen++
	if cmd.timer != nil {
		cmd.timer.Stop()
		cmd.timer = nil
	}
	if cmd.deadline != nil {
		cmd.deadline.Stop()
		cmd.deadline = nil
	}

	res := cmd.finalResult
	res.Kind = kind
	res.Request = cmd.req
	res.Err = err
	res.Blocks = cmd.blockCount
	res.AckTimers = cmd.ackTimers
	res.Retransmissions = cmd.retransmits
	res.MessageCount = cmd.messageCount
	if cmd.issued {
		res.Elapsed = c.cfg.Clock.Now().Sub(cmd.startedAt)
	}
	if len(cmd.blocks) > 0 {
		total := 0
		for _, b := range cmd.blocks {
			total += len(b)
		}
		data := make([]byte, 0, total)
		for _, b := range cmd.blocks {
			data = append(data, b...)
		}
		res.Data = data
	}

	cmd.finalResult = res
	cmd.done <- res
	close(cmd.done)

	c.emitLocked(Event{Kind: EventCommandComplete, Node: cmd.req.Node, UID: cmd.req.UID,
		Result: &res, At: c.cfg.Clock.Now()})
}

func (c *RDMController) emitLocked(ev Event) {
	select {
	case c.events <- ev:
	default:
		c.stats.DroppedEvents++
	}
}
