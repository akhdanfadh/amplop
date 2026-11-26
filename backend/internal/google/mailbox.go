package google

import (
	"context"
	"strings"

	"google.golang.org/api/gmail/v1"

	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
)

// Gmail's system labels amplop reads by ID (Gmail's labels guide).
//
// NOTE: Google's Go client exports none of these as constants; only Gmail's
// guides list them, as text. A name Google changed would not fail the
// build, only the reads.
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

// labelTypeSystem is the type labels.list gives the labels Gmail made,
// as opposed to "user" for the member's (Gmail's labels reference).
const labelTypeSystem = "system"

// labelRoles are the system labels that are mailboxes with a role. Every
// other label is a mailbox without one, but for [labelUnread] and
// [labelStarred], which are flags.
var labelRoles = map[string]mailsync.Role{
	labelInbox:     mailsync.RoleInbox,
	labelSent:      mailsync.RoleSent,
	labelDraft:     mailsync.RoleDrafts,
	labelSpam:      mailsync.RoleJunk,
	labelTrash:     mailsync.RoleTrash,
	labelImportant: mailsync.RoleImportant,
}

// Mailboxes returns the account's labels, from labels.list, as mailboxes,
// all but UNREAD and STARRED, which are flags. Gmail's own labels are made
// by the provider, the rest by the member.
func (c *Client) Mailboxes(ctx context.Context, token connectedaccount.AccessToken) ([]mailsync.Mailbox, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return nil, err
	}
	labels, err := c.labels(ctx, service)
	if err != nil {
		return nil, err
	}
	return mailboxesOf(labels), nil
}

// labels returns the account's labels from labels.list, each with its ID,
// whole name, and type.
func (c *Client) labels(ctx context.Context, service *gmail.Service) ([]*gmail.Label, error) {
	list, err := retry(ctx, c, func() (*gmail.ListLabelsResponse, error) {
		return service.Users.Labels.List(gmailUser).Fields("labels(id,name,type)").Context(ctx).Do()
	})
	if err != nil {
		return nil, redactGmail(err)
	}
	return list.Labels, nil
}

// mailboxesOf returns labels as mailboxes, as [Client.Mailboxes] does.
func mailboxesOf(labels []*gmail.Label) []mailsync.Mailbox {
	mailboxes := make([]mailsync.Mailbox, 0, len(labels))
	for _, label := range labels {
		if label == nil || isFlag(label.Id) {
			continue
		}
		mailboxes = append(mailboxes, mailsync.Mailbox{
			ProviderID: label.Id,
			Name:       cleanText(label.Name),
			Role:       labelRoles[label.Id],
			MadeBy:     maker(label.Type),
		})
	}
	nest(mailboxes)
	return mailboxes
}

// labelSeparator nests a Gmail label under another by name: Gmail names a
// nested label parent/child.
const labelSeparator = "/"

// nest gives each of mailboxes, named as Gmail names them, its parent and
// its own name: the mailbox whose name is the longest that leads its own
// followed by [labelSeparator] and something more, and what follows that,
// as Gmail's web app shows them: Zq/Mid/Leaf with Zq and no Zq/Mid is
// Mid/Leaf under Zq (checked 2026-11-14). A label with no such mailbox,
// e.g. Home/Bills without Home, stays at the top with its whole name: no
// mailbox is made up for a parent the member does not have. Only the
// member's own labels nest, and only under each other, the labels they
// see.
func nest(mailboxes []mailsync.Mailbox) {
	own := func(mailbox mailsync.Mailbox) bool {
		return mailbox.Role == "" && mailbox.MadeBy == mailsync.MadeByMember
	}
	byName := make(map[string]string, len(mailboxes))
	for _, mailbox := range mailboxes {
		if own(mailbox) {
			byName[mailbox.Name] = mailbox.ProviderID
		}
	}
	for i := range mailboxes {
		mailbox := &mailboxes[i]
		if !own(*mailbox) {
			continue
		}
		name := mailbox.Name
		for end := strings.LastIndex(name, labelSeparator); end > 0; end = strings.LastIndex(name[:end], labelSeparator) {
			// A name that ends with the separator has nothing after it to
			// be its own name.
			if end+len(labelSeparator) == len(name) {
				continue
			}
			parent, ok := byName[name[:end]]
			if !ok {
				continue
			}
			mailbox.ProviderParentID = parent
			mailbox.Name = name[end+len(labelSeparator):]
			break
		}
	}
}

// maker returns who made a label of labelType.
func maker(labelType string) mailsync.Maker {
	if labelType == labelTypeSystem {
		return mailsync.MadeByProvider
	}
	return mailsync.MadeByMember
}

// isFlag reports whether labelID is a label amplop keeps as a flag on the
// message rather than as a mailbox.
func isFlag(labelID string) bool {
	return labelID == labelUnread || labelID == labelStarred
}
