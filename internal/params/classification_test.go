package params

import (
	"testing"

	"benny512/internal/rdm"
)

// TestUnknownPIDDefaultsToStandard pins down classification.go's core
// safety rule: a PID Tier has never heard of falls back to TierStandard,
// never TierHidden. Hiding by default would silently swallow real,
// spec-defined data from a future fixture this table hasn't been taught
// about yet — see the file doc comment. Picks an arbitrary standard PID
// (REAL_TIME_CLOCK, 0x0603) that this file deliberately gives no pidTiers
// entry, plus an arbitrary manufacturer-specific PID, to prove the rule
// holds for both "forgot to classify" and "can never be classified by
// number alone" cases.
func TestUnknownPIDDefaultsToStandard(t *testing.T) {
	if _, explicit := pidTiers[rdm.PIDRealTimeClock]; explicit {
		t.Fatalf("test PID rdm.PIDRealTimeClock has a pidTiers entry; pick a different PID this file truly has no opinion on")
	}
	if got := Tier(rdm.PIDRealTimeClock); got != TierStandard {
		t.Errorf("Tier(PIDRealTimeClock) = %v, want TierStandard (no entry -> default)", got)
	}

	mfrPID := rdm.ParameterID(0x8123) // arbitrary manufacturer-specific value
	if !mfrPID.IsManufacturerSpecific() {
		t.Fatalf("test setup: 0x8123 is not manufacturer-specific")
	}
	if got := Tier(mfrPID); got != TierStandard {
		t.Errorf("Tier(0x8123) = %v, want TierStandard (manufacturer-specific always defaults Standard)", got)
	}

	// A totally made-up standard-range PID nobody has assigned meaning to.
	if got := Tier(rdm.ParameterID(0x0999)); got != TierStandard {
		t.Errorf("Tier(0x0999) = %v, want TierStandard", got)
	}
}

// TestPromotedTiersMatchBrief spot-checks every PID the Phase D task 1
// brief names as owner-approved TierPromoted.
func TestPromotedTiersMatchBrief(t *testing.T) {
	promoted := []rdm.ParameterID{
		rdm.PIDDeviceLabel, rdm.PIDDMXStartAddress, rdm.PIDDMXPersonality, rdm.PIDIdentifyDevice,
		rdm.PIDDeviceHours, rdm.PIDLampHours, rdm.PIDLampStrikes, rdm.PIDLampState, rdm.PIDDevicePowerCycles,
		rdm.PIDCurve, rdm.PIDMinimumLevel, rdm.PIDMaximumLevel,
		rdm.PIDResetDevice, rdm.PIDFactoryDefaults,
		rdm.PIDPanInvert, rdm.PIDTiltInvert, rdm.PIDPanTiltSwap,
		rdm.PIDDisplayInvert, rdm.PIDDisplayLevel,
	}
	for _, pid := range promoted {
		if got := Tier(pid); got != TierPromoted {
			t.Errorf("Tier(0x%04X) = %v, want TierPromoted", uint16(pid), got)
		}
	}
}

// TestHiddenTiersMatchBrief spot-checks every PID family the brief names as
// TierHidden, plus the specific proxy-family wrinkle (Task 1's "hide them
// as rows again" ask).
func TestHiddenTiersMatchBrief(t *testing.T) {
	hidden := []rdm.ParameterID{
		rdm.PIDDiscUniqueBranch, rdm.PIDDiscMute, rdm.PIDDiscUnMute,
		rdm.PIDProxiedDevices, rdm.PIDProxiedDeviceCount, rdm.PIDCommsStatus,
		rdm.PIDQueuedMessage,
		rdm.PIDStatusMessages, rdm.PIDStatusIDDescription, rdm.PIDClearStatusID, rdm.PIDSubDeviceStatusReportThreshold,
		rdm.PIDSupportedParameters, rdm.PIDParameterDescription,
		rdm.PIDSupportedParametersEnhanced, rdm.PIDControllerFlagSupport, rdm.PIDNackDescription,
		rdm.PIDPackedPIDSub, rdm.PIDPackedPIDIndex, rdm.PIDEnumLabel,
		rdm.PIDDeviceInfo, rdm.PIDProductDetailIDList,
		rdm.PIDSensorDefinition, rdm.PIDRecordSensors,
		rdm.PIDListInterfaces, rdm.PIDDNSDomainName,
		rdm.PIDComponentScope, rdm.PIDBrokerStatus,
		rdm.PIDEndpointList, rdm.PIDBackgroundQueuedStatusPolicyDescription,
	}
	for _, pid := range hidden {
		if got := Tier(pid); got != TierHidden {
			t.Errorf("Tier(0x%04X) = %v, want TierHidden", uint16(pid), got)
		}
	}
}

func TestPIDTierString(t *testing.T) {
	cases := map[PIDTier]string{TierStandard: "standard", TierPromoted: "promoted", TierHidden: "hidden"}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("PIDTier(%d).String() = %q, want %q", tier, got, want)
		}
	}
}
