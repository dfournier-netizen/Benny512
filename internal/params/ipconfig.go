package params

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"

	"benny512/internal/rdm"
)

// This file exposes E1.37-2's IPv4/DNS configuration PIDs for RDM-capable
// network devices through the same generic Get/Set path package rdm/params
// already provides: these are ordinary PIDs, decoded/encoded like any other
// once introspected via PARAMETER_DESCRIPTION, so a device this package
// never anticipated can still describe its own E1.37-2 PIDs the same way.
//
// --- Wire layouts: VERIFIED against primary text -------------------------
//
// These layouts were CONFIRMED against ANSI E1.37-2:2015 (R2021), including
// the worked example in its Appendix B, on 2026-09-08. They previously
// carried a note saying they were "this package's best reading of the common
// E1.37-2 responder convention (matching how OLA's own e137_2 responder
// shapes these PIDs) — UNVERIFIED against ANSI/ESTA E1.37-2 primary text or
// a real device". That reading was wrong in three places, and the errors are
// recorded here because each one is the kind that a length check waves
// through:
//
//	LIST_INTERFACES (§4.1) — the response is a packed list of interface
//	  descriptors of 48 BITS (6 bytes) each: Interface Identifier (32-bit)
//	  plus Interface Hardware Type (16-bit, IANA ARP-PARAMETERS; 0x0001 is
//	  Ethernet). This package read 4-byte identifiers and guarded on
//	  len(data)%4 != 0 — which CANNOT catch the error, because Appendix B's
//	  real two-interface response is 12 bytes and 12 % 4 == 0. It decoded
//	  three phantom interfaces (0x00000001, 0x00010000, 0x00020001) from two
//	  real ones, and every per-interface GET that followed carried a
//	  fabricated identifier and earned NR_DATA_OUT_OF_RANGE.
//
//	IPV4_CURRENT_ADDRESS (§4.6) — response PDL 0x0a: interface ID (32-bit),
//	  IPv4 address (32-bit), netmask as a ONE-BYTE PREFIX LENGTH (0-32), and
//	  a DHCP Status byte. This package required >= 12 bytes and read the mask
//	  as a 4-byte dotted address, so a conforming 10-byte response was
//	  REJECTED OUTRIGHT and the DHCP status was discarded.
//
//	IPV4_STATIC_ADDRESS (§4.7) — GET response and SET request are both PDL
//	  0x09: interface ID, address, 1-byte prefix. Appendix B sets 10.0.0.32/8
//	  as 00000001 0a000020 08. This package built a 12-byte SET with a 4-byte
//	  mask, i.e. it would have written a MALFORMED SET to a real gateway.
//
// The netmask is a prefix length everywhere in this package for that reason.
// It is converted to a dotted mask only at a presentation boundary; storing
// it as an address is precisely what produced the defect above.
//
// Byte order is big-endian throughout (§3.4). Every per-interface PID returns
// NR_DATA_OUT_OF_RANGE if the interface identifier is not one that
// LIST_INTERFACES would report (§4.2 and following).
var (
	// ErrBadInterfaceList is returned when LIST_INTERFACES data is not a
	// whole number of 6-byte interface descriptors. Six, not four: see this
	// file's doc comment for why a 4-byte reading passed its own length
	// check against a real device and still decoded the wrong interfaces.
	ErrBadInterfaceList = fmt.Errorf("%w: LIST_INTERFACES data not a multiple of 6 bytes", ErrBadLength)
)

// InterfaceDescriptorSize is the wire size of one LIST_INTERFACES entry:
// a 32-bit Interface Identifier followed by a 16-bit Interface Hardware
// Type, 48 bits in total (E1.37-2 §4.1).
const InterfaceDescriptorSize = 6

// Interface is one decoded LIST_INTERFACES descriptor.
type Interface struct {
	// ID is the 32-bit Interface Identifier every other per-interface PID
	// takes as its first four bytes. The standard says these range from 1 to
	// 0xFFFFFF00 and are not required to be contiguous, so they are opaque
	// handles: never assume 1..n, and never synthesise one.
	ID uint32
	// HardwareType is the interface's underlying hardware, from IANA's
	// ARP-PARAMETERS "Hardware Types" registry. 0x0001 is Ethernet.
	HardwareType uint16
}

// HardwareTypeLabel names the common hardware types and falls back to the
// raw value, so an unrecognised interface is reported rather than hidden.
func (i Interface) HardwareTypeLabel() string {
	switch i.HardwareType {
	case 0x0001:
		return "Ethernet"
	case 0x0006:
		return "IEEE 802"
	case 0x0018:
		return "IEEE 1394"
	default:
		return fmt.Sprintf("hardware type %d", i.HardwareType)
	}
}

// ListInterfaces issues GET LIST_INTERFACES (PID 0x0700), decoding the
// packed list of interface descriptors.
func (c *Client) ListInterfaces(ctx context.Context) ([]Interface, error) {
	data, err := c.getRaw(ctx, rdm.PIDListInterfaces, nil)
	if err != nil {
		return nil, err
	}
	return DecodeInterfaceList(data)
}

// DecodeInterfaceList decodes LIST_INTERFACES' response: a packed list of
// 6-byte descriptors (E1.37-2 §4.1). Exported so internal/capture can render
// the same interpretation in a --logrdm capture without duplicating the wire
// format.
func DecodeInterfaceList(data []byte) ([]Interface, error) {
	if len(data)%InterfaceDescriptorSize != 0 {
		return nil, ErrBadInterfaceList
	}
	out := make([]Interface, 0, len(data)/InterfaceDescriptorSize)
	for i := 0; i+InterfaceDescriptorSize <= len(data); i += InterfaceDescriptorSize {
		out = append(out, Interface{
			ID:           binary.BigEndian.Uint32(data[i : i+4]),
			HardwareType: binary.BigEndian.Uint16(data[i+4 : i+6]),
		})
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

// IPv4Config is the decoded shape of IPV4_STATIC_ADDRESS's 9-byte response
// (interface ID, IP, 1-byte prefix length — §4.7) and of IPV4_CURRENT_
// ADDRESS's 10-byte response, which appends a DHCP Status byte (§4.6).
type IPv4Config struct {
	InterfaceID uint32
	IP          netip.Addr
	// PrefixLen is the netmask expressed the way E1.37-2 puts it on the
	// wire: the NUMBER OF BITS in the network portion, 0-32 (§4.6, §4.7).
	// It is deliberately not a netip.Addr — storing a prefix as a dotted
	// address is what produced the 12-byte payloads this package used to
	// send. Use SubnetMask for display.
	PrefixLen uint8
	// DHCPStatus is only carried by IPV4_CURRENT_ADDRESS (§4.6), whose
	// response is one byte longer than IPV4_STATIC_ADDRESS's for exactly
	// this field. DHCPStatusKnown separates "the responder told us" from
	// "this reply had no such field", because the standard's own
	// DHCP_STATUS_UNKNOWN is a REAL answer meaning "this device cannot tell
	// whether its address came from DHCP" — collapsing the two would turn a
	// missing field into a confident statement about the device.
	DHCPStatus      rdm.DHCPStatus
	DHCPStatusKnown bool
}

// SubnetMask renders PrefixLen as the dotted IPv4 mask a lighting tech
// expects to read. Presentation only — never store or transmit this.
func (c IPv4Config) SubnetMask() netip.Addr {
	n := c.PrefixLen
	if n > 32 {
		n = 32
	}
	var m uint32
	if n > 0 {
		m = ^uint32(0) << (32 - n)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], m)
	return netip.AddrFrom4(b)
}

// PrefixLenFromMask converts a dotted IPv4 mask to E1.37-2's prefix length.
// It REJECTS a non-contiguous mask (255.0.255.0 and friends) rather than
// silently counting bits, because such a mask cannot be expressed in this
// field at all and quietly reinterpreting one would write an address the
// operator never asked for.
func PrefixLenFromMask(mask netip.Addr) (uint8, error) {
	if !mask.Is4() {
		return 0, fmt.Errorf("params: subnet mask must be IPv4")
	}
	b := mask.As4()
	v := binary.BigEndian.Uint32(b[:])
	// A valid mask is a run of ones then a run of zeros: inverting it must
	// yield a value whose bits are all contiguous from the bottom.
	inv := ^v
	if inv&(inv+1) != 0 {
		return 0, fmt.Errorf("params: %s is not a contiguous subnet mask", mask)
	}
	n := uint8(0)
	for ; v&0x80000000 != 0; v <<= 1 {
		n++
	}
	return n, nil
}

func decodeIPv4Config(data []byte) (IPv4Config, error) {
	return DecodeIPv4Config(data)
}

// DecodeIPv4Config decodes IPV4_STATIC_ADDRESS's 9-byte shape (interface ID,
// address, 1-byte prefix — E1.37-2 §4.7, and the shape of a SET request's
// payload) and IPV4_CURRENT_ADDRESS's 10-byte shape, which appends a DHCP
// Status byte (§4.6). Exported for internal/capture's benefit.
//
// Anything shorter than 9 bytes is rejected. The previous >=12 floor is what
// made a conforming responder's reply unreadable.
func DecodeIPv4Config(data []byte) (IPv4Config, error) {
	if len(data) < 9 {
		return IPv4Config{}, fmt.Errorf("%w: IPv4 config wants >=9 bytes, got %d", ErrBadLength, len(data))
	}
	cfg := IPv4Config{
		InterfaceID: binary.BigEndian.Uint32(data[0:4]),
		IP:          netip.AddrFrom4([4]byte(data[4:8])),
		PrefixLen:   data[8],
	}
	if len(data) >= 10 {
		cfg.DHCPStatus, cfg.DHCPStatusKnown = rdm.DHCPStatus(data[9]), true
	}
	return cfg, nil
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
//
// PDL 0x09 per E1.37-2 §4.7: interface ID (32-bit), IPv4 address (32-bit),
// and the netmask as a ONE-BYTE PREFIX LENGTH. Appendix B's worked example
// sets 10.0.0.32/8 as 00000001 0a000020 08.
//
// This previously built a 12-byte payload with a 4-byte dotted mask, which
// is a malformed SET to a conforming responder — and this PID writes network
// configuration to a gateway, so a malformed write is how a device ends up
// stranded off the lighting network.
//
// The change does not take effect on the device until a subsequent SET
// INTERFACE_APPLY_CONFIGURATION (§4.8), and the standard warns that some
// devices reboot when that is applied.
func (c *Client) SetIPv4StaticAddress(ctx context.Context, interfaceID uint32, ip netip.Addr, prefixLen uint8) error {
	if !ip.Is4() {
		return fmt.Errorf("params: SetIPv4StaticAddress: ip must be IPv4")
	}
	if prefixLen > 32 {
		return fmt.Errorf("params: SetIPv4StaticAddress: prefix length %d out of range (0-32)", prefixLen)
	}
	b := make([]byte, 9)
	binary.BigEndian.PutUint32(b[0:4], interfaceID)
	ipb := ip.As4()
	copy(b[4:8], ipb[:])
	b[8] = prefixLen
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
