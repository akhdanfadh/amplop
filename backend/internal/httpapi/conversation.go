package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
	"inbok/internal/mailsync"
	"inbok/internal/session"
)

const (
	conversationsPath = "/conversations"

	placeNotFoundType = problemPrefix + "place-not-found"
)

// conversationService reads the member's conversations.
type conversationService interface {
	List(ctx context.Context, userID int64, query conversation.Query) ([]conversation.Conversation, error)
	CountAfter(ctx context.Context, userID int64, place conversation.Place, account int64, after time.Time) (int, error)
	Places(ctx context.Context, userID int64) ([]conversation.PlaceCount, error)
	Get(ctx context.Context, userID int64, id uuid.UUID) (conversation.Conversation, []conversation.Message, error)
}

// conversationActionService applies the member's actions to their
// conversations.
type conversationActionService interface {
	Apply(ctx context.Context, userID int64, action conversation.Action, ids []uuid.UUID) (conversation.Applied, error)
}

type conversationHandler struct {
	conversations conversationService
	// actions is nil when no Google client is configured.
	actions conversationActionService
}

func registerConversations(api huma.API, conversations conversationService, actions conversationActionService) {
	handler := conversationHandler{conversations: conversations, actions: actions}

	registerConversationList(api, handler)
	registerNewConversationCount(api, handler)
	registerPlaceList(api, handler)
	registerConversationRead(api, handler)
	registerConversationAction(api, handler)
}

// mailAddress is one entry of an address header.
type mailAddress struct {
	Name    string `json:"name" doc:"The display name, perhaps empty."`
	Address string `json:"address" doc:"The address; empty for an entry that is none, e.g. a group's name, which is then the name."`
}

func mailAddressesOf(addresses []mailsync.Address) []mailAddress {
	out := make([]mailAddress, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, mailAddress(a))
	}
	return out
}

// conversationRow is one conversation as a list shows it. Its own fields
// are made from its shown messages: those in neither Spam nor Trash, or
// when it has none, its Spam and Trash ones; drafts never.
type conversationRow struct {
	ID           uuid.UUID     `json:"id" format:"uuid"`
	AccountID    int64         `json:"accountId" doc:"The connected account it belongs to."`
	Date         time.Time     `json:"date" doc:"When the provider received its newest shown message: one in neither Spam nor Trash, or when it has none, in Spam or Trash; drafts are never shown. Lists sort by it, newest first, then by id descending."`
	Subject      string        `json:"subject" doc:"Its oldest shown message's subject, as sent."`
	Preview      string        `json:"preview" doc:"A plain-text excerpt of its newest shown message, as the provider made it."`
	LatestFrom   []mailAddress `json:"latestFrom" nullable:"false" doc:"Its newest shown message's From, perhaps empty."`
	Participants []mailAddress `json:"participants" nullable:"false" doc:"The senders of its shown messages in date order, each address once, at most 10."`
	MessageCount int           `json:"messageCount" minimum:"1" doc:"How many shown messages it has."`
	Unread       bool          `json:"unread" doc:"Whether a shown message is unread."`
	Starred      bool          `json:"starred" doc:"Whether a shown message is starred."`
	Inbox        bool          `json:"inbox" doc:"Whether it is in the inbox."`
	Sent         bool          `json:"sent" doc:"Whether it is in Sent."`
	AllMail      bool          `json:"allMail" doc:"Whether it has a message in neither Spam nor Trash: All mail lists it, and Starred when starred."`
	Spam         bool          `json:"spam" doc:"Whether a message is in Spam."`
	Trash        bool          `json:"trash" doc:"Whether a message is in Trash."`
	LabelIDs     []uuid.UUID   `json:"labelIds" nullable:"false" doc:"The account's labels it is in, in a stable order, by name."`
}

// conversationRowOf converts c, whose own fields storage derived from its
// shown messages, as [conversation.Derive] chooses them.
func conversationRowOf(c conversation.Conversation) conversationRow {
	return conversationRow{
		ID:           c.ID,
		AccountID:    c.AccountID,
		Date:         c.LatestDate,
		Subject:      c.Subject,
		Preview:      c.Preview,
		LatestFrom:   mailAddressesOf(c.LatestFrom),
		Participants: mailAddressesOf(c.Participants),
		MessageCount: c.MessageCount,
		Unread:       c.Unread,
		Starred:      c.Flagged,
		Inbox:        c.InInbox,
		Sent:         c.InSent,
		AllMail:      c.InAllMail,
		Spam:         c.InSpam,
		Trash:        c.InTrash,
		LabelIDs:     c.LabelIDs,
	}
}

// placeOf parses text as a place, or returns a 422 naming the place
// parameter.
func placeOf(text string) (conversation.Place, error) {
	place, err := conversation.ParsePlace(text)
	if err != nil {
		invalid := requestProblem(http.StatusUnprocessableEntity)
		invalid.Errors = []inputError{{Location: "query.place", Message: "Choose a view or one of your labels."}}
		return conversation.Place{}, invalid
	}
	return place, nil
}

// placeProblem maps the errors of reading a place's conversations.
func placeProblem(err error) error {
	switch {
	case errors.Is(err, conversation.ErrPlaceNotFound):
		return newProblem(http.StatusNotFound, placeNotFoundType, "Label not found", "The label does not exist. It may have been deleted.")
	case errors.Is(err, conversation.ErrAccountNotFound):
		return connectedAccountProblem(connectedaccount.ErrNotFound)
	default:
		return err
	}
}

// placeResponses documents the failures of an operation on a place.
func placeResponses(api huma.API) map[string]*huma.Response {
	responses := memberResponses(api, http.StatusNotFound)
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no label with the place's id, or no connected account with the account filter's id."
	// OpenAPI allows example or examples, not both (3.1, Media Type Object).
	notFound.Content[problemContentType].Example = nil
	notFound.Content[problemContentType].Examples = map[string]*huma.Example{
		"label":   {Value: placeProblem(conversation.ErrPlaceNotFound)},
		"account": {Value: placeProblem(conversation.ErrAccountNotFound)},
	}
	return responses
}

// ========================
// Conversation list
// ========================

// NOTE: Huma reads no parameter of an embedded struct whose type is
// unexported, as it skips unexported fields, so each input repeats the place
// and account.
type conversationListInput struct {
	Place    string    `query:"place" required:"true" doc:"A view, inbox, starred, sent, all, spam, or trash, or the id of one of the account's labels from List Places. all is every conversation but those only in Spam or Trash; starred is the starred ones of all."`
	Account  int64     `query:"account" minimum:"1" doc:"A connected account's id, to list only its conversations; every account's when left out."`
	Before   time.Time `query:"before" doc:"The page holds conversations dated before it: the date of the last conversation of the page before, with beforeId, or any date alone. The first page leaves both out."`
	BeforeID uuid.UUID `query:"beforeId" format:"uuid" doc:"The id of the last conversation of the page before. With before, the page also holds conversations of its date with a lower id; it needs before."`
	Limit    int       `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"How many conversations to return, at most."`
}

type conversationPage struct {
	Conversations []conversationRow `json:"conversations" nullable:"false" doc:"Newest first, then by id descending. Fewer than limit means the place has no more."`
}

func registerConversationList(api huma.API, handler conversationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listConversations",
		Method:      http.MethodGet,
		Path:        conversationsPath,
		Summary:     "List Conversations",
		Tags:        []string{tagConversations},
		Description: "Lists one page of the signed-in account's conversations in a place, across its connected accounts or one of them. " +
			"A conversation is an account's messages in one provider thread; it is in a place when one of its messages is. " +
			"Messages in Spam or Trash count only there, and drafts nowhere.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: placeResponses(api),
	}, handler.handleListConversations)
}

func (h conversationHandler) handleListConversations(ctx context.Context, in *conversationListInput) (*output[conversationPage], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	place, err := placeOf(in.Place)
	if err != nil {
		return nil, err
	}
	query := conversation.Query{Place: place, Account: in.Account, Limit: in.Limit}

	// A date alone is the cursor with the zero id, which no id sorts below,
	// so the page holds only older dates; an id alone names no row.
	switch {
	case !in.Before.IsZero():
		query.Before = &conversation.Cursor{Date: in.Before, ID: in.BeforeID}
	case in.BeforeID != uuid.Nil():
		return nil, cursorProblem("query.before")
	}

	rows, err := h.conversations.List(ctx, user.ID, query)
	if err != nil {
		return nil, placeProblem(err)
	}
	page := conversationPage{Conversations: make([]conversationRow, 0, len(rows))}
	for _, row := range rows {
		page.Conversations = append(page.Conversations, conversationRowOf(row))
	}
	return respondData(page), nil
}

// cursorProblem returns the 422 for a beforeId sent without before, at
// location.
func cursorProblem(location string) error {
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{{Location: location, Message: "Send beforeId with before."}}
	return invalid
}

// ========================
// New conversation count
// ========================

type newConversationCountInput struct {
	Place   string    `query:"place" required:"true" doc:"A view, inbox, starred, sent, all, spam, or trash, or the id of one of the account's labels from List Places. all is every conversation but those only in Spam or Trash; starred is the starred ones of all."`
	Account int64     `query:"account" minimum:"1" doc:"A connected account's id, to list only its conversations; every account's when left out."`
	After   time.Time `query:"after" required:"true" doc:"The date of the newest conversation the list shows."`
}

type newConversationCount struct {
	Count int `json:"count" minimum:"0" doc:"How many conversations of the place are dated after after: new ones, and ones a new message moved up."`
}

func registerNewConversationCount(api huma.API, handler conversationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "countNewConversations",
		Method:      http.MethodGet,
		Path:        conversationsPath + "/new-count",
		Summary:     "Count New Conversations",
		Tags:        []string{tagConversations},
		Description: "Counts a place's conversations dated after the newest one a list shows, for a list that keeps its place while new mail arrives.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   placeResponses(api),
	}, handler.handleCountNewConversations)
}

func (h conversationHandler) handleCountNewConversations(ctx context.Context, in *newConversationCountInput) (*output[newConversationCount], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	place, err := placeOf(in.Place)
	if err != nil {
		return nil, err
	}
	count, err := h.conversations.CountAfter(ctx, user.ID, place, in.Account, in.After)
	if err != nil {
		return nil, placeProblem(err)
	}
	return respondData(newConversationCount{Count: count}), nil
}

// ========================
// Place list
// ========================

const placesPath = "/places"

// placeCount is one account's place with its unread count.
type placeCount struct {
	AccountID int64   `json:"accountId"`
	Place     string  `json:"place" doc:"A view's name or a label's id, as the place of List Conversations takes it."`
	Name      *string `json:"name" nullable:"true" doc:"A label's name as the provider names it, nesting included, e.g. Work/Projects for a Gmail label nested with slashes; null for a view."`
	Unread    int     `json:"unread" minimum:"0" doc:"How many of its conversations are unread."`
}

type places struct {
	Places []placeCount `json:"places" nullable:"false" doc:"Every place of each connected account, in a stable order: the accounts oldest first, each with its views, inbox, starred, sent, all, spam, and trash, then its labels by name. The web app orders them for display. Gmail's own labels, e.g. its inbox categories, are not places."`
}

func registerPlaceList(api huma.API, handler conversationHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listPlaces",
		Method:      http.MethodGet,
		Path:        placesPath,
		Summary:     "List Places",
		Tags:        []string{tagConversations},
		Description: "Lists the places of each of the signed-in account's connected accounts, its views and labels, with how many of their conversations are unread. " +
			"A label is a mailbox the member made at the provider: a Gmail label, and later an IMAP folder, shown alike. The provider's own mailboxes are places only through the views.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: memberResponses(api),
	}, handler.handleListPlaces)
}

func (h conversationHandler) handleListPlaces(ctx context.Context, _ *struct{}) (*output[places], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	counts, err := h.conversations.Places(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	listed := places{Places: make([]placeCount, 0, len(counts))}
	for _, count := range counts {
		item := placeCount{AccountID: count.Account, Place: count.Place.String(), Unread: count.Unread}
		if count.Place.View == "" {
			name := count.Name
			item.Name = &name
		}
		listed.Places = append(listed.Places, item)
	}
	return respondData(listed), nil
}

// ========================
// Conversation read
// ========================

const conversationNotFoundType = problemPrefix + "conversation-not-found"

// conversationReadProblem maps the errors of reading one conversation.
func conversationReadProblem(err error) error {
	if errors.Is(err, conversation.ErrNotFound) {
		return newProblem(http.StatusNotFound, conversationNotFoundType, "Conversation not found", "The conversation does not exist. It may have been deleted.")
	}
	return err
}

type conversationInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Conversation ID from a list."`
}

// conversationMessage is one message of a conversation, without its body.
type conversationMessage struct {
	ID       uuid.UUID     `json:"id" format:"uuid"`
	Date     time.Time     `json:"date" doc:"When the provider received it."`
	Subject  string        `json:"subject"`
	From     []mailAddress `json:"from" nullable:"false"`
	ReplyTo  []mailAddress `json:"replyTo" nullable:"false"`
	To       []mailAddress `json:"to" nullable:"false"`
	Cc       []mailAddress `json:"cc" nullable:"false"`
	Preview  string        `json:"preview" doc:"A plain-text excerpt, as the provider made it."`
	Unread   bool          `json:"unread"`
	Starred  bool          `json:"starred"`
	Spam     bool          `json:"spam" doc:"Whether it is in Spam."`
	Trash    bool          `json:"trash" doc:"Whether it is in Trash."`
	LabelIDs []uuid.UUID   `json:"labelIds" nullable:"false" doc:"The account's labels it is in, in a stable order, by name."`
}

type conversationDetail struct {
	Conversation conversationRow       `json:"conversation"`
	Messages     []conversationMessage `json:"messages" nullable:"false" doc:"Oldest first, drafts left out."`
}

func registerConversationRead(api huma.API, handler conversationHandler) {
	responses := memberResponses(api, http.StatusNotFound)
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no conversation with this id."
	notFound.Content[problemContentType].Example = conversationReadProblem(conversation.ErrNotFound)

	huma.Register(api, huma.Operation{
		OperationID: "getConversation",
		Method:      http.MethodGet,
		Path:        conversationsPath + "/{id}",
		Summary:     "Get Conversation",
		Tags:        []string{tagConversations},
		Description: "Returns one of the signed-in account's conversations with its messages' headers, without their bodies.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   responses,
	}, handler.handleGetConversation)
}

func (h conversationHandler) handleGetConversation(ctx context.Context, in *conversationInput) (*output[conversationDetail], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	read, messages, err := h.conversations.Get(ctx, user.ID, in.ID)
	if err != nil {
		return nil, conversationReadProblem(err)
	}
	detail := conversationDetail{Conversation: conversationRowOf(read), Messages: make([]conversationMessage, 0, len(messages))}
	for _, m := range messages {
		detail.Messages = append(detail.Messages, conversationMessage{
			ID:       m.ID,
			Date:     m.Date,
			Subject:  m.Subject,
			From:     mailAddressesOf(m.From),
			ReplyTo:  mailAddressesOf(m.ReplyTo),
			To:       mailAddressesOf(m.To),
			Cc:       mailAddressesOf(m.CC),
			Preview:  m.Preview,
			Unread:   !m.Seen,
			Starred:  m.Flagged,
			Spam:     m.InSpam(),
			Trash:    m.InTrash(),
			LabelIDs: m.LabelIDs(),
		})
	}
	return respondData(detail), nil
}

// ========================
// Conversation action
// ========================

type conversationActionInput struct {
	Body conversationActionBody
}

type conversationActionBody struct {
	Action conversation.Action `json:"action" doc:"What to do: mark read or unread, archive out of the inbox or unarchive back into it, move to Trash or out of it, star or unstar. Each applies to every message of a conversation, drafts too, as the provider's own thread action does."`
	IDs    []uuid.UUID         `json:"ids" nullable:"false" format:"uuid" minItems:"1" maxItems:"100" uniqueItems:"true" doc:"The conversations to act on, by id from a list."`
}

// TransformSchema lists action's values from [conversation.Actions].
func (conversationActionBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["action"].Enum = actionValues()
	return s
}

// actionValues returns every action as a schema's enum lists it.
func actionValues() []any {
	values := make([]any, 0, len(conversation.Actions))
	for _, action := range conversation.Actions {
		values = append(values, string(action))
	}
	return values
}

// refusedConversation is a conversation an action left as it was.
type refusedConversation struct {
	ID     uuid.UUID           `json:"id" format:"uuid"`
	Reason conversation.Reason `json:"reason" enum:"not-found,needs-reconnect,failed" doc:"not-found: the signed-in account has no such conversation, or the provider no longer has it. needs-reconnect: its connected account must be reconnected first. failed: the provider failed, refused, or took too long; trying again may work."`
}

type conversationActionResult struct {
	Conversations []conversationRow     `json:"conversations" nullable:"false" doc:"The conversations the action changed, as the provider answered, in the order of ids. One that no longer exists is in neither list."`
	Refused       []refusedConversation `json:"refused" nullable:"false" doc:"The conversations the action left as they were, in the order of ids."`
	Undo          conversation.Action   `json:"undo" doc:"The action that undoes this one, for the changed conversations."`
}

// TransformSchema lists undo's values from [conversation.Actions].
func (conversationActionResult) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["undo"].Enum = actionValues()
	return s
}

// conversationActionProblem maps the errors of applying an action.
func conversationActionProblem(err error) error {
	if errors.Is(err, errGmailNotConfigured) {
		return newProblem(http.StatusConflict, gmailNotConfiguredType, "Gmail not configured", "Conversations can't be changed until an administrator sets up a Google client.")
	}
	return err
}

func registerConversationAction(api huma.API, handler conversationHandler) {
	responses := memberResponses(api, http.StatusConflict, http.StatusUnprocessableEntity)
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No Google client is configured, so inbok can't reach Gmail."
	conflict.Content[problemContentType].Example = conversationActionProblem(errGmailNotConfigured)

	huma.Register(api, huma.Operation{
		OperationID: "applyConversationAction",
		Method:      http.MethodPost,
		Path:        conversationsPath + "/actions",
		Summary:     "Apply Conversation Action",
		Tags:        []string{tagConversations},
		Description: "Applies one action to the signed-in account's conversations at their provider, then returns them as the provider answered. " +
			"Nothing changes in inbok that the provider did not accept: each conversation is changed or refused on its own, so one answer can hold both. " +
			"Opening a conversation does not mark it read; send read.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleApplyConversationAction)
}

func (h conversationHandler) handleApplyConversationAction(ctx context.Context, in *conversationActionInput) (*output[conversationActionResult], error) {
	if h.actions == nil {
		return nil, conversationActionProblem(errGmailNotConfigured)
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	applied, err := h.actions.Apply(ctx, user.ID, in.Body.Action, in.Body.IDs)
	if err != nil {
		return nil, conversationActionProblem(err)
	}
	result := conversationActionResult{
		Conversations: make([]conversationRow, 0, len(applied.Conversations)),
		Refused:       make([]refusedConversation, 0, len(applied.Refused)),
		Undo:          in.Body.Action.Opposite(),
	}
	for _, row := range applied.Conversations {
		result.Conversations = append(result.Conversations, conversationRowOf(row))
	}
	for _, refused := range applied.Refused {
		result.Refused = append(result.Refused, refusedConversation(refused))
	}
	return respondData(result), nil
}
