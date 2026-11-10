package mailsync

import (
	"context"
	"errors"
	"time"

	"inbok/internal/connectedaccount"
)

var (
	// ErrPositionExpired means the provider no longer keeps changes since
	// the position asked for, or the position is not one it can read, so
	// only a full sync brings the copy current.
	ErrPositionExpired = errors.New("provider no longer has changes since this position")
	// ErrProviderBusy means the provider kept refusing for its rate limits,
	// or kept failing, past the retries, or asked to wait longer than a
	// retry waits. Trying later may succeed.
	ErrProviderBusy = errors.New("provider is rate limiting or unavailable")
)

// ListQuery asks for one page of an account's messages.
type ListQuery struct {
	// Since leaves out messages the provider received at or before it, by
	// the provider's own timestamp.
	Since time.Time
	// IncludeJunkAndTrash lists the messages in the mailboxes with
	// [RoleJunk] and [RoleTrash] too; false leaves them out.
	IncludeJunkAndTrash bool
	// PageToken is the [MessagePage.NextPageToken] of the page before;
	// empty for the first page.
	PageToken string
}

// MessagePage is one page of a listing, newest first.
type MessagePage struct {
	// ProviderIDs are the listed messages' [Message.ProviderID]s.
	ProviderIDs []string
	// NextPageToken asks for the next page; empty on the last.
	NextPageToken string
}

// Fetched is the metadata of the messages [Provider.Messages] asked for.
type Fetched struct {
	// Messages are the ones the provider still has, in the order asked,
	// without a [Message.ID] or [Message.AccountID].
	Messages []Message
	// Gone are the ProviderIDs of the ones it no longer has.
	Gone []string
}

// ChangePage is one page of what changed in an account's mail since a
// position, each message in at most one of its lists.
type ChangePage struct {
	// Fetch are the ProviderIDs of messages whose metadata is fetched
	// whole, e.g. those added.
	Fetch []string
	// Update are the changes to the mailboxes and flags of messages the
	// provider had before the page, net of the page. The copy may not
	// hold every one.
	Update []MessageUpdate
	// Deleted are the ProviderIDs of messages the provider deleted,
	// whatever else happened to them in the page.
	Deleted []string
	// Position is where the account stands now: once the last page is
	// written, the next poll asks for changes since it, as
	// [SyncState.Position].
	Position string
	// NextPageToken asks for the next page; empty on the last.
	NextPageToken string
}

// MessageUpdate is how one message's mailboxes and flags changed. It says
// where the message stands after the page on what the page changed, not
// the steps in between, so applying it twice changes nothing more.
type MessageUpdate struct {
	// ProviderID is the message's [Message.ProviderID].
	ProviderID string
	// MailboxesAdded are the [Mailbox.ProviderID]s of mailboxes the
	// message is in after the page and may not have been in before.
	MailboxesAdded []string
	// MailboxesRemoved are those of mailboxes it is not in after the page.
	MailboxesRemoved []string
	// Seen is [Message.Seen] after the page; nil when the page did not
	// change it.
	Seen *bool
	// Flagged is [Message.Flagged] after the page; nil when the page did
	// not change it.
	Flagged *bool
}

// Provider reads an account's mail at its provider, with an access token
// for the account. Every method only reads, so a retried call changes
// nothing. Each returns [ErrProviderBusy] when the provider kept refusing
// for its rate limits, and an error naming only the provider's status for
// any other refusal.
//
// DESIGN: A position is the provider's own text, which only its
// implementation reads, e.g. Gmail's history ID. Synchronization only
// stores it and passes it back, so the interface names no provider's kind
// of position.
type Provider interface {
	// Mailboxes returns all of the account's mailboxes.
	Mailboxes(ctx context.Context, token connectedaccount.AccessToken) ([]Mailbox, error)
	// Position returns where the account stands now, which a poll asks for
	// changes since, as [SyncState.Position].
	Position(ctx context.Context, token connectedaccount.AccessToken) (string, error)
	// ListMessages returns one page of the account's messages that query
	// asks for.
	ListMessages(ctx context.Context, token connectedaccount.AccessToken, query ListQuery) (MessagePage, error)
	// Messages returns the metadata of accountID's messages with
	// providerIDs, keeping the account's fetches, across calls, to the
	// provider's limits for one account.
	Messages(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerIDs []string) (Fetched, error)
	// Changes returns one page of what changed since position, the
	// [ChangePage.Position] of an earlier poll or a full sync's
	// [Provider.Position], reduced to what to fetch, what to update, and what to
	// delete; the order within the page is not kept, so pages are applied in
	// order. pageToken is the [ChangePage.NextPageToken] of the page before;
	// empty for the first. It returns [ErrPositionExpired] when the provider no
	// longer keeps changes that old, or cannot read position.
	Changes(ctx context.Context, token connectedaccount.AccessToken, position string, pageToken string) (ChangePage, error)
}
