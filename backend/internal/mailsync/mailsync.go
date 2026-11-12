// Package mailsync copies each connected account's recent mail metadata and
// mailboxes from the provider into inbok, and keeps the copy current (ADR001).
// The provider stays authoritative: a message gone there is deleted here. It
// holds the types later features read, a [Mailbox], a [Message], and a
// [SyncState], the [Storage] that every synchronized write goes through, and
// the [Provider] it reads mail from.
//
// DESIGN: The types follow JMAP Mail (RFC 8621), which fits both folders and
// Gmail labels: a mailbox is a named set of messages with an optional [Role], a
// message is in a set of mailboxes, and read and starred are flags on the
// message. Provider rules, e.g. that Gmail's UNREAD label means not seen, stay
// in the code that reads the provider, so features that read mail never see them.
package mailsync

import (
	"context"
	"errors"
	"time"
	"uuid"
)

var (
	// ErrNotFound means the account has no sync state: it was disconnected,
	// or never connected.
	ErrNotFound = errors.New("synchronized account not found")
	// ErrUnknownMailbox means a saved message names a mailbox the account
	// does not have, e.g. a Gmail label made since the mailboxes were last
	// listed. Nothing was written; listing the mailboxes again mends it.
	ErrUnknownMailbox = errors.New("message names a mailbox the account does not have")
)

// Role is the purpose of a special mailbox, whatever its name: an attribute of
// IANA's IMAP Mailbox Name Attributes registry that JMAP uses as a role,
// lowercased (RFC 8621 section 2; RFC 6154, RFC 8457, RFC 9979). The empty role
// is the member's own mailbox, or one the provider made for no listed purpose.
type Role string

const (
	RoleNone      Role = ""
	RoleAll       Role = "all"     // every message, perhaps but junk and trash, e.g. a server's All Mail folder
	RoleArchive   Role = "archive" // a folder for archived mail; Gmail archives by leaving the inbox
	RoleDrafts    Role = "drafts"
	RoleFlagged   Role = "flagged"   // a folder of flagged messages, which IMAP servers may show
	RoleImportant Role = "important" // what the provider judged important, e.g. Gmail's IMPORTANT
	RoleInbox     Role = "inbox"
	RoleJunk      Role = "junk"      // spam
	RoleMemos     Role = "memos"     // the member's notes on other messages, stored as messages
	RoleScheduled Role = "scheduled" // messages waiting to be sent later
	RoleSent      Role = "sent"
	RoleSnoozed   Role = "snoozed" // messages hidden until a time the member chose
	RoleTrash     Role = "trash"
)

// Mailbox is one of an account's mailboxes: a Gmail label, or later a folder.
type Mailbox struct {
	// ProviderID is the provider's ID for it, e.g. Gmail's label ID, which
	// [Message.MailboxProviderIDs] lists.
	ProviderID string
	// Name is the provider's, as received; a nested Gmail label is one name
	// with slashes, e.g. Work/Projects.
	Name string
	Role Role
	// MadeBy is who made it. The labels a member sees are their own: no
	// role, made by the member.
	MadeBy Maker
}

// Maker is who made a mailbox.
type Maker string

const (
	// MadeByProvider is a mailbox the provider made for its own purpose,
	// e.g. Gmail's INBOX or CATEGORY_SOCIAL, or Outlook's well-known
	// folders.
	MadeByProvider Maker = "provider"
	// MadeByMember is a mailbox the member made, e.g. a Gmail label of
	// their own, or any IMAP folder.
	MadeByMember Maker = "member"
)

// Address is one entry of an address header: a display name, which may be
// empty, and the address. An entry that was not an address keeps its
// decoded text as the name and an empty address.
type Address struct {
	Name    string
	Address string
}

// Message is the metadata of one provider message as inbok keeps it.
type Message struct {
	// ID is inbok's own, random, given when the message is first saved.
	ID        uuid.UUID
	AccountID int64
	// ProviderID is the provider's ID for the message, e.g. Gmail's
	// message ID, unique within the account.
	ProviderID string
	// ThreadID is the provider's ID for its conversation, e.g. Gmail's thread ID.
	ThreadID string
	// InternalDate is the provider's own timestamp, which the window counts
	// from (ADR001): for Gmail, when Google accepted received mail, which
	// the Date header cannot fake, though imported mail may take its Date.
	InternalDate time.Time
	Subject      string
	From         []Address
	ReplyTo      []Address
	To           []Address
	CC           []Address
	// Preview is the provider's plain-text excerpt of the body, e.g.
	// Gmail's snippet.
	Preview string
	// Seen is whether the member has read it.
	Seen bool
	// Flagged is whether the member starred it.
	Flagged bool
	// MailboxProviderIDs are the [Mailbox.ProviderID]s of the mailboxes it
	// is in, perhaps none. An archived Gmail message is in no mailbox with
	// [RoleInbox] but keeps its other labels.
	MailboxProviderIDs []string
	// Version is the provider's change number this state was read at,
	// which grows with every change, e.g. Gmail's historyId; never 0.
	Version uint64
	// MessageID is the Message-ID header's msg-id, with its angle brackets;
	// empty when the header is missing.
	MessageID string
	// InReplyTo and References are the msg-ids of those headers, in order,
	// each with its angle brackets; a reply threads with them and
	// [Message.MessageID].
	InReplyTo  []string
	References []string
}

// Status is where an account's full sync stands, as the member sees it.
type Status string

const (
	// StatusWaiting means a full sync is saved but has not started.
	StatusWaiting Status = "waiting"
	// StatusRunning means a full sync is copying messages.
	StatusRunning Status = "running"
	// StatusComplete means the last full sync finished; polls keep the
	// copy current.
	StatusComplete Status = "complete"
	// StatusFailed means the last full sync stopped on [SyncState.Failure].
	StatusFailed Status = "failed"
)

// Failure is why the last sync, full or poll, failed.
type Failure string

const (
	// FailureProvider means the provider kept failing past the retries.
	FailureProvider Failure = "provider"
	// FailureTokenUnreadable means the saved refresh token no longer opens,
	// e.g. after a lost encryption key.
	FailureTokenUnreadable Failure = "token-unreadable"
	// FailureInternal means inbok failed for any other reason.
	FailureInternal Failure = "internal"
)

// SyncState is where an account's synchronization stands.
type SyncState struct {
	AccountID int64
	Status    Status
	// Position is where the account stood at the provider when last read,
	// which the next poll asks for changes since: text only the [Provider]
	// reads, e.g. Gmail's history ID. Empty before a full sync records one.
	Position string
	// Generation numbers the account's full syncs from 1; 0 before the first
	// starts. Each message records the generation that last saved it.
	Generation int
	// Failure is why the last sync, full or poll, failed; empty once one
	// succeeds. Never empty when [SyncState.Status] is [StatusFailed]; a
	// complete account keeps a failed poll's.
	Failure Failure
	// FailedAt is when [SyncState.Failure] happened; nil exactly when it is
	// empty.
	FailedAt *time.Time
	// LastSyncedAt is when inbok last confirmed its copy matched the
	// provider: when a sync, full or poll, last finished, or a check found
	// the position unchanged; nil before the first.
	LastSyncedAt *time.Time
}

// Change is what one synchronization step writes, all in one transaction.
type Change struct {
	// State replaces the account's sync state; its [SyncState.AccountID]
	// is ignored.
	State SyncState
	// Mailboxes, when not nil, are all of the account's mailboxes: each is
	// added or updated by [Mailbox.ProviderID], and one no longer listed is
	// deleted, leaving its messages in their other mailboxes.
	Mailboxes []Mailbox
	// Saved are messages to add, or to update when the account already has
	// one with the same [Message.ProviderID], which keeps its ID. Their
	// mailboxes replace the ones saved before. When one ProviderID appears
	// more than once, the last wins. Their ID and AccountID are ignored.
	// Each records the [SyncState.Generation] of [Change.State].
	Saved []Message
	// Deleted are the ProviderIDs of messages to delete; one the account
	// does not have, or one also in [Change.Saved], is skipped, so a
	// repeated change keeps a saved message's ID.
	Deleted []string
	// Updated replace the mailboxes and flags of messages the account has,
	// after [Change.Saved]; one it does not have is skipped, and so is one
	// whose stored [Message.Version] is newer than the update's. When one
	// ProviderID appears more than once, the last wins.
	Updated []MessageUpdate
	// DeleteEarlier is how many, at most, of the account's messages that no
	// step of the generation of [Change.State] saved to delete, after
	// [Change.Saved]: the provider no longer has them. 0 deletes none.
	DeleteEarlier int
}

// DueSync is an account whose synchronization the schedule may start,
// with where its sync state stands.
type DueSync struct {
	AccountID int64
	Status    Status
	// Position is [SyncState.Position].
	Position string
	// Failure is [SyncState.Failure].
	Failure Failure
}

// Confirmed is a complete account whose position at the provider was found
// unchanged, so its copy matched the provider at a time.
type Confirmed struct {
	AccountID int64
	// Position is the stored [SyncState.Position] the provider's matched.
	Position string
	// At is when the provider answered.
	At time.Time
}

// Written is what [Storage.UpdateSyncedMail] wrote.
type Written struct {
	// State is the saved sync state.
	State SyncState
	// EarlierDeleted counts the messages [Change.DeleteEarlier] deleted.
	EarlierDeleted int
}

// Storage keeps synchronized mail.
type Storage interface {
	// UpdateSyncedMail locks accountID's sync state, calls update with it, and
	// writes the [Change] update returns, in one transaction: the mailboxes, then
	// the deletes, the saves, the updates, the earlier generations' deletes,
	// the job that deletes the deleted messages' stored content, and last the
	// state. It returns what it wrote,
	// [ErrNotFound] when the account has no sync state, and [ErrUnknownMailbox]
	// when a saved or updated message names a mailbox the account does not have
	// after the change's mailboxes. On any error it writes nothing; an error
	// from update is returned unchanged.
	UpdateSyncedMail(ctx context.Context, accountID int64, update func(SyncState) (Change, error)) (Written, error)
	// SyncState returns accountID's sync state, or [ErrNotFound] when it has
	// none.
	SyncState(ctx context.Context, accountID int64) (SyncState, error)
	// OldestMessage returns when the provider received accountID's oldest
	// message, and false when it has none.
	OldestMessage(ctx context.Context, accountID int64) (time.Time, bool, error)
	// DueSyncs returns the accounts whose provider work may run, i.e. whose
	// member is active and which need no reconnect, and that are complete,
	// waiting, running, or failed at or before failedBefore.
	DueSyncs(ctx context.Context, failedBefore time.Time) ([]DueSync, error)
	// ConfirmSynced records each of confirmed as its account's
	// [SyncState.LastSyncedAt], where the account is still complete at its
	// [Confirmed.Position] with no failure and the time moves forward, so a
	// sync that finished meanwhile keeps its own.
	ConfirmSynced(ctx context.Context, confirmed []Confirmed) error
	// Reports returns the reports of those of accountIDs that have a sync
	// state, in account ID order.
	Reports(ctx context.Context, accountIDs []int64) ([]Report, error)
	// RequestSync, in one transaction: 1. locks userID's account
	// accountID's sync state; 2. calls request with what it finds; 3.
	// records the time request returns as [Report.RequestedAt]; and 4.
	// saves the account's sync run, unless one is already queued or
	// running, which keeps its place. It returns the account's report
	// after, and [ErrNotFound] when userID has no account accountID. An
	// error from request is returned unchanged, and nothing is written.
	RequestSync(ctx context.Context, userID, accountID int64, request func(SyncRequest) (time.Time, error)) (Report, error)
}
