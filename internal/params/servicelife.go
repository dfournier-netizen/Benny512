package params

import (
	"context"
	"fmt"

	"benny512/internal/rdm"
)

// This file adds typed params.Client methods for Phase D task 2's PID set:
// the E1.20 §10.8 "service life" family (DEVICE_HOURS, LAMP_HOURS,
// LAMP_STRIKES, LAMP_STATE, DEVICE_POWER_CYCLES), FACTORY_DEFAULTS
// (§10.5.6) and RESET_DEVICE (§10.11.2). Every wire layout used here is
// transcribed directly from the ANSI E1.20-2025 PDF text (see
// internal/rdm/lampstate.go's file doc comment) — CONFIRMED, not a
// best-reading guess the way internal/rdm/dimmer.go's E1.37-1 codecs are.
//
// All six PIDs go through getRaw/setRaw exactly like every other typed
// method in this package, so they automatically pick up params.go's
// RESET_DEVICE-has-no-GET guard and Phase D task 3's speculative-PID gate
// (introspect.go's isSpeculativePID includes every PID here except
// RESET_DEVICE itself, which is gated differently — see ResetDevice's doc
// comment).

// DeviceHours issues GET DEVICE_HOURS (0x0400): total hours of operation.
func (c *Client) DeviceHours(ctx context.Context) (uint32, error) {
	data, err := c.getRaw(ctx, rdm.PIDDeviceHours, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeUint32Counter(data, "DEVICE_HOURS")
}

// SetDeviceHours issues SET DEVICE_HOURS. Per E1.20 §10.8.1, "some devices
// may only support the GET_COMMAND for this operation" — a device that
// doesn't allow setting its hour counter NACKs UNSUPPORTED_COMMAND_CLASS,
// surfaced as an ordinary *session.NackError like any other rejected SET.
func (c *Client) SetDeviceHours(ctx context.Context, hours uint32) error {
	return c.setRaw(ctx, rdm.PIDDeviceHours, rdm.EncodeUint32Counter(hours))
}

// LampHours issues GET LAMP_HOURS (0x0401).
func (c *Client) LampHours(ctx context.Context) (uint32, error) {
	data, err := c.getRaw(ctx, rdm.PIDLampHours, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeUint32Counter(data, "LAMP_HOURS")
}

// SetLampHours issues SET LAMP_HOURS (e.g. to zero the counter after a
// lamp/LED-engine replacement).
func (c *Client) SetLampHours(ctx context.Context, hours uint32) error {
	return c.setRaw(ctx, rdm.PIDLampHours, rdm.EncodeUint32Counter(hours))
}

// LampStrikes issues GET LAMP_STRIKES (0x0402).
func (c *Client) LampStrikes(ctx context.Context) (uint32, error) {
	data, err := c.getRaw(ctx, rdm.PIDLampStrikes, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeUint32Counter(data, "LAMP_STRIKES")
}

// SetLampStrikes issues SET LAMP_STRIKES.
func (c *Client) SetLampStrikes(ctx context.Context, strikes uint32) error {
	return c.setRaw(ctx, rdm.PIDLampStrikes, rdm.EncodeUint32Counter(strikes))
}

// LampState issues GET LAMP_STATE (0x0403).
func (c *Client) LampState(ctx context.Context) (rdm.LampState, error) {
	data, err := c.getRaw(ctx, rdm.PIDLampState, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeLampState(data)
}

// SetLampState issues SET LAMP_STATE.
func (c *Client) SetLampState(ctx context.Context, s rdm.LampState) error {
	return c.setRaw(ctx, rdm.PIDLampState, rdm.EncodeLampState(s))
}

// DevicePowerCycles issues GET DEVICE_POWER_CYCLES (0x0405).
func (c *Client) DevicePowerCycles(ctx context.Context) (uint32, error) {
	data, err := c.getRaw(ctx, rdm.PIDDevicePowerCycles, nil)
	if err != nil {
		return 0, err
	}
	return rdm.DecodeUint32Counter(data, "DEVICE_POWER_CYCLES")
}

// SetDevicePowerCycles issues SET DEVICE_POWER_CYCLES.
func (c *Client) SetDevicePowerCycles(ctx context.Context, cycles uint32) error {
	return c.setRaw(ctx, rdm.PIDDevicePowerCycles, rdm.EncodeUint32Counter(cycles))
}

// FactoryDefaults issues GET FACTORY_DEFAULTS (0x0090): whether the device
// is CURRENTLY set to its factory defaults (E1.20 §10.5.6). This is a
// read-only status query; use ResetToFactoryDefaults to trigger the revert
// itself.
func (c *Client) FactoryDefaults(ctx context.Context) (bool, error) {
	data, err := c.getRaw(ctx, rdm.PIDFactoryDefaults, nil)
	if err != nil {
		return false, err
	}
	return rdm.DecodeFactoryDefaults(data)
}

// ResetToFactoryDefaults issues SET FACTORY_DEFAULTS (PDL=0, no Parameter
// Data — E1.20 §10.5.6) to instruct the device to revert every user
// setting/configuration to its manufacturer defaults. This is destructive
// and, per the spec text, manufacturer-defined in scope ("Factory Default
// user settings or configuration as determined by the Manufacturer") — the
// HTTP layer (internal/web/device.go's handleSetFactoryDefaults) requires
// an explicit {"confirm":"RESET"} body before calling this, mirroring
// internal/web/reset.go's confirmation discipline for the app's own
// destructive full-reset endpoint.
func (c *Client) ResetToFactoryDefaults(ctx context.Context) error {
	return c.setRaw(ctx, rdm.PIDFactoryDefaults, nil)
}

// ErrInvalidResetMode is returned by ResetDevice for any mode other than
// rdm.ResetWarm/rdm.ResetCold — E1.20 §10.11.2 defines exactly those two
// Parameter Data values and no others.
var ErrInvalidResetMode = fmt.Errorf("params: RESET_DEVICE mode must be Warm (0x01) or Cold (0xFF)")

// ResetDevice issues SET RESET_DEVICE (0x1001) with the given mode
// (E1.20 §10.11.2). This PID is SET_COMMAND only — there is no GET form at
// all (see params.go's ErrResetDeviceHasNoGet, enforced in getRaw) — and a
// successful SET_COMMAND_RESPONSE ACK (PDL=0) is the only response shape
// the spec defines; the device is expected to actually reset afterward, not
// to keep answering on this connection.
//
// E1.20's own text is explicit that this also clears the Discovery Mute
// flag ("This parameter shall also clear the Discovery Mute flag. A cold
// reset is the equivalent of removing and reapplying power to the
// device."), meaning the device WILL drop off the bus and need
// re-discovery — Benny512's own cached state about it (introspection
// descriptors, the process-wide PARAMETER_DESCRIPTION cache entries keyed
// on its manufacturer ID) is invalidated by this call via ForgetDevice, but
// its registry.Fixture row (ToD membership, last-known params) is
// deliberately left alone — see handleResetDevice's doc comment in
// internal/web/device.go for why that split is the right call and what's
// still debatable about it.
//
// There is no way to discover, from any RDM PID, whether a given device
// actually distinguishes a warm reset from a cold one in practice —
// SUPPORTED_PARAMETERS only says whether 0x1001 is listed at all, and
// PARAMETER_DESCRIPTION is spec-legal only for manufacturer-specific PIDs
// (E1.20 §10.4.2), so it can never describe a standard PID like this one.
// Callers (the HTTP layer) must offer both modes whenever 0x1001 is
// advertised and never claim to know more than that — see
// Client.IsAdvertised.
func (c *Client) ResetDevice(ctx context.Context, mode rdm.ResetMode) error {
	if !mode.IsValid() {
		return fmt.Errorf("%w: got 0x%02X", ErrInvalidResetMode, byte(mode))
	}
	if err := c.setRaw(ctx, rdm.PIDResetDevice, rdm.EncodeResetDevice(mode)); err != nil {
		return err
	}
	// The device just told us (by accepting this command) that it is about
	// to reset and lose its Discovery Mute state — whatever this package
	// has cached about its introspected PID shape is now presumptively
	// stale (firmware could even change on a cold reset), so drop it. See
	// this method's doc comment for what's deliberately NOT touched here.
	ForgetDevice(c.uid)
	return nil
}
