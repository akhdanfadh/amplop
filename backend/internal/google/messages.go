package google

import (
	"context"
	"strconv"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

const (
	// listPageSize is how many IDs one listing page holds, inbok's choice:
	// fetched at four a second, one page's metadata takes about 25
	// seconds, a bounded step. Gmail allows up to 500.
	listPageSize = 100
)

// Gmail's own fields for the answers inbok reads, so it sends nothing
// else (Gmail's performance tips, "Partial response").
const (
	listFields    = "messages/id,nextPageToken"
	profileFields = "historyId"
)

// HistoryID returns the mailbox's current history ID, from
// users.getProfile.
func (c *Client) HistoryID(ctx context.Context, token connectedaccount.AccessToken) (uint64, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return 0, err
	}
	profile, err := retry(ctx, c, func() (*gmail.Profile, error) {
		return service.Users.GetProfile("me").Fields(profileFields).Context(ctx).Do()
	})
	if err != nil {
		return 0, redactGmail(err)
	}
	return profile.HistoryId, nil
}

// ListMessages returns one page of the account's messages Gmail received
// after query's Since, newest first, from messages.list.
func (c *Client) ListMessages(ctx context.Context, token connectedaccount.AccessToken, query mailsync.ListQuery) (mailsync.MessagePage, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return mailsync.MessagePage{}, err
	}
	call := service.Users.Messages.List("me").
		// Gmail's after: takes seconds since the epoch for an exact time;
		// a date means midnight Pacific time (Gmail's "Search and filter
		// messages" guide).
		Q("after:" + strconv.FormatInt(query.Since.Unix(), 10)).
		IncludeSpamTrash(query.IncludeJunkAndTrash).
		MaxResults(listPageSize).
		Fields(listFields)
	if query.PageToken != "" {
		call = call.PageToken(query.PageToken)
	}
	list, err := retry(ctx, c, func() (*gmail.ListMessagesResponse, error) {
		return call.Context(ctx).Do()
	})
	if err != nil {
		return mailsync.MessagePage{}, redactGmail(err)
	}
	page := mailsync.MessagePage{NextPageToken: list.NextPageToken}
	for _, m := range list.Messages {
		if m != nil && m.Id != "" {
			page.ProviderIDs = append(page.ProviderIDs, m.Id)
		}
	}
	return page, nil
}
