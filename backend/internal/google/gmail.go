package google

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"inbok/internal/mailsync"
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
// over [Client.authorized]'s HTTP client.
func (c *Client) gmail(ctx context.Context, accessToken connectedaccount.AccessToken) (*gmail.Service, error) {
	// The endpoint is always set, so no environment setting the Google client
	// reads, e.g. GOOGLE_CLOUD_UNIVERSE_DOMAIN, can send the token elsewhere,
	// and so is a logger that drops everything: with
	// GOOGLE_SDK_GO_LOGGING_LEVEL=debug the client's own logger writes each
	// request's body, a whole message for messages.send, to stderr.
	service, err := gmail.NewService(ctx, option.WithHTTPClient(c.authorized(accessToken)), option.WithEndpoint(c.gmailURL),
		option.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		return nil, fmt.Errorf("gmail client: %w", err)
	}
	return service, nil
}

// authorized returns an HTTP client that sends accessToken with every
// request, over the client's own transport, so with its deadline, and
// reads at most [maxResponseBytes] of each answer.
func (c *Client) authorized(accessToken connectedaccount.AccessToken) *http.Client {
	return &http.Client{
		Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken.Value()}),
			Base:   cappedTransport{base: c.http.Transport},
		},
		CheckRedirect: c.http.CheckRedirect,
	}
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
// it is, and so is [retry]'s [mailsync.ErrProviderBusy], already redacted.
//
// NOTE: net/http also reports an answer it cannot parse, e.g. a malformed
// Content-Length or Location, as a [*url.Error] whose cause quotes it.
func redactGmail(err error) error {
	if err == nil || errors.Is(err, mailsync.ErrProviderBusy) {
		return err
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
// error, when it cannot hold Gmail's answer, [errResponseTooLarge] for an
// answer past the cap, and [errGmailAnswer] otherwise.
func transportCause(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	// inbok's own cap, which quotes nothing; a caller compares it.
	if errors.Is(cause, errResponseTooLarge) {
		return errResponseTooLarge
	}
	if _, ok := errors.AsType[*net.OpError](cause); ok {
		return cause
	}
	if timeout, ok := errors.AsType[net.Error](cause); ok && timeout.Timeout() {
		return cause
	}
	return errGmailAnswer
}

// maxResponseBytes bounds one Gmail answer, inbok's choice: far past a
// page of IDs or history, or a message's metadata, while a sender's
// oversized headers stop here rather than in memory.
const maxResponseBytes = 4 << 20

// errResponseTooLarge means a Gmail answer passed [maxResponseBytes].
var errResponseTooLarge = errors.New("gmail answer is too large")

// cappedTransport sends requests over base and returns answers whose body
// reads [errResponseTooLarge] past [maxResponseBytes].
type cappedTransport struct {
	base http.RoundTripper
}

// RoundTrip sends req over the base transport.
func (t cappedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	res.Body = &cappedBody{ReadCloser: res.Body, left: maxResponseBytes}
	return res, nil
}

// cappedBody reads its body until left runs out, then returns
// [errResponseTooLarge] if any byte remains.
type cappedBody struct {
	io.ReadCloser
	left int64
}

// Read reads into p, at most what is left.
func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, errResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, err
}
