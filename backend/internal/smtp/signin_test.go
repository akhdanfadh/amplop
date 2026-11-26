package smtp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/systememail"
)

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

// A failed sign-in keeps no text the server wrote, since a reply at this step
// can quote the credentials amplop sent: a refusal keeps only its codes,
// amplop's, go-sasl's, and Go's own errors keep their fixed text, and any
// other reply becomes [errUnexpectedReply].
func TestSendSignInReply(t *testing.T) {
	certificate, roots := testCertificate(t)
	// The credentials amplop sends with AUTH PLAIN, as a server could quote them.
	credentials := base64.StdEncoding.EncodeToString([]byte("\x00" + testUsername + "\x00" + testPassword))
	refuse := func(line string) func(*textproto.Conn) {
		return func(conn *textproto.Conn) { reply(conn, line) }
	}
	for _, c := range []struct {
		name      string
		answer    func(*textproto.Conn)
		reason    systememail.Reason
		want      string
		permanent bool
	}{
		{"refusal", refuse("535 5.7.8 Authentication failed for AUTH PLAIN " + credentials),
			systememail.ReasonAuth, "SMTP error 535: 5.7.8", true},
		{"refusal without an enhanced code", refuse("454 Try again, you sent " + credentials),
			systememail.ReasonAuth, "SMTP error 454", false},
		{"enhanced code out of range", refuse("535 5.7.123456789 " + credentials),
			systememail.ReasonAuth, "SMTP error 535", true},
		{"multi-line refusal", func(conn *textproto.Conn) {
			reply(conn, "535-5.7.8 You sent "+credentials, "535 5.7.8 Denied")
		}, systememail.ReasonAuth, "SMTP error 535: 5.7.8", true},
		{"reply without a code", refuse(credentials),
			systememail.ReasonAuth, errUnexpectedReply.Error(), false},
		{"challenge, then a quoting reply to the abort", func(conn *textproto.Conn) {
			reply(conn, "334 ")
			expect(conn, "*")
			reply(conn, "501 5.0.0 Cancelled "+credentials)
		}, systememail.ReasonAuth, sasl.ErrUnexpectedServerChallenge.Error(), false},
		{"connection closed", func(*textproto.Conn) {},
			systememail.ReasonConnect, io.EOF.Error(), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := scriptedTLSServer(t, certificate, func(conn *textproto.Conn) {
				reply(conn, "220 localhost")
				expect(conn, "EHLO")
				reply(conn, "250-localhost", "250 AUTH PLAIN")
				expect(conn, "AUTH")
				c.answer(conn)
			})
			server.Username = testUsername
			sender := &Sender{roots: roots, timeout: testTimeout, hello: testHello}

			err := sender.Send(t.Context(), server, testPassword, testMessage())

			failed, ok := errors.AsType[*systememail.SendError](err)
			require.True(t, ok, "err = %v", err)
			assert.Equal(t, c.reason, failed.Reason)
			assert.Equal(t, c.permanent, failed.Permanent)
			assert.Equal(t, c.want, failed.Err.Error())
			assert.NotContains(t, err.Error(), credentials)
			assert.NotContains(t, err.Error(), testPassword)
		})
	}
}

// An error that wraps a known one keeps only the known one, so a wrapper's
// text cannot pass.
func TestSignInErrorWrapped(t *testing.T) {
	err := signInError(fmt.Errorf("server said %s: %w", "secret", io.EOF))

	assert.Equal(t, io.EOF, err)
}

// An enhanced code is kept only within RFC 3463's ranges.
func TestEnhancedCode(t *testing.T) {
	for code, want := range map[gosmtp.EnhancedCode]string{
		{5, 7, 8}:     "5.7.8",
		{2, 0, 0}:     "2.0.0",
		{4, 999, 999}: "4.999.999",
		{0, 0, 0}:     "",
		{3, 7, 8}:     "",
		{9, 7, 8}:     "",
		{5, -7, 8}:    "",
		{5, 7, -8}:    "",
		{5, 1000, 8}:  "",
		{5, 7, 1000}:  "",
	} {
		assert.Equal(t, want, enhancedCode(code), "code %v", code)
	}
}

// amplop's own sign-in errors keep their text: no server wrote it.
func TestSendSignInOwnError(t *testing.T) {
	certificate, roots := testCertificate(t)
	server := scriptedTLSServer(t, certificate, func(conn *textproto.Conn) {
		reply(conn, "220 localhost")
		expect(conn, "EHLO")
		reply(conn, "250-localhost", "250 AUTH CRAM-MD5")
	})
	server.Username = testUsername

	err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, testPassword, testMessage())

	assert.Equal(t, systememail.ReasonAuth, failure(t, err))
	require.ErrorIs(t, err, errNoSignIn)
}
