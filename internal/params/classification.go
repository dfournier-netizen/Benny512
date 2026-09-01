package params

import "benny512/internal/rdm"

// This file implements Phase D task 1's PID classification table: a
// three-tier UI-presentation hint (Promoted/Standard/Hidden) for every PID
// the generic parameter editor might otherwise show as an undifferentiated
// raw-hex row. It answers "how prominently should the UI show this PID",
// which is a different question from isEditorTarget/knownDecodedESTAPIDs in
// introspect.go ("does this PID get a row in the generic editor at all") —
// see isEditorTarget's doc comment for how the two combine: Hidden-tier
// PIDs are additionally excluded from the generic editor outright (Task 1's
// "hide them as rows" ask), while Promoted/Standard both still get one
// unless a dedicated typed field (knownDecodedESTAPIDs) already covers them.
//
// Design requirements (owner-approved brief, Phase D task 1):
//
//   - Backed by a map, so adding a PID later — the owner has said he will
//     ask for more user-facing PIDs after seeing this — is a one-line
//     addition to pidTiers below, nothing else in this file or its callers
//     needs to change.
//   - The fallback for a PID with no entry in pidTiers is TierStandard,
//     never TierHidden. TierHidden is deliberately reachable only by an
//     explicit map entry: defaulting new/unrecognized PIDs to hidden would
//     silently swallow real spec-defined data from a future fixture this
//     table hasn't been taught about yet, which is a much worse failure
//     mode than an unfamiliar PID landing in the generic editor's ordinary
//     "Standard" bucket, unlabeled but visible. See TestUnknownPIDDefaultsToStandard.
//   - Manufacturer-specific PIDs (rdm.ParameterID.IsManufacturerSpecific,
//     0x8000-0xFFDF) are never given a pidTiers entry and so always fall
//     through to the TierStandard default — they're self-describing via
//     PARAMETER_DESCRIPTION, and that machinery must not change here.
//   - Display names are NOT duplicated here. Tier answers "how prominent",
//     package capture's PIDName (already reused by internal/web/device.go's
//     paramLabel) answers "what is it called" — params cannot import
//     capture directly (capture already imports params, for its own
//     PIDName-adjacent RDM detail rendering; the reverse import would be a
//     cycle), so the web layer, which already imports both, is where Tier
//     and PIDName are combined into one JSON row. See
//     internal/web/device.go's paramDescriptorJSON.Tier field.

// PIDTier is a UI-presentation priority hint for one Parameter ID: how
// prominently a device-detail screen should surface it, independent of
// whether the device is even known to support it. The zero value is
// TierStandard — see Tier's doc comment for why that fallback matters.
type PIDTier int

// Tiers.
const (
	// TierStandard is both the ordinary "unremarkable but real" tier and
	// the default for any PID pidTiers has no explicit entry for.
	TierStandard PIDTier = iota
	// TierPromoted marks a PID the owner has asked to see given prominent,
	// dedicated UI treatment (its own field/section) rather than a row
	// among many in a generic list.
	TierPromoted
	// TierHidden marks a PID that should never appear as a generic-editor
	// row at all — either because it's protocol/introspection plumbing a
	// lighting tech has no use for (DISC_*, SUPPORTED_PARAMETERS, the
	// STATUS_* queue family, ...), or because a dedicated, structured
	// surface already exists for the fact it carries (PROXIED_DEVICES/
	// PROXIED_DEVICE_COUNT -> fixtureJSON.ProxiedDeviceCount; see
	// introspect.go's isEditorTarget and this task's wrinkle note below).
	TierHidden
)

// String renders a lowercase tag matching the JSON value
// paramDescriptorJSON.Tier uses (internal/web/device.go).
func (t PIDTier) String() string {
	switch t {
	case TierPromoted:
		return "promoted"
	case TierHidden:
		return "hidden"
	default:
		return "standard"
	}
}

// Tier classifies pid for UI presentation. See the file doc comment for the
// fallback rule (TierStandard, never TierHidden) and TestUnknownPIDDefaultsToStandard
// for the test that pins it down.
func Tier(pid rdm.ParameterID) PIDTier {
	if t, ok := pidTiers[pid]; ok {
		return t
	}
	return TierStandard
}

// pidTiers is the single source of truth this file promises is a one-line
// change to extend. Every PID below is one of the base rdm.ParameterID
// constants (message.go) or an rdm/pids_ext.go extension constant — no new
// numeric literals are invented here.
var pidTiers = map[rdm.ParameterID]PIDTier{
	// --- TierPromoted: owner-approved, Phase D task 1 brief -------------

	// Already promoted (kept).
	rdm.PIDDeviceLabel:     TierPromoted,
	rdm.PIDDMXStartAddress: TierPromoted,
	rdm.PIDDMXPersonality:  TierPromoted,
	rdm.PIDIdentifyDevice:  TierPromoted,

	// "Service life" family (E1.20 §10.8) — see internal/params/servicelife.go
	// for the typed Client methods backing these.
	rdm.PIDDeviceHours:       TierPromoted,
	rdm.PIDLampHours:         TierPromoted,
	rdm.PIDLampStrikes:       TierPromoted,
	rdm.PIDLampState:         TierPromoted,
	rdm.PIDDevicePowerCycles: TierPromoted,

	// Dimmer curve family this package already has typed fields for
	// (internal/rdm/dimmer.go, internal/params/params.go).
	rdm.PIDCurve:        TierPromoted,
	rdm.PIDMinimumLevel: TierPromoted,
	rdm.PIDMaximumLevel: TierPromoted,

	// Destructive actions (Phase D task 2) — confirm-gated dedicated
	// endpoints, not a plain generic-editor SET. See internal/web/device.go's
	// handleResetDevice/handleSetFactoryDefaults.
	rdm.PIDResetDevice:     TierPromoted,
	rdm.PIDFactoryDefaults: TierPromoted,

	// Pan/tilt orientation.
	rdm.PIDPanInvert:   TierPromoted,
	rdm.PIDTiltInvert:  TierPromoted,
	rdm.PIDPanTiltSwap: TierPromoted,

	// SLOT_INFO/SLOT_DESCRIPTION (E1.20 §10.6.4/§10.6.5) — the function-
	// aware Rig Check foundation's RDM-inference path (decision 3, task
	// brief) for a fixture with no GDTF data: see internal/rdm/slotinfo.go
	// and internal/web/patchattrs.go. Promoted rather than left to default
	// to Standard because these are exactly the two PIDs that feed a
	// dedicated structured surface (patch.ChannelFunction, Source==
	// SourceRDMInferred), the same reasoning TierPromoted already applies
	// to DMX_PERSONALITY/DMX_START_ADDRESS above.
	rdm.PIDSlotInfo:        TierPromoted,
	rdm.PIDSlotDescription: TierPromoted,

	// Display settings.
	rdm.PIDDisplayInvert: TierPromoted,
	rdm.PIDDisplayLevel:  TierPromoted,

	// E1.20 §10.11 "Device Control Parameter Messages" — the remaining four
	// user-facing controls the owner asked for (IDENTIFY_DEVICE/RESET_DEVICE
	// above already cover §10.11.1/10.11.2). SELF_TEST_DESCRIPTION (0x1021)
	// and SELFTEST_ENHANCED (0x1022) are deliberately NOT given their own
	// entry here — they're companion/informational PIDs the PERFORM_SELFTEST
	// control consumes internally (the same relationship
	// DMX_PERSONALITY_DESCRIPTION has to DMX_PERSONALITY, which also has no
	// pidTiers entry of its own), not standalone rows a tech would look for.
	rdm.PIDPowerState:      TierPromoted,
	rdm.PIDPerformSelfTest: TierPromoted,
	rdm.PIDCapturePreset:   TierPromoted,
	rdm.PIDPresetPlayback:  TierPromoted,

	// --- TierHidden: protocol/introspection plumbing, or superseded by a
	// dedicated structured surface elsewhere. Every entry below is an
	// explicit opt-in per this file's "never default to Hidden" rule. -----

	// Network Management / discovery (never meaningful as an editor row —
	// these ARE the discovery protocol, not device state).
	rdm.PIDDiscUniqueBranch: TierHidden,
	rdm.PIDDiscMute:         TierHidden,
	rdm.PIDDiscUnMute:       TierHidden,

	// Proxy family. PROXIED_DEVICES/PROXIED_DEVICE_COUNT used to be
	// deliberately left OUT of knownDecodedESTAPIDs (introspect.go) so they
	// still got a generic-editor row, because the UI had at the time
	// dropped its dedicated proxy-status callout. The owner has since asked
	// for the opposite: hide them as rows again, now that the callout is
	// coming back as structured data (fixtureJSON.ProxiedDeviceCount /
	// .ProxiedDeviceCountKnown / .ProxiedListChanged, populated by
	// registry.reclassify from the same PROXIED_DEVICE_COUNT ACK — see
	// internal/registry/registry.go and internal/web/server.go). Tier alone
	// now drives that hiding (isEditorTarget consults it), so no second
	// "is this reachable via the editor" list needs to agree with this one.
	rdm.PIDProxiedDevices:     TierHidden,
	rdm.PIDProxiedDeviceCount: TierHidden,
	rdm.PIDCommsStatus:        TierHidden,

	// Status Collection / queued-message family.
	rdm.PIDQueuedMessage:                  TierHidden,
	rdm.PIDStatusMessages:                 TierHidden,
	rdm.PIDStatusIDDescription:            TierHidden,
	rdm.PIDClearStatusID:                  TierHidden,
	rdm.PIDSubDeviceStatusReportThreshold: TierHidden,
	rdm.PIDQueuedMessageSensorSubscribe:   TierHidden,

	// RDM Information / introspection machinery (SUPPORTED_PARAMETERS,
	// PARAMETER_DESCRIPTION, and the enhanced/packed/enum-label shortcuts
	// E1.20-2025 §10.4.4-10.4.9 adds — see pids_ext.go). METADATA_JSON(_URL)
	// (E1.37-5) is introspection-adjacent in the same sense (a manufacturer
	// schema pointer this app deliberately doesn't decode — see pids_ext.go's
	// doc comment on those two) and is hidden for the same reason: none of
	// these are device *state* a lighting tech would ever want a row for.
	rdm.PIDSupportedParameters:         TierHidden,
	rdm.PIDParameterDescription:        TierHidden,
	rdm.PIDMetadataJSON:                TierHidden,
	rdm.PIDMetadataJSONURL:             TierHidden,
	rdm.PIDSupportedParametersEnhanced: TierHidden,
	rdm.PIDControllerFlagSupport:       TierHidden,
	rdm.PIDNackDescription:             TierHidden,
	rdm.PIDPackedPIDSub:                TierHidden,
	rdm.PIDPackedPIDIndex:              TierHidden,
	rdm.PIDEnumLabel:                   TierHidden,

	// Product Information identity PIDs — surfaced through fixtureJSON's
	// dedicated Manufacturer/Model/DeviceModelID fields (server.go), not as
	// generic-editor rows.
	rdm.PIDDeviceInfo:          TierHidden,
	rdm.PIDProductDetailIDList: TierHidden,

	// Sensor family — surfaced through the dedicated /api/device/{uid}/sensors
	// endpoint (device.go), not as generic-editor rows. SENSOR_VALUE itself
	// is left out of this table (defaults Standard) since it's already
	// excluded from the editor via knownDecodedESTAPIDs on its own terms and
	// has no reason to be hidden from a future caller that wants it.
	rdm.PIDSensorDefinition: TierHidden,
	rdm.PIDRecordSensors:    TierHidden,

	// E1.37-2 IPv4/DNS configuration — dedicated node-config surface
	// (internal/web/node_config.go), not the per-device parameter editor.
	rdm.PIDListInterfaces:                TierHidden,
	rdm.PIDInterfaceLabel:                TierHidden,
	rdm.PIDInterfaceHardwareAddressType1: TierHidden,
	rdm.PIDIPv4DHCPMode:                  TierHidden,
	rdm.PIDIPv4ZeroconfMode:              TierHidden,
	rdm.PIDIPv4CurrentAddress:            TierHidden,
	rdm.PIDIPv4StaticAddress:             TierHidden,
	rdm.PIDInterfaceRenewDHCP:            TierHidden,
	rdm.PIDInterfaceReleaseDHCP:          TierHidden,
	rdm.PIDInterfaceApplyConfiguration:   TierHidden,
	rdm.PIDIPv4DefaultRoute:              TierHidden,
	rdm.PIDDNSNameServer:                 TierHidden,
	rdm.PIDDNSHostname:                   TierHidden,
	rdm.PIDDNSDomainName:                 TierHidden,

	// E1.33 (RDMnet) bootstrapping PIDs — out of scope for an Art-Net/RDM
	// controller; nothing in this app ever GETs these today, but they're
	// listed explicitly per the brief rather than relying on the default.
	rdm.PIDComponentScope: TierHidden,
	rdm.PIDSearchDomain:   TierHidden,
	rdm.PIDTCPCommsStatus: TierHidden,
	rdm.PIDBrokerStatus:   TierHidden,

	// E1.37-7 Gateway & Splitter endpoint/gateway PIDs — a node/gateway
	// concern (registry.Port et al.), not a per-fixture parameter row.
	rdm.PIDEndpointList:                            TierHidden,
	rdm.PIDEndpointListChange:                      TierHidden,
	rdm.PIDIdentifyEndpoint:                        TierHidden,
	rdm.PIDEndpointToUniverse:                      TierHidden,
	rdm.PIDEndpointMode:                            TierHidden,
	rdm.PIDEndpointLabel:                           TierHidden,
	rdm.PIDRDMTrafficEnable:                        TierHidden,
	rdm.PIDDiscoveryState:                          TierHidden,
	rdm.PIDBackgroundDiscovery:                     TierHidden,
	rdm.PIDEndpointTiming:                          TierHidden,
	rdm.PIDEndpointTimingDescription:               TierHidden,
	rdm.PIDEndpointResponders:                      TierHidden,
	rdm.PIDEndpointResponderListChange:             TierHidden,
	rdm.PIDBindingControlFields:                    TierHidden,
	rdm.PIDBackgroundQueuedStatusPolicy:            TierHidden,
	rdm.PIDBackgroundQueuedStatusPolicyDescription: TierHidden,
}
