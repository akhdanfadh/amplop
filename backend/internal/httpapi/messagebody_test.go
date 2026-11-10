package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/messagecontent"
	"inbok/internal/session"
)

// bodyAPI adds a test operation that answers with [messageBody] of body
// under remote, as a frame loads it.
func bodyAPI(body io.Reader, remote messagecontent.Remote) *API {
	a := New(discard, Services{})
	huma.Register(a.api, huma.Operation{
		OperationID: "getTestBody",
		Metadata:    map[string]any{accessKey: session.AccessPublic, fetchDestKey: []string{fetchDestIframe}},
		Method:      http.MethodGet,
		Path:        "/test-body",
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return messageBody(body, messagecontent.Policy{
			Inline:  []messagecontent.Part{{ID: "1.2", ContentID: "logo"}},
			PartURL: func(partID string) string { return "/api/v0/test-parts/" + partID },
			Remote:  remote,
		})
	})
	return a
}

// frameRequest returns a request for the test body as an iframe loads it.
func frameRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v0/test-body", nil)
	request.Header.Set("Sec-Fetch-Dest", fetchDestIframe)
	return request
}

// A body is the sanitized document with its own policy, which the web
// app may frame.
func TestMessageBody(t *testing.T) {
	const html = `<p onclick="steal()">Hi</p><script>steal()</script>` +
		`<img src="cid:logo"><img src="https://tracker.example/p.gif">`
	cases := []struct {
		name   string
		remote messagecontent.Remote
		images string
		body   string
	}{
		{
			"remote blocked", messagecontent.RemoteBlocked, "img-src 'self'",
			`<!DOCTYPE html><p>Hi</p><img src="/api/v0/test-parts/1.2"><img>`,
		},
		{
			"remote allowed", messagecontent.RemoteAllowed, "img-src 'self' http: https:",
			`<!DOCTYPE html><p>Hi</p><img src="/api/v0/test-parts/1.2">` +
				`<img src="https://tracker.example/p.gif">`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			response := httptest.NewRecorder()

			bodyAPI(strings.NewReader(html), c.remote).Handler().ServeHTTP(response, frameRequest())

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, c.body, response.Body.String())
			header := response.Header()
			assert.Equal(t, "text/html; charset=utf-8", header.Get("Content-Type"))
			assert.Equal(t, "sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox; "+
				"default-src 'none'; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'; "+
				"frame-ancestors 'self'; "+c.images, header.Get("Content-Security-Policy"))
			// One value: a second X-Frame-Options would make browsers refuse the frame.
			assert.Equal(t, []string{"SAMEORIGIN"}, header.Values("X-Frame-Options"))
			assert.Equal(t, "nosniff", header.Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", header.Get("Cache-Control"))
		})
	}
}

// A body whose stored copy fails to read is an error answer, which the
// frame refuses, not a message cut short.
func TestMessageBodyReadError(t *testing.T) {
	response := httptest.NewRecorder()

	bodyAPI(iotest.ErrReader(errors.New("store failed")), messagecontent.RemoteBlocked).Handler().
		ServeHTTP(response, frameRequest())

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
	assert.Equal(t, []string{"DENY"}, response.Header().Values("X-Frame-Options"))
}
