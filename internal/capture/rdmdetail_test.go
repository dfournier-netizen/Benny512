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
	pkt := artnet.EncodeRdmPacket(msg, artnet.DefaultProtocolVersion, 0, 0, false)
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

// TestDecodeEntry_UndecodableCarriesDecodeErr covers the blind spot this
// change closes: a datagram that fails to decode used to become an Entry
// with Kind "unknown" and the error discarded entirely. It must now retain
// the error, keep Kind "unknown" (IsRDMKind's contract for that string is
// unchanged), and keep its full hex regardless of size.
func TestDecodeEntry_UndecodableCarriesDecodeErr(t *testing.T) {
	garbage := []byte("this is not an art-net packet, at all")
	e := DecodeEntry(DirIn, testPeer, garbage)
	if e.Kind != "unknown" {
		t.Errorf("Kind = %q, want unknown", e.Kind)
	}
	if e.DecodeErr == "" {
		t.Error("expected DecodeErr to be populated")
	}
	if !strings.Contains(e.DecodeErr, "invalid packet ID") {
		t.Errorf("DecodeErr = %q, want it to mention the invalid ID", e.DecodeErr)
	}
	if e.Hex == "" {
		t.Error("expected Hex to be populated even though decode failed")
	}
}

// TestDecodeEntry_SuccessfulDecodeLeavesDecodeErrEmpty is the "normal ArtRdm
// entry is unaffected" half of the contract: a clean decode must never
// populate DecodeErr.
func TestDecodeEntry_SuccessfulDecodeLeavesDecodeErrEmpty(t *testing.T) {
	msg := rdm.Message{
		DestinationUID:    rdm.UID{ManufacturerID: 0x7FF0, DeviceID: 1},
		SourceUID:         rdm.UID{ManufacturerID: 0x2222, DeviceID: 1},
		TransactionNumber: 1, PortIDOrResponseType: byte(rdm.ResponseACK),
		CommandClass: rdm.GetCommandResponse, ParameterID: rdm.PIDIdentifyDevice,
		ParameterData: []byte{0x01},
	}
	raw := encodeRDMPacket(t, msg)
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.Kind != "ArtRdm" {
		t.Fatalf("Kind = %q, want ArtRdm", e.Kind)
	}
	if e.DecodeErr != "" {
		t.Errorf("DecodeErr = %q, want empty for a successful decode", e.DecodeErr)
	}
}

// TestIsRDMLoggable is the "routing decision" contract: the four RDM-family
// opcodes route in (matching IsRDMKind, which this deliberately doesn't
// replace), an undecodable entry routes in via DecodeErr regardless of its
// (always "unknown") Kind, and an ordinary decoded non-RDM entry does not.
func TestIsRDMLoggable(t *testing.T) {
	for _, k := range []string{"ArtRdm", "ArtTodRequest", "ArtTodData", "ArtTodControl"} {
		if !IsRDMLoggable(Entry{Kind: k}) {
			t.Errorf("IsRDMLoggable(Kind=%q) = false, want true", k)
		}
	}
	if !IsRDMLoggable(Entry{Kind: "unknown", DecodeErr: "artnet: invalid packet ID"}) {
		t.Error("IsRDMLoggable should be true for an undecodable entry")
	}
	for _, k := range []string{"ArtDmx", "ArtPoll", "ArtPollReply", "unknown", ""} {
		if IsRDMLoggable(Entry{Kind: k}) {
			t.Errorf("IsRDMLoggable(Kind=%q, no DecodeErr) = true, want false", k)
		}
	}
}

// TestUndecodableEntryReachesRDMRingAndLogsWithHex exercises the same
// production shape as TestRDMRingSurvivesGeneralRingEviction, but for an
// undecodable datagram instead of a valid ArtRdm one: DecodeEntry -> general
// ring -> (IsRDMLoggable) -> RDM-only ring, and FormatEntryText (what the
// disk logger writes) must show direction, peer, size, the decode error,
// and the hex.
func TestUndecodableEntryReachesRDMRingAndLogsWithHex(t *testing.T) {
	general := New(5)
	rdmRing := New(100)

	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	e := DecodeEntry(DirOut, testPeer, garbage)
	e = general.Add(e)
	if !IsRDMLoggable(e) {
		t.Fatal("undecodable entry should be IsRDMLoggable")
	}
	e = rdmRing.Add(e)

	got := rdmRing.Snapshot(Filter{}, 0)
	if len(got) != 1 {
		t.Fatalf("RDM ring has %d entries, want 1", len(got))
	}

	text := FormatEntryText(e)
	for _, want := range []string{"OUT", testPeer.String(), "size=11", "DECODE ERROR", "hex:", "deadbeef"} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatEntryText output missing %q:\n%s", want, text)
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

// TestDecodeEntry_UnrecognizedOpcodeIsNotAFailure covers the other
// "unknown"-shaped state (see KindUnrecognized's doc comment): a valid
// envelope carrying an opcode this package doesn't decode is a successful
// decode, not a decode error — DecodeErr must stay empty, Kind must be
// KindUnrecognized (not KindUndecoded), and the raw opcode must survive
// onto UnknownOpCode/Key for the throttle and the log to use.
func TestDecodeEntry_UnrecognizedOpcodeIsNotAFailure(t *testing.T) {
	raw := artnet.Encode(artnet.Packet{
		Kind:           artnet.KindUnknown,
		UnknownOpCode:  0x5200, // ArtSync — real, just not modeled by this codebase
		UnknownPayload: []byte{0x00},
	})
	e := DecodeEntry(DirIn, testPeer, raw)
	if e.Kind != KindUnrecognized {
		t.Fatalf("Kind = %q, want %q", e.Kind, KindUnrecognized)
	}
	if e.DecodeErr != "" {
		t.Errorf("DecodeErr = %q, want empty (this is a successful decode)", e.DecodeErr)
	}
	if e.UnknownOpCode != 0x5200 {
		t.Errorf("UnknownOpCode = 0x%04X, want 0x5200", e.UnknownOpCode)
	}
	if !strings.Contains(e.Key, "0x5200") {
		t.Errorf("Key = %q, want it to mention the opcode", e.Key)
	}
}

// TestUnknownOpcodeThrottle_CapsAndSuppressesOncePerPair is the core
// contract: the first `cap` sightings of a (peer, opcode) pair log the real
// entry, the sighting that crosses the cap logs exactly one suppression
// entry naming the pair, and every sighting after that logs nothing (while
// still being counted).
func TestUnknownOpcodeThrottle_CapsAndSuppressesOncePerPair(t *testing.T) {
	const cap = 3
	th := NewUnknownOpcodeThrottle(cap)
	peer := testPeer
	mk := func() Entry {
		return Entry{Dir: DirIn, Peer: peer, Kind: KindUnrecognized, UnknownOpCode: 0x5200, Key: "opcode=0x5200"}
	}

	var suppressionsSeen int
	var loggedCount int
	for i := 0; i < cap+5; i++ {
		logEntry, ok := th.Consider(mk())
		switch {
		case i < cap:
			if !ok {
				t.Fatalf("sighting %d: Consider = false, want true (still under cap)", i+1)
			}
			if logEntry.Kind != KindUnrecognized {
				t.Fatalf("sighting %d: logEntry.Kind = %q, want the real Kind while under cap", i+1, logEntry.Kind)
			}
			loggedCount++
		case i == cap:
			if !ok {
				t.Fatalf("sighting %d (cap-crossing): Consider = false, want true (one suppression entry)", i+1)
			}
			if logEntry.Kind == KindUnrecognized {
				t.Fatalf("sighting %d: expected a suppression entry, got the real Kind", i+1)
			}
			text := FormatEntryText(logEntry)
			for _, want := range []string{peer.String(), "0x5200", "suppress"} {
				if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
					t.Errorf("suppression entry text missing %q:\n%s", want, text)
				}
			}
			suppressionsSeen++
			loggedCount++
		default:
			if ok {
				t.Fatalf("sighting %d: Consider = true, want false (already suppressed)", i+1)
			}
		}
	}
	if suppressionsSeen != 1 {
		t.Fatalf("suppression entries emitted = %d, want exactly 1", suppressionsSeen)
	}
	if loggedCount != cap+1 {
		t.Fatalf("total logged (real + suppression) = %d, want %d", loggedCount, cap+1)
	}
	if got := th.Count(peer, 0x5200); got != cap+5 {
		t.Errorf("Count = %d, want %d (every sighting counted, logged or not)", got, cap+5)
	}
}

// TestUnknownOpcodeThrottle_OutboundNeverEligible covers the "outbound is
// our own bug, not a device behavior" call: an outbound entry, however
// shaped, is never routed by Consider and never consumes cap budget.
func TestUnknownOpcodeThrottle_OutboundNeverEligible(t *testing.T) {
	th := NewUnknownOpcodeThrottle(2)
	e := Entry{Dir: DirOut, Peer: testPeer, Kind: KindUnrecognized, UnknownOpCode: 0x5200}
	for i := 0; i < 5; i++ {
		if _, ok := th.Consider(e); ok {
			t.Fatalf("sighting %d: outbound entry should never be eligible", i+1)
		}
	}
	if got := th.Count(testPeer, 0x5200); got != 0 {
		t.Errorf("Count = %d, want 0 (outbound never touches throttle state)", got)
	}
}

// TestUnknownOpcodeThrottle_IgnoresOtherKinds confirms Consider only acts on
// KindUnrecognized — a normal decoded entry or an undecodable one (already
// handled unconditionally by IsRDMLoggable) must pass through untouched
// rather than accidentally being caught or counted by this throttle.
func TestUnknownOpcodeThrottle_IgnoresOtherKinds(t *testing.T) {
	th := NewUnknownOpcodeThrottle(2)
	for _, e := range []Entry{
		{Dir: DirIn, Peer: testPeer, Kind: "ArtDmx"},
		{Dir: DirIn, Peer: testPeer, Kind: KindUndecoded, DecodeErr: "artnet: invalid packet ID"},
		{Dir: DirIn, Peer: testPeer, Kind: "ArtRdm"},
	} {
		if _, ok := th.Consider(e); ok {
			t.Fatalf("Consider(Kind=%q) = true, want false", e.Kind)
		}
	}
}

// TestUnknownOpcodeThrottle_PairsAreIndependent confirms the cap is keyed by
// (peer, opcode), not opcode alone or peer alone — a second peer, or a
// second opcode from the same peer, gets its own fresh budget.
func TestUnknownOpcodeThrottle_PairsAreIndependent(t *testing.T) {
	const cap = 2
	th := NewUnknownOpcodeThrottle(cap)
	peerA := testPeer
	peerB := netip.MustParseAddrPort("10.0.0.9:6454")

	// Exhaust peerA/0x5200's budget plus its one suppression entry.
	for i := 0; i < cap+1; i++ {
		if _, ok := th.Consider(Entry{Dir: DirIn, Peer: peerA, Kind: KindUnrecognized, UnknownOpCode: 0x5200}); !ok {
			t.Fatalf("peerA/0x5200 sighting %d unexpectedly suppressed", i+1)
		}
	}
	if _, ok := th.Consider(Entry{Dir: DirIn, Peer: peerA, Kind: KindUnrecognized, UnknownOpCode: 0x5200}); ok {
		t.Fatal("peerA/0x5200 should now be suppressed")
	}

	// A different opcode from the same peer still gets logged.
	if _, ok := th.Consider(Entry{Dir: DirIn, Peer: peerA, Kind: KindUnrecognized, UnknownOpCode: 0x9999}); !ok {
		t.Error("a different opcode from the same peer should have its own budget")
	}
	// The same opcode from a different peer still gets logged.
	if _, ok := th.Consider(Entry{Dir: DirIn, Peer: peerB, Kind: KindUnrecognized, UnknownOpCode: 0x5200}); !ok {
		t.Error("the same opcode from a different peer should have its own budget")
	}
}

// TestUnrecognizedOpcodeReachesRDMRingBoundedAndLogsWithSuppression is the
// end-to-end shape (mirrors cmd/benny512's tap wiring): decode, general
// ring, IsRDMLoggable (false for these), then the throttle. Confirms the
// RDM-only ring ends up with exactly cap+1 entries for a flood from one
// (peer, opcode) pair — the real ones plus one suppression entry — not one
// per datagram.
func TestUnrecognizedOpcodeReachesRDMRingBoundedAndLogsWithSuppression(t *testing.T) {
	const cap = 4
	general := New(1000)
	rdmRing := New(1000)
	th := NewUnknownOpcodeThrottle(cap)

	raw := artnet.Encode(artnet.Packet{Kind: artnet.KindUnknown, UnknownOpCode: 0x5200})

	var suppressionLines int
	const flood = 50
	for i := 0; i < flood; i++ {
		e := DecodeEntry(DirIn, testPeer, raw)
		e = general.Add(e)
		if IsRDMLoggable(e) {
			t.Fatalf("iteration %d: a valid-but-unrecognized-opcode entry should not be IsRDMLoggable", i)
		}
		if logEntry, ok := th.Consider(e); ok {
			logEntry = rdmRing.Add(logEntry)
			if strings.Contains(FormatEntryText(logEntry), "suppress") {
				suppressionLines++
			}
		}
	}

	if len(general.Snapshot(Filter{}, 0)) != flood {
		t.Fatalf("general ring should have every datagram regardless of throttling")
	}
	got := rdmRing.Snapshot(Filter{}, 0)
	if len(got) != cap+1 {
		t.Fatalf("RDM ring has %d entries for a %d-packet flood from one (peer,opcode) pair, want %d (cap + one suppression entry)",
			len(got), flood, cap+1)
	}
	if suppressionLines != 1 {
		t.Fatalf("suppression lines logged = %d, want exactly 1", suppressionLines)
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
