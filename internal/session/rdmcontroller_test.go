package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
)

// testProfile keeps simulated durations small so the tests read clearly. The
// real shipped profiles are asserted separately in
// TestShippedTimeoutProfiles.
var testProfile = TimeoutProfile{
	Name:            "test",
	ResponseTimeout: 100 * time.Millisecond,
	Retries:         2,
	MaxAckTimer:     500 * time.Millisecond,
	CommandDeadline: 2 * time.Second,
}

func newHarness(t *testing.T) *rdmHarness {
	t.Helper()
	return newRDMHarness(t, RDMConfig{DefaultProfile: testProfile})
}

// nodeRef builds a second/third node port for the serialization tests.
func nodeRef(ip string, bind byte, port artnet.PortAddress) NodeRef {
	a := addrPort(ip, ArtNetUDPPort)
	return NodeRef{Key: NodeKey{IP: a.Addr(), BindIndex: bind}, Addr: a, Port: port}
}

// --- GET / SET happy paths --------------------------------------------

func TestGetHappyPath(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseACK, Data: []byte("DEVICE INFO")})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if res.Err != nil {
		t.Fatalf("err = %v, want nil", res.Err)
	}
	if string(res.Data) != "DEVICE INFO" {
		t.Fatalf("data = %q", res.Data)
	}
	if res.Blocks != 1 || res.AckTimers != 0 || res.Retransmissions != 0 {
		t.Fatalf("blocks=%d ackTimers=%d retx=%d, want 1/0/0", res.Blocks, res.AckTimers, res.Retransmissions)
	}

	// The request itself must be a well-formed RDM GET wrapped in ArtRdm,
	// unicast to the node.
	sent := h.tr.TakeSent()
	if len(sent) != 1 {
		t.Fatalf("datagrams = %d, want 1", len(sent))
	}
	if sent[0].Dst != h.node.Addr || sent[0].Broadcast {
		t.Fatalf("dst = %v broadcast=%v, want unicast to %v", sent[0].Dst, sent[0].Broadcast, h.node.Addr)
	}
	if got, want := sent[0].Packet.Rdm.Net, h.node.Port.Net; got != want {
		t.Fatalf("ArtRdm Net = %d, want %d", got, want)
	}
	if got, want := sent[0].Packet.Rdm.Address, h.node.Port.SubUni(); got != want {
		t.Fatalf("ArtRdm Address = 0x%02X, want 0x%02X", got, want)
	}
	if got := sent[0].Packet.Rdm.RdmVersion; got != 0x01 {
		t.Fatalf("ArtRdm RdmVer = 0x%02X, want 0x01", got)
	}

	req := h.requestAt(0)
	if req.CommandClass != rdm.GetCommand {
		t.Fatalf("CC = 0x%02X, want GET", byte(req.CommandClass))
	}
	if req.ParameterID != rdm.PIDDeviceInfo {
		t.Fatalf("PID = 0x%04X", uint16(req.ParameterID))
	}
	if req.DestinationUID != uidA {
		t.Fatalf("dest UID = %v, want %v", req.DestinationUID, uidA)
	}
	if req.SourceUID != (rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1}) {
		t.Fatalf("source UID = %v, want the controller UID", req.SourceUID)
	}
	if req.PortIDOrResponseType != 1 {
		t.Fatalf("Port ID = %d, want 1 (0 is not a legal Port ID)", req.PortIDOrResponseType)
	}
}

func TestSetHappyPath(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseACK})

	payload := []byte{0x00, 0x2A} // DMX start address 42
	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXStartAddress, payload)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	req := h.requestAt(0)
	if req.CommandClass != rdm.SetCommand {
		t.Fatalf("CC = 0x%02X, want SET", byte(req.CommandClass))
	}
	if string(req.ParameterData) != string(payload) {
		t.Fatalf("parameter data = %v, want %v", req.ParameterData, payload)
	}
	// A SET is answered by SET_COMMAND_RESPONSE; matching must accept that
	// and not the GET response class.
	if res.Response == nil || res.Response.CommandClass != rdm.SetCommandResponse {
		t.Fatalf("response CC = %v, want SET_COMMAND_RESPONSE", res.Response)
	}
}

func TestSubmitRejectsNonGetSetCommandClass(t *testing.T) {
	h := newHarness(t)
	cmd := h.ctrl.Submit(Request{Node: h.node, UID: uidA, CommandClass: rdm.DiscoveryCommand, PID: rdm.PIDDiscMute})
	res, ok := tryResult(cmd)
	if !ok {
		t.Fatal("bad command class should be rejected synchronously")
	}
	if !errors.Is(res.Err, ErrInvalidCommandClass) {
		t.Fatalf("err = %v, want ErrInvalidCommandClass", res.Err)
	}
	if h.tr.SentCount() != 0 {
		t.Fatal("rejected command must not reach the wire")
	}
}

func TestBroadcastCompletesWithoutWaitingForAResponse(t *testing.T) {
	h := newHarness(t)
	cmd := h.ctrl.Set(h.node, rdm.BroadcastAll, rdm.PIDIdentifyDevice, []byte{0x00})

	res, ok := tryResult(cmd)
	if !ok {
		t.Fatal("broadcast should complete as soon as it is sent (responders never ACK it)")
	}
	if res.Kind != ResultBroadcast {
		t.Fatalf("kind = %v, want broadcast", res.Kind)
	}
	if h.tr.SentCount() != 1 {
		t.Fatalf("datagrams = %d, want 1", h.tr.SentCount())
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending = %d, want 0 (no response is coming)", got)
	}
}

// --- NACK -------------------------------------------------------------

func TestNackDecodesToTypedError(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackWriteProtect)})

	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXStartAddress, []byte{0x00, 0x01})
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultNack {
		t.Fatalf("kind = %v, want nack", res.Kind)
	}
	if res.NackReason != rdm.NackWriteProtect {
		t.Fatalf("reason = 0x%04X, want WRITE_PROTECT", uint16(res.NackReason))
	}
	var nerr *NackError
	if !errors.As(res.Err, &nerr) || nerr.Reason != rdm.NackWriteProtect {
		t.Fatalf("err = %v, want *NackError{WRITE_PROTECT}", res.Err)
	}
	if got := h.ctrl.Stats().Nacks; got != 1 {
		t.Fatalf("Stats().Nacks = %d, want 1", got)
	}
}

func TestNackReasonNameCoversUnknownCodes(t *testing.T) {
	if got := NackReasonName(rdm.NackProxyBufferFull); got != "PROXY_BUFFER_FULL" {
		t.Fatalf("name = %q", got)
	}
	if got := NackReasonName(rdm.NackReason(0x8123)); got != "NACK_0x8123" {
		t.Fatalf("name = %q, want hex rendering for an unmodelled code", got)
	}
}

// --- timeout / retry --------------------------------------------------

func TestTimeoutThenRetryThenSuccess(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Drop: true}, // first attempt lost
		reply{Type: rdm.ResponseACK, Data: []byte{9}}, // retransmission answered
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if res.Retransmissions != 1 {
		t.Fatalf("retransmissions = %d, want 1", res.Retransmissions)
	}
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2", h.requestCount())
	}
	// A retransmission is the SAME transaction: reusing the TN means a late
	// reply to the original attempt still satisfies the command.
	if a, b := h.requestAt(0), h.requestAt(1); a.TransactionNumber != b.TransactionNumber {
		t.Fatalf("retransmission TN %d != original TN %d", b.TransactionNumber, a.TransactionNumber)
	}
}

func TestTimeoutRetriesExhausted(t *testing.T) {
	h := newHarness(t)
	// No script entries at all ⇒ the responder never answers.
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultTimeout {
		t.Fatalf("kind = %v, want timeout", res.Kind)
	}
	if !errors.Is(res.Err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", res.Err)
	}
	if want := 1 + testProfile.Retries; h.requestCount() != want {
		t.Fatalf("requests = %d, want %d (first attempt + %d retries)", h.requestCount(), want, testProfile.Retries)
	}
	if res.Retransmissions != testProfile.Retries {
		t.Fatalf("retransmissions = %d, want %d", res.Retransmissions, testProfile.Retries)
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending after failure = %d, want 0", got)
	}
	if got := h.ctrl.Stats().Timeouts; got != 1 {
		t.Fatalf("Stats().Timeouts = %d, want 1", got)
	}
}

func TestLateResponseToRetriedAttemptStillSatisfiesTheCommand(t *testing.T) {
	h := newHarness(t)
	// The first attempt is answered, but only after the response timeout has
	// already triggered a retransmission — the classic slow-link case.
	h.script(
		reply{Delay: 150 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{1}},
		reply{Drop: true},
	)
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack (late reply must not be discarded)", res.Kind)
	}
	if res.Retransmissions != 1 {
		t.Fatalf("retransmissions = %d, want 1", res.Retransmissions)
	}
}

// --- ACK_TIMER --------------------------------------------------------

// TestAckTimerSingleDeferral: one deferral, continued the way E1.20 says to
// continue it — a GET QUEUED_MESSAGE collection, with the parked answer
// coming back under the PID it was parked for. See rdmacktimer.go for the
// RDM-LOG8 evidence behind the change from a plain re-issue.
func TestAckTimerSingleDeferral(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(20)}, // 20 × 10 ms = 200 ms
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("late but fine")},
	)

	start := h.clock.Now()
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if string(res.Data) != "late but fine" {
		t.Fatalf("data = %q", res.Data)
	}
	if res.AckTimers != 1 {
		t.Fatalf("ackTimers = %d, want 1", res.AckTimers)
	}
	if res.Retransmissions != 0 {
		t.Fatalf("retransmissions = %d, want 0 (an ACK_TIMER is not a lost packet)", res.Retransmissions)
	}
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2", h.requestCount())
	}

	// The continuation must wait out the responder's estimate — not fire at
	// the ordinary 100 ms response timeout.
	if elapsed := h.clock.Now().Sub(start); elapsed < 200*time.Millisecond {
		t.Fatalf("completed after %v, want at least the 200 ms the responder asked for", elapsed)
	}
	// It is a new transaction on the wire, and it is a collection rather than
	// a fresh ask for the same parameter. That difference is the whole fix:
	// a re-issue costs the proxy another buffer slot, a collection frees one.
	if a, b := h.requestAt(0), h.requestAt(1); a.TransactionNumber == b.TransactionNumber {
		t.Fatalf("continuation reused TN %d; it is a new transaction", a.TransactionNumber)
	}
	if got := h.requestAt(1).ParameterID; got != rdm.PIDQueuedMessage {
		t.Fatalf("continuation PID = 0x%04X, want QUEUED_MESSAGE", uint16(got))
	}
	if got := h.ctrl.Stats().AckTimerCollectHits; got != 1 {
		t.Fatalf("AckTimerCollectHits = %d, want 1", got)
	}
	if got := h.ctrl.Stats().AckTimerReissues; got != 0 {
		t.Fatalf("AckTimerReissues = %d, want 0", got)
	}
}

// TestAckTimerRepeatedIsTheNormalWirelessProxyPath walks the RDM-LOG8 shape:
// one command deferred over and over. Every deferral must be continued by a
// collection, never by re-asking for the parameter — twenty re-asks are what
// exhausted the MoonLite2's buffer from a verified cold start.
func TestAckTimerRepeatedIsTheNormalWirelessProxyPath(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(10)},
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(10)},
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(10)},
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(10)},
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte{0xAB}},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if res.AckTimers != 4 {
		t.Fatalf("ackTimers = %d, want 4", res.AckTimers)
	}
	if h.requestCount() != 5 {
		t.Fatalf("requests = %d, want 5", h.requestCount())
	}
	if got := h.ctrl.Stats().AckTimers; got != 4 {
		t.Fatalf("Stats().AckTimers = %d, want 4", got)
	}
	// Exactly one ask for the parameter itself; everything after it is
	// collection. This is the assertion that would have caught the LOG8 leak.
	asks := 0
	for _, r := range h.allRequests() {
		if r.ParameterID == rdm.PIDDeviceInfo {
			asks++
		}
	}
	if asks != 1 {
		t.Fatalf("DEVICE_INFO asked %d times across 4 deferrals, want exactly 1 — each extra ask costs a proxy buffer slot", asks)
	}
	// Every transmission still gets its own transaction number.
	seen := map[byte]bool{}
	for _, r := range h.allRequests() {
		if seen[r.TransactionNumber] {
			t.Fatalf("duplicate TN %d across ACK_TIMER continuations", r.TransactionNumber)
		}
		seen[r.TransactionNumber] = true
	}
}

// TestAckTimerBeyondDeadlineFailsImmediately is about the deadline
// arithmetic, not about how a deferral is continued, so it pins the
// continuation to ReissueOnly. That keeps the test measuring one thing, and
// keeps the escape hatch covered.
func TestAckTimerBeyondDeadlineFailsImmediately(t *testing.T) {
	profile := testProfile
	profile.CommandDeadline = 500 * time.Millisecond
	h := newRDMHarness(t, RDMConfig{DefaultProfile: profile, AckTimerCollect: ReissueOnly})
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(40)}, // 400 ms, fits
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(40)}, // 400 ms more, does not
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultDeadlineExceeded {
		t.Fatalf("kind = %v, want deadline-exceeded", res.Kind)
	}
	if !errors.Is(res.Err, ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", res.Err)
	}
	if res.AckTimers != 2 {
		t.Fatalf("ackTimers = %d, want 2", res.AckTimers)
	}
	// It fails the moment the estimate overruns the budget rather than
	// sleeping into a certain failure.
	if res.Elapsed > 450*time.Millisecond {
		t.Fatalf("elapsed = %v, want failure at the point the estimate overran the budget", res.Elapsed)
	}
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2 (no third issue after the budget ran out)", h.requestCount())
	}
	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("timers pending = %d, want 0", got)
	}
}

func TestUnansweredAckTimerChainStopsAtTheDeadline(t *testing.T) {
	profile := testProfile
	profile.Retries = 0
	profile.CommandDeadline = 400 * time.Millisecond
	// Subject is the deadline bound on an endless chain, so the
	// continuation is pinned; see TestAckTimerBeyondDeadlineFailsImmediately.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: profile, AckTimerCollect: ReissueOnly})
	// An endlessly-deferring responder: every request gets a short
	// ACK_TIMER, forever.
	for i := 0; i < 50; i++ {
		h.script(reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(5)})
	}

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)

	if res.Kind != ResultDeadlineExceeded {
		t.Fatalf("kind = %v, want deadline-exceeded", res.Kind)
	}
	if res.Elapsed > 450*time.Millisecond {
		t.Fatalf("elapsed = %v, want the command bounded near its 400 ms deadline", res.Elapsed)
	}
}

func TestAckTimerDelayIsClampedToProfileCap(t *testing.T) {
	profile := testProfile
	profile.MaxAckTimer = 150 * time.Millisecond
	// Subject is the clamp on the wait, not the continuation.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: profile, AckTimerCollect: ReissueOnly})
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(6000)}, // asks for 60 s
		reply{Type: rdm.ResponseACK},
	)

	start := h.clock.Now()
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if elapsed := h.clock.Now().Sub(start); elapsed > 300*time.Millisecond {
		t.Fatalf("elapsed = %v; a 60 s estimate should have been clamped to the 150 ms cap", elapsed)
	}
}

func TestAckTimerUnitIsConfigurable(t *testing.T) {
	// The 10 ms vs 1 ms ambiguity that this knob existed for is SETTLED, in
	// favour of E1.20's 10 ms: RDM-LOG8's twenty deferrals have inter-response
	// gaps tracking raw x 10 ms plus a 12-30 ms round trip almost exactly
	// (raw=6 -> 88 ms, raw=12 -> 144 ms, raw=30 -> 312 ms). A 1 ms unit would
	// have made every gap a flat ~30 ms. The knob stays as an escape hatch for
	// a responder that disagrees, so it still has to actually change pacing.
	profile := testProfile
	profile.MaxAckTimer = time.Hour
	h := newRDMHarness(t, RDMConfig{DefaultProfile: profile, AckTimerUnit: time.Millisecond, AckTimerCollect: ReissueOnly})
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(300)},
		reply{Type: rdm.ResponseACK},
	)

	start := h.clock.Now()
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)
	if res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}
	elapsed := h.clock.Now().Sub(start)
	if elapsed < 300*time.Millisecond || elapsed > 400*time.Millisecond {
		t.Fatalf("elapsed = %v, want ~300 ms with a 1 ms ACK_TIMER unit", elapsed)
	}
}

// --- response matching ------------------------------------------------

func TestDuplicateResponseIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseACK, Data: []byte{1}, Duplicates: 2})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}
	// Let the duplicates land after completion; they must be discarded,
	// not double-complete or corrupt state.
	h.clock.Advance(time.Second)
	if len(res.Data) != 1 {
		t.Fatalf("data = %v, want a single block", res.Data)
	}
	if got := h.ctrl.Stats().StrayResponses; got != 2 {
		t.Fatalf("StrayResponses = %d, want 2 duplicates counted and dropped", got)
	}
}

func TestResponseWithWrongTransactionNumberIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseACK, TNOffset: 5})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)
	if res.Kind != ResultTimeout {
		t.Fatalf("kind = %v, want timeout (a mismatched TN is not our answer)", res.Kind)
	}
	if got := h.ctrl.Stats().StrayResponses; got == 0 {
		t.Fatal("mismatched-TN response was not counted as stray")
	}
}

func TestResponseFromWrongUIDIsIgnored(t *testing.T) {
	h := newHarness(t)
	other := uidC
	h.script(reply{Type: rdm.ResponseACK, SourceUID: &other})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 2*time.Second)
	if res.Kind != ResultTimeout {
		t.Fatalf("kind = %v, want timeout (right TN, wrong responder)", res.Kind)
	}
}

func TestResponseWithMismatchedPIDAborts(t *testing.T) {
	h := newHarness(t)
	wrong := rdm.PIDDeviceLabel
	h.script(reply{Type: rdm.ResponseACK, PID: &wrong})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultAborted {
		t.Fatalf("kind = %v, want aborted", res.Kind)
	}
	if !errors.Is(res.Err, ErrPIDMismatch) {
		t.Fatalf("err = %v, want ErrPIDMismatch", res.Err)
	}
}

func TestUnknownResponseTypeAborts(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseType(0x7E)})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultAborted || !errors.Is(res.Err, ErrUnknownResponseType) {
		t.Fatalf("kind = %v err = %v, want aborted/ErrUnknownResponseType", res.Kind, res.Err)
	}
}

func TestQueuedMessageCountIsSurfaced(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Type: rdm.ResponseACK, MessageCount: 3})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, time.Second)
	if res.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", res.MessageCount)
	}
	var found bool
	for _, ev := range drainEvents(h.ctrl.Events()) {
		if ev.Kind == EventQueuedMessages && ev.MessageCount == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("no EventQueuedMessages emitted for a responder with messages waiting")
	}
}

func TestReorderedResponsesAreMatchedToTheRightCommand(t *testing.T) {
	// UDP reorders freely; matching is by transaction number, not arrival
	// order, so the second command's reply landing first must not be
	// mistaken for the first command's.
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, Scope: ScopeUID})
	h.script(
		reply{Delay: 80 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte("first")},
		reply{Delay: 20 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte("second")},
	)

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceLabel, nil)

	h.clock.Advance(30 * time.Millisecond)
	rb, done := tryResult(b)
	if !done {
		t.Fatal("the out-of-order reply did not complete its own command")
	}
	if string(rb.Data) != "second" {
		t.Fatalf("B data = %q, want %q", rb.Data, "second")
	}
	if _, done := tryResult(a); done {
		t.Fatal("A completed on B's reply")
	}
	ra := h.awaitResult(a, time.Second)
	if string(ra.Data) != "first" {
		t.Fatalf("A data = %q, want %q", ra.Data, "first")
	}
}

func TestCommandLifecycleEvents(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Drop: true},
		reply{Type: rdm.ResponseACK, Data: []byte{1}},
	)
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v, want ack", res.Kind)
	}

	var sends, completes int
	var final *Result
	for _, ev := range drainEvents(h.ctrl.Events()) {
		switch ev.Kind {
		case EventCommandSent:
			sends++
			if ev.UID != uidA {
				t.Fatalf("sent event UID = %v", ev.UID)
			}
		case EventCommandComplete:
			completes++
			final = ev.Result
		}
	}
	if sends != 2 {
		t.Fatalf("EventCommandSent count = %d, want 2 (attempt + retransmission)", sends)
	}
	if completes != 1 || final == nil || final.Kind != ResultAck {
		t.Fatalf("completion events = %d, result = %+v", completes, final)
	}
}

// --- transaction numbers ----------------------------------------------

func TestTransactionNumberWrapsAt255(t *testing.T) {
	h := newHarness(t)
	h.ctrl.mu.Lock()
	h.ctrl.tn = 254
	h.ctrl.mu.Unlock()

	for i := 0; i < 4; i++ {
		h.script(reply{Type: rdm.ResponseACK})
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
			t.Fatalf("command %d kind = %v, want ack", i, res.Kind)
		}
	}

	want := []byte{254, 255, 0, 1}
	reqs := h.allRequests()
	if len(reqs) != 4 {
		t.Fatalf("requests = %d, want 4", len(reqs))
	}
	for i, w := range want {
		if reqs[i].TransactionNumber != w {
			t.Fatalf("request %d TN = %d, want %d (TN wraps 255→0)", i, reqs[i].TransactionNumber, w)
		}
	}
}

// --- serialization ----------------------------------------------------

func TestSameUIDIsStrictlySerialized(t *testing.T) {
	h := newHarness(t)
	h.script(
		reply{Delay: 60 * time.Millisecond, Type: rdm.ResponseACK, Data: []byte{1}},
		reply{Type: rdm.ResponseACK, Data: []byte{2}},
	)

	first := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	second := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceLabel, nil)

	if h.requestCount() != 1 {
		t.Fatalf("requests in flight = %d, want 1 (one command per UID at a time)", h.requestCount())
	}
	h.clock.Advance(50 * time.Millisecond)
	if h.requestCount() != 1 {
		t.Fatalf("second command started while the first was in flight")
	}

	r1 := h.awaitResult(first, time.Second)
	if r1.Kind != ResultAck || r1.Data[0] != 1 {
		t.Fatalf("first result = %+v", r1)
	}
	r2 := h.awaitResult(second, time.Second)
	if r2.Kind != ResultAck || r2.Data[0] != 2 {
		t.Fatalf("second result = %+v", r2)
	}
	// FIFO: the queue preserves submission order.
	if h.requestAt(0).ParameterID != rdm.PIDDeviceInfo || h.requestAt(1).ParameterID != rdm.PIDDeviceLabel {
		t.Fatal("per-UID queue did not preserve FIFO order")
	}
}

func TestNodePortScopeHoldsOtherUIDsOnTheSamePort(t *testing.T) {
	// Default scope: one physical RS-485 link per node port, so a second
	// responder on that port waits its turn.
	h := newHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(30)},
		// A's deferral is continued by a collection, which returns A's
		// parked DEVICE_INFO. The point here is the port queue, not the
		// continuation; see rdmacktimer_test.go for that.
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID},
		reply{Type: rdm.ResponseACK},
	)

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)

	h.clock.Advance(50 * time.Millisecond) // A is mid-ACK_TIMER
	if h.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1 — UID B must wait for the port", h.requestCount())
	}

	ra := h.awaitResult(a, time.Second)
	if ra.Kind != ResultAck {
		t.Fatalf("A result = %+v", ra)
	}
	rb := h.awaitResult(b, time.Second)
	if rb.Kind != ResultAck {
		t.Fatalf("B result = %+v", rb)
	}
}

func TestUIDScopeLetsDifferentRespondersProceedInParallel(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, Scope: ScopeUID})
	h.script(
		reply{Delay: 60 * time.Millisecond, Type: rdm.ResponseACK},
		reply{Delay: 60 * time.Millisecond, Type: rdm.ResponseACK},
	)

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	b := h.ctrl.Get(h.node, uidB, rdm.PIDDeviceInfo, nil)

	if h.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2 in flight under ScopeUID", h.requestCount())
	}
	if h.awaitResult(a, time.Second).Kind != ResultAck {
		t.Fatal("A did not ack")
	}
	if h.awaitResult(b, time.Second).Kind != ResultAck {
		t.Fatal("B did not ack")
	}
}

func TestNodePortScopeAllowsParallelWorkOnADifferentPort(t *testing.T) {
	h := newHarness(t)
	other := nodeRef("2.11.90.2", 1, artnet.PortAddress{Universe: 1})
	h.script(
		reply{Delay: 20 * time.Millisecond, Type: rdm.ResponseACKTimer, Data: ackTimerData(30)},
		// Port 1's own answer, then the collection that continues port 0's
		// deferral. Both carry DEVICE_INFO, which is what each is really
		// waiting for.
		reply{Delay: 20 * time.Millisecond, Type: rdm.ResponseACK, PID: &deviceInfoPID},
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID},
	)

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	b := h.ctrl.Get(other, uidB, rdm.PIDDeviceInfo, nil)

	if h.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2 — a different node port is a separate queue", h.requestCount())
	}
	if h.awaitResult(b, time.Second).Kind != ResultAck {
		t.Fatal("port 1 command did not complete while port 0 was mid-ACK_TIMER")
	}
	if h.awaitResult(a, 2*time.Second).Kind != ResultAck {
		t.Fatal("port 0 command did not complete")
	}
}

func TestNodeScopeSerializesAcrossPorts(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: testProfile, Scope: ScopeNode})
	other := nodeRef("2.11.90.2", 1, artnet.PortAddress{Universe: 1})
	h.script(
		reply{Delay: 60 * time.Millisecond, Type: rdm.ResponseACK},
		reply{Type: rdm.ResponseACK},
	)

	a := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	b := h.ctrl.Get(other, uidB, rdm.PIDDeviceInfo, nil)
	if h.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1 under ScopeNode", h.requestCount())
	}
	if h.awaitResult(a, time.Second).Kind != ResultAck {
		t.Fatal("A did not ack")
	}
	if h.awaitResult(b, time.Second).Kind != ResultAck {
		t.Fatal("B did not ack")
	}
}

// --- profiles ---------------------------------------------------------

func TestShippedTimeoutProfiles(t *testing.T) {
	if ProfileDirect.ResponseTimeout != 1500*time.Millisecond || ProfileDirect.Retries != 2 {
		t.Fatalf("ProfileDirect = %+v, want 1.5 s / 2 retries", ProfileDirect)
	}
	if ProfileWirelessProxy.ResponseTimeout != 5*time.Second || ProfileWirelessProxy.Retries != 3 {
		t.Fatalf("ProfileWirelessProxy = %+v, want 5 s / 3 retries", ProfileWirelessProxy)
	}
	if ProfileDirect.MaxAckTimer != 10*time.Second || ProfileWirelessProxy.MaxAckTimer != 10*time.Second {
		t.Fatal("both profiles should cap a single ACK_TIMER at 10 s")
	}
}

func TestPerNodeProfileSelection(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: ProfileDirect})
	proxy := nodeRef("2.11.90.9", 1, artnet.PortAddress{})
	h.ctrl.SetNodeProfile(proxy.Key, testProfile)

	// The proxy node's short 100 ms timeout applies; the default 1.5 s does
	// not.
	cmd := h.ctrl.Get(proxy, uidA, rdm.PIDDeviceInfo, nil)
	h.clock.Advance(120 * time.Millisecond)
	if h.requestCount() != 2 {
		t.Fatalf("requests = %d after 120 ms, want 2 (per-node profile ignored?)", h.requestCount())
	}
	h.ctrl.Stop()
	if res, ok := tryResult(cmd); !ok || res.Kind != ResultAborted {
		t.Fatalf("Stop should abort in-flight commands, got %+v ok=%v", res, ok)
	}
}

func TestRequestProfileOverride(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: ProfileDirect})
	p := testProfile
	cmd := h.ctrl.Submit(Request{Node: h.node, UID: uidA, CommandClass: rdm.GetCommand,
		PID: rdm.PIDDeviceInfo, Profile: &p})

	res := h.awaitResult(cmd, time.Second)
	if res.Kind != ResultTimeout {
		t.Fatalf("kind = %v, want timeout inside the overridden 100 ms/2-retry budget", res.Kind)
	}
}

// --- lifecycle --------------------------------------------------------

func TestStopAbortsQueuedAndInFlightCommands(t *testing.T) {
	h := newHarness(t)
	h.script(reply{Delay: time.Second, Type: rdm.ResponseACK})
	inflight := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	queued := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceLabel, nil)

	h.ctrl.Stop()

	for name, cmd := range map[string]*Command{"in-flight": inflight, "queued": queued} {
		res, ok := tryResult(cmd)
		if !ok {
			t.Fatalf("%s command was not completed by Stop", name)
		}
		if res.Kind != ResultAborted || !errors.Is(res.Err, ErrControllerStopped) {
			t.Fatalf("%s result = %+v, want aborted/ErrControllerStopped", name, res)
		}
	}
	after := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res, ok := tryResult(after); !ok || !errors.Is(res.Err, ErrControllerStopped) {
		t.Fatalf("submission after Stop = %+v ok=%v", res, ok)
	}
}

func TestControllerRunPumpsInboundChannel(t *testing.T) {
	clock := NewFakeClock(time.Time{})
	tr := NewFakeTransport()
	ctrl := NewRDMController(RDMConfig{Transport: tr, Clock: clock, DefaultProfile: testProfile})
	t.Cleanup(ctrl.Stop)

	node := NodeRef{Key: NodeKey{IP: nodeIP, BindIndex: 1}, Addr: nodeAddr}
	cmd := ctrl.Get(node, uidA, rdm.PIDDeviceInfo, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ctrl.Run(ctx) }()

	req := rdmRequestsSent(t, tr.TakeSent())[0]
	resp := rdm.Message{
		DestinationUID:       req.SourceUID,
		SourceUID:            uidA,
		TransactionNumber:    req.TransactionNumber,
		PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass:         rdm.GetCommandResponse,
		ParameterID:          rdm.PIDDeviceInfo,
		ParameterData:        []byte{0x42},
	}
	pkt := artnet.EncodeRdmPacket(resp, artnet.DefaultProtocolVersion, 0, 0, false)
	tr.Deliver(Inbound{Data: artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: pkt}), From: nodeAddr})

	select {
	case res := <-cmd.Done():
		if res.Kind != ResultAck || res.Data[0] != 0x42 {
			t.Fatalf("result = %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not deliver the response")
	}
}

func TestAwaitRespectsContextCancellation(t *testing.T) {
	h := newHarness(t)
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cmd.Await(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Await err = %v, want context.Canceled", err)
	}
}
