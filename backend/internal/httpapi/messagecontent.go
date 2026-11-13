package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
	"inbok/internal/messagecontent"
	"inbok/internal/session"
)

const (
	messagesPath = "/messages"

	messageNotFoundType     = problemPrefix + "message-not-found"
	messagePartNotFoundType = problemPrefix + "message-part-not-found"
	messageUnreadableType   = problemPrefix + "message-unreadable"
	mailProviderBusyType    = problemPrefix + "mail-provider-busy"
)

// providerBusyRetry is the Retry-After of a request the mail provider
// refused for now, inbok's choice: Gmail's backoff reaches its 32 seconds
// within about a minute, while a member waits for the message.
const providerBusyRetry = 30 * time.Second

// partSendTime is how long sending one part may take, inbok's choice: it
// replaces the server's 30-second write limit, so 25 MB, Gmail's largest
// attachment, reaches a client at about 0.7 Mbit/s, while a client that
// stalls holds its connection for at most this long.
const partSendTime = 5 * time.Minute

// inlineImageTypes are the media types the part route lets a browser show,
// as images every browser decodes and none runs: an SVG holds scripts.
var inlineImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// The part route's answers that a browser must not show as a page.
const (
	// Nothing in the part runs or loads, even opened in a tab.
	partPolicy = "sandbox; default-src 'none'"
	// The fallback for a media type a sender wrote that Go cannot format.
	partFallbackType = "application/octet-stream"
)

// messageContentService reads members' messages' content, e.g.
// [messagecontent.Reader].
type messageContentService interface {
	View(ctx context.Context, userID int64, messageID uuid.UUID, partURL messagecontent.PartURL) (messagecontent.View, error)
	Body(ctx context.Context, userID int64, messageID uuid.UUID, path []int, partURL messagecontent.PartURL) (messagecontent.HTMLBody, error)
	Part(ctx context.Context, userID int64, messageID uuid.UUID, path []int) (messagecontent.Part, io.ReadCloser, error)
}

type messageContentHandler struct {
	contents messageContentService
}

func registerMessageContent(api huma.API, contents messageContentService) {
	handler := messageContentHandler{contents: contents}
	registerMessageContentRead(api, handler)
	registerMessageBodyRead(api, handler)
	registerMessagePartRead(api, handler)
}

// messageNotFound returns the problem for a message the member does not
// have. The content operations and Reply to Message share it, so the
// type has one meaning, title, and detail.
func messageNotFound() error {
	return newProblem(http.StatusNotFound, messageNotFoundType, "Message not found", "The message does not exist. It may have been deleted.")
}

// messageContentProblem maps the errors of reading a message's content.
func messageContentProblem(err error) error {
	switch {
	// The provider no longer having it reads the same to the member.
	case errors.Is(err, messagecontent.ErrMessageNotFound), errors.Is(err, messagecontent.ErrGone),
		errors.Is(err, connectedaccount.ErrNotFound):
		return messageNotFound()
	case errors.Is(err, messagecontent.ErrPartNotFound):
		return newProblem(http.StatusNotFound, messagePartNotFoundType, "Part not found", "The message has no such part to show.")
	case errors.Is(err, messagecontent.ErrTooLarge), errors.Is(err, messagecontent.ErrUnreadable),
		errors.Is(err, messagecontent.ErrTooManyParts):
		return newProblem(http.StatusBadGateway, messageUnreadableType, "Message can't be shown", "This message can't be read from its mail provider.")
	case errors.Is(err, connectedaccount.ErrNeedsReconnect), errors.Is(err, connectedaccount.ErrTokenUnreadable):
		return newProblem(http.StatusConflict, accountNeedsReconnectType, "Account needs reconnecting", "The message's account no longer opens. Reconnect it to load the message.")
	case errors.Is(err, mailsync.ErrProviderBusy), errors.Is(err, connectedaccount.ErrRefreshFailed),
		errors.Is(err, connectedaccount.ErrCredentialChanged):
		return huma.ErrorWithHeaders(mailProviderBusyProblem(), http.Header{retryAfterHeader: {retryAfterSeconds(providerBusyRetry)}})
	default:
		return err
	}
}

// mailProviderBusyProblem returns the 503 for a mail provider that refuses
// requests for now; [messageContentProblem] adds its Retry-After.
func mailProviderBusyProblem() *problem {
	return newProblem(http.StatusServiceUnavailable, mailProviderBusyType, "Mail provider busy", "The message's mail provider is not answering right now. Try again shortly.")
}

// messageContentResponses returns the problem responses of an operation
// that reads a message's content, fetching it from its provider first
// when it is not stored, with notFound as the 404's example.
func messageContentResponses(api huma.API, notFound error) map[string]*huma.Response {
	responses := memberResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusBadGateway, http.StatusServiceUnavailable)
	examples := map[int]struct {
		description string
		example     error
	}{
		http.StatusNotFound:           {"The signed-in account has no such message, or its mail provider no longer has it.", messageContentProblem(notFound)},
		http.StatusConflict:           {"The message's account needs reconnecting before its content can be fetched.", messageContentProblem(connectedaccount.ErrNeedsReconnect)},
		http.StatusBadGateway:         {"The mail provider's answer is too large or cannot be read; asking again gives the same.", messageContentProblem(messagecontent.ErrUnreadable)},
		http.StatusServiceUnavailable: {"The mail provider refuses requests for now.", mailProviderBusyProblem()},
	}
	for status, example := range examples {
		response := responses[strconv.Itoa(status)]
		response.Description = example.description
		response.Content[problemContentType].Example = example.example
	}
	return responses
}

type messageInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Message ID from a conversation."`
}

// messagePartInput names one part of a message.
//
// NOTE: Huma reads no parameter of an embedded struct whose type is
// unexported, so each input repeats the message ID.
type messagePartInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Message ID from a conversation."`
	// A place in a tree of at most 1000 parts, the parser's limit, has at
	// most 1999 characters, 0 and then .0 at each of 999 levels, and no
	// index past 999.
	Part string `path:"part" maxLength:"1999" pattern:"^0(\\.(0|[1-9][0-9]{0,2}))*$" doc:"The part's place in the message, from the message's content: 0 is the whole message, 0.1 its second part, and so on."`
}

// partPathText returns path as a [messagePartInput.Part].
func partPathText(path []int) string {
	var text strings.Builder
	text.WriteString("0")
	for _, i := range path {
		text.WriteString(".")
		text.WriteString(strconv.Itoa(i))
	}
	return text.String()
}

// parsePartPath returns the path text names, as [messagePartInput.Part]'s
// pattern allows it.
func parsePartPath(text string) []int {
	path := []int{}
	for _, index := range strings.Split(text, ".")[1:] {
		// The pattern allows at most three digits.
		i, _ := strconv.Atoi(index)
		path = append(path, i)
	}
	return path
}

// messageURLs builds the URLs of one message's content.
type messageURLs struct {
	id uuid.UUID
}

func (u messageURLs) base() string {
	return apiPrefix + messagesPath + "/" + u.id.String()
}

// body returns the URL of the HTML body at path.
func (u messageURLs) body(path []int, remote messagecontent.Remote) string {
	url := u.base() + "/bodies/" + partPathText(path)
	if remote == messagecontent.RemoteAllowed {
		url += "?remote=" + remoteAllowed
	}
	return url
}

// part returns the URL of the part at path.
func (u messageURLs) part(path []int) string {
	return u.base() + "/parts/" + partPathText(path)
}

// ========================
// Message content read
// ========================

// messageContent is what the view of a message shows.
type messageContent struct {
	Bodies      []shownBody         `json:"bodies" nullable:"false" doc:"Shown one after another, in message order."`
	Remote      string              `json:"remote" enum:"none,blocked" doc:"none: no HTML body refers to remote images. blocked: one does, and they are left out unless loaded from its remoteUrl."`
	Attachments []messageAttachment `json:"attachments" nullable:"false" doc:"Parts offered as downloads, in message order: files, and images the HTML does not show."`
}

type shownBody struct {
	PartID    string  `json:"partId" doc:"The part's place in the message."`
	Type      string  `json:"type" enum:"html,text"`
	URL       string  `json:"url" doc:"html: the body, sanitized, with remote images left out, for a frame. text: the whole text as a file."`
	RemoteURL *string `json:"remoteUrl" nullable:"true" doc:"html: the body with its remote images loaded. Null for text."`
	Text      *string `json:"text" nullable:"true" doc:"text: the text, its start only when clipped. Null for html."`
	Clipped   bool    `json:"clipped" doc:"Whether text holds only the start of a long text, about its first 100 KB; url has the rest. False for html."`
}

type messageAttachment struct {
	PartID    string `json:"partId" doc:"The part's place in the message."`
	Filename  string `json:"filename" doc:"The name the sender gave it; empty when none."`
	MediaType string `json:"mediaType" doc:"As the sender declared it, e.g. application/pdf."`
	Size      int64  `json:"size" doc:"Its size in bytes, as the provider reports it."`
	URL       string `json:"url" doc:"Downloads it."`
}

func registerMessageContentRead(api huma.API, handler messageContentHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getMessageContent",
		Method:      http.MethodGet,
		Path:        messagesPath + "/{id}/content",
		Summary:     "Get Message Content",
		Tags:        []string{tagMessages},
		Description: "Returns what the view of one of the signed-in account's messages shows: its bodies, whether its HTML refers to remote images, and its attachments. " +
			"Content not stored yet, e.g. of a message older than the 90 days kept in the background, is fetched from the mail provider first, which can take a few seconds.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: messageContentResponses(api, messagecontent.ErrMessageNotFound),
	}, handler.handleGetMessageContent)
}

func (h messageContentHandler) handleGetMessageContent(ctx context.Context, in *messageInput) (*output[messageContent], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	urls := messageURLs{id: in.ID}
	view, err := h.contents.View(ctx, user.ID, in.ID, urls.part)
	if err != nil {
		return nil, messageContentProblem(err)
	}
	content := messageContent{
		Bodies:      make([]shownBody, 0, len(view.Bodies)),
		Remote:      "none",
		Attachments: make([]messageAttachment, 0, len(view.Downloads)),
	}
	if view.Blocked {
		content.Remote = "blocked"
	}
	for _, body := range view.Bodies {
		shown := shownBody{PartID: partPathText(body.Path), Type: "text", URL: urls.part(body.Path), Clipped: body.Clipped}
		if body.Text == nil {
			remote := urls.body(body.Path, messagecontent.RemoteAllowed)
			shown.Type, shown.URL, shown.RemoteURL = "html", urls.body(body.Path, messagecontent.RemoteBlocked), &remote
		} else {
			text := string(body.Text)
			shown.Text = &text
		}
		content.Bodies = append(content.Bodies, shown)
	}
	for _, download := range view.Downloads {
		content.Attachments = append(content.Attachments, messageAttachment{
			PartID:    partPathText(download.Path),
			Filename:  download.Part.Filename,
			MediaType: download.Part.MediaType,
			Size:      download.Part.Size,
			URL:       urls.part(download.Path),
		})
	}
	return respondData(content), nil
}

// ========================
// Message body read
// ========================

// remoteAllowed is the remote query value that loads remote images.
const remoteAllowed = "allowed"

type messageBodyInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Message ID from a conversation."`
	// As in [messagePartInput.Part].
	Part   string `path:"part" maxLength:"1999" pattern:"^0(\\.(0|[1-9][0-9]{0,2}))*$" doc:"The body's partId from the message's content."`
	Remote string `query:"remote" enum:"blocked,allowed" default:"blocked" doc:"allowed loads the remote images the body refers to, telling their sender the message was read; blocked leaves them out."`
}

func registerMessageBodyRead(api huma.API, handler messageContentHandler) {
	responses := messageContentResponses(api, messagecontent.ErrPartNotFound)
	responses["200"] = &huma.Response{
		Description: "The body as an HTML document, sanitized: no script, form, or frame, and only the images allowed. Its Content-Security-Policy refuses anything else.",
		Content:     map[string]*huma.MediaType{"text/html": {Schema: &huma.Schema{Type: huma.TypeString}}},
	}
	huma.Register(api, huma.Operation{
		OperationID: "getMessageBody",
		Method:      http.MethodGet,
		Path:        messagesPath + "/{id}/bodies/{part}",
		Summary:     "Get Message Body",
		Tags:        []string{tagMessages},
		Description: "Returns an HTML body of one of the signed-in account's messages as a document for a frame on inbok's own pages; its url in the message's content names it. " +
			"Only a frame loads it.",
		Metadata: map[string]any{
			accessKey:    session.AccessMember,
			fetchDestKey: []string{fetchDestIframe},
		},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleGetMessageBody)
}

func (h messageContentHandler) handleGetMessageBody(ctx context.Context, in *messageBodyInput) (*huma.StreamResponse, error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	urls := messageURLs{id: in.ID}
	body, err := h.contents.Body(ctx, user.ID, in.ID, parsePartPath(in.Part), urls.part)
	if err != nil {
		return nil, messageContentProblem(err)
	}
	defer body.Content.Close()
	policy := body.Policy
	policy.Remote = messagecontent.RemoteBlocked
	if in.Remote == remoteAllowed {
		policy.Remote = messagecontent.RemoteAllowed
	}
	return messageBody(body.Content, policy)
}

// ========================
// Message part read
// ========================

func registerMessagePartRead(api huma.API, handler messageContentHandler) {
	responses := messageContentResponses(api, messagecontent.ErrPartNotFound)
	responses["200"] = &huma.Response{
		Description: "The part's bytes, as the sender sent them; a text body's in UTF-8. PNG, JPEG, GIF, and WebP images come inline; anything else as a download a browser runs nothing of.",
		Content:     map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}},
	}
	huma.Register(api, huma.Operation{
		OperationID: "getMessagePart",
		Method:      http.MethodGet,
		Path:        messagesPath + "/{id}/parts/{part}",
		Summary:     "Download Message Part",
		Tags:        []string{tagMessages},
		Description: "Returns one part of one of the signed-in account's messages: an attachment, an image its HTML shows, or a text body whole. " +
			"A part not stored yet, e.g. an attachment never downloaded, is fetched from the mail provider first.",
		Metadata: map[string]any{
			accessKey: session.AccessMember,
			// An image in a body's frame, a part opened in a tab, and a
			// download link and the web app's own calls, which both send
			// empty (measured in Chromium 158).
			fetchDestKey: []string{fetchDestImage, fetchDestDocument, fetchDestEmpty},
		},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleGetMessagePart)
}

func (h messageContentHandler) handleGetMessagePart(ctx context.Context, in *messagePartInput) (*huma.StreamResponse, error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	part, content, err := h.contents.Part(ctx, user.ID, in.ID, parsePartPath(in.Part))
	if err != nil {
		return nil, messageContentProblem(err)
	}
	return &huma.StreamResponse{Body: func(ctx huma.Context) {
		defer content.Close()
		inline := slices.Contains(inlineImageTypes, part.MediaType)
		disposition := "attachment"
		if inline {
			disposition = "inline"
		} else {
			ctx.SetHeader("Content-Security-Policy", partPolicy)
		}
		if part.Filename != "" {
			// RFC 6266 with RFC 8187's encoding for a name beyond ASCII.
			disposition = mime.FormatMediaType(disposition, map[string]string{"filename": part.Filename})
		}
		ctx.SetHeader("Content-Type", partType(part))
		ctx.SetHeader("Content-Disposition", disposition)
		// A browser takes the type as sent, never guessing a script.
		ctx.SetHeader("X-Content-Type-Options", "nosniff")
		// The member's mail, which no cache may keep.
		ctx.SetHeader("Cache-Control", "no-store")

		// A large part to a slow client outlasts the server's write limit.
		// A server without write deadlines, e.g. in tests, has nothing to
		// extend.
		_ = http.NewResponseController(humagin.Unwrap(ctx).Writer).SetWriteDeadline(time.Now().Add(partSendTime))
		ctx.SetStatus(http.StatusOK)
		// The answer has begun, so its status can no longer change: cutting
		// it off is how a browser learns the file is incomplete, rather than
		// saving the start of it as a finished download. withRecovery hands
		// this value on to net/http, which aborts the response.
		if _, err := io.Copy(ctx.BodyWriter(), content); err != nil {
			panic(http.ErrAbortHandler)
		}
	}}, nil
}

// partType returns the Content-Type of part's stored content: a text body
// is stored in UTF-8, any other part as received, in its declared charset.
func partType(part messagecontent.Part) string {
	params := map[string]string{}
	switch {
	case part.Kind == messagecontent.KindText:
		params["charset"] = "utf-8"
	case part.Charset != "":
		params["charset"] = part.Charset
	}
	value := mime.FormatMediaType(part.MediaType, params)
	if value == "" {
		return partFallbackType
	}
	return value
}
