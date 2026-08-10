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

// Settings is the mutable, JSON-persisted runtime configuration exposed via
// GET/POST /api/settings.
type Settings struct {
	NIC             string            `json:"nic"`
	PollIntervalMS  int               `json:"pollIntervalMs"`
	CaptureLimit    int               `json:"captureLimit"`
	TimeoutProfiles map[string]string `json:"timeoutProfiles"` // node key string -> "Direct"|"WirelessProxy"
}

// Server bundles the engines and serves REST + WS + the embedded UI.
type Server struct {
	Nodes    *session.ArtNetSession
	RDM      *session.RDMController
	DMX      *session.DMXOutputEngine
	Registry *registry.Registry
	Capture  *capture.Ring

	settingsMu sync.Mutex
	settings   Settings

	hub *hub

	mux *http.ServeMux
}

// New wires a Server over already-constructed engines.
func New(nodes *session.ArtNetSession, rdmc *session.RDMController, dmx *session.DMXOutputEngine, reg *registry.Registry, cap *capture.Ring) *Server {
	s := &Server{
		Nodes: nodes, RDM: rdmc, DMX: dmx, Registry: reg, Capture: cap,
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
}

func toFixtureJSON(f registry.Fixture) fixtureJSON {
	return fixtureJSON{
		UID: f.UID.String(), ManufacturerID: f.ManufacturerID, ManufacturerName: f.ManufacturerName,
		NodeIP: f.Node.IP.String(), BindIndex: f.Node.BindIndex, PortAddress: f.Port.RawValue(),
		LastSeen: f.LastSeen,
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
	s.settings = req
	s.settingsMu.Unlock()
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

// --- node/RDM event -> WS pump ------------------------------------------------

func (s *Server) pumpNodeEvents(ctx context.Context) {
	events := s.Nodes.Events()
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
	events := s.RDM.Events()
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

type wsMessage struct {
	Type   string          `json:"type"` // "node" | "rdm" | "capture" | "dmxTick"
	Kind   string          `json:"kind,omitempty"`
	At     time.Time       `json:"at"`
	Node   nodeJSON        `json:"node,omitempty"`
	Result *resultJSON     `json:"result,omitempty"`
	ToD    *todJSON        `json:"tod,omitempty"`
	Batch  []capture.Entry `json:"batch,omitempty"`
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

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
		}
	}
}
