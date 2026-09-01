package params

import (
	"context"
	"errors"
	"fmt"

	"benny512/internal/rdm"
)

// This file adds typed params.Client methods for the remainder of E1.20
// §10.11 "Device Control Parameter Messages" — POWER_STATE (§10.11.3),
// PERFORM_SELFTEST (§10.11.4), SELF_TEST_DESCRIPTION (§10.11.5),
// CAPTURE_PRESET (§10.11.6), PRESET_PLAYBACK (§10.11.7) and
// SELFTEST_ENHANCED (§10.11.8) — following servicelife.go's pattern:
// getRaw/setRaw wrappers around internal/rdm/devicecontrol.go's codecs.
// IDENTIFY_DEVICE (§10.11.1) and RESET_DEVICE (§10.11.2) already have Client
// methods elsewhere (params.go / servicelife.go respectively) and are not
// duplicated here.

// PowerState issues GET POWER_STATE (0x1010).
func (c *Client) PowerState(ctx context.Context) (rdm.PowerState, error) {
	data, err := c.getRaw(ctx, rdm.PIDPowerState, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodePowerState(data)
}

// ErrInvalidPowerState is returned by SetPowerState for any value other
// than the four E1.20 Table A-11 states.
var ErrInvalidPowerState = errors.New("params: POWER_STATE value must be one of the four E1.20 Table A-11 states")

// SetPowerState issues SET POWER_STATE. Per E1.20 §10.11.3/Table A-11, a
// FULL_OFF or SHUTDOWN request can leave the responder unable to answer
// further messages until it is reset or power-cycled — callers (the HTTP
// layer) are expected to gate this behind an explicit confirmation naming
// the device, the same discipline RESET_DEVICE/FACTORY_DEFAULTS use.
func (c *Client) SetPowerState(ctx context.Context, mode rdm.PowerState) error {
	if !mode.IsValid() {
		return fmt.Errorf("%w: got 0x%02X", ErrInvalidPowerState, byte(mode))
	}
	return c.setRaw(ctx, rdm.PIDPowerState, rdm.EncodePowerState(mode))
}

// SelfTestActive issues GET PERFORM_SELFTEST (0x1020): whether any self
// test is currently running.
func (c *Client) SelfTestActive(ctx context.Context) (bool, error) {
	data, err := c.getRaw(ctx, rdm.PIDPerformSelfTest, nil)
	if err != nil {
		return false, err
	}
	return rdm.DecodeSelfTestActive(data)
}

// StartSelfTest issues SET PERFORM_SELFTEST with the given self test number
// (rdm.SelfTestOff to stop, rdm.SelfTestAll to run every declared test, or
// a manufacturer-specific 0x01-0xFE value). E1.20 §10.11.4's own example
// ("execute any built-in Self-Test routine") is explicitly one a fixture
// may use to "swing... through its full range" — disruptive mid-show even
// though it isn't destructive — so, like SetPowerState, this is expected to
// be gated behind an explicit confirmation at the HTTP layer for any test
// number other than SelfTestOff.
func (c *Client) StartSelfTest(ctx context.Context, n rdm.SelfTestNumber) error {
	return c.setRaw(ctx, rdm.PIDPerformSelfTest, rdm.EncodePerformSelfTest(n))
}

// ErrSelfTestDescriptionNeedsNumber guards against a bare, index-free GET
// SELF_TEST_DESCRIPTION the same way params.go's
// ErrSlotDescriptionNeedsIndex guards SLOT_DESCRIPTION — this package was
// bitten once already by sending an indexed-description GET with PDL=0.
// SelfTestDescription always supplies the 1-byte index itself, so this can
// only fire if a future caller starts calling getRaw directly for this PID.
var ErrSelfTestDescriptionNeedsNumber = errors.New("params: SELF_TEST_DESCRIPTION GET requires a 1-byte self test number")

// SelfTestDescription issues GET SELF_TEST_DESCRIPTION (0x1021) for one
// self test number, resolving its human-readable text label.
func (c *Client) SelfTestDescription(ctx context.Context, n rdm.SelfTestNumber) (rdm.SelfTestDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDSelfTestDescription, rdm.EncodeSelfTestDescriptionRequest(n))
	if err != nil {
		return rdm.SelfTestDescription{}, err
	}
	return rdm.DecodeSelfTestDescription(data)
}

// ErrCapturePresetHasNoGet is returned by getRaw for any attempt to GET
// CAPTURE_PRESET (0x1030) — E1.20 §10.11.6 and Table A-3 define SET_COMMAND
// only for this PID, exactly like RESET_DEVICE (see
// params.ErrResetDeviceHasNoGet).
var ErrCapturePresetHasNoGet = errors.New("params: CAPTURE_PRESET is SET_COMMAND only (E1.20 §10.11.6); GET is not defined")

// CapturePreset issues SET CAPTURE_PRESET: captures the responder's current
// static scene into its preset store at scene. timing is optional
// (E1.20 §10.11.6: "Fade and Wait times for building sequences may also be
// included") — nil sends the 2-byte scene-number-only form, non-nil sends
// the 8-byte form with fade/wait times attached. This overwrites whatever
// the device previously had stored at scene, unrecoverably from RDM's own
// perspective (the standard doesn't define a way to read a preset back) —
// callers are expected to gate this behind an explicit confirmation naming
// both the device and the scene number.
func (c *Client) CapturePreset(ctx context.Context, scene uint16, timing *rdm.PresetTiming) error {
	return c.setRaw(ctx, rdm.PIDCapturePreset, rdm.EncodeCapturePreset(scene, timing))
}

// PresetPlayback issues GET PRESET_PLAYBACK (0x1031): the currently active
// playback mode and master-fader level.
func (c *Client) PresetPlayback(ctx context.Context) (rdm.PresetPlayback, error) {
	data, err := c.getRaw(ctx, rdm.PIDPresetPlayback, nil)
	if err != nil {
		return rdm.PresetPlayback{}, err
	}
	return rdm.DecodePresetPlayback(data)
}

// SetPresetPlayback issues SET PRESET_PLAYBACK — recalling a stored preset
// (or PresetPlaybackOff to release control back to live DMX512). This
// changes the fixture's live output the moment it's accepted, so callers
// are expected to gate it behind an explicit confirmation.
func (c *Client) SetPresetPlayback(ctx context.Context, p rdm.PresetPlayback) error {
	return c.setRaw(ctx, rdm.PIDPresetPlayback, rdm.EncodePresetPlayback(p))
}

// SelfTestEnhanced issues GET SELFTEST_ENHANCED (0x1022): the full roster
// of self tests this responder declares, each with its current status and
// capability bits (E1.20 §10.11.8). This is the only PID in §10.11 that
// lets a controller enumerate which self test numbers exist at all without
// invoking each one — the spec's own text is explicit that there is no
// other way (see this package's isSpeculativePID gating and
// internal/web's device-control endpoint, which use this to resolve
// SELF_TEST_DESCRIPTION labels only for numbers this call actually
// reports).
func (c *Client) SelfTestEnhanced(ctx context.Context) (rdm.SelfTestEnhanced, error) {
	data, err := c.getRaw(ctx, rdm.PIDSelfTestEnhanced, nil)
	if err != nil {
		return rdm.SelfTestEnhanced{}, err
	}
	return rdm.DecodeSelfTestEnhanced(data)
}
