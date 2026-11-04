package systememail

import "context"

// Server is where and how to send. It holds no secret: the password is an
// argument to [Sender.Send].
type Server struct {
	Host     string
	Port     int
	Security Security
	Username string // empty: send without signing in
}

// Message is one plain-text message to one recipient.
type Message struct {
	FromAddress string
	FromName    string
	To          string
	Subject     string
	Body        string
}

// Reason names the step of the SMTP session (RFC 5321) that failed, which
// points an administrator at the setting to check.
//
// NOTE: it names the step, not the cause, because servers use SMTP reply
// codes inconsistently. So a step can point at the wrong setting: a refusal
// at or after DATA is sender, even when content filtering caused it; a
// non-ASCII recipient on a server without SMTPUTF8 fails at MAIL and is
// sender; STARTTLS or none aimed at an implicit-TLS port such as 465 waits
// and becomes timeout. Whether a refusal is temporary (4xx) or permanent
// (5xx) is [SendError.Permanent].
type Reason string

const (
	ReasonConnect   Reason = "connect"   // No connection, or the server refused to talk.
	ReasonTLS       Reason = "tls"       // The connection could not be protected.
	ReasonAuth      Reason = "auth"      // The server refused the username or password.
	ReasonSender    Reason = "sender"    // The server refused the From address or the message.
	ReasonRecipient Reason = "recipient" // The server refused the recipient.
	ReasonTimeout   Reason = "timeout"   // The server took too long to answer.
)

// SendError is a send that failed at Reason's step. Show users the Reason;
// Err is for logs only, since it can hold the server's reply, which can quote
// the message. Unconfirmed is set when the whole message, final dot
// included, was written and the send then failed without a 4xx or 5xx
// refusal: the server may have accepted it, so sending it again could
// deliver it twice. Permanent is set when the server refused with a 5xx
// reply, which RFC 5321 says the same command gets again; a caller that
// retries can stop.
//
// NOTE: servers do not always use codes as RFC 5321 intends, e.g. 552 for
// a full mailbox, which can clear. Permanent reports the code, not a
// promise.
type SendError struct {
	Reason      Reason
	Err         error
	Unconfirmed bool
	Permanent   bool
}

func (e *SendError) Error() string {
	return "send failed at " + string(e.Reason) + ": " + e.Err.Error()
}

func (e *SendError) Unwrap() error { return e.Err }

// Sender delivers one message while the caller waits, signing in with
// password when server.Username is set. It returns a [*SendError] when the
// server or network fails, ctx's error when the caller gives up, and a plain
// error for unsafe input or a message that cannot be built.
type Sender interface {
	Send(ctx context.Context, server Server, password string, message Message) error
}
