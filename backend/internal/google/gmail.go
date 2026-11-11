package google

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"inbok/internal/connectedaccount"
)

// reasonFailedPrecondition is the reason Gmail gives, with a 400, for a
// Google account that has no Gmail ("Mail service not enabled").
//
// NOTE: neither Gmail's client nor googleapi exports it;
// [googleapi.ErrorItem.Reason] is a plain string.
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
	// reads, e.g. GOOGLE_CLOUD_UNIVERSE_DOMAIN, can send the token elsewhere,
	// and so is a logger that drops everything: with
	// GOOGLE_SDK_GO_LOGGING_LEVEL=debug the client's own logger writes each
	// request's body, a whole message for messages.send, to stderr.
	service, err := gmail.NewService(ctx, option.WithHTTPClient(authorized), option.WithEndpoint(c.gmailURL),
		option.WithLogger(slog.New(slog.DiscardHandler)))
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

// errGmailAnswer stands in for an answer from Gmail that could not be read.
var errGmailAnswer = errors.New("gmail api: unreadable answer")

// redactGmail returns err, from the Gmail API, without any of Gmail's
// answer, for the same reason as [redactToken]: an error answer as only its
// HTTP status, and an answer that could not be decoded as
// [errGmailAnswer], since a decoding error can quote it. A transport error
// keeps its operation and inbok's own request URL, and its cause only when
// that cannot hold the answer: the context ending, a timeout, or a network
// failure. The context's own error, which callers compare, is returned as
// it is.
//
// NOTE: net/http also reports an answer it cannot parse, e.g. a malformed
// Content-Length or Location, as a [*url.Error] whose cause quotes it.
func redactGmail(err error) error {
	if err == nil {
		return nil
	}
	if refused, ok := errors.AsType[*googleapi.Error](err); ok {
		return fmt.Errorf("gmail api: %d", refused.Code)
	}
	if failed, ok := errors.AsType[*url.Error](err); ok {
		return &url.Error{Op: failed.Op, URL: failed.URL, Err: transportCause(failed.Err)}
	}
	// NOTE: when the context has ended, Google's client returns the
	// context's error in place of the transport's (gensupport's send).
	return transportCause(err)
}

// transportCause returns cause, a transport failure or the context's
// error, when it cannot hold Gmail's answer, and [errGmailAnswer]
// otherwise.
func transportCause(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	if _, ok := errors.AsType[*net.OpError](cause); ok {
		return cause
	}
	if timeout, ok := errors.AsType[net.Error](cause); ok && timeout.Timeout() {
		return cause
	}
	return errGmailAnswer
}
