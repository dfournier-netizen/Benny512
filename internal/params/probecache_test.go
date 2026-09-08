package params

import (
	"context"
	"errors"
	"sync"
	"testing"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// The per-device probe cache exists to stop Benny512 asking a fixture a
// question it has already answered. The machinery for it — supportedSet,
// unsupportedPIDs, ensureAdvertised, rememberUnsupported — was in place
// before this file, and nothing anywhere asserted that a gated PID actually
// stays OFF THE WIRE. A cache that is built but not consulted looks exactly
// like a cache that works, from every angle except a packet capture, and
// this project has now been bitten ten-plus times by a value that agreed
// with itself on one side of a boundary.
//
// So these tests count transactions at the responder, which is the only
// place the question "did we send it?" has an honest answer.
//
// The numbers come from bench capture RDM-LOG24: eight GLP JDC-1s over 23
// minutes, in which Benny512 asked each fixture for PAN_INVERT and
// PAN_TILT_SWAP NINE TIMES apiece and was NACKed UNKNOWN_PID every time —
// 18 of that capture's 26 NACKs, about 16% of all RDM traffic on the line.
// The fixture's SUPPORTED_PARAMETERS advertises TILT_INVERT and neither of
// the other two, which is correct: a JDC-1 tilts but does not pan. It told
// us once; we asked nine more times.

// probeCounter records how many GET transactions reached the device for
// each PID. Counting per-PID rather than in total is deliberate: the
// interesting failure is "the gate is open for this one PID", which a total
// would hide behind the transactions the test legitimately expects.
type probeCounter struct {
	mu sync.Mutex
	n  map[rdm.ParameterID]int
}

func newProbeCounter() *probeCounter {
	return &probeCounter{n: map[rdm.ParameterID]int{}}
}

func (p *probeCounter) hit(pid rdm.ParameterID) {
	p.mu.Lock()
	p.n[pid]++
	p.mu.Unlock()
}

func (p *probeCounter) count(pid rdm.ParameterID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[pid]
}

// TestProbeCache_AdvertisedSetKeepsUnsupportedPIDsOffTheWire is LOG24,
// replayed: a fixture that advertises TILT_INVERT and not PAN_INVERT must be
// asked for PAN_INVERT exactly zero times, however many callers want it, and
// must still answer TILT_INVERT normally.
//
// The "and still answers TILT_INVERT" half is not padding. The cheap way to
// pass the first half is to gate the whole pan/tilt family, which would stop
// Benny512 reading a setting the fixture genuinely has — a silent feature
// loss that no NACK would ever reveal, because the request would never be
// sent. The gate is per-PID against the device's own list, and this pins it.
func TestProbeCache_AdvertisedSetKeepsUnsupportedPIDsOffTheWire(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0001} // a JDC-1 stand-in
	counter := newProbeCounter()

	// The real JDC-1 answer: it tilts, it does not pan.
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDTiltInvert})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0
		case rdm.PIDTiltInvert:
			return []byte{0}, false, 0
		default:
			// What a real fixture does when asked for something it hasn't got.
			return nil, true, rdm.NackUnknownPID
		}
	})

	// LOG24's own count: nine asks for a PID the device does not have.
	const asks = 9
	for i := 0; i < asks; i++ {
		var err error
		runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDPanInvert, nil) })
		if !errors.Is(err, ErrPIDNotAdvertised) {
			t.Fatalf("ask %d: getRaw(PAN_INVERT) err = %v, want ErrPIDNotAdvertised — "+
				"the device's SUPPORTED_PARAMETERS does not list it, so this must be "+
				"refused locally rather than sent and NACKed", i+1, err)
		}
	}

	if got := counter.count(rdm.PIDPanInvert); got != 0 {
		t.Errorf("PAN_INVERT reached the fixture %d time(s) across %d asks, want 0 — "+
			"this is the RDM-LOG24 defect: 16%% of a bench session's RDM traffic spent "+
			"asking a JDC-1 about a pan motor it does not have", got, asks)
	}

	// SUPPORTED_PARAMETERS is what makes the gate possible, so it is allowed
	// on the wire — but once, not once per gated PID. That distinction is the
	// difference between a cache and a redirect.
	if got := counter.count(rdm.PIDSupportedParameters); got != 1 {
		t.Errorf("SUPPORTED_PARAMETERS fetched %d times across %d gated asks, want exactly 1",
			got, asks)
	}

	// The other half: a PID the device DOES advertise must still be asked.
	var data []byte
	var err error
	runAsync(t, clock, func() { data, err = client.getRaw(context.Background(), rdm.PIDTiltInvert, nil) })
	if err != nil {
		t.Fatalf("getRaw(TILT_INVERT) = %v, want success — the fixture advertises it, "+
			"and gating a PID the device reported would be a silent feature loss "+
			"with no NACK to reveal it", err)
	}
	if len(data) != 1 {
		t.Errorf("TILT_INVERT data = %v, want one byte", data)
	}
	if got := counter.count(rdm.PIDTiltInvert); got != 1 {
		t.Errorf("TILT_INVERT reached the fixture %d times, want 1", got)
	}
}

// TestProbeCache_RemembersUnknownPIDNackWhenSupportedListIsUnavailable
// covers the device the advertised-set gate cannot help: one that NACKs
// SUPPORTED_PARAMETERS itself.
//
// That is a real and legal shape — E1.20 lists SUPPORTED_PARAMETERS among
// the required PIDs, and responders that get required PIDs wrong are exactly
// the ones this app exists to find. For such a device the gate must FAIL
// OPEN: absence of a list is not evidence of absence of a PID, and refusing
// to probe would make Benny512 blind to a fixture precisely because that
// fixture is non-conforming.
//
// So the first probe goes out. What must not happen is the second through
// ninth: a live NACK UNKNOWN_PID for a specific PID on a specific UID is a
// direct answer from the device about itself, and it is worth remembering
// even when the device's summary of itself was not.
func TestProbeCache_RemembersUnknownPIDNackWhenSupportedListIsUnavailable(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0002}
	counter := newProbeCounter()

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		// This device won't even tell us what it supports.
		return nil, true, rdm.NackUnknownPID
	})

	const asks = 9
	for i := 0; i < asks; i++ {
		var err error
		runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDPanInvert, nil) })
		if err == nil {
			t.Fatalf("ask %d: getRaw(PAN_INVERT) unexpectedly succeeded", i+1)
		}
		if i == 0 {
			// The first ask must be a REAL device NACK, not a local refusal:
			// with no supported list, the app has no grounds to refuse.
			var nackErr *session.NackError
			if !errors.As(err, &nackErr) || nackErr.Reason != rdm.NackUnknownPID {
				t.Fatalf("first ask returned %v, want a live NACK UNKNOWN_PID from the "+
					"device — with SUPPORTED_PARAMETERS unavailable the gate must fail "+
					"open, or a non-conforming fixture becomes invisible", err)
			}
		}
	}

	if got := counter.count(rdm.PIDPanInvert); got != 1 {
		t.Errorf("PAN_INVERT reached the fixture %d times across %d asks, want exactly 1 — "+
			"the device answered UNKNOWN_PID once; repeating the question does not "+
			"change the answer, it just costs the line", got, asks)
	}

	// And the failed SUPPORTED_PARAMETERS lookup is itself remembered, so a
	// device that cannot answer it is asked once per process rather than once
	// per gated PID — otherwise the cache turns one wasted transaction into
	// two.
	if got := counter.count(rdm.PIDSupportedParameters); got != 1 {
		t.Errorf("SUPPORTED_PARAMETERS attempted %d times across %d gated asks, want exactly 1",
			got, asks)
	}
}

// TestProbeCache_ForgetDeviceReopensTheGate pins the escape hatch, and the
// reason one is needed.
//
// Every fact this cache holds is about a specific UID's specific firmware.
// A fixture that is re-flashed between shows, or a UID that returns as a
// different unit, can genuinely gain a PID it once lacked — so "we asked and
// it said no" has to be forgettable, or the UI's rescan action would be a
// lie and a firmware update would look like a broken app for the life of the
// process.
func TestProbeCache_ForgetDeviceReopensTheGate(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0003}
	counter := newProbeCounter()

	var mu sync.Mutex
	hasPan := false // flipped to simulate a firmware update mid-test

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		mu.Lock()
		pan := hasPan
		mu.Unlock()
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			pids := []rdm.ParameterID{rdm.PIDTiltInvert}
			if pan {
				pids = append(pids, rdm.PIDPanInvert)
			}
			return rdm.EncodeSupportedParameters(pids), false, 0
		case rdm.PIDPanInvert:
			if pan {
				return []byte{0}, false, 0
			}
			return nil, true, rdm.NackUnknownPID
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	var err error
	runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDPanInvert, nil) })
	if !errors.Is(err, ErrPIDNotAdvertised) {
		t.Fatalf("pre-update getRaw(PAN_INVERT) = %v, want ErrPIDNotAdvertised", err)
	}

	// The fixture gains a pan motor's worth of firmware, and the operator
	// hits rescan.
	mu.Lock()
	hasPan = true
	mu.Unlock()
	ForgetDevice(uid)

	runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDPanInvert, nil) })
	if err != nil {
		t.Fatalf("post-rescan getRaw(PAN_INVERT) = %v, want success — ForgetDevice must "+
			"drop the cached refusal, or a re-flashed fixture stays broken for the life "+
			"of the process and rescan means nothing", err)
	}
	if got := counter.count(rdm.PIDPanInvert); got != 1 {
		t.Errorf("PAN_INVERT reached the fixture %d times, want 1 (zero before rescan, one after)", got)
	}
	if got := counter.count(rdm.PIDSupportedParameters); got != 2 {
		t.Errorf("SUPPORTED_PARAMETERS fetched %d times, want 2 — once before the rescan "+
			"and once after, since rescan exists precisely to re-ask", got)
	}
}

// TestProbeCache_RequiredPIDsAreNeverGated is the boundary the cache must
// never cross, restated here as a wire-level assertion rather than a
// property of the switch statement in isSpeculativePID.
//
// E1.20 §10.4.1: a device SHALL NOT report its minimum-support PIDs in
// SUPPORTED_PARAMETERS. So for a required PID, absence from that list is the
// CONFORMING case and carries no information at all. Gating on it would stop
// Benny512 reading a DMX start address from a device that implements it
// perfectly — and the symptom would be silence, not an error.
//
// speculativepan_test.go asserts the same rule against isSpeculativePID
// directly. This one asserts it where it actually matters: that the packet
// leaves.
func TestProbeCache_RequiredPIDsAreNeverGated(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0004}
	counter := newProbeCounter()

	// A conforming list: TILT_INVERT only. Per §10.4.1 the required PIDs
	// below are correctly absent from it.
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDTiltInvert})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		counter.hit(msg.ParameterID)
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0
		case rdm.PIDDMXStartAddress:
			return []byte{0x00, 0x01}, false, 0
		case rdm.PIDIdentifyDevice:
			return []byte{0}, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	// Prime the cache with a gated ask, so the supported set is definitely
	// resolved and cached before the required PIDs are tried. Without this
	// the test could pass merely because nothing had been fetched yet.
	var err error
	runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), rdm.PIDPanInvert, nil) })
	if !errors.Is(err, ErrPIDNotAdvertised) {
		t.Fatalf("priming ask returned %v, want ErrPIDNotAdvertised", err)
	}

	for _, c := range []struct {
		pid  rdm.ParameterID
		name string
	}{
		{rdm.PIDDMXStartAddress, "DMX_START_ADDRESS"},
		{rdm.PIDIdentifyDevice, "IDENTIFY_DEVICE"},
	} {
		runAsync(t, clock, func() { _, err = client.getRaw(context.Background(), c.pid, nil) })
		if err != nil {
			t.Errorf("getRaw(%s) = %v, want success — E1.20 §10.4.1 forbids a device from "+
				"listing its required PIDs, so absence from SUPPORTED_PARAMETERS proves "+
				"nothing and must not gate", c.name, err)
		}
		if got := counter.count(c.pid); got != 1 {
			t.Errorf("%s reached the fixture %d times, want 1 — a gated required PID fails "+
				"silently, because the request is never sent", c.name, got)
		}
	}
}
