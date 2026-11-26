package smtp

import (
	"errors"
	"io"
	"net"

	gosmtp "github.com/emersion/go-smtp"

	"amplop/internal/systememail"
)

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
