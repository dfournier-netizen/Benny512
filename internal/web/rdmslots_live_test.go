package web

import (
	"net/http"
	"testing"

	"benny512/internal/params"
	"benny512/internal/patch"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// The literal endpoint must GET the responder's five-byte SLOT_INFO records,
// persist 1-based channels, and leave the GDTF profile untouched.
func TestReadPatchRDMSlotsFromResponder(t *testing.T) {
	h := newHarness(t)
	uid := rdm.UID{ManufacturerID: 0x6655, DeviceID: 17}
	node := h.seedNode(t)
	port := mustPort(t)
	h.srv.Registry.NoteFixture(session.NodeRef{Key: node.Key, Addr: node.Addr, Port: port}, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDSlotInfo}), false, 0, 0
		case rdm.PIDDeviceInfo:
			return params.EncodeDeviceInfo(params.DeviceInfo{DMXFootprint: 2, CurrentPersonality: 1, PersonalityCount: 1}), false, 0, 0
		case rdm.PIDSlotInfo:
			return []byte{0, 0, 0, 1, 1, 0, 1, 1, 0, 0}, false, 0, 0 // pan coarse, pan fine -> offset 0
		default:
			return nil, true, rdm.NackUnknownPID, 0
		}
	})
	rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/entries", entryRequest{
		Name: "Mover", FixtureType: "Test Mover", Mode: "2ch", Footprint: 2,
		Universe: port.RawValue(), StartAddress: 1,
	})
	var created patchResponse
	mustUnmarshal(t, rr, &created)
	id := created.Patch.Entries[0].ID
	if _, err := h.srv.PatchStore.Mutate(func(p *patch.Patch) error {
		p.Entries[0].ConfirmedUID = uid.String()
		p.Entries[0].MatchState = patch.MatchStateConfirmed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr = h.runHTTPAsync(t, "POST", "/api/patch/entries/"+id+"/rdm-slots", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("read slots: %d %s", rr.Code, rr.Body.String())
	}
	var out patchResponse
	mustUnmarshal(t, rr, &out)
	got := out.Patch.Entries[0].ChannelFunctions
	if len(got) != 2 || got[1].Attribute != "Pan" || got[2].Attribute != "Pan" || got[1].Source != patch.SourceRDMInferred {
		t.Fatalf("responder's pan coarse/fine must drive channels 1 and 2: %+v", got)
	}
	if _, err := h.srv.PatchStore.Mutate(func(p *patch.Patch) error {
		p.Entries[0].ChannelFunctions[1] = patch.ChannelFunction{Source: patch.SourceGDTF, Attribute: "Dimmer", ChannelSets: make([]patch.ChannelSet, 0)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr = h.runHTTPAsync(t, "POST", "/api/patch/entries/"+id+"/rdm-slots", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("repeat read: %d %s", rr.Code, rr.Body.String())
	}
	mustUnmarshal(t, rr, &out)
	got = out.Patch.Entries[0].ChannelFunctions
	if got[1].Source != patch.SourceGDTF || got[1].Attribute != "Dimmer" || got[2].Source != patch.SourceRDMInferred {
		t.Fatalf("RDM read replaced GDTF data: %+v", got)
	}
}
