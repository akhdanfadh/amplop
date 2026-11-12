package google

import (
	"context"
	"fmt"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
	"inbok/internal/mailsync"
)

// threadFields are the parts of a thread an action's answer is read for:
// each message's labels and the version they are of (Gmail's performance
// tips, "Partial response").
const threadFields = "messages(id,labelIds,historyId)"

// labelChange is the labels a threads.modify adds and removes.
type labelChange struct {
	add, remove []string
}

// labelChanges are the actions that are a change of labels, by Gmail's
// rules (ADR007, "Gmail labels as mailboxes"): read is UNREAD removed.
var labelChanges = map[conversation.Action]labelChange{
	conversation.ActionRead:   {remove: []string{labelUnread}},
	conversation.ActionUnread: {add: []string{labelUnread}},
}

// ChangeThread applies action to every message of the thread with
// threadID, with threads.modify, and returns each message's labels as
// Gmail answers, as inbok keeps them. It returns an error wrapping
// [conversation.ErrNotFound] when Gmail no longer has the thread.
func (c *Client) ChangeThread(ctx context.Context, token connectedaccount.AccessToken, threadID string, action conversation.Action) ([]mailsync.MessageUpdate, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return nil, err
	}
	change, ok := labelChanges[action]
	if !ok {
		return nil, fmt.Errorf("gmail api: no call for action %q", action)
	}
	request := &gmail.ModifyThreadRequest{AddLabelIds: change.add, RemoveLabelIds: change.remove}
	call := func() (*gmail.Thread, error) {
		return service.Users.Threads.Modify(gmailUser, threadID, request).Fields(threadFields).Context(ctx).Do()
	}

	// Each call sets labels rather than toggling them, so a retried one
	// that Gmail already applied changes nothing more.
	thread, err := retry(ctx, c, call)
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %w", conversation.ErrNotFound, redactGmail(err))
	}
	if err != nil {
		return nil, redactGmail(err)
	}
	// A thread has at least one message; an answer without one cannot say
	// where the thread stands.
	if len(thread.Messages) == 0 {
		return nil, errGmailAnswer
	}
	updates := make([]mailsync.MessageUpdate, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		if message.Id == "" {
			return nil, errGmailAnswer
		}
		update := mailsync.MessageUpdate{ProviderID: message.Id, Version: message.HistoryId}
		update.MailboxProviderIDs, update.Seen, update.Flagged = labelState(message.LabelIds)
		updates = append(updates, update)
	}
	return updates, nil
}
