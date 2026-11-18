package web

import (
	"encoding/json"
	"strings"
	"testing"

	"benny512/internal/registry"
)

// TestG3b4FixtureJSONCarriesSoftwareVersionAndPersonality: GET
// /api/fixtures carries DEVICE_INFO's software version ID and current
// personality (meaningful once hasDeviceInfo is true; no omitempty — 0 is
// a value to be read beside hasDeviceInfo, never dropped), for the device
// cards' GDTF tags.
func TestG3b4FixtureJSONCarriesSoftwareVersionAndPersonality(t *testing.T) {
	b, err := json.Marshal(toFixtureJSON(registry.Fixture{HasDeviceInfo: true, DeviceModelID: 1, SoftwareVersionID: 0x01000000, CurrentPersonality: 2}, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"softwareVersionId":16777216`, `"currentPersonality":2`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("fixture JSON lacks %s: %s", want, b)
		}
	}
	b, _ = json.Marshal(toFixtureJSON(registry.Fixture{}, "", 0))
	if !strings.Contains(string(b), `"softwareVersionId":0`) || !strings.Contains(string(b), `"currentPersonality":0`) || !strings.Contains(string(b), `"hasDeviceInfo":false`) {
		t.Fatalf("an unread fixture must still carry both (read beside hasDeviceInfo): %s", b)
	}
}
