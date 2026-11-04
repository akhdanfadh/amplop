package clientprefix

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An IPv4 address counts alone, however it is written; an IPv6 address counts
// with the rest of its /64.
func TestOf(t *testing.T) {
	prefix := func(addr string) netip.Prefix { return Of(netip.MustParseAddr(addr)) }

	assert.Equal(t, netip.MustParsePrefix("192.0.2.1/32"), prefix("192.0.2.1"))
	assert.Equal(t, prefix("192.0.2.1"), prefix("::ffff:192.0.2.1"))
	assert.NotEqual(t, prefix("192.0.2.1"), prefix("192.0.2.2"))
	assert.Equal(t, netip.MustParsePrefix("2001:db8:1:2::/64"), prefix("2001:db8:1:2::1"))
	assert.Equal(t, prefix("2001:db8:1:2::1"), prefix("2001:db8:1:2:ffff::9"))
	assert.Equal(t, prefix("2001:db8:1:2::1"), prefix("2001:db8:1:2::1%eth0"))
	assert.NotEqual(t, prefix("2001:db8:1:2::1"), prefix("2001:db8:1:3::1"))
}
