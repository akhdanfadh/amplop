package session

import (
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestCleanDevice(t *testing.T) {
	cases := []struct {
		name      string
		agent     string
		addr      netip.Addr
		wantAgent string
		wantAddr  netip.Addr
	}{
		{"kept", "Mozilla/5.0 Firefox/131.0", netip.MustParseAddr("192.0.2.1"), "Mozilla/5.0 Firefox/131.0", netip.MustParseAddr("192.0.2.1")},
		{"empty", "", netip.Addr{}, "", netip.Addr{}},
		{"invalid UTF-8", "a\xffgent\xc3", netip.Addr{}, "agent", netip.Addr{}},
		{"only invalid UTF-8", "\xff", netip.Addr{}, "", netip.Addr{}},
		{"IPv4 in IPv6", "", netip.MustParseAddr("::ffff:192.0.2.1"), "", netip.MustParseAddr("192.0.2.1")},
		{"IPv6 zone", "", netip.MustParseAddr("fe80::1%eth0"), "", netip.MustParseAddr("fe80::1")},
		{"IPv6", "", netip.MustParseAddr("2001:db8::1"), "", netip.MustParseAddr("2001:db8::1")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			device := cleanDevice(Device{UserAgent: c.agent, Addr: c.addr})

			assert.Equal(t, c.wantAgent, device.UserAgent)
			assert.Equal(t, c.wantAddr, device.Addr)
		})
	}
}

func TestCleanDeviceLength(t *testing.T) {
	cases := []struct {
		name, agent, want string
	}{
		{"at limit", strings.Repeat("a", 512), strings.Repeat("a", 512)},
		{"over limit", strings.Repeat("a", 600), strings.Repeat("a", 512)},
		// "é" is two bytes, so byte 512 falls inside the 256th character.
		{"split character", "a" + strings.Repeat("é", 300), "a" + strings.Repeat("é", 255)},
		{"four-byte characters", strings.Repeat("😀", 200), strings.Repeat("😀", 128)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			device := cleanDevice(Device{UserAgent: c.agent})

			assert.Equal(t, c.want, device.UserAgent)
			assert.LessOrEqual(t, len(device.UserAgent), maxUserAgentBytes)
			assert.True(t, utf8.ValidString(device.UserAgent))
		})
	}
}
