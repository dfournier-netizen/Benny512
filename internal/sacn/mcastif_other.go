//go:build !unix && !windows

package sacn

import "errors"

// setMulticastIf is unsupported on this platform; the caller falls back to
// whatever egress interface the routing table selects.
func setMulticastIf(fd uintptr, ip4 [4]byte) error {
	return errors.New("sacn: selecting the multicast interface is not supported on this platform")
}
