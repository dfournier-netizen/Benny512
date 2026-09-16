//go:build unix

package sacn

import "syscall"

// setMulticastIf pins the socket's outgoing multicast interface to the NIC
// that owns ip4. Without this the kernel picks the egress interface from the
// routing table, which on a show machine with several NICs is not necessarily
// the one the operator selected.
func setMulticastIf(fd uintptr, ip4 [4]byte) error {
	return syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, ip4)
}
