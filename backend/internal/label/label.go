// Package label creates, renames, moves, and deletes a member's own labels
// at their account's provider, then saves the account's mailboxes as the
// provider lists them after, as a sync saves them.
//
// DESIGN: a label is a mailbox of the member's with no role. The provider
// stays authoritative: inbok saves only what the provider lists after a
// change, never the change it asked for, so it shows nothing the provider
// does not have, and copies none of its naming rules beyond the checks a
// provider's code makes before calling it.
package label

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// MaxNameLength is how many characters a label's own name holds at most,
// inbok's choice: Gmail's limit on a label's whole name, so no own name
// passes that Gmail refuses alone (checked on inbok's test account,
// 2026-11-16). A provider's code also checks the whole name, with the
// names of the labels it is under.
const MaxNameLength = 225

// Errors of [Service] a caller shows the member. A [Provider] returns
// those of names and parents too, and [*SeparatorError].
var (
	// ErrNotFound means the member has no label with that ID: a mailbox of
	// theirs with no role that they made.
	ErrNotFound = errors.New("label not found")
	// ErrNameBlank means a name has nothing but white space.
	ErrNameBlank = errors.New("label name is blank")
	// ErrNameTooLong means a name is longer than [MaxNameLength], or the
	// provider's whole name for it, with the names of the labels it is
	// under, is longer than the provider takes.
	ErrNameTooLong = errors.New("label name is too long")
	// ErrNameReserved means the provider keeps the name for its own
	// mailboxes, e.g. Gmail's Inbox.
	ErrNameReserved = errors.New("label name is the provider's own")
	// ErrNameTaken means the account already has a mailbox of that name
	// where the label would be, as the provider compares names.
	ErrNameTaken = errors.New("label name is taken")
	// ErrInvalidParent means the label to nest under is none of the
	// account's labels, or is the label itself or one inside it.
	ErrInvalidParent = errors.New("parent label is not one the label can nest under")
)

// SeparatorError means a name holds the character the provider nests
// mailboxes by, so the provider would read it as more than one level.
type SeparatorError struct {
	// Separator is the provider's separator, e.g. Gmail's "/".
	Separator string
}

// Error returns a message naming no name.
func (e *SeparatorError) Error() string {
	return "label name holds the provider's separator"
}

// Label is one of a member's own labels.
type Label struct {
	ID        uuid.UUID
	AccountID int64
	// ProviderID is the provider's ID of its mailbox, as
	// [mailsync.Mailbox.ProviderID].
	ProviderID string
	// Name is its own name under its parent, as [mailsync.Mailbox.Name].
	Name string
	// Parent is the label it nests under, one of the same account's
	// labels; [uuid.Nil] at the top.
	Parent uuid.UUID
}

// Storage reads a member's labels and saves an account's mailboxes.
type Storage interface {
	// ReadLabelAccount returns the account of userID's label id, or
	// [ErrNotFound].
	ReadLabelAccount(ctx context.Context, userID int64, id uuid.UUID) (int64, error)

	// ListLabels returns the labels of userID's account accountID, or
	// [connectedaccount.ErrNotFound] when userID has no such account.
	ListLabels(ctx context.Context, userID, accountID int64) ([]Label, error)

	// SaveMailboxes makes mailboxes accountID's mailboxes as a sync saves
	// [mailsync.Change.Mailboxes], keeping the sync state: a mailbox
	// listed keeps its ID, and one no longer listed is deleted with its
	// memberships. It returns [connectedaccount.ErrNotFound] when the
	// account is gone.
	SaveMailboxes(ctx context.Context, accountID int64, mailboxes []mailsync.Mailbox) error
}

// accountAccess hands out access tokens, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// Provider changes an account's mailboxes at its provider. Each method
// returns [ErrNameTaken], [ErrNameReserved], [ErrNameTooLong],
// [*SeparatorError], or [ErrInvalidParent] for a change the provider
// refuses, an error wrapping [mailsync.ErrProviderBusy] when the provider
// keeps refusing for its limits, and any other error as a failure.
//
// DESIGN: a provider whose messages sit in one folder each, e.g. IMAP, must
// refuse deleting a folder that holds mail, so a delete never deletes a
// message, and keep each folder's ID across a rename, e.g. by MAILBOXID
// (RFC 8474), so a renamed label keeps its conversations.
type Provider interface {
	// Mailboxes returns all of the account's mailboxes, as
	// [mailsync.Provider.Mailboxes] does.
	Mailboxes(ctx context.Context, token connectedaccount.AccessToken) ([]mailsync.Mailbox, error)

	// CreateMailbox makes a mailbox of the member's named name, under the
	// mailbox parentID or at the top when parentID is empty, and returns
	// its provider ID. It refuses a name holding the separator.
	CreateMailbox(ctx context.Context, token connectedaccount.AccessToken, parentID, name string) (string, error)

	// EditMailbox names the mailbox id name and moves it under the mailbox
	// parentID, or to the top when parentID is empty. The mailboxes inside
	// it follow, keeping their own names. It refuses a name holding the
	// separator unless it is the mailbox's own name now, and returns
	// [ErrNotFound] when the provider has no mailbox id. It changes the
	// mailbox before those inside it, and checks every name it will give
	// before changing any.
	EditMailbox(ctx context.Context, token connectedaccount.AccessToken, id, parentID, name string) error

	// DeleteMailbox deletes the mailbox id and every mailbox inside it, as
	// the provider has them now, the deepest first, and never a message.
	// A mailbox already gone counts as deleted, so asking again finishes a
	// delete that stopped part way; it stops at the first failure,
	// leaving every mailbox above it.
	DeleteMailbox(ctx context.Context, token connectedaccount.AccessToken, id string) error
}

// providerBudget is how long a label change waits on its provider at
// most, inbok's choice, as conversation actions wait.
var providerBudget = 7 * time.Second

// providerReserve is how much of the caller's deadline a change keeps for
// listing the mailboxes after, saving them, and answering, inbok's choice:
// with a request's 10 seconds, [providerBudget] fits whole when the request
// started on time.
var providerReserve = 3 * time.Second

// Service changes a member's labels at their provider, then saves the
// account's mailboxes as the provider lists them after.
type Service struct {
	storage  Storage
	access   accountAccess
	provider Provider
	logger   *slog.Logger
}

// NewService returns a [Service] that reads and saves with storage, takes
// tokens from access, calls provider, and logs to logger.
func NewService(storage Storage, access accountAccess, provider Provider, logger *slog.Logger) *Service {
	return &Service{storage: storage, access: access, provider: provider, logger: logger}
}

// ValidateName returns [ErrNameBlank] for a name with nothing but white
// space, [ErrNameTooLong] for one longer than [MaxNameLength], and nil
// otherwise.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrNameBlank
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return ErrNameTooLong
	}
	return nil
}

// Create makes a label named name in userID's account accountID, under
// the label parent, or at the top when parent is [uuid.Nil], and returns
// it as the provider lists it after, e.g. with its name's white space
// collapsed. It returns the errors of [ValidateName] and [Provider],
// [connectedaccount.ErrNotFound] when userID has no account accountID, and
// those of [connectedaccount.Access.Token] for an account the provider
// can't be reached for.
func (s *Service) Create(ctx context.Context, userID, accountID int64, name string, parent uuid.UUID) (Label, error) {
	// 1. Refuse bad input before any read or provider call.
	if err := ValidateName(name); err != nil {
		return Label{}, err
	}

	// 2. Read the account's labels, which checks the account is the
	// member's, and find the parent among them.
	labels, err := s.storage.ListLabels(ctx, userID, accountID)
	if err != nil {
		return Label{}, err
	}
	parentID, err := parentProviderID(labels, parent, uuid.Nil())
	if err != nil {
		return Label{}, err
	}

	var providerID string
	after, err := s.write(ctx, userID, accountID, func(ctx context.Context, token connectedaccount.AccessToken) error {
		var err error
		providerID, err = s.provider.CreateMailbox(ctx, token, parentID, name)
		return err
	})
	if err != nil {
		return Label{}, err
	}
	for _, label := range after {
		if label.ProviderID == providerID {
			return label, nil
		}
	}
	// Deleted at the provider, or by a sync, before the list after.
	return Label{}, ErrNotFound
}

// Edit names userID's label id name and moves it under the label parent,
// or to the top when parent is [uuid.Nil]; the labels inside it follow. It
// returns the label as the provider lists it after, [ErrNotFound] when
// userID has no label id, and the errors of [Service.Create].
func (s *Service) Edit(ctx context.Context, userID int64, id uuid.UUID, name string, parent uuid.UUID) (Label, error) {
	// 1. Refuse bad input before any read or provider call.
	if err := ValidateName(name); err != nil {
		return Label{}, err
	}

	// 2. Find the label and its account's labels, and the parent among
	// them, neither the label nor one inside it.
	label, labels, err := s.readLabel(ctx, userID, id)
	if err != nil {
		return Label{}, err
	}
	parentID, err := parentProviderID(labels, parent, id)
	if err != nil {
		return Label{}, err
	}

	after, err := s.write(ctx, userID, label.AccountID, func(ctx context.Context, token connectedaccount.AccessToken) error {
		return s.provider.EditMailbox(ctx, token, label.ProviderID, parentID, name)
	})
	if err != nil {
		return Label{}, err
	}
	for _, edited := range after {
		if edited.ID == id {
			return edited, nil
		}
	}
	return Label{}, ErrNotFound
}

// Delete deletes userID's label id and every label inside it at the
// provider, never their mail, and returns the IDs of those labels inbok
// no longer has after. It returns [ErrNotFound] when userID has no label
// id, and the errors of [Service.Create] but those of names and parents.
// A delete that stopped part way returns its error: asking again
// finishes it.
func (s *Service) Delete(ctx context.Context, userID int64, id uuid.UUID) ([]uuid.UUID, error) {
	// 1. Find the label and the labels inside it, as inbok has them now.
	label, labels, err := s.readLabel(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	subtree := append(inside(labels, id), id)

	after, err := s.write(ctx, userID, label.AccountID, func(ctx context.Context, token connectedaccount.AccessToken) error {
		return s.provider.DeleteMailbox(ctx, token, label.ProviderID)
	})
	if err != nil {
		return nil, err
	}
	kept := make(map[uuid.UUID]bool, len(after))
	for _, label := range after {
		kept[label.ID] = true
	}
	deleted := make([]uuid.UUID, 0, len(subtree))
	for _, id := range subtree {
		if !kept[id] {
			deleted = append(deleted, id)
		}
	}
	return deleted, nil
}

// readLabel returns userID's label id and the labels of its account, or
// [ErrNotFound].
func (s *Service) readLabel(ctx context.Context, userID int64, id uuid.UUID) (Label, []Label, error) {
	accountID, err := s.storage.ReadLabelAccount(ctx, userID, id)
	if err != nil {
		return Label{}, nil, err
	}
	labels, err := s.storage.ListLabels(ctx, userID, accountID)
	if errors.Is(err, connectedaccount.ErrNotFound) {
		// The account went with its labels since.
		return Label{}, nil, ErrNotFound
	}
	if err != nil {
		return Label{}, nil, err
	}
	for _, label := range labels {
		if label.ID == id {
			return label, labels, nil
		}
	}
	return Label{}, nil, ErrNotFound
}

// write takes a token for accountID and calls change with it within
// [providerBudget], then lists the account's mailboxes at the provider and
// saves them, whether or not change succeeded. It returns userID's labels
// of the account after, and change's error when it failed, else the
// error of listing or saving.
//
// DESIGN: the list after is saved even after a failure, so inbok shows
// whatever part of the change the provider kept, e.g. the labels a delete
// that stopped part way deleted, and a label the provider says is taken
// that inbok had not synced yet. A sync that read the mailboxes before
// this change and saves after it shows the old ones until the next sync,
// which is accepted.
func (s *Service) write(ctx context.Context, userID, accountID int64, change func(context.Context, connectedaccount.AccessToken) error) ([]Label, error) {
	budget, cancel := budgetOf(ctx)
	defer cancel()

	// 1. Take a token; an account its provider can't be reached for stops
	// here, changing nothing.
	token, err := s.access.Token(budget, accountID)
	if err != nil {
		return nil, busy(ctx, budget, err)
	}

	// 2. Change the mailboxes at the provider.
	changeErr := busy(ctx, budget, change(budget, token))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// 3. List the mailboxes after and save them, with ctx: the change is
	// at the provider now, whether or not the budget has run out.
	saveErr := s.save(ctx, accountID, token)
	if changeErr != nil {
		if saveErr != nil {
			s.logger.WarnContext(ctx, "label change not saved after failing", "account_id", accountID, "err", saveErr)
		}
		return nil, changeErr
	}
	if saveErr != nil {
		return nil, saveErr
	}
	return s.storage.ListLabels(ctx, userID, accountID)
}

// save lists accountID's mailboxes at the provider with token and saves
// them.
func (s *Service) save(ctx context.Context, accountID int64, token connectedaccount.AccessToken) error {
	mailboxes, err := s.provider.Mailboxes(ctx, token)
	if err != nil {
		return fmt.Errorf("list mailboxes after label change: %w", err)
	}
	return s.storage.SaveMailboxes(ctx, accountID, mailboxes)
}

// budgetOf returns ctx ending after [providerBudget], or earlier to keep
// [providerReserve] of ctx's own deadline.
func budgetOf(ctx context.Context) (context.Context, context.CancelFunc) {
	end := time.Now().Add(providerBudget)
	if deadline, ok := ctx.Deadline(); ok && deadline.Add(-providerReserve).Before(end) {
		end = deadline.Add(-providerReserve)
	}
	return context.WithDeadline(ctx, end)
}

// busy returns err, from a call made within budget, as wrapping
// [mailsync.ErrProviderBusy] when it failed because budget ran out while
// ctx had not: the provider took too long.
func busy(ctx, budget context.Context, err error) error {
	if err != nil && ctx.Err() == nil && budget.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", mailsync.ErrProviderBusy, err)
	}
	return err
}

// parentProviderID returns the provider ID of parent among labels, or ""
// for [uuid.Nil], and [ErrInvalidParent] when parent is none of labels, or
// is self or a label inside it.
func parentProviderID(labels []Label, parent, self uuid.UUID) (string, error) {
	if parent == uuid.Nil() {
		return "", nil
	}
	if parent == self || (self != uuid.Nil() && slices.Contains(inside(labels, self), parent)) {
		return "", ErrInvalidParent
	}
	for _, label := range labels {
		if label.ID == parent {
			return label.ProviderID, nil
		}
	}
	return "", ErrInvalidParent
}

// inside returns the IDs of the labels inside id among labels, at any
// depth, each before the labels inside it.
func inside(labels []Label, id uuid.UUID) []uuid.UUID {
	children := make(map[uuid.UUID][]uuid.UUID)
	for _, label := range labels {
		if label.Parent != uuid.Nil() {
			children[label.Parent] = append(children[label.Parent], label.ID)
		}
	}
	var found []uuid.UUID
	seen := map[uuid.UUID]bool{id: true}
	queue := []uuid.UUID{id}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, child := range children[next] {
			// A provider's names cannot nest in a loop, but a stored one
			// must not hang this.
			if seen[child] {
				continue
			}
			seen[child] = true
			found = append(found, child)
			queue = append(queue, child)
		}
	}
	return found
}
