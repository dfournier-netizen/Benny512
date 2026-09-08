// This file adds the node/network configuration endpoints (ArtAddress/
// ArtIpProg/ArtInput, wired through session.ArtNetSession — see
// internal/session/nodeconfig.go for the confirmation-via-ArtPollReply
// model and internal/artnet/nodeconfig.go for this session's wire-format
// confidence notes on these three packets).
//
// Endpoint reference (UI agent):
//
//	POST /api/node/{ip}/address   <- nodeAddressRequestJSON   -> nodeConfigResultJSON
//	POST /api/node/{ip}/ipconfig  <- nodeIPConfigRequestJSON  -> nodeConfigResultJSON
//	POST /api/node/{ip}/input     <- nodeInputRequestJSON     -> nodeConfigResultJSON
//	GET  /api/nics                -> []nicJSON
package web

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"benny512/internal/artnet"
	"benny512/internal/session"
	"benny512/internal/transport"
)

// nodeConfigTimeout bounds one Set*/ProgramIP call, generously over
// session.DefaultConfigConfirmTimeout so the HTTP handler's own context
// deadline is never what cuts the wait short.
const nodeConfigTimeout = 6 * time.Second

func resolveNodeKey(r *http.Request, bindIndex byte) (session.NodeKey, error) {
	ip, err := netip.ParseAddr(r.PathValue("ip"))
	if err != nil {
		return session.NodeKey{}, fmt.Errorf("bad node IP: %w", err)
	}
	if bindIndex == 0 {
		bindIndex = 1
	}
	return session.NodeKey{IP: ip, BindIndex: bindIndex}, nil
}

type nodeConfigResultJSON struct {
	Node      string `json:"node"`
	Confirmed bool   `json:"confirmed"`
	ElapsedMS int64  `json:"elapsedMs"`
	Warning   string `json:"warning,omitempty"`
}

func toNodeConfigResultJSON(res session.ConfigResult) nodeConfigResultJSON {
	return nodeConfigResultJSON{
		Node: res.Node.IP.String(), Confirmed: res.Confirmed,
		ElapsedMS: res.Elapsed.Milliseconds(), Warning: res.Warning,
	}
}

func writeNodeConfigError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadGateway, err)
}

// acCommandByName maps the JSON "command" string to artnet.AcCommand.
// Empty/unrecognized names default to AcNone ("no extra action") rather
// than erroring, so a UI that only wants to set addressing doesn't have to
// pass a command field at all.
var acCommandByName = map[string]artnet.AcCommand{
	"":                artnet.AcNone,
	"none":            artnet.AcNone,
	"cancel_merge":    artnet.AcCancelMerge,
	"led_normal":      artnet.AcLedNormal,
	"led_mute":        artnet.AcLedMute,
	"led_locate":      artnet.AcLedLocate,
	"reset_rx_flags":  artnet.AcResetRxFlags,
	"merge_ltp_0":     artnet.AcMergeLTP0,
	"merge_ltp_1":     artnet.AcMergeLTP1,
	"merge_ltp_2":     artnet.AcMergeLTP2,
	"merge_ltp_3":     artnet.AcMergeLTP3,
	"direction_tx_0":  artnet.AcDirectionTx0,
	"direction_tx_1":  artnet.AcDirectionTx1,
	"direction_tx_2":  artnet.AcDirectionTx2,
	"direction_tx_3":  artnet.AcDirectionTx3,
	"direction_rx_0":  artnet.AcDirectionRx0,
	"direction_rx_1":  artnet.AcDirectionRx1,
	"direction_rx_2":  artnet.AcDirectionRx2,
	"direction_rx_3":  artnet.AcDirectionRx3,
	"merge_htp_0":     artnet.AcMergeHTP0,
	"merge_htp_1":     artnet.AcMergeHTP1,
	"merge_htp_2":     artnet.AcMergeHTP2,
	"merge_htp_3":     artnet.AcMergeHTP3,
	"artnet_select_0": artnet.AcArtNetSel0,
	"artnet_select_1": artnet.AcArtNetSel1,
	"artnet_select_2": artnet.AcArtNetSel2,
	"artnet_select_3": artnet.AcArtNetSel3,
	"acn_select_0":    artnet.AcAcnSel0,
	"acn_select_1":    artnet.AcAcnSel1,
	"acn_select_2":    artnet.AcAcnSel2,
	"acn_select_3":    artnet.AcAcnSel3,
	"clear_output_0":  artnet.AcClearOp0,
	"clear_output_1":  artnet.AcClearOp1,
	"clear_output_2":  artnet.AcClearOp2,
	"clear_output_3":  artnet.AcClearOp3,
	"rdm_enable_0":    artnet.AcRdmEnable0,
	"rdm_enable_1":    artnet.AcRdmEnable1,
	"rdm_enable_2":    artnet.AcRdmEnable2,
	"rdm_enable_3":    artnet.AcRdmEnable3,
	"rdm_disable_0":   artnet.AcRdmDisable0,
	"rdm_disable_1":   artnet.AcRdmDisable1,
	"rdm_disable_2":   artnet.AcRdmDisable2,
	"rdm_disable_3":   artnet.AcRdmDisable3,
}

// nodeAddressRequestJSON is POST /api/node/{ip}/address's body. Every
// pointer/optional field means "leave unchanged" when omitted/null —
// SwIn/SwOut entries are per-port DMX Universe values 0-15; NetSwitch is
// 0-127; SubSwitch is 0-15. ShortName/LongName, if provided (non-nil), are
// always sent (ArtAddress has no partial-name-update mechanism — see
// internal/artnet/nodeconfig.go).
type nodeAddressRequestJSON struct {
	BindIndex byte     `json:"bindIndex,omitempty"`
	ShortName *string  `json:"shortName,omitempty"`
	LongName  *string  `json:"longName,omitempty"`
	NetSwitch *byte    `json:"netSwitch,omitempty"`
	SubSwitch *byte    `json:"subSwitch,omitempty"`
	SwIn      [4]*byte `json:"swIn,omitempty"`
	SwOut     [4]*byte `json:"swOut,omitempty"`
	Command   string   `json:"command,omitempty"`
}

func (s *Server) handleNodeAddress(w http.ResponseWriter, r *http.Request) {
	var req nodeAddressRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	key, err := resolveNodeKey(r, req.BindIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cmd, ok := acCommandByName[req.Command]
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown command %q", req.Command))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), nodeConfigTimeout)
	defer cancel()

	// Names have no "leave unchanged" wire representation, so when the
	// caller wants a name change we go through SetPortAddresses with the
	// requested names threaded in (via a small extension below); when no
	// name change is requested, we still use SetPortAddresses, which
	// itself preserves the node's current advertised names.
	node, ok := s.Nodes.Node(key)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown node %s", key.IP))
		return
	}
	shortName, longName := node.ShortName, node.LongName
	if req.ShortName != nil {
		shortName = *req.ShortName
	}
	if req.LongName != nil {
		longName = *req.LongName
	}

	update := session.PortAddressUpdate{
		NetSwitch: req.NetSwitch, SubSwitch: req.SubSwitch,
		SwIn: req.SwIn, SwOut: req.SwOut, Command: cmd,
	}
	res, err := s.Nodes.SetPortAddressesAndNames(ctx, key, update, shortName, longName)
	if err != nil {
		writeNodeConfigError(w, err)
		return
	}
	out := toNodeConfigResultJSON(res)
	s.hub.broadcast(wsMessage{Type: "node_config", Kind: key.IP.String(), At: time.Now(), NodeConfig: &out})
	writeJSON(w, http.StatusOK, out)
}

type nodeIPConfigRequestJSON struct {
	BindIndex byte   `json:"bindIndex,omitempty"`
	IP        string `json:"ip,omitempty"`
	Mask      string `json:"mask,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	DHCP      bool   `json:"dhcp"`
}

func (s *Server) handleNodeIPConfig(w http.ResponseWriter, r *http.Request) {
	var req nodeIPConfigRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	key, err := resolveNodeKey(r, req.BindIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	parseOptionalAddr := func(s string) netip.Addr {
		if s == "" {
			return netip.Addr{}
		}
		a, _ := netip.ParseAddr(s)
		return a
	}
	ip := parseOptionalAddr(req.IP)
	mask := parseOptionalAddr(req.Mask)
	gateway := parseOptionalAddr(req.Gateway)
	if !req.DHCP && req.IP != "" && !ip.IsValid() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad ip %q", req.IP))
		return
	}
	if !req.DHCP && req.Mask != "" && !mask.IsValid() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("bad mask %q", req.Mask))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), nodeConfigTimeout)
	defer cancel()
	res, err := s.Nodes.ProgramIP(ctx, key, ip, mask, gateway, req.DHCP)
	if err != nil {
		writeNodeConfigError(w, err)
		return
	}
	out := toNodeConfigResultJSON(res)
	s.hub.broadcast(wsMessage{Type: "node_config", Kind: key.IP.String(), At: time.Now(), NodeConfig: &out})
	writeJSON(w, http.StatusOK, out)
}

type nodeInputRequestJSON struct {
	BindIndex byte    `json:"bindIndex,omitempty"`
	Enabled   [4]bool `json:"enabled"`
}

func (s *Server) handleNodeInput(w http.ResponseWriter, r *http.Request) {
	var req nodeInputRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	key, err := resolveNodeKey(r, req.BindIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeConfigTimeout)
	defer cancel()
	res, err := s.Nodes.SetInputEnabled(ctx, key, req.Enabled)
	if err != nil {
		writeNodeConfigError(w, err)
		return
	}
	out := toNodeConfigResultJSON(res)
	s.hub.broadcast(wsMessage{Type: "node_config", Kind: key.IP.String(), At: time.Now(), NodeConfig: &out})
	writeJSON(w, http.StatusOK, out)
}

// --- NICs (Phase 1c gap flagged in phase1c handoff notes) -------------------

type nicJSON struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	IPv4        []string `json:"ipv4"`
	Up          bool     `json:"up"`
	Loopback    bool     `json:"loopback"`
	Broadcast   bool     `json:"broadcast"`
}

func (s *Server) handleGetNICs(w http.ResponseWriter, r *http.Request) {
	ifs, err := transport.ListInterfaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]nicJSON, 0, len(ifs))
	for _, ifi := range ifs {
		out = append(out, nicJSON{
			Name: ifi.Name, DisplayName: ifi.DisplayName, IPv4: ifi.IPv4,
			Up: ifi.Up, Loopback: ifi.Loopback, Broadcast: ifi.Broadcast,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
