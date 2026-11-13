package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"

	"inbok/internal/connectedaccount"
	"inbok/internal/outgoingmessage"
)

// Gmail's names for what a send and its check use, from its API reference.
//
// NOTE: Google's Go client exports none of these as constants; only its doc
// comments list them, as text. A name Google changed would not fail the
// build, only the calls.
const (
	// historyMessageAdded is the history type of a message added to the
	// mailbox.
	historyMessageAdded = "messageAdded"
)

// inbokMessageIDHeader is the header compose writes the message's ID in.
const inbokMessageIDHeader = "X-Inbok-Message-Id"

// The 403 reasons Gmail gives for limits that pass ("Resolve errors").
// dailyLimitExceeded is the Google project's quota, which lasts until the
// day ends.
//
// NOTE: neither Gmail's client nor googleapi exports these;
// [googleapi.ErrorItem.Reason] is a plain string, and the values come from
// Gmail's error guide.
const (
	reasonRateLimit     = "rateLimitExceeded"
	reasonUserRateLimit = "userRateLimitExceeded"
	reasonDailyLimit    = "dailyLimitExceeded"
)

// sendingLimitFragment is in the message of Gmail's 429 for the daily
// sending limit, "User-rate limit exceeded (Mail sending)" ("Resolve
// errors", Mail sending limits).
//
// NOTE: Gmail gives that limit no reason code of its own, so this relies on
// the message's wording. Were Google to reword it, the limit would be retried
// as another limit and the message would end gave-up, not sending-limit.
const sendingLimitFragment = "Mail sending"

// maxCheckPages bounds how many pages of history a check reads, 100 changes
// each by Gmail's default; a mailbox that sent more since is not searched
// further. inbok's choice: a send's check looks minutes back.
const maxCheckPages = 10

// Mailbox returns the address of the mailbox accessToken reaches and its
// current history ID, from users.getProfile.
func (c *Client) Mailbox(ctx context.Context, accessToken connectedaccount.AccessToken) (outgoingmessage.Mailbox, error) {
	service, err := c.gmail(ctx, accessToken)
	if err != nil {
		return outgoingmessage.Mailbox{}, err
	}
	profile, err := service.Users.GetProfile(gmailUser).Context(ctx).Do()
	if err != nil {
		return outgoingmessage.Mailbox{}, redactGmail(err)
	}
	return outgoingmessage.Mailbox{
		Email:     profile.EmailAddress,
		HistoryID: strconv.FormatUint(profile.HistoryId, 10),
	}, nil
}

// Send sends raw from the mailbox accessToken reaches, with messages.send,
// into the mailbox's thread threadID when not empty, and returns Gmail's IDs
// for it. It returns [outgoingmessage.ErrRefused],
// [outgoingmessage.ErrSendingLimit], or [outgoingmessage.ErrNotSent] for an
// answer that says the message was not sent, [outgoingmessage.ErrNotSent] for a
// failure before any connection to Gmail, and [outgoingmessage.ErrUnanswered]
// for any other end.
//
// DESIGN: Gmail has no idempotency key, so a request that may have reached
// Gmail may have been sent, whatever went wrong after. Only no connection at
// all proves it did not: Go reports a finished write after the fact, so Gmail
// can hold the whole request before the transport says it wrote it
// ([httptrace.ClientTrace] allows callbacks after a request failed). A 5xx counts as
// unanswered too: Google's guide asks for a retry but does not say the message
// was not sent.
func (c *Client) Send(ctx context.Context, accessToken connectedaccount.AccessToken, raw []byte, threadID string) (outgoingmessage.Sent, error) {
	service, err := c.gmail(ctx, accessToken)
	if err != nil {
		return outgoingmessage.Sent{}, fmt.Errorf("%w: %w", outgoingmessage.ErrNotSent, err)
	}

	// 1. Note whether the transport got a connection, before which no byte
	// of the request can reach Gmail.
	var connected atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	})

	// 2. Send, and sort the end by what it says of the message.
	// threadID asks for the thread: Gmail's "Manage threads" guide also
	// requires RFC-compliant References and In-Reply-To headers and a
	// matching Subject. A reply whose answered message had no writable
	// Message-ID, or whose subject was cut, may not meet them.
	message := &gmail.Message{Raw: base64.URLEncoding.EncodeToString(raw), ThreadId: threadID}
	sent, err := service.Users.Messages.Send(gmailUser, message).Context(traced).Do()
	if err == nil && sent.Id != "" {
		return outgoingmessage.Sent{ProviderMessageID: sent.Id, ProviderThreadID: sent.ThreadId}, nil
	}
	if refused, ok := errors.AsType[*googleapi.Error](err); ok {
		return outgoingmessage.Sent{}, fmt.Errorf("%w: %w", sendAnswer(refused), redactGmail(err))
	}
	if err != nil && !connected.Load() {
		return outgoingmessage.Sent{}, fmt.Errorf("%w: %w", outgoingmessage.ErrNotSent, redactGmail(err))
	}
	if err == nil {
		// A 200 without an ID: Gmail took the message, but inbok cannot
		// name it, so the check finds it.
		err = errors.New("gmail answered without a message id")
	}
	return outgoingmessage.Sent{}, fmt.Errorf("%w: %w", outgoingmessage.ErrUnanswered, redactGmail(err))
}

// sendAnswer returns what refused, Gmail's error answer to messages.send,
// says of the message ("Resolve errors").
func sendAnswer(refused *googleapi.Error) error {
	switch {
	case refused.Code >= http.StatusInternalServerError:
		return outgoingmessage.ErrUnanswered
	case refused.Code == http.StatusTooManyRequests:
		// NOTE: The guide's text for the daily sending limit, "User-rate limit
		// exceeded (Mail sending)", tells it from other limits; it gives no
		// reason code for it.
		if strings.Contains(refused.Message, sendingLimitFragment) {
			return outgoingmessage.ErrSendingLimit
		}
		return outgoingmessage.ErrNotSent
	case refused.Code == http.StatusUnauthorized:
		// The token was refused; the next attempt takes a new one.
		return outgoingmessage.ErrNotSent
	case refused.Code == http.StatusForbidden && hasReason(refused, reasonRateLimit, reasonUserRateLimit, reasonDailyLimit):
		return outgoingmessage.ErrNotSent
	}
	return outgoingmessage.ErrRefused
}

// hasReason reports whether refused gives one of reasons.
func hasReason(refused *googleapi.Error, reasons ...string) bool {
	return slices.ContainsFunc(refused.Errors, func(item googleapi.ErrorItem) bool {
		return slices.Contains(reasons, item.Reason)
	})
}

// FindSent returns the message Gmail added to Sent after historyID, in the
// mailbox accessToken reaches, whose X-Inbok-Message-Id is inbokMessageID.
// It returns [outgoingmessage.ErrSentNotFound] when it read every such
// message and none matched, and [outgoingmessage.ErrCheckIncomplete] when
// one could not be read, Gmail no longer keeps changes since historyID, or
// there were more than it reads.
func (c *Client) FindSent(ctx context.Context, accessToken connectedaccount.AccessToken, historyID, inbokMessageID string) (outgoingmessage.Sent, error) {
	start, err := strconv.ParseUint(historyID, 10, 64)
	if err != nil {
		return outgoingmessage.Sent{}, fmt.Errorf("%w: history id is not a number", outgoingmessage.ErrCheckIncomplete)
	}
	service, err := c.gmail(ctx, accessToken)
	if err != nil {
		return outgoingmessage.Sent{}, err
	}

	// 1. List the messages added to Sent since, which history gives without
	// search's delay.
	added, complete, err := sentSince(ctx, service, start)
	if err != nil {
		return outgoingmessage.Sent{}, err
	}

	// 2. Read each one's ID header. One deleted meanwhile cannot be read,
	// so it might have been this message.
	for _, id := range added {
		message, err := service.Users.Messages.Get(gmailUser, id).Format(formatMetadata).
			MetadataHeaders(inbokMessageIDHeader).Context(ctx).Do()
		if isNotFound(err) {
			complete = false
			continue
		}
		if err != nil {
			return outgoingmessage.Sent{}, redactGmail(err)
		}
		if headerValue(message.Payload, inbokMessageIDHeader) == inbokMessageID {
			return outgoingmessage.Sent{
				ProviderMessageID: message.Id,
				ProviderThreadID:  message.ThreadId,
				At:                gmailTime(message.InternalDate),
			}, nil
		}
	}
	if !complete {
		return outgoingmessage.Sent{}, outgoingmessage.ErrCheckIncomplete
	}
	return outgoingmessage.Sent{}, outgoingmessage.ErrSentNotFound
}

// sentSince returns the IDs of the messages added to Sent after start, in
// the order Gmail lists them, and whether the list is complete: false when
// Gmail no longer keeps changes since start, or there are more than
// [maxCheckPages] pages.
func sentSince(ctx context.Context, service *gmail.Service, start uint64) ([]string, bool, error) {
	var ids []string
	seen := map[string]bool{}
	call := service.Users.History.List(gmailUser).StartHistoryId(start).
		HistoryTypes(historyMessageAdded).LabelId(labelSent)
	pageToken := ""
	for range maxCheckPages {
		page, err := call.PageToken(pageToken).Context(ctx).Do()
		// Gmail answers 404 for a start older than the history it keeps.
		if isNotFound(err) {
			return ids, false, nil
		}
		if err != nil {
			return nil, false, redactGmail(err)
		}
		for _, record := range page.History {
			for _, added := range record.MessagesAdded {
				if added.Message != nil && !seen[added.Message.Id] {
					seen[added.Message.Id] = true
					ids = append(ids, added.Message.Id)
				}
			}
		}
		if page.NextPageToken == "" {
			return ids, true, nil
		}
		pageToken = page.NextPageToken
	}
	return ids, false, nil
}

// gmailTime returns ms, Gmail's internalDate in milliseconds since the
// epoch, as a time, or the zero time when Gmail gave none.
func gmailTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// headerValue returns the value of part's header name, which Gmail spells
// in its own case, e.g. Message-Id for Message-ID, or empty.
func headerValue(part *gmail.MessagePart, name string) string {
	if part == nil {
		return ""
	}
	for _, header := range part.Headers {
		if strings.EqualFold(header.Name, name) {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}
