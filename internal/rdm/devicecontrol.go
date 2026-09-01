package rdm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// This file adds typed codecs for the remainder of E1.20-2025 §10.11
// "Device Control Parameter Messages" that internal/rdm/lampstate.go's
// RESET_DEVICE codec (§10.11.2) and message.go's IDENTIFY_DEVICE (§10.11.1)
// didn't already cover: POWER_STATE (§10.11.3), PERFORM_SELFTEST
// (§10.11.4), SELF_TEST_DESCRIPTION (§10.11.5), CAPTURE_PRESET (§10.11.6),
// PRESET_PLAYBACK (§10.11.7) and SELFTEST_ENHANCED (§10.11.8). Every wire
// layout here is transcribed directly from the ANSI E1.20-2025 PDF text
// (§10.11.3-10.11.8) and Appendix A's Table A-3 (GET/SET Allowed), Table
// A-7 (Preset Playback Defines), Table A-10 (Self Test Defines), Table A-11
// (Power State Defines), Table 10-3 (Self Test Capability) and Table 10-4
// (Self Test Status) — CONFIRMED, not inferred.

// ErrBadDeviceControlLength is returned when a §10.11 device-control PID's
// parameter data isn't the length its wire layout requires.
var ErrBadDeviceControlLength = errors.New("rdm: unexpected device-control parameter data length")

// --- POWER_STATE (0x1010), E1.20 §10.11.3 -----------------------------------

// PowerState is POWER_STATE's 1-byte enum, per E1.20 Table A-11.
type PowerState byte

// Power State Defines (Table A-11).
const (
	PowerStateFullOff  PowerState = 0x00 // "Completely disengages power to device. Device can no longer respond."
	PowerStateShutdown PowerState = 0x01 // "Reduced power mode, may require device reset to return to normal operation. Device still responds to messages."
	PowerStateStandby  PowerState = 0x02 // "Reduced power mode. Device can return to NORMAL without a reset. Device still responds to messages."
	PowerStateNormal   PowerState = 0xFF // "Normal Operating Mode."
)

// String renders a human label for the four Table A-11 values, and a hex
// fallback for anything else (the table defines no other values, but a
// device could still misbehave and report one).
func (p PowerState) String() string {
	switch p {
	case PowerStateFullOff:
		return "Full Off"
	case PowerStateShutdown:
		return "Shutdown"
	case PowerStateStandby:
		return "Standby"
	case PowerStateNormal:
		return "Normal"
	default:
		return fmt.Sprintf("Unknown (0x%02X)", byte(p))
	}
}

// IsValid reports whether p is one of the four Table A-11 values — the only
// ones E1.20 defines for a SET.
func (p PowerState) IsValid() bool {
	switch p {
	case PowerStateFullOff, PowerStateShutdown, PowerStateStandby, PowerStateNormal:
		return true
	}
	return false
}

// DecodePowerState decodes POWER_STATE's 1-byte GET response.
func DecodePowerState(data []byte) (PowerState, error) {
	if len(data) != 1 {
		return 0, fmt.Errorf("%w: POWER_STATE wants 1 byte, got %d", ErrBadDeviceControlLength, len(data))
	}
	return PowerState(data[0]), nil
}

// EncodePowerState is DecodePowerState's inverse, also used for the SET
// request (E1.20 §10.11.3: GET and SET share the same 1-byte PD shape).
func EncodePowerState(p PowerState) []byte { return []byte{byte(p)} }

// --- PERFORM_SELFTEST (0x1020), E1.20 §10.11.4 ------------------------------

// SelfTestNumber is PERFORM_SELFTEST's SET-request/SELF_TEST_DESCRIPTION's
// GET-request 1-byte value, per E1.20 Table A-10.
type SelfTestNumber byte

// Self Test Defines (Table A-10). Everything in between (0x01-0xFE) is
// "Various Manufacturer Self Tests" — no ESTA-defined label exists for any
// specific value in that range, per this app's own "never invent enum
// labels the spec doesn't define" rule.
const (
	SelfTestOff SelfTestNumber = 0x00 // "Turns Self Tests Off"
	SelfTestAll SelfTestNumber = 0xFF // "Self Test All, if applicable"
)

// DecodeSelfTestActive decodes PERFORM_SELFTEST's 1-byte GET response
// ("Self Tests Active TRUE/FALSE (1/0)").
func DecodeSelfTestActive(data []byte) (bool, error) {
	if len(data) != 1 {
		return false, fmt.Errorf("%w: PERFORM_SELFTEST wants 1 byte, got %d", ErrBadDeviceControlLength, len(data))
	}
	return data[0] != 0, nil
}

// EncodePerformSelfTest builds PERFORM_SELFTEST's 1-byte SET request PD (the
// self test number to run, or SelfTestOff to stop, or SelfTestAll).
func EncodePerformSelfTest(n SelfTestNumber) []byte { return []byte{byte(n)} }

// --- SELF_TEST_DESCRIPTION (0x1021), E1.20 §10.11.5 -------------------------

// SelfTestDescription is SELF_TEST_DESCRIPTION's GET response: the self
// test number echoed back plus its up-to-32-byte text label.
type SelfTestDescription struct {
	Number SelfTestNumber
	Label  string
}

// EncodeSelfTestDescriptionRequest builds SELF_TEST_DESCRIPTION's 1-byte GET
// request PD (the self test number being asked about) — this PID has no
// index-free form; a bare PDL=0x00 GET is a malformed request every real
// responder correctly NACKs FORMAT_ERROR for, the same footgun
// SLOT_DESCRIPTION's own guard (params.ErrSlotDescriptionNeedsIndex)
// exists to prevent.
func EncodeSelfTestDescriptionRequest(n SelfTestNumber) []byte { return []byte{byte(n)} }

// DecodeSelfTestDescription parses SELF_TEST_DESCRIPTION's 1-33 byte GET
// response: 1-byte Self Test # Requested + up to 32 bytes of text label.
func DecodeSelfTestDescription(data []byte) (SelfTestDescription, error) {
	if len(data) < 1 {
		return SelfTestDescription{}, fmt.Errorf("%w: SELF_TEST_DESCRIPTION wants >=1 byte, got %d", ErrBadDeviceControlLength, len(data))
	}
	return SelfTestDescription{
		Number: SelfTestNumber(data[0]),
		Label:  strings.TrimRight(string(data[1:]), "\x00"),
	}, nil
}

// --- CAPTURE_PRESET (0x1030), E1.20 §10.11.6 --------------------------------

// PresetTiming is CAPTURE_PRESET's optional fade/wait payload (E1.20
// §10.11.6: "Fade and Wait times for building sequences may also be
// included... in tenths of a second"). nil in EncodeCapturePreset means the
// 2-byte (scene-number-only) PDL form; non-nil means the 8-byte form.
type PresetTiming struct {
	UpFadeTime   uint16 // tenths of a second
	DownFadeTime uint16 // tenths of a second
	WaitTime     uint16 // tenths of a second
}

// EncodeCapturePreset builds CAPTURE_PRESET's SET request PD: either the
// 2-byte Scene#-only form, or the 8-byte form with timing appended, per
// whether timing is nil.
func EncodeCapturePreset(scene uint16, timing *PresetTiming) []byte {
	if timing == nil {
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, scene)
		return b
	}
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:2], scene)
	binary.BigEndian.PutUint16(b[2:4], timing.UpFadeTime)
	binary.BigEndian.PutUint16(b[4:6], timing.DownFadeTime)
	binary.BigEndian.PutUint16(b[6:8], timing.WaitTime)
	return b
}

// --- PRESET_PLAYBACK (0x1031), E1.20 §10.11.7 -------------------------------

// PresetPlaybackMode is PRESET_PLAYBACK's 16-bit Mode field, per E1.20
// Table A-7. Values 0x0001-0xFFFE are "Plays individual Scene #" — an
// open range, not an enum, so only the two sentinel values get names.
type PresetPlaybackMode uint16

// Preset Playback Defines (Table A-7).
const (
	PresetPlaybackOff PresetPlaybackMode = 0x0000 // "Returns to Normal DMX512 Input"
	PresetPlaybackAll PresetPlaybackMode = 0xFFFF // "Plays Scenes in Sequence if supported"
)

// String renders "Off"/"All" for the two sentinel values and "Scene N" for
// anything else (E1.20 §10.11.7: "Setting the Preset Playback Mode to an
// individual Scene number shall play an individual scene").
func (m PresetPlaybackMode) String() string {
	switch m {
	case PresetPlaybackOff:
		return "Off"
	case PresetPlaybackAll:
		return "All"
	default:
		return fmt.Sprintf("Scene %d", uint16(m))
	}
}

// PresetPlayback is PRESET_PLAYBACK's GET/SET Parameter Data: a 16-bit Mode
// plus an 8-bit master-fader Level (E1.20 §10.11.7: "0x00 means preset
// scaled at 0 and... 0xFF represents a Preset scaled at full").
type PresetPlayback struct {
	Mode  PresetPlaybackMode
	Level byte
}

// DecodePresetPlayback parses PRESET_PLAYBACK's 3-byte GET/SET-echo PD.
func DecodePresetPlayback(data []byte) (PresetPlayback, error) {
	if len(data) != 3 {
		return PresetPlayback{}, fmt.Errorf("%w: PRESET_PLAYBACK wants 3 bytes, got %d", ErrBadDeviceControlLength, len(data))
	}
	return PresetPlayback{Mode: PresetPlaybackMode(binary.BigEndian.Uint16(data[0:2])), Level: data[2]}, nil
}

// EncodePresetPlayback is DecodePresetPlayback's inverse, for SET requests.
func EncodePresetPlayback(p PresetPlayback) []byte {
	b := make([]byte, 3)
	binary.BigEndian.PutUint16(b[0:2], uint16(p.Mode))
	b[2] = p.Level
	return b
}

// --- SELFTEST_ENHANCED (0x1022), E1.20 §10.11.8 -----------------------------

// SelfTestStatus is one packed-list entry's Self Test Status byte, per
// E1.20 Table 10-4.
type SelfTestStatus byte

// Self Test Status Defines (Table 10-4).
const (
	SelfTestStatusNoSupport  SelfTestStatus = 0x00 // "Test Status not supported"
	SelfTestStatusNotRun     SelfTestStatus = 0x01 // "Test not run since last power cycle"
	SelfTestStatusAborted    SelfTestStatus = 0x02 // "Aborted/Reset"
	SelfTestStatusActive     SelfTestStatus = 0x03 // "Active/Running"
	SelfTestStatusPass       SelfTestStatus = 0x04 // "Complete - PASS"
	SelfTestStatusFail       SelfTestStatus = 0x05 // "Complete - FAIL"
	SelfTestStatusNoAnalysis SelfTestStatus = 0x06 // "Complete - No Analysis"
	SelfTestStatusResultCode SelfTestStatus = 0x07 // "Complete - Result Code Available"
	SelfTestStatusOther      SelfTestStatus = 0xFF // "Other - Refer to Manufacturer"
)

// String renders a human label for the Table 10-4 values.
func (s SelfTestStatus) String() string {
	switch s {
	case SelfTestStatusNoSupport:
		return "Not supported"
	case SelfTestStatusNotRun:
		return "Not run since last power cycle"
	case SelfTestStatusAborted:
		return "Aborted"
	case SelfTestStatusActive:
		return "Active"
	case SelfTestStatusPass:
		return "Pass"
	case SelfTestStatusFail:
		return "Fail"
	case SelfTestStatusNoAnalysis:
		return "Complete (no analysis)"
	case SelfTestStatusResultCode:
		return "Complete (result code available)"
	case SelfTestStatusOther:
		return "Other (refer to manufacturer)"
	default:
		return fmt.Sprintf("Unknown (0x%02X)", byte(s))
	}
}

// Self Test Capability bits (Table 10-3) — a bit-field, not an enum;
// SelfTestEnhancedEntry.Capability is tested against these with &.
const (
	SelfTestCapAutoTerminate      uint16 = 0x0001 // "Set if Self Test Auto-terminates. Clear if controller termination required."
	SelfTestCapDMXRestricted      uint16 = 0x0002 // "Set if DMX512 Start-Code operation is restricted during running of test."
	SelfTestCapRDMRestricted      uint16 = 0x0004 // "Set if RDM operation is restricted during running of test."
	SelfTestCapAllIgnoresAutoTerm uint16 = 0x0008 // "Set if SELF_TEST_ALL ignores individual test Auto-terminate flag."
	SelfTestCapMfrResultCodes     uint16 = 0x0010 // "Set if Manufacturer-Specific Result Codes available for this test."
	SelfTestCapStatusMessages     uint16 = 0x0020 // "Set if STATUS MESSAGES generated by this test."
)

// SelfTestEnhancedEntry is one packed-list element of SELFTEST_ENHANCED's
// GET response.
type SelfTestEnhancedEntry struct {
	Number     SelfTestNumber
	Status     SelfTestStatus
	Capability uint16
	// ResultCode is 0x0000 ("Not available") unless SelfTestCapMfrResultCodes
	// is set in Capability, per Table A-5/§10.11.8's Result Code Enumeration
	// text — Result Code label lookup itself (via ENUM_LABEL, keyed on
	// ManufacturerResultCodePID) is out of scope here; ManufacturerResultCodePID
	// is preserved for a future pass that wants it.
	ResultCode uint16
}

// SelfTestEnhanced is SELFTEST_ENHANCED's fully-reassembled GET response
// (internal/session's RDMController transparently reassembles any
// ACK_OVERFLOW sequence before params.Client.getRaw ever sees the data, so
// DecodeSelfTestEnhanced always operates on the complete packed list — see
// E1.20 §10.11.8: "Upon an ACK_OVERFLOW, the Parameter Data appearing in
// subsequent messages shall only contain the continued packed list
// elements", which concatenation preserves correctly since the header field
// is First-Response-Only).
type SelfTestEnhanced struct {
	// ManufacturerResultCodePID is 0x0000 when the responder declares no
	// manufacturer-specific Result Code enumeration PID (E1.20 §10.11.8:
	// "A Responder not supporting this feature shall set the field to
	// 0x0000").
	ManufacturerResultCodePID uint16
	Entries                   []SelfTestEnhancedEntry
}

// ErrBadSelfTestEnhanced is returned when SELFTEST_ENHANCED's parameter
// data doesn't fit its header-plus-6-byte-packed-list shape.
var ErrBadSelfTestEnhanced = fmt.Errorf("%w: SELFTEST_ENHANCED", ErrBadDeviceControlLength)

// DecodeSelfTestEnhanced parses SELFTEST_ENHANCED's (fully reassembled, see
// SelfTestEnhanced's doc comment) GET response: a 2-byte Manufacturer-
// Specific Result Code Enumeration PID header, followed by a packed list of
// 6-byte entries (Self Test # (1) + Self Test Status (1) + Self Test
// Capability (2) + Result Code (2)).
func DecodeSelfTestEnhanced(data []byte) (SelfTestEnhanced, error) {
	if len(data) < 2 {
		return SelfTestEnhanced{}, fmt.Errorf("%w: wants at least 2 bytes, got %d", ErrBadSelfTestEnhanced, len(data))
	}
	rest := data[2:]
	if len(rest)%6 != 0 {
		return SelfTestEnhanced{}, fmt.Errorf("%w: packed-list remainder %d bytes is not a multiple of 6", ErrBadSelfTestEnhanced, len(rest))
	}
	out := SelfTestEnhanced{
		ManufacturerResultCodePID: binary.BigEndian.Uint16(data[0:2]),
		Entries:                   make([]SelfTestEnhancedEntry, 0, len(rest)/6),
	}
	for i := 0; i+6 <= len(rest); i += 6 {
		out.Entries = append(out.Entries, SelfTestEnhancedEntry{
			Number:     SelfTestNumber(rest[i]),
			Status:     SelfTestStatus(rest[i+1]),
			Capability: binary.BigEndian.Uint16(rest[i+2 : i+4]),
			ResultCode: binary.BigEndian.Uint16(rest[i+4 : i+6]),
		})
	}
	return out, nil
}
