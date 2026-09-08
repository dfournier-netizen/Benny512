// This file adds the HTTP surface for E1.37-2's IPv4 & DNS Configuration
// PIDs (internal/params/ipconfig.go), gated on SUPPORTED_PARAMETERS the same
// way device.go/devicecontrol.go gate every other optional feature — a
// device that doesn't advertise LIST_INTERFACES simply returns
// Supported:false, and the UI shows nothing for it (not an error).
//
// IMPORTANT — read internal/params/ipconfig.go's own doc comment before
// touching this file: its wire layout (interface-ID-prefixed GET/SET) is
// this app's best reading of the common E1.37-2 responder convention, NOT
// verified against ANSI/ESTA E1.37-2 primary text or a real device this
// session. Nothing in this file invents anything beyond what ipconfig.go
// already implements — INTERFACE_HARDWARE_ADDRESS_TYPE1, IPV4_ZEROCONF_
// MODE, IPV4_DEFAULT_ROUTE and writing DNS_NAME_SERVER have NO typed
// support anywhere in this app (no confirmed byte layout to build one on)
// and are deliberately absent here rather than guessed at.
//
// Endpoint reference (server.go's routes() is the authoritative wiring):
//
//	GET  /api/device/{uid}/network                          -> networkJSON (best-effort; Supported:false when LIST_INTERFACES isn't advertised)
//	POST /api/device/{uid}/network/interface/{id}/static     <- networkStaticRequestJSON <- {"confirm":"CONFIRM"} -> networkActionResponseJSON  (destructive; can strand the device off the show network)
//	POST /api/device/{uid}/network/interface/{id}/dhcp       <- networkDHCPRequestJSON   <- {"confirm":"CONFIRM"} -> networkActionResponseJSON  (destructive; can strand the device off the show network)
//	POST /api/device/{uid}/network/dns                       <- networkDNSRequestJSON    <- {"confirm":"CONFIRM"} -> networkActionResponseJSON
package web

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"benny512/internal/params"
	"benny512/internal/rdm"
)

// networkConfirm is the fixed confirm-string wire contract every
// state-changing endpoint in this file requires — same discipline as
// devicecontrol.go's deviceControlConfirm ("CONFIRM", distinct from
// device.go/reset.go's "RESET", since this isn't a factory-reset-class
// action but still one the UI's own arm-then-confirm gate must not be the
// only thing standing between a stray request and a stranded device).
const networkConfirm = "CONFIRM"

// ErrNetworkConfirmationRequired is returned (as a 400) when a network
// config endpoint is called without the exact confirm string.
var ErrNetworkConfirmationRequired = errors.New("confirmation required")

type networkInterfaceJSON struct {
	ID uint32 `json:"id"`

	// HardwareType is LIST_INTERFACES' 16-bit Interface Hardware Type (IANA
	// ARP-PARAMETERS). No omitempty: 0 is a real value the responder can
	// send, and an unrecognised type is exactly the thing worth showing.
	HardwareType     uint16 `json:"hardwareType"`
	HardwareTypeName string `json:"hardwareTypeName,omitempty"`

	Label      string `json:"label,omitempty"`
	LabelKnown bool   `json:"labelKnown"`

	CurrentIP string `json:"currentIp,omitempty"`
	// CurrentMask is the dotted rendering of CurrentPrefixLen, for display.
	// The prefix length is what E1.37-2 actually puts on the wire; both are
	// sent so the UI can show whichever reads better without re-deriving it.
	// No omitempty on the prefix: /0 is a legal, meaningful value.
	CurrentMask      string `json:"currentMask,omitempty"`
	CurrentPrefixLen uint8  `json:"currentPrefixLen"`
	CurrentKnown     bool   `json:"currentKnown"`
	// CurrentDHCPStatus comes from IPV4_CURRENT_ADDRESS itself and answers
	// "was the address in use obtained via DHCP". DHCPStatus below answers
	// the different question of whether DHCP is enabled on the interface.
	CurrentDHCPStatus string `json:"currentDhcpStatus,omitempty"`
	CurrentDHCPKnown  bool   `json:"currentDhcpKnown"`

	StaticIP        string `json:"staticIp,omitempty"`
	StaticMask      string `json:"staticMask,omitempty"`
	StaticPrefixLen uint8  `json:"staticPrefixLen"`
	StaticKnown     bool   `json:"staticKnown"`

	// DHCPStatus is DHCPStatus.String()'s label ("inactive"/"active"/
	// "unknown"/a raw hex fallback) — never a bare bool, since IPV4_DHCP_
	// MODE's response can itself report "unknown".
	DHCPStatus string `json:"dhcpStatus,omitempty"`
	DHCPKnown  bool   `json:"dhcpKnown"`

	// HardwareAddressHex is INTERFACE_HARDWARE_ADDRESS_TYPE1's raw response,
	// undecoded (hex only) — this app has NO confirmed byte layout for what
	// follows the (also-unconfirmed) interface-ID-prefix convention, so it
	// is deliberately never interpreted as a MAC address here; see
	// params.Client.InterfaceHardwareAddress's doc comment.
	HardwareAddressHex   string `json:"hardwareAddressHex,omitempty"`
	HardwareAddressKnown bool   `json:"hardwareAddressKnown"`

	// ApplySupported: whether INTERFACE_APPLY_CONFIGURATION is advertised —
	// the UI uses this only to word its note about whether a static/DHCP
	// change needs (and got) a follow-up apply; it never blocks the SET
	// itself, since a device that doesn't need an apply step still SETs
	// directly per ipconfig.go's own doc comment.
	ApplySupported bool `json:"applySupported"`
}

type networkDNSServerJSON struct {
	Index int    `json:"index"`
	IP    string `json:"ip"`
}

type networkDNSJSON struct {
	Supported bool `json:"supported"`

	Hostname      string `json:"hostname,omitempty"`
	HostnameKnown bool   `json:"hostnameKnown"`
	Domain        string `json:"domain,omitempty"`
	DomainKnown   bool   `json:"domainKnown"`
	// NameServers is read-only here: this app has no SET DNS_NAME_SERVER
	// support (ipconfig.go only implements the GET direction — see this
	// file's doc comment) so there's nothing to write back.
	NameServers []networkDNSServerJSON `json:"nameServers"`
}

// networkJSON is GET /api/device/{uid}/network's response. Known is
// SUPPORTED_PARAMETERS' own resolved/not-resolved state (client.IsAdvertised's
// discriminator — Known:false MUST read as "don't know", never as "confirmed
// unsupported"); Supported is specifically whether LIST_INTERFACES is
// advertised, the gate this whole feature hangs off. Every other field is
// independently best-effort — a device that advertises LIST_INTERFACES but
// not, say, IPV4_STATIC_ADDRESS still returns 200 with StaticKnown:false on
// each interface row, never a whole-request error.
type networkJSON struct {
	Known      bool                   `json:"known"`
	Supported  bool                   `json:"supported"`
	Interfaces []networkInterfaceJSON `json:"interfaces"`
	DNS        networkDNSJSON         `json:"dns"`
}

func (s *Server) handleGetDeviceNetwork(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()

	out := networkJSON{
		Interfaces: make([]networkInterfaceJSON, 0),
		DNS:        networkDNSJSON{NameServers: make([]networkDNSServerJSON, 0)},
	}
	out.Supported, out.Known = client.IsAdvertised(ctx, rdm.PIDListInterfaces)
	if out.Supported {
		if ids, err := client.ListInterfaces(ctx); err == nil {
			applySupported, _ := client.IsAdvertised(ctx, rdm.PIDInterfaceApplyConfiguration)
			labelSupported, _ := client.IsAdvertised(ctx, rdm.PIDInterfaceLabel)
			currentSupported, _ := client.IsAdvertised(ctx, rdm.PIDIPv4CurrentAddress)
			staticSupported, _ := client.IsAdvertised(ctx, rdm.PIDIPv4StaticAddress)
			dhcpSupported, _ := client.IsAdvertised(ctx, rdm.PIDIPv4DHCPMode)
			hwSupported, _ := client.IsAdvertised(ctx, rdm.PIDInterfaceHardwareAddressType1)
			for _, iface := range ids {
				id := iface.ID
				row := networkInterfaceJSON{
					ID:               id,
					HardwareType:     iface.HardwareType,
					HardwareTypeName: iface.HardwareTypeLabel(),
					ApplySupported:   applySupported,
				}
				if hwSupported {
					if raw, err := client.InterfaceHardwareAddress(ctx, id); err == nil {
						row.HardwareAddressHex, row.HardwareAddressKnown = hex.EncodeToString(raw), true
					}
				}
				if labelSupported {
					if lbl, err := client.InterfaceLabel(ctx, id); err == nil {
						row.Label, row.LabelKnown = lbl, true
					}
				}
				if currentSupported {
					if cfg, err := client.IPv4CurrentAddress(ctx, id); err == nil {
						row.CurrentIP, row.CurrentKnown = cfg.IP.String(), true
						row.CurrentMask, row.CurrentPrefixLen = cfg.SubnetMask().String(), cfg.PrefixLen
						// IPV4_CURRENT_ADDRESS carries its own DHCP status
						// (E1.37-2 §4.6), which is the authoritative answer to
						// "did this address come from DHCP" for the address
						// actually in use. IPV4_DHCP_MODE below reports whether
						// DHCP is *enabled*, which is a different question.
						if cfg.DHCPStatusKnown {
							row.CurrentDHCPStatus, row.CurrentDHCPKnown = cfg.DHCPStatus.String(), true
						}
					}
				}
				if staticSupported {
					if cfg, err := client.IPv4StaticAddress(ctx, id); err == nil {
						row.StaticIP, row.StaticKnown = cfg.IP.String(), true
						row.StaticMask, row.StaticPrefixLen = cfg.SubnetMask().String(), cfg.PrefixLen
					}
				}
				if dhcpSupported {
					if st, err := client.IPv4DHCPMode(ctx, id); err == nil {
						row.DHCPStatus, row.DHCPKnown = st.String(), true
					}
				}
				out.Interfaces = append(out.Interfaces, row)
			}
		}
	}

	if hostSupported, hostKnown := client.IsAdvertised(ctx, rdm.PIDDNSHostname); hostKnown && hostSupported {
		out.DNS.Supported = true
		if h, err := client.DNSHostname(ctx); err == nil {
			out.DNS.Hostname, out.DNS.HostnameKnown = h, true
		}
	}
	if domSupported, domKnown := client.IsAdvertised(ctx, rdm.PIDDNSDomainName); domKnown && domSupported {
		if d, err := client.DNSDomainName(ctx); err == nil {
			out.DNS.Domain, out.DNS.DomainKnown = d, true
		}
	}
	if nsSupported, nsKnown := client.IsAdvertised(ctx, rdm.PIDDNSNameServer); nsKnown && nsSupported {
		for i := byte(0); i <= rdm.DNSNameServerMaxIndex; i++ {
			if ip, err := client.DNSNameServer(ctx, i); err == nil && ip.IsValid() {
				out.DNS.NameServers = append(out.DNS.NameServers, networkDNSServerJSON{Index: int(i), IP: ip.String()})
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// networkActionResponseJSON is every write endpoint's response — Note is a
// finished sentence the UI renders verbatim (server.go's unreachableNote
// convention), reporting whether an INTERFACE_APPLY_CONFIGURATION follow-up
// was attempted/succeeded, since the SET's own ACK doesn't say that.
type networkActionResponseJSON struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func parseInterfaceID(r *http.Request) (uint32, bool) {
	v, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	return uint32(v), err == nil
}

// applyInterfaceConfigNote issues SET INTERFACE_APPLY_CONFIGURATION when the
// device advertises it (best-effort: a failure here is reported in the note
// but does not fail the request — the static/DHCP SET itself already
// succeeded by the time this runs).
func applyInterfaceConfigNote(ctx context.Context, client interface {
	IsAdvertised(context.Context, rdm.ParameterID) (bool, bool)
	ApplyInterfaceConfiguration(context.Context, uint32) error
}, id uint32) string {
	supported, known := client.IsAdvertised(ctx, rdm.PIDInterfaceApplyConfiguration)
	if !(known && supported) {
		return "This device does not advertise INTERFACE_APPLY_CONFIGURATION — the change above may already be live, or may need a power-cycle/front-panel action to take effect; this app cannot tell which."
	}
	if err := client.ApplyInterfaceConfiguration(ctx, id); err != nil {
		return "INTERFACE_APPLY_CONFIGURATION was sent but did not succeed (" + err.Error() + ") — the change above may not have taken effect."
	}
	return "INTERFACE_APPLY_CONFIGURATION sent and accepted."
}

// --- IPV4_STATIC_ADDRESS ----------------------------------------------------

type networkStaticRequestJSON struct {
	IP      string `json:"ip"`
	Mask    string `json:"mask"`
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetNetworkStatic(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	id, ok := parseInterfaceID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("bad interface id"))
		return
	}
	var req networkStaticRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != networkConfirm {
		writeError(w, http.StatusBadRequest, ErrNetworkConfirmationRequired)
		return
	}
	ip, err := netip.ParseAddr(req.IP)
	if err != nil || !ip.Is4() {
		writeError(w, http.StatusBadRequest, errors.New("ip must be a valid IPv4 address"))
		return
	}
	mask, err := netip.ParseAddr(req.Mask)
	if err != nil || !mask.Is4() {
		writeError(w, http.StatusBadRequest, errors.New("mask must be a valid IPv4 subnet mask"))
		return
	}
	// E1.37-2 carries the netmask as a prefix length, so a mask that is not a
	// contiguous run of ones cannot be expressed at all. Reject it here
	// rather than counting bits and writing an address the operator did not
	// ask for — this request configures a gateway's network interface.
	prefixLen, err := params.PrefixLenFromMask(mask)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.SetIPv4StaticAddress(ctx, id, ip, prefixLen); err != nil {
		writeParamError(w, err)
		return
	}
	note := applyInterfaceConfigNote(ctx, client, id)
	writeJSON(w, http.StatusOK, networkActionResponseJSON{Status: "ok", Note: note})
}

// --- IPV4_DHCP_MODE ---------------------------------------------------------

type networkDHCPRequestJSON struct {
	Enable  bool   `json:"enable"`
	Confirm string `json:"confirm"`
}

func (s *Server) handleSetNetworkDHCP(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	id, ok := parseInterfaceID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("bad interface id"))
		return
	}
	var req networkDHCPRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != networkConfirm {
		writeError(w, http.StatusBadRequest, ErrNetworkConfirmationRequired)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.SetIPv4DHCPMode(ctx, id, req.Enable); err != nil {
		writeParamError(w, err)
		return
	}
	note := applyInterfaceConfigNote(ctx, client, id)
	writeJSON(w, http.StatusOK, networkActionResponseJSON{Status: "ok", Note: note})
}

// --- DNS_HOSTNAME / DNS_DOMAIN_NAME -----------------------------------------

type networkDNSRequestJSON struct {
	Hostname string `json:"hostname"`
	Domain   string `json:"domain"`
	Confirm  string `json:"confirm"`
}

func (s *Server) handleSetNetworkDNS(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.resolveDeviceClient(w, r)
	if !ok {
		return
	}
	var req networkDNSRequestJSON
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Confirm != networkConfirm {
		writeError(w, http.StatusBadRequest, ErrNetworkConfirmationRequired)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), deviceParamTimeout)
	defer cancel()
	if err := client.SetDNSHostname(ctx, req.Hostname); err != nil {
		writeParamError(w, err)
		return
	}
	if err := client.SetDNSDomainName(ctx, req.Domain); err != nil {
		writeParamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, networkActionResponseJSON{Status: "ok"})
}
