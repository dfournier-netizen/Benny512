package params

import (
	"context"
	"encoding/hex"
	"testing"

	"benny512/internal/rdm"
)

// Inverting a fixture's tilt is a checkbox. It was a hex field.
//
// PAN_INVERT, TILT_INVERT and PAN_TILT_SWAP reached the generic parameter
// editor as bare SelfDescribing:false descriptors, so the editor — which
// already renders a proper toggle for anything typed DS_BOOLEAN — had
// nothing to go on and fell back to "type the bytes yourself".
//
// The cause is structural, not an oversight about three PIDs. E1.20 §10.4.2
// defines PARAMETER_DESCRIPTION only for MANUFACTURER-SPECIFIC PIDs, so a
// standard PID can never be self-describing however precisely the standard
// specifies it. With only two states — "the device described it" and
// "unknown bytes" — every standard PID this app has no dedicated screen for
// lands in the second one, which is both wrong and unfalsifiable from the
// device's side. SpecDefined is the missing third state.
//
// Verified against ANSI E1.20-2025 primary text on 2026-09-09:
//
//	§10.10.1 PAN_INVERT      GET response PDL 0x01, PD "Off/On (0/1)"
//	§10.10.2 TILT_INVERT     SET request  PDL 0x01, same field
//	§10.10.3 PAN_TILT_SWAP   SET response PDL 0x00; GET and SET both allowed
//
// A note for the next person, because the repository misled me first: these
// are E1.20 PIDs. Several comments here cited E1.37-1, whose Table A-1
// contains no 0x0600-0x0602 at all.
func TestSpecKnownPIDs_PanTiltOrientationIsATypedBoolean(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0010}

	for _, c := range []struct {
		pid  rdm.ParameterID
		name string
	}{
		{rdm.PIDPanInvert, "PAN_INVERT"},
		{rdm.PIDTiltInvert, "TILT_INVERT"},
		{rdm.PIDPanTiltSwap, "PAN_TILT_SWAP"},
	} {
		// The device advertises the PID but, being a conforming responder,
		// NACKs PARAMETER_DESCRIPTION for a standard PID.
		supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{c.pid})
		client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
			switch msg.ParameterID {
			case rdm.PIDSupportedParameters:
				return supported, false, 0
			case c.pid:
				return []byte{0x01}, false, 0
			default:
				return nil, true, rdm.NackUnknownPID
			}
		})

		var desc ParamDescriptor
		runAsync(t, clock, func() { desc = client.DescribeParam(context.Background(), c.pid) })

		if !desc.Typed() {
			t.Errorf("%s: descriptor is not typed, so the editor renders a raw-hex box and "+
				"an operator inverts a fixture by typing 01", c.name)
			continue
		}
		if desc.SelfDescribing {
			t.Errorf("%s: claims SelfDescribing, but E1.20 §10.4.2 allows PARAMETER_DESCRIPTION "+
				"only for manufacturer PIDs — the device never described this", c.name)
		}
		if !desc.SpecDefined {
			t.Errorf("%s: not marked SpecDefined, so the UI cannot tell a standard-derived "+
				"layout from a guess and will keep the 'raw / unverified' warning", c.name)
		}
		if desc.DataType != rdm.DSBoolean {
			t.Errorf("%s: DataType = %v, want DS_BOOLEAN — this is what selects the toggle",
				c.name, desc.DataType)
		}
		if desc.PDLSize != 1 {
			t.Errorf("%s: PDLSize = %d, want 1 (E1.20 §10.10.x: PDL 0x01)", c.name, desc.PDLSize)
		}
		if !desc.CommandClass.SupportsSet() || !desc.CommandClass.SupportsGet() {
			t.Errorf("%s: command class %v does not allow both GET and SET, which the "+
				"standard does — a read-only render would be wrong", c.name, desc.CommandClass)
		}
	}
}

// TestSpecKnownPIDs_SetsOneByteNotHex is the half that reaches the wire.
//
// The three `!desc.SelfDescribing` gates (GetParam, SetParam, and the web
// layer's decodeSetValue) each meant "we have no typed layout". Adding a
// second source of layout knowledge without giving that question a single
// name would have left a descriptor that RENDERS as a toggle but still
// demands raw []byte on SET — a control that looks right and refuses to
// work. ParamDescriptor.Typed is that single name; this test is what fails
// if a fourth gate is ever added spelling it the old way.
func TestSpecKnownPIDs_SetsOneByteNotHex(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0011}
	var sent []byte

	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDTiltInvert})
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch {
		case msg.ParameterID == rdm.PIDSupportedParameters:
			return supported, false, 0
		case msg.ParameterID == rdm.PIDTiltInvert && msg.CommandClass == rdm.SetCommand:
			sent = append([]byte{}, msg.ParameterData...)
			return nil, false, 0
		case msg.ParameterID == rdm.PIDTiltInvert:
			return []byte{0x00}, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	// The value the UI's toggle produces for a DS_BOOLEAN row: a number, not
	// a hex string.
	var err error
	runAsync(t, clock, func() { err = client.SetParam(context.Background(), rdm.PIDTiltInvert, int64(1)) })
	if err != nil {
		t.Fatalf("SetParam(TILT_INVERT, 1) = %v — a typed boolean SET must not require raw bytes", err)
	}
	if got := hex.EncodeToString(sent); got != "01" {
		t.Errorf("SET TILT_INVERT payload = %q (PDL %d), want \"01\" (PDL 1) per E1.20 §10.10.2",
			got, len(sent))
	}

	// And the GET decodes as a value, not as opaque bytes.
	var val ParamValue
	runAsync(t, clock, func() { val, _, err = client.GetParam(context.Background(), rdm.PIDTiltInvert) })
	if err != nil {
		t.Fatalf("GetParam(TILT_INVERT): %v", err)
	}
	if val.Kind == ParamValueRaw {
		t.Errorf("GET TILT_INVERT decoded as raw bytes; the editor needs an int to set the "+
			"toggle's checked state, and will render the hex box instead. value=%+v", val)
	}
}

// TestSpecKnownPIDs_DoNotReachTheWireForParameterDescription pins the saving
// that comes with the fix, and the reason the table sits ahead of the
// not-describable branch rather than behind it.
//
// Both paths avoid sending PARAMETER_DESCRIPTION for a standard PID — that
// was already right, and RDM-LOG4 is why. The difference is what the app
// knows afterwards: the old branch produced "we know nothing", this one
// produces the layout. Asking anyway would earn a NACK from a conforming
// responder, so the count here must stay zero.
func TestSpecKnownPIDs_DoNotReachTheWireForParameterDescription(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x4744, DeviceID: 0x0012}
	paramDescGets := 0

	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDPanInvert})
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDParameterDescription {
			paramDescGets++
			return nil, true, rdm.NackUnknownPID
		}
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return supported, false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})

	for i := 0; i < 3; i++ {
		runAsync(t, clock, func() { client.DescribeParam(context.Background(), rdm.PIDPanInvert) })
	}
	if paramDescGets != 0 {
		t.Errorf("PARAMETER_DESCRIPTION was sent %d time(s) for PAN_INVERT — E1.20 §10.4.2 "+
			"defines that message only for manufacturer-specific PIDs, so a conforming "+
			"device NACKs it and the transaction is pure waste", paramDescGets)
	}
}
