// This file adds the device-centric API surface for Benny512's Phase 1c+
// pass (self-describing manufacturer PIDs, sensors, device status) — see
// the package doc comment in server.go for the general REST/WS/JSON
// conventions these handlers follow (writeJSON/writeError/decodeJSON,
// r.PathValue for path params, params.NackError -> 422, timeout ->
// 504, other errors -> 502).
//
// Endpoint reference (UI agent — this is the authoritative shape; keep it
// in sync with routes() below):
//
//	GET  /api/device/{uid}/params              -> []paramDescriptorJSON (cached only, no wire traffic)
//	GET  /api/device/{uid}/param/{pid}          -> paramValueJSON        ({pid} is 4-hex-digit, e.g. "8010")
//	POST /api/device/{uid}/param/{pid}          <- setParamRequestJSON   -> {"status":"ok"}
//	POST /api/device/{uid}/introspect           -> 202 {"status":"started"}; progress/result over WS (see wsMessage Type "introspect_progress"/"introspect_complete")
//	GET  /api/device/{uid}/sensors              -> []sensorReadingJSON
//	POST /api/device/{uid}/sensors/record       <- sensorActionRequestJSON -> {"status":"ok"}
//	POST /api/device/{uid}/sensors/reset        <- sensorActionRequestJSON -> {"status":"ok"}
//	GET  /api/device/{uid}/status               -> []statusMessageJSON (query: ?filter=advisory|warning|error, default advisory)
//
// Phase D additions (task 2/3 — service life, destructive actions, and the
// supported-PID surface the UI needs to gate its own probing):
//
//	GET  /api/device/{uid}/service-life         -> serviceLifeJSON (best-effort per field; see that type)
//	POST /api/device/{uid}/service-life         <- setServiceLifeRequestJSON -> {"status":"ok"}
//	GET  /api/device/{uid}/actions              -> deviceActionsJSON (whether RESET_DEVICE/FACTORY_DEFAULTS are advertised)
//	GET  /api/device/{uid}/factory-defaults     -> {"factoryDefaults":bool}   (whether the device currently reports factory-default settings)
//	POST /api/device/{uid}/factory-defaults     <- {"confirm":"RESET"}       -> {"status":"ok","note":string}  (destructive; SET FACTORY_DEFAULTS)
//	POST /api/device/{uid}/reset                <- resetDeviceRequestJSON   -> resetDeviceResponseJSON        (destructive; SET RESET_DEVICE)
//	GET  /api/device/{uid}/supported-parameters -> supportedParametersJSON  (resolves+caches SUPPORTED_PARAMETERS if not yet known)
package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"benny512/internal/capture"
	"benny512/internal/params"
	"benny512/internal/rdm"
)

// introspectTimeout bounds a single /introspect run — it can involve dozens
// of PARAMETER_DESCRIPTION round-trips, so this is deliberately generous
// (report §1.1 item 3's "sluggish for N in the hundreds" caveat).
const introspectTimeout = 60 * time.Second

// deviceParamTimeout bounds one GET/SET param round-trip.
const deviceParamTimeout = 15 * time.Second

func (s *Server) resolveDeviceClient(w http.ResponseWriter, r *http.Request) (*params.Client, rdm.UID, bool) {
	uid, ok := rdm.ParseUID(r.PathValue("uid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad uid %q", r.PathValue("uid")))
		return nil, rdm.UID{}, false
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown device %s", uid))
		return nil, rdm.UID{}, false
	}
	return params.New(s.RDM, node, uid), uid, true
}

func parsePID(s string) (rdm.ParameterID, bool) {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, false
	}
	return rdm.ParameterID(v), true
}

// --- descriptors -------------------------------------------------------

type paramDescriptorJSON struct {
	PID            string `json:"pid"` // 4-hex-digit, e.g. "8010"
	Label          string `json:"label"`
	DataType       byte   `json:"dataType"`
	DataTypeName   string `json:"dataTypeName"`
	CommandClass   byte   `json:"commandClass"`
	SupportsGet    bool   `json:"supportsGet"`
	SupportsSet    bool   `json:"supportsSet"`
	PDLSize        byte   `json:"pdlSize"`
	Unit           byte   `json:"unit"`
	UnitSuffix     string `json:"unitSuffix"`
	Prefix         byte   `json:"prefix"`
	Min            int64  `json:"min"`
	Max            int64  `json:"max"`
	Default        int64  `json:"default"`
	SelfDescribing bool   `json:"selfDescribing"`
	// SpecDefined: this app filled the layout in from a published standard
	// because the device cannot be asked (E1.20 §10.4.2). The editor uses it
	// to render a real control instead of a raw-hex box, and to NOT show the
	// "raw / unverified" tag — the layout is neither raw nor unverified.
	SpecDefined bool `json:"specDefined"`
	// Tier is params.Tier(d.PID)'s string form — "promoted" | "standard" |
	// "hidden" (Phase D task 1). In practice a "hidden" row should never
	// actually appear here: isEditorTarget already excludes TierHidden PIDs
	// from Introspect's results entirely (see introspect.go), so this is
	// included for completeness/future-proofing rather than because the UI
	// needs to filter it out itself.
	Tier string `json:"tier"`
}

// paramLabel resolves a descriptor's display label: the device's own
// PARAMETER_DESCRIPTION-reported description when available (d.Label,
// self-describing PIDs), else the ANSI E1.20/E1.37 mnemonic from the shared
// PID-name table (task ask, "Generic ESTA PID rendering": "still surface it
// ... using the PID-name table for the label") for any PID this app
// recognizes by number even without a live description, else empty — the
// UI's own fallback renders "PID 0x____" when this is empty.
func paramLabel(d params.ParamDescriptor) string {
	if d.Label != "" {
		return d.Label
	}
	return capture.PIDName(d.PID)
}

func toParamDescriptorJSON(d params.ParamDescriptor) paramDescriptorJSON {
	return paramDescriptorJSON{
		PID: fmt.Sprintf("%04X", uint16(d.PID)), Label: paramLabel(d),
		DataType: byte(d.DataType), DataTypeName: d.DataType.String(),
		CommandClass: byte(d.CommandClass), SupportsGet: d.CommandClass.SupportsGet(), SupportsSet: d.CommandClass.SupportsSet(),
		PDLSize: d.PDLSize, Unit: byte(d.Unit), UnitSuffix: d.Unit.Suffix(), Prefix: byte(d.Prefix),
		Min: d.Min, Max: d.Max, Default: d.Default, SelfDescribing: d.SelfDescribing,
		SpecDefined: d.SpecDefined,
		Tier:        params.Tier(d.PID).String(),
	}
}

// handleGetDeviceParams returns whatever descriptors have already been
// resolved (via a prior /introspect or /param GET) — it issues no RDM
// traffic itself, matching the task's "descriptors, cached" ask.
func (s *Server) handleGetDeviceParams(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	descs := client.Descriptors()
	out := make([]paramDescriptorJSON, 0, len(descs))
	for _, d := range descs {
		out = append(out, toParamDescriptorJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- single param GET/SET ------------------------------------------------

type paramValueJSON struct {
	PID  string `json:"pid"`
	Kind string `json:"kind"` // "int" | "string" | "raw"
	// Int deliberately has NO `omitempty`: Kind discriminates which of
	// Int/Str/Hex applies, so Int is only meaningful when Kind=="int" —
	// but plenty of RDM params legitimately report 0 (a level, an address,
	// a count), and `omitempty` would silently drop that real value from
	// the wire exactly like Entry.Universe did. Str/Hex keep `omitempty`:
	// they're genuinely absent (never even considered) whenever
	// Kind!="int" picks a different branch, and an empty string is not a
	// distinct piece of data from "absent" for either of them the way a
	// numeric 0 is for Int.
	Int        int64               `json:"int"`
	Str        string              `json:"str,omitempty"`
	Hex        string              `json:"hex,omitempty"` // set when Kind=="raw"
	Descriptor paramDescriptorJSON `json:"descriptor"`
}

func (s *Server) handleGetDeviceParam(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	pid, ok := parsePID(r.PathValue("pid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad pid %q, want 4 hex digits", r.PathValue("pid")))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	val, desc, err := client.GetParam(ctx, pid)
	if err != nil {
		writeParamError(w, err)
		return
	}
	out := paramValueJSON{PID: fmt.Sprintf("%04X", uint16(pid)), Kind: val.Kind.String(), Descriptor: toParamDescriptorJSON(desc)}
	switch val.Kind {
	case params.ParamValueInt:
		out.Int = val.Int
	case params.ParamValueString:
		out.Str = val.Str
	case params.ParamValueRaw:
		out.Hex = hex.EncodeToString(val.Raw)
	}
	writeJSON(w, http.StatusOK, out)
}

// setParamRequestJSON's Value shape depends on the PID's DataType (report
// §1.1): a number for numeric DS_* types, a string for DS_ASCII, or a hex
// string for anything else (bit field, raw fallback, unrecognized DS_*).
// The handler decides which to expect by calling DescribeParam first.
type setParamRequestJSON struct {
	Value json.RawMessage `json:"value"`
}

func (s *Server) handleSetDeviceParam(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	pid, ok := parsePID(r.PathValue("pid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad pid %q, want 4 hex digits", r.PathValue("pid")))
		return
	}
	var req setParamRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()

	desc := client.DescribeParam(ctx, pid)
	value, err := decodeSetValue(req.Value, desc)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := client.SetParam(ctx, pid, value); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeSetValue interprets a JSON value per desc's DataType: a JSON number
// for numeric types, a JSON string for DS_ASCII, and either a JSON string
// (hex-encoded) or a JSON array of numbers for the raw-fallback case.
func decodeSetValue(raw json.RawMessage, desc params.ParamDescriptor) (any, error) {
	if !desc.Typed() {
		return decodeHexOrByteArray(raw)
	}
	switch desc.DataType {
	case rdm.DSASCII:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("value must be a string for DS_ASCII: %w", err)
		}
		return s, nil
	case rdm.DSUnsignedByte, rdm.DSSignedByte, rdm.DSUnsignedWord, rdm.DSSignedWord,
		rdm.DSUnsignedDWord, rdm.DSSignedDWord, rdm.DSBoolean:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("value must be a number for this PID's data type: %w", err)
		}
		return n, nil
	default:
		return decodeHexOrByteArray(raw)
	}
}

func decodeHexOrByteArray(raw json.RawMessage) ([]byte, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		b, decErr := hex.DecodeString(s)
		if decErr != nil {
			return nil, fmt.Errorf("value must be a hex string for this PID: %w", decErr)
		}
		return b, nil
	}
	var ints []int
	if err := json.Unmarshal(raw, &ints); err == nil {
		b := make([]byte, len(ints))
		for i, v := range ints {
			b[i] = byte(v)
		}
		return b, nil
	}
	return nil, fmt.Errorf("value must be a hex string or byte array for this PID")
}

// --- introspect ------------------------------------------------------------

func (s *Server) handleIntrospectDevice(w http.ResponseWriter, r *http.Request) {
	client, uid, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	uidStr := uid.String()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), introspectTimeout)
		defer cancel()
		result, err := client.Introspect(ctx, func(p params.IntrospectProgress) {
			s.hub.broadcast(wsMessage{
				Type: "introspect_progress", Kind: uidStr, At: time.Now(),
				Introspect: &introspectProgressJSON{UID: uidStr, Done: p.Done, Total: p.Total, PID: fmt.Sprintf("%04X", uint16(p.PID))},
			})
		})
		if err != nil {
			s.hub.broadcast(wsMessage{Type: "introspect_complete", Kind: uidStr, At: time.Now(), Err: err.Error()})
			return
		}
		descs := make([]paramDescriptorJSON, 0, len(result.Descriptors))
		for _, d := range result.Descriptors {
			descs = append(descs, toParamDescriptorJSON(d))
		}
		s.hub.broadcast(wsMessage{Type: "introspect_complete", Kind: uidStr, At: time.Now(), Descriptors: descs})
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "uid": uidStr})
}

// --- sensors ---------------------------------------------------------------

type sensorReadingJSON struct {
	Number           byte   `json:"number"`
	Type             byte   `json:"type"`
	TypeName         string `json:"typeName"`
	Unit             byte   `json:"unit"`
	UnitSuffix       string `json:"unitSuffix"`
	Prefix           byte   `json:"prefix"`
	Description      string `json:"description"`
	Present          int16  `json:"present"`
	PresentFormatted string `json:"presentFormatted"`
	Lowest           int16  `json:"lowest"`
	Highest          int16  `json:"highest"`
	Recorded         int16  `json:"recorded"`
	// RangeMin/RangeMax/NormalMin/NormalMax deliberately have NO
	// `omitempty`: this is the exact field-class the project's own Notes
	// flag (sensorReadingJSON's range/normal-band fields losing a
	// legitimate 0) — HasRange/HasNormalBand already say whether the pair
	// is meaningful, so `omitempty` here doesn't add "optional" semantics,
	// it just drops a real 0 boundary (e.g. a sensor whose normal band
	// starts at 0) from the wire while a nonzero boundary round-trips fine.
	HasRange      bool  `json:"hasRange"`
	RangeMin      int16 `json:"rangeMin"`
	RangeMax      int16 `json:"rangeMax"`
	HasNormalBand bool  `json:"hasNormalBand"`
	NormalMin     int16 `json:"normalMin"`
	NormalMax     int16 `json:"normalMax"`
	InNormalBand  bool  `json:"inNormalBand"`
	RecordsValue  bool  `json:"recordsValue"`
	RecordsRange  bool  `json:"recordsRange"`
}

func toSensorReadingJSON(r params.SensorReading) sensorReadingJSON {
	d := r.Definition
	return sensorReadingJSON{
		Number: d.SensorNumber, Type: byte(d.Type), TypeName: d.Type.String(),
		Unit: byte(d.Unit), UnitSuffix: d.Unit.Suffix(), Prefix: byte(d.Prefix),
		Description: d.Description,
		Present:     r.Value.Present, PresentFormatted: rdm.FormatValue(int64(r.Value.Present), d.Unit, d.Prefix),
		Lowest: r.Value.Lowest, Highest: r.Value.Highest, Recorded: r.Value.Recorded,
		HasRange: d.HasRange(), RangeMin: d.RangeMin, RangeMax: d.RangeMax,
		HasNormalBand: d.HasNormalBand(), NormalMin: d.NormalMin, NormalMax: d.NormalMax,
		InNormalBand: r.InNormalBand(), RecordsValue: d.RecordsValue(), RecordsRange: d.RecordsRange(),
	}
}

func (s *Server) handleGetDeviceSensors(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	readings, err := client.Sensors(ctx)
	if err != nil {
		writeParamError(w, err)
		return
	}
	out := make([]sensorReadingJSON, 0, len(readings))
	for _, r := range readings {
		out = append(out, toSensorReadingJSON(r))
	}
	writeJSON(w, http.StatusOK, out)
}

type sensorActionRequestJSON struct {
	// Sensor is the sensor index, or 255 (rdm.AllSensors) for every sensor.
	Sensor byte `json:"sensor"`
}

func (s *Server) handleRecordDeviceSensors(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req sensorActionRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.RecordSensors(ctx, req.Sensor); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleResetDeviceSensors(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req sensorActionRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.ResetSensors(ctx, req.Sensor); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- status ------------------------------------------------------------

type statusMessageJSON struct {
	SubDevice uint16 `json:"subDevice"`
	Type      byte   `json:"type"`
	TypeName  string `json:"typeName"`
	MessageID uint16 `json:"messageId"`
	Value1    int16  `json:"value1"`
	Value2    int16  `json:"value2"`
}

func (s *Server) handleGetDeviceStatus(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	filter := rdm.StatusAdvisory
	switch r.URL.Query().Get("filter") {
	case "warning":
		filter = rdm.StatusWarning
	case "error":
		filter = rdm.StatusError
	case "advisory", "":
		filter = rdm.StatusAdvisory
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	msgs, err := client.DeviceStatus(ctx, filter, 0)
	if err != nil {
		writeParamError(w, err)
		return
	}
	out := make([]statusMessageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, statusMessageJSON{
			SubDevice: m.SubDevice, Type: byte(m.Type), TypeName: m.Type.String(),
			MessageID: m.MessageID, Value1: m.Value1, Value2: m.Value2,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- service life (Phase D task 2) -----------------------------------------

// serviceLifeFieldJSON is one best-effort field of GET /service-life: Known
// distinguishes "device answered" from "not supported/couldn't be
// fetched" (the latter carries Error, a human-readable message — usually
// params.ErrPIDNotAdvertised's text when the device's own SUPPORTED_
// PARAMETERS omits the PID, per Phase D task 3's gate). Label is set only
// for LampState, where the raw byte alone (Table A-8) isn't self-explanatory.
type serviceLifeFieldJSON struct {
	Known bool `json:"known"`
	// Value deliberately has NO `omitempty`: Known already distinguishes
	// "device answered" from "unsupported/unfetched", but when Known is
	// true, Value==0 is real, meaningful data (a brand-new fixture
	// legitimately has 0 lamp strikes / 0 power cycles) — `omitempty`
	// would drop exactly that value from the wire.
	Value int64  `json:"value"`
	Label string `json:"label,omitempty"`
	Error string `json:"error,omitempty"`
}

// serviceLifeJSON is GET /api/device/{uid}/service-life's response shape.
// Every field is independently best-effort — a device that supports only
// some of the five PIDs (e.g. an LED fixture with no lamp at all, so
// LampHours/LampStrikes/LampState NACK while DeviceHours/DevicePowerCycles
// ACK) still returns 200 with a mix of Known:true/Known:false fields, never
// a whole-request error for one missing PID.
type serviceLifeJSON struct {
	DeviceHours       serviceLifeFieldJSON `json:"deviceHours"`
	LampHours         serviceLifeFieldJSON `json:"lampHours"`
	LampStrikes       serviceLifeFieldJSON `json:"lampStrikes"`
	LampState         serviceLifeFieldJSON `json:"lampState"`
	DevicePowerCycles serviceLifeFieldJSON `json:"devicePowerCycles"`
}

func serviceLifeCounterField(ctx context.Context, fn func(context.Context) (uint32, error)) serviceLifeFieldJSON {
	v, err := fn(ctx)
	if err != nil {
		return serviceLifeFieldJSON{Known: false, Error: err.Error()}
	}
	return serviceLifeFieldJSON{Known: true, Value: int64(v)}
}

func (s *Server) handleGetServiceLife(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()

	out := serviceLifeJSON{
		DeviceHours:       serviceLifeCounterField(ctx, client.DeviceHours),
		LampHours:         serviceLifeCounterField(ctx, client.LampHours),
		LampStrikes:       serviceLifeCounterField(ctx, client.LampStrikes),
		DevicePowerCycles: serviceLifeCounterField(ctx, client.DevicePowerCycles),
	}
	if state, err := client.LampState(ctx); err != nil {
		out.LampState = serviceLifeFieldJSON{Known: false, Error: err.Error()}
	} else {
		out.LampState = serviceLifeFieldJSON{Known: true, Value: int64(state), Label: state.String()}
	}
	writeJSON(w, http.StatusOK, out)
}

// setServiceLifeRequestJSON's Field selects which of the five counters/
// enum to SET; Value is the new value (a plain JSON number in every case —
// LampState's enum values all fit a byte, so no separate string encoding is
// needed the way DS_ASCII PIDs elsewhere in this package require).
type setServiceLifeRequestJSON struct {
	Field string `json:"field"` // "deviceHours" | "lampHours" | "lampStrikes" | "lampState" | "devicePowerCycles"
	Value int64  `json:"value"`
}

// ErrUnknownServiceLifeField is returned (as a 400) for a Field value other
// than the five this endpoint recognizes.
var ErrUnknownServiceLifeField = errors.New("unknown service-life field")

// ErrLampStateOutOfRange is returned (as a 400) when Value doesn't fit
// LAMP_STATE's 1-byte wire encoding.
var ErrLampStateOutOfRange = errors.New("lampState value must be 0-255")

func (s *Server) handleSetServiceLifeField(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req setServiceLifeRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()

	var err error
	switch req.Field {
	case "deviceHours":
		err = client.SetDeviceHours(ctx, uint32(req.Value))
	case "lampHours":
		err = client.SetLampHours(ctx, uint32(req.Value))
	case "lampStrikes":
		err = client.SetLampStrikes(ctx, uint32(req.Value))
	case "devicePowerCycles":
		err = client.SetDevicePowerCycles(ctx, uint32(req.Value))
	case "lampState":
		if req.Value < 0 || req.Value > 255 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%w: got %d", ErrLampStateOutOfRange, req.Value))
			return
		}
		err = client.SetLampState(ctx, rdm.LampState(req.Value))
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("%w: %q", ErrUnknownServiceLifeField, req.Field))
		return
	}
	if err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- destructive actions: RESET_DEVICE / FACTORY_DEFAULTS (Phase D task 2) -

// actionSupportJSON is one entry of GET /actions: Known false means "could
// not determine" (SUPPORTED_PARAMETERS itself is unanswered for this
// device) and MUST be treated as "don't know", never as "confirmed
// unsupported" — Supported is only meaningful when Known is true.
type actionSupportJSON struct {
	Known     bool `json:"known"`
	Supported bool `json:"supported"`
}

// deviceActionsJSON is GET /api/device/{uid}/actions' response: whether
// this device's own SUPPORTED_PARAMETERS lists RESET_DEVICE/
// FACTORY_DEFAULTS, resolving (and caching) that list on demand if not
// already known. There is no way to learn, from any RDM PID, whether a
// device that DOES advertise RESET_DEVICE distinguishes a warm reset from a
// cold one — SUPPORTED_PARAMETERS only says the PID exists at all, and
// PARAMETER_DESCRIPTION is spec-legal only for manufacturer-specific PIDs
// (E1.20 §10.4.2), so it can never describe a standard PID like this one.
// The UI should offer BOTH modes whenever ResetDevice.Supported is true,
// and never claim to know more than that.
type deviceActionsJSON struct {
	ResetDevice     actionSupportJSON `json:"resetDevice"`
	FactoryDefaults actionSupportJSON `json:"factoryDefaults"`
}

func (s *Server) handleGetDeviceActions(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	var out deviceActionsJSON
	out.ResetDevice.Supported, out.ResetDevice.Known = client.IsAdvertised(ctx, rdm.PIDResetDevice)
	out.FactoryDefaults.Supported, out.FactoryDefaults.Known = client.IsAdvertised(ctx, rdm.PIDFactoryDefaults)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetFactoryDefaults(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	v, err := client.FactoryDefaults(ctx)
	if err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"factoryDefaults": v})
}

// ErrFactoryDefaultsConfirmationRequired is returned (as a 400) when POST
// /api/device/{uid}/factory-defaults is called without the exact confirm
// string — mirrors internal/web/reset.go's {"confirm":"RESET"} tripwire
// discipline for the app's own full-reset endpoint (task ask: "the server
// must not be one stray request away from resetting a fixture").
var ErrFactoryDefaultsConfirmationRequired = errors.New("confirmation required")

type confirmOnlyRequestJSON struct {
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetFactoryDefaults(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req confirmOnlyRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != "RESET" {
		writeError(w, http.StatusBadRequest, ErrFactoryDefaultsConfirmationRequired)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.ResetToFactoryDefaults(ctx); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"note":   "FACTORY_DEFAULTS sent. The device will revert its own settings to manufacturer defaults; anything Benny512 has cached about it may now be stale until it answers again.",
	})
}

// resetDeviceRequestJSON is POST /api/device/{uid}/reset's request body.
// Mode is required and must be exactly "warm" or "cold" — see
// params.Client.ResetDevice's doc comment for why the app can never pick
// one on the device's behalf. Confirm must be exactly "RESET", mirroring
// internal/web/reset.go's discipline: the server must not be one stray
// request away from resetting a fixture, even with the UI's own
// arm-then-confirm flow in front of it.
type resetDeviceRequestJSON struct {
	Mode    string `json:"mode"`
	Confirm string `json:"confirm"`
}

// resetDeviceResponseJSON's Note is a finished sentence, same convention as
// server.go's unreachableNote: the UI renders it verbatim. It exists
// because RESET_DEVICE's ACK is not the end of the story — E1.20 §10.11.2
// says the command "shall also clear the Discovery Mute flag" and, for a
// cold reset, "is the equivalent of removing and reapplying power to the
// device": the device is expected to drop off the bus and need
// re-discovery, which nothing about a bare 200 OK would otherwise convey.
type resetDeviceResponseJSON struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
	Note   string `json:"note"`
}

var (
	// ErrDeviceResetConfirmationRequired is returned (as a 400) when the
	// confirm string is missing/wrong.
	ErrDeviceResetConfirmationRequired = errors.New("confirmation required")
	// ErrDeviceResetBadMode is returned (as a 400) when mode isn't exactly
	// "warm" or "cold".
	ErrDeviceResetBadMode = errors.New(`mode must be "warm" or "cold"`)
)

// handleResetDevice issues SET RESET_DEVICE (E1.20 §10.11.2).
//
// What this handler deliberately does NOT do: remove the device from
// s.Registry, or otherwise force it out of the ToD/fixture table. The
// device SET_COMMAND_RESPONSE ACK only confirms the command was accepted,
// not that the reset has completed — the responder is still expected to
// keep answering on THIS transaction before it drops off, so proactively
// deleting its registry row here would race a device that hasn't actually
// gone anywhere yet, and would turn a "reset sent, please re-discover
// shortly" situation into a device that looks like it vanished/errored,
// which reads as a worse failure than what actually happened. What it DOES
// do is drop this package's own PARAMETER_DESCRIPTION/introspection cache
// for the UID (params.Client.ResetDevice calls params.ForgetDevice
// internally) since that's presumptively stale the moment a reset is
// accepted. Whether the registry ToD entry itself should eventually be
// proactively marked stale/pending-rediscovery — rather than leaving it to
// the next Discover pass or ToD refresh to notice the device dropped out —
// is a genuinely debatable product call (it would need a way to say
// "recently reset, expect a gap" distinctly from "unreachable"/"gone") that
// this pass deliberately leaves for the owner to weigh in on, rather than
// guessing; the Note this handler returns is what carries that expectation
// to the UI/user in the meantime.
func (s *Server) handleResetDevice(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req resetDeviceRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != "RESET" {
		writeError(w, http.StatusBadRequest, ErrDeviceResetConfirmationRequired)
		return
	}
	var mode rdm.ResetMode
	switch req.Mode {
	case "warm":
		mode = rdm.ResetWarm
	case "cold":
		mode = rdm.ResetCold
	default:
		writeError(w, http.StatusBadRequest, ErrDeviceResetBadMode)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.ResetDevice(ctx, mode); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resetDeviceResponseJSON{
		Status: "ok", Mode: req.Mode,
		Note: "RESET_DEVICE (" + req.Mode + ") sent and accepted. The device will drop off the bus and clear its Discovery Mute flag — run Discover again once it comes back to re-add it.",
	})
}

// --- supported-PID surface (Phase D task 3) --------------------------------

// supportedParametersJSON is GET /api/device/{uid}/supported-parameters'
// response. Known false means SUPPORTED_PARAMETERS itself is unanswered
// for this device (NACKed, timed out, or a prior attempt already failed) —
// PIDs is always [] (never omitted/null) in that case, never used to imply
// "advertises nothing". A client-side caller (Phase D task 3's brief: some
// of the speculative probing this task fixes is client-driven and needs
// exactly this to gate itself) should treat Known:false as "go ahead and
// ask, same as today" — never as "block the request".
type supportedParametersJSON struct {
	Known bool     `json:"known"`
	PIDs  []string `json:"pids"`
}

func (s *Server) handleGetSupportedParameters(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	pids, known := client.SupportedParameters(ctx)
	out := supportedParametersJSON{Known: known, PIDs: make([]string, 0, len(pids))}
	for _, p := range pids {
		out.PIDs = append(out.PIDs, fmt.Sprintf("%04X", uint16(p)))
	}
	writeJSON(w, http.StatusOK, out)
}
