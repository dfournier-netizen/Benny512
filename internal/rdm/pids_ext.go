package rdm

// This file adds the Parameter ID constants introduced by the manufacturer-
// PID/sensors/non-fixture research pass
// (rdm-pids-sensors-research_2026-08-13_2347.md §6) to the base set already
// declared in message.go. ParameterID is an open type (see message.go's
// doc comment) — these are convenience names, not a closed enum.
//
// Status: PID *numbers* are CONFIRMED via OLA RDMEnums.h (report §6, single
// primary-derived source for the numeric values). The one-line purpose
// descriptions in the doc comments are standard/self-descriptive RDM naming
// conventions and are WEAKLY CONFIRMED (not independently re-verified prose
// per PID this session) — see report §6 preamble.

// --- E1.37-1: Dimmer Message Sets (report §6.1) ---
const (
	PIDDMXBlockAddress ParameterID = 0x0140 // block-contiguous DMX start address across sub-devices
	PIDDMXFailMode     ParameterID = 0x0141 // behavior on loss of DMX signal
	PIDDMXStartupMode  ParameterID = 0x0142 // behavior at power-up before DMX received

	PIDDimmerInfo                    ParameterID = 0x0340
	PIDMinimumLevel                  ParameterID = 0x0341
	PIDMaximumLevel                  ParameterID = 0x0342
	PIDCurve                         ParameterID = 0x0343
	PIDCurveDescription              ParameterID = 0x0344
	PIDOutputResponseTime            ParameterID = 0x0345
	PIDOutputResponseTimeDescription ParameterID = 0x0346
	// PIDModulationFrequency is the mechanism-shaped PID family the Chroma-Q
	// "Frequency" byte table (report §7.1) most plausibly maps to, though the
	// Chroma-Q manual itself does not name a PID number — UNVERIFIED mapping,
	// confirm live via SUPPORTED_PARAMETERS/PARAMETER_DESCRIPTION walking
	// rather than assuming this PID is what a given fixture uses.
	PIDModulationFrequency            ParameterID = 0x0347
	PIDModulationFrequencyDescription ParameterID = 0x0348

	PIDBurnIn ParameterID = 0x0440

	PIDLockPin              ParameterID = 0x0640
	PIDLockState            ParameterID = 0x0641
	PIDLockStateDescription ParameterID = 0x0642

	PIDIdentifyMode    ParameterID = 0x1040
	PIDPresetInfo      ParameterID = 0x1041
	PIDPresetStatus    ParameterID = 0x1042
	PIDPresetMergeMode ParameterID = 0x1043
	PIDPowerOnSelfTest ParameterID = 0x1044
)

// --- E1.37-2: IPv4 & DNS Configuration Messages (report §6.2) ---
const (
	PIDListInterfaces                ParameterID = 0x0700
	PIDInterfaceLabel                ParameterID = 0x0701
	PIDInterfaceHardwareAddressType1 ParameterID = 0x0702
	PIDIPv4DHCPMode                  ParameterID = 0x0703
	PIDIPv4ZeroconfMode              ParameterID = 0x0704
	PIDIPv4CurrentAddress            ParameterID = 0x0705
	PIDIPv4StaticAddress             ParameterID = 0x0706
	PIDInterfaceRenewDHCP            ParameterID = 0x0707
	PIDInterfaceReleaseDHCP          ParameterID = 0x0708
	PIDInterfaceApplyConfiguration   ParameterID = 0x0709
	PIDIPv4DefaultRoute              ParameterID = 0x070A
	PIDDNSNameServer                 ParameterID = 0x070B
	PIDDNSHostname                   ParameterID = 0x070C
	PIDDNSDomainName                 ParameterID = 0x070D
)

// E1.37-2 constants of note (report §6.2, CONFIRMED via OLA RDMEnums.h).
const (
	MaxRDMHostnameLength   = 63
	MaxRDMDomainNameLength = 231
	// DNSNameServerMaxIndex is the highest valid index into the up-to-3
	// configured DNS_NAME_SERVER slots (0-2).
	DNSNameServerMaxIndex = 2
)

// DHCPStatus is IPV4_DHCP_MODE-family status byte.
type DHCPStatus byte

// DHCP status values.
const (
	DHCPStatusInactive DHCPStatus = 0x00
	DHCPStatusActive   DHCPStatus = 0x01
	DHCPStatusUnknown  DHCPStatus = 0x02
)

// --- E1.37-7: Gateway & Splitter Configuration Messages (report §6.3) ---
//
// UNVERIFIED whether any real gateway in Dom's kit (notably the Netron EN4)
// actually implements this document on the wire — the EN4's own manual only
// documents per-port config as front-panel/web-UI, not as remote RDM PIDs
// (report §7.3). Treat these PIDs as "worth probing for", not "known
// present"; the app's per-port endpoint model (registry.Port /
// params.Endpoint*, see registry package) works from the safer
// PROXIED_DEVICES/PROXIED_DEVICE_COUNT baseline when E1.37-7 support can't
// be confirmed.
const (
	PIDEndpointList                            ParameterID = 0x0900
	PIDEndpointListChange                      ParameterID = 0x0901
	PIDIdentifyEndpoint                        ParameterID = 0x0902
	PIDEndpointToUniverse                      ParameterID = 0x0903
	PIDEndpointMode                            ParameterID = 0x0904
	PIDEndpointLabel                           ParameterID = 0x0905
	PIDRDMTrafficEnable                        ParameterID = 0x0906
	PIDDiscoveryState                          ParameterID = 0x0907
	PIDBackgroundDiscovery                     ParameterID = 0x0908
	PIDEndpointTiming                          ParameterID = 0x0909
	PIDEndpointTimingDescription               ParameterID = 0x090A
	PIDEndpointResponders                      ParameterID = 0x090B
	PIDEndpointResponderListChange             ParameterID = 0x090C
	PIDBindingControlFields                    ParameterID = 0x090D
	PIDBackgroundQueuedStatusPolicy            ParameterID = 0x090E
	PIDBackgroundQueuedStatusPolicyDescription ParameterID = 0x090F
)

// --- E1.33 (RDMnet) bootstrapping PIDs (report §6.4, context only) ---
const (
	PIDComponentScope ParameterID = 0x0800
	PIDSearchDomain   ParameterID = 0x0801
	PIDTCPCommsStatus ParameterID = 0x0802
	PIDBrokerStatus   ParameterID = 0x0803
)

// METADATA_JSON / METADATA_JSON_URL (E1.37-5) — the app deliberately does
// NOT decode these: report §1.1 Gap 1 recommends rendering unknown
// enumerated-looking manufacturer fields as a bounded numeric stepper rather
// than chasing per-vendor enum labels, and treats METADATA_JSON support as
// rare in the field. The PID numbers are recorded here only so a future pass
// can wire them in without re-deriving them.
const (
	PIDMetadataJSON    ParameterID = 0x0053
	PIDMetadataJSONURL ParameterID = 0x0054
)

// --- E1.20-2025 §10.4.4-10.4.9: enhanced introspection PIDs (Table A-3) ---
//
// Unlike the OLA-derived constants elsewhere in this file, these six values
// are transcribed directly from the ANSI E1.20-2025 PDF's own Table A-3
// (Phase D backend pass, orchestrator-supplied primary source) — CONFIRMED,
// not merely cross-referenced. They round out the RDM Information category
// SUPPORTED_PARAMETERS/PARAMETER_DESCRIPTION (0x0050/0x0051) already declare
// in message.go: a controller that already knows how to walk
// SUPPORTED_PARAMETERS has no use for these (they exist for controllers with
// tighter transaction budgets that want a packed/enumerated shortcut), so
// this package classifies all six PIDTierHidden — see classification.go.
const (
	PIDSupportedParametersEnhanced ParameterID = 0x0055
	PIDControllerFlagSupport       ParameterID = 0x0056
	PIDNackDescription             ParameterID = 0x0057
	PIDPackedPIDSub                ParameterID = 0x0058
	PIDPackedPIDIndex              ParameterID = 0x0059
	PIDEnumLabel                   ParameterID = 0x005A
)
