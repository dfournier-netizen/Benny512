// Package web is Benny512's HTTP+WebSocket layer: REST command endpoints,
// a WebSocket hub streaming live events, and the embedded browser UI.
//
// Layering rule: this package is the only place that knows about HTTP. It
// drives the session engines (ArtNetSession, RDMController,
// DMXOutputEngine), the registry, params helpers and the capture ring —
// none of which import this package, keeping the dependency direction
// one-way (architecture rev 5 §2).
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/capture"
	"benny512/internal/params"
	"benny512/internal/rdm"
	"benny512/internal/registry"
	"benny512/internal/session"
	"benny512/internal/web/ws"
)

//go:embed all:static
var embeddedUI embed.FS

// DefaultPort is Benny512's default HTTP port (architecture rev 5 §3, decision #12).
const DefaultPort = 5812

// AppVersion is reported in capture export headers so an exported file
// self-identifies which build produced it (report task item 3: "Include a
// header with app version, timestamp, NIC, and node/device context").
const AppVersion = "benny512 phase1d"

// Settings is the mutable, JSON-persisted runtime configuration exposed via
// GET/POST /api/settings.
type Settings struct {
	NIC             string            `json:"nic"`
	PollIntervalMS  int               `json:"pollIntervalMs"`
	CaptureLimit    int               `json:"captureLimit"`
	TimeoutProfiles map[string]string `json:"timeoutProfiles"` // node key string -> "Direct"|"WirelessProxy"
	// LogRDMPath, when non-empty, appends every RDM/ToD exchange to this
	// file as it happens (report task item 3's optional continuous-logging
	// ask) — a Settings-screen alternative to the `--logrdm` startup flag.
	// Changing it via POST /api/settings opens/closes the on-disk logger
	// immediately (see Server.applyLogRDMPathLocked).
	LogRDMPath string `json:"logRdmPath,omitempty"`
}

// Server bundles the engines and serves REST + WS + the embedded UI.
type Server struct {
	Nodes    *session.ArtNetSession
	RDM      *session.RDMController
	DMX      *session.DMXOutputEngine
	Registry *registry.Registry
	// Capture is the general-purpose ring: every decoded packet, all kinds,
	// bounded at capture.DefaultCapacity so high-rate ArtDmx traffic doesn't
	// grow memory without bound — it's what the Analyzer's live/general view
	// and GET /api/capture/snapshot serve from.
	Capture *capture.Ring
	// RDMCapture is a dedicated, much larger ring holding only RDM/ToD
	// entries (capture.IsRDMKind) — so a bench session's RDM history is
	// never evicted by DMX chatter in the shared ring above. This is what
	// the RDM-focused Analyzer view and GET /api/capture/export serve from.
	RDMCapture *capture.Ring

	// NIC is a human-readable label for the network interface this server
	// is bound to (or "demo (fake transport)" in --demo mode) — surfaced in
	// capture export headers. Set once after construction; read
	// concurrently, so it must not be mutated after startup.
	NIC string

	settingsMu sync.Mutex
	settings   Settings
	rdmLogger  *capture.DiskLogger

	hub *hub

	mux *http.ServeMux
}

// New wires a Server over already-constructed engines. rdmCap is the
// dedicated RDM-only capture ring (see Server.RDMCapture); pass
// capture.New(capture.DefaultRDMCapacity) if the caller has no reason to
// size it differently.
func New(nodes *session.ArtNetSession, rdmc *session.RDMController, dmx *session.DMXOutputEngine, reg *registry.Registry, cap *capture.Ring, rdmCap *capture.Ring) *Server {
	s := &Server{
		Nodes: nodes, RDM: rdmc, DMX: dmx, Registry: reg, Capture: cap, RDMCapture: rdmCap,
		settings: Settings{
			PollIntervalMS:  int(session.DefaultPollInterval / time.Millisecond),
			CaptureLimit:    capture.DefaultCapacity,
			TimeoutProfiles: map[string]string{},
		},
		hub: newHub(),
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Close releases any resources the server opened directly (currently just
// an active RDM disk logger, if one was configured via --logrdm or
// Settings). Safe to call even if nothing was ever opened.
func (s *Server) Close() {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.rdmLogger != nil {
		_ = s.rdmLogger.Close()
		s.rdmLogger = nil
	}
}

// LogRDMEntry appends e to the configured RDM disk logger, if one is open
// (report task: "appends RDM exchanges to a file as they happen"). No-op
// when no --logrdm path / Settings.LogRDMPath is configured. Safe to call
// from the capture tap on every RDM/ToD entry regardless of direction.
func (s *Server) LogRDMEntry(e capture.Entry) {
	s.settingsMu.Lock()
	l := s.rdmLogger
	s.settingsMu.Unlock()
	if l != nil {
		l.Log(e)
	}
}

// SetLogRDMPath opens (or closes, if path=="") the continuous RDM disk
// logger. Used both by cmd/benny512's --logrdm flag at startup and by
// POST /api/settings when Settings.LogRDMPath changes.
func (s *Server) SetLogRDMPath(path string) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.applyLogRDMPathLocked(path)
}

func (s *Server) applyLogRDMPathLocked(path string) error {
	if s.rdmLogger != nil {
		_ = s.rdmLogger.Close()
		s.rdmLogger = nil
	}
	s.settings.LogRDMPath = path
	if path == "" {
		return nil
	}
	l, err := capture.OpenDiskLogger(path, 0, 0)
	if err != nil {
		return err
	}
	s.rdmLogger = l
	return nil
}

// Handler returns the http.Handler to serve (routes + static UI).
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts the WS hub's broadcast pumps (capture batches, node/RDM
// events) and the capture ring's throttle ticker. Call once at startup;
// blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) {
	go s.pumpNodeEvents(ctx)
	go s.pumpRDMEvents(ctx)
	go s.pumpCapture(ctx)
	<-ctx.Done()
}

func (s *Server) routes() {
	static, err := fs.Sub(embeddedUI, "static")
	if err != nil {
		log.Fatalf("web: embedded UI missing: %v", err)
	}
	fileServer := http.FileServer(http.FS(static))
	s.mux.Handle("/", fileServer)

	s.mux.HandleFunc("GET /api/nodes", s.handleGetNodes)
	s.mux.HandleFunc("GET /api/fixtures", s.handleGetFixtures)
	s.mux.HandleFunc("POST /api/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/fixture/{uid}/param/{pid}", s.handleGetParam)
	s.mux.HandleFunc("POST /api/fixture/{uid}/param/{pid}", s.handleSetParam)
	s.mux.HandleFunc("POST /api/identify", s.handleIdentify)
	s.mux.HandleFunc("POST /api/dmx", s.handleDMX)
	s.mux.HandleFunc("POST /api/dmx/start", s.handleDMXStart)
	s.mux.HandleFunc("POST /api/dmx/stop", s.handleDMXStop)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("POST /api/settings", s.handlePostSettings)
	s.mux.HandleFunc("GET /api/capture/snapshot", s.handleCaptureSnapshot)
	s.mux.HandleFunc("GET /api/capture/rdm/snapshot", s.handleRDMCaptureSnapshot)
	s.mux.HandleFunc("GET /api/capture/export", s.handleCaptureExport)

	// --- Phase 1c+: self-describing PIDs, sensors, device status ---
	s.mux.HandleFunc("GET /api/device/{uid}/params", s.handleGetDeviceParams)
	s.mux.HandleFunc("GET /api/device/{uid}/param/{pid}", s.handleGetDeviceParam)
	s.mux.HandleFunc("POST /api/device/{uid}/param/{pid}", s.handleSetDeviceParam)
	s.mux.HandleFunc("POST /api/device/{uid}/introspect", s.handleIntrospectDevice)
	s.mux.HandleFunc("GET /api/device/{uid}/sensors", s.handleGetDeviceSensors)
	s.mux.HandleFunc("POST /api/device/{uid}/sensors/record", s.handleRecordDeviceSensors)
	s.mux.HandleFunc("POST /api/device/{uid}/sensors/reset", s.handleResetDeviceSensors)
	s.mux.HandleFunc("GET /api/device/{uid}/status", s.handleGetDeviceStatus)

	// --- Phase 1c+: node/network configuration ---
	s.mux.HandleFunc("POST /api/node/{ip}/address", s.handleNodeAddress)
	s.mux.HandleFunc("POST /api/node/{ip}/ipconfig", s.handleNodeIPConfig)
	s.mux.HandleFunc("POST /api/node/{ip}/input", s.handleNodeInput)
	s.mux.HandleFunc("GET /api/nics", s.handleGetNICs)

	s.mux.HandleFunc("GET /ws", s.handleWS)
}

// --- JSON helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- Nodes -------------------------------------------------------------------

type nodePortJSON struct {
	Index         int    `json:"index"`
	Input         bool   `json:"input"`
	Output        bool   `json:"output"`
	InputAddress  uint16 `json:"inputAddress"`
	OutputAddress uint16 `json:"outputAddress"`
	RDMEnabled    bool   `json:"rdmEnabled"`
}

type nodeJSON struct {
	IP           string         `json:"ip"`
	BindIndex    byte           `json:"bindIndex"`
	ShortName    string         `json:"shortName"`
	LongName     string         `json:"longName"`
	Style        string         `json:"style"`
	RDMCapable   bool           `json:"rdmCapable"`
	Stale        bool           `json:"stale"`
	LastSeen     time.Time      `json:"lastSeen"`
	Ports        []nodePortJSON `json:"ports"`
	FixtureCount int            `json:"fixtureCount"`
}

func toNodeJSON(n registry.NodeView) nodeJSON {
	out := nodeJSON{
		IP: n.Key.IP.String(), BindIndex: n.Key.BindIndex,
		ShortName: n.ShortName, LongName: n.LongName, Style: n.Style.String(),
		RDMCapable: n.RDMCapable, Stale: n.Stale, LastSeen: n.LastSeen,
		FixtureCount: n.FixtureCount,
	}
	for _, p := range n.Ports {
		out.Ports = append(out.Ports, nodePortJSON{
			Index: p.Index, Input: p.Input, Output: p.Output,
			InputAddress: p.InputAddress.RawValue(), OutputAddress: p.OutputAddress.RawValue(),
			RDMEnabled: p.RDMEnabled,
		})
	}
	return out
}

func (s *Server) handleGetNodes(w http.ResponseWriter, r *http.Request) {
	nodes := s.Registry.Nodes()
	out := make([]nodeJSON, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toNodeJSON(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Fixtures ------------------------------------------------------------

type fixtureJSON struct {
	UID              string    `json:"uid"`
	ManufacturerID   uint16    `json:"manufacturerId"`
	ManufacturerName string    `json:"manufacturerName"`
	NodeIP           string    `json:"nodeIp"`
	BindIndex        byte      `json:"bindIndex"`
	PortAddress      uint16    `json:"portAddress"`
	LastSeen         time.Time `json:"lastSeen"`
	// Class/IsWirelessProxy surface registry.DeviceClass (report §1.3):
	// "Fixture" is the JSON default (ClassUnknown also renders as a string,
	// "Unknown", until DEVICE_INFO/PRODUCT_DETAIL_ID_LIST/
	// PROXIED_DEVICE_COUNT have actually been fetched at least once — the
	// UI should treat "Unknown" as "not yet classified", not as its own
	// category). See internal/registry/deviceclass.go for the full enum.
	Class           string `json:"class"`
	IsWirelessProxy bool   `json:"isWirelessProxy"`

	// Manufacturer/Model are the Devices screen's ready-to-render column
	// values, resolved server-side per the task's priority rule:
	// Manufacturer prefers the device's own MANUFACTURER_LABEL report, then
	// falls back to the static ESTA table (ManufacturerName), then
	// "Unknown (0xXXXX)" (ManufacturerName's own fallback — never blank).
	// Model prefers the device's own DEVICE_MODEL_DESCRIPTION report, then
	// falls back to DEVICE_INFO's numeric Device Model ID in hex, then "—"
	// (never blank). See effectiveManufacturer/effectiveModel below.
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	// ManufacturerLabel/ModelDescription are the raw device-reported
	// strings (may be empty — either not yet fetched or the device NACKed
	// the GET) plus a *Known flag so the UI can tell "still pending, show a
	// placeholder" apart from "resolved, device just doesn't report this"
	// without polling forever. DeviceModelID/HasDeviceInfo back Model's
	// numeric fallback.
	ManufacturerLabel      string `json:"manufacturerLabel,omitempty"`
	ManufacturerLabelKnown bool   `json:"manufacturerLabelKnown"`
	ModelDescription       string `json:"modelDescription,omitempty"`
	ModelDescriptionKnown  bool   `json:"modelDescriptionKnown"`
	DeviceModelID          uint16 `json:"deviceModelId,omitempty"`
	HasDeviceInfo          bool   `json:"hasDeviceInfo"`
}

// effectiveManufacturer implements the Manufacturer column's priority rule:
// device's own MANUFACTURER_LABEL report first, then the static ESTA-table
// lookup (registry.ManufacturerName, already computed into
// f.ManufacturerName and itself falling back to "Unknown (0xXXXX)") — so
// this never returns "".
func effectiveManufacturer(f registry.Fixture) string {
	if f.ManufacturerLabel != "" {
		return f.ManufacturerLabel
	}
	return f.ManufacturerName
}

// effectiveModel implements the Model column's priority rule: device's own
// DEVICE_MODEL_DESCRIPTION report first, then DEVICE_INFO's numeric Device
// Model ID in hex (once DEVICE_INFO has actually been fetched), then an
// em-dash — never "".
func effectiveModel(f registry.Fixture) string {
	if f.ModelDescription != "" {
		return f.ModelDescription
	}
	if f.HasDeviceInfo {
		return fmt.Sprintf("0x%04X", f.DeviceModelID)
	}
	return "—"
}

func toFixtureJSON(f registry.Fixture) fixtureJSON {
	return fixtureJSON{
		UID: f.UID.String(), ManufacturerID: f.ManufacturerID, ManufacturerName: f.ManufacturerName,
		NodeIP: f.Node.IP.String(), BindIndex: f.Node.BindIndex, PortAddress: f.Port.RawValue(),
		LastSeen: f.LastSeen, Class: f.Class.String(), IsWirelessProxy: f.IsWirelessProxy,
		Manufacturer: effectiveManufacturer(f), Model: effectiveModel(f),
		ManufacturerLabel: f.ManufacturerLabel, ManufacturerLabelKnown: f.ManufacturerLabelKnown,
		ModelDescription: f.ModelDescription, ModelDescriptionKnown: f.ModelDescriptionKnown,
		DeviceModelID: f.DeviceModelID, HasDeviceInfo: f.HasDeviceInfo,
	}
}

func (s *Server) handleGetFixtures(w http.ResponseWriter, r *http.Request) {
	fixtures := s.Registry.Fixtures(session.NodeKey{}, artnet.PortAddress{}, false)
	out := make([]fixtureJSON, 0, len(fixtures))
	for _, f := range fixtures {
		out = append(out, toFixtureJSON(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Discover --------------------------------------------------------------

type discoverRequest struct {
	Node        string `json:"node"` // IP
	BindIndex   byte   `json:"bindIndex"`
	PortAddress uint16 `json:"portAddress"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ip, err := netip.ParseAddr(req.Node)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad node IP: %w", err))
		return
	}
	bind := req.BindIndex
	if bind == 0 {
		bind = 1
	}
	key := session.NodeKey{IP: ip, BindIndex: bind}
	node, ok := s.Nodes.Node(key)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown node %s", req.Node))
		return
	}
	pa, err := artnet.PortAddressFromRaw(req.PortAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ref := session.NodeRef{Key: key, Addr: node.Addr, Port: pa}
	disc := s.RDM.Discover(ref)

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := disc.Await(ctx)
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, err)
		return
	}
	for _, uid := range res.UIDs {
		s.Registry.NoteFixture(ref, uid)
	}
	uids := make([]string, 0, len(res.UIDs))
	for _, u := range res.UIDs {
		uids = append(uids, u.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uids": uids, "complete": res.Complete, "blocks": res.Blocks,
		"elapsedMs": res.Elapsed.Milliseconds(),
	})
}

// --- Fixture params ----------------------------------------------------------

func (s *Server) resolveFixture(w http.ResponseWriter, r *http.Request) (*params.Client, rdm.UID, bool) {
	uid, ok := rdm.ParseUID(r.PathValue("uid"))
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad uid %q", r.PathValue("uid")))
		return nil, rdm.UID{}, false
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown fixture %s", uid))
		return nil, rdm.UID{}, false
	}
	return params.New(s.RDM, node, uid), uid, true
}

func (s *Server) handleGetParam(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveFixture(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var val any
	var err error
	switch r.PathValue("pid") {
	case "device_info":
		val, err = client.DeviceInfo(ctx)
	case "dmx_start_address":
		val, err = client.DMXStartAddress(ctx)
	case "dmx_personality":
		val, err = client.DMXPersonality(ctx)
	case "device_label":
		val, err = client.DeviceLabel(ctx)
	case "manufacturer_label":
		val, err = client.ManufacturerLabel(ctx)
	case "device_model_description":
		val, err = client.DeviceModelDescription(ctx)
	case "software_version_label":
		val, err = client.SoftwareVersionLabel(ctx)
	case "identify_device":
		val, err = client.IdentifyDevice(ctx)
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown param %q", r.PathValue("pid")))
		return
	}
	if err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": val})
}

type setParamRequest struct {
	Value json.RawMessage `json:"value"`
}

func (s *Server) handleSetParam(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveFixture(w, r)
	if !ok {
		return
	}
	var req setParamRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var err error
	switch r.PathValue("pid") {
	case "dmx_start_address":
		var addr uint16
		if jsonErr := json.Unmarshal(req.Value, &addr); jsonErr != nil {
			writeError(w, http.StatusBadRequest, jsonErr)
			return
		}
		err = client.SetDMXStartAddress(ctx, addr)
	case "dmx_personality":
		var idx byte
		if jsonErr := json.Unmarshal(req.Value, &idx); jsonErr != nil {
			writeError(w, http.StatusBadRequest, jsonErr)
			return
		}
		err = client.SetDMXPersonality(ctx, idx)
	case "device_label":
		var label string
		if jsonErr := json.Unmarshal(req.Value, &label); jsonErr != nil {
			writeError(w, http.StatusBadRequest, jsonErr)
			return
		}
		err = client.SetDeviceLabel(ctx, label)
	case "identify_device":
		var on bool
		if jsonErr := json.Unmarshal(req.Value, &on); jsonErr != nil {
			writeError(w, http.StatusBadRequest, jsonErr)
			return
		}
		err = client.SetIdentifyDevice(ctx, on)
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("param %q is not settable", r.PathValue("pid")))
		return
	}
	if err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeParamError(w http.ResponseWriter, err error) {
	var nack *session.NackError
	switch {
	case errors.As(err, &nack):
		writeError(w, http.StatusUnprocessableEntity, err)
	case errors.Is(err, session.ErrTimeout), errors.Is(err, session.ErrDeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// --- Identify ----------------------------------------------------------------

type identifyRequest struct {
	UID string `json:"uid"`
	On  bool   `json:"on"`
}

func (s *Server) handleIdentify(w http.ResponseWriter, r *http.Request) {
	var req identifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	uid, ok := rdm.ParseUID(req.UID)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad uid %q", req.UID))
		return
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown fixture %s", req.UID))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := params.New(s.RDM, node, uid)
	if err := client.SetIdentifyDevice(ctx, req.On); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- DMX send ------------------------------------------------------------

type dmxRequest struct {
	Universe uint16          `json:"universe"`
	Channels map[string]byte `json:"channels"`
}

func (s *Server) handleDMX(w http.ResponseWriter, r *http.Request) {
	var req dmxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pa, err := artnet.PortAddressFromRaw(req.Universe)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.DMX.StartUniverse(pa, netip.AddrPort{}, 512)
	for chStr, val := range req.Channels {
		var ch int
		if _, scanErr := fmt.Sscanf(chStr, "%d", &ch); scanErr != nil || ch < 1 || ch > 512 {
			continue
		}
		_ = s.DMX.SetChannels(pa, ch, []byte{val})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDMXStart(w http.ResponseWriter, r *http.Request) {
	s.DMX.Start()
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleDMXStop(w http.ResponseWriter, r *http.Request) {
	s.DMX.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// --- Settings ------------------------------------------------------------

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, s.settings)
}

func (s *Server) handlePostSettings(w http.ResponseWriter, r *http.Request) {
	var req Settings
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.settingsMu.Lock()
	logPathChanged := req.LogRDMPath != s.settings.LogRDMPath
	s.settings = req
	var logErr error
	if logPathChanged {
		logErr = s.applyLogRDMPathLocked(req.LogRDMPath)
	}
	s.settingsMu.Unlock()
	if logErr != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("logRdmPath: %w", logErr))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Settings returns a copy of the current settings (used by cmd/benny512 to
// read back the configured NIC on startup logging, etc).
func (s *Server) SettingsSnapshot() Settings {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.settings
}

// --- Capture ---------------------------------------------------------------

func (s *Server) handleCaptureSnapshot(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := capture.Filter{Kind: q.Get("kind")}
	if v := q.Get("universe"); v != "" {
		var u uint16
		if _, err := fmt.Sscanf(v, "%d", &u); err == nil {
			f.HasUniv = true
			f.Universe = u
		}
	}
	if v := q.Get("source"); v != "" {
		if addr, err := netip.ParseAddr(v); err == nil {
			f.HasSrc = true
			f.Source = addr
		}
	}
	entries := s.Capture.Snapshot(f, 1000)
	writeJSON(w, http.StatusOK, entries)
}

// captureFilterFromQuery parses the RDM-specific filter dimensions shared by
// the RDM snapshot and export endpoints: uid, pid (4 hex digits), cc
// (command class mnemonic, e.g. "GET_COMMAND"), dir ("in"|"out").
func captureFilterFromQuery(q url.Values) (capture.Filter, error) {
	f := capture.Filter{}
	if uid := q.Get("uid"); uid != "" {
		if _, ok := rdm.ParseUID(uid); !ok {
			return f, fmt.Errorf("bad uid %q", uid)
		}
		f.UID = uid
	}
	if pidStr := q.Get("pid"); pidStr != "" {
		v, err := strconv.ParseUint(pidStr, 16, 16)
		if err != nil {
			return f, fmt.Errorf("bad pid %q, want 4 hex digits: %w", pidStr, err)
		}
		f.HasPID = true
		f.PID = uint16(v)
	}
	if cc := q.Get("cc"); cc != "" {
		f.CommandClass = cc
	}
	switch q.Get("dir") {
	case "in":
		f.HasDir, f.Dir = true, capture.DirIn
	case "out":
		f.HasDir, f.Dir = true, capture.DirOut
	case "":
	default:
		return f, fmt.Errorf("bad dir %q, want in|out", q.Get("dir"))
	}
	return f, nil
}

// handleRDMCaptureSnapshot serves the Analyzer's RDM-focused view: the
// dedicated RDM-only ring (never DMX-evicted), filterable by uid/pid/cc/dir
// (report task item 3: "filter by UID, PID, command class, and direction").
func (s *Server) handleRDMCaptureSnapshot(w http.ResponseWriter, r *http.Request) {
	f, err := captureFilterFromQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit := 2000
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	entries := s.RDMCapture.Snapshot(f, limit)
	writeJSON(w, http.StatusOK, entries)
}

// --- node/RDM event -> WS pump ------------------------------------------------

// pumpNodeEvents/pumpRDMEvents read from Registry's fan-out republish
// channels (NodeEvents()/RDMEvents()), not the engines' own Events()
// directly. Registry.Run() is the sole true consumer of the engine
// channels; a second independent reader there would silently steal a
// random subset of events out from under Registry (Go channels split
// values across concurrent readers, they don't broadcast) — see the
// Registry doc comment in internal/registry/registry.go for the bug this
// fixed (found via the demo end-to-end test, where device classification
// went nondeterministic).
func (s *Server) pumpNodeEvents(ctx context.Context) {
	events := s.Registry.NodeEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			s.hub.broadcast(wsMessage{
				Type: "node", Kind: ev.Kind.String(), At: ev.At,
				Node: toNodeJSON(registry.NodeView{Node: ev.Node}),
			})
		}
	}
}

func (s *Server) pumpRDMEvents(ctx context.Context) {
	events := s.Registry.RDMEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			msg := wsMessage{Type: "rdm", Kind: ev.Kind.String(), At: ev.At}
			if ev.Result != nil {
				msg.Result = &resultJSON{
					Kind: ev.Result.Kind.String(), UID: ev.UID.String(),
					PID: uint16(ev.Result.Request.PID), Err: errString(ev.Result.Err),
				}
			}
			if ev.Kind == session.EventToDUpdate {
				uids := make([]string, 0, len(ev.UIDs))
				for _, u := range ev.UIDs {
					uids = append(uids, u.String())
				}
				msg.ToD = &todJSON{UIDs: uids, Complete: ev.Complete}
			}
			s.hub.broadcast(msg)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) pumpCapture(ctx context.Context) {
	ticker := time.NewTicker(capture.DefaultBatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Capture.Publish()
		}
	}
}

// --- WebSocket hub -----------------------------------------------------------

// wsMessage is the envelope for every server->client WebSocket push. Type
// selects which of the optional fields below is populated:
//
//	"node"                 — Node (an ArtNetSession NodeEvent)
//	"rdm"                  — Result and/or ToD (an RDMController Event)
//	"capture"              — Batch (a capture-ring publish tick)
//	"introspect_progress"  — Introspect (Kind carries the UID string)
//	"introspect_complete"  — Descriptors (and/or Err on failure; Kind carries the UID string)
//	"sensor_values"        — Sensors (Kind carries the UID string) — sent only to
//	                         connections subscribed to that UID (see handleWS)
//	"node_config"          — NodeConfig (Kind carries the node IP string) — a
//	                         SetPortAddresses/SetNodeNames/ProgramIP/SetInputEnabled result
type wsMessage struct {
	Type        string                  `json:"type"`
	Kind        string                  `json:"kind,omitempty"`
	At          time.Time               `json:"at"`
	Node        nodeJSON                `json:"node,omitempty"`
	Result      *resultJSON             `json:"result,omitempty"`
	ToD         *todJSON                `json:"tod,omitempty"`
	Batch       []capture.Entry         `json:"batch,omitempty"`
	Introspect  *introspectProgressJSON `json:"introspect,omitempty"`
	Descriptors []paramDescriptorJSON   `json:"descriptors,omitempty"`
	Sensors     []sensorReadingJSON     `json:"sensors,omitempty"`
	NodeConfig  *nodeConfigResultJSON   `json:"nodeConfig,omitempty"`
	Err         string                  `json:"err,omitempty"`
}

// introspectProgressJSON mirrors params.IntrospectProgress.
type introspectProgressJSON struct {
	UID   string `json:"uid"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	PID   string `json:"pid"`
}

type resultJSON struct {
	Kind string `json:"kind"`
	UID  string `json:"uid"`
	PID  uint16 `json:"pid"`
	Err  string `json:"err,omitempty"`
}

type todJSON struct {
	UIDs     []string `json:"uids"`
	Complete bool     `json:"complete"`
}

type hub struct {
	mu      sync.Mutex
	clients map[*ws.Conn]chan wsMessage
}

func newHub() *hub {
	return &hub{clients: make(map[*ws.Conn]chan wsMessage)}
}

func (h *hub) add(c *ws.Conn) chan wsMessage {
	ch := make(chan wsMessage, 64)
	h.mu.Lock()
	h.clients[c] = ch
	h.mu.Unlock()
	return ch
}

func (h *hub) remove(c *ws.Conn) {
	h.mu.Lock()
	if ch, ok := h.clients[c]; ok {
		close(ch)
		delete(h.clients, c)
	}
	h.mu.Unlock()
}

func (h *hub) broadcast(msg wsMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// Slow client: drop rather than block the broadcaster.
		}
	}
}

// sendTo delivers msg to one specific connection's channel, used for
// sensor-value pushes (report task: "poll live sensors... only for devices
// whose detail panel is open" — a per-connection subscription, not a
// global broadcast). Race-safe against a concurrent hub.remove: the lookup
// and the send both happen while holding h.mu, the same lock remove uses
// to close+delete the channel, so this can never send on an already-closed
// channel.
func (h *hub) sendTo(c *ws.Conn, msg wsMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.clients[c]
	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

// DefaultSensorPollInterval is how often a connection's subscribed devices'
// live sensor values are re-fetched (report task: "poll live sensors...
// default 2s").
const DefaultSensorPollInterval = 2 * time.Second

// wsClientMessage is an inbound client->server WS message: currently just
// the sensor-subscription control channel (report task: "expose subscribe/
// unsubscribe").
type wsClientMessage struct {
	Type string `json:"type"` // "subscribe_sensors" | "unsubscribe_sensors"
	UID  string `json:"uid"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ch := s.hub.add(conn)
	defer s.hub.remove(conn)
	defer conn.Close()

	// Feed the capture ring's batches into this client's own channel too.
	captureCh, cancelCapture := s.Capture.Subscribe(capture.Filter{})
	defer cancelCapture()

	var subsMu sync.Mutex
	subs := make(map[string]bool)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd wsClientMessage
			if jsonErr := json.Unmarshal(data, &cmd); jsonErr != nil {
				continue
			}
			uid, ok := rdmParseUIDForWS(cmd.UID)
			if !ok {
				continue
			}
			switch cmd.Type {
			case "subscribe_sensors":
				subsMu.Lock()
				subs[uid] = true
				subsMu.Unlock()
			case "unsubscribe_sensors":
				subsMu.Lock()
				delete(subs, uid)
				subsMu.Unlock()
			}
		}
	}()

	ticker := time.NewTicker(DefaultSensorPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(msg)
			if err := conn.WriteMessage(b); err != nil {
				return
			}
		case batch, ok := <-captureCh:
			if !ok {
				captureCh = nil
				continue
			}
			b, _ := json.Marshal(wsMessage{Type: "capture", At: time.Now(), Batch: batch})
			if err := conn.WriteMessage(b); err != nil {
				return
			}
		case <-ticker.C:
			subsMu.Lock()
			uids := make([]string, 0, len(subs))
			for u := range subs {
				uids = append(uids, u)
			}
			subsMu.Unlock()
			for _, u := range uids {
				go s.publishSensorValues(conn, u)
			}
		}
	}
}

// rdmParseUIDForWS validates and normalizes a client-supplied UID string
// (round-tripping through rdm.UID so a malformed subscribe request can't
// accumulate garbage keys in a connection's subscription set).
func rdmParseUIDForWS(s string) (string, bool) {
	uid, ok := rdm.ParseUID(s)
	if !ok {
		return "", false
	}
	return uid.String(), true
}

// publishSensorValues fetches live sensor values for uidStr and pushes them
// to conn via the hub's race-safe targeted send. Runs on its own goroutine
// per poll tick per subscribed device so one slow/unreachable device (e.g.
// a wireless proxy mid-retry) never delays delivery of other events to this
// connection — see hub.sendTo's doc comment for why this is safe against a
// concurrent disconnect.
func (s *Server) publishSensorValues(conn *ws.Conn, uidStr string) {
	uid, ok := rdm.ParseUID(uidStr)
	if !ok {
		return
	}
	node, ok := s.Registry.FixtureNode(uid)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := params.New(s.RDM, node, uid)
	readings, err := client.SensorValues(ctx)
	if err != nil {
		return
	}
	out := make([]sensorReadingJSON, 0, len(readings))
	for _, r := range readings {
		out = append(out, toSensorReadingJSON(r))
	}
	s.hub.sendTo(conn, wsMessage{Type: "sensor_values", Kind: uidStr, At: time.Now(), Sensors: out})
}
