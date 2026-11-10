package outgoingmessage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testContent is content every rule accepts.
func testContent() Content {
	return Content{
		To:      []string{"ann@example.com"},
		Cc:      []string{"ben@example.com"},
		Bcc:     []string{"cat@example.com"},
		Subject: "Lunch on Friday",
		Body:    "Are you free?\nI booked a table.",
	}
}

func TestParseContent(t *testing.T) {
	content := testContent()
	content.To = []string{" Ann@Example.com\t", "dan@example.com"}

	parsed, err := parseContent(content)

	require.NoError(t, err)
	assert.Equal(t, []string{"Ann@Example.com", "dan@example.com"}, parsed.To, "trimmed, case kept")
	assert.Equal(t, content.Cc, parsed.Cc)
	assert.Equal(t, content.Bcc, parsed.Bcc)
	assert.Equal(t, content.Subject, parsed.Subject)
	assert.Equal(t, content.Body, parsed.Body)
}

func TestParseContentEmptyLists(t *testing.T) {
	content := Content{Bcc: []string{"cat@example.com"}}

	parsed, err := parseContent(content)

	require.NoError(t, err)
	assert.Equal(t, []string{}, parsed.To, "never nil, so storage saves an empty list")
	assert.Equal(t, []string{}, parsed.Cc)
	assert.Equal(t, []string{"cat@example.com"}, parsed.Bcc)
	assert.Empty(t, parsed.Subject)
	assert.Empty(t, parsed.Body)
}

func TestParseContentRecipient(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Content)
		field Field
		index int
	}{
		{"to", func(c *Content) { c.To = []string{"ann@example.com", "not an address"} }, FieldTo, 1},
		{"cc", func(c *Content) { c.Cc = []string{"Ben <ben@example.com>"} }, FieldCc, 0},
		{"bcc", func(c *Content) { c.Bcc = []string{"a@example.com, b@example.com"} }, FieldBcc, 0},
		{"line break", func(c *Content) { c.To = []string{"ann@example.com\r\nBcc: eve@example.net"} }, FieldTo, 0},
		{"blank", func(c *Content) { c.Cc = []string{"ben@example.com", " "} }, FieldCc, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := testContent()
			c.edit(&content)

			_, err := parseContent(content)

			var recipientErr *RecipientError
			require.ErrorAs(t, err, &recipientErr)
			assert.Equal(t, c.field, recipientErr.Field)
			assert.Equal(t, c.index, recipientErr.Index)
			assert.NotContains(t, err.Error(), "@", "the error holds no address")
		})
	}
}

// Each list holds at most [MaxRecipients], counted on its own, and the three
// together at least one.
func TestParseContentCount(t *testing.T) {
	most := testContent()
	most.To = repeatAddress(MaxRecipients)
	most.Cc = repeatAddress(MaxRecipients)
	most.Bcc = repeatAddress(MaxRecipients)

	_, err := parseContent(most)
	require.NoError(t, err)

	for _, field := range []Field{FieldTo, FieldCc, FieldBcc} {
		t.Run(string(field), func(t *testing.T) {
			tooMany := testContent()
			list := repeatAddress(MaxRecipients + 1)
			switch field {
			case FieldTo:
				tooMany.To = list
			case FieldCc:
				tooMany.Cc = list
			case FieldBcc:
				tooMany.Bcc = list
			}

			_, err := parseContent(tooMany)

			require.ErrorIs(t, err, ErrTooManyRecipients)
			var countErr *TooManyRecipientsError
			require.ErrorAs(t, err, &countErr)
			assert.Equal(t, field, countErr.Field)
		})
	}

	_, err = parseContent(Content{Subject: "Hello"})
	require.ErrorIs(t, err, ErrNoRecipients)
}

func TestParseContentSubject(t *testing.T) {
	cases := []struct {
		name, subject string
	}{
		{"line feed", "Hello\nBcc: eve@example.net"},
		{"carriage return", "Hello\rthere"},
		{"tab", "Hello\tthere"},
		{"NUL", "Hello\x00"},
		{"C1 control", "Hello\u0085there"},
		{"too long", strings.Repeat("é", maxSubjectLength+1)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := testContent()
			content.Subject = c.subject

			_, err := parseContent(content)

			require.ErrorIs(t, err, ErrSubjectInvalid)
		})
	}

	longest := testContent()
	longest.Subject = strings.Repeat("é", maxSubjectLength)
	_, err := parseContent(longest)
	require.NoError(t, err, "the limit counts characters, not bytes")
}

func TestParseContentBody(t *testing.T) {
	kept := testContent()
	kept.Body = "Tabs\tand\r\nline breaks\rstay.\u0085"
	_, err := parseContent(kept)
	require.NoError(t, err)

	cases := []struct {
		name, body string
	}{
		{"NUL", "Hello\x00there"},
		{"too long", strings.Repeat("é", maxBodyLength+1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := testContent()
			content.Body = c.body

			_, err := parseContent(content)

			require.ErrorIs(t, err, ErrBodyInvalid)
		})
	}

	longest := testContent()
	longest.Body = strings.Repeat("é", maxBodyLength)
	_, err = parseContent(longest)
	require.NoError(t, err)
}

func TestRecipientErrorIsInvalid(t *testing.T) {
	err := error(&RecipientError{Field: FieldTo, Index: 2})

	assert.ErrorIs(t, err, ErrRecipientInvalid)
}

// repeatAddress returns n valid addresses.
func repeatAddress(n int) []string {
	addresses := make([]string, n)
	for i := range addresses {
		addresses[i] = "person" + strings.Repeat("x", i%5) + "@example.com"
	}
	return addresses
}
