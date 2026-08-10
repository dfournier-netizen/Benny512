package rdm

// CommandClass is the RDM Command Class (slot 20). Closed six-value set per
// ANSI E1.20.
type CommandClass byte

// Command Class values (slot 20).
const (
	DiscoveryCommand         CommandClass = 0x10
	DiscoveryCommandResponse CommandClass = 0x11
	GetCommand               CommandClass = 0x20
	GetCommandResponse       CommandClass = 0x21
	SetCommand               CommandClass = 0x30
	SetCommandResponse       CommandClass = 0x31
)

// IsResponse reports whether cc is one of the three *Response classes.
func (cc CommandClass) IsResponse() bool {
	switch cc {
	case DiscoveryCommandResponse, GetCommandResponse, SetCommandResponse:
		return true
	case DiscoveryCommand, GetCommand, SetCommand:
		return false
	default:
		return false
	}
}

// IsValid reports whether cc is one of the six defined command classes.
func (cc CommandClass) IsValid() bool {
	switch cc {
	case DiscoveryCommand, DiscoveryCommandResponse, GetCommand, GetCommandResponse, SetCommand, SetCommandResponse:
		return true
	default:
		return false
	}
}

// ResponseType is slot 16, meaningful only when CommandClass.IsResponse().
type ResponseType byte

// Response Type values (slot 16 in responses).
const (
	ResponseACK         ResponseType = 0x00
	ResponseACKTimer    ResponseType = 0x01
	ResponseNackReason  ResponseType = 0x02
	ResponseACKOverflow ResponseType = 0x03
)

// NackReason is the 2-byte Parameter Data payload when Response Type =
// NACK_REASON. Modeled as an open type (not a closed enum) because E1.37/
// E1.33 define further reason codes past 0x000A that should still round-trip
// losslessly through the codec.
type NackReason uint16

// Well-known NACK reason codes 0x0000-0x000A.
const (
	NackUnknownPID              NackReason = 0x0000
	NackFormatError             NackReason = 0x0001
	NackHardwareFault           NackReason = 0x0002
	NackProxyReject             NackReason = 0x0003
	NackWriteProtect            NackReason = 0x0004
	NackUnsupportedCommandClass NackReason = 0x0005
	NackDataOutOfRange          NackReason = 0x0006
	NackBufferFull              NackReason = 0x0007
	NackPacketSizeUnsupported   NackReason = 0x0008
	NackSubDeviceOutOfRange     NackReason = 0x0009
	NackProxyBufferFull         NackReason = 0x000A
)

// ParameterID is the RDM Parameter ID (slots 21-22). Modeled as an open type
// rather than a closed enum: 0x8000-0xFFDF is manufacturer-specific space, so
// any 16-bit value is legal on the wire.
type ParameterID uint16

// Well-known Parameter IDs.
const (
	PIDDiscUniqueBranch ParameterID = 0x0001
	PIDDiscMute         ParameterID = 0x0002
	PIDDiscUnMute       ParameterID = 0x0003

	PIDProxiedDevices     ParameterID = 0x0010
	PIDProxiedDeviceCount ParameterID = 0x0011
	PIDCommsStatus        ParameterID = 0x0015

	PIDQueuedMessage                  ParameterID = 0x0020
	PIDStatusMessages                 ParameterID = 0x0030
	PIDStatusIDDescription            ParameterID = 0x0031
	PIDClearStatusID                  ParameterID = 0x0032
	PIDSubDeviceStatusReportThreshold ParameterID = 0x0033

	PIDSupportedParameters       ParameterID = 0x0050
	PIDParameterDescription      ParameterID = 0x0051
	PIDDeviceInfo                ParameterID = 0x0060
	PIDProductDetailIDList       ParameterID = 0x0070
	PIDDeviceModelDescription    ParameterID = 0x0080
	PIDManufacturerLabel         ParameterID = 0x0081
	PIDDeviceLabel               ParameterID = 0x0082
	PIDFactoryDefaults           ParameterID = 0x0090
	PIDLanguageCapabilities      ParameterID = 0x00A0
	PIDLanguage                  ParameterID = 0x00B0
	PIDSoftwareVersionLabel      ParameterID = 0x00C0
	PIDBootSoftwareVersionID     ParameterID = 0x00C1
	PIDBootSoftwareVersionLabel  ParameterID = 0x00C2
	PIDDMXPersonality            ParameterID = 0x00E0
	PIDDMXPersonalityDescription ParameterID = 0x00E1
	PIDDMXStartAddress           ParameterID = 0x00F0
	PIDSlotInfo                  ParameterID = 0x0120
	PIDSlotDescription           ParameterID = 0x0121
	PIDDefaultSlotValue          ParameterID = 0x0122

	PIDSensorDefinition ParameterID = 0x0200
	PIDSensorValue      ParameterID = 0x0201
	PIDRecordSensors    ParameterID = 0x0202

	PIDDeviceHours       ParameterID = 0x0400
	PIDLampHours         ParameterID = 0x0401
	PIDLampStrikes       ParameterID = 0x0402
	PIDLampState         ParameterID = 0x0403
	PIDLampOnMode        ParameterID = 0x0404
	PIDDevicePowerCycles ParameterID = 0x0405
	PIDDisplayInvert     ParameterID = 0x0500
	PIDDisplayLevel      ParameterID = 0x0501
	PIDPanInvert         ParameterID = 0x0600
	PIDTiltInvert        ParameterID = 0x0601
	PIDPanTiltSwap       ParameterID = 0x0602
	PIDRealTimeClock     ParameterID = 0x0603

	PIDIdentifyDevice      ParameterID = 0x1000
	PIDResetDevice         ParameterID = 0x1001
	PIDPowerState          ParameterID = 0x1010
	PIDPerformSelfTest     ParameterID = 0x1020
	PIDSelfTestDescription ParameterID = 0x1021
	PIDCapturePreset       ParameterID = 0x1030
	PIDPresetPlayback      ParameterID = 0x1031
)

// RootDevice is the root RDM sub-device: 0x0000.
const RootDevice uint16 = 0x0000

// AllSubDevices is the "all sub-devices" target, SET only: 0xFFFF.
const AllSubDevices uint16 = 0xFFFF

// Message is a full ANSI E1.20 RDM message (slots 0..end, excluding the
// on-wire checksum, which Encode computes and Decode verifies).
//
// MessageLength and the checksum are wire-transport artifacts, not stored
// fields — they are fully determined by the other fields (24 +
// len(ParameterData)), so storing them separately would let them silently
// disagree with the payload. Encode recomputes them every call.
type Message struct {
	DestinationUID    UID
	SourceUID         UID
	TransactionNumber byte
	// PortIDOrResponseType is slot 16: Port ID in a request, Response Type in
	// a response. Stored as the raw wire byte; use ResponseType() for a typed
	// view when CommandClass.IsResponse().
	PortIDOrResponseType byte
	MessageCount         byte
	SubDevice            uint16
	CommandClass         CommandClass
	ParameterID          ParameterID
	ParameterData        []byte
}

// ResponseType returns a typed view of slot 16 when this message is a
// response, and ok=false for requests.
func (m Message) ResponseType() (rt ResponseType, ok bool) {
	if !m.CommandClass.IsResponse() {
		return 0, false
	}
	return ResponseType(m.PortIDOrResponseType), true
}

// MessageLength is slot 2: total slot count from Slot 0 through end of
// Parameter Data, excluding the 2-byte checksum.
func (m Message) MessageLength() int { return 24 + len(m.ParameterData) }
