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

	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
)

// Gmail's per-user quota and what each fetch costs of it (Gmail's usage
// limits): 6,000 units a minute per user per project, 20 for a
// messages.get in metadata format, 60 in full format, and 20 for a
// messages.attachments.get.
const (
	userUnitsPerMinute = 6000
	metadataUnits      = 20
	fullUnits          = 60
	attachmentUnits    = 20
)

const (
	// fetchUnitsPerMinute is the share of [userUnitsPerMinute] one account's
	// fetches spend, amplop's choice: four fifths, leaving room for the same
	// account's sends (100 units each), polls, and the messages its member
	// opens meanwhile. That paces metadata fetches at 4 a second, full ones
	// at 4 every 3 seconds. The pace is this process's: the worker runs
	// one sync and one body fetch per account at a time, while the API's
	// fetches for a member who opens a message share its own process's
	// pace, inside the room left.
	fetchUnitsPerMinute = userUnitsPerMinute * 4 / 5
	// fetchConcurrency bounds one call's fetches in flight, amplop's choice.
	// Gmail also limits a user's concurrent requests, at a number it does
	// not publish (Gmail's "Resolve errors" guide).
	fetchConcurrency = 4
	// listPageSize is how many IDs one listing page holds, amplop's choice:
	// fetched at four a second, one page's metadata takes about 25
	// seconds, a bounded step. Gmail allows up to 500.
	listPageSize = 100
)

// Gmail's own fields for the answers amplop reads, so it sends nothing
// else (Gmail's performance tips, "Partial response").
const (
	listFields     = "messages/id,nextPageToken"
	metadataFields = "id,threadId,labelIds,historyId,snippet,internalDate,payload/headers"
	minimalFields  = "id,threadId,labelIds,historyId,snippet,internalDate"
	profileFields  = "historyId"
)

// Position returns the account's current history ID, from
// users.getProfile, as decimal text.
func (c *Client) Position(ctx context.Context, token connectedaccount.AccessToken) (string, error) {
	historyID, err := c.historyID(ctx, token)
	if err != nil {
		return "", err
	}
	return formatHistoryID(historyID), nil
}

// Version returns the account's current history ID, from
// users.getProfile: every change made after it carries a greater one, as
// a mailbox's history IDs grow in time order and a message's historyId is
// the ID of its last change (Gmail's users.history.list and Message
// references).
func (c *Client) Version(ctx context.Context, token connectedaccount.AccessToken) (uint64, error) {
	return c.historyID(ctx, token)
}

// historyID returns the account's current history ID, from
// users.getProfile.
func (c *Client) historyID(ctx context.Context, token connectedaccount.AccessToken) (uint64, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return 0, err
	}
	profile, err := retry(ctx, c, func() (*gmail.Profile, error) {
		return service.Users.GetProfile(gmailUser).Fields(profileFields).Context(ctx).Do()
	})
	if err != nil {
		return 0, redactGmail(err)
	}
	return profile.HistoryId, nil
}

// ListMessages returns one page of the account's messages Gmail received
// after query's Since, in whole seconds, newest first, from messages.list.
func (c *Client) ListMessages(ctx context.Context, token connectedaccount.AccessToken, query mailsync.ListQuery) (mailsync.MessagePage, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return mailsync.MessagePage{}, err
	}
	call := service.Users.Messages.List(gmailUser).
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
// ends, amplop's choice: by then its bucket has long refilled, so a new
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

	// 3. Take the account's pace, or start a full one. Its bucket holds the
	// costliest fetch, which could never wait for more than the bucket holds.
	current, ok := p.byAccount[accountID]
	if !ok {
		current = &pace{limiter: rate.NewLimiter(rate.Limit(float64(fetchUnitsPerMinute)/60), fullUnits)}
		p.byAccount[accountID] = current
	}

	// 4. Count this call before unlocking, so a later call's step 2 keeps
	// the pace while this call uses it.
	current.calls++
	return current.limiter
}

// waitPace waits on pace until n units are free. It returns ctx's error
// once ctx ends, and an error wrapping [mailsync.ErrProviderBusy] when the
// wait would outlast ctx's deadline: the account's share of Gmail's quota
// is spent for now, and no request was sent.
func waitPace(ctx context.Context, pace *rate.Limiter, n int) error {
	// NOTE: WaitN refuses at once, before the deadline, with an error of its
	// own, when the wait would pass it.
	err := pace.WaitN(ctx, n)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: the account's gmail quota share is spent for now", mailsync.ErrProviderBusy)
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
			if err := waitPace(ctx, pace, metadataUnits); err != nil {
				return nil, err
			}
			call := service.Users.Messages.Get(gmailUser, providerID).Format(format).Fields(fields)
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
	// NOTE: Google's client answers a JSON null, or a 204, with no value
	// and no error.
	if message == nil {
		return nil, errGmailAnswer
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

// messages.get's formats amplop asks for.
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

// messageFromGmail returns m as amplop keeps it: its labels as mailboxes
// and flags, its headers decoded, and its snippet as text.
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
		InReplyTo:  parseMsgIDs(h.get(headerInReplyTo)),
		References: parseReferences(h.full(headerReferences)),
	}
	if ids := parseMsgIDs(h.get(headerMessageID)); len(ids) > 0 {
		message.MessageID = ids[0]
	}
	message.MailboxProviderIDs, message.Seen, message.Flagged = labelState(m.LabelIds)
	message.Version = m.HistoryId
	return message
}

// labelState returns a message's labelIDs as amplop keeps them: every label
// but UNREAD and STARRED as its mailboxes, nil for none, and those two as
// whether it is seen and flagged.
func labelState(labelIDs []string) (mailboxes []string, seen, flagged bool) {
	for _, labelID := range labelIDs {
		if !isFlag(labelID) {
			mailboxes = append(mailboxes, labelID)
		}
	}
	return mailboxes, !slices.Contains(labelIDs, labelUnread), slices.Contains(labelIDs, labelStarred)
}
