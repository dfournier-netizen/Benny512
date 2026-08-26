package session

import (
	"context"
	"errors"

	"benny512/internal/rdm"
)

// ErrDrainInProgress reports that a drain pass for this device was already
// running and had a waiter, so this caller's request was not started.
var ErrDrainInProgress = errors.New("session: QUEUED_MESSAGE drain already in progress for this device")

// GET QUEUED_MESSAGE (PID 0x0020) support.
//
// Why this exists, and why it did not until now.
//
// E1.20 defines NR_PROXY_BUFFER_FULL (0x000A) as "the proxy buffer is full
// and can not store any more Queued Message or Status Message responses".
// The one mechanism named in the definition of that error is QUEUED_MESSAGE,
// and QUEUED_MESSAGE is the one mechanism this project deliberately skipped
// — see the note this replaces in params/status.go. The reason for skipping
// it was an internal invariant (HandleRDMResponse required the response's
// PID to equal the request's), not a protocol constraint: by spec a
// QUEUED_MESSAGE response carries the PID of the message being *delivered*,
// which is the entire point of the PID. So the invariant was relaxed for
// exactly that one request PID and nothing else — see Command.answerPIDSet.
//
// What the bench logs actually say (RDM-LOG2..7, LumenRadio MoonLite2
// 4C55:EFAD7AF2 acting as proxy for 4C55:6DA2C93B and an Elation KL Core IP):
//
//   - The proxy answers for itself perfectly: 54 ACKs of 69 requests in
//     RDM-LOG7, while every one of the 24 requests to the two devices behind
//     it is refused with PROXY_BUFFER_FULL.
//   - In RDM-LOG7 the refusals start at transaction number 0. The very first
//     request of the session is refused, with no ACK_TIMER first. The buffer
//     was already full before Benny512 said anything — it is state the proxy
//     carried in from an earlier session, and nothing Benny512 does has ever
//     emptied it.
//   - Across every log, the proxy does use ACK_TIMER (0x001E = 300 ms at
//     E1.20's 10 ms unit) when it accepts a request and goes to fetch the
//     answer over the air. That deferred answer becomes a queued message.
//     Benny512 collects it by re-issuing the original request under a fresh
//     transaction number, which the proxy reasonably treats as a new
//     transaction needing a new buffer slot — so the deferred answer is
//     never collected and never freed. See the TODO at the bottom of this
//     file: closing that loop is the next change, and it is deliberately
//     not this one.
//
// Draining is therefore the documented remedy for the condition the bench
// actually produces, and the only remedy short of power-cycling the radio.

// DefaultQueuedMessageDrainLimit bounds one drain pass. A responder that
// keeps handing back messages without ever reporting its queue empty is
// misbehaving; 32 is generous for real status backlogs and small enough
// that a stuck responder costs one bounded burst rather than a live-lock.
const DefaultQueuedMessageDrainLimit = 32

// QueuedMessageDrainPolicy selects when the controller drains on its own.
type QueuedMessageDrainPolicy int

// Drain policies. The zero value is the default.
const (
	// DrainOnProxyRecovery drains a device's queue at the moment its proxy
	// circuit breaker opens — the moment Benny512 would otherwise stop
	// asking that device anything at all.
	//
	// Bounded by construction: the breaker opens at most once per
	// ProxyBreakerTrip failed commands, so the cost is one bounded pass per
	// giving-up event, not one per refused command. It is the moment with
	// the most to gain and the least to lose, because the alternative on
	// that code path is a cool-down during which nothing is asked at all.
	//
	// Since RDM-LOG8 this is a backstop for a buffer that was already full
	// at session start, not the primary defence — the ACK_TIMER continuation
	// in rdmacktimer.go is what stops the buffer filling. It is also skipped
	// for devices the continuation has already found do not serve queued
	// messages; see maybeAutoDrainLocked.
	DrainOnProxyRecovery QueuedMessageDrainPolicy = iota
	// DrainOff never drains automatically. DrainQueuedMessages still works.
	DrainOff
	// DrainOnProxyRecoveryAndMessageCount additionally drains whenever a
	// responder reports a non-zero Message Count, which is E1.20's canonical
	// "I have something for you" signal.
	//
	// It is not the default, for a measured reason: Message Count is zero on
	// all 621 responses across all seven bench logs, including the 54 clean
	// ACKs from the proxy itself and all 24 of its PROXY_BUFFER_FULL
	// refusals. On this hardware the trigger never fires, so making it the
	// default would buy nothing while adding a drain pass to every wired
	// device that does populate the field honestly.
	DrainOnProxyRecoveryAndMessageCount
)

// String renders the policy.
func (p QueuedMessageDrainPolicy) String() string {
	switch p {
	case DrainOnProxyRecovery:
		return "on-proxy-recovery"
	case DrainOff:
		return "off"
	case DrainOnProxyRecoveryAndMessageCount:
		return "on-proxy-recovery-and-message-count"
	default:
		return "unknown"
	}
}

// DrainReason records what started a drain pass.
type DrainReason int

// Drain reasons.
const (
	// DrainReasonCaller: an explicit DrainQueuedMessages call.
	DrainReasonCaller DrainReason = iota
	// DrainReasonProxyRecovery: a device's proxy breaker just opened.
	DrainReasonProxyRecovery
	// DrainReasonMessageCount: a responder reported MessageCount > 0.
	DrainReasonMessageCount
)

// String renders the reason.
func (r DrainReason) String() string {
	switch r {
	case DrainReasonCaller:
		return "caller"
	case DrainReasonProxyRecovery:
		return "proxy-recovery"
	case DrainReasonMessageCount:
		return "message-count"
	default:
		return "unknown"
	}
}

// QueuedMessage is one message collected from a responder's queue.
//
// PID is the parameter the responder chose to deliver, which is emphatically
// not QUEUED_MESSAGE: a queued message is a deferred answer to some earlier
// request, so it arrives under that request's PID. Decode Data with that
// PID's own decoder.
type QueuedMessage struct {
	PID  rdm.ParameterID
	Data []byte
	// Status is the decoded payload when PID is STATUS_MESSAGES, which is
	// the common case and the one E1.20 requires every queue-capable
	// responder to support.
	Status []rdm.StatusMessage
}

// DrainResult is the outcome of one drain pass.
type DrainResult struct {
	UID rdm.UID
	// Messages is every queued message collected, in delivery order. The
	// terminating empty STATUS_MESSAGES marker is not included.
	Messages []QueuedMessage
	// Status is the concatenation of every decoded STATUS_MESSAGES payload,
	// for callers that only care about status and not about which iteration
	// a message arrived in.
	Status []rdm.StatusMessage
	// Iterations counts GET QUEUED_MESSAGE commands actually issued.
	Iterations int
	// Empty is true when the responder reported its queue empty (a
	// STATUS_MESSAGES response with no messages, or only STATUS_NONE) —
	// the only clean termination.
	Empty bool
	// Truncated is true when the pass stopped at the iteration cap instead.
	Truncated bool
	// Unsupported is true when the responder NACKed the request with
	// UNKNOWN_PID: it does not implement QUEUED_MESSAGE, which is a
	// settled answer rather than a failure.
	Unsupported bool
	// Reason records what started the pass.
	Reason DrainReason
	// LastKind is the result kind of the final command issued.
	LastKind ResultKind
	// Err is the error that stopped the pass early, if any. A drain that
	// ends Empty, Truncated or Unsupported has a nil Err.
	Err error
}

// drainState is one in-flight drain pass. Guarded by RDMController.mu.
type drainState struct {
	key    deviceKey
	node   NodeRef
	uid    rdm.UID
	filter rdm.StatusType
	max    int
	res    DrainResult
	done   chan DrainResult // nil for automatic drains
}

// queuedMessagePIDFloats reports whether cmd may accept a response whose
// Parameter ID differs from the one it asked for.
//
// This is the whole of the relaxation, and it is deliberately four
// conjunctions wide:
//
//   - what is on the wire must be GET QUEUED_MESSAGE — either because this
//     is a drain command, or because it is mid-collection standing in for a
//     re-issue after ACK_TIMER (rdmacktimer.go). No other PID is affected;
//   - the answering PID must not already be pinned, so a transaction gets
//     exactly one degree of freedom rather than a licence to wander;
//   - the response must be an answer, not an ACK_TIMER deferral — a
//     deferral echoes the request's own PID by spec and has nothing to
//     substitute yet;
//   - the command must not be mid-ACK_OVERFLOW, so a partial reassembly is
//     still aborted on a PID change exactly as before.
//
// Note what is NOT relaxed. Matching a response to a command still requires
// transaction number, responder UID and command class to agree
// (HandleRDMResponse), and that — not the PID check — is what stops a stray
// or duplicated response being attributed to the wrong in-flight command.
// The PID check's job is narrower: it catches a responder answering a
// different question than the one asked, and it protects the integrity of
// an ACK_OVERFLOW reassembly. Both survive intact for every other PID, and
// the second survives intact for this one too.
//
// Nor does a collecting command lose the first protection for its own
// request: a collected message is only allowed to *satisfy* that command
// when its PID matches req.PID exactly (collectRoutesToLocked). The float
// here lets the probe's answer through the door; it does not let it be
// mistaken for the caller's answer.
func queuedMessagePIDFloats(cmd *Command, rt rdm.ResponseType) bool {
	return (cmd.collecting || cmd.req.PID == rdm.PIDQueuedMessage) &&
		!cmd.answerPIDSet &&
		rt != rdm.ResponseACKTimer &&
		!cmd.overflow
}

// isQueuedMessageRequest reports whether req is a drain command — a command
// whose *request* is QUEUED_MESSAGE, as opposed to one merely collecting.
func isQueuedMessageRequest(req Request) bool {
	return req.CommandClass == rdm.GetCommand && req.PID == rdm.PIDQueuedMessage
}

// DrainQueuedMessages drains one responder's message queue by issuing GET
// QUEUED_MESSAGE repeatedly until the responder reports the queue empty,
// maxIter iterations are reached, the responder answers something other
// than an ACK, or ctx is cancelled. maxIter <= 0 uses the controller's
// configured cap.
//
// filter is the severity floor: E1.20 STATUS_NONE (0x00) asks for every
// queued message regardless of severity, which is what a recovery drain
// wants.
//
// At most one drain runs per device at a time. A caller arriving while an
// automatic drain is already in flight waits for that pass and receives its
// result, rather than interleaving with it and stealing half its messages.
func (c *RDMController) DrainQueuedMessages(ctx context.Context, node NodeRef, uid rdm.UID, filter rdm.StatusType, maxIter int) (DrainResult, error) {
	done := make(chan DrainResult, 1)

	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return DrainResult{UID: uid}, ErrControllerStopped
	}
	c.startDrainLocked(node, uid, filter, maxIter, DrainReasonCaller, done)
	c.mu.Unlock()

	select {
	case res := <-done:
		return res, res.Err
	case <-ctx.Done():
		return DrainResult{UID: uid}, ctx.Err()
	}
}

// startDrainLocked begins a drain pass, or attaches done to the one already
// running for this device.
func (c *RDMController) startDrainLocked(node NodeRef, uid rdm.UID, filter rdm.StatusType, maxIter int, reason DrainReason, done chan DrainResult) {
	if c.stopped {
		if done != nil {
			done <- DrainResult{UID: uid, Reason: reason, Err: ErrControllerStopped}
		}
		return
	}
	key := deviceKey{link: linkKeyFor(node), uid: uid}
	if st := c.drains[key]; st != nil {
		if done != nil && st.done == nil {
			st.done = done
		} else if done != nil {
			// A second waiter on the same pass; the first one owns the
			// channel, so answer this one immediately with what we know.
			done <- DrainResult{UID: uid, Reason: reason, Err: ErrDrainInProgress}
		}
		return
	}
	if maxIter <= 0 {
		maxIter = c.cfg.MaxQueuedMessageDrain
	}
	if maxIter <= 0 {
		maxIter = DefaultQueuedMessageDrainLimit
	}
	st := &drainState{
		key: key, node: node, uid: uid, filter: filter, max: maxIter, done: done,
		res: DrainResult{UID: uid, Reason: reason},
	}
	c.drains[key] = st
	c.stats.QueuedDrains++
	c.scheduleDrainStepLocked(st)
}

// scheduleDrainStepLocked defers the next GET QUEUED_MESSAGE to a zero-delay
// timer.
//
// The indirection is not decoration. Drain steps are triggered from inside
// completeLocked, which runs with c.mu held and part-way through another
// command's teardown; submitting the next command straight from there would
// re-enter pumpLocked while an outer pumpLocked is still walking the queue
// map. Bouncing off the Clock means every drain command is submitted from a
// clean stack with nothing else in flight, and — because FakeClock fires
// zero-delay timers on Advance, never inline — it stays exactly as
// deterministic under test as it is in production.
func (c *RDMController) scheduleDrainStepLocked(st *drainState) {
	c.cfg.Clock.AfterFunc(0, func() { c.drainStep(st) })
}

// drainStep issues the next GET QUEUED_MESSAGE of a pass.
func (c *RDMController) drainStep(st *drainState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.drains[st.key] != st {
		return // superseded or already finished
	}
	if c.stopped {
		st.res.Err = ErrControllerStopped
		c.finishDrainLocked(st)
		return
	}
	if st.res.Iterations >= st.max {
		st.res.Truncated = true
		c.finishDrainLocked(st)
		return
	}
	st.res.Iterations++
	cmd := c.submitLocked(Request{
		Node:         st.node,
		UID:          st.uid,
		CommandClass: rdm.GetCommand,
		PID:          rdm.PIDQueuedMessage,
		Data:         rdm.EncodeQueuedMessageRequest(st.filter),
	})
	cmd.notify = func(res Result) { c.drainAdvanceLocked(st, res) }
}

// drainAdvanceLocked folds one drain command's result into the pass and
// decides whether to go round again. Called from completeLocked with c.mu
// held, so it only ever mutates state and schedules a timer.
func (c *RDMController) drainAdvanceLocked(st *drainState, res Result) {
	if c.drains[st.key] != st {
		return
	}
	st.res.LastKind = res.Kind

	if res.Kind != ResultAck {
		// A NACK of UNKNOWN_PID is a settled answer: this responder does not
		// implement QUEUED_MESSAGE, so there is nothing to drain and nothing
		// went wrong. Every other non-ACK outcome ends the pass carrying its
		// own error, which is what a caller needs to tell "the queue is
		// empty" from "we never got to look".
		if res.Kind == ResultNack && res.NackReason == rdm.NackUnknownPID {
			st.res.Unsupported = true
		} else {
			st.res.Err = res.Err
		}
		c.finishDrainLocked(st)
		return
	}

	pid := res.ResponsePID
	if pid == 0 && res.Response != nil {
		pid = res.Response.ParameterID
	}

	if pid == rdm.PIDStatusMessages {
		msgs, err := rdm.DecodeStatusMessages(res.Data)
		if err != nil {
			st.res.Err = err
			c.finishDrainLocked(st)
			return
		}
		if isEmptyStatusReport(msgs) {
			// E1.20's "nothing queued" marker: STATUS_MESSAGES with no
			// entries, or entries that are all STATUS_NONE. This is the
			// only clean way a drain ends, and the loop's real terminator.
			st.res.Empty = true
			c.finishDrainLocked(st)
			return
		}
		st.res.Status = append(st.res.Status, msgs...)
		st.res.Messages = append(st.res.Messages, QueuedMessage{PID: pid, Data: res.Data, Status: msgs})
	} else {
		// A queued message under some other PID — a deferred DEVICE_INFO,
		// a SENSOR_VALUE update. Keep it: this is exactly the class of
		// message the old repeat-GET-STATUS_MESSAGES workaround could not
		// see at all.
		st.res.Messages = append(st.res.Messages, QueuedMessage{PID: pid, Data: res.Data})
	}
	c.stats.QueuedMessagesDrained++
	c.scheduleDrainStepLocked(st)
}

// finishDrainLocked retires a pass and hands its result to any waiter.
func (c *RDMController) finishDrainLocked(st *drainState) {
	if c.drains[st.key] == st {
		delete(c.drains, st.key)
	}
	if st.done != nil {
		st.done <- st.res
		st.done = nil
	}
}

// isEmptyStatusReport reports whether a STATUS_MESSAGES payload is E1.20's
// "queue empty" marker rather than real content.
func isEmptyStatusReport(msgs []rdm.StatusMessage) bool {
	for _, m := range msgs {
		if m.Type != rdm.StatusNone {
			return false
		}
	}
	return true
}

// maybeAutoDrainLocked starts an automatic drain if the policy allows it.
//
// Since RDM-LOG8 this is a backstop rather than the main event. The main
// event is the ACK_TIMER continuation in rdmacktimer.go, which stops the
// buffer filling in the first place; a recovery drain only matters for a
// buffer that is *already* full when a session starts — which LOG8 proved
// really does happen, because the MoonLite2 carries its buffer across power
// cycles of the controller and only a power cycle of the radio clears it.
//
// It also defers to what the continuation has learned. LOG8's own drain
// passes drew six probes and zero responses from the proxied UIDs; there is
// no point spending that again on a device the collection path has already
// found does not serve queued messages.
func (c *RDMController) maybeAutoDrainLocked(node NodeRef, uid rdm.UID, reason DrainReason) {
	switch c.cfg.QueuedMessageDrain {
	case DrainOff:
		return
	case DrainOnProxyRecovery:
		if reason != DrainReasonProxyRecovery {
			return
		}
	}
	if c.collectModeLocked(Request{Node: node, UID: uid}) == collectReissue {
		// Known not to answer GET QUEUED_MESSAGE. Draining would be six
		// silent probes and a pair of response timeouts, which is what the
		// bench actually measured.
		return
	}
	// StatusNone as the filter asks for everything the responder is holding,
	// regardless of severity — a recovery drain wants the buffer empty, not
	// a severity-filtered view of it.
	c.startDrainLocked(node, uid, rdm.StatusNone, 0, reason, nil)
}

// --- circuit-breaker interaction -----------------------------------------

// drainExemptFromBreaker reports whether req may go to the wire even though
// this device's proxy circuit breaker is open.
//
// Only a QUEUED_MESSAGE drain is exempt, and the reasoning is narrow: the
// breaker exists to stop Benny512 spending its budget on a device whose
// proxy refuses everything, but the drain is the documented remedy for
// precisely that condition. Refusing to send the one command that could fix
// the problem, because the problem is happening, is the deadlock the bench
// logs show — RDM-LOG7's buffer was already full at transaction 0, so no
// amount of waiting was ever going to clear it.
//
// The exemption is bounded: a pass is capped at MaxQueuedMessageDrain
// commands, at most one pass runs per device at a time, and automatic passes
// only start when the breaker opens.
func drainExemptFromBreaker(req Request) bool {
	return isQueuedMessageRequest(req)
}

// drainFeedsBreaker reports whether a command's PROXY_BUFFER_FULL failure
// should count toward opening the breaker.
//
// Drain commands deliberately do not count. The asymmetry is the point: a
// drain that is itself refused tells us nothing the breaker did not already
// know (it is open, or opening, because this device is being refused), while
// letting recovery attempts extend their own cool-down would make every
// failed rescue lengthen the sentence. A drain that *succeeds*, on the other
// hand, goes through the ordinary ResultAck path in finishLocked and closes
// the breaker like any other real answer — which is exactly the behaviour
// wanted: the device answered, so stop holding it out.
func drainFeedsBreaker(req Request) bool {
	return !isQueuedMessageRequest(req)
}

// --- diagnostics ----------------------------------------------------------

// DrainsInFlight reports how many drain passes are currently running.
func (c *RDMController) DrainsInFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.drains)
}

// abortDrainsLocked fails every in-flight pass, for Stop().
func (c *RDMController) abortDrainsLocked() {
	for k, st := range c.drains {
		delete(c.drains, k)
		if st.done != nil {
			st.res.Err = ErrControllerStopped
			st.done <- st.res
			st.done = nil
		}
	}
}

// TODO(bench): close the ACK_TIMER loop.
//
// E1.20's flow after an ACK_TIMER is to wait the estimated time and then
// collect the deferred answer with GET QUEUED_MESSAGE. Benny512 instead
// re-issues the original request under a fresh transaction number
// (onAckTimerElapsed), which a proxy reasonably reads as a new transaction
// needing a new buffer slot — so each ACK_TIMER chain strands one answer in
// the proxy's queue and consumes a slot that is never freed. RDM-LOG2 shows
// the shape directly: two ACK_TIMERs at 313 ms apart, then
// PROXY_BUFFER_FULL, repeatedly.
//
// This is not fixed here on purpose. Switching the ACK_TIMER continuation to
// QUEUED_MESSAGE means the collected answer may carry a PID that has nothing
// to do with the command waiting on it — a stale message from an earlier
// session, most likely — so it needs a way to route a drained message to
// whichever command it actually answers, and to leave the current command
// waiting when it answers something else. That is a bigger change to the
// matching rules than relaxing one PID check, it must not regress the wired
// path where re-issue works today, and it wants a bench session to confirm.
// The drain below makes the current damage recoverable; that change would
// stop causing it.
