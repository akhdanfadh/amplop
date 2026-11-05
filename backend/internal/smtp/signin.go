package smtp

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

var (
	// errNoSignIn means the server offers neither sign-in method inbok supports.
	errNoSignIn = errors.New("server offers no PLAIN or LOGIN sign-in")
	// errCleartext means a sign-in was attempted without TLS.
	errCleartext = errors.New("refusing to sign in without TLS")
	// errUnexpectedReply stands for a reply to the sign-in that inbok cannot
	// use, whose text it does not keep.
	errUnexpectedReply = errors.New("unexpected reply to the sign-in")
)

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
