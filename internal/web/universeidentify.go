package web

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/patch"
	"benny512/internal/session"
)

const identifyLease = 5 * time.Second

type identifyStatus struct {
	Armed    bool   `json:"armed"`
	Running  bool   `json:"running"`
	Protocol string `json:"protocol"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Error    string `json:"error,omitempty"`
}

// No patch entries or shared output frames are reachable through this state.
// All fields and callbacks are protected by Server.identifyMu.
type universeIdentify struct {
	identifyStatus
	token    string
	deadline time.Time
	timer    session.Timer
	dmx      *session.DMXOutputEngine
	sacn     patch.SACNStream
	sends    int
	nextSend time.Time
}

// Identify owns output exclusively while armed. Serialize the check with
// commands that could start/write the other engines, including other browsers.
// Stops remain available and disarm Identify as well.
func (s *Server) identifyOutputGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		shared := r.Method == "POST" && (p == "/api/dmx" || p == "/api/dmx/start" || p == "/api/dmx/stop" ||
			strings.HasPrefix(p, "/api/patch/rigcheck/") || p == "/api/output/stop" || p == "/api/reset" || p == "/api/patch/workspace/rehearse")
		if !shared {
			next.ServeHTTP(w, r)
			return
		}
		s.identifyMu.Lock()
		defer s.identifyMu.Unlock()
		if s.identify.Armed {
			if strings.HasSuffix(p, "/stop") || strings.HasSuffix(p, "/blackout") || p == "/api/reset" || strings.HasSuffix(p, "/rehearse") {
				s.stopIdentifyLocked()
			} else {
				writeError(w, http.StatusConflict, fmt.Errorf("disarm Universe Identify before using manual Send or Rig Check"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleUniverseIdentifyStatus(w http.ResponseWriter, r *http.Request) {
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	// Reading status never renews another operator's lease or reveals its token.
	writeJSON(w, http.StatusOK, s.identify.identifyStatus)
}

func (s *Server) handleUniverseIdentifyArm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Protocol string `json:"protocol"`
		From     *int   `json:"from"`
		To       *int   `json:"to"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Explicit presence matters: omitted/null/empty scope must never mean all
	// patched universes, and omitted From must never select Art-Net zero.
	min := 0
	if req.Protocol == "sacn" {
		min = 1
	} else if req.Protocol != "artnet" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("protocol must be artnet or sacn"))
		return
	}
	if req.From == nil || req.To == nil || *req.From < min || *req.To > 512 || *req.From > *req.To {
		writeError(w, http.StatusBadRequest, fmt.Errorf("enter both ends of a protocol universe range from %d to 512, first <= last", min))
		return
	}
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	st := s.RigCheck.State()
	if s.identify.Armed || s.DMX.OutputRunning() || st.Running || st.PatternRunning {
		writeError(w, http.StatusConflict, fmt.Errorf("stop existing output and disarm Universe Identify before arming a new range"))
		return
	}
	if s.Simulation && req.Protocol == "sacn" {
		writeError(w, http.StatusConflict, fmt.Errorf("sACN Universe Identify is unavailable in rehearsal; no network output is opened"))
		return
	}
	s.identify = universeIdentify{
		identifyStatus: identifyStatus{Armed: true, Protocol: req.Protocol, From: *req.From, To: *req.To},
		token:          rand.Text(), deadline: s.DMX.Clock().Now().Add(identifyLease),
	}
	s.scheduleIdentifyLocked()
	writeJSON(w, http.StatusOK, struct {
		identifyStatus
		Token string `json:"token"`
	}{s.identify.identifyStatus, s.identify.token})
}

func (s *Server) identifyToken(w http.ResponseWriter, r *http.Request) bool {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	if s.identify.Armed && !s.DMX.Clock().Now().Before(s.identify.deadline) {
		s.stopIdentifyLocked()
	}
	if !s.identify.Armed || req.Token == "" || req.Token != s.identify.token {
		writeError(w, http.StatusConflict, fmt.Errorf("Universe Identify is disarmed or this arm has expired; arm the range again"))
		return false
	}
	return true
}

func identifyFrame(n int) []byte {
	f := make([]byte, 512)
	if n == 0 { // Explicit Art-Net 0: there is no DMX channel zero.
		for i := range f {
			f[i] = 255
		}
	} else {
		f[n-1] = 255
	}
	return f
}

func (s *Server) handleUniverseIdentifyStart(w http.ResponseWriter, r *http.Request) {
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	if !s.identifyToken(w, r) {
		return
	}
	i := &s.identify
	if !i.Running {
		if i.Protocol == "artnet" {
			i.dmx = s.DMX.NewIsolatedOutput()
			for n := i.From; n <= i.To; n++ {
				pa, _ := artnet.PortAddressFromRaw(uint16(n)) // validated 0..512
				i.dmx.StartUniverse(pa, netip.AddrPort{}, 512)
				_ = i.dmx.SetFrame(pa, identifyFrame(n))
			}
		} else {
			// Only reuse the NIC/priority/destination binding. Universe numbers
			// are raw sACN here: never apply the show's Art-Net/sACN offsets.
			stream, err := s.sacnBinding().Open()
			if err != nil {
				s.stopIdentifyLocked()
				i.Error = err.Error()
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			i.sacn = stream
		}
		i.Running = true
		if err := s.sendIdentifyLocked(); err != nil {
			s.stopIdentifyLocked()
			i.Error = err.Error()
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	i.deadline = s.DMX.Clock().Now().Add(identifyLease)
	writeJSON(w, http.StatusOK, i.identifyStatus)
}

func (s *Server) handleUniverseIdentifyHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	if !s.identifyToken(w, r) {
		return
	}
	s.identify.deadline = s.DMX.Clock().Now().Add(identifyLease)
	writeJSON(w, http.StatusOK, s.identify.identifyStatus)
}

func (s *Server) handleUniverseIdentifyStop(w http.ResponseWriter, r *http.Request) {
	// Any operator can stop. Stop also invalidates queued starts/heartbeats.
	s.stopUniverseIdentify()
	s.handleUniverseIdentifyStatus(w, r)
}

func (s *Server) stopUniverseIdentify() {
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	s.stopIdentifyLocked()
}

func (s *Server) stopIdentifyLocked() {
	i := &s.identify
	i.Armed, i.Running, i.token = false, false, ""
	if i.timer != nil {
		i.timer.Stop()
		i.timer = nil
	}
	var err error
	if i.dmx != nil {
		// This engine contains only the operator's explicit range. Never call
		// the shared DMX engine's Blackout/SendNow/Start from this tool.
		for k := 0; k < 3; k++ {
			i.dmx.Blackout()
		}
		err = i.dmx.LastSendError()
		i.dmx = nil
	}
	if i.sacn != nil {
		for n := i.From; n <= i.To; n++ {
			err = errors.Join(err, i.sacn.Stop(uint16(n)))
		}
		err = errors.Join(err, i.sacn.Close())
		i.sacn = nil
	}
	if err != nil {
		i.Error = err.Error()
	}
}

func (s *Server) sendIdentifyLocked() error {
	i := &s.identify
	if i.dmx != nil {
		i.dmx.SendNow()
		return i.dmx.LastSendError()
	}
	for n := i.From; n <= i.To; n++ {
		if err := i.sacn.Send(uint16(n), identifyFrame(n)); err != nil {
			return err
		}
	}
	i.sends++
	interval := patch.SACNBurstInterval
	if i.sends >= patch.SACNSuppressionBurst {
		interval = patch.SACNKeepAliveInterval
	}
	i.nextSend = s.DMX.Clock().Now().Add(interval)
	return nil
}

func (s *Server) scheduleIdentifyLocked() {
	token := s.identify.token
	s.identify.timer = s.DMX.Clock().AfterFunc(s.DMX.Interval(), func() {
		s.identifyMu.Lock()
		defer s.identifyMu.Unlock()
		i := &s.identify
		if !i.Armed || i.token != token {
			return
		}
		now := s.DMX.Clock().Now()
		if !now.Before(i.deadline) {
			s.stopIdentifyLocked()
			i.Error = "Browser heartbeat lost; Universe Identify disarmed."
			return
		}
		if i.Running && (i.Protocol == "artnet" || !now.Before(i.nextSend)) {
			if err := s.sendIdentifyLocked(); err != nil {
				s.stopIdentifyLocked()
				i.Error = err.Error()
				return
			}
		}
		s.scheduleIdentifyLocked()
	})
}
