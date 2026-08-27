package session

import (
	"errors"
	"testing"
	"time"

	"benny512/internal/rdm"
)

// The ACK_TIMER continuation, under test.
//
// RDM-LOG8 is the reference shape: from a verified cold start (power-cycled
// MoonLite2 pair), one command is deferred over and over, the responder's own
// estimate climbs as its queue grows, and after twenty deferrals the buffer is
// full. The old continuation — re-issue the original request under a fresh
// transaction number — is what filled it, one stranded answer per deferral.
//
// The rules these tests hold:
//
//   - a deferral is continued by GET QUEUED_MESSAGE, not by re-asking for the
//     parameter, so N deferrals cost the proxy one slot rather than N;
//   - a collected message is only allowed to satisfy the waiting command when
//     its PID is the one that command asked for;
//   - anything else collected is published, not attributed and not discarded;
//   - a responder that cannot serve a queued message — by NACK or by silence,
//     which is what RDM-LOG8's drain passes actually drew — falls back to the
//     old behaviour, once per device rather than once per deferral;
//   - the anti-misattribution guard and ACK_OVERFLOW reassembly are untouched.

func collectHarness(t *testing.T) *rdmHarness {
	t.Helper()
	return newRDMHarness(t, RDMConfig{DefaultProfile: proxyProfile, QueuedMessageDrain: DrainOff})
}

// --- the LOG8 shape -------------------------------------------------------

// TestRepeatedDeferralsDoNotCostASlotEach walks RDM-LOG8's shape directly: one
// command, deferred eight times before the answer arrives. The assertion that
// matters is the count of requests for the parameter itself — that is what the
// proxy allocates a buffer slot and an over-air round trip for.
func TestRepeatedDeferralsDoNotCostASlotEach(t *testing.T) {
	h := collectHarness(t)
	// The responder's estimate climbs exactly as the MoonLite2's did.
	for _, raw := range []uint16{6, 9, 12, 15, 18, 21, 24, 30} {
		h.script(reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(raw)})
	}
	h.script(reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("parked answer")})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if string(res.Data) != "parked answer" {
		t.Fatalf("data = %q", res.Data)
	}
	if res.AckTimers != 8 {
		t.Fatalf("ackTimers = %d, want 8", res.AckTimers)
	}

	asks := h.countRequests(rdm.PIDDeviceInfo)
	if asks != 1 {
		t.Fatalf("DEVICE_INFO asked %d times, want exactly 1 — RDM-LOG8 filled the proxy at one ask per deferral", asks)
	}
	// Count and content both. Counting probes alone would pass on a
	// continuation that puts an out-of-range status_type on the wire, which
	// is a probe a real responder answers with silence — so the shape would
	// look right here and collect nothing on the rig.
	assertEveryQueuedFilterLegal(t, h, "ACK_TIMER probe", 8)
	if got := h.ctrl.Stats().AckTimerReissues; got != 0 {
		t.Fatalf("AckTimerReissues = %d, want 0 — nothing should have fallen back", got)
	}
	if got := h.ctrl.Stats().AckTimerCollectHits; got != 1 {
		t.Fatalf("AckTimerCollectHits = %d, want 1", got)
	}
}

// TestReissueOnlyReproducesTheOldShape pins the escape hatch, and documents
// what it costs: under ReissueOnly the same eight deferrals produce nine asks
// for the parameter — nine slots, which is the behaviour that exhausted a
// freshly power-cycled MoonLite2 in twenty.
func TestReissueOnlyReproducesTheOldShape(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{
		DefaultProfile:     proxyProfile,
		QueuedMessageDrain: DrainOff,
		AckTimerCollect:    ReissueOnly,
	})
	for i := 0; i < 8; i++ {
		h.script(reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(6)})
	}
	h.script(reply{Type: rdm.ResponseACK, Data: []byte("ok")})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if asks := h.countRequests(rdm.PIDDeviceInfo); asks != 9 {
		t.Fatalf("DEVICE_INFO asks under ReissueOnly = %d, want 9", asks)
	}
	if probes := h.countRequests(rdm.PIDQueuedMessage); probes != 0 {
		t.Fatalf("QUEUED_MESSAGE probes under ReissueOnly = %d, want 0", probes)
	}
}

// TestAckTimerProbeUsesALegalStatusTypeFilter pins the one byte of param data
// the continuation puts on the wire.
//
// E1.20 allows only 1=Last Message, 2=Advisory, 3=Warning, 4=Error as
// QUEUED_MESSAGE's request status_type; 0=None is STATUS_MESSAGES' value and
// is out of range for this PID (research doc §2.4). StatusAdvisory (0x02) is
// the lowest legal floor and so is this PID's "hand me everything you are
// holding".
//
// This is the same assertion the recovery drain already carries
// (TestAutoDrainUsesALegalStatusTypeFilter), made here because the two paths
// build their request data independently — the drain in startDrainLocked, the
// continuation in issueLocked's wire override — and the mistake was in fact
// made in both. RDM-LOG8 is what makes it worth a byte-level test rather than
// a shape one: six probes carrying 0x00 drew no response of any kind, not
// even a NACK, from devices answering everything else in the same seconds. A
// continuation that is silently never answered falls back to a re-issue every
// time, which is exactly the buffer-filling behaviour the continuation exists
// to end.
//
// Every route into issueLocked's override is covered, because each is its own
// call: the first probe of a deferral, a re-probe after collecting somebody
// else's message, and a probe standing in for a deferred SET.
func TestAckTimerProbeUsesALegalStatusTypeFilter(t *testing.T) {
	t.Run("first probe and re-probes after orphans", func(t *testing.T) {
		h := newRDMHarness(t, RDMConfig{
			DefaultProfile:     proxyProfile,
			QueuedMessageDrain: DrainOff,
			MaxAckTimerCollect: 3,
		})
		h.script(reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)})
		// Two messages belonging to somebody else, each of which sends the
		// command back through beginCollectLocked for another probe...
		for i := 0; i < 2; i++ {
			h.script(reply{Type: rdm.ResponseACK, PID: &sensorValuePID, Data: []byte{byte(i)}})
		}
		// ...then ours.
		h.script(reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("parked answer")})

		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultAck {
			t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
		}
		assertEveryQueuedFilterLegal(t, h, "ACK_TIMER probe", 3)
	})

	t.Run("probe standing in for a deferred SET", func(t *testing.T) {
		h := collectHarness(t)
		startAddr := rdm.PIDDMXStartAddress
		h.script(
			reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
			reply{Type: rdm.ResponseACK, PID: &startAddr},
		)

		cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXStartAddress, []byte{0x00, 0x2A})
		if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultAck {
			t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
		}
		assertEveryQueuedFilterLegal(t, h, "ACK_TIMER probe", 1)
	})
}

// --- routing --------------------------------------------------------------

// TestCollectedMessageForAnotherPIDIsNotAttributed is the routing guard. A
// queued message carries no reference to the request it answers, so a message
// under some other PID must never be handed to the waiting command as if it
// were its answer — it is published and collection continues.
func TestCollectedMessageForAnotherPIDIsNotAttributed(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		// A stale answer to somebody else's SENSOR_VALUE.
		reply{Type: rdm.ResponseACK, PID: &sensorValuePID, Data: []byte{0, 1, 2}},
		// Then ours.
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("mine")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if string(res.Data) != "mine" {
		t.Fatalf("data = %q, want only this command's own answer — the orphan leaked in", res.Data)
	}
	if res.ResponsePID != rdm.PIDDeviceInfo {
		t.Fatalf("ResponsePID = 0x%04X, want DEVICE_INFO", uint16(res.ResponsePID))
	}

	// The orphan is published rather than dropped.
	var found *Event
	for _, ev := range drainEvents(h.ctrl.Events()) {
		if ev.Kind == EventQueuedMessageCollected {
			e := ev
			found = &e
		}
	}
	if found == nil {
		t.Fatal("no EventQueuedMessageCollected — the orphaned queued message was discarded")
	}
	if found.QueuedPID != rdm.PIDSensorValue {
		t.Fatalf("orphan PID = 0x%04X, want SENSOR_VALUE", uint16(found.QueuedPID))
	}
	if string(found.QueuedData) != string([]byte{0, 1, 2}) {
		t.Fatalf("orphan data = %v", found.QueuedData)
	}
	if found.UID != uidA {
		t.Fatalf("orphan UID = %v, want %v", found.UID, uidA)
	}
}

// TestEmptyQueueFallsBackToAsking: the responder collected properly but had
// nothing for us, so the parked answer is gone and the only way forward is to
// ask again.
func TestEmptyQueueFallsBackToAsking(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		emptyQueueAck(),
		reply{Type: rdm.ResponseACK, Data: []byte("asked again")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck || string(res.Data) != "asked again" {
		t.Fatalf("kind = %v data = %q, want ack/\"asked again\"", res.Kind, res.Data)
	}
	if asks := h.countRequests(rdm.PIDDeviceInfo); asks != 2 {
		t.Fatalf("DEVICE_INFO asks = %d, want 2 (original plus the fallback)", asks)
	}
	if got := h.ctrl.Stats().AckTimerReissues; got != 1 {
		t.Fatalf("AckTimerReissues = %d, want 1", got)
	}
}

// TestCollectCapFallsBackRatherThanSpinning: a responder handing back an
// endless stream of other people's messages must not hold one command
// forever.
func TestCollectCapFallsBackRatherThanSpinning(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{
		DefaultProfile:     proxyProfile,
		QueuedMessageDrain: DrainOff,
		MaxAckTimerCollect: 3,
	})
	h.script(reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)})
	// Three messages that are never ours — the cap — then the fallback ask.
	for i := 0; i < 3; i++ {
		h.script(reply{Type: rdm.ResponseACK, PID: &sensorValuePID, Data: []byte{byte(i)}})
	}
	h.script(reply{Type: rdm.ResponseACK, Data: []byte("finally")})

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck || string(res.Data) != "finally" {
		t.Fatalf("kind = %v data = %q, want ack/\"finally\"", res.Kind, res.Data)
	}
	if probes := h.countRequests(rdm.PIDQueuedMessage); probes != 3 {
		t.Fatalf("probes = %d, want exactly 3 — one per collected orphan, then the cap", probes)
	}
	if got := h.ctrl.Stats().QueuedMessagesDrained; got != 3 {
		t.Fatalf("QueuedMessagesDrained = %d, want 3 — collected orphans are still real messages freed", got)
	}
	if asks := h.countRequests(rdm.PIDDeviceInfo); asks != 2 {
		t.Fatalf("DEVICE_INFO asks = %d, want 2 (original plus the post-cap fallback)", asks)
	}
}

// --- learning what a device supports -------------------------------------

// TestUnknownPIDMarksTheDeviceOnceNotPerDeferral: a responder that does not
// implement QUEUED_MESSAGE must cost one probe in total, not one per
// deferral. Otherwise the fix would tax every device that does not need it.
func TestUnknownPIDMarksTheDeviceOnceNotPerDeferral(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)}, // probe refused
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},                // deferred again
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},                // and again
		reply{Type: rdm.ResponseACK, Data: []byte("done")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck || string(res.Data) != "done" {
		t.Fatalf("kind = %v data = %q", res.Kind, res.Data)
	}
	if probes := h.countRequests(rdm.PIDQueuedMessage); probes != 1 {
		t.Fatalf("probes = %d, want exactly 1 — the device said UNKNOWN_PID and must not be asked again", probes)
	}
	// A NACK of the probe is not the caller's answer and must never be
	// reported as one.
	if res.NackReason != 0 {
		t.Fatalf("NackReason = 0x%04X leaked from the probe into the caller's result", uint16(res.NackReason))
	}
}

// TestSilentProbeFallsBackWithoutSpendingRetries is RDM-LOG8's actual drain
// result: six probes to the proxied UIDs drew no response at all. Silence must
// cost one probe with no retransmissions, then revert to asking directly.
func TestSilentProbeFallsBackWithoutSpendingRetries(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Drop: true}, // the probe vanishes, exactly as on the bench
		reply{Type: rdm.ResponseACK, Data: []byte("direct")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 60*time.Second)

	if res.Kind != ResultAck || string(res.Data) != "direct" {
		t.Fatalf("kind = %v data = %q (err %v)", res.Kind, res.Data, res.Err)
	}
	if probes := h.countRequests(rdm.PIDQueuedMessage); probes != 1 {
		t.Fatalf("probes = %d, want 1 — silence gets no retry budget", probes)
	}
	if res.Retransmissions != 0 {
		t.Fatalf("retransmissions = %d, want 0 — the probe must not spend the command's retries", res.Retransmissions)
	}
	if got := h.ctrl.Stats().AckTimerCollectTimeouts; got != 1 {
		t.Fatalf("AckTimerCollectTimeouts = %d, want 1", got)
	}
	if got := h.ctrl.Stats().AckTimerReissues; got != 1 {
		t.Fatalf("AckTimerReissues = %d, want 1", got)
	}
}

// TestDeviceCollectModeIsRememberedAcrossCommands: the fallback decision is
// per device, so a second command to a responder already known not to collect
// goes straight to a re-issue.
func TestDeviceCollectModeIsRememberedAcrossCommands(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)},
		reply{Type: rdm.ResponseACK, Data: []byte("one")},
	)
	first := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(first, 30*time.Second); res.Kind != ResultAck {
		t.Fatalf("first kind = %v", res.Kind)
	}
	probesAfterFirst := h.countRequests(rdm.PIDQueuedMessage)

	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseACK, Data: []byte("two")},
	)
	second := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceLabel, nil)
	if res := h.awaitResult(second, 30*time.Second); res.Kind != ResultAck {
		t.Fatalf("second kind = %v (err %v)", res.Kind, res.Err)
	}

	if got := h.countRequests(rdm.PIDQueuedMessage); got != probesAfterFirst {
		t.Fatalf("probes went from %d to %d — the device's mode was not remembered", probesAfterFirst, got)
	}
}

// --- the guards that must survive ----------------------------------------

// TestCollectionDoesNotWeakenTheStrayResponseGuard: the anti-misattribution
// rule is transaction number plus source UID plus command class, and a probe
// changes none of that. A response carrying a wrong UID is still stray, even
// though its PID would have been licensed to float.
func TestCollectionDoesNotWeakenTheStrayResponseGuard(t *testing.T) {
	h := collectHarness(t)
	other := uidC
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		// Right TN, right PID shape for a collection — but the wrong
		// responder. It must be discarded, not folded in.
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("imposter"), SourceUID: &other},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 60*time.Second)

	if res.Kind == ResultAck {
		t.Fatalf("a response from %v satisfied a command addressed to %v", other, uidA)
	}
	if got := h.ctrl.Stats().StrayResponses; got == 0 {
		t.Fatal("StrayResponses = 0, want the mis-addressed response counted as stray")
	}
}

// TestCollectionSurvivesAckOverflowReassembly: a parked answer can itself be
// too big for one packet. The overflow blocks are reassembled under the PID
// the first block pinned, and a block that changes PID mid-sequence still
// aborts.
func TestCollectionSurvivesAckOverflowReassembly(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseACKOverflow, PID: &deviceInfoPID, Data: []byte("part-one:")},
		reply{Type: rdm.ResponseACK, PID: &deviceInfoPID, Data: []byte("part-two")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if string(res.Data) != "part-one:part-two" {
		t.Fatalf("data = %q, want the reassembled parked answer", res.Data)
	}
	if res.Blocks != 2 {
		t.Fatalf("blocks = %d, want 2", res.Blocks)
	}
}

func TestCollectionAckOverflowPIDChangeStillAborts(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseACKOverflow, PID: &deviceInfoPID, Data: []byte("aaaa")},
		reply{Type: rdm.ResponseACK, PID: &sensorValuePID, Data: []byte("bbbb")},
	)

	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAborted {
		t.Fatalf("kind = %v, want aborted", res.Kind)
	}
	if !errors.Is(res.Err, ErrOverflowPIDMismatch) {
		t.Fatalf("err = %v, want ErrOverflowPIDMismatch", res.Err)
	}
}

// TestCollectionLeavesTheWiredPathAlone: a responder that never defers never
// sees a probe, and never gets an entry in the health map. RDM-LOG6's wired
// walk was 82 requests with no ACK_TIMER and no refusal; nothing about this
// change touches that path.
func TestCollectionLeavesTheWiredPathAlone(t *testing.T) {
	h := collectHarness(t)
	for i := 0; i < 12; i++ {
		h.script(reply{Type: rdm.ResponseACK, Data: []byte{byte(i)}})
	}
	for i := 0; i < 12; i++ {
		cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(cmd, time.Second); res.Kind != ResultAck {
			t.Fatalf("command %d kind = %v", i, res.Kind)
		}
	}
	if probes := h.countRequests(rdm.PIDQueuedMessage); probes != 0 {
		t.Fatalf("QUEUED_MESSAGE probes on a path that never deferred = %d, want 0", probes)
	}
	st := h.ctrl.Stats()
	if st.AckTimerCollects != 0 || st.AckTimerReissues != 0 {
		t.Fatalf("collects=%d reissues=%d, want 0/0 on a wired path", st.AckTimerCollects, st.AckTimerReissues)
	}
	if n := len(h.ctrl.Reachability()); n != 0 {
		t.Fatalf("Reachability entries = %d, want 0 — a healthy wired device should stay unknown to the health map", n)
	}
}

// TestSetDeferredThenCollected: a SET's confirmation can be parked too. The
// probe is a GET even though the request was a SET, so response matching has
// to follow what was actually put on the wire.
func TestSetDeferredThenCollected(t *testing.T) {
	h := collectHarness(t)
	startAddr := rdm.PIDDMXStartAddress
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseACK, PID: &startAddr},
	)

	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXStartAddress, []byte{0x00, 0x2A})
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultAck {
		t.Fatalf("kind = %v (err %v), want ack", res.Kind, res.Err)
	}
	if got := h.requestAt(1); got.ParameterID != rdm.PIDQueuedMessage || got.CommandClass != rdm.GetCommand {
		t.Fatalf("probe = PID 0x%04X CC 0x%02X, want a GET of QUEUED_MESSAGE",
			uint16(got.ParameterID), byte(got.CommandClass))
	}
}

// --- a SET that cannot be collected must never be re-sent ------------------
//
// RDM-LOG13's bench shape: SET DMX_PERSONALITY got ACK_TIMER, the one
// QUEUED_MESSAGE probe drew silence, and the pre-fix code fell back to
// abandonCollectLocked's re-issue exactly as a GET would — 352 duplicate SET
// commands over 61 seconds against an already-congested link, for a mode
// change that had already taken effect on the very first attempt. A SET must
// stop and report ResultUnverified instead: see ResultUnverified's doc
// comment (rdmcontroller.go) for the reasoning. These tests hold that a SET
// never gets a second transmission once its deferral can't be verified,
// however the "can't collect" finding arrives — a NACK on the probe, silence
// on the probe, or (TestSetAlreadyKnownNotToCollectNeverReissues) the device
// having already taught the controller not to bother probing at all.

func TestSetUnknownPIDDoesNotReissue(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)}, // probe refused
	)

	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXPersonality, []byte{0x01})
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultUnverified {
		t.Fatalf("kind = %v (err %v), want ResultUnverified", res.Kind, res.Err)
	}
	if !errors.Is(res.Err, ErrSetUnverified) {
		t.Fatalf("err = %v, want ErrSetUnverified", res.Err)
	}
	if got := h.countRequests(rdm.PIDDMXPersonality); got != 1 {
		t.Fatalf("SET DMX_PERSONALITY sent %d times, want exactly 1 — a SET must never be re-issued once unverifiable", got)
	}
}

func TestSetSilentProbeDoesNotReissue(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(10)},
		reply{Drop: true}, // the QUEUED_MESSAGE probe vanishes, exactly as RDM-LOG13
	)

	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXPersonality, []byte{0x01})
	res := h.awaitResult(cmd, 60*time.Second)

	if res.Kind != ResultUnverified {
		t.Fatalf("kind = %v (err %v), want ResultUnverified", res.Kind, res.Err)
	}
	if got := h.countRequests(rdm.PIDDMXPersonality); got != 1 {
		t.Fatalf("SET DMX_PERSONALITY sent %d times, want exactly 1", got)
	}
	if got := h.countRequests(rdm.PIDQueuedMessage); got != 1 {
		t.Fatalf("probes = %d, want exactly 1 — silence gets no retransmission budget here either", got)
	}
}

// TestSetAlreadyKnownNotToCollectNeverReissues covers onAckTimerElapsed's
// other exit path: a device already taught (by an earlier command) that it
// does not collect skips probing entirely — ackTimerCollectsLocked returns
// false immediately, so cmd.collecting is never set, and the fix in that
// branch (not abandonCollectLocked) is what must catch this SET.
func TestSetAlreadyKnownNotToCollectNeverReissues(t *testing.T) {
	h := collectHarness(t)
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)},
		reply{Type: rdm.ResponseACK, Data: []byte("direct")},
	)
	learn := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(learn, 30*time.Second); res.Kind != ResultAck {
		t.Fatalf("setup command kind = %v (err %v)", res.Kind, res.Err)
	}
	probesAfterLearning := h.countRequests(rdm.PIDQueuedMessage)

	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
	)
	cmd := h.ctrl.Set(h.node, uidA, rdm.PIDDMXPersonality, []byte{0x01})
	res := h.awaitResult(cmd, 30*time.Second)

	if res.Kind != ResultUnverified {
		t.Fatalf("kind = %v (err %v), want ResultUnverified", res.Kind, res.Err)
	}
	if got := h.countRequests(rdm.PIDQueuedMessage); got != probesAfterLearning {
		t.Fatalf("probes went from %d to %d — should not have probed a device already known not to collect", probesAfterLearning, got)
	}
	if got := h.countRequests(rdm.PIDDMXPersonality); got != 1 {
		t.Fatalf("SET DMX_PERSONALITY sent %d times, want exactly 1", got)
	}
}

// TestRecoveryDrainSkipsDevicesKnownNotToCollect: the breaker-open drain is a
// backstop now, and it must not repeat RDM-LOG8's six silent probes against a
// device the collection path has already found cannot serve them.
func TestRecoveryDrainSkipsDevicesKnownNotToCollect(t *testing.T) {
	h := newRDMHarness(t, RDMConfig{DefaultProfile: proxyProfile}) // default: drain on breaker open

	// First, teach the controller that uidA does not collect.
	h.script(
		reply{Type: rdm.ResponseACKTimer, Data: ackTimerData(3)},
		reply{Type: rdm.ResponseNackReason, Data: nackData(rdm.NackUnknownPID)},
		reply{Type: rdm.ResponseACK, Data: []byte("direct")},
	)
	cmd := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
	if res := h.awaitResult(cmd, 30*time.Second); res.Kind != ResultAck {
		t.Fatalf("setup command kind = %v (err %v)", res.Kind, res.Err)
	}
	probesAfterLearning := h.countRequests(rdm.PIDQueuedMessage)

	// Now drive it into the breaker.
	for i := 0; i < ProxyBreakerTrip*sendsPerCommand; i++ {
		h.script(proxyNack())
	}
	for i := 0; i < ProxyBreakerTrip; i++ {
		c := h.ctrl.Get(h.node, uidA, rdm.PIDDeviceInfo, nil)
		if res := h.awaitResult(c, 60*time.Second); res.Kind != ResultProxyBufferFull {
			t.Fatalf("command %d kind = %v, want proxy-buffer-full", i, res.Kind)
		}
	}
	h.clock.Advance(2 * time.Second)

	if got := h.countRequests(rdm.PIDQueuedMessage); got != probesAfterLearning {
		t.Fatalf("QUEUED_MESSAGE requests went from %d to %d — the recovery drain ignored what the "+
			"continuation had already learned about this device", probesAfterLearning, got)
	}
	if got := h.ctrl.Stats().QueuedDrains; got != 0 {
		t.Fatalf("QueuedDrains = %d, want 0", got)
	}
}
