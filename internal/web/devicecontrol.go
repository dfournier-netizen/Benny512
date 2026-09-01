// This file adds the HTTP surface for E1.20 §10.11 "Device Control
// Parameter Messages" beyond what device.go already covers (IDENTIFY_DEVICE
// has no dedicated endpoint — it goes through the generic
// /api/device/{uid}/param/{pid} path already — and RESET_DEVICE/
// FACTORY_DEFAULTS have their own destructive-action endpoints in
// device.go). Follows device.go's service-life/actions pattern rather than
// inventing a PID-per-endpoint shape: one GET bundles every best-effort
// device-control field/list, and each state-changing action gets its own
// small POST endpoint requiring an explicit {"confirm":"CONFIRM"} body,
// mirroring device.go's {"confirm":"RESET"} tripwire discipline for
// RESET_DEVICE/FACTORY_DEFAULTS. The UI's own arm-then-confirm gate
// (internal/web/static/js/devicedetail.js) is what actually protects the
// click; this is the fixed wire contract the server demands regardless.
//
// Endpoint reference (kept here rather than device.go's own list since this
// is a separate file — server.go's routes() is the authoritative wiring):
//
//	GET  /api/device/{uid}/device-control -> deviceControlJSON (best-effort per field, like serviceLifeJSON)
//	POST /api/device/{uid}/power-state    <- devicePowerStateRequestJSON     -> {"status":"ok"}
//	POST /api/device/{uid}/self-test      <- deviceSelfTestRequestJSON      -> {"status":"ok"}
//	POST /api/device/{uid}/capture-preset <- deviceCapturePresetRequestJSON -> {"status":"ok"}
//	POST /api/device/{uid}/preset-playback <- devicePresetPlaybackRequestJSON -> {"status":"ok"}
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"benny512/internal/rdm"
)

// deviceControlConfirm is the fixed confirm-string wire contract every
// §10.11 state-changing endpoint in this file requires, mirroring device.go's
// "RESET" sentinel for RESET_DEVICE/FACTORY_DEFAULTS.
const deviceControlConfirm = "CONFIRM"

// ErrDeviceControlConfirmationRequired is returned (as a 400) when a §10.11
// state-changing endpoint is called without the exact confirm string.
var ErrDeviceControlConfirmationRequired = errors.New("confirmation required")

// deviceControlFieldJSON is one best-effort field of GET /device-control —
// same Known/Value/Label/Error shape as device.go's serviceLifeFieldJSON,
// reused here rather than duplicated with a different name.
type deviceControlFieldJSON = serviceLifeFieldJSON

// selfTestEntryJSON is one self test SELFTEST_ENHANCED reports, with its
// SELF_TEST_DESCRIPTION label resolved best-effort (Description is empty
// when the device doesn't support/answer that PID for this number — the UI
// falls back to a bare "Self test N", never inventing a label).
type selfTestEntryJSON struct {
	Number        int    `json:"number"`
	StatusCode    int    `json:"statusCode"`
	StatusLabel   string `json:"statusLabel"`
	Capability    int    `json:"capability"`
	AutoTerminate bool   `json:"autoTerminate"`
	Description   string `json:"description,omitempty"`
}

// presetPlaybackFieldJSON is GET /device-control's PRESET_PLAYBACK field:
// deviceControlFieldJSON's Known/Error shape plus the extra Level byte
// PRESET_PLAYBACK carries alongside its Mode (which reuses Value/Label).
type presetPlaybackFieldJSON struct {
	Known bool `json:"known"`
	Mode  int  `json:"mode"`
	// Level deliberately has NO `omitempty`: 0 is a real, meaningful Level
	// (a preset scaled fully down), not "absent" — same reasoning as every
	// other zero-is-real numeric field in this package (see
	// serviceLifeFieldJSON.Value's doc comment).
	Level int    `json:"level"`
	Label string `json:"label,omitempty"`
	Error string `json:"error,omitempty"`
}

// deviceControlJSON is GET /api/device/{uid}/device-control's response.
// Every field is independently best-effort, exactly like serviceLifeJSON —
// a device supporting only some of §10.11 still returns 200 with a mix of
// Known:true/Known:false fields, never a whole-request error for one
// unsupported PID.
type deviceControlJSON struct {
	PowerState     deviceControlFieldJSON  `json:"powerState"`
	SelfTestActive deviceControlFieldJSON  `json:"selfTestActive"`
	PresetPlayback presetPlaybackFieldJSON `json:"presetPlayback"`
	// SelfTests is the SELFTEST_ENHANCED-derived roster (empty, never null,
	// when SELFTEST_ENHANCED isn't advertised or the GET failed —
	// SelfTestsKnown distinguishes "device has no self tests to enumerate"
	// from "couldn't be determined", the same Known-discriminates-absence
	// pattern as every other field here).
	SelfTests              []selfTestEntryJSON `json:"selfTests"`
	SelfTestsKnown         bool                `json:"selfTestsKnown"`
	CapturePresetSupported actionSupportJSON   `json:"capturePresetSupported"`
}

func (s *Server) handleGetDeviceControl(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()

	out := deviceControlJSON{SelfTests: make([]selfTestEntryJSON, 0)}

	if ps, err := client.PowerState(ctx); err != nil {
		out.PowerState = deviceControlFieldJSON{Known: false, Error: err.Error()}
	} else {
		out.PowerState = deviceControlFieldJSON{Known: true, Value: int64(ps), Label: ps.String()}
	}

	if active, err := client.SelfTestActive(ctx); err != nil {
		out.SelfTestActive = deviceControlFieldJSON{Known: false, Error: err.Error()}
	} else {
		var v int64
		if active {
			v = 1
		}
		out.SelfTestActive = deviceControlFieldJSON{Known: true, Value: v}
	}

	if pp, err := client.PresetPlayback(ctx); err != nil {
		out.PresetPlayback = presetPlaybackFieldJSON{Known: false, Error: err.Error()}
	} else {
		out.PresetPlayback = presetPlaybackFieldJSON{Known: true, Mode: int(pp.Mode), Level: int(pp.Level), Label: pp.Mode.String()}
	}

	out.CapturePresetSupported.Supported, out.CapturePresetSupported.Known = client.IsAdvertised(ctx, rdm.PIDCapturePreset)

	// SELFTEST_ENHANCED is the only way to enumerate which self test numbers
	// exist at all without invoking each one (E1.20 §10.11.8's own text) —
	// only attempted when the device actually advertises it, per this
	// package's speculative-PID gate.
	if supported, known := client.IsAdvertised(ctx, rdm.PIDSelfTestEnhanced); known && supported {
		if enh, err := client.SelfTestEnhanced(ctx); err == nil {
			out.SelfTestsKnown = true
			descSupported, descKnown := client.IsAdvertised(ctx, rdm.PIDSelfTestDescription)
			for _, e := range enh.Entries {
				entry := selfTestEntryJSON{
					Number: int(e.Number), StatusCode: int(e.Status), StatusLabel: e.Status.String(),
					Capability: int(e.Capability), AutoTerminate: e.Capability&rdm.SelfTestCapAutoTerminate != 0,
				}
				// descKnown==false means "don't know" (SUPPORTED_PARAMETERS
				// itself unresolved) — fall through and try anyway, same
				// "never claim to know more than that" rule the rest of this
				// package follows; descKnown==true && !descSupported is the
				// only case that's skipped outright.
				if descKnown && !descSupported {
					out.SelfTests = append(out.SelfTests, entry)
					continue
				}
				if d, err := client.SelfTestDescription(ctx, e.Number); err == nil {
					entry.Description = d.Label
				}
				out.SelfTests = append(out.SelfTests, entry)
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// --- POWER_STATE ------------------------------------------------------------

type devicePowerStateRequestJSON struct {
	Value   int    `json:"value"`
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetPowerState(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req devicePowerStateRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != deviceControlConfirm {
		writeError(w, http.StatusBadRequest, ErrDeviceControlConfirmationRequired)
		return
	}
	if req.Value < 0 || req.Value > 255 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("value must be 0-255"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.SetPowerState(ctx, rdm.PowerState(req.Value)); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- PERFORM_SELFTEST --------------------------------------------------------

type deviceSelfTestRequestJSON struct {
	Test    int    `json:"test"`
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetSelfTest(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req deviceSelfTestRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != deviceControlConfirm {
		writeError(w, http.StatusBadRequest, ErrDeviceControlConfirmationRequired)
		return
	}
	if req.Test < 0 || req.Test > 255 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("test must be 0-255"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.StartSelfTest(ctx, rdm.SelfTestNumber(req.Test)); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- CAPTURE_PRESET -----------------------------------------------------------

type deviceCapturePresetRequestJSON struct {
	Scene         int    `json:"scene"`
	IncludeTiming bool   `json:"includeTiming"`
	UpFadeTime    int    `json:"upFadeTime"`
	DownFadeTime  int    `json:"downFadeTime"`
	WaitTime      int    `json:"waitTime"`
	Confirm       string `json:"confirm"`
}

func (s *Server) handleCapturePreset(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req deviceCapturePresetRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != deviceControlConfirm {
		writeError(w, http.StatusBadRequest, ErrDeviceControlConfirmationRequired)
		return
	}
	if req.Scene < 0 || req.Scene > 0xFFFF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scene must be 0-65535"))
		return
	}
	var timing *rdm.PresetTiming
	if req.IncludeTiming {
		for _, v := range []int{req.UpFadeTime, req.DownFadeTime, req.WaitTime} {
			if v < 0 || v > 0xFFFF {
				writeError(w, http.StatusBadRequest, fmt.Errorf("fade/wait times must be 0-65535"))
				return
			}
		}
		timing = &rdm.PresetTiming{
			UpFadeTime: uint16(req.UpFadeTime), DownFadeTime: uint16(req.DownFadeTime), WaitTime: uint16(req.WaitTime),
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.CapturePreset(ctx, uint16(req.Scene), timing); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- PRESET_PLAYBACK ----------------------------------------------------------

type devicePresetPlaybackRequestJSON struct {
	Mode    int    `json:"mode"`
	Level   int    `json:"level"`
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetPresetPlayback(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req devicePresetPlaybackRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != deviceControlConfirm {
		writeError(w, http.StatusBadRequest, ErrDeviceControlConfirmationRequired)
		return
	}
	if req.Mode < 0 || req.Mode > 0xFFFF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be 0-65535"))
		return
	}
	if req.Level < 0 || req.Level > 0xFF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("level must be 0-255"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	p := rdm.PresetPlayback{Mode: rdm.PresetPlaybackMode(req.Mode), Level: byte(req.Level)}
	if err := client.SetPresetPlayback(ctx, p); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
