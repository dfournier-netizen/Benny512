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
package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

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
}

func toParamDescriptorJSON(d params.ParamDescriptor) paramDescriptorJSON {
	return paramDescriptorJSON{
		PID: fmt.Sprintf("%04X", uint16(d.PID)), Label: d.Label,
		DataType: byte(d.DataType), DataTypeName: d.DataType.String(),
		CommandClass: byte(d.CommandClass), SupportsGet: d.CommandClass.SupportsGet(), SupportsSet: d.CommandClass.SupportsSet(),
		PDLSize: d.PDLSize, Unit: byte(d.Unit), UnitSuffix: d.Unit.Suffix(), Prefix: byte(d.Prefix),
		Min: d.Min, Max: d.Max, Default: d.Default, SelfDescribing: d.SelfDescribing,
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
	PID        string              `json:"pid"`
	Kind       string              `json:"kind"` // "int" | "string" | "raw"
	Int        int64               `json:"int,omitempty"`
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
	if !desc.SelfDescribing {
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
	HasRange         bool   `json:"hasRange"`
	RangeMin         int16  `json:"rangeMin,omitempty"`
	RangeMax         int16  `json:"rangeMax,omitempty"`
	HasNormalBand    bool   `json:"hasNormalBand"`
	NormalMin        int16  `json:"normalMin,omitempty"`
	NormalMax        int16  `json:"normalMax,omitempty"`
	InNormalBand     bool   `json:"inNormalBand"`
	RecordsValue     bool   `json:"recordsValue"`
	RecordsRange     bool   `json:"recordsRange"`
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
