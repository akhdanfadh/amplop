package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/webapp"
)

// fakeWebApp serves /security and /assets/index-BrBO-kCw.js, recording each
// path asked.
type fakeWebApp struct {
	asked []string
}

func (f *fakeWebApp) Serve(w http.ResponseWriter, request *http.Request) bool {
	f.asked = append(f.asked, request.URL.Path)
	if request.URL.Path != "/security" && request.URL.Path != "/assets/index-BrBO-kCw.js" {
		return false
	}
	_, _ = w.Write([]byte("page"))
	return true
}

// newWebAppHandler returns the API's handler after serving app.
func newWebAppHandler(t *testing.T, app webApp) http.Handler {
	t.Helper()
	a := New(discard, Services{})
	a.ServeWebApp(app)
	return a.Handler()
}

// Paths the web app has answer with it; the rest get the API's 404.
func TestWebApp(t *testing.T) {
	cases := []struct {
		path   string
		status int
	}{
		{"/security", http.StatusOK},
		{"/assets/index-BrBO-kCw.js", http.StatusOK},
		{"/favicon.ico", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			response := httptest.NewRecorder()

			newWebAppHandler(t, &fakeWebApp{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, c.path, nil))

			require.Equal(t, c.status, response.Code)
			if c.status == http.StatusNotFound {
				assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			} else {
				assert.Equal(t, "page", response.Body.String())
			}
		})
	}
}

// /api and every path under it stay the API's, never the web app's.
func TestWebAppAPIPaths(t *testing.T) {
	for _, path := range []string{"/api", "/api/", "/api/v0/nope", "/api/v1/health", "//api/v0/health"} {
		t.Run(path, func(t *testing.T) {
			app := &fakeWebApp{}
			response := httptest.NewRecorder()

			newWebAppHandler(t, app).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusNotFound, response.Code)
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			assert.Empty(t, app.asked)
		})
	}
}

// The API's own routes answer as before; paths that only look like the API
// belong to the web app.
func TestWebAppAPI(t *testing.T) {
	app := &fakeWebApp{}
	handler := newWebAppHandler(t, app)
	health := httptest.NewRecorder()
	other := httptest.NewRecorder()

	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v0/health", nil))
	handler.ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/apiary", nil))

	require.Equal(t, http.StatusOK, health.Code)
	assert.JSONEq(t, `{"data": {"status": "ok"}}`, health.Body.String())
	assert.Equal(t, []string{"/apiary"}, app.asked)
}

// The web app's paths only answer reads; the API's paths answer as before.
func TestWebAppMethods(t *testing.T) {
	cases := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodPost, "/security", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, "/assets/index-BrBO-kCw.js", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodDelete, "/favicon.ico", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodOptions, "/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/v0/nope", http.StatusNotFound, ""},
		{http.MethodDelete, "/api/v0/health", http.StatusMethodNotAllowed, "GET"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			app := &fakeWebApp{}
			request := httptest.NewRequest(c.method, c.path, nil)
			// Same origin, so the cross-origin check lets the write reach routing.
			request.Header.Set("Origin", "http://"+request.Host)
			response := httptest.NewRecorder()

			newWebAppHandler(t, app).ServeHTTP(response, request)

			require.Equal(t, c.status, response.Code)
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			assert.Equal(t, c.allow, response.Header().Get("Allow"))
			assert.Empty(t, app.asked)
		})
	}
}

// A write without a browser's source headers is refused before routing, as
// on the API.
func TestWebAppWriteWithoutSource(t *testing.T) {
	response := httptest.NewRecorder()

	newWebAppHandler(t, &fakeWebApp{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/security", nil))

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
}

// The real build answers through Gin as it does alone: a HEAD with its
// headers and no body, a recheck with a bodyless 304.
func TestWebAppBuild(t *testing.T) {
	build, err := webapp.Load(fstest.MapFS{"_shell.html": {Data: []byte("<!DOCTYPE html>")}})
	require.NoError(t, err)
	handler := newWebAppHandler(t, build)
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/security", nil))
	recheck := httptest.NewRequest(http.MethodGet, "/security", nil)
	recheck.Header.Set("If-None-Match", head.Header().Get("ETag"))
	again := httptest.NewRecorder()

	handler.ServeHTTP(again, recheck)

	assert.Equal(t, http.StatusOK, head.Code)
	assert.Equal(t, "15", head.Header().Get("Content-Length"))
	assert.Empty(t, head.Body.String())
	assert.Equal(t, http.StatusNotModified, again.Code)
	assert.Empty(t, again.Body.String())
	assert.NotEmpty(t, again.Header().Get("Content-Security-Policy"))
}
