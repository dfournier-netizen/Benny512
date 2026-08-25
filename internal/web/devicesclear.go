// This file implements POST /api/devices/clear: wiping the currently
// discovered-RDM-device cache, at either of two granularities Dom asked
// for — one port, or all ports. Deliberately narrow in scope (task ask:
// "keep your logging + responses for now"):
//
//	CLEARS:     internal/registry's fixture/device entries (cached PID
//	            values + classification) for the chosen scope, and
//	            internal/session's RDMController Table-of-Devices entries
//	            for the affected (IP, Port-Address) keys, so the next
//	            Discover repopulates from scratch instead of merging into a
//	            stale ToD.
//	DOES NOT:   touch the Art-Net node table, params' process-wide
//	            PARAMETER_DESCRIPTION cache, per-UID introspection state,
//	            the capture rings, the RDM disk logger, the patch, the walk
//	            session, or settings — every one of those is either "logging
//	            + responses" Dom explicitly wants kept, or state this
//	            endpoint has no business touching. The destructive
//	            everything-including-the-patch clear is POST /api/reset
//	            (see reset.go), a different, much more consequential action.
package web

import (
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"benny512/internal/artnet"
)

// devicesClearRequest is POST /api/devices/clear's body. Scope selects the
// granularity: "all" wipes every discovered device on every port; "port"
// wipes just the one (Ip, PortAddress) pair — note NOT BindIndex, matching
// how session.RDMController's Table of Devices and registry.Registry's new
// ClearDevicesOnPort are both keyed (a Port-Address is globally
// unambiguous on its own; see todKey's doc comment in
// internal/session/rdmdiscovery.go).
type devicesClearRequest struct {
	Scope       string `json:"scope"` // "all" | "port"
	IP          string `json:"ip,omitempty"`
	PortAddress uint16 `json:"portAddress,omitempty"`
}

// devicesClearResponse echoes the resolved scope plus how much was
// actually removed: Cleared counts registry device entries, TodCleared
// counts (ip,port) Table-of-Devices tables dropped ("all" scope can drop
// more than one; "port" scope drops at most one).
type devicesClearResponse struct {
	Scope       string `json:"scope"`
	IP          string `json:"ip,omitempty"`
	PortAddress uint16 `json:"portAddress,omitempty"`
	Cleared     int    `json:"cleared"`
	TodCleared  int    `json:"todCleared"`
}

func (s *Server) handleDevicesClear(w http.ResponseWriter, r *http.Request) {
	var req devicesClearRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	switch req.Scope {
	case "all":
		cleared := s.Registry.ClearDevices()
		todCleared := s.RDM.ClearToD()
		s.broadcastDevicesCleared("all", cleared)
		writeJSON(w, http.StatusOK, devicesClearResponse{Scope: "all", Cleared: cleared, TodCleared: todCleared})

	case "port":
		ip, err := netip.ParseAddr(req.IP)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("bad ip %q: %w", req.IP, err))
			return
		}
		pa, err := artnet.PortAddressFromRaw(req.PortAddress)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cleared := s.Registry.ClearDevicesOnPort(ip, pa)
		todCleared := s.RDM.ClearToDPort(ip, pa)
		s.broadcastDevicesCleared("port", cleared)
		writeJSON(w, http.StatusOK, devicesClearResponse{
			Scope: "port", IP: ip.String(), PortAddress: req.PortAddress,
			Cleared: cleared, TodCleared: todCleared,
		})

	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown scope %q, want \"all\" or \"port\"", req.Scope))
	}
}

// broadcastDevicesCleared pushes a "devices_cleared" event to every open
// browser (task ask) so other tabs/devices refresh their device list rather
// than showing now-stale cached entries until their next poll.
func (s *Server) broadcastDevicesCleared(scope string, cleared int) {
	s.hub.broadcast(wsMessage{Type: "devices_cleared", At: time.Now(), Scope: scope, Cleared: &cleared})
}
