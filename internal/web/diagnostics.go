package web

import "net/http"

// rdmDiagnosticsJSON is the small controller-health view used beside the
// Analyzer's RDM traffic list. These are lifetime counters, not packet-list
// filters: they answer whether ACK_TIMER deferrals are being collected
// successfully or falling back to costly original-request reissues.
type rdmDiagnosticsJSON struct {
	AckTimerCollects      uint64 `json:"ackTimerCollects"`
	AckTimerCollectHits   uint64 `json:"ackTimerCollectHits"`
	AckTimerReissues      uint64 `json:"ackTimerReissues"`
	AckTimerTimeouts      uint64 `json:"ackTimerCollectTimeouts"`
	AckTimerCollectors    uint64 `json:"ackTimerCollectors"`
	ProxyBufferFull       uint64 `json:"proxyBufferFull"`
	QueuedMessagesDrained uint64 `json:"queuedMessagesDrained"`
	// MalformedRDM counts inbound ArtRdm datagrams whose RDM message did not
	// decode (a node-level fault; see nodeJSON.Fault). ForeignResponses
	// counts RDM responses addressed to some other controller's UID. Zero is
	// the healthy answer and stays on the wire.
	MalformedRDM     uint64 `json:"malformedRdm"`
	ForeignResponses uint64 `json:"foreignResponses"`
}

// handleRDMDiagnostics exposes controller counters without exposing mutable
// controller state. Meaningful zeros stay explicit: zero reissues is the
// healthy result and must never disappear from the wire.
func (s *Server) handleRDMDiagnostics(w http.ResponseWriter, r *http.Request) {
	st := s.RDM.Stats()
	writeJSON(w, http.StatusOK, rdmDiagnosticsJSON{
		AckTimerCollects: st.AckTimerCollects, AckTimerCollectHits: st.AckTimerCollectHits,
		AckTimerReissues: st.AckTimerReissues, AckTimerTimeouts: st.AckTimerCollectTimeouts,
		AckTimerCollectors: st.AckTimerCollectors, ProxyBufferFull: st.ProxyBufferFull,
		QueuedMessagesDrained: st.QueuedMessagesDrained,
		MalformedRDM:          st.MalformedRDM, ForeignResponses: st.ForeignResponses,
	})
}
