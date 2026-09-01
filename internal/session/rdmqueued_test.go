package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"benny512/internal/rdm"
)

// GET QUEUED_MESSAGE rules under test (E1.20 §10.3.1, and the RDM-LOG2..7
// bench captures against a LumenRadio MoonLite2 proxy):
//
//   - a QUEUED_MESSAGE response may carry a different PID than the request,
//     because it delivers some *other* parameter's deferred answer;
//   - no other PID gains that licence, and the licence is spent after one
//     answer, so an ACK_OVERFLOW sequence cannot wander mid-reassembly;
//   - the drain loop ends when the responder reports its queue empty, which
//     E1.20 spells as STATUS_MESSAGES with nothing (or only STATUS_NONE) in
//     it;
//   - a responder that never says "empty" is stopped by the iteration cap
//     rather than spinning forever;
//   - a drain reaches the wire even when the device's proxy circuit breaker
//     is open, because it is the documented remedy for the condition that
//     opened it, and a drain that gets an answer closes the breaker.

var (
	statusMessagesPID = rdm.PIDStatusMessages
	deviceInfoPID     = rdm.PIDDeviceInfo
	sensorValuePID    = rdm.PIDSensorValue
)

func queuedHarness(t *testing.T, policy QueuedMessageDrainPolicy) *rdmHarness {
	t.Helper()
	return newRDMHarness(t, RDMConfig{
		DefaultProfile:     proxyProfile,
		Scope:              ScopeNodePort,
		QueuedMessageDrain: policy,
	})
}

// statusAck is one queued STATUS_MESSAGES message: real content, so the
// drain keeps going.
func statusAck(id uint16) reply {
	return reply{
		Type: rdm.ResponseACK,
		PID:  &statusMessagesPID,
		Data: rdm.EncodeStatusMessages([]rdm.StatusMessage{
			{SubDevice: 0, Type: rdm.StatusWarning, MessageID: id, Value1: 85},
		}),
	}
}

// emptyQueueAck is E1.20's "nothing queued" marker.
func emptyQueueAck() reply {
	return reply{Type: rdm.ResponseACK, PID: &statusMessagesPID, Data: nil}
}

// awaitDrain runs a drain to completion, advancing the fake clock until it
// finishes. No sleeps: the drain's own steps are zero-delay timers and the
// scripted responses land on the harness latency, so every step is driven by
// Advance and nothing waits on wall-clock time.
func (h *rdmHarness) awaitDrain(uid rdm.UID, filter rdm.StatusType, maxIter int, limit time.Duration) DrainResult {
	h.t.Helper()
	ch := make(chan drainOutcome, 1)
	go func() {
		res, err := h.ctrl.DrainQueuedMessages(context.Background(), h.node, uid, filter, maxIter)
		ch <- drainOutcome{res, err}
	}()

	// DrainQueuedMessages registers the pass and schedules its first step
	// before it blocks, so wait for the registration before driving the
	// clock. Without this the advance budget can be spent in full before the
	// goroutine has even run, leaving a scheduled step that nothing will
	// ever fire — a flake, not a failure. Once the pass is registered the
	// whole state machine runs on the clock-advancing goroutine, so the rest
	// is deterministic. This loop is intentionally unbounded (no iteration
	// cap): it can only ever wait for something that is already true or
	// about to become true almost immediately (drainStarted flips before
	// DrainQueuedMessages's registering goroutine does anything else), so
	// there is no "budget" to size, and no way it can spin forever without
	// itself indicating a real hang elsewhere.
	for !h.drainStarted(ch) {
		runtime.Gosched()
	}

	// limit is a real assertion this test makes (the drain must converge
	// within limit of *simulated* protocol time), not a loose "big enough"
	// backstop, so — unlike runHTTPAsync's pumpUntilDone in internal/web —
	// this loop's iteration count intentionally stays tied to limit/step
	// rather than becoming unbounded. What changes is how each iteration
	// yields: a bare runtime.Gosched() only *offers* the drain goroutine a
	// turn, and under contention the offer can go unclaimed for the whole
	// loop (see pumpUntilDone's doc comment in internal/web/device_test.go
	// for a captured example of exactly that starvation). Blocking on
	// either h.tr.SentSignal() or a short real timer guarantees this
	// goroutine actually gives up its own CPU between advances — a real
	// wait, not a hopeful yield — so the drain goroutine gets genuine
	// scheduling opportunities across the full budget regardless of load.
	const step = time.Millisecond
	for elapsed := time.Duration(0); elapsed <= limit; elapsed += step {
		select {
		case o := <-ch:
			return o.res
		default:
		}
		h.clock.Advance(step)
		select {
		case o := <-ch:
			return o.res
		case <-h.tr.SentSignal():
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case o := <-ch:
		return o.res
	default:
	}
	h.t.Fatalf("drain did not complete within %v of simulated time", limit)
	return DrainResult{}
}

// drainOutcome carries a drain's return values off its goroutine.
type drainOutcome struct {
	res DrainResult
	err error
}

// drainStarted reports whether the pass is registered, or has already
// finished without needing the clock at all (a stopped controller).
func (h *rdmHarness) drainStarted(ch chan drainOutcome) bool {
	if h.ctrl.DrainsInFlight() > 0 {
		return true
	}
	return len(ch) > 0
}

// --- the relaxed invariant ------------------------------------------------

// TestQueuedMessageAcceptsADifferentResponsePID is the whole point: E1.20
// says a QUEUED_MESSAGE response carries the queued parameter's PID, so
// answering 0x0020 with 0x0030 is correct behaviour, not a violation.
func TestQueuedMessageAcceptsADifferentResponsePID(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(statusAck(0x0021))

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDQueuedMessage, rdm.EncodeQueuedMessageRequest(rdm.StatusNone))
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if res.ResponsePID != rdm.PIDStatusMessages {
		t.Fatalf("ResponsePID = 0x%04X, want STATUS_MESSAGES 0x%04X",
			uint16(res.ResponsePID), uint16(rdm.PIDStatusMessages))
	}
	if res.Request.PID != rdm.PIDQueuedMessage {
		t.Fatalf("Request.PID = 0x%04X, want QUEUED_MESSAGE", uint16(res.Request.PID))
	}
}

// TestPIDMismatchStillAbortsEveryOtherPID is the guard on the guard: the
// licence above is granted to QUEUED_MESSAGE and to nothing else. A
// DEVICE_INFO request answered with STATUS_MESSAGES is still a responder
// answering a different question, and still aborts.
func TestPIDMismatchStillAbortsEveryOtherPID(t *testing.T) {
	for _, tc := range []struct {
		name string
		pid  rdm.ParameterID
	}{
		{"device-info", rdm.PIDDeviceInfo},
		{"status-messages", rdm.PIDStatusMessages},
		{"sensor-value", rdm.PIDSensorValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := queuedHarness(t, DrainOff)
			// Every one of these is answered with a PID it did not ask for.
			other := rdm.PIDManufacturerLabel
			h.script(reply{Type: rdm.ResponseACK, PID: &other, Data: []byte("nope")})

			cmd := h.ctrl.Get(h.node, uidA, tc.pid, nil)
			res := h.awaitResult(cmd, time.Second)

			if res.Kind != ResultAborted {
				t.Fatalf("kind = %v, want aborted", res.Kind)
			}
			if !errors.Is(res.Err, ErrPIDMismatch) {
				t.Fatalf("err = %v, want ErrPIDMismatch", res.Err)
			}
		})
	}
}

// TestQueuedMessagePIDIsPinnedByTheFirstAnswer: the licence is one degree of
// freedom, not a free-for-all. Once the first block of an ACK_OVERFLOW
// sequence has said which parameter is being delivered, a later block that
// changes its mind still aborts the reassembly — exactly as it would for any
// other PID.
func TestQueuedMessagePIDIsPinnedByTheFirstAnswer(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(
		// First block: QUEUED_MESSAGE answered as STATUS_MESSAGES. Allowed,
		// and it pins the answering PID.
		reply{Type: rdm.ResponseACKOverflow, PID: &statusMessagesPID, Data: []byte("aaaa")},
		// Second block changes PID. Not allowed any more.
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("bbbb")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDQueuedMessage, rdm.EncodeQueuedMessageRequest(rdm.StatusNone))
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAborted {
		t.Fatalf("kind = %v, want aborted", res.Kind)
	}
	if !errors.Is(res.Err, ErrOverflowPIDMismatch) {
		t.Fatalf("err = %v, want ErrOverflowPIDMismatch", res.Err)
	}
}

// TestQueuedMessageAckTimerDoesNotPinThePID: an ACK_TIMER defers, it does not
// answer, so it must not spend the one substitution the transaction is
// allowed. The real answer that follows still gets to change the PID.
func TestQueuedMessageAckTimerDoesNotPinThePID(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(
		// A spec-shaped ACK_TIMER: echoes the request's own PID.
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(2)},
		statusAck(0x0021),
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDQueuedMessage, rdm.EncodeQueuedMessageRequest(rdm.StatusNone))
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if res.AckTimers != 1 {
		t.Fatalf("AckTimers = %d, want 1", res.AckTimers)
	}
	if res.ResponsePID != rdm.PIDStatusMessages {
		t.Fatalf("ResponsePID = 0x%04X, want STATUS_MESSAGES", uint16(res.ResponsePID))
	}
}

// --- the drain loop -------------------------------------------------------

// TestDrainTerminatesOnEmptyQueue is the loop's normal life: collect until
// the responder reports nothing left.
func TestDrainTerminatesOnEmptyQueue(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(statusAck(0x0021), statusAck(0x0022), emptyQueueAck())

	res := h.awaitDrain(uidA, rdm.StatusNone, 0, 5*time.Second)

	if !res.Empty {
		t.Fatalf("Empty = false (err %v), want true — the drain did not see the queue-empty marker", res.Err)
	}
	if res.Truncated {
		t.Fatal("Truncated = true, want false")
	}
	if res.Iterations != 3 {
		t.Fatalf("Iterations = %d, want 3 (two messages plus the empty marker)", res.Iterations)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2 — the empty marker must not be collected as content", len(res.Messages))
	}
	if len(res.Status) != 2 {
		t.Fatalf("Status = %d, want 2", len(res.Status))
	}
	if res.Status[0].MessageID != 0x0021 || res.Status[1].MessageID != 0x0022 {
		t.Fatalf("status ids = %v, want 0x21 then 0x22 in delivery order", res.Status)
	}
	if got := h.ctrl.Stats().QueuedMessagesDrained; got != 2 {
		t.Fatalf("QueuedMessagesDrained = %d, want 2", got)
	}
	if n := h.ctrl.DrainsInFlight(); n != 0 {
		t.Fatalf("DrainsInFlight = %d after completion, want 0", n)
	}
}

// TestDrainStopsAtTheIterationCap is the anti-live-lock guard: a responder
// that hands back a message every time and never reports empty must cost one
// bounded burst, not an unbounded one.
func TestDrainStopsAtTheIterationCap(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	for i := 0; i < 50; i++ {
		h.script(statusAck(uint16(0x0030 + i)))
	}

	res := h.awaitDrain(uidA, rdm.StatusNone, 4, 5*time.Second)

	if !res.Truncated {
		t.Fatal("Truncated = false, want true — the cap did not stop the loop")
	}
	if res.Empty {
		t.Fatal("Empty = true, want false — the responder never said empty")
	}
	if res.Iterations != 4 {
		t.Fatalf("Iterations = %d, want exactly the cap of 4", res.Iterations)
	}
	if len(res.Messages) != 4 {
		t.Fatalf("Messages = %d, want 4", len(res.Messages))
	}
	if h.requestCount() != 4 {
		t.Fatalf("requests on the wire = %d, want 4 — the cap must bound wire traffic, not just bookkeeping", h.requestCount())
	}
}

// TestDrainDefaultCapIsBounded checks the same property through the config
// default rather than an explicit argument.
func TestDrainDefaultCapIsBounded(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{
		DefaultProfile:        proxyProfile,
		QueuedMessageDrain:    DrainOff,
		MaxQueuedMessageDrain: 3,
	})
	for i := 0; i < 20; i++ {
		h.script(statusAck(uint16(0x0040 + i)))
	}

	res := h.awaitDrain(uidA, rdm.StatusNone, 0, 5*time.Second)

	if !res.Truncated || res.Iterations != 3 {
		t.Fatalf("Truncated=%v Iterations=%d, want true/3", res.Truncated, res.Iterations)
	}
}

// TestDrainCollectsNonStatusQueuedPIDs is the capability the old
// repeat-GET-STATUS_MESSAGES workaround could not have: a queued message
// that is a deferred answer to some other parameter entirely.
func TestDrainCollectsNonStatusQueuedPIDs(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(
		reply{Type: rdm.ResponseACK, PID: &sensorValuePID, Data: []byte{0, 0, 0x3D, 0, 0, 0, 0, 0, 0}},
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("device-info-ish")},
		emptyQueueAck(),
	)

	res := h.awaitDrain(uidA, rdm.StatusNone, 0, 5*time.Second)

	if !res.Empty {
		t.Fatalf("Empty = false (err %v), want true", res.Err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(res.Messages))
	}
	if res.Messages[0].PID != rdm.PIDSensorValue {
		t.Fatalf("first queued PID = 0x%04X, want SENSOR_VALUE", uint16(res.Messages[0].PID))
	}
	if res.Messages[1].PID != rdm.PIDDeviceInfo {
		t.Fatalf("second queued PID = 0x%04X, want DEVICE_INFO", uint16(res.Messages[1].PID))
	}
	if len(res.Status) != 0 {
		t.Fatalf("Status = %d, want 0 — neither message was a status report", len(res.Status))
	}
}

// TestDrainOnUnsupportedPIDIsNotAnError: a responder that does not implement
// QUEUED_MESSAGE has given a settled answer, not a failure.
func TestDrainOnUnsupportedPIDIsNotAnError(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.script(reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)})

	res := h.awaitDrain(uidA, rdm.StatusNone, 0, 5*time.Second)

	if !res.Unsupported {
		t.Fatalf("Unsupported = false, want true (kind %v, err %v)", res.LastKind, res.Err)
	}
	if res.Err != nil {
		t.Fatalf("Err = %v, want nil — an UNKNOWN_PID NACK is an answer", res.Err)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1 — an unsupported responder must not be asked twice", res.Iterations)
	}
}

// --- breaker cooperation --------------------------------------------------

// TestSuccessfulDrainClosesTheBreaker is the recovery path end to end, with
// the default policy: three refused commands open the breaker, the opening
// starts one drain, the drain reaches the wire despite the open breaker, its
// answer closes the breaker, and ordinary commands flow again — instead of
// the device sitting out a full cool-down.
func TestSuccessfulDrainClosesTheBreaker(t *testing.T) {
	h := queuedHarness(t, DrainOnProxyRecovery)

	for i := 0; i < ProxyBreakerTrip*sendsPerCommand; i++ {
		h.script(proxyNack())
	}
	// The recovery drain's answer: the queue was holding one stranded
	// message, then it is empty.
	h.script(statusAck(0x0021), emptyQueueAck())

	for i := 0; i < ProxyBreakerTrip; i++ {
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultProxyBufferFull {
			t.Fatalf("command %d kind = %v, want proxy-buffer-full", i, res.Kind)
		}
	}

	// Let the scheduled recovery drain run to completion.
	h.clock.Advance(time.Second)

	// Two QUEUED_MESSAGE requests reached the wire — one per scripted
	// answer — even though the breaker was open the whole time. That is the
	// exemption doing its job; without it nothing would have been sent.
	if got := h.countRequests(rdm.PIDQueuedMessage); got != 2 {
		t.Fatalf("QUEUED_MESSAGE requests on the wire = %d, want 2 — the drain did not get through the open breaker", got)
	}
	if got := h.countRequests(rdm.PIDDeviceInfo); got != ProxyBreakerTrip*sendsPerCommand {
		t.Fatalf("DEVICE_INFO requests = %d, want %d", got, ProxyBreakerTrip*sendsPerCommand)
	}
	if got := h.ctrl.Stats().QueuedDrains; got != 1 {
		t.Fatalf("QueuedDrains = %d, want exactly 1 — one bounded pass per giving-up event", got)
	}

	// The drain got real answers, so the device is reachable again.
	if r := h.reachability(uidA); r.Unreachable {
		t.Fatalf("breaker still open after a successful drain (retry at %v)", r.RetryAt)
	}

	// And ordinary traffic flows again immediately, with no cool-down served.
	h.script(reply{Type: rdm.ResponseACK, Data: []byte("DEVICE INFO")})
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
		t.Fatalf("post-drain command kind = %v (err %v), want ack", res.Kind, res.Err)
	}
}

// TestRefusedDrainDoesNotLengthenTheCooldown: a rescue attempt that is itself
// refused must not extend the sentence. The breaker opens once, on the three
// real commands, and the drain's own refusals are invisible to it.
func TestRefusedDrainDoesNotLengthenTheCooldown(t *testing.T) {
	h := queuedHarness(t, DrainOnProxyRecovery)

	// Everything is refused, drain included.
	for i := 0; i < 64; i++ {
		h.script(proxyNack())
	}

	for i := 0; i < ProxyBreakerTrip; i++ {
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultProxyBufferFull {
			t.Fatalf("command %d kind = %v, want proxy-buffer-full", i, res.Kind)
		}
	}
	opensAfterTrip := h.reachability(uidA).Opens
	retryAfterTrip := h.reachability(uidA).RetryAt

	// Run the recovery drain to exhaustion; every command in it is refused.
	h.clock.Advance(2 * time.Second)

	r := h.reachability(uidA)
	if r.Opens != opensAfterTrip {
		t.Fatalf("breaker opens = %d after a refused drain, want %d — the drain re-opened it",
			r.Opens, opensAfterTrip)
	}
	if !r.RetryAt.Equal(retryAfterTrip) {
		t.Fatalf("RetryAt moved from %v to %v — a refused rescue lengthened the cool-down",
			retryAfterTrip, r.RetryAt)
	}
	if !r.Unreachable {
		t.Fatal("breaker closed after a wholly refused drain, want still open")
	}
}

// TestDrainOffMakesNoAutomaticTraffic is the escape hatch: with the policy
// off, opening the breaker costs nothing extra.
func TestDrainOffMakesNoAutomaticTraffic(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	sent := h.tripBreaker(t, uidA)
	h.clock.Advance(2 * time.Second)

	if got := h.requestCount(); got != sent {
		t.Fatalf("requests = %d, want %d — DrainOff still produced traffic", got, sent)
	}
	if got := h.ctrl.Stats().QueuedDrains; got != 0 {
		t.Fatalf("QueuedDrains = %d, want 0", got)
	}
}

// TestMessageCountTriggersADrainOnlyUnderThatPolicy: E1.20's canonical
// "I have something for you" signal is honoured when asked for, and ignored
// by default — because the bench logs show it is zero on every response this
// rig produces, so defaulting to it would buy nothing and cost traffic.
func TestMessageCountTriggersADrainOnlyUnderThatPolicy(t *testing.T) {
	t.Run("default policy ignores it", func(t *testing.T) {
		h := queuedHarness(t, DrainOnProxyRecovery)
		h.script(reply{Type: rdm.ResponseACK, Data: []byte("x"), MessageCount: 3})

		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.MessageCount != 3 {
			t.Fatalf("MessageCount = %d, want 3", res.MessageCount)
		}
		h.clock.Advance(time.Second)

		if got := h.ctrl.Stats().QueuedDrains; got != 0 {
			t.Fatalf("QueuedDrains = %d, want 0 under DrainOnProxyRecovery", got)
		}
	})

	t.Run("message-count policy drains", func(t *testing.T) {
		h := queuedHarness(t, DrainOnProxyRecoveryAndMessageCount)
		h.script(
			reply{Type: rdm.ResponseACK, Data: []byte("x"), MessageCount: 3},
			statusAck(0x0021),
			emptyQueueAck(),
		)

		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
			t.Fatalf("kind = %v, want ack", res.Kind)
		}
		h.clock.Advance(2 * time.Second)

		if got := h.ctrl.Stats().QueuedDrains; got != 1 {
			t.Fatalf("QueuedDrains = %d, want 1", got)
		}
		if got := h.ctrl.Stats().QueuedMessagesDrained; got != 1 {
			t.Fatalf("QueuedMessagesDrained = %d, want 1", got)
		}
	})
}

// firstQueuedMessageRequest returns the first GET QUEUED_MESSAGE that
// actually reached the wire, so a test can assert on the bytes sent rather
// than on the intent behind them.
func (h *rdmHarness) firstQueuedMessageRequest() rdm.Message {
	h.t.Helper()
	for _, m := range h.allRequests() {
		if m.ParameterID == rdm.PIDQueuedMessage {
			return m
		}
	}
	h.t.Fatalf("no GET QUEUED_MESSAGE reached the wire (saw %d requests)", h.requestCount())
	return rdm.Message{}
}

// queuedMessageRequests returns every GET QUEUED_MESSAGE that actually
// reached the wire, so a test can assert on the bytes sent rather than on
// the intent behind them.
func (h *rdmHarness) queuedMessageRequests() []rdm.Message {
	h.t.Helper()
	var out []rdm.Message
	for _, m := range h.allRequests() {
		if m.ParameterID == rdm.PIDQueuedMessage {
			out = append(out, m)
		}
	}
	return out
}

// assertQueuedFilterByte checks one QUEUED_MESSAGE request's param data is
// the single byte StatusAdvisory, naming the illegal value explicitly when
// it is not. what identifies the caller ("drain", "ACK_TIMER probe").
func assertQueuedFilterByte(t *testing.T, what string, got []byte) {
	t.Helper()
	want := []byte{byte(rdm.StatusAdvisory)}
	if bytes.Equal(got, want) {
		return
	}
	if len(got) == 1 && got[0] == byte(rdm.StatusNone) {
		t.Fatalf("%s sent status_type 0x%02X (STATUS_NONE), want 0x%02X (STATUS_ADVISORY) — "+
			"0x00 is STATUS_MESSAGES' request value and is illegal for QUEUED_MESSAGE; "+
			"real responders answer it with silence (RDM-LOG8)", what, got[0], want[0])
	}
	t.Fatalf("%s param data = % X, want % X", what, got, want)
}

// assertLegalQueuedFilter checks the first drain probe's param data is the
// single byte StatusAdvisory.
func assertLegalQueuedFilter(t *testing.T, h *rdmHarness) {
	t.Helper()
	assertQueuedFilterByte(t, "drain", h.firstQueuedMessageRequest().ParameterData)
}

// assertEveryQueuedFilterLegal pins the filter byte of every QUEUED_MESSAGE
// that reached the wire, and that there were wantProbes of them. Used by the
// ACK_TIMER continuation, where a probe is issued per deferral and each one
// is an independent trip through issueLocked.
func assertEveryQueuedFilterLegal(t *testing.T, h *rdmHarness, what string, wantProbes int) {
	t.Helper()
	got := h.queuedMessageRequests()
	if len(got) != wantProbes {
		t.Fatalf("%s count = %d, want %d", what, len(got), wantProbes)
	}
	for i, m := range got {
		assertQueuedFilterByte(t, fmt.Sprintf("%s %d/%d", what, i+1, len(got)), m.ParameterData)
	}
}

// TestAutoDrainUsesALegalStatusTypeFilter pins the one byte of param data an
// automatic drain puts on the wire.
//
// E1.20 allows only 1=Last Message, 2=Advisory, 3=Warning, 4=Error as
// QUEUED_MESSAGE's request status_type; 0=None is STATUS_MESSAGES' value and
// is out of range for this PID (research doc §2.4). StatusAdvisory (0x02) is
// therefore the lowest legal floor and this PID's way of saying "everything
// you are holding".
//
// The distinction is not academic. RDM-LOG8 captured six auto-drain probes
// carrying 0x00 and got back no response of any kind — not even a NACK —
// from devices answering everything else in the same seconds, which is why
// this asserts the wire byte rather than the drain's bookkeeping. Both
// automatic reasons are covered because each reaches startDrainLocked by its
// own route.
func TestAutoDrainUsesALegalStatusTypeFilter(t *testing.T) {
	t.Run("message-count drain", func(t *testing.T) {
		h := queuedHarness(t, DrainOnProxyRecoveryAndMessageCount)
		h.script(
			reply{Type: rdm.ResponseACK, Data: []byte("x"), MessageCount: 3},
			emptyQueueAck(),
		)

		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
			t.Fatalf("kind = %v, want ack", res.Kind)
		}
		h.clock.Advance(2 * time.Second)

		assertLegalQueuedFilter(t, h)
	})

	t.Run("proxy-recovery drain", func(t *testing.T) {
		h := queuedHarness(t, DrainOnProxyRecovery)
		for i := 0; i < ProxyBreakerTrip*sendsPerCommand; i++ {
			h.script(proxyNack())
		}
		h.script(emptyQueueAck())

		for i := 0; i < ProxyBreakerTrip; i++ {
			cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
			if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultProxyBufferFull {
				t.Fatalf("command %d kind = %v, want proxy-buffer-full", i, res.Kind)
			}
		}
		h.clock.Advance(2 * time.Second)

		assertLegalQueuedFilter(t, h)
	})
}

// TestDrainAfterStopFailsCleanly guards the shutdown path: no goroutine is
// left waiting on a channel nobody will write to.
func TestDrainAfterStopFailsCleanly(t *testing.T) {
	h := queuedHarness(t, DrainOff)
	h.ctrl.Stop()

	_, err := h.ctrl.DrainQueuedMessages(context.Background(), h.node, uidA, rdm.StatusNone, 0)
	if !errors.Is(err, ErrControllerStopped) {
		t.Fatalf("err = %v, want ErrControllerStopped", err)
	}
}

// countRequests counts the requests seen for one PID.
func (h *rdmHarness) countRequests(pid rdm.ParameterID) int {
	n := 0
	for _, m := range h.allRequests() {
		if m.ParameterID == pid {
			n++
		}
	}
	return n
}

// reachability finds one UID's breaker snapshot.
func (h *rdmHarness) reachability(uid rdm.UID) DeviceReachability {
	h.t.Helper()
	for _, r := range h.ctrl.Reachability() {
		if r.UID == uid {
			return r
		}
	}
	h.t.Fatalf("no reachability record for %v", uid)
	return DeviceReachability{}
}
