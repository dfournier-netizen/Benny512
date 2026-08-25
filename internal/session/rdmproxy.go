package session

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"benny512/internal/rdm"
)

// Proxy-saturation handling.
//
// ANSI E1.20 §5.5 / table A-17 define NR_PROXY_BUFFER_FULL as "the proxy
// buffer is full and can not store any more queued messages" — a transient
// condition meaning *retry later*, not a refusal of the request. A bench run
// against an Obsidian EN4 with two LumenRadio Moonlite CRMX units and an
// Elation KL Core showed what treating it as terminal costs: 240 requests,
// 152 NACKs, 134 of them PROXY_BUFFER_FULL, the median gap between requests
// 32 ms and the median delay after a PROXY_BUFFER_FULL before re-asking the
// same UID 0.000 s. Everything on the far side of the wireless link was
// refused; the only responder answering cleanly was the proxy itself. The
// controller was re-issuing into a full buffer as fast as the queue could
// turn around, so the buffer never got the idle time it needed to drain.
//
// Two mechanisms fix that, and they are deliberately separate:
//
//   - Per-command exponential backoff. One command that is refused waits,
//     with a growing delay, and asks again — up to ProxyBackoffAttempts
//     times before it gives up with ResultProxyBufferFull.
//   - Per-link adaptive pacing. The buffer is a *shared* resource, so a
//     refusal also throttles every other command that would traverse the
//     same link (see linkKey), and that throttle relaxes again as
//     transactions start completing.
//
// Neither is a configured profile. Saturation is discovered from the wire,
// so a rig that never refuses is never slowed down (see TimeoutProfile's doc
// comment for why the pacing state does not live there).

// Per-command backoff after a NACK PROXY_BUFFER_FULL.
//
// The delay for the Nth refusal of one command is
// ProxyBackoffInitial * ProxyBackoffFactor^(N-1), capped at
// ProxyBackoffMax; with the shipped values that is
// 250 ms, 500 ms, 1 s, 2 s, 4 s — 7.75 s of patience spread over five
// re-issues.
const (
	// ProxyBackoffInitial is the pause after the first refusal. It is an
	// order of magnitude above the 32 ms hammer rate the bench log caught,
	// and above one CRMX radio frame cycle, so the very first retry already
	// gives the proxy a realistic chance to have moved a response along;
	// it is still short enough that an isolated one-slot-full blip costs a
	// quarter of a second rather than a whole discovery pass.
	ProxyBackoffInitial = 250 * time.Millisecond
	// ProxyBackoffFactor is the growth per successive refusal. Doubling is
	// the standard choice: it reaches useful delays in a handful of steps
	// without a long tail of near-identical retries.
	ProxyBackoffFactor = 2
	// ProxyBackoffMax caps one backoff delay. 4 s is comparable to
	// ProfileWirelessProxy.ResponseTimeout (5 s) — a proxy that still has no
	// room after four seconds of quiet is not "momentarily full", it is
	// wedged or gone, and waiting longer only burns the command deadline.
	ProxyBackoffMax = 4 * time.Second
	// ProxyBackoffAttempts is how many times one command is re-issued after
	// a PROXY_BUFFER_FULL before it fails with ResultProxyBufferFull. Five
	// re-issues span the full 250 ms→4 s schedule, which fits inside
	// ProfileDirect.CommandDeadline (15 s) with room for the round trips and
	// well inside ProfileWirelessProxy's 60 s.
	ProxyBackoffAttempts = 5
)

// Per-link adaptive pacing, engaged only once a link has actually refused.
const (
	// ProxyPaceInitial is the minimum inter-request gap imposed on a link
	// the first time it answers PROXY_BUFFER_FULL. ~10 transactions/s is a
	// realistic sustained rate through a CRMX proxy; the observed 32 ms
	// (~31/s) is not.
	ProxyPaceInitial = 100 * time.Millisecond
	// ProxyPaceFactor widens the gap on each further refusal, so a link that
	// keeps refusing is throttled harder without any operator input.
	ProxyPaceFactor = 2
	// ProxyPaceMax caps the enforced gap at one request every two seconds.
	// Slower than that and a walk of a 30-device rig stops making progress
	// on any useful timescale; at that point the honest outcome is to fail
	// the command with ResultProxyBufferFull rather than to crawl.
	ProxyPaceMax = 2 * time.Second
	// ProxyPaceRelaxAfter is how many consecutive completed transactions on
	// a paced link halve the gap. Four is enough that one lucky reply does
	// not undo the throttle, few enough that a link which has recovered is
	// back to full speed within a handful of commands.
	ProxyPaceRelaxAfter = 4
	// ProxyPaceRelaxFactor is the divisor applied on each relaxation step.
	ProxyPaceRelaxFactor = 2
	// ProxyPaceMin is the floor below which pacing is dropped altogether: a
	// gap this small is already at the rate that saturated the link in the
	// first place, so keeping it buys nothing and costs a timer.
	ProxyPaceMin = 50 * time.Millisecond
)

// ErrProxyBufferFull reports that an RDM path stayed saturated
// (NACK PROXY_BUFFER_FULL) until the retry budget ran out. It is the
// sentinel behind every ProxyBufferFullError, so callers can tell "the proxy
// was saturated" from "the device refused this PID" with errors.Is.
var ErrProxyBufferFull = errors.New("session: RDM proxy buffer full, retries exhausted")

// ProxyBufferFullError is the Result.Err of a ResultProxyBufferFull command:
// the responder's proxy answered NACK PROXY_BUFFER_FULL to every attempt.
//
// It deliberately does not wrap *NackError. A NACK from the device says
// something about the device ("I do not support this PID"); PROXY_BUFFER_FULL
// says something about the path, and callers that cache "asked and answered"
// state per PID must not treat the two alike.
type ProxyBufferFullError struct {
	// Attempts is how many PROXY_BUFFER_FULL responses this command drew.
	Attempts int
	// Waited is the total backoff time spent before giving up.
	Waited time.Duration
	// DeadlineCut is true when the command stopped early because the next
	// backoff would have run past its CommandDeadline, rather than because
	// ProxyBackoffAttempts was reached.
	DeadlineCut bool
}

func (e *ProxyBufferFullError) Error() string {
	if e.DeadlineCut {
		return fmt.Sprintf("session: RDM proxy buffer full (%d refusals over %s; command deadline left no room to retry)",
			e.Attempts, e.Waited)
	}
	return fmt.Sprintf("session: RDM proxy buffer full (%d refusals over %s; retries exhausted)",
		e.Attempts, e.Waited)
}

// Unwrap makes errors.Is(err, ErrProxyBufferFull) work.
func (e *ProxyBufferFullError) Unwrap() error { return ErrProxyBufferFull }

// linkKey identifies the shared RS-485 / wireless path an RDM request
// traverses: one Art-Net node port.
//
// This is NOT the queue key. queueKeyFor follows the configured
// SerializationScope, which may be per-UID (ScopeUID) or per-node
// (ScopeNode); the proxy buffer that fills up is neither. On the bench rig
// the proxy, the responder behind it and a third fixture all sat on one EN4
// port, and the refusals came from the devices *behind* the proxy while the
// proxy's own UID answered every request cleanly — so keying the pause on
// the refusing UID would have left the queue free to hammer the same link
// through its neighbours, and keying it on the node would have throttled
// three unrelated ports. The node port is the granularity that actually
// shares the constrained link.
type linkKey struct {
	ip   netip.Addr
	bind byte
	port uint16
}

func linkKeyFor(n NodeRef) linkKey {
	return linkKey{ip: n.Key.IP, bind: n.Key.BindIndex, port: n.Port.RawValue()}
}

// linkState is one node port's learned pacing. A zero gap means the link has
// never refused (or has fully recovered) and is not paced at all.
type linkState struct {
	gap        time.Duration
	nextSendAt time.Time
	successes  int
	// timer wakes pumpLocked when the gap expires, so a queue held by pacing
	// is never left stalled waiting for an unrelated event.
	timer   Timer
	timerAt time.Time
}

// proxyBackoffFor returns the delay before the Nth refusal is re-issued.
func proxyBackoffFor(refusal int) time.Duration {
	if refusal < 1 {
		refusal = 1
	}
	d := ProxyBackoffInitial
	for i := 1; i < refusal; i++ {
		d *= ProxyBackoffFactor
		if d >= ProxyBackoffMax {
			return ProxyBackoffMax
		}
	}
	return d
}

func (c *RDMController) linkLocked(node NodeRef) *linkState {
	return c.links[linkKeyFor(node)]
}

// noteProxyBufferFullLocked engages (or tightens) pacing on the link a
// refusal came from, and holds every other command on that link for at least
// one gap.
func (c *RDMController) noteProxyBufferFullLocked(node NodeRef) {
	k := linkKeyFor(node)
	ls := c.links[k]
	if ls == nil {
		ls = &linkState{}
		c.links[k] = ls
	}
	if ls.gap <= 0 {
		ls.gap = ProxyPaceInitial
	} else {
		ls.gap *= ProxyPaceFactor
		if ls.gap > ProxyPaceMax {
			ls.gap = ProxyPaceMax
		}
	}
	ls.successes = 0
	ls.nextSendAt = c.cfg.Clock.Now().Add(ls.gap)
}

// noteLinkTransactionLocked records a transaction that the link carried end
// to end. Sustained success relaxes the gap and eventually removes it.
//
// A device NACK counts as success: the proxy found room for the response and
// delivered it, which is exactly the property being measured. A timeout does
// not, because it is evidence of the opposite.
func (c *RDMController) noteLinkTransactionLocked(node NodeRef) {
	ls := c.linkLocked(node)
	if ls == nil || ls.gap <= 0 {
		return
	}
	ls.successes++
	if ls.successes < ProxyPaceRelaxAfter {
		return
	}
	ls.successes = 0
	ls.gap /= ProxyPaceRelaxFactor
	if ls.gap < ProxyPaceMin {
		ls.gap = 0
		ls.nextSendAt = time.Time{}
		if ls.timer != nil {
			ls.timer.Stop()
			ls.timer = nil
		}
	}
}

// paceWaitLocked reports how long a request to node must be held back. Zero
// on any link that has never refused — the direct-wired case pays nothing
// but one map lookup.
func (c *RDMController) paceWaitLocked(node NodeRef, now time.Time) time.Duration {
	ls := c.linkLocked(node)
	if ls == nil || ls.gap <= 0 || !now.Before(ls.nextSendAt) {
		return 0
	}
	return ls.nextSendAt.Sub(now)
}

// stampLinkSendLocked records that a request just went out on node's link,
// so the next one waits a full gap. Called for every transmission, including
// retransmissions and ACK_TIMER re-issues, because the proxy sees those too.
func (c *RDMController) stampLinkSendLocked(node NodeRef, now time.Time) {
	ls := c.linkLocked(node)
	if ls == nil || ls.gap <= 0 {
		return
	}
	ls.nextSendAt = now.Add(ls.gap)
}

// armLinkTimerLocked schedules a pump for when the link's gap expires,
// keeping at most one (earliest) timer per link.
func (c *RDMController) armLinkTimerLocked(node NodeRef, wait time.Duration, now time.Time) {
	k := linkKeyFor(node)
	ls := c.links[k]
	if ls == nil {
		return
	}
	at := now.Add(wait)
	if ls.timer != nil && !ls.timerAt.After(at) {
		return
	}
	if ls.timer != nil {
		ls.timer.Stop()
	}
	ls.timerAt = at
	ls.timer = c.cfg.Clock.AfterFunc(wait, func() { c.onLinkPaceElapsed(k) })
}

func (c *RDMController) onLinkPaceElapsed(k linkKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ls := c.links[k]; ls != nil {
		ls.timer = nil
	}
	c.pumpLocked()
}

// onProxyBufferFullLocked handles a NACK PROXY_BUFFER_FULL: the command
// keeps its queue slot and is re-issued after a growing backoff, and the
// whole link is paced. Only when the attempt cap (or the command deadline)
// is reached does the command fail — with ResultProxyBufferFull, which says
// why.
func (c *RDMController) onProxyBufferFullLocked(cmd *Command, msg *rdm.Message) {
	cmd.proxyRefusals++
	c.stats.ProxyBufferFull++
	c.noteProxyBufferFullLocked(cmd.req.Node)

	if cmd.proxyRefusals > ProxyBackoffAttempts {
		cmd.finalResult.NackReason = rdm.NackProxyBufferFull
		c.finishResponseLocked(cmd, ResultProxyBufferFull,
			&ProxyBufferFullError{Attempts: cmd.proxyRefusals, Waited: cmd.proxyWaited}, msg)
		return
	}

	now := c.cfg.Clock.Now()
	wait := proxyBackoffFor(cmd.proxyRefusals)
	if gap := c.paceWaitLocked(cmd.req.Node, now); gap > wait {
		wait = gap
	}
	if cmd.profile.CommandDeadline > 0 && now.Add(wait).After(cmd.deadlineAt) {
		// Sleeping through the backoff would exhaust the command's overall
		// budget anyway; fail now, while the cause can still be named.
		cmd.finalResult.NackReason = rdm.NackProxyBufferFull
		c.finishResponseLocked(cmd, ResultProxyBufferFull,
			&ProxyBufferFullError{Attempts: cmd.proxyRefusals, Waited: cmd.proxyWaited, DeadlineCut: true}, msg)
		return
	}

	cmd.proxyWaited += wait
	// Drop the transaction-number registration for the duration of the
	// backoff: the re-issue takes a fresh TN, so a duplicate of this refusal
	// arriving late must find nothing and be discarded as stray, exactly as
	// a duplicate of a completed response is.
	c.releaseTNLocked(cmd)
	cmd.gen++
	gen := cmd.gen
	if cmd.timer != nil {
		cmd.timer.Stop()
	}
	cmd.timer = c.cfg.Clock.AfterFunc(wait, func() { c.onProxyBackoffElapsed(cmd, gen) })
}

func (c *RDMController) onProxyBackoffElapsed(cmd *Command, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd.finished || cmd.gen != gen {
		return
	}
	c.issueLocked(cmd, true)
}
