package web

import (
	"encoding/json"
	"strings"
	"testing"

	"benny512/internal/autoread"
	"benny512/internal/registry"
)

// This file is the Go<->JS seam for the server-side automatic identity read.
// It reads the LITERAL devices.js the browser loads, the way sacn_ui_test.go
// and universe_scheme_test.go do, because the defect class it guards is the
// one neither half can see alone: both sides internally consistent, and
// disagreeing about a string.

// TestDevicesScreenHasNoBrowserProbeSweep is the decision about
// classifyUnknown(), pinned.
//
// It was removed, not kept. Its job — issuing the per-device identity GETs —
// is now the server's (internal/autoread), and two paths asking the same
// device the same six PIDs would double the traffic on a shared half-duplex
// RDM bus, which is the exact regression this change exists to avoid. It also
// could not do the job: it only ever probed rows the Devices screen happened
// to be rendering, so a Table of Devices arriving after discovery had
// finished (RDM-LOG30, 2.11.90.2 Port-Address 31: three empty ToDs inside
// 240 ms, then 11 and 14 UIDs 11.5 s later) left those devices null until an
// operator opened Inspect; and it re-ran on every refresh, which is how one
// session accumulated 173 DEVICE_INFO requests and spent 48-63 apiece on four
// fixtures that never answered.
func TestDevicesScreenHasNoBrowserProbeSweep(t *testing.T) {
	js := readJS(t, "devices.js")
	for _, banned := range []string{
		"function classifyUnknown",
		"classifying[",
	} {
		if strings.Contains(js, banned) {
			t.Errorf("devices.js still contains %q — the browser-side probe sweep is "+
				"gone, and a second path reading the same PIDs as internal/autoread "+
				"would double the traffic on the RDM line", banned)
		}
	}
	// The sweep's own request shapes must be gone with it. These are the PIDs
	// it fired per Unknown device; the server's core-identity pass issues the
	// same set once, gated.
	for _, banned := range []string{
		`Api.getDeviceParam(f.uid, '0070')`,
		`Api.getDeviceParam(f.uid, '0011')`,
		`Api.getDeviceParam(f.uid, '0081')`,
		`Api.getDeviceParam(f.uid, '0080')`,
		`Api.getParam(f.uid, 'device_info')`,
	} {
		if strings.Contains(js, banned) {
			t.Errorf("devices.js still issues %s from the list screen", banned)
		}
	}
}

// TestDevicesScreenReadsTheServersReadState pins the vocabulary. The Go
// constants and the JS string literals are two independent copies of the same
// words; this is the only place that can catch them drifting.
func TestDevicesScreenReadsTheServersReadState(t *testing.T) {
	js := readJS(t, "devices.js")
	for _, state := range []autoread.State{
		autoread.StatePending, autoread.StateReading, autoread.StateRead, autoread.StateGaveUp,
	} {
		if state == autoread.StateRead {
			// "read" is a substring of "reading"; assert the quoted literal.
			continue
		}
		if !strings.Contains(js, "'"+string(state)+"'") {
			t.Errorf("devices.js never mentions the reader state %q that the server "+
				"puts on every device row", state)
		}
	}
	if !strings.Contains(js, "f.readState") {
		t.Error("devices.js does not read f.readState — the Devices screen would have " +
			"no way to tell 'being read now' from 'we gave up', and would print the " +
			"same placeholder for both")
	}
	// gaveUp must produce a sentence of its own. A device the reader stopped
	// asking is a stated condition, not a row that quietly stops filling in
	// (the standing rule behind unreachableNote).
	if !strings.Contains(js, "no answer — address not read") {
		t.Error("devices.js has no distinct wording for a device the reader gave up on")
	}
}

// TestFixtureJSONCarriesTheFieldsTheListNowNeeds checks the wire names the
// browser hard-codes against the ones the Go struct actually marshals. The
// Devices list stopped fetching DEVICE_INFO for itself, so every one of these
// is now load-bearing: if a tag is renamed, the Address column silently goes
// back to "address not read yet" for every device on the rig, with no error
// anywhere.
func TestFixtureJSONCarriesTheFieldsTheListNowNeeds(t *testing.T) {
	b, err := json.Marshal(toFixtureJSON(registry.Fixture{
		HasDeviceInfo: true, DMXStartAddress: 295, DMXFootprint: 42,
	}, autoread.StateGaveUp, 2))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	js := readJS(t, "devices.js")
	for _, field := range []string{"dmxStartAddress", "dmxFootprint", "hasDeviceInfo", "readState"} {
		if _, ok := got[field]; !ok {
			t.Errorf("fixture JSON has no %q key; devices.js reads f.%s", field, field)
		}
		if !strings.Contains(js, "f."+field) {
			t.Errorf("devices.js never reads f.%s", field)
		}
	}
	// The numbers themselves, from RDM-LOG30's 4D50:001158EA DEVICE_INFO at
	// 14:03:34.340 (footprint=42, startAddr=295).
	if got["dmxStartAddress"] != float64(295) || got["dmxFootprint"] != float64(42) {
		t.Errorf("addressing pair marshalled as %v/%v, want 295/42",
			got["dmxStartAddress"], got["dmxFootprint"])
	}
	if got["readState"] != string(autoread.StateGaveUp) || got["readAttempts"] != float64(2) {
		t.Errorf("reader state marshalled as %v/%v, want %q/2",
			got["readState"], got["readAttempts"], autoread.StateGaveUp)
	}
}

// TestFixtureJSONOmitsReaderStateWhenUntracked keeps the honest-zero rule:
// a device the reader has never heard of must not marshal as though it were
// in some state, and its footprint-0 must not be mistaken for a reading —
// hasDeviceInfo is what says whether the pair means anything.
func TestFixtureJSONOmitsReaderStateWhenUntracked(t *testing.T) {
	b, _ := json.Marshal(toFixtureJSON(registry.Fixture{}, autoread.StateUnknown, 0))
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["readState"]; ok {
		t.Errorf("readState present (%v) for a device the reader has never seen", got["readState"])
	}
	if got["hasDeviceInfo"] != false {
		t.Errorf("hasDeviceInfo = %v for an unread device, want false", got["hasDeviceInfo"])
	}
	if _, ok := got["dmxStartAddress"]; !ok {
		t.Error("dmxStartAddress vanished from the JSON; it must always round-trip, " +
			"with hasDeviceInfo saying whether it means anything (the Entry.Universe " +
			"defect class)")
	}
}
