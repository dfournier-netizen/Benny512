package session

import (
	"errors"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// PROXY_BUFFER_FULL rules under test (E1.20 table A-17, and four rounds of
// EN4 + Moonlite CRMX bench logs):
//
//   - NR_PROXY_BUFFER_FULL means "retry later", so one command is re-issued
//     a couple of times rather than completed;
//   - each re-issue waits longer than the last;
//   - a command that still never gets through fails as
//     ResultProxyBufferFull, so "the proxy was saturated" is never mistaken
//     for "the device refused this PID";
//   - once a device has failed ProxyBreakerTrip commands in a row, the
//     controller stops asking it for a cool-down and says so — instead of
//     spending a full retry budget per PID forever;
//   - a HEALTHY device sharing that same node port is not slowed down at
//     all, which is the round-3 pacing regression this replaces;
//   - a device that starts answering again is picked up on the next probe;
//   - a link that has never refused is untouched — no state, no timers, no
//     delay.

// proxyProfile leaves room for the full backoff schedule, so the command
// deadline is not what ends these tests. The deadline interaction has its own
// test below.
var proxyProfile = TimeoutProfile{
	Name:            "proxy-test",
	ResponseTimeout: 100 * time.Millisecond,
	Retries:         2,
	MaxAckTimer:     500 * time.Millisecond,
	CommandDeadline: 5 * time.Minute,
}

func newProxyHarness(t *testing.T, scope SerializationScope) *rdmHarness {
	t.Helper()
	return newRDMHarness(t, RDMConfig{DefaultProfile: proxyProfile, Scope: scope})
}

func proxyNack() reply {
	return reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackProxyBufferFull)}
}

func (h *rdmHarness) lastSendTime() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sentAt) == 0 {
		h.t.Fatal("nothing has been sent")
	}
	return h.sentAt[len(h.sentAt)-1]
}

// sendsPerCommand is how many requests one command puts on the wire before it
// gives up: the first attempt plus every backoff re-issue.
const sendsPerCommand = ProxyBackoffAttempts + 1

// tripBreaker drives enough wholly-refused commands at uid to open its
// circuit breaker, and returns how many requests that cost.
func (h *rdmHarness) tripBreaker(t *testing.T, uid rdm.UID) int {
	t.Helper()
	for i := 0; i < ProxyBreakerTrip*sendsPerCommand; i++ {
		h.script(proxyNack())
	}
	for i := 0; i < ProxyBreakerTrip; i++ {
		cmd := h.ctrl.Get(h.node, uid, rdm.PIDDeviceInfo, nil)
		res := h.awaitResult(cmd, 30*time.Second)
		if res.Kind != ResultProxyBufferFull {
			t.Fatalf("command %d kind = %v (err %v), want proxy-buffer-full", i, res.Kind, res.Err)
		}
	}
	return h.requestCount()
}

func TestProxyBufferFullIsRetriedNotFailed(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	h.script(
		proxyNack(),
		reply{Type: rdm.ResponseACK, Data: []byte("DEVICE INFO")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)

	// The refusal arrives one latency in (t=10 ms).
	h.clock.Advance(20 * time.Millisecond)
	if res, done := tryResult(cmd); done {
		t.Fatalf("PROXY_BUFFER_FULL completed the command as %v; it means retry later", res.Kind)
	}
	if h.requestCount() != 1 {
		t.Fatalf("requests = %d — the controller re-issued into a full buffer instead of backing off",
			h.requestCount())
	}

	// Still nothing just before the backoff expires (refusal at 10 ms + 250 ms).
	h.clock.Advance(ProxyBackoffInitial - 40*time.Millisecond)
	if h.requestCount() != 1 {
		t.Fatalf("requests = %d before the backoff elapsed, want 1", h.requestCount())
	}

	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack after the proxy drained", res.Kind, res.Err)
	}
	if string(res.Data) != "DEVICE INFO" {
		t.Fatalf("data = %q", res.Data)
	}
	if res.ProxyRefusals != 1 {
		t.Fatalf("ProxyRefusals = %d, want 1", res.ProxyRefusals)
	}
	if res.NackReason != 0 {
		t.Fatalf("NackReason = 0x%04X on a successful command, want 0", uint16(res.NackReason))
	}
	if st := h.ctrl.Stats(); st.ProxyBufferFull != 1 || st.Nacks != 1 {
		t.Fatalf("stats ProxyBufferFull=%d Nacks=%d, want 1/1", st.ProxyBufferFull, st.Nacks)
	}
}

func TestProxyBufferFullBackoffGrowsPerRefusal(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	h.script(proxyNack(), proxyNack(), reply{Type: rdm.ResponseACK})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, 5*time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack on the third attempt", res.Kind, res.Err)
	}

	// Each request follows its refusal (one latency after the previous send)
	// by the backoff for that refusal: 250 ms, then 500 ms.
	gaps := h.sendGaps()
	want := []time.Duration{
		h.latency + ProxyBackoffInitial,
		h.latency + ProxyBackoffInitial*ProxyBackoffFactor,
	}
	if len(gaps) != len(want) {
		t.Fatalf("send gaps = %v, want %d of them", gaps, len(want))
	}
	for i, w := range want {
		if gaps[i] != w {
			t.Fatalf("gap %d = %v, want %v (backoff must grow, not repeat)", i, gaps[i], w)
		}
	}
}

// TestRefusedDeviceDoesNotSlowAHealthyNeighbour is the round-4 regression
// test, and it asserts the exact opposite of what the round-3 build did.
//
// Round 3 keyed its pause on the node port, reasoning that a shared proxy
// buffer is a shared resource. The bench measured the cost of that: on a rig
// where all three devices sat on one EN4 port, the LumenRadio proxy — which
// answered 96 of 96 requests with zero refusals — was throttled down to 12
// requests because the two devices *behind* it were refusing. The healthy
// device must pay nothing for a sick neighbour.
func TestRefusedDeviceDoesNotSlowAHealthyNeighbour(t *testing.T) {
	// ScopeUID puts uidA and uidB in separate queues; they still share one
	// node port, which is what the removed pacing keyed on.
	h := newProxyHarness(t, ScopeUID)
	h.script(
		proxyNack(),                  // uidA's first attempt is refused
		reply{Type: rdm.ResponseACK}, // uidB answers straight away
		proxyNack(), proxyNack(),     // uidA's remaining attempts
	)

	h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	h.clock.Advance(20 * time.Millisecond) // uidA refused at t=10 ms

	submitted := h.clock.Now()
	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d — the healthy device's command was held back by its neighbour's refusal",
			h.requestCount())
	}
	if got := h.lastSendTime(); !got.Equal(submitted) {
		t.Fatalf("healthy device was delayed by %v, want no delay at all", got.Sub(submitted))
	}
	if got := h.requestAt(1).DestinationUID; got != uidB {
		t.Fatalf("second request went to %v, want %v", got, uidB)
	}
	if res := h.awaitResult(b, 2*time.Second); res.Kind != ResultAck {
		t.Fatalf("uidB kind = %v, want ack", res.Kind)
	}
}

// TestOpenBreakerDoesNotStopANeighbourOnTheSameLink is the same property one
// step further along: not merely "not slowed while refusing", but "not
// affected at all once we have given up on the neighbour entirely".
func TestOpenBreakerDoesNotStopANeighbourOnTheSameLink(t *testing.T) {
	h := newProxyHarness(t, ScopeUID)
	sent := h.tripBreaker(t, uidA)

	// uidA is now unreachable. uidB shares the port and is healthy.
	h.script(reply{Type: rdm.ResponseACK})
	submitted := h.clock.Now()
	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != sent+1 {
		t.Fatalf("requests = %d, want %d — the healthy device did not get its request out",
			h.requestCount(), sent+1)
	}
	if got := h.lastSendTime(); !got.Equal(submitted) {
		t.Fatalf("healthy device delayed by %v while its neighbour was unreachable, want 0", got.Sub(submitted))
	}
	if res := h.awaitResult(b, time.Second); res.Kind != ResultAck {
		t.Fatalf("uidB kind = %v (err %v), want ack", res.Kind, res.Err)
	}
}

// TestRepeatedRefusalsOpenTheBreakerAndStopSending is the budget test: a
// device that refuses everything must stop costing the session anything.
func TestRepeatedRefusalsOpenTheBreakerAndStopSending(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	sent := h.tripBreaker(t, uidA)

	if want := ProxyBreakerTrip * sendsPerCommand; sent != want {
		t.Fatalf("requests before the breaker opened = %d, want %d", sent, want)
	}

	// From here every command must fail instantly, with no wire traffic and
	// no simulated time spent.
	before := h.clock.Now()
	for i := 0; i < 10; i++ {
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		res, done := tryResult(cmd)
		if !done {
			t.Fatalf("command %d to an unreachable device did not complete immediately", i)
		}
		if res.Kind != ResultDeviceUnreachable {
			t.Fatalf("command %d kind = %v (err %v), want device-unreachable", i, res.Kind, res.Err)
		}
		if !errors.Is(res.Err, ErrDeviceUnreachable) {
			t.Fatalf("err = %v, want it to wrap ErrDeviceUnreachable", res.Err)
		}
		// Nothing has been settled about the PID, so this must not be
		// mistakable for the device's own answer.
		var nerr *NackError
		if errors.As(res.Err, &nerr) {
			t.Fatalf("err = %v also matches *NackError; a device we never asked has said nothing", res.Err)
		}
	}
	if h.requestCount() != sent {
		t.Fatalf("requests = %d, want %d — the controller kept asking a device it had given up on",
			h.requestCount(), sent)
	}
	if !h.clock.Now().Equal(before) {
		t.Fatalf("ten suppressed commands consumed %v of budget, want none", h.clock.Now().Sub(before))
	}

	var due *DeviceUnreachableError
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res, _ := tryResult(cmd)
	if !errors.As(res.Err, &due) {
		t.Fatalf("err = %v, want *DeviceUnreachableError", res.Err)
	}
	if due.UID != uidA {
		t.Fatalf("DeviceUnreachableError.UID = %v, want %v", due.UID, uidA)
	}
	if due.Refusals < ProxyBreakerTrip {
		t.Fatalf("Refusals = %d, want at least %d", due.Refusals, ProxyBreakerTrip)
	}
	if !due.RetryAt.After(h.clock.Now()) {
		t.Fatalf("RetryAt = %v is not in the future; the UI has nothing to promise the user",
			due.RetryAt)
	}
	if got := h.ctrl.Stats().DevicesUnreachable; got != 1 {
		t.Fatalf("Stats().DevicesUnreachable = %d, want 1", got)
	}
}

// TestBreakerProbesAndRecoversWhenTheDeviceAnswers covers the half-open
// transition: an unreachable device must not be blacklisted for the process
// lifetime, it must be picked up again as soon as it starts answering.
func TestBreakerProbesAndRecoversWhenTheDeviceAnswers(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	sent := h.tripBreaker(t, uidA)

	// Just before the cool-down expires, still suppressed.
	h.clock.Advance(ProxyBreakerCooldownInitial - time.Millisecond)
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res, _ := tryResult(cmd); res.Kind != ResultDeviceUnreachable {
		t.Fatalf("kind = %v before the cool-down expired, want device-unreachable", res.Kind)
	}
	if h.requestCount() != sent {
		t.Fatal("a probe went out before the cool-down expired")
	}

	// Cool-down over: exactly one probe is admitted, and the device answers.
	h.clock.Advance(time.Millisecond)
	h.script(reply{Type: rdm.ResponseACK, Data: []byte("BACK")})
	probe := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != sent+1 {
		t.Fatalf("requests = %d, want %d — the probe was not admitted after the cool-down",
			h.requestCount(), sent+1)
	}
	res := h.awaitResult(probe, time.Second)
	if res.Kind != ResultAck || string(res.Data) != "BACK" {
		t.Fatalf("probe kind = %v data = %q, want ack/BACK", res.Kind, res.Data)
	}

	// Recovered: full speed, no residual delay and no residual suppression.
	for i := 0; i < 3; i++ {
		h.script(reply{Type: rdm.ResponseACK})
		submitted := h.clock.Now()
		c := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if got := h.lastSendTime(); !got.Equal(submitted) {
			t.Fatalf("post-recovery command %d delayed by %v, want 0", i, got.Sub(submitted))
		}
		if r := h.awaitResult(c, time.Second); r.Kind != ResultAck {
			t.Fatalf("post-recovery command %d kind = %v, want ack", i, r.Kind)
		}
	}

	reach := h.ctrl.Reachability()
	if len(reach) != 1 {
		t.Fatalf("Reachability() = %v, want one entry recording that uidA had trouble", reach)
	}
	if reach[0].Unreachable {
		t.Fatal("Reachability() still reports uidA unreachable after it answered")
	}
	if reach[0].Opens != 1 {
		t.Fatalf("Opens = %d, want 1", reach[0].Opens)
	}
}

// TestRefusedProbeReopensTheBreakerForLonger: a device that is genuinely gone
// must not cost one probe every cool-down forever at the same rate.
func TestRefusedProbeReopensTheBreakerForLonger(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	sent := h.tripBreaker(t, uidA)

	h.clock.Advance(ProxyBreakerCooldownInitial)
	// The probe is admitted and refused for its whole (short) budget.
	for i := 0; i < sendsPerCommand; i++ {
		h.script(proxyNack())
	}
	probe := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(probe, 30*time.Second); res.Kind != ResultProxyBufferFull {
		t.Fatalf("probe kind = %v, want proxy-buffer-full", res.Kind)
	}
	if h.requestCount() != sent+sendsPerCommand {
		t.Fatalf("requests = %d, want %d — the probe should be exactly one command's worth",
			h.requestCount(), sent+sendsPerCommand)
	}

	// Reopened. The second cool-down must be longer than the first, so the
	// probe rate falls off rather than repeating every 15 s indefinitely.
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res, done := tryResult(cmd)
	if !done || res.Kind != ResultDeviceUnreachable {
		t.Fatalf("kind = %v done = %v, want an immediate device-unreachable", res.Kind, done)
	}
	var due *DeviceUnreachableError
	if !errors.As(res.Err, &due) {
		t.Fatalf("err = %v, want *DeviceUnreachableError", res.Err)
	}
	if got := due.RetryAt.Sub(h.clock.Now()); got <= ProxyBreakerCooldownInitial {
		t.Fatalf("second cool-down = %v, want longer than the first (%v)", got, ProxyBreakerCooldownInitial)
	}
	if due.Opens != 2 {
		t.Fatalf("Opens = %d, want 2", due.Opens)
	}
	if got := h.ctrl.Stats().DevicesUnreachable; got != 2 {
		t.Fatalf("Stats().DevicesUnreachable = %d, want 2", got)
	}
}

// TestTimeoutsDoNotOpenTheBreaker guards a real bench event, not a
// hypothetical one.
//
// RDM-LOG4 contains a 36-second window in which the EN4 answered nothing to
// anybody — including the wired proxy that had never once refused. The cause
// was the owner power-cycling the node mid-capture. A breaker that counted
// silence would have written off the whole rig for that reboot, then sat out
// a full cool-down after the node was already back and answering. Only an
// explicit PROXY_BUFFER_FULL is a statement about a device.
func TestTimeoutsDoNotOpenTheBreaker(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	// No script at all: every request goes unanswered, exactly as in the
	// silent window.
	const commands = ProxyBreakerTrip + 3
	for i := 0; i < commands; i++ {
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 10*time.Second); res.Kind != ResultTimeout {
			t.Fatalf("command %d kind = %v, want timeout", i, res.Kind)
		}
	}
	// Every one of them must have reached the wire — retries included.
	if want := commands * (proxyProfile.Retries + 1); h.requestCount() != want {
		t.Fatalf("requests = %d, want %d — silence was mistaken for a device refusing",
			h.requestCount(), want)
	}
	if got := h.ctrl.Stats().DevicesUnreachable; got != 0 {
		t.Fatalf("Stats().DevicesUnreachable = %d, want 0 — a silent node is not an unreachable device", got)
	}
	if reach := h.ctrl.Reachability(); len(reach) != 0 {
		t.Fatalf("Reachability() = %v, want empty", reach)
	}

	// And the node coming back needs no cool-down to wait out.
	h.script(reply{Type: rdm.ResponseACK})
	submitted := h.clock.Now()
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if got := h.lastSendTime(); !got.Equal(submitted) {
		t.Fatalf("first command after the silence was delayed %v, want 0", got.Sub(submitted))
	}
	if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}
}

func TestBreakerOnOnePortDoesNotAffectAnother(t *testing.T) {
	// A device refusing through one node port says nothing about the same
	// gateway's other ports, which have their own wire — and nothing about a
	// different device.
	h := newProxyHarness(t, ScopeNodePort)
	other := nodeRef("2.11.90.2", 1, artnet.PortAddress{Universe: 1})
	sent := h.tripBreaker(t, uidA)

	h.script(reply{Type: rdm.ResponseACK})
	submitted := h.clock.Now()
	b := h.ctrl.Get(other, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != sent+1 {
		t.Fatalf("requests = %d, want %d — port 1 was held by port 0's open breaker",
			h.requestCount(), sent+1)
	}
	if got := h.lastSendTime(); !got.Equal(submitted) {
		t.Fatalf("port 1 request was delayed by %v, want no delay", got.Sub(submitted))
	}
	if res := h.awaitResult(b, time.Second); res.Kind != ResultAck {
		t.Fatalf("port 1 kind = %v, want ack", res.Kind)
	}
}

func TestProxyBufferFullAttemptCapFailsWithADistinguishableReason(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	for i := 0; i < sendsPerCommand; i++ {
		h.script(proxyNack())
	}

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultProxyBufferFull {
		t.Fatalf("kind = %v (err %v), want proxy-buffer-full", res.Kind, res.Err)
	}
	if !errors.Is(res.Err, ErrProxyBufferFull) {
		t.Fatalf("err = %v, want it to wrap ErrProxyBufferFull", res.Err)
	}
	// A saturated path must not look like a device that refused the PID:
	// callers cache the latter as a settled answer.
	var nerr *NackError
	if errors.As(res.Err, &nerr) {
		t.Fatalf("err = %v also matches *NackError; the two must stay distinguishable", res.Err)
	}
	var perr *ProxyBufferFullError
	if !errors.As(res.Err, &perr) {
		t.Fatalf("err = %v, want *ProxyBufferFullError", res.Err)
	}
	if perr.Attempts != sendsPerCommand {
		t.Fatalf("Attempts = %d, want %d", perr.Attempts, sendsPerCommand)
	}
	if perr.DeadlineCut {
		t.Fatal("DeadlineCut set although the attempt cap was what ended the command")
	}
	if res.NackReason != rdm.NackProxyBufferFull {
		t.Fatalf("NackReason = 0x%04X, want PROXY_BUFFER_FULL", uint16(res.NackReason))
	}
	if res.ProxyRefusals != sendsPerCommand {
		t.Fatalf("ProxyRefusals = %d, want %d", res.ProxyRefusals, sendsPerCommand)
	}
	if h.requestCount() != sendsPerCommand {
		t.Fatalf("requests = %d, want %d (one attempt plus %d re-issues)",
			h.requestCount(), sendsPerCommand, ProxyBackoffAttempts)
	}
	if got := h.ctrl.Stats().ProxyBufferFull; got != uint64(sendsPerCommand) {
		t.Fatalf("Stats().ProxyBufferFull = %d, want %d", got, sendsPerCommand)
	}
	// One refused command is not enough to give up on a device.
	if got := h.ctrl.Stats().DevicesUnreachable; got != 0 {
		t.Fatalf("Stats().DevicesUnreachable = %d after one refused command, want 0", got)
	}
}

func TestProxyBufferFullStopsBeforeSleepingPastTheCommandDeadline(t *testing.T) {
	// testProfile's deadline cannot hold the full backoff schedule. The
	// command must give up naming the saturation rather than parking on a
	// timer that is certain to expire into a deadline.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: shortDeadlineProfile})
	for i := 0; i < sendsPerCommand; i++ {
		h.script(proxyNack())
	}

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 5*time.Second)

	if res.Kind != ResultProxyBufferFull {
		t.Fatalf("kind = %v (err %v), want proxy-buffer-full, not a bare deadline", res.Kind, res.Err)
	}
	var perr *ProxyBufferFullError
	if !errors.As(res.Err, &perr) || !perr.DeadlineCut {
		t.Fatalf("err = %v, want *ProxyBufferFullError{DeadlineCut: true}", res.Err)
	}
	if res.Elapsed > shortDeadlineProfile.CommandDeadline {
		t.Fatalf("elapsed = %v, want the command to stop inside its %v deadline",
			res.Elapsed, shortDeadlineProfile.CommandDeadline)
	}
}

// shortDeadlineProfile's CommandDeadline is deliberately smaller than the
// 250 ms + 500 ms backoff schedule, so the deadline is what ends the command.
var shortDeadlineProfile = TimeoutProfile{
	Name:            "short-deadline",
	ResponseTimeout: 100 * time.Millisecond,
	Retries:         1,
	MaxAckTimer:     200 * time.Millisecond,
	CommandDeadline: 400 * time.Millisecond,
}

func TestDeviceWithoutProxyNacksIsNeverThrottledOrTracked(t *testing.T) {
	// The direct-wired case: nothing has ever refused, so nothing is slowed
	// and no per-device state is allocated at all.
	h := newProxyHarness(t, ScopeNodePort)
	for i := 0; i < 5; i++ {
		h.script(reply{Type: rdm.ResponseACK})
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
			t.Fatalf("command %d kind = %v, want ack", i, res.Kind)
		}
	}

	// Back to back: the only gap is the responder's own latency.
	for i, gap := range h.sendGaps() {
		if gap != h.latency {
			t.Fatalf("gap %d = %v, want %v — an unrefused device must not be throttled", i, gap, h.latency)
		}
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending = %d, want 0", got)
	}
	h.ctrl.mu.Lock()
	tracked := len(h.ctrl.devices)
	h.ctrl.mu.Unlock()
	if tracked != 0 {
		t.Fatalf("device health entries = %d, want 0 for a device that never refused", tracked)
	}
	if reach := h.ctrl.Reachability(); len(reach) != 0 {
		t.Fatalf("Reachability() = %v, want empty on a healthy wired rig", reach)
	}
}

func TestOrdinaryNackStaysTerminalAndClosesTheBreaker(t *testing.T) {
	// Only PROXY_BUFFER_FULL is a "try again"; every other reason is the
	// responder's final answer and must complete the command at once. A NACK
	// also proves the proxy delivered a response, so it counts as the device
	// being reachable.
	h := newProxyHarness(t, ScopeNodePort)
	h.script(reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)}, reply{Type: rdm.ResponseACK})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultNack || res.NackReason != rdm.NackUnknownPID {
		t.Fatalf("kind = %v reason = 0x%04X, want nack/UNKNOWN_PID", res.Kind, uint16(res.NackReason))
	}
	if res.ProxyRefusals != 0 {
		t.Fatalf("ProxyRefusals = %d on a device NACK, want 0", res.ProxyRefusals)
	}
	if got := h.ctrl.Stats().ProxyBufferFull; got != 0 {
		t.Fatalf("Stats().ProxyBufferFull = %d, want 0", got)
	}

	submitted := h.clock.Now()
	next := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)
	if got := h.lastSendTime(); !got.Equal(submitted) {
		t.Fatalf("next command was delayed %v by an ordinary NACK, want no delay", got.Sub(submitted))
	}
	if res := h.awaitResult(next, time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}
}

// TestOneGoodAnswerResetsTheRefusalCount: a marginal link that alternates
// between refusing and answering must never accumulate its way to an open
// breaker, because it is in fact working.
func TestOneGoodAnswerResetsTheRefusalCount(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	for round := 0; round < ProxyBreakerTrip*2; round++ {
		// One wholly-refused command...
		for i := 0; i < sendsPerCommand; i++ {
			h.script(proxyNack())
		}
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultProxyBufferFull {
			t.Fatalf("round %d kind = %v, want proxy-buffer-full", round, res.Kind)
		}
		// ...followed by one that gets through.
		h.script(reply{Type: rdm.ResponseACK})
		ok := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(ok, time.Second); res.Kind != ResultAck {
			t.Fatalf("round %d recovery kind = %v, want ack", round, res.Kind)
		}
	}
	if got := h.ctrl.Stats().DevicesUnreachable; got != 0 {
		t.Fatalf("Stats().DevicesUnreachable = %d — a link that keeps answering was written off", got)
	}
}
