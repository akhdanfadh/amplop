package google

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/googleemulator"
	"inbok/internal/mailsync"
)

// alexMailbox is the seeded mailbox the sync tests read.
const alexMailbox = "alex@example.com"

// startMailbox starts an emulator whose alex@example.com has labels and
// messages, and returns a client pointed at it with an access token for
// alex. Retries wait nothing.
func startMailbox(t *testing.T, labels []googleemulator.Label, messages []googleemulator.Message) (*Client, connectedaccount.AccessToken) {
	t.Helper()
	e := googleemulator.Start(t, googleemulator.Seed{
		Users:    []googleemulator.User{{Email: alexMailbox, Name: "Alex Rivera", UID: alexSubject}},
		Clients:  []googleemulator.Client{{ID: testClientID, Secret: testSecret, RedirectURIs: []string{testRedirect}}},
		Labels:   labels,
		Messages: messages,
	})
	client := New(Settings{ClientID: testClientID, ClientSecret: testSecret, RedirectURL: testRedirect, BaseURL: e.BaseURL}, time.Now)
	client.pause = (&pauses{}).record
	code, verifier := consent(t, e, client, alexMailbox)
	grant, err := client.Exchange(t.Context(), code, verifier)
	require.NoError(t, err)
	return client, grant.AccessToken
}

// Gmail's labels are mailboxes, system ones with their roles, except the
// two that are flags.
func TestMailboxes(t *testing.T) {
	client, token := startMailbox(t, []googleemulator.Label{{ID: "Label_receipts", Mailbox: alexMailbox, Name: "Receipts/2026"}}, nil)

	mailboxes, err := client.Mailboxes(t.Context(), token)

	require.NoError(t, err)
	byID := make(map[string]mailsync.Mailbox)
	for _, mailbox := range mailboxes {
		byID[mailbox.ProviderID] = mailbox
	}
	assert.Equal(t, mailsync.Mailbox{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox, MadeBy: mailsync.MadeByProvider}, byID["INBOX"])
	assert.Equal(t, mailsync.RoleSent, byID["SENT"].Role)
	assert.Equal(t, mailsync.RoleDrafts, byID["DRAFT"].Role)
	assert.Equal(t, mailsync.RoleJunk, byID["SPAM"].Role)
	assert.Equal(t, mailsync.RoleTrash, byID["TRASH"].Role)
	assert.Equal(t, mailsync.RoleImportant, byID["IMPORTANT"].Role)
	assert.Equal(t, mailsync.Mailbox{ProviderID: "CATEGORY_SOCIAL", Name: "CATEGORY_SOCIAL", MadeBy: mailsync.MadeByProvider}, byID["CATEGORY_SOCIAL"], "Gmail's own label is no member's")
	assert.Equal(t, mailsync.Mailbox{ProviderID: "Label_receipts", Name: "Receipts/2026", MadeBy: mailsync.MadeByMember}, byID["Label_receipts"], "the member's label")
	assert.NotContains(t, byID, "UNREAD")
	assert.NotContains(t, byID, "STARRED")
}

// A refusal keeps only its status.
func TestMailboxesRefused(t *testing.T) {
	client, _ := stubGmail(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "secret page body", http.StatusUnauthorized)
	})

	_, err := client.Mailboxes(t.Context(), stubToken)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.NotContains(t, err.Error(), "secret page body")
}
