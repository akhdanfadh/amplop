package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/conversation"
	"inbok/internal/mailsync"
)

type fakeConversations struct {
	rows        []conversation.Conversation
	messages    []conversation.Message
	providerURL string
	counts      []conversation.PlaceCount
	count       int
	err         error

	userID  int64
	query   conversation.Query
	place   conversation.Place
	account int64
	after   time.Time
	id      uuid.UUID
	calls   int
}

func (f *fakeConversations) List(_ context.Context, userID int64, query conversation.Query) ([]conversation.Conversation, error) {
	f.calls++
	f.userID, f.query = userID, query
	return f.rows, f.err
}

func (f *fakeConversations) CountAfter(_ context.Context, userID int64, place conversation.Place, account int64, after time.Time) (int, error) {
	f.calls++
	f.userID, f.place, f.account, f.after = userID, place, account, after
	return f.count, f.err
}

func (f *fakeConversations) Places(_ context.Context, userID int64) ([]conversation.PlaceCount, error) {
	f.calls++
	f.userID = userID
	return f.counts, f.err
}

func (f *fakeConversations) Get(_ context.Context, userID int64, id uuid.UUID) (conversation.Detail, error) {
	f.calls++
	f.userID, f.id = userID, id
	if f.err != nil {
		return conversation.Detail{}, f.err
	}
	return conversation.Detail{Conversation: f.rows[0], Messages: f.messages, ProviderURL: f.providerURL}, nil
}

func newConversationsAPI(conversations *fakeConversations) *API {
	return New(discard, Services{Conversations: conversations, Session: memberSessions(account.RoleMember)})
}

var (
	reportID      = uuid.MustParse("0b1b2c3d-0000-4000-8000-000000000001")
	receiptsLabel = uuid.MustParse("0b1b2c3d-0000-4000-8000-0000000000aa")
	reportDate    = time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)
	report        = conversation.Conversation{
		ID: reportID, AccountID: 3, LatestDate: reportDate, Subject: "Quarterly report", Preview: "Numbers attached",
		LatestFrom:   []mailsync.Address{{Name: "Asha K", Address: "asha@example.com"}},
		Participants: []mailsync.Address{{Name: "Asha K", Address: "asha@example.com"}, {Address: "alex@example.com"}},
		MessageCount: 2, Unread: true, Flagged: true, InInbox: true, InAllMail: true, LabelIDs: []uuid.UUID{receiptsLabel},
	}
)

const reportJSON = `{"id":"0b1b2c3d-0000-4000-8000-000000000001","accountId":3,"date":"2026-11-09T10:30:00Z",` +
	`"subject":"Quarterly report","preview":"Numbers attached",` +
	`"latestFrom":[{"name":"Asha K","address":"asha@example.com"}],` +
	`"participants":[{"name":"Asha K","address":"asha@example.com"},{"name":"","address":"alex@example.com"}],` +
	`"messageCount":2,"unread":true,"starred":true,"inbox":true,"sent":false,"allMail":true,"spam":false,"trash":false,` +
	`"labelIds":["0b1b2c3d-0000-4000-8000-0000000000aa"]}`

// ========================
// Conversation list
// ========================

func TestListConversations(t *testing.T) {
	conversations := &fakeConversations{rows: []conversation.Conversation{report}}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations?place=inbox", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[`+reportJSON+`]}}`, response.Body.String())
	assert.Equal(t, int64(42), conversations.userID)
	assert.Equal(t, conversation.Query{Place: conversation.Place{View: conversation.ViewInbox}, Limit: conversation.DefaultLimit}, conversations.query,
		"every account, from the newest")
}

func TestListConversationsPage(t *testing.T) {
	conversations := &fakeConversations{rows: []conversation.Conversation{}}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations?place="+receiptsLabel.String()+
		"&account=3&before=2026-11-09T10:30:00.123Z&beforeId="+reportID.String()+"&limit=51", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[]}}`, response.Body.String())
	assert.Equal(t, conversation.Query{
		Place:   conversation.Place{Mailbox: receiptsLabel},
		Account: 3,
		Before:  &conversation.Cursor{Date: time.Date(2026, 11, 9, 10, 30, 0, 123_000_000, time.UTC), ID: reportID},
		Limit:   51,
	}, conversations.query)
}

// A date alone asks for the conversations dated before it: its cursor
// carries the zero ID.
func TestListConversationsOlderThanADate(t *testing.T) {
	conversations := &fakeConversations{rows: []conversation.Conversation{}}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations?place=inbox&before=2026-11-09T10:30:00Z", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, &conversation.Cursor{Date: time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)}, conversations.query.Before)
}

func TestListConversationsInvalid(t *testing.T) {
	for _, c := range []struct {
		name, query, location string
	}{
		{"no place", "", "query.place"},
		{"an unknown view", "place=drafts", "query.place"},
		{"an id alone", "place=inbox&beforeId=" + reportID.String(), "query.before"},
		{"too many", "place=inbox&limit=101", "query.limit"},
		{"no account", "place=inbox&account=0", "query.account"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conversations := &fakeConversations{}
			api := newConversationsAPI(conversations)

			response := requestAuthenticated(api, http.MethodGet, "/conversations?"+c.query, "")

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			errs, ok := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
			require.True(t, ok, response.Body.String())
			require.Len(t, errs, 1)
			assert.Equal(t, c.location, errs[0].(map[string]any)["location"])
			assert.Zero(t, conversations.calls)
		})
	}
}

func TestListConversationsNotFound(t *testing.T) {
	for err, want := range map[error]string{
		conversation.ErrLabelNotFound:   labelNotFoundType,
		conversation.ErrAccountNotFound: connectedAccountNotFoundType,
	} {
		conversations := &fakeConversations{err: err}
		api := newConversationsAPI(conversations)

		response := requestAuthenticated(api, http.MethodGet, "/conversations?place="+receiptsLabel.String(), "")

		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.Equal(t, want, decodeJSON(t, response.Body.Bytes())["type"])
	}
}

// ========================
// New conversation count
// ========================

func TestCountNewConversations(t *testing.T) {
	conversations := &fakeConversations{count: 4}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/new-count?place=starred&account=3&after=2026-11-09T10:30:00Z", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"count":4}}`, response.Body.String())
	assert.Equal(t, conversation.Place{View: conversation.ViewStarred}, conversations.place)
	assert.Equal(t, int64(3), conversations.account)
	assert.Equal(t, reportDate, conversations.after)
}

func TestCountAfterNoAnchor(t *testing.T) {
	conversations := &fakeConversations{}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/new-count?place=inbox", "")

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	assert.Zero(t, conversations.calls)
}

// ========================
// Place list
// ========================

func TestListPlaces(t *testing.T) {
	billsLabel := uuid.MustParse("0b1b2c3d-0000-4000-8000-0000000000cc")
	conversations := &fakeConversations{counts: []conversation.PlaceCount{
		{Account: 3, Place: conversation.Place{View: conversation.ViewInbox}, Unread: 2},
		{Account: 3, Place: conversation.Place{Mailbox: billsLabel}, Name: "Bills"},
		{Account: 3, Place: conversation.Place{Mailbox: receiptsLabel}, Name: "2026", Parent: billsLabel, Unread: 1},
	}}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/places", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"places":[`+
		`{"accountId":3,"place":"inbox","name":null,"parent":null,"unread":2},`+
		`{"accountId":3,"place":"0b1b2c3d-0000-4000-8000-0000000000cc","name":"Bills","parent":null,"unread":0},`+
		`{"accountId":3,"place":"0b1b2c3d-0000-4000-8000-0000000000aa","name":"2026",`+
		`"parent":"0b1b2c3d-0000-4000-8000-0000000000cc","unread":1}]}}`, response.Body.String())
	assert.Equal(t, int64(42), conversations.userID)
}

// ========================
// Conversation read
// ========================

func TestGetConversation(t *testing.T) {
	messageID := uuid.MustParse("0b1b2c3d-0000-4000-8000-0000000000bb")
	conversations := &fakeConversations{
		rows: []conversation.Conversation{report},
		messages: []conversation.Message{{
			ID: messageID, Date: reportDate, Subject: "Quarterly report",
			From:    []mailsync.Address{{Name: "Asha K", Address: "asha@example.com"}},
			ReplyTo: []mailsync.Address{}, To: []mailsync.Address{{Address: "alex@example.com"}}, CC: []mailsync.Address{},
			Preview: "Numbers attached", Flagged: true,
			Mailboxes: []conversation.Mailbox{
				{ID: uuid.New(), Role: mailsync.RoleSent, MadeBy: mailsync.MadeByProvider},
				{ID: uuid.New(), Role: mailsync.RoleTrash, MadeBy: mailsync.MadeByProvider},
				{ID: receiptsLabel, MadeBy: mailsync.MadeByMember},
			},
			Answers:     uuid.MustParse("0b1b2c3d-0000-4000-8000-0000000000dd"),
			Attachments: true,
		}},
		providerURL: "https://mail.google.com/mail/?authuser=alex%40example.com#all/t1",
	}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/"+reportID.String(), "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversation":`+reportJSON+`,"messages":[{`+
		`"id":"0b1b2c3d-0000-4000-8000-0000000000bb","date":"2026-11-09T10:30:00Z","subject":"Quarterly report",`+
		`"from":[{"name":"Asha K","address":"asha@example.com"}],"replyTo":[],"to":[{"name":"","address":"alex@example.com"}],"cc":[],`+
		`"preview":"Numbers attached","unread":true,"starred":true,"spam":false,"trash":true,"sent":true,`+
		`"labelIds":["0b1b2c3d-0000-4000-8000-0000000000aa"],`+
		`"answers":"0b1b2c3d-0000-4000-8000-0000000000dd","attachments":true}],`+
		`"providerUrl":"https://mail.google.com/mail/?authuser=alex%40example.com#all/t1"}}`, response.Body.String())
	assert.Equal(t, reportID, conversations.id)
	assert.Equal(t, int64(42), conversations.userID)
}

// A conversation whose provider has no web app has no address there.
func TestGetConversationNoProviderURL(t *testing.T) {
	api := newConversationsAPI(&fakeConversations{rows: []conversation.Conversation{report}, messages: []conversation.Message{}})

	response := requestAuthenticated(api, http.MethodGet, "/conversations/"+reportID.String(), "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Nil(t, decodeJSON(t, response.Body.Bytes())["data"].(map[string]any)["providerUrl"])
}

func TestGetConversationNotFound(t *testing.T) {
	conversations := &fakeConversations{err: conversation.ErrNotFound}
	api := newConversationsAPI(conversations)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/"+reportID.String(), "")

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.Equal(t, conversationNotFoundType, decodeJSON(t, response.Body.Bytes())["type"])
}

// ========================
// Conversation action
// ========================

type fakeConversationActions struct {
	applied conversation.Applied
	err     error

	userID  int64
	action  conversation.Action
	ids     []uuid.UUID
	changes []conversation.Change
	calls   int
}

func (f *fakeConversationActions) Apply(_ context.Context, userID int64, action conversation.Action, ids []uuid.UUID) (conversation.Applied, error) {
	f.calls++
	f.userID, f.action, f.ids = userID, action, ids
	return f.applied, f.err
}

func (f *fakeConversationActions) ApplyChanges(_ context.Context, userID int64, changes []conversation.Change) (conversation.Applied, error) {
	f.calls++
	f.userID, f.changes = userID, changes
	return f.applied, f.err
}

func newActionsAPI(actions *fakeConversationActions) *API {
	return newActionsCountsAPI(actions, &fakeConversations{})
}

// newActionsCountsAPI returns an API that applies actions with actions, as
// [newActionsAPI] does, and reads places and conversations with
// conversations.
func newActionsCountsAPI(actions *fakeConversationActions, conversations *fakeConversations) *API {
	services := Services{Conversations: conversations, Session: memberSessions(account.RoleMember)}
	if actions != nil {
		services.ConversationActions = actions
	}
	return New(discard, services)
}

var goneID = uuid.MustParse("0b1b2c3d-0000-4000-8000-000000000002")

// An action answers with the conversations it changed, those it refused
// with why, the changes that undo it, and the member's unread counts after
// it.
func TestApplyAction(t *testing.T) {
	actions := &fakeConversationActions{applied: conversation.Applied{
		Conversations: []conversation.Conversation{report},
		Refused:       []conversation.Refusal{{ID: goneID, Reason: conversation.ReasonNeedsReconnect}},
		Undo:          []conversation.Change{{IDs: []uuid.UUID{reportID}, Unread: conversation.MarkCleared}},
	}}
	conversations := &fakeConversations{counts: []conversation.PlaceCount{
		{Account: 3, Place: conversation.Place{View: conversation.ViewInbox}, Unread: 2},
	}}
	api := newActionsCountsAPI(actions, conversations)

	response := requestAuthenticated(api, http.MethodPost, "/conversations/actions",
		`{"action":"unread","ids":["`+reportID.String()+`","`+goneID.String()+`"]}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[`+reportJSON+`],`+
		`"refused":[{"id":"`+goneID.String()+`","reason":"needs-reconnect"}],"undo":[{"ids":["`+reportID.String()+`"],"unread":false}],`+
		`"places":[{"accountId":3,"place":"inbox","name":null,"parent":null,"unread":2}]}}`, response.Body.String())
	assert.Equal(t, int64(42), actions.userID)
	assert.Equal(t, conversation.ActionUnread, actions.action)
	assert.Equal(t, []uuid.UUID{reportID, goneID}, actions.ids)
	assert.Equal(t, int64(42), conversations.userID)
}

// Counting no places, e.g. once the member's last account is gone, answers
// an empty list, which tells the web app to clear its counts.
func TestApplyActionNoPlaces(t *testing.T) {
	actions := &fakeConversationActions{applied: conversation.Applied{
		Conversations: []conversation.Conversation{}, Refused: []conversation.Refusal{{ID: goneID, Reason: conversation.ReasonNotFound}},
	}}
	api := newActionsCountsAPI(actions, &fakeConversations{})

	response := requestAuthenticated(api, http.MethodPost, "/conversations/actions", `{"action":"read","ids":["`+goneID.String()+`"]}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[],"refused":[{"id":"`+goneID.String()+`","reason":"not-found"}],`+
		`"undo":[],"places":[]}}`, response.Body.String())
}

// The action is done at the provider, so counts that fail after it leave
// only the counts out.
func TestApplyActionCountsFail(t *testing.T) {
	actions := &fakeConversationActions{applied: conversation.Applied{
		Conversations: []conversation.Conversation{report}, Refused: []conversation.Refusal{},
	}}
	api := newActionsCountsAPI(actions, &fakeConversations{err: errors.New("database down")})

	response := requestAuthenticated(api, http.MethodPost, "/conversations/actions", `{"action":"spam","ids":["`+reportID.String()+`"]}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[`+reportJSON+`],"refused":[],"undo":[]}}`, response.Body.String())
}

// A bad action or ID list is refused with the field it is in, before the
// service runs.
func TestApplyActionInvalid(t *testing.T) {
	tooMany := make([]string, conversation.MaxActionIDs+1)
	for i := range tooMany {
		tooMany[i] = `"` + uuid.New().String() + `"`
	}
	id := `"` + reportID.String() + `"`
	for _, c := range []struct {
		name, body, location string
	}{
		{"an unknown action", `{"action":"delete","ids":[` + id + `]}`, "body.action"},
		{"no ids", `{"action":"read","ids":[]}`, "body.ids"},
		{"too many ids", `{"action":"read","ids":[` + strings.Join(tooMany, ",") + `]}`, "body.ids"},
		{"a repeated id", `{"action":"read","ids":[` + id + `,` + id + `]}`, "body.ids"},
		{"null ids", `{"action":"read","ids":null}`, "body.ids"},
		{"an id that is no UUID", `{"action":"read","ids":[` + id + `,"nope"]}`, "body.ids[1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			actions := &fakeConversationActions{}
			api := newActionsAPI(actions)

			response := requestAuthenticated(api, http.MethodPost, "/conversations/actions", c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			errs, ok := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
			require.True(t, ok, response.Body.String())
			require.NotEmpty(t, errs)
			assert.Equal(t, c.location, errs[0].(map[string]any)["location"])
			assert.Zero(t, actions.calls)
		})
	}
}

// Without a Google client, inbok cannot reach Gmail, so an action is
// refused whole.
func TestApplyActionNoGmail(t *testing.T) {
	api := newActionsAPI(nil)

	response := requestAuthenticated(api, http.MethodPost, "/conversations/actions", `{"action":"read","ids":["`+reportID.String()+`"]}`)

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Equal(t, gmailNotConfiguredType, decodeJSON(t, response.Body.Bytes())["type"])
}

// ========================
// Conversation changes
// ========================

var workLabel = uuid.MustParse("0b1b2c3d-0000-4000-8000-0000000000f1")

// Changes reach the service as their places and marks, and answer as an
// action does, their own undo included.
func TestChangeConversations(t *testing.T) {
	actions := &fakeConversationActions{applied: conversation.Applied{
		Conversations: []conversation.Conversation{report},
		Refused:       []conversation.Refusal{},
		Undo: []conversation.Change{{
			IDs: []uuid.UUID{reportID}, Add: []conversation.Place{{View: conversation.ViewTrash}},
			Remove: []conversation.Place{{Mailbox: workLabel}}, Starred: conversation.MarkSet,
		}},
	}}
	api := newActionsCountsAPI(actions, &fakeConversations{counts: []conversation.PlaceCount{}})

	response := requestAuthenticated(api, http.MethodPost, "/conversations/changes",
		`{"changes":[{"ids":["`+reportID.String()+`"],"add":["`+workLabel.String()+`"],"remove":["trash"],"starred":false},`+
			`{"ids":["`+goneID.String()+`"],"unread":true}]}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"conversations":[`+reportJSON+`],"refused":[],`+
		`"undo":[{"ids":["`+reportID.String()+`"],"add":["trash"],"remove":["`+workLabel.String()+`"],"starred":true}],"places":[]}}`,
		response.Body.String())
	assert.Equal(t, []conversation.Change{
		{
			IDs: []uuid.UUID{reportID}, Add: []conversation.Place{{Mailbox: workLabel}},
			Remove: []conversation.Place{{View: conversation.ViewTrash}}, Starred: conversation.MarkCleared,
		},
		{IDs: []uuid.UUID{goneID}, Add: []conversation.Place{}, Remove: []conversation.Place{}, Unread: conversation.MarkSet},
	}, actions.changes)
}

// The contract names the places a change takes, so a client learns which
// views a change can name, e.g. which lists a move can leave, from it.
func TestChangePlaceSchema(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()

	place := doc.Components.Schemas.Map()[changePlaceSchemaName]
	add := doc.Components.Schemas.Map()["ConversationChange"].Properties["add"]

	require.NotNil(t, place)
	require.Len(t, place.AnyOf, 2)
	assert.Equal(t, []any{"inbox", "spam", "trash"}, place.AnyOf[0].Enum)
	assert.Equal(t, "uuid", place.AnyOf[1].Format)
	assert.Equal(t, "#/components/schemas/"+changePlaceSchemaName, add.Items.Ref)
}

// A change that is none, names a place twice or one it cannot reach, or
// repeats a conversation is refused with the field it is in, before the
// service runs.
func TestChangeConversationsInvalid(t *testing.T) {
	id := `"` + reportID.String() + `"`
	// One place more than Gmail's 100 labels a side.
	labels := make([]string, 101)
	for i := range labels {
		labels[i] = `"` + uuid.New().String() + `"`
	}
	for _, c := range []struct {
		name, body, location string
	}{
		{"no changes", `{"changes":[]}`, "body.changes"},
		{"a change of nothing", `{"changes":[{"ids":[` + id + `]}]}`, "body.changes[0]"},
		{"a place added and removed", `{"changes":[{"ids":[` + id + `],"add":["inbox"],"remove":["inbox"]}]}`, "body.changes[0]"},
		{"Sent", `{"changes":[{"ids":[` + id + `],"add":["inbox"],"remove":["trash","sent"]}]}`, "body.changes[0].remove[1]"},
		{"no place", `{"changes":[{"ids":[` + id + `],"add":["nope"]}]}`, "body.changes[0].add[0]"},
		{"too many places", `{"changes":[{"ids":[` + id + `],"remove":[` + strings.Join(labels, ",") + `]}]}`, "body.changes[0].remove"},
		{"one conversation twice", `{"changes":[{"ids":[` + id + `],"add":["inbox"]},{"ids":[` + id + `],"unread":true}]}`, "body.changes[1].ids"},
	} {
		t.Run(c.name, func(t *testing.T) {
			actions := &fakeConversationActions{}
			api := newActionsAPI(actions)

			response := requestAuthenticated(api, http.MethodPost, "/conversations/changes", c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			errs, ok := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
			require.True(t, ok, response.Body.String())
			require.NotEmpty(t, errs)
			assert.Equal(t, c.location, errs[0].(map[string]any)["location"])
			assert.Zero(t, actions.calls)
		})
	}
}

// The undo a change answers with is a change the operation takes too, at
// its limits: one adding 100 places is undone by one removing them, as
// sent back unchanged.
func TestChangeConversationsUndoTaken(t *testing.T) {
	places := make([]conversation.Place, 100)
	texts := make([]string, 100)
	for i := range places {
		label := uuid.New()
		places[i] = conversation.Place{Mailbox: label}
		texts[i] = `"` + label.String() + `"`
	}
	actions := &fakeConversationActions{applied: conversation.Applied{
		Conversations: []conversation.Conversation{report},
		Refused:       []conversation.Refusal{},
		Undo:          []conversation.Change{{IDs: []uuid.UUID{reportID}, Remove: places}},
	}}
	api := newActionsCountsAPI(actions, &fakeConversations{counts: []conversation.PlaceCount{}})
	response := requestAuthenticated(api, http.MethodPost, "/conversations/changes",
		`{"changes":[{"ids":["`+reportID.String()+`"],"add":[`+strings.Join(texts, ",")+`]}]}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var answer struct {
		Data struct {
			Undo json.RawMessage `json:"undo"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	response = requestAuthenticated(api, http.MethodPost, "/conversations/changes", `{"changes":`+string(answer.Data.Undo)+`}`)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, places, actions.changes[0].Remove)
}

// A label not in a conversation's account, e.g. deleted since, is not
// found.
func TestChangeConversationsLabelNotFound(t *testing.T) {
	api := newActionsAPI(&fakeConversationActions{err: conversation.ErrLabelNotFound})

	response := requestAuthenticated(api, http.MethodPost, "/conversations/changes",
		`{"changes":[{"ids":["`+reportID.String()+`"],"add":["`+workLabel.String()+`"]}]}`)

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.Equal(t, labelNotFoundType, decodeJSON(t, response.Body.Bytes())["type"])
}
