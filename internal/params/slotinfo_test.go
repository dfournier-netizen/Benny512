package params

import (
	"context"
	"testing"

	"benny512/internal/rdm"
)

// TestClientSlotInfoAndSlotDescription exercises both new typed methods end
// to end through a scripted responder, mirroring TestClientServiceLifePIDs'
// pattern. Neither PID is in isSpeculativePID's gated set (introspect.go),
// so no SUPPORTED_PARAMETERS advertisement is needed for the calls to
// reach the responder.
func TestClientSlotInfoAndSlotDescription(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 2}
	slotInfo := rdm.EncodeSlotInfo([]rdm.SlotInfoEntry{
		{Offset: 0, Type: rdm.SlotTypePrimary, Value: uint16(rdm.SDPan)},
		{Offset: 1, Type: rdm.SlotTypeSecondaryFine, Value: 0},
	})
	slotDesc := rdm.EncodeSlotDescription(rdm.SlotDescription{SlotOffset: 3, Label: "Gobo Wheel"})

	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		switch msg.ParameterID {
		case rdm.PIDSlotInfo:
			return slotInfo, false, 0
		case rdm.PIDSlotDescription:
			if len(msg.ParameterData) != 2 {
				return nil, true, rdm.NackFormatError
			}
			return slotDesc, false, 0
		default:
			return nil, true, rdm.NackUnknownPID
		}
	})

	type result struct {
		slots []rdm.SlotInfoEntry
		desc  rdm.SlotDescription
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		var r result
		if r.slots, r.err = client.SlotInfo(context.Background()); r.err != nil {
			resCh <- r
			return
		}
		r.desc, r.err = client.SlotDescription(context.Background(), 3)
		resCh <- r
	}()

	var got result
	runAsyncRecv(t, clock, resCh, &got)
	if got.err != nil {
		t.Fatalf("client call failed: %v", got.err)
	}
	if len(got.slots) != 2 || got.slots[0].Type != rdm.SlotTypePrimary {
		t.Errorf("SlotInfo = %+v", got.slots)
	}
	if got.desc.Label != "Gobo Wheel" || got.desc.SlotOffset != 3 {
		t.Errorf("SlotDescription = %+v", got.desc)
	}
}

// TestClientSlotDescription_RequiresIndex confirms the live-Client path
// actually goes through getRaw's ErrSlotDescriptionNeedsIndex guard, not
// just a bare wire-format check — SlotDescription always sends exactly 2
// bytes, so this proves the guard is satisfied end-to-end rather than
// asserting the guard exists in isolation (params_test.go's own
// TestGetRaw* tests already cover the guard directly; this test's job is
// to confirm this file's method actually feeds it correctly).
func TestClientSlotDescription_RequiresIndex(t *testing.T) {
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 3}
	var gotLen int
	client, clock := newTestClient(t, uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason) {
		if msg.ParameterID == rdm.PIDSlotDescription {
			gotLen = len(msg.ParameterData)
			return rdm.EncodeSlotDescription(rdm.SlotDescription{SlotOffset: 7, Label: "X"}), false, 0
		}
		return nil, true, rdm.NackUnknownPID
	})
	resCh := make(chan error, 1)
	go func() {
		_, err := client.SlotDescription(context.Background(), 7)
		resCh <- err
	}()
	var err error
	runAsyncRecv(t, clock, resCh, &err)
	if err != nil {
		t.Fatalf("SlotDescription: %v", err)
	}
	if gotLen != 2 {
		t.Errorf("request PDL = %d, want 2", gotLen)
	}
}
