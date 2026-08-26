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
// This reads a device's *current* status list. It is not the same operation
// as DrainQueuedMessages below, and the difference matters:
//
//   - DeviceStatus asks "what is wrong with you now", and gets an answer
//     under the PID it asked for. It is the right call for a status panel.
//   - DrainQueuedMessages asks "hand me everything you are holding for me",
//     and the answers come back under whatever PIDs were queued. It is the
//     right call for emptying a proxy that is refusing traffic because its
//     buffer is full.
//
// This function used to carry a note explaining that QUEUED_MESSAGE could
// not be implemented at all, because RDMController required a response's PID
// to match its request's. That was a real invariant but the wrong conclusion
// drawn from it: a QUEUED_MESSAGE response carrying a different PID is
// correct E1.20 behaviour, not a violation, so the invariant was the thing
// that needed narrowing. It has since been relaxed for that one PID and
// nothing else — see session/rdmqueued.go — and the old TODO's worry
// (queued PIDs that are not STATUS_MESSAGES, such as a deferred
// SENSOR_VALUE) is now handled there rather than missed here.
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

// DrainQueuedMessages empties this responder's message queue with repeated
// GET QUEUED_MESSAGE (PID 0x0020), collecting whatever it hands back.
//
// Use it when a proxy is answering NACK PROXY_BUFFER_FULL — E1.20 defines
// that reason as "the proxy buffer is full and can not store any more Queued
// Message or Status Message responses", so emptying the queue is the
// documented remedy and, short of power-cycling the radio, the only one.
// The controller also runs this itself when a device's proxy circuit breaker
// opens; see session.QueuedMessageDrainPolicy.
//
// filter is a severity floor. rdm.StatusNone asks for everything the
// responder is holding, which is what a recovery drain wants; a higher
// StatusType asks only for messages at least that severe.
//
// maxDrain <= 0 uses the controller's configured cap. The loop always
// terminates: on the responder reporting its queue empty, on that cap, on
// any non-ACK answer, or on ctx.
func (c *Client) DrainQueuedMessages(ctx context.Context, filter rdm.StatusType, maxDrain int) (session.DrainResult, error) {
	return c.ctrl.DrainQueuedMessages(ctx, c.node, c.uid, filter, maxDrain)
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
