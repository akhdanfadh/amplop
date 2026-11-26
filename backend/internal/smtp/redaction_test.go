package smtp

import (
	"amplop/internal/systememail"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reply after the sign-in keeps its text, except what amplop sent that it
// quotes: the sign-in's Base64 lines become [credentials] and the
// recipient's address, in any letter case, becomes [recipient]. A refusal
// keeps its reason and whether it is permanent.
func TestSendRedactsReplies(t *testing.T) {
	certificate, roots := testCertificate(t)
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	plain := encode("\x00" + testUsername + "\x00" + testPassword)
	plainSignIn := func(conn *textproto.Conn) {
		reply(conn, "250-localhost", "250 AUTH PLAIN")
		expect(conn, "AUTH PLAIN "+plain)
		reply(conn, "235 2.7.0 Accepted")
	}
	loginSignIn := func(conn *textproto.Conn) {
		reply(conn, "250-localhost", "250 AUTH LOGIN")
		expect(conn, "AUTH LOGIN "+encode(testUsername))
		reply(conn, "334 "+encode("Password:"))
		expect(conn, encode(testPassword))
		reply(conn, "235 2.7.0 Accepted")
	}
	for _, c := range []struct {
		name      string
		signIn    func(*textproto.Conn)
		after     func(*textproto.Conn)
		reason    systememail.Reason
		want      string
		permanent bool
	}{
		{"sender refusal quoting the PLAIN line", plainSignIn, func(conn *textproto.Conn) {
			expect(conn, "MAIL")
			reply(conn, "500 5.5.1 unknown command: AUTH PLAIN "+plain)
		}, systememail.ReasonSender, "SMTP error 500: unknown command: AUTH PLAIN [credentials]", true},
		{"sender refusal quoting both LOGIN lines", loginSignIn, func(conn *textproto.Conn) {
			expect(conn, "MAIL")
			reply(conn, "451-4.3.0 you sent "+encode(testUsername), "451 4.3.0 then "+encode(testPassword))
		}, systememail.ReasonSender, "SMTP error 451: you sent [credentials]\nthen [credentials]", false},
		{"reply without a code quoting the PLAIN line", plainSignIn, func(conn *textproto.Conn) {
			expect(conn, "MAIL")
			reply(conn, "unknown command AUTH PLAIN "+plain)
		}, systememail.ReasonSender, errGarbledReply.Error(), false},
		{"recipient refusal quoting the address in another case", plainSignIn, func(conn *textproto.Conn) {
			expect(conn, "MAIL")
			reply(conn, "250 OK")
			expect(conn, "RCPT")
			reply(conn, "550 5.1.1 <ADMIN@Example.org> unknown, also admin@example.org")
		}, systememail.ReasonRecipient, "SMTP error 550: <[recipient]> unknown, also [recipient]", true},
		{"refusal of the message quoting the address", plainSignIn, func(conn *textproto.Conn) {
			expect(conn, "MAIL")
			reply(conn, "250 OK")
			expect(conn, "RCPT")
			reply(conn, "250 OK")
			expect(conn, "DATA")
			reply(conn, "354 Go ahead")
			for {
				line, err := conn.ReadLine()
				if err != nil || line == "." {
					break
				}
			}
			reply(conn, "554 5.7.1 message for admin@example.org rejected")
		}, systememail.ReasonSender, "SMTP error 554: message for [recipient] rejected", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := scriptedTLSServer(t, certificate, func(conn *textproto.Conn) {
				reply(conn, "220 localhost")
				expect(conn, "EHLO")
				c.signIn(conn)
				c.after(conn)
			})
			server.Username = testUsername
			sender := &Sender{roots: roots, timeout: testTimeout, hello: testHello}

			err := sender.Send(t.Context(), server, testPassword, testMessage())

			failed, ok := errors.AsType[*systememail.SendError](err)
			require.True(t, ok, "err = %v", err)
			assert.Equal(t, c.reason, failed.Reason)
			assert.Equal(t, c.permanent, failed.Permanent)
			assert.Equal(t, c.want, failed.Err.Error())
		})
	}
}

// An error the server did not write is returned as is, even when its text
// holds a string amplop sent, so a dropped connection still reads as one.
func TestRedactionKeepsOtherErrors(t *testing.T) {
	// LOGIN's line for the username w_4 is d180, which the server's address
	// below holds too.
	sent := redaction{credentials: []string{"d180"}, recipient: "admin@example.org"}
	reset := &net.OpError{Op: "read", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("2001:db8::d180"), Port: 587}, Err: errors.New("connection reset by peer")}

	assert.Same(t, io.EOF, sent.redact(io.EOF))
	assert.Same(t, error(reset), sent.redact(reset))
}

// A malformed reply keeps none of its text: Go quotes it with escapes, which
// can hide a quoted recipient from matching.
func TestRedactionGarbledReply(t *testing.T) {
	sent := redaction{recipient: "a\uE000@example.org"}

	err := sent.redact(textproto.ProtocolError(fmt.Sprintf("short response: %q", "550 a\uE000@example.org")))

	require.ErrorIs(t, err, errGarbledReply)
}

// Without a recipient nothing is matched as one.
func TestRedactionWithoutRecipient(t *testing.T) {
	err := errors.New("550 refused")

	assert.Same(t, err, redaction{}.redact(err))
}

// Every match is found in the original reply, so one string inside or
// overlapping another leaves no part of either: LOGIN's username line can
// start its password line.
func TestRedactionText(t *testing.T) {
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	// LOGIN's lines for the username abc and the password abcdef: YWJj starts YWJjZGVm.
	login := redaction{credentials: []string{encode("abc"), encode("abcdef")}, recipient: "a@example.org"}
	for _, c := range []struct {
		name  string
		sent  redaction
		reply string
		want  string
	}{
		{"password line holding the username line", login,
			"500 unknown command " + encode("abcdef"), "500 unknown command [credentials]"},
		{"both lines, apart", login,
			encode("abc") + " then " + encode("abcdef"), "[credentials] then [credentials]"},
		{"partial overlap", redaction{credentials: []string{"XYZ", "ZW"}},
			"quoted XYZW here", "quoted [credentials] here"},
		{"recipient touching a line", login,
			"550 " + encode("abc") + "A@EXAMPLE.ORG unknown", "550 [credentials] unknown"},
		{"recipient holding a line", redaction{credentials: []string{encode("w_4")}, recipient: encode("w_4") + "@example.org"},
			"550 " + encode("w_4") + "@example.org unknown", "550 [credentials] unknown"},
		{"recipient with regexp characters", redaction{recipient: "a+b@example.org"},
			"550 <A+B@example.org>, not aab@example.org", "550 <[recipient]>, not aab@example.org"},
		{"nothing sent", redaction{}, "550 refused", "550 refused"},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.sent.text(c.reply))
		})
	}
}
