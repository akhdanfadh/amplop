package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/connectedaccount"
	"inbok/internal/label"
	"inbok/internal/mailsync"
	"inbok/internal/session"
)

const labelsPath = "/labels"

// labelService changes the member's labels at their provider.
type labelService interface {
	Create(ctx context.Context, userID, accountID int64, name string, parent uuid.UUID) (label.Label, error)
	Edit(ctx context.Context, userID int64, id uuid.UUID, name string, parent uuid.UUID) (label.Label, error)
	Delete(ctx context.Context, userID int64, id uuid.UUID) ([]uuid.UUID, error)
}

type labelHandler struct {
	// labels is nil when no Google client is configured.
	labels labelService
}

func registerLabels(api huma.API, labels labelService) {
	handler := labelHandler{labels: labels}
	registerLabelCreate(api, handler)
	registerLabelEdit(api, handler)
	registerLabelDelete(api, handler)
}

// labelResource is one of the member's labels, as List Places shows it.
type labelResource struct {
	ID        uuid.UUID  `json:"id" format:"uuid" doc:"The label's id: a place of List Conversations and List Places."`
	AccountID int64      `json:"accountId" doc:"The connected account it belongs to."`
	Name      string     `json:"name" doc:"Its own name under its parent, as the provider saved it, e.g. with its white space collapsed, and as List Places names it."`
	Parent    *uuid.UUID `json:"parent" nullable:"true" format:"uuid" doc:"The label it nests under; null at the top."`
}

// labelResourceOf converts l.
func labelResourceOf(l label.Label) labelResource {
	resource := labelResource{ID: l.ID, AccountID: l.AccountID, Name: l.Name}
	if l.Parent != uuid.Nil() {
		parent := l.Parent
		resource.Parent = &parent
	}
	return resource
}

// parentOf returns parent, or [uuid.Nil] for none.
func parentOf(parent *uuid.UUID) uuid.UUID {
	if parent == nil {
		return uuid.Nil()
	}
	return *parent
}

// The texts of a label's 422s, at body.name or body.parent.
const (
	labelNameTooLongMessage  = "Too long with the names of the labels it's under."
	labelNameReservedMessage = "Gmail keeps this name for its own labels."
	labelNameTakenMessage    = "This account already has a label with this name."
	labelParentMessage       = "Choose one of this account's labels, not this one or one inside it."
)

// labelProblem maps the errors of changing a label.
func labelProblem(err error) error {
	var input inputError
	switch {
	case errors.Is(err, label.ErrNotFound):
		return labelNotFoundProblem()
	case errors.Is(err, connectedaccount.ErrNotFound), errors.Is(err, connectedaccount.ErrDeactivated):
		return connectedAccountProblem(err)
	case errors.Is(err, errGmailNotConfigured):
		return newProblem(http.StatusConflict, gmailNotConfiguredType, "Gmail not configured", "Labels can't be changed until an administrator sets up a Google client.")
	case errors.Is(err, connectedaccount.ErrNeedsReconnect), errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return newProblem(http.StatusConflict, accountNeedsReconnectType, "Account needs reconnecting", "The label's account no longer opens. Reconnect it to change its labels.")
	case errors.Is(err, mailsync.ErrProviderBusy), errors.Is(err, connectedaccount.ErrRefreshFailed),
		errors.Is(err, connectedaccount.ErrCredentialChanged):
		busy := newProblem(http.StatusServiceUnavailable, mailProviderBusyType, "Mail provider busy", "The label's mail provider is not answering right now. Try again shortly.")
		return huma.ErrorWithHeaders(busy, http.Header{retryAfterHeader: {retryAfterSeconds(providerBusyRetry)}})
	// Huma's own pattern check refuses it first, in the same words.
	case errors.Is(err, label.ErrNameBlank):
		input = inputError{Location: "body.name", Message: "Enter a valid value."}
	case errors.Is(err, label.ErrNameTooLong):
		input = inputError{Location: "body.name", Message: labelNameTooLongMessage}
	case errors.Is(err, label.ErrNameReserved):
		input = inputError{Location: "body.name", Message: labelNameReservedMessage}
	case errors.Is(err, label.ErrNameTaken):
		input = inputError{Location: "body.name", Message: labelNameTakenMessage}
	case errors.Is(err, label.ErrInvalidParent):
		input = inputError{Location: "body.parent", Message: labelParentMessage}
	default:
		separator, ok := errors.AsType[*label.SeparatorError](err)
		if !ok {
			return err
		}
		input = inputError{Location: "body.name", Message: fmt.Sprintf("Can't contain %q.", separator.Separator)}
	}
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{input}
	return invalid
}

// labelNotFoundProblem returns the problem for a label the member does not
// have. Reading a place's conversations and changing a label share it, so
// the type has one meaning, title, and detail.
func labelNotFoundProblem() error {
	return newProblem(http.StatusNotFound, labelNotFoundType, "Label not found", "The label does not exist. It may have been deleted.")
}

// labelResponses documents the failures of an operation that changes a
// label at its provider, with notFound as the 404's description and
// example, and a 422 when invalid holds examples.
func labelResponses(api huma.API, notFound string, notFoundExample error, invalid map[string]*huma.Example) map[string]*huma.Response {
	statuses := []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable}
	if invalid != nil {
		statuses = append(statuses, http.StatusUnprocessableEntity)
	}
	responses := memberResponses(api, statuses...)

	missing := responses[strconv.Itoa(http.StatusNotFound)]
	missing.Description = notFound
	missing.Content[problemContentType].Example = notFoundExample

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The account needs reconnecting: Google stopped accepting its permission, or no key inbok has opens the permission it saved. " +
		"Or no Google client is configured, so Gmail is not offered."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"needsReconnect":     {Value: labelProblem(connectedaccount.ErrNeedsReconnect)},
		"gmailNotConfigured": {Value: labelProblem(errGmailNotConfigured)},
	}

	busy := responses[strconv.Itoa(http.StatusServiceUnavailable)]
	busy.Description = "The mail provider refuses requests for now, or took too long. Part of a change may be done; List Places shows the labels as the provider has them."
	busy.Content[problemContentType].Example = newProblem(http.StatusServiceUnavailable, mailProviderBusyType, "Mail provider busy", "The label's mail provider is not answering right now. Try again shortly.")

	if invalid != nil {
		unprocessable := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
		unprocessable.Description = "The name or parent is refused, by inbok or by the provider; errors[].location names the field."
		unprocessable.Content[problemContentType].Examples = invalid
	}
	return responses
}

// labelInvalidExamples are the 422s of a name or parent.
func labelInvalidExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"separator":     {Value: labelProblem(&label.SeparatorError{Separator: "/"})},
		"tooLong":       {Value: labelProblem(label.ErrNameTooLong)},
		"reserved":      {Value: labelProblem(label.ErrNameReserved)},
		"taken":         {Value: labelProblem(label.ErrNameTaken)},
		"invalidParent": {Value: labelProblem(label.ErrInvalidParent)},
	}
}

// ========================
// Create label
// ========================

type labelCreateInput struct {
	Body struct {
		AccountID int64      `json:"accountId" minimum:"1" doc:"The connected account to make it in, one of the signed-in account's."`
		Name      string     `json:"name" pattern:"\\S" maxLength:"225" doc:"The label's own name, without the provider's separator, Gmail's /. The provider saves it its own way, e.g. Gmail collapses white space, and refuses a name it keeps, e.g. Inbox, one the account has where the label would be, in any case, and one too long with the names of the labels it is under."`
		Parent    *uuid.UUID `json:"parent" required:"true" nullable:"true" format:"uuid" doc:"The label of the same account to nest it under, by id from List Places; null for the top."`
	}
}

func registerLabelCreate(api huma.API, handler labelHandler) {
	responses := labelResponses(api, "The signed-in account has no connected account with this id.",
		connectedAccountProblem(connectedaccount.ErrNotFound), labelInvalidExamples())
	huma.Register(api, huma.Operation{
		OperationID: "createLabel",
		Method:      http.MethodPost,
		Path:        labelsPath,
		Summary:     "Create Label",
		Tags:        []string{tagLabels},
		Description: "Makes a label in one of the signed-in account's connected accounts, at its provider, and returns it as the provider saved it. " +
			"inbok then lists the account's labels at the provider and saves them, as a sync does.",
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusCreated,
		Responses:     responses,
	}, handler.handleCreateLabel)
}

func (h labelHandler) handleCreateLabel(ctx context.Context, in *labelCreateInput) (*output[labelResource], error) {
	if h.labels == nil {
		return nil, labelProblem(errGmailNotConfigured)
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	created, err := h.labels.Create(ctx, user.ID, in.Body.AccountID, in.Body.Name, parentOf(in.Body.Parent))
	if err != nil {
		return nil, labelProblem(err)
	}
	return respondData(labelResourceOf(created)), nil
}

// ========================
// Edit label
// ========================

type labelInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"The label's id, from List Places."`
}

// NOTE: Huma reads no parameter of an embedded struct whose type is
// unexported, so the input repeats the id.
type labelEditInput struct {
	ID   uuid.UUID `path:"id" format:"uuid" doc:"The label's id, from List Places."`
	Body struct {
		Name   string     `json:"name" pattern:"\\S" maxLength:"225" doc:"The label's new own name, or the one it has, which may hold the separator when List Places shows it so; refused as in Create Label."`
		Parent *uuid.UUID `json:"parent" required:"true" nullable:"true" format:"uuid" doc:"The label of the same account to nest it under, or the one it is under; never itself or a label inside it. Null for the top."`
	}
}

func registerLabelEdit(api huma.API, handler labelHandler) {
	responses := labelResponses(api, "The signed-in account has no label with this id, e.g. deleted since.", labelNotFoundProblem(), labelInvalidExamples())
	huma.Register(api, huma.Operation{
		OperationID: "editLabel",
		Method:      http.MethodPatch,
		Path:        labelsPath + "/{id}",
		Summary:     "Edit Label",
		Tags:        []string{tagLabels},
		Description: "Renames a label, moves it under another of its account's labels or to the top, or both, at its provider, and returns it as the provider saved it. " +
			"The labels inside it follow, keeping their own names; its conversations stay in it. " +
			"A provider may do it in steps: when one fails, the rest of the change is not made, and List Places shows the labels as the provider has them.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleEditLabel)
}

func (h labelHandler) handleEditLabel(ctx context.Context, in *labelEditInput) (*output[labelResource], error) {
	if h.labels == nil {
		return nil, labelProblem(errGmailNotConfigured)
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	edited, err := h.labels.Edit(ctx, user.ID, in.ID, in.Body.Name, parentOf(in.Body.Parent))
	if err != nil {
		return nil, labelProblem(err)
	}
	return respondData(labelResourceOf(edited)), nil
}

// ========================
// Delete label
// ========================

type deletedLabels struct {
	Deleted []uuid.UUID `json:"deleted" nullable:"false" format:"uuid" doc:"The ids of the labels deleted: the label and each label inside it that inbok had."`
}

func registerLabelDelete(api huma.API, handler labelHandler) {
	responses := labelResponses(api, "The signed-in account has no label with this id, e.g. deleted since.", labelNotFoundProblem(), nil)
	huma.Register(api, huma.Operation{
		OperationID: "deleteLabel",
		Method:      http.MethodDelete,
		Path:        labelsPath + "/{id}",
		Summary:     "Delete Label",
		Tags:        []string{tagLabels},
		Description: "Deletes a label and every label inside it at its provider, the deepest first, and never their mail: their conversations keep their other places and stay in All mail. " +
			"A label already gone at the provider counts as deleted, so a delete that failed part way finishes when asked again.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleDeleteLabel)
}

func (h labelHandler) handleDeleteLabel(ctx context.Context, in *labelInput) (*output[deletedLabels], error) {
	if h.labels == nil {
		return nil, labelProblem(errGmailNotConfigured)
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	deleted, err := h.labels.Delete(ctx, user.ID, in.ID)
	if err != nil {
		return nil, labelProblem(err)
	}
	return respondData(deletedLabels{Deleted: deleted}), nil
}
