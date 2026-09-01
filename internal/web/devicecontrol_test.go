package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"benny512/internal/rdm"
	"benny512/internal/session"
)

// wireDeviceControlDevice scripts a fake responder supporting the full
// E1.20 §10.11 device-control PID set (mirrors wireServiceLifeDevice's
// pattern in device_test.go) — power state Normal, no self test active,
// two SELFTEST_ENHANCED-declared tests with SELF_TEST_DESCRIPTION labels,
// PRESET_PLAYBACK Off/full, CAPTURE_PRESET always ACKing a well-formed SET.
func (h *testHarness) wireDeviceControlDevice(uid rdm.UID) {
	supported := rdm.EncodeSupportedParameters([]rdm.ParameterID{
		rdm.PIDPowerState, rdm.PIDPerformSelfTest, rdm.PIDSelfTestDescription,
		rdm.PIDSelfTestEnhanced, rdm.PIDCapturePreset, rdm.PIDPresetPlayback,
	})
	values := map[rdm.ParameterID][]byte{
		rdm.PIDPowerState:     rdm.EncodePowerState(rdm.PowerStateNormal),
		rdm.PIDPresetPlayback: rdm.EncodePresetPlayback(rdm.PresetPlayback{Mode: rdm.PresetPlaybackOff, Level: 0xFF}),
	}
	selfTestActive := false
	descs := map[byte]string{1: "Lamp Check", 2: "Pan/Tilt Sweep"}

	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		switch msg.ParameterID {
		case rdm.PIDSupportedParameters:
			return supported, false, 0, 0
		case rdm.PIDPerformSelfTest:
			if msg.CommandClass == rdm.SetCommand {
				selfTestActive = len(msg.ParameterData) == 1 && msg.ParameterData[0] != 0
				return nil, false, 0, 0
			}
			active := byte(0)
			if selfTestActive {
				active = 1
			}
			return []byte{active}, false, 0, 0
		case rdm.PIDSelfTestDescription:
			if len(msg.ParameterData) != 1 {
				return nil, true, rdm.NackFormatError, 0
			}
			label, ok := descs[msg.ParameterData[0]]
			if !ok {
				return nil, true, rdm.NackDataOutOfRange, 0
			}
			return append([]byte{msg.ParameterData[0]}, []byte(label)...), false, 0, 0
		case rdm.PIDSelfTestEnhanced:
			out := []byte{0x00, 0x00}
			for n := byte(1); n <= 2; n++ {
				status := rdm.SelfTestStatusNotRun
				if selfTestActive {
					status = rdm.SelfTestStatusActive
				}
				out = append(out, n, byte(status), 0x00, 0x01, 0x00, 0x00)
			}
			return out, false, 0, 0
		case rdm.PIDCapturePreset:
			if msg.CommandClass == rdm.GetCommand {
				return nil, true, rdm.NackUnsupportedCommandClass, 0
			}
			if len(msg.ParameterData) != 2 && len(msg.ParameterData) != 8 {
				return nil, true, rdm.NackFormatError, 0
			}
			return nil, false, 0, 0
		default:
			v, ok := values[msg.ParameterID]
			if !ok {
				return nil, true, rdm.NackUnknownPID, 0
			}
			if msg.CommandClass == rdm.SetCommand {
				values[msg.ParameterID] = append([]byte(nil), msg.ParameterData...)
				return nil, false, 0, 0
			}
			return v, false, 0, 0
		}
	})
}

func TestGetDeviceControl(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x20}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceControlDevice(uid)

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/device-control", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got deviceControlJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.PowerState.Known || got.PowerState.Value != int64(rdm.PowerStateNormal) || got.PowerState.Label != "Normal" {
		t.Errorf("PowerState = %+v, want known/0xFF/Normal", got.PowerState)
	}
	if !got.SelfTestActive.Known || got.SelfTestActive.Value != 0 {
		t.Errorf("SelfTestActive = %+v, want known/0", got.SelfTestActive)
	}
	if !got.PresetPlayback.Known || got.PresetPlayback.Mode != 0 || got.PresetPlayback.Level != 0xFF || got.PresetPlayback.Label != "Off" {
		t.Errorf("PresetPlayback = %+v, want known/mode 0/level 255/Off", got.PresetPlayback)
	}
	if !got.CapturePresetSupported.Known || !got.CapturePresetSupported.Supported {
		t.Errorf("CapturePresetSupported = %+v, want known/supported", got.CapturePresetSupported)
	}
	if !got.SelfTestsKnown {
		t.Fatal("SelfTestsKnown = false, want true (SELFTEST_ENHANCED advertised and answered)")
	}
	if len(got.SelfTests) != 2 {
		t.Fatalf("len(SelfTests) = %d, want 2: %+v", len(got.SelfTests), got.SelfTests)
	}
	if got.SelfTests[0].Number != 1 || got.SelfTests[0].Description != "Lamp Check" {
		t.Errorf("SelfTests[0] = %+v, want number 1 / \"Lamp Check\"", got.SelfTests[0])
	}
	if got.SelfTests[1].Number != 2 || got.SelfTests[1].Description != "Pan/Tilt Sweep" {
		t.Errorf("SelfTests[1] = %+v, want number 2 / \"Pan/Tilt Sweep\"", got.SelfTests[1])
	}
	if !got.SelfTests[0].AutoTerminate {
		t.Errorf("SelfTests[0].AutoTerminate = false, want true (capability bit 0x0001 set)")
	}
}

// TestGetDeviceControlDegradesForUnsupportedDevice proves GET
// /device-control returns 200 with every field Known:false / an empty
// self-test roster — never an error — for a device that advertises none of
// §10.11's PIDs (mirrors TestGetServiceLifeDegradesForUnsupportedDevice;
// this is wash2's exact shape in --demo mode).
func TestGetDeviceControlDegradesForUnsupportedDevice(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x21}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDSupportedParameters {
			return rdm.EncodeSupportedParameters([]rdm.ParameterID{rdm.PIDDeviceLabel}), false, 0, 0
		}
		return nil, true, rdm.NackUnknownPID, 0
	})

	rr := h.runHTTPAsync(t, "GET", "/api/device/"+uid.String()+"/device-control", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got deviceControlJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PowerState.Known {
		t.Errorf("PowerState.Known = true, want false")
	}
	if got.SelfTestActive.Known {
		t.Errorf("SelfTestActive.Known = true, want false")
	}
	if got.PresetPlayback.Known {
		t.Errorf("PresetPlayback.Known = true, want false")
	}
	if got.CapturePresetSupported.Known && got.CapturePresetSupported.Supported {
		t.Errorf("CapturePresetSupported = %+v, want not both known+supported", got.CapturePresetSupported)
	}
	if got.SelfTestsKnown {
		t.Errorf("SelfTestsKnown = true, want false (SELFTEST_ENHANCED not advertised)")
	}
	if got.SelfTests == nil {
		t.Fatal("SelfTests is nil, want an empty non-nil slice (never null on the wire)")
	}
	if len(got.SelfTests) != 0 {
		t.Fatalf("len(SelfTests) = %d, want 0", len(got.SelfTests))
	}
}

// TestSetPowerStateRequiresConfirmation guards the {"confirm":"CONFIRM"}
// tripwire — mirrors TestResetDeviceRequiresConfirmation.
func TestSetPowerStateRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x22}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	wireHit := false
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDPowerState {
			wireHit = true
		}
		return nil, false, 0, 0
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/power-state", map[string]any{"value": 0x00})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/power-state", map[string]any{"value": 0x00, "confirm": "yes"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if wireHit {
		t.Fatal("POWER_STATE reached the wire without a valid confirm")
	}
}

func TestSetPowerStateSucceedsWithConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x23}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	var sent byte
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDPowerState && msg.CommandClass == rdm.SetCommand {
			sent = msg.ParameterData[0]
		}
		return nil, false, 0, 0
	})

	rr := h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/power-state", map[string]any{"value": int(rdm.PowerStateStandby), "confirm": "CONFIRM"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sent != byte(rdm.PowerStateStandby) {
		t.Fatalf("wire got PD=0x%02X, want 0x%02X (Standby)", sent, byte(rdm.PowerStateStandby))
	}
}

// TestSetSelfTestRequiresConfirmation guards the self-test start endpoint's
// confirm tripwire the same way.
func TestSetSelfTestRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x24}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	wireHit := false
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDPerformSelfTest {
			wireHit = true
		}
		return nil, false, 0, 0
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/self-test", map[string]any{"test": 1})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if wireHit {
		t.Fatal("PERFORM_SELFTEST reached the wire without a valid confirm")
	}
}

func TestCapturePresetRequiresConfirmationAndValidatesScene(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x25}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	var gotPD []byte
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDCapturePreset {
			gotPD = msg.ParameterData
		}
		return nil, false, 0, 0
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/capture-preset", map[string]any{"scene": 5})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if gotPD != nil {
		t.Fatal("CAPTURE_PRESET reached the wire without a valid confirm")
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/capture-preset", map[string]any{"scene": 5, "confirm": "CONFIRM"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(gotPD) != 2 || gotPD[0] != 0 || gotPD[1] != 5 {
		t.Fatalf("scene-only PD = %v, want [0x00,0x05]", gotPD)
	}

	rr = h.runHTTPAsync(t, "POST", "/api/device/"+uid.String()+"/capture-preset", map[string]any{
		"scene": 6, "confirm": "CONFIRM", "includeTiming": true, "upFadeTime": 10, "downFadeTime": 20, "waitTime": 30,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	want := []byte{0x00, 0x06, 0x00, 0x0A, 0x00, 0x14, 0x00, 0x1E}
	if len(gotPD) != 8 {
		t.Fatalf("timed PD = %v, want length 8", gotPD)
	}
	for i, b := range want {
		if gotPD[i] != b {
			t.Fatalf("timed PD = %v, want %v", gotPD, want)
		}
	}
}

func TestSetPresetPlaybackRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	node := h.seedNode(t)
	uid := rdm.UID{ManufacturerID: 0x22A6, DeviceID: 0x26}
	ref := session.NodeRef{Key: session.NodeKey{IP: node.Key.IP, BindIndex: node.Key.BindIndex}, Addr: node.Addr, Port: mustPort(t)}
	h.srv.Registry.NoteFixture(ref, uid)
	wireHit := false
	h.wireDeviceResponder(uid, func(msg rdm.Message) ([]byte, bool, rdm.NackReason, byte) {
		if msg.ParameterID == rdm.PIDPresetPlayback {
			wireHit = true
		}
		return nil, false, 0, 0
	})

	rr := doJSON(t, h.srv.Handler(), "POST", "/api/device/"+uid.String()+"/preset-playback", map[string]any{"mode": 1, "level": 255})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirm: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if wireHit {
		t.Fatal("PRESET_PLAYBACK reached the wire without a valid confirm")
	}
}

// TestDeviceControlFieldJSON_ZeroValueMarshalsExplicit guards the
// zero-is-real-data fields this file introduces the same way
// TestServiceLifeFieldJSON_ZeroValueMarshalsExplicit guards serviceLifeFieldJSON —
// a real, known 0 (Full Off's value 0x00; a preset Level of 0, "scaled at
// 0") must round-trip explicitly rather than reading back as `undefined`.
func TestDeviceControlFieldJSON_ZeroValueMarshalsExplicit(t *testing.T) {
	f := deviceControlFieldJSON{Known: true, Value: 0}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"value":0`) {
		t.Errorf("marshalled device-control field missing \"value\":0; got %s", got)
	}

	pp := presetPlaybackFieldJSON{Known: true, Mode: 0, Level: 0}
	data, err = json.Marshal(pp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"mode":0`) {
		t.Errorf("marshalled preset-playback field missing \"mode\":0; got %s", got)
	}
	if !strings.Contains(got, `"level":0`) {
		t.Errorf("marshalled preset-playback field missing \"level\":0; got %s", got)
	}
}

// TestSelfTestEntryJSON_SliceNeverNull guards deviceControlJSON.SelfTests:
// an empty roster must marshal as `[]`, never `null` — the same slice-must-
// be-make bug class this project has been bitten by before.
func TestSelfTestEntryJSON_SliceNeverNull(t *testing.T) {
	out := deviceControlJSON{SelfTests: make([]selfTestEntryJSON, 0)}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"selfTests":[]`) {
		t.Errorf("marshalled deviceControlJSON missing \"selfTests\":[]; got %s", got)
	}
}
