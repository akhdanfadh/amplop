// Package clientprefix decides which addresses count as one client in a
// limit: an IPv4 address alone, or an IPv6 /64.
//
// DESIGN: an IPv6 connection usually gets a whole /64 block, so one host can
// give every request a new address within it. Counting the block keeps such
// a host one client. ADR024 records the rule.
package clientprefix

import "net/netip"

// ipv6Bits is the IPv6 prefix one client is counted by.
const ipv6Bits = 64

// Of returns the prefix addr counts as: an IPv4 address alone, also when
// written as an IPv4-mapped IPv6 address, or its IPv6 /64, without a zone.
func Of(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return netip.PrefixFrom(addr, addr.BitLen())
	}
	return netip.PrefixFrom(addr, ipv6Bits).Masked()
}
