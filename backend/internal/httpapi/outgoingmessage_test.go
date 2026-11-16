package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/connectedaccount"
	"inbok/internal/outgoingmessage"
	"inbok/internal/session"
)

type fakeOutgoingMessages struct {
	message  outgoingmessage.Message
	messages []outgoingmessage.Message
	err      error

	userID    int64
	draft     outgoingmessage.Draft
	reply     outgoingmessage.Reply
	messageID uuid.UUID
	calls     int
}

func (f *fakeOutgoingMessages) Reply(_ context.Context, userID int64, reply outgoingmessage.Reply) (outgoingmessage.Message, error) {
	f.calls++
	f.userID, f.reply = userID, reply
	return f.message, f.err
}

func (f *fakeOutgoingMessages) Create(_ context.Context, userID int64, draft outgoingmessage.Draft) (outgoingmessage.Message, error) {
	f.calls++
	f.userID, f.draft = userID, draft
	return f.message, f.err
}

func (f *fakeOutgoingMessages) SendAgain(_ context.Context, userID int64, messageID uuid.UUID) (outgoingmessage.Message, error) {
	f.calls++
	f.userID, f.messageID = userID, messageID
	return f.message, f.err
}

func (f *fakeOutgoingMessages) Get(_ context.Context, userID int64, messageID uuid.UUID) (outgoingmessage.Message, error) {
	f.calls++
	f.userID, f.messageID = userID, messageID
	return f.message, f.err
}

func (f *fakeOutgoingMessages) List(_ context.Context, userID int64) ([]outgoingmessage.Message, error) {
	f.calls++
	f.userID = userID
	return f.messages, f.err
}

var (
	lunchID      = uuid.MustParse("0a1b2c3d-0000-4000-8000-000000000001")
	lunchCopyID  = uuid.MustParse("0a1b2c3d-0000-4000-8000-000000000002")
	lunchKey     = uuid.MustParse("0a1b2c3d-0000-4000-8000-0000000000aa")
	lunchCreated = time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)
	lunch        = outgoingmessage.Message{
		ID: lunchID, ConnectedAccountID: 3, From: "alex@example.com",
		Content: outgoingmessage.Content{
			To: []string{"Ann@example.com"}, Cc: []string{}, Bcc: []string{"cat@example.com"},
			Subject: "Lunch", Body: "Are you free?",
		},
		InbokMessageID: "<abc@example.com>", Status: outgoingmessage.StatusWaiting,
		ProviderMessageID: "gmail-secret-id", CreatedAt: lunchCreated,
		Account: connectedaccount.Account{ID: 3, Status: connectedaccount.StatusUsable},
	}
)

const lunchSummaryJSON = `"id":"0a1b2c3d-0000-4000-8000-000000000001","connectedAccountId":3,"from":"alex@example.com",` +
	`"to":["Ann@example.com"],"cc":[],"bcc":["cat@example.com"],"subject":"Lunch","status":"waiting",` +
	`"failure":null,"sentAgainAs":null,"createdAt":"2026-11-09T10:30:00Z","sentAt":null,"allowedActions":[]`

const lunchRequest = `{"connectedAccountId":3,"to":["Ann@example.com"],"cc":[],"bcc":["cat@example.com"],"subject":"Lunch","body":"Are you free?"}`

// requestSend sends body to Send Message with key as its Idempotency-Key,
// or none when key is empty.
func requestSend(api *API, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/outgoing-messages", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func newOutgoingMessagesAPI(messages *fakeOutgoingMessages) *API {
	return New(discard, Services{OutgoingMessages: messages, Connections: &fakeConnections{}, Session: memberSessions(account.RoleMember)})
}

// ========================
// Send message
// ========================

// The message comes back with its body, and without Gmail's IDs.
func TestSendMessage(t *testing.T) {
	messages := &fakeOutgoingMessages{message: lunch}
	api := newOutgoingMessagesAPI(messages)

	response := requestSend(api, lunchKey.String(), lunchRequest)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{`+lunchSummaryJSON+`,"body":"Are you free?"}}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "gmail-secret-id")
	assert.NotContains(t, response.Body.String(), "abc@example.com", "inbok's Message-ID stays on the server")
	assert.Equal(t, int64(42), messages.userID)
	assert.Equal(t, outgoingmessage.Draft{ConnectedAccountID: 3, IdempotencyKey: lunchKey, Content: lunch.Content}, messages.draft)
}

// The key may be the IETF draft's quoted string or a bare UUID, in either
// letter case; each names the same key, and the contract's pattern says so.
func TestSendMessageKeyForms(t *testing.T) {
	for _, key := range []string{
		`"` + lunchKey.String() + `"`,
		lunchKey.String(),
		strings.ToUpper(lunchKey.String()),
	} {
		t.Run(key, func(t *testing.T) {
			messages := &fakeOutgoingMessages{message: lunch}

			response := requestSend(newOutgoingMessagesAPI(messages), key, lunchRequest)

			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			assert.Equal(t, lunchKey, messages.draft.IdempotencyKey)
		})
	}
}

// The contract's pattern accepts exactly what the handler parses, so a client
// that checks the contract sends no key the server refuses.
func TestIdempotencyKeyPattern(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	var pattern *regexp.Regexp
	for _, param := range doc.Paths[outgoingMessagesPath].Post.Parameters {
		if param.Name == "Idempotency-Key" {
			pattern = regexp.MustCompile(param.Schema.Pattern)
		}
	}
	require.NotNil(t, pattern)
	key := lunchKey.String()
	for _, value := range []string{key, `"` + key + `"`, strings.ToUpper(key),
		`"` + key, key + `"`, `""` + key + `""`, "{" + key + "}", " " + key, strings.ReplaceAll(key, "-", "")} {
		_, err := parseIdempotencyKey(value)
		assert.Equal(t, err == nil, pattern.MatchString(value), value)
	}
}

// A missing or malformed key, or input over the contract's limits, is
// refused before the service.
func TestSendMessageInvalid(t *testing.T) {
	hundredOne := `["` + strings.Repeat(`a@example.com","`, 100) + `a@example.com"]`
	for _, c := range []struct {
		name, key, body, location, message string
	}{
		{"no key", "", lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key not a UUID", "lunch-1", lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key opens a quote", `"0a1b2c3d-0000-4000-8000-0000000000aa`, lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key closes a quote", `0a1b2c3d-0000-4000-8000-0000000000aa"`, lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key in braces", "{0a1b2c3d-0000-4000-8000-0000000000aa}", lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key as a URN", "urn:uuid:0a1b2c3d-0000-4000-8000-0000000000aa", lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"key without hyphens", "0a1b2c3d0000400080000000000000aa", lunchRequest, "header.Idempotency-Key", "Enter a valid value."},
		{"too many recipients", lunchKey.String(), strings.Replace(lunchRequest, `"cc":[]`, `"cc":`+hundredOne, 1), "body.cc", "Use at most 100 items."},
		{"recipient not an address", lunchKey.String(), strings.Replace(lunchRequest, `"cc":[]`, `"cc":["Ann <ann@example.com>"]`, 1), "body.cc[0]", "Enter an email address, like name@example.com."},
		{"subject too long", lunchKey.String(), strings.Replace(lunchRequest, `"Lunch"`, `"`+strings.Repeat("s", 501)+`"`, 1), "body.subject", "Use at most 500 characters."},
	} {
		t.Run(c.name, func(t *testing.T) {
			messages := &fakeOutgoingMessages{message: lunch}
			api := newOutgoingMessagesAPI(messages)

			response := requestSend(api, c.key, c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+"validation-error", problem["type"])
			assert.Equal(t, []any{map[string]any{"location": c.location, "message": c.message}}, problem["errors"])
			assert.Zero(t, messages.calls)
		})
	}
}

func TestSendMessageRefused(t *testing.T) {
	throttled := &attemptlimit.Error{Err: outgoingmessage.ErrThrottled, Wait: 90 * time.Second}
	for _, c := range []struct {
		name       string
		err        error
		code       int
		typ        string
		retryAfter string
		input      map[string]any
	}{
		{"needs reconnect", outgoingmessage.ErrNeedsReconnect, http.StatusConflict, sendingAccountNeedsReconnectType, "", nil},
		{"token unreadable", outgoingmessage.ErrTokenUnreadable, http.StatusConflict, sendingAccountNeedsReconnectType, "", nil},
		{"throttled", throttled, http.StatusTooManyRequests, sendThrottledType, "90", nil},
		{"key reused", outgoingmessage.ErrKeyReused, http.StatusUnprocessableEntity, idempotencyKeyReusedType, "", nil},
		{"deactivated meanwhile", outgoingmessage.ErrDeactivated, http.StatusUnauthorized, unauthenticatedType, "", nil},
		{"other member's account", outgoingmessage.ErrAccountNotFound, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.connectedAccountId", "message": "Choose one of your connected accounts."}},
		{"no recipients", outgoingmessage.ErrNoRecipients, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.to", "message": "Add at least one recipient."}},
		{"recipient", &outgoingmessage.RecipientError{Field: outgoingmessage.FieldBcc, Index: 4}, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.bcc[4]", "message": "Enter an email address, like name@example.com."}},
		{"too many", &outgoingmessage.TooManyRecipientsError{Field: outgoingmessage.FieldTo}, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.to", "message": "Use at most 100 items."}},
		{"subject", outgoingmessage.ErrSubjectInvalid, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.subject", "message": "Enter a subject without line breaks or other control characters."}},
		{"body", outgoingmessage.ErrBodyInvalid, http.StatusUnprocessableEntity, problemPrefix + "validation-error", "",
			map[string]any{"location": "body.body", "message": "Enter text without NUL characters."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newOutgoingMessagesAPI(&fakeOutgoingMessages{err: c.err})

			response := requestSend(api, lunchKey.String(), lunchRequest)

			require.Equal(t, c.code, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, c.typ, problem["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
			if c.input != nil {
				assert.Equal(t, []any{c.input}, problem["errors"])
			}
		})
	}
}

// ========================
// Outgoing message list
// ========================

func TestListOutgoingMessages(t *testing.T) {
	failed := lunch
	failed.Status, failed.Failure, failed.SentAgainAs = outgoingmessage.StatusFailed, outgoingmessage.FailureGaveUp, &lunchCopyID
	stale := lunch
	stale.Status, stale.Failure = outgoingmessage.StatusFailed, outgoingmessage.FailureNeedsReconnect
	stale.Account.Status = connectedaccount.StatusNeedsReconnect
	for _, c := range []struct {
		name     string
		messages []outgoingmessage.Message
		want     string
	}{
		{"none", []outgoingmessage.Message{}, `{"data":{"messages":[]}}`},
		{"one", []outgoingmessage.Message{lunch}, `{"data":{"messages":[{` + lunchSummaryJSON + `}]}}`},
		{"failed and sent again", []outgoingmessage.Message{failed}, `{"data":{"messages":[{` +
			strings.NewReplacer(`"status":"waiting"`, `"status":"failed"`, `"failure":null`, `"failure":"gave-up"`,
				`"sentAgainAs":null`, `"sentAgainAs":"`+lunchCopyID.String()+`"`,
				`"allowedActions":[]`, `"allowedActions":["send-again"]`).Replace(lunchSummaryJSON) + `}]}}`},
		{"failed, its account needs reconnecting", []outgoingmessage.Message{stale}, `{"data":{"messages":[{` +
			strings.NewReplacer(`"status":"waiting"`, `"status":"failed"`, `"failure":null`, `"failure":"needs-reconnect"`).Replace(lunchSummaryJSON) + `}]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			messages := &fakeOutgoingMessages{messages: c.messages}
			api := newOutgoingMessagesAPI(messages)

			response := requestAuthenticated(api, http.MethodGet, "/outgoing-messages", "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, c.want, response.Body.String())
			assert.Equal(t, int64(42), messages.userID)
		})
	}
}

// Without Gmail set up nothing can be sent again, as Send Message Again
// refuses it.
func TestListOutgoingMessagesWithoutGmail(t *testing.T) {
	failed := lunch
	failed.Status, failed.Failure = outgoingmessage.StatusFailed, outgoingmessage.FailureGaveUp
	api := New(discard, Services{OutgoingMessages: &fakeOutgoingMessages{messages: []outgoingmessage.Message{failed}}, Session: memberSessions(account.RoleMember)})

	response := requestAuthenticated(api, http.MethodGet, "/outgoing-messages", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"allowedActions":[]`)
}

// ========================
// Outgoing message read
// ========================

func TestGetOutgoingMessage(t *testing.T) {
	sent := lunch
	sentAt := lunchCreated.Add(time.Minute)
	sent.Status, sent.SentAt = outgoingmessage.StatusSent, &sentAt
	messages := &fakeOutgoingMessages{message: sent}
	api := newOutgoingMessagesAPI(messages)

	response := requestAuthenticated(api, http.MethodGet, "/outgoing-messages/"+lunchID.String(), "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	want := strings.NewReplacer(`"status":"waiting"`, `"status":"sent"`, `"sentAt":null`, `"sentAt":"2026-11-09T10:31:00Z"`).Replace(lunchSummaryJSON)
	assert.JSONEq(t, `{"data":{`+want+`,"body":"Are you free?"}}`, response.Body.String())
	assert.Equal(t, lunchID, messages.messageID)
}

func TestGetOutgoingMessageMissing(t *testing.T) {
	api := newOutgoingMessagesAPI(&fakeOutgoingMessages{err: outgoingmessage.ErrNotFound})

	response := requestAuthenticated(api, http.MethodGet, "/outgoing-messages/"+lunchID.String(), "")

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.Equal(t, outgoingMessageNotFoundType, decodeJSON(t, response.Body.Bytes())["type"])
}

// ========================
// Send message again
// ========================

func TestSendMessageAgain(t *testing.T) {
	copied := lunch
	copied.ID = lunchCopyID
	messages := &fakeOutgoingMessages{message: copied}
	api := newOutgoingMessagesAPI(messages)

	response := requestAuthenticated(api, http.MethodPost, "/outgoing-messages/"+lunchID.String()+"/send-again", "")

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.Equal(t, lunchCopyID.String(), decodeJSON(t, response.Body.Bytes())["data"].(map[string]any)["id"])
	assert.Equal(t, lunchID, messages.messageID)
	assert.Equal(t, int64(42), messages.userID)
}

func TestSendMessageAgainRefused(t *testing.T) {
	throttled := &attemptlimit.Error{Err: outgoingmessage.ErrThrottled, Wait: 90 * time.Second}
	for _, c := range []struct {
		name       string
		err        error
		code       int
		typ        string
		retryAfter string
	}{
		{"not failed", outgoingmessage.ErrNotSendableAgain, http.StatusConflict, notSendableAgainType, ""},
		{"needs reconnect", outgoingmessage.ErrNeedsReconnect, http.StatusConflict, sendingAccountNeedsReconnectType, ""},
		{"token unreadable", outgoingmessage.ErrTokenUnreadable, http.StatusConflict, sendingAccountNeedsReconnectType, ""},
		{"not found", outgoingmessage.ErrNotFound, http.StatusNotFound, outgoingMessageNotFoundType, ""},
		{"throttled", throttled, http.StatusTooManyRequests, sendThrottledType, "90"},
		{"deactivated meanwhile", outgoingmessage.ErrDeactivated, http.StatusUnauthorized, unauthenticatedType, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newOutgoingMessagesAPI(&fakeOutgoingMessages{err: c.err})

			response := requestAuthenticated(api, http.MethodPost, "/outgoing-messages/"+lunchID.String()+"/send-again", "")

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
		})
	}
}

// ========================
// Reply to message
// ========================

// answeredMessageID is the synchronized message the reply tests answer.
var answeredMessageID = uuid.MustParse("0a1b2c3d-0000-4000-8000-0000000000bb")

const replyRequest = `{"connectedAccountId":3,"to":["Ann@example.com"],"cc":[],"bcc":["cat@example.com"],"body":"Are you free?"}`

// requestReply sends body to Reply to Message for answeredMessageID with
// key as its Idempotency-Key.
func requestReply(api *API, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/messages/"+answeredMessageID.String()+"/replies", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// A reply comes back as the message it saved, with the subject the server
// gave it.
func TestReplyToMessage(t *testing.T) {
	saved := lunch
	saved.Subject = "Re: Lunch"
	messages := &fakeOutgoingMessages{message: saved}
	api := newOutgoingMessagesAPI(messages)

	response := requestReply(api, lunchKey.String(), replyRequest)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{`+strings.Replace(lunchSummaryJSON, `"subject":"Lunch"`, `"subject":"Re: Lunch"`, 1)+`,"body":"Are you free?"}}`, response.Body.String())
	assert.Equal(t, int64(42), messages.userID)
	assert.Equal(t, outgoingmessage.Reply{
		ConnectedAccountID: 3,
		IdempotencyKey:     lunchKey,
		AnsweredMessageID:  answeredMessageID,
		To:                 []string{"Ann@example.com"},
		Cc:                 []string{},
		Bcc:                []string{"cat@example.com"},
		Body:               "Are you free?",
	}, messages.reply)
}

// A reply takes no subject, and its key is checked as a send's.
func TestReplyToMessageInvalid(t *testing.T) {
	for _, c := range []struct {
		name, key, body, location string
	}{
		{"subject", lunchKey.String(), strings.Replace(replyRequest, `"body"`, `"subject":"Lunch","body"`, 1), "body.subject"},
		{"key not a UUID", "lunch-1", replyRequest, "header.Idempotency-Key"},
		{"key opens a quote", `"0a1b2c3d-0000-4000-8000-0000000000aa`, replyRequest, "header.Idempotency-Key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			messages := &fakeOutgoingMessages{message: lunch}
			api := newOutgoingMessagesAPI(messages)

			response := requestReply(api, c.key, c.body)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			problem := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, problemPrefix+"validation-error", problem["type"])
			errors, ok := problem["errors"].([]any)
			require.True(t, ok)
			require.Len(t, errors, 1)
			assert.Equal(t, c.location, errors[0].(map[string]any)["location"])
			assert.Zero(t, messages.calls)
		})
	}
}

// A reply fails as a send does, and with a 404 when the answered message is
// not the member's or is gone.
func TestReplyToMessageRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"answered message not found", outgoingmessage.ErrAnsweredNotFound, http.StatusNotFound, messageNotFoundType},
		{"needs reconnect", outgoingmessage.ErrNeedsReconnect, http.StatusConflict, sendingAccountNeedsReconnectType},
		{"key reused", outgoingmessage.ErrKeyReused, http.StatusUnprocessableEntity, idempotencyKeyReusedType},
		{"other member's account", outgoingmessage.ErrAccountNotFound, http.StatusUnprocessableEntity, problemPrefix + "validation-error"},
		{"throttled", &attemptlimit.Error{Err: outgoingmessage.ErrThrottled, Wait: time.Minute}, http.StatusTooManyRequests, sendThrottledType},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newOutgoingMessagesAPI(&fakeOutgoingMessages{err: c.err})

			response := requestReply(api, lunchKey.String(), replyRequest)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, c.typ, decodeJSON(t, response.Body.Bytes())["type"])
		})
	}
}

// Without a Google client no worker sends Gmail, so every send is refused
// before anything is saved, rather than waiting forever.
func TestSendRefusedWithoutGoogleClient(t *testing.T) {
	for _, c := range []struct {
		name string
		send func(api *API) *httptest.ResponseRecorder
	}{
		{"send", func(api *API) *httptest.ResponseRecorder { return requestSend(api, lunchKey.String(), lunchRequest) }},
		{"reply", func(api *API) *httptest.ResponseRecorder { return requestReply(api, lunchKey.String(), replyRequest) }},
		{"send again", func(api *API) *httptest.ResponseRecorder {
			return requestAuthenticated(api, http.MethodPost, "/outgoing-messages/"+lunchID.String()+"/send-again", "")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			messages := &fakeOutgoingMessages{message: lunch}
			api := New(discard, Services{OutgoingMessages: messages, Session: memberSessions(account.RoleMember)})

			response := c.send(api)

			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.Equal(t, gmailNotConfiguredType, decodeJSON(t, response.Body.Bytes())["type"])
			assert.Zero(t, messages.calls)
		})
	}
}

// Every outgoing message operation is member level, and the documented
// problems carry the types the web compares.
func TestOutgoingMessagesOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, c := range []struct {
		operation *huma.Operation
		id        string
		statuses  []int
	}{
		{doc.Paths[outgoingMessagesPath].Post, "sendMessage", []int{http.StatusCreated, http.StatusUnauthorized, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests}},
		{doc.Paths[outgoingMessagesPath].Get, "listOutgoingMessages", []int{http.StatusOK, http.StatusUnauthorized}},
		{doc.Paths[outgoingMessagesPath+"/{id}"].Get, "getOutgoingMessage", []int{http.StatusOK, http.StatusUnauthorized, http.StatusNotFound}},
		{doc.Paths[outgoingMessagesPath+"/{id}/send-again"].Post, "sendMessageAgain", []int{http.StatusCreated, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests}},
		{doc.Paths[messagesPath+"/{id}/replies"].Post, "replyToMessage", []int{http.StatusCreated, http.StatusUnauthorized, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests}},
	} {
		op := c.operation
		require.NotNil(t, op, c.id)
		assert.Equal(t, c.id, op.OperationID)
		assert.Equal(t, []string{tagOutgoingMessages}, op.Tags)
		assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
		for _, status := range c.statuses {
			assert.Contains(t, op.Responses, strconv.Itoa(status), c.id)
		}
	}

	// A client draws one control per value, so the contract names them all.
	for _, name := range []string{"OutgoingMessageSummary", "OutgoingMessage"} {
		schema := doc.Components.Schemas.Map()[name]
		require.NotNil(t, schema, name)
		assert.Contains(t, schema.Required, "allowedActions", name)
		assert.Equal(t, []any{"send-again"}, schema.Properties["allowedActions"].Items.Enum, name)
	}

	types := problemTypes(doc)
	for _, typ := range []string{outgoingMessageNotFoundType, sendingAccountNeedsReconnectType, gmailNotConfiguredType, notSendableAgainType, idempotencyKeyReusedType, sendThrottledType, messageNotFoundType} {
		assert.Contains(t, types, typ)
	}
}
