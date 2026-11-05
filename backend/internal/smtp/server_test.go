package smtp

import (
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

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
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

// scriptedServer runs script for each connection on a free local port.
func scriptedServer(t *testing.T, script func(*textproto.Conn)) systememail.Server {
	t.Helper()

	return scriptedOver(t, func(conn net.Conn) net.Conn { return conn }, script)
}

// scriptedTLSServer is scriptedServer behind implicit TLS with certificate.
func scriptedTLSServer(t *testing.T, certificate tls.Certificate, script func(*textproto.Conn)) systememail.Server {
	t.Helper()

	server := scriptedOver(t, func(conn net.Conn) net.Conn {
		return tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}})
	}, script)
	server.Security = systememail.SecurityTLS
	return server
}

// scriptedOver runs script on each connection a local listener accepts,
// after wrap, and returns the plain server's settings.
func scriptedOver(t *testing.T, wrap func(net.Conn) net.Conn, script func(*textproto.Conn)) systememail.Server {
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
				script(textproto.NewConn(wrap(conn)))
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
