package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"inbok/internal/connectedaccount"
)

// reasonFailedPrecondition is the reason Gmail gives, with a 400, for a
// Google account that has no Gmail ("Mail service not enabled").
const reasonFailedPrecondition = "failedPrecondition"

// Profile returns the Gmail mailbox accessToken reaches, from
// users.getProfile. It returns an error wrapping [connectedaccount.ErrNoGmail]
// when the Google account has no Gmail.
func (c *Client) Profile(ctx context.Context, accessToken connectedaccount.AccessToken) (connectedaccount.Profile, error) {
	service, err := c.gmail(ctx, accessToken)
	if err != nil {
		return connectedaccount.Profile{}, err
	}
	profile, err := service.Users.GetProfile("me").Context(ctx).Do()
	if noGmail(err) {
		return connectedaccount.Profile{}, fmt.Errorf("%w: %w", connectedaccount.ErrNoGmail, redactGmail(err))
	}
	if err != nil {
		return connectedaccount.Profile{}, redactGmail(err)
	}
	return connectedaccount.Profile{Email: profile.EmailAddress}, nil
}

// gmail returns a Gmail client that sends accessToken with every request,
// over the client's own HTTP client.
func (c *Client) gmail(ctx context.Context, accessToken connectedaccount.AccessToken) (*gmail.Service, error) {
	authorized := &http.Client{
		Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken.Value()}),
			Base:   c.http.Transport,
		},
		Timeout:       c.http.Timeout,
		CheckRedirect: c.http.CheckRedirect,
	}
	// The endpoint is always set, so no environment setting the Google client
	// reads, e.g. GOOGLE_CLOUD_UNIVERSE_DOMAIN, can send the token elsewhere.
	service, err := gmail.NewService(ctx, option.WithHTTPClient(authorized), option.WithEndpoint(c.gmailURL))
	if err != nil {
		return nil, fmt.Errorf("gmail client: %w", err)
	}
	return service, nil
}

// noGmail reports whether err is Gmail's answer for a Google account without
// Gmail, e.g. a Workspace user whose administrator turned Gmail off.
func noGmail(err error) bool {
	refused, ok := errors.AsType[*googleapi.Error](err)
	if !ok || refused.Code != http.StatusBadRequest {
		return false
	}
	return slices.ContainsFunc(refused.Errors, func(item googleapi.ErrorItem) bool {
		return item.Reason == reasonFailedPrecondition
	})
}

// redactGmail returns err, from the Gmail API, as only its HTTP status, for
// the same reason as [redactToken].
func redactGmail(err error) error {
	var refused *googleapi.Error
	if !errors.As(err, &refused) {
		return err
	}
	return fmt.Errorf("gmail api: %d", refused.Code)
}
