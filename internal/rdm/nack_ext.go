package rdm

// Extended NACK reason codes beyond Phase 1a's confirmed 0x0000-0x000A
// (report §5.6). WEAKLY CONFIRMED — single source (OLA RDMEnums.h), not
// cross-checked against ETCLabs defs.h (which stops at 0x000A).
const (
	NackActionNotSupported           NackReason = 0x000B
	NackEndpointNumberInvalid        NackReason = 0x000C
	NackInvalidEndpointMode          NackReason = 0x000D
	NackUnknownUID                   NackReason = 0x000E
	NackUnknownScope                 NackReason = 0x000F
	NackInvalidStaticConfigType      NackReason = 0x0010
	NackInvalidIPv4Address           NackReason = 0x0011
	NackInvalidIPv6Address           NackReason = 0x0012
	NackInvalidPort                  NackReason = 0x0013
	NackDeviceAbsent                 NackReason = 0x0014
	NackSensorOutOfRange             NackReason = 0x0015
	NackSensorFault                  NackReason = 0x0016
	NackPackingNotSupported          NackReason = 0x0017
	NackErrorInPackedListTransaction NackReason = 0x0018
	NackProxyDrop                    NackReason = 0x0019
	NackAllCallSetFail               NackReason = 0x0020
)
