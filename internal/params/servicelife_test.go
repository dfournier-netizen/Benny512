package params

import (
	"context"
	"errors"
	"testing"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// TestClientServiceLifePIDs exercises the DEVICE_HOURS/LAMP_HOURS/
// LAMP_STRIKES/LAMP_STATE/DEVICE_POWER_CYCLES/FACTORY_DEFAULTS typed methods
// end to end through a scripted responder that also advertises all six PIDs
// in SUPPORTED_PARAMETERS, so Phase D task 3's speculative gate lets every
// call through — mirrors TestClientDimmerPIDs' pattern.
func TestClientServiceLifePIDs(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 1}
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{
		rdm.PIDDeviceHours, rdm.PIDLampHours, rdm.PIDLampStrikes, rdm.PIDLampState,
		rdm.PIDDevicePowerCycles, rdm.PIDFactoryDefaults,
	})
	values := map[rdm.ParameterID][]byte{
		rdm.PIDDeviceHours:       rdm.EncodeUint32Counter(1200),
		rdm.PIDLampHours:         rdm.EncodeUint32Counter(300),
		rdm.PIDLampStrikes:       rdm.EncodeUint32Counter(42),
		rdm.PIDLampState:         rdm.EncodeLampState(rdm.LampOn),
		rdm.PIDDevicePowerCycles: rdm.EncodeUint32Counter(7),
		rdm.PIDFactoryDefaults:   {0x00},
	}
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return supported, false, 0
		}
		v, ok := values[msg.ParameterID]
		if !ok {
			return nil, true, rdm.NackUnknownPID
		}
		if msg.CommandClass == rdm.SetCommand {
			values[msg.ParameterID] = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0
		}
		return v, false, 0
	})

	type result struct {
		deviceHours, lampHours, lampStrikes, powerCycles uint32
		lampState                                        rdm.LampState
		factoryDefaults                                  bool
		err                                              error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		if r.deviceHours, r.err = client.DeviceHours(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.lampHours, r.err = client.LampHours(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.lampStrikes, r.err = client.LampStrikes(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.lampState, r.err = client.LampState(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		if r.powerCycles, r.err = client.DevicePowerCycles(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		r.factoryDefaults, r.err = client.FactoryDefaults(context.Background())
		resCh <- r
	}()

	var got result
	runAsyncRecv(t, clock, resCh, &got)
	if got.err != nil {
		t.Fatalf("client call failed: %v", got.err)
	}
	if got.deviceHours != 1200 {
		t.Errorf("DeviceHours = %d, want 1200", got.deviceHours)
	}
	if got.lampHours != 300 {
		t.Errorf("LampHours = %d, want 300", got.lampHours)
	}
	if got.lampStrikes != 42 {
		t.Errorf("LampStrikes = %d, want 42", got.lampStrikes)
	}
	if got.lampState != rdm.LampOn {
		t.Errorf("LampState = %v, want LampOn", got.lampState)
	}
	if got.powerCycles != 7 {
		t.Errorf("DevicePowerCycles = %d, want 7", got.powerCycles)
	}
	if got.factoryDefaults {
		t.Errorf("FactoryDefaults = true, want false")
	}
}

// runAsyncRecv is runAsync's variant for a goroutine that reports its result
// on a channel rather than just signalling completion.
func runAsyncRecv[T any](t *testing.T, clock *session.FakeClock, ch chan T, out *T) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		*out = <-ch
		close(done)
	}()
	runAsync(t, clock, func() { <-done })
}

// TestResetDeviceRejectsInvalidMode proves ResetDevice validates its mode
// argument BEFORE sending anything on the wire — reverting the ==
// mode.IsValid() check (temporarily, by hand, during development of this
// test) let an arbitrary byte reach EncodeResetDevice/setRaw and the fake
// responder ACK it, which is exactly the "one stray request away from
// resetting a fixture with a nonsense mode byte" bug the check exists to
// prevent.
func TestResetDeviceRejectsInvalidMode(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 2}
	wireHit := false
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDResetDevice {
			wireHit = true
		}
		return nil, false, 0
	})
	_ = clock

	err := client.ResetDevice(context.Background(), rdm.ResetMode(0x42))
	if !errors.Is(err, ErrInvalidResetMode) {
		t.Fatalf("ResetDevice(0x42) error = %v, want ErrInvalidResetMode", err)
	}
	if wireHit {
		t.Fatal("ResetDevice sent an invalid mode byte on the wire")
	}
}

// TestResetDeviceWarmAndCold exercises the real SET RESET_DEVICE round trip
// for both spec-defined modes and confirms getRaw's ErrResetDeviceHasNoGet
// guard actually blocks a GET for the same PID — proving both halves of
// E1.20 §10.11.2's "SET_COMMAND only, no GET form" rule.
func TestResetDeviceWarmAndCold(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 3}
	var gotPD []byte
	var sawGet bool
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDResetDevice {
			if msg.CommandClass == rdm.GetCommand {
				sawGet = true
				return nil, true, rdm.NackUnsupportedCommandClass
			}
			gotPD = append([]byte(nil), msg.ParameterData...)
			return nil, false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})

	for _, tc := range []struct {
		mode rdm.ResetMode
		want byte
	}{
		{rdm.ResetWarm, 0x01},
		{rdm.ResetCold, 0xFF},
	} {
		var err error
		runAsync(t, clock, func() {
			err = client.ResetDevice(context.Background(), tc.mode)
		})
		if err != nil {
			t.Fatalf("ResetDevice(%v): %v", tc.mode, err)
		}
		if len(gotPD) != 1 || gotPD[0] != tc.want {
			t.Fatalf("ResetDevice(%v) sent PD %x, want [%02X]", tc.mode, gotPD, tc.want)
		}
	}

	// getRaw must never let a GET for RESET_DEVICE reach the wire at all —
	// this call must fail locally, with sawGet staying false, proving the
	// guard fires before any transaction is issued.
	_, err := client.getRaw(context.Background(), rdm.PIDResetDevice, nil)
	if !errors.Is(err, ErrResetDeviceHasNoGet) {
		t.Fatalf("getRaw(RESET_DEVICE) error = %v, want ErrResetDeviceHasNoGet", err)
	}
	if sawGet {
		t.Fatal("a GET for RESET_DEVICE reached the wire")
	}
}

// TestSlotDescriptionRequiresIndex proves getRaw refuses a SLOT_DESCRIPTION
// GET with no (or a wrong-length) request payload rather than sending the
// malformed PDL=0x00 request real hardware NACKs FORMAT_ERROR for.
func TestSlotDescriptionRequiresIndex(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 4}
	wireHit := false
	client, _ := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDSlotDescription {
			wireHit = true
		}
		return nil, true, rdm.NackUnknownPID
	})

	if _, err := client.getRaw(context.Background(), rdm.PIDSlotDescription, nil); !errors.Is(err, ErrSlotDescriptionNeedsIndex) {
		t.Fatalf("getRaw(SLOT_DESCRIPTION, nil) error = %v, want ErrSlotDescriptionNeedsIndex", err)
	}
	if _, err := client.getRaw(context.Background(), rdm.PIDSlotDescription, []byte{0x01}); !errors.Is(err, ErrSlotDescriptionNeedsIndex) {
		t.Fatalf("getRaw(SLOT_DESCRIPTION, 1 byte) error = %v, want ErrSlotDescriptionNeedsIndex", err)
	}
	if wireHit {
		t.Fatal("a malformed SLOT_DESCRIPTION GET reached the wire")
	}
}

// TestSpeculativeGateBlocksUnadvertisedPID is the direct, root-cause proof
// for Phase D task 3: once a device's SUPPORTED_PARAMETERS is known and
// omits a speculative PID, a later GET for that PID must be refused
// locally — never reach the wire — rather than repeating the NACK the bench
// capture found. It also proves the opposite: the SAME PID succeeds
// wire-for-wire when it IS advertised, and that a device with NO
// SUPPORTED_PARAMETERS support at all still gets to ask (today's behavior,
// preserved as the required fallback).
func TestSpeculativeGateBlocksUnadvertisedPID(t *testing.T) {
	t.Run("not advertised -> blocked without wire traffic", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 5}
		supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel}) // no PIDCurve
		curveGets := 0
		client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
			switch msg.ParameterID {
			case rdm.PIDSupportedParameters:
				return supported, false, 0
			case rdm.PIDCurve:
				curveGets++
				return []byte{1, 1}, false, 0
			default:
				return nil, true, rdm.NackUnknownPID
			}
		})
		var err error
		runAsync(t, clock, func() {
			_, err = client.Curve(context.Background())
		})
		if !errors.Is(err, ErrPIDNotAdvertised) {
			t.Fatalf("Curve() error = %v, want ErrPIDNotAdvertised", err)
		}
		if curveGets != 0 {
			t.Fatalf("GET CURVE reached the wire %d times; want 0 (device never advertised it)", curveGets)
		}
	})

	t.Run("advertised -> succeeds", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 6}
		supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDCurve})
		client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
			switch msg.ParameterID {
			case rdm.PIDSupportedParameters:
				return supported, false, 0
			case rdm.PIDCurve:
				return []byte{2, 3}, false, 0
			default:
				return nil, true, rdm.NackUnknownPID
			}
		})
		var choice rdm.IndexedChoice
		var err error
		runAsync(t, clock, func() {
			choice, err = client.Curve(context.Background())
		})
		if err != nil {
			t.Fatalf("Curve(): %v", err)
		}
		if choice != (rdm.IndexedChoice{Current: 2, Count: 3}) {
			t.Errorf("Curve() = %+v", choice)
		}
	})

	t.Run("SUPPORTED_PARAMETERS unanswered -> falls back to asking anyway", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 7}
		client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
			switch msg.ParameterID {
			case rdm.PIDSupportedParameters:
				return nil, true, rdm.NackUnknownPID // device doesn't implement it
			case rdm.PIDCurve:
				return []byte{1, 1}, false, 0
			default:
				return nil, true, rdm.NackUnknownPID
			}
		})
		var choice rdm.IndexedChoice
		var err error
		runAsync(t, clock, func() {
			choice, err = client.Curve(context.Background())
		})
		if err != nil {
			t.Fatalf("Curve(): %v (must fall back to asking when SUPPORTED_PARAMETERS is unavailable)", err)
		}
		if choice != (rdm.IndexedChoice{Current: 1, Count: 1}) {
			t.Errorf("Curve() = %+v", choice)
		}
	})

	t.Run("live UNKNOWN_PID NACK is remembered without a second GET SUPPORTED_PARAMETERS", func(t *testing.T) {
		uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 8}
		client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
			switch msg.ParameterID {
			case rdm.PIDSupportedParameters:
				return nil, true, rdm.NackUnknownPID
			case rdm.PIDCurve:
				return nil, true, rdm.NackUnknownPID
			default:
				return nil, true, rdm.NackUnknownPID
			}
		})
		var err error
		runAsync(t, clock, func() {
			_, err = client.Curve(context.Background())
		})
		var nackErr *session.NackError
		if !errors.As(err, &nackErr) || nackErr.Reason != rdm.NackUnknownPID {
			t.Fatalf("first Curve() error = %v, want a NACK UNKNOWN_PID", err)
		}

		// Second call: must be refused locally via the remembered-unsupported
		// cache, not sent again.
		runAsync(t, clock, func() {
			_, err = client.Curve(context.Background())
		})
		if !errors.Is(err, ErrPIDNotAdvertised) {
			t.Fatalf("second Curve() error = %v, want ErrPIDNotAdvertised (learned from the first NACK)", err)
		}
	})
}
