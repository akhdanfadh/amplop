package conversation

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
)

var (
	inInbox   = Place{View: ViewInbox}
	inSpam    = Place{View: ViewSpam}
	inTrash   = Place{View: ViewTrash}
	labelWork = Place{Mailbox: uuid.MustParse("00000000-0000-4000-8000-0000000000f1")}
	idE       = uuid.MustParse("00000000-0000-4000-8000-00000000000e")
)

// A change must change something, name each place once, and only places
// a change reaches.
func TestValidateChange(t *testing.T) {
	for _, c := range []struct {
		name   string
		change Change
		want   error
	}{
		{"a place", Change{Add: []Place{inInbox}}, nil},
		{"a mark", Change{Starred: MarkSet}, nil},
		{"a label out, Trash in", Change{Add: []Place{inTrash}, Remove: []Place{labelWork}}, nil},
		{"nothing", Change{}, ErrEmptyChange},
		{"added and removed", Change{Add: []Place{inInbox}, Remove: []Place{inInbox}}, ErrPlaceTwice},
		{"added twice", Change{Add: []Place{inSpam, inSpam}}, ErrPlaceTwice},
		{"Sent", Change{Add: []Place{{View: ViewSent}}}, ErrUnchangeablePlace},
		{"Starred", Change{Remove: []Place{{View: ViewStarred}}}, ErrUnchangeablePlace},
		{"All mail", Change{Add: []Place{{View: ViewAll}}}, ErrUnchangeablePlace},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.ErrorIs(t, ValidateChange(c.change), c.want)
		})
	}
}

// Every action reaches what it changes: a move its places, a mark that
// mark; a new action fails here until it says.
func TestActionReach(t *testing.T) {
	for _, action := range Actions {
		assert.NotZero(t, action.Reach(), string(action))
	}
	assert.Equal(t, ReachPlaces, ActionTrash.Reach())
	assert.Equal(t, ReachStar, ActionUnstar.Reach())
	assert.Equal(t, ReachUnread, ActionRead.Reach())
}

// A change reaches what it names.
func TestChangeReach(t *testing.T) {
	assert.Equal(t, ReachPlaces, Change{Remove: []Place{labelWork}}.reach())
	assert.Equal(t, ReachStar|ReachUnread, Change{Starred: MarkSet, Unread: MarkCleared}.reach())
	assert.Equal(t, ReachPlaces|ReachUnread, Change{Add: []Place{inInbox}, Unread: MarkSet}.reach())
}

// An undo puts back only what changed within the change's reach: the
// places and marks of the row before, from the row after.
func TestInverse(t *testing.T) {
	unreadInInbox := Conversation{ID: idA, InInbox: true, InAllMail: true, Unread: true}
	readInInbox := Conversation{ID: idA, InInbox: true, InAllMail: true}
	// A thread with a message in the inbox and one deleted on its own.
	partlyTrashed := Conversation{ID: idA, InInbox: true, InTrash: true, InAllMail: true}
	for _, c := range []struct {
		name          string
		before, after Conversation
		reach         Reach
		want          Change
	}{
		{
			"archive",
			unreadInInbox,
			Conversation{ID: idA, InAllMail: true, Unread: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}},
		},
		{
			"Trash from the inbox",
			unreadInInbox,
			Conversation{ID: idA, InTrash: true, Unread: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}, Remove: []Place{inTrash}},
		},
		{
			"Trash from a label, archived",
			Conversation{ID: idA, InAllMail: true, LabelIDs: []uuid.UUID{labelWork.Mailbox}},
			Conversation{ID: idA, InTrash: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{labelWork}, Remove: []Place{inTrash}},
		},
		{
			"Delete in Spam",
			Conversation{ID: idA, InSpam: true},
			Conversation{ID: idA, InTrash: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inSpam}, Remove: []Place{inTrash}},
		},
		{
			"Delete with a message already in Trash",
			partlyTrashed,
			Conversation{ID: idA, InTrash: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}, Remove: []Place{inTrash}},
		},
		{
			"Report spam with a reply already in Spam",
			Conversation{ID: idA, InInbox: true, InSpam: true, InAllMail: true},
			Conversation{ID: idA, InSpam: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}, Remove: []Place{inSpam}},
		},
		{
			// Back to Trash, where the member moved it from.
			"Move to Inbox from Trash with a message outside it",
			partlyTrashed,
			Conversation{ID: idA, InInbox: true, InAllMail: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inTrash}},
		},
		{
			// The row reads its mark from the messages it shows: in Trash,
			// the unread one deleted earlier too.
			"Delete whose row reads unread once all in Trash",
			partlyTrashed,
			Conversation{ID: idA, InTrash: true, Unread: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}, Remove: []Place{inTrash}},
		},
		{
			"archive while starred elsewhere",
			readInInbox,
			Conversation{ID: idA, InAllMail: true, Flagged: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}},
		},
		{
			"archive while an unread reply comes",
			readInInbox,
			Conversation{ID: idA, InAllMail: true, Unread: true},
			ReachPlaces,
			Change{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}},
		},
		{
			"star",
			unreadInInbox,
			Conversation{ID: idA, InInbox: true, InAllMail: true, Unread: true, Flagged: true},
			ReachStar,
			Change{IDs: []uuid.UUID{idA}, Starred: MarkCleared},
		},
		{
			"star while archived elsewhere",
			readInInbox,
			Conversation{ID: idA, InAllMail: true, Flagged: true},
			ReachStar,
			Change{IDs: []uuid.UUID{idA}, Starred: MarkCleared},
		},
		{
			"read",
			unreadInInbox,
			readInInbox,
			ReachUnread,
			Change{IDs: []uuid.UUID{idA}, Unread: MarkSet},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := inverse(c.before, c.after, c.reach)
			require.True(t, ok)
			assert.Equal(t, c.want, got)
		})
	}

	_, ok := inverse(unreadInInbox, unreadInInbox, ReachPlaces|ReachStar|ReachUnread)
	assert.False(t, ok, "nothing changed")
	_, ok = inverse(readInInbox, Conversation{ID: idA, InInbox: true, InAllMail: true, Flagged: true}, ReachPlaces)
	assert.False(t, ok, "only a mark changed, outside a move's reach")
}

// Conversations needing the same undo share one change, in the order
// changed; one that did not change, or was not read before, needs none.
func TestUndoOf(t *testing.T) {
	inboxRow := func(id uuid.UUID) Conversation { return Conversation{ID: id, InInbox: true, InAllMail: true} }
	archived := func(id uuid.UUID) Conversation { return Conversation{ID: id, InAllMail: true} }
	trashed := func(id uuid.UUID) Conversation { return Conversation{ID: id, InTrash: true} }

	undo := undoOf(
		[]Conversation{inboxRow(idA), archived(idB), inboxRow(idC), trashed(idD)},
		[]Conversation{trashed(idA), trashed(idB), trashed(idC), trashed(idD), trashed(idE)},
		func(uuid.UUID) Reach { return ReachPlaces },
	)

	assert.Equal(t, []Change{
		{IDs: []uuid.UUID{idA, idC}, Add: []Place{inInbox}, Remove: []Place{inTrash}},
		{IDs: []uuid.UUID{idB}, Remove: []Place{inTrash}},
	}, undo)
}

// Changes reach each conversation's provider as its own account names
// their places, and the answer carries their undo.
func TestApplyChanges(t *testing.T) {
	storage := &fakeActionStorage{
		threads: []Thread{
			{ID: idA, AccountID: 1, ProviderThreadID: "ta"},
			{ID: idB, AccountID: 2, ProviderThreadID: "tb"},
		},
		mailboxes: []PlaceMailbox{
			{AccountID: 1, Place: inInbox, ProviderID: "INBOX"},
			{AccountID: 1, Place: labelWork, ProviderID: "Label_1"},
			{AccountID: 2, Place: inInbox, ProviderID: "INBOX-2"},
			{AccountID: 2, Place: inTrash, ProviderID: "TRASH-2"},
		},
		before: map[uuid.UUID]Conversation{idA: {ID: idA, InAllMail: true}},
		after:  map[uuid.UUID]Conversation{idA: {ID: idA, InInbox: true, InAllMail: true, LabelIDs: []uuid.UUID{labelWork.Mailbox}}},
	}
	provider := &fakeProvider{}
	actions := newTestActions(storage, fakeAccess{}, provider)

	applied, err := actions.ApplyChanges(t.Context(), 42, []Change{
		{IDs: []uuid.UUID{idA}, Add: []Place{inInbox, labelWork}, Starred: MarkSet},
		{IDs: []uuid.UUID{idB}, Add: []Place{inInbox}, Remove: []Place{inTrash}, Unread: MarkCleared},
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]Modification{
		"ta": {AddMailboxes: []string{"INBOX", "Label_1"}, RemoveMailboxes: []string{}, Starred: MarkSet},
		"tb": {AddMailboxes: []string{"INBOX-2"}, RemoveMailboxes: []string{"TRASH-2"}, Unread: MarkCleared},
	}, provider.modifications)
	assert.Empty(t, applied.Refused)
	assert.Equal(t, []Change{{IDs: []uuid.UUID{idA}, Remove: []Place{inInbox, labelWork}}}, applied.Undo)
}

// A change the provider kept only part of, e.g. its labels but not its
// newest message's mark, is written as the provider answered and reported
// changed, its undo putting back only that part.
func TestApplyChangesPartly(t *testing.T) {
	storage := &fakeActionStorage{
		threads:   []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}},
		mailboxes: []PlaceMailbox{{AccountID: 1, Place: inInbox, ProviderID: "INBOX"}},
		before:    map[uuid.UUID]Conversation{idA: {ID: idA, InAllMail: true}},
		after:     map[uuid.UUID]Conversation{idA: {ID: idA, InInbox: true, InAllMail: true}},
	}
	provider := &fakeProvider{
		errs:   map[string]error{"ta": mailsync.ErrProviderBusy},
		partly: map[string]bool{"ta": true},
	}
	actions := newTestActions(storage, fakeAccess{}, provider)

	applied, err := actions.ApplyChanges(t.Context(), 42, []Change{
		{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}, Unread: MarkSet},
	})

	require.NoError(t, err)
	assert.Equal(t, []mailsync.MessageUpdate{{ProviderID: "ta-m", Seen: true}}, storage.written[1])
	assert.Empty(t, applied.Refused)
	assert.Equal(t, []Conversation{storage.after[idA]}, applied.Conversations)
	assert.Equal(t, []Change{{IDs: []uuid.UUID{idA}, Remove: []Place{inInbox}}}, applied.Undo)
}

// Changes are refused whole, before any provider call, when they name a
// conversation twice, or a place not in a conversation's account.
func TestApplyChangesInvalid(t *testing.T) {
	storage := &fakeActionStorage{
		threads:   []Thread{{ID: idA, AccountID: 1, ProviderThreadID: "ta"}},
		mailboxes: []PlaceMailbox{{AccountID: 2, Place: labelWork, ProviderID: "Label_1"}},
	}
	provider := &fakeProvider{}
	actions := newTestActions(storage, fakeAccess{}, provider)

	for _, c := range []struct {
		name    string
		changes []Change
		want    error
	}{
		{"none", nil, ErrInvalidIDs},
		{"twice", []Change{{IDs: []uuid.UUID{idA}, Add: []Place{inInbox}}, {IDs: []uuid.UUID{idA}, Starred: MarkSet}}, ErrInvalidIDs},
		{"empty", []Change{{IDs: []uuid.UUID{idA}}}, ErrEmptyChange},
		{"another account's label", []Change{{IDs: []uuid.UUID{idA}, Add: []Place{labelWork}}}, ErrPlaceNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := actions.ApplyChanges(t.Context(), 42, c.changes)
			assert.ErrorIs(t, err, c.want)
		})
	}
	assert.Empty(t, provider.calls)
}
