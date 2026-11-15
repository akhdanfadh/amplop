package conversation

import (
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	inInbox   = Place{View: ViewInbox}
	inSpam    = Place{View: ViewSpam}
	inTrash   = Place{View: ViewTrash}
	labelWork = Place{Mailbox: uuid.MustParse("00000000-0000-4000-8000-0000000000f1")}
	idE       = uuid.MustParse("00000000-0000-4000-8000-00000000000e")
)

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
