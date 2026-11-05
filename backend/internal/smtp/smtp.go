// Package smtp sends system email through an SMTP server with the emersion
// libraries: go-smtp for the exchange, go-sasl to sign in, and go-message to
// build the message.
package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
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
	// maxReplyBytes caps the bytes read from the server in one session; a test
	// send needs a few kilobytes. go-smtp limits each line, not the line count.
	maxReplyBytes = 1 << 20
)

var (
	// errNoSignIn means the server offers neither sign-in method inbok supports.
	errNoSignIn = errors.New("server offers no PLAIN or LOGIN sign-in")
	// errCleartext means a sign-in was attempted without TLS.
	errCleartext = errors.New("refusing to sign in without TLS")
	// errUnexpectedReply stands for a reply to the sign-in that inbok cannot
	// use, whose text it does not keep.
	errUnexpectedReply = errors.New("unexpected reply to the sign-in")
	// errUnsafeInput means the input would change the SMTP session or cannot be dialed.
	errUnsafeInput = errors.New("address, credentials, port, or security mode cannot be sent")
	// errTooLong means the server sent more than maxReplyBytes.
	errTooLong = errors.New("server replies exceed the size limit")
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

var _ systememail.Sender = (*Sender)(nil)

// NewSender returns a Sender that names itself host in EHLO, a domain or an
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
	if server.Username != "" {
		if err := signIn(client, server.Username, password); err != nil {
			// The reply can quote the credentials just sent.
			return refused(systememail.ReasonAuth, signInError(err))
		}
	}

	// 4. Name the sender and the recipient (MAIL, RCPT). A non-ASCII
	// address needs the server's SMTPUTF8 extension (RFC 6531).
	var options *gosmtp.MailOptions
	if !isASCII(message.FromAddress) || !isASCII(message.To) {
		options = &gosmtp.MailOptions{UTF8: true}
	}
	if err := client.Mail(message.FromAddress, options); err != nil {
		return refused(systememail.ReasonSender, err)
	}
	if err := client.Rcpt(message.To, nil); err != nil {
		return refused(systememail.ReasonRecipient, err)
	}

	// 5. Send the message (DATA). A refusal after the recipients is about
	// the message itself. For a test message that is nearly always the From
	// address, e.g. an unverified sender.
	data, err := client.Data()
	if err != nil {
		return refused(systememail.ReasonSender, err)
	}
	if _, err := data.Write(body); err != nil {
		return refused(systememail.ReasonSender, err)
	}
	// CloseWithResponse flushes the message's end and its final dot, then
	// reads the server's answer. A failed flush means the dot never left, so
	// the message was not accepted. Once reading starts, only a 4xx or 5xx
	// reply, which go-smtp documents as an *SMTPError, says the server did
	// not take it; any other failure, a garbled reply included, leaves the
	// message possibly accepted.
	conn.ended.Store(true)
	if _, err := data.CloseWithResponse(); err != nil {
		failure := refused(systememail.ReasonSender, err)
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

// signIn authenticates with PLAIN, or with LOGIN when the server offers only
// that. It returns errCleartext on a connection without TLS.
func signIn(client *gosmtp.Client, username, password string) error {
	// Neither go-smtp nor go-sasl checks for TLS. Saving settings forbids a
	// username without it; this holds even if a stored row does not.
	if _, ok := client.TLSConnectionState(); !ok {
		return errCleartext
	}
	switch {
	case client.SupportsAuth(sasl.Plain):
		return client.Auth(sasl.NewPlainClient("", username, password))
	// Some providers offer only LOGIN, e.g. Microsoft 365.
	case client.SupportsAuth(sasl.Login):
		return client.Auth(sasl.NewLoginClient(username, password))
	}
	return errNoSignIn
}

// signInError returns err, from a failed sign-in, without any text the
// server wrote: a refusal keeps only its code and an enhanced code within RFC
// 3463's ranges, a known error of inbok's, go-sasl's, or Go's is returned
// alone, without what wraps it, and anything else becomes errUnexpectedReply.
//
// DESIGN: a reply to AUTH can quote what inbok sent, the Base64 credentials
// with the SMTP password, and the mailer logs a reply. What leaves this step
// is a value inbok formats from integers, a fixed error, or Go's network
// error, so no parsing detail of go-smtp's, and no wrapper, decides what is
// kept. A server that accepts the sign-in and quotes it at a later step is
// not covered.
func signInError(err error) error {
	// go-smtp documents a server's error from Auth as an *SMTPError. A new
	// one with the same code keeps Permanent's decision in failed.
	if smtpErr, ok := errors.AsType[*gosmtp.SMTPError](err); ok {
		return &gosmtp.SMTPError{Code: smtpErr.Code, Message: enhancedCode(smtpErr.EnhancedCode)}
	}
	for _, known := range []error{
		errCleartext, errNoSignIn, errTooLong, sasl.ErrUnexpectedServerChallenge,
		io.EOF, io.ErrUnexpectedEOF, net.ErrClosed,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	// The network error Go wrote, e.g. a reset or a timeout, which refused
	// and Send label by.
	if netErr, ok := errors.AsType[net.Error](err); ok {
		return netErr
	}
	return errUnexpectedReply
}

// enhancedCode returns code as "class.subject.detail" when RFC 3463 allows
// it: class 2, 4, or 5, and a subject and a detail from 0 to 999.
// It returns "" otherwise, including for a code the server did not send.
func enhancedCode(code gosmtp.EnhancedCode) string {
	class, subject, detail := code[0], code[1], code[2]
	if class != 2 && class != 4 && class != 5 {
		return ""
	}
	if subject < 0 || subject > 999 || detail < 0 || detail > 999 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", class, subject, detail)
}

// compose returns message as RFC 5322 bytes: From with name and address, To,
// Subject, Date, Message-ID, and a quoted-printable UTF-8 text body.
func compose(message systememail.Message, now time.Time) ([]byte, error) {
	// go-message encodes or rejects line breaks in header values, so no field
	// can add a header.
	var header mail.Header
	header.SetAddressList("From", []*mail.Address{{Name: message.FromName, Address: message.FromAddress}})
	header.SetAddressList("To", []*mail.Address{{Address: message.To}})
	header.SetSubject(message.Subject)
	header.SetDate(now)
	// The sender's domain identifies inbok's messages; the host name might not.
	domain := message.FromAddress[strings.LastIndexByte(message.FromAddress, '@')+1:]
	if domain == "" || domain == message.FromAddress {
		domain = "localhost"
	}
	if err := header.GenerateMessageIDWithHostname(domain); err != nil {
		return nil, err
	}
	header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})

	var out bytes.Buffer
	writer, err := mail.CreateSingleInlineWriter(&out, header)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(writer, message.Body); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// validateSend returns errUnsafeInput for input that would change the SMTP
// session or cannot be dialed, and nil otherwise.
func validateSend(server systememail.Server, password string, message systememail.Message) error {
	// SMTP commands end at CRLF (RFC 5321), and go-smtp writes MAIL FROM:<%s>
	// unescaped: a line break would start a command, an angle bracket would
	// add parameters, and NUL is not allowed at all.
	if strings.ContainsAny(message.FromAddress+message.To, "\r\n\x00<>") {
		return errUnsafeInput
	}
	// SASL PLAIN separates its fields with NUL (RFC 4616).
	if strings.ContainsRune(server.Username+password, 0) {
		return errUnsafeInput
	}
	if server.Port < 1 || server.Port > 65535 {
		return errUnsafeInput
	}
	switch server.Security {
	case systememail.SecurityTLS, systememail.SecuritySTARTTLS, systememail.SecurityNone:
		return nil
	}
	return errUnsafeInput
}

// failed returns err as a [*systememail.SendError] with reason, permanent
// when err holds a 5xx reply.
func failed(reason systememail.Reason, err error) *systememail.SendError {
	// Checked here, where every SendError is made, so no step and no
	// rewrap, e.g. a timeout's, loses it. go-smtp documents a 4xx or 5xx
	// reply as an *SMTPError.
	smtpErr, ok := errors.AsType[*gosmtp.SMTPError](err)
	return &systememail.SendError{Reason: reason, Err: err, Permanent: ok && smtpErr.Code >= 500}
}

// refused returns err as a [*systememail.SendError] with reason, or with
// connect when the connection dropped or passed the reply limit.
func refused(reason systememail.Reason, err error) *systememail.SendError {
	// The step's reason applies only when the server answered it.
	var opErr *net.OpError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, errTooLong) || errors.As(err, &opErr) {
		return failed(systememail.ReasonConnect, err)
	}
	return failed(reason, err)
}

// watchedConn wraps the server connection to cap the bytes read, to record
// whether STARTTLS was sent, and to record whether reading started after
// the message ended, which happens only once its final dot was written.
//
// NOTE: that last record relies on go-smtp's DataCommand.CloseWithResponse
// flushing the message's end and its dot before it reads the reply, which
// its source does but its documentation does not promise. SMTP forces the
// order, since no reply to the message exists before the dot. If a release
// reads earlier, TestSendFinalWriteFailed fails; if it stops reading the
// connection, TestSendUnconfirmed does. Either way a mismatch mostly errs
// toward unconfirmed, which skips a retry rather than sending twice.
type watchedConn struct {
	net.Conn
	left      int64
	startTLS  atomic.Bool
	ended     atomic.Bool // the message's end is being written
	answering atomic.Bool // a read started after ended was set
}

// Read reads from the connection until maxReplyBytes have been read in total,
// then returns errTooLong. A read after the message's end records that the
// answer is awaited.
func (c *watchedConn) Read(b []byte) (int, error) {
	if c.ended.Load() {
		c.answering.Store(true)
	}
	if c.left <= 0 {
		return 0, errTooLong
	}
	// Shorten b so a single read cannot pass the limit.
	if int64(len(b)) > c.left {
		b = b[:c.left]
	}
	n, err := c.Conn.Read(b)
	c.left -= int64(n)
	return n, err
}

// Write writes b unchanged and records whether b is the STARTTLS command.
func (c *watchedConn) Write(b []byte) (int, error) {
	// go-smtp flushes each command in one write, so the command starts b.
	if bytes.HasPrefix(b, []byte("STARTTLS\r\n")) {
		c.startTLS.Store(true)
	}
	return c.Conn.Write(b)
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
