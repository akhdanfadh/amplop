package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/session"
)

// fetchDestAPI adds test operations: one with the default destinations,
// one tagged [tagRedirects], one listing an iframe, and one that needs a
// session.
func fetchDestAPI() *API {
	a := New(discard, Services{Session: &fakeSessions{}})
	ok := func(context.Context, *struct{}) (*output[healthStatus], error) {
		return respondData(healthStatus{Status: "ok"}), nil
	}
	public := map[string]any{accessKey: session.AccessPublic}
	huma.Register(a.api, huma.Operation{OperationID: "testDefault", Method: http.MethodGet, Path: "/test-default", Metadata: public}, ok)
	huma.Register(a.api, huma.Operation{OperationID: "testRedirect", Method: http.MethodGet, Path: "/test-redirect", Tags: []string{tagRedirects}, Metadata: public}, ok)
	huma.Register(a.api, huma.Operation{OperationID: "testFrame", Method: http.MethodGet, Path: "/test-frame", Metadata: map[string]any{
		accessKey: session.AccessPublic, fetchDestKey: []string{fetchDestIframe},
	}}, ok)
	huma.Register(a.api, huma.Operation{OperationID: "testMember", Method: http.MethodGet, Path: "/test-member", Metadata: map[string]any{accessKey: session.AccessMember}}, ok)
	return a
}

// An operation answers only the kinds of browser request it lists, so a
// message body's <img> cannot load an API operation as an image.
func TestFetchDest(t *testing.T) {
	cases := []struct {
		path, dest string
		code       int
	}{
		{"/test-default", fetchDestEmpty, http.StatusOK},
		// Not a browser, or one without Fetch Metadata.
		{"/test-default", "", http.StatusOK},
		{"/test-default", fetchDestImage, http.StatusForbidden},
		{"/test-default", fetchDestDocument, http.StatusForbidden},
		{"/test-default", fetchDestIframe, http.StatusForbidden},
		{"/test-redirect", fetchDestDocument, http.StatusOK},
		{"/test-redirect", fetchDestEmpty, http.StatusForbidden},
		{"/test-redirect", fetchDestImage, http.StatusForbidden},
		{"/test-frame", fetchDestIframe, http.StatusOK},
		{"/test-frame", fetchDestEmpty, http.StatusForbidden},
		// Refused before the access check, which would answer 401.
		{"/test-member", fetchDestImage, http.StatusForbidden},
	}
	api := fetchDestAPI()
	for _, c := range cases {
		t.Run(c.path+" "+c.dest, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, apiPrefix+c.path, nil)
			if c.dest != "" {
				request.Header.Set("Sec-Fetch-Dest", c.dest)
			}
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, c.code, response.Code, response.Body.String())
			if c.code == http.StatusForbidden {
				assert.Equal(t, problemPrefix+"fetch-destination-refused", decodeJSON(t, response.Body.Bytes())["type"])
			}
		})
	}
}
