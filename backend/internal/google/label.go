package google

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"

	"inbok/internal/connectedaccount"
	"inbok/internal/label"
	"inbok/internal/mailsync"
)

// Gmail's limits on a label's whole name, with the names of the labels it
// is under: it refuses one over 225 characters with a 400, and fails one
// over 255 bytes of UTF-8 with a 500 (checked on inbok's test account,
// 2026-11-16). inbok checks both before calling, so a long name is the
// member's to fix rather than a failure. It counts characters as Unicode
// code points; whether Gmail counts one outside the Basic Multilingual
// Plane, e.g. an emoji, as one or two is unchecked, and the byte limit
// binds first for a name of them alone.
const (
	maxLabelChars = 225
	maxLabelBytes = 255
)

// messageInvalidLabelName is the message of Gmail's 400 for a name it keeps
// for its own labels, e.g. Inbox, Sent, or All Mail, in any case (checked on
// inbok's test account, 2026-11-16).
//
// NOTE: Gmail gives the same 400 for a blank or too long name, which
// [label.ValidateName] and [fitsLabel] refuse first, and documents none of
// them; the message is the only thing that tells this 400 from others.
const messageInvalidLabelName = "Invalid label name"

// CreateMailbox makes a label named name under the label parentID, or at
// the top when parentID is empty: Gmail's whole name parent/name, from
// labels.create. It returns the label's ID, and the errors of
// [label.Provider].
func (c *Client) CreateMailbox(ctx context.Context, token connectedaccount.AccessToken, parentID, name string) (string, error) {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return "", err
	}
	labels, err := c.labels(ctx, service)
	if err != nil {
		return "", err
	}
	tree := labelTreeOf(labels)
	name = cleanLabelName(name)
	if strings.Contains(name, labelSeparator) {
		return "", &label.SeparatorError{Separator: labelSeparator}
	}
	whole, err := tree.wholeName(parentID, name)
	if err != nil {
		return "", err
	}

	// NOTE: not retried: Gmail may have made the label before failing, and
	// a retry would then be refused as taken.
	created, err := service.Users.Labels.Create(gmailUser, &gmail.Label{Name: whole}).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", labelRefusal(ctx, err)
	}
	return created.Id, nil
}

// EditMailbox names the label id name and moves it under the label
// parentID, or to the top when parentID is empty, then gives each label
// inside it the new whole name in place of the old, each with
// labels.patch. It returns the errors of [label.Provider], and stops at
// the first refusal.
//
// DESIGN: Gmail renames only the label patched; the labels nested under it
// by name keep their old names, so each is renamed here, after the label
// itself, whose refusal, e.g. a taken name, then changes nothing. A label
// inside it that fails stays at the top under its old name, as Gmail shows
// a label whose parent is gone, which the member can move; asking again
// does not find it.
func (c *Client) EditMailbox(ctx context.Context, token connectedaccount.AccessToken, id, parentID, name string) error {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return err
	}
	labels, err := c.labels(ctx, service)
	if err != nil {
		return err
	}
	tree := labelTreeOf(labels)
	old, ok := tree.whole[id]
	if !ok {
		return label.ErrNotFound
	}
	name = cleanLabelName(name)
	if strings.Contains(name, labelSeparator) && name != tree.own[id] {
		return &label.SeparatorError{Separator: labelSeparator}
	}
	if parentID == id || slices.Contains(tree.inside(id), parentID) {
		return label.ErrInvalidParent
	}
	whole, err := tree.wholeName(parentID, name)
	if err != nil {
		return err
	}
	if whole == old {
		return nil
	}

	// 1. Name every label it changes, itself first, and refuse a name too
	// long before changing any.
	type rename struct{ id, name string }
	renames := []rename{{id, whole}}
	for _, inner := range tree.inside(id) {
		renames = append(renames, rename{inner, whole + strings.TrimPrefix(tree.whole[inner], old)})
	}
	for _, r := range renames {
		if !fitsLabel(r.name) {
			return label.ErrNameTooLong
		}
	}

	// 2. Rename them, the label first.
	for _, r := range renames {
		_, err := retry(ctx, c, func() (*gmail.Label, error) {
			return service.Users.Labels.Patch(gmailUser, r.id, &gmail.Label{Name: r.name}).Fields("id").Context(ctx).Do()
		})
		if err != nil {
			return labelRefusal(ctx, err)
		}
	}
	return nil
}

// DeleteMailbox deletes the label id and every label inside it, the
// deepest first, with labels.delete, which takes the label off its
// messages and deletes none. A label already gone counts as deleted. It
// returns the errors of [label.Provider], and stops at the first failure.
//
// DESIGN: Gmail's API deletes only the label asked, leaving the labels
// nested under it at the top, while its web app deletes them too (checked
// on inbok's test account, 2026-11-16). inbok deletes them, as the web app
// does, the deepest first, as JMAP asks a client to remove a mailbox's
// children before it (mailboxHasChild, RFC 8621 section 2.5): a delete
// that stops part way leaves every label above the one that failed, and
// asking again finds the rest.
func (c *Client) DeleteMailbox(ctx context.Context, token connectedaccount.AccessToken, id string) error {
	service, err := c.gmail(ctx, token)
	if err != nil {
		return err
	}
	labels, err := c.labels(ctx, service)
	if err != nil {
		return err
	}
	tree := labelTreeOf(labels)
	if _, ok := tree.whole[id]; !ok {
		return nil
	}
	deletes := tree.inside(id)
	slices.SortStableFunc(deletes, func(a, b string) int {
		return cmp.Compare(strings.Count(tree.whole[b], labelSeparator), strings.Count(tree.whole[a], labelSeparator))
	})
	for _, target := range append(deletes, id) {
		_, err := retry(ctx, c, func() (struct{}, error) {
			return struct{}{}, service.Users.Labels.Delete(gmailUser, target).Context(ctx).Do()
		})
		if refused, ok := errors.AsType[*googleapi.Error](err); ok && refused.Code == http.StatusNotFound {
			continue
		}
		if err != nil {
			return labelRefusal(ctx, err)
		}
	}
	return nil
}

// labelTree is the member's own labels of an account by ID: each one's
// whole name, as Gmail names it, and its own name, as [nest] gives it.
type labelTree struct {
	whole map[string]string
	own   map[string]string
}

// labelTreeOf returns the member's labels among labels, as labels.list
// gives them.
func labelTreeOf(labels []*gmail.Label) labelTree {
	tree := labelTree{whole: make(map[string]string), own: make(map[string]string)}
	for _, mailbox := range mailboxesOf(labels) {
		if mailbox.Role == mailsync.RoleNone && mailbox.MadeBy == mailsync.MadeByMember {
			tree.own[mailbox.ProviderID] = mailbox.Name
		}
	}
	for _, l := range labels {
		if _, ok := tree.own[l.Id]; ok {
			tree.whole[l.Id] = cleanText(l.Name)
		}
	}
	return tree
}

// inside returns the IDs of the labels nested under id by name, at any
// depth: those whose whole name starts with id's and the separator, as
// [nest] nests them.
func (t labelTree) inside(id string) []string {
	prefix := t.whole[id] + labelSeparator
	var found []string
	for other, whole := range t.whole {
		if other != id && strings.HasPrefix(whole, prefix) {
			found = append(found, other)
		}
	}
	slices.Sort(found)
	return found
}

// wholeName returns Gmail's whole name of a label named name under the
// label parentID, or at the top when parentID is empty, and
// [label.ErrInvalidParent] when parentID is none of the member's labels,
// or [label.ErrNameTooLong] when Gmail would refuse it as too long.
func (t labelTree) wholeName(parentID, name string) (string, error) {
	whole := name
	if parentID != "" {
		parent, ok := t.whole[parentID]
		if !ok {
			return "", label.ErrInvalidParent
		}
		whole = parent + labelSeparator + name
	}
	if !fitsLabel(whole) {
		return "", label.ErrNameTooLong
	}
	return whole, nil
}

// fitsLabel reports whether Gmail takes whole as a label's whole name by
// length.
func fitsLabel(whole string) bool {
	return utf8.RuneCountInString(whole) <= maxLabelChars && len(whole) <= maxLabelBytes
}

// cleanLabelName returns name as Gmail saves it: each run of white space,
// tabs and line breaks too, as one space, and none at either end (checked
// on inbok's test account, 2026-11-16), so its length is checked as Gmail
// will count it.
func cleanLabelName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// labelRefusal returns err, from a labels call made with ctx, as the
// [label] error it means: a 409 as [label.ErrNameTaken], Gmail's 400 for a
// name it keeps as [label.ErrNameReserved], and a 404 as
// [label.ErrNotFound]. A refusal waiting can outlast wraps
// [mailsync.ErrProviderBusy]; any other error is redacted.
func labelRefusal(ctx context.Context, err error) error {
	if errors.Is(err, mailsync.ErrProviderBusy) {
		return err
	}
	if refused, ok := errors.AsType[*googleapi.Error](err); ok {
		switch {
		case refused.Code == http.StatusConflict:
			return label.ErrNameTaken
		case refused.Code == http.StatusBadRequest && refused.Message == messageInvalidLabelName:
			return label.ErrNameReserved
		case refused.Code == http.StatusNotFound:
			return label.ErrNotFound
		}
	}
	if retryable(ctx, err) {
		return fmt.Errorf("%w: %w", mailsync.ErrProviderBusy, redactGmail(err))
	}
	return redactGmail(err)
}
