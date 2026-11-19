package registry

import (
	"testing"

	"benny512/internal/rdm"
)

// TestReclassifyKeepsSoftwareVersionAndPersonality (G3b4): DEVICE_INFO's
// software version ID and current personality are cached on the fixture,
// so every Devices card can ask the GDTF library for its match (manufacturer,
// model, software version, personality) without a second RDM round-trip.
// E1.20 Table A-3 / §10.5.1 DEVICE_INFO layout: protocol (2), model (2),
// category (2), software version ID (4), footprint (2), current personality
// (1), personality count (1), start address (2), sub-devices (2), sensors (1).
func TestReclassifyKeepsSoftwareVersionAndPersonality(t *testing.T) {
	data := []byte{0x01, 0x00, 0x00, 0x2A, 0x01, 0x01, 0x02, 0x01, 0x00, 0x03, 0x00, 0x31, 0x03, 0x06, 0x00, 0x65, 0x00, 0x00, 0x00}
	f := &Fixture{}
	reclassify(f, rdm.PIDDeviceInfo, data)
	if !f.HasDeviceInfo || f.DeviceModelID != 0x2A || f.SoftwareVersionID != 0x02010003 || f.CurrentPersonality != 3 || f.DMXFootprint != 49 {
		t.Fatalf("fixture after DEVICE_INFO = model %#x sw %#x personality %d footprint %d (has %v), want 0x2a 0x2010003 3 49",
			f.DeviceModelID, f.SoftwareVersionID, f.CurrentPersonality, f.DMXFootprint, f.HasDeviceInfo)
	}
}
