package rdm

// This file transcribes the enum tables from
// rdm-pids-sensors-research_2026-08-13_2347.md §4-5 (product category/detail,
// data type, unit, prefix, command class, status type). Per-value CONFIRMED /
// WEAKLY CONFIRMED status from that report is preserved in comments; treat
// WEAKLY CONFIRMED values as "plausible, re-verify against real hardware"
// rather than spec-grade fact. Values with no confirmation note are
// CONFIRMED via two independent sources (OLA RDMEnums.h + ETCLabs defs.h) in
// the source report.

// DataType is PARAMETER_DESCRIPTION's data_type field (offset 3) — the wire
// shape of a PID's parameter data.
type DataType byte

// Data Type values (report §5.1). 0x09-0x12 are WEAKLY CONFIRMED — present
// in OLA's RDMEnums.h but not independently verified against ANSI text this
// session; a manufacturer PID reporting one of these is plausible but should
// be treated cautiously until seen on real hardware.
const (
	DSNotDefined    DataType = 0x00
	DSBitField      DataType = 0x01
	DSASCII         DataType = 0x02
	DSUnsignedByte  DataType = 0x03
	DSSignedByte    DataType = 0x04
	DSUnsignedWord  DataType = 0x05
	DSSignedWord    DataType = 0x06
	DSUnsignedDWord DataType = 0x07
	DSSignedDWord   DataType = 0x08
	DSUint64        DataType = 0x09 // WEAKLY CONFIRMED
	DSInt64         DataType = 0x0A // WEAKLY CONFIRMED
	DSGroup         DataType = 0x0B // WEAKLY CONFIRMED
	DSUID           DataType = 0x0C // WEAKLY CONFIRMED
	DSBoolean       DataType = 0x0D // WEAKLY CONFIRMED
	DSURL           DataType = 0x0E // WEAKLY CONFIRMED
	DSMAC           DataType = 0x0F // WEAKLY CONFIRMED
	DSIPv4          DataType = 0x10 // WEAKLY CONFIRMED
	DSIPv6          DataType = 0x11 // WEAKLY CONFIRMED
	DSEnumeration   DataType = 0x12 // WEAKLY CONFIRMED

	// DSManufacturerSpecificMin/Max bound the manufacturer-specific data-type
	// range (report §5.1: "range bound 128-223 explicit in pids.proto").
	DSManufacturerSpecificMin DataType = 0x80
	DSManufacturerSpecificMax DataType = 0xDF
)

// IsSigned reports whether raw min/max/default fields for this data type
// should be sign-extended from a 32-bit two's-complement value rather than
// read as a plain unsigned magnitude.
//
// WEAKLY CONFIRMED overall: OLA's pids.proto models PARAMETER_DESCRIPTION's
// min/max/default as bare UINT32 regardless of data_type (report §2.2's
// "sign discrepancy note") — this method encodes the app's own decoding
// policy (sign-interpret per data_type), which is the general RDM
// convention but was not independently re-derived from primary ANSI text
// this session. Flagged for hardware verification against a real
// DS_SIGNED_* manufacturer PID.
func (d DataType) IsSigned() bool {
	switch d {
	case DSSignedByte, DSSignedWord, DSSignedDWord, DSInt64:
		return true
	default:
		return false
	}
}

// IsManufacturerSpecific reports whether d falls in the manufacturer data-type range.
func (d DataType) IsManufacturerSpecific() bool {
	return d >= DSManufacturerSpecificMin && d <= DSManufacturerSpecificMax
}

// String renders a human label for the UI.
func (d DataType) String() string {
	switch d {
	case DSNotDefined:
		return "Not Defined"
	case DSBitField:
		return "Bit Field"
	case DSASCII:
		return "ASCII"
	case DSUnsignedByte:
		return "Unsigned Byte"
	case DSSignedByte:
		return "Signed Byte"
	case DSUnsignedWord:
		return "Unsigned Word"
	case DSSignedWord:
		return "Signed Word"
	case DSUnsignedDWord:
		return "Unsigned Dword"
	case DSSignedDWord:
		return "Signed Dword"
	case DSUint64:
		return "Unsigned 64-bit"
	case DSInt64:
		return "Signed 64-bit"
	case DSGroup:
		return "Group"
	case DSUID:
		return "UID"
	case DSBoolean:
		return "Boolean"
	case DSURL:
		return "URL"
	case DSMAC:
		return "MAC Address"
	case DSIPv4:
		return "IPv4 Address"
	case DSIPv6:
		return "IPv6 Address"
	case DSEnumeration:
		return "Enumeration"
	default:
		if d.IsManufacturerSpecific() {
			return "Manufacturer-Specific"
		}
		return "Unknown"
	}
}

// PDCommandClass is PARAMETER_DESCRIPTION's command_class field (offset 4) —
// distinct from rdm.CommandClass (the RDM message header's slot-20 field);
// this one only ever takes the three values below (report §5.4, CONFIRMED
// both sources).
type PDCommandClass byte

// PARAMETER_DESCRIPTION command_class values.
const (
	PDCommandClassGet    PDCommandClass = 0x01
	PDCommandClassSet    PDCommandClass = 0x02
	PDCommandClassGetSet PDCommandClass = 0x03
)

// String renders a human label.
func (c PDCommandClass) String() string {
	switch c {
	case PDCommandClassGet:
		return "GET"
	case PDCommandClassSet:
		return "SET"
	case PDCommandClassGetSet:
		return "GET_SET"
	default:
		return "Unknown"
	}
}

// SupportsGet/SupportsSet report whether the described PID's GET/SET side is
// implemented, driving whether the UI shows a "Set" control.
func (c PDCommandClass) SupportsGet() bool {
	return c == PDCommandClassGet || c == PDCommandClassGetSet
}
func (c PDCommandClass) SupportsSet() bool {
	return c == PDCommandClassSet || c == PDCommandClassGetSet
}

// Unit is PARAMETER_DESCRIPTION/SENSOR_DEFINITION's shared unit enum
// (report §5.2). 0x1D-0x24 are WEAKLY CONFIRMED (OLA-only, post-E1.37-2
// additions); 0x00-0x1C CONFIRMED both sources.
type Unit byte

// Unit values.
const (
	UnitNone               Unit = 0x00
	UnitCentigrade         Unit = 0x01
	UnitVoltsDC            Unit = 0x02
	UnitVoltsACPeak        Unit = 0x03
	UnitVoltsACRMS         Unit = 0x04
	UnitAmpsDC             Unit = 0x05
	UnitAmpsACPeak         Unit = 0x06
	UnitAmpsACRMS          Unit = 0x07
	UnitHertz              Unit = 0x08
	UnitOhms               Unit = 0x09
	UnitWatts              Unit = 0x0A
	UnitKilograms          Unit = 0x0B
	UnitMeters             Unit = 0x0C
	UnitMetersSquared      Unit = 0x0D
	UnitMetersCubed        Unit = 0x0E
	UnitKilogramsPerMeter3 Unit = 0x0F
	UnitMetersPerSecond    Unit = 0x10
	UnitMetersPerSecond2   Unit = 0x11
	UnitNewtons            Unit = 0x12
	UnitJoules             Unit = 0x13
	UnitPascals            Unit = 0x14
	UnitSeconds            Unit = 0x15
	UnitDegrees            Unit = 0x16
	UnitSteradian          Unit = 0x17
	UnitCandela            Unit = 0x18
	UnitLumens             Unit = 0x19
	UnitLux                Unit = 0x1A
	UnitIre                Unit = 0x1B
	UnitBytes              Unit = 0x1C
	UnitDecibel            Unit = 0x1D // WEAKLY CONFIRMED
	UnitDecibelVolt        Unit = 0x1E // WEAKLY CONFIRMED
	UnitDecibelWatt        Unit = 0x1F // WEAKLY CONFIRMED
	UnitDecibelMeter       Unit = 0x20 // WEAKLY CONFIRMED ("Decibel Meter" per report; name as given, not independently re-derived)
	UnitPercent            Unit = 0x21 // WEAKLY CONFIRMED
	UnitMolesPerMeter3     Unit = 0x22 // WEAKLY CONFIRMED
	UnitRPM                Unit = 0x23 // WEAKLY CONFIRMED
	UnitBytesPerSecond     Unit = 0x24 // WEAKLY CONFIRMED

	UnitManufacturerSpecificMin Unit = 0x80
	UnitManufacturerSpecificMax Unit = 0xFF
)

// Suffix returns the display suffix for a value already scaled to base
// units (i.e. after PrefixMultiplier has been applied) — see FormatValue.
// Manufacturer-specific and unrecognized values render as empty so callers
// fall back to a bare number.
func (u Unit) Suffix() string {
	switch u {
	case UnitNone:
		return ""
	case UnitCentigrade:
		return "°C"
	case UnitVoltsDC, UnitVoltsACPeak, UnitVoltsACRMS:
		return "V"
	case UnitAmpsDC, UnitAmpsACPeak, UnitAmpsACRMS:
		return "A"
	case UnitHertz:
		return "Hz"
	case UnitOhms:
		return "Ω"
	case UnitWatts:
		return "W"
	case UnitKilograms:
		return "kg"
	case UnitMeters:
		return "m"
	case UnitMetersSquared:
		return "m²"
	case UnitMetersCubed:
		return "m³"
	case UnitKilogramsPerMeter3:
		return "kg/m³"
	case UnitMetersPerSecond:
		return "m/s"
	case UnitMetersPerSecond2:
		return "m/s²"
	case UnitNewtons:
		return "N"
	case UnitJoules:
		return "J"
	case UnitPascals:
		return "Pa"
	case UnitSeconds:
		return "s"
	case UnitDegrees:
		return "°"
	case UnitSteradian:
		return "sr"
	case UnitCandela:
		return "cd"
	case UnitLumens:
		return "lm"
	case UnitLux:
		return "lx"
	case UnitIre:
		return "IRE"
	case UnitBytes:
		return "B"
	case UnitDecibel:
		return "dB"
	case UnitDecibelVolt:
		return "dBV"
	case UnitDecibelWatt:
		return "dBW"
	case UnitDecibelMeter:
		return "dBm"
	case UnitPercent:
		return "%"
	case UnitMolesPerMeter3:
		return "mol/m³"
	case UnitRPM:
		return "RPM"
	case UnitBytesPerSecond:
		return "B/s"
	default:
		return ""
	}
}

// IsManufacturerSpecific reports whether u is in the manufacturer unit range.
func (u Unit) IsManufacturerSpecific() bool {
	return u >= UnitManufacturerSpecificMin && u <= UnitManufacturerSpecificMax
}

// Prefix is PARAMETER_DESCRIPTION/SENSOR_DEFINITION's shared SI-prefix enum
// (report §5.3). CONFIRMED, full agreement both sources. 0x0B-0x10 are
// genuine gaps (reserved/unused), not an encoding error.
type Prefix byte

// Prefix values.
const (
	PrefixNone  Prefix = 0x00
	PrefixDeci  Prefix = 0x01
	PrefixCenti Prefix = 0x02
	PrefixMilli Prefix = 0x03
	PrefixMicro Prefix = 0x04
	PrefixNano  Prefix = 0x05
	PrefixPico  Prefix = 0x06
	PrefixFemto Prefix = 0x07
	PrefixAtto  Prefix = 0x08
	PrefixZepto Prefix = 0x09
	PrefixYocto Prefix = 0x0A
	// 0x0B-0x10 reserved/unused (gap CONFIRMED in both sources).
	PrefixDeca  Prefix = 0x11
	PrefixHecto Prefix = 0x12
	PrefixKilo  Prefix = 0x13
	PrefixMega  Prefix = 0x14
	PrefixGiga  Prefix = 0x15
	PrefixTera  Prefix = 0x16
	PrefixPeta  Prefix = 0x17
	PrefixExa   Prefix = 0x18
	PrefixZetta Prefix = 0x19
	PrefixYotta Prefix = 0x1A
)

// Multiplier returns the power-of-ten multiplier for p (report §1.2/§5.3:
// "apply the prefix as a power-of-ten multiplier to the raw value before
// display"). Unrecognized/reserved values return 1 (no scaling).
func (p Prefix) Multiplier() float64 {
	switch p {
	case PrefixNone:
		return 1
	case PrefixDeci:
		return 1e-1
	case PrefixCenti:
		return 1e-2
	case PrefixMilli:
		return 1e-3
	case PrefixMicro:
		return 1e-6
	case PrefixNano:
		return 1e-9
	case PrefixPico:
		return 1e-12
	case PrefixFemto:
		return 1e-15
	case PrefixAtto:
		return 1e-18
	case PrefixZepto:
		return 1e-21
	case PrefixYocto:
		return 1e-24
	case PrefixDeca:
		return 1e1
	case PrefixHecto:
		return 1e2
	case PrefixKilo:
		return 1e3
	case PrefixMega:
		return 1e6
	case PrefixGiga:
		return 1e9
	case PrefixTera:
		return 1e12
	case PrefixPeta:
		return 1e15
	case PrefixExa:
		return 1e18
	case PrefixZetta:
		return 1e21
	case PrefixYotta:
		return 1e24
	default:
		return 1
	}
}

// StatusType is STATUS_MESSAGES'/QUEUED_MESSAGE's severity/filter enum
// (report §5.5, CONFIRMED both sources).
type StatusType byte

// Status Type values.
const (
	StatusNone            StatusType = 0x00
	StatusGetLastMessage  StatusType = 0x01 // request-only filter value
	StatusAdvisory        StatusType = 0x02
	StatusWarning         StatusType = 0x03
	StatusError           StatusType = 0x04
	StatusAdvisoryCleared StatusType = 0x12
	StatusWarningCleared  StatusType = 0x13
	StatusErrorCleared    StatusType = 0x14
)

// String renders a human label.
func (s StatusType) String() string {
	switch s {
	case StatusNone:
		return "None"
	case StatusGetLastMessage:
		return "Last Message"
	case StatusAdvisory:
		return "Advisory"
	case StatusWarning:
		return "Warning"
	case StatusError:
		return "Error"
	case StatusAdvisoryCleared:
		return "Advisory Cleared"
	case StatusWarningCleared:
		return "Warning Cleared"
	case StatusErrorCleared:
		return "Error Cleared"
	default:
		return "Unknown"
	}
}

// SensorType is SENSOR_DEFINITION's type field (report §3.5). 0x00-0x20 and
// 0x7F CONFIRMED via 2 independent sources; 0x21-0x28 WEAKLY CONFIRMED
// (OLA-only extension, source of the addendum not independently identified).
type SensorType byte

// Sensor Type values.
const (
	SensorTemperature        SensorType = 0x00
	SensorVoltage            SensorType = 0x01
	SensorCurrent            SensorType = 0x02
	SensorFrequency          SensorType = 0x03
	SensorResistance         SensorType = 0x04
	SensorPower              SensorType = 0x05
	SensorMass               SensorType = 0x06
	SensorLength             SensorType = 0x07
	SensorArea               SensorType = 0x08
	SensorVolume             SensorType = 0x09
	SensorDensity            SensorType = 0x0A
	SensorVelocity           SensorType = 0x0B
	SensorAcceleration       SensorType = 0x0C
	SensorForce              SensorType = 0x0D
	SensorEnergy             SensorType = 0x0E
	SensorPressure           SensorType = 0x0F
	SensorTime               SensorType = 0x10
	SensorAngle              SensorType = 0x11
	SensorPositionX          SensorType = 0x12
	SensorPositionY          SensorType = 0x13
	SensorPositionZ          SensorType = 0x14
	SensorAngularVelocity    SensorType = 0x15
	SensorLuminousIntensity  SensorType = 0x16
	SensorLuminousFlux       SensorType = 0x17
	SensorIlluminance        SensorType = 0x18
	SensorChrominanceRed     SensorType = 0x19
	SensorChrominanceGreen   SensorType = 0x1A
	SensorChrominanceBlue    SensorType = 0x1B
	SensorContacts           SensorType = 0x1C
	SensorMemory             SensorType = 0x1D
	SensorItems              SensorType = 0x1E
	SensorHumidity           SensorType = 0x1F
	SensorCounter16Bit       SensorType = 0x20
	SensorCPULoad            SensorType = 0x21 // WEAKLY CONFIRMED
	SensorBandwidth          SensorType = 0x22 // WEAKLY CONFIRMED
	SensorConcentration      SensorType = 0x23 // WEAKLY CONFIRMED
	SensorSoundPressureLevel SensorType = 0x24 // WEAKLY CONFIRMED
	SensorSolidAngle         SensorType = 0x25 // WEAKLY CONFIRMED
	SensorLogRatio           SensorType = 0x26 // WEAKLY CONFIRMED
	SensorLogRatioVolts      SensorType = 0x27 // WEAKLY CONFIRMED
	SensorLogRatioWatts      SensorType = 0x28 // WEAKLY CONFIRMED
	SensorOther              SensorType = 0x7F

	SensorManufacturerSpecificMin SensorType = 0x80
	SensorManufacturerSpecificMax SensorType = 0xFE
)

// String renders a human label. Names beyond 0x28 (custom/manufacturer
// range) render generically; use SENSOR_TYPE_CUSTOM (0x0210, not decoded by
// this package — see report §3.4) to resolve a real label for those.
func (t SensorType) String() string {
	names := map[SensorType]string{
		SensorTemperature: "Temperature", SensorVoltage: "Voltage", SensorCurrent: "Current",
		SensorFrequency: "Frequency", SensorResistance: "Resistance", SensorPower: "Power",
		SensorMass: "Mass", SensorLength: "Length", SensorArea: "Area", SensorVolume: "Volume",
		SensorDensity: "Density", SensorVelocity: "Velocity", SensorAcceleration: "Acceleration",
		SensorForce: "Force", SensorEnergy: "Energy", SensorPressure: "Pressure", SensorTime: "Time",
		SensorAngle: "Angle", SensorPositionX: "Position X", SensorPositionY: "Position Y",
		SensorPositionZ: "Position Z", SensorAngularVelocity: "Angular Velocity",
		SensorLuminousIntensity: "Luminous Intensity", SensorLuminousFlux: "Luminous Flux",
		SensorIlluminance: "Illuminance", SensorChrominanceRed: "Chrominance Red",
		SensorChrominanceGreen: "Chrominance Green", SensorChrominanceBlue: "Chrominance Blue",
		SensorContacts: "Contacts", SensorMemory: "Memory", SensorItems: "Items",
		SensorHumidity: "Humidity", SensorCounter16Bit: "16-bit Counter", SensorCPULoad: "CPU Load",
		SensorBandwidth: "Bandwidth", SensorConcentration: "Concentration",
		SensorSoundPressureLevel: "Sound Pressure Level", SensorSolidAngle: "Solid Angle",
		SensorLogRatio: "Log Ratio", SensorLogRatioVolts: "Log Ratio Volts",
		SensorLogRatioWatts: "Log Ratio Watts", SensorOther: "Other",
	}
	if n, ok := names[t]; ok {
		return n
	}
	if t >= SensorManufacturerSpecificMin && t <= SensorManufacturerSpecificMax {
		return "Manufacturer-Specific"
	}
	return "Unknown"
}

// ProductCategory is DEVICE_INFO's product_category field (report §4.1).
// Full table CONFIRMED via two independent primary-derived sources
// (numerically identical). Top byte = family, bottom byte = sub-type.
type ProductCategory uint16

// Product Category values.
const (
	CategoryNotDeclared ProductCategory = 0x0000

	CategoryFixture           ProductCategory = 0x0100
	CategoryFixtureFixed      ProductCategory = 0x0101
	CategoryFixtureMovingYoke ProductCategory = 0x0102
	CategoryFixtureMovingMirr ProductCategory = 0x0103
	CategoryFixtureOther      ProductCategory = 0x01FF

	CategoryFixtureAccessory       ProductCategory = 0x0200
	CategoryFixtureAccessoryColor  ProductCategory = 0x0201
	CategoryFixtureAccessoryYoke   ProductCategory = 0x0202
	CategoryFixtureAccessoryMirror ProductCategory = 0x0203
	CategoryFixtureAccessoryEffect ProductCategory = 0x0204
	CategoryFixtureAccessoryBeam   ProductCategory = 0x0205
	CategoryFixtureAccessoryOther  ProductCategory = 0x02FF

	CategoryProjector           ProductCategory = 0x0300
	CategoryProjectorFixed      ProductCategory = 0x0301
	CategoryProjectorMovingYoke ProductCategory = 0x0302
	CategoryProjectorMovingMirr ProductCategory = 0x0303
	CategoryProjectorOther      ProductCategory = 0x03FF

	CategoryAtmospheric      ProductCategory = 0x0400
	CategoryAtmosphericEfx   ProductCategory = 0x0401
	CategoryAtmosphericPyro  ProductCategory = 0x0402
	CategoryAtmosphericOther ProductCategory = 0x04FF

	CategoryDimmer           ProductCategory = 0x0500
	CategoryDimmerACIncand   ProductCategory = 0x0501
	CategoryDimmerACFluor    ProductCategory = 0x0502
	CategoryDimmerACColdCath ProductCategory = 0x0503
	CategoryDimmerACNonDim   ProductCategory = 0x0504
	CategoryDimmerACElv      ProductCategory = 0x0505
	CategoryDimmerACOther    ProductCategory = 0x0506
	CategoryDimmerDCLevel    ProductCategory = 0x0507
	CategoryDimmerDCPWM      ProductCategory = 0x0508
	CategoryDimmerCSLED      ProductCategory = 0x0509
	CategoryDimmerOther      ProductCategory = 0x05FF

	CategoryPower        ProductCategory = 0x0600
	CategoryPowerControl ProductCategory = 0x0601
	CategoryPowerSource  ProductCategory = 0x0602
	CategoryPowerOther   ProductCategory = 0x06FF

	CategoryScenic      ProductCategory = 0x0700
	CategoryScenicDrive ProductCategory = 0x0701
	CategoryScenicOther ProductCategory = 0x07FF

	CategoryData             ProductCategory = 0x0800
	CategoryDataDistribution ProductCategory = 0x0801 // gateways/nodes/splitters
	CategoryDataConversion   ProductCategory = 0x0802
	CategoryDataOther        ProductCategory = 0x08FF

	CategoryAV      ProductCategory = 0x0900
	CategoryAVAudio ProductCategory = 0x0901
	CategoryAVVideo ProductCategory = 0x0902
	CategoryAVOther ProductCategory = 0x09FF

	CategoryMonitor              ProductCategory = 0x0A00
	CategoryMonitorACLinePower   ProductCategory = 0x0A01
	CategoryMonitorDCPower       ProductCategory = 0x0A02
	CategoryMonitorEnvironmental ProductCategory = 0x0A03
	CategoryMonitorOther         ProductCategory = 0x0AFF

	CategoryControl             ProductCategory = 0x7000
	CategoryControlController   ProductCategory = 0x7001
	CategoryControlBackupDevice ProductCategory = 0x7002
	CategoryControlOther        ProductCategory = 0x70FF

	CategoryTest               ProductCategory = 0x7100
	CategoryTestEquipment      ProductCategory = 0x7101
	CategoryTestEquipmentOther ProductCategory = 0x71FF

	CategoryOther ProductCategory = 0x7FFF
)

// Family returns the top-byte family code (e.g. CategoryFixture for any
// 0x01xx value), the level DeviceClass classification (see package
// registry) is built from.
func (c ProductCategory) Family() ProductCategory {
	return ProductCategory(uint16(c) & 0xFF00)
}

// ProductDetail is PRODUCT_DETAIL_ID_LIST's per-entry enum (report §4.2).
// Only the values the report names individually are modeled; ranges the
// report describes only in prose (e.g. "0x0001-0x0009 lamp types") are not
// given per-value constants since the report itself doesn't enumerate them
// numerically.
type ProductDetail uint16

// Product Detail values (named subset, CONFIRMED both sources).
const (
	DetailNotDeclared ProductDetail = 0x0000

	DetailSplitter            ProductDetail = 0x0600
	DetailEthernetNode        ProductDetail = 0x0601
	DetailMerge               ProductDetail = 0x0602
	DetailDataPatch           ProductDetail = 0x0603
	DetailWirelessLink        ProductDetail = 0x0604
	DetailProtocolConverter   ProductDetail = 0x0701
	DetailAnalogDemultiplex   ProductDetail = 0x0702
	DetailAnalogMultiplex     ProductDetail = 0x0703
	DetailSwitchPanel         ProductDetail = 0x0704
	DetailRouter              ProductDetail = 0x0800
	DetailFader               ProductDetail = 0x0801
	DetailMixer               ProductDetail = 0x0802
	DetailChangeoverManual    ProductDetail = 0x0900
	DetailChangeoverAuto      ProductDetail = 0x0901
	DetailTest                ProductDetail = 0x0902
	DetailGFIRCD              ProductDetail = 0x0A00
	DetailBattery             ProductDetail = 0x0A01
	DetailControllableBreaker ProductDetail = 0x0A02
	DetailOther               ProductDetail = 0x7FFF
)

// IsInfrastructure reports whether d is one of the "this is infrastructure,
// not a light" signals called out in report §1.3/§4.2 (splitter, gateway,
// datapatch, wireless link) — used as the PRODUCT_DETAIL_ID_LIST fallback
// signal when product_category itself is left at NOT_DECLARED.
func (d ProductDetail) IsInfrastructure() bool {
	switch d {
	case DetailSplitter, DetailEthernetNode, DetailDataPatch, DetailWirelessLink,
		DetailMerge, DetailProtocolConverter, DetailRouter:
		return true
	default:
		return false
	}
}
