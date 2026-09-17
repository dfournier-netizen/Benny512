package params

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// Silence is not an answer.
//
// probecache_test.go pins what the probe cache does with a device that
// ANSWERS: a fixture that advertises TILT_INVERT and not PAN_INVERT is never
// asked for PAN_INVERT (RDM-LOG24), and a fixture that NACKs
// SUPPORTED_PARAMETERS itself still gets its speculative probes, because
// absence of a list is not evidence of absence of a PID and a non-conforming
// fixture must not become invisible because it is non-conforming.
//
// This file pins the other half, which was wrong: a device that says
// NOTHING. resolveSupportedSet used to set supportedAttempted before it so
// much as looked at err, so one response timeout permanently recorded the
// UID as "asked, don't ask again" — and since no set was ever learned, the
// gate then failed open for it forever. Asked once, never re-asked, every
// speculative PID allowed, for the life of the process.
//
// That is backwards twice over. It spends the most blind traffic on the
// responder least able to absorb it, and it does so in a way no later read
// can ever correct. RDM-LOG31's 4D50:0011597E is the shape of it: advertised
// in the gateway's Table of Devices, answered nothing at all across the
// whole capture, and drew 15 PRODUCT_DETAIL_ID_LIST and 15
// PROXIED_DEVICE_COUNT packets against 3 SUPPORTED_PARAMETERS packets —
// every one of those 30 a speculative probe the gate was built to stop.
// 4D50:001159FE and 4D50:0011593E are the same device class, same silence.
//
// The guarantee asserted below, per silent UID per process (until
// ForgetDevice / the UI's rescan):
//
//	at most maxSupportedSilentAttempts (2) SUPPORTED_PARAMETERS transactions,
//	and exactly 0 speculative-PID transactions.
//
// E1.20-required PIDs are not gated by any of this — see
// TestProbeCache_RequiredPIDsAreNeverGated, which must keep passing
// unchanged.
//
// Every count below is taken at the responder, for the reason
// probecache_test.go gives: "did we send it?" only has an honest answer at
// the far end.

// sendsPerSilentTransaction is how many packets one unanswered command puts
// on the wire: the first transmission plus the profile's retries. Derived
// from the profile rather than written as "3", so a retry-budget change
// shows up as a changed expectation here instead of a broken test.
var sendsPerSilentTransaction = 1 + session.ProfileDirect.Retries

// silentResponder is dynamicResponder's counterpart for devices that may
// answer nothing. The handler is consulted for every request that reaches
// the UID (so the counting is at the far end, like probecache_test.go) and
// returns reply=false to model a responder that simply is not there.
func silentResponder(clock *session.FakeClock, ctrlRef *[]*session.RDMController, uid rdm.UID,
	handler func(msg rdm.Message) (reply bool, data []byte, nack bool, reason rdm.NackReason)) *session.FakeTransport {
	tport := session.NewFakeTransport()
	tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil || sp.Packet.Kind != artnet.KindRdm {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil || msg.DestinationUID != uid {
			return
		}
		reply, data, nack, reason := handler(msg)
		if !reply {
			return // the wire stays quiet; the controller will retry, then time out
		}
		respClass := rdm.GetCommandResponse
		if msg.CommandClass == rdm.SetCommand {
			respClass = rdm.SetCommandResponse
		}
		resp := rdm.Message{
			DestinationUID: msg.SourceUID, SourceUID: uid,
			TransactionNumber: msg.TransactionNumber, PortIDOrResponseType: byte(rdm.ResponseACK),
			SubDevice: msg.SubDevice, CommandClass: respClass, ParameterID: msg.ParameterID,
			ParameterData: data,
		}
		if nack {
			resp.PortIDOrResponseType = byte(rdm.ResponseNackReason)
			resp.ParameterData = []byte{byte(reason >> 8), byte(reason)}
		}
		clock.AfterFunc(time.Millisecond, func() {
			(*ctrlRef)[0].HandleRDMResponse(resp)
		})
	}
	return tport
}

func newSilentTestClient(t *testing.T, uid rdm.UID,
	handler func(msg rdm.Message) (reply bool, data []byte, nack bool, reason rdm.NackReason)) (*Client, *session.FakeClock) {
	t.Helper()
	ClearDescriptorCache()
	ForgetDevice(uid)
	clock := session.NewFakeClock(time.Time{})
	ctrlRef := make([]*session.RDMController, 1)
	tport := silentResponder(clock, &ctrlRef, uid, handler)
	ctrl := session.NewRDMController(session.RDMConfig{Transport: tport, Clock: clock})
	ctrlRef[0] = ctrl
	port, _ := artnet.NewPortAddress(0, 0, 1)
	node := session.NodeRef{
		Key:  session.NodeKey{IP: netip.MustParseAddr("10.0.0.9"), BindIndex: 1},
		Addr: netip.MustParseAddrPort("10.0.0.9:6454"),
		Port: port,
	}
	return New(ctrl, node, uid), clock
}

// runSilent is runAsync with a coarser clock step: a single unanswered
// command burns ResponseTimeout * (1+Retries) of fake time, which runAsync's
// 2ms step cannot reach inside its real-time budget.
func runSilent(t *testing.T, clock *session.FakeClock, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(250 * time.Millisecond)
		select {
		case <-done:
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for async call to complete")
}

// speculativeVictims are the three speculative PIDs RDM-LOG31's silent UIDs
// actually absorbed, plus CURVE for a third gated family.
var speculativeVictims = []rdm.ParameterID{
	rdm.PIDProductDetailIDList,
	rdm.PIDProxiedDeviceCount,
	rdm.PIDCurve,
}

// TestProbeCache_SilentDeviceDoesNotOpenTheSpeculativeGate is the defect,
// replayed: a responder that answers nothing, ever, asked repeatedly for
// speculative PIDs.
//
// Against the old code SUPPORTED_PARAMETERS timed out once, that timeout was
// recorded as a completed attempt, and every one of the nine speculative
// asks below then went out on the wire — a device with nobody home
// collecting the full blind-probe stream. The exact bounded numbers are the
// point of the test.
func TestProbeCache_SilentDeviceDoesNotOpenTheSpeculativeGate(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x0011597E} // RDM-LOG31's own silent UID
	counter := newProbeCounter()

	goroutinesBefore := runtime.NumGoroutine()

	client, clock := newSilentTestClient(t, uid, func(msg rdm.Message) (bool, []byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		return false, nil, false, 0 // nothing. ever.
	})

	const asks = 9
	for i := 0; i < asks; i++ {
		pid := speculativeVictims[i%len(speculativeVictims)]
		var err error
		runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), pid, nil) })
		if !errors.Is(err, ErrDeviceNotAnswering) {
			t.Fatalf("ask %d for 0x%04X: err = %v, want ErrDeviceNotAnswering — a device that "+
				"says nothing has told us nothing about what it supports, and 25 blind probes "+
				"will not change that", i+1, uint16(pid), err)
		}
	}

	for _, pid := range speculativeVictims {
		if got := counter.count(pid); got != 0 {
			t.Errorf("speculative PID 0x%04X reached the silent responder %d time(s) across %d asks, want 0 — "+
				"this is the RDM-LOG31 defect: 4D50:0011597E answered nothing all capture and still "+
				"absorbed 15 PRODUCT_DETAIL_ID_LIST and 15 PROXIED_DEVICE_COUNT packets", uint16(pid), got, asks)
		}
	}

	wantSupported := maxSupportedSilentAttempts * sendsPerSilentTransaction
	if got := counter.count(rdm.PIDSupportedParameters); got != wantSupported {
		t.Errorf("SUPPORTED_PARAMETERS packets = %d across %d speculative asks, want exactly %d "+
			"(%d transactions x %d sends each) — silence must stay retryable, but BOUNDED, or "+
			"every speculative GET re-triggers the fetch and one waste is traded for a worse one",
			got, asks, wantSupported, maxSupportedSilentAttempts, sendsPerSilentTransaction)
	}

	for i := 0; i < 50 && runtime.NumGoroutine() > goroutinesBefore; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > goroutinesBefore {
		t.Errorf("goroutines: %d before, %d after", goroutinesBefore, after)
	}
}

// TestProbeCache_NackedSupportedParametersStillProbes restates the fail-open
// half in the same vocabulary as the silence tests, so the two sit side by
// side and the difference is visible: this device is just as unable to
// describe itself, but it ANSWERS, and an answer — even "I don't know that
// PID" — is a fact about the responder. It keeps its probes.
//
// TestProbeCache_RemembersUnknownPIDNackWhenSupportedListIsUnavailable is
// the original of this and must also keep passing, untouched.
func TestProbeCache_NackedSupportedParametersStillProbes(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115901}
	counter := newProbeCounter()

	client, clock := newSilentTestClient(t, uid, func(msg rdm.Message) (bool, []byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		return true, nil, true, rdm.NackUnknownPID
	})

	var err error
	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDProductDetailIDList, nil) })
	var nackErr *session.NackError
	if !errors.As(err, &nackErr) || nackErr.Reason != rdm.NackUnknownPID {
		t.Fatalf("getRaw(PRODUCT_DETAIL_ID_LIST) = %v, want a live NACK UNKNOWN_PID from the device — "+
			"a NACKed SUPPORTED_PARAMETERS must still fail OPEN, exactly as before this change", err)
	}
	if got := counter.count(rdm.PIDProductDetailIDList); got != 1 {
		t.Errorf("PRODUCT_DETAIL_ID_LIST reached the device %d times, want 1 — the gate must fail "+
			"open for a device that answered", got)
	}
	if got := counter.count(rdm.PIDSupportedParameters); got != 1 {
		t.Errorf("SUPPORTED_PARAMETERS packets = %d, want 1 — a NACK is an answer; asking twice "+
			"gets the same one", got)
	}
}

// TestProbeCache_SilentDeviceThatStartsAnsweringIsLearned is why the silent
// state is retryable at all rather than a permanent closed gate.
//
// A responder can be booting, powered down, or out of radio range when it is
// first asked, and the same UID is then perfectly answerable a minute later.
// One retry is budgeted for exactly that, and it must actually produce a
// correct supported set — the per-PID gate working normally afterwards, not
// a device stuck behind its own first bad minute.
func TestProbeCache_SilentDeviceThatStartsAnsweringIsLearned(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115902}
	counter := newProbeCounter()

	var mu sync.Mutex
	awake := false

	client, clock := newSilentTestClient(t, uid, func(msg rdm.Message) (bool, []byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		mu.Lock()
		up := awake
		mu.Unlock()
		if !up {
			return false, nil, false, 0
		}
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return true, rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDCurve}), false, 0
		case rdm.PIDCurve:
			return true, []byte{1, 1}, false, 0
		default:
			return true, nil, true, rdm.NackUnknownPID
		}
	})

	var err error
	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
	if !errors.Is(err, ErrDeviceNotAnswering) {
		t.Fatalf("while asleep: getRaw(CURVE) = %v, want ErrDeviceNotAnswering", err)
	}

	mu.Lock()
	awake = true
	mu.Unlock()

	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
	if err != nil {
		t.Fatalf("after waking: getRaw(CURVE) = %v, want success — the retry budget exists so a "+
			"responder that was merely booting is learned rather than written off", err)
	}
	if got := counter.count(rdm.PIDCurve); got != 1 {
		t.Errorf("CURVE reached the device %d times, want 1 (zero while silent, one once awake)", got)
	}
	if got, want := counter.count(rdm.PIDSupportedParameters), sendsPerSilentTransaction+1; got != want {
		t.Errorf("SUPPORTED_PARAMETERS packets = %d, want %d (one silent transaction, then one answered)", got, want)
	}

	// And the set it learned is used per-PID, like any other: the device
	// advertises CURVE and not PROXIED_DEVICE_COUNT.
	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDProxiedDeviceCount, nil) })
	if !errors.Is(err, ErrPIDNotAdvertised) {
		t.Fatalf("getRaw(PROXIED_DEVICE_COUNT) = %v, want ErrPIDNotAdvertised — once the device "+
			"answered, the gate is the device's own list again", err)
	}
	if got := counter.count(rdm.PIDProxiedDeviceCount); got != 0 {
		t.Errorf("PROXIED_DEVICE_COUNT reached the device %d times, want 0", got)
	}
}

// TestProbeCache_SilentBudgetSpentStaysClosedUntilForget states the price of
// bounding the retry, rather than leaving it implied.
//
// A device that is still silent after maxSupportedSilentAttempts is written
// off for the life of the process: if it starts answering later, its
// SPECULATIVE PIDs stay gated closed until ForgetDevice (the UI's rescan)
// clears the state. That is the deliberate trade — the alternative is one
// SUPPORTED_PARAMETERS transaction per speculative GET, forever, against a
// device that may never answer.
//
// What it does NOT cost: E1.20-required PIDs, which are not gated by any of
// this, so the ordinary "is it back yet?" reads (DEVICE_INFO,
// DMX_START_ADDRESS) are untouched and a returning device is still seen.
func TestProbeCache_SilentBudgetSpentStaysClosedUntilForget(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115903}
	counter := newProbeCounter()

	var mu sync.Mutex
	awake := false

	client, clock := newSilentTestClient(t, uid, func(msg rdm.Message) (bool, []byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		mu.Lock()
		up := awake
		mu.Unlock()
		if !up {
			return false, nil, false, 0
		}
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return true, rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDCurve}), false, 0
		case rdm.PIDCurve:
			return true, []byte{1, 1}, false, 0
		case rdm.PIDDMXStartAddress:
			return true, []byte{0x00, 0x01}, false, 0
		default:
			return true, nil, true, rdm.NackUnknownPID
		}
	})

	var err error
	for i := 0; i < maxSupportedSilentAttempts; i++ {
		runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
		if !errors.Is(err, ErrDeviceNotAnswering) {
			t.Fatalf("silent ask %d: getRaw(CURVE) = %v, want ErrDeviceNotAnswering", i+1, err)
		}
	}
	spentPackets := counter.count(rdm.PIDSupportedParameters)

	mu.Lock()
	awake = true
	mu.Unlock()

	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
	if !errors.Is(err, ErrDeviceNotAnswering) {
		t.Fatalf("after the budget was spent: getRaw(CURVE) = %v, want ErrDeviceNotAnswering — "+
			"this is the documented consequence of bounding the retry, and it is asserted rather "+
			"than left implied", err)
	}
	if got := counter.count(rdm.PIDSupportedParameters); got != spentPackets {
		t.Errorf("SUPPORTED_PARAMETERS packets = %d after the budget was spent, want %d (no further asks)",
			got, spentPackets)
	}
	if got := counter.count(rdm.PIDCurve); got != 0 {
		t.Errorf("CURVE reached the device %d times, want 0", got)
	}

	// The required-PID half of the trade: never gated, so the device is
	// still visible and still readable while its speculative gate is shut.
	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDDMXStartAddress, nil) })
	if err != nil {
		t.Fatalf("getRaw(DMX_START_ADDRESS) = %v, want success — E1.20-required PIDs are never "+
			"gated, by this or anything else", err)
	}

	// Rescan is the escape hatch, exactly as in
	// TestProbeCache_ForgetDeviceReopensTheGate.
	ForgetDevice(uid)
	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
	if err != nil {
		t.Fatalf("post-rescan getRaw(CURVE) = %v, want success — ForgetDevice must clear the "+
			"silent-attempt count too, or rescan means nothing for exactly the devices that need it", err)
	}
	if got := counter.count(rdm.PIDCurve); got != 1 {
		t.Errorf("CURVE reached the device %d times, want 1 (all of them after the rescan)", got)
	}
}

// TestProbeCache_CancelledContextDoesNotPoisonTheCache covers the third
// outcome, which is neither an answer nor the device's silence: we gave up.
//
// A cancelled context says nothing whatsoever about the responder, so it
// must be recorded as neither a completed attempt (which would fail the gate
// open forever) nor a silent one (which would spend the retry budget on our
// own impatience). The proof is that a later read with a live context still
// learns the real set and gates per-PID off it.
func TestProbeCache_CancelledContextDoesNotPoisonTheCache(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4D50, DeviceID: 0x00115904}
	counter := newProbeCounter()

	client, clock := newSilentTestClient(t, uid, func(msg rdm.Message) (bool, []byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return true, rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDCurve}), false, 0
		case rdm.PIDCurve:
			return true, []byte{1, 1}, false, 0
		default:
			return true, nil, true, rdm.NackUnknownPID
		}
	})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var err error
	runSilent(t, clock, func() { _, err = client.getRaw(cancelled, rdm.PIDCurve, nil) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled getRaw(CURVE) = %v, want context.Canceled", err)
	}

	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDCurve, nil) })
	if err != nil {
		t.Fatalf("post-cancel getRaw(CURVE) = %v, want success — our own cancellation must not "+
			"record an attempt, or one impatient caller decides this UID's gate for the life of "+
			"the process", err)
	}

	runSilent(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDProxiedDeviceCount, nil) })
	if !errors.Is(err, ErrPIDNotAdvertised) {
		t.Fatalf("getRaw(PROXIED_DEVICE_COUNT) = %v, want ErrPIDNotAdvertised — the cache must "+
			"hold the device's real list, not a cancellation's shadow", err)
	}
	if got := counter.count(rdm.PIDProxiedDeviceCount); got != 0 {
		t.Errorf("PROXIED_DEVICE_COUNT reached the device %d times, want 0", got)
	}
}
