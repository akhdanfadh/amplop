package google

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/connectedaccount"
	"inbok/internal/googleemulator"
	"inbok/internal/label"
	"inbok/internal/mailsync"
)

// workLabels are alex's labels Work, Work/Projects, Work/Projects/Q4, and
// Home, and Old/Bills with no Old.
func workLabels() []googleemulator.Label {
	return []googleemulator.Label{
		{ID: "Label_work", Mailbox: alexMailbox, Name: "Work"},
		{ID: "Label_projects", Mailbox: alexMailbox, Name: "Work/Projects"},
		{ID: "Label_q4", Mailbox: alexMailbox, Name: "Work/Projects/Q4"},
		{ID: "Label_home", Mailbox: alexMailbox, Name: "Home"},
		{ID: "Label_bills", Mailbox: alexMailbox, Name: "Old/Bills"},
	}
}

// labelNames returns the whole names of the member's labels, as
// labels.list gives them, by ID.
func labelNames(t *testing.T, client *Client, token connectedaccount.AccessToken) map[string]string {
	t.Helper()
	service, err := client.gmail(t.Context(), token)
	require.NoError(t, err)
	labels, err := client.labels(t.Context(), service)
	require.NoError(t, err)
	return labelTreeOf(labels).whole
}

// A label is made under its parent by Gmail's whole name, its white space
// collapsed as Gmail does, and nests there when listed.
func TestCreateMailbox(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	id, err := client.CreateMailbox(t.Context(), token, "Label_projects", "  Q1 \t plans ")

	require.NoError(t, err)
	assert.Equal(t, "Work/Projects/Q1 plans", labelNames(t, client, token)[id])
	mailboxes, err := client.Mailboxes(t.Context(), token)
	require.NoError(t, err)
	assert.Contains(t, mailboxes, mailsync.Mailbox{ProviderID: id, Name: "Q1 plans", ProviderParentID: "Label_projects", MadeBy: mailsync.MadeByMember})
}

// Gmail's refusals are label's errors; the ones inbok checks first never
// reach Gmail.
func TestCreateMailboxRefused(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	for name, c := range map[string]struct {
		parent, name string
		want         error
	}{
		"taken, in another case":    {"", "home", label.ErrNameTaken},
		"taken under its parent":    {"Label_work", "projects", label.ErrNameTaken},
		"gmail's own":               {"", "Inbox", label.ErrNameReserved},
		"gmail's own, any case":     {"", "all mail", label.ErrNameReserved},
		"the separator":             {"", "Clients/Initech", &label.SeparatorError{Separator: "/"}},
		"too many bytes":            {"", strings.Repeat("é", 128), label.ErrNameTooLong},
		"too long with its parents": {"Label_projects", strings.Repeat("a", 225-len("Work/Projects/")+1), label.ErrNameTooLong},
		"no such parent":            {"Label_gone", "Q1", label.ErrInvalidParent},
		"gmail's own as parent":     {"INBOX", "Q1", label.ErrInvalidParent},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.CreateMailbox(t.Context(), token, c.parent, c.name)

			if separator, ok := c.want.(*label.SeparatorError); ok {
				assert.Equal(t, separator, err)
				return
			}
			require.ErrorIs(t, err, c.want)
		})
	}
	assert.Len(t, labelNames(t, client, token), len(workLabels()), "nothing made")
}

// A name at Gmail's limits is made.
func TestCreateMailboxAtLimits(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	_, err := client.CreateMailbox(t.Context(), token, "Label_projects", strings.Repeat("a", 225-len("Work/Projects/")))
	require.NoError(t, err)
	_, err = client.CreateMailbox(t.Context(), token, "", strings.Repeat("é", 127))
	require.NoError(t, err)
	// 213 characters and 252 bytes, but 226 UTF-16 units.
	_, err = client.CreateMailbox(t.Context(), token, "", strings.Repeat("a", 200)+strings.Repeat("😀", 13))
	require.NoError(t, err)
}

// Renaming a label renames every label inside it to follow, at any depth.
func TestEditMailboxRename(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	err := client.EditMailbox(t.Context(), token, "Label_work", "", "Job")

	require.NoError(t, err)
	names := labelNames(t, client, token)
	assert.Equal(t, "Job", names["Label_work"])
	assert.Equal(t, "Job/Projects", names["Label_projects"])
	assert.Equal(t, "Job/Projects/Q4", names["Label_q4"])
	assert.Equal(t, "Home", names["Label_home"])
}

// Moving a label under another moves the labels inside it too.
func TestEditMailboxMove(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	err := client.EditMailbox(t.Context(), token, "Label_projects", "Label_home", "Projects")

	require.NoError(t, err)
	names := labelNames(t, client, token)
	assert.Equal(t, "Home/Projects", names["Label_projects"])
	assert.Equal(t, "Home/Projects/Q4", names["Label_q4"])
	assert.Equal(t, "Work", names["Label_work"])
}

// A label whose own name holds the separator, at the top since its parent
// is gone, keeps that name when moved; a new name can't hold it.
func TestEditMailboxKeepsOwnName(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	err := client.EditMailbox(t.Context(), token, "Label_bills", "Label_home", "Old/Bills")

	require.NoError(t, err)
	assert.Equal(t, "Home/Old/Bills", labelNames(t, client, token)["Label_bills"])
	err = client.EditMailbox(t.Context(), token, "Label_home", "", "New/Home")
	assert.Equal(t, &label.SeparatorError{Separator: "/"}, err)
}

// A label can't move under itself or a label inside it, nor be found once
// gone; a name too long for a label inside it changes nothing.
func TestEditMailboxRefused(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	for name, c := range map[string]struct {
		id, parent, name string
		want             error
	}{
		"under itself":           {"Label_work", "Label_work", "Work", label.ErrInvalidParent},
		"under one inside it":    {"Label_work", "Label_q4", "Work", label.ErrInvalidParent},
		"no such label":          {"Label_gone", "", "Gone", label.ErrNotFound},
		"taken":                  {"Label_home", "", "WORK", label.ErrNameTaken},
		"too long for one in it": {"Label_work", "", strings.Repeat("w", 225-len("/Projects/Q4")+1), label.ErrNameTooLong},
	} {
		t.Run(name, func(t *testing.T) {
			err := client.EditMailbox(t.Context(), token, c.id, c.parent, c.name)

			require.ErrorIs(t, err, c.want)
		})
	}
	assert.Equal(t, map[string]string{
		"Label_work": "Work", "Label_projects": "Work/Projects", "Label_q4": "Work/Projects/Q4",
		"Label_home": "Home", "Label_bills": "Old/Bills",
	}, labelNames(t, client, token), "nothing renamed")
}

// Deleting a label deletes every label inside it, and a label already gone
// counts as deleted.
func TestDeleteMailbox(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)

	require.NoError(t, client.DeleteMailbox(t.Context(), token, "Label_work"))

	assert.Equal(t, map[string]string{"Label_home": "Home", "Label_bills": "Old/Bills"}, labelNames(t, client, token))
	require.NoError(t, client.DeleteMailbox(t.Context(), token, "Label_work"))
}

// labelCalls records the label writes a client sends to the emulator, as
// "METHOD id", and answers refuse's with its status instead of sending
// them, a body Gmail's client decodes as its error.
type labelCalls struct {
	base   http.RoundTripper
	mu     sync.Mutex
	writes []string
	refuse map[string]int
}

func (l *labelCalls) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || !strings.Contains(req.URL.Path, "/labels") {
		return l.base.RoundTrip(req)
	}
	call := req.Method + " " + path.Base(req.URL.Path)
	l.mu.Lock()
	l.writes = append(l.writes, call)
	status, refused := l.refuse[call]
	l.mu.Unlock()
	if !refused {
		return l.base.RoundTrip(req)
	}
	body := fmt.Sprintf(`{"error":{"code":%d,"message":"refused by the test"}}`, status)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// watchLabels makes client's label writes go through a [labelCalls] that
// refuses refuse's, and returns it.
func watchLabels(client *Client, refuse map[string]int) *labelCalls {
	calls := &labelCalls{base: client.http.Transport, refuse: refuse}
	client.http.Transport = calls
	return calls
}

// A rename patches the label itself before the labels inside it, so a
// refusal of the label changes nothing.
func TestEditMailboxOrder(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)
	calls := watchLabels(client, nil)

	require.NoError(t, client.EditMailbox(t.Context(), token, "Label_work", "", "Job"))

	assert.Equal(t, "PATCH Label_work", calls.writes[0])
	assert.ElementsMatch(t, []string{"PATCH Label_work", "PATCH Label_projects", "PATCH Label_q4"}, calls.writes)
}

// A rename stops at the first label refused, leaving those after it as
// they were.
func TestEditMailboxStops(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)
	calls := watchLabels(client, map[string]int{"PATCH Label_projects": http.StatusForbidden})

	err := client.EditMailbox(t.Context(), token, "Label_work", "", "Job")

	require.Error(t, err)
	assert.Equal(t, []string{"PATCH Label_work", "PATCH Label_projects"}, calls.writes)
	names := labelNames(t, client, token)
	assert.Equal(t, "Job", names["Label_work"])
	assert.Equal(t, "Work/Projects", names["Label_projects"])
	assert.Equal(t, "Work/Projects/Q4", names["Label_q4"])
}

// A delete goes deepest first, the label itself last.
func TestDeleteMailboxOrder(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)
	calls := watchLabels(client, nil)

	require.NoError(t, client.DeleteMailbox(t.Context(), token, "Label_work"))

	assert.Equal(t, []string{"DELETE Label_q4", "DELETE Label_projects", "DELETE Label_work"}, calls.writes)
}

// A delete that fails stops there, keeping every label above, so asking
// again finds and finishes the rest.
func TestDeleteMailboxStops(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)
	calls := watchLabels(client, map[string]int{"DELETE Label_projects": http.StatusForbidden})

	err := client.DeleteMailbox(t.Context(), token, "Label_work")

	require.Error(t, err)
	assert.Equal(t, []string{"DELETE Label_q4", "DELETE Label_projects"}, calls.writes)
	names := labelNames(t, client, token)
	assert.NotContains(t, names, "Label_q4")
	assert.Contains(t, names, "Label_projects")
	assert.Contains(t, names, "Label_work")

	calls.refuse = nil
	require.NoError(t, client.DeleteMailbox(t.Context(), token, "Label_work"))
	assert.Equal(t, map[string]string{"Label_home": "Home", "Label_bills": "Old/Bills"}, labelNames(t, client, token))
}

// A label inside that Gmail no longer has counts as deleted, and the
// delete goes on.
func TestDeleteMailboxGoneInside(t *testing.T) {
	client, token := startMailbox(t, workLabels(), nil)
	calls := watchLabels(client, map[string]int{"DELETE Label_q4": http.StatusNotFound})

	require.NoError(t, client.DeleteMailbox(t.Context(), token, "Label_work"))

	assert.Equal(t, []string{"DELETE Label_q4", "DELETE Label_projects", "DELETE Label_work"}, calls.writes)
}
