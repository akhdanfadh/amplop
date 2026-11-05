package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"inbok/internal/connectedaccount"
)

// Profile returns the Gmail mailbox accessToken reaches, from
// users.getProfile.
func (c *Client) Profile(ctx context.Context, accessToken string) (connectedaccount.Profile, error) {
	authorized := &http.Client{
		Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken}),
			Base:   c.http.Transport,
		},
		Timeout:       c.http.Timeout,
		CheckRedirect: c.http.CheckRedirect,
	}
	// The endpoint is always set, so no environment setting the Google client
	// reads, e.g. GOOGLE_CLOUD_UNIVERSE_DOMAIN, can send the token elsewhere.
	service, err := gmail.NewService(ctx, option.WithHTTPClient(authorized), option.WithEndpoint(c.gmailURL))
	if err != nil {
		return connectedaccount.Profile{}, fmt.Errorf("gmail client: %w", err)
	}
	profile, err := service.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return connectedaccount.Profile{}, redactGmail(err)
	}
	return connectedaccount.Profile{Email: profile.EmailAddress}, nil
}

// redactGmail returns err, from the Gmail API, as only its HTTP status, for
// the same reason as redactToken.
func redactGmail(err error) error {
	var refused *googleapi.Error
	if !errors.As(err, &refused) {
		return err
	}
	return fmt.Errorf("gmail api: %d", refused.Code)
}
