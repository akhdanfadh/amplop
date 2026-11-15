package conversation

import (
	"context"
	"errors"
	"slices"
	"time"
	"uuid"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// Errors a caller shows the member.
var (
	// ErrNotFound means the member has no conversation with that ID,
	// including one of another member's accounts.
	ErrNotFound = errors.New("conversation not found")
	// ErrPlaceNotFound means the member has no label with the place's ID:
	// a mailbox of theirs with no role that they made.
	ErrPlaceNotFound = errors.New("place not found")
	// ErrAccountNotFound means the member has no connected account with the
	// filter's ID.
	ErrAccountNotFound = errors.New("connected account not found")
	// ErrInvalidPlace means a place is neither a view's name nor a mailbox
	// ID.
	ErrInvalidPlace = errors.New("place is neither a view nor a mailbox ID")
	// ErrInvalidLimit means a page's size is outside 1 to [MaxLimit].
	ErrInvalidLimit = errors.New("page size out of range")
)

// Page sizes: the web app loads pages of [DefaultLimit] and asks for one
// more row to learn whether another page follows (ADR031).
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// EveryAccount, as a query's account, lists the member's every account.
const EveryAccount int64 = 0

// View is one of the places every account has, by its name in URLs.
type View string

// The views every account has.
const (
	ViewInbox   View = "inbox"
	ViewStarred View = "starred"
	ViewSent    View = "sent"
	ViewAll     View = "all"   // every visible conversation: neither Spam nor Trash
	ViewSpam    View = "spam"  // the junk role
	ViewTrash   View = "trash" // the trash role
)

// Views lists every view, in a fixed order.
var Views = []View{ViewInbox, ViewStarred, ViewSent, ViewAll, ViewSpam, ViewTrash}

// Place is where a list of conversations is: a view, or one of the
// member's labels by its mailbox ID. Exactly one is set.
type Place struct {
	View    View
	Mailbox uuid.UUID
}

// ParsePlace returns the place text names: a view's name, or a mailbox
// ID. It returns [ErrInvalidPlace] for anything else.
func ParsePlace(text string) (Place, error) {
	for _, view := range Views {
		if text == string(view) {
			return Place{View: view}, nil
		}
	}
	id, err := uuid.Parse(text)
	if err != nil || id == uuid.Nil() {
		return Place{}, ErrInvalidPlace
	}
	return Place{Mailbox: id}, nil
}

// String returns the place as [ParsePlace] reads it.
func (p Place) String() string {
	if p.View != "" {
		return string(p.View)
	}
	return p.Mailbox.String()
}

// Conversation is one row of a list: what its visible messages, or its
// Spam and Trash ones, add up to.
type Conversation struct {
	ID        uuid.UUID
	AccountID int64
	// LatestDate is its newest message's [mailsync.Message.InternalDate],
	// which lists sort by, newest first, then by ID.
	LatestDate time.Time
	// Subject is its oldest message's, as sent: the original's, or a
	// reply's, with its "Re:", when the original is not copied.
	Subject string
	// Preview is its newest message's.
	Preview string
	// LatestFrom is its newest message's From, perhaps empty.
	LatestFrom []mailsync.Address
	// Participants are its messages' senders in date order, each address
	// once, at most 10.
	Participants []mailsync.Address
	MessageCount int
	// Unread is whether a message is not seen.
	Unread bool
	// Flagged is whether a message is starred.
	Flagged bool
	// The places it is in. The Starred view lists it when
	// [Conversation.Flagged] and [Conversation.InAllMail].
	InInbox   bool
	InSent    bool
	InSpam    bool
	InTrash   bool
	InAllMail bool
	// LabelIDs are the member's own mailboxes it is in, in a stable order,
	// by name.
	LabelIDs []uuid.UUID
}

// Source is where a conversation comes from: its account's provider and
// address, and its thread there.
type Source struct {
	Provider connectedaccount.Provider
	Address  string
	ThreadID string
}

// Detail is one conversation as [Service.Get] returns it.
type Detail struct {
	Conversation Conversation
	Source       Source
	// Messages are its thread's messages, oldest first.
	Messages []Message
	// ProviderURL is where its provider's own web app shows it, empty when
	// the provider has none.
	ProviderURL string
}

// Linker finds conversations in their providers' own web apps.
type Linker interface {
	// ConversationURL returns where the web app of source's provider shows
	// c, or empty for a provider it knows no web app of.
	ConversationURL(source Source, c Conversation) string
}

// Cursor is the row a page continues from: its date and ID. The zero ID
// continues below every row of its date, so the page holds only older
// dates.
type Cursor struct {
	Date time.Time
	ID   uuid.UUID
}

// Query asks for one page of a place's conversations.
type Query struct {
	Place Place
	// Account limits the page to one account; [EveryAccount] lists all.
	Account int64
	// Before is the last, oldest row of the page before, and the page
	// holds older rows; nil starts at the newest.
	Before *Cursor
	// Limit is how many rows to return, at most.
	Limit int
}

// PlaceCount is how many conversations of one account's place are unread.
type PlaceCount struct {
	Account int64
	Place   Place
	// Name is a label's own name, as [mailsync.Mailbox.Name]; empty for a
	// view.
	Name string
	// Parent is the label a label nests under, one of the same account's
	// own labels; [uuid.Nil] for a view and a label at the top.
	Parent uuid.UUID
	Unread int
}

// Storage reads conversations, each derived from synchronized mail.
type Storage interface {
	// ListConversations returns up to query.Limit of userID's
	// conversations in query.Place, newest first, then by ID descending,
	// each older than query.Before. It returns [ErrAccountNotFound] when
	// userID has no account query.Account, and [ErrPlaceNotFound] when
	// userID has no label query.Place.Mailbox. A label of an account other
	// than query.Account lists nothing.
	ListConversations(ctx context.Context, userID int64, query Query) ([]Conversation, error)

	// CountConversationsAfter returns how many of userID's conversations in
	// place, of account or [EveryAccount], are dated after after, with the
	// errors of [Storage.ListConversations].
	CountConversationsAfter(ctx context.Context, userID int64, place Place, account int64, after time.Time) (int, error)

	// ListPlaceCounts returns how many conversations of each place of
	// userID's accounts are unread, in a stable order: the accounts in the
	// order they were connected, each with every view in [Views] order, then
	// its own labels by name, each with the label it nests under.
	ListPlaceCounts(ctx context.Context, userID int64) ([]PlaceCount, error)

	// ReadConversation returns userID's conversation id with its source
	// and every message of its thread, drafts too, oldest first, each with
	// its mailboxes by name, or [ErrNotFound]; [Detail.ProviderURL] is
	// left empty. It checks the conversation is userID's first, then reads
	// its messages from the same snapshot, so they are the thread's as that
	// check saw it.
	ReadConversation(ctx context.Context, userID int64, id uuid.UUID) (Detail, error)
}

// Service answers a member's reads of their conversations.
type Service struct {
	storage Storage
	linker  Linker
}

// NewService returns a [Service] that reads with storage and finds
// conversations in their providers' web apps with linker.
func NewService(storage Storage, linker Linker) *Service {
	return &Service{storage: storage, linker: linker}
}

// List returns one page of userID's conversations as query asks, with
// the errors of [Storage.ListConversations] and [ErrInvalidLimit].
func (s *Service) List(ctx context.Context, userID int64, query Query) ([]Conversation, error) {
	if query.Limit < 1 || query.Limit > MaxLimit {
		return nil, ErrInvalidLimit
	}
	return s.storage.ListConversations(ctx, userID, query)
}

// CountAfter returns how many of userID's conversations in place, of
// account or [EveryAccount], are newer than after, e.g. the newest one a
// list shows, with the errors of [Storage.ListConversations].
func (s *Service) CountAfter(ctx context.Context, userID int64, place Place, account int64, after time.Time) (int, error) {
	return s.storage.CountConversationsAfter(ctx, userID, place, account, after)
}

// Places returns the unread count of every place of userID's accounts,
// each label with its parent, in [Storage.ListPlaceCounts] order.
func (s *Service) Places(ctx context.Context, userID int64) ([]PlaceCount, error) {
	return s.storage.ListPlaceCounts(ctx, userID)
}

// Get returns userID's conversation id with its messages but drafts,
// oldest first, each linked to the message it answers ([Message.Answers]),
// and where its provider's web app shows it, or [ErrNotFound].
func (s *Service) Get(ctx context.Context, userID int64, id uuid.UUID) (Detail, error) {
	detail, err := s.storage.ReadConversation(ctx, userID, id)
	if err != nil {
		return Detail{}, err
	}
	// A draft shows nowhere (ADR001, "Conversations").
	detail.Messages = slices.DeleteFunc(detail.Messages, Message.draft)
	linkAnswers(detail.Messages)
	detail.ProviderURL = s.linker.ConversationURL(detail.Source, detail.Conversation)
	return detail, nil
}

// linkAnswers sets each of messages' [Message.Answers]: the message among
// messages whose Message-ID is the last of its In-Reply-To. In-Reply-To
// lists the parents a message answers (RFC 5322 section 3.6.4); taking
// the last as the one answered is inbok's choice, as a reply names one
// parent and a mailer adding more puts the newest last. Of two copies
// with one Message-ID, e.g. a message the account sent itself, it
// answers the first in messages' order.
func linkAnswers(messages []Message) {
	byMessageID := make(map[string]uuid.UUID, len(messages))
	for _, m := range messages {
		if _, seen := byMessageID[m.MessageID]; m.MessageID != "" && !seen {
			byMessageID[m.MessageID] = m.ID
		}
	}
	for i, m := range messages {
		if len(m.InReplyTo) == 0 {
			continue
		}
		if answered, ok := byMessageID[m.InReplyTo[len(m.InReplyTo)-1]]; ok && answered != m.ID {
			messages[i].Answers = answered
		}
	}
}
