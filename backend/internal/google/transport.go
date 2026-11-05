package google

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

const (
	// requestTimeout bounds each call to Google, including reading its
	// answer.
	requestTimeout = 30 * time.Second
	// dialTimeout bounds opening a connection.
	dialTimeout = 10 * time.Second
)

// reach is where the HTTP client may connect.
type reach int

const (
	// reachAny connects wherever Google's names resolve.
	reachAny reach = iota
	// reachLoopback connects only to this machine, for a base URL.
	reachLoopback
)

// errNotLoopback means a base URL's name resolved off this machine.
var errNotLoopback = errors.New("google base url resolved to an address off this machine")

// httpClient returns the client that carries codes and tokens, connecting
// only where reach allows. It follows no redirects.
func httpClient(reach reach) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Config checks a base URL's text; this checks where a connection goes,
	// so a name that resolves off this machine, or a proxy, gets nothing.
	if reach == reachLoopback {
		dialer := &net.Dialer{Timeout: dialTimeout, Control: dialLoopback}
		transport.DialContext = dialer.DialContext
		transport.Proxy = nil
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		// A redirect would carry the code or token to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// dialLoopback returns an error unless address, the resolved IP and port
// about to be dialed, is on this machine.
func dialLoopback(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil || !addrPort.Addr().Unmap().IsLoopback() {
		return errNotLoopback
	}
	return nil
}
