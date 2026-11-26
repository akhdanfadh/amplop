package smtp

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"

	"amplop/internal/systememail"
)

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
	// The sender's domain identifies amplop's messages; the host name
	// might not.
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
