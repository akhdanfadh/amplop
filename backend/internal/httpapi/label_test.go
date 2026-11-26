package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/connectedaccount"
	"amplop/internal/label"
	"amplop/internal/mailsync"
)

var (
	clientsLabel = uuid.MustParse("0c1b2c3d-0000-4000-8000-000000000001")
	initechLabel = uuid.MustParse("0c1b2c3d-0000-4000-8000-000000000002")
)

// newLabelsAPI returns an API that changes labels with labels, or with no
// Google client configured when labels is nil.
func newLabelsAPI(labels *fakeLabels) *API {
	services := Services{Session: memberSessions(account.RoleMember)}
	if labels != nil {
		services.Labels = labels
	}
	return New(discard, services)
}

// Creating a label answers 201 with it as the provider saved it; a label
// at the top has a null parent.
func TestCreateLabel(t *testing.T) {
	labels := &fakeLabels{label: label.Label{ID: initechLabel, AccountID: 7, Name: "Initech", Parent: clientsLabel}}
	api := newLabelsAPI(labels)

	response := requestAuthenticated(api, http.MethodPost, "/labels",
		`{"accountId":7,"name":"Initech","parent":"`+clientsLabel.String()+`"}`)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"id":"`+initechLabel.String()+`","accountId":7,"name":"Initech","parent":"`+clientsLabel.String()+`"}}`, response.Body.String())
	assert.Equal(t, []string{fmt.Sprintf("create 42 7 Initech %s", clientsLabel)}, labels.calls)

	labels.label.Parent = uuid.Nil()
	response = requestAuthenticated(api, http.MethodPost, "/labels", `{"accountId":7,"name":"Initech","parent":null}`)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.Nil(t, decodeJSON(t, response.Body.Bytes())["data"].(map[string]any)["parent"])
	assert.Equal(t, fmt.Sprintf("create 42 7 Initech %s", uuid.Nil()), labels.calls[1])
}

// The contract refuses a blank name, a name past the limit, and a missing
// parent before the service runs.
func TestCreateLabelInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"blank name":     `{"accountId":7,"name":"  ","parent":null}`,
		"long name":      `{"accountId":7,"name":"` + fmt.Sprintf("%0226d", 0) + `","parent":null}`,
		"missing parent": `{"accountId":7,"name":"Initech"}`,
	} {
		t.Run(name, func(t *testing.T) {
			labels := &fakeLabels{}

			response := requestAuthenticated(newLabelsAPI(labels), http.MethodPost, "/labels", body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Empty(t, labels.calls)
		})
	}
}

// Each refusal of a label change answers its own status and type, a name's
// or parent's at its field.
func TestLabelProblems(t *testing.T) {
	for name, c := range map[string]struct {
		err        error
		status     int
		problem    string
		location   string
		message    string
		retryAfter string
	}{
		"not found":          {label.ErrNotFound, http.StatusNotFound, labelNotFoundType, "", "", ""},
		"no account":         {connectedaccount.ErrNotFound, http.StatusNotFound, connectedAccountNotFoundType, "", "", ""},
		"needs reconnect":    {connectedaccount.ErrNeedsReconnect, http.StatusConflict, accountNeedsReconnectType, "", "", ""},
		"token unreadable":   {connectedaccount.ErrTokenUnreadable, http.StatusConflict, accountNeedsReconnectType, "", "", ""},
		"provider busy":      {fmt.Errorf("%w: 429", mailsync.ErrProviderBusy), http.StatusServiceUnavailable, mailProviderBusyType, "", "", "30"},
		"token endpoint":     {connectedaccount.ErrRefreshFailed, http.StatusServiceUnavailable, mailProviderBusyType, "", "", "30"},
		"separator":          {&label.SeparatorError{Separator: "/"}, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "body.name", `Can't contain "/".`, ""},
		"too long":           {label.ErrNameTooLong, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "body.name", labelNameTooLongMessage, ""},
		"reserved":           {label.ErrNameReserved, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "body.name", labelNameReservedMessage, ""},
		"taken":              {label.ErrNameTaken, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "body.name", labelNameTakenMessage, ""},
		"parent":             {label.ErrInvalidParent, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "body.parent", labelParentMessage, ""},
		"unexpected failure": {fmt.Errorf("database down"), http.StatusInternalServerError, "", "", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			api := newLabelsAPI(&fakeLabels{err: c.err})

			response := requestAuthenticated(api, http.MethodPatch, "/labels/"+initechLabel.String(), `{"name":"Initech","parent":null}`)

			require.Equal(t, c.status, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			if c.problem != "" {
				assert.Equal(t, c.problem, body["type"])
			}
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
			if c.location != "" {
				assert.Equal(t, []any{map[string]any{"location": c.location, "message": c.message}}, body["errors"])
			}
		})
	}
}

// Editing a label passes its id, name, and parent, and answers with it.
func TestEditLabel(t *testing.T) {
	labels := &fakeLabels{label: label.Label{ID: initechLabel, AccountID: 7, Name: "Initrode"}}

	response := requestAuthenticated(newLabelsAPI(labels), http.MethodPatch, "/labels/"+initechLabel.String(), `{"name":"Initrode","parent":null}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"id":"`+initechLabel.String()+`","accountId":7,"name":"Initrode","parent":null}}`, response.Body.String())
	assert.Equal(t, []string{fmt.Sprintf("edit 42 %s Initrode %s", initechLabel, uuid.Nil())}, labels.calls)
}

// Deleting a label answers the ids of the labels deleted.
func TestDeleteLabel(t *testing.T) {
	labels := &fakeLabels{deleted: []uuid.UUID{initechLabel, clientsLabel}}

	response := requestAuthenticated(newLabelsAPI(labels), http.MethodDelete, "/labels/"+clientsLabel.String(), "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"deleted":["`+initechLabel.String()+`","`+clientsLabel.String()+`"]}}`, response.Body.String())
	assert.Equal(t, []string{fmt.Sprintf("delete 42 %s", clientsLabel)}, labels.calls)
}

// With no Google client configured, every label change is refused with a
// 409 before anything runs.
func TestLabelsGmailNotConfigured(t *testing.T) {
	api := newLabelsAPI(nil)

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/labels", `{"accountId":7,"name":"Initech","parent":null}`},
		{http.MethodPatch, "/labels/" + initechLabel.String(), `{"name":"Initech","parent":null}`},
		{http.MethodDelete, "/labels/" + initechLabel.String(), ""},
	} {
		response := requestAuthenticated(api, r.method, r.path, r.body)

		require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		assert.Equal(t, gmailNotConfiguredType, decodeJSON(t, response.Body.Bytes())["type"])
	}
}

type fakeLabels struct {
	label   label.Label
	deleted []uuid.UUID
	err     error
	calls   []string
}

func (f *fakeLabels) Create(_ context.Context, userID, accountID int64, name string, parent uuid.UUID) (label.Label, error) {
	f.calls = append(f.calls, fmt.Sprintf("create %d %d %s %s", userID, accountID, name, parent))
	return f.label, f.err
}

func (f *fakeLabels) Edit(_ context.Context, userID int64, id uuid.UUID, name string, parent uuid.UUID) (label.Label, error) {
	f.calls = append(f.calls, fmt.Sprintf("edit %d %s %s %s", userID, id, name, parent))
	return f.label, f.err
}

func (f *fakeLabels) Delete(_ context.Context, userID int64, id uuid.UUID) ([]uuid.UUID, error) {
	f.calls = append(f.calls, fmt.Sprintf("delete %d %s", userID, id))
	return f.deleted, f.err
}
