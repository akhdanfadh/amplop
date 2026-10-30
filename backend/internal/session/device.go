package session

import "net/netip"

// Device is what the request that creates a session says about its browser.
// Either part can be empty: an absent header or an unknown address.
type Device struct {
	UserAgent string
	Addr      netip.Addr
}
