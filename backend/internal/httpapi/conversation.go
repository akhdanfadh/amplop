package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"amplop/internal/connectedaccount"
	"amplop/internal/conversation"
	"amplop/internal/mailsync"
	"amplop/internal/session"
)

const (
	conversationsPath = "/conversations"

	labelNotFoundType = problemPrefix + "label-not-found"
)

// conversationService reads the member's conversations.
type conversationService interface {
	List(ctx context.Context, userID int64, query conversation.Query) ([]conversation.Conversation, error)
	CountAfter(ctx context.Context, userID int64, place conversation.Place, account int64, after time.Time) (int, error)
	Places(ctx context.Context, userID int64) ([]conversation.PlaceCount, error)
	Get(ctx context.Context, userID int64, id uuid.UUID) (conversation.Detail, error)
}

// conversationActionService applies the member's actions and changes to
// their conversations.
type conversationActionService interface {
	Apply(ctx context.Context, userID int64, action conversation.Action, ids []uuid.UUID) (conversation.Applied, error)
	ApplyChanges(ctx context.Context, userID int64, changes []conversation.Change) (conversation.Applied, error)
}

type conversationHandler struct {
	conversations conversationService
	// actions is nil when no Google client is configured.
	actions conversationActionService
	logger  *slog.Logger
}

func registerConversations(api huma.API, logger *slog.Logger, conversations conversationService, actions conversationActionService) {
	handler := conversationHandler{conversations: conversations, actions: actions, logger: logger}

	registerConversationList(api, handler)
	registerNewConversationCount(api, handler)
	registerPlaceList(api, handler)
	registerConversationRead(api, handler)
	registerConversationAction(api, handler)
	registerConversationChanges(api, handler)
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

// placeMessage is the input message of a place that is neither a view nor
// a mailbox ID, and msgExpectedPlace the Huma message an input's own check
// gives for one, which [publicInputError] words as placeMessage.
const (
	placeMessage     = "Choose a view or one of your labels."
	msgExpectedPlace = "expected a view or a label id"
)

// placeOf parses text as a place, or returns a 422 naming the place
// parameter.
func placeOf(text string) (conversation.Place, error) {
	place, err := conversation.ParsePlace(text)
	if err != nil {
		invalid := requestProblem(http.StatusUnprocessableEntity)
		invalid.Errors = []inputError{{Location: "query.place", Message: placeMessage}}
		return conversation.Place{}, invalid
	}
	return place, nil
}

// placeProblem maps the errors of reading a place's conversations.
func placeProblem(err error) error {
	switch {
	case errors.Is(err, conversation.ErrLabelNotFound):
		return labelNotFoundProblem()
	case errors.Is(err, conversation.ErrAccountNotFound):
		return connectedAccountProblem(connectedaccount.ErrNotFound)
	default:
		return err
	}
}

// placeResponses documents the failures of an operation on places, and
// those of more statuses.
func placeResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	responses := memberResponses(api, append([]int{http.StatusNotFound}, statuses...)...)
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no label with the place's id, or no connected account with the account filter's id."
	// OpenAPI allows example or examples, not both (3.1, Media Type Object).
	notFound.Content[problemContentType].Example = nil
	notFound.Content[problemContentType].Examples = map[string]*huma.Example{
		"label":   {Value: placeProblem(conversation.ErrLabelNotFound)},
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
	AccountID int64      `json:"accountId"`
	Place     string     `json:"place" doc:"A view's name or a label's id, as the place of List Conversations takes it."`
	Name      *string    `json:"name" nullable:"true" doc:"A label's own name under its parent, e.g. Projects for a Gmail label Work/Projects under its label Work. It keeps what the provider names the levels the member has no label for, so it can hold the provider's separator: Home/Bills with no label Home, at the top; 2026/Q4 for Work/Projects/2026/Q4 under Projects with no label Work/Projects/2026. Null for a view."`
	Parent    *uuid.UUID `json:"parent" nullable:"true" format:"uuid" doc:"The place, a label's id, that a label nests under, so no client parses a provider's names; null for a view and a label at the top."`
	Unread    int        `json:"unread" minimum:"0" doc:"How many of its conversations are unread."`
}

type places struct {
	Places []placeCount `json:"places" nullable:"false" doc:"Every place of each connected account, in a stable order: the accounts oldest first, each with its views, inbox, starred, sent, all, spam, and trash, then its labels by their own names. The web app orders them for display. Gmail's own labels, e.g. its inbox categories, are not places."`
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
	return respondData(places{Places: placeCountsOf(counts)}), nil
}

// placeCountsOf returns counts as List Places answers them.
func placeCountsOf(counts []conversation.PlaceCount) []placeCount {
	listed := make([]placeCount, 0, len(counts))
	for _, count := range counts {
		item := placeCount{AccountID: count.Account, Place: count.Place.String(), Unread: count.Unread}
		if count.Place.View == "" {
			name := count.Name
			item.Name = &name
		}
		if count.Parent != uuid.Nil() {
			item.Parent = &count.Parent
		}
		listed = append(listed, item)
	}
	return listed
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
	Sent     bool          `json:"sent" doc:"Whether it is in Sent: its account sent it, from its own address or an alias."`
	LabelIDs []uuid.UUID   `json:"labelIds" nullable:"false" doc:"The account's labels it is in, in a stable order, by name."`
	// Named answers, not JMAP's inReplyTo, which lists Message-IDs (RFC
	// 8621 section 4.1.3): this is the message itself.
	Answers     *uuid.UUID `json:"answers" nullable:"true" format:"uuid" doc:"The message of this conversation it answers, by its In-Reply-To header; null for none, or one this conversation does not hold."`
	Attachments bool       `json:"attachments" doc:"Whether it has an attachment. amplop knows only once it has fetched the message's content: the last 90 days' in the background, others when opened. False until then."`
}

type conversationDetail struct {
	Conversation conversationRow       `json:"conversation"`
	Messages     []conversationMessage `json:"messages" nullable:"false" doc:"Oldest first, drafts left out."`
	ProviderURL  *string               `json:"providerUrl" nullable:"true" format:"uri" doc:"Where the provider's own web app shows the conversation, signed in as its connected account, e.g. in Gmail; null for a provider without one. Gmail documents no such address, so it may stop working."`
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
	read, err := h.conversations.Get(ctx, user.ID, in.ID)
	if err != nil {
		return nil, conversationReadProblem(err)
	}
	detail := conversationDetail{Conversation: conversationRowOf(read.Conversation), Messages: make([]conversationMessage, 0, len(read.Messages))}
	if read.ProviderURL != "" {
		detail.ProviderURL = &read.ProviderURL
	}
	for _, m := range read.Messages {
		var answers *uuid.UUID
		if m.Answers != uuid.Nil() {
			answers = &m.Answers
		}
		detail.Messages = append(detail.Messages, conversationMessage{
			ID:          m.ID,
			Date:        m.Date,
			Subject:     m.Subject,
			From:        mailAddressesOf(m.From),
			ReplyTo:     mailAddressesOf(m.ReplyTo),
			To:          mailAddressesOf(m.To),
			Cc:          mailAddressesOf(m.CC),
			Preview:     m.Preview,
			Unread:      !m.Seen,
			Starred:     m.Flagged,
			Spam:        m.InSpam(),
			Trash:       m.InTrash(),
			Sent:        m.InSent(),
			LabelIDs:    m.LabelIDs(),
			Answers:     answers,
			Attachments: m.Attachments,
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
	Action conversation.Action `json:"action" doc:"What to do: mark read or unread, archive out of the inbox or unarchive back into it, move to Trash (out of the inbox) or out of it (back into the inbox), star or unstar, report spam (out of the inbox) or not spam (back into it). star and unread mark a conversation's newest message, the newest it did not send when it has one, as the provider's own web app does. Every other action applies to every message of a conversation, as the provider's own thread action does."`
	IDs    []uuid.UUID         `json:"ids" nullable:"false" format:"uuid" minItems:"1" maxItems:"25" uniqueItems:"true" doc:"The conversations to act on, by id from a list."`
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
	Conversations []conversationRow     `json:"conversations" nullable:"false" doc:"The conversations it changed, as the provider answered, in the order of ids. One that no longer exists is in neither list."`
	Refused       []refusedConversation `json:"refused" nullable:"false" doc:"The conversations it left as they were, in the order of ids."`
	Undo          []conversationChange  `json:"undo" nullable:"false" doc:"The changes that put each changed conversation back as it was before, as its row showed it; send them as they are to Change Conversations. Conversations needing the same change share one. A conversation it changed nothing of is in none."`
	// A pointer, so a successful count of no places is [] and only a failed
	// one is left out.
	Places *[]placeCount `json:"places,omitempty" nullable:"false" doc:"Every place with its unread count after it, as List Places answers; left out when counting failed."`
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
	conflict.Description = "No Google client is configured, so amplop can't reach Gmail."
	conflict.Content[problemContentType].Example = conversationActionProblem(errGmailNotConfigured)

	huma.Register(api, huma.Operation{
		OperationID: "applyConversationAction",
		Method:      http.MethodPost,
		Path:        conversationsPath + "/actions",
		Summary:     "Apply Conversation Action",
		Tags:        []string{tagConversations},
		Description: "Applies one action to the signed-in account's conversations at their provider, then returns them as the provider answered. " +
			"Nothing changes in amplop that the provider did not accept: each conversation is changed or refused on its own, so one answer can hold both. " +
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
	return h.respondApplied(ctx, user.ID, applied), nil
}

// respondApplied returns applied as userID's answer, with the unread
// counts after it.
func (h conversationHandler) respondApplied(ctx context.Context, userID int64, applied conversation.Applied) *output[conversationActionResult] {
	result := conversationActionResult{
		Conversations: make([]conversationRow, 0, len(applied.Conversations)),
		Refused:       make([]refusedConversation, 0, len(applied.Refused)),
		Undo:          make([]conversationChange, 0, len(applied.Undo)),
	}
	for _, change := range applied.Undo {
		result.Undo = append(result.Undo, conversationChangeOf(change))
	}
	for _, row := range applied.Conversations {
		result.Conversations = append(result.Conversations, conversationRowOf(row))
	}
	for _, refused := range applied.Refused {
		result.Refused = append(result.Refused, refusedConversation(refused))
	}
	// The change is done at the provider, so a count that fails is left out
	// rather than failing the answer; the next List Places brings it.
	counts, err := h.conversations.Places(ctx, userID)
	if err != nil {
		h.logger.WarnContext(ctx, "place counts after a conversation change failed", "err", err)
		return respondData(result)
	}
	listed := placeCountsOf(counts)
	result.Places = &listed
	return respondData(result)
}

// ========================
// Conversation changes
// ========================

type conversationChangesInput struct {
	Body conversationChangesBody
}

type conversationChangesBody struct {
	Changes []conversationChange `json:"changes" nullable:"false" minItems:"1" maxItems:"25" doc:"The changes, each to its own conversations; together they name at most 25, each once."`
}

// conversationChange is a change of conversations as it is: an action's
// undo, or one to send.
type conversationChange struct {
	IDs []uuid.UUID `json:"ids" nullable:"false" format:"uuid" minItems:"1" maxItems:"25" uniqueItems:"true" doc:"The conversations to change, by id from a list."`
	// Gmail takes at most 100 labels to add and 100 to remove per
	// threads.modify (users.threads.modify reference). The same limit on
	// both, so an undo, which swaps them, is a change this takes too; the
	// Google client sends cleared marks in a call of their own when the
	// labels leave no room for them.
	Add     []changePlace `json:"add,omitempty" nullable:"false" maxItems:"100" uniqueItems:"true" doc:"The places to add each conversation's messages to: inbox, spam, trash, or one of the member's labels in its account, by id."`
	Remove  []changePlace `json:"remove,omitempty" nullable:"false" maxItems:"100" uniqueItems:"true" doc:"The places to take each conversation's messages out of, named as in add."`
	Starred *bool         `json:"starred,omitempty" doc:"true stars each conversation's newest message, the newest it did not send when it has one, as star does; false unstars every message. Left out, the star stays as it is."`
	Unread  *bool         `json:"unread,omitempty" doc:"true marks each conversation's newest message unread, as unread does; false marks every message read. Left out, it stays as it is."`
}

// changePlaceSchemaName names the contract's places a change can name.
const changePlaceSchemaName = "ChangePlace"

// changePlace is a place a [conversationChange] names: one of
// [conversation.ChangeViews], or the id of one of the member's labels.
type changePlace string

// Schema describes changePlace once, under its own name, as one of
// [conversation.ChangeViews] or a label's id, so a client learns from the
// contract which views a change can name, e.g. which lists a move can
// leave.
func (changePlace) Schema(r huma.Registry) *huma.Schema {
	if _, ok := r.Map()[changePlaceSchemaName]; !ok {
		views := make([]any, 0, len(conversation.ChangeViews))
		for _, view := range conversation.ChangeViews {
			views = append(views, string(view))
		}
		r.Map()[changePlaceSchemaName] = &huma.Schema{
			Description: "A place a change can add a conversation to or take it out of: a view, inbox, spam, or trash, or the id of one of the member's labels from List Places. The other views follow: Sent from what a conversation's account sent, All mail from Spam and Trash, Starred from the star.",
			AnyOf: []*huma.Schema{
				{Type: huma.TypeString, Enum: views},
				{Type: huma.TypeString, Format: "uuid"},
			},
		}
	}
	return &huma.Schema{Ref: "#/components/schemas/" + changePlaceSchemaName}
}

// conversationChangeOf returns change as the API sends it.
func conversationChangeOf(change conversation.Change) conversationChange {
	names := func(places []conversation.Place) []changePlace {
		texts := make([]changePlace, 0, len(places))
		for _, place := range places {
			texts = append(texts, changePlace(place.String()))
		}
		return texts
	}
	mark := func(m conversation.Mark) *bool {
		if m == conversation.MarkKept {
			return nil
		}
		set := m == conversation.MarkSet
		return &set
	}
	return conversationChange{
		IDs:     change.IDs,
		Add:     names(change.Add),
		Remove:  names(change.Remove),
		Starred: mark(change.Starred),
		Unread:  mark(change.Unread),
	}
}

// parseChanges parses body's changes, or returns a 422 naming each field
// that is not one.
func parseChanges(body conversationChangesBody) ([]conversation.Change, error) {
	var errs []inputError
	places := func(location string, texts []changePlace) []conversation.Place {
		parsed := make([]conversation.Place, 0, len(texts))
		for j, text := range texts {
			place, err := conversation.ParsePlace(string(text))
			// A view the others decide, e.g. Sent, is no place to name:
			// the service's own rule, asked of this place alone, so the
			// error points at it.
			if err != nil || errors.Is(conversation.ValidateChange(conversation.Change{Add: []conversation.Place{place}}), conversation.ErrUnchangeablePlace) {
				errs = append(errs, inputError{Location: fmt.Sprintf("%s[%d]", location, j), Message: "Choose inbox, spam, trash, or one of your labels."})
				continue
			}
			parsed = append(parsed, place)
		}
		return parsed
	}
	mark := func(set *bool) conversation.Mark {
		switch {
		case set == nil:
			return conversation.MarkKept
		case *set:
			return conversation.MarkSet
		default:
			return conversation.MarkCleared
		}
	}
	changes := make([]conversation.Change, 0, len(body.Changes))
	seen := make(map[uuid.UUID]bool)
	total := 0
	for i, in := range body.Changes {
		location := fmt.Sprintf("body.changes[%d]", i)
		change := conversation.Change{
			IDs:     in.IDs,
			Add:     places(location+".add", in.Add),
			Remove:  places(location+".remove", in.Remove),
			Starred: mark(in.Starred),
			Unread:  mark(in.Unread),
		}
		// An unreachable place was named above, at its item, and left out.
		switch err := conversation.ValidateChange(change); {
		case len(change.Add) < len(in.Add) || len(change.Remove) < len(in.Remove):
		case errors.Is(err, conversation.ErrEmptyChange):
			errs = append(errs, inputError{Location: location, Message: "Change at least one place or mark."})
		case errors.Is(err, conversation.ErrPlaceTwice):
			errs = append(errs, inputError{Location: location, Message: "Name each place once, to add or to remove."})
		}
		for _, id := range in.IDs {
			if seen[id] {
				errs = append(errs, inputError{Location: location + ".ids", Message: "Name each conversation in one change only."})
				break
			}
			seen[id] = true
		}
		total += len(in.IDs)
		changes = append(changes, change)
	}
	if total > conversation.MaxActionIDs {
		errs = append(errs, inputError{Location: "body.changes", Message: fmt.Sprintf("Change at most %d conversations at once.", conversation.MaxActionIDs)})
	}
	if len(errs) > 0 {
		invalid := requestProblem(http.StatusUnprocessableEntity)
		invalid.Errors = errs
		return nil, invalid
	}
	return changes, nil
}

// conversationChangesProblem maps the errors of applying changes.
func conversationChangesProblem(err error) error {
	if errors.Is(err, conversation.ErrLabelNotFound) {
		return placeProblem(err)
	}
	return conversationActionProblem(err)
}

func registerConversationChanges(api huma.API, handler conversationHandler) {
	responses := memberResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity)
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No Google client is configured, so amplop can't reach Gmail."
	conflict.Content[problemContentType].Example = conversationActionProblem(errGmailNotConfigured)
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "A label is not one of the member's in a conversation's account, e.g. deleted since."
	notFound.Content[problemContentType].Example = placeProblem(conversation.ErrLabelNotFound)

	huma.Register(api, huma.Operation{
		OperationID: "changeConversations",
		Method:      http.MethodPost,
		Path:        conversationsPath + "/changes",
		Summary:     "Change Conversations",
		Tags:        []string{tagConversations},
		Description: "Changes the signed-in account's conversations at their provider as asked, by none of the provider's own rules, e.g. an action's undo, then returns them as the provider answered, as Apply Conversation Action does. " +
			"Each change adds its conversations to places and takes them out of others: the inbox, Spam, Trash, or the member's labels; Sent, All mail, and Starred follow. " +
			"A provider whose messages sit in one folder each moves them: such a change removes one place and adds one.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleChangeConversations)
}

func (h conversationHandler) handleChangeConversations(ctx context.Context, in *conversationChangesInput) (*output[conversationActionResult], error) {
	if h.actions == nil {
		return nil, conversationActionProblem(errGmailNotConfigured)
	}
	changes, err := parseChanges(in.Body)
	if err != nil {
		return nil, err
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	applied, err := h.actions.ApplyChanges(ctx, user.ID, changes)
	if err != nil {
		return nil, conversationChangesProblem(err)
	}
	return h.respondApplied(ctx, user.ID, applied), nil
}
