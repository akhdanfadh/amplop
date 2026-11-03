package smtp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

const (
	testUsername = "inbok@example.com"
	testPassword = "app password 7f3c"
	testTimeout  = 2 * time.Second
	testHello    = "mail.example.com"
)

// fakeServer is an in-process SMTP server that records what it accepts.
// Each field left zero behaves like a normal provider.
type fakeServer struct {
	security   systememail.Security
	noSTARTTLS bool     // starttls: advertise no STARTTLS
	mechanisms []string // nil: PLAIN
	rejectMail bool
	rejectRcpt bool
	rejectData bool

	mu       sync.Mutex
	received []received
}

type received struct {
	hello    string
	signedIn string
	from, to string
	data     []byte
}

// start serves on a free local port and returns the settings that reach it.
func (f *fakeServer) start(t *testing.T, certificate tls.Certificate) systememail.Server {
	t.Helper()

	// go-smtp creates the session at the greeting, so the name is known.
	server := gosmtp.NewServer(gosmtp.BackendFunc(func(c *gosmtp.Conn) (gosmtp.Session, error) {
		return &fakeSession{server: f, hello: c.Hostname()}, nil
	}))
	server.Domain = "localhost"
	server.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are expected
	config := &tls.Config{Certificates: []tls.Certificate{certificate}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	switch f.security {
	case systememail.SecurityTLS:
		listener = tls.NewListener(listener, config)
	case systememail.SecuritySTARTTLS:
		if !f.noSTARTTLS {
			server.TLSConfig = config
		}
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	port := listener.Addr().(*net.TCPAddr).Port
	return systememail.Server{Host: "127.0.0.1", Port: port, Security: f.security, Username: testUsername}
}

func (f *fakeServer) messages() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.received...)
}

type fakeSession struct {
	server   *fakeServer
	hello    string
	signedIn string
	current  received
}

func (s *fakeSession) AuthMechanisms() []string {
	if s.server.mechanisms == nil {
		return []string{sasl.Plain}
	}
	return s.server.mechanisms
}

func (s *fakeSession) Auth(mech string) (sasl.Server, error) {
	check := func(username, password string) error {
		if username != testUsername || password != testPassword {
			return gosmtp.ErrAuthFailed
		}
		s.signedIn = username
		return nil
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error { return check(username, password) }), nil
	case sasl.Login:
		// go-sasl has a LOGIN client but no LOGIN server.
		return &loginServer{check: check}, nil
	}
	return nil, gosmtp.ErrAuthUnknownMechanism
}

func (s *fakeSession) Mail(from string, _ *gosmtp.MailOptions) error {
	if s.server.rejectMail {
		return &gosmtp.SMTPError{Code: 553, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Sender not allowed"}
	}
	s.current = received{hello: s.hello, signedIn: s.signedIn, from: from}
	return nil
}

func (s *fakeSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if s.server.rejectRcpt {
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "No such user"}
	}
	s.current.to = to
	return nil
}

func (s *fakeSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if s.server.rejectData {
		return &gosmtp.SMTPError{Code: 554, EnhancedCode: gosmtp.EnhancedCode{5, 7, 1}, Message: "Email address is not verified"}
	}
	s.current.data = data
	s.server.mu.Lock()
	s.server.received = append(s.server.received, s.current)
	s.server.mu.Unlock()
	return nil
}

func (s *fakeSession) Reset()        {}
func (s *fakeSession) Logout() error { return nil }

// loginServer is the server side of the LOGIN mechanism.
type loginServer struct {
	check    func(username, password string) error
	username string
	step     int
}

// Next asks for the username, unless the client sent it with AUTH as go-sasl
// does, then for the password.
func (l *loginServer) Next(response []byte) ([]byte, bool, error) {
	l.step++
	switch {
	case l.step == 1 && response == nil:
		return []byte("Username:"), false, nil
	case l.username == "":
		l.username = string(response)
		return []byte("Password:"), false, nil
	}
	return nil, true, l.check(l.username, string(response))
}

// testCertificate is a self-signed certificate for 127.0.0.1 and a pool that trusts it.
func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "inbok test SMTP"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func testMessage() systememail.Message {
	return systememail.Message{
		FromAddress: "inbok@example.com",
		FromName:    "inbok",
		To:          "admin@example.org",
		Subject:     "Test message from inbok",
		Body:        "This is a test message from inbok.\nIt needs no reply.\n",
	}
}

// failure returns the reason a send failed and requires that it is a SendError.
func failure(t *testing.T, err error) systememail.Reason {
	t.Helper()

	var failed *systememail.SendError
	require.ErrorAs(t, err, &failed)
	return failed.Reason
}

func TestSendAccepted(t *testing.T) {
	certificate, roots := testCertificate(t)
	for _, c := range []struct {
		security   systememail.Security
		mechanisms []string
		signIn     bool
	}{
		{systememail.SecurityTLS, nil, true},
		{systememail.SecuritySTARTTLS, nil, true},
		{systememail.SecuritySTARTTLS, []string{sasl.Login}, true},
		{systememail.SecuritySTARTTLS, []string{sasl.Login, sasl.Plain}, true},
		{systememail.SecurityNone, nil, false},
	} {
		t.Run(string(c.security)+" "+strings.Join(c.mechanisms, ""), func(t *testing.T) {
			fake := &fakeServer{security: c.security, mechanisms: c.mechanisms}
			server := fake.start(t, certificate)
			if !c.signIn {
				server.Username = ""
			}
			sender := &Sender{roots: roots, timeout: testTimeout, hello: testHello}

			err := sender.Send(t.Context(), server, testPassword, testMessage())
			require.NoError(t, err)

			got := fake.messages()
			require.Len(t, got, 1)
			assert.Equal(t, testHello, got[0].hello)
			assert.Equal(t, server.Username, got[0].signedIn)
			assert.Equal(t, "inbok@example.com", got[0].from)
			assert.Equal(t, "admin@example.org", got[0].to)
		})
	}
}

// The greeting names inbok's public host; an IP address becomes an address
// literal (RFC 5321 section 4.1.3).
func TestNewSenderHello(t *testing.T) {
	for _, c := range []struct {
		host string
		want string
	}{
		{"mail.example.com", "mail.example.com"},
		{"localhost", "localhost"},
		{"127.0.0.1", "[127.0.0.1]"},
		{"::1", "[IPv6:::1]"},
		{"2001:db8::1", "[IPv6:2001:db8::1]"},
	} {
		t.Run(c.host, func(t *testing.T) {
			fake := &fakeServer{security: systememail.SecurityNone}
			server := fake.start(t, tls.Certificate{})
			server.Username = ""
			sender := NewSender(c.host)

			err := sender.Send(t.Context(), server, "", testMessage())
			require.NoError(t, err)

			got := fake.messages()
			require.Len(t, got, 1)
			assert.Equal(t, c.want, got[0].hello)
		})
	}
}

func TestSendMessage(t *testing.T) {
	certificate, roots := testCertificate(t)
	fake := &fakeServer{security: systememail.SecuritySTARTTLS}
	server := fake.start(t, certificate)
	message := testMessage()
	message.FromName = "inbok, Mail Ünit"

	err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, message)
	require.NoError(t, err)

	got := fake.messages()
	require.Len(t, got, 1)
	reader, err := mail.CreateReader(bytes.NewReader(got[0].data))
	require.NoError(t, err)
	from, err := reader.Header.AddressList("From")
	require.NoError(t, err)
	assert.Equal(t, []*mail.Address{{Name: "inbok, Mail Ünit", Address: "inbok@example.com"}}, from)
	to, err := reader.Header.AddressList("To")
	require.NoError(t, err)
	assert.Equal(t, []*mail.Address{{Address: "admin@example.org"}}, to)
	subject, err := reader.Header.Subject()
	require.NoError(t, err)
	assert.Equal(t, "Test message from inbok", subject)
	date, err := reader.Header.Date()
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), date, time.Minute)
	id, err := reader.Header.MessageID()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(id, "@example.com"), id)
	contentType, params, err := reader.Header.ContentType()
	require.NoError(t, err)
	assert.Equal(t, "text/plain", contentType)
	assert.Equal(t, "utf-8", params["charset"])
	part, err := reader.NextPart()
	require.NoError(t, err)
	body, err := io.ReadAll(part.Body)
	require.NoError(t, err)
	assert.Equal(t, "This is a test message from inbok.\r\nIt needs no reply.\r\n", string(body))
}

// Line breaks in a field are encoded or rejected; they never start a header.
func TestComposeInjection(t *testing.T) {
	message := testMessage()
	message.FromName = "inbok\r\nBcc: victim@example.net"
	message.Subject = "Test\r\nBcc: victim@example.net"

	data, err := compose(message, time.Now())
	require.NoError(t, err)

	reader, err := mail.CreateReader(bytes.NewReader(data))
	require.NoError(t, err)
	assert.False(t, reader.Header.Has("Bcc"))
}

// Input that would change the SMTP commands, or cannot be dialed, is refused
// before connecting, and is not reported as the server's refusal.
func TestSendUnsafeInput(t *testing.T) {
	certificate, roots := testCertificate(t)
	for _, c := range []struct {
		name   string
		change func(*systememail.Server, *string, *systememail.Message)
	}{
		{"recipient", func(_ *systememail.Server, _ *string, m *systememail.Message) {
			m.To = "admin@example.org>\r\nRCPT TO:<victim@example.net"
		}},
		{"sender", func(_ *systememail.Server, _ *string, m *systememail.Message) { m.FromAddress = "inbok@example.com\n" }},
		{"username", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Username = "inbok\x00admin" }},
		{"password", func(_ *systememail.Server, p *string, _ *systememail.Message) { *p = "app\x00password" }},
		{"ESMTP parameter", func(_ *systememail.Server, _ *string, m *systememail.Message) {
			m.To = "admin@example.org> NOTIFY=SUCCESS"
		}},
		{"port", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Port = 0 }},
		{"security", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Security = "ssl" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeServer{security: systememail.SecurityTLS}
			server, password, message := fake.start(t, certificate), testPassword, testMessage()
			c.change(&server, &password, &message)

			err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, password, message)
			require.ErrorIs(t, err, errUnsafeInput)

			var failed *systememail.SendError
			assert.NotErrorAs(t, err, &failed)
			assert.Empty(t, fake.messages())
		})
	}
}

func TestSendFailure(t *testing.T) {
	certificate, roots := testCertificate(t)
	for _, c := range []struct {
		name   string
		fake   *fakeServer
		change func(*systememail.Server, *Sender)
		want   systememail.Reason
	}{
		{"nothing listening", &fakeServer{security: systememail.SecurityNone}, func(server *systememail.Server, _ *Sender) {
			server.Port = closedPort(t)
		}, systememail.ReasonConnect},
		{"TLS certificate not trusted", &fakeServer{security: systememail.SecurityTLS}, func(_ *systememail.Server, sender *Sender) {
			sender.roots = x509.NewCertPool()
		}, systememail.ReasonTLS},
		{"STARTTLS certificate not trusted", &fakeServer{security: systememail.SecuritySTARTTLS}, func(_ *systememail.Server, sender *Sender) {
			sender.roots = x509.NewCertPool()
		}, systememail.ReasonTLS},
		{"TLS to a plain server", &fakeServer{security: systememail.SecurityNone}, func(server *systememail.Server, _ *Sender) {
			server.Security = systememail.SecurityTLS
		}, systememail.ReasonTLS},
		{"no supported sign-in", &fakeServer{security: systememail.SecurityTLS, mechanisms: []string{"XOAUTH2"}}, nil, systememail.ReasonAuth},
		{"no sign-in offered", &fakeServer{security: systememail.SecurityTLS, mechanisms: []string{}}, nil, systememail.ReasonAuth},
		{"sender refused", &fakeServer{security: systememail.SecurityTLS, rejectMail: true}, nil, systememail.ReasonSender},
		{"recipient refused", &fakeServer{security: systememail.SecurityTLS, rejectRcpt: true}, nil, systememail.ReasonRecipient},
		{"message refused", &fakeServer{security: systememail.SecurityTLS, rejectData: true}, nil, systememail.ReasonSender},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := c.fake.start(t, certificate)
			sender := &Sender{roots: roots, timeout: testTimeout, hello: testHello}
			if c.change != nil {
				c.change(&server, sender)
			}

			err := sender.Send(t.Context(), server, testPassword, testMessage())

			assert.Equal(t, c.want, failure(t, err))
			assert.NotContains(t, err.Error(), testPassword)
			assert.Empty(t, c.fake.messages())
		})
	}
}

// A refused password is an auth failure, and the error never repeats it.
func TestSendWrongPassword(t *testing.T) {
	certificate, roots := testCertificate(t)
	fake := &fakeServer{security: systememail.SecurityTLS}
	server := fake.start(t, certificate)
	const wrong = "wrong password 91ad"

	err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, wrong, testMessage())

	assert.Equal(t, systememail.ReasonAuth, failure(t, err))
	assert.NotContains(t, err.Error(), wrong)
	assert.Empty(t, fake.messages())
}

// A server that does not offer STARTTLS is a tls failure. Send detects it by
// go-smtp's error text, so this fails if an upgrade changes that text.
func TestSendSTARTTLSMissing(t *testing.T) {
	certificate, roots := testCertificate(t)
	fake := &fakeServer{security: systememail.SecuritySTARTTLS, noSTARTTLS: true}
	server := fake.start(t, certificate)

	err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonTLS, failure(t, err), "STARTTLS is required, never skipped")
	assert.Empty(t, fake.messages())
}

// A server that accepts the connection but never answers is a timeout,
// reported before the caller's own deadline.
func TestSendTimeout(t *testing.T) {
	for _, security := range []systememail.Security{systememail.SecurityTLS, systememail.SecuritySTARTTLS, systememail.SecurityNone} {
		t.Run(string(security), func(t *testing.T) {
			port := silentServer(t)
			sender := &Sender{timeout: 200 * time.Millisecond, hello: testHello}
			server := systememail.Server{Host: "127.0.0.1", Port: port, Security: security}
			start := time.Now()

			err := sender.Send(t.Context(), server, testPassword, testMessage())

			assert.Equal(t, systememail.ReasonTimeout, failure(t, err))
			assert.Less(t, time.Since(start), testTimeout)
		})
	}
}

// When the caller gives up, the server is not to blame.
func TestSendCanceled(t *testing.T) {
	port := silentServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	server := systememail.Server{Host: "127.0.0.1", Port: port, Security: systememail.SecurityNone}

	err := (&Sender{timeout: testTimeout, hello: testHello}).Send(ctx, server, testPassword, testMessage())

	require.ErrorIs(t, err, context.Canceled)
	var failed *systememail.SendError
	assert.NotErrorAs(t, err, &failed)
}

// closedPort returns a local port with nothing listening.
func closedPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

// silentServer accepts connections and never writes to them.
func silentServer(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			conn.Close()
		}
	})
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	number, err := strconv.Atoi(port)
	require.NoError(t, err)
	return number
}

// Signing in needs TLS even if stored settings say none; the password is
// never written to the connection.
func TestSendCleartextSignIn(t *testing.T) {
	var commands []string
	server := scriptedServer(t, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		for {
			line, err := conn.ReadLine()
			if err != nil {
				return
			}
			commands = append(commands, line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				reply(conn, "250-localhost", "250 AUTH PLAIN LOGIN")
			default:
				reply(conn, "250 OK")
			}
		}
	})
	server.Username = testUsername

	err := (&Sender{timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonAuth, failure(t, err))
	require.ErrorIs(t, err, errCleartext)
	for _, command := range commands {
		assert.NotContains(t, command, "AUTH")
	}
}

// Replies a go-smtp server would not send, from a scripted conversation.
func TestSendScripted(t *testing.T) {
	for _, c := range []struct {
		name     string
		security systememail.Security
		script   func(*textproto.Conn)
		want     systememail.Reason
	}{
		{"greeting refused", systememail.SecurityNone, func(conn *textproto.Conn) {
			reply(conn, "554 No SMTP service here")
		}, systememail.ReasonConnect},
		{"STARTTLS refused", systememail.SecuritySTARTTLS, func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250-localhost", "250 STARTTLS")
			expect(conn, "STARTTLS")
			reply(conn, "454 TLS not available")
		}, systememail.ReasonTLS},
		{"closed during the message", systememail.SecurityNone, func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250 localhost")
			expect(conn, "MAIL")
			reply(conn, "250 OK")
			expect(conn, "RCPT")
			reply(conn, "250 OK")
			expect(conn, "DATA")
			reply(conn, "354 Go ahead")
			expect(conn, "")
		}, systememail.ReasonConnect},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := scriptedServer(t, c.script)
			server.Security = c.security

			err := (&Sender{timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

			assert.Equal(t, c.want, failure(t, err))
		})
	}
}

// A send is unconfirmed only when the server got the whole message and no
// readable answer came back, so the message may have been accepted. A 4xx
// or 5xx refusal, and any failure before the message ends, is not.
func TestSendUnconfirmed(t *testing.T) {
	// upToMessage answers everything up to and including the message, then
	// runs after.
	upToMessage := func(after func(*textproto.Conn)) func(*textproto.Conn) {
		return func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250 localhost")
			expect(conn, "MAIL")
			reply(conn, "250 OK")
			expect(conn, "RCPT")
			reply(conn, "250 OK")
			expect(conn, "DATA")
			reply(conn, "354 Go ahead")
			if _, err := conn.ReadDotBytes(); err != nil {
				return
			}
			after(conn)
		}
	}
	for _, c := range []struct {
		name        string
		script      func(*textproto.Conn)
		reason      systememail.Reason
		unconfirmed bool
	}{
		{"closed after the message", upToMessage(func(*textproto.Conn) {}), systememail.ReasonConnect, true},
		{"silent after the message", upToMessage(func(*textproto.Conn) { <-t.Context().Done() }), systememail.ReasonTimeout, true},
		{"refused the message", upToMessage(func(conn *textproto.Conn) { reply(conn, "554 Rejected") }), systememail.ReasonSender, false},
		{"garbled answer", upToMessage(func(conn *textproto.Conn) { reply(conn, "queued, thanks") }), systememail.ReasonSender, true},
		{"unexpected success code", upToMessage(func(conn *textproto.Conn) { reply(conn, "354 Go ahead") }), systememail.ReasonSender, true},
		{"overlong answer", upToMessage(func(conn *textproto.Conn) { reply(conn, "250 "+strings.Repeat("x", 3000)) }), systememail.ReasonSender, true},
		{"refused the recipient", func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250 localhost")
			expect(conn, "MAIL")
			reply(conn, "250 OK")
			expect(conn, "RCPT")
			reply(conn, "550 No such user")
		}, systememail.ReasonRecipient, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := scriptedServer(t, c.script)

			err := (&Sender{timeout: 500 * time.Millisecond, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

			var failed *systememail.SendError
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, c.reason, failed.Reason)
			assert.Equal(t, c.unconfirmed, failed.Unconfirmed)
		})
	}
}

// failingWrites wraps a connection so writes fail once the message's final
// dot is among them, as when the connection breaks while inbok flushes the
// message's end.
type failingWrites struct {
	net.Conn
}

func (c failingWrites) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("\r\n.\r\n")) {
		return 0, net.ErrClosed
	}
	return c.Conn.Write(b)
}

// A failure while flushing the message's end means the final dot never left
// inbok, so the server cannot have accepted the message: not unconfirmed.
func TestSendFinalWriteFailed(t *testing.T) {
	server := scriptedServer(t, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		expect(conn, "EHLO")
		reply(conn, "250 localhost")
		expect(conn, "MAIL")
		reply(conn, "250 OK")
		expect(conn, "RCPT")
		reply(conn, "250 OK")
		expect(conn, "DATA")
		reply(conn, "354 Go ahead")
		<-t.Context().Done()
	})
	sender := &Sender{timeout: testTimeout, hello: testHello, dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		return failingWrites{conn}, err
	}}

	err := sender.Send(t.Context(), server, testPassword, testMessage())

	var failed *systememail.SendError
	require.ErrorAs(t, err, &failed)
	assert.False(t, failed.Unconfirmed)
}

// A caller giving up while inbok waits for the answer to a complete message
// still learns the send is unconfirmed; one giving up earlier does not.
func TestSendCanceledUnconfirmed(t *testing.T) {
	// Given a server that has the whole message and never answers, and a
	// caller that gives up as soon as it does.
	ctx, cancel := context.WithCancel(t.Context())
	server := scriptedServer(t, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		expect(conn, "EHLO")
		reply(conn, "250 localhost")
		expect(conn, "MAIL")
		reply(conn, "250 OK")
		expect(conn, "RCPT")
		reply(conn, "250 OK")
		expect(conn, "DATA")
		reply(conn, "354 Go ahead")
		if _, err := conn.ReadDotBytes(); err != nil {
			return
		}
		cancel()
		<-t.Context().Done()
	})

	// When it sends.
	err := (&Sender{timeout: testTimeout, hello: testHello}).Send(ctx, server, testPassword, testMessage())

	// Then the error is still the cancellation, and unconfirmed.
	require.ErrorIs(t, err, context.Canceled)
	var failed *systememail.SendError
	require.ErrorAs(t, err, &failed)
	assert.True(t, failed.Unconfirmed)
}

// A server that never stops replying is cut off at the size limit.
func TestSendReplyLimit(t *testing.T) {
	server := scriptedServer(t, func(conn *textproto.Conn) {
		for conn.PrintfLine("220-hello") == nil {
		}
	})

	err := (&Sender{timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonConnect, failure(t, err))
	require.ErrorIs(t, err, errTooLong)
}

// A server that accepts the message but never answers QUIT does not hold the send.
func TestSendQuitUnanswered(t *testing.T) {
	server := scriptedServer(t, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		for {
			line, err := conn.ReadLine()
			if err != nil || line == "QUIT" {
				<-t.Context().Done()
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				reply(conn, "250 localhost")
			case line == "DATA":
				reply(conn, "354 Go ahead")
				if _, err := conn.ReadDotBytes(); err != nil {
					return
				}
				reply(conn, "250 Queued")
			default:
				reply(conn, "250 OK")
			}
		}
	})
	start := time.Now()

	err := (&Sender{timeout: 5 * time.Second, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())
	require.NoError(t, err)

	assert.Less(t, time.Since(start), 3*time.Second)
}

// A caller with less time left than the exchange limit still gets a timeout,
// before its own deadline.
func TestSendCallerDeadline(t *testing.T) {
	port := silentServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	server := systememail.Server{Host: "127.0.0.1", Port: port, Security: systememail.SecurityNone}

	err := (&Sender{timeout: exchangeTimeout, hello: testHello}).Send(ctx, server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonTimeout, failure(t, err))
	assert.NoError(t, ctx.Err(), "the caller still has time to record the result")
}

// A timeout's error names the step that was waiting, without nesting its
// SendError.
func TestSendTimeoutCause(t *testing.T) {
	port := silentServer(t)
	server := systememail.Server{Host: "127.0.0.1", Port: port, Security: systememail.SecurityNone}

	err := (&Sender{timeout: 100 * time.Millisecond, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	var failed *systememail.SendError
	require.ErrorAs(t, err, &failed)
	assert.NotErrorAs(t, failed.Err, new(*systememail.SendError))
	assert.Contains(t, failed.Err.Error(), "waiting at connect")
}

// The Message-ID needs a domain even if the From address has none.
func TestComposeMessageID(t *testing.T) {
	message := testMessage()
	message.FromAddress = "inbok"

	data, err := compose(message, time.Now())
	require.NoError(t, err)

	reader, err := mail.CreateReader(bytes.NewReader(data))
	require.NoError(t, err)
	id, err := reader.Header.MessageID()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(id, "@localhost"), id)
}

// scriptedServer runs script for each connection on a free local port.
func scriptedServer(t *testing.T, script func(*textproto.Conn)) systememail.Server {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				script(textproto.NewConn(conn))
			}()
		}
	}()
	return systememail.Server{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Security: systememail.SecurityNone}
}

func reply(conn *textproto.Conn, lines ...string) {
	for _, line := range lines {
		if err := conn.PrintfLine("%s", line); err != nil {
			return
		}
	}
}

// expect reads one command; a script stops at the first unexpected one.
func expect(conn *textproto.Conn, prefix string) {
	line, err := conn.ReadLine()
	if err != nil || !strings.HasPrefix(line, prefix) {
		conn.Close()
	}
}
