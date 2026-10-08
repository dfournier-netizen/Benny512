package web

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"benny512/internal/sacn"
	"benny512/internal/session"
)

// This file is the HTTP surface of the unified output engine (chunk C3,
// session.DMXOutputEngine): the master ARM / DISARM, the multi-browser
// lease, and the Output settings (lease-loss action, sACN adapter, and each
// universe's protocol) that configure the engine.
//
// THE LEASE RULE, end to end. Every open Benny512 page heartbeats
// POST /api/output/heartbeat once a second with a random per-page client id
// (workspace.js). The Arm stays alive while ANY of those browsers has been
// heard within 5 s. A page unload sends POST /api/output/goodbye with a
// keepalive request: the server forgets that browser at once, and if no
// OTHER browser has been heard within the window the lease is lost there and
// then — Blackout or Hold last look per Settings — instead of 5 s later.
// Leaving a screen inside the app is neither: it never stops output, because
// the master control in the strip, visible on every screen, is what governs
// output now.

// Output setting values. The wire vocabulary is the JSON below; ui.js and
// settings.js use exactly these strings.
const (
	protocolArtNet = "artnet"
	protocolSACN   = "sacn"
	protocolBoth   = "both"
)

// UniverseProtocol is one row of Settings.UniverseProtocols: the protocol(s)
// one show universe goes out on. Universe is the raw Art-Net Port-Address,
// like every stored universe.
type UniverseProtocol struct {
	Universe int    `json:"universe"`
	Protocol string `json:"protocol"`
}

func protocolsFor(name string) (session.OutputProtocols, bool) {
	switch name {
	case protocolArtNet:
		return session.OutputArtNet, true
	case protocolSACN:
		return session.OutputSACN, true
	case protocolBoth:
		return session.OutputBoth, true
	}
	return 0, false
}

func protocolName(p session.OutputProtocols) string {
	switch p {
	case session.OutputSACN:
		return protocolSACN
	case session.OutputBoth:
		return protocolBoth
	}
	return protocolArtNet
}

// normalizeOutputSettings fills the Output fields' defaults and refuses any
// value the engine does not know. An absent leaseLossAction (a client that
// predates C3, or a settings file written before it) is Blackout: the safe
// choice, and the owner-confirmed default. It never maps an unknown value.
func normalizeOutputSettings(s *Settings) error {
	switch s.LeaseLossAction {
	case "":
		s.LeaseLossAction = string(session.LeaseLossBlackout)
	case string(session.LeaseLossBlackout), string(session.LeaseLossHold):
	default:
		return fmt.Errorf("leaseLossAction must be %q or %q; %q is not an action Benny512 knows.", session.LeaseLossBlackout, session.LeaseLossHold, s.LeaseLossAction)
	}
	s.SACNNIC = strings.TrimSpace(s.SACNNIC)
	if s.UniverseProtocols == nil {
		s.UniverseProtocols = make([]UniverseProtocol, 0)
	}
	seen := map[int]bool{}
	for _, row := range s.UniverseProtocols {
		if row.Universe < 0 || row.Universe > 32767 {
			return fmt.Errorf("universeProtocols: every universe must be an Art-Net Port-Address in 0-32767; one row is outside that range.")
		}
		if _, ok := protocolsFor(row.Protocol); !ok {
			return fmt.Errorf("universeProtocols: a protocol must be %q, %q or %q; %q is not one of them.", protocolArtNet, protocolSACN, protocolBoth, row.Protocol)
		}
		if seen[row.Universe] {
			return fmt.Errorf("universeProtocols: a universe is listed twice; each universe takes one protocol choice.")
		}
		seen[row.Universe] = true
	}
	return nil
}

// applyOutputSettings pushes the Output settings into the engine. Called on
// construction, on every successful settings save and load, and on reset.
func (s *Server) applyOutputSettings(st Settings) {
	routing := make(map[uint16]session.OutputProtocols, len(st.UniverseProtocols))
	for _, row := range st.UniverseProtocols {
		if p, ok := protocolsFor(row.Protocol); ok && row.Universe >= 0 && row.Universe <= 32767 {
			routing[uint16(row.Universe)] = p
		}
	}
	s.DMX.SetLeaseLossAction(session.LeaseLossAction(st.LeaseLossAction))
	s.DMX.SetRouting(routing)
}

// --- sACN binding ---------------------------------------------------------------

// sacnAdapter resolves the adapter the sACN socket binds to: the Settings
// sACN adapter when one is chosen, otherwise the Art-Net adapter chosen at
// startup (OutputInterface/OutputBindIP — what sACN used before it had a
// setting of its own), otherwise the operating system's routing table.
func (s *Server) sacnAdapter() (*net.Interface, net.IP, string, error) {
	name := s.SettingsSnapshot().SACNNIC
	if name != "" {
		ifi, err := net.InterfaceByName(name)
		if err != nil {
			return nil, nil, "", fmt.Errorf("the sACN network adapter %q is not present on this machine", name)
		}
		return ifi, nil, describeAdapter(ifi), nil
	}
	if s.OutputInterface != nil {
		return s.OutputInterface, s.OutputBindIP, describeAdapter(s.OutputInterface) + " (same as Art-Net)", nil
	}
	return nil, s.OutputBindIP, "chosen by the operating system (same as Art-Net)", nil
}

func describeAdapter(ifi *net.Interface) string {
	addrs, _ := ifi.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return fmt.Sprintf("%s (%s)", ifi.Name, n.IP.To4())
		}
	}
	return ifi.Name
}

// engineSACNBinding is the session.SACNBinding this Server hands the engine.
// Both closures read the CURRENT settings each time they run, so an sACN
// settings change takes effect at the next Arm (when the socket is opened).
func (s *Server) engineSACNBinding() session.SACNBinding {
	return session.SACNBinding{
		Open: func() (session.SACNLink, session.SACNParams, error) {
			cfg := s.SACNSettings.Get()
			params := session.SACNParams{CID: cfg.CID, Priority: byte(cfg.Priority), Port: uint16(s.sacnPort)}
			var unicast net.IP
			if cfg.UnicastTo != "" {
				unicast = net.ParseIP(cfg.UnicastTo)
				if a, ok := netip.AddrFromSlice(unicast.To4()); ok {
					params.UnicastTo = a
				}
			}
			if s.Simulation {
				// Rehearsal and --demo never open a socket: their sACN goes
				// to an in-memory link, exactly as their Art-Net goes to a
				// FakeTransport. (Before C3 a demo Rig Check over sACN opened
				// a real multicast socket on whatever adapter the OS chose.)
				params.Adapter = "simulated (no network)"
				return s.simSACN, params, nil
			}
			ifi, bindIP, desc, err := s.sacnAdapter()
			if err != nil {
				return nil, session.SACNParams{}, err
			}
			link, err := sacn.OpenLink(sacn.Config{Interface: ifi, LocalIP: bindIP, UnicastTo: unicast})
			if err != nil {
				return nil, session.SACNParams{}, err
			}
			params.Adapter = desc
			return link, params, nil
		},
		Universe: func(raw uint16) (uint16, error) {
			return sacn.ArtnetPortAddressToSACNUniverse(raw, s.SettingsSnapshot().ArtnetStartUniverse, s.SACNSettings.Get().StartUniverse)
		},
	}
}

// simSACNLink is the in-memory sACN link a simulated server uses.
type simSACNLink struct {
	mu   sync.Mutex
	sent int
}

func (l *simSACNLink) Send(data []byte, dst netip.AddrPort) error {
	l.mu.Lock()
	l.sent++
	l.mu.Unlock()
	return nil
}

func (l *simSACNLink) Close() error { return nil }

// --- HTTP -------------------------------------------------------------------------

type outputUniverseJSON struct {
	Universe int    `json:"universe"`
	Protocol string `json:"protocol"`
	// SACNUniverse is the sACN universe this show universe maps to at the
	// current start universes, or null when it has none.
	SACNUniverse *int `json:"sacnUniverse"`
	// SACNMappable is false when the universe has no sACN universe; a
	// universe routed to sACN then goes out on nothing but Art-Net (if also
	// routed there) and the output reports an error.
	SACNMappable bool `json:"sacnMappable"`
}

type outputStatusJSON struct {
	State            string                 `json:"state"`
	Simulated        bool                   `json:"simulated"`
	LeaseLossAction  string                 `json:"leaseLossAction"`
	LeaseMS          int64                  `json:"leaseMs"`
	LeaseRemainingMS int64                  `json:"leaseRemainingMs"`
	Browsers         int                    `json:"browsers"`
	Error            string                 `json:"error"`
	LastDisarm       string                 `json:"lastDisarm"`
	SACNAdapter      string                 `json:"sacnAdapter"`
	Streams          []session.StreamStatus `json:"streams"`
	Universes        []outputUniverseJSON   `json:"universes"`
}

func (s *Server) outputStatus() outputStatusJSON {
	st := s.DMX.Status()
	adapter := st.SACNAdapter
	if adapter == "" {
		if s.Simulation {
			adapter = "simulated (no network)"
		} else if _, _, desc, err := s.sacnAdapter(); err == nil {
			adapter = desc
		} else {
			adapter = err.Error()
		}
	}
	out := outputStatusJSON{
		State: string(st.State), Simulated: st.Simulated, LeaseLossAction: string(st.LeaseLossAction),
		LeaseMS: session.DefaultLease.Milliseconds(), LeaseRemainingMS: st.LeaseRemaining.Milliseconds(),
		Browsers: st.Browsers, Error: st.Error, LastDisarm: st.LastDisarm, SACNAdapter: adapter,
		Streams: st.Streams, Universes: make([]outputUniverseJSON, 0),
	}
	// The universes worth showing: every one the active show patches, every
	// one with a protocol row, and every one on the wire right now.
	set := map[int]string{}
	if p, ok := s.PatchStore.Get(); ok {
		for _, e := range p.Entries {
			set[int(e.Universe)] = protocolArtNet
		}
	}
	settings := s.SettingsSnapshot()
	for _, row := range settings.UniverseProtocols {
		set[row.Universe] = row.Protocol
	}
	for _, u := range s.DMX.Universes() {
		if _, ok := set[int(u.RawValue())]; !ok {
			set[int(u.RawValue())] = protocolArtNet
		}
	}
	keys := make([]int, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	sacnStart := s.SACNSettings.Get().StartUniverse
	for _, k := range keys {
		row := outputUniverseJSON{Universe: k, Protocol: set[k]}
		if su, err := sacn.ArtnetPortAddressToSACNUniverse(uint16(k), settings.ArtnetStartUniverse, sacnStart); err == nil {
			v := int(su)
			row.SACNUniverse, row.SACNMappable = &v, true
		}
		out.Universes = append(out.Universes, row)
	}
	return out
}

// outputClientRequest is the body of every /api/output action: the
// per-page client id the browser heartbeats with. The body may be empty for
// arm/disarm (an API caller with no page); heartbeat and goodbye need an id.
type outputClientRequest struct {
	Client string `json:"client"`
}

func decodeOutputClient(r *http.Request, required bool) (string, error) {
	var req outputClientRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(req.Client) > 64 {
		return "", fmt.Errorf("client must be an id of at most 64 characters.")
	}
	if required && req.Client == "" {
		return "", fmt.Errorf("client is required: each browser heartbeats with its own id.")
	}
	return req.Client, nil
}

func (s *Server) handleGetOutput(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.outputStatus())
}

func (s *Server) handleOutputArm(w http.ResponseWriter, r *http.Request) {
	client, err := decodeOutputClient(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.DMX.Arm(client)
	writeJSON(w, http.StatusOK, s.outputStatus())
}

func (s *Server) handleOutputDisarm(w http.ResponseWriter, r *http.Request) {
	if _, err := decodeOutputClient(r, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.DMX.Disarm()
	writeJSON(w, http.StatusOK, s.outputStatus())
}

func (s *Server) handleOutputHeartbeat(w http.ResponseWriter, r *http.Request) {
	client, err := decodeOutputClient(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.DMX.Heartbeat(client)
	writeJSON(w, http.StatusOK, s.outputStatus())
}

func (s *Server) handleOutputGoodbye(w http.ResponseWriter, r *http.Request) {
	client, err := decodeOutputClient(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.DMX.Goodbye(client)
	writeJSON(w, http.StatusOK, s.outputStatus())
}
