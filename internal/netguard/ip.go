package netguard

import "net"

// IsBlockedIP reports whether ip names an address an agent-driven network tool
// should not reach by default. It covers private, loopback, link-local,
// unspecified, multicast, and IPv4 broadcast addresses, including IPv4-mapped
// IPv6 forms handled by net.IP's category methods.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// 255.255.255.255 is not covered by any Is* method but is not a
	// reachable public fetch target.
	if v4 := ip.To4(); v4 != nil && v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255 {
		return true
	}
	return false
}
