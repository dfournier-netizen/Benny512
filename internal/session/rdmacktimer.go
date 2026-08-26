package session

import (
	"benny512/internal/rdm"
)

// The ACK_TIMER continuation.
//
// What RDM-LOG8 established, from a verified cold start.
//
// The owner power-cycled the MoonLite2 pair and started a session
// immediately. The first request of the session, TN=0 to 22A6:004D09D1,
// came back ACK_TIMER — not PROXY_BUFFER_FULL. The proxy buffer really was
// persistent state carried across sessions, and a power cycle really does
// clear it. Then, on a link with nothing else on it:
//
//	TN=0  ACK_TIMER raw=3     TN=14 ACK_TIMER raw=6
//	TN=1  ACK_TIMER raw=6     TN=15 ACK_TIMER raw=9
//	TN=2  ACK                 TN=16 ACK_TIMER raw=12
//	TN=3  ACK_TIMER raw=2     TN=17 ACK_TIMER raw=15
//	TN=4  ACK_TIMER raw=2     TN=18 ACK_TIMER raw=18
//	TN=5  ACK_TIMER raw=3     TN=19 ACK_TIMER raw=21
//	TN=6  ACK                 TN=20 ACK_TIMER raw=24
//	  …                       TN=21 ACK_TIMER raw=30
//	                          TN=22 NACK PROXY_BUFFER_FULL
//
// Twenty deferrals, four answers, then the buffer is full — and the
// responder's own time estimate climbs monotonically (6, 9, 12, 15, 18, 21,
// 24, 30) right up to the refusal. That is the proxy reporting its queue
// depth in real time while we fill it.
//
// The mechanism: Benny512 collected each deferred answer by re-issuing the
// original request under a fresh transaction number. To the proxy that is a
// new transaction needing a new buffer slot and a new trip over the air, so
// the previously parked answer is stranded and its slot never freed. Twenty
// of those exhausts the buffer. E1.20's actual continuation after ACK_TIMER
// is to wait the estimated time and then collect the parked answer with
// GET QUEUED_MESSAGE, which the proxy serves from the buffer it already
// holds — freeing the slot instead of consuming another.
//
// The measured wait also settles the AckTimerUnit ambiguity that had been
// flagged for architect review since Phase 1c. Inter-response gaps across
// the twenty deferrals track raw x 10 ms plus a 12-30 ms round trip almost
// exactly (raw=6 -> 88 ms, raw=9 -> 116 ms, raw=12 -> 144 ms, raw=15 ->
// 171 ms, raw=30 -> 312 ms). A 1 ms unit would make every gap a flat ~30 ms
// and a 100 ms unit would make raw=30 over three seconds. E1.20's 10 ms is
// right and Benny512 was already honouring it.
//
// Why the continuation is nevertheless not unconditional.
//
// RDM-LOG8 also contains the first field trial of the recovery drain, and
// it is a negative result worth keeping: six GET QUEUED_MESSAGE requests
// went out across two drain passes, addressed to the two proxied UIDs, and
// drew ZERO responses. Not a NACK — silence, three response timeouts per
// pass. And 22A6:004D09D1's own SUPPORTED_PARAMETERS (RDM-LOG6, taken while
// it was wired directly with no radio in the path) lists neither 0x0020
// QUEUED_MESSAGE nor 0x0030 STATUS_MESSAGES.
//
// So on this rig, replacing the re-issue with a bare QUEUED_MESSAGE would
// trade a path that demonstrably produces answers — four of them in LOG8 —
// for one that has so far produced none. Unconditional would not be
// "correct", merely unconditional. The spec path is therefore tried first
// and the answer is remembered per device: a responder that collects
// properly is collected from forever after, and one that does not costs a
// single probe before reverting to exactly today's behaviour. Nothing about
// the wired path changes except that one probe, once, per device that ever
// answers ACK_TIMER at all — and RDM-LOG6's 82-for-82 wired walk contains
// no ACK_TIMER, so a hard line pays nothing.

// DefaultMaxAckTimerCollect bounds how many queued messages that are NOT
// ours one command may collect before giving up and re-issuing the original
// request.
//
// Four, because each collected message is real progress — it is a slot freed
// in the proxy's buffer — but a responder handing back an endless stream of
// other people's messages is one we should stop waiting on. Note what this
// does not count: a probe answered with ACK_TIMER, which means the responder
// is still working and has cost us nothing. That chain is bounded by the
// command deadline instead, exactly as a chain of re-issues already was.
const DefaultMaxAckTimerCollect = 4

// AckTimerCollectPolicy selects what the controller does when an ACK_TIMER's
// estimated time has elapsed.
type AckTimerCollectPolicy int

// ACK_TIMER continuation policies. The zero value is the default.
const (
	// CollectQueuedMessageFirst follows E1.20: collect the parked answer
	// with GET QUEUED_MESSAGE, and fall back to re-issuing the original
	// request if this responder turns out not to support that. The fallback
	// decision is made once per device and then remembered.
	CollectQueuedMessageFirst AckTimerCollectPolicy = iota
	// ReissueOnly is the pre-LOG8 behaviour: always re-issue the original
	// request under a fresh transaction number. Kept as an escape hatch for
	// a rig where the probe turns out to cost more than it saves; it is the
	// behaviour that fills a proxy buffer in twenty deferrals.
	ReissueOnly
)

// String renders the policy.
func (p AckTimerCollectPolicy) String() string {
	switch p {
	case CollectQueuedMessageFirst:
		return "queued-message-first"
	case ReissueOnly:
		return "reissue-only"
	default:
		return "unknown"
	}
}

// collectMode is what one device has been observed to support, learned from
// the first probe and reused thereafter.
type collectMode int

const (
	// collectUnknown: never probed. The next ACK_TIMER probes.
	collectUnknown collectMode = iota
	// collectQueued: this device answers GET QUEUED_MESSAGE properly.
	collectQueued
	// collectReissue: this device does not, so re-issue instead. Set by a
	// NACK of UNKNOWN_PID / UNSUPPORTED_COMMAND_CLASS, or by silence.
	collectReissue
)

// ackTimerCollects decides whether cmd's elapsed ACK_TIMER should be
// continued with a QUEUED_MESSAGE probe or with a plain re-issue.
func (c *RDMController) ackTimerCollectsLocked(cmd *Command) bool {
	if c.cfg.AckTimerCollect == ReissueOnly {
		return false
	}
	if isQueuedMessageRequest(cmd.req) {
		// A drain command's own ACK_TIMER is continued by re-issuing the
		// drain, which is already a QUEUED_MESSAGE. Probing for a probe
		// would be circular.
		return false
	}
	if cmd.collectOrphans >= c.maxAckTimerCollect() {
		// The cap counts messages collected that were not ours, not probes
		// issued. A probe answered with ACK_TIMER made no progress and cost
		// no buffer slot — the responder is simply still working, which is
		// the RDM-LOG8 shape (its estimate climbed 6, 9, 12 ... 30 across
		// consecutive deferrals). Capping on probes would abandon exactly
		// the case this change exists to serve; the CommandDeadline is what
		// bounds an endless deferral chain, as it already did for re-issues.
		return false
	}
	switch c.collectModeLocked(cmd.req) {
	case collectReissue:
		return false
	default:
		return true
	}
}

func (c *RDMController) maxAckTimerCollect() int {
	if c.cfg.MaxAckTimerCollect > 0 {
		return c.cfg.MaxAckTimerCollect
	}
	return DefaultMaxAckTimerCollect
}

// collectModeLocked reads a device's learned collection mode.
func (c *RDMController) collectModeLocked(req Request) collectMode {
	if h := c.devices[deviceKeyFor(req)]; h != nil {
		return h.collect
	}
	return collectUnknown
}

// noteCollectModeLocked records what a probe taught us about this device.
func (c *RDMController) noteCollectModeLocked(req Request, m collectMode) {
	h := c.deviceHealthLocked(req)
	if h.collect == m {
		return
	}
	h.collect = m
	if m == collectQueued {
		c.stats.AckTimerCollectors++
	}
}

// beginCollectLocked puts a GET QUEUED_MESSAGE probe on the wire in place of
// re-issuing cmd's original request.
//
// The command keeps its queue slot, its deadline and its identity: req is
// never mutated, so it still knows which parameter it is waiting for. What
// changes is only what goes out on the wire for this one transaction — see
// issueLocked's wirePID override.
func (c *RDMController) beginCollectLocked(cmd *Command) {
	cmd.collecting = true
	cmd.collectProbes++
	c.stats.AckTimerCollects++
	// The latch tracks what was put on the wire, which for a probe is
	// QUEUED_MESSAGE — so an ACK_TIMER that echoes 0x0020 matches exactly
	// as it would for any other request, and only a real answer needs the
	// one licensed substitution. Each probe is a fresh question, so the pin
	// is released for it.
	cmd.answerPID = rdm.PIDQueuedMessage
	cmd.answerPIDSet = false
	c.resetBlocksLocked(cmd)
	c.issueLocked(cmd, true)
}

// abandonCollectLocked gives up on collecting and re-issues the original
// request — the pre-LOG8 path, reached when this responder cannot serve a
// queued message, or when the queue held nothing for us.
func (c *RDMController) abandonCollectLocked(cmd *Command) {
	cmd.collecting = false
	cmd.answerPID = cmd.req.PID
	cmd.answerPIDSet = false
	c.resetBlocksLocked(cmd)
	c.stats.AckTimerReissues++
	c.issueLocked(cmd, true)
}

// resetBlocksLocked discards data accumulated for a probe that turned out
// not to be our answer, so it cannot leak into the eventual Result.
func (c *RDMController) resetBlocksLocked(cmd *Command) {
	cmd.blocks = nil
	cmd.blockCount = 0
	if owner, ok := c.overflowUID[cmd.req.UID]; ok && owner == cmd {
		delete(c.overflowUID, cmd.req.UID)
	}
	cmd.overflow = false
}

// collectRoutesToLocked decides whether a collected queued message answers
// the command that collected it.
//
// This is the routing rule, and it is deliberately narrow. A queued message
// arrives with no reference to the request it answers: the transaction
// number on the wire belongs to the QUEUED_MESSAGE probe, not to the
// original request, and E1.20 gives a responder no way to say "this is the
// answer to the DEVICE_INFO you asked for at 11:46:55". All we have is the
// PID the responder chose to deliver under.
//
// So a collected message is attributed to the waiting command only when all
// of node, responder UID and delivered PID agree with what that command is
// waiting for — and it is only ever offered to the one command that issued
// the probe, never searched for across the whole in-flight set. Everything
// else is an orphan: a stale answer from an earlier session, or one whose
// command has already given up. Orphans are published (see
// EventQueuedMessageCollected) and filed by the registry under their own
// PID, but they are never attributed to anything.
//
// Note what this does NOT touch. Deciding which *command* a *response*
// belongs to is still transaction number plus source UID plus command
// class, in HandleRDMResponse, unchanged. This is a second and separate
// hand-off, from a completed probe to the command that issued it, and it
// can only ever move data within one command's own transaction.
func collectRoutesToLocked(cmd *Command, pid rdm.ParameterID) bool {
	return pid == cmd.req.PID
}

// isEmptyQueueMarker reports whether a collected STATUS_MESSAGES payload is
// E1.20's "nothing queued" answer rather than real content.
func isEmptyQueueMarker(pid rdm.ParameterID, data []byte) bool {
	if pid != rdm.PIDStatusMessages {
		return false
	}
	msgs, err := rdm.DecodeStatusMessages(data)
	if err != nil {
		return false
	}
	return isEmptyStatusReport(msgs)
}

// handleCollectAckLocked folds a probe's ACK into the waiting command.
func (c *RDMController) handleCollectAckLocked(cmd *Command, msg *rdm.Message) {
	// The responder served a queued message, so it does collect properly.
	c.noteCollectModeLocked(cmd.req, collectQueued)

	if len(msg.ParameterData) > 0 || cmd.blockCount == 0 {
		cmd.blocks = append(cmd.blocks, msg.ParameterData)
		cmd.blockCount++
	}

	if collectRoutesToLocked(cmd, msg.ParameterID) {
		// This is the answer we were deferred on, collected without costing
		// the proxy another slot. Finish exactly as a direct ACK would.
		cmd.collecting = false
		c.stats.AckTimerCollectHits++
		c.finishResponseLocked(cmd, ResultAck, nil, msg)
		return
	}

	data := concatBlocks(cmd.blocks)
	if isEmptyQueueMarker(msg.ParameterID, data) {
		// The queue is empty and our answer was not in it. The responder
		// behaved correctly, so keep collectQueued; but there is nothing
		// left to collect, so go back to asking directly.
		c.abandonCollectLocked(cmd)
		return
	}

	// An orphan: a real queued message, but not ours. File it and keep
	// collecting — ours may be further down the queue.
	cmd.collectOrphans++
	c.stats.QueuedMessagesDrained++
	c.emitLocked(Event{
		Kind: EventQueuedMessageCollected, Node: cmd.req.Node, UID: cmd.req.UID,
		QueuedPID: msg.ParameterID, QueuedData: data, At: c.cfg.Clock.Now(),
	})
	if cmd.collectOrphans >= c.maxAckTimerCollect() {
		c.abandonCollectLocked(cmd)
		return
	}
	c.beginCollectLocked(cmd)
}

// handleCollectNackLocked folds a probe's NACK into the waiting command.
func (c *RDMController) handleCollectNackLocked(cmd *Command, msg *rdm.Message, reason rdm.NackReason) {
	switch reason {
	case rdm.NackUnknownPID, rdm.NackUnsupportedCommandClass:
		// A settled answer about this device: it does not do queued
		// messages. Remember it, so this costs one probe per device rather
		// than one per deferral, and never ask it again.
		c.noteCollectModeLocked(cmd.req, collectReissue)
	case rdm.NackProxyBufferFull:
		// The proxy would not even take the probe. That says nothing about
		// whether this device collects properly, so nothing is learned —
		// but the original request is what the caller wants, and it is the
		// thing that feeds the breaker, so hand back to it.
	default:
		// Any other NACK is about QUEUED_MESSAGE, not about the parameter
		// the caller asked for, so it must not be reported as the caller's
		// answer. Fall back rather than surfacing it.
	}
	c.abandonCollectLocked(cmd)
}

// collectTimedOutLocked handles silence in answer to a probe.
//
// RDM-LOG8's drain passes are exactly this case: six probes, no responses at
// all. Silence gets no retransmission budget — one probe is enough to learn
// that this device will not answer — and the device is marked so that later
// deferrals go straight to a re-issue.
func (c *RDMController) collectTimedOutLocked(cmd *Command) {
	c.noteCollectModeLocked(cmd.req, collectReissue)
	c.stats.AckTimerCollectTimeouts++
	c.abandonCollectLocked(cmd)
}

// concatBlocks joins an ACK_OVERFLOW reassembly.
func concatBlocks(blocks [][]byte) []byte {
	if len(blocks) == 0 {
		return nil
	}
	if len(blocks) == 1 {
		return blocks[0]
	}
	total := 0
	for _, b := range blocks {
		total += len(b)
	}
	out := make([]byte, 0, total)
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}
