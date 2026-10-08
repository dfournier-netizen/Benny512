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
// buffer is full and can not store any more queued messages" — nominally a
// transient condition meaning *retry later*. Four bench rounds against an
// Obsidian EN4 with two LumenRadio Moonlite CRMX units and an Elation KL
// Core show that on real gear it is very often not transient at all, and
// the shape of the handling below follows from that measurement rather than
// from the standard's wording.
//
// What the three captured logs establish:
//
//   - RDM-LOG2 (no pacing, median 32 ms between requests): 240 requests,
//     240 responses. The proxy's own UID answered 96/96 with ZERO refusals
//     while the two devices behind it were refused 134 times.
//   - RDM-LOG3 and RDM-LOG4 (pacing engaged, median 2.025 s between
//     requests, p90 4.0 s): the refusal rate did not move. LOG3 refused
//     67 of 75 responses; LOG4 refused 112 of 133.
//
// Two conclusions, both load-bearing:
//
//  1. Request rate is not the variable. A refusal that survives a two- to
//     four-second gap is not a rate limit. Sixty times more traffic (LOG2)
//     produced no refusals at all from the healthy responder, and sixty
//     times less traffic (LOG4) produced no improvement for the sick ones.
//     Per-link pacing was therefore removed in full: it cost the one device
//     that worked 96 requests/78 ACKs (LOG2) down to 12 requests/8 ACKs
//     (LOG4) and bought nothing measurable in return.
//
//  2. The refusal is attributable to the target UID, not to the shared
//     link. In every log the proxy's own UID answers cleanly in the same
//     seconds that the two devices behind it are refused, through the same
//     buffer. A genuinely full shared buffer would have no room for the
//     next response either, whoever it was addressed to. The constrained
//     resource is per-proxied-device, so the response must be too.
//
// Hence the two mechanisms below, replacing the previous round's
// per-command-backoff-plus-per-link-pacing pair:
//
//   - A short per-command backoff, for the case E1.20 actually describes:
//     one momentarily-full slot that clears on the next ask.
//   - A per-device circuit breaker, for the case the bench actually
//     produces: a device that refuses everything, indefinitely. Rather than
//     spending a full retry budget per PID forever, the controller stops
//     asking that one device for a while, keeps the rest of the rig running
//     at full speed, and probes periodically so a device that comes back is
//     picked up again.

// Per-command backoff after a NACK PROXY_BUFFER_FULL.
const (
	// ProxyBackoffInitial is the pause after the first refusal. It is an
	// order of magnitude above the 32 ms hammer rate the first bench log
	// caught, and above one CRMX radio frame cycle, so the retry gives a
	// momentarily-full buffer a realistic chance to have moved a response
	// along.
	ProxyBackoffInitial = 250 * time.Millisecond
	// ProxyBackoffFactor is the growth per successive refusal.
	ProxyBackoffFactor = 2
	// ProxyBackoffMax caps one backoff delay.
	ProxyBackoffMax = 4 * time.Second
	// ProxyBackoffAttempts is how many times one command is re-issued after
	// a PROXY_BUFFER_FULL before it fails with ResultProxyBufferFull.
	//
	// This was five, spanning 250 ms→4 s — about 7.75 s of patience per
	// PID. RDM-LOG4 measured what that costs when the refusal is permanent:
	// roughly 8 s burned per PID, across ~15 PIDs and 3 devices, which is
	// minutes of a bench session producing nothing at all. It is two now,
	// because the persistent case is no longer this mechanism's job — the
	// circuit breaker below takes over after a handful of commands, and
	// what is left here only has to cover the genuine one-slot blip E1.20
	// describes. 250 ms + 500 ms fits inside every profile's
	// CommandDeadline with room to spare.
	ProxyBackoffAttempts = 2
)

// Per-device circuit breaker.
//
// Keyed on (node port, responder UID) — see deviceKey. Two kinds of evidence
// trip it: PROXY_BUFFER_FULL refusals (ProxyBreakerTrip) and silence that is
// attributable to one device because the rest of its port kept answering
// (SilenceBreakerTrip). Silence on its own never does; see ProxyBreakerTrip.
const (
	// ProxyBreakerTrip is how many consecutive commands to one device must
	// fail with ResultProxyBufferFull before the controller stops asking.
	//
	// Three, not one: a device really can hit a momentarily-full buffer on
	// a single command, and tripping on that would make a healthy rig
	// flicker in and out of "unreachable" for no reason. Three consecutive
	// commands, each of which has already exhausted its own backoff, is a
	// device refusing everything rather than one that was briefly busy —
	// which is exactly the LOG3/LOG4 pattern.
	//
	// A response TIMEOUT does not count toward this and does not reset it.
	// That is not an oversight, and the bench has confirmed why.
	//
	// RDM-LOG4 contains a 36-second window in which the EN4 answered nothing
	// at all, to any UID, including the healthy wired proxy that had never
	// once refused. The cause turned out to be the owner power-cycling the
	// node mid-capture. Had a breaker counted bare silence, that reboot
	// would have blacklisted the entire rig — the healthy wired proxy
	// included — and then held every device out through a full cool-down
	// after the node was already back and answering. Someone who reboots a
	// node to fix something would watch the tool go blind for the reboot
	// AND the cool-down, and reasonably conclude the reboot made things
	// worse.
	//
	// PROXY_BUFFER_FULL is a statement a device made about itself. Bare
	// silence is a statement about nothing: a reboot, a pulled cable, a
	// switch renegotiating. TestTimeoutsDoNotOpenTheBreaker guards this
	// against a real event.
	//
	// Silence CAN open the breaker, but only through SilenceBreakerTrip and
	// only with link evidence: a silent command counts against its device
	// solely when some other command on the same node port was answered
	// since that device's previous strike or answer. A dead fixture
	// (RDM-LOG36) is silent while its neighbours answer, so its strikes
	// accumulate. A rebooting node (RDM-LOG4) silences everyone at once:
	// each device can take at most one strike before the answers stop, and
	// no further strike can count until something answers again — at
	// which point the answers reset whoever gave them. So the LOG4
	// reasoning above still holds; it now holds by construction rather
	// than by ignoring silence altogether.
	ProxyBreakerTrip = 3
	// SilenceBreakerTrip is how many link-attributed silent commands to one
	// device open its breaker with CauseNoResponse. "Silent" means the
	// command timed out without a single response packet of any kind; see
	// silentTimeout and noteSilenceLocked for what counts. Three, for the
	// same reason as ProxyBreakerTrip: each strike has already spent its
	// whole retry budget, and three in a row with the port answering
	// around them is a device that is not there.
	SilenceBreakerTrip = 3
	// ProxyBreakerCooldownInitial is how long the first open lasts.
	// Fifteen seconds is long enough to be worth the trouble — it takes the
	// device out of a whole discovery/backfill pass — and short enough that
	// a tech who has just re-seated a radio or powered up a fixture sees it
	// come back while still standing at the rig.
	ProxyBreakerCooldownInitial = 15 * time.Second
	// ProxyBreakerCooldownFactor lengthens the cool-down each time a probe
	// is refused again, so a device that is genuinely gone for the evening
	// costs one probe every couple of minutes rather than one every fifteen
	// seconds.
	ProxyBreakerCooldownFactor = 2
	// ProxyBreakerCooldownMax caps the cool-down. Two minutes keeps a
	// recovered device's worst-case rediscovery inside the time it takes to
	// walk a rig and come back to the laptop.
	ProxyBreakerCooldownMax = 2 * time.Minute
)

// ErrProxyBufferFull reports that one command's proxy kept answering
// NACK PROXY_BUFFER_FULL until its retry budget ran out. It is the sentinel
// behind every ProxyBufferFullError, so callers can tell "the proxy was
// saturated" from "the device refused this PID" with errors.Is.
var ErrProxyBufferFull = errors.New("session: RDM proxy buffer full, retries exhausted")

// ErrDeviceUnreachable reports that a device's proxy circuit breaker is
// open: the controller is deliberately not asking, because the last
// ProxyBreakerTrip commands to this device were all refused.
var ErrDeviceUnreachable = errors.New("session: device not answering through its proxy")

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

// DeviceUnreachableError is the Result.Err of a ResultDeviceUnreachable
// command: nothing was transmitted, because this device's breaker is open.
//
// It carries enough for the UI to say something a lighting tech can act on
// rather than silently dropping the row.
type DeviceUnreachableError struct {
	// UID is the device the controller has stopped asking.
	UID rdm.UID
	// Refusals is how many consecutive commands were refused before the
	// breaker opened.
	Refusals int
	// Opens counts how many times this device's breaker has opened, so a
	// caller can distinguish "first time, probably transient" from
	// "repeatedly gone".
	Opens int
	// RetryAt is when the next probe command will be allowed through.
	RetryAt time.Time
	// Cause says which evidence opened the breaker. The zero value,
	// CauseProxyRefusal, is the original PROXY_BUFFER_FULL meaning.
	Cause UnreachableCause
	// Silences is the consecutive unanswered-command count behind a
	// CauseNoResponse open. Zero for CauseProxyRefusal.
	Silences int
}

// UnreachableCause names the evidence that opened a device's breaker.
type UnreachableCause int

const (
	// CauseProxyRefusal: consecutive commands ended ResultProxyBufferFull.
	CauseProxyRefusal UnreachableCause = iota
	// CauseNoResponse: consecutive commands to this device drew no response
	// at all while other devices on the same node port were answering, so
	// the silence is attributable to this device rather than to the link.
	CauseNoResponse
)

// String renders the cause for JSON and logs.
func (c UnreachableCause) String() string {
	switch c {
	case CauseProxyRefusal:
		return "proxy-refusal"
	case CauseNoResponse:
		return "no-response"
	default:
		return "unknown"
	}
}

func (e *DeviceUnreachableError) Error() string {
	if e.Cause == CauseNoResponse {
		return fmt.Sprintf("session: %s not answering (%d requests in a row unanswered while other devices on the same port answered; next probe at %s)",
			e.UID, e.Silences, e.RetryAt.Format(time.RFC3339))
	}
	return fmt.Sprintf("session: %s not answering through its proxy (%d consecutive refusals; next probe at %s)",
		e.UID, e.Refusals, e.RetryAt.Format(time.RFC3339))
}

// Unwrap makes errors.Is(err, ErrDeviceUnreachable) work.
func (e *DeviceUnreachableError) Unwrap() error { return ErrDeviceUnreachable }

// linkKey identifies the shared RS-485 / wireless path an RDM request
// traverses: one Art-Net node port.
//
// It is no longer a throttling key — per-link pacing was removed once the
// bench data showed request rate was not the variable (see this file's
// header). It survives as half of deviceKey, so that the same UID seen
// through two different node ports gets independent health: a device can be
// reachable on one port's link and refused on another's, and collapsing
// those would let one path's failure hide the other's success.
type linkKey struct {
	ip   netip.Addr
	bind byte
	port uint16
}

func linkKeyFor(n NodeRef) linkKey {
	return linkKey{ip: n.Key.IP, bind: n.Key.BindIndex, port: n.Port.RawValue()}
}

// deviceKey identifies one responder as reached through one node port —
// the granularity at which PROXY_BUFFER_FULL is actually attributable.
type deviceKey struct {
	link linkKey
	uid  rdm.UID
}

func deviceKeyFor(req Request) deviceKey {
	return deviceKey{link: linkKeyFor(req.Node), uid: req.UID}
}

// deviceHealth is one responder's proxy circuit breaker.
//
// A directly-wired device that never draws a PROXY_BUFFER_FULL never gets
// an entry in the map at all, so the wired path costs one absent-key lookup
// per command and nothing else — no timer, no allocation, no delay.
type deviceHealth struct {
	// refusals counts consecutive commands that ended ResultProxyBufferFull.
	refusals int
	// openUntil is when the breaker's cool-down expires. Zero means the
	// breaker is not currently holding anything back.
	openUntil time.Time
	// cooldown is the duration the NEXT open will use.
	cooldown time.Duration
	// probing is true while a single half-open probe command is in flight.
	probing bool
	// opens counts how many times this breaker has opened.
	opens int
	// since is when the breaker most recently opened.
	since time.Time
	// cause is the evidence behind the most recent open.
	cause UnreachableCause
	// silences counts link-attributed silent commands (SilenceBreakerTrip).
	silences int
	// silenceMark is the port's answer count (linkEvidence.answers) at this
	// device's most recent counted strike or answer. A new strike counts
	// only once the port's count has moved past it.
	silenceMark uint64
	// openAnswers is the port's answer count when the breaker last opened;
	// a silent probe grows the cool-down only if it has moved since.
	openAnswers uint64
	// lastCooldown is the duration the most recent open actually used.
	lastCooldown time.Duration
	// node is the port the breaker was opened through, for the automatic
	// silence probe.
	node NodeRef
	// probeGen and probeTimer drive the automatic silence probe. Every open,
	// close and Stop bumps probeGen, so a timer armed for an earlier state
	// never submits anything.
	probeGen   uint64
	probeTimer Timer
	// collect is what this device was observed to do with a GET
	// QUEUED_MESSAGE probe, so the probe is paid for once per device rather
	// than once per deferral. Unrelated to the breaker; it lives here
	// because deviceKey is already the right granularity for "what have we
	// learned about this responder through this node port". See
	// rdmacktimer.go.
	collect collectMode
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

// deviceHealthLocked returns req's health record, creating it on demand.
func (c *RDMController) deviceHealthLocked(req Request) *deviceHealth {
	k := deviceKeyFor(req)
	h := c.devices[k]
	if h == nil {
		h = &deviceHealth{cooldown: ProxyBreakerCooldownInitial}
		c.devices[k] = h
	}
	return h
}

// breakerHoldLocked decides whether a command may go to the wire.
//
// It returns a non-nil error exactly when the command must be failed
// immediately without transmitting. As a side effect it performs the
// open→half-open transition: once the cool-down has expired, the next
// command through is admitted as the single probe.
func (c *RDMController) breakerHoldLocked(req Request, now time.Time) *DeviceUnreachableError {
	h := c.devices[deviceKeyFor(req)]
	if h == nil || h.openUntil.IsZero() {
		return nil // closed — the common case, and the only one wired gear sees.
	}
	if drainExemptFromBreaker(req) {
		// Checked ahead of the open→half-open transition below, so a drain
		// never consumes the single probe slot: that slot is reserved for
		// real work, and a drain is the thing that might make real work
		// possible again. See drainExemptFromBreaker in rdmqueued.go.
		return nil
	}
	if !now.Before(h.openUntil) {
		// Cool-down expired: admit exactly one probe. openUntil is cleared
		// and probing set, so a refusal of the probe reopens the breaker
		// with a longer cool-down rather than restarting the three-strike
		// count from zero.
		h.openUntil = time.Time{}
		h.probing = true
		return nil
	}
	e := &DeviceUnreachableError{
		UID: req.UID, Refusals: h.refusals, Opens: h.opens, RetryAt: h.openUntil, Cause: h.cause,
	}
	if h.cause == CauseNoResponse {
		e.Silences = h.silences
	}
	return e
}

// noteProxyRefusalLocked records one command that gave up with
// ResultProxyBufferFull, and opens the breaker when the evidence is in.
func (c *RDMController) noteProxyRefusalLocked(req Request, now time.Time) {
	if !drainFeedsBreaker(req) {
		// A refused drain says nothing the breaker does not already know;
		// counting it would let every failed rescue lengthen the sentence.
		// See drainFeedsBreaker in rdmqueued.go.
		return
	}
	h := c.deviceHealthLocked(req)
	h.refusals++
	switch {
	case h.probing:
		// A half-open probe was refused: the device is still gone. Reopen
		// straight away with a longer cool-down — no second three-strike
		// count, because the strike that matters has already been served.
		h.probing = false
		c.openBreakerLocked(h, now, req)
	case h.refusals >= ProxyBreakerTrip:
		c.openBreakerLocked(h, now, req)
	}
}

func (c *RDMController) openBreakerLocked(h *deviceHealth, now time.Time, req Request) {
	h.cause = CauseProxyRefusal
	c.armBreakerLocked(h, now, req, true)
	// The breaker opening is the moment Benny512 gives up on this device,
	// and PROXY_BUFFER_FULL is the one failure with a documented remedy:
	// drain the queue the proxy says it cannot add to. One bounded pass per
	// giving-up event — see maybeAutoDrainLocked and DrainOnProxyRecovery.
	c.maybeAutoDrainLocked(req.Node, req.UID, DrainReasonProxyRecovery)
}

// armBreakerLocked opens h's breaker for one cool-down. grow selects the
// usual escalation (use h.cooldown, then lengthen it for next time); without
// it the open repeats the previous cool-down unchanged, for a silent probe
// that the link gave no evidence about (see noteSilenceLocked).
func (c *RDMController) armBreakerLocked(h *deviceHealth, now time.Time, req Request, grow bool) {
	if h.cooldown <= 0 {
		h.cooldown = ProxyBreakerCooldownInitial
	}
	d := h.cooldown
	if !grow && h.lastCooldown > 0 {
		d = h.lastCooldown
	}
	h.openUntil = now.Add(d)
	h.lastCooldown = d
	h.since = now
	h.opens++
	h.probing = false
	h.node = req.Node
	h.openAnswers = c.linkEvidenceLocked(req.Node).answers
	c.stats.DevicesUnreachable++
	if grow {
		h.cooldown *= ProxyBreakerCooldownFactor
		if h.cooldown > ProxyBreakerCooldownMax {
			h.cooldown = ProxyBreakerCooldownMax
		}
	}
	c.cancelSilenceProbeLocked(h)
	if h.cause == CauseNoResponse {
		c.scheduleSilenceProbeLocked(h, deviceKeyFor(req), d)
	}
}

// noteDeviceRespondedLocked records that the device answered for real — an
// ACK, or a NACK about the request itself. Either proves the proxy found
// room and delivered a response, which is precisely the property the
// breaker measures, so both fully close it and reset the cool-down.
//
// It is also the port's proof of life for the silence breaker: every answer
// on a node port advances that port's answer count, whoever gave it.
func (c *RDMController) noteDeviceRespondedLocked(req Request) {
	ev := c.linkEvidenceLocked(req.Node)
	ev.answers++
	ev.last = req.UID
	h := c.devices[deviceKeyFor(req)]
	if h == nil {
		return
	}
	h.refusals = 0
	h.probing = false
	h.openUntil = time.Time{}
	h.cooldown = ProxyBreakerCooldownInitial
	h.silences = 0
	h.silenceMark = ev.answers
	c.cancelSilenceProbeLocked(h)
}

// DeviceReachability is a snapshot of one responder's proxy breaker, for
// the diagnostics screen and for callers that want to explain a missing row.
type DeviceReachability struct {
	UID rdm.UID
	// Unreachable is true while the breaker is open — the controller is
	// deliberately not sending to this device.
	Unreachable bool
	// Refusals is the consecutive PROXY_BUFFER_FULL command count.
	Refusals int
	// Opens counts how many times the breaker has opened this session.
	Opens int
	// Since is when the breaker most recently opened.
	Since time.Time
	// RetryAt is when the next probe is due.
	RetryAt time.Time
	// Cause is what opened the breaker most recently.
	Cause UnreachableCause
	// Silences is the consecutive link-attributed unanswered-command count.
	Silences int
}

// Reachability reports every responder whose proxy breaker has opened at
// least once this session, whether or not it is open right now. An empty
// slice means nothing has ever been refused — the normal state of a
// directly-wired rig.
func (c *RDMController) Reachability() []DeviceReachability {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Clock.Now()
	out := make([]DeviceReachability, 0, len(c.devices))
	for k, h := range c.devices {
		if h.opens == 0 {
			continue
		}
		out = append(out, DeviceReachability{
			UID:         k.uid,
			Unreachable: !h.openUntil.IsZero() && now.Before(h.openUntil),
			Refusals:    h.refusals, Opens: h.opens,
			Since: h.since, RetryAt: h.openUntil,
			Cause: h.cause, Silences: h.silences,
		})
	}
	return out
}

// onProxyBufferFullLocked handles a NACK PROXY_BUFFER_FULL: the command
// keeps its queue slot and is re-issued after a short growing backoff. Only
// when the attempt cap (or the command deadline) is reached does the command
// fail — with ResultProxyBufferFull, which says why, and which is what feeds
// the per-device breaker from finishLocked.
func (c *RDMController) onProxyBufferFullLocked(cmd *Command, msg *rdm.Message) {
	cmd.proxyRefusals++
	c.stats.ProxyBufferFull++

	if cmd.proxyRefusals > ProxyBackoffAttempts {
		cmd.finalResult.NackReason = rdm.NackProxyBufferFull
		c.finishResponseLocked(cmd, ResultProxyBufferFull,
			&ProxyBufferFullError{Attempts: cmd.proxyRefusals, Waited: cmd.proxyWaited}, msg)
		return
	}

	now := c.cfg.Clock.Now()
	wait := proxyBackoffFor(cmd.proxyRefusals)
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

// --- silence breaker -------------------------------------------------------
//
// RDM-LOG36: three Martin ERA 800s on one port of node 2.11.90.6, one of them
// (4D50:00115938) dead — 123 requests, zero replies — while the other two
// answered every request. Commands are serialised one in flight per node
// port, so each silent command held the port for its whole retry budget and
// the healthy fixtures starved. Owner decision (Dom, 2026-10-06): a fixture
// that stops answering is moved to the end of the queue, marked, and
// rechecked now and then. The mechanism is the breaker above with a second
// kind of evidence; see ProxyBreakerTrip for why that evidence must be
// link-attributed and SilenceBreakerTrip for the threshold.

// linkEvidence is one node port's proof of life: how many commands on it
// have been answered (ACK or NACK), and by whom most recently.
type linkEvidence struct {
	answers uint64
	last    rdm.UID
}

func (c *RDMController) linkEvidenceLocked(n NodeRef) *linkEvidence {
	k := linkKeyFor(n)
	ev := c.links[k]
	if ev == nil {
		ev = &linkEvidence{}
		c.links[k] = ev
	}
	return ev
}

// silentTimeout reports whether a ResultTimeout command is evidence of a
// silent DEVICE: not one response packet of any kind came back (no
// ACK_TIMER, no ACK_OVERFLOW block, no proxy refusal), it was not a
// broadcast (never answered by spec), and it was not a GET QUEUED_MESSAGE
// drain — RDM-LOG8 shows real devices ignoring some of those while answering
// everything else.
func silentTimeout(cmd *Command) bool {
	return cmd.responses == 0 &&
		!cmd.req.UID.IsBroadcast() &&
		!isQueuedMessageRequest(cmd.req)
}

// noteSilenceLocked records one silent command, and opens the breaker with
// CauseNoResponse once SilenceBreakerTrip of them are attributable to this
// device.
//
// A strike counts only if the port has answered something since this
// device's previous strike or answer. Because an answer from this device
// resets it, that answer necessarily came from a neighbour: the port is
// alive and this device is not answering. A node-wide outage yields at most
// one strike per device and then nothing, so it can never trip anyone. A
// strike that does not count changes nothing — it neither counts nor resets.
func (c *RDMController) noteSilenceLocked(req Request, now time.Time) {
	ev := c.linkEvidenceLocked(req.Node)
	k := deviceKeyFor(req)
	h := c.devices[k]
	if h == nil {
		h = &deviceHealth{cooldown: ProxyBreakerCooldownInitial}
		if ev.last == req.UID {
			// This device gave the port's most recent answer, so nothing
			// has answered since it did.
			h.silenceMark = ev.answers
		}
		c.devices[k] = h
	}
	if h.probing && h.cause == CauseNoResponse {
		// A silent half-open probe: still gone, reopen. Lengthen the
		// cool-down only if the port proved itself alive meanwhile;
		// otherwise this probe is no evidence about the device at all (the
		// node may be the thing that is down) and the same cool-down repeats.
		grow := ev.answers > h.openAnswers
		if grow {
			h.silences++
			h.silenceMark = ev.answers
		}
		c.armBreakerLocked(h, now, req, grow)
		return
	}
	if ev.answers <= h.silenceMark {
		return
	}
	h.silences++
	h.silenceMark = ev.answers
	if h.silences >= SilenceBreakerTrip {
		h.cause = CauseNoResponse
		// Deliberately no maybeAutoDrainLocked: draining queued messages is
		// the remedy for PROXY_BUFFER_FULL, not for a device that says
		// nothing at all.
		c.armBreakerLocked(h, now, req, true)
	}
}

// requeueBehind moves every pending command for uid in q behind all the
// pending commands for other UIDs, keeping both groups' own order — "move it
// to the end of the queue and proceed to the next". It runs when a command
// to uid has just finished silent, so a neighbour's request (and with it the
// link evidence the silence breaker needs) gets the port before uid's next.
//
// It does nothing while any pending command's UID is held mid-ACK_OVERFLOW
// by another command: that hold stalls the queue head deliberately, and
// reordering around it could put a held UID at the head ahead of runnable
// work.
func requeueBehind(q *cmdQueue, uid rdm.UID, overflow map[rdm.UID]*Command) {
	if len(q.pending) < 2 {
		return
	}
	for _, p := range q.pending {
		if owner, held := overflow[p.req.UID]; held && owner != p {
			return
		}
	}
	others := make([]*Command, 0, len(q.pending))
	var mine []*Command
	for _, p := range q.pending {
		if p.req.UID == uid {
			mine = append(mine, p)
		} else {
			others = append(others, p)
		}
	}
	if len(mine) == 0 || len(others) == 0 {
		return
	}
	q.pending = append(others, mine...)
}

// --- automatic recheck ------------------------------------------------------

// scheduleSilenceProbeLocked arms the automatic probe for a silence-open
// breaker d from now, replacing any earlier one.
func (c *RDMController) scheduleSilenceProbeLocked(h *deviceHealth, k deviceKey, d time.Duration) {
	c.cancelSilenceProbeLocked(h)
	if c.stopped {
		return
	}
	gen := h.probeGen
	h.probeTimer = c.cfg.Clock.AfterFunc(d, func() { c.onSilenceProbeDue(k, gen) })
}

// cancelSilenceProbeLocked retires any armed probe; the generation bump also
// disarms a callback that has already been dequeued and is waiting for c.mu.
func (c *RDMController) cancelSilenceProbeLocked(h *deviceHealth) {
	h.probeGen++
	if h.probeTimer != nil {
		h.probeTimer.Stop()
		h.probeTimer = nil
	}
}

// stopSilenceProbesLocked retires every armed probe, for Stop.
func (c *RDMController) stopSilenceProbesLocked() {
	for _, h := range c.devices {
		c.cancelSilenceProbeLocked(h)
	}
}

// onSilenceProbeDue is the controller rechecking a silent device on its own,
// so a fixture that comes back is picked up without any caller having to ask
// it something. It runs on the clock's goroutine and takes c.mu itself.
//
// The probe is GET DEVICE_INFO — E1.20-required, so never gated on
// SUPPORTED_PARAMETERS — submitted through the normal path, so its result
// reaches the event stream like any command's, and it is admitted as the
// breaker's single half-open probe exactly as a caller's command would be.
func (c *RDMController) onSilenceProbeDue(k deviceKey, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.devices[k]
	if c.stopped || h == nil || h.probeGen != gen {
		return
	}
	h.probeTimer = nil
	if h.cause != CauseNoResponse || h.openUntil.IsZero() || h.probing {
		return // closed, or a probe is already out; its outcome reschedules.
	}
	now := c.cfg.Clock.Now()
	if now.Before(h.openUntil) {
		c.scheduleSilenceProbeLocked(h, k, h.openUntil.Sub(now))
		return
	}
	if c.commandForLocked(h.node, k.uid) || !c.todListsLocked(h.node, k.uid) {
		// Either a command for this device is already queued (it becomes the
		// probe when it reaches the wire) or the node no longer lists the
		// device. Look again after another cool-down, without sending.
		c.scheduleSilenceProbeLocked(h, k, h.lastCooldown)
		return
	}
	c.submitLocked(Request{Node: h.node, UID: k.uid, CommandClass: rdm.GetCommand, PID: rdm.PIDDeviceInfo})
}

// commandForLocked reports whether a command for uid through node's port is
// in flight or pending.
func (c *RDMController) commandForLocked(node NodeRef, uid rdm.UID) bool {
	q := c.queues[c.queueKeyFor(Request{Node: node, UID: uid})]
	if q == nil {
		return false
	}
	link := linkKeyFor(node)
	is := func(cmd *Command) bool {
		return cmd != nil && cmd.req.UID == uid && linkKeyFor(cmd.req.Node) == link
	}
	if is(q.inflight) {
		return true
	}
	for _, p := range q.pending {
		if is(p) {
			return true
		}
	}
	return false
}

// todListsLocked reports whether the cached Table of Devices for node's port
// lists uid.
func (c *RDMController) todListsLocked(node NodeRef, uid rdm.UID) bool {
	e := c.tod[todKey{ip: node.Key.IP, port: node.Port.RawValue()}]
	if e == nil {
		return false
	}
	for _, u := range e.uids {
		if u == uid {
			return true
		}
	}
	return false
}

// silenceProbeOnToDLocked makes the probe due at once for every silence-open
// device that a fresh Table of Devices lists: the node has just discovered
// it, which is what power-cycling a fixture produces, and that is how a tech
// fixes one. Waiting out a cool-down of up to ProxyBreakerCooldownMax after
// that would read as the fix not having worked.
func (c *RDMController) silenceProbeOnToDLocked(key todKey, uids []rdm.UID) {
	if len(c.devices) == 0 || len(uids) == 0 {
		return
	}
	now := c.cfg.Clock.Now()
	for k, h := range c.devices {
		if k.link.ip != key.ip || k.link.port != key.port {
			continue
		}
		if h.cause != CauseNoResponse || h.openUntil.IsZero() || h.probing {
			continue
		}
		listed := false
		for _, u := range uids {
			if u == k.uid {
				listed = true
				break
			}
		}
		if !listed {
			continue
		}
		if now.Before(h.openUntil) {
			h.openUntil = now
		}
		c.scheduleSilenceProbeLocked(h, k, 0)
	}
}
