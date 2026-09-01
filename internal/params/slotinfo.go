// This file adds typed params.Client wrappers over internal/rdm/slotinfo.go's
// SLOT_INFO/SLOT_DESCRIPTION codecs — the live-RDM half of Task 3's function-
// aware Rig Check foundation (decode is package rdm's job, per this
// package's own layering: params wraps rdm+session, it doesn't reimplement
// wire parsing). Fetching SLOT_INFO/SLOT_DESCRIPTION from a real device and
// turning the result into patch.ChannelFunction (Source==SourceRDMInferred)
// is internal/web/patchattrs.go's job, same "web layer combines packages
// that don't import each other" shape internal/params/classification.go's
// doc comment already documents for Tier+PIDName.
package params

import (
	"context"

	"benny512/internal/rdm"
)

// SlotInfo issues GET SLOT_INFO (0x0120) and decodes the response.
func (c *Client) SlotInfo(ctx context.Context) ([]rdm.SlotInfoEntry, error) {
	data, err := c.getRaw(ctx, rdm.PIDSlotInfo, nil)
	if err != nil {
		return nil, err
	}
	return rdm.DecodeSlotInfo(data)
}

// SlotDescription issues GET SLOT_DESCRIPTION (0x0121) for one slot offset
// and decodes the response. getRaw's ErrSlotDescriptionNeedsIndex guard
// (params.go) already refuses anything but a 2-byte request payload; this
// method is the one place in this package that builds that payload, via
// rdm.EncodeSlotDescriptionRequest, so no caller can accidentally send the
// bare/no-payload GET that guard exists to catch.
func (c *Client) SlotDescription(ctx context.Context, slotOffset uint16) (rdm.SlotDescription, error) {
	data, err := c.getRaw(ctx, rdm.PIDSlotDescription, rdm.EncodeSlotDescriptionRequest(slotOffset))
	if err != nil {
		return rdm.SlotDescription{}, err
	}
	return rdm.DecodeSlotDescription(data)
}
