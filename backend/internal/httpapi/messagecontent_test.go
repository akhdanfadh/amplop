package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
	"amplop/internal/messagecontent"
)

// contentMessage is the message the content tests read.
var contentMessage = uuid.MustParse("0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0b")

// fakeContents answers with view, body, or part and its content, or err,
// and records what it was asked.
type fakeContents struct {
	view messagecontent.View
	// readErr, when set, ends a part's content with this error after
	// content, as a store whose connection drops.
	readErr error
	body    string
	inline  []messagecontent.Part
	part    messagecontent.Part
	content string
	err     error

	userID    int64
	messageID uuid.UUID
	path      []int
	closed    bool
}

func (f *fakeContents) View(_ context.Context, userID int64, messageID uuid.UUID, _ messagecontent.PartURL) (messagecontent.View, error) {
	f.userID, f.messageID = userID, messageID
	return f.view, f.err
}

func (f *fakeContents) Body(_ context.Context, userID int64, messageID uuid.UUID, path []int, partURL messagecontent.PartURL) (messagecontent.HTMLBody, error) {
	f.userID, f.messageID, f.path = userID, messageID, path
	if f.err != nil {
		return messagecontent.HTMLBody{}, f.err
	}
	policy := messagecontent.Policy{Inline: f.inline, PartURL: func(string) string { return partURL([]int{1}) }}
	return messagecontent.HTMLBody{Content: f.closer(f.body), Policy: policy}, nil
}

func (f *fakeContents) Part(_ context.Context, userID int64, messageID uuid.UUID, path []int) (messagecontent.Part, io.ReadCloser, error) {
	f.userID, f.messageID, f.path = userID, messageID, path
	if f.err != nil {
		return messagecontent.Part{}, nil, f.err
	}
	reader := f.closer(f.content)
	if f.readErr != nil {
		reader = closeNoter{Reader: io.MultiReader(reader, iotest.ErrReader(f.readErr)), closed: &f.closed}
	}
	return f.part, reader, nil
}

// closer returns content to read, noting when it is closed.
func (f *fakeContents) closer(content string) io.ReadCloser {
	return closeNoter{Reader: strings.NewReader(content), closed: &f.closed}
}

type closeNoter struct {
	io.Reader
	closed *bool
}

func (c closeNoter) Close() error {
	*c.closed = true
	return nil
}

// contentAPI returns the API reading contents for a signed-in member.
func contentAPI(contents *fakeContents) *API {
	return New(discard, Services{MessageContent: contents, Session: memberSessions(account.RoleMember)})
}

// requestLoaded returns the answer to a GET of path as the browser loads
// it as dest.
func requestLoaded(api *API, path, dest string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v0"+path, nil)
	request.Header.Set("Sec-Fetch-Dest", dest)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// The content lists the bodies, the HTML's URLs, clipped text, and the
// downloads, each named by its place.
func TestGetMessageContent(t *testing.T) {
	contents := &fakeContents{view: messagecontent.View{
		Bodies: []messagecontent.Body{
			{Part: messagecontent.Part{ID: "", MediaType: "text/plain"}, Path: []int{0}, Text: []byte("Hi"), Clipped: true},
			{Part: messagecontent.Part{ID: "1", MediaType: "text/html"}, Path: []int{1}},
		},
		Downloads: []messagecontent.Download{
			{Part: messagecontent.Part{ID: "2", MediaType: "application/pdf", Filename: "report.pdf", Size: 1234}, Path: []int{2, 10}},
		},
		Blocked: true,
	}}
	api := contentAPI(contents)

	response := requestAuthenticated(api, http.MethodGet, "/messages/"+contentMessage.String()+"/content", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	base := "/api/v0/messages/" + contentMessage.String()
	assert.JSONEq(t, `{"data":{
		"bodies":[
			{"partId":"0.0","type":"text","url":"`+base+`/parts/0.0","remoteUrl":null,"text":"Hi","clipped":true},
			{"partId":"0.1","type":"html","url":"`+base+`/bodies/0.1","remoteUrl":"`+base+`/bodies/0.1?remote=allowed","text":null,"clipped":false}
		],
		"remote":"blocked",
		"attachments":[
			{"partId":"0.2.10","filename":"report.pdf","mediaType":"application/pdf","size":1234,"url":"`+base+`/parts/0.2.10"}
		]}}`, response.Body.String())
	assert.Equal(t, int64(42), contents.userID)
	assert.Equal(t, contentMessage, contents.messageID)
}

// A message with nothing to show still answers lists, and remote none.
func TestGetMessageContentEmpty(t *testing.T) {
	api := contentAPI(&fakeContents{})

	response := requestAuthenticated(api, http.MethodGet, "/messages/"+contentMessage.String()+"/content", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"bodies":[],"remote":"none","attachments":[]}}`, response.Body.String())
}

// Each failure answers its own problem; a busy provider says when to try
// again.
func TestGetMessageContentRefused(t *testing.T) {
	for _, c := range []struct {
		name       string
		err        error
		status     int
		typ        string
		retryAfter string
	}{
		{"not the member's", messagecontent.ErrMessageNotFound, http.StatusNotFound, messageNotFoundType, ""},
		{"gone at the provider", messagecontent.ErrGone, http.StatusNotFound, messageNotFoundType, ""},
		{"account gone", connectedaccount.ErrNotFound, http.StatusNotFound, messageNotFoundType, ""},
		{"too large", messagecontent.ErrTooLarge, http.StatusBadGateway, messageUnreadableType, ""},
		{"unreadable", messagecontent.ErrUnreadable, http.StatusBadGateway, messageUnreadableType, ""},
		{"too many parts", messagecontent.ErrTooManyParts, http.StatusBadGateway, messageUnreadableType, ""},
		{"needs reconnect", connectedaccount.ErrNeedsReconnect, http.StatusConflict, accountNeedsReconnectType, ""},
		{"token unreadable", connectedaccount.ErrTokenUnreadable, http.StatusConflict, accountNeedsReconnectType, ""},
		{"provider busy", mailsync.ErrProviderBusy, http.StatusServiceUnavailable, mailProviderBusyType, "30"},
		{"refresh failed", connectedaccount.ErrRefreshFailed, http.StatusServiceUnavailable, mailProviderBusyType, "30"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := contentAPI(&fakeContents{err: c.err})

			response := requestAuthenticated(api, http.MethodGet, "/messages/"+contentMessage.String()+"/content", "")

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"`+c.typ+`"`)
			assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader))
		})
	}
}

// A body is the sanitized document, whose inline images name their part
// URLs; the remote query loads remote images.
func TestGetMessageBody(t *testing.T) {
	contents := &fakeContents{
		body:   `<p>Hi <img src="cid:logo"> <img src="https://tracker.example/p.gif"></p>`,
		inline: []messagecontent.Part{{ID: "1", ContentID: "logo"}},
	}
	api := contentAPI(contents)
	path := "/messages/" + contentMessage.String() + "/bodies/0.1.2"

	response := requestLoaded(api, path, fetchDestIframe)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, []int{1, 2}, contents.path)
	assert.True(t, contents.closed)
	assert.Contains(t, response.Body.String(), `<img src="/api/v0/messages/`+contentMessage.String()+`/parts/0.1">`)
	assert.NotContains(t, response.Body.String(), "tracker.example")
	assert.Contains(t, response.Header().Get("Content-Security-Policy"), bodyImagesBlocked)

	response = requestLoaded(api, path+"?remote=allowed", fetchDestIframe)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "tracker.example")
	assert.Contains(t, response.Header().Get("Content-Security-Policy"), bodyImagesAllowed)
}

// Only a frame loads a body, e.g. never an image in another body.
func TestGetMessageBodyNotFramed(t *testing.T) {
	api := contentAPI(&fakeContents{body: "<p>Hi</p>"})

	response := requestLoaded(api, "/messages/"+contentMessage.String()+"/bodies/0", fetchDestImage)

	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

func TestGetMessageBodyNotFound(t *testing.T) {
	api := contentAPI(&fakeContents{err: messagecontent.ErrPartNotFound})

	response := requestLoaded(api, "/messages/"+contentMessage.String()+"/bodies/0.3", fetchDestIframe)

	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), messagePartNotFoundType)
}

// A part's place must be one a content answer gives.
func TestGetMessagePartBadPlace(t *testing.T) {
	for _, place := range []string{"1", "0.01", "0.", "0.1000", "0.-1", "x"} {
		t.Run(place, func(t *testing.T) {
			contents := &fakeContents{content: "x"}
			api := contentAPI(contents)

			response := requestLoaded(api, "/messages/"+contentMessage.String()+"/parts/"+place, fetchDestImage)

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Zero(t, contents.userID, "not asked")
		})
	}
}

// Only PNG, JPEG, GIF, and WebP images come inline; anything else, an SVG
// included, is a download nothing of runs, and no part is sniffed.
func TestGetMessagePart(t *testing.T) {
	for _, c := range []struct {
		name        string
		part        messagecontent.Part
		contentType string
		disposition string
		policy      string
	}{
		{
			"png", messagecontent.Part{MediaType: "image/png", Filename: "logo.png", Kind: messagecontent.KindInline},
			"image/png", `inline; filename=logo.png`, "",
		},
		{
			"webp without a name", messagecontent.Part{MediaType: "image/webp", Kind: messagecontent.KindInline},
			"image/webp", "inline", "",
		},
		{
			"svg", messagecontent.Part{MediaType: "image/svg+xml", Filename: "logo.svg", Kind: messagecontent.KindInline},
			"image/svg+xml", `attachment; filename=logo.svg`, partPolicy,
		},
		{
			"pdf", messagecontent.Part{MediaType: "application/pdf", Filename: "Café report.pdf", Kind: messagecontent.KindAttachment},
			"application/pdf", `attachment; filename*=utf-8''Caf%C3%A9%20report.pdf`, partPolicy,
		},
		{
			"html file", messagecontent.Part{MediaType: "text/html", Charset: "iso-8859-1", Filename: "page.html", Kind: messagecontent.KindAttachment},
			"text/html; charset=iso-8859-1", `attachment; filename=page.html`, partPolicy,
		},
		{
			"text body", messagecontent.Part{MediaType: "text/plain", Charset: "iso-8859-1", Kind: messagecontent.KindText},
			"text/plain; charset=utf-8", "attachment", partPolicy,
		},
		{
			"bad media type", messagecontent.Part{MediaType: "a b", Kind: messagecontent.KindAttachment},
			partFallbackType, "attachment", partPolicy,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			contents := &fakeContents{part: c.part, content: "bytes"}
			api := contentAPI(contents)

			response := requestLoaded(api, "/messages/"+contentMessage.String()+"/parts/0.4", fetchDestImage)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, "bytes", response.Body.String())
			assert.Equal(t, []int{4}, contents.path)
			assert.True(t, contents.closed)
			assert.Equal(t, c.contentType, response.Header().Get("Content-Type"))
			assert.Equal(t, c.disposition, response.Header().Get("Content-Disposition"))
			assert.Equal(t, c.policy, response.Header().Get("Content-Security-Policy"))
			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

// A part loads as an image, a download, or a page, but never in a frame.
func TestGetMessagePartFetchDest(t *testing.T) {
	part := messagecontent.Part{MediaType: "application/pdf", Kind: messagecontent.KindAttachment}
	for dest, status := range map[string]int{
		fetchDestImage:    http.StatusOK,
		fetchDestDocument: http.StatusOK,
		fetchDestEmpty:    http.StatusOK,
		fetchDestIframe:   http.StatusForbidden,
	} {
		api := contentAPI(&fakeContents{part: part})

		response := requestLoaded(api, "/messages/"+contentMessage.String()+"/parts/0", dest)

		assert.Equal(t, status, response.Code, dest)
	}
}

// A part whose stored content fails midway is cut off, so the browser
// reports a failed download rather than saving its start as the file.
func TestGetMessagePartBroken(t *testing.T) {
	contents := &fakeContents{
		part:    messagecontent.Part{MediaType: "application/pdf", Kind: messagecontent.KindAttachment},
		content: strings.Repeat("%PDF", 1<<12),
		readErr: io.ErrUnexpectedEOF,
	}
	server := httptest.NewServer(contentAPI(contents).Handler())
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v0/messages/"+contentMessage.String()+"/parts/0.1", nil)
	require.NoError(t, err)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})

	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	_, err = io.ReadAll(response.Body)

	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.True(t, contents.closed)
}

// The place of a part as deep as a message's 1000 parts allow is a
// place the routes take.
func TestGetMessagePartDeep(t *testing.T) {
	path := make([]int, 999)
	urls := messageURLs{id: contentMessage}
	for _, c := range []struct{ url, dest string }{
		{urls.part(path), fetchDestImage},
		{urls.body(path, messagecontent.RemoteBlocked), fetchDestIframe},
	} {
		contents := &fakeContents{part: messagecontent.Part{MediaType: "image/png"}, body: "<p>Hi</p>"}

		response := requestLoaded(contentAPI(contents), strings.TrimPrefix(c.url, "/api/v0"), c.dest)

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Len(t, contents.path, 999)
	}
}

func TestPartPathText(t *testing.T) {
	for text, path := range map[string][]int{"0": {}, "0.1": {1}, "0.1.0.12": {1, 0, 12}} {
		assert.Equal(t, text, partPathText(path))
		assert.Equal(t, path, parsePartPath(text))
	}
}
