package web

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"time"

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
//
// Since C3 Universe Identify is a source of the unified output engine, not an
// engine of its own: Start hands the engine one frame per wire stream in the
// range (session.DMXOutputEngine.SetIdentify), which owns each of those
// streams exclusively while it runs — every other universe keeps whatever
// Send, Rig Check or the programmer put there, so Identify no longer refuses
// to arm while other output runs, and other output no longer gets a 409 while
// Identify is armed. Nothing reaches the wire unless the master output is
// armed. The tool keeps its own range arm, token and 5 s lease: that lease
// belongs to the page that started it, and when it lapses Identify ends and
// its streams fall back to the composed show (or leave the wire with zeros).
type universeIdentify struct {
	identifyStatus
	token    string
	deadline time.Time
	timer    session.Timer
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
	if s.identify.Armed {
		writeError(w, http.StatusConflict, fmt.Errorf("turn Universe Identify off before setting a new range"))
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
		// Raw protocol universe numbers: never the show's Art-Net/sACN
		// offsets. Validated at arm: Art-Net 0..512, sACN 1..512.
		proto := session.WireArtNet
		if i.Protocol == "sacn" {
			proto = session.WireSACN
		}
		frames := make(map[session.Stream][]byte, i.To-i.From+1)
		for n := i.From; n <= i.To; n++ {
			frames[session.Stream{Protocol: proto, Universe: uint16(n)}] = identifyFrame(n)
		}
		if err := s.DMX.SetIdentify(frames); err != nil {
			s.stopIdentifyLocked()
			i.Error = err.Error()
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		i.Running = true
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
	wasRunning := i.Running
	i.Armed, i.Running, i.token = false, false, ""
	if i.timer != nil {
		i.timer.Stop()
		i.timer = nil
	}
	if wasRunning {
		// The engine retires each identified stream: back to the composed
		// show universe if a source drives it, otherwise three zero frames
		// (Art-Net) or zero frames and Stream_Terminated (sACN).
		s.DMX.ClearIdentify()
	}
}

// identifyLeaseCheck is how often an armed Identify checks its own lease.
const identifyLeaseCheck = 100 * time.Millisecond

func (s *Server) scheduleIdentifyLocked() {
	token := s.identify.token
	s.identify.timer = s.DMX.Clock().AfterFunc(identifyLeaseCheck, func() {
		s.identifyMu.Lock()
		defer s.identifyMu.Unlock()
		i := &s.identify
		if !i.Armed || i.token != token {
			return
		}
		if !s.DMX.Clock().Now().Before(i.deadline) {
			s.stopIdentifyLocked()
			i.Error = "Browser heartbeat lost; Universe Identify turned off."
			return
		}
		s.scheduleIdentifyLocked()
	})
}
