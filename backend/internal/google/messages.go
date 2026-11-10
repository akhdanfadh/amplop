package google

import (
	"context"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
)

// Gmail's own fields for the answers inbok reads, so it sends nothing
// else (Gmail's performance tips, "Partial response").
const (
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
