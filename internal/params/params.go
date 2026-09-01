// Package params provides typed helpers over session.RDMController for the
// Fixtures screen: encode/decode of the handful of well-known RDM PIDs Dom
// needs, layered over the controller's Get/Set so callers work with typed
// Go values and the controller's own typed errors instead of raw bytes.
//
// Layering rule: this package touches no socket and owns no state; it is a
// thin, pure encode/decode + Get/Set wrapper, tested against
// session.FakeTransport exactly like the engines it wraps.
package params

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// ErrBadLength is returned when a GET response's Parameter Data isn't the
// length the PID's struct requires.
var ErrBadLength = errors.New("params: unexpected parameter data length")

// DeviceInfo is RDM's DEVICE_INFO (PID 0x0060) parameter, the 19-byte
// struct verified byte-for-byte in the Phase-1a wire-format report
// (phase1a-wire-format-verification_2026-08-04_2305.md §3.9).
type DeviceInfo struct {
	ProtocolVersionMajor byte
	ProtocolVersionMinor byte
	DeviceModelID        uint16
	ProductCategory      uint16
	SoftwareVersionID    uint32
	DMXFootprint         uint16
	CurrentPersonality   byte
	PersonalityCount     byte
	DMXStartAddress      uint16
	SubDeviceCount       uint16
	SensorCount          byte
}

// DecodeDeviceInfo parses the 19-byte DEVICE_INFO parameter data. Layout
// (golden fixture, Phase-1a report §3.9): RDM Protocol Version (2), Device
// Model ID (2), Product Category (2), Software Version ID (4), DMX
// Footprint (2), Current Personality (1), Personality Count (1), DMX Start
// Address (2), Sub-Device Count (2), Sensor Count (1) — all big-endian.
func DecodeDeviceInfo(data []byte) (DeviceInfo, error) {
	if len(data) != 19 {
		return DeviceInfo{}, fmt.Errorf("%w: DEVICE_INFO wants 19 bytes, got %d", ErrBadLength, len(data))
	}
	return DeviceInfo{
		ProtocolVersionMajor: data[0],
		ProtocolVersionMinor: data[1],
		DeviceModelID:        binary.BigEndian.Uint16(data[2:4]),
		ProductCategory:      binary.BigEndian.Uint16(data[4:6]),
		SoftwareVersionID:    binary.BigEndian.Uint32(data[6:10]),
		DMXFootprint:         binary.BigEndian.Uint16(data[10:12]),
		CurrentPersonality:   data[12],
		PersonalityCount:     data[13],
		DMXStartAddress:      binary.BigEndian.Uint16(data[14:16]),
		SubDeviceCount:       binary.BigEndian.Uint16(data[16:18]),
		SensorCount:          data[18],
	}, nil
}

// EncodeDeviceInfo is the inverse of DecodeDeviceInfo, mostly useful for
// tests and for the demo-mode fake responder.
func EncodeDeviceInfo(d DeviceInfo) []byte {
	b := make([]byte, 19)
	b[0], b[1] = d.ProtocolVersionMajor, d.ProtocolVersionMinor
	binary.BigEndian.PutUint16(b[2:4], d.DeviceModelID)
	binary.BigEndian.PutUint16(b[4:6], d.ProductCategory)
	binary.BigEndian.PutUint32(b[6:10], d.SoftwareVersionID)
	binary.BigEndian.PutUint16(b[10:12], d.DMXFootprint)
	b[12] = d.CurrentPersonality
	b[13] = d.PersonalityCount
	binary.BigEndian.PutUint16(b[14:16], d.DMXStartAddress)
	binary.BigEndian.PutUint16(b[16:18], d.SubDeviceCount)
	b[18] = d.SensorCount
	return b
}

// Client wraps an RDMController with typed per-PID methods aimed at one
// responder through one node port.
type Client struct {
	ctrl *session.RDMController
	node session.NodeRef
	uid  rdm.UID
}

// New builds a params.Client for one responder.
func New(ctrl *session.RDMController, node session.NodeRef, uid rdm.UID) *Client {
	return &Client{ctrl: ctrl, node: node, uid: uid}
}

// ErrResetDeviceHasNoGet is returned by getRaw for any attempt to GET
// RESET_DEVICE (0x1001) — E1.20 §10.11.2 defines SET_COMMAND only for this
// PID, no GET form exists at all. This is a hard, spec-wide fact (unlike
// the SUPPORTED_PARAMETERS-driven speculative gate below, it applies
// whether or not the device advertises the PID), so it's checked first and
// unconditionally: no GET for RESET_DEVICE ever reaches the wire through
// this package, from any caller, by any path.
var ErrResetDeviceHasNoGet = errors.New("params: RESET_DEVICE is SET_COMMAND only (E1.20 §10.11.2); GET is not defined")

// ErrSlotDescriptionNeedsIndex is returned by getRaw for a SLOT_DESCRIPTION
// (0x0121) GET whose request data isn't the required 2-byte slot number
// (E1.20 §10.7.2) — sending PDL=0x00 to ask "the" slot description, the way
// a bare GET_COMMAND with no payload does, is a malformed request every
// real responder correctly NACKs FORMAT_ERROR for.
var ErrSlotDescriptionNeedsIndex = errors.New("params: SLOT_DESCRIPTION GET requires a 2-byte slot number")

// ErrPIDNotAdvertised is returned by getRaw when a speculative PID (see
// isSpeculativePID in introspect.go) is known NOT to be in this device's own
// SUPPORTED_PARAMETERS report — either because a fresh GET SUPPORTED_
// PARAMETERS just confirmed that, or because a past live GET for this exact
// PID already came back NACK UNKNOWN_PID. Phase D task 3: this is what stops
// Benny512 from repeating a probe a device has already told it will always
// fail. It is deliberately NOT returned when SUPPORTED_PARAMETERS itself is
// unknown/unsupported — see ensureAdvertised's doc comment in introspect.go.
var ErrPIDNotAdvertised = errors.New("params: PID not advertised in this device's SUPPORTED_PARAMETERS")

// getRaw issues a GET and returns the raw ACK data, translating any
// non-ACK result into an error (NackError for NACK, session's own typed
// errors for timeout/deadline/etc).
func (c *Client) getRaw(ctx context.Context, pid rdm.ParameterID, data []byte) ([]byte, error) {
	if pid == rdm.PIDResetDevice {
		return nil, ErrResetDeviceHasNoGet
	}
	if pid == rdm.PIDSlotDescription && len(data) != 2 {
		return nil, ErrSlotDescriptionNeedsIndex
	}
	if isSpeculativePID(pid) {
		if err := c.ensureAdvertised(ctx, pid); err != nil {
			return nil, err
		}
	}
	cmd := c.ctrl.Get(c.node, c.uid, pid, data)
	res, err := cmd.Await(ctx)
	if err != nil {
		return nil, err
	}
	if res.Kind != session.ResultAck {
		if res.Kind == session.ResultNack && isSpeculativePID(pid) {
			var nackErr *session.NackError
			if errors.As(res.Err, &nackErr) && nackErr.Reason == rdm.NackUnknownPID {
				c.rememberUnsupported(pid)
			}
		}
		if res.Err != nil {
			return nil, res.Err
		}
		return nil, fmt.Errorf("params: GET 0x%04X returned %s", uint16(pid), res.Kind)
	}
	return res.Data, nil
}

func (c *Client) setRaw(ctx context.Context, pid rdm.ParameterID, data []byte) error {
	cmd := c.ctrl.Set(c.node, c.uid, pid, data)
	res, err := cmd.Await(ctx)
	if err != nil {
		return err
	}
	if res.Kind != session.ResultAck {
		if res.Err != nil {
			return res.Err
		}
		return fmt.Errorf("params: SET 0x%04X returned %s", uint16(pid), res.Kind)
	}
	return nil
}

// DeviceInfo issues GET DEVICE_INFO and decodes the response.
func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	data, err := c.getRaw(ctx, rdm.PIDDeviceInfo, nil)
	if err != nil {
		return DeviceInfo{}, err
	}
	return DecodeDeviceInfo(data)
}

// DMXStartAddress issues GET DMX_START_ADDRESS (2-byte big-endian, 1-based).
func (c *Client) DMXStartAddress(ctx context.Context) (uint16, error) {
	data, err := c.getRaw(ctx, rdm.PIDDMXStartAddress, nil)
	if err != nil {
		return 0, err
	}
	if len(data) != 2 {
		return 0, fmt.Errorf("%w: DMX_START_ADDRESS wants 2 bytes, got %d", ErrBadLength, len(data))
	}
	return binary.BigEndian.Uint16(data), nil
}

// SetDMXStartAddress issues SET DMX_START_ADDRESS. addr must be 1-512.
func (c *Client) SetDMXStartAddress(ctx context.Context, addr uint16) error {
	if addr < 1 || addr > 512 {
		return fmt.Errorf("params: DMX start address out of range: %d", addr)
	}
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, addr)
	return c.setRaw(ctx, rdm.PIDDMXStartAddress, b)
}

// Personality is DMX_PERSONALITY's GET response: current index + count.
type Personality struct {
	Current byte
	Count   byte
}

// DMXPersonality issues GET DMX_PERSONALITY.
func (c *Client) DMXPersonality(ctx context.Context) (Personality, error) {
	data, err := c.getRaw(ctx, rdm.PIDDMXPersonality, nil)
	if err != nil {
		return Personality{}, err
	}
	if len(data) != 2 {
		return Personality{}, fmt.Errorf("%w: DMX_PERSONALITY wants 2 bytes, got %d", ErrBadLength, len(data))
	}
	return Personality{Current: data[0], Count: data[1]}, nil
}

// SetDMXPersonality issues SET DMX_PERSONALITY with a 1-based personality
// index.
func (c *Client) SetDMXPersonality(ctx context.Context, index byte) error {
	return c.setRaw(ctx, rdm.PIDDMXPersonality, []byte{index})
}

// PersonalityDescription is DMX_PERSONALITY_DESCRIPTION's (0x00E1) GET
// response for one personality index (report brief: verified against E1.20
// §6.5.5's request/response shape, plus internal/rdm/dimmer.go's
// IndexedDescription doc comment, which already documents CURVE_DESCRIPTION
// et al. as "DMX_PERSONALITY_DESCRIPTION's shape minus the footprint field"
// — i.e. this layout was already the established sibling pattern in this
// codebase before this type had its own codec). Wire layout:
//
//	0    personality (echoed)  UINT8
//	1-2  DMX slots required    UINT16 BE
//	3-.. description           ASCII, no NUL terminator, length = PDL-3, max 32
type PersonalityDescription struct {
	Index        byte
	DMXFootprint uint16
	Description  string
}

// ErrBadPersonalityDescription is returned when DMX_PERSONALITY_DESCRIPTION
// parameter data is shorter than its 3-byte fixed portion.
var ErrBadPersonalityDescription = fmt.Errorf("%w: DMX_PERSONALITY_DESCRIPTION", ErrBadLength)

// DecodePersonalityDescription parses a DMX_PERSONALITY_DESCRIPTION GET
// response. The GET request itself is just the 1-byte personality number
// being asked about (rdm.EncodeSensorNumberRequest-shaped — a bare index —
// so no dedicated encoder is needed; callers pass []byte{index} directly,
// as DMXPersonalityDescription below does).
func DecodePersonalityDescription(data []byte) (PersonalityDescription, error) {
	if len(data) < 3 {
		return PersonalityDescription{}, fmt.Errorf("%w: wants >=3 bytes, got %d", ErrBadPersonalityDescription, len(data))
	}
	return PersonalityDescription{
		Index:        data[0],
		DMXFootprint: binary.BigEndian.Uint16(data[1:3]),
		Description:  string(data[3:]),
	}, nil
}

// EncodePersonalityDescription is the inverse of DecodePersonalityDescription,
// mostly useful for tests and the demo-mode fake responder. Description
// longer than 32 bytes is truncated (RDM label convention, matching
// EncodeParameterDescription/EncodeSensorDefinition in package rdm).
func EncodePersonalityDescription(d PersonalityDescription) []byte {
	desc := d.Description
	if len(desc) > 32 {
		desc = desc[:32]
	}
	b := make([]byte, 3+len(desc))
	b[0] = d.Index
	binary.BigEndian.PutUint16(b[1:3], d.DMXFootprint)
	copy(b[3:], desc)
	return b
}

// DMXPersonalityDescription issues GET DMX_PERSONALITY_DESCRIPTION for one
// index.
func (c *Client) DMXPersonalityDescription(ctx context.Context, index byte) (PersonalityDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDDMXPersonalityDescription, []byte{index})
	if err != nil {
		return PersonalityDescription{}, err
	}
	return DecodePersonalityDescription(data)
}

// label helpers -------------------------------------------------------------

func (c *Client) getLabel(ctx context.Context, pid rdm.ParameterID) (string, error) {
	data, err := c.getRaw(ctx, pid, nil)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) setLabel(ctx context.Context, pid rdm.ParameterID, label string) error {
	if len(label) > 32 {
		label = label[:32]
	}
	return c.setRaw(ctx, pid, []byte(label))
}

// DeviceLabel issues GET DEVICE_LABEL.
func (c *Client) DeviceLabel(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDDeviceLabel)
}

// SetDeviceLabel issues SET DEVICE_LABEL. RDM labels are ASCII, max 32
// bytes; longer input is truncated rather than rejected, matching how most
// consoles behave.
func (c *Client) SetDeviceLabel(ctx context.Context, label string) error {
	return c.setLabel(ctx, rdm.PIDDeviceLabel, label)
}

// ManufacturerLabel issues GET MANUFACTURER_LABEL.
func (c *Client) ManufacturerLabel(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDManufacturerLabel)
}

// DeviceModelDescription issues GET DEVICE_MODEL_DESCRIPTION.
func (c *Client) DeviceModelDescription(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDDeviceModelDescription)
}

// SoftwareVersionLabel issues GET SOFTWARE_VERSION_LABEL.
func (c *Client) SoftwareVersionLabel(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDSoftwareVersionLabel)
}

// IdentifyDevice issues GET IDENTIFY_DEVICE (1-byte bool: 0/1).
func (c *Client) IdentifyDevice(ctx context.Context) (bool, error) {
	data, err := c.getRaw(ctx, rdm.PIDIdentifyDevice, nil)
	if err != nil {
		return false, err
	}
	if len(data) != 1 {
		return false, fmt.Errorf("%w: IDENTIFY_DEVICE wants 1 byte, got %d", ErrBadLength, len(data))
	}
	return data[0] != 0, nil
}

// SetIdentifyDevice issues SET IDENTIFY_DEVICE.
func (c *Client) SetIdentifyDevice(ctx context.Context, on bool) error {
	v := byte(0)
	if on {
		v = 1
	}
	return c.setRaw(ctx, rdm.PIDIdentifyDevice, []byte{v})
}

// --- E1.37-1 dimmer PIDs -----------------------------------------------
//
// Typed helpers for the "dimmer curve" family the owner asked for by name
// (rdm-pids-sensors-research_2026-08-13_2347.md §6.1). See
// internal/rdm/dimmer.go's file doc comment for the TODO(hardware) caveat
// that applies to every wire layout in this section: the research report
// confirms these PIDs' *numbers* but not their byte layouts.

// Curve issues GET CURVE (0x0343).
func (c *Client) Curve(ctx context.Context) (rdm.IndexedChoice, error) {
	data, err := c.getRaw(ctx, rdm.PIDCurve, nil)
	if err != nil {
		return rdm.IndexedChoice{}, err
	}
	return rdm.DecodeCurve(data)
}

// SetCurve issues SET CURVE with a 1-based curve index.
func (c *Client) SetCurve(ctx context.Context, index byte) error {
	return c.setRaw(ctx, rdm.PIDCurve, rdm.EncodeIndexedChoiceSet(index))
}

// CurveDescription issues GET CURVE_DESCRIPTION for one curve index.
func (c *Client) CurveDescription(ctx context.Context, index byte) (rdm.IndexedDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDCurveDescription, rdm.EncodeIndexedDescriptionRequest(index))
	if err != nil {
		return rdm.IndexedDescription{}, err
	}
	return rdm.DecodeCurveDescription(data)
}

// OutputResponseTime issues GET OUTPUT_RESPONSE_TIME (0x0345).
func (c *Client) OutputResponseTime(ctx context.Context) (rdm.IndexedChoice, error) {
	data, err := c.getRaw(ctx, rdm.PIDOutputResponseTime, nil)
	if err != nil {
		return rdm.IndexedChoice{}, err
	}
	return rdm.DecodeOutputResponseTime(data)
}

// SetOutputResponseTime issues SET OUTPUT_RESPONSE_TIME with a 1-based index.
func (c *Client) SetOutputResponseTime(ctx context.Context, index byte) error {
	return c.setRaw(ctx, rdm.PIDOutputResponseTime, rdm.EncodeIndexedChoiceSet(index))
}

// OutputResponseTimeDescription issues GET OUTPUT_RESPONSE_TIME_DESCRIPTION
// for one index.
func (c *Client) OutputResponseTimeDescription(ctx context.Context, index byte) (rdm.IndexedDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDOutputResponseTimeDescription, rdm.EncodeIndexedDescriptionRequest(index))
	if err != nil {
		return rdm.IndexedDescription{}, err
	}
	return rdm.DecodeOutputResponseTimeDescription(data)
}

// ModulationFrequency issues GET MODULATION_FREQUENCY (0x0347).
func (c *Client) ModulationFrequency(ctx context.Context) (rdm.IndexedChoice, error) {
	data, err := c.getRaw(ctx, rdm.PIDModulationFrequency, nil)
	if err != nil {
		return rdm.IndexedChoice{}, err
	}
	return rdm.DecodeModulationFrequency(data)
}

// SetModulationFrequency issues SET MODULATION_FREQUENCY with a 1-based index.
func (c *Client) SetModulationFrequency(ctx context.Context, index byte) error {
	return c.setRaw(ctx, rdm.PIDModulationFrequency, rdm.EncodeIndexedChoiceSet(index))
}

// ModulationFrequencyDescription issues GET MODULATION_FREQUENCY_DESCRIPTION
// for one index.
func (c *Client) ModulationFrequencyDescription(ctx context.Context, index byte) (rdm.IndexedDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDModulationFrequencyDescription, rdm.EncodeIndexedDescriptionRequest(index))
	if err != nil {
		return rdm.IndexedDescription{}, err
	}
	return rdm.DecodeModulationFrequencyDescription(data)
}

// MinimumLevel issues GET MINIMUM_LEVEL (0x0341).
func (c *Client) MinimumLevel(ctx context.Context) (rdm.MinimumLevel, error) {
	data, err := c.getRaw(ctx, rdm.PIDMinimumLevel, nil)
	if err != nil {
		return rdm.MinimumLevel{}, err
	}
	return rdm.DecodeMinimumLevel(data)
}

// SetMinimumLevel issues SET MINIMUM_LEVEL.
func (c *Client) SetMinimumLevel(ctx context.Context, v rdm.MinimumLevel) error {
	return c.setRaw(ctx, rdm.PIDMinimumLevel, rdm.EncodeMinimumLevel(v))
}

// MaximumLevel issues GET MAXIMUM_LEVEL (0x0342).
func (c *Client) MaximumLevel(ctx context.Context) (uint16, error) {
	data, err := c.getRaw(ctx, rdm.PIDMaximumLevel, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeMaximumLevel(data)
}

// SetMaximumLevel issues SET MAXIMUM_LEVEL.
func (c *Client) SetMaximumLevel(ctx context.Context, v uint16) error {
	return c.setRaw(ctx, rdm.PIDMaximumLevel, rdm.EncodeMaximumLevel(v))
}

// IdentifyMode issues GET IDENTIFY_MODE (0x1040).
func (c *Client) IdentifyMode(ctx context.Context) (rdm.IdentifyMode, error) {
	data, err := c.getRaw(ctx, rdm.PIDIdentifyMode, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeIdentifyMode(data)
}

// SetIdentifyMode issues SET IDENTIFY_MODE.
func (c *Client) SetIdentifyMode(ctx context.Context, m rdm.IdentifyMode) error {
	return c.setRaw(ctx, rdm.PIDIdentifyMode, rdm.EncodeIdentifyMode(m))
}
