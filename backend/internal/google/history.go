package google

import (
	"context"
	"fmt"

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
		"labelsAdded/message/id,labelsRemoved/message/id),historyId,nextPageToken"
)

// Changes returns one page of the mailbox's changes since historyID, from
// history.list: a message added or with labels added or removed is
// changed, and one deleted is deleted, whatever else happened to it. It
// returns an error wrapping [mailsync.ErrHistoryExpired] when Gmail no
// longer keeps history that old, which it answers with 404 (Gmail's
// "Synchronize clients with Gmail" guide).
func (c *Client) Changes(ctx context.Context, token connectedaccount.AccessToken, historyID uint64, pageToken string) (mailsync.ChangePage, error) {
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
		return mailsync.ChangePage{}, fmt.Errorf("%w: %w", mailsync.ErrHistoryExpired, redactGmail(err))
	}
	if err != nil {
		return mailsync.ChangePage{}, redactGmail(err)
	}

	page := mailsync.ChangePage{HistoryID: list.HistoryId, NextPageToken: list.NextPageToken}

	// 1. Collect every record's messages: those added or relabelled, to
	// fetch again, and those deleted.
	var changed []string
	deleted := make(map[string]bool)
	for _, record := range list.History {
		if record == nil {
			continue
		}
		for _, m := range record.MessagesAdded {
			if m != nil {
				changed = appendMessageID(changed, m.Message)
			}
		}
		for _, m := range record.LabelsAdded {
			if m != nil {
				changed = appendMessageID(changed, m.Message)
			}
		}
		for _, m := range record.LabelsRemoved {
			if m != nil {
				changed = appendMessageID(changed, m.Message)
			}
		}
		for _, m := range record.MessagesDeleted {
			if m != nil && m.Message != nil && m.Message.Id != "" && !deleted[m.Message.Id] {
				deleted[m.Message.Id] = true
				page.Deleted = append(page.Deleted, m.Message.Id)
			}
		}
	}

	// 2. Only then leave the deleted out of the changed: a later record
	// may delete a message an earlier one added or relabelled.
	seen := make(map[string]bool, len(changed))
	for _, id := range changed {
		if !deleted[id] && !seen[id] {
			seen[id] = true
			page.Changed = append(page.Changed, id)
		}
	}
	return page, nil
}

// appendMessageID appends m's ID to ids, unless m has none.
func appendMessageID(ids []string, m *gmail.Message) []string {
	if m == nil || m.Id == "" {
		return ids
	}
	return append(ids, m.Id)
}
