package conversation

import (
	"slices"
	"uuid"
)

// ========================
// Changes
// ========================

// Mark is what a [Change] does to a conversation's star or unread mark.
type Mark string

const (
	// MarkKept leaves the mark as it is.
	MarkKept Mark = ""
	// MarkSet sets it on the conversation's newest message, as
	// [Action.OnNewest] does.
	MarkSet Mark = "set"
	// MarkCleared clears it from every message.
	MarkCleared Mark = "cleared"
)

// Change is a change of conversations as it is, by none of their
// provider's own rules: the places it adds them to and takes them out of,
// each the inbox, Spam, Trash, or one of the member's labels, and what it
// does to their star and unread marks. An action's [Applied.Undo] is one.
type Change struct {
	IDs     []uuid.UUID
	Add     []Place
	Remove  []Place
	Starred Mark
	Unread  Mark
}

// Reach is what an action or a [Change] may change of a conversation:
// its places, its star, its unread mark, or several of them. Its undo puts
// back only that, so a change made meanwhile by another client, or by a
// sync, is not taken back with it.
type Reach uint8

const (
	// ReachPlaces is the conversation's places: the inbox, Spam, Trash,
	// and its labels.
	ReachPlaces Reach = 1 << iota
	// ReachStar is its star.
	ReachStar
	// ReachUnread is its unread mark.
	ReachUnread
)

// Reach returns what a may change of a conversation: an action that moves
// it, its places; one that marks it, that mark.
func (a Action) Reach() Reach {
	switch a {
	case ActionArchive, ActionUnarchive, ActionTrash, ActionUntrash, ActionSpam, ActionUnspam:
		return ReachPlaces
	case ActionStar, ActionUnstar:
		return ReachStar
	case ActionRead, ActionUnread:
		return ReachUnread
	}
	return 0
}

// changeViews are the views a [Change] can add to or take out of; the
// others follow from them and the marks.
var changeViews = []View{ViewInbox, ViewSpam, ViewTrash}

// ========================
// Undo
// ========================

// undoOf returns the changes that put each conversation of after back as
// it stands in before, within what reach says the change of it reached,
// conversations needing the same change together, in the order of after.
// A conversation that is as before there, or missing from before, needs
// none.
func undoOf(before, after []Conversation, reach func(uuid.UUID) Reach) []Change {
	var undo []Change
	for _, row := range after {
		i := slices.IndexFunc(before, func(c Conversation) bool { return c.ID == row.ID })
		if i < 0 {
			continue
		}
		change, ok := inverse(before[i], row, reach(row.ID))
		if !ok {
			continue
		}
		same := slices.IndexFunc(undo, func(c Change) bool { return sameChange(c, change) })
		if same < 0 {
			undo = append(undo, change)
			continue
		}
		undo[same].IDs = append(undo[same].IDs, row.ID)
	}
	return undo
}

// inverse returns the change that takes the conversation after back to
// before in what reach covers, and false when they do not differ there:
// in the places a [Change] reaches, and in their star and unread marks.
//
// DESIGN: by the row, not each message: putting a message back exactly
// would take Gmail a call per message, beyond an action's budget for 100
// conversations, while the row is what inbok and the provider's own list
// show. So undoing Trash also puts the member's own replies in the inbox
// (ADR031, "Mail actions as built", checkpoint 7c; DEBT.md, "Undo per
// message").
func inverse(before, after Conversation, reach Reach) (Change, bool) {
	change := Change{IDs: []uuid.UUID{after.ID}}
	if reach&ReachPlaces != 0 {
		was, is := changePlaces(before), changePlaces(after)
		// Only some of its messages were in Trash or Spam, and the change
		// put all of them there. The undo reaches every message, so it
		// cannot leave those few behind: it takes them all out, back to
		// where the change took the conversation from, rather than leave
		// it hidden where it was not.
		if before.InAllMail && !after.InAllMail {
			was = slices.DeleteFunc(was, func(p Place) bool { return p.View == ViewTrash || p.View == ViewSpam })
		}
		change.Add = placesNotIn(was, is)
		change.Remove = placesNotIn(is, was)
	}
	// A mark only when the change reached it: a move can change the row's
	// marks without changing a message's, as the row reads its marks from
	// the messages it shows.
	if reach&ReachStar != 0 && before.Flagged != after.Flagged {
		change.Starred = markOf(before.Flagged)
	}
	if reach&ReachUnread != 0 && before.Unread != after.Unread {
		change.Unread = markOf(before.Unread)
	}
	changed := len(change.Add) > 0 || len(change.Remove) > 0 ||
		change.Starred != MarkKept || change.Unread != MarkKept
	return change, changed
}

// changePlaces returns the places of c a [Change] reaches: its views in
// [changeViews]' order, then its labels in theirs, so two conversations'
// alike changes compare equal.
func changePlaces(c Conversation) []Place {
	in := map[View]bool{ViewInbox: c.InInbox, ViewSpam: c.InSpam, ViewTrash: c.InTrash}
	var places []Place
	for _, view := range changeViews {
		if in[view] {
			places = append(places, Place{View: view})
		}
	}
	for _, label := range c.LabelIDs {
		places = append(places, Place{Mailbox: label})
	}
	return places
}

// placesNotIn returns the places of from that other lacks, in from's order.
func placesNotIn(from, other []Place) []Place {
	var places []Place
	for _, place := range from {
		if !slices.Contains(other, place) {
			places = append(places, place)
		}
	}
	return places
}

// markOf returns the mark that sets a mark when set, or clears it.
func markOf(set bool) Mark {
	if set {
		return MarkSet
	}
	return MarkCleared
}

// sameChange reports whether a and b change conversations alike, whatever
// their IDs.
func sameChange(a, b Change) bool {
	return slices.Equal(a.Add, b.Add) && slices.Equal(a.Remove, b.Remove) &&
		a.Starred == b.Starred && a.Unread == b.Unread
}
