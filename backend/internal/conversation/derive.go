// Package conversation holds inbok's conversations: each account's messages
// that share the provider's thread, as one row of the unified inbox
// (ADR031). [Derive] applies the rules that make a thread's row and places,
// which storage keeps derived for every thread a sync write touches.
//
// DESIGN: The rules (ADR001, "Conversations"): a draft shows nowhere, which
// inbok cannot open yet; a message in Spam or Trash counts only there. A
// conversation's row is made from its visible messages, the others, or when
// it has none, from its Spam and Trash ones. One row per conversation, the
// same in every place, is what the web app's data layer keeps (ADR031), so a
// row's date is its newest visible message's in every place, as in Gmail's
// conversation view.
package conversation

import (
	"cmp"
	"slices"
	"strings"
	"time"
	"uuid"

	"inbok/internal/mailsync"
)

// maxParticipants bounds the senders a row keeps: it shows a few names, and
// a long thread's would grow without end. inbok's choice.
const maxParticipants = 10

// Mailbox is a mailbox a message is in, as the rules read it.
type Mailbox struct {
	ID   uuid.UUID
	Role mailsync.Role
}

// Message is one message of a thread, as the rules read it.
type Message struct {
	ID uuid.UUID
	// Date is the provider's own timestamp, [mailsync.Message.InternalDate].
	Date    time.Time
	Subject string
	Preview string
	From    []mailsync.Address
	Seen    bool
	Flagged bool
	// Mailboxes are the mailboxes it is in, perhaps none.
	Mailboxes []Mailbox
}

// Summary is a conversation's row and places, derived from its thread's
// messages.
type Summary struct {
	// LatestDate is the newest message's date, which lists sort by.
	LatestDate time.Time
	// Subject is the oldest message's, as sent.
	Subject string
	// Preview and LatestFrom are the newest message's.
	Preview    string
	LatestFrom []mailsync.Address
	// Participants are the senders in date order, each address once, at
	// most maxParticipants.
	Participants []mailsync.Address
	MessageCount int
	// Unread is whether a message is not seen; Flagged whether one is
	// flagged.
	Unread  bool
	Flagged bool
	// InAllMail is whether the thread has a visible message, which All mail
	// and Starred list.
	InAllMail bool
	// Mailboxes are the IDs of the mailboxes the conversation is in, in ID
	// order: those holding a visible message of it, and Spam and Trash when
	// they hold any.
	Mailboxes []uuid.UUID
}

// Derive returns the conversation of a thread's messages, in any order, and
// false when the thread has none to show, e.g. only drafts.
func Derive(messages []Message) (Summary, bool) {
	// 1. Leave out drafts, and find the visible messages among the rest.
	var kept, visible []Message
	for _, m := range messages {
		if m.has(mailsync.RoleDrafts) {
			continue
		}
		kept = append(kept, m)
		if !m.hidden() {
			visible = append(visible, m)
		}
	}
	if len(kept) == 0 {
		return Summary{}, false
	}

	// 2. Make the row from the visible messages, or with none, from the
	// Spam and Trash ones, oldest first, as lists order rows.
	shown := visible
	if len(shown) == 0 {
		shown = kept
	}
	shown = slices.SortedFunc(slices.Values(shown), func(a, b Message) int {
		return cmp.Or(a.Date.Compare(b.Date), a.ID.Compare(b.ID))
	})
	oldest, newest := shown[0], shown[len(shown)-1]
	summary := Summary{
		LatestDate:   newest.Date,
		Subject:      oldest.Subject,
		Preview:      newest.Preview,
		LatestFrom:   orNone(newest.From),
		Participants: participants(shown),
		MessageCount: len(shown),
		InAllMail:    len(visible) > 0,
		Mailboxes:    places(kept),
	}
	for _, m := range shown {
		summary.Unread = summary.Unread || !m.Seen
		summary.Flagged = summary.Flagged || m.Flagged
	}
	return summary, true
}

// participants returns the senders of messages, oldest first, each once:
// by address, or by name for an entry that is no address, e.g. a group.
func participants(messages []Message) []mailsync.Address {
	seen := map[senderKey]bool{}
	found := []mailsync.Address{}
	for _, m := range messages {
		for _, a := range m.From {
			key := keyOf(a)
			if seen[key] || len(found) == maxParticipants {
				continue
			}
			seen[key] = true
			found = append(found, a)
		}
	}
	return found
}

// senderKey tells senders apart: by address, any case, or by name for an
// entry with no address. One is always empty, so a name never matches an
// address, however either reads.
type senderKey struct {
	address string
	name    string
}

// keyOf returns a's key.
func keyOf(a mailsync.Address) senderKey {
	if a.Address == "" {
		return senderKey{name: a.Name}
	}
	return senderKey{address: strings.ToLower(a.Address)}
}

// places returns the IDs of the mailboxes messages put their conversation
// in, in ID order: every mailbox of a visible message, and only Spam and
// Trash for a hidden one.
func places(messages []Message) []uuid.UUID {
	ids := []uuid.UUID{}
	for _, m := range messages {
		hidden := m.hidden()
		for _, mailbox := range m.Mailboxes {
			if hidden && !isHiding(mailbox.Role) {
				continue
			}
			ids = append(ids, mailbox.ID)
		}
	}
	slices.SortFunc(ids, uuid.UUID.Compare)
	return slices.Compact(ids)
}

// has reports whether m is in a mailbox of role.
func (m Message) has(role mailsync.Role) bool {
	return slices.ContainsFunc(m.Mailboxes, func(mailbox Mailbox) bool { return mailbox.Role == role })
}

// hidden reports whether m is in Spam or Trash, which it then counts only
// in.
func (m Message) hidden() bool {
	return slices.ContainsFunc(m.Mailboxes, func(mailbox Mailbox) bool { return isHiding(mailbox.Role) })
}

// isHiding reports whether role is Spam's or Trash's.
func isHiding(role mailsync.Role) bool {
	return role == mailsync.RoleJunk || role == mailsync.RoleTrash
}

// orNone returns addresses, or an empty list for nil.
func orNone(addresses []mailsync.Address) []mailsync.Address {
	if addresses == nil {
		return []mailsync.Address{}
	}
	return addresses
}
