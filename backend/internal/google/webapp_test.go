package google

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
)

// A conversation opens in All mail, or in Spam or Trash when only there,
// as the account whose address names it.
func TestConversationURL(t *testing.T) {
	gmail := conversation.Source{Provider: connectedaccount.ProviderGmail, Address: "alex+work@example.com", ThreadID: "18c2f0a1b2c3d4e5"}
	for _, c := range []struct {
		name   string
		source conversation.Source
		row    conversation.Conversation
		want   string
	}{
		{"inbox", gmail, conversation.Conversation{InInbox: true, InAllMail: true},
			"https://mail.google.com/mail/?authuser=alex%2Bwork%40example.com#all/18c2f0a1b2c3d4e5"},
		{"one message in trash", gmail, conversation.Conversation{InAllMail: true, InTrash: true},
			"https://mail.google.com/mail/?authuser=alex%2Bwork%40example.com#all/18c2f0a1b2c3d4e5"},
		{"only spam", gmail, conversation.Conversation{InSpam: true},
			"https://mail.google.com/mail/?authuser=alex%2Bwork%40example.com#spam/18c2f0a1b2c3d4e5"},
		{"only trash", gmail, conversation.Conversation{InTrash: true},
			"https://mail.google.com/mail/?authuser=alex%2Bwork%40example.com#trash/18c2f0a1b2c3d4e5"},
		{"another provider", conversation.Source{Provider: "imap", Address: "a@example.com", ThreadID: "t1"}, conversation.Conversation{InAllMail: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, WebApp{}.ConversationURL(c.source, c.row))
		})
	}
}
