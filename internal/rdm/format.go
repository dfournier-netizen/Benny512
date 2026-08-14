package rdm

import "strconv"

// FormatValue renders raw (already sign-interpreted, e.g. via
// ParameterDescription.MinValue or a plain sensor present_value) scaled by
// prefix and suffixed with unit's label — report §1.2/§5.3: "apply the
// prefix as a power-of-ten multiplier to the raw value before display",
// e.g. raw=235, unit=Volts, prefix=Deci -> "23.5 V".
//
// FormatValue does not special-case the sensor undefined-range sentinels
// (-32768/32767) — callers rendering a SensorDefinition's range/normal
// fields should check HasRange/HasNormalBand (or use FormatSensorRange)
// first, since those sentinels are legitimate finite numbers everywhere
// else (e.g. a PARAMETER_DESCRIPTION min/max that happens to be exactly
// -32768).
func FormatValue(raw int64, unit Unit, prefix Prefix) string {
	scaled := float64(raw) * prefix.Multiplier()
	s := strconv.FormatFloat(scaled, 'f', -1, 64)
	suffix := unit.Suffix()
	if suffix == "" {
		return s
	}
	return s + " " + suffix
}

// FormatSensorRangeValue renders one bound of a SensorDefinition's
// range/normal band, returning "undefined" for the sentinel values
// (report §1.2/§3.1) instead of a literal -32768/32767.
func FormatSensorRangeValue(v int16, unit Unit, prefix Prefix) string {
	if isUndefinedSensorValue(v) {
		return "undefined"
	}
	return FormatValue(int64(v), unit, prefix)
}
