package capture

// This file adds full RDM/ToD decode detail to capture Entries (report
// task item 3, priority ask: "every RDM message on the wire — both
// directions — must be captured with FULL decoded detail"). It leans on the
// existing rdm/params codecs rather than re-deriving any wire format; see
// each package's own doc comments for the source-confidence notes on the
// individual PIDs decoded below.
//
// Layering note: this pulls in internal/params (for DecodeDeviceInfo) and
// internal/session (for NackReasonName), both one-way dependencies already
// satisfied elsewhere in the module (neither imports capture), so no cycle
// is introduced.

import (
	"encoding/binary"
	"fmt"
	"strings"

	"benny512/internal/artnet"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/session"
)

// RDMDetail is the full decoded view of one ArtRdm entry's embedded RDM
// message. ParamDataHex is always populated when the message decoded at
// all (independent of the outer Entry.Hex truncation policy — RDM messages
// are small enough that this is never actually truncated in practice, but
// the field exists so exporters never have to reason about the outer
// threshold). Decoded is a best-effort human-readable interpretation for
// the PIDs this app has a codec for (DEVICE_INFO, PARAMETER_DESCRIPTION,
// SENSOR_*, STATUS_*, SUPPORTED_PARAMETERS, and a handful of simple
// labels/scalars) — empty when no codec applies, in which case ParamDataHex
// is the only representation (still always present).
type RDMDetail struct {
	SourceUID         string `json:"sourceUid,omitempty"`
	DestUID           string `json:"destUid,omitempty"`
	TransactionNumber byte   `json:"transactionNumber"`
	IsResponse        bool   `json:"isResponse"`
	// PortID is slot 16 in a request (meaningless/unset for a response).
	PortID byte `json:"portId,omitempty"`
	// ResponseType is slot 16's typed view in a response: ACK, ACK_TIMER,
	// NACK_REASON or ACK_OVERFLOW (empty for a request).
	ResponseType string `json:"responseType,omitempty"`
	MessageCount byte   `json:"messageCount,omitempty"`
	SubDevice    uint16 `json:"subDevice"`
	CommandClass string `json:"commandClass"`
	PID          uint16 `json:"pid"`
	PIDName      string `json:"pidName"`
	PDL          int    `json:"pdl"`
	// ParamDataHex is the raw parameter data, always present when the
	// message decoded (report task: "raw hex ALWAYS").
	ParamDataHex string `json:"paramDataHex"`
	// Decoded is the best-effort interpreted view, present only when a
	// codec applies to this PID/direction.
	Decoded       string `json:"decoded,omitempty"`
	ChecksumValid bool   `json:"checksumValid"`
	// DecodeError is set (and every other field left at its zero value,
	// aside from ParamDataHex which is unavailable too since PDL couldn't
	// be trusted) when the bytes did not decode as a standard RDM message —
	// notably a raw DUB (Discovery Unique Branch) response, which is
	// intentionally not framed as one. The entry's outer Hex field still
	// carries the full raw bytes regardless.
	DecodeError string `json:"decodeError,omitempty"`

	// NackReasonCode/NackReasonName are set when ResponseType ==
	// "NACK_REASON" (report task: "capture NACK reasons... explicitly").
	NackReasonCode uint16 `json:"nackReasonCode,omitempty"`
	NackReasonName string `json:"nackReasonName,omitempty"`

	// ACK_TIMER's 2-byte estimate, reported under both candidate unit
	// interpretations (report task: "capture ACK_TIMER values explicitly —
	// those matter for wireless-proxy debugging"; see
	// session.RDMConfig.AckTimerUnit's doc comment for the spec-ambiguity
	// this is deliberately not resolving on the capture side — the raw
	// units plus both readings are recorded so the architect can judge from
	// real hardware timing which one applies).
	AckTimerRawUnits    uint16 `json:"ackTimerRawUnits,omitempty"`
	AckTimerMsPerE120   int64  `json:"ackTimerMsPerE120,omitempty"`   // units * 10ms (ANSI E1.20 §6.3.3)
	AckTimerMsIfRawIsMs int64  `json:"ackTimerMsIfRawIsMs,omitempty"` // units taken literally as ms
}

// TodDetail is the decoded view of one ArtTodRequest/ArtTodData/
// ArtTodControl entry.
type TodDetail struct {
	// SubKind distinguishes which of the three ToD packet types this is:
	// "Request", "Data", or "Control".
	SubKind     string `json:"subKind"`
	Net         byte   `json:"net"`
	Command     byte   `json:"command"`
	CommandName string `json:"commandName"`
	// Addresses holds the low-byte Port-Address value(s): the requested
	// universe list for a Request, or the single target universe for
	// Data/Control.
	Addresses  []byte `json:"addresses,omitempty"`
	RdmVersion byte   `json:"rdmVersion,omitempty"` // Data only
	Port       byte   `json:"port,omitempty"`       // Data only
	UidTotal   uint16 `json:"uidTotal,omitempty"`   // Data only
	BlockCount byte   `json:"blockCount,omitempty"` // Data only
	// UIDs is the Table of Devices block carried by this ArtTodData packet
	// (empty for Request/Control).
	UIDs []string `json:"uids,omitempty"`
}

// attachRDMDetail populates e.RDM/e.Tod for the four RDM-family packet
// kinds; a no-op for anything else.
func attachRDMDetail(e *Entry, pkt artnet.Packet) {
	switch pkt.Kind {
	case artnet.KindRdm:
		e.RDM = buildRDMDetail(pkt.Rdm)
	case artnet.KindTodRequest:
		e.Tod = buildTodRequestDetail(pkt.TodRequest)
	case artnet.KindTodData:
		e.Tod = buildTodDataDetail(pkt.TodData)
	case artnet.KindTodControl:
		e.Tod = buildTodControlDetail(pkt.TodControl)
	}
}

func buildRDMDetail(p artnet.Rdm) *RDMDetail {
	d := &RDMDetail{}
	msg, err := p.DecodedRDMMessage()
	if err != nil {
		d.DecodeError = err.Error()
		// ChecksumValid stays false — either the checksum genuinely
		// mismatched, or the bytes never got far enough to compute one (e.g.
		// a raw DUB response, which isn't framed as a standard RDM message
		// at all; see the DecodeError field's doc comment).
		return d
	}
	d.ChecksumValid = true
	d.SourceUID = msg.SourceUID.String()
	d.DestUID = msg.DestinationUID.String()
	d.TransactionNumber = msg.TransactionNumber
	d.SubDevice = msg.SubDevice
	d.CommandClass = msg.CommandClass.String()
	d.PID = uint16(msg.ParameterID)
	d.PIDName = pidName(msg.ParameterID)
	d.PDL = len(msg.ParameterData)
	d.ParamDataHex = hexEncode(msg.ParameterData)
	d.IsResponse = msg.CommandClass.IsResponse()

	if d.IsResponse {
		rt, _ := msg.ResponseType()
		d.ResponseType = rt.String()
		d.MessageCount = msg.MessageCount
		switch rt {
		case rdm.ResponseNackReason:
			if len(msg.ParameterData) >= 2 {
				code := uint16(msg.ParameterData[0])<<8 | uint16(msg.ParameterData[1])
				d.NackReasonCode = code
				d.NackReasonName = session.NackReasonName(rdm.NackReason(code))
			}
		case rdm.ResponseACKTimer:
			if len(msg.ParameterData) >= 2 {
				units := uint16(msg.ParameterData[0])<<8 | uint16(msg.ParameterData[1])
				d.AckTimerRawUnits = units
				d.AckTimerMsPerE120 = int64(units) * 10
				d.AckTimerMsIfRawIsMs = int64(units)
			}
		}
	} else {
		d.PortID = msg.PortIDOrResponseType
	}

	d.Decoded = decodeParamDataString(msg.ParameterID, d.IsResponse, msg.ParameterData)
	return d
}

func buildTodRequestDetail(p artnet.TodRequest) *TodDetail {
	return &TodDetail{
		SubKind: "Request", Net: p.Net, Command: p.Command,
		CommandName: todRequestCommandName(p.Command),
		Addresses:   append([]byte(nil), p.Address...),
	}
}

func buildTodDataDetail(p artnet.TodData) *TodDetail {
	uids := make([]string, 0, len(p.Tod))
	for _, u := range p.Tod {
		uids = append(uids, u.String())
	}
	return &TodDetail{
		SubKind: "Data", Net: p.Net, Command: p.CommandResponse,
		CommandName: todDataCommandName(p.CommandResponse),
		Addresses:   []byte{p.Address},
		RdmVersion:  p.RdmVersion, Port: p.Port,
		UidTotal: p.UidTotal, BlockCount: p.BlockCount, UIDs: uids,
	}
}

func buildTodControlDetail(p artnet.TodControl) *TodDetail {
	return &TodDetail{
		SubKind: "Control", Net: p.Net, Command: p.Command,
		CommandName: todControlCommandName(p.Command),
		Addresses:   []byte{p.Address},
	}
}

func todRequestCommandName(c byte) string {
	if c == 0x00 {
		return "TodFull"
	}
	return fmt.Sprintf("Unknown_0x%02X", c)
}

func todDataCommandName(c byte) string {
	switch c {
	case 0x00:
		return "TodFull"
	case 0xFF:
		return "TodNak"
	default:
		return fmt.Sprintf("Unknown_0x%02X", c)
	}
}

func todControlCommandName(c byte) string {
	switch c {
	case 0x00:
		return "AtcNone"
	case 0x01:
		return "AtcFlush"
	default:
		return fmt.Sprintf("Unknown_0x%02X", c)
	}
}

// decodeParamDataString renders a best-effort human-readable interpretation
// of one message's Parameter Data, for the PIDs this app has a codec for.
// Returns "" when no codec applies (callers fall back to ParamDataHex,
// which is always populated separately).
func decodeParamDataString(pid rdm.ParameterID, isResponse bool, data []byte) string {
	switch pid {
	case rdm.PIDDeviceInfo:
		if isResponse {
			if di, err := params.DecodeDeviceInfo(data); err == nil {
				return fmt.Sprintf(
					"protoVer=%d.%d modelId=0x%04X category=0x%04X swVer=0x%08X footprint=%d personality=%d/%d startAddr=%d subDevices=%d sensors=%d",
					di.ProtocolVersionMajor, di.ProtocolVersionMinor, di.DeviceModelID, di.ProductCategory,
					di.SoftwareVersionID, di.DMXFootprint, di.CurrentPersonality, di.PersonalityCount,
					di.DMXStartAddress, di.SubDeviceCount, di.SensorCount)
			}
		}

	case rdm.PIDParameterDescription:
		if isResponse {
			if pd, err := rdm.DecodeParameterDescription(data); err == nil {
				return fmt.Sprintf(
					"pid=0x%04X pdlSize=%d dataType=%s cc=%s unit=%s min=%d max=%d default=%d desc=%q",
					uint16(pd.PID), pd.PDLSize, pd.DataType, pd.CommandClass, unitLabel(pd.Unit),
					pd.MinValue, pd.MaxValue, pd.DefaultValue, pd.Description)
			}
		} else if len(data) >= 2 {
			req := rdm.ParameterID(binary.BigEndian.Uint16(data))
			return fmt.Sprintf("requesting description of PID 0x%04X (%s)", uint16(req), pidName(req))
		}

	case rdm.PIDSensorDefinition:
		if isResponse {
			if sd, err := rdm.DecodeSensorDefinition(data); err == nil {
				return fmt.Sprintf(
					"sensor#%d type=%s unit=%s range=[%d,%d] normal=[%d,%d] recording=0x%02X desc=%q",
					sd.SensorNumber, sd.Type, unitLabel(sd.Unit), sd.RangeMin, sd.RangeMax,
					sd.NormalMin, sd.NormalMax, sd.SupportsRecording, sd.Description)
			}
		} else if len(data) >= 1 {
			return fmt.Sprintf("requesting sensor #%d", data[0])
		}

	case rdm.PIDSensorValue:
		if len(data) == 9 {
			if sv, err := rdm.DecodeSensorValue(data); err == nil {
				return fmt.Sprintf("sensor#%d present=%d lowest=%d highest=%d recorded=%d",
					sv.SensorNumber, sv.Present, sv.Lowest, sv.Highest, sv.Recorded)
			}
		} else if len(data) == 1 {
			return fmt.Sprintf("sensor #%d", data[0])
		}

	case rdm.PIDStatusMessages:
		if isResponse {
			if msgs, err := rdm.DecodeStatusMessages(data); err == nil {
				if len(msgs) == 0 {
					return "no status messages"
				}
				parts := make([]string, 0, len(msgs))
				for _, m := range msgs {
					parts = append(parts, fmt.Sprintf("[sub=%d %s id=0x%04X v1=%d v2=%d]",
						m.SubDevice, m.Type, m.MessageID, m.Value1, m.Value2))
				}
				return strings.Join(parts, " ")
			}
		} else if len(data) >= 1 {
			return fmt.Sprintf("filter=%s", rdm.StatusType(data[0]))
		}

	case rdm.PIDSupportedParameters:
		if isResponse {
			if pids, err := rdm.DecodeSupportedParameters(data); err == nil {
				parts := make([]string, 0, len(pids))
				for _, p := range pids {
					parts = append(parts, fmt.Sprintf("0x%04X(%s)", uint16(p), pidName(p)))
				}
				return strings.Join(parts, " ")
			}
		}

	case rdm.PIDDMXStartAddress:
		if len(data) == 2 {
			return fmt.Sprintf("start address = %d", binary.BigEndian.Uint16(data))
		}

	case rdm.PIDDMXPersonality:
		if isResponse && len(data) == 2 {
			return fmt.Sprintf("personality %d of %d", data[0], data[1])
		} else if !isResponse && len(data) == 1 {
			return fmt.Sprintf("set personality %d", data[0])
		}

	case rdm.PIDIdentifyDevice:
		if len(data) == 1 {
			if data[0] != 0 {
				return "identify ON"
			}
			return "identify OFF"
		}

	case rdm.PIDDeviceLabel, rdm.PIDManufacturerLabel, rdm.PIDDeviceModelDescription,
		rdm.PIDSoftwareVersionLabel:
		if len(data) > 0 || !isResponse {
			return fmt.Sprintf("%q", string(data))
		}

	case rdm.PIDProductDetailIDList:
		if isResponse {
			if details, err := rdm.DecodeProductDetailIDList(data); err == nil {
				parts := make([]string, 0, len(details))
				for _, d := range details {
					parts = append(parts, fmt.Sprintf("0x%04X", uint16(d)))
				}
				return strings.Join(parts, " ")
			}
		}
	}
	return ""
}

func unitLabel(u rdm.Unit) string {
	if s := u.Suffix(); s != "" {
		return s
	}
	if u == rdm.UnitNone {
		return "none"
	}
	return fmt.Sprintf("unit_0x%02X", byte(u))
}

// pidName renders a well-known PID's ANSI E1.20/E1.37 mnemonic, or a
// manufacturer-specific/unknown marker for anything not in the table below.
// Numbers are transcribed from internal/rdm/message.go and pids_ext.go's own
// constants (see those files' doc comments for confirmation-status notes on
// the individual values); this is purely a display-name lookup, not a new
// source of protocol facts.
func pidName(pid rdm.ParameterID) string {
	if name, ok := pidNames[pid]; ok {
		return name
	}
	if pid.IsManufacturerSpecific() {
		return "Manufacturer-Specific"
	}
	return "Unknown"
}

var pidNames = map[rdm.ParameterID]string{
	rdm.PIDDiscUniqueBranch: "DISC_UNIQUE_BRANCH",
	rdm.PIDDiscMute:         "DISC_MUTE",
	rdm.PIDDiscUnMute:       "DISC_UN_MUTE",

	rdm.PIDProxiedDevices:     "PROXIED_DEVICES",
	rdm.PIDProxiedDeviceCount: "PROXIED_DEVICE_COUNT",
	rdm.PIDCommsStatus:        "COMMS_STATUS",

	rdm.PIDQueuedMessage:                  "QUEUED_MESSAGE",
	rdm.PIDStatusMessages:                 "STATUS_MESSAGES",
	rdm.PIDStatusIDDescription:            "STATUS_ID_DESCRIPTION",
	rdm.PIDClearStatusID:                  "CLEAR_STATUS_ID",
	rdm.PIDSubDeviceStatusReportThreshold: "SUB_DEVICE_STATUS_REPORT_THRESHOLD",

	rdm.PIDSupportedParameters:       "SUPPORTED_PARAMETERS",
	rdm.PIDParameterDescription:      "PARAMETER_DESCRIPTION",
	rdm.PIDDeviceInfo:                "DEVICE_INFO",
	rdm.PIDProductDetailIDList:       "PRODUCT_DETAIL_ID_LIST",
	rdm.PIDDeviceModelDescription:    "DEVICE_MODEL_DESCRIPTION",
	rdm.PIDManufacturerLabel:         "MANUFACTURER_LABEL",
	rdm.PIDDeviceLabel:               "DEVICE_LABEL",
	rdm.PIDFactoryDefaults:           "FACTORY_DEFAULTS",
	rdm.PIDLanguageCapabilities:      "LANGUAGE_CAPABILITIES",
	rdm.PIDLanguage:                  "LANGUAGE",
	rdm.PIDSoftwareVersionLabel:      "SOFTWARE_VERSION_LABEL",
	rdm.PIDBootSoftwareVersionID:     "BOOT_SOFTWARE_VERSION_ID",
	rdm.PIDBootSoftwareVersionLabel:  "BOOT_SOFTWARE_VERSION_LABEL",
	rdm.PIDDMXPersonality:            "DMX_PERSONALITY",
	rdm.PIDDMXPersonalityDescription: "DMX_PERSONALITY_DESCRIPTION",
	rdm.PIDDMXStartAddress:           "DMX_START_ADDRESS",
	rdm.PIDSlotInfo:                  "SLOT_INFO",
	rdm.PIDSlotDescription:           "SLOT_DESCRIPTION",
	rdm.PIDDefaultSlotValue:          "DEFAULT_SLOT_VALUE",

	rdm.PIDSensorDefinition: "SENSOR_DEFINITION",
	rdm.PIDSensorValue:      "SENSOR_VALUE",
	rdm.PIDRecordSensors:    "RECORD_SENSORS",

	rdm.PIDDeviceHours:       "DEVICE_HOURS",
	rdm.PIDLampHours:         "LAMP_HOURS",
	rdm.PIDLampStrikes:       "LAMP_STRIKES",
	rdm.PIDLampState:         "LAMP_STATE",
	rdm.PIDLampOnMode:        "LAMP_ON_MODE",
	rdm.PIDDevicePowerCycles: "DEVICE_POWER_CYCLES",
	rdm.PIDDisplayInvert:     "DISPLAY_INVERT",
	rdm.PIDDisplayLevel:      "DISPLAY_LEVEL",
	rdm.PIDPanInvert:         "PAN_INVERT",
	rdm.PIDTiltInvert:        "TILT_INVERT",
	rdm.PIDPanTiltSwap:       "PAN_TILT_SWAP",
	rdm.PIDRealTimeClock:     "REAL_TIME_CLOCK",

	rdm.PIDIdentifyDevice:      "IDENTIFY_DEVICE",
	rdm.PIDResetDevice:         "RESET_DEVICE",
	rdm.PIDPowerState:          "POWER_STATE",
	rdm.PIDPerformSelfTest:     "PERFORM_SELF_TEST",
	rdm.PIDSelfTestDescription: "SELF_TEST_DESCRIPTION",
	rdm.PIDCapturePreset:       "CAPTURE_PRESET",
	rdm.PIDPresetPlayback:      "PRESET_PLAYBACK",

	// E1.37-1 (pids_ext.go)
	rdm.PIDDMXBlockAddress: "DMX_BLOCK_ADDRESS",
	rdm.PIDDMXFailMode:     "DMX_FAIL_MODE",
	rdm.PIDDMXStartupMode:  "DMX_STARTUP_MODE",

	rdm.PIDDimmerInfo:                     "DIMMER_INFO",
	rdm.PIDMinimumLevel:                   "MINIMUM_LEVEL",
	rdm.PIDMaximumLevel:                   "MAXIMUM_LEVEL",
	rdm.PIDCurve:                          "CURVE",
	rdm.PIDCurveDescription:               "CURVE_DESCRIPTION",
	rdm.PIDOutputResponseTime:             "OUTPUT_RESPONSE_TIME",
	rdm.PIDOutputResponseTimeDescription:  "OUTPUT_RESPONSE_TIME_DESCRIPTION",
	rdm.PIDModulationFrequency:            "MODULATION_FREQUENCY",
	rdm.PIDModulationFrequencyDescription: "MODULATION_FREQUENCY_DESCRIPTION",

	rdm.PIDBurnIn: "BURN_IN",

	rdm.PIDLockPin:              "LOCK_PIN",
	rdm.PIDLockState:            "LOCK_STATE",
	rdm.PIDLockStateDescription: "LOCK_STATE_DESCRIPTION",

	rdm.PIDIdentifyMode:    "IDENTIFY_MODE",
	rdm.PIDPresetInfo:      "PRESET_INFO",
	rdm.PIDPresetStatus:    "PRESET_STATUS",
	rdm.PIDPresetMergeMode: "PRESET_MERGE_MODE",
	rdm.PIDPowerOnSelfTest: "POWER_ON_SELF_TEST",

	// E1.37-2 (pids_ext.go)
	rdm.PIDListInterfaces:                "LIST_INTERFACES",
	rdm.PIDInterfaceLabel:                "INTERFACE_LABEL",
	rdm.PIDInterfaceHardwareAddressType1: "INTERFACE_HARDWARE_ADDRESS_TYPE1",
	rdm.PIDIPv4DHCPMode:                  "IPV4_DHCP_MODE",
	rdm.PIDIPv4ZeroconfMode:              "IPV4_ZEROCONF_MODE",
	rdm.PIDIPv4CurrentAddress:            "IPV4_CURRENT_ADDRESS",
	rdm.PIDIPv4StaticAddress:             "IPV4_STATIC_ADDRESS",
	rdm.PIDInterfaceRenewDHCP:            "INTERFACE_RENEW_DHCP",
	rdm.PIDInterfaceReleaseDHCP:          "INTERFACE_RELEASE_DHCP",
	rdm.PIDInterfaceApplyConfiguration:   "INTERFACE_APPLY_CONFIGURATION",
	rdm.PIDIPv4DefaultRoute:              "IPV4_DEFAULT_ROUTE",
	rdm.PIDDNSNameServer:                 "DNS_NAME_SERVER",
	rdm.PIDDNSHostname:                   "DNS_HOSTNAME",
	rdm.PIDDNSDomainName:                 "DNS_DOMAIN_NAME",

	// E1.37-7 (pids_ext.go)
	rdm.PIDEndpointList:                            "ENDPOINT_LIST",
	rdm.PIDEndpointListChange:                      "ENDPOINT_LIST_CHANGE",
	rdm.PIDIdentifyEndpoint:                        "IDENTIFY_ENDPOINT",
	rdm.PIDEndpointToUniverse:                      "ENDPOINT_TO_UNIVERSE",
	rdm.PIDEndpointMode:                            "ENDPOINT_MODE",
	rdm.PIDEndpointLabel:                           "ENDPOINT_LABEL",
	rdm.PIDRDMTrafficEnable:                        "RDM_TRAFFIC_ENABLE",
	rdm.PIDDiscoveryState:                          "DISCOVERY_STATE",
	rdm.PIDBackgroundDiscovery:                     "BACKGROUND_DISCOVERY",
	rdm.PIDEndpointTiming:                          "ENDPOINT_TIMING",
	rdm.PIDEndpointTimingDescription:               "ENDPOINT_TIMING_DESCRIPTION",
	rdm.PIDEndpointResponders:                      "ENDPOINT_RESPONDERS",
	rdm.PIDEndpointResponderListChange:             "ENDPOINT_RESPONDER_LIST_CHANGE",
	rdm.PIDBindingControlFields:                    "BINDING_CONTROL_FIELDS",
	rdm.PIDBackgroundQueuedStatusPolicy:            "BACKGROUND_QUEUED_STATUS_POLICY",
	rdm.PIDBackgroundQueuedStatusPolicyDescription: "BACKGROUND_QUEUED_STATUS_POLICY_DESCRIPTION",

	// E1.33 bootstrap (pids_ext.go, context only)
	rdm.PIDComponentScope: "COMPONENT_SCOPE",
	rdm.PIDSearchDomain:   "SEARCH_DOMAIN",
	rdm.PIDTCPCommsStatus: "TCP_COMMS_STATUS",
	rdm.PIDBrokerStatus:   "BROKER_STATUS",

	rdm.PIDMetadataJSON:    "METADATA_JSON",
	rdm.PIDMetadataJSONURL: "METADATA_JSON_URL",
}
