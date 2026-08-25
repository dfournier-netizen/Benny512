package session

import (
	"errors"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// PROXY_BUFFER_FULL rules under test (E1.20 table A-17, and the EN4 +
// Moonlite CRMX bench log that motivated them):
//   - NR_PROXY_BUFFER_FULL means "retry later", so the command is re-issued
//     rather than completed;
//   - each re-issue waits longer than the last;
//   - the pause covers every command crossing the same node port, because
//     the buffer is shared by everything behind it;
//   - a command that never gets through fails as ResultProxyBufferFull, so
//     "the proxy was saturated" is never mistaken for "the device refused";
//   - the pacing a link learns relaxes again once transactions complete;
//   - a link that has never refused is not paced at all.

// proxyProfile leaves room for the full 250 ms→4 s backoff schedule, so the
// command deadline is not what ends these tests. The deadline interaction has
// its own test below.
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
	h.script(proxyNack(), proxyNack(), proxyNack(), reply{Type: rdm.ResponseACK})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, 5*time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack on the fourth attempt", res.Kind, res.Err)
	}

	// Each request follows its refusal (one latency after the previous send)
	// by the backoff for that refusal: 250 ms, 500 ms, 1 s.
	gaps := h.sendGaps()
	want := []time.Duration{
		h.latency + ProxyBackoffInitial,
		h.latency + ProxyBackoffInitial*ProxyBackoffFactor,
		h.latency + ProxyBackoffInitial*ProxyBackoffFactor*ProxyBackoffFactor,
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

func TestProxyBufferFullPausesOtherCommandsOnTheSameLink(t *testing.T) {
	// ScopeUID puts uidA and uidB in separate queues, which is exactly the
	// case that must NOT let the second command keep hammering: both UIDs sit
	// behind the same node port, so they share the proxy buffer that just
	// reported itself full.
	h := newProxyHarness(t, ScopeUID)
	h.script(proxyNack(), reply{Type: rdm.ResponseACK}, reply{Type: rdm.ResponseACK})

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	h.clock.Advance(20 * time.Millisecond) // uidA is refused at t=10 ms

	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != 1 {
		t.Fatalf("requests = %d — a command for another UID went out during the proxy pause", h.requestCount())
	}

	// The gap runs from the refused request, so uidB starts one gap later.
	h.clock.Advance(ProxyPaceInitial)
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d after the pause elapsed, want 2 (the held command must be released)",
			h.requestCount())
	}
	if got := h.requestAt(1).DestinationUID; got != uidB {
		t.Fatalf("second request went to %v, want %v", got, uidB)
	}
	if res := h.awaitResult(b, 2*time.Second); res.Kind != ResultAck {
		t.Fatalf("uidB kind = %v, want ack", res.Kind)
	}
	if res := h.awaitResult(a, 2*time.Second); res.Kind != ResultAck {
		t.Fatalf("uidA kind = %v, want ack", res.Kind)
	}
}

func TestProxyBufferFullOnOnePortDoesNotPaceAnother(t *testing.T) {
	// The constrained resource is one node port's link. A refusal there says
	// nothing about the gateway's other ports, which have their own wire.
	h := newProxyHarness(t, ScopeNodePort)
	other := nodeRef("2.11.90.2", 1, artnet.PortAddress{Universe: 1})
	h.script(proxyNack(), reply{Type: rdm.ResponseACK}, reply{Type: rdm.ResponseACK})

	h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	h.clock.Advance(20 * time.Millisecond) // port 0 refused

	submitted := h.clock.Now()
	b := h.ctrl.Get(other, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d — port 1 was held by port 0's proxy pause", h.requestCount())
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
	for i := 0; i <= ProxyBackoffAttempts; i++ {
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
	if perr.Attempts != ProxyBackoffAttempts+1 {
		t.Fatalf("Attempts = %d, want %d", perr.Attempts, ProxyBackoffAttempts+1)
	}
	if perr.DeadlineCut {
		t.Fatal("DeadlineCut set although the attempt cap was what ended the command")
	}
	if res.NackReason != rdm.NackProxyBufferFull {
		t.Fatalf("NackReason = 0x%04X, want PROXY_BUFFER_FULL", uint16(res.NackReason))
	}
	if res.ProxyRefusals != ProxyBackoffAttempts+1 {
		t.Fatalf("ProxyRefusals = %d, want %d", res.ProxyRefusals, ProxyBackoffAttempts+1)
	}
	if h.requestCount() != ProxyBackoffAttempts+1 {
		t.Fatalf("requests = %d, want %d (one attempt plus %d re-issues)",
			h.requestCount(), ProxyBackoffAttempts+1, ProxyBackoffAttempts)
	}
	if got := h.ctrl.Stats().ProxyBufferFull; got != uint64(ProxyBackoffAttempts+1) {
		t.Fatalf("Stats().ProxyBufferFull = %d, want %d", got, ProxyBackoffAttempts+1)
	}
}

func TestProxyBufferFullStopsBeforeSleepingPastTheCommandDeadline(t *testing.T) {
	// testProfile's 2 s deadline cannot hold the full backoff schedule. The
	// command must give up naming the saturation rather than parking on a
	// timer that is certain to expire into a deadline.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile})
	for i := 0; i <= ProxyBackoffAttempts; i++ {
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
	if res.Elapsed > testProfile.CommandDeadline {
		t.Fatalf("elapsed = %v, want the command to stop inside its %v deadline",
			res.Elapsed, testProfile.CommandDeadline)
	}
}

func TestProxyPacingRelaxesAfterSustainedSuccess(t *testing.T) {
	h := newProxyHarness(t, ScopeNodePort)
	h.script(proxyNack(), reply{Type: rdm.ResponseACK})

	first := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(first, 2*time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack (pacing is now engaged at %v)", res.Kind, ProxyPaceInitial)
	}

	// Every command from here on succeeds. The gap the link imposes should
	// halve on each run of ProxyPaceRelaxAfter completed transactions, and
	// disappear once it would fall below ProxyPaceMin.
	const runs = 9
	delays := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		h.script(reply{Type: rdm.ResponseACK})
		submitted := h.clock.Now()
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 2*time.Second); res.Kind != ResultAck {
			t.Fatalf("run %d kind = %v, want ack", i, res.Kind)
		}
		delays = append(delays, h.lastSendTime().Sub(submitted))
	}

	for i := 1; i < len(delays); i++ {
		if delays[i] > delays[i-1] {
			t.Fatalf("imposed delays = %v, want them to shrink as the link recovers", delays)
		}
	}
	// The command is submitted one latency after the previous one was sent,
	// so the wait it observes is the gap minus that latency.
	if want := ProxyPaceInitial - h.latency; delays[0] != want {
		t.Fatalf("first delay = %v, want %v (pacing engaged)", delays[0], want)
	}
	halved := ProxyPaceInitial/ProxyPaceRelaxFactor - h.latency
	if !containsDuration(delays, halved) {
		t.Fatalf("delays = %v, want one of them to be the halved gap %v", delays, halved)
	}
	if last := delays[len(delays)-1]; last != 0 {
		t.Fatalf("last delay = %v, want 0 — a recovered link must run at full speed again", last)
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending = %d, want 0 once pacing is dropped", got)
	}
}

func TestLinkWithoutProxyNacksIsNeverPaced(t *testing.T) {
	// The direct-wired case: nothing has ever refused, so nothing is slowed.
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
			t.Fatalf("gap %d = %v, want %v — an unrefused link must not be throttled", i, gap, h.latency)
		}
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending = %d, want 0", got)
	}
	h.ctrl.mu.Lock()
	links := len(h.ctrl.links)
	h.ctrl.mu.Unlock()
	if links != 0 {
		t.Fatalf("link pacing state entries = %d, want 0 for a link that never refused", links)
	}
}

func TestOrdinaryNackStaysTerminalAndDoesNotPace(t *testing.T) {
	// Only PROXY_BUFFER_FULL is a "try again"; every other reason is the
	// responder's final answer and must complete the command at once.
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
		t.Fatalf("next command was delayed %v by an ordinary NACK, want no pacing", got.Sub(submitted))
	}
	if res := h.awaitResult(next, time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}
}

func containsDuration(ds []time.Duration, want time.Duration) bool {
	for _, d := range ds {
		if d == want {
			return true
		}
	}
	return false
}
