package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reject cross-site writes before they reach the service or body validation.
func TestCrossOriginRejectsWrite(t *testing.T) {
	writes := &testWrites{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	request := httptest.NewRequest(http.MethodPost, apiPrefix+testWritePath, strings.NewReader(`{"note":"private-note","amount":3,"highlighted":false}`))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Origin", "https://private-origin.example")
	response := httptest.NewRecorder()

	newTestWriteAPI(logger, writes).Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, writes.added)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
	assert.NotEmpty(t, response.Header().Get(requestIDHeader))
	assert.Contains(t, response.Header().Get("Vary"), "Sec-Fetch-Site")
	assert.Contains(t, response.Header().Get("Vary"), "Origin")
	assert.JSONEq(t, `{"type":"/api/v0/problems/cross-origin-request","status":403,"title":"Cross-origin request blocked","detail":"Send this request from the amplop site."}`, response.Body.String())
	assert.Contains(t, logs.String(), "status=403")
	for _, secret := range []string{"private-note", "private-origin.example"} {
		assert.NotContains(t, response.Body.String(), secret)
		assert.NotContains(t, logs.String(), secret)
	}
}

func TestCrossOrigin(t *testing.T) {
	cases := []struct {
		name, site, origin string
		status             int
	}{
		{"same origin", "same-origin", "http://amplop.example", http.StatusCreated},
		{"same origin metadata only", "same-origin", "", http.StatusCreated},
		{"same site", "same-site", "http://sibling.amplop.example", http.StatusForbidden},
		{"cross site", "cross-site", "http://outside.example", http.StatusForbidden},
		{"metadata takes priority", "cross-site", "http://amplop.example", http.StatusForbidden},
		{"direct navigation", "none", "", http.StatusCreated},
		{"unknown metadata", "unknown", "http://amplop.example", http.StatusForbidden},
		{"matching origin fallback", "", "http://amplop.example", http.StatusCreated},
		{"foreign origin fallback", "", "http://outside.example", http.StatusForbidden},
		{"different port", "", "http://amplop.example:8080", http.StatusForbidden},
		{"null origin", "", "null", http.StatusForbidden},
		{"invalid origin", "", "://invalid", http.StatusForbidden},
		{"missing source headers", "", "", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writes := &testWrites{}
			request := httptest.NewRequest(http.MethodPost, "http://amplop.example"+apiPrefix+testWritePath, strings.NewReader(`{"note":"hello","amount":3,"highlighted":false}`))
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("Sec-Fetch-Site", c.site)
			request.Header.Set("Origin", c.origin)
			response := httptest.NewRecorder()

			newTestWriteAPI(discard, writes).Handler().ServeHTTP(response, request)

			require.Equal(t, c.status, response.Code, response.Body.String())
			if c.status == http.StatusForbidden {
				assert.Empty(t, writes.added)
				assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/cross-origin-request"`)
				return
			}
			assert.Len(t, writes.added, 1)
		})
	}
}

func TestCrossOriginMethods(t *testing.T) {
	cases := []struct {
		method string
		status int
	}{
		{http.MethodGet, http.StatusOK},
		{http.MethodHead, http.StatusOK},
		{http.MethodOptions, http.StatusOK},
		{http.MethodPost, http.StatusForbidden},
		{http.MethodPut, http.StatusForbidden},
		{http.MethodPatch, http.StatusForbidden},
		{http.MethodDelete, http.StatusForbidden},
	}
	for _, source := range []struct{ name, site string }{{"cross site", "cross-site"}, {"missing source", ""}} {
		for _, c := range cases {
			t.Run(source.name+"/"+c.method, func(t *testing.T) {
				a := New(discard, Services{})
				var called bool
				a.router.Handle(c.method, "/test-cross-origin", func(ctx *gin.Context) {
					called = true
					ctx.Status(http.StatusOK)
				})
				request := httptest.NewRequest(c.method, "/test-cross-origin", nil)
				request.Header.Set("Sec-Fetch-Site", source.site)
				response := httptest.NewRecorder()

				a.Handler().ServeHTTP(response, request)

				assert.Equal(t, c.status, response.Code)
				assert.Equal(t, c.status == http.StatusOK, called)
			})
		}
	}
}

func TestCrossOriginSetup(t *testing.T) {
	cases := []struct {
		name, body, site string
	}{
		{"cross site valid body", setupBody(t, setupFields()), "cross-site"},
		{"cross site malformed body", "{", "cross-site"},
		{"missing source valid body", setupBody(t, setupFields()), ""},
		{"missing source malformed body", "{", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{}
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(c.body))
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("Sec-Fetch-Site", c.site)
			response := httptest.NewRecorder()

			New(discard, Services{Account: accounts}).Handler().ServeHTTP(response, request)

			require.Equal(t, http.StatusForbidden, response.Code)
			assert.Empty(t, accounts.created)
			assert.NotContains(t, response.Body.String(), setupHTTPToken)
		})
	}
}
