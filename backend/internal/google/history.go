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
	// historyFields are the parts of history.list's answer [Client.Changes]
	// reads: a label record's message carries its whole label set, and the
	// record's ID is the version of that set.
	historyFields = "history(id,messagesAdded/message/id,messagesDeleted/message/id," +
		"labelsAdded/message(id,labelIds),labelsRemoved/message(id,labelIds)),historyId,nextPageToken"
)

// errUnreadablePosition means a position is not a history ID
// [Client.Position] wrote.
var errUnreadablePosition = errors.New("position is not a gmail history id")

// Changes returns one page of the account's changes since position, a history
// ID, from history.list: a message deleted is deleted, whatever else happened
// to it; one added is fetched; and one relabelled is updated to the whole
// label set its last label record carries, UNREAD and STARRED as its flags,
// or reread when that record carries none. It returns an error wrapping
// [mailsync.ErrPositionExpired] when position is not a history ID, or when
// Gmail no longer keeps history that old, which it answers with 404 (Gmail's
// "Synchronize clients with Gmail" guide).
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
	call := service.Users.History.List(gmailUser).
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
	// (history.list reference), so a message's last label record holds
	// where it stands after the page.
	var changes pageChanges
	for _, record := range list.History {
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
				changes.relabelled(m.Message, record.Id)
			}
		}
		for _, m := range record.LabelsRemoved {
			if m != nil {
				changes.relabelled(m.Message, record.Id)
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
	// labels are the message's labels after its last label record, nil
	// when that record carried none.
	//
	// NOTE: Gmail's reference shows a label record's message with labelIds
	// but not when they are taken. On real Gmail they are the message's
	// whole set just after that record (observed 2026-11-10); the emulator
	// gives the set now. Either is where the message stands after its last
	// record.
	labels []string
	// version is the ID of the record labels came with.
	version uint64
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

// relabelled records that the label record with recordID changed m, with
// the labels it carries, replacing an earlier record's.
func (p *pageChanges) relabelled(m *gmail.Message, recordID uint64) {
	if h := p.message(m); h != nil {
		h.labels = m.LabelIds
		h.version = recordID
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
	h := &messageHistory{}
	p.byID[m.Id] = h
	p.ids = append(p.ids, m.Id)
	return h
}

// fill sorts each message into page: deleted whatever else happened to
// it; fetched whole when added; reread when its last label record carried
// no labels, since Gmail leaves out an empty list too; and otherwise
// updated.
func (p *pageChanges) fill(page *mailsync.ChangePage) {
	for _, id := range p.ids {
		h := p.byID[id]
		switch {
		case h.deleted:
			page.Deleted = append(page.Deleted, id)
		case h.added:
			page.Fetch = append(page.Fetch, id)
		case h.labels == nil:
			page.Reread = append(page.Reread, id)
		default:
			update := mailsync.MessageUpdate{ProviderID: id, Version: h.version}
			update.MailboxProviderIDs, update.Seen, update.Flagged = labelState(h.labels)
			page.Update = append(page.Update, update)
		}
	}
}

// formatHistoryID returns historyID as a position: Gmail's discovery
// document types it as an unsigned 64-bit integer, sent as decimal text.
func formatHistoryID(historyID uint64) string {
	return strconv.FormatUint(historyID, 10)
}
