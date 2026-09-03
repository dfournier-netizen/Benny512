package params

import (
	"testing"

	"benny512/internal/rdm"
)

// TestIsSpeculativePID_PanTiltOrientationIsGated pins the change made after
// bench capture RDM-LOG24, and — more importantly — pins the line it must not
// cross.
//
// LOG24: eight GLP JDC-1s, 23 minutes. Benny512 asked each of them for
// PAN_INVERT and PAN_TILT_SWAP nine times and was NACKed UNKNOWN_PID every
// time — 18 of that capture's 26 NACKs, roughly 16% of all RDM traffic on the
// line spent asking questions the fixture had already answered. Its
// SUPPORTED_PARAMETERS advertises TILT_INVERT and neither of the other two,
// which is correct: a JDC-1 tilts but does not pan.
//
// That is only a safe thing to gate because all three are OPTIONAL. E1.20
// §10.4.1 says a device SHALL NOT report its minimum-support PIDs in
// SUPPORTED_PARAMETERS at all, so for a REQUIRED PID absence carries no
// information whatsoever and gating on it would silently stop asking for
// something every device implements. The second half of this test is the one
// that matters: it fails if anyone widens the gate across that line.
//
// Honesty note: the first half asserts the contents of a switch this change
// added, so it guards against the entry being REMOVED rather than proving a
// fix. The second half fails against a genuinely wrong change, and was
// verified by temporarily adding DMX_START_ADDRESS to the gate.
func TestIsSpeculativePID_PanTiltOrientationIsGated(t *testing.T) {
	gated := []struct {
		pid  rdm.ParameterID
		name string
		why  string
	}{
		{rdm.PIDPanInvert, "PAN_INVERT", "optional per E1.37-1; NACKed 9x by a JDC-1 in RDM-LOG24"},
		{rdm.PIDTiltInvert, "TILT_INVERT", "optional per E1.37-1; gated per-PID, so a fixture that advertises it is still asked"},
		{rdm.PIDPanTiltSwap, "PAN_TILT_SWAP", "optional per E1.37-1; NACKed 9x by a JDC-1 in RDM-LOG24"},
	}
	for _, c := range gated {
		if !isSpeculativePID(c.pid) {
			t.Errorf("%s (0x%04X) is not gated, so every caller probes it blind on a fixture that never advertised it — %s",
				c.name, uint16(c.pid), c.why)
		}
	}

	// The line the gate must never cross. Each of these is in E1.20's
	// minimum-support list, which the spec forbids a device from reporting in
	// SUPPORTED_PARAMETERS — so absence proves nothing and gating on it would
	// stop us asking for something every responder implements. The JDC-1's
	// real 21-PID list in LOG24 contains none of these, and Benny512 still
	// reads its DMX start address correctly, which is the whole point.
	required := []struct {
		pid  rdm.ParameterID
		name string
	}{
		{rdm.PIDDeviceInfo, "DEVICE_INFO"},
		{rdm.PIDDMXStartAddress, "DMX_START_ADDRESS"},
		{rdm.PIDSupportedParameters, "SUPPORTED_PARAMETERS"},
		{rdm.PIDIdentifyDevice, "IDENTIFY_DEVICE"},
		{rdm.PIDSoftwareVersionLabel, "SOFTWARE_VERSION_LABEL"},
	}
	for _, c := range required {
		if isSpeculativePID(c.pid) {
			t.Errorf("%s (0x%04X) is gated, but E1.20 §10.4.1 says a device SHALL NOT list its "+
				"minimum-support PIDs in SUPPORTED_PARAMETERS — absence carries no information, so "+
				"gating this stops us asking for something every responder implements",
				c.name, uint16(c.pid))
		}
	}
}
