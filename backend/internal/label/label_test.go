package label

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

// The member's account 1 has Work, Work/Projects, and Home; account 2 is
// another of theirs, with Taxes.
const (
	member  = int64(42)
	account = int64(1)
	other   = int64(2)
)

var (
	idWork     = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	idProjects = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	idHome     = uuid.MustParse("00000000-0000-4000-8000-000000000003")
	idTaxes    = uuid.MustParse("00000000-0000-4000-8000-000000000004")
)

// newTestService returns a service over storage and provider, whose
// access hands out a token for any account.
func newTestService(storage *fakeStorage, provider *fakeProvider) *Service {
	return NewService(storage, fakeAccess{}, provider, slog.New(slog.DiscardHandler))
}

// newWorld returns storage and a provider that both hold the member's
// labels as described above.
func newWorld() (*fakeStorage, *fakeProvider) {
	storage := &fakeStorage{
		ids: map[string]uuid.UUID{"work": idWork, "projects": idProjects, "home": idHome, "taxes": idTaxes},
		labels: map[int64][]Label{
			account: {
				{ID: idWork, AccountID: account, ProviderID: "work", Name: "Work"},
				{ID: idProjects, AccountID: account, ProviderID: "projects", Name: "Projects", Parent: idWork},
				{ID: idHome, AccountID: account, ProviderID: "home", Name: "Home"},
			},
			other: {{ID: idTaxes, AccountID: other, ProviderID: "taxes", Name: "Taxes"}},
		},
	}
	provider := &fakeProvider{mailboxes: []mailsync.Mailbox{
		{ProviderID: "INBOX", Name: "INBOX", Role: mailsync.RoleInbox, MadeBy: mailsync.MadeByProvider},
		{ProviderID: "work", Name: "Work", MadeBy: mailsync.MadeByMember},
		{ProviderID: "projects", Name: "Projects", ProviderParentID: "work", MadeBy: mailsync.MadeByMember},
		{ProviderID: "home", Name: "Home", MadeBy: mailsync.MadeByMember},
	}}
	return storage, provider
}

// A created label is made under its parent's mailbox, then returned as the
// provider lists it after, with the ID inbok saved it under.
func TestCreate(t *testing.T) {
	storage, provider := newWorld()

	created, err := newTestService(storage, provider).Create(t.Context(), member, account, "Q4", idProjects)

	require.NoError(t, err)
	assert.Equal(t, []string{"create projects/Q4", "list"}, provider.calls)
	assert.NotEqual(t, uuid.Nil(), created.ID)
	assert.Equal(t, Label{ID: created.ID, AccountID: account, ProviderID: "new-1", Name: "Q4", Parent: idProjects}, created)
	assert.Contains(t, storage.labels[account], created, "saved as the provider listed it")
}

// A label at the top has no parent mailbox.
func TestCreateAtTop(t *testing.T) {
	storage, provider := newWorld()

	created, err := newTestService(storage, provider).Create(t.Context(), member, account, "Receipts", uuid.Nil())

	require.NoError(t, err)
	assert.Equal(t, []string{"create /Receipts", "list"}, provider.calls)
	assert.Equal(t, uuid.Nil(), created.Parent)
}

// A name or parent the service can tell is wrong is refused before any
// provider call.
func TestCreateRefused(t *testing.T) {
	for name, c := range map[string]struct {
		account int64
		name    string
		parent  uuid.UUID
		want    error
	}{
		"blank name":           {account, " \t", uuid.Nil(), ErrNameBlank},
		"name too long":        {account, strings.Repeat("é", MaxNameLength+1), uuid.Nil(), ErrNameTooLong},
		"another account's":    {account, "Q4", idTaxes, ErrInvalidParent},
		"no such parent":       {account, "Q4", uuid.New(), ErrInvalidParent},
		"another member's":     {99, "Q4", uuid.Nil(), connectedaccount.ErrNotFound},
		"name at the limit ok": {account, strings.Repeat("é", MaxNameLength), uuid.Nil(), nil},
	} {
		t.Run(name, func(t *testing.T) {
			storage, provider := newWorld()

			_, err := newTestService(storage, provider).Create(t.Context(), member, c.account, c.name, c.parent)

			if c.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, c.want)
			assert.Empty(t, provider.calls)
		})
	}
}

// A provider's refusal is returned as it is, after saving what the
// provider lists, so a label inbok had not synced shows.
func TestCreateProviderRefused(t *testing.T) {
	storage, provider := newWorld()
	provider.createErr = ErrNameTaken
	provider.mailboxes = append(provider.mailboxes, mailsync.Mailbox{ProviderID: "receipts", Name: "Receipts", MadeBy: mailsync.MadeByMember})

	_, err := newTestService(storage, provider).Create(t.Context(), member, account, "receipts", uuid.Nil())

	require.ErrorIs(t, err, ErrNameTaken)
	assert.Contains(t, storage.names(account), "Receipts")
}

// Editing names the label, moves it under its new parent's mailbox, and
// returns it as the provider lists it after.
func TestEdit(t *testing.T) {
	storage, provider := newWorld()

	edited, err := newTestService(storage, provider).Edit(t.Context(), member, idProjects, "Plans", idHome)

	require.NoError(t, err)
	assert.Equal(t, []string{"edit projects home/Plans", "list"}, provider.calls)
	assert.Equal(t, Label{ID: idProjects, AccountID: account, ProviderID: "projects", Name: "Plans", Parent: idHome}, edited)
}

// A label can't nest under itself or a label inside it, nor under another
// account's; a label not the member's is not found. Each is refused
// before any provider call.
func TestEditRefused(t *testing.T) {
	for name, c := range map[string]struct {
		id     uuid.UUID
		parent uuid.UUID
		want   error
	}{
		"under itself":         {idWork, idWork, ErrInvalidParent},
		"under one inside it":  {idWork, idProjects, ErrInvalidParent},
		"under another's":      {idWork, idTaxes, ErrInvalidParent},
		"no such label":        {uuid.New(), uuid.Nil(), ErrNotFound},
		"to the top is fine":   {idProjects, uuid.Nil(), nil},
		"under a sibling fine": {idHome, idWork, nil},
	} {
		t.Run(name, func(t *testing.T) {
			storage, provider := newWorld()

			_, err := newTestService(storage, provider).Edit(t.Context(), member, c.id, "Name", c.parent)

			if c.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, c.want)
			assert.Empty(t, provider.calls)
		})
	}
}

// Deleting returns the IDs of the label and every label inside it that
// inbok no longer has after.
func TestDelete(t *testing.T) {
	storage, provider := newWorld()

	deleted, err := newTestService(storage, provider).Delete(t.Context(), member, idWork)

	require.NoError(t, err)
	assert.Equal(t, []string{"delete work", "list"}, provider.calls)
	assert.ElementsMatch(t, []uuid.UUID{idWork, idProjects}, deleted)
	assert.Equal(t, []string{"Home"}, storage.names(account))
}

// A delete that stopped part way returns its error, after saving what the
// provider kept, so the deleted labels leave inbok and asking again
// finishes it.
func TestDeletePartly(t *testing.T) {
	storage, provider := newWorld()
	provider.deleteStops = "work"

	_, err := newTestService(storage, provider).Delete(t.Context(), member, idWork)

	require.ErrorIs(t, err, errProviderDown)
	assert.ElementsMatch(t, []string{"Work", "Home"}, storage.names(account))

	provider.deleteStops = ""
	deleted, err := newTestService(storage, provider).Delete(t.Context(), member, idWork)

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{idWork}, deleted)
}

// An account its provider can't be reached for changes nothing: the
// token's error is returned before any provider call.
func TestWriteTokenRefused(t *testing.T) {
	storage, provider := newWorld()
	service := NewService(storage, fakeAccess{err: connectedaccount.ErrNeedsReconnect}, provider, slog.New(slog.DiscardHandler))

	_, err := service.Create(t.Context(), member, account, "Q4", uuid.Nil())

	require.ErrorIs(t, err, connectedaccount.ErrNeedsReconnect)
	assert.Empty(t, provider.calls)
	assert.Zero(t, storage.saves)
}

// A provider slower than the budget is busy; the mailboxes are still
// listed and saved after, with the caller's time.
func TestWriteBudget(t *testing.T) {
	budget := providerBudget
	providerBudget = 10 * time.Millisecond
	t.Cleanup(func() { providerBudget = budget })
	storage, provider := newWorld()
	provider.slow = true

	_, err := newTestService(storage, provider).Edit(t.Context(), member, idHome, "House", uuid.Nil())

	require.ErrorIs(t, err, mailsync.ErrProviderBusy)
	assert.Equal(t, 1, storage.saves)
}

// A label edited at the provider but gone from the list after is not
// found.
func TestEditGoneAfter(t *testing.T) {
	storage, provider := newWorld()
	provider.dropAfter = "home"

	_, err := newTestService(storage, provider).Edit(t.Context(), member, idHome, "House", uuid.Nil())

	require.ErrorIs(t, err, ErrNotFound)
}

// A refusal of the change is what the caller hears, even when the list
// after it fails too; nothing is saved then.
func TestWriteChangeErrorWins(t *testing.T) {
	storage, provider := newWorld()
	provider.createErr = ErrNameTaken
	provider.listErr = errProviderDown

	_, err := newTestService(storage, provider).Create(t.Context(), member, account, "Home", uuid.Nil())

	require.ErrorIs(t, err, ErrNameTaken)
	assert.Zero(t, storage.saves)
}

// A change the provider made, but whose list or save after fails, returns
// that failure rather than a label inbok did not save.
func TestWriteSaveFails(t *testing.T) {
	databaseDown := errors.New("database down")
	for name, c := range map[string]struct {
		listErr, saveErr, want error
	}{
		"list fails": {listErr: errProviderDown, want: errProviderDown},
		"save fails": {saveErr: databaseDown, want: databaseDown},
	} {
		t.Run(name, func(t *testing.T) {
			storage, provider := newWorld()
			provider.listErr = c.listErr
			storage.saveErr = c.saveErr

			_, err := newTestService(storage, provider).Create(t.Context(), member, account, "Q4", uuid.Nil())

			require.ErrorIs(t, err, c.want)
			assert.Contains(t, provider.calls, "create /Q4", "the change was made")
		})
	}
}

// errProviderDown is a provider failure no caller compares.
var errProviderDown = errors.New("provider down")

type fakeAccess struct {
	err error
}

func (a fakeAccess) Token(_ context.Context, _ int64) (connectedaccount.AccessToken, error) {
	if a.err != nil {
		return connectedaccount.AccessToken{}, a.err
	}
	return connectedaccount.NewAccessToken("token"), nil
}

// fakeStorage keeps each account's labels, and gives a mailbox the ID it
// had, else a new one, as Postgres does.
type fakeStorage struct {
	labels map[int64][]Label
	ids    map[string]uuid.UUID
	saves  int
	// saveErr fails every save.
	saveErr error
}

func (s *fakeStorage) ReadLabelAccount(_ context.Context, userID int64, id uuid.UUID) (int64, error) {
	if userID != member {
		return 0, ErrNotFound
	}
	for accountID, labels := range s.labels {
		for _, label := range labels {
			if label.ID == id {
				return accountID, nil
			}
		}
	}
	return 0, ErrNotFound
}

func (s *fakeStorage) ListLabels(_ context.Context, userID, accountID int64) ([]Label, error) {
	labels, ok := s.labels[accountID]
	if userID != member || !ok {
		return nil, connectedaccount.ErrNotFound
	}
	return labels, nil
}

func (s *fakeStorage) SaveMailboxes(_ context.Context, accountID int64, mailboxes []mailsync.Mailbox) error {
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	byProvider := make(map[string]uuid.UUID)
	for _, mailbox := range mailboxes {
		if _, ok := s.ids[mailbox.ProviderID]; !ok {
			s.ids[mailbox.ProviderID] = uuid.New()
		}
		byProvider[mailbox.ProviderID] = s.ids[mailbox.ProviderID]
	}
	var labels []Label
	for _, mailbox := range mailboxes {
		if mailbox.Role != mailsync.RoleNone || mailbox.MadeBy != mailsync.MadeByMember {
			continue
		}
		labels = append(labels, Label{
			ID:         byProvider[mailbox.ProviderID],
			AccountID:  accountID,
			ProviderID: mailbox.ProviderID,
			Name:       mailbox.Name,
			Parent:     byProvider[mailbox.ProviderParentID],
		})
	}
	s.labels[accountID] = labels
	return nil
}

// names returns the names of accountID's labels.
func (s *fakeStorage) names(accountID int64) []string {
	var names []string
	for _, label := range s.labels[accountID] {
		names = append(names, label.Name)
	}
	return names
}

// fakeProvider keeps one account's mailboxes and changes them as asked,
// recording each call.
type fakeProvider struct {
	mailboxes []mailsync.Mailbox
	calls     []string
	created   int
	createErr error
	// deleteStops makes a delete fail when it reaches this mailbox, after
	// deleting those inside it.
	deleteStops string
	// slow makes a change wait until its context ends.
	slow bool
	// dropAfter leaves this mailbox out of the lists after a change.
	dropAfter string
	// listErr fails every list.
	listErr error
}

func (p *fakeProvider) Mailboxes(_ context.Context, _ connectedaccount.AccessToken) ([]mailsync.Mailbox, error) {
	p.calls = append(p.calls, "list")
	if p.listErr != nil {
		return nil, p.listErr
	}
	var listed []mailsync.Mailbox
	for _, mailbox := range p.mailboxes {
		if mailbox.ProviderID != p.dropAfter {
			listed = append(listed, mailbox)
		}
	}
	return listed, nil
}

func (p *fakeProvider) CreateMailbox(_ context.Context, _ connectedaccount.AccessToken, parentID, name string) (string, error) {
	p.calls = append(p.calls, "create "+parentID+"/"+name)
	if p.createErr != nil {
		return "", p.createErr
	}
	p.created++
	id := "new-" + string(rune('0'+p.created))
	p.mailboxes = append(p.mailboxes, mailsync.Mailbox{ProviderID: id, Name: name, ProviderParentID: parentID, MadeBy: mailsync.MadeByMember})
	return id, nil
}

func (p *fakeProvider) EditMailbox(ctx context.Context, _ connectedaccount.AccessToken, id, parentID, name string) error {
	p.calls = append(p.calls, "edit "+id+" "+parentID+"/"+name)
	if p.slow {
		<-ctx.Done()
		return ctx.Err()
	}
	for i := range p.mailboxes {
		if p.mailboxes[i].ProviderID == id {
			p.mailboxes[i].Name = name
			p.mailboxes[i].ProviderParentID = parentID
			return nil
		}
	}
	return ErrNotFound
}

func (p *fakeProvider) DeleteMailbox(_ context.Context, _ connectedaccount.AccessToken, id string) error {
	p.calls = append(p.calls, "delete "+id)
	// Delete the mailboxes inside first, then id itself.
	gone := map[string]bool{}
	var walk func(parent string)
	walk = func(parent string) {
		for _, mailbox := range p.mailboxes {
			if mailbox.ProviderParentID == parent {
				walk(mailbox.ProviderID)
				gone[mailbox.ProviderID] = true
			}
		}
	}
	walk(id)
	if p.deleteStops != id {
		gone[id] = true
	}
	var kept []mailsync.Mailbox
	for _, mailbox := range p.mailboxes {
		if !gone[mailbox.ProviderID] {
			kept = append(kept, mailbox)
		}
	}
	p.mailboxes = kept
	if p.deleteStops == id {
		return errProviderDown
	}
	return nil
}
