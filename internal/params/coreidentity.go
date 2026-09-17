// This file is the one read Benny512 performs on a device it has just
// learned about: the core-identity pass.
//
// It exists because of RDM-LOG30. A node's Table of Devices can arrive long
// after discovery has given up on that port — on 2.11.90.2 Port-Address 31,
// three empty ToDs came back at +211 ms, +228 ms and +223 ms after their
// flushes, discovery accepted "the port is empty" and moved on, and the real
// table then arrived at +11.554 s (11 UIDs) and +12.568 s (14 UIDs).
// 2.11.90.6 Port-Address 13 did the same: empty at +210 ms and +222 ms, then
// 4 UIDs at +6.979 s. Those 29 devices reached the registry with nothing but
// a UID, and nothing server-side ever read them.
//
// The read itself is deliberately NARROW. params.Client.Introspect is the
// heavyweight, on-demand tool behind POST /api/device/{uid}/introspect: it
// walks every editor-target PID in SUPPORTED_PARAMETERS and issues a live
// GET PARAMETER_DESCRIPTION for each manufacturer PID — tens of transactions
// per device — and it returns DESCRIPTORS, not values, so it would not fill
// in a single cell of the Devices list. A background pass that ran it for
// every newly discovered UID would put a full descriptor walk on a shared
// half-duplex RS-485 bus for devices nobody has opened. This pass asks
// exactly what the Devices screen shows.
package params

import (
	"context"
	"errors"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// coreIdentityPIDs is the ordered PID list one core-identity pass issues.
//
// The order and the membership are both taken from RDM-LOG30, not invented:
// this is exactly what a healthy device receives today, in the order it
// receives it (see 14:03:34.319 onwards — DEVICE_INFO, then
// SUPPORTED_PARAMETERS, then the rest). Across that capture the six PIDs
// below account for 173 / 111 / 66 / 36 / 163 / 161 outbound GETs
// respectively; nothing else in the list is part of building a device row.
//
// PRODUCT_DETAIL_ID_LIST and PROXIED_DEVICE_COUNT are speculative PIDs
// (isSpeculativePID) and are gated by getRaw against the device's own
// SUPPORTED_PARAMETERS. They are listed here anyway, and listed AFTER
// SUPPORTED_PARAMETERS on purpose: by the time they are reached the
// advertised set is already resolved and cached for this UID, so a device
// that does not advertise them is refused locally and nothing reaches the
// wire, while a device that does advertise them still gets asked. Dropping
// them would be a silent classification loss — they are the two signals
// registry.reclassify derives ClassWireless and the product-detail fallback
// from — and no NACK would ever reveal it, because the request would never
// be sent.
var coreIdentityPIDs = []rdm.ParameterID{
	rdm.PIDDeviceInfo,
	rdm.PIDSupportedParameters,
	rdm.PIDProductDetailIDList,
	rdm.PIDProxiedDeviceCount,
	rdm.PIDDeviceModelDescription,
	rdm.PIDManufacturerLabel,
}

// CoreIdentityPIDs returns the PIDs one core-identity pass issues, in the
// order it issues them. The slice is a copy; callers may not mutate the
// package's own list.
func CoreIdentityPIDs() []rdm.ParameterID {
	return append([]rdm.ParameterID(nil), coreIdentityPIDs...)
}

// CoreRead is the outcome of one PID within a core-identity pass.
//
// OnWire is the field that must not be collapsed into Err: a PID the probe
// gate refused locally (ErrPIDNotAdvertised) and a PID the device NACKed are
// both "no value", but only one of them cost a transaction, and only one of
// them is evidence about the device.
type CoreRead struct {
	PID    rdm.ParameterID
	Data   []byte
	Err    error
	OnWire bool
}

// CoreIdentityResult is ReadCoreIdentity's report on one pass.
//
// HaveDeviceInfo is the pass's success condition, and it is deliberately
// narrow: DEVICE_INFO ACKed. It is what the Devices screen's class, model ID,
// footprint and start address all come from, and it is the PID the four
// silent responders in RDM-LOG30 (4D50:001158FE, 4D50:0011597E,
// 4D50:001159BE, 4D50:001159FE) never answered across 9-12 asks each.
//
// Silent is true when DEVICE_INFO produced no answer at all — a timeout, an
// open proxy breaker, a cancelled context — as opposed to a NACK, which is
// an answer. A silent device's pass is abandoned after DEVICE_INFO rather
// than asking it five more questions it is not going to answer either; in
// RDM-LOG30 those four devices absorbed 48-63 requests apiece.
type CoreIdentityResult struct {
	UID            rdm.UID
	Reads          []CoreRead
	HaveDeviceInfo bool
	Silent         bool
	// Answered is true when ANY PID in the pass ACKed. A device can be
	// alive and still fail HaveDeviceInfo (it NACKed the one PID E1.20
	// requires of it), which is a fact about the device worth keeping
	// distinct from "said nothing".
	Answered bool
}

// ReadCoreIdentity issues the core-identity pass against this Client's
// responder, in coreIdentityPIDs order, strictly one PID at a time.
//
// It returns what happened rather than an error: a pass in which the device
// NACKed four of six PIDs is a completed pass, not a failure. Nothing here
// invents a value — a PID that did not answer has Err set and Data nil, and
// the registry is populated by the controller's own EventCommandComplete
// stream (registry.handleRDMEvent), so a read that did not happen leaves the
// registry's "not yet fetched" state exactly as it was.
//
// Serialization is the controller's: every GET goes through the same
// session.RDMController as every other command, so this contends for the
// line on the same terms as an operator-driven read. This function adds no
// concurrency of its own.
func (c *Client) ReadCoreIdentity(ctx context.Context) CoreIdentityResult {
	out := CoreIdentityResult{UID: c.uid, Reads: make([]CoreRead, 0, len(coreIdentityPIDs))}
	for _, pid := range coreIdentityPIDs {
		if err := ctx.Err(); err != nil {
			out.Reads = append(out.Reads, CoreRead{PID: pid, Err: err})
			if pid == rdm.PIDDeviceInfo {
				out.Silent = true
			}
			return out
		}
		data, err := c.getRaw(ctx, pid, nil)
		onWire := reachedTheWire(err)
		if pid == rdm.PIDSupportedParameters {
			// Hand the answer to the probe cache. Without this the gate on
			// PRODUCT_DETAIL_ID_LIST two lines below would go and ask the
			// same device the same question a second time.
			c.noteSupportedParametersRead(data, err, onWire && ctx.Err() == nil)
		}
		read := CoreRead{PID: pid, Data: data, Err: err, OnWire: onWire}
		out.Reads = append(out.Reads, read)
		switch {
		case err == nil:
			out.Answered = true
			if pid == rdm.PIDDeviceInfo {
				out.HaveDeviceInfo = true
			}
		case pid == rdm.PIDDeviceInfo && isSilence(err):
			// Nothing came back. Asking the same responder five more
			// questions in the same pass is how RDM-LOG30's silent
			// responders collected 48-63 requests each.
			out.Silent = true
			return out
		}
	}
	return out
}

// reachedTheWire reports whether a failed read actually cost a transaction.
// Only the local refusals — the probe gate and getRaw's unconditional
// no-GET-form guards — never leave the machine.
func reachedTheWire(err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, ErrPIDNotAdvertised),
		errors.Is(err, ErrDeviceNotAnswering),
		errors.Is(err, ErrResetDeviceHasNoGet),
		errors.Is(err, ErrCapturePresetHasNoGet),
		errors.Is(err, ErrSlotDescriptionNeedsIndex),
		errors.Is(err, ErrSelfTestDescriptionNeedsNumber):
		return false
	}
	return true
}

// isSilence reports whether err means the device said nothing, as opposed to
// saying "no". A NACK is an answer and a locally refused probe never asked;
// everything else — timeout, deadline, open proxy breaker, controller
// stopped, cancelled context — is silence.
func isSilence(err error) bool {
	if err == nil {
		return false
	}
	var nack *session.NackError
	if errors.As(err, &nack) {
		return false
	}
	if !reachedTheWire(err) {
		return false
	}
	return true
}
