package capture

import (
	"net/netip"
	"strings"
	"testing"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
)

var testPeer = netip.MustParseAddrPort("10.0.0.2:6454")

func encodeRDMPacket(t *testing.T, msg rdm.Message) []byte {
	t.Helper()
	pkt := artnet.EncodeRdmPacket(msg, artnet.DefaultProtocolVersion, 0, 0)
	return artnet.Encode(artnet.Packet{Kind: artnet.KindRdm, Rdm: pkt})
}

func TestDecodeEntry_DeviceInfoResponse(t *testing.T) {
	di := params.DeviceInfo{
		ProtocolVersionMajor: 1, ProtocolVersionMinor: 0, DeviceModelID: 1,
		ProductCategory: 0x0102, SoftwareVersionID: 0x01000000, DMXFootprint: 20,
		CurrentPersonality: 1, PersonalityCount: 2, DMXStartAddress: 5,
		SubDeviceCount: 0, SensorCount: 0,
	}
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 14, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDDeviceInfo,
		ParameterData: params.EncodeDeviceInfo(di),
	}
	raw := encodeRDMPacket(t, msg)

	e := DecodeEntry(DirIn, testPeer, raw)
	if e.Kind != "ArtRdm" {
		t.Fatalf("Kind = %q, want ArtRdm", e.Kind)
	}
	if e.RDM == nil {
		t.Fatal("RDM detail is nil")
	}
	d := e.RDM
	if d.DecodeError != "" {
		t.Fatalf("unexpected decode error: %s", d.DecodeError)
	}
	if !d.ChecksumValid {
		t.Error("ChecksumValid = false, want true for a well-formed message")
	}
	if !d.IsResponse {
		t.Error("IsResponse = false, want true")
	}
	if d.PIDName != "DEVICE_INFO" {
		t.Errorf("PIDName = %q, want DEVICE_INFO", d.PIDName)
	}
	if d.CommandClass != "GET_COMMAND_RESPONSE" {
		t.Errorf("CommandClass = %q, want GET_COMMAND_RESPONSE", d.CommandClass)
	}
	if d.ResponseType != "ACK" {
		t.Errorf("ResponseType = %q, want ACK", d.ResponseType)
	}
	if d.ParamDataHex == "" {
		t.Error("ParamDataHex is empty, want raw hex always present")
	}
	if !strings.Contains(d.Decoded, "footprint=20") || !strings.Contains(d.Decoded, "startAddr=5") {
		t.Errorf("Decoded = %q, missing expected DEVICE_INFO fields", d.Decoded)
	}
	if d.SourceUID != "2222:00000001" {
		t.Errorf("SourceUID = %q, want 2222:00000001", d.SourceUID)
	}
}

func TestDecodeEntry_NackReason(t *testing.T) {
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 1, PortIDOrResponseType: byte(rdm.ResponseNackReason),
		CommandClass: rdm.SetCommandResponse, ParameterID: rdm.PIDDeviceLabel,
		ParameterData: []byte{0x00, 0x06}, // NackDataOutOfRange
	}
	raw := encodeRDMPacket(t, msg)
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.RDM == nil {
		t.Fatal("RDM detail is nil")
	}
	if e.RDM.ResponseType != "NACK_REASON" {
		t.Fatalf("ResponseType = %q, want NACK_REASON", e.RDM.ResponseType)
	}
	if e.RDM.NackReasonCode != 0x0006 {
		t.Errorf("NackReasonCode = 0x%04X, want 0x0006", e.RDM.NackReasonCode)
	}
	if e.RDM.NackReasonName != "DATA_OUT_OF_RANGE" {
		t.Errorf("NackReasonName = %q, want DATA_OUT_OF_RANGE", e.RDM.NackReasonName)
	}
}

func TestDecodeEntry_AckTimer(t *testing.T) {
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x4C55, DeviceID: 1},
		TransactionNumber: 2, PortIDOrResponseType: byte(rdm.ResponseACKTimer),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDDeviceInfo,
		ParameterData: []byte{0x00, 0x14}, // 20 units
	}
	raw := encodeRDMPacket(t, msg)
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.RDM == nil {
		t.Fatal("RDM detail is nil")
	}
	if e.RDM.AckTimerRawUnits != 20 {
		t.Fatalf("AckTimerRawUnits = %d, want 20", e.RDM.AckTimerRawUnits)
	}
	if e.RDM.AckTimerMsPerE120 != 200 {
		t.Errorf("AckTimerMsPerE120 = %d, want 200 (20 units * 10ms)", e.RDM.AckTimerMsPerE120)
	}
	if e.RDM.AckTimerMsIfRawIsMs != 20 {
		t.Errorf("AckTimerMsIfRawIsMs = %d, want 20", e.RDM.AckTimerMsIfRawIsMs)
	}
}

func TestDecodeEntry_ChecksumMismatch(t *testing.T) {
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 3, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDIdentifyDevice,
		ParameterData: []byte{0x01},
	}
	raw := encodeRDMPacket(t, msg)
	// Corrupt the trailing checksum bytes.
	raw[len(raw)-1] ^= 0xFF

	e := DecodeEntry(DirIn, testPeer, raw)
	if e.RDM == nil {
		t.Fatal("RDM detail is nil")
	}
	if e.RDM.ChecksumValid {
		t.Error("ChecksumValid = true, want false for a corrupted checksum")
	}
	if e.RDM.DecodeError == "" {
		t.Error("DecodeError is empty, want a checksum-mismatch message")
	}
	// The raw datagram is still fully captured even though structured
	// decode failed.
	if e.Hex == "" {
		t.Error("Hex is empty; raw bytes should always be retained even on decode failure")
	}
}

func TestDecodeEntry_TodData(t *testing.T) {
	tod := artnet.TodData{
		ProtocolVersion: artnet.DefaultProtocolVersion, RdmVersion: 1, Port: 1,
		Net: 0, CommandResponse: 0x00, Address: 0, UidTotal: 2, BlockCount: 0,
		Tod: []rdm.UID{{ManufacturerID: 0x2222, DeviceID: 1}, {ManufacturerID: 0x2222, DeviceID: 2}},
	}
	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindTodData, TodData: tod})
	e := DecodeEntry(DirOut, testPeer, raw)
	if e.Kind != "ArtTodData" {
		t.Fatalf("Kind = %q, want ArtTodData", e.Kind)
	}
	if e.Tod == nil {
		t.Fatal("Tod detail is nil")
	}
	if e.Tod.SubKind != "Data" {
		t.Errorf("SubKind = %q, want Data", e.Tod.SubKind)
	}
	if len(e.Tod.UIDs) != 2 {
		t.Fatalf("got %d UIDs, want 2: %v", len(e.Tod.UIDs), e.Tod.UIDs)
	}
	if e.Tod.UidTotal != 2 {
		t.Errorf("UidTotal = %d, want 2", e.Tod.UidTotal)
	}
}

func TestIsRDMKind(t *testing.T) {
	rdmKinds := []string{"ArtRdm", "ArtTodRequest", "ArtTodData", "ArtTodControl"}
	for _, k := range rdmKinds {
		if !IsRDMKind(k) {
			t.Errorf("IsRDMKind(%q) = false, want true", k)
		}
	}
	nonRDM := []string{"ArtDmx", "ArtPoll", "ArtPollReply", "unknown", ""}
	for _, k := range nonRDM {
		if IsRDMKind(k) {
			t.Errorf("IsRDMKind(%q) = true, want false", k)
		}
	}
}

// TestRDMRingSurvivesGeneralRingEviction demonstrates the production
// pattern (cmd/benny512's capture tap): every datagram is decoded once via
// DecodeEntry, added to the general ring, and additionally added to a
// dedicated RDM-only ring when IsRDMKind. A high-rate flood of non-RDM
// traffic (ArtDmx at 40Hz in production) evicts the RDM entry from a small
// shared ring almost immediately, but the dedicated ring — fed only RDM
// traffic — is unaffected. This is the buffer-eviction-policy guarantee
// report task item 3 calls for.
func TestRDMRingSurvivesGeneralRingEviction(t *testing.T) {
	general := New(5) // tiny, easily flooded
	rdmRing := New(100)

	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 1, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDIdentifyDevice,
		ParameterData: []byte{0x01},
	}
	raw := encodeRDMPacket(t, msg)
	e := DecodeEntry(DirIn, testPeer, raw)
	general.Add(e)
	if IsRDMKind(e.Kind) {
		rdmRing.Add(e)
	}

	// Flood the shared general ring past capacity with ArtDmx-shaped
	// entries, exactly as real ArtDmx traffic would.
	for i := 0; i < 50; i++ {
		general.Add(Entry{Kind: "ArtDmx", Size: 530})
	}

	for _, ge := range general.Snapshot(Filter{}, 0) {
		if ge.Kind == "ArtRdm" {
			t.Fatal("RDM entry unexpectedly survived in the flooded general ring — test premise broken")
		}
	}
	rdmEntries := rdmRing.Snapshot(Filter{}, 0)
	if len(rdmEntries) != 1 || rdmEntries[0].Kind != "ArtRdm" {
		t.Fatalf("RDM entry missing from the dedicated ring after the general ring was flooded: %+v", rdmEntries)
	}
}

func TestFilter_UIDPIDCommandClass(t *testing.T) {
	r := New(10)
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 1, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDDeviceInfo,
		ParameterData: params.EncodeDeviceInfo(params.DeviceInfo{}),
	}
	raw := encodeRDMPacket(t, msg)
	r.AddPacket(DirIn, testPeer, raw)
	r.Add(Entry{Kind: "ArtDmx", Size: 20})

	byUID := r.Snapshot(Filter{UID: "2222:00000001"}, 0)
	if len(byUID) != 1 {
		t.Fatalf("UID filter matched %d entries, want 1", len(byUID))
	}
	byWrongUID := r.Snapshot(Filter{UID: "9999:00000009"}, 0)
	if len(byWrongUID) != 0 {
		t.Fatalf("UID filter matched %d entries for an unrelated UID, want 0", len(byWrongUID))
	}
	byPID := r.Snapshot(Filter{HasPID: true, PID: uint16(rdm.PIDDeviceInfo)}, 0)
	if len(byPID) != 1 {
		t.Fatalf("PID filter matched %d entries, want 1", len(byPID))
	}
	byCC := r.Snapshot(Filter{CommandClass: "GET_COMMAND_RESPONSE"}, 0)
	if len(byCC) != 1 {
		t.Fatalf("CommandClass filter matched %d entries, want 1", len(byCC))
	}
	byDir := r.Snapshot(Filter{HasDir: true, Dir: DirOut}, 0)
	if len(byDir) != 0 {
		t.Fatalf("Dir filter matched %d entries for DirOut, want 0 (both entries were DirIn/zero-value)", len(byDir))
	}
}

func TestFormatEntryText_IncludesDecodedAndHex(t *testing.T) {
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 7, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDDeviceLabel,
		ParameterData: []byte("Wash 1"),
	}
	raw := encodeRDMPacket(t, msg)
	e := DecodeEntry(DirIn, testPeer, raw)
	text := FormatEntryText(e)
	for _, want := range []string{"ArtRdm", "GET_COMMAND_RESPONSE", "DEVICE_LABEL", "Wash 1", "hex:"} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatEntryText output missing %q:\n%s", want, text)
		}
	}
}
