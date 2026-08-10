package transport

import (
	"net"
	"net/netip"
	"sort"
)

// Interface describes one NIC for the Settings screen's picker: name,
// friendly display string, its IPv4 addresses (with subnet masks so directed
// broadcast can be computed), and up/loopback flags so the UI can grey out
// interfaces that are unusable for Art-Net.
type Interface struct {
	Name        string   // OS interface name, e.g. "eth0" / "Ethernet"
	DisplayName string   // friendly label for the UI
	HardwareLen int      // MAC length, informational
	IPv4        []string // dotted-quad addresses, no mask
	Up          bool
	Loopback    bool
	Broadcast   bool
}

// ListInterfaces enumerates the machine's network interfaces via
// net.Interfaces(), reduced to what the Settings screen needs. Interfaces
// with no IPv4 address are omitted — Art-Net is IPv4-only.
func ListInterfaces() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(ifs))
	for _, ifi := range ifs {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		var ipv4 []string
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				ipv4 = append(ipv4, v4.String())
			}
		}
		if len(ipv4) == 0 {
			continue
		}
		out = append(out, Interface{
			Name:        ifi.Name,
			DisplayName: friendlyName(ifi),
			HardwareLen: len(ifi.HardwareAddr),
			IPv4:        ipv4,
			Up:          ifi.Flags&net.FlagUp != 0,
			Loopback:    ifi.Flags&net.FlagLoopback != 0,
			Broadcast:   ifi.Flags&net.FlagBroadcast != 0,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// friendlyName builds a display string. net.Interface carries no OS-native
// "friendly name" (that's a Windows-only concept surfaced via syscalls this
// package deliberately avoids, to keep the code cross-platform-buildable in
// the Linux sandbox); Name plus its addresses is the practical stand-in.
func friendlyName(ifi net.Interface) string {
	return ifi.Name
}

// BroadcastAddrFor computes the directed-subnet broadcast address for an
// IPv4 address and prefix length: (ip | ^mask). A pure function so its math
// is unit-testable without a socket.
func BroadcastAddrFor(ip netip.Addr, prefixLen int) (netip.Addr, bool) {
	if !ip.Is4() {
		return netip.Addr{}, false
	}
	if prefixLen < 0 || prefixLen > 32 {
		return netip.Addr{}, false
	}
	b := ip.As4()
	var mask uint32
	if prefixLen == 0 {
		mask = 0
	} else {
		mask = ^uint32(0) << (32 - prefixLen)
	}
	ipVal := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	bcast := ipVal | ^mask
	out := [4]byte{byte(bcast >> 24), byte(bcast >> 16), byte(bcast >> 8), byte(bcast)}
	return netip.AddrFrom4(out), true
}

// SubnetAddrPrefix finds an interface's IPv4 address+prefix by name, for
// feeding BroadcastAddrFor. Returns ok=false if the interface or an IPv4
// address isn't found.
func SubnetAddrPrefix(name string) (netip.Addr, int, bool) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return netip.Addr{}, 0, false
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		ones, _ := ipnet.Mask.Size()
		addr, ok := netip.AddrFromSlice(v4)
		if !ok {
			continue
		}
		return addr, ones, true
	}
	return netip.Addr{}, 0, false
}
