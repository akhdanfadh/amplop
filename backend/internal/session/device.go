package session

import (
	"net/netip"
	"strings"
)

// maxUserAgentBytes bounds the stored User-Agent. Browsers send far less,
// but a request may send up to the server's header limit.
const maxUserAgentBytes = 512

// Device is what the request that creates a session says about its browser.
// Either part can be empty: an absent header or an unknown address.
type Device struct {
	UserAgent string
	Addr      netip.Addr
}

// cleanDevice bounds the User-Agent and gives each address one form.
func cleanDevice(device Device) Device {
	agent := device.UserAgent
	if len(agent) > maxUserAgentBytes {
		agent = agent[:maxUserAgentBytes]
	}
	return Device{
		// PostgreSQL text rejects invalid UTF-8, which a header may carry and the
		// cut can create by splitting a character.
		UserAgent: strings.ToValidUTF8(agent, ""),
		// An IPv4 client seen through an IPv6 socket or proxy keeps its IPv4
		// address, and the zone, which names a server interface, is dropped.
		Addr: device.Addr.Unmap().WithZone(""),
	}
}
