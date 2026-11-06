// Package smtp sends system email through an SMTP server with the emersion
// libraries: go-smtp for the exchange, go-sasl to sign in, and go-message to
// build the message.
package smtp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"inbok/internal/systememail"
)

const (
	// exchangeTimeout bounds one whole send, from dialing to the server
	// accepting the message. It stays below the 10-second request timeout, so
	// a slow server reports a timeout instead of ending the request.
	exchangeTimeout = 8 * time.Second
	// callerMargin is left of a caller's own deadline to record a timeout result
	// instead of failing with its own deadline.
	callerMargin = time.Second
	// quitTimeout bounds the goodbye after the server has accepted the message.
	quitTimeout = time.Second
)

// noSTARTTLS is the text of go-smtp's error for a server that does not offer
// STARTTLS.
//
// NOTE: noSTARTTLS is a workaround. Remove it once go-smtp exports one.
//
// Matching error text depends on go-smtp's v0.25.0 internals, which any release
// may change. It has no exported error for a server without STARTTLS and no way
// to upgrade an existing client, so this is the only way to report tls rather
// than connect. We accept this because TestSendSTARTTLSMissing fails if an
// upgrade changes the text, and a mismatch only mislabels the failure: the send
// still stops before anything is sent in clear text.
const noSTARTTLS = "smtp: server doesn't support STARTTLS"

// Sender sends each message over a new connection. It is safe for concurrent use.
type Sender struct {
	roots   *x509.CertPool // nil: the system's trusted certificates
	timeout time.Duration
	hello   string // the name sent in EHLO

	// dial opens the connection; nil uses a plain [net.Dialer]. Tests
	// replace it to break a connection at a chosen moment.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewSender returns a [Sender] that names itself host in EHLO, a domain or an
// IP address, and verifies servers against the system's trusted certificates.
func NewSender(host string) *Sender {
	return &Sender{timeout: exchangeTimeout, hello: helloName(host)}
}

// WithTimeout returns a copy of s whose sends end after timeout, from
// dialing to the server's last reply, instead of exchangeTimeout.
func (s *Sender) WithTimeout(timeout time.Duration) *Sender {
	copied := *s
	copied.timeout = timeout
	return &copied
}

// helloName returns host as an EHLO argument: a domain unchanged, an IP
// address as an address literal, e.g. [192.0.2.1] or [IPv6:2001:db8::1].
func helloName(host string) string {
	// RFC 5321 section 4.1.3 defines the address literals.
	ip, err := netip.ParseAddr(host)
	switch {
	case err != nil:
		return host
	case ip.Is4():
		return "[" + ip.String() + "]"
	}
	return "[IPv6:" + ip.String() + "]"
}

// Send delivers message to message.To, signing in with password when
// server.Username is set. It returns nil once the server accepts the message,
// a [*systememail.SendError] when the server or network fails, ctx's error
// when the caller gives up, and a plain error for unsafe input.
func (s *Sender) Send(ctx context.Context, server systememail.Server, password string, message systememail.Message) error {
	// 1. Refuse unsafe input before connecting. It is the caller's fault,
	// not the server's, so it is no SendError.
	if err := validateSend(server, password, message); err != nil {
		return err
	}

	// 2. Compose the message, so a malformed one fails before connecting.
	body, err := compose(message, time.Now())
	if err != nil {
		return err
	}

	// 3. Bound the session. End before the caller's own deadline, so it can
	// still record the result.
	limit := s.timeout
	if deadline, ok := ctx.Deadline(); ok {
		limit = min(limit, time.Until(deadline)-callerMargin)
	}
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	// 4. Run the session.
	err = s.exchange(bounded, server, password, message, body)
	if err == nil {
		return nil
	}

	// 5. Label a failure: the caller giving up, then time running out, then
	// the step's own reason.
	if ctx.Err() != nil {
		// The caller gave up; the server is not at fault. Waiting for the
		// answer to a whole message, the send stays unconfirmed, and the
		// error still matches the caller's.
		if step, ok := errors.AsType[*systememail.SendError](err); ok && step.Unconfirmed {
			cancelled := failed(step.Reason, fmt.Errorf("%w while waiting for the answer to the message", ctx.Err()))
			cancelled.Unconfirmed = true
			return cancelled
		}
		return ctx.Err()
	}
	// When time runs out, the waiting step fails on the closed connection and
	// labels itself, but the cause is the wait. Keep the step for logs.
	var netErr net.Error
	if bounded.Err() != nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		unconfirmed := false
		if step, ok := errors.AsType[*systememail.SendError](err); ok {
			err = fmt.Errorf("waiting at %s: %w", step.Reason, step.Err)
			unconfirmed = step.Unconfirmed
		}
		timeout := failed(systememail.ReasonTimeout, err)
		timeout.Unconfirmed = unconfirmed
		return timeout
	}
	return err
}

// exchange runs one SMTP session that delivers message with body to server.
// It returns nil once the server accepts the message, and otherwise a failure
// carrying the reason of its step.
//
// DESIGN: this follows [gosmtp.SendMail] step by step. SendMail takes no
// context or TLS settings, has no plain mode, and returns one error for
// every step, so it cannot meet the deadline or report a reason.
func (s *Sender) exchange(ctx context.Context, server systememail.Server, password string, message systememail.Message, body []byte) error {
	// 1. Connect.
	dial := s.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	raw, err := dial(ctx, "tcp", net.JoinHostPort(server.Host, strconv.Itoa(server.Port)))
	if err != nil {
		return failed(systememail.ReasonConnect, err)
	}
	conn := &watchedConn{Conn: raw, left: maxReplyBytes}

	// go-smtp sets and clears a deadline around each command, so a deadline on
	// the connection would not hold; closing it ends any read or write in progress.
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	defer raw.Close()

	// 2. Protect the connection and greet the server.
	client, err := s.open(ctx, conn, server)
	if err != nil {
		return err
	}
	defer client.Close()

	// 3. Sign in, which needs the protected connection.
	sent := redaction{recipient: message.To}
	if server.Username != "" {
		sent.credentials, err = signIn(client, server.Username, password)
		if err != nil {
			// The reply can quote the credentials just sent.
			return refused(systememail.ReasonAuth, signInError(err))
		}
	}

	// 4. Name the sender and the recipient (MAIL, RCPT). A non-ASCII
	// address needs the server's SMTPUTF8 extension (RFC 6531). From here
	// on a reply can quote the sign-in or the recipient, so each failure
	// passes through sent.
	var options *gosmtp.MailOptions
	if !isASCII(message.FromAddress) || !isASCII(message.To) {
		options = &gosmtp.MailOptions{UTF8: true}
	}
	if err := client.Mail(message.FromAddress, options); err != nil {
		return refused(systememail.ReasonSender, sent.redact(err))
	}
	if err := client.Rcpt(message.To, nil); err != nil {
		return refused(systememail.ReasonRecipient, sent.redact(err))
	}

	// 5. Send the message (DATA). A refusal after the recipients is about
	// the message itself. For a test message that is nearly always the From
	// address, e.g. an unverified sender.
	data, err := client.Data()
	if err != nil {
		return refused(systememail.ReasonSender, sent.redact(err))
	}
	if _, err := data.Write(body); err != nil {
		return refused(systememail.ReasonSender, sent.redact(err))
	}
	// CloseWithResponse flushes the message's end and its final dot, then
	// reads the server's answer. A failed flush means the dot never left, so
	// the message was not accepted. Once reading starts, only a 4xx or 5xx
	// reply, which go-smtp documents as an *SMTPError, says the server did
	// not take it; any other failure, a garbled reply included, leaves the
	// message possibly accepted.
	conn.ended.Store(true)
	if _, err := data.CloseWithResponse(); err != nil {
		failure := refused(systememail.ReasonSender, sent.redact(err))
		smtpErr, ok := errors.AsType[*gosmtp.SMTPError](err)
		failure.Unconfirmed = conn.answering.Load() && (!ok || smtpErr.Code < 400)
		return failure
	}

	// 6. Say goodbye (QUIT). The server has accepted the message; a slow or
	// failed goodbye changes nothing.
	client.CommandTimeout = quitTimeout
	_ = client.Quit()
	return nil
}

// open protects conn as server.Security says and greets the server. It
// returns a client ready to sign in.
func (s *Sender) open(ctx context.Context, conn *watchedConn, server systememail.Server) (*gosmtp.Client, error) {
	config := &tls.Config{ServerName: server.Host, RootCAs: s.roots, MinVersion: tls.VersionTLS12}
	if server.Security == systememail.SecuritySTARTTLS {
		// Once STARTTLS is asked for, any failure means no protected connection.
		client, err := gosmtp.NewClientStartTLS(conn, config)
		if err != nil && (conn.startTLS.Load() || err.Error() == noSTARTTLS) {
			return nil, failed(systememail.ReasonTLS, err)
		}
		if err != nil {
			return nil, refused(systememail.ReasonConnect, err)
		}

		// The handshake runs on the first command after STARTTLS.
		if err := client.Hello(s.hello); err != nil {
			client.Close()
			return nil, failed(systememail.ReasonTLS, err)
		}
		return client, nil
	}

	var plain net.Conn = conn
	if server.Security == systememail.SecurityTLS {
		// Handshake before greeting: go-smtp would do it inside the greeting and
		// the failure would read as connect.
		protected := tls.Client(conn, config)
		if err := protected.HandshakeContext(ctx); err != nil {
			return nil, failed(systememail.ReasonTLS, err)
		}
		plain = protected
	}
	client := gosmtp.NewClient(plain)
	if err := client.Hello(s.hello); err != nil {
		return nil, failed(systememail.ReasonConnect, err)
	}
	return client, nil
}

// isASCII reports whether s holds only ASCII bytes.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
