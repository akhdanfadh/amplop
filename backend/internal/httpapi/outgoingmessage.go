package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/outgoingmessage"
	"inbok/internal/session"
)

const (
	outgoingMessagesPath = "/outgoing-messages"
	messagesPath         = "/messages"
)

const (
	messageNotFoundType              = problemPrefix + "message-not-found"
	outgoingMessageNotFoundType      = problemPrefix + "outgoing-message-not-found"
	sendingAccountNeedsReconnectType = problemPrefix + "sending-account-needs-reconnect"
	notSendableAgainType             = problemPrefix + "not-sendable-again"
	idempotencyKeyReusedType         = problemPrefix + "idempotency-key-reused"
	sendThrottledType                = problemPrefix + "send-throttled"
)

// sendLimitDescription states the member's send limit, which
// outgoingmessage counts.
const sendLimitDescription = "Sending counts toward the signed-in account's limit of 30 messages per 15 minutes and 200 per 24 hours, from all its connected accounts."

type outgoingMessageService interface {
	Create(ctx context.Context, userID int64, draft outgoingmessage.Draft) (outgoingmessage.Message, error)
	Reply(ctx context.Context, userID int64, reply outgoingmessage.Reply) (outgoingmessage.Message, error)
	SendAgain(ctx context.Context, userID int64, messageID uuid.UUID) (outgoingmessage.Message, error)
	Get(ctx context.Context, userID int64, messageID uuid.UUID) (outgoingmessage.Message, error)
	List(ctx context.Context, userID int64) ([]outgoingmessage.Message, error)
}

type outgoingMessageHandler struct {
	service outgoingMessageService
}

func registerOutgoingMessages(api huma.API, service outgoingMessageService) {
	handler := outgoingMessageHandler{service: service}

	registerOutgoingMessageSend(api, handler)
	registerReplyToMessage(api, handler)
	registerOutgoingMessageList(api, handler)
	registerOutgoingMessageRead(api, handler)
	registerSendAgain(api, handler)
}

// outgoingMessageSummary is an outgoing message without its body, as the
// list shows it. Gmail's IDs for it stay on the server.
type outgoingMessageSummary struct {
	ID                 uuid.UUID                `json:"id" format:"uuid"`
	ConnectedAccountID int64                    `json:"connectedAccountId" doc:"The sending account. Disconnecting it deletes the message."`
	From               string                   `json:"from" doc:"The sending account's address when the message was saved."`
	To                 []string                 `json:"to" nullable:"false"`
	Cc                 []string                 `json:"cc" nullable:"false"`
	Bcc                []string                 `json:"bcc" nullable:"false"`
	Subject            string                   `json:"subject"`
	Status             outgoingmessage.Status   `json:"status" enum:"waiting,sending,sent,failed,unknown" doc:"waiting: saved, not started. sending: in progress. sent: Gmail accepted it. failed: not sent, for failure's reason. unknown: it may have reached Gmail, and inbok cannot tell; check the account's Sent folder before sending it again."`
	Failure            *outgoingmessage.Failure `json:"failure" nullable:"true" enum:"refused,sending-limit,needs-reconnect,token-unreadable,deactivated,sender-changed,gave-up" doc:"Why the message failed; null unless status is failed. refused: Gmail refused the message; edit it into a new one. sending-limit: Gmail's daily sending limit for the account. needs-reconnect and token-unreadable: reconnect the account. deactivated: the account's member was deactivated. sender-changed: Gmail names the account by another address, which inbok has now saved. gave-up: Google did not take the message after several tries, e.g. it could not be reached."`
	SentAgainAs        *uuid.UUID               `json:"sentAgainAs" nullable:"true" format:"uuid" doc:"The copy this failed or unknown message was sent again as; null until then."`
	CreatedAt          time.Time                `json:"createdAt"`
	SentAt             *time.Time               `json:"sentAt" nullable:"true" doc:"When Gmail took the message: Gmail's own time for it when inbok found it in Sent after an unanswered send, otherwise when Gmail answered the send. Null unless status is sent."`
}

// TransformSchema lets failure be null.
func (outgoingMessageSummary) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return withNullEnum(s, "failure")
}

// OutgoingMessageSummary names outgoingMessageSummary for embedding.
//
// NOTE: Huma leaves out the fields of an embedded struct whose field name is
// unexported, though encoding/json sends them; embedding this alias gives
// the field an exported name.
type OutgoingMessageSummary = outgoingMessageSummary

// outgoingMessage is an outgoing message with its body.
type outgoingMessage struct {
	OutgoingMessageSummary
	Body string `json:"body" doc:"Plain text."`
}

// TransformSchema lets failure be null.
func (outgoingMessage) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return withNullEnum(s, "failure")
}

// withNullEnum adds null to the enum of s's nullable property name.
//
// NOTE: Huma writes a nullable field's type as [string, null] but leaves
// null out of its enum, which JSON Schema then refuses.
func withNullEnum(s *huma.Schema, name string) *huma.Schema {
	property := s.Properties[name]
	if property != nil && property.Nullable && len(property.Enum) > 0 {
		property.Enum = append(property.Enum, nil)
	}
	return s
}

func outgoingMessageSummaryOf(message outgoingmessage.Message) outgoingMessageSummary {
	summary := outgoingMessageSummary{
		ID:                 message.ID,
		ConnectedAccountID: message.ConnectedAccountID,
		From:               message.From,
		To:                 message.To,
		Cc:                 message.Cc,
		Bcc:                message.Bcc,
		Subject:            message.Subject,
		Status:             message.Status,
		SentAgainAs:        message.SentAgainAs,
		CreatedAt:          message.CreatedAt,
		SentAt:             message.SentAt,
	}
	if message.Failure != "" {
		summary.Failure = &message.Failure
	}
	return summary
}

func outgoingMessageOf(message outgoingmessage.Message) outgoingMessage {
	return outgoingMessage{OutgoingMessageSummary: outgoingMessageSummaryOf(message), Body: message.Body}
}

// outgoingMessageProblem maps the errors the operations share.
func outgoingMessageProblem(err error) error {
	switch {
	case errors.Is(err, outgoingmessage.ErrNotFound):
		return newProblem(http.StatusNotFound, outgoingMessageNotFoundType, "Message not found", "The message does not exist.")
	case errors.Is(err, outgoingmessage.ErrNeedsReconnect):
		return newProblem(http.StatusConflict, sendingAccountNeedsReconnectType, "Account needs reconnecting", "Google stopped accepting the sending account's permission. Reconnect it to send.")
	// The same type: either way the member reconnects the account.
	case errors.Is(err, outgoingmessage.ErrTokenUnreadable):
		return newProblem(http.StatusConflict, sendingAccountNeedsReconnectType, "Account needs reconnecting", "inbok can't open the permission it saved for the sending account. Reconnect it to send.")
	case errors.Is(err, outgoingmessage.ErrThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, sendThrottledType, "Too many messages", "Too many messages were sent recently."), err)
	// The member was deactivated after the session was checked.
	case errors.Is(err, outgoingmessage.ErrDeactivated):
		return unauthenticatedProblem()
	default:
		return err
	}
}

// outgoingMessageResponses documents the failures of an operation on one
// message.
func outgoingMessageResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	responses := memberResponses(api, append([]int{http.StatusNotFound}, statuses...)...)
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no outgoing message with this id."
	notFound.Content[problemContentType].Example = outgoingMessageProblem(outgoingmessage.ErrNotFound)
	return responses
}

// sendResponses adds the failures every send shares to responses.
func sendResponses(responses map[string]*huma.Response) {
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The sending account needs reconnecting: Google stopped accepting its permission, or no key inbok has opens the permission it saved. Nothing is sent from it until its permission is usable again."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"needsReconnect":  {Value: outgoingMessageProblem(outgoingmessage.ErrNeedsReconnect)},
		"tokenUnreadable": {Value: outgoingMessageProblem(outgoingmessage.ErrTokenUnreadable)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many messages were sent recently."
	throttled.Content[problemContentType].Example = outgoingMessageProblem(outgoingmessage.ErrThrottled)
}

// ========================
// Send message
// ========================

type outgoingMessageSendInput struct {
	IdempotencyKeyInput
	Body newOutgoingMessage
}

// idempotencyKeyInput is the Idempotency-Key header of a request that saves
// a message.
type idempotencyKeyInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" pattern:"^(\"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\"|[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})$" doc:"A new UUID for each message the client means to send, reused for every attempt to send that message, as a quoted string per the IETF Idempotency-Key draft, e.g. \"8e03978e-40d5-43e8-bc93-6894a57f9324\"; a bare UUID is accepted too. A repeat with the same key and request returns the message the first saved; with another request it is refused. Keys are the signed-in account's own, and a key lasts as long as its message."`
}

// IdempotencyKeyInput names idempotencyKeyInput for embedding, as
// [OutgoingMessageSummary] does.
type IdempotencyKeyInput = idempotencyKeyInput

// parseKey returns the UUID in the header, or a 422 problem naming it.
func (in idempotencyKeyInput) parseKey() (uuid.UUID, error) {
	key, err := parseIdempotencyKey(in.IdempotencyKey)
	if err != nil {
		invalid := requestProblem(http.StatusUnprocessableEntity)
		invalid.Errors = []inputError{{Location: "header.Idempotency-Key", Message: "Enter a valid value."}}
		return uuid.UUID{}, invalid
	}
	return key, nil
}

// newOutgoingMessage is a message to send. Its limits are inbok's choices
// (ADR006).
type newOutgoingMessage struct {
	ConnectedAccountID int64    `json:"connectedAccountId" minimum:"1" doc:"The sending account, one of the signed-in account's connected accounts."`
	To                 []string `json:"to" nullable:"false" maxItems:"100" format:"email" maxLength:"254" doc:"To, Cc, and Bcc together need at least one address."`
	Cc                 []string `json:"cc" nullable:"false" maxItems:"100" format:"email" maxLength:"254"`
	Bcc                []string `json:"bcc" nullable:"false" maxItems:"100" format:"email" maxLength:"254"`
	Subject            string   `json:"subject" maxLength:"500" doc:"May be empty. No line breaks or other control characters."`
	Body               string   `json:"body" maxLength:"100000" doc:"Plain text."`
}

func registerOutgoingMessageSend(api huma.API, handler outgoingMessageHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "sendMessage",
		Method:      http.MethodPost,
		Path:        outgoingMessagesPath,
		Summary:     "Send Message",
		Tags:        []string{tagOutgoingMessages},
		Description: "Saves a message from one of the signed-in account's connected accounts, waiting to be sent, and returns it. A background job sends it; read it again to follow its status. " +
			sendLimitDescription,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusCreated,
		Responses:     outgoingMessageSendResponses(api),
	}, handler.handleSendMessage)
}

func (h outgoingMessageHandler) handleSendMessage(ctx context.Context, in *outgoingMessageSendInput) (*output[outgoingMessage], error) {
	key, err := in.parseKey()
	if err != nil {
		return nil, err
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	message, err := h.service.Create(ctx, user.ID, outgoingmessage.Draft{
		ConnectedAccountID: in.Body.ConnectedAccountID,
		IdempotencyKey:     key,
		Content: outgoingmessage.Content{
			To:      in.Body.To,
			Cc:      in.Body.Cc,
			Bcc:     in.Body.Bcc,
			Subject: in.Body.Subject,
			Body:    in.Body.Body,
		},
	})
	if err != nil {
		return nil, outgoingMessageSendProblem(err)
	}
	return respondData(outgoingMessageOf(message)), nil
}

// parseIdempotencyKey returns the UUID in an Idempotency-Key header value:
// a Structured Field string, as the IETF draft defines it
// (draft-ietf-httpapi-idempotency-key-header-08, section 2.1), or a bare
// UUID, as Stripe's clients send it. A UUID needs no escape inside quotes, so
// one pair of surrounding quotes is the whole string syntax it can use.
func parseIdempotencyKey(value string) (uuid.UUID, error) {
	if unquoted, ok := strings.CutPrefix(value, `"`); ok {
		value, ok = strings.CutSuffix(unquoted, `"`)
		if !ok {
			return uuid.UUID{}, errUnbalancedQuote
		}
	}
	// Only the hyphenated form, which the contract's pattern states; Parse
	// also takes braces, a URN, and no hyphens.
	if len(value) != len("8e03978e-40d5-43e8-bc93-6894a57f9324") {
		return uuid.UUID{}, errKeyForm
	}
	return uuid.Parse(value)
}

var (
	errUnbalancedQuote = errors.New("idempotency key opens a quote it does not close")
	errKeyForm         = errors.New("idempotency key is not a hyphenated UUID")
)

func outgoingMessageSendResponses(api huma.API) map[string]*huma.Response {
	responses := memberResponses(api, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests)
	sendResponses(responses)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid, the sending account is not one of the signed-in account's, or the Idempotency-Key was used for another request."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"noRecipients":     {Value: outgoingMessageSendProblem(outgoingmessage.ErrNoRecipients)},
		"noAccount":        {Value: outgoingMessageSendProblem(outgoingmessage.ErrAccountNotFound)},
		"keyReused":        {Value: outgoingMessageSendProblem(outgoingmessage.ErrKeyReused)},
		"invalidInput":     {Value: requestProblem(http.StatusUnprocessableEntity)},
		"invalidRecipient": {Value: outgoingMessageSendProblem(&outgoingmessage.RecipientError{Field: outgoingmessage.FieldCc, Index: 2})},
	}
	return responses
}

// outgoingMessageSendProblem maps the errors of sending a new message.
func outgoingMessageSendProblem(err error) error {
	var input inputError
	switch {
	case errors.Is(err, outgoingmessage.ErrKeyReused):
		return newProblem(http.StatusUnprocessableEntity, idempotencyKeyReusedType, "Idempotency key reused", "This Idempotency-Key was used for another request. Use a new key for a new message.")
	case errors.Is(err, outgoingmessage.ErrAccountNotFound):
		input = inputError{Location: "body.connectedAccountId", Message: "Choose one of your connected accounts."}
	case errors.Is(err, outgoingmessage.ErrNoRecipients):
		input = inputError{Location: "body.to", Message: "Add at least one recipient."}
	case errors.Is(err, outgoingmessage.ErrSubjectInvalid):
		input = inputError{Location: "body.subject", Message: "Enter a subject without line breaks or other control characters."}
	case errors.Is(err, outgoingmessage.ErrBodyInvalid):
		input = inputError{Location: "body.body", Message: "Enter text without NUL characters."}
	default:
		if recipient, ok := errors.AsType[*outgoingmessage.RecipientError](err); ok {
			return invalidEmailProblem("body." + string(recipient.Field) + "[" + strconv.Itoa(recipient.Index) + "]")
		}
		if count, ok := errors.AsType[*outgoingmessage.TooManyRecipientsError](err); ok {
			// Huma's own maxItems failure reads the same, so the response
			// does not depend on which check refused the list.
			input = inputError{Location: "body." + string(count.Field), Message: maxItemsMessage(outgoingmessage.MaxRecipients)}
			break
		}
		return outgoingMessageProblem(err)
	}
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{input}
	return invalid
}

// ========================
// Reply to message
// ========================

type replyInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"The synchronized message the reply answers."`
	IdempotencyKeyInput
	Body newReply
}

// newReply is a reply to send: a message to send without a subject, which
// the reply takes from the answered message.
type newReply struct {
	ConnectedAccountID int64    `json:"connectedAccountId" minimum:"1" doc:"The sending account, one of the signed-in account's connected accounts. It need not be the account that holds the answered message."`
	To                 []string `json:"to" nullable:"false" maxItems:"100" format:"email" maxLength:"254" doc:"To, Cc, and Bcc together need at least one address."`
	Cc                 []string `json:"cc" nullable:"false" maxItems:"100" format:"email" maxLength:"254"`
	Bcc                []string `json:"bcc" nullable:"false" maxItems:"100" format:"email" maxLength:"254"`
	Body               string   `json:"body" maxLength:"100000" doc:"Plain text."`
}

func registerReplyToMessage(api huma.API, handler outgoingMessageHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "replyToMessage",
		Method:      http.MethodPost,
		Path:        messagesPath + "/{id}/replies",
		Summary:     "Reply to Message",
		Tags:        []string{tagOutgoingMessages},
		Description: "Saves a reply to one of the signed-in account's messages, from one of its connected accounts, waiting to be sent, and returns it as an outgoing message. " +
			"Its subject is the answered message's, cut at 500 characters, with \"Re: \" added when it has none and still fits, and it carries the In-Reply-To and References headers that thread it when the answered message has a valid Message-ID. " +
			"Sent from the account that holds the answered message, it asks Gmail for that message's thread; from another account, it carries only the headers and starts a new thread in that account's Gmail. " +
			sendLimitDescription,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusCreated,
		Responses:     replyResponses(api),
	}, handler.handleReplyToMessage)
}

func (h outgoingMessageHandler) handleReplyToMessage(ctx context.Context, in *replyInput) (*output[outgoingMessage], error) {
	key, err := in.parseKey()
	if err != nil {
		return nil, err
	}
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	message, err := h.service.Reply(ctx, user.ID, outgoingmessage.Reply{
		ConnectedAccountID: in.Body.ConnectedAccountID,
		IdempotencyKey:     key,
		AnsweredMessageID:  in.ID,
		To:                 in.Body.To,
		Cc:                 in.Body.Cc,
		Bcc:                in.Body.Bcc,
		Body:               in.Body.Body,
	})
	if err != nil {
		return nil, replyProblem(err)
	}
	return respondData(outgoingMessageOf(message)), nil
}

func replyResponses(api huma.API) map[string]*huma.Response {
	responses := memberResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests)
	sendResponses(responses)

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no message with this id, e.g. a sync deleted it."
	notFound.Content[problemContentType].Example = replyProblem(outgoingmessage.ErrAnsweredNotFound)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid, the sending account is not one of the signed-in account's, or the Idempotency-Key was used for another request."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"noRecipients": {Value: replyProblem(outgoingmessage.ErrNoRecipients)},
		"noAccount":    {Value: replyProblem(outgoingmessage.ErrAccountNotFound)},
		"keyReused":    {Value: replyProblem(outgoingmessage.ErrKeyReused)},
		"invalidInput": {Value: requestProblem(http.StatusUnprocessableEntity)},
	}
	return responses
}

// replyProblem maps the errors of replying to a message.
func replyProblem(err error) error {
	if errors.Is(err, outgoingmessage.ErrAnsweredNotFound) {
		return newProblem(http.StatusNotFound, messageNotFoundType, "Message not found", "The message does not exist. It may have been deleted.")
	}
	return outgoingMessageSendProblem(err)
}

// ========================
// Outgoing message list
// ========================

type outgoingMessages struct {
	Messages []outgoingMessageSummary `json:"messages" nullable:"false" doc:"The newest 50, newest first."`
}

func registerOutgoingMessageList(api huma.API, handler outgoingMessageHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listOutgoingMessages",
		Method:      http.MethodGet,
		Path:        outgoingMessagesPath,
		Summary:     "List Outgoing Messages",
		Tags:        []string{tagOutgoingMessages},
		Description: "Lists the messages the signed-in account sent from inbok, newest first, without their bodies, including those that failed or whose outcome is unknown.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   memberResponses(api),
	}, handler.handleListOutgoingMessages)
}

func (h outgoingMessageHandler) handleListOutgoingMessages(ctx context.Context, _ *struct{}) (*output[outgoingMessages], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	messages, err := h.service.List(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	listed := outgoingMessages{Messages: make([]outgoingMessageSummary, 0, len(messages))}
	for _, message := range messages {
		listed.Messages = append(listed.Messages, outgoingMessageSummaryOf(message))
	}
	return respondData(listed), nil
}

// ========================
// Outgoing message read
// ========================

type outgoingMessageInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Outgoing message ID."`
}

func registerOutgoingMessageRead(api huma.API, handler outgoingMessageHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getOutgoingMessage",
		Method:      http.MethodGet,
		Path:        outgoingMessagesPath + "/{id}",
		Summary:     "Get Outgoing Message",
		Tags:        []string{tagOutgoingMessages},
		Description: "Returns one of the signed-in account's outgoing messages with its body and status.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   outgoingMessageResponses(api),
	}, handler.handleGetOutgoingMessage)
}

func (h outgoingMessageHandler) handleGetOutgoingMessage(ctx context.Context, in *outgoingMessageInput) (*output[outgoingMessage], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	message, err := h.service.Get(ctx, user.ID, in.ID)
	if err != nil {
		return nil, outgoingMessageProblem(err)
	}
	return respondData(outgoingMessageOf(message)), nil
}

// ========================
// Send message again
// ========================

func registerSendAgain(api huma.API, handler outgoingMessageHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "sendMessageAgain",
		Method:      http.MethodPost,
		Path:        outgoingMessagesPath + "/{id}/send-again",
		Summary:     "Send Message Again",
		Tags:        []string{tagOutgoingMessages},
		Description: "Saves a copy of a failed or unknown message, waiting to be sent from the same account's current address, and returns the copy. " +
			"The message then names the copy in sentAgainAs, and sending it again returns the same copy, so it is sent again at most once. " +
			sendLimitDescription,
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusCreated,
		Responses:     sendAgainResponses(api),
	}, handler.handleSendMessageAgain)
}

func (h outgoingMessageHandler) handleSendMessageAgain(ctx context.Context, in *outgoingMessageInput) (*output[outgoingMessage], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	message, err := h.service.SendAgain(ctx, user.ID, in.ID)
	if err != nil {
		return nil, sendAgainProblem(err)
	}
	return respondData(outgoingMessageOf(message)), nil
}

func sendAgainResponses(api huma.API) map[string]*huma.Response {
	responses := outgoingMessageResponses(api, http.StatusConflict, http.StatusTooManyRequests)
	sendResponses(responses)

	// A second 409 meaning, with its own type.
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description += " Or the message is waiting, sending, or sent, so only a failed or unknown one is sent again."
	conflict.Content[problemContentType].Examples["notSendableAgain"] = &huma.Example{Value: sendAgainProblem(outgoingmessage.ErrNotSendableAgain)}
	return responses
}

// sendAgainProblem maps the errors of sending a message again.
func sendAgainProblem(err error) error {
	if errors.Is(err, outgoingmessage.ErrNotSendableAgain) {
		return newProblem(http.StatusConflict, notSendableAgainType, "Message not sendable again", "Only a failed message, or one whose outcome is unknown, can be sent again.")
	}
	return outgoingMessageProblem(err)
}
