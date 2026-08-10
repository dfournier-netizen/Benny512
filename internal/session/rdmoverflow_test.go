package session

import (
	"errors"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// ACK_OVERFLOW rules under test (protocol reference §2.4 and §3.5):
//   - the controller re-issues the SAME GET for the SAME PID until the
//     responder finishes with a plain ACK;
//   - Parameter Data accumulates across the blocks;
//   - NOTHING else may be sent to that responder until the sequence ends,
//     or the responder aborts the transfer;
//   - a response for a different PID mid-sequence means the transfer is
//     dead and must be abandoned cleanly.

func TestAckOverflowReassemblesAcrossPackets(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("AAAA")},
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("BBBB")},
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("CCCC")},
		reply{Type: rdm.ResponseACK, Data: []byte("DD")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if got, want := string(res.Data), "AAAABBBBCCCCDD"; got != want {
		t.Fatalf("assembled data = %q, want %q", got, want)
	}
	if res.Blocks != 4 {
		t.Fatalf("blocks = %d, want 4", res.Blocks)
	}
	if h.requestCount() != 4 {
		t.Fatalf("requests = %d, want 4 (one per block)", h.requestCount())
	}
	if got := h.ctrl.Stats().Overflows; got != 1 {
		t.Fatalf("Stats().Overflows = %d, want 1", got)
	}
}

func TestAckOverflowReissuesTheIdenticalRequestWithFreshTransactionNumbers(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKOverflow, Data: []byte{1}},
		reply{Type: rdm.ResponseACKOverflow, Data: []byte{2}},
		reply{Type: rdm.ResponseACK, Data: []byte{3}},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, []byte{0xAB})
	if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}

	reqs := h.allRequests()
	seenTN := map[byte]bool{}
	for i, r := range reqs {
		if r.ParameterID != rdm.PIDSupportedParameters {
			t.Fatalf("request %d PID = 0x%04X, want the same PID throughout", i, uint16(r.ParameterID))
		}
		if r.CommandClass != rdm.GetCommand {
			t.Fatalf("request %d CC changed mid-sequence", i)
		}
		if string(r.ParameterData) != string([]byte{0xAB}) {
			t.Fatalf("request %d parameter data = %v, want the original request echoed", i, r.ParameterData)
		}
		if r.DestinationUID != uidA {
			t.Fatalf("request %d went to %v, want %v", i, r.DestinationUID, uidA)
		}
		if seenTN[r.TransactionNumber] {
			t.Fatalf("request %d reused TN %d; each re-issue is a new transaction", i, r.TransactionNumber)
		}
		seenTN[r.TransactionNumber] = true
	}
}

func TestNothingElseIsSentToAResponderMidOverflow(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACKOverflow, Data: []byte{1}},
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACKOverflow, Data: []byte{2}},
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{3}},
		reply{Type: rdm.ResponseACK, Data: []byte{0xFF}},
	)

	overflowing := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	// Queued behind it, and aimed at the same responder: it must not touch
	// the wire until the overflow sequence resolves.
	other := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceLabel, nil)

	h.clock.Advance(70 * time.Millisecond) // mid-sequence
	for i, r := range h.allRequests() {
		if r.ParameterID != rdm.PIDSupportedParameters {
			t.Fatalf("request %d interleaved PID 0x%04X into the overflow sequence", i, uint16(r.ParameterID))
		}
	}
	if _, done := tryResult(other); done {
		t.Fatal("queued command completed while the overflow sequence was still running")
	}

	if res := h.awaitResult(overflowing, time.Second); res.Kind != ResultAck {
		t.Fatalf("overflow command kind = %v, want ack", res.Kind)
	}
	if res := h.awaitResult(other, time.Second); res.Kind != ResultAck || res.Data[0] != 0xFF {
		t.Fatalf("queued command = %+v, want it to run after the sequence", res)
	}

	// The interleaved command is the LAST request on the wire.
	reqs := h.allRequests()
	if reqs[len(reqs)-1].ParameterID != rdm.PIDDeviceLabel {
		t.Fatalf("final request PID = 0x%04X, want the queued command last", uint16(reqs[len(reqs)-1].ParameterID))
	}
}

func TestOverflowHoldsTheUIDEvenAcrossSeparateQueues(t *testing.T) {
	// Under the default node-port scope the same responder reached through
	// two different node ports lands in two independent queues. The per-UID
	// overflow hold is what still prevents interleaving — this is the case
	// the queue structure alone does not cover.
	h := newHarness(t)
	otherPort := nodeRef("2.11.90.2", 1, artnet.PortAddress{Universe: 1})
	h.script(
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACKOverflow, Data: []byte{1}},
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{2}},
		reply{Type: rdm.ResponseACK, Data: []byte{0xFF}},
	)

	overflowing := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	h.clock.Advance(35 * time.Millisecond) // overflow sequence now active

	sameUIDOtherPort := h.ctrl.Get(otherPort, uidA, rdm.PIDDeviceLabel, nil)
	if got := h.requestCount(); got != 2 {
		t.Fatalf("requests = %d, want 2 — the second port's command must be held, not sent", got)
	}

	if res := h.awaitResult(overflowing, time.Second); res.Kind != ResultAck {
		t.Fatalf("overflow command kind = %v", res.Kind)
	}
	if res := h.awaitResult(sameUIDOtherPort, time.Second); res.Kind != ResultAck {
		t.Fatalf("held command kind = %v, want ack once the hold released", res.Kind)
	}
	reqs := h.allRequests()
	if reqs[len(reqs)-1].ParameterID != rdm.PIDDeviceLabel {
		t.Fatal("held command did not run last")
	}
}

func TestOverflowDoesNotHoldADifferentResponder(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, Scope: ScopeUID})
	h.script(
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACKOverflow, Data: []byte{1}},
		reply{Delay: 10 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{9}},
		reply{Delay: 30 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{2}},
	)

	overflowing := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	h.clock.Advance(35 * time.Millisecond)

	unrelated := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceLabel, nil)
	if res := h.awaitResult(unrelated, time.Second); res.Kind != ResultAck {
		t.Fatalf("a different responder was blocked by UID A's overflow: %+v", res)
	}
	if res := h.awaitResult(overflowing, time.Second); res.Kind != ResultAck {
		t.Fatalf("overflow command kind = %v", res.Kind)
	}
}

func TestOverflowAbortsOnMismatchedPID(t *testing.T) {
	h := newHarness(t)
	wrong := rdm.PIDDeviceLabel
	h.script(
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("AAAA")},
		reply{Type: rdm.ResponseACK, Data: []byte("XXXX"), PID: &wrong},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAborted {
		t.Fatalf("kind = %v, want aborted", res.Kind)
	}
	if !errors.Is(res.Err, ErrOverflowPIDMismatch) {
		t.Fatalf("err = %v, want ErrOverflowPIDMismatch", res.Err)
	}
	// The partial data is reported, but flagged as an abort — the caller
	// must not treat it as a complete parameter value.
	if string(res.Data) != "AAAA" {
		t.Fatalf("partial data = %q, want the blocks received before the abort", res.Data)
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending after abort = %d, want 0 (clean abort)", got)
	}
}

func TestOverflowReleasesTheUIDHoldAfterAnAbort(t *testing.T) {
	h := newHarness(t)
	wrong := rdm.PIDDeviceLabel
	h.script(
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("AAAA")},
		reply{Type: rdm.ResponseACK, PID: &wrong},
		reply{Type: rdm.ResponseACK, Data: []byte{0x77}},
	)

	aborted := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	queued := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceLabel, nil)

	if res := h.awaitResult(aborted, time.Second); res.Kind != ResultAborted {
		t.Fatalf("kind = %v, want aborted", res.Kind)
	}
	res := h.awaitResult(queued, time.Second)
	if res.Kind != ResultAck || res.Data[0] != 0x77 {
		t.Fatalf("queued command after abort = %+v, want it to run normally", res)
	}
}

func TestOverflowTimeoutRetriesTheSamePID(t *testing.T) {
	// A dropped block mid-sequence is an ordinary lost packet: retransmit
	// the same request (same TN), never a different PID.
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKOverflow, Data: []byte("AAAA")},
		reply{Drop: true},
		reply{Type: rdm.ResponseACK, Data: []byte("BB")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if string(res.Data) != "AAAABB" {
		t.Fatalf("data = %q, want AAAABB", res.Data)
	}
	if res.Retransmissions != 1 {
		t.Fatalf("retransmissions = %d, want 1", res.Retransmissions)
	}
	reqs := h.allRequests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	// The retransmission of block 2 reuses block 2's transaction number.
	if reqs[1].TransactionNumber != reqs[2].TransactionNumber {
		t.Fatalf("retransmission TN %d != original %d", reqs[2].TransactionNumber, reqs[1].TransactionNumber)
	}
}

func TestOverflowStopsAtTheCommandDeadline(t *testing.T) {
	profile := testProfile
	profile.CommandDeadline = 200 * time.Millisecond
	h := newRDMHarness(t, RDMConfig{DefaultProfile: profile})
	// A responder that overflows forever.
	for i := 0; i < 200; i++ {
		h.script(reply{Delay: 5 * time.Millisecond, Type: rdm.ResponseACKOverflow, Data: []byte{byte(i)}})
	}

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDSupportedParameters, nil)
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultDeadlineExceeded {
		t.Fatalf("kind = %v, want deadline-exceeded", res.Kind)
	}
	if res.Elapsed > 250*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded near the 200 ms deadline", res.Elapsed)
	}
	if got := h.ctrl.Stats().Overflows; got != 1 {
		t.Fatalf("Stats().Overflows = %d, want 1", got)
	}
}
