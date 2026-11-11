package google

import (
	"context"
	"errors"
	"io"
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
// only where reach allows and giving each request timeout to finish. It
// follows no redirects.
func httpClient(reach reach, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Config checks a base URL's text; this checks where a connection goes,
	// so a name that resolves off this machine, or a proxy, gets nothing.
	if reach == reachLoopback {
		dialer := &net.Dialer{Timeout: dialTimeout, Control: dialLoopback}
		transport.DialContext = dialer.DialContext
		transport.Proxy = nil
	}
	return &http.Client{
		Transport: timeoutTransport{base: transport, timeout: timeout},
		// A redirect would carry the code or token to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// timeoutTransport sends each request over base with its own deadline,
// timeout from now, which lasts until its answer's body is closed, so it
// bounds connecting, sending, and reading the answer.
//
// NOTE: it replaces [http.Client.Timeout]. For a transport net/http does
// not know, such as [oauth2.Transport], that timeout cancels through the
// deprecated [http.Request.Cancel] and calls
// [oauth2.Transport.CancelRequest], which x/oauth2 keeps only to log that
// it does nothing (golang/oauth2#487, closed "not planned"). A context is
// Go's one supported way.
type timeoutTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

// RoundTrip sends req over base, with the deadline added to its context.
func (t timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	response, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &releasingBody{ReadCloser: response.Body, release: cancel}
	return response, nil
}

// releasingBody is an answer's body that releases its request's deadline
// when closed.
type releasingBody struct {
	io.ReadCloser
	release context.CancelFunc
}

// Close closes the body, then releases the deadline.
func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err
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
