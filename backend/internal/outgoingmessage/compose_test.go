package outgoingmessage

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Line limits of the composed message.
const (
	// maxEncodedLine is the most characters in a quoted-printable line, CRLF
	// not counted (RFC 2045, section 6.7).
	maxEncodedLine = 76
	// maxLine is the most octets in any line, CRLF not counted (RFC 5322,
	// section 2.1.1).
	maxLine = 998
)

// testMessage is a saved message every rule accepts.
func testMessage() Message {
	return Message{
		ID:                 uuid.MustParse("6f1c2b0e-4d3a-4b8e-9c1f-2a7d5e8b9c0d"),
		ConnectedAccountID: 5,
		From:               "me@gmail.com",
		Content:            testContent(),
		InbokMessageID:     "<LZ3K2.9F1Q@gmail.com>",
		Status:             StatusWaiting,
		CreatedAt:          time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC),
	}
}

// readComposed parses data as a mail message and returns its header and
// decoded text body.
func readComposed(t *testing.T, data []byte) (mail.Header, string) {
	t.Helper()
	reader, err := mail.CreateReader(bytes.NewReader(data))
	require.NoError(t, err)
	part, err := reader.NextPart()
	require.NoError(t, err)
	body, err := io.ReadAll(part.Body)
	require.NoError(t, err)
	return reader.Header, string(body)
}

// addresses returns the addresses in header's field key.
func addresses(t *testing.T, header mail.Header, key string) []string {
	t.Helper()
	list, err := header.AddressList(key)
	require.NoError(t, err)
	out := make([]string, 0, len(list))
	for _, address := range list {
		assert.Empty(t, address.Name, "no display names")
		out = append(out, address.Address)
	}
	return out
}

func TestCompose(t *testing.T) {
	message := testMessage()

	data, err := compose(message)
	require.NoError(t, err)

	header, body := readComposed(t, data)
	assert.Equal(t, []string{"me@gmail.com"}, addresses(t, header, "From"))
	assert.Equal(t, []string{"ann@example.com"}, addresses(t, header, "To"))
	assert.Equal(t, []string{"ben@example.com"}, addresses(t, header, "Cc"))
	// Gmail reads Bcc from the message to deliver it.
	assert.Equal(t, []string{"cat@example.com"}, addresses(t, header, "Bcc"))
	subject, err := header.Subject()
	require.NoError(t, err)
	assert.Equal(t, "Lunch on Friday", subject)
	date, err := header.Date()
	require.NoError(t, err)
	assert.True(t, message.CreatedAt.Equal(date), "the Date is when the member sent it, so every attempt builds the same bytes")
	id, err := header.MessageID()
	require.NoError(t, err)
	assert.Equal(t, "LZ3K2.9F1Q@gmail.com", id)
	// Gmail keeps this header though it replaces the Message-ID.
	assert.Equal(t, "<LZ3K2.9F1Q@gmail.com>", header.Get("X-Inbok-Message-Id"))
	mediaType, params, err := header.ContentType()
	require.NoError(t, err)
	assert.Equal(t, "text/plain", mediaType)
	assert.Equal(t, "utf-8", params["charset"])
	assert.Equal(t, "Are you free?\r\nI booked a table.", body)
}

func TestComposeSame(t *testing.T) {
	first, err := compose(testMessage())
	require.NoError(t, err)

	second, err := compose(testMessage())
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
}

func TestComposeEmptyFields(t *testing.T) {
	message := testMessage()
	message.To = []string{}
	message.Cc = []string{}
	message.Subject = ""
	message.Body = ""

	data, err := compose(message)
	require.NoError(t, err)

	header, body := readComposed(t, data)
	assert.False(t, header.Has("To"))
	assert.False(t, header.Has("Cc"))
	assert.Equal(t, []string{"cat@example.com"}, addresses(t, header, "Bcc"))
	assert.Empty(t, body)
}

// Non-ASCII text is encoded: the subject in RFC 2047 words, the body in
// quoted-printable, and every line stays within its limit. Addresses keep
// their UTF-8 (RFC 6532).
func TestComposeEncoding(t *testing.T) {
	message := testMessage()
	message.To = []string{"Élise@bücher.de"}
	message.Subject = strings.Repeat("Café crème, ", 30)
	message.Body = "Résumé attached.\n" + strings.Repeat("ü", 2000)

	data, err := compose(message)
	require.NoError(t, err)

	headerText, _, found := bytes.Cut(data, []byte("\r\n\r\n"))
	require.True(t, found)
	_, bodyText, _ := bytes.Cut(data, []byte("\r\n\r\n"))
	for line := range strings.SplitSeq(string(bodyText), "\r\n") {
		assert.LessOrEqual(t, len(line), maxEncodedLine)
		assert.NotContains(t, line, "ü")
	}
	for line := range strings.SplitSeq(string(headerText), "\r\n") {
		assert.LessOrEqual(t, len(line), maxLine)
	}
	assert.NotContains(t, string(headerText), "Café", "the subject is encoded")
	header, body := readComposed(t, data)
	subject, err := header.Subject()
	require.NoError(t, err)
	assert.Equal(t, message.Subject, subject)
	// RFC 6532 lets an address hold UTF-8 as it is.
	assert.Equal(t, []string{"Élise@bücher.de"}, addresses(t, header, "To"))
	assert.Equal(t, "Résumé attached.\r\n"+strings.Repeat("ü", 2000), body)
}

// The longest address the rules allow, 254 octets of UTF-8, reaches every
// recipient field whole: no fold splits it.
func TestComposeLongAddress(t *testing.T) {
	longest := "ab" + strings.Repeat("\U0001F600", 62) + "@b.c"
	require.Len(t, longest, 254)
	message := testMessage()
	message.To = []string{longest, "ann@example.com"}
	message.Cc = []string{longest}
	message.Bcc = []string{"cat@example.com", longest}

	data, err := compose(message)
	require.NoError(t, err)

	assert.True(t, utf8.Valid(data))
	header, _ := readComposed(t, data)
	assert.Equal(t, message.To, addresses(t, header, "To"))
	assert.Equal(t, message.Cc, addresses(t, header, "Cc"))
	assert.Equal(t, message.Bcc, addresses(t, header, "Bcc"))
}

// A subject that only looks like an RFC 2047 word reads as the member
// typed it.
func TestComposeEncodedSubject(t *testing.T) {
	cases := []string{
		"=?UTF-8?Q?Confidential?=",
		"Re: =?utf-8?b?SGk=?= and more",
		"=?" + strings.Repeat("é", 200) + "?=",
	}

	for _, subject := range cases {
		t.Run(subject[:10], func(t *testing.T) {
			message := testMessage()
			message.Subject = subject

			data, err := compose(message)
			require.NoError(t, err)

			header, _ := readComposed(t, data)
			got, err := header.Subject()
			require.NoError(t, err)
			assert.Equal(t, subject, got)
			headerText, _, _ := bytes.Cut(data, []byte("\r\n\r\n"))
			for line := range strings.SplitSeq(string(headerText), "\r\n") {
				assert.LessOrEqual(t, len(line), maxLine)
			}
		})
	}
}

// The Date is the same whichever zone the saved time was read in.
func TestComposeDateZone(t *testing.T) {
	utc := testMessage()
	local := testMessage()
	local.CreatedAt = utc.CreatedAt.In(time.FixedZone("UTC+7", 7*60*60))

	first, err := compose(utc)
	require.NoError(t, err)
	second, err := compose(local)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
}

// A field compose is given unchecked never adds a header: compose applies
// the content rules itself.
func TestComposeInjection(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Message)
	}{
		{"subject", func(m *Message) { m.Subject = "Hi\r\nBcc: eve@example.net" }},
		{"recipient", func(m *Message) { m.To = []string{"ann@example.com>\r\nBcc: <eve@example.net"} }},
		{"from", func(m *Message) { m.From = "me@gmail.com\r\nBcc: eve@example.net" }},
		{"message ID", func(m *Message) { m.InbokMessageID = "<x@gmail.com>\r\nBcc: <eve@example.net>" }},
		{"message ID without brackets", func(m *Message) { m.InbokMessageID = "x@gmail.com" }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message := testMessage()
			c.edit(&message)

			data, err := compose(message)

			require.Error(t, err)
			assert.Nil(t, data)
		})
	}
}

// A message over today's limits still composes: they decided what inbok
// accepted, so lowering one leaves saved messages sendable.
func TestComposeNoLimits(t *testing.T) {
	message := testMessage()
	message.To = repeatAddress(MaxRecipients + 1)
	message.Subject = strings.Repeat("a", maxSubjectLength+1)
	message.Body = strings.Repeat("b", maxBodyLength+1)

	data, err := compose(message)
	require.NoError(t, err)

	header, body := readComposed(t, data)
	assert.Len(t, addresses(t, header, "To"), MaxRecipients+1)
	subject, err := header.Subject()
	require.NoError(t, err)
	assert.Equal(t, message.Subject, subject)
	assert.Equal(t, message.Body, body)
}

// A body line that looks like a header stays in the body.
func TestComposeBodyHeaderLine(t *testing.T) {
	message := testMessage()
	message.Body = "\r\nBcc: eve@example.net\r\n\r\nHello"

	data, err := compose(message)
	require.NoError(t, err)

	header, body := readComposed(t, data)
	assert.Equal(t, []string{"cat@example.com"}, addresses(t, header, "Bcc"))
	assert.Equal(t, message.Body, body)
}

func TestNewMessageID(t *testing.T) {
	first, err := newMessageID("me@gmail.com")
	require.NoError(t, err)
	second, err := newMessageID("me@gmail.com")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(first, "<"), first)
	assert.True(t, strings.HasSuffix(first, "@gmail.com>"), first)
	assert.True(t, messageIDSafe(first), first)
	assert.NotEqual(t, first, second)
	assert.NotContains(t, first, "me@", "the left side holds nothing of the address")
}

// The right side must be a dot-atom (RFC 5322); a domain that is not one
// gives way to a fixed one.
func TestNewMessageIDDomain(t *testing.T) {
	cases := []struct {
		name, email string
	}{
		{"non-ASCII", "me@bücher.de"},
		{"literal", "me@[127.0.0.1]"},
		{"no domain", "me"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := newMessageID(c.email)

			require.NoError(t, err)
			assert.True(t, strings.HasSuffix(id, "@"+fallbackMessageIDDomain+">"), id)
		})
	}
}
