package google

import (
	"context"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// Gmail's system labels inbok reads by ID (Gmail's labels guide).
const (
	labelInbox     = "INBOX"
	labelSent      = "SENT"
	labelDraft     = "DRAFT"
	labelSpam      = "SPAM"
	labelTrash     = "TRASH"
	labelImportant = "IMPORTANT"
	labelUnread    = "UNREAD"
	labelStarred   = "STARRED"
)

// labelRoles are the system labels that are mailboxes with a role
// (ADR007, "Gmail labels as mailboxes"). Every other label is a mailbox
// without one, but for [labelUnread] and [labelStarred], which are flags.
var labelRoles = map[string]mailsync.Role{
	labelInbox:     mailsync.RoleInbox,
	labelSent:      mailsync.RoleSent,
	labelDraft:     mailsync.RoleDrafts,
	labelSpam:      mailsync.RoleJunk,
	labelTrash:     mailsync.RoleTrash,
	labelImportant: mailsync.RoleImportant,
}

// Mailboxes returns the account's labels, from labels.list, as mailboxes,
// all but UNREAD and STARRED, which are flags.
func (c *Client) Mailboxes(ctx context.Context, token connectedaccount.AccessToken) ([]mailsync.Mailbox, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return nil, err
	}
	list, err := retry(ctx, c, func() (*gmail.ListLabelsResponse, error) {
		return service.Users.Labels.List("me").Fields("labels(id,name)").Context(ctx).Do()
	})
	if err != nil {
		return nil, redactGmail(err)
	}
	mailboxes := make([]mailsync.Mailbox, 0, len(list.Labels))
	for _, label := range list.Labels {
		if label == nil || isFlag(label.Id) {
			continue
		}
		mailboxes = append(mailboxes, mailsync.Mailbox{
			ProviderID: label.Id,
			Name:       cleanText(label.Name),
			Role:       labelRoles[label.Id],
		})
	}
	return mailboxes, nil
}

// isFlag reports whether labelID is a label inbok keeps as a flag on the
// message rather than as a mailbox.
func isFlag(labelID string) bool {
	return labelID == labelUnread || labelID == labelStarred
}
