package smtp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

// Input that would change the SMTP commands, or cannot be dialed, is refused
// before connecting, and is not reported as the server's refusal.
func TestSendUnsafeInput(t *testing.T) {
	certificate, roots := testCertificate(t)
	for _, c := range []struct {
		name   string
		change func(*systememail.Server, *string, *systememail.Message)
	}{
		{"recipient", func(_ *systememail.Server, _ *string, m *systememail.Message) {
			m.To = "admin@example.org>\r\nRCPT TO:<victim@example.net"
		}},
		{"sender", func(_ *systememail.Server, _ *string, m *systememail.Message) { m.FromAddress = "inbok@example.com\n" }},
		{"username", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Username = "inbok\x00admin" }},
		{"password", func(_ *systememail.Server, p *string, _ *systememail.Message) { *p = "app\x00password" }},
		{"ESMTP parameter", func(_ *systememail.Server, _ *string, m *systememail.Message) {
			m.To = "admin@example.org> NOTIFY=SUCCESS"
		}},
		{"port", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Port = 0 }},
		{"security", func(s *systememail.Server, _ *string, _ *systememail.Message) { s.Security = "ssl" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeServer{security: systememail.SecurityTLS}
			server, password, message := fake.start(t, certificate), testPassword, testMessage()
			c.change(&server, &password, &message)

			err := (&Sender{roots: roots, timeout: testTimeout, hello: testHello}).Send(t.Context(), server, password, message)
			require.ErrorIs(t, err, errUnsafeInput)

			var failed *systememail.SendError
			assert.NotErrorAs(t, err, &failed)
			assert.Empty(t, fake.messages())
		})
	}
}
