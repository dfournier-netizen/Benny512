package params

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"

	"benny512/internal/rdm"
)

// This file exposes E1.37-2's IPv4/DNS configuration PIDs (report §6.2) for
// RDM-capable network devices (a Netron-EN4-style gateway's own root
// device, if it turns out to implement E1.37-2 — report §7.3 flags this as
// UNVERIFIED for the EN4 specifically, confirm tomorrow) through the same
// generic Get/Set path package rdm/params already provide: these are
// ordinary PIDs, decoded/encoded like any other once introspected via
// PARAMETER_DESCRIPTION (report §1.1's "no hardcoded vendor tables"
// principle applies here too — a device the report didn't anticipate can
// still describe its own E1.37-2 PIDs the same way).
//
// The typed helpers below are a convenience layer on top of that generic
// path, not a replacement for it — Introspect/GetParam/SetParam still work
// on these PIDs directly (their DS_IPV4/DS_GROUP-shaped payloads fall
// through to ParamValueRaw per report §5.1's DS_* uncertainty notes, so a
// UI that doesn't know about IPv4 rendering can still show/edit them as
// hex). Wire layout here (interface-ID-prefixed request/response) is this
// package's best reading of the common E1.37-2 responder convention
// (matching how OLA's own e137_2 responder shapes these PIDs) — UNVERIFIED
// against ANSI/ESTA E1.37-2 primary text or a real device this session;
// confirm against hardware (the EN4, if it answers) before relying on it.
var (
	// ErrBadInterfaceList is returned when LIST_INTERFACES data isn't a
	// whole number of 4-byte interface IDs.
	ErrBadInterfaceList = fmt.Errorf("%w: LIST_INTERFACES data not a multiple of 4 bytes", ErrBadLength)
)

// ListInterfaces issues GET LIST_INTERFACES (PID 0x0700), decoding a flat
// array of 4-byte interface IDs.
func (c *Client) ListInterfaces(ctx context.Context) ([]uint32, error) {
	data, err := c.getRaw(ctx, rdm.PIDListInterfaces, nil)
	if err != nil {
		return nil, err
	}
	return DecodeInterfaceList(data)
}

// DecodeInterfaceList decodes LIST_INTERFACES' response shape (a flat array
// of 4-byte interface IDs) — exported so internal/capture can render the
// same interpretation in a --logrdm capture without duplicating the wire
// format. See ListInterfaces above and this file's doc comment.
func DecodeInterfaceList(data []byte) ([]uint32, error) {
	if len(data)%4 != 0 {
		return nil, ErrBadInterfaceList
	}
	out := make([]uint32, 0, len(data)/4)
	for i := 0; i+4 <= len(data); i += 4 {
		out = append(out, binary.BigEndian.Uint32(data[i:i+4]))
	}
	return out, nil
}

// DecodeInterfaceID decodes the bare 4-byte interface ID that prefixes (or,
// for INTERFACE_APPLY_CONFIGURATION/INTERFACE_RENEW_DHCP/INTERFACE_
// RELEASE_DHCP, entirely constitutes) most of this package's per-interface
// GET requests and SET commands. Exported for internal/capture's benefit —
// see DecodeInterfaceList's doc comment.
func DecodeInterfaceID(data []byte) (uint32, error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("%w: interface ID wants >=4 bytes, got %d", ErrBadLength, len(data))
	}
	return binary.BigEndian.Uint32(data[0:4]), nil
}

func encodeInterfaceID(id uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, id)
	return b
}

// InterfaceLabel issues GET INTERFACE_LABEL for one interface ID (as
// returned by ListInterfaces). Response is assumed interface-ID-prefixed
// (4 bytes) followed by the ASCII label — see file doc comment.
func (c *Client) InterfaceLabel(ctx context.Context, interfaceID uint32) (string, error) {
	data, err := c.getRaw(ctx, rdm.PIDInterfaceLabel, encodeInterfaceID(interfaceID))
	if err != nil {
		return "", err
	}
	_, label, err := DecodeInterfaceLabel(data)
	return label, err
}

// DecodeInterfaceLabel decodes INTERFACE_LABEL's interface-ID+ASCII-label
// response shape. Exported for internal/capture's benefit — see
// DecodeInterfaceList's doc comment.
func DecodeInterfaceLabel(data []byte) (interfaceID uint32, label string, err error) {
	if len(data) < 4 {
		return 0, "", fmt.Errorf("%w: INTERFACE_LABEL wants >=4 bytes, got %d", ErrBadLength, len(data))
	}
	return binary.BigEndian.Uint32(data[0:4]), string(data[4:]), nil
}

// IPv4Config is the decoded shape of IPV4_CURRENT_ADDRESS / IPV4_STATIC_
// ADDRESS's response: interface ID, IP, and subnet mask (12 bytes total —
// 4+4+4 — per this package's best reading; see file doc comment).
type IPv4Config struct {
	InterfaceID uint32
	IP          netip.Addr
	SubnetMask  netip.Addr
}

func decodeIPv4Config(data []byte) (IPv4Config, error) {
	return DecodeIPv4Config(data)
}

// DecodeIPv4Config decodes IPV4_CURRENT_ADDRESS/IPV4_STATIC_ADDRESS's
// interface-ID+IP+mask response shape (also the shape of a SET IPV4_
// STATIC_ADDRESS request's payload). Exported for internal/capture's
// benefit — see DecodeInterfaceList's doc comment.
func DecodeIPv4Config(data []byte) (IPv4Config, error) {
	if len(data) < 12 {
		return IPv4Config{}, fmt.Errorf("%w: IPv4 config wants >=12 bytes, got %d", ErrBadLength, len(data))
	}
	return IPv4Config{
		InterfaceID: binary.BigEndian.Uint32(data[0:4]),
		IP:          netip.AddrFrom4([4]byte(data[4:8])),
		SubnetMask:  netip.AddrFrom4([4]byte(data[8:12])),
	}, nil
}

// IPv4CurrentAddress issues GET IPV4_CURRENT_ADDRESS for one interface —
// the currently-active IP/mask, whether from DHCP or static config.
func (c *Client) IPv4CurrentAddress(ctx context.Context, interfaceID uint32) (IPv4Config, error) {
	data, err := c.getRaw(ctx, rdm.PIDIPv4CurrentAddress, encodeInterfaceID(interfaceID))
	if err != nil {
		return IPv4Config{}, err
	}
	return decodeIPv4Config(data)
}

// IPv4StaticAddress issues GET IPV4_STATIC_ADDRESS for one interface — the
// configured (not necessarily active) static IP/mask.
func (c *Client) IPv4StaticAddress(ctx context.Context, interfaceID uint32) (IPv4Config, error) {
	data, err := c.getRaw(ctx, rdm.PIDIPv4StaticAddress, encodeInterfaceID(interfaceID))
	if err != nil {
		return IPv4Config{}, err
	}
	return decodeIPv4Config(data)
}

// SetIPv4StaticAddress issues SET IPV4_STATIC_ADDRESS for one interface.
// Per report §6.2, a device typically requires a follow-up SET
// INTERFACE_APPLY_CONFIGURATION (PIDInterfaceApplyConfiguration) to commit
// pending interface changes — call ApplyInterfaceConfiguration afterward.
func (c *Client) SetIPv4StaticAddress(ctx context.Context, interfaceID uint32, ip, mask netip.Addr) error {
	if !ip.Is4() || !mask.Is4() {
		return fmt.Errorf("params: SetIPv4StaticAddress: ip and mask must be IPv4")
	}
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b[0:4], interfaceID)
	ipb := ip.As4()
	maskb := mask.As4()
	copy(b[4:8], ipb[:])
	copy(b[8:12], maskb[:])
	return c.setRaw(ctx, rdm.PIDIPv4StaticAddress, b)
}

// IPv4DHCPMode issues GET IPV4_DHCP_MODE for one interface.
func (c *Client) IPv4DHCPMode(ctx context.Context, interfaceID uint32) (rdm.DHCPStatus, error) {
	data, err := c.getRaw(ctx, rdm.PIDIPv4DHCPMode, encodeInterfaceID(interfaceID))
	if err != nil {
		return 0, err
	}
	_, status, err := DecodeDHCPMode(data)
	return status, err
}

// DecodeDHCPMode decodes IPV4_DHCP_MODE's interface-ID+status-byte response
// shape (also the shape of a SET IPV4_DHCP_MODE request's payload).
// Exported for internal/capture's benefit — see DecodeInterfaceList's doc
// comment.
func DecodeDHCPMode(data []byte) (interfaceID uint32, status rdm.DHCPStatus, err error) {
	if len(data) < 5 {
		return 0, 0, fmt.Errorf("%w: IPV4_DHCP_MODE wants >=5 bytes, got %d", ErrBadLength, len(data))
	}
	return binary.BigEndian.Uint32(data[0:4]), rdm.DHCPStatus(data[4]), nil
}

// SetIPv4DHCPMode issues SET IPV4_DHCP_MODE for one interface.
func (c *Client) SetIPv4DHCPMode(ctx context.Context, interfaceID uint32, enable bool) error {
	mode := byte(rdm.DHCPStatusInactive)
	if enable {
		mode = byte(rdm.DHCPStatusActive)
	}
	b := append(encodeInterfaceID(interfaceID), mode)
	return c.setRaw(ctx, rdm.PIDIPv4DHCPMode, b)
}

// ApplyInterfaceConfiguration issues SET INTERFACE_APPLY_CONFIGURATION for
// one interface, committing any pending IPv4/DHCP changes.
func (c *Client) ApplyInterfaceConfiguration(ctx context.Context, interfaceID uint32) error {
	return c.setRaw(ctx, rdm.PIDInterfaceApplyConfiguration, encodeInterfaceID(interfaceID))
}

// DNSHostname issues GET DNS_HOSTNAME (device-global, not interface-scoped
// per report §6.2's constants table).
func (c *Client) DNSHostname(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDDNSHostname)
}

// SetDNSHostname issues SET DNS_HOSTNAME, truncated to
// rdm.MaxRDMHostnameLength (63) bytes.
func (c *Client) SetDNSHostname(ctx context.Context, hostname string) error {
	if len(hostname) > rdm.MaxRDMHostnameLength {
		hostname = hostname[:rdm.MaxRDMHostnameLength]
	}
	return c.setRaw(ctx, rdm.PIDDNSHostname, []byte(hostname))
}

// DNSDomainName issues GET DNS_DOMAIN_NAME.
func (c *Client) DNSDomainName(ctx context.Context) (string, error) {
	return c.getLabel(ctx, rdm.PIDDNSDomainName)
}

// SetDNSDomainName issues SET DNS_DOMAIN_NAME, truncated to
// rdm.MaxRDMDomainNameLength (231) bytes.
func (c *Client) SetDNSDomainName(ctx context.Context, domain string) error {
	if len(domain) > rdm.MaxRDMDomainNameLength {
		domain = domain[:rdm.MaxRDMDomainNameLength]
	}
	return c.setRaw(ctx, rdm.PIDDNSDomainName, []byte(domain))
}

// DNSNameServer issues GET DNS_NAME_SERVER for one index (0-
// rdm.DNSNameServerMaxIndex).
func (c *Client) DNSNameServer(ctx context.Context, index byte) (netip.Addr, error) {
	if index > rdm.DNSNameServerMaxIndex {
		return netip.Addr{}, fmt.Errorf("params: DNS name server index %d exceeds max %d", index, rdm.DNSNameServerMaxIndex)
	}
	data, err := c.getRaw(ctx, rdm.PIDDNSNameServer, []byte{index})
	if err != nil {
		return netip.Addr{}, err
	}
	_, ip, err := DecodeDNSNameServer(data)
	return ip, err
}

// DecodeDNSNameServer decodes DNS_NAME_SERVER's index+IPv4 response shape.
// Exported for internal/capture's benefit — see DecodeInterfaceList's doc
// comment.
func DecodeDNSNameServer(data []byte) (index byte, ip netip.Addr, err error) {
	if len(data) < 5 {
		return 0, netip.Addr{}, fmt.Errorf("%w: DNS_NAME_SERVER wants >=5 bytes, got %d", ErrBadLength, len(data))
	}
	return data[0], netip.AddrFrom4([4]byte(data[1:5])), nil
}

// InterfaceHardwareAddress issues GET INTERFACE_HARDWARE_ADDRESS_TYPE1 for
// one interface and returns the raw response bytes undecoded. UNVERIFIED:
// unlike every other PID in this file, this package has no confirmed
// reading for what follows the (also-unconfirmed) interface-ID prefix
// convention — a Type 1 hardware address is conventionally a 6-byte
// Ethernet MAC, but this session found nothing to confirm that byte count
// or ordering against ANSI/ESTA E1.37-2 primary text or a real device. This
// method intentionally does NOT slice/interpret the payload — callers get
// the interface ID convention applied nowhere here, just the whole raw
// response, so a caller (e.g. internal/capture's --logrdm decoding) can
// show hex without asserting a structure this package cannot back up.
func (c *Client) InterfaceHardwareAddress(ctx context.Context, interfaceID uint32) ([]byte, error) {
	return c.getRaw(ctx, rdm.PIDInterfaceHardwareAddressType1, encodeInterfaceID(interfaceID))
}
