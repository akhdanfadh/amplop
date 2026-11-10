package google

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
)

const (
	// historyPageSize is Gmail's largest history.list page: a page costs
	// 2 units however long it is (Gmail's usage limits).
	historyPageSize = 500
	// historyFields are the parts of history.list's answer
	// [Client.Changes] reads.
	historyFields = "history(messagesAdded/message/id,messagesDeleted/message/id," +
		"labelsAdded(message/id,labelIds),labelsRemoved(message/id,labelIds)),historyId,nextPageToken"
)

// errUnreadablePosition means a position is not a history ID
// [Client.Position] wrote.
var errUnreadablePosition = errors.New("position is not a gmail history id")

// Changes returns one page of the account's changes since position, a history
// ID, from history.list: a message deleted is deleted, whatever else happened
// to it; one added is fetched; and one relabelled is updated, each label it
// changed at its last change, UNREAD and STARRED as its flags. It returns
// an error wrapping [mailsync.ErrPositionExpired] when position is not a
// history ID, or when Gmail no longer keeps history that old, which it answers
// with 404 (Gmail's "Synchronize clients with Gmail" guide).
func (c *Client) Changes(ctx context.Context, token connectedaccount.AccessToken, position string, pageToken string) (mailsync.ChangePage, error) {
	historyID, err := strconv.ParseUint(position, 10, 64)
	if err != nil {
		// Only a full sync replaces a position Gmail cannot read; the error
		// leaves the text out.
		return mailsync.ChangePage{}, fmt.Errorf("%w: %w", mailsync.ErrPositionExpired, errUnreadablePosition)
	}
	service, err := c.gmail(ctx, token)
	if err != nil {
		return mailsync.ChangePage{}, err
	}
	call := service.Users.History.List("me").
		StartHistoryId(historyID).
		MaxResults(historyPageSize).
		Fields(historyFields)
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	list, err := retry(ctx, c, func() (*gmail.ListHistoryResponse, error) {
		return call.Context(ctx).Do()
	})
	if isNotFound(err) {
		return mailsync.ChangePage{}, fmt.Errorf("%w: %w", mailsync.ErrPositionExpired, redactGmail(err))
	}
	if err != nil {
		return mailsync.ChangePage{}, redactGmail(err)
	}

	page := mailsync.ChangePage{Position: formatHistoryID(list.HistoryId), NextPageToken: list.NextPageToken}

	// 1. Collect every record in turn: Gmail returns them oldest first
	// (history.list reference), so a label's last change in the page is
	// where the message stands.
	var changes pageChanges
	for i, record := range list.History {
		if record == nil {
			continue
		}
		for _, m := range record.MessagesAdded {
			if m != nil {
				changes.added(m.Message)
			}
		}
		for _, m := range record.LabelsAdded {
			if m != nil {
				changes.relabelled(m.Message, m.LabelIds, labelEvent{change: labelAdded, record: i})
			}
		}
		for _, m := range record.LabelsRemoved {
			if m != nil {
				changes.relabelled(m.Message, m.LabelIds, labelEvent{change: labelRemoved, record: i})
			}
		}
		for _, m := range record.MessagesDeleted {
			if m != nil {
				changes.deleted(m.Message)
			}
		}
	}

	// 2. Only then sort each message: a later record may delete one an
	// earlier record added or relabelled.
	changes.fill(&page)
	return page, nil
}

// labelChange is whether a history record added a label to a message or
// removed it.
type labelChange int

const (
	labelAdded labelChange = iota + 1
	labelRemoved
)

// labelEvent is one change of a label on a message, and the index of the
// history record that made it.
type labelEvent struct {
	change labelChange
	record int
}

// pageChanges is what one history page did to each message it names, in
// the order the messages first appear.
type pageChanges struct {
	ids  []string
	byID map[string]*messageHistory
}

// messageHistory is what one history page did to one message.
type messageHistory struct {
	added   bool
	deleted bool
	// unclear means a record both added and removed one label, so where
	// the message stands is not known from the page.
	unclear bool
	// labels are the labels the page changed, in the order first changed,
	// and last their latest change.
	labels []string
	last   map[string]labelEvent
}

// added records that m was added.
func (p *pageChanges) added(m *gmail.Message) {
	if h := p.message(m); h != nil {
		h.added = true
	}
}

// deleted records that m was deleted.
func (p *pageChanges) deleted(m *gmail.Message) {
	if h := p.message(m); h != nil {
		h.deleted = true
	}
}

// relabelled records event for each of m's labelIDs, replacing an
// earlier change of the label.
func (p *pageChanges) relabelled(m *gmail.Message, labelIDs []string, event labelEvent) {
	h := p.message(m)
	if h == nil {
		return
	}
	for _, label := range labelIDs {
		if label == "" {
			continue
		}
		earlier, ok := h.last[label]
		if !ok {
			h.labels = append(h.labels, label)
		}
		// NOTE: Gmail documents no order between one record's labelsAdded
		// and labelsRemoved, so a label in both leaves the message's state
		// unknown; fetching it whole finds out.
		if ok && earlier.record == event.record && earlier.change != event.change {
			h.unclear = true
		}
		h.last[label] = event
	}
}

// message returns the history of m, starting one on first sight; nil when
// m has no ID.
func (p *pageChanges) message(m *gmail.Message) *messageHistory {
	if m == nil || m.Id == "" {
		return nil
	}
	if h, ok := p.byID[m.Id]; ok {
		return h
	}
	if p.byID == nil {
		p.byID = make(map[string]*messageHistory)
	}
	h := &messageHistory{last: make(map[string]labelEvent)}
	p.byID[m.Id] = h
	p.ids = append(p.ids, m.Id)
	return h
}

// fill sorts each message into page: deleted whatever else happened to
// it, fetched whole when added or unclear, and otherwise updated, unless
// nothing changed.
func (p *pageChanges) fill(page *mailsync.ChangePage) {
	for _, id := range p.ids {
		h := p.byID[id]
		switch {
		case h.deleted:
			page.Deleted = append(page.Deleted, id)
		case h.added || h.unclear:
			page.Fetch = append(page.Fetch, id)
		default:
			if update, ok := h.update(id); ok {
				page.Update = append(page.Update, update)
			}
		}
	}
}

// update returns message id's net change, with UNREAD and STARRED as its
// flags and every other label as a mailbox. It reports false when nothing
// changed.
func (h *messageHistory) update(id string) (mailsync.MessageUpdate, bool) {
	update := mailsync.MessageUpdate{ProviderID: id}
	for _, label := range h.labels {
		on := h.last[label].change == labelAdded
		switch {
		case label == labelUnread:
			seen := !on
			update.Seen = &seen
		case label == labelStarred:
			update.Flagged = &on
		case on:
			update.MailboxesAdded = append(update.MailboxesAdded, label)
		default:
			update.MailboxesRemoved = append(update.MailboxesRemoved, label)
		}
	}
	changed := update.Seen != nil || update.Flagged != nil ||
		len(update.MailboxesAdded) > 0 || len(update.MailboxesRemoved) > 0
	return update, changed
}

// formatHistoryID returns historyID as a position: Gmail's discovery
// document types it as an unsigned 64-bit integer, sent as decimal text.
func formatHistoryID(historyID uint64) string {
	return strconv.FormatUint(historyID, 10)
}
