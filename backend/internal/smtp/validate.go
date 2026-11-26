package smtp

import (
	"errors"
	"strings"

	"amplop/internal/systememail"
)

// errUnsafeInput means the input would change the SMTP session or cannot be dialed.
var errUnsafeInput = errors.New("address, credentials, port, or security mode cannot be sent")

// validateSend returns [errUnsafeInput] for input that would change the SMTP
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
	// TCP ports are 16 bits, and port 0 is reserved (RFC 6335).
	if server.Port < 1 || server.Port > 65535 {
		return errUnsafeInput
	}
	switch server.Security {
	case systememail.SecurityTLS, systememail.SecuritySTARTTLS, systememail.SecurityNone:
		return nil
	}
	return errUnsafeInput
}
