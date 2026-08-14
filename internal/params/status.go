package params

import (
	"context"
	"fmt"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// DefaultStatusDrainLimit bounds DeviceStatus's repeat-GET loop against a
// misbehaving responder that never lets its Message Count reach zero.
const DefaultStatusDrainLimit = 32

// DeviceStatus drains a device's status-message queue by repeatedly issuing
// GET STATUS_MESSAGES at filter severity, following the RDM header's
// Message Count field (report §2.4) until it reads zero, the responder
// returns no new messages, maxDrain iterations are hit, or ctx is
// cancelled. maxDrain<=0 defaults to DefaultStatusDrainLimit.
//
// Implementation note — why this doesn't use QUEUED_MESSAGE (0x0020): report
// §2.4 describes QUEUED_MESSAGE's response as "whatever queued PID gets
// sent back" (e.g. a spontaneous STATUS_MESSAGES payload under a different
// PID than the request). session.RDMController.HandleRDMResponse requires
// the response's ParameterID to equal the request's ParameterID (see
// rdmcontroller.go's ErrPIDMismatch check) — a real, load-bearing
// invariant for every other PID, but one that QUEUED_MESSAGE's spec'd
// behavior would violate on hardware that actually varies the response PID.
// Draining via repeated GET STATUS_MESSAGES instead satisfies the
// controller's invariant and is a spec-legal path (STATUS_MESSAGES' own
// GET is what QUEUED_MESSAGE-triggered pushes ultimately deliver anyway on
// the devices this session's research could confirm). TODO(hardware): if
// tomorrow's devices push non-STATUS_MESSAGES queued PIDs (a queued
// SENSOR_VALUE update, say), this drain loop will miss them; revisit
// whether RDMController's PID-match should be relaxed specifically for
// QUEUED_MESSAGE requests.
func (c *Client) DeviceStatus(ctx context.Context, filter rdm.StatusType, maxDrain int) ([]rdm.StatusMessage, error) {
	if maxDrain <= 0 {
		maxDrain = DefaultStatusDrainLimit
	}
	var all []rdm.StatusMessage
	for i := 0; i < maxDrain; i++ {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}
		cmd := c.ctrl.Get(c.node, c.uid, rdm.PIDStatusMessages, rdm.EncodeStatusMessagesRequest(filter))
		res, err := cmd.Await(ctx)
		if err != nil {
			return all, err
		}
		if res.Kind != session.ResultAck {
			if res.Err != nil {
				return all, res.Err
			}
			break
		}
		msgs, decErr := rdm.DecodeStatusMessages(res.Data)
		if decErr != nil {
			return all, fmt.Errorf("params: DeviceStatus: %w", decErr)
		}
		all = append(all, msgs...)
		if res.MessageCount == 0 || len(msgs) == 0 {
			break
		}
	}
	return all, nil
}

// StatusIDDescription issues GET STATUS_ID_DESCRIPTION for one message_id
// seen in a StatusMessage.
func (c *Client) StatusIDDescription(ctx context.Context, statusID uint16) (string, error) {
	data, err := c.getRaw(ctx, rdm.PIDStatusIDDescription, rdm.EncodeStatusIDDescriptionRequest(statusID))
	if err != nil {
		return "", err
	}
	return rdm.DecodeStatusIDDescriptionResponse(data), nil
}

// ProductDetails issues GET PRODUCT_DETAIL_ID_LIST.
func (c *Client) ProductDetails(ctx context.Context) ([]rdm.ProductDetail, error) {
	data, err := c.getRaw(ctx, rdm.PIDProductDetailIDList, nil)
	if err != nil {
		return nil, err
	}
	return rdm.DecodeProductDetailIDList(data)
}
