package google

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
	"inbok/internal/mailsync"
)

// threadFields and messageFields are the parts of a thread, or of one
// message, an action's answer is read for: each message's labels (Gmail's
// performance tips, "Partial response").
//
// NOTE: Gmail answers a label change and Trash without history IDs,
// whatever the fields ask for (seen on inbok's test account, 2026-11-15),
// so an action's updates carry no version; the action writes them at
// [Client.Version] read before.
const (
	threadFields  = "messages(id,labelIds)"
	messageFields = "id,labelIds"
)

// maxModifyLabels is how many labels one threads.modify takes to add,
// and how many to remove (Gmail API reference, users.threads.modify).
const maxModifyLabels = 100

// labelChange is the labels a threads.modify or messages.modify adds and
// removes.
type labelChange struct {
	add, remove []string
}

// labelChanges are the actions that are a change of labels, by Gmail's rules:
// read is UNREAD removed, archive is INBOX removed, star is STARRED added,
// and spam is SPAM added and INBOX removed, as Gmail's Report spam does, and
// untrash is TRASH removed and INBOX added. SPAM, TRASH, and INBOX are all
// labels a client can set (Gmail's "Manage labels" guide).
var labelChanges = map[conversation.Action]labelChange{
	conversation.ActionRead:      {remove: []string{labelUnread}},
	conversation.ActionUnread:    {add: []string{labelUnread}},
	conversation.ActionArchive:   {remove: []string{labelInbox}},
	conversation.ActionUnarchive: {add: []string{labelInbox}},
	conversation.ActionStar:      {add: []string{labelStarred}},
	conversation.ActionUnstar:    {remove: []string{labelStarred}},
	conversation.ActionSpam:      {add: []string{labelSpam}, remove: []string{labelInbox}},
	conversation.ActionUnspam:    {add: []string{labelInbox}, remove: []string{labelSpam}},
	// As Gmail's own Move to Inbox from Trash: threads.untrash would leave
	// the thread out of the inbox, which Trash took it out of.
	conversation.ActionUntrash: {add: []string{labelInbox}, remove: []string{labelTrash}},
}

// ChangeThread applies action to thread: to its newest message with
// messages.modify when action is [conversation.Action.OnNewest], else to
// every message with threads.modify, or threads.trash for Trash. It
// returns each changed message's labels as Gmail answers, as inbok keeps
// them, without a version, and an error wrapping
// [conversation.ErrNotFound] when Gmail no longer has the thread or that
// message.
func (c *Client) ChangeThread(ctx context.Context, token connectedaccount.AccessToken, thread conversation.Thread, action conversation.Action) ([]mailsync.MessageUpdate, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return nil, err
	}
	threads := service.Users.Threads
	// call returns the changed messages.
	var call func() ([]*gmail.Message, error)
	change, isLabels := labelChanges[action]
	switch {
	case isLabels && action.OnNewest():
		request := &gmail.ModifyMessageRequest{AddLabelIds: change.add, RemoveLabelIds: change.remove}
		call = func() ([]*gmail.Message, error) {
			message, err := service.Users.Messages.Modify(gmailUser, thread.ProviderNewestMessageID, request).Fields(messageFields).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return []*gmail.Message{message}, nil
		}
	case isLabels:
		request := &gmail.ModifyThreadRequest{AddLabelIds: change.add, RemoveLabelIds: change.remove}
		call = threadMessages(func() (*gmail.Thread, error) {
			return threads.Modify(gmailUser, thread.ProviderThreadID, request).Fields(threadFields).Context(ctx).Do()
		})
	case action == conversation.ActionTrash:
		call = threadMessages(func() (*gmail.Thread, error) {
			return threads.Trash(gmailUser, thread.ProviderThreadID).Fields(threadFields).Context(ctx).Do()
		})
	default:
		return nil, fmt.Errorf("gmail api: no call for action %q", action)
	}
	return c.changed(ctx, call)
}

// ModifyThread applies modification to thread as it is: its labels and
// cleared marks to every message with threads.modify, then its set marks
// to the newest message with messages.modify, each only when it has any.
// Labels and cleared marks together over what one threads.modify removes
// (maxModifyLabels) take two: the labels, then the cleared marks. It
// returns as [Client.ChangeThread] does, except that when a call fails
// after another, it returns the last answer's updates, which Gmail kept,
// with the failed call's error.
func (c *Client) ModifyThread(ctx context.Context, token connectedaccount.AccessToken, thread conversation.Thread, modification conversation.Modification) ([]mailsync.MessageUpdate, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return nil, err
	}
	every := labelChange{add: modification.AddMailboxes, remove: slices.Clone(modification.RemoveMailboxes)}
	var cleared []string
	var newest labelChange
	for _, mark := range []struct {
		label string
		mark  conversation.Mark
	}{{labelStarred, modification.Starred}, {labelUnread, modification.Unread}} {
		switch mark.mark {
		case conversation.MarkSet:
			newest.add = append(newest.add, mark.label)
		case conversation.MarkCleared:
			cleared = append(cleared, mark.label)
		}
	}
	threadChanges := []labelChange{every}
	if len(every.remove)+len(cleared) > maxModifyLabels {
		threadChanges = append(threadChanges, labelChange{remove: cleared})
	} else {
		threadChanges[0].remove = append(threadChanges[0].remove, cleared...)
	}

	// 1. Every message first, so the newest message's answer, last, is
	// where it stands after both. Each answer holds every message, so the
	// last is where the thread stands.
	var updates []mailsync.MessageUpdate
	for _, change := range threadChanges {
		if len(change.add) == 0 && len(change.remove) == 0 {
			continue
		}
		request := &gmail.ModifyThreadRequest{AddLabelIds: change.add, RemoveLabelIds: change.remove}
		changed, err := c.changed(ctx, threadMessages(func() (*gmail.Thread, error) {
			return service.Users.Threads.Modify(gmailUser, thread.ProviderThreadID, request).Fields(threadFields).Context(ctx).Do()
		}))
		if err != nil {
			return updates, err
		}
		updates = changed
	}
	// 2. The newest message.
	if len(newest.add) > 0 {
		request := &gmail.ModifyMessageRequest{AddLabelIds: newest.add}
		changed, err := c.changed(ctx, func() ([]*gmail.Message, error) {
			message, err := service.Users.Messages.Modify(gmailUser, thread.ProviderNewestMessageID, request).Fields(messageFields).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return []*gmail.Message{message}, nil
		})
		if err != nil {
			return updates, err
		}
		updates = slices.DeleteFunc(updates, func(u mailsync.MessageUpdate) bool { return u.ProviderID == changed[0].ProviderID })
		updates = append(updates, changed...)
	}
	return updates, nil
}

// changed makes call, retrying as [retry] does, and returns the messages
// it answers with as inbok keeps them, without a version, or an error
// wrapping [conversation.ErrNotFound] when Gmail no longer has them.
func (c *Client) changed(ctx context.Context, call func() ([]*gmail.Message, error)) ([]mailsync.MessageUpdate, error) {
	// Each call sets labels rather than toggling them, so a retried one
	// that Gmail already applied changes nothing more.
	messages, err := retry(ctx, c, call)
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %w", conversation.ErrNotFound, redactGmail(err))
	}
	if err != nil {
		return nil, redactGmail(err)
	}
	// A thread has at least one message; an answer without one cannot say
	// where the thread stands.
	if len(messages) == 0 {
		return nil, errGmailAnswer
	}
	updates := make([]mailsync.MessageUpdate, 0, len(messages))
	for _, message := range messages {
		if message.Id == "" {
			return nil, errGmailAnswer
		}
		update := mailsync.MessageUpdate{ProviderID: message.Id}
		update.MailboxProviderIDs, update.Seen, update.Flagged = labelState(message.LabelIds)
		updates = append(updates, update)
	}
	return updates, nil
}

// threadMessages returns call as a call that returns the thread's messages.
func threadMessages(call func() (*gmail.Thread, error)) func() ([]*gmail.Message, error) {
	return func() ([]*gmail.Message, error) {
		thread, err := call()
		if err != nil {
			return nil, err
		}
		return thread.Messages, nil
	}
}
