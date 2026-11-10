package google

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// Gmail's per-user quota and what a metadata fetch costs of it (Gmail's
// usage limits): 6,000 units a minute per user per project, and 20 for a
// messages.get in metadata format.
const (
	userUnitsPerMinute = 6000
	metadataUnits      = 20
)

const (
	// fetchUnitsPerMinute is the share of [userUnitsPerMinute] one account's
	// fetches spend, inbok's choice: four fifths, leaving room for the same
	// account's sends (100 units each) and polls meanwhile. That paces
	// fetches at 4 a second. The pace is this process's: one sync per
	// account at a time keeps the account's fetches in one process.
	fetchUnitsPerMinute = userUnitsPerMinute * 4 / 5
	// fetchConcurrency bounds one call's fetches in flight, inbok's choice.
	// Gmail also limits a user's concurrent requests, at a number it does
	// not publish (Gmail's "Resolve errors" guide).
	fetchConcurrency = 4
	// listPageSize is how many IDs one listing page holds, inbok's choice:
	// fetched at four a second, one page's metadata takes about 25
	// seconds, a bounded step. Gmail allows up to 500.
	listPageSize = 100
)

// Gmail's own fields for the answers inbok reads, so it sends nothing
// else (Gmail's performance tips, "Partial response").
const (
	listFields     = "messages/id,nextPageToken"
	metadataFields = "id,threadId,labelIds,snippet,internalDate,payload/headers"
	minimalFields  = "id,threadId,labelIds,snippet,internalDate"
	profileFields  = "historyId"
)

// Position returns the account's current history ID, from
// users.getProfile, as decimal text.
func (c *Client) Position(ctx context.Context, token connectedaccount.AccessToken) (string, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return "", err
	}
	profile, err := retry(ctx, c, func() (*gmail.Profile, error) {
		return service.Users.GetProfile("me").Fields(profileFields).Context(ctx).Do()
	})
	if err != nil {
		return "", redactGmail(err)
	}
	return formatHistoryID(profile.HistoryId), nil
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

// Messages returns the metadata of accountID's messages with providerIDs,
// each from messages.get in metadata format. The account's fetches, in
// this call and the ones before, are paced at four fifths of Gmail's
// per-user quota, with at most four of this call's in flight. It stops at
// the first fetch that fails for another reason than the message being
// gone.
func (c *Client) Messages(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerIDs []string) (mailsync.Fetched, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return mailsync.Fetched{}, err
	}
	pace := c.paces.take(accountID)
	defer c.paces.release(accountID)

	// Each fetch is converted where it lands, so a page holds no more than
	// its bounded metadata, never every raw answer at once.
	results := make([]fetchResult, len(providerIDs))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(fetchConcurrency)
	for i, providerID := range providerIDs {
		group.Go(func() error {
			message, err := c.fetchMetadata(groupCtx, service, pace, providerID)
			if err != nil {
				return err
			}
			if message == nil {
				results[i].gone = true
				return nil
			}
			results[i].message = messageFromGmail(message)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return mailsync.Fetched{}, redactGmail(err)
	}

	var fetched mailsync.Fetched
	for i, result := range results {
		if result.gone {
			fetched.Gone = append(fetched.Gone, providerIDs[i])
			continue
		}
		fetched.Messages = append(fetched.Messages, result.message)
	}
	return fetched, nil
}

// fetchResult is one fetch of [Client.Messages]: the message, or gone.
type fetchResult struct {
	message mailsync.Message
	gone    bool
}

// paceIdle is how long an account's pace is kept after its last call
// ends, inbok's choice: by then its bucket has long refilled, so a new
// one paces the same.
const paceIdle = time.Minute

// paces holds each account's fetch pace across calls, so a run of small
// calls is paced as one large one.
type paces struct {
	mu        sync.Mutex
	byAccount map[int64]*pace
}

// pace is one account's token bucket and how many calls use it.
type pace struct {
	limiter *rate.Limiter
	calls   int
	ended   time.Time
}

// newPaces returns an empty set of paces.
func newPaces() *paces {
	return &paces{byAccount: make(map[int64]*pace)}
}

// take returns accountID's limiter, made full if it has none, and drops
// the paces idle past [paceIdle].
func (p *paces) take(accountID int64) *rate.Limiter {
	// 1. Lock, so another account's call cannot change the map between
	// the steps below.
	p.mu.Lock()
	defer p.mu.Unlock()

	// 2. Drop idle paces before looking up this account's, so the one this
	// call gets is never one this loop deletes.
	now := time.Now()
	for id, idle := range p.byAccount {
		if idle.calls == 0 && now.Sub(idle.ended) > paceIdle {
			delete(p.byAccount, id)
		}
	}

	// 3. Take the account's pace, or start a full one.
	current, ok := p.byAccount[accountID]
	if !ok {
		current = &pace{limiter: rate.NewLimiter(rate.Limit(float64(fetchUnitsPerMinute)/60), metadataUnits)}
		p.byAccount[accountID] = current
	}

	// 4. Count this call before unlocking, so a later call's step 2 keeps
	// the pace while this call uses it.
	current.calls++
	return current.limiter
}

// release ends one call's use of accountID's pace.
func (p *paces) release(accountID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.byAccount[accountID]
	current.calls--
	current.ended = time.Now()
}

// fetchMetadata returns providerID's metadata, waiting on pace before each
// request, or nil when Gmail no longer has the message. A message whose
// headers alone pass [maxResponseBytes] is fetched again without them.
func (c *Client) fetchMetadata(ctx context.Context, service *gmail.Service, pace *rate.Limiter, providerID string) (*gmail.Message, error) {
	get := func(format string, fields googleapi.Field) (*gmail.Message, error) {
		return retry(ctx, c, func() (*gmail.Message, error) {
			// Inside the retry, so every attempt waits for its units: Gmail
			// charges each request, a retried one too.
			if err := pace.WaitN(ctx, metadataUnits); err != nil {
				return nil, err
			}
			call := service.Users.Messages.Get("me", providerID).Format(format).Fields(fields)
			if format == formatMetadata {
				call = call.MetadataHeaders(metadataHeaders...)
			}
			return call.Context(ctx).Do()
		})
	}
	message, err := get(formatMetadata, metadataFields)
	// A sender can make headers past any size; the message still shows,
	// by its labels, snippet, and date, rather than stopping every sync.
	if errors.Is(err, errResponseTooLarge) {
		message, err = get(formatMinimal, minimalFields)
	}
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if message.Id != providerID {
		return nil, fmt.Errorf("gmail api: asked for one message, got another")
	}
	return message, nil
}

// metadataHeaders are the headers messages.get returns in metadata
// format, all that [messageFromGmail] reads.
var metadataHeaders = []string{
	headerFrom, headerReplyTo, headerTo, headerCc, headerSubject,
	headerMessageID, headerInReplyTo, headerReferences,
}

// messages.get's formats inbok asks for.
//
// NOTE: Google's Go client exports none of these as constants; only its doc
// comments list them, as text. A name Google changed would not fail the
// build, only the calls.
const (
	formatMetadata = "metadata"
	formatMinimal  = "minimal"
)

// isNotFound reports whether err is Gmail's 404.
func isNotFound(err error) bool {
	refused, ok := errors.AsType[*googleapi.Error](err)
	return ok && refused.Code == http.StatusNotFound
}

// messageFromGmail returns m as inbok keeps it: its labels as mailboxes
// and flags (ADR007, "Gmail labels as mailboxes"), its headers decoded,
// and its snippet as text.
func messageFromGmail(m *gmail.Message) mailsync.Message {
	var h headers
	if m.Payload != nil {
		h = m.Payload.Headers
	}
	message := mailsync.Message{
		ProviderID:   m.Id,
		ThreadID:     m.ThreadId,
		InternalDate: time.UnixMilli(m.InternalDate).UTC(),
		Subject:      decodeText(h.get(headerSubject)),
		From:         parseAddresses(h.get(headerFrom)),
		ReplyTo:      parseAddresses(h.get(headerReplyTo)),
		To:           parseAddresses(h.get(headerTo)),
		CC:           parseAddresses(h.get(headerCc)),
		// Gmail sends the snippet HTML-escaped, e.g. an apostrophe as &#39;.
		Preview:    cleanText(html.UnescapeString(m.Snippet)),
		Seen:       !slices.Contains(m.LabelIds, labelUnread),
		Flagged:    slices.Contains(m.LabelIds, labelStarred),
		InReplyTo:  parseMsgIDs(h.get(headerInReplyTo)),
		References: parseReferences(h.full(headerReferences)),
	}
	if ids := parseMsgIDs(h.get(headerMessageID)); len(ids) > 0 {
		message.MessageID = ids[0]
	}
	for _, labelID := range m.LabelIds {
		if !isFlag(labelID) {
			message.MailboxProviderIDs = append(message.MailboxProviderIDs, labelID)
		}
	}
	return message
}
