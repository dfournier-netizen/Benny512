package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
)

// TestEffectiveManufacturerAndModel unit-tests the Devices screen's
// Manufacturer/Model column priority chains directly against
// registry.Fixture values, with no RDM traffic involved — task ask: prefer
// the device's own report (MANUFACTURER_LABEL / DEVICE_MODEL_DESCRIPTION),
// fall back to the ESTA table / numeric Device Model ID hex, and never
// return "".
func TestEffectiveManufacturerAndModel(t *testing.T) {
	cases := []struct {
		name string
		f    registry.Fixture
		want string
		fn   func(registry.Fixture) string
	}{
		{
			name: "manufacturer label preferred over ESTA table",
			f:    registry.Fixture{ManufacturerLabel: "Chroma-Q", ManufacturerName: "Unknown (0x5370)"},
			want: "Chroma-Q",
			fn:   effectiveManufacturer,
		},
		{
			name: "manufacturer falls back to ESTA table when label unknown",
			f:    registry.Fixture{ManufacturerLabel: "", ManufacturerName: "ADJ Products LLC"},
			want: "ADJ Products LLC",
			fn:   effectiveManufacturer,
		},
		{
			name: "manufacturer falls back to Unknown(0xXXXX) when both unknown",
			f:    registry.Fixture{ManufacturerLabel: "", ManufacturerName: "Unknown (0xBEEF)"},
			want: "Unknown (0xBEEF)",
			fn:   effectiveManufacturer,
		},
		{
			name: "model description preferred over numeric fallback",
			f:    registry.Fixture{ModelDescription: "Color Force II 48", HasDeviceInfo: true, DeviceModelID: 7},
			want: "Color Force II 48",
			fn:   effectiveModel,
		},
		{
			name: "model falls back to numeric Device Model ID hex when NACKed",
			f:    registry.Fixture{ModelDescription: "", HasDeviceInfo: true, DeviceModelID: 1},
			want: "0x0001",
			fn:   effectiveModel,
		},
		{
			name: "model falls back to em-dash when neither is known",
			f:    registry.Fixture{ModelDescription: "", HasDeviceInfo: false},
			want: "—",
			fn:   effectiveModel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.f); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFixtureJSONManufacturerModelFields confirms toFixtureJSON's new
// fields round-trip through JSON with the lowerCamelCase tags the task
// requires, and that the raw label/description fields stay distinct from
// the resolved manufacturer/model fields.
func TestFixtureJSONManufacturerModelFields(t *testing.T) {
	f := registry.Fixture{
		ManufacturerLabel: "LumenRadio", ManufacturerLabelKnown: true, ManufacturerName: "LumenRadio AB",
		ModelDescription: "Aurora", ModelDescriptionKnown: true, HasDeviceInfo: true, DeviceModelID: 3,
	}
	b, err := json.Marshal(toFixtureJSON(f))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"manufacturer": "LumenRadio", "model": "Aurora",
		"manufacturerLabel": "LumenRadio", "manufacturerLabelKnown": true,
		"modelDescription": "Aurora", "modelDescriptionKnown": true,
		"deviceModelId": float64(3), "hasDeviceInfo": true,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("field %q = %v (%T), want %v (%T)", k, m[k], m[k], v, v)
		}
	}
}

// TestDevicesBackfillPopulatesManufacturerAndModel drives the same RDM
// round-trips the Devices screen's background backfill issues (GET
// MANUFACTURER_LABEL / GET DEVICE_MODEL_DESCRIPTION / GET DEVICE_INFO
// through /api/device/{uid}/param/*) against two scripted devices — one
// that ACKs both label PIDs, one that NACKs both — and confirms /api/
// fixtures reflects the priority-resolved values plus the *Known flags in
// each case, including the DEVICE_MODEL_DESCRIPTION NACK -> numeric
// Device Model ID hex fallback the task specifically calls out.
func TestDevicesBackfillPopulatesManufacturerAndModel(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	port := mustPort(t)
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: port}

	// reportingUID: a device whose own report differs from (and should
	// win over) both the ESTA-table lookup and the numeric fallback.
	reportingUID := rdm.UID{ManufacturerID: 0x1900, DeviceID: 1} // ESTA 0x1900 -> "ADJ Products LLC"
	// nackingUID: a device that NACKs both MANUFACTURER_LABEL and
	// DEVICE_MODEL_DESCRIPTION (the task's required fallback-path demo).
	nackingUID := rdm.UID{ManufacturerID: 0xAAAA, DeviceID: 2} // ESTA 0xAAAA -> "Ayrton"

	h.srv.Registry.NoteFixture(ref, reportingUID)
	h.srv.Registry.NoteFixture(ref, nackingUID)

	deviceInfoBytes := params.EncodeDeviceInfo(params.DeviceInfo{ProtocolVersionMajor: 1, DeviceModelID: 0x0042})

	h.tport.OnSend = func(sp session.SentPacket) {
		if sp.DecodeErr != nil {
			return
		}
		msg, err := sp.Packet.Rdm.DecodedRDMMessage()
		if err != nil {
			return
		}
		var uid rdm.UID
		var data []byte
		nack := false
		switch msg.DestinationUID {
		case reportingUID:
			uid = reportingUID
			switch msg.ParameterID {
			case rdm.PIDManufacturerLabel:
				data = []byte("Obsidian Control Systems")
			case rdm.PIDDeviceModelDescription:
				data = []byte("Netron EN4")
			case rdm.PIDDeviceInfo:
				data = deviceInfoBytes
			default:
				nack = true
			}
		case nackingUID:
			uid = nackingUID
			switch msg.ParameterID {
			case rdm.PIDManufacturerLabel, rdm.PIDDeviceModelDescription:
				nack = true
			case rdm.PIDDeviceInfo:
				data = deviceInfoBytes
			default:
				nack = true
			}
		default:
			return
		}
		respClass := rdm.GetCommandResponse
		var resp rdm.Message
		if nack {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseNackReason), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID,
				ParameterData: []byte{byte(rdm.NackUnknownPID >> 8), byte(rdm.NackUnknownPID)},
			}
		} else {
			resp = rdm.Message{
				DestinationUID: msg.SourceUID, SourceUID: uid, TransactionNumber: msg.TransactionNumber,
				PortIDOrResponseType: byte(rdm.ResponseACK), SubDevice: msg.SubDevice,
				CommandClass: respClass, ParameterID: msg.ParameterID, ParameterData: data,
			}
		}
		h.clock.AfterFunc(0, func() { h.rdmc.HandleRDMResponse(resp) })
	}

	for _, uidStr := range []string{reportingUID.String(), nackingUID.String()} {
		for _, pid := range []string{"0081", "0080"} {
			rr := h.runHTTPAsync(t, "GET", "/api/device/"+uidStr+"/param/"+pid, nil)
			if rr.Code != http.StatusOK && rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("uid=%s pid=%s status=%d body=%s", uidStr, pid, rr.Code, rr.Body.String())
			}
		}
	}
	// Only the NACKing device needs DEVICE_INFO fetched to exercise the
	// numeric-hex fallback; fetch it for both so the assertions below are
	// symmetric.
	for _, uidStr := range []string{reportingUID.String(), nackingUID.String()} {
		rr := h.runHTTPAsync(t, "GET", "/api/fixture/"+uidStr+"/param/device_info", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("uid=%s device_info status=%d body=%s", uidStr, rr.Code, rr.Body.String())
		}
	}

	rr := doJSON(t, h.srv.Handler(), "GET", "/api/fixtures", nil)
	var out []fixtureJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, rr.Body.String())
	}
	byUID := map[string]fixtureJSON{}
	for _, f := range out {
		byUID[f.UID] = f
	}

	rep, ok := byUID[reportingUID.String()]
	if !ok {
		t.Fatalf("reporting device missing from /api/fixtures: %+v", out)
	}
	if rep.Manufacturer != "Obsidian Control Systems" {
		t.Errorf("reporting device manufacturer = %q, want device's own report", rep.Manufacturer)
	}
	if rep.Model != "Netron EN4" {
		t.Errorf("reporting device model = %q, want device's own report", rep.Model)
	}
	if !rep.ManufacturerLabelKnown || !rep.ModelDescriptionKnown {
		t.Errorf("reporting device Known flags = %+v, want both true", rep)
	}

	nacked, ok := byUID[nackingUID.String()]
	if !ok {
		t.Fatalf("nacking device missing from /api/fixtures: %+v", out)
	}
	if nacked.Manufacturer != "Ayrton" {
		t.Errorf("nacking device manufacturer = %q, want ESTA-table fallback %q", nacked.Manufacturer, "Ayrton")
	}
	if nacked.Model != "0x0042" {
		t.Errorf("nacking device model = %q, want numeric Device Model ID hex fallback", nacked.Model)
	}
	if !nacked.ManufacturerLabelKnown || !nacked.ModelDescriptionKnown {
		t.Errorf("nacking device Known flags = %+v, want both true (NACK still marks attempted)", nacked)
	}
	if nacked.ManufacturerLabel != "" || nacked.ModelDescription != "" {
		t.Errorf("nacking device raw fields should stay empty: %+v", nacked)
	}
}

// TestFixtureJSONStatesUnreachabilityInPlainLanguage covers the round-4
// visibility requirement: when Benny512 stops asking a device, the UI must be
// able to say so in words a lighting tech can act on, rather than showing a
// row that quietly stops filling in.
func TestFixtureJSONStatesUnreachabilityInPlainLanguage(t *testing.T) {
	retry := time.Date(2026, 8, 25, 21, 17, 4, 0, time.UTC)
	f := registry.Fixture{
		ManufacturerName: "LumenRadio AB",
		ProxyUnreachable: true, ProxyRetryAt: retry, ProxyRefusals: 3,
	}
	out := toFixtureJSON(f)
	if !out.Unreachable {
		t.Fatal("unreachable = false for a fixture whose breaker is open")
	}
	if out.RetryAt == nil || !out.RetryAt.Equal(retry) {
		t.Errorf("retryAt = %v, want %v", out.RetryAt, retry)
	}
	note := out.UnreachableNote
	if note == "" {
		t.Fatal("unreachableNote is empty; a missing row with no explanation reads as a Benny512 fault")
	}
	// The wording rules, asserted rather than left to drift: name the thing a
	// tech can act on, and say it is not permanent. No protocol jargon —
	// the NACK is in the RDM log for whoever wants it.
	for _, want := range []string{"wireless proxy", "try again"} {
		if !strings.Contains(note, want) {
			t.Errorf("unreachableNote = %q, want it to mention %q", note, want)
		}
	}
	for _, unwanted := range []string{"NACK", "PROXY_BUFFER_FULL", "0x000A", "breaker"} {
		if strings.Contains(note, unwanted) {
			t.Errorf("unreachableNote = %q leaks protocol jargon %q", note, unwanted)
		}
	}

	// A healthy fixture carries neither the flag, the note, nor a retry time.
	healthy := toFixtureJSON(registry.Fixture{ManufacturerName: "LumenRadio AB"})
	if healthy.Unreachable || healthy.UnreachableNote != "" || healthy.RetryAt != nil {
		t.Errorf("healthy fixture = %+v, want no unreachability at all", healthy)
	}
	b, err := json.Marshal(healthy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "unreachableNote") || strings.Contains(string(b), "retryAt") {
		t.Errorf("healthy fixture JSON = %s, want the optional unreachability fields omitted", b)
	}
}
