package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/time/rate"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"

	"amplop/internal/connectedaccount"
	"amplop/internal/messagecontent"
)

const (
	// fullResponseBytes bounds one messages.get answer in full format,
	// amplop's choice: Gmail sends attachments by attachmentId, so the answer
	// holds the headers and text parts, which a message past this would
	// not show whole anyway.
	fullResponseBytes = 16 << 20
	// attachmentResponseBytes bounds one messages.attachments.get answer,
	// amplop's choice: Gmail receives messages of up to 50 MB, whose largest
	// attachment, base64 in the message and base64url in the answer, stays
	// under it.
	attachmentResponseBytes = 64 << 20
)

// Gmail's own fields for the content answers amplop reads (Gmail's
// performance tips, "Partial response"): the payload holds the whole
// part tree.
const (
	fullFields       = "id,payload"
	attachmentFields = "data"
)

// formatFull is messages.get's format with the parsed part tree, as
// [formatMetadata].
const formatFull = "full"

// Message returns accountID's message providerID whole, from messages.get
// in full format, as [messagecontent.Parse] reads it. It is paced with the
// account's other fetches in this process, as [Client.Messages] is. It
// returns [messagecontent.ErrGone] when Gmail no longer has the message,
// [messagecontent.ErrTooLarge] when the answer passes 16 MiB,
// [messagecontent.ErrUnreadable] when Gmail refuses the request or its
// answer cannot be read, and [mailsync.ErrProviderBusy] when the account's
// pace or Gmail's rate limits keep it waiting too long.
func (c *Client) Message(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID string) (messagecontent.Received, error) {
	pace := c.paces.take(accountID)
	defer c.paces.release(accountID)

	message, err := c.fetchFull(ctx, token, pace, providerID)
	if err != nil {
		return messagecontent.Received{}, err
	}
	received, err := receivedPart(message.Payload)
	if err != nil {
		return messagecontent.Received{}, fmt.Errorf("%w: %w", messagecontent.ErrUnreadable, err)
	}
	return received, nil
}

// PartContent returns the content of accountID's message providerID's
// part partID, Gmail's partId, transfer encoding undone, and the charset
// Gmail converted it to, as [messagecontent.Received.ContentCharset]: the
// data of a fresh messages.get in full format, UTF-8 for a text part, or
// else of messages.attachments.get with the attachmentId that answer
// gives, as sent. Both are paced as
// [Client.Message] is. It returns [messagecontent.ErrGone] when Gmail no
// longer has the message or it has no such part,
// [messagecontent.ErrTooLarge] when an answer passes its cap,
// [messagecontent.ErrUnreadable] when Gmail refuses a request or an
// answer, its data included, cannot be read, and [mailsync.ErrProviderBusy]
// as [Client.Message] does.
//
// DESIGN: The attachmentId comes from a messages.get made now, never from
// storage or a request: Gmail does not promise an old one keeps working,
// and one from a request could name another message's attachment.
func (c *Client) PartContent(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID, partID string) ([]byte, string, error) {
	pace := c.paces.take(accountID)
	defer c.paces.release(accountID)

	// 1. Fetch the message, for the part's data or a fresh attachmentId.
	message, err := c.fetchFull(ctx, token, pace, providerID)
	if err != nil {
		return nil, "", err
	}
	part := findPart(message.Payload, partID)
	if part == nil {
		return nil, "", messagecontent.ErrGone
	}
	if part.Body == nil {
		return []byte{}, "", nil
	}
	if part.Body.AttachmentId == "" {
		content, err := decodeBody(part.Body.Data)
		return content, dataCharset(part), err
	}

	// 2. Fetch the attachment's data.
	service, err := c.gmailCapped(ctx, token, attachmentResponseBytes)
	if err != nil {
		return nil, "", err
	}
	body, err := retry(ctx, c, func() (*gmail.MessagePartBody, error) {
		// Inside the retry, as in [Client.fetchMetadata].
		if err := waitPace(ctx, pace, attachmentUnits); err != nil {
			return nil, err
		}
		return service.Users.Messages.Attachments.Get(gmailUser, providerID, part.Body.AttachmentId).
			Fields(attachmentFields).Context(ctx).Do()
	})
	if err != nil {
		return nil, "", contentError(err)
	}
	// NOTE: Google's client answers a JSON null with no value and no
	// error, and a 204 with an empty one.
	if body == nil || body.HTTPStatusCode == http.StatusNoContent {
		return nil, "", errNoAnswer
	}
	content, err := decodeBody(body.Data)
	return content, "", err
}

// fetchFull returns providerID from messages.get in full format, with
// token, waiting on pace before each request.
func (c *Client) fetchFull(ctx context.Context, token connectedaccount.AccessToken, pace *rate.Limiter, providerID string) (*gmail.Message, error) {
	service, err := c.gmailCapped(ctx, token, fullResponseBytes)
	if err != nil {
		return nil, err
	}
	message, err := retry(ctx, c, func() (*gmail.Message, error) {
		// Inside the retry, as in [Client.fetchMetadata].
		if err := waitPace(ctx, pace, fullUnits); err != nil {
			return nil, err
		}
		return service.Users.Messages.Get(gmailUser, providerID).Format(formatFull).Fields(fullFields).Context(ctx).Do()
	})
	if err != nil {
		return nil, contentError(err)
	}
	// NOTE: as for an attachment, a null answer is no value and no error,
	// and a 204 an empty message, without the ID asked for.
	if message == nil || message.Id != providerID {
		return nil, errNoAnswer
	}
	return message, nil
}

// errNoAnswer means Gmail answered without the value asked for: JSON
// null, a 204, or another message.
var errNoAnswer = fmt.Errorf("%w: gmail answered without what was asked for", messagecontent.ErrUnreadable)

// contentError returns err, from a content fetch, as
// [messagecontent.Provider] names it: Gmail's 404 or 410 as
// [messagecontent.ErrGone]; an answer past its cap as
// [messagecontent.ErrTooLarge]; an answer that is not the JSON Gmail
// documents, or a refusal of this request that trying again does not
// change, as [messagecontent.ErrUnreadable]; and anything else redacted,
// which ends a batch rather than skipping a message.
func contentError(err error) error {
	refused, isRefusal := errors.AsType[*googleapi.Error](err)
	_, isSyntax := errors.AsType[*json.SyntaxError](err)
	_, isType := errors.AsType[*json.UnmarshalTypeError](err)
	switch {
	case isRefusal && (refused.Code == http.StatusNotFound || refused.Code == http.StatusGone):
		return messagecontent.ErrGone
	case errors.Is(err, errResponseTooLarge):
		return messagecontent.ErrTooLarge
	// A whole answer that does not decode is decoded the same way again; a
	// cut-off one is a network failure, which stays transient.
	case isSyntax || isType:
		return fmt.Errorf("%w: gmail answer does not decode", messagecontent.ErrUnreadable)
	case isRefusal && refusesMessage(refused.Code):
		return fmt.Errorf("%w: gmail api: %d", messagecontent.ErrUnreadable, refused.Code)
	default:
		return redactGmail(err)
	}
}

// refusesMessage reports whether status, Gmail's answer to a request about
// one message, refuses that message rather than the account or the
// moment: a 4xx, except 401 for the access token, 403 for a rate limit or
// the account's permission, 408 for a timeout, and 429 for a rate limit
// (Gmail's "Resolve errors" guide).
func refusesMessage(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return status >= http.StatusBadRequest && status < http.StatusInternalServerError
}

// findPart returns the part of root, or root itself, whose partId is
// partID, or nil when there is none.
func findPart(root *gmail.MessagePart, partID string) *gmail.MessagePart {
	if root == nil {
		return nil
	}
	if root.PartId == partID {
		return root
	}
	for _, child := range root.Parts {
		if found := findPart(child, partID); found != nil {
			return found
		}
	}
	return nil
}

// decodeBody returns data, a part's base64url data, decoded, or an error
// wrapping [messagecontent.ErrUnreadable].
func decodeBody(data string) ([]byte, error) {
	content, err := bodyContent(&gmail.MessagePartBody{Data: data})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", messagecontent.ErrUnreadable, err)
	}
	return content, nil
}
