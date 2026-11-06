package smtp

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Line breaks in a field are encoded or rejected; they never start a header.
func TestComposeInjection(t *testing.T) {
	message := testMessage()
	message.FromName = "inbok\r\nBcc: victim@example.net"
	message.Subject = "Test\r\nBcc: victim@example.net"

	data, err := compose(message, time.Now())
	require.NoError(t, err)

	reader, err := mail.CreateReader(bytes.NewReader(data))
	require.NoError(t, err)
	assert.False(t, reader.Header.Has("Bcc"))
}

// The Message-ID needs a domain even if the From address has none.
func TestComposeMessageID(t *testing.T) {
	message := testMessage()
	message.FromAddress = "inbok"

	data, err := compose(message, time.Now())
	require.NoError(t, err)

	reader, err := mail.CreateReader(bytes.NewReader(data))
	require.NoError(t, err)
	id, err := reader.Header.MessageID()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(id, "@localhost"), id)
}
