package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

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

// A server that does not offer STARTTLS is a tls failure. [Sender.Send] detects
// it by go-smtp's error text, so this fails if an upgrade changes that text.
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

// A 5xx refusal is permanent, whatever the step; a 4xx refusal or a dropped
// connection is not. None of them is unconfirmed.
func TestSendPermanent(t *testing.T) {
	// upTo answers everything before command, then answers it with answer.
	upTo := func(command string, answer func(*textproto.Conn)) func(*textproto.Conn) {
		return func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250 localhost")
			for _, next := range []string{"MAIL", "RCPT", "DATA"} {
				expect(conn, next)
				if next == command {
					answer(conn)
					return
				}
				reply(conn, "250 OK")
			}
		}
	}
	refuse := func(line string) func(*textproto.Conn) {
		return func(conn *textproto.Conn) { reply(conn, line) }
	}
	for _, c := range []struct {
		name      string
		security  systememail.Security
		script    func(*textproto.Conn)
		reason    systememail.Reason
		permanent bool
	}{
		{"greeting refused", systememail.SecurityNone, refuse("554 No SMTP service here"), systememail.ReasonConnect, true},
		{"STARTTLS refused", systememail.SecuritySTARTTLS, func(conn *textproto.Conn) {
			reply(conn, "220 localhost")
			expect(conn, "EHLO")
			reply(conn, "250-localhost", "250 STARTTLS")
			expect(conn, "STARTTLS")
			reply(conn, "554 TLS not available")
		}, systememail.ReasonTLS, true},
		{"sender refused", systememail.SecurityNone, upTo("MAIL", refuse("553 Sender not allowed")), systememail.ReasonSender, true},
		{"recipient refused", systememail.SecurityNone, upTo("RCPT", refuse("550 No such user")), systememail.ReasonRecipient, true},
		{"recipient refused for now", systememail.SecurityNone, upTo("RCPT", refuse("450 Greylisted, try again later")), systememail.ReasonRecipient, false},
		{"closed at the recipient", systememail.SecurityNone, upTo("RCPT", func(*textproto.Conn) {}), systememail.ReasonConnect, false},
		{"message refused", systememail.SecurityNone, upTo("DATA", func(conn *textproto.Conn) {
			reply(conn, "354 Go ahead")
			if _, err := conn.ReadDotBytes(); err != nil {
				return
			}
			reply(conn, "554 Rejected")
		}), systememail.ReasonSender, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := scriptedServer(t, c.script)
			server.Security = c.security

			err := (&Sender{timeout: 500 * time.Millisecond, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

			var failed *systememail.SendError
			require.ErrorAs(t, err, &failed)
			assert.Equal(t, c.reason, failed.Reason)
			assert.Equal(t, c.permanent, failed.Permanent)
			assert.False(t, failed.Unconfirmed)
		})
	}
}

// A 5xx refusal stays permanent when time runs out afterwards: go-smtp
// answers a refused sign-in with "*" and waits, and a server that stays
// silent turns the send into a timeout.
func TestSendPermanentTimeout(t *testing.T) {
	certificate, roots := testCertificate(t)
	server := scriptedTLSServer(t, certificate, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		expect(conn, "EHLO")
		reply(conn, "250-localhost", "250 AUTH PLAIN")
		expect(conn, "AUTH")
		reply(conn, "535 Authentication failed")
		expect(conn, "*")
		<-t.Context().Done()
	})
	server.Username = testUsername

	err := (&Sender{roots: roots, timeout: 500 * time.Millisecond, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	var failed *systememail.SendError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, systememail.ReasonTimeout, failed.Reason)
	assert.True(t, failed.Permanent)
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

// [Sender.WithTimeout] sets the limit of a copy, leaving the original's alone.
func TestSenderWithTimeout(t *testing.T) {
	port := silentServer(t)
	server := systememail.Server{Host: "127.0.0.1", Port: port, Security: systememail.SecurityNone}
	original := NewSender("inbok.example.com")
	start := time.Now()

	err := original.WithTimeout(100*time.Millisecond).Send(t.Context(), server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonTimeout, failure(t, err))
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, exchangeTimeout, original.timeout)
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
